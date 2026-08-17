package gc

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
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

	// block_id_mappings keyed by "orgID:representationID:normalizedExternalID"
	// (see mockMappingKey); value is the canonicalized internal SHA-256 id.
	mappings map[string]string

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
	// shard-local storage snapshots keyed by scope then shard. Only the platform
	// scope uses multiple shards in the mock today.
	storageShardSnapshots map[string]map[int]traffic.StorageSnapshot

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
	// libraryDestructiveCalls records HardDeleteLibrary / DeleteLibraryStorageCounter
	// in call order so tests can assert the hard delete precedes the counter cleanup.
	libraryDestructiveCalls        []string
	deleteLibraryStorageCounterFor map[uuid.UUID]int
	deleteGroupFullErr             error
	reconcileStorageCountersHook   func()
	acquireOrgHardDeleteLockHook   func(orgID uuid.UUID)
	beginOrgPurgeHook              func(orgID uuid.UUID)
	getBlockRefCountErr            error
	blockExistsErr                 error
	blockExistsCalls               int
	libraryExistsErr               error
	canonicalLibraryExistsErr      error
	forceRenewLibraryLockNotOwned  bool
	groupExistsErr                 error
	groupExistsCalls               atomic.Int64
	findOrgForLibraryErr           error
	blockHasReferencesHook         func(orgID uuid.UUID, blockID string, current bool) (bool, error)
	blockHasReferencesErr          error
	blockHasReferencesGlobalErr    error
	blockHasReferencesLocalCalls   int
	blockHasReferencesGlobalCalls  int
	releaseStaleBlockClaimErr      error
	releaseBlockClaimErr           error
	claimBlockDeleteErr            error
	validateDestructiveTopologyErr error
	blockReferenceExistsErr        error
	ensureBlockGCCandidateErr      error
	deleteProvisionalProjectionErr error
	getS3OrphanGlobalErr           error
	getS3OrphanGlobalCalls         int
	getS3OrphanGlobalHook          func(orgID uuid.UUID, blockID string, call int, info S3OrphanInfo) (S3OrphanInfo, error)
	deleteS3OrphanErrOnce          error

	// optional test hooks for reproducing concurrency windows deterministically.
	getQueueSizeHook                func(orgID uuid.UUID, size int)
	removeActiveOrgHook             func(orgID uuid.UUID, activeBefore time.Time)
	recalculateStatsHook            func(orgID uuid.UUID)
	startBlockDeleteOrphanResetRace bool
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
	dlqOpHook                        func(orgID uuid.UUID, op string)
	deleteClaimedBlockStubForceFalse bool

	// audit_log entries
	auditLog []AuditLogEntry

	// S3 orphans keyed by "orgID:blockID"
	s3Orphans map[string]*S3OrphanInfo
	// S3 orphan discovery rows keyed by the full projection PK.
	s3OrphanProjections map[mockS3OrphanProjectionKey]S3OrphanDiscoveryInfo
}

var _ GCStore = (*MockStore)(nil)

type mockBlock struct {
	OrgID               uuid.UUID
	BlockID             string
	StorageClass        string
	StorageClassPresent bool
	CreatedAt           *time.Time
	GCState             string
	GCClaimID           string
	GCClaimedAt         *time.Time
	RepresentationID    string
	Sha1                string
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
	OrgID                 uuid.UUID
	LibraryID             uuid.UUID
	OwnerID               uuid.UUID
	BlockRepresentationID string
	// Encrypted mirrors libraries.encrypted so the mock can resolve an empty
	// stored block_representation_id the same way CassandraStore does — plaintext
	// derives plain:v1, encrypted derives library:<id>.
	Encrypted      bool
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
	OrgID                 uuid.UUID
	LibraryID             uuid.UUID
	BlockRepresentationID string
	StorageClass          string
	DeletedAt             time.Time
	PurgeRequestedAt      time.Time
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
		storageShardSnapshots:                make(map[string]map[int]traffic.StorageSnapshot),
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
		s3OrphanProjections:                  make(map[mockS3OrphanProjectionKey]S3OrphanDiscoveryInfo),
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

// upsertS3OrphanProjection mirrors the Cassandra discovery write. Since R22b the
// projection stores identity only, so this deliberately takes an
// S3OrphanDiscoveryInfo rather than the canonical row: a mock that kept the full
// payload could satisfy a test that production Cassandra would now fail.
func (m *MockStore) upsertS3OrphanProjection(orgID uuid.UUID, blockID string, firstSeenAt time.Time) {
	key := newMockS3OrphanProjectionKey(orgID, blockID, firstSeenAt)
	m.s3OrphanProjections[key] = S3OrphanDiscoveryInfo{
		OrgID:       orgID,
		BlockID:     blockID,
		FirstSeenAt: firstSeenAt.UTC(),
	}
}

func mockMappingKey(orgID uuid.UUID, representationID, externalID string) string {
	representationID = strings.TrimSpace(representationID)
	if representationID == "" {
		representationID = db.PlainBlockRepresentationID
	}
	// Mirror the real store's canonicalization (db.NormalizeBlockID) so the mock
	// resolves/deletes case-insensitively too.
	return fmt.Sprintf("%s:%s:%s", orgID, representationID, db.NormalizeBlockID(externalID))
}

func (m *MockStore) blockRepresentationIDForLibraryLocked(orgID, libraryID uuid.UUID) (string, error) {
	if lib := m.libraries[libraryID]; lib != nil {
		if lib.OrgID != orgID {
			return "", gocql.ErrNotFound
		}
		resolved, err := db.CanonicalBlockRepresentationIDForLibrary(lib.LibraryID.String(), lib.Encrypted, lib.BlockRepresentationID)
		if err != nil {
			return "", err
		}
		return resolved, nil
	}
	if deleted := m.deletedLibraries[libraryID]; deleted != nil {
		if deleted.OrgID != orgID {
			return "", gocql.ErrNotFound
		}
		if rep := strings.TrimSpace(deleted.BlockRepresentationID); rep != "" {
			if !db.IsCanonicalBlockRepresentationForLibrary(rep, libraryID) {
				if !db.IsCanonicalBlockRepresentationID(rep) {
					return "", fmt.Errorf("deleted library %s carries non-canonical block representation %q", libraryID, rep)
				}
				return "", fmt.Errorf("deleted library %s carries block representation %q for a different library", libraryID, rep)
			}
			return rep, nil
		}
		return "", gocql.ErrNotFound
	}
	return "", gocql.ErrNotFound
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
	// Mirror the Cassandra store: block pending rows are always keyed under uuid.Nil.
	libraryID = pendingItemLibraryID(itemType, libraryID)
	key := newMockPendingItemKey(orgID, libraryID, itemType, itemID, identityAt)
	if expiresAt == nil {
		m.pendingItems[key] = nil
		return
	}
	expiry := *expiresAt
	m.pendingItems[key] = &expiry
}

func (m *MockStore) deletePendingItem(orgID, libraryID uuid.UUID, itemType ItemType, itemID string, identityAt time.Time) {
	libraryID = pendingItemLibraryID(itemType, libraryID)
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
	createdAt := time.Now().UTC()
	m.blocks[key] = &mockBlock{
		OrgID:               orgID,
		BlockID:             blockID,
		StorageClass:        storageClass,
		StorageClassPresent: true,
		RepresentationID:    db.PlainBlockRepresentationID,
		CreatedAt:           &createdAt,
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

// AddStubBlockForTest seeds the kind of metadata-free row Cassandra can surface
// after a claim races with a missing canonical block.
func (m *MockStore) AddStubBlockForTest(orgID uuid.UUID, blockID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%s:%s", orgID, blockID)
	m.blocks[key] = &mockBlock{
		OrgID:            orgID,
		BlockID:          blockID,
		RepresentationID: db.PlainBlockRepresentationID,
		CreatedAt:        nil,
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

// AddS3OrphanProjectionForTest seeds a discovery row independently of the
// canonical row, so recovery tests can model a projection that outlived, or
// never matched, its canonical counterpart.
//
// R22b removed the payload counterpart of this helper, SetS3OrphanProjectionForTest,
// which existed to poison storage_class/representation_id/external_sha1/recovery_phase
// on a discovery row and prove recovery ignored them. Those columns no longer exist
// (migration 014), so the property is now structural rather than behavioural and is
// gated by TestR22bProjectionSchemaIsIdentityOnly instead of by a poisoned fixture.
func (m *MockStore) AddS3OrphanProjectionForTest(info S3OrphanDiscoveryInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.upsertS3OrphanProjection(info.OrgID, info.BlockID, info.FirstSeenAt)
}

// GetS3OrphanProjectionForTest reads the raw discovery row for store tests.
// Production recovery cannot learn anything more from it than this: since R22b
// the stored row IS its key.
func (m *MockStore) GetS3OrphanProjectionForTest(orgID uuid.UUID, blockID string, firstSeenAt time.Time) (S3OrphanDiscoveryInfo, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	projection, ok := m.s3OrphanProjections[newMockS3OrphanProjectionKey(orgID, blockID, firstSeenAt)]
	return projection, ok
}

// DeleteS3OrphanCanonicalForTest removes only the canonical row, leaving its
// discovery projection behind to model canonical expiry or drift.
func (m *MockStore) DeleteS3OrphanCanonicalForTest(orgID uuid.UUID, blockID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.s3Orphans, fmt.Sprintf("%s:%s", orgID, blockID))
}

// SetGetS3OrphanGlobalErrForTest makes the canonical EACH_QUORUM read fail.
func (m *MockStore) SetGetS3OrphanGlobalErrForTest(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.getS3OrphanGlobalErr = err
}

// SetBlockExistsErrForTest makes BlockExists fail without affecting the other
// block reads. It is used to prove that post-S3 orphan finalization no longer
// depends on the resurrection guard that protected mapping deletion.
func (m *MockStore) SetBlockExistsErrForTest(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.blockExistsErr = err
}

func (m *MockStore) BlockExistsCallsForTest() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.blockExistsCalls
}

// SetDeleteS3OrphanErrOnceForTest makes the next orphan clear fail without
// mutating state, modeling a crash or failed canonical clear after S3 succeeds.
func (m *MockStore) SetDeleteS3OrphanErrOnceForTest(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteS3OrphanErrOnce = err
}

// SetGetS3OrphanGlobalHookForTest injects deterministic changes into canonical
// reloads. The call number starts at one for the first row read.
func (m *MockStore) SetGetS3OrphanGlobalHookForTest(hook func(orgID uuid.UUID, blockID string, call int, info S3OrphanInfo) (S3OrphanInfo, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.getS3OrphanGlobalHook = hook
}

// GetS3OrphanGlobalCallsForTest reports canonical reloads issued by recovery.
func (m *MockStore) GetS3OrphanGlobalCallsForTest() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.getS3OrphanGlobalCalls
}

func (m *MockStore) AddBlockMapping(orgID uuid.UUID, externalID, internalID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.addBlockMappingLocked(orgID, db.PlainBlockRepresentationID, externalID, internalID)
}

func (m *MockStore) AddBlockMappingForRepresentation(orgID uuid.UUID, representationID, externalID, internalID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.addBlockMappingLocked(orgID, representationID, externalID, internalID)
}

// addBlockMappingLocked mirrors the server-derived blocks.sha1 and
// representation_id onto the block row. Each field is filled independently so a
// block that already carries a sha1 still gets its representation modeled. Caller
// must hold m.mu.
func (m *MockStore) addBlockMappingLocked(orgID uuid.UUID, representationID, externalID, internalID string) {
	// Canonicalize exactly like the Cassandra writer (writeCheckedBlockIDMapping +
	// UpsertBlockMetadataWithRepresentationAndSHA1) so a test that passes an
	// uppercase/padded id behaves identically in the mock and against Cassandra.
	externalID = db.NormalizeBlockID(externalID)
	internalID = db.NormalizeBlockID(internalID)
	representationID = strings.TrimSpace(representationID)
	m.mappings[mockMappingKey(orgID, representationID, externalID)] = internalID
	if b := m.blocks[fmt.Sprintf("%s:%s", orgID, internalID)]; b != nil {
		if strings.TrimSpace(b.Sha1) == "" {
			b.Sha1 = externalID
		}
		if strings.TrimSpace(b.RepresentationID) == "" && representationID != "" {
			b.RepresentationID = representationID
		}
	}
}

// ForwardBlockMappingExists reports whether the forward external->internal block
// mapping row still exists. Test accessor that replaces the dropped reverse-index
// assertions.
func (m *MockStore) ForwardBlockMappingExists(orgID uuid.UUID, externalID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.mappings[mockMappingKey(orgID, db.PlainBlockRepresentationID, externalID)]
	return ok
}

func (m *MockStore) ForwardBlockMappingExistsForRepresentation(orgID uuid.UUID, representationID, externalID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.mappings[mockMappingKey(orgID, representationID, externalID)]
	return ok
}

func (m *MockStore) AddStorageSnapshot(scope string, bytesUsed, fileCount int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if scope == traffic.PlatformStorageScope() {
		m.storageShardSnapshots[scope] = map[int]traffic.StorageSnapshot{
			0: {BytesUsed: bytesUsed, FileCount: fileCount},
		}
		return
	}
	m.storageSnapshots[scope] = traffic.StorageSnapshot{BytesUsed: bytesUsed, FileCount: fileCount}
}

func (m *MockStore) StorageSnapshot(scope string) traffic.StorageSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if scope == traffic.PlatformStorageScope() {
		var sum traffic.StorageSnapshot
		for _, snapshot := range m.storageShardSnapshots[scope] {
			sum.BytesUsed += snapshot.BytesUsed
			sum.FileCount += snapshot.FileCount
		}
		return sum
	}
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
		OrgID:                 orgID,
		LibraryID:             libraryID,
		BlockRepresentationID: db.PlainBlockRepresentationID,
		StorageClass:          storageClass,
		HeadCommitID:          headCommitID,
		VersionTTLDays:        versionTTLDays,
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
		OrgID:                 orgID,
		LibraryID:             libraryID,
		BlockRepresentationID: db.PlainBlockRepresentationID,
		StorageClass:          storageClass,
		HeadCommitID:          headCommitID,
		AutoDeleteDays:        autoDeleteDays,
	}
}

// SetLibraryEncrypted flips a mock library's encrypted flag. Tests that need
// the "encrypted + empty stored representation -> library:<id>" derivation
// path must clear BlockRepresentationID separately.
func (m *MockStore) SetLibraryEncrypted(libraryID uuid.UUID, encrypted bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if lib := m.libraries[libraryID]; lib != nil {
		lib.Encrypted = encrypted
	}
}

func (m *MockStore) AddLibrary(orgID, libraryID uuid.UUID, storageClass string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.libraries[libraryID] = &mockLibrary{
		OrgID:                 orgID,
		LibraryID:             libraryID,
		BlockRepresentationID: db.PlainBlockRepresentationID,
		StorageClass:          storageClass,
	}
}

func (m *MockStore) AddLibraryWithOwner(orgID, libraryID, ownerID uuid.UUID, storageClass string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.libraries[libraryID] = &mockLibrary{
		OrgID:                 orgID,
		LibraryID:             libraryID,
		OwnerID:               ownerID,
		BlockRepresentationID: db.PlainBlockRepresentationID,
		StorageClass:          storageClass,
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
	m.libraries[libraryID] = &mockLibrary{OrgID: orgID, LibraryID: libraryID, BlockRepresentationID: db.PlainBlockRepresentationID, StorageClass: storageClass}
	m.deletedLibraries[libraryID] = &mockDeletedLibrary{OrgID: orgID, LibraryID: libraryID, BlockRepresentationID: db.PlainBlockRepresentationID, StorageClass: storageClass, DeletedAt: deletedAt}
}

// AddPurgeRequestedDeletedLibrary adds a permanently-deleted library marker whose
// canonical libraries row is already gone and whose purge_requested_at is set, so
// Phase 13 must treat it as eligible on its next scan regardless of deleted_at (the
// worker still grace-gates the enqueued cascade before processing).
func (m *MockStore) AddPurgeRequestedDeletedLibrary(orgID, libraryID uuid.UUID, storageClass string, deletedAt, purgeRequestedAt time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deletedLibraries[libraryID] = &mockDeletedLibrary{OrgID: orgID, LibraryID: libraryID, BlockRepresentationID: db.PlainBlockRepresentationID, StorageClass: storageClass, DeletedAt: deletedAt, PurgeRequestedAt: purgeRequestedAt}
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
	return BlockInfo{BlockID: block.BlockID, StorageClass: block.StorageClass, CreatedAt: block.CreatedAt, RepresentationID: block.RepresentationID, Sha1: block.Sha1}, nil
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
	// Mirror CassandraStore.EnqueueItem: the raw single-row path carries no block
	// representation, so it must reject the types that require one.
	if itemTypeRequiresBlockRepresentation(itemType) {
		return fmt.Errorf("item type %s requires explicit block representation; use EnqueueBatch", itemType)
	}
	m.seedQueueItemRow(orgID, queuedAt, itemType, itemID, libraryID, storageClass, retryCount)
	return nil
}

// seedQueueItemRow appends a raw gc_queue row with an empty block representation.
// It backs both the guarded EnqueueItem and the test-only SeedQueueItemForTest
// (defined in a _test.go file so it never compiles into a production build); the
// block representation is intentionally always empty on this raw single-row path.
func (m *MockStore) seedQueueItemRow(orgID uuid.UUID, queuedAt time.Time, itemType ItemType, itemID string, libraryID uuid.UUID, storageClass string, retryCount int) {
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
		BlockRepresentationID:       "",
		StorageClass:                storageClass,
		RetryCount:                  retryCount,
	}
	m.queue[orgID] = append(m.queue[orgID], item)
	m.upsertPendingItem(orgID, libraryID, itemType, itemID, queuedAt, nil)
	m.activeQueueOrgs[orgID] = time.Now().UTC()
	m.dirtyQueueOrgs[orgID] = time.Now().UTC()
}

func (m *MockStore) EnqueueBatch(items []QueueItem) error {
	for _, item := range items {
		if err := validateQueueItemBlockRepresentation(item); err != nil {
			return err
		}
	}
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
	libraryID = pendingItemLibraryID(itemType, libraryID)
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

func (m *MockStore) RequeueItem(orgID uuid.UUID, oldQueuedAt, newQueuedAt time.Time, itemType ItemType, itemID string, libraryID uuid.UUID, blockRepresentationID, storageClass string, newRetryCount int, identityAt time.Time, requiresLibraryDeletedCheck bool, libraryGuardMode LibraryGuardMode) error {
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
			newItem.LibraryGuardMode = effectiveLibraryGuardMode(libraryGuardMode, requiresLibraryDeletedCheck)
			newItem.BlockRepresentationID = strings.TrimSpace(blockRepresentationID)
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
	found := false
	for i, existing := range items {
		if existing.QueuedAt.Equal(item.QueuedAt) && existing.ItemType == item.ItemType && existing.ItemID == item.ItemID {
			m.queue[item.OrgID] = append(items[:i], items[i+1:]...)
			found = true
			break
		}
	}
	if !found {
		return nil
	}
	m.failedItems[item.OrgID] = append(m.failedItems[item.OrgID], GCFailedItemInfo{
		OrgID:                       item.OrgID,
		FailedAt:                    failedAt,
		ExpiresAt:                   failedAt.Add(gcFailedItemRetention),
		QueuedAt:                    item.QueuedAt,
		IdentityAt:                  effectiveIdentityAt(item.QueuedAt, item.IdentityAt),
		RequiresLibraryDeletedCheck: item.RequiresLibraryDeletedCheck,
		LibraryGuardMode:            effectiveLibraryGuardMode(item.LibraryGuardMode, item.RequiresLibraryDeletedCheck),
		ItemType:                    item.ItemType,
		ItemID:                      item.ItemID,
		LibraryID:                   item.LibraryID,
		BlockRepresentationID:       strings.TrimSpace(item.BlockRepresentationID),
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

func (m *MockStore) ListFailedItemExpiriesByDay(day time.Time, bucket int) ([]GCFailedItemExpiryInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	projectionDay := db.GCProjectionUTCDate(day)
	result := make([]GCFailedItemExpiryInfo, 0)
	for orgID, items := range m.failedItems {
		for _, item := range items {
			expiresAt := item.ExpiresAt
			if expiresAt.IsZero() {
				expiresAt = item.FailedAt.Add(gcFailedItemRetention)
			}
			if !db.GCProjectionUTCDate(expiresAt).Equal(projectionDay) {
				continue
			}
			if db.GCDiscoveryBucket(orgID.String(), string(item.ItemType), item.ItemID, item.FailedAt.UTC().Format(time.RFC3339Nano)) != bucket {
				continue
			}
			result = append(result, GCFailedItemExpiryInfo{
				OrgID:     orgID,
				FailedAt:  item.FailedAt,
				ExpiresAt: expiresAt,
				ItemType:  item.ItemType,
				ItemID:    item.ItemID,
			})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if !result[i].ExpiresAt.Equal(result[j].ExpiresAt) {
			return result[i].ExpiresAt.Before(result[j].ExpiresAt)
		}
		return result[i].ItemID < result[j].ItemID
	})
	return result, nil
}

func (m *MockStore) ListOrgsWithFailedItems(limit int) ([]GCFailedItemOrgInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	results := make([]GCFailedItemOrgInfo, 0)
	for orgID, stats := range m.orgQueueStats {
		if stats.FailedDepth <= 0 {
			continue
		}
		// Mirror CassandraStore: UpdatedAt reflects the most recent real failure,
		// not the snapshot refresh time.
		var lastFailedAt time.Time
		for _, item := range m.failedItems[orgID] {
			if item.FailedAt.After(lastFailedAt) {
				lastFailedAt = item.FailedAt
			}
		}
		results = append(results, GCFailedItemOrgInfo{
			OrgID:            orgID,
			OrgName:          m.orgNames[orgID],
			FailedItemsTotal: stats.FailedDepth,
			UpdatedAt:        lastFailedAt,
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

func (m *MockStore) DeleteExpiredFailedItem(expiry GCFailedItemExpiryInfo, now time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	items := m.failedItems[expiry.OrgID]
	for i, item := range items {
		if item.FailedAt.Equal(expiry.FailedAt) && item.ItemType == expiry.ItemType && item.ItemID == expiry.ItemID {
			expiresAt := item.ExpiresAt
			if expiresAt.IsZero() {
				expiresAt = item.FailedAt.Add(gcFailedItemRetention)
			}
			if expiresAt.After(now.UTC()) {
				return false, nil
			}
			m.failedItems[expiry.OrgID] = append(items[:i], items[i+1:]...)
			m.deletePendingItem(expiry.OrgID, item.LibraryID, item.ItemType, item.ItemID, effectiveIdentityAt(item.QueuedAt, item.IdentityAt))
			m.dirtyQueueOrgs[expiry.OrgID] = time.Now().UTC()
			return true, nil
		}
	}
	m.dirtyQueueOrgs[expiry.OrgID] = time.Now().UTC()
	return false, nil
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
			// Mirror CassandraStore.RequeueFailedItem: this path writes straight
			// into the queue, so re-assert the block-representation invariant that
			// EnqueueBatch would otherwise enforce.
			if verr := validateQueueItemBlockRepresentation(QueueItem{
				ItemType:              item.ItemType,
				ItemID:                item.ItemID,
				LibraryID:             item.LibraryID,
				BlockRepresentationID: item.BlockRepresentationID,
			}); verr != nil {
				return fmt.Errorf("refusing to requeue failed item org=%s item_type=%s item_id=%s failed_at=%s: %w",
					orgID, itemType, itemID, failedAt.UTC().Format(time.RFC3339Nano), verr)
			}
			m.queue[orgID] = append(m.queue[orgID], QueueItem{
				OrgID:                       orgID,
				QueuedAt:                    item.QueuedAt,
				IdentityAt:                  effectiveIdentityAt(item.QueuedAt, item.IdentityAt),
				RequiresLibraryDeletedCheck: item.RequiresLibraryDeletedCheck,
				LibraryGuardMode:            effectiveLibraryGuardMode(item.LibraryGuardMode, item.RequiresLibraryDeletedCheck),
				ItemType:                    item.ItemType,
				ItemID:                      item.ItemID,
				LibraryID:                   item.LibraryID,
				BlockRepresentationID:       strings.TrimSpace(item.BlockRepresentationID),
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

func (m *MockStore) RecalculateOrgQueueStats(orgID uuid.UUID) (GCOrgStats, error) {
	m.mu.RLock()
	hook := m.recalculateStatsHook
	m.mu.RUnlock()
	if hook != nil {
		hook(orgID)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	stats := GCOrgStats{OrgID: orgID}
	for _, item := range m.queue[orgID] {
		stats.QueueDepth++
		queuedAtCopy := item.QueuedAt
		if stats.OldestQueuedAt == nil || queuedAtCopy.Before(*stats.OldestQueuedAt) {
			stats.OldestQueuedAt = &queuedAtCopy
		}
	}
	stats.FailedDepth = len(m.failedItems[orgID])
	now := time.Now().UTC()
	stats.UpdatedAt = now
	stats.RecalculatedAt = now
	m.orgQueueStats[orgID] = stats
	return stats, nil
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
	m.mu.Lock()
	defer m.mu.Unlock()
	m.blockExistsCalls++
	if m.blockExistsErr != nil {
		return false, m.blockExistsErr
	}
	if m.getBlockRefCountErr != nil {
		return false, m.getBlockRefCountErr
	}
	_, ok := m.blocks[fmt.Sprintf("%s:%s", orgID, blockID)]
	return ok, nil
}

func (m *MockStore) BlockHasReferences(orgID uuid.UUID, blockID string) (bool, error) {
	m.mu.Lock()
	m.blockHasReferencesLocalCalls++
	m.mu.Unlock()
	return m.blockHasReferencesShared(orgID, blockID)
}

// BlockHasReferencesGlobal mirrors the EACH_QUORUM read. It shares the reference
// state and the concurrency hook with BlockHasReferences so tests that inject a
// mid-claim reference still exercise claim-then-verify, but it counts separately and
// honours its own error injection, which is how the fail-closed path is driven.
func (m *MockStore) BlockHasReferencesGlobal(orgID uuid.UUID, blockID string) (bool, error) {
	m.mu.Lock()
	m.blockHasReferencesGlobalCalls++
	globalErr := m.blockHasReferencesGlobalErr
	m.mu.Unlock()
	if globalErr != nil {
		return false, globalErr
	}
	return m.blockHasReferencesShared(orgID, blockID)
}

func (m *MockStore) blockHasReferencesShared(orgID uuid.UUID, blockID string) (bool, error) {
	m.mu.RLock()
	current := len(m.blockReferences[fmt.Sprintf("%s:%s", orgID, blockID)]) > 0
	hook := m.blockHasReferencesHook
	err := m.blockHasReferencesErr
	m.mu.RUnlock()
	if err != nil {
		return false, err
	}
	if hook != nil {
		return hook(orgID, blockID, current)
	}
	return current, nil
}

// SetBlockHasReferencesGlobalErrForTest drives the fail-closed path: an unreachable
// DC makes the EACH_QUORUM read fail, and GC must not delete on that uncertainty.
func (m *MockStore) SetBlockHasReferencesGlobalErrForTest(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.blockHasReferencesGlobalErr = err
}

// ValidateDestructiveGCTopology always passes for the mock: an in-memory store has
// no keyspace whose replication could invalidate the per-DC EACH_QUORUM argument.
// It is implemented because the gate is part of GCStore, so a store that forgets it
// fails to compile rather than silently disarming the destructive gate.
func (m *MockStore) ValidateDestructiveGCTopology() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.validateDestructiveTopologyErr
}

// SetValidateDestructiveGCTopologyErrForTest makes the mock's gate reject, so tests
// can drive the fail-closed path through the real wiring instead of overriding the
// worker's gate function.
func (m *MockStore) SetValidateDestructiveGCTopologyErrForTest(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.validateDestructiveTopologyErr = err
}

// ReleaseStaleBlockClaim mirrors the Cassandra semantics: silent no-op when there is
// nothing to release, so the common "referenced block, never claimed" path neither
// errors nor warns; a stale claim is released regardless of which attempt owns it;
// and a real failure is injectable to prove that callers refuse to settle a candidate
// whose fence they could not confirm gone.
func (m *MockStore) ReleaseStaleBlockClaim(orgID uuid.UUID, blockID string, staleBefore time.Time) (BlockClaimReleaseOutcome, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.releaseStaleBlockClaimErr != nil {
		return BlockClaimAbsent, m.releaseStaleBlockClaimErr
	}
	b, ok := m.blocks[fmt.Sprintf("%s:%s", orgID, blockID)]
	if !ok {
		return BlockClaimAbsent, nil
	}
	if b.GCState != db.BlockGCStateDeleting {
		return BlockClaimAbsent, nil
	}
	if b.GCClaimedAt == nil || b.GCClaimedAt.After(staleBefore) {
		return BlockClaimTooFresh, nil
	}
	b.GCState = ""
	b.GCClaimID = ""
	b.GCClaimedAt = nil
	return BlockClaimReleased, nil
}

// SetReleaseStaleBlockClaimErrForTest injects a failure into the stale-claim release.
func (m *MockStore) SetReleaseStaleBlockClaimErrForTest(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.releaseStaleBlockClaimErr = err
}

// BackdateBlockClaimForTest ages an existing delete claim, so a test can model an
// abandoned fence while leaving the worker on the real clock.
//
// The alternative — seeding a fresh claim and pushing w.clock() forward past
// blockDeleteClaimStaleAfter — has a trap that has already produced a misleading
// green. postponeItem stamps the requeued row with w.clock(), while
// Queue.DequeueBatch derives its cutoff from time.Now(); those are the same instant in
// production and only diverge under a test clock. A worker driven 15 minutes into the
// future therefore requeues postponed items into the future, where no later pass can
// dequeue them — so a multi-pass assertion would be testing an empty queue rather than
// repeated refusals. Backdating the claim instead keeps both clocks agreeing.
func (m *MockStore) BackdateBlockClaimForTest(orgID uuid.UUID, blockID string, claimedAt time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.blocks[fmt.Sprintf("%s:%s", orgID, blockID)]
	if !ok {
		return
	}
	at := claimedAt.UTC()
	b.GCClaimedAt = &at
}

// SetClaimBlockDeleteErrForTest injects a failure into the LWT claim.
//
// An LWT can fail for availability reasons depending on its serial and regular
// consistency levels and on which replicas are reachable — Paxos needs its serial
// quorum on top of the ordinary one, so contention and a degraded cluster surface here
// first. This hook drives the worker's TREATMENT of such a failure (postpone, not DLQ);
// it deliberately does not model any rule of the form "one DC down implies the claim
// fails". SERIAL and LOCAL_SERIAL are consistencies of the Paxos phase; EACH_QUORUM,
// the level X2 turns on, is a per-datacenter requirement of an ordinary read. Conflating
// the two is what produced an incorrect advisory once already.
func (m *MockStore) SetClaimBlockDeleteErrForTest(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.claimBlockDeleteErr = err
}

// BlockHasReferencesCallCountsForTest reports how many liveness reads of each kind
// were issued, so a test can assert which one authorized a delete.
func (m *MockStore) BlockHasReferencesCallCountsForTest() (local, global int) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.blockHasReferencesLocalCalls, m.blockHasReferencesGlobalCalls
}

func (m *MockStore) BlockReferenceExists(orgID uuid.UUID, blockID, referrer string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.blockReferenceExistsErr != nil {
		return false, m.blockReferenceExistsErr
	}
	refs, ok := m.blockReferences[fmt.Sprintf("%s:%s", orgID, blockID)]
	if !ok {
		return false, nil
	}
	_, exists := refs[referrer]
	return exists, nil
}

// SetBlockHasReferencesHookForTest installs a deterministic concurrency hook for
// component tests that drive the real worker against MockStore.
func (m *MockStore) SetBlockHasReferencesHookForTest(hook func(orgID uuid.UUID, blockID string, current bool) (bool, error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.blockHasReferencesHook = hook
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

func (m *MockStore) GetLibraryBlockRepresentationID(orgID, libraryID uuid.UUID) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.blockRepresentationIDForLibraryLocked(orgID, libraryID)
}

func (m *MockStore) ResolveBlockIDs(orgID, libraryID uuid.UUID, blockRepresentationID string, blockIDs []string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	representationID := strings.TrimSpace(blockRepresentationID)
	if representationID == "" {
		resolvedRepresentationID, err := m.blockRepresentationIDForLibraryLocked(orgID, libraryID)
		if err == nil {
			representationID = resolvedRepresentationID
		} else if !errors.Is(err, gocql.ErrNotFound) {
			return nil, err
		}
	}

	// Mirror CassandraStore.resolveBlockIDsConcurrent exactly: canonicalize before
	// classifying by hex content, only resolve hex SHA-1 ids, and only accept a hex
	// SHA-256 internal id — otherwise keep the original (lenient). Without this the
	// mock would pass GC tests that behave differently against Cassandra for
	// uppercase / padded / non-hex garbage ids.
	resolved := make([]string, len(blockIDs))
	for i, blockID := range blockIDs {
		normalized := db.NormalizeBlockID(blockID)
		resolved[i] = normalized
		if !db.IsSHA1BlockID(normalized) {
			continue
		}
		if representationID != "" {
			if internalID, ok := m.mappings[mockMappingKey(orgID, representationID, normalized)]; ok {
				if canonical := db.NormalizeBlockID(internalID); db.IsSHA256BlockID(canonical) {
					resolved[i] = canonical
				}
			}
			continue
		}

		plainInternalID, plainOK := m.mappings[mockMappingKey(orgID, db.PlainBlockRepresentationID, normalized)]
		plainCanonical := db.NormalizeBlockID(plainInternalID)
		plainValid := plainOK && db.IsSHA256BlockID(plainCanonical)
		encryptedInternalID, encryptedOK := m.mappings[mockMappingKey(orgID, db.EncryptedLibraryBlockRepresentationID(libraryID.String()), normalized)]
		encryptedCanonical := db.NormalizeBlockID(encryptedInternalID)
		encryptedValid := encryptedOK && db.IsSHA256BlockID(encryptedCanonical)

		switch {
		case plainValid && encryptedValid && plainCanonical == encryptedCanonical:
			resolved[i] = plainCanonical
		case plainValid && encryptedValid:
			metrics.GCAuditEventsTotal.WithLabelValues("gc_block_mapping_unresolved_ambiguous_representation").Inc()
		case plainValid:
			resolved[i] = plainCanonical
		case encryptedValid:
			resolved[i] = encryptedCanonical
		}
	}
	return resolved, nil
}

func (m *MockStore) EnsureBlockGCCandidate(orgID uuid.UUID, blockID, storageClass string, candidateAt time.Time) (time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ensureBlockGCCandidateErr != nil {
		return time.Time{}, m.ensureBlockGCCandidateErr
	}
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
	// Mirror the production canonical row's TTL. The projection intentionally
	// remains durable, so callers can exercise canonical-missing recovery.
	if !expiry.ExpiresAt.Add(time.Duration(db.ProvisionalBlockRefTrackerTTLGraceSeconds) * time.Second).After(time.Now().UTC()) {
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
	if m.deleteProvisionalProjectionErr != nil {
		return m.deleteProvisionalProjectionErr
	}
	delete(m.provisionalBlockRefExpiryProjections, newMockProvisionalBlockRefExpiryProjectionKey(orgID, blockID, referrer, expiresAt))
	return nil
}

func (m *MockStore) ClaimBlockDelete(orgID uuid.UUID, blockID, claimID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.claimBlockDeleteErr != nil {
		return false, m.claimBlockDeleteErr
	}
	key := fmt.Sprintf("%s:%s", orgID, blockID)
	b, ok := m.blocks[key]
	if !ok {
		claimedAt := time.Now().UTC()
		m.blocks[key] = &mockBlock{
			OrgID:       orgID,
			BlockID:     blockID,
			GCState:     db.BlockGCStateDeleting,
			GCClaimID:   claimID,
			GCClaimedAt: &claimedAt,
		}
		return true, nil
	}
	if b.GCState == db.BlockGCStateDeleting {
		return b.GCClaimID == claimID, nil
	}
	b.GCState = db.BlockGCStateDeleting
	b.GCClaimID = claimID
	claimedAt := time.Now().UTC()
	b.GCClaimedAt = &claimedAt
	return true, nil
}

// SetReleaseBlockClaimErrForTest injects a failure into the post-claim release.
//
// The interesting injection is a NON-availability error. Those used to surface as
// ordinary failures, spend the item's retry budget and reach the DLQ while
// gc_state='deleting' stayed on the row — a permanent upload fence on a block the
// walk may have just proven to be still referenced. See Worker.releaseBlockClaim.
func (m *MockStore) SetReleaseBlockClaimErrForTest(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.releaseBlockClaimErr = err
}

func (m *MockStore) ReleaseBlockClaim(orgID uuid.UUID, blockID, claimID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.releaseBlockClaimErr != nil {
		return m.releaseBlockClaimErr
	}
	if b, ok := m.blocks[fmt.Sprintf("%s:%s", orgID, blockID)]; ok {
		if b.GCState != db.BlockGCStateDeleting || b.GCClaimID != claimID {
			return fmt.Errorf("block delete claim release not applied for %s", blockID)
		}
		b.GCState = ""
		b.GCClaimID = ""
		b.GCClaimedAt = nil
		return nil
	}
	return fmt.Errorf("block delete claim release not applied for %s", blockID)
}

func (m *MockStore) DeleteClaimedBlockStub(orgID uuid.UUID, blockID, claimID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.deleteClaimedBlockStubForceFalse {
		return false, nil
	}

	key := fmt.Sprintf("%s:%s", orgID, blockID)
	b, ok := m.blocks[key]
	if !ok || b.CreatedAt != nil || b.StorageClassPresent || b.GCState != db.BlockGCStateDeleting || b.GCClaimID != claimID || b.GCClaimedAt == nil {
		return false, nil
	}
	delete(m.blocks, key)
	return true, nil
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
	if m.libraryExistsErr != nil {
		return false, m.libraryExistsErr
	}
	_, ok := m.libraries[libraryID]
	return ok, nil
}

func (m *MockStore) CanonicalLibraryExists(orgID, libraryID uuid.UUID) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.canonicalLibraryExistsErr != nil {
		return false, m.canonicalLibraryExistsErr
	}
	lib, ok := m.libraries[libraryID]
	return ok && lib.OrgID == orgID, nil
}

func (m *MockStore) FindOrgForLibrary(libraryID uuid.UUID) (uuid.UUID, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.findOrgForLibraryErr != nil {
		return uuid.Nil, m.findOrgForLibraryErr
	}

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

	if m.reconcileStorageCountersHook != nil {
		m.reconcileStorageCountersHook()
	}

	if len(m.storageCounterReconciliations) == 0 {
		return 0, nil
	}

	expected := make(map[string]traffic.StorageSnapshot, len(m.storageCounterReconciliations))
	expectedPlatformByShard := make(map[int]traffic.StorageSnapshot, traffic.CounterShardCount)
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
			shard := traffic.CounterShard(lib.OrgID.String())
			snap := expectedPlatformByShard[shard]
			snap.BytesUsed += libSnapshot.BytesUsed
			snap.FileCount += libSnapshot.FileCount
			expectedPlatformByShard[shard] = snap
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
		if scope == traffic.PlatformStorageScope() {
			shards := make(map[int]traffic.StorageSnapshot, traffic.CounterShardCount)
			traffic.ForEachCounterShard(func(shard int) {
				shards[shard] = expectedPlatformByShard[shard]
			})
			m.storageShardSnapshots[scope] = shards
		} else {
			m.storageSnapshots[scope] = expected[scope]
		}
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
			stored := strings.TrimSpace(lib.BlockRepresentationID)
			resolved, repErr := db.CanonicalBlockRepresentationIDForLibrary(lib.LibraryID.String(), lib.Encrypted, stored)
			if repErr != nil {
				results = append(results, LibraryTTLInfo{
					OrgID:                 lib.OrgID,
					LibraryID:             lib.LibraryID,
					HeadCommitID:          lib.HeadCommitID,
					BlockRepresentationID: stored,
					RepresentationInvalid: true,
					VersionTTLDays:        lib.VersionTTLDays,
				})
				continue
			}
			results = append(results, LibraryTTLInfo{
				OrgID:                   lib.OrgID,
				LibraryID:               lib.LibraryID,
				HeadCommitID:            lib.HeadCommitID,
				BlockRepresentationID:   resolved,
				RepresentationDefaulted: stored == "",
				VersionTTLDays:          lib.VersionTTLDays,
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
			stored := strings.TrimSpace(lib.BlockRepresentationID)
			resolved, repErr := db.CanonicalBlockRepresentationIDForLibrary(lib.LibraryID.String(), lib.Encrypted, stored)
			if repErr != nil {
				results = append(results, LibraryAutoDeleteInfo{
					OrgID:                 lib.OrgID,
					LibraryID:             lib.LibraryID,
					HeadCommitID:          lib.HeadCommitID,
					BlockRepresentationID: stored,
					RepresentationInvalid: true,
					AutoDeleteDays:        lib.AutoDeleteDays,
				})
				continue
			}
			results = append(results, LibraryAutoDeleteInfo{
				OrgID:                   lib.OrgID,
				LibraryID:               lib.LibraryID,
				HeadCommitID:            lib.HeadCommitID,
				BlockRepresentationID:   resolved,
				RepresentationDefaulted: stored == "",
				AutoDeleteDays:          lib.AutoDeleteDays,
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
func (m *MockStore) ScanAllGroupShares(ctx context.Context, visit func(GroupShareInfo) error) error {
	m.mu.RLock()
	rows := make([]GroupShareInfo, 0, len(m.shares))
	for _, share := range m.shares {
		if share.SharedToType != "group" {
			continue
		}
		rows = append(rows, GroupShareInfo{
			LibraryID:    share.LibraryID,
			ShareID:      share.ShareID,
			SharedTo:     share.SharedTo,
			SharedToType: share.SharedToType,
			OrgID:        share.OrgID,
		})
	}
	m.mu.RUnlock()
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visit(row); err != nil {
			return err
		}
	}
	return nil
}
func (m *MockStore) GroupExists(orgID, groupID uuid.UUID) (bool, error) {
	m.groupExistsCalls.Add(1)
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.groupExistsErr != nil {
		return false, m.groupExistsErr
	}
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
		if !dl.PurgeRequestedAt.IsZero() || dl.DeletedAt.Before(cutoff) {
			result = append(result, DeletedLibraryInfo{
				OrgID:                 dl.OrgID,
				LibraryID:             dl.LibraryID,
				BlockRepresentationID: strings.TrimSpace(dl.BlockRepresentationID),
				StorageClass:          dl.StorageClass,
				DeletedAt:             dl.DeletedAt,
				PurgeRequestedAt:      dl.PurgeRequestedAt,
			})
		}
	}
	return result, nil
}
func (m *MockStore) HardDeleteLibrary(orgID, libraryID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.libraryDestructiveCalls = append(m.libraryDestructiveCalls, "HardDeleteLibrary")
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
	blockRepresentationID, err := db.CanonicalBlockRepresentationIDForLibrary(lib.LibraryID.String(), lib.Encrypted, lib.BlockRepresentationID)
	if err != nil {
		return err
	}
	lib.DeletedAt = time.Now()
	m.deletedLibraries[libraryID] = &mockDeletedLibrary{
		OrgID:                 orgID,
		LibraryID:             libraryID,
		BlockRepresentationID: blockRepresentationID,
		StorageClass:          lib.StorageClass,
		DeletedAt:             lib.DeletedAt,
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
	if m.forceRenewLibraryLockNotOwned {
		// Simulate a lease lost to TTL expiry or a concurrent restore between acquire and fence.
		return false, nil
	}
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
	m.mu.Lock()
	m.libraryDestructiveCalls = append(m.libraryDestructiveCalls, "DeleteLibraryStorageCounter")
	if m.deleteLibraryStorageCounterFor == nil {
		m.deleteLibraryStorageCounterFor = make(map[uuid.UUID]int)
	}
	m.deleteLibraryStorageCounterFor[libraryID]++
	m.mu.Unlock()
	return nil // storage_counters not otherwise simulated
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
	mu             sync.Mutex
	DeletedKeys    []string
	ScopedDeletes  []ScopedBlockDelete
	ResolvedStores []ScopedBlockStoreRequest

	// failTimes is the number of upcoming DeleteBlock calls that should
	// return an error before the next call succeeds. Decremented per call.
	// Zero means "always succeed".
	failTimes int
	// failAlways overrides failTimes and makes every DeleteBlock fail.
	failAlways bool
	// failErr is the error returned while failing.
	failErr error
}

type ScopedBlockDelete struct {
	OrgID        string
	StorageClass string
	BlockID      string
}

type ScopedBlockStoreRequest struct {
	OrgID        string
	StorageClass string
}

func (p *MockStorageProvider) GetBlockStoreForOrg(orgID, storageClass string) (BlockStoreDeleter, error) {
	trimmedOrgID := strings.TrimSpace(orgID)
	if trimmedOrgID == "" {
		return nil, fmt.Errorf("org-scoped block store requires a non-empty org id")
	}
	parsedOrgID, err := uuid.Parse(trimmedOrgID)
	if err != nil {
		return nil, fmt.Errorf("invalid org id %q: %w", orgID, err)
	}
	if strings.TrimSpace(storageClass) == "" {
		return nil, fmt.Errorf("storage class is empty")
	}
	p.mu.Lock()
	p.ResolvedStores = append(p.ResolvedStores, ScopedBlockStoreRequest{OrgID: parsedOrgID.String(), StorageClass: storageClass})
	p.mu.Unlock()
	return &mockBlockDeleter{provider: p, orgID: parsedOrgID.String(), storageClass: storageClass}, nil
}

// DeletedBlocks returns the list of block IDs that were deleted.
func (p *MockStorageProvider) DeletedBlocks() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string{}, p.DeletedKeys...)
}

func (p *MockStorageProvider) ScopedBlockDeletes() []ScopedBlockDelete {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]ScopedBlockDelete{}, p.ScopedDeletes...)
}

func (p *MockStorageProvider) BlockStoreRequests() []ScopedBlockStoreRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]ScopedBlockStoreRequest{}, p.ResolvedStores...)
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
	provider     *MockStorageProvider
	orgID        string
	storageClass string
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
	d.provider.ScopedDeletes = append(d.provider.ScopedDeletes, ScopedBlockDelete{
		OrgID:        d.orgID,
		StorageClass: d.storageClass,
		BlockID:      blockID,
	})
	return nil
}

// --- S3 orphan recovery (mock) ---

func (m *MockStore) GetS3OrphanGlobal(orgID uuid.UUID, blockID string) (S3OrphanInfo, bool, error) {
	m.mu.Lock()
	m.getS3OrphanGlobalCalls++
	call := m.getS3OrphanGlobalCalls
	err := m.getS3OrphanGlobalErr
	var info S3OrphanInfo
	existing, found := m.s3Orphans[fmt.Sprintf("%s:%s", orgID, blockID)]
	if found {
		info = *existing
	}
	hook := m.getS3OrphanGlobalHook
	m.mu.Unlock()
	if err != nil {
		return S3OrphanInfo{}, false, err
	}
	if !found {
		return S3OrphanInfo{}, false, nil
	}
	if hook != nil {
		info, err = hook(orgID, blockID, call, info)
		if err != nil {
			return S3OrphanInfo{}, false, err
		}
	}
	return info, true, nil
}

func (m *MockStore) StartBlockDeleteOrphan(orgID uuid.UUID, blockID, storageClass, representationID, externalSHA1 string, now time.Time) (time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%s:%s", orgID, blockID)
	if existing, ok := m.s3Orphans[key]; ok {
		if m.startBlockDeleteOrphanResetRace {
			firstSeenAt := existing.FirstSeenAt
			delete(m.s3Orphans, key)
			delete(m.s3OrphanProjections, newMockS3OrphanProjectionKey(orgID, blockID, firstSeenAt))
			return firstSeenAt, fmt.Errorf("reset S3 orphan recovery state for org=%s block=%s: row disappeared before update", orgID, blockID)
		}
		existing.StorageClass = storageClass
		existing.RepresentationID = strings.TrimSpace(representationID)
		existing.ExternalSHA1 = strings.TrimSpace(externalSHA1)
		existing.RecoveryPhase = S3OrphanPhasePendingS3
		existing.LastAttemptAt = now
		existing.RetryCount = 0
		existing.LastError = ""
		m.upsertS3OrphanProjection(existing.OrgID, existing.BlockID, existing.FirstSeenAt)
		return existing.FirstSeenAt, nil
	}
	orphan := &S3OrphanInfo{
		OrgID:            orgID,
		BlockID:          blockID,
		StorageClass:     storageClass,
		RepresentationID: strings.TrimSpace(representationID),
		ExternalSHA1:     strings.TrimSpace(externalSHA1),
		RecoveryPhase:    S3OrphanPhasePendingS3,
		FirstSeenAt:      now.UTC(),
		LastAttemptAt:    now,
	}
	m.s3Orphans[key] = orphan
	m.upsertS3OrphanProjection(orphan.OrgID, orphan.BlockID, orphan.FirstSeenAt)
	return orphan.FirstSeenAt, nil
}

func (m *MockStore) MarkS3OrphanMappingCleanupPending(orgID uuid.UUID, blockID, representationID, externalSHA1 string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := fmt.Sprintf("%s:%s", orgID, blockID)
	if existing, ok := m.s3Orphans[key]; ok {
		existing.RepresentationID = strings.TrimSpace(representationID)
		existing.ExternalSHA1 = strings.TrimSpace(externalSHA1)
		existing.RecoveryPhase = S3OrphanPhasePendingMappingCleanup
		existing.LastAttemptAt = now
		existing.LastError = ""
		m.upsertS3OrphanProjection(existing.OrgID, existing.BlockID, existing.FirstSeenAt)
	}
	return nil
}

func (m *MockStore) UpdateS3OrphanAttempt(orgID uuid.UUID, blockID string, expectedFirstSeenAt time.Time, errMsg string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if expectedFirstSeenAt.IsZero() {
		return nil
	}
	expectedFirstSeenAt = expectedFirstSeenAt.UTC().Truncate(time.Millisecond)
	key := fmt.Sprintf("%s:%s", orgID, blockID)
	if existing, ok := m.s3Orphans[key]; ok {
		storedFirstSeenAt := existing.FirstSeenAt.UTC().Truncate(time.Millisecond)
		if !storedFirstSeenAt.Equal(expectedFirstSeenAt) {
			return nil
		}
		if s3OrphanRemainingTTLSeconds(expectedFirstSeenAt, now) <= 0 {
			return nil
		}
		existing.LastAttemptAt = now
		existing.RetryCount++
		existing.LastError = errMsg
	}
	return nil
}

func (m *MockStore) DeleteS3Orphan(orgID uuid.UUID, blockID string, firstSeenAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.deleteS3OrphanErrOnce != nil {
		err := m.deleteS3OrphanErrOnce
		m.deleteS3OrphanErrOnce = nil
		return err
	}
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

func (m *MockStore) ListS3OrphansByDay(day time.Time, bucket int, limit int) ([]S3OrphanDiscoveryInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if limit <= 0 {
		limit = 100
	}
	targetDay := db.GCProjectionUTCDate(day)
	var out []S3OrphanDiscoveryInfo
	for key, orphan := range m.s3OrphanProjections {
		if !key.FirstSeenDay.Equal(targetDay) {
			continue
		}
		if key.Bucket != bucket {
			continue
		}
		out = append(out, S3OrphanDiscoveryInfo{
			OrgID:       orphan.OrgID,
			BlockID:     orphan.BlockID,
			FirstSeenAt: orphan.FirstSeenAt,
		})
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

// SetStartBlockDeleteOrphanResetRaceForTest makes the mock model the canonical
// row disappearing after the CAS read and before the conditional reset update.
func (m *MockStore) SetStartBlockDeleteOrphanResetRaceForTest(enabled bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.startBlockDeleteOrphanResetRace = enabled
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
