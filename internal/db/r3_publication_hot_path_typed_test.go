package db

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

type r3TypedType struct {
	pkg  string
	name string
}

type r3TypedCallable struct {
	symbol   r3ProgramSymbol
	body     ast.Node
	imports  map[string]string
	receiver *r3TypedType
	params   map[string]r3TypedType
	results  []r3TypedType
}

type r3TypedProgram struct {
	functions map[r3ProgramSymbol][]*r3TypedCallable
	aliases   map[r3ProgramSymbol][]r3ProgramSymbol
	variables map[r3ProgramSymbol]bool
	methods   map[r3TypedType]map[string][]*r3TypedCallable
	fields    map[r3TypedType]map[string]r3TypedType
	constants map[string]map[string]string
	packages  map[string]bool
}

func r3TypedTypeOf(expr ast.Expr, pkg string, imports map[string]string) (r3TypedType, bool) {
	switch value := expr.(type) {
	case *ast.StarExpr:
		return r3TypedTypeOf(value.X, pkg, imports)
	case *ast.ParenExpr:
		return r3TypedTypeOf(value.X, pkg, imports)
	case *ast.Ident:
		return r3TypedType{pkg: pkg, name: value.Name}, true
	case *ast.SelectorExpr:
		prefix, ok := value.X.(*ast.Ident)
		if !ok {
			return r3TypedType{}, false
		}
		importPath, ok := imports[prefix.Name]
		if !ok {
			return r3TypedType{}, false
		}
		return r3TypedType{pkg: importPath, name: value.Sel.Name}, true
	default:
		return r3TypedType{}, false
	}
}

func r3TypedFieldTypes(fields *ast.FieldList, pkg string, imports map[string]string) map[string]r3TypedType {
	result := make(map[string]r3TypedType)
	if fields == nil {
		return result
	}
	for _, field := range fields.List {
		typeRef, ok := r3TypedTypeOf(field.Type, pkg, imports)
		if !ok {
			continue
		}
		for _, name := range field.Names {
			result[name.Name] = typeRef
		}
	}
	return result
}

func r3TypedResultTypes(fields *ast.FieldList, pkg string, imports map[string]string) []r3TypedType {
	if fields == nil {
		return nil
	}
	var result []r3TypedType
	for _, field := range fields.List {
		typeRef, ok := r3TypedTypeOf(field.Type, pkg, imports)
		if !ok {
			continue
		}
		count := len(field.Names)
		if count == 0 {
			count = 1
		}
		for range count {
			result = append(result, typeRef)
		}
	}
	return result
}

func r3TypedCallableForFunc(symbol r3ProgramSymbol, fnType *ast.FuncType, body ast.Node, imports map[string]string) *r3TypedCallable {
	return &r3TypedCallable{
		symbol:  symbol,
		body:    body,
		imports: imports,
		params:  r3TypedFieldTypes(fnType.Params, symbol.pkg, imports),
		results: r3TypedResultTypes(fnType.Results, symbol.pkg, imports),
	}
}

func r3BuildTypedProgram(t *testing.T, packages []r3ProgramPackage) *r3TypedProgram {
	t.Helper()
	program := &r3TypedProgram{
		functions: make(map[r3ProgramSymbol][]*r3TypedCallable),
		aliases:   make(map[r3ProgramSymbol][]r3ProgramSymbol),
		variables: make(map[r3ProgramSymbol]bool),
		methods:   make(map[r3TypedType]map[string][]*r3TypedCallable),
		fields:    make(map[r3TypedType]map[string]r3TypedType),
		constants: make(map[string]map[string]string),
		packages:  make(map[string]bool),
	}
	fset := token.NewFileSet()
	for _, spec := range packages {
		program.packages[spec.importPath] = true
		program.constants[spec.importPath] = make(map[string]string)
		parsed, err := parser.ParseDir(fset, spec.directory, func(info os.FileInfo) bool {
			return !strings.HasSuffix(info.Name(), "_test.go")
		}, 0)
		if err != nil {
			t.Fatalf("R3 HOT PATH TYPE: parse %s: %v", spec.directory, err)
		}
		for _, pkg := range parsed {
			for _, file := range pkg.Files {
				imports := r3ProgramImports(file)
				for _, decl := range file.Decls {
					switch value := decl.(type) {
					case *ast.FuncDecl:
						symbol := r3ProgramSymbol{pkg: spec.importPath, name: value.Name.Name}
						callable := r3TypedCallableForFunc(symbol, value.Type, value.Body, imports)
						if value.Recv == nil || len(value.Recv.List) == 0 {
							program.functions[symbol] = append(program.functions[symbol], callable)
							continue
						}
						receiver, ok := r3TypedTypeOf(value.Recv.List[0].Type, spec.importPath, imports)
						if !ok {
							continue
						}
						callable.receiver = &receiver
						for _, name := range value.Recv.List[0].Names {
							callable.params[name.Name] = receiver
						}
						if program.methods[receiver] == nil {
							program.methods[receiver] = make(map[string][]*r3TypedCallable)
						}
						program.methods[receiver][value.Name.Name] = append(program.methods[receiver][value.Name.Name], callable)
					case *ast.GenDecl:
						for _, item := range value.Specs {
							switch typed := item.(type) {
							case *ast.TypeSpec:
								structure, ok := typed.Type.(*ast.StructType)
								if !ok {
									continue
								}
								key := r3TypedType{pkg: spec.importPath, name: typed.Name.Name}
								program.fields[key] = r3TypedFieldTypes(structure.Fields, spec.importPath, imports)
							case *ast.ValueSpec:
								for position, name := range typed.Names {
									symbol := r3ProgramSymbol{pkg: spec.importPath, name: name.Name}
									if value.Tok == token.CONST && position < len(typed.Values) {
										if decoded, ok := r3ProgramString(typed.Values[position], program.constants[spec.importPath]); ok {
											program.constants[spec.importPath][name.Name] = decoded
										}
										continue
									}
									if value.Tok != token.VAR {
										continue
									}
									program.variables[symbol] = true
									if position >= len(typed.Values) {
										continue
									}
									switch initializer := typed.Values[position].(type) {
									case *ast.FuncLit:
										program.functions[symbol] = append(program.functions[symbol], r3TypedCallableForFunc(symbol, initializer.Type, initializer.Body, imports))
									default:
										if target, ok := r3ProgramAlias(initializer, spec.importPath, imports); ok {
											program.aliases[symbol] = append(program.aliases[symbol], target)
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}
	return program
}

func r3TypedLocalBindings(callable *r3TypedCallable, program *r3TypedProgram) (map[string]r3TypedType, map[string][]r3ProgramSymbol, map[string]bool) {
	typesByName := make(map[string]r3TypedType, len(callable.params))
	for name, typeRef := range callable.params {
		typesByName[name] = typeRef
	}
	aliases := make(map[string][]r3ProgramSymbol)
	unknown := make(map[string]bool)
	ast.Inspect(callable.body, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.AssignStmt:
			for position, left := range value.Lhs {
				name, ok := left.(*ast.Ident)
				if !ok || position >= len(value.Rhs) {
					continue
				}
				right := value.Rhs[position]
				if target, ok := r3ProgramAlias(right, callable.symbol.pkg, callable.imports); ok {
					aliases[name.Name] = append(aliases[name.Name], target)
					delete(unknown, name.Name)
					continue
				}
				if r3TypedMethodValueOnIndexedReceiver(right, callable, program, typesByName, aliases) {
					unknown[name.Name] = true
					continue
				}
				if typeRef, ok := r3TypedExprType(right, callable, program, typesByName, aliases); ok {
					typesByName[name.Name] = typeRef
				}

			}
		case *ast.DeclStmt:
			general, ok := value.Decl.(*ast.GenDecl)
			if !ok {
				return true
			}
			for _, item := range general.Specs {
				spec, ok := item.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for position, name := range spec.Names {
					if typeRef, ok := r3TypedTypeOf(spec.Type, callable.symbol.pkg, callable.imports); ok {
						typesByName[name.Name] = typeRef
					}
					if position < len(spec.Values) {
						if target, ok := r3ProgramAlias(spec.Values[position], callable.symbol.pkg, callable.imports); ok {
							aliases[name.Name] = append(aliases[name.Name], target)
							delete(unknown, name.Name)
							continue
						}
						if r3TypedMethodValueOnIndexedReceiver(spec.Values[position], callable, program, typesByName, aliases) {
							unknown[name.Name] = true
						}
					}
				}
			}
		}
		return true
	})
	return typesByName, aliases, unknown
}

func r3TypedMethodValueOnIndexedReceiver(expr ast.Expr, callable *r3TypedCallable, program *r3TypedProgram, locals map[string]r3TypedType, aliases map[string][]r3ProgramSymbol) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	receiver, ok := r3TypedExprType(selector.X, callable, program, locals, aliases)
	if !ok || !program.packages[receiver.pkg] {
		return false
	}
	return len(program.methods[receiver][selector.Sel.Name]) > 0
}

func r3TypedExprType(expr ast.Expr, callable *r3TypedCallable, program *r3TypedProgram, locals map[string]r3TypedType, aliases map[string][]r3ProgramSymbol) (r3TypedType, bool) {
	switch value := expr.(type) {
	case *ast.Ident:
		typeRef, ok := locals[value.Name]
		return typeRef, ok
	case *ast.ParenExpr:
		return r3TypedExprType(value.X, callable, program, locals, aliases)
	case *ast.UnaryExpr:
		return r3TypedExprType(value.X, callable, program, locals, aliases)
	case *ast.SelectorExpr:
		owner, ok := r3TypedExprType(value.X, callable, program, locals, aliases)
		if !ok {
			return r3TypedType{}, false
		}
		fieldType, ok := program.fields[owner][value.Sel.Name]
		return fieldType, ok
	case *ast.CallExpr:
		targets := r3TypedCallTargets(value, callable, program, locals, aliases)
		if len(targets) != 1 || len(targets[0].results) != 1 {
			return r3TypedType{}, false
		}
		return targets[0].results[0], true
	default:
		return r3TypedType{}, false
	}
}

func r3TypedCallTargets(call *ast.CallExpr, callable *r3TypedCallable, program *r3TypedProgram, locals map[string]r3TypedType, localAliases map[string][]r3ProgramSymbol) []*r3TypedCallable {
	var result []*r3TypedCallable
	var appendSymbol func(r3ProgramSymbol)
	appendSymbol = func(symbol r3ProgramSymbol) {
		for _, alias := range program.aliases[symbol] {
			appendSymbol(alias)
		}
		result = append(result, program.functions[symbol]...)
	}
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		if aliases := localAliases[fun.Name]; len(aliases) > 0 {
			for _, alias := range aliases {
				appendSymbol(alias)
			}
			return result
		}
		appendSymbol(r3ProgramSymbol{pkg: callable.symbol.pkg, name: fun.Name})
	case *ast.SelectorExpr:
		if prefix, ok := fun.X.(*ast.Ident); ok {
			if importPath, imported := callable.imports[prefix.Name]; imported {
				appendSymbol(r3ProgramSymbol{pkg: importPath, name: fun.Sel.Name})
				return result
			}
		}
		receiver, ok := r3TypedExprType(fun.X, callable, program, locals, localAliases)
		if !ok {
			return nil
		}
		result = append(result, program.methods[receiver][fun.Sel.Name]...)
	}
	return result
}

func r3TypedCallLabel(call *ast.CallExpr) string {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return fun.Name
	case *ast.SelectorExpr:
		return fun.Sel.Name
	default:
		return ""
	}
}

func r3TypedCQLEntryPoint(call *ast.CallExpr) bool {
	name := r3TypedCallLabel(call)
	return (name == "Query" && len(call.Args) > 0) || name == "Bind"
}

func r3TypedRootBudget(t *testing.T, program *r3TypedProgram, root r3ProgramSymbol) int {
	t.Helper()
	visited := make(map[*r3TypedCallable]bool)
	cqlSites := 0
	var inspectCallable func(*r3TypedCallable, []string)
	inspectCallable = func(callable *r3TypedCallable, path []string) {
		if visited[callable] {
			return
		}
		visited[callable] = true
		path = append(path, filepath.Base(callable.symbol.pkg)+"."+callable.symbol.name)
		locals, localAliases, localUnknown := r3TypedLocalBindings(callable, program)
		if r3ProgramContainsAuthoritySelect(r3ProgramCallable{symbol: callable.symbol, body: callable.body, imports: callable.imports}, program.constants[callable.symbol.pkg]) {
			t.Fatalf("R3 HOT PATH TYPE: canonical/orphan authority CQL is reachable through %s", strings.Join(path, " -> "))
		}
		ast.Inspect(callable.body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			if r3TypedCQLEntryPoint(call) {
				cqlSites++
			}
			called := r3TypedCallLabel(call)
			if r3ForbiddenAuthorityName(called) {
				t.Fatalf("R3 HOT PATH TYPE: authority helper %s is reachable through %s", called, strings.Join(path, " -> "))
			}
			targets := r3TypedCallTargets(call, callable, program, locals, localAliases)
			if len(targets) == 0 {
				if localUnknown[called] {
					t.Fatalf("R3 HOT PATH TYPE: unresolved local function seam %s is reachable through %s", called, strings.Join(path, " -> "))
				}
				if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
					if receiver, typed := r3TypedExprType(selector.X, callable, program, locals, localAliases); typed && program.packages[receiver.pkg] {
						t.Fatalf("R3 HOT PATH TYPE: unresolved method %s on indexed receiver %s.%s through %s", selector.Sel.Name, filepath.Base(receiver.pkg), receiver.name, strings.Join(path, " -> "))
					}
				}
				return true
			}
			for _, target := range targets {
				inspectCallable(target, path)
			}
			return true
		})
	}
	rootCallables := append([]*r3TypedCallable(nil), program.functions[root]...)
	for receiver, methods := range program.methods {
		if receiver.pkg == root.pkg {
			rootCallables = append(rootCallables, methods[root.name]...)
		}
	}
	for _, callable := range rootCallables {
		inspectCallable(callable, nil)
	}
	if len(rootCallables) == 0 {
		t.Fatalf("R3 HOT PATH TYPE: root %s.%s not found", filepath.Base(root.pkg), root.name)
	}
	return cqlSites
}

func TestR3PublicationHotPathTypedReceiversAndCQLBudget(t *testing.T) {
	root := r3RepositoryRoot(t)
	const module = "github.com/Sesame-Disk/sesamefs"
	packages := []r3ProgramPackage{
		{importPath: module + "/internal/db", directory: filepath.Join(root, "internal", "db")},
		{importPath: module + "/internal/api/v2", directory: filepath.Join(root, "internal", "api", "v2")},
		{importPath: module + "/internal/api", directory: filepath.Join(root, "internal", "api")},
	}
	program := r3BuildTypedProgram(t, packages)
	expected := map[r3ProgramSymbol]int{
		{pkg: module + "/internal/db", name: "AddPublishAttemptReferences"}:     2,
		{pkg: module + "/internal/db", name: "StagePublishAttemptReferences"}:   3,
		{pkg: module + "/internal/db", name: "PromotePublishAttemptReferences"}: 1,
		// 17 -> 18 -> 17 (W2 final audit, 2026-09-02/03): stagePendingPublishedFiles's
		// createFileFSObjectRow error path reaches cleanupPendingPublishedFileOwnerAttempt
		// -> publishedBlockReferenceRepairCommitReachableFn ->
		// publishedBlockReferenceRepairHeadCommitFn, which calls
		// FSHelper.getCanonicalHeadCommitSerial. It first went 17->18 when
		// that function was added with 2 dedicated queries (a libraries_by_id
		// org lookup, then a SERIAL read of libraries) in place of the shared
		// GetHeadCommitID/resolveLiveLibraryStateByIDFn chain (already visited
		// via other reachable paths, so it contributed no marginal count).
		// A follow-up audit found the libraries_by_id hop itself was a
		// liability -- a library's hard-delete cascade
		// (library_delete_helpers.go) deletes that row in the same batch as
		// libraries, so resolving it here made a permanently, legitimately
		// hard-deleted library's leftover repair row retry forever instead of
		// converging -- and every caller already has orgID durably
		// (publishedBlockReferenceRepair.OrgID / pendingPublishedFile.cleanupOrgID),
		// so getCanonicalHeadCommitSerial now takes it directly and only
		// issues the single SERIAL query, landing back at 17. Both changes
		// are deliberate: this reachability check drives an IRREVERSIBLE
		// cleanup decision, and a plain LOCAL_QUORUM read of HEAD (or of an
		// org-resolution mapping used only to get to it) can be stale across
		// DCs -- see getCanonicalHeadCommitSerial's doc comment. Pinning the
		// whole shared GetHeadCommitID helper to SERIAL instead would have
		// forced every other caller onto SERIAL too.
		{pkg: module + "/internal/api/v2", name: "stagePendingPublishedFiles"}:   17,
		{pkg: module + "/internal/api/v2", name: "promotePendingPublishedFiles"}: 5,
		{pkg: module + "/internal/api", name: "stageSyncCommitBlockDelta"}:       8,
		{pkg: module + "/internal/api", name: "finalizeSyncCommitBlockDelta"}:    5,
	}
	var labels []string
	for symbol := range expected {
		labels = append(labels, symbol.pkg+"|"+symbol.name)
	}
	sort.Strings(labels)
	for _, label := range labels {
		parts := strings.SplitN(label, "|", 2)
		symbol := r3ProgramSymbol{pkg: parts[0], name: parts[1]}
		got := r3TypedRootBudget(t, program, symbol)
		if got != expected[symbol] {
			t.Errorf("R3 HOT PATH BUDGET: %s.%s submitted CQL callsites = %d, want %d", filepath.Base(symbol.pkg), symbol.name, got, expected[symbol])
		}
	}
}
