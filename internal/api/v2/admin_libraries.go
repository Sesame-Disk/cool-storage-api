package v2

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// =============================================================================
// Phase 3: Admin Library Management Endpoints
// =============================================================================

// adminLibraryResponse is the response format for admin library endpoints.
// Field names must match what the Seahub sys-admin frontend expects.
type adminLibraryResponse struct {
	ID          string `json:"id"`
	RepoID      string `json:"repo_id"`
	Name        string `json:"name"`
	RepoName    string `json:"repo_name"`
	OwnerEmail  string `json:"owner_email"`
	OwnerName   string `json:"owner_name"`
	Size        int64  `json:"size"`
	FileCount   int64  `json:"file_count"`
	Encrypted   bool   `json:"encrypted"`
	Permission  string `json:"permission"`
	StorageName string `json:"storage_name,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
	UpdatedAt   string `json:"updated_at,omitempty"`
}

// resolveOwnerEmail looks up the user's email by user_id. Falls back to user_id@sesamefs.local.
func (h *AdminHandler) resolveOwnerEmail(orgID, ownerID string) string {
	var email string
	err := h.db.Session().Query(`
		SELECT email FROM users WHERE org_id = ? AND user_id = ?
	`, orgID, ownerID).Scan(&email)
	if err != nil || email == "" {
		return ownerID + "@sesamefs.local"
	}
	return email
}

// resolveOwnerName returns the display name for a user. Falls back to the local part of email.
func (h *AdminHandler) resolveOwnerName(orgID, ownerID string) string {
	var name, email string
	h.db.Session().Query(`
		SELECT email, name FROM users WHERE org_id = ? AND user_id = ?
	`, orgID, ownerID).Scan(&email, &name)
	if name != "" {
		return name
	}
	if email != "" {
		return strings.Split(email, "@")[0]
	}
	return ownerID
}

// AdminListAllLibraries lists all libraries visible to the admin.
// adminListLibrariesSharedToUser handles GET /admin/libraries/?shared_to=email
// It returns all libraries that have been directly shared to the given user.
func (h *AdminHandler) adminListLibrariesSharedToUser(c *gin.Context, email string) {
	targetUserID, targetOrgID, err := h.lookupUserByEmail(email)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"repo_list": []gin.H{}})
		return
	}

	shareIter := h.db.Session().Query(`
		SELECT library_id, permission FROM shares_by_user
		WHERE shared_to = ?
	`, targetUserID).Iter()

	var repoList []gin.H
	var libID, perm string

	for shareIter.Scan(&libID, &perm) {
		var name, ownerID string
		var encrypted bool
		var sizeBytes int64
		var updatedAt, deletedAt time.Time
		if err := h.db.Session().Query(`
			SELECT name, owner_id, encrypted, size_bytes, updated_at, deleted_at
			FROM libraries WHERE org_id = ? AND library_id = ?
		`, targetOrgID, libID).Scan(&name, &ownerID, &encrypted, &sizeBytes, &updatedAt, &deletedAt); err != nil {
			continue
		}
		if !deletedAt.IsZero() {
			continue
		}
		ownerEmail := h.resolveOwnerEmail(targetOrgID, ownerID)
		ownerName := h.resolveOwnerName(targetOrgID, ownerID)
		repoList = append(repoList, gin.H{
			"id":          libID,
			"name":        name,
			"owner_email": ownerEmail,
			"owner_name":  ownerName,
			"size":        sizeBytes,
			"last_modify": updatedAt.Format(time.RFC3339),
			"encrypted":   encrypted,
			"permission":  perm,
		})
	}
	shareIter.Close()

	if repoList == nil {
		repoList = []gin.H{}
	}
	c.JSON(http.StatusOK, gin.H{"repo_list": repoList})
}

// GET /admin/libraries/?page=&per_page=&order_by=
func (h *AdminHandler) AdminListAllLibraries(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")

	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	// Handle shared_to filter: return only libs shared to a specific user
	if sharedTo := c.Query("shared_to"); sharedTo != "" {
		h.adminListLibrariesSharedToUser(c, sharedTo)
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

	// Determine which orgs to query: platform superadmin may query all orgs.
	callerRole, _ := h.permMiddleware.GetUserOrgRole(callerOrgID, callerUserID)
	var orgIDs []string
	if middleware.IsPlatformSuperAdmin(callerOrgID, callerRole) {
		// Superadmin: query all orgs
		orgIter := h.db.Session().Query(`SELECT org_id FROM organizations`).Iter()
		var oid string
		for orgIter.Scan(&oid) {
			orgIDs = append(orgIDs, oid)
		}
		orgIter.Close()
	} else {
		orgIDs = []string{callerOrgID}
	}

	// Collect all libraries across target orgs
	var allLibs []adminLibraryResponse
	for _, orgID := range orgIDs {
		iter := h.db.Session().Query(`
			SELECT library_id, owner_id, name, encrypted, storage_class,
			       size_bytes, file_count, created_at, updated_at, deleted_at
			FROM libraries WHERE org_id = ?
		`, orgID).Iter()

		var libID, ownerID, name, storageClass string
		var encrypted bool
		var sizeBytes, fileCount int64
		var createdAt, updatedAt, deletedAt time.Time

		for iter.Scan(&libID, &ownerID, &name, &encrypted, &storageClass,
			&sizeBytes, &fileCount, &createdAt, &updatedAt, &deletedAt) {
			if !deletedAt.IsZero() {
				continue
			}
			ownerEmail := h.resolveOwnerEmail(orgID, ownerID)
			ownerName := h.resolveOwnerName(orgID, ownerID)
			allLibs = append(allLibs, adminLibraryResponse{
				ID:          libID,
				RepoID:      libID,
				Name:        name,
				RepoName:    name,
				OwnerEmail:  ownerEmail,
				Permission:  "rw", // Admin always has rw over all libraries
				OwnerName:   ownerName,
				Size:        sizeBytes,
				FileCount:   fileCount,
				Encrypted:   encrypted,
				StorageName: storageClass,
				CreatedAt:   createdAt.Format(time.RFC3339),
				UpdatedAt:   updatedAt.Format(time.RFC3339),
			})
		}
		iter.Close()
	}

	if allLibs == nil {
		allLibs = []adminLibraryResponse{}
	}

	// Apply ordering
	orderBy := c.Query("order_by")
	if orderBy == "size" {
		// Sort descending by size
		for i := 0; i < len(allLibs); i++ {
			for j := i + 1; j < len(allLibs); j++ {
				if allLibs[j].Size > allLibs[i].Size {
					allLibs[i], allLibs[j] = allLibs[j], allLibs[i]
				}
			}
		}
	}

	// Paginate
	total := len(allLibs)
	start := (page - 1) * perPage
	if start > total {
		start = total
	}
	end := start + perPage
	if end > total {
		end = total
	}
	pageLibs := allLibs[start:end]

	hasNextPage := end < total

	c.JSON(http.StatusOK, gin.H{
		"repos": pageLibs,
		"page_info": gin.H{
			"has_next_page": hasNextPage,
			"current_page":  page,
		},
	})
}

// AdminSearchLibraries searches libraries by name or ID.
// GET /admin/search-libraries/?name_or_id=&page=&per_page=
func (h *AdminHandler) AdminSearchLibraries(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")

	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	query := strings.TrimSpace(c.Query("name_or_id"))
	log.Printf("[AdminSearchLibraries] query=%q, orgID=%s, userID=%s", query, callerOrgID, callerUserID)
	if query == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name_or_id parameter is required"})
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

	callerRole, _ := h.permMiddleware.GetUserOrgRole(callerOrgID, callerUserID)
	var orgIDs []string
	if middleware.IsPlatformSuperAdmin(callerOrgID, callerRole) {
		orgIter := h.db.Session().Query(`SELECT org_id FROM organizations`).Iter()
		var oid string
		for orgIter.Scan(&oid) {
			orgIDs = append(orgIDs, oid)
		}
		orgIter.Close()
	} else {
		orgIDs = []string{callerOrgID}
	}

	queryLower := strings.ToLower(query)

	var results []adminLibraryResponse
	for _, orgID := range orgIDs {
		iter := h.db.Session().Query(`
			SELECT library_id, owner_id, name, encrypted, storage_class,
			       size_bytes, file_count, created_at, updated_at, deleted_at
			FROM libraries WHERE org_id = ?
		`, orgID).Iter()

		var libID, ownerID, name, storageClass string
		var encrypted bool
		var sizeBytes, fileCount int64
		var createdAt, updatedAt, deletedAt time.Time

		for iter.Scan(&libID, &ownerID, &name, &encrypted, &storageClass,
			&sizeBytes, &fileCount, &createdAt, &updatedAt, &deletedAt) {
			if !deletedAt.IsZero() {
				continue
			}
			// Match by name (case-insensitive substring) or by ID (exact or prefix)
			libIDLower := strings.ToLower(libID)
			if strings.Contains(strings.ToLower(name), queryLower) ||
				strings.HasPrefix(libIDLower, queryLower) || libIDLower == queryLower {
				ownerEmail := h.resolveOwnerEmail(orgID, ownerID)
				ownerName := h.resolveOwnerName(orgID, ownerID)
				results = append(results, adminLibraryResponse{
					ID:          libID,
					Name:        name,
					OwnerEmail:  ownerEmail,
					Permission:  "rw", // Admin always has rw over all libraries
					OwnerName:   ownerName,
					Size:        sizeBytes,
					FileCount:   fileCount,
					Encrypted:   encrypted,
					StorageName: storageClass,
					CreatedAt:   createdAt.Format(time.RFC3339),
					UpdatedAt:   updatedAt.Format(time.RFC3339),
				})
			}
		}
		iter.Close()
	}

	if results == nil {
		results = []adminLibraryResponse{}
	}

	log.Printf("[AdminSearchLibraries] found %d results for query=%q across %d orgs", len(results), query, len(orgIDs))

	// Paginate
	total := len(results)
	start := (page - 1) * perPage
	if start > total {
		start = total
	}
	end := start + perPage
	if end > total {
		end = total
	}

	c.JSON(http.StatusOK, gin.H{
		"repo_list": results[start:end],
		"page_info": gin.H{
			"has_next_page": end < total,
			"current_page":  page,
		},
	})
}

// AdminGetLibrary returns details for a single library.
// GET /admin/libraries/:library_id/
func (h *AdminHandler) AdminGetLibrary(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")

	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	libraryID := c.Param("library_id")
	if _, err := uuid.Parse(libraryID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid library_id"})
		return
	}

	// Lookup org_id for this library via libraries_by_id
	var orgID string
	if err := h.db.Session().Query(`
		SELECT org_id FROM libraries_by_id WHERE library_id = ?
	`, libraryID).Scan(&orgID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}

	var libID, ownerID, name, description, storageClass, headCommitID string
	var encrypted bool
	var sizeBytes, fileCount int64
	var versionTTLDays int
	var createdAt, updatedAt, deletedAt time.Time

	if err := h.db.Session().Query(`
		SELECT library_id, owner_id, name, description, encrypted,
		       storage_class, size_bytes, file_count, version_ttl_days,
		       head_commit_id, created_at, updated_at, deleted_at
		FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, libraryID).Scan(
		&libID, &ownerID, &name, &description, &encrypted,
		&storageClass, &sizeBytes, &fileCount, &versionTTLDays,
		&headCommitID, &createdAt, &updatedAt, &deletedAt,
	); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}

	if !deletedAt.IsZero() {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}

	ownerEmail := h.resolveOwnerEmail(orgID, ownerID)
	ownerName := h.resolveOwnerName(orgID, ownerID)

	c.JSON(http.StatusOK, gin.H{
		"id":               libID,
		"name":             name,
		"desc":             description,
		"owner":            ownerEmail,
		"owner_name":       ownerName,
		"size":             sizeBytes,
		"file_count":       fileCount,
		"encrypted":        encrypted,
		"storage_name":     storageClass,
		"head_commit_id":   headCommitID,
		"version_ttl_days": versionTTLDays,
		"created_at":       createdAt.Format(time.RFC3339),
		"updated_at":       updatedAt.Format(time.RFC3339),
	})
}

// AdminDeleteLibrary soft-deletes a library (admin privilege — no owner check).
// DELETE /admin/libraries/:library_id/
func (h *AdminHandler) AdminDeleteLibrary(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")

	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	libraryID := c.Param("library_id")
	if _, err := uuid.Parse(libraryID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid library_id"})
		return
	}

	// Lookup org_id
	var orgID string
	if err := h.db.Session().Query(`
		SELECT org_id FROM libraries_by_id WHERE library_id = ?
	`, libraryID).Scan(&orgID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}

	// Verify library exists and is not already deleted; fetch owner for storage accounting.
	var ownerID string
	var deletedAt time.Time
	if err := h.db.Session().Query(`
		SELECT owner_id, deleted_at FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, libraryID).Scan(&ownerID, &deletedAt); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}
	if !deletedAt.IsZero() {
		c.JSON(http.StatusNotFound, gin.H{"error": "library already deleted"})
		return
	}

	// Soft-delete + adjust storage counters.
	if err := softDeleteLibrary(h.db, orgID, ownerID, callerUserID, libraryID); err != nil {
		log.Printf("[AdminDeleteLibrary] Failed to delete library %s: %v", libraryID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete library"})
		return
	}

	log.Printf("[AdminDeleteLibrary] Admin %s deleted library %s", callerUserID, libraryID)
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// AdminCreateLibrary creates a new library on behalf of a user (admin privilege).
// POST /admin/libraries/  FormData: name, owner (email)
func (h *AdminHandler) AdminCreateLibrary(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")

	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	var createLibReq struct {
		Name  string `json:"name"`
		Owner string `json:"owner"`
	}
	if err := c.ShouldBindJSON(&createLibReq); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	repoName := createLibReq.Name
	ownerEmail := createLibReq.Owner

	if repoName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}
	if ownerEmail == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "owner email is required"})
		return
	}

	// Lookup owner by email
	ownerUserID, ownerOrgID, err := h.lookupUserByEmail(ownerEmail)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "owner user not found"})
		return
	}

	newLibID := uuid.New()
	now := time.Now()

	// Create empty root directory
	emptyDirEntries := "[]"
	emptyDirData := fmt.Sprintf("%d\n%s", 1, emptyDirEntries)
	emptyDirHash := sha1.Sum([]byte(emptyDirData))
	rootFSID := hex.EncodeToString(emptyDirHash[:])

	commitData := fmt.Sprintf("%s:%s:%d", newLibID.String(), repoName, now.UnixNano())
	commitHash := sha1.Sum([]byte(commitData))
	headCommitID := hex.EncodeToString(commitHash[:])

	storageClass := "default"
	if h.config != nil && h.config.Storage.DefaultClass != "" {
		storageClass = h.config.Storage.DefaultClass
	}
	versionTTLDays := 90
	if h.config != nil && h.config.Versioning.DefaultTTLDays > 0 {
		versionTTLDays = h.config.Versioning.DefaultTTLDays
	}

	batch := h.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		INSERT INTO fs_objects (library_id, fs_id, obj_type, obj_name, dir_entries, mtime)
		VALUES (?, ?, ?, ?, ?, ?)
	`, newLibID.String(), rootFSID, "dir", "", emptyDirEntries, now.Unix())
	batch.Query(`
		INSERT INTO libraries (
			org_id, library_id, owner_id, name, description, encrypted,
			storage_class, size_bytes, file_count, version_ttl_days,
			head_commit_id, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, ownerOrgID, newLibID.String(), ownerUserID, repoName,
		"", false, storageClass, int64(0), int64(0), versionTTLDays,
		headCommitID, now, now,
	)
	batch.Query(`
		INSERT INTO libraries_by_id (
			library_id, org_id, owner_id, name, head_commit_id, encrypted
		) VALUES (?, ?, ?, ?, ?, ?)
	`, newLibID.String(), ownerOrgID, ownerUserID, repoName, headCommitID, false,
	)
	batch.Query(`
		INSERT INTO commits (library_id, commit_id, root_fs_id, creator_id, description, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, newLibID.String(), headCommitID, rootFSID, callerUserID, "Initial commit", now)

	if err := batch.Exec(); err != nil {
		log.Printf("[AdminCreateLibrary] Failed to create library: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create library"})
		return
	}

	log.Printf("[AdminCreateLibrary] Admin %s created library %s for user %s", callerUserID, newLibID.String(), ownerEmail)

	ownerName := h.resolveOwnerName(ownerOrgID, ownerUserID)
	c.JSON(http.StatusOK, adminLibraryResponse{
		ID:         newLibID.String(),
		Name:       repoName,
		OwnerEmail: ownerEmail,
		OwnerName:  ownerName,
		Size:       0,
		FileCount:  0,
		Encrypted:  false,
		Permission: "rw",
		CreatedAt:  now.Format(time.RFC3339),
		UpdatedAt:  now.Format(time.RFC3339),
	})
}

// AdminTransferLibrary transfers library ownership to another user.
// PUT /admin/libraries/:library_id/transfer/  FormData: owner (email)
func (h *AdminHandler) AdminTransferLibrary(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")

	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	libraryID := c.Param("library_id")
	if _, err := uuid.Parse(libraryID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid library_id"})
		return
	}

	var transferLibReq struct {
		Owner string `json:"owner"`
	}
	if err := c.ShouldBindJSON(&transferLibReq); err != nil || transferLibReq.Owner == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "owner email is required"})
		return
	}
	newOwnerEmail := transferLibReq.Owner

	// Lookup library's org
	var orgID string
	if err := h.db.Session().Query(`
		SELECT org_id FROM libraries_by_id WHERE library_id = ?
	`, libraryID).Scan(&orgID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}

	// Lookup new owner
	newOwnerID, newOwnerOrgID, err := h.lookupUserByEmail(newOwnerEmail)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "new owner user not found"})
		return
	}

	// New owner must be in the same org as the library
	if newOwnerOrgID != orgID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "new owner must be in the same organization as the library"})
		return
	}

	now := time.Now()
	if err := updateLibraryOwner(h.db, orgID, libraryID, newOwnerID, now); err != nil {
		log.Printf("[AdminTransferLibrary] Failed to transfer owner: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to transfer library"})
		return
	}

	log.Printf("[AdminTransferLibrary] Admin %s transferred library %s to %s", callerUserID, libraryID, newOwnerEmail)
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// AdminGetHistorySetting returns the history setting for a library.
// GET /admin/libraries/:library_id/history-setting/
func (h *AdminHandler) AdminGetHistorySetting(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")

	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	libraryID := c.Param("library_id")
	if _, err := uuid.Parse(libraryID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid library_id"})
		return
	}

	// Lookup org
	var orgID string
	if err := h.db.Session().Query(`
		SELECT org_id FROM libraries_by_id WHERE library_id = ?
	`, libraryID).Scan(&orgID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}

	var versionTTLDays int
	if err := h.db.Session().Query(`
		SELECT version_ttl_days FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, libraryID).Scan(&versionTTLDays); err != nil {
		c.JSON(http.StatusOK, gin.H{"keep_days": -1})
		return
	}

	keepDays := versionTTLDays
	if keepDays == 0 {
		keepDays = -1
	}
	c.JSON(http.StatusOK, gin.H{"keep_days": keepDays})
}

// AdminUpdateHistorySetting updates the history setting for a library.
// PUT /admin/libraries/:library_id/history-setting/  FormData: keep_days
func (h *AdminHandler) AdminUpdateHistorySetting(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")

	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	libraryID := c.Param("library_id")
	if _, err := uuid.Parse(libraryID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid library_id"})
		return
	}

	var req struct {
		KeepDays int `json:"keep_days" form:"keep_days"`
	}
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if req.KeepDays < -1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "keep_days must be -1 (all), 0 (none), or a positive integer"})
		return
	}

	var orgID string
	if err := h.db.Session().Query(`
		SELECT org_id FROM libraries_by_id WHERE library_id = ?
	`, libraryID).Scan(&orgID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}

	if err := h.db.Session().Query(`
		UPDATE libraries SET version_ttl_days = ?, updated_at = ?
		WHERE org_id = ? AND library_id = ?
	`, req.KeepDays, time.Now(), orgID, libraryID).Exec(); err != nil {
		log.Printf("[AdminUpdateHistorySetting] Failed to update: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update history setting"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"keep_days": req.KeepDays})
}

// AdminGetDownloadLink returns a download URL for a file in a library (admin privilege).
// GET /admin/libraries/:library_id/download-link/?path=
func (h *AdminHandler) AdminGetDownloadLink(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")

	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	libraryID := c.Param("library_id")
	if _, err := uuid.Parse(libraryID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid library_id"})
		return
	}

	filePath := c.Query("path")
	if filePath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is required"})
		return
	}

	// Lookup org for this library
	var orgID string
	if err := h.db.Session().Query(`
		SELECT org_id FROM libraries_by_id WHERE library_id = ?
	`, libraryID).Scan(&orgID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}

	// Get the library owner so the download token passes permission checks
	var ownerID string
	if err := h.db.Session().Query(`
		SELECT owner_id FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, libraryID).Scan(&ownerID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}

	if h.tokenCreator == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "service not available"})
		return
	}

	token, err := h.tokenCreator.CreateDownloadToken(orgID, libraryID, filePath, ownerID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate download link"})
		return
	}

	filename := filePath
	if idx := strings.LastIndex(filePath, "/"); idx >= 0 {
		filename = filePath[idx+1:]
	}
	downloadURL := fmt.Sprintf("%s/seafhttp/files/%s/%s", getBrowserURL(c, h.serverURL), token, filename)
	c.JSON(http.StatusOK, gin.H{"download_url": downloadURL})
}

// AdminListDirents lists directory entries for a library (admin privilege).
// GET /admin/libraries/:library_id/dirents/?path=
func (h *AdminHandler) AdminListDirents(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")

	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	libraryID := c.Param("library_id")
	if _, err := uuid.Parse(libraryID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid library_id"})
		return
	}

	dirPath := c.DefaultQuery("path", "/")
	if dirPath == "" {
		dirPath = "/"
	}

	// Lookup org
	var orgID string
	if err := h.db.Session().Query(`
		SELECT org_id FROM libraries_by_id WHERE library_id = ?
	`, libraryID).Scan(&orgID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}

	// Get library head commit and name
	var headCommitID string
	var repoName string
	if err := h.db.Session().Query(`
		SELECT head_commit_id, name FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, libraryID).Scan(&headCommitID, &repoName); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}
	if headCommitID == "" {
		c.JSON(http.StatusOK, gin.H{
			"dirent_list":       []interface{}{},
			"repo_name":         repoName,
			"is_system_library": false,
		})
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

			type fsEntry struct {
				Name  string `json:"name"`
				ID    string `json:"id"`
				Mode  int64  `json:"mode"`
				Mtime int64  `json:"mtime"`
				Size  int64  `json:"size"`
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

	type fsEntry struct {
		Name  string `json:"name"`
		ID    string `json:"id"`
		Mode  int64  `json:"mode"`
		Mtime int64  `json:"mtime"`
		Size  int64  `json:"size"`
	}
	var entries []fsEntry
	if entriesJSON != "" && entriesJSON != "[]" {
		if err := json.Unmarshal([]byte(entriesJSON), &entries); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid directory data"})
			return
		}
	}

	// Build response
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

		d := gin.H{
			"type":        entryType,
			"obj_name":    e.Name,
			"name":        e.Name,
			"id":          e.ID,
			"last_update": e.Mtime,
			"mtime":       e.Mtime,
			"file_size":   e.Size,
			"size":        e.Size,
			"path":        entryPath,
			"is_file":     !isDir,
			"is_dir":      isDir,
		}
		dirents = append(dirents, d)
	}

	if dirents == nil {
		dirents = []gin.H{}
	}

	c.JSON(http.StatusOK, gin.H{
		"dirent_list":       dirents,
		"repo_name":         repoName,
		"is_system_library": false,
	})
}

// AdminListSharedItems lists users and groups a library is shared with.
// GET /admin/libraries/:library_id/shared-items/?share_type=user|group
func (h *AdminHandler) AdminListSharedItems(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")

	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	libraryID := c.Param("library_id")
	if _, err := uuid.Parse(libraryID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid library_id"})
		return
	}

	shareType := c.Query("share_type") // "user", "group", or "" (all)

	// Lookup org
	var orgID string
	if err := h.db.Session().Query(`
		SELECT org_id FROM libraries_by_id WHERE library_id = ?
	`, libraryID).Scan(&orgID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}

	var items []gin.H

	// Query shares for this library
	iter := h.db.Session().Query(`
		SELECT shared_to, shared_to_type, permission FROM shares
		WHERE library_id = ?
	`, libraryID).Iter()

	var sharedTo, sharedToType, permission string
	for iter.Scan(&sharedTo, &sharedToType, &permission) {
		if shareType != "" && sharedToType != shareType {
			continue
		}

		item := gin.H{
			"share_type": sharedToType,
			"permission": permission,
		}

		if sharedToType == "user" {
			userEmail := h.resolveOwnerEmail(orgID, sharedTo)
			userName := h.resolveOwnerName(orgID, sharedTo)
			item["user_email"] = userEmail
			item["user_name"] = userName
		} else if sharedToType == "group" {
			// Lookup group name
			var groupName string
			h.db.Session().Query(`
				SELECT name FROM groups WHERE org_id = ? AND group_id = ?
			`, orgID, sharedTo).Scan(&groupName)
			item["group_id"] = sharedTo
			item["group_name"] = groupName
		}

		items = append(items, item)
	}
	iter.Close()

	if items == nil {
		items = []gin.H{}
	}

	c.JSON(http.StatusOK, items)
}

// AdminListTrashLibraries lists soft-deleted libraries.
// GET /admin/trash-libraries/?page=&per_page=
func (h *AdminHandler) AdminListTrashLibraries(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")

	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
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

	callerRole, _ := h.permMiddleware.GetUserOrgRole(callerOrgID, callerUserID)
	var orgIDs []string
	if middleware.IsPlatformSuperAdmin(callerOrgID, callerRole) {
		orgIter := h.db.Session().Query(`SELECT org_id FROM organizations`).Iter()
		var oid string
		for orgIter.Scan(&oid) {
			orgIDs = append(orgIDs, oid)
		}
		orgIter.Close()
	} else {
		orgIDs = []string{callerOrgID}
	}

	// Filter by owner email if provided
	ownerFilter := c.Query("owner")

	var trashed []gin.H
	for _, orgID := range orgIDs {
		iter := h.db.Session().Query(`
			SELECT library_id, owner_id, name, size_bytes, deleted_at
			FROM libraries WHERE org_id = ?
		`, orgID).Iter()

		var libID, ownerID, name string
		var sizeBytes int64
		var deletedAt time.Time

		for iter.Scan(&libID, &ownerID, &name, &sizeBytes, &deletedAt) {
			if deletedAt.IsZero() {
				continue // Not deleted
			}
			ownerEmail := h.resolveOwnerEmail(orgID, ownerID)
			if ownerFilter != "" && ownerEmail != ownerFilter {
				continue
			}
			ownerName := h.resolveOwnerName(orgID, ownerID)
			trashed = append(trashed, gin.H{
				"id":          libID,
				"name":        name,
				"owner":       ownerEmail,
				"owner_name":  ownerName,
				"size":        sizeBytes,
				"delete_time": deletedAt.Format(time.RFC3339),
			})
		}
		iter.Close()
	}

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
			"has_next_page": end < total,
			"current_page":  page,
		},
	})
}

// AdminCleanTrashLibraries permanently deletes all soft-deleted libraries visible to the caller.
// Platform superadmin cleans trash across all organizations.
//
// For each trashed library this performs the same cleanup as PermanentDeleteRepo:
//   - Enqueues all commits, fs_objects and blocks for GC (async)
//   - Removes all tag data (async)
//   - Hard-deletes the library rows from libraries and libraries_by_id
//
// DELETE /admin/trash-libraries/
func (h *AdminHandler) AdminCleanTrashLibraries(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")

	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	callerRole, _ := h.permMiddleware.GetUserOrgRole(callerOrgID, callerUserID)
	var orgIDs []string
	if middleware.IsPlatformSuperAdmin(callerOrgID, callerRole) {
		orgIter := h.db.Session().Query(`SELECT org_id FROM organizations`).Iter()
		var oid string
		for orgIter.Scan(&oid) {
			orgIDs = append(orgIDs, oid)
		}
		orgIter.Close()
	} else {
		orgIDs = []string{callerOrgID}
	}

	libEnqueuer := getLibraryEnqueuer()
	cleaned := 0

	for _, orgID := range orgIDs {
		// Collect all soft-deleted libraries for this org in one pass.
		type trashedLib struct {
			libID        string
			storageClass string
		}
		var candidates []trashedLib

		iter := h.db.Session().Query(`
			SELECT library_id, storage_class, deleted_at FROM libraries WHERE org_id = ?
		`, orgID).Iter()
		var libID, storageClass string
		var deletedAt time.Time
		for iter.Scan(&libID, &storageClass, &deletedAt) {
			if !deletedAt.IsZero() {
				candidates = append(candidates, trashedLib{libID, storageClass})
			}
		}
		iter.Close()

		for _, lib := range candidates {
			// 1. Enqueue all file data for GC (commits, fs_objects, blocks → S3 deletion)
			if libEnqueuer != nil {
				go libEnqueuer.EnqueueLibraryDeletion(orgID, lib.libID, lib.storageClass)
			}

			// 2. Remove all tag metadata for this library
			go CleanupAllLibraryTags(h.db, lib.libID)

			// 3. Hard-delete library rows (same batch approach as PermanentDeleteRepo)
			batch := h.db.Session().Batch(gocql.LoggedBatch)
			batch.Query(`DELETE FROM libraries WHERE org_id = ? AND library_id = ?`, orgID, lib.libID)
			batch.Query(`DELETE FROM libraries_by_id WHERE library_id = ?`, lib.libID)

			// Preserve the org lookup for the Garbage Collector
			batch.Query(`INSERT INTO deleted_libraries (library_id, org_id, deleted_at) VALUES (?, ?, ?)`, lib.libID, orgID, time.Now())

			if err := batch.Exec(); err != nil {
				log.Printf("[AdminCleanTrashLibraries] failed to delete library %s (org %s): %v", lib.libID, orgID, err)
				continue
			}

			cleaned++
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "cleaned": cleaned})
}
