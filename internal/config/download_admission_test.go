package config

import (
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestDownloadAdmissionDefaultsUseMeasuredD6Values(t *testing.T) {
	d := DefaultConfig().DownloadAdmission
	want := defaultDownloadAdmissionConfig()
	if d != want {
		t.Fatalf("download admission defaults = %#v, want %#v", d, want)
	}
}

func TestLegacyConfigWithoutDownloadAdmissionInheritsD6Defaults(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Auth.DevMode = true
	if err := yaml.Unmarshal([]byte("server:\n  write_timeout: 0s\n"), cfg); err != nil {
		t.Fatalf("unmarshal legacy config: %v", err)
	}
	if got, want := cfg.DownloadAdmission, defaultDownloadAdmissionConfig(); got != want {
		t.Fatalf("legacy config download admission = %#v, want D6 defaults %#v", got, want)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("legacy config with inherited D6 defaults failed validation: %v", err)
	}
}

// legacyD1PlaceholderYAML is the section the D1-D5 templates actually shipped,
// copied from configs/config.docker.yaml at aa083805c~1. Inheriting defaults
// cannot reach a file that contains it, which is the upgrade the test below
// exists for.
const legacyD1PlaceholderYAML = `
download_admission:
  enabled: false
  max_active_per_node: 0
  max_active_per_auth_user: 0
  max_active_per_link_source: 0
  max_active_per_client_link: 0
  max_waiters_per_identity: 0
  max_waiters_per_node: 0
  admission_wait: 0s
  preparation_deadline: 0s
  idle_write_timeout: 0s
  retry_after: 0s
  max_active_block: 0
  max_active_file: 0
  max_active_raw: 0
  max_active_history: 0
  max_active_link_raw: 0
  max_active_zip: 0
  max_active_link_inline: 0
`

func TestLegacyD1PlaceholderIsRefusedRatherThanSilentlyUnprotected(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Auth.DevMode = true
	if err := yaml.Unmarshal([]byte(legacyD1PlaceholderYAML), cfg); err != nil {
		t.Fatalf("unmarshal legacy placeholder: %v", err)
	}
	if cfg.DownloadAdmission != (DownloadAdmissionConfig{}) {
		t.Fatalf("fixture no longer reproduces the shipped placeholder: %#v", cfg.DownloadAdmission)
	}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("the D1-D5 placeholder validated; that deployment starts with no aggregate download bound")
	}
	if !strings.Contains(err.Error(), "staging placeholder") {
		t.Fatalf("Validate() error = %q, want the placeholder to be named so the operator knows which edit to make", err)
	}
}

// TestExplicitDownloadAdmissionOptOutStillValidates is the other half: refusing
// the placeholder must not turn `enabled: false` into an unsupported state. An
// operator who deliberately opts out keeps the measured values beside it, and
// that is a configuration the server must still start on.
func TestExplicitDownloadAdmissionOptOutStillValidates(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Auth.DevMode = true
	if err := yaml.Unmarshal([]byte("download_admission:\n  enabled: false\n"), cfg); err != nil {
		t.Fatalf("unmarshal opt-out: %v", err)
	}
	if cfg.DownloadAdmission.Enabled {
		t.Fatal("explicit enabled: false was overridden; it is the documented opt-out")
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("explicit opt-out failed validation: %v", err)
	}
}

// TestLegacyD1PlaceholderRepairedByEnvValidates pins the ordering. The check
// belongs after applyEnvOverrides: a deployment that carries the old YAML but
// supplies real values through the environment is already protected, and
// refusing it would reject a correct configuration.
func TestLegacyD1PlaceholderRepairedByEnvValidates(t *testing.T) {
	clearLoadEnvOverrides(t)
	cfg := DefaultConfig()
	cfg.Auth.DevMode = true
	if err := yaml.Unmarshal([]byte(legacyD1PlaceholderYAML), cfg); err != nil {
		t.Fatalf("unmarshal legacy placeholder: %v", err)
	}

	t.Setenv("DOWNLOAD_ADMISSION_ENABLED", "true")
	t.Setenv("DOWNLOAD_ADMISSION_MAX_ACTIVE_PER_NODE", "6")
	t.Setenv("DOWNLOAD_ADMISSION_MAX_ACTIVE_PER_AUTH_USER", "6")
	t.Setenv("DOWNLOAD_ADMISSION_MAX_ACTIVE_PER_LINK_SOURCE", "6")
	t.Setenv("DOWNLOAD_ADMISSION_MAX_ACTIVE_PER_CLIENT_LINK", "3")
	t.Setenv("DOWNLOAD_ADMISSION_PREPARATION_DEADLINE", "60s")
	t.Setenv("DOWNLOAD_ADMISSION_IDLE_WRITE_TIMEOUT", "60s")
	t.Setenv("DOWNLOAD_ADMISSION_RETRY_AFTER", "10s")
	cfg.applyEnvOverrides()

	if err := cfg.Validate(); err != nil {
		t.Fatalf("placeholder repaired through the environment failed validation: %v", err)
	}
}

func TestDownloadAdmissionValidation(t *testing.T) {
	valid := DownloadAdmissionConfig{
		Enabled:                true,
		MaxActivePerNode:       8,
		MaxActivePerAuthUser:   2,
		MaxActivePerLinkSource: 4,
		MaxActivePerClientLink: 2,
		MaxWaitersPerIdentity:  4,
		MaxWaitersPerNode:      8,
		AdmissionWait:          10 * time.Second,
		PreparationDeadline:    10 * time.Minute,
		IdleWriteTimeout:       5 * time.Minute,
		RetryAfter:             time.Minute,
	}
	cases := []struct {
		name       string
		modify     func(*Config)
		wantErr    bool
		wantString string
	}{
		{name: "valid", modify: func(c *Config) { c.DownloadAdmission = valid }},
		{
			name: "negative active cap",
			modify: func(c *Config) {
				c.DownloadAdmission = valid
				c.DownloadAdmission.MaxActivePerNode = -1
			},
			wantErr:    true,
			wantString: "max_active_per_node",
		},
		{
			name: "active cap ceiling",
			modify: func(c *Config) {
				c.DownloadAdmission = valid
				c.DownloadAdmission.MaxActivePerNode = MaxDownloadAdmissionActive + 1
			},
			wantErr:    true,
			wantString: "above",
		},
		{
			name: "waiter cap ceiling",
			modify: func(c *Config) {
				c.DownloadAdmission = valid
				c.DownloadAdmission.MaxWaitersPerNode = MaxDownloadAdmissionWaitersPerNode + 1
			},
			wantErr:    true,
			wantString: "max_waiters_per_node",
		},
		{
			name: "duration ceiling",
			modify: func(c *Config) {
				c.DownloadAdmission = valid
				c.DownloadAdmission.RetryAfter = MaxDownloadAdmissionRetryAfter + time.Second
			},
			wantErr:    true,
			wantString: "retry_after",
		},
		{
			name: "enabled requires positive caps",
			modify: func(c *Config) {
				c.DownloadAdmission = DownloadAdmissionConfig{
					Enabled:              true,
					MaxActivePerNode:     1,
					MaxActivePerAuthUser: 1,
				}
			},
			wantErr:    true,
			wantString: "greater than zero",
		},
		{
			name: "identity cap exceeds node",
			modify: func(c *Config) {
				c.DownloadAdmission = valid
				c.DownloadAdmission.MaxActivePerAuthUser = valid.MaxActivePerNode + 1
			},
			wantErr:    true,
			wantString: "must not exceed max_active_per_node",
		},
		{
			name: "identity cap requires node",
			modify: func(c *Config) {
				c.DownloadAdmission = valid
				c.DownloadAdmission.MaxActivePerNode = 0
				c.DownloadAdmission.MaxActivePerAuthUser = 1
			},
			wantErr:    true,
			wantString: "must not exceed max_active_per_node",
		},
		{
			name: "client link exceeds source",
			modify: func(c *Config) {
				c.DownloadAdmission = valid
				c.DownloadAdmission.MaxActivePerClientLink = valid.MaxActivePerLinkSource + 1
			},
			wantErr:    true,
			wantString: "max_active_per_client_link",
		},
		{
			name: "write timeout incompatible",
			modify: func(c *Config) {
				c.DownloadAdmission = valid
				c.Server.WriteTimeout = time.Second
			},
			wantErr:    true,
			wantString: "server.write_timeout",
		},
		{
			name: "enabled admission requires a positive iWork source cap",
			modify: func(c *Config) {
				c.DownloadAdmission = valid
				c.FileView.MaxIWorkSourceBytes = 0
			},
			wantErr:    true,
			wantString: "max_iwork_source_bytes",
		},
		{
			name: "iWork source cap cannot exceed general preview cap",
			modify: func(c *Config) {
				c.DownloadAdmission = valid
				c.FileView.MaxIWorkSourceBytes = c.FileView.MaxPreviewBytes + 1
			},
			wantErr:    true,
			wantString: "must not exceed",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Auth.DevMode = true
			tc.modify(cfg)
			err := cfg.Validate()
			if tc.wantErr != (err != nil) {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tc.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), tc.wantString) {
				t.Fatalf("Validate() error = %q, want substring %q", err, tc.wantString)
			}
		})
	}
}

func d6MemoryBudgetConfig() *Config {
	cfg := DefaultConfig()
	cfg.Server.WriteTimeout = 0
	cfg.DownloadAdmission = DownloadAdmissionConfig{
		Enabled:                true,
		MaxActivePerNode:       18,
		MaxActivePerAuthUser:   6,
		MaxActivePerLinkSource: 6,
		MaxActivePerClientLink: 3,
		MaxWaitersPerIdentity:  4,
		MaxWaitersPerNode:      24,
		AdmissionWait:          2 * time.Second,
		PreparationDeadline:    60 * time.Second,
		IdleWriteTimeout:       60 * time.Second,
		RetryAfter:             10 * time.Second,
		MaxActiveBlock:         16,
		MaxActiveFile:          16,
		MaxActiveRaw:           6,
		MaxActiveHistory:       6,
		MaxActiveLinkRaw:       12,
		MaxActiveZIP:           4,
		MaxActiveLinkInline:    8,
	}
	cfg.SeafHTTP.SyncBlockMaxBytes = DefaultSyncBlockMaxBytes
	cfg.FileView.MaxIWorkSourceBytes = 32 * 1024 * 1024
	return cfg
}

func TestDownloadAdmissionMemoryBudgetBoundaries(t *testing.T) {
	cases := []struct {
		name       string
		modify     func(*Config)
		wantErr    bool
		wantString string
	}{
		{name: "shipped 18 slots at 16 MiB", wantErr: false},
		{
			name: "node cap 19 exceeds budget",
			modify: func(cfg *Config) {
				cfg.DownloadAdmission.MaxActivePerNode = 19
			},
			wantErr:    true,
			wantString: "memory design",
		},
		{
			name: "32 MiB sync block exceeds budget",
			modify: func(cfg *Config) {
				cfg.SeafHTTP.SyncBlockMaxBytes = 32 * 1024 * 1024
			},
			wantErr:    true,
			wantString: "memory design",
		},
		{
			name: "zero iWork source cap",
			modify: func(cfg *Config) {
				cfg.FileView.MaxIWorkSourceBytes = 0
			},
			wantErr:    true,
			wantString: "greater than zero",
		},
		{
			name: "iWork source cap exceeds preview cap",
			modify: func(cfg *Config) {
				cfg.FileView.MaxIWorkSourceBytes = cfg.FileView.MaxPreviewBytes + 1
			},
			wantErr:    true,
			wantString: "must not exceed",
		},
		{
			name: "zero raw profile cap charges every node slot",
			modify: func(cfg *Config) {
				cfg.DownloadAdmission.MaxActiveRaw = 0
			},
			wantErr:    true,
			wantString: "memory design",
		},
		{
			name: "small iWork source cap cannot undercharge raw streams",
			modify: func(cfg *Config) {
				cfg.DownloadAdmission.MaxActivePerNode = 100
				cfg.DownloadAdmission.MaxActiveRaw = 0
				cfg.FileView.MaxIWorkSourceBytes = 1 * 1024 * 1024
			},
			wantErr:    true,
			wantString: "memory design",
		},
		{
			name: "preview output cap participates in raw cost",
			modify: func(cfg *Config) {
				cfg.FileView.MaxIWorkPreviewBytes = 100 * 1024 * 1024
			},
			wantErr:    true,
			wantString: "memory design",
		},
		{
			// The guard names the offending key rather than letting the sum wrap
			// and reporting a raw-slot cost that looks affordable. Without it the
			// addition overflows negative, the preview term loses to the iWork
			// term, and the whole design passes at the shipped values.
			name: "preview output overflow cannot wrap the budget",
			modify: func(cfg *Config) {
				cfg.FileView.MaxIWorkPreviewBytes = int64(^uint64(0) >> 1)
			},
			wantErr:    true,
			wantString: "max_iwork_preview_bytes=9223372036854775807 overflows",
		},
		{
			name: "plaintext stream floor applies when block is tiny",
			modify: func(cfg *Config) {
				cfg.DownloadAdmission.MaxActivePerNode = 500
				cfg.SeafHTTP.SyncBlockMaxBytes = 1
			},
			wantErr:    true,
			wantString: "other slots at 4194304",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := d6MemoryBudgetConfig()
			if tc.modify != nil {
				tc.modify(cfg)
			}
			err := cfg.validateDownloadAdmissionBounds()
			if tc.wantErr != (err != nil) {
				t.Fatalf("validateDownloadAdmissionBounds() error = %v, wantErr %v", err, tc.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), tc.wantString) {
				t.Fatalf("validateDownloadAdmissionBounds() error = %q, want substring %q", err, tc.wantString)
			}
		})
	}
}

func TestDownloadAdmissionEnvironmentOverrides(t *testing.T) {
	clearLoadEnvOverrides(t)
	cfg := DefaultConfig()
	t.Setenv("DOWNLOAD_ADMISSION_ENABLED", "true")
	t.Setenv("DOWNLOAD_ADMISSION_MAX_ACTIVE_PER_NODE", "8")
	t.Setenv("DOWNLOAD_ADMISSION_MAX_ACTIVE_PER_AUTH_USER", "2")
	t.Setenv("DOWNLOAD_ADMISSION_ADMISSION_WAIT", "1500ms")
	t.Setenv("DOWNLOAD_ADMISSION_PREPARATION_DEADLINE", "10m")
	t.Setenv("DOWNLOAD_ADMISSION_IDLE_WRITE_TIMEOUT", "5m")
	t.Setenv("DOWNLOAD_ADMISSION_RETRY_AFTER", "1m")
	cfg.applyEnvOverrides()

	if !cfg.DownloadAdmission.Enabled || cfg.DownloadAdmission.MaxActivePerNode != 8 || cfg.DownloadAdmission.MaxActivePerAuthUser != 2 {
		t.Fatalf("active env overrides = %#v", cfg.DownloadAdmission)
	}
	if cfg.DownloadAdmission.AdmissionWait != 1500*time.Millisecond || cfg.DownloadAdmission.PreparationDeadline != 10*time.Minute || cfg.DownloadAdmission.IdleWriteTimeout != 5*time.Minute || cfg.DownloadAdmission.RetryAfter != time.Minute {
		t.Fatalf("duration env overrides = %#v", cfg.DownloadAdmission)
	}
	if len(cfg.envOverrideErrors) != 0 {
		t.Fatalf("unexpected env override errors: %v", cfg.envOverrideErrors)
	}
}

func TestDownloadAdmissionEnvironmentOverrideErrors(t *testing.T) {
	clearLoadEnvOverrides(t)
	cfg := DefaultConfig()
	t.Setenv("DOWNLOAD_ADMISSION_ENABLED", "maybe")
	t.Setenv("DOWNLOAD_ADMISSION_MAX_ACTIVE_PER_NODE", "not-an-int")
	t.Setenv("DOWNLOAD_ADMISSION_RETRY_AFTER", "not-a-duration")
	cfg.applyEnvOverrides()
	if len(cfg.envOverrideErrors) != 3 {
		t.Fatalf("env override errors = %v, want three errors", cfg.envOverrideErrors)
	}
}

func TestIWorkSourceEnvironmentOverrideKeepsTheD6GuardValidated(t *testing.T) {
	for _, tc := range []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "valid", value: "33554432"},
		{name: "zero", value: "0", wantErr: true},
		{name: "negative", value: "-1", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearLoadEnvOverrides(t)
			cfg := DefaultConfig()
			cfg.Auth.DevMode = true
			cfg.DownloadAdmission = DownloadAdmissionConfig{
				Enabled:                true,
				MaxActivePerNode:       8,
				MaxActivePerAuthUser:   2,
				MaxActivePerLinkSource: 4,
				MaxActivePerClientLink: 2,
				AdmissionWait:          10 * time.Second,
				PreparationDeadline:    time.Minute,
				IdleWriteTimeout:       time.Minute,
				RetryAfter:             time.Second,
			}
			t.Setenv("FILEVIEW_MAX_IWORK_SOURCE_BYTES", tc.value)
			cfg.applyEnvOverrides()
			err := cfg.Validate()
			if tc.wantErr != (err != nil) {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tc.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), "max_iwork_source_bytes") {
				t.Fatalf("Validate() error = %v, want iWork source cap failure", err)
			}
		})
	}
}
