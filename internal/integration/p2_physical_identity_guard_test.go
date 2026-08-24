package integration

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type p2AuthoritySite struct {
	path     string
	symbol   string
	receiver string
	pos      token.Position
}

func p2FreshInstallWriteTarget(expression ast.Expr) (ast.Node, bool) {
	for {
		switch typed := expression.(type) {
		case *ast.Ident:
			return typed, typed.Name == "FreshInstall"
		case *ast.SelectorExpr:
			return typed, typed.Sel.Name == "FreshInstall"
		case *ast.ParenExpr:
			expression = typed.X
		default:
			return nil, false
		}
	}
}

func p2LiteralTrue(expression ast.Expr, file *ast.File) bool {
	identifier, ok := expression.(*ast.Ident)
	if !ok || identifier.Name != "true" || identifier.Obj != nil {
		return false
	}
	for _, imported := range file.Imports {
		if imported.Name != nil && imported.Name.Name == "true" {
			return false
		}
	}
	return true
}

func TestP2LiteralTrueRequiresPredeclaredConstant(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   bool
	}{
		{
			name:   "predeclared",
			source: `package synthetic; var _ = struct{ FreshInstall bool }{FreshInstall: true}`,
			want:   true,
		},
		{
			name:   "parameter",
			source: `package synthetic; func f(true bool) { _ = struct{ FreshInstall bool }{FreshInstall: true} }`,
		},
		{
			name:   "local",
			source: `package synthetic; func f() { true := false; _ = struct{ FreshInstall bool }{FreshInstall: true} }`,
		},
		{
			name:   "package declaration",
			source: `package synthetic; var true bool; var _ = struct{ FreshInstall bool }{FreshInstall: true}`,
		},
		{
			name:   "import",
			source: `package synthetic; import true "example.invalid/true"; var _ = struct{ FreshInstall any }{FreshInstall: true}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "synthetic.go", test.source, 0)
			if err != nil {
				t.Fatalf("parse synthetic source: %v", err)
			}

			var values []ast.Expr
			ast.Inspect(file, func(node ast.Node) bool {
				field, ok := node.(*ast.KeyValueExpr)
				if ok && field.Key.(*ast.Ident).Name == "FreshInstall" {
					values = append(values, field.Value)
				}
				return true
			})
			if len(values) != 1 {
				t.Fatalf("FreshInstall values = %d, want 1", len(values))
			}
			if got := p2LiteralTrue(values[0], file); got != test.want {
				t.Errorf("p2LiteralTrue() = %v, want %v", got, test.want)
			}
		})
	}
}

func p2CanonicalTargetType(expression ast.Expr, path string, file *ast.File) bool {
	for {
		switch typed := expression.(type) {
		case *ast.ParenExpr:
			expression = typed.X
		case *ast.IndexExpr:
			expression = typed.X
		case *ast.IndexListExpr:
			expression = typed.X
		default:
			goto resolved
		}
	}

resolved:
	const targetImport = "github.com/Sesame-Disk/sesamefs/internal/api/v2"
	if identifier, ok := expression.(*ast.Ident); ok {
		if strings.HasSuffix(filepath.ToSlash(filepath.Dir(path)), "/internal/api/v2") && identifier.Name == "BlockMaterializationTarget" {
			return true
		}
		for _, imported := range file.Imports {
			importPath, err := strconv.Unquote(imported.Path.Value)
			if err == nil && importPath == targetImport && imported.Name != nil && imported.Name.Name == "." {
				return identifier.Name == "BlockMaterializationTarget"
			}
		}
		return false
	}
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "BlockMaterializationTarget" {
		return false
	}
	qualifier, ok := selector.X.(*ast.Ident)
	if !ok {
		return false
	}
	for _, imported := range file.Imports {
		importPath, err := strconv.Unquote(imported.Path.Value)
		if err != nil || importPath != targetImport {
			continue
		}
		name := "v2"
		if imported.Name != nil {
			name = imported.Name.Name
		}
		return qualifier.Name == name
	}
	return false
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
	var freshInstallTrueWrites []ast.Node
	var returnedFreshInstall *ast.KeyValueExpr

	r12WalkProductionFiles(t, func(fset *token.FileSet, path string, file *ast.File) {
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			symbol := "package value"
			if ok {
				symbol = r12FunctionName(function)
			}
			if ok && forbiddenTupleWrappers[function.Name.Name] {
				t.Errorf("%s: tuple-only production materialization wrapper %s bypasses target authority", fset.Position(function.Pos()), symbol)
			}
			ast.Inspect(declaration, func(node ast.Node) bool {
				if identifier, identifierOK := node.(*ast.Ident); identifierOK && identifier.Name == "true" && identifier.Obj != nil && identifier.Obj.Pos() == identifier.Pos() {
					t.Errorf("%s: %s declares reserved predeclared true identifier", fset.Position(identifier.Pos()), symbol)
				}
				if imported, importedOK := node.(*ast.ImportSpec); importedOK && imported.Name != nil && imported.Name.Name == "true" {
					t.Errorf("%s: %s imports a package as reserved predeclared true identifier", fset.Position(imported.Name.Pos()), symbol)
				}
				switch typed := node.(type) {
				case *ast.ValueSpec:
					for index, name := range typed.Names {
						if name.Name != "FreshInstall" {
							continue
						}
						t.Errorf("%s: %s declares reserved FreshInstall authority", fset.Position(name.Pos()), symbol)
						if len(typed.Names) == len(typed.Values) && p2LiteralTrue(typed.Values[index], file) {
							freshInstallTrueWrites = append(freshInstallTrueWrites, typed)
						}
					}
				case *ast.AssignStmt:
					for index, lhs := range typed.Lhs {
						if target, reserved := p2FreshInstallWriteTarget(lhs); reserved {
							t.Errorf("%s: %s assigns reserved FreshInstall authority", fset.Position(target.Pos()), symbol)
							if len(typed.Lhs) == len(typed.Rhs) && p2LiteralTrue(typed.Rhs[index], file) {
								freshInstallTrueWrites = append(freshInstallTrueWrites, typed)
							}
						}
					}
				case *ast.IncDecStmt:
					if target, reserved := p2FreshInstallWriteTarget(typed.X); reserved {
						t.Errorf("%s: %s mutates reserved FreshInstall authority", fset.Position(target.Pos()), symbol)
					}
				case *ast.RangeStmt:
					for _, targetExpression := range []ast.Expr{typed.Key, typed.Value} {
						if targetExpression != nil {
							if target, reserved := p2FreshInstallWriteTarget(targetExpression); reserved {
								t.Errorf("%s: %s assigns reserved FreshInstall authority in range", fset.Position(target.Pos()), symbol)
							}
						}
					}
				case *ast.CompositeLit:
					if len(typed.Elts) != 0 && p2CanonicalTargetType(typed.Type, path, file) {
						if _, keyed := typed.Elts[0].(*ast.KeyValueExpr); !keyed {
							t.Errorf("%s: %s uses positional BlockMaterializationTarget construction", fset.Position(typed.Pos()), symbol)
						}
					}
					for _, element := range typed.Elts {
						field, keyed := element.(*ast.KeyValueExpr)
						if !keyed || r24NodeText(t, field.Key) != "FreshInstall" {
							continue
						}
						if !p2LiteralTrue(field.Value, file) {
							t.Errorf("%s: %s writes reserved FreshInstall authority with a value other than literal true", fset.Position(field.Pos()), symbol)
							continue
						}
						freshInstallTrueWrites = append(freshInstallTrueWrites, field)
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

			if !ok || function.Body == nil || filepath.Base(path) != "upload_reuse.go" || symbol != "ResolveNeedsPutBlockStore" {
				continue
			}
			for _, statement := range function.Body.List {
				conditional, branchOK := statement.(*ast.IfStmt)
				if !branchOK || r24NodeText(t, conditional.Cond) != `canonicalClass == ""` {
					continue
				}
				rowlessBranchCount++
				branchMintCount := 0
				mintedKey := ""
				var mintAssignment *ast.AssignStmt
				var freshReturn *ast.ReturnStmt
				for _, branchStatement := range conditional.Body.List {
					assignment, assignmentOK := branchStatement.(*ast.AssignStmt)
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
								mintAssignment = assignment
							}
						}
					}
					returned, returnOK := branchStatement.(*ast.ReturnStmt)
					if !returnOK {
						continue
					}
					if len(returned.Results) != 2 {
						continue
					}
					literal, literalOK := returned.Results[0].(*ast.CompositeLit)
					nilError, nilOK := returned.Results[1].(*ast.Ident)
					if !literalOK || !p2CanonicalTargetType(literal.Type, path, file) || !nilOK || nilError.Name != "nil" {
						continue
					}
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
						if name == "FreshInstall" && p2LiteralTrue(field.Value, file) {
							returnedFreshInstall = field
							freshReturn = returned
						}
					}
					if len(fields) != 4 || fields["Store"] != "preferredStore" || fields["StorageClass"] != "preferredClass" || fields["StorageKey"] != mintedKey || fields["FreshInstall"] != "true" {
						t.Errorf("%s: returned fresh target = Store:%s StorageClass:%s StorageKey:%s FreshInstall:%s, want preferredStore/preferredClass/%s/true only", fset.Position(literal.Pos()), fields["Store"], fields["StorageClass"], fields["StorageKey"], fields["FreshInstall"], mintedKey)
					}
					if mintAssignment == nil || mintAssignment.Pos() >= returned.Pos() {
						t.Errorf("%s: returned fresh target is not dataflow-bound to a preceding rowless mint", fset.Position(literal.Pos()))
					}
				}
				mintedKeyWrites := 0
				mintedKeyUses := 0
				ast.Inspect(conditional.Body, func(child ast.Node) bool {
					if assignment, assignmentOK := child.(*ast.AssignStmt); assignmentOK {
						for _, lhs := range assignment.Lhs {
							identifier, identifierOK := lhs.(*ast.Ident)
							if identifierOK && identifier.Name == mintedKey {
								mintedKeyWrites++
							}
						}
					}
					identifier, ok := child.(*ast.Ident)
					if ok && identifier.Name == mintedKey {
						mintedKeyUses++
					}
					return true
				})
				if mintedKey != "" && mintedKeyWrites != 1 {
					t.Errorf("%s: minted key variable %s has %d assignments, want only the preferredStore.MintStorageKey assignment", fset.Position(conditional.Pos()), mintedKey, mintedKeyWrites)
				}
				if mintedKey != "" && mintedKeyUses != 2 {
					t.Errorf("%s: minted key variable %s has %d AST uses, want definition plus returned StorageKey only", fset.Position(conditional.Pos()), mintedKey, mintedKeyUses)
				}
				mintInsideRowlessBranch = branchMintCount == 1 && mintedKey != "" && mintAssignment != nil && freshReturn != nil
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
	if len(freshInstallTrueWrites) != 1 || returnedFreshInstall == nil || (len(freshInstallTrueWrites) == 1 && freshInstallTrueWrites[0] != returnedFreshInstall) {
		t.Errorf("production FreshInstall:true writes/direct rowless return binding = %d/%v, want 1/true", len(freshInstallTrueWrites), returnedFreshInstall != nil && len(freshInstallTrueWrites) == 1 && freshInstallTrueWrites[0] == returnedFreshInstall)
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
