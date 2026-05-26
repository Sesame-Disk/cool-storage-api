package api

import (
	"log"

	v2 "github.com/Sesame-Disk/sesamefs/internal/api/v2"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/gc"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	"github.com/google/uuid"
)

// gcBlockEnqueuer adapts gc.Service to the v2.GCEnqueuer interface.
type gcBlockEnqueuer struct {
	service *gc.Service
}

// EnqueueBlocks enqueues blocks with ref_count=0 for garbage collection.
//
// Each failure increments gc_zero_ref_enqueue_failures_total. After the
// blocks schema refactor removed the per-org partition scan backfill, this
// metric is the only signal that a block hit ref_count=0 without being
// registered in gc_block_candidates. Alert on sustained non-zero rate.
func (e *gcBlockEnqueuer) EnqueueBlocks(orgID string, blockIDs []string, storageClass string) {
	orgUUID, err := uuid.Parse(orgID)
	if err != nil {
		log.Printf("[GC Adapter] Invalid org_id %q: %v", orgID, err)
		metrics.GCZeroRefEnqueueFailuresTotal.WithLabelValues("invalid_org").Add(float64(len(blockIDs)))
		return
	}
	for _, blockID := range blockIDs {
		if err := e.service.EnqueueBlock(orgUUID, blockID, uuid.Nil, storageClass); err != nil {
			log.Printf("[GC Adapter] WARNING: failed to enqueue zero-ref block %s for org %s: %v", blockID, orgID, err)
			metrics.GCZeroRefEnqueueFailuresTotal.WithLabelValues("service_error").Inc()
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

// onlyOfficeReconcilerAdapter bridges the GC scanner to the OnlyOffice
// pending-blocks reconciler that lives in the v2 package.
type onlyOfficeReconcilerAdapter struct {
	database *db.DB
}

func newOnlyOfficeReconcilerAdapter(database *db.DB) gc.OnlyOfficeReconciler {
	if database == nil {
		return nil
	}
	return &onlyOfficeReconcilerAdapter{database: database}
}

func (a *onlyOfficeReconcilerAdapter) ReconcileOnlyOfficePendingBlocks(orgID uuid.UUID) error {
	if a == nil || a.database == nil {
		return nil
	}
	return v2.ReconcileOnlyOfficePendingBlocksForOrg(a.database, orgID.String())
}
