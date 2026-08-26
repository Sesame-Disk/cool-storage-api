//go:build integration

package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
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
// that assertion passes at LOCAL_QUORUM too -- it would pass on the unfixed code.
//
// X2 solved this by building a genuinely divergent cluster: stop the other DCs,
// disable hinted handoff, write, bring them back. P3 cannot use that shape,
// because with a datacenter down an EACH_QUORUM publication does not succeed at
// all. That is not an obstacle -- it IS the property:
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
// HOW TO RUN THESE
// ----------------
// docs/TESTING.md, "Cross-datacenter (3-DC) legs" -- including the container route
// for hosts where a policy blocks executing freshly built Go binaries.

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

// p3RequireUnavailableAtEachQuorum is the assertion that makes the fail-closed leg
// mean something.
//
// `err != nil` is far too weak for a lightweight transaction. Cassandra separates
// the outcomes deliberately, and so must this test:
//
//	RequestErrUnavailable    the coordinator knew up front it could not reach the
//	                         required replicas -- the mutation was NOT attempted.
//	RequestErrWriteTimeout   with WriteType CAS on an LWT, the proposal may or may
//	                         not have been accepted. "It errored" says nothing.
//
// A timeout, a transport failure or any other error would satisfy a bare nil-check
// while the fence might well be published. Since this leg is what promotes the
// cross-DC consistency row to GREEN, it has to name the outcome it requires.
// It reports rather than aborts, deliberately. Both publications carry the same
// contract, and a mutation run downgrades both at once -- so if the first one
// aborted the test, a mutation would only ever demonstrate that ClaimBlockDelete is
// sensitive and StartBlockDeleteOrphan would never be reached. Recording the
// failure and continuing makes one mutation run exercise both, and the trailing
// survivor check still runs and reports the fence the downgrade left behind.
func p3RequireUnavailableAtEachQuorum(t *testing.T, what string, err error) {
	t.Helper()
	if err == nil {
		t.Errorf("P3 REGRESSION: %s succeeded with dc-na down; a fence a dc-na writer cannot see is a fence that does not fence", what)
		return
	}
	var unavailable *gocql.RequestErrUnavailable
	if !errors.As(err, &unavailable) {
		var timeout *gocql.RequestErrWriteTimeout
		if errors.As(err, &timeout) {
			t.Errorf("P3 REGRESSION: %s returned a write timeout (consistency=%v write_type=%q), which does NOT prove the fence was left unpublished; an LWT that times out may still have been accepted: %v", what, timeout.Consistency, timeout.WriteType, err)
			return
		}
		t.Errorf("P3 REGRESSION: %s failed with an error that does not prove the mutation was refused; want Unavailable, got %T: %v", what, err, err)
		return
	}
	if unavailable.Consistency != gocql.EachQuorum {
		t.Errorf("P3 REGRESSION: %s was refused at consistency %v, not EACH_QUORUM; the publication is not carrying the level the cross-DC argument depends on (required=%d alive=%d)", what, unavailable.Consistency, unavailable.Required, unavailable.Alive)
		return
	}
	t.Logf("%s refused as required: Unavailable at EACH_QUORUM (required=%d alive=%d)", what, unavailable.Required, unavailable.Alive)
}

// TestP3_FencePublicationFailsClosedWhenADatacenterIsDown is the green leg. With
// dc-na stopped, neither half of the fence may be published: an EACH_QUORUM commit
// cannot obtain a quorum in a datacenter that is not there, so GC cannot condemn an
// incarnation it would be unable to fence for writers in that DC.
//
// It asserts three things, not one: the error is specifically Unavailable, the
// level it names is EACH_QUORUM, and no fence actually landed in either surviving
// datacenter. The third is what turns "the call failed" into "nothing was
// published", and it is checked through the production reader from both DCs that
// were up and could therefore have accepted a partial write.
//
// The script downgrades the publishers and re-runs this leg to show it goes red.
func TestP3_FencePublicationFailsClosedWhenADatacenterIsDown(t *testing.T) {
	endpoints := p3RequireDCsDown(t)

	publisher := x2ConnectToDC(t, "dc-eu", endpoints)
	orgID, blockID, location := p3MultiDCBlock(t)
	p3InstallCanonical(t, publisher, orgID, blockID, location)

	store := gcpkg.NewCassandraStore(publisher)
	orgUUID := uuid.MustParse(orgID)

	// The in-row claim: the earliest fence a writer can observe. It names the exact
	// incarnation just installed, so the call is refused for the consistency reason this
	// leg is measuring rather than for failing to match the row.
	attempt := gcpkg.BlockDeleteAuthority{
		Target:    gcpkg.BlockDeleteTarget{StorageClass: location.StorageClass, StorageKey: location.StorageKey},
		ClaimID:   "p3-multidc-" + uuid.NewString(),
		ClaimedAt: time.Now().UTC(),
	}
	_, claimErr := store.ClaimBlockDelete(orgUUID, blockID, attempt)
	p3RequireUnavailableAtEachQuorum(t, "ClaimBlockDelete", claimErr)

	// The orphan: the fence that gates a rowless mint, so a publication invisible to
	// dc-na is what lets a second physical life be born while the first retires.
	_, orphanErr := store.StartBlockDeleteOrphan(orgUUID, blockID, location.StorageClass, location.StorageKey, "", time.Now().UTC())
	p3RequireUnavailableAtEachQuorum(t, "StartBlockDeleteOrphan", orphanErr)

	// Refused is not the same as "left no trace". Check both datacenters that WERE
	// reachable, because those are the ones a partially applied publication would
	// have landed on.
	for _, dc := range []string{"dc-eu", "dc-asia"} {
		survivor := x2ConnectToDC(t, dc, endpoints)
		fenced, err := survivor.BlockDeleteFenceActive(orgID, blockID)
		if err != nil {
			t.Fatalf("fence read from %s after the refused publications: %v", dc, err)
		}
		if fenced {
			t.Fatalf("P3 REGRESSION: %s holds a fence for a block whose publication could not obtain EACH_QUORUM; a publication that cannot reach every datacenter must leave nothing behind", dc)
		}
	}

	t.Logf("P3_MULTIDC_FAILCLOSED_EVIDENCE org=%s block=%s both publications refused as Unavailable at EACH_QUORUM, and neither dc-eu nor dc-asia holds a fence", orgID, blockID)
}

// TestP3_WriterInAnotherDatacenterObservesTheFence runs the REAL destructive
// lifecycle from dc-eu -- claim, orphan, finalize -- and then asks dc-na the
// question a fresh writer asks: is this block free to mint?
//
// Running the whole lifecycle is what makes this the rowless-mint gate rather than
// merely an orphan read. After finalize, blocks(L) is gone, so dc-na sees the exact
// state the A+ handoff is about -- no canonical row, live orphan -- and must still
// refuse. An earlier version of this leg published only the orphan and left the
// canonical row in place, which measured the fence read but not the gate.
//
// It is the cross-datacenter form of what
// TestP3BlockDeleteFenceSurvivesOrphanHandoff pins inside a single process.
func TestP3_WriterInAnotherDatacenterObservesTheFence(t *testing.T) {
	endpoints := x2DCEndpoints(t)
	p3RequireAllDCsUp(t, endpoints)

	publisher := x2ConnectToDC(t, "dc-eu", endpoints)
	writer := x2ConnectToDC(t, "dc-na", endpoints)

	orgID, blockID, location := p3MultiDCBlock(t)
	p3InstallCanonical(t, publisher, orgID, blockID, location)

	// Free to reuse before anything is condemned, from the writer's datacenter.
	probe, err := writer.ProbeBlockReuse(orgID, blockID)
	if err != nil || probe.Decision == dbpkg.BlockReuseBlockedByGC {
		t.Fatalf("dc-na probe before condemnation = %v, %v; the fixture is dirty", probe.Decision, err)
	}
	outcome, err := writer.ValidateBlockRepairAuthority(orgID, blockID, location)
	if outcome != dbpkg.BlockRepairAuthorityAuthorized {
		t.Fatalf("dc-na repair authority before condemnation = %v, %v; want authorized", outcome, err)
	}

	// The real lifecycle, in the order GC runs it.
	store := gcpkg.NewCassandraStore(publisher)
	orgUUID := uuid.MustParse(orgID)
	authority := gcpkg.BlockDeleteAuthority{
		Target:    gcpkg.BlockDeleteTarget{StorageClass: location.StorageClass, StorageKey: location.StorageKey},
		ClaimID:   "p3-multidc-" + uuid.NewString(),
		ClaimedAt: time.Now().UTC(),
	}
	outcome2, err := store.ClaimBlockDelete(orgUUID, blockID, authority)
	if err != nil || outcome2 != gcpkg.BlockClaimAcquired {
		t.Fatalf("claim P1 from dc-eu = %s, %v; want acquired", outcome2, err)
	}
	if _, err := store.StartBlockDeleteOrphan(orgUUID, blockID, location.StorageClass, location.StorageKey, "", time.Now().UTC()); err != nil {
		t.Fatalf("publish orphan fence from dc-eu: %v", err)
	}
	if err := store.FinalizeBlockDelete(orgUUID, blockID, authority); err != nil {
		t.Fatalf("finalize P1 from dc-eu: %v", err)
	}

	// dc-na now faces the handoff state: canonical row gone, orphan live.
	if exists, err := gcpkg.NewCassandraStore(writer).BlockExists(orgUUID, blockID); err != nil || exists {
		t.Fatalf("dc-na still sees blocks(L) after finalize (exists=%v err=%v); this leg needs the rowless state to be the thing under test", exists, err)
	}
	fenced, err := writer.BlockDeleteFenceActive(orgID, blockID)
	if err != nil {
		t.Fatalf("dc-na fence read after the lifecycle: %v", err)
	}
	if !fenced {
		t.Fatalf("P3 REGRESSION: dc-na sees no canonical row AND no fence after dc-eu published orphan(P1); a fresh writer there would mint a second physical life while the first is still retiring")
	}
	probe, err = writer.ProbeBlockReuse(orgID, blockID)
	if err != nil {
		t.Fatalf("dc-na probe after the lifecycle: %v", err)
	}
	if probe.Decision != dbpkg.BlockReuseBlockedByGC {
		t.Fatalf("P3 REGRESSION: dc-na probe = %v, want BlockedByGC while dc-eu's orphan fence is live", probe.Decision)
	}

	// The pre-PUT boundary, on the same state. This is the writer that captured P1
	// before the lifecycle started and arrives late at its revalidation, and the
	// state it arrives to is the interesting one: the canonical row is GONE. The
	// authority read therefore has to separate two rowless observations that look
	// identical until the orphan is consulted --
	//
	//	row absent, no orphan  -> Changed  (start over; a new life may be minted)
	//	row absent, orphan(P1) -> Blocked  (the previous life is still retiring)
	//
	// which is exactly the blocks-first/orphan-last ordering, exercised across
	// datacenters. Asserting the cause and not only the outcome keeps a future
	// refactor from satisfying this by returning Blocked for the wrong reason.
	outcome, authErr := writer.ValidateBlockRepairAuthority(orgID, blockID, location)
	if outcome != dbpkg.BlockRepairAuthorityBlocked {
		t.Fatalf("P3 REGRESSION: dc-na pre-PUT repair authority = %v, %v; want Blocked while dc-eu's orphan(P1) is live and the canonical row is gone", outcome, authErr)
	}
	if !errors.Is(authErr, dbpkg.ErrBlockRepairBlocked) {
		t.Fatalf("P3 REGRESSION: dc-na repair authority was Blocked but the cause is %v; want ErrBlockRepairBlocked, so the refusal is the fence and not some other rowless condition", authErr)
	}

	t.Logf("P3_MULTIDC_HANDOFF_EVIDENCE org=%s block=%s dc-eu ran claim->orphan->finalize; dc-na sees rowless + orphan, refuses to mint, and refuses the pre-PUT repair with ErrBlockRepairBlocked", orgID, blockID)
}

// p3RequireDCsDown refuses to run the fail-closed leg against a healthy cluster.
// Without it the leg would pass vacuously the moment someone forgets to stop
// dc-na: the publications would simply succeed and the assertions would fail for
// the opposite reason, reported as a regression that is really a fixture error.
// Naming the condition keeps the diagnosis honest.
func p3RequireDCsDown(t *testing.T) map[string]string {
	t.Helper()
	endpoints := x2DCEndpoints(t)
	if strings.TrimSpace(os.Getenv("P3_EXPECT_DC_DOWN")) != "1" {
		t.Skip("P3_EXPECT_DC_DOWN not set; this leg requires a datacenter to be stopped (see docs/TESTING.md)")
	}
	if _, ok := endpoints["dc-na"]; !ok {
		t.Fatal("X2_DC_HOSTS does not describe dc-na")
	}
	return endpoints
}

func p3RequireAllDCsUp(t *testing.T, endpoints map[string]string) {
	t.Helper()
	if strings.TrimSpace(os.Getenv("P3_EXPECT_DC_DOWN")) == "1" {
		t.Skip("P3_EXPECT_DC_DOWN is set; the handoff leg needs every datacenter reachable")
	}
	for _, dc := range []string{"dc-na", "dc-eu", "dc-asia"} {
		if _, ok := endpoints[dc]; !ok {
			t.Fatalf("X2_DC_HOSTS does not describe %s", dc)
		}
	}
}
