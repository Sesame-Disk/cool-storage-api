package gc

import (
	"context"
	"time"

	"github.com/google/uuid"
)

const gcFailedItemRetentionSeconds = 30 * 24 * 60 * 60

var gcFailedItemRetention = time.Duration(gcFailedItemRetentionSeconds) * time.Second

const (
	GCFailureCodeNone                        = ""
	GCFailureCodeLibraryHardDeleteInProgress = "library_hard_delete_in_progress"
	// GCFailureCodeBlockClaimNotYetStale marks a candidate that cannot be settled
	// yet because a delete claim on its block is too young to hand back safely. The
	// item is postponed, not retried and not failed.
	GCFailureCodeBlockClaimNotYetStale = "block_claim_not_yet_stale"
	// GCFailureCodeDestructiveFailClosed marks a delete refused because the
	// environment could not authorize it — an unreachable datacenter, or a
	// replication map that no longer carries the per-DC EACH_QUORUM argument. These
	// are postponed rather than retried, so the code exists mainly to make the
	// refusal legible; it should not normally reach the DLQ.
	GCFailureCodeDestructiveFailClosed = "destructive_fail_closed"
	// GCFailureCodeBlockClaimReleaseUnconfirmed marks a candidate whose block still
	// carries a stale delete claim that this pass tried and failed to hand back.
	//
	// It postpones for ANY failure reason, not only an availability one, and that
	// breadth is the whole point. This queue item is the only work that will ever
	// lift that fence: block items do not auto-recover from the DLQ, and the
	// scanner's day cursor has already moved past the candidate, so spending the
	// retry budget here strands a LIVE block behind gc_state='deleting' forever and
	// BlockDeleteFenceActive then refuses every future upload of that content. An
	// unknown column or a CQL bug in the release statement is exactly as fatal to
	// that fence as an unreachable datacenter is.
	//
	// The cost of that breadth is a permanently failing release postponing forever
	// instead of surfacing in the DLQ, which is the same trade documented on
	// isClusterUnavailableError's timeout codes. It is paid deliberately, and the
	// visibility it gives up is bought back by a dedicated
	// gc_errors_total{type="stale_claim_release_failed"} counter rather than left
	// silent. That counter is deliberately NOT seeded at registration: unlike the
	// destructive blocked/liveness gauge pair — where an absent series silently drops
	// out of a comparison — a counter that has never fired is simply absent, and
	// `increase(...) > 0` reads absence as "did not happen", which is true.
	GCFailureCodeBlockClaimReleaseUnconfirmed = "block_claim_release_unconfirmed"
)

// GCStore abstracts all database operations used by the GC system.
// This allows unit tests to use an in-memory mock instead of Cassandra.
type GCStore interface {
	// Queue operations
	// EnqueueItem is the low-level path for queue rows that do not need
	// block_representation_id context. Callers enqueuing commits, fs_objects,
	// or library cascades must use EnqueueBatch with QueueItem.
	EnqueueItem(orgID uuid.UUID, queuedAt time.Time, itemType ItemType, itemID string, libraryID uuid.UUID, storageClass string, retryCount int) error
	EnqueueBatch(items []QueueItem) error
	QueueItemExists(orgID uuid.UUID, queuedAt time.Time, itemType ItemType, itemID string) (bool, error)
	PendingItemExists(orgID, libraryID uuid.UUID, identityAt time.Time, itemType ItemType, itemID string) (bool, error)
	DequeueBatch(orgID uuid.UUID, batchSize int, cutoff time.Time) ([]QueueItem, error)
	CompleteItem(orgID uuid.UUID, queuedAt time.Time, itemType ItemType, itemID string) error
	RequeueItem(orgID uuid.UUID, oldQueuedAt, newQueuedAt time.Time, itemType ItemType, itemID string, libraryID uuid.UUID, blockRepresentationID, storageClass string, newRetryCount int, identityAt time.Time, requiresLibraryDeletedCheck bool, libraryGuardMode LibraryGuardMode) error
	FailItem(item QueueItem, failedAt time.Time, lastError, failureCode string) error
	GetQueueSize(orgID uuid.UUID) (int, error)
	GetTotalQueueSize() (int, error)
	GetTotalFailedItems() (int, error)
	ListOrgsWithQueuedItems() ([]uuid.UUID, error)
	ListOrgsWithQueuedSnapshots(limit int) ([]uuid.UUID, error)
	ListOrgsWithFailedItems(limit int) ([]GCFailedItemOrgInfo, error)
	ListFailedItems(orgID uuid.UUID, limit int) ([]GCFailedItemInfo, error)
	ListFailedItemExpiriesByDay(day time.Time, bucket int) ([]GCFailedItemExpiryInfo, error)
	DeleteFailedItem(orgID uuid.UUID, failedAt time.Time, itemType ItemType, itemID string) error
	DeleteExpiredFailedItem(expiry GCFailedItemExpiryInfo, now time.Time) (bool, error)
	RequeueFailedItem(orgID uuid.UUID, failedAt time.Time, itemType ItemType, itemID string, queuedAt time.Time) error
	MarkOrgActive(orgID uuid.UUID, activeAt time.Time) error
	RemoveOrgFromActiveSet(orgID uuid.UUID, activeBefore time.Time) error
	MarkOrgDirty(orgID uuid.UUID, dirtyAt time.Time) error
	ListDirtyOrgs(limit int) ([]GCDirtyOrg, error)
	ClearDirtyOrg(orgID uuid.UUID, dirtyBefore time.Time) error
	GetOrgQueueStats(orgID uuid.UUID) (GCOrgStats, error)
	SaveOrgQueueStats(stats GCOrgStats) error
	RecalculateOrgQueueStats(orgID uuid.UUID) (GCOrgStats, error)
	GetOldestQueuedAt(orgID uuid.UUID) (*time.Time, error)
	SumOrgQueueStats() (int, int, error)
	GetUserDeletedAt(orgID, userID uuid.UUID) (*time.Time, error)
	GetLibraryDeletedAt(libraryID uuid.UUID) (*time.Time, error)
	GetOrgDeletedAt(orgID uuid.UUID) (*time.Time, error)
	GetLibraryBlockRepresentationID(orgID, libraryID uuid.UUID) (string, error)

	// Block operations (worker)
	//
	// BlockExists reports whether the canonical `blocks` row still exists.
	// RecoverS3Orphans relies on this to distinguish a block still being
	// claimed/finalized by GC (row present → skip) from one whose DB row was
	// already removed (absent → proceed with S3 cleanup).
	BlockExists(orgID uuid.UUID, blockID string) (bool, error)
	// BlockHasReferences reports whether any block_references row still exists for
	// the block, at the session consistency. TRUE is proof and may abort a delete;
	// FALSE proves only local absence, so it may drive discovery but MUST NOT
	// authorize destroying bytes. Use BlockHasReferencesGlobal for that.
	BlockHasReferences(orgID uuid.UUID, blockID string) (bool, error)
	// BlockHasReferencesGlobal is the same liveness check pinned to EACH_QUORUM, so
	// it intersects every DC that can acknowledge a LOCAL_QUORUM reference write.
	// Its FALSE answer is the ONLY one that may authorize a physical delete
	// (ISSUE-GC-CROSS-DC-REFERENCE-VISIBILITY-01). An unreachable DC makes it fail;
	// callers must fail closed rather than treat the error as "no references".
	BlockHasReferencesGlobal(orgID uuid.UUID, blockID string) (bool, error)
	// ValidateDestructiveGCTopology reports whether the live keyspace replication
	// still supports the per-datacenter EACH_QUORUM argument that authorizes
	// physical deletes. It is part of this interface rather than an optional
	// capability so the guarantee cannot be lost by wrapping the store: dropping it
	// is a compile error, not a silently disarmed safety gate.
	ValidateDestructiveGCTopology() error
	GetBlockInfo(orgID uuid.UUID, blockID string) (BlockInfo, error)
	// RemoveBlockReference deletes one (block, referrer) reference row. Idempotent.
	RemoveBlockReference(orgID uuid.UUID, blockID, referrer string) error
	ResolveBlockIDs(orgID, libraryID uuid.UUID, blockRepresentationID string, blockIDs []string) ([]string, error)
	// ClaimBlockDelete atomically marks the block row gc_state='deleting' via LWT
	// and records the deterministic claimID for the logical delete attempt.
	// Callers MUST re-check BlockHasReferencesGlobal — the EACH_QUORUM form, never
	// the session-consistency one — after a successful claim before deleting from S3
	// (claim-then-verify). Verifying with the local read reopens
	// ISSUE-GC-CROSS-DC-REFERENCE-VISIBILITY-01.
	ClaimBlockDelete(orgID uuid.UUID, blockID, claimID string) (bool, error)
	// ReleaseBlockClaim clears gc_state only when the same claimID still owns the
	// row. This prevents another attempt from releasing a claim it did not win.
	// It returns an error when the conditional update does not apply, so callers
	// that must observe the release use it rather than ReleaseStaleBlockClaim.
	ReleaseBlockClaim(orgID uuid.UUID, blockID, claimID string) error
	// ReleaseStaleBlockClaim clears gc_state on a block whose delete claim was taken
	// at or before staleBefore, WHICHEVER attempt owns it. Age is the only criterion,
	// and that is the point: an unconditional release would let one worker drop the
	// fence out from under another worker's in-flight delete, while an owner-only
	// release cannot lift a claim whose owner will never come back.
	//
	// The owner-only form was the earlier design and it leaks. claimID derives from
	// the candidate timestamp, so a claim left behind by candidate C1 carries C1's id;
	// a later candidate C2 finds an id that is not its own, concludes "someone else's
	// pass will lift it", and settles. If C1's queue item is gone — DLQ'd, and block
	// items never auto-recover from there — nothing ever lifts it, and
	// BlockDeleteFenceActive refuses every future upload of that content forever.
	// Releasing by age closes that: the only claim this can touch is one older than
	// any possible live attempt.
	//
	// The outcome is three-valued on purpose. "Nothing to release" and "there IS a
	// claim, but it is too young to touch" demand opposite things from the caller:
	// the first means the item is settled, the second means it emphatically is not,
	// because that fence still has to come off later and this candidate is what will
	// do it. Collapsing them into a single false is how a live block ends up fenced
	// forever — see BlockClaimTooFresh.
	ReleaseStaleBlockClaim(orgID uuid.UUID, blockID string, staleBefore time.Time) (BlockClaimReleaseOutcome, error)
	// DeleteClaimedBlockStub removes only a metadata-free stub owned by claimID.
	// applied=false means the row changed and callers must retry rather than
	// treating the stale observation as success.
	DeleteClaimedBlockStub(orgID uuid.UUID, blockID, claimID string) (bool, error)
	// FinalizeBlockDelete removes the block row only when the same claimID still
	// owns the row.
	FinalizeBlockDelete(orgID uuid.UUID, blockID, claimID string) error
	EnsureBlockGCCandidate(orgID uuid.UUID, blockID, storageClass string, candidateAt time.Time) (time.Time, error)
	DeleteBlockGCCandidate(orgID uuid.UUID, blockID string, candidateAt time.Time) error
	// ListBlockGCCandidatesByDay enumerates candidates whose `candidate_at`
	// falls on the given UTC day for one discovery bucket. Bucket indices
	// range over [0, db.GCDiscoveryBucketCount). Replaces the old per-org
	// partition scan that depended on `blocks` partitioning by org.
	ListBlockGCCandidatesByDay(day time.Time, bucket int) ([]BlockGCCandidateInfo, error)
	// ListProvisionalBlockRefExpiriesByDay enumerates provisional upload-ref
	// expiry records whose `expires_at` falls on the given UTC day for one
	// discovery bucket. Each row is keyed by the specific provisional referrer,
	// so concurrent uploads of the same block are expired independently.
	ListProvisionalBlockRefExpiriesByDay(day time.Time, bucket int) ([]ProvisionalBlockRefExpiryInfo, error)
	// GetProvisionalBlockRefExpiry loads the canonical expiry row. The scanner
	// revalidates this before acting because the durable by-day table is only a
	// discovery projection and can be stale.
	GetProvisionalBlockRefExpiry(orgID uuid.UUID, blockID, referrer string) (ProvisionalBlockRefExpiryInfo, bool, error)
	// DeleteProvisionalBlockRefExpiryProjection removes only the discovery row.
	// It intentionally leaves the canonical row untouched for stale projection
	// cleanup when an upload ref was renewed or already finalized elsewhere.
	DeleteProvisionalBlockRefExpiryProjection(orgID uuid.UUID, blockID, referrer string, expiresAt time.Time) error
	// BlockReferenceExists reports whether one specific reference row survives.
	// Phase 0 uses it to confirm a provisional reference has actually been retired
	// by its Cassandra TTL before drawing any conclusion about block liveness.
	BlockReferenceExists(orgID uuid.UUID, blockID, referrer string) (bool, error)

	// S3 orphan recovery / pending delete tracking for blocks claimed by GC.
	// StartBlockDeleteOrphan records the durable recovery row for a NEW block
	// deletion. It always resets recovery state to pending_s3 so a stale
	// pending_mapping_cleanup row from an older delete cannot make recovery skip
	// the physical object delete for this new lifecycle.
	StartBlockDeleteOrphan(orgID uuid.UUID, blockID, storageClass, storageKey, externalSHA1 string, now time.Time) (time.Time, error)
	// GetS3OrphanGlobal reads the canonical orphan row at EACH_QUORUM for the
	// destructive recovery path. It supplies recovery state and the physical
	// backend selector; it is not a Paxos settlement read and does not authorize
	// deletion by itself. R22a keeps an absent or failed read fail-closed.
	GetS3OrphanGlobal(orgID uuid.UUID, blockID string) (S3OrphanInfo, bool, error)
	// ListS3OrphansByDay enumerates S3-orphan rows whose `first_seen_at`
	// falls on the given UTC day for one discovery bucket. It returns only the
	// discovery identity; callers must reload the canonical orphan before acting.
	// `limit` caps the number of rows returned for a single (day, bucket) pair.
	ListS3OrphansByDay(day time.Time, bucket int, limit int) ([]S3OrphanDiscoveryInfo, error)
	// MarkS3OrphanMappingCleanupPending advances the recovery row after the S3
	// delete has completed so restart recovery can finish the orphan lifecycle
	// without touching S3 again. The phase name is historical: after R11a this
	// transition performs no block-id mapping cleanup.
	MarkS3OrphanMappingCleanupPending(orgID uuid.UUID, blockID, externalSHA1 string, now time.Time) error
	UpdateS3OrphanAttempt(orgID uuid.UUID, blockID string, expectedFirstSeenAt time.Time, errMsg string, now time.Time) error
	DeleteS3Orphan(orgID uuid.UUID, blockID string, firstSeenAt time.Time) error

	// Commit operations (worker)
	GetCommit(libraryID uuid.UUID, commitID string) (CommitInfo, error)
	DeleteCommit(libraryID uuid.UUID, commitID string) error

	// FS object operations (worker)
	GetFSObject(libraryID uuid.UUID, fsID string) (FSObjectInfo, error)
	DeleteFSObject(libraryID uuid.UUID, fsID string) error

	// Library operations (worker + scanner)
	GetLibraryStorageClass(orgID, libraryID uuid.UUID) (string, error)
	ListCommitsForLibrary(libraryID uuid.UUID) ([]CommitInfo, error)
	ListFSObjectsForLibrary(libraryID uuid.UUID) ([]FSObjectInfo, error)

	// Scanner operations
	ListOrganizations() ([]uuid.UUID, error)
	ListExpiredShareLinks() ([]ExpiredShareLinkInfo, error)
	ListDistinctCommitLibraries() ([]uuid.UUID, error)
	ListDistinctFSObjectLibraries() ([]uuid.UUID, error)
	LibraryExists(libraryID uuid.UUID) (bool, error)
	// CanonicalLibraryExists reports whether the authoritative `libraries` row exists
	// for (orgID, libraryID). Unlike LibraryExists (which reads the libraries_by_id
	// projection), this consults the canonical table so orphan cleanup cannot act on a
	// library that is still live under projection drift. Fails closed on read errors.
	CanonicalLibraryExists(orgID, libraryID uuid.UUID) (bool, error)
	FindOrgForLibrary(libraryID uuid.UUID) (uuid.UUID, error)
	ListCommitIDsForLibrary(libraryID uuid.UUID) ([]string, error)
	ListFSObjectIDsForLibrary(libraryID uuid.UUID) ([]string, error)
	ReconcilePendingStorageCounters() (int, error)

	// Version TTL enforcement
	ListLibrariesWithVersionTTL() ([]LibraryTTLInfo, error)
	ListCommitsWithTimestamps(libraryID uuid.UUID) ([]CommitWithTimestamp, error)

	// Auto-delete enforcement
	ListLibrariesWithAutoDelete() ([]LibraryAutoDeleteInfo, error)

	// Share link deletion (defensive: attempts index cleanup even if primary record is gone)
	DeleteShareLink(shareToken string, orgID uuid.UUID, libraryID uuid.UUID) error
	DeleteExpiredShareLink(link ExpiredShareLinkInfo) error

	// Expired shares (user-to-user library shares)
	ListExpiredShares() ([]ExpiredShareInfo, error)
	DeleteShare(libraryID, shareID uuid.UUID) error
	DeleteExpiredShare(share ExpiredShareInfo) error

	// Expired restore jobs
	ListExpiredRestoreJobs() ([]ExpiredRestoreJobInfo, error)
	DeleteRestoreJob(orgID, libraryID, jobID uuid.UUID) error

	// Orphaned library artifacts cleanup
	ListSharesByLibrary(libraryID uuid.UUID) ([]ShareInfo, error)
	ListRepoTagsByLibrary(libraryID uuid.UUID) ([]string, error)
	DeleteRepoTag(libraryID uuid.UUID, tagID int) error
	ListFileTagsByLibrary(libraryID uuid.UUID) ([]FileTagInfo, error)
	DeleteFileTag(libraryID uuid.UUID, filePath string, tagID int) error
	DeleteFileTagByID(libraryID uuid.UUID, fileTagID int) error
	ListRepoAPITokensByLibrary(libraryID uuid.UUID) ([]RepoAPITokenInfo, error)
	DeleteRepoAPIToken(libraryID uuid.UUID, appName string) error
	DeleteRepoAPITokenByToken(apiToken string) error
	DeleteLockedFilesByLibrary(libraryID uuid.UUID) error
	DeleteShareLinksByLibrary(orgID, libraryID uuid.UUID) ([]string, error)

	// Starred files and monitored repos cleanup
	DeleteStarredFilesByLibrary(libraryID uuid.UUID) error
	DeleteMonitoredReposByLibrary(libraryID uuid.UUID) error

	// Restore jobs cleanup by library
	DeleteRestoreJobsByLibrary(orgID, libraryID uuid.UUID) error

	// Tag counter cleanup
	DeleteRepoTagCounters(libraryID uuid.UUID) error
	DeleteFileTagCounters(libraryID uuid.UUID) error
	DeleteRepoTagFileCounts(libraryID uuid.UUID) error

	// Group shares cleanup (shares where shared_to is a group)
	ListSharesByGroup(groupID uuid.UUID) ([]GroupShareInfo, error)

	// Scanner: orphaned group shares
	ScanAllGroupShares(ctx context.Context, visit func(GroupShareInfo) error) error
	GroupExists(orgID, groupID uuid.UUID) (bool, error)

	// Audit log
	WriteAuditLog(entry AuditLogEntry) error

	// User cascade (soft-delete → hard-delete after grace period)
	ListDeletedUsersExpired(graceDays int) ([]DeletedUserInfo, error)
	ListLibrariesByOwner(orgID, ownerID uuid.UUID) ([]uuid.UUID, error)
	SoftDeleteLibrary(orgID, libraryID, deletedBy uuid.UUID) error
	ListGroupMembershipsByUser(orgID, userID uuid.UUID) ([]uuid.UUID, error)
	DeleteGroupMember(groupID, userID uuid.UUID) error
	DeleteGroupByMember(orgID, userID, groupID uuid.UUID) error
	ListSharesByUser(orgID, userID uuid.UUID) ([]ShareByUserInfo, error)
	ListSharesCreatedByUser(orgID, userID uuid.UUID) ([]ShareByCreatorInfo, error)
	DeleteStarredFilesByUser(userID uuid.UUID) error
	DeleteMonitoredReposByUser(userID uuid.UUID) error
	DeleteAPIKeysByUser(orgID, userID uuid.UUID) error
	HardDeleteUser(orgID, userID uuid.UUID, email string) error
	GetUserEmail(orgID, userID uuid.UUID) (string, error)
	// AcquireUserHardDeleteLock acquires a renewable lease for a user cascade
	// delete. Returns (true, nil) when the lock is successfully acquired.
	// activateUser checks this table to block concurrent restores.
	AcquireUserHardDeleteLock(userID, leaseToken uuid.UUID) (bool, error)
	RenewUserHardDeleteLock(userID, leaseToken uuid.UUID) (bool, error)
	ReleaseUserHardDeleteLock(userID, leaseToken uuid.UUID) error

	// AcquireLibraryHardDeleteLock acquires a renewable lease for a library
	// cascade delete. Returns (true, nil) when the lock is successfully acquired.
	// restoreDeletedLibrary checks this table to block concurrent restores.
	AcquireLibraryHardDeleteLock(libraryID, leaseToken uuid.UUID) (bool, error)
	RenewLibraryHardDeleteLock(libraryID, leaseToken uuid.UUID) (bool, error)
	ReleaseLibraryHardDeleteLock(libraryID, leaseToken uuid.UUID) error

	// Library trash auto-purge (soft-deleted libraries past retention period)
	ListExpiredDeletedLibraries(retentionDays int) ([]DeletedLibraryInfo, error)
	HardDeleteLibrary(orgID, libraryID uuid.UUID) error

	// Storage counter cleanup after permanent library deletion.
	// Deletes the lib-scope counter row. Aggregate scopes (org, user, platform)
	// must have been adjusted earlier via SoftDeleteLibrary.
	DeleteLibraryStorageCounter(orgID, libraryID uuid.UUID) error

	// Org cascade (soft-deleted orgs past grace period)
	ListExpiredDeletedOrgs(graceDays int) ([]DeletedOrgInfo, error)
	ListUsersByOrg(orgID uuid.UUID) ([]OrgUserInfo, error)
	ListGroupsByOrg(orgID uuid.UUID) ([]uuid.UUID, error)
	ListLibrariesForOrg(orgID uuid.UUID) ([]OrgLibraryInfo, error)
	DeleteGroupFull(orgID, groupID uuid.UUID) error
	// BeginOrgPurge atomically transitions a soft-deleted org into an internal
	// purge-in-progress state for the given deleted_at identity. It returns
	// false when the org was restored or deleted again under a different
	// identity before the transition could be claimed.
	BeginOrgPurge(orgID uuid.UUID, identityAt time.Time) (bool, error)
	// AcquireOrgHardDeleteLock acquires a renewable lease for an org cascade
	// delete. Returns (true, nil) when the lock is successfully acquired.
	// Restore/reactivation is blocked by the purging lifecycle state claimed
	// with BeginOrgPurge; this lock only serializes concurrent hard-delete work.
	AcquireOrgHardDeleteLock(orgID, leaseToken uuid.UUID) (bool, error)
	RenewOrgHardDeleteLock(orgID, leaseToken uuid.UUID) (bool, error)
	ReleaseOrgHardDeleteLock(orgID, leaseToken uuid.UUID) error
	// HardDeleteOrgLocked deletes the org record after child rows have been
	// removed. Caller must already hold the org hard-delete lock.
	HardDeleteOrgLocked(orgID uuid.UUID) error
	HardDeleteOrg(orgID uuid.UUID) error
	GetOrgName(orgID uuid.UUID) (string, error)

	// GC stats persistence
	SaveGCStats(key, value string) error
	LoadGCStats(key string) (string, error)
}

// ItemType constants for new GC item types
const (
	ItemShare      ItemType = "share"
	ItemRestoreJob ItemType = "restore_job"
)

// FSObjectInfo holds data about an fs_object needed by the worker.
type FSObjectInfo struct {
	FSID       string
	ObjType    string
	BlockIDs   []string
	DirEntries []string // child fs_ids for dir objects; nil for files
}

// CommitInfo holds data about a commit needed by the worker.
type CommitInfo struct {
	CommitID string
	RootFSID string
}

// BlockInfo holds data about a block needed by the scanner.
type BlockInfo struct {
	BlockID      string
	StorageClass string
	StorageKey   string
	CreatedAt    *time.Time
	// Sha1 is the block's external Seafile SHA-1 (blocks.sha1), retained for
	// orphan recovery metadata and legacy diagnostics. Empty for legacy/pre-PR2
	// rows.
	Sha1 string
}

type BlockGCCandidateInfo struct {
	OrgID        uuid.UUID
	BlockID      string
	StorageClass string
	CandidateAt  time.Time
}

type ProvisionalBlockRefExpiryInfo struct {
	OrgID        uuid.UUID
	BlockID      string
	Referrer     string
	StorageClass string
	ExpiresAt    time.Time
}

// GCOrgStats stores reconciled queue state for a single org.
type GCOrgStats struct {
	OrgID          uuid.UUID
	QueueDepth     int
	FailedDepth    int
	OldestQueuedAt *time.Time
	UpdatedAt      time.Time
	RecalculatedAt time.Time
}

// GCFailedItemOrgInfo summarizes one organization with items in the GC DLQ.
type GCFailedItemOrgInfo struct {
	OrgID            uuid.UUID `json:"org_id"`
	OrgName          string    `json:"org_name"`
	FailedItemsTotal int       `json:"failed_items_total"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// GCFailedItemInfo represents an item moved to the GC dead-letter queue.
type GCFailedItemInfo struct {
	OrgID                       uuid.UUID        `json:"org_id"`
	FailedAt                    time.Time        `json:"failed_at"`
	ExpiresAt                   time.Time        `json:"expires_at"`
	QueuedAt                    time.Time        `json:"queued_at"`
	IdentityAt                  time.Time        `json:"identity_at"`
	RequiresLibraryDeletedCheck bool             `json:"requires_library_deleted_check"`
	LibraryGuardMode            LibraryGuardMode `json:"library_guard_mode"`
	ItemType                    ItemType         `json:"item_type"`
	ItemID                      string           `json:"item_id"`
	LibraryID                   uuid.UUID        `json:"library_id"`
	BlockRepresentationID       string           `json:"block_representation_id"`
	StorageClass                string           `json:"storage_class"`
	RetryCount                  int              `json:"retry_count"`
	LastError                   string           `json:"last_error"`
	FailureCode                 string           `json:"failure_code"`
	ResolvedAt                  *time.Time       `json:"resolved_at"`
	ResolvedState               string           `json:"resolved_state"`
}

// GCFailedItemExpiryInfo is the lightweight discovery row used by the scanner
// to expire DLQ rows through the store, preserving failed-depth counters.
type GCFailedItemExpiryInfo struct {
	OrgID     uuid.UUID
	FailedAt  time.Time
	ExpiresAt time.Time
	ItemType  ItemType
	ItemID    string
}

// GCDirtyOrg identifies an org whose queue snapshot needs reconciliation.
type GCDirtyOrg struct {
	OrgID    uuid.UUID
	MarkedAt time.Time
}

// S3OrphanDiscoveryInfo is the non-authoritative identity emitted by the
// gc_s3_orphans_by_day discovery projection. Its first_seen_at value is only a
// correlation token; it is not a complete lifecycle identity.
type S3OrphanDiscoveryInfo struct {
	OrgID       uuid.UUID
	BlockID     string
	FirstSeenAt time.Time
}

// S3OrphanInfo holds canonical data about a block whose S3 deletion still needs
// recovery.
// Rows are created as soon as GC claims the block for deletion, before the DB
// row is physically removed, so a crash between DB and S3 phases remains
// recoverable after restart.
type S3OrphanInfo struct {
	OrgID         uuid.UUID
	BlockID       string
	StorageClass  string
	StorageKey    string
	ExternalSHA1  string
	RecoveryPhase string
	FirstSeenAt   time.Time
	LastAttemptAt time.Time
	RetryCount    int
	LastError     string
}

const (
	S3OrphanPhasePendingS3 = "pending_s3"
	// Historical name. After R11a this phase means the physical S3 delete has
	// completed and only orphan finalization remains; it performs no mapping delete.
	S3OrphanPhasePendingMappingCleanup = "pending_mapping_cleanup"
)

// ShareLinkInfo holds data about a share link needed by the scanner.
type ShareLinkInfo struct {
	ShareToken string
	OrgID      uuid.UUID
	ExpiresAt  time.Time
}

// ExpiredShareLinkInfo holds the cleanup context from gc_share_links_by_expiry.
type ExpiredShareLinkInfo struct {
	ShareToken string
	OrgID      uuid.UUID
	LibraryID  uuid.UUID
	CreatedBy  uuid.UUID
	CreatedAt  time.Time
	LinkType   string
	ExpiresAt  time.Time
}

// LibraryTTLInfo holds library data needed for version TTL enforcement.
type LibraryTTLInfo struct {
	OrgID                 uuid.UUID
	LibraryID             uuid.UUID
	HeadCommitID          string
	BlockRepresentationID string
	// RepresentationDefaulted is true when the stored block_representation_id was
	// empty and BlockRepresentationID was derived from the library's own identity
	// (plain:v1 / library:<id>). The derivation is safe, but an empty stored value
	// signals a writer/migration that did not stamp it, so scanners report it as
	// drift rather than hiding it.
	RepresentationDefaulted bool
	// RepresentationInvalid is true when the stored block_representation_id cannot
	// be validated against the library's identity and encrypted flag. This includes
	// malformed identities/representations and cross-domain values. The scanner
	// must skip the library and report drift instead of enqueuing work under an
	// unsafe mapping domain. BlockRepresentationID carries the raw stored value
	// only so the drift metric can classify it.
	RepresentationInvalid bool
	VersionTTLDays        int
}

// CommitWithTimestamp holds commit data needed for version TTL enforcement.
type CommitWithTimestamp struct {
	CommitID  string
	ParentID  string
	RootFSID  string
	CreatedAt time.Time
}

// LibraryAutoDeleteInfo holds library data needed for auto_delete_days enforcement.
type LibraryAutoDeleteInfo struct {
	OrgID                 uuid.UUID
	LibraryID             uuid.UUID
	HeadCommitID          string
	BlockRepresentationID string
	// RepresentationDefaulted mirrors LibraryTTLInfo.RepresentationDefaulted.
	RepresentationDefaulted bool
	// RepresentationInvalid mirrors LibraryTTLInfo.RepresentationInvalid.
	RepresentationInvalid bool
	AutoDeleteDays        int
}

// ExpiredShareInfo holds data about an expired user-to-user share.
type ExpiredShareInfo struct {
	OrgID        uuid.UUID
	LibraryID    uuid.UUID
	ShareID      uuid.UUID
	SharedBy     uuid.UUID
	SharedTo     uuid.UUID
	SharedToType string
	CreatedAt    time.Time
	ExpiresAt    time.Time
}

// ExpiredRestoreJobInfo holds data about an expired/completed restore job.
type ExpiredRestoreJobInfo struct {
	OrgID     uuid.UUID
	LibraryID uuid.UUID
	JobID     uuid.UUID
	Status    string
	ExpiresAt time.Time
}

// ShareInfo holds data about a library share for orphan cleanup.
type ShareInfo struct {
	LibraryID uuid.UUID
	ShareID   uuid.UUID
	SharedTo  uuid.UUID
}

// FileTagInfo holds data about a file tag for orphan cleanup.
type FileTagInfo struct {
	RepoID    uuid.UUID
	FilePath  string
	TagID     int
	FileTagID int
}

// RepoAPITokenInfo holds data about a repo API token for orphan cleanup.
type RepoAPITokenInfo struct {
	RepoID   uuid.UUID
	AppName  string
	APIToken string
}

// GroupShareInfo holds data about a share where shared_to is a group.
type GroupShareInfo struct {
	LibraryID    uuid.UUID
	ShareID      uuid.UUID
	SharedTo     uuid.UUID // group_id
	SharedToType string    // "group"
	OrgID        uuid.UUID // needed for scanner lookups
}

// DeletedUserInfo holds data about a soft-deleted user for cascade processing.
type DeletedUserInfo struct {
	OrgID     uuid.UUID
	UserID    uuid.UUID
	Email     string
	DeletedAt time.Time
}

// DeletedLibraryInfo holds data about a soft-deleted library for trash auto-purge.
type DeletedLibraryInfo struct {
	OrgID                 uuid.UUID
	LibraryID             uuid.UUID
	BlockRepresentationID string
	StorageClass          string
	DeletedAt             time.Time
	// PurgeRequestedAt is set (non-zero) when a permanent-delete path asked for the
	// library to be reclaimed on the retention-independent schedule rather than after
	// TrashRetentionDays. When set, Phase 13 treats the row as eligible regardless of
	// DeletedAt (the worker still applies the normal grace period before processing).
	PurgeRequestedAt time.Time
}

// DeletedOrgInfo holds data about a soft-deleted org for cascade processing.
type DeletedOrgInfo struct {
	OrgID     uuid.UUID
	Name      string
	DeletedAt time.Time
}

// OrgUserInfo holds basic user data for org cascade cleanup.
type OrgUserInfo struct {
	UserID uuid.UUID
	Email  string
}

// OrgLibraryInfo holds basic library data for org cascade cleanup.
type OrgLibraryInfo struct {
	LibraryID    uuid.UUID
	StorageClass string
	OwnerID      uuid.UUID
	DeletedAt    time.Time // zero means library is still active
}

// ShareByUserInfo holds data about a share received by a user.
type ShareByUserInfo struct {
	SharedTo  uuid.UUID // user_id
	LibraryID uuid.UUID
	ShareID   uuid.UUID
}

// ShareByCreatorInfo holds data about a share created by a user.
type ShareByCreatorInfo struct {
	LibraryID uuid.UUID
	ShareID   uuid.UUID
}

// AuditLogEntry records a deletion event for compliance/traceability.
type AuditLogEntry struct {
	OrgID      uuid.UUID
	Action     string // e.g. "delete_library", "delete_group", "gc_block_deleted"
	TargetType string // e.g. "library", "group", "block", "user"
	TargetID   string
	ActorID    string // user who triggered it, or "gc_worker"/"gc_scanner"
	Details    string // JSON or free-text with extra context
	Timestamp  time.Time
}

// BlockClaimReleaseOutcome is what ReleaseStaleBlockClaim observed about a block's
// delete claim. The distinction between "absent" and "too fresh" is load-bearing:
// only the first means the caller may settle its candidate.
type BlockClaimReleaseOutcome int

const (
	// BlockClaimAbsent: the block carries no delete claim at all. Safe to settle.
	BlockClaimAbsent BlockClaimReleaseOutcome = iota
	// BlockClaimReleased: a stale claim was handed back. Safe to settle.
	BlockClaimReleased
	// BlockClaimTooFresh: a claim exists but was taken too recently to distinguish
	// from a live in-flight attempt, so it was left alone. Its owner is irrelevant —
	// a fresh claim belonging to another candidate is exactly as unsafe to lift as a
	// fresh claim belonging to this one.
	//
	// The caller must NOT settle. A claim younger than the staleness threshold may
	// belong to a worker still deleting — releasing it drops the upload fence
	// mid-delete — but it may equally belong to a worker that died seconds ago, in
	// which case the fence still has to come off eventually. This candidate is the
	// only work item that will ever look at that block again, so consuming it now
	// leaves gc_state='deleting' with nothing left to clear it: the block is fenced
	// against every future upload of its content, permanently. Postpone instead and
	// let a later pass release the claim once it has aged out.
	BlockClaimTooFresh
)

// BlockStoreDeleter is a minimal interface for S3 block deletion.
// Allows mocking the storage layer in tests.
type BlockStoreDeleter interface {
	// StorageKeyForHash returns the canonical org-scoped locator this store would
	// mint for a block id. Destructive callers compare the PERSISTED key against it
	// and refuse the delete on a mismatch.
	//
	// The persisted key is authoritative for WHICH object to destroy, but the store
	// is only a bucket client: it applies whatever key it is handed. So a row whose
	// storage_key names another org's prefix — corruption, a bad backfill, a future
	// writer that mints keys — would otherwise aim an org's delete at another org's
	// bytes, which is exactly the cross-org delete P10 closed at the code level. The
	// writers already refuse a non-derived key; this is the same refusal on the side
	// that destroys.
	StorageKeyForHash(hash string) string
	DeleteBlockByStorageKey(ctx context.Context, storageKey string) error
}

// StorageProvider returns a BlockStoreDeleter bound to one org and exact storage
// class. GC must never health-failover a delete to a different physical backend.
type StorageProvider interface {
	GetBlockStoreForOrg(orgID, storageClass string) (BlockStoreDeleter, error)
}
