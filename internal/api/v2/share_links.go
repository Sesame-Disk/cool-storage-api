package v2

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/httputil"
	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// ShareLinkHandler handles share link API requests
type ShareLinkHandler struct {
	db             *db.DB
	serverURL      string
	permMiddleware *middleware.PermissionMiddleware
	config         *config.Config
}

// NewShareLinkHandler creates a new ShareLinkHandler
func NewShareLinkHandler(database *db.DB, serverURL string, permMiddleware *middleware.PermissionMiddleware, cfg *config.Config) *ShareLinkHandler {
	return &ShareLinkHandler{db: database, serverURL: serverURL, permMiddleware: permMiddleware, config: cfg}
}

// ShareLinkResponse represents a share link in API response (Seafile-compatible)
type ShareLinkResponse struct {
	Token       string `json:"token"`
	RepoID      string `json:"repo_id"`
	RepoName    string `json:"repo_name"`
	Path        string `json:"path"`
	IsDir       bool   `json:"is_dir"`
	IsExpired   bool   `json:"is_expired"`
	ObjID       string `json:"obj_id,omitempty"`
	ObjName     string `json:"obj_name"`
	ViewCount   int    `json:"view_cnt"`
	CTime       string `json:"ctime"`
	ExpireDate  string `json:"expire_date,omitempty"`
	CanEdit     bool   `json:"can_edit"`
	CanDownload bool   `json:"can_download"`
	Permissions Perms  `json:"permissions"`
	UserEmail   string `json:"username"`
	CreatorName string `json:"creator_name"`
	LinkURL     string `json:"link,omitempty"`
	IsOwner     bool   `json:"is_owner"`
	Password    string `json:"password,omitempty"`
	HasPassword bool   `json:"has_password"`
}

// Perms represents permission settings for share links (always stored as JSON in DB)
type Perms struct {
	CanEdit     bool `json:"can_edit"`
	CanDownload bool `json:"can_download"`
	CanUpload   bool `json:"can_upload"`
}

// RegisterShareLinkRoutes registers share link routes
func RegisterShareLinkRoutes(rg *gin.RouterGroup, database *db.DB, serverURL string, permMiddleware *middleware.PermissionMiddleware, cfg *config.Config) *ShareLinkHandler {
	h := NewShareLinkHandler(database, serverURL, permMiddleware, cfg)

	shareLinks := rg.Group("/share-links")
	{
		shareLinks.GET("", h.ListShareLinks)
		shareLinks.GET("/", h.ListShareLinks)
		shareLinks.POST("", h.CreateShareLink)
		shareLinks.POST("/", h.CreateShareLink)
		shareLinks.PUT("/:token", h.UpdateShareLink)
		shareLinks.PUT("/:token/", h.UpdateShareLink)
		shareLinks.DELETE("", h.BatchDeleteShareLinks)
		shareLinks.DELETE("/", h.BatchDeleteShareLinks)
		shareLinks.DELETE("/:token", h.DeleteShareLink)
		shareLinks.DELETE("/:token/", h.DeleteShareLink)
	}

	// Multi-share-links: Seafile frontend uses this endpoint for creating share links
	// that can be accessed multiple times. Functionally the same as regular share links.
	multiShareLinks := rg.Group("/multi-share-links")
	{
		multiShareLinks.GET("", h.ListShareLinks)
		multiShareLinks.GET("/", h.ListShareLinks)
		multiShareLinks.POST("", h.CreateShareLink)
		multiShareLinks.POST("/", h.CreateShareLink)
		multiShareLinks.POST("/batch", h.BatchCreateShareLinks)
		multiShareLinks.POST("/batch/", h.BatchCreateShareLinks)
		multiShareLinks.PUT("/:token", h.UpdateShareLink)
		multiShareLinks.PUT("/:token/", h.UpdateShareLink)
		multiShareLinks.DELETE("/:token", h.DeleteShareLink)
		multiShareLinks.DELETE("/:token/", h.DeleteShareLink)
	}

	// Repo-specific share links (used by frontend file detail panel)
	repoShareLinks := rg.Group("/repos/:repo_id/share-links")
	{
		repoShareLinks.GET("", h.ListRepoShareLinks)
		repoShareLinks.GET("/", h.ListRepoShareLinks)
	}

	return h
}

// parsePermsJSON parses a JSON permission string into Perms struct
func parsePermsJSON(permission string) Perms {
	if permission == "" {
		return Perms{CanDownload: true}
	}
	var perms Perms
	if err := json.Unmarshal([]byte(permission), &perms); err != nil {
		// Fallback for legacy string format during transition
		switch permission {
		case "edit":
			return Perms{CanEdit: true, CanDownload: true}
		case "upload":
			return Perms{CanUpload: true, CanDownload: true}
		case "preview_download", "download":
			return Perms{CanDownload: true}
		case "preview_only":
			return Perms{}
		default:
			return Perms{CanDownload: true}
		}
	}
	return perms
}

// permsToJSON converts a Perms struct to JSON string for DB storage
func permsToJSON(p Perms) string {
	b, _ := json.Marshal(p)
	return string(b)
}

// normalizePermissionInput converts frontend permission input (string or JSON) to canonical JSON
func normalizePermissionInput(input string) string {
	if input == "" {
		return permsToJSON(Perms{CanDownload: true})
	}
	if strings.HasPrefix(input, "{") {
		// Already JSON — parse and re-serialize to ensure canonical format
		var perms Perms
		if err := json.Unmarshal([]byte(input), &perms); err == nil {
			return permsToJSON(perms)
		}
	}
	// Legacy string format — convert to JSON
	switch input {
	case "edit":
		return permsToJSON(Perms{CanEdit: true, CanDownload: true})
	case "upload":
		return permsToJSON(Perms{CanUpload: true, CanDownload: true})
	case "preview_download", "download":
		return permsToJSON(Perms{CanDownload: true})
	case "preview_only":
		return permsToJSON(Perms{})
	default:
		return permsToJSON(Perms{CanDownload: true})
	}
}

// objNameFromPath extracts the display name from a file path
func objNameFromPath(filePath, repoName string) string {
	if filePath == "/" {
		return repoName
	}
	trimmed := strings.TrimSuffix(filePath, "/")
	if idx := strings.LastIndex(trimmed, "/"); idx >= 0 {
		return trimmed[idx+1:]
	}
	return filePath
}

// insertShareLink inserts a share link into all 4 tables using a logged batch.
// If expiresAt is set, applies TTL to all tables for automatic cleanup.
// viewCount/downloadCount/uploadCount allow preserving counters during updates (pass 0 for new links).
func (h *ShareLinkHandler) insertShareLink(
	token, linkType, orgID, libraryID, filePath, createdBy, permission, passwordHash string,
	expiresAt *time.Time, singleUse bool, createdAt time.Time,
	viewCount, downloadCount, uploadCount int,
) error {
	batch := h.db.Session().Batch(gocql.LoggedBatch)
	hasPassword := passwordHash != ""

	// Calculate TTL if expires_at is set
	var ttlSeconds int
	if expiresAt != nil {
		ttlSeconds = int(time.Until(*expiresAt).Seconds())
		if ttlSeconds < 1 {
			ttlSeconds = 1
		}
	}

	// 1. Primary table
	if ttlSeconds > 0 {
		batch.Query(`
			INSERT INTO share_links (
				link_token, link_type, org_id, library_id, file_path, created_by, permission,
				password_hash, expires_at, single_use, active,
				view_count, download_count, upload_count, max_downloads,
				last_accessed_at, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, true, ?, ?, ?, null, null, ?) USING TTL ?
		`, token, linkType, orgID, libraryID, filePath, createdBy, permission,
			passwordHash, expiresAt, singleUse, viewCount, downloadCount, uploadCount, createdAt, ttlSeconds)
	} else {
		batch.Query(`
			INSERT INTO share_links (
				link_token, link_type, org_id, library_id, file_path, created_by, permission,
				password_hash, expires_at, single_use, active,
				view_count, download_count, upload_count, max_downloads,
				last_accessed_at, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, true, ?, ?, ?, null, null, ?)
		`, token, linkType, orgID, libraryID, filePath, createdBy, permission,
			passwordHash, expiresAt, singleUse, viewCount, downloadCount, uploadCount, createdAt)
	}

	// 2. By creator (for "my links")
	if ttlSeconds > 0 {
		batch.Query(`
			INSERT INTO share_links_by_creator (
				org_id, created_by, created_at, link_token, link_type, library_id, file_path,
				permission, expires_at, single_use, active,
				view_count, download_count, upload_count, max_downloads,
				has_password, last_accessed_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, true, ?, ?, ?, null, ?, null) USING TTL ?
		`, orgID, createdBy, createdAt, token, linkType, libraryID, filePath,
			permission, expiresAt, singleUse, viewCount, downloadCount, uploadCount, hasPassword, ttlSeconds)
	} else {
		batch.Query(`
			INSERT INTO share_links_by_creator (
				org_id, created_by, created_at, link_token, link_type, library_id, file_path,
				permission, expires_at, single_use, active,
				view_count, download_count, upload_count, max_downloads,
				has_password, last_accessed_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, true, ?, ?, ?, null, ?, null)
		`, orgID, createdBy, createdAt, token, linkType, libraryID, filePath,
			permission, expiresAt, singleUse, viewCount, downloadCount, uploadCount, hasPassword)
	}

	// 3. By library (for orphan cleanup)
	if ttlSeconds > 0 {
		batch.Query(`
			INSERT INTO share_links_by_library (
				org_id, library_id, link_token, link_type, created_by, created_at
			) VALUES (?, ?, ?, ?, ?, ?) USING TTL ?
		`, orgID, libraryID, token, linkType, createdBy, createdAt, ttlSeconds)
	} else {
		batch.Query(`
			INSERT INTO share_links_by_library (
				org_id, library_id, link_token, link_type, created_by, created_at
			) VALUES (?, ?, ?, ?, ?, ?)
		`, orgID, libraryID, token, linkType, createdBy, createdAt)
	}
	if expiresAt != nil {
		db.AddUpsertShareLinkExpiryQuery(batch, token, orgID, libraryID, createdBy, linkType, createdAt, *expiresAt)
	}
	repoName, objName, creatorEmail, creatorName := db.ResolveAdminLinkDisplayFields(h.db.Session(), orgID, libraryID, filePath, createdBy)
	db.AddUpsertAdminLinkReadModelQuery(
		batch,
		token,
		linkType,
		orgID,
		libraryID,
		filePath,
		createdBy,
		permission,
		repoName,
		objName,
		creatorEmail,
		creatorName,
		expiresAt,
		hasPassword,
		true,
		viewCount,
		uploadCount,
		ttlSeconds,
		createdAt,
	)

	if err := batch.Exec(); err != nil {
		return err
	}
	db.BestEffortAdjustAdminOrgLinkCount(h.db.Session(), orgID, linkType, db.AdminOrgLinkCountDelta(1))
	return nil
}

// deleteShareLink deletes a link from the canonical tables and admin projections.
// Requires createdAt for the clustering key in _by_creator.
func (h *ShareLinkHandler) deleteShareLink(token, orgID, createdBy, libraryID string, createdAt time.Time) error {
	var linkType string
	var expiresAt *time.Time
	if err := h.db.Session().Query(`SELECT link_type, expires_at FROM share_links WHERE link_token = ?`, token).Scan(&linkType, &expiresAt); err != nil {
		return err
	}

	batch := h.db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`DELETE FROM share_links WHERE link_token = ?`, token)
	batch.Query(`DELETE FROM share_links_by_creator WHERE org_id = ? AND created_by = ? AND created_at = ? AND link_token = ?`,
		orgID, createdBy, createdAt, token)
	batch.Query(`DELETE FROM share_links_by_library WHERE org_id = ? AND library_id = ? AND link_token = ?`,
		orgID, libraryID, token)
	if expiresAt != nil && !expiresAt.IsZero() {
		db.AddDeleteShareLinkExpiryQuery(batch, token, *expiresAt)
	}
	db.AddDeleteAdminLinkReadModelQuery(batch, linkType, createdAt, orgID, token)
	if err := batch.Exec(); err != nil {
		return err
	}
	db.BestEffortAdjustAdminOrgLinkCount(h.db.Session(), orgID, linkType, db.AdminOrgLinkCountDelta(-1))
	return nil
}

// ListShareLinks returns share links for a file or all share links
// Implements: GET /api/v2.1/share-links/?repo_id=xxx&path=/xxx
func (h *ShareLinkHandler) ListShareLinks(c *gin.Context) {
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")
	repoIDFilter := c.Query("repo_id")
	pathFilter := c.Query("path")

	orgUUID, err := gocql.ParseUUID(orgID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid org_id"})
		return
	}
	userUUID, err := gocql.ParseUUID(userID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user_id"})
		return
	}

	// Query from unified table — already ordered by created_at DESC
	iter := h.db.Session().Query(`
		SELECT link_token, link_type, library_id, file_path, permission, expires_at,
		       view_count, download_count, max_downloads, created_at, has_password
		FROM share_links_by_creator
		WHERE org_id = ? AND created_by = ?
	`, orgUUID, userUUID).Iter()

	var links []ShareLinkResponse
	var token, linkType, libID, filePath, permission string
	var expiresAt *time.Time
	var viewCount, downloadCount int
	var maxDownloads *int
	var createdAt time.Time
	var hasPassword bool

	// Get user email and name once
	var userEmail, userName string
	if err := h.db.Session().Query(`SELECT email, name FROM users WHERE org_id = ? AND user_id = ?`, orgUUID, userUUID).Scan(&userEmail, &userName); err != nil || userEmail == "" {
		userEmail = userID
	}
	if userName == "" {
		userName = userEmail
	}

	for iter.Scan(&token, &linkType, &libID, &filePath, &permission, &expiresAt,
		&viewCount, &downloadCount, &maxDownloads, &createdAt, &hasPassword) {
		// Only return share links from this endpoint
		if linkType != "share" {
			continue
		}

		// Client-side filtering by repo_id and path (if specified)
		if repoIDFilter != "" && libID != repoIDFilter {
			continue
		}
		if pathFilter != "" && filePath != pathFilter {
			continue
		}

		isExpired := false
		if expiresAt != nil && time.Now().After(*expiresAt) {
			isExpired = true
		}

		expireDate := ""
		if expiresAt != nil {
			expireDate = expiresAt.Format(time.RFC3339)
		}

		// Get library name
		var repoName string
		libUUID, parseErr := gocql.ParseUUID(libID)
		if parseErr == nil {
			h.db.Session().Query(`SELECT name FROM libraries WHERE org_id = ? AND library_id = ?`, orgUUID, libUUID).Scan(&repoName)
		}
		if repoName == "" {
			repoName = "Unknown Library"
		}

		perms := parsePermsJSON(permission)

		links = append(links, ShareLinkResponse{
			Token:       token,
			RepoID:      libID,
			RepoName:    repoName,
			Path:        filePath,
			IsDir:       filePath == "/" || strings.HasSuffix(filePath, "/"),
			IsExpired:   isExpired,
			ObjName:     objNameFromPath(filePath, repoName),
			ViewCount:   viewCount,
			CTime:       createdAt.Format(time.RFC3339),
			ExpireDate:  expireDate,
			CanEdit:     perms.CanEdit,
			CanDownload: perms.CanDownload,
			Permissions: perms,
			UserEmail:   userEmail,
			CreatorName: userName,
			LinkURL:     fmt.Sprintf("%s/d/%s", httputil.GetBrowserURL(c, h.serverURL), token),
			IsOwner:     true,
			HasPassword: hasPassword,
		})
	}

	if err := iter.Close(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to list share links: %v", err)})
		return
	}

	if links == nil {
		links = []ShareLinkResponse{}
	}

	// In-memory pagination (TODO: migrate to PageState cursor-based pagination)
	if pageStr := c.Query("page"); pageStr != "" {
		page, _ := strconv.Atoi(pageStr)
		perPage, _ := strconv.Atoi(c.DefaultQuery("per_page", "25"))
		if page < 1 {
			page = 1
		}
		if perPage < 1 {
			perPage = 25
		}
		start := (page - 1) * perPage
		if start >= len(links) {
			links = []ShareLinkResponse{}
		} else {
			end := start + perPage
			if end > len(links) {
				end = len(links)
			}
			links = links[start:end]
		}
	}

	c.JSON(http.StatusOK, links)
}

// ShareLinkCreateRequest represents the request for creating a share link
type ShareLinkCreateRequest struct {
	RepoID         string `json:"repo_id" form:"repo_id"`
	Path           string `json:"path" form:"path"`
	Password       string `json:"password" form:"password"`
	ExpireDays     int    `json:"expire_days" form:"expire_days"`
	ExpirationTime string `json:"expiration_time" form:"expiration_time"`
	Permissions    string `json:"permissions" form:"permissions"` // "preview_download", "preview_only", or JSON
}

// CreateShareLink creates a new share link
// Implements: POST /api/v2.1/share-links/
func (h *ShareLinkHandler) CreateShareLink(c *gin.Context) {
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	var req ShareLinkCreateRequest
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.RepoID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "repo_id is required"})
		return
	}

	if req.Path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is required"})
		return
	}

	// Validate repo exists and user has access
	if _, err := uuid.Parse(req.RepoID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid repo_id"})
		return
	}

	libraryState, err := readLiveLibraryStateFn(h.db.Session(), orgID, req.RepoID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}

	// PERMISSION CHECK: User must have at least read access to the library
	if h.permMiddleware != nil {
		hasAccess, err := h.permMiddleware.HasLibraryAccess(orgID, userID, req.RepoID, middleware.PermissionR)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check permissions"})
			return
		}
		if !hasAccess {
			c.JSON(http.StatusForbidden, gin.H{"error": "you do not have access to this library"})
			return
		}
	}

	// CUSTOM PERMISSION CHECK: download_external_link flag
	if h.permMiddleware != nil && !h.permMiddleware.RequirePermFlagForRepo(c, req.RepoID, "download_external_link") {
		c.JSON(http.StatusForbidden, gin.H{"error": "generating share links is not allowed by your permission"})
		return
	}

	// ENFORCEMENT CHECK: feature flag + numeric limit + expiry cap
	if h.config != nil {
		enforcement := GetOrgEnforcement(h.db, orgID, h.config)
		if !enforcement.Profile.Features.CanGenerateShareLink {
			c.JSON(http.StatusForbidden, gin.H{
				"error":            "Share link creation is not available on your plan",
				"upgrade_required": true,
			})
			return
		}
		if enforcement.Profile.Limits.MaxShareLinks > 0 {
			count := CountShareLinks(h.db, orgID)
			if count >= enforcement.Profile.Limits.MaxShareLinks {
				c.JSON(http.StatusForbidden, gin.H{
					"error":   "Share link limit reached",
					"limit":   enforcement.Profile.Limits.MaxShareLinks,
					"current": count,
				})
				return
			}
		}
		if max := enforcement.Profile.Limits.ShareLinkExpireDaysMax; max > 0 {
			if req.ExpireDays > max {
				req.ExpireDays = max
			}
			if req.ExpireDays == 0 && req.ExpirationTime == "" {
				req.ExpireDays = max
			}
		}
	}

	// Normalize permission to canonical JSON format
	permissionJSON := normalizePermissionInput(req.Permissions)

	// Generate secure token
	token, err := generateSecureShareToken(16)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate token"})
		return
	}

	// Hash password if provided
	var passwordHash string
	if req.Password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to hash password"})
			return
		}
		passwordHash = string(hash)
	}

	// Calculate expiration
	var expiresAt *time.Time
	if req.ExpirationTime != "" {
		if t, err := time.Parse(time.RFC3339, req.ExpirationTime); err == nil {
			expiresAt = &t
		} else if t, err := time.Parse("2006-01-02", req.ExpirationTime); err == nil {
			expiresAt = &t
		}
	} else if req.ExpireDays > 0 {
		exp := time.Now().AddDate(0, 0, req.ExpireDays)
		expiresAt = &exp
	}

	now := time.Now()

	// Insert into all 4 tables
	if err := h.insertShareLink(
		token, "share", orgID, req.RepoID, req.Path, userID,
		permissionJSON, passwordHash, expiresAt, false, now,
		0, 0, 0,
	); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create share link"})
		return
	}

	// Build response
	orgUUID, _ := gocql.ParseUUID(orgID)
	userUUID, _ := gocql.ParseUUID(userID)
	repoName := libraryState.Name
	if repoName == "" {
		repoName = "Unknown Library"
	}

	var userEmail, userName string
	if err := h.db.Session().Query(`SELECT email, name FROM users WHERE org_id = ? AND user_id = ?`, orgUUID, userUUID).Scan(&userEmail, &userName); err != nil || userEmail == "" {
		userEmail = userID
	}
	if userName == "" {
		userName = userEmail
	}

	perms := parsePermsJSON(permissionJSON)
	expireDate := ""
	if expiresAt != nil {
		expireDate = expiresAt.Format(time.RFC3339)
	}

	c.JSON(http.StatusOK, ShareLinkResponse{
		Token:       token,
		RepoID:      req.RepoID,
		RepoName:    repoName,
		Path:        req.Path,
		IsDir:       req.Path == "/" || strings.HasSuffix(req.Path, "/"),
		IsExpired:   false,
		ObjName:     objNameFromPath(req.Path, repoName),
		ViewCount:   0,
		CTime:       now.Format(time.RFC3339),
		ExpireDate:  expireDate,
		CanEdit:     perms.CanEdit,
		CanDownload: perms.CanDownload,
		Permissions: perms,
		UserEmail:   userEmail,
		CreatorName: userName,
		LinkURL:     fmt.Sprintf("%s/d/%s", httputil.GetBrowserURL(c, h.serverURL), token),
		IsOwner:     true,
		Password:    req.Password,
		HasPassword: req.Password != "",
	})
}

// DeleteShareLink deletes a share link
// Implements: DELETE /api/v2.1/share-links/:token/
func (h *ShareLinkHandler) DeleteShareLink(c *gin.Context) {
	token := c.Param("token")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	// Read from primary table to verify ownership and get clustering keys
	var createdBy, libID string
	var createdAt time.Time
	if err := h.db.Session().Query(`
		SELECT created_by, library_id, created_at FROM share_links WHERE link_token = ?
	`, token).Scan(&createdBy, &libID, &createdAt); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "share link not found"})
		return
	}

	if createdBy != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "not authorized to delete this share link"})
		return
	}

	if err := h.deleteShareLink(token, orgID, userID, libID, createdAt); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete share link"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ListRepoShareLinks returns share links for a specific repo.
// Implements: GET /api/v2.1/repos/:repo_id/share-links/
func (h *ShareLinkHandler) ListRepoShareLinks(c *gin.Context) {
	repoID := c.Param("repo_id")
	c.Request.URL.RawQuery = fmt.Sprintf("repo_id=%s&%s", repoID, c.Request.URL.RawQuery)
	h.ListShareLinks(c)
}

// generateSecureShareToken generates a URL-safe random token
func generateSecureShareToken(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

// UpdateShareLink updates a share link's permissions and/or expiration
// Implements: PUT /api/v2.1/share-links/:token/
func (h *ShareLinkHandler) UpdateShareLink(c *gin.Context) {
	token := c.Param("token")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	// Look up existing link to verify ownership and get current values
	var createdBy, libID, filePath, currentPermission, currentPasswordHash string
	var currentExpiresAt *time.Time
	var viewCount, downloadCount int
	var maxDownloads *int
	var createdAt time.Time

	if err := h.db.Session().Query(`
		SELECT created_by, library_id, file_path, permission, expires_at,
		       view_count, download_count, max_downloads, created_at, password_hash
		FROM share_links WHERE link_token = ?
	`, token).Scan(&createdBy, &libID, &filePath, &currentPermission, &currentExpiresAt,
		&viewCount, &downloadCount, &maxDownloads, &createdAt, &currentPasswordHash); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "share link not found"})
		return
	}

	if createdBy != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "not authorized to update this share link"})
		return
	}

	if _, err := readLiveLibraryStateFn(h.db.Session(), orgID, libID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}

	// Parse update fields
	var updateBody struct {
		Permissions    string `json:"permissions"`
		ExpirationTime string `json:"expiration_time"`
		Password       string `json:"password"`
	}
	if err := c.ShouldBindJSON(&updateBody); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	permissionsInput := updateBody.Permissions
	expirationTime := updateBody.ExpirationTime
	newPasswordPlain := updateBody.Password

	newPermission := currentPermission
	newExpiresAt := currentExpiresAt

	if permissionsInput != "" {
		newPermission = normalizePermissionInput(permissionsInput)
	}

	if expirationTime != "" {
		if t, err := time.Parse(time.RFC3339, expirationTime); err == nil {
			newExpiresAt = &t
		} else if t, err := time.Parse("2006-01-02", expirationTime); err == nil {
			newExpiresAt = &t
		}
	}

	// Handle password change
	newPasswordHash := currentPasswordHash
	returnedPassword := ""
	if newPasswordPlain == "__remove__" {
		newPasswordHash = ""
	} else if newPasswordPlain != "" {
		hashed, err := bcrypt.GenerateFromPassword([]byte(newPasswordPlain), bcrypt.DefaultCost)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to hash password"})
			return
		}
		newPasswordHash = string(hashed)
		returnedPassword = newPasswordPlain
	}

	// For updates, we re-insert (upsert) to handle TTL changes properly
	// Delete old entry and insert new one with potentially new TTL
	if err := h.deleteShareLink(token, orgID, userID, libID, createdAt); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update share link"})
		return
	}
	if err := h.insertShareLink(
		token, "share", orgID, libID, filePath, userID,
		newPermission, newPasswordHash, newExpiresAt, false, createdAt,
		viewCount, downloadCount, 0,
	); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update share link"})
		return
	}

	// Build response
	orgUUID, _ := gocql.ParseUUID(orgID)
	userUUID, _ := gocql.ParseUUID(userID)
	libUUID, _ := gocql.ParseUUID(libID)

	var repoName string
	h.db.Session().Query(`SELECT name FROM libraries WHERE org_id = ? AND library_id = ?`, orgUUID, libUUID).Scan(&repoName)
	if repoName == "" {
		repoName = "Unknown Library"
	}

	var userEmail, updateUserName string
	if err := h.db.Session().Query(`SELECT email, name FROM users WHERE org_id = ? AND user_id = ?`, orgUUID, userUUID).Scan(&userEmail, &updateUserName); err != nil || userEmail == "" {
		userEmail = userID
	}
	if updateUserName == "" {
		updateUserName = userEmail
	}

	isExpired := false
	if newExpiresAt != nil && time.Now().After(*newExpiresAt) {
		isExpired = true
	}

	expireDate := ""
	if newExpiresAt != nil {
		expireDate = newExpiresAt.Format(time.RFC3339)
	}

	perms := parsePermsJSON(newPermission)

	c.JSON(http.StatusOK, ShareLinkResponse{
		Token:       token,
		RepoID:      libID,
		RepoName:    repoName,
		Path:        filePath,
		IsDir:       filePath == "/" || strings.HasSuffix(filePath, "/"),
		IsExpired:   isExpired,
		ObjName:     objNameFromPath(filePath, repoName),
		ViewCount:   viewCount,
		CTime:       createdAt.Format(time.RFC3339),
		ExpireDate:  expireDate,
		CanEdit:     perms.CanEdit,
		CanDownload: perms.CanDownload,
		Permissions: perms,
		UserEmail:   userEmail,
		CreatorName: updateUserName,
		LinkURL:     fmt.Sprintf("%s/d/%s", httputil.GetBrowserURL(c, h.serverURL), token),
		IsOwner:     true,
		Password:    returnedPassword,
		HasPassword: newPasswordHash != "",
	})
}

// BatchDeleteShareLinks deletes multiple share links at once
// Implements: DELETE /api/v2.1/share-links/
func (h *ShareLinkHandler) BatchDeleteShareLinks(c *gin.Context) {
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	var req struct {
		Tokens []string `json:"tokens"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "tokens array required"})
		return
	}

	success := make([]gin.H, 0)
	failed := make([]gin.H, 0)

	for _, tk := range req.Tokens {
		var createdBy, libID string
		var createdAt time.Time
		err := h.db.Session().Query(
			`SELECT created_by, library_id, created_at FROM share_links WHERE link_token = ?`, tk,
		).Scan(&createdBy, &libID, &createdAt)
		if err != nil {
			failed = append(failed, gin.H{"token": tk, "error_msg": "not found"})
			continue
		}
		if createdBy != userID {
			failed = append(failed, gin.H{"token": tk, "error_msg": "permission denied"})
			continue
		}

		if err := h.deleteShareLink(tk, orgID, userID, libID, createdAt); err != nil {
			failed = append(failed, gin.H{"token": tk, "error_msg": err.Error()})
			continue
		}
		success = append(success, gin.H{"token": tk})
	}

	c.JSON(http.StatusOK, gin.H{"success": success, "failed": failed})
}

// generateRandomPassword generates a cryptographically secure random password
func generateRandomPassword(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%"
	b := make([]byte, length)
	for i := range b {
		randBytes := make([]byte, 1)
		rand.Read(randBytes)
		b[i] = charset[int(randBytes[0])%len(charset)]
	}
	return string(b)
}

// BatchCreateShareLinks creates multiple share links at once
// Implements: POST /api/v2.1/multi-share-links/batch/
func (h *ShareLinkHandler) BatchCreateShareLinks(c *gin.Context) {
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	var req struct {
		RepoID               string `form:"repo_id"`
		Path                 string `form:"path"`
		Number               int    `form:"number"`
		AutoGeneratePassword bool   `form:"auto_generate_password"`
		ExpirationTime       string `form:"expiration_time"`
		Permissions          string `form:"permissions"`
	}
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.RepoID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "repo_id is required"})
		return
	}
	if req.Number < 2 || req.Number > 200 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "number must be between 2 and 200"})
		return
	}
	if req.Path == "" {
		req.Path = "/"
	}

	// Validate repo access
	if _, err := uuid.Parse(req.RepoID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid repo_id"})
		return
	}

	libraryState, err := readLiveLibraryStateFn(h.db.Session(), orgID, req.RepoID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}

	if h.permMiddleware != nil {
		hasAccess, err := h.permMiddleware.HasLibraryAccess(orgID, userID, req.RepoID, middleware.PermissionR)
		if err != nil || !hasAccess {
			c.JSON(http.StatusForbidden, gin.H{"error": "you do not have access to this library"})
			return
		}
	}

	// ENFORCEMENT CHECK: feature flag + numeric limit
	if h.config != nil {
		enforcement := GetOrgEnforcement(h.db, orgID, h.config)
		if !enforcement.Profile.Features.CanGenerateShareLink {
			c.JSON(http.StatusForbidden, gin.H{
				"error":            "Share link creation is not available on your plan",
				"upgrade_required": true,
			})
			return
		}
		if enforcement.Profile.Limits.MaxShareLinks > 0 {
			count := CountShareLinks(h.db, orgID)
			if count+req.Number > enforcement.Profile.Limits.MaxShareLinks {
				c.JSON(http.StatusForbidden, gin.H{
					"error":   "Share link limit would be exceeded",
					"limit":   enforcement.Profile.Limits.MaxShareLinks,
					"current": count,
				})
				return
			}
		}
	}

	permissionJSON := normalizePermissionInput(req.Permissions)

	// Parse expiration
	var expiresAt *time.Time
	if req.ExpirationTime != "" {
		if t, err := time.Parse(time.RFC3339, req.ExpirationTime); err == nil {
			expiresAt = &t
		} else if t, err := time.Parse("2006-01-02", req.ExpirationTime); err == nil {
			expiresAt = &t
		}
	}

	orgUUID, _ := gocql.ParseUUID(orgID)
	userUUID, _ := gocql.ParseUUID(userID)
	repoName := libraryState.Name
	if repoName == "" {
		repoName = "Unknown Library"
	}

	// Get creator info
	var userEmail, userName string
	h.db.Session().Query(`SELECT email, name FROM users WHERE org_id = ? AND user_id = ?`, orgUUID, userUUID).Scan(&userEmail, &userName)
	if userEmail == "" {
		userEmail = userID
	}
	if userName == "" {
		userName = userEmail
	}

	perms := parsePermsJSON(permissionJSON)
	now := time.Now()
	expireDate := ""
	if expiresAt != nil {
		expireDate = expiresAt.Format(time.RFC3339)
	}

	var links []ShareLinkResponse

	for i := 0; i < req.Number; i++ {
		token, err := generateSecureShareToken(16)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate token"})
			return
		}

		var password, passwordHash string
		if req.AutoGeneratePassword {
			password = generateRandomPassword(10)
			hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to hash password"})
				return
			}
			passwordHash = string(hash)
		}

		if err := h.insertShareLink(
			token, "share", orgID, req.RepoID, req.Path, userID,
			permissionJSON, passwordHash, expiresAt, false, now,
			0, 0, 0,
		); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create share link"})
			return
		}

		links = append(links, ShareLinkResponse{
			Token:       token,
			RepoID:      req.RepoID,
			RepoName:    repoName,
			Path:        req.Path,
			IsDir:       req.Path == "/" || strings.HasSuffix(req.Path, "/"),
			IsExpired:   false,
			ObjName:     objNameFromPath(req.Path, repoName),
			ViewCount:   0,
			CTime:       now.Format(time.RFC3339),
			ExpireDate:  expireDate,
			CanEdit:     perms.CanEdit,
			CanDownload: perms.CanDownload,
			Permissions: perms,
			UserEmail:   userEmail,
			CreatorName: userName,
			LinkURL:     fmt.Sprintf("%s/d/%s", httputil.GetBrowserURL(c, h.serverURL), token),
			IsOwner:     true,
			Password:    password,
			HasPassword: password != "",
		})
	}

	c.JSON(http.StatusOK, links)
}
