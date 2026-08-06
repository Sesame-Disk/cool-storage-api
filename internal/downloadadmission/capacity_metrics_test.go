package downloadadmission

import (
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/config"
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
