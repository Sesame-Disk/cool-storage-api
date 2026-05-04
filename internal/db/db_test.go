package db

import (
	"strings"
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/config"
)

func TestCreateKeyspaceCQLSimpleStrategy(t *testing.T) {
	query, err := createKeyspaceCQL(config.DatabaseConfig{
		Keyspace:          "sesamefs",
		ReplicationClass:  "SimpleStrategy",
		ReplicationFactor: 1,
	})
	if err != nil {
		t.Fatalf("createKeyspaceCQL() error = %v", err)
	}
	if !strings.Contains(query, "'class': 'SimpleStrategy'") {
		t.Fatalf("createKeyspaceCQL() = %s, want SimpleStrategy", query)
	}
	if !strings.Contains(query, "'replication_factor': 1") {
		t.Fatalf("createKeyspaceCQL() = %s, want replication_factor 1", query)
	}
}

func TestCreateKeyspaceCQLNetworkTopologyStrategy(t *testing.T) {
	query, err := createKeyspaceCQL(config.DatabaseConfig{
		Keyspace:         "sesamefs",
		ReplicationClass: "NetworkTopologyStrategy",
		ReplicationDCs: map[string]int{
			"dc-eu": 1,
			"dc-na": 2,
		},
	})
	if err != nil {
		t.Fatalf("createKeyspaceCQL() error = %v", err)
	}
	if !strings.Contains(query, "'class': 'NetworkTopologyStrategy'") {
		t.Fatalf("createKeyspaceCQL() = %s, want NetworkTopologyStrategy", query)
	}
	if !strings.Contains(query, "'dc-eu': 1") || !strings.Contains(query, "'dc-na': 2") {
		t.Fatalf("createKeyspaceCQL() = %s, want per-DC replication", query)
	}
}

func TestCreateKeyspaceCQLEmptyClassDefaultsToNetworkTopologyStrategy(t *testing.T) {
	query, err := createKeyspaceCQL(config.DatabaseConfig{
		Keyspace: "sesamefs",
		LocalDC:  "datacenter1",
	})
	if err != nil {
		t.Fatalf("createKeyspaceCQL() error = %v", err)
	}
	if !strings.Contains(query, "'class': 'NetworkTopologyStrategy'") {
		t.Fatalf("createKeyspaceCQL() = %s, want NetworkTopologyStrategy", query)
	}
	if !strings.Contains(query, "'datacenter1': 1") {
		t.Fatalf("createKeyspaceCQL() = %s, want datacenter1:1", query)
	}
}

func TestMissingKeyspaceError(t *testing.T) {
	err := missingKeyspaceError("sesamefs")
	if err == nil {
		t.Fatal("missingKeyspaceError() = nil, want error")
	}
	if got := err.Error(); got != "keyspace sesamefs does not exist; run cassandra-bootstrap first" {
		t.Fatalf("missingKeyspaceError() = %q, want clear bootstrap guidance", got)
	}
}
