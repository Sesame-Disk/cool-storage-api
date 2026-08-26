package gc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
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
	candidate, err := store.EnsureBlockGCCandidateExact(orgID, blockID, "hot", time.Now().Add(-2*time.Hour))
	if err != nil {
		t.Fatalf("EnsureBlockGCCandidateExact: %v", err)
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

	if claim, err := store.ClaimBlockDelete(orgID, "blk-shared", attemptA); err != nil || claim.Outcome != BlockClaimAcquired {
		t.Fatalf("A claim = %s, %v; want acquired", claim.Outcome, err)
	}
	claim, err := store.ClaimBlockDelete(orgID, "blk-shared", attemptB)
	if err != nil {
		t.Fatalf("B claim: %v", err)
	}
	if claim.Outcome == BlockClaimAcquired {
		t.Fatal("both attempts acquired the same row; either could then delete the bytes while the other drops the fence")
	}
	if claim.Outcome != BlockClaimFreshOwner {
		t.Fatalf("B claim = %s, want fresh_owner: A's claim is live, so B must postpone and preserve the candidate", claim.Outcome)
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
	if claim, err := store.ClaimBlockDelete(orgID, "blk-loser", winner); err != nil || claim.Outcome != BlockClaimAcquired {
		t.Fatalf("winner claim = %s, %v", claim.Outcome, err)
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
	if claim, err := store.ClaimBlockDelete(orgID, "blk-takeover", attemptA); err != nil || claim.Outcome != BlockClaimAcquired {
		t.Fatalf("A claim = %s, %v", claim.Outcome, err)
	}

	store.BackdateBlockClaimForTest(orgID, "blk-takeover", time.Now().Add(-2*blockDeleteClaimStaleAfter))

	// B observes A as stale through its own claim attempt and takes over using the exact
	// authority that attempt reported — the production flow. The observed ClaimedAt is
	// the aged one rather than what A originally wrote, which is precisely why a takeover
	// must use what it SAW instead of anything it remembers.
	observe := p4aAttempt(candidate, time.Now().UTC())
	observed, err := store.ClaimBlockDelete(orgID, "blk-takeover", observe)
	if err != nil || observed.Outcome != BlockClaimStaleOwner {
		t.Fatalf("B observing A = %s, %v; want stale_owner", observed.Outcome, err)
	}
	if observed.Owner.ClaimID != attemptA.ClaimID {
		t.Fatalf("the claim reported owner %q, want the abandoned attempt %q", observed.Owner.ClaimID, attemptA.ClaimID)
	}
	if released, err := store.ReleaseBlockClaim(orgID, "blk-takeover", observed.Owner); err != nil || released != BlockReleaseReleased {
		t.Fatalf("stale takeover = %s, %v; want released", released, err)
	}

	attemptB := p4aAttempt(candidate, time.Now().UTC())
	if claim, err := store.ClaimBlockDelete(orgID, "blk-takeover", attemptB); err != nil || claim.Outcome != BlockClaimAcquired {
		t.Fatalf("B claim after takeover = %s, %v", claim.Outcome, err)
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
	claim, err := store.ClaimBlockDelete(orgID, "blk-aba", staleAttempt)
	if err != nil {
		t.Fatalf("stale claim: %v", err)
	}
	if claim.Outcome != BlockClaimTargetChanged {
		t.Fatalf("claim for the dead incarnation = %s, want target_changed", claim.Outcome)
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
	fresh, err := store.EnsureBlockGCCandidateExact(orgID, "blk-cand-aba", "hot", time.Now())
	if err != nil {
		t.Fatalf("EnsureBlockGCCandidate for the new incarnation: %v", err)
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
		t.Fatalf("candidate rows = %d, want 1: exact cleanup of P1 must preserve P2", got)
	}
}

// TestP4A_DelayedP1EnqueueSettlesOnlyP1AndLeavesP2Claimable models the stale
// Ensure P1 versus fresh P2 race. A captures P1, then waits before enqueueing;
// meanwhile P2 becomes canonical and B decides and enqueues it. A's delayed P1
// item must settle only its own dead incarnation, leaving P2's candidate,
// discovery projection, queue row, and pending row available for P2's lifecycle.
func TestP4A_DelayedP1EnqueueSettlesOnlyP1AndLeavesP2Claimable(t *testing.T) {
	store := NewMockStore()
	storage := &MockStorageProvider{}
	queue := NewQueue(store)
	worker := NewWorker(store, storage, queue, 1, time.Hour, false, &Stats{})

	orgID := uuid.New()
	blockID := testSHA256BlockID("blk-delayed-p1")
	p1At := time.Date(2020, time.January, 2, 3, 4, 5, 0, time.UTC)
	p2At := p1At.Add(time.Second)
	store.AddBlock(orgID, blockID, "hot", 0)

	// A captures the candidate while P1 is canonical, then stalls before enqueueing it.
	p1, err := store.EnsureBlockGCCandidateExact(orgID, blockID, "hot", p1At)
	if err != nil {
		t.Fatalf("A EnsureBlockGCCandidateExact(P1): %v", err)
	}

	// P1 is replaced. B observes P2, decides its own candidate, and enqueues it first.
	store.SetBlockStorageKeyForTest(orgID, blockID, p1.Target.StorageKey+".00000000-0000-0000-0000-000000000002")
	p2, err := store.EnsureBlockGCCandidateExact(orgID, blockID, "hot", p2At)
	if err != nil {
		t.Fatalf("B EnsureBlockGCCandidateExact(P2): %v", err)
	}
	if p1.Target == p2.Target {
		t.Fatal("P2 reused P1's physical candidate identity")
	}
	enqueueExactBlockCandidateForTest(t, store, p2, 0)

	// A wakes and enqueues exactly the candidate it captured before the replacement.
	enqueueExactBlockCandidateForTest(t, store, p1, 0)

	for _, candidate := range []BlockGCCandidateInfo{p1, p2} {
		if _, ok, err := store.GetBlockGCCandidateExact(orgID, blockID, candidate.Identity()); err != nil || !ok {
			t.Fatalf("exact candidate %s: ok=%v err=%v", candidate.Target, ok, err)
		}
		if exists, err := store.QueueItemExists(orgID, candidate.CandidateAt, ItemBlock, blockID, candidate.ItemIdentity()); err != nil || !exists {
			t.Fatalf("queue identity for %s: exists=%v err=%v", candidate.Target, exists, err)
		}
		if exists, err := store.PendingItemExists(orgID, uuid.Nil, ItemBlock, blockID, candidate.ItemIdentity()); err != nil || !exists {
			t.Fatalf("pending identity for %s: exists=%v err=%v", candidate.Target, exists, err)
		}
	}
	if queued := store.QueueItems(orgID); len(queued) != 2 {
		t.Fatalf("queue rows = %d, want 2 exact P1 and P2 identities", len(queued))
	}
	bucket := db.GCDiscoveryBucket(orgID.String(), blockID)
	if projected, err := store.ListBlockGCCandidatesByDay(p1At, bucket); err != nil || len(projected) != 2 {
		t.Fatalf("coexisting candidate projections: rows=%d err=%v, want 2", len(projected), err)
	}

	// P1 is older in the queue, so its stale lifecycle reaches TargetChanged first.
	if processed, err := worker.ProcessOrgOnce(context.Background(), orgID); err != nil || processed != 1 {
		t.Fatalf("process stale P1: processed=%d err=%v, want 1/nil", processed, err)
	}
	if _, ok, err := store.GetBlockGCCandidateExact(orgID, blockID, p1.Identity()); err != nil || ok {
		t.Fatalf("P1 candidate after TargetChanged: ok=%v err=%v, want removed", ok, err)
	}
	if _, ok, err := store.GetBlockGCCandidateExact(orgID, blockID, p2.Identity()); err != nil || !ok {
		t.Fatalf("P2 candidate after P1 TargetChanged: ok=%v err=%v, want preserved", ok, err)
	}
	if projected, err := store.ListBlockGCCandidatesByDay(p1At, bucket); err != nil || len(projected) != 1 || projected[0].Target != p2.Target {
		t.Fatalf("projection after P1 TargetChanged: rows=%+v err=%v, want only P2", projected, err)
	}
	if queued := store.QueueItems(orgID); len(queued) != 1 || queued[0].BlockGCCandidateIdentity != p2.Identity() {
		t.Fatalf("queue after P1 TargetChanged = %+v, want only P2", queued)
	}
	if exists, err := store.PendingItemExists(orgID, uuid.Nil, ItemBlock, blockID, p1.ItemIdentity()); err != nil || exists {
		t.Fatalf("P1 pending after settlement: exists=%v err=%v, want removed", exists, err)
	}
	if exists, err := store.PendingItemExists(orgID, uuid.Nil, ItemBlock, blockID, p2.ItemIdentity()); err != nil || !exists {
		t.Fatalf("P2 pending after P1 settlement: exists=%v err=%v, want preserved", exists, err)
	}
	if deletes := storage.ScopedBlockDeletes(); len(deletes) != 0 {
		t.Fatalf("P1 TargetChanged deleted physical data: %+v", deletes)
	}

	// P2 has served its grace period and reaches its own ordinary claim/delete lifecycle.
	if processed, err := worker.ProcessOrgOnce(context.Background(), orgID); err != nil || processed != 1 {
		t.Fatalf("process P2: processed=%d err=%v, want 1/nil", processed, err)
	}
	attempts := store.ClaimAttemptsForTest()
	if len(attempts) != 2 || attempts[0].Target != p1.Target || attempts[1].Target != p2.Target {
		t.Fatalf("claim targets = %+v, want P1 then P2", attempts)
	}
	if store.GetBlock(orgID, blockID) != nil {
		t.Fatal("P2 canonical row survived its own unreferenced lifecycle")
	}
	if got := len(store.AllBlockGCCandidates()); got != 0 {
		t.Fatalf("candidates after P2 lifecycle = %d, want 0", got)
	}
	if queued := store.QueueItems(orgID); len(queued) != 0 {
		t.Fatalf("queue after P2 lifecycle = %+v, want empty", queued)
	}
	if exists, err := store.PendingItemExists(orgID, uuid.Nil, ItemBlock, blockID, p2.ItemIdentity()); err != nil || exists {
		t.Fatalf("P2 pending after lifecycle: exists=%v err=%v, want removed", exists, err)
	}
	if deletes := storage.ScopedBlockDeletes(); len(deletes) != 1 || deletes[0].StorageKey != p2.Target.StorageKey {
		t.Fatalf("physical deletes = %+v, want only P2 %q", deletes, p2.Target.StorageKey)
	}
}

// TestP4A_SameCandidateAtDistinctTargetsKeepIndependentQueueAndPendingIdentity
// proves the physical queue and pending keys include P. Two candidates with the
// same logical block and candidate_at differ only by their physical locator; neither
// may overwrite or complete the other.
func TestP4A_SameCandidateAtDistinctTargetsKeepIndependentQueueAndPendingIdentity(t *testing.T) {
	store := NewMockStore()
	queue := NewQueue(store)
	orgID := uuid.New()
	blockID := testSHA256BlockID("blk-same-candidate-at")
	candidateAt := time.Date(2020, time.January, 3, 4, 5, 6, 0, time.UTC)
	store.AddBlock(orgID, blockID, "hot", 0)

	p1, err := store.EnsureBlockGCCandidateExact(orgID, blockID, "hot", candidateAt)
	if err != nil {
		t.Fatalf("EnsureBlockGCCandidateExact(P1): %v", err)
	}
	store.SetBlockStorageKeyForTest(orgID, blockID, p1.Target.StorageKey+".00000000-0000-0000-0000-000000000002")
	p2, err := store.EnsureBlockGCCandidateExact(orgID, blockID, "hot", candidateAt)
	if err != nil {
		t.Fatalf("EnsureBlockGCCandidateExact(P2): %v", err)
	}
	if p1.Target == p2.Target || !p1.CandidateAt.Equal(p2.CandidateAt) {
		t.Fatalf("candidate identities = %+v / %+v, want distinct P at the same candidate_at", p1.Identity(), p2.Identity())
	}

	for _, candidate := range []BlockGCCandidateInfo{p1, p2} {
		if err := queue.EnqueueBatch([]QueueItem{{
			OrgID:                    orgID,
			QueuedAt:                 candidateAt,
			IdentityAt:               candidateAt,
			ItemType:                 ItemBlock,
			ItemID:                   blockID,
			LibraryID:                uuid.Nil,
			StorageClass:             candidate.StorageClass(),
			BlockGCCandidateIdentity: candidate.Identity(),
		}}); err != nil {
			t.Fatalf("EnqueueBatch(%s): %v", candidate.Target, err)
		}
	}

	for _, candidate := range []BlockGCCandidateInfo{p1, p2} {
		if exists, err := store.QueueItemExists(orgID, candidateAt, ItemBlock, blockID, candidate.ItemIdentity()); err != nil || !exists {
			t.Fatalf("queue identity for %s: exists=%v err=%v", candidate.Target, exists, err)
		}
		if exists, err := store.PendingItemExists(orgID, uuid.Nil, ItemBlock, blockID, candidate.ItemIdentity()); err != nil || !exists {
			t.Fatalf("pending identity for %s: exists=%v err=%v", candidate.Target, exists, err)
		}
	}
	if queued := store.QueueItems(orgID); len(queued) != 2 {
		t.Fatalf("queue rows = %d, want 2 despite identical logical id and candidate_at", len(queued))
	}

	// Exact completion of P1 must leave the physically distinct P2 row and pending
	// identity in place; this is the operation a collapsed physical queue key loses.
	if err := store.CompleteItem(orgID, candidateAt, ItemBlock, blockID, p1.ItemIdentity()); err != nil {
		t.Fatalf("CompleteItem(P1): %v", err)
	}
	if exists, err := store.QueueItemExists(orgID, candidateAt, ItemBlock, blockID, p1.ItemIdentity()); err != nil || exists {
		t.Fatalf("P1 queue after exact completion: exists=%v err=%v, want removed", exists, err)
	}
	if exists, err := store.PendingItemExists(orgID, uuid.Nil, ItemBlock, blockID, p1.ItemIdentity()); err != nil || exists {
		t.Fatalf("P1 pending after exact completion: exists=%v err=%v, want removed", exists, err)
	}
	if exists, err := store.QueueItemExists(orgID, candidateAt, ItemBlock, blockID, p2.ItemIdentity()); err != nil || !exists {
		t.Fatalf("P2 queue after P1 completion: exists=%v err=%v, want preserved", exists, err)
	}
	if exists, err := store.PendingItemExists(orgID, uuid.Nil, ItemBlock, blockID, p2.ItemIdentity()); err != nil || !exists {
		t.Fatalf("P2 pending after P1 completion: exists=%v err=%v, want preserved", exists, err)
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
	enqueueExactBlockCandidateForTest(t, store, candidate, 0)

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

// TestP4A_StaleTakeoverRequiresTheExactPreviousAuthority: a takeover is a CAS against
// the authority the claim OBSERVED, not a fresh look at whoever owns the row now.
//
// The previous version of this test seeded a FRESH replacement owner, which the staleness
// check rejects on its own — so it passed without ever exercising the CAS, and would have
// stayed green against a takeover that re-read the row and released whatever it found.
// Both cases below replace the observed owner with another STALE one, so age cannot be
// what saves the row and only the exact-authority CAS can.
func TestP4A_StaleTakeoverRequiresTheExactPreviousAuthority(t *testing.T) {
	for _, tc := range []struct {
		name       string
		replaceKey bool
		why        string
	}{
		{
			name: "same incarnation, different attempt",
			why:  "the claim id is load-bearing on its own: another attempt took the row over first, and only IT may hand its own claim back",
		},
		{
			name:       "different incarnation",
			replaceKey: true,
			why:        "a worker authorized for P1 must never drop P2's fence, however old that fence is",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := NewMockStore()
			orgID := uuid.New()
			candidate := p4aSeedBlockCandidate(t, store, orgID, "blk-stale-cas")

			// A claims and is abandoned; this is the authority a worker would observe.
			observed := p4aAttempt(candidate, time.Now().UTC())
			if claim, err := store.ClaimBlockDelete(orgID, "blk-stale-cas", observed); err != nil || claim.Outcome != BlockClaimAcquired {
				t.Fatalf("A claim = %s, %v", claim.Outcome, err)
			}

			// Between that observation and the takeover, a DIFFERENT owner takes the row —
			// and is itself already stale, so nothing but exact authority can refuse it.
			if tc.replaceKey {
				store.SetBlockStorageKeyForTest(orgID, "blk-stale-cas", candidate.Target.StorageKey+".remint")
			}
			survivor := store.SeedBlockClaimForTest(orgID, "blk-stale-cas", "attempt-c", time.Now().Add(-2*blockDeleteClaimStaleAfter))

			released, err := store.ReleaseBlockClaim(orgID, "blk-stale-cas", observed)
			if err != nil {
				t.Fatalf("takeover release: %v", err)
			}
			if released != BlockReleaseNotOwner {
				t.Fatalf("takeover of a claim that changed hands = %s, want not_owner: %s", released, tc.why)
			}
			blk := store.GetBlock(orgID, "blk-stale-cas")
			if blk == nil {
				t.Fatal("the canonical row disappeared")
			}
			if blk.GCClaimID != survivor.ClaimID || blk.GCState != db.BlockGCStateDeleting {
				t.Fatalf("the surviving owner's fence was dropped by a worker that never observed it (block=%+v): %s", blk, tc.why)
			}
		})
	}
}

// TestP4A_StaleClaimReleaseIsBoundToTheCandidatesIncarnation covers the OTHER caller of
// the age-based release: the pre-check path, which runs on a still-referenced block and
// is deliberately owner-agnostic so it can lift a fence whose owner will never return.
//
// Owner-agnostic is not incarnation-agnostic. `blocks` can perfectly ordinarily hold a
// different life by the time this runs — P1 died, P2 was installed, a lifecycle for P2
// claimed it and was abandoned — and a candidate for P1 has no authority over P2's fence.
// No clock skew or unusual interleaving is needed to reach this; it is the ordinary
// shape of a re-minted block.
func TestP4A_StaleClaimReleaseIsBoundToTheCandidatesIncarnation(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	candidate := p4aSeedBlockCandidate(t, store, orgID, "blk-precheck")
	p1 := candidate.Target

	// The block is re-minted, and a GC lifecycle for the NEW incarnation fences it and
	// is abandoned.
	store.SetBlockStorageKeyForTest(orgID, "blk-precheck", p1.StorageKey+".remint")
	survivor := store.SeedBlockClaimForTest(orgID, "blk-precheck", "p2-lifecycle", time.Now().Add(-2*blockDeleteClaimStaleAfter))

	outcome, err := store.ReleaseStaleBlockClaim(orgID, "blk-precheck", p1, time.Now().Add(-blockDeleteClaimStaleAfter))
	if err != nil {
		t.Fatalf("ReleaseStaleBlockClaim: %v", err)
	}
	if outcome == BlockClaimReleased {
		t.Fatal("a candidate for P1 released a fence belonging to P2; age is not authority over an incarnation this worker was never given")
	}
	if outcome != BlockClaimTooFresh {
		t.Fatalf("stale release across incarnations = %s, want too_fresh so the caller postpones instead of settling", outcome)
	}
	blk := store.GetBlock(orgID, "blk-precheck")
	if blk == nil || blk.GCClaimID != survivor.ClaimID || blk.GCState != db.BlockGCStateDeleting {
		t.Fatalf("P2's fence did not survive a P1 worker's stale release (block=%+v)", blk)
	}

	// And the same call, correctly named, still lifts P2's own abandoned fence.
	if outcome, err := store.ReleaseStaleBlockClaim(orgID, "blk-precheck", survivor.Target, time.Now().Add(-blockDeleteClaimStaleAfter)); err != nil || outcome != BlockClaimReleased {
		t.Fatalf("stale release naming the right incarnation = %s, %v; want released — binding to P must not cost the unwedging this path exists for", outcome, err)
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

	claim, err := store.ClaimBlockDelete(orgID, "blk-repairing", p4aAttempt(candidate, time.Now().UTC()))
	if err != nil {
		t.Fatalf("claim over repairing_stub: %v", err)
	}
	if claim.Outcome == BlockClaimAcquired {
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
	candidate := store.AllBlockGCCandidates()[0]
	store.SeedExactBlockQueueItemForTest(orgID, candidateAt, "blk-no-key", "hot", candidate.Identity(), 0)

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
	enqueueExactBlockCandidateForTest(t, store, candidate, 0)
	timeout := errors.New("gocql: no response received from cassandra within timeout period")
	store.SetClaimBlockDeleteErrForTest(timeout)
	store.SetClaimBlockDeleteSettleErrForTest(timeout)

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

	if _, err := store.EnsureBlockGCCandidateExact(orgID, "blk-absent", "hot", time.Now()); !errors.Is(err, ErrBlockCandidateTargetUnavailable) {
		t.Fatalf("EnsureBlockGCCandidateExact(no canonical row) = %v, want ErrBlockCandidateTargetUnavailable", err)
	}
	store.AddStubBlockForTest(orgID, "blk-locatorless")
	if _, err := store.EnsureBlockGCCandidateExact(orgID, "blk-locatorless", "hot", time.Now()); !errors.Is(err, ErrBlockCandidateTargetUnavailable) {
		t.Fatalf("EnsureBlockGCCandidateExact(no locator) = %v, want ErrBlockCandidateTargetUnavailable", err)
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
	enqueueExactBlockCandidateForTest(t, store, candidate, 0)

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
		if _, ok := store.GetBlockGCCandidateForTest(orgID, "blk-per-attempt"); !ok {
			store.AddBlockGCCandidate(orgID, "blk-per-attempt", "hot", candidate.CandidateAt)
			enqueueExactBlockCandidateForTest(t, store, store.AllBlockGCCandidates()[0], 0)
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

// TestP4A_DestructiveLocatorComesFromTheClaimNotAReadBack is the orphan/S3 half of R14.
//
// The claim binds to the incarnation the candidate authorized, and FinalizeBlockDelete
// was already bound to it — but the two steps that actually touch bytes, the orphan
// publication and the S3 delete, used to take their locator from a GetBlockInfo re-read.
// That read is an ordinary one (`database.consistency` accepts ONE) while the claim
// commits at EACH_QUORUM in the serial domain, so it can legitimately show a different
// incarnation. Publishing and deleting THAT is the same "re-read blocks and destroy what
// is there now" the candidate authority exists to forbid, one step further down.
func TestP4A_DestructiveLocatorComesFromTheClaimNotAReadBack(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, &Stats{})

	orgID := uuid.New()
	candidate := p4aSeedBlockCandidate(t, store, orgID, testSHA256BlockID("blk-readback"))
	p1 := candidate.Target
	enqueueExactBlockCandidateForTest(t, store, candidate, 0)

	// The claim succeeds against P1, and only afterwards does the canonical row read
	// back as a different incarnation — the divergence a ONE-consistency read can show.
	p2Key := p1.StorageKey + ".remint"
	store.SetGetBlockInfoHookForTest(func(info BlockInfo) BlockInfo {
		info.StorageKey = p2Key
		return info
	})

	if _, err := w.ProcessOnce(context.Background()); err != nil {
		// A refusal here is expected to surface as an ordinary error; what must not
		// happen is a publication or a delete naming P2.
		t.Logf("ProcessOnce returned %v (a refusal is the correct outcome)", err)
	}

	for _, orphan := range store.AllS3Orphans() {
		if orphan.StorageKey == p2Key {
			t.Fatalf("published an orphan for %q, an incarnation the claim never authorized", p2Key)
		}
	}
	for _, deleted := range sp.ScopedBlockDeletes() {
		if deleted.StorageKey == p2Key {
			t.Fatalf("deleted %q from S3, an incarnation the claim never authorized", p2Key)
		}
	}
	if blk := store.GetBlock(orgID, testSHA256BlockID("blk-readback")); blk != nil && blk.GCState != "" {
		t.Errorf("refused the divergent row but left its fence up (gc_state=%q)", blk.GCState)
	}
}

// TestP4A_MissingCanonicalRowDoesNotAbortTheEnqueueBatch pins the sentinel contract.
//
// A block with no canonical row has nothing reclaimable, which processBlock itself treats
// as routine further down the walk. Failing the enqueue hard on it is not just
// inconsistent: on the fs_object path it is self-poisoning, because the caller aborts the
// whole delete, the retry re-derives the same zero-ref block through an idempotent
// reference removal, and the fs_object is never deleted at all.
func TestP4A_MissingCanonicalRowDoesNotAbortTheEnqueueBatch(t *testing.T) {
	store := NewMockStore()
	w := NewWorker(store, nil, NewQueue(store), 100, 0, false, &Stats{})
	orgID := uuid.New()

	// One collectable block either side of one that has nothing to collect.
	store.AddBlock(orgID, "blk-before", "hot", 0)
	store.AddBlock(orgID, "blk-after", "hot", 0)

	err := w.enqueueZeroRefBlocks(orgID, uuid.Nil, []string{"blk-before", "blk-gone", "blk-after"}, "hot")
	if err != nil {
		t.Fatalf("enqueueZeroRefBlocks aborted on a block with nothing reclaimable: %v", err)
	}

	queued := map[string]bool{}
	for _, item := range store.QueueItems(orgID) {
		queued[item.ItemID] = true
	}
	if !queued["blk-before"] || !queued["blk-after"] {
		t.Fatalf("siblings of an unreclaimable block were dropped from the batch (queued=%v)", queued)
	}
	if queued["blk-gone"] {
		t.Fatal("a block with no canonical row was enqueued; there is no incarnation to authorize its deletion")
	}
	if got := len(store.AllBlockGCCandidates()); got != 2 {
		t.Fatalf("candidate rows = %d, want 2: one per collectable block and none for the missing one", got)
	}
}

// TestP4A_ReplacedCandidateServesItsOwnGracePeriod: replacement gives the new incarnation
// a fresh candidate_at precisely so it serves its own grace. The queue's grace check runs
// against the QUEUE row's timestamp, so an old item that already cleared grace would
// otherwise pick the fresh candidate up immediately and hand the new life exactly the
// head start replacement exists to deny it.
func TestP4A_ReplacedCandidateServesItsOwnGracePeriod(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	const grace = time.Hour
	w := NewWorker(store, sp, NewQueue(store), 100, grace, false, &Stats{})

	orgID := uuid.New()
	blockID := testSHA256BlockID("blk-grace")
	store.AddBlock(orgID, blockID, "hot", 0)

	// THE GRACE PERIOD BELONGS TO THE CANDIDATE, NOT TO THE QUEUE ROW, and this is
	// the state that separates the two: a queue row whose queued_at cleared the
	// grace window long ago, pointing at a candidate decided moments ago.
	//
	// The item carries that candidate's EXACT identity, which is what makes this
	// test reach the grace check at all. An earlier version re-decided the
	// candidate with a fresh candidate_at and left the queue item on the old one;
	// under exact-P identity the worker then settled it as unauthorized and
	// returned long before the grace comparison, so the gate it meant to pin was
	// never executed and the mutation harness could not turn it red.
	candidate, err := store.EnsureBlockGCCandidateExact(orgID, blockID, "hot", time.Now())
	if err != nil {
		t.Fatalf("EnsureBlockGCCandidateExact: %v", err)
	}
	if candidate.Target.IsZero() {
		t.Fatal("the candidate lost its incarnation; this test must reach the grace check, not a locator refusal")
	}
	enqueueExactBlockCandidateAtForTest(t, store, candidate, time.Now().Add(-24*time.Hour), 0)

	if _, err := w.ProcessOnce(context.Background()); err != nil {
		t.Logf("ProcessOnce returned %v (a postponement is the correct outcome)", err)
	}
	if deletes := sp.ScopedBlockDeletes(); len(deletes) != 0 {
		t.Fatalf("deleted a freshly decided incarnation inside its grace period: %+v", deletes)
	}
	if blk := store.GetBlock(orgID, blockID); blk == nil || blk.GCState != "" {
		t.Fatalf("claimed a freshly decided incarnation inside its grace period (block=%+v)", blk)
	}
	if got := len(store.AllBlockGCCandidates()); got != 1 {
		t.Fatalf("candidate rows = %d, want 1: postponing must not consume the work item", got)
	}
}
