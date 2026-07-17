package gc

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"
)

// shortRetries trims the S3 retry backoff so tests don't sleep for seconds.
// Returns a cleanup func that restores the original schedule.
func shortRetries(t *testing.T) func() {
	t.Helper()
	orig := s3DeleteRetryDelays
	s3DeleteRetryDelays = []time.Duration{1 * time.Millisecond, 1 * time.Millisecond, 1 * time.Millisecond}
	return func() { s3DeleteRetryDelays = orig }
}

// TestWorker_ProcessBlock_S3RetrySucceeds verifies that a transient S3 failure
// recovers within the retry budget and does NOT leave an orphan row.
func TestWorker_ProcessBlock_S3RetrySucceeds(t *testing.T) {
	defer shortRetries(t)()

	store := NewMockStore()
	sp := &MockStorageProvider{}
	// Fail the first 2 attempts, then succeed.
	sp.FailNextN(2, errors.New("transient s3 outage"))
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)

	orgID := uuid.New()
	store.AddBlock(orgID, "block-retry", "hot", 0)
	store.EnqueueItem(orgID, time.Now().Add(-2*time.Hour), ItemBlock, "block-retry", uuid.Nil, "hot", 0)

	n, err := w.ProcessOnce(context.Background())
	if err != nil {
		t.Fatalf("ProcessOnce: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 processed, got %d", n)
	}
	if store.S3OrphanCount() != 0 {
		t.Errorf("expected no orphans, got %d", store.S3OrphanCount())
	}
	if stats.BlocksDeleted() != 1 {
		t.Errorf("BlocksDeleted=%d, want 1", stats.BlocksDeleted())
	}
	deletes := sp.ScopedBlockDeletes()
	if len(deletes) != 1 || deletes[0] != (ScopedBlockDelete{OrgID: orgID.String(), StorageClass: "hot", BlockID: "block-retry"}) {
		t.Errorf("unexpected scoped S3 deletes: %+v", deletes)
	}
}

// TestWorker_ProcessBlock_S3RetryExhausted verifies that when all retries are
// exhausted, the block is recorded in gc_s3_orphans and the DB cleanup
// continues (mapping deleted, candidate cleared, stats incremented). Critically
// the queue item completes successfully so it does NOT re-enter the LWT path
// that would skip S3 deletion forever.
func TestWorker_ProcessBlock_S3RetryExhausted(t *testing.T) {
	defer shortRetries(t)()

	store := NewMockStore()
	sp := &MockStorageProvider{}
	sp.FailAlways(errors.New("s3 down"))
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)

	orgID := uuid.New()
	store.AddBlock(orgID, "block-perma", "cold", 0)
	store.AddBlockMapping(orgID, "sha1-xyz", "block-perma")
	store.EnqueueItem(orgID, time.Now().Add(-2*time.Hour), ItemBlock, "block-perma", uuid.Nil, "cold", 0)

	n, err := w.ProcessOnce(context.Background())
	if err != nil {
		t.Fatalf("ProcessOnce: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 processed (queue item completed), got %d", n)
	}
	if store.S3OrphanCount() != 1 {
		t.Fatalf("expected 1 orphan recorded, got %d", store.S3OrphanCount())
	}
	orphans := store.AllS3Orphans()
	if len(orphans) != 1 {
		t.Fatalf("expected 1 orphan for org, got %d", len(orphans))
	}
	if orphans[0].BlockID != "block-perma" || orphans[0].StorageClass != "cold" {
		t.Errorf("unexpected orphan info: %+v", orphans[0])
	}
	if orphans[0].LastError == "" {
		t.Error("orphan should record the last error message")
	}
	if orphans[0].ExternalSHA1 != "sha1-xyz" {
		t.Errorf("orphan external sha1 = %q, want sha1-xyz", orphans[0].ExternalSHA1)
	}
	if orphans[0].RecoveryPhase != S3OrphanPhasePendingS3 {
		t.Errorf("orphan recovery phase = %q, want %q", orphans[0].RecoveryPhase, S3OrphanPhasePendingS3)
	}

	// DB cleanup must have happened even though S3 failed.
	if store.GetBlock(orgID, "block-perma") != nil {
		t.Error("block DB row should be gone after LWT delete")
	}
	if store.ForwardBlockMappingExists(orgID, "sha1-xyz") {
		t.Error("expected forward block mapping cleaned up via blocks.sha1")
	}
	if stats.BlocksDeleted() != 1 {
		t.Errorf("BlocksDeleted=%d, want 1 (logical deletion counts even with S3 orphan)", stats.BlocksDeleted())
	}
}

func TestWorker_ProcessBlock_UsesExistingOrphanFirstSeenAtForCleanup(t *testing.T) {
	defer shortRetries(t)()

	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)

	orgID := uuid.New()
	firstSeenAt := time.Now().Add(-24 * time.Hour).UTC().Truncate(time.Millisecond)
	store.AddBlock(orgID, "block-orphan-cleanup", "hot", 0)
	if _, err := store.RecordS3Orphan(orgID, "block-orphan-cleanup", "hot", db.PlainBlockRepresentationID, "", "previous failure", firstSeenAt); err != nil {
		t.Fatalf("seed orphan: %v", err)
	}
	if err := store.EnqueueItem(orgID, time.Now().Add(-2*time.Hour), ItemBlock, "block-orphan-cleanup", uuid.Nil, "hot", 0); err != nil {
		t.Fatalf("EnqueueItem failed: %v", err)
	}

	n, err := w.ProcessOnce(context.Background())
	if err != nil {
		t.Fatalf("ProcessOnce: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 processed, got %d", n)
	}
	if got := store.S3OrphanCount(); got != 0 {
		t.Fatalf("expected canonical orphan cleanup, got %d rows", got)
	}
	orphans, err := store.ListS3OrphansByDay(firstSeenAt, db.GCDiscoveryBucket(orgID.String(), "block-orphan-cleanup"), 10)
	if err != nil {
		t.Fatalf("ListS3OrphansByDay failed: %v", err)
	}
	if len(orphans) != 0 {
		t.Fatalf("expected discovery row cleanup using preserved first_seen_at, got %d rows", len(orphans))
	}
}

// TestWorker_RecoverS3Orphans_Success confirms that a previously-recorded
// orphan is retried and the row is removed when S3 delete finally succeeds.
func TestWorker_RecoverS3Orphans_Success(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)

	orgID := uuid.New()
	// Seed an orphan directly.
	if _, err := store.RecordS3Orphan(orgID, "orph-1", " hot ", db.PlainBlockRepresentationID, "", "earlier failure", time.Now()); err != nil {
		t.Fatalf("seed orphan: %v", err)
	}

	recovered, err := w.RecoverS3Orphans(context.Background(), 100)
	if err != nil {
		t.Fatalf("RecoverS3Orphans: %v", err)
	}
	if recovered != 1 {
		t.Errorf("recovered=%d, want 1", recovered)
	}
	if store.S3OrphanCount() != 0 {
		t.Errorf("orphan should be cleared, got %d", store.S3OrphanCount())
	}
	deletes := sp.ScopedBlockDeletes()
	if len(deletes) != 1 || deletes[0] != (ScopedBlockDelete{OrgID: orgID.String(), StorageClass: "hot", BlockID: "orph-1"}) {
		t.Errorf("unexpected scoped S3 deletes: %+v", deletes)
	}
}

func TestWorker_RecoverS3Orphans_EmptyStorageClassFailsClosed(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	w := NewWorker(store, sp, NewQueue(store), 100, 0, false, &Stats{})

	orgID := uuid.New()
	if _, err := store.RecordS3Orphan(orgID, "orph-empty-class", "", db.PlainBlockRepresentationID, "", "earlier failure", time.Now()); err != nil {
		t.Fatalf("seed orphan: %v", err)
	}

	recovered, err := w.RecoverS3Orphans(context.Background(), 100)
	if err == nil {
		t.Fatal("RecoverS3Orphans() error = nil, want empty storage class error")
	}
	if recovered != 0 {
		t.Fatalf("recovered = %d, want 0", recovered)
	}
	if store.S3OrphanCount() != 1 {
		t.Fatal("orphan row must remain for repair/retry")
	}
	if got := sp.BlockStoreRequests(); len(got) != 0 {
		t.Fatalf("storage must not be touched for empty class, got %+v", got)
	}
	if _, err := store.LoadGCStats(gcS3OrphansCursorKey); !errors.Is(err, gocql.ErrNotFound) {
		t.Fatalf("cursor advanced despite failed partition, err=%v", err)
	}
}

func TestWorker_RecoverS3Orphans_SameHashInTwoOrgsDeletesBothScopes(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	w := NewWorker(store, sp, NewQueue(store), 100, 0, false, &Stats{})

	orgA := uuid.New()
	orgB := uuid.New()
	for _, orgID := range []uuid.UUID{orgA, orgB} {
		if _, err := store.RecordS3Orphan(orgID, "shared-hash", "hot", db.PlainBlockRepresentationID, "", "earlier failure", time.Now()); err != nil {
			t.Fatalf("seed orphan for %s: %v", orgID, err)
		}
	}

	recovered, err := w.RecoverS3Orphans(context.Background(), 100)
	if err != nil {
		t.Fatalf("RecoverS3Orphans() error: %v", err)
	}
	if recovered != 2 {
		t.Fatalf("recovered = %d, want 2", recovered)
	}
	want := map[string]bool{orgA.String(): false, orgB.String(): false}
	for _, deletion := range sp.ScopedBlockDeletes() {
		if deletion.StorageClass != "hot" || deletion.BlockID != "shared-hash" {
			t.Fatalf("unexpected scoped deletion: %+v", deletion)
		}
		if _, ok := want[deletion.OrgID]; !ok {
			t.Fatalf("unexpected org deletion: %+v", deletion)
		}
		want[deletion.OrgID] = true
	}
	for orgID, found := range want {
		if !found {
			t.Fatalf("missing scoped deletion for org %s", orgID)
		}
	}
}

func TestWorker_RecoverS3Orphans_S3ThenMappingCleanup(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)

	orgID := uuid.New()
	store.AddBlockMapping(orgID, "sha1-recover", "orph-map")
	if _, err := store.RecordS3Orphan(orgID, "orph-map", "hot", db.PlainBlockRepresentationID, "sha1-recover", "prev", time.Now()); err != nil {
		t.Fatalf("seed orphan: %v", err)
	}

	recovered, err := w.RecoverS3Orphans(context.Background(), 100)
	if err != nil {
		t.Fatalf("RecoverS3Orphans: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("recovered=%d, want 1", recovered)
	}
	if store.S3OrphanCount() != 0 {
		t.Fatalf("orphan should be cleared, got %d", store.S3OrphanCount())
	}
	if store.ForwardBlockMappingExists(orgID, "sha1-recover") {
		t.Fatal("forward mapping should be cleaned up after S3 recovery")
	}
	deleted := sp.DeletedBlocks()
	if len(deleted) != 1 || deleted[0] != "orph-map" {
		t.Fatalf("expected one S3 delete for orph-map, got %v", deleted)
	}
}

func TestWorker_RecoverS3Orphans_CompletesPendingMappingCleanupWithoutS3(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)

	orgID := uuid.New()
	store.AddBlockMapping(orgID, "sha1-pending-cleanup", "orph-cleanup")
	firstSeenAt, err := store.RecordS3Orphan(orgID, "orph-cleanup", "hot", db.PlainBlockRepresentationID, "sha1-pending-cleanup", "", time.Now())
	if err != nil {
		t.Fatalf("seed orphan: %v", err)
	}
	if err := store.MarkS3OrphanMappingCleanupPending(orgID, "orph-cleanup", db.PlainBlockRepresentationID, "sha1-pending-cleanup", firstSeenAt.Add(5*time.Second)); err != nil {
		t.Fatalf("advance orphan phase: %v", err)
	}

	recovered, err := w.RecoverS3Orphans(context.Background(), 100)
	if err != nil {
		t.Fatalf("RecoverS3Orphans: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("recovered=%d, want 1", recovered)
	}
	if store.S3OrphanCount() != 0 {
		t.Fatalf("orphan should be cleared, got %d", store.S3OrphanCount())
	}
	if store.ForwardBlockMappingExists(orgID, "sha1-pending-cleanup") {
		t.Fatal("forward mapping should be cleaned up from pending mapping phase")
	}
	if got := sp.DeletedBlocks(); len(got) != 0 {
		t.Fatalf("S3 should not be touched in pending mapping phase, got %v", got)
	}
}

func TestWorker_RecoverS3Orphans_NewDeleteResetsStalePhaseAndStillDeletesS3(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)

	orgID := uuid.New()
	store.AddBlock(orgID, "blk-redelete", "hot", 0)
	store.AddBlockMapping(orgID, "sha1-new", "blk-redelete")
	firstSeenAt, err := store.RecordS3Orphan(orgID, "blk-redelete", "hot", db.PlainBlockRepresentationID, "sha1-old", "prev", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("seed stale orphan: %v", err)
	}
	if err := store.MarkS3OrphanMappingCleanupPending(orgID, "blk-redelete", db.PlainBlockRepresentationID, "sha1-old", firstSeenAt.Add(5*time.Minute)); err != nil {
		t.Fatalf("advance stale orphan phase: %v", err)
	}
	applied, err := store.ClaimBlockDelete(orgID, "blk-redelete", "claim-1")
	if err != nil || !applied {
		t.Fatalf("claim block delete: applied=%v err=%v", applied, err)
	}
	if _, err := store.StartBlockDeleteOrphan(orgID, "blk-redelete", "hot", db.PlainBlockRepresentationID, "sha1-new", time.Now().UTC()); err != nil {
		t.Fatalf("StartBlockDeleteOrphan: %v", err)
	}
	if err := store.FinalizeBlockDelete(orgID, "blk-redelete", "claim-1"); err != nil {
		t.Fatalf("FinalizeBlockDelete: %v", err)
	}

	recovered, err := w.RecoverS3Orphans(context.Background(), 100)
	if err != nil {
		t.Fatalf("RecoverS3Orphans: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("recovered=%d, want 1", recovered)
	}
	if store.S3OrphanCount() != 0 {
		t.Fatalf("orphan should be cleared, got %d", store.S3OrphanCount())
	}
	if store.ForwardBlockMappingExists(orgID, "sha1-new") {
		t.Fatal("forward mapping should be cleaned up after recovered S3 delete")
	}
	deleted := sp.DeletedBlocks()
	if len(deleted) != 1 || deleted[0] != "blk-redelete" {
		t.Fatalf("expected one S3 delete for blk-redelete, got %v", deleted)
	}
}

// TestWorker_RecoverS3Orphans_PendingMappingCleanupKeepsResurrectedBlockMapping
// pins the resurrection guard: if a crash leaves a recovery row at
// pending_mapping_cleanup and the same block_id is re-uploaded before recovery
// runs (deterministic content -> same block_id + SHA-1, so a live canonical row +
// forward mapping exist again), recovery must NOT delete that live forward
// mapping. It discards the stale recovery row instead.
func TestWorker_RecoverS3Orphans_PendingMappingCleanupKeepsResurrectedBlockMapping(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)

	orgID := uuid.New()
	// Re-uploaded block: live canonical row + live forward mapping.
	store.AddBlock(orgID, "blk-resurrected", "hot", 0)
	store.AddBlockMapping(orgID, "sha1-resurrected", "blk-resurrected")
	// Stale recovery row stuck at pending_mapping_cleanup from the earlier delete.
	firstSeenAt, err := store.RecordS3Orphan(orgID, "blk-resurrected", "hot", db.PlainBlockRepresentationID, "sha1-resurrected", "", time.Now())
	if err != nil {
		t.Fatalf("seed orphan: %v", err)
	}
	if err := store.MarkS3OrphanMappingCleanupPending(orgID, "blk-resurrected", db.PlainBlockRepresentationID, "sha1-resurrected", firstSeenAt.Add(5*time.Second)); err != nil {
		t.Fatalf("advance orphan phase: %v", err)
	}

	if _, err := w.RecoverS3Orphans(context.Background(), 100); err != nil {
		t.Fatalf("RecoverS3Orphans: %v", err)
	}

	if !store.ForwardBlockMappingExists(orgID, "sha1-resurrected") {
		t.Fatal("forward mapping of a resurrected block must NOT be deleted by recovery")
	}
	if store.S3OrphanCount() != 0 {
		t.Fatalf("expected stale recovery row discarded, got %d", store.S3OrphanCount())
	}
	if got := sp.DeletedBlocks(); len(got) != 0 {
		t.Fatalf("S3 must not be touched for a resurrected block, got %v", got)
	}
}

func TestWorker_RecoverS3Orphans_PendingMappingCleanupPreservesSiblingRepresentation(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)

	orgID := uuid.New()
	externalSHA1 := "sha1-shared"
	plainBlockID := "blk-plain"
	encBlockID := "blk-enc"
	encRep := db.EncryptedLibraryBlockRepresentationID(uuid.NewString())

	store.AddBlockMappingForRepresentation(orgID, db.PlainBlockRepresentationID, externalSHA1, plainBlockID)
	store.AddBlockMappingForRepresentation(orgID, encRep, externalSHA1, encBlockID)
	firstSeenAt, err := store.RecordS3Orphan(orgID, encBlockID, "hot", encRep, externalSHA1, "", time.Now())
	if err != nil {
		t.Fatalf("seed orphan: %v", err)
	}
	if err := store.MarkS3OrphanMappingCleanupPending(orgID, encBlockID, encRep, externalSHA1, firstSeenAt.Add(5*time.Second)); err != nil {
		t.Fatalf("advance orphan phase: %v", err)
	}

	recovered, err := w.RecoverS3Orphans(context.Background(), 100)
	if err != nil {
		t.Fatalf("RecoverS3Orphans: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("recovered=%d, want 1", recovered)
	}
	if store.ForwardBlockMappingExistsForRepresentation(orgID, encRep, externalSHA1) {
		t.Fatal("encrypted forward mapping should be cleaned up from pending mapping phase")
	}
	if !store.ForwardBlockMappingExistsForRepresentation(orgID, db.PlainBlockRepresentationID, externalSHA1) {
		t.Fatal("plain sibling mapping must be preserved during encrypted orphan cleanup")
	}
	if store.S3OrphanCount() != 0 {
		t.Fatalf("orphan should be cleared, got %d", store.S3OrphanCount())
	}
	if got := sp.DeletedBlocks(); len(got) != 0 {
		t.Fatalf("S3 should not be touched in pending mapping phase, got %v", got)
	}
}

// TestWorker_RecoverS3Orphans_PartialFailure confirms that when S3 is still
// down for some orphans, those rows stay (with bumped retry count) while the
// succeeding ones are removed.
func TestWorker_RecoverS3Orphans_PartialFailure(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)

	orgID := uuid.New()
	now := time.Now()
	if _, err := store.RecordS3Orphan(orgID, "orph-A", "hot", db.PlainBlockRepresentationID, "", "prev", now); err != nil {
		t.Fatalf("seed orphan A: %v", err)
	}
	if _, err := store.RecordS3Orphan(orgID, "orph-B", "hot", db.PlainBlockRepresentationID, "", "prev", now); err != nil {
		t.Fatalf("seed orphan B: %v", err)
	}

	// Fail one call during this recovery attempt. Since iteration order over a
	// map is random, assert on totals rather than which block survives.
	sp.FailNextN(1, errors.New("still down"))

	recovered, err := w.RecoverS3Orphans(context.Background(), 100)
	if err == nil {
		t.Fatal("RecoverS3Orphans() error = nil, want non-nil")
	}
	if recovered != 1 {
		t.Errorf("recovered=%d, want 1", recovered)
	}
	if got := store.S3OrphanCount(); got != 1 {
		t.Errorf("expected 1 orphan remaining, got %d", got)
	}
	remaining := store.AllS3Orphans()
	if len(remaining) != 1 {
		t.Fatalf("expected 1 remaining, got %d", len(remaining))
	}
	if remaining[0].RetryCount < 2 {
		t.Errorf("retry count should have been bumped to >=2, got %d", remaining[0].RetryCount)
	}
	if _, err := store.LoadGCStats(gcS3OrphansCursorKey); !errors.Is(err, gocql.ErrNotFound) {
		t.Fatalf("expected cursor to remain unset after partial failure, got err=%v", err)
	}
}

// TestWorker_RecoverS3Orphans_DryRun confirms dry-run mode is a no-op.
func TestWorker_RecoverS3Orphans_DryRun(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, true, stats) // dryRun=true

	orgID := uuid.New()
	if _, err := store.RecordS3Orphan(orgID, "orph-dr", "hot", db.PlainBlockRepresentationID, "", "prev", time.Now()); err != nil {
		t.Fatalf("seed orphan: %v", err)
	}

	recovered, err := w.RecoverS3Orphans(context.Background(), 100)
	if err != nil {
		t.Fatalf("RecoverS3Orphans: %v", err)
	}
	if recovered != 0 {
		t.Errorf("dry run recovered=%d, want 0", recovered)
	}
	if store.S3OrphanCount() != 1 {
		t.Errorf("orphan should survive dry run, got count=%d", store.S3OrphanCount())
	}
}

// TestWorker_RecoverS3Orphans_SkipsClaimedRows verifies the recovery phase does
// not delete from S3 while the canonical block row still exists in DB. This
// covers the crash window after the pending S3 row is recorded but before the
// claimed block row is physically deleted.
func TestWorker_RecoverS3Orphans_SkipsClaimedRows(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)

	orgID := uuid.New()
	store.AddBlock(orgID, "orph-claimed", "hot", 0)
	applied, err := store.ClaimBlockDelete(orgID, "orph-claimed", "claim-1")
	if err != nil || !applied {
		t.Fatalf("claim block delete: applied=%v err=%v", applied, err)
	}
	if _, err := store.RecordS3Orphan(orgID, "orph-claimed", "hot", db.PlainBlockRepresentationID, "", "", time.Now()); err != nil {
		t.Fatalf("seed pending orphan: %v", err)
	}

	recovered, err := w.RecoverS3Orphans(context.Background(), 100)
	if err == nil {
		t.Fatal("RecoverS3Orphans() error = nil, want non-nil while canonical block row still exists")
	}
	if recovered != 0 {
		t.Errorf("recovered=%d, want 0 while block row still exists", recovered)
	}
	if store.S3OrphanCount() != 1 {
		t.Errorf("pending orphan row should remain, got %d", store.S3OrphanCount())
	}
	if got := sp.DeletedBlocks(); len(got) != 0 {
		t.Errorf("S3 should not be touched while claimed block row still exists, got %v", got)
	}
	if _, err := store.LoadGCStats(gcS3OrphansCursorKey); !errors.Is(err, gocql.ErrNotFound) {
		t.Fatalf("expected cursor to remain unset while claimed row is deferred, got err=%v", err)
	}
}

// TestWorker_RecoverS3Orphans_ColdStartSeesOldRows verifies that the first
// recovery pass scans the full TTL horizon instead of only a recent 14-day
// window, so old orphan rows do not get stranded forever on a cold start.
func TestWorker_RecoverS3Orphans_ColdStartSeesOldRows(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)

	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	w.clock = func() time.Time { return now }

	orgID := uuid.New()
	firstSeenAt := now.AddDate(0, 0, -30)
	if _, err := store.RecordS3Orphan(orgID, "orph-old", "hot", db.PlainBlockRepresentationID, "", "old failure", firstSeenAt); err != nil {
		t.Fatalf("seed orphan: %v", err)
	}

	recovered, err := w.RecoverS3Orphans(context.Background(), 100)
	if err != nil {
		t.Fatalf("RecoverS3Orphans: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("recovered=%d, want 1", recovered)
	}
	if got := store.S3OrphanCount(); got != 0 {
		t.Fatalf("expected old orphan to be cleared, got %d rows", got)
	}
	cursorValue, err := store.LoadGCStats(gcS3OrphansCursorKey)
	if err != nil {
		t.Fatalf("expected S3 orphan cursor to be persisted, got err=%v", err)
	}
	wantCursor := db.GCProjectionDateString(now.AddDate(0, 0, -1))
	if cursorValue != wantCursor {
		t.Fatalf("cursor=%q, want %q", cursorValue, wantCursor)
	}
}

func TestWorker_RecoverS3Orphans_PartitionLimitKeepsCursorUnchanged(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)

	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	w.clock = func() time.Time { return now }
	orgID := uuid.New()
	targetBucket := 0
	seeded := 0
	for i := 0; seeded < 101; i++ {
		blockID := fmt.Sprintf("orph-bucket-%03d", i)
		if db.GCDiscoveryBucket(orgID.String(), blockID) != targetBucket {
			continue
		}
		if _, err := store.RecordS3Orphan(orgID, blockID, "hot", db.PlainBlockRepresentationID, "", "prev", now.AddDate(0, 0, -30)); err != nil {
			t.Fatalf("seed orphan %s: %v", blockID, err)
		}
		seeded++
	}

	recovered, err := w.RecoverS3Orphans(context.Background(), 100)
	if err == nil {
		t.Fatal("RecoverS3Orphans() error = nil, want non-nil on incomplete partition")
	}
	if recovered != 100 {
		t.Fatalf("recovered=%d, want 100", recovered)
	}
	if got := store.S3OrphanCount(); got != 1 {
		t.Fatalf("expected 1 orphan left behind for next pass, got %d", got)
	}
	if _, err := store.LoadGCStats(gcS3OrphansCursorKey); !errors.Is(err, gocql.ErrNotFound) {
		t.Fatalf("expected cursor to remain unset after incomplete partition, got err=%v", err)
	}
	if got := len(sp.DeletedBlocks()); got != 100 {
		t.Fatalf("expected 100 S3 deletes on first pass, got %d", got)
	}

	recovered, err = w.RecoverS3Orphans(context.Background(), 100)
	if err != nil {
		t.Fatalf("second RecoverS3Orphans: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("second recovered=%d, want 1", recovered)
	}
	if got := store.S3OrphanCount(); got != 0 {
		t.Fatalf("expected partition to drain on second pass, got %d rows", got)
	}
	wantCursor := db.GCProjectionDateString(now.AddDate(0, 0, -1))
	cursorValue, err := store.LoadGCStats(gcS3OrphansCursorKey)
	if err != nil {
		t.Fatalf("expected cursor after full drain, got err=%v", err)
	}
	if cursorValue != wantCursor {
		t.Fatalf("cursor=%q, want %q", cursorValue, wantCursor)
	}
}

func TestWorker_RecoverS3Orphans_RefCountLookupErrorKeepsCursorUnchanged(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)

	now := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC)
	w.clock = func() time.Time { return now }
	orgID := uuid.New()
	if _, err := store.RecordS3Orphan(orgID, "orph-refcount-error", "hot", db.PlainBlockRepresentationID, "", "prev", now.AddDate(0, 0, -10)); err != nil {
		t.Fatalf("seed orphan: %v", err)
	}
	store.getBlockRefCountErr = fmt.Errorf("temporary cassandra failure")

	recovered, err := w.RecoverS3Orphans(context.Background(), 100)
	if err == nil {
		t.Fatal("RecoverS3Orphans() error = nil, want non-nil")
	}
	if recovered != 0 {
		t.Fatalf("recovered=%d, want 0", recovered)
	}
	if got := sp.DeletedBlocks(); len(got) != 0 {
		t.Fatalf("S3 should not be touched on refcount lookup error, got %v", got)
	}
	if got := store.S3OrphanCount(); got != 1 {
		t.Fatalf("orphan should remain after refcount lookup error, got %d", got)
	}
	if _, err := store.LoadGCStats(gcS3OrphansCursorKey); !errors.Is(err, gocql.ErrNotFound) {
		t.Fatalf("expected cursor to remain unset after refcount lookup error, got err=%v", err)
	}
}

// TestScanner_S3OrphanRecoveryPhase_WiringNilSafe ensures the scanner phase is
// a no-op when no recoverer is wired (covers the case of tests that spin up a
// bare scanner without a worker).
func TestScanner_S3OrphanRecoveryPhase_NilSafe(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	s := NewScanner(store, q, stats, config.GCConfig{})

	// No SetOrphanRecoverer call — phase must not panic.
	n, err := s.scanS3OrphanRecovery(context.Background())
	if err != nil {
		t.Errorf("expected nil err, got %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 recovered, got %d", n)
	}
}

// TestScanner_S3OrphanRecoveryPhase_CallsRecoverer proves the phase delegates
// to the wired recoverer and propagates the count.
func TestScanner_S3OrphanRecoveryPhase_CallsRecoverer(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)
	s := NewScanner(store, q, stats, config.GCConfig{})
	s.SetOrphanRecoverer(w)

	orgID := uuid.New()
	if _, err := store.RecordS3Orphan(orgID, "orph-phase", "hot", db.PlainBlockRepresentationID, "", "prev", time.Now()); err != nil {
		t.Fatalf("seed orphan: %v", err)
	}

	n, err := s.scanS3OrphanRecovery(context.Background())
	if err != nil {
		t.Fatalf("phase: %v", err)
	}
	if n != 1 {
		t.Errorf("phase returned %d, want 1", n)
	}
	if store.S3OrphanCount() != 0 {
		t.Errorf("orphan should be recovered, got count=%d", store.S3OrphanCount())
	}
}
