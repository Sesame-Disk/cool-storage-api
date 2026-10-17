package gc

import (
	"context"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/google/uuid"
)

// Regression for ISSUE-GC-PHASE5-CASCADE-SHARED-FSOBJECTS-01. The original
// characterization demonstrated that an expired dangling commit deleted nodes
// shared with HEAD. Unguarded commit retirement must not cascade into its tree.
func TestPhase5CommitRetirementPreservesFSObjectsSharedWithHEAD(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	w := NewWorker(store, nil, q, 100, 0, false, stats)

	orgID := uuid.New()
	libID := uuid.New()

	// Live HEAD commit H:      root-H -> dir-D -> file-F (blockB)
	// Dangling commit X (old): root-X -> dir-D (same content-addressed fs_id) -> file-F
	store.AddLibraryWithTTL(orgID, libID, "standard", "commit-H", 30)
	store.AddCommitWithDetails(libID, "commit-H", "root-H", "", time.Now())
	store.AddCommitWithDetails(libID, "commit-X", "root-X", "", time.Now().Add(-90*24*time.Hour))
	store.AddFSObjectWithEntries(libID, "root-H", "dir", nil, []string{"dir-D"})
	store.AddFSObjectWithEntries(libID, "root-X", "dir", nil, []string{"dir-D"})
	store.AddFSObjectWithEntries(libID, "dir-D", "dir", nil, []string{"file-F"})
	store.AddFSObject(libID, "file-F", "file", []string{"blockB"})
	store.AddBlock(orgID, "blockB", "standard", 1)
	store.AddBlockReferenceForTest(orgID, "blockB", db.BlockReferrerForFSObject(libID.String(), "file-F"))
	referencesBefore := store.BlockReferenceCount(orgID, "blockB")

	// Exactly the queue item scanExpiredVersions produces for commit-X.
	if err := store.EnqueueBatch([]QueueItem{{
		OrgID:                 orgID,
		QueuedAt:              time.Now().Add(-2 * time.Hour),
		IdentityAt:            time.Now().Add(-2 * time.Hour),
		ItemType:              ItemCommit,
		ItemID:                "commit-X",
		LibraryID:             libID,
		BlockRepresentationID: db.PlainBlockRepresentationID,
	}}); err != nil {
		t.Fatalf("seed expired-version commit: %v", err)
	}

	ctx := context.Background()
	for i := 0; i < 20; i++ {
		n, err := w.ProcessOnce(ctx)
		if err != nil {
			t.Fatalf("ProcessOnce: %v", err)
		}
		if n == 0 {
			break
		}
	}

	if store.GetCommitRecord(libID, "commit-H") == nil {
		t.Fatal("HEAD commit row itself must survive; Phase 5 only targets non-HEAD-chain commits")
	}
	if store.GetCommitRecord(libID, "commit-X") != nil {
		t.Fatal("expired commit must still be retired")
	}
	for _, id := range []string{"root-H", "root-X", "dir-D", "file-F"} {
		if _, err := store.GetFSObject(libID, id); err != nil {
			t.Errorf("commit retirement deleted shared or retained tree node %s: %v", id, err)
		}
	}
	if n := store.BlockReferenceCount(orgID, "blockB"); n != referencesBefore {
		t.Errorf("permanent shared fs: reference count = %d, want %d", n, referencesBefore)
	}
	if n := len(store.QueueItems(orgID)); n != 0 {
		t.Errorf("commit retirement left %d descendant queue items", n)
	}
}
