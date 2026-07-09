package gc

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestWorker_GracePeriod_BlocksRecentItems ensures the worker respects the grace
// period and does NOT process items that are too new (even if queued).
func TestWorker_GracePeriod_BlocksRecentItems(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	// 1-hour grace period
	w := NewWorker(store, nil, q, 100, 1*time.Hour, false, stats)

	orgID := uuid.New()
	store.AddBlock(orgID, "block-fresh", "hot", 0)

	// Enqueue the item NOW — it's within the grace period
	store.EnqueueItem(orgID, time.Now(), ItemBlock, "block-fresh", uuid.Nil, "hot", 0)

	ctx := context.Background()
	n, err := w.ProcessOnce(ctx)
	if err != nil {
		t.Fatalf("ProcessOnce failed: %v", err)
	}
	// Nothing should be processed — item is too new
	if n != 0 {
		t.Errorf("expected 0 processed (inside grace period), got %d", n)
	}

	// Block must still exist
	if store.GetBlock(orgID, "block-fresh") == nil {
		t.Error("block should still exist during grace period")
	}
}

// TestWorker_GracePeriod_AllowsOldItems verifies that items placed just beyond
// the grace period are processed normally.
func TestWorker_GracePeriod_AllowsOldItems(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	// 1-minute grace period
	w := NewWorker(store, nil, q, 100, 1*time.Minute, false, stats)

	orgID := uuid.New()
	store.AddBlock(orgID, "block-old", "hot", 0)

	// Queued 2 minutes ago — past the grace period
	store.EnqueueItem(orgID, time.Now().Add(-2*time.Minute), ItemBlock, "block-old", uuid.Nil, "hot", 0)

	ctx := context.Background()
	n, err := w.ProcessOnce(ctx)
	if err != nil {
		t.Fatalf("ProcessOnce failed: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 processed (past grace period), got %d", n)
	}
	if store.GetBlock(orgID, "block-old") != nil {
		t.Error("block should have been deleted (past grace period, ref_count=0)")
	}
}

// TestWorker_HOLBlocking_RequeueMovesItemToBack verifies that a failed item is
// requeued with a new timestamp (moved to the back of the queue), preventing
// head-of-line blocking.
func TestWorker_HOLBlocking_RequeueMovesItemToBack(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	firstQueuedAt := time.Now().Add(-5 * time.Hour)

	// Enqueue an item with unknown type — will fail every time
	store.EnqueueItem(orgID, firstQueuedAt, ItemType("unknown_type"), "stuck-item", uuid.Nil, "", 0)

	ctx := context.Background()
	_, _ = w.ProcessOnce(ctx)

	// After failure the item should be requeued at a new (later) timestamp
	items := store.QueueItems(orgID)
	if len(items) != 1 {
		t.Fatalf("expected 1 item requeued, got %d", len(items))
	}
	requeuedItem := items[0]
	if !requeuedItem.QueuedAt.After(firstQueuedAt) {
		t.Errorf("requeued item should have a newer QueuedAt (%v) than original (%v)", requeuedItem.QueuedAt, firstQueuedAt)
	}
	if requeuedItem.RetryCount != 1 {
		t.Errorf("RetryCount = %d, want 1", requeuedItem.RetryCount)
	}
}

// TestWorker_MaxRetry_MovesItemToFailedQueue verifies that retry-capped items
// are removed from the live queue and captured in the DLQ instead of lingering
// until queue TTL cleanup.
func TestWorker_MaxRetry_MovesItemToFailedQueue(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	originalQueuedAt := time.Now().Add(-2 * time.Hour)

	// Enqueue with retry_count already at 5 (the cap)
	store.EnqueueItem(orgID, originalQueuedAt, ItemType("unknown_type"), "maxed-item", uuid.Nil, "", 5)

	ctx := context.Background()
	_, _ = w.ProcessOnce(ctx)

	// The item should leave the live queue and move to failedItems.
	items := store.QueueItems(orgID)
	if len(items) != 0 {
		t.Fatalf("expected 0 live queue items after retry cap, got %d", len(items))
	}

	failedItems := store.FailedItems(orgID)
	if len(failedItems) != 1 {
		t.Fatalf("expected 1 failed item after retry cap, got %d", len(failedItems))
	}
	failed := failedItems[0]
	if failed.ItemID != "maxed-item" {
		t.Fatalf("failed item id = %q, want %q", failed.ItemID, "maxed-item")
	}
	if failed.RetryCount != 5 {
		t.Fatalf("failed item retry count = %d, want 5", failed.RetryCount)
	}
	if failed.LastError == "" {
		t.Fatal("expected failed item to retain the last error")
	}
	if !failed.QueuedAt.Equal(originalQueuedAt) {
		t.Fatalf("failed item queued_at changed: got %v want %v", failed.QueuedAt, originalQueuedAt)
	}
}

// TestWorker_IncrementRetryFailure_EscalatesToDLQ verifies that when the
// underlying RequeueItem call fails (e.g. a transient DB error), the worker
// escalates the item to the DLQ instead of leaving it in the live queue with
// the same RetryCount — which would re-dequeue and re-fail every tick (livelock).
func TestWorker_IncrementRetryFailure_EscalatesToDLQ(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	originalQueuedAt := time.Now().Add(-2 * time.Hour)

	// retry_count=0 so the worker would normally call IncrementRetry. Item
	// type is unknown so processItem returns an error, triggering the retry path.
	if err := store.EnqueueItem(orgID, originalQueuedAt, ItemType("unknown_type"), "stuck-item", uuid.Nil, "", 0); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	store.requeueItemErr = errors.New("simulated DB outage on RequeueItem")

	if _, err := w.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce: %v", err)
	}

	failed := store.FailedItems(orgID)
	if len(failed) != 1 {
		t.Fatalf("expected 1 DLQ entry after IncrementRetry failure, got %d", len(failed))
	}
	if failed[0].ItemID != "stuck-item" {
		t.Fatalf("DLQ entry id = %q, want %q", failed[0].ItemID, "stuck-item")
	}
	if failed[0].RetryCount != 0 {
		t.Fatalf("DLQ entry RetryCount = %d, want 0 (escalation should preserve original count)", failed[0].RetryCount)
	}
	if failed[0].LastError == "" {
		t.Fatal("expected DLQ entry to capture an error message that includes the requeue failure")
	}
	if !strings.Contains(failed[0].LastError, "requeue failed") {
		t.Fatalf("expected DLQ LastError to mention requeue failure, got %q", failed[0].LastError)
	}

	if items := store.QueueItems(orgID); len(items) != 0 {
		t.Fatalf("expected live queue to be drained after DLQ escalation, got %d items", len(items))
	}
}

// TestWorker_IncrementRetryAmbiguousError_DoesNotDuplicate covers the
// dangerous case the reviewer flagged: RequeueItem is a LoggedBatch
// (DELETE old + INSERT new) and Cassandra timeout/unavailable responses
// are ambiguous — the batch may have actually committed even though the
// client received an error. Escalating blindly to the DLQ would create
// both a live requeued row (in gc_queue) and a DLQ entry (in
// gc_failed_items), causing double processing and a lying DLQ.
//
// The worker must verify the original row's existence after a failed
// IncrementRetry: if the old row is gone, the batch did apply and we
// must NOT touch the DLQ.
func TestWorker_IncrementRetryAmbiguousError_DoesNotDuplicate(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	originalQueuedAt := time.Now().Add(-2 * time.Hour).UTC()

	if err := store.EnqueueItem(orgID, originalQueuedAt, ItemType("unknown_type"), "ambiguous-item", uuid.Nil, "", 0); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// Simulate the ambiguous case: the LoggedBatch APPLIED (mock mutates
	// queue normally), but RequeueItem returns a timeout error to the
	// caller — exactly the situation Cassandra produces under coordinator
	// failover.
	store.requeueItemErrAfterMutate = errors.New("simulated cassandra write timeout — batch may have applied")

	if _, err := w.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce: %v", err)
	}

	if failed := store.FailedItems(orgID); len(failed) != 0 {
		t.Fatalf("expected NO DLQ entries when requeue actually applied, got %d entries: %#v", len(failed), failed)
	}

	items := store.QueueItems(orgID)
	if len(items) != 1 {
		t.Fatalf("expected exactly 1 live queue row after successful (but ambiguous) requeue, got %d", len(items))
	}
	if items[0].ItemID != "ambiguous-item" {
		t.Fatalf("live queue item id = %q, want %q", items[0].ItemID, "ambiguous-item")
	}
	if items[0].RetryCount != 1 {
		t.Fatalf("live queue item RetryCount = %d, want 1 (requeue should have bumped it)", items[0].RetryCount)
	}
	if items[0].QueuedAt.Equal(originalQueuedAt) {
		t.Fatalf("live queue item still has the original queued_at; the requeue did not actually move it to the back of the queue")
	}
}

func TestWorker_IncrementRetryAmbiguousCascadeError_DoesNotFalseDLQ(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	libraryID := uuid.New()
	deletedAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)

	store.AddOrganization(orgID)
	store.AddDeletedLibrary(orgID, libraryID, "hot", deletedAt)
	store.deleteRestoreJobsByLibraryErr = errors.New("restore jobs unavailable")
	store.requeueItemErrAfterMutate = errors.New("simulated cassandra write timeout — batch may have applied")

	store.SeedQueueItemForTest(orgID, deletedAt, ItemLibraryCascade, libraryID.String(), uuid.Nil, "hot", 0)

	if _, err := w.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce: %v", err)
	}

	if failed := store.FailedItems(orgID); len(failed) != 0 {
		t.Fatalf("expected NO DLQ entries when ambiguous cascade requeue applied, got %d entries: %#v", len(failed), failed)
	}

	items := store.QueueItems(orgID)
	if len(items) != 1 {
		t.Fatalf("expected exactly 1 live cascade queue row after ambiguous requeue, got %d", len(items))
	}
	if items[0].ItemType != ItemLibraryCascade {
		t.Fatalf("live queue item type = %q, want %q", items[0].ItemType, ItemLibraryCascade)
	}
	if items[0].RetryCount != 1 {
		t.Fatalf("live queue item RetryCount = %d, want 1", items[0].RetryCount)
	}
	if items[0].QueuedAt.Equal(deletedAt) {
		t.Fatal("ambiguous cascade requeue kept original queued_at; item was not moved to the back of the queue")
	}
	if !items[0].IdentityAt.Equal(deletedAt) {
		t.Fatalf("live queue item IdentityAt = %v, want %v", items[0].IdentityAt, deletedAt)
	}
}

func TestWorker_ProcessLibraryCascade_RetriedChildDoesNotDuplicateOnParentRetry(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	libID := uuid.New()
	deletedAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)

	store.AddOrganization(orgID)
	store.AddDeletedLibrary(orgID, libID, "hot", deletedAt)
	store.AddCommit(libID, "commit-1", "fs-root-1")
	store.AddCommit(libID, "commit-2", "fs-root-2")
	store.AddFSObject(libID, "fs-root-1", "dir", nil)
	store.AddFSObject(libID, "fs-root-2", "dir", nil)
	store.deleteRestoreJobsByLibraryErr = errors.New("restore jobs unavailable")

	parent := QueueItem{OrgID: orgID, QueuedAt: deletedAt, IdentityAt: deletedAt, ItemType: ItemLibraryCascade, ItemID: libID.String(), StorageClass: "hot"}
	if err := w.processLibraryCascade(context.Background(), parent); err == nil {
		t.Fatal("expected first library cascade attempt to fail")
	}

	var retriedChild QueueItem
	for _, item := range store.QueueItems(orgID) {
		if item.ItemType == ItemCommit {
			retriedChild = item
			break
		}
	}
	if retriedChild.ItemID == "" {
		t.Fatal("expected commit child to be enqueued")
	}
	if err := q.IncrementRetry(retriedChild); err != nil {
		t.Fatalf("IncrementRetry(child) failed: %v", err)
	}

	if err := w.processLibraryCascade(context.Background(), parent); err == nil {
		t.Fatal("expected second library cascade attempt to fail while auxiliary cleanup stays broken")
	}

	items := store.QueueItems(orgID)
	commitCount := 0
	fsCount := 0
	for _, item := range items {
		switch item.ItemType {
		case ItemCommit:
			commitCount++
		case ItemFSObject:
			fsCount++
		}
	}
	if commitCount != 2 {
		t.Fatalf("expected 2 commit children after parent retry, got %d", commitCount)
	}
	if fsCount != 2 {
		t.Fatalf("expected 2 fs_object children after parent retry, got %d", fsCount)
	}
}

func TestWorker_ProcessOrg_PreservesActiveOrgOnConcurrentEnqueue(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	base := time.Now().UTC()
	queuedAt := base.Add(-2 * time.Hour)
	clockCalls := atomic.Int32{}
	w.clock = func() time.Time {
		if clockCalls.Add(1) == 1 {
			return base
		}
		return base.Add(2 * time.Second)
	}

	store.AddBlock(orgID, "old-block", "hot", 0)
	if err := store.EnqueueItem(orgID, queuedAt, ItemBlock, "old-block", uuid.Nil, "hot", 0); err != nil {
		t.Fatalf("enqueue old item: %v", err)
	}

	hooked := atomic.Bool{}
	store.removeActiveOrgHook = func(hookOrgID uuid.UUID, activeBefore time.Time) {
		if hookOrgID != orgID || !hooked.CompareAndSwap(false, true) {
			return
		}
		if err := store.EnqueueItem(orgID, base.Add(3*time.Second), ItemShareLink, "new-item", uuid.Nil, "", 0); err != nil {
			t.Fatalf("enqueue concurrent item: %v", err)
		}
	}

	processed, err := w.processOrg(context.Background(), orgID)
	if err != nil {
		t.Fatalf("processOrg failed: %v", err)
	}
	if processed != 1 {
		t.Fatalf("processed = %d, want 1", processed)
	}
	if !store.IsOrgActive(orgID) {
		t.Fatal("expected org to remain active after concurrent enqueue")
	}
	items := store.QueueItems(orgID)
	if len(items) != 1 || items[0].ItemID != "new-item" {
		t.Fatalf("expected concurrent queue item to remain live, got %#v", items)
	}
}

func TestWorker_ProcessOrg_RemovesStaleActiveOrgWhenQueueEmpty(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	base := time.Now().UTC()
	store.MarkOrgActive(orgID, base.Add(-2*time.Second))
	w.clock = func() time.Time {
		return base
	}

	processed, err := w.processOrg(context.Background(), orgID)
	if err != nil {
		t.Fatalf("processOrg failed: %v", err)
	}
	if processed != 0 {
		t.Fatalf("processed = %d, want 0", processed)
	}
	if store.IsOrgActive(orgID) {
		t.Fatal("expected stale active org to be removed after empty dequeue")
	}
	if len(store.QueueItems(orgID)) != 0 {
		t.Fatalf("expected queue to stay empty, got %#v", store.QueueItems(orgID))
	}
}

func TestWorker_ProcessOrg_KeepsActiveOrgWhenOnlyGraceBlockedItemsRemain(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, time.Hour, false, stats)

	orgID := uuid.New()
	base := time.Now().UTC()
	queuedAt := base.Add(-30 * time.Second)
	store.MarkOrgActive(orgID, base.Add(-2*time.Second))
	w.clock = func() time.Time {
		return base
	}

	if err := store.EnqueueItem(orgID, queuedAt, ItemBlock, "grace-blocked", uuid.Nil, "hot", 0); err != nil {
		t.Fatalf("enqueue grace-blocked item: %v", err)
	}

	processed, err := w.processOrg(context.Background(), orgID)
	if err != nil {
		t.Fatalf("processOrg failed: %v", err)
	}
	if processed != 0 {
		t.Fatalf("processed = %d, want 0", processed)
	}
	if !store.IsOrgActive(orgID) {
		t.Fatal("expected org to remain active while queued items are still within grace period")
	}
	items := store.QueueItems(orgID)
	if len(items) != 1 || items[0].ItemID != "grace-blocked" {
		t.Fatalf("expected grace-blocked item to remain queued, got %#v", items)
	}
}

func TestWorker_ProcessOrg_RemovesStaleActiveOrgWhenSnapshotStaysPositiveButQueueIsEmpty(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	base := time.Now().UTC()
	store.MarkOrgActive(orgID, base.Add(-2*time.Second))
	store.orgQueueStats[orgID] = GCOrgStats{OrgID: orgID, QueueDepth: 9, UpdatedAt: base.Add(-time.Minute)}
	w.clock = func() time.Time {
		return base
	}

	processed, err := w.processOrg(context.Background(), orgID)
	if err != nil {
		t.Fatalf("processOrg failed: %v", err)
	}
	if processed != 0 {
		t.Fatalf("processed = %d, want 0", processed)
	}
	if store.IsOrgActive(orgID) {
		t.Fatal("expected stale active org to be removed when real queue is empty even if snapshot queue_depth is positive")
	}
	if len(store.QueueItems(orgID)) != 0 {
		t.Fatalf("expected queue to stay empty, got %#v", store.QueueItems(orgID))
	}
}

// TestWorker_StorageLeak_LWTSkipsLiveBlock is a regression test that ensures
// the LWT guard prevents a block from being deleted from S3 when its ref_count
// is still > 0 at the moment the GC tries to process it.
// This simulates a race where another upload incremented ref_count between
// the block being enqueued and the worker running.
func TestWorker_StorageLeak_LWTSkipsLiveBlock(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)

	orgID := uuid.New()

	// Block is enqueued for deletion (ref_count was 0 when enqueued)
	// but by the time the worker runs, another file has been uploaded
	// that references this block — ref_count is now 1.
	store.AddBlock(orgID, "shared-block", "hot", 1)
	store.EnqueueItem(orgID, time.Now().Add(-2*time.Hour), ItemBlock, "shared-block", uuid.Nil, "hot", 0)

	ctx := context.Background()
	n, err := w.ProcessOnce(ctx)
	if err != nil {
		t.Fatalf("ProcessOnce failed: %v", err)
	}
	// Item is counted as "processed" (skipped gracefully via LWT)
	if n != 1 {
		t.Errorf("expected 1 processed (skipped by LWT), got %d", n)
	}

	// Block MUST still exist — deleting it would cause data corruption
	if store.GetBlock(orgID, "shared-block") == nil {
		t.Fatal("STORAGE LEAK REGRESSION: live block was deleted despite ref_count > 0")
	}

	// S3 must NOT have been touched
	if len(sp.DeletedBlocks()) != 0 {
		t.Fatalf("STORAGE LEAK REGRESSION: S3 deletion was triggered for a live block")
	}
}

// TestWorker_Paginated_ProcessesMultipleOrgs verifies that the worker iterates
// across all orgs with queued items in a single ProcessOnce call.
func TestWorker_Paginated_ProcessesMultipleOrgs(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgA := uuid.New()
	orgB := uuid.New()
	orgC := uuid.New()

	store.AddBlock(orgA, "blk-a", "hot", 0)
	store.AddBlock(orgB, "blk-b", "hot", 0)
	store.AddBlock(orgC, "blk-c", "hot", 0)

	store.EnqueueItem(orgA, time.Now().Add(-2*time.Hour), ItemBlock, "blk-a", uuid.Nil, "hot", 0)
	store.EnqueueItem(orgB, time.Now().Add(-2*time.Hour), ItemBlock, "blk-b", uuid.Nil, "hot", 0)
	store.EnqueueItem(orgC, time.Now().Add(-2*time.Hour), ItemBlock, "blk-c", uuid.Nil, "hot", 0)

	ctx := context.Background()
	n, err := w.ProcessOnce(ctx)
	if err != nil {
		t.Fatalf("ProcessOnce failed: %v", err)
	}
	if n != 3 {
		t.Errorf("expected 3 total processed across 3 orgs, got %d", n)
	}
	if store.GetBlock(orgA, "blk-a") != nil {
		t.Error("blk-a should be deleted")
	}
	if store.GetBlock(orgB, "blk-b") != nil {
		t.Error("blk-b should be deleted")
	}
	if store.GetBlock(orgC, "blk-c") != nil {
		t.Error("blk-c should be deleted")
	}
}

// TestWorker_Paginated_BatchSizeRespected ensures the batch size cap limits how
// many items are dequeued per org per ProcessOnce pass.
func TestWorker_Paginated_BatchSizeRespected(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	batchSize := 3
	w := NewWorker(store, nil, q, batchSize, 0, false, stats)

	orgID := uuid.New()
	// Enqueue 10 blocks
	for i := 0; i < 10; i++ {
		id := "batch-blk-" + string(rune('a'+i))
		store.AddBlock(orgID, id, "hot", 0)
		store.EnqueueItem(orgID, time.Now().Add(-2*time.Hour), ItemBlock, id, uuid.Nil, "hot", 0)
	}

	ctx := context.Background()
	n, err := w.ProcessOnce(ctx)
	if err != nil {
		t.Fatalf("ProcessOnce failed: %v", err)
	}
	// Only up to batchSize should be processed per pass
	if n > batchSize {
		t.Errorf("processed %d items but batchSize=%d — batch limit not respected", n, batchSize)
	}
	// At least 1 should still be in the queue
	remaining := len(store.QueueItems(orgID))
	if remaining == 0 {
		t.Errorf("expected some items remaining after batch-limited pass, got 0")
	}
}

// TestQueue_IncrementRetry_UpdatesTimestampAndCount verifies that IncrementRetry
// moves the item to the back of the queue with a newer QueuedAt and higher RetryCount.
func TestQueue_IncrementRetry_UpdatesTimestampAndCount(t *testing.T) {
	store := NewMockStore()
	q := NewQueue(store)

	orgID := uuid.New()
	originalQueuedAt := time.Now().Add(-1 * time.Hour).UTC().Truncate(time.Millisecond)
	store.EnqueueItem(orgID, originalQueuedAt, ItemBlock, "blk-retry", uuid.Nil, "hot", 2)

	// Fetch the item
	items, err := store.DequeueBatch(orgID, 1, time.Now())
	if err != nil || len(items) != 1 {
		t.Fatalf("DequeueBatch failed: %v / items=%d", err, len(items))
	}
	item := items[0]

	beforeRequeue := time.Now()
	if err := q.IncrementRetry(item); err != nil {
		t.Fatalf("IncrementRetry failed: %v", err)
	}

	requeued := store.QueueItems(orgID)
	if len(requeued) != 1 {
		t.Fatalf("expected 1 item after requeue, got %d", len(requeued))
	}
	r := requeued[0]
	if r.RetryCount != 3 {
		t.Errorf("RetryCount = %d, want 3", r.RetryCount)
	}
	if !r.QueuedAt.After(beforeRequeue) && !r.QueuedAt.Equal(beforeRequeue) {
		t.Errorf("requeued QueuedAt (%v) should be >= beforeRequeue (%v)", r.QueuedAt, beforeRequeue)
	}
	if r.QueuedAt.Equal(originalQueuedAt) {
		t.Error("requeued QueuedAt should differ from original (HOL prevention)")
	}
}
