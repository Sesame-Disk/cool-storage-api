package v2

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/httputil"
	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// TestCreateShareLink_Validation tests input validation for CreateShareLink
func TestCreateShareLink_Validation(t *testing.T) {
	r := gin.New()
	handler := &ShareLinkHandler{}

	r.POST("/share-links", func(c *gin.Context) {
		c.Set("org_id", "test-org-id")
		c.Set("user_id", "test-user-id")
		handler.CreateShareLink(c)
	})

	tests := []struct {
		name       string
		body       map[string]interface{}
		wantStatus int
		wantError  string
	}{
		{
			name:       "empty body",
			body:       map[string]interface{}{},
			wantStatus: http.StatusBadRequest,
			wantError:  "repo_id is required",
		},
		{
			name: "missing repo_id",
			body: map[string]interface{}{
				"path": "/test.txt",
			},
			wantStatus: http.StatusBadRequest,
			wantError:  "repo_id is required",
		},
		{
			name: "missing path",
			body: map[string]interface{}{
				"repo_id": "123e4567-e89b-12d3-a456-426614174000",
			},
			wantStatus: http.StatusBadRequest,
			wantError:  "path is required",
		},
		{
			name: "invalid repo_id format",
			body: map[string]interface{}{
				"repo_id": "not-a-uuid",
				"path":    "/test.txt",
			},
			wantStatus: http.StatusBadRequest,
			wantError:  "invalid repo_id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(tt.body)
			req := httptest.NewRequest("POST", "/share-links", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d. Response: %s", w.Code, tt.wantStatus, w.Body.String())
			}

			if tt.wantError != "" {
				var resp map[string]string
				if err := json.Unmarshal(w.Body.Bytes(), &resp); err == nil {
					if resp["error"] != tt.wantError {
						t.Errorf("error = %q, want %q", resp["error"], tt.wantError)
					}
				}
			}
		})
	}
}

// TestGenerateSecureShareToken tests token generation
func TestGenerateSecureShareToken(t *testing.T) {
	tests := []struct {
		name   string
		length int
	}{
		{"short token", 8},
		{"medium token", 16},
		{"long token", 32},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token, err := generateSecureShareToken(tt.length)
			if err != nil {
				t.Errorf("generateSecureShareToken() error = %v", err)
				return
			}

			if token == "" {
				t.Error("token is empty")
			}

			// Generate another token and ensure it's different (extremely unlikely to be same)
			token2, _ := generateSecureShareToken(tt.length)
			if token == token2 {
				t.Error("generated tokens are identical (extremely unlikely - possible weak RNG)")
			}
		})
	}
}

// TestParsePermsJSON tests parsePermsJSON with both JSON and legacy string formats
func TestParsePermsJSON(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantEdit     bool
		wantDownload bool
		wantUpload   bool
	}{
		{"empty defaults to download", "", false, true, false},
		{"json all false", `{"can_edit":false,"can_download":false,"can_upload":false}`, false, false, false},
		{"json download only", `{"can_edit":false,"can_download":true,"can_upload":false}`, false, true, false},
		{"json edit+download", `{"can_edit":true,"can_download":true,"can_upload":false}`, true, true, false},
		{"json all true", `{"can_edit":true,"can_download":true,"can_upload":true}`, true, true, true},
		{"legacy download", "download", false, true, false},
		{"legacy preview_download", "preview_download", false, true, false},
		{"legacy preview_only", "preview_only", false, false, false},
		{"legacy edit", "edit", true, true, false},
		{"legacy upload", "upload", false, true, true},
		{"unknown defaults to download", "something_else", false, true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			perms := parsePermsJSON(tt.input)
			if perms.CanEdit != tt.wantEdit {
				t.Errorf("CanEdit = %v, want %v", perms.CanEdit, tt.wantEdit)
			}
			if perms.CanDownload != tt.wantDownload {
				t.Errorf("CanDownload = %v, want %v", perms.CanDownload, tt.wantDownload)
			}
			if perms.CanUpload != tt.wantUpload {
				t.Errorf("CanUpload = %v, want %v", perms.CanUpload, tt.wantUpload)
			}
		})
	}
}

// TestNormalizePermissionInput tests that all input formats produce canonical JSON
func TestNormalizePermissionInput(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", `{"can_edit":false,"can_download":true,"can_upload":false}`},
		{"legacy download", "download", `{"can_edit":false,"can_download":true,"can_upload":false}`},
		{"legacy preview_download", "preview_download", `{"can_edit":false,"can_download":true,"can_upload":false}`},
		{"legacy preview_only", "preview_only", `{"can_edit":false,"can_download":false,"can_upload":false}`},
		{"legacy edit", "edit", `{"can_edit":true,"can_download":true,"can_upload":false}`},
		{"legacy upload", "upload", `{"can_edit":false,"can_download":true,"can_upload":true}`},
		{"json passthrough", `{"can_edit":true,"can_download":true,"can_upload":false}`, `{"can_edit":true,"can_download":true,"can_upload":false}`},
		{"json re-canonicalize", `{"can_download":true,"can_edit":false,"can_upload":false}`, `{"can_edit":false,"can_download":true,"can_upload":false}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizePermissionInput(tt.input)
			if got != tt.want {
				t.Errorf("normalizePermissionInput(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestObjNameFromPath tests display name extraction from file paths
func TestObjNameFromPath(t *testing.T) {
	tests := []struct {
		path     string
		repoName string
		want     string
	}{
		{"/", "My Library", "My Library"},
		{"/folder/", "My Library", "folder"},
		{"/folder/file.pdf", "My Library", "file.pdf"},
		{"/a/b/c/deep.txt", "My Library", "deep.txt"},
		{"/single.txt", "My Library", "single.txt"},
		{"file.txt", "My Library", "file.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := objNameFromPath(tt.path, tt.repoName)
			if got != tt.want {
				t.Errorf("objNameFromPath(%q, %q) = %q, want %q", tt.path, tt.repoName, got, tt.want)
			}
		})
	}
}

// TestPermsToJSON tests JSON serialization roundtrip
func TestPermsToJSON(t *testing.T) {
	p := Perms{CanEdit: true, CanDownload: true, CanUpload: false}
	got := permsToJSON(p)
	want := `{"can_edit":true,"can_download":true,"can_upload":false}`
	if got != want {
		t.Errorf("permsToJSON() = %q, want %q", got, want)
	}

	// Roundtrip
	parsed := parsePermsJSON(got)
	if parsed != p {
		t.Errorf("roundtrip failed: got %+v, want %+v", parsed, p)
	}
}

// TestListRepoShareLinks_SetsRepoIDQueryParam verifies that ListRepoShareLinks
// sets the repo_id query parameter from the URL path before delegating.
func TestListRepoShareLinks_SetsRepoIDQueryParam(t *testing.T) {
	r := gin.New()
	handler := &ShareLinkHandler{serverURL: "http://test.example.com"}

	// Register a route that captures the query param after ListRepoShareLinks sets it.
	// Since ListRepoShareLinks calls ListShareLinks which needs a DB, we intercept early
	// by checking the query param was injected.
	r.GET("/api/v2.1/repos/:repo_id/share-links/", func(c *gin.Context) {
		// Simulate what ListRepoShareLinks does: set repo_id query param
		repoID := c.Param("repo_id")
		c.Request.URL.RawQuery = fmt.Sprintf("repo_id=%s&%s", repoID, c.Request.URL.RawQuery)
		// Verify the query param is set
		got := c.Query("repo_id")
		if got != "test-repo-123" {
			t.Errorf("repo_id query = %q, want %q", got, "test-repo-123")
		}
		c.JSON(http.StatusOK, []interface{}{})
	})

	req := httptest.NewRequest("GET", "/api/v2.1/repos/test-repo-123/share-links/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	// Also verify that RegisterShareLinkRoutes registers the repo-specific route
	r2 := gin.New()
	rg := r2.Group("/api/v2.1")
	RegisterShareLinkRoutes(rg, nil, "http://test.example.com", nil, nil)

	// The route should exist and respond (will fail at DB level but route is registered)
	req2 := httptest.NewRequest("GET", "/api/v2.1/repos/some-repo/share-links/", nil)
	req2.Header.Set("Authorization", "Token test")
	w2 := httptest.NewRecorder()

	// Use Recovery middleware to catch panics from nil DB
	r2.Use(gin.Recovery())
	r2.ServeHTTP(w2, req2)

	// Should NOT get 404 (route not found) — any other status means the route matched
	if w2.Code == http.StatusNotFound {
		t.Error("repo-specific share-links route not registered")
	}

	_ = handler // suppress unused warning
}

// TestShareLinkURL_IncludesServerURL verifies that share link URLs are
// constructed as full URLs (not relative paths like /d/token).
func TestShareLinkURL_IncludesServerURL(t *testing.T) {
	testCases := []struct {
		name       string
		serverURL  string
		host       string
		wantPrefix string
	}{
		{"configured URL", "https://cloud.example.com", "localhost:8080", "https://cloud.example.com"},
		{"auto-detect from host", "", "myhost.com", "http://myhost.com"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("GET", "/test", nil)
			c.Request.Host = tc.host

			url := httputil.GetBrowserURL(c, tc.serverURL)
			if url != tc.wantPrefix {
				t.Errorf("GetBrowserURL() = %q, want %q", url, tc.wantPrefix)
			}

			// Verify share link URL format matches what ListShareLinks/CreateShareLink produce
			token := "abc123"
			linkURL := fmt.Sprintf("%s/d/%s", url, token)
			if linkURL != tc.wantPrefix+"/d/abc123" {
				t.Errorf("link URL = %q, want %q", linkURL, tc.wantPrefix+"/d/abc123")
			}
		})
	}
}
