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
var registerUploadedBlockForMaterializationFn = func(database *db.DB, orgID, repoID, internalBlockID, operationID string, sizeBytes int, storageClass, storageKey string) error {
	return NewFSHelper(database).RegisterUploadedBlock(orgID, repoID, internalBlockID, operationID, sizeBytes, storageClass, storageKey)
}
var writeBlockMappingForMaterializationFn = func(database *db.DB, orgID, externalBlockID, internalBlockID string) error {
	if database == nil {
		return nil
	}
	return database.WriteBlockIDMapping(orgID, externalBlockID, internalBlockID, time.Time{})
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
	if err := registerUploadedBlockForMaterializationFn(database, orgID, repoID, internalBlockID, operationID, sizeBytes, storageClass, storageKey); err != nil {
		return err
	}
	if strings.TrimSpace(externalBlockID) == "" {
		return nil
	}
	if err := writeBlockMappingForMaterializationFn(database, orgID, externalBlockID, internalBlockID); err != nil {
		rollbackUploadedBlockRefsFn(database, orgID, repoID, operationID, []string{internalBlockID})
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
