package traffic

import (
	"testing"
	"time"
)

func TestAddMonth_Normal(t *testing.T) {
	tests := []struct {
		name string
		in   time.Time
		want time.Time
	}{
		{"mid-month", d(2026, 3, 15), d(2026, 4, 15)},
		{"jan-to-feb", d(2026, 1, 15), d(2026, 2, 15)},
		{"dec-to-jan", d(2025, 12, 10), d(2026, 1, 10)},
		{"first-of-month", d(2026, 6, 1), d(2026, 7, 1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := addMonth(tt.in)
			if !got.Equal(tt.want) {
				t.Errorf("addMonth(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestAddMonth_DayClamping(t *testing.T) {
	tests := []struct {
		name string
		in   time.Time
		want time.Time
	}{
		{"jan31-to-feb28", d(2026, 1, 31), d(2026, 2, 28)},
		{"jan31-to-feb29-leap", d(2024, 1, 31), d(2024, 2, 29)},
		{"mar31-to-apr30", d(2026, 3, 31), d(2026, 4, 30)},
		{"may31-to-jun30", d(2026, 5, 31), d(2026, 6, 30)},
		{"aug31-to-sep30", d(2026, 8, 31), d(2026, 9, 30)},
		{"oct31-to-nov30", d(2026, 10, 31), d(2026, 11, 30)},
		// 30-day months to 31-day months: no clamping needed
		{"apr30-to-may30", d(2026, 4, 30), d(2026, 5, 30)},
		{"nov30-to-dec30", d(2026, 11, 30), d(2026, 12, 30)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := addMonth(tt.in)
			if !got.Equal(tt.want) {
				t.Errorf("addMonth(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestAdvancePeriodUntilCurrent_SingleMonth(t *testing.T) {
	// Period ended April 1, now is April 10 → new period April 1 – May 1.
	expiredEnd := d(2026, 4, 1)
	now := d(2026, 4, 10)

	start, end := advancePeriodUntilCurrent(expiredEnd, now)
	if !start.Equal(d(2026, 4, 1)) {
		t.Errorf("start = %v, want 2026-04-01", start)
	}
	if !end.Equal(d(2026, 5, 1)) {
		t.Errorf("end = %v, want 2026-05-01", end)
	}
}

func TestAdvancePeriodUntilCurrent_MultipleMonths(t *testing.T) {
	// Server was down for 3 months: period ended Jan 15, now is April 20.
	// Should skip Feb 15, Mar 15 and land on Apr 15 – May 15.
	expiredEnd := d(2026, 1, 15)
	now := d(2026, 4, 20)

	start, end := advancePeriodUntilCurrent(expiredEnd, now)
	if !start.Equal(d(2026, 4, 15)) {
		t.Errorf("start = %v, want 2026-04-15", start)
	}
	if !end.Equal(d(2026, 5, 15)) {
		t.Errorf("end = %v, want 2026-05-15", end)
	}
}

func TestAdvancePeriodUntilCurrent_ExactlyOnBoundary(t *testing.T) {
	// now == period_ends_at exactly → should advance.
	expiredEnd := d(2026, 3, 1)
	now := d(2026, 3, 1)

	start, end := advancePeriodUntilCurrent(expiredEnd, now)
	if !start.Equal(d(2026, 3, 1)) {
		t.Errorf("start = %v, want 2026-03-01", start)
	}
	if !end.Equal(d(2026, 4, 1)) {
		t.Errorf("end = %v, want 2026-04-01", end)
	}
}

func TestAdvancePeriodUntilCurrent_ClampingAcrossMultiple(t *testing.T) {
	// Period ended Jan 31, server down until April 5.
	// Jan 31 → Feb 28 → Mar 28 → Apr 28 (all via addMonth).
	// Apr 28 + 1 month = May 28 > Apr 5, so: start=Mar 28, end=Apr 28.
	expiredEnd := d(2026, 1, 31)
	now := d(2026, 4, 5)

	start, end := advancePeriodUntilCurrent(expiredEnd, now)
	// Jan 31 → Feb 28 → Mar 28 → Apr 28 (> Apr 5). Start = Mar 28.
	if !start.Equal(d(2026, 3, 28)) {
		t.Errorf("start = %v, want 2026-03-28", start)
	}
	if !end.Equal(d(2026, 4, 28)) {
		t.Errorf("end = %v, want 2026-04-28", end)
	}
}

func TestAdvancePeriodUntilCurrent_YearBoundary(t *testing.T) {
	// Period ended Nov 15 2025, now is Feb 10 2026.
	expiredEnd := d(2025, 11, 15)
	now := d(2026, 2, 10)

	start, end := advancePeriodUntilCurrent(expiredEnd, now)
	// Nov 15 → Dec 15 → Jan 15 → Feb 15. Feb 15 > Feb 10.
	if !start.Equal(d(2026, 1, 15)) {
		t.Errorf("start = %v, want 2026-01-15", start)
	}
	if !end.Equal(d(2026, 2, 15)) {
		t.Errorf("end = %v, want 2026-02-15", end)
	}
}

// d is a helper to create UTC dates concisely.
func d(y int, m time.Month, day int) time.Time {
	return time.Date(y, m, day, 0, 0, 0, 0, time.UTC)
}
