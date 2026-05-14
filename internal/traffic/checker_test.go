package traffic

import (
	"testing"
	"time"
)

// TestQuotaStatus_PeriodStartedAtZeroByDefault verifies that a zero-value
// QuotaStatus has a zero PeriodStartedAt (non-traffic checks such as
// CheckStorageQuota or CheckMaxUsers never set this field).
func TestQuotaStatus_PeriodStartedAtZeroByDefault(t *testing.T) {
	var qs QuotaStatus
	if !qs.PeriodStartedAt.IsZero() {
		t.Errorf("zero-value QuotaStatus.PeriodStartedAt should be zero, got %s", qs.PeriodStartedAt)
	}
}

// TestQuotaStatus_PeriodStartedAtPreserved verifies that a QuotaStatus
// constructed with a specific PeriodStartedAt retains that value (basic
// struct sanity — guards against accidental field removal or renaming).
func TestQuotaStatus_PeriodStartedAtPreserved(t *testing.T) {
	want := time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC)
	qs := QuotaStatus{PeriodStartedAt: want}
	if !qs.PeriodStartedAt.Equal(want) {
		t.Errorf("PeriodStartedAt = %s, want %s", qs.PeriodStartedAt, want)
	}
}

func TestIsHardEnforcement(t *testing.T) {
	tests := []struct {
		quotaPolicy string
		expected    bool
	}{
		{"", true},      // empty defaults to hard (safe default)
		{"hard", true},  // explicit hard
		{"soft", false}, // paid tier
		{"HARD", false}, // case-sensitive, not "hard"
		{"unknown", false},
	}

	for _, tt := range tests {
		t.Run("policy="+tt.quotaPolicy, func(t *testing.T) {
			result := isHardEnforcement(tt.quotaPolicy)
			if result != tt.expected {
				t.Errorf("isHardEnforcement(%q) = %v, want %v", tt.quotaPolicy, result, tt.expected)
			}
		})
	}
}

func TestEvaluateStorageQuota_HardBlocksOverLimit(t *testing.T) {
	status := evaluateStorageQuota(900, 1000, 101, "hard")
	if status.Allowed {
		t.Fatal("Allowed = true, want false")
	}
	if status.Warning {
		t.Fatal("Warning = true, want false for hard block")
	}
	if status.Reason != "storage" {
		t.Fatalf("Reason = %q, want storage", status.Reason)
	}
}

func TestEvaluateStorageQuota_SoftWarnsAndAllows(t *testing.T) {
	status := evaluateStorageQuota(700, 1000, 100, "soft")
	if !status.Allowed {
		t.Fatal("Allowed = false, want true")
	}
	if !status.Warning {
		t.Fatal("Warning = false, want true at 80 percent")
	}
	if status.LimitBytes != 1000 || status.UsedBytes != 700 {
		t.Fatalf("status usage = (%d/%d), want (700/1000)", status.UsedBytes, status.LimitBytes)
	}
}

func TestMoreRestrictive_PicksLowerBlockedLimit(t *testing.T) {
	orgStatus := QuotaStatus{Allowed: false, Warning: true, LimitBytes: 500, Reason: "storage"}
	userStatus := QuotaStatus{Allowed: false, Warning: true, LimitBytes: 100, Reason: "storage"}

	got := moreRestrictive(orgStatus, userStatus)
	if got.LimitBytes != 100 {
		t.Fatalf("LimitBytes = %d, want user limit 100", got.LimitBytes)
	}
}

func TestMoreRestrictive_PreservesPeriodWhenSelectingWarning(t *testing.T) {
	period := time.Date(2026, time.April, 1, 0, 0, 0, 0, time.UTC)
	base := QuotaStatus{Allowed: true, LimitBytes: -1, PeriodStartedAt: period}
	warning := QuotaStatus{Allowed: true, Warning: true, LimitBytes: 1000, Reason: "traffic-upload"}

	got := moreRestrictive(base, warning)
	if !got.PeriodStartedAt.Equal(period) {
		t.Fatalf("PeriodStartedAt = %s, want %s", got.PeriodStartedAt, period)
	}
}
