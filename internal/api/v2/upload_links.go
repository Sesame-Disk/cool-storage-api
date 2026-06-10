package v2

import (
	"fmt"
	"net/http"
	"strconv"
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

// UploadLinkHandler handles upload link API requests.
// Uses the unified share_links tables via ShareLinkHandler helpers.
type UploadLinkHandler struct {
	db             *db.DB
	serverURL      string
	permMiddleware *middleware.PermissionMiddleware
	config         *config.Config
	shareHandler   *ShareLinkHandler // reuse insertShareLink/deleteShareLink
}

// NewUploadLinkHandler creates a new UploadLinkHandler
func NewUploadLinkHandler(database *db.DB, serverURL string, permMiddleware *middleware.PermissionMiddleware, cfg *config.Config) *UploadLinkHandler {
	return &UploadLinkHandler{
		db:             database,
		serverURL:      serverURL,
		permMiddleware: permMiddleware,
		config:         cfg,
		shareHandler:   &ShareLinkHandler{db: database, serverURL: serverURL, permMiddleware: permMiddleware, config: cfg},
	}
}

// UploadLinkResponse represents an upload link in API response
type UploadLinkResponse struct {
	Token       string `json:"token"`
	RepoID      string `json:"repo_id"`
	RepoName    string `json:"repo_name"`
	Path        string `json:"path"`
	ObjName     string `json:"obj_name"`
	IsExpired   bool   `json:"is_expired"`
	ViewCount   int    `json:"view_cnt"`
	UploadCount int    `json:"upload_cnt"`
	CTime       string `json:"ctime"`
	ExpireDate  string `json:"expire_date,omitempty"`
	UserEmail   string `json:"username"`
	CreatorName string `json:"creator_name"`
	LinkURL     string `json:"link,omitempty"`
	IsOwner     bool   `json:"is_owner"`
	Password    string `json:"password,omitempty"`
	HasPassword bool   `json:"has_password"`
}

// RegisterUploadLinkRoutes registers upload link routes
func RegisterUploadLinkRoutes(rg *gin.RouterGroup, database *db.DB, serverURL string, permMiddleware *middleware.PermissionMiddleware, cfg *config.Config) *UploadLinkHandler {
	h := NewUploadLinkHandler(database, serverURL, permMiddleware, cfg)

	uploadLinks := rg.Group("/upload-links")
	{
		uploadLinks.GET("", h.ListUploadLinks)
		uploadLinks.GET("/", h.ListUploadLinks)
		uploadLinks.POST("", h.CreateUploadLink)
		uploadLinks.POST("/", h.CreateUploadLink)
		uploadLinks.PUT("/:token", h.UpdateUploadLink)
		uploadLinks.PUT("/:token/", h.UpdateUploadLink)
		uploadLinks.DELETE("/:token", h.DeleteUploadLink)
		uploadLinks.DELETE("/:token/", h.DeleteUploadLink)
	}

	// Repo-specific upload links
	repoUploadLinks := rg.Group("/repos/:repo_id/upload-links")
	{
		repoUploadLinks.GET("", h.ListRepoUploadLinks)
		repoUploadLinks.GET("/", h.ListRepoUploadLinks)
	}

	return h
}

// ListUploadLinks returns upload links for the authenticated user
// Implements: GET /api/v2.1/upload-links/?repo_id=xxx
func (h *UploadLinkHandler) ListUploadLinks(c *gin.Context) {
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
		SELECT link_token, link_type, library_id, file_path, expires_at,
		       view_count, upload_count, created_at, has_password
		FROM share_links_by_creator
		WHERE org_id = ? AND created_by = ?
	`, orgUUID, userUUID).Iter()

	var links []UploadLinkResponse
	var token, linkType, libID, filePath string
	var expiresAt *time.Time
	var viewCount, uploadCount int
	var createdAt time.Time
	var hasPassword bool

	// Get user email and name
	var userEmail, uploaderName string
	if err := h.db.Session().Query(`SELECT email, name FROM users WHERE org_id = ? AND user_id = ?`, orgUUID, userUUID).Scan(&userEmail, &uploaderName); err != nil || userEmail == "" {
		userEmail = userID
	}
	if uploaderName == "" {
		uploaderName = userEmail
	}

	libNameCache := map[string]string{}

	for iter.Scan(&token, &linkType, &libID, &filePath, &expiresAt,
		&viewCount, &uploadCount, &createdAt, &hasPassword) {
		// Only return upload links from this endpoint
		if linkType != "upload" {
			continue
		}

		if repoIDFilter != "" && libID != repoIDFilter {
			continue
		}
		if pathFilter != "" && filePath != pathFilter {
			continue
		}

		isExpired := false
		expireDate := ""
		if expiresAt != nil && !expiresAt.IsZero() {
			isExpired = expiresAt.Before(time.Now())
			expireDate = expiresAt.Format(time.RFC3339)
		}

		repoName, ok := libNameCache[libID]
		if !ok {
			libUUID, parseErr := gocql.ParseUUID(libID)
			if parseErr == nil {
				h.db.Session().Query(`SELECT name FROM libraries WHERE org_id = ? AND library_id = ?`, orgUUID, libUUID).Scan(&repoName)
			}
			if repoName == "" {
				repoName = "Unknown Library"
			}
			libNameCache[libID] = repoName
		}

		links = append(links, UploadLinkResponse{
			Token:       token,
			RepoID:      libID,
			RepoName:    repoName,
			Path:        filePath,
			ObjName:     objNameFromPath(filePath, repoName),
			IsExpired:   isExpired,
			ViewCount:   viewCount,
			UploadCount: uploadCount,
			CTime:       createdAt.Format(time.RFC3339),
			ExpireDate:  expireDate,
			UserEmail:   userEmail,
			CreatorName: uploaderName,
			LinkURL:     fmt.Sprintf("%s/u/d/%s", httputil.GetBrowserURL(c, h.serverURL), token),
			IsOwner:     true,
			HasPassword: hasPassword,
		})
	}

	if err := iter.Close(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to list upload links: %v", err)})
		return
	}

	if links == nil {
		links = []UploadLinkResponse{}
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
			links = []UploadLinkResponse{}
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

// UploadLinkCreateRequest represents the request for creating an upload link
type UploadLinkCreateRequest struct {
	RepoID         string `json:"repo_id" form:"repo_id"`
	Path           string `json:"path" form:"path"`
	Password       string `json:"password" form:"password"`
	ExpireDays     int    `json:"expire_days" form:"expire_days"`
	ExpirationTime string `json:"expiration_time" form:"expiration_time"`
}

// CreateUploadLink creates a new upload link
// Implements: POST /api/v2.1/upload-links/
func (h *UploadLinkHandler) CreateUploadLink(c *gin.Context) {
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	var req UploadLinkCreateRequest
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.RepoID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "repo_id is required"})
		return
	}

	if req.Path == "" {
		req.Path = "/"
	}

	// Validate repo exists
	if _, err := uuid.Parse(req.RepoID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid repo_id"})
		return
	}

	libraryState, err := readLiveLibraryStateFn(h.db.Session(), orgID, req.RepoID)
	if err != nil {
		writeLiveLibraryStateError(c, err)
		return
	}

	// PERMISSION CHECK: User must have write access to the library
	if h.permMiddleware != nil {
		hasAccess, err := h.permMiddleware.HasLibraryAccess(orgID, userID, req.RepoID, middleware.PermissionRW)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check permissions"})
			return
		}
		if !hasAccess {
			c.JSON(http.StatusForbidden, gin.H{"error": "you do not have write access to this library"})
			return
		}
	}

	// CUSTOM PERMISSION CHECK: upload flag
	if h.permMiddleware != nil && !h.permMiddleware.RequirePermFlagForRepo(c, req.RepoID, "upload") {
		c.JSON(http.StatusForbidden, gin.H{"error": "upload is not allowed by your permission"})
		return
	}

	// ENFORCEMENT CHECK: feature flag + numeric limit + expiry cap
	if h.config != nil {
		enforcement := GetOrgEnforcement(h.db, orgID, h.config)
		if !enforcement.Profile.Features.CanGenerateUploadLink {
			c.JSON(http.StatusForbidden, gin.H{
				"error":            "Upload link creation is not available on your plan",
				"upgrade_required": true,
			})
			return
		}
		if enforcement.Profile.Limits.MaxUploadLinks > 0 {
			count := CountUploadLinks(h.db, orgID)
			if count >= enforcement.Profile.Limits.MaxUploadLinks {
				c.JSON(http.StatusForbidden, gin.H{
					"error":   "Upload link limit reached",
					"limit":   enforcement.Profile.Limits.MaxUploadLinks,
					"current": count,
				})
				return
			}
		}
		if max := enforcement.Profile.Limits.UploadLinkExpireDaysMax; max > 0 {
			if req.ExpireDays > max {
				req.ExpireDays = max
			}
			if req.ExpireDays == 0 && req.ExpirationTime == "" {
				req.ExpireDays = max
			}
		}
	}

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

	// Insert into all 4 tables (permission is NULL for upload links)
	if err := h.shareHandler.insertShareLink(
		token, "upload", orgID, req.RepoID, req.Path, userID,
		"", passwordHash, expiresAt, false, now,
		0, 0, 0,
	); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create upload link"})
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

	expireDate := ""
	if expiresAt != nil {
		expireDate = expiresAt.Format(time.RFC3339)
	}

	c.JSON(http.StatusOK, UploadLinkResponse{
		Token:       token,
		RepoID:      req.RepoID,
		RepoName:    repoName,
		Path:        req.Path,
		ObjName:     objNameFromPath(req.Path, repoName),
		IsExpired:   false,
		ViewCount:   0,
		UploadCount: 0,
		CTime:       now.Format(time.RFC3339),
		ExpireDate:  expireDate,
		UserEmail:   userEmail,
		CreatorName: userName,
		LinkURL:     fmt.Sprintf("%s/u/d/%s", httputil.GetBrowserURL(c, h.serverURL), token),
		IsOwner:     true,
		Password:    req.Password,
		HasPassword: req.Password != "",
	})
}

// DeleteUploadLink deletes an upload link
// Implements: DELETE /api/v2.1/upload-links/:token/
func (h *UploadLinkHandler) DeleteUploadLink(c *gin.Context) {
	token := c.Param("token")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	// Read from primary table
	var createdBy, libID string
	var createdAt time.Time
	if err := h.db.Session().Query(`
		SELECT created_by, library_id, created_at FROM share_links WHERE link_token = ?
	`, token).Scan(&createdBy, &libID, &createdAt); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "upload link not found"})
		return
	}

	if createdBy != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "not authorized to delete this upload link"})
		return
	}

	if err := h.shareHandler.deleteShareLink(token, orgID, userID, libID, createdAt); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete upload link"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// UpdateUploadLink updates an upload link (expiration, password)
// Implements: PUT /api/v2.1/upload-links/:token/
func (h *UploadLinkHandler) UpdateUploadLink(c *gin.Context) {
	token := c.Param("token")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	// Read existing link
	var createdBy, libID, filePath, currentPasswordHash string
	var currentExpiresAt *time.Time
	var createdAt time.Time
	var viewCount, uploadCount int

	if err := h.db.Session().Query(`
		SELECT created_by, library_id, file_path, expires_at, created_at, password_hash, view_count, upload_count
		FROM share_links WHERE link_token = ?
	`, token).Scan(&createdBy, &libID, &filePath, &currentExpiresAt, &createdAt, &currentPasswordHash, &viewCount, &uploadCount); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "upload link not found"})
		return
	}

	if createdBy != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "not authorized"})
		return
	}

	if _, err := readLiveLibraryStateFn(h.db.Session(), orgID, libID); err != nil {
		writeLiveLibraryStateError(c, err)
		return
	}

	// Parse updates
	var updateBody struct {
		ExpirationTime string `json:"expiration_time"`
		Password       string `json:"password"`
	}
	if err := c.ShouldBindJSON(&updateBody); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	expirationTime := updateBody.ExpirationTime
	newPasswordPlain := updateBody.Password

	newExpiresAt := currentExpiresAt
	if expirationTime != "" {
		if t, err := time.Parse(time.RFC3339, expirationTime); err == nil {
			newExpiresAt = &t
		} else if t, err := time.Parse("2006-01-02", expirationTime); err == nil {
			newExpiresAt = &t
		}
	}

	newPasswordHash := currentPasswordHash
	if newPasswordPlain == "__remove__" {
		newPasswordHash = ""
	} else if newPasswordPlain != "" {
		hashed, err := bcrypt.GenerateFromPassword([]byte(newPasswordPlain), bcrypt.DefaultCost)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to hash password"})
			return
		}
		newPasswordHash = string(hashed)
	}

	// Re-insert (upsert) to handle TTL changes
	if err := h.shareHandler.deleteShareLink(token, orgID, userID, libID, createdAt); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update upload link"})
		return
	}
	if err := h.shareHandler.insertShareLink(
		token, "upload", orgID, libID, filePath, userID,
		"", newPasswordHash, newExpiresAt, false, createdAt,
		viewCount, 0, uploadCount,
	); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update upload link"})
		return
	}

	expireDate := ""
	if newExpiresAt != nil {
		expireDate = newExpiresAt.Format(time.RFC3339)
	}

	c.JSON(http.StatusOK, gin.H{
		"token":       token,
		"expire_date": expireDate,
	})
}

// ListRepoUploadLinks returns upload links for a specific repo
// Implements: GET /api/v2.1/repos/:repo_id/upload-links/
func (h *UploadLinkHandler) ListRepoUploadLinks(c *gin.Context) {
	repoID := c.Param("repo_id")
	c.Request.URL.RawQuery = fmt.Sprintf("repo_id=%s&%s", repoID, c.Request.URL.RawQuery)
	h.ListUploadLinks(c)
}
