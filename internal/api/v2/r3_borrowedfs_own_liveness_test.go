package v2

import (
	"errors"
	"testing"
	"time"
)

func TestBorrowedFSOwnLivenessPinsExactDistinctBorrowedBlocks(t *testing.T) {
	oldAdd := registerUploadedBlockAddProvisionalRefFn
	t.Cleanup(func() { registerUploadedBlockAddProvisionalRefFn = oldAdd })

	wantReferrer := "up:session-1"
	calls := make(map[string]int)
	registerUploadedBlockAddProvisionalRefFn = func(_ *FSHelper, orgID, blockID, referrer, libraryID, storageClass string, expiresAt time.Time) error {
		if orgID != "org-1" || libraryID != "repo-1" || referrer != wantReferrer || storageClass != "hot" || expiresAt.Before(time.Now()) {
			t.Fatalf("unexpected own-liveness arguments: %s/%s/%s/%s/%s", orgID, blockID, referrer, libraryID, storageClass)
		}
		calls[blockID]++
		return nil
	}

	blocks := []borrowedFSCommitBlock{
		{blockID: "b1", storageClass: "hot", storageKey: "k1"},
		{blockID: "b1", storageClass: "hot", storageKey: "k1"},
		{blockID: "b2", storageClass: "hot", storageKey: "k2"},
	}
	if err := (&FileHandler{}).ensureBorrowedFSOwnLiveness(nil, "org-1", "repo-1", "session-1", blocks); err != nil {
		t.Fatalf("ensureBorrowedFSOwnLiveness: %v", err)
	}
	if len(calls) != 2 || calls["b1"] != 1 || calls["b2"] != 1 {
		t.Fatalf("own-liveness calls = %v, want one call for each distinct borrowed block", calls)
	}
}

func TestBorrowedFSOwnLivenessLeavesSessionUploadAtPlusZero(t *testing.T) {
	oldAdd := registerUploadedBlockAddProvisionalRefFn
	t.Cleanup(func() { registerUploadedBlockAddProvisionalRefFn = oldAdd })

	addCalls := 0
	registerUploadedBlockAddProvisionalRefFn = func(*FSHelper, string, string, string, string, string, time.Time) error {
		addCalls++
		return nil
	}
	if err := (&FileHandler{}).ensureBorrowedFSOwnLiveness(nil, "org-1", "repo-1", "session-1", nil); err != nil {
		t.Fatalf("empty BorrowedFS liveness set: %v", err)
	}
	if addCalls != 0 {
		t.Fatalf("SessionUpload-compatible empty BorrowedFS set made %d pin calls, want 0", addCalls)
	}
}

func TestBorrowedFSOwnLivenessFailureIsRetryable(t *testing.T) {
	oldAdd := registerUploadedBlockAddProvisionalRefFn
	t.Cleanup(func() { registerUploadedBlockAddProvisionalRefFn = oldAdd })

	attempts := 0
	refs := make(map[string]bool)
	registerUploadedBlockAddProvisionalRefFn = func(_ *FSHelper, _, blockID, referrer, _, _ string, _ time.Time) error {
		attempts++
		if attempts == 1 {
			return errors.New("temporary Cassandra failure")
		}
		refs[blockID+"/"+referrer] = true
		return nil
	}
	block := []borrowedFSCommitBlock{{blockID: "b1", storageClass: "hot", storageKey: "k1"}}
	if err := (&FileHandler{}).ensureBorrowedFSOwnLiveness(nil, "org-1", "repo-1", "session-1", block); !errors.Is(err, ErrBlockMaterializationTransient) {
		t.Fatalf("first own-liveness attempt error = %v, want transient", err)
	}
	if err := (&FileHandler{}).ensureBorrowedFSOwnLiveness(nil, "org-1", "repo-1", "session-1", block); err != nil {
		t.Fatalf("retry own-liveness: %v", err)
	}
	if attempts != 2 || len(refs) != 1 {
		t.Fatalf("retry attempts/refs = %d/%v, want 2/one idempotent ref", attempts, refs)
	}
}

func TestBorrowedFSFenceRejectsActiveDelete(t *testing.T) {
	oldFence := registerUploadedBlockFenceActiveFn
	t.Cleanup(func() { registerUploadedBlockFenceActiveFn = oldFence })

	registerUploadedBlockFenceActiveFn = func(_ *FSHelper, _, blockID string) (bool, error) {
		if blockID != "b1" {
			t.Fatalf("fence checked block %q, want b1", blockID)
		}
		return true, nil
	}
	blocks := []borrowedFSCommitBlock{{blockID: "b1", storageClass: "hot", storageKey: "k1"}}
	if err := (&FileHandler{}).validateBorrowedFSFences("org-1", blocks); !errors.Is(err, ErrBlockDeleteInProgress) {
		t.Fatalf("validateBorrowedFSFences error = %v, want ErrBlockDeleteInProgress", err)
	}
}

func TestBorrowedFSFenceLeavesSessionUploadAtPlusZero(t *testing.T) {
	oldFence := registerUploadedBlockFenceActiveFn
	t.Cleanup(func() { registerUploadedBlockFenceActiveFn = oldFence })

	fenceCalls := 0
	registerUploadedBlockFenceActiveFn = func(*FSHelper, string, string) (bool, error) {
		fenceCalls++
		return false, nil
	}
	if err := (&FileHandler{}).validateBorrowedFSFences("org-1", nil); err != nil {
		t.Fatalf("empty BorrowedFS fence set: %v", err)
	}
	if fenceCalls != 0 {
		t.Fatalf("SessionUpload-compatible empty BorrowedFS set made %d fence calls, want 0", fenceCalls)
	}
}
