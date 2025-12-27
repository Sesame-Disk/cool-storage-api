package v2

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// FileHandler handles file-related API requests
type FileHandler struct {
	db     *db.DB
	config *config.Config
}

// RegisterFileRoutes registers file routes
func RegisterFileRoutes(rg *gin.RouterGroup, database *db.DB, cfg *config.Config) {
	h := &FileHandler{db: database, config: cfg}

	repos := rg.Group("/repos/:repo_id")
	{
		// Directory operations
		repos.GET("/dir", h.ListDirectory)
		repos.POST("/dir", h.CreateDirectory)
		repos.DELETE("/dir", h.DeleteDirectory)

		// File operations
		repos.GET("/file", h.GetFileInfo)
		repos.DELETE("/file", h.DeleteFile)
		repos.POST("/file/move", h.MoveFile)
		repos.POST("/file/copy", h.CopyFile)

		// Upload/Download links
		repos.GET("/file/download-link", h.GetDownloadLink)
		repos.POST("/upload-link", h.GetUploadLink)

		// Direct upload (for smaller files)
		repos.POST("/upload", h.UploadFile)
	}
}

// ListDirectory returns the contents of a directory
func (h *FileHandler) ListDirectory(c *gin.Context) {
	repoID := c.Param("repo_id")
	dirPath := c.DefaultQuery("p", "/")
	orgID := c.GetString("org_id")

	// Normalize path
	dirPath = normalizePath(dirPath)
	_ = dirPath // Will be used when implementing directory listing

	// Get library to verify access (use strings for UUID binding)
	var libID, headCommitID string
	if err := h.db.Session().Query(`
		SELECT library_id, head_commit_id FROM libraries
		WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(&libID, &headCommitID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}

	// TODO: Traverse commits and fs_objects to build directory listing
	// For now, return empty directory
	c.JSON(http.StatusOK, gin.H{
		"dirent_list": []models.FileInfo{},
	})
}

// CreateDirectoryRequest represents the request for creating a directory
type CreateDirectoryRequest struct {
	Path string `json:"path" form:"path" binding:"required"`
}

// CreateDirectory creates a new directory
func (h *FileHandler) CreateDirectory(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")

	var req CreateDirectoryRequest
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	dirPath := normalizePath(req.Path)
	if dirPath == "/" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot create root directory"})
		return
	}

	// TODO: Create directory in fs_objects and new commit
	_ = orgID // Will be used when implementing directory creation

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"repo_id": repoID,
		"path":    dirPath,
	})
}

// DeleteDirectory deletes a directory
func (h *FileHandler) DeleteDirectory(c *gin.Context) {
	dirPath := c.Query("p")
	if dirPath == "" || dirPath == "/" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid path"})
		return
	}

	// TODO: Delete directory and all contents
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// GetFileInfo returns information about a file
func (h *FileHandler) GetFileInfo(c *gin.Context) {
	filePath := c.Query("p")
	if filePath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is required"})
		return
	}

	// TODO: Get file info from fs_objects
	c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
}

// DeleteFile deletes a file
func (h *FileHandler) DeleteFile(c *gin.Context) {
	filePath := c.Query("p")
	if filePath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is required"})
		return
	}

	// TODO: Delete file and update commit
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// MoveFileRequest represents the request for moving a file
type MoveFileRequest struct {
	SrcPath string `json:"src" form:"src" binding:"required"`
	DstPath string `json:"dst" form:"dst" binding:"required"`
}

// MoveFile moves a file to a new location
func (h *FileHandler) MoveFile(c *gin.Context) {
	var req MoveFileRequest
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// TODO: Implement move
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// CopyFileRequest represents the request for copying a file
type CopyFileRequest struct {
	SrcPath string `json:"src" form:"src" binding:"required"`
	DstPath string `json:"dst" form:"dst" binding:"required"`
}

// CopyFile copies a file to a new location
func (h *FileHandler) CopyFile(c *gin.Context) {
	var req CopyFileRequest
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// TODO: Implement copy
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// GetDownloadLink returns a presigned URL for downloading a file
func (h *FileHandler) GetDownloadLink(c *gin.Context) {
	filePath := c.Query("p")
	if filePath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is required"})
		return
	}

	// TODO: Generate presigned S3 URL
	// For now, return a placeholder
	c.JSON(http.StatusOK, models.DownloadLink{
		URL:       "https://example.com/download/placeholder",
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})
}

// GetUploadLink returns a presigned URL for uploading a file
func (h *FileHandler) GetUploadLink(c *gin.Context) {
	filePath := c.Query("p")
	if filePath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is required"})
		return
	}

	// TODO: Generate presigned S3 URL for upload
	// For now, return a placeholder
	c.JSON(http.StatusOK, models.UploadLink{
		URL:       "https://example.com/upload/placeholder",
		Token:     uuid.New().String(),
		ExpiresAt: time.Now().Add(1 * time.Hour),
	})
}

// UploadFile handles direct file uploads (for smaller files)
func (h *FileHandler) UploadFile(c *gin.Context) {
	repoID := c.Param("repo_id")
	parentDir := c.DefaultPostForm("parent_dir", "/")

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file is required"})
		return
	}
	defer file.Close()

	orgID := c.GetString("org_id")

	// Read file content and calculate hash
	content, err := io.ReadAll(file)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read file"})
		return
	}

	hash := sha256.Sum256(content)
	blockID := hex.EncodeToString(hash[:])

	// Check if block already exists (deduplication) - use string for UUID
	var existingBlockID string
	_ = h.db.Session().Query(`
		SELECT block_id FROM blocks WHERE org_id = ? AND block_id = ?
	`, orgID, blockID).Scan(&existingBlockID)

	if existingBlockID == "" {
		// TODO: Upload block to S3
		// TODO: Store block metadata in database

		// For now, just record the block (use string for UUID)
		if err := h.db.Session().Query(`
			INSERT INTO blocks (org_id, block_id, size_bytes, storage_class, storage_key, ref_count, created_at, last_accessed)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, orgID, blockID, len(content), h.config.Storage.DefaultClass,
			fmt.Sprintf("%s/%s", orgID, blockID), 1, time.Now(), time.Now(),
		).Exec(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to store block"})
			return
		}
	}

	// TODO: Create/update fs_object and commit

	filePath := path.Join(parentDir, header.Filename)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"id":      blockID,
		"name":    header.Filename,
		"path":    filePath,
		"size":    len(content),
		"repo_id": repoID,
	})
}

// normalizePath ensures path starts with / and removes trailing /
func normalizePath(p string) string {
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if p != "/" && strings.HasSuffix(p, "/") {
		p = strings.TrimSuffix(p, "/")
	}
	return path.Clean(p)
}
