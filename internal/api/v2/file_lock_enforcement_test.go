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
