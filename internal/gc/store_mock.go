package gc

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/traffic"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"
)

// MockStore is an in-memory implementation of GCStore for testing.
type MockStore struct {
	mu sync.RWMutex

	// gc_queue items keyed by orgID
	queue map[uuid.UUID][]QueueItem
	// gc_pending_items keyed by semantic item identity.
	pendingItems map[mockPendingItemKey]*time.Time
	// active queue orgs keyed by orgID
	activeQueueOrgs map[uuid.UUID]time.Time
	// when true, ListOrgsWithQueuedItems simulates Cassandra's gc_active_orgs-backed listing.
	useActiveQueueOrgsForListing bool
	// dirty queue orgs keyed by orgID
	dirtyQueueOrgs map[uuid.UUID]time.Time
	// reconciled org-level queue stats keyed by orgID
	orgQueueStats map[uuid.UUID]GCOrgStats
	// failed GC items keyed by orgID
	failedItems map[uuid.UUID][]GCFailedItemInfo

	// blocks keyed by "orgID:blockID"
	blocks          map[string]*mockBlock
	blockReferences map[string]map[string]struct{}

	// block GC candidates keyed by "orgID:blockID"
	blockGCCandidates map[string]*mockBlockGCCandidate
	// block GC candidate discovery rows keyed by the full projection PK.
	blockGCCandidateProjections map[mockBlockGCCandidateProjectionKey]BlockGCCandidateInfo
	// provisional upload-ref expiry rows keyed by "orgID:blockID:referrer".
	provisionalBlockRefExpiries map[string]*mockProvisionalBlockRefExpiry
	// provisional upload-ref expiry discovery rows keyed by the full projection PK.
	provisionalBlockRefExpiryProjections map[mockProvisionalBlockRefExpiryProjectionKey]ProvisionalBlockRefExpiryInfo

	// block_id_mappings keyed by "orgID:externalID"
	mappings map[string]string // externalID -> internalID

	// commits keyed by "libraryID:commitID"
	commits map[string]*mockCommit

	// fs_objects keyed by "libraryID:fsID"
	fsObjects map[string]*mockFSObject
	// injected fs_object read failures keyed by "libraryID:fsID"
	getFSObjectErrors map[string]error

	// libraries keyed by libraryID
	libraries map[uuid.UUID]*mockLibrary

	// organizations keyed by orgID
	organizations []uuid.UUID
	orgNames      map[uuid.UUID]string
	orgStatus     map[uuid.UUID]string
	orgDeletedAt  map[uuid.UUID]time.Time

	// users keyed by "orgID:userID"
	users map[string]*mockUser

	// groups keyed by "orgID:groupID"
	groups map[string]bool

	// group_members keyed by "groupID:userID"
	groupMembers map[string]bool

	// groups_by_member keyed by "orgID:userID:groupID"
	groupsByMember map[string]bool

	// deleted_libraries keyed by "libraryID"
	deletedLibraries map[uuid.UUID]*mockDeletedLibrary

	// storage snapshots keyed by storage scope.
	storageSnapshots map[string]traffic.StorageSnapshot

	// pending aggregate storage reconciliation keyed by scope.
	storageCounterReconciliations map[string]*mockStorageCounterReconciliation

	// in-progress org hard-delete locks keyed by orgID.
	orgHardDeleteLocks map[uuid.UUID]mockHardDeleteLock

	// in-progress user hard-delete locks keyed by userID.
	userHardDeleteLocks map[uuid.UUID]mockHardDeleteLock

	// in-progress library hard-delete locks keyed by libraryID.
	libraryHardDeleteLocks map[uuid.UUID]mockHardDeleteLock

	// share_links keyed by shareToken
	shareLinks map[string]*mockShareLink

	// shares keyed by "libraryID:shareID"
	shares map[string]*mockShare

	// restore_jobs keyed by "orgID:libraryID:jobID"
	restoreJobs map[string]*mockRestoreJob

	// repo_tags keyed by "libraryID:tagID"
	repoTags map[string]bool

	// file_tags keyed by "libraryID:filePath:tagID"
	fileTags map[string]*mockFileTag

	// repo_api_tokens keyed by "libraryID:appName"
	apiTokens map[string]*mockAPIToken

	// locked_files keyed by "libraryID:path"
	lockedFiles map[string]bool

	// starred_files keyed by "userID"
	starredFiles map[uuid.UUID]bool

	// monitored_repos keyed by "userID"
	monitoredRepos map[uuid.UUID]bool

	// gc_stats keyed by stat_key
	gcStats map[string]string

	// test hooks for scanner and worker failure paths.
	enqueueBatchErr                error
	listExpiredShareLinksErr       error
	deleteExpiredShareLinkErr      error
	listExpiredSharesErr           error
	deleteExpiredShareErr          error
	listDeletedUsersExpiredErr     error
	deleteRestoreJobsByLibraryErr  error
	listLibrariesByOwnerErr        error
	softDeleteLibraryErr           error
	listGroupMembershipsByUserErr  error
	deleteGroupMemberErr           error
	deleteGroupByMemberErr         error
	listSharesByUserErr            error
	listSharesCreatedByUserErr     error
	deleteShareErr                 error
	deleteStarredFilesByUserErr    error
	deleteMonitoredReposByUserErr  error
	deleteAPIKeysByUserErr         error
	hardDeleteUserErr              error
	listUsersByOrgErr              error
	listGroupsByOrgErr             error
	listLibrariesForOrgErr         error
	deleteLibraryStorageCounterErr error
	deleteGroupFullErr             error
	acquireOrgHardDeleteLockHook   func(orgID uuid.UUID)
	beginOrgPurgeHook              func(orgID uuid.UUID)
	getBlockRefCountErr            error
	blockHasReferencesHook         func(orgID uuid.UUID, blockID string, current bool) (bool, error)

	// optional test hooks for reproducing concurrency windows deterministically.
	getQueueSizeHook      func(orgID uuid.UUID, size int)
	removeActiveOrgHook   func(orgID uuid.UUID, activeBefore time.Time)
	recountQueueDepthHook func(orgID uuid.UUID, depth int)
	// requeueItemErr, when non-nil, forces RequeueItem to return this error
	// without mutating state. Used to exercise IncrementRetry failure paths
	// where the LoggedBatch never applied.
	requeueItemErr error
	// requeueItemErrAfterMutate, when non-nil, forces RequeueItem to apply
	// the queue mutation AND THEN return this error. Models the ambiguous
	// LoggedBatch case (Cassandra timeout / unavailable) where the batch
	// committed at the cluster but the client observed a failure.
	requeueItemErrAfterMutate error
	// ensureBlockGCCandidateErrAfterMutate, when non-nil, forces
	// EnsureBlockGCCandidate to preserve the canonical/discovery rows and then
	// return this error. Models degraded projection-repair outcomes where the
	// candidate identity is still known to the caller.
	ensureBlockGCCandidateErrAfterMutate error
	// dlqOpHook is invoked at the very top of RequeueFailedItem and
	// DeleteFailedItem on the mock — before the internal mutex is taken —
	// so concurrency tests can observe whether two admin DLQ ops are
	// allowed to overlap (i.e. whether dlqOpsMu in the Service is doing
	// its job).
	dlqOpHook func(orgID uuid.UUID, op string)

	// audit_log entries
	auditLog []AuditLogEntry

	// S3 orphans keyed by "orgID:blockID"
	s3Orphans map[string]*S3OrphanInfo
	// S3 orphan discovery rows keyed by the full projection PK.
	s3OrphanProjections map[mockS3OrphanProjectionKey]S3OrphanInfo
}

type mockBlock struct {
	OrgID        uuid.UUID
	BlockID      string
	StorageClass string
	GCState      string
	GCClaimID    string
}

type mockPendingItemKey struct {
	OrgID      uuid.UUID
	LibraryID  uuid.UUID
	ItemType   ItemType
	ItemID     string
	IdentityAt time.Time
}

type mockBlockGCCandidate struct {
	OrgID        uuid.UUID
	BlockID      string
	StorageClass string
	CandidateAt  time.Time
}

type mockBlockGCCandidateProjectionKey struct {
	CandidateDay time.Time
	Bucket       int
	CandidateAt  time.Time
	OrgID        uuid.UUID
	BlockID      string
}

type mockS3OrphanProjectionKey struct {
	FirstSeenDay time.Time
	Bucket       int
	FirstSeenAt  time.Time
	OrgID        uuid.UUID
	BlockID      string
}

type mockProvisionalBlockRefExpiry struct {
	OrgID        uuid.UUID
	BlockID      string
	Referrer     string
	StorageClass string
	ExpiresAt    time.Time
}

type mockProvisionalBlockRefExpiryProjectionKey struct {
	ExpiryDay time.Time
	Bucket    int
	ExpiresAt time.Time
	OrgID     uuid.UUID
	BlockID   string
	Referrer  string
}

type mockCommit struct {
	LibraryID uuid.UUID
	CommitID  string
	RootFSID  string
	ParentID  string
	CreatedAt time.Time
}

type mockFSObject struct {
	LibraryID  uuid.UUID
	FSID       string
	ObjType    string
	BlockIDs   []string
	DirEntries []string
}

type mockLibrary struct {
	OrgID          uuid.UUID
	LibraryID      uuid.UUID
	OwnerID        uuid.UUID
	StorageClass   string
	HeadCommitID   string
	VersionTTLDays int
	AutoDeleteDays int
	DeletedAt      time.Time
	// SizeBytes and FileCount mirror the canonical libraries.size_bytes and
	// libraries.file_count columns used by ReconcilePendingStorageCounters
	// in production. Storage-counter rows (m.storageSnapshots) are derived
	// caches that can drift; aggregate reconciliation must read from these
	// canonical fields to match store_cassandra.go semantics.
	SizeBytes int64
	FileCount int64
}

type mockShareLink struct {
	ShareToken string
	OrgID      uuid.UUID
	LibraryID  uuid.UUID
	CreatedBy  uuid.UUID
	CreatedAt  time.Time
	LinkType   string
	ExpiresAt  time.Time
}

type mockShare struct {
	OrgID        uuid.UUID
	LibraryID    uuid.UUID
	ShareID      uuid.UUID
	SharedBy     uuid.UUID
	SharedTo     uuid.UUID
	SharedToType string
	CreatedAt    time.Time
	ExpiresAt    time.Time
}

type mockRestoreJob struct {
	OrgID     uuid.UUID
	LibraryID uuid.UUID
	JobID     uuid.UUID
	Status    string
	ExpiresAt time.Time
}

type mockFileTag struct {
	RepoID    uuid.UUID
	FilePath  string
	TagID     int
	FileTagID int
}

type mockAPIToken struct {
	RepoID   uuid.UUID
	AppName  string
	APIToken string
}

type mockUser struct {
	OrgID     uuid.UUID
	UserID    uuid.UUID
	Email     string
	Status    string
	DeletedAt *time.Time
}

type mockDeletedLibrary struct {
	OrgID        uuid.UUID
	LibraryID    uuid.UUID
	StorageClass string
	DeletedAt    time.Time
}

type mockStorageCounterReconciliation struct {
	Scope       string
	OrgID       uuid.UUID
	OwnerID     uuid.UUID
	RequestedAt time.Time
}

type mockHardDeleteLock struct {
	LeaseToken uuid.UUID
	StartedAt  time.Time
	Heartbeat  time.Time
}

func mockAcquireHardDeleteLock(locks map[uuid.UUID]mockHardDeleteLock, targetID, leaseToken uuid.UUID) bool {
	now := time.Now().UTC()
	lock, locked := locks[targetID]
	if locked && now.Sub(lock.Heartbeat) < hardDeleteLockStaleAfter {
		return false
	}
	locks[targetID] = mockHardDeleteLock{LeaseToken: leaseToken, StartedAt: now, Heartbeat: now}
	return true
}

// NewMockStore creates a new in-memory mock store.
func NewMockStore() *MockStore {
	return &MockStore{
		queue:                                make(map[uuid.UUID][]QueueItem),
		pendingItems:                         make(map[mockPendingItemKey]*time.Time),
		activeQueueOrgs:                      make(map[uuid.UUID]time.Time),
		dirtyQueueOrgs:                       make(map[uuid.UUID]time.Time),
		orgQueueStats:                        make(map[uuid.UUID]GCOrgStats),
		failedItems:                          make(map[uuid.UUID][]GCFailedItemInfo),
		blocks:                               make(map[string]*mockBlock),
		blockReferences:                      make(map[string]map[string]struct{}),
		blockGCCandidates:                    make(map[string]*mockBlockGCCandidate),
		blockGCCandidateProjections:          make(map[mockBlockGCCandidateProjectionKey]BlockGCCandidateInfo),
		provisionalBlockRefExpiries:          make(map[string]*mockProvisionalBlockRefExpiry),
		provisionalBlockRefExpiryProjections: make(map[mockProvisionalBlockRefExpiryProjectionKey]ProvisionalBlockRefExpiryInfo),
		mappings:                             make(map[string]string),
		commits:                              make(map[string]*mockCommit),
		fsObjects:                            make(map[string]*mockFSObject),
		getFSObjectErrors:                    make(map[string]error),
		libraries:                            make(map[uuid.UUID]*mockLibrary),
		orgNames:                             make(map[uuid.UUID]string),
		orgStatus:                            make(map[uuid.UUID]string),
		orgDeletedAt:                         make(map[uuid.UUID]time.Time),
		users:                                make(map[string]*mockUser),
		groups:                               make(map[string]bool),
		groupMembers:                         make(map[string]bool),
		groupsByMember:                       make(map[string]bool),
		deletedLibraries:                     make(map[uuid.UUID]*mockDeletedLibrary),
		storageSnapshots:                     make(map[string]traffic.StorageSnapshot),
		storageCounterReconciliations:        make(map[string]*mockStorageCounterReconciliation),
		orgHardDeleteLocks:                   make(map[uuid.UUID]mockHardDeleteLock),
		userHardDeleteLocks:                  make(map[uuid.UUID]mockHardDeleteLock),
		libraryHardDeleteLocks:               make(map[uuid.UUID]mockHardDeleteLock),
		shareLinks:                           make(map[string]*mockShareLink),
		shares:                               make(map[string]*mockShare),
		restoreJobs:                          make(map[string]*mockRestoreJob),
		repoTags:                             make(map[string]bool),
		fileTags:                             make(map[string]*mockFileTag),
		apiTokens:                            make(map[string]*mockAPIToken),
		lockedFiles:                          make(map[string]bool),
		starredFiles:                         make(map[uuid.UUID]bool),
		monitoredRepos:                       make(map[uuid.UUID]bool),
		gcStats:                              make(map[string]string),
		organizations:                        nil,
		s3Orphans:                            make(map[string]*S3OrphanInfo),
		s3OrphanProjections:                  make(map[mockS3OrphanProjectionKey]S3OrphanInfo),
	}
}

func newMockPendingItemKey(orgID, libraryID uuid.UUID, itemType ItemType, itemID string, identityAt time.Time) mockPendingItemKey {
	return mockPendingItemKey{
		OrgID:      orgID,
		LibraryID:  libraryID,
		ItemType:   itemType,
		ItemID:     itemID,
		IdentityAt: identityAt,
	}
}

func newMockBlockGCCandidateProjectionKey(orgID uuid.UUID, blockID string, candidateAt time.Time) mockBlockGCCandidateProjectionKey {
	candidateAt = candidateAt.UTC()
	return mockBlockGCCandidateProjectionKey{
		CandidateDay: db.GCProjectionUTCDate(candidateAt),
		Bucket:       db.GCDiscoveryBucket(orgID.String(), blockID),
		CandidateAt:  candidateAt,
		OrgID:        orgID,
		BlockID:      blockID,
	}
}

func newMockS3OrphanProjectionKey(orgID uuid.UUID, blockID string, firstSeenAt time.Time) mockS3OrphanProjectionKey {
	firstSeenAt = firstSeenAt.UTC()
	return mockS3OrphanProjectionKey{
		FirstSeenDay: db.GCProjectionUTCDate(firstSeenAt),
		Bucket:       db.GCDiscoveryBucket(orgID.String(), blockID),
		FirstSeenAt:  firstSeenAt,
		OrgID:        orgID,
		BlockID:      blockID,
	}
}

func newMockProvisionalBlockRefExpiryProjectionKey(orgID uuid.UUID, blockID, referrer string, expiresAt time.Time) mockProvisionalBlockRefExpiryProjectionKey {
	expiresAt = expiresAt.UTC()
	return mockProvisionalBlockRefExpiryProjectionKey{
		ExpiryDay: db.GCProjectionUTCDate(expiresAt),
		Bucket:    db.GCDiscoveryBucket(orgID.String(), blockID, referrer),
		ExpiresAt: expiresAt,
		OrgID:     orgID,
		BlockID:   blockID,
		Referrer:  referrer,
	}
}

func (m *MockStore) upsertBlockGCCandidateProjection(candidate *mockBlockGCCandidate) {
	key := newMockBlockGCCandidateProjectionKey(candidate.OrgID, candidate.BlockID, candidate.CandidateAt)
	m.blockGCCandidateProjections[key] = BlockGCCandidateInfo{
		OrgID:        candidate.OrgID,
		BlockID:      candidate.BlockID,
		StorageClass: candidate.StorageClass,
		CandidateAt:  candidate.CandidateAt.UTC(),
	}
}

func (m *MockStore) upsertS3OrphanProjection(orphan *S3OrphanInfo) {
	key := newMockS3OrphanProjectionKey(orphan.OrgID, orphan.BlockID, orphan.FirstSeenAt)
	m.s3OrphanProjections[key] = *orphan
}

func (m *MockStore) upsertProvisionalBlockRefExpiryProjection(expiry *mockProvisionalBlockRefExpiry) {
	key := newMockProvisionalBlockRefExpiryProjectionKey(expiry.OrgID, expiry.BlockID, expiry.Referrer, expiry.ExpiresAt)
	m.provisionalBlockRefExpiryProjections[key] = ProvisionalBlockRefExpiryInfo{
		OrgID:        expiry.OrgID,
		BlockID:      expiry.BlockID,
		Referrer:     expiry.Referrer,
		StorageClass: expiry.StorageClass,
		ExpiresAt:    expiry.ExpiresAt.UTC(),
	}
}

func (m *MockStore) upsertPendingItem(orgID, libraryID uuid.UUID, itemType ItemType, itemID string, identityAt time.Time, expiresAt *time.Time) {
	key := newMockPendingItemKey(orgID, libraryID, itemType, itemID, identityAt)
	if expiresAt == nil {
		m.pendingItems[key] = nil
		return
	}
	expiry := *expiresAt
	m.pendingItems[key] = &expiry
}

func (m *MockStore) deletePendingItem(orgID, libraryID uuid.UUID, itemType ItemType, itemID string, identityAt time.Time) {
	delete(m.pendingItems, newMockPendingItemKey(orgID, libraryID, itemType, itemID, identityAt))
}

// --- Test helpers for seeding data ---

func (m *MockStore) AddOrganization(orgID uuid.UUID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.organizations = append(m.organizations, orgID)
	m.orgNames[orgID] = ""
}

func (m *MockStore) AddBlock(orgID uuid.UUID, blockID, storageClass string, refCount int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%s:%s", orgID, blockID)
	m.blocks[key] = &mockBlock{
		OrgID:        orgID,
		BlockID:      blockID,
		StorageClass: storageClass,
	}
	// Model the legacy refCount as that many distinct reference rows so existing
	// tests keep their "block is alive with N refs" intent under the row model.
	if refCount > 0 {
		refs := make(map[string]struct{}, refCount)
		for i := 0; i < refCount; i++ {
			refs[fmt.Sprintf("synthetic:%d", i)] = struct{}{}
		}
		m.blockReferences[key] = refs
	}
}

func (m *MockStore) AddBlockGCCandidate(orgID uuid.UUID, blockID, storageClass string, candidateAt time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%s:%s", orgID, blockID)
	candidate := &mockBlockGCCandidate{
		OrgID:        orgID,
		BlockID:      blockID,
		StorageClass: storageClass,
		CandidateAt:  candidateAt.UTC(),
	}
	m.blockGCCandidates[key] = candidate
	m.upsertBlockGCCandidateProjection(candidate)
}

func (m *MockStore) AddProvisionalBlockRefExpiry(orgID uuid.UUID, blockID, referrer, storageClass string, expiresAt time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%s:%s:%s", orgID, blockID, referrer)
	expiry := &mockProvisionalBlockRefExpiry{
		OrgID:        orgID,
		BlockID:      blockID,
		Referrer:     referrer,
		StorageClass: storageClass,
		ExpiresAt:    expiresAt.UTC(),
	}
	m.provisionalBlockRefExpiries[key] = expiry
	m.upsertProvisionalBlockRefExpiryProjection(expiry)
}

func (m *MockStore) AddProvisionalBlockRefExpiryProjectionForTest(orgID uuid.UUID, blockID, referrer, storageClass string, expiresAt time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	expiresAt = expiresAt.UTC()
	m.provisionalBlockRefExpiryProjections[newMockProvisionalBlockRefExpiryProjectionKey(orgID, blockID, referrer, expiresAt)] = ProvisionalBlockRefExpiryInfo{
		OrgID:        orgID,
		BlockID:      blockID,
		Referrer:     referrer,
		StorageClass: storageClass,
		ExpiresAt:    expiresAt,
	}
}

func (m *MockStore) DeleteBlockGCCandidateProjectionForTest(orgID uuid.UUID, blockID string, candidateAt time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.blockGCCandidateProjections, newMockBlockGCCandidateProjectionKey(orgID, blockID, candidateAt))
}

func (m *MockStore) DeleteProvisionalBlockRefExpiryProjectionForTest(orgID uuid.UUID, blockID, referrer string, expiresAt time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.provisionalBlockRefExpiryProjections, newMockProvisionalBlockRefExpiryProjectionKey(orgID, blockID, referrer, expiresAt))
}

func (m *MockStore) DeleteS3OrphanProjectionForTest(orgID uuid.UUID, blockID string, firstSeenAt time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.s3OrphanProjections, newMockS3OrphanProjectionKey(orgID, blockID, firstSeenAt))
}

func (m *MockStore) AddBlockMapping(orgID uuid.UUID, externalID, internalID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%s:%s", orgID, externalID)
	m.mappings[key] = internalID
}

func (m *MockStore) AddStorageSnapshot(scope string, bytesUsed, fileCount int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.storageSnapshots[scope] = traffic.StorageSnapshot{BytesUsed: bytesUsed, FileCount: fileCount}
}

func (m *MockStore) StorageSnapshot(scope string) traffic.StorageSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.storageSnapshots[scope]
}

func (m *MockStore) AddPendingStorageCounterReconciliation(scope string, orgID, ownerID uuid.UUID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.storageCounterReconciliations[scope] = &mockStorageCounterReconciliation{
		Scope:       scope,
		OrgID:       orgID,
		OwnerID:     ownerID,
		RequestedAt: time.Now(),
	}
}

func (m *MockStore) AddCommit(libraryID uuid.UUID, commitID, rootFSID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%s:%s", libraryID, commitID)
	m.commits[key] = &mockCommit{
		LibraryID: libraryID,
		CommitID:  commitID,
		RootFSID:  rootFSID,
		CreatedAt: time.Now(),
	}
}

// AddCommitWithDetails adds a commit with parent and creation time for TTL testing.
func (m *MockStore) AddCommitWithDetails(libraryID uuid.UUID, commitID, rootFSID, parentID string, createdAt time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%s:%s", libraryID, commitID)
	m.commits[key] = &mockCommit{
		LibraryID: libraryID,
		CommitID:  commitID,
		RootFSID:  rootFSID,
		ParentID:  parentID,
		CreatedAt: createdAt,
	}
}

// AddLibraryWithTTL adds a library with version TTL configuration.
func (m *MockStore) AddLibraryWithTTL(orgID, libraryID uuid.UUID, storageClass, headCommitID string, versionTTLDays int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.libraries[libraryID] = &mockLibrary{
		OrgID:          orgID,
		LibraryID:      libraryID,
		StorageClass:   storageClass,
		HeadCommitID:   headCommitID,
		VersionTTLDays: versionTTLDays,
	}
}

func (m *MockStore) AddFSObject(libraryID uuid.UUID, fsID, objType string, blockIDs []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%s:%s", libraryID, fsID)
	m.fsObjects[key] = &mockFSObject{
		LibraryID: libraryID,
		FSID:      fsID,
		ObjType:   objType,
		BlockIDs:  blockIDs,
	}
}

// AddFSObjectWithEntries adds an fs_object with child dir entries for tree walking.
func (m *MockStore) AddFSObjectWithEntries(libraryID uuid.UUID, fsID, objType string, blockIDs, dirEntries []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%s:%s", libraryID, fsID)
	m.fsObjects[key] = &mockFSObject{
		LibraryID:  libraryID,
		FSID:       fsID,
		ObjType:    objType,
		BlockIDs:   blockIDs,
		DirEntries: dirEntries,
	}
}

// AddLibraryWithAutoDelete adds a library with auto_delete_days configuration.
func (m *MockStore) AddLibraryWithAutoDelete(orgID, libraryID uuid.UUID, storageClass, headCommitID string, autoDeleteDays int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.libraries[libraryID] = &mockLibrary{
		OrgID:          orgID,
		LibraryID:      libraryID,
		StorageClass:   storageClass,
		HeadCommitID:   headCommitID,
		AutoDeleteDays: autoDeleteDays,
	}
}

func (m *MockStore) AddLibrary(orgID, libraryID uuid.UUID, storageClass string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.libraries[libraryID] = &mockLibrary{
		OrgID:        orgID,
		LibraryID:    libraryID,
		StorageClass: storageClass,
	}
}

func (m *MockStore) AddLibraryWithOwner(orgID, libraryID, ownerID uuid.UUID, storageClass string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.libraries[libraryID] = &mockLibrary{
		OrgID:        orgID,
		LibraryID:    libraryID,
		OwnerID:      ownerID,
		StorageClass: storageClass,
	}
}

// SetLibraryCanonicalStats sets the canonical size_bytes / file_count columns
// on a mock library row. Tests must use this instead of (or in addition to)
// AddStorageSnapshot for lib-scope when exercising the aggregate
// reconciliation path: production reads from these canonical columns, and a
// test that only seeds the lib-scope storage_counters row will not exercise
// the same code path.
func (m *MockStore) SetLibraryCanonicalStats(libraryID uuid.UUID, sizeBytes, fileCount int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	lib, ok := m.libraries[libraryID]
	if !ok {
		return
	}
	lib.SizeBytes = sizeBytes
	lib.FileCount = fileCount
}

func (m *MockStore) AddShareLink(shareToken string, orgID uuid.UUID, expiresAt time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	createdAt := time.Now()
	m.shareLinks[shareToken] = &mockShareLink{
		ShareToken: shareToken,
		OrgID:      orgID,
		LibraryID:  uuid.New(),
		CreatedBy:  uuid.New(),
		CreatedAt:  createdAt,
		LinkType:   "file",
		ExpiresAt:  expiresAt,
	}
}

func (m *MockStore) AddShare(libraryID, shareID, sharedTo uuid.UUID, expiresAt time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%s:%s", libraryID, shareID)
	orgID := uuid.Nil
	if lib, ok := m.libraries[libraryID]; ok {
		orgID = lib.OrgID
	}
	m.shares[key] = &mockShare{
		OrgID:        orgID,
		LibraryID:    libraryID,
		ShareID:      shareID,
		SharedBy:     uuid.New(),
		SharedTo:     sharedTo,
		SharedToType: "user",
		CreatedAt:    time.Now(),
		ExpiresAt:    expiresAt,
	}
}

func (m *MockStore) AddGroupShare(libraryID, shareID, groupID uuid.UUID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%s:%s", libraryID, shareID)
	orgID := uuid.Nil
	if lib, ok := m.libraries[libraryID]; ok {
		orgID = lib.OrgID
	}
	m.shares[key] = &mockShare{
		OrgID:        orgID,
		LibraryID:    libraryID,
		ShareID:      shareID,
		SharedTo:     groupID,
		SharedToType: "group",
		SharedBy:     uuid.New(),
		CreatedAt:    time.Now(),
	}
}

func (m *MockStore) AddRestoreJob(orgID, libraryID, jobID uuid.UUID, status string, expiresAt time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%s:%s:%s", orgID, libraryID, jobID)
	m.restoreJobs[key] = &mockRestoreJob{
		OrgID:     orgID,
		LibraryID: libraryID,
		JobID:     jobID,
		Status:    status,
		ExpiresAt: expiresAt,
	}
}

// AddOrganizationWithName adds an org with a name (for cascade tests).
func (m *MockStore) AddOrganizationWithName(orgID uuid.UUID, name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.organizations = append(m.organizations, orgID)
	m.orgNames[orgID] = name
}

// AddDeletedOrg adds an org with status='deleted' and deleted_at set (for scanner Phase 12).
func (m *MockStore) AddDeletedOrg(orgID uuid.UUID, name string, deletedAt time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.organizations = append(m.organizations, orgID)
	m.orgNames[orgID] = name
	m.orgStatus[orgID] = "deleted"
	m.orgDeletedAt[orgID] = deletedAt
}

// AddUser adds a user to the mock store.
func (m *MockStore) AddUser(orgID, userID uuid.UUID, email string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%s:%s", orgID, userID)
	m.users[key] = &mockUser{OrgID: orgID, UserID: userID, Email: email}
}

// AddDeletedUser adds a user with status='deleted' and deleted_at set (for scanner Phase 10).
func (m *MockStore) AddDeletedUser(orgID, userID uuid.UUID, email string, deletedAt time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%s:%s", orgID, userID)
	m.users[key] = &mockUser{OrgID: orgID, UserID: userID, Email: email, Status: "deleted", DeletedAt: &deletedAt}
}

// AddGroupForOrg adds a group to the mock store.
func (m *MockStore) AddGroupForOrg(orgID, groupID uuid.UUID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%s:%s", orgID, groupID)
	m.groups[key] = true
}

// AddGroupMembership adds a user to a group (both tables).
func (m *MockStore) AddGroupMembership(orgID, userID, groupID uuid.UUID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.groupMembers[fmt.Sprintf("%s:%s", groupID, userID)] = true
	m.groupsByMember[fmt.Sprintf("%s:%s:%s", orgID, userID, groupID)] = true
}

// AddDeletedLibrary adds a soft-deleted library (for scanner Phase 11).
func (m *MockStore) AddDeletedLibrary(orgID, libraryID uuid.UUID, storageClass string, deletedAt time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.libraries[libraryID] = &mockLibrary{OrgID: orgID, LibraryID: libraryID, StorageClass: storageClass}
	m.deletedLibraries[libraryID] = &mockDeletedLibrary{OrgID: orgID, LibraryID: libraryID, StorageClass: storageClass, DeletedAt: deletedAt}
}

// AddShareByUser adds a share received by a user.
func (m *MockStore) AddShareByUser(orgID, userID, libraryID uuid.UUID) uuid.UUID {
	m.mu.Lock()
	defer m.mu.Unlock()
	shareID := uuid.New()
	shareKey := fmt.Sprintf("%s:%s", libraryID, shareID)
	m.shares[shareKey] = &mockShare{
		OrgID:        orgID,
		LibraryID:    libraryID,
		ShareID:      shareID,
		SharedBy:     uuid.New(),
		SharedTo:     userID,
		SharedToType: "user",
	}
	return shareID
}

// AddShareCreatedByUser adds a user-to-user share created by a specific user.
func (m *MockStore) AddShareCreatedByUser(orgID, userID, recipientID, libraryID uuid.UUID) uuid.UUID {
	m.mu.Lock()
	defer m.mu.Unlock()
	shareID := uuid.New()
	shareKey := fmt.Sprintf("%s:%s", libraryID, shareID)
	m.shares[shareKey] = &mockShare{
		OrgID:        orgID,
		LibraryID:    libraryID,
		ShareID:      shareID,
		SharedBy:     userID,
		SharedTo:     recipientID,
		SharedToType: "user",
	}
	return shareID
}

// AddStarredFile adds a starred file entry for a user.
func (m *MockStore) AddStarredFile(userID uuid.UUID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.starredFiles[userID] = true
}

// AddMonitoredRepo adds a monitored repo entry for a user.
func (m *MockStore) AddMonitoredRepo(userID uuid.UUID) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.monitoredRepos[userID] = true
}

// HasUser returns true if the user exists in the store (for test assertions).
func (m *MockStore) HasUser(orgID, userID uuid.UUID) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.users[fmt.Sprintf("%s:%s", orgID, userID)]
	return ok
}

// HasGroup returns true if the group exists in the store (for test assertions).
func (m *MockStore) HasGroup(orgID, groupID uuid.UUID) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.groups[fmt.Sprintf("%s:%s", orgID, groupID)]
}

// HasOrg returns true if the org exists (not hard-deleted).
func (m *MockStore) HasOrg(orgID uuid.UUID) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, id := range m.organizations {
		if id == orgID {
			return true
		}
	}
	return false
}

// HasStarredFiles returns true if starred files exist for user.
func (m *MockStore) HasStarredFiles(userID uuid.UUID) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.starredFiles[userID]
}

// HasMonitoredRepos returns true if monitored repos exist for user.
func (m *MockStore) HasMonitoredRepos(userID uuid.UUID) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.monitoredRepos[userID]
}

// AuditLogEntries returns all audit log entries (for test assertions).
func (m *MockStore) AuditLogEntries() []AuditLogEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]AuditLogEntry{}, m.auditLog...)
}

// GetBlock returns a block for test assertions.
func (m *MockStore) GetBlock(orgID uuid.UUID, blockID string) *mockBlock {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.blocks[fmt.Sprintf("%s:%s", orgID, blockID)]
}

func (m *MockStore) GetBlockInfo(orgID uuid.UUID, blockID string) (BlockInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	block := m.blocks[fmt.Sprintf("%s:%s", orgID, blockID)]
	if block == nil {
		return BlockInfo{}, gocql.ErrNotFound
	}
	return BlockInfo{BlockID: block.BlockID, StorageClass: block.StorageClass}, nil
}

// BlockReferenceCount returns how many reference rows a block currently has.
// Test helper that replaces the old mutable ref_count assertions.
func (m *MockStore) BlockReferenceCount(orgID uuid.UUID, blockID string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.blockReferences[fmt.Sprintf("%s:%s", orgID, blockID)])
}

// GetCommitRecord returns a commit for test assertions.
func (m *MockStore) GetCommitRecord(libraryID uuid.UUID, commitID string) *mockCommit {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.commits[fmt.Sprintf("%s:%s", libraryID, commitID)]
}

// GetFSObj returns an fs_object for test assertions.
func (m *MockStore) GetFSObj(libraryID uuid.UUID, fsID string) *mockFSObject {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.fsObjects[fmt.Sprintf("%s:%s", libraryID, fsID)]
}

// QueueLen returns the total number of items in the queue.
func (m *MockStore) QueueLen() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	total := 0
	for _, items := range m.queue {
		total += len(items)
	}
	return total
}

// QueueItems returns all queue items for an org.
func (m *MockStore) QueueItems(orgID uuid.UUID) []QueueItem {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]QueueItem{}, m.queue[orgID]...)
}

func (m *MockStore) IsOrgActive(orgID uuid.UUID) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.activeQueueOrgs[orgID]
	return ok
}

// GetShareLink returns a share link for test assertions.
func (m *MockStore) GetShareLink(shareToken string) *mockShareLink {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.shareLinks[shareToken]
}

// --- GCStore interface implementation ---

func (m *MockStore) EnqueueItem(orgID uuid.UUID, queuedAt time.Time, itemType ItemType, itemID string, libraryID uuid.UUID, storageClass string, retryCount int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	item := QueueItem{
		OrgID:                       orgID,
		QueuedAt:                    queuedAt,
		IdentityAt:                  queuedAt,
		RequiresLibraryDeletedCheck: false,
		ItemType:                    itemType,
		ItemID:                      itemID,
		LibraryID:                   libraryID,
		StorageClass:                storageClass,
		RetryCount:                  retryCount,
	}
	m.queue[orgID] = append(m.queue[orgID], item)
	m.upsertPendingItem(orgID, libraryID, itemType, itemID, queuedAt, nil)
	m.activeQueueOrgs[orgID] = time.Now().UTC()
	m.dirtyQueueOrgs[orgID] = time.Now().UTC()
	return nil
}

func (m *MockStore) EnqueueBatch(items []QueueItem) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.enqueueBatchErr != nil {
		return m.enqueueBatchErr
	}
	for _, item := range items {
		item.IdentityAt = effectiveIdentityAt(item.QueuedAt, item.IdentityAt)
		m.queue[item.OrgID] = append(m.queue[item.OrgID], item)
		m.upsertPendingItem(item.OrgID, item.LibraryID, item.ItemType, item.ItemID, item.IdentityAt, nil)
		m.activeQueueOrgs[item.OrgID] = time.Now().UTC()
		m.dirtyQueueOrgs[item.OrgID] = time.Now().UTC()
	}
	return nil
}

func (m *MockStore) QueueItemExists(orgID uuid.UUID, queuedAt time.Time, itemType ItemType, itemID string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, item := range m.queue[orgID] {
		if item.QueuedAt.Equal(queuedAt) && item.ItemType == itemType && item.ItemID == itemID {
			return true, nil
		}
	}
	return false, nil
}

func (m *MockStore) PendingItemExists(orgID, libraryID uuid.UUID, identityAt time.Time, itemType ItemType, itemID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	for key, expiresAt := range m.pendingItems {
		if key.OrgID != orgID || key.LibraryID != libraryID || key.ItemType != itemType || key.ItemID != itemID {
			continue
		}
		if expiresAt != nil && !expiresAt.After(now) {
			delete(m.pendingItems, key)
			continue
		}
		if identityAt.IsZero() || key.IdentityAt.Equal(identityAt) {
			return true, nil
		}
	}
	for _, item := range m.failedItems[orgID] {
		if item.ItemType == itemType && item.ItemID == itemID && item.LibraryID == libraryID && (identityAt.IsZero() || effectiveIdentityAt(item.QueuedAt, item.IdentityAt).Equal(identityAt)) {
			return true, nil
		}
	}
	return false, nil
}

func (m *MockStore) DequeueBatch(orgID uuid.UUID, batchSize int, cutoff time.Time) ([]QueueItem, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	items := m.queue[orgID]
	// Sort by QueuedAt ASC
	sort.Slice(items, func(i, j int) bool {
		return items[i].QueuedAt.Before(items[j].QueuedAt)
	})

	var result []QueueItem
	for _, item := range items {
		if !item.QueuedAt.After(cutoff) {
			result = append(result, item)
			if len(result) >= batchSize {
				break
			}
		}
	}
	return result, nil
}

func (m *MockStore) CompleteItem(orgID uuid.UUID, queuedAt time.Time, itemType ItemType, itemID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	items := m.queue[orgID]
	for i, item := range items {
		if item.QueuedAt.Equal(queuedAt) && item.ItemType == itemType && item.ItemID == itemID {
			m.queue[orgID] = append(items[:i], items[i+1:]...)
			m.deletePendingItem(orgID, item.LibraryID, itemType, itemID, effectiveIdentityAt(item.QueuedAt, item.IdentityAt))
			m.dirtyQueueOrgs[orgID] = time.Now().UTC()
			return nil
		}
	}
	return nil
}

func (m *MockStore) RequeueItem(orgID uuid.UUID, oldQueuedAt, newQueuedAt time.Time, itemType ItemType, itemID string, libraryID uuid.UUID, storageClass string, newRetryCount int, identityAt time.Time, requiresLibraryDeletedCheck bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.requeueItemErr != nil {
		return m.requeueItemErr
	}

	items := m.queue[orgID]
	for i, item := range items {
		if item.QueuedAt.Equal(oldQueuedAt) && item.ItemType == itemType && item.ItemID == itemID {
			// Remove the old item
			m.queue[orgID] = append(items[:i], items[i+1:]...)

			// Append the new recreated item
			newItem := item
			newItem.QueuedAt = newQueuedAt
			newItem.IdentityAt = effectiveIdentityAt(item.QueuedAt, identityAt)
			newItem.RequiresLibraryDeletedCheck = item.RequiresLibraryDeletedCheck
			newItem.RetryCount = newRetryCount
			m.queue[orgID] = append(m.queue[orgID], newItem)
			m.upsertPendingItem(orgID, libraryID, itemType, itemID, newItem.IdentityAt, nil)
			m.activeQueueOrgs[orgID] = time.Now().UTC()
			m.dirtyQueueOrgs[orgID] = time.Now().UTC()

			if m.requeueItemErrAfterMutate != nil {
				return m.requeueItemErrAfterMutate
			}
			return nil
		}
	}
	return nil
}

func (m *MockStore) FailItem(item QueueItem, failedAt time.Time, lastError, failureCode string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	items := m.queue[item.OrgID]
	for i, existing := range items {
		if existing.QueuedAt.Equal(item.QueuedAt) && existing.ItemType == item.ItemType && existing.ItemID == item.ItemID {
			m.queue[item.OrgID] = append(items[:i], items[i+1:]...)
			break
		}
	}
	m.failedItems[item.OrgID] = append(m.failedItems[item.OrgID], GCFailedItemInfo{
		OrgID:                       item.OrgID,
		FailedAt:                    failedAt,
		QueuedAt:                    item.QueuedAt,
		IdentityAt:                  effectiveIdentityAt(item.QueuedAt, item.IdentityAt),
		RequiresLibraryDeletedCheck: item.RequiresLibraryDeletedCheck,
		ItemType:                    item.ItemType,
		ItemID:                      item.ItemID,
		LibraryID:                   item.LibraryID,
		StorageClass:                item.StorageClass,
		RetryCount:                  item.RetryCount,
		LastError:                   lastError,
		FailureCode:                 failureCode,
	})
	expiresAt := failedAt.Add(gcFailedItemRetention)
	m.upsertPendingItem(item.OrgID, item.LibraryID, item.ItemType, item.ItemID, effectiveIdentityAt(item.QueuedAt, item.IdentityAt), &expiresAt)
	m.dirtyQueueOrgs[item.OrgID] = time.Now().UTC()
	return nil
}

func (m *MockStore) GetQueueSize(orgID uuid.UUID) (int, error) {
	m.mu.RLock()
	size := len(m.queue[orgID])
	hook := m.getQueueSizeHook
	m.mu.RUnlock()
	if hook != nil {
		hook(orgID, size)
	}
	return size, nil
}

func (m *MockStore) GetTotalQueueSize() (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	total := 0
	for _, items := range m.queue {
		total += len(items)
	}
	return total, nil
}

func (m *MockStore) GetTotalFailedItems() (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	total := 0
	for _, items := range m.failedItems {
		total += len(items)
	}
	return total, nil
}

func (m *MockStore) FailedItems(orgID uuid.UUID) []GCFailedItemInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := m.failedItems[orgID]
	result := make([]GCFailedItemInfo, len(items))
	copy(result, items)
	return result
}

func (m *MockStore) ListFailedItems(orgID uuid.UUID, limit int) ([]GCFailedItemInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := m.failedItems[orgID]
	result := make([]GCFailedItemInfo, len(items))
	copy(result, items)
	if limit > 0 && len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (m *MockStore) ListOrgsWithFailedItems(limit int) ([]GCFailedItemOrgInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	results := make([]GCFailedItemOrgInfo, 0)
	seen := make(map[uuid.UUID]struct{})
	for orgID, stats := range m.orgQueueStats {
		if stats.FailedDepth <= 0 {
			continue
		}
		results = append(results, GCFailedItemOrgInfo{
			OrgID:            orgID,
			OrgName:          m.orgNames[orgID],
			FailedItemsTotal: stats.FailedDepth,
			UpdatedAt:        stats.UpdatedAt,
		})
		seen[orgID] = struct{}{}
	}
	for orgID, items := range m.failedItems {
		if _, ok := seen[orgID]; ok || len(items) == 0 {
			continue
		}
		updatedAt := time.Time{}
		for _, item := range items {
			if item.FailedAt.After(updatedAt) {
				updatedAt = item.FailedAt
			}
		}
		results = append(results, GCFailedItemOrgInfo{
			OrgID:            orgID,
			OrgName:          m.orgNames[orgID],
			FailedItemsTotal: len(items),
			UpdatedAt:        updatedAt,
		})
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].FailedItemsTotal != results[j].FailedItemsTotal {
			return results[i].FailedItemsTotal > results[j].FailedItemsTotal
		}
		if !results[i].UpdatedAt.Equal(results[j].UpdatedAt) {
			return results[i].UpdatedAt.After(results[j].UpdatedAt)
		}
		return results[i].OrgID.String() < results[j].OrgID.String()
	})
	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

func (m *MockStore) DeleteFailedItem(orgID uuid.UUID, failedAt time.Time, itemType ItemType, itemID string) error {
	m.mu.RLock()
	hook := m.dlqOpHook
	m.mu.RUnlock()
	if hook != nil {
		hook(orgID, "delete")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	items := m.failedItems[orgID]
	for i, item := range items {
		if item.FailedAt.Equal(failedAt) && item.ItemType == itemType && item.ItemID == itemID {
			m.failedItems[orgID] = append(items[:i], items[i+1:]...)
			m.deletePendingItem(orgID, item.LibraryID, itemType, itemID, effectiveIdentityAt(item.QueuedAt, item.IdentityAt))
			m.dirtyQueueOrgs[orgID] = time.Now().UTC()
			return nil
		}
	}
	return nil
}

func (m *MockStore) RequeueFailedItem(orgID uuid.UUID, failedAt time.Time, itemType ItemType, itemID string, queuedAt time.Time) error {
	m.mu.RLock()
	hook := m.dlqOpHook
	m.mu.RUnlock()
	if hook != nil {
		hook(orgID, "requeue")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	items := m.failedItems[orgID]
	for i, item := range items {
		if item.FailedAt.Equal(failedAt) && item.ItemType == itemType && item.ItemID == itemID {
			m.queue[orgID] = append(m.queue[orgID], QueueItem{
				OrgID:                       orgID,
				QueuedAt:                    item.QueuedAt,
				IdentityAt:                  effectiveIdentityAt(item.QueuedAt, item.IdentityAt),
				RequiresLibraryDeletedCheck: item.RequiresLibraryDeletedCheck,
				ItemType:                    item.ItemType,
				ItemID:                      item.ItemID,
				LibraryID:                   item.LibraryID,
				StorageClass:                item.StorageClass,
				RetryCount:                  0,
			})
			m.upsertPendingItem(orgID, item.LibraryID, itemType, itemID, effectiveIdentityAt(item.QueuedAt, item.IdentityAt), nil)
			m.failedItems[orgID] = append(items[:i], items[i+1:]...)
			m.activeQueueOrgs[orgID] = queuedAt
			m.dirtyQueueOrgs[orgID] = queuedAt
			return nil
		}
	}
	return fmt.Errorf("failed item not found")
}

func (m *MockStore) ListOrgsWithQueuedItems() ([]uuid.UUID, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.useActiveQueueOrgsForListing {
		orgs := make([]uuid.UUID, 0, len(m.activeQueueOrgs))
		for orgID := range m.activeQueueOrgs {
			orgs = append(orgs, orgID)
		}
		return orgs, nil
	}

	var orgs []uuid.UUID
	for orgID, items := range m.queue {
		if len(items) > 0 {
			orgs = append(orgs, orgID)
		}
	}
	return orgs, nil
}

func (m *MockStore) ListOrgsWithQueuedSnapshots(limit int) ([]uuid.UUID, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	orgs := make([]uuid.UUID, 0)
	for orgID, stats := range m.orgQueueStats {
		if stats.QueueDepth <= 0 {
			continue
		}
		orgs = append(orgs, orgID)
		if limit > 0 && len(orgs) >= limit {
			break
		}
	}
	return orgs, nil
}

func (m *MockStore) MarkOrgActive(orgID uuid.UUID, activeAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.activeQueueOrgs[orgID] = activeAt
	return nil
}

func (m *MockStore) RemoveOrgFromActiveSet(orgID uuid.UUID, activeBefore time.Time) error {
	m.mu.RLock()
	hook := m.removeActiveOrgHook
	m.mu.RUnlock()
	if hook != nil {
		hook(orgID, activeBefore)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.activeQueueOrgs[orgID]; ok && existing.Before(activeBefore) {
		delete(m.activeQueueOrgs, orgID)
	}
	return nil
}

func (m *MockStore) MarkOrgDirty(orgID uuid.UUID, dirtyAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dirtyQueueOrgs[orgID] = dirtyAt
	return nil
}

func (m *MockStore) ListDirtyOrgs(limit int) ([]GCDirtyOrg, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	orgs := make([]GCDirtyOrg, 0, len(m.dirtyQueueOrgs))
	for orgID, markedAt := range m.dirtyQueueOrgs {
		orgs = append(orgs, GCDirtyOrg{OrgID: orgID, MarkedAt: markedAt})
	}
	sort.Slice(orgs, func(i, j int) bool {
		return strings.Compare(orgs[i].OrgID.String(), orgs[j].OrgID.String()) < 0
	})
	if limit > 0 && len(orgs) > limit {
		orgs = orgs[:limit]
	}
	return orgs, nil
}

func (m *MockStore) ClearDirtyOrg(orgID uuid.UUID, dirtyBefore time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.dirtyQueueOrgs[orgID]; ok && !existing.After(dirtyBefore) {
		delete(m.dirtyQueueOrgs, orgID)
	}
	return nil
}

func (m *MockStore) GetOrgQueueStats(orgID uuid.UUID) (GCOrgStats, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	stats, ok := m.orgQueueStats[orgID]
	if !ok {
		return GCOrgStats{OrgID: orgID}, nil
	}
	return stats, nil
}

func (m *MockStore) SaveOrgQueueStats(stats GCOrgStats) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.orgQueueStats[stats.OrgID] = stats
	return nil
}

func (m *MockStore) RecountOrgQueueDepth(orgID uuid.UUID) (int, error) {
	m.mu.RLock()
	depth := len(m.queue[orgID])
	hook := m.recountQueueDepthHook
	m.mu.RUnlock()
	if hook != nil {
		hook(orgID, depth)
	}
	return depth, nil
}

func (m *MockStore) RecountOrgFailedDepth(orgID uuid.UUID) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.failedItems[orgID]), nil
}

func (m *MockStore) GetOldestQueuedAt(orgID uuid.UUID) (*time.Time, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := m.queue[orgID]
	if len(items) == 0 {
		return nil, nil
	}
	oldest := items[0].QueuedAt
	for _, item := range items[1:] {
		if item.QueuedAt.Before(oldest) {
			oldest = item.QueuedAt
		}
	}
	oldestCopy := oldest
	return &oldestCopy, nil
}

func (m *MockStore) SumOrgQueueStats() (int, int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	totalQueue := 0
	totalFailed := 0
	for _, stats := range m.orgQueueStats {
		totalQueue += stats.QueueDepth
		totalFailed += stats.FailedDepth
	}
	return totalQueue, totalFailed, nil
}

func (m *MockStore) GetUserDeletedAt(orgID, userID uuid.UUID) (*time.Time, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	user, ok := m.users[fmt.Sprintf("%s:%s", orgID, userID)]
	if !ok || user.Status != "deleted" || user.DeletedAt == nil {
		return nil, nil
	}
	deletedAt := *user.DeletedAt
	return &deletedAt, nil
}

func (m *MockStore) GetLibraryDeletedAt(libraryID uuid.UUID) (*time.Time, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	marker, ok := m.deletedLibraries[libraryID]
	if !ok {
		return nil, nil
	}
	deletedAt := marker.DeletedAt
	return &deletedAt, nil
}

func (m *MockStore) GetOrgDeletedAt(orgID uuid.UUID) (*time.Time, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	deletedAt, ok := m.orgDeletedAt[orgID]
	status := m.orgStatus[orgID]
	if !ok || (status != "deleted" && status != "purging") {
		return nil, nil
	}
	deletedAtCopy := deletedAt
	return &deletedAtCopy, nil
}

func (m *MockStore) BlockExists(orgID uuid.UUID, blockID string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.getBlockRefCountErr != nil {
		return false, m.getBlockRefCountErr
	}
	_, ok := m.blocks[fmt.Sprintf("%s:%s", orgID, blockID)]
	return ok, nil
}

func (m *MockStore) BlockHasReferences(orgID uuid.UUID, blockID string) (bool, error) {
	m.mu.RLock()
	current := len(m.blockReferences[fmt.Sprintf("%s:%s", orgID, blockID)]) > 0
	hook := m.blockHasReferencesHook
	m.mu.RUnlock()
	if hook != nil {
		return hook(orgID, blockID, current)
	}
	return current, nil
}

func (m *MockStore) RemoveBlockReference(orgID uuid.UUID, blockID, referrer string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%s:%s", orgID, blockID)
	if refs, ok := m.blockReferences[key]; ok {
		delete(refs, referrer)
		if len(refs) == 0 {
			delete(m.blockReferences, key)
		}
	}
	return nil
}

// AddBlockReferenceForTest registers a reference row for tests exercising the
// row-per-reference model directly.
func (m *MockStore) AddBlockReferenceForTest(orgID uuid.UUID, blockID, referrer string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%s:%s", orgID, blockID)
	if m.blockReferences[key] == nil {
		m.blockReferences[key] = make(map[string]struct{})
	}
	m.blockReferences[key][referrer] = struct{}{}
}

// AddFSObjectReferenceForTest registers the permanent reference an fs_object holds
// on a block, using the same referrer the GC worker removes when it sweeps that
// fs_object. Use it to seed "block referenced by fs_object" relationships.
func (m *MockStore) AddFSObjectReferenceForTest(orgID uuid.UUID, blockID string, libID uuid.UUID, fsID string) {
	m.AddBlockReferenceForTest(orgID, blockID, db.BlockReferrerForFSObject(libID.String(), fsID))
}

func (m *MockStore) ResolveBlockIDs(orgID uuid.UUID, blockIDs []string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	resolved := make([]string, len(blockIDs))
	copy(resolved, blockIDs)
	for i, blockID := range blockIDs {
		if len(blockID) != 40 {
			continue
		}
		if internalID, ok := m.mappings[fmt.Sprintf("%s:%s", orgID, blockID)]; ok && internalID != "" {
			resolved[i] = internalID
		}
	}
	return resolved, nil
}

func (m *MockStore) EnsureBlockGCCandidate(orgID uuid.UUID, blockID, storageClass string, candidateAt time.Time) (time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%s:%s", orgID, blockID)
	if existing, ok := m.blockGCCandidates[key]; ok {
		if candidateAt = candidateAt.UTC(); !candidateAt.IsZero() && candidateAt.Before(existing.CandidateAt) {
			delete(m.blockGCCandidateProjections, newMockBlockGCCandidateProjectionKey(existing.OrgID, existing.BlockID, existing.CandidateAt))
			existing.CandidateAt = candidateAt
		}
		m.upsertBlockGCCandidateProjection(existing)
		return existing.CandidateAt, m.ensureBlockGCCandidateErrAfterMutate
	}
	candidate := &mockBlockGCCandidate{
		OrgID:        orgID,
		BlockID:      blockID,
		StorageClass: storageClass,
		CandidateAt:  candidateAt.UTC(),
	}
	m.blockGCCandidates[key] = candidate
	m.upsertBlockGCCandidateProjection(candidate)
	return candidate.CandidateAt, m.ensureBlockGCCandidateErrAfterMutate
}

func (m *MockStore) DeleteBlockGCCandidate(orgID uuid.UUID, blockID string, candidateAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%s:%s", orgID, blockID)
	if candidateAt.IsZero() {
		if existing, ok := m.blockGCCandidates[key]; ok {
			candidateAt = existing.CandidateAt
		}
	}
	delete(m.blockGCCandidates, key)
	if !candidateAt.IsZero() {
		delete(m.blockGCCandidateProjections, newMockBlockGCCandidateProjectionKey(orgID, blockID, candidateAt))
	}
	return nil
}

func (m *MockStore) ListBlockGCCandidatesByDay(day time.Time, bucket int) ([]BlockGCCandidateInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	targetDay := db.GCProjectionUTCDate(day)
	var candidates []BlockGCCandidateInfo
	for key, candidate := range m.blockGCCandidateProjections {
		if !key.CandidateDay.Equal(targetDay) {
			continue
		}
		if key.Bucket != bucket {
			continue
		}
		candidates = append(candidates, candidate)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if !candidates[i].CandidateAt.Equal(candidates[j].CandidateAt) {
			return candidates[i].CandidateAt.Before(candidates[j].CandidateAt)
		}
		if candidates[i].OrgID != candidates[j].OrgID {
			return candidates[i].OrgID.String() < candidates[j].OrgID.String()
		}
		return candidates[i].BlockID < candidates[j].BlockID
	})
	return candidates, nil
}

func (m *MockStore) ListProvisionalBlockRefExpiriesByDay(day time.Time, bucket int) ([]ProvisionalBlockRefExpiryInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	targetDay := db.GCProjectionUTCDate(day)
	var expiries []ProvisionalBlockRefExpiryInfo
	for key, expiry := range m.provisionalBlockRefExpiryProjections {
		if !key.ExpiryDay.Equal(targetDay) {
			continue
		}
		if key.Bucket != bucket {
			continue
		}
		expiries = append(expiries, expiry)
	}
	sort.Slice(expiries, func(i, j int) bool {
		if !expiries[i].ExpiresAt.Equal(expiries[j].ExpiresAt) {
			return expiries[i].ExpiresAt.Before(expiries[j].ExpiresAt)
		}
		if expiries[i].OrgID != expiries[j].OrgID {
			return expiries[i].OrgID.String() < expiries[j].OrgID.String()
		}
		if expiries[i].BlockID != expiries[j].BlockID {
			return expiries[i].BlockID < expiries[j].BlockID
		}
		return expiries[i].Referrer < expiries[j].Referrer
	})
	return expiries, nil
}

func (m *MockStore) GetProvisionalBlockRefExpiry(orgID uuid.UUID, blockID, referrer string) (ProvisionalBlockRefExpiryInfo, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	key := fmt.Sprintf("%s:%s:%s", orgID, blockID, referrer)
	expiry, ok := m.provisionalBlockRefExpiries[key]
	if !ok {
		return ProvisionalBlockRefExpiryInfo{}, false, nil
	}
	return ProvisionalBlockRefExpiryInfo{
		OrgID:        expiry.OrgID,
		BlockID:      expiry.BlockID,
		Referrer:     expiry.Referrer,
		StorageClass: expiry.StorageClass,
		ExpiresAt:    expiry.ExpiresAt.UTC(),
	}, true, nil
}

func (m *MockStore) DeleteProvisionalBlockRefExpiryProjection(orgID uuid.UUID, blockID, referrer string, expiresAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.provisionalBlockRefExpiryProjections, newMockProvisionalBlockRefExpiryProjectionKey(orgID, blockID, referrer, expiresAt))
	return nil
}

func (m *MockStore) DeleteProvisionalBlockRefExpiry(orgID uuid.UUID, blockID, referrer string, expiresAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%s:%s:%s", orgID, blockID, referrer)
	if expiresAt.IsZero() {
		if existing, ok := m.provisionalBlockRefExpiries[key]; ok {
			expiresAt = existing.ExpiresAt
		}
	}
	delete(m.provisionalBlockRefExpiries, key)
	if !expiresAt.IsZero() {
		delete(m.provisionalBlockRefExpiryProjections, newMockProvisionalBlockRefExpiryProjectionKey(orgID, blockID, referrer, expiresAt))
	}
	return nil
}

func (m *MockStore) ClaimBlockDelete(orgID uuid.UUID, blockID, claimID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := fmt.Sprintf("%s:%s", orgID, blockID)
	b, ok := m.blocks[key]
	if !ok {
		return false, nil
	}
	if b.GCState == db.BlockGCStateDeleting {
		return b.GCClaimID == claimID, nil
	}
	b.GCState = db.BlockGCStateDeleting
	b.GCClaimID = claimID
	return true, nil
}

func (m *MockStore) ReleaseBlockClaim(orgID uuid.UUID, blockID, claimID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if b, ok := m.blocks[fmt.Sprintf("%s:%s", orgID, blockID)]; ok {
		if b.GCState != db.BlockGCStateDeleting || b.GCClaimID != claimID {
			return fmt.Errorf("block delete claim release not applied for %s", blockID)
		}
		b.GCState = ""
		b.GCClaimID = ""
		return nil
	}
	return fmt.Errorf("block delete claim release not applied for %s", blockID)
}

func (m *MockStore) FinalizeBlockDelete(orgID uuid.UUID, blockID, claimID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := fmt.Sprintf("%s:%s", orgID, blockID)
	if b, ok := m.blocks[key]; !ok || b.GCState != db.BlockGCStateDeleting || b.GCClaimID != claimID {
		return fmt.Errorf("block delete finalize not applied for %s", blockID)
	}
	delete(m.blocks, key)
	return nil
}

func (m *MockStore) ListBlockMappingsByInternalID(orgID uuid.UUID, internalID string) ([]BlockMapping, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := fmt.Sprintf("%s:", orgID)
	var mappings []BlockMapping
	for key, intID := range m.mappings {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix && intID == internalID {
			extID := key[len(prefix):]
			mappings = append(mappings, BlockMapping{ExternalID: extID, InternalID: intID})
		}
	}
	return mappings, nil
}

func (m *MockStore) DeleteBlockMapping(orgID uuid.UUID, externalID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := fmt.Sprintf("%s:%s", orgID, externalID)
	delete(m.mappings, key)
	return nil
}

func (m *MockStore) GetCommit(libraryID uuid.UUID, commitID string) (CommitInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	key := fmt.Sprintf("%s:%s", libraryID, commitID)
	c, ok := m.commits[key]
	if !ok {
		return CommitInfo{}, fmt.Errorf("commit not found: %s", commitID)
	}
	return CommitInfo{CommitID: c.CommitID, RootFSID: c.RootFSID}, nil
}

func (m *MockStore) DeleteCommit(libraryID uuid.UUID, commitID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := fmt.Sprintf("%s:%s", libraryID, commitID)
	delete(m.commits, key)
	return nil
}

func (m *MockStore) GetFSObject(libraryID uuid.UUID, fsID string) (FSObjectInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	key := fmt.Sprintf("%s:%s", libraryID, fsID)
	if err := m.getFSObjectErrors[key]; err != nil {
		return FSObjectInfo{}, err
	}
	obj, ok := m.fsObjects[key]
	if !ok {
		return FSObjectInfo{}, fmt.Errorf("fs_object not found: %s", fsID)
	}
	return FSObjectInfo{
		FSID:       obj.FSID,
		ObjType:    obj.ObjType,
		BlockIDs:   obj.BlockIDs,
		DirEntries: obj.DirEntries,
	}, nil
}

func (m *MockStore) SetGetFSObjectError(libraryID uuid.UUID, fsID string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%s:%s", libraryID, fsID)
	if err == nil {
		delete(m.getFSObjectErrors, key)
		return
	}
	m.getFSObjectErrors[key] = err
}

func (m *MockStore) DeleteFSObject(libraryID uuid.UUID, fsID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := fmt.Sprintf("%s:%s", libraryID, fsID)
	delete(m.fsObjects, key)
	return nil
}

func (m *MockStore) GetLibraryStorageClass(orgID, libraryID uuid.UUID) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	lib, ok := m.libraries[libraryID]
	if !ok {
		return "", fmt.Errorf("library not found: %s", libraryID)
	}
	return lib.StorageClass, nil
}

func (m *MockStore) ListCommitsForLibrary(libraryID uuid.UUID) ([]CommitInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := fmt.Sprintf("%s:", libraryID)
	var commits []CommitInfo
	for key, c := range m.commits {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix {
			commits = append(commits, CommitInfo{CommitID: c.CommitID, RootFSID: c.RootFSID})
		}
	}
	return commits, nil
}

func (m *MockStore) ListFSObjectsForLibrary(libraryID uuid.UUID) ([]FSObjectInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := fmt.Sprintf("%s:", libraryID)
	var objects []FSObjectInfo
	for key, obj := range m.fsObjects {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix {
			objects = append(objects, FSObjectInfo{
				FSID:       obj.FSID,
				ObjType:    obj.ObjType,
				BlockIDs:   obj.BlockIDs,
				DirEntries: obj.DirEntries,
			})
		}
	}
	return objects, nil
}

func (m *MockStore) ListOrganizations() ([]uuid.UUID, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]uuid.UUID{}, m.organizations...), nil
}

func (m *MockStore) ListExpiredShareLinks() ([]ExpiredShareLinkInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.listExpiredShareLinksErr != nil {
		return nil, m.listExpiredShareLinksErr
	}

	now := time.Now()
	var links []ExpiredShareLinkInfo
	for _, sl := range m.shareLinks {
		if sl.ExpiresAt.IsZero() || sl.ExpiresAt.After(now) {
			continue
		}
		links = append(links, ExpiredShareLinkInfo{
			ShareToken: sl.ShareToken,
			OrgID:      sl.OrgID,
			LibraryID:  sl.LibraryID,
			CreatedBy:  sl.CreatedBy,
			CreatedAt:  sl.CreatedAt,
			LinkType:   sl.LinkType,
			ExpiresAt:  sl.ExpiresAt,
		})
	}
	return links, nil
}

func (m *MockStore) ListDistinctCommitLibraries() ([]uuid.UUID, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	seen := make(map[uuid.UUID]bool)
	for libraryID := range m.libraries {
		seen[libraryID] = true
	}
	for libraryID := range m.deletedLibraries {
		seen[libraryID] = true
	}

	var result []uuid.UUID
	for id := range seen {
		result = append(result, id)
	}
	return result, nil
}

func (m *MockStore) ListDistinctFSObjectLibraries() ([]uuid.UUID, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	seen := make(map[uuid.UUID]bool)
	for libraryID := range m.libraries {
		seen[libraryID] = true
	}
	for libraryID := range m.deletedLibraries {
		seen[libraryID] = true
	}

	var result []uuid.UUID
	for id := range seen {
		result = append(result, id)
	}
	return result, nil
}

func (m *MockStore) LibraryExists(libraryID uuid.UUID) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.libraries[libraryID]
	return ok, nil
}

func (m *MockStore) FindOrgForLibrary(libraryID uuid.UUID) (uuid.UUID, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	lib, ok := m.libraries[libraryID]
	if ok {
		return lib.OrgID, nil
	}
	deleted, ok := m.deletedLibraries[libraryID]
	if ok {
		return deleted.OrgID, nil
	}
	return uuid.Nil, fmt.Errorf("library not found: %s", libraryID)
}

func (m *MockStore) ListCommitIDsForLibrary(libraryID uuid.UUID) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := fmt.Sprintf("%s:", libraryID)
	var ids []string
	for key, c := range m.commits {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix {
			ids = append(ids, c.CommitID)
		}
	}
	return ids, nil
}

func (m *MockStore) ListFSObjectIDsForLibrary(libraryID uuid.UUID) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := fmt.Sprintf("%s:", libraryID)
	var ids []string
	for key, obj := range m.fsObjects {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix {
			ids = append(ids, obj.FSID)
		}
	}
	return ids, nil
}

func (m *MockStore) ReconcilePendingStorageCounters() (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.storageCounterReconciliations) == 0 {
		return 0, nil
	}

	expected := make(map[string]traffic.StorageSnapshot, len(m.storageCounterReconciliations))
	for _, lib := range m.libraries {
		if !lib.DeletedAt.IsZero() {
			continue
		}
		// Match store_cassandra.go semantics: aggregate reconciliation reads
		// from canonical libraries.size_bytes / libraries.file_count, not from
		// the (possibly drifted) lib-scope storage_counters snapshot.
		libSnapshot := traffic.StorageSnapshot{BytesUsed: lib.SizeBytes, FileCount: lib.FileCount}
		if libSnapshot.BytesUsed == 0 && libSnapshot.FileCount == 0 {
			continue
		}

		if _, ok := m.storageCounterReconciliations[traffic.PlatformStorageScope()]; ok {
			snap := expected[traffic.PlatformStorageScope()]
			snap.BytesUsed += libSnapshot.BytesUsed
			snap.FileCount += libSnapshot.FileCount
			expected[traffic.PlatformStorageScope()] = snap
		}

		orgScope := traffic.OrganizationStorageScope(lib.OrgID.String())
		if _, ok := m.storageCounterReconciliations[orgScope]; ok {
			snap := expected[orgScope]
			snap.BytesUsed += libSnapshot.BytesUsed
			snap.FileCount += libSnapshot.FileCount
			expected[orgScope] = snap
		}

		userScope := traffic.UserStorageScope(lib.OrgID.String(), lib.OwnerID.String())
		if _, ok := m.storageCounterReconciliations[userScope]; ok {
			snap := expected[userScope]
			snap.BytesUsed += libSnapshot.BytesUsed
			snap.FileCount += libSnapshot.FileCount
			expected[userScope] = snap
		}
	}

	reconciled := 0
	for scope := range m.storageCounterReconciliations {
		m.storageSnapshots[scope] = expected[scope]
		delete(m.storageCounterReconciliations, scope)
		reconciled++
	}
	return reconciled, nil
}

func (m *MockStore) ListLibrariesWithVersionTTL() ([]LibraryTTLInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var results []LibraryTTLInfo
	for _, lib := range m.libraries {
		if lib.VersionTTLDays > 0 {
			results = append(results, LibraryTTLInfo{
				OrgID:          lib.OrgID,
				LibraryID:      lib.LibraryID,
				HeadCommitID:   lib.HeadCommitID,
				VersionTTLDays: lib.VersionTTLDays,
			})
		}
	}
	return results, nil
}

func (m *MockStore) ListLibrariesWithAutoDelete() ([]LibraryAutoDeleteInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var results []LibraryAutoDeleteInfo
	for _, lib := range m.libraries {
		if lib.AutoDeleteDays > 0 {
			results = append(results, LibraryAutoDeleteInfo{
				OrgID:          lib.OrgID,
				LibraryID:      lib.LibraryID,
				HeadCommitID:   lib.HeadCommitID,
				AutoDeleteDays: lib.AutoDeleteDays,
			})
		}
	}
	return results, nil
}

func (m *MockStore) ListCommitsWithTimestamps(libraryID uuid.UUID) ([]CommitWithTimestamp, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := fmt.Sprintf("%s:", libraryID)
	var commits []CommitWithTimestamp
	for key, c := range m.commits {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix {
			commits = append(commits, CommitWithTimestamp{
				CommitID:  c.CommitID,
				ParentID:  c.ParentID,
				RootFSID:  c.RootFSID,
				CreatedAt: c.CreatedAt,
			})
		}
	}
	return commits, nil
}

func (m *MockStore) DeleteShareLink(shareToken string, orgID uuid.UUID, libraryID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.shareLinks, shareToken)
	return nil
}

func (m *MockStore) DeleteExpiredShareLink(link ExpiredShareLinkInfo) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.deleteExpiredShareLinkErr != nil {
		return m.deleteExpiredShareLinkErr
	}
	delete(m.shareLinks, link.ShareToken)
	return nil
}

// --- Expired shares ---

func (m *MockStore) ListExpiredShares() ([]ExpiredShareInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.listExpiredSharesErr != nil {
		return nil, m.listExpiredSharesErr
	}

	now := time.Now()
	var results []ExpiredShareInfo
	for _, s := range m.shares {
		if !s.ExpiresAt.IsZero() && s.ExpiresAt.Before(now) {
			results = append(results, ExpiredShareInfo{
				OrgID:        s.OrgID,
				LibraryID:    s.LibraryID,
				ShareID:      s.ShareID,
				SharedBy:     s.SharedBy,
				SharedTo:     s.SharedTo,
				SharedToType: s.SharedToType,
				CreatedAt:    s.CreatedAt,
				ExpiresAt:    s.ExpiresAt,
			})
		}
	}
	return results, nil
}

func (m *MockStore) DeleteShare(libraryID, shareID uuid.UUID) error {
	if m.deleteShareErr != nil {
		return m.deleteShareErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%s:%s", libraryID, shareID)
	delete(m.shares, key)
	return nil
}

func (m *MockStore) DeleteExpiredShare(share ExpiredShareInfo) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.deleteExpiredShareErr != nil {
		return m.deleteExpiredShareErr
	}
	key := fmt.Sprintf("%s:%s", share.LibraryID, share.ShareID)
	delete(m.shares, key)
	return nil
}

// --- Expired restore jobs ---

func (m *MockStore) ListExpiredRestoreJobs() ([]ExpiredRestoreJobInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	now := time.Now()
	var results []ExpiredRestoreJobInfo
	for _, j := range m.restoreJobs {
		if j.Status == "completed" || j.Status == "failed" || (!j.ExpiresAt.IsZero() && j.ExpiresAt.Before(now)) {
			results = append(results, ExpiredRestoreJobInfo{
				OrgID:     j.OrgID,
				LibraryID: j.LibraryID,
				JobID:     j.JobID,
				Status:    j.Status,
				ExpiresAt: j.ExpiresAt,
			})
		}
	}
	return results, nil
}

func (m *MockStore) DeleteRestoreJob(orgID, libraryID, jobID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%s:%s:%s", orgID, libraryID, jobID)
	delete(m.restoreJobs, key)
	return nil
}

// HasShare returns true if the canonical share row still exists.
func (m *MockStore) HasShare(libraryID, shareID uuid.UUID) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.shares[fmt.Sprintf("%s:%s", libraryID, shareID)]
	return ok
}

// --- Library artifact cleanup ---

func (m *MockStore) ListSharesByLibrary(libraryID uuid.UUID) ([]ShareInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := fmt.Sprintf("%s:", libraryID)
	var results []ShareInfo
	for key, s := range m.shares {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix {
			results = append(results, ShareInfo{LibraryID: s.LibraryID, ShareID: s.ShareID, SharedTo: s.SharedTo})
		}
	}
	return results, nil
}

func (m *MockStore) ListRepoTagsByLibrary(libraryID uuid.UUID) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := fmt.Sprintf("%s:", libraryID)
	var results []string
	for key := range m.repoTags {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix {
			results = append(results, key[len(prefix):])
		}
	}
	return results, nil
}

func (m *MockStore) DeleteRepoTag(libraryID uuid.UUID, tagID int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%s:%d", libraryID, tagID)
	delete(m.repoTags, key)
	return nil
}

func (m *MockStore) ListFileTagsByLibrary(libraryID uuid.UUID) ([]FileTagInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := fmt.Sprintf("%s:", libraryID)
	var results []FileTagInfo
	for key, ft := range m.fileTags {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix {
			results = append(results, FileTagInfo{
				RepoID:    ft.RepoID,
				FilePath:  ft.FilePath,
				TagID:     ft.TagID,
				FileTagID: ft.FileTagID,
			})
		}
	}
	return results, nil
}

func (m *MockStore) DeleteFileTag(libraryID uuid.UUID, filePath string, tagID int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%s:%s:%d", libraryID, filePath, tagID)
	delete(m.fileTags, key)
	return nil
}

func (m *MockStore) DeleteFileTagByID(libraryID uuid.UUID, fileTagID int) error {
	return nil // Mock doesn't have secondary index
}

func (m *MockStore) ListRepoAPITokensByLibrary(libraryID uuid.UUID) ([]RepoAPITokenInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	prefix := fmt.Sprintf("%s:", libraryID)
	var results []RepoAPITokenInfo
	for key, t := range m.apiTokens {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix {
			results = append(results, RepoAPITokenInfo{RepoID: t.RepoID, AppName: t.AppName, APIToken: t.APIToken})
		}
	}
	return results, nil
}

func (m *MockStore) DeleteRepoAPIToken(libraryID uuid.UUID, appName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%s:%s", libraryID, appName)
	delete(m.apiTokens, key)
	return nil
}

func (m *MockStore) DeleteRepoAPITokenByToken(apiToken string) error {
	return nil // Mock doesn't have reverse lookup
}

func (m *MockStore) DeleteLockedFilesByLibrary(libraryID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	prefix := fmt.Sprintf("%s:", libraryID)
	for key := range m.lockedFiles {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix {
			delete(m.lockedFiles, key)
		}
	}
	return nil
}

func (m *MockStore) DeleteShareLinksByLibrary(orgID, libraryID uuid.UUID) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// In mock, iterate share links and delete those matching libraryID
	// (simplified — real impl uses share_links_by_library table)
	return nil, nil
}

// --- New cleanup methods ---

func (m *MockStore) DeleteStarredFilesByLibrary(libraryID uuid.UUID) error   { return nil }
func (m *MockStore) DeleteMonitoredReposByLibrary(libraryID uuid.UUID) error { return nil }
func (m *MockStore) DeleteRestoreJobsByLibrary(orgID, libraryID uuid.UUID) error {
	if m.deleteRestoreJobsByLibraryErr != nil {
		return m.deleteRestoreJobsByLibraryErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	prefix := fmt.Sprintf("%s:%s:", orgID, libraryID)
	for key := range m.restoreJobs {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			delete(m.restoreJobs, key)
		}
	}
	return nil
}
func (m *MockStore) DeleteRepoTagCounters(libraryID uuid.UUID) error   { return nil }
func (m *MockStore) DeleteFileTagCounters(libraryID uuid.UUID) error   { return nil }
func (m *MockStore) DeleteRepoTagFileCounts(libraryID uuid.UUID) error { return nil }
func (m *MockStore) ListSharesByGroup(groupID uuid.UUID) ([]GroupShareInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []GroupShareInfo
	for _, share := range m.shares {
		if share.SharedToType != "group" || share.SharedTo != groupID {
			continue
		}
		result = append(result, GroupShareInfo{
			LibraryID:    share.LibraryID,
			ShareID:      share.ShareID,
			SharedTo:     share.SharedTo,
			SharedToType: share.SharedToType,
			OrgID:        share.OrgID,
		})
	}
	return result, nil
}
func (m *MockStore) ListAllGroupShares() ([]GroupShareInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []GroupShareInfo
	for _, share := range m.shares {
		if share.SharedToType != "group" {
			continue
		}
		result = append(result, GroupShareInfo{
			LibraryID:    share.LibraryID,
			ShareID:      share.ShareID,
			SharedTo:     share.SharedTo,
			SharedToType: share.SharedToType,
			OrgID:        share.OrgID,
		})
	}
	return result, nil
}
func (m *MockStore) GroupExists(orgID, groupID uuid.UUID) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.groups[fmt.Sprintf("%s:%s", orgID, groupID)], nil
}
func (m *MockStore) WriteAuditLog(entry AuditLogEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.auditLog = append(m.auditLog, entry)
	return nil
}

// --- Library trash auto-purge (Fase 3) ---

func (m *MockStore) ListExpiredDeletedLibraries(retentionDays int) ([]DeletedLibraryInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	var result []DeletedLibraryInfo
	for _, dl := range m.deletedLibraries {
		if dl.DeletedAt.Before(cutoff) {
			result = append(result, DeletedLibraryInfo{
				OrgID:        dl.OrgID,
				LibraryID:    dl.LibraryID,
				StorageClass: dl.StorageClass,
				DeletedAt:    dl.DeletedAt,
			})
		}
	}
	return result, nil
}
func (m *MockStore) HardDeleteLibrary(orgID, libraryID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.libraries, libraryID)
	delete(m.deletedLibraries, libraryID)
	return nil
}

// --- User cascade (Fase 1) ---

func (m *MockStore) ListDeletedUsersExpired(graceDays int) ([]DeletedUserInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.listDeletedUsersExpiredErr != nil {
		return nil, m.listDeletedUsersExpiredErr
	}
	cutoff := time.Now().AddDate(0, 0, -graceDays)
	var result []DeletedUserInfo
	for _, u := range m.users {
		if u.Status == "deleted" && u.DeletedAt != nil && u.DeletedAt.Before(cutoff) {
			result = append(result, DeletedUserInfo{
				OrgID:     u.OrgID,
				UserID:    u.UserID,
				Email:     u.Email,
				DeletedAt: *u.DeletedAt,
			})
		}
	}
	return result, nil
}
func (m *MockStore) ListLibrariesByOwner(orgID, ownerID uuid.UUID) ([]uuid.UUID, error) {
	if m.listLibrariesByOwnerErr != nil {
		return nil, m.listLibrariesByOwnerErr
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []uuid.UUID
	for _, lib := range m.libraries {
		if lib.OrgID == orgID && lib.OwnerID == ownerID {
			result = append(result, lib.LibraryID)
		}
	}
	return result, nil
}
func (m *MockStore) SoftDeleteLibrary(orgID, libraryID, deletedBy uuid.UUID) error {
	if m.softDeleteLibraryErr != nil {
		return m.softDeleteLibraryErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	lib, ok := m.libraries[libraryID]
	if !ok {
		return nil
	}
	lib.DeletedAt = time.Now()
	m.deletedLibraries[libraryID] = &mockDeletedLibrary{
		OrgID:        orgID,
		LibraryID:    libraryID,
		StorageClass: lib.StorageClass,
		DeletedAt:    lib.DeletedAt,
	}
	m.storageCounterReconciliations[traffic.PlatformStorageScope()] = &mockStorageCounterReconciliation{Scope: traffic.PlatformStorageScope(), RequestedAt: time.Now()}
	m.storageCounterReconciliations[traffic.OrganizationStorageScope(orgID.String())] = &mockStorageCounterReconciliation{Scope: traffic.OrganizationStorageScope(orgID.String()), OrgID: orgID, RequestedAt: time.Now()}
	if lib.OwnerID != uuid.Nil {
		m.storageCounterReconciliations[traffic.UserStorageScope(orgID.String(), lib.OwnerID.String())] = &mockStorageCounterReconciliation{Scope: traffic.UserStorageScope(orgID.String(), lib.OwnerID.String()), OrgID: orgID, OwnerID: lib.OwnerID, RequestedAt: time.Now()}
	}
	return nil
}
func (m *MockStore) ListGroupMembershipsByUser(orgID, userID uuid.UUID) ([]uuid.UUID, error) {
	if m.listGroupMembershipsByUserErr != nil {
		return nil, m.listGroupMembershipsByUserErr
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	prefix := fmt.Sprintf("%s:%s:", orgID, userID)
	var result []uuid.UUID
	for key := range m.groupsByMember {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix {
			gid, err := uuid.Parse(key[len(prefix):])
			if err == nil {
				result = append(result, gid)
			}
		}
	}
	return result, nil
}
func (m *MockStore) DeleteGroupMember(groupID, userID uuid.UUID) error {
	if m.deleteGroupMemberErr != nil {
		return m.deleteGroupMemberErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.groupMembers, fmt.Sprintf("%s:%s", groupID, userID))
	return nil
}
func (m *MockStore) DeleteGroupByMember(orgID, userID, groupID uuid.UUID) error {
	if m.deleteGroupByMemberErr != nil {
		return m.deleteGroupByMemberErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.groupsByMember, fmt.Sprintf("%s:%s:%s", orgID, userID, groupID))
	return nil
}
func (m *MockStore) ListSharesByUser(orgID, userID uuid.UUID) ([]ShareByUserInfo, error) {
	if m.listSharesByUserErr != nil {
		return nil, m.listSharesByUserErr
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []ShareByUserInfo
	for _, share := range m.shares {
		if share.OrgID == orgID && share.SharedTo == userID && share.SharedToType == "user" {
			result = append(result, ShareByUserInfo{SharedTo: share.SharedTo, LibraryID: share.LibraryID, ShareID: share.ShareID})
		}
	}
	return result, nil
}
func (m *MockStore) ListSharesCreatedByUser(orgID, userID uuid.UUID) ([]ShareByCreatorInfo, error) {
	if m.listSharesCreatedByUserErr != nil {
		return nil, m.listSharesCreatedByUserErr
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []ShareByCreatorInfo
	for _, share := range m.shares {
		if share.OrgID == orgID && share.SharedBy == userID {
			result = append(result, ShareByCreatorInfo{LibraryID: share.LibraryID, ShareID: share.ShareID})
		}
	}
	return result, nil
}
func (m *MockStore) DeleteStarredFilesByUser(userID uuid.UUID) error {
	if m.deleteStarredFilesByUserErr != nil {
		return m.deleteStarredFilesByUserErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.starredFiles, userID)
	return nil
}
func (m *MockStore) DeleteMonitoredReposByUser(userID uuid.UUID) error {
	if m.deleteMonitoredReposByUserErr != nil {
		return m.deleteMonitoredReposByUserErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.monitoredRepos, userID)
	return nil
}
func (m *MockStore) DeleteAPIKeysByUser(orgID, userID uuid.UUID) error {
	if m.deleteAPIKeysByUserErr != nil {
		return m.deleteAPIKeysByUserErr
	}
	return nil
}
func (m *MockStore) HardDeleteUser(orgID, userID uuid.UUID, email string) error {
	if m.hardDeleteUserErr != nil {
		return m.hardDeleteUserErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.users, fmt.Sprintf("%s:%s", orgID, userID))
	return nil
}

func (m *MockStore) AcquireUserHardDeleteLock(userID, leaseToken uuid.UUID) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return mockAcquireHardDeleteLock(m.userHardDeleteLocks, userID, leaseToken), nil
}

func (m *MockStore) RenewUserHardDeleteLock(userID, leaseToken uuid.UUID) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	lock, locked := m.userHardDeleteLocks[userID]
	if !locked || lock.LeaseToken != leaseToken {
		return false, nil
	}
	lock.Heartbeat = time.Now().UTC()
	m.userHardDeleteLocks[userID] = lock
	return true, nil
}

func (m *MockStore) ReleaseUserHardDeleteLock(userID, leaseToken uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	lock, locked := m.userHardDeleteLocks[userID]
	if !locked || lock.LeaseToken != leaseToken {
		return nil
	}
	delete(m.userHardDeleteLocks, userID)
	return nil
}

func (m *MockStore) AcquireLibraryHardDeleteLock(libraryID, leaseToken uuid.UUID) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return mockAcquireHardDeleteLock(m.libraryHardDeleteLocks, libraryID, leaseToken), nil
}

func (m *MockStore) RenewLibraryHardDeleteLock(libraryID, leaseToken uuid.UUID) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	lock, locked := m.libraryHardDeleteLocks[libraryID]
	if !locked || lock.LeaseToken != leaseToken {
		return false, nil
	}
	lock.Heartbeat = time.Now().UTC()
	m.libraryHardDeleteLocks[libraryID] = lock
	return true, nil
}

func (m *MockStore) ReleaseLibraryHardDeleteLock(libraryID, leaseToken uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	lock, locked := m.libraryHardDeleteLocks[libraryID]
	if !locked || lock.LeaseToken != leaseToken {
		return nil
	}
	delete(m.libraryHardDeleteLocks, libraryID)
	return nil
}

func (m *MockStore) GetUserEmail(orgID, userID uuid.UUID) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	u, ok := m.users[fmt.Sprintf("%s:%s", orgID, userID)]
	if !ok {
		return "", fmt.Errorf("user not found")
	}
	return u.Email, nil
}

// --- Org cascade (Fase 4) ---

func (m *MockStore) ListExpiredDeletedOrgs(graceDays int) ([]DeletedOrgInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cutoff := time.Now().AddDate(0, 0, -graceDays)
	var result []DeletedOrgInfo
	for orgID, deletedAt := range m.orgDeletedAt {
		if m.orgStatus[orgID] == "deleted" && deletedAt.Before(cutoff) {
			result = append(result, DeletedOrgInfo{
				OrgID:     orgID,
				Name:      m.orgNames[orgID],
				DeletedAt: deletedAt,
			})
		}
	}
	return result, nil
}
func (m *MockStore) ListUsersByOrg(orgID uuid.UUID) ([]OrgUserInfo, error) {
	if m.listUsersByOrgErr != nil {
		return nil, m.listUsersByOrgErr
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	prefix := fmt.Sprintf("%s:", orgID)
	var result []OrgUserInfo
	for key, u := range m.users {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix {
			result = append(result, OrgUserInfo{UserID: u.UserID, Email: u.Email})
		}
	}
	return result, nil
}
func (m *MockStore) ListGroupsByOrg(orgID uuid.UUID) ([]uuid.UUID, error) {
	if m.listGroupsByOrgErr != nil {
		return nil, m.listGroupsByOrgErr
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	prefix := fmt.Sprintf("%s:", orgID)
	var result []uuid.UUID
	for key := range m.groups {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix {
			gid, err := uuid.Parse(key[len(prefix):])
			if err == nil {
				result = append(result, gid)
			}
		}
	}
	return result, nil
}
func (m *MockStore) ListLibrariesForOrg(orgID uuid.UUID) ([]OrgLibraryInfo, error) {
	if m.listLibrariesForOrgErr != nil {
		return nil, m.listLibrariesForOrgErr
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []OrgLibraryInfo
	for _, lib := range m.libraries {
		if lib.OrgID == orgID {
			result = append(result, OrgLibraryInfo{
				LibraryID:    lib.LibraryID,
				StorageClass: lib.StorageClass,
				OwnerID:      lib.OwnerID,
				DeletedAt:    lib.DeletedAt,
			})
		}
	}
	return result, nil
}

func (m *MockStore) DeleteLibraryStorageCounter(orgID, libraryID uuid.UUID) error {
	if m.deleteLibraryStorageCounterErr != nil {
		return m.deleteLibraryStorageCounterErr
	}
	return nil // no-op in mock — storage_counters not simulated
}
func (m *MockStore) DeleteGroupFull(orgID, groupID uuid.UUID) error {
	if m.deleteGroupFullErr != nil {
		return m.deleteGroupFullErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.groups, fmt.Sprintf("%s:%s", orgID, groupID))
	// Clean group_members
	prefix := fmt.Sprintf("%s:", groupID)
	for key := range m.groupMembers {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix {
			delete(m.groupMembers, key)
		}
	}
	return nil
}

func (m *MockStore) AcquireOrgHardDeleteLock(orgID, leaseToken uuid.UUID) (bool, error) {
	m.mu.Lock()
	if !mockAcquireHardDeleteLock(m.orgHardDeleteLocks, orgID, leaseToken) {
		m.mu.Unlock()
		return false, nil
	}
	hook := m.acquireOrgHardDeleteLockHook
	m.mu.Unlock()
	if hook != nil {
		hook(orgID)
	}
	return true, nil
}

func (m *MockStore) RenewOrgHardDeleteLock(orgID, leaseToken uuid.UUID) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	lock, locked := m.orgHardDeleteLocks[orgID]
	if !locked || lock.LeaseToken != leaseToken {
		return false, nil
	}
	lock.Heartbeat = time.Now().UTC()
	m.orgHardDeleteLocks[orgID] = lock
	return true, nil
}

func (m *MockStore) ReleaseOrgHardDeleteLock(orgID, leaseToken uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	lock, locked := m.orgHardDeleteLocks[orgID]
	if !locked || lock.LeaseToken != leaseToken {
		return nil
	}
	delete(m.orgHardDeleteLocks, orgID)
	return nil
}

func (m *MockStore) BeginOrgPurge(orgID uuid.UUID, identityAt time.Time) (bool, error) {
	if hook := m.beginOrgPurgeHook; hook != nil {
		hook(orgID)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	deletedAt, ok := m.orgDeletedAt[orgID]
	if !ok || !deletedAt.Equal(identityAt) {
		return false, nil
	}
	status := m.orgStatus[orgID]
	if status == "purging" {
		return true, nil
	}
	if status != "deleted" {
		return false, nil
	}
	m.orgStatus[orgID] = "purging"
	return true, nil
}

func (m *MockStore) HardDeleteOrg(orgID uuid.UUID) error {
	leaseToken := uuid.New()
	acquired, err := m.AcquireOrgHardDeleteLock(orgID, leaseToken)
	if err != nil {
		return err
	}
	if !acquired {
		return fmt.Errorf("org %s hard delete already in progress", orgID)
	}
	defer m.ReleaseOrgHardDeleteLock(orgID, leaseToken) //nolint:errcheck
	deletedAt, err := m.GetOrgDeletedAt(orgID)
	if err != nil {
		return err
	}
	if deletedAt == nil {
		return fmt.Errorf("org %s is not in deleted state", orgID)
	}
	if err := m.ensureOrgHasNoLiveChildren(orgID); err != nil {
		return err
	}
	purging, err := m.BeginOrgPurge(orgID, *deletedAt)
	if err != nil {
		return err
	}
	if !purging {
		return fmt.Errorf("org %s is not in deleted state", orgID)
	}
	return m.HardDeleteOrgLocked(orgID)
}

func (m *MockStore) ensureOrgHasNoLiveChildren(orgID uuid.UUID) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, lib := range m.libraries {
		if lib.OrgID == orgID {
			return fmt.Errorf("org %s still has live libraries", orgID)
		}
	}
	for _, user := range m.users {
		if user.OrgID == orgID {
			return fmt.Errorf("org %s still has live users", orgID)
		}
	}
	for key := range m.groups {
		if strings.HasPrefix(key, orgID.String()+":") {
			return fmt.Errorf("org %s still has live groups", orgID)
		}
	}
	return nil
}

func (m *MockStore) HardDeleteOrgLocked(orgID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.orgStatus[orgID] != "purging" {
		return fmt.Errorf("org %s is not in purge state", orgID)
	}
	for _, lib := range m.libraries {
		if lib.OrgID == orgID {
			return fmt.Errorf("org %s still has live libraries", orgID)
		}
	}
	for _, user := range m.users {
		if user.OrgID == orgID {
			return fmt.Errorf("org %s still has live users", orgID)
		}
	}
	for key := range m.groups {
		if strings.HasPrefix(key, orgID.String()+":") {
			return fmt.Errorf("org %s still has live groups", orgID)
		}
	}
	for i, id := range m.organizations {
		if id == orgID {
			m.organizations = append(m.organizations[:i], m.organizations[i+1:]...)
			break
		}
	}
	delete(m.orgNames, orgID)
	delete(m.orgStatus, orgID)
	delete(m.orgDeletedAt, orgID)
	return nil
}
func (m *MockStore) GetOrgName(orgID uuid.UUID) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	name, ok := m.orgNames[orgID]
	if !ok {
		return "", fmt.Errorf("org not found")
	}
	return name, nil
}

// --- GC stats persistence ---

func (m *MockStore) SaveGCStats(key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gcStats[key] = value
	return nil
}

func (m *MockStore) LoadGCStats(key string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	val, ok := m.gcStats[key]
	if !ok {
		// Mirror the Cassandra store contract so callers can use
		// `errors.Is(err, gocql.ErrNotFound)` regardless of backend.
		return "", gocql.ErrNotFound
	}
	return val, nil
}

// MockStorageProvider implements StorageProvider for testing.
type MockStorageProvider struct {
	mu          sync.Mutex
	DeletedKeys []string

	// failTimes is the number of upcoming DeleteBlock calls that should
	// return an error before the next call succeeds. Decremented per call.
	// Zero means "always succeed".
	failTimes int
	// failAlways overrides failTimes and makes every DeleteBlock fail.
	failAlways bool
	// failErr is the error returned while failing.
	failErr error
}

func (p *MockStorageProvider) GetBlockStore(storageClass string) (BlockStoreDeleter, error) {
	return &mockBlockDeleter{provider: p}, nil
}

// DeletedBlocks returns the list of block IDs that were deleted.
func (p *MockStorageProvider) DeletedBlocks() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string{}, p.DeletedKeys...)
}

// FailNextN causes the next n DeleteBlock calls to return err. After that,
// calls succeed as normal. Used to simulate transient S3 failures.
func (p *MockStorageProvider) FailNextN(n int, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failTimes = n
	p.failAlways = false
	p.failErr = err
}

// FailAlways causes every DeleteBlock call to return err until cleared.
func (p *MockStorageProvider) FailAlways(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failAlways = true
	p.failErr = err
}

// ClearFailures stops injecting failures.
func (p *MockStorageProvider) ClearFailures() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failTimes = 0
	p.failAlways = false
	p.failErr = nil
}

type mockBlockDeleter struct {
	provider *MockStorageProvider
}

func (d *mockBlockDeleter) DeleteBlock(ctx context.Context, blockID string) error {
	d.provider.mu.Lock()
	defer d.provider.mu.Unlock()
	if d.provider.failAlways {
		return d.provider.failErr
	}
	if d.provider.failTimes > 0 {
		d.provider.failTimes--
		return d.provider.failErr
	}
	d.provider.DeletedKeys = append(d.provider.DeletedKeys, blockID)
	return nil
}

// --- S3 orphan recovery (mock) ---

func (m *MockStore) RecordS3Orphan(orgID uuid.UUID, blockID, storageClass, errMsg string, now time.Time) (time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%s:%s", orgID, blockID)
	initialRetryCount := 0
	if errMsg != "" {
		initialRetryCount = 1
	}
	if existing, ok := m.s3Orphans[key]; ok {
		m.upsertS3OrphanProjection(existing)
		if errMsg != "" {
			existing.LastAttemptAt = now
			existing.RetryCount++
			existing.LastError = errMsg
		}
		return existing.FirstSeenAt, nil
	}
	orphan := &S3OrphanInfo{
		OrgID:         orgID,
		BlockID:       blockID,
		StorageClass:  storageClass,
		FirstSeenAt:   now.UTC(),
		LastAttemptAt: now,
		RetryCount:    initialRetryCount,
		LastError:     errMsg,
	}
	m.s3Orphans[key] = orphan
	m.upsertS3OrphanProjection(orphan)
	return orphan.FirstSeenAt, nil
}

func (m *MockStore) UpdateS3OrphanAttempt(orgID uuid.UUID, blockID, errMsg string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%s:%s", orgID, blockID)
	if existing, ok := m.s3Orphans[key]; ok {
		existing.LastAttemptAt = now
		existing.RetryCount++
		existing.LastError = errMsg
	}
	return nil
}

func (m *MockStore) DeleteS3Orphan(orgID uuid.UUID, blockID string, firstSeenAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%s:%s", orgID, blockID)
	if firstSeenAt.IsZero() {
		if existing, ok := m.s3Orphans[key]; ok {
			firstSeenAt = existing.FirstSeenAt
		}
	}
	delete(m.s3Orphans, key)
	if !firstSeenAt.IsZero() {
		delete(m.s3OrphanProjections, newMockS3OrphanProjectionKey(orgID, blockID, firstSeenAt))
	}
	return nil
}

func (m *MockStore) ListS3OrphansByDay(day time.Time, bucket int, limit int) ([]S3OrphanInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if limit <= 0 {
		limit = 100
	}
	targetDay := db.GCProjectionUTCDate(day)
	var out []S3OrphanInfo
	for key, orphan := range m.s3OrphanProjections {
		if !key.FirstSeenDay.Equal(targetDay) {
			continue
		}
		if key.Bucket != bucket {
			continue
		}
		out = append(out, orphan)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].FirstSeenAt.Equal(out[j].FirstSeenAt) {
			return out[i].FirstSeenAt.Before(out[j].FirstSeenAt)
		}
		if out[i].OrgID != out[j].OrgID {
			return out[i].OrgID.String() < out[j].OrgID.String()
		}
		return out[i].BlockID < out[j].BlockID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// S3OrphanCount is a test helper returning the total orphan count across orgs.
func (m *MockStore) S3OrphanCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.s3Orphans)
}

// AllS3Orphans is a test helper returning every orphan row across all
// (day, bucket) partitions. Replaces the old per-org ListS3Orphans path used
// by tests that just need to assert "what orphans exist right now".
func (m *MockStore) AllS3Orphans() []S3OrphanInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]S3OrphanInfo, 0, len(m.s3Orphans))
	for _, o := range m.s3Orphans {
		out = append(out, *o)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].OrgID != out[j].OrgID {
			return out[i].OrgID.String() < out[j].OrgID.String()
		}
		return out[i].BlockID < out[j].BlockID
	})
	return out
}

// AllBlockGCCandidates is a test helper returning every candidate row across
// all (day, bucket) partitions.
func (m *MockStore) AllBlockGCCandidates() []BlockGCCandidateInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]BlockGCCandidateInfo, 0, len(m.blockGCCandidates))
	for _, c := range m.blockGCCandidates {
		out = append(out, BlockGCCandidateInfo{
			OrgID:        c.OrgID,
			BlockID:      c.BlockID,
			StorageClass: c.StorageClass,
			CandidateAt:  c.CandidateAt,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].OrgID != out[j].OrgID {
			return out[i].OrgID.String() < out[j].OrgID.String()
		}
		return out[i].BlockID < out[j].BlockID
	})
	return out
}
