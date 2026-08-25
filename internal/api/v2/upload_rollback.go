package v2

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
)

var registerUploadedBlockTargetForMaterializationFn = func(ctx context.Context, database *db.DB, orgID, repoID, internalBlockID, operationID string, sizeBytes int, target BlockMaterializationTarget, sha1ID string) error {
	return NewFSHelper(database).RegisterUploadedBlockTarget(ctx, orgID, repoID, internalBlockID, operationID, sizeBytes, target, sha1ID)
}
var writeBlockMappingForMaterializationFn = func(database *db.DB, orgID, repoID, externalBlockID, internalBlockID string) error {
	if database == nil {
		return nil
	}
	representationID, err := db.ResolveBlockRepresentationID(database.Session(), orgID, repoID)
	if err != nil {
		return err
	}
	return database.WriteBlockIDMapping(orgID, representationID, externalBlockID, internalBlockID, time.Time{})
}

// writeVerifiedWebBlockMappingFn is the WEB block-upload (session) variant of the
// mapping writer. It uses WriteVerifiedWebBlockMapping, which fails closed on an
// external→different-internal conflict. Legacy paths keep writeBlockMappingForMaterializationFn.
var writeVerifiedWebBlockMappingFn = func(database *db.DB, orgID, repoID, externalBlockID, internalBlockID string) error {
	if database == nil {
		return nil
	}
	representationID, err := db.ResolveBlockRepresentationID(database.Session(), orgID, repoID)
	if err != nil {
		return err
	}
	return database.WriteVerifiedWebBlockMapping(orgID, representationID, externalBlockID, internalBlockID, time.Time{})
}

// RegisterUploadedBlockTargetAndMapping materializes the exact target selected
// by the store/probe flow, then writes its optional external SHA-1 mapping.
func RegisterUploadedBlockTargetAndMapping(ctx context.Context, database *db.DB, orgID, repoID, internalBlockID, operationID string, sizeBytes int, target BlockMaterializationTarget, externalBlockID string) error {
	if err := registerUploadedBlockTargetForMaterializationFn(ctx, database, orgID, repoID, internalBlockID, operationID, sizeBytes, target, externalBlockID); err != nil {
		return err
	}
	if strings.TrimSpace(externalBlockID) == "" {
		return nil
	}
	return writeBlockMappingAfterInstall(ctx, func() error {
		return writeBlockMappingForMaterializationFn(database, orgID, repoID, externalBlockID, internalBlockID)
	}, internalBlockID, func(err error) error {
		return fmt.Errorf("%w: %w", ErrBlockMappingWriteFailed, err)
	})
}

// RegisterWebUploadedBlockTargetAndMapping is the WEB block-upload (session)
// variant. It writes the forward
// SHA-1 -> SHA-256 mapping through WriteVerifiedWebBlockMapping, which fails
// closed on an external→different-internal conflict (a forged/colliding SHA-1).
// Both hashes are server-computed from the block's real bytes, so the mapping is
// verified, not client-asserted.
//
// A db.ErrBlockIDMappingConflict is returned unwrapped (so callers can errors.Is
// it into a 409); any other mapping failure is wrapped as ErrBlockMappingWriteFailed.
func RegisterWebUploadedBlockTargetAndMapping(ctx context.Context, database *db.DB, orgID, repoID, internalBlockID, operationID string, sizeBytes int, target BlockMaterializationTarget, externalBlockID string) error {
	if err := registerUploadedBlockTargetForMaterializationFn(ctx, database, orgID, repoID, internalBlockID, operationID, sizeBytes, target, externalBlockID); err != nil {
		return err
	}
	if strings.TrimSpace(externalBlockID) == "" {
		return nil
	}
	return writeBlockMappingAfterInstall(ctx, func() error {
		return writeVerifiedWebBlockMappingFn(database, orgID, repoID, externalBlockID, internalBlockID)
	}, internalBlockID, func(err error) error { return err })
}

// writeBlockMappingAfterInstall writes the external SHA-1 mapping for a block
// whose canonical metadata is ALREADY installed, retrying transient failures in
// place instead of letting them escape as ErrBlockMaterializationTransient.
//
// That distinction is the whole point. The materialization retry driver answers a
// transient materialize error by restarting the cycle at
// BlockMaterializationInitial -- the one phase with mint authority. Before the
// INSTALL that is correct: nothing is canonical yet, so re-probing and minting is
// the recovery. After the INSTALL applied it is not: the block is already
// canonical under this exact target, and a probe that momentarily reads rowless
// would hand a sidecar mapping timeout the authority to mint and PUT a second
// physical incarnation. The mapping is a sidecar write; failing it must never
// reopen physical identity.
//
// So transient mapping failures retry here, on the same bounded budget the driver
// uses, and an exhausted budget returns a NON-retryable error. The block stays
// installed and durable; the request fails with a mapping error and the client's
// own retry finds the block reusable. A mapping conflict (external -> different
// internal) is permanent and never retried -- conflictErr maps it to each
// funnel's established sentinel.
func writeBlockMappingAfterInstall(ctx context.Context, write func() error, blockID string, conflictErr func(error) error) error {
	attempts := RetryAttempts()
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("%w: mapping write for block %s aborted: %w", ErrBlockMappingWriteFailed, blockID, err)
			}
		}
		err := write()
		if err == nil {
			return nil
		}
		if errors.Is(err, db.ErrBlockIDMappingConflict) {
			return conflictErr(err)
		}
		lastErr = err
		if attempt == attempts {
			break
		}
		metrics.BlockUploadMappingRetriesTotal.Inc()
		if err := waitBeforeBlockMaterializationRetry(ctx, RetryBackoff(attempt)); err != nil {
			return fmt.Errorf("%w: mapping write for block %s aborted: %w", ErrBlockMappingWriteFailed, blockID, err)
		}
	}
	// Deliberately NOT tagged ErrBlockMaterializationTransient: the canonical
	// install already applied, so the retry driver must not restart the store
	// phase and reopen mint authority for this block.
	return fmt.Errorf("%w: mapping write for block %s failed after %d attempts: %w", ErrBlockMappingWriteFailed, blockID, attempts, lastErr)
}
