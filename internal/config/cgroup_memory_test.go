package config

import "testing"

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
