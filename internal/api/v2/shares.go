package v2

import (
	"net/http"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// ShareHandler handles share-related API requests (simplified/legacy API)
type ShareHandler struct {
	db           *db.DB
	config       *config.Config
	shareHandler *ShareLinkHandler // reuse insert/delete helpers
}

// RegisterShareRoutes registers share routes
func RegisterShareRoutes(rg *gin.RouterGroup, database *db.DB, cfg *config.Config) {
	h := &ShareHandler{
		db:           database,
		config:       cfg,
		shareHandler: &ShareLinkHandler{db: database},
	}

	shares := rg.Group("/share-links")
	{
		shares.GET("", h.ListShareLinks)
		shares.POST("", h.CreateShareLink)
		shares.GET("/:token", h.GetShareLink)
		shares.DELETE("/:token", h.DeleteShareLink)
	}
}

// ListShareLinks returns all share links for the authenticated user
func (h *ShareHandler) ListShareLinks(c *gin.Context) {
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	orgUUID, _ := uuid.Parse(orgID)
	userUUID, _ := uuid.Parse(userID)

	iter := h.db.Session().Query(`
		SELECT link_token, link_type, library_id, file_path, permission, expires_at,
		       download_count, max_downloads, created_at
		FROM share_links_by_creator WHERE org_id = ? AND created_by = ?
	`, orgID, userID).Iter()

	var links []models.ShareLink
	var token, linkType, libID, filePath, permission string
	var expiresAt *time.Time
	var downloadCount int
	var maxDownloads *int
	var createdAt time.Time

	for iter.Scan(
		&token, &linkType, &libID, &filePath, &permission,
		&expiresAt, &downloadCount, &maxDownloads, &createdAt,
	) {
		// Only return share links
		if linkType != "share" {
			continue
		}
		libUUID, _ := uuid.Parse(libID)
		links = append(links, models.ShareLink{
			Token:         token,
			LinkType:      linkType,
			OrgID:         orgUUID,
			LibraryID:     libUUID,
			FilePath:      filePath,
			CreatedBy:     userUUID,
			Permission:    permission,
			ExpiresAt:     expiresAt,
			DownloadCount: downloadCount,
			MaxDownloads:  maxDownloads,
			CreatedAt:     createdAt,
		})
	}

	if err := iter.Close(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list share links"})
		return
	}

	if links == nil {
		links = []models.ShareLink{}
	}

	c.JSON(http.StatusOK, links)
}

// CreateShareLinkRequest represents the request for creating a share link
type CreateShareLinkRequest struct {
	RepoID       string `json:"repo_id" binding:"required"`
	Path         string `json:"path"`
	Permission   string `json:"permission"` // view, download, upload
	Password     string `json:"password,omitempty"`
	ExpireDays   int    `json:"expire_days,omitempty"`
	MaxDownloads *int   `json:"max_downloads,omitempty"`
}

// CreateShareLink creates a new share link
func (h *ShareHandler) CreateShareLink(c *gin.Context) {
	var req CreateShareLinkRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	orgUUID, _ := uuid.Parse(orgID)
	repoUUID, err := uuid.Parse(req.RepoID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid repo_id"})
		return
	}
	userUUID, _ := uuid.Parse(userID)

	if _, err := readLiveLibraryStateFn(h.db.Session(), orgID, req.RepoID); err != nil {
		writeLiveLibraryStateError(c, err)
		return
	}

	// Normalize permission to JSON
	permissionJSON := normalizePermissionInput(req.Permission)

	// Generate secure token
	token, err := generateSecureToken(16)
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
	if req.ExpireDays > 0 {
		exp := time.Now().AddDate(0, 0, req.ExpireDays)
		expiresAt = &exp
	}

	now := time.Now()

	// Insert into all 4 tables
	if err := h.shareHandler.insertShareLink(
		token, "share", orgID, req.RepoID, req.Path, userID,
		permissionJSON, passwordHash, expiresAt, false, now,
		0, 0, 0,
	); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create share link"})
		return
	}

	link := models.ShareLink{
		Token:        token,
		LinkType:     "share",
		OrgID:        orgUUID,
		LibraryID:    repoUUID,
		FilePath:     req.Path,
		CreatedBy:    userUUID,
		Permission:   permissionJSON,
		ExpiresAt:    expiresAt,
		MaxDownloads: req.MaxDownloads,
		Active:       true,
		CreatedAt:    now,
	}

	c.JSON(http.StatusCreated, link)
}

// GetShareLink returns a share link by token
func (h *ShareHandler) GetShareLink(c *gin.Context) {
	tokenParam := c.Param("token")

	var token, linkType, orgID, libID, filePath, createdBy, permission string
	var expiresAt *time.Time
	var downloadCount int
	var maxDownloads *int
	var createdAt time.Time
	var active bool

	if err := h.db.Session().Query(`
		SELECT link_token, link_type, org_id, library_id, file_path, created_by, permission,
		       expires_at, download_count, max_downloads, created_at, active
		FROM share_links WHERE link_token = ?
	`, tokenParam).Scan(
		&token, &linkType, &orgID, &libID, &filePath, &createdBy,
		&permission, &expiresAt, &downloadCount, &maxDownloads, &createdAt, &active,
	); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "share link not found"})
		return
	}

	// Check if disabled
	if !active {
		c.JSON(http.StatusGone, gin.H{"error": "share link has been disabled"})
		return
	}

	// Check if expired
	if expiresAt != nil && time.Now().After(*expiresAt) {
		c.JSON(http.StatusGone, gin.H{"error": "share link has expired"})
		return
	}

	// Check max downloads
	if maxDownloads != nil && downloadCount >= *maxDownloads {
		c.JSON(http.StatusGone, gin.H{"error": "share link has reached max downloads"})
		return
	}

	orgUUID, _ := uuid.Parse(orgID)
	libUUID, _ := uuid.Parse(libID)
	createdByUUID, _ := uuid.Parse(createdBy)

	link := models.ShareLink{
		Token:         token,
		LinkType:      linkType,
		OrgID:         orgUUID,
		LibraryID:     libUUID,
		FilePath:      filePath,
		CreatedBy:     createdByUUID,
		Permission:    permission,
		ExpiresAt:     expiresAt,
		Active:        active,
		DownloadCount: downloadCount,
		MaxDownloads:  maxDownloads,
		CreatedAt:     createdAt,
	}

	c.JSON(http.StatusOK, link)
}

// DeleteShareLink deletes a share link
func (h *ShareHandler) DeleteShareLink(c *gin.Context) {
	token := c.Param("token")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	// Read from primary table
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

	if err := h.shareHandler.deleteShareLink(token, orgID, userID, libID, createdAt); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete share link"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// generateSecureToken generates a URL-safe random token
func generateSecureToken(length int) (string, error) {
	return generateSecureShareToken(length)
}
