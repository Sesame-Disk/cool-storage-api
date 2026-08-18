package integration

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestR11bCanonicalOrphanRepresentationSurface keeps representation_id out of
// the physical GC orphan table while allowing the identifier in blocks, mapping
// domains, queue state and library metadata. This is intentionally a source
// gate: the effective Cassandra schema gate lives in the integration suite.
func TestR11bCanonicalOrphanRepresentationSurface(t *testing.T) {
	root := filepath.Join("..", "..")
	skipDirs := map[string]bool{
		".git": true, "frontend": true, "mobile-frontend": true,
		"node_modules": true, "vendor": true,
	}
	// `_` is a word character, so `gc_s3_orphans\b` already cannot match inside
	// `gc_s3_orphans_by_day` — the same boundary the R22a gate relies on. Skipping
	// literals that merely mention the projection would therefore buy nothing and
	// would open an evasion: one literal naming both tables would carry a canonical
	// violation past the gate.
	canonicalOrphanTable := regexp.MustCompile(`(?i)\bgc_s3_orphans\b`)

	scanned := 0
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			if skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		scanned++
		source, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
		if err != nil {
			t.Errorf("%s: parse: %v", path, err)
			return nil
		}
		for _, query := range stringLiteralsIn(file) {
			if !canonicalOrphanTable.MatchString(query) {
				continue
			}
			if strings.Contains(strings.ToLower(query), "representation_id") {
				t.Errorf("%s: canonical gc_s3_orphans statement names representation_id: %s", path, strings.Join(strings.Fields(query), " "))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan production Go sources: %v", err)
	}
	if scanned == 0 {
		t.Fatal("scanned no production Go sources; the gate would pass vacuously")
	}
}
