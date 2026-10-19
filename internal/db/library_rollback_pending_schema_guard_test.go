package db

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestLibraryRollbackPendingTableExists(t *testing.T) {
	requireEffectiveTableDDL(t, "library_rollback_pending")
}

func TestLibraryRollbackPendingHasExactPerLibraryIdentity(t *testing.T) {
	keys := effectivePrimaryKeys(t, map[string]bool{"library_rollback_pending": true})
	got, ok := keys["library_rollback_pending"]
	if !ok {
		t.Fatal("the migration set does not create library_rollback_pending")
	}
	want := []string{"recovery_bucket", "org_id", "library_id"}
	if len(got) != len(want) {
		t.Fatalf("library_rollback_pending PRIMARY KEY = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("library_rollback_pending PRIMARY KEY = %v, want %v", got, want)
		}
	}
	for _, column := range got {
		if column == "recorded_at" || column == "created_at" {
			t.Fatalf("library_rollback_pending PRIMARY KEY = %v includes %s; identity must be one row per (org_id, library_id), with recovery_bucket only for discovery", got, column)
		}
	}
}

func TestLibraryRollbackPendingHasNoTTL(t *testing.T) {
	m := &Migrator{}
	files, err := m.loadFiles()
	if err != nil {
		t.Fatalf("loadFiles: %v", err)
	}
	var statements []string
	for _, file := range files {
		statements = append(statements, file.Statements...)
	}
	if !regexp.MustCompile(`(?i)default_time_to_live\s*=\s*0\b`).MatchString(requireEffectiveTableDDL(t, "library_rollback_pending")) {
		t.Fatal("library_rollback_pending must set default_time_to_live = 0 explicitly so pending rollback work cannot expire")
	}

	ttl, ok := effectiveTableDefaultTTL(t, "library_rollback_pending")
	if !ok {
		t.Fatal("the migration set does not create library_rollback_pending")
	}
	if ttl != 0 {
		t.Fatalf("effective library_rollback_pending default_time_to_live = %d, want 0; a later ALTER must not expire pending rollback work", ttl)
	}
	if err := statementsMustNotSetRowTTLOnTable(statements, "library_rollback_pending"); err != nil {
		t.Fatal(err)
	}
}

func TestLibraryRollbackPendingInsertHasNoTTL(t *testing.T) {
	src, err := os.ReadFile("library_rollback_pending.go")
	if err != nil {
		t.Fatalf("read library_rollback_pending.go: %v", err)
	}
	text := strings.ToLower(string(src))
	insertAt := strings.Index(text, "func insertlibraryrollbackpending")
	deleteAt := strings.Index(text, "func deletelibraryrollbackpending")
	if insertAt < 0 || deleteAt < 0 || deleteAt <= insertAt {
		t.Fatal("could not locate InsertLibraryRollbackPending in library_rollback_pending.go")
	}
	insertSrc := text[insertAt:deleteAt]
	if strings.Contains(insertSrc, "using ttl") {
		t.Fatal("InsertLibraryRollbackPending must not attach USING TTL; pending rollback work is permanently recoverable until settlement")
	}
}

func TestEffectiveTableDefaultTTLSeesLaterAlter(t *testing.T) {
	ttl, exists := effectiveTableDefaultTTLFromStatements([]string{
		`CREATE TABLE IF NOT EXISTS library_rollback_pending (
			recovery_bucket INT,
			org_id UUID,
			library_id UUID,
			PRIMARY KEY ((recovery_bucket), org_id, library_id)
		) WITH default_time_to_live = 0`,
		`ALTER TABLE library_rollback_pending WITH default_time_to_live = 604800`,
	}, "library_rollback_pending")
	if !exists {
		t.Fatal("synthetic CREATE must register the table")
	}
	if ttl != 604800 {
		t.Fatalf("effective TTL after ALTER = %d, want 604800 (the guard must not stop at the CREATE)", ttl)
	}
}

func TestLibraryRollbackPendingRecoveryIsBucketedNotGlobal(t *testing.T) {
	keys := effectivePrimaryKeys(t, map[string]bool{"library_rollback_pending": true})
	got, ok := keys["library_rollback_pending"]
	if !ok {
		t.Fatal("the migration set does not create library_rollback_pending")
	}
	if got[0] != "recovery_bucket" {
		t.Fatalf("library_rollback_pending partition key must start with recovery_bucket so recovery can scan a bounded set of partitions, got %v", got)
	}
}

func TestStatementsMustNotSetRowTTLOnLibraryRollbackPending(t *testing.T) {
	err := statementsMustNotSetRowTTLOnTable([]string{
		`INSERT INTO library_rollback_pending (recovery_bucket, org_id, library_id) VALUES (0, 1, 2) USING TTL 60`,
	}, "library_rollback_pending")
	if err == nil {
		t.Fatal("USING TTL on library_rollback_pending must fail the guard")
	}
}

var (
	createTableHeaderPattern = regexp.MustCompile(`(?is)^\s*CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([A-Za-z0-9_."]+)`)
	alterTableHeaderPattern  = regexp.MustCompile(`(?is)^\s*ALTER\s+TABLE\s+(?:IF\s+EXISTS\s+)?([A-Za-z0-9_."]+)`)
	defaultTTLAssignPattern  = regexp.MustCompile(`(?i)default_time_to_live\s*=\s*(\d+)`)
	usingTTLPattern          = regexp.MustCompile(`(?i)\bUSING\s+TTL\b`)
)

func effectiveTableDefaultTTL(t *testing.T, table string) (int, bool) {
	t.Helper()
	m := &Migrator{}
	files, err := m.loadFiles()
	if err != nil {
		t.Fatalf("loadFiles: %v", err)
	}
	var statements []string
	for _, file := range files {
		statements = append(statements, file.Statements...)
	}
	return effectiveTableDefaultTTLFromStatements(statements, table)
}

func effectiveTableDefaultTTLFromStatements(statements []string, table string) (int, bool) {
	want := strings.ToLower(table)
	ttlByTable := map[string]int{}
	exists := map[string]bool{}
	for _, stmt := range statements {
		if drop := dropTableStatement.FindStringSubmatch(stmt); drop != nil {
			name := bareTableName(drop[1])
			delete(ttlByTable, name)
			delete(exists, name)
			continue
		}
		if m := createTableHeaderPattern.FindStringSubmatch(stmt); m != nil {
			name := bareTableName(m[1])
			exists[name] = true
			if v, ok := parseDefaultTTLAssignment(stmt); ok {
				ttlByTable[name] = v
			} else {
				ttlByTable[name] = 0
			}
			continue
		}
		if m := alterTableHeaderPattern.FindStringSubmatch(stmt); m != nil {
			name := bareTableName(m[1])
			if v, ok := parseDefaultTTLAssignment(stmt); ok {
				ttlByTable[name] = v
			}
		}
	}
	if !exists[want] {
		return 0, false
	}
	return ttlByTable[want], true
}

func parseDefaultTTLAssignment(stmt string) (int, bool) {
	m := defaultTTLAssignPattern.FindStringSubmatch(stmt)
	if m == nil {
		return 0, false
	}
	ttl, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	return ttl, true
}

func statementsMustNotSetRowTTLOnTable(statements []string, table string) error {
	want := strings.ToLower(table)
	for _, stmt := range statements {
		if !strings.Contains(strings.ToLower(stmt), want) {
			continue
		}
		if usingTTLPattern.MatchString(stmt) {
			return fmt.Errorf("migration statement sets USING TTL on %s; pending rollback work must not expire", table)
		}
	}
	return nil
}
