package api

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/gin-gonic/gin"
)

// TokenType represents the type of access token
type TokenType string

const (
	TokenTypeUpload   TokenType = "upload"
	TokenTypeDownload TokenType = "download"
)

// AccessToken represents a temporary access token for file operations
type AccessToken struct {
	Token     string
	Type      TokenType
	OrgID     string
	RepoID    string
	Path      string    // File path for downloads, parent dir for uploads
	UserID    string
	ExpiresAt time.Time
	CreatedAt time.Time
}

// TokenStore is the interface for token operations (can be in-memory or Cassandra-backed)
type TokenStore interface {
	CreateUploadToken(orgID, repoID, path, userID string) (string, error)
	CreateDownloadToken(orgID, repoID, path, userID string) (string, error)
	GetToken(tokenStr string, expectedType TokenType) (*AccessToken, bool)
	DeleteToken(tokenStr string) error
}

// TokenManager manages temporary access tokens for file operations
type TokenManager struct {
	tokens   map[string]*AccessToken
	mu       sync.RWMutex
	tokenTTL time.Duration
}

// NewTokenManager creates a new token manager with the specified TTL
func NewTokenManager(tokenTTL time.Duration) *TokenManager {
	if tokenTTL <= 0 {
		tokenTTL = DefaultTokenTTL
	}
	tm := &TokenManager{
		tokens:   make(map[string]*AccessToken),
		tokenTTL: tokenTTL,
	}
	// Start cleanup goroutine
	go tm.cleanup()
	return tm
}

// DefaultTokenTTL is the default time-to-live for tokens
const DefaultTokenTTL = 1 * time.Hour

// CreateToken creates a new access token
func (tm *TokenManager) CreateToken(tokenType TokenType, orgID, repoID, path, userID string, ttl time.Duration) (*AccessToken, error) {
	// Generate random token
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return nil, err
	}
	tokenStr := hex.EncodeToString(bytes)

	token := &AccessToken{
		Token:     tokenStr,
		Type:      tokenType,
		OrgID:     orgID,
		RepoID:    repoID,
		Path:      path,
		UserID:    userID,
		ExpiresAt: time.Now().Add(ttl),
		CreatedAt: time.Now(),
	}

	tm.mu.Lock()
	tm.tokens[tokenStr] = token
	tm.mu.Unlock()

	return token, nil
}

// CreateUploadToken creates an upload token (implements TokenCreator interface)
func (tm *TokenManager) CreateUploadToken(orgID, repoID, path, userID string) (string, error) {
	token, err := tm.CreateToken(TokenTypeUpload, orgID, repoID, path, userID, tm.tokenTTL)
	if err != nil {
		return "", err
	}
	return token.Token, nil
}

// CreateDownloadToken creates a download token (implements TokenCreator interface)
func (tm *TokenManager) CreateDownloadToken(orgID, repoID, path, userID string) (string, error) {
	token, err := tm.CreateToken(TokenTypeDownload, orgID, repoID, path, userID, tm.tokenTTL)
	if err != nil {
		return "", err
	}
	return token.Token, nil
}

// GetToken retrieves and validates a token
func (tm *TokenManager) GetToken(tokenStr string, expectedType TokenType) (*AccessToken, bool) {
	tm.mu.RLock()
	token, exists := tm.tokens[tokenStr]
	tm.mu.RUnlock()

	if !exists {
		return nil, false
	}

	// Check if expired
	if time.Now().After(token.ExpiresAt) {
		tm.DeleteToken(tokenStr)
		return nil, false
	}

	// Check type
	if token.Type != expectedType {
		return nil, false
	}

	return token, true
}

// DeleteToken removes a token
func (tm *TokenManager) DeleteToken(tokenStr string) error {
	tm.mu.Lock()
	delete(tm.tokens, tokenStr)
	tm.mu.Unlock()
	return nil
}

// cleanup periodically removes expired tokens
func (tm *TokenManager) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	for range ticker.C {
		tm.mu.Lock()
		now := time.Now()
		for token, at := range tm.tokens {
			if now.After(at.ExpiresAt) {
				delete(tm.tokens, token)
			}
		}
		tm.mu.Unlock()
	}
}

// Ensure TokenManager implements TokenStore
var _ TokenStore = (*TokenManager)(nil)

// SeafHTTPHandler handles Seafile-compatible file operations
type SeafHTTPHandler struct {
	storage        *storage.S3Store
	storageManager *storage.Manager
	db             *db.DB
	tokenStore     TokenStore
}

// NewSeafHTTPHandler creates a new SeafHTTP handler
func NewSeafHTTPHandler(s3Store *storage.S3Store, storageManager *storage.Manager, database *db.DB, tokenStore TokenStore) *SeafHTTPHandler {
	return &SeafHTTPHandler{
		storage:        s3Store,
		storageManager: storageManager,
		db:             database,
		tokenStore:     tokenStore,
	}
}

// RegisterSeafHTTPRoutes registers the seafhttp routes
func (h *SeafHTTPHandler) RegisterSeafHTTPRoutes(router *gin.Engine) {
	seafhttp := router.Group("/seafhttp")
	{
		// Upload endpoint - receives files and stores them in S3
		seafhttp.POST("/upload-api/:token", h.HandleUpload)

		// Download endpoint - streams files from S3
		seafhttp.GET("/files/:token/*filepath", h.HandleDownload)
	}
}

// HandleUpload handles file uploads via the upload token
func (h *SeafHTTPHandler) HandleUpload(c *gin.Context) {
	tokenStr := c.Param("token")

	// Validate token
	token, valid := h.tokenStore.GetToken(tokenStr, TokenTypeUpload)
	if !valid {
		c.JSON(http.StatusForbidden, gin.H{"error": "invalid or expired upload token"})
		return
	}

	// Check if storage is available
	if h.storage == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "storage not available"})
		return
	}

	// Get the file from the request
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file is required"})
		return
	}
	defer file.Close()

	// Get optional parameters
	parentDir := c.DefaultPostForm("parent_dir", token.Path)
	replace := c.DefaultPostForm("replace", "0")
	retJSON := c.Query("ret-json") == "1" || c.PostForm("ret-json") == "1"

	_ = replace // TODO: Handle replace logic

	// Build the storage key
	filename := header.Filename
	filePath := filepath.Join(parentDir, filename)
	storageKey := fmt.Sprintf("%s/%s%s", token.OrgID, token.RepoID, filePath)

	// Read file content
	content, err := io.ReadAll(file)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read file"})
		return
	}

	// Upload to S3 using the content we already read
	_, err = h.storage.Put(c.Request.Context(), storageKey, newBytesReader(content), int64(len(content)))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to upload file"})
		return
	}

	// Generate file ID (content hash would be better, but using storage key for now)
	fileID := generateFileID(storageKey)

	// Delete the upload token (one-time use)
	h.tokenStore.DeleteToken(tokenStr)

	// Return response based on ret-json parameter
	if retJSON {
		c.JSON(http.StatusOK, []gin.H{
			{
				"name": filename,
				"id":   fileID,
				"size": len(content),
			},
		})
	} else {
		// Return just the file ID as plain text (Seafile compatible)
		c.String(http.StatusOK, fileID)
	}
}

// HandleDownload handles file downloads via the download token
func (h *SeafHTTPHandler) HandleDownload(c *gin.Context) {
	tokenStr := c.Param("token")
	requestedPath := c.Param("filepath")

	log.Printf("[HandleDownload] Token: %s, RequestedPath: %s", tokenStr, requestedPath)

	// Validate token
	token, valid := h.tokenStore.GetToken(tokenStr, TokenTypeDownload)
	if !valid {
		log.Printf("[HandleDownload] Invalid token: %s", tokenStr)
		c.JSON(http.StatusForbidden, gin.H{"error": "invalid or expired download token"})
		return
	}

	log.Printf("[HandleDownload] Token valid: OrgID=%s, RepoID=%s, Path=%s", token.OrgID, token.RepoID, token.Path)

	// Get filename from path
	filename := filepath.Base(token.Path)
	if requestedPath != "" && requestedPath != "/" {
		filename = filepath.Base(requestedPath)
	}

	// Try to get file content from block storage (content-addressed)
	// This is the normal flow for SesameFS files
	if h.db != nil && h.storageManager != nil {
		log.Printf("[HandleDownload] Attempting block-based file retrieval")
		content, err := h.getFileFromBlocks(c, token)
		if err == nil {
			log.Printf("[HandleDownload] Block-based retrieval SUCCESS, size=%d", len(content))
			c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
			c.Data(http.StatusOK, "application/octet-stream", content)
			return
		}
		log.Printf("[HandleDownload] Block-based retrieval FAILED: %v", err)
		// If block-based retrieval fails, fall back to direct S3 path-based retrieval
	} else {
		log.Printf("[HandleDownload] Block storage not available (db=%v, storageManager=%v)", h.db != nil, h.storageManager != nil)
	}

	// Fallback: Try direct S3 path-based retrieval (legacy)
	if h.storage == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "storage not available"})
		return
	}

	// Build the storage key
	storageKey := fmt.Sprintf("%s/%s%s", token.OrgID, token.RepoID, token.Path)

	// Get the file from S3
	reader, err := h.storage.Get(c.Request.Context(), storageKey)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		return
	}
	defer reader.Close()

	// Read content
	content, err := io.ReadAll(reader)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read file"})
		return
	}

	// Set headers for file download
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Data(http.StatusOK, "application/octet-stream", content)
}

// getFileFromBlocks retrieves a file by looking up its blocks and concatenating them
func (h *SeafHTTPHandler) getFileFromBlocks(c *gin.Context, token *AccessToken) ([]byte, error) {
	ctx := c.Request.Context()

	// Get the library's head commit to find the root FS
	var headCommit string
	err := h.db.Session().Query(`
		SELECT head_commit_id FROM libraries
		WHERE org_id = ? AND library_id = ?
	`, token.OrgID, token.RepoID).Scan(&headCommit)
	if err != nil {
		return nil, fmt.Errorf("library not found: %w", err)
	}

	// Get the root FS ID from the commit
	var rootFSID string
	err = h.db.Session().Query(`
		SELECT root_fs_id FROM commits
		WHERE library_id = ? AND commit_id = ?
	`, token.RepoID, headCommit).Scan(&rootFSID)
	if err != nil {
		return nil, fmt.Errorf("commit not found: %w", err)
	}

	// Navigate to the target file through the directory structure
	filePath := token.Path
	if !strings.HasPrefix(filePath, "/") {
		filePath = "/" + filePath
	}

	// Split path into components
	pathParts := strings.Split(strings.Trim(filePath, "/"), "/")
	if len(pathParts) == 0 || (len(pathParts) == 1 && pathParts[0] == "") {
		return nil, fmt.Errorf("invalid file path")
	}

	currentFSID := rootFSID

	// Navigate to the file (all parts except the last are directories)
	for i := 0; i < len(pathParts)-1; i++ {
		dirName := pathParts[i]
		nextFSID, err := h.findEntryInDir(token.RepoID, currentFSID, dirName)
		if err != nil {
			return nil, fmt.Errorf("directory not found: %s: %w", dirName, err)
		}
		currentFSID = nextFSID
	}

	// Find the target file in the current directory
	targetName := pathParts[len(pathParts)-1]
	fileFSID, err := h.findEntryInDir(token.RepoID, currentFSID, targetName)
	if err != nil {
		return nil, fmt.Errorf("file not found: %s: %w", targetName, err)
	}

	// Get the file's block IDs
	var blockIDs []string
	err = h.db.Session().Query(`
		SELECT block_ids FROM fs_objects
		WHERE library_id = ? AND fs_id = ?
	`, token.RepoID, fileFSID).Scan(&blockIDs)
	if err != nil {
		return nil, fmt.Errorf("file metadata not found: %w", err)
	}

	// Get block store from storage manager
	blockStore, _, err := h.storageManager.GetHealthyBlockStore("")
	if err != nil {
		return nil, fmt.Errorf("block store not available: %w", err)
	}

	// Retrieve and concatenate blocks
	var content bytes.Buffer
	for _, blockID := range blockIDs {
		// Translate SHA-1 (40 chars) to SHA-256 (64 chars) if needed
		internalID := blockID
		if len(blockID) == 40 {
			// Look up internal SHA-256 ID from mapping
			var mappedID string
			err := h.db.Session().Query(`
				SELECT internal_id FROM block_id_mappings WHERE org_id = ? AND external_id = ?
			`, token.OrgID, blockID).Scan(&mappedID)
			if err == nil && mappedID != "" {
				log.Printf("[getFileFromBlocks] Resolved block %s → %s", blockID, mappedID)
				internalID = mappedID
			} else {
				log.Printf("[getFileFromBlocks] No mapping for block %s, using as-is", blockID)
			}
		}

		blockData, err := blockStore.GetBlock(ctx, internalID)
		if err != nil {
			return nil, fmt.Errorf("failed to retrieve block %s: %w", blockID, err)
		}
		content.Write(blockData)
	}

	return content.Bytes(), nil
}

// findEntryInDir finds an entry (file or directory) within a directory FS object
func (h *SeafHTTPHandler) findEntryInDir(repoID, dirFSID, entryName string) (string, error) {
	var dirEntries string
	err := h.db.Session().Query(`
		SELECT dir_entries FROM fs_objects
		WHERE library_id = ? AND fs_id = ?
	`, repoID, dirFSID).Scan(&dirEntries)
	if err != nil {
		return "", fmt.Errorf("directory not found: %w", err)
	}

	log.Printf("[findEntryInDir] Looking for entry '%s' in dir %s", entryName, dirFSID)
	log.Printf("[findEntryInDir] Dir entries length: %d", len(dirEntries))

	// Parse dir_entries - format is JSON array like [{"id":"...","mode":...,"modifier":"...","mtime":...,"name":"...","size":...},...]
	// Find the entry by name, then extract the entire JSON object to get the ID
	searchPattern := fmt.Sprintf(`"name":"%s"`, entryName)
	log.Printf("[findEntryInDir] Search pattern: %s", searchPattern)
	idx := strings.Index(dirEntries, searchPattern)
	log.Printf("[findEntryInDir] Pattern found at index: %d", idx)
	if idx == -1 {
		// Log a snippet of the dir_entries for debugging
		if len(dirEntries) > 500 {
			log.Printf("[findEntryInDir] Dir entries (first 500 chars): %s", dirEntries[:500])
		} else {
			log.Printf("[findEntryInDir] Dir entries: %s", dirEntries)
		}
		return "", fmt.Errorf("entry not found: %s", entryName)
	}

	// Find the start of the JSON object containing this entry (search backwards for '{')
	objectStart := -1
	for i := idx; i >= 0; i-- {
		if dirEntries[i] == '{' {
			objectStart = i
			break
		}
	}
	if objectStart == -1 {
		return "", fmt.Errorf("malformed dir entries: no object start for: %s", entryName)
	}

	// Find the end of the JSON object (search forward for '}')
	objectEnd := -1
	for i := idx; i < len(dirEntries); i++ {
		if dirEntries[i] == '}' {
			objectEnd = i + 1
			break
		}
	}
	if objectEnd == -1 {
		return "", fmt.Errorf("malformed dir entries: no object end for: %s", entryName)
	}

	// Extract just this entry's JSON object
	entryJSON := dirEntries[objectStart:objectEnd]
	log.Printf("[findEntryInDir] Extracted object: %s", entryJSON)

	// Find the "id" field within this object
	idPattern := `"id":"`
	idIdx := strings.Index(entryJSON, idPattern)
	if idIdx == -1 {
		return "", fmt.Errorf("entry ID not found for: %s", entryName)
	}

	// Extract the ID value
	idStart := idIdx + len(idPattern)
	idEnd := strings.Index(entryJSON[idStart:], `"`)
	if idEnd == -1 {
		return "", fmt.Errorf("malformed entry for: %s", entryName)
	}

	entryID := entryJSON[idStart : idStart+idEnd]
	log.Printf("[findEntryInDir] Found entry ID: %s", entryID)

	return entryID, nil
}

// Helper function to generate a file ID
func generateFileID(storageKey string) string {
	bytes := make([]byte, 20)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

// bytesReader wraps []byte to implement io.Reader
type bytesReader struct {
	data []byte
	pos  int
}

func newBytesReader(data []byte) *bytesReader {
	return &bytesReader{data: data}
}

func (r *bytesReader) Read(p []byte) (n int, err error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n = copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}
