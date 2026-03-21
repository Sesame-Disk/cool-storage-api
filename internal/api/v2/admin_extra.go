package v2

// admin_extra.go — Additional admin panel endpoints (sysinfo, statistics, devices,
// web-settings, logs, share links, notifications, institutions, invitations, org
// user management, search organizations).
//
// These are stub implementations returning realistic empty/default data matching
// the response format expected by the Seahub-compatible frontend.

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ============================================================================
// System Info — GET /admin/sysinfo/
// ============================================================================

func (h *AdminHandler) AdminGetSysInfo(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	// Count users
	var usersCount int
	iter := h.db.Session().Query(`SELECT user_id FROM users`).Iter()
	var dummy string
	for iter.Scan(&dummy) {
		usersCount++
	}
	iter.Close()

	// Count libraries
	var reposCount int
	iter = h.db.Session().Query(`SELECT library_id FROM libraries`).Iter()
	for iter.Scan(&dummy) {
		reposCount++
	}
	iter.Close()

	// Count groups
	var groupsCount int
	iter = h.db.Session().Query(`SELECT group_id FROM groups`).Iter()
	for iter.Scan(&dummy) {
		groupsCount++
	}
	iter.Close()

	// Count organizations
	var orgCount int
	iter = h.db.Session().Query(`SELECT org_id FROM organizations`).Iter()
	for iter.Scan(&dummy) {
		orgCount++
	}
	iter.Close()

	c.JSON(http.StatusOK, gin.H{
		"users_count":                     usersCount,
		"active_users_count":              usersCount, // Approximate
		"repos_count":                     reposCount,
		"total_files_count":               0, // Would require scanning all libraries
		"groups_count":                    groupsCount,
		"org_count":                       orgCount,
		"multi_tenancy_enabled":           true,
		"is_pro":                          true,
		"with_license":                    true,
		"license_expiration":              "2030-12-31",
		"license_mode":                    "subscription",
		"license_maxusers":                1000,
		"license_to":                      "SesameFS",
		"total_storage":                   int64(0),
		"total_devices_count":             0,
		"current_connected_devices_count": 0,
	})
}

// ============================================================================
// Statistics — GET /admin/statistics/{type}/
// ============================================================================

func (h *AdminHandler) AdminStatisticFiles(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	// Return empty statistics array with realistic date range
	stats := h.generateDateRange(c)
	result := make([]gin.H, len(stats))
	for i, dt := range stats {
		result[i] = gin.H{
			"datetime": dt,
			"added":    0,
			"deleted":  0,
			"modified": 0,
			"visited":  0,
		}
	}
	c.JSON(http.StatusOK, result)
}

func (h *AdminHandler) AdminStatisticStorage(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	stats := h.generateDateRange(c)
	result := make([]gin.H, len(stats))
	for i, dt := range stats {
		result[i] = gin.H{
			"datetime":      dt,
			"total_storage": int64(0),
		}
	}
	c.JSON(http.StatusOK, result)
}

func (h *AdminHandler) AdminStatisticActiveUsers(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	stats := h.generateDateRange(c)
	result := make([]gin.H, len(stats))
	for i, dt := range stats {
		result[i] = gin.H{
			"datetime": dt,
			"count":    0,
		}
	}
	c.JSON(http.StatusOK, result)
}

func (h *AdminHandler) AdminStatisticTraffic(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	stats := h.generateDateRange(c)
	result := make([]gin.H, len(stats))
	for i, dt := range stats {
		result[i] = gin.H{
			"datetime":           dt,
			"web-file-upload":    int64(0),
			"web-file-download":  int64(0),
			"sync-file-upload":   int64(0),
			"sync-file-download": int64(0),
			"link-file-upload":   int64(0),
			"link-file-download": int64(0),
		}
	}
	c.JSON(http.StatusOK, result)
}

// generateDateRange parses start/end params and returns date strings spaced by group_by.
func (h *AdminHandler) generateDateRange(c *gin.Context) []string {
	startStr := c.Query("start")
	endStr := c.Query("end")
	groupBy := c.DefaultQuery("group_by", "day")

	now := time.Now()
	end := now
	start := now.AddDate(0, 0, -7)

	if startStr != "" {
		if t, err := time.Parse("2006-01-02", startStr); err == nil {
			start = t
		}
	}
	if endStr != "" {
		if t, err := time.Parse("2006-01-02", endStr); err == nil {
			end = t
		}
	}

	var dates []string
	for d := start; !d.After(end); {
		dates = append(dates, d.Format("2006-01-02T00:00:00+00:00"))
		switch groupBy {
		case "month":
			d = d.AddDate(0, 1, 0)
		case "week":
			d = d.AddDate(0, 0, 7)
		default: // day
			d = d.AddDate(0, 0, 1)
		}
	}
	if len(dates) == 0 {
		dates = []string{now.Format("2006-01-02T00:00:00+00:00")}
	}
	return dates
}

// ============================================================================
// Devices — GET /admin/devices/ , GET /admin/device-errors/
// ============================================================================

func (h *AdminHandler) AdminListDevices(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
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

func (h *AdminHandler) AdminListDeviceErrors(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"device_errors": []gin.H{},
		"page_info": gin.H{
			"current_page":  1,
			"has_next_page": false,
		},
	})
}

func (h *AdminHandler) AdminClearDeviceErrors(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ============================================================================
// Web Settings — GET/PUT /admin/web-settings/
// ============================================================================

func (h *AdminHandler) AdminGetWebSettings(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	serviceURL := h.config.Server.Port
	if strings.HasPrefix(serviceURL, ":") {
		serviceURL = "http://localhost" + serviceURL
	}

	c.JSON(http.StatusOK, gin.H{
		"SERVICE_URL":                        serviceURL,
		"FILE_SERVER_ROOT":                   serviceURL + "/seafhttp",
		"SITE_TITLE":                         "SesameFS",
		"SITE_NAME":                          "SesameFS",
		"ENABLE_BRANDING_CSS":                false,
		"CUSTOM_CSS":                         "",
		"ENABLE_SIGNUP":                      false,
		"ACTIVATE_AFTER_REGISTRATION":        true,
		"REGISTRATION_SEND_MAIL":             false,
		"LOGIN_REMEMBER_DAYS":                7,
		"LOGIN_ATTEMPT_LIMIT":                5,
		"FREEZE_USER_ON_LOGIN_FAILED":        false,
		"ENABLE_SHARE_TO_ALL_GROUPS":         true,
		"USER_STRONG_PASSWORD_REQUIRED":      false,
		"FORCE_PASSWORD_CHANGE":              false,
		"USER_PASSWORD_MIN_LENGTH":           6,
		"USER_PASSWORD_STRENGTH_LEVEL":       1,
		"ENABLE_TWO_FACTOR_AUTH":             false,
		"ENABLE_REPO_HISTORY_SETTING":        true,
		"ENABLE_ENCRYPTED_LIBRARY":           true,
		"REPO_PASSWORD_MIN_LENGTH":           8,
		"SHARE_LINK_FORCE_USE_PASSWORD":      false,
		"SHARE_LINK_PASSWORD_MIN_LENGTH":     8,
		"SHARE_LINK_PASSWORD_STRENGTH_LEVEL": 1,
		"ENABLE_USER_CLEAN_TRASH":            true,
		"TEXT_PREVIEW_EXT":                   "ac,am,bat,c,cc,cmake,cpp,cs,css,diff,el,go,h,html,htm,java,js,json,less,make,md,org,php,pl,properties,py,rb,scala,script,sh,sql,txt,text,tex,vi,vim,xhtml,xml,log,csv,groovy,rst,patch,yml,yaml",
		"DISABLE_SYNC_WITH_ANY_FOLDER":       false,
	})
}

func (h *AdminHandler) AdminSetWebSettings(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}
	// Accept the setting but don't persist (stub)
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ============================================================================
// Logo/Favicon/Login BG uploads — stubs
// ============================================================================

func (h *AdminHandler) AdminUpdateLogo(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}
	c.JSON(http.StatusOK, gin.H{"logo_path": "/media/custom/logo.png"})
}

func (h *AdminHandler) AdminUpdateFavicon(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}
	c.JSON(http.StatusOK, gin.H{"favicon_path": "/media/custom/favicon.ico"})
}

func (h *AdminHandler) AdminUpdateLoginBG(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}
	c.JSON(http.StatusOK, gin.H{"login_bg_image_path": "/media/custom/login-bg.jpg"})
}

// ============================================================================
// Search Organizations — GET /admin/search-organization/
// ============================================================================

func (h *AdminHandler) AdminSearchOrganizations(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	query := strings.ToLower(c.Query("query"))

	iter := h.db.Session().Query(`
		SELECT org_id, name, storage_quota, storage_used, created_at
		FROM organizations
	`).Iter()

	var orgs []gin.H
	var orgID, name string
	var storageQuota, storageUsed int64
	var createdAt time.Time

	for iter.Scan(&orgID, &name, &storageQuota, &storageUsed, &createdAt) {
		if query != "" && !strings.Contains(strings.ToLower(name), query) {
			continue
		}
		// Count users in this org
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
	iter.Close()

	if orgs == nil {
		orgs = []gin.H{}
	}

	c.JSON(http.StatusOK, gin.H{
		"organizations": orgs,
		"total_count":   len(orgs),
	})
}

// ============================================================================
// Organization User Management (POST, PUT, DELETE)
// ============================================================================

// AdminAddOrgUser creates a user in an organization
// POST /admin/organizations/:org_id/users/
func (h *AdminHandler) AdminAddOrgUser(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	targetOrgID := c.Param("org_id")

	var req struct {
		Email    string `json:"email"`
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	if req.Email == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "email is required"})
		return
	}

	// Check if email already exists
	var existingUID string
	if err := h.db.Session().Query(`
		SELECT user_id FROM users_by_email WHERE email = ?
	`, req.Email).Scan(&existingUID); err == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user with this email already exists"})
		return
	}

	// Create user via the existing admin create user logic
	userID := generateUserID()
	now := time.Now()
	role := "user"

	if req.Name == "" {
		req.Name = strings.Split(req.Email, "@")[0]
	}

	if err := createUserWithEmailLookup(h.db, targetOrgID, userID, req.Email, req.Name, role, int64(-2), int64(0), now); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create user"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"email":        req.Email,
		"name":         req.Name,
		"status":       StatusActive,
		"active":       true,
		"is_org_staff": false,
		"quota_usage":  0,
		"quota_total":  -2,
		"create_time":  now.Format(time.RFC3339),
		"last_login":   "",
		"org_id":       targetOrgID,
	})
}

// AdminUpdateOrgUser updates a user in an organization.
// Accepts FormData: active, is_org_staff, is_staff, name, quota_total, role.
// PUT /admin/organizations/:org_id/users/:email/
func (h *AdminHandler) AdminUpdateOrgUser(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	targetOrgID := c.Param("org_id")
	email := c.Param("email")

	// Find user by email in org
	userID, _, err := h.lookupUserByEmail(email)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	var name, role, status string
	var quotaBytes, usedBytes int64
	var createdAt time.Time

	if err := h.db.Session().Query(`
		SELECT name, role, status, quota_bytes, used_bytes, created_at
		FROM users WHERE org_id = ? AND user_id = ?
	`, targetOrgID, userID).Scan(&name, &role, &status, &quotaBytes, &usedBytes, &createdAt); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	// Apply updates from FormData
	if v := c.Request.FormValue("active"); v != "" {
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

	// Support both is_org_staff and is_staff (frontend uses is_org_staff from sys-admin, is_staff from org-admin)
	isStaffVal := c.Request.FormValue("is_org_staff")
	if isStaffVal == "" {
		isStaffVal = c.Request.FormValue("is_staff")
	}
	if isStaffVal != "" {
		if isStaffVal == "true" {
			role = "admin"
		} else if role == "admin" {
			role = "user"
		}
		if err := h.db.Session().Query(`UPDATE users SET role = ? WHERE org_id = ? AND user_id = ?`,
			role, targetOrgID, userID).Exec(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update user"})
			return
		}
	}

	if v := c.Request.FormValue("name"); v != "" {
		name = v
		if err := h.db.Session().Query(`UPDATE users SET name = ? WHERE org_id = ? AND user_id = ?`,
			name, targetOrgID, userID).Exec(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update user"})
			return
		}
	}

	if v := c.Request.FormValue("role"); v != "" {
		validRoles := map[string]bool{"admin": true, "user": true, "readonly": true, "guest": true}
		if validRoles[v] {
			role = v
			if err := h.db.Session().Query(`UPDATE users SET role = ? WHERE org_id = ? AND user_id = ?`,
				role, targetOrgID, userID).Exec(); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update user"})
				return
			}
		}
	}

	if v := c.Request.FormValue("quota_total"); v != "" {
		if q, err := strconv.ParseInt(v, 10, 64); err == nil {
			quotaBytes = q
			if err := h.db.Session().Query(`UPDATE users SET quota_bytes = ? WHERE org_id = ? AND user_id = ?`,
				quotaBytes, targetOrgID, userID).Exec(); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update user"})
				return
			}
		}
	}

	isActive := IsUserUsable(status)
	isOrgStaff := role == "admin" || role == "superadmin"

	c.JSON(http.StatusOK, gin.H{
		"email":        email,
		"name":         name,
		"status":       normalizeUserStatus(status),
		"active":       isActive,
		"is_org_staff": isOrgStaff,
		"quota_usage":  usedBytes,
		"quota_total":  quotaBytes,
		"create_time":  createdAt.Format(time.RFC3339),
		"last_login":   "",
		"org_id":       targetOrgID,
	})
}

// AdminDeleteOrgUser removes a user from an organization
// DELETE /admin/organizations/:org_id/users/:email/
func (h *AdminHandler) AdminDeleteOrgUser(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	targetOrgID := c.Param("org_id")
	email := c.Param("email")

	// Find user by email in org
	iter := h.db.Session().Query(`
		SELECT user_id, email FROM users WHERE org_id = ?
	`, targetOrgID).Iter()

	var scanUID, scanEmail string
	var foundUID string
	for iter.Scan(&scanUID, &scanEmail) {
		if scanEmail == email {
			foundUID = scanUID
			break
		}
	}
	iter.Close()

	if foundUID == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	// Soft-delete: mark as "deleted" with timestamp for grace period cascade
	if err := softDeleteUser(h.db, h.sessions, targetOrgID, foundUID, time.Now()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete user"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// AdminListOrgGroups lists groups in an organization
// GET /admin/organizations/:org_id/groups/
func (h *AdminHandler) AdminListOrgGroups(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	targetOrgID := c.Param("org_id")

	iter := h.db.Session().Query(`
		SELECT group_id, name, creator_id, created_at
		FROM groups WHERE org_id = ?
	`, targetOrgID).Iter()

	var groups []gin.H
	var groupID, groupName, creatorID string
	var createdAt time.Time

	for iter.Scan(&groupID, &groupName, &creatorID, &createdAt) {
		ownerEmail := h.resolveOwnerEmail(targetOrgID, creatorID)
		ownerName := ownerEmail // fallback; resolveOwnerEmail returns email
		groups = append(groups, gin.H{
			"id":                    groupID,
			"group_id":              groupID,
			"group_name":            groupName,
			"creator_email":         ownerEmail,
			"creator_name":          ownerName,
			"creator_contact_email": ownerEmail,
			"ctime":                 createdAt.Format(time.RFC3339),
			"created_at":            createdAt.Format(time.RFC3339),
			"parent_group_id":       0,
		})
	}
	iter.Close()

	if groups == nil {
		groups = []gin.H{}
	}

	c.JSON(http.StatusOK, gin.H{"groups": groups, "group_list": groups})
}

// ============================================================================
// Logs — GET /admin/logs/*
// ============================================================================

func (h *AdminHandler) AdminListLoginLogs(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"login_log_list": []gin.H{},
		"total_count":    0,
	})
}

func (h *AdminHandler) AdminListFileAccessLogs(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"file_access_log_list": []gin.H{},
		"total_count":          0,
	})
}

func (h *AdminHandler) AdminListFileUpdateLogs(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"file_update_log_list": []gin.H{},
		"total_count":          0,
	})
}

func (h *AdminHandler) AdminListSharePermissionLogs(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"share_permission_log_list": []gin.H{},
		"total_count":               0,
	})
}

func (h *AdminHandler) AdminListAdminLogs(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"admin_operation_log_list": []gin.H{},
		"total_count":              0,
	})
}

func (h *AdminHandler) AdminListAdminLoginLogs(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"admin_login_log_list": []gin.H{},
		"total_count":          0,
	})
}

// ============================================================================
// Share Links — GET /admin/share-links/ , DELETE /admin/share-links/:token/
// ============================================================================

func (h *AdminHandler) AdminListShareLinks(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	// Backward-compatible filter: status=active|inactive maps to active=true|false
	statusFilter := strings.TrimSpace(strings.ToLower(c.DefaultQuery("status", "")))
	activeParam := strings.TrimSpace(strings.ToLower(c.DefaultQuery("active", "")))
	expiredParam := strings.TrimSpace(strings.ToLower(c.DefaultQuery("expired", "")))

	if statusFilter == "active" {
		activeParam = "true"
	} else if statusFilter == "inactive" {
		activeParam = "false"
	} else if statusFilter != "" && statusFilter != "all" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid status filter"})
		return
	}

	hasActiveFilter := false
	activeFilter := false
	if activeParam != "" && activeParam != "all" {
		parsed, err := strconv.ParseBool(activeParam)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid active filter"})
			return
		}
		hasActiveFilter = true
		activeFilter = parsed
	}

	hasExpiredFilter := false
	expiredFilter := false
	if expiredParam != "" && expiredParam != "all" {
		parsed, err := strconv.ParseBool(expiredParam)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid expired filter"})
			return
		}
		hasExpiredFilter = true
		expiredFilter = parsed
	}

	var links []gin.H
	iter := h.db.Session().Query(`
		SELECT link_token, link_type, library_id, file_path, created_by, permission, expires_at, has_password, active, view_count, created_at
		FROM share_links_by_org WHERE org_id = ?
	`, callerOrgID).Iter()
	var token, linkType, libID, filePath, createdBy, permission string
	var expiresAt *time.Time
	var hasPassword, active bool
	var viewCount *int
	var createdAt time.Time

	libNameCache := map[string]string{}
	userCache := map[string][2]string{} // createdBy -> [email, name]

	for iter.Scan(&token, &linkType, &libID, &filePath, &createdBy, &permission, &expiresAt, &hasPassword, &active, &viewCount, &createdAt) {
		// Only share links
		if linkType != "share" {
			continue
		}

		objName := filePath
		if idx := strings.LastIndex(filePath, "/"); idx >= 0 && idx < len(filePath)-1 {
			objName = filePath[idx+1:]
		}

		isExpired := false
		expireDateStr := ""
		if expiresAt != nil && !expiresAt.IsZero() {
			if expiresAt.Before(time.Now()) {
				isExpired = true
			}
			expireDateStr = expiresAt.Format("2006-01-02T15:04:05+00:00")
		}
		if hasActiveFilter && active != activeFilter {
			continue
		}
		if hasExpiredFilter && isExpired != expiredFilter {
			continue
		}

		status := "active"
		if !active {
			status = "inactive"
		}

		repoName, ok := libNameCache[libID]
		if !ok {
			h.db.Session().Query(`SELECT name FROM libraries WHERE org_id = ? AND library_id = ?`, callerOrgID, libID).Scan(&repoName)
			if repoName == "" {
				repoName = "Unknown Library"
			}
			libNameCache[libID] = repoName
		}

		userData, ok := userCache[createdBy]
		if !ok {
			var email, name string
			h.db.Session().Query(`SELECT email, name FROM users WHERE org_id = ? AND user_id = ?`, callerOrgID, createdBy).Scan(&email, &name)
			if email == "" {
				email = createdBy
			}
			if name == "" {
				name = email
			}
			userData = [2]string{email, name}
			userCache[createdBy] = userData
		}

		perms := parsePermsJSON(permission)

		count := 0
		if viewCount != nil {
			count = *viewCount
		}

		links = append(links, gin.H{
			"obj_name":      objName,
			"token":         token,
			"repo_id":       libID,
			"repo_name":     repoName,
			"path":          filePath,
			"creator_email": userData[0],
			"creator_name":  userData[1],
			"ctime":         createdAt.Format(time.RFC3339),
			"view_cnt":      count,
			"expire_date":   expireDateStr,
			"is_expired":    isExpired,
			"active":        active,
			"has_password":  hasPassword,
			"status":        status,
			"permissions":   gin.H{"can_download": perms.CanDownload, "can_edit": perms.CanEdit},
		})
	}
	if err := iter.Close(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list share links"})
		return
	}

	if links == nil {
		links = []gin.H{}
	}

	// Sorting support (data already comes sorted by ctime DESC from Cassandra)
	sortBy := c.DefaultQuery("order_by", "")
	direction := c.DefaultQuery("direction", "asc")
	sortAdminLinks(links, sortBy, direction)

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	perPage, _ := strconv.Atoi(c.DefaultQuery("per_page", "25"))
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
		"share_link_list": links[start:end],
		"count":           total,
	})
}

func (h *AdminHandler) AdminDeleteShareLink(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	token := c.Param("token")

	// Read from primary table to get clustering keys for deletion
	var createdBy, orgID, libID string
	var createdAt time.Time
	if err := h.db.Session().Query(`SELECT created_by, org_id, library_id, created_at FROM share_links WHERE link_token = ?`, token).Scan(&createdBy, &orgID, &libID, &createdAt); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "share link not found"})
		return
	}

	sh := &ShareLinkHandler{db: h.db}
	if err := sh.deleteShareLink(token, orgID, createdBy, libID, createdAt); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete share link"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// AdminSetShareLinkActive toggles active flag for a share link (superadmin/admin scope).
// PUT /admin/share-links/:token/active/
// Body/Form: active=true|false
func (h *AdminHandler) AdminSetShareLinkActive(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	token := c.Param("token")
	activeRaw := strings.TrimSpace(strings.ToLower(c.PostForm("active")))
	if activeRaw == "" {
		var req struct {
			Active *bool `json:"active"`
		}
		if err := c.ShouldBindJSON(&req); err == nil && req.Active != nil {
			activeRaw = strconv.FormatBool(*req.Active)
		}
	}
	active, err := strconv.ParseBool(activeRaw)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "active is required and must be true or false"})
		return
	}

	if err := h.setAdminLinkActive(token, "share", active); err != nil {
		if err.Error() == "not found" {
			c.JSON(http.StatusNotFound, gin.H{"error": "share link not found"})
			return
		}
		if err.Error() == "wrong type" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "link is not a share link"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update share link"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "active": active})
}

// ============================================================================
// Upload Links — GET /admin/upload-links/ , DELETE /admin/upload-links/:token/
// ============================================================================

func (h *AdminHandler) AdminListUploadLinks(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	// Backward-compatible filter: status=active|inactive maps to active=true|false
	statusFilter := strings.TrimSpace(strings.ToLower(c.DefaultQuery("status", "")))
	activeParam := strings.TrimSpace(strings.ToLower(c.DefaultQuery("active", "")))
	expiredParam := strings.TrimSpace(strings.ToLower(c.DefaultQuery("expired", "")))

	if statusFilter == "active" {
		activeParam = "true"
	} else if statusFilter == "inactive" {
		activeParam = "false"
	} else if statusFilter != "" && statusFilter != "all" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid status filter"})
		return
	}

	hasActiveFilter := false
	activeFilter := false
	if activeParam != "" && activeParam != "all" {
		parsed, err := strconv.ParseBool(activeParam)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid active filter"})
			return
		}
		hasActiveFilter = true
		activeFilter = parsed
	}

	hasExpiredFilter := false
	expiredFilter := false
	if expiredParam != "" && expiredParam != "all" {
		parsed, err := strconv.ParseBool(expiredParam)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid expired filter"})
			return
		}
		hasExpiredFilter = true
		expiredFilter = parsed
	}

	var links []gin.H
	iter := h.db.Session().Query(`
		SELECT link_token, link_type, library_id, file_path, created_by, expires_at, active, has_password, upload_count, created_at
		FROM share_links_by_org WHERE org_id = ?
	`, callerOrgID).Iter()
	var token, linkType, libID, filePath, createdBy string
	var expiresAt *time.Time
	var active, hasPassword bool
	var uploadCount *int
	var createdAt time.Time

	libNameCache := map[string]string{}
	userCache := map[string][2]string{}

	for iter.Scan(&token, &linkType, &libID, &filePath, &createdBy, &expiresAt, &active, &hasPassword, &uploadCount, &createdAt) {
		// Only upload links
		if linkType != "upload" {
			continue
		}

		objName := filePath
		if idx := strings.LastIndex(filePath, "/"); idx >= 0 && idx < len(filePath)-1 {
			objName = filePath[idx+1:]
		}

		isExpired := false
		expireDateStr := ""
		if expiresAt != nil && !expiresAt.IsZero() {
			if expiresAt.Before(time.Now()) {
				isExpired = true
			}
			expireDateStr = expiresAt.Format("2006-01-02T15:04:05+00:00")
		}
		if hasActiveFilter && active != activeFilter {
			continue
		}
		if hasExpiredFilter && isExpired != expiredFilter {
			continue
		}

		status := "active"
		if !active {
			status = "inactive"
		}

		repoName, ok := libNameCache[libID]
		if !ok {
			h.db.Session().Query(`SELECT name FROM libraries WHERE org_id = ? AND library_id = ?`, callerOrgID, libID).Scan(&repoName)
			if repoName == "" {
				repoName = "Unknown Library"
			}
			libNameCache[libID] = repoName
		}

		userData, ok := userCache[createdBy]
		if !ok {
			var email, name string
			h.db.Session().Query(`SELECT email, name FROM users WHERE org_id = ? AND user_id = ?`, callerOrgID, createdBy).Scan(&email, &name)
			if email == "" {
				email = createdBy
			}
			if name == "" {
				name = email
			}
			userData = [2]string{email, name}
			userCache[createdBy] = userData
		}

		count := 0
		if uploadCount != nil {
			count = *uploadCount
		}

		links = append(links, gin.H{
			"obj_name":      objName,
			"path":          filePath,
			"token":         token,
			"repo_id":       libID,
			"repo_name":     repoName,
			"creator_email": userData[0],
			"creator_name":  userData[1],
			"ctime":         createdAt.Format(time.RFC3339),
			"view_cnt":      count,
			"expire_date":   expireDateStr,
			"is_expired":    isExpired,
			"active":        active,
			"has_password":  hasPassword,
			"status":        status,
		})
	}
	if err := iter.Close(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list upload links"})
		return
	}

	if links == nil {
		links = []gin.H{}
	}

	sortBy := c.DefaultQuery("order_by", "")
	direction := c.DefaultQuery("direction", "asc")
	sortAdminLinks(links, sortBy, direction)

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	perPage, _ := strconv.Atoi(c.DefaultQuery("per_page", "25"))
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

func (h *AdminHandler) AdminDeleteUploadLink(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	token := c.Param("token")

	var createdBy, orgID, libID string
	var createdAt time.Time
	if err := h.db.Session().Query(`SELECT created_by, org_id, library_id, created_at FROM share_links WHERE link_token = ?`, token).Scan(&createdBy, &orgID, &libID, &createdAt); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "upload link not found"})
		return
	}

	sh := &ShareLinkHandler{db: h.db}
	if err := sh.deleteShareLink(token, orgID, createdBy, libID, createdAt); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete upload link"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

func sortAdminLinks(links []gin.H, sortBy, direction string) {
	if sortBy == "" {
		return
	}

	sort.Slice(links, func(i, j int) bool {
		if sortBy == "view_cnt" {
			vi, _ := links[i]["view_cnt"].(int)
			vj, _ := links[j]["view_cnt"].(int)
			if direction == "desc" {
				return vi > vj
			}
			return vi < vj
		}

		var vi, vj string
		switch sortBy {
		case "ctime":
			vi, _ = links[i]["ctime"].(string)
			vj, _ = links[j]["ctime"].(string)
		case "creator":
			vi, _ = links[i]["creator_email"].(string)
			vj, _ = links[j]["creator_email"].(string)
		case "name":
			vi, _ = links[i]["obj_name"].(string)
			vj, _ = links[j]["obj_name"].(string)
		default:
			vi, _ = links[i]["ctime"].(string)
			vj, _ = links[j]["ctime"].(string)
		}
		if direction == "desc" {
			return vi > vj
		}
		return vi < vj
	})
}

// AdminSetUploadLinkActive toggles active flag for an upload link (superadmin/admin scope).
// PUT /admin/upload-links/:token/active/
// Body/Form: active=true|false
func (h *AdminHandler) AdminSetUploadLinkActive(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	token := c.Param("token")
	activeRaw := strings.TrimSpace(strings.ToLower(c.PostForm("active")))
	if activeRaw == "" {
		var req struct {
			Active *bool `json:"active"`
		}
		if err := c.ShouldBindJSON(&req); err == nil && req.Active != nil {
			activeRaw = strconv.FormatBool(*req.Active)
		}
	}
	active, err := strconv.ParseBool(activeRaw)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "active is required and must be true or false"})
		return
	}

	if err := h.setAdminLinkActive(token, "upload", active); err != nil {
		if err.Error() == "not found" {
			c.JSON(http.StatusNotFound, gin.H{"error": "upload link not found"})
			return
		}
		if err.Error() == "wrong type" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "link is not an upload link"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update upload link"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "active": active})
}

func (h *AdminHandler) setAdminLinkActive(token, expectedType string, active bool) error {
	var createdBy, orgID, libID, linkType string
	var createdAt time.Time
	if err := h.db.Session().Query(`
		SELECT created_by, org_id, library_id, created_at, link_type
		FROM share_links WHERE link_token = ?
	`, token).Scan(&createdBy, &orgID, &libID, &createdAt, &linkType); err != nil {
		return fmt.Errorf("not found")
	}
	if linkType != expectedType {
		return fmt.Errorf("wrong type")
	}

	batch := h.db.Session().Batch(gocql.UnloggedBatch)
	batch.Query(`UPDATE share_links SET active = ? WHERE link_token = ?`, active, token)
	batch.Query(`UPDATE share_links_by_creator SET active = ? WHERE org_id = ? AND created_by = ? AND created_at = ? AND link_token = ?`,
		active, orgID, createdBy, createdAt, token)
	batch.Query(`UPDATE share_links_by_org SET active = ? WHERE org_id = ? AND created_at = ? AND link_token = ?`,
		active, orgID, createdAt, token)

	if err := batch.Exec(); err != nil {
		return err
	}
	return nil
}

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

// ============================================================================
// User share/upload links — GET /admin/users/:email/share-links/
// ============================================================================

func (h *AdminHandler) AdminListUserShareLinks(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	email := c.GetString("resolved_user_param")
	if email == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "email is required"})
		return
	}

	var targetUserID string
	var targetOrgID string
	if err := h.db.Session().Query(`SELECT user_id, org_id FROM users_by_email WHERE email = ?`, email).Scan(&targetUserID, &targetOrgID); err != nil {
		c.JSON(http.StatusOK, gin.H{"share_link_list": []gin.H{}, "count": 0})
		return
	}

	var links []gin.H
	iter := h.db.Session().Query(`
		SELECT link_token, link_type, library_id, file_path, permission, expires_at, view_count, download_count, created_at
		FROM share_links_by_creator WHERE org_id = ? AND created_by = ?`,
		targetOrgID, targetUserID).Iter()

	var token, linkType, libID, filePath, permission string
	var expiresAt *time.Time
	var viewCount, downloadCount int
	var createdAt time.Time

	libNameCache := map[string]string{}

	for iter.Scan(&token, &linkType, &libID, &filePath, &permission, &expiresAt, &viewCount, &downloadCount, &createdAt) {
		if linkType != "share" {
			continue
		}

		objName := filePath
		if idx := strings.LastIndex(filePath, "/"); idx >= 0 && idx < len(filePath)-1 {
			objName = filePath[idx+1:]
		}

		isExpired := false
		expireDateStr := ""
		if expiresAt != nil && !expiresAt.IsZero() {
			isExpired = expiresAt.Before(time.Now())
			expireDateStr = expiresAt.Format("2006-01-02T15:04:05+00:00")
		}

		repoName, ok := libNameCache[libID]
		if !ok {
			h.db.Session().Query(`SELECT name FROM libraries WHERE org_id = ? AND library_id = ?`, targetOrgID, libID).Scan(&repoName)
			if repoName == "" {
				repoName = "Unknown Library"
			}
			libNameCache[libID] = repoName
		}

		links = append(links, gin.H{
			"obj_name":      objName,
			"token":         token,
			"repo_id":       libID,
			"repo_name":     repoName,
			"path":          filePath,
			"creator_email": email,
			"creator_name":  email,
			"ctime":         createdAt.Format(time.RFC3339),
			"view_cnt":      viewCount,
			"expire_date":   expireDateStr,
			"is_expired":    isExpired,
		})
	}
	iter.Close()

	if links == nil {
		links = []gin.H{}
	}

	c.JSON(http.StatusOK, gin.H{
		"share_link_list": links,
		"count":           len(links),
	})
}

func (h *AdminHandler) AdminListUserUploadLinks(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	email := c.GetString("resolved_user_param")
	if email == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "email is required"})
		return
	}

	var targetUserID string
	var targetOrgID string
	if err := h.db.Session().Query(`SELECT user_id, org_id FROM users_by_email WHERE email = ?`, email).Scan(&targetUserID, &targetOrgID); err != nil {
		c.JSON(http.StatusOK, gin.H{"upload_link_list": []gin.H{}, "count": 0})
		return
	}

	var links []gin.H
	iter := h.db.Session().Query(`
		SELECT link_token, link_type, library_id, file_path, expires_at, created_at
		FROM share_links_by_creator WHERE org_id = ? AND created_by = ?`,
		targetOrgID, targetUserID).Iter()

	var token, linkType, libID, filePath string
	var expiresAt *time.Time
	var createdAt time.Time

	libNameCache := map[string]string{}

	for iter.Scan(&token, &linkType, &libID, &filePath, &expiresAt, &createdAt) {
		if linkType != "upload" {
			continue
		}

		objName := filePath
		if idx := strings.LastIndex(filePath, "/"); idx >= 0 && idx < len(filePath)-1 {
			objName = filePath[idx+1:]
		}

		isExpired := false
		expireDateStr := ""
		if expiresAt != nil && !expiresAt.IsZero() {
			isExpired = expiresAt.Before(time.Now())
			expireDateStr = expiresAt.Format("2006-01-02T15:04:05+00:00")
		}

		repoName, ok := libNameCache[libID]
		if !ok {
			h.db.Session().Query(`SELECT name FROM libraries WHERE org_id = ? AND library_id = ?`, targetOrgID, libID).Scan(&repoName)
			if repoName == "" {
				repoName = "Unknown Library"
			}
			libNameCache[libID] = repoName
		}

		links = append(links, gin.H{
			"obj_name":      objName,
			"path":          filePath,
			"token":         token,
			"repo_id":       libID,
			"repo_name":     repoName,
			"creator_email": email,
			"creator_name":  email,
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

	c.JSON(http.StatusOK, gin.H{
		"upload_link_list": links,
		"count":            len(links),
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

	// Tenant admins may only manage groups in their own org.
	callerRole, _ := h.permMiddleware.GetUserOrgRole(callerOrgID, callerUserID)
	if callerRole != middleware.RoleSuperAdmin && orgID != callerOrgID {
		c.JSON(http.StatusForbidden, gin.H{"error": "access denied"})
		return
	}

	isAdminStr := c.Request.FormValue("is_admin")
	if isAdminStr == "" {
		// Try JSON body
		var req struct {
			IsAdmin interface{} `json:"is_admin"`
		}
		c.ShouldBindJSON(&req)
		if req.IsAdmin != nil {
			isAdminStr = fmt.Sprintf("%v", req.IsAdmin)
		}
	}

	isAdmin := isAdminStr == "true" || isAdminStr == "True" || isAdminStr == "1"
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
// POST /admin/address-book/groups/  FormData: group_name, parent_group (optional), group_owner (optional), group_staff (optional, comma-separated)
func (h *AdminHandler) AdminAddAddressBookGroup(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	groupName := c.Request.FormValue("group_name")
	if groupName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "group_name is required"})
		return
	}
	parentGroup := c.Request.FormValue("parent_group")
	groupOwner := c.Request.FormValue("group_owner")
	groupStaff := c.Request.FormValue("group_staff")

	newGroupID := uuid.New().String()
	now := time.Now()

	creatorID := callerUserID
	if groupOwner != "" {
		if ownerID, _, err := h.lookupUserByEmail(groupOwner); err == nil {
			creatorID = ownerID
		}
	}

	batch := h.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		INSERT INTO groups (org_id, group_id, name, creator_id, parent_group_id, is_department, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, callerOrgID, newGroupID, groupName, creatorID, parentGroup, true, now, now)
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
	if err := batch.Exec(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create address-book group"})
		return
	}

	// Add staff members if specified
	if groupStaff != "" {
		for _, staffEmail := range strings.Split(groupStaff, ",") {
			staffEmail = strings.TrimSpace(staffEmail)
			if staffEmail == "" {
				continue
			}
			staffID, _, err := h.lookupUserByEmail(staffEmail)
			if err != nil {
				continue
			}
			if err := upsertGroupMember(h.db, callerOrgID, newGroupID, staffID, groupName, "member", now); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to add address-book group staff"})
				return
			}
		}
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
// PUT /admin/address-book/groups/:group_id/  FormData: group_name
func (h *AdminHandler) AdminUpdateAddressBookGroup(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	groupID := c.Param("group_id")
	newName := c.Request.FormValue("group_name")
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

	// Collect members before deletion (for groups_by_member cleanup)
	memIter := h.db.Session().Query(`
		SELECT user_id FROM group_members WHERE group_id = ?
	`, groupID).Iter()
	var memberID string
	var memberIDs []string
	for memIter.Scan(&memberID) {
		memberIDs = append(memberIDs, memberID)
	}
	memIter.Close()

	// Atomic batch: delete group + groups_by_id + group_members
	batch := h.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`DELETE FROM groups WHERE org_id = ? AND group_id = ?`, callerOrgID, groupID)
	batch.Query(`DELETE FROM groups_by_id WHERE group_id = ?`, groupID)
	batch.Query(`DELETE FROM group_members WHERE group_id = ?`, groupID)
	if err := batch.Exec(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete group"})
		return
	}

	if err := cleanupGroupsByMember(h.db, callerOrgID, groupID, memberIDs); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to clean group membership index"})
		return
	}

	groupUUID, _ := uuid.Parse(groupID)
	if err := cleanupGroupShares(h.db, groupUUID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to clean group shares"})
		return
	}

	// Audit log
	orgUUID, _ := uuid.Parse(callerOrgID)
	auditDetails, _ := json.Marshal(map[string]interface{}{
		"group_name":    groupName,
		"members_count": len(memberIDs),
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
// POST /admin/groups/:group_id/group-owned-libraries/  FormData: repo_name
func (h *AdminHandler) AdminAddGroupOwnedLibrary(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	groupID := c.Param("group_id")
	repoName := c.Request.FormValue("repo_name")
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
		INSERT INTO libraries_by_id (library_id, org_id, owner_id, encrypted)
		VALUES (?, ?, ?, ?)
	`, newLibID, callerOrgID, callerUserID, false)
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
	if err := h.db.Session().Query(`
		SELECT deleted_at FROM libraries WHERE org_id = ? AND library_id = ?
	`, callerOrgID, repoID).Scan(&deletedAt); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}
	if !deletedAt.IsZero() {
		c.JSON(http.StatusNotFound, gin.H{"error": "library already deleted"})
		return
	}

	now := time.Now()
	if err := h.db.Session().Query(`
		UPDATE libraries SET deleted_at = ?, deleted_by = ?
		WHERE org_id = ? AND library_id = ?
	`, now, callerUserID, callerOrgID, repoID).Exec(); err != nil {
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

// resolveOrgCreator returns the email and name of the first admin user in an org.
// Falls back to the first user if no admin/superadmin exists.
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
		if role == "superadmin" || role == "admin" {
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
