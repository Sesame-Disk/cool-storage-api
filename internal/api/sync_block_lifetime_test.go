package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

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
	syncBlockExistsFn = func(context.Context, *storage.BlockStore, string) (bool, error) {
		return false, context.DeadlineExceeded
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
	if err := requireDrainedLimiter(h.blockInflight); err != nil {
		t.Fatal(err)
	}
}

// Keep io imported as a compile-time assertion that the test body implements
// the exact request-body contract used by net/http.
var _ io.ReadCloser = (*lifetimeBlockingBody)(nil)
