//go:build integration

package integration

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

const borrowedFSHeadCharacterizationEnv = "SESAMEFS_REQUIRE_BORROWEDFS_HEAD_CHARACTERIZATION"

// borrowedFSHeadEvidenceState records each characterization leg by name.
// Completeness is the conjunction of these fields, never a counter: marking one
// leg twice cannot hide another. TestMain inspects missing() after m.Run().
type borrowedFSHeadEvidenceState struct {
	currentHeadAfterCut        bool
	currentPubRevokesZeroProof bool
	currentPubAfterZeroProof   bool
	harnessWriterWins          bool
	harnessCutAfterClassify    bool
	harnessLatePubStillFenced  bool
	harnessLatePinStillFenced  bool
}

func (state borrowedFSHeadEvidenceState) namedLegs() []struct {
	name string
	seen bool
} {
	return []struct {
		name string
		seen bool
	}{
		{"currentHeadAfterCut", state.currentHeadAfterCut},
		{"currentPubRevokesZeroProof", state.currentPubRevokesZeroProof},
		{"currentPubAfterZeroProof", state.currentPubAfterZeroProof},
		{"harnessWriterWins", state.harnessWriterWins},
		{"harnessCutAfterClassify", state.harnessCutAfterClassify},
		{"harnessLatePubStillFenced", state.harnessLatePubStillFenced},
		{"harnessLatePinStillFenced", state.harnessLatePinStillFenced},
	}
}

func (state borrowedFSHeadEvidenceState) complete() bool {
	for _, leg := range state.namedLegs() {
		if !leg.seen {
			return false
		}
	}
	return true
}

func (state borrowedFSHeadEvidenceState) missing() []string {
	missing := make([]string, 0, 7)
	for _, leg := range state.namedLegs() {
		if !leg.seen {
			missing = append(missing, leg.name)
		}
	}
	return missing
}

var borrowedFSHeadEvidence borrowedFSHeadEvidenceState

type borrowedFSHeadEvidenceGate struct{ observed bool }

func borrowedFSRequireHeadEvidence(t *testing.T) *borrowedFSHeadEvidenceGate {
	t.Helper()
	gate := &borrowedFSHeadEvidenceGate{}
	if os.Getenv(borrowedFSHeadCharacterizationEnv) != "1" {
		return gate
	}
	t.Cleanup(func() {
		if t.Skipped() {
			t.Errorf("%s=1 requires real Cassandra+MinIO BorrowedFS HEAD evidence, but the test skipped", borrowedFSHeadCharacterizationEnv)
		} else if !t.Failed() && !gate.observed {
			t.Errorf("%s=1 incomplete BorrowedFS HEAD evidence; missing=%s", borrowedFSHeadCharacterizationEnv, strings.Join(borrowedFSHeadEvidence.missing(), ","))
		}
	})
	return gate
}

func TestBorrowedFSHeadEvidenceRequiresEveryNamedLeg(t *testing.T) {
	partial := borrowedFSHeadEvidenceState{currentHeadAfterCut: true, harnessWriterWins: true}
	if partial.complete() {
		t.Fatal("partial BorrowedFS HEAD evidence must not satisfy the package gate")
	}
	if got := strings.Join(partial.missing(), ","); !strings.Contains(got, "currentPubRevokesZeroProof") || !strings.Contains(got, "harnessLatePubStillFenced") || !strings.Contains(got, "harnessLatePinStillFenced") {
		t.Fatalf("missing() must name absent legs individually, got %q", got)
	}
	full := borrowedFSHeadEvidenceState{
		currentHeadAfterCut:        true,
		currentPubRevokesZeroProof: true,
		currentPubAfterZeroProof:   true,
		harnessWriterWins:          true,
		harnessCutAfterClassify:    true,
		harnessLatePubStillFenced:  true,
		harnessLatePinStillFenced:  true,
	}
	if !full.complete() || len(full.missing()) != 0 || len(full.namedLegs()) != 7 {
		t.Fatalf("all 7 named legs should satisfy the package gate; missing=%v legs=%d", full.missing(), len(full.namedLegs()))
	}
	twice := full
	twice.currentHeadAfterCut = true
	if !twice.complete() {
		t.Fatal("marking one leg twice must not hide a different required leg")
	}
}

func TestBorrowedFSHeadCharacterizationIsDocumented(t *testing.T) {
	raw, err := os.ReadFile("../../docs/R3-BORROWEDFS-HEAD-CHARACTERIZATION.md")
	if err != nil {
		t.Fatalf("read BorrowedFS HEAD characterization document: %v", err)
	}
	text := string(raw)
	required := []string{
		"X1 remains OPEN",
		"R3 remains OPEN",
		"GC_ENABLED=false",
		fmt.Sprintf("`%s=1`", borrowedFSHeadCharacterizationEnv),
		"currentHeadAfterCut",
		"currentPubRevokesZeroProof",
		"currentPubAfterZeroProof",
		"harnessWriterWins",
		"harnessCutAfterClassify",
		"harnessLatePubStillFenced",
		"harnessLatePinStillFenced",
		"integration-only",
		"last dangerous point",
		"not production protocol",
		"BorrowedFS",
		"up:<session>",
	}
	for _, needle := range required {
		if !strings.Contains(text, needle) {
			t.Fatalf("BorrowedFS HEAD characterization document is missing %q", needle)
		}
	}
	if !strings.Contains(text, "HEAD is not characterized") {
		t.Fatal("document must keep the X1 phrasing that D2's subsequent HEAD was previously uncharacterized")
	}
	verdicts := regexp.MustCompile(`(?m)^\*\*Current verdict: ([A-Z_]+)\*\*\s*$`).FindAllStringSubmatch(text, -1)
	if len(verdicts) != 1 {
		t.Fatalf("document must declare exactly one Current verdict, got %d", len(verdicts))
	}
	switch verdicts[0][1] {
	case "PROMISING", "PROMISING_WITH_PREREQUISITE", "REJECT":
	default:
		t.Fatalf("Current verdict %q is not a terminal state", verdicts[0][1])
	}
}
