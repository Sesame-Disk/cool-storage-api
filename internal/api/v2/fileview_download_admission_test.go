package v2

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/downloadadmission"
	"github.com/Sesame-Disk/sesamefs/internal/httputil"
	"github.com/gin-gonic/gin"
)

type fileViewDeadlineResponseWriter struct {
	gin.ResponseWriter
}

func (w *fileViewDeadlineResponseWriter) SetWriteDeadline(time.Time) error {
	return nil
}

func fileViewAdmissionConfig() config.DownloadAdmissionConfig {
	return config.DownloadAdmissionConfig{
		Enabled:                true,
		MaxActivePerNode:       1,
		MaxActivePerAuthUser:   1,
		MaxActivePerLinkSource: 1,
		MaxActivePerClientLink: 1,
		MaxActiveRaw:           1,
		MaxActiveHistory:       1,
		PreparationDeadline:    time.Minute,
		IdleWriteTimeout:       time.Minute,
		RetryAfter:             1500 * time.Millisecond,
	}
}

func newFileViewAdmissionContext(parent context.Context, deadlineCapable bool) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/repo/repo/raw/file.txt", nil).WithContext(parent)
	if deadlineCapable {
		c.Writer = &fileViewDeadlineResponseWriter{ResponseWriter: c.Writer}
	}
	return c, recorder
}

func newFileViewAdmissionHandler(t *testing.T, cfg config.DownloadAdmissionConfig) *FileViewHandler {
	t.Helper()
	coordinator, err := downloadadmission.New(&cfg)
	if err != nil {
		t.Fatalf("new download admission coordinator: %v", err)
	}
	return &FileViewHandler{
		config:            &config.Config{DownloadAdmission: cfg},
		downloadAdmission: coordinator,
	}
}

func TestFileViewDownloadAdmissionDisabledIsTransparent(t *testing.T) {
	h := &FileViewHandler{config: &config.Config{}}
	parent := context.Background()
	c, _ := newFileViewAdmissionContext(parent, false)
	requestBefore := c.Request
	writerBefore := c.Writer

	lifecycle, ok := h.acquireFileViewDownloadAdmission(c, "org-1", "user-1", downloadadmission.ProfileRaw)
	if !ok || lifecycle == nil {
		t.Fatal("disabled file-view admission did not return a lifecycle")
	}
	if c.Request != requestBefore || c.Writer != writerBefore {
		t.Fatal("disabled file-view admission changed the request or response writer")
	}
	if lifecycle.PreparationContext() != parent {
		t.Fatal("disabled file-view admission did not preserve the request context")
	}
	if err := lifecycle.Finish(downloadadmission.ReleaseCompleted); err != nil {
		t.Fatalf("finish disabled lifecycle: %v", err)
	}
}

func TestFileViewDownloadAdmissionUsesLifecycleContexts(t *testing.T) {
	cfg := fileViewAdmissionConfig()
	h := newFileViewAdmissionHandler(t, cfg)
	c, recorder := newFileViewAdmissionContext(context.Background(), true)

	lifecycle, ok := h.acquireFileViewDownloadAdmission(c, "org-1", "user-1", downloadadmission.ProfileRaw)
	if !ok || lifecycle == nil {
		t.Fatal("enabled file-view admission did not return a lifecycle")
	}
	if _, hasDeadline := lifecycle.PreparationContext().Deadline(); !hasDeadline {
		t.Fatal("preparation context has no deadline")
	}

	streamCtx, err := lifecycle.StartStreaming()
	if err != nil {
		t.Fatalf("start streaming: %v", err)
	}
	if _, hasDeadline := streamCtx.Deadline(); hasDeadline {
		t.Fatal("streaming context retained the preparation deadline")
	}
	if _, ok := c.Writer.(*httputil.IdleWriteWriter); !ok {
		t.Fatal("streaming did not install the idle-write writer")
	}
	if _, err := c.Writer.Write([]byte("body")); err != nil {
		t.Fatalf("write through streaming writer: %v", err)
	}
	if err := lifecycle.Finish(downloadadmission.ReleaseCompleted); err != nil {
		t.Fatalf("finish streaming lifecycle: %v", err)
	}
	if body := recorder.Body.String(); body != "body" {
		t.Fatalf("response body = %q, want body", body)
	}
}

func TestFileViewDownloadAdmissionRefusalDoesNotEnterPreparation(t *testing.T) {
	cfg := fileViewAdmissionConfig()
	h := newFileViewAdmissionHandler(t, cfg)
	holderContext, _ := newFileViewAdmissionContext(context.Background(), false)
	holder, ok := h.acquireFileViewDownloadAdmission(holderContext, "org-1", "user-1", downloadadmission.ProfileRaw)
	if !ok || holder == nil {
		t.Fatal("holder admission was not granted")
	}
	defer holder.FinishHandler()

	c, recorder := newFileViewAdmissionContext(context.Background(), false)
	requestBefore := c.Request
	lifecycle, ok := h.acquireFileViewDownloadAdmission(c, "org-1", "user-1", downloadadmission.ProfileRaw)
	if ok || lifecycle != nil {
		t.Fatalf("refused admission = (%v, %v), want no lifecycle", lifecycle, ok)
	}
	if c.Request != requestBefore {
		t.Fatal("refused admission entered preparation and changed the request context")
	}
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
	if got := recorder.Header().Get("Retry-After"); got != "2" {
		t.Fatalf("Retry-After = %q, want 2", got)
	}
}

func TestFileViewDownloadAdmissionFinishHandlerReleasesAfterPanic(t *testing.T) {
	cfg := fileViewAdmissionConfig()
	h := newFileViewAdmissionHandler(t, cfg)
	var recovered any

	func() {
		defer func() { recovered = recover() }()
		c, _ := newFileViewAdmissionContext(context.Background(), false)
		lifecycle, ok := h.acquireFileViewDownloadAdmission(c, "org-1", "user-1", downloadadmission.ProfileHistory)
		if !ok || lifecycle == nil {
			t.Fatal("panic-path admission was not granted")
		}
		defer lifecycle.FinishHandler()
		panic("file-view handler panic")
	}()
	if recovered != "file-view handler panic" {
		t.Fatalf("recovered panic = %v, want file-view handler panic", recovered)
	}

	c, _ := newFileViewAdmissionContext(context.Background(), false)
	lifecycle, ok := h.acquireFileViewDownloadAdmission(c, "org-1", "user-1", downloadadmission.ProfileHistory)
	if !ok || lifecycle == nil {
		t.Fatal("panic-path lease was not released")
	}
	if err := lifecycle.Finish(downloadadmission.ReleaseCompleted); err != nil {
		t.Fatalf("finish replacement lifecycle: %v", err)
	}
}

func TestFileViewAdmissionProtectsReaderSetupAndHTTPFastPaths(t *testing.T) {
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to resolve test file")
	}
	fileNode, err := parser.ParseFile(token.NewFileSet(), filepath.Join(filepath.Dir(testFile), "fileview.go"), nil, 0)
	if err != nil {
		t.Fatalf("parse fileview.go: %v", err)
	}

	tests := []struct {
		name          string
		profile       string
		startCount    int
		mustFollow    []string
		fastPathCalls []string
	}{
		{
			name:       "ServeRawFile",
			profile:    "ProfileRaw",
			startCount: 3,
			mustFollow: []string{"resolveLibraryBlockStoreForRequestContext", "NewCanonicalBlockReader", "BatchResolveBlockIDsContext", "QueryBlockSizes", "GetBlockReader", "NewBlockReadSeeker", "ServeContent", "StreamBlocks"},
			fastPathCalls: []string{
				"setCacheHeaders",
				"getMaxFileSizeForPreview",
			},
		},
		{
			name:       "DownloadHistoricFile",
			profile:    "ProfileHistory",
			startCount: 1,
			mustFollow: []string{"resolveLibraryBlockStoreForRequestContext", "NewCanonicalBlockReader", "BatchResolveBlockIDsContext", "StreamBlocks"},
		},
		{
			name:       "ServeHistoricFileRaw",
			profile:    "ProfileHistory",
			startCount: 1,
			mustFollow: []string{"resolveLibraryBlockStoreForRequestContext", "NewCanonicalBlockReader", "BatchResolveBlockIDsContext", "StreamBlocks"},
			fastPathCalls: []string{
				"setCacheHeaders",
				"getMaxFileSizeForPreview",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fn := fileViewAdmissionFunction(t, fileNode, tt.name)
			acquire := fileViewAdmissionCallPos(fn, "acquireFileViewDownloadAdmission")
			if !acquire.IsValid() {
				t.Fatal("handler does not acquire download admission")
			}
			if !fileViewAdmissionProfileUsed(fn, tt.profile) {
				t.Fatalf("handler does not use %s", tt.profile)
			}
			if !fileViewAdmissionDefersFinishHandler(fn) {
				t.Fatal("handler does not defer FinishHandler for panic-safe release")
			}
			for _, name := range tt.fastPathCalls {
				if pos := fileViewAdmissionCallPos(fn, name); !pos.IsValid() || pos > acquire {
					t.Fatalf("%s must run before admission so pre-reader fast paths do not reserve a lease", name)
				}
			}
			for _, name := range tt.mustFollow {
				if pos := fileViewAdmissionCallPos(fn, name); !pos.IsValid() || pos < acquire {
					t.Fatalf("%s must run after admission to avoid reader setup for rejected requests", name)
				}
			}
			streamStart := fileViewAdmissionCallPos(fn, "StartStreaming")
			if !streamStart.IsValid() {
				t.Fatal("handler does not start the streaming writer")
			}
			if got := fileViewAdmissionCallCount(fn, "StartStreaming"); got != tt.startCount {
				t.Fatalf("StartStreaming call count = %d, want %d; every producer branch must transition explicitly", got, tt.startCount)
			}
			for _, name := range []string{"Data", "ServeContent", "StreamBlocks"} {
				if pos := fileViewAdmissionCallPos(fn, name); pos.IsValid() && streamStart > pos {
					t.Fatalf("StartStreaming must precede %s", name)
				}
			}
		})
	}

	if pos := fileViewAdmissionCallPos(fileViewAdmissionFunction(t, fileNode, "ViewHistoricFile"), "acquireFileViewDownloadAdmission"); pos.IsValid() {
		t.Fatal("ViewHistoricFile is a redirect-only endpoint and must not acquire admission")
	}
}

func fileViewAdmissionCallCount(fn *ast.FuncDecl, name string) int {
	count := 0
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if ok && fileViewAdmissionCallName(call) == name {
			count++
		}
		return true
	})
	return count
}

func fileViewAdmissionFunction(t *testing.T, fileNode *ast.File, name string) *ast.FuncDecl {
	t.Helper()
	for _, decl := range fileNode.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == name {
			return fn
		}
	}
	t.Fatalf("function %s not found", name)
	return nil
}

func fileViewAdmissionCallPos(fn *ast.FuncDecl, name string) token.Pos {
	pos := token.NoPos
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if fileViewAdmissionCallName(call) == name && (pos == token.NoPos || call.Pos() < pos) {
			pos = call.Pos()
		}
		return true
	})
	return pos
}

func fileViewAdmissionCallName(call *ast.CallExpr) string {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return fun.Name
	case *ast.SelectorExpr:
		return fun.Sel.Name
	default:
		return ""
	}
}

func fileViewAdmissionProfileUsed(fn *ast.FuncDecl, profile string) bool {
	found := false
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == profile {
			found = true
		}
		return true
	})
	return found
}

func fileViewAdmissionDefersFinishHandler(fn *ast.FuncDecl) bool {
	found := false
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		deferStmt, ok := node.(*ast.DeferStmt)
		if !ok {
			return true
		}
		if fileViewAdmissionCallName(deferStmt.Call) == "FinishHandler" {
			found = true
		}
		return true
	})
	return found
}
