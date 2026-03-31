package v2

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	"github.com/gin-gonic/gin"
)

// Links (public share links)
// ============================================================================

// ListOrgLinks lists all public share links in the org.
// GET /org/admin/links/?page=N
// Frontend reads: res.data.link_list, res.data.page, res.data.page_next
func (h *OrgAdminHandler) ListOrgLinks(c *gin.Context) {
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	role, err := h.permMiddleware.GetUserOrgRole(orgID, userID)
	if err != nil || !middleware.HasRequiredOrgRole(role, middleware.RoleAdmin) {
		c.JSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	perPage, _ := strconv.Atoi(c.DefaultQuery("per_page", "25"))
	if perPage < 1 || perPage > 100 {
		perPage = 25
	}

	expiredParam := strings.TrimSpace(strings.ToLower(c.DefaultQuery("expired", "")))
	hasExpiredFilter := false
	expiredFilter := false
	if expiredParam != "" && expiredParam != "all" {
		parsed, err := strconv.ParseBool(expiredParam)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid expired filter"})
			return
		}
		hasExpiredFilter = true
		expiredFilter = parsed
	}

	// Single-partition query on share_links_by_org — no more iterating users
	userCache := make(map[string][2]string) // createdBy -> [email, name]
	libNameCache := make(map[string]string)
	var links []gin.H
	iter := h.db.Session().Query(`
		SELECT link_token, link_type, library_id, file_path, created_by, permission, expires_at, has_password, active, view_count, created_at
		FROM share_links_by_org WHERE org_id = ?
	`, orgID).Iter()

	var token, linkType, libID, filePath, createdBy, permission string
	var expiresAt *time.Time
	var hasPassword, active bool
	var viewCount *int
	var createdAt time.Time

	for iter.Scan(&token, &linkType, &libID, &filePath, &createdBy, &permission, &expiresAt, &hasPassword, &active, &viewCount, &createdAt) {
		if linkType != "share" {
			continue
		}

		// Resolve user
		info, ok := userCache[createdBy]
		if !ok {
			var email, name string
			h.db.Session().Query(`SELECT email, name FROM users WHERE org_id = ? AND user_id = ?`, orgID, createdBy).Scan(&email, &name)
			if name == "" && email != "" {
				name = strings.Split(email, "@")[0]
			}
			info = [2]string{email, name}
			userCache[createdBy] = info
		}

		// Derive name from file_path
		linkName := filePath
		if idx := strings.LastIndex(filePath, "/"); idx >= 0 && idx < len(filePath)-1 {
			linkName = filePath[idx+1:]
		}
		if linkName == "" || linkName == "/" {
			libName, ok := libNameCache[libID]
			if !ok {
				h.db.Session().Query(`SELECT name FROM libraries WHERE org_id = ? AND library_id = ?`, orgID, libID).Scan(&libName)
				if libName == "" {
					libName = "Unknown Library"
				}
				libNameCache[libID] = libName
			}
			if libName != "" {
				linkName = libName
			}
		}

		repoName, ok := libNameCache[libID]
		if !ok {
			h.db.Session().Query(`SELECT name FROM libraries WHERE org_id = ? AND library_id = ?`, orgID, libID).Scan(&repoName)
			if repoName == "" {
				repoName = "Unknown Library"
			}
			libNameCache[libID] = repoName
		}

		isExpired := false
		expireDateStr := ""
		if expiresAt != nil && !expiresAt.IsZero() {
			isExpired = expiresAt.Before(time.Now())
			expireDateStr = expiresAt.Format(time.RFC3339)
		}
		if hasExpiredFilter && isExpired != expiredFilter {
			continue
		}

		linkURL := fmt.Sprintf("%s/d/%s", getBrowserURL(c, ""), token)
		count := 0
		if viewCount != nil {
			count = *viewCount
		}

		perms := parsePermsJSON(permission)
		status := "active"
		if !active {
			status = "inactive"
		}

		links = append(links, gin.H{
			"obj_name":      linkName,
			"name":          linkName,
			"path":          filePath,
			"token":         token,
			"link":          linkURL,
			"repo_id":       libID,
			"repo_name":     repoName,
			"owner_email":   info[0],
			"owner_name":    info[1],
			"creator_email": info[0],
			"creator_name":  info[1],
			"created_time":  createdAt.Format(time.RFC3339),
			"ctime":         createdAt.Format(time.RFC3339),
			"view_count":    count,
			"view_cnt":      count,
			"expire_date":   expireDateStr,
			"is_expired":    isExpired,
			"active":        active,
			"status":        status,
			"has_password":  hasPassword,
			"permissions":   gin.H{"can_download": perms.CanDownload, "can_edit": perms.CanEdit},
		})
	}
	if err := iter.Close(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list links"})
		return
	}

	if links == nil {
		links = []gin.H{}
	}

	sortBy := c.DefaultQuery("order_by", "")
	direction := c.DefaultQuery("direction", "asc")
	sortAdminLinks(links, sortBy, direction)

	// Paginate
	total := len(links)
	start := (page - 1) * perPage
	if start > total {
		start = total
	}
	end := start + perPage
	if end > total {
		end = total
	}

	c.JSON(http.StatusOK, gin.H{
		"link_list": links[start:end],
		"page":      page,
		"page_next": end < total,
		"count":     total,
	})
}

// DeleteOrgLink deletes a public share link.
// DELETE /org/admin/links/:token/
func (h *OrgAdminHandler) DeleteOrgLink(c *gin.Context) {
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	role, err := h.permMiddleware.GetUserOrgRole(orgID, userID)
	if err != nil || !middleware.HasRequiredOrgRole(role, middleware.RoleAdmin) {
		c.JSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
		return
	}

	token := c.Param("token")

	// Look up the link to verify it belongs to this org
	var linkOrgID, createdBy, libID string
	var createdAt time.Time
	if err := h.db.Session().Query(`
		SELECT org_id, created_by, library_id, created_at FROM share_links WHERE link_token = ?
	`, token).Scan(&linkOrgID, &createdBy, &libID, &createdAt); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "link not found"})
		return
	}
	if linkOrgID != orgID {
		c.JSON(http.StatusForbidden, gin.H{"error": "link does not belong to this organization"})
		return
	}

	sh := &ShareLinkHandler{db: h.db}
	if err := sh.deleteShareLink(token, orgID, createdBy, libID, createdAt); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete link"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ============================================================================
// Upload links
// ============================================================================

// ListOrgUploadLinks lists all upload links in the org.
// GET /org/admin/upload-links/?page=N
// Frontend reads: res.data.upload_link_list, res.data.count
func (h *OrgAdminHandler) ListOrgUploadLinks(c *gin.Context) {
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	role, err := h.permMiddleware.GetUserOrgRole(orgID, userID)
	if err != nil || !middleware.HasRequiredOrgRole(role, middleware.RoleAdmin) {
		c.JSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	if page < 1 {
		page = 1
	}
	perPage, _ := strconv.Atoi(c.DefaultQuery("per_page", "25"))
	if perPage < 1 || perPage > 100 {
		perPage = 25
	}

	expiredParam := strings.TrimSpace(strings.ToLower(c.DefaultQuery("expired", "")))
	hasExpiredFilter := false
	expiredFilter := false
	if expiredParam != "" && expiredParam != "all" {
		parsed, err := strconv.ParseBool(expiredParam)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid expired filter"})
			return
		}
		hasExpiredFilter = true
		expiredFilter = parsed
	}

	// Single-partition query on share_links_by_org
	userCache := make(map[string][2]string)
	libNameCache := make(map[string]string)
	var links []gin.H
	iter := h.db.Session().Query(`
		SELECT link_token, link_type, library_id, file_path, created_by, expires_at, active, has_password, upload_count, created_at
		FROM share_links_by_org WHERE org_id = ?
	`, orgID).Iter()

	var token, linkType, libID, filePath, createdBy string
	var expiresAt *time.Time
	var active, hasPassword bool
	var uploadCount *int
	var createdAt time.Time

	for iter.Scan(&token, &linkType, &libID, &filePath, &createdBy, &expiresAt, &active, &hasPassword, &uploadCount, &createdAt) {
		if linkType != "upload" {
			continue
		}

		// Resolve user
		info, ok := userCache[createdBy]
		if !ok {
			var email, name string
			h.db.Session().Query(`SELECT email, name FROM users WHERE org_id = ? AND user_id = ?`, orgID, createdBy).Scan(&email, &name)
			if name == "" && email != "" {
				name = strings.Split(email, "@")[0]
			}
			info = [2]string{email, name}
			userCache[createdBy] = info
		}

		objName := filePath
		if idx := strings.LastIndex(filePath, "/"); idx >= 0 && idx < len(filePath)-1 {
			objName = filePath[idx+1:]
		}

		isExpired := false
		expireDateStr := ""
		if expiresAt != nil && !expiresAt.IsZero() {
			isExpired = expiresAt.Before(time.Now())
			expireDateStr = expiresAt.Format(time.RFC3339)
		}
		if hasExpiredFilter && isExpired != expiredFilter {
			continue
		}

		repoName, ok := libNameCache[libID]
		if !ok {
			h.db.Session().Query(`SELECT name FROM libraries WHERE org_id = ? AND library_id = ?`, orgID, libID).Scan(&repoName)
			if repoName == "" {
				repoName = "Unknown Library"
			}
			libNameCache[libID] = repoName
		}

		uploadLinkURL := fmt.Sprintf("%s/u/d/%s", getBrowserURL(c, ""), token)
		count := 0
		if uploadCount != nil {
			count = *uploadCount
		}

		status := "active"
		if !active {
			status = "inactive"
		}

		links = append(links, gin.H{
			"obj_name":      objName,
			"path":          filePath,
			"token":         token,
			"link":          uploadLinkURL,
			"repo_id":       libID,
			"repo_name":     repoName,
			"creator_email": info[0],
			"creator_name":  info[1],
			"ctime":         createdAt.Format(time.RFC3339),
			"view_cnt":      count,
			"expire_date":   expireDateStr,
			"is_expired":    isExpired,
			"active":        active,
			"status":        status,
			"has_password":  hasPassword,
		})
	}
	if err := iter.Close(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list upload links"})
		return
	}

	if links == nil {
		links = []gin.H{}
	}

	sortBy := c.DefaultQuery("order_by", "")
	direction := c.DefaultQuery("direction", "asc")
	sortAdminLinks(links, sortBy, direction)

	total := len(links)
	start := (page - 1) * perPage
	if start > total {
		start = total
	}
	end := start + perPage
	if end > total {
		end = total
	}

	c.JSON(http.StatusOK, gin.H{
		"upload_link_list": links[start:end],
		"count":            total,
	})
}

// DeleteOrgUploadLink deletes an upload link.
// DELETE /org/admin/upload-links/:token/
func (h *OrgAdminHandler) DeleteOrgUploadLink(c *gin.Context) {
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	role, err := h.permMiddleware.GetUserOrgRole(orgID, userID)
	if err != nil || !middleware.HasRequiredOrgRole(role, middleware.RoleAdmin) {
		c.JSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
		return
	}

	token := c.Param("token")

	// Look up the link to verify it belongs to this org
	var linkOrgID, createdBy, libID string
	var createdAt time.Time
	if err := h.db.Session().Query(`
		SELECT org_id, created_by, library_id, created_at FROM share_links WHERE link_token = ?
	`, token).Scan(&linkOrgID, &createdBy, &libID, &createdAt); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "upload link not found"})
		return
	}
	if linkOrgID != orgID {
		c.JSON(http.StatusForbidden, gin.H{"error": "upload link does not belong to this organization"})
		return
	}

	sh := &ShareLinkHandler{db: h.db}
	if err := sh.deleteShareLink(token, orgID, createdBy, libID, createdAt); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete upload link"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

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

	// Backend logo asset persistence is not implemented yet. Keep the route
	// functional and return a stable path so org-admin UI no longer depends on
	// the legacy /info endpoint.
	logoPath := h.getOrgSetting(targetOrgID, "logo_path", "/media/custom/logo.png")
	if err := h.updateOrgSetting(targetOrgID, "logo_path", logoPath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update organization logo"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"logo_path": logoPath})
}
func (h *OrgAdminHandler) GetOrgSAMLConfig(c *gin.Context) {
	h.notImplemented(c, "get org SAML config")
}
func (h *OrgAdminHandler) UpdateOrgSAMLConfig(c *gin.Context) {
	h.notImplemented(c, "update org SAML config")
}
func (h *OrgAdminHandler) VerifyOrgDomain(c *gin.Context) {
	h.notImplemented(c, "verify org domain")
}
