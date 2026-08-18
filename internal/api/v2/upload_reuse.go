package v2

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
)

// ErrBlockMaterializationTransient marks a retryable transient failure surfaced
// from the store or materialize phase — a Cassandra/S3 I/O error, a lost CAS
// race, or a still-converging stub/fence row. The store->materialize wrapper
// retries it inside its bounded budget instead of failing the upload on the
// first timeout. Permanent metadata failures (db.ErrBlockMetadataPermanent) are
// deliberately NOT wrapped with it, so they are not retried.
var ErrBlockMaterializationTransient = errors.New("block materialization transient failure")

// IsRetryableBlockMaterializationError reports whether the store->materialize
// wrapper should retry err: a GC delete fence (ErrBlockDeleteInProgress) or a
// tagged transient I/O failure (ErrBlockMaterializationTransient). Anything else —
// including a permanent metadata failure and any untagged raw error — is returned
// to the caller as-is. Store callback behavior is intentionally explicit: the shared
// store helper tags canonical HEAD/repair/direct-PUT failures, while raw probe errors
// and older manual direct-PUT branches remain untagged.
func IsRetryableBlockMaterializationError(err error) bool {
	return errors.Is(err, ErrBlockDeleteInProgress) || errors.Is(err, ErrBlockMaterializationTransient)
}

// Retry reasons for BlockUploadMaterializationRetriesTotal. The reason is chosen
// by the PHASE that failed (which callback returned it), never the sentinel, so a
// materialize-phase metadata write is never labeled "probe" (finding F14).
const (
	blockMaterializationReasonFence    = "gc_fence"
	blockMaterializationReasonProbe    = "probe"           // store phase (probe/HEAD/PUT)
	blockMaterializationReasonMaterial = "materialization" // metadata materialize phase
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
	if !config.IsCanonicalStorageClassName(canonicalClass) {
		if canonicalClass == "" {
			return nil, errors.New("canonical storage class is empty")
		}
		return nil, fmt.Errorf("canonical storage class %q is not canonical", canonicalClass)
	}
	if storageManager != nil {
		return storageManager.GetBlockStoreForOrg(orgID, canonicalClass)
	}
	if fallbackStore != nil {
		if fallbackClass != "" && fallbackClass == canonicalClass {
			return fallbackStore, nil
		}
	}
	return nil, fmt.Errorf("canonical storage class %s is not available", canonicalClass)
}

// ResolveNeedsPutBlockStore chooses the physical destination for a NeedsPut
// probe. A first writer has no storage metadata and keeps the preferred store.
// Existing metadata is immutable placement state and must resolve without
// health failover to the exact org-scoped class and derived key.
func ResolveNeedsPutBlockStore(storageManager *storage.Manager, preferredStore *storage.BlockStore, preferredClass string, probe db.BlockReuseProbe, orgID, blockID string) (*storage.BlockStore, string, string, error) {
	if probe.Decision != db.BlockReuseNeedsPut {
		return nil, "", "", fmt.Errorf("block %s does not need a PUT", blockID)
	}

	canonicalClass := probe.StorageClass
	if canonicalClass == "" {
		if preferredStore == nil {
			return nil, "", "", fmt.Errorf("preferred block store is unavailable for %s", blockID)
		}
		// The first writer MINTS this block's physical identity: the class returned
		// here is the one persisted, so it is certified, never normalized. Trimming
		// would store an identity the writer never named -- and now that the write
		// funnel refuses a non-canonical class outright, a trim's only remaining
		// effect would be to turn that hard refusal into a silent rewrite.
		//
		// Certifying here rather than leaving it to the funnel also keeps the PUT
		// from landing: the object is written before materialization, so a class
		// rejected downstream would leave bytes in S3 that no row points at.
		if preferredClass == "" {
			return nil, "", "", fmt.Errorf("preferred storage class is empty for %s", blockID)
		}
		if !config.IsCanonicalStorageClassName(preferredClass) {
			return nil, "", "", fmt.Errorf("preferred storage class %q for block %s is not canonical", preferredClass, blockID)
		}
		return preferredStore, preferredClass, preferredStore.StorageKeyForHash(blockID), nil
	}

	canonicalStore, err := resolveCanonicalBlockStoreFn(storageManager, preferredStore, preferredClass, canonicalClass, orgID)
	if err != nil {
		return nil, "", "", fmt.Errorf("resolve canonical block store for %s: %w", blockID, err)
	}
	storageKey := canonicalStore.StorageKeyForHash(blockID)
	if storedKey := strings.TrimSpace(probe.StorageKey); storedKey != "" && storedKey != storageKey {
		return nil, "", "", fmt.Errorf("canonical block %s storage key %q does not match derived org-scoped key %q", blockID, storedKey, storageKey)
	}
	return canonicalStore, canonicalClass, storageKey, nil
}

// StoreUploadedBlockForProbe executes the physical action selected by a
// prepared Cassandra probe and returns the placement to materialize. beforePut
// runs only when a physical write is about to occur.
func StoreUploadedBlockForProbe(ctx context.Context, blockID string, probe db.BlockReuseProbe, data []byte, storageManager *storage.Manager, preferredStore *storage.BlockStore, preferredClass, orgID string, beforePut func() error) (storageKey, storageClass string, didPut bool, err error) {
	switch probe.Decision {
	case db.BlockReuseReusable:
		// The class this returns is re-persisted by the caller, so it must be the
		// stored identity itself, never a normalized copy of it.
		canonicalClass := probe.StorageClass
		canonicalStore, resolveErr := resolveCanonicalBlockStoreFn(storageManager, preferredStore, preferredClass, canonicalClass, orgID)
		if resolveErr != nil {
			return "", canonicalClass, false, fmt.Errorf("resolve canonical block store for %s: %w", blockID, resolveErr)
		}
		storageKey = canonicalStore.StorageKeyForHash(blockID)
		if storedKey := strings.TrimSpace(probe.StorageKey); storedKey != "" && storedKey != storageKey {
			return "", canonicalClass, false, fmt.Errorf("canonical block %s storage key %q does not match derived org-scoped key %q", blockID, storedKey, storageKey)
		}
		exists, existsErr := reusableCanonicalObjectExistsFn(ctx, canonicalStore, storageKey)
		if existsErr != nil {
			return storageKey, canonicalClass, false, fmt.Errorf("%w: verify canonical block %s in %s: %w", ErrBlockMaterializationTransient, blockID, canonicalClass, existsErr)
		}
		if exists {
			return storageKey, canonicalClass, false, nil
		}
		if beforePut != nil {
			if beforeErr := beforePut(); beforeErr != nil {
				return storageKey, canonicalClass, false, beforeErr
			}
		}
		if _, putErr := repairCanonicalBlockDirectFn(ctx, canonicalStore, storageKey, data); putErr != nil {
			return storageKey, canonicalClass, false, fmt.Errorf("%w: repair canonical block %s in %s: %w", ErrBlockMaterializationTransient, blockID, canonicalClass, putErr)
		}
		return storageKey, canonicalClass, true, nil
	case db.BlockReuseNeedsPut:
		putStore, resolvedClass, resolvedKey, resolveErr := ResolveNeedsPutBlockStore(storageManager, preferredStore, preferredClass, probe, orgID, blockID)
		if resolveErr != nil {
			return "", "", false, resolveErr
		}
		if beforePut != nil {
			if beforeErr := beforePut(); beforeErr != nil {
				return resolvedKey, resolvedClass, false, beforeErr
			}
		}
		if _, putErr := repairCanonicalBlockDirectFn(ctx, putStore, resolvedKey, data); putErr != nil {
			return resolvedKey, resolvedClass, false, fmt.Errorf("%w: store block %s in %s: %w", ErrBlockMaterializationTransient, blockID, resolvedClass, putErr)
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

// RetryUploadedBlockMaterialization retries the full store->materialize->confirm
// cycle. The second store observation runs after the provisional reference is
// durable, repairing an object deleted by a GC cycle that cleared its fence before
// materialization observed it.
// when GC temporarily fences the block or a transient I/O failure interrupts
// either phase. The retryable sentinel can surface from either phase because
// Cassandra-first probes may reject a PUT before S3 work starts, and the
// materialize helper now propagates the fence instead of absorbing it (F1), so
// a fence during materialize repeats the store phase and re-PUTs the object.
func RetryUploadedBlockMaterialization(label, blockID string, store func() error, materialize func() error, onRetry func(), resolveFence func() (bool, error)) error {
	return retryUploadedBlockMaterialization(nil, label, blockID, store, materialize, onRetry, resolveFence)
}

// RetryUploadedBlockMaterializationContext is the request-cancellable variant
// used by production handlers. The context aborts only the retry backoff wait;
// the store and materialize callbacks remain responsible for propagating it to
// their own I/O.
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

	// retryBlocked records the retry under a reason derived from the failing
	// PHASE (phaseReason), overridden to gc_fence only when the block is fenced.
	// It returns a non-nil error only when the backoff wait must abort the whole
	// operation (context cancelled).
	retryBlocked := func(attempt int, phaseReason string, retryErr error) error {
		if onRetry != nil {
			onRetry()
		}
		reason := phaseReason
		if errors.Is(retryErr, ErrBlockDeleteInProgress) {
			reason = blockMaterializationReasonFence
		}
		metrics.BlockUploadMaterializationRetriesTotal.WithLabelValues(label, reason).Inc()
		if reason == blockMaterializationReasonFence && resolveFence != nil {
			resolved, resolveErr := resolveFence()
			if resolveErr != nil {
				log.Printf("[%s] failed to inspect S3 orphan fence%s: %v", label, blockSuffix, resolveErr)
			} else if resolved {
				return nil
			}
		}
		sleepFor := RetryBackoff(attempt)
		log.Printf("[%s] block materialization retry%s reason=%s (%d/%d) after %s", label, blockSuffix, reason, attempt, attempts, sleepFor)
		return waitBeforeBlockMaterializationRetry(ctx, sleepFor)
	}

	for attempt := 1; attempt <= attempts; attempt++ {
		if err := store(); err != nil {
			if !IsRetryableBlockMaterializationError(err) || attempt == attempts {
				return err
			}
			if abortErr := retryBlocked(attempt, blockMaterializationReasonProbe, err); abortErr != nil {
				return abortErr
			}
			continue
		}
		if err := materialize(); err != nil {
			if !IsRetryableBlockMaterializationError(err) || attempt == attempts {
				return err
			}
			if abortErr := retryBlocked(attempt, blockMaterializationReasonMaterial, err); abortErr != nil {
				return abortErr
			}
			continue
		}
		if err := store(); err != nil {
			if !IsRetryableBlockMaterializationError(err) || attempt == attempts {
				return err
			}
			if abortErr := retryBlocked(attempt, blockMaterializationReasonProbe, err); abortErr != nil {
				return abortErr
			}
			continue
		}
		return nil
	}

	return fmt.Errorf("%w%s", ErrBlockDeleteInProgress, blockSuffix)
}

// waitBeforeBlockMaterializationRetry sleeps sleepFor before the next attempt.
// With a non-nil context the wait is cancellable (an aborted request stops
// retrying instead of burning the full budget); without one it uses the
// overridable sleep hook so tests stay fast.
func waitBeforeBlockMaterializationRetry(ctx context.Context, sleepFor time.Duration) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	if sleepFor <= 0 {
		return nil
	}
	if ctx == nil {
		registerUploadedBlockSleepFn(sleepFor)
		return nil
	}
	timer := time.NewTimer(sleepFor)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
