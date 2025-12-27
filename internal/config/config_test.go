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
