package db

import (
	"bytes"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
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

func TestNewClusterUsesExplicitSerialConsistency(t *testing.T) {
	cluster := newCluster(config.DatabaseConfig{
		Hosts:             []string{"127.0.0.1:9042"},
		Consistency:       "LOCAL_QUORUM",
		SerialConsistency: "SERIAL",
		Timeout:           25 * time.Second,
		LocalDC:           "dc-na",
	})

	if cluster.SerialConsistency != gocql.Serial {
		t.Fatalf("newCluster() serial consistency = %v, want %v", cluster.SerialConsistency, gocql.Serial)
	}
}

func TestNewClusterUsesConfiguredLocalSerialConsistency(t *testing.T) {
	cluster := newCluster(config.DatabaseConfig{
		Hosts:             []string{"127.0.0.1:9042"},
		Consistency:       "LOCAL_QUORUM",
		SerialConsistency: "LOCAL_SERIAL",
		Timeout:           25 * time.Second,
		LocalDC:           "dc-na",
	})

	if cluster.SerialConsistency != gocql.LocalSerial {
		t.Fatalf("newCluster() serial consistency = %v, want %v", cluster.SerialConsistency, gocql.LocalSerial)
	}
}

func TestConfiguredReplicationSettingsUsesLocalDCFallback(t *testing.T) {
	got := configuredReplicationSettings(config.DatabaseConfig{
		LocalDC: "dc-na",
	})

	if got.Class != "NetworkTopologyStrategy" {
		t.Fatalf("configuredReplicationSettings() class = %q, want NetworkTopologyStrategy", got.Class)
	}
	if formatted := formatReplicationOptions(got.Options); formatted != "dc-na:1" {
		t.Fatalf("configuredReplicationSettings() options = %q, want dc-na:1", formatted)
	}
}

func TestFormatReplicationOptionsSortsOutput(t *testing.T) {
	got := formatReplicationOptions(map[string]string{
		"dc-eu":   "1",
		"dc-na":   "1",
		"dc-asia": "1",
	})

	if got != "dc-asia:1,dc-eu:1,dc-na:1" {
		t.Fatalf("formatReplicationOptions() = %q, want deterministic sorted output", got)
	}
}

func TestLogCassandraRuntimeConfigUsesActualKeyspaceReplicationAndWarnsOnDrift(t *testing.T) {
	cfg := config.DatabaseConfig{
		Hosts:             []string{"127.0.0.1:9042"},
		Keyspace:          "sesamefs",
		Consistency:       "LOCAL_QUORUM",
		SerialConsistency: "SERIAL",
		Timeout:           25 * time.Second,
		LocalDC:           "dc-na",
		ReplicationClass:  "NetworkTopologyStrategy",
		ReplicationDCs: map[string]int{
			"dc-na": 1,
		},
	}

	output := captureLogOutput(t, func() {
		logCassandraRuntimeConfig(cfg, newCluster(cfg), cassandraKeyspaceMetadata{
			Exists: true,
			Replication: cassandraReplicationSettings{
				Class: "NetworkTopologyStrategy",
				Options: map[string]string{
					"dc-na": "1",
					"dc-eu": "1",
				},
			},
		})
	})

	if !strings.Contains(output, "configured_replication=dc-na:1") {
		t.Fatalf("logCassandraRuntimeConfig() output = %q, want configured replication in log", output)
	}
	if !strings.Contains(output, "actual_replication=dc-eu:1,dc-na:1") {
		t.Fatalf("logCassandraRuntimeConfig() output = %q, want actual keyspace replication in log", output)
	}
	if !strings.Contains(output, "WARNING: Cassandra keyspace replication differs from configured replication") {
		t.Fatalf("logCassandraRuntimeConfig() output = %q, want drift warning", output)
	}
}

func TestLogCassandraRuntimeConfigWarnsOnLocalSerialInMultiregion(t *testing.T) {
	cfg := config.DatabaseConfig{
		Hosts:             []string{"127.0.0.1:9042"},
		Keyspace:          "sesamefs",
		Consistency:       "LOCAL_QUORUM",
		SerialConsistency: "LOCAL_SERIAL",
		Timeout:           25 * time.Second,
		LocalDC:           "dc-na",
		ReplicationClass:  "NetworkTopologyStrategy",
		ReplicationDCs: map[string]int{
			"dc-na": 1,
			"dc-eu": 1,
		},
	}

	output := captureLogOutput(t, func() {
		logCassandraRuntimeConfig(cfg, newCluster(cfg), cassandraKeyspaceMetadata{
			Exists: true,
			Replication: cassandraReplicationSettings{
				Class: "NetworkTopologyStrategy",
				Options: map[string]string{
					"dc-na": "1",
					"dc-eu": "1",
				},
			},
		})
	})

	if !strings.Contains(output, "WARNING: serial_consistency=LOCAL_SERIAL") {
		t.Fatalf("logCassandraRuntimeConfig() output = %q, want LOCAL_SERIAL warning", output)
	}
	if !strings.Contains(output, "multi-region keyspace replication") {
		t.Fatalf("logCassandraRuntimeConfig() output = %q, want warning context about keyspace replication", output)
	}
	if !strings.Contains(output, "LWT/CAS without an explicit query override will only serialize within the local DC") {
		t.Fatalf("logCassandraRuntimeConfig() output = %q, want warning impact statement", output)
	}
}

func captureLogOutput(t *testing.T, fn func()) string {
	t.Helper()

	var buf bytes.Buffer
	originalWriter := log.Writer()
	originalFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(originalWriter)
		log.SetFlags(originalFlags)
	}()

	fn()
	return buf.String()
}
