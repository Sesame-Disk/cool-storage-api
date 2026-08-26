//go:build integration

package integration

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
)

// Migration 018 drops the GC tables it recreates. That is correct for the
// contract this release ships under — a clean keyspace, migrated before any node
// produces GC work — and catastrophic outside it: running the binary against a
// keyspace that already accumulated GC work would erase queue, pending and DLQ
// rows, including the non-block item types that have nothing to do with the
// exact-P change.
//
// A comment in the .cql file cannot enforce that. Migrator.preflightDestructive
// can, and this is the evidence that it does — against the real engine, because
// the whole check is a live read of a live table. The unit tests in internal/db
// cover the parser and the wiring; only this can show the barrier actually
// closing on a table that holds a row.
func TestMigratorRefusesToDropATableThatStillHoldsRows(t *testing.T) {
	requireCassandra(t)

	database := shareProjectionDBForTest(t)
	session := database.Session()

	// A scratch table stands in for the real drop targets. Using a synthetic one
	// keeps the test from touching the shared keyspace's GC state, while
	// exercising exactly the code path 018 goes through.
	table := fmt.Sprintf("preflight_probe_%s", strings.ReplaceAll(uuid.NewString(), "-", ""))
	if err := session.Query(fmt.Sprintf(
		`CREATE TABLE %s (id UUID PRIMARY KEY, seeded_at TIMESTAMP)`, table)).Exec(); err != nil {
		t.Fatalf("create probe table: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Query(fmt.Sprintf(`DROP TABLE IF EXISTS %s`, table)).Exec()
	})

	migration := fmt.Sprintf("DROP TABLE IF EXISTS %s;", table)

	// EMPTY: the migration is allowed through. This half matters as much as the
	// other one — a barrier that refuses everything would simply block the
	// release it is meant to protect.
	if err := dbpkg.PreflightDestructiveForTest(session, migration); err != nil {
		t.Fatalf("preflight refused a destructive migration whose target is empty: %v", err)
	}

	// OCCUPIED: the migration must be refused, and the message must name the
	// table so an operator can act on it.
	if err := session.Query(fmt.Sprintf(
		`INSERT INTO %s (id, seeded_at) VALUES (?, ?)`, table), uuid.New().String(), time.Now().UTC()).Exec(); err != nil {
		t.Fatalf("seed probe row: %v", err)
	}

	err := dbpkg.PreflightDestructiveForTest(session, migration)
	if err == nil {
		t.Fatal("PREFLIGHT REGRESSION: a table-dropping migration was allowed to run against a table that still holds rows. " +
			"Deploying this release onto a keyspace with existing GC work would erase the queue, the pending markers and the DLQ.")
	}
	if !strings.Contains(err.Error(), table) {
		t.Errorf("preflight refusal does not name the offending table; an operator cannot act on it: %v", err)
	}
	if !strings.Contains(err.Error(), "still holds rows") {
		t.Errorf("preflight refusal does not say why it refused: %v", err)
	}

	// And it refused rather than dropping: the table and its row survive.
	var count int
	if err := session.Query(fmt.Sprintf(`SELECT COUNT(*) FROM %s`, table)).Scan(&count); err != nil {
		t.Fatalf("PREFLIGHT REGRESSION: the probe table is unreadable after a refused migration (%v); it should have been left untouched", err)
	}
	if count != 1 {
		t.Fatalf("PREFLIGHT REGRESSION: probe table holds %d rows after a refused migration, want 1 — the refusal must not mutate anything", count)
	}
}

// TestMigratorPreflightPassesAMissingTable pins the convergence half: a re-run
// after a partially applied destructive migration must not deadlock on a table
// that is already gone.
func TestMigratorPreflightPassesAMissingTable(t *testing.T) {
	requireCassandra(t)

	session := shareProjectionDBForTest(t).Session()
	table := fmt.Sprintf("preflight_absent_%s", strings.ReplaceAll(uuid.NewString(), "-", ""))
	if err := dbpkg.PreflightDestructiveForTest(session, fmt.Sprintf("DROP TABLE IF EXISTS %s;", table)); err != nil {
		t.Fatalf("preflight refused a migration whose target does not exist (%v); a re-run after a partial apply would never converge", err)
	}
}
