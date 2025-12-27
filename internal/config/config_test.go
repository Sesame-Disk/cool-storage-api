package config

import (
	"os"
	"path/filepath"
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
	if cfg.Versioning.DefaultTTLDays != 90 {
		t.Errorf("Versioning.DefaultTTLDays = %d, want 90", cfg.Versioning.DefaultTTLDays)
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		modify  func(*Config)
		wantErr bool
	}{
		{
			name:    "valid config",
			modify:  func(c *Config) {},
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			tt.modify(cfg)

			err := cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
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

// TestEnvOverrideS3 tests S3-related env vars
func TestEnvOverrideS3(t *testing.T) {
	cfg := DefaultConfig()

	os.Setenv("S3_BUCKET", "my-bucket")
	os.Setenv("S3_REGION", "eu-west-1")
	os.Setenv("S3_ENDPOINT", "http://localhost:9000")
	defer func() {
		os.Unsetenv("S3_BUCKET")
		os.Unsetenv("S3_REGION")
		os.Unsetenv("S3_ENDPOINT")
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
	defer os.Unsetenv("SEAFHTTP_TOKEN_TTL")

	cfg.applyEnvOverrides()

	// 2 hours = 7200 seconds
	if cfg.SeafHTTP.TokenTTL.Hours() != 2 {
		t.Errorf("SeafHTTP.TokenTTL = %v, want 2h", cfg.SeafHTTP.TokenTTL)
	}
}

// TestEnvOverrideSeafHTTPInvalid tests invalid duration is ignored
func TestEnvOverrideSeafHTTPInvalid(t *testing.T) {
	cfg := DefaultConfig()
	originalTTL := cfg.SeafHTTP.TokenTTL

	os.Setenv("SEAFHTTP_TOKEN_TTL", "invalid")
	defer os.Unsetenv("SEAFHTTP_TOKEN_TTL")

	cfg.applyEnvOverrides()

	// Should keep default since parse failed
	if cfg.SeafHTTP.TokenTTL != originalTTL {
		t.Errorf("SeafHTTP.TokenTTL = %v, want %v (unchanged)", cfg.SeafHTTP.TokenTTL, originalTTL)
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
