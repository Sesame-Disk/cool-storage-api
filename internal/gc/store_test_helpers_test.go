package gc

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/google/uuid"
)

// testSHA256BlockID turns a readable scenario label into the content-address
// shape used by production blocks without making the test lose its label.
func testSHA256BlockID(label string) string {
	digest := sha256.Sum256([]byte("sesamefs-gc-test:" + label))
	return hex.EncodeToString(digest[:])
}

// SeedQueueItemForTest injects a raw gc_queue row for test setup, deliberately
// bypassing the block-representation enqueue contract that EnqueueItem and
// EnqueueBatch enforce. It always stores an empty block representation, so it is
// how unit tests stage the incomplete/legacy commit/fs_object/library_cascade
// rows those guarded paths now reject — without building full library fixtures.
//
// It lives in a _test.go file on purpose: that keeps the bypass out of the
// production build entirely (an external caller of MockStore cannot reach it),
// so the enqueue invariant is a structural restriction, not just a naming
// convention.
func (m *MockStore) SeedQueueItemForTest(orgID uuid.UUID, queuedAt time.Time, itemType ItemType, itemID string, libraryID uuid.UUID, storageClass string, retryCount int) {
	m.seedQueueItemRow(orgID, queuedAt, itemType, itemID, libraryID, storageClass, retryCount)
}

// SeedGuardedQueueItemForTest is SeedQueueItemForTest with a library guard. Since
// Phase 6 is suspended the worker completes unguarded fs_object rows without any
// mutation, so tests of the destructive fs_object path stage guarded rows.
func (m *MockStore) SeedGuardedQueueItemForTest(orgID uuid.UUID, queuedAt time.Time, itemType ItemType, itemID string, libraryID uuid.UUID, guard LibraryGuardMode) {
	m.seedQueueItemRow(orgID, queuedAt, itemType, itemID, libraryID, "", 0)
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := m.queue[orgID]
	rows[len(rows)-1].LibraryGuardMode = guard
}

// hardDeleteLibraryForTest puts a fixture library where a guarded fs_object item
// may legitimately run: soft-deleted, then hard-deleted (canonical row and marker
// gone), as after a completed library cascade.
func hardDeleteLibraryForTest(t testing.TB, store *MockStore, orgID, libraryID uuid.UUID) {
	t.Helper()
	if err := store.SoftDeleteLibrary(orgID, libraryID, uuid.Nil); err != nil {
		t.Fatalf("SoftDeleteLibrary failed: %v", err)
	}
	if err := store.HardDeleteLibrary(orgID, libraryID); err != nil {
		t.Fatalf("HardDeleteLibrary failed: %v", err)
	}
}

// enqueueExactBlockCandidateForTest queues a block only after its exact candidate
// has been captured. Tests use this instead of the legacy single-row enqueue path.
func enqueueExactBlockCandidateForTest(t testing.TB, store *MockStore, candidate BlockGCCandidateInfo, retryCount int) {
	enqueueExactBlockCandidateAtForTest(t, store, candidate, candidate.CandidateAt, retryCount)
}

func enqueueExactBlockCandidateAtForTest(t testing.TB, store *MockStore, candidate BlockGCCandidateInfo, queuedAt time.Time, retryCount int) {
	t.Helper()
	if err := NewQueue(store).EnqueueBatch([]QueueItem{{
		OrgID:                    candidate.OrgID,
		QueuedAt:                 queuedAt,
		IdentityAt:               candidate.CandidateAt,
		ItemType:                 ItemBlock,
		ItemID:                   candidate.BlockID,
		LibraryID:                uuid.Nil,
		StorageClass:             candidate.StorageClass(),
		BlockGCCandidateIdentity: candidate.Identity(),
		RetryCount:               retryCount,
	}}); err != nil {
		t.Fatalf("enqueue exact block candidate: %v", err)
	}
}

func ensureAndEnqueueBlockForTest(t testing.TB, store *MockStore, orgID uuid.UUID, blockID, storageClass string, candidateAt time.Time, retryCount int) BlockGCCandidateInfo {
	t.Helper()
	candidate, err := store.EnsureBlockGCCandidateExact(orgID, blockID, storageClass, candidateAt)
	if err != nil {
		t.Fatalf("EnsureBlockGCCandidateExact(%s): %v", blockID, err)
	}
	enqueueExactBlockCandidateForTest(t, store, candidate, retryCount)
	return candidate
}

// SeedExactBlockQueueItemForTest models a queue row written before exact-P
// validation. It is reserved for tests that deliberately exercise malformed
// candidates which production enqueue paths must reject.
func (m *MockStore) SeedExactBlockQueueItemForTest(orgID uuid.UUID, queuedAt time.Time, blockID, storageClass string, candidate BlockGCCandidateIdentity, retryCount int) {
	m.mu.Lock()
	defer m.mu.Unlock()

	item := QueueItem{
		OrgID:                    orgID,
		QueuedAt:                 queuedAt,
		IdentityAt:               candidate.CandidateAt,
		ItemType:                 ItemBlock,
		ItemID:                   blockID,
		LibraryID:                uuid.Nil,
		StorageClass:             storageClass,
		BlockGCCandidateIdentity: candidate,
		RetryCount:               retryCount,
	}
	m.queue[orgID] = append(m.queue[orgID], item)
	m.pendingItems[newMockPendingItemKey(orgID, uuid.Nil, ItemBlock, blockID, item.Identity())] = nil
	now := time.Now().UTC()
	m.activeQueueOrgs[orgID] = now
	m.dirtyQueueOrgs[orgID] = now
}

// gcItemIdentityForTest looks up the durable identity of a DLQ row the way an
// admin client does: read the listing, then act on exactly the row you saw.
// Every DLQ mutation now requires that identity, so a test cannot accidentally
// exercise a prefix match that production can no longer perform.
func gcItemIdentityForTest(store *MockStore, orgID uuid.UUID, failedAt time.Time, itemType ItemType, itemID string) GCItemIdentity {
	items, err := store.ListFailedItems(orgID, 0)
	if err != nil {
		return GCItemIdentityAt(failedAt)
	}
	for _, item := range items {
		if item.FailedAt.Equal(failedAt) && item.ItemType == itemType && item.ItemID == itemID {
			return item.Identity()
		}
	}
	// No such row: hand back a well-formed identity so the call still exercises
	// the not-found path rather than an argument-validation error.
	return GCItemIdentityAt(failedAt)
}
