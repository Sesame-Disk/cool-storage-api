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
