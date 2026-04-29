package gc

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	"github.com/Sesame-Disk/sesamefs/internal/traffic"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestNewScanner(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)

	s := NewScanner(store, q, stats, config.GCConfig{})

	if s == nil {
		t.Fatal("NewScanner returned nil")
	}
}

func TestScanner_ScanOrphanedBlocks(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	candidateAt1 := time.Now().Add(-2 * time.Minute)
	candidateAt2 := time.Now().Add(-1 * time.Minute)
	store.AddBlockGCCandidate(orgID, "block-orphan-1", "hot", candidateAt1)
	store.AddBlockGCCandidate(orgID, "block-orphan-2", "cold", candidateAt2)

	ctx := context.Background()
	err := s.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("ScanOnce failed: %v", err)
	}

	// Should enqueue 2 orphaned blocks
	items := store.QueueItems(orgID)
	blockItems := 0
	for _, item := range items {
		if item.ItemType == ItemBlock {
			blockItems++
		}
	}
	if blockItems != 2 {
		t.Errorf("expected 2 orphaned blocks enqueued, got %d", blockItems)
	}
	for _, item := range items {
		if item.ItemID == "block-orphan-1" && !item.QueuedAt.Equal(candidateAt1) {
			t.Fatalf("block-orphan-1 queued_at = %v, want %v", item.QueuedAt, candidateAt1)
		}
		if item.ItemID == "block-orphan-2" && !item.QueuedAt.Equal(candidateAt2) {
			t.Fatalf("block-orphan-2 queued_at = %v, want %v", item.QueuedAt, candidateAt2)
		}
	}

	// Stats should be updated
	if stats.LastScanRun().IsZero() {
		t.Error("LastScanRun should be set after scan")
	}
}

func TestScanner_ScanOrphanedBlocks_SkipsAlreadyQueuedCandidate(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	candidateAt := time.Now().Add(-1 * time.Minute)
	store.AddBlockGCCandidate(orgID, "block-orphan", "hot", candidateAt)
	if err := store.EnqueueItem(orgID, candidateAt, ItemBlock, "block-orphan", uuid.Nil, "hot", 0); err != nil {
		t.Fatalf("failed to seed queue item: %v", err)
	}

	if err := s.ScanOnce(context.Background()); err != nil {
		t.Fatalf("ScanOnce failed: %v", err)
	}

	if got := len(store.QueueItems(orgID)); got != 1 {
		t.Errorf("expected scanner to avoid duplicate queue entries, got %d items", got)
	}
}

func TestScanner_ScanOrphanedBlocks_BackfillsMissingCandidateFromBlocksTable(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	store.AddOrganization(orgID)
	store.AddBlock(orgID, "block-zero-ref", "hot", 0)
	store.AddBlock(orgID, "block-still-live", "hot", 2)

	if err := s.ScanOnce(context.Background()); err != nil {
		t.Fatalf("ScanOnce failed: %v", err)
	}

	items := store.QueueItems(orgID)
	if len(items) != 1 {
		t.Fatalf("expected 1 queued item, got %d", len(items))
	}
	if items[0].ItemType != ItemBlock || items[0].ItemID != "block-zero-ref" {
		t.Fatalf("unexpected queued item: %+v", items[0])
	}

	candidates, err := store.ListBlockGCCandidates(orgID)
	if err != nil {
		t.Fatalf("ListBlockGCCandidates failed: %v", err)
	}
	if len(candidates) != 1 || candidates[0].BlockID != "block-zero-ref" {
		t.Fatalf("expected reconciled GC candidate for block-zero-ref, got %+v", candidates)
	}
}

func TestScanner_ScanOnce_ReconcilesPendingStorageCounters(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	ownerID := uuid.New()
	activeLibID := uuid.New()
	deletedLibID := uuid.New()

	store.AddOrganization(orgID)
	store.AddLibraryWithOwner(orgID, activeLibID, ownerID, "hot")
	store.AddLibraryWithOwner(orgID, deletedLibID, ownerID, "hot")
	store.mu.Lock()
	store.libraries[deletedLibID].DeletedAt = time.Now().Add(-time.Hour)
	store.mu.Unlock()

	store.AddStorageSnapshot(traffic.LibraryStorageScope(orgID.String(), activeLibID.String()), 100, 2)
	store.AddStorageSnapshot(traffic.LibraryStorageScope(orgID.String(), deletedLibID.String()), 50, 1)
	store.AddStorageSnapshot(traffic.PlatformStorageScope(), 999, 9)
	store.AddStorageSnapshot(traffic.OrganizationStorageScope(orgID.String()), 999, 9)
	store.AddStorageSnapshot(traffic.UserStorageScope(orgID.String(), ownerID.String()), 999, 9)

	store.AddPendingStorageCounterReconciliation(traffic.PlatformStorageScope(), uuid.Nil, uuid.Nil)
	store.AddPendingStorageCounterReconciliation(traffic.OrganizationStorageScope(orgID.String()), orgID, uuid.Nil)
	store.AddPendingStorageCounterReconciliation(traffic.UserStorageScope(orgID.String(), ownerID.String()), orgID, ownerID)

	if err := s.ScanOnce(context.Background()); err != nil {
		t.Fatalf("ScanOnce failed: %v", err)
	}

	if got := store.StorageSnapshot(traffic.PlatformStorageScope()); got.BytesUsed != 100 || got.FileCount != 2 {
		t.Fatalf("platform snapshot = %+v, want bytes=100 files=2", got)
	}
	if got := store.StorageSnapshot(traffic.OrganizationStorageScope(orgID.String())); got.BytesUsed != 100 || got.FileCount != 2 {
		t.Fatalf("org snapshot = %+v, want bytes=100 files=2", got)
	}
	if got := store.StorageSnapshot(traffic.UserStorageScope(orgID.String(), ownerID.String())); got.BytesUsed != 100 || got.FileCount != 2 {
		t.Fatalf("user snapshot = %+v, want bytes=100 files=2", got)
	}
	if got, err := store.ReconcilePendingStorageCounters(); err != nil || got != 0 {
		t.Fatalf("expected no remaining reconciliation work, got count=%d err=%v", got, err)
	}
	if store.QueueLen() != 0 {
		t.Fatalf("storage reconciliation should not enqueue GC items, got %d", store.QueueLen())
	}
	if stats.LastScanRun().IsZero() {
		t.Fatal("LastScanRun should be updated after reconciliation pass")
	}
}

func TestScanner_ScanExpiredShareLinks(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	store.AddOrganization(orgID)

	// 3 share links: expired, not expired, no expiry (zero time = permanent)
	store.AddShareLink("token-expired", orgID, time.Now().Add(-24*time.Hour))
	store.AddShareLink("token-active", orgID, time.Now().Add(24*time.Hour))
	store.AddShareLink("token-permanent", orgID, time.Time{})

	ctx := context.Background()
	err := s.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("ScanOnce failed: %v", err)
	}

	if store.QueueLen() != 0 {
		t.Fatalf("expired share links should be cleaned directly, got %d queued items", store.QueueLen())
	}
	if _, ok := store.shareLinks["token-expired"]; ok {
		t.Fatalf("expected expired share link to be deleted")
	}
	if _, ok := store.shareLinks["token-active"]; !ok {
		t.Fatalf("expected active share link to remain")
	}
	if _, ok := store.shareLinks["token-permanent"]; !ok {
		t.Fatalf("expected permanent share link to remain")
	}

	gotCursor, err := store.LoadGCStats(gcExpiredShareLinksCursorKey)
	if err != nil {
		t.Fatalf("LoadGCStats() failed: %v", err)
	}
	wantCursor := db.GCProjectionDateString(expiredShareLinksCursorDay(time.Now()))
	if gotCursor != wantCursor {
		t.Fatalf("share links cursor = %q, want %q", gotCursor, wantCursor)
	}
}

func TestScanner_ScanExpiredShareLinks_DeleteFailureKeepsCursorUnchanged(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	store.AddOrganization(orgID)
	store.AddShareLink("token-expired", orgID, time.Now().Add(-24*time.Hour))
	store.deleteExpiredShareLinkErr = fmt.Errorf("delete failed")
	beforeFailed := testutil.ToFloat64(metrics.GCScannerActionsTotal.WithLabelValues("expired_links", "failed"))
	previousCursor := db.GCProjectionDateString(expiredShareLinksCursorDay(time.Now().AddDate(0, 0, -1)))
	if err := store.SaveGCStats(gcExpiredShareLinksCursorKey, previousCursor); err != nil {
		t.Fatalf("SaveGCStats() failed: %v", err)
	}

	cleaned, err := s.scanExpiredShareLinks(context.Background())
	if err == nil {
		t.Fatal("scanExpiredShareLinks() error = nil, want non-nil")
	}
	if cleaned != 0 {
		t.Fatalf("scanExpiredShareLinks() cleaned = %d, want 0", cleaned)
	}
	if _, ok := store.shareLinks["token-expired"]; !ok {
		t.Fatal("expired share link should remain after delete failure")
	}
	gotCursor, err := store.LoadGCStats(gcExpiredShareLinksCursorKey)
	if err != nil {
		t.Fatalf("LoadGCStats() failed: %v", err)
	}
	if gotCursor != previousCursor {
		t.Fatalf("share links cursor = %q, want unchanged %q", gotCursor, previousCursor)
	}
	if store.QueueLen() != 0 {
		t.Fatalf("delete failure should not enqueue GC items, got %d", store.QueueLen())
	}
	afterFailed := testutil.ToFloat64(metrics.GCScannerActionsTotal.WithLabelValues("expired_links", "failed"))
	if afterFailed-beforeFailed != 1 {
		t.Fatalf("expired_links failed metric delta = %v, want 1", afterFailed-beforeFailed)
	}
}

func TestScanner_ScanOrphanedGroupShares(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	groupID := uuid.New()
	missingGroupID := uuid.New()
	liveLibraryID := uuid.New()
	orphanLibraryID := uuid.New()
	store.AddOrganization(orgID)
	store.AddLibrary(orgID, liveLibraryID, "hot")
	store.AddLibrary(orgID, orphanLibraryID, "hot")
	store.AddGroupForOrg(orgID, groupID)
	store.AddGroupShare(liveLibraryID, uuid.New(), groupID)
	orphanShareID := uuid.New()
	store.AddGroupShare(orphanLibraryID, orphanShareID, missingGroupID)

	ctx := context.Background()
	n, err := s.scanOrphanedGroupShares(ctx)
	if err != nil {
		t.Fatalf("scanOrphanedGroupShares failed: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 orphaned group share cleaned, got %d", n)
	}

	store.mu.RLock()
	defer store.mu.RUnlock()
	if _, ok := store.shares[fmt.Sprintf("%s:%s", orphanLibraryID, orphanShareID)]; ok {
		t.Fatalf("orphaned group share still present after scanner cleanup")
	}
	for _, share := range store.shares {
		if share.LibraryID == liveLibraryID && share.SharedTo == groupID {
			return
		}
	}
	t.Fatalf("live group share was incorrectly removed")
}

func TestScanner_ScanOrphanedCommits(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	store.AddOrganization(orgID)

	libA := uuid.New()
	libB := uuid.New()

	// Library A exists with 2 commits
	store.AddLibrary(orgID, libA, "hot")
	store.AddCommit(libA, "commit-a1", "fs-root-1")
	store.AddCommit(libA, "commit-a2", "fs-root-2")

	// Library B does NOT exist (deleted), but has 1 orphaned commit
	store.AddLibrary(orgID, libB, "hot")
	store.AddCommit(libB, "commit-b1", "fs-root-3")

	// Now remove library B to simulate deletion
	store.mu.Lock()
	delete(store.libraries, libB)
	store.mu.Unlock()

	ctx := context.Background()
	err := s.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("ScanOnce failed: %v", err)
	}

	// Library B's commit should NOT be enqueued because FindOrgForLibrary will fail
	items := store.QueueItems(orgID)
	commitItems := 0
	for _, item := range items {
		if item.ItemType == ItemCommit {
			commitItems++
		}
	}
	if commitItems != 0 {
		t.Errorf("expected 0 orphaned commits enqueued (org lookup fails), got %d", commitItems)
	}
}

func TestScanner_ScanOrphanedCommits_WithOrgLookup(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	store.AddOrganization(orgID)

	libOrphaned := uuid.New()

	store.AddCommit(libOrphaned, "commit-orphan-1", "fs-root")

	ctx := context.Background()
	s.ScanOnce(ctx)

	if store.QueueLen() != 0 {
		t.Errorf("expected 0 items enqueued when org can't be found, got %d", store.QueueLen())
	}
}

func TestScanner_ScanOrphanedFSObjects(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	store.AddOrganization(orgID)

	libA := uuid.New()
	libB := uuid.New()

	store.AddLibrary(orgID, libA, "hot")
	store.AddFSObject(libA, "fs-a1", "file", []string{"blk-1"})

	store.AddLibrary(orgID, libB, "cold")
	store.AddFSObject(libB, "fs-b1", "file", []string{"blk-3"})

	ctx := context.Background()
	s.ScanOnce(ctx)

	// Both libraries exist, so no orphaned fs_objects
	fsItems := 0
	for _, item := range store.QueueItems(orgID) {
		if item.ItemType == ItemFSObject {
			fsItems++
		}
	}
	if fsItems != 0 {
		t.Errorf("expected 0 orphaned fs_objects (both libraries exist), got %d", fsItems)
	}
}

func TestScanner_ScanOnce_EmptyDB(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	ctx := context.Background()
	err := s.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("ScanOnce failed on empty DB: %v", err)
	}

	if store.QueueLen() != 0 {
		t.Errorf("expected 0 items enqueued on empty DB, got %d", store.QueueLen())
	}

	if stats.LastScanRun().IsZero() {
		t.Error("LastScanRun should be set even on empty scan")
	}
}

func TestScanner_ScanOnce_FullPipeline(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	store.AddOrganization(orgID)

	// Phase 1: orphaned blocks
	store.AddBlock(orgID, "alive-blk", "hot", 5)
	orphanCandidateAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)
	store.AddBlockGCCandidate(orgID, "orphan-blk", "hot", orphanCandidateAt)

	// Phase 2: expired share links
	store.AddShareLink("expired-token", orgID, time.Now().Add(-1*time.Hour))
	store.AddShareLink("active-token", orgID, time.Now().Add(1*time.Hour))

	// Phase 3+4: no orphaned commits/fs_objects (libraries exist)
	libID := uuid.New()
	store.AddLibrary(orgID, libID, "hot")
	store.AddCommit(libID, "commit-1", "fs-root")
	store.AddFSObject(libID, "fs-1", "file", []string{"alive-blk"})

	ctx := context.Background()
	err := s.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("ScanOnce failed: %v", err)
	}

	// Should enqueue only the orphaned block; expired share links are cleaned directly.
	items := store.QueueItems(orgID)
	if len(items) != 1 {
		t.Errorf("expected 1 item from full pipeline, got %d", len(items))
	}

	typeCount := make(map[ItemType]int)
	for _, item := range items {
		typeCount[item.ItemType]++
	}
	if typeCount[ItemBlock] != 1 {
		t.Errorf("expected 1 block item, got %d", typeCount[ItemBlock])
	}
	if _, ok := store.shareLinks["expired-token"]; ok {
		t.Errorf("expected expired share link to be cleaned during full pipeline scan")
	}
	for _, item := range items {
		if item.ItemType == ItemBlock && !item.QueuedAt.Equal(orphanCandidateAt) {
			t.Errorf("expected block queued_at %v, got %v", orphanCandidateAt, item.QueuedAt)
		}
	}
}

func TestScanner_ContextCancellation(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	for i := 0; i < 100; i++ {
		orgID := uuid.New()
		store.AddOrganization(orgID)
		store.AddBlock(orgID, "block-"+orgID.String(), "hot", 0)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := s.ScanOnce(ctx)
	_ = err
}

func TestScanner_ScanExpiredVersions_EnqueuesExpired(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	store.AddOrganization(orgID)
	libID := uuid.New()

	headID := "commit-head"
	parentID := "commit-parent"
	grandparentID := "commit-grandparent"

	store.AddLibraryWithTTL(orgID, libID, "hot", headID, 1)

	now := time.Now()
	old := now.Add(-48 * time.Hour)

	store.AddCommitWithDetails(libID, headID, "fs-1", parentID, old)
	store.AddCommitWithDetails(libID, parentID, "fs-2", grandparentID, old)
	store.AddCommitWithDetails(libID, grandparentID, "fs-3", "", old)

	store.AddCommitWithDetails(libID, "commit-expired-1", "fs-4", "", old)
	store.AddCommitWithDetails(libID, "commit-expired-2", "fs-5", "", old)
	store.AddCommitWithDetails(libID, "commit-recent", "fs-6", "", now)

	ctx := context.Background()
	err := s.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("ScanOnce failed: %v", err)
	}

	items := store.QueueItems(orgID)
	commitItems := 0
	for _, item := range items {
		if item.ItemType == ItemCommit {
			commitItems++
			if item.ItemID == headID || item.ItemID == parentID || item.ItemID == grandparentID {
				t.Errorf("HEAD chain commit %s should not be enqueued", item.ItemID)
			}
		}
	}
	if commitItems != 2 {
		t.Errorf("expected 2 expired commits enqueued, got %d", commitItems)
	}
}

func TestScanner_ScanExpiredVersions_PreservesHEADChain(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	store.AddOrganization(orgID)
	libID := uuid.New()

	headID := "head"
	parentID := "parent"

	store.AddLibraryWithTTL(orgID, libID, "hot", headID, 1)

	old := time.Now().Add(-72 * time.Hour)

	store.AddCommitWithDetails(libID, headID, "fs-1", parentID, old)
	store.AddCommitWithDetails(libID, parentID, "fs-2", "", old)

	ctx := context.Background()
	s.ScanOnce(ctx)

	items := store.QueueItems(orgID)
	commitItems := 0
	for _, item := range items {
		if item.ItemType == ItemCommit {
			commitItems++
		}
	}
	if commitItems != 0 {
		t.Errorf("expected 0 commits enqueued (all in HEAD chain), got %d", commitItems)
	}
}

func TestScanner_ScanExpiredVersions_SkipsNegativeTTL(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	store.AddOrganization(orgID)
	libID := uuid.New()

	store.AddLibraryWithTTL(orgID, libID, "hot", "head", -1)

	old := time.Now().Add(-720 * time.Hour)
	store.AddCommitWithDetails(libID, "head", "fs-1", "", old)
	store.AddCommitWithDetails(libID, "old-commit", "fs-2", "", old)

	ctx := context.Background()
	s.ScanOnce(ctx)

	items := store.QueueItems(orgID)
	commitItems := 0
	for _, item := range items {
		if item.ItemType == ItemCommit {
			commitItems++
		}
	}
	if commitItems != 0 {
		t.Errorf("expected 0 commits enqueued (TTL=-1 keeps all), got %d", commitItems)
	}
}

func TestScanner_ScanExpiredVersions_SkipsZeroTTL(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	store.AddOrganization(orgID)
	libID := uuid.New()

	store.AddLibraryWithTTL(orgID, libID, "hot", "head", 0)

	old := time.Now().Add(-720 * time.Hour)
	store.AddCommitWithDetails(libID, "head", "fs-1", "", old)
	store.AddCommitWithDetails(libID, "old-commit", "fs-2", "", old)

	ctx := context.Background()
	s.ScanOnce(ctx)

	items := store.QueueItems(orgID)
	commitItems := 0
	for _, item := range items {
		if item.ItemType == ItemCommit {
			commitItems++
		}
	}
	if commitItems != 0 {
		t.Errorf("expected 0 commits enqueued (TTL=0 no setting), got %d", commitItems)
	}
}

func TestScanner_ScanAutoDeleteExpiredObjects_Basic(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	store.AddOrganization(orgID)
	libID := uuid.New()

	headCommitID := "commit-head"

	store.AddLibraryWithAutoDelete(orgID, libID, "hot", headCommitID, 1)

	now := time.Now()
	store.AddCommitWithDetails(libID, headCommitID, "fs-root", "", now)

	store.AddFSObjectWithEntries(libID, "fs-root", "dir", nil, []string{"fs-file1"})
	store.AddFSObject(libID, "fs-file1", "file", []string{"blk-1"})

	store.AddFSObject(libID, "fs-orphan", "file", []string{"blk-2"})

	ctx := context.Background()
	err := s.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("ScanOnce failed: %v", err)
	}

	items := store.QueueItems(orgID)
	fsItems := 0
	for _, item := range items {
		if item.ItemType == ItemFSObject {
			fsItems++
			if item.ItemID != "fs-orphan" {
				t.Errorf("unexpected fs_object enqueued: %s", item.ItemID)
			}
		}
	}
	if fsItems != 1 {
		t.Errorf("expected 1 orphaned fs_object enqueued, got %d", fsItems)
	}
}

func TestScanner_ScanAutoDeleteExpiredObjects_PreservesHEADTree(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	store.AddOrganization(orgID)
	libID := uuid.New()

	headCommitID := "commit-head"

	store.AddLibraryWithAutoDelete(orgID, libID, "hot", headCommitID, 1)

	now := time.Now()
	store.AddCommitWithDetails(libID, headCommitID, "fs-root", "", now)

	store.AddFSObjectWithEntries(libID, "fs-root", "dir", nil, []string{"fs-subdir"})
	store.AddFSObjectWithEntries(libID, "fs-subdir", "dir", nil, []string{"fs-file"})
	store.AddFSObject(libID, "fs-file", "file", []string{"blk-1"})

	ctx := context.Background()
	err := s.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("ScanOnce failed: %v", err)
	}

	items := store.QueueItems(orgID)
	fsItems := 0
	for _, item := range items {
		if item.ItemType == ItemFSObject {
			fsItems++
		}
	}
	if fsItems != 0 {
		t.Errorf("expected 0 fs_objects enqueued (all in HEAD tree), got %d", fsItems)
	}
}

func TestScanner_ScanAutoDeleteExpiredObjects_PreservesRecentCommits(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	store.AddOrganization(orgID)
	libID := uuid.New()

	headCommitID := "commit-head"
	recentCommitID := "commit-recent"

	store.AddLibraryWithAutoDelete(orgID, libID, "hot", headCommitID, 30)

	now := time.Now()
	fiveDaysAgo := now.Add(-5 * 24 * time.Hour)

	store.AddCommitWithDetails(libID, headCommitID, "fs-root-head", "", now)
	store.AddFSObjectWithEntries(libID, "fs-root-head", "dir", nil, []string{"fs-file-head"})
	store.AddFSObject(libID, "fs-file-head", "file", []string{"blk-1"})

	store.AddCommitWithDetails(libID, recentCommitID, "fs-root-recent", "", fiveDaysAgo)
	store.AddFSObjectWithEntries(libID, "fs-root-recent", "dir", nil, []string{"fs-file-recent"})
	store.AddFSObject(libID, "fs-file-recent", "file", []string{"blk-2"})

	ctx := context.Background()
	err := s.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("ScanOnce failed: %v", err)
	}

	items := store.QueueItems(orgID)
	fsItems := 0
	for _, item := range items {
		if item.ItemType == ItemFSObject {
			fsItems++
		}
	}
	if fsItems != 0 {
		t.Errorf("expected 0 fs_objects enqueued (recent commit within window), got %d", fsItems)
	}
}

func TestScanner_ScanAutoDeleteExpiredObjects_SkipsZeroAutoDelete(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	store.AddOrganization(orgID)
	libID := uuid.New()

	store.AddLibraryWithAutoDelete(orgID, libID, "hot", "commit-head", 0)

	now := time.Now()
	store.AddCommitWithDetails(libID, "commit-head", "fs-root", "", now)
	store.AddFSObject(libID, "fs-root", "dir", nil)
	store.AddFSObject(libID, "fs-orphan", "file", []string{"blk-1"})

	ctx := context.Background()
	err := s.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("ScanOnce failed: %v", err)
	}

	items := store.QueueItems(orgID)
	fsItems := 0
	for _, item := range items {
		if item.ItemType == ItemFSObject {
			fsItems++
		}
	}
	if fsItems != 0 {
		t.Errorf("expected 0 fs_objects enqueued (auto_delete disabled), got %d", fsItems)
	}
}

func TestScanner_ScanAutoDeleteExpiredObjects_NestedDirs(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	store.AddOrganization(orgID)
	libID := uuid.New()

	headCommitID := "commit-head"

	store.AddLibraryWithAutoDelete(orgID, libID, "hot", headCommitID, 1)

	now := time.Now()
	store.AddCommitWithDetails(libID, headCommitID, "fs-root", "", now)

	store.AddFSObjectWithEntries(libID, "fs-root", "dir", nil, []string{"fs-dir1"})
	store.AddFSObjectWithEntries(libID, "fs-dir1", "dir", nil, []string{"fs-dir2"})
	store.AddFSObjectWithEntries(libID, "fs-dir2", "dir", nil, []string{"fs-file"})
	store.AddFSObject(libID, "fs-file", "file", []string{"blk-1"})

	store.AddFSObject(libID, "fs-orphan-1", "file", []string{"blk-2"})
	store.AddFSObject(libID, "fs-orphan-2", "dir", nil)

	ctx := context.Background()
	err := s.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("ScanOnce failed: %v", err)
	}

	items := store.QueueItems(orgID)
	fsItems := 0
	enqueuedIDs := make(map[string]bool)
	for _, item := range items {
		if item.ItemType == ItemFSObject {
			fsItems++
			enqueuedIDs[item.ItemID] = true
		}
	}
	if fsItems != 2 {
		t.Errorf("expected 2 orphaned fs_objects enqueued, got %d", fsItems)
	}
	if !enqueuedIDs["fs-orphan-1"] {
		t.Error("expected fs-orphan-1 to be enqueued")
	}
	if !enqueuedIDs["fs-orphan-2"] {
		t.Error("expected fs-orphan-2 to be enqueued")
	}
	for _, treeID := range []string{"fs-root", "fs-dir1", "fs-dir2", "fs-file"} {
		if enqueuedIDs[treeID] {
			t.Errorf("tree object %s should not be enqueued", treeID)
		}
	}
}

func TestScanner_ScanExpiredShares(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	store.AddOrganization(orgID)
	libID := uuid.New()

	// Add expired and active shares
	store.AddShare(libID, uuid.New(), uuid.New(), time.Now().Add(-24*time.Hour)) // expired
	store.AddShare(libID, uuid.New(), uuid.New(), time.Now().Add(24*time.Hour))  // active
	store.AddShare(libID, uuid.New(), uuid.New(), time.Time{})                   // permanent

	ctx := context.Background()
	s.scanExpiredShares(ctx)

	// One expired share should be deleted (directly, not via queue)
	shares, _ := store.ListExpiredShares()
	if len(shares) != 0 {
		t.Errorf("expected 0 expired shares after cleanup, got %d", len(shares))
	}

	gotCursor, err := store.LoadGCStats(gcExpiredSharesCursorKey)
	if err != nil {
		t.Fatalf("LoadGCStats() failed: %v", err)
	}
	wantCursor := db.GCProjectionDateString(expiredSharesCursorDay(time.Now()))
	if gotCursor != wantCursor {
		t.Fatalf("expired shares cursor = %q, want %q", gotCursor, wantCursor)
	}
}

func TestScanner_ScanExpiredShares_DeleteFailureKeepsCursorUnchanged(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	libID := uuid.New()
	shareID := uuid.New()
	store.AddOrganization(orgID)
	store.AddLibrary(orgID, libID, "hot")
	store.AddShare(libID, shareID, uuid.New(), time.Now().Add(-24*time.Hour))
	store.deleteExpiredShareErr = fmt.Errorf("delete failed")
	beforeFailed := testutil.ToFloat64(metrics.GCScannerActionsTotal.WithLabelValues("expired_shares", "failed"))
	previousCursor := db.GCProjectionDateString(expiredSharesCursorDay(time.Now().AddDate(0, 0, -1)))
	if err := store.SaveGCStats(gcExpiredSharesCursorKey, previousCursor); err != nil {
		t.Fatalf("SaveGCStats() failed: %v", err)
	}

	cleaned, err := s.scanExpiredShares(context.Background())
	if err == nil {
		t.Fatal("scanExpiredShares() error = nil, want non-nil")
	}
	if cleaned != 0 {
		t.Fatalf("scanExpiredShares() cleaned = %d, want 0", cleaned)
	}
	if !store.HasShare(libID, shareID) {
		t.Fatal("expired share should remain after delete failure")
	}
	gotCursor, err := store.LoadGCStats(gcExpiredSharesCursorKey)
	if err != nil {
		t.Fatalf("LoadGCStats() failed: %v", err)
	}
	if gotCursor != previousCursor {
		t.Fatalf("expired shares cursor = %q, want unchanged %q", gotCursor, previousCursor)
	}
	afterFailed := testutil.ToFloat64(metrics.GCScannerActionsTotal.WithLabelValues("expired_shares", "failed"))
	if afterFailed-beforeFailed != 1 {
		t.Fatalf("expired_shares failed metric delta = %v, want 1", afterFailed-beforeFailed)
	}
}

func TestScanner_ScanExpiredRestoreJobs(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	store.AddOrganization(orgID)
	libID := uuid.New()

	// Add completed and active restore jobs
	store.AddRestoreJob(orgID, libID, uuid.New(), "completed", time.Now().Add(-24*time.Hour))
	store.AddRestoreJob(orgID, libID, uuid.New(), "failed", time.Now().Add(-24*time.Hour))
	store.AddRestoreJob(orgID, libID, uuid.New(), "pending", time.Now().Add(24*time.Hour)) // active

	ctx := context.Background()
	s.scanExpiredRestoreJobs(ctx)

	// 2 expired jobs should be deleted, 1 should remain
	jobs, _ := store.ListExpiredRestoreJobs()
	if len(jobs) != 0 {
		t.Errorf("expected 0 expired restore jobs after cleanup, got %d", len(jobs))
	}
}

func TestScanner_IdempotentEnqueue(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	store.AddOrganization(orgID)
	candidateAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)
	store.AddBlockGCCandidate(orgID, "orphan-blk", "hot", candidateAt)

	ctx := context.Background()

	s.ScanOnce(ctx)
	firstCount := store.QueueLen()

	s.ScanOnce(ctx)
	secondCount := store.QueueLen()

	if firstCount != 1 {
		t.Errorf("first scan should enqueue 1 item, got %d", firstCount)
	}
	if secondCount != 1 {
		t.Errorf("expected 1 item after second scan, got %d", secondCount)
	}
}

// === Phase 10: Expired Deleted Users ===

func TestScanner_ScanExpiredDeletedUsers_EnqueuesExpired(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{UserGraceDays: 7})

	orgID := uuid.New()
	store.AddOrganization(orgID)

	// Expired user (deleted 10 days ago, grace = 7)
	expiredUser := uuid.New()
	store.AddDeletedUser(orgID, expiredUser, "expired@test.com", time.Now().AddDate(0, 0, -10))

	// Recent user (deleted 3 days ago, grace = 7 → NOT expired)
	recentUser := uuid.New()
	store.AddDeletedUser(orgID, recentUser, "recent@test.com", time.Now().AddDate(0, 0, -3))

	// Active user (not deleted)
	activeUser := uuid.New()
	store.AddUser(orgID, activeUser, "active@test.com")

	ctx := context.Background()
	n, err := s.scanExpiredDeletedUsers(ctx)
	if err != nil {
		t.Fatalf("scanExpiredDeletedUsers failed: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 expired user enqueued, got %d", n)
	}

	// Verify queue contains user_cascade for expired user only
	items := store.QueueItems(orgID)
	cascadeCount := 0
	for _, item := range items {
		if item.ItemType == ItemUserCascade {
			cascadeCount++
			if item.ItemID != expiredUser.String() {
				t.Errorf("expected expired user %s enqueued, got %s", expiredUser, item.ItemID)
			}
		}
	}
	if cascadeCount != 1 {
		t.Errorf("expected 1 user_cascade item, got %d", cascadeCount)
	}
}

func TestScanner_ScanExpiredDeletedUsers_NoneExpired(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{UserGraceDays: 7})

	orgID := uuid.New()
	store.AddOrganization(orgID)

	// Only recent deleted user (within grace)
	store.AddDeletedUser(orgID, uuid.New(), "recent@test.com", time.Now().AddDate(0, 0, -2))

	ctx := context.Background()
	n, err := s.scanExpiredDeletedUsers(ctx)
	if err != nil {
		t.Fatalf("scanExpiredDeletedUsers failed: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 expired users enqueued, got %d", n)
	}
	if store.QueueLen() != 0 {
		t.Errorf("expected empty queue, got %d items", store.QueueLen())
	}
}

func TestScanner_ScanExpiredDeletedUsers_DeduplicatesAcrossRuns(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{UserGraceDays: 7})

	orgID := uuid.New()
	store.AddOrganization(orgID)
	deletedAt := time.Now().AddDate(0, 0, -10)
	store.AddDeletedUser(orgID, uuid.New(), "expired@test.com", deletedAt)

	if n, err := s.scanExpiredDeletedUsers(context.Background()); err != nil || n != 1 {
		t.Fatalf("first scanExpiredDeletedUsers = (%d, %v), want (1, nil)", n, err)
	}
	if n, err := s.scanExpiredDeletedUsers(context.Background()); err != nil || n != 0 {
		t.Fatalf("second scanExpiredDeletedUsers = (%d, %v), want (0, nil)", n, err)
	}
}

func TestScanner_ScanExpiredDeletedUsers_AdvancesCursorOnSuccess(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{UserGraceDays: 7})

	orgID := uuid.New()
	store.AddOrganization(orgID)
	store.AddDeletedUser(orgID, uuid.New(), "expired@test.com", time.Now().AddDate(0, 0, -10))

	enqueued, err := s.scanExpiredDeletedUsers(context.Background())
	if err != nil {
		t.Fatalf("scanExpiredDeletedUsers() failed: %v", err)
	}
	if enqueued != 1 {
		t.Fatalf("scanExpiredDeletedUsers() enqueued = %d, want 1", enqueued)
	}
	gotCursor, err := store.LoadGCStats(gcDeletedUsersCursorKey)
	if err != nil {
		t.Fatalf("LoadGCStats() failed: %v", err)
	}
	wantCursor := db.GCProjectionDateString(deletedUsersCursorDay(time.Now(), 7))
	if gotCursor != wantCursor {
		t.Fatalf("deleted users cursor = %q, want %q", gotCursor, wantCursor)
	}
}

func TestScanner_ScanExpiredDeletedUsers_EnqueueFailureKeepsCursorUnchanged(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{UserGraceDays: 7})

	orgID := uuid.New()
	store.AddOrganization(orgID)
	deletedAt := time.Now().AddDate(0, 0, -10)
	store.AddDeletedUser(orgID, uuid.New(), "expired@test.com", deletedAt)
	store.enqueueBatchErr = fmt.Errorf("enqueue failed")
	previousCursor := db.GCProjectionDateString(deletedUsersCursorDay(time.Now().AddDate(0, 0, -1), 7))
	if err := store.SaveGCStats(gcDeletedUsersCursorKey, previousCursor); err != nil {
		t.Fatalf("SaveGCStats() failed: %v", err)
	}

	enqueued, err := s.scanExpiredDeletedUsers(context.Background())
	if err == nil {
		t.Fatal("scanExpiredDeletedUsers() error = nil, want non-nil")
	}
	if enqueued != 0 {
		t.Fatalf("scanExpiredDeletedUsers() enqueued = %d, want 0", enqueued)
	}
	if store.QueueLen() != 0 {
		t.Fatalf("queue should remain empty after enqueue failure, got %d items", store.QueueLen())
	}
	gotCursor, err := store.LoadGCStats(gcDeletedUsersCursorKey)
	if err != nil {
		t.Fatalf("LoadGCStats() failed: %v", err)
	}
	if gotCursor != previousCursor {
		t.Fatalf("deleted users cursor = %q, want unchanged %q", gotCursor, previousCursor)
	}
}

// === Phase 11: Expired Deleted Libraries ===

func TestScanner_ScanExpiredDeletedLibraries_EnqueuesExpired(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{TrashRetentionDays: 30})

	orgID := uuid.New()
	store.AddOrganization(orgID)

	// Expired library (deleted 45 days ago, retention = 30)
	expiredLib := uuid.New()
	store.AddDeletedLibrary(orgID, expiredLib, "hot", time.Now().AddDate(0, 0, -45))

	// Recent library (deleted 10 days ago, retention = 30 → NOT expired)
	recentLib := uuid.New()
	store.AddDeletedLibrary(orgID, recentLib, "cold", time.Now().AddDate(0, 0, -10))

	ctx := context.Background()
	n, err := s.scanExpiredDeletedLibraries(ctx)
	if err != nil {
		t.Fatalf("scanExpiredDeletedLibraries failed: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 expired library enqueued, got %d", n)
	}

	// Verify queue contains library_cascade with correct storage class
	items := store.QueueItems(orgID)
	cascadeCount := 0
	for _, item := range items {
		if item.ItemType == ItemLibraryCascade {
			cascadeCount++
			if item.ItemID != expiredLib.String() {
				t.Errorf("expected expired lib %s enqueued, got %s", expiredLib, item.ItemID)
			}
			if item.StorageClass != "hot" {
				t.Errorf("expected storage class 'hot', got %s", item.StorageClass)
			}
		}
	}
	if cascadeCount != 1 {
		t.Errorf("expected 1 library_cascade item, got %d", cascadeCount)
	}
}

func TestScanner_ScanExpiredDeletedLibraries_UsesDeletedMarkerStorageClassWithoutLiveLibrary(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{TrashRetentionDays: 30})

	orgID := uuid.New()
	store.AddOrganization(orgID)

	expiredLib := uuid.New()
	store.AddDeletedLibrary(orgID, expiredLib, "archive", time.Now().AddDate(0, 0, -45))

	store.mu.Lock()
	delete(store.libraries, expiredLib)
	store.mu.Unlock()

	n, err := s.scanExpiredDeletedLibraries(context.Background())
	if err != nil {
		t.Fatalf("scanExpiredDeletedLibraries failed: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 expired library enqueued, got %d", n)
	}

	items := store.QueueItems(orgID)
	if len(items) != 1 {
		t.Fatalf("expected 1 queued item, got %d", len(items))
	}
	if items[0].StorageClass != "archive" {
		t.Fatalf("expected queue item storage class archive, got %q", items[0].StorageClass)
	}
}

func TestScanner_ScanExpiredDeletedLibraries_NoneExpired(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{TrashRetentionDays: 30})

	orgID := uuid.New()
	store.AddOrganization(orgID)

	// Only recently deleted library
	store.AddDeletedLibrary(orgID, uuid.New(), "hot", time.Now().AddDate(0, 0, -5))

	ctx := context.Background()
	n, err := s.scanExpiredDeletedLibraries(ctx)
	if err != nil {
		t.Fatalf("scanExpiredDeletedLibraries failed: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 expired libraries enqueued, got %d", n)
	}
}

func TestScanner_ScanExpiredDeletedLibraries_Multiple(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{TrashRetentionDays: 30})

	orgID := uuid.New()
	store.AddOrganization(orgID)

	// 3 expired libraries
	for i := 0; i < 3; i++ {
		store.AddDeletedLibrary(orgID, uuid.New(), "hot", time.Now().AddDate(0, 0, -60))
	}

	ctx := context.Background()
	n, err := s.scanExpiredDeletedLibraries(ctx)
	if err != nil {
		t.Fatalf("scanExpiredDeletedLibraries failed: %v", err)
	}
	if n != 3 {
		t.Errorf("expected 3 expired libraries enqueued, got %d", n)
	}
}

func TestScanner_ScanExpiredDeletedLibraries_DeduplicatesAcrossRuns(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{TrashRetentionDays: 30})

	orgID := uuid.New()
	store.AddOrganization(orgID)
	deletedAt := time.Now().AddDate(0, 0, -45)
	store.AddDeletedLibrary(orgID, uuid.New(), "hot", deletedAt)

	if n, err := s.scanExpiredDeletedLibraries(context.Background()); err != nil || n != 1 {
		t.Fatalf("first scanExpiredDeletedLibraries = (%d, %v), want (1, nil)", n, err)
	}
	if n, err := s.scanExpiredDeletedLibraries(context.Background()); err != nil || n != 0 {
		t.Fatalf("second scanExpiredDeletedLibraries = (%d, %v), want (0, nil)", n, err)
	}
}

// === Phase 12: Expired Deleted Orgs ===

func TestScanner_ScanExpiredDeletedOrgs_EnqueuesExpired(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{OrgGraceDays: 30})

	// Expired org (deleted 45 days ago, grace = 30)
	expiredOrg := uuid.New()
	store.AddDeletedOrg(expiredOrg, "Expired Corp", time.Now().AddDate(0, 0, -45))

	// Recent org (deleted 10 days ago, grace = 30 → NOT expired)
	recentOrg := uuid.New()
	store.AddDeletedOrg(recentOrg, "Recent Corp", time.Now().AddDate(0, 0, -10))

	ctx := context.Background()
	n, err := s.scanExpiredDeletedOrgs(ctx)
	if err != nil {
		t.Fatalf("scanExpiredDeletedOrgs failed: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 expired org enqueued, got %d", n)
	}

	// Verify queue: org_cascade for expired org only
	items := store.QueueItems(expiredOrg)
	cascadeCount := 0
	for _, item := range items {
		if item.ItemType == ItemOrgCascade {
			cascadeCount++
			if item.ItemID != expiredOrg.String() {
				t.Errorf("expected expired org %s enqueued, got %s", expiredOrg, item.ItemID)
			}
		}
	}
	if cascadeCount != 1 {
		t.Errorf("expected 1 org_cascade item, got %d", cascadeCount)
	}

	// Recent org should have nothing enqueued
	recentItems := store.QueueItems(recentOrg)
	if len(recentItems) != 0 {
		t.Errorf("expected 0 items for recent org, got %d", len(recentItems))
	}
}

func TestScanner_ScanExpiredDeletedOrgs_NoneExpired(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{OrgGraceDays: 30})

	// Only recently deleted org
	store.AddDeletedOrg(uuid.New(), "Fresh Corp", time.Now().AddDate(0, 0, -5))

	ctx := context.Background()
	n, err := s.scanExpiredDeletedOrgs(ctx)
	if err != nil {
		t.Fatalf("scanExpiredDeletedOrgs failed: %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 expired orgs enqueued, got %d", n)
	}
}

func TestScanner_ScanExpiredDeletedOrgs_Multiple(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{OrgGraceDays: 30})

	// 3 expired orgs
	orgIDs := make([]uuid.UUID, 3)
	for i := 0; i < 3; i++ {
		orgIDs[i] = uuid.New()
		store.AddDeletedOrg(orgIDs[i], "Doomed Corp", time.Now().AddDate(0, 0, -60))
	}

	ctx := context.Background()
	n, err := s.scanExpiredDeletedOrgs(ctx)
	if err != nil {
		t.Fatalf("scanExpiredDeletedOrgs failed: %v", err)
	}
	if n != 3 {
		t.Errorf("expected 3 expired orgs enqueued, got %d", n)
	}
}

func TestScanner_ScanExpiredDeletedOrgs_DeduplicatesAcrossRuns(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{OrgGraceDays: 30})

	orgID := uuid.New()
	deletedAt := time.Now().AddDate(0, 0, -45)
	store.AddDeletedOrg(orgID, "Expired Corp", deletedAt)

	if n, err := s.scanExpiredDeletedOrgs(context.Background()); err != nil || n != 1 {
		t.Fatalf("first scanExpiredDeletedOrgs = (%d, %v), want (1, nil)", n, err)
	}
	if n, err := s.scanExpiredDeletedOrgs(context.Background()); err != nil || n != 0 {
		t.Fatalf("second scanExpiredDeletedOrgs = (%d, %v), want (0, nil)", n, err)
	}
}

// === Full pipeline integration: Phases 10-12 via ScanOnce ===

func TestScanner_ScanOnce_IncludesPhases10to12(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{
		UserGraceDays:      7,
		TrashRetentionDays: 30,
		OrgGraceDays:       30,
	})

	orgID := uuid.New()
	store.AddOrganization(orgID)

	// Phase 10: expired deleted user
	expiredUser := uuid.New()
	store.AddDeletedUser(orgID, expiredUser, "expired@test.com", time.Now().AddDate(0, 0, -10))

	// Phase 11: expired deleted library
	expiredLib := uuid.New()
	store.AddDeletedLibrary(orgID, expiredLib, "hot", time.Now().AddDate(0, 0, -45))

	// Phase 12: expired deleted org (different org)
	expiredOrgID := uuid.New()
	store.AddDeletedOrg(expiredOrgID, "Dead Corp", time.Now().AddDate(0, 0, -60))

	ctx := context.Background()
	err := s.ScanOnce(ctx)
	if err != nil {
		t.Fatalf("ScanOnce failed: %v", err)
	}

	// Check all three phases produced items
	typeCount := make(map[ItemType]int)
	for _, orgQ := range []uuid.UUID{orgID, expiredOrgID} {
		for _, item := range store.QueueItems(orgQ) {
			typeCount[item.ItemType]++
		}
	}
	if typeCount[ItemUserCascade] != 1 {
		t.Errorf("expected 1 user_cascade from Phase 10, got %d", typeCount[ItemUserCascade])
	}
	if typeCount[ItemLibraryCascade] != 1 {
		t.Errorf("expected 1 library_cascade from Phase 11, got %d", typeCount[ItemLibraryCascade])
	}
	if typeCount[ItemOrgCascade] != 1 {
		t.Errorf("expected 1 org_cascade from Phase 12, got %d", typeCount[ItemOrgCascade])
	}
}
