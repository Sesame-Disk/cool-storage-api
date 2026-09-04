package gc

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// The library lease is now the cross-DC lifecycle fence, not merely a local
// hard-delete mutex. Every LWT shape in the generic helper must therefore pin
// both its regular acknowledgement and its serial domain explicitly.
func TestLibraryLifecycleFencePinsGlobalConsistency(t *testing.T) {
	source, err := os.ReadFile("store_cassandra.go")
	if err != nil {
		t.Fatalf("read Cassandra store: %v", err)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "store_cassandra.go", source, 0)
	if err != nil {
		t.Fatalf("parse Cassandra store: %v", err)
	}
	want := map[string]int{
		"acquireHardDeleteLock": 2, // initial insert plus stale takeover
		"renewHardDeleteLock":   1,
		"releaseHardDeleteLock": 1,
	}
	for functionName, wantLWTs := range want {
		var function *ast.FuncDecl
		for _, decl := range file.Decls {
			if candidate, ok := decl.(*ast.FuncDecl); ok && candidate.Name.Name == functionName {
				function = candidate
				break
			}
		}
		if function == nil {
			t.Fatalf("%s not found", functionName)
		}
		var formatted bytes.Buffer
		if err := format.Node(&formatted, fset, function); err != nil {
			t.Fatalf("format %s: %v", functionName, err)
		}
		text := formatted.String()
		if got := strings.Count(text, "MapScanCAS("); got != wantLWTs {
			t.Fatalf("%s has %d MapScanCAS calls, want %d", functionName, got, wantLWTs)
		}
		if got := strings.Count(text, "Consistency(gocql.EachQuorum)"); got != wantLWTs {
			t.Errorf("%s pins EACH_QUORUM %d times, want %d", functionName, got, wantLWTs)
		}
		if got := strings.Count(text, "SerialConsistency(gocql.Serial)"); got != wantLWTs {
			t.Errorf("%s pins SERIAL %d times, want %d", functionName, got, wantLWTs)
		}
	}
}
