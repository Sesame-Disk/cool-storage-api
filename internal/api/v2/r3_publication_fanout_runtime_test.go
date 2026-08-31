package v2

import (
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/db"
)

// TestR3StagePendingPublishedFilesInvokesAddReferencesOncePerPendingFile is the
// runtime half of the v2 fan-out contract: N pending files must invoke the
// authorized staging seam N times. A wrapper of that same seam inside the loop
// would still be visible here; the source allowlist covers a differently named
// call. This does not count physical Cassandra RTTs.
func TestR3StagePendingPublishedFilesInvokesAddReferencesOncePerPendingFile(t *testing.T) {
	helper := &FSHelper{}
	oldResolve := stagePendingPublishedFilesResolveFn
	oldAdd := stagePendingPublishedFilesAddReferencesFn
	oldRemove := stagePendingPublishedFilesRemoveReferencesFn
	oldPersist := stagePendingPublishedFilesPersistFn
	t.Cleanup(func() {
		stagePendingPublishedFilesResolveFn = oldResolve
		stagePendingPublishedFilesAddReferencesFn = oldAdd
		stagePendingPublishedFilesRemoveReferencesFn = oldRemove
		stagePendingPublishedFilesPersistFn = oldPersist
	})

	stagePendingPublishedFilesResolveFn = func(h *FSHelper, orgID, libraryID string, blockIDs []string) ([]string, error) {
		return []string{"resolved-" + blockIDs[0]}, nil
	}
	stagePendingPublishedFilesPersistFn = func(h *FSHelper, repoID string, pending *pendingPublishedFile) error {
		return nil
	}
	addCalls := 0
	stagePendingPublishedFilesAddReferencesFn = func(database *db.DB, orgID, repoID, attemptID string, blockIDs []string) error {
		addCalls++
		return nil
	}
	stagePendingPublishedFilesRemoveReferencesFn = func(database *db.DB, orgID, attemptID string, blockIDs []string) error {
		t.Fatalf("remove should not run on successful stage, got %s/%s %#v", orgID, attemptID, blockIDs)
		return nil
	}

	pending := []*pendingPublishedFile{
		{fsID: "fs-1", externalBlockIDs: []string{"sha1-1"}},
		nil,
		{fsID: "fs-2", externalBlockIDs: []string{"sha1-2"}},
		{fsID: "fs-3", externalBlockIDs: []string{"sha1-3"}},
	}
	if err := helper.stagePendingPublishedFiles("org-1", "repo-1", "commit-1", pending); err != nil {
		t.Fatalf("stagePendingPublishedFiles() error = %v, want nil", err)
	}
	if addCalls != 3 {
		t.Fatalf("R3 FANOUT: stagePendingPublishedFilesAddReferencesFn calls = %d, want 1 per non-nil pending file (3)", addCalls)
	}
}
