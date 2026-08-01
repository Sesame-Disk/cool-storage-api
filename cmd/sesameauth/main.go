// Command sesameauth is the optional, standalone local-authentication service
// for SesameFS. It is the only internet-facing login surface for local
// (username/password) accounts and is deployed as a separate container so it
// can be scaled and isolated independently — or simply not run at all, in which
// case the storage service keeps working with OIDC and/or dev tokens.
//
// It shares the Cassandra keyspace with the storage service: it reads the
// canonical users tables read-only, owns the user_credentials + login-failure
// tables, and mints normal sessions via the same SessionManager the storage
// service validates. No schema changes, no new session mechanism.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/auth"
	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/localauth"
	"github.com/Sesame-Disk/sesamefs/internal/logging"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

var Version = "dev"

func main() {
	_ = godotenv.Load()

	cfg, err := config.Load()
	if err != nil {
		slog.Error("sesameauth: failed to load configuration", "error", err)
		os.Exit(1)
	}
	logging.Setup(cfg.Auth.DevMode)

	database, err := db.New(cfg.Database)
	if err != nil {
		slog.Error("sesameauth: failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer database.Close()

	sessions := auth.NewSessionManager(&cfg.Auth.OIDC, database)
	svc := localauth.NewService(database.Session(), localauth.Policy{
		MinPasswordLength: cfg.Auth.Local.MinPasswordLength,
		MaxFailedAttempts: cfg.Auth.Local.MaxFailedAttempts,
		LockoutDuration:   cfg.Auth.Local.LockoutDuration,
	})

	h := &handler{cfg: cfg, sessions: sessions, svc: svc}

	if !cfg.Auth.DevMode {
		gin.SetMode(gin.ReleaseMode)
	}
	router := gin.New()
	router.RedirectTrailingSlash = false
	router.Use(gin.Recovery())

	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "healthy", "service": "sesameauth", "version": Version})
	})

	for _, base := range []string{"/api/v2.1", "/api/v2"} {
		g := router.Group(base + "/auth")
		g.GET("/methods", h.methods)
		g.POST("/local/login", h.login)
		g.POST("/local/change-password", h.changePassword)
	}

	// cfg.Server.Port is already a full listen address (e.g. ":8080").
	addr := cfg.Server.Port
	srv := &http.Server{Addr: addr, Handler: router, ReadHeaderTimeout: 10 * time.Second}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		slog.Info("sesameauth starting", "version", Version, "addr", addr, "local_enabled", cfg.Auth.Local.Enabled)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("sesameauth: server failed", "error", err)
			os.Exit(1)
		}
	}()

	<-stop
	slog.Info("sesameauth: shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

type handler struct {
	cfg      *config.Config
	sessions *auth.SessionManager
	svc      *localauth.Service
}

// methods advertises which auth methods are enabled so a login UI can render
// the right options.
func (h *handler) methods(c *gin.Context) {
	oidcEnabled := h.cfg.Auth.OIDC.Enabled && h.cfg.Auth.OIDC.Issuer != "" && h.cfg.Auth.OIDC.ClientID != ""
	c.JSON(http.StatusOK, gin.H{
		"local": h.cfg.Auth.Local.Enabled,
		"oidc":  oidcEnabled,
	})
}

func (h *handler) login(c *gin.Context) {
	if !h.cfg.Auth.Local.Enabled {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "local authentication is not enabled"})
		return
	}
	var req struct {
		Email    string `json:"email" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "email and password are required"})
		return
	}

	id, err := h.svc.Authenticate(req.Email, req.Password, c.ClientIP(), time.Now())
	if err != nil {
		switch {
		case errors.Is(err, localauth.ErrLockedOut):
			c.JSON(http.StatusTooManyRequests, gin.H{"error": err.Error()})
		case errors.Is(err, localauth.ErrInvalidCredentials):
			c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		case errors.Is(err, localauth.ErrAccountInactive):
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		default:
			slog.Error("sesameauth: authenticate failed", "error", err)
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "authentication temporarily unavailable"})
		}
		return
	}

	session, err := h.sessions.CreateSession(id.UserID, id.OrgID, id.Email, id.Role)
	if err != nil {
		slog.Error("sesameauth: create session failed", "error", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "could not create session"})
		return
	}

	// Match the sesamefs_auth cookie the OIDC callback sets, so browser-based
	// panels identify the user identically regardless of login method.
	seahubAuth := id.Email + "@" + session.Token
	isSecure := c.Request.TLS != nil
	cookieMaxAge := int(h.cfg.Auth.OIDC.SessionTTL.Seconds())
	c.SetCookie("sesamefs_auth", seahubAuth, cookieMaxAge, "/", "", isSecure, false)

	c.JSON(http.StatusOK, gin.H{
		"token":                session.Token,
		"user_id":              id.UserID,
		"org_id":               id.OrgID,
		"email":                id.Email,
		"name":                 id.Name,
		"role":                 id.Role,
		"expires_at":           session.ExpiresAt.Unix(),
		"must_change_password": id.MustChange,
	})
}

func (h *handler) changePassword(c *gin.Context) {
	if !h.cfg.Auth.Local.Enabled {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "local authentication is not enabled"})
		return
	}
	token := bearerToken(c.GetHeader("Authorization"))
	if token == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "no token provided"})
		return
	}
	session, err := h.sessions.ValidateSession(token)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired session"})
		return
	}

	var req struct {
		CurrentPassword string `json:"current_password" binding:"required"`
		NewPassword     string `json:"new_password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "current_password and new_password are required"})
		return
	}

	if err := h.svc.ChangePassword(session.Email, req.CurrentPassword, req.NewPassword, c.ClientIP(), time.Now()); err != nil {
		if errors.Is(err, localauth.ErrInvalidCredentials) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "current password is incorrect"})
			return
		}
		// Policy violations surface their message directly.
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

func bearerToken(header string) string {
	if strings.HasPrefix(header, "Token ") {
		return strings.TrimPrefix(header, "Token ")
	}
	if strings.HasPrefix(header, "Bearer ") {
		return strings.TrimPrefix(header, "Bearer ")
	}
	return ""
}
