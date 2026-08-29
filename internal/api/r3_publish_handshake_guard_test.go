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
// without staging. These guards read production source and require stage before
// promote by offset, and that each stage error aborts (`if err != nil { return }`)
// rather than continuing to promote. Abort is the statement that owns the
// call, including inside retry callbacks; the outer retry `if err != nil`
// is not credited to a nested stage.

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

func TestR3ProductionStageErrorsAbortBeforePromote(t *testing.T) {
	checked := 0
	for _, file := range r3ProductionFiles(t) {
		for _, decl := range file.syntax.Decls {
			name := r3EnclosingName(decl)
			if r3PromoteOnlyHelpers[name] || r3PublishRepairPromoteOnly[name] {
				continue
			}
			if len(r3CallPositions(decl, r3PromoteNames)) == 0 {
				continue
			}
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			for _, call := range r3StageCalls(fn, r3StageNames) {
				checked++
				if !r3StageErrorReturns(fn, call) {
					t.Errorf("%s: %s stage at offset %d does not abort on error before promote", file.path, name, int(call.Pos()))
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no stage calls in dual stage+promote functions; the abort guard is vacuous")
	}
}

func TestR3StageErrorAbortLooksInsideRetryCallback(t *testing.T) {
	guarded := `package p
func f() {
	err := retry(func() error {
		if err := stagePendingPublishedFiles(); err != nil {
			return err
		}
		promotePendingPublishedFiles()
		return nil
	})
	if err != nil {
		return
	}
}
`
	dropped := `package p
func f() {
	err := retry(func() error {
		_ = stagePendingPublishedFiles()
		if false {
			return err
		}
		promotePendingPublishedFiles()
		return nil
	})
	if err != nil {
		return
	}
}
`
	if !r3SnippetStageAborts(t, guarded) {
		t.Fatal("retry callback with if-err-return must count as aborting")
	}
	if r3SnippetStageAborts(t, dropped) {
		t.Fatal("dropping the callback return must not be credited to the outer retry err-guard")
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

func TestR3FunnelBHelperDoesNotPromote(t *testing.T) {
	path := filepath.Join("v2", "fs_helpers.go")
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range file.Decls {
		function, ok := decl.(*ast.FuncDecl)
		if !ok || function.Name.Name != "stagePendingPublishedFiles" {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			if r3PromoteNames[r3CalledName(call)] {
				t.Errorf("stagePendingPublishedFiles calls %s; funnel B must return denial, not promote", r3CalledName(call))
			}
			return true
		})
		return
	}
	t.Fatal("stagePendingPublishedFiles not found")
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

func r3StageCalls(fn *ast.FuncDecl, names map[string]bool) []*ast.CallExpr {
	var calls []*ast.CallExpr
	ast.Inspect(fn, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if names[r3CalledName(call)] {
			calls = append(calls, call)
		}
		return true
	})
	return calls
}

func r3StageErrorReturns(fn *ast.FuncDecl, stage *ast.CallExpr) bool {
	found := false
	var walk func(stmts []ast.Stmt)
	walk = func(stmts []ast.Stmt) {
		for i, stmt := range stmts {
			// Copy/CreateFile/OnlyOffice stage inside retryLibraryHeadMutation
			// callbacks. The outer `err := retry(...); if err != nil { return }`
			// is not this stage's abort: dropping the inner return still
			// continues to promote inside the callback.
			r3WalkNestedFuncLits(stmt, walk)
			switch s := stmt.(type) {
			case *ast.IfStmt:
				if r3NodeContainsCall(s.Init, stage) && r3IfGuardsError(s) {
					found = true
				}
				if s.Body != nil {
					walk(s.Body.List)
				}
				switch elseStmt := s.Else.(type) {
				case *ast.BlockStmt:
					walk(elseStmt.List)
				case *ast.IfStmt:
					walk([]ast.Stmt{elseStmt})
				}
			case *ast.AssignStmt:
				if r3NodeContainsCall(s, stage) && i+1 < len(stmts) {
					if ifs, ok := stmts[i+1].(*ast.IfStmt); ok && r3IfGuardsError(ifs) {
						found = true
					}
				}
			case *ast.ForStmt:
				if s.Body != nil {
					walk(s.Body.List)
				}
			case *ast.RangeStmt:
				if s.Body != nil {
					walk(s.Body.List)
				}
			case *ast.BlockStmt:
				walk(s.List)
			case *ast.SwitchStmt:
				if s.Body == nil {
					continue
				}
				for _, clause := range s.Body.List {
					if cc, ok := clause.(*ast.CaseClause); ok {
						walk(cc.Body)
					}
				}
			}
		}
	}
	walk(fn.Body.List)
	return found
}

func r3WalkNestedFuncLits(node ast.Node, walk func([]ast.Stmt)) {
	if node == nil {
		return
	}
	ast.Inspect(node, func(n ast.Node) bool {
		lit, ok := n.(*ast.FuncLit)
		if !ok {
			return true
		}
		if lit.Body != nil {
			walk(lit.Body.List)
		}
		return false
	})
}

func r3SnippetStageAborts(t *testing.T, src string) bool {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "snippet.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	fn, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok || fn.Body == nil {
		t.Fatal("expected function snippet")
	}
	calls := r3StageCalls(fn, r3StageNames)
	if len(calls) != 1 {
		t.Fatalf("want 1 stage call, got %d", len(calls))
	}
	return r3StageErrorReturns(fn, calls[0])
}

func r3IfGuardsError(ifs *ast.IfStmt) bool {
	return r3ExprMentionsErr(ifs.Cond) && r3BlockHasReturn(ifs.Body)
}

func r3ExprMentionsErr(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		ident, ok := n.(*ast.Ident)
		if ok && ident.Name == "err" {
			found = true
			return false
		}
		return true
	})
	return found
}

func r3BlockHasReturn(block *ast.BlockStmt) bool {
	if block == nil {
		return false
	}
	found := false
	ast.Inspect(block, func(n ast.Node) bool {
		if _, ok := n.(*ast.ReturnStmt); ok {
			found = true
			return false
		}
		return true
	})
	return found
}

func r3NodeContainsCall(node ast.Node, want *ast.CallExpr) bool {
	if node == nil || want == nil {
		return false
	}
	found := false
	ast.Inspect(node, func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncLit); ok {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if ok && call.Pos() == want.Pos() {
			found = true
			return false
		}
		return true
	})
	return found
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
