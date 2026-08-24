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

// r12AllowedBatchCASShape pins what each allowlisted conditional batch is
// allowed to contain. The allowance in r12AllowedBatchCAS rests on the claim
// that the general Query rule reads the batch's statements, and that claim is
// only as good as the way the statements were added: gocql exposes
// Batch.Bind(stmt, binding) as a second entry point, and Batch.Entries is a
// public []BatchEntry with a public Stmt field. A batch could therefore carry a
// statement the scanner never classifies while its CAS terminal stays
// allowlisted. TestR12AllowedBatchCASStatementsStayOutOfScope reads the real
// source and proves the shape instead of assuming it.
var r12AllowedBatchCASShape = map[string]struct {
	statements int
	tables     map[string]bool
}{
	"relocateLockRowCASFn": {statements: 2, tables: map[string]bool{"locked_files": true}},
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
// Five escape routes are closed explicitly, because each one removes a
// statement from discovery instead of reporting it unpinned: an execution path
// the scanner does not recognise (the Context CAS variants, batch CAS, or a
// conditional statement consumed by Exec); CQL that is not a source literal; an
// identifier resolved to a binding that is not the one reaching the call site
// -- a shadowed name, or a package var that another function can reassign; a
// table reference spelled with quotes or a keyspace qualifier; and a batch
// statement added through Batch.Bind, a hand-built BatchEntry, or direct access
// to Batch.Entries rather than Batch.Query.
func TestR12SerialDomainGuard(t *testing.T) {
	discovered := map[string][]r12DiscoveredOperation{}
	unresolved := map[string][]token.Position{}
	batchCAS := map[string][]token.Position{}

	r12WalkProductionFiles(t, func(fset *token.FileSet, _ string, file *ast.File) {
		r12ScanFile(fset, file, discovered, unresolved, batchCAS)
	})

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

// r12WalkProductionFiles parses every non-test Go file outside the excluded
// trees and hands it to visit. The gate and the tests that prove an allowlist
// entry share it, so all of them reason about exactly the same file set.
func r12WalkProductionFiles(t *testing.T, visit func(fset *token.FileSet, path string, file *ast.File)) {
	t.Helper()
	root := filepath.Join("..", "..")
	skipDirs := map[string]bool{
		".git":            true,
		"frontend":        true,
		"mobile-frontend": true,
		"node_modules":    true,
		"vendor":          true,
	}

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

		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			t.Errorf("%s: parse: %v", path, parseErr)
			return nil
		}
		visit(fset, path, file)
		return nil
	})
	if err != nil {
		t.Fatalf("scan production Go sources: %v", err)
	}
	if scanned == 0 {
		t.Fatal("scanned no production Go sources; R12 guard would pass vacuously")
	}
}

// r12ScanFile classifies one parsed file, scoping each declaration the way Go
// scopes it: a declared function is read with its own bindings, and a
// package-level value -- the shape the protected statements are written in --
// with the bindings of the function literal it holds.
func r12ScanFile(fset *token.FileSet, file *ast.File, discovered map[string][]r12DiscoveredOperation, unresolved map[string][]token.Position, batchCAS map[string][]token.Position) {
	packageBindings := r12PackageStringBindings(file)
	for _, declaration := range file.Decls {
		switch typed := declaration.(type) {
		case *ast.FuncDecl:
			// A function's own bindings -- parameters and receiver included --
			// shadow the package scope, and a name bound in both scopes is
			// resolved in neither.
			r12ScanNode(fset, typed.Body, r12FunctionName(typed), r12FunctionBindings(packageBindings, typed), discovered, unresolved, batchCAS)
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
					r12ScanNode(fset, value, symbol, r12ValueBindings(packageBindings, value), discovered, unresolved, batchCAS)
				}
			}
		}
	}
}

// r12DeclaredSymbols maps a top-level declaration to the symbols it defines and
// the node each symbol's body or value lives in, under the same names the gate
// reports.
func r12DeclaredSymbols(declaration ast.Decl) map[string]ast.Node {
	symbols := map[string]ast.Node{}
	switch typed := declaration.(type) {
	case *ast.FuncDecl:
		if typed.Body != nil {
			symbols[r12FunctionName(typed)] = typed.Body
		}
	case *ast.GenDecl:
		for _, specification := range typed.Specs {
			valueSpec, ok := specification.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for index, value := range valueSpec.Values {
				if index < len(valueSpec.Names) {
					symbols[valueSpec.Names[index].Name] = value
				}
			}
		}
	}
	return symbols
}

// r12DirectBatchEntriesSelector finds the exported batch slice access that the
// general scanner cannot classify. The shape gate has no type information, so
// any selector named Entries inside an allowlisted batch is rejected.
func r12DirectBatchEntriesSelector(node ast.Node) *ast.SelectorExpr {
	var found *ast.SelectorExpr
	ast.Inspect(node, func(current ast.Node) bool {
		selector, ok := current.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == "Entries" {
			found = selector
			return false
		}
		return true
	})
	return found
}

// r12ScanSyntheticSource classifies a synthetic file exactly as the gate
// classifies a production one, so a regression cannot pass through a scan path
// the gate does not use.
func r12ScanSyntheticSource(t *testing.T, source string) (map[string][]r12DiscoveredOperation, map[string][]token.Position, map[string][]token.Position) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "synthetic.go", source, 0)
	if err != nil {
		t.Fatalf("parse synthetic source: %v", err)
	}
	discovered := map[string][]r12DiscoveredOperation{}
	unresolved := map[string][]token.Position{}
	batchCAS := map[string][]token.Position{}
	r12ScanFile(fset, file, discovered, unresolved, batchCAS)
	return discovered, unresolved, batchCAS
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
		// A batch statement can also be added by building the driver's exported
		// BatchEntry directly: Batch.Entries is a public []BatchEntry whose Stmt
		// field is public too, so `b.Entries = append(b.Entries,
		// gocql.BatchEntry{Stmt: cql})` reaches Cassandra without passing
		// through Batch.Query or Batch.Bind. Discovery does not model that
		// shape, so any reference to the type is fail-closed rather than read.
		// Checking the identifier covers both `BatchEntry{...}` and
		// `gocql.BatchEntry{...}`, and reaches each occurrence once.
		if identifier, ok := current.(*ast.Ident); ok && identifier.Name == "BatchEntry" {
			unresolved[symbol] = append(unresolved[symbol], fset.Position(identifier.Pos()))
			return true
		}

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
		if selector, ok := call.Fun.(*ast.SelectorExpr); ok && r12IsCQLEntryPoint(selector.Sel.Name, len(call.Args)) {
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

// r12IsCQLEntryPoint reports whether a call hands CQL text to the driver as its
// first argument. Two methods do, and both have to be read: Session/Batch.Query
// and Batch.Bind. Bind is not a variant spelling -- it appends its own
// BatchEntry with the statement it was given, so a conditional statement added
// with Bind inside an allowlisted batch would otherwise never be classified,
// which is precisely the hole the batch allowance is supposed to be free of.
//
// The argument counts are the discriminator, because the guard has no type
// information. net/url's URL.Query() takes none, while gocql's Query always
// takes the statement. gin's c.Bind(&payload) takes exactly one, while gocql's
// Batch.Bind takes the statement plus a binding callback. A two-argument Bind
// that turns out to be something else fails closed and costs an allowlist
// entry, which is the direction this gate errs in everywhere else.
func r12IsCQLEntryPoint(method string, arguments int) bool {
	switch method {
	case "Query":
		return arguments > 0
	case "Bind":
		return arguments >= 2
	}
	return false
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
// A name is recorded even when it cannot be resolved, and that is what makes the
// resolver safe under Go's scoping rules. `values` says what a name is worth;
// `names` records every name the scope binds at all -- parameters, receivers,
// named results, range and := declarations, plain assignments, and poisoned
// names alike. A name present in `names` but absent from `values` is the
// load-bearing state: it means "bound here, value unknown", and it has to shadow
// an outer binding of the same name instead of letting the outer value show
// through. Without it, `func mutate(session S, stmt string)` sitting under a
// package-level `const stmt = "SELECT ... FROM libraries"` would resolve
// session.Query(stmt) to the package const, and the LWT the caller actually
// passes would never be discovered -- the precise false green this guard exists
// to prevent.
type r12StringBindings struct {
	values map[string]string
	names  map[string]bool
}

func r12NewStringBindings() r12StringBindings {
	return r12StringBindings{values: map[string]string{}, names: map[string]bool{}}
}

// resolve reports the CQL a name carries, for names this scope can resolve.
func (bindings r12StringBindings) resolve(name string) (string, bool) {
	value, ok := bindings.values[name]
	return value, ok
}

// r12CollectStringBindings gathers the literal-only string identifiers bound in
// the given nodes, concatenating appends in source order, and records every name
// they bind at all. It descends into function literals, so a closure's
// parameters shadow and its locals are seen.
func r12CollectStringBindings(nodes ...ast.Node) r12StringBindings {
	return r12CollectScopeBindings(true, nodes...)
}

// r12CollectScopeBindings collects one scope's bindings. descendIntoFuncLits
// separates the two callers: a function is collected together with the literals
// nested in it, while the package collection stops at every function literal,
// because the names bound inside `var fn = func(query string) { ... }` belong to
// that literal and not to the file.
func r12CollectScopeBindings(descendIntoFuncLits bool, nodes ...ast.Node) r12StringBindings {
	bindings := r12NewStringBindings()
	poisoned := map[string]bool{}

	// bind records that this scope binds the name at all. Every path below goes
	// through it, so a name can never be resolved in an enclosing scope after
	// this one has bound it.
	bind := func(name string) bool {
		if name == "" || name == "_" {
			return false
		}
		bindings.names[name] = true
		return true
	}

	poison := func(name string) {
		if !bind(name) {
			return
		}
		poisoned[name] = true
		delete(bindings.values, name)
	}

	poisonFields := func(fields *ast.FieldList) {
		if fields == nil {
			return
		}
		for _, field := range fields.List {
			for _, name := range field.Names {
				poison(name.Name)
			}
		}
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
		if !bind(name) || poisoned[name] {
			return
		}
		text, ok := literalValue(value)
		if !ok {
			poison(name)
			return
		}
		if _, seen := bindings.values[name]; seen {
			// A second opening binding: which one reaches the call site is a
			// flow question this guard does not answer.
			poison(name)
			return
		}
		bindings.values[name] = text
	}

	appendFragment := func(name string, value ast.Expr) {
		if !bind(name) || poisoned[name] {
			return
		}
		text, ok := literalValue(value)
		if !ok {
			poison(name)
			return
		}
		existing, seen := bindings.values[name]
		if !seen {
			// Appending to something never opened here.
			poison(name)
			return
		}
		bindings.values[name] = existing + text
	}

	for _, node := range nodes {
		if node == nil {
			continue
		}
		ast.Inspect(node, func(current ast.Node) bool {
			switch typed := current.(type) {
			case *ast.FuncLit:
				if !descendIntoFuncLits {
					return false
				}
			case *ast.FuncDecl:
				// The receiver and the signature bind before the body runs.
				// ast.Inspect is pre-order, so it reaches this node and then the
				// FuncType below before any statement in the body, and those
				// names are poisoned before a body binding could open them.
				poisonFields(typed.Recv)
			case *ast.FuncType:
				// Parameters and named results are bindings whose value the
				// resolver cannot see: they come from the caller.
				poisonFields(typed.Params)
				poisonFields(typed.Results)
			case *ast.RangeStmt:
				// `for name := range ...` binds without an AssignStmt.
				for _, target := range []ast.Expr{typed.Key, typed.Value} {
					if identifier, ok := target.(*ast.Ident); ok {
						poison(identifier.Name)
					}
				}
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

// r12PackageStringBindings gathers the file's top-level string bindings, so one
// function's locals never resolve another function's names -- and it separates
// the two kinds of package-level name, because only one of them is a value.
//
// A package `const` is immutable: the literal in its declaration is what every
// call site sees. A package `var` is not. Any function in the package can
// reassign it, `&stmt` can be handed to a helper, and package vars are shared
// across files while this gate reads one file at a time. A var declared as
// `"SELECT id FROM libraries"` may therefore be carrying
// `UPDATE blocks ... IF ...` by the time a Query call runs, and resolving it
// from its declaration would be a false green manufactured by the guard itself.
//
// So a package var contributes its name -- which still shadows, and still
// blocks a call site from resolving -- but never a value. Proving that a
// package var is never reassigned anywhere in the package, in any file, init
// function, closure or build variant, is not a thing this gate should attempt;
// refusing to resolve it costs an inline literal or an allowlist entry.
func r12PackageStringBindings(file *ast.File) r12StringBindings {
	bindings := r12NewStringBindings()
	poisoned := map[string]bool{}
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || (general.Tok != token.CONST && general.Tok != token.VAR) {
			continue
		}
		collected := r12CollectScopeBindings(false, general)
		for name := range collected.names {
			if bindings.names[name] {
				// Bound by more than one package-level declaration; resolve
				// none of them.
				poisoned[name] = true
				delete(bindings.values, name)
				continue
			}
			bindings.names[name] = true
		}
		if general.Tok != token.CONST {
			continue
		}
		for name, value := range collected.values {
			if poisoned[name] {
				continue
			}
			bindings.values[name] = value
		}
	}
	return bindings
}

// r12ScopedBindings resolves a function's view of the package scope. It is
// deliberately fail-closed rather than a faithful model of Go's lexical scoping,
// because the two failure modes are not symmetric: a name this guard refuses to
// resolve is reported as unresolvable CQL and fails the gate loudly, while a
// name resolved to the wrong binding is a silent false green.
//
// Two rules, both in the same direction:
//
//   - A package name the function binds anywhere -- parameter, receiver, local,
//     range variable, resolved or poisoned -- is dropped from the merged values.
//     This is what stops a parameter, or a dynamically built local, from
//     unmasking the package const it shadows.
//   - A local name that collides with a package binding is dropped as well. The
//     shadowing runs both ways: an inner-block `stmt := "SELECT ..."` must not
//     make a package-level `stmt` holding an R12 LWT resolve to that inner
//     SELECT at a call site the inner declaration never reaches.
//
// What stays resolvable is the case with no ambiguity at all: a name bound in
// exactly one of the two scopes, once, entirely from string literals. Everything
// else is answered with "unresolvable", which the caller turns into a fail-closed
// report rather than a classification.
func r12ScopedBindings(pkg r12StringBindings, local r12StringBindings) r12StringBindings {
	merged := r12NewStringBindings()
	for name := range pkg.names {
		merged.names[name] = true
	}
	for name := range local.names {
		merged.names[name] = true
	}
	for name, value := range pkg.values {
		if local.names[name] {
			continue
		}
		merged.values[name] = value
	}
	for name, value := range local.values {
		if pkg.names[name] {
			continue
		}
		merged.values[name] = value
	}
	return merged
}

// r12FunctionBindings is the only supported way to build the bindings for a
// declared function: it collects the receiver, the signature and the body as one
// scope, so a parameter can never be missing from the function's name set.
func r12FunctionBindings(pkg r12StringBindings, function *ast.FuncDecl) r12StringBindings {
	return r12ScopedBindings(pkg, r12CollectStringBindings(function))
}

// r12ValueBindings scopes a package-level declaration's value. It matters for a
// package-level `var mutateFn = func(session *gocql.Session, query string) {...}`:
// a function literal's parameters and locals bind names exactly as a declared
// function's do, so scanning such a body against the package scope alone would
// resolve a shadowed name to the package value.
func r12ValueBindings(pkg r12StringBindings, value ast.Expr) r12StringBindings {
	return r12ScopedBindings(pkg, r12CollectStringBindings(value))
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
		value, ok := bindings.resolve(argument.Name)
		return value, ok
	}
	return "", false
}

type r12CQLStatement struct {
	table string
	verb  string
}

// r12CQLStatements lists the mutating statements a query contains, in source
// order, with the relation each one addresses.
func r12CQLStatements(query string) []r12CQLStatement {
	query = r12StripCQLStringLiterals(r12StripCQLComments(query))
	statements := []r12CQLStatement{}
	for _, matches := range r12StatementPattern.FindAllStringSubmatch(query, -1) {
		table := r12NormalizeCQLIdentifier(matches[2])
		if matches[3] != "" {
			// The reference was keyspace-qualified; the table is the second
			// component.
			table = r12NormalizeCQLIdentifier(matches[3])
		}
		statements = append(statements, r12CQLStatement{
			table: table,
			verb:  strings.ToUpper(strings.Fields(matches[1])[0]),
		})
	}
	return statements
}

func r12TargetStatement(query string) (table, statement string, ok bool) {
	if !r12ConditionalPattern.MatchString(r12StripCQLStringLiterals(r12StripCQLComments(query))) {
		return "", "", false
	}
	// Every mutating statement in the query is examined, not just the first, so
	// a target table cannot be hidden behind a leading out-of-scope one.
	for _, candidate := range r12CQLStatements(query) {
		if !r12TargetTables[candidate.table] {
			continue
		}
		return candidate.table, candidate.verb, true
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
// TestR12AllowedBatchCASStatementsStayOutOfScope reads the real source of every
// allowlisted conditional batch and proves the property the allowance depends
// on, rather than asserting it in a comment: each statement is added with an
// inline literal through a CQL entry point the scanner reads, every relation
// named is one the entry is allowed to touch, and the batch neither uses
// Batch.Bind, builds BatchEntry values by hand, or accesses Batch.Entries
// directly. Scanner coverage of Bind and BatchEntry is the general defense;
// this is the specific one, so a change to the allowlisted helper has to pass
// both.
func TestR12AllowedBatchCASStatementsStayOutOfScope(t *testing.T) {
	found := map[string]bool{}

	r12WalkProductionFiles(t, func(fset *token.FileSet, path string, file *ast.File) {
		for _, declaration := range file.Decls {
			for symbol, node := range r12DeclaredSymbols(declaration) {
				shape, tracked := r12AllowedBatchCASShape[symbol]
				if !tracked {
					continue
				}
				found[symbol] = true

				statements := 0
				if selector := r12DirectBatchEntriesSelector(node); selector != nil {
					t.Errorf(
						"%s: allowlisted batch %s accesses .Entries directly at %s; the pinned shape only permits inline Batch.Query statements",
						path,
						symbol,
						fset.Position(selector.Pos()),
					)
				}
				ast.Inspect(node, func(current ast.Node) bool {
					if identifier, ok := current.(*ast.Ident); ok && identifier.Name == "BatchEntry" {
						t.Errorf("%s: allowlisted batch %s builds a BatchEntry at %s; its statements are no longer readable from the call site", path, symbol, fset.Position(identifier.Pos()))
						return true
					}
					call, ok := current.(*ast.CallExpr)
					if !ok {
						return true
					}
					selector, ok := call.Fun.(*ast.SelectorExpr)
					if !ok || !r12IsCQLEntryPoint(selector.Sel.Name, len(call.Args)) {
						return true
					}
					statements++
					if selector.Sel.Name != "Query" {
						t.Errorf("%s: allowlisted batch %s adds a statement with %s at %s; the allowance is justified by the Batch.Query rule alone", path, symbol, selector.Sel.Name, fset.Position(call.Pos()))
						return true
					}
					literal, ok := call.Args[0].(*ast.BasicLit)
					if !ok || literal.Kind != token.STRING {
						t.Errorf("%s: allowlisted batch %s has a non-literal statement at %s; the allowance cannot prove it stays out of the R12 target set", path, symbol, fset.Position(call.Pos()))
						return true
					}
					value, unquoteErr := strconv.Unquote(literal.Value)
					if unquoteErr != nil {
						t.Errorf("%s: allowlisted batch %s has an unparsable statement literal at %s", path, symbol, fset.Position(call.Pos()))
						return true
					}
					relations := r12CQLStatements(value)
					if len(relations) == 0 {
						t.Errorf("%s: allowlisted batch %s has a statement at %s that names no relation the matcher recognises", path, symbol, fset.Position(call.Pos()))
						return true
					}
					for _, relation := range relations {
						if !shape.tables[relation.table] {
							t.Errorf("%s: allowlisted batch %s touches %s at %s, which its allowance does not cover", path, symbol, relation.table, fset.Position(call.Pos()))
						}
						if r12TargetTables[relation.table] {
							t.Errorf("%s: allowlisted batch %s touches R12 target table %s at %s", path, symbol, relation.table, fset.Position(call.Pos()))
						}
					}
					return true
				})

				if statements != shape.statements {
					t.Errorf("%s: allowlisted batch %s adds %d statements, want %d; re-check what the batch does before widening the shape", path, symbol, statements, shape.statements)
				}
			}
		}
	})

	for symbol := range r12AllowedBatchCASShape {
		if !found[symbol] {
			t.Errorf("pinned batch shape %s no longer exists in production sources; drop the stale entry", symbol)
		}
	}

	// A new batch allowance cannot be added without the proof that makes it
	// sound.
	for symbol, allowance := range r12AllowedBatchCAS {
		if _, pinned := r12AllowedBatchCASShape[symbol]; !pinned {
			t.Errorf("conditional batch %s is allowlisted (%s) but has no pinned statement shape; add one so the allowance proves what it claims", symbol, allowance.reason)
		}
	}
}

// TestR12AllowedBatchCASShapeRejectsDirectEntries is the synthetic mutation for
// the allowlisted batch proof. The general scanner deliberately does not model
// a statement assigned through the driver's exported Entries slice, so this
// mutation must be rejected by the shape gate instead.
func TestR12AllowedBatchCASShapeRejectsDirectEntries(t *testing.T) {
	source := `package p

var relocateLockRowCASFn = func(session S) {
	batch := session.Batch(gocql.LoggedBatch)
	batch.Query("INSERT INTO locked_files (repo_id, path) VALUES (?, ?) IF NOT EXISTS")
	batch.Query("DELETE FROM locked_files WHERE repo_id = ? AND path = ? IF locked_by = ?")
	batch.Entries[0].Stmt = "UPDATE blocks SET gc_state = ? WHERE org_id = ? IF gc_state = ?"
	session.MapExecuteBatchCAS(batch, nil)
}
`

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "synthetic.go", source, 0)
	if err != nil {
		t.Fatalf("parse synthetic source: %v", err)
	}
	var node ast.Node
	for _, declaration := range file.Decls {
		if candidate := r12DeclaredSymbols(declaration)["relocateLockRowCASFn"]; candidate != nil {
			node = candidate
			break
		}
	}
	if node == nil {
		t.Fatal("synthetic relocateLockRowCASFn was not found")
	}
	if selector := r12DirectBatchEntriesSelector(node); selector == nil {
		t.Fatal("direct .Entries mutation was not rejected by the batch shape predicate")
	} else if got := fset.Position(selector.Pos()).Line; got == 0 {
		t.Fatal("direct .Entries mutation has no source position")
	}
}

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
			discovered, unresolved, batchCAS := r12ScanSyntheticSource(t, test.source)

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

// TestR12ScanNodeFailsClosedOnShadowedBindings is the regression for the fourth
// way a target statement can leave the guard's view: not by hiding its CQL and
// not by an unrecognised terminal, but by having the guard resolve its
// identifier to the wrong binding. Go resolves a name by lexical scope; the
// resolver here does not model scopes, so every name that is bound in more than
// one of them has to be answered with "unresolvable" instead of a guess.
//
// The two dangerous directions are both covered below. A parameter or a
// dynamically built local must not unmask the package const it shadows -- that
// reads a harmless SELECT while the caller's real LWT runs. And a local
// declared inside a block must not hide a package const that is itself an R12
// LWT -- that removes a genuine target from the set. Either way the guard would
// report nothing at all, which is the false green it exists to prevent.
func TestR12ScanNodeFailsClosedOnShadowedBindings(t *testing.T) {
	tests := []struct {
		name                string
		source              string
		wantDiscovered      bool
		wantUnresolved      bool
		wantUnresolvedCount int
	}{
		{
			// The caller decides what this parameter holds, so the package const
			// of the same name says nothing about the statement that runs.
			name: "parameter shadowing a package const is unresolvable",
			source: `package p

const stmt = "SELECT id FROM libraries"

func mutate(session S, stmt string) {
	session.Query(stmt).Exec()
}
`,
			wantUnresolved:      true,
			wantUnresolvedCount: 1,
		},
		{
			// The local is poisoned because it is built at run time. Poisoning
			// has to shadow the package binding rather than remove the name and
			// let the const show through again.
			name: "dynamically built local shadowing a package const is unresolvable",
			source: `package p

const stmt = "SELECT id FROM libraries"

func mutate(session S, table string) {
	stmt := fmt.Sprintf("UPDATE %s SET gc_state = ? WHERE org_id = ? IF gc_state = ?", table)
	session.Query(stmt).Exec()
}
`,
			wantUnresolved:      true,
			wantUnresolvedCount: 1,
		},
		{
			// The shadowing runs the other way here: the package const is a real
			// R12 LWT and the inner SELECT only exists inside the if. Resolving
			// the trailing call to the inner literal would drop a genuine target
			// from the set, so both call sites are reported instead.
			name: "inner-block local does not hide a package-level R12 LWT",
			source: `package p

const stmt = "UPDATE blocks SET gc_state = ? WHERE org_id = ? IF gc_state = ?"

func mutate(session S, flag bool) {
	if flag {
		stmt := "SELECT id FROM libraries"
		session.Query(stmt).Exec()
	}

	session.Query(stmt).Exec()
}
`,
			wantUnresolved:      true,
			wantUnresolvedCount: 2,
		},
		{
			name: "closure parameter shadowing an enclosing local is unresolvable",
			source: `package p

func mutate(session S, run func(func(string))) {
	query := "SELECT id FROM libraries"
	_ = query
	run(func(query string) {
		session.Query(query).Exec()
	})
}
`,
			wantUnresolved:      true,
			wantUnresolvedCount: 1,
		},
		{
			name: "range variable shadowing a package const is unresolvable",
			source: `package p

const stmt = "SELECT id FROM libraries"

func mutate(session S, statements []string) {
	for _, stmt := range statements {
		session.Query(stmt).Exec()
	}
}
`,
			wantUnresolved:      true,
			wantUnresolvedCount: 1,
		},
		{
			// The package-level function literal is the shape the 17 protected
			// statements are actually written in, so the scope rules have to
			// hold there and not only inside declared functions.
			name: "function-literal parameter shadowing a package const is unresolvable",
			source: `package p

const stmt = "SELECT id FROM libraries"

var mutateFn = func(session S, stmt string) {
	session.Query(stmt).Exec()
}
`,
			wantUnresolved:      true,
			wantUnresolvedCount: 1,
		},
		{
			name: "function-literal local still resolves",
			source: `package p

var mutateFn = func(session S) {
	stmt := "UPDATE blocks SET gc_state = ? WHERE org_id = ? IF gc_state = ?"
	session.Query(stmt).MapScanCAS(nil)
}
`,
			wantDiscovered: true,
		},
		{
			// The counterweight: shadowing is not an excuse to stop resolving.
			// A package const nothing shadows still has to be read, or the
			// fail-closed rules above would degrade into an allowlist of
			// everything.
			name: "unshadowed package const still resolves inside a function with parameters",
			source: `package p

const updateBlock = "UPDATE blocks SET gc_state = ? WHERE org_id = ? IF gc_state = ?"

func mutate(session S, orgID string, stmt string) {
	session.Query(updateBlock).MapScanCAS(nil)
}
`,
			wantDiscovered: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			discovered, unresolved, batchCAS := r12ScanSyntheticSource(t, test.source)

			if got := len(discovered) > 0; got != test.wantDiscovered {
				t.Errorf("discovered target statement = %v, want %v (discovered=%v)", got, test.wantDiscovered, discovered)
			}
			if got := len(unresolved) > 0; got != test.wantUnresolved {
				t.Errorf("recorded unresolvable CAS = %v, want %v (unresolved=%v)", got, test.wantUnresolved, unresolved)
			}
			if test.wantUnresolvedCount > 0 {
				total := 0
				for _, positions := range unresolved {
					total += len(positions)
				}
				if total != test.wantUnresolvedCount {
					t.Errorf("unresolvable CAS call sites = %d, want %d (unresolved=%v)", total, test.wantUnresolvedCount, unresolved)
				}
			}
			if len(batchCAS) > 0 {
				t.Errorf("recorded conditional batch CAS = %v, want none", batchCAS)
			}
		})
	}
}

// TestR12ScanNodeFailsClosedOnPackageVarCQL is the regression for a binding that
// looks resolvable and is not. A package `const` is immutable, so its literal is
// what every call site sees; a package `var` can be reassigned by any function
// in any file of the package, including one this gate is not looking at when it
// classifies the call site. Reading a var's declaration would therefore let
//
//	var stmt = "SELECT id FROM libraries"
//
// stand in for whatever a reassignment put there -- an unpinned LWT on blocks,
// for instance -- and the gate would report nothing at all.
func TestR12ScanNodeFailsClosedOnPackageVarCQL(t *testing.T) {
	tests := []struct {
		name           string
		source         string
		wantDiscovered bool
		wantUnresolved bool
	}{
		{
			name: "package var reassigned elsewhere is unresolvable",
			source: `package p

var stmt = "SELECT id FROM libraries"

func replace() {
	stmt = "UPDATE blocks SET gc_state = ? WHERE org_id = ? IF gc_state = ?"
}

func mutate(session S) {
	session.Query(stmt).Exec()
}
`,
			wantUnresolved: true,
		},
		{
			// No reassignment is visible here, and it still does not resolve:
			// the reassignment may live in another file of the package, in an
			// init function, or behind `someHelper(&stmt)`. Proving a package
			// var never changes is not this gate's job.
			name: "package var is unresolvable even with no visible reassignment",
			source: `package p

var stmt = "UPDATE blocks SET gc_state = ? WHERE org_id = ? IF gc_state = ?"

func mutate(session S) {
	session.Query(stmt).MapScanCAS(nil)
}
`,
			wantUnresolved: true,
		},
		{
			// The counterweight: a const is immutable, so it still resolves and
			// is still held to the pin.
			name: "package const still resolves",
			source: `package p

const stmt = "UPDATE blocks SET gc_state = ? WHERE org_id = ? IF gc_state = ?"

func mutate(session S) {
	session.Query(stmt).SerialConsistency(gocql.Serial).MapScanCAS(nil)
}
`,
			wantDiscovered: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			discovered, unresolved, _ := r12ScanSyntheticSource(t, test.source)

			if got := len(discovered) > 0; got != test.wantDiscovered {
				t.Errorf("discovered target statement = %v, want %v (discovered=%v)", got, test.wantDiscovered, discovered)
			}
			if got := len(unresolved) > 0; got != test.wantUnresolved {
				t.Errorf("recorded unresolvable CAS = %v, want %v (unresolved=%v)", got, test.wantUnresolved, unresolved)
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
			discovered, unresolved, batchCAS := r12ScanSyntheticSource(t, test.source)

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

// TestR12ScanNodeSeesBatchStatementsAddedWithoutQuery covers the way into a
// batch that is not Batch.Query. The conditional-batch allowance is explicitly
// justified by "the general Query rule still reads every Batch.Query statement",
// so an entry point that is not Batch.Query is exactly the shape that makes the
// allowance unsound: the CAS terminal stays allowlisted while the statement is
// never classified. gocql v2 has two such entry points -- Batch.Bind(stmt,
// binding), which appends its own BatchEntry, and the exported Batch.Entries
// slice of exported BatchEntry values.
func TestR12ScanNodeSeesBatchStatementsAddedWithoutQuery(t *testing.T) {
	tests := []struct {
		name           string
		source         string
		wantDiscovered bool
		wantUnresolved bool
		wantBatchCAS   bool
	}{
		{
			name: "Batch.Bind with an inline literal is discovered",
			source: `package p

func mutate(session S, binder B) {
	batch := session.Batch(gocql.LoggedBatch)
	batch.Bind("UPDATE blocks SET gc_state = ? WHERE org_id = ? IF gc_state = ?", binder)
	session.MapExecuteBatchCAS(batch, nil)
}
`,
			wantDiscovered: true,
			wantBatchCAS:   true,
		},
		{
			name: "Batch.Bind with non-literal CQL fails closed",
			source: `package p

func mutate(session S, stmt string, binder B) {
	batch := session.Batch(gocql.LoggedBatch)
	batch.Bind(stmt, binder)
	session.MapExecuteBatchCAS(batch, nil)
}
`,
			wantUnresolved: true,
			wantBatchCAS:   true,
		},
		{
			// The discriminator has to leave ordinary one-argument Bind alone --
			// gin's c.Bind(&payload) is not a CQL call site, and a gate that
			// demanded an allowlist entry for it would be abandoned.
			name: "single-argument Bind is not a CQL call site",
			source: `package p

func handler(c C, payload *P) {
	_ = c.Bind(payload)
}
`,
		},
		{
			name: "hand-built BatchEntry fails closed",
			source: `package p

func mutate(session S, cql string) {
	batch := session.Batch(gocql.LoggedBatch)
	batch.Entries = append(batch.Entries, gocql.BatchEntry{Stmt: cql})
	session.MapExecuteBatchCAS(batch, nil)
}
`,
			wantUnresolved: true,
			wantBatchCAS:   true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			discovered, unresolved, batchCAS := r12ScanSyntheticSource(t, test.source)

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
			discovered, unresolved, _ := r12ScanSyntheticSource(t, test.source)

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
			discovered, unresolved, _ := r12ScanSyntheticSource(t, test.source)

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
