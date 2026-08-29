package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// R3's safety argument is an order, not a function-name allowlist. A CreateFile
// that still "calls stagePendingPublishedFiles" somewhere can later promote
// without staging. These guards read production source and require stage < promote
// by offset in every request path.

var r3PromoteNames = map[string]bool{
	"PromotePublishAttemptReferences":           true,
	"promoteSyncPublishAttemptReferencesFn":     true,
	"promoteSeafHTTPPublishAttemptReferencesFn": true,
	"promotePendingPublishedFiles":              true,
	"finalizeSyncCommitBlockDelta":              true,
	"finalizeSeafHTTPPublishedBlockReferences":  true,
	"publishedBlockReferenceRepairPromoteFn":    true,
}

var r3StageNames = map[string]bool{
	"StagePublishAttemptReferences":           true,
	"stageSyncPublishAttemptReferencesFn":     true,
	"stageSeafHTTPPublishAttemptReferencesFn": true,
	"stageSeafHTTPPublishAttemptReferences":   true,
	"stageSyncCommitBlockDelta":               true,
	"stagePendingPublishedFiles":              true,
}

var r3PromoteOnlyHelpers = map[string]bool{
	"finalizeSyncCommitBlockDelta":             true,
	"finalizeSeafHTTPPublishedBlockReferences": true,
	"promotePendingPublishedFiles":             true,
}

var r3AllowedPromoteCallers = map[string]bool{
	"finalizeSyncCommitBlockDelta":             true,
	"finalizeSeafHTTPPublishedBlockReferences": true,
	"promotePendingPublishedFiles":             true,
	"handleSyncHeadPromotion":                  true,
	"tryAutoMergeSyncHeadPromotion":            true,
	"repairPublishedSyncCommitBlockDelta":      true,
	"commitUploadedFileOnce":                   true,
	"commitUploadedFileMultiBlockOnce":         true,
	"CreateFile":                               true,
	"finalizeStoredUploadMetadataOnce":         true,
	"processSingleItem":                        true,
	"publishEditedDocumentMetadata":            true,
	"publishedBlockReferenceRepairPromoteFn":   true,
	"repairPublishedBlockReferenceRepair":      true,
	"cleanupPendingPublishedFileOwnerAttempt":  true,
}

var r3PublishRepairPromoteOnly = map[string]bool{
	"publishedBlockReferenceRepairPromoteFn":  true,
	"repairPublishedBlockReferenceRepair":     true,
	"cleanupPendingPublishedFileOwnerAttempt": true,
}

var r3AuthorityNames = map[string]bool{
	"ValidatePublishAttemptAuthority": true,
	"FinishCheckedPublishAttempt":     true,
	"validatePublishAttemptAuthority": true,
	"finishCheckedPublishAttempt":     true,
}

func TestR3ProductionPromoteCallersStageBeforePromote(t *testing.T) {
	callers := map[string]bool{}
	for _, file := range r3ProductionFiles(t) {
		for _, decl := range file.syntax.Decls {
			name := r3EnclosingName(decl)
			promotes := r3CallPositions(decl, r3PromoteNames)
			if len(promotes) == 0 {
				continue
			}
			callers[name] = true
			if !r3AllowedPromoteCallers[name] {
				t.Errorf("%s: unlisted production promote caller %s; a new promote path reopens R25 unless it is inventoried and staged", file.path, name)
				continue
			}
			if r3PromoteOnlyHelpers[name] || r3PublishRepairPromoteOnly[name] {
				continue
			}
			stages := r3CallPositions(decl, r3StageNames)
			if len(stages) == 0 {
				t.Errorf("%s: %s promotes without staging; R3 cannot see a path that never writes pub:", file.path, name)
				continue
			}
			if stages[0] > promotes[0] {
				t.Errorf("%s: %s promotes at offset %d before it stages at %d; R3 is a post-stage check", file.path, name, promotes[0], stages[0])
			}
		}
	}
	if len(callers) == 0 {
		t.Fatal("no production promote call sites found; the handshake guard is vacuous")
	}
	for name := range r3AllowedPromoteCallers {
		if !callers[name] {
			t.Errorf("inventoried promote caller %s is gone; update r3AllowedPromoteCallers", name)
		}
	}
}

func TestR3PublishRepairMustNotRunAuthorityCheck(t *testing.T) {
	path := filepath.Join("v2", "publish_repair.go")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := r3CalledName(call)
		if r3AuthorityNames[name] {
			t.Errorf("publish_repair.go calls %s; v2 durable repair is promote-only because HEAD may already be published and pub: is the liveness pin", name)
		}
		return true
	})
}

func TestR3FunnelBFinishCheckedRunsAfterAdds(t *testing.T) {
	path := filepath.Join("v2", "fs_helpers.go")
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var fn *ast.FuncDecl
	for _, decl := range file.Decls {
		function, ok := decl.(*ast.FuncDecl)
		if ok && function.Name.Name == "stagePendingPublishedFiles" {
			fn = function
			break
		}
	}
	if fn == nil {
		t.Fatal("stagePendingPublishedFiles not found")
	}
	addPos := token.NoPos
	checkPos := token.NoPos
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch r3CalledName(call) {
		case "stagePendingPublishedFilesAddReferencesFn":
			addPos = call.Pos()
		case "stagePendingPublishedFilesFinishCheckedFn":
			checkPos = call.Pos()
		}
		return true
	})
	if addPos == token.NoPos || checkPos == token.NoPos {
		t.Fatal("stagePendingPublishedFiles must Add pub: rows then FinishChecked")
	}
	if checkPos < addPos {
		t.Fatal("funnel B runs R3 before the last Add; a pre-stage check cannot close the refs==0 race")
	}
}

func TestR3ProductionDoesNotCallAddPublishAttemptReferencesDirectly(t *testing.T) {
	for _, file := range r3ProductionFiles(t) {
		ast.Inspect(file.syntax, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			if r3CalledName(call) == "AddPublishAttemptReferences" {
				t.Errorf("%s calls AddPublishAttemptReferences directly; production must go through Stage or stagePendingPublishedFiles so R3 can run", file.path)
			}
			return true
		})
	}
}

func TestR3FinishCheckedDefaultIsWiredOnFunnelB(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("v2", "fs_helpers.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "var stagePendingPublishedFilesFinishCheckedFn = db.FinishCheckedPublishAttempt") {
		t.Fatal("funnel B finish-checked seam must default to db.FinishCheckedPublishAttempt")
	}
}

type r3ParsedFile struct {
	path   string
	syntax *ast.File
}

func r3ProductionFiles(t *testing.T) []r3ParsedFile {
	t.Helper()
	var files []r3ParsedFile
	roots := []string{".", "v2"}
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			path := filepath.Join(root, name)
			syntax, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			files = append(files, r3ParsedFile{path: path, syntax: syntax})
		}
	}
	if len(files) == 0 {
		t.Fatal("no production API files found")
	}
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	return files
}

func r3CallPositions(decl ast.Decl, names map[string]bool) []int {
	var positions []int
	ast.Inspect(decl, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if names[r3CalledName(call)] {
			positions = append(positions, int(call.Pos()))
		}
		return true
	})
	sort.Ints(positions)
	return positions
}

func r3CalledName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		return fn.Sel.Name
	default:
		return ""
	}
}

func r3EnclosingName(decl ast.Decl) string {
	switch node := decl.(type) {
	case *ast.FuncDecl:
		return node.Name.Name
	case *ast.GenDecl:
		for _, spec := range node.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok || len(value.Names) == 0 {
				continue
			}
			return value.Names[0].Name
		}
	}
	return "<unknown>"
}
