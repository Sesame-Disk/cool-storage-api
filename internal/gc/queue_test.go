package gc

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/google/uuid"
)

func TestItemType_Constants(t *testing.T) {
	tests := []struct {
		itemType ItemType
		want     string
	}{
		{ItemBlock, "block"},
		{ItemCommit, "commit"},
		{ItemFSObject, "fs_object"},
		{ItemShareLink, "share_link"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if string(tt.itemType) != tt.want {
				t.Errorf("ItemType = %q, want %q", string(tt.itemType), tt.want)
			}
		})
	}
}

func TestQueueItem_Fields(t *testing.T) {
	orgID := uuid.New()
	libID := uuid.New()

	item := QueueItem{
		OrgID:        orgID,
		ItemType:     ItemBlock,
		ItemID:       "abc123def456",
		LibraryID:    libID,
		StorageClass: "hot",
		RetryCount:   0,
	}

	if item.OrgID != orgID {
		t.Errorf("OrgID = %v, want %v", item.OrgID, orgID)
	}
	if item.ItemType != ItemBlock {
		t.Errorf("ItemType = %v, want %v", item.ItemType, ItemBlock)
	}
	if item.ItemID != "abc123def456" {
		t.Errorf("ItemID = %v, want abc123def456", item.ItemID)
	}
}

func TestNewQueue_WithMockStore(t *testing.T) {
	store := NewMockStore()
	q := NewQueue(store)
	if q == nil {
		t.Fatal("NewQueue returned nil")
	}
}

func TestQueue_EnqueueAndDequeue(t *testing.T) {
	store := NewMockStore()
	q := NewQueue(store)

	orgID := uuid.New()

	store.AddBlock(orgID, "block-1", "hot", 0)
	ensureAndEnqueueBlockForTest(t, store, orgID, "block-1", "hot", time.Now(), 0)

	// DequeueBatch with 0 grace period should return the item
	items, err := q.DequeueBatch(orgID, 10, 0)
	if err != nil {
		t.Fatalf("DequeueBatch failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].ItemID != "block-1" {
		t.Errorf("ItemID = %q, want block-1", items[0].ItemID)
	}
	if items[0].ItemType != ItemBlock {
		t.Errorf("ItemType = %v, want %v", items[0].ItemType, ItemBlock)
	}
	if items[0].StorageClass != "hot" {
		t.Errorf("StorageClass = %q, want hot", items[0].StorageClass)
	}

	// Complete the item
	err = q.Complete(items[0])
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}

	// DequeueBatch should now return empty
	items, err = q.DequeueBatch(orgID, 10, 0)
	if err != nil {
		t.Fatalf("DequeueBatch failed: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected 0 items after complete, got %d", len(items))
	}
}

func TestQueue_DequeueBatch_GracePeriod(t *testing.T) {
	store := NewMockStore()
	q := NewQueue(store)

	orgID := uuid.New()

	// Enqueue an item (queued at time.Now()).
	store.AddBlock(orgID, "block-1", "hot", 0)
	ensureAndEnqueueBlockForTest(t, store, orgID, "block-1", "hot", time.Now(), 0)

	// DequeueBatch with 1h grace period should return empty (item too new)
	items, err := q.DequeueBatch(orgID, 10, 1*time.Hour)
	if err != nil {
		t.Fatalf("DequeueBatch failed: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected 0 items with 1h grace period, got %d", len(items))
	}

	// DequeueBatch with 0 grace period should return the item
	items, err = q.DequeueBatch(orgID, 10, 0)
	if err != nil {
		t.Fatalf("DequeueBatch failed: %v", err)
	}
	if len(items) != 1 {
		t.Errorf("expected 1 item with 0 grace period, got %d", len(items))
	}
}

func TestQueue_IncrementRetry(t *testing.T) {
	store := NewMockStore()
	q := NewQueue(store)

	orgID := uuid.New()

	store.AddBlock(orgID, "block-1", "hot", 0)
	ensureAndEnqueueBlockForTest(t, store, orgID, "block-1", "hot", time.Now(), 0)

	// Get item
	items, _ := q.DequeueBatch(orgID, 10, 0)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	if items[0].RetryCount != 0 {
		t.Errorf("initial RetryCount = %d, want 0", items[0].RetryCount)
	}

	// Increment retry
	err := q.IncrementRetry(items[0])
	if err != nil {
		t.Fatalf("IncrementRetry failed: %v", err)
	}

	// Re-fetch and verify
	items, _ = q.DequeueBatch(orgID, 10, 0)
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].RetryCount != 1 {
		t.Errorf("RetryCount = %d after increment, want 1", items[0].RetryCount)
	}

	// Increment again
	q.IncrementRetry(items[0])
	items, _ = q.DequeueBatch(orgID, 10, 0)
	if items[0].RetryCount != 2 {
		t.Errorf("RetryCount = %d after 2nd increment, want 2", items[0].RetryCount)
	}
}

func TestQueue_IncrementRetry_PreservesIdentityAtForCascadeItems(t *testing.T) {
	store := NewMockStore()
	q := NewQueue(store)

	orgID := uuid.New()
	originalQueuedAt := time.Now().Add(-1 * time.Hour).UTC().Truncate(time.Millisecond)
	blockRepresentationID := db.PlainBlockRepresentationID
	if err := store.EnqueueBatch([]QueueItem{{
		OrgID:                 orgID,
		QueuedAt:              originalQueuedAt,
		IdentityAt:            originalQueuedAt,
		ItemType:              ItemLibraryCascade,
		ItemID:                uuid.New().String(),
		LibraryID:             uuid.Nil,
		BlockRepresentationID: blockRepresentationID,
		StorageClass:          "hot",
		RetryCount:            2,
	}}); err != nil {
		t.Fatalf("EnqueueBatch failed: %v", err)
	}

	items, err := store.DequeueBatch(orgID, 1, time.Now())
	if err != nil || len(items) != 1 {
		t.Fatalf("DequeueBatch failed: %v / items=%d", err, len(items))
	}

	if err := q.IncrementRetry(items[0]); err != nil {
		t.Fatalf("IncrementRetry failed: %v", err)
	}

	requeued := store.QueueItems(orgID)
	if len(requeued) != 1 {
		t.Fatalf("expected 1 item after requeue, got %d", len(requeued))
	}
	if requeued[0].RetryCount != 3 {
		t.Errorf("RetryCount = %d, want 3", requeued[0].RetryCount)
	}
	if requeued[0].QueuedAt.Equal(originalQueuedAt) {
		t.Fatalf("cascade retry QueuedAt = %v, want a newer back-of-queue timestamp", requeued[0].QueuedAt)
	}
	if !requeued[0].IdentityAt.Equal(originalQueuedAt) {
		t.Errorf("cascade retry IdentityAt = %v, want %v", requeued[0].IdentityAt, originalQueuedAt)
	}
	if requeued[0].BlockRepresentationID != blockRepresentationID {
		t.Errorf("cascade retry BlockRepresentationID = %q, want %q", requeued[0].BlockRepresentationID, blockRepresentationID)
	}
}

func TestQueue_NonBlockIdentityAtSurvivesRetryAndCompletion(t *testing.T) {
	store := NewMockStore()
	queue := NewQueue(store)
	orgID := uuid.New()
	queuedAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)
	identityAt := queuedAt.Add(-time.Hour)
	item := QueueItem{
		OrgID:      orgID,
		QueuedAt:   queuedAt,
		IdentityAt: identityAt,
		ItemType:   ItemShareLink,
		ItemID:     "share-with-stale-queue-time",
	}
	if err := queue.EnqueueBatch([]QueueItem{item}); err != nil {
		t.Fatalf("EnqueueBatch failed: %v", err)
	}

	items, err := queue.DequeueBatch(orgID, 1, 0)
	if err != nil || len(items) != 1 {
		t.Fatalf("DequeueBatch failed: %v / items=%d", err, len(items))
	}
	if !items[0].IdentityAt.Equal(identityAt) {
		t.Fatalf("dequeued IdentityAt = %v, want %v", items[0].IdentityAt, identityAt)
	}

	if err := queue.IncrementRetry(items[0]); err != nil {
		t.Fatalf("IncrementRetry failed: %v", err)
	}
	retried := store.QueueItems(orgID)
	if len(retried) != 1 || !retried[0].IdentityAt.Equal(identityAt) {
		t.Fatalf("requeued items = %+v, want one item retaining identity_at=%v", retried, identityAt)
	}
	if err := queue.Complete(retried[0]); err != nil {
		t.Fatalf("Complete failed: %v", err)
	}
	if got := store.QueueLen(); got != 0 {
		t.Fatalf("queue length after completion = %d, want 0", got)
	}
}

func TestQueue_BlockCandidateIdentityIsRequiredAndSurvivesRetryAndDLQ(t *testing.T) {
	store := NewMockStore()
	queue := NewQueue(store)
	orgID := uuid.New()
	queuedAt := time.Now().UTC().Add(-time.Hour)
	store.AddBlock(orgID, "block-exact-identity", "hot", 0)
	candidate, err := store.EnsureBlockGCCandidateExact(orgID, "block-exact-identity", "hot", queuedAt)
	if err != nil {
		t.Fatalf("EnsureBlockGCCandidateExact: %v", err)
	}

	incomplete := QueueItem{OrgID: orgID, QueuedAt: queuedAt, ItemType: ItemBlock, ItemID: candidate.BlockID}
	if err := queue.EnqueueBatch([]QueueItem{incomplete}); err == nil {
		t.Fatal("block item without exact candidate identity was accepted")
	}

	item := QueueItem{
		OrgID:                    orgID,
		QueuedAt:                 queuedAt,
		IdentityAt:               candidate.CandidateAt,
		ItemType:                 ItemBlock,
		ItemID:                   candidate.BlockID,
		StorageClass:             candidate.StorageClass(),
		BlockGCCandidateIdentity: candidate.Identity(),
	}
	if err := queue.EnqueueBatch([]QueueItem{item}); err != nil {
		t.Fatalf("EnqueueBatch: %v", err)
	}
	dequeued, err := queue.DequeueBatch(orgID, 1, 0)
	if err != nil || len(dequeued) != 1 {
		t.Fatalf("DequeueBatch: items=%d err=%v", len(dequeued), err)
	}
	if got := dequeued[0]; got.BlockGCCandidateIdentity != candidate.Identity() || !got.IdentityAt.Equal(candidate.CandidateAt) {
		t.Fatalf("dequeued identity = %+v / %s, want %+v / %s", got.BlockGCCandidateIdentity, got.IdentityAt, candidate.Identity(), candidate.CandidateAt)
	}
	if err := queue.IncrementRetry(dequeued[0]); err != nil {
		t.Fatalf("IncrementRetry: %v", err)
	}
	retried := store.QueueItems(orgID)[0]
	if retried.BlockGCCandidateIdentity != candidate.Identity() || !retried.IdentityAt.Equal(candidate.CandidateAt) {
		t.Fatalf("retried identity = %+v / %s, want %+v / %s", retried.BlockGCCandidateIdentity, retried.IdentityAt, candidate.Identity(), candidate.CandidateAt)
	}
	if err := store.FailItem(retried, time.Now().UTC(), "boom", "test"); err != nil {
		t.Fatalf("FailItem: %v", err)
	}
	failed := store.FailedItems(orgID)
	if len(failed) != 1 || failed[0].BlockGCCandidateIdentity != candidate.Identity() {
		t.Fatalf("failed item identity = %+v, want %+v", failed, candidate.Identity())
	}
	if err := store.RequeueFailedItem(orgID, failed[0].FailedAt, ItemBlock, candidate.BlockID, time.Now().UTC(), candidate.Identity()); err != nil {
		t.Fatalf("RequeueFailedItem: %v", err)
	}
	if requeued := store.QueueItems(orgID); len(requeued) != 1 || requeued[0].BlockGCCandidateIdentity != candidate.Identity() || !requeued[0].IdentityAt.Equal(candidate.CandidateAt) {
		t.Fatalf("requeued identity = %+v, want %+v", requeued, candidate.Identity())
	}
}

func TestQueue_CompleteUsesFullBlockCandidateIdentity(t *testing.T) {
	store := NewMockStore()
	queue := NewQueue(store)
	orgID := uuid.New()
	queuedAt := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	candidateAt := queuedAt.Add(-time.Hour)

	p1 := QueueItem{
		OrgID:                    orgID,
		QueuedAt:                 queuedAt,
		IdentityAt:               candidateAt,
		ItemType:                 ItemBlock,
		ItemID:                   "same-block",
		StorageClass:             "hot",
		BlockGCCandidateIdentity: BlockGCCandidateIdentity{Target: BlockDeleteTarget{StorageClass: "hot", StorageKey: "p1"}, CandidateAt: candidateAt},
	}
	p2 := p1
	p2.BlockGCCandidateIdentity.Target.StorageKey = "p2"

	if err := queue.EnqueueBatch([]QueueItem{p1, p2}); err != nil {
		t.Fatalf("EnqueueBatch: %v", err)
	}
	if err := queue.Complete(p1); err != nil {
		t.Fatalf("Complete(P1): %v", err)
	}

	remaining := store.QueueItems(orgID)
	if len(remaining) != 1 || remaining[0].BlockGCCandidateIdentity != p2.BlockGCCandidateIdentity {
		t.Fatalf("remaining queue items = %+v, want only P2", remaining)
	}
}

func TestQueue_CompleteRejectsLegacyBlockItem(t *testing.T) {
	queue := NewQueue(NewMockStore())
	err := queue.Complete(QueueItem{ItemType: ItemBlock, ItemID: "legacy-block"})
	if err == nil || !strings.Contains(err.Error(), "exact block GC candidate identity") {
		t.Fatalf("Complete(legacy block) error = %v, want exact-identity rejection", err)
	}
}

func TestQueue_CompletePreservesNonBlockBehavior(t *testing.T) {
	store := NewMockStore()
	queue := NewQueue(store)
	orgID := uuid.New()
	if err := queue.Enqueue(orgID, ItemShareLink, "share-link", uuid.Nil, ""); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	items, err := queue.DequeueBatch(orgID, 1, 0)
	if err != nil || len(items) != 1 {
		t.Fatalf("DequeueBatch: items=%d err=%v", len(items), err)
	}
	if err := queue.Complete(items[0]); err != nil {
		t.Fatalf("Complete(non-block): %v", err)
	}
	if got := store.QueueLen(); got != 0 {
		t.Fatalf("queue length after non-block completion = %d, want 0", got)
	}
}

func TestGCQueueBucket_DeterministicAndDistributedByIdentity(t *testing.T) {
	orgID := uuid.New()
	first := gcQueueBucket(orgID, ItemBlock, "block-1")
	second := gcQueueBucket(orgID, ItemBlock, "block-1")
	if first != second {
		t.Fatalf("bucket changed for same queue identity: %d != %d", first, second)
	}
	if first < 0 || first >= gcDefaultQueueBucketCount {
		t.Fatalf("bucket = %d, want [0,%d)", first, gcDefaultQueueBucketCount)
	}

	buckets := make(map[int]struct{})
	for i := 0; i < 128; i++ {
		buckets[gcQueueBucket(orgID, ItemBlock, fmt.Sprintf("block-%d", i))] = struct{}{}
	}
	if len(buckets) < gcDefaultQueueBucketCount/2 {
		t.Fatalf("expected queue identities to spread across buckets, got %d distinct buckets", len(buckets))
	}
}

func TestGCPendingItemBucket_DeterministicAndDistributedByIdentity(t *testing.T) {
	orgID := uuid.New()
	libraryID := uuid.New()
	first := gcPendingItemBucket(orgID, libraryID, ItemFSObject, "fs-1")
	second := gcPendingItemBucket(orgID, libraryID, ItemFSObject, "fs-1")
	if first != second {
		t.Fatalf("pending bucket changed for same identity: %d != %d", first, second)
	}
	if first < 0 || first >= gcDefaultQueueBucketCount {
		t.Fatalf("pending bucket = %d, want [0,%d)", first, gcDefaultQueueBucketCount)
	}

	// library_id must participate in the bucket identity: some other library
	// must land in a different bucket. Search deterministically instead of
	// trusting a single random UUID (which collides ~1/bucket-count of the time).
	foundDifferent := false
	for i := 0; i < 64; i++ {
		if gcPendingItemBucket(orgID, uuid.New(), ItemFSObject, "fs-1") != first {
			foundDifferent = true
			break
		}
	}
	if !foundDifferent {
		t.Fatalf("expected library_id to participate in pending bucket identity, all mapped to %d", first)
	}

	buckets := make(map[int]struct{})
	for i := 0; i < 128; i++ {
		buckets[gcPendingItemBucket(orgID, libraryID, ItemFSObject, fmt.Sprintf("fs-%d", i))] = struct{}{}
	}
	if len(buckets) < gcDefaultQueueBucketCount/2 {
		t.Fatalf("expected pending identities to spread across buckets, got %d distinct buckets", len(buckets))
	}
}

func TestQueue_ListOrgsWithQueuedItems(t *testing.T) {
	store := NewMockStore()
	q := NewQueue(store)

	org1 := uuid.New()
	org2 := uuid.New()

	// Enqueue items for two orgs
	store.AddBlock(org1, "b1", "hot", 0)
	ensureAndEnqueueBlockForTest(t, store, org1, "b1", "hot", time.Now(), 0)
	if err := q.EnqueueBatch([]QueueItem{{
		OrgID:                 org2,
		QueuedAt:              time.Now(),
		ItemType:              ItemCommit,
		ItemID:                "c1",
		LibraryID:             uuid.New(),
		BlockRepresentationID: db.PlainBlockRepresentationID,
	}}); err != nil {
		t.Fatalf("EnqueueBatch ItemCommit failed: %v", err)
	}

	orgs, err := q.ListOrgsWithQueuedItems()
	if err != nil {
		t.Fatalf("ListOrgsWithQueuedItems failed: %v", err)
	}
	if len(orgs) != 2 {
		t.Errorf("expected 2 orgs, got %d", len(orgs))
	}

	// Complete all items for org1
	items, _ := q.DequeueBatch(org1, 10, 0)
	for _, item := range items {
		q.Complete(item)
	}

	orgs, _ = q.ListOrgsWithQueuedItems()
	if len(orgs) != 1 {
		t.Errorf("expected 1 org after completing org1 items, got %d", len(orgs))
	}
}

func TestQueue_GetQueueSize(t *testing.T) {
	store := NewMockStore()
	q := NewQueue(store)

	orgID := uuid.New()

	// Empty queue
	size, err := q.GetQueueSize(orgID)
	if err != nil {
		t.Fatalf("GetQueueSize failed: %v", err)
	}
	if size != 0 {
		t.Errorf("expected size 0, got %d", size)
	}

	// Enqueue 3 items
	store.AddBlock(orgID, "b1", "hot", 0)
	store.AddBlock(orgID, "b2", "hot", 0)
	ensureAndEnqueueBlockForTest(t, store, orgID, "b1", "hot", time.Now(), 0)
	ensureAndEnqueueBlockForTest(t, store, orgID, "b2", "hot", time.Now(), 0)
	if err := q.EnqueueBatch([]QueueItem{{
		OrgID:                 orgID,
		QueuedAt:              time.Now(),
		ItemType:              ItemCommit,
		ItemID:                "c1",
		LibraryID:             uuid.New(),
		BlockRepresentationID: db.PlainBlockRepresentationID,
	}}); err != nil {
		t.Fatalf("EnqueueBatch commit failed: %v", err)
	}

	size, _ = q.GetQueueSize(orgID)
	if size != 3 {
		t.Errorf("expected size 3, got %d", size)
	}

	// Complete 1 item
	items, _ := q.DequeueBatch(orgID, 1, 0)
	q.Complete(items[0])

	size, _ = q.GetQueueSize(orgID)
	if size != 2 {
		t.Errorf("expected size 2 after complete, got %d", size)
	}
}

func TestQueue_GetTotalQueueSize(t *testing.T) {
	store := NewMockStore()
	q := NewQueue(store)

	org1 := uuid.New()
	org2 := uuid.New()

	store.AddBlock(org1, "b1", "hot", 0)
	store.AddBlock(org1, "b2", "hot", 0)
	ensureAndEnqueueBlockForTest(t, store, org1, "b1", "hot", time.Now(), 0)
	ensureAndEnqueueBlockForTest(t, store, org1, "b2", "hot", time.Now(), 0)
	if err := q.EnqueueBatch([]QueueItem{{
		OrgID:                 org2,
		QueuedAt:              time.Now(),
		ItemType:              ItemCommit,
		ItemID:                "c1",
		LibraryID:             uuid.New(),
		BlockRepresentationID: db.PlainBlockRepresentationID,
	}}); err != nil {
		t.Fatalf("EnqueueBatch org2 commit failed: %v", err)
	}

	total, err := q.GetTotalQueueSize()
	if err != nil {
		t.Fatalf("GetTotalQueueSize failed: %v", err)
	}
	if total != 3 {
		t.Errorf("expected total 3, got %d", total)
	}
}

func TestQueue_MultipleItemTypes(t *testing.T) {
	store := NewMockStore()
	q := NewQueue(store)

	orgID := uuid.New()
	libID := uuid.New()

	store.AddBlock(orgID, "block-1", "hot", 0)
	ensureAndEnqueueBlockForTest(t, store, orgID, "block-1", "hot", time.Now(), 0)
	if err := q.EnqueueBatch([]QueueItem{
		{
			OrgID:                 orgID,
			QueuedAt:              time.Now(),
			ItemType:              ItemCommit,
			ItemID:                "commit-1",
			LibraryID:             libID,
			BlockRepresentationID: db.PlainBlockRepresentationID,
		},
		{
			OrgID:                 orgID,
			QueuedAt:              time.Now(),
			ItemType:              ItemFSObject,
			ItemID:                "fs-1",
			LibraryID:             libID,
			BlockRepresentationID: db.PlainBlockRepresentationID,
		},
	}); err != nil {
		t.Fatalf("EnqueueBatch data items failed: %v", err)
	}
	q.Enqueue(orgID, ItemShareLink, "token-abc", uuid.Nil, "")

	items, err := q.DequeueBatch(orgID, 10, 0)
	if err != nil {
		t.Fatalf("DequeueBatch failed: %v", err)
	}
	if len(items) != 4 {
		t.Errorf("expected 4 items, got %d", len(items))
	}

	// Verify all types are present
	typeSet := make(map[ItemType]bool)
	for _, item := range items {
		typeSet[item.ItemType] = true
	}
	for _, expected := range []ItemType{ItemBlock, ItemCommit, ItemFSObject, ItemShareLink} {
		if !typeSet[expected] {
			t.Errorf("missing item type %s", expected)
		}
	}
}

func TestQueue_EnqueueRejectsItemsThatRequireRepresentation(t *testing.T) {
	store := NewMockStore()
	q := NewQueue(store)
	orgID := uuid.New()
	libID := uuid.New()

	if err := q.Enqueue(orgID, ItemCommit, "commit-1", libID, ""); err == nil {
		t.Fatal("expected ItemCommit enqueue to require explicit representation")
	}
	if err := q.Enqueue(orgID, ItemFSObject, "fs-1", libID, ""); err == nil {
		t.Fatal("expected ItemFSObject enqueue to require explicit representation")
	}
	if err := q.EnqueueCascade(orgID, time.Now(), ItemLibraryCascade, libID.String(), uuid.Nil, "hot"); err == nil {
		t.Fatal("expected ItemLibraryCascade enqueue to require explicit representation")
	}
}

func TestQueue_EnqueueBatchRejectsItemsMissingRepresentation(t *testing.T) {
	orgID := uuid.New()
	libID := uuid.New()
	now := time.Now()

	cases := []struct {
		name     string
		itemType ItemType
		itemID   string
	}{
		{"commit", ItemCommit, "commit-1"},
		{"fs_object", ItemFSObject, "fs-1"},
		{"library_cascade", ItemLibraryCascade, libID.String()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := NewMockStore()
			q := NewQueue(store)
			item := QueueItem{
				OrgID:     orgID,
				QueuedAt:  now,
				ItemType:  tc.itemType,
				ItemID:    tc.itemID,
				LibraryID: libID,
			}
			if err := q.EnqueueBatch([]QueueItem{item}); err == nil {
				t.Fatalf("expected %s batch enqueue to require a block representation", tc.itemType)
			}
			if items := store.QueueItems(orgID); len(items) != 0 {
				t.Fatalf("rejected batch must not persist any item, got %#v", items)
			}
		})
	}
}

func TestQueue_EnqueueBatchRejectsItemsWithForeignRepresentation(t *testing.T) {
	orgID := uuid.New()
	libID := uuid.New()
	otherLibID := uuid.New()
	now := time.Now()
	q := NewQueue(NewMockStore())

	err := q.EnqueueBatch([]QueueItem{{
		OrgID:                 orgID,
		QueuedAt:              now,
		ItemType:              ItemCommit,
		ItemID:                "commit-1",
		LibraryID:             libID,
		BlockRepresentationID: db.EncryptedLibraryBlockRepresentationID(otherLibID.String()),
	}})
	if err == nil {
		t.Fatal("expected foreign library representation to be rejected")
	}
}

func TestQueue_EnqueueBatchRejectsNonCanonicalRepresentationFormats(t *testing.T) {
	orgID := uuid.New()
	libID := uuid.New()
	now := time.Now()
	q := NewQueue(NewMockStore())

	cases := []string{
		"library:" + strings.ReplaceAll(libID.String(), "-", ""),
		"library:{" + libID.String() + "}",
	}
	for _, representationID := range cases {
		err := q.EnqueueBatch([]QueueItem{{
			OrgID:                 orgID,
			QueuedAt:              now,
			ItemType:              ItemCommit,
			ItemID:                "commit-1",
			LibraryID:             libID,
			BlockRepresentationID: representationID,
		}})
		if err == nil {
			t.Fatalf("expected non-canonical representation %q to be rejected", representationID)
		}
	}
}

func TestQueue_NewItemTypes(t *testing.T) {
	store := NewMockStore()
	q := NewQueue(store)

	orgID := uuid.New()
	libID := uuid.New()

	// Enqueue the new item types: share and restore_job
	err := q.Enqueue(orgID, ItemShare, "share-1", libID, "")
	if err != nil {
		t.Fatalf("Enqueue ItemShare failed: %v", err)
	}
	err = q.Enqueue(orgID, ItemRestoreJob, "restore-job-1", libID, "")
	if err != nil {
		t.Fatalf("Enqueue ItemRestoreJob failed: %v", err)
	}

	items, err := q.DequeueBatch(orgID, 10, 0)
	if err != nil {
		t.Fatalf("DequeueBatch failed: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	typeSet := make(map[ItemType]bool)
	for _, item := range items {
		typeSet[item.ItemType] = true
	}
	if !typeSet[ItemShare] {
		t.Error("missing ItemShare")
	}
	if !typeSet[ItemRestoreJob] {
		t.Error("missing ItemRestoreJob")
	}
}

func TestItemType_NewConstants(t *testing.T) {
	if string(ItemShare) != "share" {
		t.Errorf("ItemShare = %q, want %q", string(ItemShare), "share")
	}
	if string(ItemRestoreJob) != "restore_job" {
		t.Errorf("ItemRestoreJob = %q, want %q", string(ItemRestoreJob), "restore_job")
	}
}
