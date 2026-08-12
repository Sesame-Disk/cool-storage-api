package v2

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/auth"
	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// setupAuthTestRouter creates a test router with auth routes
func setupAuthTestRouter() (*gin.Engine, *AuthHandler) {
	router := gin.New()
	router.Use(gin.Recovery())

	cfg := &config.Config{
		Auth: config.AuthConfig{
			OIDC: config.OIDCConfig{
				Enabled:      true,
				Issuer:       "https://test-issuer.example.com",
				ClientID:     "test-client-id",
				ClientSecret: "test-client-secret",
				RedirectURIs: []string{"http://localhost:3000/sso", "http://localhost:8080/sso"},
				Scopes:       []string{"openid", "profile", "email"},
				SessionTTL:   24 * time.Hour,
			},
		},
	}

	// Create session manager without database for testing
	sessions := auth.NewSessionManager(&cfg.Auth.OIDC, nil)

	// Create OIDC client
	oidc := auth.NewOIDCClient(cfg, nil, sessions)

	handler := &AuthHandler{
		config:   cfg,
		oidc:     oidc,
		sessions: sessions,
	}

	// Register routes
	api := router.Group("/api/v2.1")
	oidcRoutes := api.Group("/auth/oidc")
	{
		oidcRoutes.GET("/config", handler.GetOIDCConfig)
		oidcRoutes.GET("/config/", handler.GetOIDCConfig)
		oidcRoutes.GET("/login", handler.GetOIDCLoginURL)
		oidcRoutes.GET("/login/", handler.GetOIDCLoginURL)
		oidcRoutes.POST("/callback", handler.HandleOIDCCallback)
		oidcRoutes.POST("/callback/", handler.HandleOIDCCallback)
		oidcRoutes.GET("/logout", handler.GetOIDCLogoutURL)
		oidcRoutes.GET("/logout/", handler.GetOIDCLogoutURL)
	}

	sessionRoutes := api.Group("/auth/session")
	{
		sessionRoutes.GET("", handler.GetSessionInfo)
		sessionRoutes.GET("/", handler.GetSessionInfo)
		sessionRoutes.DELETE("", handler.Logout)
		sessionRoutes.DELETE("/", handler.Logout)
	}

	return router, handler
}

// setupDisabledOIDCRouter creates a router with OIDC disabled
func setupDisabledOIDCRouter() *gin.Engine {
	router := gin.New()
	router.Use(gin.Recovery())

	cfg := &config.Config{
		Auth: config.AuthConfig{
			OIDC: config.OIDCConfig{
				Enabled: false,
			},
		},
	}

	sessions := &auth.SessionManager{}
	oidc := auth.NewOIDCClient(cfg, nil, sessions)

	handler := &AuthHandler{
		config:   cfg,
		oidc:     oidc,
		sessions: sessions,
	}

	api := router.Group("/api/v2.1")
	oidcRoutes := api.Group("/auth/oidc")
	{
		oidcRoutes.GET("/config", handler.GetOIDCConfig)
		oidcRoutes.GET("/login", handler.GetOIDCLoginURL)
		oidcRoutes.POST("/callback", handler.HandleOIDCCallback)
		oidcRoutes.GET("/logout", handler.GetOIDCLogoutURL)
	}

	return router
}

// TestGetOIDCConfig tests the OIDC config endpoint
func TestGetOIDCConfig(t *testing.T) {
	t.Run("returns config when OIDC enabled", func(t *testing.T) {
		router, _ := setupAuthTestRouter()

		req := httptest.NewRequest("GET", "/api/v2.1/auth/oidc/config/", nil)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Status code = %d, want %d", w.Code, http.StatusOK)
		}

		var response map[string]interface{}
		json.Unmarshal(w.Body.Bytes(), &response)

		if response["enabled"] != true {
			t.Error("enabled should be true")
		}
		if len(response) != 1 {
			t.Fatalf("response = %v, want only enabled flag", response)
		}
		for _, field := range []string{"issuer", "client_id", "scopes", "redirect_uris", "client_secret"} {
			if _, exists := response[field]; exists {
				t.Errorf("%s should NOT be returned", field)
			}
		}
	})

	t.Run("returns disabled when OIDC not enabled", func(t *testing.T) {
		router := setupDisabledOIDCRouter()

		req := httptest.NewRequest("GET", "/api/v2.1/auth/oidc/config", nil)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Status code = %d, want %d", w.Code, http.StatusOK)
		}

		var response map[string]interface{}
		json.Unmarshal(w.Body.Bytes(), &response)

		if response["enabled"] != false {
			t.Error("enabled should be false")
		}
	})
}

// TestGetOIDCLoginURL tests the login URL endpoint
func TestGetOIDCLoginURL(t *testing.T) {
	t.Run("returns error when OIDC disabled", func(t *testing.T) {
		router := setupDisabledOIDCRouter()

		req := httptest.NewRequest("GET", "/api/v2.1/auth/oidc/login", nil)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("Status code = %d, want %d", w.Code, http.StatusServiceUnavailable)
		}
	})

	t.Run("uses configured redirect when query is omitted", func(t *testing.T) {
		_, handler := setupAuthTestRouter()
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Request = httptest.NewRequest("GET", "/api/v2.1/auth/oidc/login", nil)
		ctx.Request.Host = "storage.example.com"

		got := handler.resolveOIDCRedirectURI(ctx)
		want := "http://localhost:3000/sso"
		if got != want {
			t.Fatalf("resolveOIDCRedirectURI() = %q, want %q", got, want)
		}
	})

	t.Run("uses explicit redirect query when provided", func(t *testing.T) {
		_, handler := setupAuthTestRouter()
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Request = httptest.NewRequest("GET", "/api/v2.1/auth/oidc/login?redirect_uri=http://localhost:8080/sso", nil)

		got := handler.resolveOIDCRedirectURI(ctx)
		want := "http://localhost:8080/sso"
		if got != want {
			t.Fatalf("resolveOIDCRedirectURI() = %q, want %q", got, want)
		}
	})

	// Note: Full login URL generation requires a working OIDC discovery endpoint
	// which we can't easily mock in this unit test without more infrastructure.
	// Integration tests would cover this better.
}

// TestHandleOIDCCallback tests the callback endpoint
func TestHandleOIDCCallback(t *testing.T) {
	t.Run("returns error when OIDC disabled", func(t *testing.T) {
		router := setupDisabledOIDCRouter()

		body := `{"code": "test-code", "state": "test-state", "redirect_uri": "http://localhost:3000/sso"}`
		req := httptest.NewRequest("POST", "/api/v2.1/auth/oidc/callback", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		if w.Code != http.StatusServiceUnavailable {
			t.Errorf("Status code = %d, want %d", w.Code, http.StatusServiceUnavailable)
		}
	})

	t.Run("returns error for invalid request body", func(t *testing.T) {
		router, _ := setupAuthTestRouter()

		body := `{"invalid": "json"}`
		req := httptest.NewRequest("POST", "/api/v2.1/auth/oidc/callback", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("Status code = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})

	t.Run("returns error for missing required fields", func(t *testing.T) {
		router, _ := setupAuthTestRouter()

		// Missing state field
		body := `{"code": "test-code", "redirect_uri": "http://localhost:3000/sso"}`
		req := httptest.NewRequest("POST", "/api/v2.1/auth/oidc/callback", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("Status code = %d, want %d", w.Code, http.StatusBadRequest)
		}
	})
}

// TestGetOIDCLogoutURL tests the logout URL endpoint
func TestGetOIDCLogoutURL(t *testing.T) {
	t.Run("returns empty when OIDC disabled", func(t *testing.T) {
		router := setupDisabledOIDCRouter()

		req := httptest.NewRequest("GET", "/api/v2.1/auth/oidc/logout", nil)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Status code = %d, want %d", w.Code, http.StatusOK)
		}

		var response map[string]interface{}
		json.Unmarshal(w.Body.Bytes(), &response)

		if response["enabled"] != false {
			t.Error("enabled should be false")
		}
		if response["logout_url"] != "" {
			t.Error("logout_url should be empty when disabled")
		}
	})

	// Note: Full logout URL generation requires a working OIDC discovery endpoint
}

// TestGetSessionInfo tests the session info endpoint
func TestGetSessionInfo(t *testing.T) {
	t.Run("returns error without token", func(t *testing.T) {
		router, _ := setupAuthTestRouter()

		req := httptest.NewRequest("GET", "/api/v2.1/auth/session/", nil)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("Status code = %d, want %d", w.Code, http.StatusUnauthorized)
		}
	})

	t.Run("returns error with invalid token", func(t *testing.T) {
		router, _ := setupAuthTestRouter()

		req := httptest.NewRequest("GET", "/api/v2.1/auth/session/", nil)
		req.Header.Set("Authorization", "Token invalid-token-12345")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("Status code = %d, want %d", w.Code, http.StatusUnauthorized)
		}
	})

	t.Run("accepts Bearer token format", func(t *testing.T) {
		router, _ := setupAuthTestRouter()

		req := httptest.NewRequest("GET", "/api/v2.1/auth/session/", nil)
		req.Header.Set("Authorization", "Bearer some-token")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		// Should fail validation but still parse the token format
		if w.Code != http.StatusUnauthorized {
			t.Errorf("Status code = %d, want %d", w.Code, http.StatusUnauthorized)
		}
	})
}

// TestLogout tests the logout endpoint
func TestLogout(t *testing.T) {
	t.Run("rejects missing token", func(t *testing.T) {
		router, _ := setupAuthTestRouter()

		req := httptest.NewRequest("DELETE", "/api/v2.1/auth/session/", nil)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("Status code = %d, want %d", w.Code, http.StatusUnauthorized)
		}
	})

	t.Run("returns success with valid token and invalidates session", func(t *testing.T) {
		router, handler := setupAuthTestRouter()
		session, err := handler.sessions.CreateSession("user-123", "org-123", "test@example.com", "user")
		if err != nil {
			t.Fatalf("CreateSession() error = %v", err)
		}

		req := httptest.NewRequest("DELETE", "/api/v2.1/auth/session/", nil)
		req.Header.Set("Authorization", "Token "+session.Token)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Status code = %d, want %d", w.Code, http.StatusOK)
		}

		if _, err := handler.sessions.ValidateSession(session.Token); !errors.Is(err, auth.ErrSessionNotFound) {
			t.Fatalf("ValidateSession() error = %v, want %v", err, auth.ErrSessionNotFound)
		}

		// ISSUE-SESSION-COOKIE-NOT-HTTPONLY-01: the clear on logout must not
		// reopen the JS-readable window the fix closes on login.
		setCookie := w.Header().Get("Set-Cookie")
		if !strings.Contains(setCookie, "HttpOnly") {
			t.Fatalf("Set-Cookie = %q, want it to contain HttpOnly", setCookie)
		}
	})

	t.Run("rejects invalid token", func(t *testing.T) {
		router, _ := setupAuthTestRouter()

		req := httptest.NewRequest("DELETE", "/api/v2.1/auth/session/", nil)
		req.Header.Set("Authorization", "Token invalid-session-token")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("Status code = %d, want %d", w.Code, http.StatusUnauthorized)
		}
	})
}

// TestAuthHandler_GetOIDCClient tests the GetOIDCClient method
func TestAuthHandler_GetOIDCClient(t *testing.T) {
	_, handler := setupAuthTestRouter()

	client := handler.GetOIDCClient()
	if client == nil {
		t.Error("GetOIDCClient() should return non-nil client")
	}
}

// TestAuthHandler_GetSessionManager tests the GetSessionManager method
func TestAuthHandler_GetSessionManager(t *testing.T) {
	_, handler := setupAuthTestRouter()

	sm := handler.GetSessionManager()
	if sm == nil {
		t.Error("GetSessionManager() should return non-nil session manager")
	}
}

// TestOIDCEndpointTrailingSlash tests that endpoints work with and without trailing slash
func TestOIDCEndpointTrailingSlash(t *testing.T) {
	router, _ := setupAuthTestRouter()

	endpoints := []string{
		"/api/v2.1/auth/oidc/config",
		"/api/v2.1/auth/oidc/config/",
	}

	for _, endpoint := range endpoints {
		t.Run(endpoint, func(t *testing.T) {
			req := httptest.NewRequest("GET", endpoint, nil)
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("%s: Status code = %d, want %d", endpoint, w.Code, http.StatusOK)
			}
		})
	}
}

// TestNewAuthHandler tests handler creation
func TestNewAuthHandler(t *testing.T) {
	cfg := &config.Config{
		Auth: config.AuthConfig{
			OIDC: config.OIDCConfig{
				Enabled:    true,
				Issuer:     "https://example.com",
				ClientID:   "test",
				SessionTTL: 24 * time.Hour,
			},
		},
	}

	handler := NewAuthHandler(nil, cfg)

	if handler == nil {
		t.Fatal("NewAuthHandler() returned nil")
	}
	if handler.config != cfg {
		t.Error("Config not set correctly")
	}
	if handler.oidc == nil {
		t.Error("OIDC client should be created")
	}
	if handler.sessions == nil {
		t.Error("Session manager should be created")
	}
}

// TestAuthHandlerSetAuthCookie pins ISSUE-SESSION-COOKIE-NOT-HTTPONLY-01 at the
// single writer HandleOIDCCallback (login) and Logout (clear) share, rather than
// at a full HTTP round trip through HandleOIDCCallback — its success path requires
// a real OIDC code exchange this test suite does not mock, so testing the helper
// directly is what actually reaches the login-side write.
func TestAuthHandlerSetAuthCookie(t *testing.T) {
	t.Run("always sets HttpOnly", func(t *testing.T) {
		h := &AuthHandler{}
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

		h.setAuthCookie(c, "user@example.com@sometoken", 3600)

		setCookie := w.Header().Get("Set-Cookie")
		if !strings.Contains(setCookie, "HttpOnly") {
			t.Fatalf("Set-Cookie = %q, want it to contain HttpOnly", setCookie)
		}
		// gin URL-encodes the cookie value, so "@" becomes "%40" — the actual
		// production value has the same shape (email@token).
		if !strings.Contains(setCookie, "sesamefs_auth=user%40example.com%40sometoken") {
			t.Fatalf("Set-Cookie = %q, want it to carry the given value", setCookie)
		}
	})

	t.Run("derives Secure from the request's TLS state", func(t *testing.T) {
		h := &AuthHandler{}
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		c.Request.TLS = &tls.ConnectionState{}

		h.setAuthCookie(c, "user@example.com@sometoken", 3600)

		setCookie := w.Header().Get("Set-Cookie")
		if !strings.Contains(setCookie, "Secure") {
			t.Fatalf("Set-Cookie = %q, want Secure over a TLS request", setCookie)
		}
		if !strings.Contains(setCookie, "HttpOnly") {
			t.Fatalf("Set-Cookie = %q, want it to still contain HttpOnly", setCookie)
		}
	})

	t.Run("clearing uses maxAge=-1 and still sets HttpOnly", func(t *testing.T) {
		h := &AuthHandler{}
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

		h.setAuthCookie(c, "", -1)

		setCookie := w.Header().Get("Set-Cookie")
		if !strings.Contains(setCookie, "HttpOnly") {
			t.Fatalf("Set-Cookie = %q, want it to contain HttpOnly on clear too", setCookie)
		}
		if !strings.Contains(setCookie, "Max-Age=0") {
			t.Fatalf("Set-Cookie = %q, want an expiring Max-Age=0 (net/http's encoding of maxAge=-1)", setCookie)
		}
	})
}
