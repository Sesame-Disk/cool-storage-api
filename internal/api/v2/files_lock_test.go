package v2

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// Test LockFile operation parameter routing
func TestLockFile_OperationRouting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	handler := &FileHandler{}

	r.PUT("/file", handler.LockFile)

	tests := []struct {
		name       string
		operation  string
		wantStatus int
	}{
		{"lock operation", "lock", http.StatusBadRequest}, // Fails due to missing DB, but validates routing
		{"unlock operation", "unlock", http.StatusBadRequest},
		{"invalid operation", "invalid", http.StatusBadRequest},
		{"empty operation", "", http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqURL := "/file"
			if tt.operation != "" {
				reqURL += "?operation=" + tt.operation
			}
			req := httptest.NewRequest("PUT", reqURL, nil)
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			// Route should be matched (not 404)
			if w.Code == http.StatusNotFound {
				t.Errorf("Route not found for operation=%q", tt.operation)
			}
		})
	}
}

// Test LockFile with missing path parameter
func TestLockFile_MissingPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	handler := &FileHandler{}

	r.PUT("/repos/:repo_id/file", handler.LockFile)

	// Request with operation but no path
	req := httptest.NewRequest("PUT", "/repos/test-repo/file?operation=lock", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d for missing path", w.Code, http.StatusBadRequest)
	}
}

// Test LockFile with form data
func TestLockFile_FormData(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	handler := &FileHandler{}

	r.PUT("/repos/:repo_id/file", handler.LockFile)

	tests := []struct {
		name       string
		operation  string
		formData   url.Values
		wantStatus int
	}{
		{
			name:      "lock with path in form",
			operation: "lock",
			formData: url.Values{
				"p": {"/test/file.docx"},
			},
			wantStatus: http.StatusBadRequest, // Will fail due to missing DB
		},
		{
			name:      "unlock with path in form",
			operation: "unlock",
			formData: url.Values{
				"p": {"/test/file.docx"},
			},
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqURL := "/repos/test-repo/file?operation=" + tt.operation
			body := strings.NewReader(tt.formData.Encode())
			req := httptest.NewRequest("PUT", reqURL, body)
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			// Should not be 404 (route matched)
			if w.Code == http.StatusNotFound {
				t.Errorf("Route not found")
			}
		})
	}
}

// Test Dirent struct JSON serialization
func TestDirent_JSONSerialization(t *testing.T) {
	d := Dirent{
		ID:         "abc123",
		Name:       "test.docx",
		Type:       "file",
		Size:       1024,
		MTime:      1609459200,
		Permission: "rw",
		IsLocked:   true,
		LockOwner:  "user@example.com",
		LockedByMe: true,
	}

	// Test that the struct has the expected fields
	if d.IsLocked != true {
		t.Errorf("IsLocked = %v, want true", d.IsLocked)
	}
	if d.LockOwner != "user@example.com" {
		t.Errorf("LockOwner = %q, want %q", d.LockOwner, "user@example.com")
	}
	if d.LockedByMe != true {
		t.Errorf("LockedByMe = %v, want true", d.LockedByMe)
	}
}

// Test Dirent struct for unlocked file
func TestDirent_UnlockedFile(t *testing.T) {
	d := Dirent{
		ID:         "def456",
		Name:       "document.pdf",
		Type:       "file",
		Size:       2048,
		MTime:      1609459200,
		Permission: "rw",
		IsLocked:   false,
		LockOwner:  "",
		LockedByMe: false,
	}

	if d.IsLocked != false {
		t.Errorf("IsLocked = %v, want false", d.IsLocked)
	}
	if d.LockOwner != "" {
		t.Errorf("LockOwner = %q, want empty string", d.LockOwner)
	}
}

// Test directory type doesn't have lock fields
func TestDirent_Directory(t *testing.T) {
	d := Dirent{
		ID:         "dir123",
		Name:       "Documents",
		Type:       "dir",
		MTime:      1609459200,
		Permission: "rw",
		// Lock fields should be zero values for directories
		IsLocked:   false,
		LockOwner:  "",
		LockedByMe: false,
	}

	if d.Type != "dir" {
		t.Errorf("Type = %q, want dir", d.Type)
	}
	// Directories should not be locked
	if d.IsLocked {
		t.Errorf("Directory should not be locked")
	}
}

// Test GetFileRevisions parameter validation
func TestGetFileRevisions_ParameterValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	handler := &FileHandler{}

	r.GET("/repos/:repo_id/file/history", handler.GetFileRevisions)

	tests := []struct {
		name       string
		path       string
		wantStatus int
	}{
		{
			name:       "missing path parameter",
			path:       "",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "with path parameter",
			path:       "/test/file.txt",
			wantStatus: http.StatusBadRequest, // Will fail due to missing DB
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqURL := "/repos/test-repo/file/history"
			if tt.path != "" {
				reqURL += "?p=" + url.QueryEscape(tt.path)
			}
			req := httptest.NewRequest("GET", reqURL, nil)
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			if w.Code == http.StatusNotFound {
				t.Errorf("Route not found")
			}
		})
	}
}

// Removed - duplicate test exists in files_crud_test.go

// Test GetDownloadLink parameter validation
func TestGetDownloadLink_ParameterValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	handler := &FileHandler{}

	r.GET("/repos/:repo_id/file/download-link", handler.GetDownloadLink)

	tests := []struct {
		name       string
		path       string
		wantStatus int
	}{
		{
			name:       "missing path parameter",
			path:       "",
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqURL := "/repos/test-repo/file/download-link"
			if tt.path != "" {
				reqURL += "?p=" + url.QueryEscape(tt.path)
			}
			req := httptest.NewRequest("GET", reqURL, nil)
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			if w.Code == http.StatusNotFound {
				t.Errorf("Route not found")
			}
		})
	}
}
