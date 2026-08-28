//go:build integration

package integration

import (
	"fmt"
	"testing"
	"time"

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

	committed := gcpkg.CommittedBlockDeleteAuthority{BlockDeleteAuthority: d1}
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

	other := store.StartBlockDeleteOrphan(orgID, blockID, gcpkg.CommittedBlockDeleteAuthority{BlockDeleteAuthority: d2}, "sha1-d2", time.Now().UTC())
	if other.Outcome != gcpkg.StartBlockDeleteOrphanDifferentAuthority {
		t.Fatalf("same-P different-D = %s, want different_authority", other.Outcome)
	}

	if result, finErr := store.FinalizeBlockDelete(orgID, blockID, gcpkg.CommittedBlockDeleteAuthority{BlockDeleteAuthority: d2}); finErr == nil || result.Outcome == gcpkg.BlockDeleteFinalized || result.Outcome == gcpkg.BlockDeleteAlreadyFinalized {
		t.Fatalf("finalize with D2 = %+v, %v; want not_authority", result, finErr)
	}
	if result, finErr := store.FinalizeBlockDelete(orgID, blockID, committed); finErr != nil || (result.Outcome != gcpkg.BlockDeleteFinalized && result.Outcome != gcpkg.BlockDeleteAlreadyFinalized) {
		t.Fatalf("finalize with D1 = %+v, %v; want applied", result, finErr)
	}

	gate.observed = true
	t.Logf("P4B_CLAIM_ORPHAN_AUTHORITY_EVIDENCE handoff=1 resume_d1=1 same_authority=1 different_authority=1 finalize_bound=1")
}
