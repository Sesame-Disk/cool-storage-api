package gc

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// projectionIdentityInsert matches the only statement R22b leaves legal on the
// discovery projection: an INSERT naming exactly the five primary-key columns.
var projectionIdentityInsert = regexp.MustCompile(
	`(?is)\bINSERT\s+INTO\s+gc_s3_orphans_by_day\s*\(\s*first_seen_day\s*,\s*bucket\s*,\s*first_seen_at\s*,\s*org_id\s*,\s*block_id\s*\)`)

// projectionUpdate matches any UPDATE of the projection. See the test below for
// why this is not merely stylistic.
var projectionUpdate = regexp.MustCompile(`(?is)\bUPDATE\s+gc_s3_orphans_by_day\b`)

// TestR22bProjectionWriteIsInsert pins the discovery write to an INSERT of the
// identity columns and nothing else.
//
// The load-bearing half is the column list: a payload column reappearing here is
// the first move of a revert, and it would be rejected by Cassandra at runtime
// inside a GC sweep nobody is watching rather than at the point it was written.
//
// The INSERT half is a conditional guard, and it is worth being precise about why
// it is not redundant today. After migration 014 every column belongs to the
// primary key, so a row carries no regular cells and its primary-key liveness IS
// the row; only INSERT writes that liveness. An UPDATE of this table is not merely
// wrong, it is currently inexpressible: CQL requires a SET over a non-key column
// and none remains. The two clauses of this test therefore protect each other —
// re-add a regular column and an UPDATE-based writer becomes expressible again.
//
// Be precise about what that would break, because the obvious answer is wrong: an
// UPDATE-created row is NOT invisible. Cassandra considers a row present when it
// has live cells even with no PK liveness — that is exactly why UPDATE upserts —
// so the day scan would enumerate it normally. The defect is deferred instead: the
// row's lifetime would be the payload cell's, so it would vanish the moment that
// cell is deleted or expires under the table's 90-day TTL, silently dropping an
// identity that was still supposed to be recoverable. R22b's property is that a
// discovery identity is durable on its own; binding its existence to a payload
// cell is what gives that away.
func TestR22bProjectionWriteIsInsert(t *testing.T) {
	path := filepath.Join("store_cassandra.go")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	var writer *ast.FuncDecl
	for _, declaration := range file.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if ok && fn.Name.Name == "upsertS3OrphanProjection" {
			writer = fn
			break
		}
	}
	if writer == nil {
		t.Fatal("upsertS3OrphanProjection not found; the discovery writer must stay a single named function so this gate can find it")
	}

	// Three identity parameters and nothing else. A payload parameter reappearing
	// here is the first move of a revert, before any CQL changes.
	params := 0
	for _, field := range writer.Type.Params.List {
		params += len(field.Names)
	}
	if params != 3 {
		t.Errorf("upsertS3OrphanProjection takes %d parameters, want 3 (orgID, blockID, firstSeenAt): the projection carries identity only since migration 014", params)
	}

	insertFound := false
	for _, query := range stringLiteralsIn(writer) {
		if projectionUpdate.MatchString(query) {
			t.Fatalf("discovery write uses UPDATE: with every column in the primary key the row would have no row marker and no SELECT would return it: %s", query)
		}
		if !discoveryOrphanTable.MatchString(query) {
			continue
		}
		if !projectionIdentityInsert.MatchString(query) {
			t.Fatalf("discovery write is not an INSERT of exactly (first_seen_day, bucket, first_seen_at, org_id, block_id): %s", query)
		}
		insertFound = true
		// Lowercased: unquoted CQL identifiers are case-insensitive, and the table
		// match above already is.
		normalized := strings.ToLower(query)
		for _, forbidden := range canonicalPayloadColumns {
			if strings.Contains(normalized, forbidden) {
				t.Fatalf("discovery write names dropped payload column %q; migration 014 removed it and Cassandra will reject the statement: %s", forbidden, query)
			}
		}
	}
	if !insertFound {
		t.Fatal("upsertS3OrphanProjection does not INSERT into gc_s3_orphans_by_day")
	}
}
