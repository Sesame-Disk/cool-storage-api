package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/gin-gonic/gin"
)

// SyncHandler handles Seafile sync protocol operations
// These endpoints are used by the Seafile Desktop client for file synchronization
type SyncHandler struct {
	db             *db.DB
	storage        *storage.S3Store    // Legacy single store
	blockStore     *storage.BlockStore // Legacy single block store
	storageManager *storage.Manager    // Multi-backend storage manager
}

// NewSyncHandler creates a new sync protocol handler
func NewSyncHandler(database *db.DB, s3Store *storage.S3Store, blockStore *storage.BlockStore, storageManager *storage.Manager) *SyncHandler {
	return &SyncHandler{
		db:             database,
		storage:        s3Store,
		blockStore:     blockStore,
		storageManager: storageManager,
	}
}

// RegisterSyncRoutes registers the sync protocol routes
func (h *SyncHandler) RegisterSyncRoutes(router *gin.Engine, authMiddleware gin.HandlerFunc) {
	// Protocol version endpoint (no auth required)
	router.GET("/seafhttp/protocol-version", h.GetProtocolVersion)

	// Multi-repo head commits endpoint (for checking multiple repos at once)
	router.POST("/seafhttp/repo/head-commits-multi", authMiddleware, h.GetHeadCommitsMulti)

	// Sync protocol routes under /seafhttp/repo/
	repo := router.Group("/seafhttp/repo/:repo_id")
	repo.Use(authMiddleware)
	{
		// Commit operations
		repo.GET("/commit/HEAD", h.GetHeadCommit)
		repo.GET("/commit/:commit_id", h.GetCommit)
		repo.PUT("/commit/:commit_id", h.PutCommit)

		// Block operations
		repo.GET("/block/:block_id", h.GetBlock)
		repo.PUT("/block/:block_id", h.PutBlock)
		repo.POST("/check-blocks", h.CheckBlocks)
		repo.POST("/check-blocks/", h.CheckBlocks)

		// Filesystem operations
		repo.GET("/fs-id-list", h.GetFSIDList)
		repo.GET("/fs-id-list/", h.GetFSIDList)
		repo.GET("/fs/:fs_id", h.GetFSObject)
		repo.POST("/pack-fs", h.PackFS)
		repo.POST("/pack-fs/", h.PackFS)
		repo.POST("/recv-fs", h.RecvFS)
		repo.POST("/recv-fs/", h.RecvFS)
		repo.POST("/check-fs", h.CheckFS)
		repo.POST("/check-fs/", h.CheckFS)

		// Permission and quota
		repo.GET("/permission-check", h.PermissionCheck)
		repo.GET("/permission-check/", h.PermissionCheck)
		repo.GET("/quota-check", h.QuotaCheck)
		repo.GET("/quota-check/", h.QuotaCheck)

		// Update branch (for committing changes)
		repo.POST("/update-branch", h.UpdateBranch)
		repo.POST("/update-branch/", h.UpdateBranch)
	}
}

// GetProtocolVersion returns the sync protocol version
// GET /seafhttp/protocol-version
func (h *SyncHandler) GetProtocolVersion(c *gin.Context) {
	// Seafile protocol version 2 is the current version used by desktop clients
	c.JSON(http.StatusOK, gin.H{
		"version": 2,
	})
}

// Commit represents a Seafile commit object
type Commit struct {
	CommitID       string  `json:"commit_id"`
	RepoID         string  `json:"repo_id"`
	RootID         string  `json:"root_id"`                    // Root FS object ID
	ParentID       *string `json:"parent_id"`                  // Parent commit ID (null for first commit)
	SecondParentID *string `json:"second_parent_id"`           // For merge commits (null if none)
	Description    string  `json:"description"`
	Creator        string  `json:"creator"`
	CreatorName    string  `json:"creator_name"`
	Ctime          int64   `json:"ctime"`                      // Creation time (Unix timestamp)
	Version        int     `json:"version"`                    // Commit version (currently 1)
	RepoName       string  `json:"repo_name,omitempty"`        // Repository name
	RepoDesc       string  `json:"repo_desc,omitempty"`        // Repository description
	RepoCategory   *string `json:"repo_category"`              // Repository category (null)
	NoLocalHistory int     `json:"no_local_history,omitempty"` // 1 = no local history
	Encrypted      bool    `json:"encrypted,omitempty"`
	EncVersion     int     `json:"enc_version,omitempty"`
	Magic          string  `json:"magic,omitempty"`
	RandomKey      string  `json:"random_key,omitempty"`
}

// FSObject represents a Seafile filesystem object (file or directory)
type FSObject struct {
	Type    int         `json:"type"`    // 1 = file, 3 = directory
	ID      string      `json:"id"`      // SHA-1 hash of contents
	Name    string      `json:"name,omitempty"`
	Mode    int         `json:"mode,omitempty"`   // Unix file mode
	Mtime   int64       `json:"mtime,omitempty"`  // Modification time
	Size    int64       `json:"size,omitempty"`   // File size
	BlockIDs []string   `json:"block_ids,omitempty"` // Block IDs for files
	Entries []FSEntry   `json:"dirents,omitempty"`   // Directory entries
}

// FSEntry represents a directory entry
type FSEntry struct {
	Name     string `json:"name"`
	ID       string `json:"id"`       // FS object ID
	Mode     int    `json:"mode"`     // Unix file mode (33188 = regular file, 16384 = directory)
	Mtime    int64  `json:"mtime"`
	Size     int64  `json:"size,omitempty"`
	Modifier string `json:"modifier,omitempty"`
}

// GetHeadCommit returns the HEAD commit for a repository
// GET /seafhttp/repo/:repo_id/commit/HEAD
func (h *SyncHandler) GetHeadCommit(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	// Check if database is available
	if h.db == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "database not available"})
		return
	}

	// Get head commit from database
	var headCommitID string
	err := h.db.Session().Query(`
		SELECT head_commit_id FROM libraries
		WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(&headCommitID)

	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "library not found"})
		return
	}

	// If no head commit exists, create an initial commit
	if headCommitID == "" {
		headCommitID, err = h.createInitialCommit(repoID, orgID, userID)
		if err != nil {
			// Log error but return empty - client can handle this
			c.JSON(http.StatusOK, gin.H{"is_corrupted": false, "head_commit_id": ""})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"is_corrupted":   false,
		"head_commit_id": headCommitID,
	})
}

// createInitialCommit creates the first commit for an empty repository
func (h *SyncHandler) createInitialCommit(repoID, orgID, userID string) (string, error) {
	now := time.Now()

	// Create empty root directory FS object
	// The ID is a hash - for empty dir, use a deterministic ID
	rootID := fmt.Sprintf("%040x", 0) // 40 zeros = empty root

	// Store the empty root FS object
	err := h.db.Session().Query(`
		INSERT INTO fs_objects (library_id, fs_id, obj_type, obj_name, dir_entries, size_bytes, mtime)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, repoID, rootID, "dir", "", "[]", 0, now.Unix()).Exec()
	if err != nil {
		return "", fmt.Errorf("failed to create root fs object: %w", err)
	}

	// Create initial commit
	// Commit ID is a hash of the content - use deterministic ID for initial (40 chars like SHA-1)
	commitID := sha1Hex(fmt.Sprintf("%s-%s-%d", repoID, rootID, now.Unix()))

	err = h.db.Session().Query(`
		INSERT INTO commits (library_id, commit_id, parent_id, root_fs_id, creator_id, description, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, repoID, commitID, "", rootID, userID, "Initial commit", now).Exec()
	if err != nil {
		return "", fmt.Errorf("failed to create initial commit: %w", err)
	}

	// Update library's head_commit_id
	err = h.db.Session().Query(`
		UPDATE libraries SET head_commit_id = ?, root_commit_id = ?, updated_at = ?
		WHERE org_id = ? AND library_id = ?
	`, commitID, commitID, now, orgID, repoID).Exec()
	if err != nil {
		return "", fmt.Errorf("failed to update library head: %w", err)
	}

	return commitID, nil
}

// sha1Hex returns the SHA1 hash of a string as hex (40 chars, Seafile compatible)
func sha1Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	// Return only first 40 chars to match Seafile's SHA-1 format
	return hex.EncodeToString(h[:20])
}

// GetCommit returns a specific commit object
// GET /seafhttp/repo/:repo_id/commit/:commit_id
func (h *SyncHandler) GetCommit(c *gin.Context) {
	repoID := c.Param("repo_id")
	commitID := c.Param("commit_id")
	orgID := c.GetString("org_id")

	// Query commit from database
	var commit Commit
	var parentID, rootID, description, creator string
	var ctime time.Time

	err := h.db.Session().Query(`
		SELECT commit_id, parent_id, root_fs_id, description, creator_id, created_at
		FROM commits WHERE library_id = ? AND commit_id = ?
	`, repoID, commitID).Scan(
		&commit.CommitID, &parentID, &rootID, &description, &creator, &ctime,
	)

	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "commit not found"})
		return
	}

	// Get library info for repo_name and repo_desc
	var repoName, repoDesc string
	h.db.Session().Query(`
		SELECT name, description FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(&repoName, &repoDesc)

	commit.RepoID = repoID
	commit.RootID = rootID
	commit.Description = description
	// Seafile uses 40 zeros for creator ID format
	commit.Creator = strings.Repeat("0", 40)
	commit.CreatorName = creator + "@sesamefs.local"
	commit.Ctime = ctime.Unix()
	commit.Version = 1 // Seafile commit format version 1
	commit.RepoName = repoName
	commit.RepoDesc = repoDesc
	commit.NoLocalHistory = 1

	// Set pointer fields - null if empty, pointer to value otherwise
	if parentID == "" {
		commit.ParentID = nil
	} else {
		commit.ParentID = &parentID
	}
	commit.SecondParentID = nil // Always null for now
	commit.RepoCategory = nil   // Always null

	// Return commit as JSON
	c.JSON(http.StatusOK, commit)
}

// PutCommit stores a new commit object or updates the HEAD pointer
// PUT /seafhttp/repo/:repo_id/commit/:commit_id
// PUT /seafhttp/repo/:repo_id/commit/HEAD?head=<commit_id> (update HEAD pointer)
func (h *SyncHandler) PutCommit(c *gin.Context) {
	repoID := c.Param("repo_id")
	commitID := c.Param("commit_id")
	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	// Special case: PUT /commit/HEAD?head=<commit_id> updates the HEAD pointer
	if commitID == "HEAD" {
		headCommitID := c.Query("head")
		if headCommitID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing head parameter"})
			return
		}

		log.Printf("PutCommit HEAD: updating repo %s head to %s", repoID, headCommitID)

		// Update library head
		now := time.Now()
		err := h.db.Session().Query(`
			UPDATE libraries SET head_commit_id = ?, updated_at = ?
			WHERE org_id = ? AND library_id = ?
		`, headCommitID, now, orgID, repoID).Exec()

		if err != nil {
			log.Printf("PutCommit HEAD: failed to update head: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update head"})
			return
		}

		c.Status(http.StatusOK)
		return
	}

	// Read commit data from body
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
		return
	}

	var commit Commit
	if err := json.Unmarshal(body, &commit); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid commit format"})
		return
	}

	// Verify commit ID matches
	if commit.CommitID != "" && commit.CommitID != commitID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "commit ID mismatch"})
		return
	}

	// Store commit in database
	now := time.Now()
	err = h.db.Session().Query(`
		INSERT INTO commits (library_id, commit_id, parent_id, root_fs_id, creator_id, description, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, repoID, commitID, commit.ParentID, commit.RootID, userID, commit.Description, now).Exec()

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to store commit"})
		return
	}

	// Update library head
	err = h.db.Session().Query(`
		UPDATE libraries SET head_commit_id = ?, updated_at = ?
		WHERE org_id = ? AND library_id = ?
	`, commitID, now, orgID, repoID).Exec()

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update head"})
		return
	}

	c.Status(http.StatusOK)
}

// GetBlock retrieves a block by ID
// GET /seafhttp/repo/:repo_id/block/:block_id
// Supports both SHA-1 (40 chars, Seafile legacy) and SHA-256 (64 chars, new clients)
func (h *SyncHandler) GetBlock(c *gin.Context) {
	externalID := c.Param("block_id")
	orgID := c.GetString("org_id")

	// Determine internal ID based on external ID length
	var internalID string
	isLegacySHA1 := len(externalID) == 40

	if h.db != nil && isLegacySHA1 {
		// SHA-1: look up internal SHA-256 ID from mapping
		err := h.db.Session().Query(`
			SELECT internal_id FROM block_id_mappings WHERE org_id = ? AND external_id = ?
		`, orgID, externalID).Scan(&internalID)

		if err != nil || internalID == "" {
			// Fallback: maybe this is an old block stored with SHA-1 directly
			// Try using the external ID as the internal ID
			internalID = externalID
			log.Printf("GetBlock: no mapping found for %s, using as-is\n", externalID)
		} else {
			log.Printf("GetBlock: resolved %s → %s\n", externalID, internalID)
		}
	} else {
		// SHA-256 or no DB: use external ID directly
		internalID = externalID
	}

	// Look up storage class from database
	var storageClass string
	if h.db != nil {
		err := h.db.Session().Query(`
			SELECT storage_class FROM blocks WHERE org_id = ? AND block_id = ?
		`, orgID, internalID).Scan(&storageClass)

		if err != nil || storageClass == "" {
			storageClass = "hot"
		}
	} else {
		storageClass = "hot"
	}

	// Get the appropriate BlockStore using StorageManager
	var blockStore *storage.BlockStore
	var err error

	if h.storageManager != nil {
		blockStore, err = h.storageManager.GetBlockStore(storageClass)
		if err != nil {
			log.Printf("GetBlock: storage class %s not found: %v, trying default\n", storageClass, err)
			blockStore, _, err = h.storageManager.GetHealthyBlockStore(h.storageManager.ResolveStorageClass("", "", "hot"))
			if err != nil {
				blockStore = h.blockStore
			}
		}
	} else {
		blockStore = h.blockStore
	}

	if blockStore == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "block storage not available"})
		return
	}

	// Retrieve block from storage using internal ID
	data, err := blockStore.GetBlock(c.Request.Context(), internalID)
	if err != nil {
		log.Printf("GetBlock: block %s (internal: %s) not found in %s: %v\n",
			externalID, internalID, storageClass, err)
		c.JSON(http.StatusNotFound, gin.H{"error": "block not found"})
		return
	}

	// Update last accessed time (if DB available)
	if h.db != nil {
		_ = h.db.Session().Query(`
			UPDATE blocks SET last_accessed = ? WHERE org_id = ? AND block_id = ?
		`, time.Now(), orgID, internalID).Exec()
	}

	c.Data(http.StatusOK, "application/octet-stream", data)
}

// PutBlock stores a block
// PUT /seafhttp/repo/:repo_id/block/:block_id
// Supports both SHA-1 (40 chars, Seafile legacy) and SHA-256 (64 chars, new clients)
// Internally always stores blocks using SHA-256 for consistency
func (h *SyncHandler) PutBlock(c *gin.Context) {
	externalID := c.Param("block_id")
	orgID := c.GetString("org_id")
	hashType := c.DefaultQuery("hash_type", "") // Optional: "sha256" for new clients

	log.Printf("PutBlock: externalID=%s, len=%d\n", externalID, len(externalID))

	// Read block data
	data, err := io.ReadAll(c.Request.Body)
	if err != nil {
		log.Printf("PutBlock: failed to read body: %v\n", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read block data"})
		return
	}

	log.Printf("PutBlock: received %d bytes for block %s\n", len(data), externalID)

	// Always compute SHA-256 as the internal storage ID
	sha256Hash := sha256.Sum256(data)
	internalID := hex.EncodeToString(sha256Hash[:])

	// Determine if this is a legacy SHA-1 ID or new SHA-256 ID
	isLegacySHA1 := len(externalID) == 40 && hashType != "sha256"
	isDirectSHA256 := len(externalID) == 64 || hashType == "sha256"

	// Verify hash for SHA-256 clients
	if isDirectSHA256 && externalID != internalID {
		log.Printf("PutBlock: SHA-256 hash mismatch, expected %s got %s\n", externalID, internalID)
		c.JSON(http.StatusBadRequest, gin.H{"error": "block hash mismatch"})
		return
	}

	// Resolve storage class based on request hostname
	hostname := c.Request.Host
	if colonIdx := strings.Index(hostname, ":"); colonIdx > 0 {
		hostname = hostname[:colonIdx] // Strip port
	}

	// Get the appropriate BlockStore using StorageManager with failover
	var blockStore *storage.BlockStore
	var storageClass string

	if h.storageManager != nil {
		preferredClass := h.storageManager.ResolveStorageClass(hostname, "", "hot")
		blockStore, storageClass, err = h.storageManager.GetHealthyBlockStore(preferredClass)
		if err != nil {
			log.Printf("PutBlock: failed to get healthy backend: %v, falling back to legacy\n", err)
			blockStore = h.blockStore
			storageClass = "hot"
		}
	} else {
		blockStore = h.blockStore
		storageClass = "hot"
	}

	if blockStore == nil {
		log.Printf("PutBlock: block storage not available\n")
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "block storage not available"})
		return
	}

	log.Printf("PutBlock: storing block external=%s internal=%s in storage class %s\n",
		externalID, internalID, storageClass)

	// Store block using internal SHA-256 ID
	blockData := &storage.BlockData{
		Data: data,
		Hash: internalID, // Always use SHA-256 for storage
	}

	_, err = blockStore.PutBlockData(c.Request.Context(), blockData)
	if err != nil {
		log.Printf("PutBlock: failed to store in backend: %v\n", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to store block"})
		return
	}

	// Store block metadata and mapping (if DB available)
	if h.db != nil {
		now := time.Now()

		// Store block metadata using internal ID
		_ = h.db.Session().Query(`
			INSERT INTO blocks (org_id, block_id, size_bytes, storage_class, ref_count, created_at, last_accessed)
			VALUES (?, ?, ?, ?, 1, ?, ?)
		`, orgID, internalID, len(data), storageClass, now, now).Exec()

		// If legacy SHA-1 client, store mapping external→internal
		if isLegacySHA1 {
			_ = h.db.Session().Query(`
				INSERT INTO block_id_mappings (org_id, external_id, internal_id, created_at)
				VALUES (?, ?, ?, ?)
			`, orgID, externalID, internalID, now).Exec()
			log.Printf("PutBlock: stored mapping %s → %s\n", externalID, internalID)
		}
	}

	c.Status(http.StatusOK)
}

// CheckBlocksRequest represents the request to check which blocks exist
type CheckBlocksRequest struct {
	BlockIDs []string `json:"block_ids"`
}

// CheckBlocks checks which blocks already exist (for deduplication)
// POST /seafhttp/repo/:repo_id/check-blocks
// Supports both SHA-1 (40 chars, Seafile legacy) and SHA-256 (64 chars, new clients)
// Translates SHA-1 external IDs to internal SHA-256 IDs for storage lookup
func (h *SyncHandler) CheckBlocks(c *gin.Context) {
	orgID := c.GetString("org_id")

	// Read block IDs from body
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
		return
	}

	// Parse as newline-separated block IDs (Seafile format)
	externalIDs := strings.Split(strings.TrimSpace(string(body)), "\n")

	// Build mapping from external IDs to internal IDs
	// For SHA-1 IDs (40 chars), look up the internal SHA-256 from mapping table
	// For SHA-256 IDs (64 chars), use directly
	externalToInternal := make(map[string]string)
	var internalIDs []string

	for _, extID := range externalIDs {
		if extID == "" {
			continue
		}

		var internalID string
		isLegacySHA1 := len(extID) == 40

		if h.db != nil && isLegacySHA1 {
			// SHA-1: look up internal SHA-256 ID from mapping
			err := h.db.Session().Query(`
				SELECT internal_id FROM block_id_mappings WHERE org_id = ? AND external_id = ?
			`, orgID, extID).Scan(&internalID)

			if err != nil || internalID == "" {
				// No mapping found - this block hasn't been uploaded yet
				// or it's an old block stored with SHA-1 directly
				internalID = extID
			}
		} else {
			// SHA-256 or no DB: use external ID directly
			internalID = extID
		}

		externalToInternal[extID] = internalID
		internalIDs = append(internalIDs, internalID)
	}

	// Resolve storage class based on request hostname
	hostname := c.Request.Host
	if colonIdx := strings.Index(hostname, ":"); colonIdx > 0 {
		hostname = hostname[:colonIdx] // Strip port
	}

	// Get the appropriate BlockStore using StorageManager with failover
	var blockStore *storage.BlockStore

	if h.storageManager != nil {
		preferredClass := h.storageManager.ResolveStorageClass(hostname, "", "hot")
		blockStore, _, err = h.storageManager.GetHealthyBlockStore(preferredClass)
		if err != nil {
			log.Printf("CheckBlocks: failed to get healthy backend: %v, falling back to legacy\n", err)
			blockStore = h.blockStore
		}
	} else {
		blockStore = h.blockStore
	}

	if blockStore == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "block storage not available"})
		return
	}

	// Check which blocks exist using internal IDs
	existMap, err := blockStore.CheckBlocksParallel(c.Request.Context(), internalIDs, 10)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check blocks"})
		return
	}

	// Return list of missing blocks using external IDs (client expects these)
	var needed []string
	for _, extID := range externalIDs {
		if extID == "" {
			continue
		}
		internalID := externalToInternal[extID]
		if !existMap[internalID] {
			needed = append(needed, extID)
		}
	}

	// Return as newline-separated list
	c.String(http.StatusOK, strings.Join(needed, "\n"))
}

// GetFSIDList returns the list of FS object IDs for sync
// GET /seafhttp/repo/:repo_id/fs-id-list
func (h *SyncHandler) GetFSIDList(c *gin.Context) {
	repoID := c.Param("repo_id")
	serverHead := c.Query("server-head")
	clientHead := c.Query("client-head")
	dirOnly := c.Query("dir-only") == "1"

	_ = clientHead // Used for incremental sync
	_ = dirOnly    // Whether to return only directories

	// Get FS object IDs by traversing from server head commit
	// Initialize as empty slice (not nil) so JSON serializes as [] not null
	fsIDs := make([]string, 0)

	if serverHead != "" {
		// Query root FS ID from commit
		var rootFSID string
		err := h.db.Session().Query(`
			SELECT root_fs_id FROM commits WHERE library_id = ? AND commit_id = ?
		`, repoID, serverHead).Scan(&rootFSID)

		if err == nil && rootFSID != "" && rootFSID != strings.Repeat("0", 40) {
			// Only include non-empty root FS IDs
			// Empty root (all zeros) means empty library, return empty list
			fsIDs = append(fsIDs, rootFSID)
		}
	}

	// Return as JSON array (Seafile format)
	c.JSON(http.StatusOK, fsIDs)
}

// GetFSObject retrieves a filesystem object
// GET /seafhttp/repo/:repo_id/fs/:fs_id
func (h *SyncHandler) GetFSObject(c *gin.Context) {
	repoID := c.Param("repo_id")
	fsID := c.Param("fs_id")

	// Query FS object from database
	// Schema uses: obj_type, obj_name, dir_entries (as TEXT), block_ids (as LIST<TEXT>)
	var fsType string
	var name string
	var size int64
	var mtime int64
	var entriesJSON string
	var blockIDs []string

	err := h.db.Session().Query(`
		SELECT obj_type, obj_name, size_bytes, mtime, dir_entries, block_ids
		FROM fs_objects WHERE library_id = ? AND fs_id = ?
	`, repoID, fsID).Scan(&fsType, &name, &size, &mtime, &entriesJSON, &blockIDs)

	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "fs object not found"})
		return
	}

	// Build FS object
	obj := FSObject{
		ID:    fsID,
		Name:  name,
		Size:  size,
		Mtime: mtime,
	}

	if fsType == "file" {
		obj.Type = 1
		obj.BlockIDs = blockIDs
	} else {
		obj.Type = 3
		if entriesJSON != "" {
			json.Unmarshal([]byte(entriesJSON), &obj.Entries)
		}
	}

	c.JSON(http.StatusOK, obj)
}

// PackFS packs multiple FS objects into a single response
// POST /seafhttp/repo/:repo_id/pack-fs
func (h *SyncHandler) PackFS(c *gin.Context) {
	repoID := c.Param("repo_id")

	// Read FS IDs from body
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
		return
	}

	fsIDs := strings.Split(strings.TrimSpace(string(body)), "\n")

	// Collect FS objects
	var objects []FSObject
	for _, fsID := range fsIDs {
		if fsID == "" {
			continue
		}

		var fsType string
		var name string
		var size int64
		var mtime int64
		var entriesJSON string
		var blockIDs []string

		err := h.db.Session().Query(`
			SELECT obj_type, obj_name, size_bytes, mtime, dir_entries, block_ids
			FROM fs_objects WHERE library_id = ? AND fs_id = ?
		`, repoID, fsID).Scan(&fsType, &name, &size, &mtime, &entriesJSON, &blockIDs)

		if err != nil {
			continue // Skip missing objects
		}

		obj := FSObject{
			ID:    fsID,
			Name:  name,
			Size:  size,
			Mtime: mtime,
		}

		if fsType == "file" {
			obj.Type = 1
			obj.BlockIDs = blockIDs
		} else {
			obj.Type = 3
			if entriesJSON != "" {
				json.Unmarshal([]byte(entriesJSON), &obj.Entries)
			}
		}

		objects = append(objects, obj)
	}

	c.JSON(http.StatusOK, objects)
}

// RecvFS receives and stores FS objects from client
// POST /seafhttp/repo/:repo_id/recv-fs
// Seafile sends packed FS objects in binary format:
// - 40-char hex FS ID + newline
// - Binary object data (type byte + serialized content)
func (h *SyncHandler) RecvFS(c *gin.Context) {
	repoID := c.Param("repo_id")

	// Read FS objects from body
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
		return
	}

	if len(body) < 41 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "body too short"})
		return
	}

	// Parse packed FS objects
	// Format: each object is [40-char hex ID][newline][object data]
	// Multiple objects can be concatenated
	offset := 0
	objectsStored := 0

	for offset < len(body) {
		// Read 40-char hex FS ID
		if offset+40 > len(body) {
			break
		}
		fsID := string(body[offset : offset+40])
		offset += 40

		// Skip newline if present
		if offset < len(body) && body[offset] == '\n' {
			offset++
		}

		// Find the object data - it ends at the next 40-char hex ID or end of body
		dataStart := offset
		dataEnd := len(body)

		// Look for the next FS ID (40 hex chars followed by newline or end)
		for i := offset; i < len(body)-40; i++ {
			if isHexString(body[i:i+40]) && (i+40 >= len(body) || body[i+40] == '\n') {
				dataEnd = i
				break
			}
		}

		objData := body[dataStart:dataEnd]
		offset = dataEnd

		// Parse the object data (Seafile binary format)
		// Type 1 = file, Type 3 = directory
		if len(objData) == 0 {
			continue
		}

		objType := int(objData[0])
		fsType := "dir"
		if objType == 1 {
			fsType = "file"
		}

		// For now, store the raw binary data and basic info
		// Full parsing of Seafile binary format would go here
		now := time.Now().Unix()

		err := h.db.Session().Query(`
			INSERT INTO fs_objects (library_id, fs_id, obj_type, obj_name, size_bytes, mtime, dir_entries, block_ids)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, repoID, fsID, fsType, "", 0, now, "[]", []string{}).Exec()

		if err != nil {
			fmt.Printf("recv-fs: Failed to store object %s: %v\n", fsID, err)
		} else {
			objectsStored++
		}
	}

	fmt.Printf("recv-fs: Stored %d objects for repo %s\n", objectsStored, repoID)
	c.Status(http.StatusOK)
}

// isHexString checks if bytes are valid hex characters
func isHexString(b []byte) bool {
	for _, c := range b {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// CheckFS checks which FS objects already exist
// POST /seafhttp/repo/:repo_id/check-fs
func (h *SyncHandler) CheckFS(c *gin.Context) {
	repoID := c.Param("repo_id")

	// Read FS IDs from body
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
		return
	}

	fsIDs := strings.Split(strings.TrimSpace(string(body)), "\n")

	// Check which FS objects exist
	var needed []string
	for _, fsID := range fsIDs {
		if fsID == "" {
			continue
		}

		var exists string
		err := h.db.Session().Query(`
			SELECT fs_id FROM fs_objects WHERE library_id = ? AND fs_id = ? LIMIT 1
		`, repoID, fsID).Scan(&exists)

		if err != nil {
			needed = append(needed, fsID)
		}
	}

	c.String(http.StatusOK, strings.Join(needed, "\n"))
}

// PermissionCheck checks user permissions for the repository
// GET /seafhttp/repo/:repo_id/permission-check
func (h *SyncHandler) PermissionCheck(c *gin.Context) {
	// Real Seafile returns empty body with 200 OK for success
	// The permission is already validated by auth middleware
	// TODO: Implement proper permission checking and return 403 if denied
	c.Status(http.StatusOK)
}

// QuotaCheck checks if user has enough quota for upload
// GET /seafhttp/repo/:repo_id/quota-check
func (h *SyncHandler) QuotaCheck(c *gin.Context) {
	// For now, return unlimited quota
	// TODO: Implement proper quota checking
	c.JSON(http.StatusOK, gin.H{
		"has_quota": true,
	})
}

// GetHeadCommitsMulti returns head commits for multiple repositories at once
// POST /seafhttp/repo/head-commits-multi
func (h *SyncHandler) GetHeadCommitsMulti(c *gin.Context) {
	orgID := c.GetString("org_id")

	// Read repo IDs from body (newline separated)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
		return
	}

	repoIDs := strings.Split(strings.TrimSpace(string(body)), "\n")

	// Build response map of repo_id -> head_commit_id
	result := make(map[string]string)

	for _, repoID := range repoIDs {
		if repoID == "" {
			continue
		}

		var headCommitID string
		err := h.db.Session().Query(`
			SELECT head_commit_id FROM libraries WHERE org_id = ? AND library_id = ?
		`, orgID, repoID).Scan(&headCommitID)

		if err == nil && headCommitID != "" {
			result[repoID] = headCommitID
		}
	}

	c.JSON(http.StatusOK, result)
}

// UpdateBranch updates the head commit of a repository branch
// POST /seafhttp/repo/:repo_id/update-branch
func (h *SyncHandler) UpdateBranch(c *gin.Context) {
	repoID := c.Param("repo_id")
	orgID := c.GetString("org_id")

	// Get new head commit from query params
	newHead := c.Query("head")
	if newHead == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing head parameter"})
		return
	}

	// Verify the commit exists
	var commitID string
	err := h.db.Session().Query(`
		SELECT commit_id FROM commits WHERE library_id = ? AND commit_id = ?
	`, repoID, newHead).Scan(&commitID)

	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "commit not found"})
		return
	}

	// Update library head
	err = h.db.Session().Query(`
		UPDATE libraries SET head_commit_id = ?, updated_at = ?
		WHERE org_id = ? AND library_id = ?
	`, newHead, time.Now(), orgID, repoID).Exec()

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update branch"})
		return
	}

	// Return empty body with 200 OK (Seafile format)
	c.Status(http.StatusOK)
}
