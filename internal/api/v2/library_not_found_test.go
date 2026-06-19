package v2

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
)

const (
	testNotFoundOrgID  = "00000000-0000-0000-0000-000000000001"
	testNotFoundUserID = "00000000-0000-0000-0000-000000000002"
	testNotFoundRepoID = "11111111-1111-1111-1111-111111111111"
)

// withLibraryStateStub stubs the v2 readLiveLibraryStateFn for the duration of fn.
func withLibraryStateStub(t *testing.T, state dbpkg.LibraryState, stubErr error, fn func()) {
	t.Helper()
	original := readLiveLibraryStateFn
	readLiveLibraryStateFn = func(_ *gocql.Session, _, _ string) (dbpkg.LibraryState, error) {
		return state, stubErr
	}
	defer func() { readLiveLibraryStateFn = original }()
	fn()
}

// withLibraryPermissionStub stubs getLibraryPermissionFn for the duration of fn.
func withLibraryPermissionStub(t *testing.T, perm middleware.LibraryPermission, stubErr error, fn func()) {
	t.Helper()
	original := getLibraryPermissionFn
	getLibraryPermissionFn = func(_ *middleware.PermissionMiddleware, _, _, _ string) (middleware.LibraryPermission, error) {
		return perm, stubErr
	}
	defer func() { getLibraryPermissionFn = original }()
	fn()
}

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

func assertStatusError(t *testing.T, w *httptest.ResponseRecorder, wantStatus int, wantError string) {
	t.Helper()
	if w.Code != wantStatus {
		t.Fatalf("status = %d, want %d (body: %s)", w.Code, wantStatus, w.Body.String())
	}
	assertJSONError(t, w.Body, wantError)
}

// --- Handler-level tests: a denied access to a *missing* library is 404, to an
// *existing* library is 403. These pin the disambiguation at the endpoint boundary
// for the handlers that perform an inline permission check (libraries.go,
// files.go, fileview.go), not just the helper in isolation.

// serveLibraryHandler drives GetLibrary/GetLibraryV21, whose permission lookup is
// stubbed via getLibraryPermissionFn (it normally hits Cassandra).
func serveLibraryHandler(t *testing.T, handler func(*LibraryHandler, *gin.Context)) *httptest.ResponseRecorder {
	t.Helper()
	r := gin.New()
	h := newLibraryHandlerForLiveFenceTests()
	r.GET("/repos/:repo_id", func(c *gin.Context) {
		c.Set("org_id", testNotFoundOrgID)
		c.Set("user_id", testNotFoundUserID)
		handler(h, c)
	})
	req := httptest.NewRequest("GET", "/repos/"+testNotFoundRepoID, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestGetLibrary_MissingLibraryReturnsNotFound(t *testing.T) {
	withLibraryPermissionStub(t, middleware.PermissionNone, nil, func() {
		withLibraryStateStub(t, dbpkg.LibraryState{}, gocql.ErrNotFound, func() {
			w := serveLibraryHandler(t, func(h *LibraryHandler, c *gin.Context) { h.GetLibrary(c) })
			assertStatusError(t, w, http.StatusNotFound, "library not found")
		})
	})
}

func TestGetLibrary_ExistingLibraryReturnsForbidden(t *testing.T) {
	withLibraryPermissionStub(t, middleware.PermissionNone, nil, func() {
		withLibraryStateStub(t, dbpkg.LibraryState{}, nil, func() {
			w := serveLibraryHandler(t, func(h *LibraryHandler, c *gin.Context) { h.GetLibrary(c) })
			assertStatusError(t, w, http.StatusForbidden, "you do not have access to this library")
		})
	})
}

func TestGetLibraryV21_MissingLibraryReturnsNotFound(t *testing.T) {
	withLibraryPermissionStub(t, middleware.PermissionNone, nil, func() {
		withLibraryStateStub(t, dbpkg.LibraryState{}, gocql.ErrNotFound, func() {
			w := serveLibraryHandler(t, func(h *LibraryHandler, c *gin.Context) { h.GetLibraryV21(c) })
			assertStatusError(t, w, http.StatusNotFound, "library not found")
		})
	})
}

func TestGetLibraryV21_ExistingLibraryReturnsForbidden(t *testing.T) {
	withLibraryPermissionStub(t, middleware.PermissionNone, nil, func() {
		withLibraryStateStub(t, dbpkg.LibraryState{}, nil, func() {
			w := serveLibraryHandler(t, func(h *LibraryHandler, c *gin.Context) { h.GetLibraryV21(c) })
			assertStatusError(t, w, http.StatusForbidden, "you do not have access to this library")
		})
	})
}

// serveDeniedFileHandler drives a FileHandler/FileViewHandler endpoint with an
// API-key scope that is insufficient for read access, so HasLibraryAccessCtx
// returns "no access" without a DB round-trip and the handler reaches its
// access-denied branch (and thus respondIfLibraryMissing).
func serveDeniedFileHandler(t *testing.T, route, target string, register func(c *gin.Context)) *httptest.ResponseRecorder {
	t.Helper()
	r := gin.New()
	r.GET(route, func(c *gin.Context) {
		c.Set("org_id", testNotFoundOrgID)
		c.Set("user_id", testNotFoundUserID)
		c.Set("api_key_scope", "none") // insufficient for PermissionR → denied, no DB lookup
		register(c)
	})
	req := httptest.NewRequest("GET", target, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func newDeniedFileHandler() *FileHandler {
	return &FileHandler{db: &dbpkg.DB{}, config: &config.Config{}, permMiddleware: middleware.NewPermissionMiddleware(&dbpkg.DB{})}
}

func TestListDirectory_MissingLibraryReturnsNotFound(t *testing.T) {
	withLibraryStateStub(t, dbpkg.LibraryState{}, gocql.ErrNotFound, func() {
		h := newDeniedFileHandler()
		w := serveDeniedFileHandler(t, "/repos/:repo_id/dir/", "/repos/"+testNotFoundRepoID+"/dir/?p=/",
			func(c *gin.Context) { h.ListDirectory(c) })
		assertStatusError(t, w, http.StatusNotFound, "library not found")
	})
}

func TestListDirectory_ExistingLibraryReturnsForbidden(t *testing.T) {
	withLibraryStateStub(t, dbpkg.LibraryState{}, nil, func() {
		h := newDeniedFileHandler()
		w := serveDeniedFileHandler(t, "/repos/:repo_id/dir/", "/repos/"+testNotFoundRepoID+"/dir/?p=/",
			func(c *gin.Context) { h.ListDirectory(c) })
		assertStatusError(t, w, http.StatusForbidden, "you do not have access to this library")
	})
}

func TestListDirectoryV21_MissingLibraryReturnsNotFound(t *testing.T) {
	withLibraryStateStub(t, dbpkg.LibraryState{}, gocql.ErrNotFound, func() {
		h := newDeniedFileHandler()
		w := serveDeniedFileHandler(t, "/repos/:repo_id/dir/", "/repos/"+testNotFoundRepoID+"/dir/?p=/",
			func(c *gin.Context) { h.ListDirectoryV21(c) })
		assertStatusError(t, w, http.StatusNotFound, "library not found")
	})
}

func TestServeRawFile_MissingLibraryReturnsNotFound(t *testing.T) {
	withLibraryStateStub(t, dbpkg.LibraryState{}, gocql.ErrNotFound, func() {
		h := &FileViewHandler{db: &dbpkg.DB{}, config: &config.Config{}, permMiddleware: middleware.NewPermissionMiddleware(&dbpkg.DB{})}
		w := serveDeniedFileHandler(t, "/repo/:repo_id/raw/*filepath", "/repo/"+testNotFoundRepoID+"/raw/doc.txt",
			func(c *gin.Context) { h.ServeRawFile(c) })
		assertStatusError(t, w, http.StatusNotFound, "library not found")
	})
}

func TestServeRawFile_ExistingLibraryReturnsForbidden(t *testing.T) {
	withLibraryStateStub(t, dbpkg.LibraryState{}, nil, func() {
		h := &FileViewHandler{db: &dbpkg.DB{}, config: &config.Config{}, permMiddleware: middleware.NewPermissionMiddleware(&dbpkg.DB{})}
		w := serveDeniedFileHandler(t, "/repo/:repo_id/raw/*filepath", "/repo/"+testNotFoundRepoID+"/raw/doc.txt",
			func(c *gin.Context) { h.ServeRawFile(c) })
		assertStatusError(t, w, http.StatusForbidden, "you do not have access to this library")
	})
}
