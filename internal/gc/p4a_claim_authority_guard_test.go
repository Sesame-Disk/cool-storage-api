package gc

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// P4a's invariants live in CQL text and in one absent function. Neither is protected by
// the type system: `ClaimBlockDelete` keeps compiling after `storage_key` is dropped
// from its IF, and a candidate-derived claim id can be reintroduced as an ordinary
// helper. These guards read the source so that removing an invariant is a red test
// rather than a silent regression.
//
// They are keyed on the CQL, not on the Go call shape, for the reason R12's guard gives:
// a rename or a wrapper must not be able to slip a mutation past the check.

const p4aStoreSource = "store_cassandra.go"

func p4aParseStore(t *testing.T) *ast.File {
	t.Helper()
	source, err := os.ReadFile(p4aStoreSource)
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), p4aStoreSource, source, 0)
	if err != nil {
		t.Fatal(err)
	}
	return file
}

// TestP4ADestructiveMutationsNameTheExactIncarnation is the core guard: every mutation
// that can fence, unfence or destroy a block row must name the physical incarnation and
// the per-attempt owner it is authorized for.
//
// Dropping `storage_key` alone reopens R14 completely — storage keys are minted, so two
// lives of one logical block share a storage_class and differ only in the key.
func TestP4ADestructiveMutationsNameTheExactIncarnation(t *testing.T) {
	file := p4aParseStore(t)

	for _, want := range []struct {
		function string
		query    string
		columns  []string
	}{
		{
			function: "ClaimBlockDelete",
			query:    "UPDATE blocks SET gc_state",
			// gc_* must be null-checked so the claim cannot take a row owned by the
			// upload path's repairing_stub, and cannot materialize a stub row.
			columns: []string{"storage_class = ?", "storage_key = ?", "gc_state = null", "gc_claim_id = null", "gc_claimed_at = null"},
		},
		{
			function: "ReleaseBlockClaim",
			query:    "UPDATE blocks SET gc_state = null",
			columns:  []string{"storage_class = ?", "storage_key = ?", "gc_claim_id = ?", "gc_claimed_at = ?"},
		},
		{
			function: "FinalizeBlockDelete",
			query:    "DELETE FROM blocks",
			columns:  []string{"storage_class = ?", "storage_key = ?", "gc_claim_id = ?", "gc_claimed_at = ?"},
		},
		{
			function: "DeleteBlockGCCandidate",
			query:    "DELETE FROM gc_block_candidates",
			columns:  []string{"storage_class = ?", "storage_key = ?", "candidate_at = ?"},
		},
		{
			function: "advanceBlockGCCandidateAt",
			query:    "UPDATE gc_block_candidates SET candidate_at",
			columns:  []string{"storage_class = ?", "storage_key = ?", "candidate_at = ?"},
		},
		{
			function: "replaceBlockGCCandidateIncarnation",
			query:    "UPDATE gc_block_candidates SET candidate_at",
			columns:  []string{"storage_class = ?", "storage_key = ?", "candidate_at = ?"},
		},
	} {
		fn := findGCFunction(file, want.function)
		if fn == nil {
			t.Errorf("%s not found in %s; P4a's authority binding cannot be verified", want.function, p4aStoreSource)
			continue
		}
		statement, ok := p4aStatementContaining(fn, want.query)
		if !ok {
			t.Errorf("%s no longer issues a %q statement; the guard is now vacuous", want.function, want.query)
			continue
		}
		condition, ok := p4aConditionOf(statement)
		if !ok {
			t.Errorf("%s statement %q has no IF clause: it is an unconditional mutation on a destructive surface", want.function, want.query)
			continue
		}
		for _, column := range want.columns {
			if !strings.Contains(condition, column) {
				t.Errorf("%s statement %q must condition on %q; without it a lifecycle for one incarnation can act on another (R14). Got IF clause: %s", want.function, want.query, column, condition)
			}
		}
	}
}

// TestP4AClaimIDIsNeverDerivedFromCandidateAt fails if the candidate-derived claim id
// comes back in any form.
//
// It was not a naming choice: candidate_at identifies a CANDIDATE, so every concurrent
// attempt on one candidate shared the id, the claim CAS answered "applied" to all of
// them, and any one could release or finalize under another. The replacement must be a
// fresh per-attempt UUID.
func TestP4AClaimIDIsNeverDerivedFromCandidateAt(t *testing.T) {
	for _, name := range []string{"worker.go", "store_cassandra.go", "scanner.go", "gc.go", "queue.go", "store.go"} {
		source, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if strings.Contains(string(source), "blockDeleteClaimID") {
			t.Errorf("%s references blockDeleteClaimID: the candidate-derived claim id is shared by concurrent attempts and must not return", name)
		}
	}

	source, err := os.ReadFile("worker.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), "worker.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	fn := findGCFunction(file, "processBlock")
	if fn == nil {
		t.Fatal("processBlock not found; the per-attempt identity guard is vacuous")
	}
	if !p4aCallsUUIDNew(fn) {
		t.Error("processBlock must mint a fresh uuid per attempt for the claim id; without it two workers on one candidate share ownership of the row")
	}
}

// TestP4ASettlementReadsUseTheSerialDomain guards R20 at the one place it is decidable
// from source: the claim-state read whose ZERO authorizes consuming a candidate.
//
// An ordinary consistency read is never authority to conclude a claim does not exist —
// Cassandra can accept a Paxos proposal the client never learns about. Note this is
// Consistency(gocql.Serial) on a plain SELECT, not SerialConsistency, which configures
// conditional mutations and is ignored for SELECTs.
func TestP4ASettlementReadsUseTheSerialDomain(t *testing.T) {
	file := p4aParseStore(t)

	fn := findGCFunction(file, "settleBlockDeleteClaimState")
	if fn == nil {
		t.Fatal("settleBlockDeleteClaimState not found; ambiguous claims have no serial-domain settlement")
	}
	if !gcQueryMethodHas(fn, "FROM blocks", "Consistency", "Serial") {
		t.Error("settleBlockDeleteClaimState must read at Consistency(gocql.Serial): an ordinary read that misses a committed claim strands the block behind a fence nothing can lift (R20)")
	}

	// ReleaseStaleBlockClaim's observing read decides whether a candidate may be
	// consumed, so it must go through the settling read rather than its own SELECT.
	stale := findGCFunction(file, "ReleaseStaleBlockClaim")
	if stale == nil {
		t.Fatal("ReleaseStaleBlockClaim not found")
	}
	if p4aHasRawBlocksSelect(stale) {
		t.Error("ReleaseStaleBlockClaim must not issue its own SELECT on blocks; its zero authorizes consuming the candidate, so it has to settle in the serial domain (ISSUE-GC-STALE-CLAIM-READ-CONSISTENCY-01)")
	}
}

// TestP4ACandidatesCarryTheExactKeyAndNeverDeriveIt pins both halves of candidate
// authority: the key is persisted, and it is never reconstructed.
//
// Deriving a key from block_id produces a plausible-looking string that belongs to a
// DIFFERENT incarnation, which is worse than having no key at all — it would authorize
// deleting the wrong object rather than failing closed.
func TestP4ACandidatesCarryTheExactKeyAndNeverDeriveIt(t *testing.T) {
	file := p4aParseStore(t)

	for _, want := range []struct{ function, query string }{
		{"EnsureBlockGCCandidate", "INSERT INTO gc_block_candidates"},
		{"upsertBlockGCCandidateProjection", "INSERT INTO gc_block_candidates_by_day"},
		{"moveBlockGCCandidateProjection", "INSERT INTO gc_block_candidates_by_day"},
		{"GetBlockGCCandidate", "FROM gc_block_candidates"},
		{"ListBlockGCCandidatesByDay", "FROM gc_block_candidates_by_day"},
	} {
		fn := findGCFunction(file, want.function)
		if fn == nil {
			t.Errorf("%s not found; candidate incarnation transport cannot be verified", want.function)
			continue
		}
		statement, ok := p4aStatementContaining(fn, want.query)
		if !ok {
			t.Errorf("%s no longer issues a %q statement; the guard is now vacuous", want.function, want.query)
			continue
		}
		if !strings.Contains(statement, "storage_key") {
			t.Errorf("%s statement %q must carry storage_key: a candidate that cannot name its incarnation cannot authorize a delete", want.function, want.query)
		}
	}

	// No key derivation anywhere in the GC package.
	for _, name := range []string{"worker.go", "store_cassandra.go", "scanner.go", "gc.go", "store.go"} {
		source, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, banned := range []string{"StorageKeyForHash", "MintStorageKey", "hashToKey"} {
			if strings.Contains(string(source), banned) {
				t.Errorf("%s references %s: GC must use the persisted locator verbatim and never reconstruct one", name, banned)
			}
		}
	}
}

// p4aStatementContaining returns the CQL literal in fn that contains needle.
func p4aStatementContaining(fn *ast.FuncDecl, needle string) (string, bool) {
	var found string
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		if found != "" {
			return false
		}
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		text, err := strconv.Unquote(literal.Value)
		if err == nil && strings.Contains(text, needle) {
			found = text
		}
		return true
	})
	return found, found != ""
}

// p4aConditionOf extracts the IF clause of a CQL statement.
func p4aConditionOf(statement string) (string, bool) {
	upper := strings.ToUpper(statement)
	index := strings.Index(upper, "\nIF ")
	if index < 0 {
		if index = strings.Index(upper, "\n\t\tIF "); index < 0 {
			// Fall back to any whitespace-delimited IF token.
			for _, marker := range []string{" IF ", "\tIF "} {
				if i := strings.Index(upper, marker); i >= 0 {
					return statement[i:], true
				}
			}
			return "", false
		}
	}
	return statement[index:], true
}

func p4aCallsUUIDNew(fn *ast.FuncDecl) bool {
	found := false
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := selector.X.(*ast.Ident)
		if !ok || pkg.Name != "uuid" {
			return true
		}
		if selector.Sel.Name == "NewString" || selector.Sel.Name == "New" {
			found = true
		}
		return true
	})
	return found
}

func p4aHasRawBlocksSelect(fn *ast.FuncDecl) bool {
	found := false
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		literal, ok := node.(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		text, err := strconv.Unquote(literal.Value)
		if err != nil {
			return true
		}
		upper := strings.ToUpper(text)
		if strings.Contains(upper, "SELECT") && strings.Contains(upper, "FROM BLOCKS") {
			found = true
		}
		return true
	})
	return found
}
