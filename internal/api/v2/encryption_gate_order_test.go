package v2

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"testing"
)

// The encryption gate has to run before anything that can answer the request or
// commit bytes. Ordering is the property that broke: ServeRawFile probed AFTER
// its ETag revalidation, so a 304 re-authorised a cached plaintext copy once the
// decrypt session had expired, and hid the probe-failure 503 from any request
// carrying an If-None-Match. A unit test on the helper cannot catch that — only
// the relative position of the two calls inside the handler can.
func TestEncryptionGateRunsBeforeShortCircuitsAndWrites(t *testing.T) {
	cases := []struct {
		file       string
		function   string
		gate       string
		mustFollow string
		why        string
	}{
		{
			file: "fileview.go", function: "ServeRawFile",
			gate: "libraryIsEncrypted", mustFollow: "setCacheHeaders",
			why: "a 304 would re-authorise cached plaintext after the session expired",
		},
		{
			file: "sharelink_view.go", function: "handleShareLinkRaw",
			gate: "libraryIsEncryptedContext", mustFollow: "setCacheHeaders",
			why: "same ETag short circuit on the public share-link surface",
		},
		{
			file: "fileview.go", function: "ServeHistoricFileRaw",
			gate: "GetFileKeyAndIV", mustFollow: "setCacheHeaders",
			why: "historic raw serves bytes for an encrypted library too",
		},
		{
			file: "files.go", function: "UploadFile",
			gate: "libraryIsEncrypted", mustFollow: "RetryUploadedBlockMaterializationContext",
			why: "failing open here would store plaintext into an encrypted library",
		},
	}

	_, thisFile, ok := callerFile()
	if !ok {
		t.Fatal("failed to resolve current file path")
	}
	pkgDir := filepath.Dir(thisFile)

	for _, tc := range cases {
		t.Run(tc.function, func(t *testing.T) {
			fset := token.NewFileSet()
			fileNode, err := parser.ParseFile(fset, filepath.Join(pkgDir, tc.file), nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", tc.file, err)
			}
			fn := findFunction(t, fileNode, tc.function)

			gatePos := firstCallPos(fn, tc.gate)
			followPos := firstCallPos(fn, tc.mustFollow)
			if gatePos == token.NoPos {
				t.Fatalf("%s does not call %s at all", tc.function, tc.gate)
			}
			if followPos == token.NoPos {
				t.Fatalf("%s does not call %s; update this test if the handler was restructured", tc.function, tc.mustFollow)
			}
			if gatePos > followPos {
				t.Fatalf("%s calls %s (line %d) before the encryption gate %s (line %d): %s",
					tc.function, tc.mustFollow, fset.Position(followPos).Line,
					tc.gate, fset.Position(gatePos).Line, tc.why)
			}
		})
	}
}

func callerFile() (string, string, bool) {
	_, file, _, ok := runtime.Caller(1)
	return "", file, ok
}

// firstCallPos returns the position of the first call whose callee name matches,
// whether it is a plain identifier or a selector such as x.Method().
func firstCallPos(fn *ast.FuncDecl, name string) token.Pos {
	found := token.NoPos
	ast.Inspect(fn, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		var callee string
		switch target := call.Fun.(type) {
		case *ast.Ident:
			callee = target.Name
		case *ast.SelectorExpr:
			callee = target.Sel.Name
		}
		if callee == name && (found == token.NoPos || call.Pos() < found) {
			found = call.Pos()
		}
		return true
	})
	return found
}
