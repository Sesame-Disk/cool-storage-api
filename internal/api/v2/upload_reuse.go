package v2

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
)

var probeUploadedBlockReuseFn = ProbeUploadedBlockReuse
var prepareUploadedBlockProbeFn = PrepareUploadedBlockProbe

var putUploadedBlockAutoDirectFn = func(ctx context.Context, blockStore *storage.BlockStore, hash string, data []byte) (string, error) {
	return blockStore.PutBlockAutoDirect(ctx, hash, data)
}
var repairReleasedBlockStubForUploadFn = func(database *db.DB, orgID, blockID string) (bool, error) {
	return database.RepairReleasedBlockStub(orgID, blockID)
}
var resolveCanonicalBlockStoreFn = ResolveCanonicalBlockStore
var reusableCanonicalObjectExistsFn = func(ctx context.Context, blockStore *storage.BlockStore, storageKey string) (bool, error) {
	return blockStore.ObjectExists(ctx, storageKey)
}
var repairCanonicalBlockDirectFn = func(ctx context.Context, blockStore *storage.BlockStore, storageKey string, data []byte) (string, error) {
	return blockStore.PutObjectAutoDirect(ctx, storageKey, data)
}

// ProbeUploadedBlockReuse wraps the DB probe. Upload callers fail closed when
// Cassandra cannot establish whether GC owns the physical object.
func ProbeUploadedBlockReuse(database *db.DB, orgID, blockID string) (db.BlockReuseProbe, error) {
	if database == nil || database.Session() == nil {
		return db.BlockReuseProbe{Decision: db.BlockReuseUnknownError}, fmt.Errorf("block reuse probe unavailable for %s: database session is nil", blockID)
	}
	return database.ProbeBlockReuse(orgID, blockID)
}

// PrepareUploadedBlockProbe repairs a released GC claim stub before the caller
// enters its existing NeedsPut branch. A lost CAS is retryable, but the caller
// must not PUT based on the stale probe.
func PrepareUploadedBlockProbe(database *db.DB, orgID, blockID string, probe db.BlockReuseProbe) (db.BlockReuseProbe, error) {
	if probe.Decision != db.BlockReuseRepairableStub {
		return probe, nil
	}
	if database == nil {
		return db.BlockReuseProbe{Decision: db.BlockReuseUnknownError}, fmt.Errorf("block stub repair unavailable for %s: database is nil", blockID)
	}
	repaired, err := repairReleasedBlockStubForUploadFn(database, orgID, blockID)
	if err != nil {
		return db.BlockReuseProbe{Decision: db.BlockReuseUnknownError}, fmt.Errorf("repair released block stub for %s: %w", blockID, err)
	}
	if !repaired {
		return db.BlockReuseProbe{Decision: db.BlockReuseBlockedByGC}, fmt.Errorf("%w: block %s changed before stub repair", ErrBlockDeleteInProgress, blockID)
	}
	probe.Decision = db.BlockReuseNeedsPut
	return probe, nil
}

// ResolveCanonicalBlockStore resolves the exact canonical backend for a block.
// It does not apply health failover because the caller is verifying or repairing
// the physical location that Cassandra has already declared canonical.
//
// orgID org-scopes the physical key so verify/repair target the requesting org's
// object (blocks/<org_id>/...). The fallback store must already be org-scoped by
// the caller.
func ResolveCanonicalBlockStore(storageManager *storage.Manager, fallbackStore *storage.BlockStore, fallbackClass, canonicalClass, orgID string) (*storage.BlockStore, error) {
	canonicalClass = strings.TrimSpace(canonicalClass)
	if canonicalClass == "" {
		return nil, errors.New("canonical storage class is empty")
	}
	if storageManager != nil {
		return storageManager.GetBlockStoreForOrg(orgID, canonicalClass)
	}
	if fallbackStore != nil {
		fallbackClass = strings.TrimSpace(fallbackClass)
		if fallbackClass == "" || strings.EqualFold(fallbackClass, canonicalClass) {
			return fallbackStore, nil
		}
	}
	return nil, fmt.Errorf("canonical storage class %s is not available", canonicalClass)
}

// EnsureReusableBlockPresent verifies that the canonical physical copy exists for
// a Cassandra-reusable block and repairs it in place when it is missing. orgID
// org-scopes the canonical locator (see ResolveCanonicalBlockStore).
func EnsureReusableBlockPresent(ctx context.Context, blockID string, probe db.BlockReuseProbe, data []byte, storageManager *storage.Manager, fallbackStore *storage.BlockStore, fallbackClass, orgID string) (string, error) {
	if probe.Decision != db.BlockReuseReusable {
		return "", fmt.Errorf("block %s is not reusable", blockID)
	}

	canonicalStore, err := resolveCanonicalBlockStoreFn(storageManager, fallbackStore, fallbackClass, probe.StorageClass, orgID)
	if err != nil {
		return "", fmt.Errorf("resolve canonical block store for %s: %w", blockID, err)
	}

	storageKey := canonicalStore.StorageKeyForHash(blockID)
	if storedKey := strings.TrimSpace(probe.StorageKey); storedKey != "" && storedKey != storageKey {
		return "", fmt.Errorf("canonical block %s storage key %q does not match derived org-scoped key %q", blockID, storedKey, storageKey)
	}

	exists, err := reusableCanonicalObjectExistsFn(ctx, canonicalStore, storageKey)
	if err != nil {
		return storageKey, fmt.Errorf("verify canonical block %s in %s: %w", blockID, probe.StorageClass, err)
	}
	if exists {
		return storageKey, nil
	}

	if _, err := repairCanonicalBlockDirectFn(ctx, canonicalStore, storageKey, data); err != nil {
		return storageKey, fmt.Errorf("repair canonical block %s in %s: %w", blockID, probe.StorageClass, err)
	}
	return storageKey, nil
}

// RetryUploadedBlockMaterialization retries the full store->materialize cycle
// when GC temporarily fences the block. The retryable sentinel can now surface
// from either phase because Cassandra-first probes may reject a PUT before S3
// work starts.
func RetryUploadedBlockMaterialization(label, blockID string, store func() error, materialize func() error, onRetry func(), resolveFence func() (bool, error)) error {
	attempts := RetryAttempts()
	if attempts < 1 {
		attempts = 1
	}

	blockSuffix := ""
	if strings.TrimSpace(blockID) != "" {
		blockSuffix = fmt.Sprintf(" for block %s", blockID)
	}

	retryBlocked := func(attempt int) error {
		if onRetry != nil {
			onRetry()
		}
		if resolveFence != nil {
			resolved, resolveErr := resolveFence()
			if resolveErr != nil {
				log.Printf("[%s] failed to inspect S3 orphan fence%s: %v", label, blockSuffix, resolveErr)
			} else if resolved {
				return nil
			}
		}
		sleepFor := RetryBackoff(attempt)
		log.Printf("[%s] block materialization fenced by GC%s; retrying (%d/%d) after %s", label, blockSuffix, attempt, attempts, sleepFor)
		if sleepFor > 0 {
			registerUploadedBlockSleepFn(sleepFor)
		}
		return nil
	}

	for attempt := 1; attempt <= attempts; attempt++ {
		if err := store(); err != nil {
			if !errors.Is(err, ErrBlockDeleteInProgress) || attempt == attempts {
				return err
			}
			if retryErr := retryBlocked(attempt); retryErr != nil {
				return retryErr
			}
			continue
		}
		if err := materialize(); err != nil {
			if !errors.Is(err, ErrBlockDeleteInProgress) || attempt == attempts {
				return err
			}
			if retryErr := retryBlocked(attempt); retryErr != nil {
				return retryErr
			}
			continue
		}
		return nil
	}

	return fmt.Errorf("%w%s", ErrBlockDeleteInProgress, blockSuffix)
}
