package v2

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// ============================================================================
// Logs
// ============================================================================

func (h *OrgAdminHandler) ListOrgFileAccessLogs(c *gin.Context) {
	h.notImplemented(c, "list org file access logs")
}
func (h *OrgAdminHandler) ListOrgFileUpdateLogs(c *gin.Context) {
	h.notImplemented(c, "list org file update logs")
}
func (h *OrgAdminHandler) ListOrgRepoPermLogs(c *gin.Context) {
	h.notImplemented(c, "list org repo permission logs")
}

// ============================================================================
// Web settings, logo, SAML, domain
// ============================================================================

func (h *OrgAdminHandler) GetOrgWebSettings(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}

	var orgName string
	if err := h.db.Session().Query(`SELECT name FROM organizations WHERE org_id = ?`, targetOrgID).Scan(&orgName); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "organization not found"})
		return
	}

	logoPath := h.getOrgSetting(targetOrgID, "logo_path", "")
	c.JSON(http.StatusOK, gin.H{
		"org_name":            orgName,
		"file_ext_white_list": h.getOrgSetting(targetOrgID, "file_ext_white_list", ""),
		"logo_path":           logoPath,
	})
}

func (h *OrgAdminHandler) SetOrgWebSettings(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}

	var body struct {
		FileExtWhiteList string `json:"file_ext_white_list"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	fileExtWhiteList := body.FileExtWhiteList
	if fileExtWhiteList == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file_ext_white_list is required"})
		return
	}

	if err := h.updateOrgSetting(targetOrgID, "file_ext_white_list", fileExtWhiteList); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update organization web settings"})
		return
	}

	var orgName string
	_ = h.db.Session().Query(`SELECT name FROM organizations WHERE org_id = ?`, targetOrgID).Scan(&orgName)
	c.JSON(http.StatusOK, gin.H{
		"org_name":            orgName,
		"file_ext_white_list": fileExtWhiteList,
		"logo_path":           h.getOrgSetting(targetOrgID, "logo_path", ""),
	})
}

func (h *OrgAdminHandler) UpdateOrgLogo(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}

	fileHeader, err := c.FormFile("logo")
	if err != nil || fileHeader == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "logo file is required"})
		return
	}

	logoPath := h.getOrgSetting(targetOrgID, "logo_path", "/media/custom/logo.png")
	if err := h.updateOrgSetting(targetOrgID, "logo_path", logoPath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update organization logo"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"logo_path": logoPath})
}
func (h *OrgAdminHandler) GetOrgSAMLConfig(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}
	h.notImplemented(c, "get org SAML config")
}
func (h *OrgAdminHandler) UpdateOrgSAMLConfig(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}
	h.notImplemented(c, "update org SAML config")
}
func (h *OrgAdminHandler) VerifyOrgDomain(c *gin.Context) {
	targetOrgID := c.Param("org_id")
	if err := h.requireOrgAccess(c, targetOrgID); err != nil {
		return
	}
	h.notImplemented(c, "verify org domain")
}
