package api

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
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/downloadadmission"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func seafHTTPAdmissionConfig() config.DownloadAdmissionConfig {
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
		MaxActiveFile:          1,
		MaxActiveZIP:           1,
	}
}

func newSeafHTTPAdmissionHandler(t *testing.T, cfg config.DownloadAdmissionConfig, tokens TokenStore) (*SeafHTTPHandler, *downloadadmission.Coordinator) {
	t.Helper()
	coordinator, err := downloadadmission.New(&cfg)
	if err != nil {
		t.Fatalf("new download admission coordinator: %v", err)
	}
	return &SeafHTTPHandler{
		config:            &config.Config{DownloadAdmission: cfg},
		db:                &db.DB{},
		storageManager:    &storage.Manager{},
		tokenStore:        tokens,
		downloadAdmission: coordinator,
	}, coordinator
}

func TestSeafHTTPDownloadAdmissionRefusesFullFileAndZIPProfiles(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name    string
		profile downloadadmission.Profile
		path    string
		invoke  func(*SeafHTTPHandler, *gin.Context)
	}{
		{
			name:    "full file",
			profile: downloadadmission.ProfileFile,
			path:    "/seafhttp/files/mock-download-token/f.txt",
			invoke:  (*SeafHTTPHandler).HandleDownload,
		},
		{
			name:    "zip",
			profile: downloadadmission.ProfileZIP,
			path:    "/seafhttp/zip/mock-download-token",
			invoke:  (*SeafHTTPHandler).HandleZipDownload,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens := NewMockTokenStore()
			if _, err := tokens.CreateDownloadToken("org-1", "repo-1", "/f.txt", "request-user"); err != nil {
				t.Fatalf("create download token: %v", err)
			}
			h, coordinator := newSeafHTTPAdmissionHandler(t, seafHTTPAdmissionConfig(), tokens)
			holderRequest, err := downloadadmission.NewAuthenticatedRequest(tt.profile, "org-1", "other-user")
			if err != nil {
				t.Fatal(err)
			}
			holder, reason := coordinator.Acquire(context.Background(), holderRequest)
			if holder == nil || reason != "" {
				t.Fatalf("hold %s profile = (%v, %q)", tt.profile, holder, reason)
			}
			defer holder.Release(downloadadmission.ReleaseCompleted)

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, tt.path, nil)
			c.Params = gin.Params{{Key: "token", Value: "mock-download-token"}, {Key: "filepath", Value: "/f.txt"}}

			tt.invoke(h, c)

			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503 (body=%s)", w.Code, w.Body.String())
			}
			if got := w.Header().Get("Retry-After"); got != "2" {
				t.Fatalf("Retry-After = %q, want 2", got)
			}
			if body := w.Body.String(); body != "" {
				t.Fatalf("admission refusal body = %q, want no replacement response", body)
			}
		})
	}
}

func TestSeafHTTPDownloadAdmissionProducersDeferCleanup(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to resolve current file path")
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join(filepath.Dir(thisFile), "seafhttp.go"), nil, 0)
	if err != nil {
		t.Fatalf("parse seafhttp.go: %v", err)
	}

	tests := []struct {
		function  string
		deferCall string
	}{
		{function: "HandleDownload", deferCall: "FinishHandler"},
		{function: "HandleZipDownload", deferCall: "finishSeafHTTPZipDownload"},
	}
	for _, tt := range tests {
		t.Run(tt.function, func(t *testing.T) {
			fn := seafHTTPFindFunction(t, file, tt.function)
			if !seafHTTPDefersCall(fn, tt.deferCall) {
				t.Fatalf("%s does not defer %s", tt.function, tt.deferCall)
			}
		})
	}
}

func seafHTTPFindFunction(t *testing.T, file *ast.File, name string) *ast.FuncDecl {
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

func seafHTTPDefersCall(fn *ast.FuncDecl, name string) bool {
	found := false
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		deferStmt, ok := node.(*ast.DeferStmt)
		if !ok {
			return true
		}
		switch call := deferStmt.Call.Fun.(type) {
		case *ast.Ident:
			found = found || call.Name == name
		case *ast.SelectorExpr:
			found = found || call.Sel.Name == name
		}
		return true
	})
	return found
}

func TestSeafHTTPDownloadAdmissionUsesLinkSourceIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tokens := NewMockTokenStore()
	const sourceID = "share-link:stable-source"
	if _, err := tokens.CreateLinkDownloadToken("org-1", "repo-1", "/f.txt", "link-owner", sourceID); err != nil {
		t.Fatalf("create link download token: %v", err)
	}

	cfg := seafHTTPAdmissionConfig()
	cfg.MaxActiveFile = 2
	h, coordinator := newSeafHTTPAdmissionHandler(t, cfg, tokens)
	holderRequest, err := downloadadmission.NewPublicLinkRequest(downloadadmission.ProfileFile, sourceID, "198.51.100.10")
	if err != nil {
		t.Fatal(err)
	}
	holder, reason := coordinator.Acquire(context.Background(), holderRequest)
	if holder == nil || reason != "" {
		t.Fatalf("hold link source = (%v, %q)", holder, reason)
	}
	defer holder.Release(downloadadmission.ReleaseCompleted)

	oldLookup := seafHTTPLookupFileBlocksFn
	t.Cleanup(func() { seafHTTPLookupFileBlocksFn = oldLookup })
	seafHTTPLookupFileBlocksFn = func(context.Context, *SeafHTTPHandler, string, *AccessToken) ([]string, int64, []byte, []byte, *storage.BlockStore, string, error) {
		t.Fatal("protected file lookup ran after a link-source admission refusal")
		return nil, 0, nil, nil, nil, "", nil
	}

	before := testutil.ToFloat64(metrics.DownloadAdmissionRejectedTotal.WithLabelValues(string(downloadadmission.RejectLinkSourceFull)))
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/seafhttp/files/mock-link-download-token/f.txt", nil)
	c.Request.RemoteAddr = "198.51.100.11:12345"
	c.Params = gin.Params{{Key: "token", Value: "mock-link-download-token"}, {Key: "filepath", Value: "/f.txt"}}

	h.HandleDownload(c)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body=%s)", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Retry-After"); got != "2" {
		t.Fatalf("Retry-After = %q, want 2", got)
	}
	if got := testutil.ToFloat64(metrics.DownloadAdmissionRejectedTotal.WithLabelValues(string(downloadadmission.RejectLinkSourceFull))); got != before+1 {
		t.Fatalf("link-source rejection count = %v, want %v", got, before+1)
	}
}

func TestSeafHTTPDownloadAdmissionPassesPreparationContextToLookup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tokens := NewMockTokenStore()
	if _, err := tokens.CreateDownloadToken("org-1", "repo-1", "/f.txt", "request-user"); err != nil {
		t.Fatalf("create download token: %v", err)
	}
	h, _ := newSeafHTTPAdmissionHandler(t, seafHTTPAdmissionConfig(), tokens)

	original := seafHTTPLookupFileBlocksFn
	t.Cleanup(func() { seafHTTPLookupFileBlocksFn = original })
	var preparationCtx context.Context
	seafHTTPLookupFileBlocksFn = func(ctx context.Context, _ *SeafHTTPHandler, _ string, _ *AccessToken) ([]string, int64, []byte, []byte, *storage.BlockStore, string, error) {
		preparationCtx = ctx
		return nil, 0, nil, nil, nil, "", context.DeadlineExceeded
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	type requestContextKey struct{}
	c.Request = httptest.NewRequest(http.MethodGet, "/seafhttp/files/mock-download-token/f.txt", nil).
		WithContext(context.WithValue(context.Background(), requestContextKey{}, "request-value"))
	c.Params = gin.Params{{Key: "token", Value: "mock-download-token"}, {Key: "filepath", Value: "/f.txt"}}

	h.HandleDownload(c)

	if preparationCtx == nil {
		t.Fatal("download lookup did not receive a context")
	}
	if got := preparationCtx.Value(requestContextKey{}); got != "request-value" {
		t.Fatalf("preparation context value = %v, want request value", got)
	}
	if _, ok := preparationCtx.Deadline(); !ok {
		t.Fatal("download lookup did not receive the admission preparation deadline")
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
}
