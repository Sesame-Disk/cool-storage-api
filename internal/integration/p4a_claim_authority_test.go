//go:build integration

package integration

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	"github.com/google/uuid"
)

// P4a evidence against REAL Cassandra.
//
// The unit suite proves the classifier; it cannot prove that Cassandra's actual CAS
// return semantics deliver what the classifier needs. That gap is not theoretical — R19
// is the standing example of a defect the whole mock suite agreed with while production
// carried it, because the mock could not reach the state the engine could.
//
// Three things need the real engine here:
//
//   - a non-applied LWT must return the row's CURRENT values, which is what lets the
//     claim classify fresh/stale/different-incarnation without a second read;
//   - `IF storage_class = ?` must not apply against a missing partition, which is what
//     removes stub materialization rather than cleaning up after it;
//   - `Consistency(gocql.Serial)` must be accepted on an ordinary SELECT, which is the
//     settling read R20 requires.
//
// MinIO is deliberately not involved: nothing here deletes bytes.

const p4aRequireEvidenceEnv = "SESAMEFS_REQUIRE_P4A_EVIDENCE"

type p4aEvidenceGate struct{ observed bool }

// p4aRequireEvidence makes the gate non-vacuous. Without it these tests can skip their
// way to exit 0 and print "ok" while proving nothing about a stack that never came up.
func p4aRequireEvidence(t *testing.T) *p4aEvidenceGate {
	t.Helper()
	gate := &p4aEvidenceGate{}
	if os.Getenv(p4aRequireEvidenceEnv) != "1" {
		return gate
	}
	t.Cleanup(func() {
		if t.Skipped() {
			t.Errorf("%s=1 requires real Cassandra P4a evidence, but the test skipped", p4aRequireEvidenceEnv)
		} else if !t.Failed() && !gate.observed {
			t.Errorf("%s=1 completed without claim-authority evidence", p4aRequireEvidenceEnv)
		}
	})
	return gate
}

func p4aSeedCandidate(t *testing.T, orgID uuid.UUID, blockID string) (gcpkg.BlockGCCandidateInfo, gcpkg.BlockDeleteTarget) {
	t.Helper()
	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	target := seedCanonicalBlockRowForTest(t, database, orgID, blockID, "hot")
	candidateAt, err := store.EnsureBlockGCCandidateExact(orgID, blockID, "hot", time.Now().UTC().Truncate(time.Millisecond))
	if err != nil {
		t.Fatalf("EnsureBlockGCCandidateExact readback: %v", err)
	}
	candidate, ok, err := store.GetBlockGCCandidateExact(orgID, blockID, candidateAt.Identity())
	if err != nil || !ok {
		t.Fatalf("GetBlockGCCandidate: ok=%v err=%v", ok, err)
	}
	if candidate.Target != target {
		t.Fatalf("candidate captured %s, want the canonical incarnation %s", candidate.Target, target)
	}
	return candidate, target
}

// TestP4A_ClaimOwnershipIsExclusiveAndTakeoverIsExact is the ownership leg.
//
// Two attempts interleave on ONE incarnation; exactly one may own the row. The loser
// must not be able to release or finalize. Then the winner's claim ages out, the loser
// takes it over by CAS against the winner's exact authority, and the winner — now awake
// and still believing it owns the row — must be refused on both of its transitions.
func TestP4A_ClaimOwnershipIsExclusiveAndTakeoverIsExact(t *testing.T) {
	requireCassandra(t)
	gate := p4aRequireEvidence(t)

	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	orgID := uuid.New()
	blockID := fmt.Sprintf("p4a-own-%d", time.Now().UnixNano())
	t.Cleanup(func() { cleanupGCBlockRowsForTest(t, orgID, blockID) })

	candidate, target := p4aSeedCandidate(t, orgID, blockID)

	now := time.Now().UTC().Truncate(time.Millisecond)
	attemptA := gcpkg.BlockDeleteAuthority{Target: target, ClaimID: uuid.NewString(), ClaimedAt: now}
	attemptB := gcpkg.BlockDeleteAuthority{Target: target, ClaimID: uuid.NewString(), ClaimedAt: now}

	if claim, err := store.ClaimBlockDelete(orgID, blockID, attemptA); err != nil || claim.Outcome != gcpkg.BlockClaimAcquired {
		t.Fatalf("A claim = %s, %v; want acquired", claim.Outcome, err)
	}

	// B loses. The engine must return the CURRENT values so the loser can tell "a live
	// attempt owns this" from "the row is gone" without a second, non-serial read.
	claim, err := store.ClaimBlockDelete(orgID, blockID, attemptB)
	if err != nil {
		t.Fatalf("B claim: %v", err)
	}
	if claim.Outcome != gcpkg.BlockClaimFreshOwner {
		t.Fatalf("P4A REGRESSION: B claim = %s, want fresh_owner; two attempts must not both own one incarnation", claim.Outcome)
	}

	// The loser holds no fence and may not touch the winner's row.
	if released, err := store.ReleaseBlockClaim(orgID, blockID, attemptB); err != nil || released != gcpkg.BlockReleaseNotOwner {
		t.Fatalf("P4A REGRESSION: B release = %s, %v; the loser dropped the winner's fence", released, err)
	}
	if _, err := store.FinalizeBlockDelete(orgID, blockID, gcpkg.CommittedBlockDeleteAuthority{BlockDeleteAuthority: attemptB}); err == nil {
		t.Fatal("P4A REGRESSION: B finalized a delete it never owned; the canonical row would vanish under A")
	}
	if exists, err := store.BlockExists(orgID, blockID); err != nil || !exists {
		t.Fatalf("canonical row gone after a non-owner finalize (exists=%v err=%v)", exists, err)
	}

	// Age A's claim, then take it over. The release names A's exact authority.
	if err := database.Session().Query(
		`UPDATE blocks SET gc_claimed_at = ? WHERE org_id = ? AND block_id = ?`,
		now.Add(-2*time.Hour), orgID.String(), blockID,
	).Exec(); err != nil {
		t.Fatalf("age A's claim: %v", err)
	}
	// The takeover names the authority a claim attempt OBSERVED, and it has to be
	// re-observed AFTER the ageing above: gc_claimed_at is part of the identity, so the
	// value A originally wrote is no longer what owns the row. That is the whole reason a
	// takeover must use what it saw rather than anything it remembers — and why
	// re-reading and releasing "the current stale owner" is a different operation, since
	// between two reads the row can be a different incarnation entirely.
	attemptC := gcpkg.BlockDeleteAuthority{Target: target, ClaimID: uuid.NewString(), ClaimedAt: time.Now().UTC().Truncate(time.Millisecond)}
	observed, err := store.ClaimBlockDelete(orgID, blockID, attemptC)
	if err != nil || observed.Outcome != gcpkg.BlockClaimStaleOwner {
		t.Fatalf("C observing the abandoned claim = %s, %v; want stale_owner", observed.Outcome, err)
	}
	if observed.Owner.ClaimID != attemptA.ClaimID {
		t.Fatalf("the claim reported owner %q, want the abandoned attempt %q; without the observed owner a takeover cannot be exact", observed.Owner.ClaimID, attemptA.ClaimID)
	}
	if released, err := store.ReleaseBlockClaim(orgID, blockID, observed.Owner); err != nil || released != gcpkg.BlockReleaseReleased {
		t.Fatalf("stale takeover = %s, %v; want released", released, err)
	}
	if claim, err := store.ClaimBlockDelete(orgID, blockID, attemptC); err != nil || claim.Outcome != gcpkg.BlockClaimAcquired {
		t.Fatalf("C claim after takeover = %s, %v; want acquired", claim.Outcome, err)
	}

	// A wakes up late. Both transitions must be refused against the real CAS.
	if released, err := store.ReleaseBlockClaim(orgID, blockID, attemptA); err != nil || released != gcpkg.BlockReleaseNotOwner {
		t.Fatalf("P4A REGRESSION: A release after takeover = %s, %v; A dropped C's fence", released, err)
	}
	if _, err := store.FinalizeBlockDelete(orgID, blockID, gcpkg.CommittedBlockDeleteAuthority{BlockDeleteAuthority: attemptA}); err == nil {
		t.Fatal("P4A REGRESSION: A finalized after being taken over; C's delete would lose its canonical row mid-flight")
	}
	if exists, err := store.BlockExists(orgID, blockID); err != nil || !exists {
		t.Fatalf("canonical row gone after a taken-over attempt finalized (exists=%v err=%v)", exists, err)
	}

	// The candidate survived every refusal — nothing settled it on a lost race.
	// Named by its exact identity: with exact-P a logical block can hold several
	// candidates, so "some candidate exists" would not be the assertion meant here.
	if _, ok, err := store.GetBlockGCCandidateExact(orgID, blockID, candidate.Identity()); err != nil || !ok {
		t.Fatalf("P4A REGRESSION: the candidate was consumed by a losing attempt (ok=%v err=%v)", ok, err)
	}
	if released, err := store.ReleaseBlockClaim(orgID, blockID, attemptC); err != nil || released != gcpkg.BlockReleaseReleased {
		t.Fatalf("cleanup release = %s, %v", released, err)
	}

	gate.observed = true
	t.Logf("P4A_CLAIM_AUTHORITY_EVIDENCE owners=1 refused_releases=2 refused_finalizes=2 takeovers=1")
}

// TestP4A_StaleIncarnationCannotActOnTheLiveOne is the ABA leg — R14 itself.
//
// A candidate is created for P1. P1 dies and P2 is installed on the same logical block.
// The delayed lifecycle must not claim, release, finalize or clear anything belonging to
// P2, and it must not consume P2's candidate on its way out.
func TestP4A_StaleIncarnationCannotActOnTheLiveOne(t *testing.T) {
	requireCassandra(t)
	gate := p4aRequireEvidence(t)

	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	orgID := uuid.New()
	blockID := fmt.Sprintf("p4a-aba-%d", time.Now().UnixNano())
	t.Cleanup(func() { cleanupGCBlockRowsForTest(t, orgID, blockID) })

	staleCandidate, p1 := p4aSeedCandidate(t, orgID, blockID)

	// P1 dies; P2 is minted onto the same logical block. Only the key changes — a
	// re-mint keeps the storage class, which is why class alone cannot identify a life.
	p2 := gcpkg.BlockDeleteTarget{StorageClass: p1.StorageClass, StorageKey: p1.StorageKey + ".remint"}
	if err := database.Session().Query(
		`UPDATE blocks SET storage_key = ? WHERE org_id = ? AND block_id = ?`,
		p2.StorageKey, orgID.String(), blockID,
	).Exec(); err != nil {
		t.Fatalf("install P2: %v", err)
	}

	stale := gcpkg.BlockDeleteAuthority{Target: p1, ClaimID: uuid.NewString(), ClaimedAt: time.Now().UTC().Truncate(time.Millisecond)}
	claim, err := store.ClaimBlockDelete(orgID, blockID, stale)
	if err != nil {
		t.Fatalf("stale claim: %v", err)
	}
	if claim.Outcome != gcpkg.BlockClaimTargetChanged {
		t.Fatalf("P4A REGRESSION: a candidate for a dead incarnation claimed %s against the live one; want target_changed", claim.Outcome)
	}

	var gcState, gcClaimID string
	if err := database.Session().Query(
		`SELECT gc_state, gc_claim_id FROM blocks WHERE org_id = ? AND block_id = ?`,
		orgID.String(), blockID,
	).Scan(&gcState, &gcClaimID); err != nil {
		t.Fatalf("read P2 after the refused claim: %v", err)
	}
	if gcState != "" || gcClaimID != "" {
		t.Fatalf("P4A REGRESSION: the refused claim still fenced P2 (gc_state=%q gc_claim_id=%q)", gcState, gcClaimID)
	}

	// Neither post-claim transition may reach P2 either.
	if released, err := store.ReleaseBlockClaim(orgID, blockID, stale); err != nil || released != gcpkg.BlockReleaseNotOwner {
		t.Fatalf("P4A REGRESSION: stale release = %s, %v; want not_owner", released, err)
	}
	if _, err := store.FinalizeBlockDelete(orgID, blockID, gcpkg.CommittedBlockDeleteAuthority{BlockDeleteAuthority: stale}); err == nil {
		t.Fatal("P4A REGRESSION: a dead incarnation's authority finalized the delete of the live one")
	}
	if exists, err := store.BlockExists(orgID, blockID); err != nil || !exists {
		t.Fatalf("P4A REGRESSION: P2's canonical row was deleted on P1's authority (exists=%v err=%v)", exists, err)
	}

	// A zero-ref decision is then taken for P2, so the candidate follows the live
	// incarnation. P2 must get its OWN candidate_at rather than inheriting P1's:
	// inheriting it would hand the new life an artificially old timestamp and let it
	// skip the grace period that lets in-flight writers finish.
	freshCandidate, err := store.EnsureBlockGCCandidateExact(orgID, blockID, "hot", time.Now().UTC().Truncate(time.Millisecond))
	if err != nil {
		t.Fatalf("EnsureBlockGCCandidate for P2: %v", err)
	}
	fresh, ok, err := store.GetBlockGCCandidateExact(orgID, blockID, freshCandidate.Identity())
	if err != nil || !ok {
		t.Fatalf("GetBlockGCCandidate(P2): ok=%v err=%v", ok, err)
	}
	if fresh.Target != p2 {
		t.Fatalf("the candidate did not follow the live incarnation: got %s, want %s", fresh.Target, p2)
	}
	if !fresh.CandidateAt.After(staleCandidate.CandidateAt) {
		t.Fatalf("P4A REGRESSION: P2 inherited candidate_at %v from P1; the new incarnation would skip its grace period", fresh.CandidateAt)
	}

	// The delayed P1 lifecycle now finishes. Its candidate cleanup must be a no-op:
	// consuming P2's work item would destroy the only thing able to reclaim P2, with no
	// fence left behind to make the loss visible.
	if err := store.DeleteBlockGCCandidate(orgID, blockID, staleCandidate.Identity()); err != nil {
		t.Fatalf("stale candidate cleanup must be a safe no-op, got: %v", err)
	}
	surviving, ok, err := store.GetBlockGCCandidateExact(orgID, blockID, freshCandidate.Identity())
	if err != nil || !ok {
		t.Fatalf("P4A REGRESSION: a stale lifecycle consumed the live incarnation's candidate (ok=%v err=%v)", ok, err)
	}
	if surviving.Target != p2 {
		t.Fatalf("P4A REGRESSION: the surviving candidate is %s, want the live incarnation %s", surviving.Target, p2)
	}

	gate.observed = true
	t.Logf("P4A_ABA_EVIDENCE p1_key=%q p2_key=%q refused=claim,release,finalize,candidate_delete", p1.StorageKey, p2.StorageKey)
}

// TestP4A_RetryOfTheSameCandidateUnderRealCAS closes the coverage gap recorded as
// technical debt: "the current tree still lacks a real Cassandra regression that proves a
// retry of the same logical candidate recognizes its prior claim under Cassandra's actual
// CAS return semantics."
//
// The answer changed with P4a and is worth pinning explicitly. A retry is a NEW attempt
// with a new id, so it does NOT inherit the previous attempt's claim: it observes a live
// owner and postpones. Recovery is by staleness, not by identity — which is what makes it
// work even when the previous attempt never comes back.
func TestP4A_RetryOfTheSameCandidateUnderRealCAS(t *testing.T) {
	requireCassandra(t)
	gate := p4aRequireEvidence(t)

	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	orgID := uuid.New()
	blockID := fmt.Sprintf("p4a-retry-%d", time.Now().UnixNano())
	t.Cleanup(func() { cleanupGCBlockRowsForTest(t, orgID, blockID) })

	_, target := p4aSeedCandidate(t, orgID, blockID)

	now := time.Now().UTC().Truncate(time.Millisecond)
	first := gcpkg.BlockDeleteAuthority{Target: target, ClaimID: uuid.NewString(), ClaimedAt: now}
	if claim, err := store.ClaimBlockDelete(orgID, blockID, first); err != nil || claim.Outcome != gcpkg.BlockClaimAcquired {
		t.Fatalf("first claim = %s, %v", claim.Outcome, err)
	}

	// The same logical candidate, retried. A fresh attempt id is the whole point.
	retry := gcpkg.BlockDeleteAuthority{Target: target, ClaimID: uuid.NewString(), ClaimedAt: time.Now().UTC().Truncate(time.Millisecond)}
	claim, err := store.ClaimBlockDelete(orgID, blockID, retry)
	if err != nil {
		t.Fatalf("retry claim: %v", err)
	}
	if claim.Outcome != gcpkg.BlockClaimFreshOwner {
		t.Fatalf("retry of the same candidate = %s, want fresh_owner: a retry must not inherit the previous attempt's ownership", claim.Outcome)
	}

	// Age the abandoned claim; the retry then takes it over and acquires under its own
	// identity. This is the recovery path that replaces the old shared-id idempotence.
	if err := database.Session().Query(
		`UPDATE blocks SET gc_claimed_at = ? WHERE org_id = ? AND block_id = ?`,
		now.Add(-2*time.Hour), orgID.String(), blockID,
	).Exec(); err != nil {
		t.Fatalf("age the abandoned claim: %v", err)
	}
	second := gcpkg.BlockDeleteAuthority{Target: target, ClaimID: uuid.NewString(), ClaimedAt: time.Now().UTC().Truncate(time.Millisecond)}
	aged, err := store.ClaimBlockDelete(orgID, blockID, second)
	if err != nil || aged.Outcome != gcpkg.BlockClaimStaleOwner {
		t.Fatalf("observing the aged claim = %s, %v; want stale_owner", aged.Outcome, err)
	}
	if released, err := store.ReleaseBlockClaim(orgID, blockID, aged.Owner); err != nil || released != gcpkg.BlockReleaseReleased {
		t.Fatalf("stale takeover = %s, %v; want released", released, err)
	}
	if claim, err := store.ClaimBlockDelete(orgID, blockID, second); err != nil || claim.Outcome != gcpkg.BlockClaimAcquired {
		t.Fatalf("claim after takeover = %s, %v; an abandoned claim must not fence the block forever", claim.Outcome, err)
	}
	if released, err := store.ReleaseBlockClaim(orgID, blockID, second); err != nil || released != gcpkg.BlockReleaseReleased {
		t.Fatalf("cleanup release = %s, %v", released, err)
	}

	gate.observed = true
	t.Logf("P4A_RETRY_EVIDENCE retry_outcome=fresh_owner recovery=stale_takeover")
}

// TestP4A_StaleClaimReleaseIsBoundToTheIncarnation is the pre-check half of the authority
// contract, against real Cassandra.
//
// The age-based release exists so a fence whose owner will never return can still be
// lifted. That is owner-agnostic by design — but it must not be INCARNATION-agnostic. A
// re-minted block ordinarily ends up holding a different life with its own abandoned
// fence, and a candidate for the previous life has no authority over it. No clock skew or
// unusual interleaving is needed to reach this; it is the plain shape of a re-mint.
func TestP4A_StaleClaimReleaseIsBoundToTheIncarnation(t *testing.T) {
	requireCassandra(t)
	gate := p4aRequireEvidence(t)

	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	orgID := uuid.New()
	blockID := fmt.Sprintf("p4a-precheck-%d", time.Now().UnixNano())
	t.Cleanup(func() { cleanupGCBlockRowsForTest(t, orgID, blockID) })

	_, p1 := p4aSeedCandidate(t, orgID, blockID)

	// The block is re-minted, and a lifecycle for the NEW incarnation fences it and is
	// then abandoned long enough to look stale.
	p2 := gcpkg.BlockDeleteTarget{StorageClass: p1.StorageClass, StorageKey: p1.StorageKey + ".remint"}
	abandoned := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Millisecond)
	if err := database.Session().Query(
		`UPDATE blocks SET storage_key = ?, gc_state = ?, gc_claim_id = ?, gc_claimed_at = ?
		 WHERE org_id = ? AND block_id = ?`,
		p2.StorageKey, "deleting", "p2-lifecycle", abandoned, orgID.String(), blockID,
	).Exec(); err != nil {
		t.Fatalf("install P2 with an abandoned fence: %v", err)
	}

	// A worker authorized only for P1 must leave that fence alone.
	outcome, err := store.ReleaseStaleBlockClaim(orgID, blockID, p1, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("ReleaseStaleBlockClaim: %v", err)
	}
	if outcome == gcpkg.BlockClaimReleased {
		t.Fatal("P4A REGRESSION: a candidate for P1 released a fence belonging to P2; age is not authority over an incarnation this worker was never given")
	}
	var gcState, gcClaimID string
	if err := database.Session().Query(
		`SELECT gc_state, gc_claim_id FROM blocks WHERE org_id = ? AND block_id = ?`,
		orgID.String(), blockID,
	).Scan(&gcState, &gcClaimID); err != nil {
		t.Fatalf("read P2's fence after the refused release: %v", err)
	}
	if gcState != "deleting" || gcClaimID != "p2-lifecycle" {
		t.Fatalf("P4A REGRESSION: P2's fence did not survive a P1 worker's stale release (gc_state=%q gc_claim_id=%q)", gcState, gcClaimID)
	}

	// Correctly named, the same call still does the unwedging it exists for.
	if outcome, err := store.ReleaseStaleBlockClaim(orgID, blockID, p2, time.Now().Add(-time.Hour)); err != nil || outcome != gcpkg.BlockClaimReleased {
		t.Fatalf("stale release naming the right incarnation = %s, %v; want released — binding to P must not cost the liveness this path provides", outcome, err)
	}

	gate.observed = true
	t.Logf("P4A_PRECHECK_EVIDENCE refused_cross_incarnation=1 released_own_incarnation=1")
}

func TestP4A_CanonicalBlockReadRepairIsBlocking(t *testing.T) {
	requireCassandra(t)
	gate := p4aRequireEvidence(t)

	keyspace := envOrDefault("CASSANDRA_KEYSPACE", "sesamefs")
	var readRepair string
	if err := shareProjectionDBForTest(t).Session().Query(`
		SELECT read_repair
		FROM system_schema.tables
		WHERE keyspace_name = ? AND table_name = ?
	`, keyspace, "blocks").Scan(&readRepair); err != nil {
		t.Fatalf("read effective read_repair for blocks: %v", err)
	}
	if strings.ToUpper(strings.TrimSpace(readRepair)) != "BLOCKING" {
		t.Fatalf("blocks effective read_repair = %q, want BLOCKING; empty is not treated as the default. Settled own-claim confirmation relies on blocking read repair at EACH_QUORUM", readRepair)
	}

	gate.observed = true
	t.Logf("P4A_READ_REPAIR_EVIDENCE blocking=1 value=%q", readRepair)
}
