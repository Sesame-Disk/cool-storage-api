package gc

import (
	"testing"
	"time"
)

func TestIsActiveLibraryForStorageReconciliation(t *testing.T) {
	zero := time.Time{}
	deleted := time.Date(2026, time.August, 22, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		deletedAt *time.Time
		want      bool
	}{
		{name: "cassandra null", deletedAt: nil, want: true},
		{name: "zero timestamp", deletedAt: &zero, want: true},
		{name: "deleted timestamp", deletedAt: &deleted, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isActiveLibraryForStorageReconciliation(tc.deletedAt); got != tc.want {
				t.Fatalf("isActiveLibraryForStorageReconciliation(%v) = %v, want %v", tc.deletedAt, got, tc.want)
			}
		})
	}
}
