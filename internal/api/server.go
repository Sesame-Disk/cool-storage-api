package api

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/api/v2"
	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

// Server represents the HTTP API server
type Server struct {
	config         *config.Config
	db             *db.DB
	storage        *storage.S3Store    // Legacy single S3 store
	storageManager *storage.Manager    // Multi-backend storage manager
	blockStore     *storage.BlockStore // Legacy single block store
	tokenStore     TokenStore
	router         *gin.Engine
	server         *http.Server
}

// NewServer creates a new API server
func NewServer(cfg *config.Config, database *db.DB) *Server {
	// Set Gin mode based on dev mode
	if !cfg.Auth.DevMode {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.New()
	// Disable trailing slash redirect - Seafile clients send POST to /api2/repos/
	// and Gin's 307 redirect breaks POST requests
	router.RedirectTrailingSlash = false
	router.RedirectFixedPath = false
	router.HandleMethodNotAllowed = true

	router.Use(gin.Recovery())
	router.Use(gin.Logger())

	// CORS middleware for frontend access
	corsConfig := cors.Config{
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "Seafile-Repo-Token"},
		ExposeHeaders:    []string{"Content-Length", "Content-Type"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}
	// In dev mode, allow all origins; in production, use configured origins
	if cfg.Auth.DevMode {
		corsConfig.AllowAllOrigins = true
	} else if len(cfg.CORS.AllowedOrigins) > 0 {
		corsConfig.AllowOrigins = cfg.CORS.AllowedOrigins
	} else {
		// Default to allowing all origins if not configured
		corsConfig.AllowAllOrigins = true
	}
	router.Use(cors.New(corsConfig))

	// Initialize storage manager with multi-backend support
	storageManager := initStorageManager(cfg)

	// Initialize legacy S3 storage (for backward compatibility)
	s3Store, err := initS3Storage(cfg)
	if err != nil {
		log.Printf("Warning: Failed to initialize legacy S3 storage: %v", err)
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
		config:         cfg,
		db:             database,
		storage:        s3Store,
		storageManager: storageManager,
		blockStore:     blockStore,
		tokenStore:     tokenStore,
		router:         router,
	}

	s.setupRoutes()

	return s
}

// initStorageManager initializes the multi-backend storage manager
func initStorageManager(cfg *config.Config) *storage.Manager {
	manager := storage.NewManager()

	// Set default class
	if cfg.Storage.DefaultClass != "" {
		manager.SetDefaultClass(cfg.Storage.DefaultClass)
	}

	// Set endpoint to region mapping
	if cfg.Storage.EndpointRegions != nil {
		manager.SetEndpointRegions(cfg.Storage.EndpointRegions)
	}

	// Set region to class mapping
	if cfg.Storage.RegionClasses != nil {
		regionClasses := make(map[string]storage.RegionClassConfig)
		for region, classes := range cfg.Storage.RegionClasses {
			regionClasses[region] = storage.RegionClassConfig{
				Hot:  classes.Hot,
				Cold: classes.Cold,
			}
		}
		manager.SetRegionClasses(regionClasses)
	}

	// Initialize storage classes from config
	for className, classCfg := range cfg.Storage.Classes {
		s3Store, err := initStorageClass(className, classCfg)
		if err != nil {
			log.Printf("Warning: Failed to initialize storage class %s: %v", className, err)
			continue
		}
		manager.RegisterBackend(className, s3Store, classCfg.FailoverClass)
		log.Printf("Registered storage class: %s (type=%s, tier=%s, bucket=%s)",
			className, classCfg.Type, classCfg.Tier, classCfg.Bucket)
	}

	// Log summary
	backends := manager.ListBackends()
	log.Printf("Storage manager initialized with %d backends: %v", len(backends), backends)

	return manager
}

// initStorageClass creates an S3Store for a storage class config
func initStorageClass(name string, cfg config.StorageClassConfig) (*storage.S3Store, error) {
	// Get credentials from config or environment
	accessKey := cfg.AccessKey
	secretKey := cfg.SecretKey
	if accessKey == "" {
		accessKey = os.Getenv("AWS_ACCESS_KEY_ID")
	}
	if secretKey == "" {
		secretKey = os.Getenv("AWS_SECRET_ACCESS_KEY")
	}

	region := cfg.Region
	if region == "" {
		region = "us-east-1"
	}

	// Determine access type from tier
	accessType := storage.AccessImmediate
	if cfg.Tier == "cold" {
		accessType = storage.AccessDelayed
	}

	s3Cfg := storage.S3Config{
		Endpoint:        cfg.Endpoint,
		Bucket:          cfg.Bucket,
		Region:          region,
		AccessKeyID:     accessKey,
		SecretAccessKey: secretKey,
		UsePathStyle:    cfg.UsePathStyle || cfg.Endpoint != "",
		AccessType:      accessType,
	}

	return storage.NewS3Store(context.Background(), s3Cfg)
}

// initS3Storage initializes the S3 storage backend (legacy, single backend)
func initS3Storage(cfg *config.Config) (*storage.S3Store, error) {
	// Get S3 configuration from environment or config
	endpoint := os.Getenv("S3_ENDPOINT")
	bucket := os.Getenv("S3_BUCKET")
	region := os.Getenv("AWS_REGION")
	accessKey := os.Getenv("AWS_ACCESS_KEY_ID")
	secretKey := os.Getenv("AWS_SECRET_ACCESS_KEY")

	// Fall back to config if not in environment
	if bucket == "" {
		// Try new storage classes first
		if defaultClass, ok := cfg.Storage.Classes[cfg.Storage.DefaultClass]; ok {
			if endpoint == "" {
				endpoint = defaultClass.Endpoint
			}
			bucket = defaultClass.Bucket
			if region == "" {
				region = defaultClass.Region
			}
			if accessKey == "" {
				accessKey = defaultClass.AccessKey
			}
			if secretKey == "" {
				secretKey = defaultClass.SecretKey
			}
		} else if hotBackend, ok := cfg.Storage.Backends["hot"]; ok {
			// Fall back to legacy backends
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
			// Library endpoints (with token creator for sync token generation)
			v2.RegisterLibraryRoutesWithToken(protected, s.db, s.config, s.tokenStore)

			// File endpoints (with Seafile-compatible URL generation)
			v2.RegisterFileRoutes(protected, s.db, s.config, s.storage, s.tokenStore, serverURL)

			// Block endpoints (content-addressable storage)
			if s.blockStore != nil || s.storageManager != nil {
				v2.RegisterBlockRoutes(protected, s.blockStore, s.storageManager, s.config)
			}

			// Share link endpoints
			v2.RegisterShareRoutes(protected, s.db, s.config)

			// Restore job endpoints (Glacier)
			v2.RegisterRestoreRoutes(protected, s.db, s.config)
		}
	}

	// Legacy /api2/ routes for Seafile CLI compatibility
	// The Seafile CLI uses /api2/ prefix (no version in path)
	// Routes registered WITHOUT trailing slashes since our wrapper strips them from requests
	api2 := s.router.Group("/api2")
	{
		// Auth token endpoint (used by seaf-cli for login)
		api2.POST("/auth-token", s.handleAuthToken)

		// Ping/server info
		api2.GET("/ping", s.handlePing)
		api2.GET("/server-info", s.handleServerInfo)

		// Account info
		api2.GET("/account/info", s.authMiddleware(), s.handleAccountInfo)

		// Protected endpoints
		protected := api2.Group("")
		protected.Use(s.authMiddleware())
		{
			// Library endpoints (same handlers as v2, with token creator)
			v2.RegisterLibraryRoutesWithToken(protected, s.db, s.config, s.tokenStore)

			// File endpoints
			v2.RegisterFileRoutes(protected, s.db, s.config, s.storage, s.tokenStore, serverURL)

			// Repo tokens endpoint (for getting sync tokens for multiple repos)
			protected.GET("/repo-tokens", s.handleRepoTokens)
		}
	}

	// Seafile-compatible file transfer endpoints (seafhttp)
	// These endpoints handle the actual file uploads/downloads
	seafHTTPHandler := NewSeafHTTPHandler(s.storage, s.tokenStore)
	seafHTTPHandler.RegisterSeafHTTPRoutes(s.router)

	// Seafile sync protocol endpoints (for Desktop client)
	// These endpoints handle repository synchronization
	// Uses a different auth middleware that accepts repo tokens
	syncHandler := NewSyncHandler(s.db, s.storage, s.blockStore, s.storageManager)
	syncHandler.RegisterSyncRoutes(s.router, s.syncAuthMiddleware())
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

// syncAuthMiddleware validates authentication for sync protocol endpoints
// It accepts multiple auth methods:
// 1. Seafile-Repo-Token header (repo-specific token from download-info)
// 2. Authorization: Token header (standard API token)
// 3. token query parameter
func (s *Server) syncAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		var token string

		// Try Seafile-Repo-Token header first (used by desktop client)
		token = c.GetHeader("Seafile-Repo-Token")

		// Try Authorization header if Seafile-Repo-Token not present
		if token == "" {
			authHeader := c.GetHeader("Authorization")
			if authHeader != "" {
				fmt.Sscanf(authHeader, "Token %s", &token)
				if token == "" {
					fmt.Sscanf(authHeader, "Bearer %s", &token)
				}
			}
		}

		// Try query parameter as last resort
		if token == "" {
			token = c.Query("token")
		}

		// If no token found, allow the request anyway for now
		// The sync protocol may authenticate at a different level
		if token == "" {
			// For sync endpoints, we'll be more lenient during development
			// Set default org_id and user_id
			c.Set("user_id", s.config.Auth.DevTokens[0].UserID)
			c.Set("org_id", s.config.Auth.DevTokens[0].OrgID)
			c.Next()
			return
		}

		// Check if it's a dev token
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

		// Check if it's a valid repo token (from download-info)
		// These tokens are stored in our token store
		if accessToken, valid := s.tokenStore.GetToken(token, TokenTypeDownload); valid {
			c.Set("user_id", accessToken.UserID)
			c.Set("org_id", accessToken.OrgID)
			c.Set("repo_id", accessToken.RepoID)
			c.Next()
			return
		}

		// For development, be lenient and use default credentials
		if s.config.Auth.DevMode && len(s.config.Auth.DevTokens) > 0 {
			c.Set("user_id", s.config.Auth.DevTokens[0].UserID)
			c.Set("org_id", s.config.Auth.DevTokens[0].OrgID)
			c.Next()
			return
		}

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
			// The username should match the user_id, email format, or token as password
			expectedEmail := devToken.UserID + "@sesamefs.local"
			if devToken.UserID == username || expectedEmail == username || devToken.Token == password {
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

// handleRepoTokens returns sync tokens for the specified repositories
// GET /api2/repo-tokens?repos=uuid1,uuid2,...
func (s *Server) handleRepoTokens(c *gin.Context) {
	userID := c.GetString("user_id")
	orgID := c.GetString("org_id")
	reposParam := c.Query("repos")

	if reposParam == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "repos parameter required"})
		return
	}

	// Parse repo IDs (comma-separated)
	repoIDs := strings.Split(reposParam, ",")

	// Generate tokens for each repo
	tokens := make(map[string]string)
	for _, repoID := range repoIDs {
		repoID = strings.TrimSpace(repoID)
		if repoID == "" {
			continue
		}

		// Verify the repo exists and user has access
		var libID string
		err := s.db.Session().Query(`
			SELECT library_id FROM libraries
			WHERE org_id = ? AND library_id = ?
		`, orgID, repoID).Scan(&libID)
		if err != nil {
			// Skip repos that don't exist or user doesn't have access to
			continue
		}

		// Generate a sync token for this repo
		token, err := s.tokenStore.CreateDownloadToken(orgID, repoID, "/", userID)
		if err != nil {
			continue
		}
		tokens[repoID] = token
	}

	c.JSON(http.StatusOK, tokens)
}

// trailingSlashHandler wraps a handler and strips trailing slashes from requests
type trailingSlashHandler struct {
	handler http.Handler
}

func (h *trailingSlashHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Strip trailing slash (except for root path)
	// This ensures /api2/repos/ is handled the same as /api2/repos
	if len(r.URL.Path) > 1 && r.URL.Path[len(r.URL.Path)-1] == '/' {
		r.URL.Path = r.URL.Path[:len(r.URL.Path)-1]
	}
	h.handler.ServeHTTP(w, r)
}

// Run starts the HTTP server
func (s *Server) Run() error {
	// Wrap router to strip trailing slashes before gin routing
	// This prevents gin's 307 redirect which breaks POST requests from Seafile clients
	handler := &trailingSlashHandler{handler: s.router}

	s.server = &http.Server{
		Addr:         s.config.Server.Port,
		Handler:      handler,
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
