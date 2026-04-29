package gc

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// GCStore abstracts all database operations used by the GC system.
// This allows unit tests to use an in-memory mock instead of Cassandra.
type GCStore interface {
	// Queue operations
	EnqueueItem(orgID uuid.UUID, queuedAt time.Time, itemType ItemType, itemID string, libraryID uuid.UUID, storageClass string, retryCount int) error
	EnqueueBatch(items []QueueItem) error
	QueueItemExists(orgID uuid.UUID, queuedAt time.Time, itemType ItemType, itemID string) (bool, error)
	DequeueBatch(orgID uuid.UUID, batchSize int, cutoff time.Time) ([]QueueItem, error)
	CompleteItem(orgID uuid.UUID, queuedAt time.Time, itemType ItemType, itemID string) error
	RequeueItem(orgID uuid.UUID, oldQueuedAt, newQueuedAt time.Time, itemType ItemType, itemID string, libraryID uuid.UUID, storageClass string, newRetryCount int) error
	FailItem(item QueueItem, failedAt time.Time, lastError string) error
	GetQueueSize(orgID uuid.UUID) (int, error)
	GetTotalQueueSize() (int, error)
	GetTotalFailedItems() (int, error)
	ListOrgsWithQueuedItems() ([]uuid.UUID, error)
	ListOrgsWithFailedItems(limit int) ([]GCFailedItemOrgInfo, error)
	ListFailedItems(orgID uuid.UUID, limit int) ([]GCFailedItemInfo, error)
	DeleteFailedItem(orgID uuid.UUID, failedAt time.Time, itemType ItemType, itemID string) error
	RequeueFailedItem(orgID uuid.UUID, failedAt time.Time, itemType ItemType, itemID string, queuedAt time.Time) error
	MarkOrgActive(orgID uuid.UUID, activeAt time.Time) error
	RemoveOrgFromActiveSet(orgID uuid.UUID, activeBefore time.Time) error
	MarkOrgDirty(orgID uuid.UUID, dirtyAt time.Time) error
	ListDirtyOrgs(limit int) ([]GCDirtyOrg, error)
	ClearDirtyOrg(orgID uuid.UUID, dirtyBefore time.Time) error
	GetOrgQueueStats(orgID uuid.UUID) (GCOrgStats, error)
	SaveOrgQueueStats(stats GCOrgStats) error
	RecountOrgQueueDepth(orgID uuid.UUID) (int, error)
	RecountOrgFailedDepth(orgID uuid.UUID) (int, error)
	GetOldestQueuedAt(orgID uuid.UUID) (*time.Time, error)
	SumOrgQueueStats() (int, int, error)
	MarkItemProcessed(taskID uuid.UUID) (bool, error)
	GetUserDeletedAt(orgID, userID uuid.UUID) (*time.Time, error)
	GetLibraryDeletedAt(libraryID uuid.UUID) (*time.Time, error)
	GetOrgDeletedAt(orgID uuid.UUID) (*time.Time, error)

	// Block operations (worker)
	//
	// GetBlockRefCount returns the current reference count for a block.
	// It MUST return a non-nil error when the block row does not exist
	// (e.g. gocql.ErrNotFound). RecoverS3Orphans relies on this to
	// distinguish blocks that were claimed-but-not-finalized (row still
	// present → skip) from blocks whose DB row was already removed
	// (error → proceed with S3 cleanup).
	GetBlockRefCount(orgID uuid.UUID, blockID string) (int, error)
	ResolveBlockIDs(orgID uuid.UUID, blockIDs []string) ([]string, error)
	ClaimBlockDelete(orgID uuid.UUID, blockID string) (bool, error)
	FinalizeBlockDelete(orgID uuid.UUID, blockID string) error
	DecrementBlockRefCount(orgID uuid.UUID, blockID string) (bool, error)
	DeleteBlockMapping(orgID uuid.UUID, externalID string) error
	EnsureBlockGCCandidate(orgID uuid.UUID, blockID, storageClass string, candidateAt time.Time) (time.Time, error)
	DeleteBlockGCCandidate(orgID uuid.UUID, blockID string) error
	ListBlockGCCandidateOrgs() ([]uuid.UUID, error)
	ListBlockGCCandidates(orgID uuid.UUID) ([]BlockGCCandidateInfo, error)

	// S3 orphan recovery / pending delete tracking for blocks claimed by GC.
	RecordS3Orphan(orgID uuid.UUID, blockID, storageClass, errMsg string, now time.Time) error
	ListS3OrphanOrgs() ([]uuid.UUID, error)
	ListS3Orphans(orgID uuid.UUID, limit int) ([]S3OrphanInfo, error)
	UpdateS3OrphanAttempt(orgID uuid.UUID, blockID, errMsg string, now time.Time) error
	DeleteS3Orphan(orgID uuid.UUID, blockID string) error

	// Reverse lookup: find block mappings by internal_id (avoids full scan)
	ListBlockMappingsByInternalID(orgID uuid.UUID, internalID string) ([]BlockMapping, error)

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
	ListBlocksForOrg(orgID uuid.UUID) ([]BlockInfo, error)
	ListShareLinks() ([]ShareLinkInfo, error)
	ListDistinctCommitLibraries() ([]uuid.UUID, error)
	ListDistinctFSObjectLibraries() ([]uuid.UUID, error)
	LibraryExists(libraryID uuid.UUID) (bool, error)
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

	// Expired shares (user-to-user library shares)
	ListExpiredShares() ([]ExpiredShareInfo, error)
	DeleteShare(libraryID, shareID uuid.UUID) error

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
	ListAllGroupShares() ([]GroupShareInfo, error)
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
	// AcquireUserHardDeleteLock acquires a short-lived lock for a user cascade
	// delete. Returns (true, nil) when the lock is successfully acquired.
	// activateUser checks this table to block concurrent restores.
	AcquireUserHardDeleteLock(userID uuid.UUID) (bool, error)
	ReleaseUserHardDeleteLock(userID uuid.UUID) error

	// AcquireLibraryHardDeleteLock acquires a short-lived lock for a library
	// cascade delete. Returns (true, nil) when the lock is successfully acquired.
	// restoreDeletedLibrary checks this table to block concurrent restores.
	AcquireLibraryHardDeleteLock(libraryID uuid.UUID) (bool, error)
	ReleaseLibraryHardDeleteLock(libraryID uuid.UUID) error

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

// BlockMapping represents a SHA-1 to SHA-256 block ID mapping.
type BlockMapping struct {
	ExternalID string
	InternalID string
}

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
	RefCount     int
}

type BlockGCCandidateInfo struct {
	BlockID      string
	StorageClass string
	CandidateAt  time.Time
}

// GCOrgStats stores reconciled queue state for a single org.
type GCOrgStats struct {
	OrgID          uuid.UUID
	QueueDepth     int
	FailedDepth    int
	OldestQueuedAt *time.Time
	UpdatedAt      time.Time
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
	OrgID         uuid.UUID
	FailedAt      time.Time
	QueuedAt      time.Time
	ItemType      ItemType
	ItemID        string
	LibraryID     uuid.UUID
	StorageClass  string
	RetryCount    int
	LastError     string
	ResolvedAt    *time.Time
	ResolvedState string
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
	OrgID         uuid.UUID
	BlockID       string
	StorageClass  string
	FirstSeenAt   time.Time
	LastAttemptAt time.Time
	RetryCount    int
	LastError     string
}

// ShareLinkInfo holds data about a share link needed by the scanner.
type ShareLinkInfo struct {
	ShareToken string
	OrgID      uuid.UUID
	ExpiresAt  time.Time
}

// LibraryTTLInfo holds library data needed for version TTL enforcement.
type LibraryTTLInfo struct {
	OrgID          uuid.UUID
	LibraryID      uuid.UUID
	HeadCommitID   string
	VersionTTLDays int
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
	OrgID          uuid.UUID
	LibraryID      uuid.UUID
	HeadCommitID   string
	AutoDeleteDays int
}

// ExpiredShareInfo holds data about an expired user-to-user share.
type ExpiredShareInfo struct {
	LibraryID uuid.UUID
	ShareID   uuid.UUID
	SharedTo  uuid.UUID
	ExpiresAt time.Time
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
	OrgID        uuid.UUID
	LibraryID    uuid.UUID
	StorageClass string
	DeletedAt    time.Time
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
