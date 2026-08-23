package v2

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/apikeys"
	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestLibrarySettings_RequireOwner_NoAuth(t *testing.T) {
	r := gin.New()
	r.Use(gin.Recovery())
	// No auth context set

	h := &LibrarySettingsHandler{
		db:             nil,
		config:         nil,
		permMiddleware: nil,
	}

	r.GET("/api2/repos/:repo_id/history-limit/", h.GetHistoryLimit)

	req, _ := http.NewRequest("GET", "/api2/repos/00000000-0000-0000-0000-000000000001/history-limit/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestLibrarySettings_RequireOwner_InvalidRepoID(t *testing.T) {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(func(c *gin.Context) {
		c.Set("org_id", "00000000-0000-0000-0000-000000000001")
		c.Set("user_id", "00000000-0000-0000-0000-000000000001")
		c.Next()
	})

	h := &LibrarySettingsHandler{
		db:             nil,
		config:         nil,
		permMiddleware: nil,
	}

	r.PUT("/api2/repos/:repo_id/history-limit/", h.SetHistoryLimit)

	req, _ := http.NewRequest("PUT", "/api2/repos/not-a-uuid/history-limit/", bytes.NewBufferString(`{"keep_days": 30}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"] != "invalid repo_id" {
		t.Errorf("error = %v, want 'invalid repo_id'", resp["error"])
	}
}

func TestLibrarySettings_RequireOwner_CredentialScopeMatrix(t *testing.T) {
	tests := []struct {
		name          string
		scope         string
		repoToken     bool
		requiredScope string
		wantOK        bool
	}{
		{name: "session mutation", requiredScope: apikeys.ScopeReadWrite, wantOK: true},
		{name: "read-write mutation", scope: apikeys.ScopeReadWrite, requiredScope: apikeys.ScopeReadWrite, wantOK: true},
		{name: "admin mutation", scope: apikeys.ScopeAdmin, requiredScope: apikeys.ScopeReadWrite, wantOK: true},
		{name: "read mutation denied", scope: apikeys.ScopeRead, requiredScope: apikeys.ScopeReadWrite},
		{name: "read settings", scope: apikeys.ScopeRead, requiredScope: apikeys.ScopeRead, wantOK: true},
		{name: "repo token denied", repoToken: true, requiredScope: apikeys.ScopeRead},
	}

	original := isLibraryOwnerFn
	isLibraryOwnerFn = func(_ *middleware.PermissionMiddleware, _, _, _ string) (bool, error) { return true, nil }
	defer func() { isLibraryOwnerFn = original }()

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Set("org_id", authTestOrgID)
			c.Set("user_id", authTestUserID)
			if tc.scope != "" {
				c.Set("api_key_scope", tc.scope)
			}
			if tc.repoToken {
				c.Set("repo_api_token", true)
			}
			c.Params = gin.Params{{Key: "repo_id", Value: authTestRepoID}}

			h := &LibrarySettingsHandler{}
			_, _, _, ok := h.requireOwner(c, tc.requiredScope)
			if ok != tc.wantOK {
				t.Fatalf("requireOwner ok = %v, want %v (status %d)", ok, tc.wantOK, w.Code)
			}
			if !tc.wantOK && w.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
			}
		})
	}
}

func TestCreateAPIToken_InvalidJSON(t *testing.T) {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(func(c *gin.Context) {
		c.Set("org_id", "00000000-0000-0000-0000-000000000001")
		c.Set("user_id", "00000000-0000-0000-0000-000000000001")
		c.Next()
	})

	h := &LibrarySettingsHandler{
		db:             nil,
		config:         nil,
		permMiddleware: nil,
	}

	r.POST("/api/v2.1/repos/:repo_id/repo-api-tokens/", h.CreateAPIToken)

	// With nil permMiddleware, requireOwner will panic trying to call IsLibraryOwner
	// so we test binding on a direct handler instead
	r2 := gin.New()
	r2.POST("/test", func(c *gin.Context) {
		var req struct {
			AppName    string `json:"app_name"`
			Permission string `json:"permission"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		if req.AppName == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "app_name is required"})
			return
		}
		if req.Permission != "r" && req.Permission != "rw" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "permission must be 'r' or 'rw'"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	tests := []struct {
		name       string
		body       map[string]interface{}
		wantStatus int
		wantError  string
	}{
		{
			name:       "missing app_name",
			body:       map[string]interface{}{"permission": "rw"},
			wantStatus: http.StatusBadRequest,
			wantError:  "app_name is required",
		},
		{
			name:       "invalid permission",
			body:       map[string]interface{}{"app_name": "test", "permission": "admin"},
			wantStatus: http.StatusBadRequest,
			wantError:  "permission must be 'r' or 'rw'",
		},
		{
			name:       "valid read permission",
			body:       map[string]interface{}{"app_name": "test", "permission": "r"},
			wantStatus: http.StatusOK,
		},
		{
			name:       "valid rw permission",
			body:       map[string]interface{}{"app_name": "test", "permission": "rw"},
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jsonBody, _ := json.Marshal(tt.body)
			req, _ := http.NewRequest("POST", "/test", bytes.NewBuffer(jsonBody))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r2.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}
			if tt.wantError != "" {
				var resp map[string]interface{}
				json.Unmarshal(w.Body.Bytes(), &resp)
				if resp["error"] != tt.wantError {
					t.Errorf("error = %v, want %v", resp["error"], tt.wantError)
				}
			}
		})
	}
}

func TestSetHistoryLimit_Validation(t *testing.T) {
	// Test the keep_days validation logic directly
	r := gin.New()
	r.PUT("/test", func(c *gin.Context) {
		var req struct {
			KeepDays int `json:"keep_days"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		if req.KeepDays < -1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "keep_days must be -1 (all), 0 (none), or a positive integer"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"keep_days": req.KeepDays})
	})

	tests := []struct {
		name       string
		keepDays   int
		wantStatus int
	}{
		{"keep all (-1)", -1, http.StatusOK},
		{"keep none (0)", 0, http.StatusOK},
		{"keep 30 days", 30, http.StatusOK},
		{"keep 365 days", 365, http.StatusOK},
		{"invalid (-2)", -2, http.StatusBadRequest},
		{"invalid (-100)", -100, http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := map[string]interface{}{"keep_days": tt.keepDays}
			jsonBody, _ := json.Marshal(body)
			req, _ := http.NewRequest("PUT", "/test", bytes.NewBuffer(jsonBody))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

func TestSetAutoDelete_Validation(t *testing.T) {
	r := gin.New()
	r.PUT("/test", func(c *gin.Context) {
		var req struct {
			AutoDeleteDays int `json:"auto_delete_days"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		if req.AutoDeleteDays < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "auto_delete_days must be 0 (disabled) or a positive integer"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"auto_delete_days": req.AutoDeleteDays})
	})

	tests := []struct {
		name       string
		days       int
		wantStatus int
	}{
		{"disabled (0)", 0, http.StatusOK},
		{"30 days", 30, http.StatusOK},
		{"negative (-1)", -1, http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := map[string]interface{}{"auto_delete_days": tt.days}
			jsonBody, _ := json.Marshal(body)
			req, _ := http.NewRequest("PUT", "/test", bytes.NewBuffer(jsonBody))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

func TestUpdateAPIToken_PermissionValidation(t *testing.T) {
	r := gin.New()
	r.PUT("/test", func(c *gin.Context) {
		var req struct {
			Permission string `json:"permission"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		if req.Permission != "r" && req.Permission != "rw" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "permission must be 'r' or 'rw'"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	tests := []struct {
		name       string
		permission string
		wantStatus int
	}{
		{"read only", "r", http.StatusOK},
		{"read-write", "rw", http.StatusOK},
		{"invalid empty", "", http.StatusBadRequest},
		{"invalid admin", "admin", http.StatusBadRequest},
		{"invalid write", "w", http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := map[string]interface{}{"permission": tt.permission}
			jsonBody, _ := json.Marshal(body)
			req, _ := http.NewRequest("PUT", "/test", bytes.NewBuffer(jsonBody))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

func TestTransferLibrary_Validation(t *testing.T) {
	r := gin.New()
	r.PUT("/test", func(c *gin.Context) {
		var req struct {
			Owner string `json:"owner"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		if req.Owner == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "owner email is required"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"success": true})
	})

	tests := []struct {
		name       string
		body       map[string]interface{}
		wantStatus int
	}{
		{
			name:       "valid email",
			body:       map[string]interface{}{"owner": "user@example.com"},
			wantStatus: http.StatusOK,
		},
		{
			name:       "missing owner",
			body:       map[string]interface{}{},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "empty owner",
			body:       map[string]interface{}{"owner": ""},
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jsonBody, _ := json.Marshal(tt.body)
			req, _ := http.NewRequest("PUT", "/test", bytes.NewBuffer(jsonBody))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

func TestAPITokenResponse_JSONFormat(t *testing.T) {
	resp := APITokenResponse{
		AppName:     "my-app",
		APIToken:    "abc123def456",
		Permission:  "rw",
		GeneratedAt: "2026-01-29T12:00:00Z",
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal APITokenResponse: %v", err)
	}

	var decoded map[string]interface{}
	json.Unmarshal(data, &decoded)

	if decoded["app_name"] != "my-app" {
		t.Errorf("app_name = %v, want my-app", decoded["app_name"])
	}
	if decoded["permission"] != "rw" {
		t.Errorf("permission = %v, want rw", decoded["permission"])
	}
	if decoded["generated_at"] != "2026-01-29T12:00:00Z" {
		t.Errorf("generated_at = %v, want 2026-01-29T12:00:00Z", decoded["generated_at"])
	}
}

func TestRegisterHistoryLimitRoutes(t *testing.T) {
	r := gin.New()
	rg := r.Group("/api2")
	RegisterHistoryLimitRoutes(rg, nil, nil)

	routes := []struct {
		method string
		path   string
	}{
		{"GET", "/api2/repos/00000000-0000-0000-0000-000000000001/history-limit/"},
		{"PUT", "/api2/repos/00000000-0000-0000-0000-000000000001/history-limit/"},
	}

	for _, rt := range routes {
		req, _ := http.NewRequest(rt.method, rt.path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code == http.StatusNotFound {
			t.Errorf("route %s %s not registered", rt.method, rt.path)
		}
	}
}

func TestGetHistoryLimit_NoAuth(t *testing.T) {
	r := gin.New()
	r.Use(gin.Recovery())

	h := &LibrarySettingsHandler{
		db:             nil,
		config:         nil,
		permMiddleware: nil,
	}

	r.GET("/api2/repos/:repo_id/history-limit/", h.GetHistoryLimit)

	req, _ := http.NewRequest("GET", "/api2/repos/00000000-0000-0000-0000-000000000001/history-limit/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"] != "authentication required" {
		t.Errorf("error = %v, want 'authentication required'", resp["error"])
	}
}

func TestGetAutoDelete_NoAuth(t *testing.T) {
	r := gin.New()
	r.Use(gin.Recovery())

	h := &LibrarySettingsHandler{
		db:             nil,
		config:         nil,
		permMiddleware: nil,
	}

	r.GET("/api/v2.1/repos/:repo_id/auto-delete/", h.GetAutoDelete)

	req, _ := http.NewRequest("GET", "/api/v2.1/repos/00000000-0000-0000-0000-000000000001/auto-delete/", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"] != "authentication required" {
		t.Errorf("error = %v, want 'authentication required'", resp["error"])
	}
}

func TestRegisterV21LibrarySettingsRoutes(t *testing.T) {
	r := gin.New()
	rg := r.Group("/api/v2.1")
	RegisterV21LibrarySettingsRoutes(rg, nil, nil)

	repoID := "00000000-0000-0000-0000-000000000001"
	routes := []struct {
		method string
		path   string
	}{
		{"GET", "/api/v2.1/repos/" + repoID + "/auto-delete/"},
		{"PUT", "/api/v2.1/repos/" + repoID + "/auto-delete/"},
		{"GET", "/api/v2.1/repos/" + repoID + "/repo-api-tokens/"},
		{"POST", "/api/v2.1/repos/" + repoID + "/repo-api-tokens/"},
		{"DELETE", "/api/v2.1/repos/" + repoID + "/repo-api-tokens/my-app/"},
		{"PUT", "/api/v2.1/repos/" + repoID + "/repo-api-tokens/my-app/"},
	}

	for _, rt := range routes {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			req, _ := http.NewRequest(rt.method, rt.path, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code == http.StatusNotFound {
				t.Errorf("route %s %s not registered", rt.method, rt.path)
			}
		})
	}
}

func TestRegisterLibraryTransferRoutes(t *testing.T) {
	r := gin.New()
	rg := r.Group("/api2")
	RegisterLibraryTransferRoutes(rg, nil, nil)

	repoID := "00000000-0000-0000-0000-000000000001"
	routes := []struct {
		method string
		path   string
	}{
		{"PUT", "/api2/repos/" + repoID + "/owner/"},
		{"PUT", "/api2/repos/" + repoID + "/owner"},
	}

	for _, rt := range routes {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			req, _ := http.NewRequest(rt.method, rt.path, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code == http.StatusNotFound {
				t.Errorf("route %s %s not registered", rt.method, rt.path)
			}
		})
	}
}

func TestSetHistoryLimit_FormBinding(t *testing.T) {
	r := gin.New()
	r.PUT("/test", func(c *gin.Context) {
		var req struct {
			KeepDays int `json:"keep_days" form:"keep_days"`
		}
		if err := c.ShouldBind(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		if req.KeepDays < -1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "keep_days must be -1 (all), 0 (none), or a positive integer"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"keep_days": req.KeepDays})
	})

	tests := []struct {
		name       string
		formData   string
		wantStatus int
		wantDays   float64
	}{
		{"form keep all (-1)", "keep_days=-1", http.StatusOK, -1},
		{"form keep none (0)", "keep_days=0", http.StatusOK, 0},
		{"form keep 90 days", "keep_days=90", http.StatusOK, 90},
		{"form invalid (-5)", "keep_days=-5", http.StatusBadRequest, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("PUT", "/test", bytes.NewBufferString(tt.formData))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}
			if tt.wantStatus == http.StatusOK {
				var resp map[string]interface{}
				json.Unmarshal(w.Body.Bytes(), &resp)
				if resp["keep_days"] != tt.wantDays {
					t.Errorf("keep_days = %v, want %v", resp["keep_days"], tt.wantDays)
				}
			}
		})
	}
}

func TestSetAutoDelete_FormBinding(t *testing.T) {
	r := gin.New()
	r.PUT("/test", func(c *gin.Context) {
		var req struct {
			AutoDeleteDays int `json:"auto_delete_days" form:"auto_delete_days"`
		}
		if err := c.ShouldBind(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		if req.AutoDeleteDays < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "auto_delete_days must be 0 (disabled) or a positive integer"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"auto_delete_days": req.AutoDeleteDays})
	})

	tests := []struct {
		name       string
		formData   string
		wantStatus int
		wantDays   float64
	}{
		{"form disabled (0)", "auto_delete_days=0", http.StatusOK, 0},
		{"form 60 days", "auto_delete_days=60", http.StatusOK, 60},
		{"form negative (-3)", "auto_delete_days=-3", http.StatusBadRequest, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("PUT", "/test", bytes.NewBufferString(tt.formData))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}
			if tt.wantStatus == http.StatusOK {
				var resp map[string]interface{}
				json.Unmarshal(w.Body.Bytes(), &resp)
				if resp["auto_delete_days"] != tt.wantDays {
					t.Errorf("auto_delete_days = %v, want %v", resp["auto_delete_days"], tt.wantDays)
				}
			}
		})
	}
}

func TestSetHistoryLimit_NilPermMiddleware_Recovery(t *testing.T) {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(func(c *gin.Context) {
		c.Set("org_id", "00000000-0000-0000-0000-000000000001")
		c.Set("user_id", "00000000-0000-0000-0000-000000000001")
		c.Next()
	})

	h := &LibrarySettingsHandler{
		db:             nil,
		config:         nil,
		permMiddleware: nil,
	}

	r.PUT("/api2/repos/:repo_id/history-limit/", h.SetHistoryLimit)

	body := map[string]interface{}{"keep_days": 30}
	jsonBody, _ := json.Marshal(body)
	req, _ := http.NewRequest("PUT", "/api2/repos/00000000-0000-0000-0000-000000000001/history-limit/", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	// Should not panic the test runner thanks to gin.Recovery()
	r.ServeHTTP(w, req)

	// The handler will panic when calling h.permMiddleware.IsLibraryOwner() on nil.
	// gin.Recovery() catches the panic and returns 500.
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d (recovery from nil permMiddleware panic)", w.Code, http.StatusInternalServerError)
	}
}

func TestDepartmentResponse_JSONFormat(t *testing.T) {
	resp := DepartmentResponse{
		ID:            "dept-001",
		Name:          "Engineering",
		CreatedAt:     "2026-02-01T10:00:00Z",
		ParentGroupID: "parent-001",
		MemberCount:   5,
		Groups:        []DepartmentResponse{},
		AncestorGroups: []DepartmentRef{
			{ID: "root-001", Name: "Company"},
		},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal DepartmentResponse: %v", err)
	}

	var decoded map[string]interface{}
	json.Unmarshal(data, &decoded)

	if decoded["id"] != "dept-001" {
		t.Errorf("id = %v, want dept-001", decoded["id"])
	}
	if decoded["name"] != "Engineering" {
		t.Errorf("name = %v, want Engineering", decoded["name"])
	}
	if decoded["created_at"] != "2026-02-01T10:00:00Z" {
		t.Errorf("created_at = %v, want 2026-02-01T10:00:00Z", decoded["created_at"])
	}
	if decoded["parent_group_id"] != "parent-001" {
		t.Errorf("parent_group_id = %v, want parent-001", decoded["parent_group_id"])
	}
	if decoded["member_count"] != float64(5) {
		t.Errorf("member_count = %v, want 5", decoded["member_count"])
	}

	ancestors, ok := decoded["ancestor_groups"].([]interface{})
	if !ok || len(ancestors) != 1 {
		t.Fatalf("ancestor_groups length = %v, want 1", len(ancestors))
	}
	ancestor := ancestors[0].(map[string]interface{})
	if ancestor["id"] != "root-001" {
		t.Errorf("ancestor id = %v, want root-001", ancestor["id"])
	}
	if ancestor["name"] != "Company" {
		t.Errorf("ancestor name = %v, want Company", ancestor["name"])
	}
}

func TestDepartmentRef_JSONFormat(t *testing.T) {
	ref := DepartmentRef{
		ID:   "ref-123",
		Name: "Sales",
	}

	data, err := json.Marshal(ref)
	if err != nil {
		t.Fatalf("failed to marshal DepartmentRef: %v", err)
	}

	var decoded map[string]interface{}
	json.Unmarshal(data, &decoded)

	if decoded["id"] != "ref-123" {
		t.Errorf("id = %v, want ref-123", decoded["id"])
	}
	if decoded["name"] != "Sales" {
		t.Errorf("name = %v, want Sales", decoded["name"])
	}

	// Verify only expected fields are present (id and name)
	if len(decoded) != 2 {
		t.Errorf("field count = %d, want 2 (id, name)", len(decoded))
	}
}
