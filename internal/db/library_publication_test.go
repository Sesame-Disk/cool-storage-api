package db

import "testing"

// TestLibraryPublicationCASStateIsLegacyNull pins the classification that
// gates the initializer legacy-NULL CAS retry (FSHelper.InitializeLibraryFS,
// SyncHandler.createInitialCommit). Getting this wrong in the "should retry"
// direction is the resurrection bug the retry's IsLibraryPublicationRevoked
// guard exists to close: this function alone cannot distinguish a genuine
// untouched pre-021 row from an absent (hard-deleted) partition, since
// Cassandra returns the same NULL for both.
func TestLibraryPublicationCASStateIsLegacyNull(t *testing.T) {
	tests := []struct {
		name     string
		casState map[string]interface{}
		want     bool
	}{
		{"legacyNullValue", map[string]interface{}{"publication_state": nil}, true},
		{"missingKey", map[string]interface{}{"head_commit_id": "commit-1"}, true},
		{"emptyString", map[string]interface{}{"publication_state": ""}, true},
		{"active", map[string]interface{}{"publication_state": LibraryPublicationStateActive}, false},
		{"terminal", map[string]interface{}{"publication_state": LibraryPublicationStateTerminal}, false},
		{"nonStringValue", map[string]interface{}{"publication_state": 42}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LibraryPublicationCASStateIsLegacyNull(tt.casState); got != tt.want {
				t.Fatalf("LibraryPublicationCASStateIsLegacyNull(%#v) = %v, want %v", tt.casState, got, tt.want)
			}
		})
	}
}
