package v2

import (
	"errors"
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/db"
)

// These exercise the centralized lock enforcement at the top of processSingleItem,
// which every move/copy entry point (the /file/move handler AND the batch endpoints)
// funnels through. The rejection paths return before any fs traversal, so a nil
// FSHelper is safe here.

func withBatchSubtreeStub(t *testing.T, stub func(*BatchOperationHandler, string, string, string) (bool, string, error)) {
	t.Helper()
	old := checkBatchSubtreeLocked
	checkBatchSubtreeLocked = stub
	t.Cleanup(func() { checkBatchSubtreeLocked = old })
}

func withBatchPathStub(t *testing.T, stub func(*BatchOperationHandler, string, string, string) (bool, string, error)) {
	t.Helper()
	old := checkBatchPathLocked
	checkBatchPathLocked = stub
	t.Cleanup(func() { checkBatchPathLocked = old })
}

func withBatchRepresentationResolverStub(t *testing.T, stub func(*BatchOperationHandler, string, string) (string, error)) {
	t.Helper()
	old := resolveBatchLibraryRepresentationID
	resolveBatchLibraryRepresentationID = stub
	t.Cleanup(func() { resolveBatchLibraryRepresentationID = old })
}

func TestProcessSingleItem_MoveRejectsLockedSource(t *testing.T) {
	withBatchSubtreeStub(t, func(_ *BatchOperationHandler, _, p, _ string) (bool, string, error) {
		if p == "/src/locked.txt" {
			return true, "owner-1", nil
		}
		return false, "", nil
	})

	h := &BatchOperationHandler{}
	err := h.processSingleItem("org", "user", "repo-1", "repo-1", "/src/locked.txt", "/dst", "move", nil)
	if !errors.Is(err, ErrBatchItemLocked) {
		t.Fatalf("move of locked source: err = %v, want ErrBatchItemLocked", err)
	}
}

func TestProcessSingleItem_MoveLockLookupFailureFailsClosed(t *testing.T) {
	withBatchSubtreeStub(t, func(_ *BatchOperationHandler, _, _, _ string) (bool, string, error) {
		return false, "", errors.New("scan failed")
	})

	h := &BatchOperationHandler{}
	err := h.processSingleItem("org", "user", "repo-1", "repo-1", "/src/file.txt", "/dst", "move", nil)
	if !errors.Is(err, ErrBatchLockStatusUnavailable) {
		t.Fatalf("lock lookup failure: err = %v, want ErrBatchLockStatusUnavailable", err)
	}
}

func TestProcessSingleItem_ReplaceRejectsLockedDestination(t *testing.T) {
	// Source is unlocked; the replace target is locked by another user.
	withBatchSubtreeStub(t, func(_ *BatchOperationHandler, _, _, _ string) (bool, string, error) {
		return false, "", nil
	})
	withBatchPathStub(t, func(_ *BatchOperationHandler, _, p, _ string) (bool, string, error) {
		if p == "/dst/file.txt" {
			return true, "owner-2", nil
		}
		return false, "", nil
	})

	h := &BatchOperationHandler{}
	// copy+replace must also be blocked: it overwrites the locked destination.
	err := h.processSingleItem("org", "user", "repo-1", "repo-1", "/src/file.txt", "/dst", "copy", nil, "replace")
	if !errors.Is(err, ErrBatchItemLocked) {
		t.Fatalf("replace onto locked destination: err = %v, want ErrBatchItemLocked", err)
	}
}

func TestProcessSingleItem_ReplaceRejectsLockedDestinationDirectory(t *testing.T) {
	withBatchSubtreeStub(t, func(_ *BatchOperationHandler, _, _, _ string) (bool, string, error) {
		return false, "", nil
	})
	withBatchPathStub(t, func(_ *BatchOperationHandler, _, p, _ string) (bool, string, error) {
		if p == "/dst/folder" {
			return true, "owner-dir", nil
		}
		return false, "", nil
	})

	h := &BatchOperationHandler{}
	err := h.processSingleItem("org", "user", "repo-1", "repo-1", "/src/folder", "/dst", "copy", nil, "replace")
	if !errors.Is(err, ErrBatchItemLocked) {
		t.Fatalf("replace onto locked destination directory: err = %v, want ErrBatchItemLocked", err)
	}
}

func TestProcessSingleItem_CrossRepoRejectsDifferentBlockRepresentations(t *testing.T) {
	withBatchSubtreeStub(t, func(_ *BatchOperationHandler, _, _, _ string) (bool, string, error) {
		return false, "", nil
	})
	withBatchRepresentationResolverStub(t, func(_ *BatchOperationHandler, _, repoID string) (string, error) {
		if repoID == "repo-src" {
			return db.PlainBlockRepresentationID, nil
		}
		return "library:encrypted-dst", nil
	})

	h := &BatchOperationHandler{}
	err := h.processSingleItem("org", "user", "repo-src", "repo-dst", "/src/file.txt", "/dst", "copy", nil)
	if !errors.Is(err, ErrBatchCrossRepresentationUnsupported) {
		t.Fatalf("cross-repo copy mismatch: err = %v, want ErrBatchCrossRepresentationUnsupported", err)
	}
}

// The /file/copy path does NOT go through processSingleItem; copyItemWithinRepoWithRetry
// enforces the replace-destination lock itself. These rejection paths return before any
// fs traversal, so a nil FSHelper is safe.

func withCopyReplaceDestStub(t *testing.T, stub func(*FSHelper, string, string, string) (bool, string, error)) {
	t.Helper()
	old := checkCopyReplaceDestLocked
	checkCopyReplaceDestLocked = stub
	t.Cleanup(func() { checkCopyReplaceDestLocked = old })
}

func TestCopyItemWithinRepo_ReplaceRejectsLockedDestination(t *testing.T) {
	withCopyReplaceDestStub(t, func(_ *FSHelper, _, dstPath, _ string) (bool, string, error) {
		if dstPath == "/dst/file.txt" {
			return true, "owner-9", nil
		}
		return false, "", nil
	})

	_, err := copyItemWithinRepoWithRetry("test", nil, "org", "user", "repo-1", "/src/file.txt", "/dst", "replace")
	if !errors.Is(err, ErrBatchItemLocked) {
		t.Fatalf("copy replace onto locked destination: err = %v, want ErrBatchItemLocked", err)
	}
}

func TestCopyItemWithinRepo_ReplaceRejectsLockedDestinationDirectory(t *testing.T) {
	withCopyReplaceDestStub(t, func(_ *FSHelper, _, dstPath, _ string) (bool, string, error) {
		if dstPath == "/dst/folder" {
			return true, "owner-dir", nil
		}
		return false, "", nil
	})

	_, err := copyItemWithinRepoWithRetry("test", nil, "org", "user", "repo-1", "/src/folder", "/dst", "replace")
	if !errors.Is(err, ErrBatchItemLocked) {
		t.Fatalf("copy replace onto locked destination directory: err = %v, want ErrBatchItemLocked", err)
	}
}

func TestCopyItemWithinRepo_ReplaceLockLookupFailureFailsClosed(t *testing.T) {
	withCopyReplaceDestStub(t, func(_ *FSHelper, _, _, _ string) (bool, string, error) {
		return false, "", errors.New("scan failed")
	})

	_, err := copyItemWithinRepoWithRetry("test", nil, "org", "user", "repo-1", "/src/file.txt", "/dst", "replace")
	if !errors.Is(err, ErrBatchLockStatusUnavailable) {
		t.Fatalf("copy replace lock lookup failure: err = %v, want ErrBatchLockStatusUnavailable", err)
	}
}

// A same-repo move whose conflict policy is "skip" and whose destination already exists
// is a no-op: nothing moves, so the operator's source lock must survive. The bug was
// that processSingleItem cleared source locks on any nil error, including a skip no-op.
// These drive the move execution via the processSameRepoMoveFn seam.

func withSameRepoMoveStub(t *testing.T, stub func(*BatchOperationHandler, string, string, string, string, string, *FSHelper, string) (bool, error)) {
	t.Helper()
	old := processSameRepoMoveFn
	processSameRepoMoveFn = stub
	t.Cleanup(func() { processSameRepoMoveFn = old })
}

func withClearMovedSourceLocksSpy(t *testing.T, spy func(*BatchOperationHandler, string, string, string)) {
	t.Helper()
	old := clearMovedSourceLocks
	clearMovedSourceLocks = spy
	t.Cleanup(func() { clearMovedSourceLocks = old })
}

func TestProcessSingleItem_SkippedMoveDoesNotClearSourceLocks(t *testing.T) {
	withBatchSubtreeStub(t, func(_ *BatchOperationHandler, _, _, _ string) (bool, string, error) {
		return false, "", nil
	})
	// Destination exists + policy "skip" → nothing moved.
	withSameRepoMoveStub(t, func(_ *BatchOperationHandler, _, _, _, _, _ string, _ *FSHelper, _ string) (bool, error) {
		return false, nil
	})
	cleared := false
	withClearMovedSourceLocksSpy(t, func(_ *BatchOperationHandler, _, _, _ string) { cleared = true })

	h := &BatchOperationHandler{}
	err := h.processSingleItem("org", "user", "repo-1", "repo-1", "/src/file.txt", "/dst", "move", nil, "skip")
	if err != nil {
		t.Fatalf("skipped move: err = %v, want nil", err)
	}
	if cleared {
		t.Fatal("source locks were cleared for a skipped (no-op) move; want them preserved")
	}
}

func TestProcessSingleItem_RealMoveClearsSourceLocks(t *testing.T) {
	withBatchSubtreeStub(t, func(_ *BatchOperationHandler, _, _, _ string) (bool, string, error) {
		return false, "", nil
	})
	// Source actually relocated.
	withSameRepoMoveStub(t, func(_ *BatchOperationHandler, _, _, _, _, _ string, _ *FSHelper, _ string) (bool, error) {
		return true, nil
	})
	cleared := false
	withClearMovedSourceLocksSpy(t, func(_ *BatchOperationHandler, _, _, _ string) { cleared = true })

	h := &BatchOperationHandler{}
	err := h.processSingleItem("org", "user", "repo-1", "repo-1", "/src/file.txt", "/dst", "move", nil)
	if err != nil {
		t.Fatalf("real move: err = %v, want nil", err)
	}
	if !cleared {
		t.Fatal("source locks were not cleared after a real move; want them dropped")
	}
}
