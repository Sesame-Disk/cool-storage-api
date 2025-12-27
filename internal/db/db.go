package db

import (
	"fmt"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/apache/cassandra-gocql-driver/v2"
)

// DB wraps the Cassandra session
type DB struct {
	session *gocql.Session
	config  config.DatabaseConfig
}

// New creates a new database connection
func New(cfg config.DatabaseConfig) (*DB, error) {
	cluster := gocql.NewCluster(cfg.Hosts...)
	cluster.Keyspace = cfg.Keyspace
	cluster.Consistency = parseConsistency(cfg.Consistency)
	cluster.Timeout = 10 * time.Second
	cluster.ConnectTimeout = 10 * time.Second

	// Set local DC for multi-DC deployments
	if cfg.LocalDC != "" {
		cluster.PoolConfig.HostSelectionPolicy = gocql.DCAwareRoundRobinPolicy(cfg.LocalDC)
	}

	// Authentication
	if cfg.Username != "" && cfg.Password != "" {
		cluster.Authenticator = gocql.PasswordAuthenticator{
			Username: cfg.Username,
			Password: cfg.Password,
		}
	}

	session, err := cluster.CreateSession()
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Cassandra: %w", err)
	}

	return &DB{
		session: session,
		config:  cfg,
	}, nil
}

// Close closes the database connection
func (db *DB) Close() {
	if db.session != nil {
		db.session.Close()
	}
}

// Session returns the underlying gocql session
func (db *DB) Session() *gocql.Session {
	return db.session
}

// Migrate runs database migrations
func (db *DB) Migrate() error {
	migrations := []string{
		migrationCreateKeyspace,
		migrationCreateOrganizations,
		migrationCreateUsers,
		migrationCreateUsersByEmail,
		migrationCreateUsersByOIDC,
		migrationCreateLibraries,
		migrationCreateCommits,
		migrationCreateFSObjects,
		migrationCreateBlocks,
		migrationCreateShareLinks,
		migrationCreateShares,
		migrationCreateRestoreJobs,
	}

	for _, migration := range migrations {
		if err := db.session.Query(migration).Exec(); err != nil {
			return fmt.Errorf("migration failed: %w", err)
		}
	}

	return nil
}

// parseConsistency converts string to gocql.Consistency
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

// Migration statements
const migrationCreateKeyspace = `
CREATE KEYSPACE IF NOT EXISTS sesamefs WITH replication = {
	'class': 'SimpleStrategy',
	'replication_factor': 1
}`

const migrationCreateOrganizations = `
CREATE TABLE IF NOT EXISTS organizations (
	org_id UUID PRIMARY KEY,
	name TEXT,
	settings MAP<TEXT, TEXT>,
	storage_quota BIGINT,
	storage_used BIGINT,
	chunking_polynomial BIGINT,
	storage_config MAP<TEXT, TEXT>,
	created_at TIMESTAMP
)`

const migrationCreateUsers = `
CREATE TABLE IF NOT EXISTS users (
	org_id UUID,
	user_id UUID,
	email TEXT,
	name TEXT,
	role TEXT,
	oidc_sub TEXT,
	quota_bytes BIGINT,
	used_bytes BIGINT,
	created_at TIMESTAMP,
	PRIMARY KEY ((org_id), user_id)
)`

const migrationCreateUsersByEmail = `
CREATE TABLE IF NOT EXISTS users_by_email (
	email TEXT PRIMARY KEY,
	user_id UUID,
	org_id UUID
)`

const migrationCreateUsersByOIDC = `
CREATE TABLE IF NOT EXISTS users_by_oidc (
	oidc_issuer TEXT,
	oidc_sub TEXT,
	user_id UUID,
	org_id UUID,
	PRIMARY KEY ((oidc_issuer), oidc_sub)
)`

const migrationCreateLibraries = `
CREATE TABLE IF NOT EXISTS libraries (
	org_id UUID,
	library_id UUID,
	owner_id UUID,
	name TEXT,
	description TEXT,
	encrypted BOOLEAN,
	enc_version INT,
	magic TEXT,
	random_key TEXT,
	root_commit_id TEXT,
	head_commit_id TEXT,
	storage_class TEXT,
	size_bytes BIGINT,
	file_count BIGINT,
	version_ttl_days INT,
	created_at TIMESTAMP,
	updated_at TIMESTAMP,
	PRIMARY KEY ((org_id), library_id)
)`

const migrationCreateCommits = `
CREATE TABLE IF NOT EXISTS commits (
	library_id UUID,
	commit_id TEXT,
	parent_id TEXT,
	root_fs_id TEXT,
	creator_id UUID,
	description TEXT,
	created_at TIMESTAMP,
	PRIMARY KEY ((library_id), commit_id)
)`

const migrationCreateFSObjects = `
CREATE TABLE IF NOT EXISTS fs_objects (
	library_id UUID,
	fs_id TEXT,
	obj_type TEXT,
	obj_name TEXT,
	dir_entries TEXT,
	block_ids LIST<TEXT>,
	size_bytes BIGINT,
	mtime BIGINT,
	PRIMARY KEY ((library_id), fs_id)
)`

const migrationCreateBlocks = `
CREATE TABLE IF NOT EXISTS blocks (
	org_id UUID,
	block_id TEXT,
	size_bytes INT,
	storage_class TEXT,
	storage_key TEXT,
	ref_count INT,
	created_at TIMESTAMP,
	last_accessed TIMESTAMP,
	PRIMARY KEY ((org_id), block_id)
)`

const migrationCreateShareLinks = `
CREATE TABLE IF NOT EXISTS share_links (
	share_token TEXT PRIMARY KEY,
	org_id UUID,
	library_id UUID,
	file_path TEXT,
	created_by UUID,
	permission TEXT,
	password_hash TEXT,
	expires_at TIMESTAMP,
	download_count INT,
	max_downloads INT,
	created_at TIMESTAMP
)`

const migrationCreateShares = `
CREATE TABLE IF NOT EXISTS shares (
	library_id UUID,
	share_id UUID,
	shared_by UUID,
	shared_to UUID,
	shared_to_type TEXT,
	permission TEXT,
	created_at TIMESTAMP,
	expires_at TIMESTAMP,
	PRIMARY KEY ((library_id), share_id)
)`

const migrationCreateRestoreJobs = `
CREATE TABLE IF NOT EXISTS restore_jobs (
	org_id UUID,
	job_id UUID,
	library_id UUID,
	block_ids LIST<TEXT>,
	glacier_job_id TEXT,
	status TEXT,
	requested_at TIMESTAMP,
	completed_at TIMESTAMP,
	expires_at TIMESTAMP,
	PRIMARY KEY ((org_id), job_id)
)`
