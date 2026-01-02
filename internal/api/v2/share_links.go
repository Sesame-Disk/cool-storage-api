package v2

import (
	"net/http"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/gin-gonic/gin"
)

// ShareLinkHandler handles share link API requests
type ShareLinkHandler struct {
	db *db.DB
}

// NewShareLinkHandler creates a new ShareLinkHandler
func NewShareLinkHandler(database *db.DB) *ShareLinkHandler {
	return &ShareLinkHandler{db: database}
}

// ShareLink represents a share link in API response
type ShareLink struct {
	Token         string `json:"token"`
	RepoID        string `json:"repo_id"`
	RepoName      string `json:"repo_name"`
	Path          string `json:"path"`
	IsDir         bool   `json:"is_dir"`
	IsExpired     bool   `json:"is_expired"`
	ObjID         string `json:"obj_id,omitempty"`
	ObjName       string `json:"obj_name"`
	ViewCount     int    `json:"view_cnt"`
	CTime         string `json:"ctime"`
	ExpireDate    string `json:"expire_date,omitempty"`
	CanEdit       bool   `json:"can_edit"`
	CanDownload   bool   `json:"can_download"`
	Permissions   Perms  `json:"permissions"`
	UserEmail     string `json:"username"`
	LinkURL       string `json:"link,omitempty"`
	IsOwner       bool   `json:"is_owner"`
}

// Perms represents permission settings for share links
type Perms struct {
	CanEdit     bool `json:"can_edit"`
	CanDownload bool `json:"can_download"`
	CanUpload   bool `json:"can_upload"`
}

// RegisterShareLinkRoutes registers share link routes
func RegisterShareLinkRoutes(rg *gin.RouterGroup, database *db.DB) *ShareLinkHandler {
	h := NewShareLinkHandler(database)

	shareLinks := rg.Group("/share-links")
	{
		shareLinks.GET("", h.ListShareLinks)
		shareLinks.GET("/", h.ListShareLinks)
		shareLinks.POST("", h.CreateShareLink)
		shareLinks.POST("/", h.CreateShareLink)
		shareLinks.DELETE("/:token", h.DeleteShareLink)
		shareLinks.DELETE("/:token/", h.DeleteShareLink)
	}

	return h
}

// ListShareLinks returns share links for a file or all share links
// Implements: GET /api/v2.1/share-links/?repo_id=xxx&path=/xxx
func (h *ShareLinkHandler) ListShareLinks(c *gin.Context) {
	// For now, return empty list - full implementation would query database
	c.JSON(http.StatusOK, []ShareLink{})
}

// ShareLinkCreateRequest represents the request for creating a share link
type ShareLinkCreateRequest struct {
	RepoID      string `json:"repo_id" form:"repo_id"`
	Path        string `json:"path" form:"path"`
	Password    string `json:"password" form:"password"`
	ExpireDays  int    `json:"expire_days" form:"expire_days"`
	Permissions string `json:"permissions" form:"permissions"` // "preview_download", "preview_only", etc.
}

// CreateShareLink creates a new share link
// Implements: POST /api/v2.1/share-links/
func (h *ShareLinkHandler) CreateShareLink(c *gin.Context) {
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

	// For now, return a stub response - full implementation would create and store link
	// Generate a fake token for demonstration
	token := "stub-token-" + req.RepoID[:8]

	c.JSON(http.StatusOK, ShareLink{
		Token:       token,
		RepoID:      req.RepoID,
		Path:        req.Path,
		IsDir:       false,
		IsExpired:   false,
		ObjName:     req.Path,
		ViewCount:   0,
		CTime:       "2024-01-01T00:00:00+00:00",
		CanEdit:     false,
		CanDownload: true,
		Permissions: Perms{
			CanEdit:     false,
			CanDownload: true,
			CanUpload:   false,
		},
		UserEmail: userID + "@sesamefs.local",
		LinkURL:   "http://localhost:3000/d/" + token,
		IsOwner:   true,
	})
}

// DeleteShareLink deletes a share link
// Implements: DELETE /api/v2.1/share-links/:token/
func (h *ShareLinkHandler) DeleteShareLink(c *gin.Context) {
	// token := c.Param("token")
	// For now, return success - full implementation would delete from database
	c.JSON(http.StatusOK, gin.H{"success": true})
}
