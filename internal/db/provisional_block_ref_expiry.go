package db

import (
	"errors"
	"fmt"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

// AddProvisionalBlockReferenceWithExpiry writes the TTL liveness row and both
// expiry records through one logged batch. Retrying is idempotent; stale by-day
// projections from a prior renewal are intentionally left for the scanner,
// which compares them with the canonical expiry before acting.
func (db *DB) AddProvisionalBlockReferenceWithExpiry(orgID, blockID, referrer, libraryID, storageClass string, expiresAt time.Time, ttlSeconds int) error {
	if db == nil || db.Session() == nil {
		return fmt.Errorf("database session is unavailable")
	}
	if expiresAt.IsZero() || ttlSeconds <= 0 {
		return fmt.Errorf("provisional block reference requires a positive expiry")
	}
	expiresAt = expiresAt.UTC()
	batch := db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		INSERT INTO block_references (org_id, block_id, referrer, library_id, created_at)
		VALUES (?, ?, ?, ?, ?) USING TTL ?
	`, orgID, blockID, referrer, libraryID, time.Now().UTC(), ttlSeconds)
	batch.Query(`
		INSERT INTO gc_provisional_block_refs (org_id, block_id, referrer, storage_class, expires_at)
		VALUES (?, ?, ?, ?, ?)
	`, orgID, blockID, referrer, storageClass, expiresAt)
	AddUpsertProvisionalBlockRefExpiryDiscoveryQuery(batch, orgID, blockID, referrer, storageClass, expiresAt)
	if err := batch.Exec(); err != nil {
		return fmt.Errorf("add provisional block reference for org=%s block=%s referrer=%s: %w", orgID, blockID, referrer, err)
	}
	return nil
}
func (db *DB) DeleteProvisionalBlockReferenceExpiryIfExpiresAt(orgID, blockID, referrer string, expiresAt time.Time) error {
	_, err := db.Session().Query(`
		DELETE FROM gc_provisional_block_refs
		WHERE org_id = ? AND block_id = ? AND referrer = ?
		IF expires_at = ?
	`, orgID, blockID, referrer, expiresAt.UTC()).MapScanCAS(map[string]interface{}{})
	if err != nil {
		return err
	}
	// The scanned generation's projection is stale whether the canonical CAS
	// applied or a concurrent renewal already replaced it.
	if err := db.Session().Query(`
		DELETE FROM gc_provisional_block_refs_by_day
		WHERE expiry_day = ? AND bucket = ? AND expires_at = ? AND org_id = ? AND block_id = ? AND referrer = ?
	`, GCProjectionUTCDate(expiresAt), GCDiscoveryBucket(orgID, blockID, referrer), expiresAt.UTC(), orgID, blockID, referrer).Exec(); err != nil {
		return err
	}
	return nil
}

// UpsertProvisionalBlockReferenceExpiry records the cleanup deadline for one
// provisional upload referrer. Each concurrent upload keeps its own expiry row,
// so liveness never collapses onto a single per-block future candidate.
func (db *DB) UpsertProvisionalBlockReferenceExpiry(orgID, blockID, referrer, storageClass string, expiresAt time.Time) error {
	if db == nil || expiresAt.IsZero() {
		return nil
	}

	expiresAt = expiresAt.UTC()
	var existingExpiresAt time.Time
	err := db.Session().Query(`
		SELECT expires_at FROM gc_provisional_block_refs
		WHERE org_id = ? AND block_id = ? AND referrer = ?
	`, orgID, blockID, referrer).Scan(&existingExpiresAt)
	if err != nil && !errors.Is(err, gocql.ErrNotFound) {
		return fmt.Errorf("read provisional block ref expiry for org=%s block=%s referrer=%s: %w", orgID, blockID, referrer, err)
	}
	if err != nil {
		existingExpiresAt = time.Time{}
	}

	batch := db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		INSERT INTO gc_provisional_block_refs (org_id, block_id, referrer, storage_class, expires_at)
		VALUES (?, ?, ?, ?, ?)
	`, orgID, blockID, referrer, storageClass, expiresAt)
	AddUpsertProvisionalBlockRefExpiryDiscoveryQuery(batch, orgID, blockID, referrer, storageClass, expiresAt)
	if !existingExpiresAt.IsZero() && !existingExpiresAt.Equal(expiresAt) {
		AddDeleteProvisionalBlockRefExpiryDiscoveryQuery(batch, orgID, blockID, referrer, existingExpiresAt)
	}
	if err := batch.Exec(); err != nil {
		return fmt.Errorf("upsert provisional block ref expiry for org=%s block=%s referrer=%s: %w", orgID, blockID, referrer, err)
	}
	return nil
}

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
