package config

import "testing"

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
