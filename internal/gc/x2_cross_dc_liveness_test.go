package gc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// These tests pin ISSUE-GC-CROSS-DC-REFERENCE-VISIBILITY-01 (X2).
//
// The defect: reference writes and GC liveness reads both ran at LOCAL_QUORUM, so
// with RF 1 per DC the write quorum in one datacenter and the read quorum in another
// need not intersect. GC in NA could read "zero references" for a block whose only
// reference had already been acknowledged in EU, and delete live data.
//
// The closure: the read that AUTHORIZES destruction is pinned to EACH_QUORUM, which
// must obtain a quorum in every datacenter and therefore intersects the quorum that
// acknowledged the write. Reads that only drive discovery stay at the session level,
// because their FALSE answer authorizes nothing.
//
// A unit test cannot observe a consistency level, so these lock in the two
// properties that a same-process test CAN prove and that a regression would break:
// which read authorizes the delete, and that an unavailable datacenter fails closed.
// The wire-level per-DC behaviour is covered by the multi-DC integration suite.

// TestX2_DestructiveVerifyUsesGlobalRead is the canary against a silent revert. If
// someone changes claim-then-verify back to the session-consistency read, the delete
// still succeeds and every other block test still passes — only this one fails.
func TestX2_DestructiveVerifyUsesGlobalRead(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)

	orgID := uuid.New()
	store.AddBlock(orgID, "block-1", "hot", 0)
	store.EnqueueItem(orgID, time.Now().Add(-2*time.Hour), ItemBlock, "block-1", uuid.Nil, "hot", 0)

	if _, err := w.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce failed: %v", err)
	}

	// The delete must have happened, and it must have been authorized globally.
	if got := len(sp.ScopedBlockDeletes()); got != 1 {
		t.Fatalf("expected the block to be deleted once, got %d deletes", got)
	}
	local, global := store.BlockHasReferencesCallCountsForTest()
	if global < 1 {
		t.Errorf("claim-then-verify must use the EACH_QUORUM read: global liveness reads = %d, want >= 1", global)
	}
	// The pre-claim check stays local on purpose: a local zero authorizes nothing,
	// so paying WAN there buys nothing the verify does not re-establish.
	if local < 1 {
		t.Errorf("pre-claim check should still use the session-consistency read: local reads = %d, want >= 1", local)
	}
}

// TestX2_UnavailableDatacenterFailsClosed proves the policy the ADR states as "when
// in doubt, do not delete". EACH_QUORUM fails when a datacenter is unreachable, and
// that error must abort the delete rather than be read as "no references".
func TestX2_UnavailableDatacenterFailsClosed(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)

	orgID := uuid.New()
	store.AddBlock(orgID, "block-1", "hot", 0)
	store.AddBlockMapping(orgID, "sha1-abc", "block-1")
	store.EnqueueItem(orgID, time.Now().Add(-2*time.Hour), ItemBlock, "block-1", uuid.Nil, "hot", 0)

	// A datacenter is unreachable: the global read errors instead of answering.
	store.SetBlockHasReferencesGlobalErrForTest(fakeRequestError{code: gocql.ErrCodeUnavailable, msg: "Cannot achieve consistency level EACH_QUORUM in DC dc-asia"})

	// ProcessOnce reports the item as attempted; the error is logged and requeued.
	if _, err := w.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce returned a fatal error: %v", err)
	}

	if deletes := sp.ScopedBlockDeletes(); len(deletes) != 0 {
		t.Errorf("fail-closed violated: block deleted from storage despite an unavailable DC: %+v", deletes)
	}
	if store.GetBlock(orgID, "block-1") == nil {
		t.Error("fail-closed violated: canonical blocks row removed despite an unavailable DC")
	}
	if !store.ForwardBlockMappingExists(orgID, "sha1-abc") {
		t.Error("fail-closed violated: forward mapping cleaned up despite an unavailable DC")
	}
	if stats.BlocksDeleted() != 0 {
		t.Errorf("BlocksDeleted = %d, want 0 when the liveness read could not be established", stats.BlocksDeleted())
	}
}

// TestX2_FailedVerifyDoesNotWedgeTheBlock covers the availability side of failing
// closed. Making the destructive verify global turned "a DC is unreachable" from a
// rare local error into a systematic one, and the claim is taken before that read.
// If a failed verify left the claim held, gc_state would stay 'deleting', every
// writer of that content would see the fence, and a later reference arriving would
// settle the queue item through the pre-check without ever releasing it — fencing
// the block permanently. Failing closed must not mean wedging the block.
func TestX2_FailedVerifyDoesNotWedgeTheBlock(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)

	orgID := uuid.New()
	store.AddBlock(orgID, "block-1", "hot", 0)
	queuedAt := time.Now().Add(-2 * time.Hour)
	store.EnqueueItem(orgID, queuedAt, ItemBlock, "block-1", uuid.Nil, "hot", 0)

	// Attempt 1: a datacenter is unreachable, so the verify fails after the claim.
	store.SetBlockHasReferencesGlobalErrForTest(fakeRequestError{code: gocql.ErrCodeUnavailable, msg: "Cannot achieve consistency level EACH_QUORUM in DC dc-asia"})
	if _, err := w.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce returned a fatal error: %v", err)
	}

	if deletes := sp.ScopedBlockDeletes(); len(deletes) != 0 {
		t.Fatalf("fail-closed violated: %+v", deletes)
	}
	// The block must be usable again: a held claim is what writers see as a fence.
	if blk := store.GetBlock(orgID, "block-1"); blk == nil {
		t.Fatal("canonical block row disappeared after a failed verify")
	} else if blk.GCState != "" {
		t.Errorf("block left claimed after a failed verify (gc_state=%q); writers would see a permanent GC fence", blk.GCState)
	}
}

// TestX2_ReferencedBlockReleasesAStaleClaim covers the same wedge from the other
// side. The verify's own error path hands its claim back, so the only claim that can
// outlive an attempt is one whose process died between claiming and releasing. This
// is the last pass that will ever look at the candidate, so it must hand that claim
// back rather than clear the candidate and walk away from a block stuck in
// 'deleting' — which would fence every future upload of the content forever.
func TestX2_ReferencedBlockReleasesAStaleClaim(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)

	orgID := uuid.New()
	store.AddBlock(orgID, "block-1", "hot", 0)
	queuedAt := time.Now().Add(-2 * time.Hour)
	store.EnqueueItem(orgID, queuedAt, ItemBlock, "block-1", uuid.Nil, "hot", 0)

	// An earlier attempt claimed the row and its process died before releasing.
	applied, err := store.ClaimBlockDelete(orgID, "block-1", blockDeleteClaimID(queuedAt))
	if err != nil || !applied {
		t.Fatalf("seed abandoned claim: applied=%v err=%v", applied, err)
	}
	// A writer republishes the content afterwards.
	store.AddBlockReferenceForTest(orgID, "block-1", "fs:lib:obj")

	// Run far enough past the claim that it cannot belong to a live attempt.
	w.clock = func() time.Time { return time.Now().Add(2 * blockDeleteClaimStaleAfter) }

	if _, err := w.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce returned a fatal error: %v", err)
	}

	if blk := store.GetBlock(orgID, "block-1"); blk == nil {
		t.Fatal("canonical block row disappeared for a referenced block")
	} else if blk.GCState != "" {
		t.Errorf("referenced block left in gc_state=%q; uploads of this content would stay fenced forever", blk.GCState)
	}
	if deletes := sp.ScopedBlockDeletes(); len(deletes) != 0 {
		t.Errorf("deleted a referenced block: %+v", deletes)
	}
}

// TestX2_ReferencedBlockLeavesAFreshClaimAlone is the counterweight to the test
// above, and the reason the release is conditional on age at all.
//
// claimID is derived from the candidate timestamp, so every attempt on one candidate
// shares it — including two workers running at once, since DequeueBatch hands the
// same row to both. An unconditional release would therefore let the worker that
// merely OBSERVES a reference hand back the claim of a worker that is mid-delete,
// dropping the upload fence in precisely the window it exists to cover. A claim young
// enough to be live must be left exactly where it is.
//
// But leaving the claim alone is only half the requirement, and the other half is
// what makes this test worth having. Declining to release must NOT be treated as
// "nothing to do here". The claim may equally belong to an attempt that died seconds
// ago, and this candidate is the only work item that will ever revisit the block, so
// settling it now would leave gc_state='deleting' with nothing left to clear it —
// fencing every future upload of that content forever. The candidate has to survive
// until the claim ages out, and then the fence has to actually come off.
func TestX2_ReferencedBlockLeavesAFreshClaimAlone(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)

	orgID := uuid.New()
	store.AddBlock(orgID, "block-1", "hot", 0)
	queuedAt := time.Now().Add(-2 * time.Hour)
	store.AddBlockGCCandidate(orgID, "block-1", "hot", queuedAt)
	store.EnqueueItem(orgID, queuedAt, ItemBlock, "block-1", uuid.Nil, "hot", 0)

	// Another worker is walking this same candidate right now and holds the claim.
	applied, err := store.ClaimBlockDelete(orgID, "block-1", blockDeleteClaimID(queuedAt))
	if err != nil || !applied {
		t.Fatalf("seed concurrent claim: applied=%v err=%v", applied, err)
	}
	store.AddBlockReferenceForTest(orgID, "block-1", "fs:lib:obj")

	if _, err := w.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce returned a fatal error: %v", err)
	}

	blk := store.GetBlock(orgID, "block-1")
	if blk == nil {
		t.Fatal("canonical block row disappeared for a referenced block")
	}
	if blk.GCState != db.BlockGCStateDeleting {
		t.Errorf("released a claim that a concurrent attempt still owns (gc_state=%q); that attempt would delete the bytes with the upload fence already down", blk.GCState)
	}
	// The candidate must still be there. Without this assertion the fence could be
	// left held with no work item able to lift it, and every other check here would
	// still pass.
	if got := store.AllBlockGCCandidates(); len(got) != 1 {
		t.Fatalf("candidate rows = %d, want 1: declining to release a fresh claim must not settle the item, or nothing is left to lift the fence when it goes stale", len(got))
	}

	// Surviving one pass is not enough, and this is the part that dictates HOW the
	// item is requeued. The claim needs the full staleness threshold to age out —
	// minutes, not seconds — which is many more passes than the five-retry budget
	// allows. If waiting burned a retry the item would reach the DLQ long before the
	// fence could be lifted, where block items never auto-recover: the same permanent
	// wedge, arrived at from the other side. Waiting must postpone, not retry.
	for i := 0; i < 8; i++ {
		if _, err := w.ProcessOnce(context.Background()); err != nil {
			t.Fatalf("ProcessOnce %d returned a fatal error: %v", i, err)
		}
	}
	if failed, err := store.GetTotalFailedItems(); err != nil {
		t.Fatalf("GetTotalFailedItems: %v", err)
	} else if failed != 0 {
		t.Fatalf("%d item(s) reached the DLQ while waiting for a claim to go stale; block items do not auto-recover from there, so the block would stay fenced forever", failed)
	}
	if got := store.AllBlockGCCandidates(); len(got) != 1 {
		t.Fatalf("candidate rows = %d after waiting, want 1", len(got))
	}

	// Once the claim is old enough to be certainly abandoned, the next pass must
	// actually release it and only then settle the candidate.
	w.clock = func() time.Time { return time.Now().Add(2 * blockDeleteClaimStaleAfter) }
	if _, err := w.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("second ProcessOnce returned a fatal error: %v", err)
	}
	if blk := store.GetBlock(orgID, "block-1"); blk == nil {
		t.Fatal("canonical block row disappeared for a referenced block")
	} else if blk.GCState != "" {
		t.Errorf("block still fenced (gc_state=%q) after its claim went stale; uploads of this content would stay blocked forever", blk.GCState)
	}
	if got := store.AllBlockGCCandidates(); len(got) != 0 {
		t.Errorf("candidate rows = %d after the fence was lifted, want 0", len(got))
	}
	if deletes := sp.ScopedBlockDeletes(); len(deletes) != 0 {
		t.Errorf("deleted a referenced block: %+v", deletes)
	}
}

// TestX2_StaleClaimReleaseFailureSurvivesTheRetryBudget is the strong form of the
// rule below, and the one that actually holds the guarantee up.
//
// Preserving the candidate ROW for one pass is not the property. The row survives an
// ordinary error too; what does not survive is the QUEUE ITEM, and the item is the
// part that does the work. Five failures retire it to the DLQ, block items never
// auto-recover from there, and the scanner's day cursor has already stepped past this
// candidate's bucket — so the fence stands on a live, still-referenced block with
// nothing left in the system able to lift it, and every future upload of that content
// is refused by BlockDeleteFenceActive.
//
// The injected error is deliberately one that isClusterUnavailableError does NOT
// recognise. Routing this branch through failClosedIfUnavailable would postpone the
// availability case and let exactly this case burn the budget, so a test that injects
// an availability failure proves nothing about the branch that matters.
func TestX2_StaleClaimReleaseFailureSurvivesTheRetryBudget(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)

	orgID := uuid.New()
	store.AddBlock(orgID, "block-1", "hot", 0)
	queuedAt := time.Now().Add(-2 * time.Hour)
	store.AddBlockGCCandidate(orgID, "block-1", "hot", queuedAt)
	store.EnqueueItem(orgID, queuedAt, ItemBlock, "block-1", uuid.Nil, "hot", 0)
	store.AddBlockReferenceForTest(orgID, "block-1", "fs:lib:obj")

	// A real fence on a real live block: the state the release exists to clear. Aged
	// via the store rather than by moving the worker's clock forward — see
	// BackdateBlockClaimForTest for why a future clock would make the multi-pass
	// assertion below vacuous.
	applied, err := store.ClaimBlockDelete(orgID, "block-1", blockDeleteClaimID(queuedAt))
	if err != nil || !applied {
		t.Fatalf("seed abandoned claim: applied=%v err=%v", applied, err)
	}
	store.BackdateBlockClaimForTest(orgID, "block-1", time.Now().Add(-2*blockDeleteClaimStaleAfter))

	// Permanent and item-specific — the shape of an unknown column or a serialization
	// bug in the release statement, not of a datacenter outage.
	store.SetReleaseStaleBlockClaimErrForTest(errors.New("undefined column gc_claimed_at in table blocks"))

	// The counter is the only thing that will ever surface this item, since it now
	// postpones forever instead of reaching the DLQ. Asserting on it also rules out the
	// wrong way to make this test pass: broadening isClusterUnavailableError until it
	// swallows permanent errors would keep the item out of the DLQ here while silently
	// suppressing genuinely DLQ-worthy failures at every other statement in the walk.
	before := testutil.ToFloat64(metrics.GCErrorsTotal.WithLabelValues("stale_claim_release_failed"))

	// More passes than the five-retry budget would have survived. Under the old
	// behaviour the sixth would have moved the item to the DLQ.
	for i := 0; i < 8; i++ {
		if _, err := w.ProcessOnce(context.Background()); err != nil {
			t.Fatalf("ProcessOnce %d returned a fatal error: %v", i, err)
		}
	}

	if got := testutil.ToFloat64(metrics.GCErrorsTotal.WithLabelValues("stale_claim_release_failed")) - before; got != 8 {
		t.Errorf("gc_errors_total{type=\"stale_claim_release_failed\"} rose by %v over 8 refused passes, want 8 — this counter is the only signal a human gets for a fence that will never lift on its own", got)
	}

	if failed, err := store.GetTotalFailedItems(); err != nil {
		t.Fatalf("GetTotalFailedItems: %v", err)
	} else if failed != 0 {
		t.Fatalf("%d item(s) reached the DLQ after a permanently failing stale-claim release; block items do not auto-recover from there and the scanner cursor has moved on, so the live block would stay fenced forever", failed)
	}
	if got := store.AllBlockGCCandidates(); len(got) != 1 {
		t.Fatalf("candidate rows = %d after %d failed releases, want the candidate preserved", len(got), 8)
	}
	if blk := store.GetBlock(orgID, "block-1"); blk == nil {
		t.Fatal("canonical block row disappeared for a referenced block")
	} else if blk.GCState != db.BlockGCStateDeleting {
		t.Fatalf("test no longer models a failed release of a real fence: gc_state=%q", blk.GCState)
	}
	if deletes := sp.ScopedBlockDeletes(); len(deletes) != 0 {
		t.Fatalf("deleted a referenced block: %+v", deletes)
	}

	// And the work is still live: once the underlying fault is fixed, the same item
	// lifts the fence and settles. This is what "the candidate was not consumed" has
	// to mean to be worth anything.
	store.SetReleaseStaleBlockClaimErrForTest(nil)
	if _, err := w.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("recovery ProcessOnce returned a fatal error: %v", err)
	}
	if blk := store.GetBlock(orgID, "block-1"); blk == nil {
		t.Fatal("canonical block row disappeared for a referenced block")
	} else if blk.GCState != "" {
		t.Errorf("fence still up (gc_state=%q) after the release recovered", blk.GCState)
	}
	if got := store.AllBlockGCCandidates(); len(got) != 0 {
		t.Errorf("candidate rows = %d after recovery, want the item settled", len(got))
	}
}

// TestX2_ReReferencedBlockSurvivesAFailedClaimRelease is the same rule as the stale
// test above, at the site where it bites hardest and where it was missed the first
// time: the POST-claim release on a block the EACH_QUORUM verify just proved alive.
//
// The fixture is the X2 divergence itself, which is what makes this reachable rather
// than theoretical. The local pre-check answers FALSE (the reference lives in another
// datacenter), so the walk claims the block and proceeds; the global verify then
// answers TRUE. The branch must hand the claim back — and if it cannot, the fence is
// standing on LIVE data.
//
// Why the next pass cannot be relied on to clean up: the pre-check is the LOCAL read,
// and it keeps answering false for as long as the divergence lasts, so every pass
// returns to this same branch instead of settling through the pre-check's safe path.
// Spending the budget here therefore ends in the DLQ, which ItemBlock never leaves,
// with gc_state='deleting' left on a referenced block forever.
func TestX2_ReReferencedBlockSurvivesAFailedClaimRelease(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)

	orgID := uuid.New()
	store.AddBlock(orgID, "block-1", "hot", 0)
	queuedAt := time.Now().Add(-2 * time.Hour)
	store.AddBlockGCCandidate(orgID, "block-1", "hot", queuedAt)
	store.EnqueueItem(orgID, queuedAt, ItemBlock, "block-1", uuid.Nil, "hot", 0)

	// The divergence, held for EVERY pass: local reads blind, global reads see the
	// reference. The hook is shared by both readers, so it tells them apart by which
	// counter just moved — the mock increments before dispatching. A cumulative test
	// like "global > 0" would make the LOCAL read start answering true from pass two
	// onward, the walk would settle through the pre-check's safe path, and the wedge
	// this test exists to catch would never be reached again.
	lastGlobal := 0
	store.SetBlockHasReferencesHookForTest(func(_ uuid.UUID, _ string, _ bool) (bool, error) {
		_, global := store.BlockHasReferencesCallCountsForTest()
		isGlobalRead := global > lastGlobal
		lastGlobal = global
		return isGlobalRead, nil
	})

	// Permanent and item-specific — not something isClusterUnavailableError knows.
	store.SetReleaseBlockClaimErrForTest(errors.New("undefined column gc_claim_id in table blocks"))

	before := testutil.ToFloat64(metrics.GCErrorsTotal.WithLabelValues("block_claim_release_failed"))

	for i := 0; i < 8; i++ {
		if _, err := w.ProcessOnce(context.Background()); err != nil {
			t.Fatalf("ProcessOnce %d returned a fatal error: %v", i, err)
		}
	}

	if failed, err := store.GetTotalFailedItems(); err != nil {
		t.Fatalf("GetTotalFailedItems: %v", err)
	} else if failed != 0 {
		t.Fatalf("%d item(s) reached the DLQ after a permanently failing post-claim release; block items do not auto-recover from there, so this still-referenced block would stay fenced forever", failed)
	}
	if got := store.AllBlockGCCandidates(); len(got) != 1 {
		t.Fatalf("candidate rows = %d, want the candidate preserved so a later pass can retry the release", len(got))
	}
	if deletes := sp.ScopedBlockDeletes(); len(deletes) != 0 {
		t.Fatalf("deleted a block the global verify reported as referenced: %+v", deletes)
	}
	if got := testutil.ToFloat64(metrics.GCErrorsTotal.WithLabelValues("block_claim_release_failed")) - before; got != 8 {
		t.Errorf("gc_errors_total{type=\"block_claim_release_failed\"} rose by %v over 8 refused passes, want 8", got)
	}

	// Once the store recovers, the same item lifts the fence and settles.
	store.SetReleaseBlockClaimErrForTest(nil)
	if _, err := w.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("recovery ProcessOnce returned a fatal error: %v", err)
	}
	if blk := store.GetBlock(orgID, "block-1"); blk == nil {
		t.Fatal("canonical block row disappeared for a referenced block")
	} else if blk.GCState != "" {
		t.Errorf("fence still up (gc_state=%q) after the release recovered", blk.GCState)
	}
	if got := store.AllBlockGCCandidates(); len(got) != 0 {
		t.Errorf("candidate rows = %d after recovery, want the item settled", len(got))
	}
}

// TestX2_FailedVerifyPlusFailedReleaseDoesNotBurnTheBudget covers the other post-claim
// site: the global verify fails for a NON-availability reason (a ReadFailure from a
// tombstone-heavy partition), which by itself is correctly DLQ-bound — and the claim
// release fails too.
//
// The queue policy used to be decided from the verify's error while the release error
// was only logged, so this combination spent five retries and parked the item in the
// DLQ with gc_state='deleting' still set. The release error must dominate until the
// fence is confirmed gone; the ReadFailure is re-reached, and reaches the DLQ as it
// should, once the release succeeds.
func TestX2_FailedVerifyPlusFailedReleaseDoesNotBurnTheBudget(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)

	orgID := uuid.New()
	store.AddBlock(orgID, "block-1", "hot", 0)
	queuedAt := time.Now().Add(-2 * time.Hour)
	store.AddBlockGCCandidate(orgID, "block-1", "hot", queuedAt)
	store.EnqueueItem(orgID, queuedAt, ItemBlock, "block-1", uuid.Nil, "hot", 0)

	// The FRAME the driver actually returns for a poisoned partition, not a plain
	// error carrying its text — a plain error can never classify as environmental
	// whatever the classifier does, so it would pass even against a classifier that
	// wrongly swallows ReadFailure. Deliberately NOT an availability failure, so on
	// its own this error must spend retries and reach the DLQ.
	store.SetBlockHasReferencesGlobalErrForTest(fakeRequestError{
		code: gocql.ErrCodeReadFailure,
		msg:  "Operation failed - received 0 responses and 1 failures: TOMBSTONE_OVERWHELMING",
	})
	store.SetReleaseBlockClaimErrForTest(errors.New("undefined column gc_claim_id in table blocks"))

	for i := 0; i < 8; i++ {
		if _, err := w.ProcessOnce(context.Background()); err != nil {
			t.Fatalf("ProcessOnce %d returned a fatal error: %v", i, err)
		}
	}

	if failed, err := store.GetTotalFailedItems(); err != nil {
		t.Fatalf("GetTotalFailedItems: %v", err)
	} else if failed != 0 {
		t.Fatalf("%d item(s) reached the DLQ while the fence could not be confirmed gone; the ReadFailure belongs in the DLQ, but only after the claim is off the row", failed)
	}
	if deletes := sp.ScopedBlockDeletes(); len(deletes) != 0 {
		t.Fatalf("deleted a block whose liveness could not be established: %+v", deletes)
	}

	// Release recovers; the ReadFailure resumes its own (correct) march to the DLQ.
	store.SetReleaseBlockClaimErrForTest(nil)
	for i := 0; i < 8; i++ {
		if _, err := w.ProcessOnce(context.Background()); err != nil {
			t.Fatalf("post-recovery ProcessOnce %d returned a fatal error: %v", i, err)
		}
	}
	if failed, err := store.GetTotalFailedItems(); err != nil {
		t.Fatalf("GetTotalFailedItems: %v", err)
	} else if failed == 0 {
		t.Error("a persistent non-availability verify failure never reached the DLQ once the fence was confirmed off; it must still be visible to a human")
	}
	if blk := store.GetBlock(orgID, "block-1"); blk == nil {
		t.Fatal("canonical block row disappeared")
	} else if blk.GCState != "" {
		t.Errorf("fence still up (gc_state=%q) after the release recovered", blk.GCState)
	}
}

// TestX2_StaleClaimReleaseFailureKeepsTheCandidate pins the single-pass half: a failed
// release must not clear the candidate. The retry-budget test above is what proves
// that preservation is durable; this one localises a regression to the branch itself.
func TestX2_StaleClaimReleaseFailureKeepsTheCandidate(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)

	orgID := uuid.New()
	store.AddBlock(orgID, "block-1", "hot", 0)
	queuedAt := time.Now().Add(-2 * time.Hour)
	store.AddBlockGCCandidate(orgID, "block-1", "hot", queuedAt)
	store.EnqueueItem(orgID, queuedAt, ItemBlock, "block-1", uuid.Nil, "hot", 0)
	store.AddBlockReferenceForTest(orgID, "block-1", "fs:lib:obj")

	// There must be a REAL stale claim on the row, not just an injected error. The
	// mock short-circuits on the injected error before it inspects any claim, so
	// without this the test would pass against a block that has no fence on it at
	// all — proving "an error preserves the candidate" while claiming to prove
	// "a failed release of an actual fence preserves the candidate". Those differ
	// exactly where it matters: the second is the case where consuming the candidate
	// strands the block.
	applied, err := store.ClaimBlockDelete(orgID, "block-1", blockDeleteClaimID(queuedAt))
	if err != nil || !applied {
		t.Fatalf("seed abandoned claim: applied=%v err=%v", applied, err)
	}
	// Age the claim past the staleness threshold so the release is one that WOULD
	// succeed. Done on the row rather than by moving w.clock() forward: a future
	// worker clock requeues postponed items past DequeueBatch's time.Now() cutoff, so
	// the recovery pass below would find an empty queue and assert nothing.
	store.BackdateBlockClaimForTest(orgID, "block-1", time.Now().Add(-2*blockDeleteClaimStaleAfter))

	store.SetReleaseStaleBlockClaimErrForTest(errors.New("undefined column gc_claimed_at in table blocks"))

	if _, err := w.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce returned a fatal error: %v", err)
	}

	if got := store.AllBlockGCCandidates(); len(got) != 1 {
		t.Fatalf("candidate rows = %d, want the candidate preserved so a later pass can retry the release", len(got))
	}
	if blk := store.GetBlock(orgID, "block-1"); blk == nil {
		t.Fatal("canonical block row disappeared for a referenced block")
	} else if blk.GCState != db.BlockGCStateDeleting {
		t.Fatalf("test no longer models a failed release of a real fence: gc_state=%q", blk.GCState)
	}

	// Once the store recovers, the same candidate lifts the fence and settles.
	store.SetReleaseStaleBlockClaimErrForTest(nil)
	if _, err := w.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("second ProcessOnce returned a fatal error: %v", err)
	}
	if blk := store.GetBlock(orgID, "block-1"); blk == nil {
		t.Fatal("canonical block row disappeared for a referenced block")
	} else if blk.GCState != "" {
		t.Errorf("fence still up (gc_state=%q) after the release succeeded", blk.GCState)
	}
	if got := store.AllBlockGCCandidates(); len(got) != 0 {
		t.Errorf("candidate rows = %d after recovery, want the item settled", len(got))
	}
}

// TestX2_TopologyGateIsArmedWithoutExplicitWiring proves the gate cannot be lost by
// omission. The worker here is built exactly as production builds it — NewWorker,
// no SetDestructiveTopologyGate call — and a store whose topology check rejects must
// still stop the delete. Before the gate moved into GCStore this was an optional
// capability resolved by type assertion, so wrapping the store silently disarmed a
// data-loss guard.
func TestX2_TopologyGateIsArmedWithoutExplicitWiring(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)

	store.SetValidateDestructiveGCTopologyErrForTest(errors.New("live replication map no longer matches the declared topology"))

	orgID := uuid.New()
	store.AddBlock(orgID, "block-1", "hot", 0)
	store.EnqueueItem(orgID, time.Now().Add(-2*time.Hour), ItemBlock, "block-1", uuid.Nil, "hot", 0)

	if _, err := w.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce returned a fatal error: %v", err)
	}

	if deletes := sp.ScopedBlockDeletes(); len(deletes) != 0 {
		t.Errorf("deleted bytes with the store's own topology gate rejecting: %+v", deletes)
	}
	if store.GetBlock(orgID, "block-1") == nil {
		t.Error("canonical blocks row removed despite the store's topology gate rejecting")
	}
}

// TestX2_FailClosedDoesNotBurnTheRetryBudget covers the second-order cost of making
// the destructive verify global.
//
// An unreachable DC is no longer a rare per-item error: every block in flight fails
// on the same tick for the same reason. At five retries and one retry burned per
// pass, a short outage would push the whole in-flight set into the DLQ — where
// ItemBlock is not auto-recoverable, and where the scanner's day cursor has already
// moved past the candidates that would otherwise rediscover them. A stall would
// quietly become permanently uncollectable storage. So these failures postpone
// instead: the item stays live and un-incremented until the environment recovers.
func TestX2_FailClosedDoesNotBurnTheRetryBudget(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)

	orgID := uuid.New()
	store.AddBlock(orgID, "block-1", "hot", 0)
	store.EnqueueItem(orgID, time.Now().Add(-2*time.Hour), ItemBlock, "block-1", uuid.Nil, "hot", 0)

	store.SetBlockHasReferencesGlobalErrForTest(fakeRequestError{code: gocql.ErrCodeUnavailable, msg: "Cannot achieve consistency level EACH_QUORUM in DC dc-asia"})

	// Ride out an outage longer than the five-retry budget would survive.
	for i := 0; i < 8; i++ {
		if _, err := w.ProcessOnce(context.Background()); err != nil {
			t.Fatalf("ProcessOnce %d returned a fatal error: %v", i, err)
		}
	}

	failed, err := store.GetTotalFailedItems()
	if err != nil {
		t.Fatalf("GetTotalFailedItems: %v", err)
	}
	if failed != 0 {
		t.Errorf("%d item(s) reached the DLQ during a fail-closed outage; block items are not auto-recoverable from there and the scanner cursor has moved past their candidates", failed)
	}

	// When the datacenter comes back the same item still collects, with no operator
	// intervention and no rediscovery needed.
	store.SetBlockHasReferencesGlobalErrForTest(nil)
	if _, err := w.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce after recovery returned a fatal error: %v", err)
	}
	if got := len(sp.ScopedBlockDeletes()); got != 1 {
		t.Errorf("block deletes after recovery = %d, want 1; the work item did not survive the outage", got)
	}
}

// TestX2_UnsupportedTopologyBlocksDelete pins the gate. The EACH_QUORUM argument is
// stated per datacenter, so under a replication class that gives EACH_QUORUM no
// per-DC meaning the closure does not apply and the destructive path must refuse
// rather than delete under a proof that does not hold.
func TestX2_UnsupportedTopologyBlocksDelete(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)
	w.SetDestructiveTopologyGate(func() error {
		return errors.New("destructive GC requires NetworkTopologyStrategy; keyspace sesamefs uses SimpleStrategy")
	})

	orgID := uuid.New()
	store.AddBlock(orgID, "block-1", "hot", 0)
	store.EnqueueItem(orgID, time.Now().Add(-2*time.Hour), ItemBlock, "block-1", uuid.Nil, "hot", 0)

	if _, err := w.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce returned a fatal error: %v", err)
	}

	if deletes := sp.ScopedBlockDeletes(); len(deletes) != 0 {
		t.Errorf("deleted bytes under a topology that does not support the EACH_QUORUM argument: %+v", deletes)
	}
	if store.GetBlock(orgID, "block-1") == nil {
		t.Error("canonical blocks row removed despite the topology gate rejecting the delete")
	}
	// The gate must run before the claim, so no liveness read should have been needed.
	if _, global := store.BlockHasReferencesCallCountsForTest(); global != 0 {
		t.Errorf("gate should short-circuit before the destructive verify; global reads = %d", global)
	}
}

// TestX2_TopologyGateAlsoGuardsOrphanRecovery covers the second destructive path.
// RecoverS3Orphans does its own BlockHasReferencesGlobal before destroying bytes, but
// that read only closes X2 if the keyspace gives EACH_QUORUM a per-datacenter meaning.
// Without the same gate, a SimpleStrategy / shrunk-map cluster would still delete
// under a proof that does not apply — a hole no processBlock test would catch.
func TestX2_TopologyGateAlsoGuardsOrphanRecovery(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)
	w.SetDestructiveTopologyGate(func() error {
		return errors.New("replication map lost the local datacenter")
	})

	recovered, err := w.RecoverS3Orphans(context.Background(), 10)
	if err == nil {
		t.Fatal("expected RecoverS3Orphans to fail closed when the topology gate rejects")
	}
	if recovered != 0 {
		t.Errorf("recovered = %d, want 0 when the topology gate rejects", recovered)
	}
	if deletes := sp.ScopedBlockDeletes(); len(deletes) != 0 {
		t.Errorf("orphan recovery deleted bytes despite the topology gate: %+v", deletes)
	}
}

// TestX2_OrphanRecoveryRefusesAReferencedBlock covers the second destructive path's
// own authorization. Recovery used to delete bytes purely on the existence of a
// gc_s3_orphans row, which is only sound while every such row descends from an
// EACH_QUORUM verify — true forward in time, but not for a row written by an older
// binary. It now establishes the global zero itself, so a block that still has
// references keeps its bytes no matter who wrote the orphan row.
func TestX2_OrphanRecoveryRefusesAReferencedBlock(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)

	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	w.clock = func() time.Time { return now }

	orgID := uuid.New()
	seedS3Orphan(t, store, orgID, "orph-referenced", "hot", db.PlainBlockRepresentationID, "", "", now.AddDate(0, 0, -1))
	// The canonical row is gone (that is why there is an orphan row at all), but a
	// reference to the content exists somewhere in the fleet.
	store.AddBlockReferenceForTest(orgID, "orph-referenced", "fs:lib:obj")

	recovered, err := w.RecoverS3Orphans(context.Background(), 100)
	if recovered != 0 {
		t.Errorf("recovered = %d, want 0", recovered)
	}
	if deletes := sp.ScopedBlockDeletes(); len(deletes) != 0 {
		t.Errorf("orphan recovery destroyed the bytes of a still-referenced block: %+v", deletes)
	}
	if store.S3OrphanCount() != 1 {
		t.Error("orphan row discarded; it must survive for an operator to inspect")
	}
	// The refusal must NOT surface as a sweep error. The orphan row is permanent
	// until an operator acts, so a returned error would fail this scanner phase on
	// every pass, and a failed phase suppresses the scanner's last_scan_success
	// timestamp — one such row would permanently mask the health of everything else.
	// The refusal is reported through its audit counter and the log instead.
	if err != nil {
		t.Errorf("refusing a referenced orphan must not fail the sweep (it would freeze last_scan_success forever); got %v", err)
	}
}

// TestX2_OrphanRecoveryFailsClosedOnAnUnavailableDatacenter is the same policy as
// processBlock's: recovery's liveness read is EACH_QUORUM, so an unreachable DC makes
// it error, and that error must stop the delete rather than read as "no references".
func TestX2_OrphanRecoveryFailsClosedOnAnUnavailableDatacenter(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)

	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	w.clock = func() time.Time { return now }

	orgID := uuid.New()
	seedS3Orphan(t, store, orgID, "orph-dc-down", "hot", db.PlainBlockRepresentationID, "", "", now.AddDate(0, 0, -1))
	store.SetBlockHasReferencesGlobalErrForTest(fakeRequestError{code: gocql.ErrCodeUnavailable, msg: "Cannot achieve consistency level EACH_QUORUM in DC dc-asia"})

	recovered, err := w.RecoverS3Orphans(context.Background(), 100)
	if err == nil {
		t.Fatal("expected RecoverS3Orphans to fail closed when the global verify cannot be established")
	}
	if recovered != 0 {
		t.Errorf("recovered = %d, want 0", recovered)
	}
	if deletes := sp.ScopedBlockDeletes(); len(deletes) != 0 {
		t.Errorf("orphan recovery deleted bytes on an uncertain liveness read: %+v", deletes)
	}
}

// TestX2_RemoteReferenceVisibleToVerifyAbortsDelete models the defect end to end in
// the only way a single-process test can: the reference is invisible to the local
// pre-check and visible to the global verify, which is exactly the cross-DC shape.
// The block must survive.
func TestX2_RemoteReferenceVisibleToVerifyAbortsDelete(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)

	orgID := uuid.New()
	store.AddBlock(orgID, "block-1", "hot", 0)
	store.EnqueueItem(orgID, time.Now().Add(-2*time.Hour), ItemBlock, "block-1", uuid.Nil, "hot", 0)

	// Local reads see nothing (the write landed in another DC); the global read does.
	callsSeen := 0
	store.SetBlockHasReferencesHookForTest(func(_ uuid.UUID, _ string, current bool) (bool, error) {
		callsSeen++
		// First call is the local pre-check: report the local view, which is empty.
		if callsSeen == 1 {
			return false, nil
		}
		// The verify reaches the remote DC and finds the reference.
		return true, nil
	})

	if _, err := w.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce failed: %v", err)
	}

	if deletes := sp.ScopedBlockDeletes(); len(deletes) != 0 {
		t.Errorf("deleted a block that a remote DC still references: %+v", deletes)
	}
	if store.GetBlock(orgID, "block-1") == nil {
		t.Error("canonical blocks row removed for a block a remote DC still references")
	}
	if stats.BlocksDeleted() != 0 {
		t.Errorf("BlocksDeleted = %d, want 0", stats.BlocksDeleted())
	}
}
