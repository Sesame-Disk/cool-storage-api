package config

import (
	"fmt"
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

func TestConfigWithoutDownloadAdmissionInheritsD6Defaults(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Auth.DevMode = true
	if err := yaml.Unmarshal([]byte("server:\n  write_timeout: 0s\n"), cfg); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}
	if got, want := cfg.DownloadAdmission, defaultDownloadAdmissionConfig(); got != want {
		t.Fatalf("config that omits the section = %#v, want the D6 defaults %#v", got, want)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config with inherited D6 defaults failed validation: %v", err)
	}
}

// zeroedDownloadAdmissionYAML is a disabled section written out with every value
// at zero — the shape a hand-written opt-out most plausibly takes, and the one
// the D1-D5 templates carried before D6 measured the values.
const zeroedDownloadAdmissionYAML = `
download_admission:
  enabled: false
  capacity_mode: manual
  memory_budget_bytes: 0
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

func TestZeroedDisabledDownloadAdmissionIsRefused(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Auth.DevMode = true
	if err := yaml.Unmarshal([]byte(zeroedDownloadAdmissionYAML), cfg); err != nil {
		t.Fatalf("unmarshal zeroed section: %v", err)
	}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("a zeroed disabled section validated; flipping enabled: true would not start")
	}
	if !strings.Contains(err.Error(), "structurally incomplete configuration") {
		t.Fatalf("Validate() error = %q, want the incomplete-opt-out explanation", err)
	}
}

// TestPartiallyFilledDisabledDownloadAdmissionIsRefused is why the rule is
// completeness rather than a fingerprint of any particular shape. A single
// changed field — one DOWNLOAD_ADMISSION_* override is enough — would defeat an
// exact-match check while leaving the section just as unusable.
func TestPartiallyFilledDisabledDownloadAdmissionIsRefused(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Auth.DevMode = true
	if err := yaml.Unmarshal([]byte(zeroedDownloadAdmissionYAML), cfg); err != nil {
		t.Fatalf("unmarshal zeroed section: %v", err)
	}
	cfg.DownloadAdmission.RetryAfter = 10 * time.Second
	if cfg.DownloadAdmission == (DownloadAdmissionConfig{}) {
		t.Fatal("fixture still equals the zero struct; it no longer tests the partial case")
	}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("a partially filled disabled section validated")
	}
	if !strings.Contains(err.Error(), "structurally incomplete configuration") {
		t.Fatalf("Validate() error = %q, want the incomplete-opt-out explanation", err)
	}
}

// TestDisabledDownloadAdmissionMissingOneCapIsRefused states the rule as a
// property: any disabled section that could not be switched on as written is
// refused, whichever single value is missing.
func TestDisabledDownloadAdmissionMissingOneCapIsRefused(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Auth.DevMode = true
	cfg.DownloadAdmission.CapacityMode = "manual"
	cfg.DownloadAdmission.MemoryBudgetBytes = DefaultDownloadAdmissionMemoryBudgetBytes
	cfg.DownloadAdmission.Enabled = false
	cfg.DownloadAdmission.MaxActivePerNode = 0

	if err := cfg.Validate(); err == nil {
		t.Fatal("a disabled section with no node cap validated; flipping enabled would not start")
	}
}

func TestDisabledDownloadAdmissionDefersMemoryBudgetUntilEnabled(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Auth.DevMode = true
	cfg.DownloadAdmission.CapacityMode = "manual"
	cfg.DownloadAdmission.MemoryBudgetBytes = DefaultDownloadAdmissionMemoryBudgetBytes
	cfg.DownloadAdmission.Enabled = false
	cfg.DownloadAdmission.MaxActivePerNode = 19

	if err := cfg.Validate(); err != nil {
		t.Fatalf("disabled admission unexpectedly enforced memory budget: %v", err)
	}

	cfg.DownloadAdmission.Enabled = true
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "memory design") {
		t.Fatalf("enabled admission did not enforce memory budget: %v", err)
	}
}

func TestDisabledDownloadAdmissionWithZeroMemoryBudgetIsRefused(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Auth.DevMode = true
	cfg.DownloadAdmission.CapacityMode = "manual"
	cfg.DownloadAdmission.Enabled = false
	cfg.DownloadAdmission.MemoryBudgetBytes = 0

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "memory_budget_bytes") {
		t.Fatalf("zero disabled memory budget error = %v, want structural memory budget error", err)
	}
}

// TestDisabledDownloadAdmissionDoesNotConstrainOtherSubsystems pins the scope of
// the completeness rule against the obvious extension of it: also charging the
// disabled section against the 2 GiB memory design. That design multiplies the
// caps by values other subsystems own, so the extension refuses configurations
// that are correct on their own terms — the sanctioned 64 MiB sync block ceiling
// below stops the server from booting for a reason about downloads it is not
// admitting, and the refusal lands before subcontract B's own, more precise
// error. An unusable section is this rule's business; an unusable design is
// caught at the flip.
func TestDisabledDownloadAdmissionDoesNotConstrainOtherSubsystems(t *testing.T) {
	for _, tc := range []struct {
		name   string
		modify func(*Config)
	}{
		{"sync block at its sanctioned ceiling", func(cfg *Config) {
			// Sized the way subcontract B's own validator requires at that
			// ceiling, so the only thing left that could reject it is D.
			cfg.SeafHTTP.SyncBlockMaxBytes = MaxSyncBlockMaxBytes
			cfg.SeafHTTP.SyncBlockMaxInflightPerNode = 6
			cfg.SeafHTTP.SyncBlockMaxInflightPerUser = 6
		}},
		{"no iWork source cap", func(cfg *Config) {
			cfg.FileView.MaxIWorkSourceBytes = 0
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Auth.DevMode = true
			cfg.DownloadAdmission.CapacityMode = "manual"
			cfg.DownloadAdmission.MemoryBudgetBytes = DefaultDownloadAdmissionMemoryBudgetBytes
			cfg.DownloadAdmission.Enabled = false
			tc.modify(cfg)

			if err := cfg.Validate(); err != nil {
				t.Fatalf("Validate() = %v; a disabled download guard must not veto another subsystem's valid value", err)
			}
		})
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

// TestZeroedDownloadAdmissionCompletedByEnvValidates pins the ordering. The
// check belongs after applyEnvOverrides: a deployment whose YAML zeroes the
// section but supplies real values through the environment is protected, and
// refusing it would reject a correct configuration.
func TestZeroedDownloadAdmissionCompletedByEnvValidates(t *testing.T) {
	clearLoadEnvOverrides(t)
	cfg := DefaultConfig()
	cfg.Auth.DevMode = true
	if err := yaml.Unmarshal([]byte(zeroedDownloadAdmissionYAML), cfg); err != nil {
		t.Fatalf("unmarshal zeroed section: %v", err)
	}

	t.Setenv("DOWNLOAD_ADMISSION_ENABLED", "true")
	t.Setenv("DOWNLOAD_ADMISSION_CAPACITY_MODE", "manual")
	t.Setenv("DOWNLOAD_ADMISSION_MAX_ACTIVE_PER_NODE", "6")
	t.Setenv("DOWNLOAD_ADMISSION_MAX_ACTIVE_PER_AUTH_USER", "6")
	t.Setenv("DOWNLOAD_ADMISSION_MAX_ACTIVE_PER_LINK_SOURCE", "6")
	t.Setenv("DOWNLOAD_ADMISSION_MAX_ACTIVE_PER_CLIENT_LINK", "3")
	t.Setenv("DOWNLOAD_ADMISSION_MEMORY_BUDGET_BYTES", "2147483648")
	t.Setenv("DOWNLOAD_ADMISSION_PREPARATION_DEADLINE", "60s")
	t.Setenv("DOWNLOAD_ADMISSION_IDLE_WRITE_TIMEOUT", "60s")
	t.Setenv("DOWNLOAD_ADMISSION_RETRY_AFTER", "10s")
	cfg.applyEnvOverrides()

	if err := cfg.Validate(); err != nil {
		t.Fatalf("zeroed section completed through the environment failed validation: %v", err)
	}
}

func TestDownloadAdmissionValidation(t *testing.T) {
	valid := DownloadAdmissionConfig{
		Enabled:                true,
		CapacityMode:           "manual",
		MemoryBudgetPercent:    DefaultDownloadAdmissionMemoryBudgetPercent,
		RawCapacityPercent:     DefaultDownloadAdmissionRawCapacityPercent,
		SafetyMarginPercent:    DefaultDownloadAdmissionSafetyMarginPercent,
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
		MemoryBudgetBytes:      DefaultDownloadAdmissionMemoryBudgetBytes,
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
					CapacityMode:         "manual",
					MemoryBudgetPercent:  DefaultDownloadAdmissionMemoryBudgetPercent,
					RawCapacityPercent:   DefaultDownloadAdmissionRawCapacityPercent,
					SafetyMarginPercent:  DefaultDownloadAdmissionSafetyMarginPercent,
					MaxActivePerNode:     1,
					MaxActivePerAuthUser: 1,
					MemoryBudgetBytes:    DefaultDownloadAdmissionMemoryBudgetBytes,
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
		CapacityMode:           "manual",
		MemoryBudgetPercent:    DefaultDownloadAdmissionMemoryBudgetPercent,
		RawCapacityPercent:     DefaultDownloadAdmissionRawCapacityPercent,
		SafetyMarginPercent:    DefaultDownloadAdmissionSafetyMarginPercent,
		MaxActivePerNode:       16,
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
		MaxActiveRaw:           4,
		MaxActiveHistory:       6,
		MaxActiveLinkRaw:       12,
		MaxActiveZIP:           4,
		MaxActiveLinkInline:    8,
		MemoryBudgetBytes:      DefaultDownloadAdmissionMemoryBudgetBytes,
	}
	cfg.SeafHTTP.SyncBlockMaxBytes = DefaultSyncBlockMaxBytes
	cfg.FileView.MaxIWorkSourceBytes = 32 * 1024 * 1024
	return cfg
}

func TestDownloadAdmissionAutoCapacityDerivesFromBudget(t *testing.T) {
	// Pin the seam. Auto capacity is a function of the host's memory limit, so
	// without this the assertions below pass on a laptop and fail on any CI
	// agent with a cgroup limit other than 8 GiB — a failure that says nothing
	// about the change under test.
	withCgroupMemoryLimit(t, 0, false)

	cfg := DefaultConfig()
	cfg.Auth.DevMode = true
	if err := cfg.Validate(); err != nil {
		t.Fatalf("auto capacity validation failed: %v", err)
	}
	if cfg.DownloadAdmission.MemoryBudgetBytes != DefaultDownloadAdmissionMemoryBudgetBytes {
		t.Fatalf("auto memory budget = %d, want fallback %d", cfg.DownloadAdmission.MemoryBudgetBytes, DefaultDownloadAdmissionMemoryBudgetBytes)
	}
	if cfg.DownloadAdmission.MaxActivePerNode != 16 || cfg.DownloadAdmission.MaxActiveRaw != 4 {
		t.Fatalf("auto capacities = node %d/raw %d, want 16/4", cfg.DownloadAdmission.MaxActivePerNode, cfg.DownloadAdmission.MaxActiveRaw)
	}
	if cfg.DownloadAdmission.MaxActivePerAuthUser != 6 || cfg.DownloadAdmission.MaxActivePerClientLink != 3 {
		t.Fatalf("auto fairness caps = user %d/client-link %d, want 6/3", cfg.DownloadAdmission.MaxActivePerAuthUser, cfg.DownloadAdmission.MaxActivePerClientLink)
	}
}

func TestDownloadAdmissionAutoCapacityProfiles(t *testing.T) {
	const mib = int64(1024 * 1024)
	for _, tc := range []struct {
		budget    int64
		wantRaw   int
		wantOther int
		wantNode  int
	}{
		{budget: 512 * mib, wantRaw: 1, wantOther: 3, wantNode: 4},
		{budget: 1 * 1024 * mib, wantRaw: 2, wantOther: 6, wantNode: 8},
		{budget: 2 * 1024 * mib, wantRaw: 4, wantOther: 12, wantNode: 16},
		{budget: 4 * 1024 * mib, wantRaw: 8, wantOther: 24, wantNode: 32},
	} {
		t.Run(fmt.Sprintf("%dMiB", tc.budget/mib), func(t *testing.T) {
			effective, err := downloadAdmissionEffectiveBudget(tc.budget, DefaultDownloadAdmissionSafetyMarginPercent)
			if err != nil {
				t.Fatalf("effective budget: %v", err)
			}
			raw, other, node, ok := deriveDownloadAdmissionSlots(effective, 192*mib, 72*mib, 33)
			if !ok || raw != tc.wantRaw || other != tc.wantOther || node != tc.wantNode {
				t.Fatalf("derived slots = (%d raw, %d other, %d node, %t), want (%d, %d, %d, true)", raw, other, node, ok, tc.wantRaw, tc.wantOther, tc.wantNode)
			}
		})
	}
}

// TestDownloadAdmissionSafetyMarginGovernsBothPhases is the regression for a
// knob that meant two different things. Derivation scaled the budget by the
// configured margin while the final validation scaled it by a hardcoded 4/5, so
// every margin below 20 derived a design the validator then refused: the server
// would not start on capacities it had computed itself.
func TestDownloadAdmissionSafetyMarginGovernsBothPhases(t *testing.T) {
	withCgroupMemoryLimit(t, 0, false)
	for _, margin := range []int{0, 5, 10, 20, 50} {
		t.Run(fmt.Sprintf("margin%d", margin), func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Auth.DevMode = true
			cfg.DownloadAdmission.SafetyMarginPercent = margin

			if err := cfg.Validate(); err != nil {
				t.Fatalf("safety_margin_percent=%d derived a design its own validator refused: %v", margin, err)
			}
			if cfg.DownloadAdmission.MaxActivePerNode < 2 {
				t.Fatalf("margin %d derived only %d slots", margin, cfg.DownloadAdmission.MaxActivePerNode)
			}
		})
	}
}

// TestDownloadAdmissionBudgetPercentAgreesWithTheCgroupGuard covers the same
// shape on the other percentage: the guard enforced a fixed 25% while the
// setting accepted up to 100, so anything larger derived a budget the guard
// rejected on any host that exposes a limit.
func TestDownloadAdmissionBudgetPercentAgreesWithTheCgroupGuard(t *testing.T) {
	const limit = int64(8 * 1024 * 1024 * 1024)
	for _, percent := range []int{10, 25, 40, MaxDownloadAdmissionMemoryBudgetPercent} {
		t.Run(fmt.Sprintf("percent%d", percent), func(t *testing.T) {
			withCgroupMemoryLimit(t, limit, true)
			cfg := DefaultConfig()
			cfg.Auth.DevMode = true
			cfg.DownloadAdmission.MemoryBudgetPercent = percent

			if err := cfg.Validate(); err != nil {
				t.Fatalf("memory_budget_percent=%d derived a budget its own guard refused: %v", percent, err)
			}
		})
	}

	t.Run("above the ceiling is refused by the range, not at startup", func(t *testing.T) {
		withCgroupMemoryLimit(t, limit, true)
		cfg := DefaultConfig()
		cfg.Auth.DevMode = true
		cfg.DownloadAdmission.MemoryBudgetPercent = MaxDownloadAdmissionMemoryBudgetPercent + 1

		err := cfg.Validate()
		if err == nil || !strings.Contains(err.Error(), "memory_budget_percent") {
			t.Fatalf("Validate() = %v, want the range to name the setting", err)
		}
	})
}

// TestDownloadAdmissionRawCapacityPercentIsMonotonic pins the knob against the
// fixed nodeSlots/4 floor it used to carry, which made a smaller requested raw
// share collapse the node total (1% gave 7 slots where 33% gave 16) and made
// every share at or above 25% derive the same split.
func TestDownloadAdmissionRawCapacityPercentIsMonotonic(t *testing.T) {
	withCgroupMemoryLimit(t, 0, false)
	type derived struct{ node, raw int }
	got := map[int]derived{}
	for _, percent := range []int{10, 25, 50, 99} {
		cfg := DefaultConfig()
		cfg.Auth.DevMode = true
		cfg.DownloadAdmission.RawCapacityPercent = percent
		if err := cfg.Validate(); err != nil {
			t.Fatalf("raw_capacity_percent=%d: %v", percent, err)
		}
		got[percent] = derived{cfg.DownloadAdmission.MaxActivePerNode, cfg.DownloadAdmission.MaxActiveRaw}
	}

	if got[10].node <= got[50].node {
		t.Fatalf("a smaller raw share must buy more slots: 10%% gave %d, 50%% gave %d", got[10].node, got[50].node)
	}
	if got[99].raw <= got[25].raw {
		t.Fatalf("a larger raw share must reserve more raw slots: 99%% gave %d, 25%% gave %d", got[99].raw, got[25].raw)
	}
}

// TestDownloadAdmissionAutoModeKeepsZeroWaiters protects the documented
// queueing contract. Zero means refuse immediately; auto mode floored it to one
// and discarded the configured node value entirely, quietly turning "no queue"
// into a queue.
func TestDownloadAdmissionAutoModeKeepsZeroWaiters(t *testing.T) {
	withCgroupMemoryLimit(t, 0, false)
	cfg := DefaultConfig()
	cfg.Auth.DevMode = true
	cfg.DownloadAdmission.MaxWaitersPerIdentity = 0
	cfg.DownloadAdmission.MaxWaitersPerNode = 0

	if err := cfg.Validate(); err != nil {
		t.Fatalf("zero waiters: %v", err)
	}
	if cfg.DownloadAdmission.MaxWaitersPerIdentity != 0 || cfg.DownloadAdmission.MaxWaitersPerNode != 0 {
		t.Fatalf("waiters = %d/%d, want the configured 0/0 to survive derivation",
			cfg.DownloadAdmission.MaxWaitersPerIdentity, cfg.DownloadAdmission.MaxWaitersPerNode)
	}
}

func TestDownloadAdmissionMemoryBudgetBoundaries(t *testing.T) {
	cases := []struct {
		name       string
		modify     func(*Config)
		wantErr    bool
		wantString string
	}{
		{name: "shipped 16 slots at 16 MiB", wantErr: false},
		{
			name: "node cap 17 exceeds budget",
			modify: func(cfg *Config) {
				cfg.DownloadAdmission.MaxActivePerNode = 17
			},
			wantErr:    true,
			wantString: "memory design",
		},
		{
			name: "deployment budget can be raised explicitly",
			modify: func(cfg *Config) {
				cfg.DownloadAdmission.MaxActivePerNode = 17
				cfg.DownloadAdmission.MemoryBudgetBytes = 3 * 1024 * 1024 * 1024
			},
			wantErr: false,
		},
		{
			name: "zero deployment budget",
			modify: func(cfg *Config) {
				cfg.DownloadAdmission.MemoryBudgetBytes = 0
			},
			wantErr:    true,
			wantString: "memory_budget_bytes must be between 1",
		},
		{
			name: "deployment budget above operator ceiling",
			modify: func(cfg *Config) {
				cfg.DownloadAdmission.MemoryBudgetBytes = MaxDownloadAdmissionMemoryBudgetBytes + 1
			},
			wantErr:    true,
			wantString: "memory_budget_bytes must be between 1",
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

func TestDownloadAdmissionMemoryArithmeticRejectsOverflow(t *testing.T) {
	if got, ok := checkedNonNegativeMultiply(1<<62, 4); ok || got != 0 {
		t.Fatalf("overflowing multiplication = (%d, %t), want (0, false)", got, ok)
	}
	if got, ok := checkedNonNegativeAdd((1<<63)-1, 1); ok || got != 0 {
		t.Fatalf("overflowing addition = (%d, %t), want (0, false)", got, ok)
	}
}

func TestDownloadAdmissionEnvironmentOverrides(t *testing.T) {
	clearLoadEnvOverrides(t)
	cfg := DefaultConfig()
	t.Setenv("DOWNLOAD_ADMISSION_ENABLED", "true")
	t.Setenv("DOWNLOAD_ADMISSION_CAPACITY_MODE", "manual")
	t.Setenv("DOWNLOAD_ADMISSION_MEMORY_BUDGET_PERCENT", "30")
	t.Setenv("DOWNLOAD_ADMISSION_RAW_CAPACITY_PERCENT", "35")
	t.Setenv("DOWNLOAD_ADMISSION_SAFETY_MARGIN_PERCENT", "15")
	t.Setenv("DOWNLOAD_ADMISSION_MAX_ACTIVE_PER_NODE", "8")
	t.Setenv("DOWNLOAD_ADMISSION_MAX_ACTIVE_PER_AUTH_USER", "2")
	t.Setenv("DOWNLOAD_ADMISSION_MEMORY_BUDGET_BYTES", "1073741824")
	t.Setenv("DOWNLOAD_ADMISSION_ADMISSION_WAIT", "1500ms")
	t.Setenv("DOWNLOAD_ADMISSION_PREPARATION_DEADLINE", "10m")
	t.Setenv("DOWNLOAD_ADMISSION_IDLE_WRITE_TIMEOUT", "5m")
	t.Setenv("DOWNLOAD_ADMISSION_RETRY_AFTER", "1m")
	cfg.applyEnvOverrides()

	if !cfg.DownloadAdmission.Enabled || cfg.DownloadAdmission.MaxActivePerNode != 8 || cfg.DownloadAdmission.MaxActivePerAuthUser != 2 {
		t.Fatalf("active env overrides = %#v", cfg.DownloadAdmission)
	}
	if cfg.DownloadAdmission.MemoryBudgetBytes != 1*1024*1024*1024 {
		t.Fatalf("memory budget env override = %d, want 1 GiB", cfg.DownloadAdmission.MemoryBudgetBytes)
	}
	if cfg.DownloadAdmission.MemoryBudgetPercent != 30 || cfg.DownloadAdmission.RawCapacityPercent != 35 || cfg.DownloadAdmission.SafetyMarginPercent != 15 {
		t.Fatalf("capacity percentage env overrides = %#v", cfg.DownloadAdmission)
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
	t.Setenv("DOWNLOAD_ADMISSION_MEMORY_BUDGET_BYTES", "not-an-int")
	t.Setenv("DOWNLOAD_ADMISSION_MEMORY_BUDGET_PERCENT", "not-an-int")
	cfg.applyEnvOverrides()
	if len(cfg.envOverrideErrors) != 5 {
		t.Fatalf("env override errors = %v, want five errors", cfg.envOverrideErrors)
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
				CapacityMode:           "manual",
				MemoryBudgetPercent:    DefaultDownloadAdmissionMemoryBudgetPercent,
				RawCapacityPercent:     DefaultDownloadAdmissionRawCapacityPercent,
				SafetyMarginPercent:    DefaultDownloadAdmissionSafetyMarginPercent,
				MaxActivePerNode:       8,
				MaxActivePerAuthUser:   2,
				MaxActivePerLinkSource: 4,
				MaxActivePerClientLink: 2,
				AdmissionWait:          10 * time.Second,
				PreparationDeadline:    time.Minute,
				IdleWriteTimeout:       time.Minute,
				RetryAfter:             time.Second,
				MemoryBudgetBytes:      DefaultDownloadAdmissionMemoryBudgetBytes,
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
