//go:build integration

package integration

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	"github.com/google/uuid"
)

// Cross-datacenter R3 evidence: a fence published EACH_QUORUM in dc-eu must
// be visible to ValidatePublishAttemptAuthority in dc-na. This fixture is
// RF=1/DC, so LOCAL_QUORUM ≡ ONE and cannot prove the read-level pin; that pin
// stays in TestR3FenceReadConsistencyIsLocalQuorum / TestP3FenceReadConsistencyIsLocalQuorum.

func TestR3_WriterInAnotherDatacenterRejectsClaimedBlock(t *testing.T) {
	endpoints := x2DCEndpoints(t)
	r3RequireAllDCsUp(t)

	publisher := x2ConnectToDC(t, "dc-eu", endpoints)
	writer := x2ConnectToDC(t, "dc-na", endpoints)

	orgID, blockID, location := p3MultiDCBlock(t)
	p3InstallCanonical(t, publisher, orgID, blockID, location)

	outcome, err := dbpkg.ValidatePublishAttemptAuthority(writer, orgID, []string{blockID})
	if err != nil || outcome != dbpkg.BlockPublishAuthorityActive {
		t.Fatalf("dc-na before claim = %v, %v; want active", outcome, err)
	}

	store := gcpkg.NewCassandraStore(publisher)
	orgUUID := uuid.MustParse(orgID)
	authority := gcpkg.BlockDeleteAuthority{
		Target:    gcpkg.BlockDeleteTarget{StorageClass: location.StorageClass, StorageKey: location.StorageKey},
		ClaimID:   "r3-multidc-" + uuid.NewString(),
		ClaimedAt: time.Now().UTC(),
	}
	claim, err := store.ClaimBlockDelete(orgUUID, blockID, authority)
	if err != nil || claim.Outcome != gcpkg.BlockClaimAcquired {
		t.Fatalf("claim from dc-eu = %s, %v; want acquired", claim.Outcome, err)
	}

	outcome, err = dbpkg.ValidatePublishAttemptAuthority(writer, orgID, []string{blockID})
	if outcome != dbpkg.BlockPublishAuthorityDeleting {
		t.Fatalf("R3 REGRESSION: dc-na Validate after dc-eu claim = %v, %v; want deleting", outcome, err)
	}
	if !errors.Is(err, dbpkg.ErrBlockPublishAuthorityDenied) {
		t.Fatalf("R3 REGRESSION: dc-na claim refusal cause = %v, want ErrBlockPublishAuthorityDenied", err)
	}
	t.Logf("R3_MULTIDC_CLAIM_EVIDENCE org=%s block=%s dc-eu claimed; dc-na Validate=deleting", orgID, blockID)
}

func TestR3_WriterInAnotherDatacenterRejectsOrphanFence(t *testing.T) {
	endpoints := x2DCEndpoints(t)
	r3RequireAllDCsUp(t)

	publisher := x2ConnectToDC(t, "dc-eu", endpoints)
	writer := x2ConnectToDC(t, "dc-na", endpoints)

	orgID, blockID, location := p3MultiDCBlock(t)
	p3InstallCanonical(t, publisher, orgID, blockID, location)

	store := gcpkg.NewCassandraStore(publisher)
	orgUUID := uuid.MustParse(orgID)
	authority := gcpkg.BlockDeleteAuthority{
		Target:    gcpkg.BlockDeleteTarget{StorageClass: location.StorageClass, StorageKey: location.StorageKey},
		ClaimID:   "r3-multidc-orphan-" + uuid.NewString(),
		ClaimedAt: time.Now().UTC(),
	}
	claim, err := store.ClaimBlockDelete(orgUUID, blockID, authority)
	if err != nil || claim.Outcome != gcpkg.BlockClaimAcquired {
		t.Fatalf("claim from dc-eu = %s, %v; want acquired", claim.Outcome, err)
	}
	handoff, err := store.CommitBlockDeleteOrphanHandoff(orgUUID, blockID, authority)
	if err != nil || (handoff.Outcome != gcpkg.BlockDeleteHandoffCommitted && handoff.Outcome != gcpkg.BlockDeleteHandoffAlreadyCommitted) {
		t.Fatalf("handoff from dc-eu = %s, %v; want committed", handoff.Outcome, err)
	}
	committed := gcpkg.CommittedBlockDeleteAuthorityForTest(authority)
	orphan := store.StartBlockDeleteOrphan(orgUUID, blockID, committed, "", time.Now().UTC())
	if orphan.Outcome != gcpkg.StartBlockDeleteOrphanCreated {
		t.Fatalf("orphan from dc-eu = %s cause=%v; want created", orphan.Outcome, orphan.Cause)
	}
	if _, err := store.FinalizeBlockDelete(orgUUID, blockID, committed); err != nil {
		t.Fatalf("finalize from dc-eu: %v", err)
	}

	outcome, err := dbpkg.ValidatePublishAttemptAuthority(writer, orgID, []string{blockID})
	if outcome != dbpkg.BlockPublishAuthorityOrphaned {
		t.Fatalf("R3 REGRESSION: dc-na Validate after dc-eu claim→orphan→finalize = %v, %v; want orphaned", outcome, err)
	}
	if !errors.Is(err, dbpkg.ErrBlockPublishAuthorityDenied) {
		t.Fatalf("R3 REGRESSION: dc-na orphan refusal cause = %v, want ErrBlockPublishAuthorityDenied", err)
	}
	t.Logf("R3_MULTIDC_ORPHAN_EVIDENCE org=%s block=%s dc-eu ran claim→orphan→finalize; dc-na Validate=orphaned", orgID, blockID)
}

func r3RequireAllDCsUp(t *testing.T) {
	t.Helper()
	if strings.TrimSpace(os.Getenv("P3_EXPECT_DC_DOWN")) == "1" {
		t.Skip("P3_EXPECT_DC_DOWN is set; R3 3-DC legs need every datacenter reachable")
	}
}
