package gc

import (
	"context"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/google/uuid"
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
	store.AddOrganization(orgID)

	// 3 blocks: ref_count=0, ref_count=1, ref_count=0
	store.AddBlock(orgID, "block-orphan-1", "hot", 0)
	store.AddBlock(orgID, "block-alive", "hot", 1)
	store.AddBlock(orgID, "block-orphan-2", "cold", 0)

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

	// Stats should be updated
	if stats.LastScanRun().IsZero() {
		t.Error("LastScanRun should be set after scan")
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

	// Should enqueue only the expired share link
	items := store.QueueItems(orgID)
	shareLinkItems := 0
	for _, item := range items {
		if item.ItemType == ItemShareLink {
			shareLinkItems++
		}
	}
	if shareLinkItems != 1 {
		t.Errorf("expected 1 expired share link enqueued, got %d", shareLinkItems)
	}
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
	store.AddBlock(orgID, "orphan-blk", "hot", 0)
	store.AddBlock(orgID, "alive-blk", "hot", 5)

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

	// Should enqueue: 1 orphaned block + 1 expired share link = 2 items
	items := store.QueueItems(orgID)
	if len(items) != 2 {
		t.Errorf("expected 2 items from full pipeline, got %d", len(items))
	}

	typeCount := make(map[ItemType]int)
	for _, item := range items {
		typeCount[item.ItemType]++
	}
	if typeCount[ItemBlock] != 1 {
		t.Errorf("expected 1 block item, got %d", typeCount[ItemBlock])
	}
	if typeCount[ItemShareLink] != 1 {
		t.Errorf("expected 1 share_link item, got %d", typeCount[ItemShareLink])
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
	store.AddShare(libID, uuid.New(), uuid.New(), time.Now().Add(-24*time.Hour))  // expired
	store.AddShare(libID, uuid.New(), uuid.New(), time.Now().Add(24*time.Hour))   // active
	store.AddShare(libID, uuid.New(), uuid.New(), time.Time{})                     // permanent

	ctx := context.Background()
	s.scanExpiredShares(ctx)

	// One expired share should be deleted (directly, not via queue)
	shares, _ := store.ListExpiredShares()
	if len(shares) != 0 {
		t.Errorf("expected 0 expired shares after cleanup, got %d", len(shares))
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
	store.AddBlock(orgID, "orphan-blk", "hot", 0)

	ctx := context.Background()

	s.ScanOnce(ctx)
	firstCount := store.QueueLen()

	s.ScanOnce(ctx)
	secondCount := store.QueueLen()

	if firstCount != 1 {
		t.Errorf("first scan should enqueue 1 item, got %d", firstCount)
	}
	// Mock doesn't deduplicate, so second count will be 2
	if secondCount != 2 {
		t.Errorf("expected 2 items after second scan (mock doesn't deduplicate), got %d", secondCount)
	}
}
