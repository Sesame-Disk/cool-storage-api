package v2

import (
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
	"github.com/Sesame-Disk/sesamefs/internal/traffic"
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
	sessions       SessionInvalidator
	serverURL      string
}

// NewAdminHandler creates a new AdminHandler
func NewAdminHandler(database *db.DB, cfg *config.Config, perm *middleware.PermissionMiddleware, tokenCreator TokenCreator, sessions SessionInvalidator, serverURL string) *AdminHandler {
	return &AdminHandler{
		db:             database,
		config:         cfg,
		permMiddleware: perm,
		tokenCreator:   tokenCreator,
		sessions:       sessions,
		serverURL:      serverURL,
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

	statusFilter := strings.TrimSpace(strings.ToLower(c.DefaultQuery("status", "")))
	if statusFilter == "all" {
		statusFilter = ""
	}
	if statusFilter != "" && statusFilter != StatusActive && statusFilter != StatusDeactivated && statusFilter != StatusDeleted {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid status filter"})
		return
	}

	iter := h.db.Session().Query(`
		SELECT org_id, name, status, storage_quota, plan, created_at, deleted_at
		FROM organizations
	`).Iter()

	var orgs []gin.H
	var orgID, name, status, plan string
	var storageQuota int64
	var createdAt time.Time
	var deletedAt *time.Time

	for iter.Scan(&orgID, &name, &status, &storageQuota, &plan, &createdAt, &deletedAt) {
		effectiveStatus := status
		if effectiveStatus == "" {
			effectiveStatus = StatusActive
		}
		if statusFilter != "" && effectiveStatus != statusFilter {
			continue
		}

		usersCount := h.countOrgUsers(orgID)
		ownerEmail, ownerName := h.resolveOrgCreator(orgID)
		var deletedAtStr interface{}
		if deletedAt != nil {
			deletedAtStr = deletedAt.Format(time.RFC3339)
		}
		orgs = append(orgs, gin.H{
			"org_id":      orgID,
			"org_name":    name,
			"owner_email": ownerEmail,
			"owner_name":  ownerName,
			"status":      effectiveStatus,
			"deleted_at":  deletedAtStr,
			"plan":        plan,
			"quota_usage": traffic.ReadStorageUsed(h.db, fmt.Sprintf("org:%s", orgID)),
			"quota":       storageQuota,
			"ctime":       createdAt.Format(time.RFC3339),
			"users_count": usersCount,
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
func (h *AdminHandler) CreateOrganization(c *gin.Context) {
	var body struct {
		Name         string `json:"name"`
		OrgName      string `json:"org_name"`
		StorageQuota int64  `json:"storage_quota"`
		OwnerEmail   string `json:"owner_email"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	orgName := strings.TrimSpace(body.OrgName)
	if orgName == "" {
		orgName = strings.TrimSpace(body.Name)
	}
	ownerEmail := strings.TrimSpace(body.OwnerEmail)

	if orgName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "org_name (or name) is required"})
		return
	}
	if h.db == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database not available"})
		return
	}
	template := h.config.GetOrganizationTemplate("")
	storageQuota := template.StorageQuota
	if body.StorageQuota > 0 {
		storageQuota = body.StorageQuota
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
	periodEnd := template.PeriodEnd(now)
	settings := template.Settings
	storageConfig := template.StorageConfig

	orgInsertQuery := `
		INSERT INTO organizations (
			org_id, name, status, settings, storage_quota, storage_used,
			chunking_polynomial, storage_config, created_at,
			plan, quota_policy, billing_cycle,
			traffic_quota, traffic_upload_quota, traffic_download_quota, max_users,
			current_period_started_at, current_period_ends_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`
	orgInsertArgs := []interface{}{
		orgID.String(), orgName, StatusActive, settings,
		storageQuota, int64(0), template.ChunkingPolynomial,
		storageConfig, now,
		template.Plan,
		template.QuotaPolicy,
		template.BillingCycle,
		template.TrafficQuota,
		template.TrafficUploadQuota,
		template.TrafficDownloadQuota,
		template.MaxUsers,
		now,       // current_period_started_at
		periodEnd, // current_period_ends_at
	}

	// ── Optionally create the owner user ──────────────────────────────────
	ownerName := ""
	if ownerEmail != "" {
		ownerName = strings.Split(ownerEmail, "@")[0]
		ownerUserID := uuid.New()

		batch := h.db.Session().Batch(gocql.LoggedBatch)
		batch.Query(orgInsertQuery, orgInsertArgs...)
		batch.Query(`
			INSERT INTO users (org_id, user_id, email, name, role, status, quota_bytes, used_bytes, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, orgID.String(), ownerUserID.String(), ownerEmail, ownerName, "owner", StatusActive,
			storageQuota, int64(0), now)

		batch.Query(`
			INSERT INTO users_by_email (email, user_id, org_id)
			VALUES (?, ?, ?)
		`, ownerEmail, ownerUserID.String(), orgID.String())

		if err := batch.Exec(); err != nil {
			log.Printf("CreateOrganization: failed to create org %s with owner %s: %v",
				orgID, ownerEmail, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create organization"})
			return
		}
	} else {
		if err := h.db.Session().Query(orgInsertQuery, orgInsertArgs...).Exec(); err != nil {
			log.Printf("CreateOrganization: failed to insert org %s: %v", orgName, err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create organization"})
			return
		}
	}

	c.JSON(http.StatusCreated, gin.H{
		"org_id":      orgID.String(),
		"org_name":    orgName,
		"owner_email": ownerEmail,
		"owner_name":  ownerName,
		"status":      StatusActive,
		"deleted_at":  nil,
		"plan":        template.Plan,
		"quota_usage": int64(0),
		"quota":       storageQuota,
		"ctime":       now.Format(time.RFC3339),
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
	var status string
	var storageQuota int64
	var trafficQuota int64
	var trafficUploadQuota int64
	var trafficDownloadQuota int64
	var maxUsers int
	var plan string
	var quotaPolicy string
	var billingCycle string
	var currentPeriodStartedAt *time.Time
	var currentPeriodEndsAt *time.Time
	var settings map[string]string
	var createdAt time.Time
	var deletedAt *time.Time

	err := h.db.Session().Query(`
		SELECT name, status, storage_quota, traffic_quota, traffic_upload_quota,
		       traffic_download_quota, max_users, plan, quota_policy, billing_cycle,
		       current_period_started_at, current_period_ends_at,
		       settings, created_at, deleted_at
		FROM organizations WHERE org_id = ?
	`, orgID).Scan(
		&name,
		&status,
		&storageQuota,
		&trafficQuota,
		&trafficUploadQuota,
		&trafficDownloadQuota,
		&maxUsers,
		&plan,
		&quotaPolicy,
		&billingCycle,
		&currentPeriodStartedAt,
		&currentPeriodEndsAt,
		&settings,
		&createdAt,
		&deletedAt,
	)

	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "organization not found"})
		return
	}

	usersCount := h.countOrgUsers(orgID)
	reposCount := h.countOrgLibraries(orgID)
	groupsCount := h.countOrgGroups(orgID)
	ownerEmail, ownerName := h.resolveOrgCreator(orgID)

	// Extract max_user_number from settings map as backward-compatible fallback.
	maxUserNumber := maxUsers
	if v, ok := settings["max_user_number"]; ok {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			maxUserNumber = parsed
		}
	}

	periodStartedAt := traffic.EffectivePeriodStart(currentPeriodStartedAt, time.Now().UTC())
	periodUsage := traffic.ReadOrgPeriodUsage(h.db, orgID, periodStartedAt)

	effectiveStatus := status
	if effectiveStatus == "" {
		effectiveStatus = StatusActive
	}

	var deletedAtStr interface{}
	if deletedAt != nil {
		deletedAtStr = deletedAt.Format(time.RFC3339)
	}

	c.JSON(http.StatusOK, gin.H{
		"org_id":                    orgID,
		"org_name":                  name,
		"owner_email":               ownerEmail,
		"owner_name":                ownerName,
		"status":                    effectiveStatus,
		"deleted_at":                deletedAtStr,
		"quota_usage":               traffic.ReadStorageUsed(h.db, fmt.Sprintf("org:%s", orgID)),
		"quota":                     storageQuota,
		"storage_quota":             storageQuota,
		"traffic_quota":             trafficQuota,
		"traffic_upload_quota":      trafficUploadQuota,
		"traffic_download_quota":    trafficDownloadQuota,
		"traffic_combined_used":     periodUsage.Combined,
		"traffic_upload_used":       periodUsage.Upload,
		"traffic_download_used":     periodUsage.Download,
		"plan":                      plan,
		"quota_policy":              quotaPolicy,
		"billing_cycle":             billingCycle,
		"current_period_started_at": currentPeriodStartedAt,
		"current_period_ends_at":    currentPeriodEndsAt,
		"ctime":                     createdAt.Format(time.RFC3339),
		"users_count":               usersCount,
		"repos_count":               reposCount,
		"groups_count":              groupsCount,
		"max_users":                 maxUserNumber,
		"max_user_number":           maxUserNumber,
	})
}

// UpdateOrganization updates an organization (superadmin only)
// PUT /admin/organizations/:org_id/
func (h *AdminHandler) UpdateOrganization(c *gin.Context) {
	orgID := c.Param("org_id")

	var req struct {
		Name                   *string    `json:"name"`
		StorageQuota           *int64     `json:"storage_quota"`
		TrafficQuota           *int64     `json:"traffic_quota"`
		TrafficUploadQuota     *int64     `json:"traffic_upload_quota"`
		TrafficDownloadQuota   *int64     `json:"traffic_download_quota"`
		MaxUsers               *int       `json:"max_users"`
		Plan                   *string    `json:"plan"`
		QuotaPolicy            *string    `json:"quota_policy"`
		BillingCycle           *string    `json:"billing_cycle"`
		CurrentPeriodStartedAt *time.Time `json:"current_period_started_at"`
		CurrentPeriodEndsAt    *time.Time `json:"current_period_ends_at"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	// Verify org exists and load current state for audit logging.
	var existingName string
	var existingStorageQuota int64
	var existingTrafficQuota int64
	var existingTrafficUploadQuota int64
	var existingTrafficDownloadQuota int64
	var existingMaxUsers int
	var existingPlan string
	var existingQuotaPolicy string
	var existingBillingCycle string
	var existingCurrentPeriodStartedAt *time.Time
	var existingCurrentPeriodEndsAt *time.Time
	err := h.db.Session().Query(`
		SELECT name, storage_quota, traffic_quota, traffic_upload_quota, traffic_download_quota,
		       max_users, plan, quota_policy, billing_cycle,
		       current_period_started_at, current_period_ends_at
		FROM organizations WHERE org_id = ?
	`, orgID).Scan(
		&existingName,
		&existingStorageQuota,
		&existingTrafficQuota,
		&existingTrafficUploadQuota,
		&existingTrafficDownloadQuota,
		&existingMaxUsers,
		&existingPlan,
		&existingQuotaPolicy,
		&existingBillingCycle,
		&existingCurrentPeriodStartedAt,
		&existingCurrentPeriodEndsAt,
	)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "organization not found"})
		return
	}

	orgUUID, _ := gocql.ParseUUID(orgID)

	type colUpdate struct {
		col string
		val interface{}
	}
	var updates []colUpdate
	auditChanges := map[string]map[string]interface{}{}
	setAuditChange := func(field string, oldVal, newVal interface{}) {
		auditChanges[field] = map[string]interface{}{
			"old": oldVal,
			"new": newVal,
		}
	}
	formatTimePtr := func(value *time.Time) interface{} {
		if value == nil {
			return nil
		}
		return value.UTC().Format(time.RFC3339)
	}

	if req.Name != nil {
		updates = append(updates, colUpdate{"name", *req.Name})
		if *req.Name != existingName {
			setAuditChange("name", existingName, *req.Name)
		}
	}
	if req.StorageQuota != nil {
		updates = append(updates, colUpdate{"storage_quota", *req.StorageQuota})
		if *req.StorageQuota != existingStorageQuota {
			setAuditChange("storage_quota", existingStorageQuota, *req.StorageQuota)
		}
	}
	if req.TrafficQuota != nil {
		updates = append(updates, colUpdate{"traffic_quota", *req.TrafficQuota})
		if *req.TrafficQuota != existingTrafficQuota {
			setAuditChange("traffic_quota", existingTrafficQuota, *req.TrafficQuota)
		}
	}
	if req.TrafficUploadQuota != nil {
		updates = append(updates, colUpdate{"traffic_upload_quota", *req.TrafficUploadQuota})
		if *req.TrafficUploadQuota != existingTrafficUploadQuota {
			setAuditChange("traffic_upload_quota", existingTrafficUploadQuota, *req.TrafficUploadQuota)
		}
	}
	if req.TrafficDownloadQuota != nil {
		updates = append(updates, colUpdate{"traffic_download_quota", *req.TrafficDownloadQuota})
		if *req.TrafficDownloadQuota != existingTrafficDownloadQuota {
			setAuditChange("traffic_download_quota", existingTrafficDownloadQuota, *req.TrafficDownloadQuota)
		}
	}
	if req.MaxUsers != nil {
		updates = append(updates, colUpdate{"max_users", *req.MaxUsers})
		if *req.MaxUsers != existingMaxUsers {
			setAuditChange("max_users", existingMaxUsers, *req.MaxUsers)
		}
	}
	if req.Plan != nil {
		updates = append(updates, colUpdate{"plan", *req.Plan})
		if *req.Plan != existingPlan {
			setAuditChange("plan", existingPlan, *req.Plan)
		}
	}
	if req.QuotaPolicy != nil {
		qp := *req.QuotaPolicy
		if qp != "hard" && qp != "soft" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "quota_policy must be 'hard' or 'soft'"})
			return
		}
		updates = append(updates, colUpdate{"quota_policy", qp})
		if qp != existingQuotaPolicy {
			setAuditChange("quota_policy", existingQuotaPolicy, qp)
		}
	}
	if req.BillingCycle != nil {
		updates = append(updates, colUpdate{"billing_cycle", *req.BillingCycle})
		if *req.BillingCycle != existingBillingCycle {
			setAuditChange("billing_cycle", existingBillingCycle, *req.BillingCycle)
		}
	}
	// Period start and end must be updated together to keep enforcement and
	// reset_date consistent. Accepting only one of the two could leave them
	// pointing at different periods.
	if req.CurrentPeriodStartedAt != nil || req.CurrentPeriodEndsAt != nil {
		if req.CurrentPeriodStartedAt == nil || req.CurrentPeriodEndsAt == nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "current_period_started_at and current_period_ends_at must be provided together",
			})
			return
		}
		if !req.CurrentPeriodEndsAt.After(*req.CurrentPeriodStartedAt) {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "current_period_ends_at must be after current_period_started_at",
			})
			return
		}
		updates = append(updates, colUpdate{"current_period_started_at", *req.CurrentPeriodStartedAt})
		if formatTimePtr(req.CurrentPeriodStartedAt) != formatTimePtr(existingCurrentPeriodStartedAt) {
			setAuditChange("current_period_started_at", formatTimePtr(existingCurrentPeriodStartedAt), formatTimePtr(req.CurrentPeriodStartedAt))
		}
		updates = append(updates, colUpdate{"current_period_ends_at", *req.CurrentPeriodEndsAt})
		if formatTimePtr(req.CurrentPeriodEndsAt) != formatTimePtr(existingCurrentPeriodEndsAt) {
			setAuditChange("current_period_ends_at", formatTimePtr(existingCurrentPeriodEndsAt), formatTimePtr(req.CurrentPeriodEndsAt))
		}
	}

	if len(updates) == 0 {
		c.JSON(http.StatusOK, gin.H{"success": true})
		return
	}

	batch := h.db.Session().Batch(gocql.LoggedBatch)
	for _, u := range updates {
		query := fmt.Sprintf("UPDATE organizations SET %s = ? WHERE org_id = ?", u.col)
		batch.Query(query, u.val, orgUUID)
	}

	actorID := c.GetString("user_id")
	if actorID == "" {
		actorID = "service-token"
	}
	auditDetails, _ := json.Marshal(map[string]interface{}{
		"org_id":          orgID,
		"changes":         auditChanges,
		"override_source": "manual-superadmin",
	})
	batch.Query(`
		INSERT INTO audit_log (org_id, timestamp, action, target_type, target_id, actor_id, details)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, orgUUID, time.Now().UTC(), "organization.update", "organization", orgID, actorID, string(auditDetails))

	if err := batch.Exec(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update organization"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// SoftDeleteOrganization soft-deletes an organization (superadmin only).
// Sets status="deleted" + deleted_at; after OrgGraceDays the GC scanner
// will cascade-delete all resources.
// DELETE /admin/organizations/:org_id/
// POST   /admin/organizations/:org_id/delete/  (alias, kept for backward compat)
func (h *AdminHandler) SoftDeleteOrganization(c *gin.Context) {
	orgID := c.Param("org_id")

	if orgID == middleware.PlatformOrgID {
		c.JSON(http.StatusForbidden, gin.H{"error": "cannot delete platform organization"})
		return
	}
	if h.db == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database not available"})
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

	if err := softDeleteOrg(h.db, h.sessions, orgID, time.Now()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete organization"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// DeactivateOrganization sets an organization to status="deactivated" (superadmin only).
// This is a reversible, non-destructive operation — no grace period, no GC cascade.
// Use POST .../reactivate/ to reverse.
// POST /admin/organizations/:org_id/deactivate/
func (h *AdminHandler) DeactivateOrganization(c *gin.Context) {
	orgID := c.Param("org_id")

	if orgID == middleware.PlatformOrgID {
		c.JSON(http.StatusForbidden, gin.H{"error": "cannot deactivate platform organization"})
		return
	}
	if h.db == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database not available"})
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

	if err := deactivateOrg(h.db, h.sessions, orgID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to deactivate organization"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// RestoreOrganization restores a soft-deleted org (within grace period).
// POST /admin/organizations/:org_id/restore/
func (h *AdminHandler) RestoreOrganization(c *gin.Context) {
	orgID := c.Param("org_id")
	if h.db == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database not available"})
		return
	}

	// Verify org exists and is actually in "deleted" state
	var orgStatus string
	err := h.db.Session().Query(`
		SELECT status FROM organizations WHERE org_id = ?
	`, orgID).Scan(&orgStatus)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "organization not found"})
		return
	}
	if orgStatus != StatusDeleted {
		c.JSON(http.StatusBadRequest, gin.H{"error": "organization is not deleted"})
		return
	}

	if err := activateOrg(h.db, orgID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to restore organization"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ReactivateOrganization reactivates a deactivated org (status="deactivated" → "active").
// Does NOT restore soft-deleted orgs — use /restore/ for that.
// POST /admin/organizations/:org_id/reactivate/
func (h *AdminHandler) ReactivateOrganization(c *gin.Context) {
	orgID := c.Param("org_id")
	if h.db == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "database not available"})
		return
	}

	var orgStatus string
	err := h.db.Session().Query(`
		SELECT status FROM organizations WHERE org_id = ?
	`, orgID).Scan(&orgStatus)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "organization not found"})
		return
	}
	if orgStatus != StatusDeactivated {
		c.JSON(http.StatusBadRequest, gin.H{"error": "organization is not deactivated"})
		return
	}

	if err := activateOrg(h.db, orgID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to reactivate organization"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ListOrgUsers lists users in an organization.
// Platform superadmin can list users for any organization.
// GET /admin/organizations/:org_id/users/
func (h *AdminHandler) ListOrgUsers(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")

	statusFilter, ok := parseUserStatusFilter(c)
	if !ok {
		return
	}

	// Check permissions: only platform superadmin can access /admin routes.
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
		if err != nil || !middleware.IsPlatformSuperAdmin(callerOrgID, role) {
			c.JSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
			c.Abort()
			return
		}
	}

	iter := h.db.Session().Query(`
		SELECT user_id, email, name, role, status, quota_bytes, created_at, last_login_at
		FROM users WHERE org_id = ?
	`, targetOrgID).Iter()

	var users []gin.H
	var userID, email, name, role, status string
	var quotaBytes int64
	var createdAt, lastLoginAt time.Time

	for iter.Scan(&userID, &email, &name, &role, &status, &quotaBytes, &createdAt, &lastLoginAt) {
		if !userMatchesStatusFilter(status, statusFilter) {
			continue
		}
		isActive := IsUserUsable(status)
		isOrgStaff := middleware.IsOrgStaff(role)
		users = append(users, gin.H{
			"email":        email,
			"name":         name,
			"role":         role,
			"status":       normalizeUserStatus(status),
			"active":       isActive,
			"is_org_staff": isOrgStaff,
			"quota_usage":  traffic.ReadStorageUsed(h.db, fmt.Sprintf("user:%s:%s", targetOrgID, userID)),
			"quota_total":  quotaBytes,
			"create_time":  createdAt.Format(time.RFC3339),
			"last_login":   formatOptionalTimestamp(lastLoginAt),
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
			if strings.HasPrefix(subResource, "restore") {
				h.RestoreUser(c)
			} else {
				h.UpdateUser(c)
			}
		case "DELETE":
			h.SoftDeleteUser(c)
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
			h.SoftDeleteUser(c)
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
	var quotaBytes int64
	var createdAt, lastLoginAt time.Time

	if callerOrgID == middleware.PlatformOrgID {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "use /admin/organizations/:org_id/users/ to list users by org"})
		return
	}

	// /admin is platform-scoped, so direct user ID lookups stay within the platform org.
	err := h.db.Session().Query(`
		SELECT email, name, role, quota_bytes, created_at, last_login_at
		FROM users WHERE org_id = ? AND user_id = ?
	`, callerOrgID, targetUserID).Scan(&email, &name, &role, &quotaBytes, &createdAt, &lastLoginAt)
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
		"used_bytes":  traffic.ReadStorageUsed(h.db, fmt.Sprintf("user:%s:%s", userOrgID, targetUserID)),
		"created_at":  createdAt,
		"last_login":  formatOptionalTimestamp(lastLoginAt),
	})
}

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

	// /admin is platform-scoped, so direct user ID updates operate within the platform org.
	orgID := callerOrgID

	// Validate role if provided
	if req.Role != nil {
		validRoles := map[string]bool{"admin": true, "user": true, "readonly": true, "guest": true}
		// Only superadmin can assign superadmin role
		if *req.Role == "superadmin" {
			role, _ := h.permMiddleware.GetUserOrgRole(callerOrgID, callerUserID)
			if !middleware.IsPlatformSuperAdmin(callerOrgID, role) {
				c.JSON(http.StatusForbidden, gin.H{"error": "only superadmin can assign superadmin role"})
				return
			}
			if orgID != middleware.PlatformOrgID {
				c.JSON(http.StatusForbidden, gin.H{"error": "superadmin role is reserved for the platform organization"})
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

// SoftDeleteUser soft-deletes a user (status="deleted", sets deleted_at).
// After UserGraceDays the GC scanner will cascade-delete all resources.
// DELETE /admin/users/:user_id/
// If :user_id contains an @ sign, it's treated as an email lookup (seafile-js compatible).
func (h *AdminHandler) SoftDeleteUser(c *gin.Context) {
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

	// Don't allow deleting yourself
	if targetUserID == callerUserID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot delete your own account"})
		return
	}

	orgID := callerOrgID

	// Soft-delete: mark as "deleted" with timestamp for grace period cascade
	if err := softDeleteUser(h.db, h.sessions, orgID, targetUserID, time.Now()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete user"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// requireAdminAccess checks that the caller is a platform superadmin.
// Returns a non-nil error (and writes the response) if not authorized.
func (h *AdminHandler) requireAdminAccess(c *gin.Context, callerOrgID, callerUserID string) error {
	role, err := h.permMiddleware.GetUserOrgRole(callerOrgID, callerUserID)
	if err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
		c.Abort()
		return err
	}
	if !middleware.IsPlatformSuperAdmin(callerOrgID, role) {
		c.JSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
		c.Abort()
		return fmt.Errorf("insufficient permissions: role %s org %s", role, callerOrgID)
	}
	c.Set("user_org_role", role)
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
	if err := h.db.Session().Query(`
		INSERT INTO users_by_email (email, user_id, org_id) VALUES (?, ?, ?)
	`, email, userID, orgID).Exec(); err != nil {
		log.Printf("[lookupUserByEmail] failed to backfill users_by_email for %s: %v", email, err)
	}

	err = nil
	return
}
