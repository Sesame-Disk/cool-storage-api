package gc

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestP4B_CommitBlockDeleteOrphanHandoffSourceContract(t *testing.T) {
	file := parseGCStoreFile(t)
	text := formattedGCFunction(t, file, "CommitBlockDeleteOrphanHandoff")

	if !strings.Contains(text, "UPDATE blocks SET gc_orphan_handoff = true") {
		t.Fatal("CommitBlockDeleteOrphanHandoff must set gc_orphan_handoff = true")
	}
	if strings.Contains(text, "gc_orphan_handoff = false") {
		t.Fatal("CommitBlockDeleteOrphanHandoff must never write gc_orphan_handoff = false")
	}
	for _, column := range []string{
		"storage_class = ?",
		"storage_key = ?",
		"gc_state = ?",
		"gc_claim_id = ?",
		"gc_claimed_at = ?",
		"gc_orphan_handoff = null",
	} {
		if !strings.Contains(text, column) {
			t.Fatalf("CommitBlockDeleteOrphanHandoff IF must name %s", column)
		}
	}
	if !strings.Contains(text, "Consistency(gocql.EachQuorum)") {
		t.Fatal("CommitBlockDeleteOrphanHandoff must pin regular consistency to EachQuorum")
	}
	if !strings.Contains(text, "SerialConsistency(gocql.Serial)") {
		t.Fatal("CommitBlockDeleteOrphanHandoff must pin the LWT serial domain")
	}
	if !strings.Contains(text, "Idempotent(false)") || !strings.Contains(text, "NumRetries: 0") || !strings.Contains(text, "NonSpeculativeExecution") {
		t.Fatal("CommitBlockDeleteOrphanHandoff must not hide an uncertain LWT behind driver retries")
	}
}

func TestP4B_ReleaseAndClaimRefuseCommittedHandoff(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	blockID := "blk-handoff-release"
	store.AddBlock(orgID, blockID, "hot", 0)
	owner := store.SeedBlockClaimForTest(orgID, blockID, "d1", time.Now().UTC().Add(-time.Hour))
	store.SeedBlockHandoffForTest(orgID, blockID)

	if outcome, err := store.ReleaseBlockClaim(orgID, blockID, owner); err != nil || outcome != BlockReleaseNotOwner {
		t.Fatalf("ReleaseBlockClaim after handoff = %s, %v; want not_owner", outcome, err)
	}
	if blk := store.GetBlock(orgID, blockID); blk == nil || blk.GCClaimID != "d1" || !orphanHandoffCommitted(blk.GCOrphanHandoff) {
		t.Fatalf("release dropped a committed authority: %+v", blk)
	}

	stale, err := store.ReleaseStaleBlockClaim(orgID, blockID, owner.Target, time.Now().UTC())
	if err != nil || stale != BlockClaimCommittedHandoff {
		t.Fatalf("ReleaseStaleBlockClaim after handoff = %s, %v; want committed_handoff", stale, err)
	}

	fresh := store.BlockDeleteAuthorityForTest(orgID, blockID, "d2", time.Now().UTC())
	claim, err := store.ClaimBlockDelete(orgID, blockID, fresh)
	if err != nil || claim.Outcome != BlockClaimCommittedOwner {
		t.Fatalf("ClaimBlockDelete after handoff = %s, %v; want committed_owner", claim.Outcome, err)
	}
	if claim.Owner.ClaimID != "d1" {
		t.Fatalf("CommittedOwner resumed %q, want stored d1", claim.Owner.ClaimID)
	}
}

func TestP4B_FinalizeRequiresCommittedHandoff(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	blockID := "blk-finalize-handoff"
	store.AddBlock(orgID, blockID, "hot", 0)
	owner := store.SeedBlockClaimForTest(orgID, blockID, "d1", time.Now().UTC())

	result, err := store.FinalizeBlockDelete(orgID, blockID, committedBlockDeleteAuthority(owner))
	if err == nil || result.Outcome != BlockDeleteNotAuthority {
		t.Fatalf("finalize without handoff = %+v, %v; want not_authority", result, err)
	}
	if store.GetBlock(orgID, blockID) == nil {
		t.Fatal("finalize without handoff deleted the canonical row")
	}

	store.SeedBlockHandoffForTest(orgID, blockID)
	result, err = store.FinalizeBlockDelete(orgID, blockID, committedBlockDeleteAuthority(owner))
	if err != nil || !result.ok() {
		t.Fatalf("finalize with handoff = %+v, %v; want applied", result, err)
	}
	if store.GetBlock(orgID, blockID) != nil {
		t.Fatal("finalize with handoff left the canonical row")
	}
}

func TestProcessBlockCommittedHandoffIsNotReleasedOnPreClaimRefsBranch(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	w := NewWorker(store, sp, NewQueue(store), 100, 0, false, &Stats{})
	orgID := uuid.New()
	blockID := testSHA256BlockID("p4b-h10-refs")
	store.AddBlock(orgID, blockID, "hot", 0)
	candidateAt := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Millisecond)
	ensureAndEnqueueBlockForTest(t, store, orgID, blockID, "hot", candidateAt, 0)
	stored := store.SeedBlockClaimForTest(orgID, blockID, "stored-d1", candidateAt)
	store.SeedBlockHandoffForTest(orgID, blockID)
	store.AddBlockReferenceForTest(orgID, blockID, "still-referenced")

	n, err := w.ProcessOnce(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("ProcessOnce() = (%d, %v), want resume and complete under stored D", n, err)
	}
	if store.GetBlock(orgID, blockID) != nil {
		t.Fatal("CommittedOwner resume must finish the stored delete, not postpone as stale")
	}
	orphans := store.AllS3Orphans()
	if len(orphans) != 0 {
		t.Fatalf("completed resume left orphan state: %+v", orphans)
	}
	if stored.ClaimID != "stored-d1" {
		t.Fatal("test fixture lost the stored claim id")
	}
}

func TestProcessBlockCommittedOwnerRevalidationFailureLeavesQueueUntouched(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	w := NewWorker(store, sp, NewQueue(store), 100, 0, false, &Stats{})
	orgID := uuid.New()
	blockID := testSHA256BlockID("p4b-h9-revalidate")
	store.AddBlock(orgID, blockID, "hot", 0)
	candidateAt := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Millisecond)
	candidate := ensureAndEnqueueBlockForTest(t, store, orgID, blockID, "hot", candidateAt, 0)
	original := store.QueueItems(orgID)[0]
	store.SeedBlockClaimForTest(orgID, blockID, "stored-d1", candidateAt)
	store.SeedBlockHandoffForTest(orgID, blockID)

	var topologyCalls int
	w.SetDestructiveTopologyGate(func() error {
		topologyCalls++
		if topologyCalls == 1 {
			return nil
		}
		return errors.New("live replication map no longer matches the declared topology")
	})

	n, err := w.ProcessOnce(context.Background())
	if err != nil || n != 0 {
		t.Fatalf("ProcessOnce() = (%d, %v), want committed-pending refusal", n, err)
	}
	block := store.GetBlock(orgID, blockID)
	if block == nil || block.GCState != "deleting" || block.GCClaimID != "stored-d1" || !orphanHandoffCommitted(block.GCOrphanHandoff) {
		t.Fatalf("revalidation failure released a committed authority: %+v", block)
	}
	if store.QueueCompleteCallsForTest() != 0 || store.QueueRequeueCallsForTest() != 0 || store.QueueFailCallsForTest() != 0 {
		t.Fatalf("queue lifecycle calls = complete:%d requeue:%d fail:%d, want all zero", store.QueueCompleteCallsForTest(), store.QueueRequeueCallsForTest(), store.QueueFailCallsForTest())
	}
	items := store.QueueItems(orgID)
	if len(items) != 1 || items[0].RetryCount != original.RetryCount || !items[0].QueuedAt.Equal(original.QueuedAt) {
		t.Fatalf("queue after H9 refusal = %+v, want original %+v", items, original)
	}
	if _, ok, err := store.GetBlockGCCandidateExact(orgID, blockID, candidate.Identity()); err != nil || !ok {
		t.Fatalf("candidate was consumed after H9 refusal: ok=%v err=%v", ok, err)
	}
	if topologyCalls < 2 {
		t.Fatalf("CommittedOwner skipped topology revalidation (calls=%d)", topologyCalls)
	}
}

func TestProcessBlockCommittedOwnerDoesNotMintANewClaim(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	w := NewWorker(store, sp, NewQueue(store), 100, 0, false, &Stats{})
	orgID := uuid.New()
	blockID := testSHA256BlockID("p4b-resume-d1")
	store.AddBlock(orgID, blockID, "hot", 0)
	candidateAt := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Millisecond)
	ensureAndEnqueueBlockForTest(t, store, orgID, blockID, "hot", candidateAt, 0)
	store.SeedBlockClaimForTest(orgID, blockID, "stored-d1", candidateAt)
	store.SeedBlockHandoffForTest(orgID, blockID)

	n, err := w.ProcessOnce(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("ProcessOnce() = (%d, %v), want resume completion", n, err)
	}
	if store.GetBlock(orgID, blockID) != nil {
		t.Fatal("resume did not finalize the stored authority")
	}
	if got := sp.DeletedBlocks(); len(got) != 1 {
		t.Fatalf("physical deletes = %v, want the stored incarnation", got)
	}
}
