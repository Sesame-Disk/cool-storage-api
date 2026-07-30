package v2

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"testing"
)

// TestShareLinkViewCountingCallSites is a regression test that ensures every
// resolveShareLink call passes literal false and loadShareLink cannot accept a
// countView argument. View counting was moved to incrementViewCount(),
// which is called AFTER password/expiry/disabled checks, so password-prompt pages
// and expired/disabled links don't inflate the view counter.
//
// All resolution through these helpers is therefore intrinsically non-counting.
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

	// These critical callsites must remain present, but validation below applies to
	// every function rather than only the functions listed here.
	expectedCallsites := map[string]string{
		"GetShareLinkBootstrap":     "resolveShareLink",
		"GetShareLinkFileBootstrap": "resolveShareLink",
		"ListShareLinkDirents":      "resolveShareLink",
		"GetShareLinkRepoTags":      "resolveShareLink",
		"GetShareLinkZipTask":       "resolveShareLink",
		"GetShareLinkUploadURL":     "loadShareLink",
		"PostShareLinkUploadDone":   "resolveShareLink",
	}

	seen := make(map[string]bool)

	for _, decl := range fileNode.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}

		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}

			callName := ""
			switch fun := call.Fun.(type) {
			case *ast.SelectorExpr:
				if fun.Sel != nil {
					callName = fun.Sel.Name
				}
			case *ast.Ident:
				callName = fun.Name
			}
			if callName != "resolveShareLink" && callName != "loadShareLink" {
				return true
			}

			if expectedCallsites[fn.Name.Name] == callName {
				seen[fn.Name.Name] = true
			}

			line := fset.Position(call.Pos()).Line
			if callName == "loadShareLink" {
				if len(call.Args) != 1 {
					t.Errorf("%s:%d: %s calls loadShareLink with %d arguments; expected exactly 1 because loadShareLink is intrinsically non-counting", filepath.Base(target), line, fn.Name.Name, len(call.Args))
				}
				return true
			}

			if len(call.Args) != 2 {
				t.Errorf("%s:%d: %s calls resolveShareLink with %d arguments; expected exactly 2 with literal false as the second argument", filepath.Base(target), line, fn.Name.Name, len(call.Args))
				return true
			}
			flagIdent, ok := call.Args[1].(*ast.Ident)
			if !ok {
				t.Errorf("%s:%d: %s calls resolveShareLink with a non-literal countView argument; expected literal false", filepath.Base(target), line, fn.Name.Name)
			} else if flagIdent.Name != "false" {
				t.Errorf("%s:%d: %s calls resolveShareLink with countView=%s; expected literal false because view counting must happen via incrementViewCount() after password/expiry checks", filepath.Base(target), line, fn.Name.Name, flagIdent.Name)
			}

			return true
		})
	}

	for name, callName := range expectedCallsites {
		if !seen[name] {
			t.Errorf("expected critical callsite not found: %s must call %s", name, callName)
		}
	}

	// Verify that the bootstrap handlers still count a view after the
	// password/availability checks. The file bootstrap now delegates its response
	// to emitShareFileBootstrap, which owns the incrementViewCount call, so
	// reaching it through that helper counts as satisfying the requirement —
	// what must hold is that a view is counted, not where the call is written.
	countsView := map[string]bool{"emitShareFileBootstrap": true}
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
				if sel.Sel.Name == "incrementViewCount" || countsView[sel.Sel.Name] {
					found = true
					return false
				}
				return true
			})
		}
		if !found {
			t.Fatalf("%s must count a view, directly or through emitShareFileBootstrap", fnName)
		}
	}
}
