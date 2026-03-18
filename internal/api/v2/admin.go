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

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// AdminHandler handles platform admin API requests
type AdminHandler struct {
	db             *db.DB
	config         *config.Config
	permMiddleware *middleware.PermissionMiddleware
	tokenCreator   TokenCreator
	serverURL      string
}

// NewAdminHandler creates a new AdminHandler
func NewAdminHandler(database *db.DB, cfg *config.Config, perm *middleware.PermissionMiddleware, tokenCreator TokenCreator, serverURL string) *AdminHandler {
	return &AdminHandler{
		db:             database,
		config:         cfg,
		permMiddleware: perm,
		tokenCreator:   tokenCreator,
		serverURL:      serverURL,
	}
}

// RegisterAdminRoutes registers admin API routes under the given router group.
// All org CRUD endpoints require superadmin. User management allows tenant admin for own org.
func RegisterAdminRoutes(rg *gin.RouterGroup, database *db.DB, cfg *config.Config, perm *middleware.PermissionMiddleware, tokenCreator TokenCreator, serverURL string) {
	h := NewAdminHandler(database, cfg, perm, tokenCreator, serverURL)

	admin := rg.Group("/admin")
	{
		// Organization management - read operations (admin or above, checked in handler)
		admin.GET("/organizations/", h.ListOrganizations)
		admin.GET("/organizations", h.ListOrganizations)
		admin.GET("/organizations/:org_id/", h.GetOrganization)
		admin.GET("/organizations/:org_id", h.GetOrganization)

		// Organization management - write operations (superadmin only)
		superadminOnly := admin.Group("", perm.RequireSuperAdmin())
		{
			superadminOnly.POST("/organizations/", h.CreateOrganization)
			superadminOnly.POST("/organizations", h.CreateOrganization)
			superadminOnly.PUT("/organizations/:org_id/", h.UpdateOrganization)
			superadminOnly.PUT("/organizations/:org_id", h.UpdateOrganization)
			superadminOnly.DELETE("/organizations/:org_id/", h.DeactivateOrganization)
			superadminOnly.DELETE("/organizations/:org_id", h.DeactivateOrganization)
		}

		// User listing per org (superadmin or tenant admin for own org)
		admin.GET("/organizations/:org_id/users/", h.ListOrgUsers)
		admin.GET("/organizations/:org_id/users", h.ListOrgUsers)
		admin.POST("/organizations/:org_id/users/", h.AdminAddOrgUser)
		admin.POST("/organizations/:org_id/users", h.AdminAddOrgUser)
		admin.PUT("/organizations/:org_id/users/:email/", h.AdminUpdateOrgUser)
		admin.PUT("/organizations/:org_id/users/:email", h.AdminUpdateOrgUser)
		admin.DELETE("/organizations/:org_id/users/:email/", h.AdminDeleteOrgUser)
		admin.DELETE("/organizations/:org_id/users/:email", h.AdminDeleteOrgUser)
		admin.GET("/organizations/:org_id/groups/", h.AdminListOrgGroups)
		admin.GET("/organizations/:org_id/groups", h.AdminListOrgGroups)

		// Search organizations
		admin.GET("/search-organization/", h.AdminSearchOrganizations)
		admin.GET("/search-organization", h.AdminSearchOrganizations)

		// User management — register a middleware on the /users group that
		// intercepts all requests and dispatches based on path.
		admin.Any("/users", h.adminUsersHandler)
		admin.Any("/users/*path", h.adminUsersHandler)

		// Admin group management (admin or above)
		admin.GET("/groups/", h.ListAllGroups)
		admin.GET("/groups", h.ListAllGroups)
		admin.POST("/groups/", h.AdminCreateGroup)
		admin.POST("/groups", h.AdminCreateGroup)
		admin.DELETE("/groups/:group_id/", h.AdminDeleteGroup)
		admin.DELETE("/groups/:group_id", h.AdminDeleteGroup)
		admin.PUT("/groups/:group_id/", h.AdminTransferGroup)
		admin.PUT("/groups/:group_id", h.AdminTransferGroup)
		admin.GET("/groups/:group_id/members/", h.AdminListGroupMembers)
		admin.GET("/groups/:group_id/members", h.AdminListGroupMembers)
		admin.POST("/groups/:group_id/members/", h.AdminAddGroupMember)
		admin.POST("/groups/:group_id/members", h.AdminAddGroupMember)
		admin.DELETE("/groups/:group_id/members/:email/", h.AdminRemoveGroupMember)
		admin.DELETE("/groups/:group_id/members/:email", h.AdminRemoveGroupMember)
		admin.GET("/groups/:group_id/libraries/", h.AdminListGroupLibraries)
		admin.GET("/groups/:group_id/libraries", h.AdminListGroupLibraries)
		admin.GET("/search-group/", h.SearchGroups)
		admin.GET("/search-group", h.SearchGroups)

		// Admin user search & admin listing
		admin.GET("/search-user/", h.SearchUsers)
		admin.GET("/search-user", h.SearchUsers)
		admin.GET("/admins/", h.ListAdminUsers)
		admin.GET("/admins", h.ListAdminUsers)
		admin.POST("/admins/", h.BatchAddAdmins)
		admin.POST("/admins", h.BatchAddAdmins)

		// Admin library management
		admin.GET("/libraries/", h.AdminListAllLibraries)
		admin.GET("/libraries", h.AdminListAllLibraries)
		admin.POST("/libraries/", h.AdminCreateLibrary)
		admin.POST("/libraries", h.AdminCreateLibrary)
		admin.GET("/libraries/:library_id/", h.AdminGetLibrary)
		admin.GET("/libraries/:library_id", h.AdminGetLibrary)
		admin.DELETE("/libraries/:library_id/", h.AdminDeleteLibrary)
		admin.DELETE("/libraries/:library_id", h.AdminDeleteLibrary)
		admin.PUT("/libraries/:library_id/transfer/", h.AdminTransferLibrary)
		admin.PUT("/libraries/:library_id/transfer", h.AdminTransferLibrary)
		admin.GET("/libraries/:library_id/dirents/", h.AdminListDirents)
		admin.GET("/libraries/:library_id/dirents", h.AdminListDirents)
		admin.GET("/libraries/:library_id/download-link/", h.AdminGetDownloadLink)
		admin.GET("/libraries/:library_id/download-link", h.AdminGetDownloadLink)
		admin.GET("/libraries/:library_id/history-setting/", h.AdminGetHistorySetting)
		admin.GET("/libraries/:library_id/history-setting", h.AdminGetHistorySetting)
		admin.PUT("/libraries/:library_id/history-setting/", h.AdminUpdateHistorySetting)
		admin.PUT("/libraries/:library_id/history-setting", h.AdminUpdateHistorySetting)
		admin.GET("/libraries/:library_id/shared-items/", h.AdminListSharedItems)
		admin.GET("/libraries/:library_id/shared-items", h.AdminListSharedItems)
		admin.GET("/search-libraries/", h.AdminSearchLibraries)
		admin.GET("/search-libraries", h.AdminSearchLibraries)

		// Admin trash library management
		admin.GET("/trash-libraries/", h.AdminListTrashLibraries)
		admin.GET("/trash-libraries", h.AdminListTrashLibraries)
		admin.DELETE("/trash-libraries/", h.AdminCleanTrashLibraries)
		admin.DELETE("/trash-libraries", h.AdminCleanTrashLibraries)

		// System info
		admin.GET("/sysinfo/", h.AdminGetSysInfo)
		admin.GET("/sysinfo", h.AdminGetSysInfo)

		// License
		admin.POST("/license/", h.AdminUploadLicense)
		admin.POST("/license", h.AdminUploadLicense)

		// Statistics
		admin.GET("/statistics/file-operations/", h.AdminStatisticFiles)
		admin.GET("/statistics/file-operations", h.AdminStatisticFiles)
		admin.GET("/statistics/total-storage/", h.AdminStatisticStorage)
		admin.GET("/statistics/total-storage", h.AdminStatisticStorage)
		admin.GET("/statistics/active-users/", h.AdminStatisticActiveUsers)
		admin.GET("/statistics/active-users", h.AdminStatisticActiveUsers)
		admin.GET("/statistics/system-traffic/", h.AdminStatisticTraffic)
		admin.GET("/statistics/system-traffic", h.AdminStatisticTraffic)

		// Devices
		admin.GET("/devices/", h.AdminListDevices)
		admin.GET("/devices", h.AdminListDevices)
		admin.GET("/device-errors/", h.AdminListDeviceErrors)
		admin.GET("/device-errors", h.AdminListDeviceErrors)
		admin.DELETE("/device-errors/", h.AdminClearDeviceErrors)
		admin.DELETE("/device-errors", h.AdminClearDeviceErrors)

		// Web settings
		admin.GET("/web-settings/", h.AdminGetWebSettings)
		admin.GET("/web-settings", h.AdminGetWebSettings)
		admin.PUT("/web-settings/", h.AdminSetWebSettings)
		admin.PUT("/web-settings", h.AdminSetWebSettings)

		// Logo / Favicon / Login BG
		admin.POST("/logo/", h.AdminUpdateLogo)
		admin.POST("/logo", h.AdminUpdateLogo)
		admin.POST("/favicon/", h.AdminUpdateFavicon)
		admin.POST("/favicon", h.AdminUpdateFavicon)
		admin.POST("/login-background-image/", h.AdminUpdateLoginBG)
		admin.POST("/login-background-image", h.AdminUpdateLoginBG)

		// Logs
		admin.GET("/logs/login-logs/", h.AdminListLoginLogs)
		admin.GET("/logs/login-logs", h.AdminListLoginLogs)
		admin.GET("/logs/file-access-logs/", h.AdminListFileAccessLogs)
		admin.GET("/logs/file-access-logs", h.AdminListFileAccessLogs)
		admin.GET("/logs/file-update-logs/", h.AdminListFileUpdateLogs)
		admin.GET("/logs/file-update-logs", h.AdminListFileUpdateLogs)
		admin.GET("/logs/share-permission-logs/", h.AdminListSharePermissionLogs)
		admin.GET("/logs/share-permission-logs", h.AdminListSharePermissionLogs)
		admin.GET("/admin-logs/", h.AdminListAdminLogs)
		admin.GET("/admin-logs", h.AdminListAdminLogs)
		admin.GET("/admin-login-logs/", h.AdminListAdminLoginLogs)
		admin.GET("/admin-login-logs", h.AdminListAdminLoginLogs)

		// Share links
		admin.GET("/share-links/", h.AdminListShareLinks)
		admin.GET("/share-links", h.AdminListShareLinks)
		admin.DELETE("/share-links/:token/", h.AdminDeleteShareLink)
		admin.DELETE("/share-links/:token", h.AdminDeleteShareLink)

		// Upload links
		admin.GET("/upload-links/", h.AdminListUploadLinks)
		admin.GET("/upload-links", h.AdminListUploadLinks)
		admin.DELETE("/upload-links/:token/", h.AdminDeleteUploadLink)
		admin.DELETE("/upload-links/:token", h.AdminDeleteUploadLink)

		// System notifications
		admin.GET("/sys-notifications/", h.AdminListSysNotifications)
		admin.GET("/sys-notifications", h.AdminListSysNotifications)
		admin.POST("/sys-notifications/", h.AdminAddSysNotification)
		admin.POST("/sys-notifications", h.AdminAddSysNotification)
		admin.PUT("/sys-notifications/:id/", h.AdminUpdateSysNotification)
		admin.PUT("/sys-notifications/:id", h.AdminUpdateSysNotification)
		admin.DELETE("/sys-notifications/:id/", h.AdminDeleteSysNotification)
		admin.DELETE("/sys-notifications/:id", h.AdminDeleteSysNotification)

		// Institutions
		admin.GET("/institutions/", h.AdminListInstitutions)
		admin.GET("/institutions", h.AdminListInstitutions)
		admin.POST("/institutions/", h.AdminAddInstitution)
		admin.POST("/institutions", h.AdminAddInstitution)
		admin.GET("/institutions/:id/", h.AdminGetInstitution)
		admin.GET("/institutions/:id", h.AdminGetInstitution)
		admin.PUT("/institutions/:id/", h.AdminUpdateInstitution)
		admin.PUT("/institutions/:id", h.AdminUpdateInstitution)
		admin.DELETE("/institutions/:id/", h.AdminDeleteInstitution)
		admin.DELETE("/institutions/:id", h.AdminDeleteInstitution)

		// Invitations
		admin.GET("/invitations/", h.AdminListInvitations)
		admin.GET("/invitations", h.AdminListInvitations)
		admin.DELETE("/invitations/:token/", h.AdminDeleteInvitation)
		admin.DELETE("/invitations/:token", h.AdminDeleteInvitation)

		// Group member role update
		admin.PUT("/groups/:group_id/members/:email/", h.AdminUpdateGroupMemberRole)
		admin.PUT("/groups/:group_id/members/:email", h.AdminUpdateGroupMemberRole)

		// Group-owned libraries (department repos)
		admin.POST("/groups/:group_id/group-owned-libraries/", h.AdminAddGroupOwnedLibrary)
		admin.POST("/groups/:group_id/group-owned-libraries", h.AdminAddGroupOwnedLibrary)
		admin.DELETE("/groups/:group_id/group-owned-libraries/:library_id/", h.AdminDeleteGroupOwnedLibrary)
		admin.DELETE("/groups/:group_id/group-owned-libraries/:library_id", h.AdminDeleteGroupOwnedLibrary)

		// Departments / Address-book groups
		admin.GET("/organizations/:org_id/departments/", h.AdminListOrgDepartments)
		admin.GET("/organizations/:org_id/departments", h.AdminListOrgDepartments)
		// NOTE: /admin/address-book/groups routes are registered by RegisterDepartmentRoutes
	}
}

// ListOrganizations returns all organizations (superadmin only, enforced by middleware)
// GET /admin/organizations/
// Response format matches Seahub frontend expectations: org_name, quota, quota_usage, ctime, etc.
func (h *AdminHandler) ListOrganizations(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	iter := h.db.Session().Query(`
		SELECT org_id, name, storage_quota, storage_used, created_at
		FROM organizations
	`).Iter()

	var orgs []gin.H
	var orgID, name string
	var storageQuota, storageUsed int64
	var createdAt time.Time

	for iter.Scan(&orgID, &name, &storageQuota, &storageUsed, &createdAt) {
		usersCount := h.countOrgUsers(orgID)
		creatorEmail, creatorName := h.resolveOrgCreator(orgID)
		orgs = append(orgs, gin.H{
			"org_id":        orgID,
			"org_name":      name,
			"creator_email": creatorEmail,
			"creator_name":  creatorName,
			"role":          "default",
			"quota_usage":   storageUsed,
			"quota":         storageQuota,
			"ctime":         createdAt.Format(time.RFC3339),
			"users_count":   usersCount,
		})
	}
	if err := iter.Close(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list organizations"})
		return
	}

	if orgs == nil {
		orgs = []gin.H{}
	}

	// Support pagination
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	perPage, _ := strconv.Atoi(c.DefaultQuery("per_page", "25"))
	total := len(orgs)
	start := (page - 1) * perPage
	if start > total {
		start = total
	}
	end := start + perPage
	if end > total {
		end = total
	}

	c.JSON(http.StatusOK, gin.H{
		"organizations": orgs[start:end],
		"total_count":   total,
	})
}

// CreateOrganization creates a new organization (superadmin only).
// POST /admin/organizations/
//
// Accepts both JSON and FormData (seafile-js compatibility):
//
//	JSON:     { "name": "Acme", "storage_quota": 1099511627776 }
//	FormData: org_name=Acme&owner_email=alice@acme.com&password=ignored
//
// If owner_email is provided, an admin user is created inside the new org.
// The password field is accepted for API compatibility but is not used —
// this system authenticates exclusively via OIDC.
func (h *AdminHandler) CreateOrganization(c *gin.Context) {
	// ── Parse request (FormData takes priority; JSON as fallback) ──────────
	var orgName, ownerEmail string
	var storageQuota int64

	ct := c.ContentType()
	if strings.Contains(ct, "multipart/form-data") || strings.Contains(ct, "application/x-www-form-urlencoded") {
		// seafile-js sends FormData with org_name / owner_email / password
		orgName = strings.TrimSpace(c.PostForm("org_name"))
		ownerEmail = strings.TrimSpace(c.PostForm("owner_email"))
		// password accepted but ignored (OIDC-only system)
		if q, err := strconv.ParseInt(c.PostForm("storage_quota"), 10, 64); err == nil {
			storageQuota = q
		}
	} else {
		// JSON body
		var body struct {
			Name         string `json:"name"`
			OrgName      string `json:"org_name"`
			StorageQuota int64  `json:"storage_quota"`
			OwnerEmail   string `json:"owner_email"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
			return
		}
		orgName = strings.TrimSpace(body.OrgName)
		if orgName == "" {
			orgName = strings.TrimSpace(body.Name)
		}
		ownerEmail = strings.TrimSpace(body.OwnerEmail)
		storageQuota = body.StorageQuota
	}

	if orgName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "org_name (or name) is required"})
		return
	}
	if storageQuota <= 0 {
		storageQuota = 1099511627776 // 1 TB default
	}

	// ── Validate owner email before creating anything ─────────────────────
	if ownerEmail != "" {
		var existingUID string
		if err := h.db.Session().Query(`
			SELECT user_id FROM users_by_email WHERE email = ?
		`, ownerEmail).Scan(&existingUID); err == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "owner email already exists"})
			return
		}
	}

	// ── Create the organization ────────────────────────────────────────────
	orgID := uuid.New()
	now := time.Now()

	settings := map[string]string{
		"theme":    "default",
		"features": "all",
	}
	storageConfig := map[string]string{
		"default_backend": "s3",
	}

	if err := h.db.Session().Query(`
		INSERT INTO organizations (
			org_id, name, settings, storage_quota, storage_used,
			chunking_polynomial, storage_config, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`,
		orgID.String(), orgName, settings,
		storageQuota, int64(0), int64(17592186044415),
		storageConfig, now,
	).Exec(); err != nil {
		log.Printf("CreateOrganization: failed to insert org %s: %v", orgName, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create organization"})
		return
	}

	// ── Optionally create the owner/admin user ─────────────────────────────
	ownerName := ""
	if ownerEmail != "" {
		ownerName = strings.Split(ownerEmail, "@")[0]
		ownerUserID := uuid.New()

		batch := h.db.Session().Batch(gocql.LoggedBatch)
		batch.Query(`
			INSERT INTO users (org_id, user_id, email, name, role, quota_bytes, used_bytes, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, orgID.String(), ownerUserID.String(), ownerEmail, ownerName, "admin",
			int64(107374182400), int64(0), now)

		batch.Query(`
			INSERT INTO users_by_email (email, user_id, org_id)
			VALUES (?, ?, ?)
		`, ownerEmail, ownerUserID.String(), orgID.String())

		if err := batch.Exec(); err != nil {
			log.Printf("CreateOrganization: failed to create owner user %s in org %s: %v",
				ownerEmail, orgID, err)
		}
	}

	c.JSON(http.StatusCreated, gin.H{
		"org_id":        orgID.String(),
		"org_name":      orgName,
		"creator_email": ownerEmail,
		"creator_name":  ownerName,
		"role":          "default",
		"quota_usage":   int64(0),
		"quota":         storageQuota,
		"ctime":         now.Format(time.RFC3339),
		"users_count": func() int {
			if ownerEmail != "" {
				return 1
			}
			return 0
		}(),
	})
}

// GetOrganization returns details for a single organization (superadmin only)
// GET /admin/organizations/:org_id/
func (h *AdminHandler) GetOrganization(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	orgID := c.Param("org_id")

	var name string
	var storageQuota, storageUsed int64
	var settings map[string]string
	var createdAt time.Time

	err := h.db.Session().Query(`
		SELECT name, storage_quota, storage_used, settings, created_at
		FROM organizations WHERE org_id = ?
	`, orgID).Scan(&name, &storageQuota, &storageUsed, &settings, &createdAt)

	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "organization not found"})
		return
	}

	usersCount := h.countOrgUsers(orgID)
	reposCount := h.countOrgLibraries(orgID)
	groupsCount := h.countOrgGroups(orgID)
	creatorEmail, creatorName := h.resolveOrgCreator(orgID)

	// Extract max_user_number from settings map
	var maxUserNumber int
	if v, ok := settings["max_user_number"]; ok {
		maxUserNumber, _ = strconv.Atoi(v)
	}

	c.JSON(http.StatusOK, gin.H{
		"org_id":          orgID,
		"org_name":        name,
		"creator_email":   creatorEmail,
		"creator_name":    creatorName,
		"role":            "default",
		"quota_usage":     storageUsed,
		"quota":           storageQuota,
		"ctime":           createdAt.Format(time.RFC3339),
		"users_count":     usersCount,
		"repos_count":     reposCount,
		"groups_count":    groupsCount,
		"max_user_number": maxUserNumber,
	})
}

// UpdateOrganization updates an organization (superadmin only)
// PUT /admin/organizations/:org_id/
func (h *AdminHandler) UpdateOrganization(c *gin.Context) {
	orgID := c.Param("org_id")

	var req struct {
		Name         *string `json:"name"`
		StorageQuota *int64  `json:"storage_quota"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	// Verify org exists
	var existingName string
	err := h.db.Session().Query(`
		SELECT name FROM organizations WHERE org_id = ?
	`, orgID).Scan(&existingName)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "organization not found"})
		return
	}

	if req.Name != nil {
		if err := h.db.Session().Query(`
			UPDATE organizations SET name = ? WHERE org_id = ?
		`, *req.Name, orgID).Exec(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update organization"})
			return
		}
	}

	if req.StorageQuota != nil {
		if err := h.db.Session().Query(`
			UPDATE organizations SET storage_quota = ? WHERE org_id = ?
		`, *req.StorageQuota, orgID).Exec(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update organization"})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// DeactivateOrganization deactivates an organization (superadmin only)
// DELETE /admin/organizations/:org_id/
func (h *AdminHandler) DeactivateOrganization(c *gin.Context) {
	orgID := c.Param("org_id")

	// Don't allow deactivating the platform org
	if orgID == middleware.PlatformOrgID {
		c.JSON(http.StatusForbidden, gin.H{"error": "cannot deactivate platform organization"})
		return
	}

	// Verify org exists
	var name string
	err := h.db.Session().Query(`
		SELECT name FROM organizations WHERE org_id = ?
	`, orgID).Scan(&name)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "organization not found"})
		return
	}

	// Mark org as deactivated by setting a setting
	if err := h.db.Session().Query(`
		UPDATE organizations SET settings['status'] = ? WHERE org_id = ?
	`, "deactivated", orgID).Exec(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to deactivate organization"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ListOrgUsers lists users in an organization.
// Superadmin can list any org. Tenant admin can list their own org.
// GET /admin/organizations/:org_id/users/
func (h *AdminHandler) ListOrgUsers(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")

	// Check permissions: superadmin can access any org, admin can access own org
	if callerOrgID != middleware.PlatformOrgID {
		// Not a platform user — must be admin of the target org
		if callerOrgID != targetOrgID {
			c.JSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
			c.Abort()
			return
		}
		role, err := h.permMiddleware.GetUserOrgRole(callerOrgID, callerUserID)
		if err != nil || !isAdminOrAbove(role) {
			c.JSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
			c.Abort()
			return
		}
	} else {
		// Platform user — must be superadmin
		role, err := h.permMiddleware.GetUserOrgRole(callerOrgID, callerUserID)
		if err != nil || role != middleware.RoleSuperAdmin {
			c.JSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
			c.Abort()
			return
		}
	}

	iter := h.db.Session().Query(`
		SELECT user_id, email, name, role, quota_bytes, used_bytes, created_at
		FROM users WHERE org_id = ?
	`, targetOrgID).Iter()

	var users []gin.H
	var userID, email, name, role string
	var quotaBytes, usedBytes int64
	var createdAt time.Time

	for iter.Scan(&userID, &email, &name, &role, &quotaBytes, &usedBytes, &createdAt) {
		isActive := role != "deactivated"
		isOrgStaff := role == "admin" || role == "superadmin"
		users = append(users, gin.H{
			"email":        email,
			"name":         name,
			"active":       isActive,
			"is_org_staff": isOrgStaff,
			"quota_usage":  usedBytes,
			"quota_total":  quotaBytes,
			"create_time":  createdAt.Format(time.RFC3339),
			"last_login":   "",
			"org_id":       targetOrgID,
		})
	}
	if err := iter.Close(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list users"})
		return
	}

	if users == nil {
		users = []gin.H{}
	}

	c.JSON(http.StatusOK, gin.H{"users": users})
}

// adminUsersHandler dispatches /admin/users and /admin/users/* requests.
// Gin's httprouter can't register both /users/ (static) and /users/:param
// at the same level, so we use Any() + wildcard and dispatch manually.
func (h *AdminHandler) adminUsersHandler(c *gin.Context) {
	path := strings.Trim(c.Param("path"), "/")

	// Check for sub-resource paths like :email/share-links, :email/upload-links, :email/groups
	if parts := strings.SplitN(path, "/", 2); len(parts) == 2 {
		email := parts[0]
		subResource := parts[1]
		c.Set("resolved_user_param", email)

		switch c.Request.Method {
		case "GET":
			switch {
			case strings.HasPrefix(subResource, "share-links"):
				h.AdminListUserShareLinks(c)
			case strings.HasPrefix(subResource, "upload-links"):
				h.AdminListUserUploadLinks(c)
			case strings.HasPrefix(subResource, "groups"):
				h.AdminListUserGroups(c)
			default:
				// Single user get (e.g., /users/uuid-with-slashes — shouldn't happen but handle)
				h.GetUser(c)
			}
		case "PUT":
			h.UpdateUser(c)
		case "DELETE":
			h.DeactivateUser(c)
		default:
			c.JSON(http.StatusMethodNotAllowed, gin.H{"error": "method not allowed"})
		}
		return
	}

	switch c.Request.Method {
	case "GET":
		if path == "" {
			h.ListAllUsers(c)
		} else {
			c.Set("resolved_user_param", path)
			h.GetUser(c)
		}
	case "POST":
		if path == "" {
			h.AdminCreateUser(c)
		} else {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		}
	case "PUT":
		if path == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "user identifier required"})
		} else {
			c.Set("resolved_user_param", path)
			h.UpdateUser(c)
		}
	case "DELETE":
		if path == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "user identifier required"})
		} else {
			c.Set("resolved_user_param", path)
			h.DeactivateUser(c)
		}
	default:
		c.JSON(http.StatusMethodNotAllowed, gin.H{"error": "method not allowed"})
	}
}

// getResolvedUserParam returns the user identifier from either the wildcard
// router's resolved param or the legacy :user_id param.
func (h *AdminHandler) getResolvedUserParam(c *gin.Context) string {
	if v, exists := c.Get("resolved_user_param"); exists {
		return v.(string)
	}
	return c.Param("user_id")
}

// GetUser returns details for a single user.
// GET /admin/users/:user_id/
// If :user_id contains an @ sign, it's treated as an email lookup (seafile-js compatible).
func (h *AdminHandler) GetUser(c *gin.Context) {
	targetUserID := h.getResolvedUserParam(c)

	// Dispatch to email-based handler if the param looks like an email
	if strings.Contains(targetUserID, "@") {
		h.GetUserByEmail(c, targetUserID)
		return
	}

	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")

	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	// Try to find the user - we need their org_id
	var email, name, role, userOrgID string
	var quotaBytes, usedBytes int64
	var createdAt time.Time

	if callerOrgID == middleware.PlatformOrgID {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "use /admin/organizations/:org_id/users/ to list users by org"})
		return
	}

	// Tenant admin: look up in own org
	err := h.db.Session().Query(`
		SELECT email, name, role, quota_bytes, used_bytes, created_at
		FROM users WHERE org_id = ? AND user_id = ?
	`, callerOrgID, targetUserID).Scan(&email, &name, &role, &quotaBytes, &usedBytes, &createdAt)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}
	userOrgID = callerOrgID

	c.JSON(http.StatusOK, gin.H{
		"user_id":     targetUserID,
		"org_id":      userOrgID,
		"email":       email,
		"name":        name,
		"role":        role,
		"quota_bytes": quotaBytes,
		"used_bytes":  usedBytes,
		"created_at":  createdAt,
	})
}

// UpdateUser updates a user's role, status, or quota.
// PUT /admin/users/:user_id/
// If :user_id contains an @ sign, it's treated as an email lookup (seafile-js compatible).
func (h *AdminHandler) UpdateUser(c *gin.Context) {
	targetUserID := h.getResolvedUserParam(c)

	// Dispatch to email-based handler if the param looks like an email
	if strings.Contains(targetUserID, "@") {
		h.UpdateUserByEmail(c, targetUserID)
		return
	}

	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")

	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	var req struct {
		Role       *string `json:"role"`
		QuotaBytes *int64  `json:"quota_bytes"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	// Tenant admin can only update users in their own org
	orgID := callerOrgID

	// Validate role if provided
	if req.Role != nil {
		validRoles := map[string]bool{"admin": true, "user": true, "readonly": true, "guest": true}
		// Only superadmin can assign superadmin role
		if *req.Role == "superadmin" {
			role, _ := h.permMiddleware.GetUserOrgRole(callerOrgID, callerUserID)
			if role != middleware.RoleSuperAdmin {
				c.JSON(http.StatusForbidden, gin.H{"error": "only superadmin can assign superadmin role"})
				return
			}
			validRoles["superadmin"] = true
		}
		if !validRoles[*req.Role] {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid role"})
			return
		}
		if err := h.db.Session().Query(`
			UPDATE users SET role = ? WHERE org_id = ? AND user_id = ?
		`, *req.Role, orgID, targetUserID).Exec(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update user"})
			return
		}
	}

	if req.QuotaBytes != nil {
		if err := h.db.Session().Query(`
			UPDATE users SET quota_bytes = ? WHERE org_id = ? AND user_id = ?
		`, *req.QuotaBytes, orgID, targetUserID).Exec(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update user"})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// DeactivateUser deactivates a user.
// DELETE /admin/users/:user_id/
// If :user_id contains an @ sign, it's treated as an email lookup (seafile-js compatible).
func (h *AdminHandler) DeactivateUser(c *gin.Context) {
	targetUserID := h.getResolvedUserParam(c)

	// Dispatch to email-based handler if the param looks like an email
	if strings.Contains(targetUserID, "@") {
		h.DeleteUserByEmail(c, targetUserID)
		return
	}

	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")

	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	// Don't allow deactivating yourself
	if targetUserID == callerUserID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot deactivate your own account"})
		return
	}

	orgID := callerOrgID

	// Set role to "deactivated" to disable the user
	if err := h.db.Session().Query(`
		UPDATE users SET role = ? WHERE org_id = ? AND user_id = ?
	`, "deactivated", orgID, targetUserID).Exec(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to deactivate user"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// requireAdminAccess checks that the caller is superadmin or tenant admin.
// Returns a non-nil error (and writes the response) if not authorized.
func (h *AdminHandler) requireAdminAccess(c *gin.Context, callerOrgID, callerUserID string) error {
	role, err := h.permMiddleware.GetUserOrgRole(callerOrgID, callerUserID)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
		c.Abort()
		return err
	}
	if !isAdminOrAbove(role) {
		c.JSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
		c.Abort()
		return fmt.Errorf("insufficient permissions: role %s", role)
	}
	return nil
}

// isAdminOrAbove returns true if the role is admin or superadmin
func isAdminOrAbove(role middleware.OrganizationRole) bool {
	return middleware.HasRequiredOrgRole(role, middleware.RoleAdmin)
}

// lookupUserByEmail finds a user's user_id and org_id by email.
// It checks users_by_email first, then falls back to a global scan of the
// users table for pre-index users, backfilling the index on success.
func (h *AdminHandler) lookupUserByEmail(email string) (userID, orgID string, err error) {
	err = h.db.Session().Query(`
		SELECT user_id, org_id FROM users_by_email WHERE email = ?
	`, email).Scan(&userID, &orgID)
	if err == nil {
		return
	}

	// Fallback: full-table scan (admin path, infrequent; stops on first match)
	iter := h.db.Session().Query(`
		SELECT user_id, org_id FROM users WHERE email = ? ALLOW FILTERING
	`, email).Iter()
	found := iter.Scan(&userID, &orgID)
	if closeErr := iter.Close(); closeErr != nil {
		err = closeErr
		return
	}
	if !found {
		return // err already set from the first query
	}

	// Backfill the index so future lookups are fast
	_ = h.db.Session().Query(`
		INSERT INTO users_by_email (email, user_id, org_id) VALUES (?, ?, ?)
	`, email, userID, orgID).Exec()

	err = nil
	return
}

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

	// Determine which orgs to query: superadmin sees all, tenant admin sees own org
	callerRole, _ := h.permMiddleware.GetUserOrgRole(callerOrgID, callerUserID)
	var orgIDs []string
	if callerRole == middleware.RoleSuperAdmin {
		orgIter := h.db.Session().Query(`SELECT org_id FROM organizations`).Iter()
		var oid string
		for orgIter.Scan(&oid) {
			orgIDs = append(orgIDs, oid)
		}
		orgIter.Close()
	} else {
		orgIDs = []string{callerOrgID}
	}

	var allGroups []adminGroupResponse
	for _, orgID := range orgIDs {
		iter := h.db.Session().Query(`
			SELECT group_id, name, creator_id, created_at FROM groups WHERE org_id = ?
		`, orgID).Iter()

		var groupID, name, creatorID string
		var createdAt time.Time

		for iter.Scan(&groupID, &name, &creatorID, &createdAt) {
			var ownerEmail, ownerName string
			h.db.Session().Query(`
				SELECT email, name FROM users WHERE org_id = ? AND user_id = ?
			`, orgID, creatorID).Scan(&ownerEmail, &ownerName)

			allGroups = append(allGroups, adminGroupResponse{
				ID:            groupID,
				Name:          name,
				Owner:         ownerEmail,
				OwnerName:     ownerName,
				CreatedAt:     createdAt.Format(time.RFC3339),
				ParentGroupID: 0,
				OrgID:         orgID,
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

	// Determine which orgs to query: superadmin sees all, tenant admin sees own org
	callerRole, _ := h.permMiddleware.GetUserOrgRole(callerOrgID, callerUserID)
	var orgIDs []string
	if callerRole == middleware.RoleSuperAdmin {
		orgIter := h.db.Session().Query(`SELECT org_id FROM organizations`).Iter()
		var oid string
		for orgIter.Scan(&oid) {
			orgIDs = append(orgIDs, oid)
		}
		orgIter.Close()
	} else {
		orgIDs = []string{callerOrgID}
	}

	var results []adminGroupResponse
	for _, orgID := range orgIDs {
		iter := h.db.Session().Query(`
			SELECT group_id, name, creator_id, created_at FROM groups WHERE org_id = ?
		`, orgID).Iter()

		var groupID, name, creatorID string
		var createdAt time.Time

		for iter.Scan(&groupID, &name, &creatorID, &createdAt) {
			if !strings.Contains(strings.ToLower(name), query) {
				continue
			}

			var ownerEmail, ownerName string
			h.db.Session().Query(`
				SELECT email, name FROM users WHERE org_id = ? AND user_id = ?
			`, orgID, creatorID).Scan(&ownerEmail, &ownerName)

			results = append(results, adminGroupResponse{
				ID:            groupID,
				Name:          name,
				Owner:         ownerEmail,
				OwnerName:     ownerName,
				CreatedAt:     createdAt.Format(time.RFC3339),
				ParentGroupID: 0,
				OrgID:         orgID,
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

	groupName := c.Request.FormValue("group_name")
	groupOwnerEmail := c.Request.FormValue("group_owner")

	if groupName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "group_name is required"})
		return
	}

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
	if err := batch.Exec(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create group"})
		return
	}

	c.JSON(http.StatusCreated, adminGroupResponse{
		ID:            groupUUID.String(),
		Name:          groupName,
		Owner:         ownerEmail,
		OwnerName:     ownerEmail,
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

	// Get all members before deleting (for groups_by_member cleanup)
	memberIter := h.db.Session().Query(`SELECT user_id FROM group_members WHERE group_id = ?`, groupID).Iter()
	var memberID string
	var memberIDs []string
	for memberIter.Scan(&memberID) {
		memberIDs = append(memberIDs, memberID)
	}
	memberIter.Close()

	// Atomic batch: delete group + groups_by_id + group_members
	batch := h.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`DELETE FROM groups WHERE org_id = ? AND group_id = ?`, orgID, groupID)
	batch.Query(`DELETE FROM groups_by_id WHERE group_id = ?`, groupID)
	batch.Query(`DELETE FROM group_members WHERE group_id = ?`, groupID)
	if err := batch.Exec(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete group"})
		return
	}

	// Clean up groups_by_member lookup (different partitions, unlogged batch)
	for i := 0; i < len(memberIDs); i += 50 {
		end := i + 50
		if end > len(memberIDs) {
			end = len(memberIDs)
		}
		memberBatch := h.db.Session().Batch(gocql.UnloggedBatch)
		for _, uid := range memberIDs[i:end] {
			memberBatch.Query(`DELETE FROM groups_by_member WHERE org_id = ? AND user_id = ? AND group_id = ?`,
				orgID, uid, groupID)
		}
		memberBatch.Exec()
	}

	// Clean up shares where shared_to = this group (async, best-effort)
	groupUUID, _ := uuid.Parse(groupID)
	go cleanupGroupSharesAsync(h.db, groupUUID)

	// Audit log
	orgUUID, _ := uuid.Parse(orgID)
	auditDetails, _ := json.Marshal(map[string]interface{}{
		"group_name":    name,
		"members_count": len(memberIDs),
	})
	h.db.Session().Query(`
		INSERT INTO audit_log (org_id, timestamp, action, target_type, target_id, actor_id, details)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, orgUUID.String(), time.Now(), "delete_group", "group", groupID, callerUserID,
		string(auditDetails)).Exec()

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// cleanupGroupSharesAsync removes all library shares targeting a deleted group.
// Runs async — safe for best-effort cleanup; scanner phase 9 catches any misses.
func cleanupGroupSharesAsync(database *db.DB, groupID uuid.UUID) {
	iter := database.Session().Query(`
		SELECT library_id FROM shares_by_user WHERE shared_to = ?
	`, groupID.String()).Iter()

	var libIDStr string
	var libraryIDs []string
	for iter.Scan(&libIDStr) {
		libraryIDs = append(libraryIDs, libIDStr)
	}
	iter.Close()

	for _, libID := range libraryIDs {
		// Find share_id(s) for this group in the shares table
		shareIter := database.Session().Query(`
			SELECT share_id, shared_to FROM shares WHERE library_id = ?
		`, libID).Iter()
		var shareIDStr, sharedToStr string
		for shareIter.Scan(&shareIDStr, &sharedToStr) {
			if sharedToStr == groupID.String() {
				database.Session().Query(`DELETE FROM shares WHERE library_id = ? AND share_id = ?`,
					libID, shareIDStr).Exec()
			}
		}
		shareIter.Close()

		// Delete from shares_by_user
		database.Session().Query(`DELETE FROM shares_by_user WHERE shared_to = ? AND library_id = ?`,
			groupID.String(), libID).Exec()
	}

	if len(libraryIDs) > 0 {
		log.Printf("[Admin] Cleaned up %d group shares for deleted group %s", len(libraryIDs), groupID)
	}
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
	newOwnerEmail := c.Request.FormValue("new_owner")
	newName := c.Request.FormValue("name")

	// Resolve the group's actual org_id from groups_by_id (superadmin may operate on groups outside their own org)
	callerRole, _ := h.permMiddleware.GetUserOrgRole(callerOrgID, callerUserID)
	orgID := callerOrgID
	if callerRole == middleware.RoleSuperAdmin {
		var groupOrgID string
		if err := h.db.Session().Query(`SELECT org_id FROM groups_by_id WHERE group_id = ?`, groupID).Scan(&groupOrgID); err == nil && groupOrgID != "" {
			orgID = groupOrgID
		}
	}

	// Rename path: seafile-js sysAdminRenameDepartment sends PUT /admin/groups/:id/ with 'name'
	if newOwnerEmail == "" && newName != "" {
		now := time.Now()
		if err := h.db.Session().Query(`
			UPDATE groups SET name = ?, updated_at = ? WHERE org_id = ? AND group_id = ?
		`, newName, now, orgID, groupID).Exec(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to rename group"})
			return
		}
		h.db.Session().Query(`UPDATE groups_by_id SET name = ? WHERE group_id = ?`, newName, groupID).Exec()
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
	var creatorID string
	if err := h.db.Session().Query(`
		SELECT creator_id FROM groups WHERE org_id = ? AND group_id = ?
	`, orgID, groupID).Scan(&creatorID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "group not found"})
		return
	}

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

	// Update group creator
	h.db.Session().Query(`
		UPDATE groups SET creator_id = ?, updated_at = ? WHERE org_id = ? AND group_id = ?
	`, newOwnerID, now, orgID, groupID).Exec()

	// Demote old owner to member in group_members
	h.db.Session().Query(`
		UPDATE group_members SET role = ? WHERE group_id = ? AND user_id = ?
	`, "member", groupID, creatorID).Exec()

	// Demote old owner in lookup table (groups_by_member)
	h.db.Session().Query(`
		UPDATE groups_by_member SET role = ? WHERE org_id = ? AND user_id = ? AND group_id = ?
	`, "member", orgID, creatorID, groupID).Exec()

	// Add new owner as owner (upsert)
	h.db.Session().Query(`
		INSERT INTO group_members (group_id, user_id, role, added_at)
		VALUES (?, ?, ?, ?)
	`, groupID, newOwnerID, "owner", now).Exec()

	// Update lookup tables
	var groupName string
	h.db.Session().Query(`SELECT name FROM groups WHERE org_id = ? AND group_id = ?`, orgID, groupID).Scan(&groupName)

	h.db.Session().Query(`
		INSERT INTO groups_by_member (org_id, user_id, group_id, group_name, role, added_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, orgID, newOwnerID, groupID, groupName, "owner", now).Exec()

	// Return updated group object (frontend replaces item = res.data)
	var newOwnerName string
	h.db.Session().Query(`SELECT name FROM users WHERE org_id = ? AND user_id = ?`, orgID, newOwnerID).Scan(&newOwnerName)
	if newOwnerName == "" {
		newOwnerName = newOwnerEmail
	}
	c.JSON(http.StatusOK, adminGroupResponse{
		ID:            groupID,
		Name:          groupName,
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

	// Tenant admins may only see groups in their own org.
	callerRole, _ := h.permMiddleware.GetUserOrgRole(callerOrgID, callerUserID)
	if callerRole != middleware.RoleSuperAdmin && orgID != callerOrgID {
		c.JSON(http.StatusForbidden, gin.H{"error": "access denied"})
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

	// Support multiple emails (form array) — handles both multipart/form-data
	// and application/x-www-form-urlencoded.
	c.Request.ParseMultipartForm(32 << 20)
	emails := c.Request.Form["email"]
	if len(emails) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "email is required"})
		return
	}

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

	// Tenant admins may only manage groups in their own org.
	callerRole, _ := h.permMiddleware.GetUserOrgRole(callerOrgID, callerUserID)
	if callerRole != middleware.RoleSuperAdmin && orgID != callerOrgID {
		c.JSON(http.StatusForbidden, gin.H{"error": "access denied"})
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

		// Add to group_members
		if err := h.db.Session().Query(`
			INSERT INTO group_members (group_id, user_id, role, added_at)
			VALUES (?, ?, ?, ?)
		`, groupID, memberID, "member", now).Exec(); err != nil {
			failed = append(failed, failedItem{Email: email, ErrorMsg: "failed to add member"})
			continue
		}

		// Add to lookup table
		h.db.Session().Query(`
			INSERT INTO groups_by_member (org_id, user_id, group_id, group_name, role, added_at)
			VALUES (?, ?, ?, ?, ?, ?)
		`, orgID, memberID, groupID, groupName, "member", now).Exec()

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

	// Tenant admins may only manage groups in their own org.
	callerRole, _ := h.permMiddleware.GetUserOrgRole(callerOrgID, callerUserID)
	if callerRole != middleware.RoleSuperAdmin && orgID != callerOrgID {
		c.JSON(http.StatusForbidden, gin.H{"error": "access denied"})
		return
	}

	// Resolve user by email
	memberID, _, err := h.lookupUserByEmail(email)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	// Delete from group_members
	h.db.Session().Query(`
		DELETE FROM group_members WHERE group_id = ? AND user_id = ?
	`, groupID, memberID).Exec()

	// Delete from lookup table
	h.db.Session().Query(`
		DELETE FROM groups_by_member WHERE org_id = ? AND user_id = ? AND group_id = ?
	`, orgID, memberID, groupID).Exec()

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

	// Tenant admins may only see groups in their own org.
	callerRole, _ := h.permMiddleware.GetUserOrgRole(callerOrgID, callerUserID)
	if callerRole != middleware.RoleSuperAdmin && orgID != callerOrgID {
		c.JSON(http.StatusForbidden, gin.H{"error": "access denied"})
		return
	}

	// Pre-load users map for this org (avoids N+1 user lookups)
	usersMap := make(map[string]struct{ Email, Name string })
	uIter := h.db.Session().Query(`SELECT user_id, email, name FROM users WHERE org_id = ?`, orgID).Iter()
	var uid, uemail, uname string
	for uIter.Scan(&uid, &uemail, &uname) {
		displayName := uname
		if displayName == "" && uemail != "" {
			displayName = strings.Split(uemail, "@")[0]
		}
		usersMap[uid] = struct{ Email, Name string }{Email: uemail, Name: displayName}
	}
	uIter.Close()

	// Query all libraries in the org and filter for those shared with this group
	libIter := h.db.Session().Query(`
		SELECT library_id, owner_id, name, encrypted, size_bytes, created_at, updated_at, deleted_at
		FROM libraries WHERE org_id = ?
	`, orgID).Iter()

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
	var libID, ownerID, libName string
	var encrypted bool
	var sizeBytes int64
	var createdAt, updatedAt, deletedAt time.Time

	for libIter.Scan(&libID, &ownerID, &libName, &encrypted, &sizeBytes, &createdAt, &updatedAt, &deletedAt) {
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

		u := usersMap[ownerID]
		ownerEmail := u.Email
		ownerName := u.Name
		if ownerEmail == "" {
			ownerEmail = ownerID
		}
		if ownerName == "" {
			ownerName = ownerID
		}

		apiPerm := "rw"
		if perm == "r" {
			apiPerm = "r"
		}

		libraries = append(libraries, adminGroupLib{
			RepoID:       libID,
			Name:         libName,
			Size:         sizeBytes,
			SharedBy:     ownerEmail,
			SharedByName: ownerName,
			Encrypted:    encrypted,
			Permission:   apiPerm,
		})
	}
	libIter.Close()

	if libraries == nil {
		libraries = []adminGroupLib{}
	}

	c.JSON(http.StatusOK, gin.H{
		"libraries":  libraries,
		"group_name": groupName,
	})
}

// =============================================================================
// Phase 2: Admin User Endpoints (email-based, seafile-js compatible)
// =============================================================================

// adminUserResponse matches the seafile-js expected user object format.
type adminUserResponse struct {
	Email      string `json:"email"`
	Name       string `json:"name"`
	IsActive   bool   `json:"is_active"`
	IsStaff    bool   `json:"is_staff"`
	Role       string `json:"role"`
	AdminRole  string `json:"admin_role"`
	QuotaTotal int64  `json:"quota_total"`
	QuotaUsage int64  `json:"quota_usage"`
	CreateTime string `json:"create_time"`
	LastLogin  string `json:"last_login"`
}

func makeAdminUserResponse(email, name, role string, quotaBytes, usedBytes int64, createdAt time.Time) adminUserResponse {
	// admin_role is the role shown in the admin panel dropdown (only for admin/superadmin users)
	adminRole := role
	if role != "admin" && role != "superadmin" {
		adminRole = ""
	}
	return adminUserResponse{
		Email:      email,
		Name:       name,
		IsActive:   role != "deactivated",
		IsStaff:    role == "admin" || role == "superadmin",
		Role:       role,
		AdminRole:  adminRole,
		QuotaTotal: quotaBytes,
		QuotaUsage: usedBytes,
		CreateTime: createdAt.Format(time.RFC3339),
		LastLogin:  "", // Not tracked currently
	}
}

// ListAllUsers lists all users with pagination.
// Superadmin sees users across ALL orgs; tenant admin sees only own org.
// GET /admin/users/?page=N&per_page=N
func (h *AdminHandler) ListAllUsers(c *gin.Context) {
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

	// Determine which orgs to query: superadmin sees all, tenant admin sees own org
	callerRole, _ := h.permMiddleware.GetUserOrgRole(callerOrgID, callerUserID)
	var orgIDs []string
	if callerRole == middleware.RoleSuperAdmin {
		orgIter := h.db.Session().Query(`SELECT org_id FROM organizations`).Iter()
		var oid string
		for orgIter.Scan(&oid) {
			orgIDs = append(orgIDs, oid)
		}
		orgIter.Close()
	} else {
		orgIDs = []string{callerOrgID}
	}

	var allUsers []adminUserResponse
	seen := make(map[string]bool) // deduplicate by email
	for _, orgID := range orgIDs {
		iter := h.db.Session().Query(`
			SELECT user_id, email, name, role, quota_bytes, used_bytes, created_at
			FROM users WHERE org_id = ?
		`, orgID).Iter()

		var userID, email, name, role string
		var quotaBytes, usedBytes int64
		var createdAt time.Time

		for iter.Scan(&userID, &email, &name, &role, &quotaBytes, &usedBytes, &createdAt) {
			if !seen[email] {
				seen[email] = true
				allUsers = append(allUsers, makeAdminUserResponse(email, name, role, quotaBytes, usedBytes, createdAt))
			}
		}
		if err := iter.Close(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list users"})
			return
		}
	}

	// Paginate
	total := len(allUsers)
	start := (page - 1) * perPage
	if start > total {
		start = total
	}
	end := start + perPage
	if end > total {
		end = total
	}
	pageUsers := allUsers[start:end]
	if pageUsers == nil {
		pageUsers = []adminUserResponse{}
	}

	c.JSON(http.StatusOK, gin.H{
		"data":        pageUsers,
		"total_count": total,
	})
}

// SearchUsers searches users by email or name.
// Superadmin searches across ALL orgs; tenant admin searches own org.
// GET /admin/search-user/?query=...
func (h *AdminHandler) SearchUsers(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")

	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	query := strings.ToLower(c.Query("query"))
	if query == "" {
		c.JSON(http.StatusOK, gin.H{"user_list": []adminUserResponse{}})
		return
	}

	// Determine which orgs to query
	callerRole, _ := h.permMiddleware.GetUserOrgRole(callerOrgID, callerUserID)
	var orgIDs []string

	// If superadmin passes org_id param, restrict search to that org
	filterOrgID := c.Query("org_id")
	if filterOrgID != "" && callerRole == middleware.RoleSuperAdmin {
		orgIDs = []string{filterOrgID}
	} else if callerRole == middleware.RoleSuperAdmin {
		orgIter := h.db.Session().Query(`SELECT org_id FROM organizations`).Iter()
		var oid string
		for orgIter.Scan(&oid) {
			orgIDs = append(orgIDs, oid)
		}
		orgIter.Close()
	} else {
		orgIDs = []string{callerOrgID}
	}

	var results []adminUserResponse
	seen := make(map[string]bool)
	for _, orgID := range orgIDs {
		iter := h.db.Session().Query(`
			SELECT user_id, email, name, role, quota_bytes, used_bytes, created_at
			FROM users WHERE org_id = ?
		`, orgID).Iter()

		var userID, email, name, role string
		var quotaBytes, usedBytes int64
		var createdAt time.Time

		for iter.Scan(&userID, &email, &name, &role, &quotaBytes, &usedBytes, &createdAt) {
			if !seen[email] && (strings.Contains(strings.ToLower(email), query) || strings.Contains(strings.ToLower(name), query)) {
				seen[email] = true
				results = append(results, makeAdminUserResponse(email, name, role, quotaBytes, usedBytes, createdAt))
			}
		}
		iter.Close()
	}

	if results == nil {
		results = []adminUserResponse{}
	}

	c.JSON(http.StatusOK, gin.H{"user_list": results})
}

// AdminCreateUser creates a new user via admin API.
// POST /admin/users/ (JSON: email, name, role)
func (h *AdminHandler) AdminCreateUser(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")

	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	var req struct {
		Email string `json:"email"`
		Name  string `json:"name"`
		Role  string `json:"role"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	email := req.Email
	name := req.Name

	if email == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "email is required"})
		return
	}
	if name == "" {
		name = email
	}

	role := req.Role
	if role == "" {
		role = "user"
	}

	orgID := callerOrgID

	// Check if user already exists
	var existingUserID string
	if err := h.db.Session().Query(`
		SELECT user_id FROM users_by_email WHERE email = ?
	`, email).Scan(&existingUserID); err == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user with this email already exists"})
		return
	}

	userID := uuid.New().String()
	now := time.Now()

	// Create user record
	if err := h.db.Session().Query(`
		INSERT INTO users (org_id, user_id, email, name, role, quota_bytes, used_bytes, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, orgID, userID, email, name, role, int64(-2), int64(0), now).Exec(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create user"})
		return
	}

	// Create email lookup
	h.db.Session().Query(`
		INSERT INTO users_by_email (email, user_id, org_id)
		VALUES (?, ?, ?)
	`, email, userID, orgID).Exec()

	c.JSON(http.StatusCreated, makeAdminUserResponse(email, name, role, -2, 0, now))
}

// GetUserByEmail returns user details by email.
// GET /admin/users/:email/
// Note: This handler is reached when the :user_id param contains an @ sign (email).
// The existing GetUser handles UUID-based lookups.
func (h *AdminHandler) GetUserByEmail(c *gin.Context, email string) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")

	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	userID, userOrgID, err := h.lookupUserByEmail(email)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	var name, role string
	var quotaBytes, usedBytes int64
	var createdAt time.Time

	if err := h.db.Session().Query(`
		SELECT name, role, quota_bytes, used_bytes, created_at
		FROM users WHERE org_id = ? AND user_id = ?
	`, userOrgID, userID).Scan(&name, &role, &quotaBytes, &usedBytes, &createdAt); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	c.JSON(http.StatusOK, makeAdminUserResponse(email, name, role, quotaBytes, usedBytes, createdAt))
}

// UpdateUserByEmail updates a user by email.
// PUT /admin/users/:email/ (FormData: role, name, quota_total, is_active)
func (h *AdminHandler) UpdateUserByEmail(c *gin.Context, email string) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")

	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	userID, userOrgID, err := h.lookupUserByEmail(email)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	// Read current user data to return updated response
	var currentName, currentRole string
	var currentQuota, currentUsed int64
	var currentCreated time.Time
	h.db.Session().Query(`
		SELECT name, role, quota_bytes, used_bytes, created_at
		FROM users WHERE org_id = ? AND user_id = ?
	`, userOrgID, userID).Scan(&currentName, &currentRole, &currentQuota, &currentUsed, &currentCreated)

	// Read form values
	newRole := c.Request.FormValue("role")
	newName := c.Request.FormValue("name")
	quotaStr := c.Request.FormValue("quota_total")
	isActiveStr := c.Request.FormValue("is_active")
	isStaffStr := c.Request.FormValue("is_staff")

	if newRole != "" {
		validRoles := map[string]bool{"admin": true, "user": true, "readonly": true, "guest": true}
		if newRole == "superadmin" {
			role, _ := h.permMiddleware.GetUserOrgRole(callerOrgID, callerUserID)
			if role != middleware.RoleSuperAdmin {
				c.JSON(http.StatusForbidden, gin.H{"error": "only superadmin can assign superadmin role"})
				return
			}
			validRoles["superadmin"] = true
		}
		if !validRoles[newRole] {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid role"})
			return
		}
		currentRole = newRole
		h.db.Session().Query(`
			UPDATE users SET role = ? WHERE org_id = ? AND user_id = ?
		`, newRole, userOrgID, userID).Exec()
	}

	if isStaffStr != "" {
		if isStaffStr == "false" && (currentRole == "admin" || currentRole == "superadmin") {
			currentRole = "user"
			h.db.Session().Query(`
				UPDATE users SET role = ? WHERE org_id = ? AND user_id = ?
			`, currentRole, userOrgID, userID).Exec()
		} else if isStaffStr == "true" && currentRole != "admin" && currentRole != "superadmin" {
			currentRole = "admin"
			h.db.Session().Query(`
				UPDATE users SET role = ? WHERE org_id = ? AND user_id = ?
			`, currentRole, userOrgID, userID).Exec()
		}
	}

	if newName != "" {
		currentName = newName
		h.db.Session().Query(`
			UPDATE users SET name = ? WHERE org_id = ? AND user_id = ?
		`, newName, userOrgID, userID).Exec()
	}

	if quotaStr != "" {
		if quota, err := strconv.ParseInt(quotaStr, 10, 64); err == nil {
			currentQuota = quota
			h.db.Session().Query(`
				UPDATE users SET quota_bytes = ? WHERE org_id = ? AND user_id = ?
			`, quota, userOrgID, userID).Exec()
		}
	}

	if isActiveStr == "false" {
		currentRole = "deactivated"
		h.db.Session().Query(`
			UPDATE users SET role = ? WHERE org_id = ? AND user_id = ?
		`, "deactivated", userOrgID, userID).Exec()
	}

	resp := makeAdminUserResponse(email, currentName, currentRole, currentQuota, currentUsed, currentCreated)
	c.JSON(http.StatusOK, resp)
}

// DeleteUserByEmail deactivates a user by email.
// DELETE /admin/users/:email/
func (h *AdminHandler) DeleteUserByEmail(c *gin.Context, email string) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")

	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	userID, userOrgID, err := h.lookupUserByEmail(email)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	// Don't allow deactivating yourself
	if userID == callerUserID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot deactivate your own account"})
		return
	}

	h.db.Session().Query(`
		UPDATE users SET role = ? WHERE org_id = ? AND user_id = ?
	`, "deactivated", userOrgID, userID).Exec()

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ListAdminUsers lists users with superadmin role.
// "admin" role = org admin (not system admin). Only superadmins appear here.
// GET /admin/admins/
func (h *AdminHandler) ListAdminUsers(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")

	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	// Determine which orgs to query
	callerRole, _ := h.permMiddleware.GetUserOrgRole(callerOrgID, callerUserID)
	var orgIDs []string
	if callerRole == middleware.RoleSuperAdmin {
		orgIter := h.db.Session().Query(`SELECT org_id FROM organizations`).Iter()
		var oid string
		for orgIter.Scan(&oid) {
			orgIDs = append(orgIDs, oid)
		}
		orgIter.Close()
	} else {
		orgIDs = []string{callerOrgID}
	}

	var admins []adminUserResponse
	seen := make(map[string]bool)
	for _, orgID := range orgIDs {
		iter := h.db.Session().Query(`
			SELECT user_id, email, name, role, quota_bytes, used_bytes, created_at
			FROM users WHERE org_id = ?
		`, orgID).Iter()

		var userID, email, name, role string
		var quotaBytes, usedBytes int64
		var createdAt time.Time

		for iter.Scan(&userID, &email, &name, &role, &quotaBytes, &usedBytes, &createdAt) {
			if !seen[email] && role == "superadmin" {
				seen[email] = true
				admins = append(admins, makeAdminUserResponse(email, name, role, quotaBytes, usedBytes, createdAt))
			}
		}
		iter.Close()
	}

	if admins == nil {
		admins = []adminUserResponse{}
	}

	c.JSON(http.StatusOK, gin.H{"admin_user_list": admins})
}

// BatchAddAdmins sets one or more users as superadmin.
// POST /admin/admins/
// Expects JSON: { "emails": ["user1@example.com", "user2@example.com"] }
func (h *AdminHandler) BatchAddAdmins(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	var req struct {
		Emails []string `json:"emails"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Emails) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "emails required"})
		return
	}

	var success []adminUserResponse
	var failed []gin.H

	for _, email := range req.Emails {
		userID, userOrgID, err := h.lookupUserByEmail(email)
		if err != nil {
			failed = append(failed, gin.H{"email": email, "error_msg": "user not found"})
			continue
		}

		// Set role to superadmin
		h.db.Session().Query(`UPDATE users SET role = ? WHERE org_id = ? AND user_id = ?`,
			"superadmin", userOrgID, userID).Exec()

		// Read back updated user data
		var name, role string
		var quotaBytes, usedBytes int64
		var createdAt time.Time
		if err := h.db.Session().Query(`
			SELECT name, role, quota_bytes, used_bytes, created_at
			FROM users WHERE org_id = ? AND user_id = ?
		`, userOrgID, userID).Scan(&name, &role, &quotaBytes, &usedBytes, &createdAt); err != nil {
			failed = append(failed, gin.H{"email": email, "error_msg": "failed to read user"})
			continue
		}
		success = append(success, makeAdminUserResponse(email, name, role, quotaBytes, usedBytes, createdAt))
	}

	if success == nil {
		success = []adminUserResponse{}
	}
	if failed == nil {
		failed = []gin.H{}
	}

	c.JSON(http.StatusOK, gin.H{"success": success, "failed": failed})
}

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

	// Determine which orgs to query: superadmin sees all, tenant admin sees own org
	callerRole, _ := h.permMiddleware.GetUserOrgRole(callerOrgID, callerUserID)
	var orgIDs []string
	if callerRole == middleware.RoleSuperAdmin {
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
	if callerRole == middleware.RoleSuperAdmin {
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

	// Tenant admin can only see libraries in their own org
	callerRole, _ := h.permMiddleware.GetUserOrgRole(callerOrgID, callerUserID)
	if callerRole != middleware.RoleSuperAdmin && orgID != callerOrgID {
		c.JSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
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

	callerRole, _ := h.permMiddleware.GetUserOrgRole(callerOrgID, callerUserID)
	if callerRole != middleware.RoleSuperAdmin && orgID != callerOrgID {
		c.JSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
		return
	}

	// Verify library exists and is not already deleted
	var deletedAt time.Time
	if err := h.db.Session().Query(`
		SELECT deleted_at FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, libraryID).Scan(&deletedAt); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}
	if !deletedAt.IsZero() {
		c.JSON(http.StatusNotFound, gin.H{"error": "library already deleted"})
		return
	}

	// Soft-delete
	now := time.Now()
	if err := h.db.Session().Query(`
		UPDATE libraries SET deleted_at = ?, deleted_by = ?
		WHERE org_id = ? AND library_id = ?
	`, now, callerUserID, orgID, libraryID).Exec(); err != nil {
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

	// Support both JSON and form data
	var repoName, ownerEmail string
	contentType := c.GetHeader("Content-Type")
	if strings.Contains(contentType, "application/json") {
		var req struct {
			Name  string `json:"name"`
			Owner string `json:"owner"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		repoName = req.Name
		ownerEmail = req.Owner
	} else {
		repoName = c.PostForm("name")
		ownerEmail = c.PostForm("owner")
	}

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

	// Tenant admin can only create in own org
	callerRole, _ := h.permMiddleware.GetUserOrgRole(callerOrgID, callerUserID)
	if callerRole != middleware.RoleSuperAdmin && ownerOrgID != callerOrgID {
		c.JSON(http.StatusForbidden, gin.H{"error": "cannot create library for user in different organization"})
		return
	}

	newLibID := uuid.New()
	now := time.Now()

	// Create empty root directory
	emptyDirEntries := "[]"
	emptyDirData := fmt.Sprintf("%d\n%s", 1, emptyDirEntries)
	emptyDirHash := sha1.Sum([]byte(emptyDirData))
	rootFSID := hex.EncodeToString(emptyDirHash[:])

	if err := h.db.Session().Query(`
		INSERT INTO fs_objects (library_id, fs_id, obj_type, obj_name, dir_entries, mtime)
		VALUES (?, ?, ?, ?, ?, ?)
	`, newLibID.String(), rootFSID, "dir", "", emptyDirEntries, now.Unix()).Exec(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create root directory"})
		return
	}

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
			library_id, org_id, owner_id, head_commit_id, encrypted
		) VALUES (?, ?, ?, ?, ?)
	`, newLibID.String(), ownerOrgID, ownerUserID, headCommitID, false,
	)

	if err := batch.Exec(); err != nil {
		log.Printf("[AdminCreateLibrary] Failed to create library: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create library"})
		return
	}

	// Create initial commit
	h.db.Session().Query(`
		INSERT INTO commits (library_id, commit_id, root_fs_id, creator_id, description, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, newLibID.String(), headCommitID, rootFSID, callerUserID, "Initial commit", now).Exec()

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

	// Support both JSON and form data
	var newOwnerEmail string
	contentType := c.GetHeader("Content-Type")
	if strings.Contains(contentType, "application/json") {
		var req struct {
			Owner string `json:"owner"`
		}
		if err := c.ShouldBindJSON(&req); err == nil {
			newOwnerEmail = req.Owner
		}
	} else {
		newOwnerEmail = c.PostForm("owner")
	}
	if newOwnerEmail == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "owner email is required"})
		return
	}

	// Lookup library's org
	var orgID string
	if err := h.db.Session().Query(`
		SELECT org_id FROM libraries_by_id WHERE library_id = ?
	`, libraryID).Scan(&orgID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}

	callerRole, _ := h.permMiddleware.GetUserOrgRole(callerOrgID, callerUserID)
	if callerRole != middleware.RoleSuperAdmin && orgID != callerOrgID {
		c.JSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
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

	// Dual-write: update both tables
	now := time.Now()
	if err := h.db.Session().Query(`
		UPDATE libraries SET owner_id = ?, updated_at = ?
		WHERE org_id = ? AND library_id = ?
	`, newOwnerID, now, orgID, libraryID).Exec(); err != nil {
		log.Printf("[AdminTransferLibrary] Failed to update libraries: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to transfer library"})
		return
	}

	h.db.Session().Query(`
		UPDATE libraries_by_id SET owner_id = ?
		WHERE library_id = ?
	`, newOwnerID, libraryID).Exec()

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

	callerRole, _ := h.permMiddleware.GetUserOrgRole(callerOrgID, callerUserID)
	if callerRole != middleware.RoleSuperAdmin && orgID != callerOrgID {
		c.JSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
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

	callerRole, _ := h.permMiddleware.GetUserOrgRole(callerOrgID, callerUserID)
	if callerRole != middleware.RoleSuperAdmin && orgID != callerOrgID {
		c.JSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
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

	callerRole, _ := h.permMiddleware.GetUserOrgRole(callerOrgID, callerUserID)
	if callerRole != middleware.RoleSuperAdmin && orgID != callerOrgID {
		c.JSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
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

	callerRole, _ := h.permMiddleware.GetUserOrgRole(callerOrgID, callerUserID)
	if callerRole != middleware.RoleSuperAdmin && orgID != callerOrgID {
		c.JSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
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

	callerRole, _ := h.permMiddleware.GetUserOrgRole(callerOrgID, callerUserID)
	if callerRole != middleware.RoleSuperAdmin && orgID != callerOrgID {
		c.JSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
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
	if callerRole == middleware.RoleSuperAdmin {
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
// Superadmin: cleans trash across all organizations.
// Org admin:  cleans trash only within their own organization.
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
	if callerRole == middleware.RoleSuperAdmin {
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
