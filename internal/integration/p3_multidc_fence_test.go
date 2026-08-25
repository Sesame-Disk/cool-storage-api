//go:build integration

package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	"github.com/google/uuid"
)

// Cross-datacenter closure evidence for the P3 writer fence, against the same
// three-datacenter fixture X2 uses.
//
// WHAT P3 CLAIMS, AND WHY ONE PROCESS CANNOT PROVE IT
// ---------------------------------------------------
// P3 publishes the writer-visible fence with EACH_QUORUM commit visibility and
// SERIAL Paxos (ClaimBlockDelete, StartBlockDeleteOrphan), and reads it back with
// a pinned LOCAL_QUORUM (BlockFenceReadConsistency). The correctness argument is
// an intersection: an EACH_QUORUM commit obtains a quorum in EVERY datacenter, so
// it meets the reader's local quorum wherever the reader is. The unit suite pins
// WHICH levels the statements carry; it cannot observe a consistency level, so it
// cannot show the intersection actually holds. These tests can.
//
// WHY THE OBVIOUS TEST WOULD PROVE NOTHING
// ----------------------------------------
// "Publish the fence in dc-eu, read it from dc-na, assert visible" is not a test
// of anything. Cassandra sends every mutation to all replicas regardless of the
// consistency level; the level only decides how many acknowledgements the
// coordinator waits for. dc-na's replica has usually received the row already, so
// that assertion passes at LOCAL_QUORUM too — it would pass on the unfixed code.
//
// X2 solved this by building a genuinely divergent cluster: stop the other DCs,
// disable hinted handoff, write, bring them back. P3 cannot use that shape,
// because with a datacenter down an EACH_QUORUM publication does not succeed at
// all. That is not an obstacle — it IS the property:
//
//	you cannot reach a state where the fence exists but a writer in another
//	datacenter cannot see it, because the publication either obtains a quorum
//	in every DC or it fails and nothing is condemned.
//
// So the green leg asserts the failure, and the mutations assert that the weaker
// levels reach exactly the state the fence exists to prevent.
//
// WHY THREE DATACENTERS
// ---------------------
// Same discrimination X2 needed, on the write side. With one DC down:
//
//	EACH_QUORUM  needs a quorum in every DC ─► dc-na unreachable ─► ERROR  ✅
//	QUORUM       needs 2 of 3 ─► dc-eu + dc-asia answer ────────► SUCCESS ❌
//	LOCAL_QUORUM needs dc-eu alone ────────────────────────────► SUCCESS ❌
//
// At two DCs with RF 1, QUORUM is 2 of 2: it would also fail with one DC down, so
// a two-DC fixture cannot tell EACH_QUORUM from plain QUORUM. Only at three does
// the wrong fix become visible.
//
// WHAT THIS FIXTURE CANNOT SHOW
// -----------------------------
// The pinned read level. BlockFenceReadConsistency exists because
// `database.consistency` accepts ONE, and a ONE read can miss a committed fence.
// With RF 1 per datacenter, LOCAL_QUORUM and ONE both resolve to the single local
// replica, so they are indistinguishable here. That pin defends a deployment with
// RF > 1 inside a datacenter, which this fixture does not have; it stays covered
// by TestP3FenceReadConsistencyIsLocalQuorum and the AST guard.
//
// See docs/GC-X2-MULTIDC-VALIDATION.md for the fixture, and
// scripts/x2-multidc-validation.sh --p3 for the executable form.

// p3MultiDCBlock derives a fresh canonical block identity per run. Reusing one
// would let a previous run's rows decide this run's assertions.
func p3MultiDCBlock(t *testing.T) (orgID, blockID string, location dbpkg.BlockPhysicalLocation) {
	t.Helper()
	orgID = uuid.NewString()
	seed := fmt.Sprintf("p3-multidc-%s-%d", orgID, time.Now().UnixNano())
	digest := sha256.Sum256([]byte(seed))
	blockID = hex.EncodeToString(digest[:])
	location = dbpkg.BlockPhysicalLocation{
		StorageClass: "hot",
		StorageKey:   fmt.Sprintf("blocks/%s/%s/%s/%s.%s", orgID, blockID[0:2], blockID[2:4], blockID, uuid.NewString()),
	}
	return orgID, blockID, location
}

// p3InstallCanonical puts blocks(L) -> P1 in place so there is a lifecycle to
// condemn, and registers cleanup that runs whatever the legs do to the row.
func p3InstallCanonical(t *testing.T, database *dbpkg.DB, orgID, blockID string, location dbpkg.BlockPhysicalLocation) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result := database.InstallBlockMetadata(ctx, orgID, dbpkg.PlainBlockRepresentationID, blockID, "", 7, location)
	if result.Outcome != dbpkg.InstallBlockMetadataApplied {
		t.Fatalf("install canonical P1: outcome=%v cause=%v", result.Outcome, result.Cause)
	}
	t.Cleanup(func() {
		_ = database.Session().Query(`DELETE FROM gc_s3_orphans WHERE org_id = ? AND block_id = ?`, orgID, blockID).Exec()
		_ = database.Session().Query(`DELETE FROM blocks WHERE org_id = ? AND block_id = ?`, orgID, blockID).Exec()
	})
}

// TestP3_FencePublicationFailsClosedWhenADatacenterIsDown is the green leg. With
// dc-na stopped, neither half of the fence may be published: an EACH_QUORUM
// commit cannot obtain a quorum in a datacenter that is not there, so GC cannot
// condemn an incarnation it would be unable to fence for writers in that DC.
//
// The script downgrades the publishers and re-runs this leg to show it goes red.
// Under LOCAL_QUORUM or plain QUORUM both publications succeed with dc-na down,
// and the cluster reaches the state P3 exists to prevent: a condemned incarnation
// whose fence a dc-na writer cannot observe.
func TestP3_FencePublicationFailsClosedWhenADatacenterIsDown(t *testing.T) {
	endpoints := p3RequireDCsDown(t)

	publisher := x2ConnectToDC(t, "dc-eu", endpoints)
	orgID, blockID, location := p3MultiDCBlock(t)
	p3InstallCanonical(t, publisher, orgID, blockID, location)

	store := gcpkg.NewCassandraStore(publisher)
	orgUUID := uuid.MustParse(orgID)

	// Half one: the in-row claim. This is the earliest fence a writer can observe.
	claimed, claimErr := store.ClaimBlockDelete(orgUUID, blockID, "p3-multidc-"+uuid.NewString())
	if claimErr == nil {
		t.Fatalf("P3 REGRESSION: ClaimBlockDelete succeeded (applied=%v) with dc-na down; a claim a dc-na writer cannot see is a fence that does not fence", claimed)
	}
	t.Logf("claim publication failed closed as required: %v", claimErr)

	// Half two: the orphan. This is the fence that gates a rowless mint, so a
	// publication invisible to dc-na is what lets a second physical life be born
	// while the first is still being retired.
	_, orphanErr := store.StartBlockDeleteOrphan(orgUUID, blockID, location.StorageClass, location.StorageKey, "", time.Now().UTC())
	if orphanErr == nil {
		t.Fatalf("P3 REGRESSION: StartBlockDeleteOrphan succeeded with dc-na down; a dc-na writer would read no fence and mint a new incarnation over a condemned one")
	}
	t.Logf("orphan publication failed closed as required: %v", orphanErr)

	t.Log("P3_MULTIDC_FAILCLOSED_EVIDENCE both fence publications refused while dc-na was unreachable")
}

// TestP3_WriterInAnotherDatacenterObservesTheFence runs with all three DCs up and
// checks the writer-side plumbing end to end against a real multi-DC topology:
// a fence published from dc-eu must block both the rowless-mint gate and the
// pre-PUT repair authority when they are evaluated from dc-na.
//
// Honest about its own strength: with every DC reachable, normal replication
// would carry the rows to dc-na regardless of the publication level, so this leg
// does NOT discriminate consistency levels — the leg above does that. What it
// does prove is that the reader path resolves correctly when the writer's session
// is pinned to a different datacenter than the publisher's, which no single-DC
// test exercises.
func TestP3_WriterInAnotherDatacenterObservesTheFence(t *testing.T) {
	endpoints := x2DCEndpoints(t)
	p3RequireAllDCsUp(t, endpoints)

	publisher := x2ConnectToDC(t, "dc-eu", endpoints)
	writer := x2ConnectToDC(t, "dc-na", endpoints)

	orgID, blockID, location := p3MultiDCBlock(t)
	p3InstallCanonical(t, publisher, orgID, blockID, location)

	// Unfenced to begin with, from the writer's datacenter.
	fenced, err := writer.BlockDeleteFenceActive(orgID, blockID)
	if err != nil {
		t.Fatalf("dc-na fence read before condemnation: %v", err)
	}
	if fenced {
		t.Fatalf("dc-na reports a fence before anything was published; the fixture is dirty")
	}
	outcome, err := writer.ValidateBlockRepairAuthority(orgID, blockID, location)
	if outcome != dbpkg.BlockRepairAuthorityAuthorized {
		t.Fatalf("dc-na repair authority before condemnation = %v, %v; want authorized", outcome, err)
	}

	store := gcpkg.NewCassandraStore(publisher)
	orgUUID := uuid.MustParse(orgID)
	if _, err := store.StartBlockDeleteOrphan(orgUUID, blockID, location.StorageClass, location.StorageKey, "", time.Now().UTC()); err != nil {
		t.Fatalf("publish orphan fence from dc-eu: %v", err)
	}

	fenced, err = writer.BlockDeleteFenceActive(orgID, blockID)
	if err != nil {
		t.Fatalf("dc-na fence read after condemnation: %v", err)
	}
	if !fenced {
		t.Fatalf("P3 REGRESSION: orphan published from dc-eu is invisible to the dc-na fence read; a dc-na writer would mint over a condemned incarnation")
	}
	outcome, authErr := writer.ValidateBlockRepairAuthority(orgID, blockID, location)
	if outcome != dbpkg.BlockRepairAuthorityBlocked {
		t.Fatalf("P3 REGRESSION: dc-na repair authority = %v, %v; want Blocked while dc-eu's orphan fence is live", outcome, authErr)
	}

	t.Logf("P3_MULTIDC_VISIBILITY_EVIDENCE org=%s block=%s fence published in dc-eu, observed in dc-na by both the mint gate and the pre-PUT authority", orgID, blockID)
}

// p3RequireDCsDown refuses to run the fail-closed leg against a healthy cluster.
// Without this the leg would pass vacuously the moment someone forgets to stop
// dc-na: the publications would simply succeed and the assertions would... also
// fail, but for the opposite reason, reported as a regression that is really a
// fixture error. Naming the condition keeps the diagnosis honest.
func p3RequireDCsDown(t *testing.T) map[string]string {
	t.Helper()
	endpoints := x2DCEndpoints(t)
	if strings.TrimSpace(os.Getenv("P3_EXPECT_DC_DOWN")) != "1" {
		t.Skip("P3_EXPECT_DC_DOWN not set; this leg requires a datacenter to be stopped (see scripts/x2-multidc-validation.sh --p3)")
	}
	if _, ok := endpoints["dc-na"]; !ok {
		t.Fatal("X2_DC_HOSTS does not describe dc-na")
	}
	return endpoints
}

func p3RequireAllDCsUp(t *testing.T, endpoints map[string]string) {
	t.Helper()
	if strings.TrimSpace(os.Getenv("P3_EXPECT_DC_DOWN")) == "1" {
		t.Skip("P3_EXPECT_DC_DOWN is set; the visibility leg needs every datacenter reachable")
	}
	for _, dc := range []string{"dc-na", "dc-eu", "dc-asia"} {
		if _, ok := endpoints[dc]; !ok {
			t.Fatalf("X2_DC_HOSTS does not describe %s", dc)
		}
	}
}
