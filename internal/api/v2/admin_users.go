package v2

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	"github.com/Sesame-Disk/sesamefs/internal/traffic"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// =============================================================================
// Phase 2: Admin User Endpoints (email-based, seafile-js compatible)
// =============================================================================

// adminUserResponse matches the seafile-js expected user object format.
type adminUserResponse struct {
	Email                string `json:"email"`
	Name                 string `json:"name"`
	Status               string `json:"status"`
	OrgID                string `json:"org_id,omitempty"`
	IsPlatformOrg        bool   `json:"is_platform_org,omitempty"`
	IsActive             bool   `json:"is_active"`
	IsStaff              bool   `json:"is_staff"`
	Role                 string `json:"role"`
	AdminRole            string `json:"admin_role"`
	QuotaTotal           int64  `json:"quota_total"`
	QuotaUsage           int64  `json:"quota_usage"`
	TrafficUploadQuota   int64  `json:"traffic_upload_quota"`
	TrafficDownloadQuota int64  `json:"traffic_download_quota"`
	CreateTime           string `json:"create_time"`
	LastLogin            string `json:"last_login"`
	// Org-level quota limits (populated only in single-user responses).
	OrgStorageQuota         int64 `json:"org_storage_quota,omitempty"`
	OrgTrafficQuota         int64 `json:"org_traffic_quota,omitempty"` // combined upload+download
	OrgTrafficUploadQuota   int64 `json:"org_traffic_upload_quota,omitempty"`
	OrgTrafficDownloadQuota int64 `json:"org_traffic_download_quota,omitempty"`
}

func makeAdminUserResponse(email, name, role, status string, quotaBytes, usedBytes int64, createdAt, lastLoginAt time.Time) adminUserResponse {
	// admin_role is the role shown in the admin panel dropdown (only for admin/superadmin users)
	adminRole := role
	if role != "admin" && role != "superadmin" {
		adminRole = ""
	}
	canonicalStatus := normalizeUserStatus(status)
	return adminUserResponse{
		Email:                email,
		Name:                 name,
		Status:               canonicalStatus,
		IsActive:             canonicalStatus == StatusActive,
		IsStaff:              middleware.IsOrgStaff(role),
		Role:                 role,
		AdminRole:            adminRole,
		QuotaTotal:           quotaBytes,
		QuotaUsage:           usedBytes,
		TrafficUploadQuota:   0,
		TrafficDownloadQuota: 0,
		CreateTime:           createdAt.Format(time.RFC3339),
		LastLogin:            formatOptionalTimestamp(lastLoginAt),
	}
}

func normalizeUserStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case StatusDeleted:
		return StatusDeleted
	case StatusDeactivated:
		return StatusDeactivated
	default:
		return StatusActive
	}
}

func parseUserStatusFilter(c *gin.Context) (string, bool) {
	status := strings.ToLower(strings.TrimSpace(c.DefaultQuery("status", "all")))
	switch status {
	case "", "all", StatusActive, StatusDeactivated, StatusDeleted:
		if status == "" {
			status = "all"
		}
		return status, true
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid status filter"})
		return "", false
	}
}

func userMatchesStatusFilter(rawStatus, statusFilter string) bool {
	if statusFilter == "all" {
		return true
	}
	return normalizeUserStatus(rawStatus) == statusFilter
}

// ListAllUsers lists all users with pagination.
// Platform superadmin sees users across all orgs.
// GET /admin/users/?page=N&per_page=N
func (h *AdminHandler) ListAllUsers(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")

	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	statusFilter, ok := parseUserStatusFilter(c)
	if !ok {
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

	// Determine which orgs to query: platform superadmin may query all orgs.
	callerRole, _ := h.permMiddleware.GetUserOrgRole(callerOrgID, callerUserID)
	var orgIDs []string
	if middleware.IsPlatformSuperAdmin(callerOrgID, callerRole) {
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
			SELECT user_id, email, name, role, status, quota_bytes, created_at, last_login_at
			FROM users WHERE org_id = ?
		`, orgID).Iter()

		var userID, email, name, role, status string
		var quotaBytes int64
		var createdAt, lastLoginAt time.Time

		for iter.Scan(&userID, &email, &name, &role, &status, &quotaBytes, &createdAt, &lastLoginAt) {
			if !seen[email] && userMatchesStatusFilter(status, statusFilter) {
				seen[email] = true
				allUsers = append(allUsers, makeAdminUserResponse(email, name, role, status, quotaBytes, traffic.ReadStorageUsed(h.db, fmt.Sprintf("user:%s:%s", orgID, userID)), createdAt, lastLoginAt))
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
// Platform superadmin searches across all orgs.
// GET /admin/search-user/?query=...
func (h *AdminHandler) SearchUsers(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")

	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	query := strings.ToLower(c.Query("query"))
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	perPage, _ := strconv.Atoi(c.DefaultQuery("per_page", "25"))
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 25
	}
	if query == "" {
		c.JSON(http.StatusOK, gin.H{
			"user_list": []adminUserResponse{},
			"page_info": gin.H{
				"current_page":  page,
				"has_next_page": false,
			},
		})
		return
	}

	// Determine which orgs to query
	callerRole, _ := h.permMiddleware.GetUserOrgRole(callerOrgID, callerUserID)
	var orgIDs []string

	// If superadmin passes org_id param, restrict search to that org
	filterOrgID := c.Query("org_id")
	if filterOrgID != "" && middleware.IsPlatformSuperAdmin(callerOrgID, callerRole) {
		orgIDs = []string{filterOrgID}
	} else if middleware.IsPlatformSuperAdmin(callerOrgID, callerRole) {
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
			SELECT user_id, email, name, role, status, quota_bytes, created_at, last_login_at
			FROM users WHERE org_id = ?
		`, orgID).Iter()

		var userID, email, name, role, status string
		var quotaBytes int64
		var createdAt, lastLoginAt time.Time

		for iter.Scan(&userID, &email, &name, &role, &status, &quotaBytes, &createdAt, &lastLoginAt) {
			if !seen[email] && (strings.Contains(strings.ToLower(email), query) || strings.Contains(strings.ToLower(name), query)) {
				seen[email] = true
				results = append(results, makeAdminUserResponse(email, name, role, status, quotaBytes, traffic.ReadStorageUsed(h.db, fmt.Sprintf("user:%s:%s", orgID, userID)), createdAt, lastLoginAt))
			}
		}
		iter.Close()
	}

	if results == nil {
		results = []adminUserResponse{}
	}

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
		"user_list": results[start:end],
		"page_info": gin.H{
			"current_page":  page,
			"has_next_page": end < total,
		},
	})
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

	// Check max_users quota before creating.
	if checker := traffic.GetChecker(); checker != nil {
		if st, _ := checker.CheckMaxUsers(orgID); !st.Allowed {
			c.JSON(http.StatusForbidden, gin.H{"error": "user limit reached for this organization"})
			return
		}
	}

	userID := uuid.New().String()
	now := time.Now()

	if err := createUserWithEmailLookup(h.db, orgID, userID, email, name, role, int64(-2), int64(0), now); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create user"})
		return
	}

	c.JSON(http.StatusCreated, makeAdminUserResponse(email, name, role, "active", -2, 0, now, time.Time{}))
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

	var name, role, status string
	var quotaBytes int64
	var trafficUploadQuota int64
	var trafficDownloadQuota int64
	var createdAt, lastLoginAt time.Time

	if err := h.db.Session().Query(`
		SELECT name, role, status, quota_bytes, traffic_upload_quota, traffic_download_quota, created_at, last_login_at
		FROM users WHERE org_id = ? AND user_id = ?
	`, userOrgID, userID).Scan(&name, &role, &status, &quotaBytes, &trafficUploadQuota, &trafficDownloadQuota, &createdAt, &lastLoginAt); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	resp := makeAdminUserResponse(email, name, role, status, quotaBytes, traffic.ReadStorageUsed(h.db, fmt.Sprintf("user:%s:%s", userOrgID, userID)), createdAt, lastLoginAt)
	resp.OrgID = userOrgID
	resp.IsPlatformOrg = userOrgID == middleware.PlatformOrgID
	resp.TrafficUploadQuota = trafficUploadQuota
	resp.TrafficDownloadQuota = trafficDownloadQuota
	oq, _ := readOrgQuotas(h.db, userOrgID) // best-effort for display
	resp.OrgStorageQuota = oq.StorageQuota
	resp.OrgTrafficQuota = oq.TrafficQuota
	resp.OrgTrafficUploadQuota = oq.TrafficUploadQuota
	resp.OrgTrafficDownloadQuota = oq.TrafficDownloadQuota
	c.JSON(http.StatusOK, resp)
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
	var currentName, currentRole, currentStatus string
	var currentQuota int64
	var currentTrafficUploadQuota int64
	var currentTrafficDownloadQuota int64
	var currentCreated, currentLastLogin time.Time
	h.db.Session().Query(`
		SELECT name, role, status, quota_bytes, traffic_upload_quota, traffic_download_quota, created_at, last_login_at
		FROM users WHERE org_id = ? AND user_id = ?
	`, userOrgID, userID).Scan(&currentName, &currentRole, &currentStatus, &currentQuota, &currentTrafficUploadQuota, &currentTrafficDownloadQuota, &currentCreated, &currentLastLogin)

	var updateReq struct {
		Role                 *string `json:"role"`
		Name                 *string `json:"name"`
		QuotaTotal           *int64  `json:"quota_total"`
		TrafficUploadQuota   *int64  `json:"traffic_upload_quota"`
		TrafficDownloadQuota *int64  `json:"traffic_download_quota"`
		IsActive             *bool   `json:"is_active"`
		IsStaff              *bool   `json:"is_staff"`
	}
	if err := c.ShouldBindJSON(&updateReq); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	if updateReq.Role != nil {
		newRole := *updateReq.Role
		validRoles := map[string]bool{"admin": true, "user": true, "readonly": true, "guest": true}
		if newRole == "superadmin" {
			role, _ := h.permMiddleware.GetUserOrgRole(callerOrgID, callerUserID)
			if !middleware.IsPlatformSuperAdmin(callerOrgID, role) {
				c.JSON(http.StatusForbidden, gin.H{"error": "only superadmin can assign superadmin role"})
				return
			}
			if userOrgID != middleware.PlatformOrgID {
				c.JSON(http.StatusForbidden, gin.H{"error": "superadmin role is reserved for the platform organization"})
				return
			}
			validRoles["superadmin"] = true
		}
		if !validRoles[newRole] {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid role"})
			return
		}
		currentRole = newRole
		if err := h.db.Session().Query(`
			UPDATE users SET role = ? WHERE org_id = ? AND user_id = ?
		`, newRole, userOrgID, userID).Exec(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update user"})
			return
		}
	}

	if updateReq.IsStaff != nil {
		nextRole := applyLegacyStaffToggle(currentRole, *updateReq.IsStaff)
		if nextRole != currentRole {
			currentRole = nextRole
			if err := h.db.Session().Query(`
				UPDATE users SET role = ? WHERE org_id = ? AND user_id = ?
			`, currentRole, userOrgID, userID).Exec(); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update user"})
				return
			}
		}
	}

	if updateReq.Name != nil {
		currentName = *updateReq.Name
		if err := h.db.Session().Query(`
			UPDATE users SET name = ? WHERE org_id = ? AND user_id = ?
		`, currentName, userOrgID, userID).Exec(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update user"})
			return
		}
	}

	// Compute new quota values for validation before writing.
	newQuota := currentQuota
	if updateReq.QuotaTotal != nil {
		newQuota = *updateReq.QuotaTotal
	}
	newUploadQuota := currentTrafficUploadQuota
	if updateReq.TrafficUploadQuota != nil {
		newUploadQuota = *updateReq.TrafficUploadQuota
	}
	newDownloadQuota := currentTrafficDownloadQuota
	if updateReq.TrafficDownloadQuota != nil {
		newDownloadQuota = *updateReq.TrafficDownloadQuota
	}

	// Validate user quotas against org limits.
	oq, quotaErr := readAndValidateUserQuotaLimits(h.db, userOrgID, newQuota, newUploadQuota, newDownloadQuota)
	if quotaErr != nil {
		c.JSON(quotaErr.StatusCode, gin.H{"error": quotaErr.Message})
		return
	}

	if newQuota != currentQuota {
		currentQuota = newQuota
		if err := h.db.Session().Query(`
			UPDATE users SET quota_bytes = ? WHERE org_id = ? AND user_id = ?
		`, currentQuota, userOrgID, userID).Exec(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update user"})
			return
		}
	}

	if newUploadQuota != currentTrafficUploadQuota {
		currentTrafficUploadQuota = newUploadQuota
		if err := h.db.Session().Query(`
			UPDATE users SET traffic_upload_quota = ? WHERE org_id = ? AND user_id = ?
		`, currentTrafficUploadQuota, userOrgID, userID).Exec(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update user"})
			return
		}
	}

	if newDownloadQuota != currentTrafficDownloadQuota {
		currentTrafficDownloadQuota = newDownloadQuota
		if err := h.db.Session().Query(`
			UPDATE users SET traffic_download_quota = ? WHERE org_id = ? AND user_id = ?
		`, currentTrafficDownloadQuota, userOrgID, userID).Exec(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update user"})
			return
		}
	}

	if updateReq.IsActive != nil && !*updateReq.IsActive {
		if err := deactivateUser(h.db, h.sessions, h.apiKeys, userOrgID, userID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update user"})
			return
		}
		currentStatus = "deactivated"
	} else if updateReq.IsActive != nil && *updateReq.IsActive {
		if err := activateUser(h.db, userOrgID, userID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update user"})
			return
		}
		currentStatus = "active"
	}

	resp := makeAdminUserResponse(email, currentName, currentRole, currentStatus, currentQuota, traffic.ReadStorageUsed(h.db, fmt.Sprintf("user:%s:%s", userOrgID, userID)), currentCreated, currentLastLogin)
	resp.TrafficUploadQuota = currentTrafficUploadQuota
	resp.TrafficDownloadQuota = currentTrafficDownloadQuota
	resp.OrgStorageQuota = oq.StorageQuota
	resp.OrgTrafficQuota = oq.TrafficQuota
	resp.OrgTrafficUploadQuota = oq.TrafficUploadQuota
	resp.OrgTrafficDownloadQuota = oq.TrafficDownloadQuota
	c.JSON(http.StatusOK, resp)
}

// DeleteUserByEmail soft-deletes a user by email (grace period → GC cascade).
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

	// Don't allow deleting yourself
	if userID == callerUserID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot delete your own account"})
		return
	}

	// Soft-delete: mark as "deleted" with timestamp for grace period cascade
	if err := softDeleteUser(h.db, h.sessions, h.apiKeys, userOrgID, userID, time.Now()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete user"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// RestoreUser restores a soft-deleted user (within grace period).
// PUT /admin/users/:email/restore/
func (h *AdminHandler) RestoreUser(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")

	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	email := h.getResolvedUserParam(c)
	if !strings.Contains(email, "@") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "email address required"})
		return
	}

	userID, userOrgID, err := h.lookupUserByEmail(email)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	// Verify user is actually in "deleted" state
	var currentStatus string
	if err := h.db.Session().Query(`
		SELECT status FROM users WHERE org_id = ? AND user_id = ?
	`, userOrgID, userID).Scan(&currentStatus); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch user"})
		return
	}
	if currentStatus != StatusDeleted {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user is not in deleted state"})
		return
	}

	if err := activateUser(h.db, userOrgID, userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to restore user"})
		return
	}
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
	if middleware.IsPlatformSuperAdmin(callerOrgID, callerRole) {
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
			SELECT user_id, email, name, role, status, quota_bytes, created_at, last_login_at
			FROM users WHERE org_id = ?
		`, orgID).Iter()

		var userID, email, name, role, status string
		var quotaBytes int64
		var createdAt, lastLoginAt time.Time

		for iter.Scan(&userID, &email, &name, &role, &status, &quotaBytes, &createdAt, &lastLoginAt) {
			if !seen[email] && middleware.IsPlatformSuperAdmin(orgID, middleware.OrganizationRole(role)) {
				seen[email] = true
				admins = append(admins, makeAdminUserResponse(email, name, role, status, quotaBytes, traffic.ReadStorageUsed(h.db, fmt.Sprintf("user:%s:%s", orgID, userID)), createdAt, lastLoginAt))
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

		if userOrgID != middleware.PlatformOrgID {
			failed = append(failed, gin.H{"email": email, "error_msg": "superadmin role is reserved for the platform organization"})
			continue
		}

		// Set role to superadmin
		if err := h.db.Session().Query(`UPDATE users SET role = ? WHERE org_id = ? AND user_id = ?`,
			"superadmin", userOrgID, userID).Exec(); err != nil {
			failed = append(failed, gin.H{"email": email, "error_msg": "failed to update user"})
			continue
		}

		// Read back updated user data
		var name, role, status string
		var quotaBytes int64
		var createdAt, lastLoginAt time.Time
		if err := h.db.Session().Query(`
			SELECT name, role, status, quota_bytes, created_at, last_login_at
			FROM users WHERE org_id = ? AND user_id = ?
		`, userOrgID, userID).Scan(&name, &role, &status, &quotaBytes, &createdAt, &lastLoginAt); err != nil {
			failed = append(failed, gin.H{"email": email, "error_msg": "failed to read user"})
			continue
		}
		success = append(success, makeAdminUserResponse(email, name, role, status, quotaBytes, traffic.ReadStorageUsed(h.db, fmt.Sprintf("user:%s:%s", userOrgID, userID)), createdAt, lastLoginAt))
	}

	if success == nil {
		success = []adminUserResponse{}
	}
	if failed == nil {
		failed = []gin.H{}
	}

	c.JSON(http.StatusOK, gin.H{"success": success, "failed": failed})

}
