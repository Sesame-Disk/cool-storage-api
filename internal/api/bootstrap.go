package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	"github.com/Sesame-Disk/sesamefs/internal/plans"
	"github.com/Sesame-Disk/sesamefs/internal/traffic"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
)

// handleBootstrap returns app bootstrap data for the SPA.
//
// This endpoint is public (no auth required). It returns the authenticated
// user's identity and org config so the frontend can populate
// window.app.pageOptions and window.org.pageOptions without relying on
// Go-side HTML string injection.
//
// GET /api/v2.1/bootstrap/
func (s *Server) handleBootstrap(c *gin.Context) {
	userID, orgID, role := s.resolveUserAuth(c)
	email := s.resolveUserEmail(c)
	resolvedOrgID, resolvedOrgName := s.resolveOrgForPanel(c)
	if orgID == "" {
		orgID = resolvedOrgID
	}
	orgName := resolvedOrgName
	appPageOptions := s.buildAppBootstrapPageOptions(userID, orgID, role, email)
	orgPageOptions := s.buildOrgBootstrapPageOptions(orgID, orgName)
	canAccessOrgAdmin := middleware.IsOrgStaff(role)
	canAccessSysAdmin := middleware.IsPlatformSuperAdmin(orgID, middleware.OrganizationRole(role))

	c.JSON(http.StatusOK, gin.H{
		"username":              email,
		"org_id":                orgID,
		"org_name":              orgName,
		"app_page_options":      appPageOptions,
		"page_options":          orgPageOptions,
		"org_page_options":      orgPageOptions,
		"sysadmin_page_options": s.buildSysAdminBootstrapPageOptions(canAccessSysAdmin),
		"permissions": gin.H{
			"isAuthenticated":   userID != "" && orgID != "",
			"canAccessOrgAdmin": canAccessOrgAdmin,
			"canAccessSysAdmin": canAccessSysAdmin,
		},
	})
}

func (s *Server) buildAppBootstrapPageOptions(userID, orgID, role, fallbackEmail string) gin.H {
	pageOptions := gin.H{
		"name":                    "",
		"username":                fallbackEmail,
		"contactEmail":            fallbackEmail,
		"userRole":                role,
		"canAddRepo":              false,
		"canShareRepo":            false,
		"canAddGroup":             false,
		"canGenerateShareLink":    false,
		"canGenerateUploadLink":   false,
		"canSendShareLinkEmail":   false,
		"canSendShareLinkMail":    false,
		"canInviteGuest":          false,
		"canInvitePeople":         false,
		"canPublishRepo":          false,
		"canViewOrg":              false,
		"isSystemStaff":           false,
		"plan":                    "",
		"isOrgOwner":              false,
		"canUpgrade":              false,
		"billingCycle":            "",
		"maxUsers":                0,
		"currentUsers":            0,
		"currentPeriodStartedAt":  nil,
		"currentPeriodEndsAt":     nil,
		"upgradeFeatures":         []string{},
		"shareLinkExpireDaysMax":  0,
		"uploadLinkExpireDaysMax": 0,
		"storageInfo":             nil,
		"trafficInfo":             nil,
		"enableSubscription":      true,
	}

	if s.db == nil || userID == "" || orgID == "" {
		return pageOptions
	}

	orgUUID, err := gocql.ParseUUID(orgID)
	if err != nil {
		return pageOptions
	}
	userUUID, err := gocql.ParseUUID(userID)
	if err != nil {
		return pageOptions
	}

	var email, name string
	err = s.db.Session().Query(`
		SELECT email, name, role, quota_bytes,
		       traffic_upload_quota, traffic_download_quota
		FROM users WHERE org_id = ? AND user_id = ?
	`, orgUUID, userUUID).Scan(&email, &name, &role, new(int64),
		new(int64), new(int64))
	if err != nil {
		email = fallbackEmail
		if email == "" {
			email = userID + "@sesamefs.local"
		}
		name = userID
		role = "user"
	}

	if name == "" {
		if atIdx := strings.Index(email, "@"); atIdx > 0 {
			name = email[:atIdx]
		} else {
			name = email
		}
	}

	var orgPlan, billingCycle, quotaPolicy string
	var storageQuota, trafficQuota, trafficUploadQuota, trafficDownloadQuota int64
	var maxUsers int
	var currentPeriodStartedAt, currentPeriodEndsAt *time.Time
	_ = s.db.Session().Query(`
		SELECT plan, billing_cycle, quota_policy, storage_quota,
		       traffic_quota, traffic_upload_quota, traffic_download_quota, max_users,
		       current_period_started_at, current_period_ends_at
		FROM organizations WHERE org_id = ?
	`, orgUUID).Scan(&orgPlan, &billingCycle, &quotaPolicy, &storageQuota,
		&trafficQuota, &trafficUploadQuota, &trafficDownloadQuota, &maxUsers,
		&currentPeriodStartedAt, &currentPeriodEndsAt)

	orgStorageUsed := traffic.ReadStorageUsed(s.db, fmt.Sprintf("org:%s", orgID))
	now := time.Now().UTC()
	periodStartedAt := traffic.EffectivePeriodStart(currentPeriodStartedAt, now)
	orgTrafficUsage := traffic.ReadOrgPeriodUsage(s.db, orgID, periodStartedAt)

	var currentUsers int
	_ = s.db.Session().Query(`SELECT COUNT(*) FROM users WHERE org_id = ?`, orgUUID).Scan(&currentUsers)

	profile := s.config.GetEnforcementProfile(quotaPolicy)
	resolved := plans.ResolveCapabilities(role, profile)

	var storagePct float64
	storageOverQuota := false
	if storageQuota > 0 {
		storagePct = float64(orgStorageUsed) / float64(storageQuota) * 100
		storageOverQuota = orgStorageUsed > storageQuota
	}

	var trafficPct float64
	trafficOverQuota := false
	if trafficQuota > 0 {
		trafficPct = float64(orgTrafficUsage.Combined) / float64(trafficQuota) * 100
		trafficOverQuota = orgTrafficUsage.Combined > trafficQuota
	}
	uploadOverQuota := trafficUploadQuota > 0 && orgTrafficUsage.Upload > trafficUploadQuota
	downloadOverQuota := trafficDownloadQuota > 0 && orgTrafficUsage.Download > trafficDownloadQuota

	isOrgOwner := role == "owner"
	canUpgrade := plans.ComputeCanUpgrade(role, quotaPolicy, storagePct, trafficPct, storageOverQuota, trafficOverQuota)
	isStaff := middleware.IsPlatformSuperAdmin(orgID, middleware.OrganizationRole(role))
	canViewOrg := middleware.IsOrgStaff(role)
	canInviteGuest := resolved.Capabilities["can_invite_guest"]
	canSendShareLinkEmail := resolved.Capabilities["can_send_share_link_mail"]

	pageOptions["name"] = name
	pageOptions["username"] = email
	pageOptions["contactEmail"] = email
	pageOptions["userRole"] = role
	pageOptions["canAddRepo"] = resolved.Capabilities["can_add_repo"]
	pageOptions["canShareRepo"] = resolved.Capabilities["can_share_repo"]
	pageOptions["canAddGroup"] = resolved.Capabilities["can_add_group"]
	pageOptions["canGenerateShareLink"] = resolved.Capabilities["can_generate_share_link"]
	pageOptions["canGenerateUploadLink"] = resolved.Capabilities["can_generate_upload_link"]
	pageOptions["canSendShareLinkEmail"] = canSendShareLinkEmail
	pageOptions["canSendShareLinkMail"] = canSendShareLinkEmail
	pageOptions["canInviteGuest"] = canInviteGuest
	pageOptions["canInvitePeople"] = canInviteGuest
	pageOptions["canPublishRepo"] = resolved.Capabilities["can_publish_repo"]
	pageOptions["canViewOrg"] = canViewOrg
	pageOptions["isSystemStaff"] = isStaff
	pageOptions["plan"] = orgPlan
	pageOptions["isOrgOwner"] = isOrgOwner
	pageOptions["canUpgrade"] = canUpgrade
	pageOptions["billingCycle"] = billingCycle
	pageOptions["maxUsers"] = maxUsers
	pageOptions["currentUsers"] = currentUsers
	pageOptions["currentPeriodStartedAt"] = currentPeriodStartedAt
	pageOptions["currentPeriodEndsAt"] = currentPeriodEndsAt
	pageOptions["upgradeFeatures"] = resolved.UpgradeFeatures
	pageOptions["shareLinkExpireDaysMax"] = resolved.Limits.ShareLinkExpireDaysMax
	pageOptions["uploadLinkExpireDaysMax"] = resolved.Limits.UploadLinkExpireDaysMax
	pageOptions["storageInfo"] = gin.H{
		"used":       orgStorageUsed,
		"quota":      storageQuota,
		"percent":    storagePct,
		"over_quota": storageOverQuota,
	}
	pageOptions["trafficInfo"] = gin.H{
		"used":                orgTrafficUsage.Combined,
		"quota":               trafficQuota,
		"percent":             trafficPct,
		"over_quota":          trafficOverQuota,
		"upload_used":         orgTrafficUsage.Upload,
		"upload_quota":        trafficUploadQuota,
		"upload_over_quota":   uploadOverQuota,
		"download_used":       orgTrafficUsage.Download,
		"download_quota":      trafficDownloadQuota,
		"download_over_quota": downloadOverQuota,
		"reset_date":          traffic.EffectiveTrafficResetDate(currentPeriodEndsAt, now),
	}

	return pageOptions
}

func (s *Server) buildOrgBootstrapPageOptions(orgID, orgName string) gin.H {
	pageOptions := gin.H{
		"orgID":                    orgID,
		"orgName":                  orgName,
		"invitationLink":           "",
		"orgMemberQuotaEnabled":    "False",
		"orgMembers":               0,
		"orgMembersQuota":          0,
		"hasUserAvailability":      true,
		"orgEnableAdminCustomLogo": "False",
		"orgEnableAdminCustomName": "False",
		"orgEnableAdminInviteUser": "False",
		"enableMultiADFS":          "False",
		"enableSubscription":       false,
	}

	if s.db == nil || orgID == "" {
		return pageOptions
	}

	var settings map[string]string
	var maxUsers int
	if err := s.db.Session().Query(`
		SELECT name, settings, max_users
		FROM organizations WHERE org_id = ?
	`, orgID).Scan(&orgName, &settings, &maxUsers); err != nil {
		return pageOptions
	}

	currentUsers := 0
	_ = s.db.Session().Query(`SELECT COUNT(*) FROM users WHERE org_id = ?`, orgID).Scan(&currentUsers)

	effectiveMaxUsers := maxUsers
	if settings != nil {
		if rawMaxUsers, ok := settings["max_user_number"]; ok {
			if parsedMaxUsers, err := strconv.Atoi(rawMaxUsers); err == nil && parsedMaxUsers > 0 {
				effectiveMaxUsers = parsedMaxUsers
			}
		}
	}

	hasUserAvailability := effectiveMaxUsers <= 0 || currentUsers < effectiveMaxUsers

	pageOptions["orgName"] = orgName
	pageOptions["orgMemberQuotaEnabled"] = boolString(effectiveMaxUsers > 0)
	pageOptions["orgMembers"] = currentUsers
	pageOptions["orgMembersQuota"] = effectiveMaxUsers
	pageOptions["hasUserAvailability"] = hasUserAvailability
	pageOptions["orgEnableAdminCustomLogo"] = "True"
	pageOptions["orgEnableAdminCustomName"] = "True"
	pageOptions["orgEnableAdminInviteUser"] = "True"
	pageOptions["enableSubscription"] = true

	return pageOptions
}

func (s *Server) buildSysAdminBootstrapPageOptions(canAccessSysAdmin bool) gin.H {
	adminPermissions := gin.H{
		"can_view_system_info": canAccessSysAdmin,
		"can_view_statistic":   canAccessSysAdmin,
		"can_config_system":    canAccessSysAdmin,
		"can_manage_library":   canAccessSysAdmin,
		"can_manage_user":      canAccessSysAdmin,
		"can_manage_group":     canAccessSysAdmin,
		"can_view_user_log":    canAccessSysAdmin,
		"can_view_admin_log":   canAccessSysAdmin,
		"other_permission":     canAccessSysAdmin,
	}

	return gin.H{
		"constance_enabled":              false,
		"multi_tenancy":                  true,
		"multi_institution":              false,
		"sysadmin_extra_enabled":         false,
		"enable_guest_invitation":        false,
		"enable_terms_and_conditions":    false,
		"is_default_admin":               canAccessSysAdmin,
		"enable_file_scan":               false,
		"enable_work_weixin":             false,
		"enable_dingtalk":                false,
		"enableSysAdminViewRepo":         true,
		"haveLDAP":                       false,
		"enable_share_link_report_abuse": false,
		"twoFactorAuthEnabled":           false,
		"trashReposExpireDays":           30,
		"availableRoles":                 []string{"default", "user", "admin", "guest", "readonly"},
		"availableAdminRoles":            []string{"superadmin"},
		"institutions":                   []string{},
		"admin_permissions":              adminPermissions,
	}
}

func boolString(enabled bool) string {
	if enabled {
		return "True"
	}
	return "False"
}
