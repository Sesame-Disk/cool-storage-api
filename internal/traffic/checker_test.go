package traffic

import "testing"

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
