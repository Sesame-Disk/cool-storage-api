package gc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
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
	store.SetBlockHasReferencesGlobalErrForTest(errors.New("cannot achieve consistency level EACH_QUORUM"))

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
	store.SetBlockHasReferencesGlobalErrForTest(errors.New("cannot achieve consistency level EACH_QUORUM"))
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
// side: if some earlier attempt did leave a claim behind, the pre-check path that
// settles the item on a live reference must hand it back rather than clear the
// candidate and walk away from a block stuck in 'deleting'.
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

	// Attempt 1 claims and then loses the verify to an unreachable DC.
	store.SetBlockHasReferencesGlobalErrForTest(errors.New("cannot achieve consistency level EACH_QUORUM"))
	if _, err := w.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("first ProcessOnce returned a fatal error: %v", err)
	}

	// A writer republishes the content before the retry.
	store.AddBlockReferenceForTest(orgID, "block-1", "fs:lib:obj")
	store.SetBlockHasReferencesGlobalErrForTest(nil)

	// Attempt 2 settles through the pre-check, which must also clear any claim.
	if _, err := w.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("second ProcessOnce returned a fatal error: %v", err)
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

// TestX2_TopologyGateAlsoGuardsOrphanRecovery covers the transitive delete path.
// RecoverS3Orphans deletes bytes without reading references at all, so it needs the
// same gate or the guarantee has a hole that no processBlock test would catch.
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
