package db

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// R26 schema guard: the shipped migration must actually declare the PRIMARY KEYs
// the runtime depends on.
//
// This closes the last blind spot in the R26 evidence chain. The other gates all
// look at Go: the source guards read the CQL literals in store_cassandra.go, the
// behavioural tests exercise the store, and the mutation harness edits Go files.
// None of them read the migration. So a regression that rewrites
//
//	PRIMARY KEY (…, candidate_storage_class, candidate_storage_key, identity_at)
//
// into
//
//	PRIMARY KEY (…, identity_at)
//
// changes no Go at all: every runtime statement still names the columns, every
// source guard stays green, every mutation still goes red on cue — and against a
// freshly migrated keyspace P1 and P2 collapse back into one row, which is the
// entire defect this slice exists to remove.
//
// The columns are asserted as an ORDERED SUFFIX of the key, not merely as members
// of it. Order is part of a Cassandra primary key: moving identity_at ahead of the
// P columns would still "contain" all three while changing which prefixes are
// queryable, and every read in the store is written against this order.
func TestR26MigrationDeclaresTheExactIdentityKeys(t *testing.T) {
	const migration = "018_gc_exact_p_candidate_identity"

	// The identity columns each table's key must END with, in order.
	wantKeySuffix := map[string][]string{
		"gc_block_candidates":        {"storage_class", "storage_key"},
		"gc_block_candidates_by_day": {"storage_class", "storage_key"},
		"gc_queue":                   {"candidate_storage_class", "candidate_storage_key", "identity_at"},
		"gc_pending_items":           {"candidate_storage_class", "candidate_storage_key", "identity_at"},
		"gc_failed_items":            {"candidate_storage_class", "candidate_storage_key", "identity_at"},
		"gc_failed_items_by_expiry":  {"candidate_storage_class", "candidate_storage_key", "identity_at"},
	}

	keys := primaryKeysInMigration(t, migration)
	if len(keys) == 0 {
		t.Fatalf("no CREATE TABLE statements found in %s; this guard is vacuous", migration)
	}

	for table, want := range wantKeySuffix {
		key, ok := keys[table]
		if !ok {
			t.Errorf("%s does not create %s; the runtime expects it to carry the exact-P identity key", migration, table)
			continue
		}
		if len(key) < len(want) {
			t.Errorf("R26 REGRESSION: %s.%s PRIMARY KEY is %v, too short to end with %v", migration, table, key, want)
			continue
		}
		got := key[len(key)-len(want):]
		for i := range want {
			if got[i] == want[i] {
				continue
			}
			t.Errorf("R26 REGRESSION: %s.%s PRIMARY KEY = %v.\n"+
				"It must END with %v, in that order. Without those columns two lives of one logical block "+
				"collapse into a single row and a delayed lifecycle for a dead incarnation addresses a live "+
				"one's work — the defect this migration exists to remove. Nothing in the Go code changes when "+
				"this key does, so no other gate can catch it.",
				migration, table, key, want)
			break
		}
	}
}

// TestR26MigrationKeepsCandidateAtOutOfTheCandidateKey pins the other half of the
// candidate contract, which is an ABSENCE and therefore invisible to the suffix
// check above.
//
// gc_block_candidates is keyed by P alone. candidate_at is a mutable VALUE on that
// row — that is what lets earliest-wins move it in place, and what makes "one
// candidate per incarnation" true. Adding candidate_at to the key would silently
// turn every re-decision into a new row, so a block would accumulate one candidate
// per observation and settling one would leave the others behind.
func TestR26MigrationKeepsCandidateAtOutOfTheCandidateKey(t *testing.T) {
	keys := primaryKeysInMigration(t, "018_gc_exact_p_candidate_identity")
	key, ok := keys["gc_block_candidates"]
	if !ok {
		t.Fatal("018 does not create gc_block_candidates; this guard is vacuous")
	}
	for _, column := range key {
		if column == "candidate_at" {
			t.Errorf("R26 REGRESSION: gc_block_candidates PRIMARY KEY = %v and includes candidate_at.\n"+
				"candidate_at must stay a mutable value: earliest-wins advances it in place, and one "+
				"incarnation must have exactly one candidate row. In the key, every re-decision would "+
				"create another row and settling one would strand the rest.", key)
		}
	}
}

var (
	createTablePattern = regexp.MustCompile(`(?is)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([A-Za-z0-9_.]+)\s*\((.*)`)
	primaryKeyPattern  = regexp.MustCompile(`(?is)PRIMARY\s+KEY\s*\((.*?)\)\s*\)`)
)

// primaryKeysInMigration returns table -> ordered primary key columns, with the
// partition-key parentheses flattened away: ((a, b), c) reads as [a b c].
func primaryKeysInMigration(t *testing.T, name string) map[string][]string {
	t.Helper()
	m := &Migrator{}
	files, err := m.loadFiles()
	if err != nil {
		t.Fatalf("loadFiles: %v", err)
	}
	for _, mf := range files {
		if mf.Name != strings.TrimPrefix(name, fmt.Sprintf("%03d_", mf.Version)) && mf.label() != name {
			continue
		}
		keys := map[string][]string{}
		for _, stmt := range mf.Statements {
			create := createTablePattern.FindStringSubmatch(stmt)
			if create == nil {
				continue
			}
			table := strings.ToLower(create[1])
			if idx := strings.LastIndex(table, "."); idx >= 0 {
				table = table[idx+1:]
			}
			pk := primaryKeyPattern.FindStringSubmatch(create[2])
			if pk == nil {
				t.Errorf("CREATE TABLE %s in %s has no parseable PRIMARY KEY; the guard cannot verify it", table, name)
				continue
			}
			keys[table] = splitKeyColumns(pk[1])
		}
		return keys
	}
	t.Fatalf("migration %s not found", name)
	return nil
}

func splitKeyColumns(raw string) []string {
	replacer := strings.NewReplacer("(", " ", ")", " ", "\n", " ", "\t", " ")
	var columns []string
	for _, part := range strings.Split(replacer.Replace(raw), ",") {
		if column := strings.ToLower(strings.TrimSpace(part)); column != "" {
			columns = append(columns, column)
		}
	}
	return columns
}
