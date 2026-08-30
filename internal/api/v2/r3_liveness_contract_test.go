package v2

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
)

const r3SourceRootEnv = "R3_SOURCE_ROOT"

func r3SourcePath(parts ...string) string {
	if root := strings.TrimSpace(os.Getenv(r3SourceRootEnv)); root != "" {
		return filepath.Join(append([]string{root}, parts...)...)
	}
	return filepath.Join(append([]string{"..", "..", ".."}, parts...)...)
}

func r3ParseFunction(t *testing.T, path, function string) (*token.FileSet, *ast.FuncDecl) {
	t.Helper()
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("R3 SOURCE CONTRACT: parse %s: %v", path, err)
	}
	for _, decl := range parsed.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == function {
			return fset, fn
		}
	}
	t.Fatalf("R3 SOURCE CONTRACT: function %s not found in %s", function, path)
	return nil, nil
}

func r3CallName(call *ast.CallExpr) string {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return fun.Name
	case *ast.SelectorExpr:
		return fun.Sel.Name
	default:
		return ""
	}
}

func r3CallPositions(fn *ast.FuncDecl) map[string][]token.Pos {
	positions := make(map[string][]token.Pos)
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if name := r3CallName(call); name != "" {
			positions[name] = append(positions[name], call.Pos())
		}
		return true
	})
	return positions
}

// TestR3RegisterUploadedBlockTargetPinsBeforeAuthority freezes the existing
// writer half of the protocol. The provisional liveness write must be durable
// before the post-pin fence, and metadata authority must remain downstream of
// that fence for both the fresh-install and exact-repair branches.
func TestR3RegisterUploadedBlockTargetPinsBeforeAuthority(t *testing.T) {
	path := r3SourcePath("internal", "api", "v2", "fs_helpers.go")
	_, fn := r3ParseFunction(t, path, "RegisterUploadedBlockTarget")
	calls := r3CallPositions(fn)

	first := func(name string) token.Pos {
		t.Helper()
		if len(calls[name]) == 0 {
			t.Fatalf("R3 HANDSHAKE: RegisterUploadedBlockTarget must call %s", name)
		}
		return calls[name][0]
	}

	up := first("registerUploadedBlockAddProvisionalRefFn")
	fence := first("registerUploadedBlockFenceActiveFn")
	fresh := first("prepareFreshBlockInstall")
	repair := first("registerUploadedBlockRepairMetadataFn")
	if !(up < fence) {
		t.Fatal("R3 HANDSHAKE: provisional up must precede post-pin fence")
	}
	if !(fence < fresh && fence < repair) {
		t.Fatal("R3 HANDSHAKE: metadata authority must remain downstream of the post-pin fence")
	}
}

func TestR3RegisterUploadedBlockTargetRejectsActiveFence(t *testing.T) {
	path := r3SourcePath("internal", "api", "v2", "fs_helpers.go")
	_, fn := r3ParseFunction(t, path, "RegisterUploadedBlockTarget")
	found := false
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		stmt, ok := node.(*ast.IfStmt)
		if !ok {
			return true
		}
		ident, ok := stmt.Cond.(*ast.Ident)
		if !ok || ident.Name != "deleteFenceActive" {
			return true
		}
		hasReturn := false
		hasFenceError := false
		ast.Inspect(stmt.Body, func(child ast.Node) bool {
			switch value := child.(type) {
			case *ast.ReturnStmt:
				hasReturn = true
			case *ast.Ident:
				if value.Name == "ErrBlockDeleteInProgress" {
					hasFenceError = true
				}
			}
			return true
		})
		found = hasReturn && hasFenceError
		return !found
	})
	if !found {
		t.Fatal("R3 HANDSHAKE: an active post-pin fence must return ErrBlockDeleteInProgress")
	}
}

func TestR3RegisterUploadedBlockTargetNeverDropsSuccessfulUploadPin(t *testing.T) {
	path := r3SourcePath("internal", "api", "v2", "fs_helpers.go")
	_, fn := r3ParseFunction(t, path, "RegisterUploadedBlockTarget")
	forbidden := map[string]bool{
		"DeleteProvisionalBlockReferenceExpiry": true,
		"RemoveBlockReference":                  true,
		"RemovePublishAttemptReferences":        true,
	}
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if ok && forbidden[r3CallName(call)] {
			t.Fatalf("R3 HANDSHAKE: successful materialization must not remove its provisional up pin via %s", r3CallName(call))
		}
		return true
	})
}

func TestR3RegisterUploadedBlockTargetRuntimeOrder(t *testing.T) {
	oldAdd := registerUploadedBlockAddProvisionalRefFn
	oldFence := registerUploadedBlockFenceActiveFn
	oldRepair := registerUploadedBlockRepairMetadataFn
	oldResolve := resolveFreshInstallRepresentationFn
	oldInstall := registerUploadedBlockInstallMetadataFn
	oldDelete := deleteFreshInstallLoserFn
	t.Cleanup(func() {
		registerUploadedBlockAddProvisionalRefFn = oldAdd
		registerUploadedBlockFenceActiveFn = oldFence
		registerUploadedBlockRepairMetadataFn = oldRepair
		resolveFreshInstallRepresentationFn = oldResolve
		registerUploadedBlockInstallMetadataFn = oldInstall
		deleteFreshInstallLoserFn = oldDelete
	})

	var calls []string
	registerUploadedBlockAddProvisionalRefFn = func(*FSHelper, string, string, string, string, string, time.Time) error {
		calls = append(calls, "up")
		return nil
	}
	registerUploadedBlockFenceActiveFn = func(*FSHelper, string, string) (bool, error) {
		calls = append(calls, "fence")
		return false, nil
	}
	registerUploadedBlockRepairMetadataFn = func(*FSHelper, string, string, string, string, int, BlockMaterializationTarget) error {
		calls = append(calls, "repair")
		return nil
	}
	resolveFreshInstallRepresentationFn = func(context.Context, *FSHelper, string, string) (string, error) {
		calls = append(calls, "resolve")
		return db.PlainBlockRepresentationID, nil
	}
	registerUploadedBlockInstallMetadataFn = func(context.Context, *FSHelper, string, string, string, string, int, BlockMaterializationTarget) db.InstallBlockMetadataResult {
		calls = append(calls, "install")
		return db.InstallBlockMetadataResult{Outcome: db.InstallBlockMetadataApplied, Submitted: true}
	}
	deleteFreshInstallLoserFn = func(context.Context, BlockMaterializationTarget) error {
		t.Fatal("R3 HANDSHAKE: an applied fresh install must retain its exact object")
		return nil
	}

	assertCalls := func(want []string) {
		t.Helper()
		if strings.Join(calls, ",") != strings.Join(want, ",") {
			t.Fatalf("R3 HANDSHAKE: calls = %v, want %v", calls, want)
		}
		calls = nil
	}

	if err := (&FSHelper{}).RegisterUploadedBlockTarget(context.Background(), "org", "repo", uploadReuseTestBlockID, "repair-op", 1, BlockMaterializationTarget{StorageClass: "hot", StorageKey: "key"}, ""); err != nil {
		t.Fatalf("repair RegisterUploadedBlockTarget: %v", err)
	}
	assertCalls([]string{"up", "fence", "repair"})

	const orgID = "3fa85f64-5717-4562-b3fc-2c963f66afa6"
	store, err := storage.NewOrgBlockStore(&storage.S3Store{}, "blocks/", orgID)
	if err != nil {
		t.Fatal(err)
	}
	key, err := store.MintStorageKey(uploadReuseTestBlockID)
	if err != nil {
		t.Fatal(err)
	}
	target := BlockMaterializationTarget{Store: store, StorageClass: "hot", StorageKey: key, FreshInstall: true}
	if err := (&FSHelper{}).RegisterUploadedBlockTarget(context.Background(), orgID, "repo", uploadReuseTestBlockID, "fresh-op", 1, target, ""); err != nil {
		t.Fatalf("fresh RegisterUploadedBlockTarget: %v", err)
	}
	assertCalls([]string{"up", "fence", "resolve", "install"})
}
