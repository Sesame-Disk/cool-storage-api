package gc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// P4a's behavioural tests. Each one names the concrete failure it prevents, because the
// whole point of exact-incarnation, per-attempt authority is that the dangerous states
// are unreachable rather than merely unlikely.
//
// Staleness is modelled with BackdateBlockClaimForTest rather than by moving w.clock()
// forward: postponeItem stamps requeued rows with w.clock() while Queue.DequeueBatch
// derives its cutoff from time.Now(), so a worker driven into the future requeues items
// where no later pass can dequeue them — a multi-pass assertion would then be testing an
// empty queue.

func p4aSeedBlockCandidate(t *testing.T, store *MockStore, orgID uuid.UUID, blockID string) BlockGCCandidateInfo {
	t.Helper()
	store.AddBlock(orgID, blockID, "hot", 0)
	if _, err := store.EnsureBlockGCCandidate(orgID, blockID, "hot", time.Now().Add(-2*time.Hour)); err != nil {
		t.Fatalf("EnsureBlockGCCandidate: %v", err)
	}
	candidate, ok, err := store.GetBlockGCCandidate(orgID, blockID)
	if err != nil || !ok {
		t.Fatalf("GetBlockGCCandidate: ok=%v err=%v", ok, err)
	}
	return candidate
}

func p4aAttempt(candidate BlockGCCandidateInfo, at time.Time) BlockDeleteAuthority {
	return BlockDeleteAuthority{Target: candidate.Target, ClaimID: uuid.NewString(), ClaimedAt: at.UTC()}
}

// TestP4A_TwoAttemptsOnOneCandidateDoNotShareOwnership is the defect that motivated the
// whole change. The claim id used to derive from candidate_at, so two workers processing
// the same candidate presented the SAME id, the CAS answered "applied" to both, and each
// believed it owned the row.
func TestP4A_TwoAttemptsOnOneCandidateDoNotShareOwnership(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	candidate := p4aSeedBlockCandidate(t, store, orgID, "blk-shared")

	now := time.Now().UTC()
	attemptA := p4aAttempt(candidate, now)
	attemptB := p4aAttempt(candidate, now)
	if attemptA.ClaimID == attemptB.ClaimID {
		t.Fatal("two attempts on one candidate produced the same claim id; ownership is not per-attempt")
	}

	if outcome, err := store.ClaimBlockDelete(orgID, "blk-shared", attemptA); err != nil || outcome != BlockClaimAcquired {
		t.Fatalf("A claim = %s, %v; want acquired", outcome, err)
	}
	outcome, err := store.ClaimBlockDelete(orgID, "blk-shared", attemptB)
	if err != nil {
		t.Fatalf("B claim: %v", err)
	}
	if outcome == BlockClaimAcquired {
		t.Fatal("both attempts acquired the same row; either could then delete the bytes while the other drops the fence")
	}
	if outcome != BlockClaimFreshOwner {
		t.Fatalf("B claim = %s, want fresh_owner: A's claim is live, so B must postpone and preserve the candidate", outcome)
	}
}

// TestP4A_LoserCannotReleaseOrFinalizeTheWinnersClaim: the losing attempt holds no
// fence, so neither of its unwind paths may touch the winner's row. Releasing would drop
// the upload fence in the exact window it exists to cover; finalizing would delete the
// canonical row out from under a delete the winner is still authorizing.
func TestP4A_LoserCannotReleaseOrFinalizeTheWinnersClaim(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	candidate := p4aSeedBlockCandidate(t, store, orgID, "blk-loser")

	now := time.Now().UTC()
	winner := p4aAttempt(candidate, now)
	loser := p4aAttempt(candidate, now)
	if outcome, err := store.ClaimBlockDelete(orgID, "blk-loser", winner); err != nil || outcome != BlockClaimAcquired {
		t.Fatalf("winner claim = %s, %v", outcome, err)
	}

	outcome, err := store.ReleaseBlockClaim(orgID, "blk-loser", loser)
	if err != nil {
		t.Fatalf("loser release: %v", err)
	}
	if outcome != BlockReleaseNotOwner {
		t.Fatalf("loser release = %s, want not_owner", outcome)
	}
	if blk := store.GetBlock(orgID, "blk-loser"); blk == nil || blk.GCClaimID != winner.ClaimID {
		t.Fatalf("the loser dropped the winner's fence (block=%+v)", blk)
	}

	if err := store.FinalizeBlockDelete(orgID, "blk-loser", loser); err == nil {
		t.Fatal("the loser finalized a delete it never owned; the canonical row would vanish under the winner")
	}
	if store.GetBlock(orgID, "blk-loser") == nil {
		t.Fatal("the canonical row was deleted by an attempt that did not own it")
	}
}

// TestP4A_TakenOverAttemptCannotActAfterwards covers the wake-up-late case: A's claim
// went stale, B took it over, and A returns believing it still owns the row.
func TestP4A_TakenOverAttemptCannotActAfterwards(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	candidate := p4aSeedBlockCandidate(t, store, orgID, "blk-takeover")

	attemptA := p4aAttempt(candidate, time.Now().UTC())
	if outcome, err := store.ClaimBlockDelete(orgID, "blk-takeover", attemptA); err != nil || outcome != BlockClaimAcquired {
		t.Fatalf("A claim = %s, %v", outcome, err)
	}

	store.BackdateBlockClaimForTest(orgID, "blk-takeover", time.Now().Add(-2*blockDeleteClaimStaleAfter))
	if outcome, err := store.ReleaseStaleBlockClaim(orgID, "blk-takeover", time.Now().Add(-blockDeleteClaimStaleAfter)); err != nil || outcome != BlockClaimReleased {
		t.Fatalf("stale takeover = %s, %v; want released", outcome, err)
	}

	attemptB := p4aAttempt(candidate, time.Now().UTC())
	if outcome, err := store.ClaimBlockDelete(orgID, "blk-takeover", attemptB); err != nil || outcome != BlockClaimAcquired {
		t.Fatalf("B claim after takeover = %s, %v", outcome, err)
	}

	// A wakes up. Both of its transitions must be refused.
	outcome, err := store.ReleaseBlockClaim(orgID, "blk-takeover", attemptA)
	if err != nil || outcome != BlockReleaseNotOwner {
		t.Fatalf("A release after takeover = %s, %v; want not_owner", outcome, err)
	}
	if blk := store.GetBlock(orgID, "blk-takeover"); blk == nil || blk.GCClaimID != attemptB.ClaimID {
		t.Fatalf("A dropped B's fence after losing ownership (block=%+v)", blk)
	}
	if err := store.FinalizeBlockDelete(orgID, "blk-takeover", attemptA); err == nil {
		t.Fatal("A finalized after being taken over; B's delete would lose its canonical row mid-flight")
	}
}

// TestP4A_CandidateForDeadIncarnationCannotTouchTheLiveOne is R14 itself: the ABA case.
// A candidate enqueued for P1 is processed after P1 died and P2 was installed on the
// same logical block. Nothing ever decided P2 was garbage.
func TestP4A_CandidateForDeadIncarnationCannotTouchTheLiveOne(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	candidate := p4aSeedBlockCandidate(t, store, orgID, "blk-aba")

	// P1 dies; P2 is minted onto the same logical block.
	p1 := candidate.Target
	p2 := BlockDeleteTarget{StorageClass: p1.StorageClass, StorageKey: p1.StorageKey + ".remint"}
	store.SetBlockStorageKeyForTest(orgID, "blk-aba", p2.StorageKey)

	staleAttempt := p4aAttempt(candidate, time.Now().UTC())
	outcome, err := store.ClaimBlockDelete(orgID, "blk-aba", staleAttempt)
	if err != nil {
		t.Fatalf("stale claim: %v", err)
	}
	if outcome != BlockClaimTargetChanged {
		t.Fatalf("claim for the dead incarnation = %s, want target_changed", outcome)
	}
	blk := store.GetBlock(orgID, "blk-aba")
	if blk == nil {
		t.Fatal("the live incarnation's row disappeared")
	}
	if blk.GCState != "" {
		t.Fatalf("a candidate for a dead incarnation fenced the live one (gc_state=%q)", blk.GCState)
	}

	// Neither post-claim transition may reach P2 either.
	if outcome, err := store.ReleaseBlockClaim(orgID, "blk-aba", staleAttempt); err != nil || outcome != BlockReleaseNotOwner {
		t.Fatalf("stale release = %s, %v; want not_owner", outcome, err)
	}
	if err := store.FinalizeBlockDelete(orgID, "blk-aba", staleAttempt); err == nil {
		t.Fatal("a candidate for a dead incarnation finalized the delete of the live one; those bytes are still referenced")
	}
	if store.GetBlock(orgID, "blk-aba") == nil {
		t.Fatal("the live incarnation was deleted on a dead incarnation's authority")
	}
}

// TestP4A_StaleLifecycleCannotConsumeTheNewIncarnationsCandidate closes the other half
// of R14. Refusing to delete P2's bytes is not enough: if the stale lifecycle consumes
// P2's candidate on its way out, the only work item authorized to reclaim P2 is gone and
// nothing will ever revisit it.
func TestP4A_StaleLifecycleCannotConsumeTheNewIncarnationsCandidate(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	old := p4aSeedBlockCandidate(t, store, orgID, "blk-cand-aba")

	// The incarnation changes and a new candidate is decided for it.
	store.SetBlockStorageKeyForTest(orgID, "blk-cand-aba", old.Target.StorageKey+".remint")
	if _, err := store.EnsureBlockGCCandidate(orgID, "blk-cand-aba", "hot", time.Now()); err != nil {
		t.Fatalf("EnsureBlockGCCandidate for the new incarnation: %v", err)
	}
	fresh, ok, err := store.GetBlockGCCandidate(orgID, "blk-cand-aba")
	if err != nil || !ok {
		t.Fatalf("GetBlockGCCandidate: ok=%v err=%v", ok, err)
	}
	if fresh.Target == old.Target {
		t.Fatal("the candidate did not follow the new incarnation")
	}
	if !fresh.CandidateAt.After(old.CandidateAt) {
		t.Fatalf("the new incarnation inherited candidate_at %v from its predecessor; it would skip the grace period that lets in-flight writers finish", fresh.CandidateAt)
	}

	if err := store.DeleteBlockGCCandidate(orgID, "blk-cand-aba", old.Identity()); err != nil {
		t.Fatalf("stale candidate cleanup must be a safe no-op, got: %v", err)
	}
	if got := len(store.AllBlockGCCandidates()); got != 1 {
		t.Fatalf("candidate rows = %d, want 1: a stale lifecycle consumed the live incarnation's work item", got)
	}
}

// TestP4A_FreshOwnerDoesNotSettleTheCandidate: "someone else owns it" must never be read
// as "it is handled". If that owner is dead, this candidate is the only thing that will
// ever take the claim over, so consuming it leaves a permanent upload fence.
func TestP4A_FreshOwnerDoesNotSettleTheCandidate(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, &MockStorageProvider{}, q, 100, 0, false, stats)

	orgID := uuid.New()
	candidate := p4aSeedBlockCandidate(t, store, orgID, "blk-fresh")
	store.SeedBlockClaimForTest(orgID, "blk-fresh", "another-live-attempt", time.Now())
	if err := store.EnqueueItem(orgID, candidate.CandidateAt, ItemBlock, "blk-fresh", uuid.Nil, "hot", 0); err != nil {
		t.Fatalf("EnqueueItem: %v", err)
	}

	if _, err := w.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce: %v", err)
	}
	if got := len(store.AllBlockGCCandidates()); got != 1 {
		t.Fatalf("candidate rows = %d, want 1: a live owner is not completion", got)
	}
	if blk := store.GetBlock(orgID, "blk-fresh"); blk == nil || blk.GCClaimID != "another-live-attempt" {
		t.Fatalf("the live owner's claim was disturbed (block=%+v)", blk)
	}
	if failed, err := store.GetTotalFailedItems(); err != nil {
		t.Fatalf("GetTotalFailedItems: %v", err)
	} else if failed != 0 {
		t.Fatalf("%d item(s) reached the DLQ; waiting for another attempt must not spend the retry budget", failed)
	}
}

// TestP4A_StaleTakeoverRequiresTheExactPreviousAuthority: takeover is a CAS, not a
// clear. An owner that re-claimed between the observation and the write keeps its row.
func TestP4A_StaleTakeoverRequiresTheExactPreviousAuthority(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	candidate := p4aSeedBlockCandidate(t, store, orgID, "blk-stale-cas")

	attemptA := p4aAttempt(candidate, time.Now().UTC())
	if outcome, err := store.ClaimBlockDelete(orgID, "blk-stale-cas", attemptA); err != nil || outcome != BlockClaimAcquired {
		t.Fatalf("A claim = %s, %v", outcome, err)
	}
	store.BackdateBlockClaimForTest(orgID, "blk-stale-cas", time.Now().Add(-2*blockDeleteClaimStaleAfter))

	// A fresh claim lands in the window between "observed stale" and the release.
	store.SeedBlockClaimForTest(orgID, "blk-stale-cas", "attempt-c", time.Now())

	outcome, err := store.ReleaseStaleBlockClaim(orgID, "blk-stale-cas", time.Now().Add(-blockDeleteClaimStaleAfter))
	if err != nil {
		t.Fatalf("ReleaseStaleBlockClaim: %v", err)
	}
	if outcome == BlockClaimReleased {
		t.Fatal("a re-claimed row was released as stale; the new owner's fence was dropped mid-delete")
	}
	if outcome != BlockClaimTooFresh {
		t.Fatalf("stale release over a re-claimed row = %s, want too_fresh", outcome)
	}
	if blk := store.GetBlock(orgID, "blk-stale-cas"); blk == nil || blk.GCClaimID != "attempt-c" {
		t.Fatalf("the new owner's claim did not survive (block=%+v)", blk)
	}
}

// TestP4A_DeleteClaimNeverOverwritesARepairingStub: gc_state='repairing_stub' belongs to
// the UPLOAD path. The old CAS was `IF gc_state != 'deleting'`, which happily overwrote
// it — GC stealing a row another subsystem was actively repairing.
func TestP4A_DeleteClaimNeverOverwritesARepairingStub(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	candidate := p4aSeedBlockCandidate(t, store, orgID, "blk-repairing")
	store.SetBlockGCStateForTest(orgID, "blk-repairing", "repairing_stub", "upload-repair-claim", time.Now())

	outcome, err := store.ClaimBlockDelete(orgID, "blk-repairing", p4aAttempt(candidate, time.Now().UTC()))
	if err != nil {
		t.Fatalf("claim over repairing_stub: %v", err)
	}
	if outcome == BlockClaimAcquired {
		t.Fatal("GC claimed a row owned by the upload path's repair claim")
	}
	blk := store.GetBlock(orgID, "blk-repairing")
	if blk == nil || blk.GCState != "repairing_stub" || blk.GCClaimID != "upload-repair-claim" {
		t.Fatalf("the upload path's repair claim was overwritten (block=%+v)", blk)
	}
}

// TestP4A_CandidateWithoutAnExactIncarnationIsNeverDestructiveAndNeverConsumed pins the
// Invalid disposition in both directions: nothing destructive happens, and the work item
// survives so the block can still be revisited.
func TestP4A_CandidateWithoutAnExactIncarnationIsNeverDestructiveAndNeverConsumed(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	sp := &MockStorageProvider{}
	w := NewWorker(store, sp, q, 100, 0, false, stats)

	orgID := uuid.New()
	candidateAt := time.Now().Add(-2 * time.Hour)
	store.AddBlock(orgID, "blk-no-key", "hot", 0)
	store.AddBlockGCCandidate(orgID, "blk-no-key", "hot", candidateAt)
	// A candidate row that lost its incarnation. EnsureBlockGCCandidate cannot produce
	// this, so reaching it means the table was written behind that helper's back.
	store.SetBlockGCCandidateTargetForTest(orgID, "blk-no-key", BlockDeleteTarget{})
	if err := store.EnqueueItem(orgID, candidateAt, ItemBlock, "blk-no-key", uuid.Nil, "hot", 0); err != nil {
		t.Fatalf("EnqueueItem: %v", err)
	}

	if _, err := w.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce: %v", err)
	}
	if deletes := sp.ScopedBlockDeletes(); len(deletes) != 0 {
		t.Fatalf("deleted bytes on an identity that could not be named: %+v", deletes)
	}
	blk := store.GetBlock(orgID, "blk-no-key")
	if blk == nil {
		t.Fatal("the canonical row was removed on an unusable identity")
	}
	if blk.GCState != "" {
		t.Fatalf("fenced a row on an unusable identity (gc_state=%q); nothing could lift that fence", blk.GCState)
	}
	if got := len(store.AllBlockGCCandidates()); got != 1 {
		t.Fatalf("candidate rows = %d, want 1: refusing to act must never consume the work item", got)
	}
}

// TestP4A_AmbiguousClaimSettlesBeforeAnyCleanup: an unsettled LWT must leave everything
// in place. Consuming the candidate here is how a fence ends up standing with nothing
// able to lift it (R20).
func TestP4A_AmbiguousClaimSettlesBeforeAnyCleanup(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	sp := &MockStorageProvider{}
	w := NewWorker(store, sp, q, 100, 0, false, stats)

	orgID := uuid.New()
	candidate := p4aSeedBlockCandidate(t, store, orgID, "blk-ambiguous")
	if err := store.EnqueueItem(orgID, candidate.CandidateAt, ItemBlock, "blk-ambiguous", uuid.Nil, "hot", 0); err != nil {
		t.Fatalf("EnqueueItem: %v", err)
	}
	store.SetClaimBlockDeleteErrForTest(errors.New("gocql: no response received from cassandra within timeout period"))

	if _, err := w.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce: %v", err)
	}
	if got := len(store.AllBlockGCCandidates()); got != 1 {
		t.Fatalf("candidate rows = %d, want 1: an unsettled claim must not consume the work item", got)
	}
	if deletes := sp.ScopedBlockDeletes(); len(deletes) != 0 {
		t.Fatalf("deleted bytes under an unsettled claim: %+v", deletes)
	}
	if store.GetBlock(orgID, "blk-ambiguous") == nil {
		t.Fatal("the canonical row was removed under an unsettled claim")
	}
}

// TestP4A_EnsureBlockGCCandidateRefusesWithoutAnExactIncarnation is the source-side gate:
// a candidate that cannot name P is never written at all, so no later code path has to
// remember to check for one.
func TestP4A_EnsureBlockGCCandidateRefusesWithoutAnExactIncarnation(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()

	if _, err := store.EnsureBlockGCCandidate(orgID, "blk-absent", "hot", time.Now()); !errors.Is(err, ErrBlockCandidateTargetUnavailable) {
		t.Fatalf("EnsureBlockGCCandidate(no canonical row) = %v, want ErrBlockCandidateTargetUnavailable", err)
	}
	store.AddStubBlockForTest(orgID, "blk-locatorless")
	if _, err := store.EnsureBlockGCCandidate(orgID, "blk-locatorless", "hot", time.Now()); !errors.Is(err, ErrBlockCandidateTargetUnavailable) {
		t.Fatalf("EnsureBlockGCCandidate(no locator) = %v, want ErrBlockCandidateTargetUnavailable", err)
	}
	if got := len(store.AllBlockGCCandidates()); got != 0 {
		t.Fatalf("candidate rows = %d, want 0: a refused capture must leave no destructive authority behind", got)
	}
}

// TestP4A_EachWorkerAttemptMintsItsOwnClaimID is the BEHAVIOURAL half of the
// per-attempt identity invariant.
//
// The source guard proves processBlock calls uuid; only this proves the value actually
// reaching the CAS differs between attempts on one candidate. That distinction matters:
// the original defect was not a missing uuid call, it was a claim id computed from
// candidate_at — which every attempt on that candidate reproduces identically, so the
// CAS answered "applied" to all of them and each could release or finalize under the
// others.
//
// The block is referenced only on the GLOBAL read, so each pass claims and then hands
// its own claim back — reaching the claim exactly once per pass without deleting
// anything.
func TestP4A_EachWorkerAttemptMintsItsOwnClaimID(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, &Stats{})

	orgID := uuid.New()
	candidate := p4aSeedBlockCandidate(t, store, orgID, "blk-per-attempt")
	if err := store.EnqueueItem(orgID, candidate.CandidateAt, ItemBlock, "blk-per-attempt", uuid.Nil, "hot", 0); err != nil {
		t.Fatalf("EnqueueItem: %v", err)
	}

	lastGlobal := 0
	store.SetBlockHasReferencesHookForTest(func(_ uuid.UUID, _ string, _ bool) (bool, error) {
		_, global := store.BlockHasReferencesCallCountsForTest()
		isGlobalRead := global > lastGlobal
		lastGlobal = global
		return isGlobalRead, nil
	})

	for i := 0; i < 2; i++ {
		if _, err := w.ProcessOnce(context.Background()); err != nil {
			t.Fatalf("ProcessOnce %d: %v", i, err)
		}
		// The re-referenced path settles the candidate; restore it so the second pass
		// is a genuine second attempt on the same candidate rather than a no-op.
		if _, ok, _ := store.GetBlockGCCandidate(orgID, "blk-per-attempt"); !ok {
			store.AddBlockGCCandidate(orgID, "blk-per-attempt", "hot", candidate.CandidateAt)
			if err := store.EnqueueItem(orgID, candidate.CandidateAt, ItemBlock, "blk-per-attempt", uuid.Nil, "hot", 0); err != nil {
				t.Fatalf("re-enqueue for pass %d: %v", i, err)
			}
		}
	}

	attempts := store.ClaimAttemptsForTest()
	if len(attempts) < 2 {
		t.Fatalf("the worker reached the claim %d time(s), want at least 2; this test cannot observe per-attempt identity", len(attempts))
	}
	seen := make(map[string]int, len(attempts))
	for _, a := range attempts {
		seen[a.ClaimID]++
	}
	for claimID, count := range seen {
		if count > 1 {
			t.Fatalf("claim id %q was reused across %d attempts on one candidate; ownership is not per-attempt, so two concurrent workers would both own the row", claimID, count)
		}
	}
	for _, a := range attempts {
		if a.Target != candidate.Target {
			t.Fatalf("an attempt claimed incarnation %s, but the candidate authorized %s", a.Target, candidate.Target)
		}
	}
}
