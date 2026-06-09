package v2

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
)

func withDeletedLibraryStateStub(t *testing.T, fn func()) {
	t.Helper()
	original := readLiveLibraryStateFn
	readLiveLibraryStateFn = func(_ *gocql.Session, _, _ string) (dbpkg.LibraryState, error) {
		return dbpkg.LibraryState{}, dbpkg.ErrLibraryDeleted
	}
	defer func() {
		readLiveLibraryStateFn = original
	}()
	fn()
}

func withDeletedLibraryByIDStub(t *testing.T, fn func()) {
	t.Helper()
	original := resolveLiveLibraryStateByIDFn
	resolveLiveLibraryStateByIDFn = func(_ *gocql.Session, _ string) (dbpkg.LibraryState, error) {
		return dbpkg.LibraryState{}, dbpkg.ErrLibraryDeleted
	}
	defer func() {
		resolveLiveLibraryStateByIDFn = original
	}()
	fn()
}

func assertJSONError(t *testing.T, body *bytes.Buffer, want string) {
	t.Helper()
	var resp map[string]any
	if err := json.Unmarshal(body.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp["error"] != want {
		t.Fatalf("error = %v, want %q", resp["error"], want)
	}
}

func TestStarFile_DeletedLibraryReturnsNotFound(t *testing.T) {
	withDeletedLibraryStateStub(t, func() {
		r := gin.New()
		handler := NewStarredHandler(&dbpkg.DB{})
		r.POST("/starredfiles", func(c *gin.Context) {
			c.Set("org_id", "00000000-0000-0000-0000-000000000001")
			c.Set("user_id", "00000000-0000-0000-0000-000000000002")
			handler.StarFile(c)
		})

		req := httptest.NewRequest("POST", "/starredfiles", strings.NewReader(`{"repo_id":"11111111-1111-1111-1111-111111111111","path":"/doc.txt"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		r.ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
		}
		assertJSONError(t, w.Body, "library not found")
	})
}

func TestUnstarFile_DeletedLibraryReturnsNotFound(t *testing.T) {
	withDeletedLibraryStateStub(t, func() {
		r := gin.New()
		handler := NewStarredHandler(&dbpkg.DB{})
		r.DELETE("/starredfiles", func(c *gin.Context) {
			c.Set("org_id", "00000000-0000-0000-0000-000000000001")
			c.Set("user_id", "00000000-0000-0000-0000-000000000002")
			handler.UnstarFile(c)
		})

		req := httptest.NewRequest("DELETE", "/starredfiles?repo_id=11111111-1111-1111-1111-111111111111&path=/doc.txt", nil)
		w := httptest.NewRecorder()

		r.ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
		}
		assertJSONError(t, w.Body, "library not found")
	})
}

func TestMonitorRepo_DeletedLibraryReturnsNotFound(t *testing.T) {
	withDeletedLibraryStateStub(t, func() {
		r := gin.New()
		handler := NewMonitoredRepoHandler(&dbpkg.DB{})
		r.POST("/monitored-repos", func(c *gin.Context) {
			c.Set("org_id", "00000000-0000-0000-0000-000000000001")
			c.Set("user_id", "00000000-0000-0000-0000-000000000002")
			handler.MonitorRepo(c)
		})

		req := httptest.NewRequest("POST", "/monitored-repos", strings.NewReader(`{"repo_id":"11111111-1111-1111-1111-111111111111"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		r.ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
		}
		assertJSONError(t, w.Body, "library not found")
	})
}

func TestCreateShareLink_DeletedLibraryReturnsNotFound(t *testing.T) {
	withDeletedLibraryStateStub(t, func() {
		r := gin.New()
		handler := NewShareLinkHandler(&dbpkg.DB{}, "http://test.example.com", nil, nil)
		r.POST("/share-links", func(c *gin.Context) {
			c.Set("org_id", "00000000-0000-0000-0000-000000000001")
			c.Set("user_id", "00000000-0000-0000-0000-000000000002")
			handler.CreateShareLink(c)
		})

		req := httptest.NewRequest("POST", "/share-links", strings.NewReader(`{"repo_id":"11111111-1111-1111-1111-111111111111","path":"/doc.txt"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		r.ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
		}
		assertJSONError(t, w.Body, "library not found")
	})
}

func TestCreateUploadLink_DeletedLibraryReturnsNotFound(t *testing.T) {
	withDeletedLibraryStateStub(t, func() {
		r := gin.New()
		handler := NewUploadLinkHandler(&dbpkg.DB{}, "http://test.example.com", nil, nil)
		r.POST("/upload-links", func(c *gin.Context) {
			c.Set("org_id", "00000000-0000-0000-0000-000000000001")
			c.Set("user_id", "00000000-0000-0000-0000-000000000002")
			handler.CreateUploadLink(c)
		})

		req := httptest.NewRequest("POST", "/upload-links", strings.NewReader(`{"repo_id":"11111111-1111-1111-1111-111111111111","path":"/incoming/"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		r.ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
		}
		assertJSONError(t, w.Body, "library not found")
	})
}

func TestCreateShare_DeletedLibraryReturnsNotFound(t *testing.T) {
	withDeletedLibraryByIDStub(t, func() {
		r := gin.New()
		handler := NewFileShareHandler(&dbpkg.DB{})
		r.PUT("/repos/:repo_id/dir/shared_items/", func(c *gin.Context) {
			c.Set("org_id", "00000000-0000-0000-0000-000000000001")
			c.Set("user_id", "00000000-0000-0000-0000-000000000002")
			handler.CreateShare(c)
		})

		req := httptest.NewRequest("PUT", "/repos/11111111-1111-1111-1111-111111111111/dir/shared_items/?p=/", strings.NewReader(`{"share_type":"user","username":["user@example.com"]}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		r.ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
		}
		assertJSONError(t, w.Body, "library not found")
	})
}
