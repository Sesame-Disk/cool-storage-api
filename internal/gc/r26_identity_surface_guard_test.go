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

// R26 source guard: every durable GC statement must name the FULL identity of the
// row it addresses.
//
// This is the counterpart to TestP4ADestructiveMutationsNameTheExactIncarnation,
// one layer out. That guard covers destructive AUTHORITY — which incarnation a
// claim may act on. This one covers durable IDENTITY — that two lives of one
// logical block occupy two different rows in every table that carries GC work.
//
// It has to be a source guard rather than a behavioural test, and that is not a
// shortcut. The invariant lives in CQL string literals: `identity_at` and
// P = (storage_class, storage_key) are PRIMARY KEY columns, so dropping one from
// a WHERE clause still compiles, still runs, and against MockStore still passes
// every behavioural test in the package — the mock keeps its entries apart by Go
// struct keys, not by the CQL. The failure only appears against a real cluster,
// as a delete that silently addresses the wrong row or no row at all. Pinning the
// text is what lets the mutation harness go red here.
//
// The identity columns per table follow migration 018:
//
//	gc_block_candidates         ((org_id, block_id), storage_class, storage_key)
//	gc_block_candidates_by_day  ((candidate_day, bucket), candidate_at, org_id, block_id, storage_class, storage_key)
//	gc_queue                    ((org_id, bucket), queued_at, item_type, item_id, candidate_storage_class, candidate_storage_key, identity_at)
//	gc_pending_items            ((org_id, bucket), item_type, library_id, item_id, candidate_storage_class, candidate_storage_key, identity_at)
//	gc_failed_items             ((org_id), failed_at, item_type, item_id, candidate_storage_class, candidate_storage_key, identity_at)
//	gc_failed_items_by_expiry   ((expiry_day, bucket), expires_at, org_id, failed_at, item_type, item_id, candidate_storage_class, candidate_storage_key, identity_at)
const r26StoreSource = "store_cassandra.go"

// r26MutationSources are every file that writes an R26 table.
//
// The canonical store is not the only one: gc_failed_items_by_expiry is written
// through internal/db, which owns that projection's key so the columns and the
// bucket hash cannot drift apart. A guard that read only store_cassandra.go would
// leave the half of the key that lives one package over unprotected — correct today,
// but nothing would say so if it stopped being correct.
var r26MutationSources = []string{
	r26StoreSource,
	filepath.Join("..", "db", "gc_projection_write_helpers.go"),
}

// r26IdentityColumns lists the columns that make a row addressable, per table.
var r26IdentityColumns = map[string][]string{
	"gc_block_candidates":        {"storage_class", "storage_key"},
	"gc_block_candidates_by_day": {"candidate_at", "storage_class", "storage_key"},
	"gc_queue":                   {"candidate_storage_class", "candidate_storage_key", "identity_at"},
	"gc_pending_items":           {"candidate_storage_class", "candidate_storage_key", "identity_at"},
	"gc_failed_items":            {"candidate_storage_class", "candidate_storage_key", "identity_at"},
	"gc_failed_items_by_expiry":  {"candidate_storage_class", "candidate_storage_key", "identity_at"},
}

// r26SingleRowReaders are the reads that resolve ONE row and hand it to a caller
// that then acts on it. A read like that is as identity-sensitive as a mutation:
// selecting on a prefix means the caller acts on a lifecycle the operator never
// saw. Reads NOT listed here scan a partition on purpose — enumerating a queue,
// listing an org's DLQ, walking a discovery bucket — and demanding a single row's
// identity of them would be wrong.
var r26SingleRowReaders = map[string][]string{
	"failedItemInfoContext":     {"candidate_storage_class", "candidate_storage_key", "identity_at"},
	"RequeueFailedItemContext":  {"candidate_storage_class", "candidate_storage_key", "identity_at"},
	"queueItemPendingInfo":      {"candidate_storage_class", "candidate_storage_key", "identity_at"},
	"QueueItemExists":           {"candidate_storage_class", "candidate_storage_key", "identity_at"},
	"PendingItemExists":         {"candidate_storage_class", "candidate_storage_key", "identity_at"},
	"GetBlockGCCandidateExact":  {"storage_class", "storage_key"},
	"DeleteBlockGCCandidate":    {"storage_class", "storage_key"},
	"advanceBlockGCCandidateAt": {"storage_class", "storage_key"},
}

var r26MutationPattern = regexp.MustCompile(`(?is)\b(?:INSERT\s+INTO|DELETE\s+FROM|UPDATE)\s+(gc_[a-z_]+)`)

// TestR26MutationsNameTheExactIdentity is the unconditional half: NOTHING may
// write or delete a row in an R26 table without naming every column that
// identifies it. There are no exemptions here by design — a mutation addressing a
// prefix is the defect, whatever else it is doing.
func TestR26MutationsNameTheExactIdentity(t *testing.T) {
	checked := 0
	for _, statement := range r26Statements(t) {
		for _, match := range r26MutationPattern.FindAllStringSubmatch(statement, -1) {
			table := strings.ToLower(match[1])
			required, tracked := r26IdentityColumns[table]
			if !tracked {
				continue
			}
			checked++
			insert := strings.Contains(strings.ToUpper(match[0]), "INSERT")
			for _, column := range required {
				// An INSERT names its identity in the column list; a DELETE or UPDATE
				// names it in the predicate. Requiring `column = ?` there matters: a
				// SET clause or a projection of the same name must not satisfy it.
				if insert && r26NamesColumn(statement, column) {
					continue
				}
				if !insert && r26PredicateBinds(statement, column) {
					continue
				}
				t.Errorf("R26 REGRESSION: a mutation on %s does not name %q.\n"+
					"That column is part of the table's PRIMARY KEY, so without it this statement addresses a "+
					"row PREFIX: it can hit a different lifecycle's row than the caller observed, or none at all "+
					"while reporting success. Two incarnations of one logical block would collapse back into one "+
					"row on this surface.\nStatement:\n%s", table, column, statement)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no mutations on R26 tables found in " + r26StoreSource + "; this guard is vacuous")
	}
}

// TestR26SingleRowReadsNameTheExactIdentity is the conditional half: the reads
// that resolve one row for a caller to act on must select on the full identity.
//
// The columns can be spread across several literals in one function — the DLQ and
// pending probes build their predicate in pieces — so this checks the function's
// whole CQL surface rather than a single statement.
func TestR26SingleRowReadsNameTheExactIdentity(t *testing.T) {
	file := r26ParseStore(t)
	for name, required := range r26SingleRowReaders {
		fn := findGCFunction(file, name)
		if fn == nil {
			t.Errorf("%s not found in %s; its identity binding cannot be verified", name, r26StoreSource)
			continue
		}
		surface := r26FunctionCQL(fn)
		if surface == "" {
			t.Errorf("%s issues no CQL any more; this guard is now vacuous", name)
			continue
		}
		for _, column := range required {
			// The predicate, not the projection: `SELECT identity_at ... WHERE
			// <no identity_at>` is exactly the defect, and a whole-statement
			// substring match would call it a pass.
			if r26PredicateBinds(surface, column) {
				continue
			}
			t.Errorf("R26 REGRESSION: %s resolves a single row without selecting on %q.\n"+
				"It hands that row to a caller that acts on it, so selecting on a prefix means acting on a "+
				"lifecycle the caller never observed.\nCQL surface:\n%s", name, column, surface)
		}
	}
}

// TestR26CandidateSettlementAlwaysRetiresItsDiscoveryRow pins the self-heal in
// source, because the shape it prevents has no behavioural signal short of an
// unbounded rediscovery loop.
//
// DeleteBlockGCCandidate performs two statements: a conditional canonical delete
// and an unconditional discovery delete. The discovery delete must NOT sit behind
// an early return on the CAS result. If it does, a settlement whose canonical half
// already applied leaves the projection standing with nothing left able to remove
// it — the scanner rebuilds the same work item forever.
func TestR26CandidateSettlementAlwaysRetiresItsDiscoveryRow(t *testing.T) {
	file := r26ParseStore(t)
	fn := findGCFunction(file, "DeleteBlockGCCandidate")
	if fn == nil {
		t.Fatal("DeleteBlockGCCandidate not found; the R26 self-heal cannot be verified")
	}

	var sawAppliedGuard, returnsEarly bool
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		stmt, ok := node.(*ast.IfStmt)
		if !ok {
			return true
		}
		unary, ok := stmt.Cond.(*ast.UnaryExpr)
		if !ok || unary.Op != token.NOT {
			return true
		}
		ident, ok := unary.X.(*ast.Ident)
		if !ok || ident.Name != "applied" {
			return true
		}
		sawAppliedGuard = true
		for _, inner := range stmt.Body.List {
			if _, isReturn := inner.(*ast.ReturnStmt); isReturn {
				returnsEarly = true
			}
		}
		return true
	})

	if !sawAppliedGuard {
		t.Fatal("DeleteBlockGCCandidate no longer branches on the CAS result; this guard is now vacuous")
	}
	if returnsEarly {
		t.Error("R26 REGRESSION: DeleteBlockGCCandidate returns early when the canonical CAS did not apply, " +
			"skipping the discovery cleanup.\nA discovery row that outlives its candidate is re-enumerated by " +
			"every scan, rebuilds the same work item, and the worker correctly no-ops it — forever, because " +
			"nothing else removes that row. The delete is safe on both paths precisely because the projection " +
			"is keyed by the full identity: it can only name this lifecycle's row.")
	}
	if !r26MentionsIdent(fn, "deleteBlockGCCandidateProjection") {
		t.Error("DeleteBlockGCCandidate no longer retires its discovery row; the R26 self-heal is gone")
	}
}

func r26ParseStore(t *testing.T) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), r26StoreSource, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	return file
}

func r26Statements(t *testing.T) []string {
	t.Helper()
	var statements []string
	for _, source := range r26MutationSources {
		file, err := parser.ParseFile(token.NewFileSet(), source, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", source, err)
		}
		found := 0
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			text, err := strconv.Unquote(literal.Value)
			if err != nil || !strings.Contains(text, "gc_") {
				return true
			}
			statements = append(statements, text)
			found++
			return true
		})
		if found == 0 {
			t.Fatalf("no CQL literals found in %s; this guard no longer covers that writer", source)
		}
	}
	return statements
}

func r26FunctionCQL(fn *ast.FuncDecl) string {
	var parts []string
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		text, err := strconv.Unquote(literal.Value)
		if err != nil {
			return true
		}
		if !strings.Contains(text, "gc_") && !strings.Contains(text, "identity_at") && !strings.Contains(text, "storage_") {
			return true
		}
		parts = append(parts, text)
		return true
	})
	return strings.Join(parts, "\n")
}

func r26MentionsIdent(fn *ast.FuncDecl, name string) bool {
	found := false
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		if ident, ok := node.(*ast.Ident); ok && ident.Name == name {
			found = true
		}
		return true
	})
	return found
}

// r26NamesColumn reports whether a statement mentions the column as a whole word,
// so `storage_class` cannot be satisfied by `candidate_storage_class`.
func r26NamesColumn(statement, column string) bool {
	return regexp.MustCompile(`(?i)(^|[^a-z_])` + regexp.QuoteMeta(column) + `([^a-z_]|$)`).MatchString(statement)
}

// r26PredicateBinds reports whether the statement CONSTRAINS the column — i.e.
// `column = ?` appears as a whole-word predicate — rather than merely mentioning
// it somewhere, which a SELECT projection list would do.
func r26PredicateBinds(statement, column string) bool {
	return regexp.MustCompile(`(?i)(^|[^a-z_])` + regexp.QuoteMeta(column) + `\s*=\s*\?`).MatchString(statement)
}

// r26MutatingStoreMethods are the store methods that WRITE or DELETE a durable
// row. None of them may be handed the "any lifecycle" probe.
var r26MutatingStoreMethods = map[string]bool{
	"CompleteItem":             true,
	"RequeueItem":              true,
	"DeleteFailedItem":         true,
	"DeleteFailedItemContext":  true,
	"RequeueFailedItem":        true,
	"RequeueFailedItemContext": true,
	"EnqueueBatch":             true,
	"FailItem":                 true,
}

// TestR26AnyIdentityIsNeverPassedToAMutation closes the last place where the
// identity contract rests on convention rather than on a check.
//
// AnyGCItemIdentity() is the deliberate "is this item pending under ANY
// lifecycle" probe: it carries a zero identity_at and no P, and PendingItemExists
// answers it by omitting the identity_at predicate. That is correct for a dedup
// read. Handed to a mutation it is the opposite of correct — the row addressed
// would be resolved from a fallback rather than named, which is the prefix
// addressing every other gate here forbids.
//
// Nothing in production does this today; the type system cannot say so, because
// AnyGCItemIdentity returns the same type a mutation legitimately takes. This
// gate says it instead.
func TestR26AnyIdentityIsNeverPassedToAMutation(t *testing.T) {
	fset := token.NewFileSet()
	pkg, err := parser.ParseDir(fset, ".", func(info os.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse package: %v", err)
	}

	checked := 0
	for _, files := range pkg {
		for path, file := range files.Files {
			ast.Inspect(file, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				selector, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || !r26MutatingStoreMethods[selector.Sel.Name] {
					return true
				}
				checked++
				for _, arg := range call.Args {
					if r26IsAnyIdentityCall(arg) {
						t.Errorf("R26 REGRESSION: %s is passed AnyGCItemIdentity() at %s.\n"+
							"That value carries no identity_at and no P, so the row this mutation addresses is "+
							"resolved from a fallback instead of named — a prefix write or delete. It is the "+
							"dedup probe, and it belongs only in PendingItemExists.",
							selector.Sel.Name, fset.Position(arg.Pos()))
					}
				}
				return true
			})
			_ = path
		}
	}
	if checked == 0 {
		t.Fatal("no calls to any mutating store method were found; this guard is vacuous")
	}
}

func r26IsAnyIdentityCall(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name == "AnyGCItemIdentity"
	case *ast.SelectorExpr:
		return fn.Sel.Name == "AnyGCItemIdentity"
	}
	return false
}

// TestR26CandidateAuthorityReadUsesTheSerialDomain pins the read whose ABSENCE
// authorizes retiring a whole lifecycle.
//
// GetBlockGCCandidateExact answering "not found" is what sends processBlock down the
// self-heal path: it deletes the discovery row and then completes the queue row and
// its pending marker. Those are the only durable references to the candidate — the
// scanner walks gc_block_candidates_by_day and nothing enumerates the canonical
// table — so if that answer can be produced by a lagging replica rather than by the
// candidate really being gone, one stale read strands a live candidate forever and
// the block behind it is never reclaimed. Every write to gc_block_candidates is an
// LWT, so an ordinary quorum read can miss an accepted-but-not-yet-committed row.
//
// Like the two P4a serial-domain guards, this is decidable only from source: in a
// single-node test cluster an ordinary read and a serial read return the same thing,
// so no behavioural test — mock or real — can tell them apart, and the downgrade
// would stay green everywhere while removing the guarantee.
//
// Note this is Consistency(gocql.Serial) on a plain SELECT, not SerialConsistency,
// which configures conditional mutations and is ignored for SELECTs.
func TestR26CandidateAuthorityReadUsesTheSerialDomain(t *testing.T) {
	file := p4aParseStore(t)

	fn := findGCFunction(file, "GetBlockGCCandidateExact")
	if fn == nil {
		t.Fatal("GetBlockGCCandidateExact not found; the candidate authority read cannot be verified")
	}
	if !gcQueryMethodHas(fn, "FROM gc_block_candidates", "Consistency", "Serial") {
		t.Error("GetBlockGCCandidateExact must read at Consistency(gocql.Serial): its \"not found\" answer " +
			"deletes the discovery row and completes the queue and pending rows, so an ordinary read that " +
			"misses an LWT-written candidate strands it with no durable reference left to rediscover it")
	}
}

