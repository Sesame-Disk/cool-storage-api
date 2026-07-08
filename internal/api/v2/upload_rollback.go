package v2

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
)

var rollbackUploadedBlockRefsFn = RollbackUploadedBlockRefs
var registerUploadedBlockForMaterializationFn = func(database *db.DB, orgID, repoID, internalBlockID, operationID string, sizeBytes int, storageClass, storageKey, sha1ID string) error {
	return NewFSHelper(database).RegisterUploadedBlock(orgID, repoID, internalBlockID, operationID, sizeBytes, storageClass, storageKey, sha1ID)
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

func releaseUploadedBlockRefs(database *db.DB, orgID, repoID, operationID string, blockIDs []string, logPrefix string) {
	if database == nil || len(blockIDs) == 0 {
		return
	}
	zeroRefBlocks := NewFSHelper(database).ReleaseUploadReferences(orgID, repoID, operationID, blockIDs)
	if len(zeroRefBlocks) == 0 {
		return
	}
	log.Printf("[%s] INFO: released %d blocks to zero refs", logPrefix, len(zeroRefBlocks))
	enqueueZeroRefBlocks(database, orgID, repoID, zeroRefBlocks)
}

// ReleaseUploadedBlockRefs releases provisional upload refs after a successful
// publish path has created the permanent fs: refs.
func ReleaseUploadedBlockRefs(database *db.DB, orgID, repoID, operationID string, blockIDs []string) {
	releaseUploadedBlockRefs(database, orgID, repoID, operationID, blockIDs, "uploadRelease")
}

// RollbackUploadedBlockRefs releases the provisional upload references for an
// aborted upload's blocks and enqueues any that became unreferenced for GC.
// It is idempotent (removing a missing reference is a no-op), so a retried
// rollback is always safe.
func RollbackUploadedBlockRefs(database *db.DB, orgID, repoID, operationID string, blockIDs []string) {
	releaseUploadedBlockRefs(database, orgID, repoID, operationID, blockIDs, "uploadRollback")
}

// RegisterUploadedBlockAndMapping materializes an uploaded block in Cassandra by
// creating the provisional upload reference + metadata first and only then
// writing the optional external SHA-1 mapping. If the mapping write fails, the
// provisional reference is rolled back so retries can restart from a clean
// state instead of leaving a registered block without a usable mapping.
func RegisterUploadedBlockAndMapping(database *db.DB, orgID, repoID, internalBlockID, operationID string, sizeBytes int, storageClass, storageKey, externalBlockID string) error {
	if err := registerUploadedBlockForMaterializationFn(database, orgID, repoID, internalBlockID, operationID, sizeBytes, storageClass, storageKey, externalBlockID); err != nil {
		return err
	}
	if strings.TrimSpace(externalBlockID) == "" {
		return nil
	}
	if err := writeBlockMappingForMaterializationFn(database, orgID, repoID, externalBlockID, internalBlockID); err != nil {
		rollbackUploadedBlockRefsFn(database, orgID, repoID, operationID, []string{internalBlockID})
		return fmt.Errorf("%w: %v", ErrBlockMappingWriteFailed, err)
	}
	return nil
}

// RegisterWebUploadedBlockAndMapping is the WEB block-upload (session) variant of
// RegisterUploadedBlockAndMapping. It is identical EXCEPT it writes the forward
// SHA-1 -> SHA-256 mapping through WriteVerifiedWebBlockMapping, which fails
// closed on an external→different-internal conflict (a forged/colliding SHA-1).
// Both hashes are server-computed from the block's real bytes, so the mapping is
// verified, not client-asserted. Legacy upload paths keep using
// RegisterUploadedBlockAndMapping + the plain WriteBlockIDMapping, so they pay no
// extra read and see no behavior change.
//
// A db.ErrBlockIDMappingConflict is returned unwrapped (so callers can errors.Is
// it into a 409); any other mapping failure is wrapped as ErrBlockMappingWriteFailed.
func RegisterWebUploadedBlockAndMapping(database *db.DB, orgID, repoID, internalBlockID, operationID string, sizeBytes int, storageClass, storageKey, externalBlockID string) error {
	if err := registerUploadedBlockForMaterializationFn(database, orgID, repoID, internalBlockID, operationID, sizeBytes, storageClass, storageKey, externalBlockID); err != nil {
		return err
	}
	if strings.TrimSpace(externalBlockID) == "" {
		return nil
	}
	if err := writeVerifiedWebBlockMappingFn(database, orgID, repoID, externalBlockID, internalBlockID); err != nil {
		rollbackUploadedBlockRefsFn(database, orgID, repoID, operationID, []string{internalBlockID})
		if errors.Is(err, db.ErrBlockIDMappingConflict) {
			return err
		}
		return fmt.Errorf("%w: %v", ErrBlockMappingWriteFailed, err)
	}
	return nil
}

func handleStoredUploadMetadataError(database *db.DB, orgID, repoID, operationID string, internalBlockIDs []string, err error) {
	if err == nil || len(internalBlockIDs) == 0 {
		return
	}
	if errors.Is(err, ErrLibraryHeadPublicationUnknown) {
		return
	}
	rollbackUploadedBlockRefsFn(database, orgID, repoID, operationID, internalBlockIDs)
}
