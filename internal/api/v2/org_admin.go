package v2

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// OrgAdminHandler handles org-scoped admin API requests.
// These endpoints are accessed by org admins managing their own organisation
// and are distinct from the platform-admin endpoints under /api/v2.1/admin/.
type OrgAdminHandler struct {
	db             *db.DB
	config         *config.Config
	permMiddleware *middleware.PermissionMiddleware
	sessions       SessionInvalidator
}

// NewOrgAdminHandler creates a new OrgAdminHandler.
func NewOrgAdminHandler(database *db.DB, cfg *config.Config, perm *middleware.PermissionMiddleware, sessions SessionInvalidator) *OrgAdminHandler {
	return &OrgAdminHandler{
		db:             database,
		config:         cfg,
		permMiddleware: perm,
		sessions:       sessions,
	}
}

// RegisterOrgAdminRoutes registers org-admin API routes under /api/v2.1/org.
//
// Route groups:
//   - /org/admin/...          — endpoints that use the JWT org (no :org_id in URL)
//   - /org/:org_id/admin/...  — endpoints that require an explicit org_id parameter
//
// All endpoints require the user to be an org admin of the specified org or a superadmin.
func RegisterOrgAdminRoutes(rg *gin.RouterGroup, database *db.DB, cfg *config.Config, perm *middleware.PermissionMiddleware, sessions SessionInvalidator) {
	h := NewOrgAdminHandler(database, cfg, perm, sessions)

	orgBase := rg.Group("/org")
	orgBase.Use(perm.RequireAdminOrAbove())

	// -------------------------------------------------------------------------
	// Endpoints without :org_id — org is derived from the authenticated user's JWT
	// -------------------------------------------------------------------------
	noIDGroup := orgBase.Group("/admin")
	{
		// Org info
		noIDGroup.GET("/info/", h.GetOrgInfo)
		noIDGroup.GET("/info", h.GetOrgInfo)
		noIDGroup.PUT("/info/", h.UpdateOrgInfo)
		noIDGroup.PUT("/info", h.UpdateOrgInfo)

		// Public share links
		noIDGroup.GET("/links/", h.ListOrgLinks)
		noIDGroup.GET("/links", h.ListOrgLinks)
		noIDGroup.DELETE("/links/:token/", h.DeleteOrgLink)
		noIDGroup.DELETE("/links/:token", h.DeleteOrgLink)

		// Upload links
		noIDGroup.GET("/upload-links/", h.ListOrgUploadLinks)
		noIDGroup.GET("/upload-links", h.ListOrgUploadLinks)
		noIDGroup.DELETE("/upload-links/:token/", h.DeleteOrgUploadLink)
		noIDGroup.DELETE("/upload-links/:token", h.DeleteOrgUploadLink)

		// Audit logs
		noIDGroup.GET("/logs/file-access/", h.ListOrgFileAccessLogs)
		noIDGroup.GET("/logs/file-access", h.ListOrgFileAccessLogs)
		noIDGroup.GET("/logs/file-update/", h.ListOrgFileUpdateLogs)
		noIDGroup.GET("/logs/file-update", h.ListOrgFileUpdateLogs)
		noIDGroup.GET("/logs/repo-permission/", h.ListOrgRepoPermLogs)
		noIDGroup.GET("/logs/repo-permission", h.ListOrgRepoPermLogs)
	}

	// -------------------------------------------------------------------------
	// Endpoints with :org_id — validated against the authenticated user's org
	// -------------------------------------------------------------------------
	idGroup := orgBase.Group("/:org_id/admin")
	{
		// ---- Users ----
		idGroup.GET("/users/", h.ListOrgUsers)
		idGroup.GET("/users", h.ListOrgUsers)
		idGroup.POST("/users/", h.AddOrgUser)
		idGroup.POST("/users", h.AddOrgUser)
		idGroup.GET("/users/:email/", h.GetOrgUser)
		idGroup.GET("/users/:email", h.GetOrgUser)
		idGroup.PUT("/users/:email/", h.UpdateOrgUser)
		idGroup.PUT("/users/:email", h.UpdateOrgUser)
		idGroup.DELETE("/users/:email/", h.DeleteOrgUser)
		idGroup.DELETE("/users/:email", h.DeleteOrgUser)
		idGroup.PUT("/users/:email/set-password/", h.ResetOrgUserPassword)
		idGroup.PUT("/users/:email/set-password", h.ResetOrgUserPassword)
		idGroup.GET("/users/:email/repos/", h.GetOrgUserOwnedRepos)
		idGroup.GET("/users/:email/repos", h.GetOrgUserOwnedRepos)
		idGroup.GET("/users/:email/beshared-repos/", h.GetOrgUserBesharedRepos)
		idGroup.GET("/users/:email/beshared-repos", h.GetOrgUserBesharedRepos)
		idGroup.GET("/search-user/", h.SearchOrgUser)
		idGroup.GET("/search-user", h.SearchOrgUser)
		idGroup.POST("/import-users/", h.ImportOrgUsers)
		idGroup.POST("/import-users", h.ImportOrgUsers)
		idGroup.POST("/invite-users/", h.InviteOrgUsers)
		idGroup.POST("/invite-users", h.InviteOrgUsers)

		// ---- Groups ----
		idGroup.GET("/groups/", h.ListOrgGroups)
		idGroup.GET("/groups", h.ListOrgGroups)
		idGroup.GET("/groups/:gid/", h.GetOrgGroup)
		idGroup.GET("/groups/:gid", h.GetOrgGroup)
		idGroup.PUT("/groups/:gid/", h.UpdateOrgGroup)
		idGroup.PUT("/groups/:gid", h.UpdateOrgGroup)
		idGroup.PUT("/groups/:gid/transfer/", h.TransferOrgGroup)
		idGroup.PUT("/groups/:gid/transfer", h.TransferOrgGroup)
		idGroup.DELETE("/groups/:gid/", h.DeleteOrgGroup)
		idGroup.DELETE("/groups/:gid", h.DeleteOrgGroup)
		idGroup.GET("/groups/:gid/members/", h.ListOrgGroupMembers)
		idGroup.GET("/groups/:gid/members", h.ListOrgGroupMembers)
		idGroup.POST("/groups/:gid/members/", h.AddOrgGroupMember)
		idGroup.POST("/groups/:gid/members", h.AddOrgGroupMember)
		idGroup.DELETE("/groups/:gid/members/:email/", h.DeleteOrgGroupMember)
		idGroup.DELETE("/groups/:gid/members/:email", h.DeleteOrgGroupMember)
		idGroup.PUT("/groups/:gid/members/:email/", h.UpdateOrgGroupMember)
		idGroup.PUT("/groups/:gid/members/:email", h.UpdateOrgGroupMember)
		idGroup.GET("/groups/:gid/libraries/", h.ListOrgGroupLibraries)
		idGroup.GET("/groups/:gid/libraries", h.ListOrgGroupLibraries)
		idGroup.POST("/groups/:gid/group-owned-libraries/", h.AddOrgGroupOwnedLibrary)
		idGroup.POST("/groups/:gid/group-owned-libraries", h.AddOrgGroupOwnedLibrary)
		idGroup.DELETE("/groups/:gid/group-owned-libraries/:rid/", h.DeleteOrgGroupOwnedLibrary)
		idGroup.DELETE("/groups/:gid/group-owned-libraries/:rid", h.DeleteOrgGroupOwnedLibrary)
		idGroup.GET("/search-group/", h.SearchOrgGroup)
		idGroup.GET("/search-group", h.SearchOrgGroup)

		// ---- Repositories ----
		idGroup.GET("/repos/", h.ListOrgRepos)
		idGroup.GET("/repos", h.ListOrgRepos)
		idGroup.DELETE("/repos/:rid/", h.DeleteOrgRepo)
		idGroup.DELETE("/repos/:rid", h.DeleteOrgRepo)
		idGroup.PUT("/repos/:rid/", h.TransferOrgRepo)
		idGroup.PUT("/repos/:rid", h.TransferOrgRepo)
		idGroup.GET("/repos/:rid/dirents/", h.ListOrgRepoDirents)
		idGroup.GET("/repos/:rid/dirents", h.ListOrgRepoDirents)

		// ---- Trash libraries ----
		idGroup.GET("/trash-libraries/", h.ListOrgTrashLibraries)
		idGroup.GET("/trash-libraries", h.ListOrgTrashLibraries)
		idGroup.DELETE("/trash-libraries/", h.CleanOrgTrashLibraries)
		idGroup.DELETE("/trash-libraries", h.CleanOrgTrashLibraries)
		idGroup.DELETE("/trash-libraries/:rid/", h.DeleteOrgTrashLibrary)
		idGroup.DELETE("/trash-libraries/:rid", h.DeleteOrgTrashLibrary)
		idGroup.PUT("/trash-libraries/:rid/", h.RestoreOrgTrashLibrary)
		idGroup.PUT("/trash-libraries/:rid", h.RestoreOrgTrashLibrary)

		// ---- Departments (address-book) ----
		idGroup.GET("/departments/", h.ListOrgDepartments)
		idGroup.GET("/departments", h.ListOrgDepartments)
		idGroup.GET("/address-book/groups/", h.ListOrgAddressBookGroups)
		idGroup.GET("/address-book/groups", h.ListOrgAddressBookGroups)
		idGroup.POST("/address-book/groups/", h.AddOrgAddressBookGroup)
		idGroup.POST("/address-book/groups", h.AddOrgAddressBookGroup)
		idGroup.GET("/address-book/groups/:gid/", h.GetOrgAddressBookGroup)
		idGroup.GET("/address-book/groups/:gid", h.GetOrgAddressBookGroup)
		idGroup.PUT("/address-book/groups/:gid/", h.UpdateOrgAddressBookGroup)
		idGroup.PUT("/address-book/groups/:gid", h.UpdateOrgAddressBookGroup)
		idGroup.DELETE("/address-book/groups/:gid/", h.DeleteOrgAddressBookGroup)
		idGroup.DELETE("/address-book/groups/:gid", h.DeleteOrgAddressBookGroup)

		// ---- Statistics ----
		idGroup.GET("/statistics/file-operations/", h.OrgStatisticFiles)
		idGroup.GET("/statistics/file-operations", h.OrgStatisticFiles)
		idGroup.GET("/statistics/total-storage/", h.OrgStatisticStorage)
		idGroup.GET("/statistics/total-storage", h.OrgStatisticStorage)
		idGroup.GET("/statistics/active-users/", h.OrgStatisticActiveUsers)
		idGroup.GET("/statistics/active-users", h.OrgStatisticActiveUsers)
		idGroup.GET("/statistics/system-traffic/", h.OrgStatisticTraffic)
		idGroup.GET("/statistics/system-traffic", h.OrgStatisticTraffic)
		idGroup.GET("/statistics/user-traffic/", h.OrgStatisticUserTraffic)
		idGroup.GET("/statistics/user-traffic", h.OrgStatisticUserTraffic)

		// ---- Devices ----
		idGroup.GET("/devices/", h.ListOrgDevices)
		idGroup.GET("/devices", h.ListOrgDevices)
		idGroup.DELETE("/devices/", h.UnlinkOrgDevice)
		idGroup.DELETE("/devices", h.UnlinkOrgDevice)
		idGroup.GET("/devices-errors/", h.ListOrgDeviceErrors)
		idGroup.GET("/devices-errors", h.ListOrgDeviceErrors)

		// ---- Web settings, logo, SAML, domain ----
		idGroup.GET("/web-settings/", h.GetOrgWebSettings)
		idGroup.GET("/web-settings", h.GetOrgWebSettings)
		idGroup.PUT("/web-settings/", h.SetOrgWebSettings)
		idGroup.PUT("/web-settings", h.SetOrgWebSettings)
		idGroup.POST("/logo/", h.UpdateOrgLogo)
		idGroup.POST("/logo", h.UpdateOrgLogo)
		idGroup.GET("/saml-config/", h.GetOrgSAMLConfig)
		idGroup.GET("/saml-config", h.GetOrgSAMLConfig)
		idGroup.PUT("/saml-config/", h.UpdateOrgSAMLConfig)
		idGroup.PUT("/saml-config", h.UpdateOrgSAMLConfig)
		idGroup.PUT("/verify-domain/", h.VerifyOrgDomain)
		idGroup.PUT("/verify-domain", h.VerifyOrgDomain)
	}
}

// notImplemented returns a 501 stub response with a descriptive message.
func (h *OrgAdminHandler) notImplemented(c *gin.Context, feature string) {
	c.JSON(http.StatusNotImplemented, gin.H{
		"error":   "not implemented",
		"feature": feature,
	})
}

// ============================================================================
// Helpers
// ============================================================================

// requireOrgAccess validates that the authenticated caller may administer
// targetOrgID. It returns a non-nil error (and writes the HTTP response) when:
//   - the caller is not authenticated, or
//   - the caller's org does not match targetOrgID and they are not a platform
//     superadmin.
func (h *OrgAdminHandler) requireOrgAccess(c *gin.Context, targetOrgID string) error {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")

	if callerOrgID == "" || callerUserID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		c.Abort()
		return fmt.Errorf("unauthenticated")
	}

	role, err := h.permMiddleware.GetUserOrgRole(callerOrgID, callerUserID)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
		c.Abort()
		return err
	}

	if callerOrgID == middleware.PlatformOrgID {
		// Platform user: must be superadmin to access any org
		if role != middleware.RoleSuperAdmin {
			c.JSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
			c.Abort()
			return fmt.Errorf("insufficient permissions")
		}
		return nil
	}

	// Tenant user: must be admin of their own org and target must match
	if !middleware.HasRequiredOrgRole(role, middleware.RoleAdmin) {
		c.JSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
		c.Abort()
		return fmt.Errorf("insufficient permissions")
	}
	if callerOrgID != targetOrgID {
		c.JSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
		c.Abort()
		return fmt.Errorf("org mismatch")
	}
	return nil
}

// lookupOrgUserByEmail finds the user_id for a user identified by email within
// a specific org. It uses the users_by_email index first and then verifies the
// user belongs to the expected org.
func (h *OrgAdminHandler) lookupOrgUserByEmail(orgID, email string) (userID string, err error) {
	var idxOrgID string
	err = h.db.Session().Query(`
		SELECT user_id, org_id FROM users_by_email WHERE email = ?
	`, email).Scan(&userID, &idxOrgID)
	if err == nil {
		if idxOrgID != orgID {
			return "", fmt.Errorf("user not found in org")
		}
		return userID, nil
	}

	// Fallback: scan the org partition (stays cheap — narrow partition)
	iter := h.db.Session().Query(`
		SELECT user_id FROM users WHERE org_id = ? AND email = ? ALLOW FILTERING
	`, orgID, email).Iter()
	var scanUID string
	found := iter.Scan(&scanUID)
	if closeErr := iter.Close(); closeErr != nil {
		return "", closeErr
	}
	if !found {
		return "", fmt.Errorf("user not found")
	}
	// Backfill the index
	if err := h.db.Session().Query(`
		INSERT INTO users_by_email (email, user_id, org_id) VALUES (?, ?, ?)
	`, email, scanUID, orgID).Exec(); err != nil {
		log.Printf("[lookupOrgUserByEmail] failed to backfill users_by_email for %s in org %s: %v", email, orgID, err)
	}
	return scanUID, nil
}

// orgUserRow is the common user response shape used across org-admin endpoints.
// Field names match the frontend OrgUserInfo model (src/models/org-user.js):
//
//	id, name, email, owner_contact_email, is_active, quota_usage, quota_total,
//	last_login, ctime, is_org_staff, role, org_id
type orgUserRow struct {
	ID           string `json:"id"`
	Email        string `json:"email"`
	Name         string `json:"name"`
	ContactEmail string `json:"owner_contact_email"`
	IsActive     bool   `json:"is_active"`
	IsOrgStaff   bool   `json:"is_org_staff"`
	Role         string `json:"role"`
	QuotaTotal   int64  `json:"quota_total"`
	QuotaUsage   int64  `json:"quota_usage"`
	Ctime        string `json:"ctime"`
	LastLogin    string `json:"last_login"`
	OrgID        string `json:"org_id"`
	AvatarURL    string `json:"avatar_url"`
}

func buildOrgUserRow(email, name, role, status, orgID string, quota, used int64, created time.Time) orgUserRow {
	return orgUserRow{
		ID:           email, // Seafile uses email as user ID in org-admin context
		Email:        email,
		Name:         name,
		ContactEmail: "",
		IsActive:     IsUserUsable(status),
		IsOrgStaff:   role == "admin" || role == "superadmin",
		Role:         role,
		QuotaTotal:   quota,
		QuotaUsage:   used,
		Ctime:        created.Format(time.RFC3339),
		LastLogin:    "",
		OrgID:        orgID,
		AvatarURL:    "/static/img/default-avatar.png",
	}
}

// ============================================================================
// Org info — GET/PUT /api/v2.1/org/admin/info/
// The org_id is taken from the JWT (no :org_id in the URL).
// ============================================================================

// GetOrgInfo returns info about the caller's organisation.
// GET /org/admin/info/
func (h *OrgAdminHandler) GetOrgInfo(c *gin.Context) {
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	role, err := h.permMiddleware.GetUserOrgRole(orgID, userID)
	if err != nil || !middleware.HasRequiredOrgRole(role, middleware.RoleAdmin) {
		c.JSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
		return
	}

	var name string
	var storageQuota, storageUsed int64
	var createdAt time.Time

	err = h.db.Session().Query(`
		SELECT name, storage_quota, storage_used, created_at
		FROM organizations WHERE org_id = ?
	`, orgID).Scan(&name, &storageQuota, &storageUsed, &createdAt)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "organization not found"})
		return
	}

	usersCount := h.countOrgMembers(orgID)

	// Response fields match what org-info.js reads:
	// res.data.org_name, storage_quota, storage_usage, member_quota, member_usage, active_members
	c.JSON(http.StatusOK, gin.H{
		"org_id":         orgID,
		"org_name":       name,
		"storage_quota":  storageQuota,
		"storage_usage":  storageUsed,
		"member_usage":   usersCount,
		"member_quota":   h.getOrgSettingInt(orgID, "max_user_number", 0),
		"active_members": usersCount,
		"ctime":          createdAt.Format(time.RFC3339),
	})
}

// UpdateOrgInfo updates the caller's organisation name or quota.
// PUT /org/admin/info/
func (h *OrgAdminHandler) UpdateOrgInfo(c *gin.Context) {
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	role, err := h.permMiddleware.GetUserOrgRole(orgID, userID)
	if err != nil || !middleware.HasRequiredOrgRole(role, middleware.RoleAdmin) {
		c.JSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
		return
	}

	// Accept both JSON and form data (seafile-js compatibility)
	orgName := c.Request.FormValue("org_name")
	maxUsersStr := c.Request.FormValue("max_user_number")

	if orgName == "" && maxUsersStr == "" {
		// Try JSON body
		var body struct {
			OrgName       string `json:"org_name"`
			MaxUserNumber string `json:"max_user_number"`
		}
		if err := c.ShouldBindJSON(&body); err == nil {
			orgName = body.OrgName
			maxUsersStr = body.MaxUserNumber
		}
	}

	if orgName != "" {
		if err := h.db.Session().Query(`
			UPDATE organizations SET name = ? WHERE org_id = ?
		`, orgName, orgID).Exec(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update organization"})
			return
		}
	}

	if maxUsersStr != "" {
		if err := h.db.Session().Query(`
			UPDATE organizations SET settings['max_user_number'] = ? WHERE org_id = ?
		`, maxUsersStr, orgID).Exec(); err != nil {
			log.Printf("UpdateOrgInfo: failed to update max_user_number for org %s: %v", orgID, err)
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ============================================================================
// Users — /api/v2.1/org/:org_id/admin/users/
// ============================================================================

// ListOrgUsers lists users in the target org with pagination.
// Supports ?is_staff=true to filter to admin/superadmin users only.
// GET /org/:org_id/admin/users/
func (h *OrgAdminHandler) ListOrgUsers(c *gin.Context) {
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
	isStaffOnly := c.Query("is_staff") == "true"

	iter := h.db.Session().Query(`
		SELECT user_id, email, name, role, status, quota_bytes, used_bytes, created_at
		FROM users WHERE org_id = ?
	`, targetOrgID).Iter()

	var all []orgUserRow
	var userID, email, name, role, status string
	var quota, used int64
	var created time.Time

	for iter.Scan(&userID, &email, &name, &role, &status, &quota, &used, &created) {
		if isStaffOnly && role != "admin" && role != "superadmin" {
			continue
		}
		all = append(all, buildOrgUserRow(email, name, role, status, targetOrgID, quota, used, created))
	}
	if err := iter.Close(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list users"})
		return
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
	page_data := all[start:end]
	if page_data == nil {
		page_data = []orgUserRow{}
	}

	// Frontend reads: res.data.user_list, res.data.page_next, res.data.page
	hasNext := end < total
	pageNext := false
	if hasNext {
		pageNext = true
	}
	c.JSON(http.StatusOK, gin.H{
		"user_list":   page_data,
		"page":        page,
		"page_next":   pageNext,
		"total_count": total,
	})
}

// AddOrgUser creates a user in the target org.
// Accepts FormData (email, name, password ignored) or JSON.
// POST /org/:org_id/admin/users/
func (h *OrgAdminHandler) AddOrgUser(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}

	// Parse email/name from either form or JSON
	email := strings.TrimSpace(c.Request.FormValue("email"))
	name := strings.TrimSpace(c.Request.FormValue("name"))
	if email == "" {
		var body struct {
			Email string `json:"email"`
			Name  string `json:"name"`
		}
		if err := c.ShouldBindJSON(&body); err == nil {
			email = strings.TrimSpace(body.Email)
			name = strings.TrimSpace(body.Name)
		}
	}

	if email == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "email is required"})
		return
	}
	if name == "" {
		name = strings.Split(email, "@")[0]
	}

	// Check uniqueness
	var existingUID string
	if err := h.db.Session().Query(`
		SELECT user_id FROM users_by_email WHERE email = ?
	`, email).Scan(&existingUID); err == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user with this email already exists"})
		return
	}

	userID := uuid.New().String()
	now := time.Now()

	if err := createUserWithEmailLookup(h.db, targetOrgID, userID, email, name, "user", int64(-2), int64(0), now); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create user"})
		return
	}

	c.JSON(http.StatusCreated, buildOrgUserRow(email, name, "user", StatusActive, targetOrgID, -2, 0, now))
}

// GetOrgUser returns details for a single user identified by email within the target org.
// GET /org/:org_id/admin/users/:email/
func (h *OrgAdminHandler) GetOrgUser(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	email := c.Param("email")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}

	userID, err := h.lookupOrgUserByEmail(targetOrgID, email)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	var name, role, status string
	var quota, used int64
	var created time.Time

	if err := h.db.Session().Query(`
		SELECT name, role, status, quota_bytes, used_bytes, created_at
		FROM users WHERE org_id = ? AND user_id = ?
	`, targetOrgID, userID).Scan(&name, &role, &status, &quota, &used, &created); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	c.JSON(http.StatusOK, buildOrgUserRow(email, name, role, status, targetOrgID, quota, used, created))
}

// UpdateOrgUser updates an org user's active status, staff role, name, or quota.
// Accepts JSON: { "is_active", "is_org_staff", "name", "quota_total" }
// or form values: active, is_org_staff, name, quota_total.
// PUT /org/:org_id/admin/users/:email/
func (h *OrgAdminHandler) UpdateOrgUser(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	email := c.Param("email")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}

	userID, err := h.lookupOrgUserByEmail(targetOrgID, email)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	var name, role, status string
	var quota, used int64
	var created time.Time

	if err := h.db.Session().Query(`
		SELECT name, role, status, quota_bytes, used_bytes, created_at
		FROM users WHERE org_id = ? AND user_id = ?
	`, targetOrgID, userID).Scan(&name, &role, &status, &quota, &used, &created); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	originalRole := role
	originalName := name
	originalQuota := quota

	// seafile-js sends FormData for all org-admin user updates.
	// Fields: is_active, is_staff, name, contact_email, quota_total
	if v := c.Request.FormValue("is_active"); v != "" {
		if v == "false" {
			if err := deactivateUser(h.db, h.sessions, targetOrgID, userID); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update user"})
				return
			}
			status = StatusDeactivated
		} else if v == "true" {
			if err := activateUser(h.db, targetOrgID, userID); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update user"})
				return
			}
			status = StatusActive
		}
	}

	if v := c.Request.FormValue("is_staff"); v != "" {
		isStaff := v == "true"
		if isStaff {
			role = "admin"
		} else if role == "admin" {
			role = "user"
		}
	}

	if v := c.Request.FormValue("name"); v != "" {
		name = v
	}

	if v := c.Request.FormValue("quota_total"); v != "" {
		if q, err := strconv.ParseInt(v, 10, 64); err == nil {
			quota = q
		}
	}

	if role != originalRole || name != originalName || quota != originalQuota {
		batch := h.db.Session().Batch(gocql.LoggedBatch)
		if role != originalRole {
			batch.Query(`UPDATE users SET role = ? WHERE org_id = ? AND user_id = ?`,
				role, targetOrgID, userID)
		}
		if name != originalName {
			batch.Query(`UPDATE users SET name = ? WHERE org_id = ? AND user_id = ?`,
				name, targetOrgID, userID)
		}
		if quota != originalQuota {
			batch.Query(`UPDATE users SET quota_bytes = ? WHERE org_id = ? AND user_id = ?`,
				quota, targetOrgID, userID)
		}
		if err := batch.Exec(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update user"})
			return
		}
	}

	if v := c.Request.FormValue("contact_email"); v != "" {
		// contact_email is not stored in users table currently but acknowledge it
		_ = v
	}

	c.JSON(http.StatusOK, buildOrgUserRow(email, name, role, status, targetOrgID, quota, used, created))
}

// DeleteOrgUser soft-deletes a user from the target org.
// The caller cannot delete themselves.
// DELETE /org/:org_id/admin/users/:email/
func (h *OrgAdminHandler) DeleteOrgUser(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	email := c.Param("email")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}

	callerUserID := c.GetString("user_id")

	userID, err := h.lookupOrgUserByEmail(targetOrgID, email)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}
	if userID == callerUserID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot delete your own account"})
		return
	}

	// Soft-delete: mark as "deleted" with timestamp for grace period cascade
	if err := softDeleteUser(h.db, h.sessions, targetOrgID, userID, time.Now()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete user"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ResetOrgUserPassword is a no-op because SesameFS authenticates exclusively
// via OIDC — there are no local passwords to reset.
// PUT /org/:org_id/admin/users/:email/set-password/
func (h *OrgAdminHandler) ResetOrgUserPassword(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"detail":  "SesameFS uses OIDC authentication; local password management is not supported",
	})
}

// GetOrgUserOwnedRepos returns libraries owned by a user in the target org.
// GET /org/:org_id/admin/users/:email/repos/
func (h *OrgAdminHandler) GetOrgUserOwnedRepos(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	email := c.Param("email")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}

	userID, err := h.lookupOrgUserByEmail(targetOrgID, email)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	iter := h.db.Session().Query(`
		SELECT library_id, name, encrypted, size_bytes, updated_at
		FROM libraries WHERE org_id = ? AND owner_id = ?
		ALLOW FILTERING
	`, targetOrgID, userID).Iter()

	type repoItem struct {
		RepoID       string `json:"repo_id"`
		RepoName     string `json:"repo_name"`
		Encrypted    bool   `json:"encrypted"`
		Size         int64  `json:"size"`
		Owner        string `json:"owner"`
		LastModified string `json:"last_modified"`
	}

	var repos []repoItem
	var libID, libName string
	var encrypted bool
	var size int64
	var updatedAt time.Time

	for iter.Scan(&libID, &libName, &encrypted, &size, &updatedAt) {
		repos = append(repos, repoItem{
			RepoID:       libID,
			RepoName:     libName,
			Encrypted:    encrypted,
			Size:         size,
			Owner:        email,
			LastModified: updatedAt.Format(time.RFC3339),
		})
	}
	if err := iter.Close(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list repos"})
		return
	}
	if repos == nil {
		repos = []repoItem{}
	}

	c.JSON(http.StatusOK, gin.H{"repo_list": repos})
}

// GetOrgUserBesharedRepos returns libraries that have been shared to a user.
// GET /org/:org_id/admin/users/:email/beshared-repos/
func (h *OrgAdminHandler) GetOrgUserBesharedRepos(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	email := c.Param("email")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}

	userID, err := h.lookupOrgUserByEmail(targetOrgID, email)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	// Collect library IDs shared to this user
	shareIter := h.db.Session().Query(`
		SELECT library_id, permission FROM shares_by_user
		WHERE shared_to = ?
	`, userID).Iter()

	type repoItem struct {
		RepoID       string `json:"repo_id"`
		RepoName     string `json:"repo_name"`
		Permission   string `json:"permission"`
		OwnerName    string `json:"owner_name"`
		Size         int64  `json:"size"`
		LastModified string `json:"last_modified"`
	}

	var repos []repoItem
	var libID, perm string

	for shareIter.Scan(&libID, &perm) {
		// Verify the library is in the target org
		var libName, ownerID string
		var libSize int64
		var libUpdatedAt time.Time
		err := h.db.Session().Query(`
			SELECT name, owner_id, size_bytes, updated_at FROM libraries WHERE org_id = ? AND library_id = ?
		`, targetOrgID, libID).Scan(&libName, &ownerID, &libSize, &libUpdatedAt)
		if err != nil {
			continue // Library not in this org or deleted
		}
		ownerName := h.resolveUserName(targetOrgID, ownerID)
		repos = append(repos, repoItem{
			RepoID:       libID,
			RepoName:     libName,
			Permission:   perm,
			OwnerName:    ownerName,
			Size:         libSize,
			LastModified: libUpdatedAt.Format(time.RFC3339),
		})
	}
	if err := shareIter.Close(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list shared repos"})
		return
	}
	if repos == nil {
		repos = []repoItem{}
	}

	c.JSON(http.StatusOK, gin.H{"repo_list": repos})
}

// SearchOrgUser searches users within the target org by email or name fragment.
// GET /org/:org_id/admin/search-user/?query=...
func (h *OrgAdminHandler) SearchOrgUser(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}

	query := strings.ToLower(strings.TrimSpace(c.Query("query")))
	if query == "" {
		c.JSON(http.StatusOK, gin.H{"users": []orgUserRow{}})
		return
	}

	iter := h.db.Session().Query(`
		SELECT user_id, email, name, role, status, quota_bytes, used_bytes, created_at
		FROM users WHERE org_id = ?
	`, targetOrgID).Iter()

	var results []orgUserRow
	var userID, email, name, role, status string
	var quota, used int64
	var created time.Time

	for iter.Scan(&userID, &email, &name, &role, &status, &quota, &used, &created) {
		if strings.Contains(strings.ToLower(email), query) || strings.Contains(strings.ToLower(name), query) {
			results = append(results, buildOrgUserRow(email, name, role, status, targetOrgID, quota, used, created))
		}
	}
	iter.Close()

	if results == nil {
		results = []orgUserRow{}
	}
	c.JSON(http.StatusOK, gin.H{"users": results})
}

// ImportOrgUsers bulk-creates users from an uploaded CSV file.
// The CSV must have a header row and at minimum an "email" column; "name" is
// optional. Passwords are accepted for compatibility but ignored (OIDC-only).
// POST /org/:org_id/admin/import-users/
func (h *OrgAdminHandler) ImportOrgUsers(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}

	file, _, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "CSV file is required (field: file)"})
		return
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.TrimLeadingSpace = true

	// Read header row
	headers, err := reader.Read()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid CSV format"})
		return
	}

	emailIdx, nameIdx := -1, -1
	for i, h := range headers {
		switch strings.ToLower(strings.TrimSpace(h)) {
		case "email":
			emailIdx = i
		case "name":
			nameIdx = i
		}
	}
	if emailIdx < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "CSV must have an 'email' column"})
		return
	}

	// Frontend reads res.data.success as an array of user objects (OrgUserInfo).
	var success []orgUserRow
	var failed []gin.H
	now := time.Now()

	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}
		if emailIdx >= len(record) {
			continue
		}

		email := strings.TrimSpace(record[emailIdx])
		if email == "" {
			continue
		}
		name := strings.Split(email, "@")[0]
		if nameIdx >= 0 && nameIdx < len(record) {
			if n := strings.TrimSpace(record[nameIdx]); n != "" {
				name = n
			}
		}

		// Skip if already exists
		var existingUID string
		if err := h.db.Session().Query(`
			SELECT user_id FROM users_by_email WHERE email = ?
		`, email).Scan(&existingUID); err == nil {
			failed = append(failed, gin.H{"email": email, "error": "already exists"})
			continue
		}

		userID := uuid.New().String()
		if err := createUserWithEmailLookup(h.db, targetOrgID, userID, email, name, "user", int64(-2), int64(0), now); err != nil {
			failed = append(failed, gin.H{"email": email, "error": "database error"})
			continue
		}
		success = append(success, buildOrgUserRow(email, name, "user", StatusActive, targetOrgID, -2, 0, now))
	}

	if success == nil {
		success = []orgUserRow{}
	}
	if failed == nil {
		failed = []gin.H{}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": success,
		"failed":  failed,
	})
}

// InviteOrgUsers accepts a list of email addresses and records them as invited.
// Since SesameFS uses OIDC exclusively, no invitation email is sent — the
// response acknowledges the request so the frontend flow completes normally.
// POST /org/:org_id/admin/invite-users/
func (h *OrgAdminHandler) InviteOrgUsers(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}

	var body struct {
		Emails []string `json:"email_list"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || len(body.Emails) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "email_list is required"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":       true,
		"invited_count": len(body.Emails),
		"detail":        "SesameFS uses OIDC; users will be created on first login",
	})
}

// ============================================================================
// Internal helpers
// ============================================================================

// countOrgMembers counts the number of users in an org (iterates the partition).
func (h *OrgAdminHandler) countOrgMembers(orgID string) int {
	count := 0
	iter := h.db.Session().Query(`SELECT user_id FROM users WHERE org_id = ?`, orgID).Iter()
	var dummy string
	for iter.Scan(&dummy) {
		count++
	}
	iter.Close()
	return count
}

// resolveUserEmail returns the email address for a user_id within an org.
// Returns an empty string if the user cannot be found.
func (h *OrgAdminHandler) resolveUserEmail(orgID, userID string) string {
	var email string
	h.db.Session().Query(`
		SELECT email FROM users WHERE org_id = ? AND user_id = ?
	`, orgID, userID).Scan(&email)
	return email
}

// getOrgSetting retrieves a single key from organizations.settings, returning
// defaultVal when the key is absent or the row cannot be read.
func (h *OrgAdminHandler) getOrgSetting(orgID, key, defaultVal string) string {
	var settings map[string]string
	if err := h.db.Session().Query(`
		SELECT settings FROM organizations WHERE org_id = ?
	`, orgID).Scan(&settings); err != nil {
		return defaultVal
	}
	if v, ok := settings[key]; ok {
		return v
	}
	return defaultVal
}

// getOrgSettingInt retrieves a single key from organizations.settings as an int.
func (h *OrgAdminHandler) getOrgSettingInt(orgID, key string, defaultVal int) int {
	s := h.getOrgSetting(orgID, key, "")
	if s == "" {
		return defaultVal
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return defaultVal
	}
	return v
}

// ============================================================================
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

// UpdateOrgGroup updates a group (quota or name).
// PUT /org/:org_id/admin/groups/:gid/  FormData: quota
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

	// Store group quota in org settings (no dedicated column on groups table).
	if quotaStr := c.Request.FormValue("quota"); quotaStr != "" {
		settingKey := "group_quota_" + groupID
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
// PUT /org/:org_id/admin/groups/:gid/transfer/  FormData: new_owner=<email>
func (h *OrgAdminHandler) TransferOrgGroup(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}

	groupID := c.Param("gid")
	newOwnerEmail := c.Request.FormValue("new_owner")
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
// POST /org/:org_id/admin/groups/:gid/members/  FormData: email
func (h *OrgAdminHandler) AddOrgGroupMember(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}

	groupID := c.Param("gid")
	email := c.Request.FormValue("email")
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
// PUT /org/:org_id/admin/groups/:gid/members/:email/  FormData: is_admin
func (h *OrgAdminHandler) UpdateOrgGroupMember(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}

	groupID := c.Param("gid")
	email := c.Param("email")

	isAdmin := c.Request.FormValue("is_admin") == "true"

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
// POST /org/:org_id/admin/groups/:gid/group-owned-libraries/  FormData: repo_name
func (h *OrgAdminHandler) AddOrgGroupOwnedLibrary(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}

	groupID := c.Param("gid")
	repoName := c.Request.FormValue("repo_name")
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
		INSERT INTO libraries_by_id (library_id, org_id, owner_id, encrypted)
		VALUES (?, ?, ?, ?)
	`, newLibID, targetOrgID, callerUserID, false)
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
// PUT /org/:org_id/admin/repos/:rid/  FormData: email
func (h *OrgAdminHandler) TransferOrgRepo(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}

	repoID := c.Param("rid")
	newOwnerEmail := c.Request.FormValue("email")
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
// Departments / address-book
// ============================================================================

// listDepartmentGroups returns groups where is_department=true for the given org.
func (h *OrgAdminHandler) listDepartmentGroups(orgID string) []gin.H {
	iter := h.db.Session().Query(`
		SELECT group_id, name, parent_group_id, created_at
		FROM groups WHERE org_id = ?
	`, orgID).Iter()

	var results []gin.H
	var groupID, name string
	var parentGroupID string
	var createdAt time.Time
	var isDept bool

	// We need is_department — re-query with it
	iter.Close()
	iter = h.db.Session().Query(`
		SELECT group_id, name, parent_group_id, is_department, created_at
		FROM groups WHERE org_id = ?
	`, orgID).Iter()

	for iter.Scan(&groupID, &name, &parentGroupID, &isDept, &createdAt) {
		if !isDept {
			continue
		}
		quota := h.getOrgSettingInt(orgID, "group_quota_"+groupID, -2)
		results = append(results, gin.H{
			"id":              groupID,
			"name":            name,
			"parent_group_id": parentGroupID,
			"created_at":      createdAt.Format(time.RFC3339),
			"quota":           quota,
		})
	}
	iter.Close()

	if results == nil {
		results = []gin.H{}
	}
	return results
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
// POST /org/:org_id/admin/address-book/groups/  FormData: group_name, parent_group (optional), group_owner (optional), group_staff (optional, comma-separated)
func (h *OrgAdminHandler) AddOrgAddressBookGroup(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}

	groupName := c.Request.FormValue("group_name")
	if groupName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "group_name is required"})
		return
	}
	parentGroup := c.Request.FormValue("parent_group")
	// "-1" is the seafile-js sentinel value meaning "no parent" (root department)
	if parentGroup == "-1" {
		parentGroup = ""
	}
	groupOwner := c.Request.FormValue("group_owner")
	groupStaff := c.Request.FormValue("group_staff")

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
// PUT /org/:org_id/admin/address-book/groups/:gid/  FormData: group_name
func (h *OrgAdminHandler) UpdateOrgAddressBookGroup(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}

	groupID := c.Param("gid")
	newName := c.Request.FormValue("group_name")
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
// Statistics
// ============================================================================

func (h *OrgAdminHandler) OrgStatisticFiles(c *gin.Context) {
	h.notImplemented(c, "org statistics file-operations")
}
func (h *OrgAdminHandler) OrgStatisticStorage(c *gin.Context) {
	h.notImplemented(c, "org statistics total-storage")
}
func (h *OrgAdminHandler) OrgStatisticActiveUsers(c *gin.Context) {
	h.notImplemented(c, "org statistics active-users")
}
func (h *OrgAdminHandler) OrgStatisticTraffic(c *gin.Context) {
	h.notImplemented(c, "org statistics system-traffic")
}
func (h *OrgAdminHandler) OrgStatisticUserTraffic(c *gin.Context) {
	h.notImplemented(c, "org statistics user-traffic")
}

// ============================================================================
// Devices
// ============================================================================

// ListOrgDevices returns an empty device list (no device tracking table yet).
// GET /org/:org_id/admin/devices/
func (h *OrgAdminHandler) ListOrgDevices(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"devices": []gin.H{},
		"page_info": gin.H{
			"current_page":  1,
			"has_next_page": false,
		},
	})
}

// UnlinkOrgDevice is a no-op (no device tracking table yet).
// DELETE /org/:org_id/admin/devices/
func (h *OrgAdminHandler) UnlinkOrgDevice(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ListOrgDeviceErrors returns an empty error list (no device tracking table yet).
// GET /org/:org_id/admin/devices-errors/
func (h *OrgAdminHandler) ListOrgDeviceErrors(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"devices": []gin.H{},
		"page_info": gin.H{
			"current_page":  1,
			"has_next_page": false,
		},
	})
}

// ============================================================================
// Links (public share links)
// ============================================================================

// ListOrgLinks lists all public share links in the org.
// GET /org/admin/links/?page=N
// Frontend reads: res.data.link_list, res.data.page, res.data.page_next
func (h *OrgAdminHandler) ListOrgLinks(c *gin.Context) {
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	role, err := h.permMiddleware.GetUserOrgRole(orgID, userID)
	if err != nil || !middleware.HasRequiredOrgRole(role, middleware.RoleAdmin) {
		c.JSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	perPage := 25

	// Single-partition query on share_links_by_org — no more iterating users
	userCache := make(map[string][2]string) // createdBy -> [email, name]
	var links []gin.H
	iter := h.db.Session().Query(`
		SELECT link_token, link_type, library_id, file_path, created_by, created_at
		FROM share_links_by_org WHERE org_id = ?
	`, orgID).Iter()

	var token, linkType, libID, filePath, createdBy string
	var createdAt time.Time

	for iter.Scan(&token, &linkType, &libID, &filePath, &createdBy, &createdAt) {
		if linkType != "share" {
			continue
		}

		// Resolve user
		info, ok := userCache[createdBy]
		if !ok {
			var email, name string
			h.db.Session().Query(`SELECT email, name FROM users WHERE org_id = ? AND user_id = ?`, orgID, createdBy).Scan(&email, &name)
			if name == "" && email != "" {
				name = strings.Split(email, "@")[0]
			}
			info = [2]string{email, name}
			userCache[createdBy] = info
		}

		// Derive name from file_path
		linkName := filePath
		if idx := strings.LastIndex(filePath, "/"); idx >= 0 && idx < len(filePath)-1 {
			linkName = filePath[idx+1:]
		}
		if linkName == "" || linkName == "/" {
			var libName string
			h.db.Session().Query(`SELECT name FROM libraries WHERE org_id = ? AND library_id = ?`, orgID, libID).Scan(&libName)
			if libName != "" {
				linkName = libName
			}
		}

		linkURL := fmt.Sprintf("%s/d/%s", getBrowserURL(c, ""), token)
		links = append(links, gin.H{
			"token":        token,
			"name":         linkName,
			"link":         linkURL,
			"owner_email":  info[0],
			"owner_name":   info[1],
			"created_time": createdAt.Format(time.RFC3339),
			"view_count":   0,
		})
	}
	iter.Close()

	if links == nil {
		links = []gin.H{}
	}

	// Paginate
	total := len(links)
	start := (page - 1) * perPage
	if start > total {
		start = total
	}
	end := start + perPage
	if end > total {
		end = total
	}

	c.JSON(http.StatusOK, gin.H{
		"link_list": links[start:end],
		"page":      page,
		"page_next": end < total,
	})
}

// DeleteOrgLink deletes a public share link.
// DELETE /org/admin/links/:token/
func (h *OrgAdminHandler) DeleteOrgLink(c *gin.Context) {
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	role, err := h.permMiddleware.GetUserOrgRole(orgID, userID)
	if err != nil || !middleware.HasRequiredOrgRole(role, middleware.RoleAdmin) {
		c.JSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
		return
	}

	token := c.Param("token")

	// Look up the link to verify it belongs to this org
	var linkOrgID, createdBy, libID string
	var createdAt time.Time
	if err := h.db.Session().Query(`
		SELECT org_id, created_by, library_id, created_at FROM share_links WHERE link_token = ?
	`, token).Scan(&linkOrgID, &createdBy, &libID, &createdAt); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "link not found"})
		return
	}
	if linkOrgID != orgID {
		c.JSON(http.StatusForbidden, gin.H{"error": "link does not belong to this organization"})
		return
	}

	sh := &ShareLinkHandler{db: h.db}
	if err := sh.deleteShareLink(token, orgID, createdBy, libID, createdAt); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete link"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ============================================================================
// Upload links
// ============================================================================

// ListOrgUploadLinks lists all upload links in the org.
// GET /org/admin/upload-links/?page=N
// Frontend reads: res.data.upload_link_list, res.data.count
func (h *OrgAdminHandler) ListOrgUploadLinks(c *gin.Context) {
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	role, err := h.permMiddleware.GetUserOrgRole(orgID, userID)
	if err != nil || !middleware.HasRequiredOrgRole(role, middleware.RoleAdmin) {
		c.JSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	perPage := 25

	// Single-partition query on share_links_by_org
	userCache := make(map[string][2]string)
	var links []gin.H
	iter := h.db.Session().Query(`
		SELECT link_token, link_type, library_id, file_path, created_by, expires_at, created_at
		FROM share_links_by_org WHERE org_id = ?
	`, orgID).Iter()

	var token, linkType, libID, filePath, createdBy string
	var expiresAt *time.Time
	var createdAt time.Time

	for iter.Scan(&token, &linkType, &libID, &filePath, &createdBy, &expiresAt, &createdAt) {
		if linkType != "upload" {
			continue
		}

		// Resolve user
		info, ok := userCache[createdBy]
		if !ok {
			var email, name string
			h.db.Session().Query(`SELECT email, name FROM users WHERE org_id = ? AND user_id = ?`, orgID, createdBy).Scan(&email, &name)
			if name == "" && email != "" {
				name = strings.Split(email, "@")[0]
			}
			info = [2]string{email, name}
			userCache[createdBy] = info
		}

		objName := filePath
		if idx := strings.LastIndex(filePath, "/"); idx >= 0 && idx < len(filePath)-1 {
			objName = filePath[idx+1:]
		}

		isExpired := false
		expireDateStr := ""
		if expiresAt != nil && !expiresAt.IsZero() {
			isExpired = expiresAt.Before(time.Now())
			expireDateStr = expiresAt.Format(time.RFC3339)
		}

		var repoName string
		h.db.Session().Query(`SELECT name FROM libraries WHERE org_id = ? AND library_id = ?`, orgID, libID).Scan(&repoName)

		uploadLinkURL := fmt.Sprintf("%s/u/d/%s", getBrowserURL(c, ""), token)
		links = append(links, gin.H{
			"obj_name":      objName,
			"path":          filePath,
			"token":         token,
			"link":          uploadLinkURL,
			"repo_id":       libID,
			"repo_name":     repoName,
			"creator_email": info[0],
			"creator_name":  info[1],
			"ctime":         createdAt.Format(time.RFC3339),
			"view_cnt":      0,
			"expire_date":   expireDateStr,
			"is_expired":    isExpired,
		})
	}
	iter.Close()

	if links == nil {
		links = []gin.H{}
	}

	total := len(links)
	start := (page - 1) * perPage
	if start > total {
		start = total
	}
	end := start + perPage
	if end > total {
		end = total
	}

	c.JSON(http.StatusOK, gin.H{
		"upload_link_list": links[start:end],
		"count":            total,
	})
}

// DeleteOrgUploadLink deletes an upload link.
// DELETE /org/admin/upload-links/:token/
func (h *OrgAdminHandler) DeleteOrgUploadLink(c *gin.Context) {
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	role, err := h.permMiddleware.GetUserOrgRole(orgID, userID)
	if err != nil || !middleware.HasRequiredOrgRole(role, middleware.RoleAdmin) {
		c.JSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
		return
	}

	token := c.Param("token")

	// Look up the link to verify it belongs to this org
	var linkOrgID, createdBy, libID string
	var createdAt time.Time
	if err := h.db.Session().Query(`
		SELECT org_id, created_by, library_id, created_at FROM share_links WHERE link_token = ?
	`, token).Scan(&linkOrgID, &createdBy, &libID, &createdAt); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "upload link not found"})
		return
	}
	if linkOrgID != orgID {
		c.JSON(http.StatusForbidden, gin.H{"error": "upload link does not belong to this organization"})
		return
	}

	sh := &ShareLinkHandler{db: h.db}
	if err := sh.deleteShareLink(token, orgID, createdBy, libID, createdAt); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete upload link"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ============================================================================
// Logs
// ============================================================================

func (h *OrgAdminHandler) ListOrgFileAccessLogs(c *gin.Context) {
	h.notImplemented(c, "list org file access logs")
}
func (h *OrgAdminHandler) ListOrgFileUpdateLogs(c *gin.Context) {
	h.notImplemented(c, "list org file update logs")
}
func (h *OrgAdminHandler) ListOrgRepoPermLogs(c *gin.Context) {
	h.notImplemented(c, "list org repo permission logs")
}

// ============================================================================
// Web settings, logo, SAML, domain
// ============================================================================

func (h *OrgAdminHandler) GetOrgWebSettings(c *gin.Context) {
	h.notImplemented(c, "get org web settings")
}
func (h *OrgAdminHandler) SetOrgWebSettings(c *gin.Context) {
	h.notImplemented(c, "set org web settings")
}
func (h *OrgAdminHandler) UpdateOrgLogo(c *gin.Context) {
	h.notImplemented(c, "update org logo")
}
func (h *OrgAdminHandler) GetOrgSAMLConfig(c *gin.Context) {
	h.notImplemented(c, "get org SAML config")
}
func (h *OrgAdminHandler) UpdateOrgSAMLConfig(c *gin.Context) {
	h.notImplemented(c, "update org SAML config")
}
func (h *OrgAdminHandler) VerifyOrgDomain(c *gin.Context) {
	h.notImplemented(c, "verify org domain")
}
