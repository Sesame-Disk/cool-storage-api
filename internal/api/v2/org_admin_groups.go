package v2

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Helpers for groups & repos
// ============================================================================

// userInfo holds resolved email and display name for a user.
type userInfo struct {
	Email string
	Name  string
}

// resolveUsersMap loads email and name for all users in an org partition in a
// single query and returns a map keyed by user_id. This eliminates N+1 queries
// when iterating lists that need user details.
func (h *OrgAdminHandler) resolveUsersMap(orgID string) map[string]userInfo {
	m := make(map[string]userInfo)
	iter := h.db.Session().Query(`
		SELECT user_id, email, name FROM users WHERE org_id = ?
	`, orgID).Iter()
	var uid, email, name string
	for iter.Scan(&uid, &email, &name) {
		displayName := name
		if displayName == "" && email != "" {
			displayName = strings.Split(email, "@")[0]
		}
		m[uid] = userInfo{Email: email, Name: displayName}
	}
	iter.Close()
	return m
}

// resolveUserName returns the display name (or email prefix) for a user.
func (h *OrgAdminHandler) resolveUserName(orgID, userID string) string {
	var name, email string
	h.db.Session().Query(`
		SELECT email, name FROM users WHERE org_id = ? AND user_id = ?
	`, orgID, userID).Scan(&email, &name)
	if name != "" {
		return name
	}
	if email != "" {
		return strings.Split(email, "@")[0]
	}
	return ""
}

// ============================================================================
// Groups
// ============================================================================

// ListOrgGroups lists all groups in the org with pagination.
// GET /org/:org_id/admin/groups/?page=N
// Frontend reads: res.data.groups, res.data.page, res.data.page_next
func (h *OrgAdminHandler) ListOrgGroups(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	perPage := 25

	iter := h.db.Session().Query(`
		SELECT group_id, name, creator_id, created_at
		FROM groups WHERE org_id = ?
	`, targetOrgID).Iter()

	type orgGroupRow struct {
		ID                  string `json:"id"`
		GroupName           string `json:"group_name"`
		CreatorName         string `json:"creator_name"`
		CreatorEmail        string `json:"creator_email"`
		CreatorContactEmail string `json:"creator_contact_email"`
		Ctime               string `json:"ctime"`
	}

	usersMap := h.resolveUsersMap(targetOrgID)

	var all []orgGroupRow
	var groupID, name, creatorID string
	var createdAt time.Time

	for iter.Scan(&groupID, &name, &creatorID, &createdAt) {
		u := usersMap[creatorID]
		all = append(all, orgGroupRow{
			ID:                  groupID,
			GroupName:           name,
			CreatorName:         u.Name,
			CreatorEmail:        u.Email,
			CreatorContactEmail: "",
			Ctime:               createdAt.Format(time.RFC3339),
		})
	}
	iter.Close()

	if all == nil {
		all = []orgGroupRow{}
	}

	total := len(all)
	start := (page - 1) * perPage
	if start > total {
		start = total
	}
	end := start + perPage
	if end > total {
		end = total
	}

	c.JSON(http.StatusOK, gin.H{
		"groups":    all[start:end],
		"page":      page,
		"page_next": end < total,
	})
}

// GetOrgGroup returns details for a single group.
// GET /org/:org_id/admin/groups/:gid/
// Frontend reads: res.data.group_name, res.data.creator_email, res.data.creator_name
func (h *OrgAdminHandler) GetOrgGroup(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}

	groupID := c.Param("gid")

	var name, creatorID string
	var createdAt time.Time
	if err := h.db.Session().Query(`
		SELECT name, creator_id, created_at FROM groups WHERE org_id = ? AND group_id = ?
	`, targetOrgID, groupID).Scan(&name, &creatorID, &createdAt); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "group not found"})
		return
	}

	creatorEmail := h.resolveUserEmail(targetOrgID, creatorID)
	creatorName := h.resolveUserName(targetOrgID, creatorID)

	c.JSON(http.StatusOK, gin.H{
		"group_name":    name,
		"creator_email": creatorEmail,
		"creator_name":  creatorName,
		"ctime":         createdAt.Format(time.RFC3339),
	})
}

// UpdateOrgGroup updates a group (quota).
// PUT /org/:org_id/admin/groups/:gid/
func (h *OrgAdminHandler) UpdateOrgGroup(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}

	groupID := c.Param("gid")

	// Verify group exists
	var name string
	if err := h.db.Session().Query(`
		SELECT name FROM groups WHERE org_id = ? AND group_id = ?
	`, targetOrgID, groupID).Scan(&name); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "group not found"})
		return
	}

	var req struct {
		Quota *int64 `json:"quota"`
	}
	_ = c.ShouldBindJSON(&req)

	// Store group quota in org settings (no dedicated column on groups table).
	if req.Quota != nil {
		settingKey := "group_quota_" + groupID
		quotaStr := strconv.FormatInt(*req.Quota, 10)
		if err := h.db.Session().Query(`
			UPDATE organizations SET settings[?] = ? WHERE org_id = ?
		`, settingKey, quotaStr, targetOrgID).Exec(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update group"})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// TransferOrgGroup transfers group ownership to another user in the organization.
// PUT /org/:org_id/admin/groups/:gid/transfer/
func (h *OrgAdminHandler) TransferOrgGroup(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}

	groupID := c.Param("gid")
	var req struct {
		NewOwner string `json:"new_owner"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	newOwnerEmail := req.NewOwner
	if newOwnerEmail == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "new_owner is required"})
		return
	}

	// Verify group exists and get current owner
	var creatorID string
	if err := h.db.Session().Query(`
		SELECT creator_id FROM groups WHERE org_id = ? AND group_id = ?
	`, targetOrgID, groupID).Scan(&creatorID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "group not found"})
		return
	}

	// Resolve new owner (must belong to this org)
	newOwnerID, err := h.lookupOrgUserByEmail(targetOrgID, newOwnerEmail)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found in this organization"})
		return
	}

	now := time.Now()

	// Read group name and created_at for lookup table and response
	var groupName string
	var createdAt time.Time
	h.db.Session().Query(`SELECT name, created_at FROM groups WHERE org_id = ? AND group_id = ?`, targetOrgID, groupID).Scan(&groupName, &createdAt)

	batch := h.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		UPDATE groups SET creator_id = ?, updated_at = ? WHERE org_id = ? AND group_id = ?
	`, newOwnerID, now, targetOrgID, groupID)
	batch.Query(`
		UPDATE group_members SET role = ? WHERE group_id = ? AND user_id = ?
	`, "member", groupID, creatorID)
	batch.Query(`
		UPDATE groups_by_member SET role = ? WHERE org_id = ? AND user_id = ? AND group_id = ?
	`, "member", targetOrgID, creatorID, groupID)
	batch.Query(`
		INSERT INTO group_members (group_id, user_id, role, added_at)
		VALUES (?, ?, ?, ?)
	`, groupID, newOwnerID, "owner", now)
	batch.Query(`
		INSERT INTO groups_by_member (org_id, user_id, group_id, group_name, role, added_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, targetOrgID, newOwnerID, groupID, groupName, "owner", now)
	if err := batch.Exec(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to transfer group ownership"})
		return
	}

	// Resolve new owner display name
	newOwnerName := h.resolveUserName(targetOrgID, newOwnerID)
	if newOwnerName == "" {
		newOwnerName = newOwnerEmail
	}

	// Return full group object so frontend can update the list item directly
	c.JSON(http.StatusOK, gin.H{
		"id":                    groupID,
		"group_name":            groupName,
		"creator_name":          newOwnerName,
		"creator_email":         newOwnerEmail,
		"creator_contact_email": "",
		"ctime":                 createdAt.Format(time.RFC3339),
	})
}

// DeleteOrgGroup deletes a group.
// DELETE /org/:org_id/admin/groups/:gid/
func (h *OrgAdminHandler) DeleteOrgGroup(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}

	groupID := c.Param("gid")

	// Verify group exists
	if err := h.db.Session().Query(`
		SELECT name FROM groups WHERE org_id = ? AND group_id = ?
	`, targetOrgID, groupID).Scan(new(string)); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "group not found"})
		return
	}

	// Get all members before deleting, so we can clean up groups_by_member
	memberIter := h.db.Session().Query(`SELECT user_id FROM group_members WHERE group_id = ?`, groupID).Iter()
	var memberID string
	var memberIDs []string
	for memberIter.Scan(&memberID) {
		memberIDs = append(memberIDs, memberID)
	}
	memberIter.Close()

	batch := h.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`DELETE FROM groups WHERE org_id = ? AND group_id = ?`, targetOrgID, groupID)
	batch.Query(`DELETE FROM groups_by_id WHERE group_id = ?`, groupID)
	batch.Query(`DELETE FROM group_members WHERE group_id = ?`, groupID)
	if err := batch.Exec(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete group"})
		return
	}
	if err := cleanupGroupsByMember(h.db, targetOrgID, groupID, memberIDs); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to clean group membership index"})
		return
	}
	groupUUID, err := uuid.Parse(groupID)
	if err == nil {
		if err := cleanupGroupShares(h.db, groupUUID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to clean group shares"})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// SearchOrgGroup searches groups by name.
// GET /org/:org_id/admin/search-group/?query=Q
// Frontend reads: res.data.groups, res.data.page_next, res.data.page
func (h *OrgAdminHandler) SearchOrgGroup(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}

	query := strings.ToLower(strings.TrimSpace(c.Query("query")))

	iter := h.db.Session().Query(`
		SELECT group_id, name, creator_id, created_at
		FROM groups WHERE org_id = ?
	`, targetOrgID).Iter()

	usersMap := h.resolveUsersMap(targetOrgID)

	var results []gin.H
	var groupID, name, creatorID string
	var createdAt time.Time

	for iter.Scan(&groupID, &name, &creatorID, &createdAt) {
		if query != "" && !strings.Contains(strings.ToLower(name), query) {
			continue
		}
		u := usersMap[creatorID]
		results = append(results, gin.H{
			"id":                    groupID,
			"group_name":            name,
			"creator_name":          u.Name,
			"creator_email":         u.Email,
			"creator_contact_email": "",
			"ctime":                 createdAt.Format(time.RFC3339),
		})
	}
	iter.Close()

	if results == nil {
		results = []gin.H{}
	}

	// org-groups-search-groups.js expects 'group_list' key (same convention as admin SearchGroups)
	c.JSON(http.StatusOK, gin.H{
		"group_list": results,
		"page":       1,
		"page_next":  false,
	})
}

// ============================================================================
// Group Members
// ============================================================================

// ListOrgGroupMembers lists members of a group.
// GET /org/:org_id/admin/groups/:gid/members/
// Frontend reads: res.data.members  (each: email, name, role, avatar_url)
func (h *OrgAdminHandler) ListOrgGroupMembers(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}

	groupID := c.Param("gid")

	// Verify group exists
	if err := h.db.Session().Query(`
		SELECT name FROM groups WHERE org_id = ? AND group_id = ?
	`, targetOrgID, groupID).Scan(new(string)); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "group not found"})
		return
	}

	iter := h.db.Session().Query(`
		SELECT user_id, role FROM group_members WHERE group_id = ?
	`, groupID).Iter()

	usersMap := h.resolveUsersMap(targetOrgID)

	var members []gin.H
	var userID, role string

	for iter.Scan(&userID, &role) {
		u := usersMap[userID]

		// Capitalize role for frontend: "owner" → "Owner", "admin" → "Admin", "member" → "Member"
		displayRole := role
		if len(role) > 0 {
			displayRole = strings.ToUpper(role[:1]) + role[1:]
		}

		members = append(members, gin.H{
			"email":      u.Email,
			"name":       u.Name,
			"role":       displayRole,
			"avatar_url": "/static/img/default-avatar.png",
		})
	}
	iter.Close()

	if members == nil {
		members = []gin.H{}
	}

	c.JSON(http.StatusOK, gin.H{"members": members})
}

// AddOrgGroupMember adds a member to a group.
// POST /org/:org_id/admin/groups/:gid/members/
func (h *OrgAdminHandler) AddOrgGroupMember(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}

	groupID := c.Param("gid")
	var req struct {
		Email string `json:"email"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	email := req.Email
	if email == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "email is required"})
		return
	}

	// Verify group exists
	var groupName string
	if err := h.db.Session().Query(`
		SELECT name FROM groups WHERE org_id = ? AND group_id = ?
	`, targetOrgID, groupID).Scan(&groupName); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "group not found"})
		return
	}

	// Resolve user
	memberID, err := h.lookupOrgUserByEmail(targetOrgID, email)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found in this organization"})
		return
	}

	now := time.Now()

	if err := upsertGroupMember(h.db, targetOrgID, groupID, memberID, groupName, "member", now); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to add group member"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// DeleteOrgGroupMember removes a member from a group.
// DELETE /org/:org_id/admin/groups/:gid/members/:email/
func (h *OrgAdminHandler) DeleteOrgGroupMember(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}

	groupID := c.Param("gid")
	email := c.Param("email")

	// Verify group exists
	if err := h.db.Session().Query(`
		SELECT name FROM groups WHERE org_id = ? AND group_id = ?
	`, targetOrgID, groupID).Scan(new(string)); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "group not found"})
		return
	}

	// Resolve user
	memberID, err := h.lookupOrgUserByEmail(targetOrgID, email)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	if err := deleteGroupMember(h.db, targetOrgID, groupID, memberID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete group member"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// UpdateOrgGroupMember updates a member's role (is_admin).
// PUT /org/:org_id/admin/groups/:gid/members/:email/
func (h *OrgAdminHandler) UpdateOrgGroupMember(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}

	groupID := c.Param("gid")
	email := c.Param("email")

	var req struct {
		IsAdmin bool `json:"is_admin"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	isAdmin := req.IsAdmin

	// Resolve user
	memberID, err := h.lookupOrgUserByEmail(targetOrgID, email)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	newRole := "member"
	if isAdmin {
		newRole = "admin"
	}

	// Update lookup table
	var groupName string
	h.db.Session().Query(`SELECT name FROM groups WHERE org_id = ? AND group_id = ?`,
		targetOrgID, groupID).Scan(&groupName)
	if err := upsertGroupMember(h.db, targetOrgID, groupID, memberID, groupName, newRole, time.Now()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update group member"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ============================================================================
// Group Libraries
// ============================================================================

// ListOrgGroupLibraries lists libraries shared with a group.
// GET /org/:org_id/admin/groups/:gid/libraries/
// Frontend reads: res.data.libraries  (each: repo_id, name, size, shared_by, shared_by_name, encrypted)
func (h *OrgAdminHandler) ListOrgGroupLibraries(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}

	groupID := c.Param("gid")

	// Verify group exists
	if err := h.db.Session().Query(`
		SELECT name FROM groups WHERE org_id = ? AND group_id = ?
	`, targetOrgID, groupID).Scan(new(string)); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "group not found"})
		return
	}

	// Look up libraries shared to this group by iterating the org's libraries
	// and checking shares per library (avoids ALLOW FILTERING on shares table).
	libIter := h.db.Session().Query(`
		SELECT library_id, owner_id, name, encrypted, size_bytes, deleted_at
		FROM libraries WHERE org_id = ?
	`, targetOrgID).Iter()

	usersMap := h.resolveUsersMap(targetOrgID)

	var libraries []gin.H
	var libID, ownerID, libName string
	var encrypted bool
	var sizeBytes int64
	var deletedAt time.Time

	for libIter.Scan(&libID, &ownerID, &libName, &encrypted, &sizeBytes, &deletedAt) {
		if !deletedAt.IsZero() {
			continue
		}
		// Check if this library has a share to the target group
		var perm string
		if err := h.db.Session().Query(`
			SELECT permission FROM shares
			WHERE library_id = ? AND shared_to = ? ALLOW FILTERING
		`, libID, groupID).Scan(&perm); err != nil {
			continue
		}
		u := usersMap[ownerID]
		libraries = append(libraries, gin.H{
			"repo_id":        libID,
			"name":           libName,
			"size":           sizeBytes,
			"shared_by":      u.Email,
			"shared_by_name": u.Name,
			"encrypted":      encrypted,
		})
	}
	libIter.Close()

	if libraries == nil {
		libraries = []gin.H{}
	}

	c.JSON(http.StatusOK, gin.H{"libraries": libraries})
}

// AddOrgGroupOwnedLibrary creates a group-owned library (department repo).
// POST /org/:org_id/admin/groups/:gid/group-owned-libraries/
func (h *OrgAdminHandler) AddOrgGroupOwnedLibrary(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}

	groupID := c.Param("gid")
	var req struct {
		RepoName string `json:"repo_name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	repoName := req.RepoName
	if repoName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "repo_name is required"})
		return
	}

	// Verify group exists
	var groupName string
	if err := h.db.Session().Query(`
		SELECT name FROM groups WHERE org_id = ? AND group_id = ?
	`, targetOrgID, groupID).Scan(&groupName); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "group not found"})
		return
	}

	callerUserID := c.GetString("user_id")
	newLibID := uuid.New().String()
	now := time.Now()

	batch := h.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		INSERT INTO libraries (org_id, library_id, owner_id, name, encrypted, size_bytes, file_count, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, targetOrgID, newLibID, callerUserID, repoName, false, int64(0), int64(0), now, now)
	batch.Query(`
		INSERT INTO libraries_by_id (library_id, org_id, owner_id, name, encrypted)
		VALUES (?, ?, ?, ?, ?)
	`, newLibID, targetOrgID, callerUserID, repoName, false)
	if err := batch.Exec(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create library"})
		return
	}

	// Initialize filesystem (root dir + initial commit)
	fsHelper := NewFSHelper(h.db)
	if err := fsHelper.InitializeLibraryFS(targetOrgID, newLibID, callerUserID, repoName); err != nil {
		_ = rollbackNewLibrary(h.db, targetOrgID, newLibID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to initialize library filesystem"})
		return
	}

	// Share to group with rw permission
	shareID := uuid.New().String()
	if err := createLibraryShare(h.db, newLibID, shareID, callerUserID, groupID, "group", "rw", now, nil); err != nil {
		_ = rollbackNewLibrary(h.db, targetOrgID, newLibID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to share library with group"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"repo_id":   newLibID,
		"repo_name": repoName,
		"group_id":  groupID,
	})
}

// DeleteOrgGroupOwnedLibrary deletes a group-owned library (soft-delete).
// DELETE /org/:org_id/admin/groups/:gid/group-owned-libraries/:rid/
func (h *OrgAdminHandler) DeleteOrgGroupOwnedLibrary(c *gin.Context) {
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

// ============================================================================
