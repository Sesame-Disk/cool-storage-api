package v2

import (
	"net/http"
	"strings"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Departments / address-book
// ============================================================================

// listDepartmentGroups returns groups where is_department=true for the given org.
func (h *OrgAdminHandler) listDepartmentGroups(orgID string) []gin.H {
	records := listDepartmentGroupRecords(h.db.Session(), orgID)
	return departmentGroupListData(records, func(groupID string) int64 {
		return int64(h.getOrgSettingInt(orgID, "group_quota_"+groupID, -2))
	})
}

// ListOrgDepartments lists department groups in the org.
// GET /org/:org_id/admin/departments/
func (h *OrgAdminHandler) ListOrgDepartments(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": h.listDepartmentGroups(targetOrgID)})
}

// ListOrgAddressBookGroups lists address-book (department) groups.
// GET /org/:org_id/admin/address-book/groups/
func (h *OrgAdminHandler) ListOrgAddressBookGroups(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": h.listDepartmentGroups(targetOrgID)})
}

// AddOrgAddressBookGroup creates a new department group.
// POST /org/:org_id/admin/address-book/groups/
func (h *OrgAdminHandler) AddOrgAddressBookGroup(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
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
	// "-1" is the seafile-js sentinel value meaning "no parent" (root department)
	if parentGroup == "-1" {
		parentGroup = ""
	}
	groupOwner := req.GroupOwner
	groupStaff := req.GroupStaff

	callerUserID := c.GetString("user_id")
	newGroupID := uuid.New().String()
	now := time.Now()

	creatorID := callerUserID
	if groupOwner != "" {
		if ownerID, err := h.lookupOrgUserByEmail(targetOrgID, groupOwner); err == nil {
			creatorID = ownerID
		}
	}

	batch := h.db.Session().Batch(gocql.LoggedBatch)
	if parentGroup != "" {
		batch.Query(`
			INSERT INTO groups (org_id, group_id, name, creator_id, parent_group_id, is_department, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, targetOrgID, newGroupID, groupName, creatorID, parentGroup, true, now, now)
	} else {
		batch.Query(`
			INSERT INTO groups (org_id, group_id, name, creator_id, is_department, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`, targetOrgID, newGroupID, groupName, creatorID, true, now, now)
	}
	batch.Query(`
		INSERT INTO groups_by_id (group_id, org_id, name) VALUES (?, ?, ?)
	`, newGroupID, targetOrgID, groupName)
	batch.Query(`
		INSERT INTO group_members (group_id, user_id, role, added_at)
		VALUES (?, ?, ?, ?)
	`, newGroupID, creatorID, "owner", now)
	batch.Query(`
		INSERT INTO groups_by_member (org_id, user_id, group_id, group_name, role, added_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, targetOrgID, creatorID, newGroupID, groupName, "owner", now)
	if err := batch.Exec(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create department"})
		return
	}
	if err := syncAdminGroupReadModel(h.db, targetOrgID, newGroupID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to sync group read model"})
		return
	}

	// Add staff members if specified
	if groupStaff != "" {
		for _, staffEmail := range strings.Split(groupStaff, ",") {
			staffEmail = strings.TrimSpace(staffEmail)
			if staffEmail == "" {
				continue
			}
			staffID, err := h.lookupOrgUserByEmail(targetOrgID, staffEmail)
			if err != nil {
				continue
			}
			if err := upsertGroupMember(h.db, targetOrgID, newGroupID, staffID, groupName, "member", now); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to add department staff"})
				return
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"id":              newGroupID,
		"name":            groupName,
		"parent_group_id": parentGroup,
		"created_at":      now.Format(time.RFC3339),
		"quota":           -2,
	})
}

// GetOrgAddressBookGroup returns details for a single address-book group.
// GET /org/:org_id/admin/address-book/groups/:gid/?return_ancestors=true
func (h *OrgAdminHandler) GetOrgAddressBookGroup(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}

	groupID := c.Param("gid")

	var name, creatorID, parentGroupID string
	var isDept bool
	var createdAt time.Time
	if err := h.db.Session().Query(`
		SELECT name, creator_id, parent_group_id, is_department, created_at
		FROM groups WHERE org_id = ? AND group_id = ?
	`, targetOrgID, groupID).Scan(&name, &creatorID, &parentGroupID, &isDept, &createdAt); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "group not found"})
		return
	}

	quota := h.getOrgSettingInt(targetOrgID, "group_quota_"+groupID, -2)

	// Resolve members
	memIter := h.db.Session().Query(`
		SELECT user_id, role FROM group_members WHERE group_id = ?
	`, groupID).Iter()
	usersMap := h.resolveUsersMap(targetOrgID)
	var members []gin.H
	var memUserID, memRole string
	for memIter.Scan(&memUserID, &memRole) {
		u := usersMap[memUserID]
		displayRole := memRole
		if len(memRole) > 0 {
			displayRole = strings.ToUpper(memRole[:1]) + memRole[1:]
		}
		members = append(members, gin.H{
			"email":      u.Email,
			"name":       u.Name,
			"role":       displayRole,
			"avatar_url": "/static/img/default-avatar.png",
		})
	}
	memIter.Close()
	if members == nil {
		members = []gin.H{}
	}

	// Resolve sub-departments
	subIter := h.db.Session().Query(`
		SELECT group_id, name, parent_group_id, is_department, created_at
		FROM groups WHERE org_id = ?
	`, targetOrgID).Iter()
	var subGroups []gin.H
	var subGID, subName, subParent string
	var subIsDept bool
	var subCreatedAt time.Time
	for subIter.Scan(&subGID, &subName, &subParent, &subIsDept, &subCreatedAt) {
		if !subIsDept || subParent != groupID {
			continue
		}
		subQuota := h.getOrgSettingInt(targetOrgID, "group_quota_"+subGID, -2)
		subGroups = append(subGroups, gin.H{
			"id":              subGID,
			"name":            subName,
			"parent_group_id": subParent,
			"created_at":      subCreatedAt.Format(time.RFC3339),
			"quota":           subQuota,
		})
	}
	subIter.Close()
	if subGroups == nil {
		subGroups = []gin.H{}
	}

	// Resolve ancestors
	var ancestors []gin.H
	if c.Query("return_ancestors") == "true" {
		currentParent := parentGroupID
		for currentParent != "" {
			var pName, pParent string
			if err := h.db.Session().Query(`
				SELECT name, parent_group_id FROM groups WHERE org_id = ? AND group_id = ?
			`, targetOrgID, currentParent).Scan(&pName, &pParent); err != nil {
				break
			}
			ancestors = append(ancestors, gin.H{
				"id":              currentParent,
				"name":            pName,
				"parent_group_id": pParent,
			})
			currentParent = pParent
		}
	}
	if ancestors == nil {
		ancestors = []gin.H{}
	}

	c.JSON(http.StatusOK, gin.H{
		"id":              groupID,
		"name":            name,
		"parent_group_id": parentGroupID,
		"created_at":      createdAt.Format(time.RFC3339),
		"quota":           quota,
		"members":         members,
		"groups":          subGroups,
		"ancestor_groups": ancestors,
	})
}

// UpdateOrgAddressBookGroup updates a department group's name.
// PUT /org/:org_id/admin/address-book/groups/:gid/
func (h *OrgAdminHandler) UpdateOrgAddressBookGroup(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}

	groupID := c.Param("gid")
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

	// Verify group exists
	if err := h.db.Session().Query(`
		SELECT name FROM groups WHERE org_id = ? AND group_id = ?
	`, targetOrgID, groupID).Scan(new(string)); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "group not found"})
		return
	}

	if err := renameGroup(h.db, targetOrgID, groupID, newName, time.Now()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to rename group"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"id": groupID, "name": newName})
}

// DeleteOrgAddressBookGroup deletes a department group and its members.
// DELETE /org/:org_id/admin/address-book/groups/:gid/
func (h *OrgAdminHandler) DeleteOrgAddressBookGroup(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}

	groupID := c.Param("gid")
	groupRow, _ := readAdminGroupReadModelRow(h.db, targetOrgID, groupID)

	// Verify group exists
	if err := h.db.Session().Query(`
		SELECT name FROM groups WHERE org_id = ? AND group_id = ?
	`, targetOrgID, groupID).Scan(new(string)); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "group not found"})
		return
	}

	// Delete group members from lookup table
	memIter := h.db.Session().Query(`
		SELECT user_id FROM group_members WHERE group_id = ?
	`, groupID).Iter()
	var memberID string
	var memberIDs []string
	for memIter.Scan(&memberID) {
		memberIDs = append(memberIDs, memberID)
	}
	if err := memIter.Close(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load group members"})
		return
	}

	batch := h.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`DELETE FROM group_members WHERE group_id = ?`, groupID)
	batch.Query(`DELETE FROM groups WHERE org_id = ? AND group_id = ?`, targetOrgID, groupID)
	batch.Query(`DELETE FROM groups_by_id WHERE group_id = ?`, groupID)
	if err := batch.Exec(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete department"})
		return
	}
	if groupRow.GroupID != "" {
		if err := deleteAdminGroupReadModel(h.db, groupRow); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to clean group read model"})
			return
		}
	}
	if err := cleanupGroupsByMember(h.db, targetOrgID, groupID, memberIDs); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to clean department membership index"})
		return
	}
	groupUUID, err := uuid.Parse(groupID)
	if err == nil {
		if err := cleanupGroupShares(h.db, groupUUID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to clean department shares"})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ============================================================================
