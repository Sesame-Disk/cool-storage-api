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

// Traffic type constants — match Seafile's category names for API compatibility.
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
// completely asynchronously — the caller is never blocked and errors are
// logged but not returned.
//
// orgID and userID must be valid UUID strings. trafficType must be one of the
// package-level constants (SyncUpload, WebDownload, …).
func (r *Recorder) Record(orgID, userID, trafficType string, bytes int64) {
	if bytes <= 0 {
		return
	}
	// Capture time values before entering the goroutine so that a slow
	// scheduler cannot shift the timestamp into the next day/month.
	now := time.Now().UTC()
	month := now.Format("200601")
	day := now.Truncate(24 * time.Hour)
	direction := directionOf(trafficType)

	select {
	case r.sem <- struct{}{}: // acquire slot (non-blocking)
		go func() {
			defer func() { <-r.sem }() // release slot
			if err := r.recordCounters(orgID, userID, month, day, trafficType, direction, bytes); err != nil {
				log.Printf("[traffic] record error org=%s user=%s type=%s: %v", orgID, userID, trafficType, err)
			}
		}()
	default:
		// Inflight limit reached — drop without spawning a goroutine.
		return
	}
}

// platformOrgID is the zero UUID used as the partition key for the cross-org
// aggregated traffic counter. Sysadmin statistics query only this partition
// instead of iterating every org partition.
const platformOrgID = "00000000-0000-0000-0000-000000000000"

// recordCounters performs all counter updates inside the goroutine.
func (r *Recorder) recordCounters(orgID, userID, month string, day time.Time, trafficType, direction string, bytes int64) error {
	orgUUID, err := gocql.ParseUUID(orgID)
	if err != nil {
		return fmt.Errorf("invalid org UUID %q: %w", orgID, err)
	}
	userUUID, err := gocql.ParseUUID(userID)
	if err != nil {
		return fmt.Errorf("invalid user UUID %q: %w", userID, err)
	}

	// 1. Daily per-user/type detail — used for org-level statistics breakdowns.
	if err := r.session.Query(
		`UPDATE traffic_counters SET bytes_transferred = bytes_transferred + ?
		 WHERE org_id = ? AND month = ? AND day = ? AND user_id = ? AND traffic_type = ?`,
		bytes, orgUUID, month, day, userUUID, trafficType,
	).Exec(); err != nil {
		return fmt.Errorf("traffic_counters: %w", err)
	}

	// 2. Platform-wide aggregate — stored in the zero-UUID partition so that
	//    sysadmin traffic charts can be served with a single partition read.
	if err := r.session.Query(
		`UPDATE traffic_counters SET bytes_transferred = bytes_transferred + ?
		 WHERE org_id = ? AND month = ? AND day = ? AND user_id = ? AND traffic_type = ?`,
		bytes, gocql.UUID{}, month, day, gocql.UUID{}, trafficType,
	).Exec(); err != nil {
		log.Printf("[traffic] platform aggregate error type=%s: %v", trafficType, err)
	}

	// 3–5. Monthly aggregates — used for quota enforcement (1 partition read).
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
	}
	return nil
}

// directionOf returns "upload" or "download" based on the traffic type string.
func directionOf(trafficType string) string {
	if strings.Contains(trafficType, "upload") {
		return "upload"
	}
	return "download"
}

// ── Global singleton ──────────────────────────────────────────────────────────
// Pattern mirrors SetGCHooks in gc_hooks.go: a package-level accessor
// initialised once during server startup and used by all handlers.

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
