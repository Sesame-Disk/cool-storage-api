package gc

import (
	"context"
	"errors"
	"fmt"
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
	store.AddBlock(orgID, "block-1", "hot", 0)
	queuedAt := time.Now().Add(-2 * time.Hour)
	store.AddBlockGCCandidate(orgID, "block-1", "hot", queuedAt)
	store.EnqueueItem(orgID, queuedAt, ItemBlock, "block-1", uuid.Nil, "hot", 0)

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
	store.AddBlock(orgID, "block-1", "hot", 0)
	queuedAt := time.Now().Add(-2 * time.Hour)
	store.AddBlockGCCandidate(orgID, "block-1", "hot", queuedAt)
	store.EnqueueItem(orgID, queuedAt, ItemBlock, "block-1", uuid.Nil, "hot", 0)

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

// TestX2_DestructiveBlockedGaugeTracksThePass pins the one signal that makes a
// permanently rejecting environment visible at all.
//
// Failing closed is deliberately quiet: it postpones without erroring, without
// touching the retry budget, and without reaching the DLQ. That is correct for a
// transient outage and indistinguishable from a healthy idle fleet when the condition
// is permanent — the counters simply stop moving, exactly as they would if there were
// nothing to collect. Only a gauge can express "still blocked, right now", and only if
// it also goes back down: a gauge that latches at 1 after a recovered outage trains
// operators to ignore it, which is worse than not having it.
func TestX2_DestructiveBlockedGaugeTracksThePass(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)

	metrics.GCDestructiveDeletesBlocked.WithLabelValues(destructivePathBlock).Set(0)

	orgID := uuid.New()
	store.AddBlock(orgID, "block-1", "hot", 0)
	queuedAt := time.Now().Add(-2 * time.Hour)
	store.AddBlockGCCandidate(orgID, "block-1", "hot", queuedAt)
	store.EnqueueItem(orgID, queuedAt, ItemBlock, "block-1", uuid.Nil, "hot", 0)

	store.SetBlockHasReferencesGlobalErrForTest(errors.New("cannot achieve consistency level EACH_QUORUM"))
	if _, err := w.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce returned a fatal error: %v", err)
	}
	if got := testutil.ToFloat64(metrics.GCDestructiveDeletesBlocked.WithLabelValues(destructivePathBlock)); got != 1 {
		t.Fatalf("gc_destructive_deletes_blocked = %v after a fail-closed pass, want 1: with no error, no retry and no DLQ entry, this gauge is the only thing that says GC cannot delete", got)
	}

	// The datacenter comes back. A pass that refuses nothing must clear the gauge —
	// including the case where there is simply nothing left to collect, which is what
	// a recovered fleet usually looks like a few passes later.
	store.SetBlockHasReferencesGlobalErrForTest(nil)
	if _, err := w.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce after recovery returned a fatal error: %v", err)
	}
	if got := testutil.ToFloat64(metrics.GCDestructiveDeletesBlocked.WithLabelValues(destructivePathBlock)); got != 0 {
		t.Errorf("gc_destructive_deletes_blocked = %v after recovery, want 0: a gauge that never comes back down is one operators learn to ignore", got)
	}

	// An idle pass with no work at all must also read as "not blocked".
	if _, err := w.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("idle ProcessOnce returned a fatal error: %v", err)
	}
	if got := testutil.ToFloat64(metrics.GCDestructiveDeletesBlocked.WithLabelValues(destructivePathBlock)); got != 0 {
		t.Errorf("gc_destructive_deletes_blocked = %v on an idle pass, want 0", got)
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
	store.AddBlock(orgID, "block-1", "hot", 0)
	queuedAt := now.Add(-2 * time.Hour)
	store.AddBlockGCCandidate(orgID, "block-1", "hot", queuedAt)
	store.EnqueueItem(orgID, queuedAt, ItemBlock, "block-1", uuid.Nil, "hot", 0)

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
	if store.GetBlock(orgID, "block-1") == nil {
		t.Error("canonical block row removed under a rejected topology")
	}
	if blk := store.GetBlock(orgID, "block-1"); blk != nil && blk.GCState == db.BlockGCStateDeleting {
		t.Error("block left fenced after a late topology rejection; the fence protects nothing here and would persist for as long as the topology stays wrong")
	}
	if got := store.AllBlockGCCandidates(); len(got) != 1 {
		t.Errorf("candidate rows = %d, want 1: failing closed must not consume the work", len(got))
	}
}

// TestX2_DestructiveBlockedGaugeIsPerPath pins why the gauge carries a path label.
//
// The two destructive paths fail independently: the worker drains gc_queue, the
// scanner sweeps gc_s3_orphans, and one can be refusing every delete while the other
// has nothing to do. Under a single shared gauge, a clean worker pass would reset the
// alarm that orphan recovery had just raised — silencing a path that is still
// completely blocked, which is the exact condition the gauge exists to surface.
func TestX2_DestructiveBlockedGaugeIsPerPath(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)

	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	w.clock = func() time.Time { return now }

	metrics.GCDestructiveDeletesBlocked.WithLabelValues(destructivePathBlock).Set(0)
	metrics.GCDestructiveDeletesBlocked.WithLabelValues(destructivePathOrphan).Set(0)

	// Orphan recovery cannot authorize anything: its liveness read fails.
	orgID := uuid.New()
	if _, err := store.RecordS3Orphan(orgID, "orph-1", "hot", db.PlainBlockRepresentationID, "", "", now.AddDate(0, 0, -1)); err != nil {
		t.Fatalf("seed orphan: %v", err)
	}
	store.SetBlockHasReferencesGlobalErrForTest(errors.New("cannot achieve consistency level EACH_QUORUM"))
	if _, err := w.RecoverS3Orphans(context.Background(), 100); err == nil {
		t.Fatal("expected the sweep to fail closed")
	}
	if got := testutil.ToFloat64(metrics.GCDestructiveDeletesBlocked.WithLabelValues(destructivePathOrphan)); got != 1 {
		t.Fatalf("orphan path gauge = %v after a fail-closed sweep, want 1", got)
	}

	// Now a worker pass with no work at all. It must not speak for the orphan path.
	store.SetBlockHasReferencesGlobalErrForTest(nil)
	if _, err := w.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce returned a fatal error: %v", err)
	}
	if got := testutil.ToFloat64(metrics.GCDestructiveDeletesBlocked.WithLabelValues(destructivePathBlock)); got != 0 {
		t.Errorf("block path gauge = %v after a clean pass, want 0", got)
	}
	if got := testutil.ToFloat64(metrics.GCDestructiveDeletesBlocked.WithLabelValues(destructivePathOrphan)); got != 1 {
		t.Errorf("block path cleared the ORPHAN path's alarm (gauge = %v, want 1); orphan recovery is still refusing every delete and nothing would say so", got)
	}

	// The orphan path clears its own alarm once its own sweep refuses nothing.
	if _, err := w.RecoverS3Orphans(context.Background(), 100); err != nil {
		t.Fatalf("clean sweep returned an error: %v", err)
	}
	if got := testutil.ToFloat64(metrics.GCDestructiveDeletesBlocked.WithLabelValues(destructivePathOrphan)); got != 0 {
		t.Errorf("orphan path gauge = %v after a clean sweep, want 0", got)
	}
}

// TestX2_OrphanRefusalDoesNotContaminateTheWorkerPass covers the guard inside
// recordDestructiveBlocked, which mutation testing showed nothing else did.
//
// The worker's gauge is cleared at the end of a pass that refused nothing, and the
// decision rests on pass-scoped state. Orphan recovery runs from the scanner, on its
// own schedule, and can therefore refuse a delete WHILE a worker pass is in flight. If
// that refusal marked the worker's flag, the worker would report itself blocked
// because a different path was — a false positive on the series operators page from.
//
// The guard is one line and looks redundant right up until the two paths overlap in
// time, which is why it is pinned here rather than trusted.
func TestX2_OrphanRefusalDoesNotContaminateTheWorkerPass(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, stats)

	// The block gauge starts at 1 — a previous pass was blocked — so that CLEARING it
	// is the observable event. Starting from 0 would make the test pass even if the
	// clear never happened, which is exactly the hole mutation testing found in an
	// earlier version of it.
	metrics.GCDestructiveDeletesBlocked.WithLabelValues(destructivePathBlock).Set(1)
	metrics.GCDestructiveDeletesBlocked.WithLabelValues(destructivePathOrphan).Set(0)

	orgID := uuid.New()
	store.AddBlock(orgID, "block-1", "hot", 0)
	queuedAt := time.Now().Add(-2 * time.Hour)
	store.AddBlockGCCandidate(orgID, "block-1", "hot", queuedAt)
	store.EnqueueItem(orgID, queuedAt, ItemBlock, "block-1", uuid.Nil, "hot", 0)

	// Orphan recovery refuses a delete from the middle of the worker's pass — the
	// interleaving that actually happens when the scanner and the worker overlap.
	store.SetBlockHasReferencesHookForTest(func(_ uuid.UUID, _ string, current bool) (bool, error) {
		w.recordDestructiveBlocked(destructivePathOrphan)
		return current, nil
	})

	if _, err := w.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce returned a fatal error: %v", err)
	}

	if got := testutil.ToFloat64(metrics.GCDestructiveDeletesBlocked.WithLabelValues(destructivePathOrphan)); got != 1 {
		t.Errorf("orphan path gauge = %v, want 1: its own refusal must still be recorded", got)
	}
	if got := testutil.ToFloat64(metrics.GCDestructiveDeletesBlocked.WithLabelValues(destructivePathBlock)); got != 0 {
		t.Errorf("block path gauge = %v after a pass that refused nothing, want 0: an orphan-path refusal marked the worker's pass, so the worker stayed latched as blocked because a different path is", got)
	}
	if stats.BlocksDeleted() != 1 {
		t.Errorf("BlocksDeleted = %d, want 1: the worker pass itself was never blocked", stats.BlocksDeleted())
	}
}
