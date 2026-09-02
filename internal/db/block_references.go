package db

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

// Row-per-reference block liveness model (replaces blocks.ref_count).
//
// A block is alive iff at least one row exists in block_references for it. Adding
// a reference is an idempotent INSERT, removing one an idempotent DELETE — neither
// needs LWT, so concurrent uploads/deletes in different regions no longer collide
// on a shared mutable counter. Separate LWTs still protect first-writer canonical
// metadata and conditional GC/stub lifecycle transitions.

const (
	blockReferrerFSObjectPrefix = "fs:"
	blockReferrerPublishPrefix  = "pub:"
	blockReferrerUploadPrefix   = "up:"

	// ProvisionalBlockReferenceTTLSeconds bounds how long an in-flight upload's
	// provisional reference survives. A committed fs_object gets a separate
	// permanent reference; the up: row still expires only by TTL. The duration must
	// exceed the longest realistic gap between uploading blocks and committing the
	// fs_object (large resumable/chunked uploads). Abandoned rows expire on their
	// own — no permanent leak.
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
	// BlockGCStateRepairingStub is a short-lived upload-owned claim used only to
	// remove a released metadata-free claim stub. Other readers fail closed on it.
	BlockGCStateRepairingStub = "repairing_stub"
)

// ErrBlockIDMappingConflict indicates an external SHA-1 already maps to a
// DIFFERENT internal SHA-256 inside the same representation domain. Writers fail
// closed instead of overwriting such a row.
var ErrBlockIDMappingConflict = errors.New("block id mapping conflict")

// ErrBlockMetadataPermanent marks a metadata-write failure that is
// deterministically irrecoverable — an invalid argument (missing storage class,
// malformed sha1/representation id), a conflicting first-writer identity, or a
// corrupt row. Retrying only burns the caller's bounded budget, so the upload
// materialization wrapper must NOT retry it. Everything left unmarked (raw
// Cassandra driver I/O, lost CAS races, transient stub/fence states) is treated
// as transient and retried. Keeping the permanent set small is the safe bias: a
// transient mislabeled permanent fails a recoverable upload, while a permanent
// mislabeled transient only wastes a bounded budget before failing.
var ErrBlockMetadataPermanent = errors.New("block metadata write is permanently failed")

// ErrInstallBlockMetadataIdentityContradiction means a definite non-applied
// create-only CAS returned the exact proposed tuple. That tuple may have come
// from an earlier use of the minted identity, so it grants neither success nor
// cleanup authority and must never be retried.
var ErrInstallBlockMetadataIdentityContradiction = errors.New("single-use block install identity was already present")

// ErrBlockRepairAuthorityChanged means the canonical row is absent or no longer
// names the physical tuple the caller verified. The caller may re-probe and retry
// against the newly observed incarnation, but must not repair the stale tuple.
var ErrBlockRepairAuthorityChanged = errors.New("block repair authority changed")

// ErrBlockRepairBlocked means GC currently owns or fences the logical block.
// Repair must wait until both the in-row claim and the A+ orphan fence are clear.
var ErrBlockRepairBlocked = errors.New("block repair blocked by GC")

// ErrBlockRepairAuthorityPermanent marks malformed input or canonical row state.
// Re-reading cannot make corrupt locator, ownership, or immutable metadata valid.
var ErrBlockRepairAuthorityPermanent = errors.New("block repair authority is permanently invalid")

type BlockReuseDecision int

const (
	BlockReuseUnknownError BlockReuseDecision = iota
	BlockReuseReusable
	BlockReuseNeedsPut
	BlockReuseBlockedByGC
	BlockReuseRepairableStub
)

type BlockReuseProbe struct {
	Decision     BlockReuseDecision
	Sha1         string
	SizeBytes    int
	StorageClass string
	StorageKey   string
}

// InstallBlockMetadataOutcome is the authority returned by the single-use
// canonical metadata install. Callers must branch on this value, not Cause.
type InstallBlockMetadataOutcome int

const (
	// InstallBlockMetadataAmbiguous authorizes neither use nor cleanup of the
	// proposed physical object.
	InstallBlockMetadataAmbiguous InstallBlockMetadataOutcome = iota
	// InstallBlockMetadataApplied means Canonical is the proposed physical tuple.
	InstallBlockMetadataApplied
	// InstallBlockMetadataKnownLost means Canonical names a different complete
	// tuple, or is empty when settlement proved that no canonical row exists.
	InstallBlockMetadataKnownLost
	// InstallBlockMetadataIdentityContradiction is a definite direct CAS result
	// that found the exact proposed tuple already present. It authorizes neither
	// use nor cleanup of the single-use proposal.
	InstallBlockMetadataIdentityContradiction
)

type BlockPhysicalLocation struct {
	StorageClass string
	StorageKey   string
}

// BlockRepairAuthorityOutcome classifies authority to repair one exact existing
// physical incarnation. Callers must not treat an unknown outcome as authority.
type BlockRepairAuthorityOutcome int

const (
	BlockRepairAuthorityUnknown BlockRepairAuthorityOutcome = iota
	BlockRepairAuthorityAuthorized
	BlockRepairAuthorityChanged
	BlockRepairAuthorityBlocked
	BlockRepairAuthorityPermanent
)

type InstallBlockMetadataResult struct {
	Outcome   InstallBlockMetadataOutcome
	Canonical BlockPhysicalLocation
	// Submitted is true once the single-use INSTALL LWT has been entered. It is
	// provenance, not an authority signal; callers still branch on Outcome.
	Submitted bool
	// Cause is diagnostic only. It never grants authority to use or clean up a
	// physical object; Outcome is the complete authority contract.
	Cause error
}

type installedBlockMetadataRow struct {
	Location            BlockPhysicalLocation
	StorageClassPresent bool
	StorageKeyPresent   bool
}

type blockReuseMetadataRow struct {
	Sha1                string
	SizeBytes           int
	StorageClass        string
	StorageClassPresent bool
	StorageKey          string
	GCState             string
	GCClaimID           string
	GCClaimedAt         *time.Time
	CreatedAt           *time.Time
}

type blockIdentityRepairRow struct {
	RepresentationID    string
	Sha1                string
	SizeBytes           int
	StorageClass        string
	StorageClassPresent bool
	StorageKey          string
	GCState             string
	GCClaimID           string
	GCClaimedAt         *time.Time
	CreatedAt           *time.Time
}

type blockRepairAuthorityRow struct {
	blockIdentityRepairRow
	StorageKeyPresent bool
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
// "up:<operation_id>". It is written with a TTL and remains until that TTL even
// after the upload creates a separate permanent fs_object reference.
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

// installBlockMetadataLWTFn is deliberately separate from the repair-capable
// create path. A proposed physical incarnation is single-use, so this statement
// gets exactly one driver attempt and is never repeated by settlement.
var installBlockMetadataLWTFn = func(ctx context.Context, database *DB, orgID, blockID, representationID, sha1 string, sizeBytes int, proposed BlockPhysicalLocation, now time.Time) (bool, map[string]interface{}, error) {
	current := map[string]interface{}{}
	applied, err := database.Session().Query(`
		INSERT INTO blocks (org_id, block_id, representation_id, sha1, size_bytes, storage_class, storage_key, created_at, last_accessed)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) IF NOT EXISTS
	`, orgID, blockID, representationID, sha1, sizeBytes, proposed.StorageClass, proposed.StorageKey, now, now).
		WithContext(ctx).
		SerialConsistency(gocql.Serial).
		Idempotent(false).
		RetryPolicy(&gocql.SimpleRetryPolicy{NumRetries: 0}).
		SetSpeculativeExecutionPolicy(&gocql.NonSpeculativeExecution{}).
		MapScanCAS(current)
	return applied, current, err
}

var settleInstalledBlockMetadataFn = func(ctx context.Context, database *DB, orgID, blockID string) (installedBlockMetadataRow, bool, error) {
	var row installedBlockMetadataRow
	var storageClass *string
	var storageKey *string
	err := database.Session().Query(`
		SELECT storage_class, storage_key
		FROM blocks
		WHERE org_id = ? AND block_id = ?
	`, orgID, blockID).
		WithContext(ctx).
		Consistency(gocql.Serial).
		Scan(&storageClass, &storageKey)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return installedBlockMetadataRow{}, false, nil
		}
		return installedBlockMetadataRow{}, false, err
	}
	if storageClass != nil {
		row.Location.StorageClass = *storageClass
		row.StorageClassPresent = true
	}
	if storageKey != nil {
		row.Location.StorageKey = *storageKey
		row.StorageKeyPresent = true
	}
	return row, true, nil
}

var readBlockIdentityForRepairFn = func(database *DB, orgID, blockID string) (blockIdentityRepairRow, bool, error) {
	var row blockIdentityRepairRow
	var storageClass *string
	err := database.Session().Query(`
		SELECT representation_id, sha1, storage_class, storage_key, gc_state, gc_claim_id, gc_claimed_at, created_at
		FROM blocks
		WHERE org_id = ? AND block_id = ?
	`, orgID, blockID).Scan(
		&row.RepresentationID,
		&row.Sha1,
		&storageClass,
		&row.StorageKey,
		&row.GCState,
		&row.GCClaimID,
		&row.GCClaimedAt,
		&row.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return blockIdentityRepairRow{}, false, nil
		}
		return blockIdentityRepairRow{}, false, err
	}
	if storageClass != nil {
		row.StorageClass = *storageClass
		row.StorageClassPresent = true
	}
	return row, true, nil
}

// BlockAuthorityRead selects how strongly a writer-side fence read must observe
// the GC lifecycle.
//
// BlockAuthorityStrong is a linearizable SERIAL read. It costs a global Paxos
// round trip, so it is reserved for the one decision that must observe a fence
// published moments earlier: the exact-incarnation revalidation immediately
// before a physical repair PUT (R10).
//
// BlockAuthorityAdvisory is a quorum read, not an inherited one. It is correct
// wherever the answer gates an early-out and the real authority is enforced
// structurally downstream — by the single-use INSTALL LWT for a fresh
// incarnation, or by the tuple-bound, non-creating CAS in
// RepairBlockMetadataIfCurrent (R17). Because ClaimBlockDelete and
// StartBlockDeleteOrphan publish with EACH_QUORUM commit visibility, a
// LOCAL_QUORUM read intersects that commit in every DC and therefore observes
// every committed fence; what it may miss is a fence whose Paxos commit is still
// in flight, and every such caller has downstream authority that rejects the
// stale observation.
//
// The level is pinned rather than inherited BECAUSE that intersection is the
// whole argument. `database.consistency` accepts ONE (config.go), and a ONE read
// can land on a replica that never received the commit — which would let a writer
// see no fence at all and mint a new incarnation while the previous lifecycle's
// orphan is still live. A fence read therefore declares the consistency its own
// correctness requires instead of trusting operator configuration, exactly as
// BlockHasReferencesGlobal does for the destructive side.
type BlockAuthorityRead int

const (
	BlockAuthorityAdvisory BlockAuthorityRead = iota
	BlockAuthorityStrong
)

// BlockFenceReadConsistency is the weakest read that still intersects an
// EACH_QUORUM fence publication in the reader's own DC.
const BlockFenceReadConsistency = gocql.LocalQuorum

func (mode BlockAuthorityRead) apply(query *gocql.Query) *gocql.Query {
	if mode == BlockAuthorityStrong {
		return query.Consistency(gocql.Serial)
	}
	return query.Consistency(BlockFenceReadConsistency)
}

// These seams keep existing-incarnation repair separate from create-only install.
// Existing-incarnation repair is non-creating and must remain bound to the tuple
// and lifecycle state that granted authority.
var readBlockRepairAuthorityFn = func(database *DB, orgID, blockID string, mode BlockAuthorityRead) (blockRepairAuthorityRow, bool, error) {
	var row blockRepairAuthorityRow
	var storageClass *string
	var storageKey *string
	err := mode.apply(database.Session().Query(`
		SELECT representation_id, sha1, size_bytes, storage_class, storage_key, gc_state, gc_claim_id, gc_claimed_at, created_at
		FROM blocks
		WHERE org_id = ? AND block_id = ?
	`, orgID, blockID)).Scan(
		&row.RepresentationID,
		&row.Sha1,
		&row.SizeBytes,
		&storageClass,
		&storageKey,
		&row.GCState,
		&row.GCClaimID,
		&row.GCClaimedAt,
		&row.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return blockRepairAuthorityRow{}, false, nil
		}
		return blockRepairAuthorityRow{}, false, err
	}
	if storageClass != nil {
		row.StorageClass = *storageClass
		row.StorageClassPresent = true
	}
	if storageKey != nil {
		row.StorageKey = *storageKey
		row.StorageKeyPresent = true
	}
	return row, true, nil
}

var blockRepairHasS3OrphanFn = func(database *DB, orgID, blockID string, mode BlockAuthorityRead) (bool, error) {
	var existingBlockID string
	err := mode.apply(database.Session().Query(`
		SELECT block_id FROM gc_s3_orphans WHERE org_id = ? AND block_id = ? LIMIT 1
	`, orgID, blockID)).Scan(&existingBlockID)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return existingBlockID != "", nil
}

var backfillCurrentBlockRepresentationIDFn = func(database *DB, orgID, blockID, representationID, expectedCurrent string, expected BlockPhysicalLocation, expectedCreatedAt time.Time, expectedSizeBytes int) (bool, error) {
	return database.Session().Query(`
		UPDATE blocks
		SET representation_id = ?
		WHERE org_id = ? AND block_id = ?
		IF representation_id = ?
		AND storage_class = ? AND storage_key = ?
		AND created_at = ? AND size_bytes = ?
		AND gc_state = null AND gc_claim_id = null AND gc_claimed_at = null
	`, representationID, orgID, blockID, expectedCurrent, expected.StorageClass, expected.StorageKey, expectedCreatedAt, expectedSizeBytes).
		SerialConsistency(gocql.Serial).
		ScanCAS()
}

var backfillCurrentBlockSHA1Fn = func(database *DB, orgID, blockID, sha1, expectedCurrent, expectedRepresentationID string, expected BlockPhysicalLocation, expectedCreatedAt time.Time, expectedSizeBytes int) (bool, error) {
	return database.Session().Query(`
		UPDATE blocks
		SET sha1 = ?
		WHERE org_id = ? AND block_id = ?
		IF sha1 = ? AND representation_id = ?
		AND storage_class = ? AND storage_key = ?
		AND created_at = ? AND size_bytes = ?
		AND gc_state = null AND gc_claim_id = null AND gc_claimed_at = null
	`, sha1, orgID, blockID, expectedCurrent, expectedRepresentationID, expected.StorageClass, expected.StorageKey, expectedCreatedAt, expectedSizeBytes).
		SerialConsistency(gocql.Serial).
		ScanCAS()
}

var claimReleasedBlockStubForRepairFn = func(database *DB, orgID, blockID, repairID string, claimedAt time.Time) (bool, error) {
	return database.Session().Query(`
		UPDATE blocks
		SET gc_state = ?, gc_claim_id = ?, gc_claimed_at = ?
		WHERE org_id = ? AND block_id = ?
		IF created_at = null
		AND storage_class = null
		AND gc_state = null
		AND gc_claim_id = null
		AND gc_claimed_at = null
	`, BlockGCStateRepairingStub, repairID, claimedAt, orgID, blockID).
		SerialConsistency(gocql.Serial).
		MapScanCAS(map[string]interface{}{})
}

var deleteRepairClaimedBlockStubFn = func(database *DB, orgID, blockID, repairID string) (bool, error) {
	return database.Session().Query(`
		DELETE FROM blocks
		WHERE org_id = ? AND block_id = ?
		IF created_at = null
		AND storage_class = null
		AND gc_state = ?
		AND gc_claim_id = ?
		AND gc_claimed_at != null
	`, orgID, blockID, BlockGCStateRepairingStub, repairID).
		SerialConsistency(gocql.Serial).
		MapScanCAS(map[string]interface{}{})
}

var blockStubRepairIDFn = func(orgID, blockID string) string {
	return "upload-repair:" + orgID + ":" + blockID
}

var deleteClaimedBlockStubFn = func(database *DB, orgID, blockID, claimID string) (bool, error) {
	return database.Session().Query(`
		DELETE FROM blocks
		WHERE org_id = ? AND block_id = ?
		IF created_at = null
		AND storage_class = null
		AND gc_state = ?
		AND gc_claim_id = ?
		AND gc_claimed_at != null
	`, orgID, blockID, BlockGCStateDeleting, claimID).
		SerialConsistency(gocql.Serial).
		MapScanCAS(map[string]interface{}{})
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
// representation domain. The write is idempotent for the same internal ID and
// fails closed on a conflicting remap, except for the documented tiny
// read-before-write race between two same-key concurrent SHA-1 collisions.
func (db *DB) WriteBlockIDMapping(orgID, representationID, externalID, internalID string, createdAt time.Time) error {
	if db == nil {
		return nil
	}
	if err := ValidateBlockRepresentationID(representationID); err != nil {
		return err
	}
	externalID = NormalizeBlockID(externalID)
	internalID = NormalizeBlockID(internalID)
	ts := createdAt.UTC()
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	return db.writeCheckedBlockIDMapping(orgID, representationID, externalID, internalID, ts)
}

var getBlockIDMappingForWriteCheckFn = func(database *DB, orgID, representationID, externalID string) (string, bool, error) {
	return database.GetBlockIDMapping(orgID, representationID, externalID)
}

var insertBlockIDMappingForWriteCheckFn = func(database *DB, orgID, representationID, externalID, internalID string, createdAt time.Time) error {
	return database.Session().Query(`
		INSERT INTO block_id_mappings (org_id, representation_id, external_id, internal_id, created_at) VALUES (?, ?, ?, ?, ?)
	`, orgID, representationID, externalID, internalID, createdAt).Exec()
}

// GetBlockIDMapping resolves one external SHA-1 block ID to its internal SHA-256
// storage identity using the forward row scoped to one representation domain.
// ok == false means no mapping row exists.
//
// This contextless form is for callers that resolve a single mapping as part of
// a write, where there is no per-request budget to respect and the driver's own
// timeout is the bound. Anything that resolves mappings in bulk must use
// GetBlockIDMappingContext instead: a loop of contextless reads cannot be
// stopped by a client disconnect or a request deadline, which is precisely the
// unbounded work subcontract C exists to close.
func (db *DB) GetBlockIDMapping(orgID, representationID, externalID string) (internalID string, ok bool, err error) {
	return db.GetBlockIDMappingContext(context.Background(), orgID, representationID, externalID)
}

// GetBlockIDMappingContext is GetBlockIDMapping bound to a context, so an
// in-flight read is abandoned when the caller's deadline expires or its client
// goes away.
func (db *DB) GetBlockIDMappingContext(ctx context.Context, orgID, representationID, externalID string) (internalID string, ok bool, err error) {
	if db == nil {
		return "", false, nil
	}
	if err := ValidateBlockRepresentationID(representationID); err != nil {
		return "", false, err
	}
	externalID = NormalizeBlockID(externalID)
	if externalID == "" {
		return "", false, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	err = db.Session().Query(`
		SELECT internal_id FROM block_id_mappings WHERE org_id = ? AND representation_id = ? AND external_id = ?
	`, orgID, representationID, externalID).WithContext(ctx).Scan(&internalID)
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
// The actual write contract is shared with WriteBlockIDMapping: create when
// absent, succeed when the same row already exists, and fail closed when the
// same (org, representation, external) key points at a different internal ID.
// The guard is a plain read-before-write, NOT a Cassandra LWT/Paxos: per-block
// Paxos on the upload hot path causes latency, contention, and timeouts in
// multi-DC deployments, and the commit-side forward-mapping check remains the
// integrity authority regardless. The only residual gap versus LWT is two
// *colliding* blocks (same SHA-1, different content) racing the tiny read->write
// window — astronomically unlikely.
func (db *DB) WriteVerifiedWebBlockMapping(orgID, representationID, externalID, internalID string, createdAt time.Time) error {
	if db == nil {
		return nil
	}
	if err := ValidateBlockRepresentationID(representationID); err != nil {
		return err
	}
	externalID = NormalizeBlockID(externalID)
	internalID = NormalizeBlockID(internalID)
	ts := createdAt.UTC()
	if ts.IsZero() {
		ts = time.Now().UTC()
	}
	return db.writeCheckedBlockIDMapping(orgID, representationID, externalID, internalID, ts)
}

func (db *DB) writeCheckedBlockIDMapping(orgID, representationID, externalID, internalID string, createdAt time.Time) error {
	// Tagged permanent because they are deterministic input rejections: the same
	// ids will fail identically on every attempt. The post-install mapping retry
	// loop reads this tag to stop immediately instead of spending its whole budget
	// on a write that cannot succeed.
	if !isHexN(externalID, 40) {
		return fmt.Errorf("%w: invalid external block id for mapping %s", ErrBlockMetadataPermanent, externalID)
	}
	if !isHexN(internalID, 64) {
		return fmt.Errorf("%w: invalid internal block id for mapping %s", ErrBlockMetadataPermanent, internalID)
	}
	existing, found, err := getBlockIDMappingForWriteCheckFn(db, orgID, representationID, externalID)
	if err != nil {
		return fmt.Errorf("read existing block mapping %s: %w", externalID, err)
	}
	if found {
		if strings.TrimSpace(existing) != internalID {
			return fmt.Errorf("%w: external %s already maps to %s (got %s)", ErrBlockIDMappingConflict, externalID, existing, internalID)
		}
		return nil
	}
	return insertBlockIDMappingForWriteCheckFn(db, orgID, representationID, externalID, internalID, createdAt)
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

// ValidateBlockRepairAuthority grants authority only for the exact canonical
// physical incarnation the caller supplies. Under conservative A+, either GC
// ownership shape or any gc_s3_orphans row blocks repair.
// ValidateBlockRepairAuthority is the pre-PUT boundary (R10) and therefore always
// reads at BlockAuthorityStrong: it is the one decision that must observe a fence
// published moments earlier, and it runs only when an existing physical object
// turned out to be missing and needs repair -- a cold path, not the dedup path.
func (db *DB) ValidateBlockRepairAuthority(orgID, blockID string, expected BlockPhysicalLocation) (BlockRepairAuthorityOutcome, error) {
	_, outcome, err := db.validateBlockRepairAuthority(orgID, blockID, expected, BlockAuthorityStrong)
	return outcome, err
}

// ValidateBorrowedFSPublicationAuthority re-validates that blocks(L) still
// names the exact physical placement `expected` immediately before a
// BorrowedFS writer may publish HEAD. It shares BlockRepairAuthorityOutcome's
// classification (Authorized / Blocked / Changed / Permanent / Unknown) with
// ValidateBlockRepairAuthority, but reads at BlockAuthorityAdvisory
// (LOCAL_QUORUM), not BlockAuthorityStrong (SERIAL).
//
// That is safe here for a DIFFERENT reason than the other Advisory callers'
// downstream CAS: this call has no downstream mutation on the block to fall
// back on -- once it says Authorized, HEAD publishes unconditionally. What
// makes Advisory safe here is ORDERING plus the shape of the two possible
// staleness directions, not a CAS. By the time this runs, the caller has
// ALREADY durably written its own up:<session> reference
// (BlockReferenceWriteConsistency = LOCAL_QUORUM). Three cases cover every
// interleaving with GC:
//
//  1. GC's zero-proof read (BlockHasReferencesGlobal, EACH_QUORUM) happens
//     after that pin is durable: it intersects the pin in every DC, so GC
//     releases the claim instead of committing D. This block can never
//     become fenced by that attempt, regardless of what this read sees.
//  2. GC's claim already landed (ClaimBlockDelete, EACH_QUORUM+SERIAL)
//     before the pin was written, but D has not committed yet: a settled
//     EACH_QUORUM write is what LOCAL_QUORUM reads are specifically proven
//     to intersect in every DC (the same argument BlockDeleteFenceActive
//     already relies on), so this read observes gc_state='deleting' ->
//     Blocked.
//  3. GC has already fully retired P1 (D committed, orphan published,
//     Finalize's row DELETE, and the settled orphan DELETE all durably
//     applied) before the pin was written. FinalizeBlockDelete and
//     DeleteS3Orphan do not themselves pin EACH_QUORUM -- they only pin the
//     LWT's serial phase -- so a lagging replica could in principle still
//     be missing one of those deletes. That lag can only bias this read
//     toward the STALE-BUT-STILL-PRESENT state (blocks(L) with
//     gc_state='deleting', or the orphan row not yet cleared), which
//     classifies as Blocked; it can never manufacture a false Authorized,
//     because a replica cannot report a row absent before it has actually
//     received the tombstone that removed it. The terminal "nothing left to
//     see" state (Changed: canonical row absent, orphan absent) is
//     therefore reachable only once every one of GC's deletes has actually,
//     durably propagated -- at which point the retirement genuinely is
//     complete everywhere this read can land.
//
// A fourth interleaving -- GC's claim was released or taken over before D
// committed -- does not weaken this: a released/superseded claim can no
// longer reach the irreversible commit, so it cannot produce a fence this
// read would need to catch.
func (db *DB) ValidateBorrowedFSPublicationAuthority(orgID, blockID string, expected BlockPhysicalLocation) (BlockRepairAuthorityOutcome, error) {
	_, outcome, err := db.validateBlockRepairAuthority(orgID, blockID, expected, BlockAuthorityAdvisory)
	return outcome, err
}

func (db *DB) validateBlockRepairAuthority(orgID, blockID string, expected BlockPhysicalLocation, mode BlockAuthorityRead) (blockRepairAuthorityRow, BlockRepairAuthorityOutcome, error) {
	if blockID != NormalizeBlockID(blockID) || !IsSHA256BlockID(blockID) {
		return blockRepairAuthorityRow{}, BlockRepairAuthorityPermanent, blockRepairPermanentError("block id %q is not a canonical lower-case SHA-256", blockID)
	}
	if !config.IsCanonicalStorageClassName(expected.StorageClass) {
		return blockRepairAuthorityRow{}, BlockRepairAuthorityPermanent, blockRepairPermanentError("non-canonical storage class %q for block %s", expected.StorageClass, blockID)
	}
	// DB authority accepts both minted and legacy deterministic keys. The storage
	// layer verifies tenant/block binding; this boundary only rejects an incomplete
	// or textually malformed persisted locator.
	if expected.StorageKey == "" || strings.TrimSpace(expected.StorageKey) != expected.StorageKey {
		return blockRepairAuthorityRow{}, BlockRepairAuthorityPermanent, blockRepairPermanentError("invalid storage key for block %s", blockID)
	}

	// Read order is load-bearing, and the proof is the GC lifecycle's own write
	// order: ClaimBlockDelete stamps gc_state, StartBlockDeleteOrphan writes the
	// orphan, and only then does FinalizeBlockDelete remove the canonical row
	// (worker.go). The orphan is therefore the LAST fence read on every path, so
	// that an absent canonical row can never be mistaken for "no fence": if the
	// row is already gone when we read it, that lifecycle's orphan was durably
	// written strictly earlier, and the orphan read that follows must observe it.
	// Reading the orphan first would leave exactly the window this ordering
	// closes — orphan absent, then GC orphans and drops the row, then a rowless
	// read reports no fence at all.
	row, found, err := readBlockRepairAuthorityFn(db, orgID, blockID, mode)
	if err != nil {
		return blockRepairAuthorityRow{}, BlockRepairAuthorityUnknown, fmt.Errorf("read block repair authority for %s: %w", blockID, err)
	}
	hasOrphan, err := blockRepairHasS3OrphanFn(db, orgID, blockID, mode)
	if err != nil {
		return blockRepairAuthorityRow{}, BlockRepairAuthorityUnknown, fmt.Errorf("read S3 orphan repair fence for %s: %w", blockID, err)
	}
	if !found {
		if hasOrphan {
			return blockRepairAuthorityRow{}, BlockRepairAuthorityBlocked, fmt.Errorf("%w: block %s has an orphan fence without a canonical row", ErrBlockRepairBlocked, blockID)
		}
		return blockRepairAuthorityRow{}, BlockRepairAuthorityChanged, fmt.Errorf("%w: canonical row for block %s is absent", ErrBlockRepairAuthorityChanged, blockID)
	}
	if row.CreatedAt == nil || !row.StorageClassPresent || !row.StorageKeyPresent || !config.IsCanonicalStorageClassName(row.StorageClass) || row.StorageKey == "" || strings.TrimSpace(row.StorageKey) != row.StorageKey {
		return blockRepairAuthorityRow{}, BlockRepairAuthorityPermanent, blockRepairPermanentError("block %s has incomplete or malformed canonical locator", blockID)
	}
	if row.SizeBytes < 0 {
		return blockRepairAuthorityRow{}, BlockRepairAuthorityPermanent, blockRepairPermanentError("block %s has invalid negative size", blockID)
	}
	activeClaim, repairClaim, ownershipErr := classifyBlockClaimOwnership(row.GCState, row.GCClaimID, row.GCClaimedAt)
	if ownershipErr != nil {
		return blockRepairAuthorityRow{}, BlockRepairAuthorityPermanent, blockRepairPermanentError("block %s has malformed GC ownership: %v", blockID, ownershipErr)
	}
	current := BlockPhysicalLocation{StorageClass: row.StorageClass, StorageKey: row.StorageKey}
	if current != expected {
		return blockRepairAuthorityRow{}, BlockRepairAuthorityChanged, fmt.Errorf("%w: block %s now names %s/%s", ErrBlockRepairAuthorityChanged, blockID, current.StorageClass, current.StorageKey)
	}
	if activeClaim || repairClaim {
		return blockRepairAuthorityRow{}, BlockRepairAuthorityBlocked, fmt.Errorf("%w: block %s has an active %s claim", ErrBlockRepairBlocked, blockID, strings.TrimSpace(row.GCState))
	}
	if hasOrphan {
		return blockRepairAuthorityRow{}, BlockRepairAuthorityBlocked, fmt.Errorf("%w: block %s has an S3 orphan fence", ErrBlockRepairBlocked, blockID)
	}
	return row, BlockRepairAuthorityAuthorized, nil
}

func blockRepairPermanentError(format string, args ...interface{}) error {
	detail := fmt.Sprintf(format, args...)
	return fmt.Errorf("%w: %w: %s", ErrBlockRepairAuthorityPermanent, ErrBlockMetadataPermanent, detail)
}

// RepairBlockMetadataIfCurrent repairs immutable identity metadata only while the
// canonical row still names expected and remains outside every A+ GC fence. It
// never executes INSERT and its conditional UPDATE statements cannot create a row.
func (db *DB) RepairBlockMetadataIfCurrent(orgID, representationID, blockID, sha1 string, sizeBytes int, expected BlockPhysicalLocation) error {
	if !IsCanonicalBlockRepresentationID(representationID) {
		return blockRepairPermanentError("invalid block representation id %q", representationID)
	}
	sha1 = NormalizeBlockID(sha1)
	if sha1 != "" && !IsSHA1BlockID(sha1) {
		return blockRepairPermanentError("invalid block sha1 for %s", blockID)
	}
	if sizeBytes < 0 {
		return blockRepairPermanentError("invalid negative block size for %s", blockID)
	}

	// Advisory, not strong: this path never creates a row, and every mutation it
	// can make is a tuple-bound CAS that re-checks gc_state at Paxos level. A
	// stale read cannot grant authority here -- it can only fail to short-circuit
	// early, and the CAS then rejects. Paying a global SERIAL round trip on every
	// deduplicated block to shorten that early-out is not a trade worth making.
	row, _, err := db.validateBlockRepairAuthority(orgID, blockID, expected, BlockAuthorityAdvisory)
	if err != nil {
		return err
	}
	if row.SizeBytes != sizeBytes {
		return blockRepairPermanentError("block %s already has conflicting size %d", blockID, row.SizeBytes)
	}
	currentRepresentationID := row.RepresentationID
	if currentRepresentationID != "" {
		if strings.TrimSpace(currentRepresentationID) != currentRepresentationID || !IsCanonicalBlockRepresentationID(currentRepresentationID) {
			return blockRepairPermanentError("block %s has malformed representation id %q", blockID, currentRepresentationID)
		}
		if currentRepresentationID != representationID {
			return blockRepairPermanentError("block %s already has conflicting representation id %s", blockID, currentRepresentationID)
		}
	}
	currentSHA1 := row.Sha1
	if currentSHA1 != "" {
		if strings.TrimSpace(currentSHA1) != currentSHA1 || !IsSHA1BlockID(currentSHA1) {
			return blockRepairPermanentError("block %s has malformed sha1 %q", blockID, currentSHA1)
		}
		if sha1 != "" && currentSHA1 != sha1 {
			return blockRepairPermanentError("block %s already has conflicting sha1 %s", blockID, currentSHA1)
		}
	}

	createdAt := row.CreatedAt.UTC()
	if currentRepresentationID == "" {
		applied, err := backfillCurrentBlockRepresentationIDFn(db, orgID, blockID, representationID, "", expected, createdAt, sizeBytes)
		if err != nil {
			return fmt.Errorf("backfill current block representation id for %s: %w", blockID, err)
		}
		if !applied {
			return fmt.Errorf("%w: block %s changed before representation repair", ErrBlockRepairAuthorityChanged, blockID)
		}
		currentRepresentationID = representationID
	}
	if sha1 == "" || currentSHA1 != "" {
		return nil
	}
	applied, err := backfillCurrentBlockSHA1Fn(db, orgID, blockID, sha1, "", currentRepresentationID, expected, createdAt, sizeBytes)
	if err != nil {
		return fmt.Errorf("backfill current block sha1 for %s: %w", blockID, err)
	}
	if !applied {
		return fmt.Errorf("%w: block %s changed before sha1 repair", ErrBlockRepairAuthorityChanged, blockID)
	}
	return nil
}

// InstallBlockMetadata performs a create-only canonical install for one freshly
// minted physical incarnation. It submits exactly one non-idempotent LWT. If the
// mutation result is unknown, it performs one bounded SERIAL read; it never
// repeats the proposed install.
//
// Production upload paths use this only for a freshly minted-and-PUT target;
// canonical reuse and repair continue through RepairBlockMetadataIfCurrent.
func (db *DB) InstallBlockMetadata(ctx context.Context, orgID, representationID, blockID, sha1 string, sizeBytes int, proposed BlockPhysicalLocation) InstallBlockMetadataResult {
	result := InstallBlockMetadataResult{Outcome: InstallBlockMetadataAmbiguous}
	if err := ValidateBlockRepresentationID(representationID); err != nil {
		result.Cause = fmt.Errorf("%w: %w", ErrBlockMetadataPermanent, err)
		return result
	}
	// This function is the single-use canonical install boundary, so it validates
	// its own inputs rather than trusting the caller to have done it. Production
	// reaches it only behind ValidateMintedPhysicalLocator, which already checks
	// the SHA-256, but that is the caller's guarantee, not this seam's -- and a
	// future internal caller must not be able to install a canonical row under a
	// block id that is not a SHA-256. Returns before Submitted is set, so a
	// rejection here is conclusively unsubmitted.
	//
	// The check is deliberately fail-closed on the EXACT spelling rather than on a
	// normalized copy. blockID is the partition key the LWT and the settlement
	// SELECT both use verbatim, so validating NormalizeBlockID(blockID) and then
	// persisting blockID would accept "  ABCD..  " and install the canonical row
	// under a padded, upper-case identity that is not the block's content address
	// -- validating one string and persisting another. At an identity boundary the
	// caller must arrive already canonical; silent normalization here would just
	// move the inconsistency downstream.
	if blockID != NormalizeBlockID(blockID) || !IsSHA256BlockID(blockID) {
		result.Cause = fmt.Errorf("%w: block id %q is not a canonical lower-case SHA-256 for canonical install", ErrBlockMetadataPermanent, blockID)
		return result
	}
	if !config.IsCanonicalStorageClassName(proposed.StorageClass) {
		result.Cause = fmt.Errorf("%w: non-canonical storage class %q for block %s", ErrBlockMetadataPermanent, proposed.StorageClass, blockID)
		return result
	}
	if proposed.StorageKey == "" || strings.TrimSpace(proposed.StorageKey) != proposed.StorageKey {
		result.Cause = fmt.Errorf("%w: invalid canonical storage key for block %s", ErrBlockMetadataPermanent, blockID)
		return result
	}
	sha1 = NormalizeBlockID(sha1)
	if sha1 != "" && !isHexN(sha1, 40) {
		result.Cause = fmt.Errorf("%w: invalid block sha1 for %s", ErrBlockMetadataPermanent, blockID)
		return result
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		result.Cause = err
		return result
	}

	now := time.Now().UTC()
	// The seam is the DB execution boundary. Once it is entered, even a
	// preflight/transport error from the driver is post-submit for cleanup
	// purposes; the LWT itself is single-use and must not be repeated.
	result.Submitted = true
	applied, current, installErr := installBlockMetadataLWTFn(ctx, db, orgID, blockID, representationID, sha1, sizeBytes, proposed, now)
	if installErr == nil {
		if applied {
			return InstallBlockMetadataResult{Outcome: InstallBlockMetadataApplied, Canonical: proposed, Submitted: true}
		}
		classified := classifyInstalledBlockMetadataCAS(current, proposed)
		classified.Submitted = true
		return classified
	}

	settlementTimeout := db.config.Timeout
	if settlementTimeout <= 0 {
		settlementTimeout = defaultCassandraTimeout
	}
	settlementCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), settlementTimeout)
	defer cancel()
	row, found, settlementErr := settleInstalledBlockMetadataFn(settlementCtx, db, orgID, blockID)
	if settlementErr != nil {
		result.Cause = errors.Join(installErr, fmt.Errorf("settle canonical install: %w", settlementErr))
		return result
	}
	if !found {
		return InstallBlockMetadataResult{Outcome: InstallBlockMetadataKnownLost, Submitted: true, Cause: installErr}
	}
	settled := classifySettledBlockMetadataRow(row, proposed)
	settled.Submitted = true
	settled.Cause = errors.Join(installErr, settled.Cause)
	return settled
}

func classifyInstalledBlockMetadataCAS(current map[string]interface{}, proposed BlockPhysicalLocation) InstallBlockMetadataResult {
	row := installedBlockMetadataRow{}
	if value, ok := current["storage_class"]; ok && value != nil {
		storageClass, stringOK := value.(string)
		if !stringOK {
			return malformedInstalledBlockMetadataResult("storage_class is not text")
		}
		row.Location.StorageClass = storageClass
		row.StorageClassPresent = true
	}
	if value, ok := current["storage_key"]; ok && value != nil {
		storageKey, stringOK := value.(string)
		if !stringOK {
			return malformedInstalledBlockMetadataResult("storage_key is not text")
		}
		row.Location.StorageKey = storageKey
		row.StorageKeyPresent = true
	}
	if malformed := validateInstalledBlockMetadataRow(row); malformed != nil {
		return *malformed
	}
	if row.Location == proposed {
		return InstallBlockMetadataResult{
			Outcome: InstallBlockMetadataIdentityContradiction,
			Cause:   fmt.Errorf("%w: direct CAS for %s/%s returned the proposed tuple", ErrInstallBlockMetadataIdentityContradiction, proposed.StorageClass, proposed.StorageKey),
		}
	}
	return InstallBlockMetadataResult{Outcome: InstallBlockMetadataKnownLost, Canonical: row.Location}
}

func classifySettledBlockMetadataRow(row installedBlockMetadataRow, proposed BlockPhysicalLocation) InstallBlockMetadataResult {
	if malformed := validateInstalledBlockMetadataRow(row); malformed != nil {
		return *malformed
	}
	if row.Location == proposed {
		return InstallBlockMetadataResult{Outcome: InstallBlockMetadataApplied, Canonical: row.Location}
	}
	return InstallBlockMetadataResult{Outcome: InstallBlockMetadataKnownLost, Canonical: row.Location}
}

func validateInstalledBlockMetadataRow(row installedBlockMetadataRow) *InstallBlockMetadataResult {
	if !row.StorageClassPresent || !row.StorageKeyPresent || !config.IsCanonicalStorageClassName(row.Location.StorageClass) || row.Location.StorageKey == "" || strings.TrimSpace(row.Location.StorageKey) != row.Location.StorageKey {
		result := malformedInstalledBlockMetadataResult("canonical physical tuple is incomplete or malformed")
		return &result
	}
	return nil
}

func malformedInstalledBlockMetadataResult(reason string) InstallBlockMetadataResult {
	return InstallBlockMetadataResult{
		Outcome: InstallBlockMetadataAmbiguous,
		Cause:   fmt.Errorf("malformed canonical block metadata: %s", reason),
	}
}

func classifyBlockClaimOwnership(gcState, claimID string, claimedAt *time.Time) (bool, bool, error) {
	gcState = strings.TrimSpace(gcState)
	claimID = strings.TrimSpace(claimID)
	if gcState == "" {
		if claimID != "" || claimedAt != nil {
			return false, false, errors.New("claim identity exists without lifecycle state")
		}
		return false, false, nil
	}
	if gcState != BlockGCStateDeleting && gcState != BlockGCStateRepairingStub {
		return false, false, fmt.Errorf("unknown gc_state %q", gcState)
	}
	if claimID == "" || claimedAt == nil {
		return false, false, errors.New("lifecycle state is missing claim identity")
	}
	return gcState == BlockGCStateDeleting, gcState == BlockGCStateRepairingStub, nil
}

func (db *DB) RepairReleasedBlockStub(orgID, blockID string) (bool, error) {
	repairID := blockStubRepairIDFn(orgID, blockID)
	claimed, err := claimReleasedBlockStubForRepairFn(db, orgID, blockID, repairID, time.Now().UTC())
	if err != nil {
		owned, _, confirmErr := db.blockStubRepairClaimStatus(orgID, blockID, repairID)
		if confirmErr != nil || !owned {
			return false, err
		}
	}
	if !claimed {
		owned, _, confirmErr := db.blockStubRepairClaimStatus(orgID, blockID, repairID)
		if confirmErr != nil {
			return false, confirmErr
		}
		if !owned {
			return false, nil
		}
	}

	hasOrphan, orphanErr := probeBlockReuseHasS3OrphanFn(db, orgID, blockID)
	deleted, deleteErr := db.deleteOwnedBlockStubRepairClaim(orgID, blockID, repairID)
	if deleteErr != nil {
		return false, fmt.Errorf("remove block stub repair claim: %w", deleteErr)
	}
	if !deleted {
		// The row changed under us (another uploader finished materializing, or GC
		// stole the claim with its `IF gc_state != 'deleting'` LWT). This is a benign
		// concurrency loss, not corruption: nothing was deleted and the CAS stayed
		// closed. Report it as retryable so the caller re-probes and converges to
		// Reusable or BlockedByGC instead of surfacing a hard 500.
		return false, nil
	}
	if orphanErr != nil {
		return false, fmt.Errorf("recheck S3 orphan fence during stub repair: %w", orphanErr)
	}
	if hasOrphan {
		return false, nil
	}
	return true, nil
}

func (db *DB) deleteOwnedBlockStubRepairClaim(orgID, blockID, repairID string) (bool, error) {
	deleted, err := deleteRepairClaimedBlockStubFn(db, orgID, blockID, repairID)
	if err == nil && deleted {
		return true, nil
	}
	owned, found, confirmErr := db.blockStubRepairClaimStatus(orgID, blockID, repairID)
	if confirmErr != nil {
		return false, confirmErr
	}
	if !found {
		return true, nil
	}
	if !owned {
		return false, nil
	}
	deleted, retryErr := deleteRepairClaimedBlockStubFn(db, orgID, blockID, repairID)
	if retryErr != nil {
		return false, retryErr
	}
	return deleted, nil
}

func (db *DB) blockStubRepairClaimStatus(orgID, blockID, repairID string) (bool, bool, error) {
	row, found, err := readBlockIdentityForRepairFn(db, orgID, blockID)
	if err != nil || !found {
		return false, found, err
	}
	_, repairing, ownershipErr := classifyBlockClaimOwnership(row.GCState, row.GCClaimID, row.GCClaimedAt)
	if ownershipErr != nil {
		return false, true, ownershipErr
	}
	owned := repairing && row.CreatedAt == nil && !row.StorageClassPresent && strings.TrimSpace(row.GCClaimID) == repairID
	return owned, true, nil
}

func (db *DB) DeleteClaimedBlockStub(orgID, blockID, claimID string) (bool, error) {
	claimID = strings.TrimSpace(claimID)
	if claimID == "" {
		return false, errors.New("missing block delete claim id")
	}
	return deleteClaimedBlockStubFn(db, orgID, blockID, claimID)
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
	var storageClass *string
	err := database.Session().Query(`
		SELECT sha1, size_bytes, storage_class, storage_key, gc_state, gc_claim_id, gc_claimed_at, created_at
		FROM blocks
		WHERE org_id = ? AND block_id = ?
	`, orgID, blockID).Scan(
		&row.Sha1,
		&row.SizeBytes,
		&storageClass,
		&row.StorageKey,
		&row.GCState,
		&row.GCClaimID,
		&row.GCClaimedAt,
		&row.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return blockReuseMetadataRow{}, false, nil
		}
		return blockReuseMetadataRow{}, false, err
	}
	if storageClass != nil {
		row.StorageClass = *storageClass
		row.StorageClassPresent = true
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
	`, orgID, blockID).Consistency(BlockFenceReadConsistency).Scan(&existingBlockID)
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

	activeClaim, repairClaim, ownershipErr := classifyBlockClaimOwnership(metadata.GCState, metadata.GCClaimID, metadata.GCClaimedAt)
	if ownershipErr != nil {
		return BlockReuseProbe{Decision: BlockReuseUnknownError}, fmt.Errorf("block %s has malformed GC ownership: %w", blockID, ownershipErr)
	}
	if metadata.CreatedAt == nil {
		if metadata.StorageClassPresent {
			return BlockReuseProbe{Decision: BlockReuseUnknownError}, fmt.Errorf("block %s has storage class without canonical creation timestamp", blockID)
		}
		if activeClaim {
			return BlockReuseProbe{Decision: BlockReuseBlockedByGC}, nil
		}
		if repairClaim && strings.TrimSpace(metadata.GCClaimID) != blockStubRepairIDFn(orgID, blockID) {
			return BlockReuseProbe{Decision: BlockReuseBlockedByGC}, nil
		}
		hasOrphan, orphanErr := probeBlockReuseHasS3OrphanFn(db, orgID, blockID)
		if orphanErr != nil {
			return BlockReuseProbe{Decision: BlockReuseUnknownError}, fmt.Errorf("read S3 orphan fence for %s: %w", blockID, orphanErr)
		}
		if hasOrphan {
			return BlockReuseProbe{Decision: BlockReuseBlockedByGC}, nil
		}
		return BlockReuseProbe{Decision: BlockReuseRepairableStub}, nil
	}

	probe := BlockReuseProbe{
		Sha1:         strings.TrimSpace(metadata.Sha1),
		SizeBytes:    metadata.SizeBytes,
		StorageClass: metadata.StorageClass,
		StorageKey:   metadata.StorageKey,
	}
	if !metadata.StorageClassPresent || probe.StorageClass == "" {
		return BlockReuseProbe{Decision: BlockReuseUnknownError}, fmt.Errorf("block %s has empty canonical storage class", blockID)
	}
	if probe.StorageKey == "" || strings.TrimSpace(probe.StorageKey) != probe.StorageKey {
		return BlockReuseProbe{Decision: BlockReuseUnknownError}, fmt.Errorf("block %s has empty canonical storage key", blockID)
	}
	// A stored class that is not canonical cannot name a physical namespace, and the
	// probe is what every reuse/repair path resolves through. Reject it here rather
	// than let a normalized copy of it select a backend.
	if !config.IsCanonicalStorageClassName(probe.StorageClass) {
		return BlockReuseProbe{Decision: BlockReuseUnknownError}, fmt.Errorf("block %s has non-canonical storage class %q", blockID, probe.StorageClass)
	}
	if activeClaim || repairClaim {
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

// BlockReferenceWriteConsistency is the consistency level EVERY write to
// block_references must reach, pinned per statement rather than inherited from the
// session.
//
// It is the write half of the destructive-GC liveness argument
// (ISSUE-GC-CROSS-DC-REFERENCE-VISIBILITY-01). BlockHasReferencesGlobal reads at
// EACH_QUORUM so a FALSE answer may authorize destroying bytes, and that answer is
// only trustworthy because a reference acknowledged at LOCAL_QUORUM in some
// datacenter necessarily intersects the read's quorum in that same datacenter. Under
// ONE a single replica can acknowledge a reference that a later per-DC read quorum of
// 2-of-3 never sees — and `ONE` is an accepted `database.consistency`, so inheriting
// the session made that a deployment typo away.
//
// PINNED HERE BECAUSE THE READER CANNOT ENFORCE IT. ValidateDestructiveGCTopology
// checks the consistency of the process running GC; references are written by API
// nodes, which are different processes with their own configuration, and no gate the
// worker runs can see them. The invariant is a property of the writers, so it belongs
// at the writers.
//
// THE FLOOR IS ALSO THE CEILING, which is a real trade and not a detail. A deployment
// configured for EACH_QUORUM or ALL now writes references at LOCAL_QUORUM — this pin
// LOWERS a stronger configured level, and it is worth being explicit that it does. The
// reason is that a pin varying with configuration gives back the exact property the
// constant exists to establish: "references reached a quorum" would once again mean
// "whatever that process was configured with", which is unverifiable from the reader's
// side and is how ONE got in. What is given up is only cross-DC promptness, never
// safety: the destructive read is EACH_QUORUM regardless, so it still intersects; every
// other reader of block_references is a local check whose false zero costs a redundant
// re-upload or an enqueued candidate that the global verify then declines to delete.
// Every shipped profile already runs LOCAL_QUORUM, so no deployment changes behaviour.
//
// TestBlockReferenceProducersPinWriteConsistency enumerates the statements this
// applies to; a new producer fails that test until it is pinned or exempted with a
// reason.
const BlockReferenceWriteConsistency = gocql.LocalQuorum

// AddBlockReference registers a reference to a block. Idempotent: re-adding the
// same (block, referrer) overwrites a row with identical key. ttlSeconds > 0 makes
// the row expire (for example publish-attempt references); 0 means permanent.
func (db *DB) AddBlockReference(orgID, blockID, referrer, libraryID string, ttlSeconds int) error {
	now := time.Now().UTC()
	if ttlSeconds > 0 {
		return db.Session().Query(`
			INSERT INTO block_references (org_id, block_id, referrer, library_id, created_at)
			VALUES (?, ?, ?, ?, ?) USING TTL ?
		`, orgID, blockID, referrer, libraryID, now, ttlSeconds).Consistency(BlockReferenceWriteConsistency).Exec()
	}
	return db.Session().Query(`
		INSERT INTO block_references (org_id, block_id, referrer, library_id, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, orgID, blockID, referrer, libraryID, now).Consistency(BlockReferenceWriteConsistency).Exec()
}

// RemoveBlockReference deletes a single (block, referrer) reference. Idempotent:
// deleting a non-existent row is a no-op, so a retried GC pass or publish-attempt
// cleanup is always safe. Upload up: references are not removed through this API.
//
// DELIBERATELY NOT PINNED to BlockReferenceWriteConsistency, and the asymmetry is the
// point — but NOT for the reason an earlier version of this comment gave. It claimed
// an under-replicated DELETE "leaves the row visible, so GC declines to collect and
// the bytes survive one more pass", i.e. that a weak delete biases toward keeping
// data. That is not a property of Cassandra. A DELETE writes a timestamped tombstone,
// the mutation is sent to every replica regardless of consistency level, and
// reconciliation is last-write-wins — so a quorum read that touches the tombstone
// resolves to "absent" and repairs the others with it. A delete acknowledged by one
// replica can absolutely make the row invisible to a later per-DC read quorum. There
// is no structural bias toward keeping data here to lean on.
//
// What actually makes the exemption safe is the PROTOCOL, not the consistency level.
// The X2 premise concerns CREATING a live reference: that write must reach a quorum,
// because the destructive read's zero is only trustworthy if it intersects whatever
// acknowledged it. Removing a reference creates no such obligation — its safety rests
// on this call only ever being made once the referrer has lost authority over the
// block. Both callers satisfy that by construction: the publish-attempt cleanup
// retires a TTL'd provisional reference, and the GC cascade removes an fs_object's
// reference as that fs_object is being deleted. The window between publishing a new
// reference and removing an old one is the publication fence, which belongs to X1
// (ISSUE-GC-UPLOAD-FENCE-REMATERIALIZATION-01), not to the consistency of this
// statement.
//
// Pinning the delete to LOCAL_QUORUM as well would be harmless — it is already the
// effective level in every shipped profile — and would remove the need to explain the
// asymmetry at all. It is left inheriting the session because the pin is a safety
// mechanism with a specific justification, and applying it where that justification
// does not hold would blur what it means everywhere else.
func (db *DB) RemoveBlockReference(orgID, blockID, referrer string) error {
	return db.Session().Query(`
		DELETE FROM block_references WHERE org_id = ? AND block_id = ? AND referrer = ?
	`, orgID, blockID, referrer).Exec()
}

// BlockHasReferences reports whether any reference row still exists for the block.
// This single-partition point read replaces reading the mutable blocks.ref_count.
//
// It runs at the session consistency (LOCAL_QUORUM in every shipped profile), so a
// TRUE answer is proof — a row visible locally is a real reference — while a FALSE
// answer proves only that the local DC has not seen one. That asymmetry is why this
// call is safe for discovery and short-circuit aborts but MUST NOT authorize a
// physical delete. Use BlockHasReferencesGlobal for that
// (ISSUE-GC-CROSS-DC-REFERENCE-VISIBILITY-01).
func (db *DB) BlockHasReferences(orgID, blockID string) (bool, error) {
	return scanBlockHasReferences(db.Session().Query(`
		SELECT referrer FROM block_references WHERE org_id = ? AND block_id = ? LIMIT 1
	`, orgID, blockID))
}

// BlockHasReferencesGlobal is the destructive-authorization liveness read: the only
// form whose FALSE answer may authorize deleting physical bytes.
//
// It pins EACH_QUORUM per query rather than inheriting the session default, so the
// read must obtain a quorum in EVERY datacenter. A reference write acknowledged at
// LOCAL_QUORUM in any DC therefore intersects this read's quorum in that same DC,
// and GC cannot conclude "zero references" while one exists somewhere else. If any
// DC is unreachable the read fails and the caller must fail closed — deleting on an
// uncertain read is exactly the defect this closes.
//
// The per-DC argument presumes NetworkTopologyStrategy with every replica-holding DC
// in the keyspace map; under SimpleStrategy EACH_QUORUM does not carry it. The
// destructive path gates on that separately.
func (db *DB) BlockHasReferencesGlobal(orgID, blockID string) (bool, error) {
	return scanBlockHasReferences(db.Session().Query(`
		SELECT referrer FROM block_references WHERE org_id = ? AND block_id = ? LIMIT 1
	`, orgID, blockID).Consistency(gocql.EachQuorum))
}

// scanBlockHasReferences turns the shared LIMIT 1 probe into a boolean. Absence is
// reported as "no references"; every other error propagates, so an unreachable DC
// under EACH_QUORUM surfaces as an error rather than as a false zero.
func scanBlockHasReferences(query *gocql.Query) (bool, error) {
	var referrer string
	if err := query.Scan(&referrer); err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// BlockReferenceExists reports whether one specific (block, referrer) reference
// row is still present. GC Phase 0 uses it to tell "this provisional reference
// has been retired by its Cassandra TTL" from "the row is still there", which is
// what lets the scanner wait for the TTL instead of deleting a reference an
// upload may have just renewed (F9).
func (db *DB) BlockReferenceExists(orgID, blockID, referrer string) (bool, error) {
	var existing string
	err := db.Session().Query(`
		SELECT referrer FROM block_references WHERE org_id = ? AND block_id = ? AND referrer = ?
	`, orgID, blockID, referrer).Scan(&existing)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

type BlockS3OrphanInfo struct {
	StorageClass string
	FirstSeenAt  time.Time
}

// BlockDeleteFenceActive reports whether GC still owns the physical object for
// this block. Writers must treat both an in-row gc_state='deleting' claim and a
// pending gc_s3_orphans row as an active fence; otherwise a re-upload can race
// with orphan recovery and lose the object after the canonical block row was
// already deleted.
// The canonical row is read FIRST and the orphan LAST, and that order is the
// whole correctness argument. GC writes gc_state, then the orphan, then removes
// the row. Reading the orphan first admits this sequence:
//
//	writer reads orphan   -> absent
//	GC     StartBlockDeleteOrphan  -> orphan(P1) now exists
//	GC     FinalizeBlockDelete     -> blocks(L) removed
//	writer reads blocks   -> absent, reported as "no fence"
//	writer installs P2    -> blocks(L) -> P2 while orphan(P1) is live
//
// which is precisely the overlapped state conservative A+ forbids (R13). Reading
// the row first inverts the dependency: an absent row proves the orphan of that
// lifecycle was already durable, so the orphan read that follows observes it.
// Both reads are ordinary. The fence publishers commit at EACH_QUORUM, so an
// ordinary read already sees every committed fence, and the authority that
// actually admits a write is downstream and structural -- the single-use INSTALL
// LWT for a fresh incarnation, the tuple-bound non-creating CAS for a repair.
func (db *DB) BlockDeleteFenceActive(orgID, blockID string) (bool, error) {
	gcState, found, err := blockDeleteFenceGCStateFn(db, orgID, blockID)
	if err != nil {
		return false, err
	}
	if found && gcState == BlockGCStateDeleting {
		return true, nil
	}
	// A rowless read is deliberately NOT an early "no fence" return: it is the
	// exact observation the orphan read below exists to disambiguate.
	return blockDeleteFenceHasS3OrphanFn(db, orgID, blockID)
}

var blockDeleteFenceGCStateFn = func(database *DB, orgID, blockID string) (string, bool, error) {
	var gcState string
	err := database.Session().Query(`
		SELECT gc_state FROM blocks WHERE org_id = ? AND block_id = ?
	`, orgID, blockID).Consistency(BlockFenceReadConsistency).Scan(&gcState)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return "", false, nil
		}
		return "", false, err
	}
	return gcState, true, nil
}

var blockDeleteFenceHasS3OrphanFn = func(database *DB, orgID, blockID string) (bool, error) {
	var existingBlockID string
	err := database.Session().Query(`
		SELECT block_id FROM gc_s3_orphans WHERE org_id = ? AND block_id = ? LIMIT 1
	`, orgID, blockID).Consistency(BlockFenceReadConsistency).Scan(&existingBlockID)
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
	`, orgID, blockID).Consistency(BlockFenceReadConsistency).Scan(&info.StorageClass, &info.FirstSeenAt)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return BlockS3OrphanInfo{}, false, nil
		}
		return BlockS3OrphanInfo{}, false, err
	}
	return info, true, nil
}
