//go:build integration

package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	"github.com/google/uuid"
)

// TestP4B_ClaimOrphanAuthorityIsBoundAtRealCassandra proves the R14b handoff
// against Cassandra's actual CAS maps: CommitBlockDeleteOrphanHandoff is
// irreversible, resume returns the stored D, and orphan publication / finalize
// reject a different claim identity on the same physical incarnation.
func TestP4B_ClaimOrphanAuthorityIsBoundAtRealCassandra(t *testing.T) {
	requireCassandra(t)
	gate := p4bRequireEvidence(t)

	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	orgID := uuid.New()
	blockID := fmt.Sprintf("p4b-authority-%d", time.Now().UnixNano())
	target := seedCanonicalBlockRowForTest(t, database, orgID, blockID, "hot")
	d1 := gcpkg.BlockDeleteAuthority{
		Target:    target,
		ClaimID:   "p4b-d1-" + uuid.NewString(),
		ClaimedAt: time.Now().UTC().Truncate(time.Millisecond),
	}
	d2 := gcpkg.BlockDeleteAuthority{
		Target:    target,
		ClaimID:   "p4b-d2-" + uuid.NewString(),
		ClaimedAt: time.Now().UTC().Truncate(time.Millisecond),
	}

	claim, err := store.ClaimBlockDelete(orgID, blockID, d1)
	if err != nil || claim.Outcome != gcpkg.BlockClaimAcquired {
		t.Fatalf("claim D1 = %s, %v; want acquired", claim.Outcome, err)
	}

	if outcome, relErr := store.ReleaseBlockClaim(orgID, blockID, d1); relErr != nil || outcome != gcpkg.BlockReleaseReleased {
		t.Fatalf("pre-handoff release of D1 = %s, %v; want released (takeover still legal)", outcome, relErr)
	}
	claim, err = store.ClaimBlockDelete(orgID, blockID, d1)
	if err != nil || claim.Outcome != gcpkg.BlockClaimAcquired {
		t.Fatalf("re-claim D1 = %s, %v; want acquired", claim.Outcome, err)
	}

	handoff, err := store.CommitBlockDeleteOrphanHandoff(orgID, blockID, d1)
	if err != nil || (handoff.Outcome != gcpkg.BlockDeleteHandoffCommitted && handoff.Outcome != gcpkg.BlockDeleteHandoffAlreadyCommitted) {
		t.Fatalf("commit handoff = %s, %v; want committed", handoff.Outcome, err)
	}

	if outcome, relErr := store.ReleaseBlockClaim(orgID, blockID, d1); relErr != nil || outcome != gcpkg.BlockReleaseNotOwner {
		t.Fatalf("post-handoff release of D1 = %s, %v; want not_owner", outcome, relErr)
	}
	stale, err := store.ReleaseStaleBlockClaim(orgID, blockID, target, time.Now().UTC().Add(time.Hour))
	if err != nil || stale != gcpkg.BlockClaimCommittedHandoff {
		t.Fatalf("stale release after handoff = %s, %v; want committed_handoff", stale, err)
	}

	resume, err := store.ClaimBlockDelete(orgID, blockID, d2)
	if err != nil || resume.Outcome != gcpkg.BlockClaimCommittedOwner {
		t.Fatalf("claim D2 after handoff = %s, %v; want committed_owner", resume.Outcome, err)
	}
	if resume.Owner.ClaimID != d1.ClaimID {
		t.Fatalf("CommittedOwner resumed %q, want stored D1 %q", resume.Owner.ClaimID, d1.ClaimID)
	}

	committed := gcpkg.CommittedBlockDeleteAuthorityForTest(d1)
	firstSeenAt := time.Now().UTC().Truncate(time.Millisecond)
	created := store.StartBlockDeleteOrphan(orgID, blockID, committed, "sha1-d1", firstSeenAt)
	if created.Outcome != gcpkg.StartBlockDeleteOrphanCreated || !created.FirstSeenAt.Equal(firstSeenAt) {
		t.Fatalf("publish D1 orphan = %s first_seen_at=%v cause=%v, want created at %v", created.Outcome, created.FirstSeenAt, created.Cause, firstSeenAt)
	}
	t.Cleanup(func() {
		if err := store.DeleteS3Orphan(orgID, blockID, created.FirstSeenAt); err != nil {
			t.Logf("cleanup DeleteS3Orphan: %v", err)
		}
	})

	same := store.StartBlockDeleteOrphan(orgID, blockID, committed, "sha1-retry", time.Now().UTC().Add(time.Hour))
	if same.Outcome != gcpkg.StartBlockDeleteOrphanSameAuthority || !same.FirstSeenAt.Equal(created.FirstSeenAt) {
		t.Fatalf("same-authority retry = %s first_seen_at=%v, want same_authority at %v", same.Outcome, same.FirstSeenAt, created.FirstSeenAt)
	}

	other := store.StartBlockDeleteOrphan(orgID, blockID, gcpkg.CommittedBlockDeleteAuthorityForTest(d2), "sha1-d2", time.Now().UTC())
	if other.Outcome != gcpkg.StartBlockDeleteOrphanDifferentAuthority {
		t.Fatalf("same-P different-D = %s, want different_authority", other.Outcome)
	}

	if result, finErr := store.FinalizeBlockDelete(orgID, blockID, gcpkg.CommittedBlockDeleteAuthorityForTest(d2)); finErr == nil || result.Outcome == gcpkg.BlockDeleteFinalized || result.Outcome == gcpkg.BlockDeleteAlreadyFinalized {
		t.Fatalf("finalize with D2 = %+v, %v; want not_authority", result, finErr)
	}
	if result, finErr := store.FinalizeBlockDelete(orgID, blockID, committed); finErr != nil || (result.Outcome != gcpkg.BlockDeleteFinalized && result.Outcome != gcpkg.BlockDeleteAlreadyFinalized) {
		t.Fatalf("finalize with D1 = %+v, %v; want applied", result, finErr)
	}

	gate.observed = true
	t.Logf("P4B_CLAIM_ORPHAN_AUTHORITY_EVIDENCE handoff=1 resume_d1=1 same_authority=1 different_authority=1 finalize_bound=1")
}

func TestP4B_LateLoserCannotCommitHandoffAtRealCassandra(t *testing.T) {
	requireCassandra(t)
	gate := p4bRequireEvidence(t)
	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	orgID := uuid.New()
	blockID := fmt.Sprintf("p4b-late-loser-%d", time.Now().UnixNano())
	target := seedCanonicalBlockRowForTest(t, database, orgID, blockID, "hot")
	d1 := gcpkg.BlockDeleteAuthority{Target: target, ClaimID: "p4b-d1-" + uuid.NewString(), ClaimedAt: time.Now().UTC().Truncate(time.Millisecond)}
	d2 := gcpkg.BlockDeleteAuthority{Target: target, ClaimID: "p4b-d2-" + uuid.NewString(), ClaimedAt: time.Now().UTC().Truncate(time.Millisecond)}

	if claim, err := store.ClaimBlockDelete(orgID, blockID, d1); err != nil || claim.Outcome != gcpkg.BlockClaimAcquired {
		t.Fatalf("claim D1 = %s, %v", claim.Outcome, err)
	}
	if outcome, err := store.ReleaseBlockClaim(orgID, blockID, d1); err != nil || outcome != gcpkg.BlockReleaseReleased {
		t.Fatalf("release D1 = %s, %v", outcome, err)
	}
	if claim, err := store.ClaimBlockDelete(orgID, blockID, d2); err != nil || claim.Outcome != gcpkg.BlockClaimAcquired {
		t.Fatalf("claim D2 = %s, %v", claim.Outcome, err)
	}
	handoff, err := store.CommitBlockDeleteOrphanHandoff(orgID, blockID, d1)
	if handoff.Outcome == gcpkg.BlockDeleteHandoffCommitted || handoff.Outcome == gcpkg.BlockDeleteHandoffAlreadyCommitted {
		t.Fatalf("late D1 handoff = %s, %v; want not_owner", handoff.Outcome, err)
	}
	if handoff.Outcome != gcpkg.BlockDeleteHandoffNotOwner {
		t.Fatalf("late D1 handoff = %s, %v; want not_owner", handoff.Outcome, err)
	}
	if _, found, err := store.GetS3OrphanGlobal(orgID, blockID); err != nil || found {
		t.Fatalf("late loser published an orphan: found=%v err=%v", found, err)
	}
	if result, finErr := store.FinalizeBlockDelete(orgID, blockID, gcpkg.CommittedBlockDeleteAuthorityForTest(d1)); p4bFinalizeAuthorizesPhysicalDelete(result) {
		t.Fatalf("late D1 finalize = %+v, %v; must not apply", result, finErr)
	}
	gate.observed = true
}

func TestP4B_CrashAfterHandoffResumesStoredDAtRealCassandra(t *testing.T) {
	requireCassandra(t)
	gate := p4bRequireEvidence(t)
	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	orgID := uuid.New()
	blockID := fmt.Sprintf("p4b-crash-handoff-%d", time.Now().UnixNano())
	target := seedCanonicalBlockRowForTest(t, database, orgID, blockID, "hot")
	d1 := gcpkg.BlockDeleteAuthority{Target: target, ClaimID: "p4b-d1-" + uuid.NewString(), ClaimedAt: time.Now().UTC().Truncate(time.Millisecond)}
	d2 := gcpkg.BlockDeleteAuthority{Target: target, ClaimID: "p4b-d2-" + uuid.NewString(), ClaimedAt: time.Now().UTC().Truncate(time.Millisecond)}

	if claim, err := store.ClaimBlockDelete(orgID, blockID, d1); err != nil || claim.Outcome != gcpkg.BlockClaimAcquired {
		t.Fatalf("claim D1 = %s, %v", claim.Outcome, err)
	}
	if handoff, err := store.CommitBlockDeleteOrphanHandoff(orgID, blockID, d1); err != nil || (handoff.Outcome != gcpkg.BlockDeleteHandoffCommitted && handoff.Outcome != gcpkg.BlockDeleteHandoffAlreadyCommitted) {
		t.Fatalf("handoff D1 = %s, %v", handoff.Outcome, err)
	}
	if _, found, err := store.GetS3OrphanGlobal(orgID, blockID); err != nil || found {
		t.Fatalf("crash-window orphan present before resume: found=%v err=%v", found, err)
	}

	resume, err := store.ClaimBlockDelete(orgID, blockID, d2)
	if err != nil || resume.Outcome != gcpkg.BlockClaimCommittedOwner || resume.Owner.ClaimID != d1.ClaimID {
		t.Fatalf("resume after crash = %s owner=%q err=%v; want committed_owner D1", resume.Outcome, resume.Owner.ClaimID, err)
	}
	created := store.StartBlockDeleteOrphan(orgID, blockID, gcpkg.CommittedBlockDeleteAuthorityForTest(d1), "sha1", time.Now().UTC())
	if created.Outcome != gcpkg.StartBlockDeleteOrphanCreated {
		t.Fatalf("resume D1 orphan = %s, want created: %v", created.Outcome, created.Cause)
	}
	t.Cleanup(func() {
		_ = store.DeleteS3Orphan(orgID, blockID, created.FirstSeenAt)
	})
	if result, err := store.FinalizeBlockDelete(orgID, blockID, gcpkg.CommittedBlockDeleteAuthorityForTest(d1)); err != nil || !p4bFinalizeAuthorizesPhysicalDelete(result) {
		t.Fatalf("resume finalize D1 = %+v, %v", result, err)
	}
	gate.observed = true
}

func TestP4B_CrashAfterOrphanResumesSameAuthorityAtRealCassandra(t *testing.T) {
	requireCassandra(t)
	gate := p4bRequireEvidence(t)
	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	orgID := uuid.New()
	blockID := fmt.Sprintf("p4b-crash-orphan-%d", time.Now().UnixNano())
	target := seedCanonicalBlockRowForTest(t, database, orgID, blockID, "hot")
	d1 := gcpkg.BlockDeleteAuthority{Target: target, ClaimID: "p4b-d1-" + uuid.NewString(), ClaimedAt: time.Now().UTC().Truncate(time.Millisecond)}

	if claim, err := store.ClaimBlockDelete(orgID, blockID, d1); err != nil || claim.Outcome != gcpkg.BlockClaimAcquired {
		t.Fatalf("claim D1 = %s, %v", claim.Outcome, err)
	}
	if _, err := store.CommitBlockDeleteOrphanHandoff(orgID, blockID, d1); err != nil {
		t.Fatalf("handoff: %v", err)
	}
	first := time.Now().UTC().Truncate(time.Millisecond)
	created := store.StartBlockDeleteOrphan(orgID, blockID, gcpkg.CommittedBlockDeleteAuthorityForTest(d1), "sha1", first)
	if created.Outcome != gcpkg.StartBlockDeleteOrphanCreated {
		t.Fatalf("publish = %s, want created: %v", created.Outcome, created.Cause)
	}
	t.Cleanup(func() { _ = store.DeleteS3Orphan(orgID, blockID, created.FirstSeenAt) })

	same := store.StartBlockDeleteOrphan(orgID, blockID, gcpkg.CommittedBlockDeleteAuthorityForTest(d1), "sha1-retry", time.Now().UTC().Add(time.Hour))
	if same.Outcome != gcpkg.StartBlockDeleteOrphanSameAuthority || !same.FirstSeenAt.Equal(created.FirstSeenAt) {
		t.Fatalf("crash resume = %s first_seen_at=%v, want same_authority at %v", same.Outcome, same.FirstSeenAt, created.FirstSeenAt)
	}
	if phase := p4bLifecyclePhaseForTest(t, database, orgID, blockID, d1.ClaimID); phase != gcpkg.BlockDeleteLifecyclePhasePublished {
		t.Fatalf("lifecycle phase = %q, want published (no second tombstone)", phase)
	}
	gate.observed = true
}

func TestP4B_SecondCommitHandoffIsAlreadyCommittedAtRealCassandra(t *testing.T) {
	requireCassandra(t)
	gate := p4bRequireEvidence(t)
	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	orgID := uuid.New()
	blockID := fmt.Sprintf("p4b-second-handoff-%d", time.Now().UnixNano())
	target := seedCanonicalBlockRowForTest(t, database, orgID, blockID, "hot")
	d1 := gcpkg.BlockDeleteAuthority{Target: target, ClaimID: "p4b-d1-" + uuid.NewString(), ClaimedAt: time.Now().UTC().Truncate(time.Millisecond)}

	if claim, err := store.ClaimBlockDelete(orgID, blockID, d1); err != nil || claim.Outcome != gcpkg.BlockClaimAcquired {
		t.Fatalf("claim D1 = %s, %v", claim.Outcome, err)
	}
	if first, err := store.CommitBlockDeleteOrphanHandoff(orgID, blockID, d1); err != nil || (first.Outcome != gcpkg.BlockDeleteHandoffCommitted && first.Outcome != gcpkg.BlockDeleteHandoffAlreadyCommitted) {
		t.Fatalf("first handoff = %s, %v", first.Outcome, err)
	}
	second, err := store.CommitBlockDeleteOrphanHandoff(orgID, blockID, d1)
	if err != nil || second.Outcome != gcpkg.BlockDeleteHandoffAlreadyCommitted {
		t.Fatalf("second handoff = %s, %v; want already_committed with EACH_QUORUM confirmation", second.Outcome, err)
	}
	gate.observed = true
}

func TestP4B_FinalizeAlreadyFinalizedRequiresNonTerminalLifecycleAtRealCassandra(t *testing.T) {
	requireCassandra(t)
	gate := p4bRequireEvidence(t)
	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	orgID := uuid.New()
	blockID := fmt.Sprintf("p4b-finalize-terminal-%d", time.Now().UnixNano())
	target := seedCanonicalBlockRowForTest(t, database, orgID, blockID, "hot")
	d1 := gcpkg.BlockDeleteAuthority{Target: target, ClaimID: "p4b-d1-" + uuid.NewString(), ClaimedAt: time.Now().UTC().Truncate(time.Millisecond)}
	committed := gcpkg.CommittedBlockDeleteAuthorityForTest(d1)

	if claim, err := store.ClaimBlockDelete(orgID, blockID, d1); err != nil || claim.Outcome != gcpkg.BlockClaimAcquired {
		t.Fatalf("claim D1 = %s, %v", claim.Outcome, err)
	}
	if _, err := store.CommitBlockDeleteOrphanHandoff(orgID, blockID, d1); err != nil {
		t.Fatalf("handoff: %v", err)
	}
	created := store.StartBlockDeleteOrphan(orgID, blockID, committed, "sha1", time.Now().UTC())
	if created.Outcome != gcpkg.StartBlockDeleteOrphanCreated {
		t.Fatalf("publish = %s: %v", created.Outcome, created.Cause)
	}
	t.Cleanup(func() { _ = store.DeleteS3Orphan(orgID, blockID, created.FirstSeenAt) })
	if result, err := store.FinalizeBlockDelete(orgID, blockID, committed); err != nil || result.Outcome != gcpkg.BlockDeleteFinalized {
		t.Fatalf("first finalize = %+v, %v", result, err)
	}
	second, err := store.FinalizeBlockDelete(orgID, blockID, committed)
	if err != nil || second.Outcome != gcpkg.BlockDeleteAlreadyFinalized {
		t.Fatalf("second finalize while published = %+v, %v; want already_finalized", second, err)
	}
	if p4bFinalizeAuthorizesPhysicalDelete(second) {
		t.Fatal("AlreadyFinalized must not authorize physical delete")
	}
	if _, err := store.TerminateBlockDeleteLifecycle(orgID, blockID, committed); err != nil {
		t.Fatalf("terminate: %v", err)
	}
	third, err := store.FinalizeBlockDelete(orgID, blockID, committed)
	if third.Outcome == gcpkg.BlockDeleteAlreadyFinalized || p4bFinalizeAuthorizesPhysicalDelete(third) {
		t.Fatalf("finalize after terminal = %+v, %v; must not authorize S3", third, err)
	}
	if third.Outcome != gcpkg.BlockDeleteAlreadyComplete {
		t.Fatalf("finalize after terminal = %s, want already_complete", third.Outcome)
	}
	if err := store.DeleteS3Orphan(orgID, blockID, created.FirstSeenAt); err != nil {
		t.Fatalf("clear orphan after terminal: %v", err)
	}
	absent, err := store.FinalizeBlockDelete(orgID, blockID, committed)
	if p4bFinalizeAuthorizesPhysicalDelete(absent) || absent.Outcome == gcpkg.BlockDeleteAlreadyFinalized {
		t.Fatalf("orphan-absent finalize after terminal = %+v, %v; must not authorize S3", absent, err)
	}
	gate.observed = true
}

func TestP4B_CommittedP1DoesNotAuthorizeP2AtRealCassandra(t *testing.T) {
	requireCassandra(t)
	gate := p4bRequireEvidence(t)
	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	orgID := uuid.New()
	blockID := fmt.Sprintf("p4b-p1p2-%d", time.Now().UnixNano())
	target := seedCanonicalBlockRowForTest(t, database, orgID, blockID, "hot")
	d1 := gcpkg.BlockDeleteAuthority{Target: target, ClaimID: "p4b-d1-" + uuid.NewString(), ClaimedAt: time.Now().UTC().Truncate(time.Millisecond)}
	p2 := gcpkg.BlockDeleteAuthority{
		Target:    gcpkg.BlockDeleteTarget{StorageClass: target.StorageClass, StorageKey: target.StorageKey + ".p2"},
		ClaimID:   "p4b-p2-" + uuid.NewString(),
		ClaimedAt: time.Now().UTC().Truncate(time.Millisecond),
	}

	if claim, err := store.ClaimBlockDelete(orgID, blockID, d1); err != nil || claim.Outcome != gcpkg.BlockClaimAcquired {
		t.Fatalf("claim D1 = %s, %v", claim.Outcome, err)
	}
	if _, err := store.CommitBlockDeleteOrphanHandoff(orgID, blockID, d1); err != nil {
		t.Fatalf("handoff P1: %v", err)
	}
	handoff, err := store.CommitBlockDeleteOrphanHandoff(orgID, blockID, p2)
	if handoff.Outcome == gcpkg.BlockDeleteHandoffCommitted || handoff.Outcome == gcpkg.BlockDeleteHandoffAlreadyCommitted {
		t.Fatalf("P2 handoff against committed P1 = %s, %v", handoff.Outcome, err)
	}
	if result, finErr := store.FinalizeBlockDelete(orgID, blockID, gcpkg.CommittedBlockDeleteAuthorityForTest(p2)); finErr == nil && p4bFinalizeAuthorizesPhysicalDelete(result) {
		t.Fatalf("finalize P2 against committed P1 = %+v", result)
	}
	exists, err := store.BlockExists(orgID, blockID)
	if err != nil || !exists {
		t.Fatalf("P2 lifecycle removed P1 row (exists=%v err=%v)", exists, err)
	}
	gate.observed = true
}

func TestP4B_ReplayAfterTerminalDoesNotDeleteP1AtRealCassandra(t *testing.T) {
	requireCassandra(t)
	gate := p4bRequireEvidence(t)
	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	orgID := uuid.New()
	blockID := fmt.Sprintf("p4b-replay-%d", time.Now().UnixNano())
	target := seedCanonicalBlockRowForTest(t, database, orgID, blockID, "hot")
	d1 := gcpkg.BlockDeleteAuthority{Target: target, ClaimID: "p4b-d1-" + uuid.NewString(), ClaimedAt: time.Now().UTC().Truncate(time.Millisecond)}
	committed := gcpkg.CommittedBlockDeleteAuthorityForTest(d1)

	if claim, err := store.ClaimBlockDelete(orgID, blockID, d1); err != nil || claim.Outcome != gcpkg.BlockClaimAcquired {
		t.Fatalf("claim D1 = %s, %v", claim.Outcome, err)
	}
	if _, err := store.CommitBlockDeleteOrphanHandoff(orgID, blockID, d1); err != nil {
		t.Fatalf("handoff: %v", err)
	}
	created := store.StartBlockDeleteOrphan(orgID, blockID, committed, "sha1", time.Now().UTC())
	if created.Outcome != gcpkg.StartBlockDeleteOrphanCreated {
		t.Fatalf("publish = %s: %v", created.Outcome, created.Cause)
	}
	if result, err := store.FinalizeBlockDelete(orgID, blockID, committed); err != nil || !p4bFinalizeAuthorizesPhysicalDelete(result) {
		t.Fatalf("finalize = %+v, %v", result, err)
	}
	if _, err := store.TerminateBlockDeleteLifecycle(orgID, blockID, committed); err != nil {
		t.Fatalf("terminate: %v", err)
	}
	if err := store.DeleteS3Orphan(orgID, blockID, created.FirstSeenAt); err != nil {
		t.Fatalf("clear orphan: %v", err)
	}

	p2 := seedCanonicalBlockRowForTest(t, database, orgID, blockID, "hot")
	replay := store.StartBlockDeleteOrphan(orgID, blockID, committed, "sha1-stale", time.Now().UTC())
	if replay.Outcome == gcpkg.StartBlockDeleteOrphanCreated || replay.Outcome == gcpkg.StartBlockDeleteOrphanSameAuthority {
		t.Fatalf("stale D1 after terminal = %s; must not republish", replay.Outcome)
	}
	if replay.Outcome != gcpkg.StartBlockDeleteOrphanLifecycleAdvanced {
		t.Fatalf("stale D1 after terminal = %s, want lifecycle_advanced", replay.Outcome)
	}
	if _, found, err := store.GetS3OrphanGlobal(orgID, blockID); err != nil || found {
		t.Fatalf("stale D1 recreated orphan: found=%v err=%v", found, err)
	}
	if result, err := store.FinalizeBlockDelete(orgID, blockID, committed); p4bFinalizeAuthorizesPhysicalDelete(result) || result.Outcome == gcpkg.BlockDeleteAlreadyFinalized {
		t.Fatalf("stale finalize after terminal = %+v, %v", result, err)
	}
	var class, key string
	if err := database.Session().Query(`SELECT storage_class, storage_key FROM blocks WHERE org_id = ? AND block_id = ?`, orgID.String(), blockID).Scan(&class, &key); err != nil {
		t.Fatalf("read P2: %v", err)
	}
	if key != p2.StorageKey {
		t.Fatalf("stale D1 disturbed P2: key=%q want %q", key, p2.StorageKey)
	}
	gate.observed = true
}

func p4bFinalizeAuthorizesPhysicalDelete(result gcpkg.BlockDeleteFinalizeResult) bool {
	return result.Outcome == gcpkg.BlockDeleteFinalized
}

func p4bLifecyclePhaseForTest(t *testing.T, database *dbpkg.DB, orgID uuid.UUID, blockID, claimID string) string {
	t.Helper()
	var phase string
	err := database.Session().Query(
		`SELECT phase FROM gc_block_delete_lifecycles WHERE org_id = ? AND block_id = ? AND claim_id = ?`,
		orgID.String(), blockID, claimID,
	).Scan(&phase)
	if err != nil {
		t.Fatalf("read lifecycle phase: %v", err)
	}
	return phase
}

func TestP4B_RecoverS3OrphansTerminalLifecycleDoesNotDeleteAtRealCassandra(t *testing.T) {
	requireCassandra(t)
	gate := p4bRequireEvidence(t)
	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	orgID := uuid.New()
	sum := sha256.Sum256([]byte("p4b-recovery-terminal-" + uuid.NewString()))
	blockID := hex.EncodeToString(sum[:])
	firstSeenAt := seedS3Orphan(t, store, orgID, blockID, "hot", "sha1-recover", "prev", time.Now().UTC())
	committed := testCommittedOrphanAuthority(blockID, "hot", syntheticCanonicalStorageKeyForTest(orgID.String(), blockID))
	if _, err := store.TerminateBlockDeleteLifecycle(orgID, blockID, committed); err != nil {
		t.Fatalf("terminate: %v", err)
	}
	t.Cleanup(func() { _ = store.DeleteS3Orphan(orgID, blockID, firstSeenAt) })

	observed := store.ObserveBlockDeleteLifecycle(orgID, blockID, committed)
	if observed.Outcome != gcpkg.StartBlockDeleteOrphanLifecycleAdvanced {
		t.Fatalf("observe terminal lifecycle = %s, want lifecycle_advanced: %v", observed.Outcome, observed.Cause)
	}

	sp := &gcpkg.MockStorageProvider{}
	w := gcpkg.NewWorker(store, sp, gcpkg.NewQueue(store), 100, 0, false, &gcpkg.Stats{})
	w.SetDestructiveTopologyGate(func() error { return nil })
	if _, err := w.RecoverS3Orphans(context.Background(), 100); err != nil {
		t.Fatalf("RecoverS3Orphans: %v", err)
	}
	if got := sp.DeletedBlocks(); len(got) != 0 {
		t.Fatalf("terminal lifecycle must not authorize recovery S3: %v", got)
	}
	if _, found, err := store.GetS3OrphanGlobal(orgID, blockID); err != nil || found {
		t.Fatalf("stale pending_s3 orphan should be cleared: found=%v err=%v", found, err)
	}
	gate.observed = true
}
