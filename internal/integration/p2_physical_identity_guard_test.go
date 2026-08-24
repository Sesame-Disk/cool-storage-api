package integration

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type p2AuthoritySite struct {
	path     string
	symbol   string
	receiver string
	pos      token.Position
}

const p2TargetPackage = "github.com/Sesame-Disk/sesamefs/internal/api/v2"

func p2FreshInstallWriteTarget(expression ast.Expr) (ast.Node, bool) {
	for {
		switch typed := expression.(type) {
		case *ast.Ident:
			return typed, typed.Name == "FreshInstall"
		case *ast.SelectorExpr:
			if typed.Sel.Name == "FreshInstall" {
				return typed, true
			}
			expression = typed.X
		case *ast.ParenExpr:
			expression = typed.X
		case *ast.StarExpr:
			expression = typed.X
		case *ast.UnaryExpr:
			expression = typed.X
		case *ast.IndexExpr:
			expression = typed.X
		case *ast.IndexListExpr:
			expression = typed.X
		default:
			return nil, false
		}
	}
}

func p2LiteralTrue(expression ast.Expr, info *types.Info) bool {
	identifier, ok := expression.(*ast.Ident)
	return ok && info.Uses[identifier] == types.Universe.Lookup("true")
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
			program := p2ParsePackageProgram(t, map[string]map[string]string{
				"example.invalid/synthetic": {"synthetic.go": test.source},
			})
			pkg := program.packages["example.invalid/synthetic"]
			program.check(pkg)
			file := pkg.files[0]

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
			if got := p2LiteralTrue(values[0], pkg.info); got != test.want {
				t.Errorf("p2LiteralTrue() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestP2IndirectFreshAuthorityMutationsAreDetected(t *testing.T) {
	tests := []struct {
		name        string
		source      string
		wantWrite   bool
		wantAddress bool
	}{
		{
			name:        "dereferenced address assignment",
			source:      `package synthetic; func f(target *struct{ FreshInstall bool }) { *(&target.FreshInstall) = true }`,
			wantWrite:   true,
			wantAddress: true,
		},
		{
			name:        "address passed to setter",
			source:      `package synthetic; func setter(*bool); func f(target *struct{ FreshInstall bool }) { setter(&target.FreshInstall) }`,
			wantAddress: true,
		},
		{
			name:   "legitimate read",
			source: `package synthetic; func f(target *struct{ FreshInstall bool }) { if target.FreshInstall {} }`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), "synthetic.go", test.source, 0)
			if err != nil {
				t.Fatalf("parse synthetic source: %v", err)
			}
			var gotWrite, gotAddress bool
			ast.Inspect(file, func(node ast.Node) bool {
				switch typed := node.(type) {
				case *ast.AssignStmt:
					for _, lhs := range typed.Lhs {
						if _, reserved := p2FreshInstallWriteTarget(lhs); reserved {
							gotWrite = true
						}
					}
				case *ast.UnaryExpr:
					if typed.Op == token.AND {
						if _, reserved := p2FreshInstallWriteTarget(typed.X); reserved {
							gotAddress = true
						}
					}
				}
				return true
			})
			if gotWrite != test.wantWrite || gotAddress != test.wantAddress {
				t.Errorf("detected write/address = %v/%v, want %v/%v", gotWrite, gotAddress, test.wantWrite, test.wantAddress)
			}
		})
	}
}

func TestP2PositionalTargetConstructionViaAliasesIsDetected(t *testing.T) {
	program := p2ParsePackageProgram(t, map[string]map[string]string{
		p2TargetPackage: {
			"target.go": `package v2
type BlockMaterializationTarget struct { Store any; StorageClass, StorageKey string; FreshInstall bool }`,
		},
		"example.invalid/bridge": {
			"bridge.go": `package bridge
import v2 "github.com/Sesame-Disk/sesamefs/internal/api/v2"
type Target = v2.BlockMaterializationTarget`,
		},
		"example.invalid/consumer": {
			"aliases.go": `package consumer
import bridge "example.invalid/bridge"
type Targets = []bridge.Target`,
			"consumer.go": `package consumer
func f() {
	_ = Targets{{nil, "", "", true}}
}`,
		},
	})
	consumerPackage := program.packages["example.invalid/consumer"]
	program.check(consumerPackage)
	target := program.targetType(t)
	consumer := consumerPackage.filesByName["consumer.go"]
	var literal *ast.CompositeLit
	ast.Inspect(consumer, func(node ast.Node) bool {
		if candidate, ok := node.(*ast.CompositeLit); ok && candidate.Type == nil {
			literal = candidate
		}
		return true
	})
	if literal == nil {
		t.Fatal("synthetic positional literal was not found")
	}
	if !p2CanonicalTargetLiteral(literal, target, consumerPackage.info) {
		t.Fatal("type-elided positional construction through a cross-file aggregate alias was not resolved to BlockMaterializationTarget")
	}
	if _, keyed := literal.Elts[0].(*ast.KeyValueExpr); keyed {
		t.Fatal("synthetic mutation unexpectedly uses keyed construction")
	}
}

type p2PackageSource struct {
	path        string
	name        string
	fset        *token.FileSet
	files       []*ast.File
	filesByName map[string]*ast.File
	paths       map[*ast.File]string
	info        *types.Info
	types       *types.Package
}

type p2PackageProgram struct {
	packages map[string]*p2PackageSource
	fallback types.Importer
}

func p2ParsePackageProgram(t *testing.T, sources map[string]map[string]string) *p2PackageProgram {
	t.Helper()
	program := &p2PackageProgram{packages: make(map[string]*p2PackageSource), fallback: importer.Default()}
	for packagePath, files := range sources {
		pkg := &p2PackageSource{path: packagePath, fset: token.NewFileSet(), filesByName: make(map[string]*ast.File), paths: make(map[*ast.File]string)}
		for name, source := range files {
			file, err := parser.ParseFile(pkg.fset, name, source, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", name, err)
			}
			pkg.name = file.Name.Name
			pkg.files = append(pkg.files, file)
			pkg.filesByName[name] = file
			pkg.paths[file] = name
		}
		program.packages[packagePath] = pkg
	}
	return program
}

func p2LoadProductionProgram(t *testing.T) *p2PackageProgram {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	program := &p2PackageProgram{packages: make(map[string]*p2PackageSource), fallback: importer.Default()}
	skipDirs := map[string]bool{".git": true, "frontend": true, "mobile-frontend": true, "node_modules": true, "vendor": true}
	scanned := 0
	err = filepath.Walk(root, func(path string, entry os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if skipDirs[entry.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		relative, relErr := filepath.Rel(root, filepath.Dir(path))
		if relErr != nil {
			return relErr
		}
		packagePath := "github.com/Sesame-Disk/sesamefs"
		if relative != "." {
			packagePath += "/" + filepath.ToSlash(relative)
		}
		pkg := program.packages[packagePath]
		if pkg == nil {
			pkg = &p2PackageSource{path: packagePath, fset: token.NewFileSet(), filesByName: make(map[string]*ast.File), paths: make(map[*ast.File]string)}
			program.packages[packagePath] = pkg
		}
		file, parseErr := parser.ParseFile(pkg.fset, path, nil, 0)
		if parseErr != nil {
			t.Errorf("%s: parse: %v", path, parseErr)
			return nil
		}
		if pkg.name != "" && pkg.name != file.Name.Name {
			t.Errorf("%s: package %s shares production directory with package %s", path, file.Name.Name, pkg.name)
		}
		pkg.name = file.Name.Name
		pkg.files = append(pkg.files, file)
		pkg.filesByName[filepath.Base(path)] = file
		pkg.paths[file] = path
		scanned++
		return nil
	})
	if err != nil {
		t.Fatalf("scan production Go sources: %v", err)
	}
	if scanned == 0 {
		t.Fatal("scanned no production Go sources; P2 guard would pass vacuously")
	}
	return program
}

func (program *p2PackageProgram) Import(path string) (*types.Package, error) {
	if source := program.packages[path]; source != nil {
		return program.check(source), nil
	}
	if imported, err := program.fallback.Import(path); err == nil {
		return imported, nil
	}
	// Unresolved third-party declarations are irrelevant to target identity.
	stub := types.NewPackage(path, filepath.Base(path))
	stub.MarkComplete()
	return stub, nil
}

func (program *p2PackageProgram) check(source *p2PackageSource) *types.Package {
	if source.types != nil {
		return source.types
	}
	source.types = types.NewPackage(source.path, source.name)
	source.info = &types.Info{
		Types: make(map[ast.Expr]types.TypeAndValue),
		Defs:  make(map[*ast.Ident]types.Object),
		Uses:  make(map[*ast.Ident]types.Object),
	}
	config := types.Config{Importer: program, Error: func(error) {}}
	_ = types.NewChecker(&config, source.fset, source.types, source.info).Files(source.files)
	return source.types
}

func (program *p2PackageProgram) targetType(t *testing.T) types.Type {
	t.Helper()
	targetPackage := program.packages[p2TargetPackage]
	if targetPackage == nil {
		t.Fatal("P2 target package was not parsed")
	}
	checked := program.check(targetPackage)
	target := checked.Scope().Lookup("BlockMaterializationTarget")
	if target == nil || target.Type() == nil {
		t.Fatal("BlockMaterializationTarget was not type-checked")
	}
	return types.Unalias(target.Type())
}

func p2CanonicalTargetLiteral(literal *ast.CompositeLit, target types.Type, info *types.Info) bool {
	literalType := info.TypeOf(literal)
	return literalType != nil && types.Identical(types.Unalias(literalType), target)
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
	program := p2LoadProductionProgram(t)
	canonicalTargetType := program.targetType(t)

	for _, productionPackage := range program.packages {
		program.check(productionPackage)
		fset := productionPackage.fset
		typeInfo := productionPackage.info
		for _, file := range productionPackage.files {
			path := productionPackage.paths[file]
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
							if len(typed.Names) == len(typed.Values) && p2LiteralTrue(typed.Values[index], typeInfo) {
								freshInstallTrueWrites = append(freshInstallTrueWrites, typed)
							}
						}
					case *ast.AssignStmt:
						for index, lhs := range typed.Lhs {
							if target, reserved := p2FreshInstallWriteTarget(lhs); reserved {
								t.Errorf("%s: %s assigns reserved FreshInstall authority", fset.Position(target.Pos()), symbol)
								if len(typed.Lhs) == len(typed.Rhs) && p2LiteralTrue(typed.Rhs[index], typeInfo) {
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
					case *ast.UnaryExpr:
						if typed.Op == token.AND {
							if target, reserved := p2FreshInstallWriteTarget(typed.X); reserved {
								t.Errorf("%s: %s takes the address of reserved FreshInstall authority", fset.Position(target.Pos()), symbol)
							}
						}
					case *ast.CompositeLit:
						if len(typed.Elts) != 0 && p2CanonicalTargetLiteral(typed, canonicalTargetType, typeInfo) {
							if _, keyed := typed.Elts[0].(*ast.KeyValueExpr); !keyed {
								t.Errorf("%s: %s uses positional BlockMaterializationTarget construction", fset.Position(typed.Pos()), symbol)
							}
						}
						for _, element := range typed.Elts {
							field, keyed := element.(*ast.KeyValueExpr)
							if !keyed || r24NodeText(t, field.Key) != "FreshInstall" {
								continue
							}
							if !p2LiteralTrue(field.Value, typeInfo) {
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
						if !literalOK || !p2CanonicalTargetLiteral(literal, canonicalTargetType, typeInfo) || !nilOK || nilError.Name != "nil" {
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
							if name == "FreshInstall" && p2LiteralTrue(field.Value, typeInfo) {
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
		}
	}

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
