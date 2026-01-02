package v2

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// Test DirectoryOperation routing
func TestDirectoryOperation_Routing(t *testing.T) {
	r := gin.New()
	handler := &FileHandler{}

	// Mock routes
	r.POST("/dir", handler.DirectoryOperation)

	tests := []struct {
		name      string
		operation string
		wantCalled string
	}{
		{"default mkdir", "", "mkdir"},
		{"explicit mkdir", "mkdir", "mkdir"},
		{"rename", "rename", "rename"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := "/dir"
			if tt.operation != "" {
				url += "?operation=" + tt.operation
			}
			req := httptest.NewRequest("POST", url, nil)
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			// The handler will fail due to missing DB, but we can verify routing
			// Just ensure it doesn't return 404 (route matched)
			if w.Code == http.StatusNotFound {
				t.Errorf("Route not found for operation=%q", tt.operation)
			}
		})
	}
}

// Test DirectoryOperation invalid operation
func TestDirectoryOperation_InvalidOperation(t *testing.T) {
	r := gin.New()
	handler := &FileHandler{}

	r.POST("/dir", handler.DirectoryOperation)

	req := httptest.NewRequest("POST", "/dir?operation=invalid", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// Test FileOperation routing
func TestFileOperation_Routing(t *testing.T) {
	r := gin.New()
	handler := &FileHandler{}

	r.POST("/file", handler.FileOperation)

	tests := []struct {
		name       string
		operation  string
		wantStatus int
	}{
		{"no operation", "", http.StatusBadRequest},
		{"rename", "rename", http.StatusBadRequest}, // Will fail due to missing params, but route matches
		{"create", "create", http.StatusBadRequest}, // Will fail due to missing params, but route matches
		{"invalid", "invalid", http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := "/file"
			if tt.operation != "" {
				url += "?operation=" + tt.operation
			}
			req := httptest.NewRequest("POST", url, nil)
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

// Test DeleteFile missing path
func TestDeleteFile_MissingPath(t *testing.T) {
	r := gin.New()
	handler := &FileHandler{}

	r.DELETE("/file", handler.DeleteFile)

	req := httptest.NewRequest("DELETE", "/file", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// Test DeleteFile root path
func TestDeleteFile_RootPath(t *testing.T) {
	r := gin.New()
	handler := &FileHandler{}

	r.DELETE("/file", handler.DeleteFile)

	req := httptest.NewRequest("DELETE", "/file?p=/", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// Test DeleteDirectory invalid path
func TestDeleteDirectory_InvalidPath(t *testing.T) {
	r := gin.New()
	handler := &FileHandler{}

	r.DELETE("/dir", handler.DeleteDirectory)

	tests := []struct {
		name string
		path string
	}{
		{"empty path", ""},
		{"root path", "/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := "/dir"
			if tt.path != "" {
				url += "?p=" + tt.path
			}
			req := httptest.NewRequest("DELETE", url, nil)
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
			}
		})
	}
}

// Test RenameFile missing params
func TestRenameFile_MissingParams(t *testing.T) {
	r := gin.New()
	handler := &FileHandler{}

	r.POST("/file", func(c *gin.Context) {
		c.Request.URL.RawQuery = "operation=rename"
		handler.FileOperation(c)
	})

	tests := []struct {
		name     string
		path     string
		newname  string
		wantCode int
	}{
		{"missing path", "", "newname.txt", http.StatusBadRequest},
		{"missing newname", "/file.txt", "", http.StatusBadRequest},
		{"root path", "/", "newname.txt", http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			form := url.Values{}
			if tt.newname != "" {
				form.Set("newname", tt.newname)
			}

			url := "/file?operation=rename"
			if tt.path != "" {
				url += "&p=" + tt.path
			}

			req := httptest.NewRequest("POST", url, strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			if w.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", w.Code, tt.wantCode)
			}
		})
	}
}

// Test RenameDirectory missing params
func TestRenameDirectory_MissingParams(t *testing.T) {
	r := gin.New()
	handler := &FileHandler{}

	r.POST("/dir", func(c *gin.Context) {
		c.Request.URL.RawQuery = "operation=rename"
		handler.DirectoryOperation(c)
	})

	tests := []struct {
		name     string
		path     string
		newname  string
		wantCode int
	}{
		{"missing path", "", "newdir", http.StatusBadRequest},
		{"root path", "/", "newdir", http.StatusBadRequest},
		{"missing newname", "/dir", "", http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			form := url.Values{}
			if tt.newname != "" {
				form.Set("newname", tt.newname)
			}

			url := "/dir?operation=rename"
			if tt.path != "" {
				url += "&p=" + tt.path
			}

			req := httptest.NewRequest("POST", url, strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			if w.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", w.Code, tt.wantCode)
			}
		})
	}
}

// Test CreateFile missing path
func TestCreateFile_MissingPath(t *testing.T) {
	r := gin.New()
	handler := &FileHandler{}

	r.POST("/file", func(c *gin.Context) {
		c.Request.URL.RawQuery = "operation=create"
		handler.FileOperation(c)
	})

	req := httptest.NewRequest("POST", "/file?operation=create", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// Test CreateDirectory root path
func TestCreateDirectory_RootPath(t *testing.T) {
	r := gin.New()
	handler := &FileHandler{}

	r.POST("/dir", handler.DirectoryOperation)

	req := httptest.NewRequest("POST", "/dir?p=/", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// Test GetFileInfo missing path
func TestGetFileInfo_MissingPath(t *testing.T) {
	r := gin.New()
	handler := &FileHandler{}

	r.GET("/file", handler.GetFileInfo)

	req := httptest.NewRequest("GET", "/file", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// Test GetFileDetail missing path
func TestGetFileDetail_MissingPath(t *testing.T) {
	r := gin.New()
	handler := &FileHandler{}

	r.GET("/file/detail", handler.GetFileDetail)

	req := httptest.NewRequest("GET", "/file/detail", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// Test MoveFile missing params
func TestMoveFile_MissingParams(t *testing.T) {
	r := gin.New()
	handler := &FileHandler{}

	r.POST("/file/move", handler.MoveFile)

	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{"missing src", `{"dst_dir":"/dest/"}`, true},
		{"missing dst", `{"src_path":"/source/file.txt"}`, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/file/move", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			if tt.wantErr && w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
			}
		})
	}
}

// Test CopyFile missing params
func TestCopyFile_MissingParams(t *testing.T) {
	r := gin.New()
	handler := &FileHandler{}

	r.POST("/file/copy", handler.CopyFile)

	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{"missing src", `{"dst_dir":"/dest/"}`, true},
		{"missing dst", `{"src_path":"/source/file.txt"}`, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/file/copy", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			if tt.wantErr && w.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
			}
		})
	}
}

// Test MoveFileRequest struct binding
func TestMoveFileRequest_StructBinding(t *testing.T) {
	r := gin.New()

	r.POST("/test", func(c *gin.Context) {
		var req MoveFileRequest
		if err := c.ShouldBind(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, req)
	})

	tests := []struct {
		name       string
		body       string
		wantFields map[string]string
	}{
		{
			name: "new format",
			body: `{"src_repo_id":"repo1","src_path":"/old.txt","dst_repo_id":"repo2","dst_dir":"/new/"}`,
			wantFields: map[string]string{
				"SrcRepoID": "repo1",
				"SrcPath":   "/old.txt",
				"DstRepoID": "repo2",
			},
		},
		{
			name: "legacy format with filename",
			body: `{"src_dir":"/path/","filename":"file.txt"}`,
			wantFields: map[string]string{
				"SrcDir":   "/path/",
				"Filename": "file.txt",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/test", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
				return
			}

			var result map[string]interface{}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatalf("Failed to parse response: %v", err)
			}

			// Response uses JSON field names, convert to check
			fieldMap := map[string]string{
				"SrcRepoID": "src_repo_id",
				"SrcPath":   "src_path",
				"DstRepoID": "dst_repo_id",
				"DstDir":    "dst_dir",
				"SrcDir":    "src_dir",
				"Filename":  "filename",
			}

			for field, expected := range tt.wantFields {
				jsonField := fieldMap[field]
				if result[jsonField] != expected {
					t.Errorf("%s = %v, want %q", field, result[jsonField], expected)
				}
			}
		})
	}
}

// Test CopyFileRequest struct binding
func TestCopyFileRequest_StructBinding(t *testing.T) {
	r := gin.New()

	r.POST("/test", func(c *gin.Context) {
		var req CopyFileRequest
		if err := c.ShouldBind(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, req)
	})

	body := `{"src_repo_id":"repo1","src_path":"/source.txt","dst_repo_id":"repo2","dst_dir":"/dest/"}`
	req := httptest.NewRequest("POST", "/test", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}
