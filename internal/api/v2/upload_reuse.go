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

// ProbeUploadedBlockReuse wraps the DB probe so callers can fail open to legacy
// storage behavior when no Cassandra session is available.
func ProbeUploadedBlockReuse(database *db.DB, orgID, blockID string) (db.BlockReuseProbe, error) {
	if database == nil || database.Session() == nil {
		return db.BlockReuseProbe{Decision: db.BlockReuseUnknownError}, fmt.Errorf("block reuse probe unavailable for %s: database session is nil", blockID)
	}
	return database.ProbeBlockReuse(orgID, blockID)
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
