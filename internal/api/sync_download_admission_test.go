package api

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/downloadadmission"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/Sesame-Disk/sesamefs/internal/streaming"
	gingzip "github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type syncBlockDeadlineResponseWriter struct {
	gin.ResponseWriter
}

func (w *syncBlockDeadlineResponseWriter) SetWriteDeadline(time.Time) error {
	return nil
}

type syncBlockPartialResponseWriter struct {
	gin.ResponseWriter
	limit int
	wrote int
}

func (w *syncBlockPartialResponseWriter) SetWriteDeadline(time.Time) error {
	return nil
}

func (w *syncBlockPartialResponseWriter) Write(p []byte) (int, error) {
	remaining := w.limit - w.wrote
	if remaining <= 0 {
		return 0, errors.New("simulated client write failure")
	}
	if len(p) > remaining {
		n, _ := w.ResponseWriter.Write(p[:remaining])
		w.wrote += n
		return n, errors.New("simulated client write failure")
	}
	n, err := w.ResponseWriter.Write(p)
	w.wrote += n
	return n, err
}

func syncBlockAdmissionConfig() config.DownloadAdmissionConfig {
	return config.DownloadAdmissionConfig{
		Enabled:                true,
		MaxActivePerNode:       4,
		MaxActivePerAuthUser:   4,
		MaxActivePerLinkSource: 1,
		MaxActivePerClientLink: 1,
		MaxWaitersPerIdentity:  4,
		MaxWaitersPerNode:      4,
		PreparationDeadline:    time.Minute,
		IdleWriteTimeout:       time.Minute,
		RetryAfter:             2 * time.Second,
		MaxActiveBlock:         1,
	}
}

func newSyncBlockAdmissionHandler(t *testing.T, cfg config.DownloadAdmissionConfig) (*SyncHandler, *downloadadmission.Coordinator) {
	t.Helper()
	coordinator, err := downloadadmission.New(&cfg)
	if err != nil {
		t.Fatalf("new download admission coordinator: %v", err)
	}
	return &SyncHandler{
		config:            &config.Config{DownloadAdmission: cfg},
		db:                &db.DB{},
		storageManager:    storage.NewManager(),
		downloadAdmission: coordinator,
	}, coordinator
}

func TestSyncBlockDownloadAdmissionDisabledIsTransparent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &SyncHandler{config: &config.Config{}}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/seafhttp/repo/repo/block/"+strings.Repeat("a", 64), nil)
	requestBefore := c.Request
	writerBefore := c.Writer

	lifecycle, ok := h.acquireSyncBlockDownloadAdmission(c, "org-1", "user-1")
	if !ok || lifecycle == nil {
		t.Fatal("disabled sync block admission did not return a lifecycle")
	}
	if c.Request != requestBefore || c.Writer != writerBefore {
		t.Fatal("disabled sync block admission changed the request or response writer")
	}
	if err := lifecycle.Finish(downloadadmission.ReleaseCompleted); err != nil {
		t.Fatalf("finish disabled lifecycle: %v", err)
	}
}

func TestSyncBlockDownloadAdmissionRefusesSaturatedProfile(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h, coordinator := newSyncBlockAdmissionHandler(t, syncBlockAdmissionConfig())
	holderRequest, err := downloadadmission.NewAuthenticatedRequest(downloadadmission.ProfileBlock, "org-hold", "holder-user")
	if err != nil {
		t.Fatal(err)
	}
	holder, reason := coordinator.Acquire(context.Background(), holderRequest)
	if holder == nil || reason != "" {
		t.Fatalf("hold block profile = (%v, %q)", holder, reason)
	}
	defer holder.Release(downloadadmission.ReleaseCompleted)

	constructorCalls := 0
	old := syncNewCanonicalBlockReaderFn
	t.Cleanup(func() { syncNewCanonicalBlockReaderFn = old })
	syncNewCanonicalBlockReaderFn = func(context.Context, *db.DB, *storage.Manager, string, []string, *storage.BlockStore, string) (streaming.CanonicalBlockReader, error) {
		constructorCalls++
		return nil, errors.New("reader must not be constructed after admission refusal")
	}

	r := setupSyncTestRouter()
	r.GET("/seafhttp/repo/:repo_id/block/:block_id", h.GetBlock)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/seafhttp/repo/repo/block/"+strings.Repeat("a", 64), nil))

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Retry-After"); got != "2" {
		t.Fatalf("Retry-After = %q, want 2", got)
	}
	if constructorCalls != 0 {
		t.Fatalf("canonical constructor calls = %d, want 0 before admission refusal", constructorCalls)
	}
}

func TestSyncBlockDownloadAdmissionCheapRejectsSkipSlots(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h, coordinator := newSyncBlockAdmissionHandler(t, syncBlockAdmissionConfig())
	beforeActive := testutil.ToFloat64(metrics.DownloadAdmissionActiveCurrent)

	r := setupSyncTestRouter()
	r.GET("/seafhttp/repo/:repo_id/block/:block_id", h.GetBlock)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/seafhttp/repo/repo/block/not-a-block-id", nil))

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
	}
	if got := testutil.ToFloat64(metrics.DownloadAdmissionActiveCurrent); got != beforeActive {
		t.Fatalf("active admissions = %v, want %v after cheap reject", got, beforeActive)
	}
	_ = coordinator
}

func TestSyncBlockDownloadAdmissionStreamsWithoutMaterializing(t *testing.T) {
	gin.SetMode(gin.TestMode)

	blockID := strings.Repeat("b", 64)
	payload := []byte("streamed-block-bytes")
	h, _ := newSyncBlockAdmissionHandler(t, syncBlockAdmissionConfig())

	var stub *syncCanonicalReaderStub
	var touchAfterSize bool
	var sizeSeen, touchSeen, readerSeen bool
	var order []string
	var mu sync.Mutex
	record := func(step string) {
		mu.Lock()
		defer mu.Unlock()
		order = append(order, step)
	}

	oldReader := syncNewCanonicalBlockReaderFn
	oldTouch := syncTouchBlockLastAccessFn
	t.Cleanup(func() {
		syncNewCanonicalBlockReaderFn = oldReader
		syncTouchBlockLastAccessFn = oldTouch
	})
	syncTouchBlockLastAccessFn = func(*db.DB, string, string, time.Time) {
		touchSeen = true
		touchAfterSize = sizeSeen && !readerSeen
		record("touch")
	}
	syncNewCanonicalBlockReaderFn = func(ctx context.Context, _ *db.DB, _ *storage.Manager, _ string, blockIDs []string, _ *storage.BlockStore, _ string) (streaming.CanonicalBlockReader, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("canonical construction must use the preparation deadline context")
		}
		stub = &syncCanonicalReaderStub{data: map[string][]byte{blockIDs[0]: payload}}
		return &countingCanonicalReader{
			inner: stub,
			onSize: func() {
				sizeSeen = true
				record("size")
			},
			onReader: func() {
				readerSeen = true
				record("reader")
			},
		}, nil
	}

	recorder := httptest.NewRecorder()
	c, engine := gin.CreateTestContext(recorder)
	_ = engine
	r := setupSyncTestRouter()
	r.GET("/seafhttp/repo/:repo_id/block/:block_id", func(gc *gin.Context) {
		gc.Writer = &syncBlockDeadlineResponseWriter{ResponseWriter: gc.Writer}
		h.GetBlock(gc)
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/seafhttp/repo/repo/block/"+blockID, nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if w.Body.String() != string(payload) {
		t.Fatalf("body = %q, want %q", w.Body.String(), string(payload))
	}
	if got := w.Header().Get("Content-Length"); got != "20" {
		t.Fatalf("Content-Length = %q, want 20", got)
	}
	if stub == nil || stub.getBlockCalls != 0 {
		t.Fatalf("buffered GetBlock calls = %d, want 0", stub.getBlockCalls)
	}
	if !touchSeen || !touchAfterSize {
		t.Fatalf("last_accessed ordering failed: touchSeen=%v touchAfterSize=%v order=%v", touchSeen, touchAfterSize, order)
	}
	if strings.Join(order, ",") != "size,touch,reader" {
		t.Fatalf("step order = %v, want size,touch,reader", order)
	}
	_ = c
}

func TestSyncBlockDownloadAdmissionPartialWriteUsesResponseBytes(t *testing.T) {
	gin.SetMode(gin.TestMode)

	blockID := strings.Repeat("c", 64)
	payload := []byte("0123456789abcdef")
	h, coordinator := newSyncBlockAdmissionHandler(t, syncBlockAdmissionConfig())

	oldReader := syncNewCanonicalBlockReaderFn
	oldTouch := syncTouchBlockLastAccessFn
	t.Cleanup(func() {
		syncNewCanonicalBlockReaderFn = oldReader
		syncTouchBlockLastAccessFn = oldTouch
	})
	syncTouchBlockLastAccessFn = func(*db.DB, string, string, time.Time) {}
	syncNewCanonicalBlockReaderFn = func(context.Context, *db.DB, *storage.Manager, string, []string, *storage.BlockStore, string) (streaming.CanonicalBlockReader, error) {
		return &syncCanonicalReaderStub{data: map[string][]byte{blockID: payload}}, nil
	}

	beforeResponse := testutil.ToFloat64(metrics.DownloadAdmissionReleasedTotal.WithLabelValues(string(downloadadmission.ReleaseResponseError)))
	beforeStorage := testutil.ToFloat64(metrics.DownloadAdmissionReleasedTotal.WithLabelValues(string(downloadadmission.ReleaseStorageError)))
	beforeCompleted := testutil.ToFloat64(metrics.DownloadAdmissionReleasedTotal.WithLabelValues(string(downloadadmission.ReleaseCompleted)))

	r := setupSyncTestRouter()
	r.GET("/seafhttp/repo/:repo_id/block/:block_id", func(gc *gin.Context) {
		gc.Writer = &syncBlockPartialResponseWriter{ResponseWriter: gc.Writer, limit: 4}
		h.GetBlock(gc)
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/seafhttp/repo/repo/block/"+blockID, nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 after headers committed; body=%s", w.Code, w.Body.String())
	}
	if got := w.Body.Len(); got != 4 {
		t.Fatalf("partial body length = %d, want 4", got)
	}

	releasedResponse := testutil.ToFloat64(metrics.DownloadAdmissionReleasedTotal.WithLabelValues(string(downloadadmission.ReleaseResponseError)))
	releasedStorage := testutil.ToFloat64(metrics.DownloadAdmissionReleasedTotal.WithLabelValues(string(downloadadmission.ReleaseStorageError)))
	releasedCompleted := testutil.ToFloat64(metrics.DownloadAdmissionReleasedTotal.WithLabelValues(string(downloadadmission.ReleaseCompleted)))
	if releasedCompleted != beforeCompleted {
		t.Fatalf("completed releases = %v, want unchanged %v", releasedCompleted, beforeCompleted)
	}
	if releasedResponse == beforeResponse && releasedStorage == beforeStorage {
		t.Fatal("expected a non-completed release cause after partial write failure")
	}
	if got := testutil.ToFloat64(metrics.DownloadAdmissionActiveCurrent); got != 0 {
		t.Fatalf("active admissions after partial write = %v, want 0", got)
	}
	_ = coordinator
}

func TestSyncBlockDownloadAdmissionFinishHandlerReleasesPanic(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := syncBlockAdmissionConfig()
	coordinator, err := downloadadmission.New(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Writer = &syncBlockDeadlineResponseWriter{ResponseWriter: c.Writer}
	c.Request = httptest.NewRequest(http.MethodGet, "/seafhttp/repo/repo/block/"+strings.Repeat("d", 64), nil)
	c.Set("org_id", "org-1")
	c.Set("user_id", "user-1")

	h := &SyncHandler{
		config:            &config.Config{DownloadAdmission: cfg},
		downloadAdmission: coordinator,
	}
	lifecycle, ok := h.acquireSyncBlockDownloadAdmission(c, "org-1", "user-1")
	if !ok || lifecycle == nil {
		t.Fatal("acquire failed")
	}

	before := testutil.ToFloat64(metrics.DownloadAdmissionReleasedTotal.WithLabelValues(string(downloadadmission.ReleasePanic)))
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expected panic to propagate")
			}
		}()
		defer lifecycle.FinishHandler()
		panic("sync block handler boom")
	}()

	if got := testutil.ToFloat64(metrics.DownloadAdmissionReleasedTotal.WithLabelValues(string(downloadadmission.ReleasePanic))); got != before+1 {
		t.Fatalf("panic releases = %v, want %v", got, before+1)
	}
	if got := testutil.ToFloat64(metrics.DownloadAdmissionActiveCurrent); got != 0 {
		t.Fatalf("active admissions after panic = %v, want 0", got)
	}
}

func TestSyncBlockGetBlockDefersFinishHandler(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to resolve current file path")
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join(filepath.Dir(thisFile), "sync.go"), nil, 0)
	if err != nil {
		t.Fatalf("parse sync.go: %v", err)
	}

	fn := syncFindFunction(t, file, "GetBlock")
	if !syncDefersCall(fn, "FinishHandler") {
		t.Fatal("GetBlock does not defer FinishHandler")
	}
	if !syncCallsSelector(fn, "ResponseBytesSince") {
		t.Fatal("GetBlock must record traffic from ResponseBytesSince, not nominal size")
	}
	if syncBufferedGetBlockOnReader(fn) {
		t.Fatal("GetBlock must not materialize via reader.GetBlock on the streaming path")
	}
}

// startSyncBlockGETServer serves the real block GET route over a real socket so
// the writer chain is the one net/http actually hands the handler.
func startSyncBlockGETServer(t *testing.T, h *SyncHandler, middleware ...gin.HandlerFunc) *httptest.Server {
	t.Helper()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	for _, m := range middleware {
		r.Use(m)
	}
	r.Use(func(c *gin.Context) {
		c.Set("org_id", "00000000-0000-0000-0000-000000000001")
		c.Set("user_id", "user")
		c.Next()
	})
	r.GET("/seafhttp/repo/:repo_id/block/:block_id", h.GetBlock)

	srv := httptest.NewUnstartedServer(r)
	// Mirror the shipped config: no write timeout, because large transfers
	// legitimately take minutes. That is what makes D's idle-write deadline the
	// only write bound on the connection.
	srv.Config.WriteTimeout = 0
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

func stubSyncBlockCanonicalPayload(t *testing.T, blockID string, payload []byte) {
	t.Helper()

	oldReader := syncNewCanonicalBlockReaderFn
	oldTouch := syncTouchBlockLastAccessFn
	t.Cleanup(func() {
		syncNewCanonicalBlockReaderFn = oldReader
		syncTouchBlockLastAccessFn = oldTouch
	})
	syncTouchBlockLastAccessFn = func(*db.DB, string, string, time.Time) {}
	syncNewCanonicalBlockReaderFn = func(context.Context, *db.DB, *storage.Manager, string, []string, *storage.BlockStore, string) (streaming.CanonicalBlockReader, error) {
		return &syncCanonicalReaderStub{data: map[string][]byte{blockID: payload}}, nil
	}
}

// TestSyncBlockGETIdleWriteReachesConnectionThroughGzipStack drives the real
// shipped middleware stack. D5's admitted lifetime is only a bound if the
// idle-write deadline reaches the socket, and gin-contrib/gzip's writer embeds
// the gin.ResponseWriter interface, so it exposes neither SetWriteDeadline nor
// Unwrap and terminates http.NewResponseController's walk. Subcontract C shipped
// exactly that defect on check-blocks; the block route is excluded outright, and
// this pins it for GET the way the PUT regression pins it for uploads.
func TestSyncBlockGETIdleWriteReachesConnectionThroughGzipStack(t *testing.T) {
	blockID := strings.Repeat("e", 64)
	payload := []byte("gzip-negotiated-block-bytes")
	h, _ := newSyncBlockAdmissionHandler(t, syncBlockAdmissionConfig())
	stubSyncBlockCanonicalPayload(t, blockID, payload)

	srv := startSyncBlockGETServer(t, h, gingzip.Gzip(gingzip.DefaultCompression,
		gingzip.WithExcludedPathsRegexs(gzipExcludedPathsRegexs("/metrics")),
	))

	before := testutil.ToFloat64(metrics.DownloadAdmissionWriterUnreachableTotal)

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/seafhttp/repo/repo/block/"+blockID, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	// Explicit, so the transport does not transparently decompress for us.
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("gzip-negotiating block GET failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	// The route must be excluded outright, not merely survive: a compressed
	// response means the writer was wrapped and the deadline could not install.
	if got := resp.Header.Get("Content-Encoding"); got != "" {
		t.Fatalf("Content-Encoding = %q, want the block route excluded from gzip", got)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != string(payload) {
		t.Fatalf("body = %q, want %q", body, payload)
	}
	if after := testutil.ToFloat64(metrics.DownloadAdmissionWriterUnreachableTotal); after != before {
		t.Fatalf("writer-unreachable counter moved from %v to %v; the idle-write deadline should install cleanly on this route", before, after)
	}
	if got := testutil.ToFloat64(metrics.DownloadAdmissionActiveCurrent); got != 0 {
		t.Fatalf("active admissions after a completed block GET = %v, want 0", got)
	}
}

// TestSyncBlockGETFailsClosedWhenIdleWriteCannotBeInstalled guards the general
// case rather than today's one excluded route: the next middleware that hides
// the connection must refuse the transfer, not stream it unprotected.
func TestSyncBlockGETFailsClosedWhenIdleWriteCannotBeInstalled(t *testing.T) {
	blockID := strings.Repeat("f", 64)
	h, _ := newSyncBlockAdmissionHandler(t, syncBlockAdmissionConfig())
	stubSyncBlockCanonicalPayload(t, blockID, []byte("must-not-be-streamed"))

	srv := startSyncBlockGETServer(t, h, func(c *gin.Context) {
		c.Writer = unwrappableWriter{ResponseWriter: c.Writer}
		c.Next()
	})

	before := testutil.ToFloat64(metrics.DownloadAdmissionWriterUnreachableTotal)

	resp, err := srv.Client().Get(srv.URL + "/seafhttp/repo/repo/block/" + blockID)
	if err != nil {
		t.Fatalf("block GET behind an unwrappable writer failed: %v", err)
	}
	defer resp.Body.Close()

	// Unlike the PUT side there is no unread request body to drain, so the
	// refusal is deliverable and the client sees a retryable 503.
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if strings.Contains(string(body), "must-not-be-streamed") {
		t.Fatal("block bytes were streamed without an installable idle-write deadline")
	}
	if after := testutil.ToFloat64(metrics.DownloadAdmissionWriterUnreachableTotal); after != before+1 {
		t.Fatalf("writer-unreachable counter = %v, want %v; an unprotectable transfer must be observable", after, before+1)
	}
	if got := testutil.ToFloat64(metrics.DownloadAdmissionActiveCurrent); got != 0 {
		t.Fatalf("active admissions after fail-closed block GET = %v, want 0", got)
	}
}

type countingCanonicalReader struct {
	inner    *syncCanonicalReaderStub
	onSize   func()
	onReader func()
}

func (r *countingCanonicalReader) GetBlock(ctx context.Context, hash string) ([]byte, error) {
	return r.inner.GetBlock(ctx, hash)
}

func (r *countingCanonicalReader) GetBlockReader(ctx context.Context, hash string) (io.ReadCloser, error) {
	if r.onReader != nil {
		r.onReader()
	}
	return r.inner.GetBlockReader(ctx, hash)
}

func (r *countingCanonicalReader) GetBlockSize(ctx context.Context, hash string) (int64, error) {
	if r.onSize != nil {
		r.onSize()
	}
	return r.inner.GetBlockSize(ctx, hash)
}

func (r *countingCanonicalReader) CheckBlocksExist(ctx context.Context, hashes []string, fanout int) (map[string]bool, error) {
	return r.inner.CheckBlocksExist(ctx, hashes, fanout)
}

func syncFindFunction(t *testing.T, file *ast.File, name string) *ast.FuncDecl {
	t.Helper()
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == name {
			return fn
		}
	}
	t.Fatalf("function %s not found", name)
	return nil
}

func syncDefersCall(fn *ast.FuncDecl, name string) bool {
	found := false
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		deferStmt, ok := node.(*ast.DeferStmt)
		if !ok {
			return true
		}
		ast.Inspect(deferStmt.Call, func(child ast.Node) bool {
			call, ok := child.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if ok && selector.Sel.Name == name {
				found = true
			}
			return true
		})
		return true
	})
	return found
}

func syncCallsSelector(fn *ast.FuncDecl, name string) bool {
	found := false
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == name {
			found = true
		}
		return true
	})
	return found
}

func syncBufferedGetBlockOnReader(fn *ast.FuncDecl) bool {
	found := false
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "GetBlock" {
			return true
		}
		if ident, ok := selector.X.(*ast.Ident); ok && ident.Name == "reader" {
			found = true
		}
		return true
	})
	return found
}
