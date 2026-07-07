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

// ErrBlockIDMappingConflict indicates an external SHA-1 already maps to a
// DIFFERENT internal SHA-256. The web block-upload flow refuses to overwrite it
// (a crafted SHA-1 collision could otherwise corrupt downloads of already
// committed files); see WriteVerifiedWebBlockMapping.
var ErrBlockIDMappingConflict = errors.New("block id mapping conflict")

type BlockReuseDecision int

const (
	BlockReuseUnknownError BlockReuseDecision = iota
	BlockReuseReusable
	BlockReuseNeedsPut
	BlockReuseBlockedByGC
)

type BlockReuseProbe struct {
	Decision     BlockReuseDecision
	Sha1         string
	SizeBytes    int
	StorageClass string
	StorageKey   string
}

type blockReuseMetadataRow struct {
	Sha1         string
	SizeBytes    int
	StorageClass string
	StorageKey   string
	GCState      string
}

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
// "up:<operation_id>". Written with a TTL; superseded by the permanent fs_object
// reference once the upload is committed.
func BlockReferrerForUpload(operationID string) string {
	return blockReferrerUploadPrefix + operationID
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

// upsertBlockMetadataInsertFn does the first-writer-wins INSERT IF NOT EXISTS (the
// one LWT this path has always taken) and reports whether the row was created by
// this call. When applied, the row now holds exactly the sha1 and representation
// id passed here, so the caller can skip any read/repair entirely.
var upsertBlockMetadataInsertFn = func(database *DB, orgID, blockID, sha1 string, sizeBytes int, storageClass, storageKey string, now time.Time) (bool, error) {
	return upsertBlockMetadataInsertWithRepresentationFn(database, orgID, blockID, PlainBlockRepresentationID, sha1, sizeBytes, storageClass, storageKey, now)
}

var upsertBlockMetadataInsertWithRepresentationFn = func(database *DB, orgID, blockID, representationID, sha1 string, sizeBytes int, storageClass, storageKey string, now time.Time) (bool, error) {
	return database.Session().Query(`
		INSERT INTO blocks (org_id, block_id, representation_id, sha1, size_bytes, storage_class, storage_key, created_at, last_accessed)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) IF NOT EXISTS
	`, orgID, blockID, representationID, sha1, sizeBytes, storageClass, storageKey, now, now).MapScanCAS(map[string]interface{}{})
}

var readBlockIdentityForRepairFn = func(database *DB, orgID, blockID string) (string, string, bool, error) {
	var representationID string
	var sha1 string
	err := database.Session().Query(`
		SELECT representation_id, sha1
		FROM blocks
		WHERE org_id = ? AND block_id = ?
	`, orgID, blockID).Scan(&representationID, &sha1)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return "", "", false, nil
		}
		return "", "", false, err
	}
	return representationID, sha1, true, nil
}

var readBlockSHA1ForRepairFn = func(database *DB, orgID, blockID string) (string, bool, error) {
	_, sha1, found, err := readBlockIdentityForRepairFn(database, orgID, blockID)
	return sha1, found, err
}

// backfillBlockSHA1Fn fills in a missing sha1 with a compare-and-set against the
// sha1 value the caller just observed. That keeps the repair fail-closed in both
// races we care about:
//   - if the row was GC'd between read and write, applied=false (no phantom row),
//   - if another writer populated sha1 concurrently, applied=false (no overwrite).
var backfillBlockSHA1Fn = func(database *DB, orgID, blockID, sha1, expectedCurrent string) (bool, error) {
	return database.Session().Query(`
		UPDATE blocks
		SET sha1 = ?
		WHERE org_id = ? AND block_id = ?
		IF sha1 = ?
	`, sha1, orgID, blockID, expectedCurrent).ScanCAS()
}

var backfillBlockRepresentationIDFn = func(database *DB, orgID, blockID, representationID, expectedCurrent string) (bool, error) {
	return database.Session().Query(`
		UPDATE blocks
		SET representation_id = ?
		WHERE org_id = ? AND block_id = ?
		IF representation_id = ?
	`, representationID, orgID, blockID, expectedCurrent).ScanCAS()
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

// WriteBlockIDMapping writes the forward external SHA-1 -> internal SHA-256
// mapping used to resolve a bare-SHA-1 compatibility read inside one block
// representation domain.
func (db *DB) WriteBlockIDMapping(orgID, representationID, externalID, internalID string, createdAt time.Time) error {
	if db == nil {
		return nil
	}
	if err := ValidateBlockRepresentationID(representationID); err != nil {
		return err
	}
	ts := createdAt.UTC()
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	return db.Session().Query(`
		INSERT INTO block_id_mappings (org_id, representation_id, external_id, internal_id, created_at) VALUES (?, ?, ?, ?, ?)
	`, orgID, representationID, externalID, internalID, ts).Exec()
}

// GetBlockIDMapping resolves one external SHA-1 block ID to its internal SHA-256
// storage identity using the forward row scoped to one representation domain.
// ok == false means no mapping row exists.
func (db *DB) GetBlockIDMapping(orgID, representationID, externalID string) (internalID string, ok bool, err error) {
	if db == nil {
		return "", false, nil
	}
	if err := ValidateBlockRepresentationID(representationID); err != nil {
		return "", false, err
	}
	externalID = strings.TrimSpace(externalID)
	if externalID == "" {
		return "", false, nil
	}
	err = db.Session().Query(`
		SELECT internal_id FROM block_id_mappings WHERE org_id = ? AND representation_id = ? AND external_id = ?
	`, orgID, representationID, externalID).Scan(&internalID)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return "", false, nil
		}
		return "", false, err
	}
	return internalID, true, nil
}

// WriteVerifiedWebBlockMapping writes the forward external SHA-1 -> internal
// SHA-256 mapping for the WEB block-upload (session) flow ONLY. Both hashes are
// computed server-side from the block's real bytes in UploadBlock, so the mapping
// is verified content, never client-asserted.
//
// Unlike WriteBlockIDMapping (the shared legacy/seafhttp hot path, which does a
// plain idempotent INSERT and is intentionally left untouched), this guards
// against silently remapping an existing external SHA-1 to a DIFFERENT internal
// SHA-256 — which a crafted SHA-1 collision could exploit to corrupt downloads of
// already-committed files. The guard is a plain read-before-write, NOT a
// Cassandra LWT/Paxos: per-block Paxos on the upload hot path causes latency,
// contention, and timeouts in multi-DC deployments, and the commit-side
// forward-mapping check (file-from-blocks) remains the integrity authority
// regardless. The only residual gap versus LWT is two *colliding* blocks (same
// SHA-1, different content) racing the tiny read->write window — astronomically
// unlikely.
//
// Only the forward row is written and it is scoped to the caller's block
// representation domain.
func (db *DB) WriteVerifiedWebBlockMapping(orgID, representationID, externalID, internalID string, createdAt time.Time) error {
	if db == nil {
		return nil
	}
	if err := ValidateBlockRepresentationID(representationID); err != nil {
		return err
	}
	externalID = strings.TrimSpace(externalID)
	internalID = strings.TrimSpace(internalID)
	ts := createdAt.UTC()
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	existing, found, err := db.GetBlockIDMapping(orgID, representationID, externalID)
	if err != nil {
		return fmt.Errorf("read existing block mapping %s: %w", externalID, err)
	}
	if found {
		if strings.TrimSpace(existing) != internalID {
			return fmt.Errorf("%w: external %s already maps to %s (got %s)", ErrBlockIDMappingConflict, externalID, existing, internalID)
		}
		return nil
	}
	return db.Session().Query(`
		INSERT INTO block_id_mappings (org_id, representation_id, external_id, internal_id, created_at) VALUES (?, ?, ?, ?, ?)
	`, orgID, representationID, externalID, internalID, ts).Exec()
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
	return db.UpsertBlockMetadataWithSHA1(orgID, blockID, "", sizeBytes, storageClass, storageKey)
}

// UpsertBlockMetadataWithSHA1 stores immutable block metadata plus the
// authoritative SHA-1 of the real block bytes when that value is available at
// write time. Callers that do not know the SHA-1 can keep using
// UpsertBlockMetadata, which passes an empty string.
func (db *DB) UpsertBlockMetadataWithSHA1(orgID, blockID, sha1 string, sizeBytes int, storageClass, storageKey string) error {
	return db.UpsertBlockMetadataWithRepresentationAndSHA1(orgID, PlainBlockRepresentationID, blockID, sha1, sizeBytes, storageClass, storageKey)
}

func (db *DB) UpsertBlockMetadataWithRepresentationAndSHA1(orgID, representationID, blockID, sha1 string, sizeBytes int, storageClass, storageKey string) error {
	if err := ValidateBlockRepresentationID(representationID); err != nil {
		return err
	}
	sha1 = strings.TrimSpace(sha1)
	if sha1 != "" && !isHexN(sha1, 40) {
		return fmt.Errorf("invalid block sha1 for %s", blockID)
	}
	now := time.Now().UTC()
	insertFn := upsertBlockMetadataInsertWithRepresentationFn
	if representationID == PlainBlockRepresentationID {
		applied, err := upsertBlockMetadataInsertFn(db, orgID, blockID, sha1, sizeBytes, storageClass, storageKey, now)
		if err != nil {
			return err
		}
		if applied {
			return nil
		}
		return db.ensureBlockIdentity(orgID, blockID, representationID, sha1)
	}
	applied, err := insertFn(db, orgID, blockID, representationID, sha1, sizeBytes, storageClass, storageKey, now)
	if err != nil {
		return err
	}
	// Hot path: when this call created the row it already holds our sha1, and when
	// the caller has no sha1 there is nothing to add — either way return without an
	// extra read or a second LWT. The read + IF EXISTS repair below only runs when a
	// PRE-EXISTING row (dedup) might be missing its sha1.
	if applied {
		return nil
	}
	return db.ensureBlockIdentity(orgID, blockID, representationID, sha1)
}

func (db *DB) ensureBlockIdentity(orgID, blockID, representationID, sha1 string) error {
	var currentRepresentationID string
	var currentSHA1 string
	var found bool
	var err error
	if representationID == PlainBlockRepresentationID && (db == nil || db.session == nil) {
		currentSHA1, found, err = readBlockSHA1ForRepairFn(db, orgID, blockID)
		if err == nil && found {
			currentRepresentationID = representationID
		}
	} else if representationID == PlainBlockRepresentationID {
		currentSHA1, found, err = readBlockSHA1ForRepairFn(db, orgID, blockID)
		if err == nil && found {
			currentRepresentationID = representationID
		}
	} else {
		currentRepresentationID, currentSHA1, found, err = readBlockIdentityForRepairFn(db, orgID, blockID)
	}
	if err != nil {
		return fmt.Errorf("read block identity for %s: %w", blockID, err)
	}
	if !found {
		return fmt.Errorf("block metadata for %s disappeared before identity repair", blockID)
	}
	currentRepresentationID = strings.TrimSpace(currentRepresentationID)
	currentSHA1 = strings.TrimSpace(currentSHA1)
	if currentRepresentationID == "" {
		applied, err := backfillBlockRepresentationIDFn(db, orgID, blockID, representationID, currentRepresentationID)
		if err != nil {
			return fmt.Errorf("backfill block representation id for %s: %w", blockID, err)
		}
		if !applied {
			return fmt.Errorf("block metadata for %s changed before representation repair", blockID)
		}
	} else if currentRepresentationID != representationID {
		return fmt.Errorf("block %s already has conflicting representation id %s", blockID, currentRepresentationID)
	}
	if sha1 == "" {
		return nil
	}
	if currentSHA1 == "" {
		applied, err := backfillBlockSHA1Fn(db, orgID, blockID, sha1, currentSHA1)
		if err != nil {
			return fmt.Errorf("backfill block sha1 for %s: %w", blockID, err)
		}
		if !applied {
			return fmt.Errorf("block metadata for %s changed before sha1 repair", blockID)
		}
		return nil
	}
	if currentSHA1 != sha1 {
		return fmt.Errorf("block %s already has conflicting sha1 %s", blockID, currentSHA1)
	}
	return nil
}

func isHexN(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

var probeBlockReuseMetadataFn = func(database *DB, orgID, blockID string) (blockReuseMetadataRow, bool, error) {
	var row blockReuseMetadataRow
	err := database.Session().Query(`
		SELECT sha1, size_bytes, storage_class, storage_key, gc_state
		FROM blocks
		WHERE org_id = ? AND block_id = ?
	`, orgID, blockID).Scan(&row.Sha1, &row.SizeBytes, &row.StorageClass, &row.StorageKey, &row.GCState)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return blockReuseMetadataRow{}, false, nil
		}
		return blockReuseMetadataRow{}, false, err
	}
	return row, true, nil
}

var probeBlockReuseHasReferencesFn = func(database *DB, orgID, blockID string) (bool, error) {
	return database.BlockHasReferences(orgID, blockID)
}

var probeBlockReuseHasS3OrphanFn = func(database *DB, orgID, blockID string) (bool, error) {
	var existingBlockID string
	err := database.Session().Query(`
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

// ProbeBlockReuse classifies whether an uploaded block can safely skip S3 PUT,
// needs a direct PUT, or must back off because GC still owns the object.
func (db *DB) ProbeBlockReuse(orgID, blockID string) (BlockReuseProbe, error) {
	metadata, found, err := probeBlockReuseMetadataFn(db, orgID, blockID)
	if err != nil {
		return BlockReuseProbe{Decision: BlockReuseUnknownError}, fmt.Errorf("read block metadata for %s: %w", blockID, err)
	}
	if !found {
		hasOrphan, orphanErr := probeBlockReuseHasS3OrphanFn(db, orgID, blockID)
		if orphanErr != nil {
			return BlockReuseProbe{Decision: BlockReuseUnknownError}, fmt.Errorf("read S3 orphan fence for %s: %w", blockID, orphanErr)
		}
		if hasOrphan {
			return BlockReuseProbe{Decision: BlockReuseBlockedByGC}, nil
		}
		return BlockReuseProbe{Decision: BlockReuseNeedsPut}, nil
	}

	probe := BlockReuseProbe{
		Sha1:         strings.TrimSpace(metadata.Sha1),
		SizeBytes:    metadata.SizeBytes,
		StorageClass: strings.TrimSpace(metadata.StorageClass),
		StorageKey:   strings.TrimSpace(metadata.StorageKey),
	}
	if probe.StorageClass == "" {
		return BlockReuseProbe{Decision: BlockReuseUnknownError}, fmt.Errorf("block %s has empty canonical storage class", blockID)
	}
	if metadata.GCState == BlockGCStateDeleting {
		probe.Decision = BlockReuseBlockedByGC
		return probe, nil
	}

	hasReferences, refErr := probeBlockReuseHasReferencesFn(db, orgID, blockID)
	if refErr != nil {
		return BlockReuseProbe{Decision: BlockReuseUnknownError}, fmt.Errorf("read block references for %s: %w", blockID, refErr)
	}
	hasOrphan, orphanErr := probeBlockReuseHasS3OrphanFn(db, orgID, blockID)
	if orphanErr != nil {
		return BlockReuseProbe{Decision: BlockReuseUnknownError}, fmt.Errorf("read S3 orphan fence for %s: %w", blockID, orphanErr)
	}
	if hasOrphan {
		probe.Decision = BlockReuseBlockedByGC
		return probe, nil
	}
	if hasReferences {
		probe.Decision = BlockReuseReusable
		return probe, nil
	}
	probe.Decision = BlockReuseNeedsPut
	return probe, nil
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
