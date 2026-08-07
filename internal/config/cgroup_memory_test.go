package config

import (
	"os"
	"strings"
	"testing"
)

// TestMain pins the detected container limit for the whole package.
//
// Auto capacity derives from the host, so any test that reaches Validate()
// with the section enabled is otherwise a function of the runner's own
// cgroup: green on a laptop, and on a sufficiently small agent failing
// outright because the budget cannot fit one raw slot plus one stream slot.
// Pinning here rather than per test means a test added later cannot
// reintroduce the dependency by forgetting to opt out; the real detection is
// covered by TestParseCgroupMemoryLimit and by the integration probe against
// a container that actually has a limit.
func TestMain(m *testing.M) {
	previous := cgroupMemoryLimit
	cgroupMemoryLimit = func() (int64, bool) { return 0, false }
	code := m.Run()
	cgroupMemoryLimit = previous
	os.Exit(code)
}

// withCgroupMemoryLimit pins the detected limit for one test. Anything that
// exercises auto capacity is otherwise a function of the runner's own cgroup.
func withCgroupMemoryLimit(t *testing.T, limit int64, ok bool) {
	t.Helper()
	previous := cgroupMemoryLimit
	cgroupMemoryLimit = func() (int64, bool) { return limit, ok }
	t.Cleanup(func() { cgroupMemoryLimit = previous })
}

func TestAutoCapacityUsesTheDetectedCgroupLimit(t *testing.T) {
	withCgroupMemoryLimit(t, 8*1024*1024*1024, true)

	cfg := DefaultConfig()
	cfg.Auth.DevMode = true
	if err := cfg.Validate(); err != nil {
		t.Fatalf("auto capacity under an 8 GiB limit: %v", err)
	}
	// 25% of 8 GiB is the same 2 GiB the fallback uses, so the shipped baseline
	// is what a detected limit of that size derives too.
	if got := cfg.DownloadAdmission.MemoryBudgetBytes; got != 2*1024*1024*1024 {
		t.Fatalf("derived budget = %d, want 25%% of the 8 GiB limit", got)
	}
	if cfg.DownloadAdmission.MaxActivePerNode != 16 || cfg.DownloadAdmission.MaxActiveRaw != 4 {
		t.Fatalf("derived capacities = node %d/raw %d, want 16/4",
			cfg.DownloadAdmission.MaxActivePerNode, cfg.DownloadAdmission.MaxActiveRaw)
	}
}

// TestAutoCapacityScalesWithTheDetectedLimit is the property the whole feature
// exists for, and the reason the shipped YAML numbers are a reference rather
// than the truth: a bigger container derives a bigger ceiling.
func TestAutoCapacityScalesWithTheDetectedLimit(t *testing.T) {
	var previous int
	for _, limit := range []int64{4, 8, 16} {
		withCgroupMemoryLimit(t, limit*1024*1024*1024, true)
		cfg := DefaultConfig()
		cfg.Auth.DevMode = true
		if err := cfg.Validate(); err != nil {
			t.Fatalf("%d GiB limit: %v", limit, err)
		}
		got := cfg.DownloadAdmission.MaxActivePerNode
		if got <= previous {
			t.Fatalf("%d GiB limit derived node cap %d, not more than the smaller container's %d", limit, got, previous)
		}
		previous = got
	}
}

func TestParseCgroupMemoryLimit(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		want  int64
		ok    bool
	}{
		{name: "v2 limit", value: "8589934592\n", want: 8 * 1024 * 1024 * 1024, ok: true},
		{name: "unlimited", value: "max", ok: false},
		{name: "empty", value: "", ok: false},
		{name: "invalid", value: "not-a-number", ok: false},
		{name: "sentinel unlimited", value: "1152921504606846976", ok: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseCgroupMemoryLimit(tc.value)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("parseCgroupMemoryLimit(%q) = (%d, %t), want (%d, %t)", tc.value, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestValidateDownloadAdmissionCgroupBudget(t *testing.T) {
	d := defaultDownloadAdmissionConfig()
	d.MemoryBudgetBytes = 2 * 1024 * 1024 * 1024

	if err := validateDownloadAdmissionCgroupBudgetForLimit(d, 8*1024*1024*1024); err != nil {
		t.Fatalf("2 GiB budget under 8 GiB limit: %v", err)
	}
	d.MemoryBudgetBytes = 2 * 1024 * 1024 * 1024
	if err := validateDownloadAdmissionCgroupBudgetForLimit(d, 4*1024*1024*1024); err == nil {
		t.Fatal("2 GiB budget under 4 GiB limit was accepted; want 25% cgroup guard")
	}
	d.MemoryBudgetBytes = 1 * 1024 * 1024 * 1024
	if err := validateDownloadAdmissionCgroupBudgetForLimit(d, 4*1024*1024*1024); err != nil {
		t.Fatalf("1 GiB budget under 4 GiB limit: %v", err)
	}
}

// TestBudgetSourceIsAlwaysAttributed pins the contract the provenance metric
// rests on: exactly one source is in effect, in every mode. Manual returned
// before recording anything, so a perfectly valid explicit budget published all
// three sources as zero and an operator reading the metric could not tell why.
func TestBudgetSourceIsAlwaysAttributed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		limit   int64
		haveCap bool
		modify  func(*Config)
		want    string
	}{
		{name: "auto from cgroup", limit: 8 << 30, haveCap: true, want: "cgroup"},
		{name: "auto without a limit", want: "fallback"},
		{
			name:   "auto with an explicit budget",
			modify: func(cfg *Config) { cfg.DownloadAdmission.MemoryBudgetBytes = 2 << 30 },
			want:   "configured",
		},
		{
			name: "manual",
			modify: func(cfg *Config) {
				cfg.DownloadAdmission.CapacityMode = "manual"
				cfg.DownloadAdmission.MemoryBudgetBytes = 2 << 30
			},
			want: "configured",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withCgroupMemoryLimit(t, tc.limit, tc.haveCap)
			cfg := DefaultConfig()
			cfg.Auth.DevMode = true
			if tc.modify != nil {
				tc.modify(cfg)
			}

			if err := cfg.Validate(); err != nil {
				t.Fatalf("Validate() = %v", err)
			}
			if got := cfg.DownloadBudgetSource(); got != tc.want {
				t.Fatalf("budget source = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestBudgetSourceSurvivesRevalidation covers the metric's whole reason for
// existing. Auto mode materialises the derived budget into the config, so a
// second Validate() sees a positive number and would call it "configured" —
// relabelling a derived budget as an explicit one is exactly the confusion this
// metric was added to prevent.
func TestBudgetSourceSurvivesRevalidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		limit   int64
		haveCap bool
		want    string
	}{
		{name: "derived from cgroup", limit: 8 << 30, haveCap: true, want: "cgroup"},
		{name: "reference fallback", want: "fallback"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withCgroupMemoryLimit(t, tc.limit, tc.haveCap)
			cfg := DefaultConfig()
			cfg.Auth.DevMode = true

			if err := cfg.Validate(); err != nil {
				t.Fatalf("first Validate() = %v", err)
			}
			if got := cfg.DownloadBudgetSource(); got != tc.want {
				t.Fatalf("budget source = %q, want %q", got, tc.want)
			}
			if err := cfg.Validate(); err != nil {
				t.Fatalf("second Validate() = %v", err)
			}
			if got := cfg.DownloadBudgetSource(); got != tc.want {
				t.Fatalf("budget source after revalidation = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestSmallContainerFailureNamesTheLevers pins the message an operator meets
// when a container is too small for the shipped design. It is the one refusal
// reachable without misconfiguring anything, so "too small" on its own leaves
// them stuck.
func TestSmallContainerFailureNamesTheLevers(t *testing.T) {
	withCgroupMemoryLimit(t, 1<<30, true) // 1 GiB derives a 256 MiB budget

	cfg := DefaultConfig()
	cfg.Auth.DevMode = true

	err := cfg.Validate()
	if err == nil {
		t.Fatal("a 1 GiB container validated; the derivation cannot fit one raw slot plus one stream slot")
	}
	for _, lever := range []string{
		"memory_budget_percent",
		"memory_budget_bytes",
		"max_iwork_source_bytes",
		"sync_block_max_bytes",
		"enabled: false",
	} {
		if !strings.Contains(err.Error(), lever) {
			t.Errorf("failure does not mention %q: %v", lever, err)
		}
	}
}
