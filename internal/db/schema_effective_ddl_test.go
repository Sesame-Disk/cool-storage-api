package db

import (
	"regexp"
	"strings"
	"testing"
)

// effectiveTableDDL returns the statements of the embedded migration set that
// shape table as a freshly migrated keyspace sees it: its last CREATE TABLE
// plus every later ALTER TABLE, in version order (comments already stripped by
// the migrator parser). A DROP TABLE resets it. Empty means the table does not
// exist.
//
// Schema guards assert on this instead of naming one migration file, so they
// pin the property and keep working whatever file declares it: the v1
// baseline today, a later NNN_*.cql tomorrow.
func effectiveTableDDL(t *testing.T, table string) string {
	t.Helper()
	files, err := (&Migrator{}).loadFiles()
	if err != nil {
		t.Fatalf("loadFiles: %v", err)
	}
	want := strings.ToLower(table)
	var ddl []string
	for _, mf := range files {
		for _, stmt := range mf.Statements {
			if drop := dropTableStatement.FindStringSubmatch(stmt); drop != nil {
				if bareTableName(drop[1]) == want {
					ddl = nil
				}
				continue
			}
			if m := createTableHeaderPattern.FindStringSubmatch(stmt); m != nil {
				if bareTableName(m[1]) == want {
					ddl = []string{stmt}
				}
				continue
			}
			if m := alterTableHeaderPattern.FindStringSubmatch(stmt); m != nil && bareTableName(m[1]) == want && ddl != nil {
				ddl = append(ddl, stmt)
			}
		}
	}
	return strings.Join(ddl, ";\n")
}

// requireEffectiveTableDDL is effectiveTableDDL that fails when the table is absent.
func requireEffectiveTableDDL(t *testing.T, table string) string {
	t.Helper()
	ddl := effectiveTableDDL(t, table)
	if ddl == "" {
		t.Fatalf("the migration set does not create %s", table)
	}
	return ddl
}

// ddlDeclaresColumn reports whether ddl declares column with cqlType, either in
// the CREATE column list or through ALTER TABLE ... ADD [IF NOT EXISTS].
func ddlDeclaresColumn(ddl, column, cqlType string) bool {
	return regexp.MustCompile(`(?i)(?:^|[\s(,])` + regexp.QuoteMeta(column) + `\s+` + regexp.QuoteMeta(cqlType) + `(?:$|[^A-Za-z0-9_<>])`).MatchString(ddl)
}

func TestEffectiveTableDDLFollowsDropCreateAndAlter(t *testing.T) {
	if ddl := effectiveTableDDL(t, "block_id_mappings_by_internal"); ddl != "" {
		t.Fatalf("block_id_mappings_by_internal must not exist in the effective schema, got %q", ddl)
	}
	ddl := requireEffectiveTableDDL(t, "blocks")
	if !ddlDeclaresColumn(ddl, "gc_orphan_handoff", "BOOLEAN") {
		t.Fatal("effectiveTableDDL lost blocks.gc_orphan_handoff")
	}
	if ddlDeclaresColumn(ddl, "gc_orphan_handoff", "TEXT") {
		t.Fatal("ddlDeclaresColumn must match the declared type, not any type")
	}
	if !ddlDeclaresColumn("ALTER TABLE x ADD IF NOT EXISTS y text", "y", "TEXT") {
		t.Fatal("ddlDeclaresColumn must recognise ALTER TABLE ... ADD IF NOT EXISTS")
	}
	if !ddlDeclaresColumn("CREATE TABLE x (y LIST<TEXT>, z INT)", "y", "LIST<TEXT>") {
		t.Fatal("ddlDeclaresColumn must recognise a collection type followed by a comma")
	}
	if ddlDeclaresColumn("CREATE TABLE x (y TEXTS)", "y", "TEXT") {
		t.Fatal("ddlDeclaresColumn must not match a type prefix")
	}
}
