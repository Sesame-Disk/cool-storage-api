package gc

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// seedS3Orphan creates test state through the production lifecycle entry point.
// A failed initial delete is represented by the same follow-up mutation the
// worker uses, rather than by a second row-creating API.
func seedS3Orphan(t *testing.T, store GCStore, orgID uuid.UUID, blockID, storageClass, externalSHA1, errMsg string, firstSeenAt time.Time) time.Time {
	t.Helper()
	firstSeenAt = firstSeenAt.UTC().Truncate(time.Millisecond)
	effectiveFirstSeenAt, err := store.StartBlockDeleteOrphan(orgID, blockID, storageClass, MockCanonicalStorageKey(orgID.String(), blockID), externalSHA1, firstSeenAt)
	if err != nil {
		t.Fatalf("StartBlockDeleteOrphan: %v", err)
	}
	if errMsg != "" {
		if err := store.UpdateS3OrphanAttempt(orgID, blockID, effectiveFirstSeenAt, errMsg, firstSeenAt); err != nil {
			t.Fatalf("UpdateS3OrphanAttempt: %v", err)
		}
	}
	return effectiveFirstSeenAt
}
