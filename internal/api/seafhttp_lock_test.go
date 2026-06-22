package api

import (
	"bytes"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/gin-gonic/gin"
)

func withSeafHTTPFileLockStub(t *testing.T, stub func(*SeafHTTPHandler, string, string, string) (bool, string, error)) {
	t.Helper()
	old := checkSeafHTTPFileLockedByOther
	checkSeafHTTPFileLockedByOther = stub
	t.Cleanup(func() {
		checkSeafHTTPFileLockedByOther = old
	})
}

func TestSeafHTTPHandleUploadReplaceRejectsLockedFileWithNormalizedPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tokenStore := NewMockTokenStore()
	token, err := tokenStore.CreateUpdateToken("org1", "repo1", "/", "user1")
	if err != nil {
		t.Fatalf("CreateUpdateToken() error = %v", err)
	}

	handler := NewSeafHTTPHandler(nil, storage.NewManager(), nil, tokenStore, nil, nil)
	var seenPath string
	withSeafHTTPFileLockStub(t, func(_ *SeafHTTPHandler, repoID, filePath, userID string) (bool, string, error) {
		seenPath = filePath
		if repoID == "repo1" && filePath == "/folder/sub/file.txt" && userID == "user1" {
			return true, "owner-123", nil
		}
		return false, "", nil
	})

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "ignored.txt")
	if err != nil {
		t.Fatalf("CreateFormFile() error = %v", err)
	}
	if _, err := part.Write([]byte("test content")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := writer.WriteField("relative_path", "folder/sub/file.txt"); err != nil {
		t.Fatalf("WriteField(relative_path) error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close() error = %v", err)
	}

	req := httptest.NewRequest("POST", "/seafhttp/upload-api/"+token, &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()

	r := gin.New()
	r.POST("/seafhttp/upload-api/:token", handler.HandleUpload)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
	if seenPath != "/folder/sub/file.txt" {
		t.Fatalf("lock check path = %q, want %q", seenPath, "/folder/sub/file.txt")
	}
}

func TestSeafHTTPHandleUploadReplaceFailsClosedWhenLockLookupFails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tokenStore := NewMockTokenStore()
	token, err := tokenStore.CreateUpdateToken("org1", "repo1", "/", "user1")
	if err != nil {
		t.Fatalf("CreateUpdateToken() error = %v", err)
	}

	handler := NewSeafHTTPHandler(nil, storage.NewManager(), nil, tokenStore, nil, nil)
	withSeafHTTPFileLockStub(t, func(_ *SeafHTTPHandler, _, _, _ string) (bool, string, error) {
		return false, "", errors.New("lookup failed")
	})

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "file.txt")
	if err != nil {
		t.Fatalf("CreateFormFile() error = %v", err)
	}
	if _, err := part.Write([]byte("test content")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close() error = %v", err)
	}

	req := httptest.NewRequest("POST", "/seafhttp/upload-api/"+token, &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()

	r := gin.New()
	r.POST("/seafhttp/upload-api/:token", handler.HandleUpload)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}
