package gc

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

func TestR22aCanonicalOrphanReadAndDiscoverySurface(t *testing.T) {
	path := filepath.Join("store_cassandra.go")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	var canonicalFn, discoveryFn *ast.FuncDecl
	for _, declaration := range file.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok {
			continue
		}
		switch fn.Name.Name {
		case "GetS3OrphanGlobal":
			canonicalFn = fn
		case "ListS3OrphansByDay":
			discoveryFn = fn
		}
	}
	if canonicalFn == nil {
		t.Fatal("GetS3OrphanGlobal not found")
	}
	if discoveryFn == nil {
		t.Fatal("ListS3OrphansByDay not found")
	}

	canonicalQueries := stringLiteralsIn(canonicalFn)
	canonicalQueryFound := false
	for _, query := range canonicalQueries {
		if strings.Contains(query, "FROM gc_s3_orphans") && strings.Contains(query, "SELECT storage_class") {
			canonicalQueryFound = true
			break
		}
	}
	if !canonicalQueryFound {
		t.Fatal("GetS3OrphanGlobal does not read the canonical gc_s3_orphans row")
	}

	eachQuorum := false
	ast.Inspect(canonicalFn.Body, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "EachQuorum" {
			return true
		}
		packageName, ok := selector.X.(*ast.Ident)
		if ok && packageName.Name == "gocql" {
			eachQuorum = true
		}
		return true
	})
	if !eachQuorum {
		t.Fatal("GetS3OrphanGlobal must pin its canonical read to gocql.EachQuorum")
	}

	discoveryQueries := stringLiteralsIn(discoveryFn)
	discoveryQueryFound := false
	for _, query := range discoveryQueries {
		if !strings.Contains(query, "FROM gc_s3_orphans_by_day") || !strings.Contains(query, "SELECT first_seen_at, org_id, block_id") {
			continue
		}
		discoveryQueryFound = true
		for _, forbidden := range []string{"storage_class", "representation_id", "external_sha1", "recovery_phase"} {
			if strings.Contains(query, forbidden) {
				t.Fatalf("discovery query exposes canonical field %q: %s", forbidden, query)
			}
		}
	}
	if !discoveryQueryFound {
		t.Fatal("ListS3OrphansByDay must select only first_seen_at, org_id, and block_id")
	}
}

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
