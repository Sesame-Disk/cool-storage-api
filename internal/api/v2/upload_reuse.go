package v2

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

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

// ProbeUploadedBlockReuse wraps the DB probe. Metadata-governed callers fail
// closed on an error because neither canonical placement nor delete fences are
// trustworthy without the complete decision.
func ProbeUploadedBlockReuse(database *db.DB, orgID, blockID string) (db.BlockReuseProbe, error) {
	if database == nil || database.Session() == nil {
		return db.BlockReuseProbe{Decision: db.BlockReuseUnknownError}, fmt.Errorf("block reuse probe unavailable for %s: database session is nil", blockID)
	}
	return database.ProbeBlockReuse(orgID, blockID)
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

// ResolveNeedsPutBlockStore chooses the backend for a NeedsPut probe. Empty
// probe metadata means this request is the first writer and may use its preferred
// backend. Existing immutable metadata must always be repaired in its canonical
// backend so reads and GC continue to address the same physical key.
func ResolveNeedsPutBlockStore(storageManager *storage.Manager, fallbackStore *storage.BlockStore, fallbackClass string, probe db.BlockReuseProbe, orgID, blockID string) (*storage.BlockStore, string, string, error) {
	if probe.Decision != db.BlockReuseNeedsPut {
		return nil, "", "", fmt.Errorf("block %s does not need a PUT", blockID)
	}

	canonicalClass := strings.TrimSpace(probe.StorageClass)
	if canonicalClass == "" {
		if fallbackStore == nil {
			return nil, "", "", fmt.Errorf("preferred block store is unavailable for %s", blockID)
		}
		return fallbackStore, strings.TrimSpace(fallbackClass), fallbackStore.StorageKeyForHash(blockID), nil
	}

	canonicalStore, err := resolveCanonicalBlockStoreFn(storageManager, fallbackStore, fallbackClass, canonicalClass, orgID)
	if err != nil {
		return nil, "", "", fmt.Errorf("resolve canonical block store for %s: %w", blockID, err)
	}
	storageKey := canonicalStore.StorageKeyForHash(blockID)
	if storedKey := strings.TrimSpace(probe.StorageKey); storedKey != "" && storedKey != storageKey {
		return nil, "", "", fmt.Errorf("canonical block %s storage key %q does not match derived org-scoped key %q", blockID, storedKey, storageKey)
	}
	return canonicalStore, canonicalClass, storageKey, nil
}

func canonicalBlockPresence(ctx context.Context, blockID string, probe db.BlockReuseProbe, storageManager *storage.Manager, fallbackStore *storage.BlockStore, fallbackClass, orgID string) (*storage.BlockStore, string, bool, error) {
	if probe.Decision != db.BlockReuseReusable {
		return nil, "", false, fmt.Errorf("block %s is not reusable", blockID)
	}
	canonicalStore, err := resolveCanonicalBlockStoreFn(storageManager, fallbackStore, fallbackClass, probe.StorageClass, orgID)
	if err != nil {
		return nil, "", false, fmt.Errorf("resolve canonical block store for %s: %w", blockID, err)
	}
	storageKey := canonicalStore.StorageKeyForHash(blockID)
	if storedKey := strings.TrimSpace(probe.StorageKey); storedKey != "" && storedKey != storageKey {
		return nil, "", false, fmt.Errorf("canonical block %s storage key %q does not match derived org-scoped key %q", blockID, storedKey, storageKey)
	}
	exists, err := reusableCanonicalObjectExistsFn(ctx, canonicalStore, storageKey)
	if err != nil {
		return nil, "", false, fmt.Errorf("verify canonical block %s in %s: %w", blockID, probe.StorageClass, err)
	}
	return canonicalStore, storageKey, exists, nil
}

// CanonicalBlockExistsForProbe verifies a reusable block in the exact backend
// and org-scoped key declared by its immutable metadata.
func CanonicalBlockExistsForProbe(ctx context.Context, blockID string, probe db.BlockReuseProbe, storageManager *storage.Manager, fallbackStore *storage.BlockStore, fallbackClass, orgID string) (bool, error) {
	_, _, exists, err := canonicalBlockPresence(ctx, blockID, probe, storageManager, fallbackStore, fallbackClass, orgID)
	return exists, err
}

// StoreUploadedBlockForProbe executes the physical part of a Cassandra-first
// upload decision. beforePut runs immediately before a real write and can be
// used for one-time admission or reservation work.
func StoreUploadedBlockForProbe(ctx context.Context, blockID string, probe db.BlockReuseProbe, data []byte, storageManager *storage.Manager, fallbackStore *storage.BlockStore, fallbackClass, orgID string, beforePut func() error) (storageKey, storageClass string, didPut bool, err error) {
	switch probe.Decision {
	case db.BlockReuseReusable:
		canonicalStore, canonicalKey, exists, existsErr := canonicalBlockPresence(ctx, blockID, probe, storageManager, fallbackStore, fallbackClass, orgID)
		storageKey = canonicalKey
		if existsErr != nil {
			return storageKey, probe.StorageClass, false, existsErr
		}
		if exists {
			return storageKey, probe.StorageClass, false, nil
		}
		if beforePut != nil {
			if beforeErr := beforePut(); beforeErr != nil {
				return storageKey, probe.StorageClass, false, beforeErr
			}
		}
		if _, putErr := repairCanonicalBlockDirectFn(ctx, canonicalStore, storageKey, data); putErr != nil {
			return storageKey, probe.StorageClass, false, fmt.Errorf("repair canonical block %s in %s: %w", blockID, probe.StorageClass, putErr)
		}
		return storageKey, probe.StorageClass, true, nil

	case db.BlockReuseNeedsPut:
		putStore, resolvedClass, resolvedKey, resolveErr := ResolveNeedsPutBlockStore(storageManager, fallbackStore, fallbackClass, probe, orgID, blockID)
		if resolveErr != nil {
			return "", "", false, resolveErr
		}
		if beforePut != nil {
			if beforeErr := beforePut(); beforeErr != nil {
				return resolvedKey, resolvedClass, false, beforeErr
			}
		}
		if _, putErr := repairCanonicalBlockDirectFn(ctx, putStore, resolvedKey, data); putErr != nil {
			return resolvedKey, resolvedClass, false, fmt.Errorf("store block %s in %s: %w", blockID, resolvedClass, putErr)
		}
		return resolvedKey, resolvedClass, true, nil

	case db.BlockReuseBlockedByGC:
		return "", "", false, ErrBlockDeleteInProgress
	default:
		return "", "", false, fmt.Errorf("unsupported block reuse decision %d for %s", probe.Decision, blockID)
	}
}

// EnsureReusableBlockPresent verifies that the canonical physical copy exists for
// a Cassandra-reusable block and repairs it in place when it is missing. orgID
// org-scopes the canonical locator (see ResolveCanonicalBlockStore).
func EnsureReusableBlockPresent(ctx context.Context, blockID string, probe db.BlockReuseProbe, data []byte, storageManager *storage.Manager, fallbackStore *storage.BlockStore, fallbackClass, orgID string) (string, error) {
	if probe.Decision != db.BlockReuseReusable {
		return "", fmt.Errorf("block %s is not reusable", blockID)
	}
	storageKey, _, _, err := StoreUploadedBlockForProbe(ctx, blockID, probe, data, storageManager, fallbackStore, fallbackClass, orgID, nil)
	return storageKey, err
}

// RetryUploadedBlockMaterialization retries the full store->materialize cycle
// when GC temporarily fences the block. The retryable sentinel can now surface
// from either phase because Cassandra-first probes may reject a PUT before S3
// work starts.
func RetryUploadedBlockMaterialization(label, blockID string, store func() error, materialize func() error, onRetry func(), resolveFence func() (bool, error)) error {
	return retryUploadedBlockMaterialization(nil, label, blockID, store, materialize, onRetry, resolveFence)
}

// RetryUploadedBlockMaterializationContext is the request-cancellable variant
// used by production handlers. The context aborts only retry backoff; store and
// materialize callbacks remain responsible for propagating it to their I/O.
func RetryUploadedBlockMaterializationContext(ctx context.Context, label, blockID string, store func() error, materialize func() error, onRetry func(), resolveFence func() (bool, error)) error {
	return retryUploadedBlockMaterialization(ctx, label, blockID, store, materialize, onRetry, resolveFence)
}

func retryUploadedBlockMaterialization(ctx context.Context, label, blockID string, store func() error, materialize func() error, onRetry func(), resolveFence func() (bool, error)) error {
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
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return err
			}
			if sleepFor <= 0 {
				return nil
			}
			timer := time.NewTimer(sleepFor)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		} else if sleepFor > 0 {
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
