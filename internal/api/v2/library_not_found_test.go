package v2

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
)

// respondIfLibraryMissing is the shared disambiguator that keeps a missing or
// soft-deleted library from being reported as 403 "permission denied" when a
// caller is denied access. These tests pin its three outcomes.
func TestRespondIfLibraryMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name        string
		stubState   dbpkg.LibraryState
		stubErr     error
		wantHandled bool
		wantStatus  int
		wantError   string
	}{
		{
			name:        "live library is not handled, caller emits its own 403",
			stubState:   dbpkg.LibraryState{},
			stubErr:     nil,
			wantHandled: false,
		},
		{
			name:        "missing library returns 404",
			stubErr:     gocql.ErrNotFound,
			wantHandled: true,
			wantStatus:  http.StatusNotFound,
			wantError:   "library not found",
		},
		{
			name:        "soft-deleted library returns 404",
			stubErr:     dbpkg.ErrLibraryDeleted,
			wantHandled: true,
			wantStatus:  http.StatusNotFound,
			wantError:   "library not found",
		},
		{
			name:        "lookup error returns 500",
			stubErr:     errors.New("cassandra unavailable"),
			wantHandled: true,
			wantStatus:  http.StatusInternalServerError,
			wantError:   "failed to check library state",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := readLiveLibraryStateFn
			readLiveLibraryStateFn = func(_ *gocql.Session, _, _ string) (dbpkg.LibraryState, error) {
				return tt.stubState, tt.stubErr
			}
			defer func() { readLiveLibraryStateFn = original }()

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			handled := respondIfLibraryMissing(c, nil,
				"00000000-0000-0000-0000-000000000001",
				"11111111-1111-1111-1111-111111111111")

			if handled != tt.wantHandled {
				t.Fatalf("handled = %v, want %v", handled, tt.wantHandled)
			}
			if !tt.wantHandled {
				if w.Body.Len() != 0 {
					t.Fatalf("expected no response body for a live library, got %q", w.Body.String())
				}
				return
			}
			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", w.Code, tt.wantStatus)
			}
			assertJSONError(t, w.Body, tt.wantError)
		})
	}
}
