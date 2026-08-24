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

// r12TargetTables is the R12 target set. Discovery resolves a statement's table
// reference to one of these names; every other relation is out of scope.
var r12TargetTables = map[string]bool{
	"blocks":              true,
	"gc_block_candidates": true,
	"gc_s3_orphans":       true,
}

// r12IdentifierPattern matches one component of a CQL table reference: either a
// quoted, case-sensitive identifier or a bare one.
const r12IdentifierPattern = `(?:"(?:[^"]|"")+"|[A-Za-z_][A-Za-z0-9_]*)`

// r12StatementPattern recognises the mutating statement forms that can carry an
// IF clause. It matches the table reference structurally rather than by literal
// name, because CQL spells the same relation several ways -- blocks, "blocks",
// sesamefs.blocks, "sesamefs"."blocks" -- and a name-literal regex would read the
// qualified and quoted spellings as out of scope. That is a false green rather
// than a missing pin: the statement leaves discovery entirely, so no pin is ever
// demanded of it. The pattern also accepts DELETE <columns> FROM <table>, not
// only row deletes.
var r12StatementPattern = regexp.MustCompile(
	`(?is)\b(INSERT\s+INTO|UPDATE|DELETE(?:\s+[\w"',\[\]\s.]+?)?\s+FROM)\s+(` +
		r12IdentifierPattern + `)(?:\s*\.\s*(` + r12IdentifierPattern + `))?`)

var r12ConditionalPattern = regexp.MustCompile(`(?i)\bIF\b`)

// r12QueryCASTerminals are the gocql Query methods that execute a lightweight
// transaction. All four matter: the driver in use
// (github.com/apache/cassandra-gocql-driver/v2) exposes the Context variants as
// equal-standing LWT execution, so a guard that knew only ScanCAS/MapScanCAS
// would not merely miss a pin -- an identical statement written with
// MapScanCASContext would leave discovery altogether and read as green.
var r12QueryCASTerminals = map[string]bool{
	"ScanCAS":           true,
	"ScanCASContext":    true,
	"MapScanCAS":        true,
	"MapScanCASContext": true,
}

// r12BatchCASTerminals are the gocql Batch methods that execute a conditional
// batch. R12 refuses them outright rather than inspecting them: a batch collects
// its statements through separate Batch.Query calls, so the CQL cannot be
// attributed to the CAS call site the way a Query chain can, and the serial pin
// lives on the batch rather than on any one statement. Allowing one would reopen
// the blind spot the fail-closed rule on non-literal CQL exists to close.
// SesameFS uses no conditional batch today; introducing one against any relation
// has to extend R12 deliberately, by teaching this guard how to classify it.
var r12BatchCASTerminals = map[string]bool{
	"ExecCAS":           true,
	"ExecCASContext":    true,
	"MapExecCAS":        true,
	"MapExecCASContext": true,
}

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

// r12UnresolvedAllowance records a conditional-CAS call site whose CQL cannot be
// resolved to a source literal, together with why it is out of the R12 target
// set. The guard is fail-closed on unresolvable CAS: without this allowlist a
// statement that moved its CQL into a const, a variable or fmt.Sprintf would
// stop being discovered instead of failing the gate. Every entry must name a
// call site that provably cannot address blocks, gc_block_candidates or
// gc_s3_orphans.
type r12UnresolvedAllowance struct {
	count  int
	reason string
}

var r12AllowedUnresolvedCAS = map[string]r12UnresolvedAllowance{
	"acquireHardDeleteLock": {count: 2, reason: "hard-delete lock table is a parameter; TestR12HardDeleteLockTablesAreOutOfScope pins every call site to a gc_*_hard_delete_locks literal"},
	"renewHardDeleteLock":   {count: 1, reason: "same helper family as acquireHardDeleteLock"},
	"releaseHardDeleteLock": {count: 1, reason: "same helper family as acquireHardDeleteLock"},
}

// r12HardDeleteLockHelpers are the only CAS helpers allowed to build their CQL
// with fmt.Sprintf. They interpolate a table name, so the allowlist above would
// be blind on its own: it says "not an R12 table" without proving it. The value
// is the zero-based index of each helper's tableName parameter.
var r12HardDeleteLockHelpers = map[string]int{
	"acquireHardDeleteLock": 1,
	"renewHardDeleteLock":   1,
	"releaseHardDeleteLock": 1,
}

var r12AllowedHardDeleteLockTables = map[string]bool{
	"gc_library_hard_delete_locks": true,
	"gc_user_hard_delete_locks":    true,
	"gc_org_hard_delete_locks":     true,
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
//
// Three escape routes are closed explicitly, because each one removes a
// statement from discovery instead of reporting it unpinned: CQL that is not a
// source literal, a table reference spelled with quotes or a keyspace
// qualifier, and a CAS executed through the Context or batch terminals.
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
	unresolved := map[string][]token.Position{}
	batchCAS := map[string][]token.Position{}
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
				r12ScanNode(fset, typed.Body, r12FunctionName(typed), discovered, unresolved, batchCAS)
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
						r12ScanNode(fset, value, symbol, discovered, unresolved, batchCAS)
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
			t.Errorf("R12 operation %s (%s) does not terminate in a Query CAS method (ScanCAS/MapScanCAS, with or without Context)", key, label)
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

	// Fail closed on CAS whose CQL is not statically resolvable. Discovery keys
	// off a source literal, so a target statement that moved its CQL into a
	// const, a variable or fmt.Sprintf would silently leave the target set. Each
	// allowed symbol is pinned to an exact count so a new unresolvable CAS in an
	// already-allowed function still fails.
	for symbol, positions := range unresolved {
		allowance, allowed := r12AllowedUnresolvedCAS[symbol]
		if !allowed {
			t.Errorf(
				"conditional CAS with non-literal CQL at %s (%s): R12 discovery cannot prove it does not target blocks, gc_block_candidates or gc_s3_orphans; keep the CQL inline or add an explicit allowlist entry",
				symbol,
				r12FormatPositions(positions),
			)
			continue
		}
		if len(positions) != allowance.count {
			t.Errorf(
				"allowlisted unresolvable CAS %s has %d call sites, want %d (%s) at %s",
				symbol,
				len(positions),
				allowance.count,
				allowance.reason,
				r12FormatPositions(positions),
			)
		}
	}

	for symbol, allowance := range r12AllowedUnresolvedCAS {
		if len(unresolved[symbol]) == 0 {
			t.Errorf("allowlisted unresolvable CAS %s (%s) no longer exists; drop the stale allowlist entry", symbol, allowance.reason)
		}
	}

	// Conditional batches are refused rather than classified. There is no
	// allowlist here on purpose: unlike a Query chain, a batch does not carry its
	// CQL or its serial pin at the CAS call site, so an allowance could not be
	// justified the way the hard-delete lock helpers are. Wanting one means
	// extending R12 to model batches, not adding a name to a map.
	for symbol, positions := range batchCAS {
		t.Errorf(
			"conditional batch CAS in %s at %s: R12 forbids conditional batches, because a batch's statements and its serial pin cannot be attributed to this call site; extend R12 to classify batches before introducing one",
			symbol,
			r12FormatPositions(positions),
		)
	}
}

func r12FormatPositions(positions []token.Position) string {
	rendered := make([]string, 0, len(positions))
	for _, position := range positions {
		rendered = append(rendered, position.String())
	}
	return strings.Join(rendered, ", ")
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
		{
			name:            "string value does not create conditional statement",
			query:           "UPDATE blocks SET last_error = 'IF' WHERE org_id = ? AND block_id = ?",
			wantConditional: false,
		},
		{
			name:            "escaped quote inside a value does not leak a conditional",
			query:           "UPDATE blocks SET last_error = 'it''s IF' WHERE org_id = ?",
			wantConditional: false,
		},
		{
			name:            "a real IF clause still parses when a value contains IF",
			query:           "UPDATE blocks SET last_error = 'IF' WHERE org_id = ? IF gc_state = ?",
			wantTable:       "blocks",
			wantStatement:   "UPDATE",
			wantConditional: true,
		},
		{
			name:            "quoted table identifier stays in scope",
			query:           `UPDATE "blocks" SET gc_state = ? WHERE org_id = ? IF gc_state = ?`,
			wantTable:       "blocks",
			wantStatement:   "UPDATE",
			wantConditional: true,
		},
		{
			name:            "keyspace-qualified table stays in scope",
			query:           "UPDATE sesamefs.blocks SET gc_state = ? WHERE org_id = ? IF gc_state = ?",
			wantTable:       "blocks",
			wantStatement:   "UPDATE",
			wantConditional: true,
		},
		{
			name:            "fully quoted qualified table stays in scope",
			query:           `UPDATE "sesamefs"."blocks" SET gc_state = ? WHERE org_id = ? IF gc_state = ?`,
			wantTable:       "blocks",
			wantStatement:   "UPDATE",
			wantConditional: true,
		},
		{
			name:            "qualified insert stays in scope",
			query:           "INSERT INTO sesamefs.gc_s3_orphans (org_id) VALUES (?) IF NOT EXISTS",
			wantTable:       "gc_s3_orphans",
			wantStatement:   "INSERT",
			wantConditional: true,
		},
		{
			name:            "column delete keeps the table in scope",
			query:           "DELETE storage_key FROM blocks WHERE org_id = ? AND block_id = ? IF gc_state = ?",
			wantTable:       "blocks",
			wantStatement:   "DELETE",
			wantConditional: true,
		},
		{
			name:            "qualified column delete stays in scope",
			query:           `DELETE storage_key FROM "sesamefs".gc_block_candidates WHERE org_id = ? IF gc_state = ?`,
			wantTable:       "gc_block_candidates",
			wantStatement:   "DELETE",
			wantConditional: true,
		},
		{
			name:            "qualified projection is still excluded",
			query:           "INSERT INTO sesamefs.gc_s3_orphans_by_day (org_id) VALUES (?) IF NOT EXISTS",
			wantConditional: false,
		},
		{
			name:            "quoted identifiers keep CQL case sensitivity",
			query:           `UPDATE "BLOCKS" SET gc_state = ? WHERE org_id = ? IF gc_state = ?`,
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

func r12ScanNode(fset *token.FileSet, node ast.Node, symbol string, discovered map[string][]r12DiscoveredOperation, unresolved map[string][]token.Position, batchCAS map[string][]token.Position) {
	if node == nil {
		return
	}
	ast.Inspect(node, func(current ast.Node) bool {
		call, ok := current.(*ast.CallExpr)
		if !ok {
			return true
		}

		if selector, ok := call.Fun.(*ast.SelectorExpr); ok && r12BatchCASTerminals[selector.Sel.Name] {
			batchCAS[symbol] = append(batchCAS[symbol], fset.Position(call.Pos()))
			return true
		}

		if selector, ok := call.Fun.(*ast.SelectorExpr); ok && r12QueryCASTerminals[selector.Sel.Name] {
			queryCall := r12FindQueryCall(selector.X)
			switch {
			case queryCall == nil:
				unresolved[symbol] = append(unresolved[symbol], fset.Position(call.Pos()))
			default:
				query, literal := r12QueryLiteral(queryCall)
				if !literal {
					// Fail closed: the CQL is not a source literal, so table
					// discovery cannot rule out an R12 target.
					unresolved[symbol] = append(unresolved[symbol], fset.Position(queryCall.Pos()))
					break
				}
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
	query = r12StripCQLStringLiterals(r12StripCQLComments(query))
	if !r12ConditionalPattern.MatchString(query) {
		return "", "", false
	}
	// Every mutating statement in the query is examined, not just the first, so
	// a target table cannot be hidden behind a leading out-of-scope one.
	for _, matches := range r12StatementPattern.FindAllStringSubmatch(query, -1) {
		name := r12NormalizeCQLIdentifier(matches[2])
		if matches[3] != "" {
			// The reference was keyspace-qualified; the table is the second
			// component.
			name = r12NormalizeCQLIdentifier(matches[3])
		}
		if !r12TargetTables[name] {
			continue
		}
		return name, strings.ToUpper(strings.Fields(matches[1])[0]), true
	}
	return "", "", false
}

// r12NormalizeCQLIdentifier applies CQL identifier folding: a bare identifier is
// case-insensitive and folds to lower case, while a quoted one keeps its case and
// loses one level of quoting. The distinction is deliberate rather than
// incidental strictness -- "BLOCKS" names a different relation than blocks, so
// folding it into the target set would be a false positive on a table R12 does
// not govern.
func r12NormalizeCQLIdentifier(identifier string) string {
	identifier = strings.TrimSpace(identifier)
	if len(identifier) >= 2 && strings.HasPrefix(identifier, `"`) && strings.HasSuffix(identifier, `"`) {
		return strings.ReplaceAll(identifier[1:len(identifier)-1], `""`, `"`)
	}
	return strings.ToLower(identifier)
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

// r12StripCQLStringLiterals blanks the contents of single-quoted CQL strings so
// a value that merely contains the word IF cannot be read as a conditional
// clause. Double quotes are left alone: in CQL they delimit case-sensitive
// identifiers, so blanking them could hide a target table name instead of a
// false positive.
func r12StripCQLStringLiterals(query string) string {
	var out strings.Builder
	out.Grow(len(query))
	inString := false
	for index := 0; index < len(query); index++ {
		char := query[index]
		if !inString {
			out.WriteByte(char)
			if char == '\'' {
				inString = true
			}
			continue
		}
		if char == '\'' {
			// A doubled quote is an escaped quote, still inside the string.
			if index+1 < len(query) && query[index+1] == '\'' {
				out.WriteString("  ")
				index++
				continue
			}
			out.WriteByte(char)
			inString = false
			continue
		}
		if char == '\r' || char == '\n' {
			out.WriteByte(char)
			continue
		}
		out.WriteByte(' ')
	}
	return out.String()
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

// TestR12HardDeleteLockTablesAreOutOfScope closes the hole the unresolvable-CAS
// allowlist would otherwise leave open. The three lock helpers take the table
// name as a parameter, so allowlisting them by symbol alone would also allow
// acquireHardDeleteLock(session, "blocks", ...) to run an unpinned LWT against
// an R12 partition without the guard ever seeing it. This pins every call site
// to a literal outside the R12 target set.
func TestR12HardDeleteLockTablesAreOutOfScope(t *testing.T) {
	root := filepath.Join("..", "..")
	skipDirs := map[string]bool{
		".git":            true,
		"frontend":        true,
		"mobile-frontend": true,
		"node_modules":    true,
		"vendor":          true,
	}

	callSites := 0
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

		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			t.Errorf("%s: parse: %v", path, parseErr)
			return nil
		}

		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			identifier, ok := call.Fun.(*ast.Ident)
			if !ok {
				return true
			}
			argumentIndex, tracked := r12HardDeleteLockHelpers[identifier.Name]
			if !tracked {
				return true
			}
			// Defensive: a compiling call always carries the table argument.
			if len(call.Args) <= argumentIndex {
				t.Errorf("%s: %s at %s has no table argument", path, identifier.Name, fset.Position(call.Pos()))
				return true
			}
			callSites++
			literal, ok := call.Args[argumentIndex].(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				t.Errorf("%s: %s at %s passes a non-literal table name; the R12 unresolvable-CAS allowlist cannot prove it stays out of the target set", path, identifier.Name, fset.Position(call.Pos()))
				return true
			}
			table, unquoteErr := strconv.Unquote(literal.Value)
			if unquoteErr != nil {
				t.Errorf("%s: %s at %s has an unparsable table literal %s", path, identifier.Name, fset.Position(call.Pos()), literal.Value)
				return true
			}
			if !r12AllowedHardDeleteLockTables[table] {
				t.Errorf("%s: %s at %s targets table %q, which is not an allowed hard-delete lock table", path, identifier.Name, fset.Position(call.Pos()), table)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("scan production Go sources: %v", err)
	}
	if callSites == 0 {
		t.Fatal("found no hard-delete lock call sites; the allowlist justification would pass vacuously")
	}
}

// TestR12ScanNodeFailsClosedOnNonLiteralCQL is the regression that the
// fail-closed rule exists for. Discovery keys off a source literal, so before
// this rule a target LWT could leave the guard's view through an ordinary
// refactor -- moving its CQL into a const, a variable or fmt.Sprintf -- and CI
// would stay green with an unpinned statement on the blocks partition.
func TestR12ScanNodeFailsClosedOnNonLiteralCQL(t *testing.T) {
	tests := []struct {
		name           string
		source         string
		wantDiscovered bool
		wantUnresolved bool
		wantBatchCAS   bool
	}{
		{
			name: "inline literal is discovered",
			source: `package p

func mutate(session S) {
	session.Query("UPDATE blocks SET gc_state = ? WHERE org_id = ? IF gc_state = ?").MapScanCAS(nil)
}
`,
			wantDiscovered: true,
		},
		{
			name: "const CQL is not silently dropped",
			source: `package p

const updateBlock = "UPDATE blocks SET gc_state = ? WHERE org_id = ? IF gc_state = ?"

func mutate(session S) {
	session.Query(updateBlock).MapScanCAS(nil)
}
`,
			wantUnresolved: true,
		},
		{
			name: "local variable CQL is not silently dropped",
			source: `package p

func mutate(session S) {
	query := "UPDATE blocks SET gc_state = ? WHERE org_id = ? IF gc_state = ?"
	session.Query(query).MapScanCAS(nil)
}
`,
			wantUnresolved: true,
		},
		{
			name: "constructed CQL is not silently dropped",
			source: `package p

func mutate(session S, table string) {
	session.Query(fmt.Sprintf("UPDATE %s SET gc_state = ? WHERE org_id = ? IF gc_state = ?", table)).MapScanCAS(nil)
}
`,
			wantUnresolved: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "synthetic.go", test.source, 0)
			if err != nil {
				t.Fatalf("parse synthetic source: %v", err)
			}

			discovered := map[string][]r12DiscoveredOperation{}
			unresolved := map[string][]token.Position{}
			batchCAS := map[string][]token.Position{}
			for _, declaration := range file.Decls {
				function, ok := declaration.(*ast.FuncDecl)
				if !ok {
					continue
				}
				r12ScanNode(fset, function.Body, r12FunctionName(function), discovered, unresolved, batchCAS)
			}

			if got := len(discovered) > 0; got != test.wantDiscovered {
				t.Errorf("discovered target statement = %v, want %v (discovered=%v)", got, test.wantDiscovered, discovered)
			}
			if got := len(unresolved) > 0; got != test.wantUnresolved {
				t.Errorf("recorded unresolvable CAS = %v, want %v (unresolved=%v)", got, test.wantUnresolved, unresolved)
			}
			if got := len(batchCAS) > 0; got != test.wantBatchCAS {
				t.Errorf("recorded conditional batch CAS = %v, want %v (batchCAS=%v)", got, test.wantBatchCAS, batchCAS)
			}
		})
	}
}

// TestR12ScanNodeSeesEveryCASTerminal is the regression for the second way a
// target statement can leave the guard's view without ever being reported: not by
// hiding its CQL, but by executing through a CAS terminal the scanner does not
// know. The gocql version in use exposes ScanCASContext/MapScanCASContext as
// ordinary LWT execution and the ExecCAS family for conditional batches, so a
// scanner that recognised only ScanCAS/MapScanCAS would return an empty discovery
// set -- a green gate -- for an unpinned mutation on the blocks partition.
func TestR12ScanNodeSeesEveryCASTerminal(t *testing.T) {
	tests := []struct {
		name           string
		source         string
		wantDiscovered bool
		wantUnresolved bool
		wantBatchCAS   bool
	}{
		{
			name: "MapScanCASContext with an inline literal is discovered",
			source: `package p

func mutate(session S, ctx C) {
	session.Query("UPDATE blocks SET gc_state = ? WHERE org_id = ? IF gc_state = ?").MapScanCASContext(ctx, nil)
}
`,
			wantDiscovered: true,
		},
		{
			name: "ScanCASContext with an inline literal is discovered",
			source: `package p

func mutate(session S, ctx C) {
	session.Query("UPDATE blocks SET gc_state = ? WHERE org_id = ? IF gc_state = ?").ScanCASContext(ctx, nil)
}
`,
			wantDiscovered: true,
		},
		{
			name: "MapScanCASContext with const CQL fails closed",
			source: `package p

const updateBlock = "UPDATE blocks SET gc_state = ? WHERE org_id = ? IF gc_state = ?"

func mutate(session S, ctx C) {
	session.Query(updateBlock).MapScanCASContext(ctx, nil)
}
`,
			wantUnresolved: true,
		},
		{
			name: "ScanCASContext with constructed CQL fails closed",
			source: `package p

func mutate(session S, ctx C, table string) {
	session.Query(fmt.Sprintf("UPDATE %s SET gc_state = ? WHERE org_id = ? IF gc_state = ?", table)).ScanCASContext(ctx, nil)
}
`,
			wantUnresolved: true,
		},
		{
			name: "batch ExecCAS is refused",
			source: `package p

func mutate(session S) {
	batch := session.Batch(gocql.LoggedBatch)
	batch.Query(updateBlock)
	batch.ExecCAS()
}
`,
			wantBatchCAS: true,
		},
		{
			name: "batch MapExecCASContext is refused",
			source: `package p

func mutate(session S, ctx C) {
	batch := session.Batch(gocql.LoggedBatch)
	batch.Query(updateBlock)
	batch.MapExecCASContext(ctx, nil)
}
`,
			wantBatchCAS: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "synthetic.go", test.source, 0)
			if err != nil {
				t.Fatalf("parse synthetic source: %v", err)
			}

			discovered := map[string][]r12DiscoveredOperation{}
			unresolved := map[string][]token.Position{}
			batchCAS := map[string][]token.Position{}
			for _, declaration := range file.Decls {
				function, ok := declaration.(*ast.FuncDecl)
				if !ok {
					continue
				}
				r12ScanNode(fset, function.Body, r12FunctionName(function), discovered, unresolved, batchCAS)
			}

			if got := len(discovered) > 0; got != test.wantDiscovered {
				t.Errorf("discovered target statement = %v, want %v (discovered=%v)", got, test.wantDiscovered, discovered)
			}
			if got := len(unresolved) > 0; got != test.wantUnresolved {
				t.Errorf("recorded unresolvable CAS = %v, want %v (unresolved=%v)", got, test.wantUnresolved, unresolved)
			}
			if got := len(batchCAS) > 0; got != test.wantBatchCAS {
				t.Errorf("recorded conditional batch CAS = %v, want %v (batchCAS=%v)", got, test.wantBatchCAS, batchCAS)
			}
		})
	}
}

// TestR12ScanNodeDiscoversAlternateTableSpellings is the mutation test for the
// table matcher. Each source below is a straggler the gate has to catch: an
// unpinned or LOCAL_SERIAL conditional mutation on an R12 partition, written with
// a table reference that a name-literal regex does not recognise. The assertion
// is not only that the statement is discovered but that its pin is reported
// wrong, since discovery without a pin verdict would still let the gate pass.
func TestR12ScanNodeDiscoversAlternateTableSpellings(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   r12SerialPin
	}{
		{
			name: "keyspace-qualified LOCAL_SERIAL mutation is caught",
			source: `package p

func mutate(session S) {
	session.Query("UPDATE sesamefs.blocks SET gc_state = ? WHERE org_id = ? IF gc_state = ?").
		SerialConsistency(gocql.LocalSerial).
		MapScanCAS(nil)
}
`,
			want: r12SerialPin{present: true, local: true},
		},
		{
			name: "quoted unpinned mutation is caught",
			source: `package p

func mutate(session S) {
	session.Query("UPDATE \"blocks\" SET gc_state = ? WHERE org_id = ? IF gc_state = ?").MapScanCAS(nil)
}
`,
			want: r12SerialPin{},
		},
		{
			name: "column delete without a pin is caught",
			source: `package p

func mutate(session S) {
	session.Query("DELETE storage_key FROM blocks WHERE org_id = ? IF gc_state = ?").MapScanCAS(nil)
}
`,
			want: r12SerialPin{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "synthetic.go", test.source, 0)
			if err != nil {
				t.Fatalf("parse synthetic source: %v", err)
			}

			discovered := map[string][]r12DiscoveredOperation{}
			unresolved := map[string][]token.Position{}
			batchCAS := map[string][]token.Position{}
			for _, declaration := range file.Decls {
				function, ok := declaration.(*ast.FuncDecl)
				if !ok {
					continue
				}
				r12ScanNode(fset, function.Body, r12FunctionName(function), discovered, unresolved, batchCAS)
			}

			operations := discovered["mutate|blocks|UPDATE"]
			if len(operations) == 0 {
				operations = discovered["mutate|blocks|DELETE"]
			}
			if len(operations) == 0 {
				t.Fatalf("statement left the R12 target set entirely (discovered=%v, unresolved=%v)", discovered, unresolved)
			}
			if got := operations[0].pin; got != test.want {
				t.Fatalf("serial pin = %+v, want %+v", got, test.want)
			}
		})
	}
}
