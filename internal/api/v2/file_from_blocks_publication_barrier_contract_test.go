package v2

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

func TestFileFromBlocksPublicationBarriersDefaultNop(t *testing.T) {
	fileFromBlocksAfterVerifiedBarrier()
	fileFromBlocksAfterStagedBarrier()
	if err := fileFromBlocksBeforeHeadBarrier(); err != nil {
		t.Fatalf("default beforeHead barrier = %v, want nil", err)
	}
}

func TestSetFileFromBlocksPublicationBarriersForTestRestores(t *testing.T) {
	called := false
	restore := SetFileFromBlocksPublicationBarriersForTest(func() { called = true }, nil, nil)
	fileFromBlocksAfterVerifiedBarrier()
	if !called {
		t.Fatal("installed afterVerified barrier was not invoked")
	}
	restore()
	called = false
	fileFromBlocksAfterVerifiedBarrier()
	if called {
		t.Fatal("afterVerified barrier leaked after restore")
	}
}

func TestFileFromBlocksPublicationBarriersAreNopFuncLits(t *testing.T) {
	_, file := parseV2ProductionFile(t, "file_from_blocks.go")
	for _, name := range []string{
		"fileFromBlocksAfterVerifiedFn",
		"fileFromBlocksAfterStagedFn",
		"fileFromBlocksBeforeHeadFn",
	} {
		lit := fileFromBlocksBarrierFuncLit(t, file, name)
		ast.Inspect(lit, func(node ast.Node) bool {
			switch value := node.(type) {
			case *ast.CallExpr:
				t.Fatalf("R3 BARRIER: default %s must be a nop FuncLit; found call %s", name, r3CallName(value))
			case *ast.Ident:
				switch value.Name {
				case "BlockDeleteFenceActive", "ProbeBlockReuse", "BlockHasReferencesGlobal",
					"Query", "Session", "AddBlockReference", "AddProvisionalBlockReferenceWithExpiry":
					t.Fatalf("R3 BARRIER: default %s must not mention %s", name, value.Name)
				}
			}
			return true
		})
	}
}

func TestFileFromBlocksAfterVerifiedBarrierIsBetweenVerifyAndClaim(t *testing.T) {
	_, fn := r3ParseFunction(t, r3SourcePath("internal", "api", "v2", "file_from_blocks.go"), "CreateFileFromBlocks")
	calls := r3CallPositions(fn)
	verify := barrierFirstCallPos(t, calls, "CreateFileFromBlocks", "summarizeBlockVerification")
	barrier := barrierFirstCallPos(t, calls, "CreateFileFromBlocks", "fileFromBlocksAfterVerifiedBarrier")
	claim := barrierFirstCallPos(t, calls, "CreateFileFromBlocks", "ClaimBlockUploadSessionForCommit")
	if !(verify < barrier && barrier < claim) {
		t.Fatal("R3 BARRIER: fileFromBlocksAfterVerifiedBarrier must run after verify and before ClaimBlockUploadSessionForCommit")
	}
	assertFnDoesNotCall(t, fn, "CreateFileFromBlocks", "BlockDeleteFenceActive", "ProbeBlockReuse")
}

func TestFileFromBlocksStageAndHeadBarriersStayOffAuthority(t *testing.T) {
	_, fn := r3ParseFunction(t, r3SourcePath("internal", "api", "v2", "files.go"), "finalizeStoredUploadMetadataOnce")
	calls := r3CallPositions(fn)
	stage := barrierFirstCallPos(t, calls, "finalizeStoredUploadMetadataOnce", "stagePendingPublishedFiles")
	afterStaged := barrierFirstCallPos(t, calls, "finalizeStoredUploadMetadataOnce", "fileFromBlocksAfterStagedBarrier")
	beforeHead := barrierFirstCallPos(t, calls, "finalizeStoredUploadMetadataOnce", "fileFromBlocksBeforeHeadBarrier")
	head := barrierFirstCallPos(t, calls, "finalizeStoredUploadMetadataOnce", "UpdateLibraryHeadFromSnapshot")
	if !(stage < afterStaged && afterStaged < beforeHead && beforeHead < head) {
		t.Fatal("R3 BARRIER: afterStaged must follow stagePendingPublishedFiles and beforeHead must sit immediately before UpdateLibraryHeadFromSnapshot")
	}
	assertFnDoesNotCall(t, fn, "finalizeStoredUploadMetadataOnce", "BlockDeleteFenceActive", "ProbeBlockReuse")
}

func TestBorrowedFSHeadCharacterizationNamesArePinned(t *testing.T) {
	path := r3SourcePath("internal", "integration", "borrowedfs_head_characterization_test.go")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read BorrowedFS HEAD characterization test: %v", err)
	}
	text := string(raw)
	for _, name := range []string{
		"currentHeadAfterCut",
		"currentPubRevokesZeroProof",
		"currentPubAfterZeroProof",
		"harnessWriterWins",
		"harnessCutAfterClassify",
		"harnessLatePubStillFenced",
	} {
		needle := `t.Run("` + name + `"`
		if !strings.Contains(text, needle) {
			t.Fatalf("BorrowedFS HEAD characterization is missing named leg %s", name)
		}
	}
	if strings.Contains(text, "worker.go") {
		t.Fatal("BorrowedFS HEAD characterization must not touch worker.go")
	}
}

func parseV2ProductionFile(t *testing.T, name string) (*token.FileSet, *ast.File) {
	t.Helper()
	path := r3SourcePath("internal", "api", "v2", name)
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return fset, parsed
}

func fileFromBlocksBarrierFuncLit(t *testing.T, file *ast.File, name string) *ast.FuncLit {
	t.Helper()
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			values, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, ident := range values.Names {
				if ident.Name != name {
					continue
				}
				if i >= len(values.Values) {
					t.Fatalf("R3 BARRIER: %s has no initializer", name)
				}
				lit, ok := values.Values[i].(*ast.FuncLit)
				if !ok {
					t.Fatalf("R3 BARRIER: %s must be initialized with a FuncLit, got %T", name, values.Values[i])
				}
				return lit
			}
		}
	}
	t.Fatalf("R3 BARRIER: %s not found", name)
	return nil
}

func barrierFirstCallPos(t *testing.T, calls map[string][]token.Pos, scope, name string) token.Pos {
	t.Helper()
	if len(calls[name]) == 0 {
		t.Fatalf("R3 BARRIER: %s must call %s", scope, name)
	}
	return calls[name][0]
}

func assertFnDoesNotCall(t *testing.T, fn *ast.FuncDecl, scope string, forbidden ...string) {
	t.Helper()
	calls := r3CallPositions(fn)
	for _, name := range forbidden {
		if len(calls[name]) > 0 {
			t.Fatalf("R3 BARRIER: %s must not call %s; fence/probe stay out of the productive publication path", scope, name)
		}
	}
}
