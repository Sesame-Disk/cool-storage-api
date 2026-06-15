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

	canonicalStore := storage.NewBlockStore(&storage.S3Store{}, "blocks/")
	resolveCanonicalBlockStoreFn = func(storageManager *storage.Manager, fallbackStore *storage.BlockStore, fallbackClass, canonicalClass string) (*storage.BlockStore, error) {
		return canonicalStore, nil
	}
	headCalls := 0
	reusableCanonicalObjectExistsFn = func(ctx context.Context, blockStore *storage.BlockStore, storageKey string) (bool, error) {
		headCalls++
		if storageKey != "blocks/custom/key" {
			t.Fatalf("storageKey = %q, want blocks/custom/key", storageKey)
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
		StorageKey:   "blocks/custom/key",
	}, []byte("data"), nil, canonicalStore, "hot-s3")
	if err != nil {
		t.Fatalf("EnsureReusableBlockPresent() error = %v, want nil", err)
	}
	if key != "blocks/custom/key" {
		t.Fatalf("key = %q, want blocks/custom/key", key)
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

	canonicalStore := storage.NewBlockStore(&storage.S3Store{}, "blocks/")
	resolveCanonicalBlockStoreFn = func(storageManager *storage.Manager, fallbackStore *storage.BlockStore, fallbackClass, canonicalClass string) (*storage.BlockStore, error) {
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
	}, []byte("repair-me"), nil, canonicalStore, "hot-s3")
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
