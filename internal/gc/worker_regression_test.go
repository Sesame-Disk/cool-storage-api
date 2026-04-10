package gc

import (
	"context"
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

// TestWorker_MaxRetry_StopsRequeuingAt5 verifies that when an item's RetryCount
// reaches the cap (5), the worker does NOT call RequeueItem again (preventing
// infinite requeue storms). The item stays in the queue and relies on Cassandra
// TTL for eventual cleanup — this is intentional in the current design.
func TestWorker_MaxRetry_StopsRequeuingAt5(t *testing.T) {
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

	// The item should still be in queue (not RequeueItem'd, not Complete'd)
	items := store.QueueItems(orgID)
	if len(items) != 1 {
		t.Errorf("expected 1 item still in queue after cap reached (awaiting TTL), got %d", len(items))
	}
	// Crucially: QueuedAt must NOT have changed (RequeueItem was not called)
	if len(items) == 1 && !items[0].QueuedAt.Equal(originalQueuedAt) {
		t.Errorf("QueuedAt changed despite reaching retry cap — RequeueItem should not be called at RetryCount=5")
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
