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

var putUploadedBlockAutoDirectFn = func(ctx context.Context, blockStore *storage.BlockStore, hash string, data []byte) (string, error) {
	return blockStore.PutBlockAutoDirect(ctx, hash, data)
}
var resolveCanonicalBlockStoreFn = ResolveCanonicalBlockStore
var reusableCanonicalObjectExistsFn = func(ctx context.Context, blockStore *storage.BlockStore, storageKey string) (bool, error) {
	return blockStore.ObjectExists(ctx, storageKey)
}
var repairCanonicalBlockDirectFn = func(ctx context.Context, blockStore *storage.BlockStore, storageKey string, data []byte) (string, error) {
	return blockStore.PutObjectAutoDirect(ctx, storageKey, data)
}

// ProbeUploadedBlockReuse wraps the DB probe so callers can fail open to legacy
// storage behavior when no Cassandra session is available.
func ProbeUploadedBlockReuse(database *db.DB, orgID, blockID string) (db.BlockReuseProbe, error) {
	if database == nil || database.Session() == nil {
		return db.BlockReuseProbe{Decision: db.BlockReuseUnknownError}, fmt.Errorf("block reuse probe unavailable for %s: database session is nil", blockID)
	}
	return database.ProbeBlockReuse(orgID, blockID)
}

// ResolveCanonicalBlockStore resolves the exact canonical backend for a block.
// It does not apply health failover because the caller is verifying or repairing
// the physical location that Cassandra has already declared canonical.
func ResolveCanonicalBlockStore(storageManager *storage.Manager, fallbackStore *storage.BlockStore, fallbackClass, canonicalClass string) (*storage.BlockStore, error) {
	canonicalClass = strings.TrimSpace(canonicalClass)
	if canonicalClass == "" {
		return nil, errors.New("canonical storage class is empty")
	}
	if storageManager != nil {
		return storageManager.GetBlockStore(canonicalClass)
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
// a Cassandra-reusable block and repairs it in place when it is missing.
func EnsureReusableBlockPresent(ctx context.Context, blockID string, probe db.BlockReuseProbe, data []byte, storageManager *storage.Manager, fallbackStore *storage.BlockStore, fallbackClass string) (string, error) {
	if probe.Decision != db.BlockReuseReusable {
		return "", fmt.Errorf("block %s is not reusable", blockID)
	}

	canonicalStore, err := resolveCanonicalBlockStoreFn(storageManager, fallbackStore, fallbackClass, probe.StorageClass)
	if err != nil {
		return "", fmt.Errorf("resolve canonical block store for %s: %w", blockID, err)
	}

	storageKey := strings.TrimSpace(probe.StorageKey)
	if storageKey == "" {
		storageKey = canonicalStore.StorageKeyForHash(blockID)
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
