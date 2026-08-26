package db

import (
	"os"
	"strings"
	"testing"
)

// A migration that DROPs a table discards whatever that table held. Migration 018
// is exactly that, and it is correct only under the contract it states: a clean
// keyspace, migrated before any node produces GC work.
//
// These tests pin the two halves of the barrier that makes the contract
// enforceable rather than advisory — the parser that decides which tables a
// migration will destroy, and the fact that the guard is wired into Run before
// apply. The behavioural half (refusing when a table holds rows) needs a live
// cluster and lives in the integration suite.

func TestDroppedTablesFindsEveryDropTarget(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		want    []string
	}{
		{
			name:    "plain",
			content: "DROP TABLE gc_queue;",
			want:    []string{"gc_queue"},
		},
		{
			name:    "if_exists",
			content: "DROP TABLE IF EXISTS gc_block_candidates;",
			want:    []string{"gc_block_candidates"},
		},
		{
			name:    "keyspace_qualified",
			content: "DROP TABLE IF EXISTS sesamefs.gc_pending_items;",
			want:    []string{"gc_pending_items"},
		},
		{
			name:    "case_and_whitespace",
			content: "drop   table\n  if exists\n  gc_failed_items;",
			want:    []string{"gc_failed_items"},
		},
		{
			name:    "several_in_one_migration",
			content: "DROP TABLE IF EXISTS a;\nDROP TABLE IF EXISTS b;\nCREATE TABLE c (x INT PRIMARY KEY);",
			want:    []string{"a", "b"},
		},
		{
			name:    "create_only_is_not_destructive",
			content: "CREATE TABLE IF NOT EXISTS gc_queue (x INT PRIMARY KEY);",
			want:    nil,
		},
		{
			name:    "alter_is_not_destructive",
			content: "ALTER TABLE gc_queue ADD IF NOT EXISTS storage_key text;",
			want:    nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mf := MigrationFile{Content: tc.content, Statements: parseCQLStatements(tc.content)}
			got := mf.droppedTables()
			if len(got) != len(tc.want) {
				t.Fatalf("droppedTables() = %v, want %v", got, tc.want)
			}
			for i, table := range tc.want {
				if got[i] != table {
					t.Fatalf("droppedTables()[%d] = %q, want %q", i, got[i], table)
				}
			}
		})
	}
}

// TestShippedDestructiveMigrationsAreDetected is the non-vacuous half: if the
// parser stopped recognising the drops in the migration that actually ships, the
// barrier would silently pass everything.
func TestShippedDestructiveMigrationsAreDetected(t *testing.T) {
	m := &Migrator{}
	files, err := m.loadFiles()
	if err != nil {
		t.Fatalf("loadFiles: %v", err)
	}

	dropped := map[string][]string{}
	for _, mf := range files {
		if tables := mf.droppedTables(); len(tables) > 0 {
			dropped[mf.label()] = tables
		}
	}
	if len(dropped) == 0 {
		t.Fatal("no shipped migration drops any table; the destructive preflight has nothing to protect and this guard is vacuous")
	}

	// 018 is the destructive GC identity migration. Every table it drops must be
	// seen by the preflight, or that table's rows are erased without a check.
	tables, ok := dropped["018_gc_exact_p_candidate_identity"]
	if !ok {
		t.Fatalf("018_gc_exact_p_candidate_identity is not recognised as destructive; the preflight would not protect it. Detected: %v", dropped)
	}
	for _, want := range []string{
		"gc_failed_items_by_expiry",
		"gc_failed_items",
		"gc_pending_items",
		"gc_queue",
		"gc_block_candidates_by_day",
		"gc_block_candidates",
	} {
		found := false
		for _, got := range tables {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("migration 018 drops %s but the preflight does not see it: rows in that table would be erased unchecked. Detected: %v", want, tables)
		}
	}
}

func TestMissingTableErrorRecognisesTheEngineWordings(t *testing.T) {
	for _, msg := range []string{
		"unconfigured table gc_queue",
		"unconfigured columnfamily gc_queue",
		"table gc_queue does not exist",
		"Cannot read from a non-existent table",
	} {
		if !missingTableError(errStub(msg)) {
			t.Errorf("missingTableError(%q) = false; a re-run after a partially applied destructive migration would deadlock", msg)
		}
	}
	// Anything else must NOT be waved through: an unreadable table is not a
	// demonstrably empty one, and dropping it on a guess is the whole failure.
	for _, msg := range []string{
		"connection refused",
		"operation timed out",
		"not enough replicas available for query at consistency QUORUM",
	} {
		if missingTableError(errStub(msg)) {
			t.Errorf("missingTableError(%q) = true; an unverified table must block a destructive migration, not pass it", msg)
		}
	}
}

type errStub string

func (e errStub) Error() string { return string(e) }

// TestRunPreflightsBeforeApplying pins the wiring: the guard is useless if Run
// applies the migration first.
func TestRunPreflightsBeforeApplying(t *testing.T) {
	source := migratorSourceForTest(t)
	runBody, ok := functionBodyForTest(source, "func (m *Migrator) Run() error {")
	if !ok {
		t.Fatal("Migrator.Run not found; the destructive preflight wiring cannot be verified")
	}
	preflight := strings.Index(runBody, "preflightDestructive")
	apply := strings.Index(runBody, "m.apply(mf)")
	if preflight < 0 {
		t.Fatal("Migrator.Run no longer calls preflightDestructive; a destructive migration would run unchecked")
	}
	if apply < 0 {
		t.Fatal("Migrator.Run no longer calls apply; this guard is now vacuous")
	}
	if preflight > apply {
		t.Error("Migrator.Run applies the migration before the destructive preflight; the check has to come first or it is checking rows that were already dropped")
	}
}

func migratorSourceForTest(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("migrator.go")
	if err != nil {
		t.Fatalf("read migrator.go: %v", err)
	}
	return string(raw)
}

// functionBodyForTest returns the source between a function's opening line and
// its closing brace at column 0.
func functionBodyForTest(source, signature string) (string, bool) {
	start := strings.Index(source, signature)
	if start < 0 {
		return "", false
	}
	rest := source[start+len(signature):]
	end := strings.Index(rest, "\n}")
	if end < 0 {
		return "", false
	}
	return rest[:end], true
}
