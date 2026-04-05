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

	assert.Contains(t, content, "name              TEXT,")
	assert.Contains(t, content, "source_api_key_hash TEXT,")
	assert.Contains(t, content, "api_key_scope       TEXT")
	assert.Contains(t, content, "CREATE INDEX IF NOT EXISTS starred_files_by_repo ON starred_files (repo_id);")
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
	assert.Contains(t, content, "storage_class TEXT")
	assert.NotContains(t, content, "CREATE TABLE IF NOT EXISTS share_links_by_org")
}
