package gc

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
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

	// Forward block mapping should be cleaned up (resolved via blocks.sha1)
	if store.ForwardBlockMappingExists(orgID, "sha1-abc") {
		t.Error("expected forward mapping sha1-abc cleaned up via blocks.sha1")
	}

	// Stats should be updated
	if stats.BlocksDeleted() != 1 {
		t.Errorf("BlocksDeleted = %d, want 1", stats.BlocksDeleted())
	}
}

// TestWorker_ProcessBlock_EmptyBlockSHA1LeavesForwardMappingObservable pins the
// PR7 fail-safe: when a deleted block has no blocks.sha1 (a legacy/pre-PR2 row),
// GC cannot resolve its forward block_id_mappings row without the dropped reverse
// index, so it must NOT delete a mapping blindly. The mapping survives as a
// harmless dangling pointer (recorded via the gc_block_mapping_sha1_missing
// metric), and the block itself is still deleted from DB + S3.
func TestWorker_ProcessBlock_EmptyBlockSHA1LeavesForwardMappingObservable(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)

	orgID := uuid.New()
	// Seed the forward mapping BEFORE the block exists, so the block row carries an
	// empty blocks.sha1 (the legacy shape this fail-safe guards).
	store.AddBlockMapping(orgID, "sha1-orphan", "block-nosha1")
	store.AddBlock(orgID, "block-nosha1", "hot", 0)
	store.EnqueueItem(orgID, time.Now().Add(-2*time.Hour), ItemBlock, "block-nosha1", uuid.Nil, "hot", 0)

	n, err := w.ProcessOnce(context.Background())
	if err != nil {
		t.Fatalf("ProcessOnce failed: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 processed, got %d", n)
	}
	if store.GetBlock(orgID, "block-nosha1") != nil {
		t.Error("block should be deleted from DB even when blocks.sha1 is empty")
	}
	if !store.ForwardBlockMappingExists(orgID, "sha1-orphan") {
		t.Error("forward mapping must survive when blocks.sha1 is empty (fail-safe, not a blind delete)")
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

func TestWorker_ProcessBlock_RetryUsesIdentityAtForCandidateCleanup(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)

	orgID := uuid.New()
	candidateAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)
	store.AddBlock(orgID, "block-retry-cleanup", "hot", 0)
	if _, err := store.EnsureBlockGCCandidate(orgID, "block-retry-cleanup", "hot", candidateAt); err != nil {
		t.Fatalf("EnsureBlockGCCandidate failed: %v", err)
	}
	if err := store.EnqueueItem(orgID, candidateAt, ItemBlock, "block-retry-cleanup", uuid.Nil, "hot", 0); err != nil {
		t.Fatalf("EnqueueItem failed: %v", err)
	}

	items, err := store.DequeueBatch(orgID, 1, time.Now())
	if err != nil || len(items) != 1 {
		t.Fatalf("DequeueBatch failed: %v / items=%d", err, len(items))
	}
	if err := q.IncrementRetry(items[0]); err != nil {
		t.Fatalf("IncrementRetry failed: %v", err)
	}

	retriedItems := store.QueueItems(orgID)
	if len(retriedItems) != 1 {
		t.Fatalf("expected exactly 1 retried queue item, got %d", len(retriedItems))
	}
	if !effectiveIdentityAt(retriedItems[0].QueuedAt, retriedItems[0].IdentityAt).Equal(candidateAt) {
		t.Fatalf("effective identity_at = %v, want %v", effectiveIdentityAt(retriedItems[0].QueuedAt, retriedItems[0].IdentityAt), candidateAt)
	}
	if retriedItems[0].QueuedAt.Equal(candidateAt) {
		t.Fatal("expected retry queued_at to differ from original candidate_at")
	}

	n, err := w.ProcessOnce(context.Background())
	if err != nil {
		t.Fatalf("ProcessOnce failed: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 processed, got %d", n)
	}
	if got := len(store.AllBlockGCCandidates()); got != 0 {
		t.Fatalf("expected canonical block GC candidate cleanup, got %d rows", got)
	}
	candidates, err := store.ListBlockGCCandidatesByDay(candidateAt, db.GCDiscoveryBucket(orgID.String(), "block-retry-cleanup"))
	if err != nil {
		t.Fatalf("ListBlockGCCandidatesByDay failed: %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("expected discovery row cleanup via identity_at, got %d rows", len(candidates))
	}
}

func TestWorker_EnqueueZeroRefBlocks_RecordsProjectionDegradationMetric(t *testing.T) {
	store := NewMockStore()
	store.ensureBlockGCCandidateErrAfterMutate = errors.New("repair projection failed")
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)
	orgID := uuid.New()
	beforeDegraded := testutil.ToFloat64(metrics.GCBlockCandidateDiscoveryDegradedTotal.WithLabelValues("worker"))

	if err := w.enqueueZeroRefBlocks(orgID, uuid.Nil, []string{"block-worker-degraded"}, "hot"); err != nil {
		t.Fatalf("enqueueZeroRefBlocks returned error despite usable queue protection: %v", err)
	}
	afterDegraded := testutil.ToFloat64(metrics.GCBlockCandidateDiscoveryDegradedTotal.WithLabelValues("worker"))
	if afterDegraded-beforeDegraded != 1 {
		t.Fatalf("worker degraded metric delta = %v, want 1", afterDegraded-beforeDegraded)
	}
	items := store.QueueItems(orgID)
	if len(items) != 1 {
		t.Fatalf("expected 1 queued block item, got %d", len(items))
	}
	if items[0].ItemID != "block-worker-degraded" || items[0].ItemType != ItemBlock {
		t.Fatalf("unexpected queued item: %+v", items[0])
	}
}

func TestWorker_ProcessBlock_RefCountZeroButLiveFSObjectReferenceSkipsDelete(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)

	orgID := uuid.New()
	libraryID := uuid.New()
	store.AddLibrary(orgID, libraryID, "hot")
	store.AddBlock(orgID, "partial-zero", "hot", 0)
	store.AddFSObjectReferenceForTest(orgID, "partial-zero", libraryID, "fs-partial")
	store.AddBlock(orgID, "not-yet-decremented", "hot", 0)
	store.AddFSObjectReferenceForTest(orgID, "not-yet-decremented", libraryID, "fs-partial")
	store.AddFSObject(libraryID, "fs-partial", "file", []string{"partial-zero", "not-yet-decremented"})
	store.EnqueueItem(orgID, time.Now().Add(-2*time.Hour), ItemBlock, "partial-zero", uuid.Nil, "hot", 0)

	n, err := w.ProcessOnce(context.Background())
	if err != nil {
		t.Fatalf("ProcessOnce failed: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 processed skip, got %d", n)
	}
	if store.GetBlock(orgID, "partial-zero") == nil {
		t.Fatal("zero-ref block should remain while a live fs_object references it")
	}
	if deleted := sp.DeletedBlocks(); len(deleted) != 0 {
		t.Fatalf("S3 should not be touched for live fs_object reference, got %v", deleted)
	}
}

func TestWorker_ProcessBlock_UsesCanonicalStorageClassForDeleteTracking(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	store.AddOrganization(orgID)
	store.AddBlock(orgID, "block-canonical-cold", "cold-tier", 0)
	queuedAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)
	if err := store.EnqueueItem(orgID, queuedAt, ItemBlock, "block-canonical-cold", uuid.Nil, "hot-tier", 0); err != nil {
		t.Fatalf("EnqueueItem() error = %v", err)
	}

	n, err := w.ProcessOnce(context.Background())
	if err != nil {
		t.Fatalf("ProcessOnce() error = %v", err)
	}
	if n != 1 {
		t.Fatalf("ProcessOnce() processed = %d, want 1", n)
	}
	if store.GetBlock(orgID, "block-canonical-cold") != nil {
		t.Fatal("expected block row to be finalized from DB")
	}
	orphans := store.AllS3Orphans()
	if len(orphans) != 0 {
		t.Fatalf("AllS3Orphans() len = %d, want 0 after cleanup completes", len(orphans))
	}
}

func TestWorker_ProcessBlock_MissingCanonicalRowSkipsWithoutClaimOrDLQ(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	candidateAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)
	store.AddBlockMapping(orgID, "sha1-missing", "block-missing")
	if _, err := store.EnsureBlockGCCandidate(orgID, "block-missing", "hot", candidateAt); err != nil {
		t.Fatalf("EnsureBlockGCCandidate failed: %v", err)
	}
	if err := store.EnqueueItem(orgID, candidateAt, ItemBlock, "block-missing", uuid.Nil, "hot", 0); err != nil {
		t.Fatalf("EnqueueItem failed: %v", err)
	}

	n, err := w.ProcessOnce(context.Background())
	if err != nil {
		t.Fatalf("ProcessOnce failed: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 processed skip, got %d", n)
	}
	if store.GetBlock(orgID, "block-missing") != nil {
		t.Fatal("missing block candidate should not materialize a canonical row")
	}
	if got := len(store.AllBlockGCCandidates()); got != 0 {
		t.Fatalf("expected block candidate cleanup, got %d rows", got)
	}
	// The canonical blocks row never existed here, so blocks.sha1 is unavailable
	// and the forward mapping cannot be resolved without the (dropped) reverse
	// index. It survives as a harmless dangling pointer (recorded via the
	// gc_block_mapping_sha1_missing metric); it is NOT swept on this path.
	if !store.ForwardBlockMappingExists(orgID, "sha1-missing") {
		t.Fatal("missing-row path should leave the unresolvable forward mapping in place")
	}
	if got := len(store.FailedItems(orgID)); got != 0 {
		t.Fatalf("expected no DLQ entries, got %d", got)
	}
}

func TestWorker_ProcessBlock_StubRowAfterClaimIsCleanedWithoutDLQ(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	candidateAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)
	store.AddStubBlockForTest(orgID, "block-stub")
	store.AddBlockMapping(orgID, "sha1-stub", "block-stub")
	if _, err := store.EnsureBlockGCCandidate(orgID, "block-stub", "hot", candidateAt); err != nil {
		t.Fatalf("EnsureBlockGCCandidate failed: %v", err)
	}
	if err := store.EnqueueItem(orgID, candidateAt, ItemBlock, "block-stub", uuid.Nil, "hot", 0); err != nil {
		t.Fatalf("EnqueueItem failed: %v", err)
	}

	n, err := w.ProcessOnce(context.Background())
	if err != nil {
		t.Fatalf("ProcessOnce failed: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 processed skip, got %d", n)
	}
	if store.GetBlock(orgID, "block-stub") != nil {
		t.Fatal("stub block row should be removed after cleanup")
	}
	if got := len(store.AllBlockGCCandidates()); got != 0 {
		t.Fatalf("expected block candidate cleanup, got %d rows", got)
	}
	if store.ForwardBlockMappingExists(orgID, "sha1-stub") {
		t.Fatal("expected stub cleanup to remove the forward mapping via blocks.sha1")
	}
	if got := len(store.FailedItems(orgID)); got != 0 {
		t.Fatalf("expected no DLQ entries, got %d", got)
	}
}

func TestWorker_ProcessBlock_LiveFSObjectReferenceViaMappedIDSkipsDelete(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)

	orgID := uuid.New()
	libraryID := uuid.New()
	store.AddLibrary(orgID, libraryID, "hot")
	store.AddBlock(orgID, "internal-block", "hot", 0)
	externalBlockID := "0123456789abcdef0123456789abcdef01234567"
	store.AddBlockMapping(orgID, externalBlockID, "internal-block")
	// The fs_object's reference is stored against the resolved internal ID (the
	// SHA-1→SHA-256 resolution happens at registration time, not at GC time).
	store.AddFSObjectReferenceForTest(orgID, "internal-block", libraryID, "fs-mapped")
	store.AddFSObject(libraryID, "fs-mapped", "file", []string{externalBlockID})
	store.EnqueueItem(orgID, time.Now().Add(-2*time.Hour), ItemBlock, "internal-block", uuid.Nil, "hot", 0)

	n, err := w.ProcessOnce(context.Background())
	if err != nil {
		t.Fatalf("ProcessOnce failed: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 processed skip, got %d", n)
	}
	if store.GetBlock(orgID, "internal-block") == nil {
		t.Fatal("mapped zero-ref block should remain while a live fs_object references it")
	}
	if deleted := sp.DeletedBlocks(); len(deleted) != 0 {
		t.Fatalf("S3 should not be touched for mapped live fs_object reference, got %v", deleted)
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
	if n != 0 {
		t.Errorf("expected 0 completed in dry run, got %d", n)
	}

	// Block should still exist (dry run)
	if store.GetBlock(orgID, "block-1") == nil {
		t.Error("block should still exist in dry run mode")
	}
	if len(sp.DeletedBlocks()) != 0 {
		t.Error("S3 should not be touched in dry run mode")
	}
	if got := len(store.QueueItems(orgID)); got != 1 {
		t.Fatalf("dry run should leave block queued, got %d items", got)
	}
}

func TestWorker_ProcessFSObject_DryRunLeavesQueueAndRefsUntouched(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, true, stats)

	orgID := uuid.New()
	libID := uuid.New()
	queuedAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)
	store.AddBlock(orgID, "blk-live", "hot", 2)
	store.AddFSObjectWithEntries(libID, "fs-root", "dir", nil, []string{"fs-child"})
	store.AddFSObject(libID, "fs-child", "file", []string{"blk-live"})
	store.SeedQueueItemForTest(orgID, queuedAt, ItemFSObject, "fs-root", libID, "", 0)

	n, err := w.ProcessOnce(context.Background())
	if err != nil {
		t.Fatalf("ProcessOnce failed: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 completed items in dry run, got %d", n)
	}
	if got := len(store.QueueItems(orgID)); got != 1 {
		t.Fatalf("dry run should leave fs_object queued, got %d items", got)
	}
	if items := store.QueueItems(orgID); items[0].ItemID != "fs-root" || items[0].ItemType != ItemFSObject {
		t.Fatalf("dry run should not enqueue children, got %#v", items)
	}
	if block := store.GetBlock(orgID, "blk-live"); block == nil || store.BlockReferenceCount(orgID, "blk-live") != 2 {
		t.Fatalf("dry run should not remove block references, got %#v refs=%d", block, store.BlockReferenceCount(orgID, "blk-live"))
	}
	taskID := uuid.NewMD5(uuid.NameSpaceOID, []byte(fmt.Sprintf("%s-%s-%d", libID, "fs-root", queuedAt.UnixNano())))
	if _, exists := store.gcStats[taskID.String()]; exists {
		t.Fatal("dry run should not mark fs_object as processed")
	}
	if obj := store.GetFSObj(libID, "fs-root"); obj == nil {
		t.Fatal("dry run should not delete fs_object")
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

	if err := store.EnqueueBatch([]QueueItem{{
		OrgID:                 orgID,
		QueuedAt:              time.Now().Add(-2 * time.Hour),
		ItemType:              ItemCommit,
		ItemID:                "commit-abc",
		LibraryID:             libID,
		BlockRepresentationID: db.PlainBlockRepresentationID,
	}}); err != nil {
		t.Fatalf("seed commit failed: %v", err)
	}

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

func TestWorker_ProcessCommit_DryRunDoesNotAcquireLibraryDeleteGuard(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, true, stats)

	orgID := uuid.New()
	libID := uuid.New()
	deletedAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)
	store.AddDeletedLibrary(orgID, libID, "hot", deletedAt)
	store.AddCommit(libID, "commit-dry", "fs-root")

	item := QueueItem{
		OrgID:                       orgID,
		QueuedAt:                    deletedAt,
		IdentityAt:                  deletedAt,
		RequiresLibraryDeletedCheck: true,
		ItemType:                    ItemCommit,
		ItemID:                      "commit-dry",
		LibraryID:                   libID,
	}
	if err := w.processCommit(item); err != nil {
		t.Fatalf("processCommit dry run failed: %v", err)
	}
	if len(store.libraryHardDeleteLocks) != 0 {
		t.Fatal("dry run commit should not acquire library hard-delete locks")
	}
	if store.GetCommitRecord(libID, "commit-dry") == nil {
		t.Fatal("dry run commit should not delete the commit")
	}
}

// P6b (ISSUE-GC-ORPHAN-WORKER-REVALIDATION-01): a scanner orphan item
// (RequiresLibraryDeletedCheck=true, no delete marker) must be re-validated against the
// CANONICAL libraries table at execution time. If the library is live/recoverable there
// (projection drift, or restore/recreate after enqueue), the worker must NOT delete its content.
func TestWorker_ProcessCommit_OrphanSkipsWhenCanonicalLibraryLive(t *testing.T) {
	store := NewMockStore()
	w := NewWorker(store, nil, NewQueue(store), 100, 0, false, &Stats{})

	orgID, libID := uuid.New(), uuid.New()
	store.AddLibrary(orgID, libID, "hot") // canonical libraries row present (live/restored)
	store.AddCommit(libID, "commit-1", "fs-root")

	item := QueueItem{
		OrgID: orgID, QueuedAt: time.Now().UTC(), IdentityAt: time.Now().UTC(),
		RequiresLibraryDeletedCheck: true, ItemType: ItemCommit, ItemID: "commit-1", LibraryID: libID,
		LibraryGuardMode: LibraryGuardCanonicalMustBeAbsent,
	}
	if err := w.processCommit(item); err != nil {
		t.Fatalf("processCommit: %v", err)
	}
	if store.GetCommitRecord(libID, "commit-1") == nil {
		t.Fatal("orphan commit of a live (canonical) library must NOT be deleted (P6b)")
	}
}

// When the canonical library is genuinely gone, the orphan is cleaned as before.
func TestWorker_ProcessCommit_OrphanDeletesWhenCanonicalLibraryGone(t *testing.T) {
	store := NewMockStore()
	w := NewWorker(store, nil, NewQueue(store), 100, 0, false, &Stats{})

	orgID, libID := uuid.New(), uuid.New()
	// No AddLibrary → canonical libraries row absent.
	store.AddCommit(libID, "commit-1", "fs-root")

	item := QueueItem{
		OrgID: orgID, QueuedAt: time.Now().UTC(), IdentityAt: time.Now().UTC(),
		RequiresLibraryDeletedCheck: true, ItemType: ItemCommit, ItemID: "commit-1", LibraryID: libID,
		LibraryGuardMode:      LibraryGuardCanonicalMustBeAbsent,
		BlockRepresentationID: db.PlainBlockRepresentationID, // needed to enqueue the root fs_object child
	}
	if err := w.processCommit(item); err != nil {
		t.Fatalf("processCommit: %v", err)
	}
	if store.GetCommitRecord(libID, "commit-1") != nil {
		t.Fatal("orphan commit of a genuinely gone library should be deleted")
	}
}

// A canonical existence read error must fail closed: do not delete.
func TestWorker_ProcessCommit_OrphanFailsClosedOnCanonicalError(t *testing.T) {
	store := NewMockStore()
	w := NewWorker(store, nil, NewQueue(store), 100, 0, false, &Stats{})

	orgID, libID := uuid.New(), uuid.New()
	store.AddCommit(libID, "commit-1", "fs-root")
	store.canonicalLibraryExistsErr = errors.New("cassandra unavailable")

	item := QueueItem{
		OrgID: orgID, QueuedAt: time.Now().UTC(), IdentityAt: time.Now().UTC(),
		RequiresLibraryDeletedCheck: true, ItemType: ItemCommit, ItemID: "commit-1", LibraryID: libID,
		LibraryGuardMode: LibraryGuardCanonicalMustBeAbsent,
	}
	if err := w.processCommit(item); err == nil {
		t.Fatal("expected a fail-closed error on canonical existence read failure")
	}
	if store.GetCommitRecord(libID, "commit-1") == nil {
		t.Fatal("commit must NOT be deleted when the canonical existence check errors (fail closed)")
	}
}

// The fence (RenewLibraryHardDeleteLock) runs immediately before the destructive delete.
// If the lease was lost to TTL expiry or a concurrent restore after the canonical-absence
// check, the fence must fail closed and the commit must NOT be deleted.
func TestWorker_ProcessCommit_FenceFailsClosedWhenLockLost(t *testing.T) {
	store := NewMockStore()
	worker := NewWorker(store, nil, NewQueue(store), 100, 0, false, &Stats{})

	orgID, libraryID := uuid.New(), uuid.New()
	// Canonical library genuinely absent, so acquire-time revalidation passes...
	store.AddCommit(libraryID, "commit-1", "")
	// ...but the lease is lost between acquire and the pre-delete fence.
	store.forceRenewLibraryLockNotOwned = true

	item := QueueItem{
		OrgID:                       orgID,
		QueuedAt:                    time.Now().UTC(),
		RequiresLibraryDeletedCheck: true,
		LibraryGuardMode:            LibraryGuardCanonicalMustBeAbsent,
		ItemType:                    ItemCommit,
		ItemID:                      "commit-1",
		LibraryID:                   libraryID,
	}
	if err := worker.processCommit(item); err == nil {
		t.Fatal("expected a fail-closed error when the delete fence loses the library lock")
	}
	if store.GetCommitRecord(libraryID, "commit-1") == nil {
		t.Fatal("commit must NOT be deleted when the pre-delete fence loses the lock")
	}
}

func TestWorker_ProcessCommit_UnknownLibraryGuardFailsClosed(t *testing.T) {
	store := NewMockStore()
	worker := NewWorker(store, nil, NewQueue(store), 100, 0, false, &Stats{})

	orgID, libraryID := uuid.New(), uuid.New()
	store.AddCommit(libraryID, "commit-1", "")
	item := QueueItem{
		OrgID:                       orgID,
		QueuedAt:                    time.Now().UTC(),
		RequiresLibraryDeletedCheck: true,
		LibraryGuardMode:            LibraryGuardMode("future_mode"),
		ItemType:                    ItemCommit,
		ItemID:                      "commit-1",
		LibraryID:                   libraryID,
	}

	if err := worker.processCommit(item); err == nil {
		t.Fatal("unknown library guard mode must fail closed")
	}
	if store.GetCommitRecord(libraryID, "commit-1") == nil {
		t.Fatal("unknown library guard mode must not delete the commit")
	}
}

// The fs_object path is where legitimate fs: refs get released, so it is the core P6b
// protection: an orphan fs_object of a live canonical library must not be deleted.
func TestWorker_ProcessFSObject_OrphanSkipsWhenCanonicalLibraryLive(t *testing.T) {
	store := NewMockStore()
	w := NewWorker(store, nil, NewQueue(store), 100, 0, false, &Stats{})

	orgID, libID := uuid.New(), uuid.New()
	store.AddLibrary(orgID, libID, "hot") // canonical present
	store.AddFSObject(libID, "fs-1", "file", []string{"blk-1"})

	item := QueueItem{
		OrgID: orgID, QueuedAt: time.Now().UTC(), IdentityAt: time.Now().UTC(),
		RequiresLibraryDeletedCheck: true, ItemType: ItemFSObject, ItemID: "fs-1", LibraryID: libID,
		LibraryGuardMode:      LibraryGuardCanonicalMustBeAbsent,
		BlockRepresentationID: db.PlainBlockRepresentationID,
	}
	if err := w.processFSObject(context.Background(), item); err != nil {
		t.Fatalf("processFSObject: %v", err)
	}
	if _, err := store.GetFSObject(libID, "fs-1"); err != nil {
		t.Fatal("orphan fs_object of a live (canonical) library must NOT be deleted (P6b)")
	}
}

func TestWorker_ProcessFSObject_FenceFailsClosedWhenLockLostBeforeDelete(t *testing.T) {
	store := NewMockStore()
	w := NewWorker(store, nil, NewQueue(store), 100, 0, false, &Stats{})

	orgID, libID := uuid.New(), uuid.New()
	store.AddFSObject(libID, "fs-1", "dir", nil)
	store.forceRenewLibraryLockNotOwned = true

	item := QueueItem{
		OrgID:                       orgID,
		QueuedAt:                    time.Now().UTC(),
		IdentityAt:                  time.Now().UTC(),
		RequiresLibraryDeletedCheck: true,
		LibraryGuardMode:            LibraryGuardCanonicalMustBeAbsent,
		ItemType:                    ItemFSObject,
		ItemID:                      "fs-1",
		LibraryID:                   libID,
		BlockRepresentationID:       db.PlainBlockRepresentationID,
	}
	if err := w.processFSObject(context.Background(), item); err == nil {
		t.Fatal("expected a fail-closed error when the fs_object delete fence loses the library lock")
	}
	if _, err := store.GetFSObject(libID, "fs-1"); err != nil {
		t.Fatal("fs_object must NOT be deleted when the pre-delete fence loses the lock")
	}
}

func TestWorker_ProcessFSObject_LongReferenceCleanupFailsClosedWhenLockLostMidLoop(t *testing.T) {
	store := NewMockStore()
	w := NewWorker(store, nil, NewQueue(store), 100, 0, false, &Stats{})

	orgID, libID := uuid.New(), uuid.New()
	store.AddFSObject(libID, "fs-1", "file", []string{"blk-1", "blk-2"})
	store.AddBlock(orgID, "blk-1", "hot", 0)
	store.AddBlock(orgID, "blk-2", "hot", 0)
	store.AddFSObjectReferenceForTest(orgID, "blk-1", libID, "fs-1")
	store.AddFSObjectReferenceForTest(orgID, "blk-2", libID, "fs-1")

	base := time.Now().UTC()
	ticks := []time.Time{base, base.Add(fsObjectReferenceFenceInterval + time.Minute)}
	w.clock = func() time.Time {
		if len(ticks) == 0 {
			return base.Add(2 * (fsObjectReferenceFenceInterval + time.Minute))
		}
		now := ticks[0]
		ticks = ticks[1:]
		return now
	}
	firstBlockChecked := false
	store.blockHasReferencesHook = func(_ uuid.UUID, blockID string, current bool) (bool, error) {
		if blockID == "blk-1" && !firstBlockChecked {
			firstBlockChecked = true
			store.forceRenewLibraryLockNotOwned = true
		}
		return current, nil
	}

	item := QueueItem{
		OrgID:                       orgID,
		QueuedAt:                    base,
		IdentityAt:                  base,
		RequiresLibraryDeletedCheck: true,
		LibraryGuardMode:            LibraryGuardCanonicalMustBeAbsent,
		ItemType:                    ItemFSObject,
		ItemID:                      "fs-1",
		LibraryID:                   libID,
		BlockRepresentationID:       db.PlainBlockRepresentationID,
	}
	if err := w.processFSObject(context.Background(), item); err == nil {
		t.Fatal("expected a fail-closed error when the long block-reference cleanup loses the library lock")
	}
	if _, err := store.GetFSObject(libID, "fs-1"); err != nil {
		t.Fatal("fs_object must NOT be deleted when long reference cleanup loses the lock")
	}
	if got := store.BlockReferenceCount(orgID, "blk-1"); got != 0 {
		t.Fatalf("first block references = %d, want 0 after the first guarded mutation", got)
	}
	if got := store.BlockReferenceCount(orgID, "blk-2"); got != 1 {
		t.Fatalf("second block references = %d, want 1 because cleanup must stop before the stale-lease mutation", got)
	}
}

// This intentionally proves the worker uses the queued org plus library UUID as
// the canonical point-read key. It is not evidence of global library absence on
// its own; the broader system invariant is that library_id is globally unique.
func TestWorker_ProcessCommit_OrphanUsesScopedCanonicalPointRead(t *testing.T) {
	store := NewMockStore()
	worker := NewWorker(store, nil, NewQueue(store), 100, 0, false, &Stats{})

	staleOrgID := uuid.New()
	canonicalOrgID := uuid.New()
	libraryID := uuid.New()
	store.AddLibrary(canonicalOrgID, libraryID, "hot")
	store.AddCommit(libraryID, "commit-1", "")

	item := QueueItem{
		OrgID:                       staleOrgID,
		QueuedAt:                    time.Now().UTC(),
		IdentityAt:                  time.Now().UTC(),
		RequiresLibraryDeletedCheck: true,
		LibraryGuardMode:            LibraryGuardCanonicalMustBeAbsent,
		ItemType:                    ItemCommit,
		ItemID:                      "commit-1",
		LibraryID:                   libraryID,
	}
	if err := worker.processCommit(item); err != nil {
		t.Fatalf("processCommit: %v", err)
	}
	if store.GetCommitRecord(libraryID, "commit-1") != nil {
		t.Fatal("orphan work must use the queued org and library as the canonical point-read key")
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
	store.SeedQueueItemForTest(orgID, queuedAt, ItemFSObject, "fs-root", libID, "", 0)

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
	store.SeedQueueItemForTest(orgID, queuedAt, ItemFSObject, "shared-root", libPending, "", 0)

	if err := w.processCommit(QueueItem{OrgID: orgID, QueuedAt: queuedAt, IdentityAt: queuedAt, ItemType: ItemCommit, ItemID: "commit-abc", LibraryID: libTarget, BlockRepresentationID: db.PlainBlockRepresentationID}); err != nil {
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
	store.SeedQueueItemForTest(orgID, time.Now().Add(-2*time.Hour), ItemCommit, "commit-gone", libID, "", 0)

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

	// blk-a, blk-b are referenced only by fs-obj-1 (will hit 0 refs when it is swept).
	store.AddBlock(orgID, "blk-a", "hot", 0)
	store.AddFSObjectReferenceForTest(orgID, "blk-a", libID, "fs-obj-1")
	store.AddBlock(orgID, "blk-b", "hot", 0)
	store.AddFSObjectReferenceForTest(orgID, "blk-b", libID, "fs-obj-1")
	// blk-c is also referenced by another fs_object, so it stays alive (1 ref left).
	store.AddBlock(orgID, "blk-c", "hot", 0)
	store.AddFSObjectReferenceForTest(orgID, "blk-c", libID, "fs-obj-1")
	store.AddFSObjectReferenceForTest(orgID, "blk-c", libID, "fs-obj-other")

	// Create an fs_object referencing these blocks
	store.AddFSObject(libID, "fs-obj-1", "file", []string{"blk-a", "blk-b", "blk-c"})

	// Add library so storage class lookup works
	store.AddLibrary(orgID, libID, "hot")

	// Enqueue the fs_object
	store.SeedQueueItemForTest(orgID, time.Now().Add(-2*time.Hour), ItemFSObject, "fs-obj-1", libID, "", 0)

	ctx := context.Background()
	n, err := w.ProcessOnce(ctx)
	if err != nil {
		t.Fatalf("ProcessOnce failed: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 completed, got %d", n)
	}

	// FS object should be deleted
	if store.GetFSObj(libID, "fs-obj-1") != nil {
		t.Error("fs_object should be deleted")
	}

	// Blocks blk-a and blk-b should have their fs-obj-1 reference removed → 0 refs.
	if got := store.BlockReferenceCount(orgID, "blk-a"); got != 0 {
		t.Errorf("blk-a references should be 0, got %d", got)
	}
	if got := store.BlockReferenceCount(orgID, "blk-b"); got != 0 {
		t.Errorf("blk-b references should be 0, got %d", got)
	}
	// blk-c keeps its fs-obj-other reference.
	if got := store.BlockReferenceCount(orgID, "blk-c"); got != 1 {
		t.Errorf("blk-c references should be 1, got %d", got)
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

func TestWorker_ProcessBlock_ReReferencedClaimReleaseIsOwnedByCandidate(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	libID := uuid.New()
	candidateAt := time.Now().Add(-2 * time.Hour).UTC()
	blockHasRefsCalls := 0
	store.blockHasReferencesHook = func(hookOrgID uuid.UUID, hookBlockID string, current bool) (bool, error) {
		blockHasRefsCalls++
		if blockHasRefsCalls == 1 {
			return false, nil
		}
		store.AddFSObjectReferenceForTest(hookOrgID, hookBlockID, libID, "fs-live")
		return true, nil
	}

	store.AddBlock(orgID, "blk-rereferenced", "hot", 0)
	store.AddBlockGCCandidate(orgID, "blk-rereferenced", "hot", candidateAt)
	store.EnqueueItem(orgID, candidateAt, ItemBlock, "blk-rereferenced", libID, "hot", 0)

	n, err := w.ProcessOnce(context.Background())
	if err != nil {
		t.Fatalf("ProcessOnce failed: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 processed item, got %d", n)
	}
	block := store.GetBlock(orgID, "blk-rereferenced")
	if block == nil {
		t.Fatal("block should remain after re-reference")
	}
	if block.GCState != "" || block.GCClaimID != "" {
		t.Fatalf("claim should be released after re-reference, got state=%q claim=%q", block.GCState, block.GCClaimID)
	}
	if got := len(store.AllBlockGCCandidates()); got != 0 {
		t.Fatalf("expected candidate cleanup after re-reference, got %d", got)
	}
	if blockHasRefsCalls != 2 {
		t.Fatalf("expected 2 block reference checks, got %d", blockHasRefsCalls)
	}
}

func TestWorker_ProcessFSObject_CascadesDirEntries(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	libID := uuid.New()
	queuedAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)
	blockRepresentationID := db.EncryptedLibraryBlockRepresentationID(libID.String())

	// Create a directory with children
	store.AddFSObjectWithEntries(libID, "fs-dir", "dir", nil, []string{"fs-child1", "fs-child2"})
	store.AddLibrary(orgID, libID, "hot")

	if err := store.EnqueueBatch([]QueueItem{{
		OrgID:                 orgID,
		QueuedAt:              queuedAt,
		IdentityAt:            queuedAt,
		ItemType:              ItemFSObject,
		ItemID:                "fs-dir",
		LibraryID:             libID,
		BlockRepresentationID: blockRepresentationID,
	}}); err != nil {
		t.Fatalf("EnqueueBatch failed: %v", err)
	}

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
			if item.BlockRepresentationID != blockRepresentationID {
				t.Fatalf("child %s BlockRepresentationID = %q, want %q", item.ItemID, item.BlockRepresentationID, blockRepresentationID)
			}
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
	store.SeedQueueItemForTest(orgID, queuedAt, ItemFSObject, "fs-child1", libID, "", 0)

	items, err := store.DequeueBatch(orgID, 1, time.Now())
	if err != nil || len(items) != 1 {
		t.Fatalf("DequeueBatch failed: %v / items=%d", err, len(items))
	}
	if err := q.IncrementRetry(items[0]); err != nil {
		t.Fatalf("IncrementRetry failed: %v", err)
	}

	err = w.processFSObject(context.Background(), QueueItem{OrgID: orgID, QueuedAt: queuedAt, IdentityAt: queuedAt, ItemType: ItemFSObject, ItemID: "fs-dir", LibraryID: libID, BlockRepresentationID: db.PlainBlockRepresentationID})
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
	store.SeedQueueItemForTest(orgID, queuedAt, ItemFSObject, "shared-child", libPending, "", 0)

	err := w.processFSObject(context.Background(), QueueItem{OrgID: orgID, QueuedAt: queuedAt, IdentityAt: queuedAt, ItemType: ItemFSObject, ItemID: "fs-dir", LibraryID: libTarget, BlockRepresentationID: db.PlainBlockRepresentationID})
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

func TestWorker_ProcessFSObject_RetryIsIdempotent(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	libID := uuid.New()
	identityAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)

	// blk-a is referenced by both fs-obj-1 and fs-other.
	store.AddBlock(orgID, "blk-a", "hot", 0)
	store.AddFSObjectReferenceForTest(orgID, "blk-a", libID, "fs-obj-1")
	store.AddFSObjectReferenceForTest(orgID, "blk-a", libID, "fs-other")
	store.AddFSObject(libID, "fs-obj-1", "file", []string{"blk-a"})
	store.AddFSObject(libID, "fs-other", "file", []string{"blk-a"})
	store.AddLibrary(orgID, libID, "hot")

	first := QueueItem{OrgID: orgID, QueuedAt: identityAt, IdentityAt: identityAt, ItemType: ItemFSObject, ItemID: "fs-obj-1", LibraryID: libID}
	if err := w.processFSObject(context.Background(), first); err != nil {
		t.Fatalf("first processFSObject failed: %v", err)
	}
	if got := store.BlockReferenceCount(orgID, "blk-a"); got != 1 {
		t.Fatalf("block references after first pass = %d, want 1 (fs-other still references it)", got)
	}

	// Re-processing the same fs_object is naturally idempotent: removing the
	// already-gone fs-obj-1 reference is a no-op (no marker bookkeeping needed),
	// so blk-a keeps its fs-other reference and is not double-removed.
	store.AddFSObject(libID, "fs-obj-1", "file", []string{"blk-a"})
	second := QueueItem{OrgID: orgID, QueuedAt: time.Now().UTC(), IdentityAt: identityAt, ItemType: ItemFSObject, ItemID: "fs-obj-1", LibraryID: libID}
	if err := w.processFSObject(context.Background(), second); err != nil {
		t.Fatalf("retry processFSObject failed: %v", err)
	}
	if got := store.BlockReferenceCount(orgID, "blk-a"); got != 1 {
		t.Fatalf("block references after retry = %d, want 1", got)
	}
	if store.GetFSObj(libID, "fs-obj-1") != nil {
		t.Fatal("fs_object should be deleted after the idempotent retry")
	}
}

func TestWorker_ProcessUserCascade_LockBusyPostponesWithoutRetryOrDLQ(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	userID := uuid.New()
	deletedAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)
	oldToken := uuid.New()

	store.AddDeletedUser(orgID, userID, "alice@test.com", deletedAt)
	if acquired, err := store.AcquireUserHardDeleteLock(userID, oldToken); err != nil || !acquired {
		t.Fatalf("seed user hard-delete lock acquired=%v err=%v", acquired, err)
	}
	if err := store.EnqueueItem(orgID, deletedAt, ItemUserCascade, userID.String(), uuid.Nil, "", 4); err != nil {
		t.Fatalf("enqueue user cascade failed: %v", err)
	}

	n, err := w.ProcessOnce(context.Background())
	if err != nil {
		t.Fatalf("ProcessOnce failed: %v", err)
	}
	if n != 0 {
		t.Fatalf("lock-contended item should not complete, got %d", n)
	}
	if !store.HasUser(orgID, userID) {
		t.Fatal("user should remain while another hard delete lock is active")
	}
	items := store.QueueItems(orgID)
	if len(items) != 1 {
		t.Fatalf("expected one postponed queue item, got %d", len(items))
	}
	if items[0].RetryCount != 4 {
		t.Fatalf("retry count after lock contention = %d, want 4", items[0].RetryCount)
	}
	if failed := store.FailedItems(orgID); len(failed) != 0 {
		t.Fatalf("lock contention should not move item to DLQ, got %d failed items", len(failed))
	}
}

func TestWorker_ProcessUserCascade_StaleLockLeaseIsRecovered(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	userID := uuid.New()
	deletedAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)
	oldToken := uuid.New()

	store.AddDeletedUser(orgID, userID, "alice@test.com", deletedAt)
	if acquired, err := store.AcquireUserHardDeleteLock(userID, oldToken); err != nil || !acquired {
		t.Fatalf("seed user hard-delete lock acquired=%v err=%v", acquired, err)
	}
	store.mu.Lock()
	lock := store.userHardDeleteLocks[userID]
	lock.Heartbeat = time.Now().Add(-hardDeleteLockStaleAfter - time.Minute)
	store.userHardDeleteLocks[userID] = lock
	store.mu.Unlock()
	if err := store.EnqueueItem(orgID, deletedAt, ItemUserCascade, userID.String(), uuid.Nil, "", 0); err != nil {
		t.Fatalf("enqueue user cascade failed: %v", err)
	}

	n, err := w.ProcessOnce(context.Background())
	if err != nil {
		t.Fatalf("ProcessOnce failed: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected stale-lease cascade to complete, got %d", n)
	}
	if store.HasUser(orgID, userID) {
		t.Fatal("user should be hard-deleted after stale lease recovery")
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

	// The same fs_id "shared-fs" lives in two libraries; each holds a distinct,
	// library-scoped reference (fs:<lib>:<fs_id>) on its own block.
	store.AddBlock(orgID, "blk-a", "hot", 0)
	store.AddFSObjectReferenceForTest(orgID, "blk-a", libA, "shared-fs")
	store.AddBlock(orgID, "blk-b", "hot", 0)
	store.AddFSObjectReferenceForTest(orgID, "blk-b", libB, "shared-fs")
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

	if got := store.BlockReferenceCount(orgID, "blk-a"); got != 0 {
		t.Fatalf("block references for libA = %d, want 0", got)
	}
	if got := store.BlockReferenceCount(orgID, "blk-b"); got != 0 {
		t.Fatalf("block references for libB = %d, want 0", got)
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
	store.AddLibrary(orgID, libID, "hot")
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

	store.AddLibrary(orgID, libPending, "hot")
	store.AddLibrary(orgID, libTarget, "hot")
	store.AddCommit(libPending, "shared-commit", "fs-pending")
	store.AddFSObject(libPending, "shared-fs", "file", []string{"blk-pending"})
	store.AddCommit(libTarget, "shared-commit", "fs-target")
	store.AddFSObject(libTarget, "shared-fs", "file", []string{"blk-target"})
	store.SeedQueueItemForTest(orgID, identityAt, ItemCommit, "shared-commit", libPending, "", 0)
	store.SeedQueueItemForTest(orgID, identityAt, ItemFSObject, "shared-fs", libPending, "", 0)

	err := w.enqueueLibraryContentsAt(orgID, libTarget, "", "hot", identityAt, LibraryGuardNone)
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

func TestWorker_ProcessCommit_LibraryCascadeChildSkipsAfterRestore(t *testing.T) {
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
	store.AddFSObject(libID, "fs-root", "file", []string{"blk-a"})
	store.deleteRestoreJobsByLibraryErr = errors.New("restore jobs unavailable")

	parent := QueueItem{OrgID: orgID, QueuedAt: deletedAt, IdentityAt: deletedAt, ItemType: ItemLibraryCascade, ItemID: libID.String(), StorageClass: "hot"}
	if err := w.processLibraryCascade(context.Background(), parent); err == nil {
		t.Fatal("expected first library cascade attempt to fail")
	}

	store.mu.Lock()
	delete(store.deletedLibraries, libID)
	store.mu.Unlock()

	var child QueueItem
	for _, item := range store.QueueItems(orgID) {
		if item.ItemType == ItemCommit && item.ItemID == "commit-1" {
			child = item
			break
		}
	}
	if child.ItemID == "" {
		t.Fatal("expected commit child to be enqueued")
	}
	if !child.RequiresLibraryDeletedCheck {
		t.Fatal("expected commit child to carry library delete guard")
	}

	if err := w.processCommit(child); err != nil {
		t.Fatalf("processCommit after restore failed: %v", err)
	}
	if store.GetCommitRecord(libID, "commit-1") == nil {
		t.Fatal("commit child should be stale after restore and must not be deleted")
	}
}

func TestWorker_ProcessCommit_RootFSObjectChildSkipsAfterRestore(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	libID := uuid.New()
	deletedAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)
	blockRepresentationID := db.EncryptedLibraryBlockRepresentationID(libID.String())

	// Post-hard-delete state: the parent cascade already removed the canonical
	// `libraries` row and the delete marker together, so the guarded commit drains
	// (canonical absent). A cascade child never legitimately deletes while the
	// canonical row still exists — that path now postpones (see the guard).
	store.AddCommit(libID, "commit-1", "fs-root")
	store.AddBlock(orgID, "blk-a", "hot", 0)
	store.AddFSObjectReferenceForTest(orgID, "blk-a", libID, "fs-root")
	store.AddFSObject(libID, "fs-root", "file", []string{"blk-a"})

	guardedCommit := QueueItem{
		OrgID:                       orgID,
		QueuedAt:                    deletedAt,
		IdentityAt:                  deletedAt,
		RequiresLibraryDeletedCheck: true,
		ItemType:                    ItemCommit,
		ItemID:                      "commit-1",
		LibraryID:                   libID,
		BlockRepresentationID:       blockRepresentationID,
	}
	if err := w.processCommit(guardedCommit); err != nil {
		t.Fatalf("processCommit while library deleted failed: %v", err)
	}
	if store.GetCommitRecord(libID, "commit-1") != nil {
		t.Fatal("commit should be deleted before restore")
	}

	var rootChild QueueItem
	for _, item := range store.QueueItems(orgID) {
		if item.ItemType == ItemFSObject && item.ItemID == "fs-root" {
			rootChild = item
			break
		}
	}
	if rootChild.ItemID == "" {
		t.Fatal("expected root fs_object child to be enqueued")
	}
	if !rootChild.RequiresLibraryDeletedCheck {
		t.Fatal("expected root fs_object child to inherit library delete guard")
	}
	if rootChild.BlockRepresentationID != blockRepresentationID {
		t.Fatalf("root fs_object child BlockRepresentationID = %q, want %q", rootChild.BlockRepresentationID, blockRepresentationID)
	}

	// A restore re-creates the canonical `libraries` row (active). The still-queued
	// root fs_object child must now be stale and skip rather than purge restored content.
	store.AddLibrary(orgID, libID, "hot")

	if err := w.processFSObject(context.Background(), rootChild); err != nil {
		t.Fatalf("processFSObject after restore failed: %v", err)
	}
	if store.GetFSObj(libID, "fs-root") == nil {
		t.Fatal("root fs_object child should be stale after restore and must not be deleted")
	}
	if got := store.BlockReferenceCount(orgID, "blk-a"); got != 1 {
		t.Fatalf("block references after restored root child = %d, want 1", got)
	}
}

// P6b crash-window guard: a deleted_at_identity cascade child must NOT purge content
// while the canonical `libraries` row still exists (soft-deleted / restorable). The parent
// cascade removes the canonical row only in HardDeleteLibrary; if it crashed before that
// and this worker stole its stale lease, the child must postpone, not delete — otherwise a
// later restore would revive a partially-purged library.
func TestWorker_ProcessCommit_DeletedAtIdentityPostponesWhileCanonicalPresent(t *testing.T) {
	store := NewMockStore()
	w := NewWorker(store, nil, NewQueue(store), 100, 0, false, &Stats{})

	orgID, libID := uuid.New(), uuid.New()
	deletedAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)
	store.AddDeletedLibrary(orgID, libID, "hot", deletedAt) // canonical row AND marker present
	store.AddCommit(libID, "commit-1", "")

	item := QueueItem{
		OrgID: orgID, QueuedAt: deletedAt, IdentityAt: deletedAt,
		RequiresLibraryDeletedCheck: true, LibraryGuardMode: LibraryGuardDeletedAtIdentity,
		ItemType: ItemCommit, ItemID: "commit-1", LibraryID: libID,
	}
	err := w.processCommit(item)
	if !isHardDeleteInProgressError(err) {
		t.Fatalf("expected a hard-delete-in-progress (postpone) error, got %v", err)
	}
	if store.GetCommitRecord(libID, "commit-1") == nil {
		t.Fatal("commit must NOT be deleted while the canonical (soft-deleted) library still exists")
	}
}

// Once the parent cascade completes HardDeleteLibrary (canonical row gone), the same
// child drains and deletes normally.
func TestWorker_ProcessCommit_DeletedAtIdentityDeletesAfterCanonicalGone(t *testing.T) {
	store := NewMockStore()
	w := NewWorker(store, nil, NewQueue(store), 100, 0, false, &Stats{})

	orgID, libID := uuid.New(), uuid.New()
	deletedAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)
	store.AddDeletedLibrary(orgID, libID, "hot", deletedAt)
	store.AddCommit(libID, "commit-1", "")
	if err := store.HardDeleteLibrary(orgID, libID); err != nil { // canonical + marker removed
		t.Fatalf("HardDeleteLibrary: %v", err)
	}

	item := QueueItem{
		OrgID: orgID, QueuedAt: deletedAt, IdentityAt: deletedAt,
		RequiresLibraryDeletedCheck: true, LibraryGuardMode: LibraryGuardDeletedAtIdentity,
		ItemType: ItemCommit, ItemID: "commit-1", LibraryID: libID,
	}
	if err := w.processCommit(item); err != nil {
		t.Fatalf("processCommit after hard delete: %v", err)
	}
	if store.GetCommitRecord(libID, "commit-1") != nil {
		t.Fatal("commit should be deleted once the canonical library is gone (hard delete completed)")
	}
}

// The fs_object path releases block references (the irreversible step). It too must
// postpone — and not release any reference — while the canonical library still exists.
func TestWorker_ProcessFSObject_DeletedAtIdentityPostponesWhileCanonicalPresent(t *testing.T) {
	store := NewMockStore()
	w := NewWorker(store, nil, NewQueue(store), 100, 0, false, &Stats{})

	orgID, libID := uuid.New(), uuid.New()
	deletedAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)
	store.AddDeletedLibrary(orgID, libID, "hot", deletedAt)
	store.AddBlock(orgID, "blk-a", "hot", 0)
	store.AddFSObjectReferenceForTest(orgID, "blk-a", libID, "fs-1")
	store.AddFSObject(libID, "fs-1", "file", []string{"blk-a"})

	item := QueueItem{
		OrgID: orgID, QueuedAt: deletedAt, IdentityAt: deletedAt,
		RequiresLibraryDeletedCheck: true, LibraryGuardMode: LibraryGuardDeletedAtIdentity,
		ItemType: ItemFSObject, ItemID: "fs-1", LibraryID: libID,
		BlockRepresentationID: db.PlainBlockRepresentationID,
	}
	err := w.processFSObject(context.Background(), item)
	if !isHardDeleteInProgressError(err) {
		t.Fatalf("expected a hard-delete-in-progress (postpone) error, got %v", err)
	}
	if _, gerr := store.GetFSObject(libID, "fs-1"); gerr != nil {
		t.Fatal("fs_object must NOT be deleted while the canonical library still exists")
	}
	if got := store.BlockReferenceCount(orgID, "blk-a"); got != 1 {
		t.Fatalf("block reference must be preserved (not released) while canonical present; got %d want 1", got)
	}
}

func TestWorker_ProcessFSObject_LibraryCascadeChildSkipsAfterRestore(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	libID := uuid.New()
	deletedAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)

	store.AddOrganization(orgID)
	store.AddDeletedLibrary(orgID, libID, "hot", deletedAt)
	store.AddBlock(orgID, "blk-a", "hot", 0)
	store.AddFSObjectReferenceForTest(orgID, "blk-a", libID, "fs-file")
	store.AddFSObject(libID, "fs-file", "file", []string{"blk-a"})
	store.deleteRestoreJobsByLibraryErr = errors.New("restore jobs unavailable")

	parent := QueueItem{OrgID: orgID, QueuedAt: deletedAt, IdentityAt: deletedAt, ItemType: ItemLibraryCascade, ItemID: libID.String(), StorageClass: "hot"}
	if err := w.processLibraryCascade(context.Background(), parent); err == nil {
		t.Fatal("expected first library cascade attempt to fail")
	}

	store.mu.Lock()
	delete(store.deletedLibraries, libID)
	store.mu.Unlock()

	var child QueueItem
	for _, item := range store.QueueItems(orgID) {
		if item.ItemType == ItemFSObject && item.ItemID == "fs-file" {
			child = item
			break
		}
	}
	if child.ItemID == "" {
		t.Fatal("expected fs_object child to be enqueued")
	}
	if !child.RequiresLibraryDeletedCheck {
		t.Fatal("expected fs_object child to carry library delete guard")
	}

	if err := w.processFSObject(context.Background(), child); err != nil {
		t.Fatalf("processFSObject after restore failed: %v", err)
	}
	if store.GetFSObj(libID, "fs-file") == nil {
		t.Fatal("fs_object child should be stale after restore and must not be deleted")
	}
	if got := store.BlockReferenceCount(orgID, "blk-a"); got != 1 {
		t.Fatalf("block references after stale fs_object child = %d, want 1", got)
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
	if n != 0 {
		t.Errorf("expected 0 processed in dry run, got %d", n)
	}

	// User should still exist (dry run)
	if !store.HasUser(orgID, userID) {
		t.Error("user should still exist in dry run mode")
	}
	if got := len(store.QueueItems(orgID)); got != 1 {
		t.Fatalf("dry run should leave user cascade queued, got %d items", got)
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

	store.SeedQueueItemForTest(orgID, time.Now().Add(-2*time.Hour), ItemLibraryCascade, libID.String(), uuid.Nil, "hot", 0)

	ctx := context.Background()
	n, err := w.ProcessOnce(ctx)
	if err != nil {
		t.Fatalf("ProcessOnce failed: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 completed in dry run, got %d", n)
	}

	// Library should still exist (dry run)
	if _, err := store.GetLibraryStorageClass(orgID, libID); err != nil {
		t.Error("library should still exist in dry run mode")
	}
	if got := len(store.QueueItems(orgID)); got != 1 {
		t.Fatalf("dry run should leave library cascade queued, got %d items", got)
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
	if n != 0 {
		t.Errorf("expected 0 completed in dry run, got %d", n)
	}

	// Org should still exist (dry run)
	if !store.HasOrg(orgID) {
		t.Error("org should still exist in dry run mode")
	}
	if got := len(store.QueueItems(orgID)); got != 1 {
		t.Fatalf("dry run should leave org cascade queued, got %d items", got)
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

	store.SeedQueueItemForTest(orgID, time.Now().Add(-2*time.Hour), ItemLibraryCascade, "not-a-uuid", uuid.Nil, "", 0)

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
	ownedLibID := uuid.New()
	recipientID := uuid.New()
	deletedAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)

	store.AddOrganization(orgID)
	store.AddDeletedUser(orgID, userID, "alice@test.com", deletedAt)
	store.AddLibraryWithOwner(orgID, ownedLibID, userID, "hot")
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
	if deletedAt, err := store.GetLibraryDeletedAt(ownedLibID); err != nil || deletedAt == nil {
		t.Fatalf("owned library should be soft-deleted after user cascade, deletedAt=%v err=%v", deletedAt, err)
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

func TestWorker_ProcessUserCascade_AuxCleanupFailureDoesNotHardDeleteUser(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	userID := uuid.New()
	deletedAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)

	store.AddOrganization(orgID)
	store.AddDeletedUser(orgID, userID, "alice@test.com", deletedAt)
	store.AddStarredFile(userID)
	store.deleteStarredFilesByUserErr = errors.New("starred cleanup unavailable")

	item := QueueItem{OrgID: orgID, QueuedAt: deletedAt, IdentityAt: deletedAt, ItemType: ItemUserCascade, ItemID: userID.String()}
	err := w.processUserCascade(context.Background(), item)
	if err == nil {
		t.Fatal("expected cleanup failure")
	}
	if !store.HasUser(orgID, userID) {
		t.Fatal("user should remain for retry when auxiliary cleanup fails")
	}
	if len(store.AuditLogEntries()) != 0 {
		t.Fatal("user cascade audit should not be written before successful hard delete")
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

	store.SeedQueueItemForTest(orgID, deletedAt, ItemLibraryCascade, libID.String(), uuid.Nil, "hot", 0)

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

func TestWorker_ProcessLibraryCascade_StorageCounterFailureDoesNotHardDeleteLibrary(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	libID := uuid.New()
	deletedAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)

	store.AddOrganization(orgID)
	store.AddDeletedLibrary(orgID, libID, "hot", deletedAt)
	store.deleteLibraryStorageCounterErr = errors.New("counter delete unavailable")

	item := QueueItem{OrgID: orgID, QueuedAt: deletedAt, IdentityAt: deletedAt, ItemType: ItemLibraryCascade, ItemID: libID.String(), StorageClass: "hot"}
	err := w.processLibraryCascade(context.Background(), item)
	if err == nil {
		t.Fatal("expected storage counter cleanup failure")
	}
	if _, err := store.GetLibraryStorageClass(orgID, libID); err != nil {
		t.Fatalf("library should remain for retry when counter cleanup fails: %v", err)
	}
	for _, entry := range store.AuditLogEntries() {
		if entry.Action == "gc_library_cascade_deleted" {
			t.Fatal("library hard-delete audit should not be written before successful counter cleanup")
		}
	}
}

func TestWorker_ProcessLibraryCascade_ChildrenContinueAfterHardDelete(t *testing.T) {
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
	store.AddBlock(orgID, "blk-a", "hot", 0)
	store.AddFSObjectReferenceForTest(orgID, "blk-a", libID, "fs-root")
	store.AddFSObject(libID, "fs-root", "file", []string{"blk-a"})

	item := QueueItem{OrgID: orgID, QueuedAt: deletedAt, IdentityAt: deletedAt, ItemType: ItemLibraryCascade, ItemID: libID.String(), StorageClass: "hot"}
	if err := w.processLibraryCascade(context.Background(), item); err != nil {
		t.Fatalf("processLibraryCascade failed: %v", err)
	}
	if exists, err := store.LibraryExists(libID); err != nil {
		t.Fatalf("LibraryExists failed: %v", err)
	} else if exists {
		t.Fatal("library should be hard-deleted after successful cascade")
	}

	var commitItem QueueItem
	var fsObjectItem QueueItem
	for _, queued := range store.QueueItems(orgID) {
		switch {
		case queued.ItemType == ItemCommit && queued.ItemID == "commit-1":
			commitItem = queued
		case queued.ItemType == ItemFSObject && queued.ItemID == "fs-root":
			fsObjectItem = queued
		}
	}
	if commitItem.ItemID == "" || fsObjectItem.ItemID == "" {
		t.Fatalf("expected commit and fs_object children after cascade, got commit=%q fs_object=%q", commitItem.ItemID, fsObjectItem.ItemID)
	}
	if !commitItem.RequiresLibraryDeletedCheck || !fsObjectItem.RequiresLibraryDeletedCheck {
		t.Fatal("expected hard-delete children to keep the library delete guard")
	}
	if commitItem.LibraryGuardMode != LibraryGuardDeletedAtIdentity || fsObjectItem.LibraryGuardMode != LibraryGuardDeletedAtIdentity {
		t.Fatalf("hard-delete child guard modes = (%q, %q), want %q", commitItem.LibraryGuardMode, fsObjectItem.LibraryGuardMode, LibraryGuardDeletedAtIdentity)
	}

	if err := w.processCommit(commitItem); err != nil {
		t.Fatalf("processCommit after hard delete failed: %v", err)
	}
	if store.GetCommitRecord(libID, "commit-1") != nil {
		t.Fatal("commit should be deleted after successful hard delete")
	}

	if err := w.processFSObject(context.Background(), fsObjectItem); err != nil {
		t.Fatalf("processFSObject after hard delete failed: %v", err)
	}
	if store.GetFSObj(libID, "fs-root") != nil {
		t.Fatal("fs_object should be deleted after successful hard delete")
	}
	if got := store.BlockReferenceCount(orgID, "blk-a"); got != 0 {
		t.Fatalf("block references after hard-delete child processing = %d, want 0", got)
	}
}

func TestWorker_EnqueueLibraryContents_RequiresBlockRepresentation(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	libID := uuid.New()

	store.AddCommit(libID, "commit-1", "fs-root")
	store.AddFSObject(libID, "fs-root", "file", []string{"0123456789abcdef0123456789abcdef01234567"})

	err := w.EnqueueLibraryContents(orgID, libID, "hot")
	if err == nil {
		t.Fatal("expected missing block representation error")
	}
	if items := store.QueueItems(orgID); len(items) != 0 {
		t.Fatalf("queue items = %#v, want no enqueued work", items)
	}
}

func TestWorker_ProcessLibraryCascade_ChildrenContinueAfterHardDeleteWithStaleLock(t *testing.T) {
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
	store.AddBlock(orgID, "blk-a", "hot", 0)
	store.AddFSObjectReferenceForTest(orgID, "blk-a", libID, "fs-root")
	store.AddFSObject(libID, "fs-root", "file", []string{"blk-a"})

	item := QueueItem{OrgID: orgID, QueuedAt: deletedAt, IdentityAt: deletedAt, ItemType: ItemLibraryCascade, ItemID: libID.String(), StorageClass: "hot"}
	if err := w.processLibraryCascade(context.Background(), item); err != nil {
		t.Fatalf("processLibraryCascade failed: %v", err)
	}
	if exists, err := store.LibraryExists(libID); err != nil {
		t.Fatalf("LibraryExists failed: %v", err)
	} else if exists {
		t.Fatal("library should be hard-deleted after successful cascade")
	}

	leaseToken := uuid.New()
	acquired, err := store.AcquireLibraryHardDeleteLock(libID, leaseToken)
	if err != nil {
		t.Fatalf("AcquireLibraryHardDeleteLock after hard delete failed: %v", err)
	}
	if !acquired {
		t.Fatal("expected to simulate a lingering library hard-delete lock")
	}

	var commitItem QueueItem
	var fsObjectItem QueueItem
	for _, queued := range store.QueueItems(orgID) {
		switch {
		case queued.ItemType == ItemCommit && queued.ItemID == "commit-1":
			commitItem = queued
		case queued.ItemType == ItemFSObject && queued.ItemID == "fs-root":
			fsObjectItem = queued
		}
	}
	if commitItem.ItemID == "" || fsObjectItem.ItemID == "" {
		t.Fatalf("expected commit and fs_object children after cascade, got commit=%q fs_object=%q", commitItem.ItemID, fsObjectItem.ItemID)
	}

	if err := w.processCommit(commitItem); err != nil {
		t.Fatalf("processCommit with stale lock after hard delete failed: %v", err)
	}
	if store.GetCommitRecord(libID, "commit-1") != nil {
		t.Fatal("commit should be deleted after hard delete even if lock row lingers")
	}

	if err := w.processFSObject(context.Background(), fsObjectItem); err != nil {
		t.Fatalf("processFSObject with stale lock after hard delete failed: %v", err)
	}
	if store.GetFSObj(libID, "fs-root") != nil {
		t.Fatal("fs_object should be deleted after hard delete even if lock row lingers")
	}
	if got := store.BlockReferenceCount(orgID, "blk-a"); got != 0 {
		t.Fatalf("block references after stale-lock hard-delete child processing = %d, want 0", got)
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

func TestWorker_ProcessOrgCascade_UserCleanupFailureDoesNotHardDeleteUserOrOrg(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	userID := uuid.New()
	deletedAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)

	store.AddDeletedOrg(orgID, "Doomed Corp", deletedAt)
	store.AddUser(orgID, userID, "alice@doomed.com")
	store.deleteAPIKeysByUserErr = errors.New("api key cleanup unavailable")

	item := QueueItem{OrgID: orgID, QueuedAt: deletedAt, IdentityAt: deletedAt, ItemType: ItemOrgCascade, ItemID: orgID.String()}
	err := w.processOrgCascade(context.Background(), item)
	if err == nil {
		t.Fatal("expected user cleanup failure")
	}
	if !store.HasUser(orgID, userID) {
		t.Fatal("user should remain for retry when org cascade user cleanup fails")
	}
	if !store.HasOrg(orgID) {
		t.Fatal("org should remain for retry when user cleanup fails")
	}
	if len(store.AuditLogEntries()) != 0 {
		t.Fatal("org cascade audit should not be written before successful hard delete")
	}
}

func TestStore_HardDeleteOrg_DoesNotEnterPurgingWhenChildrenRemain(t *testing.T) {
	store := NewMockStore()

	orgID := uuid.New()
	userID := uuid.New()
	deletedAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)

	store.AddDeletedOrg(orgID, "Blocked Corp", deletedAt)
	store.AddUser(orgID, userID, "alice@blocked.com")

	err := store.HardDeleteOrg(orgID)
	if err == nil {
		t.Fatal("expected hard delete to fail while live users remain")
	}
	if store.orgStatus[orgID] != "deleted" {
		t.Fatalf("org status after failed wrapper hard delete = %q, want deleted", store.orgStatus[orgID])
	}
	if !store.HasUser(orgID, userID) {
		t.Fatal("live user should remain after failed wrapper hard delete")
	}
	leaseToken := uuid.New()
	acquired, err := store.AcquireOrgHardDeleteLock(orgID, leaseToken)
	if err != nil {
		t.Fatalf("reacquire org lock: %v", err)
	}
	if !acquired {
		t.Fatal("org hard-delete lock should be released after failed wrapper hard delete")
	}
	if err := store.ReleaseOrgHardDeleteLock(orgID, leaseToken); err != nil {
		t.Fatalf("release reacquired org lock: %v", err)
	}
}

func TestWorker_ProcessOrgCascade_RestoreBetweenChecksSkipsDelete(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	userID := uuid.New()
	groupID := uuid.New()
	libID := uuid.New()
	deletedAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)

	store.AddDeletedOrg(orgID, "Restorable Corp", deletedAt)
	store.AddUser(orgID, userID, "alice@restorable.com")
	store.AddGroupForOrg(orgID, groupID)
	store.AddLibrary(orgID, libID, "hot")
	store.beginOrgPurgeHook = func(lockedOrgID uuid.UUID) {
		store.mu.Lock()
		defer store.mu.Unlock()
		store.orgStatus[lockedOrgID] = "active"
		delete(store.orgDeletedAt, lockedOrgID)
	}

	item := QueueItem{OrgID: orgID, QueuedAt: deletedAt, IdentityAt: deletedAt, ItemType: ItemOrgCascade, ItemID: orgID.String()}
	err := w.processOrgCascade(context.Background(), item)
	if err != nil {
		t.Fatalf("expected stale skip after restore, got error: %v", err)
	}
	if !store.HasOrg(orgID) {
		t.Fatal("org should remain after restore wins the race")
	}
	if !store.HasUser(orgID, userID) {
		t.Fatal("user should remain after restore wins the race")
	}
	if !store.HasGroup(orgID, groupID) {
		t.Fatal("group should remain after restore wins the race")
	}
	if _, err := store.GetLibraryStorageClass(orgID, libID); err != nil {
		t.Fatalf("library should remain after restore wins the race: %v", err)
	}
	if len(store.AuditLogEntries()) != 0 {
		t.Fatal("org cascade audit should not be written when restore wins the race")
	}
	leaseToken := uuid.New()
	acquired, err := store.AcquireOrgHardDeleteLock(orgID, leaseToken)
	if err != nil {
		t.Fatalf("reacquire org lock: %v", err)
	}
	if !acquired {
		t.Fatal("org hard-delete lock should be released after stale skip")
	}
	if err := store.ReleaseOrgHardDeleteLock(orgID, leaseToken); err != nil {
		t.Fatalf("release reacquired org lock: %v", err)
	}
}

func TestWorker_ProcessOrgCascade_RetryWhilePurgingContinues(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	userID := uuid.New()
	deletedAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)

	store.AddDeletedOrg(orgID, "Purging Corp", deletedAt)
	store.AddUser(orgID, userID, "alice@purging.com")
	store.orgStatus[orgID] = "purging"

	item := QueueItem{OrgID: orgID, QueuedAt: deletedAt, IdentityAt: deletedAt, ItemType: ItemOrgCascade, ItemID: orgID.String()}
	err := w.processOrgCascade(context.Background(), item)
	if err != nil {
		t.Fatalf("retry on purging org should continue: %v", err)
	}
	if store.HasOrg(orgID) {
		t.Fatal("org should be hard-deleted when retry resumes a purging cascade")
	}
	if store.HasUser(orgID, userID) {
		t.Fatal("user should be hard-deleted when retry resumes a purging cascade")
	}
}

func TestWorker_ProcessOrgCascade_LibraryLockContentionPostponesWithoutRetryBurn(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	libID := uuid.New()
	deletedAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)
	oldToken := uuid.New()

	store.AddDeletedOrg(orgID, "Locked Corp", deletedAt)
	store.AddDeletedLibrary(orgID, libID, "hot", deletedAt)
	if acquired, err := store.AcquireLibraryHardDeleteLock(libID, oldToken); err != nil || !acquired {
		t.Fatalf("seed library hard-delete lock acquired=%v err=%v", acquired, err)
	}
	if err := store.EnqueueItem(orgID, deletedAt, ItemOrgCascade, orgID.String(), uuid.Nil, "", 4); err != nil {
		t.Fatalf("enqueue org cascade failed: %v", err)
	}

	n, err := w.ProcessOnce(context.Background())
	if err != nil {
		t.Fatalf("ProcessOnce failed: %v", err)
	}
	if n != 0 {
		t.Fatalf("lock-contended org cascade should not complete, got %d", n)
	}
	if !store.HasOrg(orgID) {
		t.Fatal("org should remain while a library hard-delete lock is active")
	}
	if _, err := store.GetLibraryStorageClass(orgID, libID); err != nil {
		t.Fatalf("library should remain while a library hard-delete lock is active: %v", err)
	}
	if deletedMarker, err := store.GetLibraryDeletedAt(libID); err != nil || deletedMarker == nil {
		t.Fatalf("deleted library marker should remain while org cascade is postponed: marker=%v err=%v", deletedMarker, err)
	}
	items := store.QueueItems(orgID)
	if len(items) != 1 {
		t.Fatalf("expected one postponed queue item, got %d", len(items))
	}
	if items[0].RetryCount != 4 {
		t.Fatalf("retry count after org-cascade library lock contention = %d, want 4", items[0].RetryCount)
	}
	if failed := store.FailedItems(orgID); len(failed) != 0 {
		t.Fatalf("lock contention should not move org cascade to DLQ, got %d failed items", len(failed))
	}
}

func TestWorker_ProcessOrgCascade_RetriesAfterLibraryLockReleases(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	libID := uuid.New()
	deletedAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)
	oldToken := uuid.New()

	store.AddDeletedOrg(orgID, "Retry Corp", deletedAt)
	store.AddDeletedLibrary(orgID, libID, "hot", deletedAt)
	if acquired, err := store.AcquireLibraryHardDeleteLock(libID, oldToken); err != nil || !acquired {
		t.Fatalf("seed library hard-delete lock acquired=%v err=%v", acquired, err)
	}
	if err := store.EnqueueItem(orgID, deletedAt, ItemOrgCascade, orgID.String(), uuid.Nil, "", 4); err != nil {
		t.Fatalf("enqueue org cascade failed: %v", err)
	}

	if n, err := w.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("first ProcessOnce failed: %v", err)
	} else if n != 0 {
		t.Fatalf("lock-contended org cascade should not complete on first pass, got %d", n)
	}
	if err := store.ReleaseLibraryHardDeleteLock(libID, oldToken); err != nil {
		t.Fatalf("release seeded library hard-delete lock: %v", err)
	}

	if n, err := w.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("second ProcessOnce failed: %v", err)
	} else if n != 1 {
		t.Fatalf("expected retried org cascade to complete once lock is released, got %d", n)
	}
	if store.HasOrg(orgID) {
		t.Fatal("org should be hard-deleted after the retried org cascade acquires the library lock")
	}
	if _, err := store.GetLibraryStorageClass(orgID, libID); err == nil {
		t.Fatal("library should be hard-deleted after the retried org cascade acquires the library lock")
	}
}

func TestWorker_ProcessOrgCascade_FenceFailsClosedWhenLibraryLockLost(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	libID := uuid.New()
	deletedAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)

	store.AddDeletedOrg(orgID, "Fence Corp", deletedAt)
	store.AddDeletedLibrary(orgID, libID, "hot", deletedAt)
	store.forceRenewLibraryLockNotOwned = true

	item := QueueItem{OrgID: orgID, QueuedAt: deletedAt, IdentityAt: deletedAt, ItemType: ItemOrgCascade, ItemID: orgID.String()}
	if err := w.processOrgCascade(context.Background(), item); err == nil {
		t.Fatal("expected a fail-closed error when org cascade loses the per-library lock before hard delete")
	}
	if !store.HasOrg(orgID) {
		t.Fatal("org must NOT be hard-deleted when the per-library fence loses the lock")
	}
	if _, err := store.GetLibraryStorageClass(orgID, libID); err != nil {
		t.Fatalf("library must NOT be hard-deleted when the per-library fence loses the lock: %v", err)
	}
	for _, entry := range store.AuditLogEntries() {
		if entry.Action == "gc_library_cascade_deleted" || entry.Action == "gc_org_cascade_deleted" {
			t.Fatalf("hard-delete audit %q should not be written before the per-library fence succeeds", entry.Action)
		}
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
	store.SeedQueueItemForTest(orgID, deletedAt, ItemLibraryCascade, libID.String(), uuid.Nil, "hot", 0)

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

func TestWorker_RemoveFSObjectBlockReferences_ReturnsOnlyZeroTransitions(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	libID := uuid.New()
	// hits-zero is referenced only by fs-obj; stays-positive is also referenced by
	// another fs_object, so removing fs-obj's reference leaves it alive.
	store.AddBlock(orgID, "hits-zero", "hot", 0)
	store.AddFSObjectReferenceForTest(orgID, "hits-zero", libID, "fs-obj")
	store.AddBlock(orgID, "stays-positive", "hot", 0)
	store.AddFSObjectReferenceForTest(orgID, "stays-positive", libID, "fs-obj")
	store.AddFSObjectReferenceForTest(orgID, "stays-positive", libID, "fs-other")

	zeroRef, err := w.removeFSObjectBlockReferences(orgID, libID, "", "fs-obj", []string{"hits-zero", "stays-positive"}, func() error { return nil })
	if err != nil {
		t.Fatalf("removeFSObjectBlockReferences failed: %v", err)
	}
	if len(zeroRef) != 1 || zeroRef[0] != "hits-zero" {
		t.Fatalf("zeroRef = %#v, want only hits-zero", zeroRef)
	}

	if got := store.BlockReferenceCount(orgID, "hits-zero"); got != 0 {
		t.Fatalf("hits-zero references = %d, want 0", got)
	}
	if got := store.BlockReferenceCount(orgID, "stays-positive"); got != 1 {
		t.Fatalf("stays-positive references = %d, want 1", got)
	}
}

func TestWorker_RemoveFSObjectBlockReferences_SoftDeletedLibraryUsesStoredRepresentation(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	libID := uuid.New()
	externalBlockID := "0123456789abcdef0123456789abcdef01234567"
	internalBlockID := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	encRep := db.EncryptedLibraryBlockRepresentationID(libID.String())

	store.AddLibrary(orgID, libID, "hot")
	store.mu.Lock()
	store.libraries[libID].Encrypted = true
	store.libraries[libID].BlockRepresentationID = encRep
	store.mu.Unlock()
	store.AddBlock(orgID, internalBlockID, "hot", 0)
	store.AddBlockMappingForRepresentation(orgID, encRep, externalBlockID, internalBlockID)
	store.AddFSObjectReferenceForTest(orgID, internalBlockID, libID, "fs-soft")
	if err := store.SoftDeleteLibrary(orgID, libID, uuid.Nil); err != nil {
		t.Fatalf("SoftDeleteLibrary failed: %v", err)
	}

	zeroRef, err := w.removeFSObjectBlockReferences(orgID, libID, "", "fs-soft", []string{externalBlockID}, func() error { return nil })
	if err != nil {
		t.Fatalf("removeFSObjectBlockReferences failed: %v", err)
	}
	if len(zeroRef) != 1 || zeroRef[0] != internalBlockID {
		t.Fatalf("zeroRef = %#v, want only %s", zeroRef, internalBlockID)
	}
	if got := store.BlockReferenceCount(orgID, internalBlockID); got != 0 {
		t.Fatalf("internal block references = %d, want 0", got)
	}
}

func TestWorker_ProcessFSObject_HardDeletedLibraryUsesQueuedRepresentation(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	libID := uuid.New()
	externalBlockID := "89abcdef0123456789abcdef0123456789abcdef"
	internalBlockID := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	encRep := db.EncryptedLibraryBlockRepresentationID(libID.String())
	queuedAt := time.Now().Add(-2 * time.Hour).UTC()

	store.AddLibrary(orgID, libID, "hot")
	store.mu.Lock()
	store.libraries[libID].Encrypted = true
	store.libraries[libID].BlockRepresentationID = encRep
	store.mu.Unlock()
	store.AddBlock(orgID, internalBlockID, "hot", 0)
	store.AddBlockMappingForRepresentation(orgID, encRep, externalBlockID, internalBlockID)
	store.AddFSObjectReferenceForTest(orgID, internalBlockID, libID, "fs-hard")
	store.AddFSObject(libID, "fs-hard", "file", []string{externalBlockID})
	if err := store.SoftDeleteLibrary(orgID, libID, uuid.Nil); err != nil {
		t.Fatalf("SoftDeleteLibrary failed: %v", err)
	}
	if err := store.HardDeleteLibrary(orgID, libID); err != nil {
		t.Fatalf("HardDeleteLibrary failed: %v", err)
	}

	err := w.processFSObject(context.Background(), QueueItem{
		OrgID:                 orgID,
		QueuedAt:              queuedAt,
		IdentityAt:            queuedAt,
		ItemType:              ItemFSObject,
		ItemID:                "fs-hard",
		LibraryID:             libID,
		BlockRepresentationID: encRep,
	})
	if err != nil {
		t.Fatalf("processFSObject failed: %v", err)
	}
	if got := store.BlockReferenceCount(orgID, internalBlockID); got != 0 {
		t.Fatalf("internal block references = %d, want 0", got)
	}
	if items := store.QueueItems(orgID); len(items) != 1 || items[0].ItemType != ItemBlock || items[0].ItemID != internalBlockID {
		t.Fatalf("queue items = %#v, want one ItemBlock for %s", items, internalBlockID)
	}
}

func TestWorker_ProcessLibraryCascade_EndToEndUsesQueuedRepresentationWithoutMetadata(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	libID := uuid.New()
	externalBlockID := "fedcba9876543210fedcba9876543210fedcba98"
	encryptedInternalID := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	plainInternalID := "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	encRep := db.EncryptedLibraryBlockRepresentationID(libID.String())

	store.AddOrganization(orgID)
	store.AddLibrary(orgID, libID, "hot")
	store.mu.Lock()
	store.libraries[libID].Encrypted = true
	store.libraries[libID].BlockRepresentationID = encRep
	store.mu.Unlock()
	store.AddCommit(libID, "commit-1", "fs-root")
	store.AddFSObject(libID, "fs-root", "file", []string{externalBlockID})
	store.AddBlock(orgID, encryptedInternalID, "hot", 0)
	store.AddBlock(orgID, plainInternalID, "hot", 0)
	store.AddBlockMappingForRepresentation(orgID, encRep, externalBlockID, encryptedInternalID)
	store.AddBlockMappingForRepresentation(orgID, db.PlainBlockRepresentationID, externalBlockID, plainInternalID)
	store.AddFSObjectReferenceForTest(orgID, encryptedInternalID, libID, "fs-root")
	store.AddBlockReferenceForTest(orgID, plainInternalID, "keep:plain")
	if err := store.SoftDeleteLibrary(orgID, libID, uuid.Nil); err != nil {
		t.Fatalf("SoftDeleteLibrary failed: %v", err)
	}
	deletedAt, err := store.GetLibraryDeletedAt(libID)
	if err != nil || deletedAt == nil {
		t.Fatalf("GetLibraryDeletedAt failed: %v deletedAt=%v", err, deletedAt)
	}

	parent := QueueItem{
		OrgID:                 orgID,
		QueuedAt:              *deletedAt,
		IdentityAt:            *deletedAt,
		ItemType:              ItemLibraryCascade,
		ItemID:                libID.String(),
		BlockRepresentationID: encRep,
		StorageClass:          "hot",
	}
	if err := w.processLibraryCascade(context.Background(), parent); err != nil {
		t.Fatalf("processLibraryCascade failed: %v", err)
	}
	if exists, err := store.LibraryExists(libID); err != nil {
		t.Fatalf("LibraryExists failed: %v", err)
	} else if exists {
		t.Fatal("library should be hard-deleted after cascade")
	}

	var commitItem QueueItem
	var fsObjectItem QueueItem
	for _, item := range store.QueueItems(orgID) {
		switch {
		case item.ItemType == ItemCommit && item.ItemID == "commit-1":
			commitItem = item
		case item.ItemType == ItemFSObject && item.ItemID == "fs-root":
			fsObjectItem = item
		}
	}
	if commitItem.ItemID == "" || fsObjectItem.ItemID == "" {
		t.Fatalf("expected commit and fs_object children, got commit=%q fs_object=%q", commitItem.ItemID, fsObjectItem.ItemID)
	}
	if commitItem.BlockRepresentationID != encRep || fsObjectItem.BlockRepresentationID != encRep {
		t.Fatalf("children lost queued representation: commit=%q fs=%q want %q", commitItem.BlockRepresentationID, fsObjectItem.BlockRepresentationID, encRep)
	}

	if err := w.processCommit(commitItem); err != nil {
		t.Fatalf("processCommit failed: %v", err)
	}
	if err := w.processFSObject(context.Background(), fsObjectItem); err != nil {
		t.Fatalf("processFSObject failed: %v", err)
	}

	if got := store.BlockReferenceCount(orgID, encryptedInternalID); got != 0 {
		t.Fatalf("encrypted block references = %d, want 0", got)
	}
	if got := store.BlockReferenceCount(orgID, plainInternalID); got != 1 {
		t.Fatalf("plain sibling references = %d, want 1", got)
	}
	if !store.ForwardBlockMappingExistsForRepresentation(orgID, db.PlainBlockRepresentationID, externalBlockID) {
		t.Fatal("plain sibling mapping should remain untouched")
	}

	var blockItems []QueueItem
	for _, item := range store.QueueItems(orgID) {
		if item.ItemType == ItemBlock {
			blockItems = append(blockItems, item)
		}
	}
	if len(blockItems) != 1 || blockItems[0].ItemID != encryptedInternalID {
		t.Fatalf("block queue items = %#v, want only encrypted internal block %s", blockItems, encryptedInternalID)
	}
}
