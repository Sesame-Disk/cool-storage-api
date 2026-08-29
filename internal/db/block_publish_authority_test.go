package db

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

func r3ActiveAuthorityRow() publishAttemptAuthorityRow {
	created := time.Unix(1_700_000_000, 0).UTC()
	return publishAttemptAuthorityRow{
		StorageClass: "hot",
		StorageKey:   "blocks/org-1/minted",
		CreatedAt:    &created,
	}
}

func withPublishAuthoritySeams(t *testing.T) {
	t.Helper()
	oldRead := readPublishAttemptAuthorityFn
	oldOrphan := publishAttemptHasS3OrphanFn
	oldValidate := validatePublishAttemptAuthorityFn
	oldFinish := finishCheckedPublishAttemptFn
	oldAdd := addPublishAttemptReferenceFn
	oldRemove := removePublishAttemptReferenceFn
	t.Cleanup(func() {
		readPublishAttemptAuthorityFn = oldRead
		publishAttemptHasS3OrphanFn = oldOrphan
		validatePublishAttemptAuthorityFn = oldValidate
		finishCheckedPublishAttemptFn = oldFinish
		addPublishAttemptReferenceFn = oldAdd
		removePublishAttemptReferenceFn = oldRemove
	})
}

func TestValidatePublishAttemptAuthorityEmptyIsActiveWithZeroReads(t *testing.T) {
	withPublishAuthoritySeams(t)
	readCalls := 0
	readPublishAttemptAuthorityFn = func(*DB, string, string) (publishAttemptAuthorityRow, bool, error) {
		readCalls++
		t.Fatal("empty batch must issue zero canonical reads")
		return publishAttemptAuthorityRow{}, false, nil
	}
	publishAttemptHasS3OrphanFn = func(*DB, string, string) (bool, error) {
		t.Fatal("empty batch must issue zero orphan reads")
		return false, nil
	}

	outcome, err := ValidatePublishAttemptAuthority(&DB{}, "org-1", nil)
	if err != nil || outcome != BlockPublishAuthorityActive {
		t.Fatalf("empty ValidatePublishAttemptAuthority() = %v, %v; want active", outcome, err)
	}
	outcome, err = ValidatePublishAttemptAuthority(&DB{}, "org-1", []string{"", "  "})
	if err != nil || outcome != BlockPublishAuthorityActive {
		t.Fatalf("whitespace-only ValidatePublishAttemptAuthority() = %v, %v; want active", outcome, err)
	}
	if readCalls != 0 {
		t.Fatalf("empty batch issued %d canonical reads, want 0", readCalls)
	}
}

func TestValidatePublishAttemptAuthorityClassifiesWriterFence(t *testing.T) {
	withPublishAuthoritySeams(t)
	claimedAt := time.Unix(1_700_000_100, 0).UTC()
	handoff := true

	tests := []struct {
		name      string
		blockID   string
		row       publishAttemptAuthorityRow
		found     bool
		orphan    bool
		readErr   error
		orphanErr error
		want      BlockPublishAuthorityOutcome
	}{
		{name: "active", blockID: installTestBlockID, row: r3ActiveAuthorityRow(), found: true, want: BlockPublishAuthorityActive},
		{name: "deleting claim", blockID: installTestBlockID, row: func() publishAttemptAuthorityRow {
			row := r3ActiveAuthorityRow()
			row.GCState, row.GCClaimID, row.GCClaimedAt = BlockGCStateDeleting, "delete-1", &claimedAt
			return row
		}(), found: true, want: BlockPublishAuthorityDeleting},
		{name: "committed handoff", blockID: installTestBlockID, row: func() publishAttemptAuthorityRow {
			row := r3ActiveAuthorityRow()
			row.GCOrphanHandoff = &handoff
			return row
		}(), found: true, want: BlockPublishAuthorityDeleting},
		{name: "repairing_stub", blockID: installTestBlockID, row: func() publishAttemptAuthorityRow {
			row := r3ActiveAuthorityRow()
			row.GCState, row.GCClaimID, row.GCClaimedAt = BlockGCStateRepairingStub, "repair-1", &claimedAt
			return row
		}(), found: true, want: BlockPublishAuthorityRepairing},
		{name: "orphan with canonical row", blockID: installTestBlockID, row: r3ActiveAuthorityRow(), found: true, orphan: true, want: BlockPublishAuthorityOrphaned},
		{name: "orphan without canonical row", blockID: installTestBlockID, found: false, orphan: true, want: BlockPublishAuthorityOrphaned},
		{name: "missing", blockID: installTestBlockID, found: false, want: BlockPublishAuthorityMissing},
		{name: "empty storage_key", blockID: installTestBlockID, row: func() publishAttemptAuthorityRow {
			row := r3ActiveAuthorityRow()
			row.StorageKey = ""
			return row
		}(), found: true, want: BlockPublishAuthorityInvalid},
		{name: "non-sha256", blockID: "not-a-sha256", want: BlockPublishAuthorityInvalid},
		{name: "canonical read error", blockID: installTestBlockID, readErr: errors.New("cassandra down"), want: BlockPublishAuthorityUnavailable},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			readPublishAttemptAuthorityFn = func(_ *DB, orgID, blockID string) (publishAttemptAuthorityRow, bool, error) {
				if orgID != "org-1" || blockID != test.blockID {
					t.Fatalf("canonical read %s/%s, want org-1/%s", orgID, blockID, test.blockID)
				}
				return test.row, test.found, test.readErr
			}
			publishAttemptHasS3OrphanFn = func(*DB, string, string) (bool, error) {
				return test.orphan, test.orphanErr
			}
			outcome, err := ValidatePublishAttemptAuthority(&DB{}, "org-1", []string{test.blockID})
			if outcome != test.want {
				t.Fatalf("outcome = %v, want %s", outcome, test.want)
			}
			if test.want == BlockPublishAuthorityActive {
				if err != nil {
					t.Fatalf("active returned error %v", err)
				}
				return
			}
			if !errors.Is(err, ErrBlockPublishAuthorityDenied) {
				t.Fatalf("error = %v, want ErrBlockPublishAuthorityDenied", err)
			}
		})
	}
}

func TestValidatePublishAttemptAuthorityHandoffIsNeverActive(t *testing.T) {
	withPublishAuthoritySeams(t)
	handoff := true
	row := r3ActiveAuthorityRow()
	row.GCOrphanHandoff = &handoff
	readPublishAttemptAuthorityFn = func(*DB, string, string) (publishAttemptAuthorityRow, bool, error) {
		return row, true, nil
	}
	publishAttemptHasS3OrphanFn = func(*DB, string, string) (bool, error) { return false, nil }

	outcome, err := ValidatePublishAttemptAuthority(&DB{}, "org-1", []string{installTestBlockID})
	if outcome != BlockPublishAuthorityDeleting {
		t.Fatalf("handoff=true outcome = %v, want deleting; ignoring handoff must never be treated as Active", outcome)
	}
	if !errors.Is(err, ErrBlockPublishAuthorityDenied) {
		t.Fatalf("error = %v, want ErrBlockPublishAuthorityDenied", err)
	}
}

func TestValidatePublishAttemptAuthorityStopsAtFirstNonActive(t *testing.T) {
	withPublishAuthoritySeams(t)
	second := strings.Repeat("c", 64)
	reads := 0
	readPublishAttemptAuthorityFn = func(_ *DB, _, blockID string) (publishAttemptAuthorityRow, bool, error) {
		reads++
		if blockID == installTestBlockID {
			return r3ActiveAuthorityRow(), true, nil
		}
		return publishAttemptAuthorityRow{}, false, nil
	}
	publishAttemptHasS3OrphanFn = func(*DB, string, string) (bool, error) { return false, nil }

	outcome, err := ValidatePublishAttemptAuthority(&DB{}, "org-1", []string{installTestBlockID, second, strings.Repeat("d", 64)})
	if outcome != BlockPublishAuthorityMissing {
		t.Fatalf("outcome = %v, want missing on the first non-Active", outcome)
	}
	if !errors.Is(err, ErrBlockPublishAuthorityDenied) {
		t.Fatalf("error = %v, want ErrBlockPublishAuthorityDenied", err)
	}
	if reads != 2 {
		t.Fatalf("canonical reads = %d, want 2 (stop at the first non-Active)", reads)
	}
}

func TestValidatePublishAttemptAuthorityReadsCanonicalRowBeforeOrphan(t *testing.T) {
	withPublishAuthoritySeams(t)
	var events []string
	readPublishAttemptAuthorityFn = func(*DB, string, string) (publishAttemptAuthorityRow, bool, error) {
		events = append(events, "canonical")
		return r3ActiveAuthorityRow(), true, nil
	}
	publishAttemptHasS3OrphanFn = func(*DB, string, string) (bool, error) {
		events = append(events, "orphan")
		return false, nil
	}
	if _, err := ValidatePublishAttemptAuthority(&DB{}, "org-1", []string{installTestBlockID}); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0] != "canonical" || events[1] != "orphan" {
		t.Fatalf("read order = %#v, want canonical then orphan", events)
	}
}

func TestFinishCheckedPublishAttemptRollsBackThisAttemptOnly(t *testing.T) {
	withPublishAuthoritySeams(t)
	validatePublishAttemptAuthorityFn = func(*DB, string, []string) (BlockPublishAuthorityOutcome, error) {
		return BlockPublishAuthorityDeleting, fmtR3Denied("deleting")
	}
	var removed []string
	removePublishAttemptReferenceFn = func(database *DB, orgID, blockID, referrer string) error {
		if orgID != "org-1" || referrer != BlockReferrerForPublishAttempt("attempt-1") {
			t.Fatalf("rollback referrer = %s/%s, want org-1/%s", orgID, referrer, BlockReferrerForPublishAttempt("attempt-1"))
		}
		removed = append(removed, blockID)
		return nil
	}

	err := FinishCheckedPublishAttempt(&DB{}, "org-1", "repo-1", "attempt-1", []string{installTestBlockID})
	if !errors.Is(err, ErrBlockPublishAuthorityDenied) {
		t.Fatalf("FinishCheckedPublishAttempt() = %v, want ErrBlockPublishAuthorityDenied", err)
	}
	if len(removed) != 1 || removed[0] != installTestBlockID {
		t.Fatalf("removed = %#v, want this attempt's staged ids only", removed)
	}
}

func TestFinishCheckedPublishAttemptRollbackFailureIsNeverSuccess(t *testing.T) {
	withPublishAuthoritySeams(t)
	validatePublishAttemptAuthorityFn = func(*DB, string, []string) (BlockPublishAuthorityOutcome, error) {
		return BlockPublishAuthorityMissing, fmtR3Denied("missing")
	}
	rollbackErr := errors.New("remove boom")
	removePublishAttemptReferenceFn = func(*DB, string, string, string) error {
		return rollbackErr
	}

	err := FinishCheckedPublishAttempt(&DB{}, "org-1", "repo-1", "attempt-1", []string{installTestBlockID})
	if err == nil {
		t.Fatal("rollback failure must never be treated as publication success")
	}
	if !errors.Is(err, ErrBlockPublishAuthorityDenied) {
		t.Fatalf("error = %v, want ErrBlockPublishAuthorityDenied even when rollback fails", err)
	}
	if !errors.Is(err, rollbackErr) {
		t.Fatalf("error = %v, want joined rollback error", err)
	}
}

func TestFinishCheckedPublishAttemptActiveDoesNotRollback(t *testing.T) {
	withPublishAuthoritySeams(t)
	validatePublishAttemptAuthorityFn = func(*DB, string, []string) (BlockPublishAuthorityOutcome, error) {
		return BlockPublishAuthorityActive, nil
	}
	removePublishAttemptReferenceFn = func(*DB, string, string, string) error {
		t.Fatal("Active must not roll back pub: rows")
		return nil
	}
	if err := FinishCheckedPublishAttempt(&DB{}, "org-1", "repo-1", "attempt-1", []string{installTestBlockID}); err != nil {
		t.Fatalf("Active FinishCheckedPublishAttempt() = %v, want nil", err)
	}
}

func TestStagePublishAttemptReferencesChecksAfterCompleteStage(t *testing.T) {
	withPublishAuthoritySeams(t)
	var events []string
	addPublishAttemptReferenceFn = func(*DB, string, string, string, string) error {
		events = append(events, "add")
		return nil
	}
	validatePublishAttemptAuthorityFn = func(*DB, string, []string) (BlockPublishAuthorityOutcome, error) {
		events = append(events, "check")
		return BlockPublishAuthorityActive, nil
	}
	removePublishAttemptReferenceFn = func(*DB, string, string, string) error {
		t.Fatal("Active complete stage must not roll back")
		return nil
	}

	staged, err := StagePublishAttemptReferences(&DB{}, "org-1", "repo-1", "attempt-1", []string{installTestBlockID}, nil)
	if err != nil {
		t.Fatalf("StagePublishAttemptReferences() error = %v, want nil", err)
	}
	if len(staged) != 1 || staged[0] != installTestBlockID {
		t.Fatalf("staged = %#v, want []string{%q}", staged, installTestBlockID)
	}
	if len(events) != 2 || events[0] != "add" || events[1] != "check" {
		t.Fatalf("order = %#v, want add then check; a pre-stage check cannot close the refs==0 race", events)
	}
}

func TestStagePublishAttemptReferencesDeniedRollsBackAfterAdd(t *testing.T) {
	withPublishAuthoritySeams(t)
	addPublishAttemptReferenceFn = func(*DB, string, string, string, string) error { return nil }
	validatePublishAttemptAuthorityFn = func(*DB, string, []string) (BlockPublishAuthorityOutcome, error) {
		return BlockPublishAuthorityDeleting, fmtR3Denied("deleting")
	}
	removed := 0
	removePublishAttemptReferenceFn = func(*DB, string, string, string) error {
		removed++
		return nil
	}

	staged, err := StagePublishAttemptReferences(&DB{}, "org-1", "repo-1", "attempt-1", []string{installTestBlockID}, nil)
	if !errors.Is(err, ErrBlockPublishAuthorityDenied) {
		t.Fatalf("error = %v, want ErrBlockPublishAuthorityDenied", err)
	}
	if staged != nil {
		t.Fatalf("denied stage returned %#v, want nil", staged)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
}

func TestR3FenceReadConsistencyIsLocalQuorum(t *testing.T) {
	if BlockFenceReadConsistency != gocql.LocalQuorum {
		t.Fatalf("BlockFenceReadConsistency = %v, want gocql.LocalQuorum; lowering this to ONE does not intersect an EACH_QUORUM fence publication", BlockFenceReadConsistency)
	}
}

func TestR3CanonicalAuthoritySelectIncludesHandoff(t *testing.T) {
	source, err := os.ReadFile("block_publish_authority.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	selectIdx := strings.Index(text, "SELECT storage_class, storage_key, gc_state, gc_claim_id, gc_claimed_at, gc_orphan_handoff, created_at")
	if selectIdx < 0 {
		t.Fatal("canonical R3 SELECT must include gc_orphan_handoff; ignoring handoff lets a committed delete look Active")
	}
	fromIdx := strings.Index(text[selectIdx:], "FROM blocks")
	if fromIdx < 0 || fromIdx > 400 {
		t.Fatal("canonical R3 SELECT is no longer against blocks")
	}
}

func TestR3PublishAuthorityReadsPinBlockFenceReadConsistency(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "block_publish_authority.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	pinned := 0
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Consistency" || len(call.Args) != 1 {
			return true
		}
		arg, ok := call.Args[0].(*ast.Ident)
		if !ok || arg.Name != "BlockFenceReadConsistency" {
			t.Errorf("R3 read pins %s, want BlockFenceReadConsistency", exprName(call.Args[0]))
			return true
		}
		pinned++
		return true
	})
	if pinned != 2 {
		t.Fatalf("R3 BlockFenceReadConsistency pins = %d, want 2 (canonical + orphan)", pinned)
	}
}

func TestR3StagePublishAttemptReferencesChecksAfterAdd(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "block_references.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var fn *ast.FuncDecl
	for _, decl := range file.Decls {
		function, ok := decl.(*ast.FuncDecl)
		if ok && function.Name.Name == "StagePublishAttemptReferences" {
			fn = function
			break
		}
	}
	if fn == nil {
		t.Fatal("StagePublishAttemptReferences not found")
	}
	addPos := token.NoPos
	checkPos := token.NoPos
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch callName(call) {
		case "addPublishAttemptReferencesRows":
			if addPos == token.NoPos {
				addPos = call.Pos()
			}
		case "FinishCheckedPublishAttempt":
			if checkPos == token.NoPos {
				checkPos = call.Pos()
			}
		}
		return true
	})
	if addPos == token.NoPos || checkPos == token.NoPos {
		t.Fatal("StagePublishAttemptReferences must add pub: rows then FinishCheckedPublishAttempt")
	}
	if checkPos < addPos {
		t.Fatal("R3 check runs before addPublishAttemptReferencesRows; a pre-stage check cannot close the refs==0 race")
	}
}

func TestPromotePublishAttemptReferencesDoesNotRunR3(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "block_references.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range file.Decls {
		function, ok := decl.(*ast.FuncDecl)
		if !ok || function.Name.Name != "PromotePublishAttemptReferences" {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch callName(call) {
			case "ValidatePublishAttemptAuthority", "FinishCheckedPublishAttempt", "validatePublishAttemptAuthority":
				t.Errorf("PromotePublishAttemptReferences calls %s; promote must stay dumb", callName(call))
			}
			return true
		})
		return
	}
	t.Fatal("PromotePublishAttemptReferences not found")
}

func fmtR3Denied(reason string) error {
	return errors.New("block publish authority denied: " + reason)
}

func callName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		return fn.Sel.Name
	default:
		return ""
	}
}

func exprName(expr ast.Expr) string {
	switch node := expr.(type) {
	case *ast.Ident:
		return node.Name
	case *ast.SelectorExpr:
		if ident, ok := node.X.(*ast.Ident); ok {
			return ident.Name + "." + node.Sel.Name
		}
		return node.Sel.Name
	default:
		return "<expr>"
	}
}
