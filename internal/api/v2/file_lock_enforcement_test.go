package v2

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/gin-gonic/gin"
)

func withCheckFileLockedByOtherStub(t *testing.T, stub func(*FileHandler, string, string, string) (bool, string, error)) {
	t.Helper()
	old := checkFileLockedByOther
	checkFileLockedByOther = stub
	t.Cleanup(func() {
		checkFileLockedByOther = old
	})
}

func withCheckSubtreeLockedByOtherStub(t *testing.T, stub func(*FileHandler, string, string, string) (bool, string, error)) {
	t.Helper()
	old := checkSubtreeLockedByOther
	checkSubtreeLockedByOther = stub
	t.Cleanup(func() {
		checkSubtreeLockedByOther = old
	})
}

func decodeJSONMap(t *testing.T, body *bytes.Buffer) map[string]interface{} {
	t.Helper()
	var resp map[string]interface{}
	if err := json.Unmarshal(body.Bytes(), &resp); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	return resp
}

func newTestGinContext() (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	return ctx, recorder
}

func TestMoveFile_BatchRejectsLockedFile(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withCheckSubtreeLockedByOtherStub(t, func(_ *FileHandler, repoID, filePath, userID string) (bool, string, error) {
		if repoID == "repo-1" && filePath == "/source/locked.txt" && userID == "test-user" {
			return true, "owner-123", nil
		}
		return false, "", nil
	})

	r := gin.New()
	handler := &FileHandler{}
	r.POST("/repos/:repo_id/file/move", func(c *gin.Context) {
		c.Set("org_id", "test-org")
		c.Set("user_id", "test-user")
		handler.MoveFile(c)
	})

	body, _ := json.Marshal(map[string]interface{}{
		"src_dir":  "/source",
		"dst_dir":  "/destination",
		"filename": []string{"locked.txt", "open.txt"},
	})
	req := httptest.NewRequest("POST", "/repos/repo-1/file/move", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
	resp := decodeJSONMap(t, w.Body)
	if got := resp["lock_owner"]; got != "owner-123" {
		t.Fatalf("lock_owner = %v, want %q", got, "owner-123")
	}
}

func TestMoveFile_LockLookupFailureReturns503(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withCheckSubtreeLockedByOtherStub(t, func(_ *FileHandler, _, _, _ string) (bool, string, error) {
		return false, "", errors.New("lookup failed")
	})

	r := gin.New()
	handler := &FileHandler{}
	r.POST("/repos/:repo_id/file/move", func(c *gin.Context) {
		c.Set("org_id", "test-org")
		c.Set("user_id", "test-user")
		handler.MoveFile(c)
	})

	body, _ := json.Marshal(map[string]interface{}{
		"src_path": "/source/file.txt",
		"dst_dir":  "/destination",
	})
	req := httptest.NewRequest("POST", "/repos/repo-1/file/move", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
	resp := decodeJSONMap(t, w.Body)
	if got := resp["error"]; got != "failed to verify file lock" {
		t.Fatalf("error = %v, want %q", got, "failed to verify file lock")
	}
}

func TestBatchDeleteItems_RejectsLockedFile(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withCheckSubtreeLockedByOtherStub(t, func(_ *FileHandler, repoID, filePath, userID string) (bool, string, error) {
		if repoID == "repo-1" && filePath == "/locked.txt" && userID == "test-user" {
			return true, "owner-456", nil
		}
		return false, "", nil
	})

	r := gin.New()
	handler := &FileHandler{}
	r.DELETE("/batch-delete", func(c *gin.Context) {
		c.Set("org_id", "test-org")
		c.Set("user_id", "test-user")
		handler.BatchDeleteItems(c)
	})

	body, _ := json.Marshal(BatchDeleteRequest{RepoID: "repo-1", ParentDir: "/", Dirents: []string{"locked.txt"}})
	req := httptest.NewRequest("DELETE", "/batch-delete", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
	resp := decodeJSONMap(t, w.Body)
	if got := resp["lock_owner"]; got != "owner-456" {
		t.Fatalf("lock_owner = %v, want %q", got, "owner-456")
	}
}

func TestRevertFile_ReplaceRejectsLockedFile(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withCheckFileLockedByOtherStub(t, func(_ *FileHandler, repoID, filePath, userID string) (bool, string, error) {
		if repoID == "repo-1" && filePath == "/locked.txt" && userID == "test-user" {
			return true, "owner-321", nil
		}
		return false, "", nil
	})

	r := gin.New()
	handler := &FileHandler{}
	r.POST("/repos/:repo_id/file/revert", func(c *gin.Context) {
		c.Set("org_id", "test-org")
		c.Set("user_id", "test-user")
		handler.RevertFile(c)
	})

	body, _ := json.Marshal(map[string]interface{}{"commit_id": "deadbeef", "conflict_policy": "replace"})
	req := httptest.NewRequest("POST", "/repos/repo-1/file/revert?p=/locked.txt", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
	resp := decodeJSONMap(t, w.Body)
	if got := resp["lock_owner"]; got != "owner-321" {
		t.Fatalf("lock_owner = %v, want %q", got, "owner-321")
	}
}

func TestRevertFile_LockLookupFailureReturns503(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withCheckFileLockedByOtherStub(t, func(_ *FileHandler, _, _, _ string) (bool, string, error) {
		return false, "", errors.New("lookup failed")
	})

	r := gin.New()
	handler := &FileHandler{}
	r.POST("/repos/:repo_id/file/revert", func(c *gin.Context) {
		c.Set("org_id", "test-org")
		c.Set("user_id", "test-user")
		handler.RevertFile(c)
	})

	body, _ := json.Marshal(map[string]interface{}{"commit_id": "deadbeef", "conflict_policy": "replace"})
	req := httptest.NewRequest("POST", "/repos/repo-1/file/revert?p=/locked.txt", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

// A non-replace revert restores under a new name (or skips) and never overwrites the
// locked file, so the lock check must not even be consulted - guarding against a future
// change that would make enforcement unconditional and break autorename reverts.
func TestRequireReplaceRevertFileNotLockedByOther_AutorenameSkipsLockCheck(t *testing.T) {
	gin.SetMode(gin.TestMode)
	called := false
	withCheckFileLockedByOtherStub(t, func(_ *FileHandler, _, _, _ string) (bool, string, error) {
		called = true
		return true, "owner-321", nil
	})

	ctx, recorder := newTestGinContext()
	handler := &FileHandler{}

	if !handler.requireReplaceRevertFileNotLockedByOther(ctx, "repo-1", "/locked.txt", "test-user", "autorename") {
		t.Fatal("guard returned false for autorename; want true without consulting the lock check")
	}
	if called {
		t.Fatalf("lock check was consulted for a non-replace revert; want it skipped")
	}
	if ctx.IsAborted() {
		t.Fatal("context was aborted, want it left untouched")
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("response body = %q, want empty", recorder.Body.String())
	}
}

func TestRevertDirectory_ReplaceRejectsLockedSubtree(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withCheckSubtreeLockedByOtherStub(t, func(_ *FileHandler, repoID, dirPath, userID string) (bool, string, error) {
		if repoID == "repo-1" && dirPath == "/locked-dir" && userID == "test-user" {
			return true, "owner-654", nil
		}
		return false, "", nil
	})

	r := gin.New()
	handler := &FileHandler{}
	r.POST("/repos/:repo_id/dir/revert", func(c *gin.Context) {
		c.Set("org_id", "test-org")
		c.Set("user_id", "test-user")
		handler.RevertDirectory(c)
	})

	body, _ := json.Marshal(map[string]interface{}{"commit_id": "deadbeef", "conflict_policy": "replace"})
	req := httptest.NewRequest("POST", "/repos/repo-1/dir/revert?p=/locked-dir", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
	resp := decodeJSONMap(t, w.Body)
	if got := resp["lock_owner"]; got != "owner-654" {
		t.Fatalf("lock_owner = %v, want %q", got, "owner-654")
	}
}

func TestRevertDirectory_LockLookupFailureReturns503(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withCheckSubtreeLockedByOtherStub(t, func(_ *FileHandler, _, _, _ string) (bool, string, error) {
		return false, "", errors.New("lookup failed")
	})

	r := gin.New()
	handler := &FileHandler{}
	r.POST("/repos/:repo_id/dir/revert", func(c *gin.Context) {
		c.Set("org_id", "test-org")
		c.Set("user_id", "test-user")
		handler.RevertDirectory(c)
	})

	body, _ := json.Marshal(map[string]interface{}{"commit_id": "deadbeef", "conflict_policy": "replace"})
	req := httptest.NewRequest("POST", "/repos/repo-1/dir/revert?p=/locked-dir", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

// A non-replace directory revert restores under a new name (or skips), so it never
// clobbers a file locked inside the subtree and must not consult the lock check.
func TestRequireReplaceRevertDirectoryNotLockedByOther_KeepBothSkipsLockCheck(t *testing.T) {
	gin.SetMode(gin.TestMode)
	called := false
	withCheckSubtreeLockedByOtherStub(t, func(_ *FileHandler, _, _, _ string) (bool, string, error) {
		called = true
		return true, "owner-654", nil
	})

	ctx, recorder := newTestGinContext()
	handler := &FileHandler{}

	if !handler.requireReplaceRevertDirectoryNotLockedByOther(ctx, "repo-1", "/locked-dir", "test-user", "keep_both") {
		t.Fatal("guard returned false for keep_both; want true without consulting the lock check")
	}
	if called {
		t.Fatalf("subtree lock check was consulted for a non-replace revert; want it skipped")
	}
	if ctx.IsAborted() {
		t.Fatal("context was aborted, want it left untouched")
	}
	if recorder.Body.Len() != 0 {
		t.Fatalf("response body = %q, want empty", recorder.Body.String())
	}
}

func TestRenameDirectory_RejectsLockedSubtree(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withCheckSubtreeLockedByOtherStub(t, func(_ *FileHandler, repoID, dirPath, userID string) (bool, string, error) {
		if repoID == "repo-1" && dirPath == "/locked-dir" && userID == "test-user" {
			return true, "owner-111", nil
		}
		return false, "", nil
	})

	r := gin.New()
	handler := &FileHandler{}
	r.POST("/repos/:repo_id/dir", func(c *gin.Context) {
		c.Set("org_id", "test-org")
		c.Set("user_id", "test-user")
		handler.RenameDirectory(c)
	})

	body, _ := json.Marshal(map[string]interface{}{"newname": "renamed"})
	req := httptest.NewRequest("POST", "/repos/repo-1/dir?p=/locked-dir", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
	resp := decodeJSONMap(t, w.Body)
	if got := resp["lock_owner"]; got != "owner-111" {
		t.Fatalf("lock_owner = %v, want %q", got, "owner-111")
	}
}

func TestDeleteDirectory_RejectsLockedSubtree(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withCheckSubtreeLockedByOtherStub(t, func(_ *FileHandler, repoID, dirPath, userID string) (bool, string, error) {
		if repoID == "repo-1" && dirPath == "/locked-dir" && userID == "test-user" {
			return true, "owner-222", nil
		}
		return false, "", nil
	})

	r := gin.New()
	handler := &FileHandler{}
	r.DELETE("/repos/:repo_id/dir", func(c *gin.Context) {
		c.Set("org_id", "test-org")
		c.Set("user_id", "test-user")
		handler.DeleteDirectory(c)
	})

	req := httptest.NewRequest("DELETE", "/repos/repo-1/dir?p=/locked-dir", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
	resp := decodeJSONMap(t, w.Body)
	if got := resp["lock_owner"]; got != "owner-222" {
		t.Fatalf("lock_owner = %v, want %q", got, "owner-222")
	}
}

func TestRenameDirectory_LockLookupFailureReturns503(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withCheckSubtreeLockedByOtherStub(t, func(_ *FileHandler, _, _, _ string) (bool, string, error) {
		return false, "", errors.New("lookup failed")
	})

	r := gin.New()
	handler := &FileHandler{}
	r.POST("/repos/:repo_id/dir", func(c *gin.Context) {
		c.Set("org_id", "test-org")
		c.Set("user_id", "test-user")
		handler.RenameDirectory(c)
	})

	body, _ := json.Marshal(map[string]interface{}{"newname": "renamed"})
	req := httptest.NewRequest("POST", "/repos/repo-1/dir?p=/locked-dir", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestDeleteDirectory_LockLookupFailureReturns503(t *testing.T) {
	gin.SetMode(gin.TestMode)
	withCheckSubtreeLockedByOtherStub(t, func(_ *FileHandler, _, _, _ string) (bool, string, error) {
		return false, "", errors.New("lookup failed")
	})

	r := gin.New()
	handler := &FileHandler{}
	r.DELETE("/repos/:repo_id/dir", func(c *gin.Context) {
		c.Set("org_id", "test-org")
		c.Set("user_id", "test-user")
		handler.DeleteDirectory(c)
	})

	req := httptest.NewRequest("DELETE", "/repos/repo-1/dir?p=/locked-dir", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestLockFile_ConflictReturns409(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldAcquire := acquireFileLock
	acquireFileLock = func(_ *FileHandler, repoID, filePath, userID string, _ time.Time) (db.LockAcquireResult, string, error) {
		if repoID == "00000000-0000-0000-0000-000000000001" && filePath == "/locked.txt" && userID == "00000000-0000-0000-0000-000000000002" {
			return db.LockConflict, "owner-789", nil
		}
		return db.LockAcquired, userID, nil
	}
	t.Cleanup(func() { acquireFileLock = oldAcquire })

	r := gin.New()
	handler := &FileHandler{}
	r.PUT("/repos/:repo_id/file", func(c *gin.Context) {
		c.Set("org_id", "test-org")
		c.Set("user_id", "00000000-0000-0000-0000-000000000002")
		handler.LockFile(c)
	})

	req := httptest.NewRequest("PUT", "/repos/00000000-0000-0000-0000-000000000001/file?p=/locked.txt&operation=lock", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusConflict)
	}
	resp := decodeJSONMap(t, w.Body)
	if got := resp["lock_owner"]; got != "owner-789" {
		t.Fatalf("lock_owner = %v, want %q", got, "owner-789")
	}
}

// LockFile must refuse to create a lock on a path the FS tree does not back: a phantom
// lock row would make SubtreeLockedByOther block legitimate operations on the real
// parent (e.g. locking /empty-dir/ghost.txt blocks rename/delete of /empty-dir, and
// /foo.txt/ghost blocks the real file /foo.txt).

func withLockTargetStateStub(t *testing.T, stub func(*FileHandler, string, string) (bool, bool, error)) {
	t.Helper()
	old := lockTargetState
	lockTargetState = stub
	t.Cleanup(func() { lockTargetState = old })
}

func newLockRequest(repoID, p string) (*http.Request, *httptest.ResponseRecorder) {
	req := httptest.NewRequest("PUT", "/repos/"+repoID+"/file?p="+p+"&operation=lock", nil)
	return req, httptest.NewRecorder()
}

func lockRouter(handler *FileHandler) *gin.Engine {
	r := gin.New()
	r.PUT("/repos/:repo_id/file", func(c *gin.Context) {
		c.Set("org_id", "test-org")
		c.Set("user_id", "00000000-0000-0000-0000-000000000002")
		handler.LockFile(c)
	})
	return r
}

func TestLockFile_RejectsNonexistentPathWith404(t *testing.T) {
	gin.SetMode(gin.TestMode)
	acquireCalled := false
	oldAcquire := acquireFileLock
	acquireFileLock = func(_ *FileHandler, _, _, _ string, _ time.Time) (db.LockAcquireResult, string, error) {
		acquireCalled = true
		return db.LockAcquired, "", nil
	}
	t.Cleanup(func() { acquireFileLock = oldAcquire })
	// Parent exists but the leaf does not → exists=false, no error.
	withLockTargetStateStub(t, func(_ *FileHandler, _, _ string) (bool, bool, error) {
		return false, false, nil
	})

	req, w := newLockRequest("00000000-0000-0000-0000-000000000001", "/empty-dir/ghost.txt")
	lockRouter(&FileHandler{}).ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
	if acquireCalled {
		t.Fatal("acquireFileLock was called for a nonexistent path; want it skipped")
	}
}

func TestLockFile_RejectsDirectoryWith400(t *testing.T) {
	gin.SetMode(gin.TestMode)
	acquireCalled := false
	oldAcquire := acquireFileLock
	acquireFileLock = func(_ *FileHandler, _, _, _ string, _ time.Time) (db.LockAcquireResult, string, error) {
		acquireCalled = true
		return db.LockAcquired, "", nil
	}
	t.Cleanup(func() { acquireFileLock = oldAcquire })
	withLockTargetStateStub(t, func(_ *FileHandler, _, _ string) (bool, bool, error) {
		return true, true, nil // exists, isDir
	})

	req, w := newLockRequest("00000000-0000-0000-0000-000000000001", "/some-dir")
	lockRouter(&FileHandler{}).ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if acquireCalled {
		t.Fatal("acquireFileLock was called for a directory; want it skipped")
	}
}

// A traversal that cannot resolve the path (e.g. descending into a file like
// /foo.txt/ghost, or a DB error) fails closed: 503 and no lock created.
func TestLockFile_TargetLookupFailureFailsClosedWith503(t *testing.T) {
	gin.SetMode(gin.TestMode)
	acquireCalled := false
	oldAcquire := acquireFileLock
	acquireFileLock = func(_ *FileHandler, _, _, _ string, _ time.Time) (db.LockAcquireResult, string, error) {
		acquireCalled = true
		return db.LockAcquired, "", nil
	}
	t.Cleanup(func() { acquireFileLock = oldAcquire })
	withLockTargetStateStub(t, func(_ *FileHandler, _, _ string) (bool, bool, error) {
		return false, false, errors.New("cannot descend into file")
	})

	req, w := newLockRequest("00000000-0000-0000-0000-000000000001", "/foo.txt/ghost")
	lockRouter(&FileHandler{}).ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
	if acquireCalled {
		t.Fatal("acquireFileLock was called despite an unverifiable target; want it skipped")
	}
}
