package gc

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// These tests cover the gaps an audit of the X2 closure found: places where the
// closure's own reasoning was correct but did not reach far enough.
//
// The X2 fix established two rules — never delete on an uncertain read, and never let
// an environmental failure consume an item's retry budget — and then applied them to
// the specific statements where the defect had been observed. Each test below pins a
// place where the rule had to be extended to a statement it had skipped, or where
// following the rule required giving something else up.

// TestX2_StaleClaimFromAnotherCandidateIsReleased closes the leak that an owner-only
// release leaves behind.
//
// claimID identifies a CANDIDATE, not an attempt: it derives from the candidate
// timestamp. So a claim abandoned by candidate C1 carries C1's id, and a later
// candidate C2 walking the same block sees an id that is not its own. Releasing only
// "our" claim, C2 concludes that someone else's pass will lift it, and settles its
// item — but if C1's item is gone (DLQ'd, and block items never auto-recover from
// there) no such pass exists. gc_state stays 'deleting' and BlockDeleteFenceActive
// refuses every future upload of that content, permanently.
//
// Age is what actually separates an abandoned claim from a live one, so age is what
// the release tests. Ownership is not consulted.
func TestX2_StaleClaimFromAnotherCandidateIsReleased(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)

	orgID := uuid.New()
	store.AddBlock(orgID, "block-1", "hot", 0)

	// The abandoned claim belongs to an OLDER candidate whose queue item is gone —
	// the shape an owner-only release can never clean up.
	abandonedCandidateAt := time.Now().Add(-6 * time.Hour)
	applied, err := store.ClaimBlockDelete(orgID, "block-1", blockDeleteClaimID(abandonedCandidateAt))
	if err != nil || !applied {
		t.Fatalf("seed abandoned claim from an earlier candidate: applied=%v err=%v", applied, err)
	}

	// A new candidate for the same block, carrying its own distinct claim id.
	queuedAt := time.Now().Add(-2 * time.Hour)
	if got, want := blockDeleteClaimID(queuedAt), blockDeleteClaimID(abandonedCandidateAt); got == want {
		t.Fatalf("test is not exercising the cross-candidate case: both candidates derive claim id %q", got)
	}
	store.AddBlockGCCandidate(orgID, "block-1", "hot", queuedAt)
	store.EnqueueItem(orgID, queuedAt, ItemBlock, "block-1", uuid.Nil, "hot", 0)
	store.AddBlockReferenceForTest(orgID, "block-1", "fs:lib:obj")

	// Far enough past the abandoned claim that it cannot belong to a live attempt.
	w.clock = func() time.Time { return time.Now().Add(2 * blockDeleteClaimStaleAfter) }

	if _, err := w.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce returned a fatal error: %v", err)
	}

	blk := store.GetBlock(orgID, "block-1")
	if blk == nil {
		t.Fatal("canonical block row disappeared for a referenced block")
	}
	if blk.GCState != "" {
		t.Errorf("block left fenced (gc_state=%q) by a claim from an abandoned candidate; every future upload of this content would be refused forever", blk.GCState)
	}
	if got := store.AllBlockGCCandidates(); len(got) != 0 {
		t.Errorf("candidate rows = %d after the fence was lifted, want 0", len(got))
	}
	if deletes := sp.ScopedBlockDeletes(); len(deletes) != 0 {
		t.Errorf("deleted a referenced block: %+v", deletes)
	}
}

// TestX2_FreshClaimFromAnotherCandidateIsLeftAlone is the boundary of the test above.
// Releasing by age must not decay into releasing by nothing: a claim from another
// candidate that is still YOUNG may belong to a worker mid-delete, and handing it back
// would drop the upload fence inside the very window it exists to cover. "Not ours"
// is not a licence to release; only "old enough" is.
func TestX2_FreshClaimFromAnotherCandidateIsLeftAlone(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)

	orgID := uuid.New()
	store.AddBlock(orgID, "block-1", "hot", 0)

	otherCandidateAt := time.Now().Add(-6 * time.Hour)
	applied, err := store.ClaimBlockDelete(orgID, "block-1", blockDeleteClaimID(otherCandidateAt))
	if err != nil || !applied {
		t.Fatalf("seed concurrent claim from another candidate: applied=%v err=%v", applied, err)
	}

	queuedAt := time.Now().Add(-2 * time.Hour)
	store.AddBlockGCCandidate(orgID, "block-1", "hot", queuedAt)
	store.EnqueueItem(orgID, queuedAt, ItemBlock, "block-1", uuid.Nil, "hot", 0)
	store.AddBlockReferenceForTest(orgID, "block-1", "fs:lib:obj")

	// Clock left at real time: the claim was taken moments ago.
	if _, err := w.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce returned a fatal error: %v", err)
	}

	blk := store.GetBlock(orgID, "block-1")
	if blk == nil {
		t.Fatal("canonical block row disappeared for a referenced block")
	}
	if blk.GCState != db.BlockGCStateDeleting {
		t.Errorf("released a fresh claim held by another candidate's attempt (gc_state=%q); that attempt could delete the bytes with the fence already down", blk.GCState)
	}
	if got := store.AllBlockGCCandidates(); len(got) != 1 {
		t.Fatalf("candidate rows = %d, want 1: declining to release must not settle the item", len(got))
	}
}

// TestX2_UnavailableClusterDuringClaimDoesNotBurnRetries extends the fail-closed retry
// rule past the EACH_QUORUM verify.
//
// The property is simply: an availability failure at ClaimBlockDelete must not consume
// the item's permanent retry budget. ClaimBlockDelete runs BEFORE the verify, so
// protecting only the verify would leave the loss reachable through the statement
// immediately preceding the one the protection covers — and block items never leave
// the DLQ.
//
// Note what this does NOT claim. An LWT is more exposed than a plain read, but no
// simple "a remote DC is down, therefore the claim fails" rule holds: SERIAL takes a
// global quorum over all replicas, not one per datacenter. The trigger here is an
// injected availability error, not a modelled datacenter outage.
func TestX2_UnavailableClusterDuringClaimDoesNotBurnRetries(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)

	orgID := uuid.New()
	blockID := testSHA256BlockID("x2-unavailable-claim")
	store.AddBlock(orgID, blockID, "hot", 0)
	queuedAt := time.Now().Add(-2 * time.Hour)
	store.AddBlockGCCandidate(orgID, blockID, "hot", queuedAt)
	store.EnqueueItem(orgID, queuedAt, ItemBlock, blockID, uuid.Nil, "hot", 0)

	store.SetClaimBlockDeleteErrForTest(gocql.ErrTimeoutNoResponse)

	// More passes than the five-retry budget allows.
	for i := 0; i < 8; i++ {
		if _, err := w.ProcessOnce(context.Background()); err != nil {
			t.Fatalf("ProcessOnce %d returned a fatal error: %v", i, err)
		}
	}

	if failed, err := store.GetTotalFailedItems(); err != nil {
		t.Fatalf("GetTotalFailedItems: %v", err)
	} else if failed != 0 {
		t.Fatalf("%d block item(s) reached the DLQ because the cluster was briefly unavailable; block items do not auto-recover from there and the scanner has moved past their candidates, so that storage becomes permanently uncollectable", failed)
	}
	if got := store.AllBlockGCCandidates(); len(got) != 1 {
		t.Errorf("candidate rows = %d, want 1: the work must survive the outage", len(got))
	}
	if deletes := sp.ScopedBlockDeletes(); len(deletes) != 0 {
		t.Errorf("deleted bytes despite never completing the walk: %+v", deletes)
	}

	// Surviving is only half of it: once the cluster is back the item must still be
	// collectable, or "no loss" would just mean the work leaked in a tidier way.
	store.SetClaimBlockDeleteErrForTest(nil)
	if _, err := w.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce after recovery returned a fatal error: %v", err)
	}
	if stats.BlocksDeleted() != 1 {
		t.Errorf("BlocksDeleted = %d after the outage cleared, want 1", stats.BlocksDeleted())
	}
}

// TestX2_NonAvailabilityErrorStillReachesTheDLQ is the other half of that rule, and
// the reason it is scoped to availability failures rather than to "any error".
//
// Postponing forever is the right answer to an outage and the wrong answer to a bug:
// a malformed statement or an unknown column never resolves on its own, and an item
// that quietly retries for eternity is an item nobody is ever told about. Those must
// still spend their retries and land in the DLQ where a human sees them.
func TestX2_NonAvailabilityErrorStillReachesTheDLQ(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)

	orgID := uuid.New()
	blockID := testSHA256BlockID("x2-nonavailability-claim")
	store.AddBlock(orgID, blockID, "hot", 0)
	queuedAt := time.Now().Add(-2 * time.Hour)
	store.AddBlockGCCandidate(orgID, blockID, "hot", queuedAt)
	store.EnqueueItem(orgID, queuedAt, ItemBlock, blockID, uuid.Nil, "hot", 0)

	store.SetClaimBlockDeleteErrForTest(errors.New("undefined column name gc_claim_id"))

	for i := 0; i < 8; i++ {
		if _, err := w.ProcessOnce(context.Background()); err != nil {
			t.Fatalf("ProcessOnce %d returned a fatal error: %v", i, err)
		}
	}

	failed, err := store.GetTotalFailedItems()
	if err != nil {
		t.Fatalf("GetTotalFailedItems: %v", err)
	}
	if failed == 0 {
		t.Fatal("a permanent, item-specific error never reached the DLQ; it would retry silently forever with nobody notified")
	}
}

// TestX2_NonAvailabilityErrorDuringGlobalVerifyStillReachesTheDLQ closes the gap the
// test above leaves open.
//
// That one injects at ClaimBlockDelete, which routes through failClosedIfUnavailable
// and therefore consults the classifier. The global liveness verify does not go through
// that helper — it builds its own failedClosedError — so for a while EVERY error there
// was treated as environmental, including ones that never resolve on their own. The
// most plausible instance is not a typo in the CQL: it is a ReadFailure from a
// tombstone-heavy block_references partition, which is specific to one block and
// permanent until someone looks at it.
//
// That combination was invisible. The item postpones without spending a retry, so it
// never reaches the DLQ; and the blocked/liveness pair is per PATH, so any other
// block's successful verify moves the recovery half forward and clears the alert while
// this one stays stuck forever.
//
// Failing closed is not what changes here: an error still aborts the delete either way.
// What the classifier decides is the QUEUE policy afterwards.
func TestX2_NonAvailabilityErrorDuringGlobalVerifyStillReachesTheDLQ(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)

	orgID := uuid.New()
	blockID := testSHA256BlockID("x2-nonavailability-global-verify")
	store.AddBlock(orgID, blockID, "hot", 0)
	queuedAt := time.Now().Add(-2 * time.Hour)
	store.AddBlockGCCandidate(orgID, blockID, "hot", queuedAt)
	store.EnqueueItem(orgID, queuedAt, ItemBlock, blockID, uuid.Nil, "hot", 0)

	// The FRAME, not a plain error carrying its text. A plain error can never be
	// classified as environmental whatever the classifier does, so injecting one would
	// pass this test on a classifier that swallows ReadFailure — the exact regression
	// it exists to catch, and the case the fix was written for.
	store.SetBlockHasReferencesGlobalErrForTest(fakeRequestError{
		code: gocql.ErrCodeReadFailure,
		msg:  "Operation failed - received 0 responses and 1 failures: TOMBSTONE_OVERWHELMING",
	})

	for i := 0; i < 8; i++ {
		if _, err := w.ProcessOnce(context.Background()); err != nil {
			t.Fatalf("ProcessOnce %d returned a fatal error: %v", i, err)
		}
	}

	if deletes := sp.ScopedBlockDeletes(); len(deletes) != 0 {
		t.Fatalf("destroyed bytes after a failed liveness verify: %+v", deletes)
	}
	failed, err := store.GetTotalFailedItems()
	if err != nil {
		t.Fatalf("GetTotalFailedItems: %v", err)
	}
	if failed == 0 {
		t.Fatal("a permanent, item-specific error at the global verify never reached the DLQ; it postpones forever, and because the blocked/liveness pair is per-path another block's success clears the alert while this item stays stuck with nobody notified")
	}
}

// TestX2_TopologyGateRejectionIsNeverCached pins the asymmetry in the gate's cache.
//
// A passing result may be reused briefly — schema metadata does not change between two
// blocks of the same batch, and re-reading it per candidate is pure overhead. A
// REJECTION may never be reused, because caching one would keep deletes blocked after
// the topology was repaired. The cache is allowed to cost latency, never availability.
func TestX2_TopologyGateRejectionIsNeverCached(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)

	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	w.clock = func() time.Time { return now }

	gateCalls := 0
	gateErr := errors.New("live replication map no longer matches the declared topology")
	w.SetDestructiveTopologyGate(func() error {
		gateCalls++
		return gateErr
	})

	for i := 0; i < 3; i++ {
		if err := w.checkDestructiveTopology(destructivePathBlock); err == nil {
			t.Fatalf("call %d: gate passed while rejecting", i)
		}
	}
	if gateCalls != 3 {
		t.Fatalf("gate consulted %d times while rejecting, want 3: a cached rejection would keep deletes blocked after the topology was repaired", gateCalls)
	}

	// Repaired: the very next call must see it, with no TTL to wait out.
	gateErr = nil
	if err := w.checkDestructiveTopology(destructivePathBlock); err != nil {
		t.Fatalf("gate still rejecting after the topology was repaired: %v", err)
	}

	// Now that it passes, it may be reused within the TTL.
	callsAfterPass := gateCalls
	if err := w.checkDestructiveTopology(destructivePathBlock); err != nil {
		t.Fatalf("unexpected rejection: %v", err)
	}
	if gateCalls != callsAfterPass {
		t.Errorf("gate re-read schema metadata %d extra time(s) within the TTL; a passing result is meant to be reused across a batch", gateCalls-callsAfterPass)
	}

	// Past the TTL it must be re-established rather than trusted indefinitely: the
	// keyspace can be altered while the process runs, which is why this is a runtime
	// check and not a startup one.
	now = now.Add(destructiveTopologyGateTTL + time.Second)
	if err := w.checkDestructiveTopology(destructivePathBlock); err != nil {
		t.Fatalf("unexpected rejection after the TTL: %v", err)
	}
	if gateCalls != callsAfterPass+1 {
		t.Errorf("gate consulted %d times after the TTL expired, want %d: a cached pass must not outlive its TTL", gateCalls, callsAfterPass+1)
	}
}

// destructivePairForTest reads the two series that together say whether a destructive
// path can still delete. Returned as a pair because neither means anything alone: the
// alert compares them, so an assertion on one in isolation asserts nothing.
func destructivePairForTest(t *testing.T, path string) (blocked, livenessSuccess float64) {
	t.Helper()
	return testutil.ToFloat64(metrics.GCDestructiveLastBlockedTimestamp.WithLabelValues(path)),
		testutil.ToFloat64(metrics.GCDestructiveLastLivenessSuccessTimestamp.WithLabelValues(path))
}

// resetDestructivePairForTest returns paths to the state metrics.Register seeds them
// in: never blocked, never succeeded. Tests share a process-wide registry, so without
// this each one inherits whatever the previous left behind.
func resetDestructivePairForTest(paths ...string) {
	for _, path := range paths {
		metrics.GCDestructiveLastBlockedTimestamp.WithLabelValues(path).Set(0)
		metrics.GCDestructiveLastLivenessSuccessTimestamp.WithLabelValues(path).Set(0)
	}
}

// advancingClock returns a clock that moves forward one millisecond per call.
//
// The frozen `func() time.Time { return now }` idiom used elsewhere in this package is
// actively wrong for anything comparing two recorded timestamps: it stamps every event
// in a walk with the same instant, so `blocked > liveness_success` is false however the
// code behaves, and the assertion passes or fails for reasons unrelated to it. Any test
// touching gc_destructive_last_*_timestamp_seconds must drive the worker with this.
func advancingClock(start time.Time) func() time.Time {
	var mu sync.Mutex
	current := start
	return func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		current = current.Add(time.Millisecond)
		return current
	}
}

// TestX2_BlockedStateSurvivesAPassThatAttemptsNothing is the regression test for the
// defect that replaced a boolean gauge with this pair.
//
// The old gauge was cleared at the end of any ProcessOnce that refused nothing, on the
// reasoning that a clean pass proves the environment recovered. It does not, and the
// counter-example is not exotic — it is the NORMAL shape of an ongoing outage. Failing
// closed postpones the item, RequeueItem stamps queued_at=now, and DequeueBatch will
// not hand it back until it has aged past the grace period. A datacenter that stays
// down therefore produces one refusing pass, then a run of passes that attempt nothing
// at all, then another refusal. Every pass in that run cleared the gauge and restarted
// the `for: 1h` window the runbook depends on, so an outage that never ended never
// alerted — the gauge neutralised precisely the alert it existed to raise.
//
// A pass that attempts nothing must leave the signal exactly as it found it.
func TestX2_BlockedStateSurvivesAPassThatAttemptsNothing(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	// A real grace period is the whole point: without one the postponed candidate is
	// eligible again on the very next pass, the walk runs again, and the gap this test
	// exists for never opens.
	w := NewWorker(store, sp, q, 100, time.Hour, false, stats)
	w.clock = advancingClock(time.Now())

	resetDestructivePairForTest(destructivePathBlock)

	orgID := uuid.New()
	blockID := testSHA256BlockID("x2-blocked-state-idle-pass")
	store.AddBlock(orgID, blockID, "hot", 0)
	queuedAt := time.Now().Add(-2 * time.Hour)
	store.AddBlockGCCandidate(orgID, blockID, "hot", queuedAt)
	store.EnqueueItem(orgID, queuedAt, ItemBlock, blockID, uuid.Nil, "hot", 0)

	store.SetBlockHasReferencesGlobalErrForTest(fakeRequestError{code: gocql.ErrCodeUnavailable, msg: "Cannot achieve consistency level EACH_QUORUM in DC dc-asia"})
	if _, err := w.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce returned a fatal error: %v", err)
	}
	blocked, livenessSuccess := destructivePairForTest(t, destructivePathBlock)
	if blocked <= livenessSuccess {
		t.Fatalf("last_blocked=%v last_liveness_success=%v after a fail-closed pass: with no error, no retry and no DLQ entry, this pair is the only thing that says GC cannot delete", blocked, livenessSuccess)
	}
	// Also the first-refusal-after-boot case: nothing had ever succeeded, so the success
	// half is still the seeded 0. That must read as blocked rather than drop out of the
	// comparison, which is why Register seeds both halves instead of stamping a startup
	// success nobody observed.
	if livenessSuccess != 0 {
		t.Fatalf("last_liveness_success=%v, want 0: nothing has ever succeeded in this test", livenessSuccess)
	}

	_, globalBefore := store.BlockHasReferencesCallCountsForTest()

	// The datacenter is still down. The candidate was requeued with queued_at=now, so it
	// is inside the grace period and this pass attempts nothing whatsoever.
	if _, err := w.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("second ProcessOnce returned a fatal error: %v", err)
	}
	_, globalAfter := store.BlockHasReferencesCallCountsForTest()
	if globalAfter != globalBefore {
		t.Fatalf("the second pass issued %d global liveness read(s); this test only means something if it issues none, so the grace period is not holding the candidate back", globalAfter-globalBefore)
	}

	blockedAfter, successAfter := destructivePairForTest(t, destructivePathBlock)
	if blockedAfter <= successAfter {
		t.Errorf("last_blocked=%v last_liveness_success=%v after a pass that attempted nothing: silence was read as recovery, and a run of such passes is exactly what an ongoing outage produces between refusals", blockedAfter, successAfter)
	}
	if blockedAfter != blocked {
		t.Errorf("last_blocked moved from %v to %v without a new refusal", blocked, blockedAfter)
	}
}

// TestX2_DestructiveTimestampsTrackTheLastEvidence pins the signal end to end: a
// refusal raises it, a later successful global read clears it, and a pass with nothing
// to do moves neither half.
//
// Failing closed is deliberately quiet — it postpones without erroring, without
// touching the retry budget and without reaching the DLQ — so a permanently rejecting
// environment is indistinguishable from a healthy idle fleet by counters alone. The
// pair has to come back down too: a signal that latches after a recovered outage is one
// operators learn to ignore, which is worse than not having it.
func TestX2_DestructiveTimestampsTrackTheLastEvidence(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)
	// Started slightly in the past on purpose. postponeItem stamps queued_at from
	// w.clock() while the queue's eligibility cutoff comes from the real time.Now(), so
	// a clock seeded at time.Now() drifts ahead of it and the requeued item never
	// becomes eligible again — this test needs the second pass to actually walk it.
	w.clock = advancingClock(time.Now().Add(-time.Minute))

	resetDestructivePairForTest(destructivePathBlock)

	// A path that has never been exercised must not read as blocked.
	if blocked, livenessSuccess := destructivePairForTest(t, destructivePathBlock); blocked > livenessSuccess {
		t.Fatalf("last_blocked=%v last_liveness_success=%v before anything happened: a never-exercised path must read as not blocked", blocked, livenessSuccess)
	}

	orgID := uuid.New()
	blockID := testSHA256BlockID("x2-destructive-timestamps-last-evidence")
	store.AddBlock(orgID, blockID, "hot", 0)
	queuedAt := time.Now().Add(-2 * time.Hour)
	store.AddBlockGCCandidate(orgID, blockID, "hot", queuedAt)
	store.EnqueueItem(orgID, queuedAt, ItemBlock, blockID, uuid.Nil, "hot", 0)

	store.SetBlockHasReferencesGlobalErrForTest(fakeRequestError{code: gocql.ErrCodeUnavailable, msg: "Cannot achieve consistency level EACH_QUORUM in DC dc-asia"})
	if _, err := w.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce returned a fatal error: %v", err)
	}
	if blocked, livenessSuccess := destructivePairForTest(t, destructivePathBlock); blocked <= livenessSuccess {
		t.Fatalf("last_blocked=%v last_liveness_success=%v after a fail-closed pass, want blocked later", blocked, livenessSuccess)
	}

	// The datacenter comes back and a candidate is walked again. The read itself is what
	// proves recovery, so this is where the signal clears.
	store.SetBlockHasReferencesGlobalErrForTest(nil)
	if _, err := w.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce after recovery returned a fatal error: %v", err)
	}
	blocked, livenessSuccess := destructivePairForTest(t, destructivePathBlock)
	if livenessSuccess <= blocked {
		t.Fatalf("last_blocked=%v last_liveness_success=%v after a successful global read, want success later: a signal that never comes back down is one operators learn to ignore", blocked, livenessSuccess)
	}

	// An idle pass is not evidence of anything and must move neither half.
	if _, err := w.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("idle ProcessOnce returned a fatal error: %v", err)
	}
	if gotBlocked, gotSuccess := destructivePairForTest(t, destructivePathBlock); gotBlocked != blocked || gotSuccess != livenessSuccess {
		t.Errorf("idle pass moved the pair from (%v, %v) to (%v, %v); silence is not an observation", blocked, livenessSuccess, gotBlocked, gotSuccess)
	}
}

// TestX2_LivenessSuccessOnAStillReferencedBlockIsEvidence pins the one call-site detail
// that decides whether the recovery half can latch.
//
// The global read is recorded as evidence when it RETURNS, before its result is
// examined. A block that turns out to be still referenced produces no delete, but the
// read that established that proves the environment can authorize one. Gating the
// record on a completed delete instead would leave any fleet whose candidates all turn
// out to be live reading as permanently blocked — the exact latch this design exists to
// avoid, reintroduced through the back door.
func TestX2_LivenessSuccessOnAStillReferencedBlockIsEvidence(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)
	w.clock = advancingClock(time.Now())

	resetDestructivePairForTest(destructivePathBlock)
	// A previous outage left the path reading as blocked.
	w.recordDestructiveBlocked(destructivePathBlock)

	orgID := uuid.New()
	store.AddBlock(orgID, "block-1", "hot", 0)
	queuedAt := time.Now().Add(-2 * time.Hour)
	store.AddBlockGCCandidate(orgID, "block-1", "hot", queuedAt)
	store.EnqueueItem(orgID, queuedAt, ItemBlock, "block-1", uuid.Nil, "hot", 0)

	// A reference appears between the cheap pre-check and the authorizing read, so the
	// walk reaches the global read and that read reports the block alive. Seeding a
	// reference up front instead would make the pre-check skip the candidate before any
	// global read happened, and the test would assert nothing.
	livenessCalls := 0
	store.SetBlockHasReferencesHookForTest(func(_ uuid.UUID, _ string, _ bool) (bool, error) {
		livenessCalls++
		return livenessCalls > 1, nil
	})

	if _, err := w.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce returned a fatal error: %v", err)
	}

	if stats.BlocksDeleted() != 0 {
		t.Fatalf("BlocksDeleted = %d, want 0: this test is only meaningful if nothing was deleted", stats.BlocksDeleted())
	}
	if blocked, livenessSuccess := destructivePairForTest(t, destructivePathBlock); livenessSuccess <= blocked {
		t.Errorf("last_blocked=%v last_liveness_success=%v after a successful read that found the block alive: a fleet of live blocks would stay latched as blocked forever", blocked, livenessSuccess)
	}
}

// TestX2_CommitPointRefusalOutranksTheLivenessSuccessInTheSameWalk pins the timestamp
// resolution, which is load-bearing rather than cosmetic.
//
// processBlock records a liveness success and can then record a topology refusal a few
// statements later, inside the same walk and milliseconds apart. The alert compares the
// two by value, so at whole-second resolution they tie, `blocked > liveness_success` is
// false, and the alert misses the failure mode where the global read works but the
// commit-point gate refuses. That mode is not rare: a gate rejecting systematically ties
// on every single walk, so the alert would never fire at all.
func TestX2_CommitPointRefusalOutranksTheLivenessSuccessInTheSameWalk(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)
	w.clock = advancingClock(time.Now())

	resetDestructivePairForTest(destructivePathBlock)

	orgID := uuid.New()
	blockID := testSHA256BlockID("x2-commit-point-refusal-same-walk")
	store.AddBlock(orgID, blockID, "hot", 0)
	queuedAt := time.Now().Add(-2 * time.Hour)
	store.AddBlockGCCandidate(orgID, blockID, "hot", queuedAt)
	store.EnqueueItem(orgID, queuedAt, ItemBlock, blockID, uuid.Nil, "hot", 0)

	// Passes at the top of the walk, rejects from the commit-point re-check onward: an
	// ALTER landing after the authorizing read.
	gateCalls := 0
	w.SetDestructiveTopologyGate(func() error {
		gateCalls++
		if gateCalls == 1 {
			return nil
		}
		return errors.New("live replication map no longer matches the declared topology")
	})

	if _, err := w.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce returned a fatal error: %v", err)
	}
	if gateCalls < 2 {
		t.Fatalf("gate consulted %d time(s); this test needs the commit-point re-check to run", gateCalls)
	}
	if deletes := sp.ScopedBlockDeletes(); len(deletes) != 0 {
		t.Fatalf("destroyed bytes under a rejected topology: %+v", deletes)
	}

	blocked, livenessSuccess := destructivePairForTest(t, destructivePathBlock)
	if livenessSuccess == 0 {
		t.Fatalf("last_liveness_success=0: the walk was supposed to complete its global read before the gate refused")
	}
	if blocked == livenessSuccess {
		t.Fatalf("last_blocked and last_liveness_success are both %v: the two events tied, so the alert cannot see the refusal. Either the timestamps lost sub-second resolution or the test clock is frozen", blocked)
	}
	if blocked < livenessSuccess {
		t.Errorf("last_blocked=%v last_liveness_success=%v: the refusal happened AFTER the read and must outrank it", blocked, livenessSuccess)
	}
}

// fakeRequestError implements gocql.RequestError, the shape Cassandra's own error
// frames arrive in.
type fakeRequestError struct {
	code int
	msg  string
}

func (e fakeRequestError) Code() int       { return e.code }
func (e fakeRequestError) Message() string { return e.msg }
func (e fakeRequestError) Error() string   { return e.msg }

// TestX2_ClusterUnavailableClassifierCoversServerErrorFrames covers the branch that
// production actually takes.
//
// A real datacenter outage does not surface as a driver sentinel: the coordinator is
// reachable and answers with an error FRAME saying it could not meet the consistency
// level. If the classifier only recognised the sentinels, it would look correct in
// tests driven by gocql.ErrTimeoutNoResponse and do nothing at all in the incident it
// was written for — so the server-frame branch is pinned here by error code, wrapped,
// alongside the codes that must NOT be swallowed.
func TestX2_ClusterUnavailableClassifierCoversServerErrorFrames(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"unavailable frame", fakeRequestError{code: gocql.ErrCodeUnavailable, msg: "Cannot achieve consistency level EACH_QUORUM in DC dc-asia"}, true},
		{"overloaded frame", fakeRequestError{code: gocql.ErrCodeOverloaded, msg: "Coordinator overloaded"}, true},
		{"read timeout frame", fakeRequestError{code: gocql.ErrCodeReadTimeout, msg: "Operation timed out"}, true},
		{"write timeout frame", fakeRequestError{code: gocql.ErrCodeWriteTimeout, msg: "Operation timed out"}, true},
		{"wrapped unavailable frame", fmt.Errorf("failed to claim block record for deletion: %w", fakeRequestError{code: gocql.ErrCodeUnavailable, msg: "unavailable"}), true},
		{"driver sentinel", gocql.ErrTimeoutNoResponse, true},
		{"no connections", gocql.ErrNoConnections, true},

		// These say something about the request, not the cluster. Swallowing them
		// would postpone a real bug forever with nobody notified.
		//
		// ReadFailure is the load-bearing one, and the reason the destructive verify
		// consults this classifier at all. It is a coordinator reporting that replicas
		// FAILED the read — the tombstone-overwhelming case on a block_references
		// partition — not that they were unreachable. It looks adjacent to the timeout
		// codes above and would be waved in by anyone extending that switch from the
		// names alone, and the item it belongs to would then postpone forever: no retry
		// spent, no DLQ entry, and nothing to see, because the blocked/liveness pair is
		// per path and the next healthy block clears the alert. WriteFailure is its
		// counterpart and sits here for the same reason.
		{"read failure frame", fakeRequestError{code: gocql.ErrCodeReadFailure, msg: "Operation failed - received 0 responses and 1 failures: TOMBSTONE_OVERWHELMING"}, false},
		{"write failure frame", fakeRequestError{code: gocql.ErrCodeWriteFailure, msg: "Operation failed - received 0 responses and 1 failures"}, false},
		{"invalid query", fakeRequestError{code: gocql.ErrCodeInvalid, msg: "undefined column name gc_claim_id"}, false},
		{"syntax error", fakeRequestError{code: gocql.ErrCodeSyntax, msg: "line 1:0 no viable alternative"}, false},
		{"not found", gocql.ErrNotFound, false},
		{"plain error", errors.New("something else"), false},
		{"nil", nil, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isClusterUnavailableError(tc.err); got != tc.want {
				t.Errorf("isClusterUnavailableError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestX2_TopologyGateIsRecheckedAtTheCommitPoint narrows the gate's TOCTOU window.
//
// The gate runs early in processBlock, several statements before any byte is
// destroyed, and a keyspace can be ALTERed at any moment: passing it once does not
// mean the EACH_QUORUM argument still holds when the delete actually commits. The
// operational rule already forbids changing topology under enabled destructive GC, so
// this is defence in depth rather than a correctness argument — but it costs a cached
// lookup, and it shrinks the exposed window from the whole walk to two statements.
//
// It also pins the direction of the refusal: a late rejection must hand the claim
// back. The block is provably unreferenced at that point, so the fence protects
// nothing, while holding it under a systematic rejection would fence the content for
// as long as the topology stays wrong.
func TestX2_TopologyGateIsRecheckedAtTheCommitPoint(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)

	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	w.clock = func() time.Time { return now }

	orgID := uuid.New()
	blockID := testSHA256BlockID("x2-topology-commit-point")
	store.AddBlock(orgID, blockID, "hot", 0)
	queuedAt := now.Add(-2 * time.Hour)
	store.AddBlockGCCandidate(orgID, blockID, "hot", queuedAt)
	store.EnqueueItem(orgID, queuedAt, ItemBlock, blockID, uuid.Nil, "hot", 0)

	// The gate passes at the top of the walk and starts rejecting once the
	// authorizing read is done — an ALTER landing mid-walk.
	//
	// The clock is deliberately FROZEN. A commit-point check that shared the cheap
	// form's TTL cache would return the pass this same walk just stored, milliseconds
	// earlier, and this test would go green while asserting nothing. Holding the clock
	// still is what forces the check to be a real re-read.
	gateCalls := 0
	w.SetDestructiveTopologyGate(func() error {
		gateCalls++
		if gateCalls == 1 {
			return nil
		}
		return errors.New("live replication map no longer matches the declared topology")
	})

	if _, err := w.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce returned a fatal error: %v", err)
	}

	if gateCalls < 2 {
		t.Fatalf("gate consulted %d time(s); it must be re-checked at the commit point, not only at the top of the walk", gateCalls)
	}
	if deletes := sp.ScopedBlockDeletes(); len(deletes) != 0 {
		t.Errorf("destroyed bytes after the topology stopped supporting the EACH_QUORUM argument: %+v", deletes)
	}
	if store.GetBlock(orgID, blockID) == nil {
		t.Error("canonical block row removed under a rejected topology")
	}
	if blk := store.GetBlock(orgID, blockID); blk != nil && blk.GCState == db.BlockGCStateDeleting {
		t.Error("block left fenced after a late topology rejection; the fence protects nothing here and would persist for as long as the topology stays wrong")
	}
	if got := store.AllBlockGCCandidates(); len(got) != 1 {
		t.Errorf("candidate rows = %d, want 1: failing closed must not consume the work", len(got))
	}
}

// TestX2_DestructiveTimestampsArePerPath pins why the pair carries a path label.
//
// The two destructive paths fail independently: the worker drains gc_queue, the scanner
// sweeps gc_s3_orphans, and one can be refusing every delete while the other has nothing
// to do. Under a single shared series, a clean worker pass would speak for orphan
// recovery and silence a path that is still completely blocked — the exact condition the
// signal exists to surface.
func TestX2_DestructiveTimestampsArePerPath(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)

	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	w.clock = advancingClock(now)

	resetDestructivePairForTest(destructivePathBlock, destructivePathOrphan)

	// Orphan recovery cannot authorize anything: its liveness read fails.
	orgID := uuid.New()
	blockID := testSHA256BlockID("x2-orphan-path")
	seedS3Orphan(t, store, orgID, blockID, "hot", "", "", now.AddDate(0, 0, -1))
	store.SetBlockHasReferencesGlobalErrForTest(fakeRequestError{code: gocql.ErrCodeUnavailable, msg: "Cannot achieve consistency level EACH_QUORUM in DC dc-asia"})
	if _, err := w.RecoverS3Orphans(context.Background(), 100); err == nil {
		t.Fatal("expected the sweep to fail closed")
	}
	orphanBlocked, orphanSuccess := destructivePairForTest(t, destructivePathOrphan)
	if orphanBlocked <= orphanSuccess {
		t.Fatalf("orphan path: last_blocked=%v last_liveness_success=%v after a fail-closed sweep, want blocked later", orphanBlocked, orphanSuccess)
	}

	// Now a worker pass with no work at all. It must not speak for the orphan path.
	store.SetBlockHasReferencesGlobalErrForTest(nil)
	if _, err := w.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce returned a fatal error: %v", err)
	}
	if gotBlocked, gotSuccess := destructivePairForTest(t, destructivePathOrphan); gotBlocked != orphanBlocked || gotSuccess != orphanSuccess {
		t.Errorf("a worker pass moved the ORPHAN pair from (%v, %v) to (%v, %v); orphan recovery is still refusing every delete and nothing would say so", orphanBlocked, orphanSuccess, gotBlocked, gotSuccess)
	}

	// The orphan path clears its own signal once its own read succeeds. Note this now
	// requires the sweep to actually REACH that read: there is no end-of-sweep clear to
	// pass the assertion on an empty sweep.
	if _, err := w.RecoverS3Orphans(context.Background(), 100); err != nil {
		t.Fatalf("clean sweep returned an error: %v", err)
	}
	if blocked, livenessSuccess := destructivePairForTest(t, destructivePathOrphan); livenessSuccess <= blocked {
		t.Errorf("orphan path: last_blocked=%v last_liveness_success=%v after a sweep whose global read succeeded, want success later", blocked, livenessSuccess)
	}
}

// TestX2_OrphanRefusalDoesNotContaminateTheWorkerPass covers the interleaving the two
// paths actually produce in a running fleet.
//
// Orphan recovery runs from the scanner, on its own schedule, so it can refuse a delete
// WHILE a worker pass is in flight. An earlier design kept pass-scoped state to decide
// when to clear a shared gauge, and that state was reachable from both paths: an orphan
// refusal marked the worker's pass, and the worker reported itself blocked because a
// different path was. Recording per path removes the shared state entirely rather than
// guarding it, but the interleaving is pinned here so a future refactor cannot quietly
// reintroduce a cross-path write.
func TestX2_OrphanRefusalDoesNotContaminateTheWorkerPass(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)
	w.clock = advancingClock(time.Now())

	resetDestructivePairForTest(destructivePathBlock, destructivePathOrphan)

	orgID := uuid.New()
	blockID := testSHA256BlockID("x2-orphan-refusal-worker")
	store.AddBlock(orgID, blockID, "hot", 0)
	queuedAt := time.Now().Add(-2 * time.Hour)
	store.AddBlockGCCandidate(orgID, blockID, "hot", queuedAt)
	store.EnqueueItem(orgID, queuedAt, ItemBlock, blockID, uuid.Nil, "hot", 0)

	// Orphan recovery refuses a delete from the middle of the worker's pass — the
	// interleaving that actually happens when the scanner and the worker overlap.
	store.SetBlockHasReferencesHookForTest(func(_ uuid.UUID, _ string, current bool) (bool, error) {
		w.recordDestructiveBlocked(destructivePathOrphan)
		return current, nil
	})

	if _, err := w.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce returned a fatal error: %v", err)
	}

	if blocked, livenessSuccess := destructivePairForTest(t, destructivePathOrphan); blocked <= livenessSuccess {
		t.Errorf("orphan path: last_blocked=%v last_liveness_success=%v, want blocked later: its own refusal must still be recorded", blocked, livenessSuccess)
	}
	if blocked, livenessSuccess := destructivePairForTest(t, destructivePathBlock); livenessSuccess <= blocked {
		t.Errorf("block path: last_blocked=%v last_liveness_success=%v, want success later: an orphan-path refusal was written to the worker's series, so the worker reads as blocked because a different path is", blocked, livenessSuccess)
	}
	if stats.BlocksDeleted() != 1 {
		t.Errorf("BlocksDeleted = %d, want 1: the worker pass itself was never blocked", stats.BlocksDeleted())
	}
}

// TestX2_OrphanRecoveryClassifiesItsGlobalVerifyFailure pins the classifier on the
// second destructive path.
//
// processBlock's verify learned to tell an unreachable cluster from a poisoned
// partition; orphan recovery ran the same read and reported every failure as
// "liveness_verify_unavailable", moving the blocked mark with it. Unlike the worker
// there is no retry budget to misspend here — the sweep defers either way — so what
// the misreport costs is the diagnosis: a permanent ReadFailure from a tombstone-heavy
// block_references partition reads as a datacenter outage, and the blocked-vs-liveness
// pair, whose whole question is whether this path can still authorize deletes at all,
// says the environment failed when it did not.
//
// Both halves are asserted because only checking the counter would pass on a change
// that fixed the label and left the mark, which is the half an alert actually watches.
func TestX2_OrphanRecoveryClassifiesItsGlobalVerifyFailure(t *testing.T) {
	seedRefusedOrphan := func(t *testing.T, w *Worker, store *MockStore, sp *MockStorageProvider, at time.Time, livenessErr error) {
		t.Helper()
		orgID := uuid.New()
		seedS3Orphan(t, store, orgID, "orph-1", "hot", "", "", at.AddDate(0, 0, -1))
		store.SetBlockHasReferencesGlobalErrForTest(livenessErr)
		if _, err := w.RecoverS3Orphans(context.Background(), 100); err == nil {
			t.Fatal("the sweep must defer when its global verify fails, whatever the error was")
		}
		if deletes := sp.ScopedBlockDeletes(); len(deletes) != 0 {
			t.Fatalf("destroyed bytes after a failed liveness verify: %+v", deletes)
		}
		if store.S3OrphanCount() != 1 {
			t.Fatalf("orphan row = %d, want 1: a failed verify must leave the row for the next sweep", store.S3OrphanCount())
		}
	}

	t.Run("a real outage is reported as one and moves the blocked mark", func(t *testing.T) {
		store := NewMockStore()
		sp := &MockStorageProvider{}
		w := NewWorker(store, sp, NewQueue(store), 100, 0, false, &Stats{})
		now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
		w.clock = advancingClock(now)
		resetDestructivePairForTest(destructivePathOrphan)

		beforeUnavailable := testutil.ToFloat64(metrics.GCErrorsTotal.WithLabelValues("liveness_verify_unavailable"))
		beforeFailed := testutil.ToFloat64(metrics.GCErrorsTotal.WithLabelValues("liveness_verify_failed"))

		seedRefusedOrphan(t, w, store, sp, now, fakeRequestError{
			code: gocql.ErrCodeUnavailable,
			msg:  "Cannot achieve consistency level EACH_QUORUM in DC dc-asia",
		})

		if got := testutil.ToFloat64(metrics.GCErrorsTotal.WithLabelValues("liveness_verify_unavailable")); got != beforeUnavailable+1 {
			t.Errorf("liveness_verify_unavailable = %v, want %v: a DC that cannot be reached is exactly what this label is for", got, beforeUnavailable+1)
		}
		if got := testutil.ToFloat64(metrics.GCErrorsTotal.WithLabelValues("liveness_verify_failed")); got != beforeFailed {
			t.Errorf("liveness_verify_failed moved on an availability failure: %v -> %v", beforeFailed, got)
		}
		if blocked, livenessSuccess := destructivePairForTest(t, destructivePathOrphan); blocked <= livenessSuccess {
			t.Errorf("last_blocked=%v last_liveness_success=%v, want blocked later: an unreachable DC is the environment refusing to authorize deletes, which is what the pair reports", blocked, livenessSuccess)
		}
	})

	t.Run("a poisoned partition is not reported as an outage", func(t *testing.T) {
		store := NewMockStore()
		sp := &MockStorageProvider{}
		w := NewWorker(store, sp, NewQueue(store), 100, 0, false, &Stats{})
		now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
		w.clock = advancingClock(now)
		resetDestructivePairForTest(destructivePathOrphan)
		// A liveness success first, so "blocked is not later than success" below is a
		// statement about this sweep rather than about two zero-valued gauges.
		w.recordDestructiveLivenessSuccess(destructivePathOrphan)
		_, baselineSuccess := destructivePairForTest(t, destructivePathOrphan)

		beforeUnavailable := testutil.ToFloat64(metrics.GCErrorsTotal.WithLabelValues("liveness_verify_unavailable"))
		beforeFailed := testutil.ToFloat64(metrics.GCErrorsTotal.WithLabelValues("liveness_verify_failed"))

		seedRefusedOrphan(t, w, store, sp, now, fakeRequestError{
			code: gocql.ErrCodeReadFailure,
			msg:  "Operation failed - received 0 responses and 1 failures: TOMBSTONE_OVERWHELMING",
		})

		if got := testutil.ToFloat64(metrics.GCErrorsTotal.WithLabelValues("liveness_verify_failed")); got != beforeFailed+1 {
			t.Errorf("liveness_verify_failed = %v, want %v: a ReadFailure is specific to this partition and permanent until someone looks at it", got, beforeFailed+1)
		}
		if got := testutil.ToFloat64(metrics.GCErrorsTotal.WithLabelValues("liveness_verify_unavailable")); got != beforeUnavailable {
			t.Errorf("liveness_verify_unavailable moved on a non-availability failure: %v -> %v; this pages whoever is on call to inspect datacenter health for a condition that survives every DC being up", beforeUnavailable, got)
		}
		if blocked, livenessSuccess := destructivePairForTest(t, destructivePathOrphan); blocked > livenessSuccess {
			t.Errorf("last_blocked=%v last_liveness_success=%v (baseline success %v), want the mark unmoved: one poisoned partition does not answer whether this path can authorize deletes, which is the pair's only question", blocked, livenessSuccess, baselineSuccess)
		}
	})
}

// TestX2_OrphanRecoveryCanonicalReadUnavailableMovesBlockedMark covers the new
// EACH_QUORUM canonical read that runs before the global liveness verify. An outage
// must still be visible in the orphan path even though that verify is never reached.
func TestX2_OrphanRecoveryCanonicalReadUnavailableMovesBlockedMark(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	w := NewWorker(store, sp, NewQueue(store), 100, 0, false, &Stats{})
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	w.clock = advancingClock(now)
	resetDestructivePairForTest(destructivePathOrphan)

	orgID := uuid.New()
	seedS3Orphan(t, store, orgID, "orph-canonical-unavailable", "hot", "", "previous failure", now.AddDate(0, 0, -1))
	store.SetGetS3OrphanGlobalErrForTest(fakeRequestError{
		code: gocql.ErrCodeUnavailable,
		msg:  "Cannot achieve consistency level EACH_QUORUM in DC dc-asia",
	})

	beforeUnavailable := testutil.ToFloat64(metrics.GCErrorsTotal.WithLabelValues("s3_orphan_canonical_read_unavailable"))
	beforeFailed := testutil.ToFloat64(metrics.GCErrorsTotal.WithLabelValues("s3_orphan_canonical_read_failed"))
	if _, err := w.RecoverS3Orphans(context.Background(), 100); err == nil {
		t.Fatal("expected the sweep to fail closed")
	}
	if deletes := sp.BlockStoreRequests(); len(deletes) != 0 {
		t.Fatalf("resolved storage after an unavailable canonical read: %+v", deletes)
	}
	if got := testutil.ToFloat64(metrics.GCErrorsTotal.WithLabelValues("s3_orphan_canonical_read_unavailable")); got != beforeUnavailable+1 {
		t.Errorf("canonical read unavailable = %v, want %v", got, beforeUnavailable+1)
	}
	if got := testutil.ToFloat64(metrics.GCErrorsTotal.WithLabelValues("s3_orphan_canonical_read_failed")); got != beforeFailed {
		t.Errorf("canonical read failed moved on an availability error: %v -> %v", beforeFailed, got)
	}
	if blocked, livenessSuccess := destructivePairForTest(t, destructivePathOrphan); blocked <= livenessSuccess {
		t.Errorf("last_blocked=%v last_liveness_success=%v, want blocked later", blocked, livenessSuccess)
	}
}

// TestX2_OrphanRecoveryCanonicalReadPermanentErrorIsNotAnOutage is the other half
// of the read-side classification, and the reason it exists is asymmetry: the
// reload side already pins both directions
// (…ReloadUnavailableMovesBlockedMark and …ReloadPermanentErrorIsNotAnOutage)
// while the initial read only pinned the outage direction. Without this, nothing
// stops the cheaper "any canonical read failure moves the blocked mark" edit,
// which pages whoever is on call for a datacenter outage over a condition that
// survives every DC being up.
//
// Unlike its outage twin, this is not a regression test for the classification
// itself — an unclassified read failure moves no mark either. It is a guard on
// the direction a future change is most likely to break.
func TestX2_OrphanRecoveryCanonicalReadPermanentErrorIsNotAnOutage(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	w := NewWorker(store, sp, NewQueue(store), 100, 0, false, &Stats{})
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	w.clock = advancingClock(now)
	resetDestructivePairForTest(destructivePathOrphan)
	// A liveness success first, so "blocked is not later than success" below is a
	// statement about this sweep rather than about two zero-valued gauges.
	w.recordDestructiveLivenessSuccess(destructivePathOrphan)
	_, baselineSuccess := destructivePairForTest(t, destructivePathOrphan)

	orgID := uuid.New()
	seedS3Orphan(t, store, orgID, "orph-canonical-read-permanent", "hot", "", "previous failure", now.AddDate(0, 0, -1))
	store.SetGetS3OrphanGlobalErrForTest(fakeRequestError{
		code: gocql.ErrCodeReadFailure,
		msg:  "Operation failed - received 0 responses and 1 failures: TOMBSTONE_OVERWHELMING",
	})

	beforeFailed := testutil.ToFloat64(metrics.GCErrorsTotal.WithLabelValues("s3_orphan_canonical_read_failed"))
	beforeUnavailable := testutil.ToFloat64(metrics.GCErrorsTotal.WithLabelValues("s3_orphan_canonical_read_unavailable"))
	if _, err := w.RecoverS3Orphans(context.Background(), 100); err == nil {
		t.Fatal("expected the sweep to fail closed")
	}
	if deletes := sp.BlockStoreRequests(); len(deletes) != 0 {
		t.Fatalf("resolved storage after a permanent canonical read failure: %+v", deletes)
	}
	if got := testutil.ToFloat64(metrics.GCErrorsTotal.WithLabelValues("s3_orphan_canonical_read_failed")); got != beforeFailed+1 {
		t.Errorf("canonical read failed = %v, want %v: a ReadFailure is specific to this partition and permanent until someone looks at it", got, beforeFailed+1)
	}
	if got := testutil.ToFloat64(metrics.GCErrorsTotal.WithLabelValues("s3_orphan_canonical_read_unavailable")); got != beforeUnavailable {
		t.Errorf("canonical read unavailable moved on a non-availability failure: %v -> %v", beforeUnavailable, got)
	}
	if blocked, livenessSuccess := destructivePairForTest(t, destructivePathOrphan); blocked > livenessSuccess {
		t.Errorf("last_blocked=%v last_liveness_success=%v (baseline success %v), want the mark unmoved: one poisoned partition does not answer whether this path can authorize deletes", blocked, livenessSuccess, baselineSuccess)
	}
}

// TestX2_OrphanRecoveryCanonicalReloadUnavailableMovesBlockedMark covers the
// commit-point reload. Its failure is not a state change, and must not be reported
// as one or leave the orphan path's availability signal silent.
func TestX2_OrphanRecoveryCanonicalReloadUnavailableMovesBlockedMark(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	w := NewWorker(store, sp, NewQueue(store), 100, 0, false, &Stats{})
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	w.clock = advancingClock(now)
	resetDestructivePairForTest(destructivePathOrphan)

	orgID := uuid.New()
	seedS3Orphan(t, store, orgID, "orph-reload-unavailable", "hot", "", "previous failure", now.AddDate(0, 0, -1))
	store.SetGetS3OrphanGlobalHookForTest(func(_ uuid.UUID, _ string, call int, info S3OrphanInfo) (S3OrphanInfo, error) {
		if call == 2 {
			return S3OrphanInfo{}, fakeRequestError{
				code: gocql.ErrCodeUnavailable,
				msg:  "Cannot achieve consistency level EACH_QUORUM in DC dc-asia",
			}
		}
		return info, nil
	})

	beforeUnavailable := testutil.ToFloat64(metrics.GCErrorsTotal.WithLabelValues("s3_orphan_canonical_reload_unavailable"))
	beforeChanged := testutil.ToFloat64(metrics.GCErrorsTotal.WithLabelValues("s3_orphan_canonical_changed"))
	if _, err := w.RecoverS3Orphans(context.Background(), 100); err == nil {
		t.Fatal("expected the sweep to fail closed")
	}
	if deletes := sp.BlockStoreRequests(); len(deletes) != 0 {
		t.Fatalf("resolved storage after an unavailable canonical reload: %+v", deletes)
	}
	if calls := store.GetS3OrphanGlobalCallsForTest(); calls != 2 {
		t.Fatalf("canonical reads=%d, want initial read plus reload", calls)
	}
	if got := testutil.ToFloat64(metrics.GCErrorsTotal.WithLabelValues("s3_orphan_canonical_reload_unavailable")); got != beforeUnavailable+1 {
		t.Errorf("canonical reload unavailable = %v, want %v", got, beforeUnavailable+1)
	}
	if got := testutil.ToFloat64(metrics.GCErrorsTotal.WithLabelValues("s3_orphan_canonical_changed")); got != beforeChanged {
		t.Errorf("canonical changed moved on an availability error: %v -> %v", beforeChanged, got)
	}
	if blocked, livenessSuccess := destructivePairForTest(t, destructivePathOrphan); blocked <= livenessSuccess {
		t.Errorf("last_blocked=%v last_liveness_success=%v, want blocked later", blocked, livenessSuccess)
	}
}

// TestX2_OrphanRecoveryCanonicalReloadMissingIsDistinctFromInitialMissing keeps
// a mid-item disappearance separate from a discovery row that was stale before
// the sweep began. Both fail closed, but the reload case is the stronger signal
// that the canonical lifecycle changed while recovery was acting on it.
func TestX2_OrphanRecoveryCanonicalReloadMissingIsDistinctFromInitialMissing(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	w := NewWorker(store, sp, NewQueue(store), 100, 0, false, &Stats{})
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	w.clock = advancingClock(now)
	resetDestructivePairForTest(destructivePathOrphan)

	orgID := uuid.New()
	blockID := "orph-reload-missing"
	seedS3Orphan(t, store, orgID, blockID, "hot", "", "previous failure", now.AddDate(0, 0, -1))
	store.SetGetS3OrphanGlobalHookForTest(func(_ uuid.UUID, _ string, call int, info S3OrphanInfo) (S3OrphanInfo, error) {
		if call == 1 {
			// The first read has already returned a canonical row. Remove it before
			// the commit-point reload to model a lifecycle clear in the race window.
			store.DeleteS3OrphanCanonicalForTest(orgID, blockID)
		}
		return info, nil
	})

	beforeReloadMissing := testutil.ToFloat64(metrics.GCErrorsTotal.WithLabelValues("s3_orphan_canonical_reload_missing"))
	beforeInitialMissing := testutil.ToFloat64(metrics.GCErrorsTotal.WithLabelValues("s3_orphan_canonical_missing"))
	if _, err := w.RecoverS3Orphans(context.Background(), 100); err == nil {
		t.Fatal("expected the sweep to fail closed")
	}
	if deletes := sp.BlockStoreRequests(); len(deletes) != 0 {
		t.Fatalf("resolved storage after a missing canonical reload: %+v", deletes)
	}
	if calls := store.GetS3OrphanGlobalCallsForTest(); calls != 2 {
		t.Fatalf("canonical reads=%d, want initial read plus reload", calls)
	}
	if got := testutil.ToFloat64(metrics.GCErrorsTotal.WithLabelValues("s3_orphan_canonical_reload_missing")); got != beforeReloadMissing+1 {
		t.Errorf("canonical reload missing = %v, want %v", got, beforeReloadMissing+1)
	}
	if got := testutil.ToFloat64(metrics.GCErrorsTotal.WithLabelValues("s3_orphan_canonical_missing")); got != beforeInitialMissing {
		t.Errorf("initial canonical missing also moved for a reload disappearance: %v -> %v", beforeInitialMissing, got)
	}
}

// TestX2_OrphanRecoveryCanonicalReloadPermanentErrorIsNotAnOutage keeps a malformed
// or otherwise permanent reload failure out of the availability signal. The reload
// still refuses the destructive action, but it needs item-level diagnosis instead of
// paging for a datacenter outage.
func TestX2_OrphanRecoveryCanonicalReloadPermanentErrorIsNotAnOutage(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	w := NewWorker(store, sp, NewQueue(store), 100, 0, false, &Stats{})
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	w.clock = advancingClock(now)
	resetDestructivePairForTest(destructivePathOrphan)

	orgID := uuid.New()
	seedS3Orphan(t, store, orgID, "orph-reload-failed", "hot", "", "previous failure", now.AddDate(0, 0, -1))
	store.SetGetS3OrphanGlobalHookForTest(func(_ uuid.UUID, _ string, call int, info S3OrphanInfo) (S3OrphanInfo, error) {
		if call == 2 {
			return S3OrphanInfo{}, errors.New("canonical row has an incompatible recovery schema")
		}
		return info, nil
	})

	beforeFailed := testutil.ToFloat64(metrics.GCErrorsTotal.WithLabelValues("s3_orphan_canonical_reload_failed"))
	beforeUnavailable := testutil.ToFloat64(metrics.GCErrorsTotal.WithLabelValues("s3_orphan_canonical_reload_unavailable"))
	beforeChanged := testutil.ToFloat64(metrics.GCErrorsTotal.WithLabelValues("s3_orphan_canonical_changed"))
	if _, err := w.RecoverS3Orphans(context.Background(), 100); err == nil {
		t.Fatal("expected the sweep to fail closed")
	}
	if got := testutil.ToFloat64(metrics.GCErrorsTotal.WithLabelValues("s3_orphan_canonical_reload_failed")); got != beforeFailed+1 {
		t.Errorf("canonical reload failed = %v, want %v", got, beforeFailed+1)
	}
	if got := testutil.ToFloat64(metrics.GCErrorsTotal.WithLabelValues("s3_orphan_canonical_reload_unavailable")); got != beforeUnavailable {
		t.Errorf("canonical reload unavailable moved on a permanent error: %v -> %v", beforeUnavailable, got)
	}
	if got := testutil.ToFloat64(metrics.GCErrorsTotal.WithLabelValues("s3_orphan_canonical_changed")); got != beforeChanged {
		t.Errorf("canonical changed moved on a permanent reload error: %v -> %v", beforeChanged, got)
	}
	if blocked, livenessSuccess := destructivePairForTest(t, destructivePathOrphan); blocked > livenessSuccess {
		t.Errorf("last_blocked=%v last_liveness_success=%v, want blocked mark unchanged", blocked, livenessSuccess)
	}
}
