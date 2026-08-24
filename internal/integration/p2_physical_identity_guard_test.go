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

func p2BlockMaterializationTargetAliases(specifications []*ast.TypeSpec) map[string]bool {
	aliases := map[string]bool{"BlockMaterializationTarget": true}
	for changed := true; changed; {
		changed = false
		for _, typeSpec := range specifications {
			if !typeSpec.Assign.IsValid() || aliases[typeSpec.Name.Name] || !p2IsBlockMaterializationTarget(typeSpec.Type, aliases) {
				continue
			}
			aliases[typeSpec.Name.Name] = true
			changed = true
		}
	}
	return aliases
}

func p2IsBlockMaterializationTarget(expression ast.Expr, aliases map[string]bool) bool {
	for {
		switch typed := expression.(type) {
		case *ast.Ident:
			return aliases[typed.Name]
		case *ast.SelectorExpr:
			return typed.Sel.Name == "BlockMaterializationTarget"
		case *ast.ParenExpr:
			expression = typed.X
		case *ast.IndexExpr:
			expression = typed.X
		case *ast.IndexListExpr:
			expression = typed.X
		default:
			return false
		}
	}
}

func p2FreshInstallTrue(literal *ast.CompositeLit) bool {
	for _, element := range literal.Elts {
		field, ok := element.(*ast.KeyValueExpr)
		if ok {
			key, keyOK := field.Key.(*ast.Ident)
			value, valueOK := field.Value.(*ast.Ident)
			if keyOK && key.Name == "FreshInstall" && valueOK && value.Name == "true" {
				return true
			}
		}
	}
	return false
}

func p2FreshInstallSelector(expression ast.Expr) (*ast.SelectorExpr, bool) {
	for {
		parenthesized, ok := expression.(*ast.ParenExpr)
		if !ok {
			break
		}
		expression = parenthesized.X
	}
	selector, ok := expression.(*ast.SelectorExpr)
	return selector, ok && selector.Sel.Name == "FreshInstall"
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
	wantMintedValidations := map[expectedUse]int{
		{file: "fs_helpers.go", symbol: "(*FSHelper).RegisterUploadedBlockTarget", receiver: "target.Store"}: 1,
	}
	validationCounts := map[expectedUse]int{}
	mintedValidationCounts := map[expectedUse]int{}
	var storageKeyUses, unexpectedMints []p2AuthoritySite
	forbiddenTupleWrappers := map[string]bool{
		"RegisterUploadedBlock":              true,
		"RegisterUploadedBlockAndMapping":    true,
		"RegisterWebUploadedBlockAndMapping": true,
	}
	mintCount := 0
	rowlessBranchCount := 0
	mintInsideRowlessBranch := false
	freshInstallTrueCount := 0
	freshInstallInsideRowlessBranch := false

	aliasSpecifications := map[string][]*ast.TypeSpec{}
	r12WalkProductionFiles(t, func(_ *token.FileSet, path string, file *ast.File) {
		packageKey := filepath.Dir(path) + "\x00" + file.Name.Name
		for _, declaration := range file.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok || general.Tok != token.TYPE {
				continue
			}
			for _, specification := range general.Specs {
				if typeSpec, ok := specification.(*ast.TypeSpec); ok {
					aliasSpecifications[packageKey] = append(aliasSpecifications[packageKey], typeSpec)
				}
			}
		}
	})
	aliasesByPackage := map[string]map[string]bool{}
	for packageKey, specifications := range aliasSpecifications {
		aliasesByPackage[packageKey] = p2BlockMaterializationTargetAliases(specifications)
	}

	r12WalkProductionFiles(t, func(fset *token.FileSet, path string, file *ast.File) {
		targetAliases := aliasesByPackage[filepath.Dir(path)+"\x00"+file.Name.Name]
		if targetAliases == nil {
			targetAliases = p2BlockMaterializationTargetAliases(nil)
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok {
				ast.Inspect(declaration, func(node ast.Node) bool {
					switch typed := node.(type) {
					case *ast.AssignStmt:
						for _, lhs := range typed.Lhs {
							if selector, selectorOK := p2FreshInstallSelector(lhs); selectorOK {
								t.Errorf("%s: package value mutates FreshInstall", fset.Position(selector.Pos()))
							}
						}
					case *ast.IncDecStmt:
						if selector, selectorOK := p2FreshInstallSelector(typed.X); selectorOK {
							t.Errorf("%s: package value mutates FreshInstall", fset.Position(selector.Pos()))
						}
					case *ast.CompositeLit:
						if p2IsBlockMaterializationTarget(typed.Type, targetAliases) {
							for _, element := range typed.Elts {
								field, keyed := element.(*ast.KeyValueExpr)
								if !keyed {
									t.Errorf("%s: package value uses positional BlockMaterializationTarget construction", fset.Position(element.Pos()))
									continue
								}
								if r24NodeText(t, field.Key) == "FreshInstall" {
									if r24NodeText(t, field.Value) != "true" {
										t.Errorf("%s: package value constructs FreshInstall with non-literal-true authority", fset.Position(field.Pos()))
									} else {
										freshInstallTrueCount++
									}
								}
							}
						}
					}
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
				switch typed := node.(type) {
				case *ast.AssignStmt:
					for _, lhs := range typed.Lhs {
						if selector, selectorOK := p2FreshInstallSelector(lhs); selectorOK {
							t.Errorf("%s: %s mutates FreshInstall; authority must be constructed only with the rowless minted target", fset.Position(selector.Pos()), symbol)
						}
					}
				case *ast.IncDecStmt:
					if selector, selectorOK := p2FreshInstallSelector(typed.X); selectorOK {
						t.Errorf("%s: %s mutates FreshInstall; authority must be constructed only with the rowless minted target", fset.Position(selector.Pos()), symbol)
					}
				case *ast.CompositeLit:
					if p2IsBlockMaterializationTarget(typed.Type, targetAliases) {
						for _, element := range typed.Elts {
							field, keyed := element.(*ast.KeyValueExpr)
							if !keyed {
								t.Errorf("%s: %s uses positional BlockMaterializationTarget construction", fset.Position(element.Pos()), symbol)
								continue
							}
							if r24NodeText(t, field.Key) == "FreshInstall" {
								if r24NodeText(t, field.Value) != "true" {
									t.Errorf("%s: %s constructs FreshInstall with non-literal-true authority", fset.Position(field.Pos()), symbol)
								} else {
									freshInstallTrueCount++
								}
							}
						}
					}
				}
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
				case "ValidateMintedPhysicalLocator":
					key := expectedUse{file: filepath.Base(path), symbol: symbol, receiver: receiver}
					if _, expected := wantMintedValidations[key]; expected {
						mintedValidationCounts[key]++
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
					rowlessBranchCount++
					branchMintCount := 0
					branchFreshReturnCount := 0
					mintedKey := ""
					mintPosition := token.NoPos
					ast.Inspect(conditional.Body, func(child ast.Node) bool {
						assignment, assignmentOK := child.(*ast.AssignStmt)
						if assignmentOK && len(assignment.Rhs) == 1 {
							call, callOK := assignment.Rhs[0].(*ast.CallExpr)
							var selector *ast.SelectorExpr
							if callOK {
								selector, _ = call.Fun.(*ast.SelectorExpr)
							}
							if selector != nil && selector.Sel.Name == "MintStorageKey" && r24NodeText(t, selector.X) == "preferredStore" {
								branchMintCount++
								if len(call.Args) != 1 || r24NodeText(t, call.Args[0]) != "blockID" {
									t.Errorf("%s: rowless mint must derive its key from blockID", fset.Position(call.Pos()))
								}
								if assignment.Tok != token.DEFINE || len(assignment.Lhs) != 2 {
									t.Errorf("%s: rowless mint must define local key and error variables", fset.Position(assignment.Pos()))
								} else if key, keyOK := assignment.Lhs[0].(*ast.Ident); !keyOK || key.Name == "_" {
									t.Errorf("%s: rowless mint result must bind a specific local key variable", fset.Position(assignment.Pos()))
								} else {
									mintedKey = key.Name
									mintPosition = assignment.Pos()
								}
							}
						}
						if assignmentOK && mintedKey != "" && assignment.Pos() > mintPosition {
							for _, lhs := range assignment.Lhs {
								identifier, identifierOK := lhs.(*ast.Ident)
								if identifierOK && identifier.Name == mintedKey {
									t.Errorf("%s: minted key variable %s is reassigned before the fresh target return", fset.Position(identifier.Pos()), mintedKey)
								}
							}
						}
						increment, incrementOK := child.(*ast.IncDecStmt)
						if incrementOK && mintedKey != "" {
							identifier, identifierOK := increment.X.(*ast.Ident)
							if identifierOK && identifier.Name == mintedKey {
								t.Errorf("%s: minted key variable %s is mutated before the fresh target return", fset.Position(identifier.Pos()), mintedKey)
							}
						}
						returned, returnOK := child.(*ast.ReturnStmt)
						if !returnOK {
							return true
						}
						for _, result := range returned.Results {
							literal, literalOK := result.(*ast.CompositeLit)
							if !literalOK || !p2IsBlockMaterializationTarget(literal.Type, targetAliases) || !p2FreshInstallTrue(literal) {
								continue
							}
							branchFreshReturnCount++
							fields := map[string]string{}
							for _, element := range literal.Elts {
								field, keyed := element.(*ast.KeyValueExpr)
								if !keyed {
									continue
								}
								name := r24NodeText(t, field.Key)
								if _, duplicate := fields[name]; duplicate {
									t.Errorf("%s: returned fresh target duplicates %s", fset.Position(field.Pos()), name)
								}
								fields[name] = r24NodeText(t, field.Value)
							}
							if len(fields) != 4 || fields["Store"] != "preferredStore" || fields["StorageClass"] != "preferredClass" || fields["StorageKey"] != mintedKey || fields["FreshInstall"] != "true" {
								t.Errorf("%s: returned fresh target = Store:%s StorageClass:%s StorageKey:%s FreshInstall:%s, want preferredStore/preferredClass/%s/true only", fset.Position(literal.Pos()), fields["Store"], fields["StorageClass"], fields["StorageKey"], fields["FreshInstall"], mintedKey)
							}
							if mintPosition == token.NoPos || mintPosition >= returned.Pos() {
								t.Errorf("%s: returned fresh target is not dataflow-bound to a preceding rowless mint", fset.Position(literal.Pos()))
							}
						}
						return true
					})
					mintedKeyUses := 0
					ast.Inspect(conditional.Body, func(child ast.Node) bool {
						identifier, ok := child.(*ast.Ident)
						if ok && identifier.Name == mintedKey {
							mintedKeyUses++
						}
						return true
					})
					if mintedKey != "" && mintedKeyUses != 2 {
						t.Errorf("%s: minted key variable %s has %d AST uses, want definition plus returned StorageKey only", fset.Position(conditional.Pos()), mintedKey, mintedKeyUses)
					}
					mintInsideRowlessBranch = branchMintCount == 1 && mintedKey != ""
					freshInstallInsideRowlessBranch = branchFreshReturnCount == 1
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
	if mintCount != 1 || rowlessBranchCount != 1 || !mintInsideRowlessBranch {
		t.Errorf("rowless ResolveNeedsPutBlockStore mint count/branch count/binding = %d/%d/%v, want 1/1/true", mintCount, rowlessBranchCount, mintInsideRowlessBranch)
	}
	if freshInstallTrueCount != 1 || !freshInstallInsideRowlessBranch {
		t.Errorf("production FreshInstall:true count/rowless branch = %d/%v, want 1/true", freshInstallTrueCount, freshInstallInsideRowlessBranch)
	}
	for use, want := range wantValidations {
		if got := validationCounts[use]; got != want {
			t.Errorf("%s %s.%s ValidatePhysicalLocator references = %d, want %d", use.file, use.symbol, use.receiver, got, want)
		}
	}
	for use, want := range wantMintedValidations {
		if got := mintedValidationCounts[use]; got != want {
			t.Errorf("%s %s.%s ValidateMintedPhysicalLocator references = %d, want %d", use.file, use.symbol, use.receiver, got, want)
		}
	}
}
