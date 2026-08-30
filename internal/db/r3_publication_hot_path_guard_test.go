package db

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type r3PublicationSurface struct {
	directory string
	roots     []string
}

func r3RepositoryRoot(t *testing.T) string {
	t.Helper()
	if root := strings.TrimSpace(os.Getenv("R3_SOURCE_ROOT")); root != "" {
		return root
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("R3 HOT PATH: resolve repository root: %v", err)
	}
	return root
}

func r3ParseProductionPackage(t *testing.T, directory string) map[string]*ast.FuncDecl {
	t.Helper()
	fset := token.NewFileSet()
	packages, err := parser.ParseDir(fset, directory, func(info os.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("R3 HOT PATH: parse %s: %v", directory, err)
	}
	functions := make(map[string]*ast.FuncDecl)
	for _, pkg := range packages {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if ok {
					functions[fn.Name.Name] = fn
				}
			}
		}
	}
	return functions
}

func r3PublicationCallName(call *ast.CallExpr) string {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return fun.Name
	case *ast.SelectorExpr:
		return fun.Sel.Name
	default:
		return ""
	}
}

func r3ForbiddenAuthorityName(name string) bool {
	for _, forbidden := range []string{
		"ValidateBlockPublishAuthority",
		"ValidateBlockRepairAuthority",
		"ProbeBlockReuse",
		"BlockDeleteFenceActive",
		"GetBlockS3OrphanInfo",
		"BlockHasReferences",
		"BlockHasReferencesGlobal",
	} {
		if strings.Contains(name, forbidden) {
			return true
		}
	}
	return strings.Contains(name, "PublishAuthority")
}

func r3ContainsAuthoritySelect(fn *ast.FuncDecl) bool {
	found := false
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(literal.Value)
		if err != nil {
			value = literal.Value
		}
		normalized := strings.ToLower(strings.Join(strings.Fields(value), " "))
		if strings.Contains(normalized, "select ") &&
			(strings.Contains(normalized, " from blocks") || strings.Contains(normalized, " from gc_s3_orphans")) {
			found = true
			return false
		}
		return true
	})
	return found
}

// TestR3PublicationHotPathHasNoPerBlockAuthorityReads is intentionally a small
// structural cost contract, not a network benchmark. It rejects submitted CQL
// authority reads in the publication/finalize call graph, which is the exact
// O(N) regression introduced by #195. It makes no claim about Cassandra's
// internal Paxos rounds, retries, speculative attempts, or physical RTT count.
func TestR3PublicationHotPathHasNoPerBlockAuthorityReads(t *testing.T) {
	root := r3RepositoryRoot(t)
	surfaces := []r3PublicationSurface{
		{directory: filepath.Join(root, "internal", "db"), roots: []string{"AddPublishAttemptReferences", "StagePublishAttemptReferences", "PromotePublishAttemptReferences"}},
		{directory: filepath.Join(root, "internal", "api", "v2"), roots: []string{"stagePendingPublishedFiles", "promotePendingPublishedFiles"}},
		{directory: filepath.Join(root, "internal", "api"), roots: []string{"stageSyncCommitBlockDelta", "finalizeSyncCommitBlockDelta", "repairPublishedSyncCommitBlockDelta"}},
	}

	for _, surface := range surfaces {
		functions := r3ParseProductionPackage(t, surface.directory)
		for _, rootName := range surface.roots {
			t.Run(filepath.Base(surface.directory)+"/"+rootName, func(t *testing.T) {
				if functions[rootName] == nil {
					t.Fatalf("R3 HOT PATH: publication root %s not found in %s", rootName, surface.directory)
				}
				visited := make(map[string]bool)
				var inspectFunction func(string, []string)
				inspectFunction = func(name string, path []string) {
					if visited[name] {
						return
					}
					visited[name] = true
					fn := functions[name]
					if fn == nil {
						return
					}
					path = append(path, name)
					if r3ContainsAuthoritySelect(fn) {
						t.Fatalf("R3 HOT PATH: submitted canonical/orphan CQL authority read is reachable through %s", strings.Join(path, " -> "))
					}
					ast.Inspect(fn.Body, func(node ast.Node) bool {
						call, ok := node.(*ast.CallExpr)
						if !ok {
							return true
						}
						called := r3PublicationCallName(call)
						if r3ForbiddenAuthorityName(called) {
							t.Fatalf("R3 HOT PATH: per-block authority helper %s is reachable through %s", called, strings.Join(path, " -> "))
						}
						if functions[called] != nil {
							inspectFunction(called, path)
						}
						return true
					})
				}
				inspectFunction(rootName, nil)
			})
		}
	}
}
