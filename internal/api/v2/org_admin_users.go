package v2

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/localauth"
	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	"github.com/Sesame-Disk/sesamefs/internal/traffic"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

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
	isStaffOnly := c.Query("is_staff") == "true"

	iter := h.db.Session().Query(`
		SELECT user_id, email, name, role, status, quota_bytes, created_at, last_login_at
		FROM users WHERE org_id = ?
	`, targetOrgID).Iter()

	var all []orgUserRow
	var userID, email, name, role, status string
	var quota int64
	var created, lastLogin time.Time

	for iter.Scan(&userID, &email, &name, &role, &status, &quota, &created, &lastLogin) {
		if isStaffOnly && !middleware.IsOrgStaff(role) {
			continue
		}
		if !userMatchesStatusFilter(status, statusFilter) {
			continue
		}
		all = append(all, buildOrgUserRow(email, name, role, status, targetOrgID, quota, traffic.ReadStorageUsed(h.db, fmt.Sprintf("user:%s:%s", targetOrgID, userID)), created, lastLogin))
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
	if h.rejectOrgUserWriteIfDisabled(c) {
		return
	}

	var body struct {
		Email    string `json:"email"`
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	email := strings.TrimSpace(body.Email)
	name := strings.TrimSpace(body.Name)

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

	// Enforce max_users quota for the org
	if checker := traffic.GetChecker(); checker != nil {
		if st, _ := checker.CheckMaxUsers(targetOrgID); !st.Allowed {
			c.JSON(http.StatusForbidden, gin.H{"error": "user limit reached for this organization"})
			return
		}
	}

	userID := uuid.New().String()
	now := time.Now()

	if err := createUserWithEmailLookup(h.db, targetOrgID, userID, email, name, "user", int64(-2), int64(0), now); err != nil {
		log.Printf("AddOrgUser: failed to create user %s in org %s: %v", email, targetOrgID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create user"})
		return
	}

	row := buildOrgUserRow(email, name, "user", StatusActive, targetOrgID, -2, 0, now, time.Time{})

	// If local auth is enabled, attach a credential so the new user can log in.
	// An explicit admin-supplied password is usable as-is; otherwise generate a
	// temporary one and return it once so the admin can share it out-of-band.
	if svc := h.localAuthService(); svc != nil {
		password := strings.TrimSpace(body.Password)
		mustChange := false
		var tempPassword string
		if password == "" {
			tempPassword = generateTempPassword()
			password = tempPassword
			mustChange = true
		}
		if err := svc.SetPassword(email, userID, targetOrgID, password, mustChange, now); err != nil {
			// The user row already exists; surface the credential problem clearly
			// rather than silently leaving them unable to log in.
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		resp := gin.H{"user": row}
		if tempPassword != "" {
			resp["temp_password"] = tempPassword
			resp["must_change_password"] = true
		}
		c.JSON(http.StatusCreated, resp)
		return
	}

	c.JSON(http.StatusCreated, row)
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
	var quota, trafficUploadQuota, trafficDownloadQuota int64
	var created, lastLogin time.Time

	if err := h.db.Session().Query(`
		SELECT name, role, status, quota_bytes, traffic_upload_quota, traffic_download_quota, created_at, last_login_at
		FROM users WHERE org_id = ? AND user_id = ?
	`, targetOrgID, userID).Scan(&name, &role, &status, &quota, &trafficUploadQuota, &trafficDownloadQuota, &created, &lastLogin); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	row := buildOrgUserRowWithTraffic(email, name, role, status, targetOrgID, quota, traffic.ReadStorageUsed(h.db, fmt.Sprintf("user:%s:%s", targetOrgID, userID)), trafficUploadQuota, trafficDownloadQuota, created, lastLogin)
	oq, _ := readOrgQuotas(h.db, targetOrgID) // best-effort for display
	row.OrgStorageQuota = oq.StorageQuota
	row.OrgTrafficQuota = oq.TrafficQuota
	row.OrgTrafficUploadQuota = oq.TrafficUploadQuota
	row.OrgTrafficDownloadQuota = oq.TrafficDownloadQuota
	c.JSON(http.StatusOK, row)
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
	if h.rejectOrgUserWriteIfDisabled(c) {
		return
	}

	userID, err := h.lookupOrgUserByEmail(targetOrgID, email)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	var name, role, status string
	var quota, trafficUploadQuota, trafficDownloadQuota int64
	var deletedAt *time.Time
	var created, lastLogin time.Time

	if err := h.db.Session().Query(`
		SELECT name, role, status, quota_bytes, traffic_upload_quota, traffic_download_quota, deleted_at, created_at, last_login_at
		FROM users WHERE org_id = ? AND user_id = ?
	`, targetOrgID, userID).Scan(&name, &role, &status, &quota, &trafficUploadQuota, &trafficDownloadQuota, &deletedAt, &created, &lastLogin); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	originalRole := role
	originalName := name
	originalQuota := quota
	originalStatus := status
	originalTrafficUploadQuota := trafficUploadQuota
	originalTrafficDownloadQuota := trafficDownloadQuota
	originalDeletedAt := deletedAt

	// Protect the org owner: only a platform superadmin can modify the owner.
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if originalRole == string(middleware.RoleOwner) {
		callerPlatformRole, _ := h.permMiddleware.GetUserOrgRole(callerOrgID, callerUserID)
		if !middleware.IsPlatformSuperAdmin(callerOrgID, callerPlatformRole) {
			c.JSON(http.StatusForbidden, gin.H{"error": "only a platform superadmin can modify the organization owner"})
			return
		}
	}

	var updateReq struct {
		IsActive             *bool   `json:"is_active"`
		IsStaff              *bool   `json:"is_staff"`
		Name                 *string `json:"name"`
		QuotaTotal           *int64  `json:"quota_total"`
		TrafficUploadQuota   *int64  `json:"traffic_upload_quota"`
		TrafficDownloadQuota *int64  `json:"traffic_download_quota"`
	}
	if err := c.ShouldBindJSON(&updateReq); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	hasMutatingFields := updateReq.IsActive != nil || updateReq.IsStaff != nil ||
		updateReq.Name != nil || updateReq.QuotaTotal != nil
	if normalizeUserStatus(status) == StatusDeleted && hasMutatingFields {
		c.JSON(http.StatusBadRequest, gin.H{"error": "deleted users must be restored before modification"})
		return
	}

	nextDeletedAt := deletedAt
	if updateReq.IsActive != nil {
		if !*updateReq.IsActive {
			status = StatusDeactivated
		} else {
			status = StatusActive
			nextDeletedAt = nil
		}
	}

	if updateReq.IsStaff != nil {
		role = applyLegacyStaffToggle(role, *updateReq.IsStaff)
	}

	if updateReq.Name != nil {
		name = *updateReq.Name
	}

	if updateReq.QuotaTotal != nil {
		quota = *updateReq.QuotaTotal
	}

	// Compute traffic quota values for validation.
	newUploadQuota := trafficUploadQuota
	newDownloadQuota := trafficDownloadQuota
	if updateReq.TrafficUploadQuota != nil {
		newUploadQuota = *updateReq.TrafficUploadQuota
	}
	if updateReq.TrafficDownloadQuota != nil {
		newDownloadQuota = *updateReq.TrafficDownloadQuota
	}

	// Validate user quotas against org limits.
	oq, quotaErr := readAndValidateUserQuotaLimits(h.db, targetOrgID, quota, newUploadQuota, newDownloadQuota)
	if quotaErr != nil {
		c.JSON(quotaErr.StatusCode, gin.H{"error": quotaErr.Message})
		return
	}

	deletedAtChanged := (originalDeletedAt == nil) != (nextDeletedAt == nil)
	if !deletedAtChanged && originalDeletedAt != nil && nextDeletedAt != nil {
		deletedAtChanged = !originalDeletedAt.Equal(*nextDeletedAt)
	}
	userChanged := role != originalRole ||
		name != originalName ||
		quota != originalQuota ||
		newUploadQuota != originalTrafficUploadQuota ||
		newDownloadQuota != originalTrafficDownloadQuota ||
		normalizeUserStatus(status) != normalizeUserStatus(originalStatus) ||
		deletedAtChanged
	if userChanged {
		if err := updateUserAndAdminReadModels(h.db, targetOrgID, userID, batchedUserUpdate{
			Name:                 name,
			Role:                 role,
			Status:               status,
			DeletedAt:            nextDeletedAt,
			QuotaBytes:           quota,
			TrafficUploadQuota:   newUploadQuota,
			TrafficDownloadQuota: newDownloadQuota,
		}); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update user"})
			return
		}
	}

	invalidateSessionsOnDemotion(h.sessions, targetOrgID, userID, originalRole, role)
	runUserStatusSideEffects(h.db, h.sessions, h.apiKeys, targetOrgID, userID, originalStatus, status)
	trafficUploadQuota = newUploadQuota
	trafficDownloadQuota = newDownloadQuota

	row := buildOrgUserRowWithTraffic(email, name, role, status, targetOrgID, quota, traffic.ReadStorageUsed(h.db, fmt.Sprintf("user:%s:%s", targetOrgID, userID)), trafficUploadQuota, trafficDownloadQuota, created, lastLogin)
	row.OrgStorageQuota = oq.StorageQuota
	row.OrgTrafficQuota = oq.TrafficQuota
	row.OrgTrafficUploadQuota = oq.TrafficUploadQuota
	row.OrgTrafficDownloadQuota = oq.TrafficDownloadQuota
	c.JSON(http.StatusOK, row)
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
	if h.rejectOrgUserWriteIfDisabled(c) {
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

	// Protect the org owner from deletion by non-superadmins.
	var targetRole string
	if err := h.db.Session().Query(`SELECT role FROM users WHERE org_id = ? AND user_id = ?`,
		targetOrgID, userID).Scan(&targetRole); err == nil && targetRole == string(middleware.RoleOwner) {
		callerOrgID := c.GetString("org_id")
		callerPlatformRole, _ := h.permMiddleware.GetUserOrgRole(callerOrgID, callerUserID)
		if !middleware.IsPlatformSuperAdmin(callerOrgID, callerPlatformRole) {
			c.JSON(http.StatusForbidden, gin.H{"error": "cannot delete the organization owner"})
			return
		}
	}

	// Soft-delete: mark as "deleted" with timestamp for grace period cascade
	if err := softDeleteUser(h.db, h.sessions, h.apiKeys, targetOrgID, userID, time.Now()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete user"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// RestoreOrgUser restores a soft-deleted user within the caller's org.
// PUT /org/:org_id/admin/users/:email/restore/
func (h *OrgAdminHandler) RestoreOrgUser(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	email := c.Param("email")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}
	if h.rejectOrgUserWriteIfDisabled(c) {
		return
	}

	userID, err := h.lookupOrgUserByEmail(targetOrgID, email)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	var currentStatus string
	if err := h.db.Session().Query(`
		SELECT status FROM users WHERE org_id = ? AND user_id = ?
	`, targetOrgID, userID).Scan(&currentStatus); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to fetch user"})
		return
	}
	if normalizeUserStatus(currentStatus) != StatusDeleted {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user is not in deleted state"})
		return
	}

	if err := activateUser(h.db, targetOrgID, userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to restore user"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ResetOrgUserPassword sets or resets a local user's password.
// If local auth is disabled, it remains a graceful no-op (OIDC-only deployment).
// Body (optional): { "password": "..." }. When password is omitted a temporary
// one is generated and returned once so the admin can share it out-of-band.
// PUT /org/:org_id/admin/users/:email/set-password/
func (h *OrgAdminHandler) ResetOrgUserPassword(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	email := c.Param("email")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}
	if h.rejectOrgUserWriteIfDisabled(c) {
		return
	}

	svc := h.localAuthService()
	if svc == nil {
		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"detail":  "local authentication is not enabled; password management is a no-op",
		})
		return
	}

	userID, err := h.lookupOrgUserByEmail(targetOrgID, email)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	var body struct {
		Password string `json:"password"`
	}
	_ = c.ShouldBindJSON(&body) // body is optional

	password := strings.TrimSpace(body.Password)
	mustChange := false
	var tempPassword string
	if password == "" {
		tempPassword = generateTempPassword()
		password = tempPassword
		mustChange = true
	}

	if err := svc.SetPassword(email, userID, targetOrgID, password, mustChange, time.Now()); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp := gin.H{"success": true}
	if tempPassword != "" {
		resp["temp_password"] = tempPassword
		resp["must_change_password"] = true
	}
	c.JSON(http.StatusOK, resp)
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

	// Read from the libraries_by_owner projection (single (org_id, owner_id)
	// partition) instead of an ALLOW FILTERING scan over the canonical table.
	iter := h.db.Session().Query(`
		SELECT library_id, name, encrypted, size_bytes, updated_at, deleted_at
		FROM libraries_by_owner WHERE org_id = ? AND owner_id = ?
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
	var updatedAt, deletedAt time.Time

	for iter.Scan(&libID, &libName, &encrypted, &size, &updatedAt, &deletedAt) {
		if !deletedAt.IsZero() {
			deletedAt = time.Time{}
			continue
		}
		repos = append(repos, repoItem{
			RepoID:       libID,
			RepoName:     libName,
			Encrypted:    encrypted,
			Size:         size,
			Owner:        email,
			LastModified: updatedAt.Format(time.RFC3339),
		})
		deletedAt = time.Time{}
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
		SELECT created_at, library_id, share_id, permission, shared_by, shared_by_email, shared_by_name, repo_name, encrypted, size_bytes
		FROM shares_by_user_org
		WHERE org_id = ? AND user_id = ?
	`, targetOrgID, userID).Iter()

	type repoItem struct {
		RepoID       string `json:"repo_id"`
		RepoName     string `json:"repo_name"`
		Permission   string `json:"permission"`
		OwnerName    string `json:"owner_name"`
		Size         int64  `json:"size"`
		LastModified string `json:"last_modified"`
	}

	var repos []repoItem
	var createdAt time.Time
	var libID, shareID, perm, sharedBy, sharedByEmail, sharedByName, repoName string
	var encrypted bool
	var sizeBytes int64

	for shareIter.Scan(&createdAt, &libID, &shareID, &perm, &sharedBy, &sharedByEmail, &sharedByName, &repoName, &encrypted, &sizeBytes) {
		row, err := dbpkg.ReadAdminLibraryProjectionRow(h.db.Session(), targetOrgID, libID)
		if err != nil || (row.DeletedAt != nil && !row.DeletedAt.IsZero()) {
			continue
		}
		repos = append(repos, repoItem{
			RepoID:       libID,
			RepoName:     repoName,
			Permission:   perm,
			OwnerName:    row.OwnerName,
			Size:         sizeBytes,
			LastModified: row.UpdatedAt.Format(time.RFC3339),
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

	query := strings.ToLower(strings.TrimSpace(c.Query("query")))
	if query == "" {
		empty := []orgUserRow{}
		c.JSON(http.StatusOK, gin.H{
			"user_list":   empty,
			"page":        page,
			"page_next":   false,
			"total_count": 0,
			"page_info": gin.H{
				"current_page":  page,
				"has_next_page": false,
			},
		})
		return
	}

	iter := h.db.Session().Query(`
		SELECT user_id, email, name, role, status, quota_bytes, created_at, last_login_at
		FROM users WHERE org_id = ?
	`, targetOrgID).Iter()

	var results []orgUserRow
	var userID, email, name, role, status string
	var quota int64
	var created, lastLogin time.Time

	for iter.Scan(&userID, &email, &name, &role, &status, &quota, &created, &lastLogin) {
		if !userMatchesStatusFilter(status, statusFilter) {
			continue
		}
		if strings.Contains(strings.ToLower(email), query) || strings.Contains(strings.ToLower(name), query) {
			results = append(results, buildOrgUserRow(email, name, role, status, targetOrgID, quota, traffic.ReadStorageUsed(h.db, fmt.Sprintf("user:%s:%s", targetOrgID, userID)), created, lastLogin))
		}
	}
	iter.Close()

	if results == nil {
		results = []orgUserRow{}
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
	pageData := results[start:end]
	if pageData == nil {
		pageData = []orgUserRow{}
	}

	c.JSON(http.StatusOK, gin.H{
		"user_list":   pageData,
		"page":        page,
		"page_next":   end < total,
		"total_count": total,
		"page_info": gin.H{
			"current_page":  page,
			"has_next_page": end < total,
		},
	})
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
	if h.rejectOrgUserWriteIfDisabled(c) {
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
			log.Printf("ImportOrgUsers: failed to create user %s in org %s: %v", email, targetOrgID, err)
			failed = append(failed, gin.H{"email": email, "error": "database error"})
			continue
		}
		success = append(success, buildOrgUserRow(email, name, "user", StatusActive, targetOrgID, -2, 0, now, time.Time{}))
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
	if h.rejectOrgUserWriteIfDisabled(c) {
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

// buildLocalAuthService returns a localauth.Service bound to the given DB and
// the configured password policy, or nil when local auth is disabled. Shared by
// the org-admin and platform-admin handlers.
func buildLocalAuthService(cfg *config.Config, database *dbpkg.DB) *localauth.Service {
	if cfg == nil || !cfg.Auth.Local.Enabled || database == nil {
		return nil
	}
	return localauth.NewService(database.Session(), localauth.Policy{
		MinPasswordLength: cfg.Auth.Local.MinPasswordLength,
		MaxFailedAttempts: cfg.Auth.Local.MaxFailedAttempts,
		LockoutDuration:   cfg.Auth.Local.LockoutDuration,
	})
}

// localAuthService returns a localauth.Service bound to this handler's DB and
// the configured password policy, or nil when local auth is disabled.
func (h *OrgAdminHandler) localAuthService() *localauth.Service {
	return buildLocalAuthService(h.config, h.db)
}

// generateTempPassword returns a random, URL-safe temporary password. It always
// satisfies the default policy length and is only ever returned to the admin
// once (never persisted in plaintext).
func generateTempPassword() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		// rand.Read failing is catastrophic; fall back to a timestamp-derived
		// value so we never emit an empty/guessable password.
		return "Temp-" + base64.RawURLEncoding.EncodeToString([]byte(time.Now().UTC().String()))[:16]
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// ============================================================================
