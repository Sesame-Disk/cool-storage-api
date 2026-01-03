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

// Test StarredFile struct JSON serialization
func TestStarredFile_JSON(t *testing.T) {
	sf := StarredFile{
		RepoID:           "repo-123",
		RepoName:         "My Library",
		RepoEncrypted:    false,
		IsDir:            false,
		Path:             "/documents/file.txt",
		ObjName:          "file.txt",
		Mtime:            "2024-01-01T00:00:00+00:00",
		Deleted:          false,
		UserEmail:        "user@example.com",
		UserName:         "user",
		UserContactEmail: "user@example.com",
	}

	data, err := json.Marshal(sf)
	if err != nil {
		t.Fatalf("Failed to marshal StarredFile: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("Failed to unmarshal JSON: %v", err)
	}

	// Verify JSON field names match Seafile v2.1 format
	expectedFields := []string{"repo_id", "repo_name", "repo_encrypted", "is_dir", "path", "obj_name", "mtime", "deleted", "user_email", "user_name", "user_contact_email"}
	for _, field := range expectedFields {
		if _, ok := parsed[field]; !ok {
			t.Errorf("Missing JSON field: %s", field)
		}
	}

	if parsed["repo_id"] != "repo-123" {
		t.Errorf("repo_id = %v, want %q", parsed["repo_id"], "repo-123")
	}
	if parsed["is_dir"] != false {
		t.Errorf("is_dir = %v, want false", parsed["is_dir"])
	}
	if parsed["mtime"] != "2024-01-01T00:00:00+00:00" {
		t.Errorf("mtime = %v, want %q", parsed["mtime"], "2024-01-01T00:00:00+00:00")
	}
}

// Test StarFileRequest struct binding
func TestStarFileRequest_Binding(t *testing.T) {
	r := gin.New()

	r.POST("/test", func(c *gin.Context) {
		var req StarFileRequest
		if err := c.ShouldBind(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, req)
	})

	tests := []struct {
		name       string
		body       string
		wantStatus int
		wantRepoID string
		wantPath   string  // Using "path" JSON tag
		wantPathV2 string  // Using "p" JSON tag (v2 API)
	}{
		{
			name:       "JSON format with path (v2.1)",
			body:       `{"repo_id":"repo-123","path":"/path/to/file.txt"}`,
			wantStatus: http.StatusOK,
			wantRepoID: "repo-123",
			wantPath:   "/path/to/file.txt",
			wantPathV2: "",
		},
		{
			name:       "JSON format with p (v2)",
			body:       `{"repo_id":"repo-456","p":"/legacy/path.txt"}`,
			wantStatus: http.StatusOK,
			wantRepoID: "repo-456",
			wantPath:   "",
			wantPathV2: "/legacy/path.txt",
		},
		{
			name:       "empty body",
			body:       `{}`,
			wantStatus: http.StatusOK,
			wantRepoID: "",
			wantPath:   "",
			wantPathV2: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/test", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}

			if tt.wantStatus == http.StatusOK {
				var result StarFileRequest
				if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
					t.Fatalf("Failed to parse response: %v", err)
				}
				if result.RepoID != tt.wantRepoID {
					t.Errorf("RepoID = %q, want %q", result.RepoID, tt.wantRepoID)
				}
				if result.Path != tt.wantPath {
					t.Errorf("Path = %q, want %q", result.Path, tt.wantPath)
				}
				if result.PathV2 != tt.wantPathV2 {
					t.Errorf("PathV2 = %q, want %q", result.PathV2, tt.wantPathV2)
				}
			}
		})
	}
}

// Test StarFileRequest form binding
func TestStarFileRequest_FormBinding(t *testing.T) {
	r := gin.New()

	r.POST("/test", func(c *gin.Context) {
		var req StarFileRequest
		if err := c.ShouldBind(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, req)
	})

	// Test with "path" form field (v2.1 API)
	form := url.Values{}
	form.Set("repo_id", "repo-456")
	form.Set("path", "/another/path.txt")

	req := httptest.NewRequest("POST", "/test", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var result StarFileRequest
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}
	if result.RepoID != "repo-456" {
		t.Errorf("RepoID = %q, want %q", result.RepoID, "repo-456")
	}
	if result.Path != "/another/path.txt" {
		t.Errorf("Path = %q, want %q", result.Path, "/another/path.txt")
	}
}

// Test StarFileRequest form binding with legacy "p" parameter
func TestStarFileRequest_FormBinding_LegacyP(t *testing.T) {
	r := gin.New()

	r.POST("/test", func(c *gin.Context) {
		var req StarFileRequest
		if err := c.ShouldBind(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, req)
	})

	// Test with "p" form field (v2 API legacy)
	form := url.Values{}
	form.Set("repo_id", "repo-789")
	form.Set("p", "/legacy/file.txt")

	req := httptest.NewRequest("POST", "/test", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var result StarFileRequest
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}
	if result.RepoID != "repo-789" {
		t.Errorf("RepoID = %q, want %q", result.RepoID, "repo-789")
	}
	if result.PathV2 != "/legacy/file.txt" {
		t.Errorf("PathV2 = %q, want %q", result.PathV2, "/legacy/file.txt")
	}
}

// Test NewStarredHandler
func TestNewStarredHandler(t *testing.T) {
	handler := NewStarredHandler(nil)
	if handler == nil {
		t.Error("NewStarredHandler returned nil")
	}
	if handler.db != nil {
		t.Error("handler.db should be nil when passed nil")
	}
}

// Test ListStarredFiles without auth returns error
func TestListStarredFiles_NoAuth(t *testing.T) {
	r := gin.New()
	handler := NewStarredHandler(nil)

	r.GET("/starredfiles", handler.ListStarredFiles)

	req := httptest.NewRequest("GET", "/starredfiles", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

// Test StarFile without auth returns error
func TestStarFile_NoAuth(t *testing.T) {
	r := gin.New()
	handler := NewStarredHandler(nil)

	r.POST("/starredfiles", handler.StarFile)

	req := httptest.NewRequest("POST", "/starredfiles", strings.NewReader(`{"repo_id":"test","p":"/file.txt"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

// Test UnstarFile without auth returns error
func TestUnstarFile_NoAuth(t *testing.T) {
	r := gin.New()
	handler := NewStarredHandler(nil)

	r.DELETE("/starredfiles", handler.UnstarFile)

	req := httptest.NewRequest("DELETE", "/starredfiles?repo_id=test&p=/file.txt", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

// Test StarFile missing repo_id
func TestStarFile_MissingRepoID(t *testing.T) {
	r := gin.New()
	handler := NewStarredHandler(nil)

	r.POST("/starredfiles", func(c *gin.Context) {
		c.Set("user_id", "user-123")
		handler.StarFile(c)
	})

	req := httptest.NewRequest("POST", "/starredfiles", strings.NewReader(`{"p":"/file.txt"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// Test StarFile missing path
func TestStarFile_MissingPath(t *testing.T) {
	r := gin.New()
	handler := NewStarredHandler(nil)

	r.POST("/starredfiles", func(c *gin.Context) {
		c.Set("user_id", "user-123")
		handler.StarFile(c)
	})

	req := httptest.NewRequest("POST", "/starredfiles", strings.NewReader(`{"repo_id":"repo-123"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// Test UnstarFile missing repo_id
func TestUnstarFile_MissingRepoID(t *testing.T) {
	r := gin.New()
	handler := NewStarredHandler(nil)

	r.DELETE("/starredfiles", func(c *gin.Context) {
		c.Set("user_id", "user-123")
		handler.UnstarFile(c)
	})

	req := httptest.NewRequest("DELETE", "/starredfiles?p=/file.txt", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// Test UnstarFile missing path
func TestUnstarFile_MissingPath(t *testing.T) {
	r := gin.New()
	handler := NewStarredHandler(nil)

	r.DELETE("/starredfiles", func(c *gin.Context) {
		c.Set("user_id", "user-123")
		handler.UnstarFile(c)
	})

	req := httptest.NewRequest("DELETE", "/starredfiles?repo_id=repo-123", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// Test RegisterStarredRoutes creates routes
func TestRegisterStarredRoutes(t *testing.T) {
	r := gin.New()
	api := r.Group("/api2")

	handler := RegisterStarredRoutes(api, nil)

	if handler == nil {
		t.Error("RegisterStarredRoutes returned nil")
	}

	// Verify routes are registered by checking they don't return 404
	routes := r.Routes()
	foundRoutes := make(map[string]bool)
	for _, route := range routes {
		foundRoutes[route.Method+" "+route.Path] = true
	}

	expectedRoutes := []string{
		"GET /api2/starredfiles",
		"GET /api2/starredfiles/",
		"POST /api2/starredfiles",
		"POST /api2/starredfiles/",
		"DELETE /api2/starredfiles",
		"DELETE /api2/starredfiles/",
	}

	for _, expected := range expectedRoutes {
		if !foundRoutes[expected] {
			t.Errorf("Route not registered: %s", expected)
		}
	}
}
