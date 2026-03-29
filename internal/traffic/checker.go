package traffic

import (
	"fmt"
	"log"
	"sync"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

// QuotaStatus describes the result of a quota check.
type QuotaStatus struct {
	Allowed         bool      // may the operation proceed?
	Warning         bool      // >80% of the included limit (paid plans only)
	UsedBytes       int64     // current usage relevant to the check
	LimitBytes      int64     // limit that was evaluated (-1 = unlimited)
	Reason          string    // "storage", "traffic-combined", "traffic-upload", "traffic-download", "max-users"
	Plan            string    // plan name from organizations table
	PeriodStartedAt time.Time // the quota period resolved during CheckTrafficQuota; zero for non-traffic checks
}

// Checker reads quota configuration and current usage from ScyllaDB to evaluate
// whether an operation should be allowed, warned, or blocked.
type Checker struct {
	session *gocql.Session
}

// NewChecker creates a Checker backed by the given ScyllaDB session.
func NewChecker(session *gocql.Session) *Checker {
	return &Checker{session: session}
}

// isHardEnforcement returns true if the quota_policy indicates hard enforcement
// (free tier). Empty defaults to "hard" — safe default, no org gets a free pass.
func isHardEnforcement(quotaPolicy string) bool {
	return quotaPolicy == "" || quotaPolicy == "hard"
}

// CheckStorageQuota evaluates whether uploading additionalBytes would exceed the
// org's storage quota.
func (c *Checker) CheckStorageQuota(orgID string, additionalBytes int64) (QuotaStatus, error) {
	var storageQuota int64
	var quotaPolicy string

	// Minimal query: only the columns we need (storage_used is stale, use live counter).
	err := c.session.Query(`
		SELECT storage_quota, quota_policy
		FROM organizations WHERE org_id = ?`,
		mustParseUUID(orgID),
	).Scan(&storageQuota, &quotaPolicy)
	if err != nil {
		return QuotaStatus{Allowed: true}, fmt.Errorf("CheckStorageQuota: %w", err)
	}

	// Always read live usage from the counter table.
	storageUsed, _ := c.readStorageCounter(fmt.Sprintf("org:%s", orgID))

	if storageQuota <= 0 {
		// -1 or 0 means unlimited
		return QuotaStatus{Allowed: true, LimitBytes: -1, UsedBytes: storageUsed, Plan: quotaPolicy}, nil
	}

	projected := storageUsed + additionalBytes
	if projected > storageQuota {
		allowed := !isHardEnforcement(quotaPolicy) // paid plans: soft warning, free: hard block
		warning := !isHardEnforcement(quotaPolicy)
		return QuotaStatus{
			Allowed:    allowed,
			Warning:    warning,
			UsedBytes:  storageUsed,
			LimitBytes: storageQuota,
			Reason:     "storage",
			Plan:       quotaPolicy,
		}, nil
	}

	// Warn at 80% for paid plans
	warning := !isHardEnforcement(quotaPolicy) && float64(projected)/float64(storageQuota) >= 0.80
	return QuotaStatus{
		Allowed:    true,
		Warning:    warning,
		UsedBytes:  storageUsed,
		LimitBytes: storageQuota,
		Plan:       quotaPolicy,
	}, nil
}

// CheckTrafficQuota evaluates upload or download traffic quotas.
// direction must be "upload" or "download".
// All three checks (combined, per-direction org, per-user) are evaluated; the
// most restrictive result is returned.
func (c *Checker) CheckTrafficQuota(orgID, userID, direction string, additionalBytes int64) (QuotaStatus, error) {
	// 1. Load org quota config.
	var trafficQuota, uploadQuota, downloadQuota int64
	var quotaPolicy string
	var currentPeriodStartedAt *time.Time
	err := c.session.Query(`
		SELECT traffic_quota, traffic_upload_quota, traffic_download_quota, quota_policy,
		       current_period_started_at
		FROM organizations WHERE org_id = ?`,
		mustParseUUID(orgID),
	).Scan(&trafficQuota, &uploadQuota, &downloadQuota, &quotaPolicy, &currentPeriodStartedAt)
	if err != nil {
		// If row doesn't exist, allow by default (migration may not have run).
		return QuotaStatus{Allowed: true}, nil
	}

	periodStartedAt := EffectivePeriodStart(currentPeriodStartedAt, time.Now().UTC())
	worst := QuotaStatus{Allowed: true, LimitBytes: -1, Plan: quotaPolicy, PeriodStartedAt: periodStartedAt}

	// 2. Check combined quota.
	if trafficQuota > 0 {
		used, _ := c.readTrafficPeriod(orgID, periodStartedAt, "org:combined")
		projected := used + additionalBytes
		if projected > trafficQuota {
			s := QuotaStatus{
				Allowed:    !isHardEnforcement(quotaPolicy),
				Warning:    !isHardEnforcement(quotaPolicy),
				UsedBytes:  used,
				LimitBytes: trafficQuota,
				Reason:     "traffic-combined",
				Plan:       quotaPolicy,
			}
			worst = moreRestrictive(worst, s)
		} else if !isHardEnforcement(quotaPolicy) && float64(projected)/float64(trafficQuota) >= 0.80 {
			worst.Warning = true
			if worst.Reason == "" {
				worst.Reason = "traffic-combined"
			}
		}
	}

	// 3. Check per-direction org quota.
	var dirQuota int64
	if direction == "upload" {
		dirQuota = uploadQuota
	} else {
		dirQuota = downloadQuota
	}
	if dirQuota > 0 {
		scope := fmt.Sprintf("org:%s", direction)
		used, _ := c.readTrafficPeriod(orgID, periodStartedAt, scope)
		projected := used + additionalBytes
		reason := "traffic-" + direction
		if projected > dirQuota {
			s := QuotaStatus{
				Allowed:    !isHardEnforcement(quotaPolicy),
				Warning:    !isHardEnforcement(quotaPolicy),
				UsedBytes:  used,
				LimitBytes: dirQuota,
				Reason:     reason,
				Plan:       quotaPolicy,
			}
			worst = moreRestrictive(worst, s)
		} else if !isHardEnforcement(quotaPolicy) && float64(projected)/float64(dirQuota) >= 0.80 {
			worst.Warning = true
			if worst.Reason == "" {
				worst.Reason = reason
			}
		}
	}

	// 4. Check per-user quota (only when a real user is specified).
	if userID != "" && userID != "00000000-0000-0000-0000-000000000000" {
		var userUpload, userDownload int64
		userErr := c.session.Query(`
			SELECT traffic_upload_quota, traffic_download_quota
			FROM users WHERE org_id = ? AND user_id = ?`,
			mustParseUUID(orgID), mustParseUUID(userID),
		).Scan(&userUpload, &userDownload)
		if userErr == nil {
			var userDirQuota int64
			if direction == "upload" {
				userDirQuota = userUpload
			} else {
				userDirQuota = userDownload
			}
			if userDirQuota > 0 {
				scope := fmt.Sprintf("%s:%s", userID, direction)
				used, _ := c.readTrafficPeriod(orgID, periodStartedAt, scope)
				projected := used + additionalBytes
				reason := "traffic-" + direction
				if projected > userDirQuota {
					s := QuotaStatus{
						Allowed:    !isHardEnforcement(quotaPolicy),
						Warning:    !isHardEnforcement(quotaPolicy),
						UsedBytes:  used,
						LimitBytes: userDirQuota,
						Reason:     reason,
						Plan:       quotaPolicy,
					}
					worst = moreRestrictive(worst, s)
				}
			}
		}
	}

	return worst, nil
}

// CheckMaxUsers evaluates whether adding a new user to the org is allowed.
func (c *Checker) CheckMaxUsers(orgID string) (QuotaStatus, error) {
	var maxUsers int
	var quotaPolicy string
	err := c.session.Query(`
		SELECT max_users, quota_policy FROM organizations WHERE org_id = ?`,
		mustParseUUID(orgID),
	).Scan(&maxUsers, &quotaPolicy)
	if err != nil {
		return QuotaStatus{Allowed: true}, nil
	}

	// -1 or 0 = unlimited
	if maxUsers <= 0 {
		return QuotaStatus{Allowed: true, LimitBytes: -1, Plan: quotaPolicy}, nil
	}

	// Count current users.
	// NOTE: COUNT(*) performs a full partition scan in ScyllaDB. Acceptable for v1
	// because orgs with many users are enterprise (max_users typically -1/unlimited).
	// For v2, consider a dedicated user_count counter table.
	var currentUsers int
	countErr := c.session.Query(`
		SELECT COUNT(*) FROM users WHERE org_id = ?`,
		mustParseUUID(orgID),
	).Scan(&currentUsers)
	if countErr != nil {
		return QuotaStatus{Allowed: true}, nil
	}

	if currentUsers >= maxUsers {
		return QuotaStatus{
			Allowed:    false, // hard block regardless of plan
			UsedBytes:  int64(currentUsers),
			LimitBytes: int64(maxUsers),
			Reason:     "max-users",
			Plan:       quotaPolicy,
		}, nil
	}

	return QuotaStatus{
		Allowed:    true,
		UsedBytes:  int64(currentUsers),
		LimitBytes: int64(maxUsers),
		Plan:       quotaPolicy,
	}, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func (c *Checker) readTrafficPeriod(orgID string, periodStartedAt time.Time, scope string) (int64, error) {
	var bytes int64
	err := c.session.Query(`
		SELECT bytes_transferred FROM traffic_period_usage
		WHERE org_id = ? AND period_started_at = ? AND scope = ?`,
		mustParseUUID(orgID), periodStartedAt.UTC(), scope,
	).Scan(&bytes)
	if err != nil {
		// Log explicitly: a read error causes fail-open (used=0), which could allow
		// traffic past the quota limit without the operator noticing.
		log.Printf("[traffic] readTrafficPeriod: org=%s scope=%s period=%s err=%v (fail-open)",
			orgID, scope, periodStartedAt.Format("2006-01-02"), err)
		return 0, err
	}
	return bytes, nil
}

func (c *Checker) readStorageCounter(scope string) (int64, error) {
	var bytes int64
	err := c.session.Query(`
		SELECT bytes_used FROM storage_counters WHERE scope = ? AND day = ?`,
		scope, storageTotalDay,
	).Scan(&bytes)
	if err != nil {
		return 0, err
	}
	return max(bytes, 0), nil
}

// moreRestrictive returns the more restrictive of two QuotaStatus values.
// A blocked status always beats a warning. A warning beats an allow.
func moreRestrictive(a, b QuotaStatus) QuotaStatus {
	if !b.Allowed && a.Allowed {
		return b
	}
	if b.Warning && !a.Warning {
		// Both allowed but b has a warning
		a.Warning = true
		if b.Reason != "" {
			a.Reason = b.Reason
		}
		return a
	}
	return a
}

// mustParseUUID converts a UUID string to gocql.UUID, returning zero on error.
// Acceptable here because all Checker inputs come from validated auth middleware
// (session tokens, DB lookups). The Recorder validates UUIDs explicitly in
// recordCounters to prevent data corruption of the platform aggregate partition.
func mustParseUUID(s string) gocql.UUID {
	u, _ := gocql.ParseUUID(s)
	return u
}

// ── Global singleton ──────────────────────────────────────────────────────────

var globalChecker struct {
	mu sync.RWMutex
	c  *Checker
}

// SetChecker installs the global Checker. Called once from server.go.
func SetChecker(c *Checker) {
	globalChecker.mu.Lock()
	globalChecker.c = c
	globalChecker.mu.Unlock()
}

// GetChecker returns the global Checker, or nil if not initialized.
func GetChecker() *Checker {
	globalChecker.mu.RLock()
	defer globalChecker.mu.RUnlock()
	return globalChecker.c
}
