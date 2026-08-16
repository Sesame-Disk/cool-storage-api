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

// TestR21OrphanAuthoritySurface is an untagged source gate. It runs with the
// normal unit suite so a future refactor cannot quietly add a second orphan
// creator or restore either removed authority surface.
func TestR21OrphanAuthoritySurface(t *testing.T) {
	root := filepath.Join("..", "..")
	skipDirs := map[string]bool{
		".git": true, "frontend": true, "mobile-frontend": true,
		"node_modules": true, "vendor": true,
	}
	creatorPattern := regexp.MustCompile(`(?i)\bINSERT\s+INTO\s+gc_s3_orphans\b`)
	forbiddenIdentifiers := map[string]bool{
		"RecordS3Orphan":      true,
		"DeleteBlockS3Orphan": true,
	}

	countCreators := func(node ast.Node) int {
		count := 0
		ast.Inspect(node, func(n ast.Node) bool {
			literal, ok := n.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(literal.Value)
			if err != nil {
				value = literal.Value
			}
			if creatorPattern.MatchString(value) {
				count++
			}
			return true
		})
		return count
	}

	totalCreators := 0
	creatorFunctions := []string{}
	scanned := 0
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
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, src, 0)
		if err != nil {
			t.Errorf("%s: parse: %v", path, err)
			return nil
		}

		ast.Inspect(file, func(n ast.Node) bool {
			ident, ok := n.(*ast.Ident)
			if ok && forbiddenIdentifiers[ident.Name] {
				t.Errorf("%s: forbidden R21 identifier %q returned to production Go code", path, ident.Name)
			}
			return true
		})

		fileCreators := countCreators(file)
		totalCreators += fileCreators
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			functionCreators := countCreators(fn)
			if functionCreators == 0 {
				continue
			}
			creatorFunctions = append(creatorFunctions, fn.Name.Name)
			if fn.Name.Name != "StartBlockDeleteOrphan" {
				t.Errorf("%s: gc_s3_orphans creator is %s, want StartBlockDeleteOrphan", path, fn.Name.Name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan production Go sources: %v", err)
	}
	if scanned == 0 {
		t.Fatal("scanned no production Go sources")
	}
	if len(creatorFunctions) != 1 || creatorFunctions[0] != "StartBlockDeleteOrphan" {
		t.Fatalf("expected exactly one creator function named StartBlockDeleteOrphan, got %v", creatorFunctions)
	}
	if totalCreators != 1 {
		t.Fatalf("found %d production INSERT INTO gc_s3_orphans statements, want exactly 1; creators=%v", totalCreators, creatorFunctions)
	}
}
