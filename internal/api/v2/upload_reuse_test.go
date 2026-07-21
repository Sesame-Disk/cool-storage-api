package v2

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type fastClearBlockDeleter struct {
	objectPresent *atomic.Bool
}

func (d fastClearBlockDeleter) DeleteBlock(context.Context, string) error {
	d.objectPresent.Store(false)
	return nil
}

type fastClearStorageProvider struct {
	objectPresent *atomic.Bool
}

func (p fastClearStorageProvider) GetBlockStoreForOrg(string, string) (gcpkg.BlockStoreDeleter, error) {
	return fastClearBlockDeleter{objectPresent: p.objectPresent}, nil
}

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

func TestRetryUploadedBlockMaterializationContextStopsCanceledBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var storeCalls atomic.Int32
	err := RetryUploadedBlockMaterializationContext(ctx, "canceled", "block-1", func() error {
		storeCalls.Add(1)
		return ErrBlockDeleteInProgress
	}, func() error {
		t.Fatal("materialize must not run after a fenced store")
		return nil
	}, nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if storeCalls.Load() != 1 {
		t.Fatalf("store calls = %d, want 1", storeCalls.Load())
	}
}

func TestRetryUploadedBlockMaterializationRestoresObjectAfterFastClearingFence(t *testing.T) {
	installSplitProvisionalReferenceHookForTest(t)
	oldAdd := registerUploadedBlockAddReferenceFn
	oldExpiry := registerUploadedBlockUpsertProvisionalExpiryFn
	oldFence := registerUploadedBlockFenceActiveFn
	oldUpsert := registerUploadedBlockUpsertMetadataFn
	oldRelease := registerUploadedBlockReleaseRefsFn
	oldEnqueue := registerUploadedBlockEnqueueZeroRefFn
	oldDelay := libraryHeadMutationRetryDelay
	oldMaxDelay := libraryHeadMutationRetryMaxDelay
	oldJitter := libraryHeadMutationRetryJitter
	oldSleep := registerUploadedBlockSleepFn
	t.Cleanup(func() {
		registerUploadedBlockAddReferenceFn = oldAdd
		registerUploadedBlockUpsertProvisionalExpiryFn = oldExpiry
		registerUploadedBlockFenceActiveFn = oldFence
		registerUploadedBlockUpsertMetadataFn = oldUpsert
		registerUploadedBlockReleaseRefsFn = oldRelease
		registerUploadedBlockEnqueueZeroRefFn = oldEnqueue
		libraryHeadMutationRetryDelay = oldDelay
		libraryHeadMutationRetryMaxDelay = oldMaxDelay
		libraryHeadMutationRetryJitter = oldJitter
		registerUploadedBlockSleepFn = oldSleep
	})

	libraryHeadMutationRetryDelay = 0
	libraryHeadMutationRetryMaxDelay = 0
	libraryHeadMutationRetryJitter = 0
	registerUploadedBlockSleepFn = func(time.Duration) {}
	registerUploadedBlockAddReferenceFn = func(*FSHelper, string, string, string, string, int) error { return nil }
	registerUploadedBlockUpsertProvisionalExpiryFn = func(*FSHelper, string, string, string, string, time.Time) error { return nil }
	registerUploadedBlockReleaseRefsFn = func(*FSHelper, string, string, string, []string) []string { return nil }
	registerUploadedBlockEnqueueZeroRefFn = func(string, []string, string) {}

	objectPresent := false
	storeCalls := 0
	materializeCalls := 0
	fenceChecks := 0
	metadataWrites := 0
	registerUploadedBlockFenceActiveFn = func(*FSHelper, string, string) (bool, error) {
		fenceChecks++
		if fenceChecks == 1 {
			// Model the GC delete that completed after the first PUT and before
			// the writer observed its fence.
			objectPresent = false
			return true, nil
		}
		return false, nil
	}
	registerUploadedBlockUpsertMetadataFn = func(*FSHelper, string, string, string, string, int, string, string) error {
		if !objectPresent {
			t.Fatal("metadata published without a post-fence physical store")
		}
		metadataWrites++
		return nil
	}

	helper := &FSHelper{}
	err := RetryUploadedBlockMaterialization("fast-clear", "block-1", func() error {
		storeCalls++
		objectPresent = true
		return nil
	}, func() error {
		materializeCalls++
		return helper.RegisterUploadedBlock("org-1", "lib-1", "block-1", "op-1", 4, "hot", "", "")
	}, nil, nil)
	if err != nil {
		t.Fatalf("RetryUploadedBlockMaterialization() error = %v", err)
	}
	if storeCalls != 2 || materializeCalls != 2 {
		t.Fatalf("calls store/materialize = %d/%d, want 2/2", storeCalls, materializeCalls)
	}
	if !objectPresent || metadataWrites != 1 {
		t.Fatalf("objectPresent/metadataWrites = %v/%d, want true/1", objectPresent, metadataWrites)
	}
}

func TestRetryUploadedBlockMaterializationWithWorkerPausedAfterPostClaimCheck(t *testing.T) {
	installSplitProvisionalReferenceHookForTest(t)
	oldAdd := registerUploadedBlockAddReferenceFn
	oldExpiry := registerUploadedBlockUpsertProvisionalExpiryFn
	oldFence := registerUploadedBlockFenceActiveFn
	oldUpsert := registerUploadedBlockUpsertMetadataFn
	oldRelease := registerUploadedBlockReleaseRefsFn
	oldEnqueue := registerUploadedBlockEnqueueZeroRefFn
	oldDelay := libraryHeadMutationRetryDelay
	oldMaxDelay := libraryHeadMutationRetryMaxDelay
	oldJitter := libraryHeadMutationRetryJitter
	oldSleep := registerUploadedBlockSleepFn
	t.Cleanup(func() {
		registerUploadedBlockAddReferenceFn = oldAdd
		registerUploadedBlockUpsertProvisionalExpiryFn = oldExpiry
		registerUploadedBlockFenceActiveFn = oldFence
		registerUploadedBlockUpsertMetadataFn = oldUpsert
		registerUploadedBlockReleaseRefsFn = oldRelease
		registerUploadedBlockEnqueueZeroRefFn = oldEnqueue
		libraryHeadMutationRetryDelay = oldDelay
		libraryHeadMutationRetryMaxDelay = oldMaxDelay
		libraryHeadMutationRetryJitter = oldJitter
		registerUploadedBlockSleepFn = oldSleep
	})

	store := gcpkg.NewMockStore()
	orgID := uuid.New()
	blockID := "block-fast-clear-worker"
	candidateAt := time.Now().Add(-2 * time.Hour).UTC()
	store.AddBlock(orgID, blockID, "hot", 0)
	store.AddBlockGCCandidate(orgID, blockID, "hot", candidateAt)
	if err := store.EnqueueItem(orgID, candidateAt, gcpkg.ItemBlock, blockID, uuid.Nil, "hot", 0); err != nil {
		t.Fatalf("EnqueueItem(): %v", err)
	}

	postClaimReached := make(chan struct{})
	releasePostClaim := make(chan struct{})
	var referenceChecks atomic.Int32
	store.SetBlockHasReferencesHookForTest(func(uuid.UUID, string, bool) (bool, error) {
		if referenceChecks.Add(1) == 2 {
			close(postClaimReached)
			<-releasePostClaim
		}
		return false, nil
	})

	var objectPresent atomic.Bool
	worker := gcpkg.NewWorker(store, fastClearStorageProvider{objectPresent: &objectPresent}, gcpkg.NewQueue(store), 1, 0, false, &gcpkg.Stats{})
	workerDone := make(chan error, 1)

	firstStoreDone := make(chan struct{})
	releaseFirstStore := make(chan struct{})
	fenceObserved := make(chan struct{})
	gcDone := make(chan struct{})
	var storeCalls atomic.Int32
	var materializeCalls atomic.Int32
	var metadataWrites atomic.Int32
	var fenceSignaled atomic.Bool

	libraryHeadMutationRetryDelay = time.Millisecond
	libraryHeadMutationRetryMaxDelay = time.Millisecond
	libraryHeadMutationRetryJitter = 0
	registerUploadedBlockSleepFn = func(time.Duration) { <-gcDone }
	registerUploadedBlockAddReferenceFn = func(*FSHelper, string, string, string, string, int) error { return nil }
	registerUploadedBlockUpsertProvisionalExpiryFn = func(*FSHelper, string, string, string, string, time.Time) error { return nil }
	registerUploadedBlockReleaseRefsFn = func(*FSHelper, string, string, string, []string) []string { return nil }
	registerUploadedBlockEnqueueZeroRefFn = func(string, []string, string) {}
	registerUploadedBlockFenceActiveFn = func(*FSHelper, string, string) (bool, error) {
		block := store.GetBlock(orgID, blockID)
		active := block != nil && block.GCState == db.BlockGCStateDeleting || store.S3OrphanCount() > 0
		if active && fenceSignaled.CompareAndSwap(false, true) {
			close(fenceObserved)
		}
		return active, nil
	}
	registerUploadedBlockUpsertMetadataFn = func(*FSHelper, string, string, string, string, int, string, string) error {
		if !objectPresent.Load() {
			return errors.New("metadata published while the physical object is absent")
		}
		metadataWrites.Add(1)
		return nil
	}

	helper := &FSHelper{}
	uploadDone := make(chan error, 1)
	go func() {
		uploadDone <- RetryUploadedBlockMaterialization("worker-fast-clear", blockID, func() error {
			call := storeCalls.Add(1)
			objectPresent.Store(true)
			if call == 1 {
				close(firstStoreDone)
				<-releaseFirstStore
			}
			return nil
		}, func() error {
			materializeCalls.Add(1)
			return helper.RegisterUploadedBlock(orgID.String(), uuid.NewString(), blockID, "op-1", 7, "hot", "", "")
		}, nil, nil)
	}()

	<-firstStoreDone
	go func() {
		_, err := worker.ProcessOnce(context.Background())
		workerDone <- err
	}()
	<-postClaimReached
	close(releaseFirstStore)
	<-fenceObserved
	close(releasePostClaim)
	if err := <-workerDone; err != nil {
		t.Fatalf("worker.ProcessOnce(): %v", err)
	}
	close(gcDone)
	if err := <-uploadDone; err != nil {
		t.Fatalf("RetryUploadedBlockMaterialization(): %v", err)
	}

	if got := storeCalls.Load(); got != 2 {
		t.Fatalf("store calls = %d, want 2", got)
	}
	if got := materializeCalls.Load(); got != 2 {
		t.Fatalf("materialize calls = %d, want 2", got)
	}
	if !objectPresent.Load() || metadataWrites.Load() != 1 {
		t.Fatalf("objectPresent/metadataWrites = %v/%d, want true/1", objectPresent.Load(), metadataWrites.Load())
	}
}

// A crashed GC worker can leave a claimed metadata stub (no storage_class, no
// created_at). The repair for it lives in RegisterUploadedBlock, which only runs
// in the materialize phase, so this exercises the real store->materialize loop
// rather than calling the repair helper directly: the upload must clear the stub
// and then publish metadata instead of dead-ending.
func TestRetryUploadedBlockMaterializationRepairsClaimedStubAndSucceeds(t *testing.T) {
	oldProvisional := registerUploadedBlockAddProvisionalFn
	oldFence := registerUploadedBlockFenceActiveFn
	oldClaimInfo := registerUploadedBlockClaimInfoFn
	oldDeleteClaimed := registerUploadedBlockDeleteStubFn
	oldDeleteReleased := registerUploadedBlockDeleteReleasedStubFn
	oldUpsert := registerUploadedBlockUpsertMetadataFn
	oldDelay := libraryHeadMutationRetryDelay
	oldMaxDelay := libraryHeadMutationRetryMaxDelay
	oldJitter := libraryHeadMutationRetryJitter
	oldSleep := registerUploadedBlockSleepFn
	t.Cleanup(func() {
		registerUploadedBlockAddProvisionalFn = oldProvisional
		registerUploadedBlockFenceActiveFn = oldFence
		registerUploadedBlockClaimInfoFn = oldClaimInfo
		registerUploadedBlockDeleteStubFn = oldDeleteClaimed
		registerUploadedBlockDeleteReleasedStubFn = oldDeleteReleased
		registerUploadedBlockUpsertMetadataFn = oldUpsert
		libraryHeadMutationRetryDelay = oldDelay
		libraryHeadMutationRetryMaxDelay = oldMaxDelay
		libraryHeadMutationRetryJitter = oldJitter
		registerUploadedBlockSleepFn = oldSleep
	})

	libraryHeadMutationRetryDelay = 0
	libraryHeadMutationRetryMaxDelay = 0
	libraryHeadMutationRetryJitter = 0
	registerUploadedBlockSleepFn = func(time.Duration) {}
	registerUploadedBlockAddProvisionalFn = func(*FSHelper, string, string, string, string, string, time.Time) error {
		return nil
	}

	stubPresent := true
	storeCalls := 0
	stubDeletes := 0
	metadataWrites := 0

	registerUploadedBlockFenceActiveFn = func(*FSHelper, string, string) (bool, error) {
		return stubPresent, nil
	}
	registerUploadedBlockClaimInfoFn = func(*FSHelper, string, string) (db.BlockDeleteClaimInfo, bool, error) {
		if !stubPresent {
			return db.BlockDeleteClaimInfo{}, false, nil
		}
		return db.BlockDeleteClaimInfo{GCState: db.BlockGCStateDeleting, GCClaimID: "claim-1"}, true, nil
	}
	registerUploadedBlockDeleteStubFn = func(_ *FSHelper, _, _, claimID string) (bool, error) {
		if claimID != "claim-1" {
			t.Fatalf("claimID = %q, want claim-1", claimID)
		}
		stubDeletes++
		stubPresent = false
		return true, nil
	}
	registerUploadedBlockDeleteReleasedStubFn = func(*FSHelper, string, string) (bool, error) {
		t.Fatal("released-stub LWT must not run while the stub is still claimed")
		return false, nil
	}
	registerUploadedBlockUpsertMetadataFn = func(*FSHelper, string, string, string, string, int, string, string) error {
		if stubPresent {
			t.Fatal("metadata published while the GC stub still exists")
		}
		metadataWrites++
		return nil
	}

	helper := &FSHelper{db: &db.DB{}}
	err := RetryUploadedBlockMaterialization("stub-repair", "block-1", func() error {
		storeCalls++
		return nil
	}, func() error {
		return helper.RegisterUploadedBlock("org-1", "lib-1", "block-1", "op-1", 4, "hot", "", "")
	}, nil, nil)
	if err != nil {
		t.Fatalf("RetryUploadedBlockMaterialization() error = %v, want nil", err)
	}
	if storeCalls != 2 {
		t.Fatalf("store calls = %d, want 2", storeCalls)
	}
	if stubDeletes != 1 || metadataWrites != 1 {
		t.Fatalf("stubDeletes/metadataWrites = %d/%d, want 1/1", stubDeletes, metadataWrites)
	}
}

// The retry budget is only useful if the production helper actually tags its
// transient failures. Injecting the sentinel by hand proves the wrapper, not the
// producer, so this drives RegisterUploadedBlock itself and asserts both
// directions: Cassandra I/O is retryable, a violated invariant is not.
func TestRegisterUploadedBlockTagsTransientButNotPermanentFailures(t *testing.T) {
	oldProvisional := registerUploadedBlockAddProvisionalFn
	oldFence := registerUploadedBlockFenceActiveFn
	oldClaimInfo := registerUploadedBlockClaimInfoFn
	oldUpsert := registerUploadedBlockUpsertMetadataFn
	t.Cleanup(func() {
		registerUploadedBlockAddProvisionalFn = oldProvisional
		registerUploadedBlockFenceActiveFn = oldFence
		registerUploadedBlockClaimInfoFn = oldClaimInfo
		registerUploadedBlockUpsertMetadataFn = oldUpsert
	})

	registerUploadedBlockFenceActiveFn = func(*FSHelper, string, string) (bool, error) { return false, nil }
	registerUploadedBlockClaimInfoFn = func(*FSHelper, string, string) (db.BlockDeleteClaimInfo, bool, error) {
		return db.BlockDeleteClaimInfo{}, false, nil
	}
	registerUploadedBlockUpsertMetadataFn = func(*FSHelper, string, string, string, string, int, string, string) error {
		return nil
	}
	helper := &FSHelper{db: &db.DB{}}

	t.Run("provisional write timeout is retryable", func(t *testing.T) {
		registerUploadedBlockAddProvisionalFn = func(*FSHelper, string, string, string, string, string, time.Time) error {
			return errors.New("gocql: no response received from cassandra within timeout period")
		}
		t.Cleanup(func() { registerUploadedBlockAddProvisionalFn = oldProvisional })
		err := helper.RegisterUploadedBlock("org-1", "lib-1", "block-1", "op-1", 4, "hot", "", "")
		if !IsRetryableBlockMaterializationError(err) {
			t.Fatalf("error = %v, want retryable", err)
		}
	})

	t.Run("fence read timeout is retryable", func(t *testing.T) {
		registerUploadedBlockAddProvisionalFn = func(*FSHelper, string, string, string, string, string, time.Time) error {
			return nil
		}
		registerUploadedBlockFenceActiveFn = func(*FSHelper, string, string) (bool, error) {
			return false, errors.New("gocql: connection closed")
		}
		t.Cleanup(func() {
			registerUploadedBlockAddProvisionalFn = oldProvisional
			registerUploadedBlockFenceActiveFn = func(*FSHelper, string, string) (bool, error) { return false, nil }
		})
		err := helper.RegisterUploadedBlock("org-1", "lib-1", "block-1", "op-1", 4, "hot", "", "")
		if !IsRetryableBlockMaterializationError(err) {
			t.Fatalf("error = %v, want retryable", err)
		}
	})

	t.Run("permanent metadata failure is not retryable", func(t *testing.T) {
		registerUploadedBlockAddProvisionalFn = func(*FSHelper, string, string, string, string, string, time.Time) error {
			return nil
		}
		registerUploadedBlockUpsertMetadataFn = func(*FSHelper, string, string, string, string, int, string, string) error {
			return fmt.Errorf("%w: storage_class must not be empty", db.ErrBlockMetadataPermanent)
		}
		t.Cleanup(func() {
			registerUploadedBlockAddProvisionalFn = oldProvisional
			registerUploadedBlockUpsertMetadataFn = oldUpsert
		})
		err := helper.RegisterUploadedBlock("org-1", "lib-1", "block-1", "op-1", 4, "hot", "", "")
		if err == nil {
			t.Fatal("error = nil, want permanent failure")
		}
		if IsRetryableBlockMaterializationError(err) {
			t.Fatalf("error = %v, want NOT retryable", err)
		}
	})
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

func TestRetryUploadedBlockMaterializationClassifiesRetryReason(t *testing.T) {
	originalDelay := libraryHeadMutationRetryDelay
	originalJitter := libraryHeadMutationRetryJitter
	libraryHeadMutationRetryDelay = 0
	libraryHeadMutationRetryJitter = 0
	t.Cleanup(func() {
		libraryHeadMutationRetryDelay = originalDelay
		libraryHeadMutationRetryJitter = originalJitter
	})

	tests := []struct {
		name          string
		retryErr      error
		retryPhase    string
		reason        string
		wantRetryable bool
	}{
		{name: "probe", retryErr: fmt.Errorf("%w: timeout", ErrBlockReuseProbeFailed), retryPhase: "store", reason: "probe", wantRetryable: true},
		{name: "provisional materialization", retryErr: fmt.Errorf("%w: publish provisional reference: timeout", ErrBlockMaterializationTransient), retryPhase: "materialize", reason: "materialization", wantRetryable: true},
		{name: "metadata materialization", retryErr: fmt.Errorf("%w: publish canonical metadata: timeout", ErrBlockMaterializationTransient), retryPhase: "materialize", reason: "materialization", wantRetryable: true},
		{name: "GC fence", retryErr: ErrBlockDeleteInProgress, retryPhase: "store", reason: "gc_fence", wantRetryable: true},
		{name: "non-retryable", retryErr: errors.New("permanent failure"), retryPhase: "store", wantRetryable: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			surface := "retry-reason-" + tt.reason
			if tt.reason == "" {
				surface = "retry-reason-non-retryable"
			}
			before := map[string]float64{}
			for _, reason := range []string{"probe", "materialization", "gc_fence"} {
				before[reason] = testutil.ToFloat64(metrics.BlockUploadMaterializationRetriesTotal.WithLabelValues(surface, reason))
			}

			storeCalls := 0
			materializeCalls := 0
			err := RetryUploadedBlockMaterialization(surface, "block-1", func() error {
				storeCalls++
				if tt.retryPhase == "store" && storeCalls == 1 {
					return tt.retryErr
				}
				return nil
			}, func() error {
				materializeCalls++
				if tt.retryPhase == "materialize" && materializeCalls == 1 {
					return tt.retryErr
				}
				return nil
			}, nil, nil)

			if tt.wantRetryable {
				if err != nil {
					t.Fatalf("RetryUploadedBlockMaterialization() error = %v", err)
				}
			} else if !errors.Is(err, tt.retryErr) {
				t.Fatalf("RetryUploadedBlockMaterialization() error = %v, want %v", err, tt.retryErr)
			}
			for _, reason := range []string{"probe", "materialization", "gc_fence"} {
				want := before[reason]
				if tt.wantRetryable && reason == tt.reason {
					want++
				}
				if got := testutil.ToFloat64(metrics.BlockUploadMaterializationRetriesTotal.WithLabelValues(surface, reason)); got != want {
					t.Errorf("reason %q counter = %v, want %v", reason, got, want)
				}
			}
		})
	}
}

func TestBlockMaterializationTransientPreservesCause(t *testing.T) {
	cause := errors.New("database timeout")
	err := fmt.Errorf("%w: register provisional reference: %w", ErrBlockMaterializationTransient, cause)

	if !IsRetryableBlockMaterializationError(err) {
		t.Fatal("transient materialization error should be retryable")
	}
	if !errors.Is(err, cause) {
		t.Fatal("transient materialization error should preserve its cause")
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

func TestResolveNeedsPutBlockStoreUsesExistingMetadataClass(t *testing.T) {
	m := storage.NewManager()
	m.RegisterBackend("preferred", &storage.S3Store{}, "")
	m.RegisterBackend("canonical", &storage.S3Store{}, "")

	const orgID = "3fa85f64-5717-4562-b3fc-2c963f66afa6"
	preferred, err := m.GetBlockStoreForOrg(orgID, "preferred")
	if err != nil {
		t.Fatalf("GetBlockStoreForOrg(preferred): %v", err)
	}
	blockID := "abcd1234"
	canonicalKey := "blocks/" + orgID + "/ab/cd/" + blockID
	resolved, resolvedClass, resolvedKey, err := ResolveNeedsPutBlockStore(m, preferred, "preferred", db.BlockReuseProbe{
		Decision:     db.BlockReuseNeedsPut,
		StorageClass: "canonical",
		StorageKey:   canonicalKey,
	}, orgID, blockID)
	if err != nil {
		t.Fatalf("ResolveNeedsPutBlockStore() error = %v", err)
	}
	if resolvedClass != "canonical" || resolvedKey != canonicalKey {
		t.Fatalf("class/key = %q/%q, want canonical/%q", resolvedClass, resolvedKey, canonicalKey)
	}
	if got := resolved.StorageKeyForHash(blockID); got != canonicalKey {
		t.Fatalf("resolved store key = %q, want %q", got, canonicalKey)
	}
}

func TestResolveNeedsPutBlockStoreUsesPreferredClassForFirstWriter(t *testing.T) {
	const orgID = "3fa85f64-5717-4562-b3fc-2c963f66afa6"
	preferred, err := storage.NewOrgBlockStore(&storage.S3Store{}, "blocks/", orgID)
	if err != nil {
		t.Fatalf("NewOrgBlockStore(): %v", err)
	}
	resolved, resolvedClass, resolvedKey, err := ResolveNeedsPutBlockStore(nil, preferred, "preferred", db.BlockReuseProbe{
		Decision: db.BlockReuseNeedsPut,
	}, orgID, "abcd1234")
	if err != nil {
		t.Fatalf("ResolveNeedsPutBlockStore() error = %v", err)
	}
	if resolved != preferred || resolvedClass != "preferred" || resolvedKey != preferred.StorageKeyForHash("abcd1234") {
		t.Fatalf("resolved preferred target = %p/%q/%q", resolved, resolvedClass, resolvedKey)
	}
}

func TestStoreUploadedBlockForProbeRunsAdmissionOnlyForPhysicalPut(t *testing.T) {
	oldPut := repairCanonicalBlockDirectFn
	t.Cleanup(func() { repairCanonicalBlockDirectFn = oldPut })

	const orgID = "3fa85f64-5717-4562-b3fc-2c963f66afa6"
	preferred, err := storage.NewOrgBlockStore(&storage.S3Store{}, "blocks/", orgID)
	if err != nil {
		t.Fatalf("NewOrgBlockStore(): %v", err)
	}
	putCalls := 0
	repairCanonicalBlockDirectFn = func(_ context.Context, gotStore *storage.BlockStore, storageKey string, data []byte) (string, error) {
		putCalls++
		if gotStore != preferred || storageKey != preferred.StorageKeyForHash("abcd1234") || string(data) != "payload" {
			t.Fatalf("put target/data = %p/%q/%q", gotStore, storageKey, data)
		}
		return storageKey, nil
	}
	admissionCalls := 0
	key, class, didPut, err := StoreUploadedBlockForProbe(context.Background(), "abcd1234", db.BlockReuseProbe{
		Decision: db.BlockReuseNeedsPut,
	}, []byte("payload"), nil, preferred, "preferred", orgID, func() error {
		admissionCalls++
		return nil
	})
	if err != nil {
		t.Fatalf("StoreUploadedBlockForProbe() error = %v", err)
	}
	if key != preferred.StorageKeyForHash("abcd1234") || class != "preferred" || !didPut {
		t.Fatalf("key/class/didPut = %q/%q/%v", key, class, didPut)
	}
	if admissionCalls != 1 || putCalls != 1 {
		t.Fatalf("admission/put calls = %d/%d, want 1/1", admissionCalls, putCalls)
	}
}

func TestStoreUploadedBlockForProbeBlockedByGCDoesNotAdmitOrPut(t *testing.T) {
	oldPut := repairCanonicalBlockDirectFn
	t.Cleanup(func() { repairCanonicalBlockDirectFn = oldPut })
	repairCanonicalBlockDirectFn = func(context.Context, *storage.BlockStore, string, []byte) (string, error) {
		t.Fatal("PUT must not run while GC fence is active")
		return "", nil
	}
	admissionCalls := 0
	_, _, _, err := StoreUploadedBlockForProbe(context.Background(), "block-1", db.BlockReuseProbe{
		Decision: db.BlockReuseBlockedByGC,
	}, []byte("payload"), nil, nil, "preferred", "org-1", func() error {
		admissionCalls++
		return nil
	})
	if !errors.Is(err, ErrBlockDeleteInProgress) {
		t.Fatalf("StoreUploadedBlockForProbe() error = %v, want ErrBlockDeleteInProgress", err)
	}
	if admissionCalls != 0 {
		t.Fatalf("admissionCalls = %d, want 0", admissionCalls)
	}
}
