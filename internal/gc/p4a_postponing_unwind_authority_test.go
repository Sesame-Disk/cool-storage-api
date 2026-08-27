package gc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"
)

// THE UNWINDS THAT ALREADY POSTPONED ARE THE ONES THE RULE FORGOT.
//
// TestP4A_ForeignOwnerUnwindDoesNotSpendTheRetryBudget covers the five post-claim exits
// that return a RETRYABLE error. Those were the sites where losing the claim used to cost
// the item its retry budget, so they were the sites that got fixed.
//
// Three more post-claim exits return a POSTPONING error instead, and they were left
// alone on the reasoning that ownership could not change an answer that was going to
// postpone either way. That reasoning held exactly until "a late loser makes no durable
// queue mutation" became the rule — because postponing is postponeItem, postponeItem is
// RequeueItem, and RequeueItem is a durable queue mutation. A stale attempt reaching one
// of these three would hand its months-old copy of the queue row to a DELETE(old) +
// INSERT(new) batch that inserts whether or not the delete addressed anything (E6).
//
// The three:
//
//	global liveness verify, AVAILABILITY half  — the ownership check guarded only the
//	                                             non-availability branch
//	releaseAndPostponeUnreliableRead           — discarded the outcome entirely
//	destructive topology gate at the commit    — discarded the outcome entirely
//
// Each case is run twice, and the second half is the one that keeps the fix honest: an
// attempt that STILL OWNS its fence must postpone exactly as before, requeue included.
// Turning these into unconditional no-ops would strand every one of them.
type postponingUnwindCase struct {
	name string
	// verifyErr, when set, is what the GLOBAL reference verify answers.
	verifyErr error
	// interpose runs at the claim-then-verify window, with the claim held, so the failure
	// this case needs is armed from inside the walk rather than before it. The topology
	// gate in particular has to be rejected THERE: armed earlier it refuses before the
	// claim, and the walk would never become post-claim at all.
	interpose func(t *testing.T, w *Worker, store *MockStore)
}

func postponingUnwindCases() []postponingUnwindCase {
	return []postponingUnwindCase{
		{
			// The half of the verify branch the ownership check did not cover. An
			// unreachable datacenter makes this systematic rather than rare: every block
			// in flight arrives here at once, and a stale attempt is as likely to be one
			// of them as the current owner is.
			name:      "global-verify-unavailable",
			verifyErr: gocql.ErrNoHosts,
		},
		{
			// An ordinary post-claim read landing on a replica that carries only the gc_*
			// columns the claim itself just wrote.
			name: "unreliable-canonical-read",
			interpose: func(_ *testing.T, _ *Worker, store *MockStore) {
				store.SetGetBlockInfoErrorForTest(errors.New("read timeout on the canonical row"))
			},
		},
		{
			// Rejected at the commit point, after the claim and after the verify proved
			// the block unreferenced. Armed from inside the walk for the reason above.
			name: "topology-gate-rejected-at-commit",
			interpose: func(_ *testing.T, w *Worker, _ *MockStore) {
				w.SetDestructiveTopologyGate(func() error {
					return errors.New("keyspace replication does not give EACH_QUORUM a per-datacenter meaning")
				})
			},
		},
	}
}

type postponingUnwindResult struct {
	store         *MockStore
	provider      *MockStorageProvider
	orgID         uuid.UUID
	blockID       string
	takeover      BlockDeleteAuthority
	originalItem  QueueItem
	completeDelta int64
	requeueDelta  int64
	failDelta     int64
}

func runPostponingUnwind(t *testing.T, tc postponingUnwindCase, takeoverAtVerify bool) postponingUnwindResult {
	t.Helper()
	store := NewMockStore()
	sp := &MockStorageProvider{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, &Stats{})

	orgID := uuid.New()
	blockID := testSHA256BlockID("blk-postponing-unwind-" + tc.name)
	candidate := p4aSeedBlockCandidate(t, store, orgID, blockID)
	enqueueExactBlockCandidateForTest(t, store, candidate, 0)
	queued := store.QueueItems(orgID)
	if len(queued) != 1 {
		t.Fatalf("queue items after enqueue = %d, want 1", len(queued))
	}
	original := queued[0]

	var calls int
	var takeover BlockDeleteAuthority
	store.SetBlockHasReferencesHookForTest(func(_ uuid.UUID, _ string, _ bool) (bool, error) {
		calls++
		if calls == 1 {
			// The LOCAL pre-check. Zero references, so the walk proceeds to the claim.
			return false, nil
		}
		if calls == 2 {
			blk := store.GetBlock(orgID, blockID)
			if blk == nil || blk.GCState != db.BlockGCStateDeleting {
				t.Errorf("expected this attempt to hold the claim at verify time, got %+v", blk)
			}
			if tc.interpose != nil {
				tc.interpose(t, w, store)
			}
			if takeoverAtVerify {
				takeover = store.SeedBlockClaimForTest(orgID, blockID, "attempt-B-D2", time.Now())
			}
		}
		if tc.verifyErr != nil {
			return false, tc.verifyErr
		}
		return false, nil
	})

	completeBefore := store.QueueCompleteCallsForTest()
	requeueBefore := store.QueueRequeueCallsForTest()
	failBefore := store.QueueFailCallsForTest()

	if _, err := w.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce returned a fatal error: %v", err)
	}
	if calls < 2 {
		t.Fatalf("the walk never reached the global verify (BlockHasReferences calls = %d); this case is not exercising a POST-claim unwind", calls)
	}
	if takeoverAtVerify && takeover.IsZero() {
		t.Fatal("the takeover was never interposed; the case proves nothing about a late loser")
	}

	return postponingUnwindResult{
		store:         store,
		provider:      sp,
		orgID:         orgID,
		blockID:       blockID,
		takeover:      takeover,
		originalItem:  original,
		completeDelta: store.QueueCompleteCallsForTest() - completeBefore,
		requeueDelta:  store.QueueRequeueCallsForTest() - requeueBefore,
		failDelta:     store.QueueFailCallsForTest() - failBefore,
	}
}

// TestP4A_ForeignOwnerOnAPostponingUnwindLeavesTheQueueUntouched pins the rule these
// three exits were missing: a lost claim means no durable queue mutation, and postponing
// is one.
func TestP4A_ForeignOwnerOnAPostponingUnwindLeavesTheQueueUntouched(t *testing.T) {
	for _, tc := range postponingUnwindCases() {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := runPostponingUnwind(t, tc, true)

			// 1. THE POINT. No Complete, no Requeue, no Fail — postponeItem included.
			if got.completeDelta != 0 || got.requeueDelta != 0 || got.failDelta != 0 {
				t.Errorf("a late loser mutated the queue: Complete=%d Requeue=%d Fail=%d; postponing is RequeueItem, and this attempt lost the authority to move the row it is holding",
					got.completeDelta, got.requeueDelta, got.failDelta)
			}

			// 2. The row it was holding is exactly as it found it.
			items := got.store.QueueItems(got.orgID)
			if len(items) != 1 {
				t.Fatalf("live queue items = %d, want 1", len(items))
			}
			if !items[0].QueuedAt.Equal(got.originalItem.QueuedAt) ||
				items[0].RetryCount != got.originalItem.RetryCount ||
				!items[0].IdentityAt.Equal(got.originalItem.IdentityAt) ||
				items[0].BlockGCCandidateIdentity != got.originalItem.BlockGCCandidateIdentity {
				t.Errorf("the stale attempt changed its queue row: got %+v, want %+v", items[0], got.originalItem)
			}
			if failed := got.store.FailedItems(got.orgID); len(failed) != 0 {
				t.Errorf("DLQ entries = %d, want 0: %+v", len(failed), failed)
			}

			// 3. The candidate and the current owner's fence are what recovery depends on.
			if candidates := got.store.AllBlockGCCandidates(); len(candidates) != 1 {
				t.Errorf("candidate rows = %d, want 1", len(candidates))
			}
			blk := got.store.GetBlock(got.orgID, got.blockID)
			if blk == nil {
				t.Fatal("canonical row disappeared")
			}
			if blk.GCState != db.BlockGCStateDeleting || blk.GCClaimID != got.takeover.ClaimID {
				t.Errorf("the current owner's claim was disturbed: gc_state=%q claim=%q, want deleting/%s", blk.GCState, blk.GCClaimID, got.takeover.ClaimID)
			}

			// 4. Nothing was destroyed on the way out.
			if deletes := got.provider.ScopedBlockDeletes(); len(deletes) != 0 {
				t.Errorf("physical deletes = %d, want 0: %+v", len(deletes), deletes)
			}
		})
	}
}

// TestP4A_OwnedPostponingUnwindStillRequeues is the half that keeps the fix from becoming
// a different bug.
//
// These three exits postpone for good reasons that have nothing to do with ownership: an
// unreachable datacenter, a replica that cannot confirm what the serial domain proved, a
// topology that cannot authorize a destructive delete. An attempt that still owns its
// fence must go on postponing — which means moving the row to the back of the queue, so
// it does not block the head of the queue for as long as the condition lasts.
func TestP4A_OwnedPostponingUnwindStillRequeues(t *testing.T) {
	for _, tc := range postponingUnwindCases() {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := runPostponingUnwind(t, tc, false)

			if got.requeueDelta != 1 {
				t.Errorf("Requeue calls = %d, want 1: an attempt that still owns its fence must keep postponing, or the item blocks the head of the queue while the condition lasts", got.requeueDelta)
			}
			if got.completeDelta != 0 || got.failDelta != 0 {
				t.Errorf("an owned postpone must not complete or fail the item: Complete=%d Fail=%d", got.completeDelta, got.failDelta)
			}

			items := got.store.QueueItems(got.orgID)
			if len(items) != 1 {
				t.Fatalf("live queue items = %d, want 1", len(items))
			}
			if items[0].RetryCount != got.originalItem.RetryCount {
				t.Errorf("retry_count = %d, want %d: a postpone must not spend the budget", items[0].RetryCount, got.originalItem.RetryCount)
			}
			if items[0].QueuedAt.Equal(got.originalItem.QueuedAt) {
				t.Errorf("the item was not moved to the back of the queue (queued_at still %s)", got.originalItem.QueuedAt.Format(time.RFC3339Nano))
			}

			// The fence must be off either way: the release itself is not conditional on
			// any of this.
			if blk := got.store.GetBlock(got.orgID, got.blockID); blk == nil {
				t.Fatal("canonical row disappeared")
			} else if blk.GCState != "" {
				t.Errorf("fence still up (gc_state=%q) after an owned unwind handed the claim back", blk.GCState)
			}
		})
	}
}
