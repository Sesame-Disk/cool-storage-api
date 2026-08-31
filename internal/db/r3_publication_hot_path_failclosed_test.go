package db

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

type r3ProgramSymbol struct {
	pkg  string
	name string
}

type r3ProgramCallable struct {
	symbol  r3ProgramSymbol
	body    ast.Node
	imports map[string]string
}

type r3ProgramIndex struct {
	callables map[r3ProgramSymbol][]r3ProgramCallable
	aliases   map[r3ProgramSymbol][]r3ProgramSymbol
	variables map[r3ProgramSymbol]bool
	constants map[string]map[string]string
}

type r3ProgramPackage struct {
	importPath string
	directory  string
}

var r3AuthoritySelectPattern = regexp.MustCompile(`(?is)\bselect\b.*\bfrom\s+(?:[a-z0-9_]+\s*\.\s*)?(?:blocks|gc_s3_orphans)\b`)

func r3ProgramImports(file *ast.File) map[string]string {
	imports := make(map[string]string)
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		name := filepath.Base(path)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		if name != "_" && name != "." {
			imports[name] = path
		}
	}
	return imports
}

func r3ProgramAlias(expr ast.Expr, pkg string, imports map[string]string) (r3ProgramSymbol, bool) {
	switch value := expr.(type) {
	case *ast.Ident:
		return r3ProgramSymbol{pkg: pkg, name: value.Name}, true
	case *ast.SelectorExpr:
		prefix, ok := value.X.(*ast.Ident)
		if !ok {
			return r3ProgramSymbol{}, false
		}
		importPath, ok := imports[prefix.Name]
		if !ok {
			return r3ProgramSymbol{}, false
		}
		return r3ProgramSymbol{pkg: importPath, name: value.Sel.Name}, true
	default:
		return r3ProgramSymbol{}, false
	}
}

func r3ProgramString(expr ast.Expr, constants map[string]string) (string, bool) {
	switch value := expr.(type) {
	case *ast.BasicLit:
		if value.Kind != token.STRING {
			return "", false
		}
		decoded, err := strconv.Unquote(value.Value)
		return decoded, err == nil
	case *ast.Ident:
		decoded, ok := constants[value.Name]
		return decoded, ok
	case *ast.ParenExpr:
		return r3ProgramString(value.X, constants)
	case *ast.BinaryExpr:
		if value.Op != token.ADD {
			return "", false
		}
		left, leftOK := r3ProgramString(value.X, constants)
		right, rightOK := r3ProgramString(value.Y, constants)
		return left + right, leftOK && rightOK
	default:
		return "", false
	}
}

func r3ProgramContainsAuthoritySelect(callable r3ProgramCallable, constants map[string]string) bool {
	found := false
	ast.Inspect(callable.body, func(node ast.Node) bool {
		expr, ok := node.(ast.Expr)
		if !ok {
			return true
		}
		query, ok := r3ProgramString(expr, constants)
		if !ok {
			return true
		}
		normalized := strings.ToLower(strings.ReplaceAll(query, `"`, ""))
		if r3AuthoritySelectPattern.MatchString(normalized) {
			found = true
			return false
		}
		return true
	})
	return found
}

func r3BuildProgramIndex(t *testing.T, packages []r3ProgramPackage) *r3ProgramIndex {
	t.Helper()
	index := &r3ProgramIndex{
		callables: make(map[r3ProgramSymbol][]r3ProgramCallable),
		aliases:   make(map[r3ProgramSymbol][]r3ProgramSymbol),
		variables: make(map[r3ProgramSymbol]bool),
		constants: make(map[string]map[string]string),
	}
	fset := token.NewFileSet()
	for _, spec := range packages {
		parsed, err := parser.ParseDir(fset, spec.directory, func(info os.FileInfo) bool {
			return !strings.HasSuffix(info.Name(), "_test.go")
		}, 0)
		if err != nil {
			t.Fatalf("R3 HOT PATH: parse %s: %v", spec.directory, err)
		}
		index.constants[spec.importPath] = make(map[string]string)
		for _, pkg := range parsed {
			for _, file := range pkg.Files {
				imports := r3ProgramImports(file)
				for _, decl := range file.Decls {
					switch value := decl.(type) {
					case *ast.FuncDecl:
						symbol := r3ProgramSymbol{pkg: spec.importPath, name: value.Name.Name}
						index.callables[symbol] = append(index.callables[symbol], r3ProgramCallable{symbol: symbol, body: value.Body, imports: imports})
					case *ast.GenDecl:
						for _, item := range value.Specs {
							values, ok := item.(*ast.ValueSpec)
							if !ok {
								continue
							}
							for position, name := range values.Names {
								symbol := r3ProgramSymbol{pkg: spec.importPath, name: name.Name}
								if value.Tok == token.CONST && position < len(values.Values) {
									if decoded, ok := r3ProgramString(values.Values[position], index.constants[spec.importPath]); ok {
										index.constants[spec.importPath][name.Name] = decoded
									}
									continue
								}
								if value.Tok != token.VAR {
									continue
								}
								index.variables[symbol] = true
								if position >= len(values.Values) {
									continue
								}
								switch initializer := values.Values[position].(type) {
								case *ast.FuncLit:
									index.callables[symbol] = append(index.callables[symbol], r3ProgramCallable{symbol: symbol, body: initializer.Body, imports: imports})
								default:
									if target, ok := r3ProgramAlias(initializer, spec.importPath, imports); ok {
										index.aliases[symbol] = append(index.aliases[symbol], target)
									}
								}
							}
						}
					}
				}
			}
		}
	}
	return index
}

func r3ProgramCallTargets(call *ast.CallExpr, callable r3ProgramCallable) []r3ProgramSymbol {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return []r3ProgramSymbol{{pkg: callable.symbol.pkg, name: fun.Name}}
	case *ast.SelectorExpr:
		prefix, ok := fun.X.(*ast.Ident)
		if ok {
			if importPath, imported := callable.imports[prefix.Name]; imported {
				return []r3ProgramSymbol{{pkg: importPath, name: fun.Sel.Name}}
			}
		}
		return []r3ProgramSymbol{{pkg: callable.symbol.pkg, name: fun.Sel.Name}}
	default:
		return nil
	}
}

// TestR3PublicationHotPathIsFailClosed follows the execution edges that #195
// used: direct calls, cross-package selectors, package-level aliases, and
// function literals stored in seams. Unknown called package variables fail
// closed instead of silently ending the walk.
func TestR3PublicationHotPathIsFailClosed(t *testing.T) {
	root := r3RepositoryRoot(t)
	const module = "github.com/Sesame-Disk/sesamefs"
	packages := []r3ProgramPackage{
		{importPath: module + "/internal/db", directory: filepath.Join(root, "internal", "db")},
		{importPath: module + "/internal/api/v2", directory: filepath.Join(root, "internal", "api", "v2")},
		{importPath: module + "/internal/api", directory: filepath.Join(root, "internal", "api")},
	}
	index := r3BuildProgramIndex(t, packages)
	roots := []r3ProgramSymbol{
		{pkg: module + "/internal/db", name: "AddPublishAttemptReferences"},
		{pkg: module + "/internal/db", name: "StagePublishAttemptReferences"},
		{pkg: module + "/internal/db", name: "PromotePublishAttemptReferences"},
		{pkg: module + "/internal/api/v2", name: "stagePendingPublishedFiles"},
		{pkg: module + "/internal/api/v2", name: "promotePendingPublishedFiles"},
		{pkg: module + "/internal/api", name: "stageSyncCommitBlockDelta"},
		{pkg: module + "/internal/api", name: "finalizeSyncCommitBlockDelta"},
	}

	for _, rootSymbol := range roots {
		t.Run(filepath.Base(rootSymbol.pkg)+"/"+rootSymbol.name, func(t *testing.T) {
			if len(index.callables[rootSymbol]) == 0 {
				t.Fatalf("R3 HOT PATH: publication root %s not found", rootSymbol.name)
			}
			visited := make(map[r3ProgramSymbol]bool)
			var inspectSymbol func(r3ProgramSymbol, []string)
			inspectSymbol = func(symbol r3ProgramSymbol, path []string) {
				if visited[symbol] {
					return
				}
				visited[symbol] = true
				path = append(path, filepath.Base(symbol.pkg)+"."+symbol.name)
				for _, target := range index.aliases[symbol] {
					inspectSymbol(target, path)
				}
				callables := index.callables[symbol]
				if len(callables) == 0 {
					if index.variables[symbol] && len(index.aliases[symbol]) == 0 {
						t.Fatalf("R3 HOT PATH: unresolved called function seam is reachable through %s", strings.Join(path, " -> "))
					}
					return
				}
				for _, callable := range callables {
					if r3ProgramContainsAuthoritySelect(callable, index.constants[symbol.pkg]) {
						t.Fatalf("R3 HOT PATH: submitted canonical/orphan CQL authority read is reachable through %s", strings.Join(path, " -> "))
					}
					ast.Inspect(callable.body, func(node ast.Node) bool {
						call, ok := node.(*ast.CallExpr)
						if !ok {
							return true
						}
						called := r3PublicationCallName(call)
						if r3ForbiddenAuthorityName(called) {
							t.Fatalf("R3 HOT PATH: per-block authority helper %s is reachable through %s", called, strings.Join(path, " -> "))
						}
						for _, target := range r3ProgramCallTargets(call, callable) {
							if len(index.callables[target]) > 0 || len(index.aliases[target]) > 0 || index.variables[target] {
								inspectSymbol(target, path)
							}
						}
						return true
					})
				}
			}
			inspectSymbol(rootSymbol, nil)
		})
	}
}
