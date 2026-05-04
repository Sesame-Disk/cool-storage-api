package db

import (
	"context"
	"fmt"
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
		if err := bootstrapSession.Query(createKeyspaceCQL(cfg.Keyspace)).Exec(); err != nil {
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

// createKeyspaceCQL returns the CQL to create the keyspace if it does not exist.
// Replication factor is kept at 1 for the bootstrap query; production deployments
// should set replication_factor = 3 via an out-of-band ALTER KEYSPACE after the
// cluster is provisioned with multiple nodes.
func createKeyspaceCQL(keyspace string) string {
	return fmt.Sprintf(`CREATE KEYSPACE IF NOT EXISTS %s WITH replication = {
		'class': 'SimpleStrategy',
		'replication_factor': 1
	}`, keyspace)
}
