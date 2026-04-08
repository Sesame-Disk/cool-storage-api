package api

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/httputil"
	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	"github.com/Sesame-Disk/sesamefs/internal/plans"
	"github.com/Sesame-Disk/sesamefs/internal/traffic"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
)

type bootstrapIdentity struct {
	UserID string
	OrgID  string
	Role   string
	Email  string
}

type bootstrapUserData struct {
	Email string
	Name  string
	Role  string
}

type bootstrapOrgData struct {
	Loaded                 bool
	Name                   string
	Settings               map[string]string
	MaxUsers               int
	CurrentUsers           int
	Plan                   string
	BillingCycle           string
	QuotaPolicy            string
	StorageQuota           int64
	TrafficQuota           int64
	TrafficUploadQuota     int64
	TrafficDownloadQuota   int64
	CurrentPeriodStartedAt *time.Time
	CurrentPeriodEndsAt    *time.Time
}

func buildBootstrapBackendRoutes() gin.H {
	return gin.H{
		"languageChange":        "i18n/?lang={langCode}",
		"passwordChange":        "accounts/password/change/",
		"twoFactorSetup":        "profile/two_factor_authentication/setup/",
		"twoFactorDisable":      "profile/two_factor_authentication/disable/",
		"twoFactorBackupTokens": "profile/two_factor_authentication/backup/tokens/",
		"deleteAccount":         "accounts/delete/",
		"wechatWorkConnect":     "work-weixin/oauth-connect/?next={next}",
		"wechatWorkDisconnect":  "work-weixin/oauth-disconnect/?next={next}",
		"dingtalkConnect":       "dingtalk/connect/?next={next}",
		"dingtalkDisconnect":    "dingtalk/disconnect/?next={next}",
		"samlConnect":           "saml2/connect/?next={next}",
		"samlDisconnect":        "saml2/disconnect/?next={next}",
		"orgSamlConnect":        "org/custom/{orgID}/saml2/connect/?next={next}",
		"orgSamlDisconnect":     "org/custom/{orgID}/saml2/disconnect/?next={next}",
	}
}

// handleBootstrap returns app bootstrap data for the SPA.
//
// This endpoint is public (no auth required). It returns the authenticated
// user's identity and org config so the frontend can populate
// window.app.pageOptions and window.org.pageOptions without relying on
// Go-side HTML string injection.
//
// GET /api/v2.1/bootstrap/
func (s *Server) handleBootstrap(c *gin.Context) {
	identity := s.resolveBootstrapIdentity(c)
	userData := s.loadBootstrapUserData(identity)
	if identity.Email == "" {
		identity.Email = userData.Email
	}
	if identity.Role == "" {
		identity.Role = userData.Role
	}
	orgData := s.loadBootstrapOrgData(identity.OrgID)
	appPageOptions := s.buildAppBootstrapPageOptions(identity, userData, orgData)
	appPageOptions["storages"] = s.buildBootstrapStorageOptions(httputil.GetRoutingHostname(c))
	orgPageOptions := s.buildOrgBootstrapPageOptions(identity.OrgID, orgData)
	canAccessOrgAdmin := middleware.IsOrgStaff(identity.Role)
	canAccessSysAdmin := middleware.IsPlatformSuperAdmin(identity.OrgID, middleware.OrganizationRole(identity.Role))

	c.JSON(http.StatusOK, gin.H{
		"username":              identity.Email,
		"org_id":                identity.OrgID,
		"org_name":              orgData.Name,
		"app_page_options":      appPageOptions,
		"org_page_options":      orgPageOptions,
		"sysadmin_page_options": s.buildSysAdminBootstrapPageOptions(canAccessSysAdmin),
		"permissions": gin.H{
			"isAuthenticated":   identity.UserID != "" && identity.OrgID != "",
			"canAccessOrgAdmin": canAccessOrgAdmin,
			"canAccessSysAdmin": canAccessSysAdmin,
		},
	})
}

func formatBootstrapRegionLabel(region string) string {
	region = strings.TrimSpace(region)
	if region == "" {
		return ""
	}

	parts := strings.FieldsFunc(region, func(r rune) bool {
		return r == '-' || r == '_' || r == ' '
	})
	for i, part := range parts {
		lower := strings.ToLower(part)
		switch lower {
		case "us", "usa", "eu", "uk", "uae":
			parts[i] = strings.ToUpper(lower)
		default:
			parts[i] = strings.ToUpper(lower[:1]) + lower[1:]
		}
	}

	return strings.Join(parts, " ")
}

func (s *Server) resolveBootstrapEndpointRegion(hostname string) string {
	if s == nil || s.config == nil {
		return "default"
	}

	hostname = strings.TrimSpace(hostname)
	if region, ok := s.config.Storage.EndpointRegions[hostname]; ok {
		return region
	}

	for pattern, region := range s.config.Storage.EndpointRegions {
		if len(pattern) > 1 && pattern[0] == '*' {
			suffix := pattern[1:]
			if strings.HasSuffix(hostname, suffix) && len(hostname) > len(suffix) {
				return region
			}
		}
	}

	if region, ok := s.config.Storage.EndpointRegions["*"]; ok {
		return region
	}

	return "default"
}

func (s *Server) bootstrapStorageDisplayName(storageClass string) string {
	storageClass = strings.TrimSpace(storageClass)
	if storageClass == "" || s == nil || s.config == nil {
		return storageClass
	}

	for region, regionConfig := range s.config.Storage.RegionClasses {
		if regionConfig.Hot == storageClass || regionConfig.Cold == storageClass {
			return formatBootstrapRegionLabel(region)
		}
	}

	return storageClass
}

func (s *Server) resolveBootstrapDefaultStorageClass(hostname string) string {
	if s == nil || s.config == nil {
		return ""
	}

	region := s.resolveBootstrapEndpointRegion(hostname)
	if regionConfig, ok := s.config.Storage.RegionClasses[region]; ok && s.isBootstrapKnownStorageClass(regionConfig.Hot) {
		return regionConfig.Hot
	}
	if s.isBootstrapKnownStorageClass(s.config.Storage.DefaultClass) {
		return s.config.Storage.DefaultClass
	}
	classNames := make([]string, 0, len(s.config.Storage.Classes))
	for name := range s.config.Storage.Classes {
		classNames = append(classNames, name)
	}
	sort.Strings(classNames)
	for _, name := range classNames {
		if s.isBootstrapKnownStorageClass(name) {
			return name
		}
	}
	backendNames := make([]string, 0, len(s.config.Storage.Backends))
	for name := range s.config.Storage.Backends {
		backendNames = append(backendNames, name)
	}
	sort.Strings(backendNames)
	for _, name := range backendNames {
		if s.isBootstrapKnownStorageClass(name) {
			return name
		}
	}
	return ""
}

func (s *Server) isBootstrapKnownStorageClass(storageClass string) bool {
	storageClass = strings.TrimSpace(storageClass)
	if storageClass == "" || s == nil || s.config == nil {
		return false
	}
	if _, ok := s.config.Storage.Classes[storageClass]; ok {
		return true
	}
	if _, ok := s.config.Storage.Backends[storageClass]; ok {
		return true
	}
	return false
}

func (s *Server) buildBootstrapStorageOptions(hostname string) []gin.H {
	if s == nil || s.config == nil {
		return []gin.H{}
	}

	defaultClass := s.resolveBootstrapDefaultStorageClass(hostname)
	options := make([]gin.H, 0)
	seen := make(map[string]struct{})
	appendOption := func(id, name string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		option := gin.H{"id": id, "name": name}
		if id == defaultClass {
			option["is_default"] = true
		}
		options = append(options, option)
		seen[id] = struct{}{}
	}

	regions := make([]string, 0, len(s.config.Storage.RegionClasses))
	for region, regionConfig := range s.config.Storage.RegionClasses {
		if strings.TrimSpace(regionConfig.Hot) == "" {
			continue
		}
		regions = append(regions, region)
	}
	sort.Strings(regions)
	for _, region := range regions {
		appendOption(s.config.Storage.RegionClasses[region].Hot, formatBootstrapRegionLabel(region))
	}

	if len(options) == 0 {
		classNames := make([]string, 0, len(s.config.Storage.Classes))
		for className := range s.config.Storage.Classes {
			classNames = append(classNames, className)
		}
		sort.Strings(classNames)
		for _, className := range classNames {
			appendOption(className, className)
		}

		backendNames := make([]string, 0, len(s.config.Storage.Backends))
		for backendName := range s.config.Storage.Backends {
			backendNames = append(backendNames, backendName)
		}
		sort.Strings(backendNames)
		for _, backendName := range backendNames {
			appendOption(backendName, backendName)
		}
	}

	if defaultClass != "" {
		appendOption(defaultClass, s.bootstrapStorageDisplayName(defaultClass))
	}

	return options
}

func (s *Server) resolveBootstrapIdentity(c *gin.Context) bootstrapIdentity {
	token := extractRequestAuthToken(c)

	if matchedToken, ok := s.matchDevToken(token); ok {
		return bootstrapIdentity{
			UserID: matchedToken.UserID,
			OrgID:  matchedToken.OrgID,
			Role:   matchedToken.Role,
			Email:  matchedToken.Email,
		}
	}

	if token != "" && s.authHandler != nil {
		if mgr := s.authHandler.GetSessionManager(); mgr != nil {
			if session, err := mgr.ValidateSession(token); err == nil {
				return bootstrapIdentity{
					UserID: session.UserID,
					OrgID:  session.OrgID,
					Role:   session.Role,
					Email:  session.Email,
				}
			}
		}
	}

	return bootstrapIdentity{Email: extractEmailFromAuthCookie(c)}
}

func (s *Server) loadBootstrapUserData(identity bootstrapIdentity) bootstrapUserData {
	data := bootstrapUserData{
		Email: identity.Email,
		Role:  identity.Role,
	}

	if identity.UserID == "" || identity.OrgID == "" || s.db == nil {
		if data.Email == "" && identity.UserID != "" {
			data.Email = identity.UserID + "@sesamefs.local"
		}
		if data.Name == "" && data.Email != "" {
			if atIdx := strings.Index(data.Email, "@"); atIdx > 0 {
				data.Name = data.Email[:atIdx]
			} else {
				data.Name = data.Email
			}
		}
		return data
	}

	orgUUID, err := gocql.ParseUUID(identity.OrgID)
	if err != nil {
		return data
	}
	userUUID, err := gocql.ParseUUID(identity.UserID)
	if err != nil {
		return data
	}

	if err := s.db.Session().Query(`
		SELECT email, name, role
		FROM users WHERE org_id = ? AND user_id = ?
	`, orgUUID, userUUID).Scan(&data.Email, &data.Name, &data.Role); err != nil {
		if data.Email == "" {
			data.Email = identity.UserID + "@sesamefs.local"
		}
		if data.Role == "" {
			data.Role = "user"
		}
	}

	if data.Name == "" {
		if atIdx := strings.Index(data.Email, "@"); atIdx > 0 {
			data.Name = data.Email[:atIdx]
		} else {
			data.Name = data.Email
		}
	}

	return data
}

func (s *Server) loadBootstrapOrgData(orgID string) bootstrapOrgData {
	if orgID == "" || s.db == nil {
		return bootstrapOrgData{}
	}

	orgUUID, err := gocql.ParseUUID(orgID)
	if err != nil {
		return bootstrapOrgData{}
	}

	data := bootstrapOrgData{}
	if err := s.db.Session().Query(`
		SELECT name, settings, max_users, plan, billing_cycle, quota_policy,
		       storage_quota, traffic_quota, traffic_upload_quota, traffic_download_quota,
		       current_period_started_at, current_period_ends_at
		FROM organizations WHERE org_id = ?
	`, orgUUID).Scan(
		&data.Name,
		&data.Settings,
		&data.MaxUsers,
		&data.Plan,
		&data.BillingCycle,
		&data.QuotaPolicy,
		&data.StorageQuota,
		&data.TrafficQuota,
		&data.TrafficUploadQuota,
		&data.TrafficDownloadQuota,
		&data.CurrentPeriodStartedAt,
		&data.CurrentPeriodEndsAt,
	); err != nil {
		return bootstrapOrgData{}
	}
	data.Loaded = true

	_ = s.db.Session().Query(`SELECT COUNT(*) FROM users WHERE org_id = ?`, orgUUID).Scan(&data.CurrentUsers)

	return data
}

func extractEmailFromAuthCookie(c *gin.Context) string {
	if cookie, err := c.Cookie("sesamefs_auth"); err == nil && cookie != "" {
		if idx := strings.LastIndex(cookie, "@"); idx >= 0 && idx < len(cookie)-1 {
			return cookie[:idx]
		}
	}

	return ""
}

func (s *Server) buildAppBootstrapPageOptions(identity bootstrapIdentity, userData bootstrapUserData, orgData bootstrapOrgData) gin.H {
	role := identity.Role
	if userData.Role != "" {
		role = userData.Role
	}
	email := identity.Email
	if userData.Email != "" {
		email = userData.Email
	}
	name := userData.Name
	if name == "" {
		name = identity.UserID
	}
	authenticated := identity.UserID != "" && identity.OrgID != ""
	hasPasswordChange := strings.TrimSpace(s.config.Accounts.PasswordChangeURL) != ""
	hasDeleteAccount := strings.TrimSpace(s.config.Accounts.DeleteAccountURL) != ""

	pageOptions := gin.H{
		"name":                      name,
		"username":                  email,
		"contactEmail":              email,
		"loginID":                   email,
		"avatarURL":                 "/static/img/default-avatar.png",
		"nameLabel":                 "Name:",
		"enableUpdateUserInfo":      authenticated,
		"enableUserSetContactEmail": false,
		"enableUserSetName":         authenticated,
		"canUpdatePassword":         authenticated && hasPasswordChange,
		"passwordOperationText":     "Change",
		"enableAPIKeys":             authenticated,
		"enableDeleteAccount":       authenticated && hasDeleteAccount,
		"langCode":                  "en",
		"currentLang": gin.H{
			"langCode": "en",
			"langName": "en",
		},
		"backendRoutes":           buildBootstrapBackendRoutes(),
		"inlinePreviewExtensions": append([]string(nil), s.config.FileView.PreviewExtensions...),
		"langList": []gin.H{
			{
				"langCode": "en",
				"langName": "en",
			},
		},
		"userRole":                role,
		"orgID":                   identity.OrgID,
		"canAddRepo":              false,
		"canShareRepo":            false,
		"canAddGroup":             false,
		"canGenerateShareLink":    false,
		"canGenerateUploadLink":   false,
		"canSendShareLinkEmail":   false,
		"canSendShareLinkMail":    false,
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

	if s.db == nil || !authenticated {
		return pageOptions
	}

	orgStorageUsed := traffic.ReadStorageUsed(s.db, fmt.Sprintf("org:%s", identity.OrgID))
	now := time.Now().UTC()
	periodStartedAt := traffic.EffectivePeriodStart(orgData.CurrentPeriodStartedAt, now)
	orgTrafficUsage := traffic.ReadOrgPeriodUsage(s.db, identity.OrgID, periodStartedAt)

	profile := s.config.GetEnforcementProfile(orgData.QuotaPolicy)
	resolved := plans.ResolveCapabilities(role, profile)

	var storagePct float64
	storageOverQuota := false
	if orgData.StorageQuota > 0 {
		storagePct = float64(orgStorageUsed) / float64(orgData.StorageQuota) * 100
		storageOverQuota = orgStorageUsed > orgData.StorageQuota
	}

	var trafficPct float64
	trafficOverQuota := false
	if orgData.TrafficQuota > 0 {
		trafficPct = float64(orgTrafficUsage.Combined) / float64(orgData.TrafficQuota) * 100
		trafficOverQuota = orgTrafficUsage.Combined > orgData.TrafficQuota
	}
	uploadOverQuota := orgData.TrafficUploadQuota > 0 && orgTrafficUsage.Upload > orgData.TrafficUploadQuota
	downloadOverQuota := orgData.TrafficDownloadQuota > 0 && orgTrafficUsage.Download > orgData.TrafficDownloadQuota

	isOrgOwner := role == "owner"
	canUpgrade := plans.ComputeCanUpgrade(role, orgData.QuotaPolicy, storagePct, trafficPct, storageOverQuota, trafficOverQuota)
	isStaff := middleware.IsPlatformSuperAdmin(identity.OrgID, middleware.OrganizationRole(role))
	canViewOrg := middleware.IsOrgStaff(role)
	canSendShareLinkEmail := resolved.Capabilities["can_send_share_link_mail"]

	pageOptions["name"] = name
	pageOptions["username"] = email
	pageOptions["contactEmail"] = email
	pageOptions["loginID"] = email
	pageOptions["avatarURL"] = "/static/img/default-avatar.png"
	pageOptions["enableUpdateUserInfo"] = true
	pageOptions["enableUserSetContactEmail"] = false
	pageOptions["enableUserSetName"] = true
	pageOptions["canUpdatePassword"] = hasPasswordChange
	pageOptions["passwordOperationText"] = "Change"
	pageOptions["enableAPIKeys"] = true
	pageOptions["enableDeleteAccount"] = hasDeleteAccount
	pageOptions["userRole"] = role
	pageOptions["canAddRepo"] = resolved.Capabilities["can_add_repo"]
	pageOptions["canShareRepo"] = resolved.Capabilities["can_share_repo"]
	pageOptions["canAddGroup"] = resolved.Capabilities["can_add_group"]
	pageOptions["canGenerateShareLink"] = resolved.Capabilities["can_generate_share_link"]
	pageOptions["canGenerateUploadLink"] = resolved.Capabilities["can_generate_upload_link"]
	pageOptions["canSendShareLinkEmail"] = canSendShareLinkEmail
	pageOptions["canSendShareLinkMail"] = canSendShareLinkEmail
	pageOptions["canPublishRepo"] = resolved.Capabilities["can_publish_repo"]
	pageOptions["canViewOrg"] = canViewOrg
	pageOptions["isSystemStaff"] = isStaff
	pageOptions["plan"] = orgData.Plan
	pageOptions["isOrgOwner"] = isOrgOwner
	pageOptions["canUpgrade"] = canUpgrade
	pageOptions["billingCycle"] = orgData.BillingCycle
	pageOptions["maxUsers"] = orgData.MaxUsers
	pageOptions["currentUsers"] = orgData.CurrentUsers
	pageOptions["currentPeriodStartedAt"] = orgData.CurrentPeriodStartedAt
	pageOptions["currentPeriodEndsAt"] = orgData.CurrentPeriodEndsAt
	pageOptions["upgradeFeatures"] = resolved.UpgradeFeatures
	pageOptions["shareLinkExpireDaysMax"] = resolved.Limits.ShareLinkExpireDaysMax
	pageOptions["uploadLinkExpireDaysMax"] = resolved.Limits.UploadLinkExpireDaysMax
	pageOptions["storageInfo"] = gin.H{
		"used":       orgStorageUsed,
		"quota":      orgData.StorageQuota,
		"percent":    storagePct,
		"over_quota": storageOverQuota,
	}
	pageOptions["trafficInfo"] = gin.H{
		"used":                orgTrafficUsage.Combined,
		"quota":               orgData.TrafficQuota,
		"percent":             trafficPct,
		"over_quota":          trafficOverQuota,
		"upload_used":         orgTrafficUsage.Upload,
		"upload_quota":        orgData.TrafficUploadQuota,
		"upload_over_quota":   uploadOverQuota,
		"download_used":       orgTrafficUsage.Download,
		"download_quota":      orgData.TrafficDownloadQuota,
		"download_over_quota": downloadOverQuota,
		"reset_date":          traffic.EffectiveTrafficResetDate(orgData.CurrentPeriodStartedAt, orgData.CurrentPeriodEndsAt, now),
	}

	return pageOptions
}

func (s *Server) buildOrgBootstrapPageOptions(orgID string, orgData bootstrapOrgData) gin.H {
	pageOptions := gin.H{
		"orgID":                    orgID,
		"orgName":                  orgData.Name,
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

	if orgID == "" || !orgData.Loaded {
		return pageOptions
	}

	effectiveMaxUsers := orgData.MaxUsers
	if orgData.Settings != nil {
		if rawMaxUsers, ok := orgData.Settings["max_user_number"]; ok {
			if parsedMaxUsers, err := strconv.Atoi(rawMaxUsers); err == nil && parsedMaxUsers > 0 {
				effectiveMaxUsers = parsedMaxUsers
			}
		}
	}

	hasUserAvailability := effectiveMaxUsers <= 0 || orgData.CurrentUsers < effectiveMaxUsers

	pageOptions["orgName"] = orgData.Name
	pageOptions["orgMemberQuotaEnabled"] = boolString(effectiveMaxUsers > 0)
	pageOptions["orgMembers"] = orgData.CurrentUsers
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
