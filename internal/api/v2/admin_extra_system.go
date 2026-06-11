package v2

import (
	"net/http"
	"strings"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/traffic"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
)

func (h *AdminHandler) AdminGetSysInfo(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	var usersCount int
	var activeUsersCount int
	iter := h.db.Session().Query(`SELECT user_id, status FROM users`).Iter()
	var userID gocql.UUID
	var status string
	for iter.Scan(&userID, &status) {
		usersCount++
		if IsUserUsable(status) {
			activeUsersCount++
		}
	}
	_ = iter.Close()

	var reposCount int
	var dummy gocql.UUID
	iter = h.db.Session().Query(`SELECT library_id FROM libraries`).Iter()
	for iter.Scan(&dummy) {
		reposCount++
	}
	_ = iter.Close()

	var groupsCount int
	iter = h.db.Session().Query(`SELECT group_id FROM groups`).Iter()
	for iter.Scan(&dummy) {
		groupsCount++
	}
	_ = iter.Close()

	var orgCount int
	iter = h.db.Session().Query(`SELECT org_id FROM organizations`).Iter()
	for iter.Scan(&dummy) {
		orgCount++
	}
	_ = iter.Close()

	platformStorage := traffic.ReadStorageSnapshot(h.db, traffic.PlatformStorageScope())
	now := time.Now().UTC()
	monthUsage := readPlatformTrafficUsage(h.db.Session(), []string{traffic.CurrentMonth()})
	yearUsage := readPlatformTrafficUsage(h.db.Session(), yearToDateMonthKeys(now))

	c.JSON(http.StatusOK, gin.H{
		"users_count":                     usersCount,
		"active_users_count":              activeUsersCount,
		"repos_count":                     reposCount,
		"total_files_count":               platformStorage.FileCount,
		"groups_count":                    groupsCount,
		"org_count":                       orgCount,
		"multi_tenancy_enabled":           true,
		"is_pro":                          true,
		"with_license":                    true,
		"license_expiration":              "2030-12-31",
		"license_mode":                    "subscription",
		"license_maxusers":                1000,
		"license_to":                      "SesameFS",
		"total_storage":                   platformStorage.BytesUsed,
		"traffic_month_total":             monthUsage.Combined,
		"traffic_month_upload":            monthUsage.Upload,
		"traffic_month_download":          monthUsage.Download,
		"traffic_year_total":              yearUsage.Combined,
		"traffic_year_upload":             yearUsage.Upload,
		"traffic_year_download":           yearUsage.Download,
		"total_devices_count":             nil,
		"current_connected_devices_count": nil,
	})
}

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
	c.JSON(http.StatusOK, gin.H{"success": true})
}

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
