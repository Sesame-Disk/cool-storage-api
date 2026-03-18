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

// TestListGroups tests listing groups for a user
func TestListGroups(t *testing.T) {
	r := gin.New()
	handler := &GroupHandler{}

	r.GET("/api/v2.1/groups/", func(c *gin.Context) {
		c.Set("org_id", "test-org-id")
		c.Set("user_id", "test-user-id")
		handler.ListGroups(c)
	})

	req := httptest.NewRequest("GET", "/api/v2.1/groups/", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d. Response: %s", w.Code, http.StatusInternalServerError, w.Body.String())
	}
}

// TestCreateGroup tests creating a new group
func TestCreateGroup(t *testing.T) {
	r := gin.New()
	handler := &GroupHandler{}

	r.POST("/api/v2.1/groups/", func(c *gin.Context) {
		c.Set("org_id", "test-org-id")
		c.Set("user_id", "test-user-id")
		handler.CreateGroup(c)
	})

	tests := []struct {
		name       string
		body       map[string]string
		wantStatus int
		wantError  string
	}{
		{
			name:       "missing group_name",
			body:       map[string]string{},
			wantStatus: http.StatusBadRequest,
			wantError:  "GroupName",
		},
		{
			name: "empty group_name",
			body: map[string]string{
				"name": "",
			},
			wantStatus: http.StatusBadRequest,
			wantError:  "GroupName",
		},
		{
			name: "valid request without DB",
			body: map[string]string{
				"name": "Test Group",
			},
			wantStatus: http.StatusInternalServerError,
			wantError:  "database not available",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(tt.body)
			req := httptest.NewRequest("POST", "/api/v2.1/groups/", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d. Response: %s", w.Code, tt.wantStatus, w.Body.String())
			}

			if tt.wantError != "" && !strings.Contains(w.Body.String(), tt.wantError) {
				t.Errorf("response = %s, want to contain %q", w.Body.String(), tt.wantError)
			}
		})
	}
}

// TestGetGroup tests getting group details
func TestGetGroup(t *testing.T) {
	r := gin.New()
	handler := &GroupHandler{}

	r.GET("/api/v2.1/groups/:group_id/", func(c *gin.Context) {
		c.Set("org_id", "test-org-id")
		c.Set("user_id", "test-user-id")
		handler.GetGroup(c)
	})

	tests := []struct {
		name       string
		groupID    string
		wantStatus int
		wantError  string
	}{
		{
			name:       "missing group_id",
			groupID:    "",
			wantStatus: http.StatusBadRequest,
			wantError:  "group_id is required",
		},
		{
			name:       "invalid group_id",
			groupID:    "not-a-uuid",
			wantStatus: http.StatusBadRequest,
			wantError:  "invalid group_id",
		},
		{
			name:       "valid request without DB",
			groupID:    "123e4567-e89b-12d3-a456-426614174000",
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqURL := "/api/v2.1/groups/" + tt.groupID + "/"
			req := httptest.NewRequest("GET", reqURL, nil)
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d. Response: %s", w.Code, tt.wantStatus, w.Body.String())
			}
		})
	}
}

// TestUpdateGroup tests updating group details
func TestUpdateGroup(t *testing.T) {
	r := gin.New()
	handler := &GroupHandler{}

	r.PUT("/api/v2.1/groups/:group_id/", func(c *gin.Context) {
		c.Set("org_id", "test-org-id")
		c.Set("user_id", "test-user-id")
		handler.UpdateGroup(c)
	})

	tests := []struct {
		name       string
		groupID    string
		body       map[string]string
		wantStatus int
		wantError  string
	}{
		{
			name:       "missing group_name",
			groupID:    "123e4567-e89b-12d3-a456-426614174000",
			body:       map[string]string{},
			wantStatus: http.StatusBadRequest,
			wantError:  "group_name",
		},
		{
			name:    "invalid group_id",
			groupID: "not-a-uuid",
			body: map[string]string{
				"group_name": "New Name",
			},
			wantStatus: http.StatusBadRequest,
			wantError:  "invalid group_id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(tt.body)
			reqURL := "/api/v2.1/groups/" + tt.groupID + "/"
			req := httptest.NewRequest("PUT", reqURL, bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d. Response: %s", w.Code, tt.wantStatus, w.Body.String())
			}
		})
	}
}

// TestDeleteGroup tests deleting a group
func TestDeleteGroup(t *testing.T) {
	r := gin.New()
	handler := &GroupHandler{}

	r.DELETE("/api/v2.1/groups/:group_id/", func(c *gin.Context) {
		c.Set("org_id", "test-org-id")
		c.Set("user_id", "test-user-id")
		handler.DeleteGroup(c)
	})

	tests := []struct {
		name       string
		groupID    string
		wantStatus int
		wantError  string
	}{
		{
			name:       "invalid group_id",
			groupID:    "not-a-uuid",
			wantStatus: http.StatusBadRequest,
			wantError:  "invalid group_id",
		},
		{
			name:       "valid request without DB",
			groupID:    "123e4567-e89b-12d3-a456-426614174000",
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqURL := "/api/v2.1/groups/" + tt.groupID + "/"
			req := httptest.NewRequest("DELETE", reqURL, nil)
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d. Response: %s", w.Code, tt.wantStatus, w.Body.String())
			}
		})
	}
}

// TestListGroupMembers tests listing group members
func TestListGroupMembers(t *testing.T) {
	r := gin.New()
	handler := &GroupHandler{}

	r.GET("/api/v2.1/groups/:group_id/members/", func(c *gin.Context) {
		c.Set("org_id", "test-org-id")
		c.Set("user_id", "test-user-id")
		handler.ListGroupMembers(c)
	})

	tests := []struct {
		name       string
		groupID    string
		wantStatus int
		wantError  string
	}{
		{
			name:       "invalid group_id",
			groupID:    "not-a-uuid",
			wantStatus: http.StatusBadRequest,
			wantError:  "invalid group_id",
		},
		{
			name:       "valid request without DB",
			groupID:    "123e4567-e89b-12d3-a456-426614174000",
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqURL := "/api/v2.1/groups/" + tt.groupID + "/members/"
			req := httptest.NewRequest("GET", reqURL, nil)
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d. Response: %s", w.Code, tt.wantStatus, w.Body.String())
			}
		})
	}
}

// TestAddGroupMember tests adding a member to a group
func TestAddGroupMember(t *testing.T) {
	r := gin.New()
	handler := &GroupHandler{}

	r.POST("/api/v2.1/groups/:group_id/members/", func(c *gin.Context) {
		c.Set("org_id", "test-org-id")
		c.Set("user_id", "test-user-id")
		handler.AddGroupMember(c)
	})

	tests := []struct {
		name       string
		groupID    string
		body       map[string]string
		wantStatus int
		wantError  string
	}{
		{
			name:       "missing email",
			groupID:    "123e4567-e89b-12d3-a456-426614174000",
			body:       map[string]string{},
			wantStatus: http.StatusBadRequest,
			wantError:  "email",
		},
		{
			name:    "empty email",
			groupID: "123e4567-e89b-12d3-a456-426614174000",
			body: map[string]string{
				"email": "",
			},
			wantStatus: http.StatusBadRequest,
			wantError:  "email is required",
		},
		{
			name:    "invalid group_id",
			groupID: "not-a-uuid",
			body: map[string]string{
				"email": "test@example.com",
			},
			wantStatus: http.StatusBadRequest,
			wantError:  "invalid group_id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			formData := url.Values{}
			for key, value := range tt.body {
				formData.Set(key, value)
			}

			reqURL := "/api/v2.1/groups/" + tt.groupID + "/members/"
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

// TestRemoveGroupMember tests removing a member from a group
func TestRemoveGroupMember(t *testing.T) {
	r := gin.New()
	handler := &GroupHandler{}

	r.DELETE("/api/v2.1/groups/:group_id/members/:user_email/", func(c *gin.Context) {
		c.Set("org_id", "test-org-id")
		c.Set("user_id", "test-user-id")
		handler.RemoveGroupMember(c)
	})

	tests := []struct {
		name       string
		groupID    string
		userEmail  string
		wantStatus int
		wantError  string
	}{
		{
			name:       "invalid group_id",
			groupID:    "not-a-uuid",
			userEmail:  "test@example.com",
			wantStatus: http.StatusBadRequest,
			wantError:  "invalid group_id",
		},
		{
			name:       "valid request without DB",
			groupID:    "123e4567-e89b-12d3-a456-426614174000",
			userEmail:  "test@example.com",
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqURL := "/api/v2.1/groups/" + tt.groupID + "/members/" + tt.userEmail + "/"
			req := httptest.NewRequest("DELETE", reqURL, nil)
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d. Response: %s", w.Code, tt.wantStatus, w.Body.String())
			}
		})
	}
}

// TestCreateGroupOwnedLibrary_FailurePaths covers early validation and missing-db behavior.
func TestCreateGroupOwnedLibrary_FailurePaths(t *testing.T) {
	r := gin.New()
	handler := &GroupHandler{}

	r.POST("/api/v2.1/groups/:group_id/group-owned-libraries/", func(c *gin.Context) {
		c.Set("org_id", "test-org-id")
		c.Set("user_id", "test-user-id")
		handler.CreateGroupOwnedLibrary(c)
	})

	tests := []struct {
		name         string
		groupID      string
		form         url.Values
		wantStatus   int
		wantContains string
	}{
		{
			name:         "invalid group id",
			groupID:      "not-a-uuid",
			form:         url.Values{"name": {"Repo"}},
			wantStatus:   http.StatusBadRequest,
			wantContains: "invalid group_id",
		},
		{
			name:         "valid group id without db",
			groupID:      "123e4567-e89b-12d3-a456-426614174000",
			form:         url.Values{"name": {"Repo"}},
			wantStatus:   http.StatusInternalServerError,
			wantContains: "database not available",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqURL := "/api/v2.1/groups/" + tt.groupID + "/group-owned-libraries/"
			req := httptest.NewRequest("POST", reqURL, strings.NewReader(tt.form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d. Response: %s", w.Code, tt.wantStatus, w.Body.String())
			}
			if tt.wantContains != "" && !strings.Contains(w.Body.String(), tt.wantContains) {
				t.Errorf("response = %s, want to contain %q", w.Body.String(), tt.wantContains)
			}
		})
	}
}

// TestDeleteGroupOwnedLibrary_FailurePaths covers early validation and missing-db behavior.
func TestDeleteGroupOwnedLibrary_FailurePaths(t *testing.T) {
	r := gin.New()
	handler := &GroupHandler{}

	r.DELETE("/api/v2.1/groups/:group_id/group-owned-libraries/:library_id/", func(c *gin.Context) {
		c.Set("org_id", "test-org-id")
		c.Set("user_id", "test-user-id")
		handler.DeleteGroupOwnedLibrary(c)
	})

	tests := []struct {
		name         string
		groupID      string
		libraryID    string
		wantStatus   int
		wantContains string
	}{
		{
			name:         "invalid group id",
			groupID:      "not-a-uuid",
			libraryID:    "11111111-1111-1111-1111-111111111111",
			wantStatus:   http.StatusBadRequest,
			wantContains: "invalid group_id",
		},
		{
			name:         "valid group id without db",
			groupID:      "123e4567-e89b-12d3-a456-426614174000",
			libraryID:    "11111111-1111-1111-1111-111111111111",
			wantStatus:   http.StatusInternalServerError,
			wantContains: "database not available",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reqURL := "/api/v2.1/groups/" + tt.groupID + "/group-owned-libraries/" + tt.libraryID + "/"
			req := httptest.NewRequest("DELETE", reqURL, nil)
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d. Response: %s", w.Code, tt.wantStatus, w.Body.String())
			}
			if tt.wantContains != "" && !strings.Contains(w.Body.String(), tt.wantContains) {
				t.Errorf("response = %s, want to contain %q", w.Body.String(), tt.wantContains)
			}
		})
	}
}
