//go:build integration

package integration

import (
	"testing"
	"time"

	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	"github.com/google/uuid"
)

// seedS3Orphan creates integration state through the production lifecycle
// entry point. A failed initial delete is represented by the same follow-up
// mutation the worker uses, rather than by a second row-creating API.
func seedS3Orphan(t *testing.T, store gcpkg.GCStore, orgID uuid.UUID, blockID, storageClass, externalSHA1, errMsg string, firstSeenAt time.Time) time.Time {
	return seedS3OrphanWithStorageKey(t, store, orgID, blockID, syntheticCanonicalStorageKeyForTest(orgID.String(), blockID), storageClass, externalSHA1, errMsg, firstSeenAt)
}

func seedS3OrphanWithStorageKey(t *testing.T, store gcpkg.GCStore, orgID uuid.UUID, blockID, storageKey, storageClass, externalSHA1, errMsg string, firstSeenAt time.Time) time.Time {
	t.Helper()
	firstSeenAt = firstSeenAt.UTC().Truncate(time.Millisecond)
	authority := gcpkg.CommittedBlockDeleteAuthority{BlockDeleteAuthority: gcpkg.BlockDeleteAuthority{
		Target:    gcpkg.BlockDeleteTarget{StorageClass: storageClass, StorageKey: storageKey},
		ClaimID:   "test-orphan-claim:" + blockID,
		ClaimedAt: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
	}}
	result := store.StartBlockDeleteOrphan(orgID, blockID, authority, externalSHA1, firstSeenAt)
	if result.Outcome != gcpkg.StartBlockDeleteOrphanCreated && result.Outcome != gcpkg.StartBlockDeleteOrphanSameAuthority {
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

func testCommittedOrphanAuthority(blockID, storageClass, storageKey string) gcpkg.CommittedBlockDeleteAuthority {
	return gcpkg.CommittedBlockDeleteAuthority{BlockDeleteAuthority: gcpkg.BlockDeleteAuthority{
		Target:    gcpkg.BlockDeleteTarget{StorageClass: storageClass, StorageKey: storageKey},
		ClaimID:   "test-orphan-claim:" + blockID,
		ClaimedAt: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC),
	}}
}
