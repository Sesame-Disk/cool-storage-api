package gc

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const x1SourceRootEnv = "X1_SOURCE_ROOT"

func x1SourcePath(parts ...string) string {
	if root := strings.TrimSpace(os.Getenv(x1SourceRootEnv)); root != "" {
		return filepath.Join(append([]string{root}, parts...)...)
	}
	return filepath.Join(append([]string{"..", ".."}, parts...)...)
}

func x1ParseFile(t *testing.T, rel ...string) (*token.FileSet, *ast.File) {
	t.Helper()
	path := x1SourcePath(rel...)
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("X1 HARNESS CONTRACT: parse %s: %v", path, err)
	}
	return fset, parsed
}

func x1Func(t *testing.T, file *ast.File, name string) *ast.FuncDecl {
	t.Helper()
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == name {
			return fn
		}
	}
	t.Fatalf("X1 HARNESS CONTRACT: function %s not found", name)
	return nil
}

func x1CallName(call *ast.CallExpr) string {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return fun.Name
	case *ast.SelectorExpr:
		return fun.Sel.Name
	default:
		return ""
	}
}

func x1FirstCall(t *testing.T, fn *ast.FuncDecl, name string) token.Pos {
	t.Helper()
	var pos token.Pos
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if x1CallName(call) == name && pos == 0 {
			pos = call.Pos()
		}
		return true
	})
	if pos == 0 {
		t.Fatalf("X1 HARNESS CONTRACT: %s must call %s", fn.Name.Name, name)
	}
	return pos
}

func TestX1CandidateHarnessDeletesBeforeFinalize(t *testing.T) {
	_, file := x1ParseFile(t, "internal", "integration", "x1_strict_nonoverlap_characterization_test.go")
	fn := x1Func(t, file, "TestX1StrictNonoverlapCharacterization")
	del := x1FirstCall(t, fn, "DeleteBlockByStorageKey")
	fin := x1FirstCall(t, fn, "FinalizeBlockDelete")
	if !(del < fin) {
		t.Fatal("candidate harness must call DeleteBlockByStorageKey before FinalizeBlockDelete")
	}
}

func x1SubtestBody(t *testing.T, fn *ast.FuncDecl, name string) *ast.BlockStmt {
	t.Helper()
	var body *ast.BlockStmt
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || x1CallName(call) != "Run" || len(call.Args) < 2 {
			return true
		}
		lit, ok := call.Args[0].(*ast.BasicLit)
		if !ok || lit.Value != `"`+name+`"` {
			return true
		}
		if arg, ok := call.Args[1].(*ast.FuncLit); ok {
			body = arg.Body
		}
		return true
	})
	if body == nil {
		t.Fatalf("X1 HARNESS CONTRACT: t.Run(%q) not found", name)
	}
	return body
}

func x1BlockContainsIdent(body *ast.BlockStmt, name string) bool {
	found := false
	ast.Inspect(body, func(node ast.Node) bool {
		ident, ok := node.(*ast.Ident)
		if ok && ident.Name == name {
			found = true
		}
		return true
	})
	return found
}

func TestX1F0b1LocksOnPendingItemsNotQueue(t *testing.T) {
	_, file := x1ParseFile(t, "internal", "integration", "x1_strict_nonoverlap_characterization_test.go")
	fn := x1Func(t, file, "TestX1StrictNonoverlapCharacterization")
	body := x1SubtestBody(t, fn, "pendingBlocksReenqueue")
	if !x1BlockContainsIdent(body, "PendingItemExists") {
		t.Fatal("F0b1 must observe gc_pending_items via PendingItemExists")
	}
	if !x1BlockContainsIdent(body, "QueueItemExists") {
		t.Fatal("F0b1 must also observe queue absence via QueueItemExists")
	}
	if !x1BlockContainsIdent(body, "x1DeleteQueueRowKeepPending") {
		t.Fatal("F0b1 must delete the exact gc_queue row without touching pending")
	}
	if !x1BlockContainsIdent(body, "x1FailedItemExists") {
		t.Fatal("F0b1 must assert DLQ absence")
	}
	if !x1BlockContainsIdent(body, "x1ScanOrphanedBlocksWithRestoredCursor") {
		t.Fatal("F0b1 must run ScanOrphanedBlocksOnce via the restored-cursor helper")
	}
	if x1BlockContainsIdent(body, "FailItem") {
		t.Fatal("F0b1 must not use FailItem; that writes DLQ")
	}
}

func TestX1F0b2RunsScannerWithCursor(t *testing.T) {
	_, file := x1ParseFile(t, "internal", "integration", "x1_strict_nonoverlap_characterization_test.go")
	fn := x1Func(t, file, "TestX1StrictNonoverlapCharacterization")
	body := x1SubtestBody(t, fn, "candidateBehindCursor")
	if !x1BlockContainsIdent(body, "x1ScanOrphanedBlocksWithRestoredCursor") {
		t.Fatal("F0b2 must run ScanOrphanedBlocksOnce via the restored-cursor helper")
	}
	if !x1BlockContainsIdent(body, "QueueItemExists") {
		t.Fatal("F0b2 must observe queue absence after the real scan")
	}
}

func TestX1CandidateHarnessDoesNotPublishOrphan(t *testing.T) {
	_, file := x1ParseFile(t, "internal", "integration", "x1_strict_nonoverlap_characterization_test.go")
	fn := x1Func(t, file, "TestX1StrictNonoverlapCharacterization")
	for _, name := range []string{"StartBlockDeleteOrphan", "ObserveBlockDeleteLifecycle", "TerminateBlockDeleteLifecycle"} {
		if x1BlockContainsIdent(fn.Body, name) {
			t.Fatalf("candidate harness must not call %s", name)
		}
	}
}

func TestX1HUsesExportedPutCallback(t *testing.T) {
	_, file := x1ParseFile(t, "internal", "integration", "x1_strict_nonoverlap_characterization_test.go")
	fn := x1Func(t, file, "TestX1StrictNonoverlapCharacterization")
	_ = x1FirstCall(t, fn, "PutBlockMaterializationTarget")
	_ = x1FirstCall(t, fn, "ObjectExists")
	src, err := os.ReadFile(x1SourcePath("internal", "integration", "x1_strict_nonoverlap_characterization_test.go"))
	if err != nil {
		t.Fatalf("read characterization test: %v", err)
	}
	if !strings.Contains(string(src), "resurrected=") {
		t.Fatal("H must observe ObjectExists after the residual PUT as resurrected=")
	}
}

func TestX1EAttemptsFailedPhysicalDelete(t *testing.T) {
	_, file := x1ParseFile(t, "internal", "integration", "x1_strict_nonoverlap_characterization_test.go")
	fn := x1Func(t, file, "TestX1StrictNonoverlapCharacterization")
	body := x1SubtestBody(t, fn, "s3Failure")
	if !x1BlockContainsIdent(body, "x1RejectForeignTenantDelete") {
		t.Fatal("E must attempt a failed DeleteBlockByStorageKey via x1RejectForeignTenantDelete")
	}
	src, err := os.ReadFile(x1SourcePath("internal", "integration", "x1_strict_nonoverlap_characterization_test.go"))
	if err != nil {
		t.Fatalf("read characterization test: %v", err)
	}
	if !strings.Contains(string(src), `t.Fatal("E: P2 must not acquire canonical authority while P1 remains")`) {
		t.Fatal("E must refuse P2 install while P1 remains")
	}
}

func TestX1HelpersScanOrphanedBlocksOnce(t *testing.T) {
	_, file := x1ParseFile(t, "internal", "integration", "x1_strict_nonoverlap_helpers_test.go")
	fn := x1Func(t, file, "x1ScanOrphanedBlocksWithRestoredCursor")
	_ = x1FirstCall(t, fn, "ScanOrphanedBlocksOnce")
	_ = x1FirstCall(t, fn, "SaveGCStats")
}

func TestX1F2SafetyAndConvergenceAreSeparateAssignments(t *testing.T) {
	src, err := os.ReadFile(x1SourcePath("internal", "integration", "x1_strict_nonoverlap_characterization_test.go"))
	if err != nil {
		t.Fatalf("read characterization test: %v", err)
	}
	text := string(src)
	if !strings.Contains(text, "x1NonoverlapEvidence.ambiguousFinalizeSafety = true") {
		t.Fatal("F2-safety must set ambiguousFinalizeSafety by name")
	}
	if !strings.Contains(text, "x1NonoverlapEvidence.ambiguousFinalizeConvergence = true") {
		t.Fatal("F2-convergence must set ambiguousFinalizeConvergence by name")
	}
}

func TestX1EvidenceMissingListsEveryNamedLeg(t *testing.T) {
	src, err := os.ReadFile(x1SourcePath("internal", "integration", "x1_strict_nonoverlap_evidence_test.go"))
	if err != nil {
		t.Fatalf("read evidence file: %v", err)
	}
	text := string(src)
	required := []string{
		"writerFirst",
		"gcFirst",
		"refBeforeZeroProof",
		"refBetweenProofAndCut",
		"lateUploadRef",
		"borrowedFSPublish",
		"s3Failure",
		"postCommitResume",
		"pendingBlocksReenqueue",
		"candidateBehindCursor",
		"postDeleteCrash",
		"ambiguousFinalizeSafety",
		"ambiguousFinalizeConvergence",
		"lateRepairPut",
		"nextIncarnation",
	}
	for _, name := range required {
		if !strings.Contains(text, `"`+name+`"`) {
			t.Fatalf("missing() must name %q individually", name)
		}
	}
	if strings.Contains(text, "completed ==") || strings.Contains(text, "count == 15") || strings.Contains(text, "len(missing) == 0 && seen >") {
		t.Fatal("completeness must not be a numeric counter")
	}
}

func TestX1ScannerLookbackConstants(t *testing.T) {
	if gcInitialScanLookbackDays != 7 {
		t.Fatalf("gcInitialScanLookbackDays = %d, F0b2 cold-start window assumes 7", gcInitialScanLookbackDays)
	}
	if gcScanOverlapDays != 2 {
		t.Fatalf("gcScanOverlapDays = %d, F0b2 overlap window assumes 2", gcScanOverlapDays)
	}
}

func TestX1ScannerLookbackSource(t *testing.T) {
	src, err := os.ReadFile(x1SourcePath("internal", "gc", "store_cassandra.go"))
	if err != nil {
		t.Fatalf("read store_cassandra.go: %v", err)
	}
	text := string(src)
	if !strings.Contains(text, "gcInitialScanLookbackDays             = 7") {
		t.Fatal("source must keep gcInitialScanLookbackDays             = 7")
	}
	if !strings.Contains(text, "gcScanOverlapDays                     = 2") {
		t.Fatal("source must keep gcScanOverlapDays                     = 2")
	}
}

func TestX1HarnesMustNotCallProcessBlock(t *testing.T) {
	src, err := os.ReadFile(x1SourcePath("internal", "integration", "x1_strict_nonoverlap_characterization_test.go"))
	if err != nil {
		t.Fatalf("read characterization test: %v", err)
	}
	if strings.Contains(string(src), "processBlock") || strings.Contains(string(src), "ProcessOrgOnce") {
		t.Fatal("characterization harness must not drive worker.go processBlock")
	}
}
