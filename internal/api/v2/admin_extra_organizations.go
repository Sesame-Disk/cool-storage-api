package v2

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	"github.com/Sesame-Disk/sesamefs/internal/traffic"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
)

func (h *AdminHandler) AdminSearchOrganizations(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	query := strings.ToLower(c.Query("query"))

	iter := h.db.Session().Query(`
		SELECT org_id, name, status, storage_quota, plan, created_at
		FROM organizations
	`).Iter()

	var orgs []gin.H
	var orgID, name, status, plan string
	var storageQuota int64
	var createdAt time.Time

	for iter.Scan(&orgID, &name, &status, &storageQuota, &plan, &createdAt) {
		if query != "" && !strings.Contains(strings.ToLower(name), query) {
			continue
		}
		effectiveStatus := status
		if effectiveStatus == "" {
			effectiveStatus = StatusActive
		}
		usersCount := h.countOrgUsers(orgID)
		ownerEmail, ownerName := h.resolveOrgCreator(orgID)
		orgs = append(orgs, gin.H{
			"org_id":      orgID,
			"org_name":    name,
			"owner_email": ownerEmail,
			"owner_name":  ownerName,
			"status":      effectiveStatus,
			"plan":        plan,
			"quota_usage": traffic.ReadStorageUsed(h.db, fmt.Sprintf("org:%s", orgID)),
			"quota":       storageQuota,
			"ctime":       createdAt.Format(time.RFC3339),
			"users_count": usersCount,
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

// AdminAddOrgUser creates a user in an organization.
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

	var existingUID string
	if err := h.db.Session().Query(`
		SELECT user_id FROM users_by_email WHERE email = ?
	`, req.Email).Scan(&existingUID); err == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user with this email already exists"})
		return
	}

	if checker := traffic.GetChecker(); checker != nil {
		if st, _ := checker.CheckMaxUsers(targetOrgID); !st.Allowed {
			c.JSON(http.StatusForbidden, gin.H{"error": "user limit reached for this organization"})
			return
		}
	}

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
		"last_login":   formatOptionalTimestamp(time.Time{}),
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

	userID, _, err := h.lookupUserByEmail(email)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	var name, role, status string
	var quotaBytes int64
	var createdAt, lastLoginAt time.Time

	if err := h.db.Session().Query(`
		SELECT name, role, status, quota_bytes, created_at, last_login_at
		FROM users WHERE org_id = ? AND user_id = ?
	`, targetOrgID, userID).Scan(&name, &role, &status, &quotaBytes, &createdAt, &lastLoginAt); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	var updateReq struct {
		Active               *bool   `json:"active"`
		IsOrgStaff           *bool   `json:"is_org_staff"`
		IsStaff              *bool   `json:"is_staff"`
		Name                 *string `json:"name"`
		Role                 *string `json:"role"`
		QuotaTotal           *int64  `json:"quota_total"`
		TrafficUploadQuota   *int64  `json:"traffic_upload_quota"`
		TrafficDownloadQuota *int64  `json:"traffic_download_quota"`
	}
	if err := c.ShouldBindJSON(&updateReq); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	if updateReq.Active != nil {
		if !*updateReq.Active {
			if err := deactivateUser(h.db, h.sessions, targetOrgID, userID); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update user"})
				return
			}
			status = StatusDeactivated
		} else {
			if err := activateUser(h.db, targetOrgID, userID); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update user"})
				return
			}
			status = StatusActive
		}
	}

	staffPtr := updateReq.IsOrgStaff
	if staffPtr == nil {
		staffPtr = updateReq.IsStaff
	}
	if staffPtr != nil {
		role = applyLegacyStaffToggle(role, *staffPtr)
		if err := h.db.Session().Query(`UPDATE users SET role = ? WHERE org_id = ? AND user_id = ?`,
			role, targetOrgID, userID).Exec(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update user"})
			return
		}
	}

	if updateReq.Name != nil && *updateReq.Name != "" {
		name = *updateReq.Name
		if err := h.db.Session().Query(`UPDATE users SET name = ? WHERE org_id = ? AND user_id = ?`,
			name, targetOrgID, userID).Exec(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update user"})
			return
		}
	}

	if updateReq.Role != nil && *updateReq.Role == string(middleware.RoleOwner) {
		var existingOwnerUserID string
		iter := h.db.Session().Query(`SELECT user_id, role FROM users WHERE org_id = ?`, targetOrgID).Iter()
		var iterUserID, iterRole string
		for iter.Scan(&iterUserID, &iterRole) {
			if iterRole == string(middleware.RoleOwner) {
				existingOwnerUserID = iterUserID
				break
			}
		}
		if err := iter.Close(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to inspect organization owner"})
			return
		}

		newOwnerRole := middleware.OrganizationRole(role)
		if !middleware.HasRequiredOrgRole(newOwnerRole, middleware.RoleAdmin) {
			newOwnerRole = middleware.RoleAdmin
		}
		plan, planErr := buildOwnershipTransferPlan(true, callerUserID, existingOwnerUserID, userID, middleware.RoleSuperAdmin, newOwnerRole)
		if planErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": planErr.Error()})
			return
		}
		if !plan.NoOp {
			batch := h.db.Session().Batch(gocql.LoggedBatch)
			if plan.DemoteOwnerID != "" {
				batch.Query(`UPDATE users SET role = ? WHERE org_id = ? AND user_id = ?`, "admin", targetOrgID, plan.DemoteOwnerID)
			}
			batch.Query(`UPDATE users SET role = ? WHERE org_id = ? AND user_id = ?`, "owner", targetOrgID, plan.PromoteUserID)
			if err := batch.Exec(); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to transfer ownership"})
				return
			}
		}
		role = string(middleware.RoleOwner)
	} else if updateReq.Role != nil {
		validRoles := map[string]bool{"admin": true, "user": true, "readonly": true, "guest": true}
		if validRoles[*updateReq.Role] {
			role = *updateReq.Role
			if err := h.db.Session().Query(`UPDATE users SET role = ? WHERE org_id = ? AND user_id = ?`,
				role, targetOrgID, userID).Exec(); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update user"})
				return
			}
		}
	}

	originalQuota := quotaBytes
	if updateReq.QuotaTotal != nil {
		quotaBytes = *updateReq.QuotaTotal
	}

	var trafficUploadQuota, trafficDownloadQuota int64
	_ = h.db.Session().Query(
		`SELECT traffic_upload_quota, traffic_download_quota FROM users WHERE org_id = ? AND user_id = ?`,
		targetOrgID, userID,
	).Scan(&trafficUploadQuota, &trafficDownloadQuota)

	newUploadQuota := trafficUploadQuota
	newDownloadQuota := trafficDownloadQuota
	if updateReq.TrafficUploadQuota != nil {
		newUploadQuota = *updateReq.TrafficUploadQuota
	}
	if updateReq.TrafficDownloadQuota != nil {
		newDownloadQuota = *updateReq.TrafficDownloadQuota
	}

	oq, quotaErr := readAndValidateUserQuotaLimits(h.db, targetOrgID, quotaBytes, newUploadQuota, newDownloadQuota)
	if quotaErr != nil {
		c.JSON(quotaErr.StatusCode, gin.H{"error": quotaErr.Message})
		return
	}

	if quotaBytes != originalQuota {
		if err := h.db.Session().Query(`UPDATE users SET quota_bytes = ? WHERE org_id = ? AND user_id = ?`,
			quotaBytes, targetOrgID, userID).Exec(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update user"})
			return
		}
	}

	if newUploadQuota != trafficUploadQuota {
		if err := h.db.Session().Query(`UPDATE users SET traffic_upload_quota = ? WHERE org_id = ? AND user_id = ?`,
			newUploadQuota, targetOrgID, userID).Exec(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update user"})
			return
		}
		trafficUploadQuota = newUploadQuota
	}

	if newDownloadQuota != trafficDownloadQuota {
		if err := h.db.Session().Query(`UPDATE users SET traffic_download_quota = ? WHERE org_id = ? AND user_id = ?`,
			newDownloadQuota, targetOrgID, userID).Exec(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update user"})
			return
		}
		trafficDownloadQuota = newDownloadQuota
	}

	isActive := IsUserUsable(status)
	isOrgStaff := middleware.IsOrgStaff(role)

	c.JSON(http.StatusOK, gin.H{
		"email":                      email,
		"name":                       name,
		"role":                       role,
		"status":                     normalizeUserStatus(status),
		"active":                     isActive,
		"is_org_staff":               isOrgStaff,
		"quota_usage":                traffic.ReadStorageUsed(h.db, fmt.Sprintf("user:%s:%s", targetOrgID, userID)),
		"quota_total":                quotaBytes,
		"traffic_upload_quota":       trafficUploadQuota,
		"traffic_download_quota":     trafficDownloadQuota,
		"org_storage_quota":          oq.StorageQuota,
		"org_traffic_quota":          oq.TrafficQuota,
		"org_traffic_upload_quota":   oq.TrafficUploadQuota,
		"org_traffic_download_quota": oq.TrafficDownloadQuota,
		"create_time":                createdAt.Format(time.RFC3339),
		"last_login":                 formatOptionalTimestamp(lastLoginAt),
		"org_id":                     targetOrgID,
	})
}

// AdminDeleteOrgUser removes a user from an organization.
// DELETE /admin/organizations/:org_id/users/:email/
func (h *AdminHandler) AdminDeleteOrgUser(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	targetOrgID := c.Param("org_id")
	email := c.Param("email")

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

	if err := softDeleteUser(h.db, h.sessions, targetOrgID, foundUID, time.Now()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete user"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// AdminListOrgGroups lists groups in an organization.
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
		ownerName := ownerEmail
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
