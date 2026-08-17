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

// TestR22bProjectionPayloadIsUnreachable is the repo-wide half of R22b. The
// package-local gate in internal/gc pins the one writer; this one states the rule
// that outlives it: no production Go anywhere may name a dropped payload column in
// a statement that touches gc_s3_orphans_by_day.
//
// R22a made the projection non-authoritative by API — the payload could not reach
// a recovery decision because S3OrphanDiscoveryInfo had nowhere to put it. R22b
// made it non-authoritative by schema: migration 014 dropped storage_class,
// representation_id, external_sha1 and recovery_phase, so Cassandra itself now
// rejects the statement. This gate turns "Cassandra will reject it at runtime,
// probably in a GC sweep nobody is watching" into a test failure at the point the
// query is written.
//
// Scope, stated honestly: like the R21 and R22a gates, this scans string
// literals. CQL assembled by concatenation or a query builder would evade it. That
// is acceptable because every CQL statement in this repo is a literal today, and a
// builder would be a visible architectural change — but this gate is not a proof
// that no reachable Cassandra write exists, and the closure documents must not
// describe it as one.
func TestR22bProjectionPayloadIsUnreachable(t *testing.T) {
	root := filepath.Join("..", "..")
	skipDirs := map[string]bool{
		".git": true, "frontend": true, "mobile-frontend": true,
		"node_modules": true, "vendor": true,
	}
	projectionTable := regexp.MustCompile(`(?i)\bgc_s3_orphans_by_day\b`)
	droppedPayloadColumns := []string{"storage_class", "representation_id", "external_sha1", "recovery_phase"}

	scanned := 0
	statements := 0
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
			if !projectionTable.MatchString(query) {
				continue
			}
			statements++
			// Unquoted CQL identifiers are case-insensitive, so the column match
			// must be too — the table match above already is. Cassandra would
			// reject STORAGE_CLASS exactly as it rejects storage_class, and the
			// point of this gate is to fail here rather than mid-sweep.
			normalized := strings.ToLower(query)
			for _, forbidden := range droppedPayloadColumns {
				if strings.Contains(normalized, forbidden) {
					t.Errorf("%s: statement touching gc_s3_orphans_by_day names %q, dropped by migration 014: %s",
						path, forbidden, strings.Join(strings.Fields(query), " "))
				}
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
	// The INSERT, the DELETE and the discovery SELECT. Without this the gate also
	// passes vacuously if the statements move somewhere the walk does not reach.
	if statements < 3 {
		t.Errorf("found %d production statements naming gc_s3_orphans_by_day, want at least 3 (insert, delete, discovery select)", statements)
	}
}
