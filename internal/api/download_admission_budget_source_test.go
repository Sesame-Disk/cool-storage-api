package api

import (
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestBudgetSourceGaugeReflectsWhetherTheGuardIsActive pins the contract the
// provenance metric states: exactly one source is in effect while admission is
// enabled, and none at all when it is off.
//
// The disabled case is the one that regressed silently. Manual mode records its
// provenance before the disabled early-return, so without the guard in the
// initializer a switched-off node published budget_source{configured}=1 beside
// a zero budget and zero capacities — the node contradicting itself about
// whether it had a design at all. Nothing asserted this gauge before.
func TestBudgetSourceGaugeReflectsWhetherTheGuardIsActive(t *testing.T) {
	sources := []string{"configured", "cgroup", "fallback"}

	for _, tc := range []struct {
		name    string
		enabled bool
		want    string
	}{
		{name: "enabled manual reports its source", enabled: true, want: "configured"},
		{name: "disabled reports none", enabled: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.Auth.DevMode = true
			cfg.DownloadAdmission.CapacityMode = "manual"
			cfg.DownloadAdmission.MemoryBudgetBytes = config.DefaultDownloadAdmissionMemoryBudgetBytes
			cfg.DownloadAdmission.Enabled = tc.enabled
			if err := cfg.Validate(); err != nil {
				t.Fatalf("Validate() = %v", err)
			}

			server := &Server{config: cfg}
			server.initializeDownloadAdmissionCoordinator()

			for _, source := range sources {
				got := testutil.ToFloat64(metrics.DownloadAdmissionBudgetSource.WithLabelValues(source))
				want := 0.0
				if source == tc.want {
					want = 1
				}
				if got != want {
					t.Errorf("budget_source{source=%q} = %v, want %v", source, got, want)
				}
			}
		})
	}
}
