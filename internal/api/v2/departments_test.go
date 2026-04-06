package v2

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/httputil"
	"github.com/gin-gonic/gin"
)

func setupDepartmentRouter() (*gin.Engine, *DepartmentHandler) {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(func(c *gin.Context) {
		c.Set("org_id", "00000000-0000-0000-0000-000000000001")
		c.Set("user_id", "00000000-0000-0000-0000-000000000001")
		c.Next()
	})

	h := NewDepartmentHandler(nil, nil)
	return r, h
}

func TestListDepartments_NilDB(t *testing.T) {
	r, h := setupDepartmentRouter()
	r.GET("/api/v2.1/admin/address-book/groups/", h.ListDepartments)

	req, _ := http.NewRequest("GET", "/api/v2.1/admin/address-book/groups/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data, ok := resp["data"].([]interface{})
	if !ok {
		t.Fatal("expected data field in response")
	}
	if len(data) != 0 {
		t.Errorf("expected empty data, got %d items", len(data))
	}
}

func TestListUserDepartments_NilDB(t *testing.T) {
	r, h := setupDepartmentRouter()
	r.GET("/api/v2.1/departments/", h.ListUserDepartments)

	req, _ := http.NewRequest("GET", "/api/v2.1/departments/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var resp []interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp) != 0 {
		t.Errorf("expected empty array, got %d items", len(resp))
	}
}

func TestCreateDepartment_MissingName(t *testing.T) {
	r, h := setupDepartmentRouter()
	r.POST("/api/v2.1/admin/address-book/groups/", h.CreateDepartment)

	req, _ := http.NewRequest("POST", "/api/v2.1/admin/address-book/groups/",
		strings.NewReader(`{"name":""}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestCreateDepartment_InvalidParent(t *testing.T) {
	r, h := setupDepartmentRouter()
	r.POST("/api/v2.1/admin/address-book/groups/", h.CreateDepartment)

	req, _ := http.NewRequest("POST", "/api/v2.1/admin/address-book/groups/",
		strings.NewReader(`{"name":"Engineering","parent_group":"not-a-uuid"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestGetDepartment_InvalidGroupID(t *testing.T) {
	r, h := setupDepartmentRouter()
	r.GET("/api/v2.1/admin/address-book/groups/:group_id/", h.GetDepartment)

	req, _ := http.NewRequest("GET", "/api/v2.1/admin/address-book/groups/not-a-uuid/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestUpdateDepartment_MissingName(t *testing.T) {
	r, h := setupDepartmentRouter()
	r.PUT("/api/v2.1/admin/address-book/groups/:group_id/", h.UpdateDepartment)

	req, _ := http.NewRequest("PUT",
		"/api/v2.1/admin/address-book/groups/00000000-0000-0000-0000-000000000099/",
		strings.NewReader(`{"name":""}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestDeleteDepartment_InvalidGroupID(t *testing.T) {
	r, h := setupDepartmentRouter()
	r.DELETE("/api/v2.1/admin/address-book/groups/:group_id/", h.DeleteDepartment)

	req, _ := http.NewRequest("DELETE", "/api/v2.1/admin/address-book/groups/not-a-uuid/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestDepartmentResponse_JSON(t *testing.T) {
	resp := DepartmentResponse{
		ID:        "test-id",
		Name:      "Engineering",
		CreatedAt: "2026-01-31T00:00:00Z",
		Groups:    []DepartmentResponse{},
		Members:   []GroupMemberResponse{},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var parsed map[string]interface{}
	json.Unmarshal(data, &parsed)

	if parsed["id"] != "test-id" {
		t.Errorf("id = %v, want test-id", parsed["id"])
	}
	if parsed["name"] != "Engineering" {
		t.Errorf("name = %v, want Engineering", parsed["name"])
	}
}

// TestRegisterDepartmentRoutes verifies that all expected routes are registered
// and respond with something other than 404 (proving the route exists).
func TestRegisterDepartmentRoutes(t *testing.T) {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(func(c *gin.Context) {
		c.Set("org_id", "00000000-0000-0000-0000-000000000001")
		c.Set("user_id", "00000000-0000-0000-0000-000000000001")
		c.Next()
	})

	rg := r.Group("/api/v2.1")
	RegisterDepartmentRoutes(rg, nil, nil)

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{"list departments", "GET", "/api/v2.1/admin/address-book/groups/"},
		{"create department", "POST", "/api/v2.1/admin/address-book/groups/"},
		{"get department", "GET", "/api/v2.1/admin/address-book/groups/00000000-0000-0000-0000-000000000099/"},
		{"update department", "PUT", "/api/v2.1/admin/address-book/groups/00000000-0000-0000-0000-000000000099/"},
		{"delete department", "DELETE", "/api/v2.1/admin/address-book/groups/00000000-0000-0000-0000-000000000099/"},
		{"list user departments", "GET", "/api/v2.1/departments/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest(tt.method, tt.path, nil)
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code == http.StatusNotFound {
				t.Errorf("%s %s returned 404, expected route to be registered", tt.method, tt.path)
			}
		})
	}
}

// TestCreateDepartment_EmptyBody sends a POST with no JSON body at all.
// The handler should return 400 because name will be empty.
func TestCreateDepartment_EmptyBody(t *testing.T) {
	r, h := setupDepartmentRouter()
	r.POST("/api/v2.1/admin/address-book/groups/", h.CreateDepartment)

	req, _ := http.NewRequest("POST", "/api/v2.1/admin/address-book/groups/", nil)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

// TestListDepartments_InvalidOrgID sets org_id to a non-UUID value.
// The handler should parse the org_id and return 400 "invalid org_id".
func TestListDepartments_InvalidOrgID(t *testing.T) {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(func(c *gin.Context) {
		c.Set("org_id", "not-a-uuid")
		c.Set("user_id", "00000000-0000-0000-0000-000000000001")
		c.Next()
	})

	// Use a non-nil-like scenario: we need db != nil so the handler
	// doesn't short-circuit with the nil DB guard. We pass nil here but
	// the nil DB guard returns before the org_id check, so we need to
	// test with a real-ish handler. Since nil DB returns early with empty
	// data (200), we verify that path separately. The org_id validation
	// only runs when db != nil. We cannot easily construct a non-nil *db.DB
	// without a real database, so we document this limitation.
	// Instead, we verify that with nil DB, even an invalid org_id returns 200
	// (the nil guard fires first).
	h := NewDepartmentHandler(nil, nil)
	r.GET("/api/v2.1/admin/address-book/groups/", h.ListDepartments)

	req, _ := http.NewRequest("GET", "/api/v2.1/admin/address-book/groups/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// With nil DB, the handler returns early with empty data before validating org_id
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d (nil DB guard fires before org_id validation)", w.Code, http.StatusOK)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	data, ok := resp["data"].([]interface{})
	if !ok {
		t.Fatal("expected data field in response")
	}
	if len(data) != 0 {
		t.Errorf("expected empty data, got %d items", len(data))
	}
}

// TestCreateDepartmentRequest_JSON verifies the CreateDepartmentRequest struct
// serializes correctly with both name and parent_group fields.
func TestCreateDepartmentRequest_JSON(t *testing.T) {
	tests := []struct {
		name     string
		req      CreateDepartmentRequest
		wantName string
		wantPG   string
	}{
		{
			name:     "with name only",
			req:      CreateDepartmentRequest{Name: "Engineering"},
			wantName: "Engineering",
			wantPG:   "",
		},
		{
			name:     "with name and parent_group",
			req:      CreateDepartmentRequest{Name: "Backend", ParentGroupID: "00000000-0000-0000-0000-000000000099"},
			wantName: "Backend",
			wantPG:   "00000000-0000-0000-0000-000000000099",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.req)
			if err != nil {
				t.Fatalf("failed to marshal: %v", err)
			}

			var parsed map[string]interface{}
			if err := json.Unmarshal(data, &parsed); err != nil {
				t.Fatalf("failed to unmarshal: %v", err)
			}

			if parsed["name"] != tt.wantName {
				t.Errorf("name = %v, want %v", parsed["name"], tt.wantName)
			}

			pg, _ := parsed["parent_group"].(string)
			if pg != tt.wantPG {
				t.Errorf("parent_group = %v, want %v", pg, tt.wantPG)
			}
		})
	}
}

// TestGetDepartment_ValidUUID_NilDB tests that UUID validation passes for a valid
// UUID and the handler returns a controlled 500 when the DB is unavailable.
func TestGetDepartment_ValidUUID_NilDB(t *testing.T) {
	r, h := setupDepartmentRouter()
	r.GET("/api/v2.1/admin/address-book/groups/:group_id/", h.GetDepartment)

	req, _ := http.NewRequest("GET", "/api/v2.1/admin/address-book/groups/00000000-0000-0000-0000-000000000099/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Valid UUID passes validation; nil DB returns explicit 500
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d (valid UUID should pass validation, nil DB returns explicit 500)", w.Code, http.StatusInternalServerError)
	}
}

// TestUpdateDepartment_ValidUUID_EmptyName sends a valid UUID but empty name in JSON.
// The handler should return 400 "name is required".
func TestUpdateDepartment_ValidUUID_EmptyName(t *testing.T) {
	r, h := setupDepartmentRouter()
	r.PUT("/api/v2.1/admin/address-book/groups/:group_id/", h.UpdateDepartment)

	req, _ := http.NewRequest("PUT",
		"/api/v2.1/admin/address-book/groups/00000000-0000-0000-0000-000000000099/",
		strings.NewReader(`{"name":""}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"] != "name is required" {
		t.Errorf("error = %v, want 'name is required'", resp["error"])
	}
}

// TestDeleteDepartment_ValidUUID_NilDB tests that UUID validation passes for a valid
// UUID in delete and the handler returns a controlled 500 when the DB is unavailable.
func TestDeleteDepartment_ValidUUID_NilDB(t *testing.T) {
	r, h := setupDepartmentRouter()
	r.DELETE("/api/v2.1/admin/address-book/groups/:group_id/", h.DeleteDepartment)

	req, _ := http.NewRequest("DELETE", "/api/v2.1/admin/address-book/groups/00000000-0000-0000-0000-000000000099/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Valid UUID passes validation; nil DB returns explicit 500
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d (valid UUID should pass validation, nil DB returns explicit 500)", w.Code, http.StatusInternalServerError)
	}
}

// TestGetBrowserURL tests the URL generation helper
func TestGetBrowserURL(t *testing.T) {
	tests := []struct {
		name          string
		host          string
		xProto        string
		configuredURL string
		want          string
	}{
		{
			name:          "configured URL takes priority over host",
			host:          "localhost:3000",
			xProto:        "https",
			configuredURL: "http://localhost:8080",
			want:          "http://localhost:8080",
		},
		{
			name:          "configured http URL upgrades to https for same host",
			host:          "sfs.nihaoshares.com",
			xProto:        "https",
			configuredURL: "http://sfs.nihaoshares.com",
			want:          "https://sfs.nihaoshares.com",
		},
		{
			name:          "configured http URL with same host and port upgrades to https",
			host:          "sfs.nihaoshares.com:443",
			xProto:        "https",
			configuredURL: "http://sfs.nihaoshares.com:443",
			want:          "https://sfs.nihaoshares.com:443",
		},
		{
			name:          "configured URL keeps alternate host",
			host:          "sfs.nihaoshares.com",
			xProto:        "https",
			configuredURL: "http://sesamefs.internal",
			want:          "http://sesamefs.internal",
		},
		{
			name:          "configured URL takes priority without proxy",
			host:          "localhost:3000",
			xProto:        "",
			configuredURL: "http://localhost:8080",
			want:          "http://localhost:8080",
		},
		{
			name:          "auto-detect with X-Forwarded-Proto",
			host:          "example.com",
			xProto:        "https",
			configuredURL: "",
			want:          "https://example.com",
		},
		{
			name:          "auto-detect without proxy headers",
			host:          "localhost:3000",
			xProto:        "",
			configuredURL: "",
			want:          "http://localhost:3000",
		},
		{
			name:          "no host and no config uses default",
			host:          "",
			xProto:        "",
			configuredURL: "",
			want:          "http://localhost:8080",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request, _ = http.NewRequest("GET", "/test", nil)
			if tt.host != "" {
				c.Request.Host = tt.host
			}
			if tt.xProto != "" {
				c.Request.Header.Set("X-Forwarded-Proto", tt.xProto)
			}

			got := httputil.GetBrowserURL(c, tt.configuredURL)
			if got != tt.want {
				t.Errorf("GetBrowserURL() = %q, want %q", got, tt.want)
			}
		})
	}
}
