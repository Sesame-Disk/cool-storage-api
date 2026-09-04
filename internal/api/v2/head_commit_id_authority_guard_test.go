package v2

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// headCommitIDGuardIfKeyword matches the CQL `IF` keyword as a standalone
// word (case-insensitive). It only ever matches inside the CQL string
// literal itself: an AssignStmt/ExprStmt/ReturnStmt cannot syntactically
// contain a nested Go `if` statement, so there is no ambiguity between CQL's
// IF and Go's if.
var headCommitIDGuardIfKeyword = regexp.MustCompile(`(?i)\bif\b`)

// headCommitIDGuardViolation is one unmet requirement found on a single
// libraries.head_commit_id writer call site.
type headCommitIDGuardViolation struct {
	pos     token.Position
	message string
}

func (v headCommitIDGuardViolation) String() string {
	return v.pos.String() + ": " + v.message
}

// headCommitIDGuardQueryLiteralText extracts and unquotes the CQL text of a
// `.Query(<raw string literal>, ...)` call's first argument. Returns ok=false
// if the argument is not a plain string literal (e.g. a dynamically built
// query), which this guard does not evaluate -- see the contract comment on
// checkHeadCommitIDWriters.
func headCommitIDGuardQueryLiteralText(call *ast.CallExpr) (text string, ok bool) {
	if len(call.Args) == 0 {
		return "", false
	}
	lit, isLit := call.Args[0].(*ast.BasicLit)
	if !isLit || lit.Kind != token.STRING {
		return "", false
	}
	unquoted, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return unquoted, true
}

func headCommitIDGuardNormalizedCQL(cql string) string {
	return strings.Join(strings.Fields(strings.ToLower(cql)), " ")
}

func headCommitIDGuardLibrariesUpdateSet(cql string) (string, bool) {
	normalized := headCommitIDGuardNormalizedCQL(cql)
	if !strings.HasPrefix(normalized, "update ") {
		return "", false
	}
	rest := strings.TrimPrefix(normalized, "update ")
	setLoc := strings.Index(rest, " set ")
	if setLoc < 0 {
		return "", false
	}
	table := strings.ReplaceAll(strings.TrimSpace(rest[:setLoc]), "\"", "")
	parts := strings.Split(table, ".")
	if len(parts) == 0 || parts[len(parts)-1] != "libraries" {
		return "", false
	}
	setStart := setLoc + len(" set ")
	whereLoc := strings.Index(rest[setStart:], " where ")
	if whereLoc < 0 {
		return rest[setStart:], true
	}
	return rest[setStart : setStart+whereLoc], true
}

func headCommitIDGuardSetTouchesHead(setClause string) bool {
	for _, assignment := range strings.Split(setClause, ",") {
		lhs, _, ok := strings.Cut(strings.TrimSpace(assignment), "=")
		if ok && strings.Trim(strings.TrimSpace(lhs), "\"") == "head_commit_id" {
			return true
		}
	}
	return false
}

var headCommitIDGuardPublicationActivePredicate = regexp.MustCompile("(?:^|[\\s(])publication_state\\s*=\\s*\\?(?:$|[\\s,)])")

func headCommitIDGuardPlaceholderCount(cql string) int {
	count := 0
	var quote rune
	for _, ch := range cql {
		if quote != 0 {
			if ch == quote {
				quote = 0
			}
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = ch
			continue
		}
		if ch == '?' {
			count++
		}
	}
	return count
}

func headCommitIDGuardIsActiveExpr(expr ast.Expr) bool {
	switch value := expr.(type) {
	case *ast.BasicLit:
		if value.Kind != token.STRING {
			return false
		}
		text, err := strconv.Unquote(value.Value)
		return err == nil && strings.EqualFold(strings.TrimSpace(text), "ACTIVE")
	case *ast.SelectorExpr:
		return value.Sel.Name == "LibraryPublicationStateActive"
	default:
		return false
	}
}

func headCommitIDGuardHasActivePublicationBind(cql string, call *ast.CallExpr, ifLoc []int) bool {
	normalized := headCommitIDGuardNormalizedCQL(cql)
	match := headCommitIDGuardPublicationActivePredicate.FindStringIndex(normalized[ifLoc[0]:])
	if match == nil {
		return false
	}
	targetEnd := ifLoc[0] + match[1]
	bindOrdinal := headCommitIDGuardPlaceholderCount(normalized[:targetEnd])
	bindIndex := bindOrdinal
	return bindIndex > 0 && bindIndex < len(call.Args) && headCommitIDGuardIsActiveExpr(call.Args[bindIndex])
}

// headCommitIDGuardHasChainedSerialConsistency reports whether
// `.SerialConsistency(gocql.Serial)` is called directly on the result of
// queryCall (i.e. `queryCall.SerialConsistency(gocql.Serial)...`), found
// anywhere in file. Matching by AST node identity (not by reconstructing
// source text) means this is exact even when several `.Query(...)` calls to
// different tables exist in the same file or the same statement.
func headCommitIDGuardHasChainedSerialConsistency(file *ast.File, queryCall *ast.CallExpr) bool {
	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		call, isCall := n.(*ast.CallExpr)
		if !isCall {
			return true
		}
		sel, isSel := call.Fun.(*ast.SelectorExpr)
		if !isSel || sel.Sel.Name != "SerialConsistency" || sel.X != ast.Expr(queryCall) {
			return true
		}
		if len(call.Args) != 1 {
			return true
		}
		argSel, isSel := call.Args[0].(*ast.SelectorExpr)
		if !isSel {
			return true
		}
		pkgIdent, isIdent := argSel.X.(*ast.Ident)
		if isIdent && pkgIdent.Name == "gocql" && argSel.Sel.Name == "Serial" {
			found = true
		}
		return true
	})
	return found
}

// checkHeadCommitIDWriters walks file for every `X.Query(<raw CQL literal>,
// ...)` call whose CQL text is an `UPDATE libraries SET ...` statement
// writing head_commit_id (the canonical table; libraries_by_id is a
// derived/projection table this guard does not police -- it is always
// updated unconditionally, after the canonical CAS has already succeeded).
// For each one found, it requires:
//  1. an `IF` clause is present at all, and
//  2. that IF clause -- not the SET list, which also legitimately mentions
//     publication_state -- contains `publication_state`, and
//  3. `.SerialConsistency(gocql.Serial)` is chained directly onto that exact
//     Query call.
//
// This finds the writer via its Query CallExpr rather than by matching a
// specific enclosing statement shape (AssignStmt/ExprStmt/...), so it also
// catches a writer whose call chain is directly returned, passed as an
// argument, or otherwise not a bare assignment.
//
// It does not evaluate dynamically built CQL (e.g. a query string assembled
// by concatenation across several statements, as internal/api/v2/libraries.go's
// UpdateLibrary handler does for other columns) -- broadening this contract
// to cover that shape is left for the day a head_commit_id writer is
// actually built that way, not built speculatively now.
func checkHeadCommitIDWriters(fset *token.FileSet, file *ast.File) (violations []headCommitIDGuardViolation, found int) {
	ast.Inspect(file, func(n ast.Node) bool {
		call, isCall := n.(*ast.CallExpr)
		if !isCall {
			return true
		}
		sel, isSel := call.Fun.(*ast.SelectorExpr)
		if !isSel || sel.Sel.Name != "Query" {
			return true
		}
		cql, ok := headCommitIDGuardQueryLiteralText(call)
		if !ok {
			return true
		}
		normalizedCQL := headCommitIDGuardNormalizedCQL(cql)
		setClause, isLibrariesUpdate := headCommitIDGuardLibrariesUpdateSet(cql)
		if !isLibrariesUpdate || !headCommitIDGuardSetTouchesHead(setClause) {
			return true
		}
		found++
		pos := fset.Position(call.Pos())

		ifLoc := headCommitIDGuardIfKeyword.FindStringIndex(normalizedCQL)
		switch {
		case ifLoc == nil:
			violations = append(violations, headCommitIDGuardViolation{pos, "writes libraries.head_commit_id with no IF clause at all -- W2a requires every HEAD writer to be a conditional LWT gated on publication_state, not an unconditional write:\n" + cql})
		case !headCommitIDGuardHasActivePublicationBind(cql, call, ifLoc):
			violations = append(violations, headCommitIDGuardViolation{pos, "writes libraries.head_commit_id but its IF clause does not condition on publication_state = ? bound to ACTIVE -- a TERMINAL (hard-deleted) library could accept a new HEAD:\n" + cql})
		}
		if !headCommitIDGuardHasChainedSerialConsistency(file, call) {
			violations = append(violations, headCommitIDGuardViolation{pos, "writes libraries.head_commit_id without .SerialConsistency(gocql.Serial) chained directly onto this Query call -- a weaker consistency can observe a stale, pre-CAS publication_state from another DC:\n" + cql})
		}
		return true
	})
	return violations, found
}

// TestNoLibrariesHeadCommitIDWriterBypassesPublicationAuthority is a source
// contract, not a runtime check: it parses every non-test .go file in this
// package and fails the build if checkHeadCommitIDWriters reports any
// violation, or if the writer count drifts from the pinned total -- so a new
// call site, compliant or not, is a visible, must-review diff instead of a
// silent pass-through.
//
// W2a's terminal-authority model (docs/CHANGELOG.md "W2a: terminal
// publication authority") depends on every head_commit_id writer honoring
// this gate: publication_state lives in the same global SERIAL/Paxos domain
// as head_commit_id specifically so a hard-deleted (TERMINAL) library can
// never accept a new HEAD from a writer that doesn't know to check. Cassandra
// has no server-side foreign-key or check-constraint mechanism to enforce
// this, so nothing but this test stands between a future edit and silently
// reopening that exact class of bug.
func TestNoLibrariesHeadCommitIDWriterBypassesPublicationAuthority(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	fset := token.NewFileSet()
	totalFound := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		violations, found := checkHeadCommitIDWriters(fset, file)
		totalFound += found
		for _, v := range violations {
			t.Error(v.String())
		}
	}
	const wantWriters = 2 // libraryHeadCASExecuteFn, InitializeLibraryFS -- one CAS each; legacy-NULL retry was removed (docs/CHANGELOG.md "W2a pre-merge closure round 3": clean-deploy codebase, no legacy dataset)
	if totalFound != wantWriters {
		t.Fatalf("found %d libraries.head_commit_id writer statements in package v2, want %d -- update this count (and re-audit each writer) if a head_commit_id writer was added, removed, or restructured", totalFound, wantWriters)
	}
}

// headCommitIDGuardParseSnippet parses a self-contained Go source snippet
// (not a file on disk) for the mutation tests below, so each one exercises
// checkHeadCommitIDWriters against a specific, deliberately mutated call
// shape without touching any real production file.
func headCommitIDGuardParseSnippet(t *testing.T, src string) (*token.FileSet, *ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "mutation_snippet.go", src, 0)
	if err != nil {
		t.Fatalf("parse mutation snippet: %v\n%s", err, src)
	}
	return fset, file
}

// TestHeadCommitIDGuardAcceptsCompliantWriter is the positive control: a
// well-formed writer, shaped exactly like the four real ones in this
// package, must report zero violations and found=1. It also proves the
// CallExpr-based detector -- unlike a statement-shape-based one -- still
// finds the writer when the call chain is the operand of a bare ReturnStmt
// rather than an AssignStmt, the specific completeness gap a review of an
// earlier version of this guard flagged.
func TestHeadCommitIDGuardAcceptsCompliantWriter(t *testing.T) {
	const src = `package v2

func compliantWriter() (bool, map[string]interface{}, error) {
	casState := map[string]interface{}{}
	return session.Query(` + "`" + `
		UPDATE libraries SET head_commit_id = ?, publication_state = ?
		WHERE org_id = ? AND library_id = ?
		IF head_commit_id = null AND publication_state = ?
	` + "`" + `, headCommitID, "ACTIVE", orgID, repoID, "ACTIVE").
		SerialConsistency(gocql.Serial).MapScanCAS(casState)
}
`
	fset, file := headCommitIDGuardParseSnippet(t, src)
	violations, found := checkHeadCommitIDWriters(fset, file)
	if found != 1 {
		t.Fatalf("found = %d, want 1", found)
	}
	for _, v := range violations {
		t.Errorf("compliant writer must report no violations, got: %s", v)
	}
}

// TestHeadCommitIDGuardCatchesMissingPublicationStateInIFClause is the
// mutation test for the exact gap a review found in an earlier version of
// this guard: it checked whether the whole statement's text contained
// "publication_state" anywhere, which the SET list alone always satisfies.
// Removing `AND publication_state = ?` from the IF clause while leaving it
// in SET -- exactly the mutation that review specified -- must still be
// caught.
func TestHeadCommitIDGuardCatchesMissingPublicationStateInIFClause(t *testing.T) {
	const src = `package v2

func mutatedWriter() (bool, map[string]interface{}, error) {
	casState := map[string]interface{}{}
	applied, err := session.Query(` + "`" + `
		UPDATE libraries SET head_commit_id = ?, publication_state = ?
		WHERE org_id = ? AND library_id = ?
		IF head_commit_id = null
	` + "`" + `, headCommitID, "ACTIVE", orgID, repoID).
		SerialConsistency(gocql.Serial).MapScanCAS(casState)
	return applied, casState, err
}
`
	fset, file := headCommitIDGuardParseSnippet(t, src)
	violations, found := checkHeadCommitIDWriters(fset, file)
	if found != 1 {
		t.Fatalf("found = %d, want 1", found)
	}
	if len(violations) != 1 || !strings.Contains(violations[0].message, "does not condition on publication_state") {
		t.Fatalf("violations = %v, want exactly one 'does not condition on publication_state' violation", violations)
	}
}

// TestHeadCommitIDGuardCatchesMissingSerialConsistency mutates a compliant
// writer to drop .SerialConsistency(gocql.Serial) from the call chain (a
// weaker default consistency could observe a stale publication_state from
// another DC) and requires the guard to catch it.
func TestHeadCommitIDGuardCatchesMissingSerialConsistency(t *testing.T) {
	const src = `package v2

func mutatedWriter() (bool, map[string]interface{}, error) {
	casState := map[string]interface{}{}
	applied, err := session.Query(` + "`" + `
		UPDATE libraries SET head_commit_id = ?, publication_state = ?
		WHERE org_id = ? AND library_id = ?
		IF head_commit_id = null AND publication_state = ?
	` + "`" + `, headCommitID, "ACTIVE", orgID, repoID, "ACTIVE").
		MapScanCAS(casState)
	return applied, casState, err
}
`
	fset, file := headCommitIDGuardParseSnippet(t, src)
	violations, found := checkHeadCommitIDWriters(fset, file)
	if found != 1 {
		t.Fatalf("found = %d, want 1", found)
	}
	if len(violations) != 1 || !strings.Contains(violations[0].message, "SerialConsistency") {
		t.Fatalf("violations = %v, want exactly one SerialConsistency violation", violations)
	}
}

// TestHeadCommitIDGuardCatchesUnconditionalWrite covers the case that is
// strictly worse than a missing publication_state condition: no IF clause at
// all, i.e. not even a CAS.
func TestHeadCommitIDGuardCatchesUnconditionalWrite(t *testing.T) {
	const src = `package v2

func mutatedWriter() error {
	return session.Query(` + "`" + `
		UPDATE libraries SET head_commit_id = ?, publication_state = ?
		WHERE org_id = ? AND library_id = ?
	` + "`" + `, headCommitID, "ACTIVE", orgID, repoID).
		SerialConsistency(gocql.Serial).Exec()
}
`
	fset, file := headCommitIDGuardParseSnippet(t, src)
	violations, found := checkHeadCommitIDWriters(fset, file)
	if found != 1 {
		t.Fatalf("found = %d, want 1", found)
	}
	if len(violations) != 1 || !strings.Contains(violations[0].message, "no IF clause at all") {
		t.Fatalf("violations = %v, want exactly one 'no IF clause at all' violation", violations)
	}
}
