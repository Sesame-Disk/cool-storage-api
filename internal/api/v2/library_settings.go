package v2

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"net/http"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// LibrarySettingsHandler handles library settings endpoints
type LibrarySettingsHandler struct {
	db             *db.DB
	config         *config.Config
	permMiddleware *middleware.PermissionMiddleware
}

// NewLibrarySettingsHandler creates a new LibrarySettingsHandler
func NewLibrarySettingsHandler(database *db.DB, cfg *config.Config) *LibrarySettingsHandler {
	return &LibrarySettingsHandler{
		db:             database,
		config:         cfg,
		permMiddleware: middleware.NewPermissionMiddleware(database),
	}
}

// RegisterHistoryLimitRoutes registers history limit routes (for api2 group)
func RegisterHistoryLimitRoutes(rg *gin.RouterGroup, database *db.DB, cfg *config.Config) {
	h := NewLibrarySettingsHandler(database, cfg)

	repos := rg.Group("/repos")
	{
		repos.GET("/:repo_id/history-limit", h.GetHistoryLimit)
		repos.GET("/:repo_id/history-limit/", h.GetHistoryLimit)
		repos.PUT("/:repo_id/history-limit", h.SetHistoryLimit)
		repos.PUT("/:repo_id/history-limit/", h.SetHistoryLimit)
	}
}

// RegisterLibraryTransferRoutes registers the library transfer route (for api2 group)
func RegisterLibraryTransferRoutes(rg *gin.RouterGroup, database *db.DB, cfg *config.Config) {
	h := NewLibrarySettingsHandler(database, cfg)

	repos := rg.Group("/repos")
	{
		repos.PUT("/:repo_id/owner", h.TransferLibrary)
		repos.PUT("/:repo_id/owner/", h.TransferLibrary)
	}
}

// RegisterV21LibrarySettingsRoutes registers auto-delete and API token routes (for v2.1 group)
func RegisterV21LibrarySettingsRoutes(rg *gin.RouterGroup, database *db.DB, cfg *config.Config) {
	h := NewLibrarySettingsHandler(database, cfg)

	repos := rg.Group("/repos")
	{
		// Auto-delete (GET/PUT)
		repos.GET("/:repo_id/auto-delete", h.GetAutoDelete)
		repos.GET("/:repo_id/auto-delete/", h.GetAutoDelete)
		repos.PUT("/:repo_id/auto-delete", h.SetAutoDelete)
		repos.PUT("/:repo_id/auto-delete/", h.SetAutoDelete)

		// API tokens (GET/POST/DELETE/PUT)
		repos.GET("/:repo_id/repo-api-tokens", h.ListAPITokens)
		repos.GET("/:repo_id/repo-api-tokens/", h.ListAPITokens)
		repos.POST("/:repo_id/repo-api-tokens", h.CreateAPIToken)
		repos.POST("/:repo_id/repo-api-tokens/", h.CreateAPIToken)
		repos.DELETE("/:repo_id/repo-api-tokens/:app_name", h.DeleteAPIToken)
		repos.DELETE("/:repo_id/repo-api-tokens/:app_name/", h.DeleteAPIToken)
		repos.PUT("/:repo_id/repo-api-tokens/:app_name", h.UpdateAPIToken)
		repos.PUT("/:repo_id/repo-api-tokens/:app_name/", h.UpdateAPIToken)
	}
}

// requireOwner checks if the current user owns the library. Returns orgID, userID, repoID on success.
func (h *LibrarySettingsHandler) requireOwner(c *gin.Context) (string, string, string, bool) {
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")
	repoID := c.Param("repo_id")

	if orgID == "" || userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return "", "", "", false
	}

	if _, err := uuid.Parse(repoID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid repo_id"})
		return "", "", "", false
	}

	isOwner, err := h.permMiddleware.IsLibraryOwner(orgID, userID, repoID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check ownership"})
		return "", "", "", false
	}

	if !isOwner {
		c.JSON(http.StatusForbidden, gin.H{"error": "only library owner can perform this action"})
		return "", "", "", false
	}

	return orgID, userID, repoID, true
}

// ============================================================================
// History Limit
// ============================================================================

// GetHistoryLimit returns the history limit for a library
// GET /api/v2.1/repos/:repo_id/history-limit/
func (h *LibrarySettingsHandler) GetHistoryLimit(c *gin.Context) {
	orgID := c.GetString("org_id")
	repoID := c.Param("repo_id")

	if orgID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	// Read version_ttl_days from the libraries table
	var versionTTLDays int
	err := h.db.Session().Query(`
		SELECT version_ttl_days FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(&versionTTLDays)

	if err != nil {
		// Library not found or no value set - return default (keep all)
		c.JSON(http.StatusOK, gin.H{"keep_days": -1})
		return
	}

	// If version_ttl_days is 0 (Cassandra default for unset int), treat as -1 (keep all)
	keepDays := versionTTLDays
	if keepDays == 0 {
		keepDays = -1
	}

	c.JSON(http.StatusOK, gin.H{"keep_days": keepDays})
}

// SetHistoryLimit sets the history limit for a library
// PUT /api/v2.1/repos/:repo_id/history-limit/
func (h *LibrarySettingsHandler) SetHistoryLimit(c *gin.Context) {
	orgID, _, repoID, ok := h.requireOwner(c)
	if !ok {
		return
	}

	var req struct {
		KeepDays int `json:"keep_days" form:"keep_days"`
	}
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	// Validate: -1 = keep all, 0 = keep none, >0 = keep N days
	if req.KeepDays < -1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "keep_days must be -1 (all), 0 (none), or a positive integer"})
		return
	}

	// Store in version_ttl_days column
	if err := h.db.Session().Query(`
		UPDATE libraries SET version_ttl_days = ?, updated_at = ?
		WHERE org_id = ? AND library_id = ?
	`, req.KeepDays, time.Now(), orgID, repoID).Exec(); err != nil {
		log.Printf("[SetHistoryLimit] Failed to update: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update history limit"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"keep_days": req.KeepDays})
}

// ============================================================================
// Auto-Delete
// ============================================================================

// GetAutoDelete returns the auto-delete settings for a library
// GET /api/v2.1/repos/:repo_id/auto-delete/
func (h *LibrarySettingsHandler) GetAutoDelete(c *gin.Context) {
	orgID := c.GetString("org_id")
	repoID := c.Param("repo_id")

	if orgID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}

	var autoDeleteDays int
	err := h.db.Session().Query(`
		SELECT auto_delete_days FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(&autoDeleteDays)

	if err != nil {
		c.JSON(http.StatusOK, gin.H{"auto_delete_days": 0})
		return
	}

	c.JSON(http.StatusOK, gin.H{"auto_delete_days": autoDeleteDays})
}

// SetAutoDelete sets the auto-delete settings for a library
// PUT /api/v2.1/repos/:repo_id/auto-delete/
func (h *LibrarySettingsHandler) SetAutoDelete(c *gin.Context) {
	orgID, _, repoID, ok := h.requireOwner(c)
	if !ok {
		return
	}

	var req struct {
		AutoDeleteDays int `json:"auto_delete_days" form:"auto_delete_days"`
	}
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	if req.AutoDeleteDays < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "auto_delete_days must be 0 (disabled) or a positive integer"})
		return
	}

	if err := h.db.Session().Query(`
		UPDATE libraries SET auto_delete_days = ?, updated_at = ?
		WHERE org_id = ? AND library_id = ?
	`, req.AutoDeleteDays, time.Now(), orgID, repoID).Exec(); err != nil {
		log.Printf("[SetAutoDelete] Failed to update: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update auto-delete settings"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"auto_delete_days": req.AutoDeleteDays})
}

// ============================================================================
// Repo API Tokens
// ============================================================================

// APITokenResponse represents an API token in responses
type APITokenResponse struct {
	AppName     string `json:"app_name"`
	APIToken    string `json:"api_token"`
	Permission  string `json:"permission"`
	GeneratedAt string `json:"generated_at"`
}

// ListAPITokens returns all API tokens for a library
// GET /api/v2.1/repos/:repo_id/repo-api-tokens/
func (h *LibrarySettingsHandler) ListAPITokens(c *gin.Context) {
	_, _, repoID, ok := h.requireOwner(c)
	if !ok {
		return
	}

	iter := h.db.Session().Query(`
		SELECT app_name, api_token, permission, created_at
		FROM repo_api_tokens WHERE repo_id = ?
	`, repoID).Iter()

	var tokens []APITokenResponse
	var appName, apiToken, permission string
	var createdAt time.Time

	for iter.Scan(&appName, &apiToken, &permission, &createdAt) {
		tokens = append(tokens, APITokenResponse{
			AppName:     appName,
			APIToken:    apiToken,
			Permission:  permission,
			GeneratedAt: createdAt.Format(time.RFC3339),
		})
	}

	if err := iter.Close(); err != nil {
		log.Printf("[ListAPITokens] Failed to list tokens: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list API tokens"})
		return
	}

	if tokens == nil {
		tokens = []APITokenResponse{}
	}

	c.JSON(http.StatusOK, gin.H{"repo_api_tokens": tokens})
}

// CreateAPIToken creates a new API token for a library
// POST /api/v2.1/repos/:repo_id/repo-api-tokens/
func (h *LibrarySettingsHandler) CreateAPIToken(c *gin.Context) {
	_, userID, repoID, ok := h.requireOwner(c)
	if !ok {
		return
	}

	var req struct {
		AppName    string `json:"app_name" form:"app_name"`
		Permission string `json:"permission" form:"permission"`
	}
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	if req.AppName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "app_name is required"})
		return
	}

	if req.Permission != "r" && req.Permission != "rw" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "permission must be 'r' or 'rw'"})
		return
	}

	// Generate a random API token (32 bytes = 64 hex chars)
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate token"})
		return
	}
	apiToken := hex.EncodeToString(tokenBytes)

	now := time.Now()

	// Use LWT to prevent concurrent duplicate app_name creation.
	applied, err := h.db.Session().Query(`
		INSERT INTO repo_api_tokens (repo_id, app_name, api_token, permission, generated_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?) IF NOT EXISTS
	`, repoID, req.AppName, apiToken, req.Permission, userID, now).MapScanCAS(map[string]interface{}{})
	if err != nil {
		log.Printf("[CreateAPIToken] Failed to create token (LWT): %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create API token"})
		return
	}
	if !applied {
		c.JSON(http.StatusConflict, gin.H{"error": "API token with this app name already exists"})
		return
	}

	if err := h.db.Session().Query(`
		INSERT INTO repo_api_tokens_by_token (api_token, repo_id, app_name, permission, generated_by)
		VALUES (?, ?, ?, ?, ?)
	`, apiToken, repoID, req.AppName, req.Permission, userID).Exec(); err != nil {
		_ = h.db.Session().Query(`
			DELETE FROM repo_api_tokens WHERE repo_id = ? AND app_name = ?
		`, repoID, req.AppName).Exec()
		log.Printf("[CreateAPIToken] Failed to create reverse lookup token: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create API token"})
		return
	}

	c.JSON(http.StatusOK, APITokenResponse{
		AppName:     req.AppName,
		APIToken:    apiToken,
		Permission:  req.Permission,
		GeneratedAt: now.Format(time.RFC3339),
	})
}

// DeleteAPIToken deletes an API token for a library
// DELETE /api/v2.1/repos/:repo_id/repo-api-tokens/:app_name/
func (h *LibrarySettingsHandler) DeleteAPIToken(c *gin.Context) {
	_, _, repoID, ok := h.requireOwner(c)
	if !ok {
		return
	}

	appName := c.Param("app_name")
	if appName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "app_name is required"})
		return
	}

	// Read the token value first so we can clean up the reverse-lookup table
	var apiToken string
	_ = h.db.Session().Query(`
		SELECT api_token FROM repo_api_tokens WHERE repo_id = ? AND app_name = ?
	`, repoID, appName).Scan(&apiToken)

	if err := deleteRepoAPIToken(h.db, repoID, appName, apiToken); err != nil {
		log.Printf("[DeleteAPIToken] Failed to delete token: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete API token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// UpdateAPIToken updates the permission of an API token
// PUT /api/v2.1/repos/:repo_id/repo-api-tokens/:app_name/
func (h *LibrarySettingsHandler) UpdateAPIToken(c *gin.Context) {
	_, _, repoID, ok := h.requireOwner(c)
	if !ok {
		return
	}

	appName := c.Param("app_name")
	if appName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "app_name is required"})
		return
	}

	var req struct {
		Permission string `json:"permission" form:"permission"`
	}
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	if req.Permission != "r" && req.Permission != "rw" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "permission must be 'r' or 'rw'"})
		return
	}

	// Read the token value to update the reverse-lookup table
	var apiToken string
	_ = h.db.Session().Query(`
		SELECT api_token FROM repo_api_tokens WHERE repo_id = ? AND app_name = ?
	`, repoID, appName).Scan(&apiToken)

	if err := updateRepoAPITokenPermission(h.db, repoID, appName, apiToken, req.Permission); err != nil {
		log.Printf("[UpdateAPIToken] Failed to update token: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update API token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// ============================================================================
// Library Transfer
// ============================================================================

// TransferLibrary transfers a library to a new owner
// PUT /api2/repos/:repo_id/owner/
func (h *LibrarySettingsHandler) TransferLibrary(c *gin.Context) {
	orgID, _, repoID, ok := h.requireOwner(c)
	if !ok {
		return
	}

	var req struct {
		Owner string `json:"owner" form:"owner"`
	}
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	if req.Owner == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "owner email is required"})
		return
	}

	// Look up new owner by email
	var newOwnerID, newOwnerOrgID string
	err := h.db.Session().Query(`
		SELECT user_id, org_id FROM users_by_email WHERE email = ?
	`, req.Owner).Scan(&newOwnerID, &newOwnerOrgID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error_msg": "user not found"})
		return
	}

	// Verify new owner is in the same org
	if newOwnerOrgID != orgID {
		c.JSON(http.StatusBadRequest, gin.H{"error_msg": "user is not in the same organization"})
		return
	}

	now := time.Now()

	if err := updateLibraryOwner(h.db, orgID, repoID, newOwnerID, now); err != nil {
		log.Printf("[TransferLibrary] Failed to transfer owner: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error_msg": "failed to transfer library"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}
