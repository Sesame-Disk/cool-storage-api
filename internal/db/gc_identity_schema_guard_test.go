package db

import (
	"regexp"
	"strings"
	"testing"
)

// R26 schema guard: the migration set must actually declare the PRIMARY KEYs the
// runtime depends on.
//
// It reads the EFFECTIVE schema — every migration in version order, last CREATE
// TABLE for a given table wins — rather than one named file. Which migration
// declares these keys is an accident of history: 018 does today, and the whole set
// is folded into the initial schema once X1 closes. The property being pinned is
// the same either way, so the guard must not have to be rewritten when the file
// moves.
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

	// The identity columns each table's key must END with, in order.
	wantKeySuffix := map[string][]string{
		"gc_block_candidates":        {"storage_class", "storage_key"},
		"gc_block_candidates_by_day": {"storage_class", "storage_key"},
		"gc_queue":                   {"candidate_storage_class", "candidate_storage_key", "identity_at"},
		"gc_pending_items":           {"candidate_storage_class", "candidate_storage_key", "identity_at"},
		"gc_failed_items":            {"candidate_storage_class", "candidate_storage_key", "identity_at"},
		"gc_failed_items_by_expiry":  {"candidate_storage_class", "candidate_storage_key", "identity_at"},
	}

	tracked := map[string]bool{}
	for table := range wantKeySuffix {
		tracked[table] = true
	}

	keys := effectivePrimaryKeys(t, tracked)
	if len(keys) == 0 {
		t.Fatal("no CREATE TABLE statements found in the migration set; this guard is vacuous")
	}

	for table, want := range wantKeySuffix {
		key, ok := keys[table]
		if !ok {
			t.Errorf("the migration set does not create %s; the runtime expects it to carry the exact-P identity key", table)
			continue
		}
		if len(key) < len(want) {
			t.Errorf("R26 REGRESSION: %s PRIMARY KEY is %v, too short to end with %v", table, key, want)
			continue
		}
		got := key[len(key)-len(want):]
		for i := range want {
			if got[i] == want[i] {
				continue
			}
			t.Errorf("R26 REGRESSION: %s PRIMARY KEY = %v.\n"+
				"It must END with %v, in that order. Without those columns two lives of one logical block "+
				"collapse into a single row and a delayed lifecycle for a dead incarnation addresses a live "+
				"one's work — the defect the exact-P identity exists to remove. Nothing in the Go code changes when "+
				"this key does, so no other gate can catch it.",
				table, key, want)
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
	keys := effectivePrimaryKeys(t, map[string]bool{"gc_block_candidates": true})
	key, ok := keys["gc_block_candidates"]
	if !ok {
		t.Fatal("the migration set does not create gc_block_candidates; this guard is vacuous")
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
	dropTableStatement = regexp.MustCompile(`(?is)^\s*DROP\s+TABLE\s+(?:IF\s+EXISTS\s+)?([A-Za-z0-9_."]+)`)
)

// effectivePrimaryKeys returns table -> ordered primary key columns, for the tracked
// tables only, as the schema
// the migration set actually leaves behind: migrations are walked in version order,
// a CREATE TABLE records (and replaces) a table's key, and a DROP TABLE removes it
// again. That is what a freshly migrated keyspace ends up with, whether the keys are
// declared by a late migration or by a consolidated initial schema.
//
// The partition-key parentheses are flattened away: ((a, b), c) reads as [a b c].
func effectivePrimaryKeys(t *testing.T, tracked map[string]bool) map[string][]string {
	t.Helper()
	m := &Migrator{}
	files, err := m.loadFiles()
	if err != nil {
		t.Fatalf("loadFiles: %v", err)
	}
	keys := map[string][]string{}
	for _, mf := range files {
		for _, stmt := range mf.Statements {
			if drop := dropTableStatement.FindStringSubmatch(stmt); drop != nil {
				delete(keys, bareTableName(drop[1]))
				continue
			}

			create := createTablePattern.FindStringSubmatch(stmt)
			if create == nil {
				continue
			}
			table := bareTableName(create[1])
			if !tracked[table] {
				continue
			}
			pk := primaryKeyPattern.FindStringSubmatch(create[2])
			if pk == nil {
				t.Errorf("CREATE TABLE %s in %s has no parseable PRIMARY KEY; the guard cannot verify it", table, mf.label())
				continue
			}
			keys[table] = splitKeyColumns(pk[1])
		}
	}
	return keys
}

// bareTableName strips a keyspace qualifier and normalises case.
func bareTableName(raw string) string {
	table := strings.ToLower(strings.Trim(strings.TrimSpace(raw), `";`))
	if idx := strings.LastIndex(table, "."); idx >= 0 {
		table = table[idx+1:]
	}
	return table
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
