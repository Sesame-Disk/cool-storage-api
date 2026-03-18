package api

import (
	"log"

	"github.com/Sesame-Disk/sesamefs/internal/gc"
	"github.com/google/uuid"
)

// gcBlockEnqueuer adapts gc.Service to the v2.GCEnqueuer interface.
type gcBlockEnqueuer struct {
	service *gc.Service
}

// EnqueueBlocks enqueues blocks with ref_count=0 for garbage collection.
func (e *gcBlockEnqueuer) EnqueueBlocks(orgID string, blockIDs []string, storageClass string) {
	orgUUID, err := uuid.Parse(orgID)
	if err != nil {
		log.Printf("[GC Adapter] Invalid org_id %q: %v", orgID, err)
		return
	}
	for _, blockID := range blockIDs {
		if err := e.service.EnqueueBlock(orgUUID, blockID, uuid.Nil, storageClass); err != nil {
			log.Printf("[GC Adapter] Failed to enqueue block %s: %v", blockID, err)
		}
	}
}

// gcLibraryEnqueuer adapts gc.Service to the v2.LibraryGCEnqueuer interface.
type gcLibraryEnqueuer struct {
	service *gc.Service
}

// EnqueueLibraryDeletion enqueues all contents of a library for GC.
func (e *gcLibraryEnqueuer) EnqueueLibraryDeletion(orgID, libraryID string, storageClass string) {
	orgUUID, err := uuid.Parse(orgID)
	if err != nil {
		log.Printf("[GC Adapter] Invalid org_id %q: %v", orgID, err)
		return
	}
	libUUID, err := uuid.Parse(libraryID)
	if err != nil {
		log.Printf("[GC Adapter] Invalid library_id %q: %v", libraryID, err)
		return
	}
	if err := e.service.EnqueueLibraryDeletion(orgUUID, libUUID, storageClass); err != nil {
		log.Printf("[GC Adapter] Failed to enqueue library %s deletion: %v", libraryID, err)
	}
}

// gcCommitEnqueuer adapts gc.Service to the v2.CommitGCEnqueuer interface.
type gcCommitEnqueuer struct {
	service *gc.Service
}

// EnqueueCommits enqueues specific commits for GC deletion.
func (e *gcCommitEnqueuer) EnqueueCommits(orgID, libraryID string, commitIDs []string) {
	orgUUID, err := uuid.Parse(orgID)
	if err != nil {
		log.Printf("[GC Adapter] Invalid org_id %q: %v", orgID, err)
		return
	}
	libUUID, err := uuid.Parse(libraryID)
	if err != nil {
		log.Printf("[GC Adapter] Invalid library_id %q: %v", libraryID, err)
		return
	}
	if err := e.service.EnqueueCommits(orgUUID, libUUID, commitIDs); err != nil {
		log.Printf("[GC Adapter] Failed to enqueue %d commits for library %s: %v", len(commitIDs), libraryID, err)
	}
}
