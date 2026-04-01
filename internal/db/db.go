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
	if err := bootstrapSession.Query(createKeyspaceCQL(cfg.Keyspace)).Exec(); err != nil {
		bootstrapSession.Close()
		return nil, fmt.Errorf("failed to create keyspace: %w", err)
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

// Migrate runs the schema migration runner and then applies idempotent data
// backfills for columns that require Go-level logic (not expressible in CQL).
func (db *DB) Migrate() error {
	m := NewMigrator(db.session)
	if err := m.Run(); err != nil {
		return err
	}
	// Data backfills — idempotent; skip rows that are already populated.
	db.backfillUserStatus()
	db.backfillOrgStatus()
	return nil
}

// backfillUserStatus populates the status column for users that predate the
// status/role separation migration. Legacy data may carry role="deactivated"
// or role="deleted" which are split into the dedicated status column.
func (db *DB) backfillUserStatus() {
	orgIter := db.session.Query(`SELECT org_id FROM organizations`).Iter()
	var orgIDStr string
	for orgIter.Scan(&orgIDStr) {
		iter := db.session.Query(
			`SELECT user_id, role, status FROM users WHERE org_id = ?`, orgIDStr,
		).Iter()
		var userID, role, status string
		for iter.Scan(&userID, &role, &status) {
			if status != "" {
				continue // already backfilled
			}
			switch role {
			case "deactivated":
				db.session.Query(
					`UPDATE users SET status = ?, role = ? WHERE org_id = ? AND user_id = ?`,
					"deactivated", "user", orgIDStr, userID,
				).Exec()
			case "deleted":
				db.session.Query(
					`UPDATE users SET status = ? WHERE org_id = ? AND user_id = ?`,
					"deleted", orgIDStr, userID,
				).Exec()
			default:
				db.session.Query(
					`UPDATE users SET status = ? WHERE org_id = ? AND user_id = ?`,
					"active", orgIDStr, userID,
				).Exec()
			}
		}
		iter.Close()
	}
	orgIter.Close()
}

// backfillOrgStatus populates the status column for organizations that predate
// the migration. Legacy data stored lifecycle state in settings["status"].
func (db *DB) backfillOrgStatus() {
	iter := db.session.Query(
		`SELECT org_id, status, settings FROM organizations`,
	).Iter()
	var orgIDStr, status string
	var settings map[string]string
	for iter.Scan(&orgIDStr, &status, &settings) {
		if status != "" {
			continue // already backfilled
		}
		newStatus := "active"
		if legacyStatus, ok := settings["status"]; ok && legacyStatus != "" {
			newStatus = legacyStatus
		}
		db.session.Query(
			`UPDATE organizations SET status = ? WHERE org_id = ?`, newStatus, orgIDStr,
		).Exec()
	}
	iter.Close()
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
