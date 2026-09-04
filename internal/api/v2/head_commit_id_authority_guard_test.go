package v2

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"strings"
	"testing"
)

// headCommitIDGuardStatementText reconstructs the source text of stmt so its
// content -- not just its AST shape -- can be checked for the required
// substrings. Reconstructing from the AST (rather than slicing raw file
// bytes) keeps this robust to reformatting.
func headCommitIDGuardStatementText(fset *token.FileSet, stmt ast.Stmt) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, stmt); err != nil {
		return ""
	}
	return buf.String()
}

// TestNoLibrariesHeadCommitIDWriterBypassesPublicationAuthority is a source
// contract, not a runtime check: it parses every non-test .go file in this
// package and fails the build if any `UPDATE libraries SET ...` CQL literal
// that writes head_commit_id is issued without BOTH a condition on
// publication_state in the same statement AND .SerialConsistency(gocql.Serial)
// on the same call chain.
//
// W2a's terminal-authority model (docs/CHANGELOG.md "W2a: terminal
// publication authority") depends on every head_commit_id writer honoring
// that gate: publication_state lives in the same global SERIAL/Paxos domain
// as head_commit_id specifically so a hard-deleted (TERMINAL) library can
// never accept a new HEAD from a writer that doesn't know to check. A future
// writer added elsewhere that forgets either half would silently reopen that
// exact class of bug -- with no compiler error, since Cassandra has no
// server-side foreign-key or check-constraint mechanism to enforce it. This
// test exists so that mistake fails the build instead of shipping silently.
//
// This is a narrow, textual source contract (it does not evaluate dynamically
// built CQL, e.g. a query assembled by string concatenation across several
// statements) -- see this package's libraries.go UpdateLibrary handler for an
// example of that shape, which today never touches head_commit_id. If a
// future head_commit_id writer is built that way, this test will not see it;
// broadening the contract is left for that occasion, not built speculatively
// here.
func TestNoLibrariesHeadCommitIDWriterBypassesPublicationAuthority(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			var stmt ast.Stmt
			switch v := n.(type) {
			case *ast.AssignStmt:
				stmt = v
			case *ast.ExprStmt:
				stmt = v
			default:
				return true
			}
			text := headCommitIDGuardStatementText(fset, stmt)
			lower := strings.ToLower(text)
			if !strings.Contains(lower, "update libraries set") || strings.Contains(lower, "libraries_by_id") {
				return true
			}
			if !strings.Contains(lower, "head_commit_id") {
				return true
			}
			checked++
			pos := fset.Position(stmt.Pos())
			if !strings.Contains(text, "publication_state") {
				t.Errorf("%s: statement writes libraries.head_commit_id without a publication_state condition in the same statement -- W2a requires every HEAD writer to gate on publication_state so a TERMINAL (hard-deleted) library can never accept a new HEAD:\n%s", pos, text)
			}
			if !strings.Contains(text, "SerialConsistency(gocql.Serial)") {
				t.Errorf("%s: statement writes libraries.head_commit_id without .SerialConsistency(gocql.Serial) on the same call chain -- a weaker consistency can observe a stale, pre-CAS publication_state from another DC:\n%s", pos, text)
			}
			return true
		})
	}
	const wantWriters = 4 // libraryHeadCASExecuteFn (primary + legacy retry), InitializeLibraryFS (primary + legacy retry)
	if checked != wantWriters {
		t.Fatalf("found %d libraries.head_commit_id writer statements in package v2, want %d -- update this count (and re-audit each writer) if a head_commit_id writer was added, removed, or restructured", checked, wantWriters)
	}
}
