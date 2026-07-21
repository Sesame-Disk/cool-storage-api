package v2

import (
	"errors"
	"fmt"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

func TestGetCanonicalHeadCommit_LibraryStateErrorIsNotMaskedAsNotFound(t *testing.T) {
	helper := &FSHelper{db: &db.DB{}}
	original := resolveLiveLibraryStateByIDFn
	resolveLiveLibraryStateByIDFn = func(_ *gocql.Session, _ string) (db.LibraryState, error) {
		return db.LibraryState{}, errors.New("cassandra unavailable")
	}
	t.Cleanup(func() {
		resolveLiveLibraryStateByIDFn = original
	})

	_, _, err := helper.getCanonicalHeadCommit("11111111-1111-1111-1111-111111111111")
	if err == nil {
		t.Fatal("getCanonicalHeadCommit() error = nil, want internal error")
	}
	if !strings.Contains(err.Error(), "failed to check library state") {
		t.Fatalf("getCanonicalHeadCommit() error = %v, want failed-to-check-library-state prefix", err)
	}
}

// TestRegisterFSObjectBlockReferences_ResolutionFailureAborts verifies the
// row-per-reference registration is fail-closed: if block ID resolution fails, no
// reference row is written and the caller's commit is aborted. The resolution
// error is returned before any DB write, so a zero-value FSHelper (nil db) is
// enough to exercise the guard.
func TestRegisterFSObjectBlockReferences_ResolutionFailureAborts(t *testing.T) {
	helper := &FSHelper{}
	prevResolve := resolveStoredBlockIDsFn
	prevExists := registerFSObjectBlockReferencesFSObjectExistsFn
	prevAdd := registerFSObjectBlockReferencesAddReferenceFn
	defer func() { resolveStoredBlockIDsFn = prevResolve }()
	defer func() { registerFSObjectBlockReferencesFSObjectExistsFn = prevExists }()
	defer func() { registerFSObjectBlockReferencesAddReferenceFn = prevAdd }()

	registerFSObjectBlockReferencesFSObjectExistsFn = func(h *FSHelper, libraryID, fsID string) (bool, error) {
		if libraryID != "lib-1" || fsID != "fs-1" {
			t.Fatalf("exists args = %s/%s, want lib-1/fs-1", libraryID, fsID)
		}
		return true, nil
	}
	registerFSObjectBlockReferencesAddReferenceFn = func(h *FSHelper, orgID, blockID, referrer, libraryID string) error {
		t.Fatal("add reference should not run when block ID resolution fails")
		return nil
	}

	resolveErr := errors.New("resolve failed")
	resolveStoredBlockIDsFn = func(h *FSHelper, orgID, libraryID string, blockIDs []string) ([]string, error) {
		if orgID != "org-1" {
			t.Fatalf("resolve orgID = %q, want %q", orgID, "org-1")
		}
		if libraryID != "lib-1" {
			t.Fatalf("resolve libraryID = %q, want %q", libraryID, "lib-1")
		}
		if len(blockIDs) != 1 || blockIDs[0] != "sha1-block" {
			t.Fatalf("resolve blockIDs = %v, want [sha1-block]", blockIDs)
		}
		return nil, resolveErr
	}

	err := helper.RegisterFSObjectBlockReferences("org-1", "lib-1", "fs-1", []string{"sha1-block"})
	if !errors.Is(err, resolveErr) {
		t.Fatalf("RegisterFSObjectBlockReferences() error = %v, want wrapped %v", err, resolveErr)
	}
}

func TestRegisterFSObjectBlockReferences_RequiresPersistedFSObject(t *testing.T) {
	helper := &FSHelper{}
	prevResolve := resolveStoredBlockIDsFn
	prevExists := registerFSObjectBlockReferencesFSObjectExistsFn
	prevAdd := registerFSObjectBlockReferencesAddReferenceFn
	t.Cleanup(func() {
		resolveStoredBlockIDsFn = prevResolve
		registerFSObjectBlockReferencesFSObjectExistsFn = prevExists
		registerFSObjectBlockReferencesAddReferenceFn = prevAdd
	})

	resolveStoredBlockIDsFn = func(h *FSHelper, orgID, libraryID string, blockIDs []string) ([]string, error) {
		t.Fatal("resolve should not run when the fs_object row is missing")
		return nil, nil
	}
	registerFSObjectBlockReferencesFSObjectExistsFn = func(h *FSHelper, libraryID, fsID string) (bool, error) {
		if libraryID != "lib-1" || fsID != "fs-1" {
			t.Fatalf("exists args = %s/%s, want lib-1/fs-1", libraryID, fsID)
		}
		return false, nil
	}
	registerFSObjectBlockReferencesAddReferenceFn = func(h *FSHelper, orgID, blockID, referrer, libraryID string) error {
		t.Fatal("add reference should not run when the fs_object row is missing")
		return nil
	}

	err := helper.RegisterFSObjectBlockReferences("org-1", "lib-1", "fs-1", []string{"sha1-block"})
	if !errors.Is(err, errFSObjectNotPersistedForBlockReferences) {
		t.Fatalf("RegisterFSObjectBlockReferences() error = %v, want wrapped %v", err, errFSObjectNotPersistedForBlockReferences)
	}
}

func TestRegisterFSObjectBlockReferences_AddsReferencesForPersistedFSObject(t *testing.T) {
	helper := &FSHelper{}
	prevResolve := resolveStoredBlockIDsFn
	prevExists := registerFSObjectBlockReferencesFSObjectExistsFn
	prevAdd := registerFSObjectBlockReferencesAddReferenceFn
	t.Cleanup(func() {
		resolveStoredBlockIDsFn = prevResolve
		registerFSObjectBlockReferencesFSObjectExistsFn = prevExists
		registerFSObjectBlockReferencesAddReferenceFn = prevAdd
	})

	registerFSObjectBlockReferencesFSObjectExistsFn = func(h *FSHelper, libraryID, fsID string) (bool, error) {
		return true, nil
	}
	resolveStoredBlockIDsFn = func(h *FSHelper, orgID, libraryID string, blockIDs []string) ([]string, error) {
		if orgID != "org-1" {
			t.Fatalf("resolve orgID = %q, want org-1", orgID)
		}
		if libraryID != "lib-1" {
			t.Fatalf("resolve libraryID = %q, want lib-1", libraryID)
		}
		if len(blockIDs) != 2 || blockIDs[0] != "sha1-a" || blockIDs[1] != "sha1-b" {
			t.Fatalf("resolve blockIDs = %v, want [sha1-a sha1-b]", blockIDs)
		}
		return []string{"sha256-a", "sha256-b"}, nil
	}
	var calls []string
	registerFSObjectBlockReferencesAddReferenceFn = func(h *FSHelper, orgID, blockID, referrer, libraryID string) error {
		calls = append(calls, fmt.Sprintf("%s|%s|%s|%s", orgID, blockID, referrer, libraryID))
		return nil
	}

	err := helper.RegisterFSObjectBlockReferences("org-1", "lib-1", "fs-1", []string{"sha1-a", "sha1-b"})
	if err != nil {
		t.Fatalf("RegisterFSObjectBlockReferences() error = %v, want nil", err)
	}
	want := []string{
		"org-1|sha256-a|" + db.BlockReferrerForFSObject("lib-1", "fs-1") + "|lib-1",
		"org-1|sha256-b|" + db.BlockReferrerForFSObject("lib-1", "fs-1") + "|lib-1",
	}
	if len(calls) != len(want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("calls[%d] = %q, want %q (full=%v)", i, calls[i], want[i], calls)
		}
	}
}

func TestRegisterUploadedBlock_PropagatesObservedFenceWithoutSleeping(t *testing.T) {
	helper := &FSHelper{}
	oldAdd := registerUploadedBlockAddReferenceFn
	oldFence := registerUploadedBlockFenceActiveFn
	oldClaimInfo := registerUploadedBlockClaimInfoFn
	oldReleaseClaim := registerUploadedBlockReleaseClaimFn
	oldUpsert := registerUploadedBlockUpsertMetadataFn
	oldUpsertExpiry := registerUploadedBlockUpsertProvisionalExpiryFn
	oldRelease := registerUploadedBlockReleaseRefsFn
	oldSleep := registerUploadedBlockSleepFn
	oldEnqueue := registerUploadedBlockEnqueueZeroRefFn
	t.Cleanup(func() {
		registerUploadedBlockAddReferenceFn = oldAdd
		registerUploadedBlockFenceActiveFn = oldFence
		registerUploadedBlockClaimInfoFn = oldClaimInfo
		registerUploadedBlockReleaseClaimFn = oldReleaseClaim
		registerUploadedBlockUpsertMetadataFn = oldUpsert
		registerUploadedBlockUpsertProvisionalExpiryFn = oldUpsertExpiry
		registerUploadedBlockReleaseRefsFn = oldRelease
		registerUploadedBlockSleepFn = oldSleep
		registerUploadedBlockEnqueueZeroRefFn = oldEnqueue
	})

	var calls []string
	registerUploadedBlockAddReferenceFn = func(h *FSHelper, orgID, blockID, referrer, libraryID string, ttlSeconds int) error {
		calls = append(calls, "add")
		if orgID != "org-1" || blockID != "block-1" || libraryID != "lib-1" {
			t.Fatalf("add args = %s/%s/%s, want org-1/block-1/lib-1", orgID, blockID, libraryID)
		}
		if referrer != db.BlockReferrerForUpload("op-1") {
			t.Fatalf("referrer = %q, want upload referrer", referrer)
		}
		if ttlSeconds != db.ProvisionalBlockReferenceTTLSeconds {
			t.Fatalf("ttlSeconds = %d, want %d", ttlSeconds, db.ProvisionalBlockReferenceTTLSeconds)
		}
		return nil
	}
	registerUploadedBlockFenceActiveFn = func(h *FSHelper, orgID, blockID string) (bool, error) {
		calls = append(calls, "fence")
		return true, nil
	}
	registerUploadedBlockClaimInfoFn = func(h *FSHelper, orgID, blockID string) (db.BlockDeleteClaimInfo, bool, error) {
		t.Fatal("stale claim lookup should not run for a normal retrying fence")
		return db.BlockDeleteClaimInfo{}, false, nil
	}
	registerUploadedBlockReleaseClaimFn = func(h *FSHelper, orgID, blockID, claimID string) (bool, error) {
		t.Fatal("stale claim release should not run for a normal retrying fence")
		return false, nil
	}
	registerUploadedBlockUpsertMetadataFn = func(h *FSHelper, orgID, libraryID, blockID, sha1ID string, sizeBytes int, storageClass, storageKey string) error {
		t.Fatal("metadata must not be published after observing a delete fence")
		return nil
	}
	registerUploadedBlockUpsertProvisionalExpiryFn = func(h *FSHelper, orgID, blockID, referrer, storageClass string, expiresAt time.Time) error {
		calls = append(calls, "expiry")
		if orgID != "org-1" || blockID != "block-1" || referrer != db.BlockReferrerForUpload("op-1") || storageClass != "hot" {
			t.Fatalf("expiry args = %s/%s/%s/%s", orgID, blockID, referrer, storageClass)
		}
		if expiresAt.IsZero() {
			t.Fatal("expiresAt should be set")
		}
		return nil
	}
	registerUploadedBlockReleaseRefsFn = func(h *FSHelper, orgID, libraryID, operationID string, blockIDs []string) []string {
		t.Fatal("observing a fence must preserve the shared TTL pin")
		return nil
	}
	registerUploadedBlockSleepFn = func(delay time.Duration) {
		t.Fatalf("RegisterUploadedBlock must not sleep, got %s", delay)
	}
	registerUploadedBlockEnqueueZeroRefFn = func(orgID string, blockIDs []string, storageClass string) {
		t.Fatal("a preserved TTL pin must not be enqueued as zero-reference")
	}

	err := helper.RegisterUploadedBlock("org-1", "lib-1", "block-1", "op-1", 123, "hot", "key-1", "sha1-1")
	if !errors.Is(err, ErrBlockDeleteInProgress) {
		t.Fatalf("RegisterUploadedBlock() error = %v, want ErrBlockDeleteInProgress", err)
	}
	want := []string{"add", "expiry", "fence"}
	if len(calls) != len(want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("calls[%d] = %q, want %q (full=%#v)", i, calls[i], want[i], calls)
		}
	}
}

func TestRegisterUploadedBlock_ReleasesStaleDeleteClaimWithEmptyStorageClass(t *testing.T) {
	helper := &FSHelper{db: &db.DB{}}
	oldAdd := registerUploadedBlockAddReferenceFn
	oldFence := registerUploadedBlockFenceActiveFn
	oldClaimInfo := registerUploadedBlockClaimInfoFn
	oldReleaseClaim := registerUploadedBlockReleaseClaimFn
	oldUpsert := registerUploadedBlockUpsertMetadataFn
	oldUpsertExpiry := registerUploadedBlockUpsertProvisionalExpiryFn
	oldRelease := registerUploadedBlockReleaseRefsFn
	oldSleep := registerUploadedBlockSleepFn
	oldEnqueue := registerUploadedBlockEnqueueZeroRefFn
	t.Cleanup(func() {
		registerUploadedBlockAddReferenceFn = oldAdd
		registerUploadedBlockFenceActiveFn = oldFence
		registerUploadedBlockClaimInfoFn = oldClaimInfo
		registerUploadedBlockReleaseClaimFn = oldReleaseClaim
		registerUploadedBlockUpsertMetadataFn = oldUpsert
		registerUploadedBlockUpsertProvisionalExpiryFn = oldUpsertExpiry
		registerUploadedBlockReleaseRefsFn = oldRelease
		registerUploadedBlockSleepFn = oldSleep
		registerUploadedBlockEnqueueZeroRefFn = oldEnqueue
	})

	var calls []string
	registerUploadedBlockAddReferenceFn = func(h *FSHelper, orgID, blockID, referrer, libraryID string, ttlSeconds int) error {
		calls = append(calls, "add")
		return nil
	}
	registerUploadedBlockFenceActiveFn = func(h *FSHelper, orgID, blockID string) (bool, error) {
		calls = append(calls, "fence")
		return true, nil
	}
	registerUploadedBlockClaimInfoFn = func(h *FSHelper, orgID, blockID string) (db.BlockDeleteClaimInfo, bool, error) {
		calls = append(calls, "claim-info")
		return db.BlockDeleteClaimInfo{GCState: db.BlockGCStateDeleting, GCClaimID: "claim-1"}, true, nil
	}
	registerUploadedBlockReleaseClaimFn = func(h *FSHelper, orgID, blockID, claimID string) (bool, error) {
		calls = append(calls, "release-claim")
		if claimID != "claim-1" {
			t.Fatalf("claimID = %q, want claim-1", claimID)
		}
		return true, nil
	}
	registerUploadedBlockUpsertMetadataFn = func(h *FSHelper, orgID, libraryID, blockID, sha1ID string, sizeBytes int, storageClass, storageKey string) error {
		t.Fatal("metadata must not be published from the PUT that observed the stale claim")
		return nil
	}
	registerUploadedBlockUpsertProvisionalExpiryFn = func(h *FSHelper, orgID, blockID, referrer, storageClass string, expiresAt time.Time) error {
		calls = append(calls, "expiry")
		return nil
	}
	registerUploadedBlockReleaseRefsFn = func(h *FSHelper, orgID, libraryID, operationID string, blockIDs []string) []string {
		t.Fatal("observing a stale fence must preserve the shared TTL pin")
		return nil
	}
	registerUploadedBlockSleepFn = func(delay time.Duration) {
		t.Fatalf("sleep should not run when stale claim release succeeds, got %s", delay)
	}
	registerUploadedBlockEnqueueZeroRefFn = func(orgID string, blockIDs []string, storageClass string) {
		t.Fatal("a preserved TTL pin must not be enqueued as zero-reference")
	}

	if err := helper.RegisterUploadedBlock("org-1", "lib-1", "block-1", "op-1", 123, "hot", "key-1", "sha1-1"); !errors.Is(err, ErrBlockDeleteInProgress) {
		t.Fatalf("RegisterUploadedBlock() error = %v, want ErrBlockDeleteInProgress", err)
	}
	want := []string{"add", "expiry", "fence", "claim-info", "release-claim"}
	if len(calls) != len(want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("calls[%d] = %q, want %q (full=%#v)", i, calls[i], want[i], calls)
		}
	}
}

func TestRegisterUploadedBlock_RecordsProvisionalExpiryAtTTL(t *testing.T) {
	helper := &FSHelper{}
	oldAdd := registerUploadedBlockAddReferenceFn
	oldFence := registerUploadedBlockFenceActiveFn
	oldUpsert := registerUploadedBlockUpsertMetadataFn
	oldUpsertExpiry := registerUploadedBlockUpsertProvisionalExpiryFn
	t.Cleanup(func() {
		registerUploadedBlockAddReferenceFn = oldAdd
		registerUploadedBlockFenceActiveFn = oldFence
		registerUploadedBlockUpsertMetadataFn = oldUpsert
		registerUploadedBlockUpsertProvisionalExpiryFn = oldUpsertExpiry
	})

	registerUploadedBlockAddReferenceFn = func(h *FSHelper, orgID, blockID, referrer, libraryID string, ttlSeconds int) error {
		return nil
	}
	registerUploadedBlockFenceActiveFn = func(h *FSHelper, orgID, blockID string) (bool, error) {
		return false, nil
	}
	registerUploadedBlockUpsertMetadataFn = func(h *FSHelper, orgID, libraryID, blockID, sha1ID string, sizeBytes int, storageClass, storageKey string) error {
		return nil
	}

	var expiresAt time.Time
	registerUploadedBlockUpsertProvisionalExpiryFn = func(h *FSHelper, orgID, blockID, referrer, storageClass string, value time.Time) error {
		if orgID != "org-1" || blockID != "block-1" || referrer != db.BlockReferrerForUpload("op-1") || storageClass != "hot" {
			t.Fatalf("expiry args = %s/%s/%s/%s", orgID, blockID, referrer, storageClass)
		}
		expiresAt = value
		return nil
	}

	before := time.Now().UTC()
	err := helper.RegisterUploadedBlock("org-1", "lib-1", "block-1", "op-1", 123, "hot", "key-1", "sha1-1")
	after := time.Now().UTC()
	if err != nil {
		t.Fatalf("RegisterUploadedBlock() error = %v, want nil", err)
	}
	if expiresAt.IsZero() {
		t.Fatal("expected provisional expiry tracker to be recorded")
	}
	wantMin := before.Add(time.Duration(db.ProvisionalBlockReferenceTTLSeconds) * time.Second)
	wantMax := after.Add(time.Duration(db.ProvisionalBlockReferenceTTLSeconds) * time.Second)
	if expiresAt.Before(wantMin) || expiresAt.After(wantMax) {
		t.Fatalf("expiresAt = %v, want between %v and %v", expiresAt, wantMin, wantMax)
	}
}

func TestRegisterUploadedBlock_RollsBackWhenExpiryTrackingFails(t *testing.T) {
	helper := &FSHelper{}
	oldAdd := registerUploadedBlockAddReferenceFn
	oldFence := registerUploadedBlockFenceActiveFn
	oldUpsert := registerUploadedBlockUpsertMetadataFn
	oldUpsertExpiry := registerUploadedBlockUpsertProvisionalExpiryFn
	oldRelease := registerUploadedBlockReleaseRefsFn
	oldEnqueue := registerUploadedBlockEnqueueZeroRefFn
	t.Cleanup(func() {
		registerUploadedBlockAddReferenceFn = oldAdd
		registerUploadedBlockFenceActiveFn = oldFence
		registerUploadedBlockUpsertMetadataFn = oldUpsert
		registerUploadedBlockUpsertProvisionalExpiryFn = oldUpsertExpiry
		registerUploadedBlockReleaseRefsFn = oldRelease
		registerUploadedBlockEnqueueZeroRefFn = oldEnqueue
	})

	var calls []string
	registerUploadedBlockAddReferenceFn = func(h *FSHelper, orgID, blockID, referrer, libraryID string, ttlSeconds int) error {
		calls = append(calls, "add")
		return nil
	}
	registerUploadedBlockFenceActiveFn = func(h *FSHelper, orgID, blockID string) (bool, error) {
		t.Fatal("fence check should not run when expiry tracking fails")
		return false, nil
	}
	registerUploadedBlockUpsertMetadataFn = func(h *FSHelper, orgID, libraryID, blockID, sha1ID string, sizeBytes int, storageClass, storageKey string) error {
		t.Fatal("metadata upsert should not run when expiry tracking fails")
		return nil
	}
	expiryErr := errors.New("expiry write failed")
	registerUploadedBlockUpsertProvisionalExpiryFn = func(h *FSHelper, orgID, blockID, referrer, storageClass string, expiresAt time.Time) error {
		calls = append(calls, "expiry")
		return expiryErr
	}
	registerUploadedBlockReleaseRefsFn = func(h *FSHelper, orgID, libraryID, operationID string, blockIDs []string) []string {
		t.Fatal("expiry failure must not delete a pin shared by another attempt")
		return nil
	}
	registerUploadedBlockEnqueueZeroRefFn = func(orgID string, blockIDs []string, storageClass string) {
		t.Fatal("a preserved TTL pin must not be enqueued as zero-reference")
	}

	err := helper.RegisterUploadedBlock("org-1", "lib-1", "block-1", "op-1", 123, "hot", "key-1", "sha1-1")
	if !errors.Is(err, expiryErr) {
		t.Fatalf("RegisterUploadedBlock() error = %v, want wrapped %v", err, expiryErr)
	}
	want := []string{"add", "expiry"}
	if len(calls) != len(want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("calls[%d] = %q, want %q (full=%#v)", i, calls[i], want[i], calls)
		}
	}
}

func TestRegisterUploadedBlock_ReenqueuesZeroRefWhenFenceNeverClears(t *testing.T) {
	helper := &FSHelper{}
	oldAdd := registerUploadedBlockAddReferenceFn
	oldFence := registerUploadedBlockFenceActiveFn
	oldUpsert := registerUploadedBlockUpsertMetadataFn
	oldRelease := registerUploadedBlockReleaseRefsFn
	oldSleep := registerUploadedBlockSleepFn
	oldEnqueue := registerUploadedBlockEnqueueZeroRefFn
	oldExpiry := registerUploadedBlockUpsertProvisionalExpiryFn
	t.Cleanup(func() {
		registerUploadedBlockAddReferenceFn = oldAdd
		registerUploadedBlockFenceActiveFn = oldFence
		registerUploadedBlockUpsertMetadataFn = oldUpsert
		registerUploadedBlockReleaseRefsFn = oldRelease
		registerUploadedBlockSleepFn = oldSleep
		registerUploadedBlockEnqueueZeroRefFn = oldEnqueue
		registerUploadedBlockUpsertProvisionalExpiryFn = oldExpiry
	})

	var calls []string
	registerUploadedBlockAddReferenceFn = func(h *FSHelper, orgID, blockID, referrer, libraryID string, ttlSeconds int) error {
		calls = append(calls, "add")
		return nil
	}
	registerUploadedBlockFenceActiveFn = func(h *FSHelper, orgID, blockID string) (bool, error) {
		calls = append(calls, "fence")
		return true, nil
	}
	registerUploadedBlockUpsertProvisionalExpiryFn = func(h *FSHelper, orgID, blockID, referrer, storageClass string, expiresAt time.Time) error {
		calls = append(calls, "expiry")
		return nil
	}
	registerUploadedBlockUpsertMetadataFn = func(h *FSHelper, orgID, libraryID, blockID, sha1ID string, sizeBytes int, storageClass, storageKey string) error {
		t.Fatal("upsert should not run while the delete fence remains active")
		return nil
	}
	registerUploadedBlockReleaseRefsFn = func(h *FSHelper, orgID, libraryID, operationID string, blockIDs []string) []string {
		t.Fatal("fence exhaustion must preserve the shared TTL pin")
		return nil
	}
	registerUploadedBlockSleepFn = func(delay time.Duration) {
		t.Fatalf("RegisterUploadedBlock must not sleep, got %s", delay)
	}
	registerUploadedBlockEnqueueZeroRefFn = func(orgID string, blockIDs []string, storageClass string) {
		t.Fatal("a preserved TTL pin must not be enqueued as zero-reference")
	}

	err := helper.RegisterUploadedBlock("org-1", "lib-1", "block-1", "op-1", 123, "hot", "", "")
	if !errors.Is(err, ErrBlockDeleteInProgress) {
		t.Fatalf("RegisterUploadedBlock() error = %v, want ErrBlockDeleteInProgress", err)
	}
	want := []string{"add", "expiry", "fence"}
	if len(calls) != len(want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("calls[%d] = %q, want %q (full=%#v)", i, calls[i], want[i], calls)
		}
	}
}

func TestStagePendingPublishedFiles_AssignsResolvedInternalBlockIDs(t *testing.T) {
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

	persistCalls := 0
	stagePendingPublishedFilesPersistFn = func(h *FSHelper, repoID string, pending *pendingPublishedFile) error {
		persistCalls++
		if repoID != "repo-1" {
			t.Fatalf("persist repoID = %q, want repo-1", repoID)
		}
		if pending.fsID != "fs-1" {
			t.Fatalf("persist fsID = %q, want fs-1", pending.fsID)
		}
		if pending.cleanupOrgID != "org-1" || pending.cleanupAttemptID != "commit-1" {
			t.Fatalf("persist metadata = %q/%q, want org-1/commit-1", pending.cleanupOrgID, pending.cleanupAttemptID)
		}
		if len(pending.internalBlockIDs) != 1 || pending.internalBlockIDs[0] != "sha256-1" {
			t.Fatalf("persist internalBlockIDs = %#v, want []string{\"sha256-1\"}", pending.internalBlockIDs)
		}
		return nil
	}

	resolveCalls := 0
	stagePendingPublishedFilesResolveFn = func(h *FSHelper, orgID, libraryID string, blockIDs []string) ([]string, error) {
		resolveCalls++
		if orgID != "org-1" {
			t.Fatalf("resolve orgID = %q, want org-1", orgID)
		}
		if libraryID != "repo-1" {
			t.Fatalf("resolve libraryID = %q, want repo-1", libraryID)
		}
		if len(blockIDs) != 1 || blockIDs[0] != "sha1-1" {
			t.Fatalf("resolve blockIDs = %#v, want []string{\"sha1-1\"}", blockIDs)
		}
		return []string{"sha256-1"}, nil
	}
	addCalls := 0
	stagePendingPublishedFilesAddReferencesFn = func(database *db.DB, orgID, repoID, attemptID string, blockIDs []string) error {
		addCalls++
		if orgID != "org-1" || repoID != "repo-1" || attemptID != "commit-1" {
			t.Fatalf("add args = %s/%s/%s, want org-1/repo-1/commit-1", orgID, repoID, attemptID)
		}
		if len(blockIDs) != 1 || blockIDs[0] != "sha256-1" {
			t.Fatalf("add blockIDs = %#v, want []string{\"sha256-1\"}", blockIDs)
		}
		return nil
	}
	stagePendingPublishedFilesRemoveReferencesFn = func(database *db.DB, orgID, attemptID string, blockIDs []string) error {
		t.Fatalf("remove should not run on successful stage, got %s/%s %#v", orgID, attemptID, blockIDs)
		return nil
	}

	pending := &pendingPublishedFile{fsID: "fs-1", externalBlockIDs: []string{"sha1-1"}}
	err := helper.stagePendingPublishedFiles("org-1", "repo-1", "commit-1", []*pendingPublishedFile{pending})
	if err != nil {
		t.Fatalf("stagePendingPublishedFiles() error = %v, want nil", err)
	}
	if resolveCalls != 1 {
		t.Fatalf("resolveCalls = %d, want 1", resolveCalls)
	}
	if addCalls != 1 {
		t.Fatalf("addCalls = %d, want 1", addCalls)
	}
	if persistCalls != 1 {
		t.Fatalf("persistCalls = %d, want 1", persistCalls)
	}
	if len(pending.internalBlockIDs) != 1 || pending.internalBlockIDs[0] != "sha256-1" {
		t.Fatalf("pending.internalBlockIDs = %#v, want []string{\"sha256-1\"}", pending.internalBlockIDs)
	}
}

func TestStagePendingPublishedFiles_ReturnsRollbackFailureAndKeepsResolvedIDs(t *testing.T) {
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
		if len(blockIDs) != 1 {
			t.Fatalf("resolve blockIDs len = %d, want 1", len(blockIDs))
		}
		return []string{"resolved-" + blockIDs[0]}, nil
	}
	stagePendingPublishedFilesPersistFn = func(h *FSHelper, repoID string, pending *pendingPublishedFile) error {
		return nil
	}
	addCalls := 0
	stageErr := errors.New("stage boom")
	stagePendingPublishedFilesAddReferencesFn = func(database *db.DB, orgID, repoID, attemptID string, blockIDs []string) error {
		addCalls++
		if addCalls == 2 {
			return stageErr
		}
		return nil
	}
	removeCalls := 0
	cleanupErr := errors.New("cleanup boom")
	stagePendingPublishedFilesRemoveReferencesFn = func(database *db.DB, orgID, attemptID string, blockIDs []string) error {
		removeCalls++
		if orgID != "org-1" || attemptID != "commit-1" {
			t.Fatalf("remove args = %s/%s, want org-1/commit-1", orgID, attemptID)
		}
		if len(blockIDs) != 2 || blockIDs[0] != "resolved-sha1-1" || blockIDs[1] != "resolved-sha1-2" {
			t.Fatalf("remove blockIDs = %#v, want both staged block IDs", blockIDs)
		}
		return cleanupErr
	}

	pending := []*pendingPublishedFile{
		{fsID: "fs-1", externalBlockIDs: []string{"sha1-1"}},
		{fsID: "fs-2", externalBlockIDs: []string{"sha1-2"}},
	}
	err := helper.stagePendingPublishedFiles("org-1", "repo-1", "commit-1", pending)
	if !errors.Is(err, stageErr) {
		t.Fatalf("stagePendingPublishedFiles() error = %v, want stage error %v", err, stageErr)
	}
	if !errors.Is(err, cleanupErr) {
		t.Fatalf("stagePendingPublishedFiles() error = %v, want cleanup error %v", err, cleanupErr)
	}
	if addCalls != 2 {
		t.Fatalf("addCalls = %d, want 2", addCalls)
	}
	if removeCalls != 1 {
		t.Fatalf("removeCalls = %d, want 1", removeCalls)
	}
	if len(pending[0].internalBlockIDs) != 1 || pending[0].internalBlockIDs[0] != "resolved-sha1-1" {
		t.Fatalf("pending[0].internalBlockIDs = %#v, want []string{\"resolved-sha1-1\"}", pending[0].internalBlockIDs)
	}
	if len(pending[1].internalBlockIDs) != 1 || pending[1].internalBlockIDs[0] != "resolved-sha1-2" {
		t.Fatalf("pending[1].internalBlockIDs = %#v, want []string{\"resolved-sha1-2\"}", pending[1].internalBlockIDs)
	}
}

func TestPrepareFileFSObjectForPublish_DefersPersistenceUntilStage(t *testing.T) {
	helper := &FSHelper{}
	pending, err := helper.prepareFileFSObjectForPublish("repo-1", "report.pdf", 123, []string{"sha1-1"})
	if err != nil {
		t.Fatalf("prepareFileFSObjectForPublish() error = %v, want nil", err)
	}
	if pending == nil {
		t.Fatal("prepareFileFSObjectForPublish() returned nil pending file")
	}
	if pending.name != "report.pdf" {
		t.Fatalf("pending.name = %q, want report.pdf", pending.name)
	}
	if pending.size != 123 {
		t.Fatalf("pending.size = %d, want 123", pending.size)
	}
	if pending.cleanupOrgID != "" || pending.cleanupAttemptID != "" {
		t.Fatalf("pending cleanup metadata = %q/%q, want empty before stage", pending.cleanupOrgID, pending.cleanupAttemptID)
	}
}

func TestLibraryHeadSnapshotValidateExpectedHeadRejectsMismatch(t *testing.T) {
	snapshot := &LibraryHeadSnapshot{HeadCommitID: "head-a"}

	if err := snapshot.ValidateExpectedHead("head-a"); err != nil {
		t.Fatalf("ValidateExpectedHead(same) error = %v, want nil", err)
	}

	if err := snapshot.ValidateExpectedHead("head-b"); err == nil {
		t.Fatal("ValidateExpectedHead(mismatch) error = nil, want mismatch error")
	}
}

func TestUpdateLibraryHeadFromSnapshotRejectsMismatchedExpectedHead(t *testing.T) {
	helper := &FSHelper{}
	snapshot := &LibraryHeadSnapshot{OrgID: "org-a", HeadCommitID: "head-a"}

	err := helper.UpdateLibraryHeadFromSnapshot(snapshot, "repo-a", "commit-a", "head-b")
	if err == nil {
		t.Fatal("UpdateLibraryHeadFromSnapshot(mismatch) error = nil, want mismatch error")
	}
}

func TestIsAmbiguousLibraryHeadUpdateError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "cas write unknown", err: gocql.RequestErrCASWriteUnknown{}, want: true},
		{name: "wrapped cas write unknown", err: fmt.Errorf("wrapped: %w", gocql.RequestErrCASWriteUnknown{}), want: true},
		{name: "no response timeout", err: gocql.ErrTimeoutNoResponse, want: true},
		{name: "connection closed", err: gocql.ErrConnectionClosed, want: true},
		{name: "generic", err: errors.New("boom"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isAmbiguousLibraryHeadUpdateError(tt.err); got != tt.want {
				t.Fatalf("isAmbiguousLibraryHeadUpdateError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestResolveLibraryHeadUpdateErrorTreatsConfirmedVisibleAmbiguousCASAsSuccess(t *testing.T) {
	err := resolveLibraryHeadUpdateError("repo-1", "commit-1", gocql.RequestErrCASWriteUnknown{}, func() (string, bool, error) {
		return "commit-1", true, nil
	})
	if err != nil {
		t.Fatalf("resolveLibraryHeadUpdateError() error = %v, want nil", err)
	}
}

func TestResolveLibraryHeadUpdateErrorReturnsFailureWhenConfirmedNotVisible(t *testing.T) {
	err := resolveLibraryHeadUpdateError("repo-1", "commit-1", gocql.RequestErrCASWriteUnknown{}, func() (string, bool, error) {
		return "head-old", false, nil
	})
	if err == nil {
		t.Fatal("resolveLibraryHeadUpdateError() error = nil, want failure")
	}
	if errors.Is(err, ErrLibraryHeadPublicationUnknown) {
		t.Fatalf("resolveLibraryHeadUpdateError() error = %v, did not want unknown-publication sentinel", err)
	}
	var casUnknown gocql.RequestErrCASWriteUnknown
	if !errors.As(err, &casUnknown) {
		t.Fatalf("resolveLibraryHeadUpdateError() error = %v, want wrapped CAS error", err)
	}
}

func TestResolveLibraryHeadUpdateErrorReturnsUnknownWhenConfirmationFails(t *testing.T) {
	confirmErr := errors.New("confirm boom")
	err := resolveLibraryHeadUpdateError("repo-1", "commit-1", gocql.ErrTimeoutNoResponse, func() (string, bool, error) {
		return "", false, confirmErr
	})
	if err == nil {
		t.Fatal("resolveLibraryHeadUpdateError() error = nil, want unknown outcome error")
	}
	if !errors.Is(err, ErrLibraryHeadPublicationUnknown) {
		t.Fatalf("resolveLibraryHeadUpdateError() error = %v, want ErrLibraryHeadPublicationUnknown", err)
	}
	if !errors.Is(err, gocql.ErrTimeoutNoResponse) {
		t.Fatalf("resolveLibraryHeadUpdateError() error = %v, want wrapped timeout error", err)
	}
	if !errors.Is(err, confirmErr) {
		t.Fatalf("resolveLibraryHeadUpdateError() error = %v, want wrapped confirmation error", err)
	}
}

func TestRetryLibraryHeadMutationRetriesConflicts(t *testing.T) {
	previousDelay := libraryHeadMutationRetryDelay
	libraryHeadMutationRetryDelay = 0
	defer func() {
		libraryHeadMutationRetryDelay = previousDelay
	}()

	attempts := 0
	err := retryLibraryHeadMutation("test", func() error {
		attempts++
		if attempts < 3 {
			return ErrLibraryHeadConflict
		}
		return nil
	})
	if err != nil {
		t.Fatalf("retryLibraryHeadMutation() error = %v, want nil", err)
	}
	if attempts != 3 {
		t.Fatalf("retryLibraryHeadMutation() attempts = %d, want 3", attempts)
	}
}

func TestRetryLibraryHeadMutationStopsOnNonConflict(t *testing.T) {
	previousDelay := libraryHeadMutationRetryDelay
	libraryHeadMutationRetryDelay = 0
	defer func() {
		libraryHeadMutationRetryDelay = previousDelay
	}()

	wantErr := errors.New("boom")
	attempts := 0
	err := retryLibraryHeadMutation("test", func() error {
		attempts++
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("retryLibraryHeadMutation() error = %v, want %v", err, wantErr)
	}
	if attempts != 1 {
		t.Fatalf("retryLibraryHeadMutation() attempts = %d, want 1", attempts)
	}
}

func TestLibraryHeadMutationRetryBackoffCaps(t *testing.T) {
	previousDelay := libraryHeadMutationRetryDelay
	previousMaxDelay := libraryHeadMutationRetryMaxDelay
	previousJitter := libraryHeadMutationRetryJitter
	libraryHeadMutationRetryDelay = 50 * time.Millisecond
	libraryHeadMutationRetryMaxDelay = 125 * time.Millisecond
	libraryHeadMutationRetryJitter = 0
	defer func() {
		libraryHeadMutationRetryDelay = previousDelay
		libraryHeadMutationRetryMaxDelay = previousMaxDelay
		libraryHeadMutationRetryJitter = previousJitter
	}()

	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{attempt: 0, want: 0},
		{attempt: 1, want: 50 * time.Millisecond},
		{attempt: 2, want: 100 * time.Millisecond},
		{attempt: 3, want: 125 * time.Millisecond},
		{attempt: 4, want: 125 * time.Millisecond},
	}

	for _, tt := range tests {
		if got := libraryHeadMutationRetryBackoff(tt.attempt); got != tt.want {
			t.Fatalf("libraryHeadMutationRetryBackoff(%d) = %s, want %s", tt.attempt, got, tt.want)
		}
	}
}

func TestLibraryHeadMutationRetryBackoffAddsDeterministicJitter(t *testing.T) {
	previousDelay := libraryHeadMutationRetryDelay
	previousMaxDelay := libraryHeadMutationRetryMaxDelay
	previousJitter := libraryHeadMutationRetryJitter
	previousInt63n := libraryHeadMutationRetryJitterInt63n
	libraryHeadMutationRetryDelay = 50 * time.Millisecond
	libraryHeadMutationRetryMaxDelay = 0
	libraryHeadMutationRetryJitter = 25 * time.Millisecond
	libraryHeadMutationRetryJitterInt63n = func(limit int64) int64 {
		wantLimit := int64(25 * time.Millisecond)
		if limit != wantLimit {
			t.Fatalf("jitter limit = %d, want %d", limit, wantLimit)
		}
		return int64(7 * time.Millisecond)
	}
	defer func() {
		libraryHeadMutationRetryDelay = previousDelay
		libraryHeadMutationRetryMaxDelay = previousMaxDelay
		libraryHeadMutationRetryJitter = previousJitter
		libraryHeadMutationRetryJitterInt63n = previousInt63n
	}()

	got := libraryHeadMutationRetryBackoff(2)
	want := 107 * time.Millisecond
	if got != want {
		t.Fatalf("libraryHeadMutationRetryBackoff(2) = %s, want %s", got, want)
	}
}

// Test normalizePath function (additional cases)
func TestNormalizePath_Additional(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"empty path", "", "/"},
		{"root path", "/", "/"},
		{"simple path", "/foo", "/foo"},
		{"path without leading slash", "foo", "/foo"},
		{"path with trailing slash", "/foo/", "/foo"},
		{"nested path", "/foo/bar/baz", "/foo/bar/baz"},
		{"nested path without leading slash", "foo/bar/baz", "/foo/bar/baz"},
		{"double slashes cleaned", "/foo//bar", "/foo/bar"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizePath(tt.input)
			if result != tt.expected {
				t.Errorf("normalizePath(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// Test RemoveEntryFromList function
func TestRemoveEntryFromList(t *testing.T) {
	entries := []FSEntry{
		{Name: "file1.txt", ID: "id1", Mode: ModeFile, Size: 100},
		{Name: "file2.txt", ID: "id2", Mode: ModeFile, Size: 200},
		{Name: "dir1", ID: "id3", Mode: ModeDir},
	}

	tests := []struct {
		name       string
		entries    []FSEntry
		removeName string
		wantLen    int
	}{
		{"remove first", entries, "file1.txt", 2},
		{"remove middle", entries, "file2.txt", 2},
		{"remove last", entries, "dir1", 2},
		{"remove non-existent", entries, "notfound.txt", 3},
		{"remove from empty", []FSEntry{}, "file.txt", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := RemoveEntryFromList(tt.entries, tt.removeName)
			if len(result) != tt.wantLen {
				t.Errorf("RemoveEntryFromList() len = %d, want %d", len(result), tt.wantLen)
			}
			// Verify the removed entry is not in result
			for _, entry := range result {
				if entry.Name == tt.removeName {
					t.Errorf("RemoveEntryFromList() still contains %q", tt.removeName)
				}
			}
		})
	}
}

// Test FindEntryInList function
func TestFindEntryInList(t *testing.T) {
	entries := []FSEntry{
		{Name: "file1.txt", ID: "id1", Mode: ModeFile, Size: 100},
		{Name: "file2.txt", ID: "id2", Mode: ModeFile, Size: 200},
		{Name: "dir1", ID: "id3", Mode: ModeDir},
	}

	tests := []struct {
		name     string
		entries  []FSEntry
		findName string
		wantNil  bool
		wantID   string
	}{
		{"find first", entries, "file1.txt", false, "id1"},
		{"find middle", entries, "file2.txt", false, "id2"},
		{"find last", entries, "dir1", false, "id3"},
		{"find non-existent", entries, "notfound.txt", true, ""},
		{"find in empty", []FSEntry{}, "file.txt", true, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FindEntryInList(tt.entries, tt.findName)
			if tt.wantNil {
				if result != nil {
					t.Errorf("FindEntryInList() = %v, want nil", result)
				}
			} else {
				if result == nil {
					t.Error("FindEntryInList() = nil, want entry")
				} else if result.ID != tt.wantID {
					t.Errorf("FindEntryInList().ID = %q, want %q", result.ID, tt.wantID)
				}
			}
		})
	}
}

// Test UpdateEntryInList function
func TestUpdateEntryInList(t *testing.T) {
	entries := []FSEntry{
		{Name: "file1.txt", ID: "id1", Mode: ModeFile, Size: 100},
		{Name: "file2.txt", ID: "id2", Mode: ModeFile, Size: 200},
	}

	tests := []struct {
		name    string
		oldName string
		newName string
	}{
		{"rename first", "file1.txt", "renamed1.txt"},
		{"rename second", "file2.txt", "renamed2.txt"},
		{"rename non-existent", "notfound.txt", "new.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := UpdateEntryInList(entries, tt.oldName, tt.newName)
			if len(result) != len(entries) {
				t.Errorf("UpdateEntryInList() changed length from %d to %d", len(entries), len(result))
			}
			// Check if rename happened
			foundOld := false
			foundNew := false
			for _, entry := range result {
				if entry.Name == tt.oldName {
					foundOld = true
				}
				if entry.Name == tt.newName {
					foundNew = true
				}
			}
			// If old name existed, it should be renamed
			oldExists := false
			for _, entry := range entries {
				if entry.Name == tt.oldName {
					oldExists = true
					break
				}
			}
			if oldExists {
				if foundOld {
					t.Errorf("UpdateEntryInList() old name %q still exists", tt.oldName)
				}
				if !foundNew {
					t.Errorf("UpdateEntryInList() new name %q not found", tt.newName)
				}
			}
		})
	}
}

// copiedFileEntry must carry the source's content identity across copy/move: the new
// fs_id and name change, but Mode/Size/MTime/Modifier are preserved so file/detail stays
// consistent and never falls back to crediting the copier. This is the regression guard
// for the cross-library copy that previously dropped Modifier and reset MTime to now.
func TestCopiedFileEntry(t *testing.T) {
	src := FSEntry{
		Name:     "report.docx",
		ID:       "src-fsid",
		Mode:     ModeFile,
		MTime:    1000,
		Size:     2048,
		Modifier: "alice-uid@sesamefs.local",
	}

	got := copiedFileEntry(src, "new-fsid", "report (1).docx")

	if got.ID != "new-fsid" {
		t.Errorf("ID = %q, want %q (must take the new fs_id)", got.ID, "new-fsid")
	}
	if got.Name != "report (1).docx" {
		t.Errorf("Name = %q, want %q (must take the new name)", got.Name, "report (1).docx")
	}
	if got.Modifier != src.Modifier {
		t.Errorf("Modifier = %q, want %q (must preserve source modifier)", got.Modifier, src.Modifier)
	}
	if got.MTime != src.MTime {
		t.Errorf("MTime = %d, want %d (must preserve source mtime, not reset to now)", got.MTime, src.MTime)
	}
	if got.Mode != src.Mode {
		t.Errorf("Mode = %d, want %d", got.Mode, src.Mode)
	}
	if got.Size != src.Size {
		t.Errorf("Size = %d, want %d", got.Size, src.Size)
	}

	// Same-repo copy keeps the source fs_id but must still preserve mtime/modifier
	// rather than treating the copy operation as a content edit.
	sameRepo := copiedFileEntry(src, src.ID, "report copy.docx")
	if sameRepo.ID != src.ID {
		t.Errorf("same-repo ID = %q, want %q", sameRepo.ID, src.ID)
	}
	if sameRepo.MTime != src.MTime {
		t.Errorf("same-repo MTime = %d, want %d", sameRepo.MTime, src.MTime)
	}
	if sameRepo.Modifier != src.Modifier {
		t.Errorf("same-repo Modifier = %q, want %q", sameRepo.Modifier, src.Modifier)
	}

	// An entry with no stamped modifier copies as empty (it will fall back to blame at
	// read time); the copy itself must not invent an identity.
	legacy := FSEntry{Name: "old.txt", ID: "x", Mode: ModeFile, MTime: 5, Size: 1}
	if e := copiedFileEntry(legacy, "y", "old.txt"); e.Modifier != "" {
		t.Errorf("Modifier = %q, want empty for an unstamped source", e.Modifier)
	}
}

// Test AddEntryToList function
func TestAddEntryToList(t *testing.T) {
	entries := []FSEntry{
		{Name: "file1.txt", ID: "id1", Mode: ModeFile, Size: 100},
	}

	newEntry := FSEntry{Name: "file2.txt", ID: "id2", Mode: ModeFile, Size: 200}

	result := AddEntryToList(entries, newEntry)

	if len(result) != 2 {
		t.Errorf("AddEntryToList() len = %d, want 2", len(result))
	}

	found := false
	for _, entry := range result {
		if entry.Name == "file2.txt" {
			found = true
			if entry.ID != "id2" {
				t.Errorf("AddEntryToList() added entry ID = %q, want %q", entry.ID, "id2")
			}
		}
	}
	if !found {
		t.Error("AddEntryToList() did not add new entry")
	}
}

// Test AddEntryToList with empty list
func TestAddEntryToList_Empty(t *testing.T) {
	entries := []FSEntry{}
	newEntry := FSEntry{Name: "file1.txt", ID: "id1", Mode: ModeFile, Size: 100}

	result := AddEntryToList(entries, newEntry)

	if len(result) != 1 {
		t.Errorf("AddEntryToList() len = %d, want 1", len(result))
	}
	if result[0].Name != "file1.txt" {
		t.Errorf("AddEntryToList()[0].Name = %q, want %q", result[0].Name, "file1.txt")
	}
}

// Test FSEntry mode constants
func TestFSEntryModeConstants(t *testing.T) {
	// Verify ModeDir and ModeFile are distinct
	if ModeDir == ModeFile {
		t.Error("ModeDir and ModeFile should be different")
	}

	// Test mode detection
	dirEntry := FSEntry{Name: "dir", Mode: ModeDir}
	fileEntry := FSEntry{Name: "file", Mode: ModeFile}

	if dirEntry.Mode != ModeDir {
		t.Errorf("dirEntry.Mode = %d, want %d (ModeDir)", dirEntry.Mode, ModeDir)
	}
	if fileEntry.Mode != ModeFile {
		t.Errorf("fileEntry.Mode = %d, want %d (ModeFile)", fileEntry.Mode, ModeFile)
	}
}

// Test PathTraverseResult struct
func TestPathTraverseResult_Struct(t *testing.T) {
	result := &PathTraverseResult{
		TargetFSID: "target123",
		TargetEntry: &FSEntry{
			Name: "file.txt",
			ID:   "entry123",
			Mode: ModeFile,
			Size: 1024,
		},
		ParentFSID:   "parent123",
		ParentPath:   "/path/to",
		Ancestors:    []string{"root", "path", "to"},
		AncestorPath: []string{"/", "/path", "/path/to"},
		Entries: []FSEntry{
			{Name: "file.txt", ID: "entry123"},
			{Name: "other.txt", ID: "other123"},
		},
	}

	if result.TargetFSID != "target123" {
		t.Errorf("TargetFSID = %q, want %q", result.TargetFSID, "target123")
	}
	if result.TargetEntry == nil {
		t.Error("TargetEntry should not be nil")
	}
	if result.TargetEntry.Name != "file.txt" {
		t.Errorf("TargetEntry.Name = %q, want %q", result.TargetEntry.Name, "file.txt")
	}
	if result.ParentFSID != "parent123" {
		t.Errorf("ParentFSID = %q, want %q", result.ParentFSID, "parent123")
	}
	if result.ParentPath != "/path/to" {
		t.Errorf("ParentPath = %q, want %q", result.ParentPath, "/path/to")
	}
	if len(result.Ancestors) != 3 {
		t.Errorf("len(Ancestors) = %d, want 3", len(result.Ancestors))
	}
	if len(result.Entries) != 2 {
		t.Errorf("len(Entries) = %d, want 2", len(result.Entries))
	}
}

// =============================================================================
// RebuildPathToRoot Algorithm Tests
//
// These tests verify the correctness of the rebuild algorithm logic without
// requiring a database. They simulate what RebuildPathToRoot does by computing
// the expected ancestor walking pattern (currentName, loop iterations) for
// various directory depths.
// =============================================================================

// TestRebuildPathToRoot_AlgorithmLogic_EmptyAncestors tests that empty ancestors
// causes an early return (parent was root — no rebuild needed).
func TestRebuildPathToRoot_AlgorithmLogic_EmptyAncestors(t *testing.T) {
	result := &PathTraverseResult{
		Ancestors:    []string{},
		AncestorPath: []string{},
	}

	// With empty ancestors, RebuildPathToRoot returns newParentFSID unchanged
	if len(result.Ancestors) != 0 {
		t.Error("Expected empty ancestors for root traversal")
	}
}

// TestRebuildPathToRoot_AlgorithmLogic_SingleAncestor tests the case where
// Ancestors = [rootFSID]. This means the modified directory is a direct child
// of root. The algorithm should update root's entries (loop runs for root).
func TestRebuildPathToRoot_AlgorithmLogic_SingleAncestor(t *testing.T) {
	// TraverseToPath("/folder") returns Ancestors = [rootFSID]
	result := &PathTraverseResult{
		TargetFSID:   "folder_fsid",
		ParentFSID:   "root_fsid",
		ParentPath:   "/",
		Ancestors:    []string{"root_fsid"},
		AncestorPath: []string{"/"},
	}

	// currentName = path.Base(AncestorPath[len-1]) = path.Base("/") = "/"
	currentName := path.Base(result.AncestorPath[len(result.AncestorPath)-1])

	// With 1 ancestor, loop runs from len-2 = -1, so loop body NEVER executes.
	// This means RebuildPathToRoot returns newParentFSID unchanged.
	loopIterations := 0
	for i := len(result.Ancestors) - 2; i >= 0; i-- {
		loopIterations++
	}

	if loopIterations != 0 {
		t.Errorf("Expected 0 loop iterations for single ancestor, got %d", loopIterations)
	}

	// With single ancestor [root], the modified directory's parent IS root.
	// After CreateDirectory updates root's entries to include the new child,
	// newGrandparentFSID = new root. RebuildPathToRoot returns it unchanged.
	// This is correct because the caller already created the new root.
	_ = currentName
}

// TestRebuildPathToRoot_AlgorithmLogic_TwoAncestors verifies the algorithm
// for Ancestors = [root, folderA]. This means the modified directory is a
// grandchild of root (e.g., /folderA/folderB was modified).
func TestRebuildPathToRoot_AlgorithmLogic_TwoAncestors(t *testing.T) {
	// TraverseToPath("/folderA/folderB") returns:
	result := &PathTraverseResult{
		TargetFSID:   "folderB_fsid",
		ParentFSID:   "folderA_fsid",
		ParentPath:   "/folderA",
		Ancestors:    []string{"root_fsid", "folderA_fsid"},
		AncestorPath: []string{"/", "/folderA"},
	}

	// currentName = path.Base("/folderA") = "folderA"
	currentName := path.Base(result.AncestorPath[len(result.AncestorPath)-1])
	if currentName != "folderA" {
		t.Errorf("currentName = %q, want %q", currentName, "folderA")
	}

	// Loop runs for i = 0 (root)
	type iteration struct {
		ancestorFSID string
		currentName  string
	}
	var iterations []iteration
	for i := len(result.Ancestors) - 2; i >= 0; i-- {
		iterations = append(iterations, iteration{
			ancestorFSID: result.Ancestors[i],
			currentName:  currentName,
		})
		if i > 0 {
			currentName = path.Base(result.AncestorPath[i])
		}
	}

	if len(iterations) != 1 {
		t.Fatalf("Expected 1 loop iteration, got %d", len(iterations))
	}

	// The single iteration should process root, looking for "folderA" in root's entries
	if iterations[0].ancestorFSID != "root_fsid" {
		t.Errorf("iteration[0].ancestorFSID = %q, want %q", iterations[0].ancestorFSID, "root_fsid")
	}
	if iterations[0].currentName != "folderA" {
		t.Errorf("iteration[0].currentName = %q, want %q", iterations[0].currentName, "folderA")
	}
}

// TestRebuildPathToRoot_AlgorithmLogic_ThreeAncestors verifies depth-3 rebuild.
// Ancestors = [root, a, b]. Modified directory is at /a/b/c.
func TestRebuildPathToRoot_AlgorithmLogic_ThreeAncestors(t *testing.T) {
	result := &PathTraverseResult{
		TargetFSID:   "c_fsid",
		ParentFSID:   "b_fsid",
		ParentPath:   "/a/b",
		Ancestors:    []string{"root_fsid", "a_fsid", "b_fsid"},
		AncestorPath: []string{"/", "/a", "/a/b"},
	}

	// currentName starts as path.Base("/a/b") = "b"
	currentName := path.Base(result.AncestorPath[len(result.AncestorPath)-1])
	if currentName != "b" {
		t.Errorf("Initial currentName = %q, want %q", currentName, "b")
	}

	type iteration struct {
		ancestorFSID string
		currentName  string
	}
	var iterations []iteration
	for i := len(result.Ancestors) - 2; i >= 0; i-- {
		iterations = append(iterations, iteration{
			ancestorFSID: result.Ancestors[i],
			currentName:  currentName,
		})
		if i > 0 {
			currentName = path.Base(result.AncestorPath[i])
		}
	}

	if len(iterations) != 2 {
		t.Fatalf("Expected 2 loop iterations, got %d", len(iterations))
	}

	// Iteration 1: Process /a, find "b" in a's entries, update to new_b
	if iterations[0].ancestorFSID != "a_fsid" {
		t.Errorf("iteration[0].ancestorFSID = %q, want %q", iterations[0].ancestorFSID, "a_fsid")
	}
	if iterations[0].currentName != "b" {
		t.Errorf("iteration[0].currentName = %q, want %q", iterations[0].currentName, "b")
	}

	// Iteration 2: Process root, find "a" in root's entries, update to new_a
	if iterations[1].ancestorFSID != "root_fsid" {
		t.Errorf("iteration[1].ancestorFSID = %q, want %q", iterations[1].ancestorFSID, "root_fsid")
	}
	if iterations[1].currentName != "a" {
		t.Errorf("iteration[1].currentName = %q, want %q", iterations[1].currentName, "a")
	}
}

// TestRebuildPathToRoot_AlgorithmLogic_FiveAncestors verifies deep rebuild.
// Ancestors = [root, d1, d2, d3, d4]. Modified directory at /d1/d2/d3/d4/d5.
func TestRebuildPathToRoot_AlgorithmLogic_FiveAncestors(t *testing.T) {
	result := &PathTraverseResult{
		TargetFSID: "d5_fsid",
		ParentFSID: "d4_fsid",
		ParentPath: "/d1/d2/d3/d4",
		Ancestors:  []string{"root_fsid", "d1_fsid", "d2_fsid", "d3_fsid", "d4_fsid"},
		AncestorPath: []string{
			"/", "/d1", "/d1/d2", "/d1/d2/d3", "/d1/d2/d3/d4",
		},
	}

	currentName := path.Base(result.AncestorPath[len(result.AncestorPath)-1])
	if currentName != "d4" {
		t.Errorf("Initial currentName = %q, want %q", currentName, "d4")
	}

	type iteration struct {
		index        int
		ancestorFSID string
		currentName  string
	}
	var iterations []iteration
	for i := len(result.Ancestors) - 2; i >= 0; i-- {
		iterations = append(iterations, iteration{
			index:        i,
			ancestorFSID: result.Ancestors[i],
			currentName:  currentName,
		})
		if i > 0 {
			currentName = path.Base(result.AncestorPath[i])
		}
	}

	// Should produce 4 iterations: d3→d2→d1→root
	expected := []struct {
		ancestorFSID string
		currentName  string
	}{
		{"d3_fsid", "d4"},   // In d3's entries, update "d4" to new_d4
		{"d2_fsid", "d3"},   // In d2's entries, update "d3" to new_d3
		{"d1_fsid", "d2"},   // In d1's entries, update "d2" to new_d2
		{"root_fsid", "d1"}, // In root's entries, update "d1" to new_d1
	}

	if len(iterations) != len(expected) {
		t.Fatalf("Expected %d loop iterations, got %d", len(expected), len(iterations))
	}

	for i, exp := range expected {
		if iterations[i].ancestorFSID != exp.ancestorFSID {
			t.Errorf("iteration[%d].ancestorFSID = %q, want %q", i, iterations[i].ancestorFSID, exp.ancestorFSID)
		}
		if iterations[i].currentName != exp.currentName {
			t.Errorf("iteration[%d].currentName = %q, want %q", i, iterations[i].currentName, exp.currentName)
		}
	}
}

// TestRebuildPathToRoot_CreateDirectory_Pattern verifies the correct calling
// pattern for CreateDirectory at various depths. This is the pattern that was
// buggy before the fix: for depth 3+, the old code re-traversed instead of
// using the original result with RebuildPathToRoot.
func TestRebuildPathToRoot_CreateDirectory_Pattern(t *testing.T) {
	tests := []struct {
		name           string
		parentPath     string // Path of directory being created's parent
		ancestorCount  int    // Expected number of ancestors from TraverseToPath(parentPath)
		loopIterations int    // Expected RebuildPathToRoot loop iterations
		description    string
	}{
		{
			name:           "depth 1: create /newdir",
			parentPath:     "/",
			ancestorCount:  0, // TraverseToPath("/") has empty ancestors
			loopIterations: 0, // Early return: parent is root
			description:    "parentPath=/ → no rebuild needed, newParentFSID IS new root",
		},
		{
			name:           "depth 2: create /folder/newdir",
			parentPath:     "/folder",
			ancestorCount:  1, // [root]
			loopIterations: 0, // Single ancestor, loop starts at -1
			description:    "grandparent is root → grandparentFSID IS new root after update",
		},
		{
			name:           "depth 3: create /a/b/newdir",
			parentPath:     "/a/b",
			ancestorCount:  2, // [root, a]
			loopIterations: 1, // Process root: update 'a' → new root
			description:    "BUG WAS HERE: old code re-traversed and broke root_fs_id",
		},
		{
			name:           "depth 4: create /a/b/c/newdir",
			parentPath:     "/a/b/c",
			ancestorCount:  3, // [root, a, b]
			loopIterations: 2, // Process a then root
			description:    "two ancestor updates needed",
		},
		{
			name:           "depth 6: create /a/b/c/d/e/newdir",
			parentPath:     "/a/b/c/d/e",
			ancestorCount:  5, // [root, a, b, c, d]
			loopIterations: 4, // Process c→b→a→root
			description:    "deep nesting requires walking all ancestors",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the traversal result's ancestor count
			loopCount := 0
			if tt.ancestorCount >= 2 {
				for i := tt.ancestorCount - 2; i >= 0; i-- {
					loopCount++
				}
			}

			if loopCount != tt.loopIterations {
				t.Errorf("Loop iterations for %d ancestors = %d, want %d (%s)",
					tt.ancestorCount, loopCount, tt.loopIterations, tt.description)
			}
		})
	}
}

func TestRebuildPathToRootRejectsNilResult(t *testing.T) {
	_, err := rebuildPathToRootWithHooks("repo", nil, "new-root",
		func(string, string) ([]FSEntry, error) { return nil, nil },
		func(string, []FSEntry) (string, error) { return "", nil },
	)
	if err == nil {
		t.Fatal("rebuildPathToRootWithHooks(nil) error = nil, want error")
	}
}

func TestRebuildPathToRootRejectsMismatchedAncestorLengths(t *testing.T) {
	result := &PathTraverseResult{
		Ancestors:    []string{"root", "folder"},
		AncestorPath: []string{"/"},
	}

	_, err := rebuildPathToRootWithHooks("repo", result, "new-root",
		func(string, string) ([]FSEntry, error) { return nil, nil },
		func(string, []FSEntry) (string, error) { return "", nil },
	)
	if err == nil {
		t.Fatal("rebuildPathToRootWithHooks(mismatched lengths) error = nil, want error")
	}
}

func TestRebuildPathToRootRejectsMissingChildInAncestor(t *testing.T) {
	result := &PathTraverseResult{
		Ancestors:    []string{"root_fsid", "a_fsid", "b_fsid"},
		AncestorPath: []string{"/", "/a", "/a/b"},
	}

	createCalls := 0
	_, err := rebuildPathToRootWithHooks("repo", result, "new-b",
		func(_ string, fsID string) ([]FSEntry, error) {
			switch fsID {
			case "a_fsid":
				return []FSEntry{{Name: "other-child", ID: "old"}}, nil
			case "root_fsid":
				return []FSEntry{{Name: "a", ID: "old-a"}}, nil
			default:
				return nil, nil
			}
		},
		func(string, []FSEntry) (string, error) {
			createCalls++
			return "should-not-happen", nil
		},
	)
	if err == nil {
		t.Fatal("rebuildPathToRootWithHooks(missing child) error = nil, want error")
	}
	if createCalls != 0 {
		t.Fatalf("createDirectoryFSObject called %d times, want 0", createCalls)
	}
}

func TestRebuildPathToRootRebuildsAncestorsWithHooks(t *testing.T) {
	result := &PathTraverseResult{
		Ancestors:    []string{"root_fsid", "a_fsid", "b_fsid"},
		AncestorPath: []string{"/", "/a", "/a/b"},
	}

	var created [][]FSEntry
	newRootFSID, err := rebuildPathToRootWithHooks("repo", result, "new-b",
		func(_ string, fsID string) ([]FSEntry, error) {
			switch fsID {
			case "a_fsid":
				return []FSEntry{{Name: "b", ID: "old-b"}, {Name: "peer", ID: "peer-id"}}, nil
			case "root_fsid":
				return []FSEntry{{Name: "a", ID: "old-a"}, {Name: "top", ID: "top-id"}}, nil
			default:
				return nil, nil
			}
		},
		func(_ string, entries []FSEntry) (string, error) {
			copied := append([]FSEntry(nil), entries...)
			created = append(created, copied)
			switch len(created) {
			case 1:
				return "new-a", nil
			case 2:
				return "new-root", nil
			default:
				return "unexpected", nil
			}
		},
	)
	if err != nil {
		t.Fatalf("rebuildPathToRootWithHooks() error = %v, want nil", err)
	}
	if newRootFSID != "new-root" {
		t.Fatalf("new root fsid = %q, want %q", newRootFSID, "new-root")
	}
	if len(created) != 2 {
		t.Fatalf("created ancestors = %d, want 2", len(created))
	}
	if created[0][0].Name != "b" || created[0][0].ID != "new-b" {
		t.Fatalf("first rebuild updated child = %#v, want name=b id=new-b", created[0][0])
	}
	if created[1][0].Name != "a" || created[1][0].ID != "new-a" {
		t.Fatalf("second rebuild updated child = %#v, want name=a id=new-a", created[1][0])
	}
}

// TestTraverseToPath_AncestorStructure verifies the expected ancestor structure
// for various path depths. This is critical for RebuildPathToRoot to work.
func TestTraverseToPath_AncestorStructure(t *testing.T) {
	tests := []struct {
		name             string
		targetPath       string
		expectedParts    int      // Number of path parts
		expectedAncCount int      // Expected ancestors count
		expectedAncPaths []string // Expected ancestor paths
	}{
		{
			name:             "root",
			targetPath:       "/",
			expectedParts:    0,
			expectedAncCount: 0,
			expectedAncPaths: []string{},
		},
		{
			name:             "depth 1: /folder",
			targetPath:       "/folder",
			expectedParts:    1,
			expectedAncCount: 1, // [root]
			expectedAncPaths: []string{"/"},
		},
		{
			name:             "depth 2: /a/b",
			targetPath:       "/a/b",
			expectedParts:    2,
			expectedAncCount: 2, // [root, a]
			expectedAncPaths: []string{"/", "/a"},
		},
		{
			name:             "depth 3: /a/b/c",
			targetPath:       "/a/b/c",
			expectedParts:    3,
			expectedAncCount: 3, // [root, a, b]
			expectedAncPaths: []string{"/", "/a", "/a/b"},
		},
		{
			name:             "depth 5: /a/b/c/d/e",
			targetPath:       "/a/b/c/d/e",
			expectedParts:    5,
			expectedAncCount: 5, // [root, a, b, c, d]
			expectedAncPaths: []string{"/", "/a", "/a/b", "/a/b/c", "/a/b/c/d"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			normalized := normalizePath(tt.targetPath)

			if normalized == "/" {
				if tt.expectedAncCount != 0 {
					t.Errorf("Root path should have 0 ancestors, expected %d", tt.expectedAncCount)
				}
				return
			}

			parts := splitPathParts(normalized)
			if len(parts) != tt.expectedParts {
				t.Errorf("splitPathParts(%q) = %d parts, want %d", normalized, len(parts), tt.expectedParts)
			}

			// Simulate ancestor building (loop runs for all but last part)
			ancestors := []string{"root_fsid"} // Always starts with root
			ancestorPath := []string{"/"}
			currentPath := "/"

			for i := 0; i < len(parts)-1; i++ {
				if currentPath == "/" {
					currentPath = "/" + parts[i]
				} else {
					currentPath = currentPath + "/" + parts[i]
				}
				ancestors = append(ancestors, parts[i]+"_fsid")
				ancestorPath = append(ancestorPath, currentPath)
			}

			if len(ancestors) != tt.expectedAncCount {
				t.Errorf("Ancestors count = %d, want %d", len(ancestors), tt.expectedAncCount)
			}

			if len(ancestorPath) != len(tt.expectedAncPaths) {
				t.Errorf("AncestorPath count = %d, want %d", len(ancestorPath), len(tt.expectedAncPaths))
			} else {
				for i, exp := range tt.expectedAncPaths {
					if ancestorPath[i] != exp {
						t.Errorf("AncestorPath[%d] = %q, want %q", i, ancestorPath[i], exp)
					}
				}
			}
		})
	}
}

// splitPathParts is a test helper that splits a normalized path into parts
func splitPathParts(p string) []string {
	if p == "/" {
		return nil
	}
	trimmed := p
	if len(trimmed) > 0 && trimmed[0] == '/' {
		trimmed = trimmed[1:]
	}
	if len(trimmed) > 0 && trimmed[len(trimmed)-1] == '/' {
		trimmed = trimmed[:len(trimmed)-1]
	}
	if trimmed == "" {
		return nil
	}
	result := []string{}
	start := 0
	for i := 0; i <= len(trimmed); i++ {
		if i == len(trimmed) || trimmed[i] == '/' {
			if i > start {
				result = append(result, trimmed[start:i])
			}
			start = i + 1
		}
	}
	return result
}

func TestSeafileFSObjectBlockIDs(t *testing.T) {
	sha256IDs := []string{strings.Repeat("a", 64), strings.Repeat("b", 64)}
	sha1IDs := []string{strings.Repeat("c", 40), strings.Repeat("d", 40)}

	t.Run("prefers validated SHA-1 column when present", func(t *testing.T) {
		got, ok := seafileFSObjectBlockIDs(sha256IDs, sha1IDs)
		if !ok || len(got) != 2 || got[0] != sha1IDs[0] || got[1] != sha1IDs[1] {
			t.Fatalf("got %v ok=%v, want %v", got, ok, sha1IDs)
		}
	})

	t.Run("falls back to legacy SHA-1 block_ids", func(t *testing.T) {
		got, ok := seafileFSObjectBlockIDs(sha1IDs, nil)
		if !ok || len(got) != 2 || got[0] != sha1IDs[0] || got[1] != sha1IDs[1] {
			t.Fatalf("got %v ok=%v, want fallback %v", got, ok, sha1IDs)
		}
	})

	t.Run("fails closed on length mismatch or invalid SHA-1 content", func(t *testing.T) {
		if got, ok := seafileFSObjectBlockIDs(sha256IDs, sha1IDs[:1]); ok || got != nil {
			t.Fatalf("got %v ok=%v, want nil+false on length mismatch", got, ok)
		}
		if got, ok := seafileFSObjectBlockIDs(nil, sha1IDs[:1]); ok || got != nil {
			t.Fatalf("got %v ok=%v, want nil+false when SHA-1 column is non-empty but block_ids is empty", got, ok)
		}
		if got, ok := seafileFSObjectBlockIDs(sha256IDs[:1], []string{"not-a-sha1"}); ok || got != nil {
			t.Fatalf("got %v ok=%v, want nil+false on invalid SHA-1", got, ok)
		}
		if got, ok := seafileFSObjectBlockIDs(sha256IDs[:1], []string{sha256IDs[0]}); ok || got != nil {
			t.Fatalf("got %v ok=%v, want nil+false on SHA-256 in SHA-1 column", got, ok)
		}
	})
}

func TestValidateCanonicalFSObjectBlockIDs(t *testing.T) {
	validInternal := []string{strings.Repeat("a", 64)}
	validSeafile := []string{strings.Repeat("b", 40)}

	if err := validateCanonicalFSObjectBlockIDs(validInternal, validSeafile); err != nil {
		t.Fatalf("validateCanonicalFSObjectBlockIDs(valid) error = %v, want nil", err)
	}
	if err := validateCanonicalFSObjectBlockIDs(nil, nil); err != nil {
		t.Fatalf("validateCanonicalFSObjectBlockIDs(empty) error = %v, want nil", err)
	}

	tests := []struct {
		name     string
		internal []string
		seafile  []string
	}{
		{name: "length mismatch", internal: []string{strings.Repeat("a", 64)}, seafile: nil},
		{name: "invalid internal", internal: []string{"bad"}, seafile: validSeafile},
		{name: "invalid seafile", internal: validInternal, seafile: []string{"bad"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := validateCanonicalFSObjectBlockIDs(tt.internal, tt.seafile); err == nil {
				t.Fatal("validateCanonicalFSObjectBlockIDs() error = nil, want non-nil")
			}
		})
	}
}
