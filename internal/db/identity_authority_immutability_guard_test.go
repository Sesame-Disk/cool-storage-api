package db

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const identityAuthorityClaimsTable = "identity_authority_claims"

// Freeze the actual executed projections, not just the presence of table literals.
const identityAuthorityExactInsert = "INSERT INTO identity_authority_claims (library_id, identity_kind, identity_id, digest_version, digest, created_at) VALUES (?, ?, ?, ?, ?, ?) IF NOT EXISTS"
const identityAuthorityExactSelect = "SELECT digest_version, digest, created_at FROM identity_authority_claims WHERE library_id = ? AND identity_kind = ? AND identity_id = ?"

var (
	identityAuthorityInsertPattern = regexp.MustCompile(`(?is)^INSERT\s+INTO\s+identity_authority_claims\s*\([^)]*\)\s*VALUES\s*\([^)]*\)\s*IF\s+NOT\s+EXISTS$`)
	identityAuthoritySelectPattern = regexp.MustCompile(`(?is)^SELECT\s+.+\s+FROM\s+identity_authority_claims\s+WHERE\s+.+$`)
	identityAuthorityCreatePattern = regexp.MustCompile(`(?is)^CREATE\s+TABLE\s+IF\s+NOT\s+EXISTS\s+identity_authority_claims\b`)
	identityAuthorityCQLComments   = regexp.MustCompile(`(?m)--[^\r\n]*`)
)

func constantIdentityAuthorityString(expr ast.Expr) (string, bool) {
	switch value := expr.(type) {
	case *ast.BasicLit:
		if value.Kind != token.STRING {
			return "", false
		}
		unquoted, err := strconv.Unquote(value.Value)
		return unquoted, err == nil
	case *ast.ParenExpr:
		return constantIdentityAuthorityString(value.X)
	case *ast.BinaryExpr:
		if value.Op != token.ADD {
			return "", false
		}
		left, leftOK := constantIdentityAuthorityString(value.X)
		right, rightOK := constantIdentityAuthorityString(value.Y)
		if leftOK && rightOK {
			return left + right, true
		}
	}
	return "", false
}

func identityAuthorityProductionGoFiles(t *testing.T, repoRoot string) []string {
	t.Helper()
	var paths []string
	walkErr := filepath.WalkDir(repoRoot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != repoRoot {
				switch entry.Name() {
				case ".git", "vendor", "node_modules", "dist", "build":
					return filepath.SkipDir
				}
			}
			return nil
		}
		if strings.HasSuffix(entry.Name(), ".go") && !strings.HasSuffix(entry.Name(), "_test.go") {
			paths = append(paths, path)
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("PCD1B CLAIM IMMUTABILITY: walk repository: %v", walkErr)
	}
	sort.Strings(paths)
	return paths
}

func identityAuthorityGoStrings(t *testing.T, path string) []string {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("PCD1B CLAIM IMMUTABILITY: parse %s: %v", path, err)
	}
	var values []string
	ast.Inspect(parsed, func(node ast.Node) bool {
		expr, ok := node.(ast.Expr)
		if !ok {
			return true
		}
		if value, ok := constantIdentityAuthorityString(expr); ok {
			values = append(values, value)
		}
		return true
	})
	return values
}

// TestIdentityAuthorityClaimsAreImmutableRepositoryWide freezes every current
// production/schema reference to the claim table. The runtime may insert a
// first claim and read it; no other CQL operation, TTL, or schema change may
// reset or retire a claim until a separately reviewed retirement protocol lands.
func TestIdentityAuthorityClaimsAreImmutableRepositoryWide(t *testing.T) {
	repoRoot := r3RepositoryRoot(t)
	relPrimitive := "internal/db/identity_authority.go"
	var violations []string
	insertCount, selectCount := 0, 0

	for _, path := range identityAuthorityProductionGoFiles(t, repoRoot) {
		relPath, err := filepath.Rel(repoRoot, path)
		if err != nil {
			t.Fatalf("PCD1B CLAIM IMMUTABILITY: relative path %s: %v", path, err)
		}
		relPath = filepath.ToSlash(relPath)
		literals := identityAuthorityGoStrings(t, path)
		var references []string
		var allLiterals strings.Builder
		for _, value := range literals {
			allLiterals.WriteString(value)
			if strings.Contains(strings.ToLower(value), identityAuthorityClaimsTable) {
				references = append(references, value)
			}
		}
		// Joining constant string values also catches a table identifier split
		// across adjacent CQL string literals in a production file.
		if relPath != relPrimitive && strings.Contains(strings.ToLower(allLiterals.String()), identityAuthorityClaimsTable) {
			violations = append(violations, relPath+": unauthorized production reference")
		}
		if relPath == relPrimitive {
			for _, statement := range references {
				normalized := strings.TrimSpace(statement)
				switch {
				case identityAuthorityInsertPattern.MatchString(normalized) && !strings.Contains(strings.ToUpper(normalized), "USING TTL"):
					insertCount++
				case identityAuthoritySelectPattern.MatchString(normalized):
					selectCount++
				default:
					violations = append(violations, relPath+": unauthorized operation on "+identityAuthorityClaimsTable)
				}
			}
		}
	}

	if insertCount != 1 {
		violations = append(violations, "runtime INSERT IF NOT EXISTS count="+strconv.Itoa(insertCount)+", want 1")
	}
	if selectCount != 1 {
		violations = append(violations, "runtime SELECT count="+strconv.Itoa(selectCount)+", want 1")
	}

	migrationsRoot := filepath.Join(repoRoot, "internal", "db", "migrations")
	createCount := 0
	walkErr := filepath.WalkDir(migrationsRoot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".cql") {
			return nil
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		content = identityAuthorityCQLComments.ReplaceAll(content, nil)
		relPath, relErr := filepath.Rel(repoRoot, path)
		if relErr != nil {
			return relErr
		}
		for _, statement := range strings.Split(string(content), ";") {
			normalized := strings.TrimSpace(statement)
			if !strings.Contains(strings.ToLower(normalized), identityAuthorityClaimsTable) {
				continue
			}
			allowedCreate := strings.HasSuffix(filepath.ToSlash(relPath), "/001_initial_schema.cql") &&
				identityAuthorityCreatePattern.MatchString(normalized)
			if !allowedCreate || strings.Contains(strings.ToLower(normalized), "default_time_to_live") || strings.Contains(strings.ToUpper(normalized), "USING TTL") {
				violations = append(violations, filepath.ToSlash(relPath)+": unauthorized operation on "+identityAuthorityClaimsTable)
				continue
			}
			createCount++
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("PCD1B CLAIM IMMUTABILITY: walk migrations: %v", walkErr)
	}
	if createCount != 1 {
		violations = append(violations, "schema CREATE TABLE count="+strconv.Itoa(createCount)+", want 1")
	}
	violations = append(violations, authorityExecutionInventory(t)...)
	sort.Strings(violations)
	if len(violations) > 0 {
		t.Fatalf("PCD1B CLAIM IMMUTABILITY: unauthorized operation on %s: %v", identityAuthorityClaimsTable, violations)
	}
}
