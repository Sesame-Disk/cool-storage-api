package db

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseMigrationFilename(t *testing.T) {
	t.Run("valid filename", func(t *testing.T) {
		version, name, err := parseMigrationFilename("042_add_lookup.cql")
		require.NoError(t, err)
		assert.Equal(t, 42, version)
		assert.Equal(t, "add_lookup", name)
	})

	t.Run("invalid filename missing separator", func(t *testing.T) {
		_, _, err := parseMigrationFilename("001.cql")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "NNN_name.cql")
	})

	t.Run("invalid filename empty name", func(t *testing.T) {
		_, _, err := parseMigrationFilename("001_.cql")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "name part is empty")
	})
}

func TestParseCQLStatements(t *testing.T) {
	content := `-- file comment
CREATE TABLE foo (
    id UUID PRIMARY KEY
);

-- index comment
CREATE INDEX IF NOT EXISTS foo_by_bar ON foo (bar);
`

	statements := parseCQLStatements(content)
	require.Len(t, statements, 2)
	assert.Equal(t, "CREATE TABLE foo (\n    id UUID PRIMARY KEY\n)", statements[0])
	assert.Equal(t, "CREATE INDEX IF NOT EXISTS foo_by_bar ON foo (bar)", statements[1])
}

func TestMigrationCheckIssues(t *testing.T) {
	files := []MigrationFile{
		{Version: 1, Name: "initial_schema", Checksum: strings.Repeat("a", 64)},
		{Version: 2, Name: "add_lookup", Checksum: strings.Repeat("b", 64)},
	}
	applied := map[int]AppliedMigration{
		1: {
			Version:   1,
			Name:      "initial_schema",
			Checksum:  strings.Repeat("a", 64),
			AppliedAt: time.Now().UTC(),
		},
	}

	issues := migrationCheckIssues(files, applied)
	require.Len(t, issues, 1)
	assert.Equal(t, "pending:  002_add_lookup", issues[0])

	applied[1] = AppliedMigration{
		Version:   1,
		Name:      "initial_schema",
		Checksum:  strings.Repeat("c", 64),
		AppliedAt: time.Now().UTC(),
	}

	issues = migrationCheckIssues(files[:1], applied)
	require.Len(t, issues, 1)
	assert.Contains(t, issues[0], "modified: 001_initial_schema")
	assert.Contains(t, issues[0], "recorded=cccccccc")
	assert.Contains(t, issues[0], "file=aaaaaaaa")
}

func TestValidateAppliedMigrationChecksums(t *testing.T) {
	files := []MigrationFile{{Version: 1, Name: "initial_schema", Checksum: strings.Repeat("a", 64)}}
	applied := map[int]AppliedMigration{
		1: {
			Version:   1,
			Name:      "initial_schema",
			Checksum:  strings.Repeat("a", 64),
			AppliedAt: time.Now().UTC(),
		},
	}

	require.NoError(t, validateAppliedMigrationChecksums(files, applied))

	applied[1] = AppliedMigration{
		Version:   1,
		Name:      "initial_schema",
		Checksum:  strings.Repeat("f", 64),
		AppliedAt: time.Now().UTC(),
	}

	err := validateAppliedMigrationChecksums(files, applied)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "checksum mismatch")
	assert.Contains(t, err.Error(), "Do not edit migration files after application")
}

func TestInitialSchemaContainsLookupNameAndStarredIndex(t *testing.T) {
	raw, err := migrationsFS.ReadFile("migrations/001_initial_schema.cql")
	require.NoError(t, err)
	content := string(raw)
	normalizedContent := strings.ReplaceAll(content, "\r\n", "\n")

	assert.Contains(t, content, "name              TEXT,")
	assert.Contains(t, content, "source_api_key_hash TEXT,")
	assert.Contains(t, content, "api_key_scope       TEXT")
	// starred_files reverse lookup is a projection table, not a secondary index.
	assert.Contains(t, content, "CREATE TABLE IF NOT EXISTS starred_files_by_repo")
	assert.NotContains(t, content, "CREATE INDEX IF NOT EXISTS starred_files_by_repo ON starred_files (repo_id);")
	// No secondary indexes in the baseline schema (scatter-gather anti-pattern).
	assert.NotContains(t, content, "CREATE INDEX IF NOT EXISTS")
	assert.Contains(t, content, "CREATE TABLE IF NOT EXISTS admin_links_by_created")
	assert.Contains(t, content, "CREATE TABLE IF NOT EXISTS admin_links_by_org_created")
	assert.Contains(t, content, "CREATE TABLE IF NOT EXISTS admin_link_buckets")
	assert.Contains(t, content, "CREATE TABLE IF NOT EXISTS admin_link_buckets_by_org")
	assert.Contains(t, content, "CREATE TABLE IF NOT EXISTS admin_link_counts_by_org")
	assert.Contains(t, content, "CREATE TABLE IF NOT EXISTS libraries_by_owner")
	assert.Contains(t, content, "CREATE TABLE IF NOT EXISTS library_admin_global_buckets")
	assert.Contains(t, content, "CREATE TABLE IF NOT EXISTS shares_by_group")
	assert.Contains(t, content, "CREATE TABLE IF NOT EXISTS shares_by_creator")
	assert.Contains(t, content, "CREATE TABLE IF NOT EXISTS shares_by_recipient")
	assert.Contains(t, content, "CREATE TABLE IF NOT EXISTS api_keys")
	assert.Contains(t, content, "CREATE TABLE IF NOT EXISTS api_keys_by_user")
	assert.Contains(t, content, "CREATE TABLE IF NOT EXISTS sessions_by_api_key")
	assert.Contains(t, content, "CREATE TABLE IF NOT EXISTS sessions_by_org")
	assert.Contains(t, content, "CREATE TABLE IF NOT EXISTS monitored_repos_by_repo")
	assert.Contains(t, content, "CREATE TABLE IF NOT EXISTS deleted_organizations")
	assert.Contains(t, content, "CREATE TABLE IF NOT EXISTS groups_admin_global_by_created")
	assert.Contains(t, content, "CREATE TABLE IF NOT EXISTS organizations_admin_by_created")
	assert.Contains(t, content, "CREATE TABLE IF NOT EXISTS organizations_admin_by_status_created")
	assert.Contains(t, content, "CREATE TABLE IF NOT EXISTS users_admin_global_by_created")
	assert.Contains(t, content, "CREATE TABLE IF NOT EXISTS users_admin_global_by_status_created")
	assert.Contains(t, content, "CREATE TABLE IF NOT EXISTS gc_block_candidates")
	assert.Contains(t, content, "CREATE TABLE IF NOT EXISTS gc_provisional_block_refs")
	assert.Contains(t, content, "CREATE TABLE IF NOT EXISTS gc_provisional_block_refs_by_day")
	assert.Contains(t, content, "CREATE TABLE IF NOT EXISTS gc_deleted_users_by_deleted_day")
	assert.Contains(t, content, "CREATE TABLE IF NOT EXISTS gc_libraries_by_policy")
	assert.Contains(t, content, "CREATE TABLE IF NOT EXISTS gc_share_links_by_expiry")
	assert.Contains(t, content, "CREATE TABLE IF NOT EXISTS gc_shares_by_expiry")
	assert.Contains(t, content, "CREATE TABLE IF NOT EXISTS gc_active_orgs")
	assert.Contains(t, content, "CREATE TABLE IF NOT EXISTS gc_dirty_orgs")
	assert.Contains(t, content, "CREATE TABLE IF NOT EXISTS gc_org_stats")
	assert.Contains(t, content, "CREATE TABLE IF NOT EXISTS gc_failed_items")
	assert.Contains(t, content, "CREATE TABLE IF NOT EXISTS gc_failed_items_by_expiry")
	assert.Contains(t, content, "CREATE TABLE IF NOT EXISTS gc_pending_items")
	assert.Contains(t, content, "CREATE TABLE IF NOT EXISTS gc_s3_orphans")
	assert.Contains(t, content, "CREATE TABLE IF NOT EXISTS gc_block_candidates_by_day")
	assert.Contains(t, content, "CREATE TABLE IF NOT EXISTS gc_s3_orphans_by_day")
	assert.Contains(t, content, "CREATE TABLE IF NOT EXISTS gc_leases")
	assert.Contains(t, content, "gc_claim_id   TEXT")
	assert.Contains(t, normalizedContent, "CREATE TABLE IF NOT EXISTS gc_org_stats (\n    org_id           UUID PRIMARY KEY,\n    queue_depth      INT,\n    failed_depth     INT,\n    oldest_queued_at TIMESTAMP,\n    updated_at       TIMESTAMP,\n    recalculated_at TIMESTAMP\n);")
	assert.Contains(t, normalizedContent, "CREATE TABLE IF NOT EXISTS gc_failed_items_by_expiry (\n    expiry_day DATE,\n    bucket     INT,\n    expires_at TIMESTAMP,\n    org_id     UUID,\n    failed_at  TIMESTAMP,\n    item_type  TEXT,\n    item_id    TEXT,\n    PRIMARY KEY ((expiry_day, bucket), expires_at, org_id, failed_at, item_type, item_id)\n) WITH CLUSTERING ORDER BY (expires_at ASC, org_id ASC, failed_at ASC, item_type ASC, item_id ASC)\n  AND default_time_to_live = 0;")
	assert.Contains(t, normalizedContent, "CREATE TABLE IF NOT EXISTS gc_pending_items (\n    org_id      UUID,\n    bucket      INT,\n    item_type   TEXT,\n    library_id  UUID,\n    item_id     TEXT,\n    identity_at TIMESTAMP,\n    PRIMARY KEY ((org_id, bucket), item_type, library_id, item_id, identity_at)\n) WITH default_time_to_live = 0;")
	// `blocks` and related tables must use per-block partitioning to avoid
	// the org_id hot partition that previously serialized all upload LWTs.
	assert.Contains(t, content, "PRIMARY KEY ((org_id, block_id))")
	assert.Contains(t, content, "WITH default_time_to_live = 0")
	assert.Contains(t, content, "INSERT INTO gc_stats (stat_key, stat_value, updated_at)")
	assert.NotContains(t, content, "CREATE TABLE IF NOT EXISTS gc_queue_stats")
	assert.Contains(t, content, "storage_class TEXT")
	assert.Contains(t, content, "replace_existing BOOLEAN")
	assert.Contains(t, content, "CREATE TABLE IF NOT EXISTS onlyoffice_pending_blocks")
	assert.Contains(t, content, "WITH default_time_to_live = 604800")
	assert.Contains(t, content, "CREATE TABLE IF NOT EXISTS published_block_reference_repairs")
	assert.Contains(t, content, "staged_block_ids LIST<TEXT>")
	assert.Contains(t, content, "PRIMARY KEY ((bucket), org_id, repo_id, commit_id, fs_id)")
	assert.Contains(t, content, "CREATE TABLE IF NOT EXISTS pending_published_fs_objects")
	assert.Contains(t, content, "CREATE TABLE IF NOT EXISTS pending_published_fs_objects_by_day")
	assert.Contains(t, content, "PRIMARY KEY ((repo_id, fs_id), owner_id)")
	assert.Contains(t, content, "attempt_id TEXT")
	assert.Contains(t, content, "block_ids  LIST<TEXT>")
	assert.Contains(t, normalizedContent, "PRIMARY KEY ((repo_id, fs_id), owner_id)\n) WITH default_time_to_live = 3024000;")
	assert.Contains(t, normalizedContent, ") WITH CLUSTERING ORDER BY (created_at ASC, repo_id ASC, fs_id ASC, owner_id ASC)\n    AND default_time_to_live = 3024000;")
	assert.NotContains(t, content, "CREATE TABLE IF NOT EXISTS share_links_by_org")
}

// Migration 003 moves the GC queue/marker/DLQ tables to
// LeveledCompactionStrategy. These five tables are the only GC tables that
// combine a long-lived hot partition with continuous scans at the coordinates
// where insert+delete churn accumulates tombstones; the date-bucketed discovery
// projections are intentionally left on the default strategy. See
// docs/SCHEMA-BOTTLENECK-AUDIT.md item I.
func TestMigration003AppliesLCSToGCQueueTables(t *testing.T) {
	raw, err := migrationsFS.ReadFile("migrations/003_gc_queue_lcs_compaction.cql")
	require.NoError(t, err)
	content := string(raw)

	for _, table := range []string{
		"gc_queue",
		"gc_pending_items",
		"gc_active_orgs",
		"gc_dirty_orgs",
		"gc_failed_items",
	} {
		assert.Contains(t, content, "ALTER TABLE "+table+" WITH compaction =",
			"migration 003 must apply LCS to %s", table)
	}

	assert.Equal(t, 5, strings.Count(content, "'class': 'LeveledCompactionStrategy'"),
		"exactly the five queue/marker/DLQ tables should switch to LCS")
	assert.Contains(t, content, "'tombstone_threshold': '0.1'")
	assert.Contains(t, content, "'tombstone_compaction_interval': '600'")

	// The date-bucketed discovery projections must NOT be altered: their aged
	// partitions fall out of the read window, so they do not need LCS.
	assert.NotContains(t, content, "ALTER TABLE gc_failed_items_by_expiry")
	assert.NotContains(t, content, "ALTER TABLE gc_block_candidates_by_day")
	assert.NotContains(t, content, "ALTER TABLE gc_s3_orphans_by_day")

	// Parser sanity: the five ALTERs split into exactly five executable statements.
	assert.Len(t, parseCQLStatements(content), 5)
}
