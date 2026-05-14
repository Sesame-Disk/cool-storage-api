// Package traffic — storage counter helpers.
//
// These functions manage the storage_counters table which tracks bytes_used
// and file_count across four scopes: platform, org, user, and library.
//
// The table uses (scope, day) as the PRIMARY KEY:
//   - day = storageTotalDay (1970-01-01) → running total for fast quota checks
//   - day = <real date>                  → daily delta for time-series graphs
//
// This mirrors how traffic uses traffic_counters (per-day) + traffic_monthly
// (aggregate), but in a single table.
package traffic

import (
	"fmt"
	"log"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

// DBSession is the minimal interface needed to execute CQL queries.
// Both *db.DB and *gocql.Session satisfy this via their Session() method.
type DBSession interface {
	Session() *gocql.Session
}

// StorageSnapshot is the running-total storage state for a given scope.
type StorageSnapshot struct {
	BytesUsed int64
	FileCount int64
}

// storageTotalDay is the sentinel date used for the running-total row.
// All real daily deltas use actual dates (2026-03-26, etc.) which are always
// after this sentinel, so range queries for graphs never include it.
var storageTotalDay = time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)

func PlatformStorageScope() string {
	return "platform"
}

func OrganizationStorageScope(orgID string) string {
	return fmt.Sprintf("org:%s", orgID)
}

func UserStorageScope(orgID, userID string) string {
	return fmt.Sprintf("user:%s:%s", orgID, userID)
}

func LibraryStorageScope(orgID, libraryID string) string {
	return fmt.Sprintf("lib:%s:%s", orgID, libraryID)
}

func storageUpdateErr(session *gocql.Session, scope string, day time.Time, deltaBytes, deltaFiles int64) error {
	return session.Query(
		`UPDATE storage_counters SET bytes_used = bytes_used + ?, file_count = file_count + ?
		 WHERE scope = ? AND day = ?`,
		deltaBytes, deltaFiles, scope, day,
	).Exec()
}

// storageUpdate writes a single counter update for the given scope and day.
func storageUpdate(session *gocql.Session, scope string, day time.Time, deltaBytes, deltaFiles int64) {
	if err := storageUpdateErr(session, scope, day, deltaBytes, deltaFiles); err != nil {
		log.Printf("[storage] update error scope=%s day=%s: %v", scope, day.Format("2006-01-02"), err)
	}
}

// IncrementStorageCounters atomically increments storage usage for org, user,
// and library. Updates both the running total and today's daily delta.
// Runs fire-and-forget — never blocks the caller.
func IncrementStorageCounters(db DBSession, orgID, userID, libraryID string, deltaBytes int64, deltaFiles int64) {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	go func() {
		session := db.Session()
		scopes := []string{
			PlatformStorageScope(),
			OrganizationStorageScope(orgID),
		}
		if userID != "" {
			scopes = append(scopes, UserStorageScope(orgID, userID))
		}
		if libraryID != "" {
			scopes = append(scopes, LibraryStorageScope(orgID, libraryID))
		}
		for _, scope := range scopes {
			storageUpdate(session, scope, storageTotalDay, deltaBytes, deltaFiles)
			storageUpdate(session, scope, today, deltaBytes, deltaFiles)
		}
	}()
}

// DecrementStorageCounters atomically decrements storage usage for org, user,
// and library. Reads the current total first and caps the delta to avoid
// negative values. Runs fire-and-forget — never blocks the caller.
func DecrementStorageCounters(db DBSession, orgID, userID, libraryID string, deltaBytes int64, deltaFiles int64) {
	if deltaBytes <= 0 && deltaFiles <= 0 {
		return
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	go func() {
		session := db.Session()
		scopes := []string{
			PlatformStorageScope(),
			OrganizationStorageScope(orgID),
		}
		if userID != "" {
			scopes = append(scopes, UserStorageScope(orgID, userID))
		}
		if libraryID != "" {
			scopes = append(scopes, LibraryStorageScope(orgID, libraryID))
		}
		for _, scope := range scopes {
			var curBytes, curFiles int64
			_ = session.Query(
				`SELECT bytes_used, file_count FROM storage_counters WHERE scope = ? AND day = ?`,
				scope, storageTotalDay,
			).Scan(&curBytes, &curFiles)

			actBytes := min(deltaBytes, max(curBytes, 0))
			actFiles := min(deltaFiles, max(curFiles, 0))
			if actBytes <= 0 && actFiles <= 0 {
				continue
			}

			storageUpdate(session, scope, storageTotalDay, -actBytes, -actFiles)
			storageUpdate(session, scope, today, -actBytes, -actFiles)
		}
	}()
}

// AdjustStorageCountersByDelta applies an arbitrary signed delta to platform,
// org, user, and library storage counters. It is used when a commit publishes a
// new tree and the exact change is known only after comparing aggregate stats.
func AdjustStorageCountersByDelta(db DBSession, orgID, userID, libraryID string, deltaBytes, deltaFiles int64) {
	if deltaBytes == 0 && deltaFiles == 0 {
		return
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	go func() {
		session := db.Session()
		scopes := []string{
			PlatformStorageScope(),
			OrganizationStorageScope(orgID),
		}
		if userID != "" {
			scopes = append(scopes, UserStorageScope(orgID, userID))
		}
		if libraryID != "" {
			scopes = append(scopes, LibraryStorageScope(orgID, libraryID))
		}
		for _, scope := range scopes {
			bytesDelta, filesDelta := clampNegativeStorageDelta(session, scope, deltaBytes, deltaFiles)
			if bytesDelta == 0 && filesDelta == 0 {
				continue
			}
			storageUpdate(session, scope, storageTotalDay, bytesDelta, filesDelta)
			storageUpdate(session, scope, today, bytesDelta, filesDelta)
		}
	}()
}

func clampNegativeStorageDelta(session *gocql.Session, scope string, deltaBytes, deltaFiles int64) (int64, int64) {
	if deltaBytes >= 0 && deltaFiles >= 0 {
		return deltaBytes, deltaFiles
	}

	var curBytes, curFiles int64
	_ = session.Query(
		`SELECT bytes_used, file_count FROM storage_counters WHERE scope = ? AND day = ?`,
		scope, storageTotalDay,
	).Scan(&curBytes, &curFiles)

	if deltaBytes < 0 {
		deltaBytes = -min(-deltaBytes, max(curBytes, 0))
	}
	if deltaFiles < 0 {
		deltaFiles = -min(-deltaFiles, max(curFiles, 0))
	}
	return deltaBytes, deltaFiles
}

// ReadStorageUsed returns the live bytes_used from the running-total row
// for the given scope. Returns 0 if the row does not exist or on any error.
func ReadStorageUsed(db DBSession, scope string) int64 {
	var v int64
	_ = db.Session().Query(
		`SELECT bytes_used FROM storage_counters WHERE scope = ? AND day = ?`,
		scope, storageTotalDay,
	).Scan(&v)
	return max(v, 0)
}

// ReadStorageSnapshot returns the running-total bytes_used and file_count for
// the given scope. Missing rows are treated as zero.
func ReadStorageSnapshot(db DBSession, scope string) StorageSnapshot {
	var bytesUsed, fileCount int64
	_ = db.Session().Query(
		`SELECT bytes_used, file_count FROM storage_counters WHERE scope = ? AND day = ?`,
		scope, storageTotalDay,
	).Scan(&bytesUsed, &fileCount)
	return StorageSnapshot{
		BytesUsed: max(bytesUsed, 0),
		FileCount: max(fileCount, 0),
	}
}

// ReadStorageDailyDeltas returns storage deltas for each day in [start, end].
// The sentinel total row is never included because storageTotalDay < any real date.
func ReadStorageDailyDeltas(db DBSession, scope string, start, end time.Time) map[string]int64 {
	deltas := map[string]int64{}
	iter := db.Session().Query(
		`SELECT day, bytes_used FROM storage_counters WHERE scope = ? AND day >= ? AND day <= ?`,
		scope, start, end,
	).Iter()
	var day time.Time
	var delta int64
	for iter.Scan(&day, &delta) {
		deltas[day.UTC().Format("2006-01-02")] = delta
	}
	_ = iter.Close()
	return deltas
}

// ReconstructStorageHistory returns date → bytes_used for each day in [start, end],
// working backwards from the current running total using daily deltas.
func ReconstructStorageHistory(db DBSession, scope string, start, end time.Time) map[string]int64 {
	currentBytes := ReadStorageUsed(db, scope)
	today := time.Now().UTC().Truncate(24 * time.Hour)

	deltas := ReadStorageDailyDeltas(db, scope, start, today)

	history := map[string]int64{}
	val := currentBytes
	for d := today; !d.Before(start); d = d.AddDate(0, 0, -1) {
		key := d.Format("2006-01-02")
		if !d.After(end) {
			history[key] = max(val, 0)
		}
		val -= deltas[key]
	}
	return history
}

// AdjustAggregateStorageCounters reads the lib-scope total and increments
// (increment=true) or decrements (increment=false) the org, user, and platform
// scopes by that amount. Runs synchronously because callers need the adjustment
// to be visible before returning (e.g. quota checks right after restore).
func AdjustAggregateStorageCounters(db DBSession, orgID, ownerID, libraryID string, increment bool) {
	libScope := LibraryStorageScope(orgID, libraryID)
	var bytesUsed, fileCount int64
	_ = db.Session().Query(
		`SELECT bytes_used, file_count FROM storage_counters WHERE scope = ? AND day = ?`,
		libScope, storageTotalDay,
	).Scan(&bytesUsed, &fileCount)

	if bytesUsed <= 0 && fileCount <= 0 {
		return
	}

	today := time.Now().UTC().Truncate(24 * time.Hour)
	session := db.Session()
	scopes := []string{
		PlatformStorageScope(),
		OrganizationStorageScope(orgID),
		UserStorageScope(orgID, ownerID),
	}

	if increment {
		for _, scope := range scopes {
			storageUpdate(session, scope, storageTotalDay, bytesUsed, fileCount)
			storageUpdate(session, scope, today, bytesUsed, fileCount)
		}
	} else {
		for _, scope := range scopes {
			var curBytes, curFiles int64
			_ = session.Query(
				`SELECT bytes_used, file_count FROM storage_counters WHERE scope = ? AND day = ?`,
				scope, storageTotalDay,
			).Scan(&curBytes, &curFiles)

			actBytes := min(bytesUsed, max(curBytes, 0))
			actFiles := min(fileCount, max(curFiles, 0))
			if actBytes <= 0 && actFiles <= 0 {
				continue
			}
			storageUpdate(session, scope, storageTotalDay, -actBytes, -actFiles)
			storageUpdate(session, scope, today, -actBytes, -actFiles)
		}
	}
}

// AddAggregateStorageReconciliationQueries records the aggregate scopes that
// must be recomputed after a library soft-delete or restore.
func AddAggregateStorageReconciliationQueries(batch *gocql.Batch, orgID, ownerID string, requestedAt time.Time) {
	batch.Query(
		`INSERT INTO gc_storage_counter_reconciliation (scope, org_id, owner_id, requested_at) VALUES (?, ?, ?, ?)`,
		PlatformStorageScope(), gocql.UUID{}, gocql.UUID{}, requestedAt,
	)
	batch.Query(
		`INSERT INTO gc_storage_counter_reconciliation (scope, org_id, owner_id, requested_at) VALUES (?, ?, ?, ?)`,
		OrganizationStorageScope(orgID), orgID, gocql.UUID{}, requestedAt,
	)
	if ownerID != "" {
		batch.Query(
			`INSERT INTO gc_storage_counter_reconciliation (scope, org_id, owner_id, requested_at) VALUES (?, ?, ?, ?)`,
			UserStorageScope(orgID, ownerID), orgID, ownerID, requestedAt,
		)
	}
}

// ReconcileStorageScope corrects a scope to the expected running total.
// The delta is derived from the current live total, so repeated runs converge.
func ReconcileStorageScope(db DBSession, scope string, expected StorageSnapshot) error {
	current := ReadStorageSnapshot(db, scope)
	deltaBytes := expected.BytesUsed - current.BytesUsed
	deltaFiles := expected.FileCount - current.FileCount
	if deltaBytes == 0 && deltaFiles == 0 {
		return nil
	}

	today := time.Now().UTC().Truncate(24 * time.Hour)
	session := db.Session()
	if err := storageUpdateErr(session, scope, storageTotalDay, deltaBytes, deltaFiles); err != nil {
		return err
	}
	if err := storageUpdateErr(session, scope, today, deltaBytes, deltaFiles); err != nil {
		return err
	}
	return nil
}

// DeleteLibraryStorageCounter removes all rows for the lib-scope after permanent
// deletion. Aggregate scopes were already adjusted by a prior soft-delete.
func DeleteLibraryStorageCounter(db DBSession, orgID, libraryID string) error {
	scope := LibraryStorageScope(orgID, libraryID)
	return db.Session().Query(`DELETE FROM storage_counters WHERE scope = ?`, scope).Exec()
}
