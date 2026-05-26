package db

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

const defaultCassandraTimeout = 10 * time.Second

// DB wraps the Cassandra session.
type DB struct {
	session *gocql.Session
	config  config.DatabaseConfig
}

type cassandraKeyspaceMetadata struct {
	Exists      bool
	Replication cassandraReplicationSettings
}

type cassandraReplicationSettings struct {
	Class   string
	Options map[string]string
}

// New creates a new database connection.
// It first connects without a keyspace to ensure the keyspace exists, then
// reconnects with the keyspace set.
func New(cfg config.DatabaseConfig) (*DB, error) {
	// Bootstrap: connect without keyspace to inspect it before reconnecting with
	// the configured keyspace selected.
	bootstrapCluster := newCluster(cfg)
	bootstrapSession, err := bootstrapCluster.CreateSession()
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Cassandra: %w", err)
	}
	keyspaceMeta, err := loadKeyspaceMetadata(bootstrapSession, cfg.Keyspace)
	if err != nil {
		bootstrapSession.Close()
		return nil, fmt.Errorf("failed to inspect Cassandra keyspace %s: %w", cfg.Keyspace, err)
	}
	if !keyspaceMeta.Exists {
		bootstrapSession.Close()
		return nil, missingKeyspaceError(cfg.Keyspace)
	}
	bootstrapSession.Close()

	// Reconnect with the keyspace set.
	cluster := newCluster(cfg)
	cluster.Keyspace = cfg.Keyspace
	logCassandraRuntimeConfig(cfg, cluster, keyspaceMeta)
	session, err := cluster.CreateSession()
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Cassandra keyspace %s: %w", cfg.Keyspace, err)
	}

	return &DB{session: session, config: cfg}, nil
}

func loadKeyspaceMetadata(session *gocql.Session, keyspace string) (cassandraKeyspaceMetadata, error) {
	var existing string
	replication := map[string]string{}
	err := session.Query(
		`SELECT keyspace_name, replication FROM system_schema.keyspaces WHERE keyspace_name = ? LIMIT 1`,
		keyspace,
	).Consistency(gocql.One).Scan(&existing, &replication)
	if err == gocql.ErrNotFound {
		return cassandraKeyspaceMetadata{}, nil
	}
	if err != nil {
		return cassandraKeyspaceMetadata{}, err
	}
	return cassandraKeyspaceMetadata{
		Exists:      existing == keyspace,
		Replication: parseSystemKeyspaceReplication(replication),
	}, nil
}

func missingKeyspaceError(keyspace string) error {
	return fmt.Errorf("keyspace %s does not exist; run cassandra-bootstrap first", keyspace)
}

// newCluster creates a gocql ClusterConfig from our config (without keyspace).
func newCluster(cfg config.DatabaseConfig) *gocql.ClusterConfig {
	cluster := gocql.NewCluster(cfg.Hosts...)
	cluster.Consistency = parseConsistency(cfg.Consistency)
	cluster.SerialConsistency = parseSerialConsistency(cfg.SerialConsistency)
	queryTimeout := cfg.Timeout
	if queryTimeout <= 0 {
		queryTimeout = defaultCassandraTimeout
	}
	cluster.Timeout = queryTimeout
	cluster.ConnectTimeout = defaultCassandraTimeout

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

func logCassandraRuntimeConfig(cfg config.DatabaseConfig, cluster *gocql.ClusterConfig, keyspaceMeta cassandraKeyspaceMetadata) {
	configuredReplication := configuredReplicationSettings(cfg)
	actualReplication := configuredReplication
	actualReplicationSource := "configured"
	if keyspaceMeta.Exists {
		actualReplication = keyspaceMeta.Replication
		actualReplicationSource = "keyspace"
	}

	hostSelection := "token-aware round robin"
	if strings.TrimSpace(cfg.LocalDC) != "" {
		hostSelection = fmt.Sprintf("dc-aware round robin (local_dc=%s)", strings.TrimSpace(cfg.LocalDC))
	}

	log.Printf(
		"[db] Cassandra runtime config: hosts=%s keyspace=%s consistency=%s serial_consistency=%s timeout=%s connect_timeout=%s local_dc=%q configured_replication_class=%s configured_replication=%s actual_replication_source=%s actual_replication_class=%s actual_replication=%s host_selection=%s",
		strings.Join(cfg.Hosts, ","),
		cfg.Keyspace,
		cluster.Consistency.String(),
		cluster.SerialConsistency.String(),
		cluster.Timeout,
		cluster.ConnectTimeout,
		cfg.LocalDC,
		configuredReplication.Class,
		formatReplicationOptions(configuredReplication.Options),
		actualReplicationSource,
		actualReplication.Class,
		formatReplicationOptions(actualReplication.Options),
		hostSelection,
	)

	if keyspaceMeta.Exists && !sameReplicationSettings(configuredReplication, actualReplication) {
		log.Printf(
			"[db] WARNING: Cassandra keyspace replication differs from configured replication: configured_class=%s configured_replication=%s actual_class=%s actual_replication=%s",
			configuredReplication.Class,
			formatReplicationOptions(configuredReplication.Options),
			actualReplication.Class,
			formatReplicationOptions(actualReplication.Options),
		)
	}

	if cluster.SerialConsistency == gocql.LocalSerial && isMultiRegionNetworkTopology(actualReplication) {
		log.Printf(
			"[db] WARNING: serial_consistency=LOCAL_SERIAL with multi-region %s replication (%s %s); LWT/CAS will only serialize within the local DC",
			actualReplicationSource,
			actualReplication.Class,
			formatReplicationOptions(actualReplication.Options),
		)
	}
}

func configuredReplicationSettings(cfg config.DatabaseConfig) cassandraReplicationSettings {
	class := normalizeReplicationClass(cfg.ReplicationClass)
	options := map[string]string{}

	switch class {
	case "", "NetworkTopologyStrategy":
		class = "NetworkTopologyStrategy"
		if len(cfg.ReplicationDCs) == 0 {
			if localDC := strings.TrimSpace(cfg.LocalDC); localDC != "" {
				options[localDC] = "1"
			}
		} else {
			for dc, rf := range cfg.ReplicationDCs {
				options[dc] = strconv.Itoa(rf)
			}
		}
	case "SimpleStrategy":
		if cfg.ReplicationFactor > 0 {
			options["replication_factor"] = strconv.Itoa(cfg.ReplicationFactor)
		}
	default:
		class = strings.TrimSpace(cfg.ReplicationClass)
		for dc, rf := range cfg.ReplicationDCs {
			options[dc] = strconv.Itoa(rf)
		}
		if cfg.ReplicationFactor > 0 {
			options["replication_factor"] = strconv.Itoa(cfg.ReplicationFactor)
		}
	}

	return cassandraReplicationSettings{
		Class:   class,
		Options: options,
	}
}

func parseSystemKeyspaceReplication(replication map[string]string) cassandraReplicationSettings {
	options := make(map[string]string, len(replication))
	for key, value := range replication {
		normalizedKey := strings.TrimSpace(key)
		if normalizedKey == "" || strings.EqualFold(normalizedKey, "class") {
			continue
		}
		options[normalizedKey] = strings.TrimSpace(value)
	}
	return cassandraReplicationSettings{
		Class:   normalizeReplicationClass(replication["class"]),
		Options: options,
	}
}

func normalizeReplicationClass(raw string) string {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case normalized == "":
		return ""
	case normalized == "networktopologystrategy", strings.HasSuffix(normalized, ".networktopologystrategy"):
		return "NetworkTopologyStrategy"
	case normalized == "simplestrategy", strings.HasSuffix(normalized, ".simplestrategy"):
		return "SimpleStrategy"
	default:
		return strings.TrimSpace(raw)
	}
}

func formatReplicationOptions(options map[string]string) string {
	if len(options) == 0 {
		return ""
	}
	keys := make([]string, 0, len(options))
	for key := range options {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s:%s", key, options[key]))
	}
	return strings.Join(parts, ",")
}

func sameReplicationSettings(a, b cassandraReplicationSettings) bool {
	return a.Class == b.Class && formatReplicationOptions(a.Options) == formatReplicationOptions(b.Options)
}

func isMultiRegionNetworkTopology(replication cassandraReplicationSettings) bool {
	if normalizeReplicationClass(replication.Class) != "NetworkTopologyStrategy" {
		return false
	}
	dcs := 0
	for key := range replication.Options {
		if strings.EqualFold(strings.TrimSpace(key), "replication_factor") {
			continue
		}
		dcs++
	}
	return dcs > 1
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
	switch strings.ToUpper(strings.TrimSpace(s)) {
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

func parseSerialConsistency(s string) gocql.Consistency {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "LOCAL_SERIAL":
		return gocql.LocalSerial
	case "", "SERIAL":
		return gocql.Serial
	default:
		return gocql.Serial
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
