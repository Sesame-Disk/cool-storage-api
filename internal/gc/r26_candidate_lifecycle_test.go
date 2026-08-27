package gc

import (
	"context"
	"errors"
	"testing"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"

	"github.com/Sesame-Disk/sesamefs/internal/db"
)

// R26 is the candidate/projection/queue identity property: the exact physical
// incarnation P = (storage_class, storage_key) is part of the IDENTITY of every
// durable GC row, not a payload column riding along beside it.
//
// The tests here cover the half of R26 that exact-P made reachable for the first
// time — the LIFECYCLE of a candidate and its discovery row — as opposed to the
// destructive-authority half that P4a already closed. Two shapes matter:
//
//  1. A discovery row must never outlive the candidate it points at. Under the
//     old L-keyed projection there was no safe way to remove one, because the
//     row a stale lifecycle wanted to delete might already belong to a newer
//     incarnation. With P in the key, "delete P1's row" is expressible and can
//     never name P2's, so the shape becomes self-healing instead of permanent.
//
//  2. A candidate that moves its candidate_at leaves its old work item behind.
//     That item must settle harmlessly AND take its own discovery row with it.

// TestR26_SettlementClearsTheDiscoveryRowEvenWhenCanonicalIsAlreadyGone pins the
// self-heal after a partially-applied settlement.
//
// The failure it prevents: DeleteBlockGCCandidate deletes the canonical row and
// then the discovery row, and those are two statements. If the second one fails,
// the pair is left as canonical-absent + projection-present. The scanner
// enumerates the projection, rebuilds a queue item, the worker finds no canonical
// candidate for it and correctly does nothing — and the next scan produces the
// same item again. Nothing in the system removes that row, so the loop has no
// exit. Retrying the settlement has to converge.
func TestR26_SettlementClearsTheDiscoveryRowEvenWhenCanonicalIsAlreadyGone(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	blockID := testSHA256BlockID("blk-r26-partial-settle")
	store.AddBlock(orgID, blockID, "hot", 0)

	candidate, err := store.EnsureBlockGCCandidateExact(orgID, blockID, "hot", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("EnsureBlockGCCandidateExact: %v", err)
	}

	// First settlement attempt: canonical clears, the projection delete fails.
	store.SetDeleteBlockGCCandidateDiscoveryErr(errors.New("projection delete failed"))
	if err := store.DeleteBlockGCCandidate(orgID, blockID, candidate.Identity()); err == nil {
		t.Fatal("a failed discovery cleanup must be reported, not swallowed: the caller has to retry or the row is never removed")
	}
	store.SetDeleteBlockGCCandidateDiscoveryErr(nil)

	// Simulate the state that leaves behind: canonical gone, projection standing.
	store.DeleteBlockGCCandidateCanonicalForTest(orgID, blockID, candidate.Identity())
	if _, ok, _ := store.GetBlockGCCandidateExact(orgID, blockID, candidate.Identity()); ok {
		t.Fatal("precondition: the canonical candidate should already be gone")
	}
	if rows := store.BlockGCCandidateProjectionsForTest(orgID, blockID); len(rows) != 1 {
		t.Fatalf("precondition: discovery rows = %d, want the stale one still standing", len(rows))
	}

	// The retry must converge even though the canonical CAS can no longer apply.
	if err := store.DeleteBlockGCCandidate(orgID, blockID, candidate.Identity()); err != nil {
		t.Fatalf("retrying settlement on an already-gone canonical row: %v", err)
	}
	if rows := store.BlockGCCandidateProjectionsForTest(orgID, blockID); len(rows) != 0 {
		t.Fatalf("discovery rows after retry = %+v, want none: a projection that outlives its candidate is rediscovered forever", rows)
	}
}

// TestR26_StaleDiscoveryNoOpRetiresItsOwnRowInsteadOfLoopingForever drives the
// same shape through the worker, which is where it actually bites.
func TestR26_StaleDiscoveryNoOpRetiresItsOwnRowInsteadOfLoopingForever(t *testing.T) {
	store := NewMockStore()
	storage := &MockStorageProvider{}
	queue := NewQueue(store)
	worker := NewWorker(store, storage, queue, 10, 0, false, &Stats{})

	orgID := uuid.New()
	blockID := testSHA256BlockID("blk-r26-stale-discovery")
	store.AddBlock(orgID, blockID, "hot", 0)

	candidate, err := store.EnsureBlockGCCandidateExact(orgID, blockID, "hot", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("EnsureBlockGCCandidateExact: %v", err)
	}
	enqueueExactBlockCandidateForTest(t, store, candidate, 0)

	// The canonical candidate disappears under the queued item, leaving only the
	// discovery row that produced it.
	store.DeleteBlockGCCandidateCanonicalForTest(orgID, blockID, candidate.Identity())
	if rows := store.BlockGCCandidateProjectionsForTest(orgID, blockID); len(rows) != 1 {
		t.Fatalf("precondition: discovery rows = %d, want 1", len(rows))
	}

	if processed, err := worker.ProcessOrgOnce(context.Background(), orgID); err != nil || processed != 1 {
		t.Fatalf("process unauthorized item: processed=%d err=%v, want 1/nil", processed, err)
	}

	if deletes := storage.ScopedBlockDeletes(); len(deletes) != 0 {
		t.Fatalf("an unauthorized work item reached physical deletion: %+v", deletes)
	}
	if queued := store.QueueItems(orgID); len(queued) != 0 {
		t.Fatalf("queue after the no-op = %+v, want empty", queued)
	}
	if rows := store.BlockGCCandidateProjectionsForTest(orgID, blockID); len(rows) != 0 {
		t.Fatalf("discovery rows after the no-op = %+v, want none: leaving one rebuilds this same item on every scan", rows)
	}

	// Re-running discovery must now find nothing at all: that is the exit the
	// loop was missing.
	bucket := db.GCDiscoveryBucket(orgID.String(), blockID)
	projected, err := store.ListBlockGCCandidatesByDay(candidate.CandidateAt, bucket)
	if err != nil {
		t.Fatalf("ListBlockGCCandidatesByDay: %v", err)
	}
	if len(projected) != 0 {
		t.Fatalf("rediscovery found %+v, want nothing", projected)
	}
}

// TestR26_UnretireableDiscoveryRowPostponesInsteadOfCompleting: if the discovery
// row cannot be removed, completing the queue item anyway would hide the problem
// and the row would come back forever. Postpone instead, so the next pass retries.
//
// POSTPONING MEANS "no retry burned", and the assertion has to say so. An earlier
// version only checked that one item was still queued, which is equally true of a
// retry — and a retry is precisely the wrong outcome: five of them park the item
// in the DLQ, ItemBlock never returns from there, and the discovery row outlives
// it and re-enqueues the identical item once the DLQ row expires. Same loop, at
// retention pace. It also injected a plain errors.New("cluster unavailable"),
// which is not a gocql error, so it silently exercised only the non-availability
// branch while reading as though it covered the availability one.
//
// Both branches are covered here, and both must postpone.
func TestR26_UnretireableDiscoveryRowPostponesInsteadOfCompleting(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "cluster_unavailable", err: gocql.ErrNoConnections},
		{name: "permanent_failure", err: errors.New("write failure: schema disagreement")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := NewMockStore()
			storage := &MockStorageProvider{}
			queue := NewQueue(store)
			worker := NewWorker(store, storage, queue, 10, 0, false, &Stats{})

			orgID := uuid.New()
			blockID := testSHA256BlockID("blk-r26-unretireable-" + tc.name)
			store.AddBlock(orgID, blockID, "hot", 0)
			candidate, err := store.EnsureBlockGCCandidateExact(orgID, blockID, "hot", time.Now().Add(-time.Hour))
			if err != nil {
				t.Fatalf("EnsureBlockGCCandidateExact: %v", err)
			}
			enqueueExactBlockCandidateForTest(t, store, candidate, 0)
			store.DeleteBlockGCCandidateCanonicalForTest(orgID, blockID, candidate.Identity())
			store.SetDeleteBlockGCCandidateDiscoveryErr(tc.err)

			if _, err := worker.ProcessOrgOnce(context.Background(), orgID); err != nil {
				t.Fatalf("ProcessOrgOnce: %v", err)
			}

			queued := store.QueueItems(orgID)
			if len(queued) != 1 {
				t.Fatalf("queue after a failed discovery cleanup = %+v, want the item preserved for a retry", queued)
			}
			if queued[0].BlockGCCandidateIdentity != candidate.Identity() {
				t.Fatalf("the preserved item lost its exact identity: %+v", queued[0])
			}
			// THE ASSERTION THAT MAKES THIS TEST LOAD-BEARING.
			if queued[0].RetryCount != 0 {
				t.Fatalf("retry_count = %d, want 0: a failed discovery cleanup must POSTPONE, not retry. "+
					"Five retries park an ItemBlock in the DLQ it never leaves, while the discovery row it "+
					"could not retire keeps re-enqueuing the same item after the DLQ row expires", queued[0].RetryCount)
			}
			if failed := store.FailedItems(orgID); len(failed) != 0 {
				t.Fatalf("DLQ after a failed discovery cleanup = %+v, want empty", failed)
			}
			if deletes := storage.ScopedBlockDeletes(); len(deletes) != 0 {
				t.Fatalf("an unauthorized item reached physical deletion: %+v", deletes)
			}
		})
	}
}

// TestR26_AdvancingCandidateAtRetiresTheStaleItemWithoutTouchingTheLiveOne
// covers the earliest-wins path, which is the only way a candidate's identity_at
// moves.
//
// Ensure(P, T0) where T0 < T1 rewrites the candidate in place and MOVES its
// discovery row from (P, T1) to (P, T0). An item already queued for (P, T1) is
// now unauthorized. It must settle harmlessly — and, critically, the discovery
// cleanup it performs on the way out must name (P, T1) and therefore miss the
// live (P, T0) row entirely. A cleanup keyed on the logical block would take the
// live lifecycle's discoverability with it, which is R26's failure mode reached
// through the earliest-wins door instead of the incarnation door.
func TestR26_AdvancingCandidateAtRetiresTheStaleItemWithoutTouchingTheLiveOne(t *testing.T) {
	store := NewMockStore()
	storage := &MockStorageProvider{}
	queue := NewQueue(store)
	worker := NewWorker(store, storage, queue, 10, 0, false, &Stats{})

	orgID := uuid.New()
	blockID := testSHA256BlockID("blk-r26-earliest-wins")
	store.AddBlock(orgID, blockID, "hot", 0)

	later := time.Now().Add(-time.Hour).UTC().Truncate(time.Millisecond)
	earlier := later.Add(-30 * time.Minute)

	// A provisional TTL-based decision lands first, and its item is queued.
	late, err := store.EnsureBlockGCCandidateExact(orgID, blockID, "hot", later)
	if err != nil {
		t.Fatalf("EnsureBlockGCCandidateExact(later): %v", err)
	}
	enqueueExactBlockCandidateForTest(t, store, late, 0)

	// An explicit zero-ref decision then wins on age and pulls candidate_at back.
	early, err := store.EnsureBlockGCCandidateExact(orgID, blockID, "hot", earlier)
	if err != nil {
		t.Fatalf("EnsureBlockGCCandidateExact(earlier): %v", err)
	}
	if !early.CandidateAt.Equal(earlier) {
		t.Fatalf("earliest-wins did not apply: candidate_at = %v, want %v", early.CandidateAt, earlier)
	}
	if early.Target != late.Target {
		t.Fatal("earliest-wins must stay within one incarnation; it changed P")
	}

	// The projection MOVED rather than accumulating: exactly one row, the new one.
	rows := store.BlockGCCandidateProjectionsForTest(orgID, blockID)
	if len(rows) != 1 || !rows[0].CandidateAt.Equal(earlier) {
		t.Fatalf("discovery rows after the advance = %+v, want only the earlier one", rows)
	}

	// Only the stale item is in the queue, so this run isolates its behaviour.
	if queued := store.QueueItems(orgID); len(queued) != 1 || !queued[0].BlockGCCandidateIdentity.CandidateAt.Equal(later) {
		t.Fatalf("precondition: queue = %+v, want only the stale item", queued)
	}
	if _, err := worker.ProcessOrgOnce(context.Background(), orgID); err != nil {
		t.Fatalf("ProcessOrgOnce: %v", err)
	}

	if deletes := storage.ScopedBlockDeletes(); len(deletes) != 0 {
		t.Fatalf("the stale work item reached physical deletion: %+v", deletes)
	}
	if store.GetBlock(orgID, blockID) == nil {
		t.Fatal("the stale work item deleted the canonical row it was no longer authorized for")
	}
	if queued := store.QueueItems(orgID); len(queued) != 0 {
		t.Fatalf("queue after the stale no-op = %+v, want empty", queued)
	}

	// The live lifecycle is untouched: candidate AND discoverability survive, so
	// the block is still reclaimable.
	if _, ok, err := store.GetBlockGCCandidateExact(orgID, blockID, early.Identity()); err != nil || !ok {
		t.Fatalf("the live candidate was consumed by the stale item: ok=%v err=%v", ok, err)
	}
	rows = store.BlockGCCandidateProjectionsForTest(orgID, blockID)
	if len(rows) != 1 || !rows[0].CandidateAt.Equal(earlier) {
		t.Fatalf("discovery rows after the stale no-op = %+v, want the live one preserved: a cleanup that erases it strands the block", rows)
	}

	// And it can still be enumerated and processed: work was postponed, not lost.
	bucket := db.GCDiscoveryBucket(orgID.String(), blockID)
	projected, err := store.ListBlockGCCandidatesByDay(earlier, bucket)
	if err != nil {
		t.Fatalf("ListBlockGCCandidatesByDay: %v", err)
	}
	if len(projected) != 1 || projected[0].Target != early.Target {
		t.Fatalf("rediscovery of the live lifecycle = %+v, want exactly the live candidate", projected)
	}
	enqueueExactBlockCandidateForTest(t, store, early, 0)
	if _, err := worker.ProcessOrgOnce(context.Background(), orgID); err != nil {
		t.Fatalf("ProcessOrgOnce(live): %v", err)
	}
	if store.GetBlock(orgID, blockID) != nil {
		t.Fatal("the live lifecycle did not reclaim the block")
	}
	if deletes := storage.ScopedBlockDeletes(); len(deletes) != 1 || deletes[0].StorageKey != early.Target.StorageKey {
		t.Fatalf("physical deletes = %+v, want exactly the live incarnation", deletes)
	}
}

// TestR26_NonBlockDLQOperationsNameTheirIdentityAt pins the DLQ selector.
//
// identity_at is a clustering column of gc_failed_items for EVERY item type. An
// admin delete or requeue that omits it selects on a prefix, and the row it hits
// is not necessarily the row the operator was looking at. This is the same class
// of defect as an L-keyed candidate delete, one table over.
func TestR26_NonBlockDLQOperationsNameTheirIdentityAt(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	libraryID := uuid.New()
	failedAt := time.Now().UTC().Truncate(time.Millisecond)
	itemID := uuid.New().String()

	// Two lifecycles of one library cascade that agree on everything the old
	// prefix selected on, and differ only in identity_at.
	first := failedAt.Add(-2 * time.Hour)
	second := failedAt.Add(-time.Hour)
	for _, identityAt := range []time.Time{first, second} {
		store.AddFailedItemForTest(GCFailedItemInfo{
			OrgID:      orgID,
			FailedAt:   failedAt,
			QueuedAt:   identityAt,
			IdentityAt: identityAt,
			ItemType:   ItemLibraryCascade,
			ItemID:     itemID,
			LibraryID:  libraryID,
		})
	}
	if failed := store.FailedItems(orgID); len(failed) != 2 {
		t.Fatalf("precondition: failed items = %d, want 2 distinct lifecycles", len(failed))
	}

	if err := store.DeleteFailedItem(orgID, failedAt, ItemLibraryCascade, itemID, GCItemIdentityAt(first)); err != nil {
		t.Fatalf("DeleteFailedItem(first): %v", err)
	}
	failed := store.FailedItems(orgID)
	if len(failed) != 1 {
		t.Fatalf("failed items after deleting one lifecycle = %d, want 1", len(failed))
	}
	if !failed[0].IdentityAt.Equal(second) {
		t.Fatalf("the delete hit the wrong lifecycle: survivor identity_at = %v, want %v", failed[0].IdentityAt, second)
	}
}

// TestR26_MutationsRefuseAnIdentityThatNamesNoLifecycle pins the fail-closed half of
// the identity contract.
//
// The store used to accept an identity with no identity_at and quietly substitute the
// row's queued_at or failed_at. That is the same defect one layer down: the mutation
// still ran, against a row nobody named. Every durable GC row has carried an explicit
// identity_at since the exact-P schema, so a zero one is a bug in the caller and the
// store must say so rather than pick a row.
//
// AnyGCItemIdentity is the one identity that legitimately names no lifecycle, and it
// belongs to PendingItemExists alone — so it is exactly what this feeds in.
func TestR26_MutationsRefuseAnIdentityThatNamesNoLifecycle(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	failedAt := time.Now().UTC().Truncate(time.Millisecond)
	queuedAt := failedAt.Add(-time.Hour)
	itemID := uuid.New().String()

	store.AddFailedItemForTest(GCFailedItemInfo{
		OrgID:      orgID,
		FailedAt:   failedAt,
		QueuedAt:   queuedAt,
		IdentityAt: queuedAt,
		ItemType:   ItemCommit,
		ItemID:     itemID,
		LibraryID:  uuid.New(),
	})

	mutations := map[string]func() error{
		"CompleteItem": func() error {
			return store.CompleteItem(orgID, queuedAt, ItemCommit, itemID, AnyGCItemIdentity())
		},
		"RequeueItem": func() error {
			return store.RequeueItem(orgID, queuedAt, time.Now().UTC(), ItemCommit, itemID, uuid.Nil, "", "", 1, AnyGCItemIdentity(), false, LibraryGuardNone)
		},
		"DeleteFailedItem": func() error {
			return store.DeleteFailedItem(orgID, failedAt, ItemCommit, itemID, AnyGCItemIdentity())
		},
		"RequeueFailedItem": func() error {
			return store.RequeueFailedItem(orgID, failedAt, ItemCommit, itemID, time.Now().UTC(), AnyGCItemIdentity())
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			if err := mutate(); err == nil {
				t.Fatal("a mutation with no identity_at must be refused: guessing the row from queued_at or " +
					"failed_at is how a lifecycle nobody named gets mutated while the call reports success")
			}
		})
	}

	// The refusals must not have touched anything on the way out.
	if failed := store.FailedItems(orgID); len(failed) != 1 {
		t.Fatalf("failed items after four refused mutations = %d, want 1 untouched", len(failed))
	}

	// ...and the dedup probe, which legitimately asks "under ANY lifecycle", still works.
	if _, err := store.PendingItemExists(orgID, uuid.Nil, ItemCommit, itemID, AnyGCItemIdentity()); err != nil {
		t.Fatalf("PendingItemExists must still accept the any-lifecycle probe: %v", err)
	}
}
