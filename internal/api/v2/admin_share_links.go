package v2

import (
	"errors"
	"fmt"
	"net/http"
	"time"

	db "github.com/Sesame-Disk/sesamefs/internal/db"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
)

// ============================================================================
// Share Links — GET /admin/share-links/ , DELETE /admin/share-links/:token/
// ============================================================================

func (h *AdminHandler) AdminListShareLinks(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	filters, err := parseAdminLinkListFiltersFromContext(c, true)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	page, perPage := parseAdminLinkPageParams(c.DefaultQuery("page", "1"), c.DefaultQuery("per_page", "25"), 25, 0)
	sortBy := c.Query("order_by")
	direction := c.Query("direction")
	if isDefaultAdminLinkSort(sortBy, direction) {
		rows, total, _, err := listAdminLinkProjectionPage(h.db.Session(), "share", filters, page, perPage)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list share links"})
			return
		}

		links := make([]gin.H, 0, len(rows))
		for _, row := range rows {
			repoName, objName, creatorEmail, creatorName := adminLinkProjectionDisplay(row)
			isExpired := false
			expireDateStr := ""
			if row.ExpiresAt != nil && !row.ExpiresAt.IsZero() {
				isExpired = row.ExpiresAt.Before(time.Now())
				expireDateStr = row.ExpiresAt.Format("2006-01-02T15:04:05+00:00")
			}
			perms := parsePermsJSON(row.Permission)
			status := "active"
			if !row.Active {
				status = "inactive"
			}
			links = append(links, gin.H{
				"obj_name":      objName,
				"token":         row.Token,
				"repo_id":       row.LibraryID,
				"repo_name":     repoName,
				"path":          row.FilePath,
				"creator_email": creatorEmail,
				"creator_name":  creatorName,
				"ctime":         row.CreatedAt.Format(time.RFC3339),
				"view_cnt":      row.ViewCount,
				"expire_date":   expireDateStr,
				"is_expired":    isExpired,
				"active":        row.Active,
				"has_password":  row.HasPassword,
				"status":        status,
				"permissions":   gin.H{"can_download": perms.CanDownload, "can_edit": perms.CanEdit},
			})
		}

		c.JSON(http.StatusOK, gin.H{
			"share_link_list": links,
			"count":           total,
		})
		return
	}

	rows, err := listAdminLinkProjectionRows(h.db.Session(), "share")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list share links"})
		return
	}

	var links []gin.H

	for _, row := range rows {
		repoName, objName, creatorEmail, creatorName := adminLinkProjectionDisplay(row)

		isExpired := false
		expireDateStr := ""
		if row.ExpiresAt != nil && !row.ExpiresAt.IsZero() {
			isExpired = row.ExpiresAt.Before(time.Now())
			expireDateStr = row.ExpiresAt.Format("2006-01-02T15:04:05+00:00")
		}
		if !filters.MatchesState(row.Active, isExpired) {
			continue
		}
		if !filters.MatchesSearch(objName, row.Token, row.FilePath, repoName, creatorEmail, creatorName) {
			continue
		}

		status := "active"
		if !row.Active {
			status = "inactive"
		}

		perms := parsePermsJSON(row.Permission)
		links = append(links, gin.H{
			"obj_name":      objName,
			"token":         row.Token,
			"repo_id":       row.LibraryID,
			"repo_name":     repoName,
			"path":          row.FilePath,
			"creator_email": creatorEmail,
			"creator_name":  creatorName,
			"ctime":         row.CreatedAt.Format(time.RFC3339),
			"view_cnt":      row.ViewCount,
			"expire_date":   expireDateStr,
			"is_expired":    isExpired,
			"active":        row.Active,
			"has_password":  row.HasPassword,
			"status":        status,
			"permissions":   gin.H{"can_download": perms.CanDownload, "can_edit": perms.CanEdit},
		})
	}

	if links == nil {
		links = []gin.H{}
	}

	sortAdminLinks(links, sortBy, direction)
	pagedLinks, total, _ := paginateAdminLinks(links, page, perPage)

	c.JSON(http.StatusOK, gin.H{
		"share_link_list": pagedLinks,
		"count":           total,
	})
}

func (h *AdminHandler) AdminDeleteShareLink(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	token := c.Param("token")

	var createdBy, orgID, libID, linkType string
	var createdAt time.Time
	if err := h.db.Session().Query(`SELECT created_by, org_id, library_id, created_at, link_type FROM share_links WHERE link_token = ?`, token).Scan(&createdBy, &orgID, &libID, &createdAt, &linkType); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "share link not found"})
		return
	}
	if err := validateAdminLinkScope(orgID, "", linkType, "share"); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "link is not a share link"})
		return
	}

	sh := &ShareLinkHandler{db: h.db}
	if err := sh.deleteShareLink(token, orgID, createdBy, libID, createdAt); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete share link"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// AdminSetShareLinkActive toggles active flag for a share link (platform superadmin scope).
// PUT /admin/share-links/:token/active/
// Body/Form: active=true|false
func (h *AdminHandler) AdminSetShareLinkActive(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	token := c.Param("token")
	var shareLinkReq struct {
		Active *bool `json:"active"`
	}
	if err := c.ShouldBindJSON(&shareLinkReq); err != nil || shareLinkReq.Active == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "active is required and must be true or false"})
		return
	}
	active := *shareLinkReq.Active

	if err := h.setAdminLinkActive(token, "share", active); err != nil {
		if errors.Is(err, errAdminLinkNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "share link not found"})
			return
		}
		if errors.Is(err, errAdminLinkWrongType) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "link is not a share link"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update share link"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "active": active})
}

// ============================================================================
// Upload Links — GET /admin/upload-links/ , DELETE /admin/upload-links/:token/
// ============================================================================

func (h *AdminHandler) AdminListUploadLinks(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	filters, err := parseAdminLinkListFiltersFromContext(c, true)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	page, perPage := parseAdminLinkPageParams(c.DefaultQuery("page", "1"), c.DefaultQuery("per_page", "25"), 25, 0)
	sortBy := c.Query("order_by")
	direction := c.Query("direction")
	if isDefaultAdminLinkSort(sortBy, direction) {
		rows, total, _, err := listAdminLinkProjectionPage(h.db.Session(), "upload", filters, page, perPage)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list upload links"})
			return
		}

		links := make([]gin.H, 0, len(rows))
		for _, row := range rows {
			repoName, objName, creatorEmail, creatorName := adminLinkProjectionDisplay(row)
			isExpired := false
			expireDateStr := ""
			if row.ExpiresAt != nil && !row.ExpiresAt.IsZero() {
				isExpired = row.ExpiresAt.Before(time.Now())
				expireDateStr = row.ExpiresAt.Format("2006-01-02T15:04:05+00:00")
			}
			status := "active"
			if !row.Active {
				status = "inactive"
			}
			links = append(links, gin.H{
				"obj_name":      objName,
				"path":          row.FilePath,
				"token":         row.Token,
				"repo_id":       row.LibraryID,
				"repo_name":     repoName,
				"creator_email": creatorEmail,
				"creator_name":  creatorName,
				"ctime":         row.CreatedAt.Format(time.RFC3339),
				"view_cnt":      row.UploadCount,
				"expire_date":   expireDateStr,
				"is_expired":    isExpired,
				"active":        row.Active,
				"has_password":  row.HasPassword,
				"status":        status,
			})
		}

		c.JSON(http.StatusOK, gin.H{
			"upload_link_list": links,
			"count":            total,
		})
		return
	}

	rows, err := listAdminLinkProjectionRows(h.db.Session(), "upload")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list upload links"})
		return
	}

	var links []gin.H

	for _, row := range rows {
		repoName, objName, creatorEmail, creatorName := adminLinkProjectionDisplay(row)

		isExpired := false
		expireDateStr := ""
		if row.ExpiresAt != nil && !row.ExpiresAt.IsZero() {
			isExpired = row.ExpiresAt.Before(time.Now())
			expireDateStr = row.ExpiresAt.Format("2006-01-02T15:04:05+00:00")
		}
		if !filters.MatchesState(row.Active, isExpired) {
			continue
		}
		if !filters.MatchesSearch(objName, row.Token, row.FilePath, repoName, creatorEmail, creatorName) {
			continue
		}

		status := "active"
		if !row.Active {
			status = "inactive"
		}

		links = append(links, gin.H{
			"obj_name":      objName,
			"path":          row.FilePath,
			"token":         row.Token,
			"repo_id":       row.LibraryID,
			"repo_name":     repoName,
			"creator_email": creatorEmail,
			"creator_name":  creatorName,
			"ctime":         row.CreatedAt.Format(time.RFC3339),
			"view_cnt":      row.UploadCount,
			"expire_date":   expireDateStr,
			"is_expired":    isExpired,
			"active":        row.Active,
			"has_password":  row.HasPassword,
			"status":        status,
		})
	}

	if links == nil {
		links = []gin.H{}
	}

	sortAdminLinks(links, sortBy, direction)
	pagedLinks, total, _ := paginateAdminLinks(links, page, perPage)

	c.JSON(http.StatusOK, gin.H{
		"upload_link_list": pagedLinks,
		"count":            total,
	})
}

func (h *AdminHandler) AdminDeleteUploadLink(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	token := c.Param("token")

	var createdBy, orgID, libID, linkType string
	var createdAt time.Time
	if err := h.db.Session().Query(`SELECT created_by, org_id, library_id, created_at, link_type FROM share_links WHERE link_token = ?`, token).Scan(&createdBy, &orgID, &libID, &createdAt, &linkType); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "upload link not found"})
		return
	}
	if err := validateAdminLinkScope(orgID, "", linkType, "upload"); err != nil {
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

// AdminSetUploadLinkActive toggles active flag for an upload link (platform superadmin scope).
// PUT /admin/upload-links/:token/active/
// Body/Form: active=true|false
func (h *AdminHandler) AdminSetUploadLinkActive(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	token := c.Param("token")
	var uploadLinkReq struct {
		Active *bool `json:"active"`
	}
	if err := c.ShouldBindJSON(&uploadLinkReq); err != nil || uploadLinkReq.Active == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "active is required and must be true or false"})
		return
	}
	active := *uploadLinkReq.Active

	if err := h.setAdminLinkActive(token, "upload", active); err != nil {
		if errors.Is(err, errAdminLinkNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": "upload link not found"})
			return
		}
		if errors.Is(err, errAdminLinkWrongType) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "link is not an upload link"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update upload link"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "active": active})
}

func (h *AdminHandler) setAdminLinkActive(token, expectedType string, active bool) error {
	var createdBy, orgID, libID, linkType string
	var createdAt time.Time
	if err := h.db.Session().Query(`
		SELECT created_by, org_id, library_id, created_at, link_type
		FROM share_links WHERE link_token = ?
	`, token).Scan(&createdBy, &orgID, &libID, &createdAt, &linkType); err != nil {
		return errAdminLinkNotFound
	}
	if err := validateAdminLinkScope(orgID, "", linkType, expectedType); err != nil {
		return err
	}

	batch := h.db.Session().Batch(gocql.UnloggedBatch)
	batch.Query(`UPDATE share_links SET active = ? WHERE link_token = ?`, active, token)
	batch.Query(`UPDATE share_links_by_creator SET active = ? WHERE org_id = ? AND created_by = ? AND created_at = ? AND link_token = ?`,
		active, orgID, createdBy, createdAt, token)
	db.AddUpdateAdminLinkActiveQuery(batch, linkType, createdAt, orgID, token, active)

	if err := batch.Exec(); err != nil {
		return err
	}
	return nil
}

// ============================================================================
// User share/upload links — GET /admin/users/:email/share-links/
// ============================================================================

func (h *AdminHandler) AdminListUserShareLinks(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	email := c.GetString("resolved_user_param")
	if email == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "email is required"})
		return
	}

	var targetUserID string
	var targetOrgID string
	if err := h.db.Session().Query(`SELECT user_id, org_id FROM users_by_email WHERE email = ?`, email).Scan(&targetUserID, &targetOrgID); err != nil {
		c.JSON(http.StatusOK, gin.H{"share_link_list": []gin.H{}, "count": 0})
		return
	}
	filters, err := parseAdminLinkListFiltersFromContext(c, true)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	page, perPage := parseAdminLinkPageParams(c.DefaultQuery("page", "1"), c.DefaultQuery("per_page", "25"), 25, 0)
	sortBy := c.Query("order_by")
	direction := c.Query("direction")

	var links []gin.H
	if isDefaultAdminLinkSort(sortBy, direction) {
		rows, total, _, err := listAdminLinkProjectionPageByCreator(h.db.Session(), targetOrgID, targetUserID, "share", filters, page, perPage)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list user share links"})
			return
		}

		links = make([]gin.H, 0, len(rows))
		for _, row := range rows {
			repoName, objName, creatorEmail, creatorName := adminLinkProjectionDisplay(row)
			isExpired := false
			expireDateStr := ""
			if row.ExpiresAt != nil && !row.ExpiresAt.IsZero() {
				isExpired = row.ExpiresAt.Before(time.Now())
				expireDateStr = row.ExpiresAt.Format("2006-01-02T15:04:05+00:00")
			}
			status := "active"
			if !row.Active {
				status = "inactive"
			}
			linkURL := fmt.Sprintf("%s/d/%s", getBrowserURL(c, ""), row.Token)
			links = append(links, gin.H{
				"obj_name":      objName,
				"token":         row.Token,
				"link":          linkURL,
				"repo_id":       row.LibraryID,
				"repo_name":     repoName,
				"path":          row.FilePath,
				"creator_email": creatorEmail,
				"creator_name":  creatorName,
				"ctime":         row.CreatedAt.Format(time.RFC3339),
				"view_cnt":      row.ViewCount,
				"expire_date":   expireDateStr,
				"is_expired":    isExpired,
				"active":        row.Active,
				"has_password":  row.HasPassword,
				"status":        status,
			})
		}

		c.JSON(http.StatusOK, gin.H{
			"share_link_list": links,
			"count":           total,
		})
		return
	}

	rows, err := listAdminLinkProjectionRowsByCreator(h.db.Session(), targetOrgID, targetUserID, "share")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list user share links"})
		return
	}

	for _, row := range rows {
		repoName, objName, creatorEmail, creatorName := adminLinkProjectionDisplay(row)

		isExpired := false
		expireDateStr := ""
		if row.ExpiresAt != nil && !row.ExpiresAt.IsZero() {
			isExpired = row.ExpiresAt.Before(time.Now())
			expireDateStr = row.ExpiresAt.Format("2006-01-02T15:04:05+00:00")
		}
		if !filters.MatchesState(row.Active, isExpired) {
			continue
		}

		status := "active"
		if !row.Active {
			status = "inactive"
		}

		linkURL := fmt.Sprintf("%s/d/%s", getBrowserURL(c, ""), row.Token)
		if !filters.MatchesSearch(objName, row.Token, row.FilePath, repoName, creatorEmail, creatorName, linkURL) {
			continue
		}

		links = append(links, gin.H{
			"obj_name":      objName,
			"token":         row.Token,
			"link":          linkURL,
			"repo_id":       row.LibraryID,
			"repo_name":     repoName,
			"path":          row.FilePath,
			"creator_email": creatorEmail,
			"creator_name":  creatorName,
			"ctime":         row.CreatedAt.Format(time.RFC3339),
			"view_cnt":      row.ViewCount,
			"expire_date":   expireDateStr,
			"is_expired":    isExpired,
			"active":        row.Active,
			"has_password":  row.HasPassword,
			"status":        status,
		})
	}

	if links == nil {
		links = []gin.H{}
	}

	sortAdminLinks(links, sortBy, direction)
	pagedLinks, total, _ := paginateAdminLinks(links, page, perPage)

	c.JSON(http.StatusOK, gin.H{
		"share_link_list": pagedLinks,
		"count":           total,
	})
}

func (h *AdminHandler) AdminListUserUploadLinks(c *gin.Context) {
	callerOrgID := c.GetString("org_id")
	callerUserID := c.GetString("user_id")
	if err := h.requireAdminAccess(c, callerOrgID, callerUserID); err != nil {
		return
	}

	email := c.GetString("resolved_user_param")
	if email == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "email is required"})
		return
	}

	var targetUserID string
	var targetOrgID string
	if err := h.db.Session().Query(`SELECT user_id, org_id FROM users_by_email WHERE email = ?`, email).Scan(&targetUserID, &targetOrgID); err != nil {
		c.JSON(http.StatusOK, gin.H{"upload_link_list": []gin.H{}, "count": 0})
		return
	}
	filters, err := parseAdminLinkListFiltersFromContext(c, true)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	page, perPage := parseAdminLinkPageParams(c.DefaultQuery("page", "1"), c.DefaultQuery("per_page", "25"), 25, 0)
	sortBy := c.Query("order_by")
	direction := c.Query("direction")

	var links []gin.H
	if isDefaultAdminLinkSort(sortBy, direction) {
		rows, total, _, err := listAdminLinkProjectionPageByCreator(h.db.Session(), targetOrgID, targetUserID, "upload", filters, page, perPage)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list user upload links"})
			return
		}

		links = make([]gin.H, 0, len(rows))
		for _, row := range rows {
			repoName, objName, creatorEmail, creatorName := adminLinkProjectionDisplay(row)
			isExpired := false
			expireDateStr := ""
			if row.ExpiresAt != nil && !row.ExpiresAt.IsZero() {
				isExpired = row.ExpiresAt.Before(time.Now())
				expireDateStr = row.ExpiresAt.Format("2006-01-02T15:04:05+00:00")
			}
			status := "active"
			if !row.Active {
				status = "inactive"
			}
			linkURL := fmt.Sprintf("%s/u/d/%s", getBrowserURL(c, ""), row.Token)
			links = append(links, gin.H{
				"obj_name":      objName,
				"path":          row.FilePath,
				"token":         row.Token,
				"link":          linkURL,
				"repo_id":       row.LibraryID,
				"repo_name":     repoName,
				"creator_email": creatorEmail,
				"creator_name":  creatorName,
				"ctime":         row.CreatedAt.Format(time.RFC3339),
				"view_cnt":      row.UploadCount,
				"expire_date":   expireDateStr,
				"is_expired":    isExpired,
				"active":        row.Active,
				"has_password":  row.HasPassword,
				"status":        status,
			})
		}

		c.JSON(http.StatusOK, gin.H{
			"upload_link_list": links,
			"count":            total,
		})
		return
	}

	rows, err := listAdminLinkProjectionRowsByCreator(h.db.Session(), targetOrgID, targetUserID, "upload")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list user upload links"})
		return
	}

	for _, row := range rows {
		repoName, objName, creatorEmail, creatorName := adminLinkProjectionDisplay(row)

		isExpired := false
		expireDateStr := ""
		if row.ExpiresAt != nil && !row.ExpiresAt.IsZero() {
			isExpired = row.ExpiresAt.Before(time.Now())
			expireDateStr = row.ExpiresAt.Format("2006-01-02T15:04:05+00:00")
		}
		if !filters.MatchesState(row.Active, isExpired) {
			continue
		}

		status := "active"
		if !row.Active {
			status = "inactive"
		}

		linkURL := fmt.Sprintf("%s/u/d/%s", getBrowserURL(c, ""), row.Token)
		if !filters.MatchesSearch(objName, row.Token, row.FilePath, repoName, creatorEmail, creatorName, linkURL) {
			continue
		}

		links = append(links, gin.H{
			"obj_name":      objName,
			"path":          row.FilePath,
			"token":         row.Token,
			"link":          linkURL,
			"repo_id":       row.LibraryID,
			"repo_name":     repoName,
			"creator_email": creatorEmail,
			"creator_name":  creatorName,
			"ctime":         row.CreatedAt.Format(time.RFC3339),
			"view_cnt":      row.UploadCount,
			"expire_date":   expireDateStr,
			"is_expired":    isExpired,
			"active":        row.Active,
			"has_password":  row.HasPassword,
			"status":        status,
		})
	}

	if links == nil {
		links = []gin.H{}
	}

	sortAdminLinks(links, sortBy, direction)
	pagedLinks, total, _ := paginateAdminLinks(links, page, perPage)

	c.JSON(http.StatusOK, gin.H{
		"upload_link_list": pagedLinks,
		"count":            total,
	})
}
