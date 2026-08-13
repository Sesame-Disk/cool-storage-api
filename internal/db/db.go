package db

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
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
	if cfg.ProtoVersion > 0 {
		// Pin the CQL native protocol version. Defaults to 4 so gocql does not
		// negotiate v5, whose per-request keyspace flag makes Cassandra log
		// "Keyspace is set via query options. This is considered dangerous".
		cluster.ProtoVersion = cfg.ProtoVersion
	}
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
		"[db] Cassandra runtime config: hosts=%s keyspace=%s consistency=%s serial_consistency=%s proto_version=%d timeout=%s connect_timeout=%s local_dc=%q configured_replication_class=%s configured_replication=%s actual_replication_source=%s actual_replication_class=%s actual_replication=%s host_selection=%s",
		strings.Join(cfg.Hosts, ","),
		cfg.Keyspace,
		cluster.Consistency.String(),
		cluster.SerialConsistency.String(),
		cluster.ProtoVersion,
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

// ValidateDestructiveGCTopology reports whether the live keyspace replication makes
// the per-DC EACH_QUORUM liveness argument sound.
//
// Closing ISSUE-GC-CROSS-DC-REFERENCE-VISIBILITY-01 rests on a quorum-intersection
// argument that is stated PER DATACENTER: an EACH_QUORUM read must obtain a quorum
// in every DC, so it intersects the quorum that acknowledged a LOCAL_QUORUM
// reference write in whichever DC accepted it. That argument presumes
// NetworkTopologyStrategy with every replica-holding DC in the keyspace map. Under
// SimpleStrategy there are no per-DC quorums for EACH_QUORUM to intersect, and the
// closure does not hold — so the destructive path must refuse to run rather than
// delete under an argument that does not apply.
//
// This reads live keyspace metadata rather than configuration, because the
// deployment map comes from the environment (CASSANDRA_REPLICATION_DCS) and the
// checked-in profiles are not the source of truth about the fleet. It then requires
// the live map to equal the declared one — see the comment on that comparison for
// what it does and does not prove, which is narrower than "the topology cannot
// change".
//
// A single-DC NetworkTopologyStrategy map passes deliberately. There, EACH_QUORUM
// and LOCAL_QUORUM denote the same quorum, so the cross-DC argument is vacuous — but
// it is vacuously TRUE, not violated: there is no second DC whose acknowledged write
// could be missed. The gate exists to reject topologies where EACH_QUORUM carries no
// per-DC meaning at all, not to require multi-DC.
func (db *DB) ValidateDestructiveGCTopology() error {
	meta, err := loadKeyspaceMetadata(db.session, db.config.Keyspace)
	if err != nil {
		return fmt.Errorf("read keyspace replication for destructive GC gate: %w", err)
	}
	if !meta.Exists {
		return missingKeyspaceError(db.config.Keyspace)
	}
	if err := validateDestructiveGCTopology(meta.Replication, db.config); err != nil {
		return err
	}
	warnOnceAboutGlobalSerialConsistency(meta.Replication, db.config)
	return nil
}

// destructiveGCSerialWarning makes the advisory below fire once per process rather
// than once per candidate.
var destructiveGCSerialWarning sync.Once

// warnOnceAboutGlobalSerialConsistency flags a multi-DC deployment that serializes
// LWTs at SERIAL rather than LOCAL_SERIAL.
//
// This ADVISES, it does not reject, and the distinction is the point. SERIAL is not
// unsound — it is strictly stronger than LOCAL_SERIAL — so refusing to run under it
// would disable destructive GC over a configuration that is merely expensive. What it
// costs is availability: SERIAL needs a quorum across every DC, so a single remote DC
// going down takes ClaimBlockDelete with it while the LOCAL_QUORUM reads around it
// keep working. GC then stalls on an outage instead of degrading.
//
// The worker no longer turns that stall into loss — availability failures anywhere in
// the destructive walk postpone without consuming the retry budget (see
// isClusterUnavailableError) — so the remaining cost is throughput, which is an
// operator's call to make with the facts in front of them. Hence a warning at the
// place that already knows the datacenter map.
//
// The shipped multi-DC profiles (config-*.cluster.yaml) already use LOCAL_SERIAL; the
// single-DC ones declare SERIAL, where the two are the same quorum and this stays
// quiet.
//
// This does not contradict the LOCAL_SERIAL warning in logCassandraRuntimeConfig.
// That one states the cost of LOCAL_SERIAL (CAS serializes only within the local DC);
// this one states the cost of SERIAL (CAS needs every DC up). Both are real, they
// point opposite ways, and the choice between them is a deployment decision — which is
// exactly why neither one refuses to start.
func warnOnceAboutGlobalSerialConsistency(live cassandraReplicationSettings, cfg config.DatabaseConfig) {
	if !isMultiRegionNetworkTopology(live) {
		return
	}
	if !strings.EqualFold(strings.TrimSpace(cfg.SerialConsistency), "SERIAL") {
		return
	}
	destructiveGCSerialWarning.Do(func() {
		log.Printf(
			"[DB] WARNING: destructive GC is running against a multi-datacenter keyspace (%s) with serial_consistency=SERIAL. "+
				"Lightweight transactions then require a quorum in every datacenter, so one unreachable DC stalls GC's block claims "+
				"even though ordinary reads still succeed. LOCAL_SERIAL is the multi-DC standard; see configs/config-*.cluster.yaml",
			formatReplicationOptions(live.Options),
		)
	})
}

// validateDestructiveGCTopology holds the gate's decision logic, separated from the
// session read so it can be exercised directly against synthetic replication maps.
func validateDestructiveGCTopology(live cassandraReplicationSettings, cfg config.DatabaseConfig) error {
	keyspace := cfg.Keyspace

	if normalizeReplicationClass(live.Class) != "NetworkTopologyStrategy" {
		return fmt.Errorf(
			"destructive GC requires NetworkTopologyStrategy so EACH_QUORUM carries a per-datacenter quorum; keyspace %s uses %s",
			keyspace, live.Class,
		)
	}

	dcs := map[string]string{}
	for dc, rf := range live.Options {
		if strings.EqualFold(strings.TrimSpace(dc), "replication_factor") {
			continue
		}
		dcs[strings.TrimSpace(dc)] = strings.TrimSpace(rf)
	}
	if len(dcs) == 0 {
		return fmt.Errorf("destructive GC requires at least one datacenter in the replication map for keyspace %s", keyspace)
	}
	for dc, rf := range dcs {
		factor, convErr := strconv.Atoi(rf)
		if convErr != nil || factor < 1 {
			return fmt.Errorf("destructive GC requires a positive replication factor in every datacenter; %s has %q", dc, rf)
		}
	}

	// The coordinator's own DC must hold replicas, otherwise "every DC" as this node
	// understands it is not the same set the writers acknowledge into.
	localDC := strings.TrimSpace(cfg.LocalDC)
	if localDC != "" {
		if _, ok := dcs[localDC]; !ok {
			return fmt.Errorf(
				"destructive GC requires local_dc %q to hold replicas; keyspace %s replicates to %s",
				localDC, keyspace, formatReplicationOptions(live.Options),
			)
		}
	}

	// The live map must be EXACTLY the declared one. A structurally valid map is not
	// enough, because the quorum-intersection proof is about the replica set that
	// ACCEPTED each reference write, not the one in effect at read time. Shrinking
	// the map — `ALTER KEYSPACE ... {dc-na:1}` after references were acknowledged in
	// dc-eu — leaves every structural check above satisfied while EACH_QUORUM quietly
	// stops being obliged to contact dc-eu at all, and Cassandra does not move
	// historical data into the new replica set on its own.
	//
	// BE PRECISE ABOUT WHAT THIS PROVES. It compares the topology in effect NOW
	// against the topology this process was configured with NOW. That catches the
	// realistic accident — someone alters the keyspace and forgets the deployment
	// config, or vice versa — but it is NOT proof that the map is unchanged since the
	// references were written. An operator who changes both together
	// (ALTER KEYSPACE, then CASSANDRA_REPLICATION_DCS, then restart) passes this gate
	// while historical references still live in the dropped datacenters.
	//
	// Closing that hole in code would take a certified fingerprint: persist the
	// replication map at first destructive activation and require an explicit
	// recertification step after any topology change, so the check becomes
	// "today's topology == the certified one" rather than "today's topology ==
	// today's config". Deliberately not built here — it is a new piece of persisted
	// protocol, and the gap it closes is only reachable through a deliberate,
	// multi-step administrative change, which is precisely what the documented
	// procedure covers. Until then the remaining guarantee is operational, and stated
	// as such in the error below and in KNOWN_ISSUES: topology changes require GC off,
	// alter, repair, reconfigure, re-enable.
	declared := configuredReplicationSettings(cfg)
	if !sameReplicationSettings(declared, live) {
		return fmt.Errorf(
			"destructive GC requires the live keyspace replication to match the declared topology exactly; keyspace %s is %s %s but CASSANDRA_REPLICATION_DCS declares %s %s. "+
				"A replication map that changed after references were written invalidates the per-datacenter EACH_QUORUM argument. "+
				"To change topology: set GC_ENABLED=false everywhere, ALTER the keyspace, run the corresponding repair, update the declared map, then re-enable GC",
			keyspace,
			live.Class, formatReplicationOptions(live.Options),
			declared.Class, formatReplicationOptions(declared.Options),
		)
	}

	return nil
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
