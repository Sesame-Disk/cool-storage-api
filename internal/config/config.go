package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds all configuration for SesameFS
type Config struct {
	Server     ServerConfig     `yaml:"server"`
	Database   DatabaseConfig   `yaml:"database"`
	Storage    StorageConfig    `yaml:"storage"`
	Auth       AuthConfig       `yaml:"auth"`
	Chunking   ChunkingConfig   `yaml:"chunking"`
	Versioning VersioningConfig `yaml:"versioning"`
	SeafHTTP   SeafHTTPConfig   `yaml:"seafhttp"`
}

// SeafHTTPConfig holds Seafile-compatible file transfer settings
type SeafHTTPConfig struct {
	TokenTTL time.Duration `yaml:"token_ttl"` // How long upload/download tokens are valid
}

// ServerConfig holds HTTP server settings
type ServerConfig struct {
	Port         string        `yaml:"port"`
	ReadTimeout  time.Duration `yaml:"read_timeout"`
	WriteTimeout time.Duration `yaml:"write_timeout"`
	MaxUploadMB  int64         `yaml:"max_upload_mb"`
}

// DatabaseConfig holds Cassandra connection settings
type DatabaseConfig struct {
	Hosts       []string `yaml:"hosts"`
	Keyspace    string   `yaml:"keyspace"`
	Consistency string   `yaml:"consistency"`
	LocalDC     string   `yaml:"local_dc"`
	Username    string   `yaml:"username"`
	Password    string   `yaml:"password"`
}

// StorageConfig holds storage backend settings
type StorageConfig struct {
	DefaultClass string                   `yaml:"default_class"`
	Backends     map[string]BackendConfig `yaml:"backends"`
}

// BackendConfig holds configuration for a storage backend
type BackendConfig struct {
	Type         string `yaml:"type"`          // s3, glacier, filesystem
	Endpoint     string `yaml:"endpoint"`      // S3 endpoint
	Bucket       string `yaml:"bucket"`        // S3 bucket name
	Region       string `yaml:"region"`        // AWS region
	StorageClass string `yaml:"storage_class"` // S3 storage class
	Vault        string `yaml:"vault"`         // Glacier vault name
	Path         string `yaml:"path"`          // Filesystem path
}

// AuthConfig holds authentication settings
type AuthConfig struct {
	DevMode   bool            `yaml:"dev_mode"`
	DevTokens []DevTokenEntry `yaml:"dev_tokens"`
	OIDC      OIDCConfig      `yaml:"oidc"`
}

// DevTokenEntry holds a development token for testing
type DevTokenEntry struct {
	Token  string `yaml:"token"`
	UserID string `yaml:"user_id"`
	OrgID  string `yaml:"org_id"`
}

// OIDCConfig holds OIDC provider settings
type OIDCConfig struct {
	Issuer       string   `yaml:"issuer"`
	ClientID     string   `yaml:"client_id"`
	ClientSecret string   `yaml:"client_secret"`
	Scopes       []string `yaml:"scopes"`
}

// ChunkingConfig holds FastCDC chunking settings
type ChunkingConfig struct {
	Algorithm     string         `yaml:"algorithm"`      // fastcdc
	HashAlgorithm string         `yaml:"hash_algorithm"` // sha256
	Adaptive      AdaptiveConfig `yaml:"adaptive"`       // Adaptive chunk sizing
	Probe         ProbeConfig    `yaml:"probe"`          // Speed probe settings
	Retry         RetryConfig    `yaml:"retry"`          // Retry settings
}

// AdaptiveConfig holds adaptive chunk sizing settings
type AdaptiveConfig struct {
	Enabled       bool  `yaml:"enabled"`        // Enable adaptive chunking
	AbsoluteMin   int64 `yaml:"absolute_min"`   // 2 MB floor (terrible connections)
	AbsoluteMax   int64 `yaml:"absolute_max"`   // 256 MB ceiling (datacenter)
	InitialSize   int64 `yaml:"initial_size"`   // 16 MB starting point (if probe skipped)
	TargetSeconds int   `yaml:"target_seconds"` // Target seconds per chunk (8s default)
}

// ProbeConfig holds speed probe settings
type ProbeConfig struct {
	Size    int64         `yaml:"size"`    // Probe size in bytes (1 MB default)
	Timeout time.Duration `yaml:"timeout"` // Probe timeout (30s default)
}

// RetryConfig holds retry and timeout settings
type RetryConfig struct {
	ChunkTimeout     time.Duration `yaml:"chunk_timeout"`      // Per-chunk timeout (60s default)
	MaxRetries       int           `yaml:"max_retries"`        // Max retry attempts (5 default)
	ReduceOnTimeout  float64       `yaml:"reduce_on_timeout"`  // Reduce to this fraction on timeout (0.5)
	ReduceOnFailure  float64       `yaml:"reduce_on_failure"`  // Reduce to this fraction on failure (0.5)
	BackoffBase      time.Duration `yaml:"backoff_base"`       // Base backoff duration (1s default)
	BackoffMaxJitter time.Duration `yaml:"backoff_max_jitter"` // Max jitter to add (500ms default)
}

// VersioningConfig holds file versioning settings
type VersioningConfig struct {
	DefaultTTLDays int           `yaml:"default_ttl_days"`
	MinTTLDays     int           `yaml:"min_ttl_days"`
	GCInterval     time.Duration `yaml:"gc_interval"`
}

// Load reads configuration from config.yaml and environment variables
func Load() (*Config, error) {
	cfg := DefaultConfig()

	// Try to load config file
	configPath := getEnv("CONFIG_PATH", "config.yaml")
	if data, err := os.ReadFile(configPath); err == nil {
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("failed to parse config file: %w", err)
		}
	}

	// Override with environment variables
	cfg.applyEnvOverrides()

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return cfg, nil
}

// DefaultConfig returns sensible defaults
func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Port:         ":8080",
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 300 * time.Second,
			MaxUploadMB:  10240, // 10 GB
		},
		Database: DatabaseConfig{
			Hosts:       []string{"localhost:9042"},
			Keyspace:    "sesamefs",
			Consistency: "LOCAL_QUORUM",
			LocalDC:     "datacenter1",
		},
		Storage: StorageConfig{
			DefaultClass: "hot",
			Backends: map[string]BackendConfig{
				"hot": {
					Type:   "s3",
					Bucket: "sesamefs-blocks",
					Region: "us-east-1",
				},
			},
		},
		Auth: AuthConfig{
			DevMode: true,
			DevTokens: []DevTokenEntry{
				{
					Token:  "dev-token-123",
					UserID: "00000000-0000-0000-0000-000000000001",
					OrgID:  "00000000-0000-0000-0000-000000000001",
				},
			},
		},
		Chunking: ChunkingConfig{
			Algorithm:     "fastcdc",
			HashAlgorithm: "sha256",
			Adaptive: AdaptiveConfig{
				Enabled:       true,
				AbsoluteMin:   2 * 1024 * 1024,   // 2 MB
				AbsoluteMax:   256 * 1024 * 1024, // 256 MB
				InitialSize:   16 * 1024 * 1024,  // 16 MB
				TargetSeconds: 8,                 // 8 seconds per chunk
			},
			Probe: ProbeConfig{
				Size:    1 * 1024 * 1024, // 1 MB probe
				Timeout: 30 * time.Second,
			},
			Retry: RetryConfig{
				ChunkTimeout:     60 * time.Second,
				MaxRetries:       5,
				ReduceOnTimeout:  0.5,
				ReduceOnFailure:  0.5,
				BackoffBase:      1 * time.Second,
				BackoffMaxJitter: 500 * time.Millisecond,
			},
		},
		Versioning: VersioningConfig{
			DefaultTTLDays: 90,
			MinTTLDays:     7,
			GCInterval:     24 * time.Hour,
		},
		SeafHTTP: SeafHTTPConfig{
			TokenTTL: 1 * time.Hour,
		},
	}
}

// applyEnvOverrides applies environment variable overrides
func (c *Config) applyEnvOverrides() {
	// Server
	if v := os.Getenv("PORT"); v != "" {
		c.Server.Port = ":" + v
	}
	if v := os.Getenv("SERVER_PORT"); v != "" {
		c.Server.Port = v
	}

	// Database
	if v := os.Getenv("CASSANDRA_HOSTS"); v != "" {
		c.Database.Hosts = []string{v}
	}
	if v := os.Getenv("CASSANDRA_KEYSPACE"); v != "" {
		c.Database.Keyspace = v
	}
	if v := os.Getenv("CASSANDRA_USERNAME"); v != "" {
		c.Database.Username = v
	}
	if v := os.Getenv("CASSANDRA_PASSWORD"); v != "" {
		c.Database.Password = v
	}
	if v := os.Getenv("CASSANDRA_LOCAL_DC"); v != "" {
		c.Database.LocalDC = v
	}

	// Storage
	if v := os.Getenv("S3_BUCKET"); v != "" {
		if hot, ok := c.Storage.Backends["hot"]; ok {
			hot.Bucket = v
			c.Storage.Backends["hot"] = hot
		}
	}
	if v := os.Getenv("S3_REGION"); v != "" {
		if hot, ok := c.Storage.Backends["hot"]; ok {
			hot.Region = v
			c.Storage.Backends["hot"] = hot
		}
	}
	if v := os.Getenv("S3_ENDPOINT"); v != "" {
		if hot, ok := c.Storage.Backends["hot"]; ok {
			hot.Endpoint = v
			c.Storage.Backends["hot"] = hot
		}
	}

	// Auth
	if v := os.Getenv("AUTH_DEV_MODE"); v != "" {
		c.Auth.DevMode = v == "true" || v == "1"
	}

	// SeafHTTP
	if v := os.Getenv("SEAFHTTP_TOKEN_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			c.SeafHTTP.TokenTTL = d
		}
	}
	if v := os.Getenv("OIDC_ISSUER"); v != "" {
		c.Auth.OIDC.Issuer = v
	}
	if v := os.Getenv("OIDC_CLIENT_ID"); v != "" {
		c.Auth.OIDC.ClientID = v
	}
	if v := os.Getenv("OIDC_CLIENT_SECRET"); v != "" {
		c.Auth.OIDC.ClientSecret = v
	}
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	if c.Server.Port == "" {
		return fmt.Errorf("server port is required")
	}
	if len(c.Database.Hosts) == 0 {
		return fmt.Errorf("at least one database host is required")
	}
	if c.Database.Keyspace == "" {
		return fmt.Errorf("database keyspace is required")
	}
	return nil
}

// getEnv returns environment variable or default
func getEnv(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

// getEnvInt returns environment variable as int or default
func getEnvInt(key string, defaultValue int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return defaultValue
}
