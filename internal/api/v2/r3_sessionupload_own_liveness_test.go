package v2

import (
	"errors"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
)

func TestCommitBlockOwnLivenessRenewsSessionUploadWithSameIdentity(t *testing.T) {
	oldAdd := registerUploadedBlockAddProvisionalRefFn
	t.Cleanup(func() { registerUploadedBlockAddProvisionalRefFn = oldAdd })

	const wantReferrer = "up:session-1"
	var calls int
	registerUploadedBlockAddProvisionalRefFn = func(_ *FSHelper, orgID, blockID, referrer, libraryID, storageClass string, expiresAt time.Time) error {
		if orgID != "org-1" || blockID != "b1" || referrer != wantReferrer || libraryID != "repo-1" || storageClass != "hot" {
			t.Fatalf("unexpected SessionUpload renewal: %s/%s/%s/%s/%s", orgID, blockID, referrer, libraryID, storageClass)
		}
		if expiresAt.Before(time.Now()) {
			t.Fatalf("renewed own-liveness expiry %v is in the past", expiresAt)
		}
		calls++
		return nil
	}

	placements := []commitBlockPlacement{{
		blockID: "b1", provenance: blockCommitLivenessSessionUpload,
		storageClass: "hot", storageKey: "k1",
	}, {
		blockID: "b1", provenance: blockCommitLivenessSessionUpload,
		storageClass: "hot", storageKey: "k1",
	}}
	if err := (&FileHandler{}).ensureCommitBlockOwnLiveness(nil, "org-1", "repo-1", "session-1", placements); err != nil {
		t.Fatalf("ensureCommitBlockOwnLiveness: %v", err)
	}
	if calls != 1 {
		t.Fatalf("renewal calls = %d, want one upsert of the existing up:<session> identity", calls)
	}
}

func TestCommitBlockOwnLivenessSessionUploadTransientFailureIsRetryable(t *testing.T) {
	oldAdd := registerUploadedBlockAddProvisionalRefFn
	t.Cleanup(func() { registerUploadedBlockAddProvisionalRefFn = oldAdd })

	attempts := 0
	registerUploadedBlockAddProvisionalRefFn = func(_ *FSHelper, _, _, _, _, _ string, _ time.Time) error {
		attempts++
		if attempts == 1 {
			return errors.New("temporary Cassandra timeout")
		}
		return nil
	}
	placement := []commitBlockPlacement{{
		blockID: "b1", provenance: blockCommitLivenessSessionUpload,
		storageClass: "hot", storageKey: "k1",
	}}

	err := (&FileHandler{}).ensureCommitBlockOwnLiveness(nil, "org-1", "repo-1", "session-1", placement)
	if !errors.Is(err, ErrBlockMaterializationTransient) {
		t.Fatalf("first SessionUpload renewal error = %v, want retryable materialization error", err)
	}
	if err := (&FileHandler{}).ensureCommitBlockOwnLiveness(nil, "org-1", "repo-1", "session-1", placement); err != nil {
		t.Fatalf("retry SessionUpload renewal: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("SessionUpload renewal attempts = %d, want first failure plus one retry", attempts)
	}
}

func TestCommitBlockPublicationFencesCheckSessionUploadPlacement(t *testing.T) {
	oldAuthority := validateBorrowedFSPublicationAuthorityFn
	t.Cleanup(func() { validateBorrowedFSPublicationAuthorityFn = oldAuthority })

	var checked db.BlockPhysicalLocation
	validateBorrowedFSPublicationAuthorityFn = func(_ *db.DB, orgID, blockID string, placement db.BlockPhysicalLocation) (db.BlockRepairAuthorityOutcome, error) {
		if orgID != "org-1" || blockID != "b1" {
			t.Fatalf("unexpected authority arguments: %s/%s", orgID, blockID)
		}
		checked = placement
		return db.BlockRepairAuthorityAuthorized, nil
	}

	placements := []commitBlockPlacement{{
		blockID: "b1", provenance: blockCommitLivenessSessionUpload,
		storageClass: "hot", storageKey: "k1",
	}}
	if err := (&FileHandler{}).validateCommitBlockPublicationFences("org-1", placements); err != nil {
		t.Fatalf("validateCommitBlockPublicationFences: %v", err)
	}
	if checked.StorageClass != "hot" || checked.StorageKey != "k1" {
		t.Fatalf("checked placement = %#v, want hot/k1", checked)
	}
}

func TestCommitBlockPublicationFencesRejectSessionUploadAuthorityStates(t *testing.T) {
	oldAuthority := validateBorrowedFSPublicationAuthorityFn
	t.Cleanup(func() { validateBorrowedFSPublicationAuthorityFn = oldAuthority })

	for _, tc := range []struct {
		name    string
		outcome db.BlockRepairAuthorityOutcome
		cause   error
	}{
		{name: "blocked", outcome: db.BlockRepairAuthorityBlocked, cause: db.ErrBlockRepairBlocked},
		{name: "changed", outcome: db.BlockRepairAuthorityChanged, cause: db.ErrBlockRepairAuthorityChanged},
		{name: "fully-retired", outcome: db.BlockRepairAuthorityChanged, cause: db.ErrBlockRepairAuthorityChanged},
	} {
		t.Run(tc.name, func(t *testing.T) {
			validateBorrowedFSPublicationAuthorityFn = func(*db.DB, string, string, db.BlockPhysicalLocation) (db.BlockRepairAuthorityOutcome, error) {
				return tc.outcome, tc.cause
			}
			placements := []commitBlockPlacement{{
				blockID: "b1", provenance: blockCommitLivenessSessionUpload,
				storageClass: "hot", storageKey: "k1",
			}}
			err := (&FileHandler{}).validateCommitBlockPublicationFences("org-1", placements)
			if !errors.Is(err, ErrBlockDeleteInProgress) || !errors.Is(err, tc.cause) {
				t.Fatalf("SessionUpload authority outcome=%v error=%v; want delete-in-progress plus %v", tc.outcome, err, tc.cause)
			}
		})
	}
}
