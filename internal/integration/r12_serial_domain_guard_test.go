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

// R12 covers the conditional mutations that can participate in the canonical
// block/orphan lifecycle. The candidate lifecycle is pinned in the same PR as
// adjacent hardening, but is kept distinct in the labels below so the design
// documents do not accidentally claim that candidate ordering closes X1.
var r12ExpectedSerialOperations = map[string]string{
	"upsertBlockMetadataInsertWithRepresentationFn|blocks|INSERT":              "metadata first-writer",
	"claimReleasedBlockStubForRepairFn|blocks|UPDATE":                          "released-stub repair claim",
	"deleteRepairClaimedBlockStubFn|blocks|DELETE":                             "released-stub repair cleanup",
	"deleteClaimedBlockStubFn|blocks|DELETE":                                   "GC stub cleanup",
	"backfillBlockSHA1Fn|blocks|UPDATE":                                        "SHA-1 identity backfill",
	"backfillBlockRepresentationIDFn|blocks|UPDATE":                            "representation identity backfill",
	"(*DB).ReleaseBlockDeleteClaim|blocks|UPDATE":                              "database claim release",
	"(*CassandraStore).ReleaseStaleBlockClaim|blocks|UPDATE":                   "stale claim release",
	"(*CassandraStore).EnsureBlockGCCandidate|gc_block_candidates|INSERT":      "candidate creation",
	"(*CassandraStore).EnsureBlockGCCandidate|gc_block_candidates|UPDATE":      "candidate replacement",
	"(*CassandraStore).StartBlockDeleteOrphan|gc_s3_orphans|INSERT":            "orphan creation",
	"(*CassandraStore).StartBlockDeleteOrphan|gc_s3_orphans|UPDATE":            "orphan lifecycle reset",
	"(*CassandraStore).MarkS3OrphanMappingCleanupPending|gc_s3_orphans|UPDATE": "orphan mapping phase",
	"(*CassandraStore).UpdateS3OrphanAttempt|gc_s3_orphans|UPDATE":             "orphan attempt update",
	"(*CassandraStore).ClaimBlockDelete|blocks|UPDATE":                         "GC claim",
	"(*CassandraStore).ReleaseBlockClaim|blocks|UPDATE":                        "GC claim release",
	"(*CassandraStore).FinalizeBlockDelete|blocks|DELETE":                      "GC finalize",
}

var r12TargetStatementPattern = regexp.MustCompile(`(?is)\b(INSERT\s+INTO|UPDATE|DELETE\s+FROM)\s+(blocks|gc_block_candidates|gc_s3_orphans)\b`)
var r12ConditionalPattern = regexp.MustCompile(`(?i)\bIF\b`)

type r12SerialPin struct {
	present bool
	serial  bool
	local   bool
}

type r12DiscoveredOperation struct {
	key      string
	position token.Position
	terminal bool
	pin      r12SerialPin
}

// TestR12SerialDomainGuard is an untagged source gate. It protects the
// correctness property that cannot be inferred from the configurable session
// default: every known load-bearing conditional mutation uses SERIAL for the
// Paxos phase, even when a deployment's unrelated LWTs use LOCAL_SERIAL.
//
// The gate intentionally checks operation identity as well as discovery. A
// count-only check would allow one protected statement to disappear while a
// different statement keeps the same total. It also checks the terminal CAS
// method and the exact serial argument, so a string-only search cannot be made
// green by a comment or an unrelated query in the same function.
func TestR12SerialDomainGuard(t *testing.T) {
	root := filepath.Join("..", "..")
	skipDirs := map[string]bool{
		".git":            true,
		"frontend":        true,
		"mobile-frontend": true,
		"node_modules":    true,
		"vendor":          true,
	}

	discovered := map[string][]r12DiscoveredOperation{}
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

		source, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, path, source, 0)
		if parseErr != nil {
			t.Errorf("%s: parse: %v", path, parseErr)
			return nil
		}

		for _, declaration := range file.Decls {
			switch typed := declaration.(type) {
			case *ast.FuncDecl:
				r12ScanNode(fset, typed.Body, r12FunctionName(typed), discovered)
			case *ast.GenDecl:
				for _, specification := range typed.Specs {
					valueSpec, ok := specification.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for index, value := range valueSpec.Values {
						symbol := "<package>"
						if index < len(valueSpec.Names) {
							symbol = valueSpec.Names[index].Name
						}
						r12ScanNode(fset, value, symbol, discovered)
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
		t.Fatal("scanned no production Go sources; R12 guard would pass vacuously")
	}

	for key, label := range r12ExpectedSerialOperations {
		operations := discovered[key]
		if len(operations) == 0 {
			t.Errorf("missing R12 operation %s (%s)", key, label)
			continue
		}
		if len(operations) > 1 {
			t.Errorf("R12 operation %s (%s) discovered %d times; operation identity is not unique", key, label, len(operations))
		}
		operation := operations[0]
		if !operation.terminal {
			t.Errorf("R12 operation %s (%s) does not terminate in ScanCAS or MapScanCAS", key, label)
		}
		if !operation.pin.present || !operation.pin.serial {
			t.Errorf("R12 operation %s (%s) must call SerialConsistency(gocql.Serial)", key, label)
		}
		if operation.pin.local {
			t.Errorf("R12 operation %s (%s) must not call SerialConsistency(gocql.LocalSerial)", key, label)
		}
	}

	for key, operations := range discovered {
		if _, expected := r12ExpectedSerialOperations[key]; !expected {
			t.Errorf("unexpected conditional mutation discovered in R12 target set: %s", key)
		}
		for _, operation := range operations {
			if operation.position.Filename == "" {
				t.Errorf("R12 operation %s has no source position", key)
			}
		}
	}
}

func TestR12TargetStatementDiscovery(t *testing.T) {
	tests := []struct {
		name            string
		query           string
		wantTable       string
		wantStatement   string
		wantConditional bool
	}{
		{
			name:            "blocks insert",
			query:           "INSERT INTO blocks (org_id) VALUES (?) IF NOT EXISTS",
			wantTable:       "blocks",
			wantStatement:   "INSERT",
			wantConditional: true,
		},
		{
			name:            "canonical orphan update",
			query:           "UPDATE gc_s3_orphans SET recovery_phase = ? WHERE org_id = ? IF EXISTS",
			wantTable:       "gc_s3_orphans",
			wantStatement:   "UPDATE",
			wantConditional: true,
		},
		{
			name:            "discovery projection excluded",
			query:           "INSERT INTO gc_s3_orphans_by_day (org_id) VALUES (?) IF NOT EXISTS",
			wantConditional: false,
		},
		{
			name:            "comment does not create conditional statement",
			query:           "UPDATE blocks SET gc_state = ? WHERE org_id = ? -- IF EXISTS",
			wantConditional: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			table, statement, ok := r12TargetStatement(test.query)
			if ok != test.wantConditional {
				t.Fatalf("r12TargetStatement() ok = %v, want %v", ok, test.wantConditional)
			}
			if !test.wantConditional {
				return
			}
			if table != test.wantTable || statement != test.wantStatement {
				t.Fatalf("r12TargetStatement() = (%q, %q), want (%q, %q)", table, statement, test.wantTable, test.wantStatement)
			}
		})
	}
}

func TestR12SerialPinDiscovery(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want r12SerialPin
	}{
		{
			name: "global serial",
			expr: `session.Query("UPDATE blocks SET x = ? IF EXISTS").SerialConsistency(gocql.Serial).MapScanCAS(result)`,
			want: r12SerialPin{present: true, serial: true},
		},
		{
			name: "local serial rejected",
			expr: `session.Query("UPDATE blocks SET x = ? IF EXISTS").SerialConsistency(gocql.LocalSerial).MapScanCAS(result)`,
			want: r12SerialPin{present: true, local: true},
		},
		{
			name: "missing pin",
			expr: `session.Query("UPDATE blocks SET x = ? IF EXISTS").MapScanCAS(result)`,
			want: r12SerialPin{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			expression, err := parser.ParseExpr(test.expr)
			if err != nil {
				t.Fatalf("parse expression: %v", err)
			}
			if got := r12FindSerialPin(expression); got != test.want {
				t.Fatalf("r12FindSerialPin() = %+v, want %+v", got, test.want)
			}
		})
	}
}

func r12FunctionName(function *ast.FuncDecl) string {
	if function.Recv == nil || len(function.Recv.List) == 0 {
		return function.Name.Name
	}
	switch receiver := function.Recv.List[0].Type.(type) {
	case *ast.StarExpr:
		if identifier, ok := receiver.X.(*ast.Ident); ok {
			return "(*" + identifier.Name + ")." + function.Name.Name
		}
	case *ast.Ident:
		return "(" + receiver.Name + ")." + function.Name.Name
	}
	return function.Name.Name
}

func r12ScanNode(fset *token.FileSet, node ast.Node, symbol string, discovered map[string][]r12DiscoveredOperation) {
	if node == nil {
		return
	}
	ast.Inspect(node, func(current ast.Node) bool {
		call, ok := current.(*ast.CallExpr)
		if !ok {
			return true
		}

		if selector, ok := call.Fun.(*ast.SelectorExpr); ok && (selector.Sel.Name == "MapScanCAS" || selector.Sel.Name == "ScanCAS") {
			queryCall := r12FindQueryCall(selector.X)
			if queryCall != nil {
				if query, ok := r12QueryLiteral(queryCall); ok {
					if table, statement, ok := r12TargetStatement(query); ok {
						key := symbol + "|" + table + "|" + statement
						discovered[key] = append(discovered[key], r12DiscoveredOperation{
							key:      key,
							position: fset.Position(queryCall.Pos()),
							terminal: true,
							pin:      r12FindSerialPin(selector.X),
						})
					}
				}
			}
		}

		if selector, ok := call.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "Query" {
			if query, ok := r12QueryLiteral(call); ok {
				if table, statement, ok := r12TargetStatement(query); ok {
					key := symbol + "|" + table + "|" + statement
					if operations := discovered[key]; len(operations) == 0 || operations[len(operations)-1].position.Offset != fset.Position(call.Pos()).Offset {
						discovered[key] = append(discovered[key], r12DiscoveredOperation{
							key:      key,
							position: fset.Position(call.Pos()),
						})
					}
				}
			}
		}
		return true
	})
}

func r12FindQueryCall(expression ast.Expr) *ast.CallExpr {
	var queryCall *ast.CallExpr
	ast.Inspect(expression, func(node ast.Node) bool {
		if queryCall != nil {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == "Query" {
			queryCall = call
		}
		return true
	})
	return queryCall
}

func r12QueryLiteral(call *ast.CallExpr) (string, bool) {
	if len(call.Args) == 0 {
		return "", false
	}
	literal, ok := call.Args[0].(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(literal.Value)
	if err != nil {
		return literal.Value, true
	}
	return value, true
}

func r12TargetStatement(query string) (table, statement string, ok bool) {
	query = r12StripCQLComments(query)
	if !r12ConditionalPattern.MatchString(query) {
		return "", "", false
	}
	matches := r12TargetStatementPattern.FindStringSubmatch(query)
	if len(matches) != 3 {
		return "", "", false
	}
	return strings.ToLower(matches[2]), strings.ToUpper(strings.Fields(matches[1])[0]), true
}

func r12FindSerialPin(expression ast.Expr) r12SerialPin {
	var pin r12SerialPin
	ast.Inspect(expression, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "SerialConsistency" {
			return true
		}
		pin.present = true
		if len(call.Args) != 1 {
			return true
		}
		argument, ok := call.Args[0].(*ast.SelectorExpr)
		if !ok {
			return true
		}
		packageName, ok := argument.X.(*ast.Ident)
		if !ok || packageName.Name != "gocql" {
			return true
		}
		switch argument.Sel.Name {
		case "Serial":
			pin.serial = true
		case "LocalSerial":
			pin.local = true
		}
		return true
	})
	return pin
}

// r12StripCQLComments keeps quoted text intact while removing comments before
// the table/IF discovery regex runs. This prevents a future explanatory CQL
// comment from becoming a false positive in the source gate.
func r12StripCQLComments(query string) string {
	var out strings.Builder
	out.Grow(len(query))
	inSingleQuote := false
	inDoubleQuote := false
	for index := 0; index < len(query); {
		char := query[index]
		if inSingleQuote {
			out.WriteByte(char)
			index++
			if char == '\'' {
				if index < len(query) && query[index] == '\'' {
					out.WriteByte(query[index])
					index++
					continue
				}
				inSingleQuote = false
			}
			continue
		}
		if inDoubleQuote {
			out.WriteByte(char)
			index++
			if char == '"' {
				if index < len(query) && query[index] == '"' {
					out.WriteByte(query[index])
					index++
					continue
				}
				inDoubleQuote = false
			}
			continue
		}
		if char == '\'' {
			inSingleQuote = true
			out.WriteByte(char)
			index++
			continue
		}
		if char == '"' {
			inDoubleQuote = true
			out.WriteByte(char)
			index++
			continue
		}
		if char == '-' && index+1 < len(query) && query[index+1] == '-' {
			out.WriteString("  ")
			index += 2
			for index < len(query) && query[index] != '\r' && query[index] != '\n' {
				index++
			}
			continue
		}
		if char == '/' && index+1 < len(query) && query[index+1] == '*' {
			out.WriteString("  ")
			index += 2
			for index < len(query) {
				if query[index] == '*' && index+1 < len(query) && query[index+1] == '/' {
					out.WriteString("  ")
					index += 2
					break
				}
				if query[index] == '\r' || query[index] == '\n' {
					out.WriteByte(query[index])
				} else {
					out.WriteByte(' ')
				}
				index++
			}
			continue
		}
		out.WriteByte(char)
		index++
	}
	return out.String()
}
