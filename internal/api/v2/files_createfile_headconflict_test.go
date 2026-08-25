package v2

import (
	"context"
	"fmt"
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
)

// TestCreateFileTemplateTargetSurvivesHeadConflictWithoutRemintOrPut proves that a
// library HEAD conflict after a successful canonical install does NOT resubmit the
// same single-use INSTALL identity.
//
// CreateFile caches templateBlockStored/templateTarget OUTSIDE retryLibraryHeadMutation,
// so both survive an outer conflict retry, and the retry's store callback short-circuits
// on templateBlockStored. Were the cached target still FreshInstall=true, the second
// register() would resubmit K1 install identity -- exactly what R24 forbids -- and the
// DB would reject it as an identity contradiction, failing a CreateFile whose block had
// been installed correctly.
//
// What actually prevents that is the confirmation re-probe: the materialization helper
// calls resetStored() before the confirmation phase, so the confirmation store re-probes
// and REBUILDS the target from the now-canonical row, and probe-derived targets carry no
// install authority. The previous version of this test hand-wrote that transition in its
// own stub, so it asserted the intended behavior rather than the shipped one and would
// have stayed green if production regressed. This version drives the production target
// builders -- ResolveNeedsPutBlockStoreForPhase and EnsureReusableBlockPresentForPhase --
// so it fails if the reset, the re-probe, or the phase handling regresses.
func TestCreateFileTemplateTargetSurvivesHeadConflictWithoutRemintOrPut(t *testing.T) {
	fastBlockMaterializationRetries(t)
	oldHeadDelay := libraryHeadMutationRetryDelay
	oldHeadJitter := libraryHeadMutationRetryJitter
	t.Cleanup(func() {
		libraryHeadMutationRetryDelay = oldHeadDelay
		libraryHeadMutationRetryJitter = oldHeadJitter
	})
	libraryHeadMutationRetryDelay = 0
	libraryHeadMutationRetryJitter = 0

	orgID := "11111111-1111-1111-1111-111111111111"
	blockStore, err := storage.NewOrgBlockStore(&storage.S3Store{}, "blocks/", orgID)
	if err != nil {
		t.Fatalf("build org block store: %v", err)
	}

	// canonicalKey stays empty until the install applies. After that the probe reports
	// the block as canonical, which is what the confirmation phase observes.
	canonicalKey := ""

	oldProbe := probeUploadedBlockReuseFn
	oldPrepare := prepareUploadedBlockProbeFn
	oldResolveCanonical := resolveCanonicalBlockStoreFn
	oldExists := reusableCanonicalObjectExistsFn
	t.Cleanup(func() {
		probeUploadedBlockReuseFn = oldProbe
		prepareUploadedBlockProbeFn = oldPrepare
		resolveCanonicalBlockStoreFn = oldResolveCanonical
		reusableCanonicalObjectExistsFn = oldExists
	})

	probeUploadedBlockReuseFn = func(*db.DB, string, string) (db.BlockReuseProbe, error) {
		if canonicalKey == "" {
			return db.BlockReuseProbe{Decision: db.BlockReuseNeedsPut}, nil
		}
		return db.BlockReuseProbe{Decision: db.BlockReuseReusable, StorageClass: "hot", StorageKey: canonicalKey}, nil
	}
	prepareUploadedBlockProbeFn = func(_ *db.DB, _, _ string, probe db.BlockReuseProbe) (db.BlockReuseProbe, error) {
		return probe, nil
	}
	resolveCanonicalBlockStoreFn = func(*storage.Manager, *storage.BlockStore, string, string, string) (*storage.BlockStore, error) {
		return blockStore, nil
	}
	reusableCanonicalObjectExistsFn = func(context.Context, *storage.BlockStore, string) (bool, error) {
		return true, nil
	}

	putCalls, registerCalls, outerAttempts := 0, 0, 0
	mintedKeys := map[string]bool{}
	var registeredTargets []BlockMaterializationTarget

	var templateBlockStored bool
	var templateTarget BlockMaterializationTarget

	// This mirrors CreateFile own callback structure: cached state declared outside the
	// head-mutation retry, a store callback that short-circuits on the cache, and a
	// resetStored that clears both.
	err = retryLibraryHeadMutation("CreateFile", func() error {
		outerAttempts++
		if materializeErr := retryCreateFileTemplateBlockMaterializationPhased(func(phase BlockMaterializationPhase) error {
			if templateBlockStored {
				return nil
			}
			probe, probeErr := probeUploadedBlockReuseFn(nil, orgID, uploadReuseTestBlockID)
			if probeErr != nil {
				return probeErr
			}
			probe, probeErr = prepareUploadedBlockProbeFn(nil, orgID, uploadReuseTestBlockID, probe)
			if probeErr != nil {
				return probeErr
			}
			switch probe.Decision {
			case db.BlockReuseReusable:
				storageKey, ensureErr := EnsureReusableBlockPresentForPhase(context.Background(), uploadReuseTestBlockID, probe, nil, nil, blockStore, "hot", orgID, phase)
				if ensureErr != nil {
					return ensureErr
				}
				templateTarget = BlockMaterializationTarget{StorageClass: probe.StorageClass, StorageKey: storageKey}
				templateBlockStored = true
				return nil
			case db.BlockReuseNeedsPut:
				target, resolveErr := ResolveNeedsPutBlockStoreForPhase(nil, blockStore, "hot", probe, orgID, uploadReuseTestBlockID, phase)
				if resolveErr != nil {
					return resolveErr
				}
				templateTarget = target
				mintedKeys[target.StorageKey] = true
				putCalls++
				templateBlockStored = true
				return nil
			}
			return fmt.Errorf("unexpected decision %d", probe.Decision)
		}, func() error {
			registerCalls++
			registeredTargets = append(registeredTargets, templateTarget)
			canonicalKey = templateTarget.StorageKey
			return nil
		}, func() {
			templateBlockStored = false
			templateTarget = BlockMaterializationTarget{}
		}); materializeErr != nil {
			return materializeErr
		}
		if outerAttempts == 1 {
			return ErrLibraryHeadConflict
		}
		return nil
	})
	if err != nil {
		t.Fatalf("forced-conflict CreateFile materialization: %v", err)
	}

	if outerAttempts != 2 || registerCalls != 2 {
		t.Fatalf("attempts/registers = %d/%d, want 2/2", outerAttempts, registerCalls)
	}
	if putCalls != 1 || len(mintedKeys) != 1 {
		t.Fatalf("PUTs/minted keys = %d/%d, want 1/1; the conflict retry must not mint or re-PUT", putCalls, len(mintedKeys))
	}
	if !registeredTargets[0].FreshInstall {
		t.Fatalf("first registration = %+v, want fresh install authority", registeredTargets[0])
	}
	if registeredTargets[1].FreshInstall {
		t.Fatalf("second registration = %+v, want FreshInstall=false; resubmitting a single-use install identity is exactly what R24 forbids", registeredTargets[1])
	}
	if registeredTargets[0].StorageKey != registeredTargets[1].StorageKey {
		t.Fatalf("registration keys = %q then %q, want the same canonical key", registeredTargets[0].StorageKey, registeredTargets[1].StorageKey)
	}
}

// TestCreateFileTemplateMaterializationRequiresResetCallback pins that the reset is
// mandatory rather than an optional optimization. It is what re-probes after
// registration and rebuilds the target without fresh-install authority; with a nil
// callback the confirmation store short-circuits on the cached state, the cached
// target keeps FreshInstall=true, and a later HEAD conflict resubmits a single-use
// install identity. Without this check that regression would be a silent nil.
func TestCreateFileTemplateMaterializationRequiresResetCallback(t *testing.T) {
	fastBlockMaterializationRetries(t)

	storeCalls, registerCalls := 0, 0
	err := retryCreateFileTemplateBlockMaterializationPhased(func(BlockMaterializationPhase) error {
		storeCalls++
		return nil
	}, func() error {
		registerCalls++
		return nil
	}, nil)

	if err == nil {
		t.Fatal("error = nil, want a caller-bug rejection for a nil reset callback")
	}
	if storeCalls != 1 || registerCalls != 1 {
		t.Errorf("store/register calls = %d/%d, want 1/1 before the rejection", storeCalls, registerCalls)
	}
	if IsRetryableBlockMaterializationError(err) {
		t.Errorf("error = %v is retryable; a caller bug must not be retried", err)
	}
}
