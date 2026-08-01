package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	v2 "github.com/Sesame-Disk/sesamefs/internal/api/v2"
	"github.com/Sesame-Disk/sesamefs/internal/apikeys"
	authpkg "github.com/Sesame-Disk/sesamefs/internal/auth"
	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/gc"
	"github.com/Sesame-Disk/sesamefs/internal/httputil"
	"github.com/Sesame-Disk/sesamefs/internal/logging"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	"github.com/Sesame-Disk/sesamefs/internal/plans"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/Sesame-Disk/sesamefs/internal/templates"
	"github.com/Sesame-Disk/sesamefs/internal/traffic"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-contrib/cors"
	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
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

func gzipExcludedPathsRegexs(metricsPath string) []string {
	return []string{
		"^" + regexp.QuoteMeta(metricsPath) + "$", // promhttp handles compression itself
		// Sync block PUT/GET. Blocks are opaque binary and a PUT's response is a
		// few bytes, so there is nothing to gain here — and something to lose:
		// gin-contrib/gzip replaces c.Writer with a gzipWriter that embeds the
		// gin.ResponseWriter *interface*, which does not declare Unwrap(). The
		// wrapper therefore exposes neither SetReadDeadline nor Unwrap, so
		// http.NewResponseController cannot reach the connection and PutBlock's
		// admitted-lifetime read deadline silently degrades to the body-close
		// fallback that cannot interrupt a parked read. A client sending
		// Accept-Encoding: gzip could then hold an admission open indefinitely.
		"/seafhttp/repo/.*/block/.*",
		// check-blocks, for the same reason (subcontract C / X11). Its admitted
		// lifetime is what makes an admission recoverable and what stops the
		// metadata work when a client goes away, and that only holds if the
		// deadline reaches the socket. With the route inside gzip it cannot, and
		// the fail-closed path answers every request by dropping the connection —
		// which is how an integration test against the real stack caught this, and
		// a unit test against a plain router never could.
		//
		// The response is a JSON array of ids that would compress well, so this
		// exclusion does cost something. It is still the right trade: an
		// unbounded, uncancellable admission is a availability defect, and a
		// compressed response is a bandwidth optimisation.
		"/seafhttp/repo/.*/check-blocks.*",
		"/seafhttp/files/.*",     // file downloads
		"/seafhttp/zip/.*",       // zip downloads
		"/seafhttp/upload/.*",    // file uploads
		"/api/v2.1/.*raw/.*",     // raw file serving (inline preview)
		"/api/v2.1/.*history/.*", // historic file downloads
	}
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
	config               *config.Config
	db                   *db.DB
	storage              *storage.S3Store // Legacy single S3 store
	storageManager       *storage.Manager // Multi-backend storage manager
	tokenStore           TokenStore
	permMiddleware       *middleware.PermissionMiddleware
	authHandler          *v2.AuthHandler         // OIDC authentication handler
	gcService            *gc.Service             // Garbage collection service
	ssoStore             *clientSSOStore         // Pending desktop-client SSO tokens
	authRateLimiter      *middleware.RateLimiter // Per-IP rate limiter for auth endpoints
	shareLinkRateLimiter *middleware.RateLimiter // Per-IP rate limiter for public share-link endpoints
	zipRateLimiter       *middleware.RateLimiter // Per-IP rate limiter for streamed ZIP downloads
	seafHTTPHandler      *SeafHTTPHandler        // Held only so Shutdown can stop the limiters it owns
	apiKeyManager        *apikeys.Manager        // API key manager for programmatic auth
	version              string                  // Build version string
	router               *gin.Engine
	server               *http.Server
}

var errLegacyS3NotConfigured = errors.New("legacy S3 storage not configured")

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
	if err := configureTrustedProxies(router, cfg); err != nil {
		panic(fmt.Errorf("configure trusted proxies: %w", err))
	}

	router.Use(gin.Recovery())
	router.Use(middleware.SecurityHeaders())
	router.Use(logging.GinMiddleware())
	// Gzip for API responses but exclude binary file transfer paths.
	// These paths stream large files and gzip would buffer them entirely,
	// waste CPU on already-compressed content, and break streaming.
	router.Use(gzip.Gzip(gzip.DefaultCompression,
		gzip.WithExcludedPathsRegexs(gzipExcludedPathsRegexs(cfg.Monitoring.MetricsPath)),
	))

	// Register Prometheus metrics and add metrics middleware
	if cfg.Monitoring.MetricsEnabled {
		metrics.Register()
		router.Use(metrics.GinMiddleware())
	}

	// CORS middleware for frontend access
	router.Use(cors.New(buildCORSConfig(cfg)))

	// Initialize storage manager with multi-backend support
	storageManager := initStorageManager(cfg)

	// Initialize legacy S3 storage (for backward compatibility)
	s3Store, err := initS3Storage(cfg)
	if err != nil {
		if !errors.Is(err, errLegacyS3NotConfigured) {
			slog.Warn("Failed to initialize legacy S3 storage", "error", err)
		}
		// Continue without the singleton S3 store; multi-backend flows use storageManager.
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
		gcService = gc.NewService(store, storageProvider, cfg.GC, database.Session())
		gcService.SetOnlyOfficeReconciler(newOnlyOfficeReconcilerAdapter(database))
	}

	// Initialize rate limiter for auth endpoints (~10 req/min per IP)
	authRL := middleware.NewRateLimiter(rate.Every(6*time.Second), 10)

	// Share-link endpoints are public (no auth) and keyed only by an
	// opaque token. Without a limiter here an attacker can enumerate
	// valid tokens by brute force (H-5). Allow a small burst so legit
	// page loads with parallel bootstrap/dirents calls don't trip it,
	// then steady ~1 req/sec per IP.
	shareLinkRL := middleware.NewRateLimiter(rate.Every(time.Second), 20)

	// ZIP creation is CPU/IO-heavy even with a valid token. Keep this
	// strictly per-IP for now to suppress repeated abuse bursts.
	zipRL := middleware.NewRateLimiter(rate.Every(15*time.Second), 3)

	var apiKeyManager *apikeys.Manager
	if database != nil {
		apiKeyManager = apikeys.NewManager(database)
	}
	v2.StartPublishedBlockReferenceRepairer(database)

	s := &Server{
		config:               cfg,
		db:                   database,
		storage:              s3Store,
		storageManager:       storageManager,
		tokenStore:           tokenStore,
		permMiddleware:       permMiddleware,
		authHandler:          authHandler,
		gcService:            gcService,
		ssoStore:             newClientSSOStore(),
		authRateLimiter:      authRL,
		shareLinkRateLimiter: shareLinkRL,
		zipRateLimiter:       zipRL,
		apiKeyManager:        apiKeyManager,
		version:              version,
		router:               router,
	}

	s.setupRoutes()

	// Start GC service after routes are set up
	if gcService != nil {
		gcService.Start()
	}

	return s
}

func buildCORSConfig(cfg *config.Config) cors.Config {
	allowedOrigins := configuredOrigins(cfg.CORS.AllowedOrigins)
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
			"X-Block-Hash",
			"Seafile-Repo-Token",
			"X-Block-Upload-Session",
			"X-Requested-With", // Common AJAX header
		},
		ExposeHeaders:    []string{"Content-Length", "Content-Type", "Retry-After", "X-Quota-Warning"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}
	if cfg.Auth.DevMode {
		corsConfig.AllowAllOrigins = true
	} else if len(allowedOrigins) > 0 {
		corsConfig.AllowOrigins = allowedOrigins
	} else {
		corsConfig.AllowOriginFunc = func(string) bool { return false }
	}
	return corsConfig
}

func configureTrustedProxies(router *gin.Engine, cfg *config.Config) error {
	return router.SetTrustedProxies(configuredTrustedProxies(cfg.Server.TrustedProxies))
}

func configuredOrigins(origins []string) []string {
	configured := make([]string, 0, len(origins))
	for _, origin := range origins {
		origin = strings.TrimSpace(origin)
		if origin == "" {
			continue
		}
		configured = append(configured, origin)
	}
	return configured
}

func configuredTrustedProxies(values []string) []string {
	configured := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		configured = append(configured, value)
	}
	if len(configured) == 0 {
		return nil
	}
	return configured
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
	manager.SetLocalRegion(cfg.Server.Region)

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
		if strings.EqualFold(backendCfg.Type, "s3") && strings.TrimSpace(backendCfg.Bucket) == "" {
			slog.Info("Skipping unconfigured legacy storage backend", "backend", name)
			continue
		}
		classCfg := config.StorageClassConfig{
			Type:                 backendCfg.Type,
			Bucket:               backendCfg.Bucket,
			Region:               backendCfg.Region,
			Endpoint:             backendCfg.Endpoint,
			AccessKey:            backendCfg.AccessKey,
			SecretKey:            backendCfg.SecretKey,
			ServerSideEncryption: backendCfg.ServerSideEncryption,
			SSEKMSKeyID:          backendCfg.SSEKMSKeyID,
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
	bucket := strings.TrimSpace(cfg.Bucket)
	if bucket == "" {
		return nil, fmt.Errorf("storage class %s bucket is not configured", name)
	}

	region := strings.TrimSpace(cfg.Region)
	if region == "" {
		region = "us-east-1"
	}

	endpoint := strings.TrimSpace(cfg.Endpoint)
	accessKey := strings.TrimSpace(cfg.AccessKey)
	secretKey := strings.TrimSpace(cfg.SecretKey)
	serverSideEncryption := strings.TrimSpace(cfg.ServerSideEncryption)
	sseKMSKeyID := strings.TrimSpace(cfg.SSEKMSKeyID)

	// Determine access type from tier
	accessType := storage.AccessImmediate
	if cfg.Tier == "cold" {
		accessType = storage.AccessDelayed
	}

	s3Cfg := storage.S3Config{
		Endpoint:             endpoint,
		Bucket:               bucket,
		Region:               region,
		AccessKeyID:          accessKey,
		SecretAccessKey:      secretKey,
		ServerSideEncryption: serverSideEncryption,
		SSEKMSKeyID:          sseKMSKeyID,
		UsePathStyle:         cfg.UsePathStyle || endpoint != "",
		AccessType:           accessType,
	}

	return storage.NewS3Store(context.Background(), s3Cfg)
}

// initS3Storage initializes the S3 storage backend (legacy, single backend)
func initS3Storage(cfg *config.Config) (*storage.S3Store, error) {
	var (
		endpoint    string
		bucket      string
		region      string
		accessKey   string
		secretKey   string
		sseMode     string
		sseKMSKeyID string
	)

	// Fall back only to the legacy single-backend config.
	// Do not derive the singleton store from storage.classes; multi-region nodes
	// must use storageManager-backed resolution instead of a process-wide default.
	if hotBackend, ok := cfg.Storage.Backends["hot"]; ok {
		endpoint = strings.TrimSpace(hotBackend.Endpoint)
		bucket = strings.TrimSpace(hotBackend.Bucket)
		region = strings.TrimSpace(hotBackend.Region)
		accessKey = strings.TrimSpace(hotBackend.AccessKey)
		secretKey = strings.TrimSpace(hotBackend.SecretKey)
		sseMode = strings.TrimSpace(hotBackend.ServerSideEncryption)
		sseKMSKeyID = strings.TrimSpace(hotBackend.SSEKMSKeyID)
	}

	if bucket == "" {
		return nil, errLegacyS3NotConfigured
	}

	if region == "" {
		region = "us-east-1"
	}

	s3Cfg := storage.S3Config{
		Endpoint:             endpoint,
		Bucket:               bucket,
		Region:               region,
		AccessKeyID:          accessKey,
		SecretAccessKey:      secretKey,
		ServerSideEncryption: sseMode,
		SSEKMSKeyID:          sseKMSKeyID,
		UsePathStyle:         endpoint != "", // Use path style for custom endpoints (MinIO)
		AccessType:           storage.AccessImmediate,
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

	s.setupRuntimeHooks()
	s.registerCoreRoutes()

	serverURL := s.resolveServerURL()
	s.registerAPIV2Routes(serverURL)
	s.registerLegacyAPIV2Routes(serverURL)
	s.registerAPIV21Routes(serverURL)
	s.registerPublicRoutes(serverURL)
	s.registerCompatibilityRoutes(serverURL)

}

func (s *Server) billingRedirectTarget(rawQuery string) (string, error) {
	portalURL := strings.TrimSpace(s.config.Billing.URL)
	if portalURL == "" {
		return "", fmt.Errorf("billing portal is not configured")
	}
	return externalRedirectTarget(portalURL, rawQuery)
}

func externalRedirectTarget(targetURL, rawQuery string) (string, error) {
	targetURL = strings.TrimSpace(targetURL)
	if targetURL == "" {
		return "", fmt.Errorf("redirect target is not configured")
	}

	target, err := url.Parse(targetURL)
	if err != nil {
		return "", err
	}

	if rawQuery == "" {
		return target.String(), nil
	}

	queryValues, err := url.ParseQuery(rawQuery)
	if err != nil {
		return "", err
	}

	merged := target.Query()
	for key, values := range queryValues {
		for _, value := range values {
			merged.Add(key, value)
		}
	}
	target.RawQuery = merged.Encode()
	return target.String(), nil
}

func (s *Server) deleteAccountRedirectTarget(rawQuery string) (string, error) {
	targetURL := strings.TrimSpace(s.config.Accounts.DeleteAccountURL)
	if targetURL == "" {
		return "", fmt.Errorf("accounts delete account URL is not configured")
	}
	return externalRedirectTarget(targetURL, rawQuery)
}

func (s *Server) handleBillingRedirect(c *gin.Context) {
	if _, orgID, _ := s.resolveUserAuth(c); orgID == "" {
		c.Redirect(http.StatusFound, "/accounts/login/?next="+url.QueryEscape(c.Request.URL.RequestURI()))
		return
	}

	target, err := s.billingRedirectTarget(c.Request.URL.RawQuery)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
		return
	}

	c.Redirect(http.StatusTemporaryRedirect, target)
}

func (s *Server) handleDeleteAccountRedirect(c *gin.Context) {
	if _, orgID, _ := s.resolveUserAuth(c); orgID == "" {
		c.Redirect(http.StatusFound, "/accounts/login/?next="+url.QueryEscape(c.Request.URL.RequestURI()))
		return
	}

	target, err := s.deleteAccountRedirectTarget(c.Request.URL.RawQuery)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
		return
	}

	c.Redirect(http.StatusTemporaryRedirect, target)
}

func extractRequestAuthToken(c *gin.Context) string {
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

	if token == "undefined" || token == "null" {
		return ""
	}

	return token
}

func isSessionValidationInfraError(err error) bool {
	if err == nil {
		return false
	}

	return !errors.Is(err, authpkg.ErrSessionExpired) &&
		!errors.Is(err, authpkg.ErrSessionInvalid) &&
		!errors.Is(err, authpkg.ErrSessionRevoked) &&
		!errors.Is(err, authpkg.ErrSessionNotFound)
}

func requestOrgScopeHint(c *gin.Context) string {
	path := c.Request.URL.Path
	const orgAPIPrefix = "/api/v2.1/org/"
	if !strings.HasPrefix(path, orgAPIPrefix) {
		return ""
	}

	rest := strings.TrimPrefix(path, orgAPIPrefix)
	if rest == "" {
		return ""
	}
	segment := rest
	if slash := strings.IndexByte(rest, '/'); slash >= 0 {
		segment = rest[:slash]
	}
	if segment == "" || segment == "admin" {
		return ""
	}
	return segment
}

func (s *Server) matchDevToken(token string) (config.DevTokenEntry, bool) {
	if !s.config.Auth.DevMode || token == "" {
		return config.DevTokenEntry{}, false
	}
	for _, devToken := range s.config.Auth.DevTokens {
		if devToken.Token == token {
			return devToken, true
		}
	}
	return config.DevTokenEntry{}, false
}

func (s *Server) applyDevToken(c *gin.Context, devToken config.DevTokenEntry) {
	c.Set("user_id", devToken.UserID)
	c.Set("org_id", devToken.OrgID)
	if devToken.Email != "" {
		c.Set("email", devToken.Email)
	}
	if devToken.Role != "" {
		c.Set("role", devToken.Role)
	}
}

func (s *Server) touchUserLastLogin(orgID, userID string, at time.Time) {
	if s.db == nil || orgID == "" || userID == "" {
		return
	}
	if err := s.db.Session().Query(`
		UPDATE users SET last_login_at = ? WHERE org_id = ? AND user_id = ?
	`, at, orgID, userID).Exec(); err != nil {
		slog.Warn("Failed to update last_login_at", "org_id", orgID, "user_id", userID, "error", err)
	}
}

// resolveOrgForPanel extracts orgID/orgName for HTML page injection (best-effort, never blocks).
// Order: dev tokens → sesamefs_auth cookie → Authorization header → OIDC session → empty.
func (s *Server) resolveOrgForPanel(c *gin.Context) (orgID, orgName string) {
	// Resolve org context using the same authenticated identity source that powers
	// the API middleware. This avoids injecting a stale org_id into the org-admin
	// shell when the cookie and active API token diverge.
	_, orgID, _ = s.resolveUserAuth(c)

	// Best-effort org name lookup.
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
	if matchedToken, ok := s.matchDevToken(extractRequestAuthToken(c)); ok {
		if matchedToken.Email != "" {
			return matchedToken.Email
		}
		if s.db != nil && matchedToken.UserID != "" && matchedToken.OrgID != "" {
			var email string
			if err := s.db.Session().Query(
				`SELECT email FROM users WHERE org_id = ? AND user_id = ?`,
				matchedToken.OrgID, matchedToken.UserID,
			).Scan(&email); err == nil && email != "" {
				return email
			}
		}
		if matchedToken.UserID != "" {
			return matchedToken.UserID + "@sesamefs.local"
		}
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
	if matchedToken, ok := s.matchDevToken(extractRequestAuthToken(c)); ok {
		userID = matchedToken.UserID
		orgID = matchedToken.OrgID
		role = matchedToken.Role
		if role == "" && s.db != nil && orgID != "" && userID != "" {
			s.db.Session().Query( //nolint:errcheck
				`SELECT role FROM users WHERE org_id = ? AND user_id = ?`, orgID, userID,
			).Scan(&role)
		}
		return
	}

	// 2. Extract token from cookie.
	token := extractRequestAuthToken(c)
	if token == "" {
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

// enforceAccountStatus checks that the user and their org are active.
// Used ONLY for repo API token auth (sessions are killed at source on deactivate/delete).
func (s *Server) checkAccountStatus(userID, orgID string) error {
	if s == nil || s.db == nil {
		return nil
	}

	// Check user status
	var userStatus string
	if err := s.db.Session().Query(
		`SELECT status FROM users WHERE org_id = ? AND user_id = ?`, orgID, userID,
	).Scan(&userStatus); err == nil {
		if !v2.IsUserUsable(userStatus) {
			return fmt.Errorf("account %s", userStatus)
		}
	}

	// Check org status
	var orgStatus string
	if err := s.db.Session().Query(
		`SELECT status FROM organizations WHERE org_id = ?`, orgID,
	).Scan(&orgStatus); err == nil {
		if !v2.IsOrgUsable(orgStatus) {
			return fmt.Errorf("organization %s", orgStatus)
		}
	}

	return nil
}

// enforceAccountStatus checks that the user and their org are active.
// Used ONLY for repo API token auth (sessions are killed at source on deactivate/delete).
// Returns an error (and aborts the gin context) if access is denied.
func (s *Server) enforceAccountStatus(c *gin.Context, userID, orgID string) error {
	if err := s.checkAccountStatus(userID, orgID); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		c.Abort()
		return err
	}
	return nil
}

// authMiddleware validates authentication tokens
func (s *Server) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := extractRequestAuthToken(c)

		if token == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "missing authorization header"})
			c.Abort()
			return
		}

		// In dev mode, check dev tokens first
		if devToken, ok := s.matchDevToken(token); ok {
			s.applyDevToken(c, devToken)
			c.Next()
			return
		}

		// Try to validate as OIDC session token
		if s.authHandler != nil {
			sessionMgr := s.authHandler.GetSessionManager()
			if sessionMgr != nil {
				session, err := sessionMgr.ValidateSession(token)
				if err == nil {
					// Sessions are killed at source when a user/org is deactivated or deleted,
					// so no per-request status check is needed here.
					c.Set("user_id", session.UserID)
					c.Set("org_id", session.OrgID)
					c.Set("email", session.Email)
					c.Set("role", session.Role)
					if session.APIKeyScope != "" {
						c.Set("api_key_scope", session.APIKeyScope)
					}
					c.Next()
					return
				}
				// If the session was found but expired, return immediately
				// with a specific error so the frontend can redirect to login.
				if authpkg.IsSessionExpired(err) {
					c.JSON(http.StatusUnauthorized, gin.H{"error": "session expired"})
					c.Abort()
					return
				}
				if isSessionValidationInfraError(err) {
					c.JSON(http.StatusServiceUnavailable, gin.H{"error": "session validation unavailable"})
					c.Abort()
					return
				}
			}
		}

		// Try to validate as a user/service API key
		if s.apiKeyManager != nil {
			apiKey, err := s.apiKeyManager.ValidateKey(token)
			if err == nil {
				if err := s.enforceAccountStatus(c, apiKey.UserID.String(), apiKey.OrgID.String()); err != nil {
					return
				}
				var email, role string
				_ = s.db.Session().Query(`SELECT email, role FROM users WHERE org_id = ? AND user_id = ?`,
					apiKey.OrgID, apiKey.UserID).Scan(&email, &role)
				role = apikeys.ConstrainRoleForScope(role, apiKey.Scope)
				c.Set("user_id", apiKey.UserID.String())
				c.Set("org_id", apiKey.OrgID.String())
				c.Set("email", email)
				c.Set("role", role)
				c.Set("api_key_scope", apiKey.Scope)
				c.Next()
				return
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
					// Enforce user and org lifecycle status for API tokens too
					if err := s.enforceAccountStatus(c, generatedBy, orgID); err != nil {
						return
					}
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

		token := extractRequestAuthToken(c)

		if token == "" {
			redirectToLogin(false)
			return
		}

		// Dev-mode tokens.
		if devToken, ok := s.matchDevToken(token); ok {
			s.applyDevToken(c, devToken)
			c.Next()
			return
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
				if authpkg.IsSessionExpired(err) {
					redirectToLogin(true)
					return
				}
				if isSessionValidationInfraError(err) {
					c.JSON(http.StatusServiceUnavailable, gin.H{"error": "session validation unavailable"})
					c.Abort()
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
					// Enforce user and org lifecycle status for API tokens too
					if err := s.enforceAccountStatus(c, generatedBy, orgID); err != nil {
						return
					}
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

		if token == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			c.Abort()
			return
		}

		// Check if it's a dev token (only in dev mode)
		if devToken, ok := s.matchDevToken(token); ok {
			s.applyDevToken(c, devToken)
			c.Next()
			return
		}

		// Check if it's a valid repo token (from download-info)
		if accessToken, valid := s.tokenStore.GetToken(token, TokenTypeDownload); valid {
			// Repo tokens are long-lived — verify the account hasn't been
			// deactivated or deleted since the token was issued.
			if err := s.enforceAccountStatus(c, accessToken.UserID, accessToken.OrgID); err != nil {
				return // enforceAccountStatus already wrote 403 + Abort
			}
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
				if isSessionValidationInfraError(err) {
					c.JSON(http.StatusServiceUnavailable, gin.H{"error": "session validation unavailable"})
					c.Abort()
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
func getEffectiveHostname(c *gin.Context, configuredURL string) string {
	return httputil.GetEffectiveHostname(c, configuredURL)
}

// getBaseURLFromRequest delegates to httputil.GetBaseURLFromRequest.
func getBaseURLFromRequest(c *gin.Context, configuredURL string) string {
	return httputil.GetBaseURLFromRequest(c, configuredURL)
}

// getRelayPortFromRequest delegates to httputil.GetRelayPortFromRequest.
func getRelayPortFromRequest(c *gin.Context, configuredURL string) string {
	return httputil.GetRelayPortFromRequest(c, configuredURL)
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
				s.touchUserLastLogin(devToken.OrgID, devToken.UserID, time.Now().UTC())
				c.JSON(http.StatusOK, gin.H{
					"token": devToken.Token,
				})
				return
			}
		}
	}

	// Try API key auth: user sends email + api_key as password.
	if s.db != nil && s.apiKeyManager != nil {
		var userUUID, orgUUID gocql.UUID
		err := s.db.Session().Query(
			`SELECT user_id, org_id FROM users_by_email WHERE email = ?`, username,
		).Scan(&userUUID, &orgUUID)
		if err == nil {
			apiKey, err := s.apiKeyManager.ValidateKey(password)
			if err == nil && apiKey.UserID == userUUID && apiKey.OrgID == orgUUID {
				if err := s.enforceAccountStatus(c, userUUID.String(), orgUUID.String()); err != nil {
					return
				}
				// API key matches — create a long-lived session for the desktop client.
				if s.authHandler != nil {
					sessionMgr := s.authHandler.GetSessionManager()
					if sessionMgr != nil {
						var email, role string
						_ = s.db.Session().Query(`SELECT email, role FROM users WHERE org_id = ? AND user_id = ?`,
							orgUUID, userUUID).Scan(&email, &role)
						role = apikeys.ConstrainRoleForScope(role, apiKey.Scope)
						session, sErr := sessionMgr.CreateAPITokenSessionFromAPIKey(
							userUUID.String(), orgUUID.String(), email, role, apiKey.KeyHash, apiKey.Scope, apiKey.ExpiresAt,
						)
						if sErr == nil {
							s.touchUserLastLogin(orgUUID.String(), userUUID.String(), time.Now().UTC())
							c.JSON(http.StatusOK, gin.H{"token": session.Token})
							return
						}
					}
				}
			}
		}
	}

	// Generic rejection — same message for user-not-found and wrong-key.
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

	brand := strings.TrimSpace(s.config.Server.DesktopCustomBrand)
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
	info["file_server_root"] = getBaseURLFromRequest(c, s.config.Server.URL) + "/seafhttp"

	if logo := strings.TrimSpace(s.config.Server.DesktopCustomLogo); logo != "" {
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

	baseURL := httputil.GetBrowserURL(c, s.config.Server.URL)

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
	orgUUID, _ := gocql.ParseUUID(orgID)
	userUUID, _ := gocql.ParseUUID(userID)

	// Fetch user data from database.
	var email, name, role string
	var quotaBytes int64
	var userTrafficUploadQuota, userTrafficDownloadQuota int64
	err := s.db.Session().Query(`
		SELECT email, name, role, quota_bytes,
		       traffic_upload_quota, traffic_download_quota
		FROM users WHERE org_id = ? AND user_id = ?
	`, orgUUID, userUUID).Scan(&email, &name, &role, &quotaBytes,
		&userTrafficUploadQuota, &userTrafficDownloadQuota)

	if err != nil {
		email = userID + "@sesamefs.local"
		name = userID
		role = "user"
		quotaBytes = -2
		userTrafficUploadQuota = -1
		userTrafficDownloadQuota = -1
	}

	// Fetch org data: plan, quotas, enforcement profile, period.
	var orgPlan, billingCycle, quotaPolicy string
	var storageQuota, trafficQuota, trafficUploadQuota, trafficDownloadQuota int64
	var maxUsers int
	var currentPeriodStartedAt, currentPeriodEndsAt *time.Time
	_ = s.db.Session().Query(`
		SELECT plan, billing_cycle, quota_policy, storage_quota,
		       traffic_quota, traffic_upload_quota, traffic_download_quota, max_users,
		       current_period_started_at, current_period_ends_at
		FROM organizations WHERE org_id = ?
	`, orgUUID).Scan(&orgPlan, &billingCycle, &quotaPolicy, &storageQuota,
		&trafficQuota, &trafficUploadQuota, &trafficDownloadQuota, &maxUsers,
		&currentPeriodStartedAt, &currentPeriodEndsAt)

	// Read live storage usage (user-level for legacy, org-level for new storage object).
	userUsedBytes := traffic.ReadStorageUsed(s.db, fmt.Sprintf("user:%s:%s", orgID, userID))
	orgStorageUsed := traffic.ReadStorageUsed(s.db, fmt.Sprintf("org:%s", orgID))

	now := time.Now().UTC()
	periodStartedAt := traffic.EffectivePeriodStart(currentPeriodStartedAt, now)

	// Query current period traffic usage.
	userTrafficUsage := traffic.ReadUserPeriodUsage(s.db, orgID, userID, periodStartedAt)
	orgTrafficUsage := traffic.ReadOrgPeriodUsage(s.db, orgID, periodStartedAt)

	var currentUsers int
	_ = s.db.Session().Query(`SELECT COUNT(*) FROM users WHERE org_id = ?`, orgUUID).Scan(&currentUsers)

	// Use email username as display name if name is empty.
	if name == "" {
		if atIdx := strings.Index(email, "@"); atIdx > 0 {
			name = email[:atIdx]
		} else {
			name = email
		}
	}

	// Legacy desktop-client fields.
	isStaff := middleware.IsPlatformSuperAdmin(orgID, middleware.OrganizationRole(role))
	spaceUsage := "0%"
	if quotaBytes > 0 && userUsedBytes > 0 {
		percentage := float64(userUsedBytes) / float64(quotaBytes) * 100
		spaceUsage = fmt.Sprintf("%.1f%%", percentage)
	}

	// Resolve capabilities from role + enforcement profile.
	profile := s.config.GetEnforcementProfile(quotaPolicy)
	resolved := plans.ResolveCapabilities(role, profile)

	// Compute storage state.
	var storagePct float64
	storageOverQuota := false
	if storageQuota > 0 {
		storagePct = float64(orgStorageUsed) / float64(storageQuota) * 100
		storageOverQuota = orgStorageUsed > storageQuota
	}

	// Compute traffic state.
	var trafficPct float64
	trafficOverQuota := false
	if trafficQuota > 0 {
		trafficPct = float64(orgTrafficUsage.Combined) / float64(trafficQuota) * 100
		trafficOverQuota = orgTrafficUsage.Combined > trafficQuota
	}
	uploadOverQuota := trafficUploadQuota > 0 && orgTrafficUsage.Upload > trafficUploadQuota
	downloadOverQuota := trafficDownloadQuota > 0 && orgTrafficUsage.Download > trafficDownloadQuota

	// Derived flags.
	isOrgOwner := role == "owner"
	canUpgrade := plans.ComputeCanUpgrade(role, quotaPolicy, storagePct, trafficPct, storageOverQuota, trafficOverQuota)

	trafficResetDate := traffic.EffectiveTrafficResetDate(currentPeriodStartedAt, currentPeriodEndsAt, now)

	// Return account info.
	// CRITICAL: Preserve all Seafile-compatible fields for desktop client.
	c.JSON(http.StatusOK, gin.H{
		// === Seafile-compatible fields ===
		"email":         email,
		"name":          name,
		"login_id":      email,
		"contact_email": email,
		"department":    "",
		"institution":   orgID,
		"is_staff":      isStaff,
		"is_org_staff": func() int {
			if middleware.IsOrgStaff(role) {
				return 1
			}
			return 0
		}(),
		"usage":                       userUsedBytes,
		"total":                       quotaBytes,
		"space_usage":                 spaceUsage,
		"avatar_url":                  getBaseURLFromRequest(c, s.config.Server.URL) + "/static/img/default-avatar.png",
		"enable_subscription":         true,
		"file_updates_email_interval": 0,
		"collaborate_email_interval":  0,

		// === Role & plan ===
		"role":                      role,
		"plan":                      orgPlan,
		"is_org_owner":              isOrgOwner,
		"can_upgrade":               canUpgrade,
		"billing_cycle":             billingCycle,
		"max_users":                 maxUsers,
		"current_users":             currentUsers,
		"current_period_started_at": currentPeriodStartedAt,
		"current_period_ends_at":    currentPeriodEndsAt,

		// === Structured storage object ===
		"storage": gin.H{
			"used":       orgStorageUsed,
			"quota":      storageQuota,
			"percent":    storagePct,
			"over_quota": storageOverQuota,
		},

		// === Structured traffic object ===
		"traffic": gin.H{
			"used":                orgTrafficUsage.Combined,
			"quota":               trafficQuota,
			"percent":             trafficPct,
			"over_quota":          trafficOverQuota,
			"upload_used":         orgTrafficUsage.Upload,
			"upload_quota":        trafficUploadQuota,
			"upload_over_quota":   uploadOverQuota,
			"download_used":       orgTrafficUsage.Download,
			"download_quota":      trafficDownloadQuota,
			"download_over_quota": downloadOverQuota,
			"reset_date":          trafficResetDate,
		},

		// === Resolved capability flags (role AND enforcement profile) ===
		"can_add_repo":                       resolved.Capabilities["can_add_repo"],
		"can_share_repo":                     resolved.Capabilities["can_share_repo"],
		"can_add_group":                      resolved.Capabilities["can_add_group"],
		"can_generate_share_link":            resolved.Capabilities["can_generate_share_link"],
		"can_generate_upload_link":           resolved.Capabilities["can_generate_upload_link"],
		"can_send_share_link_mail":           resolved.Capabilities["can_send_share_link_mail"],
		"can_publish_repo":                   resolved.Capabilities["can_publish_repo"],
		"can_use_global_address_book":        resolved.Capabilities["can_use_global_address_book"],
		"can_connect_with_desktop_clients":   resolved.Capabilities["can_connect_with_desktop_clients"],
		"can_connect_with_android_clients":   resolved.Capabilities["can_connect_with_android_clients"],
		"can_connect_with_ios_clients":       resolved.Capabilities["can_connect_with_ios_clients"],
		"can_export_files_via_mobile_client": resolved.Capabilities["can_export_files_via_mobile_client"],

		// === Numeric limits from enforcement profile ===
		"share_link_expire_days_max":  resolved.Limits.ShareLinkExpireDaysMax,
		"upload_link_expire_days_max": resolved.Limits.UploadLinkExpireDaysMax,

		// === Upgrade CTA support ===
		"upgrade_features": resolved.UpgradeFeatures,

		// === Per-user traffic (backward compat) ===
		"traffic_upload_quota":   userTrafficUploadQuota,
		"traffic_upload_used":    userTrafficUsage.Upload,
		"traffic_download_quota": userTrafficDownloadQuota,
		"traffic_download_used":  userTrafficUsage.Download,
	})
}

// handleUpdateAccountInfo updates editable account profile fields for the authenticated user.
// PUT/PATCH /api2/account/info/
func (s *Server) handleUpdateAccountInfo(c *gin.Context) {
	var req struct {
		Name         *string `json:"name"`
		ContactEmail *string `json:"contact_email"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if req.ContactEmail != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "contact email updates are not supported"})
		return
	}
	if req.Name == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no supported profile fields were provided"})
		return
	}
	if s.db == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "database is not available"})
		return
	}

	name := strings.TrimSpace(*req.Name)
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name cannot be empty"})
		return
	}

	userID := c.GetString("user_id")
	orgID := c.GetString("org_id")
	orgUUID, err := gocql.ParseUUID(orgID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid organization id"})
		return
	}
	userUUID, err := gocql.ParseUUID(userID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
		return
	}

	userProjectionRow, err := db.ReadAdminUserProjectionRow(s.db.Session(), orgID, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to build profile read model"})
		return
	}
	orgProjectionRow, err := db.ReadAdminOrganizationProjectionRow(s.db.Session(), orgID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to build organization read model"})
		return
	}
	userProjectionRow.Name = name
	if orgProjectionRow.OwnerEmail == userProjectionRow.Email {
		orgProjectionRow.OwnerName = name
	}

	batch := s.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		UPDATE users SET name = ? WHERE org_id = ? AND user_id = ?
	`, name, orgUUID, userUUID)
	db.AddUpsertAdminUserReadModelQuery(batch, userProjectionRow)
	db.AddUpsertAdminOrganizationReadModelQuery(batch, orgProjectionRow)
	if err := batch.Exec(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update profile"})
		return
	}

	s.handleAccountInfo(c)
}

// handleGetSubscription returns the current org's plan and usage info.
// GET /api/v2.1/subscription/
func (s *Server) handleGetSubscription(c *gin.Context) {
	userID := c.GetString("user_id")
	orgID := c.GetString("org_id")
	orgUUID, _ := gocql.ParseUUID(orgID)

	var plan, billingCycle, quotaPolicy string
	var storageQuota int64
	var trafficQuota, trafficUploadQuota, trafficDownloadQuota int64
	var maxUsers int
	var currentPeriodStartedAt, currentPeriodEndsAt *time.Time
	_ = s.db.Session().Query(`
		SELECT plan, billing_cycle, storage_quota,
		       traffic_quota, traffic_upload_quota, traffic_download_quota, max_users,
		       quota_policy, current_period_started_at, current_period_ends_at
		FROM organizations WHERE org_id = ?
	`, orgUUID).Scan(&plan, &billingCycle, &storageQuota,
		&trafficQuota, &trafficUploadQuota, &trafficDownloadQuota, &maxUsers,
		&quotaPolicy, &currentPeriodStartedAt, &currentPeriodEndsAt)

	// Read live storage usage from the counter table.
	storageUsed := traffic.ReadStorageUsed(s.db, fmt.Sprintf("org:%s", orgID))

	now := time.Now().UTC()
	periodStartedAt := traffic.EffectivePeriodStart(currentPeriodStartedAt, now)

	// Current quota-period org traffic totals.
	orgTrafficUsage := traffic.ReadOrgPeriodUsage(s.db, orgID, periodStartedAt)

	// Count current users.
	var currentUsers int
	_ = s.db.Session().Query(
		`SELECT COUNT(*) FROM users WHERE org_id = ?`, orgUUID,
	).Scan(&currentUsers)

	// Compute storage state.
	var storagePct float64
	storageOverQuota := false
	if storageQuota > 0 {
		storagePct = float64(storageUsed) / float64(storageQuota) * 100
		storageOverQuota = storageUsed > storageQuota
	}

	// Compute traffic state.
	var trafficPct float64
	trafficOverQuota := false
	if trafficQuota > 0 {
		trafficPct = float64(orgTrafficUsage.Combined) / float64(trafficQuota) * 100
		trafficOverQuota = orgTrafficUsage.Combined > trafficQuota
	}
	uploadOverQuota := trafficUploadQuota > 0 && orgTrafficUsage.Upload > trafficUploadQuota
	downloadOverQuota := trafficDownloadQuota > 0 && orgTrafficUsage.Download > trafficDownloadQuota

	trafficResetDate := traffic.EffectiveTrafficResetDate(currentPeriodStartedAt, currentPeriodEndsAt, now)

	// Per-user traffic for the caller.
	userTrafficUsage := traffic.ReadUserPeriodUsage(s.db, orgID, userID, periodStartedAt)

	c.JSON(http.StatusOK, gin.H{
		// Flat fields (backward compat)
		"plan":                      plan,
		"billing_cycle":             billingCycle,
		"quota_policy":              quotaPolicy,
		"storage_quota":             storageQuota,
		"storage_used":              storageUsed,
		"storage_percent":           storagePct,
		"traffic_quota":             trafficQuota,
		"traffic_combined_used":     orgTrafficUsage.Combined,
		"traffic_upload_quota":      trafficUploadQuota,
		"traffic_upload_used":       orgTrafficUsage.Upload,
		"traffic_download_quota":    trafficDownloadQuota,
		"traffic_download_used":     orgTrafficUsage.Download,
		"traffic_reset_date":        trafficResetDate,
		"max_users":                 maxUsers,
		"current_users":             currentUsers,
		"current_period_started_at": currentPeriodStartedAt,
		"current_period_ends_at":    currentPeriodEndsAt,
		// Structured objects
		"storage": gin.H{
			"used":       storageUsed,
			"quota":      storageQuota,
			"percent":    storagePct,
			"over_quota": storageOverQuota,
		},
		"traffic": gin.H{
			"used":                orgTrafficUsage.Combined,
			"quota":               trafficQuota,
			"percent":             trafficPct,
			"over_quota":          trafficOverQuota,
			"upload_used":         orgTrafficUsage.Upload,
			"upload_quota":        trafficUploadQuota,
			"upload_over_quota":   uploadOverQuota,
			"download_used":       orgTrafficUsage.Download,
			"download_quota":      trafficDownloadQuota,
			"download_over_quota": downloadOverQuota,
			"reset_date":          trafficResetDate,
		},
		// Per-user traffic
		"user_upload_used":   userTrafficUsage.Upload,
		"user_download_used": userTrafficUsage.Download,
	})
}

// handleSearchUser searches for users within the caller organization.
// GET /api2/search-user/?q=<query>
// Returns users matching the query string (by email or name)
func (s *Server) handleSearchUser(c *gin.Context) {
	query := c.Query("q")
	orgID := c.GetString("org_id")

	if query == "" {
		c.JSON(http.StatusOK, gin.H{"users": []gin.H{}})
		return
	}

	users := make([]gin.H, 0)
	queryLower := strings.ToLower(query)
	appendMatchedUser := func(email, name, status string) {
		if !v2.IsUserUsable(status) {
			return
		}
		if !strings.Contains(strings.ToLower(email), queryLower) && !strings.Contains(strings.ToLower(name), queryLower) {
			return
		}
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
			"avatar_url":    getBaseURLFromRequest(c, s.config.Server.URL) + "/static/img/default-avatar.png",
			"contact_email": email,
			"login_id":      email,
		})
	}

	// Query all users in the caller organization.
	iter := s.db.Session().Query(`
		SELECT user_id, email, name, role, status FROM users WHERE org_id = ?
	`, orgID).Iter()

	var iterUserID, email, name, iterRole, status string

	for iter.Scan(&iterUserID, &email, &name, &iterRole, &status) {
		appendMatchedUser(email, name, status)
	}
	if err := iter.Close(); err != nil {
		slog.Error("search-user query failed", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "search failed"})
		return
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
	if s.authRateLimiter != nil {
		s.authRateLimiter.Stop()
	}
	if s.shareLinkRateLimiter != nil {
		s.shareLinkRateLimiter.Stop()
	}
	if s.zipRateLimiter != nil {
		s.zipRateLimiter.Stop()
	}
	s.seafHTTPHandler.Close()
	if s.apiKeyManager != nil {
		s.apiKeyManager.Stop()
	}
	if s.authHandler != nil && s.authHandler.GetOIDCClient() != nil {
		s.authHandler.GetOIDCClient().StopStateSweeper()
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
	if serverURL := strings.TrimSpace(s.config.Server.URL); serverURL != "" {
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
		middleware.SetCSP(c, "default-src 'none'; script-src 'unsafe-inline'; img-src 'self'; style-src 'self'; frame-ancestors 'none'")
		if err := templates.Render(c.Writer, "login_success.html", data); err != nil {
			log.Printf("[handleSSOCallback] template error: %v", err)
			c.String(http.StatusInternalServerError, "Internal Server Error")
		}
		return
	}

	// Web browser login — redirect to home page.
	c.Redirect(http.StatusFound, "/")
}

// handleLogout invalidates the user's session and redirects to home.
// GET /accounts/logout/
func (s *Server) handleLogout(c *gin.Context) {
	if token := extractRequestAuthToken(c); token != "" && s.authHandler != nil {
		if sessionMgr := s.authHandler.GetSessionManager(); sessionMgr != nil {
			if err := sessionMgr.InvalidateSession(token); err != nil {
				log.Printf("[handleLogout] failed to invalidate session: %v", err)
			}
		}
	}

	// Clear the auth cookie server-side (maxAge=-1 expires immediately)
	isSecure := c.Request.TLS != nil
	c.SetCookie("sesamefs_auth", "", -1, "/", "", isSecure, false)

	// Redirect to home — the SPA will detect the missing session and show the login page
	c.Redirect(http.StatusFound, "/")
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

// handleGCStatus returns the current GC status.
// Protected by RequireSuperAdmin() middleware at the route group level.
// GET /api/v2.1/admin/gc/status
func (s *Server) handleGCStatus(c *gin.Context) {
	if s.gcService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "GC service not available"})
		return
	}

	s.gcService.RefreshFailedItemSnapshot()
	c.JSON(http.StatusOK, s.gcService.Status())
}

// handleGCRun triggers a manual GC run.
// Protected by RequireSuperAdmin() middleware at the route group level.
// POST /api/v2.1/admin/gc/run
func (s *Server) handleGCRun(c *gin.Context) {
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

func parseGCFailedItemSelector(c *gin.Context) (uuid.UUID, time.Time, gc.ItemType, string, error) {
	orgIDStr := strings.TrimSpace(c.Query("org_id"))
	if orgIDStr == "" {
		orgIDStr = strings.TrimSpace(c.PostForm("org_id"))
	}
	failedAtStr := strings.TrimSpace(c.Query("failed_at"))
	if failedAtStr == "" {
		failedAtStr = strings.TrimSpace(c.PostForm("failed_at"))
	}
	itemTypeStr := strings.TrimSpace(c.Query("item_type"))
	if itemTypeStr == "" {
		itemTypeStr = strings.TrimSpace(c.PostForm("item_type"))
	}
	itemID := strings.TrimSpace(c.Query("item_id"))
	if itemID == "" {
		itemID = strings.TrimSpace(c.PostForm("item_id"))
	}

	// Only attempt to parse a JSON body when the caller actually sent JSON.
	// An empty body with Content-Type: application/json is legal — the
	// selector may have arrived entirely via query string or form fields,
	// and many HTTP clients set the JSON header by default. Surface real
	// parse errors (malformed JSON), but do not penalise an empty body.
	if strings.HasPrefix(strings.ToLower(c.ContentType()), "application/json") {
		var body struct {
			OrgID    string `json:"org_id"`
			FailedAt string `json:"failed_at"`
			ItemType string `json:"item_type"`
			ItemID   string `json:"item_id"`
		}
		if err := c.ShouldBindJSON(&body); err != nil && !errors.Is(err, io.EOF) {
			return uuid.Nil, time.Time{}, "", "", fmt.Errorf("invalid json body: %w", err)
		}
		if orgIDStr == "" {
			orgIDStr = strings.TrimSpace(body.OrgID)
		}
		if failedAtStr == "" {
			failedAtStr = strings.TrimSpace(body.FailedAt)
		}
		if itemTypeStr == "" {
			itemTypeStr = strings.TrimSpace(body.ItemType)
		}
		if itemID == "" {
			itemID = strings.TrimSpace(body.ItemID)
		}
	}

	orgID, err := uuid.Parse(orgIDStr)
	if err != nil {
		return uuid.Nil, time.Time{}, "", "", fmt.Errorf("invalid org_id")
	}
	failedAt, err := time.Parse(time.RFC3339Nano, failedAtStr)
	if err != nil {
		failedAt, err = time.Parse(time.RFC3339, failedAtStr)
		if err != nil {
			return uuid.Nil, time.Time{}, "", "", fmt.Errorf("invalid failed_at")
		}
	}
	if itemTypeStr == "" {
		return uuid.Nil, time.Time{}, "", "", fmt.Errorf("item_type is required")
	}
	if itemID == "" {
		return uuid.Nil, time.Time{}, "", "", fmt.Errorf("item_id is required")
	}
	return orgID, failedAt, gc.ItemType(itemTypeStr), itemID, nil
}

func (s *Server) handleGCFailedItems(c *gin.Context) {
	if s.gcService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "GC service not available"})
		return
	}
	orgIDStr := strings.TrimSpace(c.Query("org_id"))
	if orgIDStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "org_id is required"})
		return
	}
	orgID, err := uuid.Parse(orgIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid org_id"})
		return
	}
	limit := s.gcService.FailedItemsPageSize()
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	items, err := s.gcService.ListFailedItems(orgID, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (s *Server) handleGCFailedItemOrgs(c *gin.Context) {
	if s.gcService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "GC service not available"})
		return
	}
	s.gcService.RefreshFailedItemSnapshot()
	limit := 20
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	orgs, err := s.gcService.ListFailedItemOrgs(limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"organizations": orgs})
}

func (s *Server) handleGCFailedItemRequeue(c *gin.Context) {
	if s.gcService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "GC service not available"})
		return
	}
	orgID, failedAt, itemType, itemID, err := parseGCFailedItemSelector(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := s.gcService.RequeueFailedItem(orgID, failedAt, itemType, itemID); err != nil {
		if errors.Is(err, gc.ErrNotLeader) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"requeued": true})
}

func (s *Server) handleGCFailedItemDelete(c *gin.Context) {
	if s.gcService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "GC service not available"})
		return
	}
	orgID, failedAt, itemType, itemID, err := parseGCFailedItemSelector(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := s.gcService.DeleteFailedItem(orgID, failedAt, itemType, itemID); err != nil {
		if errors.Is(err, gc.ErrNotLeader) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

// ---------------------------------------------------------------------------
// API Keys CRUD
// ---------------------------------------------------------------------------

// handleCreateAPIKey creates a new API key for the authenticated user.
// POST /api/v2.1/api-keys/
func (s *Server) handleCreateAPIKey(c *gin.Context) {
	if s.apiKeyManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "api key management unavailable"})
		return
	}

	userID := c.GetString("user_id")
	orgID := c.GetString("org_id")
	role := c.GetString("role")

	userUUID, err := gocql.ParseUUID(userID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user"})
		return
	}
	orgUUID, err := gocql.ParseUUID(orgID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid organization"})
		return
	}

	var req struct {
		Label         string `json:"label" form:"label"`
		Scope         string `json:"scope" form:"scope"`
		ExpiresInDays *int   `json:"expires_in_days" form:"expires_in_days"`
	}
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if req.Label == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "label is required"})
		return
	}
	if req.ExpiresInDays != nil && *req.ExpiresInDays <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expires_in_days must be greater than zero"})
		return
	}
	if !apikeys.IsValidScope(req.Scope) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "scope must be 'read', 'read-write', or 'admin'"})
		return
	}
	// Only admins/owners/superadmins can create admin-scoped keys.
	if req.Scope == apikeys.ScopeAdmin {
		if role != "superadmin" && role != "owner" && role != "admin" {
			c.JSON(http.StatusForbidden, gin.H{"error": "admin scope requires admin role"})
			return
		}
	}

	var expiresAt *time.Time
	if req.ExpiresInDays != nil && *req.ExpiresInDays > 0 {
		t := time.Now().UTC().AddDate(0, 0, *req.ExpiresInDays)
		expiresAt = &t
	}

	rawToken, key, err := s.apiKeyManager.CreateKey(userUUID, orgUUID, req.Label, req.Scope, expiresAt)
	if err != nil {
		if errors.Is(err, apikeys.ErrKeyLimitReached) {
			c.JSON(http.StatusConflict, gin.H{"error": "maximum number of API keys reached"})
			return
		}
		if errors.Is(err, apikeys.ErrInvalidExpiry) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid API key expiry"})
			return
		}
		log.Printf("[apikeys] create error user=%s: %v", userID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create API key"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"key":        rawToken,
		"key_hash":   key.KeyHash,
		"key_prefix": key.KeyPrefix,
		"label":      key.Label,
		"scope":      key.Scope,
		"created_at": key.CreatedAt,
		"expires_at": key.ExpiresAt,
	})
}

// handleListAPIKeys lists all API keys for the authenticated user.
// GET /api/v2.1/api-keys/
func (s *Server) handleListAPIKeys(c *gin.Context) {
	if s.apiKeyManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "api key management unavailable"})
		return
	}

	userID := c.GetString("user_id")
	orgID := c.GetString("org_id")

	userUUID, _ := gocql.ParseUUID(userID)
	orgUUID, _ := gocql.ParseUUID(orgID)

	keys, err := s.apiKeyManager.ListUserKeys(orgUUID, userUUID)
	if err != nil {
		log.Printf("[apikeys] list error user=%s: %v", userID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list API keys"})
		return
	}

	// Return keys without raw tokens — only prefix for identification.
	result := make([]gin.H, 0, len(keys))
	for _, k := range keys {
		result = append(result, gin.H{
			"key_hash":     k.KeyHash,
			"key_prefix":   k.KeyPrefix,
			"label":        k.Label,
			"scope":        k.Scope,
			"created_at":   k.CreatedAt,
			"last_used_at": k.LastUsedAt,
			"expires_at":   k.ExpiresAt,
		})
	}

	c.JSON(http.StatusOK, result)
}

// handleRevokeAPIKey revokes an API key belonging to the authenticated user.
// DELETE /api/v2.1/api-keys/:key_hash/
func (s *Server) handleRevokeAPIKey(c *gin.Context) {
	if s.apiKeyManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "api key management unavailable"})
		return
	}

	userID := c.GetString("user_id")
	orgID := c.GetString("org_id")
	keyHash := c.Param("key_hash")

	if keyHash == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "key_hash is required"})
		return
	}

	userUUID, _ := gocql.ParseUUID(userID)
	orgUUID, _ := gocql.ParseUUID(orgID)
	key, err := s.apiKeyManager.GetOwnedKey(orgUUID, userUUID, keyHash)
	if err != nil {
		if errors.Is(err, apikeys.ErrKeyNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "API key not found"})
			return
		}
		if errors.Is(err, apikeys.ErrNotOwner) {
			c.JSON(http.StatusForbidden, gin.H{"error": "API key does not belong to this user"})
			return
		}
		log.Printf("[apikeys] revoke precheck error user=%s key=%s: %v", userID, keyHash[:16], err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to revoke API key"})
		return
	}

	err = s.apiKeyManager.RevokeKey(orgUUID, userUUID, keyHash)
	if err != nil {
		if errors.Is(err, apikeys.ErrKeyNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "API key not found"})
			return
		}
		if errors.Is(err, apikeys.ErrNotOwner) {
			c.JSON(http.StatusForbidden, gin.H{"error": "API key does not belong to this user"})
			return
		}
		log.Printf("[apikeys] revoke error user=%s key=%s: %v", userID, keyHash[:16], err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to revoke API key"})
		return
	}

	if s.authHandler != nil {
		sessionMgr := s.authHandler.GetSessionManager()
		if sessionMgr != nil {
			if err := sessionMgr.InvalidateAPIKeySessions(keyHash); err != nil {
				if restoreErr := s.apiKeyManager.RestoreKey(key); restoreErr != nil {
					log.Printf("[apikeys] failed to restore key after session invalidation error user=%s key=%s: %v", userID, keyHash[:16], restoreErr)
				}
				log.Printf("[apikeys] failed to invalidate derived sessions user=%s key=%s: %v", userID, keyHash[:16], err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to revoke API key"})
				return
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}
