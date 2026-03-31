package v2

import (
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
)

// Repositories
// ============================================================================

// ListOrgRepos lists all repositories in the org with pagination.
// GET /org/:org_id/admin/repos/?page=N&per_page=N&order_by=
// Frontend reads: res.data.repo_list, res.data.page, res.data.page_next, res.data.page_info
func (h *OrgAdminHandler) ListOrgRepos(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	perPage, _ := strconv.Atoi(c.DefaultQuery("per_page", "25"))
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 25
	}

	iter := h.db.Session().Query(`
		SELECT library_id, owner_id, name, encrypted, size_bytes, file_count,
		       created_at, deleted_at
		FROM libraries WHERE org_id = ?
	`, targetOrgID).Iter()

	type repoRow struct {
		RepoID           string `json:"repo_id"`
		RepoName         string `json:"repo_name"`
		OwnerName        string `json:"owner_name"`
		OwnerEmail       string `json:"owner_email"`
		Size             int64  `json:"size"`
		FileCount        int64  `json:"file_count"`
		Encrypted        bool   `json:"encrypted"`
		IsDepartmentRepo bool   `json:"is_department_repo"`
		GroupID          *int   `json:"group_id"`
	}

	usersMap := h.resolveUsersMap(targetOrgID)

	var all []repoRow
	var libID, ownerID, name string
	var encrypted bool
	var sizeBytes, fileCount int64
	var createdAt, deletedAt time.Time

	for iter.Scan(&libID, &ownerID, &name, &encrypted, &sizeBytes, &fileCount,
		&createdAt, &deletedAt) {
		// Skip deleted libraries
		if !deletedAt.IsZero() {
			continue
		}
		u := usersMap[ownerID]
		all = append(all, repoRow{
			RepoID:           libID,
			RepoName:         name,
			OwnerName:        u.Name,
			OwnerEmail:       u.Email,
			Size:             sizeBytes,
			FileCount:        fileCount,
			Encrypted:        encrypted,
			IsDepartmentRepo: false,
			GroupID:          nil,
		})
	}
	iter.Close()

	if all == nil {
		all = []repoRow{}
	}

	// Apply ordering
	switch c.Query("order_by") {
	case "size":
		sort.Slice(all, func(i, j int) bool { return all[i].Size > all[j].Size })
	case "file_count":
		sort.Slice(all, func(i, j int) bool { return all[i].FileCount > all[j].FileCount })
	}

	// Paginate
	total := len(all)
	start := (page - 1) * perPage
	if start > total {
		start = total
	}
	end := start + perPage
	if end > total {
		end = total
	}
	pageData := all[start:end]

	hasNext := end < total

	c.JSON(http.StatusOK, gin.H{
		"repo_list": pageData,
		"page":      page,
		"page_next": hasNext,
		"page_info": gin.H{
			"current_page":  page,
			"has_next_page": hasNext,
		},
	})
}

// DeleteOrgRepo soft-deletes a repository.
// DELETE /org/:org_id/admin/repos/:rid/
func (h *OrgAdminHandler) DeleteOrgRepo(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}

	repoID := c.Param("rid")

	// Verify library exists and is not already deleted
	var deletedAt time.Time
	if err := h.db.Session().Query(`
		SELECT deleted_at FROM libraries WHERE org_id = ? AND library_id = ?
	`, targetOrgID, repoID).Scan(&deletedAt); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}
	if !deletedAt.IsZero() {
		c.JSON(http.StatusNotFound, gin.H{"error": "library already deleted"})
		return
	}

	// Soft-delete
	callerUserID := c.GetString("user_id")
	now := time.Now()
	if err := h.db.Session().Query(`
		UPDATE libraries SET deleted_at = ?, deleted_by = ?
		WHERE org_id = ? AND library_id = ?
	`, now, callerUserID, targetOrgID, repoID).Exec(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete library"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// TransferOrgRepo transfers a repository to another user.
// PUT /org/:org_id/admin/repos/:rid/
func (h *OrgAdminHandler) TransferOrgRepo(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}

	repoID := c.Param("rid")
	var req struct {
		Email string `json:"email"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	newOwnerEmail := req.Email
	if newOwnerEmail == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "email is required"})
		return
	}

	// Verify library exists
	var deletedAt time.Time
	if err := h.db.Session().Query(`
		SELECT deleted_at FROM libraries WHERE org_id = ? AND library_id = ?
	`, targetOrgID, repoID).Scan(&deletedAt); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}
	if !deletedAt.IsZero() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot transfer a deleted library"})
		return
	}

	// Resolve new owner
	newOwnerID, err := h.lookupOrgUserByEmail(targetOrgID, newOwnerEmail)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found in this organization"})
		return
	}

	now := time.Now()
	if err := updateLibraryOwner(h.db, targetOrgID, repoID, newOwnerID, now); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to transfer library"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ListOrgRepoDirents lists directory entries for a library.
// GET /org/:org_id/admin/repos/:rid/dirents/?path=/
// Frontend reads: res.data.dirent_list
func (h *OrgAdminHandler) ListOrgRepoDirents(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}

	libraryID := c.Param("rid")
	dirPath := c.DefaultQuery("path", "/")
	if dirPath == "" {
		dirPath = "/"
	}

	// Verify library belongs to this org
	var headCommitID string
	if err := h.db.Session().Query(`
		SELECT head_commit_id FROM libraries WHERE org_id = ? AND library_id = ?
	`, targetOrgID, libraryID).Scan(&headCommitID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}
	if headCommitID == "" {
		c.JSON(http.StatusOK, gin.H{"dirent_list": []interface{}{}})
		return
	}

	// Get root_fs_id from head commit
	var rootFSID string
	if err := h.db.Session().Query(`
		SELECT root_fs_id FROM commits WHERE library_id = ? AND commit_id = ?
	`, libraryID, headCommitID).Scan(&rootFSID); err != nil {
		c.JSON(http.StatusOK, gin.H{"dirent_list": []interface{}{}})
		return
	}

	type fsEntry struct {
		Name  string `json:"name"`
		ID    string `json:"id"`
		Mode  int64  `json:"mode"`
		Mtime int64  `json:"mtime"`
		Size  int64  `json:"size"`
	}

	// Traverse to requested path
	currentFSID := rootFSID
	if dirPath != "/" {
		parts := strings.Split(strings.Trim(dirPath, "/"), "/")
		for _, part := range parts {
			if part == "" {
				continue
			}
			var entriesJSON string
			if err := h.db.Session().Query(`
				SELECT dir_entries FROM fs_objects WHERE library_id = ? AND fs_id = ?
			`, libraryID, currentFSID).Scan(&entriesJSON); err != nil {
				c.JSON(http.StatusNotFound, gin.H{"error": "directory not found"})
				return
			}

			var entries []fsEntry
			if entriesJSON != "" && entriesJSON != "[]" {
				if err := json.Unmarshal([]byte(entriesJSON), &entries); err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid directory data"})
					return
				}
			}

			found := false
			for _, e := range entries {
				if e.Name == part && (e.Mode&0040000) != 0 {
					currentFSID = e.ID
					found = true
					break
				}
			}
			if !found {
				c.JSON(http.StatusNotFound, gin.H{"error": "directory not found"})
				return
			}
		}
	}

	// Read entries at current path
	var entriesJSON string
	if err := h.db.Session().Query(`
		SELECT dir_entries FROM fs_objects WHERE library_id = ? AND fs_id = ?
	`, libraryID, currentFSID).Scan(&entriesJSON); err != nil {
		c.JSON(http.StatusOK, gin.H{"dirent_list": []interface{}{}})
		return
	}

	var entries []fsEntry
	if entriesJSON != "" && entriesJSON != "[]" {
		if err := json.Unmarshal([]byte(entriesJSON), &entries); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid directory data"})
			return
		}
	}

	var dirents []gin.H
	for _, e := range entries {
		isDir := (e.Mode & 0040000) != 0
		entryType := "file"
		if isDir {
			entryType = "dir"
		}
		entryPath := dirPath
		if !strings.HasSuffix(entryPath, "/") {
			entryPath += "/"
		}
		entryPath += e.Name

		dirents = append(dirents, gin.H{
			"type":   entryType,
			"name":   e.Name,
			"id":     e.ID,
			"mtime":  e.Mtime,
			"size":   e.Size,
			"path":   entryPath,
			"is_dir": isDir,
		})
	}

	if dirents == nil {
		dirents = []gin.H{}
	}
	c.JSON(http.StatusOK, gin.H{"dirent_list": dirents})
}

// ============================================================================
// Trash libraries
// ============================================================================

// ListOrgTrashLibraries lists soft-deleted libraries.
// GET /org/:org_id/admin/trash-libraries/?page=N&per_page=N
// Frontend reads: res.data.repos, res.data.page_info
func (h *OrgAdminHandler) ListOrgTrashLibraries(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	perPage, _ := strconv.Atoi(c.DefaultQuery("per_page", "25"))
	if page < 1 {
		page = 1
	}
	if perPage < 1 || perPage > 100 {
		perPage = 25
	}

	iter := h.db.Session().Query(`
		SELECT library_id, owner_id, name, encrypted, size_bytes, deleted_at
		FROM libraries WHERE org_id = ?
	`, targetOrgID).Iter()

	usersMap := h.resolveUsersMap(targetOrgID)

	var trashed []gin.H
	var libID, ownerID, name string
	var encrypted bool
	var sizeBytes int64
	var deletedAt time.Time

	for iter.Scan(&libID, &ownerID, &name, &encrypted, &sizeBytes, &deletedAt) {
		if deletedAt.IsZero() {
			continue
		}
		u := usersMap[ownerID]
		trashed = append(trashed, gin.H{
			"id":          libID,
			"name":        name,
			"owner":       u.Email,
			"owner_name":  u.Name,
			"group_name":  "",
			"delete_time": deletedAt.Format(time.RFC3339),
			"encrypted":   encrypted,
		})
	}
	iter.Close()

	if trashed == nil {
		trashed = []gin.H{}
	}

	// Paginate
	total := len(trashed)
	start := (page - 1) * perPage
	if start > total {
		start = total
	}
	end := start + perPage
	if end > total {
		end = total
	}

	c.JSON(http.StatusOK, gin.H{
		"repos": trashed[start:end],
		"page_info": gin.H{
			"current_page":  page,
			"has_next_page": end < total,
		},
	})
}

// CleanOrgTrashLibraries permanently deletes all trashed libraries in the org.
// DELETE /org/:org_id/admin/trash-libraries/
func (h *OrgAdminHandler) CleanOrgTrashLibraries(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}

	iter := h.db.Session().Query(`
		SELECT library_id, deleted_at FROM libraries WHERE org_id = ?
	`, targetOrgID).Iter()

	var libID string
	var deletedAt time.Time
	cleaned := 0

	for iter.Scan(&libID, &deletedAt) {
		if deletedAt.IsZero() {
			continue
		}
		if err := cleanupLibraryLinks(h.db, targetOrgID, libID); err != nil {
			iter.Close()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to clean library links"})
			return
		}
		// Hard-delete library rows + preserve org lookup for GC
		batch := h.db.Session().Batch(gocql.LoggedBatch)
		batch.Query(`DELETE FROM libraries WHERE org_id = ? AND library_id = ?`, targetOrgID, libID)
		batch.Query(`DELETE FROM libraries_by_id WHERE library_id = ?`, libID)
		batch.Query(`INSERT INTO deleted_libraries (library_id, org_id, deleted_at) VALUES (?, ?, ?)`, libID, targetOrgID, time.Now())
		if err := batch.Exec(); err != nil {
			iter.Close()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete library"})
			return
		}
		cleaned++
	}
	if err := iter.Close(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to scan trash libraries"})
		return
	}

	log.Printf("[CleanOrgTrashLibraries] Cleaned %d trashed libraries in org %s", cleaned, targetOrgID)
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// DeleteOrgTrashLibrary permanently deletes a single trashed library.
// DELETE /org/:org_id/admin/trash-libraries/:rid/
func (h *OrgAdminHandler) DeleteOrgTrashLibrary(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}

	repoID := c.Param("rid")

	// Verify it's actually trashed
	var deletedAt time.Time
	if err := h.db.Session().Query(`
		SELECT deleted_at FROM libraries WHERE org_id = ? AND library_id = ?
	`, targetOrgID, repoID).Scan(&deletedAt); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}
	if deletedAt.IsZero() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "library is not in trash"})
		return
	}

	if err := cleanupLibraryLinks(h.db, targetOrgID, repoID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to clean library links"})
		return
	}

	// Hard-delete + preserve org lookup for GC
	batch := h.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`DELETE FROM libraries WHERE org_id = ? AND library_id = ?`, targetOrgID, repoID)
	batch.Query(`DELETE FROM libraries_by_id WHERE library_id = ?`, repoID)
	batch.Query(`INSERT INTO deleted_libraries (library_id, org_id, deleted_at) VALUES (?, ?, ?)`, repoID, targetOrgID, time.Now())
	if err := batch.Exec(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete library"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// RestoreOrgTrashLibrary restores a trashed library.
// PUT /org/:org_id/admin/trash-libraries/:rid/
func (h *OrgAdminHandler) RestoreOrgTrashLibrary(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}

	repoID := c.Param("rid")

	// Verify it's actually trashed
	var deletedAt time.Time
	if err := h.db.Session().Query(`
		SELECT deleted_at FROM libraries WHERE org_id = ? AND library_id = ?
	`, targetOrgID, repoID).Scan(&deletedAt); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}
	if deletedAt.IsZero() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "library is not in trash"})
		return
	}

	// Clear deleted_at and deleted_by to restore
	err := h.db.Session().Query(`
		DELETE deleted_at, deleted_by FROM libraries WHERE org_id = ? AND library_id = ?
	`, targetOrgID, repoID).Exec()
	if err != nil {
		log.Printf("[RestoreOrgTrashLibrary] Failed to restore library %s: %v", repoID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to restore library"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ============================================================================
