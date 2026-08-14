package db

import (
	"errors"
	"fmt"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

const (
	// ProvisionalBlockRefTrackerTTLGraceSeconds is the bounded period for which
	// the canonical tracker normally outlives its provisional reference. It
	// matches the default 24-hour GC scanner interval, giving the usual scan a
	// full extra cycle to observe the canonical row. Correctness does not depend
	// on scanning inside this window: the durable by-day projection remains after
	// the canonical TTL and drives canonical-missing recovery on a later scan.
	ProvisionalBlockRefTrackerTTLGraceSeconds = 24 * 60 * 60
)

// readProvisionalBlockRefExpiry returns the currently recorded deadline for one
// provisional referrer, or the zero time when no row exists. Used by the write path
// to find the discovery projection an overwrite has to retract.
func (db *DB) readProvisionalBlockRefExpiry(orgID, blockID, referrer string) (time.Time, error) {
	var expiresAt time.Time
	err := db.Session().Query(`
		SELECT expires_at FROM gc_provisional_block_refs
		WHERE org_id = ? AND block_id = ? AND referrer = ?
	`, orgID, blockID, referrer).Scan(&expiresAt)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return time.Time{}, nil
		}
		return time.Time{}, fmt.Errorf("read provisional block ref expiry for org=%s block=%s referrer=%s: %w", orgID, blockID, referrer, err)
	}
	return expiresAt.UTC(), nil
}

// addProvisionalBlockRefExpiryQueries stages the canonical expiry row plus its
// by-day discovery projection, retracting the projection the previous deadline
// left behind when the deadline moves.
func addProvisionalBlockRefExpiryQueries(batch *gocql.Batch, orgID, blockID, referrer, storageClass string, trackerTTLSeconds int, expiresAt, existingExpiresAt time.Time) {
	batch.Query(`
		INSERT INTO gc_provisional_block_refs (org_id, block_id, referrer, storage_class, expires_at)
		VALUES (?, ?, ?, ?, ?) USING TTL ?
	`, orgID, blockID, referrer, storageClass, expiresAt, trackerTTLSeconds)
	// Deliberately no TTL: this projection is the recovery anchor after the
	// canonical row self-expires, including when scanning is delayed beyond the
	// normal tracker margin.
	AddUpsertProvisionalBlockRefExpiryDiscoveryQuery(batch, orgID, blockID, referrer, storageClass, expiresAt)
	if !existingExpiresAt.IsZero() && !existingExpiresAt.Equal(expiresAt) {
		AddDeleteProvisionalBlockRefExpiryDiscoveryQuery(batch, orgID, blockID, referrer, existingExpiresAt)
	}
}

// AddProvisionalBlockReferenceWithExpiry writes an in-flight upload's block
// reference AND its GC expiry tracking in ONE logged batch (F10). When these
// rows were written separately, a failure between them left a reference with no
// discovery
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
	trackerTTLSeconds := ttlSeconds + ProvisionalBlockRefTrackerTTLGraceSeconds

	existingExpiresAt, err := db.readProvisionalBlockRefExpiry(orgID, blockID, referrer)
	if err != nil {
		return err
	}

	// The reference statement below is a block_references producer, so it carries the
	// same pin AddBlockReference does — see BlockReferenceWriteConsistency for why
	// inheriting the session is not good enough. Consistency on a batch is a property
	// of the batch, not of each statement, so the tracker and its projection are
	// written at the same level. That is the right side to err on anyway: they are the
	// only way Phase 0 ever finds this reference, and a tracker acknowledged by one
	// replica while the reference it retires is durable at quorum is its own leak.
	batch := db.Session().Batch(gocql.LoggedBatch).Consistency(BlockReferenceWriteConsistency)
	batch.Query(`
		INSERT INTO block_references (org_id, block_id, referrer, library_id, created_at)
		VALUES (?, ?, ?, ?, ?) USING TTL ?
	`, orgID, blockID, referrer, libraryID, now, ttlSeconds)
	addProvisionalBlockRefExpiryQueries(batch, orgID, blockID, referrer, storageClass, trackerTTLSeconds, expiresAt, existingExpiresAt)
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

// DeleteProvisionalBlockReferenceExpiry removes the canonical expiry row and,
// when available, its by-day discovery projection. It is retained for controlled
// teardown paths; production Phase 0 never deletes canonical trackers and relies
// on their TTL plus canonical-missing recovery instead.
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
