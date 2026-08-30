package db

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

type r3LogicalDeltaVector struct {
	Name    string            `json:"name"`
	Old     []string          `json:"old"`
	New     []string          `json:"new"`
	Aliases map[string]string `json:"aliases,omitempty"`
	Want    []string          `json:"want"`
}

// r3LogicalPositiveDelta is deliberately test-only. These vectors record the
// logical set operation without claiming that this set is the complete R3 work
// set; provenance and continuity can add work outside this delta.
func r3LogicalPositiveDelta(oldIDs, newIDs []string, aliases map[string]string) []string {
	canonical := func(id string) string {
		if resolved := aliases[id]; resolved != "" {
			return resolved
		}
		return id
	}
	oldSet := make(map[string]struct{}, len(oldIDs))
	for _, id := range oldIDs {
		oldSet[canonical(id)] = struct{}{}
	}
	delta := make(map[string]struct{}, len(newIDs))
	for _, id := range newIDs {
		id = canonical(id)
		if _, existed := oldSet[id]; !existed {
			delta[id] = struct{}{}
		}
	}
	out := make([]string, 0, len(delta))
	for id := range delta {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func TestR3LogicalPositiveBlockDeltaVectors(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "r3_logical_positive_delta.json"))
	if err != nil {
		t.Fatal(err)
	}
	var vectors []r3LogicalDeltaVector
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatalf("decode vectors: %v", err)
	}
	if len(vectors) == 0 {
		t.Fatal("logical positive-delta vectors are empty")
	}
	for _, vector := range vectors {
		t.Run(vector.Name, func(t *testing.T) {
			got := r3LogicalPositiveDelta(vector.Old, vector.New, vector.Aliases)
			sort.Strings(vector.Want)
			if !reflect.DeepEqual(got, vector.Want) {
				t.Fatalf("LogicalPositiveBlockDelta(%v, %v) = %v, want %v", vector.Old, vector.New, got, vector.Want)
			}
		})
	}
}
