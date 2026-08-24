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

// r12BatchCASTerminals are the driver methods that execute a conditional batch.
// A batch collects its statements through separate Batch.Query calls, so its CAS
// call site carries neither the CQL nor the serial pin; the terminal alone cannot
// be classified. Each one therefore has to be allowlisted, and the allowance is
// only sound because the general Query rule still reads every Batch.Query
// statement -- an R12 target inside a batch is discovered there, with no CAS
// terminal attributed to it, and fails the gate.
var r12BatchCASTerminals = map[string]bool{
	"ExecCAS":           true,
	"ExecCASContext":    true,
	"MapExecCAS":        true,
	"MapExecCASContext": true,
	// The deprecated-but-functional Session-level forms of the same operation.
	// v2 keeps them working and delegates to the Batch methods above, so leaving
	// them out would make the rule depend on which spelling a caller picked
	// rather than on what the call does. SesameFS uses MapExecuteBatchCAS today,
	// which is precisely why the omission mattered.
	"ExecuteBatchCAS":    true,
	"MapExecuteBatchCAS": true,
}

// r12AllowedBatchCAS records the conditional batches that have been checked
// against the R12 target set. Every entry must name a call site whose batch
// statements are readable by the general Query rule, so the allowance never
// stands on its own.
var r12AllowedBatchCAS = map[string]r12UnresolvedAllowance{
	"relocateLockRowCASFn": {count: 1, reason: "locked_files row relocation; both batch.Query statements are inline literals on locked_files, so the general Query rule classifies them and would discover an R12 target with no CAS terminal"},
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

// r12UnresolvedAllowance records a Query call site whose CQL cannot be resolved
// to a string, together with why it is out of the R12 target set. The guard is
// fail-closed on unresolvable CQL: without this allowlist a statement that moved
// its CQL into a variable or fmt.Sprintf would stop being discovered instead of
// failing the gate. Every entry must name a call site that provably cannot
// address blocks, gc_block_candidates or gc_s3_orphans.
//
// The rule deliberately applies to every Query call site, not only those ending
// in a CAS method. Cassandra decides that a statement is a lightweight
// transaction because its CQL carries IF, not because the Go method used to
// consume the result happens to end in "CAS" -- Query.Exec is literally
// q.Iter().Close(), and the driver's own NoSkipMetadata documentation refers to
// "CAS operations which do not end in Cas". Keying the fail-closed rule off the
// terminal would leave Query(nonLiteralConditionalCQL).Exec() invisible.
type r12UnresolvedAllowance struct {
	count  int
	reason string
}

var r12AllowedUnresolvedCAS = map[string]r12UnresolvedAllowance{
	"acquireHardDeleteLock":               {count: 2, reason: "hard-delete lock table is a parameter; TestR12HardDeleteLockTablesAreOutOfScope pins every call site to a gc_*_hard_delete_locks literal"},
	"renewHardDeleteLock":                 {count: 1, reason: "same helper family as acquireHardDeleteLock"},
	"releaseHardDeleteLock":               {count: 1, reason: "same helper family as acquireHardDeleteLock"},
	"(*LibraryHandler).UpdateLibrary":     {count: 1, reason: "opens with the literal `UPDATE libraries SET ` and appends caller-built column assignments; the relation is fixed in the literal"},
	"(*OrgAdminHandler).updateOrgSetting": {count: 1, reason: "fmt.Sprintf interpolates an allowlisted settings map key into a literal `UPDATE organizations` statement; the relation is fixed in the format string"},
	"(*AdminHandler).UpdateOrganization":  {count: 1, reason: "fmt.Sprintf interpolates a column name into a literal `UPDATE organizations` statement; the relation is fixed in the format string"},
	"(*Migrator).apply":                   {count: 1, reason: "applies checked-in DDL statements read from migrations/*.cql at startup; not a runtime mutation path and not conditional DML"},
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

		packageBindings := r12PackageStringBindings(file)
		for _, declaration := range file.Decls {
			switch typed := declaration.(type) {
			case *ast.FuncDecl:
				// A function's own locals shadow the package scope, so one
				// function's `query` never resolves another function's name.
				bindings := r12ScopedBindings(packageBindings, r12CollectStringBindings(typed.Body))
				r12ScanNode(fset, typed.Body, r12FunctionName(typed), bindings, discovered, unresolved, batchCAS)
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
						r12ScanNode(fset, value, symbol, packageBindings, discovered, unresolved, batchCAS)
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

	// Conditional batches cannot be classified from their CAS call site, so each
	// one is refused unless it is explicitly allowlisted. The allowance is not a
	// blind spot: the general Query rule above still reads every Batch.Query
	// statement, so an R12 target inside an allowlisted batch is discovered with
	// no CAS terminal attributed to it and fails the gate anyway.
	for symbol, positions := range batchCAS {
		allowance, allowed := r12AllowedBatchCAS[symbol]
		if !allowed {
			t.Errorf(
				"conditional batch CAS in %s at %s: a batch's statements and its serial pin cannot be attributed to this call site; add an explicit allowlist entry, or extend R12 to classify batches",
				symbol,
				r12FormatPositions(positions),
			)
			continue
		}
		if len(positions) != allowance.count {
			t.Errorf(
				"allowlisted conditional batch CAS %s has %d call sites, want %d (%s) at %s",
				symbol,
				len(positions),
				allowance.count,
				allowance.reason,
				r12FormatPositions(positions),
			)
		}
	}

	for symbol, allowance := range r12AllowedBatchCAS {
		if len(batchCAS[symbol]) == 0 {
			t.Errorf("allowlisted conditional batch CAS %s (%s) no longer exists; drop the stale allowlist entry", symbol, allowance.reason)
		}
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

func r12ScanNode(fset *token.FileSet, node ast.Node, symbol string, bindings r12StringBindings, discovered map[string][]r12DiscoveredOperation, unresolved map[string][]token.Position, batchCAS map[string][]token.Position) {
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

		// A CAS terminal whose Query call cannot be located at all: the chain was
		// broken up, so neither the CQL nor the pin is readable here.
		if selector, ok := call.Fun.(*ast.SelectorExpr); ok && r12QueryCASTerminals[selector.Sel.Name] {
			if r12FindQueryCall(selector.X) == nil {
				unresolved[symbol] = append(unresolved[symbol], fset.Position(call.Pos()))
			}
		}

		// Every Query call is classified from its CQL, whatever terminal consumes
		// the result. Cassandra makes a statement a lightweight transaction
		// because its CQL carries IF; the Go method that reads the outcome is not
		// the authority. Query.Exec is q.Iter().Close(), and the driver's own
		// NoSkipMetadata documentation speaks of "CAS operations which do not end
		// in Cas" -- so keying discovery off CAS terminals would let an unpinned
		// conditional mutation ending in Exec through untouched.
		if selector, ok := call.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "Query" {
			if len(call.Args) == 0 {
				// net/url's URL.Query() takes no arguments; gocql's always takes
				// the statement. Without this the guard would demand an allowlist
				// entry for ordinary URL handling.
				return true
			}
			query, resolvedCQL := r12QueryLiteral(call, bindings)
			if !resolvedCQL {
				// Fail closed: the CQL cannot be read, so discovery cannot rule
				// out an R12 target no matter how the statement is executed.
				unresolved[symbol] = append(unresolved[symbol], fset.Position(call.Pos()))
				return true
			}
			if table, statement, ok := r12TargetStatement(query); ok {
				key := symbol + "|" + table + "|" + statement
				// The terminal and the pin come from the enclosing expression, so
				// a statement executed through Exec is discovered with no
				// terminal and reported, rather than never discovered at all.
				terminal, pin := r12FindTerminalAndPin(node, call)
				discovered[key] = append(discovered[key], r12DiscoveredOperation{
					key:      key,
					position: fset.Position(call.Pos()),
					terminal: terminal,
					pin:      pin,
				})
			}
		}
		return true
	})
}

// r12FindTerminalAndPin walks the scanned scope for the expression that consumes
// the given Query call, and reports whether it ends in a recognised CAS terminal
// along with the serial pin applied along the way. Reading the terminal from the
// enclosing chain, rather than making the terminal the entry point, is what lets
// a conditional statement executed through Exec be discovered and then reported
// as having no CAS terminal instead of never being discovered at all.
func r12FindTerminalAndPin(scope ast.Node, queryCall *ast.CallExpr) (bool, r12SerialPin) {
	var terminal bool
	var pin r12SerialPin
	ast.Inspect(scope, func(current ast.Node) bool {
		call, ok := current.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !r12QueryCASTerminals[selector.Sel.Name] {
			return true
		}
		if r12FindQueryCall(selector.X) != queryCall {
			return true
		}
		terminal = true
		pin = r12FindSerialPin(selector.X)
		return false
	})
	if terminal {
		return true, pin
	}
	// No CAS terminal consumes this Query, but a pin may still have been applied
	// to the chain; report it so the failure names the real defect.
	return false, r12FindSerialPinForQuery(scope, queryCall)
}

// r12FindSerialPinForQuery reads the serial pin from the expression the given
// Query call is embedded in, for chains that do not end in a CAS terminal.
func r12FindSerialPinForQuery(scope ast.Node, queryCall *ast.CallExpr) r12SerialPin {
	var pin r12SerialPin
	ast.Inspect(scope, func(current ast.Node) bool {
		call, ok := current.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "SerialConsistency" {
			return true
		}
		if r12FindQueryCall(selector.X) != queryCall {
			return true
		}
		pin = r12FindSerialPin(call)
		return false
	})
	return pin
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

// r12StringBindings resolves an identifier to the CQL it carries, for
// identifiers whose value is built entirely from string literals. Anything else
// is absent from the map and therefore unresolvable.
//
// Resolution has to account for how this codebase actually builds CQL. The
// common shape is a literal opening followed by conditional appends:
//
//	query := `SELECT ... FROM gc_pending_items WHERE ...`
//	if !identityAt.IsZero() { query += ` AND identity_at = ?` }
//	query += ` LIMIT 1`
//
// Resolving such a name to its first binding alone would be a false green
// manufactured by the resolver: the guard would read a prefix, conclude the
// statement is out of scope, and stay green while the appended text made it
// something else. So a name is resolved to the concatenation of every literal
// fragment assigned or appended to it, and only when every fragment is a
// literal. That over-approximates -- the value reaching a given call site may
// omit a conditional append -- which is the safe direction: the opening binding
// fixes the statement verb and table, appends can only add clauses, so a value
// that is not an R12 target cannot become one by dropping a fragment.
//
// A name touched in any way the resolver cannot follow (a non-literal fragment,
// a plain reassignment, a declaration without a value, an address taken) is
// poisoned and reported as unresolvable.
type r12StringBindings map[string]string

// r12CollectStringBindings gathers the literal-only string identifiers visible in
// the given nodes, concatenating appends in source order.
func r12CollectStringBindings(nodes ...ast.Node) r12StringBindings {
	bindings := r12StringBindings{}
	poisoned := map[string]bool{}

	poison := func(name string) {
		poisoned[name] = true
		delete(bindings, name)
	}

	literalValue := func(value ast.Expr) (string, bool) {
		literal, ok := value.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return "", false
		}
		unquoted, err := strconv.Unquote(literal.Value)
		if err != nil {
			return "", false
		}
		return unquoted, true
	}

	open := func(name string, value ast.Expr) {
		if poisoned[name] {
			return
		}
		text, ok := literalValue(value)
		if !ok {
			poison(name)
			return
		}
		if _, seen := bindings[name]; seen {
			// A second opening binding: which one reaches the call site is a
			// flow question this guard does not answer.
			poison(name)
			return
		}
		bindings[name] = text
	}

	appendFragment := func(name string, value ast.Expr) {
		if poisoned[name] {
			return
		}
		text, ok := literalValue(value)
		if !ok {
			poison(name)
			return
		}
		existing, seen := bindings[name]
		if !seen {
			// Appending to something never opened here.
			poison(name)
			return
		}
		bindings[name] = existing + text
	}

	for _, node := range nodes {
		if node == nil {
			continue
		}
		ast.Inspect(node, func(current ast.Node) bool {
			switch typed := current.(type) {
			case *ast.ValueSpec:
				for index, name := range typed.Names {
					if index >= len(typed.Values) {
						// Declared without an initializer; whatever fills it in
						// later is not visible here.
						poison(name.Name)
						continue
					}
					open(name.Name, typed.Values[index])
				}
			case *ast.AssignStmt:
				for index, target := range typed.Lhs {
					identifier, ok := target.(*ast.Ident)
					if !ok {
						continue
					}
					if index >= len(typed.Rhs) || len(typed.Rhs) != len(typed.Lhs) {
						poison(identifier.Name)
						continue
					}
					switch typed.Tok {
					case token.ADD_ASSIGN:
						appendFragment(identifier.Name, typed.Rhs[index])
					case token.DEFINE, token.ASSIGN:
						// `query = query + lit` is the same append written out.
						if binary, ok := typed.Rhs[index].(*ast.BinaryExpr); ok && binary.Op == token.ADD {
							if left, ok := binary.X.(*ast.Ident); ok && left.Name == identifier.Name {
								appendFragment(identifier.Name, binary.Y)
								continue
							}
						}
						open(identifier.Name, typed.Rhs[index])
					default:
						poison(identifier.Name)
					}
				}
			case *ast.UnaryExpr:
				if typed.Op == token.AND {
					if identifier, ok := typed.X.(*ast.Ident); ok {
						poison(identifier.Name)
					}
				}
			}
			return true
		})
	}
	return bindings
}

// r12PackageStringBindings gathers only the file's top-level const/var string
// bindings, so one function's locals never resolve another function's names.
func r12PackageStringBindings(file *ast.File) r12StringBindings {
	nodes := make([]ast.Node, 0, len(file.Decls))
	for _, declaration := range file.Decls {
		if general, ok := declaration.(*ast.GenDecl); ok {
			nodes = append(nodes, general)
		}
	}
	return r12CollectStringBindings(nodes...)
}

// r12ScopedBindings overlays a function's locals on the package-level bindings,
// matching Go's shadowing.
func r12ScopedBindings(pkg r12StringBindings, local r12StringBindings) r12StringBindings {
	if len(local) == 0 {
		return pkg
	}
	merged := make(r12StringBindings, len(pkg)+len(local))
	for name, value := range pkg {
		merged[name] = value
	}
	for name, value := range local {
		merged[name] = value
	}
	return merged
}

func r12QueryLiteral(call *ast.CallExpr, bindings r12StringBindings) (string, bool) {
	if len(call.Args) == 0 {
		return "", false
	}
	switch argument := call.Args[0].(type) {
	case *ast.BasicLit:
		if argument.Kind != token.STRING {
			return "", false
		}
		value, err := strconv.Unquote(argument.Value)
		if err != nil {
			return argument.Value, true
		}
		return value, true
	case *ast.Ident:
		// A const or single-binding variable holding the CQL is as visible as an
		// inline literal, so moving a statement into one neither hides it from
		// discovery nor costs it an allowlist entry.
		value, ok := bindings[argument.Name]
		return value, ok
	}
	return "", false
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
			// A const is resolved rather than waived, which is strictly stronger
			// than the fail-closed fallback: the statement is analysed for its
			// table and pin instead of costing an allowlist entry.
			name: "const CQL is resolved, not dropped and not waived",
			source: `package p

const updateBlock = "UPDATE blocks SET gc_state = ? WHERE org_id = ? IF gc_state = ?"

func mutate(session S) {
	session.Query(updateBlock).MapScanCAS(nil)
}
`,
			wantDiscovered: true,
		},
		{
			name: "single-binding local variable CQL is resolved",
			source: `package p

func mutate(session S) {
	query := "UPDATE blocks SET gc_state = ? WHERE org_id = ? IF gc_state = ?"
	session.Query(query).MapScanCAS(nil)
}
`,
			wantDiscovered: true,
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
			packageBindings := r12PackageStringBindings(file)
			for _, declaration := range file.Decls {
				function, ok := declaration.(*ast.FuncDecl)
				if !ok {
					continue
				}
				bindings := r12ScopedBindings(packageBindings, r12CollectStringBindings(function.Body))
				r12ScanNode(fset, function.Body, r12FunctionName(function), bindings, discovered, unresolved, batchCAS)
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
			name: "MapScanCASContext with const CQL is resolved and discovered",
			source: `package p

const updateBlock = "UPDATE blocks SET gc_state = ? WHERE org_id = ? IF gc_state = ?"

func mutate(session S, ctx C) {
	session.Query(updateBlock).MapScanCASContext(ctx, nil)
}
`,
			wantDiscovered: true,
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
			// The batch statement is an inline literal on a non-R12 relation, so
			// only the batch terminal itself is at issue here.
			name: "batch ExecCAS is refused",
			source: `package p

func mutate(session S) {
	batch := session.Batch(gocql.LoggedBatch)
	batch.Query("INSERT INTO locked_files (repo_id) VALUES (?) IF NOT EXISTS")
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
	batch.Query("INSERT INTO locked_files (repo_id) VALUES (?) IF NOT EXISTS")
	batch.MapExecCASContext(ctx, nil)
}
`,
			wantBatchCAS: true,
		},
		{
			name: "deprecated Session.ExecuteBatchCAS is refused",
			source: `package p

func mutate(session S) {
	batch := session.Batch(gocql.LoggedBatch)
	batch.Query("INSERT INTO locked_files (repo_id) VALUES (?) IF NOT EXISTS")
	session.ExecuteBatchCAS(batch)
}
`,
			wantBatchCAS: true,
		},
		{
			name: "deprecated Session.MapExecuteBatchCAS is refused",
			source: `package p

func mutate(session S) {
	batch := session.Batch(gocql.LoggedBatch)
	batch.Query("INSERT INTO locked_files (repo_id) VALUES (?) IF NOT EXISTS")
	session.MapExecuteBatchCAS(batch, nil)
}
`,
			wantBatchCAS: true,
		},
		{
			// An R12 statement inside a batch is still caught, by the general
			// Query rule rather than by the terminal: that is what makes the
			// batch allowlist sound.
			name: "an R12 statement inside a batch is discovered with no terminal",
			source: `package p

func mutate(session S) {
	batch := session.Batch(gocql.LoggedBatch)
	batch.Query("UPDATE blocks SET gc_state = ? WHERE org_id = ? IF gc_state = ?")
	session.MapExecuteBatchCAS(batch, nil)
}
`,
			wantBatchCAS:   true,
			wantDiscovered: true,
		},
		{
			name: "url.Query() is not a CQL query",
			source: `package p

func redirect(target *url.URL) {
	merged := target.Query()
	_ = merged
}
`,
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
			packageBindings := r12PackageStringBindings(file)
			for _, declaration := range file.Decls {
				function, ok := declaration.(*ast.FuncDecl)
				if !ok {
					continue
				}
				bindings := r12ScopedBindings(packageBindings, r12CollectStringBindings(function.Body))
				r12ScanNode(fset, function.Body, r12FunctionName(function), bindings, discovered, unresolved, batchCAS)
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

// TestR12ScanNodeSeesLWTsThatDoNotEndInCAS is the regression for the deepest
// version of the same mistake: treating the Go method name as the authority on
// whether a statement is a lightweight transaction. Cassandra decides that from
// the CQL -- a statement carrying IF is an LWT however the caller consumes the
// result -- and gocql's Query.Exec is literally q.Iter().Close(), with the
// driver's own NoSkipMetadata documentation referring to "CAS operations which do
// not end in Cas". A guard keyed on CAS terminals reports nothing at all for
// Query(conditionalCQL).Exec(), which is a green gate over an unpinned mutation.
//
// Discovery is therefore keyed on the CQL. A conditional R12 statement is found
// whatever executes it, and a chain with no CAS terminal is reported as having
// none rather than never being seen.
func TestR12ScanNodeSeesLWTsThatDoNotEndInCAS(t *testing.T) {
	tests := []struct {
		name           string
		source         string
		wantDiscovered bool
		wantUnresolved bool
		wantTerminal   bool
		wantPin        r12SerialPin
	}{
		{
			name: "const CQL executed through Exec is discovered with no terminal",
			source: `package p

const updateBlock = "UPDATE blocks SET gc_state = ? WHERE org_id = ? IF gc_state = ?"

func mutate(session S) {
	session.Query(updateBlock).Exec()
}
`,
			wantDiscovered: true,
		},
		{
			name: "const CQL executed through Exec with LOCAL_SERIAL reports the wrong pin",
			source: `package p

const updateBlock = "UPDATE blocks SET gc_state = ? WHERE org_id = ? IF gc_state = ?"

func mutate(session S) {
	session.Query(updateBlock).SerialConsistency(gocql.LocalSerial).Exec()
}
`,
			wantDiscovered: true,
			wantPin:        r12SerialPin{present: true, local: true},
		},
		{
			name: "single-binding local variable executed through ExecContext is discovered",
			source: `package p

func mutate(session S, ctx C) {
	stmt := "UPDATE blocks SET gc_state = ? WHERE org_id = ? IF gc_state = ?"
	session.Query(stmt).ExecContext(ctx)
}
`,
			wantDiscovered: true,
		},
		{
			name: "a resolved const with a proper CAS terminal and pin still passes",
			source: `package p

const updateBlock = "UPDATE blocks SET gc_state = ? WHERE org_id = ? IF gc_state = ?"

func mutate(session S) {
	session.Query(updateBlock).SerialConsistency(gocql.Serial).MapScanCAS(nil)
}
`,
			wantDiscovered: true,
			wantTerminal:   true,
			wantPin:        r12SerialPin{present: true, serial: true},
		},
		{
			name: "an appended fragment cannot hide the conditional clause",
			source: `package p

func mutate(session S) {
	query := "UPDATE blocks SET gc_state = ? WHERE org_id = ? "
	query += "IF gc_state = ?"
	session.Query(query).Exec()
}
`,
			wantDiscovered: true,
		},
		{
			name: "a non-literal fragment poisons resolution instead of resolving a prefix",
			source: `package p

func mutate(session S, extra string) {
	query := "UPDATE blocks SET gc_state = ? WHERE org_id = ? "
	query += extra
	session.Query(query).Exec()
}
`,
			wantUnresolved: true,
		},
		{
			name: "a reassigned name is not resolved to either binding",
			source: `package p

func mutate(session S, flag bool) {
	query := "SELECT 1 FROM blocks WHERE org_id = ?"
	if flag {
		query = "UPDATE blocks SET gc_state = ? WHERE org_id = ? IF gc_state = ?"
	}
	session.Query(query).Exec()
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
			packageBindings := r12PackageStringBindings(file)
			for _, declaration := range file.Decls {
				function, ok := declaration.(*ast.FuncDecl)
				if !ok {
					continue
				}
				bindings := r12ScopedBindings(packageBindings, r12CollectStringBindings(function.Body))
				r12ScanNode(fset, function.Body, r12FunctionName(function), bindings, discovered, unresolved, batchCAS)
			}

			if got := len(discovered) > 0; got != test.wantDiscovered {
				t.Fatalf("discovered target statement = %v, want %v (discovered=%v, unresolved=%v)", got, test.wantDiscovered, discovered, unresolved)
			}
			if got := len(unresolved) > 0; got != test.wantUnresolved {
				t.Fatalf("recorded unresolvable CQL = %v, want %v (unresolved=%v)", got, test.wantUnresolved, unresolved)
			}
			if !test.wantDiscovered {
				return
			}
			operations := discovered["mutate|blocks|UPDATE"]
			if len(operations) != 1 {
				t.Fatalf("want exactly one discovered blocks UPDATE, got %v", discovered)
			}
			if operations[0].terminal != test.wantTerminal {
				t.Errorf("terminal = %v, want %v", operations[0].terminal, test.wantTerminal)
			}
			if operations[0].pin != test.wantPin {
				t.Errorf("serial pin = %+v, want %+v", operations[0].pin, test.wantPin)
			}
		})
	}
}

// TestR12UnresolvedAllowlistNamesNoR12Table closes the hole every symbol-keyed
// allowlist leaves open: it says "this call site is not an R12 table" without
// proving it. For each allowlisted symbol, every CQL literal in its body that
// opens a mutation must name a relation outside the target set. The hard-delete
// lock helpers interpolate their table and are proven separately by
// TestR12HardDeleteLockTablesAreOutOfScope; here they simply have no literal
// opening to check, which is why both tests exist.
func TestR12UnresolvedAllowlistNamesNoR12Table(t *testing.T) {
	root := filepath.Join("..", "..")
	skipDirs := map[string]bool{
		".git":            true,
		"frontend":        true,
		"mobile-frontend": true,
		"node_modules":    true,
		"vendor":          true,
	}

	checked := map[string]bool{}
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

		inspect := func(symbol string, node ast.Node) {
			if _, allowed := r12AllowedUnresolvedCAS[symbol]; !allowed {
				return
			}
			checked[symbol] = true
			ast.Inspect(node, func(current ast.Node) bool {
				literal, ok := current.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					return true
				}
				value, unquoteErr := strconv.Unquote(literal.Value)
				if unquoteErr != nil {
					return true
				}
				// A format string is not conditional on its own, so ask the
				// table question directly rather than through the IF gate.
				for _, matches := range r12StatementPattern.FindAllStringSubmatch(r12StripCQLStringLiterals(r12StripCQLComments(value)), -1) {
					name := r12NormalizeCQLIdentifier(matches[2])
					if matches[3] != "" {
						name = r12NormalizeCQLIdentifier(matches[3])
					}
					if r12TargetTables[name] {
						t.Errorf(
							"%s: allowlisted symbol %s contains a CQL literal opening a mutation on R12 table %q at %s; the allowlist entry claims it cannot reach the target set",
							path, symbol, name, fset.Position(literal.Pos()),
						)
					}
				}
				return true
			})
		}

		for _, declaration := range file.Decls {
			switch typed := declaration.(type) {
			case *ast.FuncDecl:
				inspect(r12FunctionName(typed), typed.Body)
			case *ast.GenDecl:
				for _, specification := range typed.Specs {
					valueSpec, ok := specification.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for index, value := range valueSpec.Values {
						if index < len(valueSpec.Names) {
							inspect(valueSpec.Names[index].Name, value)
						}
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan production Go sources: %v", err)
	}

	for symbol := range r12AllowedUnresolvedCAS {
		if !checked[symbol] {
			t.Errorf("allowlisted symbol %s was never found in production sources; the entry is stale or misspelled", symbol)
		}
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
			packageBindings := r12PackageStringBindings(file)
			for _, declaration := range file.Decls {
				function, ok := declaration.(*ast.FuncDecl)
				if !ok {
					continue
				}
				bindings := r12ScopedBindings(packageBindings, r12CollectStringBindings(function.Body))
				r12ScanNode(fset, function.Body, r12FunctionName(function), bindings, discovered, unresolved, batchCAS)
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
