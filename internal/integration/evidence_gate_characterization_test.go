//go:build integration

package integration

import (
	"slices"
	"testing"
)

func TestEvidenceGatePatternIncludesCharacterizationGates(t *testing.T) {
	const source = `
const evidence = "SESAMEFS_REQUIRE_P4B_EVIDENCE"
const characterization = "SESAMEFS_REQUIRE_R3_CHARACTERIZATION"
`
	want := []string{"SESAMEFS_REQUIRE_P4B_EVIDENCE", "SESAMEFS_REQUIRE_R3_CHARACTERIZATION"}
	if got := evidenceGatePattern.FindAllString(source, -1); !slices.Equal(got, want) {
		t.Fatalf("evidence gates = %v, want %v", got, want)
	}
}
