package v2

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// FileShareHandler handles file/folder sharing to users and groups
type FileShareHandler struct {
	db             *db.DB
	permMiddleware *middleware.PermissionMiddleware
}

// NewFileShareHandler creates a new FileShareHandler
func NewFileShareHandler(database *db.DB, permMiddleware ...*middleware.PermissionMiddleware) *FileShareHandler {
	h := &FileShareHandler{db: database}
	if len(permMiddleware) > 0 {
		h.permMiddleware = permMiddleware[0]
	}
	return h
}

// lookupUserIDByEmail resolves an email address to a user_id, scoped to orgID.
// It first checks the optimised users_by_email index.  If the row is missing
// (e.g. the user was created before the dual-write was introduced) it falls
// back to scanning the users table scoped to the given org, then backfills
// users_by_email so that subsequent lookups are fast.
// Returns an error if the user does not exist OR belongs to a different org.
func (h *FileShareHandler) lookupUserIDByEmail(orgID, email string) (string, error) {
	var userID, foundOrgID string
	if err := h.db.Session().Query(`
		SELECT user_id, org_id FROM users_by_email WHERE email = ?
	`, email).Scan(&userID, &foundOrgID); err == nil {
		// Enforce same-org: reject cross-org shares
		if foundOrgID != orgID {
			return "", fmt.Errorf("user not in this organization")
		}
		return userID, nil
	}

	// Fallback: scan within the org partition (bounded, safe with ALLOW FILTERING)
	if err := h.db.Session().Query(`
		SELECT user_id FROM users WHERE org_id = ? AND email = ? ALLOW FILTERING
	`, orgID, email).Scan(&userID); err != nil {
		return "", err
	}

	// Backfill the index so future lookups skip the slow path
	_ = h.db.Session().Query(`
		INSERT INTO users_by_email (email, user_id, org_id) VALUES (?, ?, ?)
	`, email, userID, orgID).Exec()

	return userID, nil
}

// RegisterFileShareRoutes registers file share routes
func RegisterFileShareRoutes(rg *gin.RouterGroup, database *db.DB, permMW ...*middleware.PermissionMiddleware) *FileShareHandler {
	h := NewFileShareHandler(database, permMW...)

	// Shared repos endpoints
	rg.GET("/shared-repos/", h.ListSharedRepos)
	rg.GET("/beshared-repos/", h.ListBeSharedRepos)
	rg.DELETE("/beshared-repos/:repo_id/", h.LeaveShareRepo)
	rg.DELETE("/beshared-repos/:repo_id", h.LeaveShareRepo)

	// Note: Other routes are registered in the libraries.go file under /repos/:repo_id/dir/shared_items
	// This file provides the handler implementations

	return h
}

// ShareResponse represents a share in API response
type ShareResponse struct {
	ShareID        string     `json:"share_id"`
	ShareType      string     `json:"share_type"` // "user" or "group"
	RepoID         string     `json:"repo_id"`
	RepoName       string     `json:"repo_name"`
	Path           string     `json:"path"`
	Permission     string     `json:"permission"`
	PermissionName string     `json:"permission_name,omitempty"` // display name for custom permissions
	IsAdmin        bool       `json:"is_admin"`
	ShareTo        string     `json:"share_to"`       // email (user identifier) or group_id
	ShareToName    string     `json:"share_to_name"`  // display name
	SharedBy       string     `json:"shared_by"`      // email
	SharedByName   string     `json:"shared_by_name"` // display name
	CreatedAt      string     `json:"ctime"`          // RFC3339 format
	ExpiresAt      *string    `json:"expire_date"`    // RFC3339 format
	UserInfo       *UserInfo  `json:"user_info,omitempty"`
	GroupInfo      *GroupInfo `json:"group_info,omitempty"`
}

// standardPermissions are the built-in permission names
var standardPermissions = map[string]bool{
	"rw": true, "r": true, "admin": true, "cloud-edit": true, "preview": true, "invisible": true,
}

// resolvePermissionName returns a display name for a permission.
// For standard permissions it returns empty (frontend handles them).
// For custom permission IDs (UUIDs), it looks up the name from the DB.
func resolvePermissionName(db *db.DB, permission string) string {
	if standardPermissions[permission] {
		return ""
	}
	// Assume it's a custom permission ID (UUID)
	var name string
	db.Session().Query(`SELECT name FROM custom_share_permissions WHERE permission_id = ?`, permission).Scan(&name)
	return name
}

// UserInfo represents user information in share response
// Note: In Seahub, "name" is the user identifier (email). The frontend uses
// user_info.name as the username param in update/delete API calls.
type UserInfo struct {
	Name     string `json:"name"`          // email (user identifier, used by frontend for API calls)
	Nickname string `json:"nickname"`      // display name
	Email    string `json:"contact_email"` // contact email
	Avatar   string `json:"avatar_url"`
}

// GroupInfo represents group information in share response
type GroupInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ListSharedItems returns list of shares for a file/folder
// GET /api2/repos/:repo_id/dir/shared_items/?p={path}&share_type={user|group}
func (h *FileShareHandler) ListSharedItems(c *gin.Context) {
	repoID := c.Param("repo_id")
	path := c.Query("p")
	shareType := c.Query("share_type") // "user" or "group"

	if repoID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "repo_id is required"})
		return
	}

	if path == "" {
		path = "/"
	}

	// Validate repo exists
	repoUUID, err := uuid.Parse(repoID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid repo_id"})
		return
	}

	// Check if database is available
	if h.db == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database not available"})
		return
	}

	// Get repo name for response
	var repoName string
	h.db.Session().Query(`SELECT name FROM libraries_by_id WHERE library_id = ?`, repoUUID.String()).Scan(&repoName)
	if repoName == "" {
		repoName = "Unknown Library"
	}

	// Query shares for this library and path
	// Note: We need to create a lookup table for efficient querying by library_id and path
	// For now, we'll query all shares for the library and filter in Go
	// Use .String() for UUID params - gocql can't marshal google/uuid.UUID directly
	iter := h.db.Session().Query(`
		SELECT share_id, shared_by, shared_to, shared_to_type, permission, created_at, expires_at
		FROM shares WHERE library_id = ?
	`, repoUUID.String()).Iter()

	var shares []ShareResponse
	var shareID, sharedBy, sharedTo, sharedToType, permission string
	var createdAt time.Time
	var expiresAt *time.Time

	for iter.Scan(&shareID, &sharedBy, &sharedTo, &sharedToType, &permission, &createdAt, &expiresAt) {
		// Filter by share_type if specified
		if shareType != "" && shareType != sharedToType {
			continue
		}

		// Get shared_by user info (users table requires org_id in WHERE clause)
		var sharedByName, sharedByEmail string
		// First get org_id from library
		var libOrgID string
		h.db.Session().Query(`SELECT org_id FROM libraries_by_id WHERE library_id = ?`, repoUUID.String()).Scan(&libOrgID)

		h.db.Session().Query(`SELECT name, email FROM users WHERE org_id = ? AND user_id = ?`, libOrgID, sharedBy).Scan(&sharedByName, &sharedByEmail)

		share := ShareResponse{
			ShareID:        shareID,
			ShareType:      sharedToType,
			RepoID:         repoID,
			RepoName:       repoName,
			Path:           path,
			Permission:     permission,
			PermissionName: resolvePermissionName(h.db, permission),
			IsAdmin:        permission == "admin",
			ShareTo:        sharedTo,
			SharedBy:       sharedByEmail,
			SharedByName:   sharedByName,
			CreatedAt:      createdAt.Format(time.RFC3339),
		}

		if expiresAt != nil {
			expStr := expiresAt.Format(time.RFC3339)
			share.ExpiresAt = &expStr
		}

		// Get shared_to info
		if sharedToType == "user" {
			var userName, userEmail string
			h.db.Session().Query(`SELECT name, email FROM users WHERE org_id = ? AND user_id = ?`, libOrgID, sharedTo).Scan(&userName, &userEmail)
			// Seahub convention: share_to = email (user identifier)
			share.ShareTo = userEmail
			share.ShareToName = userEmail
			share.UserInfo = &UserInfo{
				Name:     userEmail, // email = user identifier (frontend uses this for update/delete calls)
				Nickname: userName,  // display name shown in UI
				Email:    userEmail, // contact email
				Avatar:   "",
			}
		} else if sharedToType == "group" {
			// Get group info from groups table
			var groupName string
			h.db.Session().Query(`SELECT name FROM groups WHERE org_id = ? AND group_id = ?`, libOrgID, sharedTo).Scan(&groupName)
			if groupName == "" {
				groupName = "Group " + sharedTo
			}
			share.ShareToName = groupName
			share.GroupInfo = &GroupInfo{
				ID:   sharedTo,
				Name: groupName,
			}
		}

		shares = append(shares, share)
	}

	if err := iter.Close(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list shares"})
		return
	}

	if shares == nil {
		shares = []ShareResponse{}
	}

	c.JSON(http.StatusOK, shares)
}

// CreateShare creates new share(s) to users or groups
// PUT /api2/repos/:repo_id/dir/shared_items/?p={path}
// Form data: share_type, permission, username[] or group_id[]
func (h *FileShareHandler) CreateShare(c *gin.Context) {
	repoID := c.Param("repo_id")
	path := c.Query("p")
	userID := c.GetString("user_id")

	if repoID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "repo_id is required"})
		return
	}

	if path == "" {
		path = "/"
	}

	var body struct {
		ShareType  string   `json:"share_type"`
		Permission string   `json:"permission"`
		Username   []string `json:"username"`
		GroupID    []string `json:"group_id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	shareType := body.ShareType
	permission := body.Permission

	if shareType == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "share_type is required"})
		return
	}

	if permission == "" {
		permission = "r"
	}

	// Validate repo exists
	repoUUID, err := uuid.Parse(repoID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid repo_id"})
		return
	}

	// Get library info
	libraryState, err := resolveLiveLibraryStateByIDFn(h.db.Session(), repoUUID.String())
	if err != nil {
		writeLiveLibraryStateError(c, err)
		return
	}
	libOrgID := libraryState.OrgID

	// PERMISSION CHECK: User must have admin or owner access to share a library
	if h.permMiddleware != nil {
		hasAdmin, err := h.permMiddleware.HasLibraryAccess(libOrgID, userID, repoID, middleware.PermissionAdmin)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check permissions"})
			return
		}
		if !hasAdmin {
			c.JSON(http.StatusForbidden, gin.H{"error": "you do not have permission to share this library"})
			return
		}
	}

	// Validate share_type-specific parameters before DB access
	if shareType == "user" {
		if len(body.Username) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "username is required"})
			return
		}
	} else if shareType == "group" {
		if len(body.GroupID) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "group_id is required"})
			return
		}
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid share_type"})
		return
	}

	// Check if database is available
	if h.db == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database not available"})
		return
	}

	var successItems []gin.H
	var failedItems []gin.H
	now := time.Now()

	// Pre-load existing shares for this library to check for duplicates
	existingShares := make(map[string]string) // shared_to+type -> share_id
	dedupIter := h.db.Session().Query(`
		SELECT share_id, shared_to, shared_to_type FROM shares WHERE library_id = ?
	`, repoUUID.String()).Iter()
	var existShareID, existSharedTo, existSharedToType string
	for dedupIter.Scan(&existShareID, &existSharedTo, &existSharedToType) {
		existingShares[existSharedTo+":"+existSharedToType] = existShareID
	}
	dedupIter.Close()

	if shareType == "user" {
		// Share to user(s)
		usernames := body.Username

		for _, username := range usernames {
			// Get user by email (with fallback for pre-index users)
			sharedToUserID, lookupErr := h.lookupUserIDByEmail(libOrgID, username)
			if lookupErr != nil {
				failedItems = append(failedItems, gin.H{
					"email":     username,
					"error_msg": "user not found",
				})
				continue
			}

			// Prevent self-sharing
			if sharedToUserID == userID {
				failedItems = append(failedItems, gin.H{
					"email":     username,
					"error_msg": "You cannot share to yourself.",
				})
				continue
			}

			// Get display name
			var userName string
			h.db.Session().Query(`SELECT name FROM users WHERE org_id = ? AND user_id = ?`, libOrgID, sharedToUserID).Scan(&userName)

			// Check for duplicate: if already shared to this user, return in failed
			if _, exists := existingShares[sharedToUserID+":user"]; exists {
				failedItems = append(failedItems, gin.H{
					"email":     username,
					"error_msg": "This library has already been shared to " + userName + ".",
				})
				continue
			}

			shareIDUUID := uuid.New()

			if err := createLibraryShare(h.db, repoUUID.String(), shareIDUUID.String(), userID, sharedToUserID, "user", permission, now, nil); err != nil {
				failedItems = append(failedItems, gin.H{
					"email":     username,
					"error_msg": "failed to create share",
				})
				continue
			}

			successItems = append(successItems, gin.H{
				"user_info":  gin.H{"name": username, "nickname": userName, "contact_email": username, "avatar_url": ""},
				"share_type": "user",
				"permission": permission,
				"is_admin":   permission == "admin",
			})
		}
	} else if shareType == "group" {
		// Share to group(s)
		groupIDs := body.GroupID
		if len(groupIDs) == 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "group_id is required"})
			return
		}

		for _, groupID := range groupIDs {
			groupUUID, err := uuid.Parse(groupID)
			if err != nil {
				failedItems = append(failedItems, gin.H{
					"group_id":  groupID,
					"error_msg": "invalid group_id",
				})
				continue
			}

			// Get group name
			var groupName string
			h.db.Session().Query(`SELECT name FROM groups WHERE org_id = ? AND group_id = ?`, libOrgID, groupUUID.String()).Scan(&groupName)

			// Check for duplicate: if already shared to this group, return in failed
			if _, exists := existingShares[groupUUID.String()+":group"]; exists {
				failedItems = append(failedItems, gin.H{
					"group_name": groupName,
					"error_msg":  "This library has already been shared to " + groupName + ".",
				})
				continue
			}

			shareIDUUID := uuid.New()

			if err := createLibraryShare(h.db, repoUUID.String(), shareIDUUID.String(), userID, groupUUID.String(), "group", permission, now, nil); err != nil {
				failedItems = append(failedItems, gin.H{
					"group_id":  groupID,
					"error_msg": "failed to create share",
				})
				continue
			}

			successItems = append(successItems, gin.H{
				"group_info": gin.H{"id": groupUUID.String(), "name": groupName},
				"share_type": "group",
				"permission": permission,
				"is_admin":   permission == "admin",
			})
		}
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid share_type"})
		return
	}

	if successItems == nil {
		successItems = []gin.H{}
	}
	if failedItems == nil {
		failedItems = []gin.H{}
	}

	c.JSON(http.StatusOK, gin.H{"success": successItems, "failed": failedItems})
}

// UpdateSharePermission updates permission for a share
// POST /api2/repos/:repo_id/dir/shared_items/?p={path}&share_type={type}&username={user} or &group_id={id}
// JSON body: { "permission": "r" | "rw" }
func (h *FileShareHandler) UpdateSharePermission(c *gin.Context) {
	repoID := c.Param("repo_id")
	path := c.Query("p")
	shareType := c.Query("share_type")
	username := c.Query("username")
	groupID := c.Query("group_id")

	var body struct {
		Permission string `json:"permission"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	permission := body.Permission

	if repoID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "repo_id is required"})
		return
	}

	if path == "" {
		path = "/"
	}

	if permission == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "permission is required"})
		return
	}

	repoUUID, err := uuid.Parse(repoID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid repo_id"})
		return
	}

	// Validate share-type-specific parameters
	if username == "" && groupID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "username or group_id is required"})
		return
	}

	// Check if database is available
	if h.db == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database not available"})
		return
	}

	libraryState, err := resolveLiveLibraryStateByIDFn(h.db.Session(), repoUUID.String())
	if err != nil {
		writeLiveLibraryStateError(c, err)
		return
	}
	updateLibOrgID := libraryState.OrgID

	// PERMISSION CHECK: User must have admin or owner access to manage shares
	userID := c.GetString("user_id")
	if h.permMiddleware != nil {
		hasAdmin, err := h.permMiddleware.HasLibraryAccess(updateLibOrgID, userID, repoID, middleware.PermissionAdmin)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check permissions"})
			return
		}
		if !hasAdmin {
			c.JSON(http.StatusForbidden, gin.H{"error": "you do not have permission to manage shares for this library"})
			return
		}
	}

	// Find the share to update
	var sharedToID string
	if shareType == "user" && username != "" {
		// Get user ID by email (with fallback for pre-index users)
		var lookupErr error
		sharedToID, lookupErr = h.lookupUserIDByEmail(updateLibOrgID, username)
		if lookupErr != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
			return
		}
	} else if shareType == "group" && groupID != "" {
		sharedToID = groupID
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"error": "username or group_id is required"})
		return
	}

	// Update the share permission
	// Note: We need to find the share_id first since we can't update by library_id + shared_to
	// For now, we'll scan all shares for this library
	iter := h.db.Session().Query(`
		SELECT share_id, shared_to, shared_to_type
		FROM shares WHERE library_id = ?
	`, repoUUID.String()).Iter()

	var foundShareID string
	var shareIDStr, sharedTo, sharedToType string
	for iter.Scan(&shareIDStr, &sharedTo, &sharedToType) {
		if sharedTo == sharedToID && sharedToType == shareType {
			foundShareID = shareIDStr
			break
		}
	}
	if err := iter.Close(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query shares"})
		return
	}

	if foundShareID == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "share not found"})
		return
	}

	shareIDUUID, err := uuid.Parse(foundShareID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid share_id in database"})
		return
	}

	if err := updateLibrarySharePermission(h.db, repoUUID.String(), shareIDUUID.String(), permission); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update share"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// DeleteShare deletes a share
// DELETE /api2/repos/:repo_id/dir/shared_items/?p={path}&share_type={type}&username={user} or &group_id={id}
func (h *FileShareHandler) DeleteShare(c *gin.Context) {
	repoID := c.Param("repo_id")
	path := c.Query("p")
	shareType := c.Query("share_type")
	username := c.Query("username")
	groupID := c.Query("group_id")

	if repoID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "repo_id is required"})
		return
	}

	if path == "" {
		path = "/"
	}

	repoUUID, err := uuid.Parse(repoID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid repo_id"})
		return
	}

	// Validate share-type-specific parameters
	if username == "" && groupID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "username or group_id is required"})
		return
	}

	// Check if database is available
	if h.db == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database not available"})
		return
	}

	libraryState, err := resolveLiveLibraryStateByIDFn(h.db.Session(), repoUUID.String())
	if err != nil {
		writeLiveLibraryStateError(c, err)
		return
	}
	deleteLibOrgID := libraryState.OrgID

	// PERMISSION CHECK: User must have admin or owner access to manage shares
	deleteUserID := c.GetString("user_id")
	if h.permMiddleware != nil {
		hasAdmin, err := h.permMiddleware.HasLibraryAccess(deleteLibOrgID, deleteUserID, repoID, middleware.PermissionAdmin)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check permissions"})
			return
		}
		if !hasAdmin {
			c.JSON(http.StatusForbidden, gin.H{"error": "you do not have permission to manage shares for this library"})
			return
		}
	}

	// Find the share to delete
	var sharedToID string
	if shareType == "user" && username != "" {
		// Get user ID by email (with fallback for pre-index users)
		var lookupErr error
		sharedToID, lookupErr = h.lookupUserIDByEmail(deleteLibOrgID, username)
		if lookupErr != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
			return
		}
	} else if shareType == "group" && groupID != "" {
		sharedToID = groupID
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"error": "username or group_id is required"})
		return
	}

	// Find and delete the share
	iter := h.db.Session().Query(`
		SELECT share_id, shared_to, shared_to_type
		FROM shares WHERE library_id = ?
	`, repoUUID.String()).Iter()

	var foundShareID string
	var shareIDStr, sharedTo, sharedToType string
	for iter.Scan(&shareIDStr, &sharedTo, &sharedToType) {
		if sharedTo == sharedToID && sharedToType == shareType {
			foundShareID = shareIDStr
			break
		}
	}
	if err := iter.Close(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query shares"})
		return
	}

	if foundShareID == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "share not found"})
		return
	}

	shareIDUUID, err := uuid.Parse(foundShareID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid share_id in database"})
		return
	}

	if err := deleteLibraryShare(h.db, repoUUID.String(), shareIDUUID.String()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete share"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// LibraryShareInfo represents library share information
type LibraryShareInfo struct {
	RepoID       string `json:"repo_id"`
	RepoName     string `json:"repo_name"`
	RepoDesc     string `json:"repo_desc"`
	Permission   string `json:"permission"`
	ShareType    string `json:"share_type"` // "personal" or "group"
	User         string `json:"user"`       // email of user who shared
	LastModified int64  `json:"last_modified"`
	IsVirtual    bool   `json:"is_virtual"`
	Encrypted    int    `json:"encrypted"` // 0 or 1
	EncVersion   int    `json:"enc_version,omitempty"`
	Magic        string `json:"magic,omitempty"`
	RandomKey    string `json:"random_key,omitempty"`
	Salt         string `json:"salt,omitempty"`
}

// ListSharedRepos returns list of libraries I have shared with others
// GET /api2/shared-repos/
func (h *FileShareHandler) ListSharedRepos(c *gin.Context) {
	userID := c.GetString("user_id")
	orgID := c.GetString("org_id")

	sharedRepos := make(map[string]*LibraryShareInfo)
	iter := h.db.Session().Query(`
		SELECT created_at, library_id, share_id, shared_to, shared_to_type, permission, expires_at
		FROM shares_by_creator WHERE org_id = ? AND shared_by = ?
	`, orgID, userID).Iter()
	var createdAt time.Time
	var libID, shareID, sharedTo, sharedToType, permission string
	var expiresAt *time.Time
	for iter.Scan(&createdAt, &libID, &shareID, &sharedTo, &sharedToType, &permission, &expiresAt) {
		if _, exists := sharedRepos[libID]; exists {
			continue
		}
		var name, description string
		var encrypted bool
		var updatedAt time.Time
		var encVersion int
		var magic, randomKey, salt string
		if queryErr := h.db.Session().Query(`
			SELECT name, description, encrypted, updated_at, enc_version, magic, random_key, salt
			FROM libraries WHERE org_id = ? AND library_id = ?
		`, orgID, libID).Scan(&name, &description, &encrypted, &updatedAt, &encVersion, &magic, &randomKey, &salt); queryErr != nil {
			continue
		}
		encryptedInt := 0
		if encrypted {
			encryptedInt = 1
		}
		shareType := "personal"
		if sharedToType == "group" {
			shareType = "group"
		}
		sharedRepos[libID] = &LibraryShareInfo{
			RepoID:       libID,
			RepoName:     name,
			RepoDesc:     description,
			Permission:   permission,
			ShareType:    shareType,
			LastModified: updatedAt.UnixMilli(),
			IsVirtual:    false,
			Encrypted:    encryptedInt,
			EncVersion:   encVersion,
			Magic:        magic,
			RandomKey:    randomKey,
			Salt:         salt,
		}
	}
	if err := iter.Close(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list shared repos"})
		return
	}

	// Convert map to array
	var result []LibraryShareInfo
	for _, repo := range sharedRepos {
		result = append(result, *repo)
	}
	sort.SliceStable(result, func(i, j int) bool {
		return result[i].RepoName < result[j].RepoName
	})

	if result == nil {
		result = []LibraryShareInfo{}
	}

	c.JSON(http.StatusOK, result)
}

// LeaveShareRepo allows the current user (recipient) to remove a personal share to themselves
// DELETE /api2/beshared-repos/:repo_id/?share_type=personal&from=<owner_email>
func (h *FileShareHandler) LeaveShareRepo(c *gin.Context) {
	userID := c.GetString("user_id")
	orgID := c.GetString("org_id")

	repoID := c.Param("repo_id")
	repoUUID, err := uuid.Parse(repoID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid repo_id"})
		return
	}

	shareType := c.Query("share_type")
	if shareType != "personal" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "only share_type=personal is supported"})
		return
	}

	fromEmail := c.Query("from")
	if fromEmail == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "from parameter is required"})
		return
	}

	// Resolve the sharer's user_id from email so we can verify it matches the share record
	sharerID, err := h.lookupUserIDByEmail(orgID, fromEmail)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "sharer not found"})
		return
	}

	// Scan shares for this library to find the one where shared_to = current user and shared_by = sharer
	iter := h.db.Session().Query(`
		SELECT share_id, shared_by, shared_to, shared_to_type
		FROM shares WHERE library_id = ?
	`, repoUUID.String()).Iter()

	var foundShareID string
	var shareIDStr, sharedBy, sharedTo, sharedToType string
	for iter.Scan(&shareIDStr, &sharedBy, &sharedTo, &sharedToType) {
		if sharedTo == userID && sharedBy == sharerID && sharedToType == "user" {
			foundShareID = shareIDStr
			break
		}
	}
	if err := iter.Close(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query shares"})
		return
	}

	if foundShareID == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "share not found"})
		return
	}

	if err := deleteLibraryShare(h.db, repoUUID.String(), foundShareID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete share"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ListBeSharedRepos returns list of libraries shared with me
// GET /api2/beshared-repos/
func (h *FileShareHandler) ListBeSharedRepos(c *gin.Context) {
	userID := c.GetString("user_id")
	orgID := c.GetString("org_id")

	orgUUID, err := uuid.Parse(orgID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid org_id"})
		return
	}

	// Get user's group IDs for group share resolution
	userTargets := map[string]string{userID: "personal"}
	groupIter := h.db.Session().Query(`
		SELECT group_id FROM groups_by_member WHERE org_id = ? AND user_id = ?
	`, orgID, userID).Iter()
	var gidStr string
	for groupIter.Scan(&gidStr) {
		userTargets[gidStr] = "group"
	}
	groupIter.Close()

	beSharedRepos := make(map[string]*LibraryShareInfo)

	// Helper to add a library share to results
	addLibShare := func(libIDStr string, sharedBy, permission, sharedToType string) {
		var name, description string
		var encrypted bool
		var updatedAt time.Time
		var encVersion int
		var magic, randomKey, salt string

		if queryErr := h.db.Session().Query(`
			SELECT name, description, encrypted, updated_at, enc_version, magic, random_key, salt
			FROM libraries WHERE org_id = ? AND library_id = ?
		`, orgID, libIDStr).Scan(&name, &description, &encrypted, &updatedAt, &encVersion, &magic, &randomKey, &salt); queryErr != nil {
			return
		}

		var sharedByEmail string
		sharedByUUID, _ := uuid.Parse(sharedBy)
		h.db.Session().Query(`SELECT email FROM users WHERE org_id = ? AND user_id = ?`, orgUUID, sharedByUUID).Scan(&sharedByEmail)

		encryptedInt := 0
		if encrypted {
			encryptedInt = 1
		}

		beSharedRepos[libIDStr] = &LibraryShareInfo{
			RepoID:       libIDStr,
			RepoName:     name,
			RepoDesc:     description,
			Permission:   permission,
			ShareType:    sharedToType,
			User:         sharedByEmail,
			LastModified: updatedAt.UnixMilli(),
			IsVirtual:    false,
			Encrypted:    encryptedInt,
			EncVersion:   encVersion,
			Magic:        magic,
			RandomKey:    randomKey,
			Salt:         salt,
		}
	}

	for sharedTo, resultType := range userTargets {
		sharedToType := "user"
		if resultType == "group" {
			sharedToType = "group"
		}
		iter := h.db.Session().Query(`
			SELECT created_at, library_id, share_id, shared_by, permission, expires_at
			FROM shares_by_recipient WHERE org_id = ? AND shared_to_type = ? AND shared_to = ?
		`, orgID, sharedToType, sharedTo).Iter()
		var libIDStr, shareID, sharedBy, permission string
		var createdAtRecipient time.Time
		var expiresAtRecipient *time.Time
		for iter.Scan(&createdAtRecipient, &libIDStr, &shareID, &sharedBy, &permission, &expiresAtRecipient) {
			if _, exists := beSharedRepos[libIDStr]; exists && resultType == "group" {
				continue
			}
			addLibShare(libIDStr, sharedBy, permission, resultType)
		}
		if err := iter.Close(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list shared repos"})
			return
		}
	}

	// Convert map to array
	var result []LibraryShareInfo
	for _, repo := range beSharedRepos {
		result = append(result, *repo)
	}
	sort.SliceStable(result, func(i, j int) bool {
		return result[i].RepoName < result[j].RepoName
	})

	if result == nil {
		result = []LibraryShareInfo{}
	}

	c.JSON(http.StatusOK, result)
}

// ListCustomSharePermissions returns custom share permissions for the current user.
// GET /api/v2.1/repos/:repo_id/custom-share-permissions/
func (h *FileShareHandler) ListCustomSharePermissions(c *gin.Context) {
	userID := c.GetString("user_id")

	iter := h.db.Session().Query(`
		SELECT permission_id, name, description, permission_json
		FROM custom_share_permissions_by_user WHERE creator_id = ?
	`, userID).Iter()

	var permID, name, description, permJSON string
	var permList []gin.H
	for iter.Scan(&permID, &name, &description, &permJSON) {
		var permObj map[string]interface{}
		if err := json.Unmarshal([]byte(permJSON), &permObj); err != nil {
			permObj = map[string]interface{}{}
		}
		permList = append(permList, gin.H{
			"id":          permID,
			"name":        name,
			"description": description,
			"permission":  permObj,
		})
	}
	if err := iter.Close(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list custom permissions"})
		return
	}

	if permList == nil {
		permList = []gin.H{}
	}

	c.JSON(http.StatusOK, gin.H{"permission_list": permList})
}

// GetCustomSharePermission returns a single custom share permission by ID.
// GET /api/v2.1/repos/:repo_id/custom-share-permissions/:perm_id/
func (h *FileShareHandler) GetCustomSharePermission(c *gin.Context) {
	permID := c.Param("perm_id")
	if permID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "permission id is required"})
		return
	}

	var name, description, permJSON string
	if err := h.db.Session().Query(`
		SELECT name, description, permission_json
		FROM custom_share_permissions WHERE permission_id = ?
	`, permID).Scan(&name, &description, &permJSON); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "custom permission not found"})
		return
	}

	var permObj map[string]interface{}
	if err := json.Unmarshal([]byte(permJSON), &permObj); err != nil {
		permObj = map[string]interface{}{}
	}

	c.JSON(http.StatusOK, gin.H{
		"permission": gin.H{
			"id":          permID,
			"name":        name,
			"description": description,
			"permission":  permObj,
		},
	})
}

// CreateCustomSharePermission creates a new custom share permission for the current user.
// POST /api/v2.1/repos/:repo_id/custom-share-permissions/
func (h *FileShareHandler) CreateCustomSharePermission(c *gin.Context) {
	userID := c.GetString("user_id")

	var body struct {
		Name        string `json:"permission_name"`
		Description string `json:"description"`
		Permission  string `json:"permission"` // JSON string from frontend
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	if strings.TrimSpace(body.Name) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "permission_name is required"})
		return
	}

	// Validate permission JSON
	var permObj map[string]interface{}
	if err := json.Unmarshal([]byte(body.Permission), &permObj); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid permission JSON"})
		return
	}

	permID := uuid.New()
	now := time.Now()

	// Dual-write: main table + by-user lookup
	if err := createCustomSharePermission(h.db.Session(), permID.String(), userID, body.Name, body.Description, body.Permission, now); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create custom permission"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"permission": gin.H{
			"id":          permID.String(),
			"name":        body.Name,
			"description": body.Description,
			"permission":  permObj,
		},
	})
}

// UpdateCustomSharePermission updates an existing custom share permission.
// PUT /api/v2.1/repos/:repo_id/custom-share-permissions/:perm_id/
func (h *FileShareHandler) UpdateCustomSharePermission(c *gin.Context) {
	userID := c.GetString("user_id")
	permID := c.Param("perm_id")

	if permID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "permission id is required"})
		return
	}

	var body struct {
		Name        string `json:"permission_name"`
		Description string `json:"description"`
		Permission  string `json:"permission"` // JSON string from frontend
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	if strings.TrimSpace(body.Name) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "permission_name is required"})
		return
	}

	var permObj map[string]interface{}
	if err := json.Unmarshal([]byte(body.Permission), &permObj); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid permission JSON"})
		return
	}

	// Verify ownership: only the creator can update
	var creatorID string
	if err := h.db.Session().Query(`
		SELECT creator_id FROM custom_share_permissions WHERE permission_id = ?
	`, permID).Scan(&creatorID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "custom permission not found"})
		return
	}
	if creatorID != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "you can only update your own custom permissions"})
		return
	}

	// Dual-write update
	if err := updateCustomSharePermission(h.db.Session(), permID, userID, body.Name, body.Description, body.Permission); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update custom permission"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"permission": gin.H{
			"id":          permID,
			"name":        body.Name,
			"description": body.Description,
			"permission":  permObj,
		},
	})
}

// DeleteCustomSharePermission deletes a custom share permission.
// DELETE /api/v2.1/repos/:repo_id/custom-share-permissions/:perm_id/
func (h *FileShareHandler) DeleteCustomSharePermission(c *gin.Context) {
	userID := c.GetString("user_id")
	permID := c.Param("perm_id")

	if permID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "permission id is required"})
		return
	}

	// Verify ownership
	var creatorID string
	if err := h.db.Session().Query(`
		SELECT creator_id FROM custom_share_permissions WHERE permission_id = ?
	`, permID).Scan(&creatorID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "custom permission not found"})
		return
	}
	if creatorID != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "you can only delete your own custom permissions"})
		return
	}

	// Dual-write delete
	if err := deleteCustomSharePermission(h.db.Session(), permID, userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete custom permission"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}
