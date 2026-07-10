package gc

import (
	"context"
	"errors"
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

func TestScanner_LoadBlockCandidatesStartDay_ColdStartUsesInitialLookback(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	cutoffDay := time.Date(2026, 6, 4, 0, 0, 0, 0, time.UTC)
	startDay, err := s.loadBlockCandidatesStartDay(cutoffDay)
	if err != nil {
		t.Fatalf("loadBlockCandidatesStartDay failed: %v", err)
	}

	want := cutoffDay.AddDate(0, 0, -gcInitialScanLookbackDays)
	if !startDay.Equal(want) {
		t.Fatalf("loadBlockCandidatesStartDay = %s, want %s", startDay.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

func TestScanner_LoadFailedItemsExpiryStartDay_ColdStartUsesFailedItemLookback(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	cutoffDay := time.Date(2026, 6, 4, 0, 0, 0, 0, time.UTC)
	startDay, err := s.loadFailedItemsExpiryStartDay(cutoffDay)
	if err != nil {
		t.Fatalf("loadFailedItemsExpiryStartDay failed: %v", err)
	}

	want := cutoffDay.AddDate(0, 0, -gcFailedItemExpiryInitialLookbackDays)
	if !startDay.Equal(want) {
		t.Fatalf("loadFailedItemsExpiryStartDay = %s, want %s", startDay.Format(time.RFC3339), want.Format(time.RFC3339))
	}
}

func TestScanner_ScanOnce_ExpiredProvisionalRefEnqueuesZeroRefBlock(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	blockID := "block-expired"
	referrer := db.BlockReferrerForUpload("upload-op")
	expiresAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)

	store.AddOrganization(orgID)
	store.AddBlock(orgID, blockID, "hot", 0)
	store.mu.Lock()
	store.blockReferences[fmt.Sprintf("%s:%s", orgID, blockID)] = map[string]struct{}{referrer: {}}
	store.mu.Unlock()
	store.AddProvisionalBlockRefExpiry(orgID, blockID, referrer, "hot", expiresAt)

	if err := s.ScanOnce(context.Background()); err != nil {
		t.Fatalf("ScanOnce() error = %v, want nil", err)
	}
	hasRefs, err := store.BlockHasReferences(orgID, blockID)
	if err != nil {
		t.Fatalf("BlockHasReferences() error = %v", err)
	}
	if hasRefs {
		t.Fatal("expected expired provisional ref to be removed")
	}
	items := store.QueueItems(orgID)
	if len(items) != 1 {
		t.Fatalf("expected 1 queued block after scan, got %d", len(items))
	}
	if items[0].ItemType != ItemBlock || items[0].ItemID != blockID {
		t.Fatalf("queued item = %#v, want block %s", items[0], blockID)
	}
	expiries, err := store.ListProvisionalBlockRefExpiriesByDay(expiresAt, db.GCDiscoveryBucket(orgID.String(), blockID, referrer))
	if err != nil {
		t.Fatalf("ListProvisionalBlockRefExpiriesByDay() error = %v", err)
	}
	if len(expiries) != 0 {
		t.Fatalf("expected expiry tracker to be removed, got %#v", expiries)
	}
	gotCursor, err := store.LoadGCStats(gcProvisionalBlockRefsCursorKey)
	if err != nil {
		t.Fatalf("LoadGCStats() error = %v", err)
	}
	if gotCursor == "" {
		t.Fatal("expected provisional ref cursor to be persisted")
	}
}

func TestScanner_ScanExpiredProvisionalBlockRefs_PreservesLiveBlocks(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	blockID := "block-live"
	expiredRef := db.BlockReferrerForUpload("expired-op")
	fsRef := db.BlockReferrerForFSObject("lib-1", "fs-1")
	expiresAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)

	store.AddOrganization(orgID)
	store.AddBlock(orgID, blockID, "hot", 0)
	store.mu.Lock()
	store.blockReferences[fmt.Sprintf("%s:%s", orgID, blockID)] = map[string]struct{}{
		expiredRef: {},
		fsRef:      {},
	}
	store.mu.Unlock()
	store.AddProvisionalBlockRefExpiry(orgID, blockID, expiredRef, "hot", expiresAt)

	cleaned, err := s.scanExpiredProvisionalBlockRefs(context.Background())
	if err != nil {
		t.Fatalf("scanExpiredProvisionalBlockRefs() error = %v, want nil", err)
	}
	if cleaned != 1 {
		t.Fatalf("scanExpiredProvisionalBlockRefs() cleaned = %d, want 1", cleaned)
	}
	hasRefs, err := store.BlockHasReferences(orgID, blockID)
	if err != nil {
		t.Fatalf("BlockHasReferences() error = %v", err)
	}
	if !hasRefs {
		t.Fatal("expected live fs: ref to keep block alive")
	}
	candidates, err := store.ListBlockGCCandidatesByDay(expiresAt, db.GCDiscoveryBucket(orgID.String(), blockID))
	if err != nil {
		t.Fatalf("ListBlockGCCandidatesByDay() error = %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("expected no GC candidate for still-live block, got %#v", candidates)
	}
	expiries, err := store.ListProvisionalBlockRefExpiriesByDay(expiresAt, db.GCDiscoveryBucket(orgID.String(), blockID, expiredRef))
	if err != nil {
		t.Fatalf("ListProvisionalBlockRefExpiriesByDay() error = %v", err)
	}
	if len(expiries) != 0 {
		t.Fatalf("expected expiry tracker to be removed, got %#v", expiries)
	}
}

func TestScanner_ScanExpiredProvisionalBlockRefs_IgnoresStaleProjectionWhenCanonicalRenewed(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	blockID := "block-renewed-upload"
	referrer := db.BlockReferrerForUpload("renewed-op")
	oldExpiresAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)
	renewedExpiresAt := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Millisecond)

	store.AddOrganization(orgID)
	store.AddBlock(orgID, blockID, "hot", 0)
	store.AddBlockReferenceForTest(orgID, blockID, referrer)
	store.AddProvisionalBlockRefExpiry(orgID, blockID, referrer, "hot", renewedExpiresAt)
	store.AddProvisionalBlockRefExpiryProjectionForTest(orgID, blockID, referrer, "hot", oldExpiresAt)

	cleaned, err := s.scanExpiredProvisionalBlockRefs(context.Background())
	if err != nil {
		t.Fatalf("scanExpiredProvisionalBlockRefs() error = %v", err)
	}
	if cleaned != 1 {
		t.Fatalf("scanExpiredProvisionalBlockRefs() cleaned = %d, want 1 stale projection", cleaned)
	}
	hasRefs, err := store.BlockHasReferences(orgID, blockID)
	if err != nil {
		t.Fatalf("BlockHasReferences() error = %v", err)
	}
	if !hasRefs {
		t.Fatal("expected renewed provisional ref to remain alive")
	}
	canonical, found, err := store.GetProvisionalBlockRefExpiry(orgID, blockID, referrer)
	if err != nil {
		t.Fatalf("GetProvisionalBlockRefExpiry() error = %v", err)
	}
	if !found {
		t.Fatal("expected renewed canonical expiry to remain")
	}
	if !canonical.ExpiresAt.Equal(renewedExpiresAt) {
		t.Fatalf("canonical expires_at = %v, want %v", canonical.ExpiresAt, renewedExpiresAt)
	}
	oldExpiries, err := store.ListProvisionalBlockRefExpiriesByDay(oldExpiresAt, db.GCDiscoveryBucket(orgID.String(), blockID, referrer))
	if err != nil {
		t.Fatalf("ListProvisionalBlockRefExpiriesByDay(old) error = %v", err)
	}
	for _, expiry := range oldExpiries {
		if expiry.ExpiresAt.Equal(oldExpiresAt) {
			t.Fatalf("expected stale projection at %v to be removed, got %#v", oldExpiresAt, oldExpiries)
		}
	}
	candidates, err := store.ListBlockGCCandidatesByDay(time.Now().UTC(), db.GCDiscoveryBucket(orgID.String(), blockID))
	if err != nil {
		t.Fatalf("ListBlockGCCandidatesByDay() error = %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("expected no GC candidate for renewed upload ref, got %#v", candidates)
	}
}

func TestScanner_ScanExpiredProvisionalBlockRefs_DropsProjectionWhenCanonicalMissing(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	blockID := "block-missing-canonical"
	referrer := db.BlockReferrerForUpload("finalized-op")
	expiresAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)

	store.AddOrganization(orgID)
	store.AddBlock(orgID, blockID, "hot", 0)
	store.AddBlockReferenceForTest(orgID, blockID, referrer)
	store.AddProvisionalBlockRefExpiryProjectionForTest(orgID, blockID, referrer, "hot", expiresAt)

	cleaned, err := s.scanExpiredProvisionalBlockRefs(context.Background())
	if err != nil {
		t.Fatalf("scanExpiredProvisionalBlockRefs() error = %v", err)
	}
	if cleaned != 1 {
		t.Fatalf("scanExpiredProvisionalBlockRefs() cleaned = %d, want 1 orphaned projection", cleaned)
	}
	hasRefs, err := store.BlockHasReferences(orgID, blockID)
	if err != nil {
		t.Fatalf("BlockHasReferences() error = %v", err)
	}
	if !hasRefs {
		t.Fatal("expected scanner to leave block refs untouched when canonical expiry is missing")
	}
	expiries, err := store.ListProvisionalBlockRefExpiriesByDay(expiresAt, db.GCDiscoveryBucket(orgID.String(), blockID, referrer))
	if err != nil {
		t.Fatalf("ListProvisionalBlockRefExpiriesByDay() error = %v", err)
	}
	if len(expiries) != 0 {
		t.Fatalf("expected orphaned projection to be removed, got %#v", expiries)
	}
	candidates, err := store.ListBlockGCCandidatesByDay(time.Now().UTC(), db.GCDiscoveryBucket(orgID.String(), blockID))
	if err != nil {
		t.Fatalf("ListBlockGCCandidatesByDay() error = %v", err)
	}
	if len(candidates) != 0 {
		t.Fatalf("expected no GC candidate when canonical expiry is missing, got %#v", candidates)
	}
}

func TestScanner_ScanExpiredProvisionalBlockRefs_UsesScanTimeForCandidatePartition(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	blockID := "block-old-expiry"
	referrer := db.BlockReferrerForUpload("stale-upload")
	expiresAt := time.Now().Add(-72 * time.Hour).UTC().Truncate(time.Millisecond)
	beforeScan := time.Now().UTC().Add(-time.Second)

	store.AddOrganization(orgID)
	store.AddBlock(orgID, blockID, "hot", 0)
	store.mu.Lock()
	store.blockReferences[fmt.Sprintf("%s:%s", orgID, blockID)] = map[string]struct{}{referrer: {}}
	store.mu.Unlock()
	store.AddProvisionalBlockRefExpiry(orgID, blockID, referrer, "hot", expiresAt)

	cleaned, err := s.scanExpiredProvisionalBlockRefs(context.Background())
	if err != nil {
		t.Fatalf("scanExpiredProvisionalBlockRefs() error = %v", err)
	}
	if cleaned != 1 {
		t.Fatalf("scanExpiredProvisionalBlockRefs() cleaned = %d, want 1", cleaned)
	}
	afterScan := time.Now().UTC().Add(time.Second)

	oldCandidates, err := store.ListBlockGCCandidatesByDay(expiresAt, db.GCDiscoveryBucket(orgID.String(), blockID))
	if err != nil {
		t.Fatalf("ListBlockGCCandidatesByDay(old expiry day) error = %v", err)
	}
	if len(oldCandidates) != 0 {
		t.Fatalf("expected no candidate in old expiry partition, got %#v", oldCandidates)
	}

	newCandidates, err := store.ListBlockGCCandidatesByDay(beforeScan, db.GCDiscoveryBucket(orgID.String(), blockID))
	if err != nil {
		t.Fatalf("ListBlockGCCandidatesByDay(scan day) error = %v", err)
	}
	if len(newCandidates) != 1 {
		t.Fatalf("expected 1 candidate in current partition, got %#v", newCandidates)
	}
	if newCandidates[0].CandidateAt.Before(beforeScan) || newCandidates[0].CandidateAt.After(afterScan) {
		t.Fatalf("candidate_at = %v, want between %v and %v", newCandidates[0].CandidateAt, beforeScan, afterScan)
	}
}

func TestScanner_ScanOnce_ReturnsPhaseErrorsButContinues(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{UserGraceDays: 7})

	orgID := uuid.New()
	store.AddOrganization(orgID)
	store.AddShareLink("token-expired", orgID, time.Now().Add(-24*time.Hour))
	store.deleteExpiredShareLinkErr = fmt.Errorf("delete failed")

	expiredUser := uuid.New()
	store.AddDeletedUser(orgID, expiredUser, "expired@test.com", time.Now().AddDate(0, 0, -10))

	beforePhaseErr := testutil.ToFloat64(metrics.GCScannerActionsTotal.WithLabelValues("expired_links", "phase_error"))
	err := s.ScanOnce(context.Background())
	if err == nil {
		t.Fatal("ScanOnce() error = nil, want non-nil")
	}
	if got := err.Error(); got == "delete failed" || got == "" {
		t.Fatalf("ScanOnce() error = %q, want wrapped phase context", got)
	}
	if stats.LastScanRun().IsZero() {
		t.Fatal("LastScanRun should be set after scan with phase errors")
	}
	if stats.LastScanAttempt().IsZero() {
		t.Fatal("LastScanAttempt should be set after scan with phase errors")
	}
	if !stats.LastScanSuccess().IsZero() {
		t.Fatal("LastScanSuccess should not be set after scan with phase errors")
	}

	items := store.QueueItems(orgID)
	userCascadeCount := 0
	for _, item := range items {
		if item.ItemType == ItemUserCascade && item.ItemID == expiredUser.String() {
			userCascadeCount++
		}
	}
	if userCascadeCount != 1 {
		t.Fatalf("expected later phases to continue and enqueue deleted user once, got %d", userCascadeCount)
	}
	afterPhaseErr := testutil.ToFloat64(metrics.GCScannerActionsTotal.WithLabelValues("expired_links", "phase_error"))
	if afterPhaseErr-beforePhaseErr != 1 {
		t.Fatalf("expired_links phase_error metric delta = %v, want 1", afterPhaseErr-beforePhaseErr)
	}
}

func TestScanner_ScanOnce_ReturnsAccumulatedPhaseErrorWhenCanceledBetweenPhases(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	store.AddOrganization(orgID)
	store.AddShareLink("token-expired", orgID, time.Now().Add(-24*time.Hour))
	store.deleteExpiredShareLinkErr = fmt.Errorf("delete failed")

	ctx, cancel := context.WithCancel(context.Background())
	store.reconcileStorageCountersHook = func() {
		cancel()
	}

	err := s.ScanOnce(ctx)
	if err == nil {
		t.Fatal("ScanOnce() error = nil, want non-nil")
	}
	if got := err.Error(); got == context.Canceled.Error() {
		t.Fatalf("ScanOnce() error = %q, want accumulated phase error", got)
	}
	if got := err.Error(); got == "" || !containsString(got, "expired_links") {
		t.Fatalf("ScanOnce() error = %q, want expired_links context", got)
	}
}

func containsString(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) && (haystack == needle || containsSubstring(haystack, needle))
}

func containsSubstring(haystack, needle string) bool {
	for index := 0; index+len(needle) <= len(haystack); index++ {
		if haystack[index:index+len(needle)] == needle {
			return true
		}
	}
	return false
}

func TestIsScannerInterruptError(t *testing.T) {
	if !isScannerInterruptError(context.Canceled) {
		t.Fatal("context.Canceled should be treated as scanner interrupt")
	}
	if !isScannerInterruptError(context.DeadlineExceeded) {
		t.Fatal("context.DeadlineExceeded should be treated as scanner interrupt")
	}
	if isScannerInterruptError(fmt.Errorf("boom")) {
		t.Fatal("ordinary errors should not be treated as scanner interrupt")
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
	if stats.LastScanAttempt().IsZero() {
		t.Error("LastScanAttempt should be set after scan")
	}
	if stats.LastScanSuccess().IsZero() {
		t.Error("LastScanSuccess should be set after successful scan")
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

func TestScanner_ScanOrphanedBlocks_SkipsRetriedQueuedCandidate(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	candidateAt := time.Now().Add(-1 * time.Minute).UTC().Truncate(time.Millisecond)
	store.AddBlockGCCandidate(orgID, "block-orphan", "hot", candidateAt)
	if err := store.EnqueueItem(orgID, candidateAt, ItemBlock, "block-orphan", uuid.Nil, "hot", 0); err != nil {
		t.Fatalf("failed to seed queue item: %v", err)
	}

	items, err := store.DequeueBatch(orgID, 1, time.Now())
	if err != nil || len(items) != 1 {
		t.Fatalf("DequeueBatch failed: %v / items=%d", err, len(items))
	}
	if err := q.IncrementRetry(items[0]); err != nil {
		t.Fatalf("IncrementRetry failed: %v", err)
	}

	if err := s.ScanOnce(context.Background()); err != nil {
		t.Fatalf("ScanOnce failed: %v", err)
	}

	if got := len(store.QueueItems(orgID)); got != 1 {
		t.Fatalf("expected scanner to avoid duplicate retried block candidate, got %d items", got)
	}
}

// TestScanner_ScanOrphanedBlocks_EnqueuesFromCandidatesByDay validates the new
// discovery flow: when EnsureBlockGCCandidate has registered a candidate for a
// zero-ref block, the scanner finds it through the by-day projection and
// enqueues exactly one queue item per candidate. Earlier versions of this test
// relied on a per-org partition scan of the `blocks` table to backfill missing
// candidate rows; that scan was removed when `blocks` moved to per-block
// partitioning, so the only entry point now is EnsureBlockGCCandidate.
func TestScanner_ScanOrphanedBlocks_EnqueuesFromCandidatesByDay(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	store.AddOrganization(orgID)
	store.AddBlock(orgID, "block-zero-ref", "hot", 0)
	store.AddBlock(orgID, "block-still-live", "hot", 2)

	// Refcount-decrement paths invoke EnsureBlockGCCandidate when they reach
	// zero; here we register the candidate row directly to simulate that.
	if _, err := store.EnsureBlockGCCandidate(orgID, "block-zero-ref", "hot", time.Now()); err != nil {
		t.Fatalf("EnsureBlockGCCandidate failed: %v", err)
	}

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

	candidates := store.AllBlockGCCandidates()
	if len(candidates) != 1 || candidates[0].BlockID != "block-zero-ref" {
		t.Fatalf("expected one persisted GC candidate for block-zero-ref, got %+v", candidates)
	}
}

// TestScanner_ScanOrphanedBlocks_DiscoversAcrossDistinctBuckets confirms that
// the scanner walks every bucket in the day partition, not just the bucket of
// the first candidate it sees. Many candidates with different (org, block)
// pairs land in different buckets via GCDiscoveryBucket().
func TestScanner_ScanOrphanedBlocks_DiscoversAcrossDistinctBuckets(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	store.AddOrganization(orgID)

	// Seed enough distinct block_ids that every discovery bucket is
	// touched at least once; 1000 produces full coverage at N=32 buckets.
	blockCount := 1000
	for i := 0; i < blockCount; i++ {
		blockID := fmt.Sprintf("block-%04d", i)
		if _, err := store.EnsureBlockGCCandidate(orgID, blockID, "hot", time.Now()); err != nil {
			t.Fatalf("EnsureBlockGCCandidate failed for %s: %v", blockID, err)
		}
	}

	if err := s.ScanOnce(context.Background()); err != nil {
		t.Fatalf("ScanOnce failed: %v", err)
	}

	items := store.QueueItems(orgID)
	if len(items) != blockCount {
		t.Fatalf("expected scanner to enqueue all %d candidates across all buckets, got %d", blockCount, len(items))
	}
}

// TestScanner_ScanOrphanedBlocks_AdvancesCursorAndSkipsOldDays validates that
// the per-day cursor (gc.scan.block_candidates.last_candidate_day) is
// persisted after a successful scan so the next pass starts later. The
// scanner uses a small overlap window (gcScanOverlapDays) for late arrivals,
// so a candidate enqueued before the cursor minus the overlap must NOT be
// re-discovered.
func TestScanner_ScanOrphanedBlocks_AdvancesCursorAndSkipsOldDays(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	store.AddOrganization(orgID)

	// Seed a recent candidate the scanner must pick up on the first pass.
	if _, err := store.EnsureBlockGCCandidate(orgID, "block-recent", "hot", time.Now()); err != nil {
		t.Fatalf("EnsureBlockGCCandidate failed: %v", err)
	}

	if err := s.ScanOnce(context.Background()); err != nil {
		t.Fatalf("first ScanOnce failed: %v", err)
	}
	if got := len(store.QueueItems(orgID)); got != 1 {
		t.Fatalf("expected 1 queued item on first pass, got %d", got)
	}
	// Cursor should now be persisted; default is today-1 (per scanner code).
	cursorValue, err := store.LoadGCStats("gc.scan.block_candidates.last_candidate_day")
	if err != nil || cursorValue == "" {
		t.Fatalf("expected scan cursor to be persisted, got value=%q err=%v", cursorValue, err)
	}

	// Drain the existing item so we can detect any duplicate re-enqueue.
	if _, err := store.DequeueBatch(orgID, 10, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("DequeueBatch failed: %v", err)
	}
	if err := store.CompleteItem(orgID, store.AllBlockGCCandidates()[0].CandidateAt, ItemBlock, "block-recent"); err != nil {
		t.Fatalf("CompleteItem failed: %v", err)
	}

	// Manually back-date the cursor 30 days into the future of the
	// candidate's day, simulating "we already scanned past this candidate."
	// The scanner only walks `cursor - gcScanOverlapDays`..today, so a
	// 30-day gap leaves the candidate's day before the overlap window.
	farFuture := time.Now().AddDate(0, 0, 30)
	if err := store.SaveGCStats("gc.scan.block_candidates.last_candidate_day", db.GCProjectionDateString(farFuture)); err != nil {
		t.Fatalf("SaveGCStats failed: %v", err)
	}

	if err := s.ScanOnce(context.Background()); err != nil {
		t.Fatalf("second ScanOnce failed: %v", err)
	}
	if got := len(store.QueueItems(orgID)); got != 0 {
		t.Fatalf("expected scanner to skip candidate outside cursor window, got %d new items", got)
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

	// Production reads from canonical libraries.size_bytes / file_count
	// during aggregate reconciliation; seed those for both libs.
	store.SetLibraryCanonicalStats(activeLibID, 100, 2)
	store.SetLibraryCanonicalStats(deletedLibID, 50, 1)
	// Seed the lib-scope storage_counters snapshots with DRIFTED values so
	// the test proves reconciliation reads canonical, not the snapshot. If
	// reconciliation regressed back to summing lib-scope snapshots, platform
	// would converge to 200 (the active lib's snapshot) instead of 100.
	store.AddStorageSnapshot(traffic.LibraryStorageScope(orgID.String(), activeLibID.String()), 200, 5)
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

// Regression for P6 (ISSUE-GC-EXISTENCE-CHECK-FAILOPEN-01): a transient error on
// the group-existence read must NOT be read as "group deleted" — that would drop a
// valid group share. Fail closed: skip the share and surface the error.
func TestScanner_ScanOrphanedGroupShares_FailClosedOnGroupExistsError(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	groupID := uuid.New()
	libID := uuid.New()
	shareID := uuid.New()
	store.AddOrganization(orgID)
	store.AddLibrary(orgID, libID, "hot")
	store.AddGroupForOrg(orgID, groupID) // group genuinely EXISTS
	store.AddGroupShare(libID, shareID, groupID)

	sentinel := errors.New("cassandra unavailable")
	store.groupExistsErr = sentinel

	n, err := s.scanOrphanedGroupShares(context.Background())
	if err == nil {
		t.Fatal("expected scanOrphanedGroupShares to surface the group-existence error, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected the surfaced error to wrap the sentinel, got %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 shares cleaned on existence error, got %d", n)
	}

	store.mu.RLock()
	defer store.mu.RUnlock()
	if _, ok := store.shares[fmt.Sprintf("%s:%s", libID, shareID)]; !ok {
		t.Fatal("valid group share was deleted despite a transient group-existence error (fail-open regression)")
	}
}

// Phase 9 must use the authoritative org_id carried on the group-share record and
// not depend on the library→org projection: an orphaned share whose group is gone
// is cleaned even when the library (and thus FindOrgForLibrary) can no longer resolve.
func TestScanner_ScanOrphanedGroupShares_UsesShareOrgIDWithoutLibraryLookup(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	missingGroupID := uuid.New() // group does NOT exist
	libID := uuid.New()
	shareID := uuid.New()
	store.AddOrganization(orgID)
	store.AddLibrary(orgID, libID, "hot") // stamps gs.OrgID on the share
	store.AddGroupShare(libID, shareID, missingGroupID)

	// Break the library→org projection: FindOrgForLibrary can no longer resolve.
	// The share still carries OrgID, so Phase 9 must still clean the orphan.
	store.mu.Lock()
	delete(store.libraries, libID)
	store.mu.Unlock()

	n, err := s.scanOrphanedGroupShares(context.Background())
	if err != nil {
		t.Fatalf("scanOrphanedGroupShares returned error: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 orphaned share cleaned via the share's own org_id, got %d", n)
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	if _, ok := store.shares[fmt.Sprintf("%s:%s", libID, shareID)]; ok {
		t.Fatal("orphaned group share was not cleaned even though the share carried its org_id")
	}
}

func TestScanner_ScanOrphanedGroupShares_FailClosedWhenOrgCannotBeResolved(t *testing.T) {
	store := NewMockStore()
	s := NewScanner(store, NewQueue(store), &Stats{}, config.GCConfig{})

	libID := uuid.New()
	shareID := uuid.New()
	store.AddGroupShare(libID, shareID, uuid.New()) // no library: legacy row has no OrgID
	sentinel := errors.New("library org projection unavailable")
	store.findOrgForLibraryErr = sentinel

	n, err := s.scanOrphanedGroupShares(context.Background())
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected org-resolution error, got %v", err)
	}
	if n != 0 {
		t.Fatalf("expected no cleanup when org resolution fails, got %d", n)
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	if _, ok := store.shares[fmt.Sprintf("%s:%s", libID, shareID)]; !ok {
		t.Fatal("share was deleted despite unresolved org")
	}
}

func TestScanner_ScanOrphanedGroupShares_CacheIsScopedByOrg(t *testing.T) {
	store := NewMockStore()
	s := NewScanner(store, NewQueue(store), &Stats{}, config.GCConfig{})

	groupID := uuid.New()
	liveOrgID, orphanOrgID := uuid.New(), uuid.New()
	liveLibID, orphanLibID := uuid.New(), uuid.New()
	liveShareID, orphanShareID := uuid.New(), uuid.New()
	store.AddLibrary(liveOrgID, liveLibID, "hot")
	store.AddLibrary(orphanOrgID, orphanLibID, "hot")
	store.AddGroupForOrg(liveOrgID, groupID)
	store.AddGroupShare(liveLibID, liveShareID, groupID)
	store.AddGroupShare(orphanLibID, orphanShareID, groupID)

	n, err := s.scanOrphanedGroupShares(context.Background())
	if err != nil {
		t.Fatalf("scanOrphanedGroupShares returned error: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected only the orphan-org share cleaned, got %d", n)
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	if _, ok := store.shares[fmt.Sprintf("%s:%s", liveLibID, liveShareID)]; !ok {
		t.Fatal("live-org share was deleted through a cross-org cache collision")
	}
	if _, ok := store.shares[fmt.Sprintf("%s:%s", orphanLibID, orphanShareID)]; ok {
		t.Fatal("orphan-org share was retained through a cross-org cache collision")
	}
}

func TestScanner_ScanOrphanedGroupShares_StreamingHonorsCancellation(t *testing.T) {
	store := NewMockStore()
	store.AddGroupShare(uuid.New(), uuid.New(), uuid.New())
	s := NewScanner(store, NewQueue(store), &Stats{}, config.GCConfig{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	n, err := s.scanOrphanedGroupShares(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation from streaming scan, got %v", err)
	}
	if n != 0 {
		t.Fatalf("expected no cleanup after cancellation, got %d", n)
	}
}

// Regression for P6: a transient LibraryExists error in Phase 3 must NOT be treated
// as "library gone" and enqueue a live library's commits for deletion.
func TestScanner_ScanOrphanedCommits_FailClosedOnLibraryExistsError(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	libID := uuid.New()
	store.AddOrganization(orgID)
	store.AddLibrary(orgID, libID, "hot") // live library with resolvable org
	store.AddCommit(libID, "commit-1", "fs-root-1")

	sentinel := errors.New("cassandra unavailable")
	store.libraryExistsErr = sentinel

	n, err := s.scanOrphanedCommits(context.Background())
	if err == nil {
		t.Fatal("expected scanOrphanedCommits to surface the existence-check error, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected the surfaced error to wrap the sentinel, got %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 commits enqueued on existence error, got %d", n)
	}
	for _, item := range store.QueueItems(orgID) {
		if item.ItemType == ItemCommit {
			t.Fatal("live library commit was enqueued for deletion despite an existence-check error (fail-open regression)")
		}
	}
}

// Regression for P6: a transient LibraryExists error in Phase 4 must NOT be treated
// as "library gone" and enqueue a live library's fs_objects for deletion.
func TestScanner_ScanOrphanedFSObjects_FailClosedOnLibraryExistsError(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	libID := uuid.New()
	store.AddOrganization(orgID)
	store.AddLibrary(orgID, libID, "hot")
	store.AddFSObject(libID, "fs-1", "file", []string{"blk-1"})

	sentinel := errors.New("cassandra unavailable")
	store.libraryExistsErr = sentinel

	n, err := s.scanOrphanedFSObjects(context.Background())
	if err == nil {
		t.Fatal("expected scanOrphanedFSObjects to surface the existence-check error, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected the surfaced error to wrap the sentinel, got %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 fs_objects enqueued on existence error, got %d", n)
	}
	for _, item := range store.QueueItems(orgID) {
		if item.ItemType == ItemFSObject {
			t.Fatal("live library fs_object was enqueued for deletion despite an existence-check error (fail-open regression)")
		}
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

func TestScanner_ScanOrphanedCommits_SkipsRetriedQueuedCommit(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	libID := uuid.New()
	deletedAt := time.Now().Add(-24 * time.Hour).UTC().Truncate(time.Millisecond)
	store.AddOrganization(orgID)
	store.AddDeletedLibrary(orgID, libID, "hot", deletedAt)
	store.AddCommit(libID, "commit-orphan-1", "fs-root")
	store.SeedQueueItemForTest(orgID, deletedAt, ItemCommit, "commit-orphan-1", libID, "", 0)

	items, err := store.DequeueBatch(orgID, 1, time.Now())
	if err != nil || len(items) != 1 {
		t.Fatalf("DequeueBatch failed: %v / items=%d", err, len(items))
	}
	if err := q.IncrementRetry(items[0]); err != nil {
		t.Fatalf("IncrementRetry failed: %v", err)
	}

	if n, err := s.scanOrphanedCommits(context.Background()); err != nil || n != 0 {
		t.Fatalf("scanOrphanedCommits after retry = (%d, %v), want (0, nil)", n, err)
	}
	if got := len(store.QueueItems(orgID)); got != 1 {
		t.Fatalf("expected 1 queued orphaned commit after dedupe, got %d", got)
	}
}

func TestScanner_ScanOrphanedBlocks_EnqueueFailureKeepsCursorUnchanged(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	store.AddOrganization(orgID)

	previousCursor := db.GCProjectionDateString(db.GCProjectionUTCDate(time.Now().AddDate(0, 0, -1)))
	if err := store.SaveGCStats(gcBlockCandidatesCursorKey, previousCursor); err != nil {
		t.Fatalf("SaveGCStats() failed: %v", err)
	}

	if _, err := store.EnsureBlockGCCandidate(orgID, "block-enqueue-fail", "hot", time.Now()); err != nil {
		t.Fatalf("EnsureBlockGCCandidate failed: %v", err)
	}
	store.enqueueBatchErr = fmt.Errorf("enqueue failed")

	enqueued, err := s.scanOrphanedBlocks(context.Background())
	if err == nil {
		t.Fatal("scanOrphanedBlocks() error = nil, want non-nil")
	}
	if enqueued != 0 {
		t.Fatalf("scanOrphanedBlocks() enqueued = %d, want 0", enqueued)
	}
	gotCursor, err := store.LoadGCStats(gcBlockCandidatesCursorKey)
	if err != nil {
		t.Fatalf("LoadGCStats() failed: %v", err)
	}
	if gotCursor != previousCursor {
		t.Fatalf("block candidates cursor = %q, want unchanged %q", gotCursor, previousCursor)
	}
	if got := len(store.QueueItems(orgID)); got != 0 {
		t.Fatalf("enqueue failure should leave queue empty, got %d items", got)
	}
}

func TestScanner_ScanOrphanedCommits_DoesNotCrossSuppressAcrossLibraries(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	libPending := uuid.New()
	libOrphan := uuid.New()
	deletedAt := time.Now().Add(-24 * time.Hour).UTC().Truncate(time.Millisecond)
	store.AddOrganization(orgID)
	store.AddLibrary(orgID, libPending, "hot")
	store.AddDeletedLibrary(orgID, libOrphan, "hot", deletedAt)
	store.mu.Lock()
	delete(store.libraries, libOrphan)
	store.mu.Unlock()
	store.AddCommit(libPending, "shared-commit", "fs-pending")
	store.AddCommit(libOrphan, "shared-commit", "fs-orphan")
	store.SeedQueueItemForTest(orgID, deletedAt, ItemCommit, "shared-commit", libPending, "", 0)

	if n, err := s.scanOrphanedCommits(context.Background()); err != nil || n != 1 {
		t.Fatalf("scanOrphanedCommits = (%d, %v), want (1, nil)", n, err)
	}

	orphanCount := 0
	for _, item := range store.QueueItems(orgID) {
		if item.ItemType == ItemCommit && item.LibraryID == libOrphan && item.ItemID == "shared-commit" {
			orphanCount++
		}
	}
	if orphanCount != 1 {
		t.Fatalf("expected orphaned library commit to enqueue once despite same commit id in another library, got %d", orphanCount)
	}
}

func TestScanner_ScanOrphanedCommits_SkipsUnresolvableRepresentation(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	libID := uuid.New()
	deletedAt := time.Now().Add(-24 * time.Hour).UTC().Truncate(time.Millisecond)
	store.AddOrganization(orgID)
	store.AddDeletedLibrary(orgID, libID, "hot", deletedAt)
	store.mu.Lock()
	store.deletedLibraries[libID].BlockRepresentationID = ""
	delete(store.libraries, libID)
	store.mu.Unlock()
	store.AddCommit(libID, "commit-orphan-legacy", "fs-root")

	// Representation is unresolvable (canonical row gone, no persisted rep): the
	// scanner must skip rather than enqueue an incomplete task.
	n, err := s.scanOrphanedCommits(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("scanOrphanedCommits = (%d, %v), want (0, nil)", n, err)
	}
	if items := store.QueueItems(orgID); len(items) != 0 {
		t.Fatalf("expected no queued work, got %#v", items)
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

func TestScanner_ScanOrphanedFSObjects_SkipsRetriedQueuedFSObject(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	libID := uuid.New()
	deletedAt := time.Now().Add(-24 * time.Hour).UTC().Truncate(time.Millisecond)
	store.AddOrganization(orgID)
	store.AddDeletedLibrary(orgID, libID, "hot", deletedAt)
	store.AddFSObject(libID, "fs-orphan-1", "file", []string{"blk-1"})
	store.SeedQueueItemForTest(orgID, deletedAt, ItemFSObject, "fs-orphan-1", libID, "", 0)

	items, err := store.DequeueBatch(orgID, 1, time.Now())
	if err != nil || len(items) != 1 {
		t.Fatalf("DequeueBatch failed: %v / items=%d", err, len(items))
	}
	if err := q.IncrementRetry(items[0]); err != nil {
		t.Fatalf("IncrementRetry failed: %v", err)
	}

	if n, err := s.scanOrphanedFSObjects(context.Background()); err != nil || n != 0 {
		t.Fatalf("scanOrphanedFSObjects after retry = (%d, %v), want (0, nil)", n, err)
	}
	if got := len(store.QueueItems(orgID)); got != 1 {
		t.Fatalf("expected 1 queued orphaned fs_object after dedupe, got %d", got)
	}
}

func TestScanner_ScanOrphanedFSObjects_DoesNotCrossSuppressAcrossLibraries(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	libPending := uuid.New()
	libOrphan := uuid.New()
	deletedAt := time.Now().Add(-24 * time.Hour).UTC().Truncate(time.Millisecond)
	store.AddOrganization(orgID)
	store.AddLibrary(orgID, libPending, "hot")
	store.AddDeletedLibrary(orgID, libOrphan, "hot", deletedAt)
	store.mu.Lock()
	delete(store.libraries, libOrphan)
	store.mu.Unlock()
	store.AddFSObject(libPending, "shared-fs", "file", []string{"blk-pending"})
	store.AddFSObject(libOrphan, "shared-fs", "file", []string{"blk-orphan"})
	store.SeedQueueItemForTest(orgID, deletedAt, ItemFSObject, "shared-fs", libPending, "", 0)

	if n, err := s.scanOrphanedFSObjects(context.Background()); err != nil || n != 1 {
		t.Fatalf("scanOrphanedFSObjects = (%d, %v), want (1, nil)", n, err)
	}

	orphanCount := 0
	for _, item := range store.QueueItems(orgID) {
		if item.ItemType == ItemFSObject && item.LibraryID == libOrphan && item.ItemID == "shared-fs" {
			orphanCount++
		}
	}
	if orphanCount != 1 {
		t.Fatalf("expected orphaned library fs_object to enqueue once despite same fs id in another library, got %d", orphanCount)
	}
}

func TestScanner_ScanOrphanedFSObjects_SkipsUnresolvableRepresentation(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	libID := uuid.New()
	deletedAt := time.Now().Add(-24 * time.Hour).UTC().Truncate(time.Millisecond)
	store.AddOrganization(orgID)
	store.AddDeletedLibrary(orgID, libID, "hot", deletedAt)
	store.mu.Lock()
	store.deletedLibraries[libID].BlockRepresentationID = ""
	delete(store.libraries, libID)
	store.mu.Unlock()
	store.AddFSObject(libID, "fs-orphan-legacy", "file", []string{"blk-legacy"})

	n, err := s.scanOrphanedFSObjects(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("scanOrphanedFSObjects = (%d, %v), want (0, nil)", n, err)
	}
	items := store.QueueItems(orgID)
	if len(items) != 0 {
		t.Fatalf("expected no queued work, got %#v", items)
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

// TestScanner_ScanExpiredVersions_DefaultsMissingPlaintextRepresentation pins the
// chosen policy: a plaintext library with an empty stored block_representation_id
// is NOT skipped — the scanner derives the safe default (plain:v1), processes the
// library, and reports the empty stored value as drift so a writer/migration that
// failed to stamp it stays visible.
func TestScanner_ScanExpiredVersions_DefaultsMissingPlaintextRepresentation(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	store.AddOrganization(orgID)
	libID := uuid.New()
	store.AddLibraryWithTTL(orgID, libID, "hot", "commit-head", 1)
	store.mu.Lock()
	store.libraries[libID].BlockRepresentationID = ""
	store.mu.Unlock()
	store.AddCommitWithDetails(libID, "commit-head", "fs-1", "", time.Now())
	store.AddCommitWithDetails(libID, "commit-expired", "fs-2", "", time.Now().Add(-48*time.Hour))

	beforeDrift := testutil.ToFloat64(metrics.GCAuditEventsTotal.WithLabelValues("gc_library_representation_defaulted"))

	n, err := s.scanExpiredVersions(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("scanExpiredVersions = (%d, %v), want (1, nil)", n, err)
	}
	items := store.QueueItems(orgID)
	if len(items) != 1 {
		t.Fatalf("expected 1 queued commit for defaulted plaintext library, got %#v", items)
	}
	if items[0].BlockRepresentationID != db.PlainBlockRepresentationID {
		t.Fatalf("queued item representation = %q, want %q", items[0].BlockRepresentationID, db.PlainBlockRepresentationID)
	}
	if afterDrift := testutil.ToFloat64(metrics.GCAuditEventsTotal.WithLabelValues("gc_library_representation_defaulted")); afterDrift != beforeDrift+1 {
		t.Fatalf("drift metric = %v, want %v", afterDrift, beforeDrift+1)
	}
}

func TestScanner_ScanAutoDeleteExpiredObjects_DefaultsMissingPlaintextRepresentation(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	store.AddOrganization(orgID)
	libID := uuid.New()
	store.AddLibraryWithAutoDelete(orgID, libID, "hot", "commit-head", 1)
	store.mu.Lock()
	store.libraries[libID].BlockRepresentationID = ""
	store.mu.Unlock()
	store.AddCommitWithDetails(libID, "commit-head", "fs-root", "", time.Now())
	store.AddFSObjectWithEntries(libID, "fs-root", "dir", nil, []string{"fs-file1"})
	store.AddFSObject(libID, "fs-file1", "file", []string{"blk-1"})
	store.AddFSObject(libID, "fs-orphan", "file", []string{"blk-2"})

	beforeDrift := testutil.ToFloat64(metrics.GCAuditEventsTotal.WithLabelValues("gc_library_representation_defaulted"))

	n, err := s.scanAutoDeleteExpiredObjects(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("scanAutoDeleteExpiredObjects = (%d, %v), want (1, nil)", n, err)
	}
	items := store.QueueItems(orgID)
	if len(items) != 1 {
		t.Fatalf("expected 1 queued orphan fs_object for defaulted plaintext library, got %#v", items)
	}
	if items[0].ItemID != "fs-orphan" || items[0].BlockRepresentationID != db.PlainBlockRepresentationID {
		t.Fatalf("queued item = %s/%q, want fs-orphan/%q", items[0].ItemID, items[0].BlockRepresentationID, db.PlainBlockRepresentationID)
	}
	if afterDrift := testutil.ToFloat64(metrics.GCAuditEventsTotal.WithLabelValues("gc_library_representation_defaulted")); afterDrift != beforeDrift+1 {
		t.Fatalf("drift metric = %v, want %v", afterDrift, beforeDrift+1)
	}
}

// TestScanner_ScanExpiredVersions_DefaultsMissingEncryptedRepresentation pins the
// encrypted half of the policy: an encrypted library with an empty stored
// block_representation_id derives library:<id> (not plain:v1), processes, and
// reports drift.
func TestScanner_ScanExpiredVersions_DefaultsMissingEncryptedRepresentation(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	store.AddOrganization(orgID)
	libID := uuid.New()
	store.AddLibraryWithTTL(orgID, libID, "hot", "commit-head", 1)
	store.mu.Lock()
	store.libraries[libID].BlockRepresentationID = ""
	store.mu.Unlock()
	store.SetLibraryEncrypted(libID, true)
	store.AddCommitWithDetails(libID, "commit-head", "fs-1", "", time.Now())
	store.AddCommitWithDetails(libID, "commit-expired", "fs-2", "", time.Now().Add(-48*time.Hour))

	wantRep := db.EncryptedLibraryBlockRepresentationID(libID.String())
	beforeDrift := testutil.ToFloat64(metrics.GCAuditEventsTotal.WithLabelValues("gc_library_representation_defaulted"))

	n, err := s.scanExpiredVersions(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("scanExpiredVersions = (%d, %v), want (1, nil)", n, err)
	}
	items := store.QueueItems(orgID)
	if len(items) != 1 {
		t.Fatalf("expected 1 queued commit, got %#v", items)
	}
	if items[0].BlockRepresentationID != wantRep {
		t.Fatalf("queued item representation = %q, want %q", items[0].BlockRepresentationID, wantRep)
	}
	if afterDrift := testutil.ToFloat64(metrics.GCAuditEventsTotal.WithLabelValues("gc_library_representation_defaulted")); afterDrift != beforeDrift+1 {
		t.Fatalf("drift metric = %v, want %v", afterDrift, beforeDrift+1)
	}
}

func TestScanner_ScanAutoDeleteExpiredObjects_DefaultsMissingEncryptedRepresentation(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	store.AddOrganization(orgID)
	libID := uuid.New()
	store.AddLibraryWithAutoDelete(orgID, libID, "hot", "commit-head", 1)
	store.mu.Lock()
	store.libraries[libID].BlockRepresentationID = ""
	store.mu.Unlock()
	store.SetLibraryEncrypted(libID, true)
	store.AddCommitWithDetails(libID, "commit-head", "fs-root", "", time.Now())
	store.AddFSObjectWithEntries(libID, "fs-root", "dir", nil, []string{"fs-file1"})
	store.AddFSObject(libID, "fs-file1", "file", []string{"blk-1"})
	store.AddFSObject(libID, "fs-orphan", "file", []string{"blk-2"})

	wantRep := db.EncryptedLibraryBlockRepresentationID(libID.String())
	beforeDrift := testutil.ToFloat64(metrics.GCAuditEventsTotal.WithLabelValues("gc_library_representation_defaulted"))

	n, err := s.scanAutoDeleteExpiredObjects(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("scanAutoDeleteExpiredObjects = (%d, %v), want (1, nil)", n, err)
	}
	items := store.QueueItems(orgID)
	if len(items) != 1 {
		t.Fatalf("expected 1 queued orphan fs_object, got %#v", items)
	}
	if items[0].ItemID != "fs-orphan" || items[0].BlockRepresentationID != wantRep {
		t.Fatalf("queued item = %s/%q, want fs-orphan/%q", items[0].ItemID, items[0].BlockRepresentationID, wantRep)
	}
	if afterDrift := testutil.ToFloat64(metrics.GCAuditEventsTotal.WithLabelValues("gc_library_representation_defaulted")); afterDrift != beforeDrift+1 {
		t.Fatalf("drift metric = %v, want %v", afterDrift, beforeDrift+1)
	}
}

func TestScanner_ScanExpiredVersions_SkipsLibraryWithNonCanonicalRepresentation(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	store.AddOrganization(orgID)
	libID := uuid.New()
	store.AddLibraryWithTTL(orgID, libID, "hot", "commit-head", 1)
	store.mu.Lock()
	store.libraries[libID].BlockRepresentationID = "library:{" + libID.String() + "}"
	store.mu.Unlock()
	store.AddCommitWithDetails(libID, "commit-head", "fs-1", "", time.Now())
	store.AddCommitWithDetails(libID, "commit-expired", "fs-2", "", time.Now().Add(-48*time.Hour))

	n, err := s.scanExpiredVersions(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("scanExpiredVersions = (%d, %v), want (0, nil)", n, err)
	}
	if items := store.QueueItems(orgID); len(items) != 0 {
		t.Fatalf("expected no queued work for library with non-canonical representation, got %#v", items)
	}
}

// TestScanner_ScanExpiredVersions_SkipsCrossDomainRepresentation pins that a
// syntactically canonical but domain-crossed stored representation — an encrypted
// library stamped plain:v1 — is skipped as drift, not enqueued under the wrong
// SHA-1 mapping domain. validateQueueItemBlockRepresentation alone cannot catch
// this (it never sees the encrypted flag); the list method now flags it via
// RepresentationInvalid so the scanner fails closed like the delete paths.
func TestScanner_ScanExpiredVersions_SkipsCrossDomainRepresentation(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	store.AddOrganization(orgID)
	libID := uuid.New()
	store.AddLibraryWithTTL(orgID, libID, "hot", "commit-head", 1)
	store.mu.Lock()
	store.libraries[libID].BlockRepresentationID = db.PlainBlockRepresentationID
	store.mu.Unlock()
	store.SetLibraryEncrypted(libID, true)
	store.AddCommitWithDetails(libID, "commit-head", "fs-1", "", time.Now())
	store.AddCommitWithDetails(libID, "commit-expired", "fs-2", "", time.Now().Add(-48*time.Hour))

	beforeDrift := testutil.ToFloat64(metrics.GCAuditEventsTotal.WithLabelValues("gc_library_representation_invalid"))

	n, err := s.scanExpiredVersions(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("scanExpiredVersions = (%d, %v), want (0, nil)", n, err)
	}
	if items := store.QueueItems(orgID); len(items) != 0 {
		t.Fatalf("expected no queued work for cross-domain representation, got %#v", items)
	}
	if afterDrift := testutil.ToFloat64(metrics.GCAuditEventsTotal.WithLabelValues("gc_library_representation_invalid")); afterDrift != beforeDrift+1 {
		t.Fatalf("invalid drift metric = %v, want %v", afterDrift, beforeDrift+1)
	}
}

func TestScanner_ScanAutoDeleteExpiredObjects_SkipsCrossDomainRepresentation(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	store.AddOrganization(orgID)
	libID := uuid.New()
	store.AddLibraryWithAutoDelete(orgID, libID, "hot", "commit-head", 1)
	// Plaintext library (encrypted stays false) stamped with an encrypted
	// per-library representation: canonical for the UUID but the wrong domain.
	store.mu.Lock()
	store.libraries[libID].BlockRepresentationID = db.EncryptedLibraryBlockRepresentationID(libID.String())
	store.mu.Unlock()
	store.AddCommitWithDetails(libID, "commit-head", "fs-root", "", time.Now())
	store.AddFSObjectWithEntries(libID, "fs-root", "dir", nil, []string{"fs-file1"})
	store.AddFSObject(libID, "fs-file1", "file", []string{"blk-1"})
	store.AddFSObject(libID, "fs-orphan", "file", []string{"blk-2"})

	beforeDrift := testutil.ToFloat64(metrics.GCAuditEventsTotal.WithLabelValues("gc_library_representation_invalid"))

	n, err := s.scanAutoDeleteExpiredObjects(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("scanAutoDeleteExpiredObjects = (%d, %v), want (0, nil)", n, err)
	}
	if items := store.QueueItems(orgID); len(items) != 0 {
		t.Fatalf("expected no queued work for cross-domain representation, got %#v", items)
	}
	if afterDrift := testutil.ToFloat64(metrics.GCAuditEventsTotal.WithLabelValues("gc_library_representation_invalid")); afterDrift != beforeDrift+1 {
		t.Fatalf("invalid drift metric = %v, want %v", afterDrift, beforeDrift+1)
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

func TestScanner_ScanExpiredVersions_SkipsRetriedQueuedCommit(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	libID := uuid.New()
	store.AddOrganization(orgID)
	store.AddLibraryWithTTL(orgID, libID, "hot", "head", 1)

	old := time.Now().Add(-72 * time.Hour)
	store.AddCommitWithDetails(libID, "head", "fs-head", "", old)
	store.AddCommitWithDetails(libID, "old-commit", "fs-old", "", old)
	store.SeedQueueItemForTest(orgID, old.UTC().Truncate(time.Millisecond), ItemCommit, "old-commit", libID, "", 0)

	items, err := store.DequeueBatch(orgID, 1, time.Now())
	if err != nil || len(items) != 1 {
		t.Fatalf("DequeueBatch failed: %v / items=%d", err, len(items))
	}
	if err := q.IncrementRetry(items[0]); err != nil {
		t.Fatalf("IncrementRetry failed: %v", err)
	}

	if n, err := s.scanExpiredVersions(context.Background()); err != nil || n != 0 {
		t.Fatalf("scanExpiredVersions after retry = (%d, %v), want (0, nil)", n, err)
	}
	if got := len(store.QueueItems(orgID)); got != 1 {
		t.Fatalf("expected 1 queued expired commit after dedupe, got %d", got)
	}
}

func TestScanner_ScanExpiredVersions_DoesNotCrossSuppressAcrossLibraries(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	libPending := uuid.New()
	libExpired := uuid.New()
	store.AddOrganization(orgID)
	store.AddLibraryWithTTL(orgID, libPending, "hot", "head-pending", 1)
	store.AddLibraryWithTTL(orgID, libExpired, "hot", "head-expired", 1)

	old := time.Now().Add(-72 * time.Hour)
	store.AddCommitWithDetails(libPending, "head-pending", "fs-head-pending", "", old)
	store.AddCommitWithDetails(libPending, "shared-commit", "fs-pending", "", old)
	store.AddCommitWithDetails(libExpired, "head-expired", "fs-head-expired", "", old)
	store.AddCommitWithDetails(libExpired, "shared-commit", "fs-expired", "", old)
	store.SeedQueueItemForTest(orgID, old.UTC().Truncate(time.Millisecond), ItemCommit, "shared-commit", libPending, "", 0)

	if n, err := s.scanExpiredVersions(context.Background()); err != nil || n != 1 {
		t.Fatalf("scanExpiredVersions = (%d, %v), want (1, nil)", n, err)
	}

	expiredCount := 0
	for _, item := range store.QueueItems(orgID) {
		if item.ItemType == ItemCommit && item.LibraryID == libExpired && item.ItemID == "shared-commit" {
			expiredCount++
		}
	}
	if expiredCount != 1 {
		t.Fatalf("expected expired commit in second library to enqueue despite same id pending elsewhere, got %d", expiredCount)
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

func TestScanner_ScanAutoDeleteExpiredObjects_SkipsRetriedQueuedFSObject(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	libID := uuid.New()
	store.AddOrganization(orgID)
	store.AddLibraryWithAutoDelete(orgID, libID, "hot", "commit-head", 1)

	now := time.Now()
	store.AddCommitWithDetails(libID, "commit-head", "fs-root", "", now)
	store.AddFSObjectWithEntries(libID, "fs-root", "dir", nil, []string{"fs-keep"})
	store.AddFSObject(libID, "fs-keep", "file", []string{"blk-keep"})
	store.AddFSObject(libID, "fs-orphan", "file", []string{"blk-orphan"})
	store.SeedQueueItemForTest(orgID, now.Add(-2*time.Hour).UTC().Truncate(time.Millisecond), ItemFSObject, "fs-orphan", libID, "", 0)

	items, err := store.DequeueBatch(orgID, 1, time.Now())
	if err != nil || len(items) != 1 {
		t.Fatalf("DequeueBatch failed: %v / items=%d", err, len(items))
	}
	if err := q.IncrementRetry(items[0]); err != nil {
		t.Fatalf("IncrementRetry failed: %v", err)
	}

	if n, err := s.scanAutoDeleteExpiredObjects(context.Background()); err != nil || n != 0 {
		t.Fatalf("scanAutoDeleteExpiredObjects after retry = (%d, %v), want (0, nil)", n, err)
	}
	if got := len(store.QueueItems(orgID)); got != 1 {
		t.Fatalf("expected 1 queued auto-delete fs_object after dedupe, got %d", got)
	}
}

func TestScanner_ScanAutoDeleteExpiredObjects_DoesNotCrossSuppressAcrossLibraries(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	libPending := uuid.New()
	libExpired := uuid.New()
	store.AddOrganization(orgID)
	store.AddLibraryWithAutoDelete(orgID, libPending, "hot", "head-pending", 1)
	store.AddLibraryWithAutoDelete(orgID, libExpired, "hot", "head-expired", 1)

	now := time.Now()
	store.AddCommitWithDetails(libPending, "head-pending", "fs-root-pending", "", now)
	store.AddFSObjectWithEntries(libPending, "fs-root-pending", "dir", nil, nil)
	store.AddFSObject(libPending, "shared-fs", "file", []string{"blk-pending"})
	store.AddCommitWithDetails(libExpired, "head-expired", "fs-root-expired", "", now)
	store.AddFSObjectWithEntries(libExpired, "fs-root-expired", "dir", nil, nil)
	store.AddFSObject(libExpired, "shared-fs", "file", []string{"blk-expired"})
	store.SeedQueueItemForTest(orgID, now.Add(-2*time.Hour).UTC().Truncate(time.Millisecond), ItemFSObject, "shared-fs", libPending, "", 0)

	if n, err := s.scanAutoDeleteExpiredObjects(context.Background()); err != nil || n != 1 {
		t.Fatalf("scanAutoDeleteExpiredObjects = (%d, %v), want (1, nil)", n, err)
	}

	expiredCount := 0
	for _, item := range store.QueueItems(orgID) {
		if item.ItemType == ItemFSObject && item.LibraryID == libExpired && item.ItemID == "shared-fs" {
			expiredCount++
		}
	}
	if expiredCount != 1 {
		t.Fatalf("expected auto-delete fs_object in second library to enqueue despite same id pending elsewhere, got %d", expiredCount)
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

func TestScanner_ScanAutoDeleteExpiredObjects_SkipsLibraryOnKeepTreeReadFailure(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	orgID := uuid.New()
	libID := uuid.New()
	store.AddOrganization(orgID)
	store.AddLibraryWithAutoDelete(orgID, libID, "hot", "commit-head", 1)

	now := time.Now()
	store.AddCommitWithDetails(libID, "commit-head", "fs-root", "", now)
	store.AddFSObjectWithEntries(libID, "fs-root", "dir", nil, []string{"fs-live-subdir"})
	store.AddFSObjectWithEntries(libID, "fs-live-subdir", "dir", nil, []string{"fs-live-file"})
	store.AddFSObject(libID, "fs-live-file", "file", []string{"blk-live"})
	store.AddFSObject(libID, "fs-orphan", "file", []string{"blk-orphan"})
	store.SetGetFSObjectError(libID, "fs-live-subdir", errors.New("transient read failure"))

	n, err := s.scanAutoDeleteExpiredObjects(context.Background())
	if err != nil {
		t.Fatalf("scanAutoDeleteExpiredObjects failed: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 fs_objects enqueued after keep-tree read failure, got %d", n)
	}
	if got := len(store.QueueItems(orgID)); got != 0 {
		t.Fatalf("expected library auto-delete to fail closed, got %d queued items", got)
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

// TestScanner_ScanExpiredDeletedLibraries_RecoversUnstampedMarker is the
// regression guard for the trash-stuck bug: the canonical soft-delete path writes
// the deleted_libraries marker WITHOUT block_representation_id, so the marker is
// empty even though the surviving library row still carries the representation.
// Phase 13 must recover it from the library row and enqueue the cascade (stamped),
// not skip the library and strand it in trash forever.
func TestScanner_ScanExpiredDeletedLibraries_RecoversUnstampedMarker(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{TrashRetentionDays: 30})

	orgID := uuid.New()
	store.AddOrganization(orgID)

	expiredLib := uuid.New()
	store.AddDeletedLibrary(orgID, expiredLib, "hot", time.Now().AddDate(0, 0, -45))
	// Simulate the unstamped write path: the marker has no representation, but the
	// live (soft-deleted) library row still carries plain:v1.
	store.mu.Lock()
	store.deletedLibraries[expiredLib].BlockRepresentationID = ""
	store.mu.Unlock()

	beforeDrift := testutil.ToFloat64(metrics.GCAuditEventsTotal.WithLabelValues("gc_library_representation_defaulted"))

	n, err := s.scanExpiredDeletedLibraries(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("scanExpiredDeletedLibraries = (%d, %v), want (1, nil)", n, err)
	}
	items := store.QueueItems(orgID)
	if len(items) != 1 || items[0].ItemType != ItemLibraryCascade {
		t.Fatalf("expected 1 library_cascade item, got %#v", items)
	}
	if items[0].BlockRepresentationID != db.PlainBlockRepresentationID {
		t.Fatalf("recovered cascade representation = %q, want %q", items[0].BlockRepresentationID, db.PlainBlockRepresentationID)
	}
	if afterDrift := testutil.ToFloat64(metrics.GCAuditEventsTotal.WithLabelValues("gc_library_representation_defaulted")); afterDrift != beforeDrift+1 {
		t.Fatalf("drift metric = %v, want %v", afterDrift, beforeDrift+1)
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

func TestScanner_ScanExpiredDeletedLibraries_SkipsInvalidRepresentationWithoutPoisoningBatch(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{TrashRetentionDays: 30})

	orgID := uuid.New()
	store.AddOrganization(orgID)

	validLib := uuid.New()
	invalidLib := uuid.New()
	deletedAt := time.Now().AddDate(0, 0, -45)
	store.AddDeletedLibrary(orgID, validLib, "hot", deletedAt)
	store.AddDeletedLibrary(orgID, invalidLib, "hot", deletedAt)

	store.mu.Lock()
	store.deletedLibraries[invalidLib].BlockRepresentationID = "library:{" + invalidLib.String() + "}"
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
		t.Fatalf("expected exactly 1 queued item, got %d", len(items))
	}
	if items[0].ItemType != ItemLibraryCascade || items[0].ItemID != validLib.String() {
		t.Fatalf("queued item = %#v, want library_cascade for valid library %s", items[0], validLib)
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

func TestScanner_ScanExpiredDeletedLibraries_SkipsOpenFailedCascade(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{TrashRetentionDays: 30})

	orgID := uuid.New()
	libID := uuid.New()
	deletedAt := time.Now().AddDate(0, 0, -45).UTC().Truncate(time.Millisecond)
	failedAt := time.Now().Add(-time.Hour).UTC().Truncate(time.Millisecond)
	store.AddOrganization(orgID)
	store.AddDeletedLibrary(orgID, libID, "hot", deletedAt)
	store.failedItems[orgID] = []GCFailedItemInfo{{
		OrgID:        orgID,
		FailedAt:     failedAt,
		ExpiresAt:    failedAt.Add(gcFailedItemRetention),
		QueuedAt:     deletedAt.Add(2 * time.Minute),
		IdentityAt:   deletedAt,
		ItemType:     ItemLibraryCascade,
		ItemID:       libID.String(),
		StorageClass: "hot",
		RetryCount:   5,
		LastError:    "scanner should not recreate this while DLQ entry is open",
	}}

	if n, err := s.scanExpiredDeletedLibraries(context.Background()); err != nil || n != 0 {
		t.Fatalf("scanExpiredDeletedLibraries with open failed cascade = (%d, %v), want (0, nil)", n, err)
	}
	if got := len(store.QueueItems(orgID)); got != 0 {
		t.Fatalf("expected no live queue rows while failed cascade remains open, got %d", got)
	}
}

func TestScanner_ScanExpiredDeletedLibraries_ExpiredFailedMarkerDoesNotSuppressFreshCascade(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{TrashRetentionDays: 30})

	orgID := uuid.New()
	libID := uuid.New()
	deletedAt := time.Now().AddDate(0, 0, -45).UTC().Truncate(time.Millisecond)
	failedAt := time.Now().Add(-gcFailedItemRetention - time.Hour).UTC().Truncate(time.Millisecond)
	store.AddOrganization(orgID)
	store.AddDeletedLibrary(orgID, libID, "hot", deletedAt)

	failedItem := QueueItem{
		OrgID:                 orgID,
		QueuedAt:              deletedAt,
		IdentityAt:            deletedAt,
		ItemType:              ItemLibraryCascade,
		ItemID:                libID.String(),
		LibraryID:             uuid.Nil,
		BlockRepresentationID: db.PlainBlockRepresentationID,
		StorageClass:          "hot",
	}
	if err := store.EnqueueBatch([]QueueItem{failedItem}); err != nil {
		t.Fatalf("failed to seed queue item: %v", err)
	}
	if err := store.FailItem(failedItem, failedAt, "old failure", GCFailureCodeNone); err != nil {
		t.Fatalf("FailItem failed: %v", err)
	}

	if n, err := s.scanExpiredFailedItems(context.Background()); err != nil || n != 1 {
		t.Fatalf("scanExpiredFailedItems = (%d, %v), want (1, nil)", n, err)
	}
	if got := len(store.FailedItems(orgID)); got != 0 {
		t.Fatalf("expected expired failed item to be removed, got %d", got)
	}

	if n, err := s.scanExpiredDeletedLibraries(context.Background()); err != nil || n != 1 {
		t.Fatalf("scanExpiredDeletedLibraries after failed-row expiry = (%d, %v), want (1, nil)", n, err)
	}
	if got := len(store.QueueItems(orgID)); got != 1 {
		t.Fatalf("expected fresh library cascade after stale failed marker expiry, got %d queued items", got)
	}
}

func TestScanner_ScanExpiredDeletedLibraries_SkipsRetriedQueuedCascade(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{TrashRetentionDays: 30})

	orgID := uuid.New()
	libID := uuid.New()
	deletedAt := time.Now().AddDate(0, 0, -45).UTC().Truncate(time.Millisecond)
	store.AddOrganization(orgID)
	store.AddDeletedLibrary(orgID, libID, "hot", deletedAt)
	store.SeedQueueItemForTest(orgID, deletedAt, ItemLibraryCascade, libID.String(), uuid.Nil, "hot", 0)

	items, err := store.DequeueBatch(orgID, 1, time.Now())
	if err != nil || len(items) != 1 {
		t.Fatalf("DequeueBatch failed: %v / items=%d", err, len(items))
	}
	if err := q.IncrementRetry(items[0]); err != nil {
		t.Fatalf("IncrementRetry failed: %v", err)
	}

	if n, err := s.scanExpiredDeletedLibraries(context.Background()); err != nil || n != 0 {
		t.Fatalf("scanExpiredDeletedLibraries after retry = (%d, %v), want (0, nil)", n, err)
	}
	if got := len(store.QueueItems(orgID)); got != 1 {
		t.Fatalf("expected 1 queued library cascade after scanner dedupe, got %d", got)
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
