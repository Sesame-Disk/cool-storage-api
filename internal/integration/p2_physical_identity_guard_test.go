package integration

import (
	"go/ast"
	"go/token"
	"path/filepath"
	"testing"
)

// TestP2PhysicalIdentityAuthorityGuard keeps logical-to-physical authority on
// the validator. It reads executable syntax, so comments and string literals
// cannot satisfy or bypass the gate.
func TestP2PhysicalIdentityAuthorityGuard(t *testing.T) {
	type callSite struct {
		path   string
		symbol string
		pos    token.Position
	}
	var storageKeyCalls, mintCalls []callSite
	validationCounts := map[string]int{}
	mintInsideRowlessBranch := false

	r12WalkProductionFiles(t, func(fset *token.FileSet, path string, file *ast.File) {
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok {
				// A package-level function literal is still executable production code.
				// Count authority calls there so moving one out of a FuncDecl cannot
				// make it disappear from this gate.
				ast.Inspect(declaration, func(node ast.Node) bool {
					call, callOK := node.(*ast.CallExpr)
					if !callOK {
						return true
					}
					selector, selectorOK := call.Fun.(*ast.SelectorExpr)
					if !selectorOK {
						return true
					}
					site := callSite{path: path, symbol: "package value", pos: fset.Position(call.Pos())}
					switch selector.Sel.Name {
					case "StorageKeyForHash":
						storageKeyCalls = append(storageKeyCalls, site)
					case "MintStorageKey":
						mintCalls = append(mintCalls, site)
					}
					return true
				})
				continue
			}
			if function.Body == nil {
				continue
			}
			symbol := r12FunctionName(function)
			ast.Inspect(function.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				site := callSite{path: path, symbol: symbol, pos: fset.Position(call.Pos())}
				switch selector.Sel.Name {
				case "StorageKeyForHash":
					storageKeyCalls = append(storageKeyCalls, site)
				case "MintStorageKey":
					mintCalls = append(mintCalls, site)
				case "ValidatePhysicalLocator":
					validationCounts[symbol]++
				}
				return true
			})

			if symbol == "ResolveNeedsPutBlockStore" {
				ast.Inspect(function.Body, func(node ast.Node) bool {
					conditional, ok := node.(*ast.IfStmt)
					if !ok || r24NodeText(t, conditional.Cond) != `canonicalClass == ""` {
						return true
					}
					count := 0
					ast.Inspect(conditional.Body, func(child ast.Node) bool {
						call, ok := child.(*ast.CallExpr)
						if !ok {
							return true
						}
						selector, ok := call.Fun.(*ast.SelectorExpr)
						if ok && selector.Sel.Name == "MintStorageKey" {
							count++
						}
						return true
					})
					mintInsideRowlessBranch = count == 1
					return true
				})
			}
		}
	})

	if len(storageKeyCalls) != 0 {
		for _, site := range storageKeyCalls {
			t.Errorf("%s: %s calls StorageKeyForHash; authority must use ValidatePhysicalLocator", site.pos, site.symbol)
		}
	}
	if len(mintCalls) != 1 || mintCalls[0].symbol != "ResolveNeedsPutBlockStore" || filepath.Base(mintCalls[0].path) != "upload_reuse.go" {
		t.Errorf("MintStorageKey production calls = %#v, want exactly the rowless ResolveNeedsPutBlockStore call", mintCalls)
	}
	if !mintInsideRowlessBranch {
		t.Error("MintStorageKey must remain inside the canonicalClass == empty rowless-install branch")
	}

	wantValidations := map[string]int{
		"(*BlockStore).ValidatePhysicalLocator": 0,
		"ResolveNeedsPutBlockStore":             1,
		"StoreUploadedBlockForProbe":            1,
		"newCanonicalBlockReader":               1,
		"(*Worker).processBlock":                1,
		"(*Worker).RecoverS3Orphans":            1,
	}
	for symbol, want := range wantValidations {
		if got := validationCounts[symbol]; got != want {
			t.Errorf("%s ValidatePhysicalLocator calls = %d, want %d", symbol, got, want)
		}
	}
}
