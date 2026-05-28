package v2

import (
	"errors"
	"log"

	"github.com/Sesame-Disk/sesamefs/internal/db"
)

var rollbackUploadedBlockRefsFn = RollbackUploadedBlockRefs

// RollbackUploadedBlockRefs releases the provisional upload references for an
// aborted upload's blocks and enqueues any that became unreferenced for GC.
// It is idempotent (removing a missing reference is a no-op), so a retried
// rollback is always safe.
func RollbackUploadedBlockRefs(database *db.DB, orgID, repoID, operationID string, blockIDs []string) {
	if database == nil || len(blockIDs) == 0 {
		return
	}
	zeroRefBlocks := NewFSHelper(database).ReleaseUploadReferences(orgID, repoID, operationID, blockIDs)
	if len(zeroRefBlocks) == 0 {
		return
	}
	log.Printf("[uploadRollback] INFO: rollback released %d blocks to zero refs", len(zeroRefBlocks))
	enqueueZeroRefBlocks(database, orgID, repoID, zeroRefBlocks)
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
