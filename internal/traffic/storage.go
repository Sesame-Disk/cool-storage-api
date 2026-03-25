// Package traffic — storage counter helpers.
//
// These functions manage the storage_counters table which tracks bytes_used
// and file_count across four scopes: platform, org, user, and library.
// They live in the traffic package so that both internal/api/v2 and
// internal/gc can share the same implementation without import cycles.
package traffic

import (
	"fmt"
	"log"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

// DBSession is the minimal interface needed to execute CQL queries.
// Both *db.DB and *gocql.Session satisfy this via their Session() method.
type DBSession interface {
	Session() *gocql.Session
}

// IncrementStorageCounters atomically increments storage usage for org, user,
// and library. Runs fire-and-forget — never blocks the caller.
func IncrementStorageCounters(db DBSession, orgID, userID, libraryID string, deltaBytes int64, deltaFiles int64) {
	go func() {
		scopes := []string{
			"platform", // cross-org total for sysadmin statistics
			fmt.Sprintf("org:%s", orgID),
			fmt.Sprintf("user:%s:%s", orgID, userID),
			fmt.Sprintf("lib:%s:%s", orgID, libraryID),
		}
		for _, scope := range scopes {
			if err := db.Session().Query(
				`UPDATE storage_counters SET bytes_used = bytes_used + ?, file_count = file_count + ?
				 WHERE scope = ?`,
				deltaBytes, deltaFiles, scope,
			).Exec(); err != nil {
				log.Printf("[storage] increment error scope=%s: %v", scope, err)
			}
		}
	}()
}

// DecrementStorageCounters atomically decrements storage usage for org, user,
// and library. Reads the current counter first and caps the delta to avoid
// negative values. Runs fire-and-forget — never blocks the caller.
func DecrementStorageCounters(db DBSession, orgID, userID, libraryID string, deltaBytes int64, deltaFiles int64) {
	if deltaBytes <= 0 && deltaFiles <= 0 {
		return
	}
	go func() {
		scopes := []string{
			"platform", // cross-org total for sysadmin statistics
			fmt.Sprintf("org:%s", orgID),
			fmt.Sprintf("user:%s:%s", orgID, userID),
			fmt.Sprintf("lib:%s:%s", orgID, libraryID),
		}
		for _, scope := range scopes {
			// Read current values to cap the delta — prevents counters going negative.
			var curBytes, curFiles int64
			_ = db.Session().Query(
				`SELECT bytes_used, file_count FROM storage_counters WHERE scope = ?`, scope,
			).Scan(&curBytes, &curFiles)

			actBytes := min(deltaBytes, max(curBytes, 0))
			actFiles := min(deltaFiles, max(curFiles, 0))
			if actBytes <= 0 && actFiles <= 0 {
				continue
			}

			if err := db.Session().Query(
				`UPDATE storage_counters SET bytes_used = bytes_used - ?, file_count = file_count - ?
				 WHERE scope = ?`,
				actBytes, actFiles, scope,
			).Exec(); err != nil {
				log.Printf("[storage] decrement error scope=%s: %v", scope, err)
			}
		}
	}()
}

// ReadStorageUsed returns the live bytes_used from storage_counters for the
// given scope (e.g. "org:<id>", "user:<orgID>:<userID>", "platform").
// Returns 0 if the row does not exist or on any error.
func ReadStorageUsed(db DBSession, scope string) int64 {
	var v int64
	_ = db.Session().Query(
		`SELECT bytes_used FROM storage_counters WHERE scope = ?`, scope,
	).Scan(&v)
	return max(v, 0)
}

// AdjustAggregateStorageCounters reads the lib-scope counter and increments
// (increment=true) or decrements (increment=false) the org, user, and platform
// scopes by that amount. Runs synchronously because callers need the adjustment
// to be visible before returning (e.g. quota checks right after restore).
func AdjustAggregateStorageCounters(db DBSession, orgID, ownerID, libraryID string, increment bool) {
	libScope := fmt.Sprintf("lib:%s:%s", orgID, libraryID)
	var bytesUsed, fileCount int64
	_ = db.Session().Query(
		`SELECT bytes_used, file_count FROM storage_counters WHERE scope = ?`, libScope,
	).Scan(&bytesUsed, &fileCount)

	if bytesUsed <= 0 && fileCount <= 0 {
		return
	}

	scopes := []string{
		"platform",
		fmt.Sprintf("org:%s", orgID),
		fmt.Sprintf("user:%s:%s", orgID, ownerID),
	}

	if increment {
		for _, scope := range scopes {
			if err := db.Session().Query(
				`UPDATE storage_counters SET bytes_used = bytes_used + ?, file_count = file_count + ?
				 WHERE scope = ?`,
				bytesUsed, fileCount, scope,
			).Exec(); err != nil {
				log.Printf("[storage] lib-restore increment error scope=%s: %v", scope, err)
			}
		}
	} else {
		for _, scope := range scopes {
			// Read-cap-decrement to avoid negative counters.
			var curBytes, curFiles int64
			_ = db.Session().Query(
				`SELECT bytes_used, file_count FROM storage_counters WHERE scope = ?`, scope,
			).Scan(&curBytes, &curFiles)

			actBytes := min(bytesUsed, max(curBytes, 0))
			actFiles := min(fileCount, max(curFiles, 0))
			if actBytes <= 0 && actFiles <= 0 {
				continue
			}
			if err := db.Session().Query(
				`UPDATE storage_counters SET bytes_used = bytes_used - ?, file_count = file_count - ?
				 WHERE scope = ?`,
				actBytes, actFiles, scope,
			).Exec(); err != nil {
				log.Printf("[storage] lib-delete decrement error scope=%s: %v", scope, err)
			}
		}
	}
}

// DeleteLibraryStorageCounter removes the lib-scope counter row after permanent
// deletion. At this point aggregate scopes were already adjusted by a prior
// soft-delete, so no decrement is needed — just cleanup.
func DeleteLibraryStorageCounter(db DBSession, orgID, libraryID string) {
	scope := fmt.Sprintf("lib:%s:%s", orgID, libraryID)
	_ = db.Session().Query(`DELETE FROM storage_counters WHERE scope = ?`, scope).Exec()
}
