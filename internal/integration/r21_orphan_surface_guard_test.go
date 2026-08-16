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
// creator, bypass the conditional orphan mutations, move the creator away from
// the authorized worker path, or restore either removed authority surface.
func TestR21OrphanAuthoritySurface(t *testing.T) {
	root := filepath.Join("..", "..")
	skipDirs := map[string]bool{
		".git": true, "frontend": true, "mobile-frontend": true,
		"node_modules": true, "vendor": true,
	}
	creatorPattern := regexp.MustCompile(`(?i)\bINSERT\s+INTO\s+gc_s3_orphans\b`)
	updatePattern := regexp.MustCompile(`(?i)\bUPDATE\s+gc_s3_orphans\b`)
	ifPattern := regexp.MustCompile(`(?i)\bIF\b`)
	forbiddenIdentifiers := map[string]bool{
		"RecordS3Orphan":      true,
		"DeleteBlockS3Orphan": true,
	}

	stringLiterals := func(node ast.Node) []string {
		values := []string{}
		ast.Inspect(node, func(n ast.Node) bool {
			literal, ok := n.(*ast.BasicLit)
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
	matchingLiterals := func(node ast.Node, pattern *regexp.Regexp) []string {
		matched := []string{}
		for _, value := range stringLiterals(node) {
			if pattern.MatchString(value) {
				matched = append(matched, value)
			}
		}
		return matched
	}
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

	totalCreators := 0
	creatorFunctions := []string{}
	callsiteFunctions := []string{}
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

		fileCreators := matchingLiterals(file, creatorPattern)
		totalCreators += len(fileCreators)
		for _, query := range matchingLiterals(file, updatePattern) {
			if !ifPattern.MatchString(query) {
				t.Errorf("%s: canonical orphan UPDATE must be conditional with IF", path)
			}
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if len(matchingLiterals(fn, creatorPattern)) > 0 {
				creatorFunctions = append(creatorFunctions, fn.Name.Name)
				if fn.Name.Name != "StartBlockDeleteOrphan" {
					t.Errorf("%s: gc_s3_orphans creator is %s, want StartBlockDeleteOrphan", path, fn.Name.Name)
				}
			}
			if fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if ok && selector.Sel.Name == "StartBlockDeleteOrphan" {
					callsiteFunctions = append(callsiteFunctions, functionName(fn))
				}
				return true
			})
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
	if len(callsiteFunctions) != 1 || callsiteFunctions[0] != "(*Worker).processBlock" {
		t.Fatalf("expected exactly one authorized StartBlockDeleteOrphan callsite in (*Worker).processBlock, got %v", callsiteFunctions)
	}
}
