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

	"github.com/Sesame-Disk/sesamefs/internal/config"
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

	// block GC candidates keyed by their logical block and exact physical incarnation.
	blockGCCandidates map[mockBlockGCCandidateKey]*mockBlockGCCandidate
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

	// in-progress library lifecycle fences keyed by libraryID.
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
	libraryDestructiveCalls                        []string
	deleteLibraryStorageCounterFor                 map[uuid.UUID]int
	deleteGroupFullErr                             error
	reconcileStorageCountersHook                   func()
	acquireOrgHardDeleteLockHook                   func(orgID uuid.UUID)
	beginOrgPurgeHook                              func(orgID uuid.UUID)
	getBlockRefCountErr                            error
	blockExistsErr                                 error
	blockExistsCalls                               int
	libraryExistsErr                               error
	canonicalLibraryExistsErr                      error
	forceRenewLibraryLockNotOwned                  bool
	groupExistsErr                                 error
	groupExistsCalls                               atomic.Int64
	findOrgForLibraryErr                           error
	blockHasReferencesHook                         func(orgID uuid.UUID, blockID string, current bool) (bool, error)
	blockHasReferencesErr                          error
	blockHasReferencesGlobalErr                    error
	blockHasReferencesLocalCalls                   int
	blockHasReferencesGlobalCalls                  int
	releaseStaleBlockClaimErr                      error
	getBlockGCCandidateErr                         error
	deleteBlockGCCandidateDiscoveryErr             error
	claimBlockDeleteSettleErr                      error
	claimBlockDeleteEachQuorumErr                  error
	getBlockInfoHook                               func(BlockInfo) BlockInfo
	getBlockInfoErr                                error
	claimAttempts                                  []BlockDeleteAuthority
	releaseBlockClaimErr                           error
	claimBlockDeleteErr                            error
	validateDestructiveTopologyErr                 error
	blockReferenceExistsErr                        error
	ensureBlockGCCandidateErr                      error
	deleteProvisionalProjectionErr                 error
	getS3OrphanGlobalErr                           error
	getS3OrphanGlobalCalls                         int
	getS3OrphanGlobalHook                          func(orgID uuid.UUID, blockID string, call int, info S3OrphanInfo) (S3OrphanInfo, error)
	deleteS3OrphanErrOnce                          error
	markS3OrphanErrOnce                            error
	startBlockDeleteOrphanAmbiguousOnce            bool
	startBlockDeleteOrphanNotPublishedOnce         bool
	startBlockDeleteOrphanCanonicalUnconfirmedOnce bool

	// optional test hooks for reproducing concurrency windows deterministically.
	getQueueSizeHook                        func(orgID uuid.UUID, size int)
	removeActiveOrgHook                     func(orgID uuid.UUID, activeBefore time.Time)
	recalculateStatsHook                    func(orgID uuid.UUID)
	startBlockDeleteOrphanProjectionErrOnce error
	releaseBlockClaimHook                   func()
	// requeueItemErr, when non-nil, forces RequeueItem to return this error
	// without mutating state. Used to exercise IncrementRetry failure paths
	// where the LoggedBatch never applied.
	requeueItemErr error
	// requeueItemErrAfterMutate, when non-nil, forces RequeueItem to apply
	// the queue mutation AND THEN return this error. Models the ambiguous
	// LoggedBatch case (Cassandra timeout / unavailable) where the batch
	// committed at the cluster but the client observed a failure.
	requeueItemErrAfterMutate error
	// Queue mutation counters let worker tests prove that a stale late loser does not
	// accidentally enter a queue lifecycle path.
	queueCompleteCalls atomic.Int64
	queueRequeueCalls  atomic.Int64
	queueFailCalls     atomic.Int64
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
	// Durable D tombstones keyed by "orgID:blockID:claimID". Never deleted.
	blockDeleteLifecycles                  map[string]*blockDeleteLifecycleRow
	commitHandoffErr                       error
	commitHandoffSettleErr                 error
	pauseAfterLifecycleBeforeOrphan        chan struct{}
	pauseAfterLifecycleBeforeOrphanEntered chan struct{}
	commitHandoffEmptyCASOnce              bool
}

var _ GCStore = (*MockStore)(nil)

type mockBlock struct {
	OrgID               uuid.UUID
	BlockID             string
	StorageClass        string
	StorageClassPresent bool
	StorageKey          string
	CreatedAt           *time.Time
	GCState             string
	GCClaimID           string
	GCClaimedAt         *time.Time
	GCOrphanHandoff     *bool
	RepresentationID    string
	Sha1                string
}

type mockPendingItemKey struct {
	OrgID      uuid.UUID
	LibraryID  uuid.UUID
	ItemType   ItemType
	ItemID     string
	IdentityAt time.Time
	Target     BlockDeleteTarget
}

type mockBlockGCCandidate struct {
	OrgID       uuid.UUID
	BlockID     string
	Target      BlockDeleteTarget
	CandidateAt time.Time
}

type mockBlockGCCandidateKey struct {
	OrgID   uuid.UUID
	BlockID string
	Target  BlockDeleteTarget
}

type mockBlockGCCandidateProjectionKey struct {
	CandidateDay time.Time
	Bucket       int
	CandidateAt  time.Time
	OrgID        uuid.UUID
	BlockID      string
	Target       BlockDeleteTarget
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
		blockGCCandidates:                    make(map[mockBlockGCCandidateKey]*mockBlockGCCandidate),
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
		blockDeleteLifecycles:                make(map[string]*blockDeleteLifecycleRow),
	}
}

func newMockPendingItemKey(orgID, libraryID uuid.UUID, itemType ItemType, itemID string, identity GCItemIdentity) mockPendingItemKey {
	return mockPendingItemKey{
		OrgID:      orgID,
		LibraryID:  libraryID,
		ItemType:   itemType,
		ItemID:     itemID,
		IdentityAt: identity.IdentityAt,
		Target:     identity.Target(),
	}
}

func sameBlockGCCandidateIdentity(left, right BlockGCCandidateIdentity) bool {
	return left.Target == right.Target && left.CandidateAt.Equal(right.CandidateAt)
}

// sameGCItemIdentity mirrors the Cassandra primary key: two rows are the same
// work item only when every clustering column matches.
func sameGCItemIdentity(left, right GCItemIdentity) bool {
	return left.IdentityAt.Equal(right.IdentityAt) && left.Target() == right.Target()
}

func newMockBlockGCCandidateKey(orgID uuid.UUID, blockID string, target BlockDeleteTarget) mockBlockGCCandidateKey {
	return mockBlockGCCandidateKey{OrgID: orgID, BlockID: blockID, Target: target}
}

func newMockBlockGCCandidateProjectionKey(orgID uuid.UUID, blockID string, target BlockDeleteTarget, candidateAt time.Time) mockBlockGCCandidateProjectionKey {
	candidateAt = candidateAt.UTC()
	return mockBlockGCCandidateProjectionKey{
		CandidateDay: db.GCProjectionUTCDate(candidateAt),
		Bucket:       db.GCDiscoveryBucket(orgID.String(), blockID),
		CandidateAt:  candidateAt,
		OrgID:        orgID,
		BlockID:      blockID,
		Target:       target,
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
	key := newMockBlockGCCandidateProjectionKey(candidate.OrgID, candidate.BlockID, candidate.Target, candidate.CandidateAt)
	m.blockGCCandidateProjections[key] = BlockGCCandidateInfo{
		OrgID:       candidate.OrgID,
		BlockID:     candidate.BlockID,
		Target:      candidate.Target,
		CandidateAt: candidate.CandidateAt.UTC(),
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

func (m *MockStore) upsertPendingItem(orgID, libraryID uuid.UUID, itemType ItemType, itemID string, identity GCItemIdentity, expiresAt *time.Time) {
	// Mirror the Cassandra store: block pending rows are always keyed under uuid.Nil.
	libraryID = pendingItemLibraryID(itemType, libraryID)
	key := newMockPendingItemKey(orgID, libraryID, itemType, itemID, identity)
	if expiresAt == nil {
		m.pendingItems[key] = nil
		return
	}
	expiry := *expiresAt
	m.pendingItems[key] = &expiry
}

func (m *MockStore) deletePendingItem(orgID, libraryID uuid.UUID, itemType ItemType, itemID string, identity GCItemIdentity) {
	libraryID = pendingItemLibraryID(itemType, libraryID)
	delete(m.pendingItems, newMockPendingItemKey(orgID, libraryID, itemType, itemID, identity))
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
		StorageKey:          MockCanonicalStorageKey(orgID.String(), blockID),
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

// SetBlockStorageKeyForTest overwrites a seeded block's persisted locator so a
// test can model a row whose storage_key does not belong to its own org — the
// corruption/bad-backfill case the destructive paths must refuse.
func (m *MockStore) SetBlockStorageKeyForTest(orgID uuid.UUID, blockID, storageKey string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if block, ok := m.blocks[fmt.Sprintf("%s:%s", orgID, blockID)]; ok {
		block.StorageKey = storageKey
	}
}

// AddBlockGCCandidate seeds a candidate. It captures the exact incarnation from the
// seeded canonical row when one exists, so a test that seeds a block and a candidate
// gets the same consistent pair production would have produced. Tests that deliberately
// model a candidate for a DEAD incarnation seed it first and mutate the block after.
func (m *MockStore) AddBlockGCCandidate(orgID uuid.UUID, blockID, storageClass string, candidateAt time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.addBlockGCCandidateLocked(orgID, blockID, storageClass, candidateAt)
}

func (m *MockStore) addBlockGCCandidateLocked(orgID uuid.UUID, blockID, storageClass string, candidateAt time.Time) {
	target := BlockDeleteTarget{StorageClass: storageClass, StorageKey: MockCanonicalStorageKey(orgID.String(), blockID)}
	if resolved, err := m.resolveBlockDeleteTargetLocked(orgID, blockID); err == nil {
		target = resolved
	}
	candidate := &mockBlockGCCandidate{
		OrgID:       orgID,
		BlockID:     blockID,
		Target:      target,
		CandidateAt: candidateAt.UTC(),
	}
	m.blockGCCandidates[newMockBlockGCCandidateKey(orgID, blockID, target)] = candidate
	m.upsertBlockGCCandidateProjection(candidate)
}

// BlockDeleteAuthorityForTest builds an authority bound to the block's CURRENT
// incarnation. Tests that need an authority for a DEAD incarnation build the struct
// themselves — the point of most of these tests is that the two differ.
func (m *MockStore) BlockDeleteAuthorityForTest(orgID uuid.UUID, blockID, claimID string, claimedAt time.Time) BlockDeleteAuthority {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var target BlockDeleteTarget
	if b, ok := m.blocks[fmt.Sprintf("%s:%s", orgID, blockID)]; ok {
		target = BlockDeleteTarget{StorageClass: b.StorageClass, StorageKey: b.StorageKey}
	}
	return BlockDeleteAuthority{Target: target, ClaimID: claimID, ClaimedAt: claimedAt.UTC()}
}

// SeedBlockClaimForTest installs a delete claim directly, modelling an attempt that
// claimed the row and then vanished. It returns the authority that owns it so a test
// can assert who may and may not release it.
func (m *MockStore) SeedBlockClaimForTest(orgID uuid.UUID, blockID, claimID string, claimedAt time.Time) BlockDeleteAuthority {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.blocks[fmt.Sprintf("%s:%s", orgID, blockID)]
	if !ok {
		return BlockDeleteAuthority{}
	}
	claimedAtUTC := claimedAt.UTC()
	b.GCState = db.BlockGCStateDeleting
	b.GCClaimID = claimID
	b.GCClaimedAt = &claimedAtUTC
	return BlockDeleteAuthority{
		Target:    BlockDeleteTarget{StorageClass: b.StorageClass, StorageKey: b.StorageKey},
		ClaimID:   claimID,
		ClaimedAt: claimedAtUTC,
	}
}

// SetBlockGCStateForTest installs an arbitrary gc_state owner, so a test can model a
// claim belonging to another subsystem — notably the upload path's repairing_stub.
func (m *MockStore) SetBlockGCStateForTest(orgID uuid.UUID, blockID, gcState, claimID string, claimedAt time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.blocks[fmt.Sprintf("%s:%s", orgID, blockID)]
	if !ok {
		return
	}
	at := claimedAt.UTC()
	b.GCState = gcState
	b.GCClaimID = claimID
	b.GCClaimedAt = &at
}

// SetBlockGCCandidateTargetForTest rewrites a candidate's captured incarnation, so a
// test can model "this candidate was created for P1" independently of what the
// canonical row holds now.
func (m *MockStore) SetBlockGCCandidateTargetForTest(orgID uuid.UUID, blockID string, target BlockDeleteTarget) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.legacyBlockGCCandidateLocked(orgID, blockID); ok {
		delete(m.blockGCCandidates, newMockBlockGCCandidateKey(c.OrgID, c.BlockID, c.Target))
		delete(m.blockGCCandidateProjections, newMockBlockGCCandidateProjectionKey(c.OrgID, c.BlockID, c.Target, c.CandidateAt))
		c.Target = target
		m.blockGCCandidates[newMockBlockGCCandidateKey(c.OrgID, c.BlockID, c.Target)] = c
		m.upsertBlockGCCandidateProjection(c)
	}
}

// GetBlockGCCandidateForTest exposes the stored candidate for assertions.
//
// It deliberately REFUSES an ambiguous logical block: with exact-P identity a
// block can legitimately hold several candidates, and answering with one of them
// would let a test assert against whichever the map happened to yield. A test
// that means a specific incarnation uses GetBlockGCCandidateExact.
func (m *MockStore) GetBlockGCCandidateForTest(orgID uuid.UUID, blockID string) (BlockGCCandidateInfo, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var found BlockGCCandidateInfo
	matches := 0
	for _, c := range m.blockGCCandidates {
		if c.OrgID != orgID || c.BlockID != blockID {
			continue
		}
		matches++
		found = BlockGCCandidateInfo{OrgID: c.OrgID, BlockID: c.BlockID, Target: c.Target, CandidateAt: c.CandidateAt}
	}
	if matches != 1 {
		return BlockGCCandidateInfo{}, false
	}
	return found, true
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
	for key := range m.blockGCCandidateProjections {
		if key.OrgID == orgID && key.BlockID == blockID && key.CandidateAt.Equal(candidateAt.UTC()) {
			delete(m.blockGCCandidateProjections, key)
		}
	}
}

// legacyBlockGCCandidateLocked supplies transitional callers that do not carry P. It
// refuses an ambiguous logical block because selecting either candidate would discard
// the identity that authorizes its lifecycle.
func (m *MockStore) legacyBlockGCCandidateLocked(orgID uuid.UUID, blockID string) (*mockBlockGCCandidate, bool) {
	var only *mockBlockGCCandidate
	for key, candidate := range m.blockGCCandidates {
		if key.OrgID != orgID || key.BlockID != blockID {
			continue
		}
		if only != nil {
			return nil, false
		}
		only = candidate
	}
	return only, only != nil
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
// The storage setters rewrite a canonical orphan after the lifecycle entry point
// has validated it, so recovery tests can model malformed legacy rows.
func (m *MockStore) SetS3OrphanStorageClassForTest(orgID uuid.UUID, blockID, storageClass string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if orphan, ok := m.s3Orphans[fmt.Sprintf("%s:%s", orgID, blockID)]; ok {
		orphan.StorageClass = storageClass
	}
}

func (m *MockStore) SetS3OrphanStorageKeyForTest(orgID uuid.UUID, blockID, storageKey string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if orphan, ok := m.s3Orphans[fmt.Sprintf("%s:%s", orgID, blockID)]; ok {
		orphan.StorageKey = storageKey
	}
}

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

// SetMarkS3OrphanMappingCleanupPendingErrOnceForTest makes the next phase
// advance fail without mutating the canonical row. This characterizes the
// window after a successful S3 delete and before the durable phase transition.
func (m *MockStore) SetMarkS3OrphanMappingCleanupPendingErrOnceForTest(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.markS3OrphanErrOnce = err
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
	// Canonicalize exactly like the Cassandra writer so a test that passes an
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
// RemoveBlockForTest deletes the canonical row, modelling an incarnation that died
// after its candidate was created. The candidate survives, which is the point: it is
// what carries the dead incarnation into the claim.
func (m *MockStore) RemoveBlockForTest(orgID uuid.UUID, blockID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.blocks, fmt.Sprintf("%s:%s", orgID, blockID))
}

func (m *MockStore) GetBlock(orgID uuid.UUID, blockID string) *mockBlock {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.blocks[fmt.Sprintf("%s:%s", orgID, blockID)]
}

func (m *MockStore) GetBlockInfo(orgID uuid.UUID, blockID string) (BlockInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.getBlockInfoErr != nil {
		return BlockInfo{}, m.getBlockInfoErr
	}
	block := m.blocks[fmt.Sprintf("%s:%s", orgID, blockID)]
	if block == nil {
		return BlockInfo{}, gocql.ErrNotFound
	}
	info := BlockInfo{BlockID: block.BlockID, StorageClass: block.StorageClass, StorageKey: block.StorageKey, CreatedAt: block.CreatedAt, Sha1: block.Sha1}
	if m.getBlockInfoHook != nil {
		info = m.getBlockInfoHook(info)
	}
	return info, nil
}

// SetGetBlockInfoHookForTest rewrites what the post-claim canonical re-read returns.
//
// It models the one thing the in-memory store cannot otherwise reach: GetBlockInfo is an
// ordinary read while the claim commits in the serial domain, so in production the two
// can legitimately disagree about which incarnation the row holds. Nothing downstream may
// publish or delete on the strength of that read.
func (m *MockStore) SetGetBlockInfoHookForTest(hook func(BlockInfo) BlockInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.getBlockInfoHook = hook
}

// SetGetBlockInfoErrorForTest makes the post-claim canonical re-read fail.
func (m *MockStore) SetGetBlockInfoErrorForTest(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.getBlockInfoErr = err
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

func (m *MockStore) QueueCompleteCallsForTest() int64 { return m.queueCompleteCalls.Load() }
func (m *MockStore) QueueRequeueCallsForTest() int64  { return m.queueRequeueCalls.Load() }
func (m *MockStore) QueueFailCallsForTest() int64     { return m.queueFailCalls.Load() }

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
	// Mirror the production guard exactly: a block work item must carry the exact
	// incarnation a zero-ref decision authorized, and this path cannot. Tests that
	// want a processable block item use EnqueueBlockForTest, which performs the
	// decision first and then enqueues its identity.
	if itemType == ItemBlock {
		return fmt.Errorf("item type %s requires an exact block GC candidate identity; use EnqueueBatch", itemType)
	}
	m.seedQueueItemRow(orgID, queuedAt, itemType, itemID, libraryID, storageClass, retryCount)
	return nil
}

// seedQueueItemRow appends a raw gc_queue row with an empty block representation.
// It backs both the guarded EnqueueItem and the test-only SeedQueueItemForTest
// (defined in a _test.go file so it never compiles into a production build); the
// block representation is intentionally always empty on this raw single-row path.
//
// FOR ItemBlock IT ALSO ENSURES THE CANDIDATE, because in production a block cannot
// reach the queue any other way: all three enqueue sites (Service.EnqueueBlock,
// Worker.enqueueZeroRefBlocks, Scanner.promoteBlockIfUnreferenced) call
// EnsureBlockGCCandidate first, and the scanner's own items come FROM the candidate
// projection. A mock that let a block item exist with no candidate behind it would let
// tests assert deletions that production would refuse for want of authority — the mock
// agreeing with the test while production disagreed, which is exactly how R19 hid.
//
// It is conditional on a canonical row existing, because the candidate captures its
// incarnation from one. Tests that deliberately model "no candidate" seed no block, or
// delete the candidate afterwards.
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
	m.upsertPendingItem(orgID, libraryID, itemType, itemID, GCItemIdentityAt(queuedAt), nil)
	m.activeQueueOrgs[orgID] = time.Now().UTC()
	m.dirtyQueueOrgs[orgID] = time.Now().UTC()
}

func (m *MockStore) EnqueueBatch(items []QueueItem) error {
	for _, item := range items {
		if err := validateQueueItemBlockRepresentation(item); err != nil {
			return err
		}
		if err := validateQueueItemBlockCandidateIdentity(item); err != nil {
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
		m.upsertPendingItem(item.OrgID, item.LibraryID, item.ItemType, item.ItemID, item.Identity(), nil)
		m.activeQueueOrgs[item.OrgID] = time.Now().UTC()
		m.dirtyQueueOrgs[item.OrgID] = time.Now().UTC()
	}
	return nil
}

func (m *MockStore) QueueItemExists(orgID uuid.UUID, queuedAt time.Time, itemType ItemType, itemID string, identity GCItemIdentity) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	identity, err := identity.requireIdentityAt("queue item existence check", itemID)
	if err != nil {
		return false, err
	}
	for _, item := range m.queue[orgID] {
		if item.QueuedAt.Equal(queuedAt) && item.ItemType == itemType && item.ItemID == itemID && sameGCItemIdentity(item.Identity(), identity) {
			return true, nil
		}
	}
	return false, nil
}

func (m *MockStore) PendingItemExists(orgID, libraryID uuid.UUID, itemType ItemType, itemID string, identity GCItemIdentity) (bool, error) {
	if err := requireBlockPendingProbeIdentity(itemType, itemID, identity); err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	libraryID = pendingItemLibraryID(itemType, libraryID)
	now := time.Now().UTC()
	for key, expiresAt := range m.pendingItems {
		if key.OrgID != orgID || key.LibraryID != libraryID || key.ItemType != itemType || key.ItemID != itemID || key.Target != identity.Target() {
			continue
		}
		if expiresAt != nil && !expiresAt.After(now) {
			delete(m.pendingItems, key)
			continue
		}
		if identity.IdentityAt.IsZero() || key.IdentityAt.Equal(identity.IdentityAt) {
			return true, nil
		}
	}
	for _, item := range m.failedItems[orgID] {
		if item.ItemType == itemType && item.ItemID == itemID && item.LibraryID == libraryID && item.BlockGCCandidateIdentity.Target == identity.Target() && (identity.IdentityAt.IsZero() || effectiveIdentityAt(item.QueuedAt, item.IdentityAt).Equal(identity.IdentityAt)) {
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

func (m *MockStore) CompleteItem(orgID uuid.UUID, queuedAt time.Time, itemType ItemType, itemID string, identity GCItemIdentity) error {
	m.queueCompleteCalls.Add(1)
	m.mu.Lock()
	defer m.mu.Unlock()
	identity, err := identity.requireIdentityAt("queue completion", itemID)
	if err != nil {
		return err
	}

	items := m.queue[orgID]
	for i, item := range items {
		if item.QueuedAt.Equal(queuedAt) && item.ItemType == itemType && item.ItemID == itemID && sameGCItemIdentity(item.Identity(), identity) {
			m.queue[orgID] = append(items[:i], items[i+1:]...)
			m.deletePendingItem(orgID, item.LibraryID, itemType, itemID, item.Identity())
			m.dirtyQueueOrgs[orgID] = time.Now().UTC()
			return nil
		}
	}
	return nil
}

func (m *MockStore) RequeueItem(orgID uuid.UUID, oldQueuedAt, newQueuedAt time.Time, itemType ItemType, itemID string, libraryID uuid.UUID, blockRepresentationID, storageClass string, newRetryCount int, identity GCItemIdentity, requiresLibraryDeletedCheck bool, libraryGuardMode LibraryGuardMode) error {
	m.queueRequeueCalls.Add(1)
	m.mu.Lock()
	defer m.mu.Unlock()
	identity, err := identity.requireIdentityAt("queue requeue", itemID)
	if err != nil {
		return err
	}

	if m.requeueItemErr != nil {
		return m.requeueItemErr
	}

	items := m.queue[orgID]
	for i, item := range items {
		if item.QueuedAt.Equal(oldQueuedAt) && item.ItemType == itemType && item.ItemID == itemID && sameGCItemIdentity(item.Identity(), identity) {
			// Remove the old item
			m.queue[orgID] = append(items[:i], items[i+1:]...)

			// Append the new recreated item
			newItem := item
			newItem.QueuedAt = newQueuedAt
			newItem.IdentityAt = identity.IdentityAt
			newItem.RequiresLibraryDeletedCheck = item.RequiresLibraryDeletedCheck
			newItem.LibraryGuardMode = effectiveLibraryGuardMode(libraryGuardMode, requiresLibraryDeletedCheck)
			newItem.BlockRepresentationID = strings.TrimSpace(blockRepresentationID)
			newItem.RetryCount = newRetryCount
			m.queue[orgID] = append(m.queue[orgID], newItem)
			m.upsertPendingItem(orgID, libraryID, itemType, itemID, newItem.Identity(), nil)
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
	m.queueFailCalls.Add(1)
	m.mu.Lock()
	defer m.mu.Unlock()

	items := m.queue[item.OrgID]
	found := false
	for i, existing := range items {
		if existing.QueuedAt.Equal(item.QueuedAt) && effectiveIdentityAt(existing.QueuedAt, existing.IdentityAt).Equal(effectiveIdentityAt(item.QueuedAt, item.IdentityAt)) && existing.ItemType == item.ItemType && existing.ItemID == item.ItemID && sameBlockGCCandidateIdentity(existing.BlockGCCandidateIdentity, item.BlockGCCandidateIdentity) {
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
		BlockGCCandidateIdentity:    item.BlockGCCandidateIdentity,
		RetryCount:                  item.RetryCount,
		LastError:                   lastError,
		FailureCode:                 failureCode,
	})
	expiresAt := failedAt.Add(gcFailedItemRetention)
	m.upsertPendingItem(item.OrgID, item.LibraryID, item.ItemType, item.ItemID, item.Identity(), &expiresAt)
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
			identityAt := effectiveIdentityAt(item.QueuedAt, item.IdentityAt)
			if !db.GCProjectionUTCDate(expiresAt).Equal(projectionDay) {
				continue
			}
			if db.GCFailedItemExpiryBucket(orgID.String(), item.FailedAt, string(item.ItemType), item.ItemID, item.BlockGCCandidateIdentity.Target.StorageClass, item.BlockGCCandidateIdentity.Target.StorageKey, identityAt) != bucket {
				continue
			}
			result = append(result, GCFailedItemExpiryInfo{
				OrgID:                    orgID,
				FailedAt:                 item.FailedAt,
				ExpiresAt:                expiresAt,
				IdentityAt:               identityAt,
				ItemType:                 item.ItemType,
				ItemID:                   item.ItemID,
				BlockGCCandidateIdentity: storedGCItemIdentity(item.ItemType, item.BlockGCCandidateIdentity.Target.StorageClass, item.BlockGCCandidateIdentity.Target.StorageKey, identityAt).BlockCandidate,
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

func (m *MockStore) DeleteFailedItem(orgID uuid.UUID, failedAt time.Time, itemType ItemType, itemID string, identity GCItemIdentity) error {
	return m.DeleteFailedItemContext(context.Background(), orgID, failedAt, itemType, itemID, identity)
}

func (m *MockStore) DeleteFailedItemContext(ctx context.Context, orgID uuid.UUID, failedAt time.Time, itemType ItemType, itemID string, identity GCItemIdentity) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.RLock()
	hook := m.dlqOpHook
	m.mu.RUnlock()
	if hook != nil {
		hook(orgID, "delete")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	identity, err := identity.requireIdentityAt("DLQ delete", itemID)
	if err != nil {
		return err
	}
	items := m.failedItems[orgID]
	for i, item := range items {
		if item.FailedAt.Equal(failedAt) && item.ItemType == itemType && item.ItemID == itemID && sameGCItemIdentity(item.Identity(), identity) {
			m.failedItems[orgID] = append(items[:i], items[i+1:]...)
			m.deletePendingItem(orgID, item.LibraryID, itemType, itemID, item.Identity())
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
		if item.FailedAt.Equal(expiry.FailedAt) && item.ItemType == expiry.ItemType && item.ItemID == expiry.ItemID && sameGCItemIdentity(item.Identity(), expiry.Identity()) {
			expiresAt := item.ExpiresAt
			if expiresAt.IsZero() {
				expiresAt = item.FailedAt.Add(gcFailedItemRetention)
			}
			if expiresAt.After(now.UTC()) {
				return false, nil
			}
			m.failedItems[expiry.OrgID] = append(items[:i], items[i+1:]...)
			m.deletePendingItem(expiry.OrgID, item.LibraryID, item.ItemType, item.ItemID, item.Identity())
			m.dirtyQueueOrgs[expiry.OrgID] = time.Now().UTC()
			return true, nil
		}
	}
	m.dirtyQueueOrgs[expiry.OrgID] = time.Now().UTC()
	return false, nil
}

func (m *MockStore) RequeueFailedItem(orgID uuid.UUID, failedAt time.Time, itemType ItemType, itemID string, queuedAt time.Time, identity GCItemIdentity) error {
	return m.RequeueFailedItemContext(context.Background(), orgID, failedAt, itemType, itemID, queuedAt, identity)
}

func (m *MockStore) RequeueFailedItemContext(ctx context.Context, orgID uuid.UUID, failedAt time.Time, itemType ItemType, itemID string, queuedAt time.Time, identity GCItemIdentity) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.RLock()
	hook := m.dlqOpHook
	m.mu.RUnlock()
	if hook != nil {
		hook(orgID, "requeue")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	identity, err := identity.requireIdentityAt("DLQ requeue", itemID)
	if err != nil {
		return err
	}
	items := m.failedItems[orgID]
	for i, item := range items {
		if item.FailedAt.Equal(failedAt) && item.ItemType == itemType && item.ItemID == itemID && sameGCItemIdentity(item.Identity(), identity) {
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
				BlockGCCandidateIdentity:    item.BlockGCCandidateIdentity,
				RetryCount:                  0,
			})
			m.upsertPendingItem(orgID, item.LibraryID, itemType, itemID, item.Identity(), nil)
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
func (m *MockStore) ReleaseStaleBlockClaim(orgID uuid.UUID, blockID string, expectedTarget BlockDeleteTarget, staleBefore time.Time) (BlockClaimReleaseOutcome, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if expectedTarget.IsZero() {
		return BlockClaimAbsent, fmt.Errorf("block %s: refusing to release a stale claim without naming the incarnation it belongs to", blockID)
	}
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
	if (BlockDeleteTarget{StorageClass: b.StorageClass, StorageKey: b.StorageKey}) != expectedTarget {
		// A fence on a different incarnation than the caller is authorized for.
		return BlockClaimTooFresh, nil
	}
	if orphanHandoffCommitted(b.GCOrphanHandoff) {
		return BlockClaimCommittedHandoff, nil
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
	// Only ages an EXISTING claim. Stamping gc_claimed_at onto an unclaimed row would
	// fabricate a state production cannot reach — the claim CAS sets and clears
	// gc_state, gc_claim_id and gc_claimed_at together — and a row with a timestamp but
	// no owner classifies as another attempt's live claim, silently wedging any test that
	// backdates in a loop.
	if b.GCState != db.BlockGCStateDeleting || b.GCClaimID == "" {
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

func (m *MockStore) EnsureBlockGCCandidateExact(orgID uuid.UUID, blockID, storageClass string, candidateAt time.Time) (BlockGCCandidateInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ensureBlockGCCandidateErr != nil {
		return BlockGCCandidateInfo{}, m.ensureBlockGCCandidateErr
	}
	target, err := m.resolveBlockDeleteTargetLocked(orgID, blockID)
	if err != nil {
		return BlockGCCandidateInfo{}, err
	}
	candidateAt = candidateAt.UTC()
	if candidateAt.IsZero() {
		candidateAt = time.Now().UTC()
	}
	key := newMockBlockGCCandidateKey(orgID, blockID, target)
	if existing, ok := m.blockGCCandidates[key]; ok {
		if candidateAt.Before(existing.CandidateAt) {
			delete(m.blockGCCandidateProjections, newMockBlockGCCandidateProjectionKey(existing.OrgID, existing.BlockID, existing.Target, existing.CandidateAt))
			existing.CandidateAt = candidateAt
		}
		m.upsertBlockGCCandidateProjection(existing)
		return BlockGCCandidateInfo{OrgID: existing.OrgID, BlockID: existing.BlockID, Target: existing.Target, CandidateAt: existing.CandidateAt}, m.ensureBlockGCCandidateErrAfterMutate
	}
	candidate := &mockBlockGCCandidate{
		OrgID:       orgID,
		BlockID:     blockID,
		Target:      target,
		CandidateAt: candidateAt,
	}
	m.blockGCCandidates[key] = candidate
	m.upsertBlockGCCandidateProjection(candidate)
	return BlockGCCandidateInfo{OrgID: candidate.OrgID, BlockID: candidate.BlockID, Target: candidate.Target, CandidateAt: candidate.CandidateAt}, m.ensureBlockGCCandidateErrAfterMutate
}

// resolveBlockDeleteTargetLocked mirrors the Cassandra store: a candidate cannot exist
// without the exact incarnation it is authorized for.
func (m *MockStore) resolveBlockDeleteTargetLocked(orgID uuid.UUID, blockID string) (BlockDeleteTarget, error) {
	b, ok := m.blocks[fmt.Sprintf("%s:%s", orgID, blockID)]
	if !ok {
		return BlockDeleteTarget{}, fmt.Errorf("%w: org=%s block=%s has no canonical row", ErrBlockCandidateTargetUnavailable, orgID, blockID)
	}
	target := BlockDeleteTarget{StorageClass: b.StorageClass, StorageKey: b.StorageKey}
	if target.IsZero() {
		return BlockDeleteTarget{}, fmt.Errorf("%w: org=%s block=%s canonical row has no usable locator %s", ErrBlockCandidateTargetUnavailable, orgID, blockID, target)
	}
	return target, nil
}

func (m *MockStore) GetBlockGCCandidateExact(orgID uuid.UUID, blockID string, candidate BlockGCCandidateIdentity) (BlockGCCandidateInfo, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.getBlockGCCandidateErr != nil {
		return BlockGCCandidateInfo{}, false, m.getBlockGCCandidateErr
	}
	if candidate.CandidateAt.IsZero() || candidate.Target.IsZero() {
		return BlockGCCandidateInfo{}, false, fmt.Errorf("block %s: refusing to get a gc candidate without its exact identity", blockID)
	}
	c, ok := m.blockGCCandidates[newMockBlockGCCandidateKey(orgID, blockID, candidate.Target)]
	if !ok || !c.CandidateAt.Equal(candidate.CandidateAt.UTC()) {
		return BlockGCCandidateInfo{}, false, nil
	}
	return BlockGCCandidateInfo{OrgID: c.OrgID, BlockID: c.BlockID, Target: c.Target, CandidateAt: c.CandidateAt}, true, nil
}

// ClaimAttemptsForTest returns every authority ClaimBlockDelete was called with, in
// order. It is how a test can assert that each ATTEMPT minted its own identity rather
// than inheriting one from the candidate.
func (m *MockStore) ClaimAttemptsForTest() []BlockDeleteAuthority {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]BlockDeleteAuthority, len(m.claimAttempts))
	copy(out, m.claimAttempts)
	return out
}

// SetClaimBlockDeleteSettleErrForTest makes the serial settling read fail too, so an
// injected LWT failure becomes genuinely unsettleable. Both knobs together are the only
// way to reach BlockClaimAmbiguous — which is the point: ambiguity is rare, and a test
// that reaches it by accident is testing the wrong thing.
func (m *MockStore) SetClaimBlockDeleteSettleErrForTest(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.claimBlockDeleteSettleErr = err
}

// SetClaimBlockDeleteEachQuorumErrForTest models SERIAL settlement seeing our claim
// while the canonical EACH_QUORUM visibility read cannot complete. Production then
// returns Ambiguous, not Acquired.
func (m *MockStore) SetClaimBlockDeleteEachQuorumErrForTest(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.claimBlockDeleteEachQuorumErr = err
}

// SetGetBlockGCCandidateErrForTest injects a failure into the candidate authority read.
func (m *MockStore) SetGetBlockGCCandidateErrForTest(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.getBlockGCCandidateErr = err
}

// DeleteBlockGCCandidateDiscovery mirrors the Cassandra store: it retires exactly
// one discovery row and never touches a canonical candidate.
func (m *MockStore) DeleteBlockGCCandidateDiscovery(orgID uuid.UUID, blockID string, candidate BlockGCCandidateIdentity) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if candidate.CandidateAt.IsZero() || candidate.Target.IsZero() {
		return fmt.Errorf("block %s: refusing to delete a gc candidate discovery row without its exact identity", blockID)
	}
	if m.deleteBlockGCCandidateDiscoveryErr != nil {
		return m.deleteBlockGCCandidateDiscoveryErr
	}
	delete(m.blockGCCandidateProjections, newMockBlockGCCandidateProjectionKey(orgID, blockID, candidate.Target, candidate.CandidateAt))
	return nil
}

func (m *MockStore) DeleteBlockGCCandidate(orgID uuid.UUID, blockID string, candidate BlockGCCandidateIdentity) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if candidate.CandidateAt.IsZero() || candidate.Target.IsZero() {
		return fmt.Errorf("block %s: refusing to delete a gc candidate without its exact identity", blockID)
	}
	if m.deleteBlockGCCandidateDiscoveryErr != nil {
		return m.deleteBlockGCCandidateDiscoveryErr
	}
	key := newMockBlockGCCandidateKey(orgID, blockID, candidate.Target)
	if existing, ok := m.blockGCCandidates[key]; ok && existing.CandidateAt.Equal(candidate.CandidateAt.UTC()) {
		delete(m.blockGCCandidates, key)
	}
	// Whether or not the canonical row was still ours, the discovery row for THIS
	// exact identity is retired: it can only be this lifecycle's row, and leaving
	// it standing is what makes a settled candidate rediscoverable forever.
	delete(m.blockGCCandidateProjections, newMockBlockGCCandidateProjectionKey(orgID, blockID, candidate.Target, candidate.CandidateAt))
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

// ClaimBlockDelete mirrors the exact-incarnation CAS.
//
// Note what it can no longer do: materialize a row. The production IF names
// storage_class, which no absent partition can satisfy, so a missing block is
// BlockClaimMissing rather than a freshly created stub.
func (m *MockStore) ClaimBlockDelete(orgID uuid.UUID, blockID string, attempt BlockDeleteAuthority) (BlockClaimResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.claimAttempts = append(m.claimAttempts, attempt)
	if attempt.IsZero() {
		return BlockClaimResult{Outcome: BlockClaimInvalid}, fmt.Errorf("block %s: refusing to claim without a complete delete authority", blockID)
	}
	staleBefore := attempt.ClaimedAt.Add(-blockDeleteClaimStaleAfter)
	if m.claimBlockDeleteErr != nil {
		// Mirror the production shape rather than collapsing it. A failed LWT is NOT
		// automatically ambiguous: the store settles it in the serial domain first, and
		// only a settlement that ALSO fails leaves the outcome unknown. Reporting every
		// injected error as ambiguous made the mock unable to express the case that
		// matters most — an LWT that timed out after committing, which the settling read
		// then recognises as our own claim. SERIAL ownership is still not EACH_QUORUM
		// visibility: claimBlockDeleteEachQuorumErr models a learn that never became
		// visible in every DC.
		if m.claimBlockDeleteSettleErr != nil {
			return BlockClaimResult{Outcome: BlockClaimAmbiguous}, fmt.Errorf("claim block %s: LWT failed (%v) and the serial settling read failed too: %w", blockID, m.claimBlockDeleteErr, m.claimBlockDeleteSettleErr)
		}
		b, ok := m.blocks[fmt.Sprintf("%s:%s", orgID, blockID)]
		if !ok {
			return BlockClaimResult{Outcome: BlockClaimMissing}, nil
		}
		settled := blockDeleteClaimRow{
			Target:          BlockDeleteTarget{StorageClass: b.StorageClass, StorageKey: b.StorageKey},
			GCState:         b.GCState,
			GCClaimID:       b.GCClaimID,
			GCOrphanHandoff: b.GCOrphanHandoff,
		}
		if b.GCClaimedAt != nil {
			settled.GCClaimedAt = *b.GCClaimedAt
		}
		result := settled.result(attempt, staleBefore)
		if result.Outcome == BlockClaimAcquired {
			// Keep the mock's uncertain-LWT path aligned with production: SERIAL ownership
			// is not enough, and the canonical visibility classifier also checks claimed_at.
			confirmed := classifySettledBlockClaimVisibility(settled, true, m.claimBlockDeleteEachQuorumErr, attempt)
			if confirmed.Outcome != BlockClaimAcquired {
				cause := m.claimBlockDeleteEachQuorumErr
				if cause == nil {
					cause = errors.New("settled block claim is not the exact authority at EACH_QUORUM")
				}
				return BlockClaimResult{Outcome: BlockClaimAmbiguous}, fmt.Errorf("confirm settled block claim visibility at EACH_QUORUM: %w", cause)
			}
			return confirmed, nil
		}
		return m.confirmMockCommittedOwner(settled, attempt, result)
	}
	b, ok := m.blocks[fmt.Sprintf("%s:%s", orgID, blockID)]
	if !ok {
		return BlockClaimResult{Outcome: BlockClaimMissing}, nil
	}
	row := blockDeleteClaimRow{
		Target:          BlockDeleteTarget{StorageClass: b.StorageClass, StorageKey: b.StorageKey},
		GCState:         b.GCState,
		GCClaimID:       b.GCClaimID,
		GCOrphanHandoff: b.GCOrphanHandoff,
	}
	if b.GCClaimedAt != nil {
		row.GCClaimedAt = *b.GCClaimedAt
	}
	result := row.result(attempt, staleBefore)
	if result.Outcome != BlockClaimAmbiguous {
		return m.confirmMockCommittedOwner(row, attempt, result)
	}
	// classify returns Ambiguous for "exact incarnation, present, unowned" because in
	// production that combination means the CAS should have applied and did not. Here it
	// IS the applying case.
	b.GCState = db.BlockGCStateDeleting
	b.GCClaimID = attempt.ClaimID
	claimedAt := attempt.ClaimedAt
	b.GCClaimedAt = &claimedAt
	return BlockClaimResult{Outcome: BlockClaimAcquired, Owner: attempt}, nil
}

func (m *MockStore) confirmMockCommittedOwner(row blockDeleteClaimRow, attempt BlockDeleteAuthority, result BlockClaimResult) (BlockClaimResult, error) {
	if result.Outcome != BlockClaimCommittedOwner {
		return result, nil
	}
	confirmed := classifyCommittedOwnerVisibility(row, true, m.claimBlockDeleteEachQuorumErr, attempt)
	if confirmed.Outcome != BlockClaimCommittedOwner {
		cause := m.claimBlockDeleteEachQuorumErr
		if cause == nil {
			cause = errors.New("EACH_QUORUM visibility is not a committed delete authority")
		}
		return BlockClaimResult{Outcome: BlockClaimAmbiguous}, fmt.Errorf("confirm committed owner visibility at EACH_QUORUM: %w", cause)
	}
	return confirmed, nil
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

func (m *MockStore) ReleaseBlockClaim(orgID uuid.UUID, blockID string, authority BlockDeleteAuthority) (BlockReleaseOutcome, error) {
	m.mu.Lock()
	hook := m.releaseBlockClaimHook
	m.releaseBlockClaimHook = nil
	m.mu.Unlock()
	if hook != nil {
		hook()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.releaseBlockClaimLocked(orgID, blockID, authority)
}

func (m *MockStore) releaseBlockClaimLocked(orgID uuid.UUID, blockID string, authority BlockDeleteAuthority) (BlockReleaseOutcome, error) {
	if authority.IsZero() {
		return BlockReleaseNotOwner, fmt.Errorf("block %s: refusing to release without a complete delete authority", blockID)
	}
	if m.releaseBlockClaimErr != nil {
		return BlockReleaseNotOwner, m.releaseBlockClaimErr
	}
	b, ok := m.blocks[fmt.Sprintf("%s:%s", orgID, blockID)]
	if !ok {
		return BlockReleaseNotOwner, nil
	}
	if b.GCState != db.BlockGCStateDeleting || b.GCClaimID != authority.ClaimID {
		return BlockReleaseNotOwner, nil
	}
	if b.GCClaimedAt == nil || !b.GCClaimedAt.Equal(authority.ClaimedAt) {
		return BlockReleaseNotOwner, nil
	}
	if (BlockDeleteTarget{StorageClass: b.StorageClass, StorageKey: b.StorageKey}) != authority.Target {
		return BlockReleaseNotOwner, nil
	}
	if orphanHandoffCommitted(b.GCOrphanHandoff) {
		return BlockReleaseNotOwner, nil
	}
	b.GCState = ""
	b.GCClaimID = ""
	b.GCClaimedAt = nil
	return BlockReleaseReleased, nil
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

func (m *MockStore) FinalizeBlockDelete(orgID uuid.UUID, blockID string, authority CommittedBlockDeleteAuthority) (BlockDeleteFinalizeResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if authority.IsZero() {
		return BlockDeleteFinalizeResult{
			Outcome: BlockDeleteInvalid,
			Cause:   fmt.Errorf("block %s: refusing to finalize without a complete delete authority", blockID),
		}, fmt.Errorf("block %s: refusing to finalize without a complete delete authority", blockID)
	}
	proposed := authority.Authority()
	key := fmt.Sprintf("%s:%s", orgID, blockID)
	b, ok := m.blocks[key]
	if !ok {
		return m.classifyMockFinalizeAbsentRow(orgID, blockID, proposed)
	}
	if b.GCState != db.BlockGCStateDeleting || b.GCClaimID != proposed.ClaimID {
		return BlockDeleteFinalizeResult{
			Outcome: BlockDeleteNotAuthority,
			Cause:   fmt.Errorf("block delete finalize not applied for %s", blockID),
		}, fmt.Errorf("block delete finalize not applied for %s", blockID)
	}
	if b.GCClaimedAt == nil || !b.GCClaimedAt.Equal(proposed.ClaimedAt) {
		return BlockDeleteFinalizeResult{
			Outcome: BlockDeleteNotAuthority,
			Cause:   fmt.Errorf("block delete finalize not applied for %s", blockID),
		}, fmt.Errorf("block delete finalize not applied for %s", blockID)
	}
	if (BlockDeleteTarget{StorageClass: b.StorageClass, StorageKey: b.StorageKey}) != proposed.Target {
		return BlockDeleteFinalizeResult{
			Outcome: BlockDeleteNotAuthority,
			Cause:   fmt.Errorf("block delete finalize not applied for %s", blockID),
		}, fmt.Errorf("block delete finalize not applied for %s", blockID)
	}
	if !orphanHandoffCommitted(b.GCOrphanHandoff) {
		return BlockDeleteFinalizeResult{
			Outcome: BlockDeleteNotAuthority,
			Cause:   fmt.Errorf("block delete finalize not applied for %s", blockID),
		}, fmt.Errorf("block delete finalize not applied for %s", blockID)
	}
	delete(m.blocks, key)
	return BlockDeleteFinalizeResult{Outcome: BlockDeleteFinalized}, nil
}

func (m *MockStore) classifyMockFinalizeAbsentRow(orgID uuid.UUID, blockID string, proposed BlockDeleteAuthority) (BlockDeleteFinalizeResult, error) {
	key := fmt.Sprintf("%s:%s", orgID, blockID)
	existing, found := m.s3Orphans[key]
	if found && existing.Authority.sameAuthority(proposed) {
		var life blockDeleteLifecycleRow
		lifeRow := m.blockDeleteLifecycles[mockBlockDeleteLifecycleKey(orgID, blockID, proposed.ClaimID)]
		lifeFound := lifeRow != nil
		if lifeFound {
			life = *lifeRow
		}
		classified := classifyFinalizeAbsentLifecycle(life, lifeFound, nil, proposed, nil)
		if classified.Outcome == BlockDeleteAlreadyFinalized {
			return classified, nil
		}
		return classified, classified.Cause
	}
	return BlockDeleteFinalizeResult{
		Outcome: BlockDeleteNotAuthority,
		Cause:   fmt.Errorf("block delete finalize not applied for %s", blockID),
	}, fmt.Errorf("block delete finalize not applied for %s", blockID)
}

func mockBlockDeleteLifecycleKey(orgID uuid.UUID, blockID, claimID string) string {
	return fmt.Sprintf("%s:%s:%s", orgID, blockID, claimID)
}

func (m *MockStore) CommitBlockDeleteOrphanHandoff(orgID uuid.UUID, blockID string, authority BlockDeleteAuthority) (BlockDeleteHandoffResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if authority.IsZero() {
		return BlockDeleteHandoffResult{
			Outcome: BlockDeleteHandoffInvalid,
			Cause:   fmt.Errorf("block %s: refusing to commit orphan handoff without a complete delete authority", blockID),
		}, fmt.Errorf("block %s: refusing to commit orphan handoff without a complete delete authority", blockID)
	}
	b, ok := m.blocks[fmt.Sprintf("%s:%s", orgID, blockID)]
	if m.commitHandoffEmptyCASOnce {
		m.commitHandoffEmptyCASOnce = false
		return m.settleMockHandoffEmpty(orgID, blockID, authority)
	}
	if m.commitHandoffErr != nil {
		if m.commitHandoffSettleErr != nil {
			return BlockDeleteHandoffResult{
				Outcome: BlockDeleteHandoffAmbiguous,
				Cause:   fmt.Errorf("commit orphan handoff for block %s: LWT failed (%v) and the serial settling read failed too: %w", blockID, m.commitHandoffErr, m.commitHandoffSettleErr),
			}, fmt.Errorf("commit orphan handoff for block %s: LWT failed (%v) and the serial settling read failed too: %w", blockID, m.commitHandoffErr, m.commitHandoffSettleErr)
		}
		if !ok {
			return BlockDeleteHandoffResult{
				Outcome: BlockDeleteHandoffInvalid,
				Cause:   fmt.Errorf("commit orphan handoff for block %s: LWT failed (%v) and serial settlement found no row", blockID, m.commitHandoffErr),
			}, fmt.Errorf("commit orphan handoff for block %s: LWT failed (%v) and serial settlement found no row", blockID, m.commitHandoffErr)
		}
		return m.classifyMockHandoffObservation(b, authority)
	}
	if !ok {
		return m.settleMockHandoffEmpty(orgID, blockID, authority)
	}
	return m.applyOrClassifyMockHandoff(b, authority)
}

func (m *MockStore) settleMockHandoffEmpty(orgID uuid.UUID, blockID string, authority BlockDeleteAuthority) (BlockDeleteHandoffResult, error) {
	b, ok := m.blocks[fmt.Sprintf("%s:%s", orgID, blockID)]
	if !ok {
		return BlockDeleteHandoffResult{
			Outcome: BlockDeleteHandoffInvalid,
			Cause:   fmt.Errorf("commit orphan handoff for block %s: LWT failed (%v) and serial settlement found no row", blockID, error(nil)),
		}, fmt.Errorf("commit orphan handoff for block %s: serial settlement found no row", blockID)
	}
	return m.classifyMockHandoffObservation(b, authority)
}

func (m *MockStore) applyOrClassifyMockHandoff(b *mockBlock, authority BlockDeleteAuthority) (BlockDeleteHandoffResult, error) {
	row := mockBlockDeleteClaimRow(b)
	classified := classifyBlockDeleteHandoffRow(row, authority)
	if classified.Outcome == BlockDeleteHandoffAlreadyCommitted {
		return m.confirmMockAlreadyCommittedHandoff(row, authority)
	}
	if classified.Outcome == BlockDeleteHandoffNotOwner || classified.Outcome == BlockDeleteHandoffTargetChanged || classified.Outcome == BlockDeleteHandoffInvalid {
		return classified, classified.Cause
	}
	handoff := true
	b.GCOrphanHandoff = &handoff
	return BlockDeleteHandoffResult{
		Outcome:   BlockDeleteHandoffCommitted,
		Authority: committedBlockDeleteAuthority(authority),
	}, nil
}

func mockBlockDeleteClaimRow(b *mockBlock) blockDeleteClaimRow {
	row := blockDeleteClaimRow{
		Target:          BlockDeleteTarget{StorageClass: b.StorageClass, StorageKey: b.StorageKey},
		GCState:         b.GCState,
		GCClaimID:       b.GCClaimID,
		GCOrphanHandoff: b.GCOrphanHandoff,
	}
	if b.GCClaimedAt != nil {
		row.GCClaimedAt = *b.GCClaimedAt
	}
	return row
}

func (m *MockStore) classifyMockHandoffObservation(b *mockBlock, authority BlockDeleteAuthority) (BlockDeleteHandoffResult, error) {
	row := mockBlockDeleteClaimRow(b)
	classified := classifyBlockDeleteHandoffRow(row, authority)
	if classified.Outcome != BlockDeleteHandoffAlreadyCommitted {
		return classified, classified.Cause
	}
	return m.confirmMockAlreadyCommittedHandoff(row, authority)
}

func (m *MockStore) confirmMockAlreadyCommittedHandoff(row blockDeleteClaimRow, authority BlockDeleteAuthority) (BlockDeleteHandoffResult, error) {
	confirmed := classifyBlockDeleteHandoffRow(row, authority)
	if m.claimBlockDeleteEachQuorumErr != nil || confirmed.Outcome != BlockDeleteHandoffAlreadyCommitted {
		cause := m.claimBlockDeleteEachQuorumErr
		if cause == nil {
			cause = errors.New("EACH_QUORUM visibility is not the committed authority")
		}
		return BlockDeleteHandoffResult{
			Outcome: BlockDeleteHandoffAmbiguous,
			Cause:   fmt.Errorf("commit orphan handoff: settled committed authority is not visible at EACH_QUORUM: %v", cause),
		}, fmt.Errorf("commit orphan handoff: settled committed authority is not visible at EACH_QUORUM: %v", cause)
	}
	return confirmed, nil
}

func (m *MockStore) SetCommitHandoffErrForTest(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.commitHandoffErr = err
}

func (m *MockStore) SetCommitHandoffSettleErrForTest(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.commitHandoffSettleErr = err
}

// ForceEmptyHandoffCASOnceForTest models Cassandra returning an empty non-applied
// CAS map. Production must SERIAL-settle that observation rather than classify it
// as Invalid.
func (m *MockStore) ForceEmptyHandoffCASOnceForTest() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.commitHandoffEmptyCASOnce = true
}

// SeedBlockHandoffForTest marks an existing delete claim as having crossed the
// irreversible orphan-handoff commit point.
func (m *MockStore) SeedBlockHandoffForTest(orgID uuid.UUID, blockID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.blocks[fmt.Sprintf("%s:%s", orgID, blockID)]
	if !ok {
		return
	}
	handoff := true
	b.GCOrphanHandoff = &handoff
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

func (m *MockStore) SoftDeleteLibraryUnderLease(orgID, libraryID, deletedBy, leaseToken uuid.UUID) error {
	owned, err := m.RenewLibraryHardDeleteLock(libraryID, leaseToken)
	if err != nil {
		return err
	}
	if !owned {
		return fmt.Errorf("lost library lifecycle fence for soft delete %s", libraryID)
	}
	return m.SoftDeleteLibrary(orgID, libraryID, deletedBy)
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
	mu                         sync.Mutex
	DeletedKeys                []string
	ScopedDeletes              []ScopedBlockDelete
	ResolvedStores             []ScopedBlockStoreRequest
	PhysicalLocatorValidations []ScopedPhysicalLocatorValidation

	// failTimes is the number of upcoming DeleteBlock calls that should
	// return an error before the next call succeeds. Decremented per call.
	// Zero means "always succeed".
	failTimes int
	// failAlways overrides failTimes and makes every DeleteBlock fail.
	failAlways bool
	// failErr is the error returned while failing.
	failErr error
	// resolveErr makes GetBlockStoreForOrg itself fail — an unregistered or
	// misconfigured storage class, which is the reachable degenerate config. It is
	// separate from failErr because the two land at opposite ends of the delete:
	// one before the block row is touched, the other after.
	resolveErr error
}

// FailResolve makes every GetBlockStoreForOrg call return err until cleared.
func (p *MockStorageProvider) FailResolve(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.resolveErr = err
}

// ScopedBlockDelete records one physical delete the way the backend saw it: the
// org and class that selected the bucket, and the exact key handed to it. It
// deliberately does NOT carry a block id — the delete API takes a locator, and a
// mock that reconstructed an id would hide the very substitution these tests exist
// to catch.
type ScopedBlockDelete struct {
	OrgID        string
	StorageClass string
	StorageKey   string
}

// MockCanonicalStorageKey mirrors storage.BlockStore.hashToKey for the mock
// backend. Tests seed and assert through it so a mock delete is only "correct"
// when it targets the same org-scoped locator the real store would derive.
func MockCanonicalStorageKey(orgID, hash string) string {
	if len(hash) < 4 {
		return fmt.Sprintf("blocks/%s/%s", orgID, hash)
	}
	return fmt.Sprintf("blocks/%s/%s/%s/%s", orgID, hash[:2], hash[2:4], hash)
}

type ScopedBlockStoreRequest struct {
	OrgID        string
	StorageClass string
}

// ScopedPhysicalLocatorValidation records the exact persisted locator presented
// to an org-scoped store before a physical delete.
type ScopedPhysicalLocatorValidation struct {
	OrgID        string
	StorageClass string
	BlockID      string
	StorageKey   string
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
	resolveErr := p.resolveErr
	p.mu.Unlock()
	if resolveErr != nil {
		return nil, resolveErr
	}
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

func (p *MockStorageProvider) LocatorValidations() []ScopedPhysicalLocatorValidation {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]ScopedPhysicalLocatorValidation{}, p.PhysicalLocatorValidations...)
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

// ClearFailures stops injecting failures, resolution failures included.
func (p *MockStorageProvider) ClearFailures() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failTimes = 0
	p.failAlways = false
	p.failErr = nil
	p.resolveErr = nil
}

type mockBlockDeleter struct {
	provider     *MockStorageProvider
	orgID        string
	storageClass string
}

// ValidatePhysicalLocator mirrors storage.BlockStore's legacy-or-minted locator
// contract so GC unit tests do not bypass block-id or incarnation validation.
func (d *mockBlockDeleter) ValidatePhysicalLocator(blockID, storageKey string) error {
	d.provider.mu.Lock()
	d.provider.PhysicalLocatorValidations = append(d.provider.PhysicalLocatorValidations, ScopedPhysicalLocatorValidation{
		OrgID:        d.orgID,
		StorageClass: d.storageClass,
		BlockID:      blockID,
		StorageKey:   storageKey,
	})
	d.provider.mu.Unlock()
	if !db.IsSHA256BlockID(blockID) {
		return fmt.Errorf("block id %q is not a resolved SHA-256 block id", blockID)
	}
	base := MockCanonicalStorageKey(d.orgID, blockID)
	if storageKey == base {
		return nil
	}
	incarnationPrefix := base + "."
	if !strings.HasPrefix(storageKey, incarnationPrefix) {
		return fmt.Errorf("block storage key %q does not match block id %q", storageKey, blockID)
	}
	incarnation := strings.TrimPrefix(storageKey, incarnationPrefix)
	parsed, err := uuid.Parse(incarnation)
	if err != nil || parsed.String() != incarnation {
		return fmt.Errorf("block storage key %q has a malformed or non-canonical incarnation", storageKey)
	}
	return nil
}

func (d *mockBlockDeleter) DeleteBlockByStorageKey(ctx context.Context, storageKey string) error {
	d.provider.mu.Lock()
	defer d.provider.mu.Unlock()
	if d.provider.failAlways {
		return d.provider.failErr
	}
	if d.provider.failTimes > 0 {
		d.provider.failTimes--
		return d.provider.failErr
	}
	d.provider.DeletedKeys = append(d.provider.DeletedKeys, storageKey)
	d.provider.ScopedDeletes = append(d.provider.ScopedDeletes, ScopedBlockDelete{
		OrgID:        d.orgID,
		StorageClass: d.storageClass,
		StorageKey:   storageKey,
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

func (m *MockStore) StartBlockDeleteOrphan(orgID uuid.UUID, blockID string, authority CommittedBlockDeleteAuthority, externalSHA1 string, now time.Time) StartBlockDeleteOrphanResult {
	result := StartBlockDeleteOrphanResult{Outcome: StartBlockDeleteOrphanAmbiguous}
	if authority.IsZero() {
		result.Outcome = StartBlockDeleteOrphanInvalid
		result.Cause = fmt.Errorf("cannot record S3 orphan for org=%s block=%s without a complete committed delete authority", orgID, blockID)
		return result
	}
	proposed := authority.Authority()
	storageClass := proposed.Target.StorageClass
	storageKey := proposed.Target.StorageKey
	if !config.IsCanonicalStorageClassName(storageClass) {
		result.Outcome = StartBlockDeleteOrphanInvalid
		result.Cause = fmt.Errorf("cannot record S3 orphan for org=%s block=%s with non-canonical storage class %q", orgID, blockID, storageClass)
		return result
	}
	if storageKey == "" || strings.TrimSpace(storageKey) != storageKey {
		result.Outcome = StartBlockDeleteOrphanInvalid
		result.Cause = fmt.Errorf("cannot record S3 orphan for org=%s block=%s without storage key", orgID, blockID)
		return result
	}
	m.mu.Lock()
	lifecycle := m.insertMockBlockDeleteLifecycleLocked(orgID, blockID, proposed)
	if lifecycle.Outcome == StartBlockDeleteOrphanLifecycleAdvanced {
		m.mu.Unlock()
		return lifecycle
	}
	if lifecycle.Outcome != StartBlockDeleteOrphanCreated && lifecycle.Outcome != StartBlockDeleteOrphanSameAuthority {
		m.mu.Unlock()
		return lifecycle
	}
	pause := m.pauseAfterLifecycleBeforeOrphan
	entered := m.pauseAfterLifecycleBeforeOrphanEntered
	if pause != nil {
		m.pauseAfterLifecycleBeforeOrphan = nil
		m.pauseAfterLifecycleBeforeOrphanEntered = nil
		m.mu.Unlock()
		if entered != nil {
			close(entered)
		}
		<-pause
		m.mu.Lock()
	}
	defer m.mu.Unlock()
	result.Submitted = true
	now = now.UTC()
	externalSHA1 = strings.TrimSpace(externalSHA1)
	if m.startBlockDeleteOrphanNotPublishedOnce {
		m.startBlockDeleteOrphanNotPublishedOnce = false
		result.Outcome = StartBlockDeleteOrphanNotPublished
		result.Cause = errors.New("test: serial settlement confirmed orphan publication absent")
		return result
	}
	if m.startBlockDeleteOrphanAmbiguousOnce {
		m.startBlockDeleteOrphanAmbiguousOnce = false
		result.Outcome = StartBlockDeleteOrphanAmbiguous
		result.Cause = errors.New("test: serial settlement could not establish orphan publication")
		return result
	}
	key := fmt.Sprintf("%s:%s", orgID, blockID)
	if existing, ok := m.s3Orphans[key]; ok {
		row := s3OrphanCASRow{
			Target:      BlockDeleteTarget{StorageClass: existing.StorageClass, StorageKey: existing.StorageKey},
			Authority:   existing.Authority,
			FirstSeenAt: existing.FirstSeenAt,
		}
		classified := classifyS3OrphanRow(row, proposed)
		classified.Submitted = true
		if classified.Outcome == StartBlockDeleteOrphanInvalid {
			return classified
		}
		if classified.Outcome == StartBlockDeleteOrphanSameAuthority {
			return m.confirmPublishedLifecycleAfterOrphanLocked(orgID, blockID, proposed, m.confirmSameAuthorityOrphanResultLocked(orgID, blockID, classified))
		}
		return classified
	}
	orphan := &S3OrphanInfo{
		OrgID:         orgID,
		BlockID:       blockID,
		StorageClass:  storageClass,
		StorageKey:    storageKey,
		ExternalSHA1:  externalSHA1,
		RecoveryPhase: S3OrphanPhasePendingS3,
		FirstSeenAt:   now,
		LastAttemptAt: now,
		Authority:     proposed,
	}
	m.s3Orphans[key] = orphan
	result.Outcome = StartBlockDeleteOrphanCreated
	result.FirstSeenAt = orphan.FirstSeenAt
	result.ExistingAuthority = proposed
	return m.confirmPublishedLifecycleAfterOrphanLocked(orgID, blockID, proposed, m.ensureS3OrphanProjectionResultLocked(orgID, blockID, result))
}

func (m *MockStore) confirmSameAuthorityOrphanResultLocked(orgID uuid.UUID, blockID string, result StartBlockDeleteOrphanResult) StartBlockDeleteOrphanResult {
	if m.startBlockDeleteOrphanCanonicalUnconfirmedOnce {
		m.startBlockDeleteOrphanCanonicalUnconfirmedOnce = false
		result.Outcome = StartBlockDeleteOrphanAmbiguous
		result.Cause = errors.New("test: canonical EACH_QUORUM visibility unconfirmed")
		return result
	}
	key := fmt.Sprintf("%s:%s", orgID, blockID)
	if existing, ok := m.s3Orphans[key]; ok && existing.RecoveryPhase != S3OrphanPhasePendingS3 {
		result.Outcome = StartBlockDeleteOrphanLifecycleAdvanced
		result.Cause = fmt.Errorf("canonical S3 orphan recovery phase %q does not authorize a new physical delete", existing.RecoveryPhase)
		return result
	}
	return m.ensureS3OrphanProjectionResultLocked(orgID, blockID, result)
}

func (m *MockStore) ObserveBlockDeleteLifecycle(orgID uuid.UUID, blockID string, authority CommittedBlockDeleteAuthority) StartBlockDeleteOrphanResult {
	m.mu.Lock()
	defer m.mu.Unlock()
	if authority.IsZero() {
		return StartBlockDeleteOrphanResult{
			Outcome: StartBlockDeleteOrphanInvalid,
			Cause:   fmt.Errorf("observe lifecycle for org=%s block=%s without a complete committed delete authority", orgID, blockID),
		}
	}
	return m.observeBlockDeleteLifecycleLocked(orgID, blockID, authority.Authority())
}

func (m *MockStore) observeBlockDeleteLifecycleLocked(orgID uuid.UUID, blockID string, proposed BlockDeleteAuthority) StartBlockDeleteOrphanResult {
	if proposed.IsZero() {
		return StartBlockDeleteOrphanResult{
			Outcome: StartBlockDeleteOrphanInvalid,
			Cause:   errors.New("observe lifecycle without a complete delete authority"),
		}
	}
	row := m.blockDeleteLifecycles[mockBlockDeleteLifecycleKey(orgID, blockID, proposed.ClaimID)]
	if row == nil {
		return StartBlockDeleteOrphanResult{
			Outcome: StartBlockDeleteOrphanNotPublished,
			Cause:   errors.New("block-delete lifecycle row is absent"),
		}
	}
	return classifyBlockDeleteLifecycleRow(*row, proposed)
}

func (m *MockStore) confirmPublishedLifecycleAfterOrphanLocked(orgID uuid.UUID, blockID string, proposed BlockDeleteAuthority, result StartBlockDeleteOrphanResult) StartBlockDeleteOrphanResult {
	return confirmPublishedLifecycleCertificate(result, m.observeBlockDeleteLifecycleLocked(orgID, blockID, proposed))
}

// ForcePauseAfterLifecycleBeforeOrphanForTest parks StartBlockDeleteOrphan after
// the durable D tombstone is inserted and before the orphan row. The lock is
// released during the wait so a concurrent executor can terminate D.
func (m *MockStore) ForcePauseAfterLifecycleBeforeOrphanForTest() (entered <-chan struct{}, resume func()) {
	enteredCh := make(chan struct{})
	gate := make(chan struct{})
	m.mu.Lock()
	m.pauseAfterLifecycleBeforeOrphan = gate
	m.pauseAfterLifecycleBeforeOrphanEntered = enteredCh
	m.mu.Unlock()
	return enteredCh, func() { close(gate) }
}

func (m *MockStore) DropBlockDeleteLifecycleForTest(orgID uuid.UUID, blockID, claimID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.blockDeleteLifecycles, mockBlockDeleteLifecycleKey(orgID, blockID, claimID))
}

func (m *MockStore) insertMockBlockDeleteLifecycleLocked(orgID uuid.UUID, blockID string, proposed BlockDeleteAuthority) StartBlockDeleteOrphanResult {
	key := mockBlockDeleteLifecycleKey(orgID, blockID, proposed.ClaimID)
	if existing, ok := m.blockDeleteLifecycles[key]; ok {
		return classifyBlockDeleteLifecycleRow(*existing, proposed)
	}
	row := &blockDeleteLifecycleRow{
		Target:    proposed.Target,
		ClaimID:   proposed.ClaimID,
		ClaimedAt: proposed.ClaimedAt,
		Phase:     BlockDeleteLifecyclePhasePublished,
	}
	m.blockDeleteLifecycles[key] = row
	return StartBlockDeleteOrphanResult{Outcome: StartBlockDeleteOrphanCreated, ExistingAuthority: proposed, Submitted: true}
}

func (m *MockStore) TerminateBlockDeleteLifecycle(orgID uuid.UUID, blockID string, authority CommittedBlockDeleteAuthority) (BlockDeleteLifecycleTerminateResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if authority.IsZero() {
		return BlockDeleteLifecycleTerminateResult{
			Outcome: BlockDeleteLifecycleInvalid,
			Cause:   fmt.Errorf("block %s: refusing to terminate lifecycle without a complete committed delete authority", blockID),
		}, fmt.Errorf("block %s: refusing to terminate lifecycle without a complete committed delete authority", blockID)
	}
	proposed := authority.Authority()
	key := mockBlockDeleteLifecycleKey(orgID, blockID, proposed.ClaimID)
	existing, ok := m.blockDeleteLifecycles[key]
	if !ok {
		return BlockDeleteLifecycleTerminateResult{
			Outcome: BlockDeleteLifecycleNotOwner,
			Cause:   fmt.Errorf("terminate lifecycle for block %s: no lifecycle row for this authority", blockID),
		}, fmt.Errorf("terminate lifecycle for block %s: no lifecycle row for this authority", blockID)
	}
	classified := classifyTerminateBlockDeleteLifecycleRow(*existing, proposed)
	if classified.Outcome == BlockDeleteLifecycleAlreadyTerminal {
		return classified, nil
	}
	stored := BlockDeleteAuthority{Target: existing.Target, ClaimID: existing.ClaimID, ClaimedAt: existing.ClaimedAt}
	if existing.Phase != BlockDeleteLifecyclePhasePublished || !stored.sameAuthority(proposed) {
		return classified, classified.Cause
	}
	existing.Phase = BlockDeleteLifecyclePhaseTerminal
	return BlockDeleteLifecycleTerminateResult{Outcome: BlockDeleteLifecycleTerminated}, nil
}

func (m *MockStore) SeedBlockDeleteLifecycleForTest(orgID uuid.UUID, blockID string, authority BlockDeleteAuthority, phase string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.blockDeleteLifecycles[mockBlockDeleteLifecycleKey(orgID, blockID, authority.ClaimID)] = &blockDeleteLifecycleRow{
		Target:    authority.Target,
		ClaimID:   authority.ClaimID,
		ClaimedAt: authority.ClaimedAt,
		Phase:     phase,
	}
}

func (m *MockStore) BlockDeleteLifecyclePhaseForTest(orgID uuid.UUID, blockID, claimID string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if row := m.blockDeleteLifecycles[mockBlockDeleteLifecycleKey(orgID, blockID, claimID)]; row != nil {
		return row.Phase
	}
	return ""
}

func (m *MockStore) ensureS3OrphanProjectionResultLocked(orgID uuid.UUID, blockID string, result StartBlockDeleteOrphanResult) StartBlockDeleteOrphanResult {
	if m.startBlockDeleteOrphanProjectionErrOnce != nil {
		result.Outcome = StartBlockDeleteOrphanProjectionUnconfirmed
		result.Cause = m.startBlockDeleteOrphanProjectionErrOnce
		m.startBlockDeleteOrphanProjectionErrOnce = nil
		return result
	}
	m.upsertS3OrphanProjection(orgID, blockID, result.FirstSeenAt)
	return result
}

func (m *MockStore) MarkS3OrphanMappingCleanupPending(orgID uuid.UUID, blockID, externalSHA1 string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.markS3OrphanErrOnce != nil {
		err := m.markS3OrphanErrOnce
		m.markS3OrphanErrOnce = nil
		return err
	}
	key := fmt.Sprintf("%s:%s", orgID, blockID)
	if existing, ok := m.s3Orphans[key]; ok {
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

// SetStartBlockDeleteOrphanProjectionErrOnceForTest makes publication retain the
// canonical row while reporting that discovery projection durability was not
// confirmed.
func (m *MockStore) SetStartBlockDeleteOrphanProjectionErrOnceForTest(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.startBlockDeleteOrphanProjectionErrOnce = err
}

// SetStartBlockDeleteOrphanNotPublishedOnceForTest makes the next publication
// report that serial settlement confirmed the orphan row was absent.
func (m *MockStore) SetStartBlockDeleteOrphanNotPublishedOnceForTest() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.startBlockDeleteOrphanNotPublishedOnce = true
}

// SetStartBlockDeleteOrphanAmbiguousOnceForTest makes the next publication
// report that serial settlement could not establish its outcome.
func (m *MockStore) SetStartBlockDeleteOrphanAmbiguousOnceForTest() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.startBlockDeleteOrphanAmbiguousOnce = true
}

// SetStartBlockDeleteOrphanCanonicalUnconfirmedOnceForTest makes the next
// same-target publication fail closed before projection repair, matching a
// SERIAL/CAS observation whose canonical EACH_QUORUM read did not confirm
// writer-visible fence dissemination.
func (m *MockStore) SetStartBlockDeleteOrphanCanonicalUnconfirmedOnceForTest() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.startBlockDeleteOrphanCanonicalUnconfirmedOnce = true
}

// SetReleaseBlockClaimHookForTest runs once immediately before a mock release
// evaluates ownership, allowing tests to interpose a competing claim.
func (m *MockStore) SetReleaseBlockClaimHookForTest(hook func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.releaseBlockClaimHook = hook
}

// AllBlockGCCandidates is a test helper returning every candidate row across
// all (day, bucket) partitions.
func (m *MockStore) AllBlockGCCandidates() []BlockGCCandidateInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]BlockGCCandidateInfo, 0, len(m.blockGCCandidates))
	for _, c := range m.blockGCCandidates {
		out = append(out, BlockGCCandidateInfo{
			OrgID:       c.OrgID,
			BlockID:     c.BlockID,
			Target:      c.Target,
			CandidateAt: c.CandidateAt,
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

// SetDeleteBlockGCCandidateDiscoveryErr makes the discovery cleanup fail, so a
// test can prove that a projection which cannot be retired postpones the item
// instead of completing it and leaving the row to be rediscovered forever.
func (m *MockStore) SetDeleteBlockGCCandidateDiscoveryErr(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.deleteBlockGCCandidateDiscoveryErr = err
}

// BlockGCCandidateProjectionsForTest returns every discovery row currently
// published, so a test can assert that a settled candidate left none behind.
func (m *MockStore) BlockGCCandidateProjectionsForTest(orgID uuid.UUID, blockID string) []BlockGCCandidateInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var rows []BlockGCCandidateInfo
	for key := range m.blockGCCandidateProjections {
		if key.OrgID != orgID || key.BlockID != blockID {
			continue
		}
		rows = append(rows, BlockGCCandidateInfo{OrgID: key.OrgID, BlockID: key.BlockID, Target: key.Target, CandidateAt: key.CandidateAt})
	}
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].CandidateAt.Equal(rows[j].CandidateAt) {
			return rows[i].CandidateAt.Before(rows[j].CandidateAt)
		}
		return rows[i].Target.StorageKey < rows[j].Target.StorageKey
	})
	return rows
}

// EnqueueBlockForTest is the test-support replacement for the raw EnqueueItem
// path that used to accept ItemBlock.
//
// That path was removed from production because "enqueue" must not be able to
// mint destructive authority: a block work item is legitimate only when a
// zero-ref decision already produced a candidate for an exact P. Tests still
// need a one-liner that sets up a processable block item, so the decision and
// the enqueue happen here, explicitly and in that order — exactly what
// Service.EnqueueBlock and Worker.enqueueZeroRefBlocks do.
func (m *MockStore) EnqueueBlockForTest(orgID uuid.UUID, queuedAt time.Time, blockID string, storageClass string, retryCount int) error {
	candidate, err := m.EnsureBlockGCCandidateExact(orgID, blockID, storageClass, queuedAt)
	if err != nil {
		return err
	}
	return m.EnqueueBatch([]QueueItem{{
		OrgID:                    orgID,
		QueuedAt:                 queuedAt,
		IdentityAt:               candidate.CandidateAt,
		ItemType:                 ItemBlock,
		ItemID:                   blockID,
		LibraryID:                uuid.Nil,
		StorageClass:             candidate.StorageClass(),
		BlockGCCandidateIdentity: candidate.Identity(),
		RetryCount:               retryCount,
	}})
}

// AddFailedItemForTest seeds a DLQ row directly, so a test can construct two
// lifecycles that differ only in identity_at.
func (m *MockStore) AddFailedItemForTest(item GCFailedItemInfo) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if item.ExpiresAt.IsZero() {
		item.ExpiresAt = item.FailedAt.Add(gcFailedItemRetention)
	}
	m.failedItems[item.OrgID] = append(m.failedItems[item.OrgID], item)
}

// DeleteBlockGCCandidateCanonicalForTest removes ONLY the canonical candidate row,
// leaving its discovery row standing. That is the shape a settlement leaves behind
// when the canonical delete commits and the projection delete does not, and it is
// the state the R26 self-heal has to converge from.
func (m *MockStore) DeleteBlockGCCandidateCanonicalForTest(orgID uuid.UUID, blockID string, candidate BlockGCCandidateIdentity) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.blockGCCandidates, newMockBlockGCCandidateKey(orgID, blockID, candidate.Target))
}
