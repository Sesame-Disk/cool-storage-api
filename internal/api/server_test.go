package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/health"
	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
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
		tokenStore: nil,
		router:     gin.New(),
	}
}

type readinessStore struct {
	headBucketErr error
}

func (s *readinessStore) Put(ctx context.Context, storageKey string, data io.Reader, size int64) (string, error) {
	_, _ = io.Copy(io.Discard, data)
	return storageKey, nil
}

func (s *readinessStore) Get(ctx context.Context, storageKey string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(nil)), nil
}

func (s *readinessStore) Delete(ctx context.Context, storageKey string) error {
	return nil
}

func (s *readinessStore) Exists(ctx context.Context, storageKey string) (bool, error) {
	return true, nil
}

func (s *readinessStore) GetAccessType() storage.AccessType {
	return storage.AccessImmediate
}

func (s *readinessStore) InitiateRestore(ctx context.Context, storageKey string) (string, error) {
	return "", nil
}

func (s *readinessStore) CheckRestoreStatus(ctx context.Context, storageKey string) (bool, error) {
	return true, nil
}

func (s *readinessStore) GetRestoreExpiry(ctx context.Context, storageKey string) (*time.Time, error) {
	return nil, nil
}

func (s *readinessStore) HeadBucket(ctx context.Context) error {
	return s.headBucketErr
}

func TestExternalRedirectTarget(t *testing.T) {
	target, err := externalRedirectTarget("https://accounts.example.com/accounts/password/change/?source=accounts", "next=%2Fprofile%2F")
	if err != nil {
		t.Fatalf("externalRedirectTarget returned error: %v", err)
	}

	parsed, err := url.Parse(target)
	if err != nil {
		t.Fatalf("url.Parse returned error: %v", err)
	}
	if parsed.Scheme != "https" || parsed.Host != "accounts.example.com" {
		t.Fatalf("redirect target = %s, want https://accounts.example.com", target)
	}
	values := parsed.Query()
	if values.Get("source") != "accounts" {
		t.Fatalf("source query = %q, want %q", values.Get("source"), "accounts")
	}
	if values.Get("next") != "/profile/" {
		t.Fatalf("next query = %q, want %q", values.Get("next"), "/profile/")
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

func TestInitS3StorageRequiresExplicitLegacyConfig(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Storage.DefaultClass = "hot-s3-usa"
	cfg.Storage.Classes = map[string]config.StorageClassConfig{
		"hot-s3-usa": {
			Type:   "s3",
			Bucket: "sesamefs-usa",
			Region: "us-east-1",
		},
	}
	cfg.Storage.Backends = map[string]config.BackendConfig{
		"hot": {
			Type:   "s3",
			Bucket: "",
			Region: "",
		},
	}

	t.Setenv("S3_BUCKET", "")
	t.Setenv("S3_REGION", "")
	t.Setenv("S3_ENDPOINT", "")

	store, err := initS3Storage(cfg)
	if !errors.Is(err, errLegacyS3NotConfigured) {
		t.Fatalf("initS3Storage error = %v, want %v", err, errLegacyS3NotConfigured)
	}
	if store != nil {
		t.Fatalf("initS3Storage store = %#v, want nil", store)
	}
}

func TestInitS3StorageUsesLegacyHotBackend(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Storage.DefaultClass = "hot-s3-usa"
	cfg.Storage.Classes = map[string]config.StorageClassConfig{
		"hot-s3-usa": {
			Type:   "s3",
			Bucket: "sesamefs-usa",
			Region: "us-east-1",
		},
	}
	cfg.Storage.Backends = map[string]config.BackendConfig{
		"hot": {
			Type:   "s3",
			Bucket: "sesamefs-single",
			Region: "eu-west-1",
		},
	}

	t.Setenv("S3_BUCKET", "")
	t.Setenv("S3_REGION", "")
	t.Setenv("S3_ENDPOINT", "")
	t.Setenv("S3_ACCESS_KEY_ID", "test-key")
	t.Setenv("S3_SECRET_ACCESS_KEY", "test-secret")

	store, err := initS3Storage(cfg)
	if err != nil {
		t.Fatalf("initS3Storage returned error: %v", err)
	}
	if store == nil {
		t.Fatal("initS3Storage store = nil, want non-nil")
	}
}

func TestInitStorageManagerSkipsEmptyLegacyHotBackend(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Region = "eu"
	cfg.Storage.DefaultClass = "hot-s3-usa"
	cfg.Storage.Classes = map[string]config.StorageClassConfig{
		"hot-s3-usa": {
			Type:   "s3",
			Bucket: "sesamefs-usa",
			Region: "us-east-1",
		},
	}
	cfg.Storage.Backends = map[string]config.BackendConfig{
		"hot": {
			Type:   "s3",
			Bucket: "",
			Region: "",
		},
	}

	manager := initStorageManager(cfg)
	if _, ok := manager.GetBackend("hot"); ok {
		t.Fatal("legacy hot backend was registered with an empty bucket")
	}
	if _, ok := manager.GetBackend("hot-s3-usa"); !ok {
		t.Fatal("configured storage class hot-s3-usa was not registered")
	}
}

func TestInitStorageClassUsesResolvedConfigBucketAndCredentials(t *testing.T) {
	store, err := initStorageClass("hot-s3-na", config.StorageClassConfig{
		Type:      "s3",
		Bucket:    "sesamefs-na-prod",
		Region:    "us-east-1",
		AccessKey: "default-key",
		SecretKey: "default-secret",
		Tier:      "hot",
	})
	if err != nil {
		t.Fatalf("initStorageClass returned error: %v", err)
	}
	if store == nil {
		t.Fatal("initStorageClass store = nil, want non-nil")
	}
	if got := store.Bucket(); got != "sesamefs-na-prod" {
		t.Fatalf("store.Bucket() = %q, want %q", got, "sesamefs-na-prod")
	}
}

func TestInitStorageClassUsesResolvedConfigSpecificCredentials(t *testing.T) {
	store, err := initStorageClass("hot-s3-na", config.StorageClassConfig{
		Type:      "s3",
		Bucket:    "sesamefs-na-prod",
		Region:    "us-east-1",
		AccessKey: "class-key",
		SecretKey: "class-secret",
		Tier:      "hot",
	})
	if err != nil {
		t.Fatalf("initStorageClass returned error: %v", err)
	}
	if store == nil {
		t.Fatal("initStorageClass store = nil, want non-nil")
	}
}

func TestInitStorageClassRequiresBucketFromResolvedConfig(t *testing.T) {
	_, err := initStorageClass("hot-s3-na", config.StorageClassConfig{
		Type:   "s3",
		Region: "us-east-1",
		Tier:   "hot",
	})
	if err == nil {
		t.Fatal("initStorageClass error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "bucket is not configured") {
		t.Fatalf("initStorageClass error = %q, want missing bucket message", err.Error())
	}
}

func TestAuthPingRequiresAuthentication(t *testing.T) {
	t.Run("legacy api2 auth ping accepts authenticated request", func(t *testing.T) {
		s := createTestServer()
		s.router.GET("/api2/auth/ping", s.authMiddleware(), s.handlePing)

		req := httptest.NewRequest(http.MethodGet, "/api2/auth/ping", nil)
		req.Header.Set("Authorization", "Token test-token-123")
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
		if w.Body.String() != "pong" {
			t.Fatalf("body = %q, want %q", w.Body.String(), "pong")
		}
	})

	t.Run("api v2.1 auth ping accepts authenticated request", func(t *testing.T) {
		s := createTestServer()
		s.router.GET("/api/v2.1/auth/ping", s.authMiddleware(), s.handlePing)

		req := httptest.NewRequest(http.MethodGet, "/api/v2.1/auth/ping", nil)
		req.Header.Set("Authorization", "Token test-token-123")
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
		if w.Body.String() != "pong" {
			t.Fatalf("body = %q, want %q", w.Body.String(), "pong")
		}
	})

	t.Run("auth ping rejects unauthenticated request", func(t *testing.T) {
		s := createTestServer()
		s.router.GET("/api/v2.1/auth/ping", s.authMiddleware(), s.handlePing)

		req := httptest.NewRequest(http.MethodGet, "/api/v2.1/auth/ping", nil)
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusUnauthorized)
		}
	})
}

// TestHandleHealth tests the health endpoint (via health.Checker)
func TestHandleHealth(t *testing.T) {
	s := createTestServer()
	checker := health.NewChecker(nil, nil, 3*time.Second, "test")
	s.router.GET("/health", checker.HandleLiveness)

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

func TestRegisterCoreRoutes_InternalOnlyReadyAndMetrics(t *testing.T) {
	s := createTestServer()
	s.authRateLimiter = middleware.NewRateLimiter(rate.Every(time.Minute), 1)
	defer s.authRateLimiter.Stop()
	s.registerCoreRoutes()

	t.Run("external client cannot access ready", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/ready", nil)
		req.RemoteAddr = "198.51.100.9:12345"
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
		}
	})

	t.Run("loopback can access ready", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/ready", nil)
		req.RemoteAddr = "127.0.0.1:12345"
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})

	t.Run("external client cannot access metrics", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, s.config.Monitoring.MetricsPath, nil)
		req.RemoteAddr = "198.51.100.9:12345"
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
		}
	})

	t.Run("private client can access metrics", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, s.config.Monitoring.MetricsPath, nil)
		req.RemoteAddr = "10.1.2.3:12345"
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})
}

func TestRegisterCoreRoutes_ReadyUsesStorageManagerHealth(t *testing.T) {
	t.Run("loopback ready fails when storage manager has no healthy backend", func(t *testing.T) {
		s := createTestServer()
		s.config.Storage.Mode = "multi"
		s.storageManager = storage.NewManager()
		s.storageManager.SetDefaultClass("hot-s3-eu")
		s.authRateLimiter = middleware.NewRateLimiter(rate.Every(time.Minute), 1)
		defer s.authRateLimiter.Stop()
		s.registerCoreRoutes()

		req := httptest.NewRequest(http.MethodGet, "/ready", nil)
		req.RemoteAddr = "127.0.0.1:12345"
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)

		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
		}
	})

	t.Run("loopback ready succeeds when storage manager backend is healthy", func(t *testing.T) {
		s := createTestServer()
		s.config.Storage.Mode = "multi"
		s.storageManager = storage.NewManager()
		s.storageManager.SetDefaultClass("hot-s3-eu")
		s.storageManager.RegisterBackend("hot-s3-eu", &readinessStore{}, "")
		s.authRateLimiter = middleware.NewRateLimiter(rate.Every(time.Minute), 1)
		defer s.authRateLimiter.Stop()
		s.registerCoreRoutes()

		req := httptest.NewRequest(http.MethodGet, "/ready", nil)
		req.RemoteAddr = "127.0.0.1:12345"
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})

	t.Run("multi mode prefers storage manager over legacy singleton", func(t *testing.T) {
		s := createTestServer()
		s.config.Storage.Mode = "multi"
		s.storage = &storage.S3Store{}
		s.storageManager = storage.NewManager()
		s.storageManager.SetDefaultClass("hot-s3-eu")
		s.storageManager.RegisterBackend("hot-s3-eu", &readinessStore{}, "")
		s.authRateLimiter = middleware.NewRateLimiter(rate.Every(time.Minute), 1)
		defer s.authRateLimiter.Stop()
		s.registerCoreRoutes()

		req := httptest.NewRequest(http.MethodGet, "/ready", nil)
		req.RemoteAddr = "127.0.0.1:12345"
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
	})
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

func TestExtractRequestAuthToken(t *testing.T) {
	tests := []struct {
		name       string
		authHeader string
		cookie     string
		want       string
	}{
		{name: "token header", authHeader: "Token abc123", want: "abc123"},
		{name: "bearer header", authHeader: "Bearer abc123", want: "abc123"},
		{name: "plain cookie token", cookie: "abc123", want: "abc123"},
		{name: "email token cookie", cookie: "user@example.com@abc123", want: "abc123"},
		{name: "undefined token ignored", cookie: "undefined", want: ""},
		{name: "null token ignored", cookie: "null", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			if tt.cookie != "" {
				req.AddCookie(&http.Cookie{Name: "sesamefs_auth", Value: tt.cookie})
			}
			c.Request = req

			if got := extractRequestAuthToken(c); got != tt.want {
				t.Errorf("extractRequestAuthToken() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildCORSConfig(t *testing.T) {
	t.Run("dev mode allows all origins", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Auth.DevMode = true

		corsConfig := buildCORSConfig(cfg)
		if !corsConfig.AllowAllOrigins {
			t.Fatal("expected dev mode to allow all origins")
		}
	})

	t.Run("production uses configured allowlist", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Auth.DevMode = false
		cfg.CORS.AllowedOrigins = []string{" https://app.example.com ", "https://admin.example.com"}

		corsConfig := buildCORSConfig(cfg)
		if corsConfig.AllowAllOrigins {
			t.Fatal("expected production allowlist to disable allow-all")
		}
		if len(corsConfig.AllowOrigins) != 2 {
			t.Fatalf("AllowOrigins length = %d, want 2", len(corsConfig.AllowOrigins))
		}
		if corsConfig.AllowOrigins[0] != "https://app.example.com" {
			t.Fatalf("AllowOrigins[0] = %q, want %q", corsConfig.AllowOrigins[0], "https://app.example.com")
		}
		foundSessionHeader := false
		foundBlockHashHeader := false
		for _, h := range corsConfig.AllowHeaders {
			if h == "X-Block-Upload-Session" {
				foundSessionHeader = true
			}
			if h == "X-Block-Hash" {
				foundBlockHashHeader = true
			}
		}
		if !foundSessionHeader {
			t.Fatal("expected X-Block-Upload-Session to be allowed for cross-origin block uploads")
		}
		if !foundBlockHashHeader {
			t.Fatal("expected X-Block-Hash to be allowed for cross-origin block uploads")
		}
	})

	t.Run("production without allowlist fails closed", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Auth.DevMode = false
		cfg.CORS.AllowedOrigins = nil

		corsConfig := buildCORSConfig(cfg)
		if corsConfig.AllowAllOrigins {
			t.Fatal("expected production without allowlist to disable allow-all")
		}
		if corsConfig.AllowOriginFunc == nil {
			t.Fatal("expected fail-closed AllowOriginFunc when allowlist is missing")
		}
		if corsConfig.AllowOriginFunc("https://attacker.example.com") {
			t.Fatal("expected missing production allowlist to reject origins")
		}
	})
}

func TestConfigureTrustedProxies(t *testing.T) {
	t.Run("disabled by default uses direct peer IP", func(t *testing.T) {
		cfg := config.DefaultConfig()
		router := gin.New()
		if err := configureTrustedProxies(router, cfg); err != nil {
			t.Fatalf("configureTrustedProxies() error = %v", err)
		}

		router.GET("/ip", func(c *gin.Context) {
			c.String(http.StatusOK, c.ClientIP())
		})

		req := httptest.NewRequest(http.MethodGet, "/ip", nil)
		req.RemoteAddr = "10.1.2.3:12345"
		req.Header.Set("X-Forwarded-For", "198.51.100.9")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
		if body := strings.TrimSpace(w.Body.String()); body != "10.1.2.3" {
			t.Fatalf("ClientIP() = %q, want %q", body, "10.1.2.3")
		}
	})

	t.Run("trusted proxy honors forwarded client IP", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Server.TrustedProxies = []string{"10.0.0.0/8"}
		router := gin.New()
		if err := configureTrustedProxies(router, cfg); err != nil {
			t.Fatalf("configureTrustedProxies() error = %v", err)
		}

		router.GET("/ip", func(c *gin.Context) {
			c.String(http.StatusOK, c.ClientIP())
		})

		req := httptest.NewRequest(http.MethodGet, "/ip", nil)
		req.RemoteAddr = "10.1.2.3:12345"
		req.Header.Set("X-Forwarded-For", "198.51.100.9")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
		if body := strings.TrimSpace(w.Body.String()); body != "198.51.100.9" {
			t.Fatalf("ClientIP() = %q, want %q", body, "198.51.100.9")
		}
	})
}

// TestHandleAccountInfo tests the account info endpoint
func TestHandleAccountInfo(t *testing.T) {
	t.Skip("Requires database connection - run as integration test")
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

func TestHandleUpdateAccountInfoRejectsUnsupportedContactEmail(t *testing.T) {
	s := createTestServer()
	s.router.PUT("/api2/account/info/", func(c *gin.Context) {
		c.Set("user_id", "00000000-0000-0000-0000-000000000001")
		c.Set("org_id", "00000000-0000-0000-0000-000000000001")
		c.Next()
	}, s.handleUpdateAccountInfo)

	req, _ := http.NewRequest(http.MethodPut, "/api2/account/info/", strings.NewReader(`{"contact_email":"new@example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
	if !strings.Contains(w.Body.String(), "contact email updates are not supported") {
		t.Fatalf("body = %q, want unsupported contact email message", w.Body.String())
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

// TestAuthMiddlewareSetsRole tests that dev tokens with Role set the role in context
func TestAuthMiddlewareSetsRole(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Auth.DevMode = true
	cfg.Auth.DevTokens = []config.DevTokenEntry{
		{Token: "superadmin-token", UserID: "sa-user", OrgID: "platform-org", Role: "superadmin"},
		{Token: "no-role-token", UserID: "user-2", OrgID: "org-1"},
	}

	s := &Server{
		config: cfg,
		router: gin.New(),
	}

	var capturedRole string
	s.router.GET("/check-role", s.authMiddleware(), func(c *gin.Context) {
		capturedRole = c.GetString("role")
		c.Status(http.StatusOK)
	})

	t.Run("dev token with role sets role in context", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/check-role", nil)
		req.Header.Set("Authorization", "Token superadmin-token")
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
		if capturedRole != "superadmin" {
			t.Errorf("role = %q, want %q", capturedRole, "superadmin")
		}
	})

	t.Run("dev token without role has empty role in context", func(t *testing.T) {
		capturedRole = "" // Reset
		req, _ := http.NewRequest("GET", "/check-role", nil)
		req.Header.Set("Authorization", "Token no-role-token")
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
		}
		if capturedRole != "" {
			t.Errorf("role = %q, want empty string", capturedRole)
		}
	})
}

func TestResolveUserEmailDoesNotFallbackToFirstDevToken(t *testing.T) {
	s := createTestServer()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req, _ := http.NewRequest("GET", "/org/", nil)
	c.Request = req

	email := s.resolveUserEmail(c)
	if email != "" {
		t.Fatalf("resolveUserEmail() = %q, want empty string when no token is present", email)
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
	t.Skip("Requires database connection - run as integration test")
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

// ============================================================================
// Stub Handler Tests
// ============================================================================

func TestHandleEmptyActivities(t *testing.T) {
	s := createTestServer()
	s.router.GET("/api/v2.1/activities/", s.handleEmptyActivities)

	req, _ := http.NewRequest("GET", "/api/v2.1/activities/", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	events, ok := response["events"].([]interface{})
	if !ok {
		t.Fatal("events field not found or not array")
	}
	if len(events) != 0 {
		t.Errorf("events should be empty, got %d items", len(events))
	}
}

func TestHandleEmptyNotifications(t *testing.T) {
	s := createTestServer()
	s.router.GET("/api/v2.1/notifications/", s.handleEmptyNotifications)

	req, _ := http.NewRequest("GET", "/api/v2.1/notifications/", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	if response["unseen_count"] != float64(0) {
		t.Errorf("unseen_count = %v, want 0", response["unseen_count"])
	}
	notifs, ok := response["notification_list"].([]interface{})
	if !ok {
		t.Fatal("notification_list field not found")
	}
	if len(notifs) != 0 {
		t.Errorf("notification_list should be empty")
	}
}

func TestHandleEmptySharedRepos(t *testing.T) {
	s := createTestServer()
	s.router.GET("/api/v2.1/shared-repos/", s.handleEmptySharedRepos)

	req, _ := http.NewRequest("GET", "/api/v2.1/shared-repos/", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var response []interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	if len(response) != 0 {
		t.Errorf("should return empty array, got %d items", len(response))
	}
}

func TestHandleEmptyDevices(t *testing.T) {
	s := createTestServer()
	s.router.GET("/api2/devices/", s.handleEmptyDevices)

	req, _ := http.NewRequest("GET", "/api2/devices/", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var response []interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	if len(response) != 0 {
		t.Errorf("should return empty array, got %d items", len(response))
	}
}

func TestHandleEmptyWikis(t *testing.T) {
	s := createTestServer()
	s.router.GET("/api/v2.1/wikis/", s.handleEmptyWikis)

	req, _ := http.NewRequest("GET", "/api/v2.1/wikis/", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestCheckAccountStatus_NilDBIsNoOp(t *testing.T) {
	s := createTestServer()
	if err := s.checkAccountStatus("user-1", "org-1"); err != nil {
		t.Fatalf("checkAccountStatus() error = %v, want nil when db is unavailable", err)
	}
}

func TestHandleEmptyRepoTags(t *testing.T) {
	s := createTestServer()
	s.router.GET("/api/v2.1/repos/:repo_id/repo-tags/", s.handleEmptyRepoTags)

	req, _ := http.NewRequest("GET", "/api/v2.1/repos/test-repo/repo-tags/", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	tags, ok := response["repo_tags"].([]interface{})
	if !ok {
		t.Fatal("repo_tags field not found")
	}
	if len(tags) != 0 {
		t.Errorf("repo_tags should be empty")
	}
}

func TestHandleEmptyFolderShareInfo(t *testing.T) {
	s := createTestServer()
	s.router.GET("/api/v2.1/repo-folder-share-info/", s.handleEmptyFolderShareInfo)

	req, _ := http.NewRequest("GET", "/api/v2.1/repo-folder-share-info/", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	if _, ok := response["share_info_list"]; !ok {
		t.Error("share_info_list field not found")
	}
}

func TestHandleEmptyGroups(t *testing.T) {
	s := createTestServer()
	s.router.GET("/api/v2.1/groups/", s.handleEmptyGroups)

	req, _ := http.NewRequest("GET", "/api/v2.1/groups/", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleEmptySharedFolders(t *testing.T) {
	s := createTestServer()
	s.router.GET("/api/v2.1/shared-folders/", s.handleEmptySharedFolders)

	req, _ := http.NewRequest("GET", "/api/v2.1/shared-folders/", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

// TestHandleUserAvatar tests the avatar stub endpoint
func TestHandleUserAvatar(t *testing.T) {
	s := createTestServer()
	s.router.GET("/api2/avatars/user/:email/resized/:size/", s.handleUserAvatar)

	req, _ := http.NewRequest("GET", "/api2/avatars/user/user@test.com/resized/80/", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	if response["url"] == nil {
		t.Error("response should contain url field")
	}
}
