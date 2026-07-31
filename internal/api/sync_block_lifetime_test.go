package api

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
)

type lifetimeBlockingBody struct {
	started chan struct{}
	closed  chan struct{}
	once    sync.Once
}

func (b *lifetimeBlockingBody) Read([]byte) (int, error) {
	b.once.Do(func() { close(b.started) })
	<-b.closed
	return 0, errors.New("body closed")
}

func (b *lifetimeBlockingBody) Close() error {
	select {
	case <-b.closed:
	default:
		close(b.closed)
	}
	return nil
}

func TestPutBlockAdmittedLifetimeClosesStalledBody(t *testing.T) {
	cfg := syncInflightConfig(1, 1, 5*time.Second)
	cfg.SeafHTTP.SyncBlockAdmittedLifetime = 50 * time.Millisecond
	h := newInflightTestHandler(t, cfg)
	r := putBlockRouterFor(h, "00000000-0000-0000-0000-000000000001", "user")
	body := &lifetimeBlockingBody{started: make(chan struct{}), closed: make(chan struct{})}
	req := httptest.NewRequest(http.MethodPut, "/seafhttp/repo/repo/block/"+inflightTestBlockID, body)
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		r.ServeHTTP(w, req)
		close(done)
	}()
	select {
	case <-body.started:
	case <-time.After(time.Second):
		t.Fatal("body read never started")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("admitted lifetime did not close and unblock the request body")
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	if got := w.Header().Get("Retry-After"); got == "" {
		t.Fatal("lifetime timeout 503 has no Retry-After")
	}
	if err := requireDrainedLimiter(h.blockInflight); err != nil {
		t.Fatal(err)
	}
}

func TestPutBlockAdmittedLifetimePropagatesToStorage(t *testing.T) {
	oldExists := syncBlockExistsFn
	oldPut := syncPutBlockDataFn
	t.Cleanup(func() {
		syncBlockExistsFn = oldExists
		syncPutBlockDataFn = oldPut
	})

	contextSeen := make(chan struct{}, 1)
	syncBlockExistsFn = func(ctx context.Context, _ *storage.BlockStore, _ string) (bool, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("storage context has no admitted-lifetime deadline")
		}
		contextSeen <- struct{}{}
		<-ctx.Done()
		return false, ctx.Err()
	}
	syncPutBlockDataFn = func(context.Context, *storage.BlockStore, *storage.BlockData) (string, error) {
		t.Fatal("put must not run after the existence check times out")
		return "", nil
	}

	cfg := syncInflightConfig(1, 1, 5*time.Second)
	cfg.SeafHTTP.SyncBlockAdmittedLifetime = 50 * time.Millisecond
	h := newInflightTestHandler(t, cfg)
	h.storage = &storage.S3Store{}
	r := putBlockRouterFor(h, "00000000-0000-0000-0000-000000000001", "user")
	req := httptest.NewRequest(http.MethodPut, "/seafhttp/repo/repo/block/0123456789012345678901234567890123456789", strings.NewReader("hello"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	select {
	case <-contextSeen:
	default:
		t.Fatal("storage operation was not called")
	}
	if w.Code == http.StatusTooManyRequests {
		t.Fatal("storage timeout returned 429; sync clients require 503")
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	if got := w.Header().Get("Retry-After"); got == "" {
		t.Fatal("storage timeout 503 has no Retry-After")
	}
	if err := requireDrainedLimiter(h.blockInflight); err != nil {
		t.Fatal(err)
	}
}

func TestPutBlockParentDeadlineIsNotReportedAsAdmittedLifetime(t *testing.T) {
	cfg := syncInflightConfig(1, 1, 5*time.Second)
	cfg.SeafHTTP.SyncBlockAdmittedLifetime = time.Second
	h := newInflightTestHandler(t, cfg)
	r := putBlockRouterFor(h, "00000000-0000-0000-0000-000000000001", "user")
	body := &lifetimeBlockingBody{started: make(chan struct{}), closed: make(chan struct{})}
	parent, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodPut, "/seafhttp/repo/repo/block/"+inflightTestBlockID, body).WithContext(parent)
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		r.ServeHTTP(w, req)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("parent deadline did not stop the admitted request")
	}
	if w.Code == http.StatusServiceUnavailable {
		t.Fatal("parent deadline was reported as a server admitted-lifetime timeout")
	}
	if got := w.Header().Get("Retry-After"); got != "" {
		t.Fatalf("parent deadline returned Retry-After %q", got)
	}
	if err := requireDrainedLimiter(h.blockInflight); err != nil {
		t.Fatal(err)
	}
}

func TestPutBlockIndependentStorageDeadlineReturnsRetryableError(t *testing.T) {
	oldExists := syncBlockExistsFn
	t.Cleanup(func() { syncBlockExistsFn = oldExists })
	for _, backendErr := range []error{context.DeadlineExceeded, os.ErrDeadlineExceeded} {
		t.Run(backendErr.Error(), func(t *testing.T) {
			syncBlockExistsFn = func(context.Context, *storage.BlockStore, string) (bool, error) {
				return false, backendErr
			}

			cfg := syncInflightConfig(1, 1, 5*time.Second)
			h := newInflightTestHandler(t, cfg)
			h.storage = &storage.S3Store{}
			r := putBlockRouterFor(h, "00000000-0000-0000-0000-000000000001", "user")
			req := httptest.NewRequest(http.MethodPut, "/seafhttp/repo/repo/block/0123456789012345678901234567890123456789", strings.NewReader("hello"))
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503", w.Code)
			}
			if got := w.Header().Get("Retry-After"); got == "" {
				t.Fatal("storage deadline 503 has no Retry-After")
			}
			if got := w.Header().Get("Connection"); got != "" {
				t.Fatalf("independent storage deadline forced connection close: %q", got)
			}
			if !strings.Contains(w.Body.String(), "storage") {
				t.Fatalf("independent backend deadline misclassified: %q", w.Body.String())
			}
			if err := requireDrainedLimiter(h.blockInflight); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// Keep io imported as a compile-time assertion that the test body implements
// the exact request-body contract used by net/http.
var _ io.ReadCloser = (*lifetimeBlockingBody)(nil)

// requireEventuallyDrained polls, because the response is written by the handler
// before its deferred release runs; asserting immediately would race the
// bookkeeping rather than the property.
func requireEventuallyDrained(t *testing.T, l *syncBlockInflightLimiter) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var err error
	for time.Now().Before(deadline) {
		if err = requireDrainedLimiter(l); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(err)
}

func requireEventuallyNodeInflight(t *testing.T, l *syncBlockInflightLimiter, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(l.node) == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("node inflight = %d, want %d", len(l.node), want)
}

// TestPutBlockAdmittedLifetimeInterruptsStalledConnectionBody is the closure
// test for the admitted lifetime, and it deliberately uses a real TCP
// connection instead of a fake body.
//
// The fake-body tests above cannot prove this property, and for a while hid its
// absence: their Close() unblocks their Read(). net/http's real body does the
// opposite — Read and Close share one mutex, so a handler parked in Read holds
// it and a Close from a timer goroutine queues behind it instead of
// interrupting it. Cancelling the context alone therefore never freed a stalled
// upload, and with server.read_timeout deliberately 0 nothing else would: the
// admission was held for as long as the peer cared to stay silent, which turns
// a handful of authenticated but stalled connections into a node-wide denial of
// block upload. Only a deadline on the connection breaks that, so only a test
// that owns a connection can show it does.
func TestPutBlockAdmittedLifetimeInterruptsStalledConnectionBody(t *testing.T) {
	cfg := syncInflightConfig(1, 1, 5*time.Second)
	cfg.SeafHTTP.SyncBlockAdmittedLifetime = 300 * time.Millisecond
	h := newInflightTestHandler(t, cfg)
	r := putBlockRouterFor(h, "00000000-0000-0000-0000-000000000001", "user")

	srv := httptest.NewUnstartedServer(r)
	// Mirror the shipped server config: no whole-body read timeout, because
	// large uploads legitimately take minutes. That is exactly what makes the
	// admitted lifetime the only bound on a stalled read.
	srv.Config.ReadTimeout = 0
	srv.Config.ReadHeaderTimeout = 10 * time.Second
	srv.Start()
	defer srv.Close()

	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Announce a body, send part of it, then go silent forever — a stalled
	// uploader, not a disconnected one. The connection stays open, so nothing
	// below the handler can notice.
	if _, err := fmt.Fprintf(conn, "PUT /seafhttp/repo/repo/block/%s HTTP/1.1\r\nHost: sesamefs\r\nContent-Length: 4096\r\n\r\npartial", inflightTestBlockID); err != nil {
		t.Fatalf("write request: %v", err)
	}

	if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatalf("set client read deadline: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("stalled upload was never answered; the admitted lifetime did not reach the connection: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	if got := resp.Header.Get("Retry-After"); got == "" {
		t.Fatal("stalled-upload 503 has no Retry-After")
	}
	requireEventuallyDrained(t, h.blockInflight)
}

func TestPutBlockEffectiveParentDeadlineInterruptsRealBodyRead(t *testing.T) {
	cfg := syncInflightConfig(1, 1, 5*time.Second)
	cfg.SeafHTTP.SyncBlockAdmittedLifetime = 2 * time.Second
	h := newInflightTestHandler(t, cfg)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 150*time.Millisecond)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Set("org_id", "00000000-0000-0000-0000-000000000001")
		c.Set("user_id", "user")
		c.Next()
	})
	r.PUT("/seafhttp/repo/:repo_id/block/:block_id", h.PutBlock)

	srv := httptest.NewServer(r)
	defer srv.Close()
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	started := time.Now()
	if _, err := fmt.Fprintf(conn, "PUT /seafhttp/repo/repo/block/%s HTTP/1.1\r\nHost: sesamefs\r\nContent-Length: 4096\r\n\r\npartial", inflightTestBlockID); err != nil {
		t.Fatalf("write request: %v", err)
	}
	requireEventuallyNodeInflight(t, h.blockInflight, 1)
	requireEventuallyDrained(t, h.blockInflight)
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("parent deadline took %s to release admission; socket kept the later admitted deadline", elapsed)
	}
}

func TestPutBlockDoesNotOverrideEarlierServerReadTimeout(t *testing.T) {
	cfg := syncInflightConfig(1, 1, 5*time.Second)
	cfg.SeafHTTP.SyncBlockAdmittedLifetime = 2 * time.Second
	cfg.Server.ReadTimeout = 150 * time.Millisecond
	h := newInflightTestHandler(t, cfg)
	r := putBlockRouterFor(h, "00000000-0000-0000-0000-000000000001", "user")

	srv := httptest.NewUnstartedServer(r)
	srv.Config.ReadTimeout = cfg.Server.ReadTimeout
	srv.Start()
	defer srv.Close()
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	started := time.Now()
	if _, err := fmt.Fprintf(conn, "PUT /seafhttp/repo/repo/block/%s HTTP/1.1\r\nHost: sesamefs\r\nContent-Length: 4096\r\n\r\npartial", inflightTestBlockID); err != nil {
		t.Fatalf("write request: %v", err)
	}
	requireEventuallyNodeInflight(t, h.blockInflight, 1)
	requireEventuallyDrained(t, h.blockInflight)
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("server read timeout took %s; admitted lifetime overwrote the earlier socket deadline", elapsed)
	}
}

func TestPutBlockParentDeadlineShortensConfiguredServerReadTimeout(t *testing.T) {
	cfg := syncInflightConfig(1, 1, 5*time.Second)
	cfg.SeafHTTP.SyncBlockAdmittedLifetime = 2 * time.Second
	cfg.Server.ReadTimeout = time.Second
	h := newInflightTestHandler(t, cfg)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 150*time.Millisecond)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Set("org_id", "00000000-0000-0000-0000-000000000001")
		c.Set("user_id", "user")
		c.Next()
	})
	r.PUT("/seafhttp/repo/:repo_id/block/:block_id", h.PutBlock)

	srv := httptest.NewUnstartedServer(r)
	srv.Config.ReadTimeout = cfg.Server.ReadTimeout
	srv.Start()
	defer srv.Close()
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	started := time.Now()
	if _, err := fmt.Fprintf(conn, "PUT /seafhttp/repo/repo/block/%s HTTP/1.1\r\nHost: sesamefs\r\nContent-Length: 4096\r\n\r\npartial", inflightTestBlockID); err != nil {
		t.Fatalf("write request: %v", err)
	}
	requireEventuallyNodeInflight(t, h.blockInflight, 1)
	requireEventuallyDrained(t, h.blockInflight)
	if elapsed := time.Since(started); elapsed >= 500*time.Millisecond {
		t.Fatalf("parent deadline took %s; configured server timeout was not shortened", elapsed)
	}
}

func TestPutBlockReadDeadlineDoesNotLeakAcrossKeepAliveRequests(t *testing.T) {
	oldExists := syncBlockExistsFn
	t.Cleanup(func() { syncBlockExistsFn = oldExists })
	syncBlockExistsFn = func(context.Context, *storage.BlockStore, string) (bool, error) {
		return true, nil
	}

	cfg := syncInflightConfig(1, 1, 5*time.Second)
	cfg.SeafHTTP.SyncBlockAdmittedLifetime = 100 * time.Millisecond
	h := newInflightTestHandler(t, cfg)
	h.storage = &storage.S3Store{}
	r := putBlockRouterFor(h, "00000000-0000-0000-0000-000000000001", "user")
	srv := httptest.NewServer(r)
	defer srv.Close()

	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	request := fmt.Sprintf("PUT /seafhttp/repo/repo/block/0123456789012345678901234567890123456789 HTTP/1.1\r\nHost: sesamefs\r\nContent-Length: 5\r\n\r\nhello")

	for i := 0; i < 2; i++ {
		if i == 1 {
			// The first request's admitted deadline is now in the past. net/http
			// must have rearmed the connection before accepting this request.
			time.Sleep(150 * time.Millisecond)
		}
		if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
			t.Fatalf("set client deadline: %v", err)
		}
		if _, err := io.WriteString(conn, request); err != nil {
			t.Fatalf("write request %d: %v", i+1, err)
		}
		resp, err := http.ReadResponse(reader, nil)
		if err != nil {
			t.Fatalf("read response %d: admitted deadline leaked across keep-alive: %v", i+1, err)
		}
		_, readErr := io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if readErr != nil {
			t.Fatalf("read response body %d: %v", i+1, readErr)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("response %d status = %d, want 200", i+1, resp.StatusCode)
		}
	}
}

// TestPutBlockTimeoutIsNotCountedAsAReadError pins the two counters apart. A
// lifetime timeout already moves sync_put_block_timeouts_total; folding it into
// sync_put_block_rejected_total{reason="read_error"} as well would make the
// size-cap-versus-malformed-body dial climb during ordinary timeouts, which is
// precisely when an operator reads it.
func TestPutBlockTimeoutIsNotCountedAsAReadError(t *testing.T) {
	cfg := syncInflightConfig(1, 1, 5*time.Second)
	cfg.SeafHTTP.SyncBlockAdmittedLifetime = 50 * time.Millisecond
	h := newInflightTestHandler(t, cfg)
	r := putBlockRouterFor(h, "00000000-0000-0000-0000-000000000001", "user")

	readErrorsBefore := testutil.ToFloat64(metrics.SyncPutBlockRejectedTotal.WithLabelValues("read_error"))
	timeoutsBefore := testutil.ToFloat64(metrics.SyncPutBlockTimeoutsTotal.WithLabelValues("body"))

	body := &lifetimeBlockingBody{started: make(chan struct{}), closed: make(chan struct{})}
	req := httptest.NewRequest(http.MethodPut, "/seafhttp/repo/repo/block/"+inflightTestBlockID, body)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	if got := testutil.ToFloat64(metrics.SyncPutBlockTimeoutsTotal.WithLabelValues("body")); got != timeoutsBefore+1 {
		t.Fatalf("body timeouts = %v, want %v", got, timeoutsBefore+1)
	}
	if got := testutil.ToFloat64(metrics.SyncPutBlockRejectedTotal.WithLabelValues("read_error")); got != readErrorsBefore {
		t.Fatalf("a lifetime timeout was also counted as read_error: %v, want %v", got, readErrorsBefore)
	}
}

// TestPutBlockDoesNotFailCompletedWorkOnLifetimeExpiry covers the last phase:
// once the bytes are stored there is nothing left for a timeout to prevent, so
// the deadline must not turn finished work into a 503 and buy a redundant
// re-upload of the whole block. Earlier phases keep failing closed — that is the
// GC-race reconfirmation and it is tested separately.
func TestPutBlockDoesNotFailCompletedStorageWorkOnLifetimeExpiry(t *testing.T) {
	oldExists := syncBlockExistsFn
	t.Cleanup(func() { syncBlockExistsFn = oldExists })
	// Succeeds, but only after the admitted lifetime has already expired.
	syncBlockExistsFn = func(context.Context, *storage.BlockStore, string) (bool, error) {
		time.Sleep(120 * time.Millisecond)
		return true, nil
	}

	cfg := syncInflightConfig(1, 1, 5*time.Second)
	cfg.SeafHTTP.SyncBlockAdmittedLifetime = 50 * time.Millisecond
	h := newInflightTestHandler(t, cfg)
	h.storage = &storage.S3Store{}
	r := putBlockRouterFor(h, "00000000-0000-0000-0000-000000000001", "user")
	req := httptest.NewRequest(http.MethodPut, "/seafhttp/repo/repo/block/0123456789012345678901234567890123456789", strings.NewReader("hello"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: an expired deadline failed work that had already completed", w.Code)
	}
	requireEventuallyDrained(t, h.blockInflight)
}

func TestPutBlockRegisterSuccessStillRunsReconfirmationAfterLifetimeExpiry(t *testing.T) {
	oldProbe := syncProbeUploadedBlockReuseFn
	oldPrepare := syncPrepareUploadedBlockProbeFn
	oldResolve := syncResolveNeedsPutBlockStoreFn
	oldPut := syncPutBlockAutoDirectFn
	oldRegister := registerUploadedBlockAndMappingForSyncFn
	oldRetry := syncRetryUploadedBlockMaterializationFn
	oldLookupClass := lookupLibraryStorageClassForSyncFn
	t.Cleanup(func() {
		syncProbeUploadedBlockReuseFn = oldProbe
		syncPrepareUploadedBlockProbeFn = oldPrepare
		syncResolveNeedsPutBlockStoreFn = oldResolve
		syncPutBlockAutoDirectFn = oldPut
		registerUploadedBlockAndMappingForSyncFn = oldRegister
		syncRetryUploadedBlockMaterializationFn = oldRetry
		lookupLibraryStorageClassForSyncFn = oldLookupClass
	})
	lookupLibraryStorageClassForSyncFn = func(*SyncHandler, string, string) string { return "hot" }

	syncProbeUploadedBlockReuseFn = func(*db.DB, string, string) (db.BlockReuseProbe, error) {
		return db.BlockReuseProbe{Decision: db.BlockReuseNeedsPut}, nil
	}
	syncPrepareUploadedBlockProbeFn = func(_ *db.DB, _, _ string, probe db.BlockReuseProbe) (db.BlockReuseProbe, error) {
		return probe, nil
	}
	syncResolveNeedsPutBlockStoreFn = func(_ *storage.Manager, preferred *storage.BlockStore, class string, _ db.BlockReuseProbe, _, _ string) (*storage.BlockStore, string, string, error) {
		return preferred, class, "key", nil
	}
	syncPutBlockAutoDirectFn = func(context.Context, *storage.BlockStore, string, []byte) (string, error) {
		return "key", nil
	}
	registerCalled := false
	registerUploadedBlockAndMappingForSyncFn = func(*db.DB, string, string, string, string, int, string, string, string) error {
		registerCalled = true
		time.Sleep(120 * time.Millisecond)
		return nil
	}
	secondStoreCalled := false
	syncRetryUploadedBlockMaterializationFn = func(_ context.Context, _, _ string, store, materialize func() error, _ func(), _ func() (bool, error)) error {
		if err := store(); err != nil {
			return err
		}
		if err := materialize(); err != nil {
			return err
		}
		secondStoreCalled = true
		return store()
	}

	cfg := syncInflightConfig(1, 1, 5*time.Second)
	cfg.SeafHTTP.SyncBlockAdmittedLifetime = 50 * time.Millisecond
	h := newInflightTestHandler(t, cfg)
	h.db = &db.DB{}
	h.storage = &storage.S3Store{}
	r := putBlockRouterFor(h, "00000000-0000-0000-0000-000000000001", "user")
	req := httptest.NewRequest(http.MethodPut, "/seafhttp/repo/repo/block/0123456789012345678901234567890123456789", strings.NewReader("hello"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if !registerCalled {
		t.Fatal("materialization register was not called")
	}
	if !secondStoreCalled {
		t.Fatal("successful register returned the expired context and skipped GC reconfirmation")
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 after reconfirmation observes the expired lifetime", w.Code)
	}
	if err := requireDrainedLimiter(h.blockInflight); err != nil {
		t.Fatal(err)
	}
}
