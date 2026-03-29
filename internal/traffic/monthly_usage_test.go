package traffic

import (
	"testing"
	"time"
)

func TestEffectivePeriodStart_UsesExplicitValue(t *testing.T) {
	now := time.Date(2026, time.March, 28, 12, 0, 0, 0, time.UTC)
	periodStart := time.Date(2026, time.March, 15, 8, 30, 0, 0, time.FixedZone("UTC-3", -3*60*60))

	got := EffectivePeriodStart(&periodStart, now)
	want := periodStart.UTC()
	if !got.Equal(want) {
		t.Fatalf("EffectivePeriodStart() = %s, want %s", got.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

func TestEffectivePeriodStart_FallsBackToCurrentMonthStart(t *testing.T) {
	now := time.Date(2026, time.March, 28, 12, 0, 0, 0, time.UTC)

	got := EffectivePeriodStart(nil, now)
	want := time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("EffectivePeriodStart(nil) = %s, want %s", got.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

func TestEffectiveTrafficResetDate_UsesExplicitPeriodEnd(t *testing.T) {
	now := time.Date(2026, time.March, 28, 12, 0, 0, 0, time.UTC)
	periodEnd := time.Date(2026, time.April, 14, 23, 59, 59, 0, time.FixedZone("UTC+2", 2*60*60))

	got := EffectiveTrafficResetDate(&periodEnd, now)
	want := periodEnd.UTC().Format("2006-01-02")
	if got != want {
		t.Fatalf("EffectiveTrafficResetDate() = %s, want %s", got, want)
	}
}

func TestEffectiveTrafficResetDate_FallsBackToNextMonth(t *testing.T) {
	now := time.Date(2026, time.March, 28, 12, 0, 0, 0, time.UTC)

	got := EffectiveTrafficResetDate(nil, now)
	if got != "2026-04-01" {
		t.Fatalf("EffectiveTrafficResetDate(nil) = %s, want 2026-04-01", got)
	}
}
