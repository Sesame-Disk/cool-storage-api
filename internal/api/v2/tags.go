package v2

import (
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
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

	// Tagged files - list files with a specific tag
	router.GET("/repos/:repo_id/tagged-files/:tag_id", h.ListTaggedFiles)
	router.GET("/repos/:repo_id/tagged-files/:tag_id/", h.ListTaggedFiles)
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
			// Get file count from counter table (no ALLOW FILTERING needed)
			var fileCount int64
			err := h.db.Session().Query(`
				SELECT file_count FROM repo_tag_file_counts WHERE repo_id = ? AND tag_id = ?
			`, repoUUID, tagID).Scan(&fileCount)
			if err != nil {
				// Counter doesn't exist yet, set to 0
				fileCount = 0
			}

			tags = append(tags, RepoTag{
				ID:        tagID,
				RepoID:    repoID,
				Name:      name,
				Color:     color,
				FileCount: int(fileCount),
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
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
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

		// Get current tag ID counter
		err = h.db.Session().Query(`
			SELECT next_tag_id FROM repo_tag_counters WHERE repo_id = ?
		`, repoUUID).Scan(&tagID)

		if err != nil {
			// Counter doesn't exist, create it with initial value 1
			tagID = 1
			err = h.db.Session().Query(`
				INSERT INTO repo_tag_counters (repo_id, next_tag_id) VALUES (?, ?)
			`, repoUUID, 2).Exec()
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to initialize tag counter"})
				return
			}
		} else {
			// Counter exists, increment it
			err = h.db.Session().Query(`
				UPDATE repo_tag_counters SET next_tag_id = ? WHERE repo_id = ?
			`, tagID+1, repoUUID).Exec()
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update tag counter"})
				return
			}
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
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
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

		// First, find all file_tags with this tag_id (need file_path for full primary key)
		iter := h.db.Session().Query(`
			SELECT file_path, file_tag_id FROM file_tags WHERE repo_id = ? AND tag_id = ? ALLOW FILTERING
		`, repoUUID, tagID).Iter()

		var filePath string
		var fileTagID int
		batch := h.db.Session().Batch(gocql.LoggedBatch)

		// Delete the repo tag itself
		batch.Query(`
			DELETE FROM repo_tags WHERE repo_id = ? AND tag_id = ?
		`, repoUUID, tagID)

		for iter.Scan(&filePath, &fileTagID) {
			// Delete from file_tags with full primary key
			batch.Query(`
				DELETE FROM file_tags WHERE repo_id = ? AND file_path = ? AND tag_id = ?
			`, repoUUID, filePath, tagID)
			// Delete from lookup table
			batch.Query(`
				DELETE FROM file_tags_by_id WHERE repo_id = ? AND file_tag_id = ?
			`, repoUUID, fileTagID)
		}
		iter.Close()

		err = batch.Exec()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete tag"})
			return
		}

		// Counter updates must be separate from non-counter operations in Cassandra
		if err := h.db.Session().Query(`
			DELETE FROM repo_tag_file_counts WHERE repo_id = ? AND tag_id = ?
		`, repoUUID, tagID).Exec(); err != nil {
			log.Printf("[DeleteRepoTag] failed to clean repo_tag_file_counts for repo %s tag %d: %v", repoID, tagID, err)
		}
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

		// Get all tags for this file (efficient query, no ALLOW FILTERING needed)
		iter := h.db.Session().Query(`
			SELECT tag_id, file_tag_id FROM file_tags WHERE repo_id = ? AND file_path = ?
		`, repoUUID, filePath).Iter()

		var tagID, fileTagID int
		for iter.Scan(&tagID, &fileTagID) {

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

func reserveNextFileTagID(sess *gocql.Session, repoUUID gocql.UUID) (int, error) {
	const maxRetries = 8

	for i := 0; i < maxRetries; i++ {
		var nextID int
		err := sess.Query(`
			SELECT next_file_tag_id FROM file_tag_counters WHERE repo_id = ?
		`, repoUUID).Scan(&nextID)

		if err == gocql.ErrNotFound {
			applied, casErr := sess.Query(`
				INSERT INTO file_tag_counters (repo_id, next_file_tag_id) VALUES (?, ?) IF NOT EXISTS
			`, repoUUID, 2).MapScanCAS(map[string]interface{}{})
			if casErr != nil {
				return 0, casErr
			}
			if applied {
				return 1, nil
			}
			continue
		}
		if err != nil {
			return 0, err
		}

		applied, casErr := sess.Query(`
			UPDATE file_tag_counters SET next_file_tag_id = ?
			WHERE repo_id = ? IF next_file_tag_id = ?
		`, nextID+1, repoUUID, nextID).MapScanCAS(map[string]interface{}{})
		if casErr != nil {
			return 0, casErr
		}
		if applied {
			return nextID, nil
		}
	}

	return 0, gocql.ErrNotFound
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
	if repoTagID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid repo_tag_id"})
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

		fileTagID, err = reserveNextFileTagID(h.db.Session(), repoUUID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to allocate file tag id"})
			return
		}

		now := time.Now()

		// Use batch for non-counter inserts
		batch := h.db.Session().Batch(gocql.LoggedBatch)

		// Add file tag to main table (includes file_tag_id for efficient lookups)
		batch.Query(`
			INSERT INTO file_tags (repo_id, file_path, tag_id, file_tag_id, created_at)
			VALUES (?, ?, ?, ?, ?)
		`, repoUUID, filePath, repoTagID, fileTagID, now)

		// Add to lookup table with unique file_tag_id
		batch.Query(`
			INSERT INTO file_tags_by_id (repo_id, file_tag_id, file_path, tag_id, created_at)
			VALUES (?, ?, ?, ?, ?)
		`, repoUUID, fileTagID, filePath, repoTagID, now)

		err = batch.Exec()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to add file tag"})
			return
		}

		// Counter updates must be separate from non-counter operations in Cassandra
		// Use an unlogged batch or individual query
		err = h.db.Session().Query(`
			UPDATE repo_tag_file_counts SET file_count = file_count + 1
			WHERE repo_id = ? AND tag_id = ?
		`, repoUUID, repoTagID).Exec()
		if err != nil {
			log.Printf("[AddFileTag] failed to increment repo_tag_file_counts for repo %s tag %d: %v", repoID, repoTagID, err)
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
		batch := h.db.Session().Batch(gocql.LoggedBatch)

		batch.Query(`
			DELETE FROM file_tags WHERE repo_id = ? AND file_path = ? AND tag_id = ?
		`, repoUUID, filePath, tagID)

		batch.Query(`
			DELETE FROM file_tags_by_id WHERE repo_id = ? AND file_tag_id = ?
		`, repoUUID, fileTagID)

		err = batch.Exec()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to remove file tag"})
			return
		}

		// Counter updates must be separate from non-counter operations in Cassandra
		err = h.db.Session().Query(`
			UPDATE repo_tag_file_counts SET file_count = file_count - 1
			WHERE repo_id = ? AND tag_id = ?
		`, repoUUID, tagID).Exec()
		if err != nil {
			log.Printf("[RemoveFileTag] failed to decrement repo_tag_file_counts for repo %s tag %d: %v", repoID, tagID, err)
		}
	}

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// TaggedFileInfo represents a file with a specific tag
type TaggedFileInfo struct {
	FileTagID   int    `json:"file_tag_id"`
	ParentPath  string `json:"parent_path"`
	Filename    string `json:"filename"`
	Size        int64  `json:"size"`
	Mtime       int64  `json:"mtime"`
	FileDeleted bool   `json:"file_deleted"`
}

// ListTaggedFiles returns all files with a specific tag
// GET /api/v2.1/repos/:repo_id/tagged-files/:tag_id/
// Filters out files that no longer exist in the current HEAD commit tree.
func (h *TagHandler) ListTaggedFiles(c *gin.Context) {
	repoID := c.Param("repo_id")
	tagIDStr := c.Param("tag_id")

	tagID, err := strconv.Atoi(tagIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid tag_id"})
		return
	}

	taggedFiles := []TaggedFileInfo{}

	if h.db != nil {
		repoUUID, err := gocql.ParseUUID(repoID)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid repo_id"})
			return
		}

		// Create FSHelper to verify file existence in current HEAD commit tree
		fsHelper := NewFSHelper(h.db)

		// Query file_tags table to find all files with this tag
		// Note: Need to use ALLOW FILTERING since tag_id is not part of partition key
		iter := h.db.Session().Query(`
			SELECT file_path, file_tag_id FROM file_tags
			WHERE repo_id = ? AND tag_id = ?
			ALLOW FILTERING
		`, repoUUID, tagID).Iter()

		var filePath string
		var fileTagID int
		for iter.Scan(&filePath, &fileTagID) {
			// Verify file still exists in current HEAD commit tree
			result, traverseErr := fsHelper.TraverseToPath(repoID, filePath)
			if traverseErr != nil || result.TargetEntry == nil {
				// File no longer exists - skip stale tag association
				continue
			}

			// Extract parent_path and filename from file_path
			parentPath := "/"
			filename := filePath
			if len(filePath) > 0 {
				// Find last slash to split path
				lastSlash := -1
				for i := len(filePath) - 1; i >= 0; i-- {
					if filePath[i] == '/' {
						lastSlash = i
						break
					}
				}
				if lastSlash >= 0 {
					if lastSlash == 0 {
						parentPath = "/"
					} else {
						parentPath = filePath[:lastSlash]
					}
					filename = filePath[lastSlash+1:]
				}
			}

			taggedFiles = append(taggedFiles, TaggedFileInfo{
				FileTagID:   fileTagID,
				ParentPath:  parentPath,
				Filename:    filename,
				Size:        result.TargetEntry.Size,
				Mtime:       result.TargetEntry.MTime,
				FileDeleted: false,
			})
		}
		iter.Close()
	}

	c.JSON(http.StatusOK, gin.H{
		"tagged_files": taggedFiles,
	})
}

// CleanupFileTagsByPath removes all tag associations for a specific file path.
// This cleans up file_tags, file_tags_by_id, and decrements repo_tag_file_counts.
// Used as cascade cleanup when files are deleted.
func CleanupFileTagsByPath(database *db.DB, repoID, filePath string) {
	if database == nil {
		return
	}

	repoUUID, err := gocql.ParseUUID(repoID)
	if err != nil {
		return
	}

	// Find all tags for this file
	iter := database.Session().Query(`
		SELECT tag_id, file_tag_id FROM file_tags WHERE repo_id = ? AND file_path = ?
	`, repoUUID, filePath).Iter()

	var tagID, fileTagID int
	for iter.Scan(&tagID, &fileTagID) {
		// Delete from both tables
		batch := database.Session().Batch(gocql.LoggedBatch)
		batch.Query(`DELETE FROM file_tags WHERE repo_id = ? AND file_path = ? AND tag_id = ?`,
			repoUUID, filePath, tagID)
		batch.Query(`DELETE FROM file_tags_by_id WHERE repo_id = ? AND file_tag_id = ?`,
			repoUUID, fileTagID)
		if err := batch.Exec(); err != nil {
			log.Printf("[CleanupFileTagsByPath] failed to delete file tags for repo %s path %q tag %d: %v", repoID, filePath, tagID, err)
			continue
		}

		// Decrement counter (must be separate from non-counter operations in Cassandra)
		if err := database.Session().Query(`
			UPDATE repo_tag_file_counts SET file_count = file_count - 1
			WHERE repo_id = ? AND tag_id = ?
		`, repoUUID, tagID).Exec(); err != nil {
			log.Printf("[CleanupFileTagsByPath] failed to decrement repo_tag_file_counts for repo %s tag %d: %v", repoID, tagID, err)
		}
	}
	iter.Close()
}

// MoveFileTagsByPath moves all tag associations from oldPath to newPath.
// Used when a file is renamed — tags are preserved under the new path.
func MoveFileTagsByPath(database *db.DB, repoID, oldPath, newPath string) {
	if database == nil {
		return
	}

	repoUUID, err := gocql.ParseUUID(repoID)
	if err != nil {
		return
	}

	// Find all tags for the old path
	iter := database.Session().Query(`
		SELECT tag_id, file_tag_id, created_at FROM file_tags WHERE repo_id = ? AND file_path = ?
	`, repoUUID, oldPath).Iter()

	var tagID, fileTagID int
	var createdAt time.Time
	for iter.Scan(&tagID, &fileTagID, &createdAt) {
		// Insert with new path
		if err := database.Session().Query(`
			INSERT INTO file_tags (repo_id, file_path, tag_id, file_tag_id, created_at)
			VALUES (?, ?, ?, ?, ?)
		`, repoUUID, newPath, tagID, fileTagID, createdAt).Exec(); err != nil {
			log.Printf("[MoveFileTagsByPath] failed to insert file_tags for repo %s old_path %q new_path %q tag %d: %v", repoID, oldPath, newPath, tagID, err)
			continue
		}

		// Update lookup table
		if err := database.Session().Query(`
			INSERT INTO file_tags_by_id (repo_id, file_tag_id, file_path, tag_id, created_at)
			VALUES (?, ?, ?, ?, ?)
		`, repoUUID, fileTagID, newPath, tagID, createdAt).Exec(); err != nil {
			log.Printf("[MoveFileTagsByPath] failed to insert file_tags_by_id for repo %s old_path %q new_path %q tag %d: %v", repoID, oldPath, newPath, tagID, err)
			continue
		}

		// Delete old entries
		batch := database.Session().Batch(gocql.LoggedBatch)
		batch.Query(`DELETE FROM file_tags WHERE repo_id = ? AND file_path = ? AND tag_id = ?`,
			repoUUID, oldPath, tagID)
		batch.Query(`DELETE FROM file_tags_by_id WHERE repo_id = ? AND file_tag_id = ?`,
			repoUUID, fileTagID)
		if err := batch.Exec(); err != nil {
			log.Printf("[MoveFileTagsByPath] failed to delete old tag rows for repo %s old_path %q tag %d: %v", repoID, oldPath, tagID, err)
		}

		// Note: counters don't change — same tag, same count, just different path
	}
	iter.Close()
	log.Printf("[MoveFileTagsByPath] Moved tags from %q to %q in repo %s", oldPath, newPath, repoID)
}

// MoveFileTagsByPrefix moves all tag associations for files under oldPrefix to newPrefix.
// Used when a directory is renamed — all child file tags are updated.
func MoveFileTagsByPrefix(database *db.DB, repoID, oldPrefix, newPrefix string) {
	if database == nil {
		return
	}

	repoUUID, err := gocql.ParseUUID(repoID)
	if err != nil {
		return
	}

	// Move tags for the directory path itself
	MoveFileTagsByPath(database, repoID, oldPrefix, newPrefix)

	// Find all file_tags for this repo and filter by prefix
	oldPrefixSlash := oldPrefix + "/"
	iter := database.Session().Query(`
		SELECT file_path FROM file_tags WHERE repo_id = ?
	`, repoUUID).Iter()

	var fp string
	var pathsToMove []string
	for iter.Scan(&fp) {
		if strings.HasPrefix(fp, oldPrefixSlash) {
			pathsToMove = append(pathsToMove, fp)
		}
	}
	iter.Close()

	// Move each child path
	for _, oldChildPath := range pathsToMove {
		newChildPath := newPrefix + oldChildPath[len(oldPrefix):]
		MoveFileTagsByPath(database, repoID, oldChildPath, newChildPath)
	}

	if len(pathsToMove) > 0 {
		log.Printf("[MoveFileTagsByPrefix] Moved tags for %d children from %q to %q in repo %s",
			len(pathsToMove), oldPrefix, newPrefix, repoID)
	}
}

// CleanupAllLibraryTags removes ALL tag data for a library.
// Used when a library is permanently deleted.
func CleanupAllLibraryTags(database *db.DB, repoID string) {
	if database == nil {
		return
	}

	repoUUID, err := gocql.ParseUUID(repoID)
	if err != nil {
		return
	}

	batch := database.Session().Batch(gocql.LoggedBatch)
	batch.Query(`DELETE FROM file_tags WHERE repo_id = ?`, repoUUID)
	batch.Query(`DELETE FROM file_tags_by_id WHERE repo_id = ?`, repoUUID)
	batch.Query(`DELETE FROM repo_tags WHERE repo_id = ?`, repoUUID)
	batch.Query(`DELETE FROM repo_tag_file_counts WHERE repo_id = ?`, repoUUID)
	batch.Query(`DELETE FROM repo_tag_counters WHERE repo_id = ?`, repoUUID)
	batch.Query(`DELETE FROM file_tag_counters WHERE repo_id = ?`, repoUUID)
	if err := batch.Exec(); err != nil {
		log.Printf("[CleanupAllLibraryTags] failed to delete tag data for library %s: %v", repoID, err)
		return
	}

	log.Printf("[CleanupAllLibraryTags] Cleaned all tag data for library %s", repoID)
}
