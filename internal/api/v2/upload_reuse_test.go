package v2

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/gc"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

var uploadReuseTestBlockID = strings.Repeat("a", 64)

type fastClearTestBlockStore struct {
	orgID         string
	objectPresent *atomic.Bool
}

// The worker asks the resolved store to validate the persisted physical locator
// before deletion, so this stub mirrors the mock store's org-scoped validation,
// including the resolved SHA-256 block-ID format.
func (s fastClearTestBlockStore) ValidatePhysicalLocator(blockID, storageKey string) error {
	if !db.IsSHA256BlockID(blockID) {
		return fmt.Errorf("block id %q is not a resolved SHA-256 block id", blockID)
	}
	base := gc.MockCanonicalStorageKey(s.orgID, blockID)
	if storageKey == base {
		return nil
	}
	incarnationPrefix := base + "."
	if !strings.HasPrefix(storageKey, incarnationPrefix) {
		return fmt.Errorf("block storage key %q does not match block id %q", storageKey, blockID)
	}
	incarnation := strings.TrimPrefix(storageKey, incarnationPrefix)
	parsed, err := uuid.Parse(incarnation)
	if err != nil || parsed.String() != incarnation {
		return fmt.Errorf("block storage key %q has a malformed or non-canonical incarnation", storageKey)
	}
	return nil
}

func fastClearTestBlockID(label string) string {
	digest := sha256.Sum256([]byte("sesamefs-v2-fast-clear:" + label))
	return hex.EncodeToString(digest[:])
}

func (s fastClearTestBlockStore) DeleteBlockByStorageKey(context.Context, string) error {
	s.objectPresent.Store(false)
	return nil
}

type fastClearTestStorageProvider struct {
	objectPresent *atomic.Bool
}

func (p fastClearTestStorageProvider) GetBlockStoreForOrg(orgID, _ string) (gc.BlockStoreDeleter, error) {
	return fastClearTestBlockStore{orgID: orgID, objectPresent: p.objectPresent}, nil
}

// fastBlockMaterializationRetries shrinks the shared retry backoff to keep tests
// fast and returns the recorded sleeps captured through the overridable hook.
func fastBlockMaterializationRetries(t *testing.T) *[]time.Duration {
	t.Helper()
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
	slept := &[]time.Duration{}
	registerUploadedBlockSleepFn = func(delay time.Duration) { *slept = append(*slept, delay) }
	return slept
}

func retryReasonCount(surface, reason string) float64 {
	return testutil.ToFloat64(metrics.BlockUploadMaterializationRetriesTotal.WithLabelValues(surface, reason))
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
	if storeCalls != 3 {
		t.Fatalf("storeCalls = %d, want 3 (failed store, retry store, confirmation)", storeCalls)
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
	if probeCalls != 3 {
		t.Fatalf("probeCalls = %d, want 3 (retry and confirmation re-probe)", probeCalls)
	}
	if reuseChecks != 2 || puts != 0 {
		t.Fatalf("reuseChecks/puts = %d/%d, want 2/0 (retry plus confirmation reusable, no PUT)", reuseChecks, puts)
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

func TestRetryUploadedBlockMaterializationRetriesTransientMaterialize(t *testing.T) {
	fastBlockMaterializationRetries(t)

	storeCalls := 0
	materializeCalls := 0
	err := RetryUploadedBlockMaterialization("UploadFile", "block-1", func() error {
		storeCalls++
		return nil
	}, func() error {
		materializeCalls++
		if materializeCalls == 1 {
			return fmt.Errorf("cassandra timeout: %w", ErrBlockMaterializationTransient)
		}
		return nil
	}, nil, nil)
	if err != nil {
		t.Fatalf("error = %v, want nil", err)
	}
	// A transient materialize failure re-runs the WHOLE cycle, so store re-PUTs
	// before the second materialize.
	if storeCalls != 3 || materializeCalls != 2 {
		t.Fatalf("store/materialize calls = %d/%d, want 3/2", storeCalls, materializeCalls)
	}
}

func TestRetryUploadedBlockMaterializationRetriesTransientConfirmation(t *testing.T) {
	fastBlockMaterializationRetries(t)

	storeCalls := 0
	materializeCalls := 0
	err := RetryUploadedBlockMaterialization("Confirmation", "block-1", func() error {
		storeCalls++
		if storeCalls == 2 {
			return fmt.Errorf("HEAD timeout: %w", ErrBlockMaterializationTransient)
		}
		return nil
	}, func() error {
		materializeCalls++
		return nil
	}, nil, nil)
	if err != nil {
		t.Fatalf("error = %v, want nil", err)
	}
	if storeCalls != 4 || materializeCalls != 2 {
		t.Fatalf("store/materialize calls = %d/%d, want 4/2", storeCalls, materializeCalls)
	}
}

// TestRetryUploadedBlockMaterializationRePutsAfterMaterializeFence is the F1
// regression for the generic wrapper: when the materialize phase
// (RegisterUploadedBlock) reports a GC delete fence — the exact case where GC may
// have physically deleted the object — the wrapper repeats the WHOLE cycle so the
// store phase re-PUTs the object before the second materialize succeeds. Three of
// the six funnels use this generic wrapper (UploadFile, PutBlock, OnlyOffice); the
// SeafHTTP and template-CreateFile wrappers are separate copies with the same
// re-PUT-on-fence shape, exercised by their own tests
// (TestRetrySeafHTTP... and TestRetryCreateFileTemplate...). This F1 coverage is at
// the wrapper level; the deterministic fast-clear window (a full GC cycle between
// the single fence read and publish) is closed with PR-5's real-worker regression.
func TestRetryUploadedBlockMaterializationRePutsAfterMaterializeFence(t *testing.T) {
	fastBlockMaterializationRetries(t)

	puts := 0
	materializeCalls := 0
	// store models a NeedsPut branch: each invocation is a physical re-PUT.
	store := func() error {
		puts++
		return nil
	}
	materialize := func() error {
		materializeCalls++
		if materializeCalls == 1 {
			// GC fenced the block during materialize; the object may be gone.
			return ErrBlockDeleteInProgress
		}
		return nil
	}
	if err := RetryUploadedBlockMaterialization("UploadFile", "block-1", store, materialize, nil, nil); err != nil {
		t.Fatalf("error = %v, want nil", err)
	}
	if puts != 3 {
		t.Fatalf("store observations = %d, want 3 (retry plus post-materialization confirmation)", puts)
	}
	if materializeCalls != 2 {
		t.Fatalf("materialize calls = %d, want 2", materializeCalls)
	}
}

func TestRetryUploadedBlockMaterializationRepairsUnobservedFastClear(t *testing.T) {
	fastBlockMaterializationRetries(t)

	objectPresent := false
	storeCalls := 0
	materializeCalls := 0
	err := RetryUploadedBlockMaterialization("FastClear", "block-1", func() error {
		storeCalls++
		objectPresent = true
		return nil
	}, func() error {
		materializeCalls++
		// Model a full GC delete cycle that already cleared its fence. The
		// materializer therefore succeeds without emitting a retry sentinel.
		objectPresent = false
		return nil
	}, nil, nil)
	if err != nil {
		t.Fatalf("error = %v, want nil", err)
	}
	if !objectPresent {
		t.Fatal("post-materialization confirmation did not repair the deleted object")
	}
	if storeCalls != 2 || materializeCalls != 1 {
		t.Fatalf("store/materialize calls = %d/%d, want 2/1", storeCalls, materializeCalls)
	}
}

func TestRetryUploadedBlockMaterializationWithWorkerFastClear(t *testing.T) {
	fastBlockMaterializationRetries(t)

	store := gc.NewMockStore()
	orgID := uuid.New()
	libraryID := uuid.New()
	blockID := fastClearTestBlockID("fast-clear-block")
	candidateAt := time.Now().Add(-2 * time.Hour).UTC()
	store.AddBlock(orgID, blockID, "hot", 0)
	store.AddBlockGCCandidate(orgID, blockID, "hot", candidateAt)
	if err := store.EnqueueItem(orgID, candidateAt, gc.ItemBlock, blockID, libraryID, "hot", 0); err != nil {
		t.Fatalf("enqueue block: %v", err)
	}

	postClaimRead := make(chan struct{})
	releasePostClaimRead := make(chan struct{})
	var referenceChecks atomic.Int32
	store.SetBlockHasReferencesHookForTest(func(hookOrgID uuid.UUID, hookBlockID string, current bool) (bool, error) {
		if hookOrgID != orgID || hookBlockID != blockID {
			return current, nil
		}
		if referenceChecks.Add(1) == 2 {
			close(postClaimRead)
			<-releasePostClaimRead
			// Return the captured pre-reference result. This is the destructive
			// decision the upload races after the worker has already observed zero.
			return false, nil
		}
		return current, nil
	})

	var objectPresent atomic.Bool
	provider := fastClearTestStorageProvider{objectPresent: &objectPresent}
	worker := gc.NewWorker(store, provider, gc.NewQueue(store), 1, 0, false, &gc.Stats{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	workerDone := make(chan error, 1)
	storeCalls := 0
	materializeCalls := 0

	err := RetryUploadedBlockMaterialization("FastClearWorker", blockID, func() error {
		storeCalls++
		objectPresent.Store(true)
		if storeCalls == 1 {
			go func() {
				processed, workerErr := worker.ProcessOrgOnce(ctx, orgID)
				if workerErr == nil && processed != 1 {
					workerErr = fmt.Errorf("processed = %d, want 1", processed)
				}
				workerDone <- workerErr
			}()
			select {
			case <-postClaimRead:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}, func() error {
		materializeCalls++
		store.AddBlockReferenceForTest(orgID, blockID, "up:test")
		close(releasePostClaimRead)
		select {
		case workerErr := <-workerDone:
			if workerErr != nil {
				return workerErr
			}
		case <-ctx.Done():
			return ctx.Err()
		}
		if objectPresent.Load() {
			return errors.New("worker did not delete the physical object")
		}
		if len(store.AllS3Orphans()) != 0 {
			return errors.New("worker fence did not clear before publication")
		}
		// Publish after the complete GC cycle. No fence remains to trigger a
		// retry, so only the mandatory confirmation can repair the bytes.
		store.AddBlock(orgID, blockID, "hot", 0)
		return nil
	}, nil, nil)
	if err != nil {
		t.Fatalf("RetryUploadedBlockMaterialization() error = %v", err)
	}
	if !objectPresent.Load() {
		t.Fatal("confirmation did not restore bytes after unobserved fast clear")
	}
	if storeCalls != 2 || materializeCalls != 1 {
		t.Fatalf("store/materialize calls = %d/%d, want 2/1", storeCalls, materializeCalls)
	}
	if got := store.BlockReferenceCount(orgID, blockID); got != 1 {
		t.Fatalf("block references = %d, want 1", got)
	}
}

func TestRetryUploadedBlockMaterializationDoesNotRetryPermanentFailure(t *testing.T) {
	fastBlockMaterializationRetries(t)

	storeCalls := 0
	materializeCalls := 0
	err := RetryUploadedBlockMaterialization("UploadFile", "block-1", func() error {
		storeCalls++
		return nil
	}, func() error {
		materializeCalls++
		return fmt.Errorf("upsert block metadata: %w", db.ErrBlockMetadataPermanent)
	}, nil, nil)
	if !errors.Is(err, db.ErrBlockMetadataPermanent) {
		t.Fatalf("error = %v, want db.ErrBlockMetadataPermanent", err)
	}
	if storeCalls != 1 || materializeCalls != 1 {
		t.Fatalf("store/materialize calls = %d/%d, want 1/1 (no retry)", storeCalls, materializeCalls)
	}
}

// TestRetryUploadedBlockMaterializationLabelsReasonByPhase is the direct F14
// regression: a write failure in the materialize phase is labeled
// "materialization", never "probe"; a store-phase transient is "probe"; and a
// fence in either phase is "gc_fence".
func TestRetryUploadedBlockMaterializationLabelsReasonByPhase(t *testing.T) {
	fastBlockMaterializationRetries(t)

	t.Run("store-phase transient is probe", func(t *testing.T) {
		const surface = "TestReasonProbe"
		before := retryReasonCount(surface, blockMaterializationReasonProbe)
		calls := 0
		err := RetryUploadedBlockMaterialization(surface, "block-1", func() error {
			calls++
			if calls == 1 {
				return fmt.Errorf("probe read: %w", ErrBlockMaterializationTransient)
			}
			return nil
		}, func() error { return nil }, nil, nil)
		if err != nil {
			t.Fatalf("error = %v, want nil", err)
		}
		if got := retryReasonCount(surface, blockMaterializationReasonProbe) - before; got != 1 {
			t.Fatalf("probe retries = %v, want 1", got)
		}
		if got := retryReasonCount(surface, blockMaterializationReasonMaterial); got != 0 {
			t.Fatalf("materialization retries = %v, want 0 (write not mislabeled as read)", got)
		}
	})

	t.Run("materialize-phase transient is materialization", func(t *testing.T) {
		const surface = "TestReasonMaterialize"
		beforeMat := retryReasonCount(surface, blockMaterializationReasonMaterial)
		beforeProbe := retryReasonCount(surface, blockMaterializationReasonProbe)
		calls := 0
		err := RetryUploadedBlockMaterialization(surface, "block-1", func() error { return nil }, func() error {
			calls++
			if calls == 1 {
				return fmt.Errorf("metadata write: %w", ErrBlockMaterializationTransient)
			}
			return nil
		}, nil, nil)
		if err != nil {
			t.Fatalf("error = %v, want nil", err)
		}
		if got := retryReasonCount(surface, blockMaterializationReasonMaterial) - beforeMat; got != 1 {
			t.Fatalf("materialization retries = %v, want 1", got)
		}
		if got := retryReasonCount(surface, blockMaterializationReasonProbe) - beforeProbe; got != 0 {
			t.Fatalf("probe retries = %v, want 0 (write must not be labeled as read)", got)
		}
	})

	t.Run("fence in materialize phase is gc_fence", func(t *testing.T) {
		const surface = "TestReasonFence"
		before := retryReasonCount(surface, blockMaterializationReasonFence)
		calls := 0
		err := RetryUploadedBlockMaterialization(surface, "block-1", func() error { return nil }, func() error {
			calls++
			if calls == 1 {
				return ErrBlockDeleteInProgress
			}
			return nil
		}, nil, nil)
		if err != nil {
			t.Fatalf("error = %v, want nil", err)
		}
		if got := retryReasonCount(surface, blockMaterializationReasonFence) - before; got != 1 {
			t.Fatalf("gc_fence retries = %v, want 1", got)
		}
		if got := retryReasonCount(surface, blockMaterializationReasonMaterial); got != 0 {
			t.Fatalf("materialization retries = %v, want 0 (fence is not a plain transient)", got)
		}
	})
}

func TestRetryUploadedBlockMaterializationContextAbortsBackoff(t *testing.T) {
	fastBlockMaterializationRetries(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled: the first backoff wait must abort immediately.

	storeCalls := 0
	err := RetryUploadedBlockMaterializationContext(ctx, "UploadFile", "block-1", func() error {
		storeCalls++
		return ErrBlockMaterializationTransient
	}, func() error {
		t.Fatal("materialize must not run when store keeps failing")
		return nil
	}, nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if storeCalls != 1 {
		t.Fatalf("storeCalls = %d, want 1 (budget not exhausted after cancel)", storeCalls)
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
	wantKey := canonicalStore.StorageKeyForHash(uploadReuseTestBlockID) + ".8f14e45f-ea4d-4f73-9f7c-63f4e7a5bc21"
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

	key, err := EnsureReusableBlockPresent(context.Background(), uploadReuseTestBlockID, db.BlockReuseProbe{
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
	blockID := uploadReuseTestBlockID
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
		StorageKey:   wantKey,
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

	_, err = EnsureReusableBlockPresent(context.Background(), uploadReuseTestBlockID, db.BlockReuseProbe{
		Decision:     db.BlockReuseReusable,
		StorageClass: "hot-s3",
		StorageKey:   "blocks/ab/cd/" + uploadReuseTestBlockID,
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

func TestResolveCanonicalBlockStoreRejectsInexactFallbackClass(t *testing.T) {
	const orgID = "3fa85f64-5717-4562-b3fc-2c963f66afa6"
	fallback, err := storage.NewOrgBlockStore(&storage.S3Store{}, "blocks/", orgID)
	if err != nil {
		t.Fatal(err)
	}

	for _, fallbackClass := range []string{"", "   ", "CANONICAL"} {
		t.Run(fmt.Sprintf("class_%q", fallbackClass), func(t *testing.T) {
			got, err := ResolveCanonicalBlockStore(nil, fallback, fallbackClass, "canonical", orgID)
			if got != nil || err == nil {
				t.Fatalf("ResolveCanonicalBlockStore() = (%p, %v), want nil and error", got, err)
			}
		})
	}
}

func TestResolveCanonicalBlockStoreRejectsNonCanonicalFallbackIdentity(t *testing.T) {
	const orgID = "3fa85f64-5717-4562-b3fc-2c963f66afa6"
	fallback, err := storage.NewOrgBlockStore(&storage.S3Store{}, "blocks/", orgID)
	if err != nil {
		t.Fatal(err)
	}

	for _, class := range []string{"CANONICAL", " canonical", "canonical ", "canonical_v1"} {
		t.Run(fmt.Sprintf("class_%q", class), func(t *testing.T) {
			got, err := ResolveCanonicalBlockStore(nil, fallback, class, class, orgID)
			if got != nil || err == nil {
				t.Fatalf("ResolveCanonicalBlockStore() = (%p, %v), want non-canonical identity rejection", got, err)
			}
		})
	}
}

func TestResolveNeedsPutBlockStoreUsesPreferredPlacementForFirstWriter(t *testing.T) {
	const orgID = "3fa85f64-5717-4562-b3fc-2c963f66afa6"
	preferred, err := storage.NewOrgBlockStore(&storage.S3Store{}, "blocks/", orgID)
	if err != nil {
		t.Fatal(err)
	}

	gotStore, gotClass, gotKey, err := ResolveNeedsPutBlockStore(nil, preferred, "preferred", db.BlockReuseProbe{Decision: db.BlockReuseNeedsPut}, orgID, "abcd1234")
	if err != nil {
		t.Fatalf("ResolveNeedsPutBlockStore() error = %v", err)
	}
	if gotStore != preferred || gotClass != "preferred" || gotKey != preferred.StorageKeyForHash("abcd1234") {
		t.Fatalf("placement = %p/%q/%q, want preferred/%q/%q", gotStore, gotClass, gotKey, "preferred", preferred.StorageKeyForHash("abcd1234"))
	}
}

// The first writer MINTS the block's physical identity -- the class this returns is
// the one persisted. It is the last write-path door a non-canonical label could
// enter through, and it must refuse rather than normalize: a trim here would turn
// the write funnel's hard refusal into a silent rewrite, and it would do so AFTER
// the PUT, leaving bytes in S3 that no row ends up pointing at.
func TestResolveNeedsPutBlockStoreRefusesNonCanonicalFirstWriterClass(t *testing.T) {
	const orgID = "3fa85f64-5717-4562-b3fc-2c963f66afa6"
	preferred, err := storage.NewOrgBlockStore(&storage.S3Store{}, "blocks/", orgID)
	if err != nil {
		t.Fatal(err)
	}

	for _, preferredClass := range []string{" hot-v1", "hot-v1 ", " hot-v1 ", "Hot-V1", "hot_v1", "hot--v1", "   "} {
		gotStore, gotClass, gotKey, err := ResolveNeedsPutBlockStore(nil, preferred, preferredClass,
			db.BlockReuseProbe{Decision: db.BlockReuseNeedsPut}, orgID, "abcd1234")
		if err == nil {
			t.Fatalf("preferred class %q: placement = %p/%q/%q, want refusal", preferredClass, gotStore, gotClass, gotKey)
		}
		if gotStore != nil || gotClass != "" || gotKey != "" {
			t.Fatalf("preferred class %q: refusal must return no placement, got %p/%q/%q", preferredClass, gotStore, gotClass, gotKey)
		}
	}
}

// An existing canonical row is immutable placement state. With no persisted
// locator there is nothing to place against, and deriving a replacement is the
// authority P1 removed — so this refuses instead of falling back to the hash.
func TestResolveNeedsPutBlockStoreRefusesExistingRowWithoutPersistedKey(t *testing.T) {
	oldResolve := resolveCanonicalBlockStoreFn
	t.Cleanup(func() { resolveCanonicalBlockStoreFn = oldResolve })

	const orgID = "3fa85f64-5717-4562-b3fc-2c963f66afa6"
	canonical, err := storage.NewOrgBlockStore(&storage.S3Store{}, "blocks/", orgID)
	if err != nil {
		t.Fatal(err)
	}
	resolveCanonicalBlockStoreFn = func(*storage.Manager, *storage.BlockStore, string, string, string) (*storage.BlockStore, error) {
		return canonical, nil
	}

	for _, storageKey := range []string{"", "   "} {
		gotStore, gotClass, gotKey, err := ResolveNeedsPutBlockStore(nil, canonical, "preferred", db.BlockReuseProbe{
			Decision: db.BlockReuseNeedsPut, StorageClass: "archive", StorageKey: storageKey,
		}, orgID, "abcd1234")
		if err == nil || !strings.Contains(err.Error(), "empty persisted storage key") {
			t.Fatalf("storage key %q: error = %v, want empty persisted key refusal", storageKey, err)
		}
		if gotStore != nil || gotClass != "" || gotKey != "" {
			t.Fatalf("storage key %q: refusal must return no placement, got %p/%q/%q", storageKey, gotStore, gotClass, gotKey)
		}
	}
}

func TestResolveNeedsPutBlockStoreUsesExistingCanonicalPlacement(t *testing.T) {
	oldResolve := resolveCanonicalBlockStoreFn
	t.Cleanup(func() { resolveCanonicalBlockStoreFn = oldResolve })

	const orgID = "3fa85f64-5717-4562-b3fc-2c963f66afa6"
	preferred, err := storage.NewOrgBlockStore(&storage.S3Store{}, "blocks/", orgID)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := storage.NewOrgBlockStore(&storage.S3Store{}, "blocks/", orgID)
	if err != nil {
		t.Fatal(err)
	}
	wantKey := canonical.StorageKeyForHash(uploadReuseTestBlockID) + ".8f14e45f-ea4d-4f73-9f7c-63f4e7a5bc21"
	resolveCanonicalBlockStoreFn = func(_ *storage.Manager, fallback *storage.BlockStore, fallbackClass, canonicalClass, gotOrgID string) (*storage.BlockStore, error) {
		if fallback != preferred || fallbackClass != "preferred" || canonicalClass != "archive" || gotOrgID != orgID {
			t.Fatalf("resolve args = %p/%q/%q/%q", fallback, fallbackClass, canonicalClass, gotOrgID)
		}
		return canonical, nil
	}

	gotStore, gotClass, gotKey, err := ResolveNeedsPutBlockStore(nil, preferred, "preferred", db.BlockReuseProbe{
		Decision: db.BlockReuseNeedsPut, StorageClass: "archive", StorageKey: wantKey,
	}, orgID, uploadReuseTestBlockID)
	if err != nil {
		t.Fatalf("ResolveNeedsPutBlockStore() error = %v", err)
	}
	if gotStore != canonical || gotClass != "archive" || gotKey != wantKey {
		t.Fatalf("placement = %p/%q/%q, want canonical/archive/%q", gotStore, gotClass, gotKey, wantKey)
	}
}

func TestStoreUploadedBlockForProbeCanonicalFailuresDoNotPut(t *testing.T) {
	oldResolve := resolveCanonicalBlockStoreFn
	oldPut := repairCanonicalBlockDirectFn
	t.Cleanup(func() {
		resolveCanonicalBlockStoreFn = oldResolve
		repairCanonicalBlockDirectFn = oldPut
	})

	putCalls := 0
	repairCanonicalBlockDirectFn = func(context.Context, *storage.BlockStore, string, []byte) (string, error) {
		putCalls++
		return "", nil
	}

	t.Run("first writer empty preferred class", func(t *testing.T) {
		_, _, _, err := StoreUploadedBlockForProbe(context.Background(), "block-1", db.BlockReuseProbe{
			Decision: db.BlockReuseNeedsPut,
		}, []byte("data"), nil, &storage.BlockStore{}, "  ", "org-1", nil)
		if err == nil || putCalls != 0 {
			t.Fatalf("error/putCalls = %v/%d, want error/0", err, putCalls)
		}
	})

	t.Run("unavailable canonical class has no fallback", func(t *testing.T) {
		resolveCanonicalBlockStoreFn = func(*storage.Manager, *storage.BlockStore, string, string, string) (*storage.BlockStore, error) {
			return nil, errors.New("class unavailable")
		}
		_, _, _, err := StoreUploadedBlockForProbe(context.Background(), "block-1", db.BlockReuseProbe{
			Decision: db.BlockReuseNeedsPut, StorageClass: "archive",
		}, []byte("data"), nil, &storage.BlockStore{}, "preferred", "org-1", nil)
		if err == nil || putCalls != 0 {
			t.Fatalf("error/putCalls = %v/%d, want error/0", err, putCalls)
		}
	})

	t.Run("existing canonical row with no persisted key", func(t *testing.T) {
		const orgID = "3fa85f64-5717-4562-b3fc-2c963f66afa6"
		canonical, err := storage.NewOrgBlockStore(&storage.S3Store{}, "blocks/", orgID)
		if err != nil {
			t.Fatal(err)
		}
		resolveCanonicalBlockStoreFn = func(*storage.Manager, *storage.BlockStore, string, string, string) (*storage.BlockStore, error) {
			return canonical, nil
		}
		for _, decision := range []db.BlockReuseDecision{db.BlockReuseNeedsPut, db.BlockReuseReusable} {
			_, _, _, err := StoreUploadedBlockForProbe(context.Background(), uploadReuseTestBlockID, db.BlockReuseProbe{
				Decision: decision, StorageClass: "archive", StorageKey: "   ",
			}, []byte("data"), nil, canonical, "preferred", orgID, nil)
			if err == nil || !strings.Contains(err.Error(), "empty persisted storage key") {
				t.Fatalf("decision %v: error = %v, want empty persisted key refusal", decision, err)
			}
			if putCalls != 0 {
				t.Fatalf("decision %v: putCalls = %d, want 0", decision, putCalls)
			}
		}
	})

	t.Run("cross-org key mismatch", func(t *testing.T) {
		const orgID = "3fa85f64-5717-4562-b3fc-2c963f66afa6"
		canonical, err := storage.NewOrgBlockStore(&storage.S3Store{}, "blocks/", orgID)
		if err != nil {
			t.Fatal(err)
		}
		resolveCanonicalBlockStoreFn = func(*storage.Manager, *storage.BlockStore, string, string, string) (*storage.BlockStore, error) {
			return canonical, nil
		}
		_, _, _, err = StoreUploadedBlockForProbe(context.Background(), uploadReuseTestBlockID, db.BlockReuseProbe{
			Decision: db.BlockReuseNeedsPut, StorageClass: "archive", StorageKey: "blocks/00000000-0000-0000-0000-000000000001/ab/cd/abcd1234",
		}, []byte("data"), nil, canonical, "preferred", orgID, nil)
		if err == nil || putCalls != 0 {
			t.Fatalf("error/putCalls = %v/%d, want mismatch error/0", err, putCalls)
		}
	})
}

func TestStoreUploadedBlockForProbeReturnsCanonicalPlacementAndFence(t *testing.T) {
	oldResolve := resolveCanonicalBlockStoreFn
	oldPut := repairCanonicalBlockDirectFn
	t.Cleanup(func() {
		resolveCanonicalBlockStoreFn = oldResolve
		repairCanonicalBlockDirectFn = oldPut
	})

	const orgID = "3fa85f64-5717-4562-b3fc-2c963f66afa6"
	canonical, err := storage.NewOrgBlockStore(&storage.S3Store{}, "blocks/", orgID)
	if err != nil {
		t.Fatal(err)
	}
	wantKey := canonical.StorageKeyForHash(uploadReuseTestBlockID) + ".8f14e45f-ea4d-4f73-9f7c-63f4e7a5bc21"
	resolveCanonicalBlockStoreFn = func(*storage.Manager, *storage.BlockStore, string, string, string) (*storage.BlockStore, error) {
		return canonical, nil
	}
	putCalls := 0
	admissionCalls := 0
	repairCanonicalBlockDirectFn = func(_ context.Context, gotStore *storage.BlockStore, gotKey string, data []byte) (string, error) {
		putCalls++
		if gotStore != canonical || gotKey != wantKey || string(data) != "data" {
			t.Fatalf("PUT = %p/%q/%q", gotStore, gotKey, data)
		}
		return gotKey, nil
	}

	gotKey, gotClass, didPut, err := StoreUploadedBlockForProbe(context.Background(), uploadReuseTestBlockID, db.BlockReuseProbe{
		Decision: db.BlockReuseNeedsPut, StorageClass: "archive", StorageKey: wantKey,
	}, []byte("data"), nil, canonical, "preferred", orgID, func() error {
		admissionCalls++
		return nil
	})
	if err != nil || gotKey != wantKey || gotClass != "archive" || !didPut || putCalls != 1 || admissionCalls != 1 {
		t.Fatalf("result = %q/%q/%v/%v puts=%d admission=%d", gotKey, gotClass, didPut, err, putCalls, admissionCalls)
	}

	_, _, _, err = StoreUploadedBlockForProbe(context.Background(), uploadReuseTestBlockID, db.BlockReuseProbe{Decision: db.BlockReuseBlockedByGC}, []byte("data"), nil, canonical, "preferred", orgID, nil)
	if !errors.Is(err, ErrBlockDeleteInProgress) || putCalls != 1 || admissionCalls != 1 {
		t.Fatalf("fence error/put/admission = %v/%d/%d, want ErrBlockDeleteInProgress/1/1", err, putCalls, admissionCalls)
	}
}

func TestStoreUploadedBlockForProbePreservesTransientStorageCause(t *testing.T) {
	oldResolve := resolveCanonicalBlockStoreFn
	oldExists := reusableCanonicalObjectExistsFn
	t.Cleanup(func() {
		resolveCanonicalBlockStoreFn = oldResolve
		reusableCanonicalObjectExistsFn = oldExists
	})

	canonical := &storage.BlockStore{}
	resolveCanonicalBlockStoreFn = func(*storage.Manager, *storage.BlockStore, string, string, string) (*storage.BlockStore, error) {
		return canonical, nil
	}
	reusableCanonicalObjectExistsFn = func(context.Context, *storage.BlockStore, string) (bool, error) {
		return false, context.Canceled
	}

	_, _, _, err := StoreUploadedBlockForProbe(context.Background(), uploadReuseTestBlockID, db.BlockReuseProbe{
		Decision: db.BlockReuseReusable, StorageClass: "archive", StorageKey: canonical.StorageKeyForHash(uploadReuseTestBlockID),
	}, []byte("data"), nil, canonical, "preferred", "org-1", nil)
	if !errors.Is(err, ErrBlockMaterializationTransient) || !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want transient sentinel and context cancellation cause", err)
	}
}

func TestRetryUploadedBlockMaterializationResetsPlacementPerAttempt(t *testing.T) {
	fastBlockMaterializationRetries(t)

	const orgID = "3fa85f64-5717-4562-b3fc-2c963f66afa6"
	preferred, err := storage.NewOrgBlockStore(&storage.S3Store{}, "blocks/", orgID)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := storage.NewOrgBlockStore(&storage.S3Store{}, "blocks/", orgID)
	if err != nil {
		t.Fatal(err)
	}
	oldResolve := resolveCanonicalBlockStoreFn
	t.Cleanup(func() { resolveCanonicalBlockStoreFn = oldResolve })
	resolveCanonicalBlockStoreFn = func(*storage.Manager, *storage.BlockStore, string, string, string) (*storage.BlockStore, error) {
		return canonical, nil
	}

	placementClass := "preferred"
	attempt := 0
	materializeCalls := 0
	err = RetryUploadedBlockMaterialization("PlacementReset", uploadReuseTestBlockID, func() error {
		attempt++
		placementClass = "preferred"
		probe := db.BlockReuseProbe{Decision: db.BlockReuseNeedsPut}
		if attempt == 1 {
			probe.StorageClass = "archive"
		}
		probe.StorageKey = canonical.StorageKeyForHash(uploadReuseTestBlockID)
		_, resolvedClass, _, resolveErr := ResolveNeedsPutBlockStore(nil, preferred, "preferred", probe, orgID, uploadReuseTestBlockID)
		if resolveErr == nil {
			placementClass = resolvedClass
		}
		return resolveErr
	}, func() error {
		materializeCalls++
		if materializeCalls == 1 {
			if placementClass != "archive" {
				t.Fatalf("first attempt class = %q, want archive", placementClass)
			}
			return ErrBlockMaterializationTransient
		}
		if placementClass != "preferred" {
			t.Fatalf("second attempt class = %q, want preferred (no stale canonical class)", placementClass)
		}
		return nil
	}, nil, nil)
	if err != nil {
		t.Fatalf("RetryUploadedBlockMaterialization() error = %v", err)
	}
	if attempt != 3 || materializeCalls != 2 {
		t.Fatalf("store/materialize calls = %d/%d, want 3/2", attempt, materializeCalls)
	}
}
