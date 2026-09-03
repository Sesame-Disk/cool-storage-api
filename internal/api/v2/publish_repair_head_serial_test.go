package v2

import (
	"go/ast"
	"testing"
)

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

	found := false
	ast.Inspect(fn.Body, func(node ast.Node) bool {
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
		if ok && pkgIdent.Name == "gocql" && argSel.Sel.Name == "Serial" {
			found = true
		}
		return true
	})
	if !found {
		t.Fatal("getCanonicalHeadCommitSerial must read head_commit_id at .Consistency(gocql.Serial); a weaker level can return a stale pre-CAS HEAD from a different DC, driving the repair sweep's irreversible cleanup off stale state")
	}
}
