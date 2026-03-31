package v2

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
)

func (h *AdminHandler) listAllOrganizationIDs() ([]string, error) {
	var orgIDs []string
	iter := h.db.Session().Query(`SELECT org_id FROM organizations`).Iter()
	var orgID string
	for iter.Scan(&orgID) {
		orgIDs = append(orgIDs, orgID)
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return orgIDs, nil
}

// Global admin link listings currently have to walk org partitions because the
// data model exposes share_links_by_org, not a global admin projection ordered
// across all organizations. This keeps reads on partition-keyed tables, but a
// truly more scalable global listing would require its own denormalized table.

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

	orgIDs, err := h.listAllOrganizationIDs()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list organizations"})
		return
	}

	var links []gin.H

	libNameCache := map[string]string{}
	userCache := map[string][2]string{}
	for _, orgID := range orgIDs {
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

			objName := filePath
			if idx := strings.LastIndex(filePath, "/"); idx >= 0 && idx < len(filePath)-1 {
				objName = filePath[idx+1:]
			}

			isExpired := false
			expireDateStr := ""
			if expiresAt != nil && !expiresAt.IsZero() {
				isExpired = expiresAt.Before(time.Now())
				expireDateStr = expiresAt.Format("2006-01-02T15:04:05+00:00")
			}
			if !filters.MatchesState(active, isExpired) {
				continue
			}

			status := "active"
			if !active {
				status = "inactive"
			}

			libCacheKey := orgID + ":" + libID
			repoName, ok := libNameCache[libCacheKey]
			if !ok {
				h.db.Session().Query(`SELECT name FROM libraries WHERE org_id = ? AND library_id = ?`, orgID, libID).Scan(&repoName)
				if repoName == "" {
					repoName = "Unknown Library"
				}
				libNameCache[libCacheKey] = repoName
			}

			userCacheKey := orgID + ":" + createdBy
			userData, ok := userCache[userCacheKey]
			if !ok {
				var email, name string
				h.db.Session().Query(`SELECT email, name FROM users WHERE org_id = ? AND user_id = ?`, orgID, createdBy).Scan(&email, &name)
				if email == "" {
					email = createdBy
				}
				if name == "" {
					name = email
				}
				userData = [2]string{email, name}
				userCache[userCacheKey] = userData
			}
			if !filters.MatchesSearch(objName, token, filePath, repoName, userData[0], userData[1]) {
				continue
			}

			perms := parsePermsJSON(permission)
			count := 0
			if viewCount != nil {
				count = *viewCount
			}

			links = append(links, gin.H{
				"obj_name":      objName,
				"token":         token,
				"repo_id":       libID,
				"repo_name":     repoName,
				"path":          filePath,
				"creator_email": userData[0],
				"creator_name":  userData[1],
				"ctime":         createdAt.Format(time.RFC3339),
				"view_cnt":      count,
				"expire_date":   expireDateStr,
				"is_expired":    isExpired,
				"active":        active,
				"has_password":  hasPassword,
				"status":        status,
				"permissions":   gin.H{"can_download": perms.CanDownload, "can_edit": perms.CanEdit},
			})
		}
		if err := iter.Close(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list share links"})
			return
		}
	}

	if links == nil {
		links = []gin.H{}
	}

	sortBy := c.DefaultQuery("order_by", "")
	direction := c.DefaultQuery("direction", "asc")
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

	orgIDs, err := h.listAllOrganizationIDs()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list organizations"})
		return
	}

	var links []gin.H

	libNameCache := map[string]string{}
	userCache := map[string][2]string{}
	for _, orgID := range orgIDs {
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

			objName := filePath
			if idx := strings.LastIndex(filePath, "/"); idx >= 0 && idx < len(filePath)-1 {
				objName = filePath[idx+1:]
			}

			isExpired := false
			expireDateStr := ""
			if expiresAt != nil && !expiresAt.IsZero() {
				if expiresAt.Before(time.Now()) {
					isExpired = true
				}
				expireDateStr = expiresAt.Format("2006-01-02T15:04:05+00:00")
			}
			if !filters.MatchesState(active, isExpired) {
				continue
			}

			status := "active"
			if !active {
				status = "inactive"
			}

			libCacheKey := orgID + ":" + libID
			repoName, ok := libNameCache[libCacheKey]
			if !ok {
				h.db.Session().Query(`SELECT name FROM libraries WHERE org_id = ? AND library_id = ?`, orgID, libID).Scan(&repoName)
				if repoName == "" {
					repoName = "Unknown Library"
				}
				libNameCache[libCacheKey] = repoName
			}

			userCacheKey := orgID + ":" + createdBy
			userData, ok := userCache[userCacheKey]
			if !ok {
				var email, name string
				h.db.Session().Query(`SELECT email, name FROM users WHERE org_id = ? AND user_id = ?`, orgID, createdBy).Scan(&email, &name)
				if email == "" {
					email = createdBy
				}
				if name == "" {
					name = email
				}
				userData = [2]string{email, name}
				userCache[userCacheKey] = userData
			}
			if !filters.MatchesSearch(objName, token, filePath, repoName, userData[0], userData[1]) {
				continue
			}

			count := 0
			if uploadCount != nil {
				count = *uploadCount
			}

			links = append(links, gin.H{
				"obj_name":      objName,
				"path":          filePath,
				"token":         token,
				"repo_id":       libID,
				"repo_name":     repoName,
				"creator_email": userData[0],
				"creator_name":  userData[1],
				"ctime":         createdAt.Format(time.RFC3339),
				"view_cnt":      count,
				"expire_date":   expireDateStr,
				"is_expired":    isExpired,
				"active":        active,
				"has_password":  hasPassword,
				"status":        status,
			})
		}
		if err := iter.Close(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list upload links"})
			return
		}
	}

	if links == nil {
		links = []gin.H{}
	}

	sortBy := c.DefaultQuery("order_by", "")
	direction := c.DefaultQuery("direction", "asc")
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
	batch.Query(`UPDATE share_links_by_org SET active = ? WHERE org_id = ? AND created_at = ? AND link_token = ?`,
		active, orgID, createdAt, token)

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

	creatorName := email
	_ = h.db.Session().Query(`SELECT name FROM users WHERE org_id = ? AND user_id = ?`, targetOrgID, targetUserID).Scan(&creatorName)
	if creatorName == "" {
		creatorName = email
	}

	var links []gin.H
	iter := h.db.Session().Query(`
		SELECT link_token, link_type, library_id, file_path, permission, expires_at, has_password, active, view_count, download_count, created_at
		FROM share_links_by_creator WHERE org_id = ? AND created_by = ?`,
		targetOrgID, targetUserID).Iter()

	var token, linkType, libID, filePath, permission string
	var expiresAt *time.Time
	var hasPassword, active bool
	var viewCount, downloadCount int
	var createdAt time.Time

	libNameCache := map[string]string{}

	for iter.Scan(&token, &linkType, &libID, &filePath, &permission, &expiresAt, &hasPassword, &active, &viewCount, &downloadCount, &createdAt) {
		if linkType != "share" {
			continue
		}

		objName := filePath
		if idx := strings.LastIndex(filePath, "/"); idx >= 0 && idx < len(filePath)-1 {
			objName = filePath[idx+1:]
		}

		isExpired := false
		expireDateStr := ""
		if expiresAt != nil && !expiresAt.IsZero() {
			isExpired = expiresAt.Before(time.Now())
			expireDateStr = expiresAt.Format("2006-01-02T15:04:05+00:00")
		}
		if !filters.MatchesState(active, isExpired) {
			continue
		}

		status := "active"
		if !active {
			status = "inactive"
		}

		repoName, ok := libNameCache[libID]
		if !ok {
			h.db.Session().Query(`SELECT name FROM libraries WHERE org_id = ? AND library_id = ?`, targetOrgID, libID).Scan(&repoName)
			if repoName == "" {
				repoName = "Unknown Library"
			}
			libNameCache[libID] = repoName
		}

		linkURL := fmt.Sprintf("%s/d/%s", getBrowserURL(c, ""), token)
		if !filters.MatchesSearch(objName, token, filePath, repoName, email, creatorName, linkURL) {
			continue
		}

		links = append(links, gin.H{
			"obj_name":      objName,
			"token":         token,
			"link":          linkURL,
			"repo_id":       libID,
			"repo_name":     repoName,
			"path":          filePath,
			"creator_email": email,
			"creator_name":  creatorName,
			"ctime":         createdAt.Format(time.RFC3339),
			"view_cnt":      viewCount,
			"expire_date":   expireDateStr,
			"is_expired":    isExpired,
			"active":        active,
			"has_password":  hasPassword,
			"status":        status,
		})
	}
	if err := iter.Close(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list user share links"})
		return
	}

	if links == nil {
		links = []gin.H{}
	}

	sortBy := c.DefaultQuery("order_by", "")
	direction := c.DefaultQuery("direction", "asc")
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

	creatorName := email
	_ = h.db.Session().Query(`SELECT name FROM users WHERE org_id = ? AND user_id = ?`, targetOrgID, targetUserID).Scan(&creatorName)
	if creatorName == "" {
		creatorName = email
	}

	var links []gin.H
	iter := h.db.Session().Query(`
		SELECT link_token, link_type, library_id, file_path, expires_at, active, has_password, upload_count, created_at
		FROM share_links_by_creator WHERE org_id = ? AND created_by = ?`,
		targetOrgID, targetUserID).Iter()

	var token, linkType, libID, filePath string
	var expiresAt *time.Time
	var active, hasPassword bool
	var uploadCount *int
	var createdAt time.Time

	libNameCache := map[string]string{}

	for iter.Scan(&token, &linkType, &libID, &filePath, &expiresAt, &active, &hasPassword, &uploadCount, &createdAt) {
		if linkType != "upload" {
			continue
		}

		objName := filePath
		if idx := strings.LastIndex(filePath, "/"); idx >= 0 && idx < len(filePath)-1 {
			objName = filePath[idx+1:]
		}

		isExpired := false
		expireDateStr := ""
		if expiresAt != nil && !expiresAt.IsZero() {
			isExpired = expiresAt.Before(time.Now())
			expireDateStr = expiresAt.Format("2006-01-02T15:04:05+00:00")
		}
		if !filters.MatchesState(active, isExpired) {
			continue
		}

		status := "active"
		if !active {
			status = "inactive"
		}

		repoName, ok := libNameCache[libID]
		if !ok {
			h.db.Session().Query(`SELECT name FROM libraries WHERE org_id = ? AND library_id = ?`, targetOrgID, libID).Scan(&repoName)
			if repoName == "" {
				repoName = "Unknown Library"
			}
			libNameCache[libID] = repoName
		}

		count := 0
		if uploadCount != nil {
			count = *uploadCount
		}

		linkURL := fmt.Sprintf("%s/u/d/%s", getBrowserURL(c, ""), token)
		if !filters.MatchesSearch(objName, token, filePath, repoName, email, creatorName, linkURL) {
			continue
		}

		links = append(links, gin.H{
			"obj_name":      objName,
			"path":          filePath,
			"token":         token,
			"link":          linkURL,
			"repo_id":       libID,
			"repo_name":     repoName,
			"creator_email": email,
			"creator_name":  creatorName,
			"ctime":         createdAt.Format(time.RFC3339),
			"view_cnt":      count,
			"expire_date":   expireDateStr,
			"is_expired":    isExpired,
			"active":        active,
			"has_password":  hasPassword,
			"status":        status,
		})
	}
	if err := iter.Close(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list user upload links"})
		return
	}

	if links == nil {
		links = []gin.H{}
	}

	sortBy := c.DefaultQuery("order_by", "")
	direction := c.DefaultQuery("direction", "asc")
	sortAdminLinks(links, sortBy, direction)
	pagedLinks, total, _ := paginateAdminLinks(links, page, perPage)

	c.JSON(http.StatusOK, gin.H{
		"upload_link_list": pagedLinks,
		"count":            total,
	})
}
