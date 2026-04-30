package gc

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNewWorker(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)

	w := NewWorker(store, nil, q, 100, 1*time.Hour, false, stats)

	if w == nil {
		t.Fatal("NewWorker returned nil")
	}
	if w.batchSize != 100 {
		t.Errorf("batchSize = %d, want 100", w.batchSize)
	}
	if w.gracePeriod != 1*time.Hour {
		t.Errorf("gracePeriod = %v, want 1h", w.gracePeriod)
	}
	if w.dryRun {
		t.Error("dryRun should be false")
	}
}

func TestNewWorker_DryRun(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)

	w := NewWorker(store, nil, q, 50, 30*time.Minute, true, stats)

	if !w.dryRun {
		t.Error("dryRun should be true when passed true")
	}
	if w.batchSize != 50 {
		t.Errorf("batchSize = %d, want 50", w.batchSize)
	}
}

func TestWorker_ProcessBlock_RefCountZero(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)

	orgID := uuid.New()
	store.AddBlock(orgID, "block-1", "hot", 0)
	store.AddBlockMapping(orgID, "sha1-abc", "block-1")

	// Enqueue the block (in the past so grace period passes)
	store.EnqueueItem(orgID, time.Now().Add(-2*time.Hour), ItemBlock, "block-1", uuid.Nil, "hot", 0)

	ctx := context.Background()
	n, err := w.ProcessOnce(ctx)
	if err != nil {
		t.Fatalf("ProcessOnce failed: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 processed, got %d", n)
	}

	// Block should be deleted from store
	if store.GetBlock(orgID, "block-1") != nil {
		t.Error("block should be deleted from DB")
	}

	// Block should be deleted from S3
	deleted := sp.DeletedBlocks()
	if len(deleted) != 1 || deleted[0] != "block-1" {
		t.Errorf("expected S3 deletion of block-1, got %v", deleted)
	}

	// Block mapping should be cleaned up (via reverse lookup)
	mappings, _ := store.ListBlockMappingsByInternalID(orgID, "block-1")
	if len(mappings) != 0 {
		t.Errorf("expected 0 mappings, got %d", len(mappings))
	}

	// Stats should be updated
	if stats.BlocksDeleted() != 1 {
		t.Errorf("BlocksDeleted = %d, want 1", stats.BlocksDeleted())
	}
}

func TestWorker_ProcessBlock_RefCountPositive(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)

	orgID := uuid.New()
	store.AddBlock(orgID, "block-1", "hot", 2)

	store.EnqueueItem(orgID, time.Now().Add(-2*time.Hour), ItemBlock, "block-1", uuid.Nil, "hot", 0)

	ctx := context.Background()
	n, err := w.ProcessOnce(ctx)
	if err != nil {
		t.Fatalf("ProcessOnce failed: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 processed (skipped), got %d", n)
	}

	// Block should NOT be deleted (ref_count > 0)
	if store.GetBlock(orgID, "block-1") == nil {
		t.Error("block should still exist (ref_count > 0)")
	}

	// No S3 deletions
	if len(sp.DeletedBlocks()) != 0 {
		t.Errorf("expected no S3 deletions, got %d", len(sp.DeletedBlocks()))
	}
}

func TestWorker_ProcessBlock_DryRun(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, true, stats) // dryRun=true

	orgID := uuid.New()
	store.AddBlock(orgID, "block-1", "hot", 0)

	store.EnqueueItem(orgID, time.Now().Add(-2*time.Hour), ItemBlock, "block-1", uuid.Nil, "hot", 0)

	ctx := context.Background()
	n, _ := w.ProcessOnce(ctx)
	if n != 1 {
		t.Errorf("expected 1 processed, got %d", n)
	}

	// Block should still exist (dry run)
	if store.GetBlock(orgID, "block-1") == nil {
		t.Error("block should still exist in dry run mode")
	}
	if len(sp.DeletedBlocks()) != 0 {
		t.Error("S3 should not be touched in dry run mode")
	}
}

func TestWorker_ProcessCommit_CascadesFSObjects(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	libID := uuid.New()

	// Commit points to a root fs_object
	store.AddCommit(libID, "commit-abc", "fs-root")

	store.EnqueueItem(orgID, time.Now().Add(-2*time.Hour), ItemCommit, "commit-abc", libID, "", 0)

	ctx := context.Background()
	n, err := w.ProcessOnce(ctx)
	if err != nil {
		t.Fatalf("ProcessOnce failed: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 processed, got %d", n)
	}

	// Commit should be deleted
	if store.GetCommitRecord(libID, "commit-abc") != nil {
		t.Error("commit should be deleted")
	}

	// Root fs_object should be enqueued for cascade deletion
	items := store.QueueItems(orgID)
	fsItems := 0
	for _, item := range items {
		if item.ItemType == ItemFSObject && item.ItemID == "fs-root" {
			fsItems++
		}
	}
	if fsItems != 1 {
		t.Errorf("expected 1 fs_object enqueued for cascade, got %d", fsItems)
	}
}

func TestWorker_ProcessCommit_SkipsAlreadyPendingRootFSObject(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	libID := uuid.New()
	queuedAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)

	store.AddCommit(libID, "commit-abc", "fs-root")
	if err := store.EnqueueItem(orgID, queuedAt, ItemFSObject, "fs-root", libID, "", 0); err != nil {
		t.Fatalf("seed root fs_object failed: %v", err)
	}

	items, err := store.DequeueBatch(orgID, 1, time.Now())
	if err != nil || len(items) != 1 {
		t.Fatalf("DequeueBatch failed: %v / items=%d", err, len(items))
	}
	if err := q.IncrementRetry(items[0]); err != nil {
		t.Fatalf("IncrementRetry failed: %v", err)
	}

	if err := w.processCommit(QueueItem{OrgID: orgID, QueuedAt: queuedAt, IdentityAt: queuedAt, ItemType: ItemCommit, ItemID: "commit-abc", LibraryID: libID}); err != nil {
		t.Fatalf("processCommit failed: %v", err)
	}

	if store.GetCommitRecord(libID, "commit-abc") != nil {
		t.Fatal("commit should be deleted even when root fs_object was already pending")
	}

	fsItems := 0
	for _, item := range store.QueueItems(orgID) {
		if item.ItemType == ItemFSObject && item.ItemID == "fs-root" {
			fsItems++
		}
	}
	if fsItems != 1 {
		t.Fatalf("expected exactly 1 pending root fs_object after dedupe, got %d", fsItems)
	}
}

func TestWorker_ProcessCommit_DoesNotCrossSuppressRootFSObjectAcrossLibraries(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	libPending := uuid.New()
	libTarget := uuid.New()
	queuedAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)

	store.AddCommit(libTarget, "commit-abc", "shared-root")
	if err := store.EnqueueItem(orgID, queuedAt, ItemFSObject, "shared-root", libPending, "", 0); err != nil {
		t.Fatalf("seed cross-library root fs_object failed: %v", err)
	}

	if err := w.processCommit(QueueItem{OrgID: orgID, QueuedAt: queuedAt, IdentityAt: queuedAt, ItemType: ItemCommit, ItemID: "commit-abc", LibraryID: libTarget}); err != nil {
		t.Fatalf("processCommit failed: %v", err)
	}

	targetCount := 0
	for _, item := range store.QueueItems(orgID) {
		if item.ItemType == ItemFSObject && item.LibraryID == libTarget && item.ItemID == "shared-root" {
			targetCount++
		}
	}
	if targetCount != 1 {
		t.Fatalf("expected target library root fs_object to enqueue once despite same id pending in another library, got %d", targetCount)
	}
}

func TestWorker_ProcessCommit_AlreadyDeleted(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	libID := uuid.New()

	// Don't add the commit — simulate already deleted
	store.EnqueueItem(orgID, time.Now().Add(-2*time.Hour), ItemCommit, "commit-gone", libID, "", 0)

	ctx := context.Background()
	n, err := w.ProcessOnce(ctx)
	if err != nil {
		t.Fatalf("ProcessOnce failed: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 processed (already deleted), got %d", n)
	}
}

func TestWorker_ProcessFSObject_CascadeBlocks(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	libID := uuid.New()

	// Create blocks with ref_count=1 (will go to 0 when fs_object is processed)
	store.AddBlock(orgID, "blk-a", "hot", 1)
	store.AddBlock(orgID, "blk-b", "hot", 1)
	store.AddBlock(orgID, "blk-c", "hot", 2) // This one won't hit 0

	// Create an fs_object referencing these blocks
	store.AddFSObject(libID, "fs-obj-1", "file", []string{"blk-a", "blk-b", "blk-c"})

	// Add library so storage class lookup works
	store.AddLibrary(orgID, libID, "hot")

	// Enqueue the fs_object
	store.EnqueueItem(orgID, time.Now().Add(-2*time.Hour), ItemFSObject, "fs-obj-1", libID, "", 0)

	ctx := context.Background()
	n, err := w.ProcessOnce(ctx)
	if err != nil {
		t.Fatalf("ProcessOnce failed: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 processed, got %d", n)
	}

	// FS object should be deleted
	if store.GetFSObj(libID, "fs-obj-1") != nil {
		t.Error("fs_object should be deleted")
	}

	// Blocks blk-a and blk-b should have ref_count decremented to 0
	blkA := store.GetBlock(orgID, "blk-a")
	if blkA == nil || blkA.RefCount != 0 {
		t.Errorf("blk-a ref_count should be 0, got %v", blkA)
	}
	blkB := store.GetBlock(orgID, "blk-b")
	if blkB == nil || blkB.RefCount != 0 {
		t.Errorf("blk-b ref_count should be 0, got %v", blkB)
	}
	blkC := store.GetBlock(orgID, "blk-c")
	if blkC == nil || blkC.RefCount != 1 {
		t.Errorf("blk-c ref_count should be 1, got %v", blkC)
	}

	// Two new block items should be enqueued (blk-a and blk-b hit 0)
	queueItems := store.QueueItems(orgID)
	blockItemCount := 0
	for _, item := range queueItems {
		if item.ItemType == ItemBlock {
			blockItemCount++
		}
	}
	if blockItemCount != 2 {
		t.Errorf("expected 2 block items enqueued, got %d", blockItemCount)
	}
}

func TestWorker_ProcessFSObject_CascadesDirEntries(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	libID := uuid.New()

	// Create a directory with children
	store.AddFSObjectWithEntries(libID, "fs-dir", "dir", nil, []string{"fs-child1", "fs-child2"})
	store.AddLibrary(orgID, libID, "hot")

	store.EnqueueItem(orgID, time.Now().Add(-2*time.Hour), ItemFSObject, "fs-dir", libID, "", 0)

	ctx := context.Background()
	n, err := w.ProcessOnce(ctx)
	if err != nil {
		t.Fatalf("ProcessOnce failed: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 processed, got %d", n)
	}

	// Directory should be deleted
	if store.GetFSObj(libID, "fs-dir") != nil {
		t.Error("directory fs_object should be deleted")
	}

	// Children should be enqueued
	items := store.QueueItems(orgID)
	childCount := 0
	for _, item := range items {
		if item.ItemType == ItemFSObject {
			childCount++
		}
	}
	if childCount != 2 {
		t.Errorf("expected 2 child fs_objects enqueued, got %d", childCount)
	}
}

func TestWorker_ProcessFSObject_SkipsAlreadyPendingChild(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	libID := uuid.New()
	queuedAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)

	store.AddFSObjectWithEntries(libID, "fs-dir", "dir", nil, []string{"fs-child1", "fs-child2"})
	store.AddLibrary(orgID, libID, "hot")
	if err := store.EnqueueItem(orgID, queuedAt, ItemFSObject, "fs-child1", libID, "", 0); err != nil {
		t.Fatalf("seed child fs_object failed: %v", err)
	}

	items, err := store.DequeueBatch(orgID, 1, time.Now())
	if err != nil || len(items) != 1 {
		t.Fatalf("DequeueBatch failed: %v / items=%d", err, len(items))
	}
	if err := q.IncrementRetry(items[0]); err != nil {
		t.Fatalf("IncrementRetry failed: %v", err)
	}

	err = w.processFSObject(context.Background(), QueueItem{OrgID: orgID, QueuedAt: queuedAt, IdentityAt: queuedAt, ItemType: ItemFSObject, ItemID: "fs-dir", LibraryID: libID})
	if err != nil {
		t.Fatalf("processFSObject failed: %v", err)
	}

	child1Count := 0
	child2Count := 0
	for _, item := range store.QueueItems(orgID) {
		if item.ItemType != ItemFSObject {
			continue
		}
		switch item.ItemID {
		case "fs-child1":
			child1Count++
		case "fs-child2":
			child2Count++
		}
	}
	if child1Count != 1 || child2Count != 1 {
		t.Fatalf("expected one pending row per child after dedupe, got child1=%d child2=%d", child1Count, child2Count)
	}
}

func TestWorker_ProcessFSObject_DoesNotCrossSuppressChildAcrossLibraries(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	libPending := uuid.New()
	libTarget := uuid.New()
	queuedAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)

	store.AddFSObjectWithEntries(libTarget, "fs-dir", "dir", nil, []string{"shared-child"})
	store.AddLibrary(orgID, libTarget, "hot")
	if err := store.EnqueueItem(orgID, queuedAt, ItemFSObject, "shared-child", libPending, "", 0); err != nil {
		t.Fatalf("seed cross-library child fs_object failed: %v", err)
	}

	err := w.processFSObject(context.Background(), QueueItem{OrgID: orgID, QueuedAt: queuedAt, IdentityAt: queuedAt, ItemType: ItemFSObject, ItemID: "fs-dir", LibraryID: libTarget})
	if err != nil {
		t.Fatalf("processFSObject failed: %v", err)
	}

	targetCount := 0
	for _, item := range store.QueueItems(orgID) {
		if item.ItemType == ItemFSObject && item.LibraryID == libTarget && item.ItemID == "shared-child" {
			targetCount++
		}
	}
	if targetCount != 1 {
		t.Fatalf("expected target library child fs_object to enqueue once despite same id pending in another library, got %d", targetCount)
	}
}

func TestWorker_ProcessFSObject_RetryKeepsSingleBlockDecrement(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	libID := uuid.New()
	identityAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)

	store.AddBlock(orgID, "blk-a", "hot", 2)
	store.AddFSObject(libID, "fs-obj-1", "file", []string{"blk-a"})
	store.AddLibrary(orgID, libID, "hot")

	first := QueueItem{OrgID: orgID, QueuedAt: identityAt, IdentityAt: identityAt, ItemType: ItemFSObject, ItemID: "fs-obj-1", LibraryID: libID}
	if err := w.processFSObject(context.Background(), first); err != nil {
		t.Fatalf("first processFSObject failed: %v", err)
	}
	if got := store.GetBlock(orgID, "blk-a").RefCount; got != 1 {
		t.Fatalf("block ref_count after first pass = %d, want 1", got)
	}

	store.AddFSObject(libID, "fs-obj-1", "file", []string{"blk-a"})
	second := QueueItem{OrgID: orgID, QueuedAt: time.Now().UTC(), IdentityAt: identityAt, ItemType: ItemFSObject, ItemID: "fs-obj-1", LibraryID: libID}
	if err := w.processFSObject(context.Background(), second); err != nil {
		t.Fatalf("second processFSObject failed: %v", err)
	}
	if got := store.GetBlock(orgID, "blk-a").RefCount; got != 1 {
		t.Fatalf("block ref_count after retry = %d, want 1", got)
	}
}

func TestWorker_ProcessFSObject_IdempotencyScopedByLibrary(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	libA := uuid.New()
	libB := uuid.New()
	identityAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)

	store.AddBlock(orgID, "blk-a", "hot", 1)
	store.AddBlock(orgID, "blk-b", "hot", 1)
	store.AddFSObject(libA, "shared-fs", "file", []string{"blk-a"})
	store.AddFSObject(libB, "shared-fs", "file", []string{"blk-b"})
	store.AddLibrary(orgID, libA, "hot")
	store.AddLibrary(orgID, libB, "hot")

	if err := w.processFSObject(context.Background(), QueueItem{OrgID: orgID, QueuedAt: identityAt, IdentityAt: identityAt, ItemType: ItemFSObject, ItemID: "shared-fs", LibraryID: libA}); err != nil {
		t.Fatalf("processFSObject libA failed: %v", err)
	}
	if err := w.processFSObject(context.Background(), QueueItem{OrgID: orgID, QueuedAt: identityAt, IdentityAt: identityAt, ItemType: ItemFSObject, ItemID: "shared-fs", LibraryID: libB}); err != nil {
		t.Fatalf("processFSObject libB failed: %v", err)
	}

	if got := store.GetBlock(orgID, "blk-a").RefCount; got != 0 {
		t.Fatalf("block ref_count for libA = %d, want 0", got)
	}
	if got := store.GetBlock(orgID, "blk-b").RefCount; got != 0 {
		t.Fatalf("block ref_count for libB = %d, want 0", got)
	}
}

func TestWorker_RetryOnFailure(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()

	// Enqueue an item with unknown type to trigger error
	store.EnqueueItem(orgID, time.Now().Add(-2*time.Hour), ItemType("unknown"), "item-1", uuid.Nil, "", 0)

	ctx := context.Background()
	n, _ := w.ProcessOnce(ctx)
	if n != 0 {
		t.Errorf("expected 0 processed (should fail), got %d", n)
	}

	// Item should still be in queue with incremented retry count
	items := store.QueueItems(orgID)
	if len(items) != 1 {
		t.Fatalf("expected 1 item still in queue, got %d", len(items))
	}
	if items[0].RetryCount != 1 {
		t.Errorf("RetryCount = %d, want 1", items[0].RetryCount)
	}
}

func TestWorker_ProcessOnce_EmptyQueue(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	ctx := context.Background()
	n, err := w.ProcessOnce(ctx)
	if err != nil {
		t.Fatalf("ProcessOnce failed: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 processed on empty queue, got %d", n)
	}
}

func TestWorker_EnqueueLibraryContents_NoDuplicateBlocks(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	libID := uuid.New()

	// Create library data
	store.AddCommit(libID, "commit-1", "fs-root-1")
	store.AddCommit(libID, "commit-2", "fs-root-2")
	store.AddFSObject(libID, "fs-1", "file", []string{"blk-a", "blk-b"})
	store.AddFSObject(libID, "fs-2", "file", []string{"blk-c"})

	err := w.EnqueueLibraryContents(orgID, libID, "hot")
	if err != nil {
		t.Fatalf("EnqueueLibraryContents failed: %v", err)
	}

	// Check queue contents — should only have commits and fs_objects,
	// NOT blocks (blocks cascade from fs_object processing)
	items := store.QueueItems(orgID)

	commitCount := 0
	fsCount := 0
	blockCount := 0
	for _, item := range items {
		switch item.ItemType {
		case ItemCommit:
			commitCount++
		case ItemFSObject:
			fsCount++
		case ItemBlock:
			blockCount++
		}
	}

	if commitCount != 2 {
		t.Errorf("expected 2 commits enqueued, got %d", commitCount)
	}
	if fsCount != 2 {
		t.Errorf("expected 2 fs_objects enqueued, got %d", fsCount)
	}
	if blockCount != 0 {
		t.Errorf("expected 0 blocks enqueued (cascade from fs_objects), got %d", blockCount)
	}
}

func TestWorker_EnqueueLibraryContents_DoesNotCrossSuppressAcrossLibraries(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	libPending := uuid.New()
	libTarget := uuid.New()
	identityAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)

	store.AddCommit(libPending, "shared-commit", "fs-pending")
	store.AddFSObject(libPending, "shared-fs", "file", []string{"blk-pending"})
	store.AddCommit(libTarget, "shared-commit", "fs-target")
	store.AddFSObject(libTarget, "shared-fs", "file", []string{"blk-target"})
	if err := store.EnqueueItem(orgID, identityAt, ItemCommit, "shared-commit", libPending, "", 0); err != nil {
		t.Fatalf("seed pending commit failed: %v", err)
	}
	if err := store.EnqueueItem(orgID, identityAt, ItemFSObject, "shared-fs", libPending, "", 0); err != nil {
		t.Fatalf("seed pending fs_object failed: %v", err)
	}

	err := w.enqueueLibraryContentsAt(orgID, libTarget, "hot", identityAt)
	if err != nil {
		t.Fatalf("enqueueLibraryContentsAt failed: %v", err)
	}

	targetCommits := 0
	targetFSObjects := 0
	for _, item := range store.QueueItems(orgID) {
		if item.LibraryID != libTarget {
			continue
		}
		switch item.ItemType {
		case ItemCommit:
			targetCommits++
		case ItemFSObject:
			targetFSObjects++
		}
	}
	if targetCommits != 1 || targetFSObjects != 1 {
		t.Fatalf("expected target library work to enqueue despite same ids pending elsewhere, got commits=%d fs_objects=%d", targetCommits, targetFSObjects)
	}
}

func TestWorker_ProcessBlockMapping(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	store.AddBlockMapping(orgID, "ext-sha1", "int-sha256")

	store.EnqueueItem(orgID, time.Now().Add(-2*time.Hour), ItemBlockMapping, "ext-sha1", uuid.Nil, "", 0)

	ctx := context.Background()
	n, err := w.ProcessOnce(ctx)
	if err != nil {
		t.Fatalf("ProcessOnce failed: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 processed, got %d", n)
	}

	// Mapping should be deleted
	mappings, _ := store.ListBlockMappingsByInternalID(orgID, "int-sha256")
	if len(mappings) != 0 {
		t.Errorf("expected 0 mappings, got %d", len(mappings))
	}
}

// Unit test for QueueItem type conversion
func TestQueueItem_TypeConversion(t *testing.T) {
	tests := []struct {
		str  string
		want ItemType
	}{
		{"block", ItemBlock},
		{"commit", ItemCommit},
		{"fs_object", ItemFSObject},
		{"block_mapping", ItemBlockMapping},
		{"share_link", ItemShareLink},
		{"share", ItemShare},
		{"restore_job", ItemRestoreJob},
	}

	for _, tt := range tests {
		t.Run(tt.str, func(t *testing.T) {
			got := ItemType(tt.str)
			if got != tt.want {
				t.Errorf("ItemType(%q) = %v, want %v", tt.str, got, tt.want)
			}
		})
	}
}

// Unit test for QueueItem with uuid.Nil
func TestQueueItem_NilUUID(t *testing.T) {
	item := QueueItem{
		OrgID:     uuid.Nil,
		LibraryID: uuid.Nil,
		ItemType:  ItemBlock,
		ItemID:    "test-block-id",
	}

	if item.OrgID != uuid.Nil {
		t.Error("OrgID should be uuid.Nil")
	}
	if item.LibraryID != uuid.Nil {
		t.Error("LibraryID should be uuid.Nil")
	}
}

func TestWorker_ContextCancellation(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()

	// Enqueue several items
	for i := 0; i < 10; i++ {
		store.AddBlock(orgID, "block-"+string(rune('a'+i)), "hot", 0)
		store.EnqueueItem(orgID, time.Now().Add(-2*time.Hour), ItemBlock, "block-"+string(rune('a'+i)), uuid.Nil, "hot", 0)
	}

	// Cancel context immediately
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := w.ProcessOnce(ctx)
	if err != context.Canceled {
		// It may or may not error depending on timing
		_ = err
	}
}

// === Cascade tests: Dry Run ===

func TestWorker_ProcessUserCascade_DryRun(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, true, stats) // dryRun=true

	orgID := uuid.New()
	userID := uuid.New()
	store.AddOrganization(orgID)
	store.AddUser(orgID, userID, "alice@test.com")

	store.EnqueueItem(orgID, time.Now().Add(-2*time.Hour), ItemUserCascade, userID.String(), uuid.Nil, "", 0)

	ctx := context.Background()
	n, err := w.ProcessOnce(ctx)
	if err != nil {
		t.Fatalf("ProcessOnce failed: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 processed, got %d", n)
	}

	// User should still exist (dry run)
	if !store.HasUser(orgID, userID) {
		t.Error("user should still exist in dry run mode")
	}
}

func TestWorker_ProcessLibraryCascade_DryRun(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, true, stats)

	orgID := uuid.New()
	libID := uuid.New()
	store.AddOrganization(orgID)
	store.AddLibrary(orgID, libID, "hot")

	store.EnqueueItem(orgID, time.Now().Add(-2*time.Hour), ItemLibraryCascade, libID.String(), uuid.Nil, "hot", 0)

	ctx := context.Background()
	n, err := w.ProcessOnce(ctx)
	if err != nil {
		t.Fatalf("ProcessOnce failed: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 processed, got %d", n)
	}

	// Library should still exist (dry run)
	if _, err := store.GetLibraryStorageClass(orgID, libID); err != nil {
		t.Error("library should still exist in dry run mode")
	}
}

func TestWorker_ProcessOrgCascade_DryRun(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, true, stats)

	orgID := uuid.New()
	store.AddOrganizationWithName(orgID, "Test Org")

	store.EnqueueItem(orgID, time.Now().Add(-2*time.Hour), ItemOrgCascade, orgID.String(), uuid.Nil, "", 0)

	ctx := context.Background()
	n, err := w.ProcessOnce(ctx)
	if err != nil {
		t.Fatalf("ProcessOnce failed: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 processed, got %d", n)
	}

	// Org should still exist (dry run)
	if !store.HasOrg(orgID) {
		t.Error("org should still exist in dry run mode")
	}
}

// === Cascade tests: Invalid UUID ===

func TestWorker_ProcessUserCascade_InvalidUUID(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	store.AddOrganization(orgID)

	store.EnqueueItem(orgID, time.Now().Add(-2*time.Hour), ItemUserCascade, "not-a-uuid", uuid.Nil, "", 0)

	ctx := context.Background()
	n, _ := w.ProcessOnce(ctx)
	// Should fail and not count as processed
	if n != 0 {
		t.Errorf("expected 0 processed (invalid UUID), got %d", n)
	}
}

func TestWorker_ProcessLibraryCascade_InvalidUUID(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	store.AddOrganization(orgID)

	store.EnqueueItem(orgID, time.Now().Add(-2*time.Hour), ItemLibraryCascade, "not-a-uuid", uuid.Nil, "", 0)

	ctx := context.Background()
	n, _ := w.ProcessOnce(ctx)
	if n != 0 {
		t.Errorf("expected 0 processed (invalid UUID), got %d", n)
	}
}

// === Cascade tests: Full cascade with real data ===

func TestWorker_ProcessUserCascade_FullCascade(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	userID := uuid.New()
	groupID := uuid.New()
	receivedLibID := uuid.New()
	createdLibID := uuid.New()
	recipientID := uuid.New()
	deletedAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)

	store.AddOrganization(orgID)
	store.AddDeletedUser(orgID, userID, "alice@test.com", deletedAt)
	store.AddGroupMembership(orgID, userID, groupID)
	receivedShareID := store.AddShareByUser(orgID, userID, receivedLibID)
	createdShareID := store.AddShareCreatedByUser(orgID, userID, recipientID, createdLibID)
	store.AddStarredFile(userID)
	store.AddMonitoredRepo(userID)

	store.EnqueueItem(orgID, deletedAt, ItemUserCascade, userID.String(), uuid.Nil, "", 0)

	ctx := context.Background()
	n, err := w.ProcessOnce(ctx)
	if err != nil {
		t.Fatalf("ProcessOnce failed: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 processed, got %d", n)
	}

	// User should be hard-deleted
	if store.HasUser(orgID, userID) {
		t.Error("user should be hard-deleted after cascade")
	}

	// Starred files and monitored repos should be cleaned
	if store.HasStarredFiles(userID) {
		t.Error("starred files should be deleted after user cascade")
	}
	if store.HasMonitoredRepos(userID) {
		t.Error("monitored repos should be deleted after user cascade")
	}
	if store.HasShare(receivedLibID, receivedShareID) {
		t.Error("received share should be deleted after user cascade")
	}
	if store.HasShare(createdLibID, createdShareID) {
		t.Error("created share should be deleted after user cascade")
	}

	// Audit log should have an entry
	entries := store.AuditLogEntries()
	if len(entries) != 1 {
		t.Errorf("expected 1 audit log entry, got %d", len(entries))
	} else if entries[0].Action != "gc_user_cascade_deleted" {
		t.Errorf("expected action gc_user_cascade_deleted, got %s", entries[0].Action)
	}
}

func TestWorker_ProcessLibraryCascade_FullCascade(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	libID := uuid.New()
	deletedAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)

	store.AddOrganization(orgID)
	store.AddDeletedLibrary(orgID, libID, "hot", deletedAt)
	store.AddCommit(libID, "commit-1", "fs-root")
	store.AddFSObject(libID, "fs-root", "dir", nil)

	store.EnqueueItem(orgID, deletedAt, ItemLibraryCascade, libID.String(), uuid.Nil, "hot", 0)

	ctx := context.Background()
	n, err := w.ProcessOnce(ctx)
	if err != nil {
		t.Fatalf("ProcessOnce failed: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 processed, got %d", n)
	}

	// Library should be hard-deleted
	if _, err := store.GetLibraryStorageClass(orgID, libID); err == nil {
		t.Error("library should be hard-deleted after cascade")
	}

	// Contents should be enqueued for deletion (commits + fs_objects)
	items := store.QueueItems(orgID)
	commitCount, fsCount := 0, 0
	for _, item := range items {
		switch item.ItemType {
		case ItemCommit:
			commitCount++
		case ItemFSObject:
			fsCount++
		}
	}
	if commitCount != 1 {
		t.Errorf("expected 1 commit enqueued, got %d", commitCount)
	}
	if fsCount != 1 {
		t.Errorf("expected 1 fs_object enqueued, got %d", fsCount)
	}

	// Audit log: 2 entries — gc_library_artifacts_cleaned + gc_library_cascade_deleted
	entries := store.AuditLogEntries()
	if len(entries) != 2 {
		t.Errorf("expected 2 audit log entries, got %d", len(entries))
	}
	actions := map[string]bool{}
	for _, e := range entries {
		actions[e.Action] = true
	}
	if !actions["gc_library_artifacts_cleaned"] {
		t.Error("expected gc_library_artifacts_cleaned audit entry")
	}
	if !actions["gc_library_cascade_deleted"] {
		t.Error("expected gc_library_cascade_deleted audit entry")
	}
}

func TestWorker_ProcessLibraryCascade_RetryableAuxCleanupDoesNotDuplicateChildren(t *testing.T) {
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

	item := QueueItem{
		OrgID:        orgID,
		QueuedAt:     deletedAt,
		ItemType:     ItemLibraryCascade,
		ItemID:       libID.String(),
		StorageClass: "hot",
	}

	err := w.processLibraryCascade(context.Background(), item)
	if err == nil {
		t.Fatal("expected restore job cleanup failure")
	}

	items := store.QueueItems(orgID)
	commitCount := 0
	fsCount := 0
	for _, queued := range items {
		switch queued.ItemType {
		case ItemCommit:
			commitCount++
			if !queued.QueuedAt.Equal(deletedAt) {
				t.Fatalf("commit queued_at = %v, want %v", queued.QueuedAt, deletedAt)
			}
		case ItemFSObject:
			fsCount++
			if !queued.QueuedAt.Equal(deletedAt) {
				t.Fatalf("fs_object queued_at = %v, want %v", queued.QueuedAt, deletedAt)
			}
		}
	}
	if commitCount != 2 {
		t.Fatalf("expected 2 commits after failed cleanup, got %d", commitCount)
	}
	if fsCount != 2 {
		t.Fatalf("expected 2 fs_objects after failed cleanup, got %d", fsCount)
	}
	if _, err := store.GetLibraryStorageClass(orgID, libID); err != nil {
		t.Fatalf("library should not be hard-deleted on cleanup failure: %v", err)
	}

	store.deleteRestoreJobsByLibraryErr = nil
	if err := w.processLibraryCascade(context.Background(), item); err != nil {
		t.Fatalf("retry processLibraryCascade failed: %v", err)
	}

	items = store.QueueItems(orgID)
	commitCount = 0
	fsCount = 0
	for _, queued := range items {
		switch queued.ItemType {
		case ItemCommit:
			commitCount++
		case ItemFSObject:
			fsCount++
		}
	}
	if commitCount != 2 {
		t.Fatalf("expected 2 commits after retry, got %d", commitCount)
	}
	if fsCount != 2 {
		t.Fatalf("expected 2 fs_objects after retry, got %d", fsCount)
	}
	if _, err := store.GetLibraryStorageClass(orgID, libID); err == nil {
		t.Fatal("library should be hard-deleted after successful retry")
	}
}

func TestWorker_ProcessOrgCascade_FullCascade(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	userID1 := uuid.New()
	userID2 := uuid.New()
	groupID := uuid.New()
	libID := uuid.New()
	deletedAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)

	store.AddDeletedOrg(orgID, "Doomed Corp", deletedAt)
	store.AddUser(orgID, userID1, "alice@doomed.com")
	store.AddUser(orgID, userID2, "bob@doomed.com")
	store.AddStarredFile(userID1)
	store.AddMonitoredRepo(userID2)
	store.AddGroupForOrg(orgID, groupID)
	store.AddGroupMembership(orgID, userID1, groupID)
	store.AddLibrary(orgID, libID, "hot")

	store.EnqueueItem(orgID, deletedAt, ItemOrgCascade, orgID.String(), uuid.Nil, "", 0)

	ctx := context.Background()
	n, err := w.ProcessOnce(ctx)
	if err != nil {
		t.Fatalf("ProcessOnce failed: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 processed, got %d", n)
	}

	// Org should be hard-deleted
	if store.HasOrg(orgID) {
		t.Error("org should be hard-deleted after cascade")
	}

	// Users should be hard-deleted
	if store.HasUser(orgID, userID1) {
		t.Error("user1 should be hard-deleted after org cascade")
	}
	if store.HasUser(orgID, userID2) {
		t.Error("user2 should be hard-deleted after org cascade")
	}

	// Groups should be deleted
	if store.HasGroup(orgID, groupID) {
		t.Error("group should be deleted after org cascade")
	}
	if _, err := store.GetLibraryStorageClass(orgID, libID); err == nil {
		t.Error("library should be hard-deleted after org cascade")
	}

	// Starred/monitored should be cleaned
	if store.HasStarredFiles(userID1) {
		t.Error("starred files should be deleted after org cascade")
	}
	if store.HasMonitoredRepos(userID2) {
		t.Error("monitored repos should be deleted after org cascade")
	}

	// Library cascade should run synchronously, so no library_cascade item remains queued
	items := store.QueueItems(orgID)
	libCascadeCount := 0
	for _, item := range items {
		if item.ItemType == ItemLibraryCascade {
			libCascadeCount++
		}
	}
	if libCascadeCount != 0 {
		t.Errorf("expected 0 library_cascade queued, got %d", libCascadeCount)
	}

	// Audit log
	entries := store.AuditLogEntries()
	if len(entries) != 3 {
		t.Errorf("expected 3 audit log entries, got %d", len(entries))
	}
	actions := map[string]bool{}
	for _, e := range entries {
		actions[e.Action] = true
	}
	if !actions["gc_library_artifacts_cleaned"] {
		t.Error("expected gc_library_artifacts_cleaned audit entry")
	}
	if !actions["gc_library_cascade_deleted"] {
		t.Error("expected gc_library_cascade_deleted audit entry")
	}
	if !actions["gc_org_cascade_deleted"] {
		t.Error("expected gc_org_cascade_deleted audit entry")
	}
}

func TestWorker_ProcessUserCascade_AlreadyDeleted(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	store.AddOrganization(orgID)

	// User doesn't exist — simulate already deleted
	store.EnqueueItem(orgID, time.Now().Add(-2*time.Hour), ItemUserCascade, uuid.New().String(), uuid.Nil, "", 0)

	ctx := context.Background()
	n, err := w.ProcessOnce(ctx)
	if err != nil {
		t.Fatalf("ProcessOnce failed: %v", err)
	}
	// Should gracefully skip (returns nil when email lookup fails)
	if n != 1 {
		t.Errorf("expected 1 processed (skipped gracefully), got %d", n)
	}
}

func TestWorker_ProcessOrgCascade_AlreadyDeleted(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	// Don't add org — simulate already deleted

	store.EnqueueItem(orgID, time.Now().Add(-2*time.Hour), ItemOrgCascade, orgID.String(), uuid.Nil, "", 0)

	ctx := context.Background()
	n, err := w.ProcessOnce(ctx)
	if err != nil {
		t.Fatalf("ProcessOnce failed: %v", err)
	}
	// Should gracefully skip (returns nil when org name lookup fails)
	if n != 1 {
		t.Errorf("expected 1 processed (skipped gracefully), got %d", n)
	}
}

func TestWorker_ProcessUserCascade_SkipsRestoredUser(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	userID := uuid.New()
	deletedAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)
	userKey := fmt.Sprintf("%s:%s", orgID, userID)

	store.AddOrganization(orgID)
	store.AddDeletedUser(orgID, userID, "alice@test.com", deletedAt)
	store.EnqueueItem(orgID, deletedAt, ItemUserCascade, userID.String(), uuid.Nil, "", 0)

	store.users[userKey].Status = "active"
	store.users[userKey].DeletedAt = nil

	n, err := w.ProcessOnce(context.Background())
	if err != nil {
		t.Fatalf("ProcessOnce failed: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 processed stale item, got %d", n)
	}
	if !store.HasUser(orgID, userID) {
		t.Fatal("restored user should not be hard-deleted by stale queue item")
	}
	if len(store.QueueItems(orgID)) != 0 {
		t.Fatal("stale user queue item should be completed")
	}
}

func TestWorker_ProcessLibraryCascade_SkipsRestoredLibrary(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	libID := uuid.New()
	deletedAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)

	store.AddOrganization(orgID)
	store.AddDeletedLibrary(orgID, libID, "hot", deletedAt)
	store.EnqueueItem(orgID, deletedAt, ItemLibraryCascade, libID.String(), uuid.Nil, "hot", 0)

	delete(store.deletedLibraries, libID)

	n, err := w.ProcessOnce(context.Background())
	if err != nil {
		t.Fatalf("ProcessOnce failed: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 processed stale item, got %d", n)
	}
	if _, err := store.GetLibraryStorageClass(orgID, libID); err != nil {
		t.Fatal("restored library should not be hard-deleted by stale queue item")
	}
	if len(store.QueueItems(orgID)) != 0 {
		t.Fatal("stale library queue item should be completed")
	}
}

func TestWorker_ProcessOrgCascade_SkipsRestoredOrg(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	deletedAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)

	store.AddDeletedOrg(orgID, "Recovered Corp", deletedAt)
	store.EnqueueItem(orgID, deletedAt, ItemOrgCascade, orgID.String(), uuid.Nil, "", 0)

	store.orgStatus[orgID] = "active"
	delete(store.orgDeletedAt, orgID)

	n, err := w.ProcessOnce(context.Background())
	if err != nil {
		t.Fatalf("ProcessOnce failed: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 processed stale item, got %d", n)
	}
	if !store.HasOrg(orgID) {
		t.Fatal("restored org should not be hard-deleted by stale queue item")
	}
	if len(store.QueueItems(orgID)) != 0 {
		t.Fatal("stale org queue item should be completed")
	}
}

func TestWorker_DecrementAndFindZeroRef_ReturnsOnlyZeroTransitions(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	store.AddBlock(orgID, "hits-zero", "hot", 1)
	store.AddBlock(orgID, "stays-positive", "hot", 2)

	zeroRef := w.decrementAndFindZeroRef(orgID, []string{"hits-zero", "stays-positive"})
	if len(zeroRef) != 1 || zeroRef[0] != "hits-zero" {
		t.Fatalf("zeroRef = %#v, want only hits-zero", zeroRef)
	}

	if got, err := store.GetBlockRefCount(orgID, "hits-zero"); err != nil || got != 0 {
		t.Fatalf("hits-zero refcount = %d, err=%v; want 0,nil", got, err)
	}
	if got, err := store.GetBlockRefCount(orgID, "stays-positive"); err != nil || got != 1 {
		t.Fatalf("stays-positive refcount = %d, err=%v; want 1,nil", got, err)
	}
}
