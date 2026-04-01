package v2

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// GroupHandler handles group-related API requests
type GroupHandler struct {
	db     *db.DB
	config *config.Config
}

// NewGroupHandler creates a new GroupHandler
func NewGroupHandler(database *db.DB, cfg *config.Config) *GroupHandler {
	return &GroupHandler{db: database, config: cfg}
}

// RegisterGroupRoutes registers group routes
func RegisterGroupRoutes(rg *gin.RouterGroup, database *db.DB, cfg *config.Config) *GroupHandler {
	h := NewGroupHandler(database, cfg)

	groups := rg.Group("/groups")
	{
		groups.GET("", h.ListGroups)
		groups.GET("/", h.ListGroups)
		groups.POST("", h.CreateGroup)
		groups.POST("/", h.CreateGroup)
		groups.GET("/:group_id", h.GetGroup)
		groups.GET("/:group_id/", h.GetGroup)
		groups.PUT("/:group_id", h.UpdateGroup)
		groups.PUT("/:group_id/", h.UpdateGroup)
		groups.DELETE("/:group_id", h.DeleteGroup)
		groups.DELETE("/:group_id/", h.DeleteGroup)

		// Group libraries
		groups.GET("/:group_id/libraries", h.ListGroupLibraries)
		groups.GET("/:group_id/libraries/", h.ListGroupLibraries)

		// Group-owned libraries
		groups.POST("/:group_id/group-owned-libraries", h.CreateGroupOwnedLibrary)
		groups.POST("/:group_id/group-owned-libraries/", h.CreateGroupOwnedLibrary)
		groups.DELETE("/:group_id/group-owned-libraries/:library_id", h.DeleteGroupOwnedLibrary)
		groups.DELETE("/:group_id/group-owned-libraries/:library_id/", h.DeleteGroupOwnedLibrary)

		// Group members
		groups.GET("/:group_id/members", h.ListGroupMembers)
		groups.GET("/:group_id/members/", h.ListGroupMembers)
		groups.POST("/:group_id/members", h.AddGroupMember)
		groups.POST("/:group_id/members/", h.AddGroupMember)
		groups.POST("/:group_id/members/bulk", h.BulkAddGroupMembers)
		groups.POST("/:group_id/members/bulk/", h.BulkAddGroupMembers)
		groups.POST("/:group_id/members/import", h.ImportGroupMembersViaFile)
		groups.POST("/:group_id/members/import/", h.ImportGroupMembersViaFile)
		groups.DELETE("/:group_id/members/:user_email", h.RemoveGroupMember)
		groups.DELETE("/:group_id/members/:user_email/", h.RemoveGroupMember)
		groups.PUT("/:group_id/members/:user_email", h.SetGroupAdmin)
		groups.PUT("/:group_id/members/:user_email/", h.SetGroupAdmin)
	}

	return h
}

// RegisterShareableGroupRoutes registers the shareable-groups endpoint.
// Returns all groups the user can share with (same as their groups).
// GET /api/v2.1/shareable-groups/
func RegisterShareableGroupRoutes(rg *gin.RouterGroup, database *db.DB, cfg *config.Config) {
	h := NewGroupHandler(database, cfg)
	rg.GET("/shareable-groups", h.ListShareableGroups)
	rg.GET("/shareable-groups/", h.ListShareableGroups)
}

// GroupRepoResponse represents a library shared with a group.
type GroupRepoResponse struct {
	RepoID               string `json:"repo_id"`
	RepoName             string `json:"repo_name"`
	Permission           string `json:"permission"`
	Size                 int64  `json:"size"`
	OwnerEmail           string `json:"owner_email"`
	OwnerContactEmail    string `json:"owner_contact_email"`
	OwnerName            string `json:"owner_name"`
	Encrypted            bool   `json:"encrypted"`
	LastModified         int64  `json:"last_modified"`
	ModifierEmail        string `json:"modifier_email"`
	ModifierContactEmail string `json:"modifier_contact_email"`
	ModifierName         string `json:"modifier_name"`
	Type                 string `json:"type"`
	Starred              bool   `json:"starred"`
	Monitored            bool   `json:"monitored"`
	StorageName          string `json:"storage_name"`
}

// GroupResponse represents a group in API response
type GroupResponse struct {
	GroupID       string              `json:"id"`
	GroupName     string              `json:"name"`
	Owner         string              `json:"owner"`
	CreatorID     string              `json:"creator_id"`
	CreatedAt     string              `json:"created_at"`
	ParentGroupID int                 `json:"parent_group_id"`
	Admins        []string            `json:"admins"`
	MemberCount   int                 `json:"member_count,omitempty"`
	Repos         []GroupRepoResponse `json:"repos"`
}

// GroupMemberResponse represents a group member in API response
type GroupMemberResponse struct {
	Email     string `json:"email"`
	Name      string `json:"name"`
	UserID    string `json:"user_id"`
	Role      string `json:"role"` // "Owner", "Admin", "Member"
	AddedAt   string `json:"added_at"`
	AvatarURL string `json:"avatar_url"`
}

// capitalizeRole converts a lowercase role ("owner", "admin", "member") to the
// Title-cased form expected by the frontend ("Owner", "Admin", "Member").
func capitalizeRole(role string) string {
	if role == "" {
		return role
	}
	return strings.ToUpper(role[:1]) + role[1:]
}

// getGroupAdminEmails returns the emails of all members with role "owner" or "admin".
func (h *GroupHandler) getGroupAdminEmails(orgID, groupID string) []string {
	iter := h.db.Session().Query(`
		SELECT user_id, role FROM group_members WHERE group_id = ?
	`, groupID).Iter()

	var emails []string
	var memberUserID, memberRole string
	for iter.Scan(&memberUserID, &memberRole) {
		if memberRole == "owner" || memberRole == "admin" {
			var email string
			h.db.Session().Query(`SELECT email FROM users WHERE org_id = ? AND user_id = ?`,
				orgID, memberUserID).Scan(&email)
			if email != "" {
				emails = append(emails, email)
			}
		}
	}
	iter.Close()
	if emails == nil {
		emails = []string{}
	}
	return emails
}

// CreateGroupRequest represents the request for creating a group
type CreateGroupRequest struct {
	GroupName string `json:"name" form:"name" binding:"required"`
}

// UpdateGroupRequest represents the request for updating a group
type UpdateGroupRequest struct {
	GroupName string `json:"name" form:"name"`
	Owner     string `json:"owner" form:"owner"`
}

// AddMemberRequest represents the request for adding a member to a group
type AddMemberRequest struct {
	Email string `json:"email" form:"email" binding:"required"`
	Role  string `json:"role" form:"role"` // "admin" or "member"
}

// getGroupRepos returns repos shared with a specific group.
func (h *GroupHandler) getGroupRepos(orgID, groupID string) []GroupRepoResponse {
	libIter := h.db.Session().Query(`
		SELECT library_id, owner_id, name, encrypted, size_bytes, created_at, updated_at, storage_class, deleted_at
		FROM libraries WHERE org_id = ?
	`, orgID).Iter()

	var repos []GroupRepoResponse
	var libID, ownerID, libName, storageClass string
	var encrypted bool
	var sizeBytes int64
	var createdAt, updatedAt, deletedAt time.Time

	for libIter.Scan(&libID, &ownerID, &libName, &encrypted, &sizeBytes, &createdAt, &updatedAt, &storageClass, &deletedAt) {
		if !deletedAt.IsZero() {
			continue
		}
		var perm string
		if err := h.db.Session().Query(`
			SELECT permission FROM shares
			WHERE library_id = ? AND shared_to = ? ALLOW FILTERING
		`, libID, groupID).Scan(&perm); err != nil {
			continue
		}

		var ownerEmail, ownerName string
		h.db.Session().Query(`SELECT email, name FROM users WHERE org_id = ? AND user_id = ?`, orgID, ownerID).Scan(&ownerEmail, &ownerName)
		if ownerEmail == "" {
			ownerEmail = ownerID
		}
		if ownerName == "" {
			ownerName = strings.Split(ownerEmail, "@")[0]
		}

		apiPerm := "rw"
		if perm == "r" {
			apiPerm = "r"
		}

		mtime := updatedAt
		if mtime.IsZero() {
			mtime = createdAt
		}

		repos = append(repos, GroupRepoResponse{
			RepoID:               libID,
			RepoName:             libName,
			Permission:           apiPerm,
			Size:                 sizeBytes,
			OwnerEmail:           ownerEmail,
			OwnerContactEmail:    ownerEmail,
			OwnerName:            ownerName,
			Encrypted:            encrypted,
			LastModified:         mtime.UnixMilli(),
			ModifierEmail:        ownerEmail,
			ModifierContactEmail: ownerEmail,
			ModifierName:         ownerName,
			Type:                 "repo",
			StorageName:          storageClass,
		})
	}
	libIter.Close()

	if repos == nil {
		repos = []GroupRepoResponse{}
	}
	return repos
}

// ListGroups returns list of groups for the authenticated user
// GET /api/v2.1/groups/
func (h *GroupHandler) ListGroups(c *gin.Context) {
	userID := c.GetString("user_id")
	orgID := c.GetString("org_id")
	withRepos := c.Query("with_repos") == "1"

	// Check if database is available
	if h.db == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database not available"})
		return
	}

	userUUID, err := uuid.Parse(userID)
	if err != nil {
		slog.Warn("ListGroups: invalid user_id", "user_id", userID, "error", err)
		c.JSON(http.StatusOK, []GroupResponse{})
		return
	}
	orgUUID, err := uuid.Parse(orgID)
	if err != nil {
		slog.Warn("ListGroups: invalid org_id", "org_id", orgID, "error", err)
		c.JSON(http.StatusOK, []GroupResponse{})
		return
	}

	// Query groups this user is a member of using lookup table
	// Use .String() for UUID params - gocql can't marshal google/uuid.UUID directly
	iter := h.db.Session().Query(`
		SELECT group_id, group_name, role, added_at
		FROM groups_by_member
		WHERE org_id = ? AND user_id = ?
	`, orgUUID.String(), userUUID.String()).Iter()

	var groups []GroupResponse
	var groupID, groupName, role string
	var addedAt time.Time

	for iter.Scan(&groupID, &groupName, &role, &addedAt) {
		// Get group details including creator
		var creatorID string
		var createdAt time.Time
		if err := h.db.Session().Query(`
			SELECT creator_id, created_at FROM groups WHERE org_id = ? AND group_id = ?
		`, orgUUID.String(), groupID).Scan(&creatorID, &createdAt); err != nil {
			slog.Warn("ListGroups: failed to get group details", "group_id", groupID, "error", err)
			continue
		}

		// Get creator email
		var creatorEmail string
		if err := h.db.Session().Query(`SELECT email FROM users WHERE org_id = ? AND user_id = ?`, orgUUID.String(), creatorID).Scan(&creatorEmail); err != nil {
			slog.Warn("ListGroups: failed to get creator email", "creator_id", creatorID, "error", err)
		}

		// Count members
		var memberCount int
		if err := h.db.Session().Query(`SELECT COUNT(*) FROM group_members WHERE group_id = ?`, groupID).Scan(&memberCount); err != nil {
			slog.Warn("ListGroups: failed to count members", "group_id", groupID, "error", err)
		}

		repos := []GroupRepoResponse{}
		if withRepos {
			repos = h.getGroupRepos(orgUUID.String(), groupID)
		}

		groups = append(groups, GroupResponse{
			GroupID:       groupID,
			GroupName:     groupName,
			Owner:         creatorEmail,
			CreatorID:     creatorID,
			CreatedAt:     createdAt.Format(time.RFC3339),
			ParentGroupID: 0,
			Admins:        h.getGroupAdminEmails(orgUUID.String(), groupID),
			MemberCount:   memberCount,
			Repos:         repos,
		})
	}

	if err := iter.Close(); err != nil {
		slog.Error("ListGroups: failed to close iterator", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list groups"})
		return
	}

	if groups == nil {
		groups = []GroupResponse{}
	}

	c.JSON(http.StatusOK, groups)
}

// CreateGroup creates a new group
// POST /api/v2.1/groups/
func (h *GroupHandler) CreateGroup(c *gin.Context) {
	var req CreateGroupRequest
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID := c.GetString("user_id")
	orgID := c.GetString("org_id")

	if req.GroupName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "group_name is required"})
		return
	}

	// ENFORCEMENT CHECK: feature flag
	if h.config != nil {
		enforcement := GetOrgEnforcement(h.db, orgID, h.config)
		if !enforcement.Profile.Features.CanAddGroup {
			c.JSON(http.StatusForbidden, gin.H{
				"error":            "Group creation is not available on your plan",
				"upgrade_required": true,
			})
			return
		}
	}

	// Check if database is available
	if h.db == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database not available"})
		return
	}

	userUUID, _ := uuid.Parse(userID)
	orgUUID, _ := uuid.Parse(orgID)
	groupUUID := uuid.New()
	now := time.Now()

	// Atomic batch: create group + lookup + creator membership
	batch := h.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`INSERT INTO groups (org_id, group_id, name, creator_id, is_department, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		orgUUID.String(), groupUUID.String(), req.GroupName, userUUID.String(), false, now, now)
	batch.Query(`INSERT INTO groups_by_id (group_id, org_id, name) VALUES (?, ?, ?)`,
		groupUUID.String(), orgUUID.String(), req.GroupName)
	batch.Query(`INSERT INTO group_members (group_id, user_id, role, added_at) VALUES (?, ?, ?, ?)`,
		groupUUID.String(), userUUID.String(), "owner", now)
	batch.Query(`INSERT INTO groups_by_member (org_id, user_id, group_id, group_name, role, added_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		orgUUID.String(), userUUID.String(), groupUUID.String(), req.GroupName, "owner", now)
	if err := batch.Exec(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create group"})
		return
	}

	// Get creator email
	var creatorEmail string
	h.db.Session().Query(`SELECT email FROM users WHERE org_id = ? AND user_id = ?`, orgUUID.String(), userID).Scan(&creatorEmail)

	c.JSON(http.StatusCreated, GroupResponse{
		GroupID:       groupUUID.String(),
		GroupName:     req.GroupName,
		Owner:         creatorEmail,
		CreatorID:     userID,
		CreatedAt:     now.Format(time.RFC3339),
		ParentGroupID: 0,
		Admins:        []string{creatorEmail},
		MemberCount:   1,
	})
}

// GetGroup returns group details
// GET /api/v2.1/groups/:group_id/
func (h *GroupHandler) GetGroup(c *gin.Context) {
	groupID := c.Param("group_id")
	orgID := c.GetString("org_id")

	if groupID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "group_id is required"})
		return
	}

	groupUUID, err := uuid.Parse(groupID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid group_id"})
		return
	}

	// Check if database is available
	if h.db == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database not available"})
		return
	}

	orgUUID, _ := uuid.Parse(orgID)

	// Get group details
	var name string
	var creatorID string
	var createdAt time.Time

	if err := h.db.Session().Query(`
		SELECT name, creator_id, created_at FROM groups WHERE org_id = ? AND group_id = ?
	`, orgUUID.String(), groupUUID.String()).Scan(&name, &creatorID, &createdAt); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "group not found"})
		return
	}

	// Get creator email
	var creatorEmail string
	h.db.Session().Query(`SELECT email FROM users WHERE org_id = ? AND user_id = ?`, orgUUID.String(), creatorID).Scan(&creatorEmail)

	// Count members
	var memberCount int
	h.db.Session().Query(`SELECT COUNT(*) FROM group_members WHERE group_id = ?`, groupUUID.String()).Scan(&memberCount)

	c.JSON(http.StatusOK, GroupResponse{
		GroupID:       groupID,
		GroupName:     name,
		Owner:         creatorEmail,
		CreatorID:     creatorID,
		CreatedAt:     createdAt.Format(time.RFC3339),
		ParentGroupID: 0,
		Admins:        h.getGroupAdminEmails(orgUUID.String(), groupUUID.String()),
		MemberCount:   memberCount,
	})
}

// UpdateGroup updates group details: rename (name) or transfer ownership (owner).
// PUT /api/v2.1/groups/:group_id/
func (h *GroupHandler) UpdateGroup(c *gin.Context) {
	groupID := c.Param("group_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	var req UpdateGroupRequest
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.GroupName == "" && req.Owner == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name or owner is required"})
		return
	}

	groupUUID, err := uuid.Parse(groupID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid group_id"})
		return
	}

	// Check if database is available
	if h.db == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database not available"})
		return
	}

	orgUUID, _ := uuid.Parse(orgID)

	// Transfer ownership
	if req.Owner != "" {
		// Only the current owner can transfer
		var callerRole string
		if err := h.db.Session().Query(`
			SELECT role FROM group_members WHERE group_id = ? AND user_id = ?
		`, groupUUID.String(), userID).Scan(&callerRole); err != nil || callerRole != "owner" {
			c.JSON(http.StatusForbidden, gin.H{"error": "only group owner can transfer the group"})
			return
		}

		// Resolve new owner by email
		var newOwnerID string
		if err := h.db.Session().Query(`
			SELECT user_id FROM users_by_email WHERE email = ?
		`, req.Owner).Scan(&newOwnerID); err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
			return
		}

		now := time.Now()
		var groupName string
		h.db.Session().Query(`SELECT name FROM groups WHERE org_id = ? AND group_id = ?`,
			orgUUID.String(), groupUUID.String()).Scan(&groupName)

		batch := h.db.Session().Batch(gocql.LoggedBatch)
		batch.Query(`
			UPDATE groups SET creator_id = ?, updated_at = ? WHERE org_id = ? AND group_id = ?
		`, newOwnerID, now, orgUUID.String(), groupUUID.String())
		batch.Query(`
			UPDATE group_members SET role = ? WHERE group_id = ? AND user_id = ?
		`, "member", groupUUID.String(), userID)
		batch.Query(`
			UPDATE groups_by_member SET role = ? WHERE org_id = ? AND user_id = ? AND group_id = ?
		`, "member", orgUUID.String(), userID, groupUUID.String())
		batch.Query(`
			INSERT INTO group_members (group_id, user_id, role, added_at)
			VALUES (?, ?, ?, ?)
		`, groupUUID.String(), newOwnerID, "owner", now)
		batch.Query(`
			INSERT INTO groups_by_member (org_id, user_id, group_id, group_name, role, added_at)
			VALUES (?, ?, ?, ?, ?, ?)
		`, orgUUID.String(), newOwnerID, groupUUID.String(), groupName, "owner", now)
		if err := batch.Exec(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to transfer group"})
			return
		}

		c.JSON(http.StatusOK, gin.H{"success": true})
		return
	}

	// Rename group
	// Verify user is owner or admin
	var role string
	if err := h.db.Session().Query(`
		SELECT role FROM group_members WHERE group_id = ? AND user_id = ?
	`, groupUUID.String(), userID).Scan(&role); err != nil || (role != "owner" && role != "admin") {
		c.JSON(http.StatusForbidden, gin.H{"error": "permission denied"})
		return
	}

	if err := renameGroup(h.db, orgUUID.String(), groupUUID.String(), req.GroupName, time.Now()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update group"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// DeleteGroup deletes a group and cleans up all related data (shares, members).
// DELETE /api/v2.1/groups/:group_id/
func (h *GroupHandler) DeleteGroup(c *gin.Context) {
	groupID := c.Param("group_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	groupUUID, err := uuid.Parse(groupID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid group_id"})
		return
	}

	if h.db == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database not available"})
		return
	}

	orgUUID, _ := uuid.Parse(orgID)
	userUUID, _ := uuid.Parse(userID)

	// Verify user is owner
	var role string
	if err := h.db.Session().Query(`
		SELECT role FROM group_members WHERE group_id = ? AND user_id = ?
	`, groupUUID.String(), userUUID.String()).Scan(&role); err != nil || role != "owner" {
		c.JSON(http.StatusForbidden, gin.H{"error": "only group owner can delete the group"})
		return
	}

	// Get group name before deletion (for audit log)
	var groupName string
	h.db.Session().Query(`SELECT name FROM groups WHERE org_id = ? AND group_id = ?`,
		orgUUID.String(), groupUUID.String()).Scan(&groupName)

	// Collect member IDs before deletion (for groups_by_member cleanup)
	memberIter := h.db.Session().Query(`SELECT user_id FROM group_members WHERE group_id = ?`, groupUUID.String()).Iter()
	var memberID string
	var memberIDs []string
	for memberIter.Scan(&memberID) {
		memberIDs = append(memberIDs, memberID)
	}
	memberIter.Close()

	// Atomic batch: delete group + groups_by_id + group_members
	batch := h.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`DELETE FROM groups WHERE org_id = ? AND group_id = ?`, orgUUID.String(), groupUUID.String())
	batch.Query(`DELETE FROM groups_by_id WHERE group_id = ?`, groupUUID.String())
	batch.Query(`DELETE FROM group_members WHERE group_id = ?`, groupUUID.String())
	if err := batch.Exec(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete group"})
		return
	}

	if err := cleanupGroupsByMember(h.db, orgUUID.String(), groupUUID.String(), memberIDs); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to clean group membership index"})
		return
	}

	if err := cleanupGroupShares(h.db, groupUUID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to clean group shares"})
		return
	}

	// Audit log
	auditDetails, _ := json.Marshal(map[string]interface{}{
		"group_name":    groupName,
		"members_count": len(memberIDs),
	})
	if err := h.db.Session().Query(`
		INSERT INTO audit_log (org_id, timestamp, action, target_type, target_id, actor_id, details)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, orgUUID.String(), time.Now(), "delete_group", "group", groupID, userID,
		string(auditDetails)).Exec(); err != nil {
		slog.Warn("DeleteGroup: failed to write audit log", "group_id", groupID, "org_id", orgUUID.String(), "error", err)
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ListGroupMembers returns list of group members
// GET /api/v2.1/groups/:group_id/members/
func (h *GroupHandler) ListGroupMembers(c *gin.Context) {
	groupID := c.Param("group_id")
	orgID := c.GetString("org_id")

	groupUUID, err := uuid.Parse(groupID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid group_id"})
		return
	}

	// Check if database is available
	if h.db == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database not available"})
		return
	}

	// Query group members
	iter := h.db.Session().Query(`
		SELECT user_id, role, added_at FROM group_members WHERE group_id = ?
	`, groupUUID.String()).Iter()

	var members []GroupMemberResponse
	var userID, role string
	var addedAt time.Time

	for iter.Scan(&userID, &role, &addedAt) {
		// Get user details (org_id is the partition key - must be included)
		var email, name string
		h.db.Session().Query(`SELECT email, name FROM users WHERE org_id = ? AND user_id = ?`, orgID, userID).Scan(&email, &name)

		members = append(members, GroupMemberResponse{
			Email:     email,
			Name:      name,
			UserID:    userID,
			Role:      capitalizeRole(role),
			AddedAt:   addedAt.Format(time.RFC3339),
			AvatarURL: "",
		})
	}

	if err := iter.Close(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list members"})
		return
	}

	if members == nil {
		members = []GroupMemberResponse{}
	}

	c.JSON(http.StatusOK, members)
}

// AddGroupMember adds a member to a group
// POST /api/v2.1/groups/:group_id/members/
func (h *GroupHandler) AddGroupMember(c *gin.Context) {
	groupID := c.Param("group_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	var req AddMemberRequest
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Email == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "email is required"})
		return
	}

	// Default role
	if req.Role == "" {
		req.Role = "member"
	}

	groupUUID, err := uuid.Parse(groupID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid group_id"})
		return
	}

	// Check if database is available
	if h.db == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database not available"})
		return
	}

	orgUUID, _ := uuid.Parse(orgID)

	// Verify user is owner or admin
	var role string
	if err := h.db.Session().Query(`
		SELECT role FROM group_members WHERE group_id = ? AND user_id = ?
	`, groupUUID.String(), userID).Scan(&role); err != nil || (role != "owner" && role != "admin") {
		c.JSON(http.StatusForbidden, gin.H{"error": "permission denied"})
		return
	}

	// Get user ID by email
	var newMemberID string
	if err := h.db.Session().Query(`
		SELECT user_id FROM users_by_email WHERE email = ?
	`, req.Email).Scan(&newMemberID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	// Check if user is already a member of this group
	var existingRole string
	if err := h.db.Session().Query(`
		SELECT role FROM group_members WHERE group_id = ? AND user_id = ?
	`, groupUUID.String(), newMemberID).Scan(&existingRole); err == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "User is already a member of this group."})
		return
	}

	now := time.Now()

	// Get group name
	var groupName string
	h.db.Session().Query(`SELECT name FROM groups WHERE org_id = ? AND group_id = ?`, orgUUID.String(), groupUUID.String()).Scan(&groupName)

	if err := upsertGroupMember(h.db, orgUUID.String(), groupUUID.String(), newMemberID, groupName, req.Role, now); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to add member"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// RemoveGroupMember removes a member from a group
// DELETE /api/v2.1/groups/:group_id/members/:user_email/
func (h *GroupHandler) RemoveGroupMember(c *gin.Context) {
	groupID := c.Param("group_id")
	userEmail := c.Param("user_email")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	groupUUID, err := uuid.Parse(groupID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid group_id"})
		return
	}

	// Check if database is available
	if h.db == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database not available"})
		return
	}

	orgUUID, _ := uuid.Parse(orgID)
	userUUID, _ := uuid.Parse(userID)

	// Resolve the target member's ID first so we can check for self-removal
	var memberID string
	if err := h.db.Session().Query(`
		SELECT user_id FROM users_by_email WHERE email = ?
	`, userEmail).Scan(&memberID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	isSelf := memberID == userUUID.String()

	// Get caller's role in the group
	var role string
	if err := h.db.Session().Query(`
		SELECT role FROM group_members WHERE group_id = ? AND user_id = ?
	`, groupUUID.String(), userUUID.String()).Scan(&role); err != nil {
		// Caller is not a member of this group
		c.JSON(http.StatusForbidden, gin.H{"error": "permission denied"})
		return
	}

	// Regular members may only remove themselves; owner/admin can remove others
	if !isSelf && role != "owner" && role != "admin" {
		c.JSON(http.StatusForbidden, gin.H{"error": "permission denied"})
		return
	}

	// Don't allow removing the owner
	var memberRole string
	if err := h.db.Session().Query(`
		SELECT role FROM group_members WHERE group_id = ? AND user_id = ?
	`, groupUUID.String(), memberID).Scan(&memberRole); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "member not found"})
		return
	}

	if memberRole == "owner" {
		c.JSON(http.StatusForbidden, gin.H{"error": "cannot remove group owner"})
		return
	}

	if err := deleteGroupMember(h.db, orgUUID.String(), groupUUID.String(), memberID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to remove member"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ListGroupLibraries returns all libraries shared with a group.
// The caller must be a member of the group.
// GET /api/v2.1/groups/:group_id/libraries/
func (h *GroupHandler) ListGroupLibraries(c *gin.Context) {
	groupID := c.Param("group_id")
	userID := c.GetString("user_id")
	orgID := c.GetString("org_id")

	groupUUID, err := uuid.Parse(groupID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid group_id"})
		return
	}

	if h.db == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database not available"})
		return
	}

	// Verify caller is a member of the group
	var role string
	if err := h.db.Session().Query(`
		SELECT role FROM group_members WHERE group_id = ? AND user_id = ?
	`, groupUUID.String(), userID).Scan(&role); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "not a member of this group"})
		return
	}

	repos := h.getGroupRepos(orgID, groupUUID.String())
	c.JSON(http.StatusOK, repos)
}

// CreateGroupOwnedLibrary creates a new library owned by the group.
// POST /api/v2.1/groups/:group_id/group-owned-libraries/
// FormData: name, passwd (optional), permission
func (h *GroupHandler) CreateGroupOwnedLibrary(c *gin.Context) {
	groupID := c.Param("group_id")
	userID := c.GetString("user_id")
	orgID := c.GetString("org_id")

	groupUUID, err := uuid.Parse(groupID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid group_id"})
		return
	}

	if h.db == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database not available"})
		return
	}

	// Verify caller is an owner or admin of the group
	var role string
	if err := h.db.Session().Query(`
		SELECT role FROM group_members WHERE group_id = ? AND user_id = ?
	`, groupUUID.String(), userID).Scan(&role); err != nil || (role != "owner" && role != "admin") {
		c.JSON(http.StatusForbidden, gin.H{"error": "permission denied"})
		return
	}

	// Resolve group name
	var groupName string
	h.db.Session().Query(`SELECT name FROM groups WHERE org_id = ? AND group_id = ?`,
		orgID, groupUUID.String()).Scan(&groupName)

	// ENFORCEMENT CHECK: feature flag + numeric limit
	if h.config != nil {
		enforcement := GetOrgEnforcement(h.db, orgID, h.config)
		if !enforcement.Profile.Features.CanAddRepo {
			c.JSON(http.StatusForbidden, gin.H{
				"error":            "Library creation is not available on your plan",
				"upgrade_required": true,
			})
			return
		}
		if enforcement.Profile.Limits.MaxLibraries > 0 {
			count := CountActiveLibraries(h.db, orgID)
			if count >= enforcement.Profile.Limits.MaxLibraries {
				c.JSON(http.StatusForbidden, gin.H{
					"error":   "Library limit reached",
					"limit":   enforcement.Profile.Limits.MaxLibraries,
					"current": count,
				})
				return
			}
		}
	}

	var createLibReq struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&createLibReq); err != nil || createLibReq.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}
	repoName := createLibReq.Name

	newLibID := uuid.New().String()
	now := time.Now()

	// Create library (owned by the requesting user on behalf of the group)
	batch := h.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		INSERT INTO libraries (org_id, library_id, owner_id, name, encrypted, size_bytes, file_count, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, orgID, newLibID, userID, repoName, false, int64(0), int64(0), now, now)
	batch.Query(`
		INSERT INTO libraries_by_id (library_id, org_id, owner_id, name, encrypted)
		VALUES (?, ?, ?, ?, ?)
	`, newLibID, orgID, userID, repoName, false)
	if err := batch.Exec(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create library"})
		return
	}

	// Initialize filesystem (root dir + initial commit)
	fsHelper := NewFSHelper(h.db)
	if err := fsHelper.InitializeLibraryFS(orgID, newLibID, userID, repoName); err != nil {
		_ = rollbackNewLibrary(h.db, orgID, newLibID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to initialize library filesystem"})
		return
	}

	// Share the library with the group
	shareID := uuid.New().String()
	if err := createLibraryShare(h.db, newLibID, shareID, userID, groupUUID.String(), "group", "rw", now, nil); err != nil {
		_ = rollbackNewLibrary(h.db, orgID, newLibID)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to share library with group"})
		return
	}

	// Resolve caller email and name for the response
	var ownerEmail, ownerName string
	h.db.Session().Query(`SELECT email, name FROM users WHERE org_id = ? AND user_id = ?`, orgID, userID).Scan(&ownerEmail, &ownerName)
	if ownerEmail == "" {
		ownerEmail = userID
	}
	if ownerName == "" {
		ownerName = strings.Split(ownerEmail, "@")[0]
	}

	c.JSON(http.StatusOK, gin.H{
		"repo_id":                newLibID,
		"repo_name":              repoName,
		"permission":             "rw",
		"size":                   0,
		"owner_email":            ownerEmail,
		"owner_contact_email":    ownerEmail,
		"owner_name":             ownerName,
		"encrypted":              false,
		"last_modified":          now.UnixMilli(),
		"modifier_email":         ownerEmail,
		"modifier_contact_email": ownerEmail,
		"modifier_name":          ownerName,
		"group_name":             groupName,
		"type":                   "repo",
	})
}

// DeleteGroupOwnedLibrary soft-deletes a group-owned library.
// DELETE /api/v2.1/groups/:group_id/group-owned-libraries/:library_id/
func (h *GroupHandler) DeleteGroupOwnedLibrary(c *gin.Context) {
	groupID := c.Param("group_id")
	repoID := c.Param("library_id")
	userID := c.GetString("user_id")
	orgID := c.GetString("org_id")

	groupUUID, err := uuid.Parse(groupID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid group_id"})
		return
	}

	if h.db == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database not available"})
		return
	}

	// Check if caller is an owner or admin of the group
	var role string
	isGroupAdmin := false
	if err := h.db.Session().Query(`
		SELECT role FROM group_members WHERE group_id = ? AND user_id = ?
	`, groupUUID.String(), userID).Scan(&role); err == nil && (role == "owner" || role == "admin") {
		isGroupAdmin = true
	}

	// Check library exists and is not already deleted; also get owner_id
	var deletedAt time.Time
	var libOwnerID string
	if err := h.db.Session().Query(`
		SELECT deleted_at, owner_id FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(&deletedAt, &libOwnerID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}
	if !deletedAt.IsZero() {
		c.JSON(http.StatusNotFound, gin.H{"error": "library already deleted"})
		return
	}

	// Allow group owner/admin OR the library owner
	if !isGroupAdmin && libOwnerID != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "permission denied"})
		return
	}

	now := time.Now()
	if err := h.db.Session().Query(`
		UPDATE libraries SET deleted_at = ?, deleted_by = ?
		WHERE org_id = ? AND library_id = ?
	`, now, userID, orgID, repoID).Exec(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete library"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// BulkAddGroupMembers adds multiple members to a group by comma-separated emails.
// POST /api/v2.1/groups/:group_id/members/bulk/
// FormData: emails (comma-separated)
func (h *GroupHandler) BulkAddGroupMembers(c *gin.Context) {
	groupID := c.Param("group_id")
	userID := c.GetString("user_id")
	orgID := c.GetString("org_id")

	groupUUID, err := uuid.Parse(groupID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid group_id"})
		return
	}

	if h.db == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database not available"})
		return
	}

	// Verify caller is an owner or admin of the group
	var role string
	if err := h.db.Session().Query(`
		SELECT role FROM group_members WHERE group_id = ? AND user_id = ?
	`, groupUUID.String(), userID).Scan(&role); err != nil || (role != "owner" && role != "admin") {
		c.JSON(http.StatusForbidden, gin.H{"error": "permission denied"})
		return
	}

	var bulkMembersReq struct {
		Emails []string `json:"emails"`
	}
	if err := c.ShouldBindJSON(&bulkMembersReq); err != nil || len(bulkMembersReq.Emails) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "emails is required"})
		return
	}
	emailsRaw := strings.Join(bulkMembersReq.Emails, ",")

	// Get group name once for lookup inserts
	var groupName string
	h.db.Session().Query(`SELECT name FROM groups WHERE org_id = ? AND group_id = ?`,
		orgID, groupUUID.String()).Scan(&groupName)

	type failedItem struct {
		Email    string `json:"email"`
		ErrorMsg string `json:"error_msg"`
	}

	// Pre-load existing members to check for duplicates
	existingMembers := make(map[string]bool)
	memberIter := h.db.Session().Query(`
		SELECT user_id FROM group_members WHERE group_id = ?
	`, groupUUID.String()).Iter()
	var existMemberID string
	for memberIter.Scan(&existMemberID) {
		existingMembers[existMemberID] = true
	}
	memberIter.Close()

	var failed []failedItem
	now := time.Now()

	// Phase 1: Resolve emails and collect valid members
	type resolvedMember struct {
		email  string
		userID string
		name   string
	}
	var valid []resolvedMember

	for _, email := range strings.Split(emailsRaw, ",") {
		email = strings.TrimSpace(email)
		if email == "" {
			continue
		}

		var memberID string
		if err := h.db.Session().Query(`
			SELECT user_id FROM users_by_email WHERE email = ?
		`, email).Scan(&memberID); err != nil {
			failed = append(failed, failedItem{Email: email, ErrorMsg: "user not found"})
			continue
		}

		if existingMembers[memberID] {
			failed = append(failed, failedItem{Email: email, ErrorMsg: "User is already a member of this group."})
			continue
		}

		existingMembers[memberID] = true

		var memberName string
		h.db.Session().Query(`SELECT name FROM users WHERE org_id = ? AND user_id = ?`, orgID, memberID).Scan(&memberName)

		valid = append(valid, resolvedMember{email: email, userID: memberID, name: memberName})
	}

	// Phase 2: Batch-insert all valid members (chunks of 25)
	var success []GroupMemberResponse
	if len(valid) > 0 {
		inserts := make([]bulkMemberInsert, len(valid))
		for i, v := range valid {
			inserts[i] = bulkMemberInsert{UserID: v.userID, Role: "member"}
		}
		if err := bulkUpsertGroupMembers(h.db, orgID, groupUUID.String(), groupName, inserts, now); err != nil {
			for _, v := range valid {
				failed = append(failed, failedItem{Email: v.email, ErrorMsg: "failed to add member"})
			}
		} else {
			for _, v := range valid {
				success = append(success, GroupMemberResponse{
					Email:     v.email,
					Name:      v.name,
					UserID:    v.userID,
					Role:      "Member",
					AddedAt:   now.Format(time.RFC3339),
					AvatarURL: "",
				})
			}
		}
	}

	if failed == nil {
		failed = []failedItem{}
	}
	if success == nil {
		success = []GroupMemberResponse{}
	}

	c.JSON(http.StatusOK, gin.H{"success": success, "failed": failed})
}

// ImportGroupMembersViaFile adds group members from an uploaded CSV/text file.
// POST /api/v2.1/groups/:group_id/members/import/
// multipart: file
func (h *GroupHandler) ImportGroupMembersViaFile(c *gin.Context) {
	groupID := c.Param("group_id")
	userID := c.GetString("user_id")
	orgID := c.GetString("org_id")

	groupUUID, err := uuid.Parse(groupID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid group_id"})
		return
	}

	if h.db == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database not available"})
		return
	}

	// Verify caller is an owner or admin of the group
	var callerRole string
	if err := h.db.Session().Query(`
		SELECT role FROM group_members WHERE group_id = ? AND user_id = ?
	`, groupUUID.String(), userID).Scan(&callerRole); err != nil || (callerRole != "owner" && callerRole != "admin") {
		c.JSON(http.StatusForbidden, gin.H{"error": "permission denied"})
		return
	}

	file, _, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file is required"})
		return
	}
	defer file.Close()

	var buf strings.Builder
	io.Copy(&buf, file) //nolint:errcheck

	var groupName string
	h.db.Session().Query(`SELECT name FROM groups WHERE org_id = ? AND group_id = ?`,
		orgID, groupUUID.String()).Scan(&groupName)

	type failedItem struct {
		Email    string `json:"email"`
		ErrorMsg string `json:"error_msg"`
	}

	// Pre-load existing members to check for duplicates
	existingMembers := make(map[string]bool)
	memberIter := h.db.Session().Query(`
		SELECT user_id FROM group_members WHERE group_id = ?
	`, groupUUID.String()).Iter()
	var existMemberID string
	for memberIter.Scan(&existMemberID) {
		existingMembers[existMemberID] = true
	}
	memberIter.Close()

	var failed []failedItem
	now := time.Now()

	// Phase 1: Resolve emails and collect valid members
	type resolvedImport struct {
		email  string
		userID string
	}
	var valid []resolvedImport

	for _, line := range strings.Split(buf.String(), "\n") {
		email := strings.TrimSpace(line)
		if email == "" {
			continue
		}
		email = strings.Trim(email, `"`)
		email = strings.Trim(email, "'")
		if email == "" {
			continue
		}

		var memberID string
		if err := h.db.Session().Query(`
			SELECT user_id FROM users_by_email WHERE email = ?
		`, email).Scan(&memberID); err != nil {
			failed = append(failed, failedItem{Email: email, ErrorMsg: "user not found"})
			continue
		}

		if existingMembers[memberID] {
			failed = append(failed, failedItem{Email: email, ErrorMsg: "User is already a member of this group."})
			continue
		}

		existingMembers[memberID] = true
		valid = append(valid, resolvedImport{email: email, userID: memberID})
	}

	// Phase 2: Batch-insert all valid members (chunks of 25)
	var success []string
	if len(valid) > 0 {
		inserts := make([]bulkMemberInsert, len(valid))
		for i, v := range valid {
			inserts[i] = bulkMemberInsert{UserID: v.userID, Role: "member"}
		}
		if err := bulkUpsertGroupMembers(h.db, orgID, groupUUID.String(), groupName, inserts, now); err != nil {
			for _, v := range valid {
				failed = append(failed, failedItem{Email: v.email, ErrorMsg: "failed to add member"})
			}
		} else {
			for _, v := range valid {
				success = append(success, v.email)
			}
		}
	}

	if failed == nil {
		failed = []failedItem{}
	}
	if success == nil {
		success = []string{}
	}

	c.JSON(http.StatusOK, gin.H{"success": success, "failed": failed})
}

// SetGroupAdmin promotes or demotes a group member to/from admin role.
// PUT /api/v2.1/groups/:group_id/members/:user_email/
// Body (JSON): { "is_admin": "True" | "False" }
func (h *GroupHandler) SetGroupAdmin(c *gin.Context) {
	groupID := c.Param("group_id")
	userEmail := c.Param("user_email")
	orgID := c.GetString("org_id")
	callerID := c.GetString("user_id")

	groupUUID, err := uuid.Parse(groupID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid group_id"})
		return
	}

	if h.db == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database not available"})
		return
	}

	// Verify caller is owner
	var callerRole string
	if err := h.db.Session().Query(`
		SELECT role FROM group_members WHERE group_id = ? AND user_id = ?
	`, groupUUID.String(), callerID).Scan(&callerRole); err != nil || callerRole != "owner" {
		c.JSON(http.StatusForbidden, gin.H{"error": "only group owner can change member roles"})
		return
	}

	// Resolve target member by email
	var memberID string
	if err := h.db.Session().Query(`
		SELECT user_id FROM users_by_email WHERE email = ?
	`, userEmail).Scan(&memberID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	// Determine new role from is_admin param
	var req struct {
		IsAdmin bool `json:"is_admin"`
	}
	c.ShouldBindJSON(&req) //nolint:errcheck
	newRole := "member"
	if req.IsAdmin {
		newRole = "admin"
	}

	batch := h.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		UPDATE group_members SET role = ? WHERE group_id = ? AND user_id = ?
	`, newRole, groupUUID.String(), memberID)
	batch.Query(`
		UPDATE groups_by_member SET role = ? WHERE org_id = ? AND user_id = ? AND group_id = ?
	`, newRole, orgID, memberID, groupUUID.String())
	if err := batch.Exec(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update member role"})
		return
	}

	// Fetch member details for response
	var memberName string
	h.db.Session().Query(`SELECT name FROM users WHERE org_id = ? AND user_id = ?`, orgID, memberID).Scan(&memberName)

	// Fetch added_at for response
	var addedAt time.Time
	h.db.Session().Query(`SELECT added_at FROM group_members WHERE group_id = ? AND user_id = ?`,
		groupUUID.String(), memberID).Scan(&addedAt)

	c.JSON(http.StatusOK, GroupMemberResponse{
		Email:     userEmail,
		Name:      memberName,
		UserID:    memberID,
		Role:      capitalizeRole(newRole),
		AddedAt:   addedAt.Format(time.RFC3339),
		AvatarURL: "",
	})
}

// ListShareableGroups returns groups the authenticated user can share items with.
// This is the same as the user's groups but with the response format expected by
// the seafile-js shareableGroups() call.
// GET /api/v2.1/shareable-groups/
func (h *GroupHandler) ListShareableGroups(c *gin.Context) {
	userID := c.GetString("user_id")
	orgID := c.GetString("org_id")

	userUUID, err := uuid.Parse(userID)
	if err != nil {
		c.JSON(http.StatusOK, []interface{}{})
		return
	}
	orgUUID, err := uuid.Parse(orgID)
	if err != nil {
		c.JSON(http.StatusOK, []interface{}{})
		return
	}

	iter := h.db.Session().Query(`
		SELECT group_id, group_name
		FROM groups_by_member
		WHERE org_id = ? AND user_id = ?
	`, orgUUID.String(), userUUID.String()).Iter()

	type shareableGroup struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		ParentGroupID int    `json:"parent_group_id"`
	}

	var groups []shareableGroup
	var groupID, groupName string

	for iter.Scan(&groupID, &groupName) {
		// Verify group still exists (groups_by_member may have stale entries)
		var existsName string
		if err := h.db.Session().Query(`
			SELECT name FROM groups WHERE org_id = ? AND group_id = ?
		`, orgUUID.String(), groupID).Scan(&existsName); err != nil {
			continue
		}
		groups = append(groups, shareableGroup{
			ID:            groupID,
			Name:          existsName,
			ParentGroupID: 0,
		})
	}

	if err := iter.Close(); err != nil {
		slog.Error("ListShareableGroups: failed to close iterator", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list groups"})
		return
	}

	if groups == nil {
		groups = []shareableGroup{}
	}

	c.JSON(http.StatusOK, groups)
}
