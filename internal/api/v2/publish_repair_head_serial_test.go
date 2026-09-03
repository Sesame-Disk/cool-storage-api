package v2

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// r3FindConsistencyPin scans body for a `.Consistency(pkg.Level)` call site
// and reports whether one matching pkg/level exists.
func r3FindConsistencyPin(body ast.Node, pkg, level string) bool {
	found := false
	ast.Inspect(body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Consistency" || len(call.Args) != 1 {
			return true
		}
		argSel, ok := call.Args[0].(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkgIdent, ok := argSel.X.(*ast.Ident)
		if ok && pkgIdent.Name == pkg && argSel.Sel.Name == level {
			found = true
		}
		return true
	})
	return found
}

// r3ParseVarFuncLitBody finds a package-level `var name = func(...) {...}`
// declaration in path and returns the literal's body, for the test-seam
// pattern used throughout this package (a plain `func` declaration is not
// how these are written, so r3ParseFunction cannot find them).
func r3ParseVarFuncLitBody(t *testing.T, path, name string) *ast.BlockStmt {
	t.Helper()
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("R3 SOURCE CONTRACT: parse %s: %v", path, err)
	}
	for _, decl := range parsed.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.VAR {
			continue
		}
		for _, spec := range genDecl.Specs {
			valueSpec, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, ident := range valueSpec.Names {
				if ident.Name != name || i >= len(valueSpec.Values) {
					continue
				}
				funcLit, ok := valueSpec.Values[i].(*ast.FuncLit)
				if !ok {
					continue
				}
				return funcLit.Body
			}
		}
	}
	t.Fatalf("R3 SOURCE CONTRACT: var %s = func(...) {...} not found in %s", name, path)
	return nil
}

// TestGetCanonicalHeadCommitSerialPinsSerialConsistency freezes the fix for
// the repair-reachability multi-DC gap found during the W2 final audit:
// publishedBlockReferenceRepairHeadCommitFn (publish_repair.go) drives an
// IRREVERSIBLE decision -- delete a commit row and its pub: block references
// -- off this function's HEAD value. A plain LOCAL_QUORUM read (what
// GetHeadCommitID/getCanonicalHeadCommit use for every other caller) can
// return a stale pre-CAS HEAD when the repair sweep runs in a different DC
// than the one that committed an ambiguous CAS, because the write half of
// that same LWT is an ordinary LOCAL_QUORUM write that only reaches other DCs
// via asynchronous replication -- the same LOCAL_QUORUM-write/
// LOCAL_QUORUM-read non-intersection class of gap X2 closed for
// block_references. A SERIAL read forces completion of any in-flight Paxos
// round first, so it observes the true HEAD regardless of which DC serves it.
// If a future edit removes the pin, this test fails the build instead of
// silently reintroducing the gap.
func TestGetCanonicalHeadCommitSerialPinsSerialConsistency(t *testing.T) {
	path := r3SourcePath("internal", "api", "v2", "fs_helpers.go")
	_, fn := r3ParseFunction(t, path, "getCanonicalHeadCommitSerial")
	if !r3FindConsistencyPin(fn.Body, "gocql", "Serial") {
		t.Fatal("getCanonicalHeadCommitSerial must read head_commit_id at .Consistency(gocql.Serial); a weaker level can return a stale pre-CAS HEAD from a different DC, driving the repair sweep's irreversible cleanup off stale state")
	}
}

// TestPublishedBlockReferenceRepairCommitParentFnPinsEachQuorumConsistency
// freezes the second half of the same fix: the repair sweep's ancestry walk
// (publishedBlockReferenceRepairCommitReachableFn, via onlyOfficeCommitReachable)
// reads each ancestor's parent_id off the ordinary commits table, which is
// written at plain LOCAL_QUORUM (insertCommit, fs_helpers.go). A LOCAL_QUORUM
// read of that row from a DIFFERENT DC than the one that inserted it can miss
// it during replication lag -- the same non-intersection gap
// getCanonicalHeadCommitSerial closes for HEAD itself, but commits rows are
// ordinary immutable inserts, not the LWT value SERIAL linearizes, so the fix
// here is EACH_QUORUM (a quorum in every DC, guaranteed to intersect the
// write's own home DC) rather than SERIAL. If a future edit removes the pin,
// this test fails the build instead of silently reintroducing the gap.
func TestPublishedBlockReferenceRepairCommitParentFnPinsEachQuorumConsistency(t *testing.T) {
	path := r3SourcePath("internal", "api", "v2", "publish_repair.go")
	body := r3ParseVarFuncLitBody(t, path, "publishedBlockReferenceRepairCommitParentFn")
	if !r3FindConsistencyPin(body, "gocql", "EachQuorum") {
		t.Fatal("publishedBlockReferenceRepairCommitParentFn must read parent_id at .Consistency(gocql.EachQuorum); a weaker level can miss a commit row that has not yet replicated to this DC, truncating the ancestry walk and producing a false-unreachable verdict")
	}
}
