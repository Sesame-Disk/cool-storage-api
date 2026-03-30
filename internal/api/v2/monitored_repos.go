package v2

import (
	"net/http"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// MonitoredRepoHandler handles watched/monitored repository operations
type MonitoredRepoHandler struct {
	db *db.DB
}

// NewMonitoredRepoHandler creates a new MonitoredRepoHandler
func NewMonitoredRepoHandler(database *db.DB) *MonitoredRepoHandler {
	return &MonitoredRepoHandler{db: database}
}

// RegisterMonitoredRepoRoutes registers monitored repo routes
func RegisterMonitoredRepoRoutes(rg *gin.RouterGroup, database *db.DB) {
	h := NewMonitoredRepoHandler(database)
	monitored := rg.Group("/monitored-repos")
	{
		monitored.POST("", h.MonitorRepo)
		monitored.POST("/", h.MonitorRepo)
		monitored.DELETE("/:repo_id", h.UnmonitorRepo)
		monitored.DELETE("/:repo_id/", h.UnmonitorRepo)
	}
}

// MonitorRepoRequest represents the request body for monitoring a repo
type MonitorRepoRequest struct {
	RepoID string `json:"repo_id" form:"repo_id"`
}

// MonitorRepo adds a library to the user's monitored list
// POST /api/v2.1/monitored-repos/
func (h *MonitoredRepoHandler) MonitorRepo(c *gin.Context) {
	userID := c.GetString("user_id")
	orgID := c.GetString("org_id")

	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	var req MonitorRepoRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.RepoID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "repo_id is required"})
		return
	}

	if _, err := uuid.Parse(req.RepoID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid repo_id"})
		return
	}

	// Verify library exists
	var libName string
	err := h.db.Session().Query(`
		SELECT name FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, req.RepoID).Scan(&libName)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}

	// Insert monitored repo
	now := time.Now()
	err = h.db.Session().Query(`
		INSERT INTO monitored_repos (user_id, repo_id, monitored_at)
		VALUES (?, ?, ?)
	`, userID, req.RepoID, now).Exec()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to monitor repo"})
		return
	}

	// Look up user email for the response
	var email, name string
	_ = h.db.Session().Query(`
		SELECT email, name FROM users WHERE org_id = ? AND user_id = ?
	`, orgID, userID).Scan(&email, &name)
	if email == "" {
		email = userID + "@sesamefs.local"
	}
	if name == "" {
		name = email
	}

	c.JSON(http.StatusOK, gin.H{
		"user_email":         email,
		"user_name":          name,
		"user_contact_email": email,
		"repo_id":            req.RepoID,
	})
}

// UnmonitorRepo removes a library from the user's monitored list
// DELETE /api/v2.1/monitored-repos/:repo_id/
func (h *MonitoredRepoHandler) UnmonitorRepo(c *gin.Context) {
	userID := c.GetString("user_id")
	repoID := c.Param("repo_id")

	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	if _, err := uuid.Parse(repoID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid repo_id"})
		return
	}

	err := h.db.Session().Query(`
		DELETE FROM monitored_repos WHERE user_id = ? AND repo_id = ?
	`, userID, repoID).Exec()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to unmonitor repo"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}
