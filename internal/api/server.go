package api

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/Sesame-Disk/sesamefs/internal/api/v2"
	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/gin-gonic/gin"
)

// Server represents the HTTP API server
type Server struct {
	config     *config.Config
	db         *db.DB
	storage    *storage.S3Store
	blockStore *storage.BlockStore
	tokenStore TokenStore
	router     *gin.Engine
	server     *http.Server
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

	// Initialize S3 storage
	s3Store, err := initS3Storage(cfg)
	if err != nil {
		log.Printf("Warning: Failed to initialize S3 storage: %v", err)
		// Continue without S3 - file operations will fail gracefully
	}

	// Initialize token store for seafhttp
	// Use Cassandra-backed store if database is available (stateless, distributed)
	// Fall back to in-memory store if database is not available
	var tokenStore TokenStore
	if database != nil {
		dbTokenStore := db.NewTokenStore(database, cfg.SeafHTTP.TokenTTL)
		tokenStore = NewCassandraTokenAdapter(dbTokenStore)
		log.Println("Using Cassandra-backed token store (stateless, distributed)")
	} else {
		tokenStore = NewTokenManager(cfg.SeafHTTP.TokenTTL)
		log.Println("Using in-memory token store (not distributed)")
	}

	// Initialize block store for content-addressable storage
	var blockStore *storage.BlockStore
	if s3Store != nil {
		blockStore = storage.NewBlockStore(s3Store, "blocks/")
	}

	s := &Server{
		config:     cfg,
		db:         database,
		storage:    s3Store,
		blockStore: blockStore,
		tokenStore: tokenStore,
		router:     router,
	}

	s.setupRoutes()

	return s
}

// initS3Storage initializes the S3 storage backend
func initS3Storage(cfg *config.Config) (*storage.S3Store, error) {
	// Get S3 configuration from environment or config
	endpoint := os.Getenv("S3_ENDPOINT")
	bucket := os.Getenv("S3_BUCKET")
	region := os.Getenv("AWS_REGION")
	accessKey := os.Getenv("AWS_ACCESS_KEY_ID")
	secretKey := os.Getenv("AWS_SECRET_ACCESS_KEY")

	// Fall back to config if not in environment
	if bucket == "" {
		if hotBackend, ok := cfg.Storage.Backends["hot"]; ok {
			if endpoint == "" {
				endpoint = hotBackend.Endpoint
			}
			bucket = hotBackend.Bucket
			if region == "" {
				region = hotBackend.Region
			}
		}
	}

	if bucket == "" {
		return nil, fmt.Errorf("S3 bucket not configured")
	}

	if region == "" {
		region = "us-east-1"
	}

	s3Cfg := storage.S3Config{
		Endpoint:        endpoint,
		Bucket:          bucket,
		Region:          region,
		AccessKeyID:     accessKey,
		SecretAccessKey: secretKey,
		UsePathStyle:    endpoint != "", // Use path style for custom endpoints (MinIO)
		AccessType:      storage.AccessImmediate,
	}

	return storage.NewS3Store(context.Background(), s3Cfg)
}

// setupRoutes configures all API routes
func (s *Server) setupRoutes() {
	// Health check endpoints
	s.router.GET("/ping", s.handlePing)
	s.router.GET("/health", s.handleHealth)

	// Determine server URL for generating seafhttp URLs
	// In production, this should come from config or be auto-detected
	serverURL := os.Getenv("SERVER_URL")
	if serverURL == "" {
		// Default to the configured port
		serverURL = fmt.Sprintf("http://localhost%s", s.config.Server.Port)
	}

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

			// File endpoints (with Seafile-compatible URL generation)
			v2.RegisterFileRoutes(protected, s.db, s.config, s.storage, s.tokenStore, serverURL)

			// Block endpoints (content-addressable storage)
			if s.blockStore != nil {
				v2.RegisterBlockRoutes(protected, s.blockStore, s.config)
			}

			// Share link endpoints
			v2.RegisterShareRoutes(protected, s.db, s.config)

			// Restore job endpoints (Glacier)
			v2.RegisterRestoreRoutes(protected, s.db, s.config)
		}
	}

	// Legacy /api2/ routes for Seafile CLI compatibility
	// The Seafile CLI uses /api2/ prefix (no version in path)
	api2 := s.router.Group("/api2")
	{
		// Auth token endpoint (used by seaf-cli for login)
		api2.POST("/auth-token/", s.handleAuthToken)

		// Ping/server info
		api2.GET("/ping/", s.handlePing)
		api2.GET("/server-info/", s.handleServerInfo)

		// Account info
		api2.GET("/account/info/", s.authMiddleware(), s.handleAccountInfo)

		// Protected endpoints
		protected := api2.Group("")
		protected.Use(s.authMiddleware())
		{
			// Library endpoints (same handlers as v2)
			v2.RegisterLibraryRoutes(protected, s.db, s.config)

			// File endpoints
			v2.RegisterFileRoutes(protected, s.db, s.config, s.storage, s.tokenStore, serverURL)
		}
	}

	// Seafile-compatible file transfer endpoints (seafhttp)
	// These endpoints handle the actual file uploads/downloads
	seafHTTPHandler := NewSeafHTTPHandler(s.storage, s.tokenStore)
	seafHTTPHandler.RegisterSeafHTTPRoutes(s.router)

	// Seafile sync protocol endpoints (for Desktop client)
	// These endpoints handle repository synchronization
	syncHandler := NewSyncHandler(s.db, s.storage, s.blockStore)
	syncHandler.RegisterSyncRoutes(s.router, s.authMiddleware())
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

// handleAuthToken handles the Seafile CLI auth-token endpoint
// POST /api2/auth-token/ with username and password
func (s *Server) handleAuthToken(c *gin.Context) {
	username := c.PostForm("username")
	password := c.PostForm("password")

	if username == "" || password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "username and password required"})
		return
	}

	// In dev mode, check dev tokens by matching username
	if s.config.Auth.DevMode {
		for _, devToken := range s.config.Auth.DevTokens {
			// In dev mode, accept any password for configured users
			// The username should match the user_id or a configured email
			if devToken.UserID == username || devToken.Token == password {
				c.JSON(http.StatusOK, gin.H{
					"token": devToken.Token,
				})
				return
			}
		}
	}

	// TODO: Implement OIDC password grant or redirect to OIDC flow
	c.JSON(http.StatusUnauthorized, gin.H{
		"non_field_errors": "Unable to login with provided credentials.",
	})
}

// handleServerInfo returns server information for Seafile clients
// GET /api2/server-info/
func (s *Server) handleServerInfo(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"version":                     "10.0.0",  // Seafile version we're compatible with
		"encrypted_library_version":  2,
		"enable_encrypted_library":   true,
		"enable_repo_history_setting": true,
		"enable_reset_encrypted_repo_password": false,
	})
}

// handleAccountInfo returns account information for the authenticated user
// GET /api2/account/info/
func (s *Server) handleAccountInfo(c *gin.Context) {
	userID := c.GetString("user_id")
	orgID := c.GetString("org_id")

	// Return basic account info
	// In a full implementation, this would query the user from the database
	c.JSON(http.StatusOK, gin.H{
		"email":       userID + "@sesamefs.local", // Placeholder email
		"name":        userID,
		"login_id":    "",
		"department":  "",
		"contact_email": "",
		"institution": orgID,
		"is_staff":    false,
		"space_usage": 0,
		"total_space": -2, // -2 means unlimited
	})
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
