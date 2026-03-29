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
		{"", true},        // empty defaults to hard (safe default)
		{"hard", true},    // explicit hard
		{"soft", false},   // paid tier
		{"HARD", false},   // case-sensitive, not "hard"
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
