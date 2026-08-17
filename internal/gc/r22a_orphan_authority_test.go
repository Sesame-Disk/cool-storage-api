package gc

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

// canonicalOrphanRead matches a SELECT against the CANONICAL table and nothing
// else. The trailing `\b` is the entire point, and it is the same boundary R21
// used: `_` is a word character, so `gc_s3_orphans_by_day` never matches. A
// plain strings.Contains("FROM gc_s3_orphans") does match the projection, which
// would let the canonical read regress to the projection — the exact authority
// inversion this gate exists to prevent — while the gate stayed green.
var canonicalOrphanRead = regexp.MustCompile(`(?is)\bSELECT\b.*\bFROM\s+gc_s3_orphans\b`)

// discoveryOrphanTable matches any statement naming the discovery projection.
var discoveryOrphanTable = regexp.MustCompile(`(?i)\bgc_s3_orphans_by_day\b`)

// discoveryOrphanIdentityRead matches the complete identity-only SELECT list.
// Keeping the FROM clause in the expression prevents an added payload or other
// column from hiding behind the accepted identity prefix.
var discoveryOrphanIdentityRead = regexp.MustCompile(`(?is)\bSELECT\s+first_seen_at\s*,\s*org_id\s*,\s*block_id\s+FROM\s+gc_s3_orphans_by_day\b`)

// canonicalPayloadColumns are the fields recovery must take from the canonical
// row. Migration 014 removed these names from the projection, so a discovery read
// must never name one, even if a future schema change reintroduces a column.
var canonicalPayloadColumns = []string{"storage_class", "representation_id", "external_sha1", "recovery_phase"}

// queryReadsCanonical reports whether node contains a `.Query(<canonical CQL>)`
// call, so a consistency level can be attributed to the canonical read rather
// than to any read that happens to sit in the same function.
func queryReadsCanonical(node ast.Node) bool {
	found := false
	ast.Inspect(node, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Query" {
			return true
		}
		for _, argument := range call.Args {
			literal, ok := argument.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				continue
			}
			value, err := strconv.Unquote(literal.Value)
			if err != nil {
				value = literal.Value
			}
			if canonicalOrphanRead.MatchString(value) {
				found = true
			}
		}
		return true
	})
	return found
}

// isGocqlEachQuorum reports whether expression is literally gocql.EachQuorum.
func isGocqlEachQuorum(expression ast.Expr) bool {
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "EachQuorum" {
		return false
	}
	packageName, ok := selector.X.(*ast.Ident)
	return ok && packageName.Name == "gocql"
}

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

	canonicalQueryFound := false
	for _, query := range stringLiteralsIn(canonicalFn) {
		if canonicalOrphanRead.MatchString(query) {
			canonicalQueryFound = true
		}
		if discoveryOrphanTable.MatchString(query) {
			t.Fatalf("GetS3OrphanGlobal reads the discovery projection; the canonical row is the only authority: %s", query)
		}
	}
	if !canonicalQueryFound {
		t.Fatal("GetS3OrphanGlobal does not read the canonical gc_s3_orphans row")
	}

	// Pin EACH_QUORUM to the canonical query's own call chain. Proving the
	// identifier merely appears somewhere in the function would stay green if the
	// level moved to an unrelated query, or if the canonical read lost its
	// .Consistency(...) call while a second read kept one.
	eachQuorumOnCanonicalRead := false
	ast.Inspect(canonicalFn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Consistency" || !queryReadsCanonical(selector.X) {
			return true
		}
		if len(call.Args) != 1 || !isGocqlEachQuorum(call.Args[0]) {
			t.Errorf("canonical read Consistency(...) argument is not gocql.EachQuorum")
			return true
		}
		eachQuorumOnCanonicalRead = true
		return true
	})
	if !eachQuorumOnCanonicalRead {
		t.Fatal("GetS3OrphanGlobal must pin the canonical gc_s3_orphans read itself to .Consistency(gocql.EachQuorum)")
	}

	// Every statement naming the projection is checked, not only ones matching an
	// expected SELECT prefix: a reworded query must not be able to skip the gate.
	discoveryQueryFound := false
	for _, query := range stringLiteralsIn(discoveryFn) {
		if canonicalOrphanRead.MatchString(query) {
			t.Fatalf("ListS3OrphansByDay reads the canonical gc_s3_orphans row; discovery must stay identity-only: %s", query)
		}
		if !discoveryOrphanTable.MatchString(query) {
			continue
		}
		discoveryQueryFound = true
		// Lowercased: unquoted CQL identifiers are case-insensitive, and the table
		// matchers above already are. Inherited from R22a, corrected with R22b.
		normalizedQuery := strings.ToLower(query)
		for _, forbidden := range canonicalPayloadColumns {
			if strings.Contains(normalizedQuery, forbidden) {
				t.Fatalf("discovery query exposes canonical field %q: %s", forbidden, query)
			}
		}
		if !discoveryOrphanIdentityRead.MatchString(query) {
			t.Fatalf("discovery query must select exactly the identity triple: %s", query)
		}
	}
	if !discoveryQueryFound {
		t.Fatal("ListS3OrphansByDay must read gc_s3_orphans_by_day")
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
