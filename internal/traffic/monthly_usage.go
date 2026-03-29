package traffic

import (
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

// MonthlyTransferUsage is the common org/user traffic summary shape used by
// admin, org-admin, and account/subscription endpoints.
type MonthlyTransferUsage struct {
	Combined int64
	Upload   int64
	Download int64
}

// EffectivePeriodStart returns the active quota period start for an org. If the
// period is missing, it falls back to the first instant of the current UTC
// calendar month for backward compatibility.
func EffectivePeriodStart(periodStart *time.Time, now time.Time) time.Time {
	if periodStart != nil && !periodStart.IsZero() {
		return periodStart.UTC()
	}
	utcNow := now.UTC()
	return time.Date(utcNow.Year(), utcNow.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// EffectiveTrafficResetDate returns the human-facing reset date used by Phase 2
// payloads. When the org has an explicit period end, that is authoritative.
// Otherwise it falls back to the first day of the next UTC calendar month.
func EffectiveTrafficResetDate(periodEnd *time.Time, now time.Time) string {
	if periodEnd != nil && !periodEnd.IsZero() {
		return periodEnd.UTC().Format("2006-01-02")
	}
	utcNow := now.UTC()
	nextMonth := time.Date(utcNow.Year(), utcNow.Month()+1, 1, 0, 0, 0, 0, time.UTC)
	return nextMonth.Format("2006-01-02")
}

// CurrentMonth returns the current UTC month in yyyymm format.
func CurrentMonth() string {
	return time.Now().UTC().Format("200601")
}

// ReadMonthlyScopeTotals returns all traffic_monthly counters for one org+month
// partition in a single query.
func ReadMonthlyScopeTotals(db DBSession, orgID, month string) map[string]int64 {
	orgUUID, err := gocql.ParseUUID(orgID)
	if err != nil {
		return map[string]int64{}
	}

	totals := map[string]int64{}
	iter := db.Session().Query(
		`SELECT scope, bytes_transferred FROM traffic_monthly WHERE org_id = ? AND month = ?`,
		orgUUID, month,
	).Iter()
	var scope string
	var bytes int64
	for iter.Scan(&scope, &bytes) {
		totals[scope] = bytes
	}
	_ = iter.Close()
	return totals
}

// ReadOrgMonthlyUsage returns the current org-level traffic usage for the month.
func ReadOrgMonthlyUsage(db DBSession, orgID, month string) MonthlyTransferUsage {
	totals := ReadMonthlyScopeTotals(db, orgID, month)
	return MonthlyTransferUsage{
		Combined: totals["org:combined"],
		Upload:   totals["org:upload"],
		Download: totals["org:download"],
	}
}

// ReadUserMonthlyUsage returns the current per-user upload/download usage for the month.
func ReadUserMonthlyUsage(db DBSession, orgID, userID, month string) MonthlyTransferUsage {
	totals := ReadMonthlyScopeTotals(db, orgID, month)
	return MonthlyTransferUsage{
		Upload:   totals[userID+":upload"],
		Download: totals[userID+":download"],
	}
}

// ReadPeriodScopeTotals returns all traffic_period_usage counters for one
// org+period partition in a single query.
func ReadPeriodScopeTotals(db DBSession, orgID string, periodStartedAt time.Time) map[string]int64 {
	orgUUID, err := gocql.ParseUUID(orgID)
	if err != nil {
		return map[string]int64{}
	}

	totals := map[string]int64{}
	iter := db.Session().Query(
		`SELECT scope, bytes_transferred FROM traffic_period_usage WHERE org_id = ? AND period_started_at = ?`,
		orgUUID, periodStartedAt.UTC(),
	).Iter()
	var scope string
	var bytes int64
	for iter.Scan(&scope, &bytes) {
		totals[scope] = bytes
	}
	_ = iter.Close()
	return totals
}

// ReadOrgPeriodUsage returns the current org-level traffic usage for the active
// quota period.
func ReadOrgPeriodUsage(db DBSession, orgID string, periodStartedAt time.Time) MonthlyTransferUsage {
	totals := ReadPeriodScopeTotals(db, orgID, periodStartedAt)
	return MonthlyTransferUsage{
		Combined: totals["org:combined"],
		Upload:   totals["org:upload"],
		Download: totals["org:download"],
	}
}

// ReadUserPeriodUsage returns the current per-user upload/download usage for
// the active quota period.
func ReadUserPeriodUsage(db DBSession, orgID, userID string, periodStartedAt time.Time) MonthlyTransferUsage {
	totals := ReadPeriodScopeTotals(db, orgID, periodStartedAt)
	return MonthlyTransferUsage{
		Upload:   totals[userID+":upload"],
		Download: totals[userID+":download"],
	}
}
