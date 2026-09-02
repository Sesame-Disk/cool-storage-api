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
	fileFromBlocksAfterVerifiedBarrier("repo")
	fileFromBlocksAfterStagedBarrier("repo")
	if err := fileFromBlocksBeforeHeadBarrier("repo"); err != nil {
		t.Fatalf("default beforeHead barrier = %v, want nil", err)
	}
}

func TestFileFromBlocksPublicationBarriersProdAreEmpty(t *testing.T) {
	path := r3SourcePath("internal", "api", "v2", "file_from_blocks_publication_barriers.go")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read production barriers: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, "//go:build !integration") {
		t.Fatal("R3 BARRIER: production barrier file must be //go:build !integration")
	}
	for _, forbidden := range []string{
		"sync.Mutex",
		"SetFileFromBlocksPublicationBarriersForTest",
		"BlockDeleteFenceActive",
		"ProbeBlockReuse",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("R3 BARRIER: production barriers must not contain %s", forbidden)
		}
	}
	_, file := parseV2ProductionFile(t, "file_from_blocks_publication_barriers.go")
	for _, name := range []string{
		"fileFromBlocksAfterVerifiedBarrier",
		"fileFromBlocksAfterStagedBarrier",
		"fileFromBlocksBeforeHeadBarrier",
	} {
		fn := productionFunc(t, file, name)
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			if _, ok := node.(*ast.CallExpr); ok {
				t.Fatalf("R3 BARRIER: production %s must be an empty nop", name)
			}
			return true
		})
	}
}

func TestFileFromBlocksPublicationBarriersIntegrationIsTagged(t *testing.T) {
	prod := r3SourcePath("internal", "api", "v2", "file_from_blocks.go")
	files := r3SourcePath("internal", "api", "v2", "files.go")
	for _, path := range []string{prod, files} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(raw)
		for _, forbidden := range []string{
			"SetFileFromBlocksPublicationBarriersForTest",
			"fileFromBlocksBarrierMu",
			"fileFromBlocksAfterVerifiedFn",
		} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("R3 BARRIER: production %s must not contain %s", path, forbidden)
			}
		}
	}

	path := r3SourcePath("internal", "api", "v2", "file_from_blocks_publication_barriers_integration.go")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read integration barriers: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, "//go:build integration") {
		t.Fatal("R3 BARRIER: integration barriers must be //go:build integration")
	}
	for _, required := range []string{
		"SetFileFromBlocksPublicationBarriersForTest",
		"sync.Mutex",
		"hooks.repoID != repoID",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("R3 BARRIER: integration barriers missing %q", required)
		}
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
	assertBarrierArgIsRepoID(t, fn, "CreateFileFromBlocks", "fileFromBlocksAfterVerifiedBarrier")
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
	assertBarrierArgIsRepoID(t, fn, "finalizeStoredUploadMetadataOnce", "fileFromBlocksAfterStagedBarrier")
	assertBarrierArgIsRepoID(t, fn, "finalizeStoredUploadMetadataOnce", "fileFromBlocksBeforeHeadBarrier")
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
		"harnessLatePinStillFenced",
	} {
		needle := `t.Run("` + name + `"`
		if !strings.Contains(text, needle) {
			t.Fatalf("BorrowedFS HEAD characterization is missing named leg %s", name)
		}
	}
	for _, needle := range []string{
		"pin must remain visible at beforeHead",
		"expected no active fence before HEAD",
		"late up:<session> after zero-proof",
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("BorrowedFS HEAD handshake characterization is missing %q", needle)
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

func productionFunc(t *testing.T, file *ast.File, name string) *ast.FuncDecl {
	t.Helper()
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == name {
			return fn
		}
	}
	t.Fatalf("R3 BARRIER: function %s not found", name)
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

func assertBarrierArgIsRepoID(t *testing.T, fn *ast.FuncDecl, scope, name string) {
	t.Helper()
	found := false
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || r3CallName(call) != name || len(call.Args) != 1 {
			return true
		}
		ident, ok := call.Args[0].(*ast.Ident)
		if !ok || ident.Name != "repoID" {
			t.Fatalf("R3 BARRIER: %s %s must pass repoID so hooks stay fixture-scoped", scope, name)
		}
		found = true
		return true
	})
	if !found {
		t.Fatalf("R3 BARRIER: %s must call %s(repoID)", scope, name)
	}
}
