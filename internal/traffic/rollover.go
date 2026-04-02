package traffic

import (
	"log"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

// RolloverExpiredPeriods scans all organizations and advances the traffic
// quota period for any whose current_period_ends_at <= now.
//
// Traffic periods are ALWAYS monthly regardless of billing_cycle (annual
// billing still has monthly traffic limits and monthly overage charges).
//
// Relationship with Accounts (external billing service):
//   - Paid orgs: Accounts is the source of truth and may push updated period
//     dates at any time. This rollover serves as a safety net in case Accounts
//     is slow or temporarily unreachable — the next Accounts sync overwrites.
//   - Free orgs: No external billing service resets them, so this cron is
//     their ONLY mechanism for period advancement.
//
// Idempotent and safe for concurrent execution: two instances will compute
// the same deterministic period values, so the last writer wins with the
// same result.
func RolloverExpiredPeriods(session *gocql.Session, now time.Time) (int, error) {
	now = now.UTC()

	iter := session.Query(
		`SELECT org_id, status, current_period_started_at, current_period_ends_at
		 FROM organizations`,
	).Iter()

	var (
		orgID       gocql.UUID
		status      string
		periodStart *time.Time
		periodEnd   *time.Time
		rolled      int
	)

	for iter.Scan(&orgID, &status, &periodStart, &periodEnd) {
		// Skip deactivated/deleted orgs.
		if status == "deactivated" || status == "deleted" {
			continue
		}
		// Skip orgs without an explicit period end (legacy/unmanaged).
		if periodEnd == nil || periodEnd.IsZero() {
			continue
		}
		// Skip if period hasn't expired yet.
		if periodEnd.UTC().After(now) {
			continue
		}

		// Period has expired — advance by monthly increments until current.
		newStart, newEnd := advancePeriodUntilCurrent(*periodEnd, now)

		if err := session.Query(
			`UPDATE organizations SET current_period_started_at = ?, current_period_ends_at = ? WHERE org_id = ?`,
			newStart, newEnd, orgID,
		).Exec(); err != nil {
			log.Printf("[traffic] rollover error org=%s: %v", orgID, err)
			continue
		}

		log.Printf("[traffic] rollover org=%s: period %s → %s",
			orgID, newStart.Format("2006-01-02"), newEnd.Format("2006-01-02"))
		rolled++
	}

	if err := iter.Close(); err != nil {
		return rolled, err
	}
	return rolled, nil
}

// advancePeriodUntilCurrent advances the period forward by monthly increments
// (possibly multiple if the server was down for a long time) until the period
// end is strictly after now.
func advancePeriodUntilCurrent(expiredEnd time.Time, now time.Time) (newStart, newEnd time.Time) {
	start := expiredEnd.UTC()
	for {
		end := addMonth(start)
		if end.After(now) {
			return start, end
		}
		start = end
	}
}

// addMonth advances a timestamp by exactly one calendar month, clamping the
// day to the last day of the target month. This way a period starting on
// Jan 31 rolls to Feb 28 (not Mar 3), then Mar 31, Apr 30, etc.
func addMonth(t time.Time) time.Time {
	y, m, d := t.Date()

	targetM := m + 1
	targetY := y
	if targetM > 12 {
		targetM = 1
		targetY++
	}

	// Clamp day to last day of target month.
	// Day 0 of the month after target = last day of target month.
	maxDay := time.Date(targetY, targetM+1, 0, 0, 0, 0, 0, time.UTC).Day()
	if d > maxDay {
		d = maxDay
	}

	return time.Date(targetY, targetM, d, t.Hour(), t.Minute(), t.Second(), 0, time.UTC)
}
