package api

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Sesame-Disk/sesamefs/internal/api/v2"
	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/gin-gonic/gin"
)

// Server represents the HTTP API server
type Server struct {
	config   *config.Config
	db       *db.DB
	router   *gin.Engine
	server   *http.Server
}

// NewServer creates a new API server
func NewServer(cfg *config.Config, database *db.DB) *Server {
	// Set Gin mode based on dev mode
	if !cfg.Auth.DevMode {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.New()
	router.Use(gin.Recovery())
	router.Use(gin.Logger())

	s := &Server{
		config: cfg,
		db:     database,
		router: router,
	}

	s.setupRoutes()

	return s
}

// setupRoutes configures all API routes
func (s *Server) setupRoutes() {
	// Health check endpoints
	s.router.GET("/ping", s.handlePing)
	s.router.GET("/health", s.handleHealth)

	// API v2 routes
	apiV2 := s.router.Group("/api/v2")
	{
		// Public endpoints
		apiV2.GET("/ping", s.handlePing)

		// Auth endpoints
		auth := apiV2.Group("/auth")
		{
			auth.POST("/token", s.handleNotImplemented)
			auth.POST("/refresh", s.handleNotImplemented)
			auth.DELETE("/token", s.handleNotImplemented)
			auth.GET("/userinfo", s.authMiddleware(), s.handleNotImplemented)
		}

		// Protected endpoints - require authentication
		protected := apiV2.Group("")
		protected.Use(s.authMiddleware())
		{
			// Library endpoints
			v2.RegisterLibraryRoutes(protected, s.db, s.config)

			// File endpoints
			v2.RegisterFileRoutes(protected, s.db, s.config)

			// Share link endpoints
			v2.RegisterShareRoutes(protected, s.db, s.config)

			// Restore job endpoints (Glacier)
			v2.RegisterRestoreRoutes(protected, s.db, s.config)
		}
	}

	// Seafile sync protocol (Phase 2)
	// seafhttp := s.router.Group("/seafhttp")
	// {
	//     seafhttp.Use(s.authMiddleware())
	//     seafhttp.RegisterSeafileRoutes(seafhttp, s.db, s.config)
	// }
}

// authMiddleware validates authentication tokens
func (s *Server) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Get token from header
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "missing authorization header"})
			c.Abort()
			return
		}

		// Parse "Token <token>" format (Seafile compatible)
		var token string
		if _, err := fmt.Sscanf(authHeader, "Token %s", &token); err != nil {
			// Try "Bearer <token>" format
			if _, err := fmt.Sscanf(authHeader, "Bearer %s", &token); err != nil {
				c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid authorization header format"})
				c.Abort()
				return
			}
		}

		// In dev mode, check dev tokens
		if s.config.Auth.DevMode {
			for _, devToken := range s.config.Auth.DevTokens {
				if devToken.Token == token {
					c.Set("user_id", devToken.UserID)
					c.Set("org_id", devToken.OrgID)
					c.Next()
					return
				}
			}
		}

		// TODO: Validate OIDC token
		// For now, reject if not a dev token
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
		c.Abort()
	}
}

// handlePing returns a simple pong response
func (s *Server) handlePing(c *gin.Context) {
	c.String(http.StatusOK, "pong")
}

// handleHealth returns server health status
func (s *Server) handleHealth(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "healthy",
		"version": "dev",
	})
}

// handleNotImplemented returns a 501 Not Implemented response
func (s *Server) handleNotImplemented(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"error": "not implemented yet"})
}

// Run starts the HTTP server
func (s *Server) Run() error {
	s.server = &http.Server{
		Addr:         s.config.Server.Port,
		Handler:      s.router,
		ReadTimeout:  s.config.Server.ReadTimeout,
		WriteTimeout: s.config.Server.WriteTimeout,
	}

	return s.server.ListenAndServe()
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown(ctx context.Context) error {
	if s.server != nil {
		return s.server.Shutdown(ctx)
	}
	return nil
}
