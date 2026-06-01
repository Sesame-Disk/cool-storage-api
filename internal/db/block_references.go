package db

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
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

	// PublishAttemptReferenceTTLSeconds bounds how long a pub:<attempt> crash
	// backstop can keep blocks alive after a winner commit becomes visible but
	// permanent fs_object refs have not been promoted yet. It must comfortably
	// exceed the stale-owner sweep window, repair lag, and prolonged restart
	// windows so reachable commits still have time to self-heal on recovery.
	PublishAttemptReferenceTTLSeconds = 35 * 24 * 60 * 60 // 35d

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

var publishAttemptPromotionRetryAttempts = 8

var publishAttemptPromotionRetryDelay = 50 * time.Millisecond

var publishAttemptPromotionRetryMaxDelay = 400 * time.Millisecond

var publishAttemptPromotionRetryJitter = 25 * time.Millisecond

var publishAttemptPromotionRetryJitterInt63n = rand.Int63n

var publishAttemptPromotionSleepFn = time.Sleep

var removePublishAttemptReferencesForPromotionFn = RemovePublishAttemptReferences

var addPublishAttemptReferenceFn = func(database *DB, orgID, blockID, referrer, repoID string) error {
	return database.AddBlockReference(orgID, blockID, referrer, repoID, PublishAttemptReferenceTTLSeconds)
}

var removePublishAttemptReferenceFn = func(database *DB, orgID, blockID, referrer string) error {
	return database.RemoveBlockReference(orgID, blockID, referrer)
}

func publishAttemptPromotionRetryBackoff(attempt int) time.Duration {
	if attempt < 1 || publishAttemptPromotionRetryDelay <= 0 {
		return 0
	}

	delay := publishAttemptPromotionRetryDelay
	for step := 1; step < attempt; step++ {
		delay *= 2
		if publishAttemptPromotionRetryMaxDelay > 0 && delay >= publishAttemptPromotionRetryMaxDelay {
			delay = publishAttemptPromotionRetryMaxDelay
			break
		}
	}

	if publishAttemptPromotionRetryMaxDelay > 0 && delay > publishAttemptPromotionRetryMaxDelay {
		delay = publishAttemptPromotionRetryMaxDelay
	}
	if publishAttemptPromotionRetryJitter > 0 && publishAttemptPromotionRetryJitterInt63n != nil {
		delay += time.Duration(publishAttemptPromotionRetryJitterInt63n(int64(publishAttemptPromotionRetryJitter)))
	}
	return delay
}

// BlockIDResolver optionally resolves caller-facing block IDs into the block ID
// partition key used in block_references before staging a publish attempt.
type BlockIDResolver func(blockIDs []string) ([]string, error)

// NormalizeBlockIDs trims, drops empties, and de-duplicates block IDs while
// preserving first-seen order. Returns nil when nothing usable remains. Callers
// that stage/remove references share this so the same key set is produced on both
// sides of an add/remove pair.
func NormalizeBlockIDs(blockIDs []string) []string {
	if len(blockIDs) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(blockIDs))
	normalized := make([]string, 0, len(blockIDs))
	for _, blockID := range blockIDs {
		blockID = strings.TrimSpace(blockID)
		if blockID == "" {
			continue
		}
		if _, dup := seen[blockID]; dup {
			continue
		}
		seen[blockID] = struct{}{}
		normalized = append(normalized, blockID)
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

// WriteBlockIDMappingDualWrite executes the forward mapping write, the reverse
// write, and optionally a compensating rollback of the forward write if the
// reverse side fails. Passing a nil rollback keeps the forward row in place,
// which is safer for caller-visible SHA-1 resolution than deleting a mapping
// that another concurrent writer may already rely on.
func WriteBlockIDMappingDualWrite(writeForward func() error, rollbackForward func() error, writeReverse func() error) error {
	if err := writeForward(); err != nil {
		return err
	}
	if err := writeReverse(); err != nil {
		if rollbackForward == nil {
			return err
		}
		if rollbackErr := rollbackForward(); rollbackErr != nil {
			return errors.Join(err, fmt.Errorf("rollback forward block mapping: %w", rollbackErr))
		}
		return err
	}
	return nil
}

// WriteBlockIDMapping dual-writes the external SHA-1 -> internal SHA-256 mapping
// plus the reverse lookup row used by GC cleanup. Reverse-write failures do NOT
// delete the forward row: client-visible SHA-1 resolution depends on that row,
// and deleting it after a partial failure can clobber a mapping that another
// concurrent writer has already made valid. Callers still fail closed so a
// later retry can heal the reverse lookup.
func (db *DB) WriteBlockIDMapping(orgID, externalID, internalID string, createdAt time.Time) error {
	if db == nil {
		return nil
	}
	ts := createdAt.UTC()
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	return WriteBlockIDMappingDualWrite(
		func() error {
			return db.Session().Query(`
				INSERT INTO block_id_mappings (org_id, external_id, internal_id, created_at) VALUES (?, ?, ?, ?)
			`, orgID, externalID, internalID, ts).Exec()
		},
		nil,
		func() error {
			return db.Session().Query(`
				INSERT INTO block_id_mappings_by_internal (org_id, internal_id, external_id, created_at) VALUES (?, ?, ?, ?)
			`, orgID, internalID, externalID, ts).Exec()
		},
	)
}

// AddPublishAttemptReferences stages temporary pub:<attempt> references for an
// in-flight metadata publish. Input IDs are normalized with TrimSpace + dedup so
// retrying callers can safely pass repeated or padded block IDs.
func AddPublishAttemptReferences(database *DB, orgID, repoID, attemptID string, blockIDs []string) error {
	_, err := addPublishAttemptReferencesRows(database, orgID, repoID, attemptID, blockIDs)
	return err
}

func addPublishAttemptReferencesRows(database *DB, orgID, repoID, attemptID string, blockIDs []string) ([]string, error) {
	if database == nil {
		return nil, nil
	}
	referrer := BlockReferrerForPublishAttempt(attemptID)
	staged := make([]string, 0, len(blockIDs))
	for _, blockID := range NormalizeBlockIDs(blockIDs) {
		if err := addPublishAttemptReferenceFn(database, orgID, blockID, referrer, repoID); err != nil {
			return staged, err
		}
		staged = append(staged, blockID)
	}
	return staged, nil
}

// RemovePublishAttemptReferences removes temporary pub:<attempt> references. It
// is safe to call repeatedly and collapses repeated delete errors with errors.Join.
func RemovePublishAttemptReferences(database *DB, orgID, attemptID string, blockIDs []string) error {
	if database == nil {
		return nil
	}
	referrer := BlockReferrerForPublishAttempt(attemptID)
	var removeErr error
	for _, blockID := range NormalizeBlockIDs(blockIDs) {
		if err := removePublishAttemptReferenceFn(database, orgID, blockID, referrer); err != nil {
			removeErr = errors.Join(removeErr, err)
		}
	}
	return removeErr
}

// StagePublishAttemptReferences resolves block IDs when needed, then records the
// attempt-local pub:<attempt> rows that keep blocks alive until HEAD publish wins.
// If a partial stage fails, this helper cleans up the rows written by this call
// before returning so direct callers do not leak stuck publish-attempt refs.
func StagePublishAttemptReferences(database *DB, orgID, repoID, attemptID string, blockIDs []string, resolve BlockIDResolver) ([]string, error) {
	resolved := blockIDs
	if resolve != nil {
		var err error
		resolved, err = resolve(blockIDs)
		if err != nil {
			return nil, err
		}
	}
	resolved = NormalizeBlockIDs(resolved)
	staged, err := addPublishAttemptReferencesRows(database, orgID, repoID, attemptID, resolved)
	if err != nil {
		if cleanupErr := RemovePublishAttemptReferences(database, orgID, attemptID, staged); cleanupErr != nil {
			return nil, errors.Join(err, fmt.Errorf("rollback staged publish-attempt refs for %s: %w", attemptID, cleanupErr))
		}
		return nil, err
	}
	return staged, nil
}

// PromotePublishAttemptReferences promotes an already-published fs_object to its
// permanent refs and then removes the temporary attempt-local pub:<attempt> rows.
// Both steps are idempotent, so bounded retries safely heal transient failures
// after HEAD is already visible without leaking attempt-local refs forever.
func PromotePublishAttemptReferences(database *DB, orgID, attemptID string, blockIDs []string, registerPermanent func() error) error {
	attempts := publishAttemptPromotionRetryAttempts
	if attempts < 1 {
		attempts = 1
	}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := registerPermanent(); err != nil {
			lastErr = err
		} else if err := removePublishAttemptReferencesForPromotionFn(database, orgID, attemptID, blockIDs); err != nil {
			lastErr = err
		} else {
			return nil
		}

		if attempt == attempts {
			break
		}
		sleepFor := publishAttemptPromotionRetryBackoff(attempt)
		if sleepFor > 0 {
			publishAttemptPromotionSleepFn(sleepFor)
		}
	}
	return lastErr
}

// UpsertBlockMetadata stores immutable block metadata (size, storage class/key)
// if the row does not already exist. It never sets liveness — that lives in
// block_references — so it is safe to call on every (deduplicated) upload of the
// same content.
//
// Uses INSERT ... IF NOT EXISTS (a per-block LWT) deliberately. storage_class and
// storage_key are NOT globally fixed per block: uploads pick the class per library
// and per routing region (see resolveLibraryBlockStore), so two writers of the
// same content can carry different classes. First-writer-wins pins ONE canonical
// physical location so reads and GC resolve the backend where the block actually
// landed; a plain last-writer-wins INSERT could repoint metadata at a class that
// holds no copy, breaking downloads and making GC act on the wrong physical copy.
// This LWT is scoped to the (org, block) partition, so writers for different
// blocks never contend — it is NOT globally serialized (the old process-wide
// concurrency permit that did serialize it has been removed).
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

type BlockS3OrphanInfo struct {
	StorageClass string
	FirstSeenAt  time.Time
}

type BlockDeleteClaimInfo struct {
	StorageClass string
	GCState      string
	GCClaimID    string
}

// BlockDeleteFenceActive reports whether GC still owns the physical object for
// this block. Writers must treat both an in-row gc_state='deleting' claim and a
// pending gc_s3_orphans row as an active fence; otherwise a re-upload can race
// with orphan recovery and lose the object after the canonical block row was
// already deleted.
func (db *DB) BlockDeleteFenceActive(orgID, blockID string) (bool, error) {
	gcState, err := db.BlockGCState(orgID, blockID)
	if err != nil {
		return false, err
	}
	if gcState == BlockGCStateDeleting {
		return true, nil
	}
	var existingBlockID string
	err = db.Session().Query(`
		SELECT block_id FROM gc_s3_orphans WHERE org_id = ? AND block_id = ? LIMIT 1
	`, orgID, blockID).Scan(&existingBlockID)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return existingBlockID != "", nil
}

func (db *DB) GetBlockS3OrphanInfo(orgID, blockID string) (BlockS3OrphanInfo, bool, error) {
	var info BlockS3OrphanInfo
	err := db.Session().Query(`
		SELECT storage_class, first_seen_at FROM gc_s3_orphans
		WHERE org_id = ? AND block_id = ?
	`, orgID, blockID).Scan(&info.StorageClass, &info.FirstSeenAt)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return BlockS3OrphanInfo{}, false, nil
		}
		return BlockS3OrphanInfo{}, false, err
	}
	return info, true, nil
}

func (db *DB) GetBlockDeleteClaimInfo(orgID, blockID string) (BlockDeleteClaimInfo, bool, error) {
	var info BlockDeleteClaimInfo
	err := db.Session().Query(`
		SELECT storage_class, gc_state, gc_claim_id FROM blocks
		WHERE org_id = ? AND block_id = ?
	`, orgID, blockID).Scan(&info.StorageClass, &info.GCState, &info.GCClaimID)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return BlockDeleteClaimInfo{}, false, nil
		}
		return BlockDeleteClaimInfo{}, false, err
	}
	return info, true, nil
}

func (db *DB) ReleaseBlockDeleteClaim(orgID, blockID, claimID string) (bool, error) {
	return db.Session().Query(`
		UPDATE blocks SET gc_state = null, gc_claim_id = null, gc_claimed_at = null
		WHERE org_id = ? AND block_id = ?
		IF gc_state = ? AND gc_claim_id = ?
	`, orgID, blockID, BlockGCStateDeleting, claimID).MapScanCAS(map[string]interface{}{})
}

func (db *DB) DeleteBlockS3Orphan(orgID, blockID string, firstSeenAt time.Time) error {
	if firstSeenAt.IsZero() {
		if err := db.Session().Query(`
			SELECT first_seen_at FROM gc_s3_orphans WHERE org_id = ? AND block_id = ?
		`, orgID, blockID).Scan(&firstSeenAt); err != nil {
			if !errors.Is(err, gocql.ErrNotFound) {
				return fmt.Errorf("failed to read gc_s3_orphans row for delete: %w", err)
			}
		}
	}

	if err := db.Session().Query(`
		DELETE FROM gc_s3_orphans WHERE org_id = ? AND block_id = ?
	`, orgID, blockID).Exec(); err != nil {
		return err
	}

	if firstSeenAt.IsZero() {
		return nil
	}
	if err := db.Session().Query(`
		DELETE FROM gc_s3_orphans_by_day
		WHERE first_seen_day = ? AND bucket = ? AND first_seen_at = ? AND org_id = ? AND block_id = ?
	`, GCProjectionUTCDate(firstSeenAt), GCDiscoveryBucket(orgID, blockID), firstSeenAt.UTC(), orgID, blockID).Exec(); err != nil {
		return err
	}
	return nil
}
