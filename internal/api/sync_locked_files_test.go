package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/gin-gonic/gin"
)

func withListRepoLocksStub(t *testing.T, stub func(h *SyncHandler, repoID string) ([]db.RepoLockedFile, error)) {
	t.Helper()
	old := listRepoLocksFn
	listRepoLocksFn = stub
	t.Cleanup(func() {
		listRepoLocksFn = old
	})
}

func newTestSyncHandler() *SyncHandler {
	return NewSyncHandler(nil, nil, nil, nil, nil, nil)
}

// GetFolderPerm's wire format was confirmed live against a genuine Seafile Pro
// 11.0.16 instance (2026-07-02): the response must be a JSON array, not an
// object — the previous `{}` response was not protocol-correct even though it
// never produced a visible client error.
func TestGetFolderPerm_ReturnsEmptyArrayNotObject(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := newTestSyncHandler()

	r := gin.New()
	r.GET("/seafhttp/repo/folder-perm", handler.GetFolderPerm)
	r.POST("/seafhttp/repo/folder-perm", handler.GetFolderPerm)

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		req := httptest.NewRequest(method, "/seafhttp/repo/folder-perm", strings.NewReader(`[{"repo_id":"r1","token":"t","ts":0}]`))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", method, w.Code)
		}
		if got := strings.TrimSpace(w.Body.String()); got != "[]" {
			t.Fatalf("%s body = %q, want %q", method, got, "[]")
		}
	}
}

func TestGetLockedFiles_InvalidJSONReturns400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := newTestSyncHandler()

	r := gin.New()
	r.POST("/seafhttp/repo/locked-files", handler.GetLockedFiles)

	req := httptest.NewRequest(http.MethodPost, "/seafhttp/repo/locked-files", strings.NewReader(`not json`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

// Verified live against a genuine Seafile Pro 11.0.16 instance (2026-07-02):
// repos with no locks are omitted entirely from the response array, rather
// than included with an empty locked_files list.
func TestGetLockedFiles_OmitsReposWithNoLocks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := newTestSyncHandler()
	withListRepoLocksStub(t, func(h *SyncHandler, repoID string) ([]db.RepoLockedFile, error) {
		return nil, nil
	})

	r := gin.New()
	r.POST("/seafhttp/repo/locked-files", handler.GetLockedFiles)

	req := httptest.NewRequest(http.MethodPost, "/seafhttp/repo/locked-files",
		strings.NewReader(`[{"repo_id":"repo-with-no-locks","token":"t","ts":0}]`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := strings.TrimSpace(w.Body.String()); got != "[]" {
		t.Fatalf("body = %q, want %q", got, "[]")
	}
}

func TestGetLockedFiles_IncludesLockedRepoWithByMeFalse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := newTestSyncHandler()
	withListRepoLocksStub(t, func(h *SyncHandler, repoID string) ([]db.RepoLockedFile, error) {
		if repoID == "repo-1" {
			return []db.RepoLockedFile{{Path: "/doc.docx", LockedBy: "user-1"}}, nil
		}
		return nil, nil
	})

	r := gin.New()
	r.POST("/seafhttp/repo/locked-files", handler.GetLockedFiles)

	req := httptest.NewRequest(http.MethodPost, "/seafhttp/repo/locked-files",
		strings.NewReader(`[{"repo_id":"repo-1","token":"t","ts":0},{"repo_id":"repo-2","token":"t","ts":0}]`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"repo_id":"repo-1"`) {
		t.Fatalf("body = %q, want it to contain repo-1's entry", body)
	}
	if !strings.Contains(body, `"path":"/doc.docx"`) || !strings.Contains(body, `"by_me":false`) {
		t.Fatalf("body = %q, want path and by_me:false for the locked file", body)
	}
	if strings.Contains(body, `"repo_id":"repo-2"`) {
		t.Fatalf("body = %q, repo-2 has no locks and should be omitted", body)
	}
}

func TestGetLockedFiles_SkipsEmptyRepoID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := newTestSyncHandler()
	called := false
	withListRepoLocksStub(t, func(h *SyncHandler, repoID string) ([]db.RepoLockedFile, error) {
		called = true
		return nil, nil
	})

	r := gin.New()
	r.POST("/seafhttp/repo/locked-files", handler.GetLockedFiles)

	req := httptest.NewRequest(http.MethodPost, "/seafhttp/repo/locked-files", strings.NewReader(`[{"repo_id":""}]`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if called {
		t.Fatal("listRepoLocksFn should not be called for an empty repo_id")
	}
	if got := strings.TrimSpace(w.Body.String()); got != "[]" {
		t.Fatalf("body = %q, want %q", got, "[]")
	}
}
