package v2

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	"github.com/Sesame-Disk/sesamefs/internal/traffic"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func (h *AdminHandler) AdminSearchOrganizations(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	query := strings.ToLower(c.Query("query"))

	rows, err := dbpkg.ListAdminOrganizationRows(h.db.Session(), "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list organizations"})
		return
	}

	var orgs []gin.H
	for _, row := range rows {
		if query != "" && !strings.Contains(strings.ToLower(row.Name), query) {
			continue
		}
		orgs = append(orgs, gin.H{
			"org_id":      row.OrgID,
			"org_name":    row.Name,
			"owner_email": row.OwnerEmail,
			"owner_name":  row.OwnerName,
			"status":      row.Status,
			"plan":        row.Plan,
			"quota_usage": traffic.ReadStorageUsed(h.db, fmt.Sprintf("org:%s", row.OrgID)),
			"quota":       row.StorageQuota,
			"ctime":       row.CreatedAt.Format(time.RFC3339),
			"users_count": row.UsersCount,
		})
	}

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

	userID := uuid.New().String()
	now := time.Now()
	role := "user"

	if req.Name == "" {
		req.Name = strings.Split(req.Email, "@")[0]
	}

	if err := createUserWithEmailLookup(h.db, targetOrgID, userID, req.Email, req.Name, role, int64(-2), int64(0), now); err != nil {
		log.Printf("AdminAddOrgUser: failed to create user %s in org %s: %v", req.Email, targetOrgID, err)
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
	var quotaBytes, trafficUploadQuota, trafficDownloadQuota int64
	var deletedAt *time.Time
	var createdAt, lastLoginAt time.Time

	if err := h.db.Session().Query(`
		SELECT name, role, status, quota_bytes, traffic_upload_quota, traffic_download_quota, deleted_at, created_at, last_login_at
		FROM users WHERE org_id = ? AND user_id = ?
	`, targetOrgID, userID).Scan(&name, &role, &status, &quotaBytes, &trafficUploadQuota, &trafficDownloadQuota, &deletedAt, &createdAt, &lastLoginAt); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}
	originalName := name
	originalRole := role
	originalStatus := status
	originalQuota := quotaBytes
	originalTrafficUploadQuota := trafficUploadQuota
	originalTrafficDownloadQuota := trafficDownloadQuota
	originalDeletedAt := deletedAt

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

	nextDeletedAt := deletedAt
	ownerTransferExecuted := false
	if updateReq.Active != nil {
		if !*updateReq.Active {
			status = StatusDeactivated
		} else {
			status = StatusActive
			nextDeletedAt = nil
		}
	}

	roleBeforeUpdate := role

	staffPtr := updateReq.IsOrgStaff
	if staffPtr == nil {
		staffPtr = updateReq.IsStaff
	}
	if staffPtr != nil {
		role = applyLegacyStaffToggle(role, *staffPtr)
	}

	if updateReq.Name != nil && *updateReq.Name != "" {
		name = *updateReq.Name
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
			promotedRow, err := dbpkg.ReadAdminUserProjectionRow(h.db.Session(), targetOrgID, plan.PromoteUserID)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to build new owner read model"})
				return
			}
			orgProjectionRow, err := dbpkg.ReadAdminOrganizationProjectionRow(h.db.Session(), targetOrgID)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to build organization read model"})
				return
			}
			promotedRow.Role = "owner"
			orgProjectionRow.OwnerEmail = promotedRow.Email
			orgProjectionRow.OwnerName = promotedRow.Name

			batch := h.db.Session().Batch(gocql.LoggedBatch)
			if plan.DemoteOwnerID != "" {
				demotedRow, err := dbpkg.ReadAdminUserProjectionRow(h.db.Session(), targetOrgID, plan.DemoteOwnerID)
				if err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to build previous owner read model"})
					return
				}
				demotedRow.Role = "admin"
				dbpkg.AddUpsertAdminUserReadModelQuery(batch, demotedRow)
			}
			if plan.DemoteOwnerID != "" {
				batch.Query(`UPDATE users SET role = ? WHERE org_id = ? AND user_id = ?`, "admin", targetOrgID, plan.DemoteOwnerID)
			}
			batch.Query(`UPDATE users SET role = ? WHERE org_id = ? AND user_id = ?`, "owner", targetOrgID, plan.PromoteUserID)
			dbpkg.AddUpsertAdminUserReadModelQuery(batch, promotedRow)
			dbpkg.AddUpsertAdminOrganizationReadModelQuery(batch, orgProjectionRow)
			if err := batch.Exec(); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to transfer ownership"})
				return
			}
			// Demoted owner goes from "owner" to "admin" — invalidate their sessions.
			if plan.DemoteOwnerID != "" {
				invalidateSessionsOnDemotion(h.sessions, targetOrgID, plan.DemoteOwnerID, "owner", "admin")
			}
		}
		ownerTransferExecuted = true
		role = string(middleware.RoleOwner)
	} else if updateReq.Role != nil {
		validRoles := map[string]bool{"admin": true, "user": true, "readonly": true, "guest": true}
		if validRoles[*updateReq.Role] {
			role = *updateReq.Role
		}
	}

	invalidateSessionsOnDemotion(h.sessions, targetOrgID, userID, roleBeforeUpdate, role)

	if updateReq.QuotaTotal != nil {
		quotaBytes = *updateReq.QuotaTotal
	}

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

	deletedAtChanged := (originalDeletedAt == nil) != (nextDeletedAt == nil)
	if !deletedAtChanged && originalDeletedAt != nil && nextDeletedAt != nil {
		deletedAtChanged = !originalDeletedAt.Equal(*nextDeletedAt)
	}
	userChanged := originalName != name ||
		originalRole != role ||
		originalQuota != quotaBytes ||
		originalTrafficUploadQuota != newUploadQuota ||
		originalTrafficDownloadQuota != newDownloadQuota ||
		normalizeUserStatus(originalStatus) != normalizeUserStatus(status) ||
		deletedAtChanged
	if userChanged && !ownerTransferExecuted {
		if err := updateUserAndAdminReadModels(h.db, targetOrgID, userID, batchedUserUpdate{
			Name:                 name,
			Role:                 role,
			Status:               status,
			DeletedAt:            nextDeletedAt,
			QuotaBytes:           quotaBytes,
			TrafficUploadQuota:   newUploadQuota,
			TrafficDownloadQuota: newDownloadQuota,
		}); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update user"})
			return
		}
	}

	runUserStatusSideEffects(h.db, h.sessions, h.apiKeys, targetOrgID, userID, originalStatus, status)
	trafficUploadQuota = newUploadQuota
	trafficDownloadQuota = newDownloadQuota

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

	if err := softDeleteUser(h.db, h.sessions, h.apiKeys, targetOrgID, foundUID, time.Now()); err != nil {
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
