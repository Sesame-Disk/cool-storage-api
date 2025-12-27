package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// createTestServer creates a minimal test server without database
func createTestServer() *Server {
	cfg := config.DefaultConfig()
	cfg.Auth.DevMode = true
	cfg.Auth.DevTokens = []config.DevTokenEntry{
		{Token: "test-token-123", UserID: "user-1", OrgID: "org-1"},
		{Token: "admin-token", UserID: "admin", OrgID: "org-1"},
	}

	return &Server{
		config:     cfg,
		db:         nil,
		storage:    nil,
		blockStore: nil,
		tokenStore: nil,
		router:     gin.New(),
	}
}

// TestHandlePing tests the ping endpoint
func TestHandlePing(t *testing.T) {
	s := createTestServer()
	s.router.GET("/ping", s.handlePing)

	req, _ := http.NewRequest("GET", "/ping", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if w.Body.String() != "pong" {
		t.Errorf("body = %q, want %q", w.Body.String(), "pong")
	}
}

// TestHandleHealth tests the health endpoint
func TestHandleHealth(t *testing.T) {
	s := createTestServer()
	s.router.GET("/health", s.handleHealth)

	req, _ := http.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if response["status"] != "healthy" {
		t.Errorf("status = %v, want healthy", response["status"])
	}
}

// TestHandleServerInfo tests the server info endpoint
func TestHandleServerInfo(t *testing.T) {
	s := createTestServer()
	s.router.GET("/api2/server-info/", s.handleServerInfo)

	req, _ := http.NewRequest("GET", "/api2/server-info/", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	// Check expected fields
	expectedFields := []string{"version", "encrypted_library_version", "enable_encrypted_library"}
	for _, field := range expectedFields {
		if _, ok := response[field]; !ok {
			t.Errorf("missing field: %s", field)
		}
	}

	// Check version is set
	if response["version"] == "" {
		t.Error("version should not be empty")
	}
}

// TestHandleAuthToken tests the auth-token endpoint
func TestHandleAuthToken(t *testing.T) {
	s := createTestServer()
	s.router.POST("/api2/auth-token/", s.handleAuthToken)

	tests := []struct {
		name       string
		username   string
		password   string
		wantStatus int
		wantToken  bool
	}{
		{
			name:       "valid dev token by user id",
			username:   "user-1",
			password:   "any-password",
			wantStatus: http.StatusOK,
			wantToken:  true,
		},
		{
			name:       "valid dev token by token value",
			username:   "any-user",
			password:   "test-token-123",
			wantStatus: http.StatusOK,
			wantToken:  true,
		},
		{
			name:       "invalid credentials",
			username:   "unknown-user",
			password:   "wrong-password",
			wantStatus: http.StatusUnauthorized,
			wantToken:  false,
		},
		{
			name:       "missing username",
			username:   "",
			password:   "password",
			wantStatus: http.StatusBadRequest,
			wantToken:  false,
		},
		{
			name:       "missing password",
			username:   "user",
			password:   "",
			wantStatus: http.StatusBadRequest,
			wantToken:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			form := url.Values{}
			form.Set("username", tt.username)
			form.Set("password", tt.password)

			req, _ := http.NewRequest("POST", "/api2/auth-token/", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

			w := httptest.NewRecorder()
			s.router.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d, body: %s", w.Code, tt.wantStatus, w.Body.String())
			}

			if tt.wantToken {
				var response map[string]interface{}
				if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
					t.Fatalf("failed to parse response: %v", err)
				}
				if _, ok := response["token"]; !ok {
					t.Error("response should contain token field")
				}
			}
		})
	}
}

// TestHandleAccountInfo tests the account info endpoint
func TestHandleAccountInfo(t *testing.T) {
	s := createTestServer()

	// Setup route with auth context
	s.router.GET("/api2/account/info/", func(c *gin.Context) {
		c.Set("user_id", "test-user-123")
		c.Set("org_id", "test-org-456")
		c.Next()
	}, s.handleAccountInfo)

	req, _ := http.NewRequest("GET", "/api2/account/info/", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	// Check expected fields
	expectedFields := []string{"email", "name", "institution", "space_usage", "total_space"}
	for _, field := range expectedFields {
		if _, ok := response[field]; !ok {
			t.Errorf("missing field: %s", field)
		}
	}

	// Check user_id is included in email
	email, _ := response["email"].(string)
	if !strings.Contains(email, "test-user-123") {
		t.Errorf("email should contain user_id, got: %s", email)
	}
}

// TestAuthMiddleware tests the authentication middleware
func TestAuthMiddleware(t *testing.T) {
	s := createTestServer()

	// Setup protected route
	s.router.GET("/protected", s.authMiddleware(), func(c *gin.Context) {
		userID := c.GetString("user_id")
		orgID := c.GetString("org_id")
		c.JSON(http.StatusOK, gin.H{"user_id": userID, "org_id": orgID})
	})

	tests := []struct {
		name       string
		authHeader string
		wantStatus int
	}{
		{
			name:       "valid Token format",
			authHeader: "Token test-token-123",
			wantStatus: http.StatusOK,
		},
		{
			name:       "valid Bearer format",
			authHeader: "Bearer test-token-123",
			wantStatus: http.StatusOK,
		},
		{
			name:       "missing header",
			authHeader: "",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "invalid token",
			authHeader: "Token invalid-token",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "invalid format",
			authHeader: "Basic dXNlcjpwYXNz",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "malformed header",
			authHeader: "TokenWithoutSpace",
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", "/protected", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}

			w := httptest.NewRecorder()
			s.router.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d, body: %s", w.Code, tt.wantStatus, w.Body.String())
			}
		})
	}
}

// TestAuthMiddlewareSetsContext tests that auth middleware sets user context
func TestAuthMiddlewareSetsContext(t *testing.T) {
	s := createTestServer()

	var capturedUserID, capturedOrgID string

	s.router.GET("/check", s.authMiddleware(), func(c *gin.Context) {
		capturedUserID = c.GetString("user_id")
		capturedOrgID = c.GetString("org_id")
		c.Status(http.StatusOK)
	})

	req, _ := http.NewRequest("GET", "/check", nil)
	req.Header.Set("Authorization", "Token test-token-123")

	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	if capturedUserID != "user-1" {
		t.Errorf("user_id = %s, want user-1", capturedUserID)
	}
	if capturedOrgID != "org-1" {
		t.Errorf("org_id = %s, want org-1", capturedOrgID)
	}
}

// TestHandleNotImplemented tests the not implemented handler
func TestHandleNotImplemented(t *testing.T) {
	s := createTestServer()
	s.router.GET("/not-implemented", s.handleNotImplemented)

	req, _ := http.NewRequest("GET", "/not-implemented", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusNotImplemented {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotImplemented)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if response["error"] != "not implemented yet" {
		t.Errorf("error = %v, want 'not implemented yet'", response["error"])
	}
}

// TestServerInfoCompatibility tests that server info matches Seafile client expectations
func TestServerInfoCompatibility(t *testing.T) {
	s := createTestServer()
	s.router.GET("/api2/server-info/", s.handleServerInfo)

	req, _ := http.NewRequest("GET", "/api2/server-info/", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)

	// Seafile client expects these specific fields
	if version, ok := response["version"].(string); !ok || version == "" {
		t.Error("version must be a non-empty string")
	}

	if encVersion, ok := response["encrypted_library_version"].(float64); !ok || encVersion < 1 {
		t.Error("encrypted_library_version must be >= 1")
	}

	if _, ok := response["enable_encrypted_library"].(bool); !ok {
		t.Error("enable_encrypted_library must be a boolean")
	}
}

// TestAuthTokenResponseFormat tests auth-token response matches Seafile format
func TestAuthTokenResponseFormat(t *testing.T) {
	s := createTestServer()
	s.router.POST("/api2/auth-token/", s.handleAuthToken)

	form := url.Values{}
	form.Set("username", "user-1")
	form.Set("password", "any")

	req, _ := http.NewRequest("POST", "/api2/auth-token/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)

	// Seafile client expects {"token": "..."} format
	token, ok := response["token"].(string)
	if !ok {
		t.Fatal("response must have 'token' string field")
	}
	if token == "" {
		t.Error("token must not be empty")
	}
}

// TestAuthTokenErrorFormat tests auth-token error response matches Seafile format
func TestAuthTokenErrorFormat(t *testing.T) {
	s := createTestServer()
	s.router.POST("/api2/auth-token/", s.handleAuthToken)

	form := url.Values{}
	form.Set("username", "invalid")
	form.Set("password", "invalid")

	req, _ := http.NewRequest("POST", "/api2/auth-token/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)

	// Seafile client expects {"non_field_errors": "..."} for auth failures
	if _, ok := response["non_field_errors"]; !ok {
		t.Error("auth error should have 'non_field_errors' field for Seafile compatibility")
	}
}

// TestAccountInfoTotalSpace tests account info total_space field
func TestAccountInfoTotalSpace(t *testing.T) {
	s := createTestServer()
	s.router.GET("/api2/account/info/", func(c *gin.Context) {
		c.Set("user_id", "user")
		c.Set("org_id", "org")
		c.Next()
	}, s.handleAccountInfo)

	req, _ := http.NewRequest("GET", "/api2/account/info/", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)

	// Seafile uses -2 for unlimited quota
	totalSpace, ok := response["total_space"].(float64)
	if !ok {
		t.Fatal("total_space must be a number")
	}
	if totalSpace != -2 {
		t.Errorf("total_space = %v, want -2 (unlimited)", totalSpace)
	}
}
