package v2

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"testing"
)

// TestShareLinkViewCountingCallSites is a regression test for the
// double-count bug on page reloads. It ensures only page-render handlers
// call resolveShareLink(..., true), while auxiliary API handlers use false.
func TestShareLinkViewCountingCallSites(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to resolve current file path")
	}

	pkgDir := filepath.Dir(thisFile)
	target := filepath.Join(pkgDir, "sharelink_view.go")

	fset := token.NewFileSet()
	fileNode, err := parser.ParseFile(fset, target, nil, 0)
	if err != nil {
		t.Fatalf("failed to parse sharelink_view.go: %v", err)
	}

	expected := map[string]bool{
		"ServeShareLinkPage":      true,
		"ServeShareLinkFilePage":  true,
		"ListShareLinkDirents":    false,
		"GetShareLinkRepoTags":    false,
		"GetShareLinkZipTask":     false,
		"GetShareLinkUploadURL":   false,
		"PostShareLinkUploadDone": false,
	}

	seen := make(map[string]bool)

	for _, decl := range fileNode.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}

		expectedFlag, tracked := expected[fn.Name.Name]
		if !tracked {
			continue
		}

		found := false
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}

			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel == nil || sel.Sel.Name != "resolveShareLink" {
				return true
			}

			if len(call.Args) != 2 {
				t.Fatalf("%s must call resolveShareLink with 2 args", fn.Name.Name)
			}

			flagIdent, ok := call.Args[1].(*ast.Ident)
			if !ok || (flagIdent.Name != "true" && flagIdent.Name != "false") {
				t.Fatalf("%s must pass boolean literal as second resolveShareLink arg", fn.Name.Name)
			}

			got := flagIdent.Name == "true"
			if got != expectedFlag {
				t.Fatalf("%s resolveShareLink countView = %v, want %v", fn.Name.Name, got, expectedFlag)
			}

			found = true
			return false
		})

		if !found {
			t.Fatalf("%s does not call resolveShareLink", fn.Name.Name)
		}
		seen[fn.Name.Name] = true
	}

	for name := range expected {
		if !seen[name] {
			t.Fatalf("expected tracked function not found in AST: %s", name)
		}
	}
}
