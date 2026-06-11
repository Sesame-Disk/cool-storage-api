// Package traffic — storage counter helpers.
//
// These functions manage the storage_counters table which tracks bytes_used
// and file_count across four scopes: platform, org, user, and library.
//
// The table uses ((scope, shard), day) as the PRIMARY KEY:
//   - shard = CounterShard(orgID) for the global platform scope
//   - shard = 0 for org/user/library scopes
//   - day = storageTotalDay (1970-01-01) → running total for fast quota checks
//   - day = <real date>                  → daily delta for time-series graphs
//
// This mirrors how traffic uses traffic_counters (per-day) + traffic_monthly
// (aggregate), but in a single table.
package traffic

import (
	"errors"
	"fmt"
	"hash/fnv"
	"log"
	"strings"
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

// CounterShardCount splits the two global hot counter aggregates into a modest
// number of deterministic shards. Reads only fan out on cold admin paths, so
// we can afford a wider spread here to reduce multiregion write concentration.
const CounterShardCount = 32

// storageTotalDay is the sentinel date used for the running-total row.
// All real daily deltas use actual dates (2026-03-26, etc.) which are always
// after this sentinel, so range queries for graphs never include it.
var storageTotalDay = time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)

const counterShardZero = 0

type storageScopeRoute struct {
	scope string
	shard int
}

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

// CounterShardUUID returns the deterministic shard for a canonical org UUID.
func CounterShardUUID(orgID gocql.UUID) int {
	hasher := fnv.New32a()
	_, _ = hasher.Write(orgID.Bytes())
	return int(hasher.Sum32() % CounterShardCount)
}

func CounterShard(orgID string) int {
	normalized := strings.TrimSpace(orgID)
	if parsed, err := gocql.ParseUUID(normalized); err == nil {
		return CounterShardUUID(parsed)
	}
	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(strings.ToLower(normalized)))
	return int(hasher.Sum32() % CounterShardCount)
}

func ForEachCounterShard(fn func(int)) {
	for shard := 0; shard < CounterShardCount; shard++ {
		fn(shard)
	}
}

func storageMutationRoutes(orgID, userID, libraryID string) []storageScopeRoute {
	routes := []storageScopeRoute{
		{scope: PlatformStorageScope(), shard: CounterShard(orgID)},
		{scope: OrganizationStorageScope(orgID), shard: counterShardZero},
	}
	if userID != "" {
		routes = append(routes, storageScopeRoute{
			scope: UserStorageScope(orgID, userID),
			shard: counterShardZero,
		})
	}
	if libraryID != "" {
		routes = append(routes, storageScopeRoute{
			scope: LibraryStorageScope(orgID, libraryID),
			shard: counterShardZero,
		})
	}
	return routes
}

func forEachStorageReadShard(scope string, fn func(int)) {
	if scope == PlatformStorageScope() {
		ForEachCounterShard(fn)
		return
	}
	fn(counterShardZero)
}

func storageUpdateErr(session *gocql.Session, scope string, shard int, day time.Time, deltaBytes, deltaFiles int64) error {
	return session.Query(
		`UPDATE storage_counters SET bytes_used = bytes_used + ?, file_count = file_count + ?
		 WHERE scope = ? AND shard = ? AND day = ?`,
		deltaBytes, deltaFiles, scope, shard, day,
	).Exec()
}

// storageUpdate writes a single counter update for the given scope and day.
func storageUpdate(session *gocql.Session, scope string, shard int, day time.Time, deltaBytes, deltaFiles int64) {
	if err := storageUpdateErr(session, scope, shard, day, deltaBytes, deltaFiles); err != nil {
		log.Printf("[storage] update error scope=%s shard=%d day=%s: %v", scope, shard, day.Format("2006-01-02"), err)
	}
}

// IncrementStorageCounters atomically increments storage usage for org, user,
// and library. Updates both the running total and today's daily delta.
// Runs fire-and-forget — never blocks the caller.
func IncrementStorageCounters(db DBSession, orgID, userID, libraryID string, deltaBytes int64, deltaFiles int64) {
	go func() {
		if err := IncrementStorageCountersSync(db, orgID, userID, libraryID, deltaBytes, deltaFiles); err != nil {
			log.Printf("[storage] increment counters error org=%s user=%s lib=%s: %v", orgID, userID, libraryID, err)
		}
	}()
}

// IncrementStorageCountersSync increments storage usage and returns only after
// all scope rows have been updated.
func IncrementStorageCountersSync(db DBSession, orgID, userID, libraryID string, deltaBytes int64, deltaFiles int64) error {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	session := db.Session()
	routes := storageMutationRoutes(orgID, userID, libraryID)
	for _, route := range routes {
		if err := storageUpdateErr(session, route.scope, route.shard, storageTotalDay, deltaBytes, deltaFiles); err != nil {
			return err
		}
		if err := storageUpdateErr(session, route.scope, route.shard, today, deltaBytes, deltaFiles); err != nil {
			return err
		}
	}
	return nil
}

// DecrementStorageCounters atomically decrements storage usage for org, user,
// and library. Reads the current total first and caps the delta to avoid
// negative values. Runs fire-and-forget — never blocks the caller.
func DecrementStorageCounters(db DBSession, orgID, userID, libraryID string, deltaBytes int64, deltaFiles int64) {
	if deltaBytes <= 0 && deltaFiles <= 0 {
		return
	}
	go func() {
		if err := DecrementStorageCountersSync(db, orgID, userID, libraryID, deltaBytes, deltaFiles); err != nil {
			log.Printf("[storage] decrement counters error org=%s user=%s lib=%s: %v", orgID, userID, libraryID, err)
		}
	}()
}

// DecrementStorageCountersSync decrements storage usage and caps negative
// deltas at the current running total for each scope.
func DecrementStorageCountersSync(db DBSession, orgID, userID, libraryID string, deltaBytes int64, deltaFiles int64) error {
	if deltaBytes <= 0 && deltaFiles <= 0 {
		return nil
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	session := db.Session()
	for _, route := range storageMutationRoutes(orgID, userID, libraryID) {
		var curBytes, curFiles int64
		_ = session.Query(
			`SELECT bytes_used, file_count FROM storage_counters WHERE scope = ? AND shard = ? AND day = ?`,
			route.scope, route.shard, storageTotalDay,
		).Scan(&curBytes, &curFiles)

		actBytes := min(deltaBytes, max(curBytes, 0))
		actFiles := min(deltaFiles, max(curFiles, 0))
		if actBytes <= 0 && actFiles <= 0 {
			continue
		}

		if err := storageUpdateErr(session, route.scope, route.shard, storageTotalDay, -actBytes, -actFiles); err != nil {
			return err
		}
		if err := storageUpdateErr(session, route.scope, route.shard, today, -actBytes, -actFiles); err != nil {
			return err
		}
	}
	return nil
}

// AdjustStorageCountersByDelta applies an arbitrary signed delta to platform,
// org, user, and library storage counters. It is used when a commit publishes a
// new tree and the exact change is known only after comparing aggregate stats.
func AdjustStorageCountersByDelta(db DBSession, orgID, userID, libraryID string, deltaBytes, deltaFiles int64) {
	if deltaBytes == 0 && deltaFiles == 0 {
		return
	}
	go func() {
		if err := AdjustStorageCountersByDeltaSync(db, orgID, userID, libraryID, deltaBytes, deltaFiles); err != nil {
			log.Printf("[storage] adjust counters error org=%s user=%s lib=%s: %v", orgID, userID, libraryID, err)
		}
	}()
}

// AdjustStorageCountersByDeltaSync applies an arbitrary signed delta and returns
// after all affected scope rows have been updated.
func AdjustStorageCountersByDeltaSync(db DBSession, orgID, userID, libraryID string, deltaBytes, deltaFiles int64) error {
	if deltaBytes == 0 && deltaFiles == 0 {
		return nil
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)
	session := db.Session()
	for _, route := range storageMutationRoutes(orgID, userID, libraryID) {
		bytesDelta, filesDelta := clampNegativeStorageDelta(session, route.scope, route.shard, deltaBytes, deltaFiles)
		if bytesDelta == 0 && filesDelta == 0 {
			continue
		}
		if err := storageUpdateErr(session, route.scope, route.shard, storageTotalDay, bytesDelta, filesDelta); err != nil {
			return err
		}
		if err := storageUpdateErr(session, route.scope, route.shard, today, bytesDelta, filesDelta); err != nil {
			return err
		}
	}
	return nil
}

func clampNegativeStorageDelta(session *gocql.Session, scope string, shard int, deltaBytes, deltaFiles int64) (int64, int64) {
	if deltaBytes >= 0 && deltaFiles >= 0 {
		return deltaBytes, deltaFiles
	}

	var curBytes, curFiles int64
	_ = session.Query(
		`SELECT bytes_used, file_count FROM storage_counters WHERE scope = ? AND shard = ? AND day = ?`,
		scope, shard, storageTotalDay,
	).Scan(&curBytes, &curFiles)

	if deltaBytes < 0 {
		deltaBytes = -min(-deltaBytes, max(curBytes, 0))
	}
	if deltaFiles < 0 {
		deltaFiles = -min(-deltaFiles, max(curFiles, 0))
	}
	return deltaBytes, deltaFiles
}

func readStorageSnapshotAtShardErr(db DBSession, scope string, shard int, day time.Time) (StorageSnapshot, error) {
	var bytesUsed, fileCount int64
	err := db.Session().Query(
		`SELECT bytes_used, file_count FROM storage_counters WHERE scope = ? AND shard = ? AND day = ?`,
		scope, shard, day,
	).Scan(&bytesUsed, &fileCount)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return StorageSnapshot{}, nil
		}
		return StorageSnapshot{}, err
	}
	return StorageSnapshot{
		BytesUsed: max(bytesUsed, 0),
		FileCount: max(fileCount, 0),
	}, nil
}

func readStorageSnapshotAtShard(db DBSession, scope string, shard int, day time.Time) StorageSnapshot {
	snapshot, err := readStorageSnapshotAtShardErr(db, scope, shard, day)
	if err != nil {
		log.Printf("[storage] read snapshot error scope=%s shard=%d day=%s: %v", scope, shard, day.Format("2006-01-02"), err)
		return StorageSnapshot{}
	}
	return snapshot
}

// ReadStorageUsed returns the live bytes_used from the running-total row
// for the given scope. Returns 0 if the row does not exist or on any error.
func ReadStorageUsed(db DBSession, scope string) int64 {
	return ReadStorageSnapshot(db, scope).BytesUsed
}

// ReadStorageSnapshot returns the running-total bytes_used and file_count for
// the given scope. Missing rows are treated as zero, and read errors are
// logged then treated as zero on these best-effort read paths.
func ReadStorageSnapshot(db DBSession, scope string) StorageSnapshot {
	var snapshot StorageSnapshot
	forEachStorageReadShard(scope, func(shard int) {
		shardSnapshot := readStorageSnapshotAtShard(db, scope, shard, storageTotalDay)
		snapshot.BytesUsed += shardSnapshot.BytesUsed
		snapshot.FileCount += shardSnapshot.FileCount
	})
	return snapshot
}

// ReadStorageDailyDeltas returns storage deltas for each day in [start, end].
// The sentinel total row is never included because storageTotalDay < any real date.
func ReadStorageDailyDeltas(db DBSession, scope string, start, end time.Time) map[string]int64 {
	deltas := map[string]int64{}
	forEachStorageReadShard(scope, func(shard int) {
		iter := db.Session().Query(
			`SELECT day, bytes_used FROM storage_counters WHERE scope = ? AND shard = ? AND day >= ? AND day <= ?`,
			scope, shard, start, end,
		).Iter()
		var day time.Time
		var delta int64
		for iter.Scan(&day, &delta) {
			key := day.UTC().Format("2006-01-02")
			deltas[key] += delta
		}
		if err := iter.Close(); err != nil {
			log.Printf("[traffic] ReadStorageDailyDeltas scope=%s shard=%d iter error: %v", scope, shard, err)
		}
	})
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
		`SELECT bytes_used, file_count FROM storage_counters WHERE scope = ? AND shard = ? AND day = ?`,
		libScope, counterShardZero, storageTotalDay,
	).Scan(&bytesUsed, &fileCount)

	if bytesUsed <= 0 && fileCount <= 0 {
		return
	}

	today := time.Now().UTC().Truncate(24 * time.Hour)
	session := db.Session()
	routes := []storageScopeRoute{
		{scope: PlatformStorageScope(), shard: CounterShard(orgID)},
		{scope: OrganizationStorageScope(orgID), shard: counterShardZero},
		{scope: UserStorageScope(orgID, ownerID), shard: counterShardZero},
	}

	if increment {
		for _, route := range routes {
			storageUpdate(session, route.scope, route.shard, storageTotalDay, bytesUsed, fileCount)
			storageUpdate(session, route.scope, route.shard, today, bytesUsed, fileCount)
		}
	} else {
		for _, route := range routes {
			var curBytes, curFiles int64
			_ = session.Query(
				`SELECT bytes_used, file_count FROM storage_counters WHERE scope = ? AND shard = ? AND day = ?`,
				route.scope, route.shard, storageTotalDay,
			).Scan(&curBytes, &curFiles)

			actBytes := min(bytesUsed, max(curBytes, 0))
			actFiles := min(fileCount, max(curFiles, 0))
			if actBytes <= 0 && actFiles <= 0 {
				continue
			}
			storageUpdate(session, route.scope, route.shard, storageTotalDay, -actBytes, -actFiles)
			storageUpdate(session, route.scope, route.shard, today, -actBytes, -actFiles)
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

var readStorageSnapshotAtShardErrFn = readStorageSnapshotAtShardErr
var storageUpdateErrFn = storageUpdateErr

// ReconcileStorageScope corrects a scope to the expected running total.
// The delta is derived from the current live total, so repeated runs converge.
func ReconcileStorageScope(db DBSession, scope string, expected StorageSnapshot) error {
	if scope == PlatformStorageScope() {
		return fmt.Errorf("platform scope requires ReconcileStorageScopeSharded")
	}
	current, err := readStorageSnapshotAtShardErrFn(db, scope, counterShardZero, storageTotalDay)
	if err != nil {
		return fmt.Errorf("read current storage scope %s: %w", scope, err)
	}
	deltaBytes := expected.BytesUsed - current.BytesUsed
	deltaFiles := expected.FileCount - current.FileCount
	if deltaBytes == 0 && deltaFiles == 0 {
		return nil
	}

	today := time.Now().UTC().Truncate(24 * time.Hour)
	session := db.Session()
	if err := storageUpdateErrFn(session, scope, counterShardZero, storageTotalDay, deltaBytes, deltaFiles); err != nil {
		return err
	}
	if err := storageUpdateErrFn(session, scope, counterShardZero, today, deltaBytes, deltaFiles); err != nil {
		return err
	}
	return nil
}

// ReconcileStorageScopeSharded corrects the platform scope shard-by-shard so
// retries converge even when the global aggregate is distributed.
func ReconcileStorageScopeSharded(db DBSession, scope string, expectedByShard map[int]StorageSnapshot) error {
	if scope != PlatformStorageScope() {
		return fmt.Errorf("sharded reconciliation only supports platform scope")
	}

	currentByShard := make(map[int]StorageSnapshot, CounterShardCount)
	var firstErr error
	ForEachCounterShard(func(shard int) {
		if firstErr != nil {
			return
		}
		current, err := readStorageSnapshotAtShardErrFn(db, scope, shard, storageTotalDay)
		if err != nil {
			firstErr = fmt.Errorf("read current storage scope %s shard %d: %w", scope, shard, err)
			return
		}
		currentByShard[shard] = current
	})
	if firstErr != nil {
		return firstErr
	}

	today := time.Now().UTC().Truncate(24 * time.Hour)
	session := db.Session()

	ForEachCounterShard(func(shard int) {
		expected := expectedByShard[shard]
		current := currentByShard[shard]
		deltaBytes := expected.BytesUsed - current.BytesUsed
		deltaFiles := expected.FileCount - current.FileCount
		if deltaBytes == 0 && deltaFiles == 0 {
			return
		}
		if err := storageUpdateErrFn(session, scope, shard, storageTotalDay, deltaBytes, deltaFiles); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			return
		}
		if err := storageUpdateErrFn(session, scope, shard, today, deltaBytes, deltaFiles); err != nil && firstErr == nil {
			firstErr = err
		}
	})

	return firstErr
}

// DeleteLibraryStorageCounter removes all rows for the lib-scope after permanent
// deletion. Aggregate scopes were already adjusted by a prior soft-delete.
func DeleteLibraryStorageCounter(db DBSession, orgID, libraryID string) error {
	scope := LibraryStorageScope(orgID, libraryID)
	return db.Session().Query(`DELETE FROM storage_counters WHERE scope = ? AND shard = ?`, scope, counterShardZero).Exec()
}
