package db

import (
	"strings"
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/config"
)

// The destructive GC topology gate decides whether the per-datacenter EACH_QUORUM
// argument that closes ISSUE-GC-CROSS-DC-REFERENCE-VISIBILITY-01 actually applies to
// the cluster in front of us. These exercise the decision directly, against synthetic
// replication maps, because the session read around it is not what can be wrong.

func prodLikeConfig() config.DatabaseConfig {
	return config.DatabaseConfig{
		Keyspace:         "sesamefs",
		LocalDC:          "dc-na",
		ReplicationClass: "NetworkTopologyStrategy",
		ReplicationDCs:   map[string]int{"dc-na": 1, "dc-eu": 1, "dc-asia": 1},
	}
}

func ntsLive(options map[string]string) cassandraReplicationSettings {
	return cassandraReplicationSettings{Class: "NetworkTopologyStrategy", Options: options}
}

func TestValidateDestructiveGCTopologyAcceptsTheDeclaredThreeDCMap(t *testing.T) {
	err := validateDestructiveGCTopology(
		ntsLive(map[string]string{"dc-na": "1", "dc-eu": "1", "dc-asia": "1"}),
		prodLikeConfig(),
	)
	if err != nil {
		t.Fatalf("gate rejected the declared production topology: %v", err)
	}
}

func TestValidateDestructiveGCTopologyAcceptsASingleDCMap(t *testing.T) {
	// EACH_QUORUM and LOCAL_QUORUM denote the same quorum here, so the cross-DC
	// argument is vacuous — but vacuously true, not violated: there is no second DC
	// whose acknowledged write could be missed. The gate rejects topologies where
	// EACH_QUORUM carries no per-DC meaning, it does not require multi-DC.
	err := validateDestructiveGCTopology(
		ntsLive(map[string]string{"dc-na": "1"}),
		config.DatabaseConfig{
			Keyspace:         "sesamefs",
			LocalDC:          "dc-na",
			ReplicationClass: "NetworkTopologyStrategy",
			ReplicationDCs:   map[string]int{"dc-na": 1},
		},
	)
	if err != nil {
		t.Fatalf("gate rejected a valid single-DC NetworkTopologyStrategy map: %v", err)
	}
}

func TestValidateDestructiveGCTopologyRejectsSimpleStrategy(t *testing.T) {
	cfg := prodLikeConfig()
	cfg.ReplicationClass = "SimpleStrategy"
	cfg.ReplicationDCs = nil
	cfg.ReplicationFactor = 3

	err := validateDestructiveGCTopology(
		cassandraReplicationSettings{Class: "SimpleStrategy", Options: map[string]string{"replication_factor": "3"}},
		cfg,
	)
	if err == nil {
		t.Fatal("gate accepted SimpleStrategy, where EACH_QUORUM has no per-datacenter quorum to intersect")
	}
	if !strings.Contains(err.Error(), "NetworkTopologyStrategy") {
		t.Errorf("error should name the required strategy, got: %v", err)
	}
}

// TestValidateDestructiveGCTopologyRejectsAShrunkMap is the reason the gate compares
// maps at all rather than only checking shape.
//
// The quorum-intersection proof is about the replica set that ACCEPTED each reference
// write, not the one in effect when GC reads. Drop dc-eu and dc-asia from the map
// after references were acknowledged there and every structural check still passes —
// NetworkTopologyStrategy, positive RF, local DC present — while EACH_QUORUM quietly
// stops being obliged to contact dc-eu at all. Cassandra does not relocate historical
// data on ALTER, so those references are simply no longer reachable by the read that
// authorizes deleting their blocks.
func TestValidateDestructiveGCTopologyRejectsAShrunkMap(t *testing.T) {
	live := ntsLive(map[string]string{"dc-na": "1"})

	// The shrunk map is structurally impeccable on its own terms.
	if err := validateDestructiveGCTopology(live, config.DatabaseConfig{
		Keyspace:         "sesamefs",
		LocalDC:          "dc-na",
		ReplicationClass: "NetworkTopologyStrategy",
		ReplicationDCs:   map[string]int{"dc-na": 1},
	}); err != nil {
		t.Fatalf("precondition: the shrunk map should pass every structural check, got %v", err)
	}

	// Against the topology the fleet actually declares, it must be refused.
	err := validateDestructiveGCTopology(live, prodLikeConfig())
	if err == nil {
		t.Fatal("gate accepted a live map that lost dc-eu/dc-asia; EACH_QUORUM no longer intersects the writes those DCs acknowledged")
	}
	if !strings.Contains(err.Error(), "GC_ENABLED=false") {
		t.Errorf("error should tell the operator how to change topology safely, got: %v", err)
	}
}

func TestValidateDestructiveGCTopologyRejectsAChangedReplicationFactor(t *testing.T) {
	err := validateDestructiveGCTopology(
		ntsLive(map[string]string{"dc-na": "1", "dc-eu": "2", "dc-asia": "1"}),
		prodLikeConfig(),
	)
	if err == nil {
		t.Fatal("gate accepted a live map whose dc-eu replication factor drifted from the declared one")
	}
}

func TestValidateDestructiveGCTopologyRejectsAMissingLocalDC(t *testing.T) {
	cfg := prodLikeConfig()
	cfg.LocalDC = "dc-melbourne"

	err := validateDestructiveGCTopology(
		ntsLive(map[string]string{"dc-na": "1", "dc-eu": "1", "dc-asia": "1"}),
		cfg,
	)
	if err == nil {
		t.Fatal("gate accepted a keyspace that does not replicate to the coordinator's own datacenter")
	}
	if !strings.Contains(err.Error(), "dc-melbourne") {
		t.Errorf("error should name the local DC, got: %v", err)
	}
}

func TestValidateDestructiveGCTopologyRejectsZeroReplicationFactor(t *testing.T) {
	cfg := prodLikeConfig()
	cfg.ReplicationDCs = map[string]int{"dc-na": 1, "dc-eu": 0}

	err := validateDestructiveGCTopology(
		ntsLive(map[string]string{"dc-na": "1", "dc-eu": "0"}),
		cfg,
	)
	if err == nil {
		t.Fatal("gate accepted a datacenter with RF 0, which holds no replicas for EACH_QUORUM to reach")
	}
}

func TestValidateDestructiveGCTopologyRejectsAnEmptyMap(t *testing.T) {
	err := validateDestructiveGCTopology(ntsLive(map[string]string{}), prodLikeConfig())
	if err == nil {
		t.Fatal("gate accepted a NetworkTopologyStrategy keyspace with no datacenters in its map")
	}
}
