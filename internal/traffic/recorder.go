// Package traffic provides fire-and-forget traffic recording and quota enforcement.
package traffic

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

// Traffic type constants match Seafile's category names for API compatibility.
const (
	SyncUpload   = "sync-file-upload"
	SyncDownload = "sync-file-download"
	WebUpload    = "web-file-upload"
	WebDownload  = "web-file-download"
	LinkUpload   = "link-file-upload"
	LinkDownload = "link-file-download"
)

// Recorder writes traffic counters asynchronously. All methods are safe for
// concurrent use. Record never blocks the caller.
type Recorder struct {
	session *gocql.Session
	sem     chan struct{} // bounds outstanding goroutines
}

// maxInflight caps the number of concurrent recording goroutines.
// Beyond this limit, new records are silently dropped to prevent OOM.
const maxInflight = 256

// NewRecorder creates a Recorder backed by the given ScyllaDB session.
func NewRecorder(session *gocql.Session) *Recorder {
	return &Recorder{
		session: session,
		sem:     make(chan struct{}, maxInflight),
	}
}

// Record increments the traffic counters for a single transfer. It runs
// completely asynchronously - the caller is never blocked and errors are
// logged but not returned.
//
// orgID and userID must be valid UUID strings. trafficType must be one of the
// package-level constants (SyncUpload, WebDownload, etc.).
//
// Callers that have already run a CheckTrafficQuota pre-check should use
// RecordWithPeriod instead - it reuses the already-resolved period and saves
// an extra SELECT per event.
func (r *Recorder) Record(orgID, userID, trafficType string, bytes int64) {
	r.recordAsync(orgID, userID, trafficType, bytes, time.Time{})
}

// RecordWithPeriod is like Record but accepts the quota period start that was
// already resolved by a preceding CheckTrafficQuota call. This eliminates the
// SELECT on organizations that Record would otherwise perform per event,
// and guarantees that enforcement and recording use the exact same period.
//
// periodStartedAt must be the PeriodStartedAt value from the QuotaStatus
// returned by CheckTrafficQuota. If zero, falls back to the DB lookup (same
// behavior as Record).
func (r *Recorder) RecordWithPeriod(orgID, userID, trafficType string, bytes int64, periodStartedAt time.Time) {
	r.recordAsync(orgID, userID, trafficType, bytes, periodStartedAt)
}

func (r *Recorder) recordAsync(orgID, userID, trafficType string, bytes int64, periodHint time.Time) {
	if bytes <= 0 {
		return
	}
	// Capture time values before entering the goroutine so that a slow
	// scheduler cannot shift the timestamp into the next day or month.
	now := time.Now().UTC()
	month := now.Format("200601")
	day := now.Truncate(24 * time.Hour)
	direction := directionOf(trafficType)

	select {
	case r.sem <- struct{}{}:
		go func() {
			defer func() { <-r.sem }()
			if err := r.recordCounters(orgID, userID, month, day, now, periodHint, trafficType, direction, bytes); err != nil {
				log.Printf("[traffic] record error org=%s user=%s type=%s: %v", orgID, userID, trafficType, err)
			}
		}()
	default:
		// Inflight limit reached - drop without spawning a goroutine.
		return
	}
}

// recordCounters performs all counter updates inside the goroutine.
func (r *Recorder) recordCounters(orgID, userID, month string, day, now, periodHint time.Time, trafficType, direction string, bytes int64) error {
	orgUUID, err := gocql.ParseUUID(orgID)
	if err != nil {
		return fmt.Errorf("invalid org UUID %q: %w", orgID, err)
	}
	userUUID, err := gocql.ParseUUID(userID)
	if err != nil {
		return fmt.Errorf("invalid user UUID %q: %w", userID, err)
	}

	// Use the hint when provided (from a preceding quota check) to avoid an
	// extra SELECT. Fall back to DB lookup only when the hint is absent.
	var periodStartedAt time.Time
	if !periodHint.IsZero() {
		periodStartedAt = periodHint
	} else {
		periodStartedAt = r.loadCurrentPeriodStart(orgUUID, now)
	}
	platformShard := CounterShardUUID(orgUUID)

	// 1. Daily per-user/type detail used for org-level statistics breakdowns.
	if err := r.session.Query(
		`UPDATE traffic_counters SET bytes_transferred = bytes_transferred + ?
		 WHERE org_id = ? AND month = ? AND shard = ? AND day = ? AND user_id = ? AND traffic_type = ?`,
		bytes, orgUUID, month, counterShardZero, day, userUUID, trafficType,
	).Exec(); err != nil {
		return fmt.Errorf("traffic_counters: %w", err)
	}

	// 2. Platform-wide aggregate lives under the zero-UUID org namespace, but
	// is spread across deterministic shards so sysadmin charts fan out only
	// over bounded platform partitions instead of every tenant org.
	if err := r.session.Query(
		`UPDATE traffic_counters SET bytes_transferred = bytes_transferred + ?
		 WHERE org_id = ? AND month = ? AND shard = ? AND day = ? AND user_id = ? AND traffic_type = ?`,
		bytes, gocql.UUID{}, month, platformShard, day, gocql.UUID{}, trafficType,
	).Exec(); err != nil {
		log.Printf("[traffic] platform aggregate error type=%s: %v", trafficType, err)
	}

	// 3. Platform-wide per-user detail reuses the same zero-UUID namespace and
	// shard so global user-traffic reports follow the same bounded fan-out.
	if err := r.session.Query(
		`UPDATE traffic_counters SET bytes_transferred = bytes_transferred + ?
		 WHERE org_id = ? AND month = ? AND shard = ? AND day = ? AND user_id = ? AND traffic_type = ?`,
		bytes, gocql.UUID{}, month, platformShard, day, userUUID, trafficType,
	).Exec(); err != nil {
		log.Printf("[traffic] platform per-user aggregate error user=%s type=%s: %v", userID, trafficType, err)
	}

	// 4-6. Aggregate counters for two different read models:
	//   - traffic_monthly for natural-month reporting and dashboards
	//   - traffic_period_usage for quota enforcement and current-period payloads
	scopes := []string{
		fmt.Sprintf("org:%s", direction),        // per-direction org total
		"org:combined",                          // combined upload+download org total
		fmt.Sprintf("%s:%s", userID, direction), // per-direction per-user total
	}
	for _, scope := range scopes {
		if err := r.session.Query(
			`UPDATE traffic_monthly SET bytes_transferred = bytes_transferred + ?
			 WHERE org_id = ? AND month = ? AND scope = ?`,
			bytes, orgUUID, month, scope,
		).Exec(); err != nil {
			return fmt.Errorf("traffic_monthly scope=%s: %w", scope, err)
		}
		if err := r.session.Query(
			`UPDATE traffic_period_usage SET bytes_transferred = bytes_transferred + ?
			 WHERE org_id = ? AND period_started_at = ? AND scope = ?`,
			bytes, orgUUID, periodStartedAt, scope,
		).Exec(); err != nil {
			return fmt.Errorf("traffic_period_usage scope=%s: %w", scope, err)
		}
	}
	return nil
}

func (r *Recorder) loadCurrentPeriodStart(orgUUID gocql.UUID, now time.Time) time.Time {
	var currentPeriodStartedAt *time.Time
	err := r.session.Query(
		`SELECT current_period_started_at FROM organizations WHERE org_id = ?`,
		orgUUID,
	).Scan(&currentPeriodStartedAt)
	if err != nil {
		return EffectivePeriodStart(nil, now)
	}
	return EffectivePeriodStart(currentPeriodStartedAt, now)
}

// directionOf returns "upload" or "download" based on the traffic type string.
func directionOf(trafficType string) string {
	if strings.Contains(trafficType, "upload") {
		return "upload"
	}
	return "download"
}

// Global singleton.
// Pattern mirrors SetGCHooks in gc_hooks.go: a package-level accessor
// initialized once during server startup and used by all handlers.
var global struct {
	mu sync.RWMutex
	r  *Recorder
}

// SetRecorder installs the global Recorder. Called once from server.go.
func SetRecorder(r *Recorder) {
	global.mu.Lock()
	global.r = r
	global.mu.Unlock()
}

// Get returns the global Recorder, or nil if SetRecorder has not been called.
// Callers must check for nil before using.
func Get() *Recorder {
	global.mu.RLock()
	defer global.mu.RUnlock()
	return global.r
}
