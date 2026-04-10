package v2

import (
	"bytes"
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

// TestListSharedItems tests listing shares for a repo/path
func TestListSharedItems(t *testing.T) {
	r := gin.New()
	handler := &FileShareHandler{}

	r.GET("/api2/repos/:repo_id/dir/shared_items/", func(c *gin.Context) {
		c.Set("org_id", "test-org-id")
		c.Set("user_id", "test-user-id")
		handler.ListSharedItems(c)
	})

	tests := []struct {
		name       string
		repoID     string
		path       string
		shareType  string
		wantStatus int
		wantError  string
	}{
		{
			name:       "missing repo_id",
			repoID:     "",
			path:       "/",
			shareType:  "user",
			wantStatus: http.StatusBadRequest,
			wantError:  "repo_id is required",
		},
		{
			name:       "invalid repo_id",
			repoID:     "not-a-uuid",
			path:       "/",
			shareType:  "user",
			wantStatus: http.StatusBadRequest,
			wantError:  "invalid repo_id",
		},
		{
			name:       "valid request without DB",
			repoID:     "123e4567-e89b-12d3-a456-426614174000",
			path:       "/test",
			shareType:  "user",
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqURL := "/api2/repos/" + tt.repoID + "/dir/shared_items/"
			if tt.path != "" {
				reqURL += "?p=" + url.QueryEscape(tt.path)
			}
			if tt.shareType != "" {
				if strings.Contains(reqURL, "?") {
					reqURL += "&share_type=" + tt.shareType
				} else {
					reqURL += "?share_type=" + tt.shareType
				}
			}

			req := httptest.NewRequest("GET", reqURL, nil)
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d. Response: %s", w.Code, tt.wantStatus, w.Body.String())
			}
		})
	}
}

// TestCreateShare_Validation tests input validation paths that don't require database
func TestCreateShare_Validation(t *testing.T) {
	r := gin.New()
	handler := &FileShareHandler{}

	r.PUT("/api2/repos/:repo_id/dir/shared_items/", func(c *gin.Context) {
		c.Set("org_id", "test-org-id")
		c.Set("user_id", "test-user-id")
		handler.CreateShare(c)
	})

	tests := []struct {
		name       string
		repoID     string
		path       string
		body       map[string]interface{}
		wantStatus int
		wantError  string
	}{
		{
			name:   "missing share_type",
			repoID: "123e4567-e89b-12d3-a456-426614174000",
			path:   "/",
			body: map[string]interface{}{
				"permission": "r",
			},
			wantStatus: http.StatusBadRequest,
			wantError:  "share_type is required",
		},
		{
			name:   "invalid repo_id",
			repoID: "not-a-uuid",
			path:   "/",
			body: map[string]interface{}{
				"share_type": "user",
				"permission": "r",
				"username":   []string{"test@example.com"},
			},
			wantStatus: http.StatusBadRequest,
			wantError:  "invalid repo_id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqURL := "/api2/repos/" + tt.repoID + "/dir/shared_items/"
			if tt.path != "" {
				reqURL += "?p=" + url.QueryEscape(tt.path)
			}

			bodyBytes, err := json.Marshal(tt.body)
			if err != nil {
				t.Fatalf("marshal body: %v", err)
			}

			req := httptest.NewRequest("PUT", reqURL, bytes.NewReader(bodyBytes))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d. Response: %s", w.Code, tt.wantStatus, w.Body.String())
			}
		})
	}
}

// TestCreateShare_Integration tests share creation paths that require database
func TestCreateShare_Integration(t *testing.T) {
	t.Skip("Requires database connection - run as integration test")
	r := gin.New()
	handler := &FileShareHandler{}

	r.PUT("/api2/repos/:repo_id/dir/shared_items/", func(c *gin.Context) {
		c.Set("org_id", "test-org-id")
		c.Set("user_id", "test-user-id")
		handler.CreateShare(c)
	})

	tests := []struct {
		name       string
		repoID     string
		path       string
		body       map[string]interface{}
		wantStatus int
		wantError  string
	}{
		{
			name:   "share to user missing username",
			repoID: "123e4567-e89b-12d3-a456-426614174000",
			path:   "/test",
			body: map[string]interface{}{
				"share_type": "user",
				"permission": "rw",
			},
			wantStatus: http.StatusBadRequest,
			wantError:  "username is required",
		},
		{
			name:   "share to group missing group_id",
			repoID: "123e4567-e89b-12d3-a456-426614174000",
			path:   "/test",
			body: map[string]interface{}{
				"share_type": "group",
				"permission": "r",
			},
			wantStatus: http.StatusBadRequest,
			wantError:  "group_id is required",
		},
		{
			name:   "invalid share_type",
			repoID: "123e4567-e89b-12d3-a456-426614174000",
			path:   "/",
			body: map[string]interface{}{
				"share_type": "invalid",
				"permission": "r",
				"username":   []string{"test@example.com"},
			},
			wantStatus: http.StatusBadRequest,
			wantError:  "invalid share_type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqURL := "/api2/repos/" + tt.repoID + "/dir/shared_items/"
			if tt.path != "" {
				reqURL += "?p=" + url.QueryEscape(tt.path)
			}

			bodyBytes, err := json.Marshal(tt.body)
			if err != nil {
				t.Fatalf("marshal body: %v", err)
			}

			req := httptest.NewRequest("PUT", reqURL, bytes.NewReader(bodyBytes))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d. Response: %s", w.Code, tt.wantStatus, w.Body.String())
			}
		})
	}
}

// TestUpdateSharePermission tests updating share permissions
func TestUpdateSharePermission(t *testing.T) {
	r := gin.New()
	handler := &FileShareHandler{}

	r.POST("/api2/repos/:repo_id/dir/shared_items/", func(c *gin.Context) {
		c.Set("org_id", "test-org-id")
		c.Set("user_id", "test-user-id")
		handler.UpdateSharePermission(c)
	})

	tests := []struct {
		name       string
		repoID     string
		path       string
		shareType  string
		username   string
		groupID    string
		permission string
		wantStatus int
		wantError  string
	}{
		{
			name:       "missing permission",
			repoID:     "123e4567-e89b-12d3-a456-426614174000",
			path:       "/",
			shareType:  "user",
			username:   "test@example.com",
			permission: "",
			wantStatus: http.StatusBadRequest,
			wantError:  "permission is required",
		},
		{
			name:       "missing username and group_id",
			repoID:     "123e4567-e89b-12d3-a456-426614174000",
			path:       "/",
			shareType:  "user",
			permission: "rw",
			wantStatus: http.StatusBadRequest,
			wantError:  "username or group_id is required",
		},
		{
			name:       "invalid repo_id",
			repoID:     "not-a-uuid",
			path:       "/",
			shareType:  "user",
			username:   "test@example.com",
			permission: "r",
			wantStatus: http.StatusBadRequest,
			wantError:  "invalid repo_id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqURL := "/api2/repos/" + tt.repoID + "/dir/shared_items/"
			queryParams := url.Values{}
			if tt.path != "" {
				queryParams.Set("p", tt.path)
			}
			if tt.shareType != "" {
				queryParams.Set("share_type", tt.shareType)
			}
			if tt.username != "" {
				queryParams.Set("username", tt.username)
			}
			if tt.groupID != "" {
				queryParams.Set("group_id", tt.groupID)
			}
			if len(queryParams) > 0 {
				reqURL += "?" + queryParams.Encode()
			}

			formData := url.Values{}
			if tt.permission != "" {
				formData.Set("permission", tt.permission)
			}

			req := httptest.NewRequest("POST", reqURL, strings.NewReader(formData.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d. Response: %s", w.Code, tt.wantStatus, w.Body.String())
			}
		})
	}
}

// TestDeleteShare tests deleting shares
func TestDeleteShare(t *testing.T) {
	r := gin.New()
	handler := &FileShareHandler{}

	r.DELETE("/api2/repos/:repo_id/dir/shared_items/", func(c *gin.Context) {
		c.Set("org_id", "test-org-id")
		c.Set("user_id", "test-user-id")
		handler.DeleteShare(c)
	})

	tests := []struct {
		name       string
		repoID     string
		path       string
		shareType  string
		username   string
		groupID    string
		wantStatus int
		wantError  string
	}{
		{
			name:       "missing username and group_id",
			repoID:     "123e4567-e89b-12d3-a456-426614174000",
			path:       "/",
			shareType:  "user",
			wantStatus: http.StatusBadRequest,
			wantError:  "username or group_id is required",
		},
		{
			name:       "invalid repo_id",
			repoID:     "not-a-uuid",
			path:       "/",
			shareType:  "user",
			username:   "test@example.com",
			wantStatus: http.StatusBadRequest,
			wantError:  "invalid repo_id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqURL := "/api2/repos/" + tt.repoID + "/dir/shared_items/"
			queryParams := url.Values{}
			if tt.path != "" {
				queryParams.Set("p", tt.path)
			}
			if tt.shareType != "" {
				queryParams.Set("share_type", tt.shareType)
			}
			if tt.username != "" {
				queryParams.Set("username", tt.username)
			}
			if tt.groupID != "" {
				queryParams.Set("group_id", tt.groupID)
			}
			if len(queryParams) > 0 {
				reqURL += "?" + queryParams.Encode()
			}

			req := httptest.NewRequest("DELETE", reqURL, nil)
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d. Response: %s", w.Code, tt.wantStatus, w.Body.String())
			}
		})
	}
}
