package v2

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// setupTestRouter creates a test router with auth context set
func setupTestRouter() *gin.Engine {
	r := gin.New()
	r.Use(func(c *gin.Context) {
		// Set test auth context
		c.Set("org_id", "00000000-0000-0000-0000-000000000001")
		c.Set("user_id", "00000000-0000-0000-0000-000000000001")
		c.Next()
	})
	return r
}

// makeRequest is a helper to make HTTP requests
func makeRequest(r *gin.Engine, method, path string, body interface{}) *httptest.ResponseRecorder {
	var reqBody *bytes.Buffer
	if body != nil {
		jsonBody, _ := json.Marshal(body)
		reqBody = bytes.NewBuffer(jsonBody)
	} else {
		reqBody = bytes.NewBuffer(nil)
	}

	req, _ := http.NewRequest(method, path, reqBody)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Token test-token")

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// Test normalizePath function
func TestNormalizePath(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", "/"},
		{"/", "/"},
		{"foo", "/foo"},
		{"/foo", "/foo"},
		{"/foo/", "/foo"},
		{"/foo/bar", "/foo/bar"},
		{"/foo/bar/", "/foo/bar"},
		{"foo/bar", "/foo/bar"},
		{"/foo/../bar", "/bar"},
		{"/foo/./bar", "/foo/bar"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := normalizePath(tt.input)
			if result != tt.expected {
				t.Errorf("normalizePath(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// Test generateSecureToken function
func TestGenerateSecureToken(t *testing.T) {
	token1, err := generateSecureToken(16)
	if err != nil {
		t.Fatalf("generateSecureToken failed: %v", err)
	}

	// Base64 without padding: 16 bytes -> 22 characters (ceiling of 16*8/6)
	expectedLen := 22
	if len(token1) != expectedLen {
		t.Errorf("token length = %d, want %d", len(token1), expectedLen)
	}

	// Tokens should be unique
	token2, _ := generateSecureToken(16)
	if token1 == token2 {
		t.Error("generateSecureToken should generate unique tokens")
	}
}

// Test request binding for CreateLibraryRequest
func TestCreateLibraryRequest(t *testing.T) {
	r := setupTestRouter()

	var receivedReq CreateLibraryRequest
	r.POST("/test", func(c *gin.Context) {
		if err := c.ShouldBindJSON(&receivedReq); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, receivedReq)
	})

	tests := []struct {
		name       string
		body       map[string]interface{}
		wantStatus int
	}{
		{
			name:       "valid request",
			body:       map[string]interface{}{"name": "Test Library", "description": "Test"},
			wantStatus: http.StatusOK,
		},
		{
			name:       "empty name",
			body:       map[string]interface{}{"name": "", "description": "Test"},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "with encryption",
			body:       map[string]interface{}{"name": "Encrypted Lib", "encrypted": true},
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := makeRequest(r, "POST", "/test", tt.body)
			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

// Test request binding for UpdateLibraryRequest
func TestUpdateLibraryRequest(t *testing.T) {
	r := setupTestRouter()

	r.PUT("/test", func(c *gin.Context) {
		var req UpdateLibraryRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, req)
	})

	tests := []struct {
		name       string
		body       map[string]interface{}
		wantStatus int
	}{
		{
			name:       "update name only",
			body:       map[string]interface{}{"name": "New Name"},
			wantStatus: http.StatusOK,
		},
		{
			name:       "update description only",
			body:       map[string]interface{}{"description": "New Description"},
			wantStatus: http.StatusOK,
		},
		{
			name:       "update version_ttl_days",
			body:       map[string]interface{}{"version_ttl_days": 30},
			wantStatus: http.StatusOK,
		},
		{
			name:       "empty body",
			body:       map[string]interface{}{},
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := makeRequest(r, "PUT", "/test", tt.body)
			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

// Test request binding for CreateDirectoryRequest
func TestCreateDirectoryRequest(t *testing.T) {
	r := setupTestRouter()

	r.POST("/test", func(c *gin.Context) {
		var req CreateDirectoryRequest
		if err := c.ShouldBind(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, req)
	})

	tests := []struct {
		name       string
		body       map[string]interface{}
		wantStatus int
	}{
		{
			name:       "valid path",
			body:       map[string]interface{}{"path": "/documents"},
			wantStatus: http.StatusOK,
		},
		{
			name:       "missing path",
			body:       map[string]interface{}{},
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := makeRequest(r, "POST", "/test", tt.body)
			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

// Test request binding for MoveFileRequest
func TestMoveFileRequest(t *testing.T) {
	r := setupTestRouter()

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
		body       map[string]interface{}
		wantStatus int
	}{
		{
			name:       "valid move",
			body:       map[string]interface{}{"src": "/old/path", "dst": "/new/path"},
			wantStatus: http.StatusOK,
		},
		{
			name:       "missing src",
			body:       map[string]interface{}{"dst": "/new/path"},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "missing dst",
			body:       map[string]interface{}{"src": "/old/path"},
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := makeRequest(r, "POST", "/test", tt.body)
			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

// Test request binding for CopyFileRequest
func TestCopyFileRequest(t *testing.T) {
	r := setupTestRouter()

	r.POST("/test", func(c *gin.Context) {
		var req CopyFileRequest
		if err := c.ShouldBind(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, req)
	})

	tests := []struct {
		name       string
		body       map[string]interface{}
		wantStatus int
	}{
		{
			name:       "valid copy",
			body:       map[string]interface{}{"src": "/source/file", "dst": "/dest/file"},
			wantStatus: http.StatusOK,
		},
		{
			name:       "missing src",
			body:       map[string]interface{}{"dst": "/dest/file"},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "missing dst",
			body:       map[string]interface{}{"src": "/source/file"},
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := makeRequest(r, "POST", "/test", tt.body)
			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

// Test request binding for CreateShareLinkRequest
func TestCreateShareLinkRequest(t *testing.T) {
	r := setupTestRouter()

	r.POST("/test", func(c *gin.Context) {
		var req CreateShareLinkRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, req)
	})

	tests := []struct {
		name       string
		body       map[string]interface{}
		wantStatus int
	}{
		{
			name:       "valid share link",
			body:       map[string]interface{}{"repo_id": "00000000-0000-0000-0000-000000000001"},
			wantStatus: http.StatusOK,
		},
		{
			name:       "with all options",
			body:       map[string]interface{}{"repo_id": "00000000-0000-0000-0000-000000000001", "path": "/file.txt", "permission": "download", "expire_days": 7},
			wantStatus: http.StatusOK,
		},
		{
			name:       "missing repo_id",
			body:       map[string]interface{}{"path": "/file.txt"},
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := makeRequest(r, "POST", "/test", tt.body)
			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

// Test request binding for InitiateRestoreRequest
func TestInitiateRestoreRequest(t *testing.T) {
	r := setupTestRouter()

	r.POST("/test", func(c *gin.Context) {
		var req InitiateRestoreRequest
		if err := c.ShouldBind(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, req)
	})

	tests := []struct {
		name       string
		body       map[string]interface{}
		wantStatus int
	}{
		{
			name:       "valid restore request",
			body:       map[string]interface{}{"path": "/archived/file.txt"},
			wantStatus: http.StatusOK,
		},
		{
			name:       "missing path",
			body:       map[string]interface{}{},
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := makeRequest(r, "POST", "/test", tt.body)
			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

// Test request binding for ChangeStorageClassRequest
func TestChangeStorageClassRequest(t *testing.T) {
	r := setupTestRouter()

	r.POST("/test", func(c *gin.Context) {
		var req ChangeStorageClassRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, req)
	})

	tests := []struct {
		name       string
		body       map[string]interface{}
		wantStatus int
	}{
		{
			name:       "valid storage class",
			body:       map[string]interface{}{"storage_class": "cold"},
			wantStatus: http.StatusOK,
		},
		{
			name:       "missing storage_class",
			body:       map[string]interface{}{},
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := makeRequest(r, "POST", "/test", tt.body)
			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}
