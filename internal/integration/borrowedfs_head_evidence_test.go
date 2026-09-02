//go:build integration

package integration

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

const borrowedFSOwnLivenessEnv = "SESAMEFS_REQUIRE_BORROWEDFS_OWN_LIVENESS_EVIDENCE"

// borrowedFSOwnLivenessEvidenceState records each W1 leg by name. Completeness
// is the conjunction of these fields, never a counter: marking one leg twice
// cannot hide another.
type borrowedFSOwnLivenessEvidenceState struct {
	borrowedExactOwnPin          bool
	sessionUploadNoExtraPin      bool
	livenessFailureNoPublication bool
	writerFirst                  bool
	gcFirst                      bool
	lateOwnPinAfterZeroProof     bool
	upPubDedup                   bool
}

func (state borrowedFSOwnLivenessEvidenceState) namedLegs() []struct {
	name string
	seen bool
} {
	return []struct {
		name string
		seen bool
	}{
		{"borrowedExactOwnPin", state.borrowedExactOwnPin},
		{"sessionUploadNoExtraPin", state.sessionUploadNoExtraPin},
		{"livenessFailureNoPublication", state.livenessFailureNoPublication},
		{"writerFirst", state.writerFirst},
		{"gcFirst", state.gcFirst},
		{"lateOwnPinAfterZeroProof", state.lateOwnPinAfterZeroProof},
		{"upPubDedup", state.upPubDedup},
	}
}

func (state borrowedFSOwnLivenessEvidenceState) complete() bool {
	for _, leg := range state.namedLegs() {
		if !leg.seen {
			return false
		}
	}
	return true
}

func (state borrowedFSOwnLivenessEvidenceState) missing() []string {
	missing := make([]string, 0, len(state.namedLegs()))
	for _, leg := range state.namedLegs() {
		if !leg.seen {
			missing = append(missing, leg.name)
		}
	}
	return missing
}

var borrowedFSOwnLivenessEvidence borrowedFSOwnLivenessEvidenceState

type borrowedFSOwnLivenessEvidenceGate struct{ observed bool }

func borrowedFSRequireOwnLivenessEvidence(t *testing.T) *borrowedFSOwnLivenessEvidenceGate {
	t.Helper()
	gate := &borrowedFSOwnLivenessEvidenceGate{}
	if os.Getenv(borrowedFSOwnLivenessEnv) != "1" {
		return gate
	}
	t.Cleanup(func() {
		if t.Skipped() {
			t.Errorf("%s=1 requires real Cassandra+MinIO BorrowedFS own-liveness evidence, but the test skipped", borrowedFSOwnLivenessEnv)
		} else if !t.Failed() && !gate.observed {
			t.Errorf("%s=1 incomplete BorrowedFS own-liveness evidence; missing=%s", borrowedFSOwnLivenessEnv, strings.Join(borrowedFSOwnLivenessEvidence.missing(), ","))
		}
	})
	return gate
}

func TestBorrowedFSOwnLivenessEvidenceRequiresEveryNamedLeg(t *testing.T) {
	partial := borrowedFSOwnLivenessEvidenceState{borrowedExactOwnPin: true, writerFirst: true}
	if partial.complete() {
		t.Fatal("partial BorrowedFS own-liveness evidence must not satisfy the package gate")
	}
	if got := strings.Join(partial.missing(), ","); !strings.Contains(got, "sessionUploadNoExtraPin") || !strings.Contains(got, "lateOwnPinAfterZeroProof") || !strings.Contains(got, "upPubDedup") {
		t.Fatalf("missing() must name absent legs individually, got %q", got)
	}
	full := borrowedFSOwnLivenessEvidenceState{
		borrowedExactOwnPin:          true,
		sessionUploadNoExtraPin:      true,
		livenessFailureNoPublication: true,
		writerFirst:                  true,
		gcFirst:                      true,
		lateOwnPinAfterZeroProof:     true,
		upPubDedup:                   true,
	}
	if !full.complete() || len(full.missing()) != 0 || len(full.namedLegs()) != 7 {
		t.Fatalf("all 7 named legs should satisfy the package gate; missing=%v legs=%d", full.missing(), len(full.namedLegs()))
	}
	twice := partial
	twice.borrowedExactOwnPin = true
	if twice.complete() {
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
		"Historical (#200) productive seams",
		"before W1",
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
