package v2

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"testing"
)

func TestFileViewCanonicalReadRouting(t *testing.T) {
	targets := map[string]map[string]canonicalReadExpectations{
		"fileview.go": {
			"ServeRawFile":         {constructors: 2, batchResolutions: 2, directReaderCalls: 1, sizeQueries: 1, readSeekers: 1, streams: 1},
			"DownloadHistoricFile": {constructors: 1, batchResolutions: 1, streams: 1},
			"ServeHistoricFileRaw": {constructors: 1, batchResolutions: 1, streams: 1},
		},
		"sharelink_view.go": {
			"handleShareLinkRaw": {constructors: 1, batchResolutions: 1, sizeQueries: 1, readSeekers: 1, streams: 1},
			// One GetBlock, not two: the encrypted and plaintext branches used to
			// duplicate the same read and differ only in the decrypt step.
			"readFileContentAsText": {constructors: 1, batchResolutions: 1, directBlockCalls: 1},
		},
	}

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to resolve current file path")
	}
	pkgDir := filepath.Dir(thisFile)

	for filename, functions := range targets {
		t.Run(filename, func(t *testing.T) {
			fset := token.NewFileSet()
			fileNode, err := parser.ParseFile(fset, filepath.Join(pkgDir, filename), nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", filename, err)
			}

			for functionName, want := range functions {
				t.Run(functionName, func(t *testing.T) {
					fn := findFunction(t, fileNode, functionName)
					assertCanonicalReadRouting(t, fn, want)
				})
			}
		})
	}
}

type canonicalReadExpectations struct {
	constructors      int
	batchResolutions  int
	directReaderCalls int
	directBlockCalls  int
	sizeQueries       int
	readSeekers       int
	streams           int
}

func findFunction(t *testing.T, fileNode *ast.File, name string) *ast.FuncDecl {
	t.Helper()
	for _, decl := range fileNode.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == name {
			return fn
		}
	}
	t.Fatalf("function %s not found", name)
	return nil
}

func assertCanonicalReadRouting(t *testing.T, fn *ast.FuncDecl, want canonicalReadExpectations) {
	t.Helper()
	var got canonicalReadExpectations
	var lastBatchPosition token.Pos

	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		receiver, _ := selector.X.(*ast.Ident)
		switch selector.Sel.Name {
		case "BatchResolveBlockIDs", "BatchResolveBlockIDsContext":
			if receiver != nil && receiver.Name == "streaming" {
				got.batchResolutions++
				lastBatchPosition = call.Pos()
			}
		case "NewCanonicalBlockReader":
			if receiver == nil || receiver.Name != "streaming" {
				break
			}
			got.constructors++
			if lastBatchPosition == token.NoPos || lastBatchPosition > call.Pos() {
				t.Errorf("NewCanonicalBlockReader must follow block ID resolution")
			}
			assertIdentifierArgument(t, call, 5, "blockStore")
			assertIdentifierArgument(t, call, 6, "blockStoreClass")
		case "GetBlockReader":
			got.directReaderCalls++
			if receiver == nil || receiver.Name != "canonicalReader" {
				t.Errorf("GetBlockReader receiver = %v, want canonicalReader", selector.X)
			}
		case "GetBlock":
			got.directBlockCalls++
			if receiver == nil || receiver.Name != "canonicalReader" {
				t.Errorf("GetBlock receiver = %v, want canonicalReader", selector.X)
			}
		case "QueryBlockSizes":
			if receiver != nil && receiver.Name == "streaming" {
				got.sizeQueries++
				assertIdentifierArgument(t, call, 3, "canonicalReader")
			}
		case "NewBlockReadSeeker":
			if receiver != nil && receiver.Name == "streaming" {
				got.readSeekers++
				assertIdentifierArgument(t, call, 1, "canonicalReader")
			}
		case "StreamBlocks":
			if receiver != nil && receiver.Name == "streaming" {
				got.streams++
				assertIdentifierArgument(t, call, 2, "canonicalReader")
			}
		}
		return true
	})

	if got != want {
		t.Errorf("canonical read call counts = %+v, want %+v", got, want)
	}
}

func assertIdentifierArgument(t *testing.T, call *ast.CallExpr, index int, want string) {
	t.Helper()
	if len(call.Args) <= index {
		t.Errorf("%s call has %d arguments, want argument %d", callName(call), len(call.Args), index)
		return
	}
	identifier, ok := call.Args[index].(*ast.Ident)
	if !ok || identifier.Name != want {
		t.Errorf("%s argument %d = %T, want identifier %s", callName(call), index, call.Args[index], want)
	}
}

func callName(call *ast.CallExpr) string {
	if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
		return selector.Sel.Name
	}
	return "call"
}
