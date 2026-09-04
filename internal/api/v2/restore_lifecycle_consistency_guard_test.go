package v2

import (
	"go/ast"
	"strconv"
	"strings"
	"testing"
)

func restoreCanonicalLifecycleQueryText(call *ast.CallExpr) (string, bool) {
	if len(call.Args) == 0 {
		return "", false
	}
	literal, ok := call.Args[0].(*ast.BasicLit)
	if !ok {
		return "", false
	}
	text, err := strconv.Unquote(literal.Value)
	if err != nil {
		return "", false
	}
	return strings.Join(strings.Fields(strings.ToLower(text)), " "), true
}

func restoreCanonicalLifecycleQueryIsEachQuorum(call *ast.CallExpr) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "Consistency" || len(call.Args) != 1 {
		return false
	}
	level, ok := call.Args[0].(*ast.SelectorExpr)
	if !ok || level.Sel.Name != "EachQuorum" {
		return false
	}
	packageName, ok := level.X.(*ast.Ident)
	if !ok || packageName.Name != "gocql" {
		return false
	}
	query, ok := selector.X.(*ast.CallExpr)
	if !ok {
		return false
	}
	querySelector, ok := query.Fun.(*ast.SelectorExpr)
	if !ok || querySelector.Sel.Name != "Query" {
		return false
	}
	text, ok := restoreCanonicalLifecycleQueryText(query)
	return ok && strings.HasPrefix(text, "select deleted_at, publication_state from libraries where ")
}

// Restore's canonical lifecycle decision must use the same cross-DC visibility
// contract as the durable deleted_libraries marker. The library lifecycle fence
// orders writers, but it does not make writes in another partition visible to a
// LOCAL_QUORUM read in the next DC. Keep this assertion tied to the Query call
// itself so an EACH_QUORUM pin on the marker or final cleanup cannot satisfy it.
func TestRestoreCanonicalLifecycleReadPinsEachQuorum(t *testing.T) {
	path := r3SourcePath("internal", "api", "v2", "write_helpers.go")
	_, fn := r3ParseFunction(t, path, "restoreDeletedLibrary")
	found := false
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if ok && restoreCanonicalLifecycleQueryIsEachQuorum(call) {
			found = true
		}
		return true
	})
	if !found {
		t.Fatal("restoreDeletedLibrary must pin its canonical libraries lifecycle read to .Consistency(gocql.EachQuorum); the fence partition does not make a LOCAL_QUORUM read in another DC observe the completed soft-delete")
	}
}
