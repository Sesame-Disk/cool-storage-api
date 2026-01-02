package v2

import (
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/gin-gonic/gin"
)

// StarredHandler handles starred files API requests
type StarredHandler struct {
	db *db.DB
}

// NewStarredHandler creates a new StarredHandler
func NewStarredHandler(database *db.DB) *StarredHandler {
	return &StarredHandler{db: database}
}

// StarredFile represents a starred file in API response format
// Format matches Seafile's /api/v2.1/starred-items/ response
type StarredFile struct {
	RepoID           string `json:"repo_id"`
	RepoName         string `json:"repo_name"`
	RepoEncrypted    bool   `json:"repo_encrypted"`
	IsDir            bool   `json:"is_dir"`
	Path             string `json:"path"`
	ObjName          string `json:"obj_name"`
	Mtime            string `json:"mtime"` // ISO 8601 format
	Deleted          bool   `json:"deleted"`
	UserEmail        string `json:"user_email"`
	UserName         string `json:"user_name"`
	UserContactEmail string `json:"user_contact_email"`
}

// StarredItemsResponse wraps the starred items list
type StarredItemsResponse struct {
	StarredItemList []StarredFile `json:"starred_item_list"`
}

// RegisterStarredRoutes registers starred files routes for /api2/starredfiles
func RegisterStarredRoutes(rg *gin.RouterGroup, database *db.DB) *StarredHandler {
	h := NewStarredHandler(database)

	starred := rg.Group("/starredfiles")
	{
		starred.GET("", h.ListStarredFiles)
		starred.GET("/", h.ListStarredFiles)
		starred.POST("", h.StarFile)
		starred.POST("/", h.StarFile)
		starred.DELETE("", h.UnstarFile)
		starred.DELETE("/", h.UnstarFile)
	}

	return h
}

// RegisterV21StarredRoutes registers starred files routes for /api/v2.1/starred-items
// The v2.1 API uses "starred-items" instead of "starredfiles"
func RegisterV21StarredRoutes(rg *gin.RouterGroup, database *db.DB) *StarredHandler {
	h := NewStarredHandler(database)

	starred := rg.Group("/starred-items")
	{
		starred.GET("", h.ListStarredFiles)
		starred.GET("/", h.ListStarredFiles)
		starred.POST("", h.StarFile)
		starred.POST("/", h.StarFile)
		starred.DELETE("", h.UnstarFile)
		starred.DELETE("/", h.UnstarFile)
	}

	return h
}

// ListStarredFiles returns all starred files for the current user
// Implements: GET /api2/starredfiles/
func (h *StarredHandler) ListStarredFiles(c *gin.Context) {
	userID := c.GetString("user_id")
	orgID := c.GetString("org_id")

	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	// Query starred files
	iter := h.db.Session().Query(`
		SELECT repo_id, path, starred_at FROM starred_files WHERE user_id = ?
	`, userID).Iter()

	var starredFiles []StarredFile
	var repoID, filePath string
	var starredAt time.Time

	for iter.Scan(&repoID, &filePath, &starredAt) {
		// Get library info
		var libName string
		var encrypted bool
		err := h.db.Session().Query(`
			SELECT name, encrypted FROM libraries WHERE org_id = ? AND library_id = ?
		`, orgID, repoID).Scan(&libName, &encrypted)
		if err != nil {
			// Library may have been deleted, skip
			continue
		}

		// Get file info from fs_objects if possible
		// For now, return basic info
		fileName := path.Base(filePath)
		isDir := strings.HasSuffix(filePath, "/")

		// Try to get file info
		var mtime int64
		fsHelper := NewFSHelper(h.db)
		result, err := fsHelper.TraverseToPath(repoID, filePath)
		if err == nil && result.TargetEntry != nil {
			mtime = result.TargetEntry.MTime
			isDir = result.TargetEntry.Mode == ModeDir || result.TargetEntry.Mode&0170000 == 040000
		} else {
			mtime = starredAt.Unix()
		}

		userEmail := userID + "@sesamefs.local"
		userName := strings.Split(userID, "@")[0]
		if userName == userID {
			userName = strings.Split(userEmail, "@")[0]
		}

		// Format mtime as ISO 8601
		var mtimeStr string
		if mtime > 0 {
			mtimeStr = time.Unix(mtime, 0).UTC().Format("2006-01-02T15:04:05+00:00")
		} else {
			mtimeStr = starredAt.UTC().Format("2006-01-02T15:04:05+00:00")
		}

		starredFiles = append(starredFiles, StarredFile{
			RepoID:           repoID,
			RepoName:         libName,
			RepoEncrypted:    encrypted,
			IsDir:            isDir,
			Path:             filePath,
			ObjName:          fileName,
			Mtime:            mtimeStr,
			Deleted:          false,
			UserEmail:        userEmail,
			UserName:         userName,
			UserContactEmail: userEmail,
		})
	}

	if err := iter.Close(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query starred files"})
		return
	}

	if starredFiles == nil {
		starredFiles = []StarredFile{}
	}

	// Return in Seafile v2.1 format
	c.JSON(http.StatusOK, StarredItemsResponse{StarredItemList: starredFiles})
}

// StarFileRequest represents the request body for starring a file
type StarFileRequest struct {
	RepoID string `json:"repo_id" form:"repo_id"`
	Path   string `json:"path" form:"path"` // v2.1 uses "path", v2 uses "p"
	PathV2 string `json:"p" form:"p"`       // fallback for v2 API
}

// StarFile stars a file
// Implements: POST /api/v2.1/starred-items/
func (h *StarredHandler) StarFile(c *gin.Context) {
	userID := c.GetString("user_id")
	orgID := c.GetString("org_id")

	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	var req StarFileRequest
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.RepoID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "repo_id is required"})
		return
	}

	// Use Path (v2.1) or fallback to PathV2 (v2 "p" parameter)
	reqPath := req.Path
	if reqPath == "" {
		reqPath = req.PathV2
	}

	if reqPath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is required"})
		return
	}

	// Normalize path
	filePath := reqPath
	if !strings.HasPrefix(filePath, "/") {
		filePath = "/" + filePath
	}

	// Get library info
	var libName string
	var encrypted bool
	err := h.db.Session().Query(`
		SELECT name, encrypted FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, req.RepoID).Scan(&libName, &encrypted)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}

	// Insert starred file
	now := time.Now()
	err = h.db.Session().Query(`
		INSERT INTO starred_files (user_id, repo_id, path, starred_at)
		VALUES (?, ?, ?, ?)
	`, userID, req.RepoID, filePath, now).Exec()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to star file"})
		return
	}

	// Get file/dir info
	fileName := path.Base(filePath)
	isDir := filePath == "/" || strings.HasSuffix(filePath, "/")
	var mtime int64

	fsHelper := NewFSHelper(h.db)
	result, err := fsHelper.TraverseToPath(req.RepoID, filePath)
	if err == nil && result.TargetEntry != nil {
		mtime = result.TargetEntry.MTime
		isDir = result.TargetEntry.Mode == ModeDir || result.TargetEntry.Mode&0170000 == 040000
		fileName = result.TargetEntry.Name
	} else {
		mtime = now.Unix()
	}

	// For root directory, use library name
	if filePath == "/" {
		fileName = libName
		isDir = true
	}

	userEmail := userID + "@sesamefs.local"
	userName := strings.Split(userID, "@")[0]
	if userName == userID {
		userName = strings.Split(userEmail, "@")[0]
	}

	// Return the starred item (Seafile format)
	c.JSON(http.StatusOK, StarredFile{
		RepoID:           req.RepoID,
		RepoName:         libName,
		RepoEncrypted:    encrypted,
		IsDir:            isDir,
		Path:             filePath,
		ObjName:          fileName,
		Mtime:            time.Unix(mtime, 0).UTC().Format("2006-01-02T15:04:05+00:00"),
		Deleted:          false,
		UserEmail:        userEmail,
		UserName:         userName,
		UserContactEmail: userEmail,
	})
}

// UnstarFile unstars a file
// Implements: DELETE /api2/starredfiles/?repo_id=xxx&p=/path
// Also supports: DELETE /api/v2.1/starred-items/?repo_id=xxx&path=/path
func (h *StarredHandler) UnstarFile(c *gin.Context) {
	userID := c.GetString("user_id")

	if userID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "not authenticated"})
		return
	}

	repoID := c.Query("repo_id")
	// v2.1 uses "path", v2 uses "p"
	filePath := c.Query("path")
	if filePath == "" {
		filePath = c.Query("p")
	}

	if repoID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "repo_id is required"})
		return
	}

	if filePath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is required"})
		return
	}

	// Normalize path
	if !strings.HasPrefix(filePath, "/") {
		filePath = "/" + filePath
	}

	// Delete starred file
	err := h.db.Session().Query(`
		DELETE FROM starred_files WHERE user_id = ? AND repo_id = ? AND path = ?
	`, userID, repoID, filePath).Exec()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to unstar file"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// IsFileStarred checks if a file is starred by the user
func (h *StarredHandler) IsFileStarred(userID, repoID, filePath string) bool {
	var starredAt time.Time
	err := h.db.Session().Query(`
		SELECT starred_at FROM starred_files WHERE user_id = ? AND repo_id = ? AND path = ?
	`, userID, repoID, filePath).Scan(&starredAt)
	return err == nil
}
