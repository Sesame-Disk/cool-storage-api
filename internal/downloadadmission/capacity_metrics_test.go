package downloadadmission

import (
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestCapacityMetricSettingsCoverEveryPublishedSetting keeps the two lists in
// publishCapacityMetrics in step. They are written separately — one publishes
// the live values, the other zeroes them when admission is off — so a setting
// added to only one would either never be zeroed on a disabled node, leaving a
// stale value visible, or be zeroed and never populated.
func TestCapacityMetricSettingsCoverEveryPublishedSetting(t *testing.T) {
	published := publishedCapacitySettings(config.DownloadAdmissionConfig{})

	if len(published) != len(capacityMetricSettings) {
		t.Fatalf("publishing %d settings but zeroing %d", len(published), len(capacityMetricSettings))
	}
	zeroed := make(map[string]bool, len(capacityMetricSettings))
	for _, setting := range capacityMetricSettings {
		zeroed[setting] = true
	}
	for setting := range published {
		if !zeroed[setting] {
			t.Errorf("%q is published but never zeroed when admission is disabled", setting)
		}
	}
}

// TestPublishCapacityRefusesOutOfContractValues covers the exported constructor.
// ValidateDownloadAdmissionConfig does not check the budget or the margin —
// those belong to Config.Validate — so a caller that builds a Coordinator
// directly can reach the metrics with values the D6 contract forbids. Half a
// publication is the failure mode: a node reporting a negative budget beside a
// zero effective budget contradicts itself.
func TestPublishCapacityRefusesOutOfContractValues(t *testing.T) {
	base := config.DownloadAdmissionConfig{
		Enabled:             true,
		MemoryBudgetBytes:   2 << 30,
		SafetyMarginPercent: 20,
	}
	for _, tc := range []struct {
		name   string
		modify func(*config.DownloadAdmissionConfig)
	}{
		{"negative budget", func(c *config.DownloadAdmissionConfig) { c.MemoryBudgetBytes = -1 }},
		{"budget above the ceiling", func(c *config.DownloadAdmissionConfig) {
			c.MemoryBudgetBytes = config.MaxDownloadAdmissionMemoryBudgetBytes + 1
		}},
		{"margin at 100", func(c *config.DownloadAdmissionConfig) { c.SafetyMarginPercent = 100 }},
		{"negative margin", func(c *config.DownloadAdmissionConfig) { c.SafetyMarginPercent = -1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base
			tc.modify(&cfg)
			publishCapacityMetrics(cfg)

			if got := testutil.ToFloat64(metrics.DownloadAdmissionMemoryBudgetBytes); got != 0 {
				t.Errorf("published budget %v for an out-of-contract configuration", got)
			}
			if got := testutil.ToFloat64(metrics.DownloadAdmissionMemoryBudgetEffectiveBytes); got != 0 {
				t.Errorf("published effective budget %v for an out-of-contract configuration", got)
			}
		})
	}

	// And the in-contract case still publishes both, so the guard above is not
	// simply zeroing everything.
	publishCapacityMetrics(base)
	if got := testutil.ToFloat64(metrics.DownloadAdmissionMemoryBudgetEffectiveBytes); got != float64(base.MemoryBudgetBytes*80/100) {
		t.Fatalf("effective budget = %v, want the margin applied to the budget", got)
	}
}
