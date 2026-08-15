package api

import (
	"errors"
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
// The historical defect was that PromotePublishAttemptReferences never verifies
// that a pub: row exists, while sync's idempotent-retry path reached finalize
// without staging one. A check added to staging was therefore unreachable from
// that path. The production fix now establishes pub: first; this test keeps the
// handshake and its ordering executable.
//
// WHAT THESE TESTS DO AND DO NOT PROVE
// -------------------------------------
// TestRepairPublishedSyncCommitBlockDeltaEstablishesHandshakeBeforeFinalizing drives the real
// entry point and is the executable R25 gate. It must stay green after the fix.
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
// handshake gates the R25 fix on its own: it is green as soon as the repair
// path re-establishes pub:, with no protocol decision required.

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
	events   []handshakeEvent
}

type handshakeEvent struct {
	attemptID string
	phase     string
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
	origAttemptID := newSyncPublishAttemptIDFn
	origBuild := buildSyncCommitBlockDeltaFn
	origResolve := resolveSyncBlockIDsFn
	t.Cleanup(func() {
		stageSyncPublishAttemptReferencesFn = origStage
		promoteSyncPublishAttemptReferencesFn = origPromote
		newSyncPublishAttemptIDFn = origAttemptID
		buildSyncCommitBlockDeltaFn = origBuild
		resolveSyncBlockIDsFn = origResolve
	})

	recordPublishAttempt := func(phase string) func(*db.DB, string, string, string, []string, db.BlockIDResolver) ([]string, error) {
		return func(_ *db.DB, _, _, attemptID string, blockIDs []string, resolve db.BlockIDResolver) ([]string, error) {
			resolved := blockIDs
			if resolve != nil {
				var err error
				if resolved, err = resolve(blockIDs); err != nil {
					return nil, err
				}
			}
			rec.staged[attemptID] = append(rec.staged[attemptID], resolved...)
			rec.events = append(rec.events, handshakeEvent{attemptID: attemptID, phase: phase})
			return resolved, nil
		}
	}
	stageSyncPublishAttemptReferencesFn = recordPublishAttempt("stage")
	promoteSyncPublishAttemptReferencesFn = func(_ *db.DB, _, attemptID string, blockIDs []string, registerPermanent func() error) error {
		// Deliberately NOT calling registerPermanent: this leg is about the
		// handshake, and invoking it would drag in a real FSHelper. Note that the
		// production implementation calls it without checking that any pub: row
		// for attemptID exists — which is half of the defect.
		rec.events = append(rec.events, handshakeEvent{attemptID: attemptID, phase: "promote"})
		rec.promoted[attemptID] = append(rec.promoted[attemptID], blockIDs...)
		return nil
	}
	// The delta every path publishes: one file carrying two blocks.
	// resolvedAddedBlockIDs is left empty on purpose so the stage seam must
	// supply the resolved IDs before finalize.
	buildSyncCommitBlockDeltaFn = func(_ *SyncHandler, _, _ string) (syncCommitBlockDelta, error) {
		return syncCommitBlockDelta{
			addedFiles: []syncCommitFileReference{{
				fsID:     "fs-r25",
				blockIDs: []string{handshakeBlockOne, handshakeBlockTwo},
			}},
		}, nil
	}
	// Resolution would need a DB in production. Identity keeps the difference under
	// test on the handshake while the stage seam supplies the resolved IDs.
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

// assertPromotedOnlyWhatItStaged fails when any attempt promotes before staging
// or promotes blocks that were not staged for that same attempt.
func (rec *handshakeRecord) assertPromotedOnlyWhatItStaged(t *testing.T) {
	t.Helper()
	stagedAt := make(map[string]bool)
	for _, event := range rec.events {
		switch event.phase {
		case "stage":
			stagedAt[event.attemptID] = true
		case "promote":
			if !stagedAt[event.attemptID] {
				t.Fatalf("promoted before staging pub: for attempt %s", event.attemptID)
			}
		}
	}
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

// TestRepairPublishedSyncCommitBlockDeltaEstablishesHandshakeBeforeFinalizing is the R25
// reproduction. It drives the real production entry point:
// handleSyncHeadPromotion answers currentHead == targetHead with
// handleSyncHeadIdempotentSuccess, which calls exactly this helper. The helper
// must rebuild the delta, establish pub:, and then finalize.
//
// EXPECTED RESULT AFTER THE R25 FIX: GREEN.
//
// Routing this helper directly to finalize must turn it red. If it goes green
// without staging, suspect the seams before believing the result.
func TestRepairPublishedSyncCommitBlockDeltaEstablishesHandshakeBeforeFinalizing(t *testing.T) {
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

func TestPublishedSyncRepairPartialStageFailureDoesNotFinalize(t *testing.T) {
	rec := installHandshakeSeams(t)
	wantErr := errors.New("stage boom")
	stageSyncPublishAttemptReferencesFn = func(_ *db.DB, _, _, attemptID string, blockIDs []string, _ db.BlockIDResolver) ([]string, error) {
		if len(blockIDs) != 2 {
			t.Fatalf("blockIDs = %#v, want two blocks", blockIDs)
		}
		// Model a successful stage for the first block followed by an error for
		// the next one. The DB-level stage test verifies its rollback is scoped
		// to this fresh attempt ID.
		rec.staged[attemptID] = append(rec.staged[attemptID], blockIDs[0])
		rec.events = append(rec.events, handshakeEvent{attemptID: attemptID, phase: "stage"})
		return nil, wantErr
	}

	if err := newHandshakeHandler().repairPublishedSyncCommitBlockDelta(handshakeOrgID, handshakeRepoID, handshakeHeadID); !errors.Is(err, wantErr) {
		t.Fatalf("repair error = %v, want %v", err, wantErr)
	}
	if len(rec.promoted) != 0 {
		t.Fatalf("repair promoted %d attempt(s) after ensure failed", len(rec.promoted))
	}
}

func TestSyncPublicationAttemptsUseDistinctIDsForTheSameTarget(t *testing.T) {
	installHandshakeSeams(t)
	ids := []string{"attempt-a", "attempt-b"}
	newSyncPublishAttemptIDFn = func() string {
		id := ids[0]
		ids = ids[1:]
		return id
	}
	h := newHandshakeHandler()

	first, err := h.stageSyncCommitBlockDelta(handshakeOrgID, handshakeRepoID, handshakeHeadID)
	if err != nil {
		t.Fatalf("first stage returned error: %v", err)
	}
	second, err := h.stageSyncCommitBlockDelta(handshakeOrgID, handshakeRepoID, handshakeHeadID)
	if err != nil {
		t.Fatalf("second stage returned error: %v", err)
	}
	if first.publishAttemptID == second.publishAttemptID {
		t.Fatalf("same target reused publication attempt ID %q", first.publishAttemptID)
	}
	if first.publishAttemptID == handshakeHeadID || second.publishAttemptID == handshakeHeadID {
		t.Fatalf("target commit ID was used as publication attempt ID: %q/%q", first.publishAttemptID, second.publishAttemptID)
	}
}

// TestRepairSkipsEverythingOnceThisProcessFinalized guards the fix's shape rather
// than the defect: repairPublishedSyncCommitBlockDelta consults a per-process memo
// first (sync.go:141,158-194), and a fix that simply always re-ensures must not
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
