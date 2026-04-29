package gc

import (
	"testing"
	"time"
)

func TestDeletedUsersScanStartDay_RescansLastProcessedDay(t *testing.T) {
	cutoffDay := time.Date(2026, 4, 29, 0, 0, 0, 0, time.UTC)
	lastProcessedDay := cutoffDay

	got := deletedUsersScanStartDay(lastProcessedDay, cutoffDay)
	if !got.Equal(cutoffDay) {
		t.Fatalf("deletedUsersScanStartDay() = %s, want %s", got.Format("2006-01-02"), cutoffDay.Format("2006-01-02"))
	}
}

func TestDeletedUsersScanStartDay_UsesCutoffWhenCursorMissing(t *testing.T) {
	cutoffDay := time.Date(2026, 4, 29, 0, 0, 0, 0, time.UTC)

	got := deletedUsersScanStartDay(time.Time{}, cutoffDay)
	if !got.Equal(cutoffDay) {
		t.Fatalf("deletedUsersScanStartDay() = %s, want %s", got.Format("2006-01-02"), cutoffDay.Format("2006-01-02"))
	}
}
