package v2

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"log"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// DepartmentHandler handles department-related API requests.
// Departments are hierarchical groups managed by org admins.
type DepartmentHandler struct {
	db   *db.DB
	perm *middleware.PermissionMiddleware
}

// NewDepartmentHandler creates a new DepartmentHandler
func NewDepartmentHandler(database *db.DB, perm *middleware.PermissionMiddleware) *DepartmentHandler {
	return &DepartmentHandler{db: database, perm: perm}
}

// DepartmentResponse represents a department in API response
type DepartmentResponse struct {
	ID             string                `json:"id"`
	Name           string                `json:"name"`
	CreatedAt      string                `json:"created_at"`
	ParentGroupID  string                `json:"parent_group_id,omitempty"`
	MemberCount    int                   `json:"member_count,omitempty"`
	Groups         []DepartmentResponse  `json:"groups"` // Sub-departments
	Members        []GroupMemberResponse `json:"members"`
	AncestorGroups []DepartmentRef       `json:"ancestor_groups"` // Breadcrumb
}

// DepartmentRef is a lightweight reference used in ancestor chains
type DepartmentRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// CreateDepartmentRequest represents the request for creating a department
type CreateDepartmentRequest struct {
	Name          string `json:"name" form:"name"`
	GroupName     string `json:"group_name" form:"group_name"`     // Seafile-js compat alias for Name
	ParentGroupID string `json:"parent_group" form:"parent_group"` // Optional parent department UUID
}

// RegisterDepartmentRoutes registers department routes under admin paths.
// Handles both /admin/address-book/groups/ (sys-admin) and /departments/ (user-facing).
func RegisterDepartmentRoutes(rg *gin.RouterGroup, database *db.DB, perm *middleware.PermissionMiddleware) {
	h := NewDepartmentHandler(database, perm)

	// Admin endpoint: /api/v2.1/admin/address-book/groups/
	// Used by sys-admin department management page
	admin := rg.Group("/admin/address-book/groups")
	{
		admin.GET("", h.ListDepartments)
		admin.GET("/", h.ListDepartments)
		admin.POST("", h.CreateDepartment)
		admin.POST("/", h.CreateDepartment)
		admin.GET("/:group_id", h.GetDepartment)
		admin.GET("/:group_id/", h.GetDepartment)
		admin.PUT("/:group_id", h.UpdateDepartment)
		admin.PUT("/:group_id/", h.UpdateDepartment)
		admin.DELETE("/:group_id", h.DeleteDepartment)
		admin.DELETE("/:group_id/", h.DeleteDepartment)
	}

	// User-facing: /api/v2.1/departments/ — list departments user belongs to
	departments := rg.Group("/departments")
	{
		departments.GET("", h.ListUserDepartments)
		departments.GET("/", h.ListUserDepartments)
	}
}

// ListDepartments returns all departments in the org (admin only).
// GET /api/v2.1/admin/address-book/groups/
func (h *DepartmentHandler) ListDepartments(c *gin.Context) {
	orgID := c.GetString("org_id")

	if h.db == nil {
		c.JSON(http.StatusOK, gin.H{"data": []DepartmentResponse{}})
		return
	}

	if _, err := uuid.Parse(orgID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid org_id"})
		return
	}

	departments := h.listDepartmentsForOrg(orgID)
	c.JSON(http.StatusOK, gin.H{"data": departments})
}

// ListUserDepartments returns departments the user belongs to.
// GET /api/v2.1/departments/
func (h *DepartmentHandler) ListUserDepartments(c *gin.Context) {
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	if h.db == nil {
		c.JSON(http.StatusOK, []DepartmentResponse{})
		return
	}

	// Get groups user is a member of, filter to departments only
	iter := h.db.Session().Query(`
		SELECT group_id, group_name FROM groups_by_member
		WHERE org_id = ? AND user_id = ?
	`, orgID, userID).Iter()

	var departments []DepartmentResponse
	var groupID, groupName string

	for iter.Scan(&groupID, &groupName) {
		// Check if this group is a department
		var isDept bool
		h.db.Session().Query(`
			SELECT is_department FROM groups WHERE org_id = ? AND group_id = ?
		`, orgID, groupID).Scan(&isDept)

		if isDept {
			var memberCount int
			h.db.Session().Query(`SELECT COUNT(*) FROM group_members WHERE group_id = ?`, groupID).Scan(&memberCount)

			departments = append(departments, DepartmentResponse{
				ID:          groupID,
				Name:        groupName,
				MemberCount: memberCount,
			})
		}
	}
	iter.Close()

	if departments == nil {
		departments = []DepartmentResponse{}
	}
	c.JSON(http.StatusOK, departments)
}

// CreateDepartment creates a new department (admin only).
// POST /api/v2.1/admin/address-book/groups/
func (h *DepartmentHandler) CreateDepartment(c *gin.Context) {
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	var req CreateDepartmentRequest
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// Accept both 'name' (API) and 'group_name' (seafile-js compat)
	if req.Name == "" && req.GroupName != "" {
		req.Name = req.GroupName
	}
	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}

	// Validate parent if specified
	// "-1" is the seafile-js sentinel value meaning "no parent" (root department)
	var parentGroupID string
	if req.ParentGroupID != "" && req.ParentGroupID != "-1" {
		if _, err := uuid.Parse(req.ParentGroupID); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid parent_group id"})
			return
		}
		// Verify parent exists
		var parentName string
		if err := h.db.Session().Query(`
			SELECT name FROM groups WHERE org_id = ? AND group_id = ?
		`, orgID, req.ParentGroupID).Scan(&parentName); err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "parent department not found"})
			return
		}
		parentGroupID = req.ParentGroupID
	}
	if h.db == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database not available"})
		return
	}

	groupUUID := uuid.New()
	groupID := groupUUID.String()
	now := time.Now()

	// Atomic batch: create department + lookup + creator membership
	batch := h.db.Session().Batch(gocql.LoggedBatch)
	if parentGroupID != "" {
		batch.Query(`INSERT INTO groups (org_id, group_id, name, creator_id, is_department, parent_group_id, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			orgID, groupID, req.Name, userID, true, parentGroupID, now, now)
	} else {
		batch.Query(`INSERT INTO groups (org_id, group_id, name, creator_id, is_department, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			orgID, groupID, req.Name, userID, true, now, now)
	}
	batch.Query(`INSERT INTO groups_by_id (group_id, org_id, name) VALUES (?, ?, ?)`,
		groupID, orgID, req.Name)
	batch.Query(`INSERT INTO group_members (group_id, user_id, role, added_at) VALUES (?, ?, ?, ?)`,
		groupID, userID, "owner", now)
	batch.Query(`INSERT INTO groups_by_member (org_id, user_id, group_id, group_name, role, added_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		orgID, userID, groupID, req.Name, "owner", now)
	if err := batch.Exec(); err != nil {
		slog.Error("failed to create department", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create department"})
		return
	}

	c.JSON(http.StatusCreated, DepartmentResponse{
		ID:            groupID,
		Name:          req.Name,
		CreatedAt:     now.Format(time.RFC3339),
		ParentGroupID: parentGroupID,
	})
}

// GetDepartment returns a department with members and sub-departments.
// GET /api/v2.1/admin/address-book/groups/:group_id/
func (h *DepartmentHandler) GetDepartment(c *gin.Context) {
	orgID := c.GetString("org_id")
	groupID := c.Param("group_id")
	returnAncestors := c.Query("return_ancestors") == "true"

	if _, err := uuid.Parse(groupID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid group_id"})
		return
	}
	if h.db == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database not available"})
		return
	}

	// Get department info
	var name string
	var parentGroupID *string
	var createdAt time.Time
	if err := h.db.Session().Query(`
		SELECT name, parent_group_id, created_at FROM groups WHERE org_id = ? AND group_id = ?
	`, orgID, groupID).Scan(&name, &parentGroupID, &createdAt); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "department not found"})
		return
	}

	// Get members
	members := h.getGroupMembers(orgID, groupID)

	// Get sub-departments (children where parent_group_id = this group)
	subDepts := h.getSubDepartments(orgID, groupID)

	// Build ancestor chain if requested
	ancestors := []DepartmentRef{}
	if returnAncestors && parentGroupID != nil {
		ancestors = h.getAncestors(orgID, *parentGroupID)
	}

	parentStr := ""
	if parentGroupID != nil {
		parentStr = *parentGroupID
	}

	resp := DepartmentResponse{
		ID:             groupID,
		Name:           name,
		CreatedAt:      createdAt.Format(time.RFC3339),
		ParentGroupID:  parentStr,
		MemberCount:    len(members),
		Members:        members,
		Groups:         subDepts,
		AncestorGroups: ancestors,
	}

	c.JSON(http.StatusOK, resp)
}

// UpdateDepartment renames a department (admin only).
// PUT /api/v2.1/admin/address-book/groups/:group_id/
func (h *DepartmentHandler) UpdateDepartment(c *gin.Context) {
	orgID := c.GetString("org_id")
	groupID := c.Param("group_id")

	if _, err := uuid.Parse(groupID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid group_id"})
		return
	}

	var req struct {
		Name string `json:"name" form:"name"`
	}
	if err := c.ShouldBind(&req); err != nil || req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}
	if h.db == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database not available"})
		return
	}

	if err := renameGroup(h.db, orgID, groupID, req.Name, time.Now()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update department"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"id": groupID, "name": req.Name})
}

// DeleteDepartment deletes a department, removes all members, and cleans up shares (admin only).
// DELETE /api/v2.1/admin/address-book/groups/:group_id/
func (h *DepartmentHandler) DeleteDepartment(c *gin.Context) {
	orgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	groupID := c.Param("group_id")

	if _, err := uuid.Parse(groupID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid group_id"})
		return
	}
	if h.db == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database not available"})
		return
	}

	// Check for sub-departments — don't allow deleting if children exist
	subDepts := h.getSubDepartments(orgID, groupID)
	if len(subDepts) > 0 {
		subNames := make([]string, len(subDepts))
		for i, sd := range subDepts {
			subNames[i] = sd.ID + ":" + sd.Name
		}
		slog.Warn("cannot delete department: has sub-departments",
			"group_id", groupID, "sub_departments", subNames)
		c.JSON(http.StatusConflict, gin.H{"error": "cannot delete department with sub-departments; delete children first"})
		return
	}

	// Get department name for audit log
	var deptName string
	h.db.Session().Query(`SELECT name FROM groups WHERE org_id = ? AND group_id = ?`,
		orgID, groupID).Scan(&deptName)

	// Collect members before deletion (for groups_by_member cleanup)
	memIter := h.db.Session().Query(`SELECT user_id FROM group_members WHERE group_id = ?`, groupID).Iter()
	var memberID string
	var memberIDs []string
	for memIter.Scan(&memberID) {
		memberIDs = append(memberIDs, memberID)
	}
	memIter.Close()

	// Atomic batch: delete group + groups_by_id + group_members
	batch := h.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`DELETE FROM groups WHERE org_id = ? AND group_id = ?`, orgID, groupID)
	batch.Query(`DELETE FROM groups_by_id WHERE group_id = ?`, groupID)
	batch.Query(`DELETE FROM group_members WHERE group_id = ?`, groupID)
	if err := batch.Exec(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete department"})
		return
	}

	if err := cleanupGroupsByMember(h.db, orgID, groupID, memberIDs); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to clean department membership index"})
		return
	}

	groupUUID, _ := uuid.Parse(groupID)
	if err := cleanupGroupShares(h.db, groupUUID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to clean department shares"})
		return
	}

	// Audit log
	orgUUID, _ := uuid.Parse(orgID)
	auditDetails, _ := json.Marshal(map[string]interface{}{
		"name":          deptName,
		"members_count": len(memberIDs),
	})
	h.db.Session().Query(`
		INSERT INTO audit_log (org_id, timestamp, action, target_type, target_id, actor_id, details)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, orgUUID.String(), time.Now(), "delete_department", "department", groupID, callerUserID,
		string(auditDetails)).Exec()

	log.Printf("[Department] Deleted department %s (%s) with %d members", groupID, deptName, len(memberIDs))
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// --- Helper methods ---
// All helpers use string UUIDs for gocql compatibility (apache/cassandra-gocql-driver/v2
// can't marshal google/uuid.UUID directly — it accepts string, [16]byte, or gocql.UUID)

// listDepartmentsForOrg returns all departments in an org
func (h *DepartmentHandler) listDepartmentsForOrg(orgID string) []DepartmentResponse {
	iter := h.db.Session().Query(`
		SELECT group_id, name, parent_group_id, created_at FROM groups WHERE org_id = ?
	`, orgID).Iter()

	var departments []DepartmentResponse
	var groupID, name string
	var parentGroupID *string
	var createdAt time.Time

	for iter.Scan(&groupID, &name, &parentGroupID, &createdAt) {
		// Only include departments (is_department=true check)
		var isDept bool
		h.db.Session().Query(`SELECT is_department FROM groups WHERE org_id = ? AND group_id = ?`,
			orgID, groupID).Scan(&isDept)
		if !isDept {
			continue
		}

		var memberCount int
		h.db.Session().Query(`SELECT COUNT(*) FROM group_members WHERE group_id = ?`, groupID).Scan(&memberCount)

		parentStr := ""
		if parentGroupID != nil {
			parentStr = *parentGroupID
		}

		departments = append(departments, DepartmentResponse{
			ID:            groupID,
			Name:          name,
			CreatedAt:     createdAt.Format(time.RFC3339),
			ParentGroupID: parentStr,
			MemberCount:   memberCount,
		})
	}
	iter.Close()

	if departments == nil {
		departments = []DepartmentResponse{}
	}
	return departments
}

// getSubDepartments returns direct child departments of a given group
func (h *DepartmentHandler) getSubDepartments(orgID, parentID string) []DepartmentResponse {
	iter := h.db.Session().Query(`
		SELECT group_id, name, parent_group_id, created_at FROM groups WHERE org_id = ?
	`, orgID).Iter()

	var children []DepartmentResponse
	var groupID, name string
	var pgID *string
	var createdAt time.Time

	for iter.Scan(&groupID, &name, &pgID, &createdAt) {
		if pgID != nil && *pgID == parentID && name != "" {
			// Verify it's still a department (not deleted/tombstone)
			var isDept bool
			var checkName string
			if err := h.db.Session().Query(`SELECT name, is_department FROM groups WHERE org_id = ? AND group_id = ?`,
				orgID, groupID).Scan(&checkName, &isDept); err != nil || !isDept {
				slog.Debug("getSubDepartments: skipping group (not found or not department)",
					"group_id", groupID, "name", name, "err", err, "is_dept", isDept)
				continue
			}
			slog.Debug("getSubDepartments: found child",
				"group_id", groupID, "name", checkName, "is_dept", isDept)
			var memberCount int
			h.db.Session().Query(`SELECT COUNT(*) FROM group_members WHERE group_id = ?`, groupID).Scan(&memberCount)

			children = append(children, DepartmentResponse{
				ID:            groupID,
				Name:          name,
				CreatedAt:     createdAt.Format(time.RFC3339),
				ParentGroupID: parentID,
				MemberCount:   memberCount,
			})
		}
	}
	iter.Close()

	if children == nil {
		children = []DepartmentResponse{}
	}
	return children
}

// getGroupMembers returns all members of a group
func (h *DepartmentHandler) getGroupMembers(orgID, groupID string) []GroupMemberResponse {
	iter := h.db.Session().Query(`
		SELECT user_id, role, added_at FROM group_members WHERE group_id = ?
	`, groupID).Iter()

	var members []GroupMemberResponse
	var userID, role string
	var addedAt time.Time

	for iter.Scan(&userID, &role, &addedAt) {
		var email, name string
		h.db.Session().Query(`SELECT email, name FROM users WHERE org_id = ? AND user_id = ?`,
			orgID, userID).Scan(&email, &name)
		if email == "" {
			email = userID
		}
		if name == "" {
			name = email
		}

		members = append(members, GroupMemberResponse{
			Email:     email,
			Name:      name,
			UserID:    userID,
			Role:      role,
			AddedAt:   addedAt.Format(time.RFC3339),
			AvatarURL: "",
		})
	}
	iter.Close()

	if members == nil {
		members = []GroupMemberResponse{}
	}
	return members
}

// getAncestors walks up the parent chain to build ancestor breadcrumbs
func (h *DepartmentHandler) getAncestors(orgID string, parentID string) []DepartmentRef {
	var ancestors []DepartmentRef
	current := parentID

	// Walk up max 10 levels to prevent infinite loops
	for i := 0; i < 10; i++ {
		var name string
		var nextParent *string
		if err := h.db.Session().Query(`
			SELECT name, parent_group_id FROM groups WHERE org_id = ? AND group_id = ?
		`, orgID, current).Scan(&name, &nextParent); err != nil {
			break
		}
		// Prepend (ancestors go from root to immediate parent)
		ancestors = append([]DepartmentRef{{ID: current, Name: name}}, ancestors...)
		if nextParent == nil {
			break
		}
		current = *nextParent
	}

	if ancestors == nil {
		ancestors = []DepartmentRef{}
	}
	return ancestors
}
