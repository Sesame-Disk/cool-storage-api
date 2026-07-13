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
	// the block. This is the liveness check that replaces reading ref_count.
	BlockHasReferences(orgID uuid.UUID, blockID string) (bool, error)
	GetBlockInfo(orgID uuid.UUID, blockID string) (BlockInfo, error)
	// RemoveBlockReference deletes one (block, referrer) reference row. Idempotent.
	RemoveBlockReference(orgID uuid.UUID, blockID, referrer string) error
	ResolveBlockIDs(orgID, libraryID uuid.UUID, blockRepresentationID string, blockIDs []string) ([]string, error)
	// ClaimBlockDelete atomically marks the block row gc_state='deleting' via LWT
	// and records the deterministic claimID for the logical delete attempt.
	// Callers MUST re-check BlockHasReferences after a successful claim before
	// deleting from S3 (claim-then-verify).
	ClaimBlockDelete(orgID uuid.UUID, blockID, claimID string) (bool, error)
	// ReleaseBlockClaim clears gc_state only when the same claimID still owns the
	// row. This prevents another attempt from releasing a claim it did not win.
	ReleaseBlockClaim(orgID uuid.UUID, blockID, claimID string) error
	// FinalizeBlockDelete removes the block row only when the same claimID still
	// owns the row.
	FinalizeBlockDelete(orgID uuid.UUID, blockID, claimID string) error
	DeleteBlockMappingExact(orgID uuid.UUID, representationID, externalID string) error
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
	// must revalidate this before removing any up:* block reference because the
	// by-day table is only a discovery projection and can be stale.
	GetProvisionalBlockRefExpiry(orgID uuid.UUID, blockID, referrer string) (ProvisionalBlockRefExpiryInfo, bool, error)
	// DeleteProvisionalBlockRefExpiryProjection removes only the discovery row.
	// It intentionally leaves the canonical row untouched for stale projection
	// cleanup when an upload ref was renewed or already finalized elsewhere.
	DeleteProvisionalBlockRefExpiryProjection(orgID uuid.UUID, blockID, referrer string, expiresAt time.Time) error
	DeleteProvisionalBlockRefExpiry(orgID uuid.UUID, blockID, referrer string, expiresAt time.Time) error

	// S3 orphan recovery / pending delete tracking for blocks claimed by GC.
	// StartBlockDeleteOrphan records the durable recovery row for a NEW block
	// deletion. It always resets recovery state to pending_s3 so a stale
	// pending_mapping_cleanup row from an older delete cannot make recovery skip
	// the physical object delete for this new lifecycle.
	StartBlockDeleteOrphan(orgID uuid.UUID, blockID, storageClass, representationID, externalSHA1 string, now time.Time) (time.Time, error)
	// RecordS3Orphan preserves and returns the effective first_seen_at identity
	// for an existing orphan row so callers can repair missing recovery metadata
	// or seed test/recovery fixtures without clobbering a newer phase.
	RecordS3Orphan(orgID uuid.UUID, blockID, storageClass, representationID, externalSHA1, errMsg string, now time.Time) (time.Time, error)
	// ListS3OrphansByDay enumerates S3-orphan rows whose `first_seen_at`
	// falls on the given UTC day for one discovery bucket. `limit` caps the
	// number of rows returned for a single (day, bucket) pair.
	ListS3OrphansByDay(day time.Time, bucket int, limit int) ([]S3OrphanInfo, error)
	// MarkS3OrphanMappingCleanupPending advances the recovery row after the S3
	// delete has completed so restart recovery can finish forward-mapping cleanup
	// without touching S3 again.
	MarkS3OrphanMappingCleanupPending(orgID uuid.UUID, blockID, representationID, externalSHA1 string, now time.Time) error
	UpdateS3OrphanAttempt(orgID uuid.UUID, blockID, errMsg string, now time.Time) error
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
	BlockID          string
	StorageClass     string
	CreatedAt        *time.Time
	RepresentationID string
	// Sha1 is the block's external Seafile SHA-1 (blocks.sha1), captured here so
	// GC mapping cleanup can delete the single forward block_id_mappings row
	// without the dropped reverse index. Empty for legacy/pre-PR2 rows.
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

// S3OrphanInfo holds data about a block whose S3 deletion still needs recovery.
// Rows are created as soon as GC claims the block for deletion, before the DB
// row is physically removed, so a crash between DB and S3 phases remains
// recoverable after restart.
type S3OrphanInfo struct {
	OrgID            uuid.UUID
	BlockID          string
	StorageClass     string
	RepresentationID string
	ExternalSHA1     string
	RecoveryPhase    string
	FirstSeenAt      time.Time
	LastAttemptAt    time.Time
	RetryCount       int
	LastError        string
}

const (
	S3OrphanPhasePendingS3             = "pending_s3"
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

// BlockStoreDeleter is a minimal interface for S3 block deletion.
// Allows mocking the storage layer in tests.
type BlockStoreDeleter interface {
	DeleteBlock(ctx context.Context, blockID string) error
}

// StorageProvider returns a BlockStoreDeleter for a given storage class.
// Allows mocking the storage.Manager in tests.
type StorageProvider interface {
	GetBlockStore(storageClass string) (BlockStoreDeleter, error)
}
