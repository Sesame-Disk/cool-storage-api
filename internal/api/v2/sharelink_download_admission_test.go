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
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func shareLinkAdmissionConfig() config.DownloadAdmissionConfig {
	return config.DownloadAdmissionConfig{
		Enabled:                true,
		MaxActivePerNode:       4,
		MaxActivePerAuthUser:   4,
		MaxActivePerLinkSource: 2,
		MaxActivePerClientLink: 1,
		MaxWaitersPerIdentity:  4,
		MaxWaitersPerNode:      4,
		PreparationDeadline:    time.Minute,
		IdleWriteTimeout:       time.Minute,
		RetryAfter:             2 * time.Second,
		MaxActiveLinkRaw:       2,
		MaxActiveLinkInline:    2,
	}
}

func newShareLinkAdmissionHandler(t *testing.T, cfg config.DownloadAdmissionConfig) (*ShareLinkViewHandler, *downloadadmission.Coordinator) {
	t.Helper()
	coordinator, err := downloadadmission.New(&cfg)
	if err != nil {
		t.Fatalf("new download admission coordinator: %v", err)
	}
	return &ShareLinkViewHandler{
		config:            &config.Config{DownloadAdmission: cfg},
		downloadAdmission: coordinator,
	}, coordinator
}

func newShareLinkAdmissionContext(t *testing.T, requestContext context.Context) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/d/share-token", nil).WithContext(requestContext)
	c.Request.RemoteAddr = "198.51.100.20:12345"
	return c, w
}

func TestShareLinkAdmissionUsesStableSourceAndClientIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, profile := range []downloadadmission.Profile{downloadadmission.ProfileLinkRaw, downloadadmission.ProfileLinkInline} {
		t.Run(string(profile), func(t *testing.T) {
			cfg := shareLinkAdmissionConfig()
			h, coordinator := newShareLinkAdmissionHandler(t, cfg)
			c, _ := newShareLinkAdmissionContext(t, context.Background())
			sl := &shareLinkData{token: "share-token"}

			holderRequest, err := downloadadmission.NewPublicLinkRequest(profile, publicLinkSourceID("share-link", sl.token), c.ClientIP())
			if err != nil {
				t.Fatal(err)
			}
			holder, reason := coordinator.Acquire(context.Background(), holderRequest)
			if holder == nil || reason != "" {
				t.Fatalf("hold admission = (%v, %q)", holder, reason)
			}
			defer holder.Release(downloadadmission.ReleaseCompleted)

			lifecycle, reason, err := h.acquireShareLinkDownloadAdmission(c, sl, profile)
			if lifecycle != nil || err != nil {
				t.Fatalf("acquire admission = (%v, %q, %v), want client-link refusal", lifecycle, reason, err)
			}
			if reason != downloadadmission.RejectClientLinkFull {
				t.Fatalf("rejection = %q, want %q; source and client identity must match the holder", reason, downloadadmission.RejectClientLinkFull)
			}
		})
	}
}

func TestShareLinkRawAdmissionFollowsCheapGates(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to resolve current file path")
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join(filepath.Dir(thisFile), "sharelink_view.go"), nil, 0)
	if err != nil {
		t.Fatalf("parse sharelink_view.go: %v", err)
	}
	fn := findFunction(t, file, "handleShareLinkRaw")

	orderedCalls := []string{
		"acquireShareLinkDownloadAdmission",
		"libraryIsEncryptedContext",
		"setCacheHeaders",
		"resolveLibraryBlockStoreForRequestContext",
		"NewCanonicalBlockReader",
	}
	previous := token.NoPos
	for _, name := range orderedCalls {
		position := firstCallPos(fn, name)
		if position == token.NoPos {
			t.Fatalf("handleShareLinkRaw does not call %s", name)
		}
		if previous != token.NoPos && position < previous {
			t.Fatalf("handleShareLinkRaw calls %s at line %d before its required predecessor", name, fset.Position(position).Line)
		}
		previous = position
	}
}

func TestShareLinkDownloadAdmissionProducersDeferCleanup(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to resolve current file path")
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join(filepath.Dir(thisFile), "sharelink_view.go"), nil, 0)
	if err != nil {
		t.Fatalf("parse sharelink_view.go: %v", err)
	}
	for _, tt := range []struct {
		name      string
		deferCall string
	}{
		{name: "handleShareLinkRaw", deferCall: "FinishHandler"},
		{name: "emitShareFileBootstrap", deferCall: "Finish"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if !shareLinkDefersCall(findFunction(t, file, tt.name), tt.deferCall) {
				t.Fatalf("%s does not defer %s", tt.name, tt.deferCall)
			}
		})
	}
}

func shareLinkDefersCall(fn *ast.FuncDecl, name string) bool {
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

func TestShareFileBootstrapSkipsAdmissionForNonProducers(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := shareLinkAdmissionConfig()
	cfg.MaxActiveLinkInline = 1
	h, coordinator := newShareLinkAdmissionHandler(t, cfg)
	holderRequest, err := downloadadmission.NewPublicLinkRequest(downloadadmission.ProfileLinkInline, publicLinkSourceID("share-link", "share-token"), "198.51.100.20")
	if err != nil {
		t.Fatal(err)
	}
	holder, reason := coordinator.Acquire(context.Background(), holderRequest)
	if holder == nil || reason != "" {
		t.Fatalf("hold inline profile = (%v, %q)", holder, reason)
	}
	defer holder.Release(downloadadmission.ReleaseCompleted)

	tests := []struct {
		name        string
		filePath    string
		onlyOffice  bool
		tokenSource TokenCreator
	}{
		{name: "PDF", filePath: "/report.pdf"},
		{name: "image", filePath: "/image.png"},
		{name: "OnlyOffice", filePath: "/report.docx", onlyOffice: true, tokenSource: &sourceIDTokenCreator{}},
		{name: "OnlyOffice CSV", filePath: "/report.csv", onlyOffice: true, tokenSource: &sourceIDTokenCreator{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h.config.OnlyOffice.Enabled = tt.onlyOffice
			h.config.OnlyOffice.JWTSecret = "onlyoffice-test-secret"
			h.config.OnlyOffice.APIJSURL = "https://office.example/web-apps/apps/api/documents/api.js"
			h.tokenCreator = tt.tokenSource
			c, w := newShareLinkAdmissionContext(t, context.Background())
			h.emitShareFileBootstrap(c, &shareLinkData{
				token:       "share-token",
				filePath:    tt.filePath,
				canDownload: true,
				targetEntry: &FSEntry{ID: "file-id", Size: 1},
			})
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; non-producer must not consume inline admission", w.Code)
			}
		})
	}
}

type shareLinkAdmissionTestWriter struct {
	gin.ResponseWriter
}

func (w *shareLinkAdmissionTestWriter) SetWriteDeadline(time.Time) error {
	return nil
}

func TestShareFileBootstrapPassesAdmissionPreparationContextToInlineRead(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h, _ := newShareLinkAdmissionHandler(t, shareLinkAdmissionConfig())
	requestContext := context.WithValue(context.Background(), shareLinkAdmissionContextKey{}, "request-value")
	c, w := newShareLinkAdmissionContext(t, requestContext)
	c.Writer = &shareLinkAdmissionTestWriter{ResponseWriter: c.Writer}

	original := shareInlineTextFn
	t.Cleanup(func() { shareInlineTextFn = original })
	var readContext context.Context
	shareInlineTextFn = func(_ *ShareLinkViewHandler, ctx context.Context, _ *shareLinkData) (string, error) {
		readContext = ctx
		return "inline text", nil
	}

	h.emitShareFileBootstrap(c, &shareLinkData{
		token:       "share-token",
		filePath:    "/notes.txt",
		targetEntry: &FSEntry{ID: "file-id", Size: 11},
	})

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if readContext == nil {
		t.Fatal("inline reader received no context")
	}
	if got := readContext.Value(shareLinkAdmissionContextKey{}); got != "request-value" {
		t.Fatalf("inline reader context value = %v, want request value", got)
	}
	if _, ok := readContext.Deadline(); !ok {
		t.Fatal("inline reader did not receive the admission preparation context")
	}
}

type shareLinkAdmissionContextKey struct{}

func TestShareFileBootstrapAdmissionRejectsBeforeInlineRead(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := shareLinkAdmissionConfig()
	cfg.MaxActiveLinkInline = 1
	h, coordinator := newShareLinkAdmissionHandler(t, cfg)
	c, w := newShareLinkAdmissionContext(t, context.Background())
	sl := &shareLinkData{
		token:       "share-token",
		filePath:    "/notes.txt",
		targetEntry: &FSEntry{ID: "file-id", Size: 11},
	}
	holderRequest, err := downloadadmission.NewPublicLinkRequest(downloadadmission.ProfileLinkInline, publicLinkSourceID("share-link", sl.token), c.ClientIP())
	if err != nil {
		t.Fatal(err)
	}
	holder, reason := coordinator.Acquire(context.Background(), holderRequest)
	if holder == nil || reason != "" {
		t.Fatalf("hold inline profile = (%v, %q)", holder, reason)
	}
	defer holder.Release(downloadadmission.ReleaseCompleted)

	original := shareInlineTextFn
	t.Cleanup(func() { shareInlineTextFn = original })
	readCalls := 0
	shareInlineTextFn = func(*ShareLinkViewHandler, context.Context, *shareLinkData) (string, error) {
		readCalls++
		return "", nil
	}

	h.emitShareFileBootstrap(c, sl)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Retry-After"); got != "2" {
		t.Fatalf("Retry-After = %q, want 2", got)
	}
	if readCalls != 0 {
		t.Fatalf("inline reader called %d time(s) after admission refusal", readCalls)
	}
}

func TestShareFileBootstrapAttributesPanicAsPanic(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _ := newShareLinkAdmissionHandler(t, shareLinkAdmissionConfig())
	c, _ := newShareLinkAdmissionContext(t, context.Background())

	original := shareFileBootstrapFn
	t.Cleanup(func() { shareFileBootstrapFn = original })
	shareFileBootstrapFn = func(_ *ShareLinkViewHandler, _ *gin.Context, _ *shareLinkData, acquire func() (downloadadmission.RejectReason, error)) (pageBootstrapResponse, downloadadmission.RejectReason, int, error) {
		if _, err := acquire(); err != nil {
			t.Fatalf("inline admission acquire: %v", err)
		}
		panic("inline bootstrap panic")
	}

	before := testutil.ToFloat64(metrics.DownloadAdmissionReleasedTotal.WithLabelValues(string(downloadadmission.ReleasePanic)))
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		h.emitShareFileBootstrap(c, &shareLinkData{token: "share-token", filePath: "/notes.txt", targetEntry: &FSEntry{ID: "file-id", Size: 1}})
	}()
	if recovered != "inline bootstrap panic" {
		t.Fatalf("recovered panic = %v, want inline bootstrap panic", recovered)
	}
	if got := testutil.ToFloat64(metrics.DownloadAdmissionReleasedTotal.WithLabelValues(string(downloadadmission.ReleasePanic))); got != before+1 {
		t.Fatalf("panic release count = %v, want %v", got, before+1)
	}
}
