package streaming

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"golang.org/x/sync/errgroup"
)

const canonicalBlockLocationConcurrency = 32

const canonicalBlockLocationLookupAttempts = 3

var canonicalBlockLocationRetryDelay = 25 * time.Millisecond

var ErrCanonicalBlockMetadataNotFound = errors.New("canonical block metadata not found")

var canonicalBlockLocationLookup = func(ctx context.Context, database *db.DB, orgID, blockID string) (db.BlockStorageLocation, bool, error) {
	if database == nil || database.Session() == nil {
		return db.BlockStorageLocation{}, false, fmt.Errorf("canonical block location database is unavailable")
	}
	return database.GetBlockStorageLocation(ctx, orgID, blockID)
}

var canonicalBlockStoreLookup = func(manager *storage.Manager, orgID, storageClass string) (*storage.BlockStore, error) {
	return manager.GetBlockStoreForOrg(orgID, storageClass)
}

var canonicalBlockGet = func(ctx context.Context, store *storage.BlockStore, storageKey string) ([]byte, error) {
	return store.GetBlockByStorageKey(ctx, storageKey)
}

var canonicalBlockGetReader = func(ctx context.Context, store *storage.BlockStore, storageKey string) (io.ReadCloser, error) {
	return store.GetBlockReaderByStorageKey(ctx, storageKey)
}

var canonicalBlockGetSize = func(ctx context.Context, store *storage.BlockStore, storageKey string) (int64, error) {
	return store.GetBlockSizeByStorageKey(ctx, storageKey)
}

type canonicalBlockReader struct {
	locations map[string]canonicalBlockLocation
}

// CanonicalBlockReader extends BlockReader with bounded physical existence
// checks routed through each block's pre-resolved canonical location.
type CanonicalBlockReader interface {
	BlockReader
	CheckBlocksExist(context.Context, []string, int) (map[string]bool, error)
}

type canonicalBlockLocation struct {
	store      *storage.BlockStore
	storageKey string
	sizeBytes  int64
	missing    bool
}

// NewCanonicalBlockReader resolves and caches the canonical physical location
// of every unique block before any reads can begin.
func NewCanonicalBlockReader(
	ctx context.Context,
	database *db.DB,
	manager *storage.Manager,
	orgID string,
	blockIDs []string,
	fallback *storage.BlockStore,
	fallbackClass string,
) (CanonicalBlockReader, error) {
	return observeCanonicalBlockResolution("read", blockIDs, func() (CanonicalBlockReader, error) {
		return newCanonicalBlockReader(ctx, database, manager, orgID, blockIDs, fallback, fallbackClass, true)
	})
}

// NewCanonicalBlockCheckReader resolves locations for an existence check. A
// missing metadata row is classified as a missing block without probing an
// arbitrary fallback backend; unlike a read path, checks do not retry expected
// misses for blocks the client has not uploaded yet.
func NewCanonicalBlockCheckReader(
	ctx context.Context,
	database *db.DB,
	manager *storage.Manager,
	orgID string,
	blockIDs []string,
	fallback *storage.BlockStore,
	fallbackClass string,
) (CanonicalBlockReader, error) {
	return observeCanonicalBlockResolution("check", blockIDs, func() (CanonicalBlockReader, error) {
		return newCanonicalBlockReader(ctx, database, manager, orgID, blockIDs, fallback, fallbackClass, false)
	})
}

func observeCanonicalBlockResolution(mode string, blockIDs []string, resolve func() (CanonicalBlockReader, error)) (CanonicalBlockReader, error) {
	started := time.Now()
	reader, err := resolve()
	result := "ok"
	if err != nil {
		result = "error"
	}
	metrics.CanonicalBlockResolutionDuration.WithLabelValues(mode, result).Observe(time.Since(started).Seconds())
	metrics.CanonicalBlockResolutionBlocks.WithLabelValues(mode).Observe(float64(len(blockIDs)))
	return reader, err
}

func newCanonicalBlockReader(
	ctx context.Context,
	database *db.DB,
	manager *storage.Manager,
	orgID string,
	blockIDs []string,
	fallback *storage.BlockStore,
	fallbackClass string,
	retryMissing bool,
) (CanonicalBlockReader, error) {
	reader := &canonicalBlockReader{
		locations: make(map[string]canonicalBlockLocation, len(blockIDs)),
	}
	uniqueBlockIDs := make([]string, 0, len(blockIDs))
	for _, rawBlockID := range blockIDs {
		blockID := db.NormalizeBlockID(rawBlockID)
		if !db.IsSHA256BlockID(blockID) {
			return nil, fmt.Errorf("block %q is not a resolved SHA-256 block id", rawBlockID)
		}
		if _, exists := reader.locations[blockID]; exists {
			continue
		}
		reader.locations[blockID] = canonicalBlockLocation{}
		uniqueBlockIDs = append(uniqueBlockIDs, blockID)
	}

	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(canonicalBlockLocationConcurrency)
	var submissionErr error
	for _, blockID := range uniqueBlockIDs {
		if err := gctx.Err(); err != nil {
			submissionErr = err
			break
		}
		blockID := blockID
		g.Go(func() error {
			metadata, found, err := lookupCanonicalBlockLocation(gctx, database, orgID, blockID, retryMissing)
			if err != nil {
				return fmt.Errorf("read canonical location for block %s: %w", blockID, err)
			}
			if !found {
				if retryMissing {
					return fmt.Errorf("%w: %s", ErrCanonicalBlockMetadataNotFound, blockID)
				}
				mu.Lock()
				reader.locations[blockID] = canonicalBlockLocation{missing: true}
				mu.Unlock()
				return nil
			}
			if !retryMissing && metadata.GCState == db.BlockGCStateDeleting {
				mu.Lock()
				reader.locations[blockID] = canonicalBlockLocation{missing: true}
				mu.Unlock()
				return nil
			}

			storageClass := strings.TrimSpace(metadata.StorageClass)
			if storageClass == "" {
				return fmt.Errorf("canonical storage class is empty for block %s", blockID)
			}
			store := fallback
			switch {
			case manager != nil:
				store, err = canonicalBlockStoreLookup(manager, orgID, storageClass)
				if err != nil {
					return fmt.Errorf("resolve canonical storage class %q for block %s: %w", storageClass, blockID, err)
				}
			case fallback != nil && strings.EqualFold(storageClass, strings.TrimSpace(fallbackClass)):
				store = fallback
			default:
				return fmt.Errorf("canonical storage class %q for block %s is unavailable", storageClass, blockID)
			}
			if store == nil {
				return fmt.Errorf("no block store available for block %s", blockID)
			}

			storageKey := store.StorageKeyForHash(blockID)
			sizeBytes := int64(0)
			if persistedKey := strings.TrimSpace(metadata.StorageKey); persistedKey != "" && persistedKey != storageKey {
				return fmt.Errorf("canonical storage key %q for block %s does not match derived org-scoped key %q", persistedKey, blockID, storageKey)
			}
			sizeBytes = metadata.SizeBytes

			mu.Lock()
			reader.locations[blockID] = canonicalBlockLocation{
				store:      store,
				storageKey: storageKey,
				sizeBytes:  sizeBytes,
			}
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	if submissionErr != nil {
		return nil, submissionErr
	}
	for blockID, location := range reader.locations {
		if location.missing {
			continue
		}
		if location.store == nil || location.storageKey == "" {
			return nil, fmt.Errorf("canonical location for block %s was not resolved", blockID)
		}
	}

	return reader, nil
}

func lookupCanonicalBlockLocation(ctx context.Context, database *db.DB, orgID, blockID string, retryMissing bool) (db.BlockStorageLocation, bool, error) {
	var lastErr error
	for attempt := 1; attempt <= canonicalBlockLocationLookupAttempts; attempt++ {
		metadata, found, err := canonicalBlockLocationLookup(ctx, database, orgID, blockID)
		if err == nil && (found || !retryMissing) {
			return metadata, found, nil
		}
		lastErr = err
		if attempt == canonicalBlockLocationLookupAttempts {
			break
		}
		timer := time.NewTimer(canonicalBlockLocationRetryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return db.BlockStorageLocation{}, false, ctx.Err()
		case <-timer.C:
		}
	}
	if lastErr != nil {
		return db.BlockStorageLocation{}, false, lastErr
	}
	return db.BlockStorageLocation{}, false, nil
}

func (r *canonicalBlockReader) CheckBlocksExist(ctx context.Context, blockIDs []string, concurrency int) (map[string]bool, error) {
	if concurrency < 1 {
		concurrency = canonicalBlockLocationConcurrency
	}
	result := make(map[string]bool, len(blockIDs))
	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(concurrency)
	for _, blockID := range blockIDs {
		blockID := db.NormalizeBlockID(blockID)
		mu.Lock()
		if _, seen := result[blockID]; seen {
			mu.Unlock()
			continue
		}
		result[blockID] = false
		mu.Unlock()
		g.Go(func() error {
			location, ok := r.locations[blockID]
			if !ok {
				return fmt.Errorf("block %q was not pre-resolved", blockID)
			}
			if location.missing {
				return nil
			}
			exists, err := location.store.ObjectExists(gctx, location.storageKey)
			if err != nil {
				return fmt.Errorf("check canonical block %s: %w", blockID, err)
			}
			mu.Lock()
			result[blockID] = exists
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *canonicalBlockReader) GetBlock(ctx context.Context, hash string) ([]byte, error) {
	location, err := r.location(hash)
	if err != nil {
		return nil, err
	}
	return canonicalBlockGet(ctx, location.store, location.storageKey)
}

func (r *canonicalBlockReader) GetBlockReader(ctx context.Context, hash string) (io.ReadCloser, error) {
	location, err := r.location(hash)
	if err != nil {
		return nil, err
	}
	return canonicalBlockGetReader(ctx, location.store, location.storageKey)
}

func (r *canonicalBlockReader) GetBlockSize(ctx context.Context, hash string) (int64, error) {
	location, err := r.location(hash)
	if err != nil {
		return 0, err
	}
	if location.sizeBytes > 0 {
		return location.sizeBytes, nil
	}
	return canonicalBlockGetSize(ctx, location.store, location.storageKey)
}

func (r *canonicalBlockReader) CachedBlockSize(hash string) (int64, bool) {
	location, err := r.location(hash)
	if err != nil || location.sizeBytes <= 0 {
		return 0, false
	}
	return location.sizeBytes, true
}

func (r *canonicalBlockReader) location(blockID string) (canonicalBlockLocation, error) {
	blockID = db.NormalizeBlockID(blockID)
	location, ok := r.locations[blockID]
	if !ok {
		return canonicalBlockLocation{}, fmt.Errorf("block %q was not pre-resolved", blockID)
	}
	if location.missing {
		return canonicalBlockLocation{}, fmt.Errorf("%w: %s", ErrCanonicalBlockMetadataNotFound, blockID)
	}
	return location, nil
}
