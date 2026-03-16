package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	v2 "github.com/Sesame-Disk/sesamefs/internal/api/v2"
	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/gc"
	"github.com/Sesame-Disk/sesamefs/internal/health"
	"github.com/Sesame-Disk/sesamefs/internal/httputil"
	"github.com/Sesame-Disk/sesamefs/internal/logging"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/Sesame-Disk/sesamefs/internal/templates"
	"github.com/gin-contrib/cors"
	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/time/rate"
)

// clientSSOEntry tracks a pending desktop-client SSO authentication.
// The Seafile desktop client calls POST /api2/client-sso-link to create one,
// opens the returned link in a browser, and polls GET /api2/client-sso-link/:token
// until status=="success" to retrieve the API token.
type clientSSOEntry struct {
	status    string // "pending" or "success"
	apiToken  string // session token, filled on success
	email     string // user email, filled on success
	createdAt time.Time
}

// clientSSOStore is a thread-safe in-memory store for pending SSO tokens.
type clientSSOStore struct {
	mu     sync.RWMutex
	tokens map[string]*clientSSOEntry
}

func newClientSSOStore() *clientSSOStore {
	s := &clientSSOStore{tokens: make(map[string]*clientSSOEntry)}
	go s.cleanupLoop()
	return s
}

func (s *clientSSOStore) create() (string, error) {
	b := make([]byte, 20) // 40-char hex, same as seahub's DRF token length
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
	s.mu.Lock()
	s.tokens[token] = &clientSSOEntry{status: "pending", createdAt: time.Now()}
	s.mu.Unlock()
	return token, nil
}

func (s *clientSSOStore) markSuccess(token, apiToken, email string) {
	s.mu.Lock()
	if entry, ok := s.tokens[token]; ok {
		entry.status = "success"
		entry.apiToken = apiToken
		entry.email = email
	}
	s.mu.Unlock()
}

func (s *clientSSOStore) get(token string) *clientSSOEntry {
	s.mu.RLock()
	entry := s.tokens[token]
	s.mu.RUnlock()
	return entry
}

func (s *clientSSOStore) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		cutoff := time.Now().Add(-15 * time.Minute)
		s.mu.Lock()
		for token, entry := range s.tokens {
			if entry.createdAt.Before(cutoff) {
				delete(s.tokens, token)
			}
		}
		s.mu.Unlock()
	}
}

// Server represents the HTTP API server
type Server struct {
	config          *config.Config
	db              *db.DB
	storage         *storage.S3Store    // Legacy single S3 store
	storageManager  *storage.Manager    // Multi-backend storage manager
	blockStore      *storage.BlockStore // Legacy single block store
	tokenStore      TokenStore
	permMiddleware  *middleware.PermissionMiddleware
	authHandler     *v2.AuthHandler         // OIDC authentication handler
	gcService       *gc.Service             // Garbage collection service
	ssoStore        *clientSSOStore         // Pending desktop-client SSO tokens
	authRateLimiter *middleware.RateLimiter // Per-IP rate limiter for auth endpoints
	version         string                  // Build version string
	router          *gin.Engine
	server          *http.Server
}

// NewServer creates a new API server
func NewServer(cfg *config.Config, database *db.DB, version string) *Server {
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
	router.Use(logging.GinMiddleware())
	// Gzip for API responses but exclude binary file transfer paths.
	// These paths stream large files and gzip would buffer them entirely,
	// waste CPU on already-compressed content, and break streaming.
	router.Use(gzip.Gzip(gzip.DefaultCompression,
		gzip.WithExcludedPathsRegexs([]string{
			"/seafhttp/files/.*",     // file downloads
			"/seafhttp/zip/.*",       // zip downloads
			"/seafhttp/upload/.*",    // file uploads
			"/api/v2.1/.*raw/.*",     // raw file serving (inline preview)
			"/api/v2.1/.*history/.*", // historic file downloads
		}),
	))

	// Register Prometheus metrics and add metrics middleware
	if cfg.Monitoring.MetricsEnabled {
		metrics.Register()
		router.Use(metrics.GinMiddleware())
	}

	// CORS middleware for frontend access
	corsConfig := cors.Config{
		AllowMethods: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders: []string{
			"Origin",
			"Content-Type",
			"Content-Length",
			"Content-Range",       // Required for resumable.js chunked uploads
			"Content-Disposition", // Required for filename in uploads
			"Accept",
			"Authorization",
			"Seafile-Repo-Token",
			"X-Requested-With", // Common AJAX header
		},
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
		slog.Warn("Failed to initialize legacy S3 storage", "error", err)
		// Continue without S3 - file operations will fail gracefully
	}

	// Initialize token store for seafhttp
	// Use Cassandra-backed store if database is available (stateless, distributed)
	// Fall back to in-memory store if database is not available
	var tokenStore TokenStore
	if database != nil {
		dbTokenStore := db.NewTokenStore(database, cfg.SeafHTTP.TokenTTL)
		tokenStore = NewCassandraTokenAdapter(dbTokenStore)
		slog.Info("Using Cassandra-backed token store (stateless, distributed)")
	} else {
		tokenStore = NewTokenManager(cfg.SeafHTTP.TokenTTL)
		slog.Info("Using in-memory token store (not distributed)")
	}

	// Initialize block store for content-addressable storage
	var blockStore *storage.BlockStore
	if s3Store != nil {
		blockStore = storage.NewBlockStore(s3Store, "blocks/")
	}

	// Initialize permission middleware
	permMiddleware := middleware.NewPermissionMiddleware(database)

	// Initialize OIDC auth handler
	authHandler := v2.NewAuthHandler(database, cfg)

	// Initialize GC service
	var gcService *gc.Service
	if database != nil {
		store := gc.NewCassandraStore(database)
		var storageProvider gc.StorageProvider
		if storageManager != nil {
			storageProvider = gc.NewStorageManagerAdapter(storageManager)
		}
		gcService = gc.NewService(store, storageProvider, cfg.GC)
	}

	// Initialize rate limiter for auth endpoints (~10 req/min per IP)
	authRL := middleware.NewRateLimiter(rate.Every(6*time.Second), 10)

	s := &Server{
		config:          cfg,
		db:              database,
		storage:         s3Store,
		storageManager:  storageManager,
		blockStore:      blockStore,
		tokenStore:      tokenStore,
		permMiddleware:  permMiddleware,
		authHandler:     authHandler,
		gcService:       gcService,
		ssoStore:        newClientSSOStore(),
		authRateLimiter: authRL,
		version:         version,
		router:          router,
	}

	s.setupRoutes()

	// Start GC service after routes are set up
	if gcService != nil {
		gcService.Start()
	}

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
			slog.Warn("Failed to initialize storage class", "class", className, "error", err)
			continue
		}
		manager.RegisterBackend(className, s3Store, classCfg.FailoverClass)
		slog.Info("Registered storage class", "class", className, "type", classCfg.Type, "tier", classCfg.Tier, "bucket", classCfg.Bucket)
	}

	// Legacy "backends:" support — register any backends not already covered by "classes:".
	// The "backends:" format is used by single-region deployments (e.g. config.prod.yaml with
	// a single AWS S3 bucket). Multi-region deployments use "classes:" instead.
	// Both formats register backends under the same storage manager so the rest of the code
	// (GetHealthyBlockStore, ResolveStorageClass, etc.) works identically regardless of which
	// config format was used.
	for name, backendCfg := range cfg.Storage.Backends {
		if _, alreadyRegistered := manager.GetBackend(name); alreadyRegistered {
			continue
		}
		classCfg := config.StorageClassConfig{
			Type:     backendCfg.Type,
			Bucket:   backendCfg.Bucket,
			Region:   backendCfg.Region,
			Endpoint: backendCfg.Endpoint,
		}
		s3Store, err := initStorageClass(name, classCfg)
		if err != nil {
			slog.Warn("Failed to initialize legacy storage backend", "backend", name, "error", err)
			continue
		}
		manager.RegisterBackend(name, s3Store, "")
		slog.Info("Registered legacy storage backend", "backend", name, "bucket", backendCfg.Bucket)
	}

	// Log summary
	backends := manager.ListBackends()
	slog.Info("Storage manager initialized", "backend_count", len(backends), "backends", backends)

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
	// ========================================================================
	// PERMISSION SYSTEM OVERVIEW
	// ========================================================================
	//
	// Permission checks are enforced at multiple levels:
	//
	// 1. AUTHENTICATION (applied via authMiddleware at group level)
	//    - Validates user token and sets user_id + org_id in context
	//    - All protected routes require authentication
	//
	// 2. ORGANIZATION ROLES (checked in handlers where needed)
	//    - admin: Full organization access
	//    - user: Can create libraries, upload files, share
	//    - readonly: Can only view content
	//    - guest: Limited access to shared content only
	//
	// 3. LIBRARY PERMISSIONS (checked in handlers)
	//    - owner: Full control (delete library, manage shares)
	//    - rw: Read and write files
	//    - r: Read-only access
	//    - Includes permissions inherited from group shares
	//
	// 4. GROUP ROLES (checked in group handlers)
	//    - owner: Created the group, can delete, manage all members
	//    - admin: Can add/remove members (except owner)
	//    - member: Regular member, no management privileges
	//
	// Permission middleware is available via s.permMiddleware but most checks
	// are done inside handlers to allow for flexible logic (e.g., owner OR rw permission).
	//
	// See: internal/middleware/permissions.go for implementation details
	//      internal/middleware/README.md for usage examples
	//
	// ========================================================================

	// Set up GC hooks so that v2 handlers can enqueue blocks/libraries for garbage collection
	if s.gcService != nil {
		v2.SetGCHooks(
			&gcBlockEnqueuer{service: s.gcService},
			&gcLibraryEnqueuer{service: s.gcService},
		)
	}

	// Health check endpoints
	s.router.GET("/ping", s.handlePing)

	// Build health checker for liveness and readiness probes
	var storageChecker health.StorageChecker
	if s.storage != nil {
		storageChecker = s.storage
	}
	healthChecker := health.NewChecker(s.db, storageChecker, s.config.Monitoring.HealthTimeout, s.version)
	s.router.GET("/health", healthChecker.HandleLiveness)
	s.router.GET("/ready", healthChecker.HandleReadiness)

	// Prometheus metrics endpoint
	if s.config.Monitoring.MetricsEnabled {
		s.router.GET(s.config.Monitoring.MetricsPath, gin.WrapH(promhttp.Handler()))
	}

	// Logout endpoint - clears token and redirects to home
	s.router.GET("/accounts/logout", s.handleLogout)
	s.router.GET("/accounts/logout/", s.handleLogout)

	// Auto-login endpoint for desktop client "View on Cloud" feature
	s.router.GET("/client-login", s.handleAutoLogin)
	s.router.GET("/client-login/", s.handleAutoLogin)

	// OAuth/OIDC server-side endpoints for the Seafile desktop client SSO flow.
	// Flow: client POSTs /api2/client-sso-link → gets pending token T → opens
	// /client-sso/T/ in browser → OIDC auth → callback marks T as success →
	// client polls GET /api2/client-sso-link/T → gets {status:"success", apiToken:...}
	s.router.GET("/oauth/login", s.handleOAuthLogin)
	s.router.GET("/oauth/login/", s.handleOAuthLogin)
	// /client-sso/:token/ is the seahub-compatible path — matches seahub's
	// reverse('client_sso', args=[token]). The pending token is in the path segment.
	s.router.GET("/client-sso", s.handleOAuthLogin)
	s.router.GET("/client-sso/", s.handleOAuthLogin)
	s.router.GET("/client-sso/:token", s.handleOAuthLogin)
	s.router.GET("/client-sso/:token/", s.handleOAuthLogin)
	oauthRL := s.authRateLimiter.Limit()
	s.router.GET("/oauth/callback", oauthRL, s.handleOAuthCallback)
	s.router.GET("/oauth/callback/", oauthRL, s.handleOAuthCallback)

	// Determine server URL for generating seafhttp URLs
	// FILE_SERVER_ROOT takes highest priority (like Seahub's FILE_SERVER_ROOT setting)
	// SERVER_URL is second priority
	// Auto-detection from request Host is the fallback
	serverURL := os.Getenv("FILE_SERVER_ROOT")
	if serverURL != "" {
		// FILE_SERVER_ROOT should be the full base URL (e.g., http://localhost:8080)
		// Strip trailing /seafhttp if present — we append it ourselves
		serverURL = strings.TrimSuffix(serverURL, "/seafhttp")
		serverURL = strings.TrimSuffix(serverURL, "/")
	} else if v := os.Getenv("SERVER_URL"); v != "" {
		serverURL = strings.TrimSuffix(v, "/")
	} else {
		// Default to empty string — getBrowserURL will auto-detect from request
		serverURL = ""
	}

	// API v2 routes
	apiV2 := s.router.Group("/api/v2")
	{
		// Public endpoints
		apiV2.GET("/ping", s.handlePing)

		// OIDC auth endpoints (public - no auth required for login flow)
		v2.RegisterAuthRoutes(apiV2, s.db, s.config, s.authRateLimiter.Limit())

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
		authRL := s.authRateLimiter.Limit()
		api2.POST("/auth-token", authRL, s.handleAuthToken)

		// Client SSO link (desktop client SSO flow)
		// POST: creates pending token and returns the browser URL to open
		// GET: client polls for SSO completion status
		//   Seafile desktop clients use path segment: GET /api2/client-sso-link/TOKEN/
		//   Some clients may also use query param: GET /api2/client-sso-link/?token=TOKEN/
		api2.POST("/client-sso-link", authRL, s.handleClientSSOLink)
		api2.POST("/client-sso-link/", authRL, s.handleClientSSOLink)
		api2.GET("/client-sso-link", s.handleGetClientSSOLink)
		api2.GET("/client-sso-link/", s.handleGetClientSSOLink)
		api2.GET("/client-sso-link/:token", s.handleGetClientSSOLink)
		api2.GET("/client-sso-link/:token/", s.handleGetClientSSOLink)

		// Client login (desktop client "View on Cloud" auto-login)
		api2.POST("/client-login", s.handleClientLogin)

		// Ping/server info
		api2.GET("/ping", s.handlePing)
		api2.GET("/server-info", s.handleServerInfo)
		api2.GET("/server-info/", s.handleServerInfo)

		// Authenticated ping — SeaDrive/Seafile desktop clients poll this to verify token validity
		api2.GET("/auth/ping", s.authMiddleware(), s.handlePing)
		api2.GET("/auth/ping/", s.authMiddleware(), s.handlePing)

		// Account info
		api2.GET("/account/info", s.authMiddleware(), s.handleAccountInfo)

		// Starred files API
		v2.RegisterStarredRoutes(api2.Group("", s.authMiddleware()), s.db)

		// User avatars (stub - returns placeholder)
		api2.GET("/avatars/user/:email/resized/:size", s.handleUserAvatar)
		api2.GET("/avatars/user/:email/resized/:size/", s.handleUserAvatar)

		// Protected endpoints
		protected := api2.Group("")
		protected.Use(s.authMiddleware())
		{
			// Library endpoints (same handlers as v2, with token creator)
			v2.RegisterLibraryRoutesWithToken(protected, s.db, s.config, s.tokenStore)

			// File endpoints
			v2.RegisterFileRoutes(protected, s.db, s.config, s.storage, s.tokenStore, serverURL)

			// File sharing endpoints (shared-repos, beshared-repos)
			v2.RegisterFileShareRoutes(protected, s.db, s.permMiddleware)

			// User search endpoint (used by transfer dialog, share dialog)
			protected.GET("/search-user", s.handleSearchUser)
			protected.GET("/search-user/", s.handleSearchUser)

			// Search routes (seafile-js calls /api2/search/)
			v2.RegisterSearchRoutes(protected, s.db)

			// File/folder trash (recycle bin) routes
			v2.RegisterTrashRoutes(protected, s.db)

			// Deleted libraries (library recycle bin) — list endpoint for /api2/
			v2.RegisterDeletedLibraryRoutes(protected, s.db, nil)

			// Repo tokens endpoint (for getting sync tokens for multiple repos)
			protected.GET("/repo-tokens", s.handleRepoTokens)

			// Default repo — SeaDrive asks for the user's "My Library".
			// GET: returns whether a default repo exists (we return exists:false, we don't auto-create).
			// POST: Seafile client calls this to create the default repo when none exists.
			//       We stub it to avoid 405 Method Not Allowed; clients handle exists:false gracefully.
			protected.GET("/default-repo", s.handleDefaultRepo)
			protected.GET("/default-repo/", s.handleDefaultRepo)
			protected.POST("/default-repo", s.handleDefaultRepo)
			protected.POST("/default-repo/", s.handleDefaultRepo)

			// History limit settings (GET/PUT /api2/repos/:id/history-limit/)
			v2.RegisterHistoryLimitRoutes(protected, s.db, s.config)

			// Library transfer (PUT /api2/repos/:id/owner/)
			v2.RegisterLibraryTransferRoutes(protected, s.db, s.config)

			// Stub: Linked devices (not yet implemented)
			protected.GET("/devices", s.handleEmptyDevices)
			protected.GET("/devices/", s.handleEmptyDevices)
		}
	}

	// API v2.1 routes for Seahub frontend compatibility
	// The Seahub frontend uses /api/v2.1/ prefix with different response format
	apiV21 := s.router.Group("/api/v2.1")
	{
		// OIDC auth endpoints (public - no auth required for login flow)
		v2.RegisterAuthRoutes(apiV21, s.db, s.config, s.authRateLimiter.Limit())

		// Protected endpoints
		protected := apiV21.Group("")
		protected.Use(s.authMiddleware())
		{
			// Admin API endpoints (superadmin and tenant admin)
			v2.RegisterAdminRoutes(protected, s.db, s.config, s.permMiddleware, s.tokenStore, serverURL)

			// Org-admin API endpoints (/api/v2.1/org/... — tenant admin for own org)
			v2.RegisterOrgAdminRoutes(protected, s.db, s.config, s.permMiddleware)

			// GC admin endpoints (staff only)
			if s.gcService != nil {
				admin := protected.Group("/admin")
				admin.GET("/gc/status", s.handleGCStatus)
				admin.GET("/gc/status/", s.handleGCStatus)
				admin.POST("/gc/run", s.handleGCRun)
				admin.POST("/gc/run/", s.handleGCRun)
			}

			// Library endpoints with v2.1 response format
			v2.RegisterV21LibraryRoutes(protected, s.db, s.config, s.tokenStore, s.storage, s.blockStore, serverURL)

			// Batch delete endpoint for files/folders
			fileHandler := v2.NewFileHandler(s.db, s.config, s.storage, s.tokenStore, serverURL, s.permMiddleware)
			fileHandler.SetGCEnqueuer(v2.GetBlockEnqueuerFunc())
			protected.DELETE("/repos/batch-delete-item/", fileHandler.BatchDeleteItems)
			protected.DELETE("/repos/batch-delete-item", fileHandler.BatchDeleteItems)

			// Batch move/copy endpoints (sync and async)
			v2.RegisterBatchOperationRoutes(protected, s.db, s.config)

			// File history endpoint (seafile-js uses /api/v2.1/repos/:id/file/new_history/)
			protected.GET("/repos/:repo_id/file/new_history/", fileHandler.GetFileHistoryV21)
			protected.GET("/repos/:repo_id/file/new_history", fileHandler.GetFileHistoryV21)
			protected.GET("/repos/:repo_id/file/history/", fileHandler.GetFileHistoryV21)
			protected.GET("/repos/:repo_id/file/history", fileHandler.GetFileHistoryV21)

			// Repository (commit) history endpoint
			protected.GET("/repos/:repo_id/history/", fileHandler.GetRepoHistory)
			protected.GET("/repos/:repo_id/history", fileHandler.GetRepoHistory)

			// OnlyOffice integration endpoints
			v2.RegisterOnlyOfficeRoutes(protected, s.db, s.config, s.storage, s.tokenStore, serverURL)

			// Starred items for v2.1 API (uses "starred-items" instead of "starredfiles")
			v2.RegisterV21StarredRoutes(protected, s.db)

			// Share links for v2.1 API
			v2.RegisterShareLinkRoutes(protected, s.db, serverURL, s.permMiddleware)

			// Upload links for v2.1 API
			v2.RegisterUploadLinkRoutes(protected, s.db, serverURL, s.permMiddleware)

			// Groups for v2.1 API
			v2.RegisterGroupRoutes(protected, s.db)

			// Shareable groups (returns groups user can share with — same as user's groups)
			v2.RegisterShareableGroupRoutes(protected, s.db)

			// Monitored repos (watch/unwatch libraries)
			v2.RegisterMonitoredRepoRoutes(protected, s.db)

			// Search routes
			v2.RegisterSearchRoutes(protected, s.db)

			// Stub handlers for optional Seahub features (return empty results instead of 404)
			protected.GET("/activities", s.handleEmptyActivities)
			protected.GET("/activities/", s.handleEmptyActivities)
			protected.GET("/notifications", s.handleEmptyNotifications)
			protected.GET("/notifications/", s.handleEmptyNotifications)
			v21FileShareHandler := v2.NewFileShareHandler(s.db, s.permMiddleware)
			protected.GET("/shared-repos", v21FileShareHandler.ListSharedRepos)
			protected.GET("/shared-repos/", v21FileShareHandler.ListSharedRepos)
			protected.GET("/shared-folders", s.handleEmptySharedFolders)
			protected.GET("/shared-folders/", s.handleEmptySharedFolders)
			protected.GET("/wikis", s.handleEmptyWikis)
			protected.GET("/wikis/", s.handleEmptyWikis)
			protected.GET("/repo-folder-share-info", s.handleEmptyFolderShareInfo)
			protected.GET("/repo-folder-share-info/", s.handleEmptyFolderShareInfo)

			// User search endpoint (used by transfer dialog, share dialog)
			protected.GET("/search-user", s.handleSearchUser)
			protected.GET("/search-user/", s.handleSearchUser)

			// Departments (hierarchical groups managed by org admins)
			v2.RegisterDepartmentRoutes(protected, s.db, s.permMiddleware)

			// Library settings endpoints (auto-delete, API tokens)
			v2.RegisterV21LibrarySettingsRoutes(protected, s.db, s.config)

			// Share links per repo - handled by RegisterShareLinkRoutes

			// Smart link (internal permalink for files/folders)
			protected.GET("/smart-link", fileHandler.GetSmartLink)
			protected.GET("/smart-link/", fileHandler.GetSmartLink)
			protected.GET("/smart-link/:token", fileHandler.ResolveSmartLink)
			protected.GET("/smart-link/:token/", fileHandler.ResolveSmartLink)

			// Tag routes (fully implemented)
			v2.RegisterTagRoutes(protected, s.db)
		}
	}

	// OnlyOffice callback endpoints (not behind auth - OnlyOffice server calls these)
	onlyoffice := s.router.Group("/onlyoffice")
	{
		v2.RegisterOnlyOfficeCallbackRoutes(onlyoffice, s.db, s.config, s.storage, serverURL)
	}

	// Export share links to Excel (requires auth)
	exportHandler := v2.NewShareLinkHandler(s.db, serverURL, s.permMiddleware)
	s.router.GET("/share/link/export-excel/", s.authMiddleware(), exportHandler.ExportShareLinksExcel)
	s.router.GET("/share/link/export-excel", s.authMiddleware(), exportHandler.ExportShareLinksExcel)

	// Smart link resolve (internal permalink — requires auth, redirects to frontend file/folder view)
	smartLinkHandler := v2.NewFileHandler(s.db, s.config, s.storage, s.tokenStore, serverURL, s.permMiddleware)
	s.router.GET("/smart-link/:token", s.smartLinkAuthMiddleware(), smartLinkHandler.ResolveSmartLink)
	s.router.GET("/smart-link/:token/", s.smartLinkAuthMiddleware(), smartLinkHandler.ResolveSmartLink)

	// Public share link view (no auth middleware - validated by share link token)
	slv := v2.NewShareLinkViewHandler(s.db, s.config, s.storage, s.storageManager, s.tokenStore, serverURL)
	s.router.GET("/d/:token", slv.ServeShareLinkPage)

	// Share link file view: /d/:token/files/?p=/path - for viewing files inside shared dirs
	s.router.GET("/d/:token/files/", slv.ServeShareLinkFilePage)
	s.router.GET("/d/:token/files", slv.ServeShareLinkFilePage)

	// Share link password check (public, no auth)
	s.router.POST("/d/:token/check-password", slv.CheckShareLinkPassword)
	s.router.POST("/d/:token/check-password/", slv.CheckShareLinkPassword)

	// Upload link view: /u/d/:token - anonymous file upload page
	s.router.GET("/u/d/:token", slv.ServeUploadLinkPage)

	// Upload link password check (public, no auth)
	s.router.POST("/u/d/:token/check-password", slv.CheckUploadLinkPassword)
	s.router.POST("/u/d/:token/check-password/", slv.CheckUploadLinkPassword)

	// Share link directory listing API (public, token-validated internally)
	s.router.GET("/api/v2.1/share-links/:token/dirents/", slv.ListShareLinkDirents)
	s.router.GET("/api/v2.1/share-links/:token/dirents", slv.ListShareLinkDirents)

	// Share link zip download task (public, token-validated internally)
	s.router.GET("/api/v2.1/share-link-zip-task/", slv.GetShareLinkZipTask)
	s.router.GET("/api/v2.1/share-link-zip-task", slv.GetShareLinkZipTask)

	// ZIP progress query - our backend creates ZIPs on-the-fly so we return "complete" immediately
	// This prevents 404 errors when the frontend (ZipDownloadDialog) polls for progress
	zipProgressStub := func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"zipped": 1,
			"total":  1,
			"failed": 0,
		})
	}
	s.router.GET("/api/v2.1/query-zip-progress/", zipProgressStub)
	s.router.GET("/api/v2.1/query-zip-progress", zipProgressStub)

	// Cancel zip task stub (no-op since zips are created on-the-fly)
	cancelZipStub := func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true})
	}
	s.router.DELETE("/api/v2.1/cancel-zip-task/", cancelZipStub)
	s.router.DELETE("/api/v2.1/cancel-zip-task", cancelZipStub)

	// Upload link API endpoints (public, token-validated internally)
	s.router.GET("/api/v2.1/upload-links/:token/upload/", slv.GetUploadLinkUploadURL)
	s.router.GET("/api/v2.1/upload-links/:token/upload", slv.GetUploadLinkUploadURL)
	s.router.POST("/api/v2.1/upload-links/:token/upload-done/", slv.PostUploadLinkDone)
	s.router.POST("/api/v2.1/upload-links/:token/upload-done", slv.PostUploadLinkDone)

	// Share link upload API endpoints (public, token-validated internally)
	// For share links with can_upload permission
	s.router.GET("/api/v2.1/share-links/:token/upload/", slv.GetShareLinkUploadURL)
	s.router.GET("/api/v2.1/share-links/:token/upload", slv.GetShareLinkUploadURL)
	s.router.POST("/api/v2.1/share-links/:token/upload-done/", slv.PostShareLinkUploadDone)
	s.router.POST("/api/v2.1/share-links/:token/upload-done", slv.PostShareLinkUploadDone)

	// Share link repo tags API (returns empty - tags are user-specific organization)
	s.router.GET("/api/v2.1/share-links/:token/repo-tags/", slv.GetShareLinkRepoTags)
	s.router.GET("/api/v2.1/share-links/:token/repo-tags", slv.GetShareLinkRepoTags)

	// Office document conversion stub (no converter configured)
	officeConvertStub := func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ERROR"})
	}
	s.router.GET("/office-convert/status/", officeConvertStub)
	s.router.GET("/office-convert/status", officeConvertStub)

	// File viewer routes (for viewing files in browser, including OnlyOffice editor)
	v2.RegisterFileViewRoutes(s.router, s.db, s.config, s.storage, s.storageManager, s.tokenStore, serverURL, s.authMiddleware(), s.permMiddleware)

	// Seafile-compatible file transfer endpoints (seafhttp)
	// These endpoints handle the actual file uploads/downloads
	seafHTTPHandler := NewSeafHTTPHandler(s.storage, s.storageManager, s.db, s.tokenStore, s.permMiddleware)
	seafHTTPHandler.RegisterSeafHTTPRoutes(s.router)

	// Seafile sync protocol endpoints (for Desktop client)
	// These endpoints handle repository synchronization
	// Uses a different auth middleware that accepts repo tokens
	syncHandler := NewSyncHandler(s.db, s.storage, s.blockStore, s.storageManager, s.permMiddleware)
	syncHandler.SetTokenCreator(s.tokenStore) // Enable download-info endpoint
	syncHandler.RegisterSyncRoutes(s.router, s.syncAuthMiddleware())

	// Serve static files from frontend build with long-lived cache headers
	staticGroup := s.router.Group("/static", func(c *gin.Context) {
		c.Header("Cache-Control", "public, max-age=31536000, immutable")
		c.Next()
	})
	staticGroup.Static("/", "./frontend/build/static")

	s.router.Static("/media", "./frontend/public/media")

	// Serve mobile frontend static assets (dev mode mobile UA detection)
	mobileStaticGroup := s.router.Group("/mobile/static", func(c *gin.Context) {
		c.Header("Cache-Control", "public, max-age=31536000, immutable")
		c.Next()
	})
	mobileStaticGroup.Static("/", s.config.Server.MobileFrontendPath+"/static")

	// Serve favicon (browsers request /favicon.ico and /favicon.png)
	s.router.StaticFile("/favicon.png", "./frontend/build/favicon.png")
	s.router.StaticFile("/favicon.ico", "./frontend/build/favicon.png")

	// Org admin panel — server-side auth gate: only org admins or superadmins.
	orgGroup := s.router.Group("/org")
	orgGroup.Use(func(c *gin.Context) {
		userID, orgID, role := s.resolveUserAuth(c)
		if orgID == "" {
			c.Redirect(http.StatusFound, "/accounts/login/?next="+url.QueryEscape(c.Request.URL.Path))
			c.Abort()
			return
		}
		// Always check the DB role — the session role may be stale
		// (e.g. user promoted or demoted after login).
		if s.db != nil {
			var dbRole string
			if err := s.db.Session().Query(
				`SELECT role FROM users WHERE org_id = ? AND user_id = ?`, orgID, userID,
			).Scan(&dbRole); err == nil {
				role = dbRole
			}
		}
		if role != "admin" && role != "superadmin" {
			s.serveAccessDenied(c, "Access Denied", "You do not have permission to access the Organization Administration panel.")
			c.Abort()
			return
		}
		c.Next()
	})
	orgGroup.GET("", s.serveOrgAdminPanel)
	orgGroup.GET("/*path", s.serveOrgAdminPanel)

	// SPA catch-all: serve appropriate HTML for non-API routes
	// - /sys/* routes → sysadmin.html (admin panel, separate webpack entry)
	// - everything else → index.html (main app)
	s.router.NoRoute(func(c *gin.Context) {
		// Don't serve HTML for API routes
		if strings.HasPrefix(c.Request.URL.Path, "/api") ||
			strings.HasPrefix(c.Request.URL.Path, "/seafhttp") ||
			strings.HasPrefix(c.Request.URL.Path, "/onlyoffice") {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}

		// Resolve user email once for injection into whichever HTML we serve.
		email := s.resolveUserEmail(c)

		// Serve admin panel for /sys/* routes — gate by superadmin role
		if strings.HasPrefix(c.Request.URL.Path, "/sys/") || c.Request.URL.Path == "/sys" {
			authUserID, authOrgID, authRole := s.resolveUserAuth(c)
			if authOrgID == "" {
				c.Redirect(http.StatusFound, "/accounts/login/?next="+url.QueryEscape(c.Request.URL.Path))
				return
			}
			// Always check the DB role — the session role may be stale.
			if s.db != nil {
				var dbRole string
				if err := s.db.Session().Query(
					`SELECT role FROM users WHERE org_id = ? AND user_id = ?`, authOrgID, authUserID,
				).Scan(&dbRole); err == nil {
					authRole = dbRole
				}
			}
			if authRole != "superadmin" || authOrgID != middleware.PlatformOrgID {
				s.serveAccessDenied(c, "Access Denied", "You do not have permission to access the System Administration panel.")
				return
			}
			s.serveHTMLWithUser(c, "./frontend/build/sysadmin.html", email)
			return
		}
		// Mobile UA detection: serve mobile frontend if available
		viewMode := getViewMode(c.Request)

		// Set cookie when overriding via query param
		if c.Query("desktop") == "1" {
			http.SetCookie(c.Writer, &http.Cookie{
				Name:     "view_mode",
				Value:    "desktop",
				Path:     "/",
				MaxAge:   86400 * 30, // 30 days
				HttpOnly: true,
				SameSite: http.SameSiteLaxMode,
			})
		} else if c.Query("mobile") == "1" {
			http.SetCookie(c.Writer, &http.Cookie{
				Name:     "view_mode",
				Value:    "mobile",
				Path:     "/",
				MaxAge:   86400 * 30,
				HttpOnly: true,
				SameSite: http.SameSiteLaxMode,
			})
		}

		serveMobile := false
		if viewMode == "mobile" {
			serveMobile = true
		} else if viewMode == "desktop" {
			serveMobile = false
		} else if isMobileUA(c.Request.UserAgent()) {
			serveMobile = true
		}

		if serveMobile {
			mobilePath := s.config.Server.MobileFrontendPath + "/index.html"
			if _, err := os.Stat(mobilePath); err == nil {
				s.serveHTMLWithUser(c, mobilePath, email)
				return
			}
			// Mobile frontend not built — fall back to desktop
		}

		s.serveHTMLWithUser(c, "./frontend/build/index.html", email)
	})
}

// serveHTMLWithUser reads an HTML file, injects the user email placeholder, and serves it.
func (s *Server) serveHTMLWithUser(c *gin.Context, filepath string, email string) {
	content, err := os.ReadFile(filepath)
	if err != nil {
		c.File(filepath) // fallback to static serving
		return
	}
	injected := injectUserEmail(string(content), email)
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
	c.String(http.StatusOK, injected)
}

// serveOrgAdminPanel serves the org admin SPA shell (orgadmin.html) with
// the authenticated user's orgID and orgName injected into window.org.
// The page is served to everyone; React handles auth for API calls.
func (s *Server) serveOrgAdminPanel(c *gin.Context) {
	orgID, orgName := s.resolveOrgForPanel(c)

	content, err := os.ReadFile("./frontend/build/orgadmin.html")
	if err != nil {
		// orgadmin.html not built yet — redirect to index so the user isn't stuck
		c.File("./frontend/build/index.html")
		return
	}

	// Build the window.org config script and replace the placeholder.
	// Values are JSON-encoded to prevent XSS via malicious org names.
	orgScript := fmt.Sprintf(
		"window.org = { pageOptions: { orgID: %s, orgName: %s, "+
			"invitationLink: '', orgMemberQuotaEnabled: '', orgMembers: 0, "+
			"orgMembersQuota: 0, hasUserAvailability: true, "+
			"orgEnableAdminCustomLogo: 'False', orgEnableAdminCustomName: 'False', "+
			"orgEnableAdminInviteUser: 'False', enableMultiADFS: 'False', "+
			"enableSubscription: false } };",
		jsonQuote(orgID), jsonQuote(orgName),
	)
	// The minifier removes the trailing semicolon, so search without it.
	injected := strings.Replace(string(content), "window.org=window.__SESAME_ORG_PLACEHOLDER__", orgScript, 1)

	// Also inject user email (same placeholder as index.html and sysadmin.html).
	email := s.resolveUserEmail(c)
	injected = injectUserEmail(injected, email)

	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
	c.String(http.StatusOK, injected)
}

// serveAccessDenied renders the access_denied.html template with the given title and message.
func (s *Server) serveAccessDenied(c *gin.Context, title, message string) {
	html, err := templates.RenderString("access_denied.html", templates.AccessDeniedData{
		Title:   title,
		Message: message,
	})
	if err != nil {
		c.String(http.StatusForbidden, "Access denied")
		return
	}
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Header("Cache-Control", "no-cache, no-store, must-revalidate")
	c.String(http.StatusForbidden, html)
}

// resolveOrgForPanel extracts orgID/orgName for HTML page injection (best-effort, never blocks).
// Order: dev tokens → sesamefs_auth cookie → Authorization header → OIDC session → empty.
func (s *Server) resolveOrgForPanel(c *gin.Context) (orgID, orgName string) {
	// 1. Dev mode: match the request token against the configured dev tokens.
	if s.config.Auth.DevMode && len(s.config.Auth.DevTokens) > 0 {
		// Try to extract the token from the Authorization header or cookie.
		reqToken := ""
		if auth := c.GetHeader("Authorization"); auth != "" {
			fmt.Sscanf(auth, "Token %s", &reqToken) //nolint:errcheck
			if reqToken == "" {
				fmt.Sscanf(auth, "Bearer %s", &reqToken) //nolint:errcheck
			}
		}
		if reqToken == "" {
			if cookie, err := c.Cookie("sesamefs_auth"); err == nil && cookie != "" {
				if idx := strings.LastIndex(cookie, "@"); idx >= 0 && idx < len(cookie)-1 {
					reqToken = cookie[idx+1:]
				} else {
					reqToken = cookie
				}
			}
		}
		// Find the matching dev token; fall back to the first token if not found.
		matchedToken := s.config.Auth.DevTokens[0]
		for _, dt := range s.config.Auth.DevTokens {
			if dt.Token == reqToken {
				matchedToken = dt
				break
			}
		}
		orgID = matchedToken.OrgID
		if s.db != nil && orgID != "" {
			s.db.Session().Query( //nolint:errcheck
				`SELECT name FROM organizations WHERE org_id = ?`, orgID,
			).Scan(&orgName)
		}
		return
	}

	// 2. Extract raw token and email from cookie or Authorization header.
	token := ""
	cookieEmail := ""
	if cookie, err := c.Cookie("sesamefs_auth"); err == nil && cookie != "" {
		// Format: email@token — token is everything after the last '@'.
		if idx := strings.LastIndex(cookie, "@"); idx >= 0 && idx < len(cookie)-1 {
			cookieEmail = cookie[:idx]
			token = cookie[idx+1:]
		} else {
			// Fallback: cookie may be a raw token without email@ prefix.
			token = cookie
		}
	}
	if token == "" {
		if auth := c.GetHeader("Authorization"); auth != "" {
			fmt.Sscanf(auth, "Token %s", &token) //nolint:errcheck
			if token == "" {
				fmt.Sscanf(auth, "Bearer %s", &token) //nolint:errcheck
			}
		}
	}

	if token == "" || token == "undefined" || token == "null" {
		return
	}

	// 3. Try OIDC session (Cassandra-backed or JWT).
	if s.authHandler != nil {
		if mgr := s.authHandler.GetSessionManager(); mgr != nil {
			if session, err := mgr.ValidateSession(token); err == nil {
				orgID = session.OrgID
			}
		}
	}

	// 4. Fallback: if session validation failed, look up org by email from cookie.
	// This covers cases where the token in the cookie is stale (e.g., page was loaded
	// before a re-login that updated localStorage but not the cookie).
	if orgID == "" && cookieEmail != "" && s.db != nil {
		var emailOrgID string
		if qerr := s.db.Session().Query(
			`SELECT org_id FROM users_by_email WHERE email = ?`, cookieEmail,
		).Scan(&emailOrgID); qerr == nil && emailOrgID != "" {
			orgID = emailOrgID
		}
	}

	// 5. Best-effort org name lookup.
	if s.db != nil && orgID != "" {
		s.db.Session().Query( //nolint:errcheck
			`SELECT name FROM organizations WHERE org_id = ?`, orgID,
		).Scan(&orgName)
	}
	return
}

// jsonQuote returns a JSON-encoded string literal (with surrounding double-quotes),
// safe for embedding directly in a <script> block.
func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// resolveUserEmail extracts the authenticated user's email for HTML page injection (best-effort, never blocks).
// Returns empty string if user cannot be identified.
func (s *Server) resolveUserEmail(c *gin.Context) string {
	// 1. Dev mode: match request token against configured dev tokens.
	if s.config.Auth.DevMode && len(s.config.Auth.DevTokens) > 0 {
		reqToken := ""
		if auth := c.GetHeader("Authorization"); auth != "" {
			fmt.Sscanf(auth, "Token %s", &reqToken) //nolint:errcheck
			if reqToken == "" {
				fmt.Sscanf(auth, "Bearer %s", &reqToken) //nolint:errcheck
			}
		}
		if reqToken == "" {
			if cookie, err := c.Cookie("sesamefs_auth"); err == nil && cookie != "" {
				if idx := strings.LastIndex(cookie, "@"); idx >= 0 && idx < len(cookie)-1 {
					reqToken = cookie[idx+1:]
				} else {
					reqToken = cookie
				}
			}
		}
		matchedToken := s.config.Auth.DevTokens[0]
		for _, dt := range s.config.Auth.DevTokens {
			if dt.Token == reqToken {
				matchedToken = dt
				break
			}
		}
		if matchedToken.Email != "" {
			return matchedToken.Email
		}
		// Fallback: look up email from DB by user_id + org_id
		if s.db != nil && matchedToken.UserID != "" && matchedToken.OrgID != "" {
			var email string
			if err := s.db.Session().Query(
				`SELECT email FROM users WHERE org_id = ? AND user_id = ?`,
				matchedToken.OrgID, matchedToken.UserID,
			).Scan(&email); err == nil && email != "" {
				return email
			}
		}
		return matchedToken.UserID + "@sesamefs.local"
	}

	// 2. Extract email from cookie (format: email@token).
	if cookie, err := c.Cookie("sesamefs_auth"); err == nil && cookie != "" {
		if idx := strings.LastIndex(cookie, "@"); idx >= 0 && idx < len(cookie)-1 {
			return cookie[:idx]
		}
	}

	return ""
}

// resolveUserAuth extracts the authenticated user's userID, orgID and role
// from the request cookie or dev tokens. Used for server-side gating of SPA pages.
// Returns empty strings if the user cannot be identified.
func (s *Server) resolveUserAuth(c *gin.Context) (userID, orgID, role string) {
	// 1. Dev mode: match request token against configured dev tokens.
	if s.config.Auth.DevMode && len(s.config.Auth.DevTokens) > 0 {
		reqToken := ""
		if auth := c.GetHeader("Authorization"); auth != "" {
			fmt.Sscanf(auth, "Token %s", &reqToken) //nolint:errcheck
			if reqToken == "" {
				fmt.Sscanf(auth, "Bearer %s", &reqToken) //nolint:errcheck
			}
		}
		if reqToken == "" {
			if cookie, err := c.Cookie("sesamefs_auth"); err == nil && cookie != "" {
				if idx := strings.LastIndex(cookie, "@"); idx >= 0 && idx < len(cookie)-1 {
					reqToken = cookie[idx+1:]
				} else {
					reqToken = cookie
				}
			}
		}
		if reqToken != "" {
			for _, dt := range s.config.Auth.DevTokens {
				if dt.Token == reqToken {
					userID = dt.UserID
					orgID = dt.OrgID
					role = dt.Role
					// Look up role from DB if not set in dev token config
					if role == "" && s.db != nil && orgID != "" && userID != "" {
						s.db.Session().Query( //nolint:errcheck
							`SELECT role FROM users WHERE org_id = ? AND user_id = ?`, orgID, userID,
						).Scan(&role)
					}
					return
				}
			}
		}
	}

	// 2. Extract token from cookie.
	token := ""
	if cookie, err := c.Cookie("sesamefs_auth"); err == nil && cookie != "" {
		if idx := strings.LastIndex(cookie, "@"); idx >= 0 && idx < len(cookie)-1 {
			token = cookie[idx+1:]
		} else {
			token = cookie
		}
	}
	if token == "" || token == "undefined" || token == "null" {
		return
	}

	// 3. Validate OIDC session.
	if s.authHandler != nil {
		if mgr := s.authHandler.GetSessionManager(); mgr != nil {
			if session, err := mgr.ValidateSession(token); err == nil {
				userID = session.UserID
				orgID = session.OrgID
				role = session.Role
				return
			}
		}
	}

	return
}

// userPlaceholder is the minified form of the placeholder in the HTML files.
// The source has: window.app.pageOptions.username=window.__SESAME_USER_PLACEHOLDER__||"";
// After build/minification the trailing semicolon may be stripped.
const userPlaceholder = `window.app.pageOptions.username=window.__SESAME_USER_PLACEHOLDER__||""`

// injectUserEmail replaces the user placeholder in HTML content with the actual email.
func injectUserEmail(html string, email string) string {
	replacement := fmt.Sprintf(`window.app.pageOptions.username=%s`, jsonQuote(email))
	return strings.Replace(html, userPlaceholder, replacement, 1)
}

// isMobileUA returns true if the User-Agent string indicates a mobile device.
func isMobileUA(userAgent string) bool {
	ua := strings.ToLower(userAgent)
	mobilePatterns := []string{"android", "iphone", "ipad", "ipod", "mobile", "tablet"}
	for _, pattern := range mobilePatterns {
		if strings.Contains(ua, pattern) {
			return true
		}
	}
	return false
}

// getViewMode checks query params and cookies for a forced view mode override.
// Returns "desktop", "mobile", or "" (no override).
func getViewMode(r *http.Request) string {
	if r.URL.Query().Get("desktop") == "1" {
		return "desktop"
	}
	if r.URL.Query().Get("mobile") == "1" {
		return "mobile"
	}
	if cookie, err := r.Cookie("view_mode"); err == nil {
		return cookie.Value
	}
	return ""
}

// authMiddleware validates authentication tokens
func (s *Server) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Helper to use anonymous access (first dev token)
		useAnonymous := func() bool {
			if s.config.Auth.AllowAnonymous && s.config.Auth.DevMode && len(s.config.Auth.DevTokens) > 0 {
				c.Set("user_id", s.config.Auth.DevTokens[0].UserID)
				c.Set("org_id", s.config.Auth.DevTokens[0].OrgID)
				c.Next()
				return true
			}
			return false
		}

		// Get token from header or cookie
		var token string
		authHeader := c.GetHeader("Authorization")
		if authHeader != "" {
			// Parse "Token <token>" format (Seafile compatible)
			if _, err := fmt.Sscanf(authHeader, "Token %s", &token); err != nil {
				// Try "Bearer <token>" format
				fmt.Sscanf(authHeader, "Bearer %s", &token) //nolint:errcheck
			}
		}

		// Fallback to sesamefs_auth cookie (used by browser navigations like export-excel)
		if token == "" {
			if cookie, err := c.Cookie("sesamefs_auth"); err == nil && cookie != "" {
				if idx := strings.LastIndex(cookie, "@"); idx >= 0 && idx < len(cookie)-1 {
					token = cookie[idx+1:]
				} else {
					token = cookie
				}
			}
		}

		if token == "" {
			if useAnonymous() {
				return
			}
			c.JSON(http.StatusUnauthorized, gin.H{"error": "missing authorization header"})
			c.Abort()
			return
		}

		// Check for empty/invalid token values
		if token == "" || token == "undefined" || token == "null" {
			if useAnonymous() {
				return
			}
			c.JSON(http.StatusUnauthorized, gin.H{"error": "empty or invalid token"})
			c.Abort()
			return
		}

		// In dev mode, check dev tokens first
		if s.config.Auth.DevMode {
			for _, devToken := range s.config.Auth.DevTokens {
				if devToken.Token == token {
					c.Set("user_id", devToken.UserID)
					c.Set("org_id", devToken.OrgID)
					if devToken.Role != "" {
						c.Set("role", devToken.Role)
					}
					c.Next()
					return
				}
			}
		}

		// Try to validate as OIDC session token
		if s.authHandler != nil {
			sessionMgr := s.authHandler.GetSessionManager()
			if sessionMgr != nil {
				session, err := sessionMgr.ValidateSession(token)
				if err == nil {
					c.Set("user_id", session.UserID)
					c.Set("org_id", session.OrgID)
					c.Set("email", session.Email)
					c.Set("role", session.Role)
					c.Next()
					return
				}
				// If the session was found but expired, return immediately
				// with a specific error so the frontend can redirect to login.
				if strings.Contains(err.Error(), "expired") {
					c.JSON(http.StatusUnauthorized, gin.H{"error": "session expired"})
					c.Abort()
					return
				}
			}
		}

		// Try to validate as a repo API token (library-scoped access)
		if s.db != nil {
			var repoID, permission, generatedBy string
			err := s.db.Session().Query(`
				SELECT repo_id, permission, generated_by FROM repo_api_tokens_by_token WHERE api_token = ?
			`, token).Scan(&repoID, &permission, &generatedBy)
			if err == nil {
				// Repo API token found — look up the library's org_id
				var orgID string
				if err := s.db.Session().Query(`
					SELECT org_id FROM libraries_by_id WHERE library_id = ?
				`, repoID).Scan(&orgID); err == nil {
					c.Set("user_id", generatedBy)
					c.Set("org_id", orgID)
					c.Set("repo_api_token", true)
					c.Set("repo_api_token_repo_id", repoID)
					c.Set("repo_api_token_permission", permission)
					c.Next()
					return
				}
			}
		}

		// Token not found - try anonymous fallback before rejecting
		if useAnonymous() {
			return
		}

		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
		c.Abort()
	}
}

// smartLinkAuthMiddleware is like authMiddleware but redirects unauthenticated
// browser requests to the login page with a ?next= parameter instead of
// returning a JSON 401 error. This ensures users who follow a smart link
// while logged-out land on the login page and are sent back afterward.
func (s *Server) smartLinkAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Build the redirect-to-login URL using the original request path.
		redirectToLogin := func(expired bool) {
			next := c.Request.URL.RequestURI()
			loginURL := "/login/?next=" + next
			if expired {
				loginURL += "&expired=1"
			}
			c.Redirect(http.StatusFound, loginURL)
			c.Abort()
		}

		// Anonymous dev-mode shortcut (mirrors authMiddleware)
		useAnonymous := func() bool {
			if s.config.Auth.AllowAnonymous && s.config.Auth.DevMode && len(s.config.Auth.DevTokens) > 0 {
				c.Set("user_id", s.config.Auth.DevTokens[0].UserID)
				c.Set("org_id", s.config.Auth.DevTokens[0].OrgID)
				c.Next()
				return true
			}
			return false
		}

		// Extract token from Authorization header or sesamefs_auth cookie.
		var token string
		authHeader := c.GetHeader("Authorization")
		if authHeader != "" {
			if _, err := fmt.Sscanf(authHeader, "Token %s", &token); err != nil {
				fmt.Sscanf(authHeader, "Bearer %s", &token) //nolint:errcheck
			}
		}
		if token == "" {
			if cookie, err := c.Cookie("sesamefs_auth"); err == nil && cookie != "" {
				if idx := strings.LastIndex(cookie, "@"); idx >= 0 && idx < len(cookie)-1 {
					token = cookie[idx+1:]
				} else {
					token = cookie
				}
			}
		}

		if token == "" || token == "undefined" || token == "null" {
			if useAnonymous() {
				return
			}
			redirectToLogin(false)
			return
		}

		// Dev-mode tokens.
		if s.config.Auth.DevMode {
			for _, devToken := range s.config.Auth.DevTokens {
				if devToken.Token == token {
					c.Set("user_id", devToken.UserID)
					c.Set("org_id", devToken.OrgID)
					if devToken.Role != "" {
						c.Set("role", devToken.Role)
					}
					c.Next()
					return
				}
			}
		}

		// OIDC session token.
		if s.authHandler != nil {
			sessionMgr := s.authHandler.GetSessionManager()
			if sessionMgr != nil {
				session, err := sessionMgr.ValidateSession(token)
				if err == nil {
					c.Set("user_id", session.UserID)
					c.Set("org_id", session.OrgID)
					c.Set("email", session.Email)
					c.Set("role", session.Role)
					c.Next()
					return
				}
				if strings.Contains(err.Error(), "expired") {
					redirectToLogin(true)
					return
				}
			}
		}

		// Repo API token.
		if s.db != nil {
			var repoID, permission, generatedBy string
			err := s.db.Session().Query(`
				SELECT repo_id, permission, generated_by FROM repo_api_tokens_by_token WHERE api_token = ?
			`, token).Scan(&repoID, &permission, &generatedBy)
			if err == nil {
				var orgID string
				if err := s.db.Session().Query(`
					SELECT org_id FROM libraries_by_id WHERE library_id = ?
				`, repoID).Scan(&orgID); err == nil {
					c.Set("user_id", generatedBy)
					c.Set("org_id", orgID)
					c.Set("repo_api_token", true)
					c.Set("repo_api_token_repo_id", repoID)
					c.Set("repo_api_token_permission", permission)
					c.Next()
					return
				}
			}
		}

		if useAnonymous() {
			return
		}
		redirectToLogin(false)
	}
}

// syncAuthMiddleware validates authentication for sync protocol endpoints
// It accepts multiple auth methods:
// 1. Seafile-Repo-Token header (repo-specific token from download-info)
// 2. Authorization: Token header (standard API token)
// 3. token query parameter
func (s *Server) syncAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Helper to use anonymous access (first dev token) — mirrors authMiddleware behavior
		useAnonymous := func() bool {
			if s.config.Auth.AllowAnonymous && s.config.Auth.DevMode && len(s.config.Auth.DevTokens) > 0 {
				c.Set("user_id", s.config.Auth.DevTokens[0].UserID)
				c.Set("org_id", s.config.Auth.DevTokens[0].OrgID)
				c.Next()
				return true
			}
			return false
		}

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

		// Try query parameter
		if token == "" {
			token = c.Query("token")
		}

		// Try form body parameter (SeaDrive sends token in POST body for some endpoints)
		if token == "" {
			token = c.PostForm("token")
		}

		// No token provided — try anonymous fallback, otherwise reject
		if token == "" {
			if useAnonymous() {
				return
			}
			c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			c.Abort()
			return
		}

		// Check if it's a dev token (only in dev mode)
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
		if accessToken, valid := s.tokenStore.GetToken(token, TokenTypeDownload); valid {
			c.Set("user_id", accessToken.UserID)
			c.Set("org_id", accessToken.OrgID)
			c.Set("repo_id", accessToken.RepoID)
			c.Next()
			return
		}

		// Try to validate as OIDC session token (SSO login)
		if s.authHandler != nil {
			sessionMgr := s.authHandler.GetSessionManager()
			if sessionMgr != nil {
				session, err := sessionMgr.ValidateSession(token)
				if err == nil {
					c.Set("user_id", session.UserID)
					c.Set("org_id", session.OrgID)
					c.Set("email", session.Email)
					c.Set("role", session.Role)
					c.Next()
					return
				}
			}
		}

		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
		c.Abort()
	}
}

// handlePing returns a simple pong response
func (s *Server) handlePing(c *gin.Context) {
	c.String(http.StatusOK, "pong")
}

// handleDefaultRepo returns the user's default library.
// GET /api2/default-repo/
// SeaDrive calls this to find "My Library". Since we don't auto-create one,
// return an empty string to indicate no default repo exists.
func (s *Server) handleDefaultRepo(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"exists": false, "repo_id": ""})
}

// handleNotImplemented returns a 501 Not Implemented response
func (s *Server) handleNotImplemented(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"error": "not implemented yet"})
}

// getEffectiveHostname delegates to httputil.GetEffectiveHostname.
func getEffectiveHostname(c *gin.Context) string {
	return httputil.GetEffectiveHostname(c)
}

// getBaseURLFromRequest delegates to httputil.GetBaseURLFromRequest.
func getBaseURLFromRequest(c *gin.Context) string {
	return httputil.GetBaseURLFromRequest(c)
}

// getRelayPortFromRequest delegates to httputil.GetRelayPortFromRequest.
func getRelayPortFromRequest(c *gin.Context) string {
	return httputil.GetRelayPortFromRequest(c)
}

// handleAuthToken handles the Seafile CLI auth-token endpoint
// POST /api2/auth-token/ with username and password
func (s *Server) handleAuthToken(c *gin.Context) {
	var username, password string

	// Support both form-encoded and JSON request bodies
	contentType := c.ContentType()
	if strings.Contains(contentType, "application/json") {
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := c.ShouldBindJSON(&req); err == nil {
			username = req.Username
			password = req.Password
		}
	} else {
		username = c.PostForm("username")
		password = c.PostForm("password")
	}

	// Trim whitespace/newlines - Seafile client may append trailing newline
	username = strings.TrimSpace(username)
	password = strings.TrimSpace(password)

	if username == "" || password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "username and password required"})
		return
	}

	// In dev mode, check dev tokens by matching username
	if s.config.Auth.DevMode {
		for _, devToken := range s.config.Auth.DevTokens {
			// In dev mode, accept multiple username formats:
			// 1. UUID directly (e.g., "00000000-0000-0000-0000-000000000001")
			// 2. UUID@sesamefs.local (e.g., "00000000-0000-0000-0000-000000000001@sesamefs.local")
			// 3. Friendly email matching this specific devToken (e.g., "admin@sesamefs.local" only for admin token)
			// 4. Token as password (Seafile CLI compatibility)

			expectedEmail := devToken.UserID + "@sesamefs.local"

			// Check if username matches THIS specific devToken
			// Note: devToken.Email is the friendly email like "admin@sesamefs.local"
			usernameMatches := (devToken.UserID == username ||
				expectedEmail == username ||
				(devToken.Email != "" && devToken.Email == username))

			if usernameMatches || devToken.Token == password {
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
	features := []string{"seafile-basic", "seafile-pro", "file-search"}

	// client-sso-via-local-browser tells the Seafile desktop client to open the
	// system browser for SSO and wait for the seafile://client-login/?token=xxx
	// redirect, instead of falling back to the legacy /shib-login Shibboleth flow.
	if s.authHandler != nil && s.authHandler.GetOIDCClient().IsEnabled() {
		features = append(features, "client-sso-via-local-browser")
	}

	// Brand name — override with DESKTOP_CUSTOM_BRAND env var in production
	brand := os.Getenv("DESKTOP_CUSTOM_BRAND")
	if brand == "" {
		brand = "Sesame Disk"
	}

	info := gin.H{
		"version":                              "11.0.0",
		"encrypted_library_version":            2,
		"enable_encrypted_library":             true,
		"enable_repo_history_setting":          true,
		"enable_reset_encrypted_repo_password": false,
		"features":                             features,
		"desktop-custom-brand":                 brand,
	}

	// file_server_root tells the desktop client/SeaDrive where the fileserver (seafhttp)
	// is located. Derived from the request host so it works in multi-tenant setups.
	info["file_server_root"] = getBaseURLFromRequest(c) + "/seafhttp"

	// Logo URL — optional, set via DESKTOP_CUSTOM_LOGO env var
	if logo := os.Getenv("DESKTOP_CUSTOM_LOGO"); logo != "" {
		info["desktop-custom-logo"] = logo
	}

	c.JSON(http.StatusOK, info)
}

// handleClientLogin generates a one-time login token for desktop client auto-login
// POST /api2/client-login/
func (s *Server) handleClientLogin(c *gin.Context) {
	// User must be authenticated
	token := c.GetHeader("Authorization")
	if token == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	// Remove "Token " prefix if present
	token = strings.TrimPrefix(token, "Token ")
	token = strings.TrimSpace(token)

	// Validate the token and get user info
	var userID, orgID string

	// In dev mode, check dev tokens
	if s.config.Auth.DevMode {
		for _, devToken := range s.config.Auth.DevTokens {
			if devToken.Token == token {
				userID = devToken.UserID
				orgID = devToken.OrgID
				break
			}
		}
	}

	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
		return
	}

	// Generate one-time login token
	oneTimeToken, err := s.tokenStore.CreateOneTimeLoginToken(userID, orgID, token)
	if err != nil {
		slog.Error("Failed to create one-time login token", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create login token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"token": oneTimeToken,
	})
}

// handleClientSSOLink creates a pending SSO token for the Seafile desktop client.
// POST /api2/client-sso-link
//
// Flow (matches seahub ClientSSOLink):
//  1. Desktop client calls POST → receives {"link": "https://server/client-sso/T/"}
//  2. Desktop client opens that link in the system browser
//  3. User authenticates via OIDC; callback marks T as success with the API token
//  4. Desktop client polls GET /api2/client-sso-link/T until status=="success"
//  5. Client extracts apiToken from the response
func (s *Server) handleClientSSOLink(c *gin.Context) {
	if s.authHandler == nil || !s.authHandler.GetOIDCClient().IsEnabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "SSO is not enabled"})
		return
	}

	// Create a pending SSO token that the client will poll for
	pendingToken, err := s.ssoStore.create()
	if err != nil {
		slog.Error("Failed to create SSO pending token", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create SSO link"})
		return
	}

	// Build base URL (respects SERVER_URL env var for reverse proxy)
	var baseURL string
	if serverURL := os.Getenv("SERVER_URL"); serverURL != "" {
		baseURL = strings.TrimSuffix(serverURL, "/")
	} else {
		scheme := "https"
		host := c.Request.Host
		if proto := c.GetHeader("X-Forwarded-Proto"); proto != "" {
			scheme = proto
		} else if c.Request.TLS == nil && (strings.HasPrefix(host, "localhost") || strings.HasPrefix(host, "127.0.0.1")) {
			scheme = "http"
		}
		baseURL = scheme + "://" + host
	}

	// Use /client-sso/TOKEN/ path — matches seahub's reverse('client_sso', args=[token]).
	// Seafile desktop clients parse the pending token from the last path segment.
	loginURL := baseURL + "/client-sso/" + pendingToken + "/"

	c.JSON(http.StatusOK, gin.H{
		"link": loginURL,
	})
}

// handleGetClientSSOLink polls the status of a pending desktop-client SSO login.
// GET /api2/client-sso-link/:token   (token as path segment)
// GET /api2/client-sso-link/?token=T (token as query param, may have trailing slash)
//
// The Seafile desktop client (seafile-client, seadrive-gui) calls this repeatedly
// after opening the browser until it gets status=="success", then uses apiToken
// for all subsequent API calls.
//
// Response format matches what the Seafile desktop client actually parses
// (see seafile-client src/api/requests.cpp — ClientSSOStatusRequest::requestSuccess):
//
//	Pending: {"status": "waiting"}
//	Success: {"status": "success", "username": "user@example.com", "apiToken": "<token>"}
//
// The client checks dict["status"], dict["username"], dict["apiToken"] (camelCase).
func (s *Server) handleGetClientSSOLink(c *gin.Context) {
	// Token may come as a path param (:token) or query param (?token=T/).
	// Seafile desktop client v9+ uses path segment: /api2/client-sso-link/TOKEN/
	token := c.Param("token")
	if token == "" {
		token = c.Query("token")
	}
	// Strip any trailing slash the client appends to the value.
	token = strings.TrimSuffix(token, "/")
	if token == "" {
		c.JSON(http.StatusOK, gin.H{"status": "waiting"})
		return
	}
	entry := s.ssoStore.get(token)
	if entry == nil {
		// Token not found or expired — return waiting so client keeps polling
		// (or times out on its own)
		c.JSON(http.StatusOK, gin.H{"status": "waiting"})
		return
	}
	if entry.status == "success" {
		c.JSON(http.StatusOK, gin.H{
			"status":   "success",
			"username": entry.email,
			"apiToken": entry.apiToken,
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "waiting"})
}

// handleAutoLogin handles the browser auto-login flow
// GET /client-login/?token=xxx&next=/library/...
func (s *Server) handleAutoLogin(c *gin.Context) {
	oneTimeToken := c.Query("token")
	nextURL := c.Query("next")

	if oneTimeToken == "" {
		c.Redirect(http.StatusFound, "/login/?error=missing_token")
		return
	}

	// Validate and consume the one-time token
	authToken, err := s.tokenStore.ConsumeOneTimeLoginToken(oneTimeToken)
	if err != nil {
		slog.Warn("Invalid or expired one-time token", "error", err)
		c.Redirect(http.StatusFound, "/login/?error=invalid_token")
		return
	}

	// Set the auth token as a cookie for the browser session
	c.SetCookie(
		"sesamefs_auth", // name
		authToken,       // value
		3600*24*7,       // maxAge (7 days)
		"/",             // path
		"",              // domain (empty = current domain)
		false,           // secure (false for localhost)
		true,            // httpOnly
	)

	// Redirect to the requested page or default to home
	if nextURL == "" {
		nextURL = "/"
	}
	c.Redirect(http.StatusFound, nextURL)
}

// handleAccountInfo returns account information for the authenticated user
// GET /api2/account/info/
func (s *Server) handleAccountInfo(c *gin.Context) {
	userID := c.GetString("user_id")
	orgID := c.GetString("org_id")

	// Fetch actual user data from database
	var email, name, role string
	var quotaBytes, usedBytes int64
	err := s.db.Session().Query(`
		SELECT email, name, role, quota_bytes, used_bytes
		FROM users WHERE org_id = ? AND user_id = ?
	`, orgID, userID).Scan(&email, &name, &role, &quotaBytes, &usedBytes)

	if err != nil {
		// Fallback to defaults if user not found (shouldn't happen for authenticated user)
		email = userID + "@sesamefs.local"
		name = userID
		role = "user"
		quotaBytes = -2 // unlimited
		usedBytes = 0
	}

	// Use email username as display name if name is empty
	if name == "" {
		if atIdx := strings.Index(email, "@"); atIdx > 0 {
			name = email[:atIdx]
		} else {
			name = email
		}
	}

	// Determine permissions based on role
	// Roles: superadmin, admin, user, readonly, guest
	isStaff := role == "superadmin"
	canAddRepo := role == "superadmin" || role == "admin" || role == "user"
	canShareRepo := role == "superadmin" || role == "admin" || role == "user"
	canAddGroup := role == "superadmin" || role == "admin" || role == "user"
	canGenerateShareLink := role == "superadmin" || role == "admin" || role == "user"
	canGenerateUploadLink := role == "superadmin" || role == "admin" || role == "user"

	// Calculate space usage
	spaceUsage := "0%"
	if quotaBytes > 0 && usedBytes > 0 {
		percentage := float64(usedBytes) / float64(quotaBytes) * 100
		spaceUsage = fmt.Sprintf("%.1f%%", percentage)
	}

	// Return basic account info matching stock Seafile format
	// CRITICAL: Field names and types must match exactly for desktop client compatibility
	// Verified against stock Seafile (app.nihaoconsult.com)
	c.JSON(http.StatusOK, gin.H{
		"email":         email,
		"name":          name,
		"login_id":      email,
		"contact_email": email,
		"department":    "",
		"institution":   orgID,
		"is_staff":      isStaff,
		"is_org_staff": func() int {
			if role == "admin" || role == "superadmin" {
				return 1
			}
			return 0
		}(),
		"usage":                       usedBytes,
		"total":                       quotaBytes,
		"space_usage":                 spaceUsage,
		"avatar_url":                  getBaseURLFromRequest(c) + "/media/avatars/default.png",
		"enable_subscription":         false,
		"file_updates_email_interval": 0,
		"collaborate_email_interval":  0,
		// SesameFS extensions for permission control
		"role":                     role,
		"can_add_repo":             canAddRepo,
		"can_share_repo":           canShareRepo,
		"can_add_group":            canAddGroup,
		"can_generate_share_link":  canGenerateShareLink,
		"can_generate_upload_link": canGenerateUploadLink,
	})
}

// handleSearchUser searches for users within the same organization
// GET /api2/search-user/?q=<query>
// Returns users matching the query string (by email or name)
func (s *Server) handleSearchUser(c *gin.Context) {
	query := c.Query("q")
	orgID := c.GetString("org_id")

	if query == "" {
		c.JSON(http.StatusOK, gin.H{"users": []gin.H{}})
		return
	}

	// Query all users in the organization
	iter := s.db.Session().Query(`
		SELECT user_id, email, name, role FROM users WHERE org_id = ?
	`, orgID).Iter()

	var users []gin.H
	var userID, email, name, role string
	queryLower := strings.ToLower(query)

	for iter.Scan(&userID, &email, &name, &role) {
		// Skip deactivated users
		if role == "deactivated" {
			continue
		}
		// Match against email or name (case-insensitive)
		if strings.Contains(strings.ToLower(email), queryLower) ||
			strings.Contains(strings.ToLower(name), queryLower) {
			displayName := name
			if displayName == "" {
				if atIdx := strings.Index(email, "@"); atIdx > 0 {
					displayName = email[:atIdx]
				} else {
					displayName = email
				}
			}
			users = append(users, gin.H{
				"email":         email,
				"name":          displayName,
				"avatar_url":    getBaseURLFromRequest(c) + "/media/avatars/default.png",
				"contact_email": email,
				"login_id":      email,
			})
		}
	}
	if err := iter.Close(); err != nil {
		slog.Error("search-user query failed", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "search failed"})
		return
	}

	if users == nil {
		users = []gin.H{}
	}
	c.JSON(http.StatusOK, gin.H{"users": users})
}

// handleUserAvatar returns an avatar for a user
// GET /api2/avatars/user/:email/resized/:size/
func (s *Server) handleUserAvatar(c *gin.Context) {
	// Return a default avatar URL
	// In production, this would return actual user avatars
	c.JSON(http.StatusOK, gin.H{
		"url":        "",
		"is_default": true,
		"mtime":      0,
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
		Addr:              s.config.Server.Port,
		Handler:           handler,
		ReadTimeout:       s.config.Server.ReadTimeout,
		ReadHeaderTimeout: s.config.Server.ReadHeaderTimeout,
		WriteTimeout:      s.config.Server.WriteTimeout,
	}

	return s.server.ListenAndServe()
}

// Shutdown gracefully shuts down the server
func (s *Server) Shutdown(ctx context.Context) error {
	// Stop GC service first
	if s.gcService != nil {
		s.gcService.Stop()
	}

	if s.server != nil {
		return s.server.Shutdown(ctx)
	}
	return nil
}

// handleEmptyActivities returns empty activities list (stub)
// GET /api/v2.1/activities/
func (s *Server) handleEmptyActivities(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"events": []interface{}{},
	})
}

// handleEmptyNotifications returns empty notifications list
// GET /api/v2.1/notifications/
func (s *Server) handleEmptyNotifications(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"notification_list": []interface{}{},
		"unseen_count":      0,
	})
}

// handleEmptySharedRepos returns empty shared repos list (stub)
// GET /api/v2.1/shared-repos/
func (s *Server) handleEmptySharedRepos(c *gin.Context) {
	c.JSON(http.StatusOK, []interface{}{})
}

// handleEmptySharedFolders returns empty shared folders list (stub)
// GET /api/v2.1/shared-folders/
func (s *Server) handleEmptySharedFolders(c *gin.Context) {
	c.JSON(http.StatusOK, []interface{}{})
}

// handleEmptyDevices returns empty devices list (stub)
// GET /api2/devices/
func (s *Server) handleEmptyDevices(c *gin.Context) {
	c.JSON(http.StatusOK, []interface{}{})
}

// handleEmptyWikis returns empty published libraries/wikis list (stub)
// GET /api/v2.1/wikis/
func (s *Server) handleEmptyWikis(c *gin.Context) {
	c.JSON(http.StatusOK, []interface{}{})
}

// handleEmptyRepoTags returns empty repo tags list
// GET /api/v2.1/repos/:repo_id/repo-tags/
func (s *Server) handleEmptyRepoTags(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"repo_tags": []interface{}{},
	})
}

// handleEmptyFolderShareInfo returns empty folder share info
// GET /api/v2.1/repo-folder-share-info/
func (s *Server) handleEmptyFolderShareInfo(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"share_info_list": []interface{}{},
	})
}

// handleEmptyGroups returns empty groups list
// GET /api/v2.1/groups/
func (s *Server) handleEmptyGroups(c *gin.Context) {
	c.JSON(http.StatusOK, []interface{}{})
}

// handleAutoDeleteSettings and handleEmptyRepoAPITokens removed -
// replaced by LibrarySettingsHandler in v2/library_settings.go

// handleEmptyRepoShareLinks removed - replaced by ShareLinkHandler.ListRepoShareLinks

// handleHistoryLimit removed - replaced by LibrarySettingsHandler in v2/library_settings.go

// buildOAuthCallbackURI constructs the server-side OAuth callback URI, respecting
// the SERVER_URL env var when running behind a reverse proxy.
func (s *Server) buildOAuthCallbackURI(c *gin.Context) string {
	if serverURL := os.Getenv("SERVER_URL"); serverURL != "" {
		return strings.TrimSuffix(serverURL, "/") + "/oauth/callback/"
	}
	scheme := "https"
	host := c.Request.Host
	if proto := c.GetHeader("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	} else if c.Request.TLS == nil && (strings.HasPrefix(host, "localhost") || strings.HasPrefix(host, "127.0.0.1")) {
		scheme = "http"
	}
	return scheme + "://" + host + "/oauth/callback/"
}

// handleOAuthLogin initiates the OIDC SSO flow for the Seafile desktop client.
// GET /oauth/login/
// GET /client-sso/:token/
// The Seafile desktop client opens this URL in a browser. The server redirects
// to the OIDC provider; after authentication the user ends up at /oauth/callback/.
func (s *Server) handleOAuthLogin(c *gin.Context) {
	if s.authHandler == nil || !s.authHandler.GetOIDCClient().IsEnabled() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "OIDC authentication is not enabled"})
		return
	}

	callbackURI := s.buildOAuthCallbackURI(c)

	// Carry the pending SSO token through the OIDC state so the callback can
	// mark it as success once the user authenticates.
	// The POST /api2/client-sso-link returns a link like /client-sso/TOKEN/
	// (matches seahub's reverse('client_sso', args=[token])), so the token
	// arrives as a path segment. Fall back to query param for compatibility.
	returnURL := "seafile://client-login/"
	pendingToken := c.Param("token")
	if pendingToken == "" {
		pendingToken = c.Query("token")
	}
	if pendingToken != "" {
		returnURL = "seafile://client-login/?token=" + url.QueryEscape(pendingToken)
	}

	authURL, err := s.authHandler.GetOIDCClient().GetAuthorizationURL(
		c.Request.Context(),
		callbackURI,
		returnURL,
	)
	if err != nil {
		slog.Error("Failed to generate OAuth login URL", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate login URL"})
		return
	}

	c.Redirect(http.StatusFound, authURL)
}

// handleOAuthCallback handles the OIDC callback for the Seafile desktop client.
// GET /oauth/callback/?code=xxx&state=yyy
// Exchanges the authorization code for a session token and redirects to
// seafile://client-login/?token=xxx so the desktop client can capture it.
func (s *Server) handleOAuthCallback(c *gin.Context) {
	errParam := c.Query("error")
	if errParam != "" {
		slog.Warn("OIDC provider returned error during desktop SSO", "error", errParam)
		c.Redirect(http.StatusFound, "/login/?error="+url.QueryEscape(errParam))
		return
	}

	code := c.Query("code")
	state := c.Query("state")
	if code == "" || state == "" {
		c.Redirect(http.StatusFound, "/login/?error=missing_params")
		return
	}

	if s.authHandler == nil || !s.authHandler.GetOIDCClient().IsEnabled() {
		c.Redirect(http.StatusFound, "/login/?error=oidc_disabled")
		return
	}

	callbackURI := s.buildOAuthCallbackURI(c)

	result, err := s.authHandler.GetOIDCClient().ExchangeCode(
		c.Request.Context(),
		code, state, callbackURI,
	)
	if err != nil {
		slog.Error("OAuth code exchange failed during desktop SSO", "error", err)
		c.Redirect(http.StatusFound, "/login/?error=auth_failed")
		return
	}

	// If the login was initiated via POST /api2/client-sso-link, the pending
	// token is encoded in the ReturnURL as ?token=<T>. Mark it as success so
	// the polling client can pick up the API token.
	if result.ReturnURL != "" {
		if returnU, parseErr := url.Parse(result.ReturnURL); parseErr == nil {
			if pendingToken := returnU.Query().Get("token"); pendingToken != "" {
				s.ssoStore.markSuccess(pendingToken, result.SessionToken, result.Email)
				slog.Info("Desktop SSO token marked as success", "sso_token_prefix", pendingToken[:min(8, len(pendingToken))])
			}
		}
	}

	// Set sesamefs_auth cookie (email@token) — matches seahub convention.
	// httpOnly=false is intentional: the embedded WebView needs to read this via JS.
	// Cookie TTL matches session TTL so both expire at the same time.
	seahubAuth := result.Email + "@" + result.SessionToken
	isSecure := c.Request.TLS != nil
	cookieMaxAge := int(s.config.Auth.OIDC.SessionTTL.Seconds())
	c.SetCookie("sesamefs_auth", seahubAuth, cookieMaxAge, "/", "", isSecure, false)

	// If this was a desktop client SSO login (returnURL starts with seafile://),
	// show a confirmation page instead of redirecting to the web app home page.
	// The desktop client receives the API token via polling, so the browser tab
	// is no longer needed. We attempt window.close() and show a message.
	if strings.HasPrefix(result.ReturnURL, "seafile://") {
		data := templates.LoginSuccessData{ReturnURL: result.ReturnURL}
		c.Header("Content-Type", "text/html; charset=utf-8")
		if err := templates.Render(c.Writer, "login_success.html", data); err != nil {
			log.Printf("[handleSSOCallback] template error: %v", err)
			c.String(http.StatusInternalServerError, "Internal Server Error")
		}
		return
	}

	// Web browser login — redirect to home page.
	c.Redirect(http.StatusFound, "/")
}

// handleLogout clears the user's session and redirects to home
// GET /accounts/logout/
func (s *Server) handleLogout(c *gin.Context) {
	// In a real implementation, we would invalidate the token in the database
	// For now, we just return an HTML page that clears localStorage and redirects
	c.Header("Content-Type", "text/html; charset=utf-8")
	if err := templates.Render(c.Writer, "logout.html", nil); err != nil {
		log.Printf("[handleLogout] template error: %v", err)
		c.String(http.StatusInternalServerError, "Internal Server Error")
	}
}

// handleCreateRepoTag returns a stub response for tag creation
// POST /api/v2.1/repos/:repo_id/repo-tags/
func (s *Server) handleCreateRepoTag(c *gin.Context) {
	// Return stub success - full implementation would create tag in database
	c.JSON(http.StatusOK, gin.H{
		"repo_tag": gin.H{
			"id":    1,
			"name":  c.PostForm("name"),
			"color": c.PostForm("color"),
		},
	})
}

// handleGCStatus returns the current GC status
// GET /api/v2.1/admin/gc/status
func (s *Server) handleGCStatus(c *gin.Context) {
	// Check staff permission
	role := c.GetString("role")
	if role != "admin" && role != "superadmin" {
		c.JSON(http.StatusForbidden, gin.H{"error": "admin access required"})
		return
	}

	if s.gcService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "GC service not available"})
		return
	}

	c.JSON(http.StatusOK, s.gcService.Status())
}

// handleGCRun triggers a manual GC run
// POST /api/v2.1/admin/gc/run
func (s *Server) handleGCRun(c *gin.Context) {
	// Check staff permission
	role := c.GetString("role")
	if role != "admin" && role != "superadmin" {
		c.JSON(http.StatusForbidden, gin.H{"error": "admin access required"})
		return
	}

	if s.gcService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "GC service not available"})
		return
	}

	var req struct {
		Type   string `json:"type"`    // "worker" or "scanner"
		DryRun *bool  `json:"dry_run"` // optional override
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		// Default to worker
		req.Type = "worker"
	}

	if req.DryRun != nil {
		s.gcService.SetDryRun(*req.DryRun)
	}

	switch req.Type {
	case "scanner":
		s.gcService.TriggerScanner()
		c.JSON(http.StatusOK, gin.H{"started": true, "message": "GC scanner triggered"})
	default:
		s.gcService.TriggerWorker()
		c.JSON(http.StatusOK, gin.H{"started": true, "message": "GC worker triggered"})
	}
}
