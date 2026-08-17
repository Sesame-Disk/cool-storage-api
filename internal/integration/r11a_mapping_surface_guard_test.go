package integration

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestR11aPhysicalGCNeverDeletesBlockIDMappings is the structural half of R11a.
// Physical GC has no authority over the logical SHA-1 -> SHA-256 mapping. The
// gate scans production Go rather than tests so fixtures may still remove their
// own rows during cleanup.
//
// This deliberately scans AST identifiers and string literals, matching the
// scope of the R21/R22 source gates. CQL assembled by a future query builder
// would need an equivalent gate before it could be treated as safe.
func TestR11aPhysicalGCNeverDeletesBlockIDMappings(t *testing.T) {
	root := filepath.Join("..", "..")
	skipDirs := map[string]bool{
		".git": true, "frontend": true, "mobile-frontend": true,
		"node_modules": true, "vendor": true,
	}
	deletePattern := regexp.MustCompile(`(?i)\bDELETE\s+FROM\s+block_id_mappings\b`)
	forbiddenIdentifiers := map[string]bool{
		"cleanupBlockMapping":     true,
		"DeleteBlockMappingExact": true,
	}

	scanned := 0
	identifierHits := []string{}
	deleteStatements := []string{}
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
				identifierHits = append(identifierHits, path+":"+ident.Name)
			}
			return true
		})
		for _, query := range stringLiteralsIn(file) {
			if deletePattern.MatchString(query) {
				deleteStatements = append(deleteStatements, path+": "+strings.Join(strings.Fields(query), " "))
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
	if len(identifierHits) != 0 {
		t.Errorf("R11a-forbidden identifiers returned to production Go: %v", identifierHits)
	}
	if len(deleteStatements) != 0 {
		t.Errorf("production Go must not DELETE FROM block_id_mappings: %v", deleteStatements)
	}
}
