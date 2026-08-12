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

func (h *AuthHandler) resolveOIDCRedirectURI(c *gin.Context) string {
	redirectURI := c.Query("redirect_uri")
	if redirectURI != "" {
		return redirectURI
	}

	for _, configured := range h.config.Auth.OIDC.RedirectURIs {
		configured = strings.TrimSpace(configured)
		if configured != "" {
			return configured
		}
	}

	scheme := "https"
	if c.Request.TLS == nil && strings.HasPrefix(c.Request.Host, "localhost") {
		scheme = "http"
	}
	return scheme + "://" + c.Request.Host + "/sso"
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

	redirectURI := h.resolveOIDCRedirectURI(c)
	returnURL := c.Query("return_url")

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
	// Cookie TTL matches session TTL so both expire at the same time.
	seahubAuth := result.Email + "@" + result.SessionToken
	cookieMaxAge := int(h.config.Auth.OIDC.SessionTTL.Seconds())
	h.setAuthCookie(c, seahubAuth, cookieMaxAge)

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

	// Return only the minimum public signal the unauthenticated login shell needs.
	// Login URL construction happens on a separate endpoint, so exposing issuer,
	// client_id, scopes, or redirect URIs here only increases reconnaissance value.
	c.JSON(http.StatusOK, gin.H{
		"enabled": true,
	})
}

func extractAuthorizationToken(authHeader string) string {
	if strings.HasPrefix(authHeader, "Token ") {
		return strings.TrimPrefix(authHeader, "Token ")
	}
	if strings.HasPrefix(authHeader, "Bearer ") {
		return strings.TrimPrefix(authHeader, "Bearer ")
	}
	return ""
}

// GetSessionInfo returns information about the current session
// GET /api/v2.1/auth/session
func (h *AuthHandler) GetSessionInfo(c *gin.Context) {
	// Get token from Authorization header
	token := extractAuthorizationToken(c.GetHeader("Authorization"))

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
	token := extractAuthorizationToken(c.GetHeader("Authorization"))
	if token == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "No token provided"})
		return
	}

	if _, err := h.sessions.ValidateSession(token); err != nil {
		if !errors.Is(err, auth.ErrSessionExpired) &&
			!errors.Is(err, auth.ErrSessionInvalid) &&
			!errors.Is(err, auth.ErrSessionRevoked) &&
			!errors.Is(err, auth.ErrSessionNotFound) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Session validation unavailable"})
			return
		}
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired session"})
		return
	}

	if err := h.sessions.InvalidateSession(token); err != nil {
		log.Printf("Failed to invalidate session: %v", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Failed to invalidate session"})
		return
	}

	// Clear the sesamefs_auth session cookie (match flags from login).
	h.setAuthCookie(c, "", -1)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
	})
}

// setAuthCookie is the shared writer for sesamefs_auth issuance and clearing in
// this package, used by both HandleOIDCCallback (login) and Logout (clear) so the
// flags cannot drift between the two.
//
// httpOnly is always true: ISSUE-SESSION-COOKIE-NOT-HTTPONLY-01 found no code in
// this repository — including mobile-frontend — that reads this cookie's value from
// JS; the desktop-client SSO flow gets its token via clientSSOStore polling, not by
// reading this cookie as an older comment here used to claim. Secure is still
// derived per-request from c.Request.TLS; the one site that hardcodes it lives in
// package api (handleAutoLogin) and is the separate, still-open
// ISSUE-AUTOLOGIN-COOKIE-INSECURE-01.
func (h *AuthHandler) setAuthCookie(c *gin.Context, value string, maxAge int) {
	isSecure := c.Request.TLS != nil
	c.SetCookie("sesamefs_auth", value, maxAge, "/", "", isSecure, true)
}
