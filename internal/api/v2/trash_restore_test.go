package v2

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRestoreTrashItem_AcceptsJSONBody(t *testing.T) {
	r := gin.New()
	h := &TrashHandler{}
	r.POST("/api/v2.1/repos/:repo_id/file/restore/", h.RestoreTrashItem)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v2.1/repos/test-repo/file/restore/",
		strings.NewReader(`{"commit_id":"test-commit","p":"/test.png"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	if strings.Contains(w.Body.String(), "commit_id is required") {
		t.Fatalf("body = %s, should not report missing commit_id", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "path is required") {
		t.Fatalf("body = %s, should not report missing path", w.Body.String())
	}
}

func TestRestoreTrashItem_MissingCommitIDStillFails(t *testing.T) {
	r := gin.New()
	h := &TrashHandler{}
	r.POST("/api/v2.1/repos/:repo_id/file/restore/", h.RestoreTrashItem)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v2.1/repos/test-repo/file/restore/",
		strings.NewReader(`{"p":"/test.png"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if !strings.Contains(w.Body.String(), "commit_id is required") {
		t.Fatalf("body = %s, want missing commit_id error", w.Body.String())
	}
}

func TestRestoreTrashItem_MissingPathStillFails(t *testing.T) {
	r := gin.New()
	h := &TrashHandler{}
	r.POST("/api/v2.1/repos/:repo_id/file/restore/", h.RestoreTrashItem)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v2.1/repos/test-repo/file/restore/",
		strings.NewReader(`{"commit_id":"test-commit"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if !strings.Contains(w.Body.String(), "path is required") {
		t.Fatalf("body = %s, want missing path error", w.Body.String())
	}
}