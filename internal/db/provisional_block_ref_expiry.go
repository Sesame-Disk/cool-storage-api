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
// reference AND its GC expiry tracking in ONE logged batch, so the two can never
// exist apart (F10). Written separately, a failure between them left a reference
// with no discovery projection: GC Phase 0 enumerates provisional refs through
// `gc_provisional_block_refs_by_day`, so an unprojected reference pins the block
// forever with nothing able to find it. A logged batch either applies every
// statement or none, which removes the split state instead of compensating for
// it afterwards.
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

// DeleteProvisionalBlockReferenceExpiry removes the canonical expiry row and,
// when available, its by-day discovery projection.
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
