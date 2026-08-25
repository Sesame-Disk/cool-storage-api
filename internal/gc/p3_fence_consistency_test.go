package gc

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestP3FencePublishersPinEachQuorumAndSerial(t *testing.T) {
	source, err := os.ReadFile("store_cassandra.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), "store_cassandra.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []struct {
		function string
		queries  []string
	}{
		{function: "ClaimBlockDelete", queries: []string{"UPDATE blocks SET gc_state"}},
		{function: "StartBlockDeleteOrphan", queries: []string{"INSERT INTO gc_s3_orphans", "UPDATE gc_s3_orphans"}},
	} {
		fn := findGCFunction(file, want.function)
		if fn == nil {
			t.Fatalf("%s not found", want.function)
		}
		for _, queryNeedle := range want.queries {
			if !gcQueryMethodHas(fn, queryNeedle, "Consistency", "EachQuorum") {
				t.Errorf("%s query %q must pin regular commit consistency to gocql.EachQuorum", want.function, queryNeedle)
			}
			if !gcQueryMethodHas(fn, queryNeedle, "SerialConsistency", "Serial") {
				t.Errorf("%s query %q must pin Paxos phase to gocql.Serial", want.function, queryNeedle)
			}
		}
	}
}

func findGCFunction(file *ast.File, name string) *ast.FuncDecl {
	for _, declaration := range file.Decls {
		if function, ok := declaration.(*ast.FuncDecl); ok && function.Name.Name == name {
			return function
		}
	}
	return nil
}

func gcQueryMethodHas(function *ast.FuncDecl, queryNeedle, method, argument string) bool {
	found := false
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || found {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != method || len(call.Args) != 1 {
			return true
		}
		value, ok := call.Args[0].(*ast.SelectorExpr)
		packageName, packageOK := value.X.(*ast.Ident)
		if !ok || value.Sel.Name != argument || !packageOK || packageName.Name != "gocql" {
			return true
		}
		query := gcUnderlyingQuery(selector.X)
		if query == nil || len(query.Args) == 0 {
			return true
		}
		literal, ok := query.Args[0].(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		text, err := strconv.Unquote(literal.Value)
		if err == nil && strings.Contains(text, queryNeedle) {
			found = true
		}
		return true
	})
	return found
}

func gcUnderlyingQuery(expression ast.Expr) *ast.CallExpr {
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return nil
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	if selector.Sel.Name == "Query" {
		return call
	}
	return gcUnderlyingQuery(selector.X)
}
