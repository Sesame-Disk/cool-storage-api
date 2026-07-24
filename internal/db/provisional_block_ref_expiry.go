package db

import (
	"errors"
	"fmt"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"
)

// ProvisionalBlockRefGeneration is the coherent identity of one provisional
// (reference, tracker) pair: a single opaque UUID minted per write and stamped on
// both rows, alongside the deadline that write recorded.
//
// It exists because the rows' own timestamps cannot serve as that identity.
// created_at and expires_at live in different tables, so reading one then the
// other lets a renewal slip between the reads and produce a MIXED observation —
// the old tracker's deadline paired with the new reference's stamp — which is
// enough to make a release delete a reference it never observed. They are also
// wall-clock values at millisecond resolution, so two renewals in the same
// millisecond are indistinguishable. One UUID read from one row has neither
// problem.
type ProvisionalBlockRefGeneration struct {
	GenerationID uuid.UUID
	ExpiresAt    time.Time
	Found        bool
}

// readProvisionalBlockRefGeneration returns the tracker's generation identity in a
// SINGLE read, so the caller never composes an identity out of two observations.
func (db *DB) readProvisionalBlockRefGeneration(orgID, blockID, referrer string) (ProvisionalBlockRefGeneration, error) {
	var generation ProvisionalBlockRefGeneration
	// The driver marshals its own gocql.UUID, not google/uuid's; both are [16]byte,
	// so the conversion at this boundary is free.
	var generationID gocql.UUID
	err := db.Session().Query(`
		SELECT expires_at, generation_id FROM gc_provisional_block_refs
		WHERE org_id = ? AND block_id = ? AND referrer = ?
	`, orgID, blockID, referrer).Scan(&generation.ExpiresAt, &generationID)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return ProvisionalBlockRefGeneration{}, nil
		}
		return ProvisionalBlockRefGeneration{}, fmt.Errorf("read provisional block ref generation for org=%s block=%s referrer=%s: %w", orgID, blockID, referrer, err)
	}
	generation.GenerationID = uuid.UUID(generationID)
	generation.Found = true
	return generation, nil
}

// readProvisionalBlockRefExpiry returns the currently recorded deadline for one
// provisional referrer, or the zero time when no row exists. Used by the write path
// to find the discovery projection an overwrite has to retract.
func (db *DB) readProvisionalBlockRefExpiry(orgID, blockID, referrer string) (time.Time, error) {
	generation, err := db.readProvisionalBlockRefGeneration(orgID, blockID, referrer)
	if err != nil {
		return time.Time{}, err
	}
	return generation.ExpiresAt, nil
}

// The release path deliberately has no "read the deadline" hook. Composing an
// identity out of more than one read is what let a renewal slip between them; the
// generation comes from readProvisionalBlockRefGeneration in one shot.

// addProvisionalBlockRefExpiryQueries stages the canonical expiry row plus its
// by-day discovery projection, retracting the projection the previous deadline
// left behind when the deadline moves.
func addProvisionalBlockRefExpiryQueries(batch *gocql.Batch, orgID, blockID, referrer, storageClass string, generationID uuid.UUID, expiresAt, existingExpiresAt time.Time) {
	batch.Query(`
		INSERT INTO gc_provisional_block_refs (org_id, block_id, referrer, storage_class, expires_at, generation_id)
		VALUES (?, ?, ?, ?, ?, ?)
	`, orgID, blockID, referrer, storageClass, expiresAt, generationID.String())
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

	// One identity for both rows, minted per write. A renewal mints a new one, which
	// is what lets a release tell "the pair I observed" from "the pair that replaced
	// it" with a single read.
	generationID := uuid.New()

	batch := db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		INSERT INTO block_references (org_id, block_id, referrer, library_id, created_at, generation_id)
		VALUES (?, ?, ?, ?, ?, ?) USING TTL ?
	`, orgID, blockID, referrer, libraryID, now, generationID.String(), ttlSeconds)
	addProvisionalBlockRefExpiryQueries(batch, orgID, blockID, referrer, storageClass, generationID, expiresAt, existingExpiresAt)
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

var releaseProvisionalReadGenerationFn = func(database *DB, orgID, blockID, referrer string) (ProvisionalBlockRefGeneration, error) {
	return database.readProvisionalBlockRefGeneration(orgID, blockID, referrer)
}

var releaseProvisionalRemoveReferenceFn = func(database *DB, orgID, blockID, referrer string, generationID uuid.UUID) (provisionalReferenceReleaseOutcome, error) {
	applied, rowPresent, err := database.RemoveBlockReferenceIfGeneration(orgID, blockID, referrer, generationID)
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

// ReleaseProvisionalBlockReference drops the provisional reference of an upload that
// finished or was rolled back — and only the reference of the generation the caller
// observed. It deliberately leaves the tracker alone.
//
// The asymmetry that dictates everything here: a **reference without a tracker is
// unrecoverable**, while a **tracker without a reference recovers itself**. GC
// Phase 0 discovers provisional refs only through the tracker's by-day projection,
// so an untracked reference is invisible: when its TTL retires it, nothing runs the
// zero-ref transition and the block, its metadata and its S3 object are retained
// forever. The reverse is routine — Phase 0 sees the reference gone, judges liveness
// and retires the tracker.
//
// Concurrency here is not theoretical: `up:` referrers are per session, so a retry
// of the same block, or a request admitted just before a commit began releasing,
// renews the very pair being released.
//
// Two properties make this safe, and both were learned the hard way:
//
//  1. **One read, one identity.** The generation is taken from the tracker in a
//     SINGLE read. An earlier version read the reference's `created_at` and the
//     tracker's `expires_at` separately, which let a renewal land between the reads
//     and hand the release a MIXED observation — the old deadline with the new
//     reference stamp — under which the reference compare *succeeds* and deletes a
//     reference belonging to a live upload.
//
//  2. **The release never retires tracking.** It deletes only the reference and
//     leaves the tracker to GC Phase 0, which retires it only after resolving
//     liveness (promoting an unreferenced block to a candidate first). That is what
//     makes the zero-reference transition durable: if this process dies right after
//     the reference delete — or the caller's liveness check or enqueue fails — the
//     tracker is still there and Phase 0 redoes the whole conclusion. Retiring it
//     here, even successfully, would erase the last discovery path the moment
//     anything downstream failed.
//
// referenceRemoved tells the caller whether a reference actually went away, which is
// its cue to run the zero-reference check. A renewal wins the compare and reports
// false, so the caller cannot promote a block a live upload still owns.
func (db *DB) ReleaseProvisionalBlockReference(orgID, blockID, referrer string) (referenceRemoved bool, err error) {
	if db == nil {
		return false, nil
	}

	// A single coherent observation. Everything below compares against this and
	// nothing re-reads, so no two generations can be mixed.
	observed, err := releaseProvisionalReadGenerationFn(db, orgID, blockID, referrer)
	if err != nil {
		return false, err
	}
	if !observed.Found {
		// No tracker: either this pair was already released, or a renewal is mid-flight
		// and its reference belongs to it. Deleting a reference with no tracking to
		// compare against is precisely the unguarded delete this exists to prevent.
		return false, nil
	}

	outcome, err := releaseProvisionalRemoveReferenceFn(db, orgID, blockID, referrer, observed.GenerationID)
	if err != nil {
		return false, fmt.Errorf("remove provisional block reference for org=%s block=%s referrer=%s: %w", orgID, blockID, referrer, err)
	}
	if outcome == provisionalReferenceRenewed {
		// A newer generation owns this referrer. Its reference and tracker belong
		// together and to it; touch neither.
		return false, nil
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

// DeleteProvisionalBlockReferenceExpiryIfGeneration removes the tracker ONLY while
// it still carries the generation the caller observed. GC Phase 0 decides a block is
// unpinned from a read of this row and then retires the tracker; without the
// compare an upload that renewed in that window would lose its tracking while
// keeping a live reference, and when that reference finally expired nothing would
// be left to notice the block went to zero references — a silent leak, the mirror
// image of F9's premature delete.
//
// The compare is on the generation, not the deadline: two renewals landing in the
// same millisecond share a deadline, and retiring the wrong one produces exactly
// that leak. This is also the only path in the system that retires tracking — a
// release deletes just its reference — so the tracker always outlives the liveness
// decision made from it.
//
// applied=false means the row moved on (renewed or already retired) and the caller
// must leave it alone: the renewal has already rewritten the discovery projection
// to its new day. The projection for the observed deadline is dropped only after
// the compare succeeds, so a failure there degrades to an orphaned projection —
// which the scanner's canonical-missing branch cleans up — never to a lost tracker.
func (db *DB) DeleteProvisionalBlockReferenceExpiryIfGeneration(orgID, blockID, referrer string, generationID uuid.UUID, expiresAt time.Time) (bool, error) {
	if db == nil || generationID == uuid.Nil || expiresAt.IsZero() {
		return false, nil
	}

	expiresAt = expiresAt.UTC()
	applied, err := db.Session().Query(`
		DELETE FROM gc_provisional_block_refs
		WHERE org_id = ? AND block_id = ? AND referrer = ?
		IF generation_id = ?
	`, orgID, blockID, referrer, generationID.String()).MapScanCAS(map[string]interface{}{})
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
