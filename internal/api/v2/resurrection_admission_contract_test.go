package v2

import (
	"errors"
	"fmt"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// Every query must be inside the callback passed to the tested fallback helper,
// and its last consistency call must bind the callback's actual parameter.
func historicalAdmissionReadContract(raw []byte) error {
	file, err := parser.ParseFile(token.NewFileSet(), "source.go", raw, 0)
	if err != nil {
		return err
	}
	tables := map[string]int{"fs_objects": 0, "libraries": 0, "block_id_mappings": 0, "blocks": 0, "block_references": 0}
	queries, wrappers := 0, 0
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Query" {
			queries++
		}
		ident, ok := call.Fun.(*ast.Ident)
		if !ok || ident.Name != "readHistoricalAdmissionRow" {
			return true
		}
		wrappers++
		if len(call.Args) != 1 {
			return true
		}
		callback, ok := call.Args[0].(*ast.FuncLit)
		if !ok || len(callback.Type.Params.List) != 1 || len(callback.Type.Params.List[0].Names) != 1 {
			return true
		}
		parameter := callback.Type.Params.List[0].Names[0].Name
		ast.Inspect(callback.Body, func(n ast.Node) bool {
			scan, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			method, ok := scan.Fun.(*ast.SelectorExpr)
			if !ok || method.Sel.Name != "Scan" {
				return true
			}
			var expr ast.Expr = method.X
			seen, pinned := false, false
			for {
				call, ok := expr.(*ast.CallExpr)
				if !ok {
					break
				}
				method, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					break
				}
				if method.Sel.Name == "Consistency" && !seen {
					seen = true
					if len(call.Args) == 1 {
						arg, ok := call.Args[0].(*ast.Ident)
						pinned = ok && arg.Name == parameter
					}
				}
				if method.Sel.Name == "Query" && len(call.Args) > 0 {
					if lit, ok := call.Args[0].(*ast.BasicLit); ok && pinned {
						query, _ := strconv.Unquote(lit.Value)
						words := strings.Fields(strings.ToLower(query))
						for i, word := range words {
							if word == "from" && i+1 < len(words) {
								if _, ok := tables[words[i+1]]; ok {
									tables[words[i+1]]++
								}
							}
						}
					}
					break
				}
				expr = method.X
			}
			return true
		})
		return true
	})
	// E1-10e added five reads; E1-17 adds the strict historical directory read.
	if wrappers != 6 || queries != 6 {
		return fmt.Errorf("scoped fallback wrappers=%d queries=%d", wrappers, queries)
	}
	// fs_objects is read twice: the historical file layout (E1-10e) and the
	// strict historical directory listing (E1-17). Every other table once.
	expected := map[string]int{"fs_objects": 2, "libraries": 1, "block_id_mappings": 1, "blocks": 1, "block_references": 1}
	for table, count := range tables {
		if count != expected[table] {
			return fmt.Errorf("%s callback-bound reads=%d", table, count)
		}
	}
	return nil
}

func TestHistoricalAdmissionReadFallback(t *testing.T) {
	transport := errors.New("transport")
	for _, tc := range []struct {
		name                string
		local, global, want error
		levels              []gocql.Consistency
	}{
		{"local-hit", nil, transport, nil, []gocql.Consistency{gocql.LocalQuorum}},
		{"local-error", transport, nil, transport, []gocql.Consistency{gocql.LocalQuorum}},
		{"global-hit", gocql.ErrNotFound, nil, nil, []gocql.Consistency{gocql.LocalQuorum, gocql.EachQuorum}},
		{"global-miss", gocql.ErrNotFound, gocql.ErrNotFound, gocql.ErrNotFound, []gocql.Consistency{gocql.LocalQuorum, gocql.EachQuorum}},
		{"global-error", gocql.ErrNotFound, transport, transport, []gocql.Consistency{gocql.LocalQuorum, gocql.EachQuorum}},
		{"wrapped-local-miss", fmt.Errorf("lookup: %w", gocql.ErrNotFound), nil, nil, []gocql.Consistency{gocql.LocalQuorum, gocql.EachQuorum}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var levels []gocql.Consistency
			err := readHistoricalAdmissionRow(func(c gocql.Consistency) error {
				levels = append(levels, c)
				if len(levels) == 1 {
					return tc.local
				}
				return tc.global
			})
			if !errors.Is(err, tc.want) || !reflect.DeepEqual(levels, tc.levels) {
				t.Fatalf("err=%v levels=%v want err=%v levels=%v", err, levels, tc.want, tc.levels)
			}
		})
	}
}

func TestHistoricalAdmissionReadsUseScopedFallback(t *testing.T) {
	raw, err := os.ReadFile(r3SourcePath("internal", "api", "v2", "resurrection_admission.go"))
	if err != nil {
		t.Fatal(err)
	}
	if err := historicalAdmissionReadContract(raw); err != nil {
		t.Fatal(err)
	}
	for _, bypass := range []string{"resolveStoredBlockIDs", "captureCopiedBlockPlacements", "ResolveBlockRepresentationID", "GetBlockIDMappingContext", "ReadFSObjectIdentitySourceRow"} {
		if strings.Contains(string(raw), bypass+"(") {
			t.Fatalf("local resolver bypass: %s", bypass)
		}
	}
	// Mutate each actual read independently, including a later override.
	pin := ".Consistency(consistency)"
	offset := 0
	for i := 0; i < 6; i++ {
		pos := strings.Index(string(raw[offset:]), pin)
		if pos < 0 {
			t.Fatal("missing target pin")
		}
		pos += offset
		for name, replacement := range map[string]string{"inherited": "", "ONE": ".Consistency(gocql.One)", "always-EQ": ".Consistency(gocql.EachQuorum)", "local-only": ".Consistency(gocql.LocalQuorum)", "override": pin + ".Consistency(gocql.One)"} {
			t.Run(fmt.Sprintf("read-%d/%s", i, name), func(t *testing.T) {
				mutated := string(raw[:pos]) + replacement + string(raw[pos+len(pin):])
				if _, err := parser.ParseFile(token.NewFileSet(), "mutation.go", mutated, 0); err != nil {
					t.Fatal(err)
				}
				if err := historicalAdmissionReadContract([]byte(mutated)); err == nil {
					t.Fatal("admission consistency bypass accepted")
				}
			})
		}
		offset = pos + len(pin)
	}
}

// A historical directory is admitted only from a typed, well-formed listing.
// The empty encodings are a real empty directory; anything else that cannot be
// read as a listing fails closed instead of becoming an empty tree.
func TestParseRetainedHistoricalDirectory(t *testing.T) {
	str := func(s string) *string { return &s }
	for _, tc := range []struct {
		name    string
		kind    string
		raw     *string
		entries int
		wantErr bool
	}{
		{"null-column", "dir", nil, 0, false},
		{"empty-string", "dir", str(""), 0, false},
		{"empty-array", "dir", str("[]"), 0, false},
		{"marshalled-nil", "dir", str("null"), 0, false},
		{"two-entries", "dir", str(`[{"id":"a1","mode":33188,"name":"f"},{"id":"b2","mode":16384,"name":"d"}]`), 2, false},
		{"file-object", "file", str("[]"), 0, true},
		{"malformed", "dir", str(`[{"id":`), 0, true},
		{"entry-without-id", "dir", str(`[{"mode":33188,"name":"f"}]`), 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries, err := parseRetainedHistoricalDirectory("fs", tc.kind, tc.raw)
			if (err != nil) != tc.wantErr || len(entries) != tc.entries {
				t.Fatalf("entries=%d err=%v, want entries=%d error=%t", len(entries), err, tc.entries, tc.wantErr)
			}
		})
	}
}
