//go:build schemaequiv

package db

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

// TestSchemaBaselineEquivalence proves that the embedded migration set builds
// exactly the schema a reference migration set builds. It applies the
// reference set (SESAMEFS_SCHEMA_EQUIV_REFERENCE_DIR, typically extracted from
// another git ref) and the embedded set to two fresh keyspaces on a disposable
// Cassandra (SESAMEFS_SCHEMA_EQUIV_HOSTS), then compares every system_schema
// row of both keyspaces and the rows the migrations seed. Any difference fails.
//
// Both sides go through the production parser and Migrator.apply, so the
// comparison covers what the migrator actually executes, not what a reader
// believes the CQL says. scripts/schema-baseline-equivalence.sh drives it.
func TestSchemaBaselineEquivalence(t *testing.T) {
	hosts := strings.TrimSpace(os.Getenv("SESAMEFS_SCHEMA_EQUIV_HOSTS"))
	referenceDir := strings.TrimSpace(os.Getenv("SESAMEFS_SCHEMA_EQUIV_REFERENCE_DIR"))
	if hosts == "" || referenceDir == "" {
		t.Fatal("SESAMEFS_SCHEMA_EQUIV_HOSTS and SESAMEFS_SCHEMA_EQUIV_REFERENCE_DIR are required")
	}

	reference, err := loadMigrationFiles(os.DirFS(referenceDir), ".")
	if err != nil {
		t.Fatalf("load reference migrations: %v", err)
	}
	if len(reference) == 0 {
		t.Fatalf("no reference migrations found in %s", referenceDir)
	}

	const (
		referenceKeyspace = "schema_equiv_reference"
		currentKeyspace   = "schema_equiv_current"
	)
	admin := schemaEquivSession(t, hosts, "")
	defer admin.Close()
	for _, ks := range []string{referenceKeyspace, currentKeyspace} {
		schemaEquivExec(t, admin, "DROP KEYSPACE IF EXISTS "+ks)
		schemaEquivExec(t, admin, "CREATE KEYSPACE "+ks+
			" WITH replication = {'class': 'SimpleStrategy', 'replication_factor': 1}")
	}

	refSession := schemaEquivSession(t, hosts, referenceKeyspace)
	defer refSession.Close()
	refMigrator := NewMigrator(refSession)
	if err := refMigrator.ensureTable(); err != nil {
		t.Fatalf("reference ensureTable: %v", err)
	}
	for _, mf := range reference {
		if err := refMigrator.apply(mf); err != nil {
			t.Fatalf("reference %s: %v", mf.label(), err)
		}
	}

	curSession := schemaEquivSession(t, hosts, currentKeyspace)
	defer curSession.Close()
	if err := NewMigrator(curSession).Run(); err != nil {
		t.Fatalf("current migrator run: %v", err)
	}

	current, err := (&Migrator{}).loadFiles()
	if err != nil {
		t.Fatalf("load current migrations: %v", err)
	}
	t.Logf("reference: %d migration files (%s .. %s); current: %d migration files",
		len(reference), reference[0].label(), reference[len(reference)-1].label(), len(current))

	refRows := schemaEquivSnapshot(t, admin, referenceKeyspace)
	curRows := schemaEquivSnapshot(t, admin, currentKeyspace)

	onlyRef, onlyCur := schemaEquivDiff(refRows, curRows)
	tables := 0
	for _, row := range curRows {
		if strings.HasPrefix(row, "system_schema.tables ") {
			tables++
		}
	}
	t.Logf("compared %d rows per side (%d tables)", len(curRows), tables)
	if len(onlyRef) > 0 || len(onlyCur) > 0 {
		var b strings.Builder
		for _, row := range onlyRef {
			fmt.Fprintf(&b, "\n  - reference only: %s", row)
		}
		for _, row := range onlyCur {
			fmt.Fprintf(&b, "\n  + current only:   %s", row)
		}
		t.Fatalf("schemas differ (%d reference-only, %d current-only rows):%s", len(onlyRef), len(onlyCur), b.String())
	}
}

// schemaEquivSystemTables are every system_schema table that can hold rows for
// a user keyspace. `keyspaces` is excluded: both keyspaces are created here
// with the same replication, so it would only compare the test's own DDL.
var schemaEquivSystemTables = []string{
	"tables", "columns", "dropped_columns", "indexes", "views",
	"types", "triggers", "functions", "aggregates", "column_masks",
}

// schemaEquivIgnoredFields are per-keyspace identities, not schema content.
var schemaEquivIgnoredFields = map[string]bool{
	"keyspace_name": true,
	"id":            true,
	"base_table_id": true,
}

// schemaEquivSnapshot returns one canonical line per schema row and per seeded
// data row of keyspace ks.
func schemaEquivSnapshot(t *testing.T, s *gocql.Session, ks string) []string {
	t.Helper()
	var rows []string
	for _, table := range schemaEquivSystemTables {
		for _, raw := range schemaEquivJSONRows(t, s,
			"SELECT JSON * FROM system_schema."+table+" WHERE keyspace_name = ?", ks) {
			rows = append(rows, "system_schema."+table+" "+schemaEquivCanonical(t, raw, schemaEquivIgnoredFields))
		}
	}

	// Data seeded by the migrations. schema_migrations is the migrator's own
	// bookkeeping and intentionally differs (one stamp per file).
	// gc_stats.updated_at is toTimestamp(now()) at apply time.
	iter := s.Query("SELECT table_name FROM system_schema.tables WHERE keyspace_name = ?", ks).Iter()
	var name string
	var names []string
	for iter.Scan(&name) {
		names = append(names, name)
	}
	if err := iter.Close(); err != nil {
		t.Fatalf("list tables of %s: %v", ks, err)
	}
	sort.Strings(names)
	for _, table := range names {
		if table == "schema_migrations" {
			continue
		}
		ignored := map[string]bool{}
		if table == "gc_stats" {
			ignored["updated_at"] = true
		}
		for _, raw := range schemaEquivJSONRows(t, s, "SELECT JSON * FROM "+ks+"."+table) {
			rows = append(rows, "data."+table+" "+schemaEquivCanonical(t, raw, ignored))
		}
	}
	sort.Strings(rows)
	return rows
}

func schemaEquivJSONRows(t *testing.T, s *gocql.Session, stmt string, args ...any) []string {
	t.Helper()
	iter := s.Query(stmt, args...).Iter()
	var raw string
	var out []string
	for iter.Scan(&raw) {
		out = append(out, raw)
	}
	if err := iter.Close(); err != nil {
		t.Fatalf("%s: %v", stmt, err)
	}
	return out
}

// schemaEquivCanonical re-encodes a JSON row with sorted keys and without the
// ignored fields, so rows compare by content only.
func schemaEquivCanonical(t *testing.T, raw string, ignored map[string]bool) string {
	t.Helper()
	var row map[string]any
	if err := json.Unmarshal([]byte(raw), &row); err != nil {
		t.Fatalf("decode %q: %v", raw, err)
	}
	for field := range ignored {
		delete(row, field)
	}
	encoded, err := json.Marshal(row)
	if err != nil {
		t.Fatalf("encode %v: %v", row, err)
	}
	return string(encoded)
}

func schemaEquivDiff(ref, cur []string) (onlyRef, onlyCur []string) {
	count := map[string]int{}
	for _, row := range ref {
		count[row]++
	}
	for _, row := range cur {
		count[row]--
	}
	for row, n := range count {
		for ; n > 0; n-- {
			onlyRef = append(onlyRef, row)
		}
		for ; n < 0; n++ {
			onlyCur = append(onlyCur, row)
		}
	}
	sort.Strings(onlyRef)
	sort.Strings(onlyCur)
	return onlyRef, onlyCur
}

func schemaEquivSession(t *testing.T, hosts, keyspace string) *gocql.Session {
	t.Helper()
	cluster := gocql.NewCluster(strings.Split(hosts, ",")...)
	cluster.Keyspace = keyspace
	cluster.Consistency = gocql.One
	cluster.Timeout = 60 * time.Second
	cluster.ConnectTimeout = 30 * time.Second
	var lastErr error
	for attempt := 0; attempt < 60; attempt++ {
		session, err := cluster.CreateSession()
		if err == nil {
			return session
		}
		lastErr = err
		time.Sleep(5 * time.Second)
	}
	t.Fatalf("connect to %s (keyspace %q): %v", hosts, keyspace, lastErr)
	return nil
}

func schemaEquivExec(t *testing.T, s *gocql.Session, stmt string) {
	t.Helper()
	if err := s.Query(stmt).Exec(); err != nil {
		t.Fatalf("%s: %v", stmt, err)
	}
}
