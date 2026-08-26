//go:build integration

package integration

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
)

// R26 evidence against REAL Cassandra.
//
// The unit suite proves the LOGIC of exact-P identity against MockStore. It cannot
// prove the thing this slice actually changed, because what changed is a set of
// Cassandra PRIMARY KEYs: gc_block_candidates, gc_block_candidates_by_day,
// gc_queue and gc_pending_items now carry P = (storage_class, storage_key) — and,
// for the queue-side tables, identity_at — as clustering columns.
//
// A map-backed mock will happily keep two entries apart under any key the Go code
// invents. Only the engine can answer the questions that matter here:
//
//   - does `INSERT ... IF NOT EXISTS` scope its CAS to ONE clustering row, so a
//     candidate for P2 does not collide with the existing candidate for P1;
//   - do two queue/pending rows for one logical block genuinely coexist, rather
//     than the second write silently overwriting the first;
//   - does a delete naming the full clustering key remove exactly one row and
//     leave its sibling standing.
//
// That last one is the whole slice in a sentence. If Cassandra collapsed the two
// rows, every unit test would still pass and production would still lose work.
//
// MinIO is deliberately not involved: nothing here deletes bytes.

const r26RequireEvidenceEnv = "SESAMEFS_REQUIRE_R26_EVIDENCE"

type r26EvidenceGate struct{ observed bool }

// r26RequireEvidence makes the gate non-vacuous, mirroring p4aRequireEvidence:
// without it these tests can skip their way to exit 0 and print "ok" while
// proving nothing about a stack that never came up.
func r26RequireEvidence(t *testing.T) *r26EvidenceGate {
	t.Helper()
	gate := &r26EvidenceGate{}
	if os.Getenv(r26RequireEvidenceEnv) != "1" {
		return gate
	}
	t.Cleanup(func() {
		if t.Skipped() {
			t.Errorf("%s=1 requires real Cassandra R26 evidence, but the test skipped", r26RequireEvidenceEnv)
		} else if !t.Failed() && !gate.observed {
			t.Errorf("%s=1 completed without candidate-identity evidence", r26RequireEvidenceEnv)
		}
	})
	return gate
}

// r26RemintCanonicalBlock replaces the canonical row's storage_key, which is what
// a re-mint does: same logical block, a new physical incarnation.
func r26RemintCanonicalBlock(t *testing.T, database *dbpkg.DB, orgID uuid.UUID, blockID string) gcpkg.BlockDeleteTarget {
	t.Helper()
	target := gcpkg.BlockDeleteTarget{
		StorageClass: "hot",
		StorageKey:   fmt.Sprintf("blocks/%s/%s.%s", orgID, blockID, uuid.NewString()),
	}
	if err := database.Session().Query(
		`UPDATE blocks SET storage_key = ? WHERE org_id = ? AND block_id = ?`,
		target.StorageKey, orgID.String(), blockID,
	).Exec(); err != nil {
		t.Fatalf("re-mint canonical storage_key: %v", err)
	}
	return target
}

// TestR26_TwoIncarnationsCoexistAcrossEveryDurableSurface is the load-bearing
// gate: candidate, discovery projection, queue and pending rows must each hold
// P1 and P2 as SEPARATE rows, and settling P1 must leave every P2 row standing.
func TestR26_TwoIncarnationsCoexistAcrossEveryDurableSurface(t *testing.T) {
	requireCassandra(t)
	gate := r26RequireEvidence(t)

	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	orgID := uuid.New()
	blockID := fmt.Sprintf("r26-coexist-%d", time.Now().UnixNano())
	t.Cleanup(func() { cleanupGCBlockRowsForTest(t, orgID, blockID) })

	// P1 becomes canonical and a zero-ref decision produces its candidate.
	target1 := seedCanonicalBlockRowForTest(t, database, orgID, blockID, "hot")
	p1At := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Millisecond)
	p1, err := store.EnsureBlockGCCandidateExact(orgID, blockID, "hot", p1At)
	if err != nil {
		t.Fatalf("EnsureBlockGCCandidateExact(P1): %v", err)
	}
	if p1.Target != target1 {
		t.Fatalf("P1 candidate captured %s, want %s", p1.Target, target1)
	}
	if err := enqueueExactBlockCandidateForTest(store, p1, p1.CandidateAt); err != nil {
		t.Fatalf("enqueue P1: %v", err)
	}

	// P1 dies, P2 is installed on the same logical block, and a SEPARATE zero-ref
	// decision produces P2's own candidate.
	target2 := r26RemintCanonicalBlock(t, database, orgID, blockID)
	if target2 == target1 {
		t.Fatal("the re-mint reused P1's storage key; the fixture proves nothing")
	}
	p2At := p1At.Add(time.Hour)
	p2, err := store.EnsureBlockGCCandidateExact(orgID, blockID, "hot", p2At)
	if err != nil {
		t.Fatalf("EnsureBlockGCCandidateExact(P2): %v", err)
	}
	if p2.Target != target2 {
		t.Fatalf("P2 candidate captured %s, want %s", p2.Target, target2)
	}
	if err := enqueueExactBlockCandidateForTest(store, p2, p2.CandidateAt); err != nil {
		t.Fatalf("enqueue P2: %v", err)
	}

	// THE CANONICAL CANDIDATE. Under the old single-row key, P2's INSERT ... IF
	// NOT EXISTS would have collided with P1's row and the code would have had to
	// choose which life owned it. Both must now exist independently.
	for _, candidate := range []gcpkg.BlockGCCandidateInfo{p1, p2} {
		got, ok, err := store.GetBlockGCCandidateExact(orgID, blockID, candidate.Identity())
		if err != nil || !ok {
			t.Fatalf("R26 REGRESSION: canonical candidate %s missing: ok=%v err=%v", candidate.Target, ok, err)
		}
		if got.Target != candidate.Target || !got.CandidateAt.Equal(candidate.CandidateAt) {
			t.Fatalf("R26 REGRESSION: candidate %s read back as %s/%v", candidate.Target, got.Target, got.CandidateAt)
		}
	}

	// THE DISCOVERY PROJECTION. P must be part of the row's identity, not a
	// payload column, or the second Ensure overwrites the first's row.
	if got := r26ProjectedTargets(t, store, orgID, blockID, p1.CandidateAt, p2.CandidateAt); len(got) != 2 {
		t.Fatalf("R26 REGRESSION: discovery rows for one logical block = %v, want P1 and P2 as separate rows", got)
	}

	// THE QUEUE AND PENDING ROWS. Two work items, one logical block.
	for _, candidate := range []gcpkg.BlockGCCandidateInfo{p1, p2} {
		exists, err := store.QueueItemExists(orgID, candidate.CandidateAt, gcpkg.ItemBlock, blockID, candidate.ItemIdentity())
		if err != nil || !exists {
			t.Fatalf("R26 REGRESSION: queue row for %s missing: exists=%v err=%v", candidate.Target, exists, err)
		}
		exists, err = store.PendingItemExists(orgID, uuid.Nil, gcpkg.ItemBlock, blockID, candidate.ItemIdentity())
		if err != nil || !exists {
			t.Fatalf("R26 REGRESSION: pending row for %s missing: exists=%v err=%v", candidate.Target, exists, err)
		}
	}

	// SETTLING P1 TOUCHES ONLY P1. This is the defect the slice exists to remove:
	// a delayed P1 lifecycle must not consume the candidate, the discoverability
	// or the work item that belong to P2.
	if err := store.DeleteBlockGCCandidate(orgID, blockID, p1.Identity()); err != nil {
		t.Fatalf("settle P1 candidate: %v", err)
	}
	if err := store.CompleteItem(orgID, p1.CandidateAt, gcpkg.ItemBlock, blockID, p1.ItemIdentity()); err != nil {
		t.Fatalf("complete P1 queue item: %v", err)
	}

	if _, ok, err := store.GetBlockGCCandidateExact(orgID, blockID, p1.Identity()); err != nil || ok {
		t.Fatalf("P1 candidate after settlement: ok=%v err=%v, want removed", ok, err)
	}
	if _, ok, err := store.GetBlockGCCandidateExact(orgID, blockID, p2.Identity()); err != nil || !ok {
		t.Fatalf("R26 REGRESSION: settling P1 consumed P2's candidate: ok=%v err=%v", ok, err)
	}
	got := r26ProjectedTargets(t, store, orgID, blockID, p1.CandidateAt, p2.CandidateAt)
	if len(got) != 1 || got[0] != p2.Target {
		t.Fatalf("R26 REGRESSION: discovery rows after settling P1 = %v, want only P2", got)
	}
	if exists, err := store.QueueItemExists(orgID, p1.CandidateAt, gcpkg.ItemBlock, blockID, p1.ItemIdentity()); err != nil || exists {
		t.Fatalf("P1 queue row after completion: exists=%v err=%v, want removed", exists, err)
	}
	if exists, err := store.QueueItemExists(orgID, p2.CandidateAt, gcpkg.ItemBlock, blockID, p2.ItemIdentity()); err != nil || !exists {
		t.Fatalf("R26 REGRESSION: completing P1 removed P2's queue row: exists=%v err=%v", exists, err)
	}
	if exists, err := store.PendingItemExists(orgID, uuid.Nil, gcpkg.ItemBlock, blockID, p1.ItemIdentity()); err != nil || exists {
		t.Fatalf("P1 pending row after completion: exists=%v err=%v, want removed", exists, err)
	}
	if exists, err := store.PendingItemExists(orgID, uuid.Nil, gcpkg.ItemBlock, blockID, p2.ItemIdentity()); err != nil || !exists {
		t.Fatalf("R26 REGRESSION: completing P1 removed P2's pending row: exists=%v err=%v", exists, err)
	}
	gate.observed = true
}

// TestR26_StaleDiscoveryRowIsRetiredByItsOwnIdentity proves the self-heal against
// the engine: a projection row whose canonical candidate is gone can be removed
// by naming its exact identity, and doing so cannot reach a sibling incarnation's
// row. Under the old L-keyed projection neither half was expressible.
func TestR26_StaleDiscoveryRowIsRetiredByItsOwnIdentity(t *testing.T) {
	requireCassandra(t)
	gate := r26RequireEvidence(t)

	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	orgID := uuid.New()
	blockID := fmt.Sprintf("r26-stale-%d", time.Now().UnixNano())
	t.Cleanup(func() { cleanupGCBlockRowsForTest(t, orgID, blockID) })

	seedCanonicalBlockRowForTest(t, database, orgID, blockID, "hot")
	p1At := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Millisecond)
	p1, err := store.EnsureBlockGCCandidateExact(orgID, blockID, "hot", p1At)
	if err != nil {
		t.Fatalf("EnsureBlockGCCandidateExact(P1): %v", err)
	}
	r26RemintCanonicalBlock(t, database, orgID, blockID)
	p2, err := store.EnsureBlockGCCandidateExact(orgID, blockID, "hot", p1At.Add(time.Hour))
	if err != nil {
		t.Fatalf("EnsureBlockGCCandidateExact(P2): %v", err)
	}

	// Leave P1 in the shape a partially-applied settlement produces: canonical
	// gone, discovery row standing. Without an exact retire this is the state that
	// regenerates the same work item on every scan, forever.
	if err := database.Session().Query(
		`DELETE FROM gc_block_candidates WHERE org_id = ? AND block_id = ? AND storage_class = ? AND storage_key = ?`,
		orgID.String(), blockID, p1.Target.StorageClass, p1.Target.StorageKey,
	).Exec(); err != nil {
		t.Fatalf("drop P1 canonical candidate: %v", err)
	}
	if got := r26ProjectedTargets(t, store, orgID, blockID, p1.CandidateAt, p2.CandidateAt); len(got) != 2 {
		t.Fatalf("precondition: discovery rows = %v, want the stale P1 row still standing", got)
	}

	if err := store.DeleteBlockGCCandidateDiscovery(orgID, blockID, p1.Identity()); err != nil {
		t.Fatalf("retire the stale discovery row: %v", err)
	}
	got := r26ProjectedTargets(t, store, orgID, blockID, p1.CandidateAt, p2.CandidateAt)
	if len(got) != 1 || got[0] != p2.Target {
		t.Fatalf("R26 REGRESSION: discovery rows after retiring P1 = %v, want only P2: the retire must remove exactly one row and never a sibling's", got)
	}
	if _, ok, err := store.GetBlockGCCandidateExact(orgID, blockID, p2.Identity()); err != nil || !ok {
		t.Fatalf("R26 REGRESSION: retiring a discovery row touched a canonical candidate: ok=%v err=%v", ok, err)
	}
	gate.observed = true
}

// TestR26_NonBlockDLQSelectorNamesIdentityAt proves the DLQ half against the
// engine: identity_at is a clustering column of gc_failed_items for every item
// type, so an admin mutation that names it addresses exactly one lifecycle.
func TestR26_NonBlockDLQSelectorNamesIdentityAt(t *testing.T) {
	requireCassandra(t)
	gate := r26RequireEvidence(t)

	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	orgID := uuid.New()
	itemID := uuid.New().String()
	libraryID := uuid.New()
	failedAt := time.Now().UTC().Truncate(time.Millisecond)
	expiresAt := failedAt.Add(24 * time.Hour)

	first := failedAt.Add(-2 * time.Hour)
	second := failedAt.Add(-time.Hour)
	t.Cleanup(func() {
		for _, identityAt := range []time.Time{first, second} {
			_ = database.Session().Query(
				`DELETE FROM gc_failed_items WHERE org_id = ? AND failed_at = ? AND item_type = ? AND item_id = ?
					AND candidate_storage_class = ? AND candidate_storage_key = ? AND identity_at = ?`,
				orgID.String(), failedAt, string(gcpkg.ItemLibraryCascade), itemID, "", "", identityAt,
			).Exec()
		}
	})

	// Two lifecycles that agree on everything the pre-R26 selector matched on and
	// differ only in identity_at.
	for _, identityAt := range []time.Time{first, second} {
		if err := database.Session().Query(
			`INSERT INTO gc_failed_items (org_id, failed_at, expires_at, queued_at, identity_at,
				requires_library_deleted_check, library_guard_mode, item_type, item_id, library_id,
				block_representation_id, storage_class, candidate_storage_class, candidate_storage_key,
				retry_count, last_error, failure_code, resolution_status)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			orgID.String(), failedAt, expiresAt, identityAt, identityAt,
			false, string(gcpkg.LibraryGuardNone), string(gcpkg.ItemLibraryCascade), itemID, libraryID.String(),
			"", "hot", "", "", 0, "seeded", "seeded", "open",
		).Exec(); err != nil {
			t.Fatalf("seed DLQ row %v: %v", identityAt, err)
		}
	}

	if err := store.DeleteFailedItem(orgID, failedAt, gcpkg.ItemLibraryCascade, itemID, gcpkg.GCItemIdentityAt(first)); err != nil {
		t.Fatalf("DeleteFailedItem(first lifecycle): %v", err)
	}

	survivors := r26FailedIdentityAts(t, database, orgID, failedAt, string(gcpkg.ItemLibraryCascade), itemID)
	if len(survivors) != 1 {
		t.Fatalf("R26 REGRESSION: DLQ rows after deleting one lifecycle = %v, want exactly one survivor", survivors)
	}
	if !survivors[0].Equal(second) {
		t.Fatalf("R26 REGRESSION: the delete hit the wrong lifecycle: survivor identity_at = %v, want %v", survivors[0], second)
	}
	gate.observed = true
}

// r26ProjectedTargets returns every discovery row published for one logical block
// across the days the given candidate timestamps fall in.
func r26ProjectedTargets(t *testing.T, store *gcpkg.CassandraStore, orgID uuid.UUID, blockID string, days ...time.Time) []gcpkg.BlockDeleteTarget {
	t.Helper()
	bucket := dbpkg.GCDiscoveryBucket(orgID.String(), blockID)
	seen := map[gcpkg.BlockDeleteTarget]bool{}
	var targets []gcpkg.BlockDeleteTarget
	for _, day := range days {
		rows, err := store.ListBlockGCCandidatesByDay(day, bucket)
		if err != nil {
			t.Fatalf("ListBlockGCCandidatesByDay(%v): %v", day, err)
		}
		for _, row := range rows {
			if row.OrgID != orgID || row.BlockID != blockID || seen[row.Target] {
				continue
			}
			seen[row.Target] = true
			targets = append(targets, row.Target)
		}
	}
	return targets
}

func r26FailedIdentityAts(t *testing.T, database *dbpkg.DB, orgID uuid.UUID, failedAt time.Time, itemType, itemID string) []time.Time {
	t.Helper()
	iter := database.Session().Query(
		`SELECT identity_at FROM gc_failed_items WHERE org_id = ? AND failed_at = ? AND item_type = ? AND item_id = ?`,
		orgID.String(), failedAt, itemType, itemID,
	).Iter()
	var out []time.Time
	var identityAt time.Time
	for iter.Scan(&identityAt) {
		out = append(out, identityAt)
	}
	if err := iter.Close(); err != nil {
		t.Fatalf("read back DLQ lifecycles: %v", err)
	}
	return out
}

// TestR26_SameIdentityAtDistinctPRemainIndependent is the load-bearing half of
// the coexistence evidence, and it exists because the test above is not.
//
// TestR26_TwoIncarnationsCoexistAcrossEveryDurableSurface gives P1 and P2
// different timestamps, which is realistic but weak: three of the four keys carry
// a timestamp of their own —
//
//	gc_block_candidates_by_day  (…, candidate_at, …, storage_class, storage_key)
//	gc_queue                    (…, queued_at, …, candidate_storage_*, identity_at)
//	gc_pending_items            (…, candidate_storage_*, identity_at)
//
// — so with T1 != T2 those tables hold two rows whether or not P is part of the
// key. That test would stay green through a migration that dropped P from all
// three. Only gc_block_candidates, whose key is ((org_id, block_id),
// storage_class, storage_key) with no timestamp at all, is load-bearing there.
//
// So this one pins every timestamp: same candidate_at, same identity_at, same
// queued_at, one logical block. The ONLY thing Cassandra can use to keep the two
// lifecycles apart is P. If P leaves any of these primary keys, the second write
// overwrites the first and the row counts collapse here.
func TestR26_SameIdentityAtDistinctPRemainIndependent(t *testing.T) {
	requireCassandra(t)
	gate := r26RequireEvidence(t)

	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	orgID := uuid.New()
	blockID := fmt.Sprintf("r26-same-t-%d", time.Now().UnixNano())
	t.Cleanup(func() { cleanupGCBlockRowsForTest(t, orgID, blockID) })

	// ONE instant, used for candidate_at, identity_at and queued_at on both lives.
	at := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Millisecond)

	target1 := seedCanonicalBlockRowForTest(t, database, orgID, blockID, "hot")
	p1, err := store.EnsureBlockGCCandidateExact(orgID, blockID, "hot", at)
	if err != nil {
		t.Fatalf("EnsureBlockGCCandidateExact(P1): %v", err)
	}

	target2 := r26RemintCanonicalBlock(t, database, orgID, blockID)
	if target2 == target1 {
		t.Fatal("the re-mint reused P1's storage key; the fixture proves nothing")
	}
	p2, err := store.EnsureBlockGCCandidateExact(orgID, blockID, "hot", at)
	if err != nil {
		t.Fatalf("EnsureBlockGCCandidateExact(P2): %v", err)
	}

	// The fixture's whole point: everything except P is identical.
	if !p1.CandidateAt.Equal(p2.CandidateAt) {
		t.Fatalf("fixture is not load-bearing: candidate_at differs (%v vs %v), so a timestamp could separate the rows instead of P",
			p1.CandidateAt, p2.CandidateAt)
	}
	if p1.Target == p2.Target {
		t.Fatal("fixture is not load-bearing: both candidates carry the same P")
	}

	for _, candidate := range []gcpkg.BlockGCCandidateInfo{p1, p2} {
		if err := enqueueExactBlockCandidateForTest(store, candidate, at); err != nil {
			t.Fatalf("enqueue %s: %v", candidate.Target, err)
		}
	}

	// Four surfaces, two rows each, separated by nothing but P.
	for _, candidate := range []gcpkg.BlockGCCandidateInfo{p1, p2} {
		if _, ok, err := store.GetBlockGCCandidateExact(orgID, blockID, candidate.Identity()); err != nil || !ok {
			t.Fatalf("R26 REGRESSION: canonical candidate %s missing: ok=%v err=%v", candidate.Target, ok, err)
		}
		if exists, err := store.QueueItemExists(orgID, at, gcpkg.ItemBlock, blockID, candidate.ItemIdentity()); err != nil || !exists {
			t.Fatalf("R26 REGRESSION: queue row for %s missing at a shared queued_at/identity_at: exists=%v err=%v — P is not separating the rows", candidate.Target, exists, err)
		}
		if exists, err := store.PendingItemExists(orgID, uuid.Nil, gcpkg.ItemBlock, blockID, candidate.ItemIdentity()); err != nil || !exists {
			t.Fatalf("R26 REGRESSION: pending row for %s missing at a shared identity_at: exists=%v err=%v — P is not separating the rows", candidate.Target, exists, err)
		}
	}
	if got := r26ProjectedTargets(t, store, orgID, blockID, at); len(got) != 2 {
		t.Fatalf("R26 REGRESSION: discovery rows at a shared candidate_at = %v, want P1 and P2 as separate rows — P is not part of the projection key", got)
	}

	// Settling P1 must leave every P2 row standing, with no timestamp available to
	// tell the statements apart.
	if err := store.DeleteBlockGCCandidate(orgID, blockID, p1.Identity()); err != nil {
		t.Fatalf("settle P1 candidate: %v", err)
	}
	if err := store.CompleteItem(orgID, at, gcpkg.ItemBlock, blockID, p1.ItemIdentity()); err != nil {
		t.Fatalf("complete P1 queue item: %v", err)
	}

	if _, ok, err := store.GetBlockGCCandidateExact(orgID, blockID, p1.Identity()); err != nil || ok {
		t.Fatalf("P1 candidate after settlement: ok=%v err=%v, want removed", ok, err)
	}
	if _, ok, err := store.GetBlockGCCandidateExact(orgID, blockID, p2.Identity()); err != nil || !ok {
		t.Fatalf("R26 REGRESSION: settling P1 consumed P2's candidate at a shared candidate_at: ok=%v err=%v", ok, err)
	}
	got := r26ProjectedTargets(t, store, orgID, blockID, at)
	if len(got) != 1 || got[0] != p2.Target {
		t.Fatalf("R26 REGRESSION: discovery rows after settling P1 = %v, want only P2", got)
	}
	if exists, err := store.QueueItemExists(orgID, at, gcpkg.ItemBlock, blockID, p1.ItemIdentity()); err != nil || exists {
		t.Fatalf("P1 queue row after completion: exists=%v err=%v, want removed", exists, err)
	}
	if exists, err := store.QueueItemExists(orgID, at, gcpkg.ItemBlock, blockID, p2.ItemIdentity()); err != nil || !exists {
		t.Fatalf("R26 REGRESSION: completing P1 removed P2's queue row at a shared queued_at: exists=%v err=%v", exists, err)
	}
	if exists, err := store.PendingItemExists(orgID, uuid.Nil, gcpkg.ItemBlock, blockID, p1.ItemIdentity()); err != nil || exists {
		t.Fatalf("P1 pending row after completion: exists=%v err=%v, want removed", exists, err)
	}
	if exists, err := store.PendingItemExists(orgID, uuid.Nil, gcpkg.ItemBlock, blockID, p2.ItemIdentity()); err != nil || !exists {
		t.Fatalf("R26 REGRESSION: completing P1 removed P2's pending row at a shared identity_at: exists=%v err=%v", exists, err)
	}
	gate.observed = true
}
