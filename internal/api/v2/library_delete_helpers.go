package v2

import (
	"errors"
	"log"
	"net/http"
	"time"

	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	"github.com/Sesame-Disk/sesamefs/internal/traffic"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
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
	errHardDeleteLibraryReadModel     = errors.New("hard delete library read model")
	errHardDeleteLibraryBatchExec     = errors.New("hard delete library batch exec")
)

func resolveDeleteRepresentationOrReject(c *gin.Context, database *dbpkg.DB, orgID, libraryID, metricOp, logPrefix string) (string, bool) {
	blockRepresentationID, err := resolveDeleteBlockRepresentationFn(database, orgID, libraryID)
	if err == nil {
		return blockRepresentationID, true
	}
	metrics.LibraryDeleteRepresentationResolutionFailures.WithLabelValues(metricOp).Inc()
	log.Printf("[%s] refusing to hard-delete %s/%s: block representation unresolved: %v", logPrefix, orgID, libraryID, err)
	c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to prepare library for permanent deletion"})
	return "", false
}

func (h *DeletedLibraryHandler) permanentDeleteResolvedRepo(c *gin.Context, orgID, repoID, storageClass string, deletedAt time.Time) {
	blockRepresentationID, ok := resolveDeleteRepresentationOrReject(c, h.db, orgID, repoID, "permanent_delete", "PermanentDeleteRepo")
	if !ok {
		return
	}
	if err := cleanupLibraryLinksForDeleteFn(h.db, orgID, repoID); err != nil {
		log.Printf("[PermanentDeleteRepo] Failed to clean share links for %s: %v", repoID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to clean share links"})
		return
	}
	if err := hardDeleteLibraryRowsFn(h.db, orgID, repoID, storageClass, blockRepresentationID, deletedAt); err != nil {
		if errors.Is(err, errHardDeleteLibraryReadModel) {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to clean library read model"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete library"})
		return
	}
	// Immediately queue the durable library cascade so reclamation starts on the next worker
	// tick instead of waiting up to a full ScanInterval for Phase 13. This is deduplicated
	// against Phase 13 (identical deletedAt identity), so it is not a second producer; the
	// durable purge_requested_at marker recovers it if this fire-and-forget enqueue is lost.
	// See migration 012 / ISSUE-GC-ORG-TRASH-NO-CASCADE-01.
	if h.libHandler != nil && h.libHandler.gcEnqueuer != nil {
		runAsyncLibraryDeleteSideEffectFn(func() {
			h.libHandler.gcEnqueuer.EnqueueLibraryCascade(orgID, repoID, blockRepresentationID, storageClass, deletedAt)
		})
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
	blockRepresentationID, ok := resolveDeleteRepresentationOrReject(c, h.db, targetOrgID, repoID, "org_delete_trash_library", "DeleteOrgTrashLibrary")
	if !ok {
		return
	}
	if err := cleanupLibraryLinksForDeleteFn(h.db, targetOrgID, repoID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to clean library links"})
		return
	}
	if err := hardDeleteLibraryRowsFn(h.db, targetOrgID, repoID, storageClass, blockRepresentationID, deletedAt); err != nil {
		if errors.Is(err, errHardDeleteLibraryReadModel) {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to clean library read model"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete library"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *AdminHandler) processAdminTrashCandidates(candidates []trashLibraryCandidate, libEnqueuer LibraryGCEnqueuer) (cleaned, failed int) {
	for _, candidate := range candidates {
		blockRepresentationID, err := resolveDeleteBlockRepresentationFn(h.db, candidate.OrgID, candidate.LibraryID)
		if err != nil {
			metrics.LibraryDeleteRepresentationResolutionFailures.WithLabelValues("admin_clean_trash").Inc()
			log.Printf("[AdminCleanTrashLibraries] skipping hard-delete of %s/%s: block representation unresolved: %v", candidate.OrgID, candidate.LibraryID, err)
			failed++
			continue
		}
		if err := hardDeleteLibraryRowsFn(h.db, candidate.OrgID, candidate.LibraryID, candidate.StorageClass, blockRepresentationID, candidate.DeletedAt); err != nil {
			log.Printf("[AdminCleanTrashLibraries] failed to delete library %s (org %s): %v", candidate.LibraryID, candidate.OrgID, err)
			failed++
			continue
		}
		// Immediately queue the durable, Phase-13-deduplicated cascade so reclamation starts
		// promptly rather than after up to a full ScanInterval; the purge_requested_at marker
		// recovers it if this best-effort enqueue is lost.
		if libEnqueuer != nil {
			candidate := candidate
			runAsyncLibraryDeleteSideEffectFn(func() {
				libEnqueuer.EnqueueLibraryCascade(candidate.OrgID, candidate.LibraryID, blockRepresentationID, candidate.StorageClass, candidate.DeletedAt)
			})
		}
		runAsyncLibraryDeleteSideEffectFn(func() {
			if err := cleanupAllLibraryTagsForDeleteFn(h.db, candidate.LibraryID); err != nil {
				log.Printf("[AdminCleanTrashLibraries] failed to clean tag metadata for library %s: %v", candidate.LibraryID, err)
			}
		})
		cleaned++
	}
	return cleaned, failed
}

