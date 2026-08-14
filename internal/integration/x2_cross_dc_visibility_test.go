//go:build integration

package integration

import (
	"os"
	"strings"
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/google/uuid"
)

// Cross-datacenter regressions for ISSUE-GC-CROSS-DC-REFERENCE-VISIBILITY-01 (X2).
//
// The unit suite (internal/gc/x2_cross_dc_liveness_test.go) pins WHICH read
// authorizes a delete and that errors fail closed. It cannot pin the property the
// closure actually rests on, because a single process cannot observe a consistency
// level. These tests do, against a real three-datacenter cluster.
//
// WHY A NAIVE TEST PROVES NOTHING
// -------------------------------
// "Write LOCAL_QUORUM in dc-eu, read EACH_QUORUM from dc-na, assert visible" is not
// a test of this fix. Cassandra sends every mutation to ALL replicas regardless of
// consistency level; the level only decides how many acknowledgements the
// coordinator waits for. The dc-na replica has therefore usually received the row
// already, and that assertion passes whether the read is EACH_QUORUM or
// LOCAL_QUORUM — it would pass on the unfixed code.
//
// To be meaningful the cluster must be in a state where dc-na genuinely does NOT
// have the row, and the test must assert BOTH halves against that same state:
//
//	LOCAL_QUORUM from dc-na  ==  false   (the defect: local view is blind)
//	EACH_QUORUM  from dc-na  ==  true    (the fix: global view intersects dc-eu)
//
// Only the pair is a regression. The first assertion is what makes the second mean
// something, and it is what a downgrade of the destructive read would break.
//
// See docs/GC-X2-MULTIDC-VALIDATION.md for the procedure that builds that state
// (stop the other DCs, disable hinted handoff, write, bring them back).
//
// THREE datacenters are required. Two reproduce the defect — LOCAL_QUORUM and
// EACH_QUORUM already diverge there — but cannot rule out a plain QUORUM as an equally
// good fix, because at two DCs with RF 1 it is 2 of 2 and intersects everything by
// accident. Only at three does QUORUM become 2 of 3, free to miss the single replica
// holding the reference, and only then is EACH_QUORUM demonstrably the right level.

// x2DCEndpoints parses X2_DC_HOSTS ("dc-na=host:port,dc-eu=host:port,...").
func x2DCEndpoints(t *testing.T) map[string]string {
	t.Helper()

	raw := strings.TrimSpace(os.Getenv("X2_DC_HOSTS"))
	if raw == "" {
		t.Skip("X2_DC_HOSTS not set; skipping cross-DC regression (see docs/GC-X2-MULTIDC-VALIDATION.md)")
	}

	endpoints := map[string]string{}
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		dc, host, ok := strings.Cut(entry, "=")
		dc, host = strings.TrimSpace(dc), strings.TrimSpace(host)
		if !ok || dc == "" || host == "" {
			t.Fatalf("malformed X2_DC_HOSTS entry %q; want dc=host:port", entry)
		}
		endpoints[dc] = host
	}
	if len(endpoints) < 3 {
		t.Skipf("X2 closure evidence needs three datacenters (two reproduce the defect but cannot show QUORUM is the wrong fix); X2_DC_HOSTS has %d", len(endpoints))
	}
	return endpoints
}

func x2Endpoint(t *testing.T, endpoints map[string]string, dc string) string {
	t.Helper()
	host, ok := endpoints[dc]
	if !ok {
		t.Skipf("X2_DC_HOSTS has no %s entry", dc)
	}
	return host
}

// x2ConnectToDC opens a session pinned to one datacenter, so a write issued here is
// acknowledged by that DC's quorum and read locally from that DC's replica.
//
// The declared replication map is every DC in X2_DC_HOSTS at RF 1, matching what the
// fixture's keyspace is created with. That is not decoration: the destructive
// topology gate requires the live map to equal the declared one, so a session that
// under-declares its topology is refused — which is the intended behaviour, since a
// process that does not know about dc-eu has no business authorizing deletes of
// blocks dc-eu may still reference.
func x2ConnectToDC(t *testing.T, dc string, endpoints map[string]string) *dbpkg.DB {
	t.Helper()

	host := x2Endpoint(t, endpoints, dc)
	replicationDCs := make(map[string]int, len(endpoints))
	for name := range endpoints {
		replicationDCs[name] = 1
	}

	database, err := dbpkg.New(config.DatabaseConfig{
		Hosts:             []string{host},
		Keyspace:          envOrDefault("CASSANDRA_KEYSPACE", "sesamefs"),
		Consistency:       "LOCAL_QUORUM",
		SerialConsistency: "SERIAL",
		LocalDC:           dc,
		ReplicationClass:  "NetworkTopologyStrategy",
		ReplicationDCs:    replicationDCs,
		Username:          os.Getenv("CASSANDRA_USERNAME"),
		Password:          os.Getenv("CASSANDRA_PASSWORD"),
	})
	if err != nil {
		t.Fatalf("connect to %s at %s: %v", dc, host, err)
	}
	t.Cleanup(database.Close)
	return database
}

// TestX2_DivergentReferenceIsInvisibleLocallyAndVisibleGlobally is the regression.
// It requires a cluster left in the divergent state described above, signalled by
// X2_DIVERGENT_BLOCK — the block id whose reference exists only in dc-eu.
//
// Build the state first (see the runbook), then:
//
//	X2_DIVERGENT_BLOCK=<block-id> go test -tags integration ./internal/integration/ -run TestX2_Divergent -v
func TestX2_DivergentReferenceIsInvisibleLocallyAndVisibleGlobally(t *testing.T) {
	endpoints := x2DCEndpoints(t)

	blockID := strings.TrimSpace(os.Getenv("X2_DIVERGENT_BLOCK"))
	orgID := strings.TrimSpace(os.Getenv("X2_DIVERGENT_ORG"))
	if blockID == "" || orgID == "" {
		t.Skip("X2_DIVERGENT_BLOCK/X2_DIVERGENT_ORG not set; skipping (the divergent state must be built first — see docs/GC-X2-MULTIDC-VALIDATION.md)")
	}

	reader := x2ConnectToDC(t, "dc-na", endpoints)

	// Half one — the defect. dc-na must NOT see the reference locally. If it does,
	// the divergent state was not built correctly (hints replayed, read repair ran,
	// or the write reached dc-na), and the second half would pass vacuously.
	localView, err := reader.BlockHasReferences(orgID, blockID)
	if err != nil {
		t.Fatalf("LOCAL_QUORUM read from dc-na failed: %v", err)
	}
	if localView {
		t.Fatalf("the cluster is not divergent: dc-na already sees the reference locally, so this run cannot distinguish EACH_QUORUM from LOCAL_QUORUM. Rebuild the divergent state (hinted handoff disabled) before trusting a green result")
	}

	// Half two — the fix. The destructive read must see it anyway, because
	// EACH_QUORUM has to obtain a quorum in dc-eu, which holds the row.
	globalView, err := reader.BlockHasReferencesGlobal(orgID, blockID)
	if err != nil {
		t.Fatalf("EACH_QUORUM read from dc-na failed: %v", err)
	}
	if !globalView {
		t.Fatalf("X2 REGRESSION: reference acknowledged at LOCAL_QUORUM in dc-eu is invisible to the EACH_QUORUM read from dc-na; GC would authorize deleting a live block")
	}

	t.Log("divergent state confirmed: LOCAL_QUORUM from dc-na is blind, EACH_QUORUM sees the dc-eu reference — this is exactly the gap the fix closes")
}

// TestX2_EachQuorumFailsClosedWhenADatacenterIsDown proves the availability side of
// the contract. With a DC stopped the destructive read must ERROR, never report
// zero — GC treats the error as "do not delete", so a false zero here is data loss.
//
//	docker compose -f docker-compose.cassandra-3dc.yaml stop cassandra-asia
//	X2_EXPECT_DC_DOWN=1 go test -tags integration ./internal/integration/ -run TestX2_EachQuorumFailsClosed -v
func TestX2_EachQuorumFailsClosedWhenADatacenterIsDown(t *testing.T) {
	endpoints := x2DCEndpoints(t)
	if strings.TrimSpace(os.Getenv("X2_EXPECT_DC_DOWN")) == "" {
		t.Skip("X2_EXPECT_DC_DOWN not set; skipping the DC-unavailable leg (stop one DC first)")
	}

	reader := x2ConnectToDC(t, "dc-na", endpoints)

	orgID := uuid.NewString()
	blockID := "x2-down-" + uuid.NewString()

	hasRefs, err := reader.BlockHasReferencesGlobal(orgID, blockID)
	if err == nil {
		t.Fatalf("X2 REGRESSION: EACH_QUORUM read succeeded (hasRefs=%v) with a datacenter down; it must fail so GC fails closed", hasRefs)
	}
	t.Logf("EACH_QUORUM correctly failed closed with a datacenter down: %v", err)

	// The local read should still work — which is precisely why it must never be
	// the read that authorizes a delete.
	if _, localErr := reader.BlockHasReferences(orgID, blockID); localErr != nil {
		t.Logf("note: the LOCAL_QUORUM read also failed (%v); the local DC may be degraded too", localErr)
	}
}

// TestX2_FailsClosedWhenTheReferenceDatacenterIsDown is the leg that makes the
// three-datacenter fixture worth building, and the only one that discriminates the
// plausible WRONG fix from the right one.
//
// The documentation has always argued that `QUORUM` is not an acceptable substitute
// for `EACH_QUORUM`: at three DCs with RF 1 it is 2 of 3, so it can be satisfied
// entirely by datacenters that never saw the reference. Until this test that argument
// lived only in prose. It is now executable.
//
// The fixture is the divergent state (reference acknowledged in dc-eu alone) with
// **dc-eu stopped** — the datacenter holding the only copy:
//
//	EACH_QUORUM  needs a quorum in dc-eu     → cannot be reached → ERROR → fail closed
//	QUORUM       needs 2 of 3, and dc-na + dc-asia answer → both blind → FALSE
//	LOCAL_QUORUM needs dc-na alone, blind                              → FALSE
//
// A FALSE here is not a missed optimisation, it is the authorization to destroy a
// block that is still referenced. So this test asserts the read ERRORS, and it goes
// red under either downgrade. `scripts/x2-multidc-validation.sh --mutate-quorum` runs
// exactly that proof.
//
// Note what leg 2 (a DC down that does NOT hold the reference) can and cannot do. It
// also goes red under QUORUM, because QUORUM succeeds where EACH_QUORUM must error —
// but succeeding is not the same as authorizing a delete of live data, and only this
// leg shows the false zero itself. The divergent state must be FRESH: leg 1's
// EACH_QUORUM read performs blocking read repair, which propagates the row to dc-na
// and would make QUORUM answer true for the right reason by accident.
func TestX2_FailsClosedWhenTheReferenceDatacenterIsDown(t *testing.T) {
	endpoints := x2DCEndpoints(t)
	if strings.TrimSpace(os.Getenv("X2_EXPECT_REFERENCE_DC_DOWN")) == "" {
		t.Skip("X2_EXPECT_REFERENCE_DC_DOWN not set; skipping (stop dc-eu against a freshly divergent cluster first)")
	}

	blockID := strings.TrimSpace(os.Getenv("X2_DIVERGENT_BLOCK"))
	orgID := strings.TrimSpace(os.Getenv("X2_DIVERGENT_ORG"))
	if blockID == "" || orgID == "" {
		t.Skip("X2_DIVERGENT_BLOCK/X2_DIVERGENT_ORG not set; skipping (the divergent state must be built first — see docs/GC-X2-MULTIDC-VALIDATION.md)")
	}

	reader := x2ConnectToDC(t, "dc-na", endpoints)

	hasRefs, err := reader.BlockHasReferencesGlobal(orgID, blockID)
	if err != nil {
		t.Logf("destructive read correctly failed closed with the reference datacenter down: %v", err)
		return
	}
	if hasRefs {
		// Not the failure this leg is built to catch, but not a pass either: the row
		// reached dc-na or dc-asia, so the fixture is no longer divergent and a green
		// result here would mean nothing. Read repair from an earlier leg does this.
		t.Fatalf("the cluster is not divergent any more: the read from dc-na found the reference with dc-eu down, so this run cannot distinguish EACH_QUORUM from QUORUM. Rebuild the divergent state before trusting a result")
	}
	t.Fatalf("X2 REGRESSION: the destructive read returned zero references while dc-eu — the only datacenter holding one — was unreachable. GC would authorize deleting a live block. A read that can be satisfied without every datacenter (QUORUM, LOCAL_QUORUM) is not an acceptable authorization for a physical delete")
}

// TestX2_TopologyGateAcceptsThreeDCNetworkTopology checks the gate agrees the
// cluster can carry the per-DC argument. If this fails on a correct 3-DC stack the
// gate is too strict and would refuse to collect in production.
func TestX2_TopologyGateAcceptsThreeDCNetworkTopology(t *testing.T) {
	endpoints := x2DCEndpoints(t)
	database := x2ConnectToDC(t, "dc-na", endpoints)

	if err := database.ValidateDestructiveGCTopology(); err != nil {
		t.Fatalf("destructive topology gate rejected a valid 3-DC NetworkTopologyStrategy keyspace: %v", err)
	}
}

// TestX2_TopologyGateRejectsAnUnderDeclaredMap is the other half of the gate leg.
// Accepting a correct topology proves the gate is not too strict; this proves it is
// not too lax, against a real keyspace rather than a synthetic map.
//
// A process that declares only dc-na while the keyspace replicates to three DCs is
// exactly the shape a shrunk `ALTER KEYSPACE` leaves behind: structurally valid
// NetworkTopologyStrategy, positive RF, local DC present — and an EACH_QUORUM read
// that is no longer obliged to contact the DCs whose acknowledged references it would
// be authorizing the deletion of.
func TestX2_TopologyGateRejectsAnUnderDeclaredMap(t *testing.T) {
	endpoints := x2DCEndpoints(t)

	database, err := dbpkg.New(config.DatabaseConfig{
		Hosts:             []string{x2Endpoint(t, endpoints, "dc-na")},
		Keyspace:          envOrDefault("CASSANDRA_KEYSPACE", "sesamefs"),
		Consistency:       "LOCAL_QUORUM",
		SerialConsistency: "SERIAL",
		LocalDC:           "dc-na",
		ReplicationClass:  "NetworkTopologyStrategy",
		ReplicationDCs:    map[string]int{"dc-na": 1},
		Username:          os.Getenv("CASSANDRA_USERNAME"),
		Password:          os.Getenv("CASSANDRA_PASSWORD"),
	})
	if err != nil {
		t.Fatalf("connect to dc-na: %v", err)
	}
	t.Cleanup(database.Close)

	if err := database.ValidateDestructiveGCTopology(); err == nil {
		t.Fatal("X2 REGRESSION: the topology gate accepted a session declaring only dc-na against a three-DC keyspace; a shrunk replication map would silently invalidate the per-datacenter EACH_QUORUM argument")
	} else {
		t.Logf("topology gate correctly refused an under-declared map: %v", err)
	}
}

// TestX2_WriteReferenceForDivergence is a helper, not an assertion. Run it against
// dc-eu while the other two DCs are stopped to create the divergent state, then
// feed the printed ids back through X2_DIVERGENT_ORG/X2_DIVERGENT_BLOCK.
//
//	X2_WRITE_DIVERGENT=1 go test -tags integration ./internal/integration/ -run TestX2_WriteReferenceForDivergence -v
func TestX2_WriteReferenceForDivergence(t *testing.T) {
	endpoints := x2DCEndpoints(t)
	if strings.TrimSpace(os.Getenv("X2_WRITE_DIVERGENT")) == "" {
		t.Skip("X2_WRITE_DIVERGENT not set; skipping the divergent-state writer")
	}

	writer := x2ConnectToDC(t, "dc-eu", endpoints)

	orgID := uuid.NewString()
	blockID := "x2-" + uuid.NewString()
	referrer := "fs:" + uuid.NewString()

	if err := writer.AddBlockReference(orgID, blockID, referrer, uuid.NewString(), 0); err != nil {
		t.Fatalf("write reference at LOCAL_QUORUM in dc-eu: %v", err)
	}

	t.Logf("divergent reference written in dc-eu only. Re-run the visibility test with:")
	t.Logf("  X2_DIVERGENT_ORG=%s", orgID)
	t.Logf("  X2_DIVERGENT_BLOCK=%s", blockID)
}
