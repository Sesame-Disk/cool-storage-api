package v2

import (
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// =============================================================================
// Phase 1: Admin Group Endpoints
// =============================================================================

// adminGroupResponse matches the field names expected by the JS frontend (groups-content.js).
type adminGroupResponse struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Owner         string `json:"owner"`
	OwnerName     string `json:"owner_name"`
	CreatedAt     string `json:"created_at"`
	ParentGroupID int    `json:"parent_group_id"`
	OrgID         string `json:"org_id,omitempty"`
}

func adminGroupResponseFromProjection(row dbpkg.AdminGroupProjectionRow) adminGroupResponse {
	return adminGroupResponse{
		ID:            row.GroupID,
		Name:          row.Name,
		Owner:         row.OwnerEmail,
		OwnerName:     row.OwnerName,
		CreatedAt:     row.CreatedAt.Format(time.RFC3339),
		ParentGroupID: 0,
		OrgID:         row.OrgID,
	}
}

// ListAllGroups returns all groups in the caller's org with pagination.
// GET /admin/groups/?page=N&per_page=N
func (h *AdminHandler) ListAllGroups(c *gin.Context) {
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
	var allGroups []adminGroupResponse
	if middleware.IsPlatformSuperAdmin(callerOrgID, callerRole) {
		rows, err := dbpkg.ListAdminGlobalGroupRows(h.db.Session())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list groups"})
			return
		}
		allGroups = make([]adminGroupResponse, 0, len(rows))
		for _, row := range rows {
			allGroups = append(allGroups, adminGroupResponseFromProjection(row))
		}
	} else {
		iter := h.db.Session().Query(`
			SELECT group_id, name, creator_id, created_at FROM groups WHERE org_id = ?
		`, callerOrgID).Iter()
		var groupID, name, creatorID string
		var createdAt time.Time
		for iter.Scan(&groupID, &name, &creatorID, &createdAt) {
			ownerEmail, ownerName := dbpkg.ResolveAdminGroupOwnerFields(h.db.Session(), callerOrgID, creatorID)
			allGroups = append(allGroups, adminGroupResponse{
				ID:            groupID,
				Name:          name,
				Owner:         ownerEmail,
				OwnerName:     ownerName,
				CreatedAt:     createdAt.Format(time.RFC3339),
				ParentGroupID: 0,
				OrgID:         callerOrgID,
			})
		}
		if err := iter.Close(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list groups"})
			return
		}
	}

	// Paginate
	total := len(allGroups)
	start := (page - 1) * perPage
	if start > total {
		start = total
	}
	end := start + perPage
	if end > total {
		end = total
	}
	pageGroups := allGroups[start:end]
	if pageGroups == nil {
		pageGroups = []adminGroupResponse{}
	}

	c.JSON(http.StatusOK, gin.H{
		"groups": pageGroups,
		"page_info": gin.H{
			"current_page":  page,
			"has_next_page": end < total,
		},
	})
}

// SearchGroups searches groups by name.
// GET /admin/search-group/?query=name
func (h *AdminHandler) SearchGroups(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")

	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	query := strings.ToLower(c.Query("query"))
	if query == "" {
		c.JSON(http.StatusOK, gin.H{"groups": []adminGroupResponse{}})
		return
	}

	callerRole, _ := h.permMiddleware.GetUserOrgRole(callerOrgID, callerUserID)
	var results []adminGroupResponse
	if middleware.IsPlatformSuperAdmin(callerOrgID, callerRole) {
		rows, err := dbpkg.ListAdminGlobalGroupRows(h.db.Session())
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to search groups"})
			return
		}
		for _, row := range rows {
			if strings.Contains(strings.ToLower(row.Name), query) {
				results = append(results, adminGroupResponseFromProjection(row))
			}
		}
	} else {
		iter := h.db.Session().Query(`
			SELECT group_id, name, creator_id, created_at FROM groups WHERE org_id = ?
		`, callerOrgID).Iter()
		var groupID, name, creatorID string
		var createdAt time.Time
		for iter.Scan(&groupID, &name, &creatorID, &createdAt) {
			if !strings.Contains(strings.ToLower(name), query) {
				continue
			}
			ownerEmail, ownerName := dbpkg.ResolveAdminGroupOwnerFields(h.db.Session(), callerOrgID, creatorID)
			results = append(results, adminGroupResponse{
				ID:            groupID,
				Name:          name,
				Owner:         ownerEmail,
				OwnerName:     ownerName,
				CreatedAt:     createdAt.Format(time.RFC3339),
				ParentGroupID: 0,
				OrgID:         callerOrgID,
			})
		}
		if err := iter.Close(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to search groups"})
			return
		}
	}

	if results == nil {
		results = []adminGroupResponse{}
	}

	// search-groups.js expects 'group_list' key
	c.JSON(http.StatusOK, gin.H{"group_list": results})
}

// AdminCreateGroup creates a new group as admin.
// POST /admin/groups/ (FormData: group_name, group_owner)
func (h *AdminHandler) AdminCreateGroup(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")

	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	var createGroupReq struct {
		GroupName  string `json:"group_name"`
		GroupOwner string `json:"group_owner"`
	}
	if err := c.ShouldBindJSON(&createGroupReq); err != nil || createGroupReq.GroupName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "group_name is required"})
		return
	}
	groupName := createGroupReq.GroupName
	groupOwnerEmail := createGroupReq.GroupOwner

	orgID := callerOrgID

	// Resolve owner: use provided email or fallback to caller
	var ownerID, ownerEmail string
	if groupOwnerEmail != "" {
		var ownerOrgID string
		var err error
		ownerID, ownerOrgID, err = h.lookupUserByEmail(groupOwnerEmail)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "group_owner not found"})
			return
		}
		if ownerOrgID != orgID {
			c.JSON(http.StatusBadRequest, gin.H{"error": "group_owner must be in the same organization"})
			return
		}
		ownerEmail = groupOwnerEmail
	} else {
		ownerID = callerUserID
		h.db.Session().Query(`
			SELECT email FROM users WHERE org_id = ? AND user_id = ?
		`, orgID, callerUserID).Scan(&ownerEmail)
	}

	groupUUID := uuid.New()
	now := time.Now()
	projectionRow := buildAdminGroupProjectionRow(h.db, orgID, groupUUID.String(), groupName, ownerID, "", false, now)

	// Atomic batch: create group + lookup + owner membership
	batch := h.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`INSERT INTO groups (org_id, group_id, name, creator_id, is_department, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		orgID, groupUUID.String(), groupName, ownerID, false, now, now)
	batch.Query(`INSERT INTO groups_by_id (group_id, org_id, name) VALUES (?, ?, ?)`,
		groupUUID.String(), orgID, groupName)
	batch.Query(`INSERT INTO group_members (group_id, user_id, role, added_at) VALUES (?, ?, ?, ?)`,
		groupUUID.String(), ownerID, "owner", now)
	batch.Query(`INSERT INTO groups_by_member (org_id, user_id, group_id, group_name, role, added_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		orgID, ownerID, groupUUID.String(), groupName, "owner", now)
	addAdminGroupReadModelUpsertQuery(batch, projectionRow)
	if err := batch.Exec(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create group"})
		return
	}

	c.JSON(http.StatusCreated, adminGroupResponse{
		ID:            groupUUID.String(),
		Name:          groupName,
		Owner:         projectionRow.OwnerEmail,
		OwnerName:     projectionRow.OwnerName,
		CreatedAt:     now.Format(time.RFC3339),
		ParentGroupID: 0,
	})
}

// AdminDeleteGroup deletes a group and cleans up all related data.
// DELETE /admin/groups/:group_id/
func (h *AdminHandler) AdminDeleteGroup(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")

	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	groupID := c.Param("group_id")
	orgID := callerOrgID

	// Verify group exists
	var name string
	if err := h.db.Session().Query(`
		SELECT name FROM groups WHERE org_id = ? AND group_id = ?
	`, orgID, groupID).Scan(&name); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "group not found"})
		return
	}

	deleteState, err := loadGroupDeleteState(h.db, orgID, groupID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load group cleanup state"})
		return
	}

	// Atomic batch: delete group + indexes + shares + read model
	batch := h.db.Session().Batch(gocql.LoggedBatch)
	addDeleteGroupMutationQueries(batch, orgID, groupID, deleteState)
	if err := batch.Exec(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete group"})
		return
	}

	// Audit log
	orgUUID, _ := uuid.Parse(orgID)
	auditDetails, _ := json.Marshal(map[string]interface{}{
		"group_name":    name,
		"members_count": len(deleteState.memberIDs),
	})
	if err := h.db.Session().Query(`
		INSERT INTO audit_log (org_id, timestamp, action, target_type, target_id, actor_id, details)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, orgUUID.String(), time.Now(), "delete_group", "group", groupID, callerUserID,
		string(auditDetails)).Exec(); err != nil {
		log.Printf("[AdminDeleteGroup] failed to write audit log for group %s in org %s: %v", groupID, orgID, err)
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// AdminTransferGroup transfers group ownership or renames a group.
// PUT /admin/groups/:group_id/ (FormData: new_owner OR name)
// When 'name' is provided, renames the group (used by sysAdminRenameDepartment in seafile-js).
// When 'new_owner' is provided, transfers ownership.
func (h *AdminHandler) AdminTransferGroup(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")

	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	groupID := c.Param("group_id")
	var transferGroupReq struct {
		NewOwner string `json:"new_owner"`
		Name     string `json:"name"`
	}
	c.ShouldBindJSON(&transferGroupReq) //nolint:errcheck
	newOwnerEmail := transferGroupReq.NewOwner
	newName := transferGroupReq.Name

	// Resolve the group's actual org_id from groups_by_id (superadmin may operate on groups outside their own org)
	callerRole, _ := h.permMiddleware.GetUserOrgRole(callerOrgID, callerUserID)
	orgID := callerOrgID
	if middleware.IsPlatformSuperAdmin(callerOrgID, callerRole) {
		var groupOrgID string
		if err := h.db.Session().Query(`SELECT org_id FROM groups_by_id WHERE group_id = ?`, groupID).Scan(&groupOrgID); err == nil && groupOrgID != "" {
			orgID = groupOrgID
		}
	}

	// Rename path: seafile-js sysAdminRenameDepartment sends PUT /admin/groups/:id/ with 'name'
	if newOwnerEmail == "" && newName != "" {
		now := time.Now()
		if err := renameGroup(h.db, orgID, groupID, newName, now); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to rename group"})
			return
		}
		// Re-fetch current owner info to return a complete group object
		var creatorID string
		h.db.Session().Query(`SELECT creator_id FROM groups WHERE org_id = ? AND group_id = ?`, orgID, groupID).Scan(&creatorID)
		var ownerEmail, ownerName string
		h.db.Session().Query(`SELECT email, name FROM users WHERE org_id = ? AND user_id = ?`, orgID, creatorID).Scan(&ownerEmail, &ownerName)
		c.JSON(http.StatusOK, adminGroupResponse{
			ID:            groupID,
			Name:          newName,
			Owner:         ownerEmail,
			OwnerName:     ownerName,
			CreatedAt:     now.Format(time.RFC3339),
			ParentGroupID: 0,
		})
		return
	}

	if newOwnerEmail == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "new_owner is required"})
		return
	}

	// Verify group exists
	projectionRow, err := readAdminGroupReadModelRow(h.db, orgID, groupID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "group not found"})
		return
	}
	creatorID := projectionRow.CreatorID

	// Resolve new owner
	newOwnerID, newOwnerOrgID, err := h.lookupUserByEmail(newOwnerEmail)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "new_owner not found"})
		return
	}
	if newOwnerOrgID != orgID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "new_owner must be in the same organization"})
		return
	}

	now := time.Now()
	updatedProjectionRow := projectionRow
	updatedProjectionRow.CreatorID = newOwnerID
	updatedProjectionRow.OwnerEmail, updatedProjectionRow.OwnerName = dbpkg.ResolveAdminGroupOwnerFields(h.db.Session(), orgID, newOwnerID)

	batch := h.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		UPDATE groups SET creator_id = ?, updated_at = ? WHERE org_id = ? AND group_id = ?
	`, newOwnerID, now, orgID, groupID)
	batch.Query(`
		UPDATE group_members SET role = ? WHERE group_id = ? AND user_id = ?
	`, "member", groupID, creatorID)
	batch.Query(`
		UPDATE groups_by_member SET role = ? WHERE org_id = ? AND user_id = ? AND group_id = ?
	`, "member", orgID, creatorID, groupID)
	batch.Query(`
		INSERT INTO group_members (group_id, user_id, role, added_at)
		VALUES (?, ?, ?, ?)
	`, groupID, newOwnerID, "owner", now)
	batch.Query(`
		INSERT INTO groups_by_member (org_id, user_id, group_id, group_name, role, added_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, orgID, newOwnerID, groupID, projectionRow.Name, "owner", now)
	addAdminGroupReadModelUpsertQuery(batch, updatedProjectionRow)
	if err := batch.Exec(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to transfer group ownership"})
		return
	}

	// Return updated group object (frontend replaces item = res.data)
	var newOwnerName string
	h.db.Session().Query(`SELECT name FROM users WHERE org_id = ? AND user_id = ?`, orgID, newOwnerID).Scan(&newOwnerName)
	if newOwnerName == "" {
		newOwnerName = newOwnerEmail
	}
	c.JSON(http.StatusOK, adminGroupResponse{
		ID:            groupID,
		Name:          projectionRow.Name,
		Owner:         newOwnerEmail,
		OwnerName:     newOwnerName,
		CreatedAt:     now.Format(time.RFC3339),
		ParentGroupID: 0,
	})
}

// AdminListGroupMembers lists members of a group.
// GET /admin/groups/:group_id/members/
func (h *AdminHandler) AdminListGroupMembers(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")

	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	groupID := c.Param("group_id")

	// Resolve the group's own org_id — callerOrgID may differ (e.g. superadmin
	// looking at a group that belongs to a tenant org).
	var orgID, groupName string
	groupIter := h.db.Session().Query(`
		SELECT org_id, name FROM groups_by_id WHERE group_id = ?
	`, groupID).Iter()
	found := groupIter.Scan(&orgID, &groupName)
	groupIter.Close()
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "group not found"})
		return
	}

	iter := h.db.Session().Query(`
		SELECT user_id, role, added_at FROM group_members WHERE group_id = ?
	`, groupID).Iter()

	type memberResponse struct {
		Email     string `json:"email"`
		Name      string `json:"name"`
		Role      string `json:"role"`
		AvatarURL string `json:"avatar_url"`
		IsAdmin   bool   `json:"is_admin"`
	}

	var members []memberResponse
	var userID, role string
	var addedAt time.Time

	for iter.Scan(&userID, &role, &addedAt) {
		var email, uname string
		h.db.Session().Query(`
			SELECT email, name FROM users WHERE org_id = ? AND user_id = ?
		`, orgID, userID).Scan(&email, &uname)

		// Capitalize role for frontend: "owner" → "Owner", "admin" → "Admin", "member" → "Member"
		displayRole := strings.ToUpper(role[:1]) + role[1:]

		members = append(members, memberResponse{
			Email:     email,
			Name:      uname,
			Role:      displayRole,
			AvatarURL: "/static/img/default-avatar.png",
			IsAdmin:   role == "admin" || role == "owner",
		})
	}
	iter.Close()

	if members == nil {
		members = []memberResponse{}
	}

	c.JSON(http.StatusOK, gin.H{
		"members":    members,
		"group_name": groupName,
		"page_info": gin.H{
			"has_next_page": false,
			"current_page":  1,
		},
	})
}

// AdminAddGroupMember adds members to a group.
// POST /admin/groups/:group_id/members/ (FormData: email — may appear multiple times)
// Returns { success: [{email, name, role, avatar_url}], failed: [{email, error_msg}] }
func (h *AdminHandler) AdminAddGroupMember(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")

	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	groupID := c.Param("group_id")

	var addMemberReq struct {
		Emails []string `json:"emails"`
	}
	if err := c.ShouldBindJSON(&addMemberReq); err != nil || len(addMemberReq.Emails) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "emails is required"})
		return
	}
	emails := addMemberReq.Emails

	// Resolve the group's own org_id — callerOrgID may differ (e.g. superadmin).
	var orgID, groupName string
	groupIter := h.db.Session().Query(`
		SELECT org_id, name FROM groups_by_id WHERE group_id = ?
	`, groupID).Iter()
	found := groupIter.Scan(&orgID, &groupName)
	groupIter.Close()
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "group not found"})
		return
	}

	type successItem struct {
		Email     string `json:"email"`
		Name      string `json:"name"`
		Role      string `json:"role"`
		AvatarURL string `json:"avatar_url"`
	}
	type failedItem struct {
		Email    string `json:"email"`
		ErrorMsg string `json:"error_msg"`
	}

	var success []successItem
	var failed []failedItem

	for _, email := range emails {
		email = strings.TrimSpace(email)
		if email == "" {
			continue
		}

		// Resolve user by email
		memberID, memberOrgID, err := h.lookupUserByEmail(email)
		if err != nil {
			failed = append(failed, failedItem{Email: email, ErrorMsg: "user not found"})
			continue
		}
		if memberOrgID != orgID {
			failed = append(failed, failedItem{Email: email, ErrorMsg: "user must be in the same organization"})
			continue
		}

		now := time.Now()

		if err := upsertGroupMember(h.db, orgID, groupID, memberID, groupName, "member", now); err != nil {
			failed = append(failed, failedItem{Email: email, ErrorMsg: "failed to add member"})
			continue
		}

		// Get member name for response
		var memberName string
		h.db.Session().Query(`SELECT name FROM users WHERE org_id = ? AND user_id = ?`, orgID, memberID).Scan(&memberName)

		success = append(success, successItem{
			Email:     email,
			Name:      memberName,
			Role:      "Member",
			AvatarURL: "/static/img/default-avatar.png",
		})
	}

	if success == nil {
		success = []successItem{}
	}
	if failed == nil {
		failed = []failedItem{}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": success,
		"failed":  failed,
	})
}

// AdminRemoveGroupMember removes a member from a group.
// DELETE /admin/groups/:group_id/members/:email/
func (h *AdminHandler) AdminRemoveGroupMember(c *gin.Context) {
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

	// Resolve user by email
	memberID, _, err := h.lookupUserByEmail(email)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	if err := deleteGroupMember(h.db, orgID, groupID, memberID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to remove group member"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// AdminListGroupLibraries lists libraries shared with a group.
// GET /admin/groups/:group_id/libraries/
func (h *AdminHandler) AdminListGroupLibraries(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")

	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	groupID := c.Param("group_id")

	// Resolve the group's own org_id — callerOrgID may differ (e.g. superadmin
	// looking at a group that belongs to a tenant org).
	var orgID, groupName string
	groupIter := h.db.Session().Query(`
		SELECT org_id, name FROM groups_by_id WHERE group_id = ?
	`, groupID).Iter()
	found := groupIter.Scan(&orgID, &groupName)
	groupIter.Close()
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "group not found"})
		return
	}

	type adminGroupLib struct {
		RepoID       string `json:"repo_id"`
		Name         string `json:"name"`
		Size         int64  `json:"size"`
		SharedBy     string `json:"shared_by"`
		SharedByName string `json:"shared_by_name"`
		Encrypted    bool   `json:"encrypted"`
		Permission   string `json:"permission"`
	}

	var libraries []adminGroupLib
	iter := h.db.Session().Query(`
		SELECT created_at, library_id, share_id, permission, shared_by, shared_by_email, shared_by_name, repo_name, encrypted, size_bytes
		FROM shares_by_group WHERE org_id = ? AND group_id = ?
	`, orgID, groupID).Iter()
	var createdAt time.Time
	var libID, shareID, perm, sharedBy, sharedByEmail, sharedByName, libName string
	var encrypted bool
	var sizeBytes int64

	for iter.Scan(&createdAt, &libID, &shareID, &perm, &sharedBy, &sharedByEmail, &sharedByName, &libName, &encrypted, &sizeBytes) {

		apiPerm := "rw"
		if perm == "r" {
			apiPerm = "r"
		}

		libraries = append(libraries, adminGroupLib{
			RepoID:       libID,
			Name:         libName,
			Size:         sizeBytes,
			SharedBy:     sharedByEmail,
			SharedByName: sharedByName,
			Encrypted:    encrypted,
			Permission:   apiPerm,
		})
	}
	if err := iter.Close(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list group libraries"})
		return
	}
	sort.SliceStable(libraries, func(i, j int) bool {
		return libraries[i].Name < libraries[j].Name
	})

	if libraries == nil {
		libraries = []adminGroupLib{}
	}

	c.JSON(http.StatusOK, gin.H{
		"libraries":  libraries,
		"group_name": groupName,
	})

}
