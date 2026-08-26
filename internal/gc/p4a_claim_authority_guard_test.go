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

// TestP4ADestructiveStepsUseTheClaimsLocator guards the orphan and S3 half of R14.
//
// FinalizeBlockDelete was always claim-bound, but the two steps that actually touch bytes
// took their locator from a post-claim GetBlockInfo re-read — an ordinary read, while the
// claim commits in the serial domain. This fails if either one goes back to sourcing its
// locator from the read rather than from the authority the claim established.
func TestP4ADestructiveStepsUseTheClaimsLocator(t *testing.T) {
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
		t.Fatal("processBlock not found; the destructive-locator guard is vacuous")
	}

	// The locator variables must be assigned from the claim's authority.
	var fromAttempt int
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		assign, ok := node.(*ast.AssignStmt)
		if !ok || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
			return true
		}
		name, ok := assign.Lhs[0].(*ast.Ident)
		if !ok || (name.Name != "storageKey" && name.Name != "storageClass") {
			return true
		}
		if p4aRendersAs(assign.Rhs[0], "attempt.Target.") {
			fromAttempt++
		}
		return true
	})
	if fromAttempt < 2 {
		t.Errorf("processBlock must take storageClass and storageKey from attempt.Target (found %d of 2); sourcing them from a post-claim re-read publishes and deletes whichever incarnation that read happens to show (R14)", fromAttempt)
	}

	// And the divergence between the claim and the read-back must abort, not be ignored.
	if !p4aMentions(fn, "block_incarnation_divergence") {
		t.Error("processBlock must refuse when the canonical row reads back as a different incarnation than the claim authorized")
	}
}

// TestP4AStaleTakeoverUsesTheObservedAuthority fails if the takeover goes back to
// re-reading the row and releasing whoever it finds.
//
// That shape is the reason this guard exists: between the claim's observation and the
// release the row can hold a different incarnation with its own stale owner, and
// releasing THAT is a worker authorized for P1 dropping P2's fence.
func TestP4AStaleTakeoverUsesTheObservedAuthority(t *testing.T) {
	source, err := os.ReadFile("worker.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), "worker.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	fn := findGCFunction(file, "takeOverStaleBlockClaim")
	if fn == nil {
		t.Fatal("takeOverStaleBlockClaim not found; the takeover-authority guard is vacuous")
	}
	if p4aMentions(fn, "ReleaseStaleBlockClaim") {
		t.Error("takeOverStaleBlockClaim must not call ReleaseStaleBlockClaim: that re-reads the row and adopts whatever owner is there, which can belong to another incarnation entirely")
	}
	if !p4aMentions(fn, "ReleaseBlockClaim") {
		t.Error("takeOverStaleBlockClaim must CAS against the exact authority the claim observed, via ReleaseBlockClaim")
	}
}

// TestP4AStaleClaimReleaseNamesAnIncarnation guards the OTHER caller: the pre-check path
// is deliberately owner-agnostic, but it must still name the incarnation its candidate
// authorizes, or a candidate for P1 can hand back a fence belonging to P2.
func TestP4AStaleClaimReleaseNamesAnIncarnation(t *testing.T) {
	file := p4aParseStore(t)
	fn := findGCFunction(file, "ReleaseStaleBlockClaim")
	if fn == nil {
		t.Fatal("ReleaseStaleBlockClaim not found")
	}
	var named bool
	for _, param := range fn.Type.Params.List {
		if ident, ok := param.Type.(*ast.Ident); ok && ident.Name == "BlockDeleteTarget" {
			named = true
		}
	}
	if !named {
		t.Error("ReleaseStaleBlockClaim must take the caller's expected BlockDeleteTarget: age is not authority over an incarnation the caller was never given")
	}
	if !p4aMentions(fn, "expectedTarget") {
		t.Error("ReleaseStaleBlockClaim must compare the observed incarnation against expectedTarget")
	}
}

// TestP4ACandidateCaptureUsesTheSerialDomain: the read that decides whether an existing
// candidate is REPLACED must be linearizable.
//
// A lagging read can move a candidate backward — canonical row already at P2, candidate
// correctly at P2, stale read returns P1, the two differ, replacement fires — and the
// worker then claims P1, gets TargetChanged, settles the candidate, and leaves P2 with no
// work item at all.
func TestP4ACandidateCaptureUsesTheSerialDomain(t *testing.T) {
	file := p4aParseStore(t)
	fn := findGCFunction(file, "resolveBlockDeleteTarget")
	if fn == nil {
		t.Fatal("resolveBlockDeleteTarget not found; candidate capture cannot be verified")
	}
	if !gcQueryMethodHas(fn, "FROM blocks", "Consistency", "Serial") {
		t.Error("resolveBlockDeleteTarget must read at Consistency(gocql.Serial): a lagging read here silently replaces a correct candidate with a dead incarnation and loses the live one's work item")
	}
}

// TestP4ACandidateRetryLoopIsBounded: every CAS in EnsureBlockGCCandidate names a value
// read moments earlier, so genuine contention converges. An unbounded loop only stays
// safe while every not-applied outcome is transient, and one is not — a CAS naming a
// value that can never match spins forever, one Paxos round per iteration, silently.
func TestP4ACandidateRetryLoopIsBounded(t *testing.T) {
	file := p4aParseStore(t)
	fn := findGCFunction(file, "EnsureBlockGCCandidate")
	if fn == nil {
		t.Fatal("EnsureBlockGCCandidate not found")
	}
	var unbounded bool
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		loop, ok := node.(*ast.ForStmt)
		if !ok {
			return true
		}
		if loop.Cond == nil {
			unbounded = true
		}
		return true
	})
	if unbounded {
		t.Error("EnsureBlockGCCandidate's CAS retry loop must be bounded; a non-converging condition otherwise becomes a silent Paxos hot loop")
	}
}

func p4aMentions(fn *ast.FuncDecl, needle string) bool {
	found := false
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.Ident:
			if typed.Name == needle {
				found = true
			}
		case *ast.SelectorExpr:
			if typed.Sel.Name == needle {
				found = true
			}
		case *ast.BasicLit:
			if typed.Kind == token.STRING {
				if text, err := strconv.Unquote(typed.Value); err == nil && strings.Contains(text, needle) {
					found = true
				}
			}
		}
		return true
	})
	return found
}

// p4aRendersAs reports whether an expression's source form starts with prefix.
func p4aRendersAs(expr ast.Expr, prefix string) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	inner, ok := selector.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	base, ok := inner.X.(*ast.Ident)
	if !ok {
		return false
	}
	return strings.HasPrefix(base.Name+"."+inner.Sel.Name+".", prefix)
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
