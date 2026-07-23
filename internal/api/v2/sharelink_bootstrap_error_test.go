package v2

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
