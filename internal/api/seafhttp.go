package api

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	v2 "github.com/Sesame-Disk/sesamefs/internal/api/v2"
	"github.com/Sesame-Disk/sesamefs/internal/crypto"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/Sesame-Disk/sesamefs/internal/streaming"
	"github.com/Sesame-Disk/sesamefs/internal/traffic"
	"github.com/gin-gonic/gin"
)

// TokenType represents the type of access token
type TokenType string

const (
	TokenTypeUpload       TokenType = "upload"
	TokenTypeDownload     TokenType = "download"
	TokenTypeOneTimeLogin TokenType = "onetime_login"
)

// AccessToken represents a temporary access token for file operations
type AccessToken struct {
	Token     string
	Type      TokenType
	OrgID     string
	RepoID    string
	Path      string // File path for downloads, parent dir for uploads
	UserID    string
	Source    string // "" or "web" = regular user; "link" = share/upload link
	AuthToken string // User's auth token (for one-time login tokens)
	ExpiresAt time.Time
	CreatedAt time.Time
}

// TokenStore is the interface for token operations (can be in-memory or Cassandra-backed)
type TokenStore interface {
	CreateUploadToken(orgID, repoID, path, userID string) (string, error)
	CreateDownloadToken(orgID, repoID, path, userID string) (string, error)
	CreateLinkUploadToken(orgID, repoID, path, userID string) (string, error)
	CreateLinkDownloadToken(orgID, repoID, path, userID string) (string, error)
	GetToken(tokenStr string, expectedType TokenType) (*AccessToken, bool)
	DeleteToken(tokenStr string) error
	CreateOneTimeLoginToken(userID, orgID, authToken string) (string, error)
	ConsumeOneTimeLoginToken(oneTimeToken string) (string, error)
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
func (tm *TokenManager) CreateToken(tokenType TokenType, orgID, repoID, path, userID, source string, ttl time.Duration) (*AccessToken, error) {
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
		Source:    source,
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
	token, err := tm.CreateToken(TokenTypeUpload, orgID, repoID, path, userID, "", tm.tokenTTL)
	if err != nil {
		return "", err
	}
	return token.Token, nil
}

// CreateDownloadToken creates a download token (implements TokenCreator interface)
func (tm *TokenManager) CreateDownloadToken(orgID, repoID, path, userID string) (string, error) {
	token, err := tm.CreateToken(TokenTypeDownload, orgID, repoID, path, userID, "", tm.tokenTTL)
	if err != nil {
		return "", err
	}
	return token.Token, nil
}

// CreateLinkUploadToken creates an upload token tagged as a share/upload link.
func (tm *TokenManager) CreateLinkUploadToken(orgID, repoID, path, userID string) (string, error) {
	token, err := tm.CreateToken(TokenTypeUpload, orgID, repoID, path, userID, "link", tm.tokenTTL)
	if err != nil {
		return "", err
	}
	return token.Token, nil
}

// CreateLinkDownloadToken creates a download token tagged as a share link.
func (tm *TokenManager) CreateLinkDownloadToken(orgID, repoID, path, userID string) (string, error) {
	token, err := tm.CreateToken(TokenTypeDownload, orgID, repoID, path, userID, "link", tm.tokenTTL)
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

// CreateOneTimeLoginToken creates a one-time login token for desktop client auto-login
func (tm *TokenManager) CreateOneTimeLoginToken(userID, orgID, authToken string) (string, error) {
	// Generate random token
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	tokenStr := hex.EncodeToString(bytes)

	token := &AccessToken{
		Token:     tokenStr,
		Type:      TokenTypeOneTimeLogin,
		UserID:    userID,
		OrgID:     orgID,
		AuthToken: authToken,
		ExpiresAt: time.Now().Add(60 * time.Second), // One-time tokens expire in 60 seconds
		CreatedAt: time.Now(),
	}

	tm.mu.Lock()
	tm.tokens[tokenStr] = token
	tm.mu.Unlock()

	return tokenStr, nil
}

// ConsumeOneTimeLoginToken validates and consumes a one-time login token
// Returns the user's auth token if valid, error otherwise
func (tm *TokenManager) ConsumeOneTimeLoginToken(oneTimeToken string) (string, error) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	token, exists := tm.tokens[oneTimeToken]
	if !exists {
		return "", fmt.Errorf("token not found")
	}

	// Check if expired
	if time.Now().After(token.ExpiresAt) {
		delete(tm.tokens, oneTimeToken)
		return "", fmt.Errorf("token expired")
	}

	// Check type
	if token.Type != TokenTypeOneTimeLogin {
		return "", fmt.Errorf("invalid token type")
	}

	// Consume the token (single-use)
	authToken := token.AuthToken
	delete(tm.tokens, oneTimeToken)

	return authToken, nil
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

// ChunkUpload tracks an ongoing chunked upload
type ChunkUpload struct {
	Token       string
	Filename    string
	ParentDir   string
	TotalSize   int64
	TempFile    *os.File
	TempPath    string
	ReceivedEnd int64 // Track the highest byte received
	mu          sync.Mutex
}

// ChunkManager manages chunked uploads
type ChunkManager struct {
	uploads map[string]*ChunkUpload // keyed by "token:filename"
	mu      sync.RWMutex
	tempDir string
}

// NewChunkManager creates a new chunk manager
func NewChunkManager() *ChunkManager {
	tempDir := os.TempDir()
	return &ChunkManager{
		uploads: make(map[string]*ChunkUpload),
		tempDir: tempDir,
	}
}

// Global chunk manager instance
var chunkManager = NewChunkManager()

// GetOrCreateUpload gets or creates a chunk upload tracker
func (cm *ChunkManager) GetOrCreateUpload(token, filename, parentDir string, totalSize int64) (*ChunkUpload, error) {
	key := token + ":" + filename
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if upload, exists := cm.uploads[key]; exists {
		return upload, nil
	}

	// Create temp file
	tempPath := filepath.Join(cm.tempDir, fmt.Sprintf("sesamefs_upload_%s_%s", token, sanitizeFilename(filename)))
	tempFile, err := os.OpenFile(tempPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}

	// Pre-allocate the file to total size (for seeking)
	if totalSize > 0 {
		if err := tempFile.Truncate(totalSize); err != nil {
			tempFile.Close()
			os.Remove(tempPath)
			return nil, fmt.Errorf("failed to pre-allocate temp file: %w", err)
		}
	}

	upload := &ChunkUpload{
		Token:       token,
		Filename:    filename,
		ParentDir:   parentDir,
		TotalSize:   totalSize,
		TempFile:    tempFile,
		TempPath:    tempPath,
		ReceivedEnd: -1,
	}
	cm.uploads[key] = upload
	log.Printf("[ChunkManager] Created upload tracker: %s, totalSize=%d", key, totalSize)
	return upload, nil
}

// WriteChunk writes a chunk to the correct position in the temp file
func (cu *ChunkUpload) WriteChunk(data []byte, start, end int64) error {
	cu.mu.Lock()
	defer cu.mu.Unlock()

	// Seek to the start position
	if _, err := cu.TempFile.Seek(start, io.SeekStart); err != nil {
		return fmt.Errorf("failed to seek: %w", err)
	}

	// Write the data
	if _, err := cu.TempFile.Write(data); err != nil {
		return fmt.Errorf("failed to write chunk: %w", err)
	}

	// Update received end marker
	if end > cu.ReceivedEnd {
		cu.ReceivedEnd = end
	}

	log.Printf("[ChunkUpload] Wrote chunk: start=%d, end=%d, received_end=%d, total=%d",
		start, end, cu.ReceivedEnd, cu.TotalSize)
	return nil
}

// IsComplete checks if all chunks have been received
func (cu *ChunkUpload) IsComplete() bool {
	return cu.ReceivedEnd >= cu.TotalSize-1
}

// WriteChunkFromReader streams a chunk from a reader directly to the temp file
// at the correct offset, without loading the entire chunk into memory.
func (cu *ChunkUpload) WriteChunkFromReader(r io.Reader, start, end int64) error {
	cu.mu.Lock()
	defer cu.mu.Unlock()

	if _, err := cu.TempFile.Seek(start, io.SeekStart); err != nil {
		return fmt.Errorf("failed to seek: %w", err)
	}

	written, err := io.Copy(cu.TempFile, r)
	if err != nil {
		return fmt.Errorf("failed to write chunk: %w", err)
	}

	// Update received end marker based on actual bytes written
	actualEnd := start + written - 1
	if actualEnd > cu.ReceivedEnd {
		cu.ReceivedEnd = actualEnd
	}

	log.Printf("[ChunkUpload] Streamed chunk: start=%d, written=%d, received_end=%d, total=%d",
		start, written, cu.ReceivedEnd, cu.TotalSize)
	return nil
}

// GetContent reads the complete file content into memory.
// DEPRECATED for large files: use GetReader instead.
func (cu *ChunkUpload) GetContent() ([]byte, error) {
	cu.mu.Lock()
	defer cu.mu.Unlock()

	if _, err := cu.TempFile.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	return io.ReadAll(cu.TempFile)
}

// GetReader returns a reader positioned at the beginning of the temp file.
// The caller must NOT call Cleanup until done reading.
func (cu *ChunkUpload) GetReader() (io.Reader, error) {
	cu.mu.Lock()
	defer cu.mu.Unlock()

	if _, err := cu.TempFile.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	return cu.TempFile, nil
}

// Cleanup removes the temp file
func (cu *ChunkUpload) Cleanup() error {
	cu.mu.Lock()
	defer cu.mu.Unlock()

	if cu.TempFile != nil {
		cu.TempFile.Close()
	}
	return os.Remove(cu.TempPath)
}

// CleanupUpload removes an upload from tracking
func (cm *ChunkManager) CleanupUpload(token, filename string) {
	key := token + ":" + filename
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if upload, exists := cm.uploads[key]; exists {
		upload.Cleanup()
		delete(cm.uploads, key)
		log.Printf("[ChunkManager] Cleaned up upload: %s", key)
	}
}

// sanitizeFilename makes a filename safe for temp file naming
func sanitizeFilename(filename string) string {
	// Replace unsafe characters with underscore
	reg := regexp.MustCompile(`[^a-zA-Z0-9._-]`)
	return reg.ReplaceAllString(filename, "_")
}

// parseContentRange parses Content-Range header
// Format: bytes start-end/total
// Returns: start, end, total, ok
func parseContentRange(header string) (int64, int64, int64, bool) {
	if header == "" {
		return 0, 0, 0, false
	}

	// Format: bytes start-end/total
	var start, end, total int64
	n, err := fmt.Sscanf(header, "bytes %d-%d/%d", &start, &end, &total)
	if err != nil || n != 3 {
		log.Printf("[parseContentRange] Failed to parse: %s, err=%v", header, err)
		return 0, 0, 0, false
	}
	return start, end, total, true
}

// SeafHTTPHandler handles Seafile-compatible file operations
type SeafHTTPHandler struct {
	storage        *storage.S3Store
	storageManager *storage.Manager
	db             *db.DB
	tokenStore     TokenStore
	permMiddleware *middleware.PermissionMiddleware
}

// NewSeafHTTPHandler creates a new SeafHTTP handler
func NewSeafHTTPHandler(s3Store *storage.S3Store, storageManager *storage.Manager, database *db.DB, tokenStore TokenStore, permMiddleware *middleware.PermissionMiddleware) *SeafHTTPHandler {
	return &SeafHTTPHandler{
		storage:        s3Store,
		storageManager: storageManager,
		db:             database,
		tokenStore:     tokenStore,
		permMiddleware: permMiddleware,
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

		// ZIP download endpoint - creates a ZIP of a directory on-the-fly
		seafhttp.GET("/zip/:token", h.HandleZipDownload)
	}
}

// uploadBlockSize is the block size used when splitting large uploads into blocks.
// 8 MB matches Seafile's default CDC block size for good deduplication compatibility.
const uploadBlockSize = 8 * 1024 * 1024 // 8 MB

// HandleUpload handles file uploads via the upload token.
// Supports both single-shot uploads and chunked/resumable uploads (via Content-Range header).
// Large files are split into blocks and streamed to S3 — never fully loaded into RAM.
func (h *SeafHTTPHandler) HandleUpload(c *gin.Context) {
	tokenStr := c.Param("token")

	// Validate token
	token, valid := h.tokenStore.GetToken(tokenStr, TokenTypeUpload)
	if !valid {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired upload token"})
		return
	}

	// Permission check
	if h.permMiddleware != nil {
		hasWrite, err := h.permMiddleware.HasLibraryAccess(token.OrgID, token.UserID, token.RepoID, middleware.PermissionRW)
		if err != nil {
			log.Printf("[HandleUpload] Failed to check permissions: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check permissions"})
			return
		}
		if !hasWrite {
			log.Printf("[HandleUpload] Permission denied: user %q library %q", token.UserID, token.RepoID)
			c.JSON(http.StatusForbidden, gin.H{"error": "you do not have write permission to this library"})
			return
		}

		// Granular flag check: upload must be allowed
		c.Set("org_id", token.OrgID)
		c.Set("user_id", token.UserID)
		if !h.permMiddleware.RequirePermFlagForRepo(c, token.RepoID, "upload") {
			c.JSON(http.StatusForbidden, gin.H{"error": "upload is not allowed by your permission"})
			return
		}
	}

	if h.storage == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "storage not available"})
		return
	}

	// Quota pre-check — evaluated before reading the body so we can fail fast.
	// contentLength is an upper bound (includes multipart overhead); acceptable for quota checks.
	if checker := traffic.GetChecker(); checker != nil {
		contentLength := c.Request.ContentLength
		if contentLength < 0 {
			contentLength = 0
		}
		if st, _ := checker.CheckStorageQuota(token.OrgID, contentLength); !st.Allowed {
			c.JSON(http.StatusForbidden, gin.H{"error": "storage quota exceeded"})
			return
		}
		if st, _ := checker.CheckTrafficQuota(token.OrgID, token.UserID, "upload", contentLength); !st.Allowed {
			c.JSON(http.StatusForbidden, gin.H{"error": "traffic quota exceeded", "reason": st.Reason})
			return
		} else if st.Warning {
			c.Header("X-Quota-Warning", st.Reason)
		}
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
	relativePath := c.PostForm("relative_path")
	replaceStr := c.DefaultPostForm("replace", "1")
	replaceFile := replaceStr != "0"
	retJSON := c.Query("ret-json") == "1" || c.PostForm("ret-json") == "1"

	filename := header.Filename

	// Handle relative_path for folder uploads (e.g., "my-folder/subfolder/file.txt")
	if relativePath != "" {
		if strings.HasSuffix(relativePath, "/") {
			dirName := strings.TrimSuffix(relativePath, "/")
			dirBaseName := filepath.Base(dirName)

			if filename == dirBaseName || filename == relativePath || filename == "" {
				log.Printf("[HandleUpload] Skipping directory marker: %s (filename=%s)", relativePath, filename)
				if retJSON {
					c.JSON(http.StatusOK, []gin.H{{"name": dirBaseName, "id": "", "size": "0"}})
				} else {
					c.String(http.StatusOK, "")
				}
				return
			}

			log.Printf("[HandleUpload] File in directory: relativePath=%s, filename=%s", relativePath, filename)
			parentDir = filepath.Join(parentDir, dirName)
		} else {
			relDir := filepath.Dir(relativePath)
			if relDir != "." && relDir != "" {
				parentDir = filepath.Join(parentDir, relDir)
			}
			filename = filepath.Base(relativePath)
		}
	}

	if !strings.HasPrefix(parentDir, "/") {
		parentDir = "/" + parentDir
	}
	parentDir = filepath.Clean(parentDir)

	log.Printf("[HandleUpload] relativePath=%s, parentDir=%s, filename=%s", relativePath, parentDir, filename)

	filePath := filepath.Join(parentDir, filename)
	storageKey := fmt.Sprintf("%s/%s%s", token.OrgID, token.RepoID, filePath)

	// Check for Content-Range header (chunked upload)
	contentRange := c.GetHeader("Content-Range")
	start, end, total, isChunked := parseContentRange(contentRange)

	log.Printf("[HandleUpload] Token=%s, File=%s, ContentRange=%s, isChunked=%v",
		tokenStr, filename, contentRange, isChunked)

	if isChunked {
		// Chunked upload: stream chunk data directly to temp file (no io.ReadAll)
		upload, err := chunkManager.GetOrCreateUpload(tokenStr, filename, parentDir, total)
		if err != nil {
			log.Printf("[HandleUpload] Failed to create upload tracker: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to initialize upload"})
			return
		}

		// Stream chunk directly to temp file at the correct offset
		if err := upload.WriteChunkFromReader(file, start, end); err != nil {
			log.Printf("[HandleUpload] Failed to write chunk: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to write chunk"})
			return
		}

		if !upload.IsComplete() {
			log.Printf("[HandleUpload] Chunk received, waiting for more: %d/%d", end+1, total)
			c.JSON(http.StatusOK, gin.H{"success": true})
			return
		}

		// All chunks received — finalize by streaming from temp file
		log.Printf("[HandleUpload] All chunks received, finalizing upload (streaming)")
		fileID, actualFilename, err := h.finalizeUploadStreaming(c, token, upload, parentDir, filename, storageKey, total, replaceFile)
		chunkManager.CleanupUpload(tokenStr, filename)
		if err != nil {
			log.Printf("[HandleUpload] Finalization failed: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to finalize upload"})
			return
		}

		log.Printf("[HandleUpload] Upload complete: file=%s, size=%d, id=%s", actualFilename, total, fileID[:16])

		// Record traffic and storage — fire-and-forget, never blocks the response.
		if rec := traffic.Get(); rec != nil {
			tt := traffic.WebUpload
			if token.Source == "link" {
				tt = traffic.LinkUpload
			}
			rec.Record(token.OrgID, token.UserID, tt, total)
		}
		if h.db != nil {
			traffic.IncrementStorageCounters(h.db, token.OrgID, token.UserID, token.RepoID, total, 1)
		}

		if retJSON {
			c.JSON(http.StatusOK, []gin.H{{"name": actualFilename, "id": fileID, "size": strconv.FormatInt(total, 10)}})
		} else {
			c.String(http.StatusOK, fileID)
		}
		return
	}

	// Single-shot upload: for small files, use the simple path.
	// For large files (> uploadBlockSize), save to temp file first then stream.
	chunkData, err := io.ReadAll(file)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read file"})
		return
	}
	finalSize := int64(len(chunkData))

	// Generate file ID (SHA-1 of plaintext for Seafile compatibility)
	sha1Hash := sha1.Sum(chunkData)
	fileID := hex.EncodeToString(sha1Hash[:])

	// Check encryption
	var encrypted bool
	var storedContent = chunkData
	err = h.db.Session().Query(`
		SELECT encrypted FROM libraries WHERE org_id = ? AND library_id = ?
	`, token.OrgID, token.RepoID).Scan(&encrypted)
	if err != nil {
		log.Printf("[HandleUpload] Failed to check encryption status: %v", err)
	}

	if encrypted {
		fileKey, fileIV := v2.GetDecryptSessions().GetFileKeyAndIV(token.UserID, token.RepoID)
		if fileKey == nil {
			c.JSON(http.StatusForbidden, gin.H{"error": "library is encrypted and not unlocked"})
			return
		}
		encryptedContent, err := crypto.EncryptBlockSeafile(chunkData, fileKey, fileIV)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to encrypt content"})
			return
		}
		storedContent = encryptedContent
	}

	sha256Hash := sha256.Sum256(storedContent)
	sha256ID := hex.EncodeToString(sha256Hash[:])

	// Store using PutAuto (automatically uses multipart for large files)
	ctx := context.Background()
	blockStore, _, err := h.storageManager.GetHealthyBlockStore("")
	if err != nil {
		log.Printf("[HandleUpload] Failed to get block store: %v, falling back to S3", err)
		_, err = h.storage.PutAuto(c.Request.Context(), storageKey, newBytesReader(storedContent), int64(len(storedContent)))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to upload file"})
			return
		}
	} else {
		_, err = blockStore.PutBlockAuto(ctx, sha256ID, storedContent)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to store block"})
			return
		}
		log.Printf("[HandleUpload] Stored block %s (SHA-256: %s)", fileID[:16], sha256ID[:16])
	}

	// Create SHA-1 → SHA-256 mapping (dual-write: forward + reverse lookup)
	if err := h.db.Session().Query(`
		INSERT INTO block_id_mappings (org_id, external_id, internal_id) VALUES (?, ?, ?)
	`, token.OrgID, fileID, sha256ID).Exec(); err != nil {
		log.Printf("[HandleUpload] CRITICAL: Failed to write block_id_mapping org=%s ext=%s int=%s: %v", token.OrgID, fileID[:16], sha256ID[:16], err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create block mapping"})
		return
	}
	if err := h.db.Session().Query(`
		INSERT INTO block_id_mappings_by_internal (org_id, internal_id, external_id, created_at) VALUES (?, ?, ?, toTimestamp(now()))
	`, token.OrgID, sha256ID, fileID).Exec(); err != nil {
		log.Printf("[HandleUpload] WARNING: Failed to write reverse block_id_mapping org=%s int=%s ext=%s: %v", token.OrgID, sha256ID[:16], fileID[:16], err)
	}

	// Register block metadata for size lookups (used by video/audio Range requests)
	if err := h.db.Session().Query(`
		INSERT INTO blocks (org_id, block_id, size_bytes, created_at) VALUES (?, ?, ?, toTimestamp(now()))
	`, token.OrgID, sha256ID, len(storedContent)).Exec(); err != nil {
		log.Printf("[HandleUpload] WARNING: Failed to write block metadata org=%s block=%s: %v", token.OrgID, sha256ID[:16], err)
	}

	// Update filesystem metadata
	commitID, actualFilename, err := h.commitUploadedFile(token.OrgID, token.RepoID, token.UserID, parentDir, filename, fileID, chunkData, finalSize, replaceFile)
	if err != nil {
		log.Printf("[HandleUpload] Failed to update filesystem: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "file stored but metadata update failed"})
		return
	}
	log.Printf("[HandleUpload] Filesystem updated, commit=%s", commitID)

	log.Printf("[HandleUpload] Upload complete: file=%s, size=%d, id=%s", actualFilename, finalSize, fileID[:16])

	// Record traffic and storage — fire-and-forget, never blocks the response.
	if rec := traffic.Get(); rec != nil {
		tt := traffic.WebUpload
		if token.Source == "link" {
			tt = traffic.LinkUpload
		}
		rec.Record(token.OrgID, token.UserID, tt, finalSize)
	}
	if h.db != nil {
		traffic.IncrementStorageCounters(h.db, token.OrgID, token.UserID, token.RepoID, finalSize, 1)
	}

	if retJSON {
		c.JSON(http.StatusOK, []gin.H{{"name": actualFilename, "id": fileID, "size": strconv.FormatInt(finalSize, 10)}})
	} else {
		c.String(http.StatusOK, fileID)
	}
}

// finalizeUploadStreaming processes a completed chunked upload by streaming from the temp file.
// It reads the file in blocks, hashes and stores each block individually — O(blockSize) RAM.
func (h *SeafHTTPHandler) finalizeUploadStreaming(c *gin.Context, token *AccessToken, upload *ChunkUpload, parentDir, filename, storageKey string, totalSize int64, replace bool) (string, string, error) {
	ctx := context.Background()

	// Get the temp file reader
	reader, err := upload.GetReader()
	if err != nil {
		return "", "", fmt.Errorf("failed to get upload reader: %w", err)
	}

	// Check encryption
	var encrypted bool
	var fileKey, fileIV []byte
	h.db.Session().Query(`
		SELECT encrypted FROM libraries WHERE org_id = ? AND library_id = ?
	`, token.OrgID, token.RepoID).Scan(&encrypted)
	if encrypted {
		fileKey, fileIV = v2.GetDecryptSessions().GetFileKeyAndIV(token.UserID, token.RepoID)
		if fileKey == nil {
			return "", "", fmt.Errorf("library is encrypted but not unlocked")
		}
	}

	blockStore, _, err := h.storageManager.GetHealthyBlockStore("")
	if err != nil {
		return "", "", fmt.Errorf("block store not available: %w", err)
	}

	// Stream through the file in blocks, computing SHA-1 incrementally
	sha1Hasher := sha1.New()
	var blockSHA1IDs []string // SHA-1 block IDs for fs_object (Seafile compat)
	buf := make([]byte, uploadBlockSize)

	for {
		n, readErr := io.ReadFull(reader, buf)
		if n == 0 {
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				return "", "", fmt.Errorf("read error: %w", readErr)
			}
		}

		blockData := buf[:n]

		// Accumulate SHA-1 of plaintext for the overall file ID
		sha1Hasher.Write(blockData)

		// Block-level SHA-1 ID (for Seafile compatibility / fs_object block_ids)
		blockSHA1Hash := sha1.Sum(blockData)
		blockSHA1ID := hex.EncodeToString(blockSHA1Hash[:])
		blockSHA1IDs = append(blockSHA1IDs, blockSHA1ID)

		// Encrypt block if needed
		storedBlock := blockData
		if fileKey != nil {
			storedBlock, err = crypto.EncryptBlockSeafile(blockData, fileKey, fileIV)
			if err != nil {
				return "", "", fmt.Errorf("failed to encrypt block: %w", err)
			}
		}

		// SHA-256 of stored content for block storage key
		sha256Hash := sha256.Sum256(storedBlock)
		sha256ID := hex.EncodeToString(sha256Hash[:])

		// Store block with PutBlockAuto (uses multipart for large blocks)
		_, err = blockStore.PutBlockAuto(ctx, sha256ID, storedBlock)
		if err != nil {
			return "", "", fmt.Errorf("failed to store block: %w", err)
		}

		// Create SHA-1 → SHA-256 mapping (dual-write: forward + reverse lookup)
		if err := h.db.Session().Query(`
			INSERT INTO block_id_mappings (org_id, external_id, internal_id) VALUES (?, ?, ?)
		`, token.OrgID, blockSHA1ID, sha256ID).Exec(); err != nil {
			log.Printf("[finalizeUploadStreaming] CRITICAL: Failed to write block_id_mapping org=%s ext=%s int=%s: %v", token.OrgID, blockSHA1ID[:16], sha256ID[:16], err)
			return "", "", fmt.Errorf("failed to create block mapping: %w", err)
		}
		if err := h.db.Session().Query(`
			INSERT INTO block_id_mappings_by_internal (org_id, internal_id, external_id, created_at) VALUES (?, ?, ?, toTimestamp(now()))
		`, token.OrgID, sha256ID, blockSHA1ID).Exec(); err != nil {
			log.Printf("[finalizeUploadStreaming] WARNING: Failed to write reverse block_id_mapping org=%s int=%s ext=%s: %v", token.OrgID, sha256ID[:16], blockSHA1ID[:16], err)
		}

		// Register block metadata for size lookups (used by video/audio Range requests)
		if err := h.db.Session().Query(`
			INSERT INTO blocks (org_id, block_id, size_bytes, created_at) VALUES (?, ?, ?, toTimestamp(now()))
		`, token.OrgID, sha256ID, len(storedBlock)).Exec(); err != nil {
			log.Printf("[finalizeUploadStreaming] WARNING: Failed to write block metadata org=%s block=%s: %v", token.OrgID, sha256ID[:16], err)
		}

		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			break
		}
	}

	// File ID = SHA-1 of the complete plaintext
	fileID := hex.EncodeToString(sha1Hasher.Sum(nil))

	log.Printf("[finalizeUploadStreaming] Stored %d blocks for file %s (size=%d)", len(blockSHA1IDs), fileID[:16], totalSize)

	// Update filesystem metadata with multiple block IDs
	commitID, actualFilename, err := h.commitUploadedFileMultiBlock(token.OrgID, token.RepoID, token.UserID, parentDir, filename, fileID, blockSHA1IDs, totalSize, replace)
	if err != nil {
		return "", "", fmt.Errorf("failed to update filesystem metadata: %w", err)
	}
	log.Printf("[finalizeUploadStreaming] Filesystem updated, commit=%s", commitID)

	return fileID, actualFilename, nil
}

// commitUploadedFileMultiBlock is like commitUploadedFile but supports multiple block IDs.
// Used for large files that are split into multiple blocks during upload.
// Returns the commit ID, the actual filename used (may differ if auto-renamed), and any error.
func (h *SeafHTTPHandler) commitUploadedFileMultiBlock(orgID, repoID, userID, parentDir, filename, fileID string, blockIDs []string, fileSize int64, replace bool) (string, string, error) {
	var headCommitID string
	err := h.db.Session().Query(`
		SELECT head_commit_id FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(&headCommitID)
	if err != nil {
		return "", "", fmt.Errorf("failed to get head commit: %w", err)
	}

	var rootFSID string
	err = h.db.Session().Query(`
		SELECT root_fs_id FROM commits WHERE library_id = ? AND commit_id = ?
	`, repoID, headCommitID).Scan(&rootFSID)
	if err != nil {
		return "", "", fmt.Errorf("failed to get root fs_id: %w", err)
	}

	log.Printf("[commitUploadedFileMultiBlock] headCommit=%s, rootFSID=%s, parentDir=%s, filename=%s, blocks=%d",
		headCommitID, rootFSID, parentDir, filename, len(blockIDs))

	// Seafile format: {"block_ids":[...],"size":N,"type":1,"version":1}
	fsContent := map[string]interface{}{
		"version":   1,
		"type":      1,
		"block_ids": blockIDs,
		"size":      fileSize,
	}
	fsContentJSON, err := json.Marshal(fsContent)
	if err != nil {
		return "", "", fmt.Errorf("failed to marshal fs content: %w", err)
	}
	fsHash := sha1.Sum(fsContentJSON)
	fileFSID := hex.EncodeToString(fsHash[:])

	// Add file to directory (may auto-rename if replace=false and file exists)
	newRootFSID, actualFilename, err := h.addFileToDirectory(repoID, rootFSID, parentDir, filename, fileFSID, fileSize, userID, replace)
	if err != nil {
		return "", "", fmt.Errorf("failed to add file to directory: %w", err)
	}

	// Compute full path using actual filename (may have been auto-renamed)
	var fullPath string
	if parentDir == "/" {
		fullPath = "/" + actualFilename
	} else {
		fullPath = parentDir + "/" + actualFilename
	}

	err = h.db.Session().Query(`
		INSERT INTO fs_objects (library_id, fs_id, obj_type, obj_name, full_path, size_bytes, mtime, block_ids)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, repoID, fileFSID, "file", actualFilename, fullPath, fileSize, time.Now().Unix(), blockIDs).Exec()
	if err != nil {
		return "", "", fmt.Errorf("failed to create file fs_object: %w", err)
	}

	description := fmt.Sprintf("Added or modified \"%s\".\n", actualFilename)
	commitData := fmt.Sprintf("%s:%s:%s:%d", repoID, newRootFSID, description, time.Now().UnixNano())
	commitHash := sha1.Sum([]byte(commitData))
	newCommitID := hex.EncodeToString(commitHash[:])

	err = h.db.Session().Query(`
		INSERT INTO commits (library_id, commit_id, parent_id, root_fs_id, creator_id, description, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, repoID, newCommitID, headCommitID, newRootFSID, userID, description, time.Now()).Exec()
	if err != nil {
		return "", "", fmt.Errorf("failed to create commit: %w", err)
	}

	fsHelper := v2.NewFSHelper(h.db)
	if err := fsHelper.UpdateLibraryHead(orgID, repoID, newCommitID); err != nil {
		log.Printf("[commitUploadedFileMultiBlock] Warning: failed to update library head: %v", err)
	}

	log.Printf("[commitUploadedFileMultiBlock] Created commit %s with root %s", newCommitID, newRootFSID)
	return newCommitID, actualFilename, nil
}

// commitUploadedFile updates the filesystem metadata after a file upload.
// When replace is false and a file with the same name exists, it auto-renames to "name (1).ext".
// Returns the commit ID, the actual filename used (may differ if auto-renamed), and any error.
func (h *SeafHTTPHandler) commitUploadedFile(orgID, repoID, userID, parentDir, filename, fileID string, content []byte, fileSize int64, replace bool) (string, string, error) {
	// Get current head commit
	var headCommitID string
	err := h.db.Session().Query(`
		SELECT head_commit_id FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(&headCommitID)
	if err != nil {
		return "", "", fmt.Errorf("failed to get head commit: %w", err)
	}

	// Get root fs_id from head commit
	var rootFSID string
	err = h.db.Session().Query(`
		SELECT root_fs_id FROM commits WHERE library_id = ? AND commit_id = ?
	`, repoID, headCommitID).Scan(&rootFSID)
	if err != nil {
		return "", "", fmt.Errorf("failed to get root fs_id: %w", err)
	}

	log.Printf("[commitUploadedFile] headCommit=%s, rootFSID=%s, parentDir=%s, filename=%s",
		headCommitID, rootFSID, parentDir, filename)

	// Create fs_object for the file (single block for now)
	// The block_id is the SHA-1 of the PLAINTEXT content (for Seafile client compatibility)
	blockID := fileID // Use the file content hash as block ID

	// CRITICAL: fs_id must be SHA-1 of the fs_object JSON content (not file content)
	// This is how Seafile verifies fs_object integrity in pack-fs
	// Seafile format: {"block_ids":["..."],"size":N,"type":1,"version":1} (alphabetical keys)
	fsContent := map[string]interface{}{
		"version":   1,
		"type":      1, // SEAF_METADATA_TYPE_FILE
		"block_ids": []string{blockID},
		"size":      fileSize,
	}
	fsContentJSON, err := json.Marshal(fsContent)
	if err != nil {
		return "", "", fmt.Errorf("failed to marshal fs content: %w", err)
	}
	fsHash := sha1.Sum(fsContentJSON)
	fileFSID := hex.EncodeToString(fsHash[:])

	log.Printf("[commitUploadedFile] File fs_id computed: %s (from JSON: %s)", fileFSID, string(fsContentJSON))

	// Navigate to parent directory and add file (may auto-rename if replace=false)
	newRootFSID, actualFilename, err := h.addFileToDirectory(repoID, rootFSID, parentDir, filename, fileFSID, fileSize, userID, replace)
	if err != nil {
		return "", "", fmt.Errorf("failed to add file to directory: %w", err)
	}

	// Compute full path for search indexing (use actual filename which may have been auto-renamed)
	var fullPath string
	if parentDir == "/" {
		fullPath = "/" + actualFilename
	} else {
		fullPath = parentDir + "/" + actualFilename
	}

	// Store file fs_object with correct fs_id and full_path
	err = h.db.Session().Query(`
		INSERT INTO fs_objects (library_id, fs_id, obj_type, obj_name, full_path, size_bytes, mtime, block_ids)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, repoID, fileFSID, "file", actualFilename, fullPath, fileSize, time.Now().Unix(), []string{blockID}).Exec()
	if err != nil {
		return "", "", fmt.Errorf("failed to create file fs_object: %w", err)
	}
	log.Printf("[commitUploadedFile] Created file fs_object: %s at %s", fileFSID, fullPath)

	// Create new commit
	description := fmt.Sprintf("Added or modified \"%s\".\n", actualFilename)
	commitData := fmt.Sprintf("%s:%s:%s:%d", repoID, newRootFSID, description, time.Now().UnixNano())
	commitHash := sha1.Sum([]byte(commitData))
	newCommitID := hex.EncodeToString(commitHash[:])

	err = h.db.Session().Query(`
		INSERT INTO commits (library_id, commit_id, parent_id, root_fs_id, creator_id, description, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, repoID, newCommitID, headCommitID, newRootFSID, userID, description, time.Now()).Exec()
	if err != nil {
		return "", "", fmt.Errorf("failed to create commit: %w", err)
	}

	// Update library head, size, and file count via FSHelper
	// CRITICAL: Both tables must be updated for consistency
	// GetRootFSID reads from libraries_by_id, so it must have the latest head_commit_id
	fsHelper := v2.NewFSHelper(h.db)
	if err := fsHelper.UpdateLibraryHead(orgID, repoID, newCommitID); err != nil {
		return "", "", fmt.Errorf("failed to update library head: %w", err)
	}

	log.Printf("[commitUploadedFile] Created commit %s with root %s", newCommitID, newRootFSID)
	return newCommitID, actualFilename, nil
}

// addFileToDirectory adds a file entry to a directory, creating parent directories as needed.
// When replace is false and a file with the same name exists, it auto-renames (e.g., "file (1).txt").
// Returns the new root fs_id, the actual filename used (may differ if auto-renamed), and any error.
func (h *SeafHTTPHandler) addFileToDirectory(repoID, rootFSID, parentDir, filename, fileID string, fileSize int64, userID string, replace bool) (string, string, error) {
	parentDir = strings.TrimSuffix(parentDir, "/")
	if parentDir == "" {
		parentDir = "/"
	}

	log.Printf("[addFileToDirectory] rootFSID=%s, parentDir=%s, filename=%s, replace=%v", rootFSID, parentDir, filename, replace)

	// Get root directory entries
	var rootEntriesJSON string
	err := h.db.Session().Query(`
		SELECT dir_entries FROM fs_objects WHERE library_id = ? AND fs_id = ?
	`, repoID, rootFSID).Scan(&rootEntriesJSON)
	if err != nil {
		return "", "", fmt.Errorf("failed to get root entries: %w", err)
	}

	var rootEntries []map[string]interface{}
	if rootEntriesJSON != "" && rootEntriesJSON != "[]" {
		if err := json.Unmarshal([]byte(rootEntriesJSON), &rootEntries); err != nil {
			return "", "", fmt.Errorf("failed to parse root entries: %w", err)
		}
	}

	if parentDir == "/" {
		actualFilename := filename
		if !replace {
			actualFilename = autoRenameIfExists(filename, rootEntries)
		}

		newEntry := map[string]interface{}{
			"id":       fileID,
			"name":     actualFilename,
			"mode":     33188, // Regular file
			"mtime":    time.Now().Unix(),
			"size":     fileSize,
			"modifier": userID + "@sesamefs.local",
		}

		// Check if file already exists and update it, otherwise add new entry
		found := false
		for i, entry := range rootEntries {
			if entry["name"] == actualFilename {
				rootEntries[i] = newEntry
				found = true
				break
			}
		}
		if !found {
			rootEntries = append(rootEntries, newEntry)
		}

		// Create new root fs_object
		fsID, err := h.createDirectoryFSObject(repoID, rootEntries)
		if err != nil {
			return "", "", err
		}
		return fsID, actualFilename, nil
	}

	// Need to traverse and possibly create parent directories
	parts := strings.Split(strings.Trim(parentDir, "/"), "/")
	return h.traverseAndAddFile(repoID, rootFSID, rootEntries, parts, 0, filename, fileID, fileSize, userID, replace)
}

// autoRenameIfExists generates a unique filename if the given name already exists in the directory entries.
// E.g., "file.txt" becomes "file (1).txt", "file (2).txt", etc.
func autoRenameIfExists(filename string, entries []map[string]interface{}) string {
	// Check if the filename exists
	exists := false
	for _, entry := range entries {
		if entry["name"] == filename {
			exists = true
			break
		}
	}
	if !exists {
		return filename
	}

	// Split into name and extension
	ext := filepath.Ext(filename)
	base := strings.TrimSuffix(filename, ext)

	// Try "name (1).ext", "name (2).ext", etc.
	for i := 1; i < 1000; i++ {
		candidate := fmt.Sprintf("%s (%d)%s", base, i, ext)
		found := false
		for _, entry := range entries {
			if entry["name"] == candidate {
				found = true
				break
			}
		}
		if !found {
			return candidate
		}
	}
	// Fallback: use timestamp
	return fmt.Sprintf("%s (%d)%s", base, time.Now().UnixNano(), ext)
}

// traverseAndAddFile recursively traverses/creates directories and adds a file.
// Returns the new directory fs_id, the actual filename (may be auto-renamed), and any error.
func (h *SeafHTTPHandler) traverseAndAddFile(repoID string, currentFSID string, entries []map[string]interface{}, pathParts []string, depth int, filename, fileID string, fileSize int64, userID string, replace bool) (string, string, error) {
	if depth >= len(pathParts) {
		// We've reached the target directory, add the file
		actualFilename := filename
		if !replace {
			actualFilename = autoRenameIfExists(filename, entries)
		}

		newEntry := map[string]interface{}{
			"id":       fileID,
			"name":     actualFilename,
			"mode":     33188,
			"mtime":    time.Now().Unix(),
			"size":     fileSize,
			"modifier": userID + "@sesamefs.local",
		}

		found := false
		for i, entry := range entries {
			if entry["name"] == actualFilename {
				entries[i] = newEntry
				found = true
				break
			}
		}
		if !found {
			entries = append(entries, newEntry)
		}

		fsID, err := h.createDirectoryFSObject(repoID, entries)
		if err != nil {
			return "", "", err
		}
		return fsID, actualFilename, nil
	}

	dirName := pathParts[depth]
	var childFSID string
	var childEntries []map[string]interface{}
	childIdx := -1

	// Look for existing directory
	for i, entry := range entries {
		if entry["name"] == dirName {
			childFSID = entry["id"].(string)
			childIdx = i

			// Get child directory entries
			var childEntriesJSON string
			err := h.db.Session().Query(`
				SELECT dir_entries FROM fs_objects WHERE library_id = ? AND fs_id = ?
			`, repoID, childFSID).Scan(&childEntriesJSON)
			if err != nil {
				return "", "", fmt.Errorf("failed to get child directory: %w", err)
			}
			if childEntriesJSON != "" && childEntriesJSON != "[]" {
				json.Unmarshal([]byte(childEntriesJSON), &childEntries)
			}
			break
		}
	}

	if childFSID == "" {
		// Create new directory
		childEntries = []map[string]interface{}{}
	}

	// Recursively process
	newChildFSID, actualFilename, err := h.traverseAndAddFile(repoID, childFSID, childEntries, pathParts, depth+1, filename, fileID, fileSize, userID, replace)
	if err != nil {
		return "", "", err
	}

	// Update or add directory entry in current level
	dirEntry := map[string]interface{}{
		"id":       newChildFSID,
		"name":     dirName,
		"mode":     16384, // Directory (040000)
		"mtime":    time.Now().Unix(),
		"size":     0,
		"modifier": userID + "@sesamefs.local",
	}

	if childIdx >= 0 {
		entries[childIdx] = dirEntry
	} else {
		entries = append(entries, dirEntry)
	}

	fsID, err := h.createDirectoryFSObject(repoID, entries)
	if err != nil {
		return "", "", err
	}
	return fsID, actualFilename, nil
}

// createDirectoryFSObject creates a new directory fs_object and returns its ID
func (h *SeafHTTPHandler) createDirectoryFSObject(repoID string, entries []map[string]interface{}) (string, error) {
	entriesJSON, err := json.Marshal(entries)
	if err != nil {
		return "", fmt.Errorf("failed to marshal entries: %w", err)
	}

	// Calculate fs_id as SHA-1 of the EXACT JSON that will be returned by pack-fs
	// Seafile format: {"dirents":[...],"type":3,"version":1} (alphabetical key order)
	// CRITICAL: The hash MUST match what pack-fs sends, or sync will fail.
	// Using map[string]interface{} ensures keys are serialized alphabetically.
	fsContent := map[string]interface{}{
		"version": 1,
		"type":    3, // SEAF_METADATA_TYPE_DIR
		"dirents": json.RawMessage(entriesJSON),
	}
	fsContentJSON, err := json.Marshal(fsContent)
	if err != nil {
		return "", fmt.Errorf("failed to marshal fs content: %w", err)
	}
	hash := sha1.Sum(fsContentJSON)
	fsID := hex.EncodeToString(hash[:])

	// Store in database
	err = h.db.Session().Query(`
		INSERT INTO fs_objects (library_id, fs_id, obj_type, dir_entries, mtime)
		VALUES (?, ?, ?, ?, ?)
	`, repoID, fsID, "dir", string(entriesJSON), time.Now().Unix()).Exec()
	if err != nil {
		return "", fmt.Errorf("failed to create directory fs_object: %w", err)
	}

	log.Printf("[createDirectoryFSObject] Created dir fs_object: %s with %d entries", fsID, len(entries))
	return fsID, nil
}

// HandleDownload handles file downloads via the download token.
// Streams content block-by-block to avoid loading entire files into RAM.
func (h *SeafHTTPHandler) HandleDownload(c *gin.Context) {
	tokenStr := c.Param("token")
	requestedPath := c.Param("filepath")

	log.Printf("[HandleDownload] Token: %s, RequestedPath: %s", tokenStr, requestedPath)

	// Validate token
	token, valid := h.tokenStore.GetToken(tokenStr, TokenTypeDownload)
	if !valid {
		log.Printf("[HandleDownload] Invalid token: %s", tokenStr)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired download token"})
		return
	}

	log.Printf("[HandleDownload] Token valid: OrgID=%s, RepoID=%s, Path=%s", token.OrgID, token.RepoID, token.Path)

	// Permission check: user must have read access to the library
	if h.permMiddleware != nil {
		hasRead, err := h.permMiddleware.HasLibraryAccess(token.OrgID, token.UserID, token.RepoID, middleware.PermissionR)
		if err != nil {
			log.Printf("[HandleDownload] Failed to check permissions: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check permissions"})
			return
		}
		if !hasRead {
			c.JSON(http.StatusForbidden, gin.H{"error": "you do not have read access to this library"})
			return
		}

		// Granular flag check: download must be allowed
		c.Set("org_id", token.OrgID)
		c.Set("user_id", token.UserID)
		if !h.permMiddleware.RequirePermFlagForRepo(c, token.RepoID, "download") {
			c.JSON(http.StatusForbidden, gin.H{"error": "download is not allowed by your permission"})
			return
		}
	}

	// Get filename from path
	filename := filepath.Base(token.Path)
	if requestedPath != "" && requestedPath != "/" {
		filename = filepath.Base(requestedPath)
	}

	// Quota pre-check: block if org is already over traffic quota.
	// We don't know the file size yet, so we check the current usage only.
	if checker := traffic.GetChecker(); checker != nil {
		if st, _ := checker.CheckTrafficQuota(token.OrgID, token.UserID, "download", 0); !st.Allowed {
			c.JSON(http.StatusForbidden, gin.H{"error": "traffic quota exceeded", "reason": st.Reason})
			return
		} else if st.Warning {
			c.Header("X-Quota-Warning", st.Reason)
		}
	}

	// Try to stream file from block storage (content-addressed)
	// This is the normal flow for SesameFS files
	if h.db != nil && h.storageManager != nil {
		log.Printf("[HandleDownload] Attempting block-based streaming download")
		err := h.streamFileFromBlocks(c, token, filename)
		if err == nil {
			return
		}
		log.Printf("[HandleDownload] Block-based streaming FAILED: %v", err)
		// If block-based retrieval fails, fall back to direct S3 path-based retrieval
	} else {
		log.Printf("[HandleDownload] Block storage not available (db=%v, storageManager=%v)", h.db != nil, h.storageManager != nil)
	}

	// Fallback: Stream directly from S3 (legacy path)
	if h.storage == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "storage not available"})
		return
	}

	storageKey := fmt.Sprintf("%s/%s%s", token.OrgID, token.RepoID, token.Path)

	reader, err := h.storage.Get(c.Request.Context(), storageKey)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
		return
	}
	defer reader.Close()

	// Stream directly to response — never load full file into RAM
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Header("Content-Type", "application/octet-stream")
	c.Status(http.StatusOK)
	bytesBefore := int64(c.Writer.Size())
	buf := streaming.GetCopyBuf()
	defer streaming.PutCopyBuf(buf)
	if _, err := io.CopyBuffer(c.Writer, reader, buf); err != nil {
		log.Printf("[HandleDownload] Streaming error: %v", err)
	}

	// Record traffic for the S3 fallback path.
	if rec := traffic.Get(); rec != nil {
		sent := int64(c.Writer.Size()) - bytesBefore
		if sent > 0 {
			tt := traffic.WebDownload
			if token.Source == "link" {
				tt = traffic.LinkDownload
			}
			rec.Record(token.OrgID, token.UserID, tt, sent)
		}
	}
}

// resolveBlockID translates a SHA-1 block ID (40 chars) to SHA-256 (64 chars) if needed.
func (h *SeafHTTPHandler) resolveBlockID(orgID, blockID string) string {
	if len(blockID) != 40 {
		return blockID
	}
	var mappedID string
	err := h.db.Session().Query(`
		SELECT internal_id FROM block_id_mappings WHERE org_id = ? AND external_id = ?
	`, orgID, blockID).Scan(&mappedID)
	if err == nil && mappedID != "" {
		return mappedID
	}
	return blockID
}

// lookupFileBlocks resolves a token's path to its block IDs, file size, encryption key, and block store.
// This is the common metadata lookup used by both download and streaming paths.
func (h *SeafHTTPHandler) lookupFileBlocks(token *AccessToken) (blockIDs []string, fileSize int64, fileKey []byte, blockStore *storage.BlockStore, err error) {
	// Check encryption
	var encrypted bool
	err = h.db.Session().Query(`
		SELECT encrypted FROM libraries WHERE org_id = ? AND library_id = ?
	`, token.OrgID, token.RepoID).Scan(&encrypted)
	if err != nil {
		return nil, 0, nil, nil, fmt.Errorf("failed to check library encryption: %w", err)
	}

	if encrypted {
		fileKey = v2.GetDecryptSessions().GetFileKey(token.UserID, token.RepoID)
		if fileKey == nil {
			return nil, 0, nil, nil, fmt.Errorf("library is encrypted but not unlocked")
		}
	}

	// Get head commit → root FS
	var headCommit string
	err = h.db.Session().Query(`
		SELECT head_commit_id FROM libraries WHERE org_id = ? AND library_id = ?
	`, token.OrgID, token.RepoID).Scan(&headCommit)
	if err != nil {
		return nil, 0, nil, nil, fmt.Errorf("library not found: %w", err)
	}

	var rootFSID string
	err = h.db.Session().Query(`
		SELECT root_fs_id FROM commits WHERE library_id = ? AND commit_id = ?
	`, token.RepoID, headCommit).Scan(&rootFSID)
	if err != nil {
		return nil, 0, nil, nil, fmt.Errorf("commit not found: %w", err)
	}

	// Navigate directory tree to the target file
	filePath := token.Path
	if !strings.HasPrefix(filePath, "/") {
		filePath = "/" + filePath
	}
	pathParts := strings.Split(strings.Trim(filePath, "/"), "/")
	if len(pathParts) == 0 || (len(pathParts) == 1 && pathParts[0] == "") {
		return nil, 0, nil, nil, fmt.Errorf("invalid file path")
	}

	currentFSID := rootFSID
	for i := 0; i < len(pathParts)-1; i++ {
		nextFSID, err := h.findEntryInDir(token.RepoID, currentFSID, pathParts[i])
		if err != nil {
			return nil, 0, nil, nil, fmt.Errorf("directory not found: %s: %w", pathParts[i], err)
		}
		currentFSID = nextFSID
	}

	targetName := pathParts[len(pathParts)-1]
	fileFSID, err := h.findEntryInDir(token.RepoID, currentFSID, targetName)
	if err != nil {
		return nil, 0, nil, nil, fmt.Errorf("file not found: %s: %w", targetName, err)
	}

	// Get block IDs and file size from fs_object
	err = h.db.Session().Query(`
		SELECT block_ids, size_bytes FROM fs_objects WHERE library_id = ? AND fs_id = ?
	`, token.RepoID, fileFSID).Scan(&blockIDs, &fileSize)
	if err != nil {
		return nil, 0, nil, nil, fmt.Errorf("file metadata not found: %w", err)
	}

	blockStore, _, err = h.storageManager.GetHealthyBlockStore("")
	if err != nil {
		return nil, 0, nil, nil, fmt.Errorf("block store not available: %w", err)
	}

	return blockIDs, fileSize, fileKey, blockStore, nil
}

// streamFileFromBlocks streams a file's blocks directly to the HTTP response.
// Uses prefetching (overlap S3 fetch with HTTP write) and 4MB io.CopyBuffer
// for maximum throughput. Only O(2 × block_size) RAM.
func (h *SeafHTTPHandler) streamFileFromBlocks(c *gin.Context, token *AccessToken, filename string) error {
	blockIDs, fileSize, fileKey, blockStore, err := h.lookupFileBlocks(token)
	if err != nil {
		return err
	}

	log.Printf("[streamFileFromBlocks] Streaming %d blocks, size=%d, encrypted=%v", len(blockIDs), fileSize, fileKey != nil)

	// Set headers before streaming — Content-Length lets clients show progress
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Header("Content-Type", "application/octet-stream")
	if fileSize > 0 && fileKey == nil {
		// Only set Content-Length for unencrypted files where we know the exact size.
		// Encrypted blocks may differ in size after decryption.
		c.Header("Content-Length", strconv.FormatInt(fileSize, 10))
	}
	c.Status(http.StatusOK)

	// Batch resolve all block IDs upfront (avoids per-block Cassandra queries)
	resolvedIDs := streaming.BatchResolveBlockIDs(h.db, token.OrgID, blockIDs)

	// Stream with prefetching pipeline
	streaming.StreamBlocks(c, c.Request.Context(), blockStore, resolvedIDs, fileKey, "streamFileFromBlocks")

	log.Printf("[streamFileFromBlocks] Streaming complete: %d blocks", len(blockIDs))

	// Record traffic — fire-and-forget, never blocks the response.
	if rec := traffic.Get(); rec != nil {
		tt := traffic.WebDownload
		if token.Source == "link" {
			tt = traffic.LinkDownload
		}
		rec.Record(token.OrgID, token.UserID, tt, fileSize)
	}

	return nil
}

// getFileFromBlocks retrieves a file by loading all blocks into memory.
// DEPRECATED: Use streamFileFromBlocks for downloads. This is kept only for
// upload metadata (commitUploadedFile) where the full content is already in memory.
func (h *SeafHTTPHandler) getFileFromBlocks(c *gin.Context, token *AccessToken) ([]byte, error) {
	blockIDs, _, fileKey, blockStore, err := h.lookupFileBlocks(token)
	if err != nil {
		return nil, err
	}

	ctx := c.Request.Context()
	var content bytes.Buffer
	for _, blockID := range blockIDs {
		internalID := h.resolveBlockID(token.OrgID, blockID)

		blockData, err := blockStore.GetBlock(ctx, internalID)
		if err != nil {
			return nil, fmt.Errorf("failed to retrieve block %s: %w", blockID, err)
		}

		if fileKey != nil {
			blockData, err = crypto.DecryptBlock(blockData, fileKey)
			if err != nil {
				return nil, fmt.Errorf("failed to decrypt block %s: %w", blockID, err)
			}
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

	// Parse dir_entries as JSON array - proper JSON parsing instead of string matching
	// This handles any JSON formatting (with or without spaces)
	var entries []map[string]interface{}
	if dirEntries == "" || dirEntries == "[]" {
		log.Printf("[findEntryInDir] Directory is empty")
		return "", fmt.Errorf("entry not found: %s", entryName)
	}

	if err := json.Unmarshal([]byte(dirEntries), &entries); err != nil {
		log.Printf("[findEntryInDir] ERROR: Failed to parse dir_entries JSON: %v", err)
		// Log a snippet for debugging
		if len(dirEntries) > 500 {
			log.Printf("[findEntryInDir] Dir entries (first 500 chars): %s", dirEntries[:500])
		} else {
			log.Printf("[findEntryInDir] Dir entries: %s", dirEntries)
		}
		return "", fmt.Errorf("malformed directory entries: %w", err)
	}

	log.Printf("[findEntryInDir] Parsed %d entries from directory", len(entries))

	// Search for the entry by name
	for _, entry := range entries {
		name, ok := entry["name"].(string)
		if !ok {
			continue
		}
		if name == entryName {
			id, ok := entry["id"].(string)
			if !ok {
				log.Printf("[findEntryInDir] ERROR: Entry found but ID is not a string: %v", entry["id"])
				return "", fmt.Errorf("malformed entry ID for: %s", entryName)
			}
			log.Printf("[findEntryInDir] Found entry '%s' with ID: %s", entryName, id)
			return id, nil
		}
	}

	// Entry not found - log available entries for debugging
	log.Printf("[findEntryInDir] Entry '%s' not found in directory. Available entries:", entryName)
	for i, entry := range entries {
		if i < 10 { // Log first 10 entries
			log.Printf("[findEntryInDir]   - %v", entry["name"])
		}
	}
	if len(entries) > 10 {
		log.Printf("[findEntryInDir]   ... and %d more entries", len(entries)-10)
	}

	return "", fmt.Errorf("entry not found: %s", entryName)
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

// HandleZipDownload creates a ZIP archive of a directory on-the-fly and streams it.
// GET /seafhttp/zip/:token
func (h *SeafHTTPHandler) HandleZipDownload(c *gin.Context) {
	tokenStr := c.Param("token")

	token, valid := h.tokenStore.GetToken(tokenStr, TokenTypeDownload)
	if !valid {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired download token"})
		return
	}

	// Permission check
	if h.permMiddleware != nil {
		hasRead, err := h.permMiddleware.HasLibraryAccess(token.OrgID, token.UserID, token.RepoID, middleware.PermissionR)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check permissions"})
			return
		}
		if !hasRead {
			c.JSON(http.StatusForbidden, gin.H{"error": "you do not have read access to this library"})
			return
		}
	}

	if h.db == nil || h.storageManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "storage not available"})
		return
	}

	// Quota pre-check — reject early if traffic quota is already exhausted.
	if checker := traffic.GetChecker(); checker != nil {
		if st, _ := checker.CheckTrafficQuota(token.OrgID, token.UserID, "download", 0); !st.Allowed {
			c.JSON(http.StatusForbidden, gin.H{"error": "traffic quota exceeded", "reason": st.Reason})
			return
		} else if st.Warning {
			c.Header("X-Quota-Warning", st.Reason)
		}
	}

	// Get the library's root FS
	var headCommit string
	err := h.db.Session().Query(`
		SELECT head_commit_id FROM libraries
		WHERE org_id = ? AND library_id = ?
	`, token.OrgID, token.RepoID).Scan(&headCommit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "library not found"})
		return
	}

	var rootFSID string
	err = h.db.Session().Query(`
		SELECT root_fs_id FROM commits
		WHERE library_id = ? AND commit_id = ?
	`, token.RepoID, headCommit).Scan(&rootFSID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "commit not found"})
		return
	}

	// Navigate to the target directory and determine the correct folder name
	targetFSID := rootFSID
	dirName := ""

	// Normalize the path
	normalizedPath := strings.TrimSuffix(strings.TrimSpace(token.Path), "/")
	if normalizedPath == "" {
		normalizedPath = "/"
	}

	if normalizedPath == "/" {
		// Root directory: use library name
		var libraryName string
		err = h.db.Session().Query(`
			SELECT name FROM libraries WHERE org_id = ? AND library_id = ?
		`, token.OrgID, token.RepoID).Scan(&libraryName)
		if err != nil || libraryName == "" {
			dirName = "library"
		} else {
			dirName = libraryName
		}
	} else {
		// Subdirectory: use the directory name
		pathParts := strings.Split(strings.Trim(normalizedPath, "/"), "/")
		if len(pathParts) > 0 {
			dirName = pathParts[len(pathParts)-1]
		}

		// Navigate to the target directory
		currentFSID := rootFSID
		for _, part := range pathParts {
			if part == "" {
				continue
			}
			nextFSID, err := h.findEntryInDir(token.RepoID, currentFSID, part)
			if err != nil {
				c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("directory not found: %s", part)})
				return
			}
			currentFSID = nextFSID
		}
		targetFSID = currentFSID
	}

	// Fallback if dirName is still empty
	if dirName == "" {
		dirName = "download"
	}

	// Check encryption
	var encrypted bool
	var fileKey []byte
	h.db.Session().Query(`
		SELECT encrypted FROM libraries WHERE org_id = ? AND library_id = ?
	`, token.OrgID, token.RepoID).Scan(&encrypted)
	if encrypted {
		fileKey = v2.GetDecryptSessions().GetFileKey(token.UserID, token.RepoID)
		if fileKey == nil {
			c.JSON(http.StatusForbidden, gin.H{"error": "library is encrypted but not unlocked"})
			return
		}
	}

	// Stream ZIP to response
	zipFilename := dirName + ".zip"
	c.Header("Content-Type", "application/zip")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, zipFilename))
	c.Status(http.StatusOK)

	zipWriter := zip.NewWriter(c.Writer)
	defer zipWriter.Close()

	// Snapshot writer size before streaming so we can calculate the delta afterward.
	bytesBefore := int64(c.Writer.Size())

	// Recursively add directory contents to the ZIP
	h.addDirToZip(c.Request.Context(), zipWriter, token.RepoID, token.OrgID, targetFSID, "", fileKey)

	// Record traffic for the bytes actually sent (zip overhead included is fine for billing granularity).
	if rec := traffic.Get(); rec != nil {
		bytesAfter := int64(c.Writer.Size())
		if bytesAfter < 0 {
			bytesAfter = 0
		}
		sent := bytesAfter - bytesBefore
		if sent < 0 {
			sent = 0
		}
		if sent > 0 {
			tt := traffic.WebDownload
			if token.Source == "link" {
				tt = traffic.LinkDownload
			}
			rec.Record(token.OrgID, token.UserID, tt, sent)
		}
	}
}

// addDirToZip recursively adds directory contents to a ZIP archive
func (h *SeafHTTPHandler) addDirToZip(ctx context.Context, zw *zip.Writer, repoID, orgID, dirFSID, prefix string, fileKey []byte) {
	var dirEntriesJSON string
	err := h.db.Session().Query(`
		SELECT dir_entries FROM fs_objects WHERE library_id = ? AND fs_id = ?
	`, repoID, dirFSID).Scan(&dirEntriesJSON)
	if err != nil || dirEntriesJSON == "" || dirEntriesJSON == "[]" {
		return
	}

	var entries []map[string]interface{}
	if err := json.Unmarshal([]byte(dirEntriesJSON), &entries); err != nil {
		log.Printf("[addDirToZip] Failed to parse dir entries: %v", err)
		return
	}

	for _, entry := range entries {
		name, _ := entry["name"].(string)
		id, _ := entry["id"].(string)
		if name == "" || id == "" {
			continue
		}

		entryPath := name
		if prefix != "" {
			entryPath = prefix + "/" + name
		}

		modeFloat, _ := entry["mode"].(float64)
		mode := int(modeFloat)

		if mode == 16384 || mode&0170000 == 040000 { // Directory
			h.addDirToZip(ctx, zw, repoID, orgID, id, entryPath, fileKey)
		} else { // File
			h.addFileToZip(ctx, zw, repoID, orgID, id, entryPath, fileKey)
		}
	}
}

// addFileToZip streams a file's blocks into a ZIP archive entry.
// Uses zip.Store (no compression) for maximum throughput — the data is already
// compressed by S3/MinIO or is binary data where deflate adds CPU cost for minimal gain.
// For encrypted files, one block at a time is loaded, decrypted, and written.
func (h *SeafHTTPHandler) addFileToZip(ctx context.Context, zw *zip.Writer, repoID, orgID, fileFSID, zipPath string, fileKey []byte) {
	var blockIDs []string
	var fileSize int64
	err := h.db.Session().Query(`
		SELECT block_ids, size_bytes FROM fs_objects WHERE library_id = ? AND fs_id = ?
	`, repoID, fileFSID).Scan(&blockIDs, &fileSize)
	if err != nil {
		log.Printf("[addFileToZip] Failed to get blocks for %s: %v", zipPath, err)
		return
	}

	// Use Store (no compression) for maximum throughput.
	// Deflate on a 28GB archive caps at ~50-100 MB/s on a single core.
	header := &zip.FileHeader{
		Name:   zipPath,
		Method: zip.Store, // No compression — raw speed
	}
	if fileSize > 0 {
		header.UncompressedSize64 = uint64(fileSize)
	}
	w, err := zw.CreateHeader(header)
	if err != nil {
		log.Printf("[addFileToZip] Failed to create zip entry %s: %v", zipPath, err)
		return
	}

	blockStore, _, err := h.storageManager.GetHealthyBlockStore("")
	if err != nil {
		log.Printf("[addFileToZip] Block store not available: %v", err)
		return
	}

	// Batch resolve all block IDs upfront
	resolvedIDs := streaming.BatchResolveBlockIDs(h.db, orgID, blockIDs)

	// Get a reusable 4MB buffer for streaming
	buf := streaming.GetCopyBuf()
	defer streaming.PutCopyBuf(buf)

	for i, blockID := range blockIDs {
		internalID := resolvedIDs[i]
		_ = blockID // original ID used only for logging

		if fileKey != nil {
			// Encrypted: load block, decrypt, write
			blockData, err := blockStore.GetBlock(ctx, internalID)
			if err != nil {
				log.Printf("[addFileToZip] Failed to get block %s for %s: %v", blockIDs[i], zipPath, err)
				return
			}
			decrypted, err := crypto.DecryptBlock(blockData, fileKey)
			if err != nil {
				log.Printf("[addFileToZip] Failed to decrypt block for %s: %v", zipPath, err)
				return
			}
			w.Write(decrypted)
		} else {
			// Unencrypted: stream directly from S3 → ZIP writer with 4MB buffer
			reader, err := blockStore.GetBlockReader(ctx, internalID)
			if err != nil {
				log.Printf("[addFileToZip] Failed to get block reader %s for %s: %v", blockIDs[i], zipPath, err)
				return
			}
			_, err = io.CopyBuffer(w, reader, buf)
			reader.Close()
			if err != nil {
				log.Printf("[addFileToZip] Failed to stream block %s for %s: %v", blockIDs[i], zipPath, err)
				return
			}
		}
	}
}
