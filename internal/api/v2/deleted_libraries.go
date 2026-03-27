package v2

import (
	"log"
	"net/http"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	"github.com/Sesame-Disk/sesamefs/internal/traffic"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
)

// RegisterDeletedLibraryRoutes registers routes for the library recycle bin
func RegisterDeletedLibraryRoutes(rg *gin.RouterGroup, database *db.DB, libHandler *LibraryHandler) {
	h := &DeletedLibraryHandler{
		db:             database,
		permMiddleware: middleware.NewPermissionMiddleware(database),
		libHandler:     libHandler,
	}

	// User-facing deleted libraries
	rg.GET("/deleted-repos", h.ListDeletedRepos)
	rg.GET("/deleted-repos/", h.ListDeletedRepos)

	repos := rg.Group("/repos")
	{
		// Restore a soft-deleted library
		repos.PUT("/deleted/:repo_id", h.RestoreDeletedRepo)
		repos.PUT("/deleted/:repo_id/", h.RestoreDeletedRepo)

		// Permanently delete a soft-deleted library
		repos.DELETE("/deleted/:repo_id", h.PermanentDeleteRepo)
		repos.DELETE("/deleted/:repo_id/", h.PermanentDeleteRepo)
	}
}

// DeletedLibraryHandler handles deleted library (recycle bin) endpoints
type DeletedLibraryHandler struct {
	db             *db.DB
	permMiddleware *middleware.PermissionMiddleware
	libHandler     *LibraryHandler
}

// DeletedRepoInfo represents a deleted library in API responses
type DeletedRepoInfo struct {
	RepoID   string `json:"repo_id"`
	RepoName string `json:"repo_name"`
	OwnerID  string `json:"owner"`
	DelTime  string `json:"del_time"`
	Size     int64  `json:"size"`
}

// ListDeletedRepos lists soft-deleted libraries for the current user
// GET /api/v2.1/deleted-repos/ or GET /api2/deleted-repos/
func (h *DeletedLibraryHandler) ListDeletedRepos(c *gin.Context) {
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	if orgID == "" || userID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing org_id or user_id"})
		return
	}

	if h.db == nil {
		c.JSON(http.StatusOK, []DeletedRepoInfo{})
		return
	}

	// Query all libraries for this org, filter for soft-deleted ones owned by user
	iter := h.db.Session().Query(`
		SELECT library_id, owner_id, name, size_bytes, deleted_at
		FROM libraries WHERE org_id = ?
	`, orgID).Iter()

	var repos []DeletedRepoInfo
	var libID, ownerID, name string
	var sizeBytes int64
	var deletedAt time.Time

	for iter.Scan(&libID, &ownerID, &name, &sizeBytes, &deletedAt) {
		// Only show deleted libraries owned by this user
		if deletedAt.IsZero() || ownerID != userID {
			continue
		}

		repos = append(repos, DeletedRepoInfo{
			RepoID:   libID,
			RepoName: name,
			OwnerID:  h.libHandler.resolveOwnerEmail(orgID, ownerID),
			DelTime:  deletedAt.Format(time.RFC3339),
			Size:     sizeBytes,
		})
	}
	iter.Close()

	if repos == nil {
		repos = []DeletedRepoInfo{}
	}

	c.JSON(http.StatusOK, repos)
}

// RestoreDeletedRepo restores a soft-deleted library
// PUT /api/v2.1/repos/deleted/:repo_id/
func (h *DeletedLibraryHandler) RestoreDeletedRepo(c *gin.Context) {
	repoID := c.Param("repo_id")
	callerOrgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	if callerOrgID == "" || repoID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing parameters"})
		return
	}

	// Resolve the library's actual org_id. Admins can manage libraries that
	// belong to any org, so we look up the real org via libraries_by_id first.
	orgID := callerOrgID
	var ownerID string
	var deletedAt time.Time
	err := h.db.Session().Query(`
		SELECT owner_id, deleted_at FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(&ownerID, &deletedAt)
	if err != nil {
		// Not found in caller's org.
		// Only superadmin can manage libraries across orgs; org admin is scoped to their own org.
		userRole := middleware.OrganizationRole(c.GetString("user_org_role"))
		if userRole != middleware.RoleSuperAdmin {
			c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
			return
		}
		var resolvedOrgID string
		if err2 := h.db.Session().Query(`
			SELECT org_id FROM libraries_by_id WHERE library_id = ?
		`, repoID).Scan(&resolvedOrgID); err2 != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
			return
		}
		orgID = resolvedOrgID
		// Re-fetch with the correct org_id
		if err3 := h.db.Session().Query(`
			SELECT owner_id, deleted_at FROM libraries WHERE org_id = ? AND library_id = ?
		`, orgID, repoID).Scan(&ownerID, &deletedAt); err3 != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
			return
		}
	}

	if deletedAt.IsZero() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "library is not deleted"})
		return
	}

	// Superadmin and org admin can restore any library in their scope; regular users only their own
	userRole := middleware.OrganizationRole(c.GetString("user_org_role"))
	isAdmin := middleware.HasRequiredOrgRole(userRole, middleware.RoleAdmin)
	if ownerID != userID && !isAdmin {
		c.JSON(http.StatusForbidden, gin.H{"error": "only library owner can restore"})
		return
	}

	// Restore: clear deleted_at + re-add library storage to aggregate counters.
	if err := restoreDeletedLibrary(h.db, orgID, ownerID, repoID); err != nil {
		log.Printf("[RestoreDeletedRepo] Failed to restore library %s: %v", repoID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to restore library"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// PermanentDeleteRepo permanently deletes a soft-deleted library.
//
// What gets cleaned up:
//   - File data: all commits, fs_objects, and blocks are enqueued for GC
//     (actual S3 deletion happens asynchronously after the grace period).
//   - Tag metadata: repo_tag_counters, file_tags, etc. are deleted async.
//   - Library rows: hard-deleted synchronously from libraries + libraries_by_id.
//
// Cleanup notes:
//   - shares (user-to-user and group shares keyed on library_id) — orphaned, not cleaned yet
//   - share_links (unified: share + upload + internal links) — cleaned via share_links_by_library lookup
//
// Note: GC enqueue only happens when libHandler is wired up (non-nil).
// See server.go RegisterDeletedLibraryRoutes call to verify.
//
// DELETE /api/v2.1/repos/deleted/:repo_id/
func (h *DeletedLibraryHandler) PermanentDeleteRepo(c *gin.Context) {
	repoID := c.Param("repo_id")
	callerOrgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	if callerOrgID == "" || repoID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing parameters"})
		return
	}

	// Resolve the library's actual org_id. Admins can manage libraries that
	// belong to any org, so we look up the real org via libraries_by_id first.
	orgID := callerOrgID
	var ownerID, storageClass string
	var deletedAt time.Time
	err := h.db.Session().Query(`
		SELECT owner_id, storage_class, deleted_at FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(&ownerID, &storageClass, &deletedAt)
	if err != nil {
		// Not found in caller's org.
		// Only superadmin can manage libraries across orgs; org admin is scoped to their own org.
		userRole := middleware.OrganizationRole(c.GetString("user_org_role"))
		if userRole != middleware.RoleSuperAdmin {
			c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
			return
		}
		var resolvedOrgID string
		if err2 := h.db.Session().Query(`
			SELECT org_id FROM libraries_by_id WHERE library_id = ?
		`, repoID).Scan(&resolvedOrgID); err2 != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
			return
		}
		orgID = resolvedOrgID
		// Re-fetch with the correct org_id
		if err3 := h.db.Session().Query(`
			SELECT owner_id, storage_class, deleted_at FROM libraries WHERE org_id = ? AND library_id = ?
		`, orgID, repoID).Scan(&ownerID, &storageClass, &deletedAt); err3 != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
			return
		}
	}

	if deletedAt.IsZero() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "library is not in trash"})
		return
	}

	// Only owner (or admin) can permanently delete
	if ownerID != userID {
		// Check if user is admin via context role set by auth middleware
		userRole := middleware.OrganizationRole(c.GetString("user_org_role"))
		if !middleware.HasRequiredOrgRole(userRole, middleware.RoleAdmin) {
			c.JSON(http.StatusForbidden, gin.H{"error": "only library owner or admin can permanently delete"})
			return
		}
	}

	// Clean up share/upload links before hard delete so derived tables do not
	// outlive the library row on partial failures.
	if err := cleanupLibraryLinks(h.db, orgID, repoID); err != nil {
		log.Printf("[PermanentDeleteRepo] Failed to clean share links for %s: %v", repoID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to clean share links"})
		return
	}

	// Hard delete the library records
	batch := h.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`DELETE FROM libraries WHERE org_id = ? AND library_id = ?`, orgID, repoID)
	batch.Query(`DELETE FROM libraries_by_id WHERE library_id = ?`, repoID)

	// Preserve the org lookup for the Garbage Collector
	batch.Query(`INSERT INTO deleted_libraries (library_id, org_id, deleted_at) VALUES (?, ?, ?)`, repoID, orgID, time.Now())

	if err := batch.Exec(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete library"})
		return
	}

	// Enqueue all library contents for GC after the delete marker is persisted.
	if h.libHandler != nil && h.libHandler.gcEnqueuer != nil {
		go h.libHandler.gcEnqueuer.EnqueueLibraryDeletion(orgID, repoID, storageClass)
	}

	// Clean up the lib-scope storage counter row. Aggregate scopes (org, user,
	// platform) were already adjusted when the library was soft-deleted.
	traffic.DeleteLibraryStorageCounter(h.db, orgID, repoID)

	// Tag cleanup is secondary metadata and can remain asynchronous.
	go CleanupAllLibraryTags(h.db, repoID)

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// cleanupLibraryLinks removes all share/upload links for a deleted library
// by scanning share_links_by_library and quad-deleting each link.
func cleanupLibraryLinks(db interface{ Session() *gocql.Session }, orgID, libraryID string) error {
	iter := db.Session().Query(`
		SELECT link_token, created_by, created_at FROM share_links_by_library
		WHERE org_id = ? AND library_id = ?
	`, orgID, libraryID).Iter()

	var linkToken, createdBy string
	var createdAt time.Time
	for iter.Scan(&linkToken, &createdBy, &createdAt) {
		batch := db.Session().Batch(gocql.LoggedBatch)
		batch.Query(`DELETE FROM share_links WHERE link_token = ?`, linkToken)
		batch.Query(`DELETE FROM share_links_by_creator WHERE org_id = ? AND created_by = ? AND created_at = ? AND link_token = ?`, orgID, createdBy, createdAt, linkToken)
		batch.Query(`DELETE FROM share_links_by_org WHERE org_id = ? AND created_at = ? AND link_token = ?`, orgID, createdAt, linkToken)
		batch.Query(`DELETE FROM share_links_by_library WHERE org_id = ? AND library_id = ? AND link_token = ?`, orgID, libraryID, linkToken)
		if err := batch.Exec(); err != nil {
			_ = iter.Close()
			return err
		}
	}
	if err := iter.Close(); err != nil {
		return err
	}
	return nil
}
