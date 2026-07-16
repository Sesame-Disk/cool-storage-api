package integration

// This guard deliberately carries NO `integration` build tag: it is a static source
// scan, needs no Cassandra/MinIO, and must run in the normal `go test ./...` CI pass
// so a reintroduced global fan-out fails fast instead of waiting for a tagged run
// against a live backend.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// guardOwnFile is skipped by the scan below: it necessarily contains the very
// pattern it forbids.
const guardOwnFile = "gc_global_fanout_guard_test.go"

// forbiddenGCFanout is assembled at runtime rather than written as a literal so the
// needle cannot match itself if this file is ever renamed and the skip above misses.
var forbiddenGCFanout = "." + "ProcessOnce("

// TestNoGlobalGCFanoutInIntegrationSuite fails if any test in this package calls
// Worker.ProcessOnce.
//
// ProcessOnce drains EVERY org that has queued GC work. Integration tests build their
// worker with storage=nil, so a global drain deletes other tests' Cassandra rows while
// leaving their S3 objects behind — manufacturing exactly the "eternal residue" that
// the 2026-07 delete audit spent weeks separating from real production leaks. Tests
// must scope their drain with ProcessOrgOnce(ctx, orgID) instead.
//
// See ISSUE-GC-TEST-RESIDUE-01 (branch 1C) and docs/GC-DELETE-CLEANUP-INVESTIGATION.md
// "Verdict 2".
func TestNoGlobalGCFanoutInIntegrationSuite(t *testing.T) {
	matches, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob package sources: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("no Go sources found; the guard would silently pass")
	}

	scanned := 0
	for _, path := range matches {
		if filepath.Base(path) == guardOwnFile {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		scanned++
		for i, line := range strings.Split(string(src), "\n") {
			if strings.Contains(line, forbiddenGCFanout) {
				t.Errorf("%s:%d calls Worker.ProcessOnce, which drains every org's GC "+
					"queue. With storage=nil that deletes other tests' DB rows and orphans "+
					"their S3 objects. Use ProcessOrgOnce(ctx, orgID) to scope the drain to "+
					"the org under test. See ISSUE-GC-TEST-RESIDUE-01.\n\t%s",
					path, i+1, strings.TrimSpace(line))
			}
		}
	}

	if scanned == 0 {
		t.Fatal("scanned no files besides the guard itself; the guard would silently pass")
	}
}
