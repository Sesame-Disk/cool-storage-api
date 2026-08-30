//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestEveryEvidenceGateIsWiredIntoTestMain turns the rule stated above
// `requireEvidence` into something the suite can check.
//
// An evidence gate has two halves. The per-test half — `p4aRequireEvidence`,
// `r26RequireEvidence` — turns a SKIP into a failure once the test is reached. The
// TestMain half turns "the stack never came up" into a non-zero exit. Only the
// second one covers the case that matters most: with the backend unreachable,
// TestMain exits BEFORE any test runs, and a gate missing from that chain lets the
// run print "ok" having executed nothing at all. That is the precise false-green
// these variables exist to prevent.
//
// The rule was written as a comment, and a comment did not hold:
// SESAMEFS_REQUIRE_R26_EVIDENCE was added to docker-compose and to its own tests
// while the chain kept only P2/P3/P4A. In the standard Docker run the omission was
// invisible, because P4A is set alongside R26 and P4A *is* in the chain — so the
// evidence was never actually false, and nothing failed. A standalone
// `SESAMEFS_REQUIRE_R26_EVIDENCE=1 go test ...` against a dead stack would have
// exited 0.
//
// So this discovers the gates from the source instead of from a hand-kept list:
// any SESAMEFS_REQUIRE_*_EVIDENCE or SESAMEFS_REQUIRE_*_CHARACTERIZATION the
// package mentions must appear in TestMain's requireEvidence chain. Adding a
// gate and forgetting to wire it is now a test failure rather than a silent hole.
func TestEveryEvidenceGateIsWiredIntoTestMain(t *testing.T) {
	const testMainFile = "integration_test.go"

	raw, err := os.ReadFile(testMainFile)
	if err != nil {
		t.Fatalf("read %s: %v", testMainFile, err)
	}
	chain, ok := requireEvidenceChain(string(raw))
	if !ok {
		t.Fatalf("could not locate the requireEvidence chain in %s; this guard is now vacuous", testMainFile)
	}

	gates := evidenceGatesReferencedInPackage(t)
	if len(gates) == 0 {
		t.Fatal("no SESAMEFS_REQUIRE_*_EVIDENCE variables found in the package; this guard is vacuous")
	}

	for _, gate := range gates {
		if strings.Contains(chain, gate) {
			continue
		}
		t.Errorf("EVIDENCE GATE NOT WIRED: %s is used by this package but is not in TestMain's requireEvidence chain.\n"+
			"Its per-test half still fails a SKIP once the test is reached, but with the backend unreachable "+
			"TestMain exits before any test runs — so a run demanding this evidence would print \"ok\" having "+
			"executed nothing. Add it to the chain in %s.", gate, testMainFile)
	}
}

var (
	evidenceGatePattern  = regexp.MustCompile(`SESAMEFS_REQUIRE_[A-Z0-9_]+_(?:EVIDENCE|CHARACTERIZATION)`)
	requireEvidenceStart = regexp.MustCompile(`requireEvidence\s*:?=`)
)

// requireEvidenceChain returns the text of the requireEvidence assignment: from the
// assignment to the first line that does not continue the `||` chain.
func requireEvidenceChain(source string) (string, bool) {
	loc := requireEvidenceStart.FindStringIndex(source)
	if loc == nil {
		return "", false
	}
	var chain []string
	for _, line := range strings.Split(source[loc[0]:], "\n") {
		chain = append(chain, line)
		// The chain ends on the first line that is not continued by `||`.
		if !strings.HasSuffix(strings.TrimSpace(strings.TrimSuffix(line, "\r")), "||") {
			break
		}
	}
	return strings.Join(chain, "\n"), true
}

func evidenceGatesReferencedInPackage(t *testing.T) []string {
	t.Helper()
	entries, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob package sources: %v", err)
	}
	seen := map[string]bool{}
	for _, file := range entries {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for _, gate := range evidenceGatePattern.FindAllString(string(raw), -1) {
			seen[gate] = true
		}
	}
	gates := make([]string, 0, len(seen))
	for gate := range seen {
		gates = append(gates, gate)
	}
	sort.Strings(gates)
	return gates
}
