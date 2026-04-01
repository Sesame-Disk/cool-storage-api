package v2

import (
	"errors"
	"fmt"
	"net/http"
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

	filters, err := parseAdminLinkListFiltersFromContext(c, false)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	page, perPage := parseAdminLinkPageParams(c.DefaultQuery("page", "1"), c.DefaultQuery("per_page", "25"), 25, 100)

	userCache := make(map[string][2]string)
	libNameCache := make(map[string]string)
	var links []gin.H
	rows, err := listAdminLinkProjectionRowsByOrg(h.db.Session(), orgID, "share")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list links"})
		return
	}

	for _, row := range rows {
		repoName := resolveAdminLinkRepoName(h.db.Session(), libNameCache, orgID, row.LibraryID, row.RepoName)
		creatorEmail, creatorName := resolveAdminLinkCreatorInfo(h.db.Session(), userCache, orgID, row.CreatedBy, row.CreatorEmail, row.CreatorName)
		linkName := resolveAdminLinkObjName(row.ObjName, row.FilePath, repoName)

		isExpired := false
		expireDateStr := ""
		if row.ExpiresAt != nil && !row.ExpiresAt.IsZero() {
			isExpired = row.ExpiresAt.Before(time.Now())
			expireDateStr = row.ExpiresAt.Format(time.RFC3339)
		}
		if !filters.MatchesState(row.Active, isExpired) {
			continue
		}

		linkURL := fmt.Sprintf("%s/d/%s", getBrowserURL(c, ""), row.Token)
		perms := parsePermsJSON(row.Permission)
		status := "active"
		if !row.Active {
			status = "inactive"
		}
		if !filters.MatchesSearch(linkName, row.Token, row.FilePath, repoName, creatorEmail, creatorName, linkURL) {
			continue
		}

		links = append(links, gin.H{
			"obj_name":      linkName,
			"name":          linkName,
			"path":          row.FilePath,
			"token":         row.Token,
			"link":          linkURL,
			"repo_id":       row.LibraryID,
			"repo_name":     repoName,
			"owner_email":   creatorEmail,
			"owner_name":    creatorName,
			"creator_email": creatorEmail,
			"creator_name":  creatorName,
			"created_time":  row.CreatedAt.Format(time.RFC3339),
			"ctime":         row.CreatedAt.Format(time.RFC3339),
			"view_count":    row.ViewCount,
			"view_cnt":      row.ViewCount,
			"expire_date":   expireDateStr,
			"is_expired":    isExpired,
			"active":        row.Active,
			"status":        status,
			"has_password":  row.HasPassword,
			"permissions":   gin.H{"can_download": perms.CanDownload, "can_edit": perms.CanEdit},
		})
	}

	if links == nil {
		links = []gin.H{}
	}

	sortBy := c.Query("order_by")
	direction := c.Query("direction")
	sortAdminLinks(links, sortBy, direction)

	pagedLinks, total, pageNext := paginateAdminLinks(links, page, perPage)

	c.JSON(http.StatusOK, gin.H{
		"link_list": pagedLinks,
		"page":      page,
		"page_next": pageNext,
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

	var linkOrgID, createdBy, libID, linkType string
	var createdAt time.Time
	if err := h.db.Session().Query(`
		SELECT org_id, created_by, library_id, created_at, link_type FROM share_links WHERE link_token = ?
	`, token).Scan(&linkOrgID, &createdBy, &libID, &createdAt, &linkType); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "link not found"})
		return
	}
	if err := validateAdminLinkScope(linkOrgID, orgID, linkType, "share"); errors.Is(err, errAdminLinkWrongOrg) {
		c.JSON(http.StatusForbidden, gin.H{"error": "link does not belong to this organization"})
		return
	} else if errors.Is(err, errAdminLinkWrongType) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "link is not a share link"})
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

	filters, err := parseAdminLinkListFiltersFromContext(c, false)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	page, perPage := parseAdminLinkPageParams(c.DefaultQuery("page", "1"), c.DefaultQuery("per_page", "25"), 25, 100)

	userCache := make(map[string][2]string)
	libNameCache := make(map[string]string)
	var links []gin.H
	rows, err := listAdminLinkProjectionRowsByOrg(h.db.Session(), orgID, "upload")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list upload links"})
		return
	}

	for _, row := range rows {
		repoName := resolveAdminLinkRepoName(h.db.Session(), libNameCache, orgID, row.LibraryID, row.RepoName)
		creatorEmail, creatorName := resolveAdminLinkCreatorInfo(h.db.Session(), userCache, orgID, row.CreatedBy, row.CreatorEmail, row.CreatorName)
		objName := resolveAdminLinkObjName(row.ObjName, row.FilePath, repoName)

		isExpired := false
		expireDateStr := ""
		if row.ExpiresAt != nil && !row.ExpiresAt.IsZero() {
			isExpired = row.ExpiresAt.Before(time.Now())
			expireDateStr = row.ExpiresAt.Format(time.RFC3339)
		}
		if !filters.MatchesState(row.Active, isExpired) {
			continue
		}

		uploadLinkURL := fmt.Sprintf("%s/u/d/%s", getBrowserURL(c, ""), row.Token)
		status := "active"
		if !row.Active {
			status = "inactive"
		}
		if !filters.MatchesSearch(objName, row.Token, row.FilePath, repoName, creatorEmail, creatorName, uploadLinkURL) {
			continue
		}

		links = append(links, gin.H{
			"obj_name":      objName,
			"path":          row.FilePath,
			"token":         row.Token,
			"link":          uploadLinkURL,
			"repo_id":       row.LibraryID,
			"repo_name":     repoName,
			"creator_email": creatorEmail,
			"creator_name":  creatorName,
			"ctime":         row.CreatedAt.Format(time.RFC3339),
			"view_cnt":      row.UploadCount,
			"expire_date":   expireDateStr,
			"is_expired":    isExpired,
			"active":        row.Active,
			"status":        status,
			"has_password":  row.HasPassword,
		})
	}

	if links == nil {
		links = []gin.H{}
	}

	sortBy := c.Query("order_by")
	direction := c.Query("direction")
	sortAdminLinks(links, sortBy, direction)

	pagedLinks, total, _ := paginateAdminLinks(links, page, perPage)

	c.JSON(http.StatusOK, gin.H{
		"upload_link_list": pagedLinks,
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

	var linkOrgID, createdBy, libID, linkType string
	var createdAt time.Time
	if err := h.db.Session().Query(`
		SELECT org_id, created_by, library_id, created_at, link_type FROM share_links WHERE link_token = ?
	`, token).Scan(&linkOrgID, &createdBy, &libID, &createdAt, &linkType); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "upload link not found"})
		return
	}
	if err := validateAdminLinkScope(linkOrgID, orgID, linkType, "upload"); errors.Is(err, errAdminLinkWrongOrg) {
		c.JSON(http.StatusForbidden, gin.H{"error": "upload link does not belong to this organization"})
		return
	} else if errors.Is(err, errAdminLinkWrongType) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "link is not an upload link"})
		return
	}

	sh := &ShareLinkHandler{db: h.db}
	if err := sh.deleteShareLink(token, orgID, createdBy, libID, createdAt); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete upload link"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}
