package db

import "testing"

// TestR3AddPublishAttemptReferencesInvokesInsertOncePerNormalizedBlock is the
// runtime half of the per-block fan-out contract: unique normalized IDs must
// invoke addPublishAttemptReferenceFn once each. Duplicates and padding collapse
// before the loop. This does not count physical Cassandra RTTs.
func TestR3AddPublishAttemptReferencesInvokesInsertOncePerNormalizedBlock(t *testing.T) {
	oldAdd := addPublishAttemptReferenceFn
	t.Cleanup(func() { addPublishAttemptReferenceFn = oldAdd })

	var added []string
	addPublishAttemptReferenceFn = func(database *DB, orgID, blockID, referrer, repoID string) error {
		if orgID != "org-1" || repoID != "repo-1" || referrer != BlockReferrerForPublishAttempt("attempt-1") {
			t.Fatalf("add args = %s/%s/%s, want org-1/repo-1/%s", orgID, repoID, referrer, BlockReferrerForPublishAttempt("attempt-1"))
		}
		added = append(added, blockID)
		return nil
	}

	if err := AddPublishAttemptReferences(&DB{}, "org-1", "repo-1", "attempt-1", []string{" a ", "a", "b", "b", " ", "c"}); err != nil {
		t.Fatalf("AddPublishAttemptReferences() error = %v, want nil", err)
	}
	if len(added) != 3 || added[0] != "a" || added[1] != "b" || added[2] != "c" {
		t.Fatalf("R3 FANOUT: addPublishAttemptReferenceFn calls = %#v, want []string{\"a\", \"b\", \"c\"}", added)
	}
}
