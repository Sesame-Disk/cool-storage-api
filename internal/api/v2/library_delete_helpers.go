package v2

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	"github.com/Sesame-Disk/sesamefs/internal/traffic"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type trashLibraryCandidate struct {
	OrgID        string
	LibraryID    string
	StorageClass string
	// DeletedAt is the library's original trash time. The permanent-delete marker
	// preserves it (rather than resetting to now) so a library_cascade already queued
	// under that identity stays deduplicated — see hardDeleteLibraryRowsFn.
	DeletedAt time.Time
}

var (
	resolveDeleteBlockRepresentationFn = func(database *dbpkg.DB, orgID, libraryID string) (string, error) {
		return dbpkg.ResolveBlockRepresentationIDForDelete(database.Session(), orgID, libraryID)
	}
	cleanupLibraryLinksForDeleteFn = func(database *dbpkg.DB, orgID, libraryID string) error {
		return cleanupLibraryLinks(database, orgID, libraryID)
	}
	readPermanentDeleteLibraryStateFn = func(database *dbpkg.DB, orgID, libraryID string, _ time.Time) (dbpkg.LibraryState, error) {
		return dbpkg.ReadLibraryState(database.Session(), orgID, libraryID)
	}
	acquireLibraryHardDeleteLockLeaseFn = func(database *dbpkg.DB, libraryID, leaseToken uuid.UUID) (bool, error) {
		return gcpkg.AcquireLibraryHardDeleteLockLease(database.Session(), libraryID, leaseToken)
	}
	renewLibraryHardDeleteLockLeaseFn = func(database *dbpkg.DB, libraryID, leaseToken uuid.UUID) (bool, error) {
		return gcpkg.RenewLibraryHardDeleteLockLease(database.Session(), libraryID, leaseToken)
	}
	releaseLibraryHardDeleteLockLeaseFn = func(database *dbpkg.DB, libraryID, leaseToken uuid.UUID) error {
		return gcpkg.ReleaseLibraryHardDeleteLockLease(database.Session(), libraryID, leaseToken)
	}
	hardDeleteLibraryRowsFn = func(database *dbpkg.DB, orgID, libraryID, storageClass, blockRepresentationID string, deletedAt time.Time) error {
		batch := database.Session().Batch(gocql.LoggedBatch)
		if err := addDeleteAdminLibraryReadModelQueries(database, batch, orgID, libraryID); err != nil {
			return errors.Join(errHardDeleteLibraryReadModel, err)
		}
		batch.Query(`DELETE FROM libraries WHERE org_id = ? AND library_id = ?`, orgID, libraryID)
		batch.Query(`DELETE FROM libraries_by_id WHERE library_id = ?`, libraryID)
		// This is a *permanent* delete. Two invariants:
		//   1. PRESERVE the original deleted_at (the library's trash time). Phase 13 dedups
		//      library_cascade by deleted_at; resetting it to now() would change the identity
		//      and let a cascade already queued under the old deleted_at be enqueued a second
		//      time. deletedAt is the authoritative libraries.deleted_at captured by the caller;
		//      fall back to now() only if it is somehow zero.
		//   2. Stamp purge_requested_at = now() so Phase 13 makes the library eligible on its
		//      next scan instead of waiting out the configured TrashRetentionDays. The cascade
		//      is still gated by the GC grace period before the worker processes it — reclamation
		//      happens on the order of the grace period, not the retention period.
		// See migration 012 / ISSUE-GC-ORG-TRASH-NO-CASCADE-01.
		markerDeletedAt := deletedAt
		if markerDeletedAt.IsZero() {
			markerDeletedAt = time.Now()
		}
		batch.Query(`INSERT INTO deleted_libraries (library_id, org_id, deleted_at, storage_class, block_representation_id, purge_requested_at) VALUES (?, ?, ?, ?, ?, ?)`, libraryID, orgID, markerDeletedAt, storageClass, blockRepresentationID, time.Now())
		if err := batch.Exec(); err != nil {
			return errors.Join(errHardDeleteLibraryBatchExec, err)
		}
		return nil
	}
	cleanupAllLibraryTagsForDeleteFn       = CleanupAllLibraryTags
	deleteLibraryStorageCounterForDeleteFn = traffic.DeleteLibraryStorageCounter
	runAsyncLibraryDeleteSideEffectFn      = func(fn func()) { go fn() }
)

var (
	errDeleteRepresentationUnresolved = errors.New("delete representation unresolved")
	errDeleteLibraryLinksCleanup      = errors.New("delete library links cleanup")
	errHardDeleteLibraryReadModel     = errors.New("hard delete library read model")
	errHardDeleteLibraryBatchExec     = errors.New("hard delete library batch exec")
	errPermanentDeleteCandidateStale  = errors.New("permanent delete candidate stale")
	errPermanentDeleteInProgress      = errors.New("permanent delete in progress")
)

func enqueueLibraryCascadeBestEffort(libEnqueuer LibraryGCEnqueuer, orgID, repoID, blockRepresentationID, storageClass string, deletedAt time.Time) {
	if libEnqueuer == nil {
		return
	}
	// Immediately queue the durable library cascade so reclamation starts on the next worker
	// tick instead of waiting up to a full ScanInterval for Phase 13. This is deduplicated
	// against Phase 13 (identical deletedAt identity), so it is not a second producer; the
	// durable purge_requested_at marker recovers it if this fire-and-forget enqueue is lost.
	// See migration 012 / ISSUE-GC-ORG-TRASH-NO-CASCADE-01.
	runAsyncLibraryDeleteSideEffectFn(func() {
		libEnqueuer.EnqueueLibraryCascade(orgID, repoID, blockRepresentationID, storageClass, deletedAt)
	})
}

func permanentlyDeleteTrashedLibraryCandidate(database *dbpkg.DB, candidate trashLibraryCandidate, metricOp, logPrefix string, cleanupLinks bool) (string, error) {
	libraryUUID, err := uuid.Parse(candidate.LibraryID)
	if err != nil {
		return "", fmt.Errorf("parse library id %q: %w", candidate.LibraryID, err)
	}

	leaseToken := uuid.New()
	acquired, err := acquireLibraryHardDeleteLockLeaseFn(database, libraryUUID, leaseToken)
	if err != nil {
		return "", fmt.Errorf("acquire library hard-delete lock for %s/%s: %w", candidate.OrgID, candidate.LibraryID, err)
	}
	if !acquired {
		return "", errPermanentDeleteInProgress
	}
	defer func() {
		if err := releaseLibraryHardDeleteLockLeaseFn(database, libraryUUID, leaseToken); err != nil {
			log.Printf("[%s] failed to release hard-delete lock for %s/%s: %v", logPrefix, candidate.OrgID, candidate.LibraryID, err)
		}
	}()

	state, err := readPermanentDeleteLibraryStateFn(database, candidate.OrgID, candidate.LibraryID, candidate.DeletedAt)
	if errors.Is(err, gocql.ErrNotFound) {
		return "", errPermanentDeleteCandidateStale
	}
	if err != nil {
		return "", fmt.Errorf("read canonical library state for %s/%s: %w", candidate.OrgID, candidate.LibraryID, err)
	}
	if state.DeletedAt == nil || !state.DeletedAt.Equal(candidate.DeletedAt) {
		return "", errPermanentDeleteCandidateStale
	}

	blockRepresentationID, err := resolveDeleteBlockRepresentationFn(database, candidate.OrgID, candidate.LibraryID)
	if err != nil {
		metrics.LibraryDeleteRepresentationResolutionFailures.WithLabelValues(metricOp).Inc()
		log.Printf("[%s] refusing to hard-delete %s/%s: block representation unresolved: %v", logPrefix, candidate.OrgID, candidate.LibraryID, err)
		return "", errDeleteRepresentationUnresolved
	}

	if cleanupLinks {
		if err := cleanupLibraryLinksForDeleteFn(database, candidate.OrgID, candidate.LibraryID); err != nil {
			return "", errors.Join(errDeleteLibraryLinksCleanup, err)
		}
	}

	owned, err := renewLibraryHardDeleteLockLeaseFn(database, libraryUUID, leaseToken)
	if err != nil {
		return "", fmt.Errorf("fence library hard-delete lock for %s/%s: %w", candidate.OrgID, candidate.LibraryID, err)
	}
	if !owned {
		return "", errPermanentDeleteInProgress
	}

	if err := hardDeleteLibraryRowsFn(database, candidate.OrgID, candidate.LibraryID, candidate.StorageClass, blockRepresentationID, candidate.DeletedAt); err != nil {
		return "", err
	}
	return blockRepresentationID, nil
}

func writePermanentDeletePreconditionError(c *gin.Context, err error) bool {
	if errors.Is(err, errPermanentDeleteCandidateStale) {
		c.JSON(http.StatusConflict, gin.H{"error": "library is no longer in trash"})
		return true
	}
	if errors.Is(err, errPermanentDeleteInProgress) {
		c.JSON(http.StatusConflict, gin.H{"error": "library permanent delete is already in progress"})
		return true
	}
	return false
}

func (h *DeletedLibraryHandler) permanentDeleteResolvedRepo(c *gin.Context, orgID, repoID, storageClass string, deletedAt time.Time) {
	blockRepresentationID, err := permanentlyDeleteTrashedLibraryCandidate(h.db, trashLibraryCandidate{
		OrgID:        orgID,
		LibraryID:    repoID,
		StorageClass: storageClass,
		DeletedAt:    deletedAt,
	}, "permanent_delete", "PermanentDeleteRepo", true)
	if writePermanentDeletePreconditionError(c, err) {
		return
	}
	if errors.Is(err, errDeleteRepresentationUnresolved) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to prepare library for permanent deletion"})
		return
	}
	if errors.Is(err, errDeleteLibraryLinksCleanup) {
		log.Printf("[PermanentDeleteRepo] Failed to clean share links for %s: %v", repoID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to clean share links"})
		return
	}
	if err != nil {
		if errors.Is(err, errHardDeleteLibraryReadModel) {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to clean library read model"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete library"})
		return
	}
	if h.libHandler != nil {
		enqueueLibraryCascadeBestEffort(h.libHandler.gcEnqueuer, orgID, repoID, blockRepresentationID, storageClass, deletedAt)
	}
	if err := deleteLibraryStorageCounterForDeleteFn(h.db, orgID, repoID); err != nil {
		log.Printf("failed to delete storage counter for permanently deleted library %s/%s: %v", orgID, repoID, err)
	}
	runAsyncLibraryDeleteSideEffectFn(func() {
		if err := cleanupAllLibraryTagsForDeleteFn(h.db, repoID); err != nil {
			log.Printf("failed to clean tag metadata for permanently deleted library %s: %v", repoID, err)
		}
	})
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *OrgAdminHandler) deleteResolvedTrashLibrary(c *gin.Context, targetOrgID, repoID, storageClass string, deletedAt time.Time) {
	blockRepresentationID, err := permanentlyDeleteTrashedLibraryCandidate(h.db, trashLibraryCandidate{
		OrgID:        targetOrgID,
		LibraryID:    repoID,
		StorageClass: storageClass,
		DeletedAt:    deletedAt,
	}, "org_delete_trash_library", "DeleteOrgTrashLibrary", true)
	if writePermanentDeletePreconditionError(c, err) {
		return
	}
	if errors.Is(err, errDeleteRepresentationUnresolved) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to prepare library for permanent deletion"})
		return
	}
	if errors.Is(err, errDeleteLibraryLinksCleanup) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to clean library links"})
		return
	}
	if err != nil {
		if errors.Is(err, errHardDeleteLibraryReadModel) {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to clean library read model"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete library"})
		return
	}
	enqueueLibraryCascadeBestEffort(h.gcEnqueuer, targetOrgID, repoID, blockRepresentationID, storageClass, deletedAt)
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// processOrgTrashCandidates hard-deletes each candidate trashed library for the org-admin
// bulk clean-trash path and enqueues its durable, deduplicated library cascade — the same
// shared-writer wiring as the single-library org-admin delete (deleteResolvedTrashLibrary),
// run over an explicit candidate list. Splitting it from the org-wide SELECT lets the bulk
// loop be exercised with explicit candidates in tests without triggering org-wide side
// effects, and mirrors processAdminTrashCandidates so the two bulk paths cannot drift.
//
// Unlike the admin path it also cleans the library's share/upload links (org bulk always
// has). A per-library failure (unresolved block representation, link cleanup, or hard-delete)
// is counted and skipped so one bad library never strands the rest or aborts the whole batch;
// a skipped library keeps its live canonical row and is retried on the next clean.
func (h *OrgAdminHandler) processOrgTrashCandidates(candidates []trashLibraryCandidate, libEnqueuer LibraryGCEnqueuer) (cleaned, failed int) {
	for _, candidate := range candidates {
		blockRepresentationID, err := permanentlyDeleteTrashedLibraryCandidate(h.db, candidate, "org_clean_trash", "CleanOrgTrashLibraries", true)
		if errors.Is(err, errPermanentDeleteCandidateStale) {
			log.Printf("[CleanOrgTrashLibraries] skipping stale trash candidate %s/%s: canonical deleted_at no longer matches %s", candidate.OrgID, candidate.LibraryID, candidate.DeletedAt.Format(time.RFC3339Nano))
			failed++
			continue
		}
		if errors.Is(err, errPermanentDeleteInProgress) {
			log.Printf("[CleanOrgTrashLibraries] skipping %s/%s: permanent delete or restore already holds the hard-delete lease", candidate.OrgID, candidate.LibraryID)
			failed++
			continue
		}
		if errors.Is(err, errDeleteLibraryLinksCleanup) {
			log.Printf("[CleanOrgTrashLibraries] skipping %s/%s: failed to clean library links: %v", candidate.OrgID, candidate.LibraryID, err)
			failed++
			continue
		}
		if err != nil {
			log.Printf("[CleanOrgTrashLibraries] failed to delete library %s (org %s): %v", candidate.LibraryID, candidate.OrgID, err)
			failed++
			continue
		}
		enqueueLibraryCascadeBestEffort(libEnqueuer, candidate.OrgID, candidate.LibraryID, blockRepresentationID, candidate.StorageClass, candidate.DeletedAt)
		cleaned++
	}
	return cleaned, failed
}

func (h *AdminHandler) processAdminTrashCandidates(candidates []trashLibraryCandidate, libEnqueuer LibraryGCEnqueuer) (cleaned, failed int) {
	for _, candidate := range candidates {
		blockRepresentationID, err := permanentlyDeleteTrashedLibraryCandidate(h.db, candidate, "admin_clean_trash", "AdminCleanTrashLibraries", false)
		if errors.Is(err, errPermanentDeleteCandidateStale) {
			log.Printf("[AdminCleanTrashLibraries] skipping stale trash candidate %s/%s: canonical deleted_at no longer matches %s", candidate.OrgID, candidate.LibraryID, candidate.DeletedAt.Format(time.RFC3339Nano))
			failed++
			continue
		}
		if errors.Is(err, errPermanentDeleteInProgress) {
			log.Printf("[AdminCleanTrashLibraries] skipping %s/%s: permanent delete or restore already holds the hard-delete lease", candidate.OrgID, candidate.LibraryID)
			failed++
			continue
		}
		if err != nil {
			log.Printf("[AdminCleanTrashLibraries] failed to delete library %s (org %s): %v", candidate.LibraryID, candidate.OrgID, err)
			failed++
			continue
		}
		enqueueLibraryCascadeBestEffort(libEnqueuer, candidate.OrgID, candidate.LibraryID, blockRepresentationID, candidate.StorageClass, candidate.DeletedAt)
		runAsyncLibraryDeleteSideEffectFn(func() {
			if err := cleanupAllLibraryTagsForDeleteFn(h.db, candidate.LibraryID); err != nil {
				log.Printf("[AdminCleanTrashLibraries] failed to clean tag metadata for library %s: %v", candidate.LibraryID, err)
			}
		})
		cleaned++
	}
	return cleaned, failed
}
