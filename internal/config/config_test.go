package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	// Create a temporary config file
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")

	configContent := `
server:
  port: ":9090"

database:
  hosts:
    - "localhost"
  keyspace: "test_keyspace"
  consistency: "ONE"

storage:
  default_class: "hot"
  backends:
    hot:
      type: "s3"
      bucket: "test-bucket"
      region: "us-east-1"

auth:
  dev_mode: true
  dev_tokens:
    - token: "test-token"
      user_id: "00000000-0000-0000-0000-000000000001"
      org_id: "00000000-0000-0000-0000-000000000001"

versioning:
  default_ttl_days: 30
  min_ttl_days: 7
`

	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	// Set CONFIG_PATH environment variable
	os.Setenv("CONFIG_PATH", configPath)
	defer os.Unsetenv("CONFIG_PATH")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Verify server config
	if cfg.Server.Port != ":9090" {
		t.Errorf("Server.Port = %s, want :9090", cfg.Server.Port)
	}

	// Verify database config
	if len(cfg.Database.Hosts) != 1 || cfg.Database.Hosts[0] != "localhost" {
		t.Errorf("Database.Hosts = %v, want [localhost]", cfg.Database.Hosts)
	}
	if cfg.Database.Keyspace != "test_keyspace" {
		t.Errorf("Database.Keyspace = %s, want test_keyspace", cfg.Database.Keyspace)
	}

	// Verify storage config
	if cfg.Storage.DefaultClass != "hot" {
		t.Errorf("Storage.DefaultClass = %s, want hot", cfg.Storage.DefaultClass)
	}
	if _, ok := cfg.Storage.Backends["hot"]; !ok {
		t.Error("Storage.Backends[hot] not found")
	}

	// Verify auth config
	if !cfg.Auth.DevMode {
		t.Error("Auth.DevMode should be true")
	}
	if len(cfg.Auth.DevTokens) == 0 {
		t.Error("Auth.DevTokens should not be empty")
	}
	if cfg.Auth.DevTokens[0].Token != "test-token" {
		t.Errorf("Auth.DevTokens[0].Token = %s, want test-token", cfg.Auth.DevTokens[0].Token)
	}

	// Verify versioning config
	if cfg.Versioning.DefaultTTLDays != 30 {
		t.Errorf("Versioning.DefaultTTLDays = %d, want 30", cfg.Versioning.DefaultTTLDays)
	}
	if cfg.Versioning.MinTTLDays != 7 {
		t.Errorf("Versioning.MinTTLDays = %d, want 7", cfg.Versioning.MinTTLDays)
	}
}

func TestLoadWithEnvOverrides(t *testing.T) {
	// Create a minimal config file
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")

	configContent := `
server:
  port: ":8080"

database:
  hosts:
    - "localhost"
  keyspace: "sesamefs"

storage:
  default_class: "hot"
  backends:
    hot:
      type: "s3"
      bucket: "test"
      region: "us-east-1"

auth:
  dev_mode: false

versioning:
  default_ttl_days: 90
  min_ttl_days: 7
`

	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	// Set environment variables
	os.Setenv("CONFIG_PATH", configPath)
	os.Setenv("SERVER_PORT", ":9999")
	os.Setenv("AUTH_DEV_MODE", "true")
	defer func() {
		os.Unsetenv("CONFIG_PATH")
		os.Unsetenv("SERVER_PORT")
		os.Unsetenv("AUTH_DEV_MODE")
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Verify env override
	if cfg.Server.Port != ":9999" {
		t.Errorf("Server.Port = %s, want :9999 (from env)", cfg.Server.Port)
	}
	if !cfg.Auth.DevMode {
		t.Error("Auth.DevMode should be true (from env)")
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Server.Port != ":8080" {
		t.Errorf("Server.Port = %s, want :8080", cfg.Server.Port)
	}
	if cfg.Database.Keyspace != "sesamefs" {
		t.Errorf("Database.Keyspace = %s, want sesamefs", cfg.Database.Keyspace)
	}
	if cfg.Storage.DefaultClass != "hot" {
		t.Errorf("Storage.DefaultClass = %s, want hot", cfg.Storage.DefaultClass)
	}
	if cfg.Chunking.Algorithm != "fastcdc" {
		t.Errorf("Chunking.Algorithm = %s, want fastcdc", cfg.Chunking.Algorithm)
	}
	if len(cfg.Server.TrustedProxies) != 0 {
		t.Errorf("Server.TrustedProxies = %v, want empty by default", cfg.Server.TrustedProxies)
	}
	if cfg.Versioning.DefaultTTLDays != 90 {
		t.Errorf("Versioning.DefaultTTLDays = %d, want 90", cfg.Versioning.DefaultTTLDays)
	}
	if cfg.SeafHTTP.ZipMaxEntries != 100000 {
		t.Errorf("SeafHTTP.ZipMaxEntries = %d, want 100000", cfg.SeafHTTP.ZipMaxEntries)
	}
	if cfg.SeafHTTP.ZipMaxDepth != 64 {
		t.Errorf("SeafHTTP.ZipMaxDepth = %d, want 64", cfg.SeafHTTP.ZipMaxDepth)
	}
	if cfg.SeafHTTP.ZipMaxBytes != 10*1024*1024*1024 {
		t.Errorf("SeafHTTP.ZipMaxBytes = %d, want %d", cfg.SeafHTTP.ZipMaxBytes, int64(10*1024*1024*1024))
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name           string
		modify         func(*Config)
		wantErr        bool
		wantErrContain string
	}{
		{
			name:    "valid config",
			modify:  func(c *Config) {},
			wantErr: false,
		},
		{
			name: "valid production config",
			modify: func(c *Config) {
				c.Auth.DevMode = false
				c.Auth.ShareLinkHMACKey = "very-secure-test-key"
				c.CORS.AllowedOrigins = []string{"https://app.example.com"}
			},
			wantErr: false,
		},
		{
			name: "empty port",
			modify: func(c *Config) {
				c.Server.Port = ""
			},
			wantErr: true,
		},
		{
			name: "empty database hosts",
			modify: func(c *Config) {
				c.Database.Hosts = []string{}
			},
			wantErr: true,
		},
		{
			name: "empty keyspace",
			modify: func(c *Config) {
				c.Database.Keyspace = ""
			},
			wantErr: true,
		},
		{
			name: "production requires cors allowlist",
			modify: func(c *Config) {
				c.Auth.DevMode = false
				c.Auth.ShareLinkHMACKey = "very-secure-test-key"
				c.CORS.AllowedOrigins = nil
			},
			wantErr:        true,
			wantErrContain: "cors.allowed_origins",
		},
		{
			name: "oidc requires redirect allowlist",
			modify: func(c *Config) {
				c.Auth.OIDC.Enabled = true
				c.Auth.OIDC.RedirectURIs = nil
			},
			wantErr:        true,
			wantErrContain: "auth.oidc.redirect_uris",
		},
		{
			name: "preview extensions are normalized",
			modify: func(c *Config) {
				c.FileView.PreviewExtensions = []string{" MD ", "png", "md"}
			},
			wantErr: false,
		},
		{
			name: "preview extensions reject unsupported values",
			modify: func(c *Config) {
				c.FileView.PreviewExtensions = []string{"exe"}
			},
			wantErr:        true,
			wantErrContain: "unsupported extension",
		},
		{
			name: "trusted proxies are normalized",
			modify: func(c *Config) {
				c.Server.TrustedProxies = []string{" 10.0.0.0/8 ", "192.168.1.10", "10.0.0.0/8", ""}
			},
			wantErr: false,
		},
		{
			name: "trusted proxies reject invalid values",
			modify: func(c *Config) {
				c.Server.TrustedProxies = []string{"not-a-cidr"}
			},
			wantErr:        true,
			wantErrContain: "server.trusted_proxies",
		},
		{
			name: "onlyoffice max document bytes must be positive",
			modify: func(c *Config) {
				c.OnlyOffice.MaxDocumentBytes = 0
			},
			wantErr:        true,
			wantErrContain: "onlyoffice.max_document_bytes",
		},
		{
			name: "onlyoffice enabled requires jwt secret",
			modify: func(c *Config) {
				c.OnlyOffice.Enabled = true
				c.OnlyOffice.JWTSecret = ""
			},
			wantErr:        true,
			wantErrContain: "onlyoffice.jwt_secret",
		},
		{
			name: "zip max entries must be positive",
			modify: func(c *Config) {
				c.SeafHTTP.ZipMaxEntries = 0
			},
			wantErr:        true,
			wantErrContain: "seafhttp.zip_max_entries",
		},
		{
			name: "zip max depth must be bounded",
			modify: func(c *Config) {
				c.SeafHTTP.ZipMaxDepth = 257
			},
			wantErr:        true,
			wantErrContain: "seafhttp.zip_max_depth",
		},
		{
			name: "zip max bytes must be bounded",
			modify: func(c *Config) {
				c.SeafHTTP.ZipMaxBytes = 101 * 1024 * 1024 * 1024
			},
			wantErr:        true,
			wantErrContain: "seafhttp.zip_max_bytes",
		},
		{
			name: "storage backend rejects unsupported sse mode",
			modify: func(c *Config) {
				hot := c.Storage.Backends["hot"]
				hot.ServerSideEncryption = "bad-mode"
				c.Storage.Backends["hot"] = hot
			},
			wantErr:        true,
			wantErrContain: "server_side_encryption",
		},
		{
			name: "storage backend rejects kms key without aws kms mode",
			modify: func(c *Config) {
				hot := c.Storage.Backends["hot"]
				hot.ServerSideEncryption = "AES256"
				hot.SSEKMSKeyID = "arn:aws:kms:us-east-1:123456789012:key/test"
				c.Storage.Backends["hot"] = hot
			},
			wantErr:        true,
			wantErrContain: "sse_kms_key_id",
		},
		{
			name: "storage backend accepts aws kms mode with key",
			modify: func(c *Config) {
				hot := c.Storage.Backends["hot"]
				hot.ServerSideEncryption = "aws:kms"
				hot.SSEKMSKeyID = "arn:aws:kms:us-east-1:123456789012:key/test"
				c.Storage.Backends["hot"] = hot
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Auth.DevMode = true // default to dev so test cases only assert the dimension they care about
			tt.modify(cfg)

			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErrContain != "" && err != nil && !strings.Contains(err.Error(), tt.wantErrContain) {
				t.Fatalf("Validate() error = %q, want substring %q", err.Error(), tt.wantErrContain)
			}
			if tt.name == "preview extensions are normalized" {
				got := strings.Join(cfg.FileView.PreviewExtensions, ",")
				if got != "md,png" {
					t.Fatalf("normalized preview extensions = %q, want %q", got, "md,png")
				}
			}
			if tt.name == "trusted proxies are normalized" {
				got := strings.Join(cfg.Server.TrustedProxies, ",")
				if got != "10.0.0.0/8,192.168.1.10" {
					t.Fatalf("normalized trusted proxies = %q, want %q", got, "10.0.0.0/8,192.168.1.10")
				}
			}
		})
	}
}

func TestEnvOverrideServerTrustedProxies(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Auth.DevMode = true

	os.Setenv("SERVER_TRUSTED_PROXIES", "10.0.0.0/8, 192.168.1.10 ")
	defer os.Unsetenv("SERVER_TRUSTED_PROXIES")

	cfg.applyEnvOverrides()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	if len(cfg.Server.TrustedProxies) != 2 {
		t.Fatalf("Server.TrustedProxies length = %d, want 2", len(cfg.Server.TrustedProxies))
	}
	if cfg.Server.TrustedProxies[0] != "10.0.0.0/8" {
		t.Fatalf("Server.TrustedProxies[0] = %q, want %q", cfg.Server.TrustedProxies[0], "10.0.0.0/8")
	}
	if cfg.Server.TrustedProxies[1] != "192.168.1.10" {
		t.Fatalf("Server.TrustedProxies[1] = %q, want %q", cfg.Server.TrustedProxies[1], "192.168.1.10")
	}
}

func TestEnvOverrideOnlyOfficeMaxDocumentBytes(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Auth.DevMode = true

	os.Setenv("ONLYOFFICE_MAX_DOCUMENT_BYTES", "1048576")
	defer os.Unsetenv("ONLYOFFICE_MAX_DOCUMENT_BYTES")

	cfg.applyEnvOverrides()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	if cfg.OnlyOffice.MaxDocumentBytes != 1048576 {
		t.Fatalf("OnlyOffice.MaxDocumentBytes = %d, want %d", cfg.OnlyOffice.MaxDocumentBytes, int64(1048576))
	}
}

// TestEnvOverridePORT tests the PORT env var (without colon prefix)
func TestEnvOverridePORT(t *testing.T) {
	cfg := DefaultConfig()
	os.Setenv("PORT", "3000")
	defer os.Unsetenv("PORT")

	cfg.applyEnvOverrides()

	if cfg.Server.Port != ":3000" {
		t.Errorf("Server.Port = %s, want :3000", cfg.Server.Port)
	}
}

// TestEnvOverrideCassandra tests Cassandra-related env vars
func TestEnvOverrideCassandra(t *testing.T) {
	cfg := DefaultConfig()

	os.Setenv("CASSANDRA_HOSTS", "cassandra1.example.com")
	os.Setenv("CASSANDRA_KEYSPACE", "test_ks")
	os.Setenv("CASSANDRA_USERNAME", "test_user")
	os.Setenv("CASSANDRA_PASSWORD", "test_pass")
	os.Setenv("CASSANDRA_LOCAL_DC", "dc2")
	defer func() {
		os.Unsetenv("CASSANDRA_HOSTS")
		os.Unsetenv("CASSANDRA_KEYSPACE")
		os.Unsetenv("CASSANDRA_USERNAME")
		os.Unsetenv("CASSANDRA_PASSWORD")
		os.Unsetenv("CASSANDRA_LOCAL_DC")
	}()

	cfg.applyEnvOverrides()

	if len(cfg.Database.Hosts) != 1 || cfg.Database.Hosts[0] != "cassandra1.example.com" {
		t.Errorf("Database.Hosts = %v, want [cassandra1.example.com]", cfg.Database.Hosts)
	}
	if cfg.Database.Keyspace != "test_ks" {
		t.Errorf("Database.Keyspace = %s, want test_ks", cfg.Database.Keyspace)
	}
	if cfg.Database.Username != "test_user" {
		t.Errorf("Database.Username = %s, want test_user", cfg.Database.Username)
	}
	if cfg.Database.Password != "test_pass" {
		t.Errorf("Database.Password = %s, want test_pass", cfg.Database.Password)
	}
	if cfg.Database.LocalDC != "dc2" {
		t.Errorf("Database.LocalDC = %s, want dc2", cfg.Database.LocalDC)
	}
}

// TestEnvOverrideCassandraMultiHost tests CASSANDRA_HOSTS with comma-separated hosts
func TestEnvOverrideCassandraMultiHost(t *testing.T) {
	cfg := DefaultConfig()

	os.Setenv("CASSANDRA_HOSTS", "10.0.1.10:9042,10.0.2.20:9042,10.0.3.30:9042")
	defer os.Unsetenv("CASSANDRA_HOSTS")

	cfg.applyEnvOverrides()

	expected := []string{"10.0.1.10:9042", "10.0.2.20:9042", "10.0.3.30:9042"}
	if len(cfg.Database.Hosts) != len(expected) {
		t.Fatalf("Database.Hosts length = %d, want %d", len(cfg.Database.Hosts), len(expected))
	}
	for i, h := range expected {
		if cfg.Database.Hosts[i] != h {
			t.Errorf("Database.Hosts[%d] = %s, want %s", i, cfg.Database.Hosts[i], h)
		}
	}
}

// TestEnvOverrideS3 tests S3-related env vars
func TestEnvOverrideS3(t *testing.T) {
	cfg := DefaultConfig()

	os.Setenv("S3_BUCKET", "my-bucket")
	os.Setenv("S3_REGION", "eu-west-1")
	os.Setenv("S3_ENDPOINT", "http://localhost:9000")
	os.Setenv("S3_SERVER_SIDE_ENCRYPTION", "AES256")
	defer func() {
		os.Unsetenv("S3_BUCKET")
		os.Unsetenv("S3_REGION")
		os.Unsetenv("S3_ENDPOINT")
		os.Unsetenv("S3_SERVER_SIDE_ENCRYPTION")
	}()

	cfg.applyEnvOverrides()

	hot := cfg.Storage.Backends["hot"]
	if hot.Bucket != "my-bucket" {
		t.Errorf("Storage.Backends[hot].Bucket = %s, want my-bucket", hot.Bucket)
	}
	if hot.Region != "eu-west-1" {
		t.Errorf("Storage.Backends[hot].Region = %s, want eu-west-1", hot.Region)
	}
	if hot.Endpoint != "http://localhost:9000" {
		t.Errorf("Storage.Backends[hot].Endpoint = %s, want http://localhost:9000", hot.Endpoint)
	}
	if hot.ServerSideEncryption != "AES256" {
		t.Errorf("Storage.Backends[hot].ServerSideEncryption = %s, want AES256", hot.ServerSideEncryption)
	}
}

func TestEnvOverrideAccounts(t *testing.T) {
	cfg := DefaultConfig()

	os.Setenv("ACCOUNTS_DELETE_ACCOUNT_URL", "https://accounts.example.com/accounts/delete/")
	os.Setenv("ACCOUNTS_ORG_USER_MANAGEMENT_URL", "https://accounts.example.com/orgs/{org_id}/users/")
	os.Setenv("ACCOUNTS_DISABLE_ORG_USER_WRITES", "true")
	defer func() {
		os.Unsetenv("ACCOUNTS_DELETE_ACCOUNT_URL")
		os.Unsetenv("ACCOUNTS_ORG_USER_MANAGEMENT_URL")
		os.Unsetenv("ACCOUNTS_DISABLE_ORG_USER_WRITES")
	}()

	cfg.applyEnvOverrides()

	if cfg.Accounts.DeleteAccountURL != "https://accounts.example.com/accounts/delete/" {
		t.Errorf("Accounts.DeleteAccountURL = %s, want https://accounts.example.com/accounts/delete/", cfg.Accounts.DeleteAccountURL)
	}
	if cfg.Accounts.OrgUserManagementURL != "https://accounts.example.com/orgs/{org_id}/users/" {
		t.Errorf("Accounts.OrgUserManagementURL = %s, want https://accounts.example.com/orgs/{org_id}/users/", cfg.Accounts.OrgUserManagementURL)
	}
	if !cfg.Accounts.DisableOrgUserWrites {
		t.Fatalf("Accounts.DisableOrgUserWrites = false, want true")
	}
}

func TestDefaultAccountsConfig(t *testing.T) {
	cfg := DefaultConfig()

	if !cfg.Accounts.DisableOrgUserWrites {
		t.Fatalf("Accounts.DisableOrgUserWrites = false, want true by default")
	}
}

// TestEnvOverrideOIDC tests OIDC-related env vars
func TestEnvOverrideOIDC(t *testing.T) {
	cfg := DefaultConfig()

	os.Setenv("OIDC_ISSUER", "https://auth.example.com")
	os.Setenv("OIDC_CLIENT_ID", "my-client")
	os.Setenv("OIDC_CLIENT_SECRET", "secret123")
	defer func() {
		os.Unsetenv("OIDC_ISSUER")
		os.Unsetenv("OIDC_CLIENT_ID")
		os.Unsetenv("OIDC_CLIENT_SECRET")
	}()

	cfg.applyEnvOverrides()

	if cfg.Auth.OIDC.Issuer != "https://auth.example.com" {
		t.Errorf("Auth.OIDC.Issuer = %s, want https://auth.example.com", cfg.Auth.OIDC.Issuer)
	}
	if cfg.Auth.OIDC.ClientID != "my-client" {
		t.Errorf("Auth.OIDC.ClientID = %s, want my-client", cfg.Auth.OIDC.ClientID)
	}
	if cfg.Auth.OIDC.ClientSecret != "secret123" {
		t.Errorf("Auth.OIDC.ClientSecret = %s, want secret123", cfg.Auth.OIDC.ClientSecret)
	}
}

// TestEnvOverrideSeafHTTP tests SeafHTTP-related env vars
func TestEnvOverrideSeafHTTP(t *testing.T) {
	cfg := DefaultConfig()

	os.Setenv("SEAFHTTP_TOKEN_TTL", "2h")
	os.Setenv("SEAFHTTP_ZIP_MAX_ENTRIES", "12345")
	os.Setenv("SEAFHTTP_ZIP_MAX_DEPTH", "32")
	os.Setenv("SEAFHTTP_ZIP_MAX_BYTES", "4294967296")
	defer os.Unsetenv("SEAFHTTP_TOKEN_TTL")
	defer os.Unsetenv("SEAFHTTP_ZIP_MAX_ENTRIES")
	defer os.Unsetenv("SEAFHTTP_ZIP_MAX_DEPTH")
	defer os.Unsetenv("SEAFHTTP_ZIP_MAX_BYTES")

	cfg.applyEnvOverrides()

	// 2 hours = 7200 seconds
	if cfg.SeafHTTP.TokenTTL.Hours() != 2 {
		t.Errorf("SeafHTTP.TokenTTL = %v, want 2h", cfg.SeafHTTP.TokenTTL)
	}
	if cfg.SeafHTTP.ZipMaxEntries != 12345 {
		t.Errorf("SeafHTTP.ZipMaxEntries = %d, want 12345", cfg.SeafHTTP.ZipMaxEntries)
	}
	if cfg.SeafHTTP.ZipMaxDepth != 32 {
		t.Errorf("SeafHTTP.ZipMaxDepth = %d, want 32", cfg.SeafHTTP.ZipMaxDepth)
	}
	if cfg.SeafHTTP.ZipMaxBytes != 4294967296 {
		t.Errorf("SeafHTTP.ZipMaxBytes = %d, want 4294967296", cfg.SeafHTTP.ZipMaxBytes)
	}
}

// TestEnvOverrideSeafHTTPInvalid tests invalid duration is ignored
func TestEnvOverrideSeafHTTPInvalid(t *testing.T) {
	cfg := DefaultConfig()
	originalTTL := cfg.SeafHTTP.TokenTTL
	originalEntries := cfg.SeafHTTP.ZipMaxEntries
	originalDepth := cfg.SeafHTTP.ZipMaxDepth
	originalBytes := cfg.SeafHTTP.ZipMaxBytes

	os.Setenv("SEAFHTTP_TOKEN_TTL", "invalid")
	os.Setenv("SEAFHTTP_ZIP_MAX_ENTRIES", "invalid")
	os.Setenv("SEAFHTTP_ZIP_MAX_DEPTH", "invalid")
	os.Setenv("SEAFHTTP_ZIP_MAX_BYTES", "invalid")
	defer os.Unsetenv("SEAFHTTP_TOKEN_TTL")
	defer os.Unsetenv("SEAFHTTP_ZIP_MAX_ENTRIES")
	defer os.Unsetenv("SEAFHTTP_ZIP_MAX_DEPTH")
	defer os.Unsetenv("SEAFHTTP_ZIP_MAX_BYTES")

	cfg.applyEnvOverrides()

	// Should keep default since parse failed
	if cfg.SeafHTTP.TokenTTL != originalTTL {
		t.Errorf("SeafHTTP.TokenTTL = %v, want %v (unchanged)", cfg.SeafHTTP.TokenTTL, originalTTL)
	}
	if cfg.SeafHTTP.ZipMaxEntries != originalEntries {
		t.Errorf("SeafHTTP.ZipMaxEntries = %d, want %d (unchanged)", cfg.SeafHTTP.ZipMaxEntries, originalEntries)
	}
	if cfg.SeafHTTP.ZipMaxDepth != originalDepth {
		t.Errorf("SeafHTTP.ZipMaxDepth = %d, want %d (unchanged)", cfg.SeafHTTP.ZipMaxDepth, originalDepth)
	}
	if cfg.SeafHTTP.ZipMaxBytes != originalBytes {
		t.Errorf("SeafHTTP.ZipMaxBytes = %d, want %d (unchanged)", cfg.SeafHTTP.ZipMaxBytes, originalBytes)
	}
}

// TestEnvOverrideAuthDevMode tests various AUTH_DEV_MODE values
func TestEnvOverrideAuthDevMode(t *testing.T) {
	tests := []struct {
		value    string
		expected bool
	}{
		{"true", true},
		{"1", true},
		{"false", false},
		{"0", false},
		{"yes", false}, // only "true" or "1" are accepted
	}

	for _, tt := range tests {
		t.Run("value="+tt.value, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Auth.DevMode = false // start false

			os.Setenv("AUTH_DEV_MODE", tt.value)
			defer os.Unsetenv("AUTH_DEV_MODE")

			cfg.applyEnvOverrides()

			if cfg.Auth.DevMode != tt.expected {
				t.Errorf("Auth.DevMode = %v, want %v", cfg.Auth.DevMode, tt.expected)
			}
		})
	}
}

// TestEnvOverrideGCEnabled tests various GC_ENABLED values.
func TestEnvOverrideGCEnabled(t *testing.T) {
	tests := []struct {
		value    string
		expected bool
	}{
		{"true", true},
		{"1", true},
		{"false", false},
		{"0", false},
		{"yes", false},
	}

	for _, tt := range tests {
		t.Run("value="+tt.value, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.GC.Enabled = false

			os.Setenv("GC_ENABLED", tt.value)
			defer os.Unsetenv("GC_ENABLED")

			cfg.applyEnvOverrides()

			if cfg.GC.Enabled != tt.expected {
				t.Errorf("GC.Enabled = %v, want %v", cfg.GC.Enabled, tt.expected)
			}
		})
	}
}

func TestDefaultConfig_GCDisabledByDefault(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.GC.Enabled {
		t.Error("DefaultConfig().GC.Enabled should be false; GC must be enabled explicitly")
	}
}

func TestEnvOverrideGCReconcileBatchSize(t *testing.T) {
	cfg := DefaultConfig()
	cfg.GC.ReconcileBatchSize = 256

	os.Setenv("GC_RECONCILE_BATCH_SIZE", "17")
	defer os.Unsetenv("GC_RECONCILE_BATCH_SIZE")

	cfg.applyEnvOverrides()

	if cfg.GC.ReconcileBatchSize != 17 {
		t.Fatalf("GC.ReconcileBatchSize = %d, want 17", cfg.GC.ReconcileBatchSize)
	}
}

func TestEnvOverrideGCFailedItemsPageSize(t *testing.T) {
	cfg := DefaultConfig()
	cfg.GC.FailedItemsPageSize = 100

	os.Setenv("GC_FAILED_ITEMS_PAGE_SIZE", "33")
	defer os.Unsetenv("GC_FAILED_ITEMS_PAGE_SIZE")

	cfg.applyEnvOverrides()

	if cfg.GC.FailedItemsPageSize != 33 {
		t.Fatalf("GC.FailedItemsPageSize = %d, want 33", cfg.GC.FailedItemsPageSize)
	}
}

// TestEnvOverridePriority tests that SERVER_PORT takes priority over PORT
func TestEnvOverridePriority(t *testing.T) {
	cfg := DefaultConfig()

	os.Setenv("PORT", "3000")
	os.Setenv("SERVER_PORT", ":4000")
	defer func() {
		os.Unsetenv("PORT")
		os.Unsetenv("SERVER_PORT")
	}()

	cfg.applyEnvOverrides()

	// SERVER_PORT is applied after PORT, so it wins
	if cfg.Server.Port != ":4000" {
		t.Errorf("Server.Port = %s, want :4000", cfg.Server.Port)
	}
}

// TestGetEnvFallback tests the getEnv helper function
func TestGetEnvFallback(t *testing.T) {
	// Ensure env var is not set
	os.Unsetenv("TEST_GETENV_VAR")

	result := getEnv("TEST_GETENV_VAR", "default_value")
	if result != "default_value" {
		t.Errorf("getEnv returned %s, want default_value", result)
	}

	// Now set it
	os.Setenv("TEST_GETENV_VAR", "actual_value")
	defer os.Unsetenv("TEST_GETENV_VAR")

	result = getEnv("TEST_GETENV_VAR", "default_value")
	if result != "actual_value" {
		t.Errorf("getEnv returned %s, want actual_value", result)
	}
}

// TestGetEnvIntFallback tests the getEnvInt helper function
func TestGetEnvIntFallback(t *testing.T) {
	// Ensure env var is not set
	os.Unsetenv("TEST_GETENV_INT")

	result := getEnvInt("TEST_GETENV_INT", 42)
	if result != 42 {
		t.Errorf("getEnvInt returned %d, want 42", result)
	}

	// Set to valid int
	os.Setenv("TEST_GETENV_INT", "100")
	defer os.Unsetenv("TEST_GETENV_INT")

	result = getEnvInt("TEST_GETENV_INT", 42)
	if result != 100 {
		t.Errorf("getEnvInt returned %d, want 100", result)
	}
}

// TestGetEnvIntInvalid tests getEnvInt with invalid value
func TestGetEnvIntInvalid(t *testing.T) {
	os.Setenv("TEST_GETENV_INT_INVALID", "not_a_number")
	defer os.Unsetenv("TEST_GETENV_INT_INVALID")

	result := getEnvInt("TEST_GETENV_INT_INVALID", 42)
	if result != 42 {
		t.Errorf("getEnvInt returned %d, want 42 (default on parse error)", result)
	}
}

// --- Enforcement Profile tests ---

func TestDefaultHardProfile(t *testing.T) {
	p := DefaultHardProfile()

	// Hard profile restricts groups, guests, publish, global address book
	if p.Features.CanAddGroup {
		t.Error("hard profile: CanAddGroup should be false")
	}
	if p.Features.CanPublishRepo {
		t.Error("hard profile: CanPublishRepo should be false")
	}
	if p.Features.CanUseGlobalAddressBook {
		t.Error("hard profile: CanUseGlobalAddressBook should be false")
	}
	if p.Features.CanSendShareLinkMail {
		t.Error("hard profile: CanSendShareLinkMail should be false")
	}

	// Hard profile allows basic features
	if !p.Features.CanAddRepo {
		t.Error("hard profile: CanAddRepo should be true")
	}
	if !p.Features.CanShareRepo {
		t.Error("hard profile: CanShareRepo should be true")
	}
	if !p.Features.CanGenerateShareLink {
		t.Error("hard profile: CanGenerateShareLink should be true")
	}
	if !p.Features.CanConnectWithDesktopClients {
		t.Error("hard profile: CanConnectWithDesktopClients should be true")
	}

	// Hard profile has strict numeric limits
	if p.Limits.MaxLibraries != 3 {
		t.Errorf("hard profile: MaxLibraries = %d, want 3", p.Limits.MaxLibraries)
	}
	if p.Limits.MaxShareLinks != 3 {
		t.Errorf("hard profile: MaxShareLinks = %d, want 3", p.Limits.MaxShareLinks)
	}
	if p.Limits.MaxUploadLinks != 1 {
		t.Errorf("hard profile: MaxUploadLinks = %d, want 1", p.Limits.MaxUploadLinks)
	}
	if p.Limits.ShareLinkExpireDaysMax != 3 {
		t.Errorf("hard profile: ShareLinkExpireDaysMax = %d, want 3", p.Limits.ShareLinkExpireDaysMax)
	}
	if p.Limits.UploadLinkExpireDaysMax != 3 {
		t.Errorf("hard profile: UploadLinkExpireDaysMax = %d, want 3", p.Limits.UploadLinkExpireDaysMax)
	}
}

func TestDefaultSoftProfile(t *testing.T) {
	p := DefaultSoftProfile()

	// Soft profile enables all features
	if !p.Features.CanAddGroup {
		t.Error("soft profile: CanAddGroup should be true")
	}
	if !p.Features.CanPublishRepo {
		t.Error("soft profile: CanPublishRepo should be true")
	}
	if !p.Features.CanUseGlobalAddressBook {
		t.Error("soft profile: CanUseGlobalAddressBook should be true")
	}
	if !p.Features.CanSendShareLinkMail {
		t.Error("soft profile: CanSendShareLinkMail should be true")
	}

	// Soft profile has unlimited numeric limits (-1 or 0)
	if p.Limits.MaxLibraries != -1 {
		t.Errorf("soft profile: MaxLibraries = %d, want -1", p.Limits.MaxLibraries)
	}
	if p.Limits.MaxShareLinks != -1 {
		t.Errorf("soft profile: MaxShareLinks = %d, want -1", p.Limits.MaxShareLinks)
	}
	if p.Limits.MaxUploadLinks != -1 {
		t.Errorf("soft profile: MaxUploadLinks = %d, want -1", p.Limits.MaxUploadLinks)
	}
	if p.Limits.ShareLinkExpireDaysMax != 0 {
		t.Errorf("soft profile: ShareLinkExpireDaysMax = %d, want 0", p.Limits.ShareLinkExpireDaysMax)
	}
	if p.Limits.UploadLinkExpireDaysMax != 0 {
		t.Errorf("soft profile: UploadLinkExpireDaysMax = %d, want 0", p.Limits.UploadLinkExpireDaysMax)
	}
}

func TestGetEnforcementProfile(t *testing.T) {
	cfg := DefaultConfig()

	// "hard" returns hard profile
	hard := cfg.GetEnforcementProfile("hard")
	if hard.Features.CanAddGroup {
		t.Error("GetEnforcementProfile(hard): CanAddGroup should be false")
	}
	if hard.Limits.MaxLibraries != 3 {
		t.Errorf("GetEnforcementProfile(hard): MaxLibraries = %d, want 3", hard.Limits.MaxLibraries)
	}

	// "soft" returns soft profile
	soft := cfg.GetEnforcementProfile("soft")
	if !soft.Features.CanAddGroup {
		t.Error("GetEnforcementProfile(soft): CanAddGroup should be true")
	}
	if soft.Limits.MaxLibraries != -1 {
		t.Errorf("GetEnforcementProfile(soft): MaxLibraries = %d, want -1", soft.Limits.MaxLibraries)
	}

	// "" defaults to hard
	empty := cfg.GetEnforcementProfile("")
	if empty.Features.CanAddGroup {
		t.Error("GetEnforcementProfile(''): should default to hard, CanAddGroup should be false")
	}

	// unknown key falls back to hard defaults
	unknown := cfg.GetEnforcementProfile("unknown_policy")
	if unknown.Features.CanAddGroup {
		t.Error("GetEnforcementProfile(unknown): should fall back to hard defaults")
	}
}

func TestDefaultConfigHasEnforcementProfiles(t *testing.T) {
	cfg := DefaultConfig()

	if len(cfg.EnforcementProfiles) != 2 {
		t.Fatalf("EnforcementProfiles count = %d, want 2", len(cfg.EnforcementProfiles))
	}
	if _, ok := cfg.EnforcementProfiles["hard"]; !ok {
		t.Error("EnforcementProfiles missing 'hard' key")
	}
	if _, ok := cfg.EnforcementProfiles["soft"]; !ok {
		t.Error("EnforcementProfiles missing 'soft' key")
	}
}

// TestS3OverrideNoHotBackend tests S3 env vars when "hot" backend doesn't exist
func TestS3OverrideNoHotBackend(t *testing.T) {
	cfg := DefaultConfig()
	delete(cfg.Storage.Backends, "hot")

	os.Setenv("S3_BUCKET", "my-bucket")
	defer os.Unsetenv("S3_BUCKET")

	// Should not panic
	cfg.applyEnvOverrides()

	// Verify "hot" was not created
	if _, ok := cfg.Storage.Backends["hot"]; ok {
		t.Error("S3_BUCKET should not create 'hot' backend if it doesn't exist")
	}
}
