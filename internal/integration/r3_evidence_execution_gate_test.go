//go:build integration

package integration

import "testing"

type r3CharacterizationEvidenceState struct {
	writerWins      bool
	deletingFence   bool
	orphanOnlyFence bool
}

func (state r3CharacterizationEvidenceState) complete() bool {
	return state.writerWins && state.deletingFence && state.orphanOnlyFence
}

func (state r3CharacterizationEvidenceState) missing() []string {
	missing := make([]string, 0, 3)
	if !state.writerWins {
		missing = append(missing, "writer_wins")
	}
	if !state.deletingFence {
		missing = append(missing, "deleting_fence")
	}
	if !state.orphanOnlyFence {
		missing = append(missing, "orphan_fence")
	}
	return missing
}

// Each leg is marked only after its own Cassandra assertions pass. TestMain
// checks the complete state after m.Run, so a subtest -run filter cannot turn
// one successful leg into evidence for all three.
var r3CharacterizationEvidence r3CharacterizationEvidenceState

func TestR3CharacterizationEvidenceRequiresEveryLeg(t *testing.T) {
	partial := r3CharacterizationEvidenceState{writerWins: true, deletingFence: true}
	if partial.complete() {
		t.Fatal("partial R3 evidence must not satisfy the package gate")
	}
	partial.orphanOnlyFence = true
	if !partial.complete() {
		t.Fatalf("all R3 evidence legs should satisfy the package gate; missing=%v", partial.missing())
	}
}
