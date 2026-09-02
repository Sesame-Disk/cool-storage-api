//go:build integration

package integration

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

const x1NonoverlapCharacterizationEnv = "SESAMEFS_REQUIRE_X1_NONOVERLAP_CHARACTERIZATION"

// x1NonoverlapEvidenceState records each characterization leg by name. Completeness
// is the conjunction of these fields, never a counter: marking one leg twice cannot
// hide another. TestMain inspects missing() after m.Run().
type x1NonoverlapEvidenceState struct {
	writerFirst                  bool
	gcFirst                      bool
	refBeforeZeroProof           bool
	refBetweenProofAndCut        bool
	lateUploadRef                bool
	borrowedFSPublish            bool
	physicalDeleteFailure        bool
	postCommitResume             bool
	pendingBlocksReenqueue       bool
	candidateBehindCursor        bool
	postDeleteCrash              bool
	ambiguousFinalizeSafety      bool
	ambiguousFinalizeConvergence bool
	lateRepairPut                bool
	nextIncarnation              bool
}

func (state x1NonoverlapEvidenceState) namedLegs() []struct {
	name string
	seen bool
} {
	return []struct {
		name string
		seen bool
	}{
		{"writerFirst", state.writerFirst},
		{"gcFirst", state.gcFirst},
		{"refBeforeZeroProof", state.refBeforeZeroProof},
		{"refBetweenProofAndCut", state.refBetweenProofAndCut},
		{"lateUploadRef", state.lateUploadRef},
		{"borrowedFSPublish", state.borrowedFSPublish},
		{"physicalDeleteFailure", state.physicalDeleteFailure},
		{"postCommitResume", state.postCommitResume},
		{"pendingBlocksReenqueue", state.pendingBlocksReenqueue},
		{"candidateBehindCursor", state.candidateBehindCursor},
		{"postDeleteCrash", state.postDeleteCrash},
		{"ambiguousFinalizeSafety", state.ambiguousFinalizeSafety},
		{"ambiguousFinalizeConvergence", state.ambiguousFinalizeConvergence},
		{"lateRepairPut", state.lateRepairPut},
		{"nextIncarnation", state.nextIncarnation},
	}
}

func (state x1NonoverlapEvidenceState) complete() bool {
	for _, leg := range state.namedLegs() {
		if !leg.seen {
			return false
		}
	}
	return true
}

func (state x1NonoverlapEvidenceState) missing() []string {
	missing := make([]string, 0, 15)
	for _, leg := range state.namedLegs() {
		if !leg.seen {
			missing = append(missing, leg.name)
		}
	}
	return missing
}

var x1NonoverlapEvidence x1NonoverlapEvidenceState

type x1NonoverlapEvidenceGate struct{ observed bool }

func x1RequireNonoverlapEvidence(t *testing.T) *x1NonoverlapEvidenceGate {
	t.Helper()
	gate := &x1NonoverlapEvidenceGate{}
	if os.Getenv(x1NonoverlapCharacterizationEnv) != "1" {
		return gate
	}
	t.Cleanup(func() {
		if t.Skipped() {
			t.Errorf("%s=1 requires real Cassandra+MinIO X1 non-overlap evidence, but the test skipped", x1NonoverlapCharacterizationEnv)
		} else if !t.Failed() && !gate.observed {
			t.Errorf("%s=1 incomplete X1 non-overlap evidence; missing=%s", x1NonoverlapCharacterizationEnv, strings.Join(x1NonoverlapEvidence.missing(), ","))
		}
	})
	return gate
}

func TestX1CharacterizationEvidenceRequiresEveryNamedLeg(t *testing.T) {
	partial := x1NonoverlapEvidenceState{writerFirst: true, gcFirst: true, lateRepairPut: true}
	if partial.complete() {
		t.Fatal("partial X1 evidence must not satisfy the package gate")
	}
	if got := strings.Join(partial.missing(), ","); !strings.Contains(got, "borrowedFSPublish") || !strings.Contains(got, "ambiguousFinalizeSafety") {
		t.Fatalf("missing() must name absent legs individually, got %q", got)
	}
	full := x1NonoverlapEvidenceState{
		writerFirst:                  true,
		gcFirst:                      true,
		refBeforeZeroProof:           true,
		refBetweenProofAndCut:        true,
		lateUploadRef:                true,
		borrowedFSPublish:            true,
		physicalDeleteFailure:        true,
		postCommitResume:             true,
		pendingBlocksReenqueue:       true,
		candidateBehindCursor:        true,
		postDeleteCrash:              true,
		ambiguousFinalizeSafety:      true,
		ambiguousFinalizeConvergence: true,
		lateRepairPut:                true,
		nextIncarnation:              true,
	}
	if !full.complete() || len(full.missing()) != 0 || len(full.namedLegs()) != 15 {
		t.Fatalf("all 15 named legs should satisfy the package gate; missing=%v legs=%d", full.missing(), len(full.namedLegs()))
	}
	twice := full
	twice.writerFirst = true
	if !twice.complete() {
		t.Fatal("marking one leg twice must not hide a different required leg")
	}
}

func TestX1CharacterizationBaseIsDocumented(t *testing.T) {
	raw, err := os.ReadFile("../../docs/GC-X1-STRICT-NONOVERLAP-CHARACTERIZATION.md")
	if err != nil {
		t.Fatalf("read X1 characterization document: %v", err)
	}
	text := string(raw)
	required := []string{
		"X1 remains OPEN",
		"R3 remains OPEN",
		"GC_ENABLED=false",
		fmt.Sprintf("`%s=1`", x1NonoverlapCharacterizationEnv),
		"writerFirst",
		"pendingBlocksReenqueue",
		"physicalDeleteFailure",
		"ambiguousFinalizeSafety",
		"ambiguousFinalizeConvergence",
		"own liveness",
		"HEAD is not characterized",
		"not yet fully specified",
		"committed-delete recovery",
		"source/AST contract mutations",
		"independent physical lives",
		"deliberately conservative",
		"PROMISING",
		"PROMISING_WITH_PREREQUISITE",
		"REJECT",
	}
	for _, needle := range required {
		if !strings.Contains(text, needle) {
			t.Fatalf("X1 characterization document is missing %q", needle)
		}
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
