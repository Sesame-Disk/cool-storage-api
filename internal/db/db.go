package db

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

// DB wraps the Cassandra session.
type DB struct {
	session *gocql.Session
	config  config.DatabaseConfig
}

// New creates a new database connection.
// It first connects without a keyspace to ensure the keyspace can be created,
// then reconnects with the keyspace set.
func New(cfg config.DatabaseConfig) (*DB, error) {
	// Bootstrap: connect without keyspace to create it if it does not exist.
	bootstrapCluster := newCluster(cfg)
	bootstrapSession, err := bootstrapCluster.CreateSession()
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Cassandra: %w", err)
	}
	keyspaceExists, err := keyspaceExists(bootstrapSession, cfg.Keyspace)
	if err != nil {
		bootstrapSession.Close()
		return nil, fmt.Errorf("failed to inspect Cassandra keyspace %s: %w", cfg.Keyspace, err)
	}
	if !keyspaceExists {
		createKeyspaceCQL, err := createKeyspaceCQL(cfg)
		if err != nil {
			bootstrapSession.Close()
			return nil, fmt.Errorf("failed to build keyspace CQL for %s: %w", cfg.Keyspace, err)
		}
		if err := bootstrapSession.Query(createKeyspaceCQL).Exec(); err != nil {
			bootstrapSession.Close()
			return nil, fmt.Errorf("failed to create missing keyspace %s: %w", cfg.Keyspace, err)
		}
	}
	bootstrapSession.Close()

	// Reconnect with the keyspace set.
	cluster := newCluster(cfg)
	cluster.Keyspace = cfg.Keyspace
	session, err := cluster.CreateSession()
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Cassandra keyspace %s: %w", cfg.Keyspace, err)
	}

	return &DB{session: session, config: cfg}, nil
}

func keyspaceExists(session *gocql.Session, keyspace string) (bool, error) {
	var existing string
	err := session.Query(
		`SELECT keyspace_name FROM system_schema.keyspaces WHERE keyspace_name = ? LIMIT 1`,
		keyspace,
	).Consistency(gocql.One).Scan(&existing)
	if err == gocql.ErrNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return existing == keyspace, nil
}

// newCluster creates a gocql ClusterConfig from our config (without keyspace).
func newCluster(cfg config.DatabaseConfig) *gocql.ClusterConfig {
	cluster := gocql.NewCluster(cfg.Hosts...)
	cluster.Consistency = parseConsistency(cfg.Consistency)
	cluster.Timeout = 10 * time.Second
	cluster.ConnectTimeout = 10 * time.Second

	if cfg.LocalDC != "" {
		cluster.PoolConfig.HostSelectionPolicy = gocql.DCAwareRoundRobinPolicy(cfg.LocalDC)
	}
	if cfg.Username != "" && cfg.Password != "" {
		cluster.Authenticator = gocql.PasswordAuthenticator{
			Username: cfg.Username,
			Password: cfg.Password,
		}
	}

	return cluster
}

// Close closes the database connection.
func (db *DB) Close() {
	if db.session != nil {
		db.session.Close()
	}
}

// Session returns the underlying gocql session.
func (db *DB) Session() *gocql.Session {
	return db.session
}

// Ping verifies database connectivity by executing a lightweight query.
func (db *DB) Ping(ctx context.Context) error {
	return db.session.Query(`SELECT now() FROM system.local`).ExecContext(ctx)
}

// Migrate runs the schema migration runner.
func (db *DB) Migrate() error {
	m := NewMigrator(db.session)
	if err := m.Run(); err != nil {
		return err
	}
	return nil
}

// parseConsistency converts a string to a gocql.Consistency level.
func parseConsistency(s string) gocql.Consistency {
	switch s {
	case "ONE":
		return gocql.One
	case "QUORUM":
		return gocql.Quorum
	case "LOCAL_QUORUM":
		return gocql.LocalQuorum
	case "EACH_QUORUM":
		return gocql.EachQuorum
	case "ALL":
		return gocql.All
	default:
		return gocql.LocalQuorum
	}
}

// createKeyspaceCQL returns the CQL to create the keyspace if it does not exist,
// using the configured replication strategy so single-node dev and multi-DC prod
// converge on the same declarative settings.
func createKeyspaceCQL(cfg config.DatabaseConfig) (string, error) {
	replicationCQL, err := replicationConfigCQL(cfg)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("CREATE KEYSPACE IF NOT EXISTS %s WITH replication = %s", cfg.Keyspace, replicationCQL), nil
}

func replicationConfigCQL(cfg config.DatabaseConfig) (string, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.ReplicationClass)) {
	case "":
		if len(cfg.ReplicationDCs) == 0 {
			localDC := strings.TrimSpace(cfg.LocalDC)
			if localDC == "" {
				return "", fmt.Errorf("NetworkTopologyStrategy requires local_dc when replication_dcs is empty")
			}
			cfg.ReplicationDCs = map[string]int{localDC: 1}
		}
		fallthrough
	case "networktopologystrategy":
		if len(cfg.ReplicationDCs) == 0 {
			return "", fmt.Errorf("NetworkTopologyStrategy requires replication_dcs")
		}
		keys := make([]string, 0, len(cfg.ReplicationDCs))
		for dc, rf := range cfg.ReplicationDCs {
			if strings.TrimSpace(dc) == "" {
				return "", fmt.Errorf("NetworkTopologyStrategy requires non-empty datacenter names")
			}
			if rf <= 0 {
				return "", fmt.Errorf("NetworkTopologyStrategy requires replication factor > 0 for datacenter %s", dc)
			}
			keys = append(keys, dc)
		}
		sort.Strings(keys)
		parts := []string{"'class': 'NetworkTopologyStrategy'"}
		for _, dc := range keys {
			parts = append(parts, fmt.Sprintf("'%s': %d", dc, cfg.ReplicationDCs[dc]))
		}
		return "{" + strings.Join(parts, ", ") + "}", nil
	case "simplestrategy":
		if cfg.ReplicationFactor <= 0 {
			return "", fmt.Errorf("SimpleStrategy requires replication_factor > 0")
		}
		return fmt.Sprintf("{'class': 'SimpleStrategy', 'replication_factor': %d}", cfg.ReplicationFactor), nil
	default:
		return "", fmt.Errorf("unsupported replication class %q", cfg.ReplicationClass)
	}
}
