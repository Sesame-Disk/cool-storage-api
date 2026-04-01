package v2

// admin_extra.go — Additional admin panel endpoints (sysinfo, statistics, devices,
// web-settings, logs, share links, notifications, institutions, invitations, org
// user management, search organizations).
//
// These are stub implementations returning realistic empty/default data matching
// the response format expected by the Seahub-compatible frontend.

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ============================================================================
// System Notifications — GET/POST/PUT/DELETE /admin/sys-notifications/
// ============================================================================

func (h *AdminHandler) AdminListSysNotifications(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"notifications": []gin.H{},
	})
}

func (h *AdminHandler) AdminAddSysNotification(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	var req struct {
		Msg string `json:"msg"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "msg is required"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"notification": gin.H{
			"id":         1,
			"msg":        req.Msg,
			"is_current": false,
		},
	})
}

func (h *AdminHandler) AdminUpdateSysNotification(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *AdminHandler) AdminDeleteSysNotification(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ============================================================================
// Institutions — GET/POST/PUT/DELETE /admin/institutions/
// ============================================================================

func (h *AdminHandler) AdminListInstitutions(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"institution_list": []gin.H{},
		"total_count":      0,
	})
}

func (h *AdminHandler) AdminAddInstitution(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"id":    1,
		"name":  req.Name,
		"ctime": time.Now().Format(time.RFC3339),
	})
}

func (h *AdminHandler) AdminGetInstitution(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}
	id := c.Param("id")
	c.JSON(http.StatusOK, gin.H{
		"id":   id,
		"name": "Institution",
	})
}

func (h *AdminHandler) AdminUpdateInstitution(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func (h *AdminHandler) AdminDeleteInstitution(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ============================================================================
// Invitations — GET/DELETE /admin/invitations/
// ============================================================================

func (h *AdminHandler) AdminListInvitations(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"invitation_list": []gin.H{},
		"total_count":     0,
	})
}

func (h *AdminHandler) AdminDeleteInvitation(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ============================================================================
// License upload — POST /admin/license/
// ============================================================================

func (h *AdminHandler) AdminUploadLicense(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"with_license":       true,
		"license_expiration": "2030-12-31",
		"license_mode":       "subscription",
		"license_maxusers":   1000,
		"license_to":         "SesameFS",
	})
}

// AdminListUserGroups returns groups that a user belongs to
// GET /admin/users/:email/groups/ (dispatched via adminUsersHandler)
func (h *AdminHandler) AdminListUserGroups(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	email := c.GetString("resolved_user_param")
	if email == "" {
		c.JSON(http.StatusOK, gin.H{"group_list": []gin.H{}})
		return
	}

	targetUserID, targetOrgID, err := h.lookupUserByEmail(email)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"group_list": []gin.H{}})
		return
	}

	iter := h.db.Session().Query(`
		SELECT group_id, group_name, role, added_at
		FROM groups_by_member
		WHERE org_id = ? AND user_id = ?
	`, targetOrgID, targetUserID).Iter()

	var groupList []gin.H
	var groupID, groupName, role string
	var addedAt time.Time

	for iter.Scan(&groupID, &groupName, &role, &addedAt) {
		displayRole := "Member"
		switch strings.ToLower(role) {
		case "owner":
			displayRole = "Owner"
		case "admin":
			displayRole = "Admin"
		}
		groupList = append(groupList, gin.H{
			"id":              groupID,
			"name":            groupName,
			"role":            displayRole,
			"created_at":      addedAt.Format(time.RFC3339),
			"parent_group_id": 0,
		})
	}
	iter.Close()

	if groupList == nil {
		groupList = []gin.H{}
	}

	c.JSON(http.StatusOK, gin.H{"group_list": groupList})
}

// ============================================================================
// User group member role update — PUT /admin/groups/:group_id/members/:email/
// ============================================================================

func (h *AdminHandler) AdminUpdateGroupMemberRole(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	groupID := c.Param("group_id")
	email := c.Param("email")

	// Resolve the group's own org_id — callerOrgID may differ (e.g. superadmin).
	var orgID string
	groupIter := h.db.Session().Query(`
		SELECT org_id FROM groups_by_id WHERE group_id = ?
	`, groupID).Iter()
	found := groupIter.Scan(&orgID)
	groupIter.Close()
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "group not found"})
		return
	}

	var groupMemberReq struct {
		IsAdmin bool `json:"is_admin"`
	}
	c.ShouldBindJSON(&groupMemberReq) //nolint:errcheck
	isAdmin := groupMemberReq.IsAdmin
	newRole := "member"
	if isAdmin {
		newRole = "admin"
	}

	// Resolve user by email
	memberID, _, err := h.lookupUserByEmail(email)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	batch := h.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		INSERT INTO group_members (group_id, user_id, role, added_at)
		VALUES (?, ?, ?, toTimestamp(now()))
	`, groupID, memberID, newRole)
	batch.Query(`
		UPDATE groups_by_member SET role = ? WHERE org_id = ? AND user_id = ? AND group_id = ?
	`, newRole, orgID, memberID, groupID)
	if err := batch.Exec(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update group member role"})
		return
	}

	displayRole := "Member"
	if isAdmin {
		displayRole = "Admin"
	}

	c.JSON(http.StatusOK, gin.H{
		"success":  true,
		"is_admin": isAdmin,
		"role":     displayRole,
	})
}

// ============================================================================
// Departments / Address-book groups
// ============================================================================

// AdminListOrgDepartments lists department groups for a specific org.
// GET /admin/organizations/:org_id/departments/
func (h *AdminHandler) AdminListOrgDepartments(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	targetOrgID := c.Param("org_id")

	iter := h.db.Session().Query(`
		SELECT group_id, name, parent_group_id, is_department, created_at
		FROM groups WHERE org_id = ?
	`, targetOrgID).Iter()

	var results []gin.H
	var groupID, name, parentGroupID string
	var isDept bool
	var createdAt time.Time

	for iter.Scan(&groupID, &name, &parentGroupID, &isDept, &createdAt) {
		if !isDept {
			continue
		}
		results = append(results, gin.H{
			"id":              groupID,
			"name":            name,
			"parent_group_id": parentGroupID,
			"created_at":      createdAt.Format(time.RFC3339),
		})
	}
	iter.Close()

	if results == nil {
		results = []gin.H{}
	}
	c.JSON(http.StatusOK, gin.H{"data": results})
}

// AdminListAddressBookGroups lists all department (address-book) groups in the caller's org.
// GET /admin/address-book/groups/
func (h *AdminHandler) AdminListAddressBookGroups(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	iter := h.db.Session().Query(`
		SELECT group_id, name, parent_group_id, is_department, created_at
		FROM groups WHERE org_id = ?
	`, callerOrgID).Iter()

	var results []gin.H
	var groupID, name, parentGroupID string
	var isDept bool
	var createdAt time.Time

	for iter.Scan(&groupID, &name, &parentGroupID, &isDept, &createdAt) {
		if !isDept {
			continue
		}
		results = append(results, gin.H{
			"id":              groupID,
			"name":            name,
			"parent_group_id": parentGroupID,
			"created_at":      createdAt.Format(time.RFC3339),
		})
	}
	iter.Close()

	if results == nil {
		results = []gin.H{}
	}
	c.JSON(http.StatusOK, gin.H{"data": results})
}

// AdminAddAddressBookGroup creates a new department group.
// POST /admin/address-book/groups/
func (h *AdminHandler) AdminAddAddressBookGroup(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	var req struct {
		GroupName   string `json:"group_name"`
		ParentGroup string `json:"parent_group"`
		GroupOwner  string `json:"group_owner"`
		GroupStaff  string `json:"group_staff"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	groupName := req.GroupName
	if groupName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "group_name is required"})
		return
	}
	parentGroup := req.ParentGroup
	groupOwner := req.GroupOwner
	groupStaff := req.GroupStaff

	newGroupID := uuid.New().String()
	now := time.Now()

	creatorID := callerUserID
	if groupOwner != "" {
		if ownerID, _, err := h.lookupUserByEmail(groupOwner); err == nil {
			creatorID = ownerID
		}
	}

	staffIDs := map[string]struct{}{}
	if groupStaff != "" {
		for _, staffEmail := range strings.Split(groupStaff, ",") {
			staffEmail = strings.TrimSpace(staffEmail)
			if staffEmail == "" {
				continue
			}
			staffID, _, err := h.lookupUserByEmail(staffEmail)
			if err != nil || staffID == "" || staffID == creatorID {
				continue
			}
			staffIDs[staffID] = struct{}{}
		}
	}

	projectionRow := buildAdminGroupProjectionRow(h.db, callerOrgID, newGroupID, groupName, creatorID, parentGroup, true, now)

	batch := h.db.Session().Batch(gocql.LoggedBatch)
	if parentGroup != "" {
		batch.Query(`
			INSERT INTO groups (org_id, group_id, name, creator_id, parent_group_id, is_department, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, callerOrgID, newGroupID, groupName, creatorID, parentGroup, true, now, now)
	} else {
		batch.Query(`
			INSERT INTO groups (org_id, group_id, name, creator_id, is_department, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`, callerOrgID, newGroupID, groupName, creatorID, true, now, now)
	}
	batch.Query(`
		INSERT INTO groups_by_id (group_id, org_id, name) VALUES (?, ?, ?)
	`, newGroupID, callerOrgID, groupName)
	batch.Query(`
		INSERT INTO group_members (group_id, user_id, role, added_at)
		VALUES (?, ?, ?, ?)
	`, newGroupID, creatorID, "owner", now)
	batch.Query(`
		INSERT INTO groups_by_member (org_id, user_id, group_id, group_name, role, added_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, callerOrgID, creatorID, newGroupID, groupName, "owner", now)
	for staffID := range staffIDs {
		batch.Query(`
			INSERT INTO group_members (group_id, user_id, role, added_at)
			VALUES (?, ?, ?, ?)
		`, newGroupID, staffID, "member", now)
		batch.Query(`
			INSERT INTO groups_by_member (org_id, user_id, group_id, group_name, role, added_at)
			VALUES (?, ?, ?, ?, ?, ?)
		`, callerOrgID, staffID, newGroupID, groupName, "member", now)
	}
	addAdminGroupReadModelUpsertQuery(batch, projectionRow)
	if err := batch.Exec(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create address-book group"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"id":              newGroupID,
		"name":            groupName,
		"parent_group_id": parentGroup,
		"created_at":      now.Format(time.RFC3339),
	})
}

// AdminGetAddressBookGroup returns details for a single address-book group.
// GET /admin/address-book/groups/:group_id/?return_ancestors=true
func (h *AdminHandler) AdminGetAddressBookGroup(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	groupID := c.Param("group_id")

	var name, creatorID, parentGroupID string
	var isDept bool
	var createdAt time.Time
	if err := h.db.Session().Query(`
		SELECT name, creator_id, parent_group_id, is_department, created_at
		FROM groups WHERE org_id = ? AND group_id = ?
	`, callerOrgID, groupID).Scan(&name, &creatorID, &parentGroupID, &isDept, &createdAt); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "group not found"})
		return
	}

	result := gin.H{
		"id":              groupID,
		"name":            name,
		"parent_group_id": parentGroupID,
		"created_at":      createdAt.Format(time.RFC3339),
	}

	if c.Query("return_ancestors") == "true" {
		var ancestors []gin.H
		currentParent := parentGroupID
		for currentParent != "" {
			var pName, pParent string
			if err := h.db.Session().Query(`
				SELECT name, parent_group_id FROM groups WHERE org_id = ? AND group_id = ?
			`, callerOrgID, currentParent).Scan(&pName, &pParent); err != nil {
				break
			}
			ancestors = append(ancestors, gin.H{
				"id":              currentParent,
				"name":            pName,
				"parent_group_id": pParent,
			})
			currentParent = pParent
		}
		if ancestors == nil {
			ancestors = []gin.H{}
		}
		result["ancestor_groups"] = ancestors
	}

	c.JSON(http.StatusOK, result)
}

// AdminUpdateAddressBookGroup updates a department group's name.
// PUT /admin/address-book/groups/:group_id/
func (h *AdminHandler) AdminUpdateAddressBookGroup(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	groupID := c.Param("group_id")
	var req struct {
		GroupName string `json:"group_name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	newName := req.GroupName
	if newName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "group_name is required"})
		return
	}

	if err := h.db.Session().Query(`
		SELECT name FROM groups WHERE org_id = ? AND group_id = ?
	`, callerOrgID, groupID).Scan(new(string)); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "group not found"})
		return
	}

	if err := renameGroup(h.db, callerOrgID, groupID, newName, time.Now()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to rename group"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// AdminDeleteAddressBookGroup deletes a department group, its members, and related shares.
// DELETE /admin/address-book/groups/:group_id/
func (h *AdminHandler) AdminDeleteAddressBookGroup(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	groupID := c.Param("group_id")

	var groupName string
	if err := h.db.Session().Query(`
		SELECT name FROM groups WHERE org_id = ? AND group_id = ?
	`, callerOrgID, groupID).Scan(&groupName); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "group not found"})
		return
	}

	deleteState, err := loadGroupDeleteState(h.db, callerOrgID, groupID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load group cleanup state"})
		return
	}

	// Atomic batch: delete group + indexes + shares + read model
	batch := h.db.Session().Batch(gocql.LoggedBatch)
	addDeleteGroupMutationQueries(batch, callerOrgID, groupID, deleteState)
	if err := batch.Exec(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete group"})
		return
	}

	// Audit log
	orgUUID, _ := uuid.Parse(callerOrgID)
	auditDetails, _ := json.Marshal(map[string]interface{}{
		"group_name":    groupName,
		"members_count": len(deleteState.memberIDs),
	})
	if err := h.db.Session().Query(`
		INSERT INTO audit_log (org_id, timestamp, action, target_type, target_id, actor_id, details)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, orgUUID.String(), time.Now(), "delete_address_book_group", "group", groupID, callerUserID,
		string(auditDetails)).Exec(); err != nil {
		log.Printf("[AdminDeleteAddressBookGroup] failed to write audit log for group %s in org %s: %v", groupID, callerOrgID, err)
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ============================================================================
// Group-owned libraries
// ============================================================================

// AdminAddGroupOwnedLibrary creates a group-owned library (department repo).
// POST /admin/groups/:group_id/group-owned-libraries/
func (h *AdminHandler) AdminAddGroupOwnedLibrary(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	groupID := c.Param("group_id")
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
	`, callerOrgID, groupID).Scan(&groupName); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "group not found"})
		return
	}

	newLibID := uuid.New().String()
	now := time.Now()

	batch := h.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		INSERT INTO libraries (org_id, library_id, owner_id, name, encrypted, size_bytes, file_count, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, callerOrgID, newLibID, callerUserID, repoName, false, int64(0), int64(0), now, now)
	batch.Query(`
		INSERT INTO libraries_by_id (library_id, org_id, owner_id, name, encrypted)
		VALUES (?, ?, ?, ?, ?)
	`, newLibID, callerOrgID, callerUserID, repoName, false)
	if err := batch.Exec(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create library"})
		return
	}

	// Initialize filesystem (root dir + initial commit)
	fsHelper := NewFSHelper(h.db)
	if err := fsHelper.InitializeLibraryFS(callerOrgID, newLibID, callerUserID, repoName); err != nil {
		_ = rollbackNewLibrary(h.db, callerOrgID, newLibID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to initialize library filesystem"})
		return
	}

	// Share to group with rw permission
	shareID := uuid.New().String()
	if err := createLibraryShare(h.db, newLibID, shareID, callerUserID, groupID, "group", "rw", now, nil); err != nil {
		_ = rollbackNewLibrary(h.db, callerOrgID, newLibID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to share library with group"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"repo_id":   newLibID,
		"repo_name": repoName,
		"group_id":  groupID,
	})
}

// AdminDeleteGroupOwnedLibrary soft-deletes a group-owned library.
// DELETE /admin/groups/:group_id/group-owned-libraries/:library_id/
func (h *AdminHandler) AdminDeleteGroupOwnedLibrary(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	repoID := c.Param("library_id")

	var deletedAt time.Time
	var ownerID string
	if err := h.db.Session().Query(`
		SELECT deleted_at, owner_id FROM libraries WHERE org_id = ? AND library_id = ?
	`, callerOrgID, repoID).Scan(&deletedAt, &ownerID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}
	if !deletedAt.IsZero() {
		c.JSON(http.StatusNotFound, gin.H{"error": "library already deleted"})
		return
	}

	if err := softDeleteLibrary(h.db, callerOrgID, ownerID, callerUserID, repoID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete library"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ============================================================================
// Helpers
// ============================================================================

func (h *AdminHandler) countOrgUsers(orgID string) int {
	count := 0
	iter := h.db.Session().Query(`SELECT user_id FROM users WHERE org_id = ?`, orgID).Iter()
	var dummy string
	for iter.Scan(&dummy) {
		count++
	}
	iter.Close()
	return count
}

func (h *AdminHandler) countOrgGroups(orgID string) int {
	count := 0
	iter := h.db.Session().Query(`SELECT group_id FROM groups WHERE org_id = ?`, orgID).Iter()
	var dummy string
	for iter.Scan(&dummy) {
		count++
	}
	iter.Close()
	return count
}

func (h *AdminHandler) countOrgLibraries(orgID string) int {
	count := 0
	iter := h.db.Session().Query(`SELECT library_id FROM libraries WHERE org_id = ?`, orgID).Iter()
	var dummy string
	for iter.Scan(&dummy) {
		count++
	}
	iter.Close()
	return count
}

// resolveOrgCreator returns the email and name of the first owner/admin user in an org.
// Falls back to the first user if no owner/admin/superadmin exists.
func (h *AdminHandler) resolveOrgCreator(orgID string) (string, string) {
	var email, name, role string
	var firstEmail, firstName string
	first := true
	iter := h.db.Session().Query(`
		SELECT email, name, role FROM users WHERE org_id = ?
	`, orgID).Iter()
	for iter.Scan(&email, &name, &role) {
		if first {
			firstEmail, firstName = email, name
			first = false
		}
		if role == "superadmin" || role == "owner" || role == "admin" {
			iter.Close()
			return email, name
		}
	}
	iter.Close()
	return firstEmail, firstName
}

func generateUserID() string {
	return "u-" + time.Now().Format("20060102150405") + "-" + strconv.FormatInt(time.Now().UnixNano()%10000, 10)
}
