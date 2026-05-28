package db

import (
	"errors"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

// Row-per-reference block liveness model (replaces blocks.ref_count).
//
// A block is alive iff at least one row exists in block_references for it. Adding
// a reference is an idempotent INSERT, removing one an idempotent DELETE — neither
// needs LWT, so concurrent uploads/deletes in different regions no longer collide
// on a shared mutable counter. Expensive Paxos is reserved for the GC worker's
// claim just before the irreversible S3 delete (see the gc store's claim path).

const (
	blockReferrerFSObjectPrefix = "fs:"
	blockReferrerPublishPrefix  = "pub:"
	blockReferrerUploadPrefix   = "up:"

	// ProvisionalBlockReferenceTTLSeconds bounds how long an in-flight upload's
	// provisional reference survives before being promoted to a permanent
	// fs_object reference. It must exceed the longest realistic gap between
	// uploading blocks and committing the fs_object (large resumable/chunked
	// uploads). An abandoned upload's rows expire on their own — no permanent leak.
	ProvisionalBlockReferenceTTLSeconds = 48 * 60 * 60 // 48h

	// BlockGCStateDeleting marks a block row claimed by the GC worker for an
	// imminent S3 delete. Writers that observe it must back off and retry.
	BlockGCStateDeleting = "deleting"
)

// BlockReferrerForFSObject builds the permanent referrer for a block referenced
// by an fs_object: "fs:<library_id>:<fs_id>". Because fs_id is content-addressed,
// the same file content always yields the same referrer, so registering the
// reference is naturally idempotent under client retries (no counter inflation).
func BlockReferrerForFSObject(libraryID, fsID string) string {
	return blockReferrerFSObjectPrefix + libraryID + ":" + fsID
}

// BlockReferrerForPublishAttempt builds a temporary referrer for an in-flight
// metadata publish attempt: "pub:<attempt_id>". Writers use it while preparing
// a new commit so a failed head-CAS cleanup can remove only the attempt-local
// referrer instead of touching shared fs:<library>:<fs_id> rows.
func BlockReferrerForPublishAttempt(attemptID string) string {
	return blockReferrerPublishPrefix + attemptID
}

// BlockReferrerForUpload builds the provisional referrer for an in-flight upload:
// "up:<upload_token>". Written with a TTL; superseded by the permanent fs_object
// reference once the upload is committed.
func BlockReferrerForUpload(uploadToken string) string {
	return blockReferrerUploadPrefix + uploadToken
}

// UpsertBlockMetadata stores immutable block metadata (size, storage class/key)
// if the row does not already exist. It never sets liveness — that lives in
// block_references — so it is safe to call on every (deduplicated) upload of the
// same content. Uses INSERT ... IF NOT EXISTS so a concurrent creator wins cleanly.
func (db *DB) UpsertBlockMetadata(orgID, blockID string, sizeBytes int, storageClass, storageKey string) error {
	now := time.Now().UTC()
	return db.Session().Query(`
		INSERT INTO blocks (org_id, block_id, size_bytes, storage_class, storage_key, created_at, last_accessed)
		VALUES (?, ?, ?, ?, ?, ?, ?) IF NOT EXISTS
	`, orgID, blockID, sizeBytes, storageClass, storageKey, now, now).Exec()
}

// AddBlockReference registers a reference to a block. Idempotent: re-adding the
// same (block, referrer) overwrites a row with identical key. ttlSeconds > 0 makes
// the row expire (provisional upload references); 0 means permanent.
func (db *DB) AddBlockReference(orgID, blockID, referrer, libraryID string, ttlSeconds int) error {
	now := time.Now().UTC()
	if ttlSeconds > 0 {
		return db.Session().Query(`
			INSERT INTO block_references (org_id, block_id, referrer, library_id, created_at)
			VALUES (?, ?, ?, ?, ?) USING TTL ?
		`, orgID, blockID, referrer, libraryID, now, ttlSeconds).Exec()
	}
	return db.Session().Query(`
		INSERT INTO block_references (org_id, block_id, referrer, library_id, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, orgID, blockID, referrer, libraryID, now).Exec()
}

// RemoveBlockReference deletes a single (block, referrer) reference. Idempotent:
// deleting a non-existent row is a no-op, so a retried GC pass or upload rollback
// is always safe.
func (db *DB) RemoveBlockReference(orgID, blockID, referrer string) error {
	return db.Session().Query(`
		DELETE FROM block_references WHERE org_id = ? AND block_id = ? AND referrer = ?
	`, orgID, blockID, referrer).Exec()
}

// BlockHasReferences reports whether any reference row still exists for the block.
// This single-partition point read replaces reading the mutable blocks.ref_count.
func (db *DB) BlockHasReferences(orgID, blockID string) (bool, error) {
	var referrer string
	err := db.Session().Query(`
		SELECT referrer FROM block_references WHERE org_id = ? AND block_id = ? LIMIT 1
	`, orgID, blockID).Scan(&referrer)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// BlockGCState returns the block's gc_state ("" when unset or the block row is
// absent). Writers consult it to back off while the GC worker is mid-delete.
func (db *DB) BlockGCState(orgID, blockID string) (string, error) {
	var gcState string
	err := db.Session().Query(`
		SELECT gc_state FROM blocks WHERE org_id = ? AND block_id = ?
	`, orgID, blockID).Scan(&gcState)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return "", nil
		}
		return "", err
	}
	return gcState, nil
}
