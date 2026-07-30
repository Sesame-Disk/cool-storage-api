package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// clearLoadEnvOverrides blanks out the env vars that Load()/applyEnvOverrides
// consult, so a test running inside a container or shell with .env exported
// (CASSANDRA_HOSTS, PORT, etc.) does not see those values override the YAML
// fixture under test. applyEnvOverrides treats "" as unset because every
// branch is gated on `v != ""`.
func clearLoadEnvOverrides(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"PORT", "SERVER_PORT", "SERVER_TRUSTED_PROXIES", "SERVER_REGION",
		"SERVER_URL", "DESKTOP_CUSTOM_BRAND", "DESKTOP_CUSTOM_LOGO",
		"CORS_ALLOWED_ORIGINS",
		"CASSANDRA_HOSTS", "CASSANDRA_KEYSPACE", "CASSANDRA_CONSISTENCY", "CASSANDRA_USERNAME",
		"CASSANDRA_PASSWORD", "CASSANDRA_LOCAL_DC", "CASSANDRA_SERIAL_CONSISTENCY", "CASSANDRA_TIMEOUT",
		"CASSANDRA_PROTO_VERSION",
		"CASSANDRA_REPLICATION_CLASS", "CASSANDRA_REPLICATION_FACTOR", "CASSANDRA_REPLICATION_DCS",
		"STORAGE_MODE", "S3_BUCKET", "S3_REGION", "S3_ENDPOINT",
		"S3_SERVER_SIDE_ENCRYPTION", "S3_SSE_KMS_KEY_ID",
		"S3_ACCESS_KEY_ID", "S3_SECRET_ACCESS_KEY",
		"BILLING_URL", "ACCOUNTS_DELETE_ACCOUNT_URL",
		"ACCOUNTS_ORG_USER_MANAGEMENT_URL", "ACCOUNTS_DISABLE_ORG_USER_WRITES",
		"AUTH_DEV_MODE", "FIRST_SUPERADMIN_EMAIL", "SHARE_LINK_HMAC_KEY",
		"WEB_UPLOADS_ENABLE_UPLOAD_FOLDER", "WEB_UPLOADS_ENABLE_RESUMABLE_FILE_UPLOAD",
		"WEB_UPLOADS_ENABLE_WEB_BLOCK_UPLOAD", "WEB_UPLOADS_RESUMABLE_CHUNK_SIZE_MB",
		"WEB_UPLOADS_BLOCK_UPLOAD_BLOCK_SIZE_MB", "WEB_UPLOADS_MAX_FILE_SIZE_MB",
		"WEB_UPLOADS_MAX_FILES_PER_BATCH", "WEB_UPLOADS_SIMULTANEOUS_UPLOADS",
		"WEB_UPLOADS_MAX_CONCURRENT_BLOCK_UPLOADS_PER_USER",
		"WEB_UPLOADS_MAX_UNCOMMITTED_BLOCK_SESSIONS_PER_USER",
		"WEB_UPLOADS_MAX_STAGED_BYTES_PER_SESSION_MB",
		"SEAFHTTP_TOKEN_TTL", "SEAFHTTP_ZIP_MAX_ENTRIES",
		"SEAFHTTP_ZIP_MAX_DEPTH", "SEAFHTTP_ZIP_MAX_BYTES",
		"SEAFHTTP_SYNC_BLOCK_MAX_BYTES",
		"SEAFHTTP_UPLOAD_LINK_WRITES_PER_MINUTE", "SEAFHTTP_UPLOAD_LINK_WRITE_BURST",
		"SEAFHTTP_UPLOAD_LINK_SOURCE_WRITES_PER_MINUTE", "SEAFHTTP_UPLOAD_LINK_SOURCE_WRITE_BURST",
		"SEAFHTTP_UPLOAD_LINK_MAX_INFLIGHT_PER_SOURCE", "SEAFHTTP_UPLOAD_LINK_MAX_INFLIGHT_PER_NODE",
	} {
		t.Setenv(k, "")
	}

	for _, prefix := range []string{
		"OIDC_",
		"ONLYOFFICE_",
		"ELASTICSEARCH_",
		"METRICS_",
		"HEALTH_",
		"FILEVIEW_",
		"GC_",
		"S3_CLASS_",
	} {
		for _, entry := range os.Environ() {
			key, _, ok := strings.Cut(entry, "=")
			if ok && strings.HasPrefix(key, prefix) {
				t.Setenv(key, "")
			}
		}
	}
}

func TestLoad(t *testing.T) {
	clearLoadEnvOverrides(t)

	// Create a temporary config file
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")

	configContent := "server:\n" +
		"  port: \":9090\"\n\n" +
		"database:\n" +
		"  hosts:\n" +
		"    - \"localhost\"\n" +
		"  keyspace: \"test_keyspace\"\n" +
		"  consistency: \"ONE\"\n" +
		"  serial_consistency: \"LOCAL_SERIAL\"\n" +
		"  timeout: \"25s\"\n\n" +
		"storage:\n" +
		"  default_class: \"hot\"\n" +
		"  backends:\n" +
		"    hot:\n" +
		"      type: \"s3\"\n" +
		"      bucket: \"test-bucket\"\n" +
		"      region: \"us-east-1\"\n\n" +
		"auth:\n" +
		"  dev_mode: true\n" +
		"  dev_tokens:\n" +
		"    - token: \"test-token\"\n" +
		"      user_id: \"00000000-0000-0000-0000-000000000001\"\n" +
		"      org_id: \"00000000-0000-0000-0000-000000000001\"\n\n" +
		"versioning:\n" +
		"  default_ttl_days: 30\n" +
		"  min_ttl_days: 7\n"

	if err := os.WriteFile(configPath, []byte(configContent), 0644); err != nil {
		t.Fatalf("Failed to write test config: %v", err)
	}

	t.Setenv("CONFIG_PATH", configPath)

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
	if cfg.Database.Timeout != 25*time.Second {
		t.Errorf("Database.Timeout = %s, want 25s", cfg.Database.Timeout)
	}
	if cfg.Database.SerialConsistency != "LOCAL_SERIAL" {
		t.Errorf("Database.SerialConsistency = %s, want LOCAL_SERIAL", cfg.Database.SerialConsistency)
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
	clearLoadEnvOverrides(t)

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

	t.Setenv("CONFIG_PATH", configPath)
	t.Setenv("SERVER_PORT", ":9999")
	t.Setenv("AUTH_DEV_MODE", "true")
	t.Setenv("CASSANDRA_CONSISTENCY", "QUORUM")
	t.Setenv("CASSANDRA_SERIAL_CONSISTENCY", "LOCAL_SERIAL")
	t.Setenv("CASSANDRA_TIMEOUT", "42s")
	t.Setenv("CASSANDRA_PROTO_VERSION", "5")

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
	if cfg.Database.Consistency != "QUORUM" {
		t.Errorf("Database.Consistency = %s, want QUORUM (from env)", cfg.Database.Consistency)
	}
	if cfg.Database.Timeout != 42*time.Second {
		t.Errorf("Database.Timeout = %s, want 42s (from env)", cfg.Database.Timeout)
	}
	if cfg.Database.SerialConsistency != "LOCAL_SERIAL" {
		t.Errorf("Database.SerialConsistency = %s, want LOCAL_SERIAL (from env)", cfg.Database.SerialConsistency)
	}
	if cfg.Database.ProtoVersion != 5 {
		t.Errorf("Database.ProtoVersion = %d, want 5 (from env)", cfg.Database.ProtoVersion)
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
	if cfg.Database.Timeout != 10*time.Second {
		t.Errorf("Database.Timeout = %s, want 10s", cfg.Database.Timeout)
	}
	if cfg.Database.SerialConsistency != "SERIAL" {
		t.Errorf("Database.SerialConsistency = %s, want SERIAL", cfg.Database.SerialConsistency)
	}
	if cfg.Database.ProtoVersion != 4 {
		t.Errorf("Database.ProtoVersion = %d, want 4", cfg.Database.ProtoVersion)
	}
	if cfg.Database.ReplicationClass != "NetworkTopologyStrategy" {
		t.Errorf("Database.ReplicationClass = %s, want NetworkTopologyStrategy", cfg.Database.ReplicationClass)
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
			name: "invalid database serial consistency",
			modify: func(c *Config) {
				c.Database.SerialConsistency = "bad"
			},
			wantErr:        true,
			wantErrContain: "database serial_consistency",
		},
		{
			name: "invalid database consistency",
			modify: func(c *Config) {
				c.Database.Consistency = "bad"
			},
			wantErr:        true,
			wantErrContain: "database consistency",
		},
		{
			name: "invalid database proto_version",
			modify: func(c *Config) {
				c.Database.ProtoVersion = 2
			},
			wantErr:        true,
			wantErrContain: "database proto_version",
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
			name: "chunked staging max bytes must be non-negative",
			modify: func(c *Config) {
				c.SeafHTTP.ChunkedStagingMaxBytes = -1
			},
			wantErr:        true,
			wantErrContain: "seafhttp.chunked_staging_max_bytes",
		},
		{
			// Deliberately unlike chunked_staging_max_bytes above, where zero means
			// "guard disabled". An unbounded PutBlock body is the defect the cap
			// exists for, so zero must not be a way back to it.
			name: "sync block max bytes rejects zero rather than meaning unlimited",
			modify: func(c *Config) {
				c.SeafHTTP.SyncBlockMaxBytes = 0
			},
			wantErr:        true,
			wantErrContain: "seafhttp.sync_block_max_bytes",
		},
		{
			name: "sync block max bytes rejects a negative value",
			modify: func(c *Config) {
				c.SeafHTTP.SyncBlockMaxBytes = -1
			},
			wantErr:        true,
			wantErrContain: "seafhttp.sync_block_max_bytes",
		},
		{
			// The ceiling exists to catch a value copied from the web uploader's
			// 256 MiB chunk ceiling, which is where the old 257 MiB bound came from.
			name: "sync block max bytes rejects a value above the ceiling",
			modify: func(c *Config) {
				c.SeafHTTP.SyncBlockMaxBytes = MaxSyncBlockMaxBytes + 1
			},
			wantErr:        true,
			wantErrContain: "seafhttp.sync_block_max_bytes",
		},
		{
			name: "sync block max bytes accepts a value at the ceiling",
			modify: func(c *Config) {
				c.SeafHTTP.SyncBlockMaxBytes = MaxSyncBlockMaxBytes
			},
			wantErr: false,
		},
		{
			// Rate and burst are independent dimensions of a token bucket, so a
			// burst below the per-second rate is a valid (if unfriendly) choice and
			// must NOT be rejected. An earlier revision rejected it, which would
			// have refused a coherent configuration on a made-up rule.
			name: "upload link burst below the per-second rate is accepted",
			modify: func(c *Config) {
				c.SeafHTTP.UploadLinkWritesPerMinute = 600
				c.SeafHTTP.UploadLinkWriteBurst = 1
			},
			wantErr: false,
		},
		{
			// The one combination that cannot mean anything: a live rate with no
			// capacity refuses every request. Not silently filled in from the rate,
			// because "no burst" and "burst equal to the rate" are far apart.
			name: "upload link zero burst with a live rate is rejected",
			modify: func(c *Config) {
				c.SeafHTTP.UploadLinkWritesPerMinute = 600
				c.SeafHTTP.UploadLinkWriteBurst = 0
			},
			wantErr:        true,
			wantErrContain: "seafhttp.upload_link_write_burst",
		},
		{
			name: "upload link rate zero disables the limiter and ignores the burst",
			modify: func(c *Config) {
				c.SeafHTTP.UploadLinkWritesPerMinute = 0
				c.SeafHTTP.UploadLinkWriteBurst = 0
			},
			wantErr: false,
		},
		{
			name: "upload link rate rejects a negative value",
			modify: func(c *Config) {
				c.SeafHTTP.UploadLinkWritesPerMinute = -1
			},
			wantErr:        true,
			wantErrContain: "seafhttp.upload_link_writes_per_minute",
		},
		{
			name: "upload link burst rejects a negative value",
			modify: func(c *Config) {
				c.SeafHTTP.UploadLinkWriteBurst = -1
			},
			wantErr:        true,
			wantErrContain: "seafhttp.upload_link_write_burst",
		},
		{
			// The ceiling is operational policy: values above it are ineffective
			// protection and likely indicate a unit mistake.
			name: "upload link rate rejects a value above the ceiling",
			modify: func(c *Config) {
				c.SeafHTTP.UploadLinkWritesPerMinute = MaxUploadLinkWritesPerMinute + 1
			},
			wantErr:        true,
			wantErrContain: "seafhttp.upload_link_writes_per_minute",
		},
		{
			// The per-source bound is a separate pair and gets the same rules; a
			// shared helper is only correct if both call sites are actually wired.
			name: "upload link per-source zero burst with a live rate is rejected",
			modify: func(c *Config) {
				c.SeafHTTP.UploadLinkSourceWritesPerMinute = 600
				c.SeafHTTP.UploadLinkSourceWriteBurst = 0
			},
			wantErr:        true,
			wantErrContain: "seafhttp.upload_link_source_write_burst",
		},
		{
			name: "upload link per-source rate can be disabled while the per-client bound stays on",
			modify: func(c *Config) {
				c.SeafHTTP.UploadLinkSourceWritesPerMinute = 0
				c.SeafHTTP.UploadLinkSourceWriteBurst = 0
			},
			wantErr: false,
		},
		{
			name: "upload link per-source inflight cap rejects a negative value",
			modify: func(c *Config) {
				c.SeafHTTP.UploadLinkMaxInflightPerSource = -1
			},
			wantErr:        true,
			wantErrContain: "seafhttp.upload_link_max_inflight_per_source",
		},
		{
			name: "upload link node inflight cap rejects a negative value",
			modify: func(c *Config) {
				c.SeafHTTP.UploadLinkMaxInflightPerNode = -1
			},
			wantErr:        true,
			wantErrContain: "seafhttp.upload_link_max_inflight_per_node",
		},
		{
			name: "upload link inflight caps accept zero as disabled",
			modify: func(c *Config) {
				c.SeafHTTP.UploadLinkMaxInflightPerSource = 0
				c.SeafHTTP.UploadLinkMaxInflightPerNode = 0
			},
			wantErr: false,
		},
		{
			name: "upload link per-source inflight cap rejects above maximum",
			modify: func(c *Config) {
				c.SeafHTTP.UploadLinkMaxInflightPerSource = MaxUploadLinkMaxInflightPerSource + 1
			},
			wantErr:        true,
			wantErrContain: "seafhttp.upload_link_max_inflight_per_source",
		},
		{
			name: "upload link node inflight cap rejects above maximum",
			modify: func(c *Config) {
				c.SeafHTTP.UploadLinkMaxInflightPerNode = MaxUploadLinkMaxInflightPerNode + 1
			},
			wantErr:        true,
			wantErrContain: "seafhttp.upload_link_max_inflight_per_node",
		},
		{
			name: "upload link per-source cap cannot exceed enabled node cap",
			modify: func(c *Config) {
				c.SeafHTTP.UploadLinkMaxInflightPerSource = 9
				c.SeafHTTP.UploadLinkMaxInflightPerNode = 8
			},
			wantErr:        true,
			wantErrContain: "must not exceed",
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
		{
			name: "production multi-region requires server region when legacy hot backend is inactive",
			modify: func(c *Config) {
				c.Auth.DevMode = false
				c.Auth.ShareLinkHMACKey = "very-secure-test-key"
				c.CORS.AllowedOrigins = []string{"https://app.example.com"}
				c.Storage.Mode = "multi"
				c.Storage.DefaultClass = "hot-s3-usa"
				c.Storage.Classes = map[string]StorageClassConfig{
					"hot-s3-usa": {Type: "s3", Bucket: "sesamefs-usa", Region: "us-east-1"},
				}
				c.Storage.RegionClasses = map[string]RegionClassConfig{"usa": {Hot: "hot-s3-usa"}}
				hot := c.Storage.Backends["hot"]
				hot.Bucket = ""
				c.Storage.Backends["hot"] = hot
			},
			wantErr:        true,
			wantErrContain: "server.region",
		},
		{
			name: "production multi-region rejects unknown server region",
			modify: func(c *Config) {
				c.Auth.DevMode = false
				c.Auth.ShareLinkHMACKey = "very-secure-test-key"
				c.CORS.AllowedOrigins = []string{"https://app.example.com"}
				c.Storage.Mode = "multi"
				c.Server.Region = "latam"
				c.Storage.DefaultClass = "hot-s3-usa"
				c.Storage.Classes = map[string]StorageClassConfig{
					"hot-s3-usa": {Type: "s3", Bucket: "sesamefs-usa", Region: "us-east-1"},
					"hot-s3-eu":  {Type: "s3", Bucket: "sesamefs-eu", Region: "eu-west-1"},
				}
				c.Storage.RegionClasses = map[string]RegionClassConfig{
					"usa": {Hot: "hot-s3-usa"},
					"eu":  {Hot: "hot-s3-eu"},
				}
				c.Storage.Backends = map[string]BackendConfig{"hot": {Type: "s3"}}
			},
			wantErr:        true,
			wantErrContain: "server.region",
		},
		{
			name: "production multi-region accepts configured server region",
			modify: func(c *Config) {
				c.Auth.DevMode = false
				c.Auth.ShareLinkHMACKey = "very-secure-test-key"
				c.CORS.AllowedOrigins = []string{"https://app.example.com"}
				c.Storage.Mode = "multi"
				c.Server.Region = "eu"
				c.Database.ReplicationClass = "NetworkTopologyStrategy"
				c.Database.ReplicationDCs = map[string]int{"dc-usa": 1, "dc-eu": 1}
				c.Storage.DefaultClass = "hot-s3-usa"
				c.Storage.Classes = map[string]StorageClassConfig{
					"hot-s3-usa": {Type: "s3", Bucket: "sesamefs-usa", Region: "us-east-1"},
					"hot-s3-eu":  {Type: "s3", Bucket: "sesamefs-eu", Region: "eu-west-1"},
				}
				c.Storage.RegionClasses = map[string]RegionClassConfig{
					"usa": {Hot: "hot-s3-usa"},
					"eu":  {Hot: "hot-s3-eu"},
				}
				c.Storage.Backends = map[string]BackendConfig{"hot": {Type: "s3"}}
			},
			wantErr: false,
		},
		{
			name: "production multi-region rejects unknown region class target",
			modify: func(c *Config) {
				c.Auth.DevMode = false
				c.Auth.ShareLinkHMACKey = "very-secure-test-key"
				c.CORS.AllowedOrigins = []string{"https://app.example.com"}
				c.Storage.Mode = "multi"
				c.Server.Region = "eu"
				c.Storage.DefaultClass = "hot-s3-usa"
				c.Storage.Classes = map[string]StorageClassConfig{
					"hot-s3-usa": {Type: "s3", Bucket: "sesamefs-usa", Region: "us-east-1"},
				}
				c.Storage.RegionClasses = map[string]RegionClassConfig{
					"eu": {Hot: "hot-s3-eu"},
				}
				c.Storage.Backends = map[string]BackendConfig{"hot": {Type: "s3"}}
			},
			wantErr:        true,
			wantErrContain: "storage.region_classes.eu.hot",
		},
		{
			name: "storage mode rejects unsupported value",
			modify: func(c *Config) {
				c.Storage.Mode = "hybrid"
			},
			wantErr:        true,
			wantErrContain: "storage.mode must be one of",
		},
		{
			name: "production multi-region rejects legacy hot backend overrides",
			modify: func(c *Config) {
				c.Auth.DevMode = false
				c.Auth.ShareLinkHMACKey = "very-secure-test-key"
				c.CORS.AllowedOrigins = []string{"https://app.example.com"}
				c.Storage.Mode = "multi"
				c.Server.Region = "eu"
				c.Storage.DefaultClass = "hot-s3-usa"
				c.Storage.Classes = map[string]StorageClassConfig{
					"hot-s3-usa": {Type: "s3", Bucket: "sesamefs-usa", Region: "us-east-1"},
					"hot-s3-eu":  {Type: "s3", Bucket: "sesamefs-eu", Region: "eu-west-1"},
				}
				c.Storage.RegionClasses = map[string]RegionClassConfig{
					"usa": {Hot: "hot-s3-usa"},
					"eu":  {Hot: "hot-s3-eu"},
				}
				hot := c.Storage.Backends["hot"]
				hot.Bucket = "legacy-bucket"
				c.Storage.Backends["hot"] = hot
			},
			wantErr:        true,
			wantErrContain: "does not allow legacy hot backend overrides",
		},
		{
			name: "production single-region rejects server region",
			modify: func(c *Config) {
				c.Auth.DevMode = false
				c.Auth.ShareLinkHMACKey = "very-secure-test-key"
				c.CORS.AllowedOrigins = []string{"https://app.example.com"}
				c.Storage.Mode = "single"
				c.Server.Region = "eu"
				hot := c.Storage.Backends["hot"]
				hot.Bucket = "legacy-bucket"
				c.Storage.Backends["hot"] = hot
			},
			wantErr:        true,
			wantErrContain: "storage.mode=single requires server.region to be empty",
		},
		{
			name: "non-positive web block upload block size",
			modify: func(c *Config) {
				c.WebUploads.WebBlockUploadBlockSizeMB = 0
			},
			wantErr:        true,
			wantErrContain: "web_block_upload_block_size_mb must be greater than zero",
		},
		{
			name: "web block upload staged cap requires positive concurrent upload cap",
			modify: func(c *Config) {
				c.WebUploads.EnableWebBlockUpload = true
				c.WebUploads.MaxStagedBytesPerSessionMB = 1024
				c.WebUploads.MaxConcurrentBlockUploadsPerUser = 0
			},
			wantErr:        true,
			wantErrContain: "web block upload with a staged-bytes cap requires web_uploads.max_concurrent_block_uploads_per_user to be greater than zero",
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

// TestEnvOverrideSyncBlockMaxBytes covers the operator lever end to end: a valid
// value wins over the default, and a malformed one is reported instead of being
// silently dropped back to the default — an operator who deliberately raised the
// cap must not end up running the lower one with no signal.
func TestEnvOverrideSyncBlockMaxBytes(t *testing.T) {
	clearLoadEnvOverrides(t)

	t.Run("valid value overrides the default", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Auth.DevMode = true
		t.Setenv("SEAFHTTP_SYNC_BLOCK_MAX_BYTES", "33554432")

		cfg.applyEnvOverrides()
		if err := cfg.Validate(); err != nil {
			t.Fatalf("Validate() error = %v", err)
		}
		if cfg.SeafHTTP.SyncBlockMaxBytes != 32*1024*1024 {
			t.Fatalf("SyncBlockMaxBytes = %d, want %d", cfg.SeafHTTP.SyncBlockMaxBytes, 32*1024*1024)
		}
	})

	t.Run("malformed value is reported, not dropped", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.Auth.DevMode = true
		t.Setenv("SEAFHTTP_SYNC_BLOCK_MAX_BYTES", "16MiB")

		cfg.applyEnvOverrides()
		err := cfg.Validate()
		if err == nil {
			t.Fatal("Validate() = nil; a malformed override must not fall back to the default silently")
		}
		if !strings.Contains(err.Error(), "SEAFHTTP_SYNC_BLOCK_MAX_BYTES") {
			t.Fatalf("Validate() error = %v, want it to name SEAFHTTP_SYNC_BLOCK_MAX_BYTES", err)
		}
	})

	// The env lever is subject to the same bounds as the YAML knob; it is not a
	// back door around the ceiling or around the "zero is not unlimited" rule.
	for _, tc := range []struct{ name, value string }{
		{"zero", "0"},
		{"negative", "-1"},
		{"above the ceiling", "67108865"},
	} {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Auth.DevMode = true
			t.Setenv("SEAFHTTP_SYNC_BLOCK_MAX_BYTES", tc.value)

			cfg.applyEnvOverrides()
			err := cfg.Validate()
			if err == nil {
				t.Fatalf("Validate() = nil for %s; the env lever must obey the same bounds as the YAML knob", tc.value)
			}
			if !strings.Contains(err.Error(), "seafhttp.sync_block_max_bytes") {
				t.Fatalf("Validate() error = %v, want it to name seafhttp.sync_block_max_bytes", err)
			}
		})
	}
}

// TestEnvOverrideUploadLinkWriteLimits pins the four operator levers for the
// anonymous upload-link bounds. All four are reported rather than silently
// dropped when malformed: an operator who deliberately loosened a limit and
// typo'd the value must not end up running the stricter default unaware.
func TestEnvOverrideUploadLinkWriteLimits(t *testing.T) {
	clearLoadEnvOverrides(t)

	cfg := DefaultConfig()
	cfg.Auth.DevMode = true
	t.Setenv("SEAFHTTP_UPLOAD_LINK_WRITES_PER_MINUTE", "900")
	t.Setenv("SEAFHTTP_UPLOAD_LINK_WRITE_BURST", "1800")
	t.Setenv("SEAFHTTP_UPLOAD_LINK_SOURCE_WRITES_PER_MINUTE", "9000")
	t.Setenv("SEAFHTTP_UPLOAD_LINK_SOURCE_WRITE_BURST", "18000")
	t.Setenv("SEAFHTTP_UPLOAD_LINK_MAX_INFLIGHT_PER_SOURCE", "12")
	t.Setenv("SEAFHTTP_UPLOAD_LINK_MAX_INFLIGHT_PER_NODE", "96")

	cfg.applyEnvOverrides()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	for _, tc := range []struct {
		name string
		got  int
		want int
	}{
		{"UploadLinkWritesPerMinute", cfg.SeafHTTP.UploadLinkWritesPerMinute, 900},
		{"UploadLinkWriteBurst", cfg.SeafHTTP.UploadLinkWriteBurst, 1800},
		{"UploadLinkSourceWritesPerMinute", cfg.SeafHTTP.UploadLinkSourceWritesPerMinute, 9000},
		{"UploadLinkSourceWriteBurst", cfg.SeafHTTP.UploadLinkSourceWriteBurst, 18000},
		{"UploadLinkMaxInflightPerSource", cfg.SeafHTTP.UploadLinkMaxInflightPerSource, 12},
		{"UploadLinkMaxInflightPerNode", cfg.SeafHTTP.UploadLinkMaxInflightPerNode, 96},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %d, want %d", tc.name, tc.got, tc.want)
		}
	}
}

func TestDefaultUploadLinkInflightCaps(t *testing.T) {
	cfg := DefaultConfig()
	if got := cfg.SeafHTTP.UploadLinkMaxInflightPerSource; got != DefaultUploadLinkMaxInflightPerSource {
		t.Fatalf("UploadLinkMaxInflightPerSource = %d, want %d", got, DefaultUploadLinkMaxInflightPerSource)
	}
	if got := cfg.SeafHTTP.UploadLinkMaxInflightPerNode; got != DefaultUploadLinkMaxInflightPerNode {
		t.Fatalf("UploadLinkMaxInflightPerNode = %d, want %d", got, DefaultUploadLinkMaxInflightPerNode)
	}
}

func TestEnvOverrideUploadLinkWriteLimitsRejectMalformedValues(t *testing.T) {
	clearLoadEnvOverrides(t)

	for _, env := range []string{
		"SEAFHTTP_UPLOAD_LINK_WRITES_PER_MINUTE",
		"SEAFHTTP_UPLOAD_LINK_WRITE_BURST",
		"SEAFHTTP_UPLOAD_LINK_SOURCE_WRITES_PER_MINUTE",
		"SEAFHTTP_UPLOAD_LINK_SOURCE_WRITE_BURST",
		"SEAFHTTP_UPLOAD_LINK_MAX_INFLIGHT_PER_SOURCE",
		"SEAFHTTP_UPLOAD_LINK_MAX_INFLIGHT_PER_NODE",
	} {
		t.Run(env, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Auth.DevMode = true
			t.Setenv(env, "lots")

			cfg.applyEnvOverrides()
			err := cfg.Validate()
			if err == nil {
				t.Fatal("Validate() = nil; a malformed override must not fall back to the default silently")
			}
			if !strings.Contains(err.Error(), env) {
				t.Fatalf("Validate() error = %v, want it to name %s", err, env)
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
	os.Setenv("CASSANDRA_CONSISTENCY", "EACH_QUORUM")
	os.Setenv("CASSANDRA_USERNAME", "test_user")
	os.Setenv("CASSANDRA_PASSWORD", "test_pass")
	os.Setenv("CASSANDRA_LOCAL_DC", "dc2")
	os.Setenv("CASSANDRA_SERIAL_CONSISTENCY", "LOCAL_SERIAL")
	os.Setenv("CASSANDRA_REPLICATION_CLASS", "NetworkTopologyStrategy")
	os.Setenv("CASSANDRA_REPLICATION_DCS", "dc1:1,dc2:2")
	defer func() {
		os.Unsetenv("CASSANDRA_HOSTS")
		os.Unsetenv("CASSANDRA_KEYSPACE")
		os.Unsetenv("CASSANDRA_CONSISTENCY")
		os.Unsetenv("CASSANDRA_USERNAME")
		os.Unsetenv("CASSANDRA_PASSWORD")
		os.Unsetenv("CASSANDRA_LOCAL_DC")
		os.Unsetenv("CASSANDRA_SERIAL_CONSISTENCY")
		os.Unsetenv("CASSANDRA_REPLICATION_CLASS")
		os.Unsetenv("CASSANDRA_REPLICATION_DCS")
	}()

	cfg.applyEnvOverrides()

	if len(cfg.Database.Hosts) != 1 || cfg.Database.Hosts[0] != "cassandra1.example.com" {
		t.Errorf("Database.Hosts = %v, want [cassandra1.example.com]", cfg.Database.Hosts)
	}
	if cfg.Database.Keyspace != "test_ks" {
		t.Errorf("Database.Keyspace = %s, want test_ks", cfg.Database.Keyspace)
	}
	if cfg.Database.Consistency != "EACH_QUORUM" {
		t.Errorf("Database.Consistency = %s, want EACH_QUORUM", cfg.Database.Consistency)
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
	if cfg.Database.SerialConsistency != "LOCAL_SERIAL" {
		t.Errorf("Database.SerialConsistency = %s, want LOCAL_SERIAL", cfg.Database.SerialConsistency)
	}
	if cfg.Database.ReplicationClass != "NetworkTopologyStrategy" {
		t.Errorf("Database.ReplicationClass = %s, want NetworkTopologyStrategy", cfg.Database.ReplicationClass)
	}
	if len(cfg.Database.ReplicationDCs) != 2 || cfg.Database.ReplicationDCs["dc1"] != 1 || cfg.Database.ReplicationDCs["dc2"] != 2 {
		t.Errorf("Database.ReplicationDCs = %#v, want dc1:1,dc2:2", cfg.Database.ReplicationDCs)
	}
}

func TestConfigValidateMultiRegionRequiresNetworkTopologyStrategy(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Auth.DevMode = false
	cfg.Auth.ShareLinkHMACKey = "very-secure-test-key"
	cfg.CORS.AllowedOrigins = []string{"https://app.example.com"}
	cfg.Storage.Mode = "multi"
	cfg.Server.Region = "na"
	cfg.Storage.DefaultClass = "hot-s3-na"
	cfg.Storage.Classes = map[string]StorageClassConfig{
		"hot-s3-na": {Type: "s3", Bucket: "sesamefs-na", Region: "us-east-1"},
	}
	cfg.Storage.Backends = map[string]BackendConfig{"hot": {Type: "s3"}}
	cfg.Storage.RegionClasses = map[string]RegionClassConfig{
		"na": {Hot: "hot-s3-na"},
	}
	cfg.Database.ReplicationClass = "SimpleStrategy"
	cfg.Database.ReplicationFactor = 1

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "NetworkTopologyStrategy") {
		t.Fatalf("Validate() error = %v, want NetworkTopologyStrategy requirement", err)
	}

	cfg.Database.ReplicationClass = "NetworkTopologyStrategy"
	cfg.Database.ReplicationDCs = map[string]int{"dc-na": 1}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() with NetworkTopologyStrategy error = %v", err)
	}
}

func TestConfigValidateNetworkTopologyStrategyDefaultsToLocalDC(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Auth.DevMode = true
	cfg.Database.ReplicationDCs = nil

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if len(cfg.Database.ReplicationDCs) != 1 || cfg.Database.ReplicationDCs["datacenter1"] != 1 {
		t.Fatalf("Database.ReplicationDCs = %#v, want datacenter1:1", cfg.Database.ReplicationDCs)
	}
}

func TestEnvOverrideCassandraRejectsInvalidReplicationFactor(t *testing.T) {
	cfg := DefaultConfig()

	os.Setenv("CASSANDRA_REPLICATION_FACTOR", "abc")
	defer os.Unsetenv("CASSANDRA_REPLICATION_FACTOR")

	cfg.applyEnvOverrides()
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "CASSANDRA_REPLICATION_FACTOR") {
		t.Fatalf("Validate() error = %v, want invalid CASSANDRA_REPLICATION_FACTOR", err)
	}
}

func TestEnvOverrideCassandraRejectsInvalidSerialConsistency(t *testing.T) {
	cfg := DefaultConfig()

	os.Setenv("CASSANDRA_SERIAL_CONSISTENCY", "bad")
	defer os.Unsetenv("CASSANDRA_SERIAL_CONSISTENCY")

	cfg.applyEnvOverrides()
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "database serial_consistency") {
		t.Fatalf("Validate() error = %v, want invalid database serial_consistency", err)
	}
}

func TestEnvOverrideCassandraRejectsInvalidConsistency(t *testing.T) {
	cfg := DefaultConfig()

	os.Setenv("CASSANDRA_CONSISTENCY", "bad")
	defer os.Unsetenv("CASSANDRA_CONSISTENCY")

	cfg.applyEnvOverrides()
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "database consistency") {
		t.Fatalf("Validate() error = %v, want invalid database consistency", err)
	}
}

func TestEnvOverrideCassandraRejectsInvalidReplicationDCs(t *testing.T) {
	cfg := DefaultConfig()

	os.Setenv("CASSANDRA_REPLICATION_DCS", "garbage")
	defer os.Unsetenv("CASSANDRA_REPLICATION_DCS")

	cfg.applyEnvOverrides()
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "CASSANDRA_REPLICATION_DCS") {
		t.Fatalf("Validate() error = %v, want invalid CASSANDRA_REPLICATION_DCS", err)
	}
}

func TestEnvOverrideCassandraRejectsInvalidReplicationDCName(t *testing.T) {
	cfg := DefaultConfig()

	os.Setenv("CASSANDRA_REPLICATION_DCS", "dc'bad:1")
	defer os.Unsetenv("CASSANDRA_REPLICATION_DCS")

	cfg.applyEnvOverrides()
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "CASSANDRA_REPLICATION_DCS") {
		t.Fatalf("Validate() error = %v, want invalid CASSANDRA_REPLICATION_DCS", err)
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

func TestEnvOverrideServerRegion(t *testing.T) {
	cfg := DefaultConfig()

	os.Setenv("SERVER_REGION", "EU")
	defer os.Unsetenv("SERVER_REGION")

	cfg.applyEnvOverrides()

	if cfg.Server.Region != "eu" {
		t.Errorf("Server.Region = %q, want %q", cfg.Server.Region, "eu")
	}
}

func TestEnvOverrideStorageMode(t *testing.T) {
	cfg := DefaultConfig()

	os.Setenv("STORAGE_MODE", "SINGLE")
	defer os.Unsetenv("STORAGE_MODE")

	cfg.applyEnvOverrides()

	if cfg.Storage.Mode != "single" {
		t.Fatalf("Storage.Mode = %q, want %q", cfg.Storage.Mode, "single")
	}
	if cfg.Storage.DefaultClass != "hot" {
		t.Fatalf("Storage.DefaultClass = %q, want %q", cfg.Storage.DefaultClass, "hot")
	}
}

func TestEnvOverrideS3ForcesLegacyDefaultClass(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Storage.DefaultClass = "hot-s3-usa"

	os.Setenv("S3_BUCKET", "my-bucket")
	os.Setenv("S3_REGION", "eu-west-1")
	defer func() {
		os.Unsetenv("S3_BUCKET")
		os.Unsetenv("S3_REGION")
	}()

	cfg.applyEnvOverrides()

	if cfg.Storage.DefaultClass != "hot" {
		t.Fatalf("Storage.DefaultClass = %q, want %q", cfg.Storage.DefaultClass, "hot")
	}
}

func TestEnvOverrideServerExternalURLsAndBranding(t *testing.T) {
	cfg := DefaultConfig()

	os.Setenv("SERVER_URL", "https://files.example.com/")
	os.Setenv("DESKTOP_CUSTOM_BRAND", "Sesame Cloud")
	os.Setenv("DESKTOP_CUSTOM_LOGO", "https://cdn.example.com/logo.svg")
	defer func() {
		os.Unsetenv("SERVER_URL")
		os.Unsetenv("DESKTOP_CUSTOM_BRAND")
		os.Unsetenv("DESKTOP_CUSTOM_LOGO")
	}()

	cfg.applyEnvOverrides()

	if cfg.Server.URL != "https://files.example.com" {
		t.Fatalf("Server.URL = %q, want %q", cfg.Server.URL, "https://files.example.com")
	}
	if cfg.Server.DesktopCustomBrand != "Sesame Cloud" {
		t.Fatalf("Server.DesktopCustomBrand = %q, want %q", cfg.Server.DesktopCustomBrand, "Sesame Cloud")
	}
	if cfg.Server.DesktopCustomLogo != "https://cdn.example.com/logo.svg" {
		t.Fatalf("Server.DesktopCustomLogo = %q, want %q", cfg.Server.DesktopCustomLogo, "https://cdn.example.com/logo.svg")
	}
}

func TestEnvOverrideStorageClassS3(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Storage.Classes = map[string]StorageClassConfig{
		"hot-s3-na": {
			Type:   "s3",
			Tier:   "hot",
			Region: "us-east-1",
		},
	}

	os.Setenv("S3_CLASS_HOT_S3_NA_BUCKET", "sesamefs-na-prod")
	os.Setenv("S3_CLASS_HOT_S3_NA_ACCESS_KEY_ID", "class-key")
	os.Setenv("S3_CLASS_HOT_S3_NA_SECRET_ACCESS_KEY", "class-secret")
	os.Setenv("S3_CLASS_HOT_S3_NA_ENDPOINT", "https://s3.example.com")
	os.Setenv("S3_CLASS_HOT_S3_NA_SERVER_SIDE_ENCRYPTION", "AES256")
	os.Setenv("S3_ACCESS_KEY_ID", "default-key")
	os.Setenv("S3_SECRET_ACCESS_KEY", "default-secret")
	defer func() {
		os.Unsetenv("S3_CLASS_HOT_S3_NA_BUCKET")
		os.Unsetenv("S3_CLASS_HOT_S3_NA_ACCESS_KEY_ID")
		os.Unsetenv("S3_CLASS_HOT_S3_NA_SECRET_ACCESS_KEY")
		os.Unsetenv("S3_CLASS_HOT_S3_NA_ENDPOINT")
		os.Unsetenv("S3_CLASS_HOT_S3_NA_SERVER_SIDE_ENCRYPTION")
		os.Unsetenv("S3_ACCESS_KEY_ID")
		os.Unsetenv("S3_SECRET_ACCESS_KEY")
	}()

	cfg.applyEnvOverrides()

	classCfg := cfg.Storage.Classes["hot-s3-na"]
	if classCfg.Bucket != "sesamefs-na-prod" {
		t.Fatalf("Storage.Classes[hot-s3-na].Bucket = %q, want %q", classCfg.Bucket, "sesamefs-na-prod")
	}
	if classCfg.Endpoint != "https://s3.example.com" {
		t.Fatalf("Storage.Classes[hot-s3-na].Endpoint = %q, want %q", classCfg.Endpoint, "https://s3.example.com")
	}
	if classCfg.AccessKey != "class-key" {
		t.Fatalf("Storage.Classes[hot-s3-na].AccessKey = %q, want %q", classCfg.AccessKey, "class-key")
	}
	if classCfg.SecretKey != "class-secret" {
		t.Fatalf("Storage.Classes[hot-s3-na].SecretKey = %q, want %q", classCfg.SecretKey, "class-secret")
	}
	if classCfg.ServerSideEncryption != "AES256" {
		t.Fatalf("Storage.Classes[hot-s3-na].ServerSideEncryption = %q, want %q", classCfg.ServerSideEncryption, "AES256")
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

// TestEffectiveMaxStagedBytesPerSession covers the per-session staging ceiling
// derivation (docs/WEB-BLOCK-UPLOAD.md item 1): explicit MB, derive-from-max-file,
// unlimited-max-file fallback, and disabled.
func TestEffectiveMaxStagedBytesPerSession(t *testing.T) {
	const mb = int64(1024 * 1024)

	t.Run("explicit MB wins", func(t *testing.T) {
		c := DefaultConfig()
		c.WebUploads.MaxStagedBytesPerSessionMB = 500
		if got := c.EffectiveMaxStagedBytesPerSession(); got != 500*mb {
			t.Fatalf("got %d, want %d", got, 500*mb)
		}
	})

	t.Run("derive from max file size x1.25", func(t *testing.T) {
		c := DefaultConfig()
		c.WebUploads.MaxStagedBytesPerSessionMB = 0
		c.WebUploads.MaxFileSizeMB = 1000
		want := 1000 * mb * 5 / 4
		if got := c.EffectiveMaxStagedBytesPerSession(); got != want {
			t.Fatalf("got %d, want %d (max file x1.25)", got, want)
		}
	})

	t.Run("unlimited max file size falls back to the documented default", func(t *testing.T) {
		c := DefaultConfig()
		c.WebUploads.MaxStagedBytesPerSessionMB = 0
		c.WebUploads.MaxFileSizeMB = 0
		c.Server.MaxUploadMB = 0 // ResolvedMaxFileSizeMB -> 0 (unlimited)
		if got := c.EffectiveMaxStagedBytesPerSession(); got != DefaultMaxStagedBytesPerSession {
			t.Fatalf("got %d, want fallback %d", got, DefaultMaxStagedBytesPerSession)
		}
	})

	t.Run("negative disables", func(t *testing.T) {
		c := DefaultConfig()
		c.WebUploads.MaxStagedBytesPerSessionMB = -1
		if got := c.EffectiveMaxStagedBytesPerSession(); got != 0 {
			t.Fatalf("got %d, want 0 (disabled)", got)
		}
	})
}

// TestShippedConfigsSeafHTTPBoundsAreValid loads every config file we ship the way
// Load() does — defaults first, YAML on top — and checks the resulting seafhttp
// bounds against what Validate() enforces.
//
// This exists because these knobs reject values other seafhttp bounds accept:
// zero for the sync block cap, and a zero burst paired with a live rate for the
// upload-link limiters. Omission is still safe — the YAML loads on top of
// DefaultConfig(), so a file that never mentions a key inherits its default —
// but a file that sets one *explicitly* to an out-of-range value, or sets a rate
// and forgets its burst, would refuse to boot, and that is what this checks. The
// test asserts the properties directly rather than calling Validate(), which
// would also demand deployment secrets these files deliberately leave to .env.
func TestShippedConfigsSeafHTTPBoundsAreValid(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "configs", "*.yaml"))
	if err != nil {
		t.Fatalf("glob configs: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no shipped configs found; this test would pass vacuously")
	}

	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			cfg := DefaultConfig()
			if err := yaml.Unmarshal(data, cfg); err != nil {
				t.Fatalf("parse: %v", err)
			}

			got := cfg.SeafHTTP.SyncBlockMaxBytes
			if got <= 0 {
				t.Fatalf("sync_block_max_bytes = %d; zero is rejected by Validate(), so this config would refuse to boot", got)
			}
			if got > MaxSyncBlockMaxBytes {
				t.Fatalf("sync_block_max_bytes = %d, above the %d ceiling; this config would refuse to boot", got, MaxSyncBlockMaxBytes)
			}

			// The upload-link bounds have the same property: a live rate paired
			// with a zero burst is a boot failure, and a YAML file that sets one
			// and forgets the other is exactly how that happens.
			if err := validateUploadLinkWriteLimit(
				"upload_link_writes_per_minute", "upload_link_write_burst",
				cfg.SeafHTTP.UploadLinkWritesPerMinute, cfg.SeafHTTP.UploadLinkWriteBurst,
			); err != nil {
				t.Fatalf("this config would refuse to boot: %v", err)
			}
			if err := validateUploadLinkWriteLimit(
				"upload_link_source_writes_per_minute", "upload_link_source_write_burst",
				cfg.SeafHTTP.UploadLinkSourceWritesPerMinute, cfg.SeafHTTP.UploadLinkSourceWriteBurst,
			); err != nil {
				t.Fatalf("this config would refuse to boot: %v", err)
			}
			if got := cfg.SeafHTTP.UploadLinkMaxInflightPerSource; got != 16 {
				t.Fatalf("upload_link_max_inflight_per_source = %d, want shipped value 16", got)
			}
			if got := cfg.SeafHTTP.UploadLinkMaxInflightPerNode; got != 128 {
				t.Fatalf("upload_link_max_inflight_per_node = %d, want shipped value 128", got)
			}
		})
	}
}
