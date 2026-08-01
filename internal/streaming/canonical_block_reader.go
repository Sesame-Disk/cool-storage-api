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
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"golang.org/x/sync/errgroup"
)

const (
	canonicalBlockLocationConcurrency = 32
	// canonicalBlockLocationLookupAttempts counts TOTAL attempts, not retries:
	// 3 attempts means the first lookup plus 2 retries, and therefore only 2
	// waits of canonicalBlockLocationRetryDelay.
	canonicalBlockLocationLookupAttempts = 3
)

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

var canonicalBlockExists = func(ctx context.Context, store *storage.BlockStore, storageKey string) (bool, error) {
	return store.ObjectExists(ctx, storageKey)
}

// CanonicalBlockReader extends BlockReader with canonical physical existence checks.
type CanonicalBlockReader interface {
	BlockReader
	CheckBlocksExist(context.Context, []string, int) (map[string]bool, error)
}

type canonicalBlockReader struct {
	locations map[string]canonicalBlockLocation
}

type canonicalBlockLocation struct {
	store      *storage.BlockStore
	storageKey string
	sizeBytes  int64
	missing    bool
}

var _ BlockReader = (*canonicalBlockReader)(nil)

// NewCanonicalBlockReader resolves every unique block's canonical location.
// Missing metadata is looked up canonicalBlockLocationLookupAttempts times in
// total — 3 attempts, so 2 retries separated by 2 waits of
// canonicalBlockLocationRetryDelay (~50ms overall) — to tolerate a short
// publication race.
func NewCanonicalBlockReader(
	ctx context.Context,
	database *db.DB,
	manager *storage.Manager,
	orgID string,
	blockIDs []string,
	fallback *storage.BlockStore,
	fallbackClass string,
) (CanonicalBlockReader, error) {
	return newCanonicalBlockReader(ctx, database, manager, orgID, blockIDs, fallback, fallbackClass, true, canonicalBlockLocationConcurrency)
}

// NewCanonicalBlockCheckReader resolves locations for existence checks. Missing
// or deleting metadata is absent and is not retried or sent to a fallback store.
func NewCanonicalBlockCheckReader(
	ctx context.Context,
	database *db.DB,
	manager *storage.Manager,
	orgID string,
	blockIDs []string,
	fallback *storage.BlockStore,
	fallbackClass string,
) (CanonicalBlockReader, error) {
	return NewCanonicalBlockCheckReaderWithFanout(ctx, database, manager, orgID, blockIDs, fallback, fallbackClass, canonicalBlockLocationConcurrency)
}

// NewCanonicalBlockCheckReaderWithFanout is NewCanonicalBlockCheckReader with a
// caller-supplied resolution concurrency.
//
// check-blocks needs this because its per-node admission cap and its per-request
// fan-out multiply: a location phase that always ran at 32 would make the node's
// stated metadata-work budget wrong no matter what the caller configured
// (subcontract C of ISSUE-RATE-LIMIT-UPLOAD-DOWNLOAD-01). A non-positive value
// falls back to the package default, so no caller can accidentally serialise the
// whole resolution.
func NewCanonicalBlockCheckReaderWithFanout(
	ctx context.Context,
	database *db.DB,
	manager *storage.Manager,
	orgID string,
	blockIDs []string,
	fallback *storage.BlockStore,
	fallbackClass string,
	fanout int,
) (CanonicalBlockReader, error) {
	return newCanonicalBlockReader(ctx, database, manager, orgID, blockIDs, fallback, fallbackClass, false, fanout)
}

func newCanonicalBlockReader(
	ctx context.Context,
	database *db.DB,
	manager *storage.Manager,
	orgID string,
	blockIDs []string,
	fallback *storage.BlockStore,
	fallbackClass string,
	strictRead bool,
	fanout int,
) (CanonicalBlockReader, error) {
	if fanout < 1 || fanout > canonicalBlockLocationConcurrency {
		fanout = canonicalBlockLocationConcurrency
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	reader := &canonicalBlockReader{locations: make(map[string]canonicalBlockLocation, len(blockIDs))}
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
	slots := make(chan struct{}, fanout)
dispatchLocations:
	for _, id := range uniqueBlockIDs {
		if err := gctx.Err(); err != nil {
			break
		}
		select {
		case slots <- struct{}{}:
		case <-gctx.Done():
			break dispatchLocations
		}
		if err := gctx.Err(); err != nil {
			<-slots
			break
		}
		blockID := id
		g.Go(func() error {
			defer func() { <-slots }()
			metadata, found, err := lookupCanonicalBlockLocation(gctx, database, orgID, blockID, strictRead)
			if err != nil {
				return fmt.Errorf("read canonical location for block %s: %w", blockID, err)
			}
			if !found {
				if strictRead {
					return fmt.Errorf("%w: %s", ErrCanonicalBlockMetadataNotFound, blockID)
				}
				mu.Lock()
				reader.locations[blockID] = canonicalBlockLocation{missing: true}
				mu.Unlock()
				return nil
			}
			if metadata.GCState == db.BlockGCStateDeleting || metadata.GCState == db.BlockGCStateRepairingStub {
				if strictRead {
					return fmt.Errorf("canonical block %s is not readable in gc state %q", blockID, metadata.GCState)
				}
				mu.Lock()
				reader.locations[blockID] = canonicalBlockLocation{missing: true}
				mu.Unlock()
				return nil
			}
			if metadata.GCState != "" {
				return fmt.Errorf("canonical block %s has unknown gc state %q", blockID, metadata.GCState)
			}
			if metadata.CreatedAt == nil {
				if !strictRead && strings.TrimSpace(metadata.StorageClass) == "" {
					mu.Lock()
					reader.locations[blockID] = canonicalBlockLocation{missing: true}
					mu.Unlock()
					return nil
				}
				return fmt.Errorf("canonical block %s has storage metadata without a creation timestamp", blockID)
			}

			storageClass := strings.TrimSpace(metadata.StorageClass)
			if storageClass == "" {
				return fmt.Errorf("canonical storage class is empty for block %s", blockID)
			}

			var store *storage.BlockStore
			switch {
			case manager != nil:
				store, err = canonicalBlockStoreLookup(manager, orgID, storageClass)
				if err != nil {
					return fmt.Errorf("resolve canonical storage class %q for block %s: %w", storageClass, blockID, err)
				}
			case fallback != nil && storageClass == strings.TrimSpace(fallbackClass):
				store = fallback
			default:
				return fmt.Errorf("canonical storage class %q for block %s is unavailable", storageClass, blockID)
			}
			if store == nil {
				return fmt.Errorf("no block store available for block %s", blockID)
			}

			storageKey := store.StorageKeyForHash(blockID)
			if persistedKey := strings.TrimSpace(metadata.StorageKey); persistedKey != "" && persistedKey != storageKey {
				return fmt.Errorf("canonical storage key %q for block %s does not match derived org-scoped key %q", persistedKey, blockID, storageKey)
			}

			mu.Lock()
			reader.locations[blockID] = canonicalBlockLocation{
				store:      store,
				storageKey: storageKey,
				sizeBytes:  metadata.SizeBytes,
			}
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	for blockID, location := range reader.locations {
		if !location.missing && (location.store == nil || location.storageKey == "") {
			return nil, fmt.Errorf("canonical location for block %s was not resolved", blockID)
		}
	}
	return reader, nil
}

func lookupCanonicalBlockLocation(ctx context.Context, database *db.DB, orgID, blockID string, retryMissing bool) (db.BlockStorageLocation, bool, error) {
	for attempt := 1; attempt <= canonicalBlockLocationLookupAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return db.BlockStorageLocation{}, false, err
		}
		metadata, found, err := canonicalBlockLocationLookup(ctx, database, orgID, blockID)
		if err != nil {
			return db.BlockStorageLocation{}, false, err
		}
		if found || !retryMissing {
			return metadata, found, nil
		}
		if attempt == canonicalBlockLocationLookupAttempts {
			break
		}
		timer := time.NewTimer(canonicalBlockLocationRetryDelay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return db.BlockStorageLocation{}, false, ctx.Err()
		case <-timer.C:
		}
	}
	return db.BlockStorageLocation{}, false, nil
}

func (r *canonicalBlockReader) CheckBlocksExist(ctx context.Context, blockIDs []string, concurrency int) (map[string]bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if concurrency < 1 || concurrency > canonicalBlockLocationConcurrency {
		concurrency = canonicalBlockLocationConcurrency
	}

	result := make(map[string]bool, len(blockIDs))
	unique := make([]string, 0, len(blockIDs))
	for _, rawBlockID := range blockIDs {
		blockID := db.NormalizeBlockID(rawBlockID)
		if !db.IsSHA256BlockID(blockID) {
			return nil, fmt.Errorf("block %q is not a resolved SHA-256 block id", rawBlockID)
		}
		if _, seen := result[blockID]; seen {
			continue
		}
		result[blockID] = false
		unique = append(unique, blockID)
	}

	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	slots := make(chan struct{}, concurrency)
dispatchExistence:
	for _, id := range unique {
		if err := gctx.Err(); err != nil {
			break
		}
		select {
		case slots <- struct{}{}:
		case <-gctx.Done():
			break dispatchExistence
		}
		if err := gctx.Err(); err != nil {
			<-slots
			break
		}
		blockID := id
		g.Go(func() error {
			defer func() { <-slots }()
			location, ok := r.locations[blockID]
			if !ok {
				return fmt.Errorf("block %q was not pre-resolved", blockID)
			}
			if location.missing {
				return nil
			}
			exists, err := canonicalBlockExists(gctx, location.store, location.storageKey)
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
	if err := ctx.Err(); err != nil {
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
	if !db.IsSHA256BlockID(blockID) {
		return canonicalBlockLocation{}, fmt.Errorf("block %q is not a resolved SHA-256 block id", blockID)
	}
	location, ok := r.locations[blockID]
	if !ok {
		return canonicalBlockLocation{}, fmt.Errorf("block %q was not pre-resolved", blockID)
	}
	if location.missing {
		return canonicalBlockLocation{}, fmt.Errorf("%w: %s", ErrCanonicalBlockMetadataNotFound, blockID)
	}
	return location, nil
}
