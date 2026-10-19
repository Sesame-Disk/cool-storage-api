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

func TestInitialSchemaContainsLookupNameAndStarredProjection(t *testing.T) {
	raw, err := migrationsFS.ReadFile("migrations/001_initial_schema.cql")
	require.NoError(t, err)
	content := string(raw)
	normalizedContent := strings.ReplaceAll(content, "\r\n", "\n")

	assert.Contains(t, content, "name              TEXT,")
	assert.Contains(t, content, "source_api_key_hash TEXT,")
	assert.Contains(t, content, "api_key_scope       TEXT")
	// starred_files reverse lookup is a projection table, not a secondary index.
	assert.Contains(t, content, "CREATE TABLE IF NOT EXISTS starred_files_by_repo")
	// file_tags reverse lookup by tag is a projection table, not ALLOW FILTERING.
	assert.Contains(t, content, "CREATE TABLE IF NOT EXISTS file_tags_by_tag")
	assert.Contains(t, content, "PRIMARY KEY ((org_id, month, shard), day, user_id, traffic_type)")
	assert.Contains(t, content, "PRIMARY KEY ((scope, shard), day)")
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
	assert.Regexp(t, `gc_claim_id\s+TEXT`, content)
	assert.Contains(t, normalizedContent, "CREATE TABLE IF NOT EXISTS gc_org_stats (\n    org_id           UUID PRIMARY KEY,\n    queue_depth      INT,\n    failed_depth     INT,\n    oldest_queued_at TIMESTAMP,\n    updated_at       TIMESTAMP,\n    recalculated_at TIMESTAMP\n);")
	// The DLQ expiry projection and the pending markers must never expire silently:
	// both declare default_time_to_live = 0 explicitly (exact keys: R26 guard).
	for _, table := range []string{"gc_failed_items_by_expiry", "gc_pending_items"} {
		assert.Regexp(t, `default_time_to_live\s*=\s*0\b`, requireEffectiveTableDDL(t, table), table)
	}
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
	assert.Regexp(t, `staged_block_ids\s+LIST<TEXT>`, content)
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

// The GC queue/marker/DLQ tables use LeveledCompactionStrategy. These five
// tables are the only GC tables that combine a long-lived hot partition with
// continuous scans at the coordinates where insert+delete churn accumulates
// tombstones; the date-bucketed discovery projections are intentionally left on
// the default strategy. See docs/SCHEMA-BOTTLENECK-AUDIT.md item I.
func TestGCQueueTablesUseLCS(t *testing.T) {
	for _, table := range []string{
		"gc_queue",
		"gc_pending_items",
		"gc_active_orgs",
		"gc_dirty_orgs",
		"gc_failed_items",
	} {
		ddl := requireEffectiveTableDDL(t, table)
		assert.Contains(t, ddl, "'class': 'LeveledCompactionStrategy'", "%s must use LCS", table)
		assert.Contains(t, ddl, "'tombstone_threshold': '0.1'", "%s tombstone_threshold", table)
		assert.Contains(t, ddl, "'tombstone_compaction_interval': '600'", "%s tombstone_compaction_interval", table)
	}

	// The date-bucketed discovery projections must NOT use LCS: their aged
	// partitions fall out of the read window, so they do not need it.
	for _, table := range []string{
		"gc_failed_items_by_expiry",
		"gc_block_candidates_by_day",
		"gc_s3_orphans_by_day",
	} {
		assert.NotContains(t, requireEffectiveTableDDL(t, table), "LeveledCompactionStrategy",
			"%s is a date-bucketed projection and must keep the default strategy", table)
	}
}

// gc_active_orgs and gc_dirty_orgs were switched to LCS by ALTER before the v1
// fold, so they kept the 0.01 bloom filter Cassandra assigns to non-LCS tables
// at CREATE time. The baseline pins it so the fold stays identical to the
// pre-fold schema (scripts/schema-baseline-equivalence.sh); the other LCS tables
// were created with LCS and carry Cassandra's LCS default.
func TestGCOrgMembershipTablesKeepPreFoldBloomFilter(t *testing.T) {
	for _, table := range []string{"gc_active_orgs", "gc_dirty_orgs"} {
		assert.Regexp(t, `bloom_filter_fp_chance\s*=\s*0\.01\b`, requireEffectiveTableDDL(t, table), table)
	}
}

func TestSchemaHasSHA256CanonicalBlockIDColumns(t *testing.T) {
	assert.True(t, ddlDeclaresColumn(requireEffectiveTableDDL(t, "fs_objects"), "seafile_block_ids_sha1", "LIST<TEXT>"))
	assert.True(t, ddlDeclaresColumn(requireEffectiveTableDDL(t, "blocks"), "sha1", "TEXT"))
}

// Physical GC no longer carries the block representation on the orphan
// recovery row (R11a); external_sha1 stays for the conservative canonical-state
// characterization.
func TestGCOrphanRowCarriesExternalSHA1WithoutRepresentationID(t *testing.T) {
	ddl := requireEffectiveTableDDL(t, "gc_s3_orphans")
	assert.NotContains(t, strings.ToLower(ddl), "representation_id")
	assert.True(t, ddlDeclaresColumn(ddl, "external_sha1", "TEXT"))
	assert.True(t, ddlDeclaresColumn(ddl, "storage_key", "TEXT"))
}

func TestSchemaHasBlockUploadStagingCapsAndFrozenAdmission(t *testing.T) {
	requireEffectiveTableDDL(t, "block_upload_session_slots_by_user")
	requireEffectiveTableDDL(t, "block_upload_session_staged_blocks")

	ddl := requireEffectiveTableDDL(t, "block_upload_sessions")
	for column, cqlType := range map[string]string{
		"slot":                   "INT",
		"expected_size":          "BIGINT",
		"expected_size_declared": "BOOLEAN",
		"block_size_bytes":       "BIGINT",
		"staged_bucket_count":    "INT",
		"staged_bucket_cap":      "INT",
	} {
		assert.True(t, ddlDeclaresColumn(ddl, column, cqlType), "block_upload_sessions.%s %s", column, cqlType)
	}
}

// PC-D1B: the identity-authority claim table must be partitioned per identity
// (the full triple), never as a library-wide partition with clustering columns,
// and must carry no TTL. Both are fixed by docs/PC-D1B-METADATA-IDENTITY-AUTHORITY.md.
func TestIdentityAuthorityClaimsUsePerIdentityPartitionWithoutTTL(t *testing.T) {
	ddl := requireEffectiveTableDDL(t, "identity_authority_claims")

	assert.Contains(t, ddl, "PRIMARY KEY ((library_id, identity_kind, identity_id))")
	assert.NotContains(t, ddl, "PRIMARY KEY ((library_id),", "a library-wide partition would serialize every identity in a library into one LWT partition")
	assert.True(t, ddlDeclaresColumn(ddl, "digest_version", "TEXT"))
	assert.True(t, ddlDeclaresColumn(ddl, "digest", "TEXT"))
	assert.NotContains(t, strings.ToLower(ddl), "default_time_to_live", "claims must outlive their source rows (ISSUE-PCD1B-AUTHORITY-CLAIM-RETIREMENT-01)")
}

// PC-D1B.3: the mapping-authority claim is keyed by exactly the
// block_id_mappings identity, one Paxos partition per mapping, with no TTL.
func TestBlockMappingAuthorityClaimsUsePerMappingPartitionWithoutTTL(t *testing.T) {
	ddl := requireEffectiveTableDDL(t, "block_mapping_authority_claims")

	assert.Contains(t, ddl, "PRIMARY KEY ((org_id, representation_id, external_id))")
	assert.True(t, ddlDeclaresColumn(ddl, "org_id", "UUID"))
	assert.True(t, ddlDeclaresColumn(ddl, "internal_id", "TEXT"))
	assert.True(t, ddlDeclaresColumn(ddl, "contract_version", "TEXT"))
	assert.NotContains(t, strings.ToLower(ddl), "default_time_to_live", "mapping claims must outlive the mutable mapping row")

	assert.Contains(t, requireEffectiveTableDDL(t, "block_id_mappings"), "PRIMARY KEY ((org_id, representation_id, external_id))",
		"the claim key must stay exactly the block_id_mappings identity")
}

func TestPublishedRepairsCarryReachabilityCursorColumns(t *testing.T) {
	ddl := requireEffectiveTableDDL(t, "published_block_reference_repairs")
	assert.True(t, ddlDeclaresColumn(ddl, "reachability_anchor_head_commit_id", "TEXT"))
	assert.True(t, ddlDeclaresColumn(ddl, "reachability_cursor_commit_id", "TEXT"))
	assert.True(t, ddlDeclaresColumn(ddl, "reachability_anchor_exhausted", "BOOLEAN"))

	ttl, ok := effectiveTableDefaultTTL(t, "published_block_reference_repairs")
	require.True(t, ok)
	assert.Equal(t, 0, ttl, "the repair row is the durable unit and must not expire")
}
