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

func uploadRollbackOperationKey(scope, repoID, identifier string) string {
	return fmt.Sprintf("%s:%s:%s:%d", scope, repoID, identifier, time.Now().UnixNano())
}

func RollbackUploadedBlockRefs(database *db.DB, orgID, repoID, operationKey string, blockIDs []string) {
	if database == nil || len(blockIDs) == 0 || strings.TrimSpace(operationKey) == "" {
		return
	}
	zeroRefBlocks := NewFSHelper(database).DecrementBlockRefCountsOnce(orgID, operationKey, blockIDs)
	if len(zeroRefBlocks) == 0 {
		return
	}
	log.Printf("[uploadRollback] INFO: rollback %q drove %d blocks to zero refs", operationKey, len(zeroRefBlocks))
	enqueueZeroRefBlocks(database, orgID, repoID, zeroRefBlocks)
}

func handleStoredUploadMetadataError(database *db.DB, orgID, repoID, fileID string, internalBlockIDs []string, err error) {
	if err == nil || len(internalBlockIDs) == 0 {
		return
	}
	if errors.Is(err, ErrLibraryHeadPublicationUnknown) {
		return
	}
	rollbackUploadedBlockRefsFn(
		database,
		orgID,
		repoID,
		uploadRollbackOperationKey("v2_direct_metadata", repoID, fileID),
		internalBlockIDs,
	)
}
