package v2

import (
	"errors"
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/db"
)

func TestHandleStoredUploadMetadataError_RollsBackPromotedBlocks(t *testing.T) {
	oldRollback := rollbackUploadedBlockRefsFn
	defer func() {
		rollbackUploadedBlockRefsFn = oldRollback
	}()

	var rollbackCalled bool
	var gotOrgID, gotRepoID string
	var gotBlockIDs []string
	rollbackUploadedBlockRefsFn = func(database *db.DB, orgID, repoID string, blockIDs []string) {
		rollbackCalled = true
		gotOrgID = orgID
		gotRepoID = repoID
		gotBlockIDs = append([]string(nil), blockIDs...)
	}

	handleStoredUploadMetadataError(nil, "org-1", "repo-1", "file-1", []string{"block-1"}, errors.New("boom"))

	if !rollbackCalled {
		t.Fatal("expected failed metadata finalize to roll back promoted blocks")
	}
	if gotOrgID != "org-1" || gotRepoID != "repo-1" {
		t.Fatalf("rollback org/repo = %s/%s, want org-1/repo-1", gotOrgID, gotRepoID)
	}
	if len(gotBlockIDs) != 1 || gotBlockIDs[0] != "block-1" {
		t.Fatalf("rollback block IDs = %#v, want []string{\"block-1\"}", gotBlockIDs)
	}
}

func TestHandleStoredUploadMetadataError_SkipsSuccessfulFinalize(t *testing.T) {
	oldRollback := rollbackUploadedBlockRefsFn
	defer func() {
		rollbackUploadedBlockRefsFn = oldRollback
	}()

	rollbackCalled := false
	rollbackUploadedBlockRefsFn = func(database *db.DB, orgID, repoID string, blockIDs []string) {
		rollbackCalled = true
	}

	handleStoredUploadMetadataError(nil, "org-1", "repo-1", "file-1", []string{"block-1"}, nil)

	if rollbackCalled {
		t.Fatal("successful metadata finalize should not roll back promoted blocks")
	}
}

func TestHandleStoredUploadMetadataError_SkipsUnknownPublicationOutcome(t *testing.T) {
	oldRollback := rollbackUploadedBlockRefsFn
	defer func() {
		rollbackUploadedBlockRefsFn = oldRollback
	}()

	rollbackCalled := false
	rollbackUploadedBlockRefsFn = func(database *db.DB, orgID, repoID string, blockIDs []string) {
		rollbackCalled = true
	}

	handleStoredUploadMetadataError(nil, "org-1", "repo-1", "file-1", []string{"block-1"}, ErrLibraryHeadPublicationUnknown)

	if rollbackCalled {
		t.Fatal("unknown publication outcome should not roll back promoted blocks")
	}
}
