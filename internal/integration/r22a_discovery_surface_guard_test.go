package integration

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

// TestR22aDiscoveryWriterSurface is the R22a counterpart to
// TestR21OrphanAuthoritySurface, and it lives here rather than beside the
// package-local gate in internal/gc because the surface it protects is
// repo-wide: the discovery projection can be written from any package that
// reaches Cassandra.
//
// R21 pinned the CANONICAL table to a single creator. It deliberately does not
// cover gc_s3_orphans_by_day — its pattern ends in `gc_s3_orphans\b`, and `_` is
// a word character, so `gc_s3_orphans_by_day` never matches. That left the
// discovery projection with no writer gate at all.
//
// R22a is what makes that gap load-bearing. Recovery now reloads the canonical
// row and fails closed when a discovery row has no canonical counterpart, which
// retains the day cursor if that row is encountered. A second writer could
// therefore leave stale discovery state that either holds the cursor while it
// remains in the scan overlap or falls behind the cursor and survives until
// the 90-day TTL. The shape to prevent is exactly the one R21 removed on the
// canonical side: a helper with no production caller, waiting to be wired up.
func TestR22aDiscoveryWriterSurface(t *testing.T) {
	root := filepath.Join("..", "..")
	skipDirs := map[string]bool{
		".git": true, "frontend": true, "mobile-frontend": true,
		"node_modules": true, "vendor": true,
	}
	insertPattern := regexp.MustCompile(`(?i)\bINSERT\s+INTO\s+gc_s3_orphans_by_day\b`)
	deletePattern := regexp.MustCompile(`(?i)\bDELETE\s+FROM\s+gc_s3_orphans_by_day\b`)

	// The canonical store owns both halves. upsertS3OrphanProjection is reached
	// only from ensureS3OrphanProjectionResult and
	// MarkS3OrphanMappingCleanupPending, both of which establish canonical state
	// before publishing the projection; the cross-table sequence is not atomic,
	// so concurrent lifecycle races remain fail-closed in recovery. DeleteS3Orphan
	// removes the canonical row and then its projection.
	allowedInsert := "upsertS3OrphanProjection"
	allowedDelete := "DeleteS3Orphan"
	// Counts, not set membership. Each authorized caller publishes the projection
	// exactly once, after establishing canonical state. A set check plus a total
	// count would accept two publications from one caller and none from the other,
	// which is precisely the case where a lifecycle transition stops publishing.
	allowedProjectionCallsites := map[string]int{
		"(*CassandraStore).ensureS3OrphanProjectionResult":    1,
		"(*CassandraStore).MarkS3OrphanMappingCleanupPending": 1,
	}
	// Direct lexical calls, not transitive ones. Created publishes from
	// StartBlockDeleteOrphan; SameAuthority only publishes after
	// confirmSameAuthorityOrphanResult has acknowledged canonical EACH_QUORUM
	// visibility. Counting three wrappers on StartBlockDeleteOrphan would
	// reject that split and miss an unauthorized helper that published
	// without confirming the canonical row.
	allowedProjectionWrapperCallsites := map[string]int{
		"(*CassandraStore).StartBlockDeleteOrphan":           1,
		"(*CassandraStore).confirmSameAuthorityOrphanResult": 1,
	}

	// Identifiers removed by R22a, kept by name so a revert is caught even if the
	// CQL is reformatted or moved behind a builder.
	forbiddenIdentifiers := map[string]bool{
		"AddUpsertS3OrphanDiscoveryQuery": true,
		"AddDeleteS3OrphanDiscoveryQuery": true,
	}

	scanned := 0
	insertWriters := []string{}
	deleteWriters := []string{}
	projectionCallsites := map[string]int{}
	projectionWrapperCallsites := map[string]int{}
	functionName := func(fn *ast.FuncDecl) string {
		if fn.Recv == nil || len(fn.Recv.List) == 0 {
			return fn.Name.Name
		}
		switch receiver := fn.Recv.List[0].Type.(type) {
		case *ast.StarExpr:
			if ident, ok := receiver.X.(*ast.Ident); ok {
				return "(*" + ident.Name + ")." + fn.Name.Name
			}
		case *ast.Ident:
			return "(" + receiver.Name + ")." + fn.Name.Name
		}
		return fn.Name.Name
	}
	recordCallsites := func(node ast.Node, caller, target string, callsites map[string]int) {
		ast.Inspect(node, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if ok {
				if function, identOK := call.Fun.(*ast.Ident); identOK && function.Name == target {
					callsites[caller]++
				}
			}
			return true
		})
		// Selectors cover method calls and method values. Direct identifier calls
		// were counted above; keeping the passes separate avoids counting a
		// selector call twice.
		ast.Inspect(node, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if ok && selector.Sel.Name == target {
				callsites[caller]++
			}
			return true
		})
	}
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			if skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		scanned++
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
		if err != nil {
			t.Errorf("%s: parse: %v", path, err)
			return nil
		}

		ast.Inspect(file, func(node ast.Node) bool {
			ident, ok := node.(*ast.Ident)
			if ok && forbiddenIdentifiers[ident.Name] {
				t.Errorf("%s: forbidden R22a identifier %q returned to production Go code", path, ident.Name)
			}
			return true
		})

		for _, declaration := range file.Decls {
			fn, ok := declaration.(*ast.FuncDecl)
			if !ok {
				// A discovery write parked in a package-level var or const is
				// still a writer surface, and it has no enclosing function to
				// attribute it to.
				for _, query := range stringLiteralsIn(declaration) {
					if insertPattern.MatchString(query) || deletePattern.MatchString(query) {
						t.Errorf("%s: gc_s3_orphans_by_day write outside any function", path)
					}
				}
				if filepath.Base(path) != "store_mock.go" {
					recordCallsites(declaration, "<package>", allowedInsert, projectionCallsites)
					recordCallsites(declaration, "<package>", "ensureS3OrphanProjectionResult", projectionWrapperCallsites)
				}
				continue
			}
			// store_mock.go mirrors the Cassandra mutation for unit fixtures. It is
			// compiled Go but is not a production caller of the Cassandra helper;
			// do not let the mirror weaken the exact Cassandra callsite contract.
			if filepath.Base(path) != "store_mock.go" {
				recordCallsites(fn.Body, functionName(fn), allowedInsert, projectionCallsites)
				recordCallsites(fn.Body, functionName(fn), "ensureS3OrphanProjectionResult", projectionWrapperCallsites)
			}
			for _, query := range stringLiteralsIn(fn) {
				if insertPattern.MatchString(query) {
					insertWriters = append(insertWriters, fn.Name.Name)
					if fn.Name.Name != allowedInsert {
						t.Errorf("%s: gc_s3_orphans_by_day INSERT is in %s, want %s: a discovery row must never be creatable without its canonical row",
							path, fn.Name.Name, allowedInsert)
					}
				}
				if deletePattern.MatchString(query) {
					deleteWriters = append(deleteWriters, fn.Name.Name)
					if fn.Name.Name != allowedDelete {
						t.Errorf("%s: gc_s3_orphans_by_day DELETE is in %s, want %s: clearing discovery independently of the canonical row is R26 territory, not a helper",
							path, fn.Name.Name, allowedDelete)
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan production Go sources: %v", err)
	}
	if scanned == 0 {
		t.Fatal("scanned no production Go sources; the gate would pass vacuously")
	}
	// Without these the gate also passes vacuously if the queries are ever moved
	// somewhere the walk does not reach.
	if len(insertWriters) != 1 {
		t.Errorf("gc_s3_orphans_by_day INSERT writers = %v, want exactly [%s]", insertWriters, allowedInsert)
	}
	if len(deleteWriters) != 1 {
		t.Errorf("gc_s3_orphans_by_day DELETE writers = %v, want exactly [%s]", deleteWriters, allowedDelete)
	}
	for caller, count := range projectionCallsites {
		want, authorized := allowedProjectionCallsites[caller]
		if !authorized {
			t.Errorf("%s: %s callsite is not an authorized canonical-first lifecycle caller", caller, allowedInsert)
			continue
		}
		if count != want {
			t.Errorf("%s calls %s %d times, want %d: a second publication in one caller can mask a transition that stopped publishing",
				caller, allowedInsert, count, want)
		}
	}
	for caller, want := range allowedProjectionCallsites {
		if _, present := projectionCallsites[caller]; !present {
			t.Errorf("%s no longer calls %s (want %d): its canonical state would exist with no discovery row, invisible to the day scan until the canonical TTL",
				caller, allowedInsert, want)
		}
	}
	for caller, count := range projectionWrapperCallsites {
		want, authorized := allowedProjectionWrapperCallsites[caller]
		if !authorized {
			t.Errorf("%s: ensureS3OrphanProjectionResult callsite is not an authorized canonical-first lifecycle caller", caller)
			continue
		}
		if count != want {
			t.Errorf("%s calls ensureS3OrphanProjectionResult %d times, want %d", caller, count, want)
		}
	}
	for caller, want := range allowedProjectionWrapperCallsites {
		if _, present := projectionWrapperCallsites[caller]; !present {
			t.Errorf("%s no longer calls ensureS3OrphanProjectionResult (want %d): its canonical state would exist with no discovery row", caller, want)
		}
	}
}

// stringLiteralsIn collects every string literal under node so CQL can be
// matched wherever it is written.
func stringLiteralsIn(node ast.Node) []string {
	values := []string{}
	ast.Inspect(node, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(literal.Value)
		if err != nil {
			value = literal.Value
		}
		values = append(values, value)
		return true
	})
	return values
}
