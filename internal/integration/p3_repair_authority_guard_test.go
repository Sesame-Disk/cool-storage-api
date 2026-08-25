package integration

import (
	"go/ast"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// P3 depends on two structural funnels: existing-incarnation PUTs pass through
// PutBlockMaterializationTarget, and existing-incarnation metadata registration
// passes through the non-creating RepairBlockMetadataIfCurrent primitive.
func TestP3RepairAuthorityFunnelsAreExclusive(t *testing.T) {
	program := p2LoadProductionProgram(t)
	// Both the known seam aliases AND the underlying storage operations they wrap.
	// Naming only the aliases would let a future direct blockStore.PutObjectAutoDirect
	// call slip past the authority funnel without tripping this guard.
	directPuts := map[string]bool{
		"putUploadedBlockAutoDirectFn":          true,
		"syncPutBlockAutoDirectFn":              true,
		"putUploadedBlockAutoDirectForUploadFn": true,
		"repairCanonicalBlockDirectFn":          true,
		"PutObjectAutoDirect":                   true,
		"PutObject":                             true,
	}
	// The seam variables ARE the funnel's put functions, so the storage operation
	// is expected inside their own bodies -- and only there.
	putSeams := map[string]bool{
		"putUploadedBlockAutoDirectFn":          true,
		"syncPutBlockAutoDirectFn":              true,
		"putUploadedBlockAutoDirectForUploadFn": true,
		"repairCanonicalBlockDirectFn":          true,
	}
	helperCalls := 0
	repairCalls := 0

	for packagePath, source := range program.packages {
		if !strings.Contains(packagePath, "/internal/api") {
			continue
		}
		for _, file := range source.files {
			for _, declaration := range file.Decls {
				enclosing := p3EnclosingName(declaration)
				ast.Inspect(declaration, func(node ast.Node) bool {
					call, ok := node.(*ast.CallExpr)
					if !ok {
						return true
					}
					name := p3CalledFunctionName(call.Fun)
					if directPuts[name] && !putSeams[enclosing] {
						t.Errorf("%s: direct physical PUT through %s in %s bypasses P3 repair authority", filepath.Base(source.paths[file]), name, enclosing)
					}
					if name == "PutBlockMaterializationTarget" {
						helperCalls++
					}
					if name == "RepairBlockMetadataIfCurrent" {
						repairCalls++
					}
					return true
				})
			}
		}
	}

	if helperCalls != 8 {
		t.Fatalf("P3 physical PUT authority sites = %d, want 8; update the audited funnel inventory", helperCalls)
	}
	if repairCalls != 1 {
		t.Fatalf("P3 production metadata repair sites = %d, want exactly 1", repairCalls)
	}
}

func TestP3MetadataRepairPrimitiveCannotCreate(t *testing.T) {
	program := p2LoadProductionProgram(t)
	source := program.packages["github.com/Sesame-Disk/sesamefs/internal/db"]
	if source == nil {
		t.Fatal("internal/db production package not found")
	}
	found := false
	for _, file := range source.files {
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Name.Name != "RepairBlockMetadataIfCurrent" {
				continue
			}
			found = true
			ast.Inspect(function.Body, func(node ast.Node) bool {
				if literal, ok := node.(*ast.BasicLit); ok && literal.Kind == token.STRING {
					if text, err := strconv.Unquote(literal.Value); err == nil {
						if strings.Contains(strings.ToUpper(text), "INSERT INTO") {
							t.Errorf("RepairBlockMetadataIfCurrent embeds a CQL INSERT; repair must never create a row")
						}
					}
					return true
				}
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				name := strings.ToLower(p3CalledFunctionName(call.Fun))
				if strings.Contains(name, "insert") || name == "installblockmetadata" {
					t.Errorf("RepairBlockMetadataIfCurrent calls create-capable primitive %s", name)
				}
				return true
			})
		}
	}
	if !found {
		t.Fatal("RepairBlockMetadataIfCurrent not found; P3 guard would pass vacuously")
	}
}

func p3CalledFunctionName(expression ast.Expr) string {
	switch function := expression.(type) {
	case *ast.Ident:
		return function.Name
	case *ast.SelectorExpr:
		return function.Sel.Name
	default:
		return ""
	}
}

// TestP3SerialReadsStayOffTheDedupPath keeps global-Paxos reads confined to the
// two places that genuinely need linearizability. Every deduplicated block upload
// crosses ProbeBlockReuse, BlockDeleteFenceActive and RepairBlockMetadataIfCurrent;
// giving any of them a SERIAL read turns an ordinary re-upload into a handful of
// cross-DC round trips. The fence publishers commit at EACH_QUORUM precisely so
// these reads do not have to.
func TestP3SerialReadsStayOffTheDedupPath(t *testing.T) {
	program := p2LoadProductionProgram(t)
	source := program.packages["github.com/Sesame-Disk/sesamefs/internal/db"]
	if source == nil {
		t.Fatal("internal/db production package not found")
	}
	// settleInstalledBlockMetadata is P2's post-install settlement read; apply is
	// the single funnel through which BlockAuthorityStrong reaches a query.
	allowed := map[string]bool{
		"settleInstalledBlockMetadataFn": true,
		"apply":                          true,
	}
	found := map[string]bool{}
	for _, file := range source.files {
		for _, declaration := range file.Decls {
			enclosing := p3EnclosingName(declaration)
			ast.Inspect(declaration, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok || len(call.Args) != 1 {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || selector.Sel.Name != "Consistency" {
					return true
				}
				value, ok := call.Args[0].(*ast.SelectorExpr)
				if !ok || value.Sel.Name != "Serial" {
					return true
				}
				if pkg, ok := value.X.(*ast.Ident); !ok || pkg.Name != "gocql" {
					return true
				}
				found[enclosing] = true
				return true
			})
		}
	}
	if len(found) == 0 {
		t.Fatal("no gocql.Serial reads found in internal/db; the guard would pass vacuously")
	}
	for name := range found {
		if !allowed[name] {
			t.Errorf("%s issues a global SERIAL read; only %v may, and the writer fence relies on EACH_QUORUM publication instead", name, allowedNames(allowed))
		}
	}
}

func p3EnclosingName(declaration ast.Decl) string {
	switch node := declaration.(type) {
	case *ast.FuncDecl:
		return node.Name.Name
	case *ast.GenDecl:
		for _, spec := range node.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok || len(value.Names) == 0 {
				continue
			}
			return value.Names[0].Name
		}
	}
	return "<unknown>"
}

func allowedNames(allowed map[string]bool) []string {
	names := make([]string, 0, len(allowed))
	for name := range allowed {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// TestP3FenceReadsPinTheirOwnConsistency keeps the A+ fence argument independent
// of operator configuration. `database.consistency` accepts ONE, and a ONE read
// can land on a replica that never received an EACH_QUORUM fence commit -- which
// would let a writer see no fence and mint while the previous lifecycle's orphan
// is live. Every gc_s3_orphans read on a writer path must therefore state the
// level its own correctness needs.
func TestP3FenceReadsPinTheirOwnConsistency(t *testing.T) {
	program := p2LoadProductionProgram(t)
	source := program.packages["github.com/Sesame-Disk/sesamefs/internal/db"]
	if source == nil {
		t.Fatal("internal/db production package not found")
	}
	checked := 0
	for _, file := range source.files {
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "Query" || len(call.Args) == 0 {
				return true
			}
			literal, ok := call.Args[0].(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			text, err := strconv.Unquote(literal.Value)
			if err != nil || !strings.Contains(text, "FROM gc_s3_orphans") {
				return true
			}
			checked++
			if !p3QueryPinsConsistency(file, call) {
				t.Errorf("a gc_s3_orphans read does not pin its consistency; inheriting the session level lets ONE miss a published fence:\n%s", strings.TrimSpace(text))
			}
			return true
		})
	}
	if checked == 0 {
		t.Fatal("no gc_s3_orphans reads found in internal/db; the guard would pass vacuously")
	}
}

// p3QueryPinsConsistency reports whether the expression chain enclosing this
// Query call applies an explicit Consistency, either directly or through the
// BlockAuthorityRead.apply funnel.
func p3QueryPinsConsistency(file *ast.File, query *ast.CallExpr) bool {
	pinned := false
	ast.Inspect(file, func(node ast.Node) bool {
		outer, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := outer.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if selector.Sel.Name != "Consistency" && selector.Sel.Name != "apply" {
			return true
		}
		found := false
		ast.Inspect(outer, func(inner ast.Node) bool {
			if inner == query {
				found = true
			}
			return true
		})
		if found {
			pinned = true
		}
		return true
	})
	return pinned
}
