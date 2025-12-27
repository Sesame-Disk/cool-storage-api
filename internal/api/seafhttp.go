package api

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sync"
	"time"

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
func (tm *TokenManager) DeleteToken(tokenStr string) {
	tm.mu.Lock()
	delete(tm.tokens, tokenStr)
	tm.mu.Unlock()
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

// SeafHTTPHandler handles Seafile-compatible file operations
type SeafHTTPHandler struct {
	storage      *storage.S3Store
	tokenManager *TokenManager
}

// NewSeafHTTPHandler creates a new SeafHTTP handler
func NewSeafHTTPHandler(s3Store *storage.S3Store, tokenManager *TokenManager) *SeafHTTPHandler {
	return &SeafHTTPHandler{
		storage:      s3Store,
		tokenManager: tokenManager,
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
	token, valid := h.tokenManager.GetToken(tokenStr, TokenTypeUpload)
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
	h.tokenManager.DeleteToken(tokenStr)

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

	// Validate token
	token, valid := h.tokenManager.GetToken(tokenStr, TokenTypeDownload)
	if !valid {
		c.JSON(http.StatusForbidden, gin.H{"error": "invalid or expired download token"})
		return
	}

	// Check if storage is available
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

	// Get filename from path
	filename := filepath.Base(token.Path)
	if requestedPath != "" && requestedPath != "/" {
		filename = filepath.Base(requestedPath)
	}

	// Read content to get size (for small files)
	// For large files, this should use streaming with content-length from S3
	content, err := io.ReadAll(reader)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read file"})
		return
	}

	// Set headers for file download
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Data(http.StatusOK, "application/octet-stream", content)
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
