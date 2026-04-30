package gc

import (
	"testing"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

func TestExpiredShareLinksCursorDay_UsesCurrentUTCDay(t *testing.T) {
	now := time.Date(2026, 4, 29, 15, 4, 5, 0, time.FixedZone("UTC-5", -5*60*60))

	got := expiredShareLinksCursorDay(now)
	want := time.Date(2026, 4, 29, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("expiredShareLinksCursorDay() = %s, want %s", got.Format("2006-01-02"), want.Format("2006-01-02"))
	}
}

func TestExpiredSharesCursorDay_UsesCurrentUTCDay(t *testing.T) {
	now := time.Date(2026, 4, 29, 23, 59, 59, 0, time.FixedZone("UTC+3", 3*60*60))

	got := expiredSharesCursorDay(now)
	want := time.Date(2026, 4, 29, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("expiredSharesCursorDay() = %s, want %s", got.Format("2006-01-02"), want.Format("2006-01-02"))
	}
}

func TestDeletedUsersCursorDay_UsesGraceCutoffUTCDay(t *testing.T) {
	now := time.Date(2026, 4, 29, 10, 30, 0, 0, time.UTC)

	got := deletedUsersCursorDay(now, 7)
	want := time.Date(2026, 4, 22, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("deletedUsersCursorDay() = %s, want %s", got.Format("2006-01-02"), want.Format("2006-01-02"))
	}
}

func TestExpiredShareLinksScanStartDay_RescansLastProcessedDay(t *testing.T) {
	cutoffDay := time.Date(2026, 4, 29, 0, 0, 0, 0, time.UTC)
	lastProcessedDay := cutoffDay

	got := expiredShareLinksScanStartDay(lastProcessedDay, cutoffDay)
	want := cutoffDay.AddDate(0, 0, -gcScanOverlapDays)
	if !got.Equal(want) {
		t.Fatalf("expiredShareLinksScanStartDay() = %s, want %s", got.Format("2006-01-02"), want.Format("2006-01-02"))
	}
}

func TestExpiredShareLinksScanStartDay_UsesCutoffWhenCursorMissing(t *testing.T) {
	cutoffDay := time.Date(2026, 4, 29, 0, 0, 0, 0, time.UTC)

	got := expiredShareLinksScanStartDay(time.Time{}, cutoffDay)
	want := cutoffDay.AddDate(0, 0, -gcInitialScanLookbackDays)
	if !got.Equal(want) {
		t.Fatalf("expiredShareLinksScanStartDay() = %s, want %s", got.Format("2006-01-02"), want.Format("2006-01-02"))
	}
}

func TestExpiredSharesScanStartDay_RescansLastProcessedDay(t *testing.T) {
	cutoffDay := time.Date(2026, 4, 29, 0, 0, 0, 0, time.UTC)
	lastProcessedDay := cutoffDay

	got := expiredSharesScanStartDay(lastProcessedDay, cutoffDay)
	want := cutoffDay.AddDate(0, 0, -gcScanOverlapDays)
	if !got.Equal(want) {
		t.Fatalf("expiredSharesScanStartDay() = %s, want %s", got.Format("2006-01-02"), want.Format("2006-01-02"))
	}
}

func TestExpiredSharesScanStartDay_UsesCutoffWhenCursorMissing(t *testing.T) {
	cutoffDay := time.Date(2026, 4, 29, 0, 0, 0, 0, time.UTC)

	got := expiredSharesScanStartDay(time.Time{}, cutoffDay)
	want := cutoffDay.AddDate(0, 0, -gcInitialScanLookbackDays)
	if !got.Equal(want) {
		t.Fatalf("expiredSharesScanStartDay() = %s, want %s", got.Format("2006-01-02"), want.Format("2006-01-02"))
	}
}

func TestDeletedUsersScanStartDay_RescansLastProcessedDay(t *testing.T) {
	cutoffDay := time.Date(2026, 4, 29, 0, 0, 0, 0, time.UTC)
	lastProcessedDay := cutoffDay

	got := deletedUsersScanStartDay(lastProcessedDay, cutoffDay)
	want := cutoffDay.AddDate(0, 0, -gcScanOverlapDays)
	if !got.Equal(want) {
		t.Fatalf("deletedUsersScanStartDay() = %s, want %s", got.Format("2006-01-02"), want.Format("2006-01-02"))
	}
}

func TestDeletedUsersScanStartDay_UsesCutoffWhenCursorMissing(t *testing.T) {
	cutoffDay := time.Date(2026, 4, 29, 0, 0, 0, 0, time.UTC)

	got := deletedUsersScanStartDay(time.Time{}, cutoffDay)
	want := cutoffDay.AddDate(0, 0, -gcInitialScanLookbackDays)
	if !got.Equal(want) {
		t.Fatalf("deletedUsersScanStartDay() = %s, want %s", got.Format("2006-01-02"), want.Format("2006-01-02"))
	}
}

func TestDeletedUsersStartDayFromCursor_UsesLookbackWhenCursorMissing(t *testing.T) {
	cutoffDay := time.Date(2026, 4, 29, 0, 0, 0, 0, time.UTC)

	got, err := deletedUsersStartDayFromCursor("", gocql.ErrNotFound, cutoffDay)
	if err != nil {
		t.Fatalf("deletedUsersStartDayFromCursor() error = %v, want nil", err)
	}
	want := cutoffDay.AddDate(0, 0, -gcInitialScanLookbackDays)
	if !got.Equal(want) {
		t.Fatalf("deletedUsersStartDayFromCursor() = %s, want %s", got.Format("2006-01-02"), want.Format("2006-01-02"))
	}
}
