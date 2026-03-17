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
	DequeueBatch(orgID uuid.UUID, batchSize int, cutoff time.Time) ([]QueueItem, error)
	CompleteItem(orgID uuid.UUID, queuedAt time.Time, itemType ItemType, itemID string) error
	UpdateRetryCount(orgID uuid.UUID, queuedAt time.Time, itemType ItemType, itemID string, retryCount int) error
	GetQueueSize(orgID uuid.UUID) (int, error)
	GetTotalQueueSize() (int, error)
	ListOrgsWithQueuedItems() ([]uuid.UUID, error)

	// Block operations (worker)
	GetBlockRefCount(orgID uuid.UUID, blockID string) (int, error)
	DeleteBlock(orgID uuid.UUID, blockID string) error
	DecrementBlockRefCount(orgID uuid.UUID, blockID string) error
	DeleteBlockMapping(orgID uuid.UUID, externalID string) error

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

	// Version TTL enforcement
	ListLibrariesWithVersionTTL() ([]LibraryTTLInfo, error)
	ListCommitsWithTimestamps(libraryID uuid.UUID) ([]CommitWithTimestamp, error)

	// Auto-delete enforcement
	ListLibrariesWithAutoDelete() ([]LibraryAutoDeleteInfo, error)

	// Share link deletion
	DeleteShareLink(shareToken string) error

	// Expired shares (user-to-user library shares)
	ListExpiredShares() ([]ExpiredShareInfo, error)
	DeleteShare(libraryID, shareID uuid.UUID) error
	DeleteShareByUser(sharedTo, libraryID uuid.UUID) error

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
