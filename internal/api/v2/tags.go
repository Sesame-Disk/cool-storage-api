package v2

import (
	"net/http"
	"strconv"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
)

// TagHandler handles repository tag operations
type TagHandler struct {
	db *db.DB
}

// NewTagHandler creates a new TagHandler
func NewTagHandler(database *db.DB) *TagHandler {
	return &TagHandler{db: database}
}

// RegisterTagRoutes registers tag-related routes
func RegisterTagRoutes(router *gin.RouterGroup, database *db.DB) {
	h := NewTagHandler(database)

	// Repository tags
	router.GET("/repos/:repo_id/repo-tags", h.ListRepoTags)
	router.GET("/repos/:repo_id/repo-tags/", h.ListRepoTags)
	router.POST("/repos/:repo_id/repo-tags", h.CreateRepoTag)
	router.POST("/repos/:repo_id/repo-tags/", h.CreateRepoTag)
	router.PUT("/repos/:repo_id/repo-tags/:tag_id", h.UpdateRepoTag)
	router.PUT("/repos/:repo_id/repo-tags/:tag_id/", h.UpdateRepoTag)
	router.DELETE("/repos/:repo_id/repo-tags/:tag_id", h.DeleteRepoTag)
	router.DELETE("/repos/:repo_id/repo-tags/:tag_id/", h.DeleteRepoTag)

	// File tags
	router.GET("/repos/:repo_id/file-tags", h.GetFileTags)
	router.GET("/repos/:repo_id/file-tags/", h.GetFileTags)
	router.POST("/repos/:repo_id/file-tags", h.AddFileTag)
	router.POST("/repos/:repo_id/file-tags/", h.AddFileTag)
	router.DELETE("/repos/:repo_id/file-tags/:file_tag_id", h.RemoveFileTag)
	router.DELETE("/repos/:repo_id/file-tags/:file_tag_id/", h.RemoveFileTag)
}

// RepoTag represents a repository tag
type RepoTag struct {
	ID        int    `json:"repo_tag_id"`
	RepoID    string `json:"repo_id"`
	Name      string `json:"tag_name"`
	Color     string `json:"tag_color"`
	FileCount int    `json:"files_count,omitempty"`
}

// FileTagResponse represents a file tag in API responses
type FileTagResponse struct {
	ID        int    `json:"file_tag_id"`
	RepoTagID int    `json:"repo_tag_id"`
	Name      string `json:"tag_name"`
	Color     string `json:"tag_color"`
}

// ListRepoTags returns all tags for a repository
// GET /api/v2.1/repos/:repo_id/repo-tags/
func (h *TagHandler) ListRepoTags(c *gin.Context) {
	repoID := c.Param("repo_id")

	tags := []RepoTag{}

	if h.db != nil {
		repoUUID, err := gocql.ParseUUID(repoID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid repo_id"})
			return
		}

		iter := h.db.Session().Query(`
			SELECT tag_id, name, color FROM repo_tags WHERE repo_id = ?
		`, repoUUID).Iter()

		var tagID int
		var name, color string
		for iter.Scan(&tagID, &name, &color) {
			// Count files with this tag
			var fileCount int
			h.db.Session().Query(`
				SELECT COUNT(*) FROM file_tags WHERE repo_id = ? AND tag_id = ? ALLOW FILTERING
			`, repoUUID, tagID).Scan(&fileCount)

			tags = append(tags, RepoTag{
				ID:        tagID,
				RepoID:    repoID,
				Name:      name,
				Color:     color,
				FileCount: fileCount,
			})
		}
		iter.Close()
	}

	c.JSON(http.StatusOK, gin.H{
		"repo_tags": tags,
	})
}

// CreateRepoTagRequest represents the request body for creating a tag
type CreateRepoTagRequest struct {
	Name  string `json:"name" form:"name"`
	Color string `json:"color" form:"color"`
}

// CreateRepoTag creates a new tag for a repository
// POST /api/v2.1/repos/:repo_id/repo-tags/
func (h *TagHandler) CreateRepoTag(c *gin.Context) {
	repoID := c.Param("repo_id")

	var req CreateRepoTagRequest
	// Try JSON first, then form
	if c.ContentType() == "application/json" {
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	} else {
		req.Name = c.PostForm("name")
		req.Color = c.PostForm("color")
	}

	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return
	}
	if req.Color == "" {
		req.Color = "#FF8000" // Default orange color
	}

	var tagID int = 1

	if h.db != nil {
		repoUUID, err := gocql.ParseUUID(repoID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid repo_id"})
			return
		}

		// Get next tag ID using lightweight transaction
		var currentID int
		applied, err := h.db.Session().Query(`
			INSERT INTO repo_tag_counters (repo_id, next_tag_id) VALUES (?, 1) IF NOT EXISTS
		`, repoUUID).ScanCAS(&currentID)

		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to initialize tag counter"})
			return
		}

		if !applied {
			// Counter exists, increment it
			h.db.Session().Query(`
				SELECT next_tag_id FROM repo_tag_counters WHERE repo_id = ?
			`, repoUUID).Scan(&tagID)

			// Update counter
			h.db.Session().Query(`
				UPDATE repo_tag_counters SET next_tag_id = ? WHERE repo_id = ?
			`, tagID+1, repoUUID).Exec()
		}

		// Create the tag
		err = h.db.Session().Query(`
			INSERT INTO repo_tags (repo_id, tag_id, name, color, created_at)
			VALUES (?, ?, ?, ?, ?)
		`, repoUUID, tagID, req.Name, req.Color, time.Now()).Exec()

		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create tag"})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"repo_tag": RepoTag{
			ID:     tagID,
			RepoID: repoID,
			Name:   req.Name,
			Color:  req.Color,
		},
	})
}

// UpdateRepoTag updates a tag
// PUT /api/v2.1/repos/:repo_id/repo-tags/:tag_id/
func (h *TagHandler) UpdateRepoTag(c *gin.Context) {
	repoID := c.Param("repo_id")
	tagIDStr := c.Param("tag_id")

	tagID, err := strconv.Atoi(tagIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid tag_id"})
		return
	}

	var req CreateRepoTagRequest
	if c.ContentType() == "application/json" {
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
	} else {
		req.Name = c.PostForm("name")
		req.Color = c.PostForm("color")
	}

	if h.db != nil {
		repoUUID, err := gocql.ParseUUID(repoID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid repo_id"})
			return
		}

		err = h.db.Session().Query(`
			UPDATE repo_tags SET name = ?, color = ? WHERE repo_id = ? AND tag_id = ?
		`, req.Name, req.Color, repoUUID, tagID).Exec()

		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update tag"})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"repo_tag": RepoTag{
			ID:     tagID,
			RepoID: repoID,
			Name:   req.Name,
			Color:  req.Color,
		},
	})
}

// DeleteRepoTag deletes a tag
// DELETE /api/v2.1/repos/:repo_id/repo-tags/:tag_id/
func (h *TagHandler) DeleteRepoTag(c *gin.Context) {
	repoID := c.Param("repo_id")
	tagIDStr := c.Param("tag_id")

	tagID, err := strconv.Atoi(tagIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid tag_id"})
		return
	}

	if h.db != nil {
		repoUUID, err := gocql.ParseUUID(repoID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid repo_id"})
			return
		}

		// Delete the tag
		err = h.db.Session().Query(`
			DELETE FROM repo_tags WHERE repo_id = ? AND tag_id = ?
		`, repoUUID, tagID).Exec()

		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete tag"})
			return
		}

		// Also delete all file tags with this tag
		h.db.Session().Query(`
			DELETE FROM file_tags WHERE repo_id = ? AND tag_id = ?
		`, repoUUID, tagID).Exec()
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// GetFileTags returns tags for a specific file
// GET /api/v2.1/repos/:repo_id/file-tags/?file_path=/xxx
func (h *TagHandler) GetFileTags(c *gin.Context) {
	repoID := c.Param("repo_id")
	filePath := c.Query("file_path")
	if filePath == "" {
		filePath = c.Query("p")
	}

	tags := []FileTagResponse{}

	if h.db != nil && filePath != "" {
		repoUUID, err := gocql.ParseUUID(repoID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid repo_id"})
			return
		}

		// Get all tag IDs for this file from the main table (efficient query)
		iter := h.db.Session().Query(`
			SELECT tag_id FROM file_tags WHERE repo_id = ? AND file_path = ?
		`, repoUUID, filePath).Iter()

		var tagID int
		for iter.Scan(&tagID) {
			// Look up the file_tag_id from the lookup table
			var fileTagID int
			h.db.Session().Query(`
				SELECT file_tag_id FROM file_tags_by_id WHERE repo_id = ? AND file_path = ? AND tag_id = ? ALLOW FILTERING
			`, repoUUID, filePath, tagID).Scan(&fileTagID)

			// Get tag details
			var name, color string
			err := h.db.Session().Query(`
				SELECT name, color FROM repo_tags WHERE repo_id = ? AND tag_id = ?
			`, repoUUID, tagID).Scan(&name, &color)

			if err == nil {
				tags = append(tags, FileTagResponse{
					ID:        fileTagID,
					RepoTagID: tagID,
					Name:      name,
					Color:     color,
				})
			}
		}
		iter.Close()
	}

	c.JSON(http.StatusOK, gin.H{
		"file_tags": tags,
	})
}

// FileTagAddRequest represents the request body for adding a file tag
type FileTagAddRequest struct {
	FilePath  string `json:"file_path" form:"file_path"`
	RepoTagID int    `json:"repo_tag_id" form:"repo_tag_id"`
}

// AddFileTag adds a tag to a file
// POST /api/v2.1/repos/:repo_id/file-tags/
func (h *TagHandler) AddFileTag(c *gin.Context) {
	repoID := c.Param("repo_id")

	var filePath string
	var repoTagID int

	if c.ContentType() == "application/json" {
		var req FileTagAddRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		filePath = req.FilePath
		repoTagID = req.RepoTagID
	} else {
		filePath = c.PostForm("file_path")
		if filePath == "" {
			filePath = c.PostForm("p")
		}
		tagIDStr := c.PostForm("repo_tag_id")
		var err error
		repoTagID, err = strconv.Atoi(tagIDStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid repo_tag_id"})
			return
		}
	}

	if filePath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file_path is required"})
		return
	}

	var tagName, tagColor string
	var fileTagID int = 1

	if h.db != nil {
		repoUUID, err := gocql.ParseUUID(repoID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid repo_id"})
			return
		}

		// Get tag info
		err = h.db.Session().Query(`
			SELECT name, color FROM repo_tags WHERE repo_id = ? AND tag_id = ?
		`, repoUUID, repoTagID).Scan(&tagName, &tagColor)

		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "tag not found"})
			return
		}

		// Get next file_tag_id using counter - simpler approach without LWT
		err = h.db.Session().Query(`
			SELECT next_file_tag_id FROM file_tag_counters WHERE repo_id = ?
		`, repoUUID).Scan(&fileTagID)

		if err != nil {
			// Counter doesn't exist, create it with value 1
			fileTagID = 1
			err = h.db.Session().Query(`
				INSERT INTO file_tag_counters (repo_id, next_file_tag_id) VALUES (?, ?)
			`, repoUUID, 2).Exec()
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to initialize file tag counter"})
				return
			}
		} else {
			// Increment counter
			h.db.Session().Query(`
				UPDATE file_tag_counters SET next_file_tag_id = ? WHERE repo_id = ?
			`, fileTagID+1, repoUUID).Exec()
		}

		now := time.Now()

		// Add file tag to main table
		err = h.db.Session().Query(`
			INSERT INTO file_tags (repo_id, file_path, tag_id, created_at)
			VALUES (?, ?, ?, ?)
		`, repoUUID, filePath, repoTagID, now).Exec()

		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to add file tag"})
			return
		}

		// Add to lookup table with unique file_tag_id
		err = h.db.Session().Query(`
			INSERT INTO file_tags_by_id (repo_id, file_tag_id, file_path, tag_id, created_at)
			VALUES (?, ?, ?, ?, ?)
		`, repoUUID, fileTagID, filePath, repoTagID, now).Exec()

		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to add file tag lookup"})
			return
		}
	} else {
		tagName = "Tag"
		tagColor = "#FF8000"
	}

	c.JSON(http.StatusOK, gin.H{
		"file_tag": FileTagResponse{
			ID:        fileTagID,
			RepoTagID: repoTagID,
			Name:      tagName,
			Color:     tagColor,
		},
	})
}

// RemoveFileTag removes a tag from a file
// DELETE /api/v2.1/repos/:repo_id/file-tags/:file_tag_id/
func (h *TagHandler) RemoveFileTag(c *gin.Context) {
	repoID := c.Param("repo_id")
	fileTagIDStr := c.Param("file_tag_id")

	fileTagID, err := strconv.Atoi(fileTagIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid file_tag_id"})
		return
	}

	if h.db != nil {
		repoUUID, err := gocql.ParseUUID(repoID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid repo_id"})
			return
		}

		// Look up the file_tag by ID to get file_path and tag_id
		var filePath string
		var tagID int
		err = h.db.Session().Query(`
			SELECT file_path, tag_id FROM file_tags_by_id WHERE repo_id = ? AND file_tag_id = ?
		`, repoUUID, fileTagID).Scan(&filePath, &tagID)

		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "file tag not found"})
			return
		}

		// Delete from both tables
		err = h.db.Session().Query(`
			DELETE FROM file_tags WHERE repo_id = ? AND file_path = ? AND tag_id = ?
		`, repoUUID, filePath, tagID).Exec()

		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to remove file tag"})
			return
		}

		err = h.db.Session().Query(`
			DELETE FROM file_tags_by_id WHERE repo_id = ? AND file_tag_id = ?
		`, repoUUID, fileTagID).Exec()

		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to remove file tag lookup"})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}
