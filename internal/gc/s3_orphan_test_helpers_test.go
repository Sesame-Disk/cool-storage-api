package gc

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func testOrphanClaimedAt() time.Time {
	return time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
}

func testDeleteAuthority(blockID, storageClass, storageKey string) BlockDeleteAuthority {
	return BlockDeleteAuthority{
		Target:    BlockDeleteTarget{StorageClass: storageClass, StorageKey: storageKey},
		ClaimID:   "test-orphan-claim:" + blockID,
		ClaimedAt: testOrphanClaimedAt(),
	}
}

func testCommittedOrphanAuthority(blockID, storageClass, storageKey string) CommittedBlockDeleteAuthority {
	return committedBlockDeleteAuthority(testDeleteAuthority(blockID, storageClass, storageKey))
}

func testCommittedOrphanAuthorityForOrg(orgID uuid.UUID, blockID, storageClass string) CommittedBlockDeleteAuthority {
	return testCommittedOrphanAuthority(blockID, storageClass, MockCanonicalStorageKey(orgID.String(), blockID))
}

// seedS3Orphan creates test state through the production lifecycle entry point.
// A failed initial delete is represented by the same follow-up mutation the
// worker uses, rather than by a second row-creating API.
func seedS3Orphan(t *testing.T, store GCStore, orgID uuid.UUID, blockID, storageClass, externalSHA1, errMsg string, firstSeenAt time.Time) time.Time {
	t.Helper()
	firstSeenAt = firstSeenAt.UTC().Truncate(time.Millisecond)
	result := store.StartBlockDeleteOrphan(orgID, blockID, testCommittedOrphanAuthorityForOrg(orgID, blockID, storageClass), externalSHA1, firstSeenAt)
	if result.Outcome != StartBlockDeleteOrphanCreated && result.Outcome != StartBlockDeleteOrphanSameAuthority {
		t.Fatalf("StartBlockDeleteOrphan: outcome=%s cause=%v", result.Outcome, result.Cause)
	}
	effectiveFirstSeenAt := result.FirstSeenAt
	if errMsg != "" {
		if err := store.UpdateS3OrphanAttempt(orgID, blockID, effectiveFirstSeenAt, errMsg, firstSeenAt); err != nil {
			t.Fatalf("UpdateS3OrphanAttempt: %v", err)
		}
	}
	return effectiveFirstSeenAt
}
