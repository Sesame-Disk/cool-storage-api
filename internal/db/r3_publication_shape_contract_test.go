package db

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

type r3StageHeadBoundary struct {
	label        string
	path         string
	function     string
	stage        string
	head         string
	sessionCalls int
	cqlCalls     int
}

var r3CQLVerbPattern = regexp.MustCompile(`(?is)\b(select|insert|update|delete)\b`)

func r3ParseProductionFile(t *testing.T, path string) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("R3 PUBLICATION SHAPE: parse %s: %v", path, err)
	}
	return file
}

func r3FindProductionFunction(t *testing.T, file *ast.File, name string) *ast.FuncDecl {
	t.Helper()
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == name {
			return fn
		}
	}
	t.Fatalf("R3 PUBLICATION SHAPE: function %s not found", name)
	return nil
}

func r3NamedCalls(fn *ast.FuncDecl, name string) []*ast.CallExpr {
	var result []*ast.CallExpr
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if ok && r3PublicationCallName(call) == name {
			result = append(result, call)
		}
		return true
	})
	return result
}

func r3FirstCallBefore(calls []*ast.CallExpr, after token.Pos) *ast.CallExpr {
	for _, call := range calls {
		if call.Pos() > after {
			return call
		}
	}
	return nil
}

func r3ExprIsDBField(expr ast.Expr) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	return ok && selector.Sel.Name == "db"
}

func r3CollectDBSurface(fn *ast.FuncDecl) (map[string]bool, map[string]bool) {
	aliases := make(map[string]bool)
	methodValues := make(map[string]bool)
	isDB := func(expr ast.Expr) bool {
		if r3ExprIsDBField(expr) {
			return true
		}
		ident, ok := expr.(*ast.Ident)
		return ok && aliases[ident.Name]
	}
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.AssignStmt:
			for position, left := range value.Lhs {
				name, ok := left.(*ast.Ident)
				if !ok || position >= len(value.Rhs) {
					continue
				}
				right := value.Rhs[position]
				if isDB(right) {
					aliases[name.Name] = true
					continue
				}
				selector, ok := right.(*ast.SelectorExpr)
				if ok && isDB(selector.X) {
					methodValues[name.Name] = true
				}
			}
		case *ast.ValueSpec:
			for position, name := range value.Names {
				if position >= len(value.Values) {
					continue
				}
				right := value.Values[position]
				if isDB(right) {
					aliases[name.Name] = true
					continue
				}
				selector, ok := right.(*ast.SelectorExpr)
				if ok && isDB(selector.X) {
					methodValues[name.Name] = true
				}
			}
		}
		return true
	})
	return aliases, methodValues
}

func r3DirectDBMethod(call *ast.CallExpr) (string, bool) {
	return r3DirectDBMethodOn(call, nil)
}

func r3DirectDBMethodOn(call *ast.CallExpr, aliases map[string]bool) (string, bool) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	dbSelector, ok := selector.X.(*ast.SelectorExpr)
	if ok && dbSelector.Sel.Name == "db" {
		if _, ok := dbSelector.X.(*ast.Ident); ok {
			return selector.Sel.Name, true
		}
	}
	ident, ok := selector.X.(*ast.Ident)
	if ok && aliases[ident.Name] {
		return selector.Sel.Name, true
	}
	return "", false
}

func r3CallCQLText(call *ast.CallExpr) (string, bool) {
	name := r3PublicationCallName(call)
	if name != "Query" && name != "Bind" {
		return "", false
	}
	if len(call.Args) == 0 {
		return "", false
	}
	return r3ProgramString(call.Args[0], map[string]string{})
}

func r3PublicationSinkName(name string) bool {
	switch name {
	case "AddPublishAttemptReferences", "addPublishAttemptReferenceFn", "StagePublishAttemptReferences":
		return true
	default:
		return false
	}
}

func r3ParseProductionValueFuncLits(t *testing.T, directory string) map[string]*ast.FuncLit {
	t.Helper()
	fset := token.NewFileSet()
	packages, err := parser.ParseDir(fset, directory, func(info os.FileInfo) bool {
		return !strings.HasSuffix(info.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("R3 PUBLICATION SHAPE: parse %s: %v", directory, err)
	}
	lits := make(map[string]*ast.FuncLit)
	for _, pkg := range packages {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				general, ok := decl.(*ast.GenDecl)
				if !ok || general.Tok != token.VAR {
					continue
				}
				for _, spec := range general.Specs {
					value, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for position, name := range value.Names {
						if position >= len(value.Values) {
							continue
						}
						lit, ok := value.Values[position].(*ast.FuncLit)
						if ok {
							lits[name.Name] = lit
						}
					}
				}
			}
		}
	}
	return lits
}

func r3LocalAssignedFuncLit(fn *ast.FuncDecl, name string) *ast.FuncLit {
	var found *ast.FuncLit
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		assign, ok := node.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for position, left := range assign.Lhs {
			ident, ok := left.(*ast.Ident)
			if !ok || ident.Name != name || position >= len(assign.Rhs) {
				continue
			}
			lit, ok := assign.Rhs[position].(*ast.FuncLit)
			if ok {
				found = lit
			}
		}
		return true
	})
	return found
}

func r3AssertNoPublicationSink(t *testing.T, body ast.Node, seam, authorized string) {
	t.Helper()
	if body == nil {
		return
	}
	ast.Inspect(body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := r3PublicationCallName(call)
		if r3PublicationSinkName(name) {
			t.Fatalf("R3 FANOUT: %s reaches %s; the only authorized publication sink is %s", seam, name, authorized)
		}
		return true
	})
}

// TestR3PublicationStageToHeadHasNoUnlistedDirectDBCalls freezes the real
// publication funnels' lexical stage -> HEAD boundary. Direct DB access includes
// ident.db.Method, a local alias of that field, a method value taken from it,
// and Query/Bind CQL entry points regardless of how the session was obtained.
// It is a narrow source contract, not a general control-flow or RTT analysis.
func TestR3PublicationStageToHeadHasNoUnlistedDirectDBCalls(t *testing.T) {
	root := r3RepositoryRoot(t)
	boundaries := []r3StageHeadBoundary{
		{label: "v2/CreateFile", path: "internal/api/v2/files.go", function: "CreateFile", stage: "stagePendingPublishedFiles", head: "UpdateLibraryHeadFromSnapshot"},
		{label: "v2/finalizeStoredUploadMetadataOnce", path: "internal/api/v2/files.go", function: "finalizeStoredUploadMetadataOnce", stage: "stagePendingPublishedFiles", head: "UpdateLibraryHeadFromSnapshot"},
		{label: "v2/processSingleItem", path: "internal/api/v2/batch_operations.go", function: "processSingleItem", stage: "stagePendingPublishedFiles", head: "UpdateLibraryHeadFromSnapshot"}, // cross-repo copy/move only; same-repo does not stage pub:
		{label: "v2/publishEditedDocumentMetadata", path: "internal/api/v2/onlyoffice.go", function: "publishEditedDocumentMetadata", stage: "stagePendingPublishedFiles", head: "UpdateLibraryHeadFromSnapshot"},
		{label: "seafhttp/commitUploadedFileMultiBlockOnce", path: "internal/api/seafhttp.go", function: "commitUploadedFileMultiBlockOnce", stage: "stageSeafHTTPPublishAttemptReferences", head: "UpdateLibraryHeadFromSnapshot", sessionCalls: 1, cqlCalls: 1},
		{label: "seafhttp/commitUploadedFileOnce", path: "internal/api/seafhttp.go", function: "commitUploadedFileOnce", stage: "stageSeafHTTPPublishAttemptReferences", head: "UpdateLibraryHeadFromSnapshot", sessionCalls: 1, cqlCalls: 1},
		{label: "sync/tryAutoMergeSyncHeadPromotion", path: "internal/api/sync.go", function: "tryAutoMergeSyncHeadPromotion", stage: "stageSyncCommitBlockDelta", head: "updateLibraryHeadWithStats"},
		{label: "sync/handleSyncHeadPromotion", path: "internal/api/sync.go", function: "handleSyncHeadPromotion", stage: "stageSyncCommitBlockDelta", head: "updateLibraryHeadWithStats"},
	}

	for _, boundary := range boundaries {
		boundary := boundary
		t.Run(boundary.label, func(t *testing.T) {
			file := r3ParseProductionFile(t, filepath.Join(root, filepath.FromSlash(boundary.path)))
			fn := r3FindProductionFunction(t, file, boundary.function)
			stages := r3NamedCalls(fn, boundary.stage)
			if len(stages) != 1 {
				t.Fatalf("R3 PUBLICATION SHAPE: %s has %d %s calls, want one", boundary.label, len(stages), boundary.stage)
			}
			head := r3FirstCallBefore(r3NamedCalls(fn, boundary.head), stages[0].Pos())
			if head == nil {
				t.Fatalf("R3 PUBLICATION SHAPE: %s has no %s after %s", boundary.label, boundary.head, boundary.stage)
			}
			aliases, methodValues := r3CollectDBSurface(fn)
			sessions := 0
			cqlCalls := 0
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok || call.Pos() <= stages[0].End() || call.End() >= head.Pos() {
					return true
				}
				name := r3PublicationCallName(call)
				if r3ForbiddenAuthorityName(name) {
					t.Fatalf("R3 PUBLICATION SHAPE: %s adds authority helper %s between %s and %s", boundary.label, name, boundary.stage, boundary.head)
				}
				if methodValues[name] {
					t.Fatalf("R3 PUBLICATION SHAPE: %s adds db method value %s between %s and %s", boundary.label, name, boundary.stage, boundary.head)
				}
				if text, ok := r3CallCQLText(call); ok {
					normalized := strings.ToLower(strings.ReplaceAll(text, `"`, ""))
					if r3AuthoritySelectPattern.MatchString(normalized) {
						t.Fatalf("R3 PUBLICATION SHAPE: %s adds authority CQL between %s and %s", boundary.label, boundary.stage, boundary.head)
					}
					if r3CQLVerbPattern.MatchString(normalized) {
						cqlCalls++
					}
				}
				method, directDB := r3DirectDBMethodOn(call, aliases)
				if !directDB {
					return true
				}
				if method != "Session" {
					t.Fatalf("R3 PUBLICATION SHAPE: %s adds direct DB method %s between %s and %s", boundary.label, method, boundary.stage, boundary.head)
				}
				sessions++
				return true
			})
			if sessions != boundary.sessionCalls {
				t.Fatalf("R3 PUBLICATION SHAPE: %s direct DB Session calls between %s and %s = %d, want %d", boundary.label, boundary.stage, boundary.head, sessions, boundary.sessionCalls)
			}
			if cqlCalls != boundary.cqlCalls {
				t.Fatalf("R3 PUBLICATION SHAPE: %s direct CQL callsites between %s and %s = %d, want %d", boundary.label, boundary.stage, boundary.head, cqlCalls, boundary.cqlCalls)
			}
		})
	}
}

// TestR3MaterializationHasNoUnlistedDirectDBCall keeps the mandatory pin/fence
// handshake behind its existing seams and rejects a direct post-metadata read
// being appended to RegisterUploadedBlockTarget, including a local alias or
// method value of h.db. The test is intentionally not a statement that the
// helper has zero I/O: its established seams perform the required pin, fence,
// and metadata work.
func TestR3MaterializationHasNoUnlistedDirectDBCall(t *testing.T) {
	root := r3RepositoryRoot(t)
	file := r3ParseProductionFile(t, filepath.Join(root, "internal", "api", "v2", "fs_helpers.go"))
	fn := r3FindProductionFunction(t, file, "RegisterUploadedBlockTarget")
	aliases, methodValues := r3CollectDBSurface(fn)
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := r3PublicationCallName(call)
		if methodValues[name] {
			t.Fatalf("R3 MATERIALIZATION BUDGET: db method value %s in RegisterUploadedBlockTarget is unlisted", name)
		}
		if text, ok := r3CallCQLText(call); ok {
			normalized := strings.ToLower(strings.ReplaceAll(text, `"`, ""))
			if r3AuthoritySelectPattern.MatchString(normalized) || r3CQLVerbPattern.MatchString(normalized) {
				t.Fatalf("R3 MATERIALIZATION BUDGET: direct CQL in RegisterUploadedBlockTarget is unlisted")
			}
		}
		if method, directDB := r3DirectDBMethodOn(call, aliases); directDB {
			t.Fatalf("R3 MATERIALIZATION BUDGET: direct DB method %s in RegisterUploadedBlockTarget is unlisted", method)
		}
		return true
	})
}

func r3CallsNamedInRangeBody(rangeStmt *ast.RangeStmt, name string) int {
	count := 0
	ast.Inspect(rangeStmt.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if ok && r3PublicationCallName(call) == name {
			count++
		}
		return true
	})
	return count
}

func r3AssertLoopHasOnlyListedCalls(t *testing.T, rangeStmt *ast.RangeStmt, function, loopName, authorizedSink string, allowed map[string]bool) {
	t.Helper()
	ast.Inspect(rangeStmt.Body, func(node ast.Node) bool {
		if _, nested := node.(*ast.FuncLit); nested {
			t.Fatalf("R3 FANOUT: %s loop over %s has a nested FuncLit; the authorized sink is %s", function, loopName, authorizedSink)
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := r3PublicationCallName(call)
		if name == "" {
			// Conversions such as []string(nil) are CallExprs without a callable name.
			return true
		}
		if !allowed[name] {
			t.Fatalf("R3 FANOUT: %s loop over %s has unlisted call %s; the authorized sink is %s", function, loopName, name, authorizedSink)
		}
		return true
	})
}

func r3RangeIteratesName(expr ast.Expr, name string) bool {
	switch value := expr.(type) {
	case *ast.Ident:
		return value.Name == name
	case *ast.CallExpr:
		if r3PublicationCallName(value) != "NormalizeBlockIDs" || len(value.Args) != 1 {
			return false
		}
		ident, ok := value.Args[0].(*ast.Ident)
		return ok && ident.Name == name
	default:
		return false
	}
}

func r3RangeOverName(fn *ast.FuncDecl, name string) *ast.RangeStmt {
	var found *ast.RangeStmt
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		rangeStmt, ok := node.(*ast.RangeStmt)
		if !ok {
			return true
		}
		if r3RangeIteratesName(rangeStmt.X, name) && found == nil {
			found = rangeStmt
		}
		return true
	})
	return found
}

// TestR3PublicationKnownFanoutIsSinglePass is the limited multiplicity part of
// the cost contract. The typed budget counts static callsites; this companion
// freezes the authorized sink in two known staging loops, fail-closes any other
// named call or nested FuncLit in those loop bodies, and requires that the
// allowed helper seams do not themselves reach a publication primitive. It does
// not claim to derive arbitrary loop bounds or count physical Cassandra requests.
func TestR3PublicationKnownFanoutIsSinglePass(t *testing.T) {
	root := r3RepositoryRoot(t)
	v2Dir := filepath.Join(root, "internal", "api", "v2")
	v2Functions := r3ParseProductionPackage(t, v2Dir)
	v2Stage := v2Functions["stagePendingPublishedFiles"]
	if v2Stage == nil {
		t.Fatal("R3 FANOUT: stagePendingPublishedFiles not found")
	}
	v2Range := r3RangeOverName(v2Stage, "pendingFiles")
	if v2Range == nil {
		t.Fatal("R3 FANOUT: pendingFiles loop not found")
	}
	if got := r3CallsNamedInRangeBody(v2Range, "stagePendingPublishedFilesAddReferencesFn"); got != 1 {
		t.Fatalf("R3 FANOUT: stagePendingPublishedFiles calls AddReferences %d times per pending file, want 1", got)
	}
	r3AssertLoopHasOnlyListedCalls(t, v2Range, "stagePendingPublishedFiles", "pendingFiles", "stagePendingPublishedFilesAddReferencesFn", map[string]bool{
		"Errorf":                                    true,
		"NormalizeBlockIDs":                         true,
		"append":                                    true,
		"rollbackStagedRefs":                        true,
		"stagePendingPublishedFilesAddReferencesFn": true,
		"stagePendingPublishedFilesPersistFn":       true,
		"stagePendingPublishedFilesResolveFn":       true,
	})
	lits := r3ParseProductionValueFuncLits(t, v2Dir)
	authorized := "stagePendingPublishedFilesAddReferencesFn"
	r3AssertNoPublicationSink(t, lits["stagePendingPublishedFilesPersistFn"], "stagePendingPublishedFilesPersistFn", authorized)
	r3AssertNoPublicationSink(t, lits["stagePendingPublishedFilesResolveFn"], "stagePendingPublishedFilesResolveFn", authorized)
	r3AssertNoPublicationSink(t, lits["resolveStoredBlockIDsFn"], "resolveStoredBlockIDsFn", authorized)
	if create := v2Functions["createPendingPublishedFileRow"]; create != nil {
		r3AssertNoPublicationSink(t, create.Body, "createPendingPublishedFileRow", authorized)
	}
	if resolve := v2Functions["resolveStoredBlockIDs"]; resolve != nil {
		r3AssertNoPublicationSink(t, resolve.Body, "resolveStoredBlockIDs", authorized)
	}
	r3AssertNoPublicationSink(t, r3LocalAssignedFuncLit(v2Stage, "rollbackStagedRefs"), "rollbackStagedRefs", authorized)

	dbFunctions := r3ParseProductionPackage(t, filepath.Join(root, "internal", "db"))
	dbStage := dbFunctions["addPublishAttemptReferencesRows"]
	if dbStage == nil {
		t.Fatal("R3 FANOUT: addPublishAttemptReferencesRows not found")
	}
	dbRange := r3RangeOverName(dbStage, "blockIDs")
	if dbRange == nil {
		t.Fatal("R3 FANOUT: normalized blockIDs loop not found")
	}
	if got := r3CallsNamedInRangeBody(dbRange, "addPublishAttemptReferenceFn"); got != 1 {
		t.Fatalf("R3 FANOUT: addPublishAttemptReferencesRows calls addPublishAttemptReferenceFn %d times per block, want 1", got)
	}
	r3AssertLoopHasOnlyListedCalls(t, dbRange, "addPublishAttemptReferencesRows", "blockIDs", "addPublishAttemptReferenceFn", map[string]bool{
		"addPublishAttemptReferenceFn": true,
		"append":                       true,
	})
}
