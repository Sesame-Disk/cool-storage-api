package db

import (
	"errors"
	"fmt"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

// readProvisionalBlockRefExpiry returns the currently recorded deadline for one
// provisional referrer, or the zero time when no row exists. Callers use it to
// find the discovery projection an overwrite has to retract.
func (db *DB) readProvisionalBlockRefExpiry(orgID, blockID, referrer string) (time.Time, error) {
	var existingExpiresAt time.Time
	err := db.Session().Query(`
		SELECT expires_at FROM gc_provisional_block_refs
		WHERE org_id = ? AND block_id = ? AND referrer = ?
	`, orgID, blockID, referrer).Scan(&existingExpiresAt)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return time.Time{}, nil
		}
		return time.Time{}, fmt.Errorf("read provisional block ref expiry for org=%s block=%s referrer=%s: %w", orgID, blockID, referrer, err)
	}
	return existingExpiresAt, nil
}

// addProvisionalBlockRefExpiryQueries stages the canonical expiry row plus its
// by-day discovery projection, retracting the projection the previous deadline
// left behind when the deadline moves.
func addProvisionalBlockRefExpiryQueries(batch *gocql.Batch, orgID, blockID, referrer, storageClass string, expiresAt, existingExpiresAt time.Time) {
	batch.Query(`
		INSERT INTO gc_provisional_block_refs (org_id, block_id, referrer, storage_class, expires_at)
		VALUES (?, ?, ?, ?, ?)
	`, orgID, blockID, referrer, storageClass, expiresAt)
	AddUpsertProvisionalBlockRefExpiryDiscoveryQuery(batch, orgID, blockID, referrer, storageClass, expiresAt)
	if !existingExpiresAt.IsZero() && !existingExpiresAt.Equal(expiresAt) {
		AddDeleteProvisionalBlockRefExpiryDiscoveryQuery(batch, orgID, blockID, referrer, existingExpiresAt)
	}
}

// AddProvisionalBlockReferenceWithExpiry writes an in-flight upload's block
// reference AND its GC expiry tracking in ONE logged batch (F10). Written as two
// statements, a failure between them left a reference with no discovery
// projection: GC Phase 0 enumerates provisional refs through
// `gc_provisional_block_refs_by_day`, so an unprojected reference pins the block
// forever with nothing able to find it.
//
// What a logged batch does and does not buy, precisely: it gives **atomicity** —
// the batchlog is replicated before anything is applied and replayed if the
// coordinator dies, so a permanently half-applied batch is not an ordinary failure
// mode the way a split write is. It does NOT give **isolation**: a concurrent
// reader can observe one statement before the other. That is harmless here in both
// directions — Phase 0 only ever discovers through the projection, and a projection
// this batch has just written points ~48h into the future, so nothing acts on it
// until long after every statement has landed.
//
// The Cassandra TTL on the reference row is DERIVED from expiresAt rather than
// passed in, which is load-bearing beyond tidiness: GC Phase 0 no longer deletes
// expired provisional references, it waits for this TTL to retire them (F9). A
// caller that could pass a TTL outliving expiresAt would strand the scanner
// waiting on a row that never goes away; deriving it makes "the row cannot
// outlive its tracker by more than the rounding second" true by construction.
//
// A zero/past expiresAt is rejected rather than silently written: an untracked
// provisional reference is exactly the leak this function exists to prevent.
func (db *DB) AddProvisionalBlockReferenceWithExpiry(orgID, blockID, referrer, libraryID, storageClass string, expiresAt time.Time) error {
	if db == nil {
		return fmt.Errorf("add provisional block reference for org=%s block=%s referrer=%s: no database", orgID, blockID, referrer)
	}
	if expiresAt.IsZero() {
		return fmt.Errorf("add provisional block reference for org=%s block=%s referrer=%s: expires_at is required", orgID, blockID, referrer)
	}

	now := time.Now().UTC()
	expiresAt = expiresAt.UTC()
	// Round up so the pin always covers at least the tracked deadline. Rounding
	// down would retire the reference before its expiry row, briefly unpinning a
	// block whose upload is still live.
	ttlSeconds := int((expiresAt.Sub(now) + time.Second - time.Nanosecond) / time.Second)
	if ttlSeconds <= 0 {
		return fmt.Errorf("add provisional block reference for org=%s block=%s referrer=%s: expires_at %s is not in the future", orgID, blockID, referrer, expiresAt.Format(time.RFC3339))
	}

	existingExpiresAt, err := db.readProvisionalBlockRefExpiry(orgID, blockID, referrer)
	if err != nil {
		return err
	}

	batch := db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		INSERT INTO block_references (org_id, block_id, referrer, library_id, created_at)
		VALUES (?, ?, ?, ?, ?) USING TTL ?
	`, orgID, blockID, referrer, libraryID, now, ttlSeconds)
	addProvisionalBlockRefExpiryQueries(batch, orgID, blockID, referrer, storageClass, expiresAt, existingExpiresAt)
	if err := batch.Exec(); err != nil {
		return fmt.Errorf("add provisional block reference with expiry for org=%s block=%s referrer=%s: %w", orgID, blockID, referrer, err)
	}
	return nil
}

// There is deliberately no "upsert just the expiry tracking" entry point. Writing
// tracking without the reference it tracks, or a reference without its tracking,
// is F10; AddProvisionalBlockReferenceWithExpiry is the only way in, and it also
// serves renewal (an existing deadline is read and its projection retracted in the
// same batch).

var releaseProvisionalReadExpiryFn = func(database *DB, orgID, blockID, referrer string) (time.Time, error) {
	return database.readProvisionalBlockRefExpiry(orgID, blockID, referrer)
}

// provisionalReferenceReleaseOutcome distinguishes the three ways a conditional
// reference delete can end, because the caller must act differently on each.
type provisionalReferenceReleaseOutcome int

const (
	// provisionalReferenceRemoved: the observed reference was deleted.
	provisionalReferenceRemoved provisionalReferenceReleaseOutcome = iota
	// provisionalReferenceAbsent: there was no reference left to delete. Nothing is
	// pinning the block through this referrer, so the release proceeds.
	provisionalReferenceAbsent
	// provisionalReferenceRenewed: a newer reference exists. It belongs to a live
	// upload; neither it nor its tracker may be touched.
	provisionalReferenceRenewed
)

var releaseProvisionalReadReferenceFn = func(database *DB, orgID, blockID, referrer string) (time.Time, bool, error) {
	return database.BlockReferenceCreatedAt(orgID, blockID, referrer)
}

var releaseProvisionalRemoveReferenceFn = func(database *DB, orgID, blockID, referrer string, createdAt time.Time) (provisionalReferenceReleaseOutcome, error) {
	applied, rowPresent, err := database.RemoveBlockReferenceIfCreatedAt(orgID, blockID, referrer, createdAt)
	if err != nil {
		return provisionalReferenceRenewed, err
	}
	switch {
	case applied:
		return provisionalReferenceRemoved, nil
	case rowPresent:
		return provisionalReferenceRenewed, nil
	default:
		return provisionalReferenceAbsent, nil
	}
}

var releaseProvisionalDeleteTrackerFn = func(database *DB, orgID, blockID, referrer string, expiresAt time.Time) (bool, error) {
	return database.DeleteProvisionalBlockReferenceExpiryIfExpiresAt(orgID, blockID, referrer, expiresAt)
}

// ReleaseProvisionalBlockReference retires one provisional (reference, tracker)
// pair when its upload finishes or is rolled back — and only the pair the caller
// observed.
//
// The asymmetry that dictates everything here: a **reference without a tracker is
// unrecoverable**, while a **tracker without a reference recovers itself**. GC
// Phase 0 discovers provisional refs only through the tracker's by-day projection,
// so an untracked reference is invisible: when its TTL retires it, nothing runs the
// zero-ref transition and the block, its metadata and its S3 object are retained
// forever. The reverse is routine — Phase 0 sees the reference gone, judges liveness
// and retires the tracker. Every step below is ordered to fail onto that side.
//
// Concurrency here is not theoretical: `up:` referrers are per session, so a retry
// of the same block, or a request admitted just before a commit began releasing,
// renews the very pair being released.
//
// BOTH deletes are therefore tied to what the release observed — the reference to
// its `created_at`, the tracker to its `expires_at`:
//
//   - Conditioning only the tracker leaves the renewal's *reference* exposed: the
//     release would delete a reference written after its observation, unpinning a
//     live upload, and the caller would then see zero references and promote the
//     block to a GC candidate. That is F9's failure mode arriving by another route.
//   - Conditioning only the reference leaves the renewal's *tracker* exposed, which
//     strands a live reference with no discovery projection (the original F10).
//
// A renewal that lands in either window simply wins both compares and keeps its
// pair whole; its own release or expiry deals with it later.
//
// referenceRemoved reports whether this call actually retired a reference. Callers
// use it to decide whether to run their zero-reference check, and it stays true even
// if tracker cleanup fails afterwards — swallowing it there would leave a block at
// zero references with no candidate and, once the orphaned projection is swept, no
// discovery path at all.
func (db *DB) ReleaseProvisionalBlockReference(orgID, blockID, referrer string) (referenceRemoved bool, err error) {
	if db == nil {
		return false, nil
	}

	// Both identities are captured before anything is mutated. These are the only
	// generations the conditional deletes below are allowed to retire.
	observedExpiry, err := releaseProvisionalReadExpiryFn(db, orgID, blockID, referrer)
	if err != nil {
		return false, err
	}
	observedCreatedAt, referencePresent, err := releaseProvisionalReadReferenceFn(db, orgID, blockID, referrer)
	if err != nil {
		return false, err
	}

	outcome := provisionalReferenceAbsent
	if referencePresent {
		// The compare is a Paxos round, so it is spent only when there is actually a
		// reference to guard. Rollback paths routinely release blocks that were never
		// referenced, and skipping the round there is safe: a reference appearing after
		// this observation arrives with a renewed tracker, which the compare below
		// refuses to retire.
		outcome, err = releaseProvisionalRemoveReferenceFn(db, orgID, blockID, referrer, observedCreatedAt)
		if err != nil {
			return false, fmt.Errorf("remove provisional block reference for org=%s block=%s referrer=%s: %w", orgID, blockID, referrer, err)
		}
	}
	if outcome == provisionalReferenceRenewed {
		// A newer reference owns this referrer now, and the tracker it arrived with
		// is the one keeping it discoverable. Touch neither.
		return false, nil
	}

	if observedExpiry.IsZero() {
		// No tracker existed when we looked. Anything present now belongs to a
		// renewal, and retiring it would be the exact bug this ordering prevents.
		return true, nil
	}
	if _, err := releaseProvisionalDeleteTrackerFn(db, orgID, blockID, referrer, observedExpiry); err != nil {
		// The reference is gone regardless, and the caller needs to know that to
		// promote a now-unreferenced block.
		return true, err
	}
	return true, nil
}

// DeleteProvisionalBlockReferenceExpiry removes the canonical expiry row and,
// when available, its by-day discovery projection, with no regard for what the
// caller last observed.
//
// Production release goes through ReleaseProvisionalBlockReference instead — an
// unconditional delete cannot tell a renewal's tracker from the one it meant to
// retire. This remains for test fixtures tearing down state they fully control.
func (db *DB) DeleteProvisionalBlockReferenceExpiry(orgID, blockID, referrer string, expiresAt time.Time) error {
	if db == nil {
		return nil
	}

	if expiresAt.IsZero() {
		err := db.Session().Query(`
			SELECT expires_at FROM gc_provisional_block_refs
			WHERE org_id = ? AND block_id = ? AND referrer = ?
		`, orgID, blockID, referrer).Scan(&expiresAt)
		if err != nil {
			if errors.Is(err, gocql.ErrNotFound) {
				return nil
			}
			return fmt.Errorf("read provisional block ref expiry for delete org=%s block=%s referrer=%s: %w", orgID, blockID, referrer, err)
		}
	}

	batch := db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		DELETE FROM gc_provisional_block_refs WHERE org_id = ? AND block_id = ? AND referrer = ?
	`, orgID, blockID, referrer)
	if !expiresAt.IsZero() {
		AddDeleteProvisionalBlockRefExpiryDiscoveryQuery(batch, orgID, blockID, referrer, expiresAt.UTC())
	}
	if err := batch.Exec(); err != nil {
		return fmt.Errorf("delete provisional block ref expiry for org=%s block=%s referrer=%s: %w", orgID, blockID, referrer, err)
	}
	return nil
}

// DeleteProvisionalBlockReferenceExpiryIfExpiresAt removes the tracker ONLY while
// it still carries the deadline the caller observed. GC Phase 0 decides a block is
// unpinned from a read of this row and then retires the tracker; without the
// compare an upload that renewed in that window would lose its tracking while
// keeping a live reference, and when that reference finally expired nothing would
// be left to notice the block went to zero references — a silent leak, the mirror
// image of F9's premature delete.
//
// applied=false means the row moved on (renewed or already retired) and the caller
// must leave it alone: the renewal has already rewritten the discovery projection
// to its new day. The projection for the observed deadline is dropped only after
// the compare succeeds, so a failure there degrades to an orphaned projection —
// which the scanner's canonical-missing branch cleans up — never to a lost tracker.
func (db *DB) DeleteProvisionalBlockReferenceExpiryIfExpiresAt(orgID, blockID, referrer string, expiresAt time.Time) (bool, error) {
	if db == nil || expiresAt.IsZero() {
		return false, nil
	}

	expiresAt = expiresAt.UTC()
	applied, err := db.Session().Query(`
		DELETE FROM gc_provisional_block_refs
		WHERE org_id = ? AND block_id = ? AND referrer = ?
		IF expires_at = ?
	`, orgID, blockID, referrer, expiresAt).MapScanCAS(map[string]interface{}{})
	if err != nil {
		return false, fmt.Errorf("conditionally delete provisional block ref expiry for org=%s block=%s referrer=%s: %w", orgID, blockID, referrer, err)
	}
	if !applied {
		return false, nil
	}

	batch := db.Session().Batch(gocql.LoggedBatch)
	AddDeleteProvisionalBlockRefExpiryDiscoveryQuery(batch, orgID, blockID, referrer, expiresAt)
	if err := batch.Exec(); err != nil {
		return true, fmt.Errorf("delete provisional block ref expiry projection for org=%s block=%s referrer=%s: %w", orgID, blockID, referrer, err)
	}
	return true, nil
}
