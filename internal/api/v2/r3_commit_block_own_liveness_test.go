package v2

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
)

func TestEnsureCommitBlockOwnLivenessPinsExactDistinctBlocks(t *testing.T) {
	oldAdd := registerUploadedBlockAddProvisionalRefFn
	t.Cleanup(func() { registerUploadedBlockAddProvisionalRefFn = oldAdd })

	wantReferrer := "up:session-1"
	var mu sync.Mutex
	calls := make(map[string]int)
	registerUploadedBlockAddProvisionalRefFn = func(_ *FSHelper, orgID, blockID, referrer, libraryID, storageClass string, expiresAt time.Time) error {
		if orgID != "org-1" || libraryID != "repo-1" || referrer != wantReferrer || storageClass != "hot" || expiresAt.Before(time.Now()) {
			t.Fatalf("unexpected own-liveness arguments: %s/%s/%s/%s/%s", orgID, blockID, referrer, libraryID, storageClass)
		}
		// ensureCommitBlockOwnLiveness fans distinct blocks out across concurrent
		// goroutines (errgroup), so this map needs its own lock: Go maps are not
		// safe for concurrent writes even to disjoint keys.
		mu.Lock()
		calls[blockID]++
		mu.Unlock()
		return nil
	}

	blocks := []commitBlockPlacement{
		{blockID: "b1", provenance: blockCommitLivenessBorrowedFS, storageClass: "hot", storageKey: "k1"},
		{blockID: "b1", provenance: blockCommitLivenessBorrowedFS, storageClass: "hot", storageKey: "k1"},
		{blockID: "b2", provenance: blockCommitLivenessBorrowedFS, storageClass: "hot", storageKey: "k2"},
	}
	if err := (&FileHandler{}).ensureCommitBlockOwnLiveness(nil, "org-1", "repo-1", "session-1", blocks); err != nil {
		t.Fatalf("ensureCommitBlockOwnLiveness: %v", err)
	}
	if len(calls) != 2 || calls["b1"] != 1 || calls["b2"] != 1 {
		t.Fatalf("own-liveness calls = %v, want one call for each distinct block", calls)
	}
}

// TestEnsureCommitBlockOwnLivenessRenewsSessionUploadPin confirms
// ensureCommitBlockOwnLiveness treats a SessionUpload-provenance placement
// identically to a BorrowedFS one: it re-registers the same up:<session>
// referrer, which AddProvisionalBlockReferenceWithExpiry treats as an
// idempotent TTL renewal (same primary key) rather than a second pin. This is
// the W2 SessionUpload-parity extension -- see the function's doc comment.
func TestEnsureCommitBlockOwnLivenessRenewsSessionUploadPin(t *testing.T) {
	oldAdd := registerUploadedBlockAddProvisionalRefFn
	t.Cleanup(func() { registerUploadedBlockAddProvisionalRefFn = oldAdd })

	renewCalls := 0
	registerUploadedBlockAddProvisionalRefFn = func(_ *FSHelper, orgID, blockID, referrer, libraryID, storageClass string, expiresAt time.Time) error {
		if referrer != "up:session-1" {
			t.Fatalf("SessionUpload renewal referrer = %q, want up:session-1", referrer)
		}
		renewCalls++
		return nil
	}
	blocks := []commitBlockPlacement{
		{blockID: "b1", provenance: blockCommitLivenessSessionUpload, storageClass: "hot", storageKey: "k1"},
	}
	if err := (&FileHandler{}).ensureCommitBlockOwnLiveness(nil, "org-1", "repo-1", "session-1", blocks); err != nil {
		t.Fatalf("ensureCommitBlockOwnLiveness: %v", err)
	}
	if renewCalls != 1 {
		t.Fatalf("SessionUpload renewal calls = %d, want 1", renewCalls)
	}
}

func TestEnsureCommitBlockOwnLivenessEmptySetMakesNoCalls(t *testing.T) {
	oldAdd := registerUploadedBlockAddProvisionalRefFn
	t.Cleanup(func() { registerUploadedBlockAddProvisionalRefFn = oldAdd })

	addCalls := 0
	registerUploadedBlockAddProvisionalRefFn = func(*FSHelper, string, string, string, string, string, time.Time) error {
		addCalls++
		return nil
	}
	if err := (&FileHandler{}).ensureCommitBlockOwnLiveness(nil, "org-1", "repo-1", "session-1", nil); err != nil {
		t.Fatalf("empty commit-block liveness set: %v", err)
	}
	if addCalls != 0 {
		t.Fatalf("empty commit-block set made %d pin calls, want 0", addCalls)
	}
}

func TestEnsureCommitBlockOwnLivenessFailureIsRetryable(t *testing.T) {
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
	block := []commitBlockPlacement{{blockID: "b1", provenance: blockCommitLivenessBorrowedFS, storageClass: "hot", storageKey: "k1"}}
	if err := (&FileHandler{}).ensureCommitBlockOwnLiveness(nil, "org-1", "repo-1", "session-1", block); !errors.Is(err, ErrBlockMaterializationTransient) {
		t.Fatalf("first own-liveness attempt error = %v, want transient", err)
	}
	if err := (&FileHandler{}).ensureCommitBlockOwnLiveness(nil, "org-1", "repo-1", "session-1", block); err != nil {
		t.Fatalf("retry own-liveness: %v", err)
	}
	if attempts != 2 || len(refs) != 1 {
		t.Fatalf("retry attempts/refs = %d/%v, want 2/one idempotent ref", attempts, refs)
	}
}

func TestValidateCommitBlockPublicationFencesRejectsActiveDelete(t *testing.T) {
	oldAuthority := validateBorrowedFSPublicationAuthorityFn
	t.Cleanup(func() { validateBorrowedFSPublicationAuthorityFn = oldAuthority })

	validateBorrowedFSPublicationAuthorityFn = func(_ *db.DB, _, blockID string, _ db.BlockPhysicalLocation) (db.BlockRepairAuthorityOutcome, error) {
		if blockID != "b1" {
			t.Fatalf("authority checked block %q, want b1", blockID)
		}
		return db.BlockRepairAuthorityBlocked, db.ErrBlockRepairBlocked
	}
	blocks := []commitBlockPlacement{{blockID: "b1", provenance: blockCommitLivenessBorrowedFS, storageClass: "hot", storageKey: "k1"}}
	if err := (&FileHandler{}).validateCommitBlockPublicationFences("org-1", blocks); !errors.Is(err, ErrBlockDeleteInProgress) {
		t.Fatalf("validateCommitBlockPublicationFences error = %v, want ErrBlockDeleteInProgress", err)
	}
}

// TestValidateCommitBlockPublicationFencesRejectsFullyRetiredPlacement is the
// exact-P revalidation this handshake was missing: BlockDeleteFenceActive
// alone reports false once GC has fully retired a block (Finalize + settled
// orphan), because there is no active claim and no orphan row left to
// observe -- but the placement this commit observed no longer exists.
// ValidateBorrowedFSPublicationAuthority reports that terminal state as
// BlockRepairAuthorityChanged, and it must reject the commit exactly like an
// active fence would.
func TestValidateCommitBlockPublicationFencesRejectsFullyRetiredPlacement(t *testing.T) {
	oldAuthority := validateBorrowedFSPublicationAuthorityFn
	t.Cleanup(func() { validateBorrowedFSPublicationAuthorityFn = oldAuthority })

	validateBorrowedFSPublicationAuthorityFn = func(_ *db.DB, _, blockID string, _ db.BlockPhysicalLocation) (db.BlockRepairAuthorityOutcome, error) {
		if blockID != "b1" {
			t.Fatalf("authority checked block %q, want b1", blockID)
		}
		return db.BlockRepairAuthorityChanged, db.ErrBlockRepairAuthorityChanged
	}
	blocks := []commitBlockPlacement{{blockID: "b1", provenance: blockCommitLivenessBorrowedFS, storageClass: "hot", storageKey: "k1"}}
	if err := (&FileHandler{}).validateCommitBlockPublicationFences("org-1", blocks); !errors.Is(err, ErrBlockDeleteInProgress) {
		t.Fatalf("validateCommitBlockPublicationFences error = %v, want ErrBlockDeleteInProgress", err)
	}
}

// TestValidateCommitBlockPublicationFencesChecksSessionUploadBlocks confirms
// the pre-HEAD exact-placement check now covers SessionUpload-provenance
// blocks exactly like BorrowedFS ones -- the W2 SessionUpload-parity
// extension. The check itself does not branch on provenance.
func TestValidateCommitBlockPublicationFencesChecksSessionUploadBlocks(t *testing.T) {
	oldAuthority := validateBorrowedFSPublicationAuthorityFn
	t.Cleanup(func() { validateBorrowedFSPublicationAuthorityFn = oldAuthority })

	checked := false
	validateBorrowedFSPublicationAuthorityFn = func(_ *db.DB, _, blockID string, _ db.BlockPhysicalLocation) (db.BlockRepairAuthorityOutcome, error) {
		if blockID != "b1" {
			t.Fatalf("authority checked block %q, want b1", blockID)
		}
		checked = true
		return db.BlockRepairAuthorityAuthorized, nil
	}
	blocks := []commitBlockPlacement{{blockID: "b1", provenance: blockCommitLivenessSessionUpload, storageClass: "hot", storageKey: "k1"}}
	if err := (&FileHandler{}).validateCommitBlockPublicationFences("org-1", blocks); err != nil {
		t.Fatalf("validateCommitBlockPublicationFences: %v", err)
	}
	if !checked {
		t.Fatal("SessionUpload block was not passed through the exact-placement authority check")
	}
}

func TestValidateCommitBlockPublicationFencesEmptySetMakesNoAuthorityCalls(t *testing.T) {
	oldAuthority := validateBorrowedFSPublicationAuthorityFn
	t.Cleanup(func() { validateBorrowedFSPublicationAuthorityFn = oldAuthority })

	authorityCalls := 0
	validateBorrowedFSPublicationAuthorityFn = func(*db.DB, string, string, db.BlockPhysicalLocation) (db.BlockRepairAuthorityOutcome, error) {
		authorityCalls++
		return db.BlockRepairAuthorityAuthorized, nil
	}
	if err := (&FileHandler{}).validateCommitBlockPublicationFences("org-1", nil); err != nil {
		t.Fatalf("empty commit-block fence set: %v", err)
	}
	if authorityCalls != 0 {
		t.Fatalf("empty commit-block set made %d authority calls, want 0", authorityCalls)
	}
}
