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
// The structure is real but the inference is not: PromotePublishAttemptReferences
// never verifies that a pub: row exists, and sync's idempotent-retry path reaches
// finalize without ever staging one. A check added to staging is therefore
// unreachable from that path, which is what makes this a design defect rather
// than a missing validation.
//
// WHAT THESE TESTS DO AND DO NOT PROVE
// -------------------------------------
// TestRepairPublishedSyncCommitBlockDeltaStagesBeforeFinalizing drives the real
// entry point and is the reproduction of R25. It is RED today.
//
// TestStagedPublicationShapeSatisfiesTheHandshake pins the intended stage→finalize
// shape. It is NOT control-flow coverage of the normal and auto-merge production
// branches: it calls stageSyncCommitBlockDelta itself, so a future refactor that
// dropped staging from handleSyncHeadPromotion or tryAutoMergeSyncHeadPromotion
// would not turn it red. Covering those branches needs a DB session — they run
// inside the HEAD CAS retry loop — so that belongs to the integration leg, not
// here. Do not cite this file as proof that every production path stages.
//
// WHY THE HANDSHAKE AND NOT THE FENCE
// ------------------------------------
// The safety consequence — a fenced block must not gain a permanent reference —
// belongs to the integration leg, which cannot go green until R3's post-check
// exists on any path at all, so it gates X1 closure rather than one PR. The
// handshake gates the R25 fix on its own: it goes green as soon as the repair
// path re-stages, with no protocol decision required.

const (
	handshakeOrgID    = "00000000-0000-0000-0000-000000000001"
	handshakeRepoID   = "lib-r25"
	handshakeHeadID   = "head-r25"
	handshakeBlockOne = "1111111111111111111111111111111111111111111111111111111111111111"
	handshakeBlockTwo = "2222222222222222222222222222222222222222222222222222222222222222"
)

// handshakeRecord captures what a path staged and promoted, per attempt id, so
// the assertion can be "this attempt staged before it promoted" rather than the
// much weaker "something staged".
type handshakeRecord struct {
	staged   map[string][]string
	promoted map[string][]string
}

// installHandshakeSeams swaps in recording seams for the publication primitives
// and restores them when the test ends.
func installHandshakeSeams(t *testing.T) *handshakeRecord {
	t.Helper()
	rec := &handshakeRecord{
		staged:   map[string][]string{},
		promoted: map[string][]string{},
	}

	origStage := stageSyncPublishAttemptReferencesFn
	origPromote := promoteSyncPublishAttemptReferencesFn
	origBuild := buildSyncCommitBlockDeltaFn
	origResolve := resolveSyncBlockIDsFn
	t.Cleanup(func() {
		stageSyncPublishAttemptReferencesFn = origStage
		promoteSyncPublishAttemptReferencesFn = origPromote
		buildSyncCommitBlockDeltaFn = origBuild
		resolveSyncBlockIDsFn = origResolve
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
		// handshake, and invoking it would drag in a real FSHelper. Note that the
		// production implementation calls it without checking that any pub: row
		// for attemptID exists — which is half of the defect.
		rec.promoted[attemptID] = append(rec.promoted[attemptID], blockIDs...)
		return nil
	}
	// The delta every path publishes: one file carrying two blocks.
	// resolvedAddedBlockIDs is left empty on purpose so finalize behaves exactly
	// as it does for a caller that did not stage — including its own resolve.
	buildSyncCommitBlockDeltaFn = func(_ *SyncHandler, _, _ string) (syncCommitBlockDelta, error) {
		return syncCommitBlockDelta{
			addedFiles: []syncCommitFileReference{{
				fsID:     "fs-r25",
				blockIDs: []string{handshakeBlockOne, handshakeBlockTwo},
			}},
		}, nil
	}
	// Resolution would need a DB, and finalize resolves when the delta arrives
	// unresolved — exactly the repair path's shape. Identity keeps the difference
	// under test on the handshake.
	resolveSyncBlockIDsFn = func(_ *SyncHandler, _, _ string, blockIDs []string) ([]string, error) {
		return db.NormalizeBlockIDs(blockIDs), nil
	}
	return rec
}

func newHandshakeHandler() *SyncHandler {
	return &SyncHandler{
		// Non-nil so repairPublishedSyncCommitBlockDelta does not take its "no DB,
		// nothing to do" early return. The seams mean it is never dereferenced.
		db:                   &db.DB{},
		finalizedBlockDeltas: newSyncFinalizedDeltaSet(),
	}
}

// assertPromotedOnlyWhatItStaged fails when any attempt promoted blocks without
// having staged a pub: for that same attempt.
func (rec *handshakeRecord) assertPromotedOnlyWhatItStaged(t *testing.T) {
	t.Helper()
	for attemptID, promoted := range rec.promoted {
		staged := rec.staged[attemptID]
		if len(staged) == 0 {
			t.Fatalf("promoted %d block(s) for attempt %s with no pub: staged for it\n"+
				"every path that writes permanent fs: references must first establish\n"+
				"the checked pub: handshake (R25 in docs/GC-X1-CLOSURE-OPTIONS.md)",
				len(promoted), attemptID)
		}
		index := make(map[string]struct{}, len(staged))
		for _, s := range staged {
			index[s] = struct{}{}
		}
		for _, p := range promoted {
			if _, ok := index[p]; !ok {
				t.Fatalf("attempt %s promoted block %s that it never staged", attemptID, p)
			}
		}
	}
}

// TestRepairPublishedSyncCommitBlockDeltaStagesBeforeFinalizing is the R25
// reproduction. It drives the real production entry point:
// handleSyncHeadPromotion answers currentHead == targetHead with
// handleSyncHeadIdempotentSuccess (sync.go:4221-4224), which calls exactly this
// helper, which rebuilds the delta and goes straight to finalize.
//
// EXPECTED RESULT TODAY: RED.
//
//	--- FAIL: TestRepairPublishedSyncCommitBlockDeltaStagesBeforeFinalizing
//	    promoted 2 block(s) for attempt head-r25 with no pub: staged for it
//
// Routing this helper through stageSyncCommitBlockDelta turns it green. If it
// goes green without that change, suspect the seams before believing the result.
func TestRepairPublishedSyncCommitBlockDeltaStagesBeforeFinalizing(t *testing.T) {
	rec := installHandshakeSeams(t)
	h := newHandshakeHandler()

	if err := h.repairPublishedSyncCommitBlockDelta(handshakeOrgID, handshakeRepoID, handshakeHeadID); err != nil {
		t.Fatalf("repairPublishedSyncCommitBlockDelta returned error: %v", err)
	}
	if len(rec.promoted) == 0 {
		t.Fatal("repair promoted nothing; the test no longer exercises the path it was written for")
	}
	rec.assertPromotedOnlyWhatItStaged(t)
}

// TestRepairSkipsEverythingOnceThisProcessFinalized guards the fix's shape rather
// than the defect: repairPublishedSyncCommitBlockDelta consults a per-process memo
// first (sync.go:141,158-194), and a fix that simply always re-stages must not
// quietly make every idempotent retry pay the full reconciliation. A path that
// promotes nothing satisfies the handshake trivially.
//
// This memo is also why R25's reachability is "a retry that lands on another
// instance, or after a failed finalize, restart or eviction" rather than "any
// retry": a warm same-instance retry short-circuits here.
func TestRepairSkipsEverythingOnceThisProcessFinalized(t *testing.T) {
	rec := installHandshakeSeams(t)
	h := newHandshakeHandler()
	h.finalizedBlockDeltas.mark(handshakeRepoID, handshakeHeadID)

	if err := h.repairPublishedSyncCommitBlockDelta(handshakeOrgID, handshakeRepoID, handshakeHeadID); err != nil {
		t.Fatalf("repairPublishedSyncCommitBlockDelta returned error: %v", err)
	}
	if len(rec.promoted) != 0 {
		t.Fatalf("memoized retry promoted %d attempt(s); it must short-circuit before finalize", len(rec.promoted))
	}
	if len(rec.staged) != 0 {
		t.Fatalf("memoized retry staged %d attempt(s); it must short-circuit before staging too", len(rec.staged))
	}
}

// TestStagedPublicationShapeSatisfiesTheHandshake pins the intended shape: a
// caller that stages and then finalizes satisfies the invariant, and finalize
// promotes exactly what was staged.
//
// SCOPE: this drives the helpers, not handleSyncHeadPromotion or
// tryAutoMergeSyncHeadPromotion. Those run inside the HEAD CAS retry loop and
// need a DB session. A refactor that dropped staging from either branch would
// NOT turn this test red — covering that is the integration leg's job. Read this
// as "the shared staged shape is correct", never as "every production path
// stages".
func TestStagedPublicationShapeSatisfiesTheHandshake(t *testing.T) {
	rec := installHandshakeSeams(t)
	h := newHandshakeHandler()

	staged, err := h.stageSyncCommitBlockDelta(handshakeOrgID, handshakeRepoID, handshakeHeadID)
	if err != nil {
		t.Fatalf("stageSyncCommitBlockDelta returned error: %v", err)
	}
	if err := h.finalizeSyncCommitBlockDelta(handshakeOrgID, handshakeRepoID, handshakeHeadID, staged); err != nil {
		t.Fatalf("finalizeSyncCommitBlockDelta returned error: %v", err)
	}
	if len(rec.promoted) == 0 {
		t.Fatal("staged publication promoted nothing; the shape under test is no longer exercised")
	}
	rec.assertPromotedOnlyWhatItStaged(t)
}

// TestPromotePublishAttemptReferencesDoesNotRequireAStagedRow pins the other half
// of R25 as a property of the primitive rather than of any one caller: promotion
// registers permanent references without ever asking whether this attempt has a
// pub: row. It is the reason a caller that skips staging fails silently instead
// of erroring, and the reason the fix belongs in the callers unless the design
// decides to carry durable proof of validation into promote.
//
// EXPECTED RESULT TODAY: PASS, documenting current behaviour. If the design
// chooses to make promote demand a staged row, this is the test to invert.
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
