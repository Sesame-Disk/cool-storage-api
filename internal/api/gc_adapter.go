package api

import (
	"log"
	"time"

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
// Hard failures increment gc_zero_ref_enqueue_failures_total. Soft discovery
// degradation (canonical candidate row ok, by-day projection repair failed) is
// tracked separately inside the GC service via
// gc_block_candidate_discovery_degraded_total.
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

// EnqueueLibraryCascade immediately queues the durable library cascade for a permanently
// deleted library (deduplicated against Phase 13). Best-effort: on failure the durable
// purge_requested_at marker still drives the cascade on the next scan.
func (e *gcLibraryEnqueuer) EnqueueLibraryCascade(orgID, libraryID, blockRepresentationID, storageClass string, deletedAt time.Time) {
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
	if err := e.service.EnqueueLibraryCascade(orgUUID, libUUID, blockRepresentationID, storageClass, deletedAt); err != nil {
		log.Printf("[GC Adapter] Failed to enqueue library %s cascade: %v", libraryID, err)
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
