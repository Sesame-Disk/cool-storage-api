package v2

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
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
	if err := writeBlockMappingForMaterializationFn(database, orgID, repoID, externalBlockID, internalBlockID); err != nil {
		if errors.Is(err, db.ErrBlockIDMappingConflict) {
			return fmt.Errorf("%w: %w", ErrBlockMappingWriteFailed, err)
		}
		return fmt.Errorf("%w: %w: %w", ErrBlockMaterializationTransient, ErrBlockMappingWriteFailed, err)
	}
	return nil
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
	if err := writeVerifiedWebBlockMappingFn(database, orgID, repoID, externalBlockID, internalBlockID); err != nil {
		if errors.Is(err, db.ErrBlockIDMappingConflict) {
			return err
		}
		return fmt.Errorf("%w: %w: %w", ErrBlockMaterializationTransient, ErrBlockMappingWriteFailed, err)
	}
	return nil
}
