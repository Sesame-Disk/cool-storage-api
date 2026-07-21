package v2

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
)

func TestRetryUploadedBlockMaterializationRetriesStoreFence(t *testing.T) {
	oldDelay := libraryHeadMutationRetryDelay
	oldMaxDelay := libraryHeadMutationRetryMaxDelay
	oldJitter := libraryHeadMutationRetryJitter
	oldSleep := registerUploadedBlockSleepFn
	t.Cleanup(func() {
		libraryHeadMutationRetryDelay = oldDelay
		libraryHeadMutationRetryMaxDelay = oldMaxDelay
		libraryHeadMutationRetryJitter = oldJitter
		registerUploadedBlockSleepFn = oldSleep
	})

	libraryHeadMutationRetryDelay = time.Millisecond
	libraryHeadMutationRetryMaxDelay = time.Millisecond
	libraryHeadMutationRetryJitter = 0
	var slept []time.Duration
	registerUploadedBlockSleepFn = func(delay time.Duration) {
		slept = append(slept, delay)
	}

	storeCalls := 0
	materializeCalls := 0
	err := RetryUploadedBlockMaterialization("UploadFile", "block-1", func() error {
		storeCalls++
		if storeCalls == 1 {
			return ErrBlockDeleteInProgress
		}
		return nil
	}, func() error {
		materializeCalls++
		return nil
	}, nil, nil)
	if err != nil {
		t.Fatalf("RetryUploadedBlockMaterialization() error = %v, want nil", err)
	}
	if storeCalls != 2 {
		t.Fatalf("storeCalls = %d, want 2", storeCalls)
	}
	if materializeCalls != 1 {
		t.Fatalf("materializeCalls = %d, want 1", materializeCalls)
	}
	if len(slept) != 1 || slept[0] != time.Millisecond {
		t.Fatalf("slept = %#v, want []time.Duration{time.Millisecond}", slept)
	}
}

func TestProbeUploadedBlockReuseReturnsUnknownErrorWithoutSession(t *testing.T) {
	probe, err := ProbeUploadedBlockReuse(nil, "org-1", "block-1")
	if err == nil {
		t.Fatal("ProbeUploadedBlockReuse() error = nil, want error")
	}
	if probe.Decision != 0 {
		t.Fatalf("decision = %v, want BlockReuseUnknownError", probe.Decision)
	}
}

func TestPrepareUploadedBlockProbeRepairsReleasedStub(t *testing.T) {
	oldRepair := repairReleasedBlockStubForUploadFn
	t.Cleanup(func() { repairReleasedBlockStubForUploadFn = oldRepair })
	deleteCalls := 0
	repairReleasedBlockStubForUploadFn = func(database *db.DB, orgID, blockID string) (bool, error) {
		deleteCalls++
		if orgID != "org-1" || blockID != "block-1" {
			t.Fatalf("delete args = %s/%s", orgID, blockID)
		}
		return true, nil
	}

	probe, err := PrepareUploadedBlockProbe(&db.DB{}, "org-1", "block-1", db.BlockReuseProbe{Decision: db.BlockReuseRepairableStub})
	if err != nil {
		t.Fatalf("PrepareUploadedBlockProbe() error = %v, want nil", err)
	}
	if probe.Decision != db.BlockReuseNeedsPut || deleteCalls != 1 {
		t.Fatalf("decision/deleteCalls = %v/%d, want NeedsPut/1", probe.Decision, deleteCalls)
	}
}

func TestPrepareUploadedBlockProbeLostCASStopsStore(t *testing.T) {
	oldRepair := repairReleasedBlockStubForUploadFn
	t.Cleanup(func() { repairReleasedBlockStubForUploadFn = oldRepair })
	repairReleasedBlockStubForUploadFn = func(*db.DB, string, string) (bool, error) { return false, nil }

	probe, err := PrepareUploadedBlockProbe(&db.DB{}, "org-1", "block-1", db.BlockReuseProbe{Decision: db.BlockReuseRepairableStub})
	if !errors.Is(err, ErrBlockDeleteInProgress) {
		t.Fatalf("PrepareUploadedBlockProbe() error = %v, want ErrBlockDeleteInProgress", err)
	}
	if probe.Decision != db.BlockReuseBlockedByGC {
		t.Fatalf("decision = %v, want BlockReuseBlockedByGC", probe.Decision)
	}
}

func TestPrepareUploadedBlockProbeLeavesOtherDecisionsUntouched(t *testing.T) {
	oldRepair := repairReleasedBlockStubForUploadFn
	t.Cleanup(func() { repairReleasedBlockStubForUploadFn = oldRepair })
	repairReleasedBlockStubForUploadFn = func(*db.DB, string, string) (bool, error) {
		t.Fatal("delete must not run for NeedsPut")
		return false, nil
	}

	probe, err := PrepareUploadedBlockProbe(&db.DB{}, "org-1", "block-1", db.BlockReuseProbe{Decision: db.BlockReuseNeedsPut})
	if err != nil || probe.Decision != db.BlockReuseNeedsPut {
		t.Fatalf("PrepareUploadedBlockProbe() = %v, %v", probe.Decision, err)
	}
}

// TestRetryUploadedBlockMaterializationConvergesAfterLostStubRepairCAS proves the
// acceptance property behind the lost-CAS reclassification: when a repairable stub
// loses its conditional delete to a concurrent actor, the store phase surfaces a
// retryable ErrBlockDeleteInProgress, the wrapper re-runs probe -> prepare -> store,
// and the second probe (now Reusable, as another uploader finished) completes the
// upload instead of returning a hard error.
func TestRetryUploadedBlockMaterializationConvergesAfterLostStubRepairCAS(t *testing.T) {
	oldDelay := libraryHeadMutationRetryDelay
	oldMaxDelay := libraryHeadMutationRetryMaxDelay
	oldJitter := libraryHeadMutationRetryJitter
	oldSleep := registerUploadedBlockSleepFn
	oldRepair := repairReleasedBlockStubForUploadFn
	t.Cleanup(func() {
		libraryHeadMutationRetryDelay = oldDelay
		libraryHeadMutationRetryMaxDelay = oldMaxDelay
		libraryHeadMutationRetryJitter = oldJitter
		registerUploadedBlockSleepFn = oldSleep
		repairReleasedBlockStubForUploadFn = oldRepair
	})
	libraryHeadMutationRetryDelay = time.Millisecond
	libraryHeadMutationRetryMaxDelay = time.Millisecond
	libraryHeadMutationRetryJitter = 0
	registerUploadedBlockSleepFn = func(time.Duration) {}

	// First attempt: repair loses the CAS (row changed under us) -> false, nil.
	repairReleasedBlockStubForUploadFn = func(*db.DB, string, string) (bool, error) { return false, nil }

	probeCalls := 0
	reuseChecks := 0
	puts := 0
	materializeCalls := 0
	store := func() error {
		probeCalls++
		var probe db.BlockReuseProbe
		if probeCalls == 1 {
			probe = db.BlockReuseProbe{Decision: db.BlockReuseRepairableStub}
		} else {
			// The concurrent actor finished materializing the block; a fresh probe
			// now sees it as reusable and repair must not run again.
			repairReleasedBlockStubForUploadFn = func(*db.DB, string, string) (bool, error) {
				t.Fatal("repair must not run once the block is reusable")
				return false, nil
			}
			probe = db.BlockReuseProbe{Decision: db.BlockReuseReusable}
		}
		prepared, prepErr := PrepareUploadedBlockProbe(&db.DB{}, "org-1", "block-1", probe)
		if prepErr != nil {
			return prepErr
		}
		switch prepared.Decision {
		case db.BlockReuseReusable:
			reuseChecks++
			return nil
		case db.BlockReuseNeedsPut:
			puts++
			return nil
		case db.BlockReuseBlockedByGC:
			return ErrBlockDeleteInProgress
		}
		return errors.New("unexpected decision")
	}

	err := RetryUploadedBlockMaterialization("UploadFile", "block-1", store, func() error {
		materializeCalls++
		return nil
	}, nil, nil)
	if err != nil {
		t.Fatalf("RetryUploadedBlockMaterialization() error = %v, want nil", err)
	}
	if probeCalls != 2 {
		t.Fatalf("probeCalls = %d, want 2 (retry re-probes)", probeCalls)
	}
	if reuseChecks != 1 || puts != 0 {
		t.Fatalf("reuseChecks/puts = %d/%d, want 1/0 (converged to reusable, no PUT)", reuseChecks, puts)
	}
	if materializeCalls != 1 {
		t.Fatalf("materializeCalls = %d, want 1", materializeCalls)
	}
}

func TestRetryUploadedBlockMaterializationReturnsNonRetryableStoreError(t *testing.T) {
	wantErr := errors.New("boom")
	err := RetryUploadedBlockMaterialization("UploadFile", "block-1", func() error {
		return wantErr
	}, func() error {
		t.Fatal("materialize should not run after non-retryable store error")
		return nil
	}, nil, nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("RetryUploadedBlockMaterialization() error = %v, want %v", err, wantErr)
	}
}

func TestEnsureReusableBlockPresentSkipsPutWhenCanonicalObjectExists(t *testing.T) {
	oldResolve := resolveCanonicalBlockStoreFn
	oldExists := reusableCanonicalObjectExistsFn
	oldRepair := repairCanonicalBlockDirectFn
	t.Cleanup(func() {
		resolveCanonicalBlockStoreFn = oldResolve
		reusableCanonicalObjectExistsFn = oldExists
		repairCanonicalBlockDirectFn = oldRepair
	})

	const orgID = "3fa85f64-5717-4562-b3fc-2c963f66afa6"
	canonicalStore, err := storage.NewOrgBlockStore(&storage.S3Store{}, "blocks/", orgID)
	if err != nil {
		t.Fatalf("NewOrgBlockStore() error = %v", err)
	}
	wantKey := canonicalStore.StorageKeyForHash("abcd1234")
	resolveCanonicalBlockStoreFn = func(storageManager *storage.Manager, fallbackStore *storage.BlockStore, fallbackClass, canonicalClass, orgID string) (*storage.BlockStore, error) {
		return canonicalStore, nil
	}
	headCalls := 0
	reusableCanonicalObjectExistsFn = func(ctx context.Context, blockStore *storage.BlockStore, storageKey string) (bool, error) {
		headCalls++
		if storageKey != wantKey {
			t.Fatalf("storageKey = %q, want %q", storageKey, wantKey)
		}
		return true, nil
	}
	repairCanonicalBlockDirectFn = func(ctx context.Context, blockStore *storage.BlockStore, storageKey string, data []byte) (string, error) {
		t.Fatal("repair PUT must not run when canonical object already exists")
		return "", nil
	}

	key, err := EnsureReusableBlockPresent(context.Background(), "abcd1234", db.BlockReuseProbe{
		Decision:     db.BlockReuseReusable,
		StorageClass: "hot-s3",
		StorageKey:   wantKey,
	}, []byte("data"), nil, canonicalStore, "hot-s3", orgID)
	if err != nil {
		t.Fatalf("EnsureReusableBlockPresent() error = %v, want nil", err)
	}
	if key != wantKey {
		t.Fatalf("key = %q, want %q", key, wantKey)
	}
	if headCalls != 1 {
		t.Fatalf("headCalls = %d, want 1", headCalls)
	}
}

func TestEnsureReusableBlockPresentRepairsMissingCanonicalObject(t *testing.T) {
	oldResolve := resolveCanonicalBlockStoreFn
	oldExists := reusableCanonicalObjectExistsFn
	oldRepair := repairCanonicalBlockDirectFn
	t.Cleanup(func() {
		resolveCanonicalBlockStoreFn = oldResolve
		reusableCanonicalObjectExistsFn = oldExists
		repairCanonicalBlockDirectFn = oldRepair
	})

	const orgID = "3fa85f64-5717-4562-b3fc-2c963f66afa6"
	canonicalStore, err := storage.NewOrgBlockStore(&storage.S3Store{}, "blocks/", orgID)
	if err != nil {
		t.Fatalf("NewOrgBlockStore() error = %v", err)
	}
	resolveCanonicalBlockStoreFn = func(storageManager *storage.Manager, fallbackStore *storage.BlockStore, fallbackClass, canonicalClass, orgID string) (*storage.BlockStore, error) {
		return canonicalStore, nil
	}
	headCalls := 0
	putCalls := 0
	blockID := "a1b2c3d4"
	wantKey := canonicalStore.StorageKeyForHash(blockID)
	reusableCanonicalObjectExistsFn = func(ctx context.Context, blockStore *storage.BlockStore, storageKey string) (bool, error) {
		headCalls++
		if storageKey != wantKey {
			t.Fatalf("storageKey = %q, want %q", storageKey, wantKey)
		}
		return false, nil
	}
	repairCanonicalBlockDirectFn = func(ctx context.Context, blockStore *storage.BlockStore, storageKey string, data []byte) (string, error) {
		putCalls++
		if storageKey != wantKey {
			t.Fatalf("repair storageKey = %q, want %q", storageKey, wantKey)
		}
		if string(data) != "repair-me" {
			t.Fatalf("repair data = %q, want repair-me", string(data))
		}
		return storageKey, nil
	}

	key, err := EnsureReusableBlockPresent(context.Background(), blockID, db.BlockReuseProbe{
		Decision:     db.BlockReuseReusable,
		StorageClass: "hot-s3",
		StorageKey:   "",
	}, []byte("repair-me"), nil, canonicalStore, "hot-s3", orgID)
	if err != nil {
		t.Fatalf("EnsureReusableBlockPresent() error = %v, want nil", err)
	}
	if key != wantKey {
		t.Fatalf("key = %q, want %q", key, wantKey)
	}
	if headCalls != 1 {
		t.Fatalf("headCalls = %d, want 1", headCalls)
	}
	if putCalls != 1 {
		t.Fatalf("putCalls = %d, want 1", putCalls)
	}
}

func TestEnsureReusableBlockPresentRejectsMismatchedStorageKey(t *testing.T) {
	oldResolve := resolveCanonicalBlockStoreFn
	oldExists := reusableCanonicalObjectExistsFn
	oldRepair := repairCanonicalBlockDirectFn
	t.Cleanup(func() {
		resolveCanonicalBlockStoreFn = oldResolve
		reusableCanonicalObjectExistsFn = oldExists
		repairCanonicalBlockDirectFn = oldRepair
	})

	const orgID = "3fa85f64-5717-4562-b3fc-2c963f66afa6"
	canonicalStore, err := storage.NewOrgBlockStore(&storage.S3Store{}, "blocks/", orgID)
	if err != nil {
		t.Fatalf("NewOrgBlockStore() error = %v", err)
	}
	resolveCanonicalBlockStoreFn = func(storageManager *storage.Manager, fallbackStore *storage.BlockStore, fallbackClass, canonicalClass, gotOrgID string) (*storage.BlockStore, error) {
		if gotOrgID != orgID {
			t.Fatalf("orgID = %q, want %q", gotOrgID, orgID)
		}
		return canonicalStore, nil
	}
	reusableCanonicalObjectExistsFn = func(context.Context, *storage.BlockStore, string) (bool, error) {
		t.Fatal("HEAD must not run for a mismatched stored key")
		return false, nil
	}
	repairCanonicalBlockDirectFn = func(context.Context, *storage.BlockStore, string, []byte) (string, error) {
		t.Fatal("repair PUT must not run for a mismatched stored key")
		return "", nil
	}

	_, err = EnsureReusableBlockPresent(context.Background(), "abcd1234", db.BlockReuseProbe{
		Decision:     db.BlockReuseReusable,
		StorageClass: "hot-s3",
		StorageKey:   "blocks/ab/cd/abcd1234",
	}, []byte("data"), nil, canonicalStore, "hot-s3", orgID)
	if err == nil {
		t.Fatal("EnsureReusableBlockPresent() error = nil, want mismatched key error")
	}
}

func TestResolveCanonicalBlockStoreUsesExactOrgScopedClass(t *testing.T) {
	m := storage.NewManager()
	m.RegisterBackend("canonical", &storage.S3Store{}, "")

	const orgID = "3fa85f64-5717-4562-b3fc-2c963f66afa6"
	blockStore, err := ResolveCanonicalBlockStore(m, nil, "", "canonical", orgID)
	if err != nil {
		t.Fatalf("ResolveCanonicalBlockStore() error = %v", err)
	}
	if got, want := blockStore.StorageKeyForHash("abcd1234"), "blocks/"+orgID+"/ab/cd/abcd1234"; got != want {
		t.Fatalf("StorageKeyForHash() = %q, want %q", got, want)
	}
}
