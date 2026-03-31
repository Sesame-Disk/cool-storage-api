package v2

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	"github.com/Sesame-Disk/sesamefs/internal/traffic"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
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

	if middleware.IsPlatformSuperAdmin(callerOrgID, role) {
		// Platform superadmin can access any org
		return nil
	}

	if callerOrgID == middleware.PlatformOrgID {
		// Platform user but not superadmin
		c.JSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
		c.Abort()
		return fmt.Errorf("insufficient permissions")
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
	ID                   string `json:"id"`
	Email                string `json:"email"`
	Name                 string `json:"name"`
	ContactEmail         string `json:"owner_contact_email"`
	Status               string `json:"status"`
	IsActive             bool   `json:"is_active"`
	IsOrgStaff           bool   `json:"is_org_staff"`
	Role                 string `json:"role"`
	QuotaTotal           int64  `json:"quota_total"`
	QuotaUsage           int64  `json:"quota_usage"`
	TrafficUploadQuota   int64  `json:"traffic_upload_quota"`
	TrafficDownloadQuota int64  `json:"traffic_download_quota"`
	Ctime                string `json:"ctime"`
	LastLogin            string `json:"last_login"`
	OrgID                string `json:"org_id"`
	AvatarURL            string `json:"avatar_url"`
	// Org-level quota limits (populated only in single-user responses).
	OrgStorageQuota         int64 `json:"org_storage_quota,omitempty"`
	OrgTrafficQuota         int64 `json:"org_traffic_quota,omitempty"` // combined upload+download
	OrgTrafficUploadQuota   int64 `json:"org_traffic_upload_quota,omitempty"`
	OrgTrafficDownloadQuota int64 `json:"org_traffic_download_quota,omitempty"`
}

func buildOrgUserRow(email, name, role, status, orgID string, quota, used int64, created, lastLogin time.Time) orgUserRow {
	canonicalStatus := normalizeUserStatus(status)
	return orgUserRow{
		ID:           email, // Seafile uses email as user ID in org-admin context
		Email:        email,
		Name:         name,
		ContactEmail: "",
		Status:       canonicalStatus,
		IsActive:     canonicalStatus == StatusActive,
		IsOrgStaff:   middleware.IsOrgStaff(role),
		Role:         role,
		QuotaTotal:   quota,
		QuotaUsage:   used,
		Ctime:        created.Format(time.RFC3339),
		LastLogin:    formatOptionalTimestamp(lastLogin),
		OrgID:        orgID,
		AvatarURL:    "/static/img/default-avatar.png",
	}
}

func buildOrgUserRowWithTraffic(email, name, role, status, orgID string, quota, used, trafficUploadQuota, trafficDownloadQuota int64, created, lastLogin time.Time) orgUserRow {
	row := buildOrgUserRow(email, name, role, status, orgID, quota, used, created, lastLogin)
	row.TrafficUploadQuota = trafficUploadQuota
	row.TrafficDownloadQuota = trafficDownloadQuota
	return row
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

	var name, plan, quotaPolicy, billingCycle string
	var storageQuota int64
	var trafficQuota, trafficUploadQuota, trafficDownloadQuota int64
	var maxUsers int
	var createdAt time.Time
	var currentPeriodStartedAt, currentPeriodEndsAt *time.Time

	err = h.db.Session().Query(`
		SELECT name, storage_quota, created_at,
		       traffic_quota, traffic_upload_quota, traffic_download_quota,
		       max_users, plan, quota_policy, billing_cycle,
		       current_period_started_at, current_period_ends_at
		FROM organizations WHERE org_id = ?
	`, orgID).Scan(&name, &storageQuota, &createdAt,
		&trafficQuota, &trafficUploadQuota, &trafficDownloadQuota,
		&maxUsers, &plan, &quotaPolicy, &billingCycle,
		&currentPeriodStartedAt, &currentPeriodEndsAt)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "organization not found"})
		return
	}

	periodStartedAt := traffic.EffectivePeriodStart(currentPeriodStartedAt, time.Now().UTC())
	periodUsage := traffic.ReadOrgPeriodUsage(h.db, orgID, periodStartedAt)
	monthlyUsage := traffic.ReadOrgMonthlyUsage(h.db, orgID, traffic.CurrentMonth())
	yearlyUsage := traffic.MonthlyTransferUsage{}
	now := time.Now().UTC()
	for month := time.January; month <= now.Month(); month++ {
		usage := traffic.ReadOrgMonthlyUsage(h.db, orgID, time.Date(now.Year(), month, 1, 0, 0, 0, 0, time.UTC).Format("200601"))
		yearlyUsage.Combined += usage.Combined
		yearlyUsage.Upload += usage.Upload
		yearlyUsage.Download += usage.Download
	}

	storageSnapshot := traffic.ReadStorageSnapshot(h.db, fmt.Sprintf("org:%s", orgID))
	usersCount := h.countOrgMembers(orgID)
	activeUsersCount := h.countOrgActiveMembers(orgID)

	var reposCount int
	iter := h.db.Session().Query(`SELECT library_id FROM libraries WHERE org_id = ?`, orgID).Iter()
	var libraryID gocql.UUID
	for iter.Scan(&libraryID) {
		reposCount++
	}
	_ = iter.Close()

	var groupsCount int
	iter = h.db.Session().Query(`SELECT group_id FROM groups WHERE org_id = ?`, orgID).Iter()
	var groupID gocql.UUID
	for iter.Scan(&groupID) {
		groupsCount++
	}
	_ = iter.Close()

	c.JSON(http.StatusOK, gin.H{
		"org_id":            orgID,
		"org_name":          name,
		"storage_quota":     storageQuota,
		"storage_usage":     storageSnapshot.BytesUsed,
		"total_files_count": storageSnapshot.FileCount,
		"repos_count":       reposCount,
		"groups_count":      groupsCount,
		"member_usage":      usersCount,
		"member_quota":      h.getOrgSettingInt(orgID, "max_user_number", 0),
		"active_members":    activeUsersCount,
		"ctime":             createdAt.Format(time.RFC3339),
		// Traffic quota info
		"plan":                      plan,
		"quota_policy":              quotaPolicy,
		"billing_cycle":             billingCycle,
		"current_period_started_at": currentPeriodStartedAt,
		"current_period_ends_at":    currentPeriodEndsAt,
		"traffic_quota":             trafficQuota,
		"traffic_month_total":       monthlyUsage.Combined,
		"traffic_month_upload":      monthlyUsage.Upload,
		"traffic_month_download":    monthlyUsage.Download,
		"traffic_combined_used":     periodUsage.Combined,
		"traffic_upload_quota":      trafficUploadQuota,
		"traffic_upload_used":       periodUsage.Upload,
		"traffic_download_quota":    trafficDownloadQuota,
		"traffic_download_used":     periodUsage.Download,
		"traffic_year_total":        yearlyUsage.Combined,
		"traffic_year_upload":       yearlyUsage.Upload,
		"traffic_year_download":     yearlyUsage.Download,
		"max_users":                 maxUsers,
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

	var body struct {
		OrgName              string `json:"org_name"`
		MaxUserNumber        *int   `json:"max_user_number"`
		TrafficQuota         *int64 `json:"traffic_quota"`
		TrafficUploadQuota   *int64 `json:"traffic_upload_quota"`
		TrafficDownloadQuota *int64 `json:"traffic_download_quota"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	if body.MaxUserNumber != nil || body.TrafficQuota != nil || body.TrafficUploadQuota != nil || body.TrafficDownloadQuota != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "organization limits are managed by billing"})
		return
	}

	if body.OrgName != "" {
		if err := h.db.Session().Query(`
			UPDATE organizations SET name = ? WHERE org_id = ?
		`, body.OrgName, orgID).Exec(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update organization"})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
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

func (h *OrgAdminHandler) countOrgActiveMembers(orgID string) int {
	count := 0
	iter := h.db.Session().Query(`SELECT user_id, status FROM users WHERE org_id = ?`, orgID).Iter()
	var userID, status string
	for iter.Scan(&userID, &status) {
		if IsUserUsable(status) {
			count++
		}
	}
	_ = iter.Close()
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

var allowedOrgSettingKeys = map[string]bool{
	"file_ext_white_list": true,
	"logo_path":           true,
}

func (h *OrgAdminHandler) updateOrgSetting(orgID, key, value string) error {
	if !allowedOrgSettingKeys[key] {
		return fmt.Errorf("unsupported org setting key: %q", key)
	}
	query := fmt.Sprintf("UPDATE organizations SET settings['%s'] = ? WHERE org_id = ?", key)
	return h.db.Session().Query(query, value, orgID).Exec()
}

// ============================================================================
// TransferOrgOwnership transfers org ownership from the current owner to another admin.
// PUT /org/:org_id/admin/transfer-ownership/
// Body: {"new_owner": "user@example.com"}
// Allowed callers:
//   - current org owner
//   - platform superadmin (can also bootstrap ownership when an org has no owner)
func (h *OrgAdminHandler) TransferOrgOwnership(c *gin.Context) {
	orgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, orgID); err != nil {
		return
	}

	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	callerPlatformRole, err := h.permMiddleware.GetUserOrgRole(callerOrgID, callerUserID)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
		return
	}
	isSuperAdmin := middleware.IsPlatformSuperAdmin(callerOrgID, callerPlatformRole)

	var existingOwnerUserID string
	iter := h.db.Session().Query(`
		SELECT user_id, role FROM users WHERE org_id = ?
	`, orgID).Iter()
	var iterUserID string
	var iterRole string
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

	callerRole := middleware.RoleOwner
	if !isSuperAdmin {
		var roleErr error
		callerRole, roleErr = h.permMiddleware.GetUserOrgRole(orgID, callerUserID)
		if roleErr != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
			return
		}
	}

	var req struct {
		NewOwner string `json:"new_owner"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.NewOwner == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "new_owner (email) is required"})
		return
	}

	newOwnerUserID, err := h.lookupOrgUserByEmail(orgID, req.NewOwner)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	newRole, err := h.permMiddleware.GetUserOrgRole(orgID, newOwnerUserID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "new owner must be an admin"})
		return
	}

	plan, planErr := buildOwnershipTransferPlan(isSuperAdmin, callerUserID, existingOwnerUserID, newOwnerUserID, callerRole, newRole)
	if planErr != nil {
		status := http.StatusBadRequest
		if planErr.Error() == "only the organization owner or a superadmin can transfer ownership" {
			status = http.StatusForbidden
		}
		c.JSON(status, gin.H{"error": planErr.Error()})
		return
	}

	if plan.NoOp {
		c.JSON(http.StatusOK, gin.H{
			"success":   true,
			"new_owner": req.NewOwner,
		})
		return
	}

	// Swap roles atomically: existing owner → admin (if any), new owner → owner.
	batch := h.db.Session().Batch(gocql.LoggedBatch)
	if plan.DemoteOwnerID != "" {
		batch.Query(`UPDATE users SET role = ? WHERE org_id = ? AND user_id = ?`,
			"admin", orgID, plan.DemoteOwnerID)
	}
	batch.Query(`UPDATE users SET role = ? WHERE org_id = ? AND user_id = ?`,
		"owner", orgID, plan.PromoteUserID)
	if err := batch.Exec(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to transfer ownership"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"new_owner": req.NewOwner,
	})
}
