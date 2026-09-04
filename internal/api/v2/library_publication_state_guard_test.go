package v2

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

type libraryPublicationStateGuardViolation struct {
	pos     token.Position
	message string
}

func (v libraryPublicationStateGuardViolation) String() string {
	return v.pos.String() + ": " + v.message
}

func libraryPublicationStateGuardInsertColumns(cql string) ([]string, bool) {
	normalized := strings.Join(strings.Fields(strings.ToLower(cql)), " ")
	if !strings.HasPrefix(normalized, "insert into ") {
		return nil, false
	}
	rest := strings.TrimPrefix(normalized, "insert into ")
	open := strings.Index(rest, "(")
	if open < 0 {
		return nil, false
	}
	table := strings.ReplaceAll(strings.TrimSpace(rest[:open]), "\"", "")
	parts := strings.Split(table, ".")
	if len(parts) == 0 || parts[len(parts)-1] != "libraries" {
		return nil, false
	}
	closeRel := strings.Index(rest[open+1:], ")")
	if closeRel < 0 {
		return nil, false
	}
	columnText := rest[open+1 : open+1+closeRel]
	rawColumns := strings.Split(columnText, ",")
	columns := make([]string, 0, len(rawColumns))
	for _, raw := range rawColumns {
		columns = append(columns, strings.Trim(strings.TrimSpace(raw), "\""))
	}
	return columns, true
}

func libraryPublicationStateGuardQueryLiteralText(call *ast.CallExpr) (string, bool) {
	if len(call.Args) == 0 {
		return "", false
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	text, err := strconv.Unquote(lit.Value)
	return text, err == nil
}

func libraryPublicationStateGuardIsActiveExpr(expr ast.Expr) bool {
	switch value := expr.(type) {
	case *ast.BasicLit:
		if value.Kind != token.STRING {
			return false
		}
		text, err := strconv.Unquote(value.Value)
		return err == nil && strings.EqualFold(strings.TrimSpace(text), "ACTIVE")
	case *ast.SelectorExpr:
		return value.Sel.Name == "LibraryPublicationStateActive"
	default:
		return false
	}
}

func checkLibraryPublicationStateCreators(fset *token.FileSet, file *ast.File) (violations []libraryPublicationStateGuardViolation, found int) {
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Query" {
			return true
		}
		cql, ok := libraryPublicationStateGuardQueryLiteralText(call)
		if !ok {
			return true
		}
		columns, ok := libraryPublicationStateGuardInsertColumns(cql)
		if !ok {
			return true
		}
		found++
		pos := fset.Position(call.Pos())
		publicationIndex := -1
		for i, column := range columns {
			if column == "publication_state" {
				publicationIndex = i
				break
			}
		}
		if publicationIndex < 0 {
			violations = append(violations, libraryPublicationStateGuardViolation{pos, "creates a libraries row without an explicit publication_state = ACTIVE bind"})
			return true
		}
		bindIndex := publicationIndex + 1
		if bindIndex >= len(call.Args) || !libraryPublicationStateGuardIsActiveExpr(call.Args[bindIndex]) {
			violations = append(violations, libraryPublicationStateGuardViolation{pos, "creates a libraries row whose publication_state bind is not ACTIVE"})
		}
		return true
	})
	return violations, found
}

// Every production library creator must make publication authority explicit.
// This is a source contract for the clean-deploy invariant: a row that reaches
// Cassandra without ACTIVE authority is malformed and must not be introduced by
// a new creator. Dynamic CQL is intentionally out of scope until a creator uses
// that shape; static Query literals are the current production contract.
func TestEveryLibraryCreatorBindsActivePublicationState(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	fset := token.NewFileSet()
	totalFound := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		violations, found := checkLibraryPublicationStateCreators(fset, file)
		totalFound += found
		for _, violation := range violations {
			t.Error(violation.String())
		}
	}
	const wantCreators = 6
	if totalFound != wantCreators {
		t.Fatalf("found %d static libraries creators in package v2, want %d; update this count only after re-auditing every creator", totalFound, wantCreators)
	}
}
