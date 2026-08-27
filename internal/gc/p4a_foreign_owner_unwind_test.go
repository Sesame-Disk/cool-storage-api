package gc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// foreignOwnerUnwindCase is one post-claim failure point that unwinds by handing the
// fence back and then returning an ORDINARY error — the kind the queue retries and, at
// the cap, parks in the DLQ.
//
// Each case is run twice: once with a takeover interposed between the claim and the
// failure point (the late loser, which must postpone), and once without (the attempt
// still owns its claim, and the error must spend the budget exactly as before).
type foreignOwnerUnwindCase struct {
	name string
	// seed stages the canonical row and any provider failure this failure point needs,
	// and returns the candidate the queue item is built from.
	seed func(t *testing.T, store *MockStore, sp *MockStorageProvider, orgID uuid.UUID, blockID string) BlockGCCandidateInfo
	// verifyErr, when set, is what the GLOBAL reference verify answers. It is the only
	// failure point that fires before the destructive phase is entered at all.
	verifyErr error
	// ownedErrorMetric is the GCErrorsTotal reason this failure point raises, or "" when
	// it raises none. It is asserted from BOTH sides for the same reason the retry budget
	// is: these counters are the item-specific alert — "this block is defective" — and a
	// late loser is in no position to draw that conclusion either. The owner must still
	// raise it, or the fix would have silenced a real defect instead of a race.
	ownedErrorMetric string
}

// gcErrorCount reads one GCErrorsTotal reason. The counters are process-global, so every
// assertion here is a DELTA around one driven walk.
func gcErrorCount(reason string) float64 {
	if reason == "" {
		return 0
	}
	return testutil.ToFloat64(metrics.GCErrorsTotal.WithLabelValues(reason))
}

func foreignOwnerUnwindCases() []foreignOwnerUnwindCase {
	seedWithLocator := func(class string, key func(orgID uuid.UUID, blockID string) string) func(*testing.T, *MockStore, *MockStorageProvider, uuid.UUID, string) BlockGCCandidateInfo {
		return func(t *testing.T, store *MockStore, _ *MockStorageProvider, orgID uuid.UUID, blockID string) BlockGCCandidateInfo {
			t.Helper()
			store.AddBlock(orgID, blockID, class, 0)
			if key != nil {
				store.SetBlockStorageKeyForTest(orgID, blockID, key(orgID, blockID))
			}
			candidate, err := store.EnsureBlockGCCandidateExact(orgID, blockID, class, time.Now().Add(-2*time.Hour))
			if err != nil {
				t.Fatalf("EnsureBlockGCCandidateExact: %v", err)
			}
			return candidate
		}
	}

	return []foreignOwnerUnwindCase{
		{
			// The realistic one. The comment on this branch already argues that a
			// ReadFailure from a tombstone-heavy block_references partition is
			// item-specific and must spend the budget — which is right, but only for an
			// attempt that still owns the fence it is unwinding from.
			name: "global-reference-verify-error",
			seed: func(t *testing.T, store *MockStore, _ *MockStorageProvider, orgID uuid.UUID, blockID string) BlockGCCandidateInfo {
				t.Helper()
				return p4aSeedBlockCandidate(t, store, orgID, blockID)
			},
			verifyErr:        errors.New("read failure: too many tombstones in block_references"),
			ownedErrorMetric: "liveness_verify_failed",
		},
		{
			name: "non-canonical-storage-class",
			seed: seedWithLocator("Hot Storage", nil),
		},
		{
			name: "untrimmed-storage-key",
			seed: seedWithLocator("hot", func(orgID uuid.UUID, blockID string) string {
				return MockCanonicalStorageKey(orgID.String(), blockID) + " "
			}),
		},
		{
			name: "block-store-resolve-failure",
			seed: func(t *testing.T, store *MockStore, sp *MockStorageProvider, orgID uuid.UUID, blockID string) BlockGCCandidateInfo {
				t.Helper()
				candidate := p4aSeedBlockCandidate(t, store, orgID, blockID)
				sp.FailResolve(errors.New("storage class is not configured for this org"))
				return candidate
			},
		},
		{
			name: "physical-locator-rejected",
			seed: seedWithLocator("hot", func(_ uuid.UUID, blockID string) string {
				// A locator belonging to a different org: trimmed, non-empty, canonical
				// class, and refused by the org-scoped store that owns the naming rules.
				return MockCanonicalStorageKey(uuid.New().String(), blockID)
			}),
			ownedErrorMetric: "block_storage_key_mismatch",
		},
	}
}

// foreignOwnerUnwindResult is the state one driven walk leaves behind.
type foreignOwnerUnwindResult struct {
	store    *MockStore
	provider *MockStorageProvider
	orgID    uuid.UUID
	blockID  string
	takeover BlockDeleteAuthority
	// itemErrorsRaised is the delta on this case's own GCErrorsTotal reason, and
	// refusalsRecorded the delta on the claim counter that says a late loser was turned
	// away.
	itemErrorsRaised float64
	refusalsRecorded float64
}

// runForeignOwnerUnwind drives one failure point to completion.
func runForeignOwnerUnwind(t *testing.T, tc foreignOwnerUnwindCase, retryCount int, takeoverAtVerify bool) foreignOwnerUnwindResult {
	t.Helper()
	store := NewMockStore()
	sp := &MockStorageProvider{}
	q := NewQueue(store)
	w := NewWorker(store, sp, q, 100, 0, false, &Stats{})

	orgID := uuid.New()
	blockID := testSHA256BlockID("blk-foreign-owner-" + tc.name)
	candidate := tc.seed(t, store, sp, orgID, blockID)
	enqueueExactBlockCandidateForTest(t, store, candidate, retryCount)

	// The interposition point is the claim-then-verify window, the same one
	// TestP4A_LateLoserCannotConsumeTheCurrentOwnersCandidate uses: this attempt holds
	// its claim and the EACH_QUORUM verify is the next thing it does.
	var calls int
	var takeover BlockDeleteAuthority
	store.SetBlockHasReferencesHookForTest(func(_ uuid.UUID, _ string, _ bool) (bool, error) {
		calls++
		if calls == 1 {
			// The LOCAL pre-check. Zero references, so the walk proceeds to the claim.
			return false, nil
		}
		if takeoverAtVerify && takeover.IsZero() {
			blk := store.GetBlock(orgID, blockID)
			if blk == nil || blk.GCState != db.BlockGCStateDeleting {
				t.Errorf("expected this attempt to hold the claim at verify time, got %+v", blk)
			}
			takeover = store.SeedBlockClaimForTest(orgID, blockID, "attempt-B-D2", time.Now())
		}
		if tc.verifyErr != nil {
			return false, tc.verifyErr
		}
		// Still unreferenced: the walk carries on into the destructive phase, where the
		// remaining failure points live.
		return false, nil
	})

	itemErrorsBefore := gcErrorCount(tc.ownedErrorMetric)
	refusalsBefore := testutil.ToFloat64(metrics.GCBlockDeleteClaimTotal.WithLabelValues("retry_refused_foreign_owner"))

	if _, err := w.ProcessOnce(context.Background()); err != nil {
		t.Fatalf("ProcessOnce returned a fatal error: %v", err)
	}
	if calls < 2 {
		t.Fatalf("the walk never reached the global verify (BlockHasReferences calls = %d); this case is not exercising a POST-claim unwind", calls)
	}
	return foreignOwnerUnwindResult{
		store:            store,
		provider:         sp,
		orgID:            orgID,
		blockID:          blockID,
		takeover:         takeover,
		itemErrorsRaised: gcErrorCount(tc.ownedErrorMetric) - itemErrorsBefore,
		refusalsRecorded: testutil.ToFloat64(metrics.GCBlockDeleteClaimTotal.WithLabelValues("retry_refused_foreign_owner")) - refusalsBefore,
	}
}

// TestP4A_ForeignOwnerUnwindDoesNotSpendTheRetryBudget is the queue-policy half of the
// late-loser contract, applied to every post-claim path that can reach the DLQ.
//
// The re-referenced branch already refuses to SETTLE the candidate when the release
// answers not-owner. But preserving the candidate row only preserves recovery if the
// work item that carries it stays reachable, and these five paths dropped the release
// outcome on the floor and returned an ordinary error. An attempt whose claim was taken
// over while it worked then spent the item's retry budget — and at the cap, ItemBlock
// lands in a DLQ it never leaves (isAutoRecoverableFailedItem rescues only
// commit/fs_object items) past a scanner day cursor that has already advanced to
// today-1. What is left is a candidate nothing can discover, standing behind whichever
// fence the current owner holds. If that owner then dies, the fence is stranded and
// BlockDeleteFenceActive refuses every future upload of that content.
//
// A not-owner release means another lifecycle owns the fence RIGHT NOW. This attempt has
// no authority to conclude anything about the item from a walk it no longer owns, so the
// only sound answer is the one BlockClaimFreshOwner gives at the claim: postpone, leave
// the candidate and its work item alone, and let whoever owns the fence finish.
func TestP4A_ForeignOwnerUnwindDoesNotSpendTheRetryBudget(t *testing.T) {
	for _, tc := range foreignOwnerUnwindCases() {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// Both sides of the retry cap: below it the defect shows up as a spent retry,
			// at it as a DLQ entry. An item already near the cap needs only ONE lost race
			// to be stranded, so the cap case is not a corner — it is the cheap path to
			// the failure this test exists to forbid.
			for _, retryCount := range []int{0, 5} {
				retryCount := retryCount
				t.Run(retryCountLabel(retryCount), func(t *testing.T) {
					got := runForeignOwnerUnwind(t, tc, retryCount, true)

					// 1. The candidate must survive: it is what will lift the current
					// owner's fence if that owner dies.
					if candidates := got.store.AllBlockGCCandidates(); len(candidates) != 1 {
						t.Fatalf("candidate rows = %d, want 1", len(candidates))
					}

					// 2. The current owner's claim must be untouched.
					blk := got.store.GetBlock(got.orgID, got.blockID)
					if blk == nil {
						t.Fatal("canonical row disappeared")
					}
					if blk.GCState != db.BlockGCStateDeleting || blk.GCClaimID != got.takeover.ClaimID {
						t.Fatalf("current owner's claim was disturbed: gc_state=%q claim=%q, want deleting/%s", blk.GCState, blk.GCClaimID, got.takeover.ClaimID)
					}

					// 3. THE POINT OF THIS TEST. The work item must still be live, and must
					// not have paid for a race it was never guaranteed to win.
					items := got.store.QueueItems(got.orgID)
					if len(items) != 1 {
						t.Fatalf("live queue items = %d, want 1: the candidate survived but nothing is left to carry it back", len(items))
					}
					if items[0].RetryCount != retryCount {
						t.Errorf("retry_count = %d, want %d: a late loser spent the item's budget on another lifecycle's walk", items[0].RetryCount, retryCount)
					}
					if failed := got.store.FailedItems(got.orgID); len(failed) != 0 {
						t.Errorf("DLQ entries = %d, want 0: ItemBlock never leaves the DLQ, so the surviving candidate would be undiscoverable: %+v", len(failed), failed)
					}

					// 4. Nothing was destroyed on the way out.
					if deletes := got.provider.ScopedBlockDeletes(); len(deletes) != 0 {
						t.Errorf("physical deletes = %d, want 0: %+v", len(deletes), deletes)
					}

					// 5. The refusal is observable, and it did NOT masquerade as an
					// item defect. Raising the item-specific counter for a lost race
					// would page someone about a healthy block, and the owner raises it
					// on the next pass anyway.
					if got.refusalsRecorded != 1 {
						t.Errorf("retry_refused_foreign_owner delta = %v, want 1: the refusal must be visible to an operator", got.refusalsRecorded)
					}
					if tc.ownedErrorMetric != "" && got.itemErrorsRaised != 0 {
						t.Errorf("GCErrorsTotal{%s} delta = %v, want 0: a late loser concluded the ITEM was defective from a walk it no longer owned", tc.ownedErrorMetric, got.itemErrorsRaised)
					}
				})
			}
		})
	}
}

// TestP4A_OwnedUnwindStillSpendsTheRetryBudget pins the other half, and it is why the fix
// is a not-owner check rather than a blanket postpone.
//
// A permanent, item-specific defect — a non-canonical class, a locator its own store
// refuses — SHOULD spend retries and SHOULD reach the DLQ, because that is the only way
// a human ever sees it. Turning these paths into unconditional postpones would trade a
// stranded fence for an item that retries forever in silence, which is exactly the
// failure mode the comment above the classifier warns about.
//
// The distinction is authority, not error class: an attempt that still owns the fence it
// is unwinding from has the standing to conclude something about the item.
func TestP4A_OwnedUnwindStillSpendsTheRetryBudget(t *testing.T) {
	for _, tc := range foreignOwnerUnwindCases() {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Run(retryCountLabel(0), func(t *testing.T) {
				got := runForeignOwnerUnwind(t, tc, 0, false)
				items := got.store.QueueItems(got.orgID)
				if len(items) != 1 {
					t.Fatalf("live queue items = %d, want 1", len(items))
				}
				if items[0].RetryCount != 1 {
					t.Errorf("retry_count = %d, want 1: an attempt that still owned its claim must spend the budget so a permanent defect can reach the DLQ", items[0].RetryCount)
				}
				// The fence must be off either way: the release itself is not conditional
				// on any of this.
				if blk := got.store.GetBlock(got.orgID, got.blockID); blk == nil {
					t.Fatal("canonical row disappeared")
				} else if blk.GCState != "" {
					t.Errorf("fence still up (gc_state=%q) after an owned unwind handed the claim back", blk.GCState)
				}
				// And the alert an operator acts on must still fire. Gating these
				// counters on ownership is only safe while the owner still raises them.
				if tc.ownedErrorMetric != "" && got.itemErrorsRaised != 1 {
					t.Errorf("GCErrorsTotal{%s} delta = %v, want 1: gating the counter on ownership silenced a real defect", tc.ownedErrorMetric, got.itemErrorsRaised)
				}
				if got.refusalsRecorded != 0 {
					t.Errorf("retry_refused_foreign_owner delta = %v, want 0: nothing was refused, this attempt owned its fence", got.refusalsRecorded)
				}
			})
			t.Run(retryCountLabel(5), func(t *testing.T) {
				got := runForeignOwnerUnwind(t, tc, 5, false)
				if failed := got.store.FailedItems(got.orgID); len(failed) != 1 {
					t.Fatalf("DLQ entries = %d, want 1: a retry-capped item whose claim was its own must still be surfaced to a human", len(failed))
				}
			})
		})
	}
}

func retryCountLabel(retryCount int) string {
	if retryCount == 0 {
		return "below-retry-cap"
	}
	return "at-retry-cap"
}
