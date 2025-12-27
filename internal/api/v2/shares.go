package v2

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

// ShareHandler handles share-related API requests
type ShareHandler struct {
	db     *db.DB
	config *config.Config
}

// RegisterShareRoutes registers share routes
func RegisterShareRoutes(rg *gin.RouterGroup, database *db.DB, cfg *config.Config) {
	h := &ShareHandler{db: database, config: cfg}

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

	// Query share links created by this user (use strings for UUID binding)
	iter := h.db.Session().Query(`
		SELECT share_token, library_id, file_path, permission, expires_at, download_count, max_downloads, created_at
		FROM share_links WHERE org_id = ? AND created_by = ? ALLOW FILTERING
	`, orgID, userID).Iter()

	var links []models.ShareLink
	var token, libID, filePath, permission string
	var expiresAt *time.Time
	var downloadCount int
	var maxDownloads *int
	var createdAt time.Time

	for iter.Scan(
		&token, &libID, &filePath, &permission,
		&expiresAt, &downloadCount, &maxDownloads, &createdAt,
	) {
		libUUID, _ := uuid.Parse(libID)
		links = append(links, models.ShareLink{
			Token:         token,
			OrgID:         orgUUID,
			LibraryID:     libUUID,
			Path:          filePath,
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
	userUUID, _ := uuid.Parse(userID)
	repoUUID, err := uuid.Parse(req.RepoID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid repo_id"})
		return
	}

	// Default permission
	if req.Permission == "" {
		req.Permission = "download"
	}

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
	link := models.ShareLink{
		Token:        token,
		OrgID:        orgUUID,
		LibraryID:    repoUUID,
		Path:         req.Path,
		CreatedBy:    userUUID,
		Permission:   req.Permission,
		PasswordHash: passwordHash,
		ExpiresAt:    expiresAt,
		MaxDownloads: req.MaxDownloads,
		CreatedAt:    now,
	}

	// Insert into database (use strings for UUIDs)
	if err := h.db.Session().Query(`
		INSERT INTO share_links (
			share_token, org_id, library_id, file_path, created_by, permission,
			password_hash, expires_at, download_count, max_downloads, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, link.Token, orgID, req.RepoID, link.Path, userID,
		link.Permission, link.PasswordHash, link.ExpiresAt, 0, link.MaxDownloads, link.CreatedAt,
	).Exec(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create share link"})
		return
	}

	// Don't return password hash
	link.PasswordHash = ""
	c.JSON(http.StatusCreated, link)
}

// GetShareLink returns a share link by token
func (h *ShareHandler) GetShareLink(c *gin.Context) {
	tokenParam := c.Param("token")

	var token, orgID, libID, filePath, createdBy, permission string
	var expiresAt *time.Time
	var downloadCount int
	var maxDownloads *int
	var createdAt time.Time

	if err := h.db.Session().Query(`
		SELECT share_token, org_id, library_id, file_path, created_by, permission,
			   expires_at, download_count, max_downloads, created_at
		FROM share_links WHERE share_token = ?
	`, tokenParam).Scan(
		&token, &orgID, &libID, &filePath, &createdBy,
		&permission, &expiresAt, &downloadCount, &maxDownloads, &createdAt,
	); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "share link not found"})
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
		OrgID:         orgUUID,
		LibraryID:     libUUID,
		Path:          filePath,
		CreatedBy:     createdByUUID,
		Permission:    permission,
		ExpiresAt:     expiresAt,
		DownloadCount: downloadCount,
		MaxDownloads:  maxDownloads,
		CreatedAt:     createdAt,
	}

	c.JSON(http.StatusOK, link)
}

// DeleteShareLink deletes a share link
func (h *ShareHandler) DeleteShareLink(c *gin.Context) {
	token := c.Param("token")
	userID := c.GetString("user_id")

	// Verify ownership
	var createdBy string
	if err := h.db.Session().Query(`
		SELECT created_by FROM share_links WHERE share_token = ?
	`, token).Scan(&createdBy); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "share link not found"})
		return
	}

	if createdBy != userID {
		c.JSON(http.StatusForbidden, gin.H{"error": "not authorized to delete this share link"})
		return
	}

	if err := h.db.Session().Query(`
		DELETE FROM share_links WHERE share_token = ?
	`, token).Exec(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete share link"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// generateSecureToken generates a URL-safe random token
func generateSecureToken(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	// Base64 encodes to ~4/3 the original length, return without padding
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}
