package gc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"
)

// TestP4A_LateLoserCannotConsumeTheCurrentOwnersCandidate closes the last claim-side
// hole in P4a, and it is the one the exact-P work does NOT catch on its own.
//
// Every other ABA leg in this package varies the incarnation: a candidate for P1 meeting
// a row that now holds P2. DeleteBlockGCCandidate's CAS names (storage_class,
// storage_key, candidate_at), so those are all refused. This one varies NOTHING about
// the candidate:
//
//	candidate  C(P1, T)   — unchanged throughout
//	attempt A  D1
//	attempt B  D2         — same block, same incarnation, same candidate
//
// A claims as D1 and stalls past the staleness threshold. B observes D1 stale, takes it
// over by exact CAS, and claims as D2 — all of which P4a already does correctly. Then A
// wakes up, which the design explicitly allows: declaring a claim stale does not kill the
// process holding it, and TestP4A_TakenOverAttemptCannotActAfterwards exists precisely
// for that late wake-up.
//
// A's global verify finds the block referenced, so A unwinds through "re-referenced after
// claim": release, then settle the candidate. The release correctly answers not-owner —
// but that answer used to be flattened to a bare nil by the worker's wrapper, so A walked
// straight on and deleted the candidate. Its CAS applied, because C really is unchanged.
//
// What is left is a fence owned by D2 with no candidate behind it, and nothing in the
// system can recover from that: a queue item with no candidate refuses to touch `blocks`
// at all (it has no authorized incarnation to name), the referenced pre-check likewise
// declines to release a fence it cannot name, and DeleteBlockGCCandidate took the
// discovery projection with it. If B then dies, the fence is stranded and
// BlockDeleteFenceActive refuses every future upload of that content.
//
// That is the same state BlockClaimFreshOwner refuses to create at the claim — another
// owner exists, so the candidate must survive — reached from the other side. R16 is not
// GREEN unless both entrances are closed.
func TestP4A_LateLoserCannotConsumeTheCurrentOwnersCandidate(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, &Stats{})

	orgID := uuid.New()
	blockID := testSHA256BlockID("blk-late-loser")
	candidate := p4aSeedBlockCandidate(t, store, orgID, blockID)
	if err := store.EnqueueItem(orgID, candidate.CandidateAt, ItemBlock, blockID, uuid.Nil, "hot", 0); err != nil {
		t.Fatalf("EnqueueItem: %v", err)
	}

	// The interposition point is the claim-then-verify window: A holds its claim, and the
	// EACH_QUORUM verify is the next thing it does. Steal the row there, exactly as a
	// staleness takeover by another worker would.
	var calls int
	var takeoverAuthority BlockDeleteAuthority
	store.SetBlockHasReferencesHookForTest(func(_ uuid.UUID, _ string, current bool) (bool, error) {
		calls++
		if calls == 1 {
			// The LOCAL pre-check. Zero references, so the walk proceeds to the claim.
			return false, nil
		}
		// The GLOBAL verify, with A's claim standing. B takes it over by exact CAS and
		// installs its own, then a writer republishes the content.
		if takeoverAuthority.IsZero() {
			blk := store.GetBlock(orgID, blockID)
			if blk == nil || blk.GCState != db.BlockGCStateDeleting {
				t.Errorf("expected attempt A to be holding the claim at verify time, got %+v", blk)
			}
			takeoverAuthority = store.SeedBlockClaimForTest(orgID, blockID, "attempt-B-D2", time.Now())
		}
		return true, nil
	})

	if _, err := w.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce returned a fatal error: %v", err)
	}

	// 1. The candidate must survive. This is the assertion the defect broke.
	if got := store.AllBlockGCCandidates(); len(got) != 1 {
		t.Fatalf("candidate rows = %d, want 1: a late loser consumed the candidate while another attempt owned the fence, leaving nothing able to take that fence over", len(got))
	}

	// 2. B's claim must be untouched — neither released nor overwritten.
	blk := store.GetBlock(orgID, blockID)
	if blk == nil {
		t.Fatal("canonical row disappeared")
	}
	if blk.GCState != db.BlockGCStateDeleting || blk.GCClaimID != takeoverAuthority.ClaimID {
		t.Fatalf("current owner's claim was disturbed: gc_state=%q claim=%q, want deleting/%s", blk.GCState, blk.GCClaimID, takeoverAuthority.ClaimID)
	}

	// 3. Nothing was destroyed. The verify said referenced.
	if deletes := sp.ScopedBlockDeletes(); len(deletes) != 0 {
		t.Fatalf("deleted a block the global verify reported as referenced: %+v", deletes)
	}

	// 4. Preserving the candidate is only worth anything if recovery still WORKS. Age B's
	// claim out and let an ordinary pass take it over: this is the mechanism the deleted
	// candidate would have destroyed, so asserting the row survived without asserting it
	// still functions would pin the wrong half.
	store.SetBlockHasReferencesHookForTest(nil)
	store.BackdateBlockClaimForTest(orgID, blockID, time.Now().Add(-2*blockDeleteClaimStaleAfter))
	store.AddBlockReferenceForTest(orgID, blockID, "fs:lib:obj")

	for i := 0; i < 3; i++ {
		if _, err := w.ProcessOnce(context.Background()); err != nil {
			t.Fatalf("recovery ProcessOnce %d returned a fatal error: %v", i, err)
		}
	}
	if blk := store.GetBlock(orgID, blockID); blk == nil {
		t.Fatal("canonical row disappeared for a referenced block")
	} else if blk.GCState != "" {
		t.Errorf("fence still up (gc_state=%q) after the abandoned claim aged out; the surviving candidate must be able to lift it", blk.GCState)
	}
}

// TestP4A_LateLoserPostponesInsteadOfSpendingTheRetryBudget pins the queue policy half.
//
// Losing a race the attempt was never guaranteed to win says nothing about the item, so
// it must not walk toward the DLQ — a destination ItemBlock never returns from, which
// would make the candidate this branch just went out of its way to preserve unreachable
// anyway.
func TestP4A_LateLoserPostponesInsteadOfSpendingTheRetryBudget(t *testing.T) {
	if !shouldPostponeWithoutRetry(blockClaimForeignOwnerError{ItemID: "b"}) {
		t.Fatal("a late loser must postpone: spending the retry budget parks the item in a DLQ that ItemBlock never leaves, stranding the fence it refused to disturb")
	}
	// The sibling codes this pass also settled. Each was documented as postponing and
	// GCFailureCodeBlockAuthorityInvalid in particular was not actually listed, so it
	// retried into the DLQ against its own contract.
	for _, err := range []error{
		blockCandidateAuthorityInvalidError{ItemID: "b"},
		blockCandidateWithinGraceError{ItemID: "b"},
		blockCanonicalReadUnreliableError{ItemID: "b", Reason: "r"},
	} {
		if !shouldPostponeWithoutRetry(err) {
			t.Errorf("%T is documented as postponing but spends the retry budget", err)
		}
	}
}

// TestP4A_StalePostClaimReadHandsTheFenceBackAndPostpones covers the other way this walk
// could strand a fence.
//
// The claim CAS names `IF storage_class = ? AND storage_key = ?` and commits at
// EACH_QUORUM in the serial domain, so a successful claim PROVES the row carried that
// locator. GetBlockInfo is an ordinary read and can land on a replica holding only the
// gc_* columns the claim itself just wrote — a row that reads back empty.
//
// The old shape treated that as a metadata-free stub and tried DeleteClaimedBlockStub.
// That CAS runs in the serial domain and correctly refused, but the refusal surfaced as a
// plain error: retry spent, fence still up, re-claimed next cycle, and after enough
// cycles parked in the DLQ with gc_state='deleting' standing. The observation is about
// the replica, not the block, so the fence has to come off and the item has to postpone.
func TestP4A_StalePostClaimReadHandsTheFenceBackAndPostpones(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, &Stats{})

	orgID := uuid.New()
	blockID := testSHA256BlockID("blk-stale-readback")
	candidate := p4aSeedBlockCandidate(t, store, orgID, blockID)
	if err := store.EnqueueItem(orgID, candidate.CandidateAt, ItemBlock, blockID, uuid.Nil, "hot", 0); err != nil {
		t.Fatalf("EnqueueItem: %v", err)
	}

	// A replica that never caught up: the row is there, but only the columns the claim
	// wrote. It does not heal, so the item meets it on every pass.
	store.SetGetBlockInfoHookForTest(func(info BlockInfo) BlockInfo {
		info.StorageClass = ""
		info.StorageKey = ""
		info.CreatedAt = nil
		return info
	})

	for i := 0; i < 12; i++ {
		if _, err := w.ProcessOnce(context.Background()); err != nil {
			t.Fatalf("ProcessOnce %d returned a fatal error: %v", i, err)
		}
	}

	blk := store.GetBlock(orgID, blockID)
	if blk == nil {
		t.Fatal("canonical row disappeared: a stale read must never authorize a delete")
	}
	if blk.GCState != "" {
		t.Errorf("fence left standing (gc_state=%q) on a block whose only problem is a lagging replica; BlockDeleteFenceActive would refuse every future upload of this content", blk.GCState)
	}
	failed, err := store.GetTotalFailedItems()
	if err != nil {
		t.Fatalf("GetTotalFailedItems: %v", err)
	}
	if failed != 0 {
		t.Errorf("items in the DLQ = %d, want 0: a stale read says nothing about the item and must not spend its retry budget", failed)
	}
	if got := store.AllBlockGCCandidates(); len(got) != 1 {
		t.Errorf("candidate rows = %d, want 1: nothing was decided about this block, so its work item must survive", len(got))
	}
	if deletes := sp.ScopedBlockDeletes(); len(deletes) != 0 {
		t.Fatalf("deleted bytes on the strength of a read the claim had already contradicted: %+v", deletes)
	}
}

// TestP4A_PostClaimReadErrorHandsTheFenceBackAndPostpones applies the same rule as a
// stale read to an ordinary read that returned no data at all. The claim remains the
// authoritative proof of its target, but the worker cannot safely continue, so it must
// release its exact claim while preserving the candidate for a later pass.
func TestP4A_PostClaimReadErrorHandsTheFenceBackAndPostpones(t *testing.T) {
	cases := []struct {
		name          string
		postClaimRefs bool
		err           error
	}{
		{name: "re-referenced generic error", postClaimRefs: true, err: errors.New("canonical read failed")},
		{name: "re-referenced read failure", postClaimRefs: true, err: fakeRequestError{code: gocql.ErrCodeReadFailure, msg: "replica read failure"}},
		{name: "re-referenced timeout", postClaimRefs: true, err: context.DeadlineExceeded},
		{name: "zero-ref generic error", err: errors.New("canonical read failed")},
		{name: "zero-ref read failure", err: fakeRequestError{code: gocql.ErrCodeReadFailure, msg: "replica read failure"}},
		{name: "zero-ref timeout", err: context.DeadlineExceeded},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := NewMockStore()
			sp := &MockStorageProvider{}
			q := NewQueue(store)
			w := NewWorker(store, sp, q, 100, 0, false, &Stats{})

			orgID := uuid.New()
			blockID := testSHA256BlockID("blk-post-claim-read-error-" + tc.name)
			candidate := p4aSeedBlockCandidate(t, store, orgID, blockID)
			if err := store.EnqueueItem(orgID, candidate.CandidateAt, ItemBlock, blockID, uuid.Nil, "hot", 0); err != nil {
				t.Fatalf("EnqueueItem: %v", err)
			}
			if tc.postClaimRefs {
				var reads int
				store.SetBlockHasReferencesHookForTest(func(_ uuid.UUID, _ string, _ bool) (bool, error) {
					reads++
					return reads > 1, nil
				})
			}
			store.SetGetBlockInfoErrorForTest(tc.err)

			if _, err := w.ProcessOnce(context.Background()); err != nil {
				t.Fatalf("ProcessOnce returned a fatal error: %v", err)
			}

			if blk := store.GetBlock(orgID, blockID); blk == nil {
				t.Fatal("canonical row disappeared: a failed read must never authorize a delete")
			} else if blk.GCState != "" {
				t.Errorf("fence left standing (gc_state=%q) after post-claim read error", blk.GCState)
			}
			if failed, err := store.GetTotalFailedItems(); err != nil {
				t.Fatalf("GetTotalFailedItems: %v", err)
			} else if failed != 0 {
				t.Errorf("items in the DLQ = %d, want 0: a post-claim read error must not spend its retry budget", failed)
			}
			if got := store.AllBlockGCCandidates(); len(got) != 1 {
				t.Errorf("candidate rows = %d, want 1: a failed read did not decide the block", len(got))
			}
			if deletes := sp.ScopedBlockDeletes(); len(deletes) != 0 {
				t.Fatalf("deleted bytes after post-claim read error: %+v", deletes)
			}
		})
	}
}

// TestP4A_DivergentPostClaimReadHandsTheFenceBackAndPostpones covers the third
// non-authoritative outcome of GetBlockInfo. A read from a lagging replica can show a
// different non-empty incarnation even though the claim proved its target in the serial
// domain. That observation must not consume the candidate through the retry budget.
func TestP4A_DivergentPostClaimReadHandsTheFenceBackAndPostpones(t *testing.T) {
	store := NewMockStore()
	sp := &MockStorageProvider{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, &Stats{})

	orgID := uuid.New()
	blockID := testSHA256BlockID("blk-divergent-readback")
	candidate := p4aSeedBlockCandidate(t, store, orgID, blockID)
	if err := store.EnqueueItem(orgID, candidate.CandidateAt, ItemBlock, blockID, uuid.Nil, "hot", 0); err != nil {
		t.Fatalf("EnqueueItem: %v", err)
	}
	store.SetGetBlockInfoHookForTest(func(info BlockInfo) BlockInfo {
		info.StorageKey = candidate.Target.StorageKey + ".stale"
		return info
	})

	// Six generic errors used to consume all five retries and move the only recovery
	// candidate to the DLQ. Keep the divergence present for twice that boundary.
	for i := 0; i < 12; i++ {
		if _, err := w.ProcessOnce(context.Background()); err != nil {
			t.Fatalf("ProcessOnce %d returned a fatal error: %v", i, err)
		}
	}

	if blk := store.GetBlock(orgID, blockID); blk == nil {
		t.Fatal("canonical row disappeared: a divergent ordinary read must not authorize a delete")
	} else if blk.GCState != "" {
		t.Errorf("fence left standing (gc_state=%q) after divergent post-claim read", blk.GCState)
	}
	if failed, err := store.GetTotalFailedItems(); err != nil {
		t.Fatalf("GetTotalFailedItems: %v", err)
	} else if failed != 0 {
		t.Errorf("items in the DLQ = %d, want 0: a divergent ordinary read must not consume the recovery candidate", failed)
	}
	if got := store.AllBlockGCCandidates(); len(got) != 1 {
		t.Errorf("candidate rows = %d, want 1: a divergent ordinary read did not decide the block", len(got))
	}
	if queued := store.QueueItems(orgID); len(queued) != 1 {
		t.Errorf("live queue items = %d, want 1: the candidate must remain reachable after a divergent ordinary read", len(queued))
	} else if queued[0].RetryCount != 0 {
		t.Errorf("live queue retry count = %d, want 0: a divergent ordinary read must not spend a retry", queued[0].RetryCount)
	}
	if deletes := sp.ScopedBlockDeletes(); len(deletes) != 0 {
		t.Fatalf("deleted bytes after divergent post-claim read: %+v", deletes)
	}
}
