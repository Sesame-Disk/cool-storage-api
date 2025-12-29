package v2

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/gin-gonic/gin"
)

// TokenCreator is an interface for creating access tokens
type TokenCreator interface {
	CreateUploadToken(orgID, repoID, path, userID string) (string, error)
	CreateDownloadToken(orgID, repoID, path, userID string) (string, error)
}

// Dirent represents a directory entry in Seafile API format
// This matches the exact format expected by Seafile clients
type Dirent struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Type          string `json:"type"` // "file" or "dir"
	Size          int64  `json:"size"`
	MTime         int64  `json:"mtime"`      // Unix timestamp
	Permission    string `json:"permission"` // "rw" or "r"
	ParentDir     string `json:"parent_dir,omitempty"`
	Starred       bool   `json:"starred,omitempty"`
	ModifierEmail string `json:"modifier_email,omitempty"`
	ModifierName  string `json:"modifier_name,omitempty"`
}

// FSEntry represents a directory entry stored in fs_objects.dir_entries
// This matches the Seafile format for directory entries
type FSEntry struct {
	Name     string `json:"name"`
	ID       string `json:"id"`       // FS object ID (40 char hex)
	Mode     int    `json:"mode"`     // Unix file mode (33188 = regular file, 16384 = directory)
	MTime    int64  `json:"mtime"`    // Unix timestamp
	Size     int64  `json:"size,omitempty"`
	Modifier string `json:"modifier,omitempty"`
}

// ModeFile is the Unix mode for a regular file (0100644)
const ModeFile = 33188

// ModeDir is the Unix mode for a directory (040000)
const ModeDir = 16384

// FileHandler handles file-related API requests
type FileHandler struct {
	db           *db.DB
	config       *config.Config
	storage      *storage.S3Store
	tokenCreator TokenCreator
	serverURL    string // Base URL of the server for generating seafhttp URLs
}

// RegisterFileRoutes registers file routes
func RegisterFileRoutes(rg *gin.RouterGroup, database *db.DB, cfg *config.Config, s3Store *storage.S3Store, tokenCreator TokenCreator, serverURL string) {
	h := &FileHandler{
		db:           database,
		config:       cfg,
		storage:      s3Store,
		tokenCreator: tokenCreator,
		serverURL:    serverURL,
	}

	repos := rg.Group("/repos/:repo_id")
	{
		// Directory operations (both with and without trailing slash for Seafile compatibility)
		repos.GET("/dir", h.ListDirectory)
		repos.GET("/dir/", h.ListDirectory)
		repos.POST("/dir", h.CreateDirectory)
		repos.POST("/dir/", h.CreateDirectory)
		repos.DELETE("/dir", h.DeleteDirectory)
		repos.DELETE("/dir/", h.DeleteDirectory)

		// File operations
		repos.GET("/file", h.GetFileInfo)
		repos.GET("/file/", h.GetFileInfo)
		repos.DELETE("/file", h.DeleteFile)
		repos.DELETE("/file/", h.DeleteFile)
		repos.POST("/file/move", h.MoveFile)
		repos.POST("/file/copy", h.CopyFile)

		// Upload/Download links (Seafile uses GET for both)
		repos.GET("/file/download-link", h.GetDownloadLink)
		repos.GET("/file/download-link/", h.GetDownloadLink)
		repos.GET("/upload-link", h.GetUploadLink)
		repos.GET("/upload-link/", h.GetUploadLink)

		// Direct upload (for smaller files)
		repos.POST("/upload", h.UploadFile)
		repos.POST("/upload/", h.UploadFile)

		// Sync info endpoint (for desktop client)
		repos.GET("/download-info", h.GetDownloadInfo)
		repos.GET("/download-info/", h.GetDownloadInfo)
	}
}

// ListDirectory returns the contents of a directory
// Implements Seafile API: GET /api2/repos/:repo_id/dir/?p=/path
// Reads from fs_objects for proper Seafile compatibility
func (h *FileHandler) ListDirectory(c *gin.Context) {
	repoID := c.Param("repo_id")
	dirPath := c.DefaultQuery("p", "/")
	orgID := c.GetString("org_id")

	// Normalize path
	dirPath = normalizePath(dirPath)

	// Check if database is available
	if h.db == nil {
		c.JSON(http.StatusOK, []Dirent{})
		return
	}

	// Get library's head_commit_id
	var libID, headCommitID string
	err := h.db.Session().Query(`
		SELECT library_id, head_commit_id FROM libraries
		WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(&libID, &headCommitID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}

	// If no head commit, return empty directory
	if headCommitID == "" {
		c.JSON(http.StatusOK, []Dirent{})
		return
	}

	// Get root_fs_id from the head commit
	var rootFSID string
	err = h.db.Session().Query(`
		SELECT root_fs_id FROM commits
		WHERE library_id = ? AND commit_id = ?
	`, repoID, headCommitID).Scan(&rootFSID)
	if err != nil {
		log.Printf("ListDirectory: failed to get commit %s: %v", headCommitID, err)
		c.JSON(http.StatusOK, []Dirent{})
		return
	}

	// Traverse from root to requested path
	currentFSID := rootFSID
	if dirPath != "/" {
		// Split path into components and traverse
		parts := strings.Split(strings.Trim(dirPath, "/"), "/")
		for _, part := range parts {
			if part == "" {
				continue
			}

			// Get current directory's entries
			var entriesJSON string
			err = h.db.Session().Query(`
				SELECT dir_entries FROM fs_objects
				WHERE library_id = ? AND fs_id = ?
			`, repoID, currentFSID).Scan(&entriesJSON)
			if err != nil {
				log.Printf("ListDirectory: failed to get fs_object %s: %v", currentFSID, err)
				c.JSON(http.StatusNotFound, gin.H{"error": "directory not found"})
				return
			}

			// Parse entries and find the next component
			var entries []FSEntry
			if entriesJSON != "" && entriesJSON != "[]" {
				if err := json.Unmarshal([]byte(entriesJSON), &entries); err != nil {
					log.Printf("ListDirectory: failed to parse entries for %s: %v", currentFSID, err)
					c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid directory data"})
					return
				}
			}

			// Find the child directory
			found := false
			for _, entry := range entries {
				if entry.Name == part {
					// Check if it's a directory (mode & 0170000 == 040000 for dirs)
					if entry.Mode&0170000 == 040000 || entry.Mode == ModeDir {
						currentFSID = entry.ID
						found = true
						break
					} else {
						// Path component is not a directory
						c.JSON(http.StatusBadRequest, gin.H{"error": "path is not a directory"})
						return
					}
				}
			}

			if !found {
				c.JSON(http.StatusNotFound, gin.H{"error": "directory not found"})
				return
			}
		}
	}

	// Get the target directory's entries
	var entriesJSON string
	err = h.db.Session().Query(`
		SELECT dir_entries FROM fs_objects
		WHERE library_id = ? AND fs_id = ?
	`, repoID, currentFSID).Scan(&entriesJSON)
	if err != nil {
		log.Printf("ListDirectory: failed to get target fs_object %s: %v", currentFSID, err)
		c.JSON(http.StatusOK, []Dirent{})
		return
	}

	// Parse entries
	var entries []FSEntry
	if entriesJSON != "" && entriesJSON != "[]" {
		if err := json.Unmarshal([]byte(entriesJSON), &entries); err != nil {
			log.Printf("ListDirectory: failed to parse target entries for %s: %v", currentFSID, err)
			c.JSON(http.StatusOK, []Dirent{})
			return
		}
	}

	// Convert FSEntry to Dirent for API response
	direntList := make([]Dirent, 0, len(entries))
	for _, entry := range entries {
		// Determine type from mode
		fileType := "file"
		if entry.Mode&0170000 == 040000 || entry.Mode == ModeDir {
			fileType = "dir"
		}

		dirent := Dirent{
			ID:         entry.ID,
			Name:       entry.Name,
			Type:       fileType,
			Size:       entry.Size,
			MTime:      entry.MTime,
			Permission: "rw",
			ParentDir:  dirPath,
		}

		// Add modifier if available
		if entry.Modifier != "" {
			dirent.ModifierEmail = entry.Modifier
		}

		direntList = append(direntList, dirent)
	}

	// Seafile API /api2/repos/:id/dir/ always returns flat array
	c.JSON(http.StatusOK, direntList)
}

// generatePathID creates a deterministic ID for a file/dir path
// This is a placeholder - in a full implementation, IDs come from fs_objects
func generatePathID(orgID, repoID, filePath string) string {
	hash := sha256.Sum256([]byte(orgID + "/" + repoID + filePath))
	return hex.EncodeToString(hash[:20]) // 40 character hex string like Seafile
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

// GetDownloadLink returns a URL for downloading a file (Seafile compatible)
// The URL points to the server's seafhttp endpoint, not directly to S3
func (h *FileHandler) GetDownloadLink(c *gin.Context) {
	repoID := c.Param("repo_id")
	filePath := c.Query("p")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	if filePath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is required"})
		return
	}

	// Check if token creator is available
	if h.tokenCreator == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "service not available"})
		return
	}

	// Normalize path
	filePath = normalizePath(filePath)

	// Create a download token
	token, err := h.tokenCreator.CreateDownloadToken(orgID, repoID, filePath, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate download link"})
		return
	}

	// Get the filename from the path
	filename := filepath.Base(filePath)

	// Build the Seafile-compatible download URL
	// Format: {server}/seafhttp/files/{token}/{filename}
	downloadURL := fmt.Sprintf("%s/seafhttp/files/%s/%s", h.serverURL, token, filename)

	// Return just the URL string (Seafile compatible)
	c.String(http.StatusOK, downloadURL)
}

// GetUploadLink returns a URL for uploading a file (Seafile compatible)
// The URL points to the server's seafhttp endpoint, not directly to S3
func (h *FileHandler) GetUploadLink(c *gin.Context) {
	repoID := c.Param("repo_id")
	parentDir := c.DefaultQuery("p", "/")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	// Check if token creator is available
	if h.tokenCreator == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "service not available"})
		return
	}

	// Normalize path
	parentDir = normalizePath(parentDir)

	// Create an upload token
	token, err := h.tokenCreator.CreateUploadToken(orgID, repoID, parentDir, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate upload link"})
		return
	}

	// Build the Seafile-compatible upload URL
	// Format: {server}/seafhttp/upload-api/{token}
	uploadURL := fmt.Sprintf("%s/seafhttp/upload-api/%s", h.serverURL, token)

	// Return just the URL string (Seafile compatible)
	c.String(http.StatusOK, uploadURL)
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

	// Check if block already exists (deduplication)
	var existingBlockID string
	_ = h.db.Session().Query(`
		SELECT block_id FROM blocks WHERE org_id = ? AND block_id = ?
	`, orgID, blockID).Scan(&existingBlockID)

	// Storage key format: org_id/block_id (content-addressed)
	storageKey := fmt.Sprintf("%s/%s", orgID, blockID)

	if existingBlockID == "" {
		// Upload block to S3 if storage is available
		if h.storage != nil {
			_, err := h.storage.Put(c.Request.Context(), storageKey, bytes.NewReader(content), int64(len(content)))
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to upload to storage"})
				return
			}
		}

		// Store block metadata in database
		if err := h.db.Session().Query(`
			INSERT INTO blocks (org_id, block_id, size_bytes, storage_class, storage_key, ref_count, created_at, last_accessed)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, orgID, blockID, len(content), h.config.Storage.DefaultClass,
			storageKey, 1, time.Now(), time.Now(),
		).Exec(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to store block metadata"})
			return
		}
	} else {
		// Block exists, increment ref count
		if err := h.db.Session().Query(`
			UPDATE blocks SET ref_count = ref_count + 1, last_accessed = ?
			WHERE org_id = ? AND block_id = ?
		`, time.Now(), orgID, blockID).Exec(); err != nil {
			// Non-fatal error, continue
		}
	}

	// TODO: Create/update fs_object and commit

	filePath := path.Join(parentDir, header.Filename)

	c.JSON(http.StatusOK, gin.H{
		"success":     true,
		"id":          blockID,
		"name":        header.Filename,
		"path":        filePath,
		"size":        len(content),
		"repo_id":     repoID,
		"storage_key": storageKey,
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

// GetDownloadInfo returns repository sync information for desktop client
// Implements Seafile API: GET /api2/repos/:repo_id/download-info/
func (h *FileHandler) GetDownloadInfo(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	// Get library info from database
	var libID, ownerID, name, description, headCommitID string
	var encrypted bool
	var encVersion int
	var magic, randomKey string
	var sizeBytes int64
	var updatedAt time.Time

	err := h.db.Session().Query(`
		SELECT library_id, owner_id, name, description, encrypted, enc_version,
		       magic, random_key, head_commit_id, size_bytes, updated_at
		FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(
		&libID, &ownerID, &name, &description, &encrypted, &encVersion,
		&magic, &randomKey, &headCommitID, &sizeBytes, &updatedAt,
	)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}

	// Generate a sync token for this repo
	token, err := h.tokenCreator.CreateDownloadToken(orgID, repoID, "/", userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate sync token"})
		return
	}

	// Extract port from server URL or config
	serverPort := "8080" // Default port
	if h.config != nil && h.config.Server.Port != "" {
		// Port in config includes colon, e.g., ":8080"
		serverPort = strings.TrimPrefix(h.config.Server.Port, ":")
	}

	// Build response in Seafile format
	response := gin.H{
		"relay_id":      "localhost",                          // Relay server ID
		"relay_addr":    "localhost",                          // Relay server address
		"relay_port":    serverPort,                           // Relay server port (same as HTTP)
		"email":         userID + "@sesamefs.local",           // User email
		"token":         token,                                // Sync token
		"repo_id":       repoID,                               // Repository ID
		"repo_name":     name,                                 // Repository name
		"repo_desc":     description,                          // Repository description
		"repo_size":     sizeBytes,                            // Repository size
		"repo_version":  1,                                    // Repository version
		"mtime":         updatedAt.Unix(),                     // Last modification time
		"encrypted":     encrypted,                            // Is encrypted
		"permission":    "rw",                                 // User permission
		"head_commit_id": headCommitID,                        // Head commit ID
		"is_corrupted":  false,                                // Is repository corrupted
	}

	// Add encryption fields if encrypted
	if encrypted {
		response["enc_version"] = encVersion
		response["magic"] = magic
		response["random_key"] = randomKey
	}

	c.JSON(http.StatusOK, response)
}
