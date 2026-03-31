package v2

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

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
