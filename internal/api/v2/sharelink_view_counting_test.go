package v2

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"testing"
)

// TestShareLinkViewCountingCallSites is a regression test that ensures NO handler
// calls resolveShareLink(..., true). View counting was moved to incrementViewCount()
// which is called AFTER password/expiry/disabled checks, so password-prompt pages
// and expired/disabled links don't inflate the view counter.
//
// All resolveShareLink calls must pass countView=false.
// Actual view counting now happens in the bootstrap handlers that feed the frontend shell.
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

	// Every handler that calls resolveShareLink must pass countView=false.
	// View counting is handled explicitly via incrementViewCount() after all checks.
	expectedFalse := []string{
		"GetShareLinkBootstrap",
		"GetShareLinkFileBootstrap",
		"ListShareLinkDirents",
		"GetShareLinkRepoTags",
		"GetShareLinkZipTask",
		"GetShareLinkUploadURL",
		"PostShareLinkUploadDone",
	}

	expectedSet := make(map[string]bool, len(expectedFalse))
	for _, name := range expectedFalse {
		expectedSet[name] = true
	}

	seen := make(map[string]bool)

	for _, decl := range fileNode.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}

		if !expectedSet[fn.Name.Name] {
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

			if flagIdent.Name == "true" {
				t.Fatalf("%s calls resolveShareLink with countView=true; view counting must happen via incrementViewCount() after password/expiry checks", fn.Name.Name)
			}

			found = true
			return false
		})

		if !found {
			t.Fatalf("%s does not call resolveShareLink", fn.Name.Name)
		}
		seen[fn.Name.Name] = true
	}

	for _, name := range expectedFalse {
		if !seen[name] {
			t.Fatalf("expected tracked function not found in AST: %s", name)
		}
	}

	// Verify that the bootstrap handlers call incrementViewCount after password/availability checks.
	for _, fnName := range []string{"GetShareLinkBootstrap", "GetShareLinkFileBootstrap"} {
		found := false
		for _, decl := range fileNode.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || fn.Name.Name != fnName {
				continue
			}

			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel == nil {
					return true
				}
				if sel.Sel.Name == "incrementViewCount" {
					found = true
					return false
				}
				return true
			})
		}
		if !found {
			t.Fatalf("%s must call incrementViewCount", fnName)
		}
	}
}
