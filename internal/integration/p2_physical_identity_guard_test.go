package integration

import (
	"go/ast"
	"go/token"
	"path/filepath"
	"testing"
)

type p2AuthoritySite struct {
	path     string
	symbol   string
	receiver string
	pos      token.Position
}

// TestP2PhysicalIdentityAuthorityGuard binds physical-locator authority to the
// intended functions and BlockStore variables. Selector references are counted,
// not only direct calls, so a method-value alias cannot evade the guard; a same-
// named method on an unrelated receiver cannot satisfy an expected flow.
func TestP2PhysicalIdentityAuthorityGuard(t *testing.T) {
	type expectedUse struct {
		file     string
		symbol   string
		receiver string
	}
	wantValidations := map[expectedUse]int{
		{file: "upload_reuse.go", symbol: "ResolveNeedsPutBlockStore", receiver: "canonicalStore"}:  1,
		{file: "upload_reuse.go", symbol: "StoreUploadedBlockForProbe", receiver: "canonicalStore"}: 1,
		{file: "canonical_block_reader.go", symbol: "newCanonicalBlockReader", receiver: "store"}:   1,
		{file: "worker.go", symbol: "(*Worker).processBlock", receiver: "resolved"}:                 1,
		{file: "worker.go", symbol: "(*Worker).RecoverS3Orphans", receiver: "blockStore"}:           1,
	}
	validationCounts := map[expectedUse]int{}
	var storageKeyUses, unexpectedMints []p2AuthoritySite
	forbiddenTupleWrappers := map[string]bool{
		"RegisterUploadedBlock":              true,
		"RegisterUploadedBlockAndMapping":    true,
		"RegisterWebUploadedBlockAndMapping": true,
	}
	mintCount := 0
	mintInsideRowlessBranch := false

	r12WalkProductionFiles(t, func(fset *token.FileSet, path string, file *ast.File) {
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok {
				ast.Inspect(declaration, func(node ast.Node) bool {
					selector, selectorOK := node.(*ast.SelectorExpr)
					if !selectorOK {
						return true
					}
					site := p2AuthoritySite{path: path, symbol: "package value", receiver: r24NodeText(t, selector.X), pos: fset.Position(selector.Pos())}
					switch selector.Sel.Name {
					case "StorageKeyForHash":
						storageKeyUses = append(storageKeyUses, site)
					case "MintStorageKey":
						unexpectedMints = append(unexpectedMints, site)
					}
					return true
				})
				continue
			}
			if function.Body == nil {
				continue
			}
			symbol := r12FunctionName(function)
			if forbiddenTupleWrappers[function.Name.Name] {
				t.Errorf("%s: tuple-only production materialization wrapper %s bypasses target authority", fset.Position(function.Pos()), symbol)
			}
			ast.Inspect(function.Body, func(node ast.Node) bool {
				selector, ok := node.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				receiver := r24NodeText(t, selector.X)
				site := p2AuthoritySite{path: path, symbol: symbol, receiver: receiver, pos: fset.Position(selector.Pos())}
				switch selector.Sel.Name {
				case "StorageKeyForHash":
					storageKeyUses = append(storageKeyUses, site)
				case "MintStorageKey":
					if filepath.Base(path) == "upload_reuse.go" && symbol == "ResolveNeedsPutBlockStore" && receiver == "preferredStore" {
						mintCount++
					} else {
						unexpectedMints = append(unexpectedMints, site)
					}
				case "ValidatePhysicalLocator":
					key := expectedUse{file: filepath.Base(path), symbol: symbol, receiver: receiver}
					if _, expected := wantValidations[key]; expected {
						validationCounts[key]++
					}
				}
				return true
			})

			if filepath.Base(path) == "upload_reuse.go" && symbol == "ResolveNeedsPutBlockStore" {
				ast.Inspect(function.Body, func(node ast.Node) bool {
					conditional, ok := node.(*ast.IfStmt)
					if !ok || r24NodeText(t, conditional.Cond) != `canonicalClass == ""` {
						return true
					}
					count := 0
					ast.Inspect(conditional.Body, func(child ast.Node) bool {
						selector, ok := child.(*ast.SelectorExpr)
						if ok && selector.Sel.Name == "MintStorageKey" && r24NodeText(t, selector.X) == "preferredStore" {
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

	for _, site := range storageKeyUses {
		t.Errorf("%s: %s references %s.StorageKeyForHash; physical authority must use ValidatePhysicalLocator", site.pos, site.symbol, site.receiver)
	}
	for _, site := range unexpectedMints {
		t.Errorf("%s: unexpected %s.MintStorageKey reference in %s", site.pos, site.receiver, site.symbol)
	}
	if mintCount != 1 || !mintInsideRowlessBranch {
		t.Errorf("rowless ResolveNeedsPutBlockStore mint count/branch = %d/%v, want 1/true", mintCount, mintInsideRowlessBranch)
	}
	for use, want := range wantValidations {
		if got := validationCounts[use]; got != want {
			t.Errorf("%s %s.%s ValidatePhysicalLocator references = %d, want %d", use.file, use.symbol, use.receiver, got, want)
		}
	}
}
