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
	ID                     string `json:"id"`
	Name                   string `json:"name"`
	Type                   string `json:"type"` // "file" or "dir"
	Size                   int64  `json:"size"`
	MTime                  int64  `json:"mtime"`      // Unix timestamp
	Permission             string `json:"permission"` // "rw" or "r"
	ParentDir              string `json:"parent_dir,omitempty"`
	Starred                bool   `json:"starred,omitempty"`
	ModifierEmail          string `json:"modifier_email,omitempty"`
	ModifierName           string `json:"modifier_name,omitempty"`
	ModifierContactEmail   string `json:"modifier_contact_email,omitempty"`
	IsLocked               bool   `json:"is_locked,omitempty"`
	LockTime               int64  `json:"lock_time,omitempty"`
	IsFreezed              bool   `json:"is_freezed,omitempty"`
	LockOwner              string `json:"lock_owner,omitempty"`
	LockOwnerName          string `json:"lock_owner_name,omitempty"`
	LockOwnerContactEmail  string `json:"lock_owner_contact_email,omitempty"`
	LockedByMe             bool   `json:"locked_by_me,omitempty"`
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
		repos.POST("/dir", h.DirectoryOperation)
		repos.POST("/dir/", h.DirectoryOperation)
		repos.DELETE("/dir", h.DeleteDirectory)
		repos.DELETE("/dir/", h.DeleteDirectory)

		// File operations
		repos.GET("/file", h.GetFileInfo)
		repos.GET("/file/", h.GetFileInfo)
		repos.GET("/file/detail", h.GetFileDetail)
		repos.GET("/file/detail/", h.GetFileDetail)
		repos.POST("/file", h.FileOperation)
		repos.POST("/file/", h.FileOperation)
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

		// Resumable upload support
		repos.GET("/file-uploaded-bytes", h.GetFileUploadedBytes)
		repos.GET("/file-uploaded-bytes/", h.GetFileUploadedBytes)
	}

	// File revisions endpoint uses different path pattern: /api2/repo/file_revisions/:repo_id/
	// This is outside the /repos/:repo_id group
	repo := rg.Group("/repo")
	{
		repo.GET("/file_revisions/:repo_id", h.GetFileRevisions)
		repo.GET("/file_revisions/:repo_id/", h.GetFileRevisions)
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

	// Get starred files for this user and repo to check starred status
	userID := c.GetString("user_id")
	starredPaths := make(map[string]bool)
	if userID != "" {
		iter := h.db.Session().Query(`
			SELECT path FROM starred_files WHERE user_id = ? AND repo_id = ?
		`, userID, repoID).Iter()
		var starredPath string
		for iter.Scan(&starredPath) {
			starredPaths[starredPath] = true
		}
		iter.Close()
	}

	// Convert FSEntry to Dirent for API response
	direntList := make([]Dirent, 0, len(entries))
	for _, entry := range entries {
		// Determine type from mode
		fileType := "file"
		if entry.Mode&0170000 == 040000 || entry.Mode == ModeDir {
			fileType = "dir"
		}

		// Build full path for starred check
		var fullPath string
		if dirPath == "/" {
			fullPath = "/" + entry.Name
		} else {
			fullPath = dirPath + "/" + entry.Name
		}

		dirent := Dirent{
			ID:         entry.ID,
			Name:       entry.Name,
			Type:       fileType,
			Size:       entry.Size,
			MTime:      entry.MTime,
			Permission: "rw",
			ParentDir:  dirPath,
			Starred:    starredPaths[fullPath],
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

// DirectoryOperation handles directory operations (mkdir, rename)
// Seafile API: POST /api2/repos/:repo_id/dir/?p=/path&operation=mkdir|rename
func (h *FileHandler) DirectoryOperation(c *gin.Context) {
	operation := c.Query("operation")
	if operation == "" {
		// Default to mkdir for backward compatibility
		operation = "mkdir"
	}

	switch operation {
	case "mkdir":
		h.CreateDirectory(c)
	case "rename":
		h.RenameDirectory(c)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid operation"})
	}
}

// CreateDirectory creates a new directory
func (h *FileHandler) CreateDirectory(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")
	dirPath := c.Query("p")

	if dirPath == "" {
		dirPath = c.PostForm("p")
	}

	dirPath = normalizePath(dirPath)
	if dirPath == "/" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot create root directory"})
		return
	}

	fsHelper := NewFSHelper(h.db)

	// Get current head commit
	headCommitID, err := fsHelper.GetHeadCommitID(repoID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}

	// Get parent path and new directory name
	parentPath := path.Dir(dirPath)
	if parentPath == "." {
		parentPath = "/"
	}
	dirName := path.Base(dirPath)

	// Traverse to parent
	result, err := fsHelper.TraverseToPath(repoID, parentPath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	// Check if directory already exists
	for _, entry := range result.Entries {
		if entry.Name == dirName {
			c.JSON(http.StatusConflict, gin.H{"error": "directory already exists"})
			return
		}
	}

	// Create empty directory fs_object
	newDirFSID, err := fsHelper.CreateDirectoryFSObject(repoID, []FSEntry{})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create directory"})
		return
	}

	// Add new entry to parent
	newEntry := FSEntry{
		Name:  dirName,
		ID:    newDirFSID,
		Mode:  ModeDir,
		MTime: time.Now().Unix(),
	}
	newEntries := AddEntryToList(result.Entries, newEntry)

	// Create new fs_object for modified parent
	var newParentFSID string
	if parentPath == "/" {
		newParentFSID, err = fsHelper.CreateDirectoryFSObject(repoID, newEntries)
	} else {
		newParentFSID, err = fsHelper.CreateDirectoryFSObject(repoID, newEntries)
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update parent directory"})
		return
	}

	// Rebuild path to root
	newRootFSID, err := fsHelper.RebuildPathToRoot(repoID, result, newParentFSID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to rebuild path"})
		return
	}

	// Create new commit
	description := fmt.Sprintf("Added directory \"%s\"", dirName)
	newCommitID, err := fsHelper.CreateCommit(repoID, userID, newRootFSID, headCommitID, description)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create commit"})
		return
	}

	// Update library head
	if err := fsHelper.UpdateLibraryHead(orgID, repoID, newCommitID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update library"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"success":   true,
		"repo_id":   repoID,
		"path":      dirPath,
		"commit_id": newCommitID,
	})
}

// RenameDirectory renames a directory
func (h *FileHandler) RenameDirectory(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")
	dirPath := c.Query("p")
	newName := c.PostForm("newname")

	if dirPath == "" || dirPath == "/" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid path"})
		return
	}

	if newName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "newname is required"})
		return
	}

	dirPath = normalizePath(dirPath)

	fsHelper := NewFSHelper(h.db)

	// Get current head commit
	headCommitID, err := fsHelper.GetHeadCommitID(repoID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}

	// Traverse to the directory
	result, err := fsHelper.TraverseToPath(repoID, dirPath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	if result.TargetEntry == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "directory not found"})
		return
	}

	oldName := path.Base(dirPath)

	// Check if new name already exists
	for _, entry := range result.Entries {
		if entry.Name == newName {
			c.JSON(http.StatusConflict, gin.H{"error": "name already exists"})
			return
		}
	}

	// Update entry name
	newEntries := UpdateEntryInList(result.Entries, oldName, newName)

	// Create new fs_object for modified parent
	newParentFSID, err := fsHelper.CreateDirectoryFSObject(repoID, newEntries)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update directory"})
		return
	}

	// Rebuild path to root
	newRootFSID, err := fsHelper.RebuildPathToRoot(repoID, result, newParentFSID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to rebuild path"})
		return
	}

	// Create new commit
	description := fmt.Sprintf("Renamed \"%s\" to \"%s\"", oldName, newName)
	newCommitID, err := fsHelper.CreateCommit(repoID, userID, newRootFSID, headCommitID, description)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create commit"})
		return
	}

	// Update library head
	if err := fsHelper.UpdateLibraryHead(orgID, repoID, newCommitID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update library"})
		return
	}

	// Get directory info for response
	parentDir := path.Dir(dirPath)
	if parentDir == "" || parentDir == "." {
		parentDir = "/"
	}

	// Get the renamed entry info
	var mtime int64
	for _, entry := range result.Entries {
		if entry.Name == oldName {
			mtime = entry.MTime
			break
		}
	}

	// Return Seafile-compatible response
	c.JSON(http.StatusOK, gin.H{
		"type":       "dir",
		"repo_id":    repoID,
		"parent_dir": parentDir,
		"obj_name":   newName,
		"obj_id":     result.TargetEntry.ID,
		"mtime":      time.Unix(mtime, 0).UTC().Format("2006-01-02T15:04:05+00:00"),
	})
}

// FileOperation handles file operations (rename, create)
// Seafile API: POST /api2/repos/:repo_id/file/?p=/path&operation=rename|create
// Note: operation can be in query string OR in form body (frontend sends it in body)
func (h *FileHandler) FileOperation(c *gin.Context) {
	operation := c.Query("operation")
	if operation == "" {
		// Also check form body - frontend sends operation in POST body
		operation = c.PostForm("operation")
	}
	if operation == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "operation parameter is required"})
		return
	}

	switch operation {
	case "rename":
		h.RenameFile(c)
	case "create":
		h.CreateFile(c)
	case "move":
		h.MoveFile(c)
	case "copy":
		h.CopyFile(c)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid operation"})
	}
}

// RenameFile renames a file
func (h *FileHandler) RenameFile(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")
	filePath := c.Query("p")
	newName := c.PostForm("newname")

	if filePath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is required"})
		return
	}

	if newName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "newname is required"})
		return
	}

	filePath = normalizePath(filePath)
	if filePath == "/" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot rename root"})
		return
	}

	fsHelper := NewFSHelper(h.db)

	// Get current head commit
	headCommitID, err := fsHelper.GetHeadCommitID(repoID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}

	// Traverse to the file
	result, err := fsHelper.TraverseToPath(repoID, filePath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	if result.TargetEntry == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		return
	}

	oldName := path.Base(filePath)

	// Check if new name already exists
	for _, entry := range result.Entries {
		if entry.Name == newName {
			c.JSON(http.StatusConflict, gin.H{"error": "name already exists"})
			return
		}
	}

	// Update entry name
	newEntries := UpdateEntryInList(result.Entries, oldName, newName)

	// Create new fs_object for modified parent
	newParentFSID, err := fsHelper.CreateDirectoryFSObject(repoID, newEntries)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update directory"})
		return
	}

	// Rebuild path to root
	newRootFSID, err := fsHelper.RebuildPathToRoot(repoID, result, newParentFSID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to rebuild path"})
		return
	}

	// Create new commit
	description := fmt.Sprintf("Renamed \"%s\" to \"%s\"", oldName, newName)
	newCommitID, err := fsHelper.CreateCommit(repoID, userID, newRootFSID, headCommitID, description)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create commit"})
		return
	}

	// Update library head
	if err := fsHelper.UpdateLibraryHead(orgID, repoID, newCommitID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update library"})
		return
	}

	// Get file info for response
	parentDir := path.Dir(filePath)
	if parentDir == "" || parentDir == "." {
		parentDir = "/"
	}

	// Get the renamed entry info
	var fileSize int64
	var mtime int64
	for _, entry := range result.Entries {
		if entry.Name == oldName {
			fileSize = entry.Size
			mtime = entry.MTime
			break
		}
	}

	// Return Seafile-compatible response
	c.JSON(http.StatusOK, gin.H{
		"type":        "file",
		"repo_id":     repoID,
		"parent_dir":  parentDir,
		"obj_name":    newName,
		"obj_id":      result.TargetEntry.ID,
		"size":        fileSize,
		"mtime":       time.Unix(mtime, 0).UTC().Format("2006-01-02T15:04:05+00:00"),
		"is_locked":   false,
		"can_preview": false,
		"can_edit":    false,
	})
}

// CreateFile creates a new empty file
func (h *FileHandler) CreateFile(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")
	filePath := c.Query("p")

	if filePath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is required"})
		return
	}

	filePath = normalizePath(filePath)

	fsHelper := NewFSHelper(h.db)

	// Get current head commit
	headCommitID, err := fsHelper.GetHeadCommitID(repoID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}

	// Get parent path and file name
	parentPath := path.Dir(filePath)
	if parentPath == "." {
		parentPath = "/"
	}
	fileName := path.Base(filePath)

	// Traverse to parent
	result, err := fsHelper.TraverseToPath(repoID, parentPath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	// Check if file already exists
	for _, entry := range result.Entries {
		if entry.Name == fileName {
			c.JSON(http.StatusConflict, gin.H{"error": "file already exists"})
			return
		}
	}

	// Create empty file fs_object
	newFileFSID, err := fsHelper.CreateFileFSObject(repoID, fileName, 0, []string{})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create file"})
		return
	}

	// Add new entry to parent
	newEntry := FSEntry{
		Name:  fileName,
		ID:    newFileFSID,
		Mode:  ModeFile,
		MTime: time.Now().Unix(),
		Size:  0,
	}
	newEntries := AddEntryToList(result.Entries, newEntry)

	// Create new fs_object for modified parent
	newParentFSID, err := fsHelper.CreateDirectoryFSObject(repoID, newEntries)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update parent directory"})
		return
	}

	// Rebuild path to root
	newRootFSID, err := fsHelper.RebuildPathToRoot(repoID, result, newParentFSID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to rebuild path"})
		return
	}

	// Create new commit
	description := fmt.Sprintf("Added \"%s\"", fileName)
	newCommitID, err := fsHelper.CreateCommit(repoID, userID, newRootFSID, headCommitID, description)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create commit"})
		return
	}

	// Update library head
	if err := fsHelper.UpdateLibraryHead(orgID, repoID, newCommitID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update library"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"success":   true,
		"id":        newFileFSID,
		"name":      fileName,
		"size":      0,
		"commit_id": newCommitID,
	})
}

// DeleteDirectory deletes a directory
func (h *FileHandler) DeleteDirectory(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")
	dirPath := c.Query("p")

	if dirPath == "" || dirPath == "/" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid path"})
		return
	}

	dirPath = normalizePath(dirPath)

	fsHelper := NewFSHelper(h.db)

	// Get current head commit
	headCommitID, err := fsHelper.GetHeadCommitID(repoID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}

	// Traverse to the directory
	result, err := fsHelper.TraverseToPath(repoID, dirPath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	if result.TargetEntry == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "directory not found"})
		return
	}

	// Verify it's a directory
	if result.TargetEntry.Mode != ModeDir && result.TargetEntry.Mode&0170000 != 040000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is not a directory"})
		return
	}

	dirName := path.Base(dirPath)

	// Collect all block IDs in the directory tree (for ref count decrement)
	blockIDs, _ := fsHelper.CollectBlockIDsRecursive(repoID, result.TargetFSID)

	// Remove entry from parent directory
	newEntries := RemoveEntryFromList(result.Entries, dirName)

	// Create new fs_object for modified parent
	newParentFSID, err := fsHelper.CreateDirectoryFSObject(repoID, newEntries)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update directory"})
		return
	}

	// Rebuild path to root
	newRootFSID, err := fsHelper.RebuildPathToRoot(repoID, result, newParentFSID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to rebuild path"})
		return
	}

	// Create new commit
	description := fmt.Sprintf("Removed directory \"%s\"", dirName)
	newCommitID, err := fsHelper.CreateCommit(repoID, userID, newRootFSID, headCommitID, description)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create commit"})
		return
	}

	// Update library head
	if err := fsHelper.UpdateLibraryHead(orgID, repoID, newCommitID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update library"})
		return
	}

	// Decrement block ref counts in background
	go func() {
		if len(blockIDs) > 0 {
			fsHelper.DecrementBlockRefCounts(orgID, blockIDs)
		}
	}()

	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"commit_id": newCommitID,
	})
}

// GetFileInfo returns information about a file
// Implements: GET /api2/repos/:repo_id/file/?p=/path
func (h *FileHandler) GetFileInfo(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")
	filePath := c.Query("p")

	if filePath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is required"})
		return
	}

	filePath = normalizePath(filePath)

	fsHelper := NewFSHelper(h.db)

	// Traverse to the file
	result, err := fsHelper.TraverseToPath(repoID, filePath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	if result.TargetEntry == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		return
	}

	entry := result.TargetEntry
	isDir := entry.Mode == ModeDir || entry.Mode&0170000 == 040000
	fileType := "file"
	if isDir {
		fileType = "dir"
	}

	// Get library info for repo name
	var repoName string
	h.db.Session().Query(`
		SELECT name FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(&repoName)

	// Check if file is starred
	starred := false
	starredHandler := NewStarredHandler(h.db)
	starred = starredHandler.IsFileStarred(userID, repoID, filePath)

	c.JSON(http.StatusOK, gin.H{
		"id":          entry.ID,
		"type":        fileType,
		"name":        entry.Name,
		"size":        entry.Size,
		"mtime":       entry.MTime,
		"permission":  "rw",
		"starred":     starred,
		"repo_id":     repoID,
		"repo_name":   repoName,
		"parent_dir":  result.ParentPath,
	})
}

// GetFileDetail returns detailed information about a file
// Implements: GET /api2/repos/:repo_id/file/detail/?p=/path
func (h *FileHandler) GetFileDetail(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")
	filePath := c.Query("p")

	if filePath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is required"})
		return
	}

	filePath = normalizePath(filePath)

	fsHelper := NewFSHelper(h.db)

	// Traverse to the file
	result, err := fsHelper.TraverseToPath(repoID, filePath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	if result.TargetEntry == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		return
	}

	entry := result.TargetEntry
	isDir := entry.Mode == ModeDir || entry.Mode&0170000 == 040000
	fileType := "file"
	if isDir {
		fileType = "dir"
	}

	// Get library info
	var repoName, ownerID string
	var encrypted bool
	h.db.Session().Query(`
		SELECT name, owner_id, encrypted FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(&repoName, &ownerID, &encrypted)

	// Check if file is starred
	starred := false
	starredHandler := NewStarredHandler(h.db)
	starred = starredHandler.IsFileStarred(userID, repoID, filePath)

	// Build user email
	userEmail := userID + "@sesamefs.local"

	c.JSON(http.StatusOK, gin.H{
		"id":              entry.ID,
		"type":            fileType,
		"name":            entry.Name,
		"size":            entry.Size,
		"mtime":           entry.MTime,
		"permission":      "rw",
		"starred":         starred,
		"repo_id":         repoID,
		"repo_name":       repoName,
		"parent_dir":      result.ParentPath,
		"last_modifier_email": userEmail,
		"last_modifier_name": strings.Split(userEmail, "@")[0],
		"last_modifier_contact_email": userEmail,
		"can_preview":     true,
		"can_edit":        true,
		"encoded_thumbnail_src": "",
	})
}

// DeleteFile deletes a file
func (h *FileHandler) DeleteFile(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")
	filePath := c.Query("p")

	if filePath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is required"})
		return
	}

	filePath = normalizePath(filePath)
	if filePath == "/" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot delete root"})
		return
	}

	fsHelper := NewFSHelper(h.db)

	// Get current head commit
	headCommitID, err := fsHelper.GetHeadCommitID(repoID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}

	// Traverse to the file's parent
	result, err := fsHelper.TraverseToPath(repoID, filePath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	if result.TargetEntry == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		return
	}

	// Verify it's a file, not a directory
	if result.TargetEntry.Mode == ModeDir || result.TargetEntry.Mode&0170000 == 040000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is a directory, use DELETE /dir/ instead"})
		return
	}

	fileName := path.Base(filePath)

	// Remove entry from parent directory
	newEntries := RemoveEntryFromList(result.Entries, fileName)

	// Create new fs_object for modified parent
	newParentFSID, err := fsHelper.CreateDirectoryFSObject(repoID, newEntries)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update directory"})
		return
	}

	// Rebuild path to root
	newRootFSID, err := fsHelper.RebuildPathToRoot(repoID, result, newParentFSID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to rebuild path"})
		return
	}

	// Create new commit
	description := fmt.Sprintf("Deleted \"%s\"", fileName)
	newCommitID, err := fsHelper.CreateCommit(repoID, userID, newRootFSID, headCommitID, description)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create commit"})
		return
	}

	// Update library head
	if err := fsHelper.UpdateLibraryHead(orgID, repoID, newCommitID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update library"})
		return
	}

	// Optionally decrement block ref counts (could be deferred to GC)
	// This is done async to not block the response
	go func() {
		if result.TargetEntry != nil {
			blockIDs, _ := fsHelper.CollectBlockIDsRecursive(repoID, result.TargetFSID)
			if len(blockIDs) > 0 {
				fsHelper.DecrementBlockRefCounts(orgID, blockIDs)
			}
		}
	}()

	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"commit_id": newCommitID,
	})
}

// MoveFileRequest represents the request for moving a file
type MoveFileRequest struct {
	SrcRepoID  string `json:"src_repo_id" form:"src_repo_id"`
	SrcPath    string `json:"src_path" form:"src_path"`
	DstRepoID  string `json:"dst_repo_id" form:"dst_repo_id"`
	DstPath    string `json:"dst_dir" form:"dst_dir"`
	// Legacy format
	SrcDir string `json:"src_dir" form:"src_dir"`
	DstDir string `json:"dst_dir" form:"dst_dir"`
	Filename string `json:"filename" form:"filename"`
}

// MoveFile moves a file to a new location
// Supports both same-repo and cross-repo moves
func (h *FileHandler) MoveFile(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	var req MoveFileRequest
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Handle different request formats
	srcRepoID := req.SrcRepoID
	if srcRepoID == "" {
		srcRepoID = repoID
	}
	dstRepoID := req.DstRepoID
	if dstRepoID == "" {
		dstRepoID = repoID
	}

	// Build source and destination paths
	srcPath := req.SrcPath
	if srcPath == "" && req.SrcDir != "" && req.Filename != "" {
		srcPath = path.Join(req.SrcDir, req.Filename)
	}
	dstDir := req.DstPath
	if dstDir == "" {
		dstDir = req.DstDir
	}

	if srcPath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "source path is required"})
		return
	}
	if dstDir == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "destination directory is required"})
		return
	}

	srcPath = normalizePath(srcPath)
	dstDir = normalizePath(dstDir)

	// Cross-repo move not yet implemented
	if srcRepoID != dstRepoID {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "cross-repo move not yet implemented"})
		return
	}

	fsHelper := NewFSHelper(h.db)

	// Get current head commit
	headCommitID, err := fsHelper.GetHeadCommitID(repoID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}

	// Traverse to source file
	srcResult, err := fsHelper.TraverseToPath(repoID, srcPath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "source not found: " + err.Error()})
		return
	}
	if srcResult.TargetEntry == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "source file not found"})
		return
	}

	// Get destination directory
	dstResult, err := fsHelper.TraverseToPath(repoID, dstDir)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "destination not found: " + err.Error()})
		return
	}

	// Get entries OF the destination directory (not its parent)
	dstDirEntries, err := fsHelper.GetDirectoryEntries(repoID, dstResult.TargetFSID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "destination directory not found: " + err.Error()})
		return
	}

	fileName := path.Base(srcPath)

	// Check if name already exists at destination
	for _, entry := range dstDirEntries {
		if entry.Name == fileName {
			c.JSON(http.StatusConflict, gin.H{"error": "file already exists at destination"})
			return
		}
	}

	// Step 1: Remove from source parent
	srcParentEntries := RemoveEntryFromList(srcResult.Entries, fileName)
	newSrcParentFSID, err := fsHelper.CreateDirectoryFSObject(repoID, srcParentEntries)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update source directory"})
		return
	}

	// Step 2: Add to destination directory
	movedEntry := *srcResult.TargetEntry
	movedEntry.MTime = time.Now().Unix()
	dstNewEntries := AddEntryToList(dstDirEntries, movedEntry)

	// Step 3: Create the new destination fs_object
	newDstFSID, err := fsHelper.CreateDirectoryFSObject(repoID, dstNewEntries)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update destination directory"})
		return
	}

	// Step 4: Apply both changes and rebuild paths to root
	// We have two changes:
	// 1. Source parent: file removed (newSrcParentFSID)
	// 2. Destination directory: file added (newDstFSID)

	// For simplicity, apply source change first, then destination change
	var newRootFSID string

	if srcResult.ParentPath == "/" && dstDir == "/" {
		// Both source and destination are root - shouldn't happen in move
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid move within root"})
		return
	} else if srcResult.ParentPath == "/" {
		// Source is root, destination is subdirectory
		// Start with source change (root = newSrcParentFSID)
		// Then update destination path
		// Find dstDir in the new root and update it
		srcRootEntries, err := fsHelper.GetDirectoryEntries(repoID, newSrcParentFSID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get source root entries"})
			return
		}
		// Update the destination directory reference in root
		dstDirName := strings.TrimPrefix(dstDir, "/")
		dstDirParts := strings.Split(dstDirName, "/")
		dstTopLevelName := dstDirParts[0]
		for i := range srcRootEntries {
			if srcRootEntries[i].Name == dstTopLevelName {
				if len(dstDirParts) == 1 {
					// dstDir is a top-level directory
					srcRootEntries[i].ID = newDstFSID
				}
				// TODO: Handle nested destination directories
				break
			}
		}
		newRootFSID, err = fsHelper.CreateDirectoryFSObject(repoID, srcRootEntries)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update root"})
			return
		}
	} else if dstDir == "/" {
		// Destination is root - add moved entry to root after applying source change
		newRootFSID, err = fsHelper.RebuildPathToRoot(repoID, srcResult, newSrcParentFSID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to rebuild source path"})
			return
		}
		rootEntries, err := fsHelper.GetDirectoryEntries(repoID, newRootFSID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get root entries"})
			return
		}
		rootEntries = AddEntryToList(rootEntries, movedEntry)
		newRootFSID, err = fsHelper.CreateDirectoryFSObject(repoID, rootEntries)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update root"})
			return
		}
	} else {
		// Both are subdirectories - apply source change first
		newRootFSID, err = fsHelper.RebuildPathToRoot(repoID, srcResult, newSrcParentFSID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to rebuild source path"})
			return
		}
		// Re-traverse to destination and update it with the new fs_id
		// Note: This is a simplified approach - for deeply nested paths we would need
		// a more sophisticated tree update algorithm
		dstResult2, err := fsHelper.TraverseToPath(repoID, dstDir)
		if err == nil {
			// Update the parent's reference to point to new destination
			dstDirName := path.Base(dstDir)
			parentEntries := make([]FSEntry, len(dstResult2.Entries))
			copy(parentEntries, dstResult2.Entries)
			for i := range parentEntries {
				if parentEntries[i].Name == dstDirName {
					parentEntries[i].ID = newDstFSID
					break
				}
			}
			newParentFSID, err := fsHelper.CreateDirectoryFSObject(repoID, parentEntries)
			if err == nil {
				newRootFSID, _ = fsHelper.RebuildPathToRoot(repoID, dstResult2, newParentFSID)
			}
		}
	}

	// Create new commit
	description := fmt.Sprintf("Moved \"%s\" to \"%s\"", fileName, dstDir)
	newCommitID, err := fsHelper.CreateCommit(repoID, userID, newRootFSID, headCommitID, description)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create commit"})
		return
	}

	// Update library head
	if err := fsHelper.UpdateLibraryHead(orgID, repoID, newCommitID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update library"})
		return
	}

	// Return Seafile-compatible response
	// Seafile returns HTTP 301 for moves but we use 200 for API compatibility
	c.JSON(http.StatusOK, gin.H{
		"repo_id":    dstRepoID,
		"parent_dir": dstDir,
		"obj_name":   fileName,
	})
}

// CopyFileRequest represents the request for copying a file
type CopyFileRequest struct {
	SrcRepoID  string `json:"src_repo_id" form:"src_repo_id"`
	SrcPath    string `json:"src_path" form:"src_path"`
	DstRepoID  string `json:"dst_repo_id" form:"dst_repo_id"`
	DstPath    string `json:"dst_dir" form:"dst_dir"`
	// Legacy format
	SrcDir string `json:"src_dir" form:"src_dir"`
	DstDir string `json:"dst_dir" form:"dst_dir"`
	Filename string `json:"filename" form:"filename"`
}

// CopyFile copies a file to a new location
// Supports both same-repo and cross-repo copies
func (h *FileHandler) CopyFile(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	var req CopyFileRequest
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Handle different request formats
	srcRepoID := req.SrcRepoID
	if srcRepoID == "" {
		srcRepoID = repoID
	}
	dstRepoID := req.DstRepoID
	if dstRepoID == "" {
		dstRepoID = repoID
	}

	// Build source and destination paths
	srcPath := req.SrcPath
	if srcPath == "" && req.SrcDir != "" && req.Filename != "" {
		srcPath = path.Join(req.SrcDir, req.Filename)
	}
	dstDir := req.DstPath
	if dstDir == "" {
		dstDir = req.DstDir
	}

	if srcPath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "source path is required"})
		return
	}
	if dstDir == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "destination directory is required"})
		return
	}

	srcPath = normalizePath(srcPath)
	dstDir = normalizePath(dstDir)

	// Cross-repo copy not yet implemented
	if srcRepoID != dstRepoID {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "cross-repo copy not yet implemented"})
		return
	}

	fsHelper := NewFSHelper(h.db)

	// Get current head commit
	headCommitID, err := fsHelper.GetHeadCommitID(repoID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}

	// Traverse to source file
	srcResult, err := fsHelper.TraverseToPath(repoID, srcPath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "source not found: " + err.Error()})
		return
	}
	if srcResult.TargetEntry == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "source file not found"})
		return
	}

	// Get destination directory
	dstResult, err := fsHelper.TraverseToPath(repoID, dstDir)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "destination not found: " + err.Error()})
		return
	}

	// Get entries OF the destination directory (not its parent)
	dstDirEntries, err := fsHelper.GetDirectoryEntries(repoID, dstResult.TargetFSID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "destination directory not found: " + err.Error()})
		return
	}

	fileName := path.Base(srcPath)

	// Check if name already exists at destination
	for _, entry := range dstDirEntries {
		if entry.Name == fileName {
			c.JSON(http.StatusConflict, gin.H{"error": "file already exists at destination"})
			return
		}
	}

	// Create copy entry (same fs_id, same blocks)
	copiedEntry := *srcResult.TargetEntry
	copiedEntry.MTime = time.Now().Unix()

	// Add to destination directory
	dstNewEntries := AddEntryToList(dstDirEntries, copiedEntry)
	newDstFSID, err := fsHelper.CreateDirectoryFSObject(repoID, dstNewEntries)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update destination directory"})
		return
	}

	// Update parent to point to the new destination directory
	dstDirName := path.Base(dstDir)
	parentEntries := make([]FSEntry, len(dstResult.Entries))
	copy(parentEntries, dstResult.Entries)
	for i := range parentEntries {
		if parentEntries[i].Name == dstDirName {
			parentEntries[i].ID = newDstFSID
			break
		}
	}
	newParentFSID, err := fsHelper.CreateDirectoryFSObject(repoID, parentEntries)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update parent directory"})
		return
	}

	// Rebuild path from parent to root
	newRootFSID, err := fsHelper.RebuildPathToRoot(repoID, dstResult, newParentFSID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to rebuild path"})
		return
	}

	// Create new commit
	description := fmt.Sprintf("Copied \"%s\" to \"%s\"", fileName, dstDir)
	newCommitID, err := fsHelper.CreateCommit(repoID, userID, newRootFSID, headCommitID, description)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create commit"})
		return
	}

	// Update library head
	if err := fsHelper.UpdateLibraryHead(orgID, repoID, newCommitID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update library"})
		return
	}

	// Increment block ref counts in background (for proper dedup)
	go func() {
		blockIDs, _ := fsHelper.CollectBlockIDsRecursive(repoID, srcResult.TargetFSID)
		if len(blockIDs) > 0 {
			fsHelper.IncrementBlockRefCounts(orgID, blockIDs)
		}
	}()

	// Return Seafile-compatible response
	c.JSON(http.StatusOK, gin.H{
		"repo_id":    dstRepoID,
		"parent_dir": dstDir,
		"obj_name":   fileName,
	})
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

// V21DirectoryResponse represents the v2.1 API response format for directory listing
type V21DirectoryResponse struct {
	UserPerm   string   `json:"user_perm"`
	DirID      string   `json:"dir_id"`
	DirentList []Dirent `json:"dirent_list"`
}

// ListDirectoryV21 returns directory contents in v2.1 API format
// Implements Seafile API: GET /api/v2.1/repos/:repo_id/dir/?p=/path
func (h *FileHandler) ListDirectoryV21(c *gin.Context) {
	repoID := c.Param("repo_id")
	dirPath := c.DefaultQuery("p", "/")
	orgID := c.GetString("org_id")

	// Normalize path
	dirPath = normalizePath(dirPath)

	// Check if database is available
	if h.db == nil {
		c.JSON(http.StatusOK, V21DirectoryResponse{
			UserPerm:   "rw",
			DirID:      "",
			DirentList: []Dirent{},
		})
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
		c.JSON(http.StatusOK, V21DirectoryResponse{
			UserPerm:   "rw",
			DirID:      "",
			DirentList: []Dirent{},
		})
		return
	}

	// Get root_fs_id from the head commit
	var rootFSID string
	err = h.db.Session().Query(`
		SELECT root_fs_id FROM commits
		WHERE library_id = ? AND commit_id = ?
	`, repoID, headCommitID).Scan(&rootFSID)
	if err != nil {
		log.Printf("ListDirectoryV21: failed to get commit %s: %v", headCommitID, err)
		c.JSON(http.StatusOK, V21DirectoryResponse{
			UserPerm:   "rw",
			DirID:      "",
			DirentList: []Dirent{},
		})
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
				log.Printf("ListDirectoryV21: failed to get fs_object %s: %v", currentFSID, err)
				c.JSON(http.StatusNotFound, gin.H{"error": "Folder does not exist."})
				return
			}

			// Parse entries and find the next component
			var entries []FSEntry
			if entriesJSON != "" && entriesJSON != "[]" {
				if err := json.Unmarshal([]byte(entriesJSON), &entries); err != nil {
					log.Printf("ListDirectoryV21: failed to parse entries for %s: %v", currentFSID, err)
					c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid directory data"})
					return
				}
			}

			// Find the child directory
			found := false
			for _, entry := range entries {
				if entry.Name == part {
					if entry.Mode&0170000 == 040000 || entry.Mode == ModeDir {
						currentFSID = entry.ID
						found = true
						break
					} else {
						c.JSON(http.StatusBadRequest, gin.H{"error": "path is not a directory"})
						return
					}
				}
			}

			if !found {
				c.JSON(http.StatusNotFound, gin.H{"error": "Folder does not exist."})
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
		log.Printf("ListDirectoryV21: failed to get target fs_object %s: %v", currentFSID, err)
		c.JSON(http.StatusOK, V21DirectoryResponse{
			UserPerm:   "rw",
			DirID:      currentFSID,
			DirentList: []Dirent{},
		})
		return
	}

	// Parse entries
	var entries []FSEntry
	if entriesJSON != "" && entriesJSON != "[]" {
		if err := json.Unmarshal([]byte(entriesJSON), &entries); err != nil {
			log.Printf("ListDirectoryV21: failed to parse target entries for %s: %v", currentFSID, err)
			c.JSON(http.StatusOK, V21DirectoryResponse{
				UserPerm:   "rw",
				DirID:      currentFSID,
				DirentList: []Dirent{},
			})
			return
		}
	}

	// Get starred files for this user and repo to check starred status
	userID := c.GetString("user_id")
	starredPaths := make(map[string]bool)
	if userID != "" {
		iter := h.db.Session().Query(`
			SELECT path FROM starred_files WHERE user_id = ? AND repo_id = ?
		`, userID, repoID).Iter()
		var starredPath string
		for iter.Scan(&starredPath) {
			starredPaths[starredPath] = true
		}
		iter.Close()
	}

	// Get locked files for this repo
	type lockInfo struct {
		LockedBy string
		LockedAt time.Time
	}
	lockedFiles := make(map[string]lockInfo)
	lockIter := h.db.Session().Query(`
		SELECT path, locked_by, locked_at FROM locked_files WHERE repo_id = ?
	`, repoID).Iter()
	var lockPath, lockedBy string
	var lockedAt time.Time
	for lockIter.Scan(&lockPath, &lockedBy, &lockedAt) {
		lockedFiles[lockPath] = lockInfo{LockedBy: lockedBy, LockedAt: lockedAt}
	}
	lockIter.Close()

	// Convert FSEntry to Dirent for API response (v2.1 format)
	direntList := make([]Dirent, 0, len(entries))
	for _, entry := range entries {
		// Determine type from mode
		fileType := "file"
		if entry.Mode&0170000 == 040000 || entry.Mode == ModeDir {
			fileType = "dir"
		}

		// Build full path for starred check
		var fullPath string
		if dirPath == "/" {
			fullPath = "/" + entry.Name
		} else {
			fullPath = dirPath + "/" + entry.Name
		}

		// Check if this file is starred
		isStarred := starredPaths[fullPath]

		dirent := Dirent{
			ID:         entry.ID,
			Name:       entry.Name,
			Type:       fileType,
			Size:       entry.Size,
			MTime:      entry.MTime,
			Permission: "rw",
			ParentDir:  dirPath,
			Starred:    isStarred,
		}

		// Add modifier if available
		if entry.Modifier != "" {
			dirent.ModifierEmail = entry.Modifier
			dirent.ModifierName = strings.Split(entry.Modifier, "@")[0]
			dirent.ModifierContactEmail = entry.Modifier
		}

		// Add file-specific fields
		if fileType == "file" {
			// Check if file is locked
			if lock, isLocked := lockedFiles[fullPath]; isLocked {
				dirent.IsLocked = true
				dirent.LockTime = lock.LockedAt.Unix()
				dirent.LockOwner = lock.LockedBy
				dirent.LockOwnerName = strings.Split(lock.LockedBy, "@")[0]
				dirent.LockOwnerContactEmail = lock.LockedBy
				dirent.LockedByMe = (lock.LockedBy == userID)
			} else {
				dirent.IsLocked = false
				dirent.LockTime = 0
				dirent.LockOwner = ""
				dirent.LockOwnerName = ""
				dirent.LockOwnerContactEmail = ""
				dirent.LockedByMe = false
			}
			dirent.IsFreezed = false
		}

		direntList = append(direntList, dirent)
	}

	// Return v2.1 format response
	c.JSON(http.StatusOK, V21DirectoryResponse{
		UserPerm:   "rw",
		DirID:      currentFSID,
		DirentList: direntList,
	})
}

// FileLockRequest represents the request for locking/unlocking a file
type FileLockRequest struct {
	Operation string `json:"operation" form:"operation"` // lock or unlock
}

// LockFile handles file lock/unlock operations
// Implements: PUT /api/v2.1/repos/:repo_id/file/?p=/path
func (h *FileHandler) LockFile(c *gin.Context) {
	repoID := c.Param("repo_id")
	filePath := c.Query("p")
	userID := c.GetString("user_id")

	if filePath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is required"})
		return
	}

	// Normalize path
	filePath = normalizePath(filePath)

	var req FileLockRequest
	if err := c.ShouldBind(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	switch req.Operation {
	case "lock":
		// Store lock in database
		lockTime := time.Now()
		if err := h.db.Session().Query(`
			INSERT INTO locked_files (repo_id, path, locked_by, locked_at)
			VALUES (?, ?, ?, ?)
		`, repoID, filePath, userID, lockTime).Exec(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to lock file"})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"success":    true,
			"repo_id":    repoID,
			"path":       filePath,
			"is_locked":  true,
			"lock_time":  lockTime.Unix(),
			"lock_owner": userID,
		})
	case "unlock":
		// Remove lock from database
		if err := h.db.Session().Query(`
			DELETE FROM locked_files WHERE repo_id = ? AND path = ?
		`, repoID, filePath).Exec(); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to unlock file"})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"success":   true,
			"repo_id":   repoID,
			"path":      filePath,
			"is_locked": false,
		})
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "operation must be 'lock' or 'unlock'"})
	}
}

// FileRevision represents a file revision in API response
type FileRevision struct {
	CommitID      string `json:"commit_id"`
	RevFileID     string `json:"rev_file_id"`
	CTime         int64  `json:"ctime"`
	Description   string `json:"description"`
	Size          int64  `json:"size"`
	RevRenamedOld string `json:"rev_renamed_old_path,omitempty"`
	CreatorName   string `json:"creator_name"`
	CreatorEmail  string `json:"creator_email"`
}

// GetFileRevisions returns the revision history of a file
// Implements: GET /api2/repo/file_revisions/:repo_id/
func (h *FileHandler) GetFileRevisions(c *gin.Context) {
	repoID := c.Param("repo_id")
	filePath := c.Query("p")
	orgID := c.GetString("org_id")

	if filePath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path is required"})
		return
	}

	if h.db == nil {
		c.JSON(http.StatusOK, gin.H{"data": []FileRevision{}})
		return
	}

	// Query commits for this library
	iter := h.db.Session().Query(`
		SELECT commit_id, root_fs_id, creator_id, description, created_at
		FROM commits WHERE library_id = ?
		LIMIT 50
	`, repoID).Iter()

	var revisions []FileRevision
	var commitID, rootFSID, creatorID, description string
	var createdAt time.Time

	fsHelper := NewFSHelper(h.db)

	for iter.Scan(&commitID, &rootFSID, &creatorID, &description, &createdAt) {
		// Check if file exists in this commit
		// For simplicity, we just list all commits that mention the file
		// A full implementation would traverse the tree for each commit
		result, err := fsHelper.TraverseToPath(repoID, filePath)
		if err != nil || result.TargetEntry == nil {
			continue
		}

		revisions = append(revisions, FileRevision{
			CommitID:     commitID,
			RevFileID:    result.TargetEntry.ID,
			CTime:        createdAt.Unix(),
			Description:  description,
			Size:         result.TargetEntry.Size,
			CreatorName:  creatorID,
			CreatorEmail: creatorID + "@sesamefs.local",
		})
	}
	iter.Close()

	// Get library info for response
	var libName string
	h.db.Session().Query(`
		SELECT name FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(&libName)

	c.JSON(http.StatusOK, gin.H{
		"data":        revisions,
		"repo_name":   libName,
		"repo_id":     repoID,
		"file_path":   filePath,
		"next_start":  0,
		"total_count": len(revisions),
	})
}

// GetFileUploadedBytes returns the number of bytes already uploaded for resumable uploads
// Implements: GET /api/v2.1/repos/:repo_id/file-uploaded-bytes/?parent_dir=/&file_name=xxx
func (h *FileHandler) GetFileUploadedBytes(c *gin.Context) {
	// For resumable uploads, this endpoint returns how many bytes have been uploaded
	// For now, return 0 to indicate no bytes uploaded (start fresh)
	// A full implementation would track partial uploads in the database

	c.JSON(http.StatusOK, gin.H{
		"uploadedBytes": 0,
	})
}
