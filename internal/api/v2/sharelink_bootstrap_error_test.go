package v2

import (
	"encoding/json"
	"errors"
	"fmt"
	goast "go/ast"
	goparser "go/parser"
	gotoken "go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/gin-gonic/gin"
)

// The share-link bootstrap is a PUBLIC, unauthenticated surface. The errors the
// read path produces wrap internal SHA-256 block ids, storage classes and
// Cassandra/S3 detail, so the body must stay generic while the real cause goes
// to the log. A transient answer also has to carry Retry-After, and a locked
// encrypted library has to be 403 rather than a retryable 503 or an empty 200.
func TestRespondShareBootstrapErrorNeverLeaksInternals(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const secretBlockID = "5f3a9b2c8d1e4f6a7b9c0d2e3f4a5b6c7d8e9f0a1b2c3d4e5f6a7b8c9d0e1f2a"
	internalErr := fmt.Errorf("read block %s for inline text: %w", secretBlockID,
		errors.New("s3: NoSuchKey on bucket sesamefs-usa key blocks/org-9/5f/3a/…"))

	tests := []struct {
		name          string
		status        int
		err           error
		wantStatus    int
		wantRetryable bool
	}{
		{
			name:          "transient failure is retryable and generic",
			status:        http.StatusServiceUnavailable,
			err:           internalErr,
			wantStatus:    http.StatusServiceUnavailable,
			wantRetryable: true,
		},
		{
			name:       "locked encrypted library is a plain 403",
			status:     http.StatusForbidden,
			err:        errShareLinkLibraryLocked,
			wantStatus: http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/d/abc123/", nil)
			sl := &shareLinkData{orgID: "org-9", libraryID: "repo-9"}

			respondShareBootstrapError(c, sl, tt.status, tt.err)

			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", w.Code, tt.wantStatus)
			}
			if got := w.Header().Get("Retry-After"); tt.wantRetryable != (got != "") {
				t.Fatalf("Retry-After = %q, wantPresent=%v", got, tt.wantRetryable)
			}

			body := w.Body.String()
			for _, leaked := range []string{secretBlockID, "sesamefs-usa", "NoSuchKey", "s3:", "blocks/org-9"} {
				if strings.Contains(body, leaked) {
					t.Fatalf("public body leaked %q: %s", leaked, body)
				}
			}
			var decoded map[string]interface{}
			if err := json.Unmarshal(w.Body.Bytes(), &decoded); err != nil {
				t.Fatalf("body is not JSON: %v", err)
			}
			if _, present := decoded["error"]; !present {
				t.Fatalf("body carries no error message: %s", body)
			}
		})
	}
}

// A locked encrypted library must not reach the caller as a successful empty
// document — that is what made the text preview disagree with handleShareLinkRaw,
// which answered 403 for the same state.
func TestShareLinkLibraryLockedIsNotSuccess(t *testing.T) {
	if errShareLinkLibraryLocked == nil {
		t.Fatal("locked-library sentinel must exist so the bootstrap can map it to 403")
	}
	wrapped := fmt.Errorf("inline text: %w", errShareLinkLibraryLocked)
	if !errors.Is(wrapped, errShareLinkLibraryLocked) {
		t.Fatal("sentinel must survive wrapping so callers can classify it")
	}
}

// The chain that matters, driven end to end rather than in disconnected halves:
//
//	readFileContentAsText -> buildShareFileBootstrapResponse -> emitShareFileBootstrap -> HTTP
//
// Only the block-storage read itself is substituted; the real builder does the
// classification and the real emitter writes the response. Asserting on
// respondShareBootstrapError alone would stay green if the builder reclassified
// the locked sentinel as 503, or if the text read went back to ("", nil).
func TestShareFileBootstrapChainClassifiesFailures(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const secretBlockID = "5f3a9b2c8d1e4f6a7b9c0d2e3f4a5b6c7d8e9f0a1b2c3d4e5f6a7b8c9d0e1f2a"

	tests := []struct {
		name          string
		readErr       error
		wantStatus    int
		wantRetryable bool
	}{
		{
			name:          "transient read failure becomes a retryable 503",
			readErr:       fmt.Errorf("read block %s for inline text: %w", secretBlockID, errors.New("s3: NoSuchKey bucket sesamefs-usa")),
			wantStatus:    http.StatusServiceUnavailable,
			wantRetryable: true,
		},
		{
			name:       "locked encrypted library becomes 403, never an empty 200",
			readErr:    errShareLinkLibraryLocked,
			wantStatus: http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := shareInlineTextFn
			t.Cleanup(func() { shareInlineTextFn = original })
			called := false
			shareInlineTextFn = func(*ShareLinkViewHandler, *shareLinkData) (string, error) {
				called = true
				return "", tt.readErr
			}

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/d/abc123/", nil)

			// A markdown target is what makes the real builder consult the inline
			// text reader at all.
			h := &ShareLinkViewHandler{config: &config.Config{}}
			sl := &shareLinkData{
				orgID:       "org-9",
				libraryID:   "repo-9",
				token:       "abc123",
				filePath:    "/notes.md",
				targetEntry: &FSEntry{ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Name: "notes.md", Size: 12, Mode: ModeFile},
			}

			h.emitShareFileBootstrap(c, sl)

			if !called {
				t.Fatal("builder never consulted the inline text reader; the chain is not being exercised")
			}
			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body=%s)", w.Code, tt.wantStatus, w.Body.String())
			}
			if got := w.Header().Get("Retry-After"); tt.wantRetryable != (got != "") {
				t.Fatalf("Retry-After = %q, wantPresent=%v", got, tt.wantRetryable)
			}
			body := w.Body.String()
			if strings.Contains(body, secretBlockID) || strings.Contains(body, "sesamefs-usa") || strings.Contains(body, "NoSuchKey") {
				t.Fatalf("public body leaked internals: %s", body)
			}
		})
	}
}

// Both public endpoints must route through the one shared emitter, so neither can
// drift back to exposing err.Error() on its own.
func TestBothShareBootstrapEndpointsUseTheSharedEmitter(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to resolve current file path")
	}
	fset := gotoken.NewFileSet()
	fileNode, err := goparser.ParseFile(fset, filepath.Join(filepath.Dir(thisFile), "sharelink_view.go"), nil, 0)
	if err != nil {
		t.Fatalf("parse sharelink_view.go: %v", err)
	}

	directResponders := 0
	goast.Inspect(fileNode, func(node goast.Node) bool {
		fn, ok := node.(*goast.FuncDecl)
		if !ok || fn.Name.Name == "emitShareFileBootstrap" {
			return true
		}
		goast.Inspect(fn, func(inner goast.Node) bool {
			call, ok := inner.(*goast.CallExpr)
			if !ok {
				return true
			}
			if ident, ok := call.Fun.(*goast.Ident); ok && ident.Name == "respondShareBootstrapError" {
				directResponders++
			}
			return true
		})
		return true
	})

	if directResponders != 0 {
		t.Fatalf("respondShareBootstrapError is called from %d place(s) outside emitShareFileBootstrap; both endpoints must share the one emitter", directResponders)
	}
}
