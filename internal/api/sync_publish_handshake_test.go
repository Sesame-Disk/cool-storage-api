package api

import (
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/db"
)

// X1 / R25 — the publication handshake, in executable form.
//
// R3 in docs/GC-X1-CLOSURE-OPTIONS.md argues that RegisterFSObjectBlockReferences
// needs no fence check of its own, because permanent fs: references are only ever
// written inside PromotePublishAttemptReferences — i.e. after this attempt staged
// pub: rows and (once R3 lands) had them post-checked against the canonical
// incarnation. Every future publication safety check hangs off that handshake.
//
// The structure is real but the inference is not, and this is the leg that shows
// it: PromotePublishAttemptReferences never verifies that a pub: row exists, and
// sync's idempotent-retry path reaches finalize without ever staging one. A check
// added to staging is therefore unreachable from that path, which is what makes
// this a design defect rather than a missing validation.
//
// WHY THIS ASSERTS THE HANDSHAKE AND NOT THE FENCE
// ------------------------------------------------
// The safety consequence — a fenced block must not gain a permanent reference —
// is the subject of the integration leg (TestSyncIdempotentRetryHonoursDeleteFence
// in internal/integration). That leg cannot go green until R3's post-check exists
// on any path at all, so it gates X1 closure, not an individual PR. This leg
// gates the R25 fix on its own: it goes green as soon as the repair path
// re-stages, with no protocol decision required.
//
// EXPECTED RESULT TODAY: RED, on the idempotent-retry case only.
//
//	--- FAIL: TestSyncPublishPathsStageBeforePromoting/idempotent retry of an
//	          applied HEAD
//	    promoted 2 block(s) for attempt <head> with no pub: staged for it
//
// The three other production entry points pass. If this test ever goes green
// without repairPublishedSyncCommitBlockDelta having been changed to re-stage,
// suspect the seams before believing the result.
func TestSyncPublishPathsStageBeforePromoting(t *testing.T) {
	const (
		orgID    = "00000000-0000-0000-0000-000000000001"
		repoID   = "lib-r25"
		headID   = "head-r25"
		blockOne = "1111111111111111111111111111111111111111111111111111111111111111"
		blockTwo = "2222222222222222222222222222222222222222222222222222222222222222"
	)

	// The delta every path under test publishes: one file carrying two blocks.
	// resolvedAddedBlockIDs is left empty on purpose so finalize behaves exactly
	// as it does for a caller that did not stage — including its own resolve.
	delta := func() syncCommitBlockDelta {
		return syncCommitBlockDelta{
			addedFiles: []syncCommitFileReference{{
				fsID:     "fs-r25",
				blockIDs: []string{blockOne, blockTwo},
			}},
		}
	}

	// handshake records what each path did, per attempt id, so the assertion is
	// "this attempt staged before it promoted" rather than "something staged".
	type handshake struct {
		staged   map[string][]string
		promoted map[string][]string
	}

	newHandler := func() *SyncHandler {
		return &SyncHandler{
			// Non-nil so repairPublishedSyncCommitBlockDelta does not take its
			// "no DB, nothing to do" early return. The seams below mean it is
			// never dereferenced.
			db:                   &db.DB{},
			finalizedBlockDeltas: newSyncFinalizedDeltaSet(),
		}
	}

	// install swaps in the recording seams and returns the record plus a restore.
	install := func(t *testing.T) *handshake {
		t.Helper()
		rec := &handshake{
			staged:   map[string][]string{},
			promoted: map[string][]string{},
		}

		origStage := stageSyncPublishAttemptReferencesFn
		origPromote := promoteSyncPublishAttemptReferencesFn
		origBuild := buildSyncCommitBlockDeltaFn
		t.Cleanup(func() {
			stageSyncPublishAttemptReferencesFn = origStage
			promoteSyncPublishAttemptReferencesFn = origPromote
			buildSyncCommitBlockDeltaFn = origBuild
		})

		stageSyncPublishAttemptReferencesFn = func(_ *db.DB, _, _, attemptID string, blockIDs []string, resolve db.BlockIDResolver) ([]string, error) {
			resolved := blockIDs
			if resolve != nil {
				var err error
				if resolved, err = resolve(blockIDs); err != nil {
					return nil, err
				}
			}
			rec.staged[attemptID] = append(rec.staged[attemptID], resolved...)
			return resolved, nil
		}
		promoteSyncPublishAttemptReferencesFn = func(_ *db.DB, _, attemptID string, blockIDs []string, registerPermanent func() error) error {
			// Deliberately NOT calling registerPermanent: this leg is about the
			// handshake, and invoking it would drag in a real FSHelper. Note that
			// the production implementation calls it without checking that any
			// pub: row for attemptID exists — which is half of the defect.
			rec.promoted[attemptID] = append(rec.promoted[attemptID], blockIDs...)
			return nil
		}
		buildSyncCommitBlockDeltaFn = func(_ *SyncHandler, _, _ string) (syncCommitBlockDelta, error) {
			return delta(), nil
		}
		return rec
	}

	// resolveSyncBlockIDs would need a DB, and finalize calls it when the delta
	// arrives unresolved — which is exactly the repair path's shape. Stub the
	// resolution to identity so the difference under test stays the handshake.
	stubResolve := func(t *testing.T) {
		t.Helper()
		orig := resolveSyncBlockIDsFn
		t.Cleanup(func() { resolveSyncBlockIDsFn = orig })
		resolveSyncBlockIDsFn = func(_ *SyncHandler, _, _ string, blockIDs []string) ([]string, error) {
			return db.NormalizeBlockIDs(blockIDs), nil
		}
	}

	cases := []struct {
		name string
		// run drives one production entry point that can end in a promote.
		run func(t *testing.T, h *SyncHandler) error
	}{
		{
			name: "first publish of a new HEAD",
			run: func(t *testing.T, h *SyncHandler) error {
				staged, err := h.stageSyncCommitBlockDelta(orgID, repoID, headID)
				if err != nil {
					return err
				}
				return h.finalizeSyncCommitBlockDelta(orgID, repoID, headID, staged)
			},
		},
		{
			name: "auto-merge publish of a merged HEAD",
			run: func(t *testing.T, h *SyncHandler) error {
				// sync.go's auto-merge branch stages the merged commit and then
				// finalizes it, same shape as a first publish.
				staged, err := h.stageSyncCommitBlockDelta(orgID, repoID, headID)
				if err != nil {
					return err
				}
				return h.finalizeSyncCommitBlockDelta(orgID, repoID, headID, staged)
			},
		},
		{
			name: "idempotent retry of an applied HEAD",
			run: func(t *testing.T, h *SyncHandler) error {
				// handleSyncHeadPromotion answers currentHead == targetHead with
				// handleSyncHeadIdempotentSuccess, which calls exactly this.
				return h.repairPublishedSyncCommitBlockDelta(orgID, repoID, headID)
			},
		},
		{
			name: "idempotent retry after this process already finalized",
			run: func(t *testing.T, h *SyncHandler) error {
				// The memoized fast path: it must not promote at all, so it
				// trivially satisfies the invariant. Present so a fix that
				// simply always re-stages does not silently make every retry pay
				// the full reconciliation.
				h.finalizedBlockDeltas.mark(repoID, headID)
				return h.repairPublishedSyncCommitBlockDelta(orgID, repoID, headID)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubResolve(t)
			rec := install(t)
			h := newHandler()

			if err := tc.run(t, h); err != nil {
				t.Fatalf("publish path returned error: %v", err)
			}

			for attemptID, promoted := range rec.promoted {
				staged := rec.staged[attemptID]
				if len(staged) == 0 {
					t.Fatalf("promoted %d block(s) for attempt %s with no pub: staged for it\n"+
						"every path that writes permanent fs: references must first establish\n"+
						"the checked pub: handshake (R25 in docs/GC-X1-CLOSURE-OPTIONS.md)",
						len(promoted), attemptID)
				}
				if missing := notCovered(promoted, staged); len(missing) > 0 {
					t.Fatalf("attempt %s promoted blocks that it never staged: %v", attemptID, missing)
				}
			}
		})
	}
}

// TestPromotePublishAttemptReferencesDoesNotRequireAStagedRow pins the other half
// of R25 as a property of the primitive rather than of any one caller: promotion
// registers permanent references without ever asking whether this attempt has a
// pub: row. It is the reason a caller that skips staging fails silently instead
// of erroring, and the reason the fix belongs in the callers (re-stage) unless
// the design decides to carry durable proof of validation into promote.
//
// EXPECTED RESULT TODAY: PASS, documenting current behaviour. If the design
// chooses to make promote demand a staged row, this test is the one to invert.
func TestPromotePublishAttemptReferencesDoesNotRequireAStagedRow(t *testing.T) {
	registered := false
	err := db.PromotePublishAttemptReferences(nil, "org", "attempt-with-no-stage", nil, func() error {
		registered = true
		return nil
	})
	if err != nil {
		t.Fatalf("PromotePublishAttemptReferences returned error: %v", err)
	}
	if !registered {
		t.Fatal("registerPermanent was not called; promote is expected to register unconditionally today")
	}
}

// notCovered returns the members of got that never appear in want.
func notCovered(got, want []string) []string {
	index := make(map[string]struct{}, len(want))
	for _, w := range want {
		index[w] = struct{}{}
	}
	var missing []string
	for _, g := range got {
		if _, ok := index[g]; !ok {
			missing = append(missing, g)
		}
	}
	return missing
}
