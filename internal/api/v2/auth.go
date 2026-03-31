package v2

import (
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/Sesame-Disk/sesamefs/internal/auth"
	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/gin-gonic/gin"
)

// AuthHandler handles authentication-related API endpoints
type AuthHandler struct {
	db       *db.DB
	config   *config.Config
	oidc     *auth.OIDCClient
	sessions *auth.SessionManager
}

// NewAuthHandler creates a new auth handler
func NewAuthHandler(database *db.DB, cfg *config.Config) *AuthHandler {
	// Create session manager
	sessions := auth.NewSessionManager(&cfg.Auth.OIDC, database)

	// Create OIDC client
	oidc := auth.NewOIDCClient(cfg, database, sessions)

	return &AuthHandler{
		db:       database,
		config:   cfg,
		oidc:     oidc,
		sessions: sessions,
	}
}

// GetOIDCClient returns the OIDC client for use in middleware
func (h *AuthHandler) GetOIDCClient() *auth.OIDCClient {
	return h.oidc
}

// GetSessionManager returns the session manager for use in middleware
func (h *AuthHandler) GetSessionManager() *auth.SessionManager {
	return h.sessions
}

// RegisterAuthRoutes registers authentication routes.
// authRL is an optional rate-limiting middleware applied to sensitive endpoints (callback).
func RegisterAuthRoutes(router *gin.RouterGroup, database *db.DB, cfg *config.Config, authRL ...gin.HandlerFunc) *AuthHandler {
	handler := NewAuthHandler(database, cfg)

	// OIDC endpoints
	oidc := router.Group("/auth/oidc")
	{
		// Get OIDC login URL
		oidc.GET("/login", handler.GetOIDCLoginURL)
		oidc.GET("/login/", handler.GetOIDCLoginURL)

		// Handle OIDC callback (code exchange) — rate limited
		callbackHandlers := append(authRL, handler.HandleOIDCCallback)
		oidc.POST("/callback", callbackHandlers...)
		oidc.POST("/callback/", callbackHandlers...)

		// Get OIDC configuration (public)
		oidc.GET("/config", handler.GetOIDCConfig)
		oidc.GET("/config/", handler.GetOIDCConfig)

		// Get OIDC logout URL (for single logout)
		oidc.GET("/logout", handler.GetOIDCLogoutURL)
		oidc.GET("/logout/", handler.GetOIDCLogoutURL)
	}

	// Session endpoints
	session := router.Group("/auth/session")
	{
		// Get current session info
		session.GET("", handler.GetSessionInfo)
		session.GET("/", handler.GetSessionInfo)

		// Logout (invalidate session)
		session.DELETE("", handler.Logout)
		session.DELETE("/", handler.Logout)
	}

	return handler
}

// GetOIDCLoginURL returns the URL to redirect users to for OIDC login
// GET /api/v2.1/auth/oidc/login?redirect_uri=...&return_url=...
func (h *AuthHandler) GetOIDCLoginURL(c *gin.Context) {
	if !h.oidc.IsEnabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": "OIDC authentication is not enabled",
		})
		return
	}

	redirectURI := c.Query("redirect_uri")
	returnURL := c.Query("return_url")

	if redirectURI == "" {
		// Default to the /sso endpoint on the same host
		scheme := "https"
		if c.Request.TLS == nil && strings.HasPrefix(c.Request.Host, "localhost") {
			scheme = "http"
		}
		redirectURI = scheme + "://" + c.Request.Host + "/sso"
	}

	if returnURL == "" {
		returnURL = "/"
	}

	authURL, err := h.oidc.GetAuthorizationURL(c.Request.Context(), redirectURI, returnURL)
	if err != nil {
		log.Printf("Failed to get OIDC authorization URL: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to generate login URL",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"authorization_url": authURL,
		"redirect_uri":      redirectURI,
	})
}

// HandleOIDCCallback handles the OIDC callback after user authenticates
// POST /api/v2.1/auth/oidc/callback
// Body: { "code": "...", "state": "...", "redirect_uri": "..." }
func (h *AuthHandler) HandleOIDCCallback(c *gin.Context) {
	if !h.oidc.IsEnabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": "OIDC authentication is not enabled",
		})
		return
	}

	var req struct {
		Code        string `json:"code" binding:"required"`
		State       string `json:"state" binding:"required"`
		RedirectURI string `json:"redirect_uri" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Invalid request: " + err.Error(),
		})
		return
	}

	// Exchange the authorization code for tokens
	result, err := h.oidc.ExchangeCode(c.Request.Context(), req.Code, req.State, req.RedirectURI)
	if err != nil {
		log.Printf("OIDC code exchange failed: %v", err)
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "Authentication failed: " + err.Error(),
		})
		return
	}

	// Set sesamefs_auth cookie so the browser can identify the user when
	// serving org admin HTML pages (resolveOrgForPanel reads this cookie).
	// httpOnly=false is required: the frontend JS reads this as a fallback.
	// Cookie TTL matches session TTL so both expire at the same time.
	seahubAuth := result.Email + "@" + result.SessionToken
	isSecure := c.Request.TLS != nil
	cookieMaxAge := int(h.config.Auth.OIDC.SessionTTL.Seconds())
	c.SetCookie("sesamefs_auth", seahubAuth, cookieMaxAge, "/", "", isSecure, false)

	// Return the session token
	c.JSON(http.StatusOK, gin.H{
		"token":       result.SessionToken,
		"user_id":     result.UserID,
		"org_id":      result.OrgID,
		"email":       result.Email,
		"name":        result.Name,
		"role":        result.Role,
		"expires_at":  result.ExpiresAt.Unix(),
		"is_new_user": result.IsNewUser,
	})
}

// GetOIDCConfig returns the public OIDC configuration
// GET /api/v2.1/auth/oidc/config
func (h *AuthHandler) GetOIDCConfig(c *gin.Context) {
	if !h.oidc.IsEnabled() {
		c.JSON(http.StatusOK, gin.H{
			"enabled": false,
		})
		return
	}

	// Return public configuration (no secrets)
	c.JSON(http.StatusOK, gin.H{
		"enabled":   true,
		"issuer":    h.config.Auth.OIDC.Issuer,
		"client_id": h.config.Auth.OIDC.ClientID,
		"scopes":    h.config.Auth.OIDC.Scopes,
	})
}

// GetSessionInfo returns information about the current session
// GET /api/v2.1/auth/session
func (h *AuthHandler) GetSessionInfo(c *gin.Context) {
	// Get token from Authorization header
	authHeader := c.GetHeader("Authorization")
	var token string
	if strings.HasPrefix(authHeader, "Token ") {
		token = strings.TrimPrefix(authHeader, "Token ")
	} else if strings.HasPrefix(authHeader, "Bearer ") {
		token = strings.TrimPrefix(authHeader, "Bearer ")
	}

	if token == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "No token provided",
		})
		return
	}

	session, err := h.sessions.ValidateSession(token)
	if err != nil {
		if !errors.Is(err, auth.ErrSessionExpired) &&
			!errors.Is(err, auth.ErrSessionInvalid) &&
			!errors.Is(err, auth.ErrSessionRevoked) &&
			!errors.Is(err, auth.ErrSessionNotFound) {
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"error": "Session validation unavailable",
			})
			return
		}
		c.JSON(http.StatusUnauthorized, gin.H{
			"error": "Invalid or expired session",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"user_id":    session.UserID,
		"org_id":     session.OrgID,
		"email":      session.Email,
		"role":       session.Role,
		"expires_at": session.ExpiresAt.Unix(),
	})
}

// GetOIDCLogoutURL returns the URL to redirect users to for OIDC logout (Single Logout)
// GET /api/v2.1/auth/oidc/logout?post_logout_redirect_uri=...
func (h *AuthHandler) GetOIDCLogoutURL(c *gin.Context) {
	if !h.oidc.IsEnabled() {
		c.JSON(http.StatusOK, gin.H{
			"logout_url": "",
			"enabled":    false,
		})
		return
	}

	postLogoutRedirectURI := c.Query("post_logout_redirect_uri")
	if postLogoutRedirectURI == "" {
		// Default to the login page on the same host
		scheme := "https"
		if c.Request.TLS == nil && strings.HasPrefix(c.Request.Host, "localhost") {
			scheme = "http"
		}
		postLogoutRedirectURI = scheme + "://" + c.Request.Host + "/login/"
	}

	logoutURL, err := h.oidc.GetLogoutURL(c.Request.Context(), "", postLogoutRedirectURI)
	if err != nil {
		log.Printf("Failed to get OIDC logout URL: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "Failed to generate logout URL",
		})
		return
	}

	// If the IdP doesn't have a logout endpoint, return empty
	if logoutURL == "" {
		c.JSON(http.StatusOK, gin.H{
			"logout_url": "",
			"enabled":    true,
			"message":    "OIDC provider does not support logout endpoint",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"logout_url":               logoutURL,
		"post_logout_redirect_uri": postLogoutRedirectURI,
		"enabled":                  true,
	})
}

// Logout invalidates the current session
// DELETE /api/v2.1/auth/session
func (h *AuthHandler) Logout(c *gin.Context) {
	// Get token from Authorization header
	authHeader := c.GetHeader("Authorization")
	var token string
	if strings.HasPrefix(authHeader, "Token ") {
		token = strings.TrimPrefix(authHeader, "Token ")
	} else if strings.HasPrefix(authHeader, "Bearer ") {
		token = strings.TrimPrefix(authHeader, "Bearer ")
	}

	if token != "" {
		if err := h.sessions.InvalidateSession(token); err != nil {
			log.Printf("Failed to invalidate session: %v", err)
		}
	}

	// Clear the sesamefs_auth session cookie (match flags from login: path="/", httpOnly=false)
	isSecure := c.Request.TLS != nil
	c.SetCookie("sesamefs_auth", "", -1, "/", "", isSecure, false)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
	})
}
