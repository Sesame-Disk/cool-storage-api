package v2

import (
	"errors"
	"strings"
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/db"
)

func TestRegisterUploadedBlockAndMapping_WritesMappingAfterMetadata(t *testing.T) {
	oldRegister := registerUploadedBlockForMaterializationFn
	oldWriteMapping := writeBlockMappingForMaterializationFn
	oldRollback := rollbackUploadedBlockRefsFn
	defer func() {
		registerUploadedBlockForMaterializationFn = oldRegister
		writeBlockMappingForMaterializationFn = oldWriteMapping
		rollbackUploadedBlockRefsFn = oldRollback
	}()

	var calls []string
	registerUploadedBlockForMaterializationFn = func(database *db.DB, orgID, repoID, internalBlockID, operationID string, sizeBytes int, storageClass, storageKey, sha1ID string) error {
		calls = append(calls, "register")
		if sha1ID != "ext-1" {
			t.Fatalf("sha1ID = %q, want ext-1", sha1ID)
		}
		return nil
	}
	writeBlockMappingForMaterializationFn = func(database *db.DB, orgID, repoID, externalBlockID, internalBlockID string) error {
		calls = append(calls, "mapping")
		return nil
	}
	rollbackUploadedBlockRefsFn = func(database *db.DB, orgID, repoID, operationID string, blockIDs []string) {
		calls = append(calls, "rollback")
	}

	err := RegisterUploadedBlockAndMapping(nil, "org-1", "repo-1", "int-1", "op-1", 123, "hot", "", "ext-1")
	if err != nil {
		t.Fatalf("RegisterUploadedBlockAndMapping returned error: %v", err)
	}
	want := []string{"register", "mapping"}
	if len(calls) != len(want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("calls[%d] = %q, want %q (full=%#v)", i, calls[i], want[i], calls)
		}
	}
}

func TestRegisterUploadedBlockAndMapping_PreservesSharedPinOnMappingFailure(t *testing.T) {
	oldRegister := registerUploadedBlockForMaterializationFn
	oldWriteMapping := writeBlockMappingForMaterializationFn
	oldRollback := rollbackUploadedBlockRefsFn
	defer func() {
		registerUploadedBlockForMaterializationFn = oldRegister
		writeBlockMappingForMaterializationFn = oldWriteMapping
		rollbackUploadedBlockRefsFn = oldRollback
	}()

	registerUploadedBlockForMaterializationFn = func(database *db.DB, orgID, repoID, internalBlockID, operationID string, sizeBytes int, storageClass, storageKey, sha1ID string) error {
		return nil
	}
	wantErr := errors.New("mapping boom")
	writeBlockMappingForMaterializationFn = func(database *db.DB, orgID, repoID, externalBlockID, internalBlockID string) error {
		return wantErr
	}
	var rollbackCalled bool
	rollbackUploadedBlockRefsFn = func(database *db.DB, orgID, repoID, operationID string, blockIDs []string) {
		rollbackCalled = true
	}

	err := RegisterUploadedBlockAndMapping(nil, "org-1", "repo-1", "int-1", "op-1", 123, "hot", "", "ext-1")
	if !errors.Is(err, ErrBlockMappingWriteFailed) {
		t.Fatalf("error = %v, want ErrBlockMappingWriteFailed", err)
	}
	if rollbackCalled {
		t.Fatal("mapping failure must not remove a pin shared by concurrent retries")
	}
}

func TestRegisterUploadedBlockAndMapping_SkipsMappingWithoutExternalID(t *testing.T) {
	oldRegister := registerUploadedBlockForMaterializationFn
	oldWriteMapping := writeBlockMappingForMaterializationFn
	defer func() {
		registerUploadedBlockForMaterializationFn = oldRegister
		writeBlockMappingForMaterializationFn = oldWriteMapping
	}()

	registerUploadedBlockForMaterializationFn = func(database *db.DB, orgID, repoID, internalBlockID, operationID string, sizeBytes int, storageClass, storageKey, sha1ID string) error {
		if strings.TrimSpace(sha1ID) != "" {
			t.Fatalf("sha1ID = %q, want empty", sha1ID)
		}
		return nil
	}
	writeCalled := false
	writeBlockMappingForMaterializationFn = func(database *db.DB, orgID, repoID, externalBlockID, internalBlockID string) error {
		writeCalled = true
		return nil
	}

	err := RegisterUploadedBlockAndMapping(nil, "org-1", "repo-1", "int-1", "op-1", 123, "hot", "", "  ")
	if err != nil {
		t.Fatalf("RegisterUploadedBlockAndMapping returned error: %v", err)
	}
	if writeCalled {
		t.Fatal("mapping write should be skipped when external block ID is empty")
	}
}

func TestRegisterUploadedBlockAndMapping_StopsOnRegisterFailure(t *testing.T) {
	oldRegister := registerUploadedBlockForMaterializationFn
	oldWriteMapping := writeBlockMappingForMaterializationFn
	oldRollback := rollbackUploadedBlockRefsFn
	defer func() {
		registerUploadedBlockForMaterializationFn = oldRegister
		writeBlockMappingForMaterializationFn = oldWriteMapping
		rollbackUploadedBlockRefsFn = oldRollback
	}()

	wantErr := errors.New("register boom")
	registerUploadedBlockForMaterializationFn = func(database *db.DB, orgID, repoID, internalBlockID, operationID string, sizeBytes int, storageClass, storageKey, sha1ID string) error {
		return wantErr
	}
	writeCalled := false
	writeBlockMappingForMaterializationFn = func(database *db.DB, orgID, repoID, externalBlockID, internalBlockID string) error {
		writeCalled = true
		return nil
	}
	rollbackCalled := false
	rollbackUploadedBlockRefsFn = func(database *db.DB, orgID, repoID, operationID string, blockIDs []string) {
		rollbackCalled = true
	}

	err := RegisterUploadedBlockAndMapping(nil, "org-1", "repo-1", "int-1", "op-1", 123, "hot", "", "ext-1")
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
	if writeCalled {
		t.Fatal("mapping write should not run after register failure")
	}
	if rollbackCalled {
		t.Fatal("rollback should not run when register step fails")
	}
}

func TestHandleStoredUploadMetadataError_RollsBackPromotedBlocks(t *testing.T) {
	oldRollback := rollbackUploadedBlockRefsFn
	defer func() {
		rollbackUploadedBlockRefsFn = oldRollback
	}()

	var rollbackCalled bool
	var gotOrgID, gotRepoID string
	var gotOperationID string
	var gotBlockIDs []string
	rollbackUploadedBlockRefsFn = func(database *db.DB, orgID, repoID, operationID string, blockIDs []string) {
		rollbackCalled = true
		gotOrgID = orgID
		gotRepoID = repoID
		gotOperationID = operationID
		gotBlockIDs = append([]string(nil), blockIDs...)
	}

	handleStoredUploadMetadataError(nil, "org-1", "repo-1", "upload-op-1", []string{"block-1"}, errors.New("boom"))

	if !rollbackCalled {
		t.Fatal("expected failed metadata finalize to roll back promoted blocks")
	}
	if gotOrgID != "org-1" || gotRepoID != "repo-1" {
		t.Fatalf("rollback org/repo = %s/%s, want org-1/repo-1", gotOrgID, gotRepoID)
	}
	if gotOperationID != "upload-op-1" {
		t.Fatalf("rollback operation ID = %s, want upload-op-1", gotOperationID)
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
	rollbackUploadedBlockRefsFn = func(database *db.DB, orgID, repoID, operationID string, blockIDs []string) {
		rollbackCalled = true
	}

	handleStoredUploadMetadataError(nil, "org-1", "repo-1", "upload-op-1", []string{"block-1"}, nil)

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
	rollbackUploadedBlockRefsFn = func(database *db.DB, orgID, repoID, operationID string, blockIDs []string) {
		rollbackCalled = true
	}

	handleStoredUploadMetadataError(nil, "org-1", "repo-1", "upload-op-1", []string{"block-1"}, ErrLibraryHeadPublicationUnknown)

	if rollbackCalled {
		t.Fatal("unknown publication outcome should not roll back promoted blocks")
	}
}
