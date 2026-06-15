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
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	v2 "github.com/Sesame-Disk/sesamefs/internal/api/v2"
	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/crypto"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/httputil"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/Sesame-Disk/sesamefs/internal/streaming"
	"github.com/Sesame-Disk/sesamefs/internal/traffic"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"
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
	Replace   bool   // Default overwrite behavior for upload tokens
	UserID    string
	Source    string // "" or "web" = regular user; "link" = share/upload link
	AuthToken string // User's auth token (for one-time login tokens)
	ExpiresAt time.Time
	CreatedAt time.Time
}

// TokenStore is the interface for token operations (can be in-memory or Cassandra-backed)
type TokenStore interface {
	CreateUploadToken(orgID, repoID, path, userID string) (string, error)
	CreateUpdateToken(orgID, repoID, path, userID string) (string, error)
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
	return tm.createToken(tokenType, orgID, repoID, path, userID, source, false, ttl)
}

func (tm *TokenManager) createToken(tokenType TokenType, orgID, repoID, path, userID, source string, replace bool, ttl time.Duration) (*AccessToken, error) {
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
		Replace:   replace,
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
	token, err := tm.createToken(TokenTypeUpload, orgID, repoID, path, userID, "", false, tm.tokenTTL)
	if err != nil {
		return "", err
	}
	return token.Token, nil
}

// CreateUpdateToken creates an upload token that overwrites the target path by default.
func (tm *TokenManager) CreateUpdateToken(orgID, repoID, path, userID string) (string, error) {
	token, err := tm.createToken(TokenTypeUpload, orgID, repoID, path, userID, "", true, tm.tokenTTL)
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
	token, err := tm.createToken(TokenTypeUpload, orgID, repoID, path, userID, "link", false, tm.tokenTTL)
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
		ExpiresAt: time.Now().Add(60 * time.Second),
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
	OperationID string
	TempFile    *os.File
	TempPath    string
	ReceivedEnd int64
	Ranges      []byteRange
	Finalizing  bool

	finalizationStarted    bool
	accountedBlockPosition map[int]string
	quotaPrecheck          chunkQuotaPrecheck
	updatedAt              time.Time
	mu                     sync.Mutex
}

type byteRange struct {
	Start int64
	End   int64
}

type chunkQuotaPrecheck struct {
	ready     bool
	parentDir string
	totalSize int64
	replace   bool
}

// Chunked upload janitor tunables. The in-memory tracker TTL must be longer
// than any realistic pause between chunks. The disk TTL is the safety net for
// temp files orphaned by a process restart (map is gone, file remains).
const (
	chunkJanitorInterval = 10 * time.Minute
	chunkTrackerTTL      = 1 * time.Hour
	chunkDiskTTL         = 2 * time.Hour
)

// ChunkManager manages chunked uploads
type ChunkManager struct {
	uploads map[string]*ChunkUpload
	mu      sync.RWMutex
	tempDir string

	// Janitor config — overridable in tests.
	janitorInterval time.Duration
	trackerTTL      time.Duration
	diskTTL         time.Duration
	now             func() time.Time

	janitorOnce sync.Once
	stopCh      chan struct{}
}

// NewChunkManager creates a new chunk manager and starts its janitor goroutine.
func NewChunkManager() *ChunkManager {
	cm := &ChunkManager{
		uploads:         make(map[string]*ChunkUpload),
		tempDir:         os.TempDir(),
		janitorInterval: chunkJanitorInterval,
		trackerTTL:      chunkTrackerTTL,
		diskTTL:         chunkDiskTTL,
		now:             time.Now,
		stopCh:          make(chan struct{}),
	}
	cm.StartJanitor()
	return cm
}

// StartJanitor launches the background sweeper exactly once per manager.
func (cm *ChunkManager) StartJanitor() {
	cm.janitorOnce.Do(func() {
		go cm.janitorLoop()
	})
}

// Stop halts the janitor goroutine. Intended for tests.
func (cm *ChunkManager) Stop() {
	select {
	case <-cm.stopCh:
		return
	default:
	}
	close(cm.stopCh)
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
		OperationID: newSeafHTTPUploadOperationID(token),
		TempFile:    tempFile,
		TempPath:    tempPath,
		ReceivedEnd: -1,
		updatedAt:   cm.now(),
	}
	cm.uploads[key] = upload
	log.Printf("[ChunkManager] Created upload tracker: %s, totalSize=%d", key, totalSize)
	return upload, nil
}

func (cm *ChunkManager) GetUpload(token, filename string) *ChunkUpload {
	key := token + ":" + filename
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.uploads[key]
}

// janitorLoop periodically reaps stale chunk uploads from memory and disk.
func (cm *ChunkManager) janitorLoop() {
	// First sweep runs one interval in — avoids a burst at process start
	// when stats aren't warmed up yet.
	ticker := time.NewTicker(cm.janitorInterval)
	defer ticker.Stop()
	for {
		select {
		case <-cm.stopCh:
			return
		case <-ticker.C:
			cm.sweepOnce()
		}
	}
}

// sweepOnce performs one pass: (1) drop stale in-memory uploads and
// (2) remove orphaned temp files from disk whose tracker is gone.
func (cm *ChunkManager) sweepOnce() {
	now := cm.now()
	trackerCutoff := now.Add(-cm.trackerTTL)
	diskCutoff := now.Add(-cm.diskTTL)

	// (1) In-memory sweep — collect, release lock, then Cleanup() outside the
	// write lock to avoid holding cm.mu during file I/O.
	cm.mu.Lock()
	var stale []*ChunkUpload
	var staleKeys []string
	for key, upload := range cm.uploads {
		upload.mu.Lock()
		updatedAt := upload.updatedAt
		upload.mu.Unlock()
		if updatedAt.Before(trackerCutoff) {
			stale = append(stale, upload)
			staleKeys = append(staleKeys, key)
		}
	}
	for _, key := range staleKeys {
		delete(cm.uploads, key)
	}
	aliveTempPaths := make(map[string]struct{}, len(cm.uploads))
	for _, upload := range cm.uploads {
		aliveTempPaths[upload.TempPath] = struct{}{}
	}
	cm.mu.Unlock()

	for _, upload := range stale {
		if err := upload.Cleanup(); err != nil && !os.IsNotExist(err) {
			log.Printf("[ChunkManager] Janitor failed to clean tracker %s: %v", upload.TempPath, err)
			continue
		}
		metrics.ChunkUploadTempOrphansCleaned.WithLabelValues("tracker").Inc()
	}

	// (2) Disk sweep — files that were never (or are no longer) in the map.
	entries, err := os.ReadDir(cm.tempDir)
	if err != nil {
		log.Printf("[ChunkManager] Janitor: failed to read tempDir %s: %v", cm.tempDir, err)
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "sesamefs_upload_") {
			continue
		}
		fullPath := filepath.Join(cm.tempDir, name)
		if _, alive := aliveTempPaths[fullPath]; alive {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(diskCutoff) {
			continue
		}
		if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
			log.Printf("[ChunkManager] Janitor failed to remove orphan %s: %v", fullPath, err)
			continue
		}
		metrics.ChunkUploadTempOrphansCleaned.WithLabelValues("disk").Inc()
	}
}

// WriteChunk writes a chunk to the correct position in the temp file.
// start/end are inclusive byte offsets from the Content-Range header.
func (cu *ChunkUpload) WriteChunk(data []byte, start, end int64) error {
	cu.mu.Lock()
	defer cu.mu.Unlock()

	if err := validateChunkRange(start, end, cu.TotalSize, int64(len(data))); err != nil {
		return err
	}
	if cu.finalizationStarted {
		if cu.hasRangeLocked(start, end) {
			cu.updatedAt = time.Now()
			return nil
		}
		return fmt.Errorf("upload is finalizing")
	}

	// Seek to the start position
	if _, err := cu.TempFile.Seek(start, io.SeekStart); err != nil {
		return fmt.Errorf("failed to seek: %w", err)
	}

	// Write the data
	if _, err := cu.TempFile.Write(data); err != nil {
		return fmt.Errorf("failed to write chunk: %w", err)
	}

	if err := cu.markRangeReceivedLocked(start, end); err != nil {
		return err
	}
	cu.updatedAt = time.Now()

	log.Printf("[ChunkUpload] Wrote chunk: start=%d, end=%d, received_end=%d, total=%d",
		start, end, cu.ReceivedEnd, cu.TotalSize)
	return nil
}

// IsComplete checks if all chunks have been received
func (cu *ChunkUpload) IsComplete() bool {
	cu.mu.Lock()
	defer cu.mu.Unlock()
	return cu.isCompleteLocked()
}

func (cu *ChunkUpload) TryStartFinalization() bool {
	cu.mu.Lock()
	defer cu.mu.Unlock()
	if cu.Finalizing {
		metrics.ChunkUploadFinalizationAttemptsTotal.WithLabelValues("already_finalizing").Inc()
		return false
	}
	if !cu.isCompleteLocked() {
		metrics.ChunkUploadFinalizationAttemptsTotal.WithLabelValues("not_complete").Inc()
		return false
	}
	cu.Finalizing = true
	cu.finalizationStarted = true
	metrics.ChunkUploadFinalizationAttemptsTotal.WithLabelValues("started").Inc()
	return true
}

func (cu *ChunkUpload) ResetFinalization() {
	cu.mu.Lock()
	defer cu.mu.Unlock()
	cu.Finalizing = false
	cu.updatedAt = time.Now()
}

// WriteChunkFromReader streams a chunk from a reader directly to the temp file
// at the correct offset, without loading the entire chunk into memory.
func (cu *ChunkUpload) WriteChunkFromReader(r io.Reader, start, end int64) error {
	cu.mu.Lock()
	defer cu.mu.Unlock()

	if err := validateChunkRange(start, end, cu.TotalSize, -1); err != nil {
		return err
	}
	if cu.finalizationStarted {
		if cu.hasRangeLocked(start, end) {
			cu.updatedAt = time.Now()
			return nil
		}
		return fmt.Errorf("upload is finalizing")
	}

	if _, err := cu.TempFile.Seek(start, io.SeekStart); err != nil {
		return fmt.Errorf("failed to seek: %w", err)
	}

	written, err := io.Copy(cu.TempFile, r)
	if err != nil {
		return fmt.Errorf("failed to write chunk: %w", err)
	}

	if err := validateChunkRange(start, end, cu.TotalSize, written); err != nil {
		return err
	}
	if err := cu.markRangeReceivedLocked(start, end); err != nil {
		return err
	}
	cu.updatedAt = time.Now()

	log.Printf("[ChunkUpload] Streamed chunk: start=%d, written=%d, received_end=%d, total=%d",
		start, written, cu.ReceivedEnd, cu.TotalSize)
	return nil
}

func validateChunkRange(start, end, totalSize, written int64) error {
	if start < 0 || end < start {
		return fmt.Errorf("invalid chunk range: start=%d end=%d", start, end)
	}
	if totalSize > 0 && end >= totalSize {
		return fmt.Errorf("chunk range exceeds total size: end=%d total=%d", end, totalSize)
	}
	expected := end - start + 1
	if written >= 0 && written != expected {
		return fmt.Errorf("chunk size mismatch: range=%d written=%d", expected, written)
	}
	return nil
}

func (cu *ChunkUpload) markRangeReceivedLocked(start, end int64) error {
	cu.Ranges = append(cu.Ranges, byteRange{Start: start, End: end})
	sort.Slice(cu.Ranges, func(i, j int) bool {
		return cu.Ranges[i].Start < cu.Ranges[j].Start
	})

	merged := cu.Ranges[:0]
	for _, r := range cu.Ranges {
		if len(merged) == 0 || r.Start > merged[len(merged)-1].End+1 {
			merged = append(merged, r)
			continue
		}
		if r.End > merged[len(merged)-1].End {
			merged[len(merged)-1].End = r.End
		}
	}
	cu.Ranges = merged

	if end > cu.ReceivedEnd {
		cu.ReceivedEnd = end
	}
	return nil
}

func (cu *ChunkUpload) isCompleteLocked() bool {
	return cu.TotalSize > 0 &&
		len(cu.Ranges) == 1 &&
		cu.Ranges[0].Start == 0 &&
		cu.Ranges[0].End >= cu.TotalSize-1
}

func (cu *ChunkUpload) hasRangeLocked(start, end int64) bool {
	for _, r := range cu.Ranges {
		if start >= r.Start && end <= r.End {
			return true
		}
	}
	return false
}

func (cu *ChunkUpload) AccountBlockOnce(index int, blockID string, account func() error) error {
	cu.mu.Lock()
	if existingBlockID, ok := cu.accountedBlockPosition[index]; ok {
		cu.mu.Unlock()
		if existingBlockID != blockID {
			return fmt.Errorf("block at position %d changed after accounting", index)
		}
		return nil
	}
	cu.mu.Unlock()

	if err := account(); err != nil {
		return err
	}

	cu.mu.Lock()
	defer cu.mu.Unlock()
	if cu.accountedBlockPosition == nil {
		cu.accountedBlockPosition = make(map[int]string)
	}
	if existingBlockID, ok := cu.accountedBlockPosition[index]; ok {
		if existingBlockID != blockID {
			return fmt.Errorf("block at position %d changed after accounting", index)
		}
		return nil
	}
	cu.accountedBlockPosition[index] = blockID
	cu.updatedAt = time.Now()
	return nil
}

func (cu *ChunkUpload) BlockAlreadyAccounted(index int, blockID string) (bool, error) {
	cu.mu.Lock()
	defer cu.mu.Unlock()
	existingBlockID, ok := cu.accountedBlockPosition[index]
	if !ok {
		return false, nil
	}
	if existingBlockID != blockID {
		return false, fmt.Errorf("block at position %d changed after accounting", index)
	}
	return true, nil
}

func (cu *ChunkUpload) AccountedBlockIDs() []string {
	cu.mu.Lock()
	defer cu.mu.Unlock()
	if len(cu.accountedBlockPosition) == 0 {
		return nil
	}
	positions := make([]int, 0, len(cu.accountedBlockPosition))
	for position := range cu.accountedBlockPosition {
		positions = append(positions, position)
	}
	sort.Ints(positions)
	blockIDs := make([]string, 0, len(positions))
	for _, position := range positions {
		blockIDs = append(blockIDs, cu.accountedBlockPosition[position])
	}
	return blockIDs
}

func (cu *ChunkUpload) UploadOperationID() string {
	cu.mu.Lock()
	defer cu.mu.Unlock()
	if strings.TrimSpace(cu.OperationID) == "" {
		cu.OperationID = newSeafHTTPUploadOperationID(cu.Token)
	}
	return cu.OperationID
}

func (cu *ChunkUpload) HasQuotaPrecheck(parentDir string, totalSize int64, replace bool) bool {
	cu.mu.Lock()
	defer cu.mu.Unlock()
	return cu.quotaPrecheck.ready &&
		cu.quotaPrecheck.parentDir == parentDir &&
		cu.quotaPrecheck.totalSize == totalSize &&
		cu.quotaPrecheck.replace == replace
}

func (cu *ChunkUpload) MarkQuotaPrecheck(parentDir string, totalSize int64, replace bool) {
	cu.mu.Lock()
	defer cu.mu.Unlock()
	cu.quotaPrecheck = chunkQuotaPrecheck{
		ready:     true,
		parentDir: parentDir,
		totalSize: totalSize,
		replace:   replace,
	}
	cu.updatedAt = time.Now()
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

func (cu *ChunkUpload) Touch() {
	cu.mu.Lock()
	defer cu.mu.Unlock()
	cu.updatedAt = time.Now()
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
	config         *config.Config
	permMiddleware *middleware.PermissionMiddleware
	zipMaxEntries  int
	zipMaxDepth    int
	zipMaxBytes    int64
}

const (
	defaultZipMaxEntries = 100000
	defaultZipMaxDepth   = 64
	defaultZipMaxBytes   = 10 * 1024 * 1024 * 1024 // 10 GiB of uncompressed file content
)

type zipLimitError struct {
	message string
}

func (e *zipLimitError) Error() string {
	return e.message
}

func isZipLimitError(err error) bool {
	var target *zipLimitError
	return errors.As(err, &target)
}

type zipTraversalBudget struct {
	maxEntries int
	maxDepth   int
	maxBytes   int64
	entries    int
	totalBytes int64
}

type zipPreparedFile struct {
	path        string
	blockIDs    []string
	resolvedIDs []string
	sizeBytes   int64
}

func (b *zipTraversalBudget) noteDirectory(depth int) error {
	if depth > b.maxDepth {
		return &zipLimitError{message: fmt.Sprintf("zip download exceeds maximum directory depth of %d", b.maxDepth)}
	}
	return nil
}

func (b *zipTraversalBudget) noteFile(size int64) error {
	if b.entries+1 > b.maxEntries {
		return &zipLimitError{message: fmt.Sprintf("zip download exceeds maximum file count of %d", b.maxEntries)}
	}
	if size < 0 {
		size = 0
	}
	if b.totalBytes+size > b.maxBytes {
		return &zipLimitError{message: fmt.Sprintf("zip download exceeds maximum total size of %d bytes", b.maxBytes)}
	}
	b.entries++
	b.totalBytes += size
	return nil
}

// NewSeafHTTPHandler creates a new SeafHTTP handler
func NewSeafHTTPHandler(s3Store *storage.S3Store, storageManager *storage.Manager, database *db.DB, tokenStore TokenStore, cfg *config.Config, permMiddleware *middleware.PermissionMiddleware) *SeafHTTPHandler {
	return &SeafHTTPHandler{
		storage:        s3Store,
		storageManager: storageManager,
		db:             database,
		tokenStore:     tokenStore,
		config:         cfg,
		permMiddleware: permMiddleware,
		zipMaxEntries:  defaultZipMaxEntries,
		zipMaxDepth:    defaultZipMaxDepth,
		zipMaxBytes:    defaultZipMaxBytes,
	}
}

func (h *SeafHTTPHandler) SetZipLimits(maxEntries, maxDepth int, maxBytes int64) {
	if maxEntries > 0 {
		h.zipMaxEntries = maxEntries
	}
	if maxDepth > 0 {
		h.zipMaxDepth = maxDepth
	}
	if maxBytes > 0 {
		h.zipMaxBytes = maxBytes
	}
}

func (h *SeafHTTPHandler) configuredServerURL() string {
	if h == nil || h.config == nil {
		return ""
	}
	return h.config.Server.URL
}

func (h *SeafHTTPHandler) newZipTraversalBudget() *zipTraversalBudget {
	maxEntries := h.zipMaxEntries
	if maxEntries <= 0 {
		maxEntries = defaultZipMaxEntries
	}
	maxDepth := h.zipMaxDepth
	if maxDepth <= 0 {
		maxDepth = defaultZipMaxDepth
	}
	maxBytes := h.zipMaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultZipMaxBytes
	}
	return &zipTraversalBudget{
		maxEntries: maxEntries,
		maxDepth:   maxDepth,
		maxBytes:   maxBytes,
	}
}

// RegisterSeafHTTPRoutes registers the seafhttp routes
func (h *SeafHTTPHandler) RegisterSeafHTTPRoutes(router *gin.Engine, zipRL ...gin.HandlerFunc) {
	seafhttp := router.Group("/seafhttp")
	{
		// Upload endpoint - receives files and stores them in S3
		seafhttp.POST("/upload-api/:token", h.HandleUpload)

		// Download endpoint - streams files from S3
		seafhttp.GET("/files/:token/*filepath", h.HandleDownload)

		// ZIP download endpoint - creates a ZIP of a directory on-the-fly
		zipHandlers := make([]gin.HandlerFunc, 0, 2)
		if len(zipRL) > 0 && zipRL[0] != nil {
			zipHandlers = append(zipHandlers, zipRL[0])
		}
		zipHandlers = append(zipHandlers, h.HandleZipDownload)
		seafhttp.GET("/zip/:token", zipHandlers...)
	}
}

func (h *SeafHTTPHandler) lookupLibraryStorageClass(orgID, repoID string) string {
	if h == nil || h.db == nil || orgID == "" || repoID == "" {
		return ""
	}

	var storageClass string
	if err := h.db.Session().Query(`
		SELECT storage_class FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(&storageClass); err != nil {
		return ""
	}

	return storageClass
}

func (h *SeafHTTPHandler) resolveLibraryBlockStore(hostname, orgID, repoID string) (*storage.BlockStore, string, error) {
	libraryClass := h.lookupLibraryStorageClass(orgID, repoID)
	if h.storageManager != nil {
		preferredClass := h.storageManager.ResolveStorageClass(hostname, libraryClass, "hot")
		return h.storageManager.GetHealthyBlockStore(preferredClass)
	}
	if h.storage != nil {
		return storage.NewBlockStore(h.storage, "blocks/"), libraryClass, nil
	}

	return nil, libraryClass, fmt.Errorf("block storage not available")
}

func (h *SeafHTTPHandler) resolveLibraryObjectStore(hostname, orgID, repoID string) (storage.Store, string, error) {
	libraryClass := h.lookupLibraryStorageClass(orgID, repoID)
	if h.storageManager != nil {
		preferredClass := h.storageManager.ResolveStorageClass(hostname, libraryClass, "hot")
		return h.storageManager.GetHealthyBackend(preferredClass)
	}
	if h.storage != nil {
		return h.storage, libraryClass, nil
	}

	return nil, libraryClass, fmt.Errorf("storage not available")
}

// uploadBlockSize is the block size used when splitting large uploads into blocks.
// 8 MB matches Seafile's default CDC block size for good deduplication compatibility.
const uploadBlockSize = 8 * 1024 * 1024 // 8 MB

// finalizeUploadConcurrency caps the number of S3 PUTs running in parallel
// during finalization of a chunked upload. The reader is sequential (one block
// at a time from the temp file); only the per-block work (encrypt + S3 PUT +
// Cassandra writes) runs concurrently. 8 keeps memory bounded
// (≤ 8 × uploadBlockSize ≈ 64 MB extra) while cutting wall-clock by ~6–8× on
// typical S3 latency.
const finalizeUploadConcurrency = 8

// finalizeUploadBlockMetadataConcurrency caps concurrent Cassandra
// materialization callbacks across chunked upload finalizations in this
// process. Block PUTs still run in parallel (up to
// finalizeUploadConcurrency); the permit starts only after S3 Exists+PUT and
// covers the provisional-ref + metadata/mapping path so Paxos-heavy writes do
// not stampede Cassandra.
const finalizeUploadBlockMetadataConcurrency = 1

const (
	seafHTTPUploadFinalizeLeaseTTL           = 30 * time.Second
	seafHTTPUploadFinalizeLeasePollInterval  = 25 * time.Millisecond
	seafHTTPUploadFinalizeLeaseRenewInterval = seafHTTPUploadFinalizeLeaseTTL / 3
	seafHTTPUploadMetadataFinalizeTimeout    = 2 * time.Minute
)

var finalizeUploadBlockMetadataPermits = make(chan struct{}, finalizeUploadBlockMetadataConcurrency)
var chunkedUploadLibraryFinalizePermits sync.Map
var putUploadedBlockAutoFn = func(ctx context.Context, blockStore *storage.BlockStore, hash string, data []byte) (string, error) {
	return blockStore.PutBlockAuto(ctx, hash, data)
}
var registerUploadedBlockAndMappingForUploadFn = v2.RegisterUploadedBlockAndMapping
var resolveSeafHTTPStoredBlockIDsFn = func(fsHelper *v2.FSHelper, orgID string, blockIDs []string) ([]string, error) {
	return fsHelper.ResolveStoredBlockIDs(orgID, blockIDs)
}
var stageSeafHTTPPublishAttemptReferencesFn = db.StagePublishAttemptReferences
var cleanupSeafHTTPFailedPublishAttemptFn = v2.CleanupFailedPublishArtifacts
var promoteSeafHTTPPublishAttemptReferencesFn = db.PromotePublishAttemptReferences
var queuePublishedFSObjectBlockReferenceRepairFn = v2.QueuePublishedFSObjectBlockReferenceRepair
var clearPublishedFSObjectBlockReferenceRepairFn = v2.ClearPublishedFSObjectBlockReferenceRepair
var schedulePublishedFSObjectBlockReferenceRepairFn = v2.SchedulePublishedFSObjectBlockReferenceRepair
var releaseSeafHTTPPendingFSObjectOwnerFn = v2.ReleasePendingPublishedFSObjectOwner
var clearSeafHTTPPendingFSObjectOwnerFn = v2.ClearPendingPublishedFSObjectOwner
var clearSeafHTTPS3OrphanFenceFn = clearSeafHTTPS3OrphanFence
var seafHTTPBlockMaterializationRetryBackoffFn = v2.RetryBackoff
var seafHTTPBlockMaterializationSleepFn = time.Sleep
var seafHTTPUploadFinalizeLeaseSleepFn = time.Sleep
var newSeafHTTPUploadFinalizeLeaseTickerFn = func(d time.Duration) *time.Ticker { return time.NewTicker(d) }
var checkUploadStorageQuotaForCurrentHeadFn = func(h *SeafHTTPHandler, orgID, repoID, userID, parentDir, filename string, fileSize int64, replace bool) (int64, int64, error) {
	return h.checkUploadStorageQuotaForCurrentHead(orgID, repoID, userID, parentDir, filename, fileSize, replace)
}
var lookupLibraryEncryptedForUploadFn = func(h *SeafHTTPHandler, orgID, repoID string) (bool, error) {
	var encrypted bool
	err := h.db.Session().Query(`
		SELECT encrypted FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(&encrypted)
	return encrypted, err
}
var commitSeafHTTPUploadedFileMultiBlockFn = func(h *SeafHTTPHandler, ctx context.Context, orgID, repoID, userID, parentDir, filename, fileID string, blockIDs []string, fileSize int64, replace bool) (string, string, int64, int64, error) {
	return h.commitUploadedFileMultiBlock(ctx, orgID, repoID, userID, parentDir, filename, fileID, blockIDs, fileSize, replace)
}

func newSeafHTTPUploadOperationID(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return "seafhttp:" + uuid.NewString()
	}
	return "seafhttp:" + token + ":" + uuid.NewString()
}

func acquireFinalizeUploadBlockMetadataPermit(ctx context.Context) (func(), error) {
	select {
	case finalizeUploadBlockMetadataPermits <- struct{}{}:
		return func() { <-finalizeUploadBlockMetadataPermits }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func newSeafHTTPUploadMetadataFinalizeContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), seafHTTPUploadMetadataFinalizeTimeout)
}

var tryAcquireSeafHTTPUploadFinalizeLeaseFn = func(ctx context.Context, database *db.DB, leaseRole, leaseToken string, ttlSeconds int) (bool, error) {
	return database.Session().Query(`
		INSERT INTO gc_leases (role, instance_id, heartbeat)
		VALUES (?, ?, ?) IF NOT EXISTS USING TTL ?
	`, leaseRole, leaseToken, time.Now().UTC(), ttlSeconds).WithContext(ctx).MapScanCAS(map[string]interface{}{})
}

var renewSeafHTTPUploadFinalizeLeaseFn = func(ctx context.Context, database *db.DB, leaseRole, leaseToken string, ttlSeconds int) (bool, error) {
	return database.Session().Query(`
		UPDATE gc_leases USING TTL ?
		SET instance_id = ?, heartbeat = ?
		WHERE role = ? IF instance_id = ?
	`, ttlSeconds, leaseToken, time.Now().UTC(), leaseRole, leaseToken).WithContext(ctx).MapScanCAS(map[string]interface{}{})
}

var releaseSeafHTTPUploadFinalizeLeaseFn = func(ctx context.Context, database *db.DB, leaseRole, leaseToken string) error {
	return database.Session().Query(`
		DELETE FROM gc_leases WHERE role = ? IF instance_id = ?
	`, leaseRole, leaseToken).WithContext(ctx).Exec()
}

func acquireChunkedUploadLibraryFinalizePermit(ctx context.Context, repoID string) (func(), error) {
	repoID = strings.TrimSpace(repoID)
	if repoID == "" {
		return func() {}, nil
	}
	permitAny, _ := chunkedUploadLibraryFinalizePermits.LoadOrStore(repoID, make(chan struct{}, 1))
	permit := permitAny.(chan struct{})
	select {
	case permit <- struct{}{}:
		return func() { <-permit }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func startSeafHTTPUploadFinalizeLeaseRenewer(parentCtx context.Context, database *db.DB, leaseRole, leaseToken string, ttlSeconds int, renewInterval time.Duration) (context.Context, func()) {
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	leaseCtx, cancelLease := context.WithCancel(parentCtx)
	if database == nil || strings.TrimSpace(leaseRole) == "" || strings.TrimSpace(leaseToken) == "" || ttlSeconds <= 0 || renewInterval <= 0 {
		return leaseCtx, func() { cancelLease() }
	}
	ticker := newSeafHTTPUploadFinalizeLeaseTickerFn(renewInterval)
	stopCh := make(chan struct{})
	doneCh := make(chan struct{})
	leaseTTL := time.Duration(ttlSeconds) * time.Second
	go func() {
		defer close(doneCh)
		defer ticker.Stop()
		lastConfirmedAt := time.Now()
		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				renewCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				applied, err := renewSeafHTTPUploadFinalizeLeaseFn(renewCtx, database, leaseRole, leaseToken, ttlSeconds)
				cancel()
				if err != nil {
					log.Printf("[uploadFinalizeLease] WARNING: renew failed for role=%s token=%s: %v", leaseRole, leaseToken, err)
					if leaseTTL > 0 && time.Since(lastConfirmedAt) >= leaseTTL {
						log.Printf("[uploadFinalizeLease] WARNING: giving up lease for role=%s token=%s after %s without successful renewal", leaseRole, leaseToken, time.Since(lastConfirmedAt))
						cancelLease()
						return
					}
					continue
				}
				if !applied {
					log.Printf("[uploadFinalizeLease] WARNING: lease lost before release for role=%s token=%s", leaseRole, leaseToken)
					cancelLease()
					return
				}
				lastConfirmedAt = time.Now()
			}
		}
	}()
	return leaseCtx, func() {
		close(stopCh)
		<-doneCh
		cancelLease()
	}
}

func acquireSeafHTTPDistributedUploadFinalizeLeaseWithIntervals(ctx context.Context, database *db.DB, repoID string, ttl, pollInterval, renewInterval time.Duration) (context.Context, func(), error) {
	repoID = strings.TrimSpace(repoID)
	if database == nil || repoID == "" {
		return ctx, func() {}, nil
	}
	if ttl <= 0 {
		ttl = seafHTTPUploadFinalizeLeaseTTL
	}
	if pollInterval <= 0 {
		pollInterval = seafHTTPUploadFinalizeLeasePollInterval
	}
	if renewInterval <= 0 || renewInterval >= ttl {
		renewInterval = ttl / 3
		if renewInterval <= 0 {
			renewInterval = time.Second
		}
	}
	leaseRole := "upload-finalize:" + repoID
	leaseToken := uuid.NewString()
	ttlSeconds := int((ttl + time.Second - time.Nanosecond) / time.Second)
	for {
		applied, err := tryAcquireSeafHTTPUploadFinalizeLeaseFn(ctx, database, leaseRole, leaseToken, ttlSeconds)
		if err != nil {
			return nil, nil, fmt.Errorf("acquire upload finalize lease for repo %s: %w", repoID, err)
		}
		if applied {
			leaseCtx, stopRenewal := startSeafHTTPUploadFinalizeLeaseRenewer(ctx, database, leaseRole, leaseToken, ttlSeconds, renewInterval)
			return leaseCtx, func() {
				stopRenewal()
				releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := releaseSeafHTTPUploadFinalizeLeaseFn(releaseCtx, database, leaseRole, leaseToken); err != nil {
					log.Printf("[uploadFinalizeLease] Best-effort lease release failed for repo=%s token=%s: %v", repoID, leaseToken, err)
				}
			}, nil
		}
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		default:
		}
		seafHTTPUploadFinalizeLeaseSleepFn(pollInterval)
	}
}

func acquireSeafHTTPDistributedUploadFinalizeLease(ctx context.Context, database *db.DB, repoID string) (context.Context, func(), error) {
	return acquireSeafHTTPDistributedUploadFinalizeLeaseWithIntervals(ctx, database, repoID, seafHTTPUploadFinalizeLeaseTTL, seafHTTPUploadFinalizeLeasePollInterval, seafHTTPUploadFinalizeLeaseRenewInterval)
}

func acquireSeafHTTPUploadFinalizePermit(ctx context.Context, database *db.DB, repoID string) (context.Context, func(), error) {
	releaseLocalPermit, err := acquireChunkedUploadLibraryFinalizePermit(ctx, repoID)
	if err != nil {
		return nil, nil, err
	}
	leaseCtx, releaseDistributedLease, err := acquireSeafHTTPDistributedUploadFinalizeLease(ctx, database, repoID)
	if err != nil {
		releaseLocalPermit()
		return nil, nil, err
	}
	return leaseCtx, func() {
		releaseDistributedLease()
		releaseLocalPermit()
	}, nil
}

func checkSeafHTTPUploadFinalizeContext(ctx context.Context, repoID, phase string) error {
	if ctx == nil || ctx.Err() == nil {
		return nil
	}
	if strings.TrimSpace(phase) == "" {
		phase = "metadata finalize"
	}
	return fmt.Errorf("upload finalize lost exclusivity for repo %s during %s: %w", repoID, phase, ctx.Err())
}

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

	if h.storageManager == nil && h.storage == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "storage not available"})
		return
	}

	contentRange := c.GetHeader("Content-Range")
	start, end, total, isChunked := parseContentRange(contentRange)

	// Traffic quota pre-check — evaluated before reading the body so we can
	// fail fast. For chunked uploads, use the declared total from Content-Range
	// so the pre-check matches the eventual upload size. Storage quota is checked
	// later with the visible tree delta, after filename/replace/chunk-total are known.
	uploadTrafficStatus := traffic.QuotaStatus{Allowed: true}
	if checker := getAPIQuotaChecker(); checker != nil {
		trafficBytes := c.Request.ContentLength
		if isChunked && total > 0 {
			trafficBytes = total
		}
		if trafficBytes < 0 {
			trafficBytes = 0
		}
		uploadTrafficStatus, _ = traffic.CheckTrafficQuotaWithChecker(checker, token.OrgID, token.UserID, "upload", trafficBytes)
		if !uploadTrafficStatus.Allowed {
			c.JSON(http.StatusForbidden, traffic.TrafficQuotaExceededResponse(uploadTrafficStatus, "traffic quota exceeded", true))
			return
		} else {
			if warning, ok := traffic.TrafficQuotaWarningHeader(uploadTrafficStatus); ok {
				c.Header("X-Quota-Warning", warning)
			}
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
	replaceFile := token.Replace
	if token.Replace {
		// The token defines whether this upload is allowed to overwrite.
		// `replace=0` may downgrade an update-link to autorename, but an
		// upload-link must not be elevated to overwrite via multipart fields.
		replaceStr, ok := c.GetPostForm("replace")
		if ok {
			replaceFile = replaceStr != "0"
		}
	}
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

	log.Printf("[HandleUpload] Token=%s, File=%s, ContentRange=%s, isChunked=%v",
		tokenStr, filename, contentRange, isChunked)

	if isChunked {
		existingUpload := chunkManager.GetUpload(tokenStr, filename)
		if existingUpload == nil || !existingUpload.HasQuotaPrecheck(parentDir, total, replaceFile) {
			if _, _, err := h.checkUploadStorageQuotaForCurrentHead(token.OrgID, token.RepoID, token.UserID, parentDir, filename, total, replaceFile); err != nil {
				log.Printf("[HandleUpload] Chunked upload storage quota check failed: %v", err)
				if errors.Is(err, errStorageQuotaExceeded) {
					c.JSON(http.StatusForbidden, gin.H{"error": "storage quota exceeded"})
				} else {
					c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check storage quota"})
				}
				return
			}
		}

		// Chunked upload: stream chunk data directly to temp file (no io.ReadAll)
		upload, err := chunkManager.GetOrCreateUpload(tokenStr, filename, parentDir, total)
		if err != nil {
			log.Printf("[HandleUpload] Failed to create upload tracker: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to initialize upload"})
			return
		}
		upload.MarkQuotaPrecheck(parentDir, total, replaceFile)

		// Stream chunk directly to temp file at the correct offset
		if err := upload.WriteChunkFromReader(file, start, end); err != nil {
			log.Printf("[HandleUpload] Failed to write chunk: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to write chunk"})
			return
		}

		if !upload.TryStartFinalization() {
			log.Printf("[HandleUpload] Chunk received, waiting for more: %d/%d", end+1, total)
			c.JSON(http.StatusOK, gin.H{"success": true})
			return
		}

		// All chunks received — finalize by streaming from temp file
		log.Printf("[HandleUpload] All chunks received, finalizing upload (streaming)")
		upload.Touch()
		fileID, actualFilename, storageDeltaBytes, storageDeltaFiles, err := h.finalizeUploadStreaming(c, token, upload, parentDir, filename, storageKey, total, replaceFile)
		if err != nil {
			h.handleChunkedFinalizeError(token, tokenStr, filename, upload, err)
			log.Printf("[HandleUpload] Finalization failed: %v", err)
			writeSeafHTTPUploadError(c, err, "failed to finalize upload")
			return
		}
		chunkManager.CleanupUpload(tokenStr, filename)

		log.Printf("[HandleUpload] Upload complete: file=%s, size=%d, id=%s", actualFilename, total, fileID[:16])

		// Record traffic and storage — fire-and-forget, never blocks the response.
		if rec := traffic.Get(); rec != nil {
			tt := traffic.WebUpload
			if token.Source == "link" {
				tt = traffic.LinkUpload
			}
			traffic.RecordCheckedTransfer(rec, uploadTrafficStatus, token.OrgID, token.UserID, tt, total)
		}
		if h.db != nil {
			if err := traffic.AdjustStorageCountersByDeltaSync(h.db, token.OrgID, token.UserID, token.RepoID, storageDeltaBytes, storageDeltaFiles); err != nil {
				log.Printf("[HandleUpload] Failed to update storage counters: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update storage counters"})
				return
			}
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
	chunkData, err := httputil.ReadAllWithLimit(file, header.Size, httputil.SingleShotUploadReadLimitBytes)
	if err != nil {
		if errors.Is(err, httputil.ErrReadLimitExceeded) {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "single-shot upload exceeds 1 GiB limit; use chunked upload"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read file"})
		return
	}
	finalSize := int64(len(chunkData))
	uploadOperationID := newSeafHTTPUploadOperationID(token.Token)

	storageDeltaBytes, storageDeltaFiles, err := checkUploadStorageQuotaForCurrentHeadFn(h, token.OrgID, token.RepoID, token.UserID, parentDir, filename, finalSize, replaceFile)
	if err != nil {
		log.Printf("[HandleUpload] Storage quota check failed: %v", err)
		if errors.Is(err, errStorageQuotaExceeded) {
			c.JSON(http.StatusForbidden, gin.H{"error": "storage quota exceeded"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check storage quota"})
		}
		return
	}

	// Generate file ID (SHA-1 of plaintext for Seafile compatibility)
	sha1Hash := sha1.Sum(chunkData)
	fileID := hex.EncodeToString(sha1Hash[:])

	// Check encryption
	encrypted, err := lookupLibraryEncryptedForUploadFn(h, token.OrgID, token.RepoID)
	var storedContent = chunkData
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
	blockStore, actualStorageClass, err := h.resolveLibraryBlockStore(httputil.GetRoutingHostname(c, h.configuredServerURL()), token.OrgID, token.RepoID)
	var storeUploadedBlock func() error
	if err != nil {
		log.Printf("[HandleUpload] Failed to get block store: %v, falling back to S3", err)
		objectStore, objectStorageClass, resolveErr := h.resolveLibraryObjectStore(httputil.GetRoutingHostname(c, h.configuredServerURL()), token.OrgID, token.RepoID)
		if resolveErr != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "block storage not available"})
			return
		}
		actualStorageClass = objectStorageClass
		storeUploadedBlock = func() error {
			_, putErr := objectStore.Put(c.Request.Context(), storageKey, newBytesReader(storedContent), int64(len(storedContent)))
			return putErr
		}
	} else {
		storeUploadedBlock = func() error {
			_, putErr := putUploadedBlockAutoFn(ctx, blockStore, sha256ID, storedContent)
			if putErr == nil {
				log.Printf("[HandleUpload] Stored block %s (SHA-256: %s)", fileID[:16], sha256ID[:16])
			}
			return putErr
		}
	}

	// Register block metadata + a provisional reference (kept alive by TTL until
	// the fs_object commit creates the permanent reference), then write the
	// external SHA-1 mapping only after the block is durable in Cassandra.
	if err := retrySeafHTTPBlockMaterialization("HandleUpload", sha256ID, func() error {
		if putErr := storeUploadedBlock(); putErr != nil {
			return fmt.Errorf("failed to store block: %w", putErr)
		}
		return nil
	}, func() error {
		return registerUploadedBlockAndMappingForUploadFn(h.db, token.OrgID, token.RepoID, sha256ID, uploadOperationID, len(storedContent), actualStorageClass, "", fileID)
	}, func() (bool, error) {
		return clearSeafHTTPS3OrphanFenceFn(c.Request.Context(), h.db, h.storageManager, "HandleUpload", token.OrgID, sha256ID)
	}); err != nil {
		log.Printf("[HandleUpload] CRITICAL: Failed to materialize block org=%s block=%s ext=%s: %v", token.OrgID, sha256ID[:16], fileID[:16], err)
		if errors.Is(err, v2.ErrBlockMappingWriteFailed) {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create block mapping"})
		} else {
			writeSeafHTTPUploadError(c, err, "failed to store block metadata")
		}
		return
	}

	// Update filesystem metadata
	finalizeCtx, cancelFinalize := newSeafHTTPUploadMetadataFinalizeContext()
	defer cancelFinalize()
	leaseCtx, releaseFinalizePermit, err := acquireSeafHTTPUploadFinalizePermit(finalizeCtx, h.db, token.RepoID)
	if err != nil {
		writeSeafHTTPUploadError(c, err, "file stored but metadata update failed")
		return
	}
	defer releaseFinalizePermit()

	commitID, actualFilename, storageDeltaBytes, storageDeltaFiles, err := h.commitUploadedFile(leaseCtx, token.OrgID, token.RepoID, token.UserID, parentDir, filename, fileID, chunkData, finalSize, replaceFile)
	if err != nil {
		h.handleSingleShotMetadataError(token, uploadOperationID, sha256ID, err)
		log.Printf("[HandleUpload] Failed to update filesystem: %v", err)
		writeSeafHTTPUploadError(c, err, "file stored but metadata update failed")
		return
	}
	releaseUploadedBlockRefsFn(h.db, token.OrgID, token.RepoID, uploadOperationID, []string{sha256ID})
	log.Printf("[HandleUpload] Filesystem updated, commit=%s", commitID)

	log.Printf("[HandleUpload] Upload complete: file=%s, size=%d, id=%s", actualFilename, finalSize, fileID[:16])

	// Record traffic and storage — fire-and-forget, never blocks the response.
	if rec := traffic.Get(); rec != nil {
		tt := traffic.WebUpload
		if token.Source == "link" {
			tt = traffic.LinkUpload
		}
		traffic.RecordCheckedTransfer(rec, uploadTrafficStatus, token.OrgID, token.UserID, tt, finalSize)
	}
	if h.db != nil {
		if err := traffic.AdjustStorageCountersByDeltaSync(h.db, token.OrgID, token.UserID, token.RepoID, storageDeltaBytes, storageDeltaFiles); err != nil {
			log.Printf("[HandleUpload] Failed to update storage counters: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update storage counters"})
			return
		}
	}

	if retJSON {
		c.JSON(http.StatusOK, []gin.H{{"name": actualFilename, "id": fileID, "size": strconv.FormatInt(finalSize, 10)}})
	} else {
		c.String(http.StatusOK, fileID)
	}
}

var errStorageQuotaExceeded = errors.New("storage quota exceeded")

var rollbackUploadedBlockRefsFn = v2.RollbackUploadedBlockRefs

var releaseUploadedBlockRefsFn = v2.ReleaseUploadedBlockRefs

var zipBatchResolveBlockIDsFn = streaming.BatchResolveBlockIDs

var cleanupChunkUploadFn = func(token, filename string) {
	chunkManager.CleanupUpload(token, filename)
}

func (h *SeafHTTPHandler) handleChunkedFinalizeError(token *AccessToken, tokenStr, filename string, upload *ChunkUpload, err error) {
	// HeadConflict and quota_exceeded are both unrecoverable on the same
	// tracker: the client cannot finalize this session again (head moved past
	// the retry budget, or quota check will keep rejecting). Drop the tracker
	// and roll back the block refs we promoted. A block-mutation outcome that
	// stayed unknown must also stop same-tracker retries. Previously accounted
	// block refs are safe to roll back because they were recorded only after a
	// confirmed success; the ambiguous current block is not in that set yet.
	// Other errors (transient DB / block-store failures) leave the tracker alive
	// so a retried finalize on the same temp file can reuse the per-tracker
	// accounting and avoid a double increment.
	scope := ""
	switch {
	case errors.Is(err, v2.ErrLibraryHeadConflict):
		scope = "seafhttp_chunk_conflict"
	case errors.Is(err, errStorageQuotaExceeded):
		scope = "seafhttp_chunk_quota"
	case errors.Is(err, v2.ErrBlockMutationOutcomeUnknown):
		scope = "seafhttp_chunk_block_unknown"
	case errors.Is(err, v2.ErrBlockDeleteInProgress):
		scope = ""
	}
	if scope != "" {
		accountedBlockIDs := upload.AccountedBlockIDs()
		if len(accountedBlockIDs) > 0 {
			rollbackUploadedBlockRefsFn(
				h.db,
				token.OrgID,
				token.RepoID,
				upload.UploadOperationID(),
				accountedBlockIDs,
			)
		}
		cleanupChunkUploadFn(tokenStr, filename)
		return
	}
	upload.ResetFinalization()
}

func (h *SeafHTTPHandler) handleSingleShotMetadataError(token *AccessToken, operationID, internalBlockID string, err error) {
	if err == nil || strings.TrimSpace(internalBlockID) == "" {
		return
	}
	if errors.Is(err, v2.ErrLibraryHeadPublicationUnknown) {
		return
	}
	rollbackUploadedBlockRefsFn(
		h.db,
		token.OrgID,
		token.RepoID,
		operationID,
		[]string{internalBlockID},
	)
}

func writeSeafHTTPUploadError(c *gin.Context, err error, genericMsg string) {
	switch {
	case errors.Is(err, v2.ErrLibraryHeadConflict):
		// CLIENT_CONTRACT: the 409 status is the authoritative signal, but
		// frontend uploaders also match this exact string as a fallback when
		// status code is not observable (see RETRYABLE_UPLOAD_CONFLICT_ERROR
		// in frontend/src/utils/upload-finalization.js). Keep the wording in
		// sync across both places.
		c.JSON(http.StatusConflict, gin.H{"error": "library was modified concurrently; retry the upload"})
	case errors.Is(err, v2.ErrBlockDeleteInProgress):
		c.JSON(http.StatusConflict, gin.H{"error": "block is being deleted; retry the upload"})
	case errors.Is(err, errStorageQuotaExceeded):
		c.JSON(http.StatusForbidden, gin.H{"error": "storage quota exceeded"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": genericMsg})
	}
}

func clearSeafHTTPS3OrphanFence(ctx context.Context, database *db.DB, storageManager *storage.Manager, label, orgID, blockID string) (bool, error) {
	_ = ctx
	_ = storageManager
	if database == nil {
		return false, nil
	}

	orphanInfo, found, err := database.GetBlockS3OrphanInfo(orgID, blockID)
	if err != nil {
		return false, fmt.Errorf("read S3 orphan row for %s: %w", blockID, err)
	}
	if !found {
		return false, nil
	}

	log.Printf("[%s] S3 orphan fence for block %s remains active since %s; writer will back off and leave S3 cleanup to GC recovery", label, blockID, orphanInfo.FirstSeenAt.UTC().Format(time.RFC3339))
	return false, nil
}

func retrySeafHTTPBlockMaterialization(label, blockID string, store func() error, materialize func() error, resolveFence func() (bool, error)) error {
	attempts := v2.RetryAttempts()
	if attempts < 1 {
		attempts = 1
	}

	for attempt := 1; attempt <= attempts; attempt++ {
		if err := store(); err != nil {
			return err
		}
		if err := materialize(); err != nil {
			if !errors.Is(err, v2.ErrBlockDeleteInProgress) || attempt == attempts {
				return err
			}
			if resolveFence != nil {
				resolved, resolveErr := resolveFence()
				if resolveErr != nil {
					log.Printf("[%s] failed to inspect S3 orphan fence for block %s: %v", label, blockID, resolveErr)
				} else if resolved {
					continue
				}
			}
			sleepFor := seafHTTPBlockMaterializationRetryBackoffFn(attempt)
			log.Printf("[%s] block %s is fenced by GC delete during materialization; retrying (%d/%d) after %s", label, blockID, attempt, attempts, sleepFor)
			if sleepFor > 0 {
				seafHTTPBlockMaterializationSleepFn(sleepFor)
			}
			continue
		}
		return nil
	}

	return fmt.Errorf("%w: exhausted SeafHTTP block materialization retry budget for block %s", v2.ErrBlockDeleteInProgress, blockID)
}

// Upload-finalize retries share v2.RetryBackoff for the per-attempt delay
// (exponential with jitter, capped). Only the attempt count is local.
const uploadMetadataRetryAttempts = 20

func (h *SeafHTTPHandler) checkUploadStorageQuotaForCurrentHead(orgID, repoID, userID, parentDir, filename string, fileSize int64, replace bool) (int64, int64, error) {
	deltaBytes, deltaFiles, err := h.uploadStorageDeltaForCurrentHead(orgID, repoID, parentDir, filename, fileSize, replace)
	if err != nil {
		return 0, 0, err
	}
	if deltaBytes <= 0 {
		return deltaBytes, deltaFiles, nil
	}
	checker := getAPIQuotaChecker()
	if checker == nil {
		return deltaBytes, deltaFiles, nil
	}
	st, _ := checker.CheckStorageQuota(orgID, userID, deltaBytes)
	if !st.Allowed {
		return 0, 0, errStorageQuotaExceeded
	}
	return deltaBytes, deltaFiles, nil
}

func (h *SeafHTTPHandler) uploadStorageDeltaForCurrentHead(orgID, repoID, parentDir, filename string, fileSize int64, replace bool) (int64, int64, error) {
	var headCommitID string
	if err := h.db.Session().Query(`
		SELECT head_commit_id FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(&headCommitID); err != nil {
		return 0, 0, fmt.Errorf("failed to get head commit: %w", err)
	}

	var rootFSID string
	if err := h.db.Session().Query(`
		SELECT root_fs_id FROM commits WHERE library_id = ? AND commit_id = ?
	`, repoID, headCommitID).Scan(&rootFSID); err != nil {
		return 0, 0, fmt.Errorf("failed to get root fs_id: %w", err)
	}

	return h.uploadStorageDeltaForRoot(repoID, rootFSID, parentDir, filename, fileSize, replace)
}

func (h *SeafHTTPHandler) uploadStorageDeltaForRoot(repoID, rootFSID, parentDir, filename string, fileSize int64, replace bool) (int64, int64, error) {
	entries, err := h.directoryEntriesAtPath(repoID, rootFSID, parentDir)
	if err != nil {
		return 0, 0, err
	}
	if !replace {
		return fileSize, 1, nil
	}
	for _, entry := range entries {
		if entry["name"] == filename {
			return fileSize - entrySize(entry), 0, nil
		}
	}
	return fileSize, 1, nil
}

func (h *SeafHTTPHandler) directoryEntriesAtPath(repoID, rootFSID, dirPath string) ([]map[string]interface{}, error) {
	entries, err := h.readDirectoryEntries(repoID, rootFSID)
	if err != nil {
		return nil, err
	}

	dirPath = filepath.Clean("/" + strings.TrimPrefix(dirPath, "/"))
	if dirPath == "/" || dirPath == "." {
		return entries, nil
	}

	for _, part := range strings.Split(strings.Trim(dirPath, "/"), "/") {
		if part == "" {
			continue
		}
		var childID string
		for _, entry := range entries {
			if entry["name"] == part {
				if id, ok := entry["id"].(string); ok {
					childID = id
				}
				break
			}
		}
		if childID == "" {
			return []map[string]interface{}{}, nil
		}
		entries, err = h.readDirectoryEntries(repoID, childID)
		if err != nil {
			return nil, err
		}
	}
	return entries, nil
}

func (h *SeafHTTPHandler) readDirectoryEntries(repoID, fsID string) ([]map[string]interface{}, error) {
	if fsID == "" {
		return []map[string]interface{}{}, nil
	}
	var entriesJSON string
	if err := h.db.Session().Query(`
		SELECT dir_entries FROM fs_objects WHERE library_id = ? AND fs_id = ?
	`, repoID, fsID).Scan(&entriesJSON); err != nil {
		return nil, fmt.Errorf("failed to get directory entries: %w", err)
	}
	if entriesJSON == "" || entriesJSON == "[]" {
		return []map[string]interface{}{}, nil
	}
	var entries []map[string]interface{}
	if err := json.Unmarshal([]byte(entriesJSON), &entries); err != nil {
		return nil, fmt.Errorf("failed to parse directory entries: %w", err)
	}
	return entries, nil
}

func entrySize(entry map[string]interface{}) int64 {
	switch v := entry["size"].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	default:
		return 0
	}
}

// finalizeUploadStreaming processes a completed chunked upload by streaming from the temp file.
// It reads the file in blocks, hashes and stores each block individually — O(blockSize) RAM.
func (h *SeafHTTPHandler) finalizeUploadStreaming(c *gin.Context, token *AccessToken, upload *ChunkUpload, parentDir, filename, storageKey string, totalSize int64, replace bool) (string, string, int64, int64, error) {
	ctx := context.Background()

	if _, _, err := checkUploadStorageQuotaForCurrentHeadFn(h, token.OrgID, token.RepoID, token.UserID, parentDir, filename, totalSize, replace); err != nil {
		return "", "", 0, 0, err
	}

	// Get the temp file reader
	reader, err := upload.GetReader()
	if err != nil {
		return "", "", 0, 0, fmt.Errorf("failed to get upload reader: %w", err)
	}

	// Check encryption
	encrypted, err := lookupLibraryEncryptedForUploadFn(h, token.OrgID, token.RepoID)
	if err != nil {
		encrypted = false
	}
	var fileKey, fileIV []byte
	if encrypted {
		fileKey, fileIV = v2.GetDecryptSessions().GetFileKeyAndIV(token.UserID, token.RepoID)
		if fileKey == nil {
			return "", "", 0, 0, fmt.Errorf("library is encrypted but not unlocked")
		}
	}

	blockStore, actualStorageClass, err := h.resolveLibraryBlockStore(httputil.GetRoutingHostname(c, h.configuredServerURL()), token.OrgID, token.RepoID)
	if err != nil {
		return "", "", 0, 0, fmt.Errorf("block store not available: %w", err)
	}

	// Stream the temp file sequentially (one block at a time) but submit per-block
	// work (encrypt + S3 PUT + Cassandra writes) to a bounded worker pool. The reader
	// stays single-threaded so we don't need to seek; what we parallelise is the
	// network/IO-bound part that dominates wall-clock time.
	sha1Hasher := sha1.New()

	eg, egCtx := errgroup.WithContext(ctx)
	sem := make(chan struct{}, finalizeUploadConcurrency)

	var blockSHA1IDs []string // SHA-1 block IDs for fs_object (Seafile compat) — populated in order

readLoop:
	for {
		// Allocate a fresh buffer per block so it can travel into a goroutine
		// without aliasing. We don't reuse buffers across iterations.
		buf := make([]byte, uploadBlockSize)
		n, readErr := io.ReadFull(reader, buf)
		if n == 0 {
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				return "", "", 0, 0, fmt.Errorf("read error: %w", readErr)
			}
		}

		blockData := buf[:n]

		// Sequential, in-order work: file-level SHA-1 accumulator and the
		// block's own SHA-1 (which determines Seafile-compat block ordering).
		sha1Hasher.Write(blockData)
		blockSHA1Hash := sha1.Sum(blockData)
		blockSHA1ID := hex.EncodeToString(blockSHA1Hash[:])
		blockIndex := len(blockSHA1IDs)
		blockSHA1IDs = append(blockSHA1IDs, blockSHA1ID)

		// Bail early if a previous goroutine already failed.
		if err := egCtx.Err(); err != nil {
			break
		}

		// Acquire a worker slot before spawning so we don't pile up goroutines
		// or buffers when S3 is the bottleneck.
		select {
		case sem <- struct{}{}:
		case <-egCtx.Done():
			break readLoop
		}

		blockSHA1IDLocal := blockSHA1ID
		blockIndexLocal := blockIndex
		blockDataLocal := blockData
		eg.Go(func() error {
			defer func() { <-sem }()

			storedBlock := blockDataLocal
			if fileKey != nil {
				enc, encErr := crypto.EncryptBlockSeafile(blockDataLocal, fileKey, fileIV)
				if encErr != nil {
					return fmt.Errorf("failed to encrypt block: %w", encErr)
				}
				storedBlock = enc
			}

			sha256Hash := sha256.Sum256(storedBlock)
			sha256ID := hex.EncodeToString(sha256Hash[:])

			accounted, accountErr := upload.BlockAlreadyAccounted(blockIndexLocal, sha256ID)
			if accountErr != nil {
				return accountErr
			}
			if accounted {
				upload.Touch()
				return nil
			}

			uploadOperationID := upload.UploadOperationID()
			if blkErr := upload.AccountBlockOnce(blockIndexLocal, sha256ID, func() error {
				return retrySeafHTTPBlockMaterialization("finalizeUploadStreaming", sha256ID, func() error {
					// S3 Exists + PUT: runs in parallel across all 8 workers.
					_, putErr := putUploadedBlockAutoFn(egCtx, blockStore, sha256ID, storedBlock)
					if putErr != nil {
						return fmt.Errorf("failed to store block: %w", putErr)
					}
					return nil
				}, func() error {
					// Cassandra materialization: serialized process-wide after the
					// S3 PUT so provisional refs + metadata/mapping writes do not
					// stampede Cassandra.
					releaseMetadataPermit, permitErr := acquireFinalizeUploadBlockMetadataPermit(egCtx)
					if permitErr != nil {
						return permitErr
					}
					defer releaseMetadataPermit()
					return registerUploadedBlockAndMappingForUploadFn(h.db, token.OrgID, token.RepoID, sha256ID, uploadOperationID, len(storedBlock), actualStorageClass, "", blockSHA1IDLocal)
				}, func() (bool, error) {
					return clearSeafHTTPS3OrphanFenceFn(egCtx, h.db, h.storageManager, "finalizeUploadStreaming", token.OrgID, sha256ID)
				})
			}); blkErr != nil {
				if errors.Is(blkErr, v2.ErrBlockMappingWriteFailed) {
					log.Printf("[finalizeUploadStreaming] CRITICAL: Failed to write block_id_mapping org=%s ext=%s int=%s: %v", token.OrgID, blockSHA1IDLocal[:16], sha256ID[:16], blkErr)
					return fmt.Errorf("failed to create block mapping: %w", blkErr)
				}
				log.Printf("[finalizeUploadStreaming] CRITICAL: Failed to write block metadata org=%s block=%s: %v", token.OrgID, sha256ID[:16], blkErr)
				return fmt.Errorf("failed to store block metadata: %w", blkErr)
			}

			upload.Touch()
			return nil
		})

		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			break
		}
	}

	if err := eg.Wait(); err != nil {
		return "", "", 0, 0, err
	}

	// File ID = SHA-1 of the complete plaintext
	fileID := hex.EncodeToString(sha1Hasher.Sum(nil))

	log.Printf("[finalizeUploadStreaming] Stored %d blocks for file %s (size=%d, parallelism=%d)", len(blockSHA1IDs), fileID[:16], totalSize, finalizeUploadConcurrency)
	finalizeCtx, cancelFinalize := newSeafHTTPUploadMetadataFinalizeContext()
	defer cancelFinalize()
	leaseCtx, releaseFinalizePermit, err := acquireSeafHTTPUploadFinalizePermit(finalizeCtx, h.db, token.RepoID)
	if err != nil {
		return "", "", 0, 0, err
	}
	defer releaseFinalizePermit()

	commitID, actualFilename, storageDeltaBytes, storageDeltaFiles, err := commitSeafHTTPUploadedFileMultiBlockFn(h, leaseCtx, token.OrgID, token.RepoID, token.UserID, parentDir, filename, fileID, blockSHA1IDs, totalSize, replace)
	if err != nil {
		return "", "", 0, 0, fmt.Errorf("failed to update filesystem metadata: %w", err)
	}
	releaseUploadedBlockRefsFn(h.db, token.OrgID, token.RepoID, upload.UploadOperationID(), upload.AccountedBlockIDs())
	log.Printf("[finalizeUploadStreaming] Filesystem updated, commit=%s", commitID)

	return fileID, actualFilename, storageDeltaBytes, storageDeltaFiles, nil
}

// commitUploadedFileMultiBlock is like commitUploadedFile but supports multiple block IDs.
// Used for large files that are split into multiple blocks during upload.
// Returns the commit ID, the actual filename used (may differ if auto-renamed),
// and the storage delta from the winning publish attempt.
func (h *SeafHTTPHandler) commitUploadedFileMultiBlock(ctx context.Context, orgID, repoID, userID, parentDir, filename, fileID string, blockIDs []string, fileSize int64, replace bool) (string, string, int64, int64, error) {
	startedAt := time.Now()
	attemptsUsed := 0
	result := "error"
	defer func() {
		metrics.UploadFinalizeAttempts.WithLabelValues("seafhttp_multiblock", result).Observe(float64(attemptsUsed))
		metrics.UploadFinalizeDuration.WithLabelValues("seafhttp_multiblock", result).Observe(time.Since(startedAt).Seconds())
	}()

	var lastConflict error
	for attempt := 1; attempt <= uploadMetadataRetryAttempts; attempt++ {
		if err := checkSeafHTTPUploadFinalizeContext(ctx, repoID, "metadata finalize retry loop"); err != nil {
			return "", "", 0, 0, err
		}
		attemptsUsed = attempt
		commitID, actualFilename, storageDeltaBytes, storageDeltaFiles, err := h.commitUploadedFileMultiBlockOnce(ctx, orgID, repoID, userID, parentDir, filename, fileID, blockIDs, fileSize, replace)
		if err == nil {
			result = "success"
			return commitID, actualFilename, storageDeltaBytes, storageDeltaFiles, nil
		}
		if !errors.Is(err, v2.ErrLibraryHeadConflict) {
			if errors.Is(err, errStorageQuotaExceeded) {
				result = "quota_exceeded"
			}
			return "", "", 0, 0, err
		}
		lastConflict = err
		metrics.UploadFinalizeHeadConflictsTotal.WithLabelValues("seafhttp_multiblock").Inc()
		if attempt == uploadMetadataRetryAttempts {
			break
		}
		sleepFor := v2.RetryBackoff(attempt)
		log.Printf("[commitUploadedFileMultiBlock] Retrying metadata publish for repo=%s after head conflict (%d/%d), sleeping %s", repoID, attempt, uploadMetadataRetryAttempts, sleepFor)
		time.Sleep(sleepFor)
	}

	log.Printf("[commitUploadedFileMultiBlock] Exhausted metadata retries for repo=%s: %v", repoID, lastConflict)
	result = "retry_exhausted"
	metrics.UploadFinalizeRetryExhaustedTotal.WithLabelValues("seafhttp_multiblock").Inc()
	return "", "", 0, 0, fmt.Errorf("%w: failed to finalize upload metadata after %d attempts", v2.ErrLibraryHeadConflict, uploadMetadataRetryAttempts)
}

func stageSeafHTTPPublishAttemptReferences(fsHelper *v2.FSHelper, database *db.DB, orgID, repoID, attemptID string, externalBlockIDs []string) ([]string, error) {
	resolved, err := resolveSeafHTTPStoredBlockIDsFn(fsHelper, orgID, externalBlockIDs)
	if err != nil {
		return nil, err
	}
	resolved = db.NormalizeBlockIDs(resolved)
	stagedBlockIDs, err := stageSeafHTTPPublishAttemptReferencesFn(database, orgID, repoID, attemptID, resolved, nil)
	if err != nil {
		return nil, err
	}
	return stagedBlockIDs, nil
}

func cleanupSeafHTTPFailedPublishAttempt(database *db.DB, orgID, repoID, commitID, fsID string, blockIDs []string) error {
	cleanupErr := cleanupSeafHTTPFailedPublishAttemptFn(database, orgID, repoID, commitID, commitID, []string{fsID}, blockIDs)
	if cleanupErr != nil {
		return cleanupErr
	}
	ownerErr := releaseSeafHTTPPendingFSObjectOwnerFn(database, repoID, fsID, commitID, time.Time{})
	clearErr := clearPublishedFSObjectBlockReferenceRepairFn(database, orgID, repoID, commitID, fsID)
	return errors.Join(ownerErr, clearErr)
}

func finalizeSeafHTTPPublishedBlockReferences(fsHelper *v2.FSHelper, database *db.DB, orgID, repoID, commitID, fsID, label string, externalBlockIDs, stagedBlockIDs []string) {
	if err := promoteSeafHTTPPublishAttemptReferencesFn(database, orgID, commitID, stagedBlockIDs, func() error {
		return fsHelper.RegisterFSObjectBlockReferences(orgID, repoID, fsID, externalBlockIDs)
	}); err != nil {
		log.Printf("[%s] WARNING: head updated for repo=%s commit=%s but failed to promote block references for fs_object %s: %v", label, repoID, commitID, fsID, err)
		schedulePublishedFSObjectBlockReferenceRepairFn(database, orgID, repoID, commitID, fsID, label, stagedBlockIDs)
	} else if clearErr := clearPublishedFSObjectBlockReferenceRepairFn(database, orgID, repoID, commitID, fsID); clearErr != nil {
		log.Printf("[%s] WARNING: published repo=%s commit=%s but failed to clear queued publish repair for fs_object %s: %v", label, repoID, commitID, fsID, clearErr)
	}
}

func (h *SeafHTTPHandler) createPendingSeafHTTPFileFSObject(orgID, repoID, attemptID, fsID, filename, fullPath string, fileSize int64, createdAt time.Time, externalBlockIDs, stagedBlockIDs []string) error {
	if err := h.db.UpsertPendingPublishedFSObjectOwner(repoID, fsID, attemptID, createdAt, orgID, attemptID, stagedBlockIDs); err != nil {
		cleanupErr := cleanupSeafHTTPFailedPublishAttempt(h.db, orgID, repoID, attemptID, fsID, stagedBlockIDs)
		if cleanupErr != nil {
			return errors.Join(fmt.Errorf("failed to create pending fs_object owner: %w", err), cleanupErr)
		}
		return fmt.Errorf("failed to create pending fs_object owner: %w", err)
	}
	if err := h.db.Session().Query(`
		INSERT INTO fs_objects (library_id, fs_id, obj_type, obj_name, full_path, size_bytes, mtime, block_ids)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, repoID, fsID, "file", filename, fullPath, fileSize, createdAt.Unix(), externalBlockIDs).Exec(); err != nil {
		cleanupErr := cleanupSeafHTTPFailedPublishAttempt(h.db, orgID, repoID, attemptID, fsID, stagedBlockIDs)
		if cleanupErr != nil {
			return errors.Join(fmt.Errorf("failed to create file fs_object: %w", err), cleanupErr)
		}
		return fmt.Errorf("failed to create file fs_object: %w", err)
	}
	return nil
}

func (h *SeafHTTPHandler) commitUploadedFileMultiBlockOnce(ctx context.Context, orgID, repoID, userID, parentDir, filename, fileID string, blockIDs []string, fileSize int64, replace bool) (string, string, int64, int64, error) {
	if err := checkSeafHTTPUploadFinalizeContext(ctx, repoID, "metadata finalize attempt"); err != nil {
		return "", "", 0, 0, err
	}
	// Single consistent snapshot: head_commit_id + root_fs_id read together so
	// the quota delta, tree traversal, and CAS compare all use the same HEAD.
	fsHelper := v2.NewFSHelper(h.db)
	snapshot, err := fsHelper.GetLibraryHeadSnapshot(repoID)
	if err != nil {
		return "", "", 0, 0, fmt.Errorf("failed to get library head: %w", err)
	}

	storageDeltaBytes, storageDeltaFiles, err := h.uploadStorageDeltaForRoot(repoID, snapshot.RootFSID, parentDir, filename, fileSize, replace)
	if err != nil {
		return "", "", 0, 0, fmt.Errorf("failed to compute storage delta: %w", err)
	}
	if storageDeltaBytes > 0 {
		if checker := getAPIQuotaChecker(); checker != nil {
			if st, _ := checker.CheckStorageQuota(orgID, userID, storageDeltaBytes); !st.Allowed {
				return "", "", 0, 0, errStorageQuotaExceeded
			}
		}
	}

	log.Printf("[commitUploadedFileMultiBlock] headCommit=%s, rootFSID=%s, parentDir=%s, filename=%s, blocks=%d",
		snapshot.HeadCommitID, snapshot.RootFSID, parentDir, filename, len(blockIDs))

	fsContent := map[string]interface{}{
		"version":   1,
		"type":      1,
		"block_ids": blockIDs,
		"size":      fileSize,
	}
	fsContentJSON, err := json.Marshal(fsContent)
	if err != nil {
		return "", "", 0, 0, fmt.Errorf("failed to marshal fs content: %w", err)
	}
	fsHash := sha1.Sum(fsContentJSON)
	fileFSID := hex.EncodeToString(fsHash[:])

	newRootFSID, actualFilename, err := h.addFileToDirectory(repoID, snapshot.RootFSID, parentDir, filename, fileFSID, fileSize, userID, replace)
	if err != nil {
		return "", "", 0, 0, fmt.Errorf("failed to add file to directory: %w", err)
	}

	var fullPath string
	if parentDir == "/" {
		fullPath = "/" + actualFilename
	} else {
		fullPath = parentDir + "/" + actualFilename
	}

	description := fmt.Sprintf("Added or modified \"%s\".\n", actualFilename)
	commitData := fmt.Sprintf("%s:%s:%s:%d", repoID, newRootFSID, description, time.Now().UnixNano())
	commitHash := sha1.Sum([]byte(commitData))
	newCommitID := hex.EncodeToString(commitHash[:])

	// Hold an attempt-local referrer while this commit races for the library HEAD.
	// If the CAS loses, cleanup removes only this publish attempt instead of
	// touching the shared fs:<library>:<fs_id> referrer that another winner may use.
	stagedBlockIDs, err := stageSeafHTTPPublishAttemptReferences(fsHelper, h.db, orgID, repoID, newCommitID, blockIDs)
	if err != nil {
		return "", "", 0, 0, fmt.Errorf("failed to stage publish-attempt block references: %w", err)
	}
	if err := checkSeafHTTPUploadFinalizeContext(ctx, repoID, "persist pending publish state"); err != nil {
		cleanupErr := db.RemovePublishAttemptReferences(h.db, orgID, newCommitID, stagedBlockIDs)
		if cleanupErr != nil {
			return "", "", 0, 0, errors.Join(err, cleanupErr)
		}
		return "", "", 0, 0, err
	}
	if err := queuePublishedFSObjectBlockReferenceRepairFn(h.db, orgID, repoID, newCommitID, fileFSID, stagedBlockIDs); err != nil {
		cleanupErr := cleanupSeafHTTPFailedPublishAttempt(h.db, orgID, repoID, newCommitID, fileFSID, stagedBlockIDs)
		return "", "", 0, 0, errors.Join(
			fmt.Errorf("failed to queue durable publish repair for commit %s: %w", newCommitID, err),
			cleanupErr,
		)
	}
	createdAt := time.Now().UTC()
	if err := h.createPendingSeafHTTPFileFSObject(orgID, repoID, newCommitID, fileFSID, actualFilename, fullPath, fileSize, createdAt, blockIDs, stagedBlockIDs); err != nil {
		return "", "", 0, 0, err
	}
	if err := checkSeafHTTPUploadFinalizeContext(ctx, repoID, "commit creation"); err != nil {
		cleanupErr := cleanupSeafHTTPFailedPublishAttempt(h.db, orgID, repoID, newCommitID, fileFSID, stagedBlockIDs)
		if cleanupErr != nil {
			return "", "", 0, 0, errors.Join(err, cleanupErr)
		}
		return "", "", 0, 0, err
	}

	err = h.db.Session().Query(`
		INSERT INTO commits (library_id, commit_id, parent_id, root_fs_id, creator_id, description, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, repoID, newCommitID, snapshot.HeadCommitID, newRootFSID, userID, description, time.Now()).Exec()
	if err != nil {
		_ = cleanupSeafHTTPFailedPublishAttempt(h.db, orgID, repoID, newCommitID, fileFSID, stagedBlockIDs)
		return "", "", 0, 0, fmt.Errorf("failed to create commit: %w", err)
	}
	if err := checkSeafHTTPUploadFinalizeContext(ctx, repoID, "library head publish"); err != nil {
		cleanupErr := cleanupSeafHTTPFailedPublishAttempt(h.db, orgID, repoID, newCommitID, fileFSID, stagedBlockIDs)
		if cleanupErr != nil {
			return "", "", 0, 0, errors.Join(err, cleanupErr)
		}
		return "", "", 0, 0, err
	}

	if err := fsHelper.UpdateLibraryHeadFromSnapshot(snapshot, repoID, newCommitID, snapshot.HeadCommitID); err != nil {
		if errors.Is(err, v2.ErrLibraryHeadConflict) {
			if cleanupErr := cleanupSeafHTTPFailedPublishAttempt(h.db, orgID, repoID, newCommitID, fileFSID, stagedBlockIDs); cleanupErr != nil {
				return "", "", 0, 0, fmt.Errorf("failed to clean up conflict publish attempt %s: %w", newCommitID, cleanupErr)
			}
		}
		return "", "", 0, 0, fmt.Errorf("failed to update library head: %w", err)
	}
	if ownerErr := clearSeafHTTPPendingFSObjectOwnerFn(h.db, repoID, fileFSID, newCommitID, createdAt); ownerErr != nil {
		log.Printf("[commitUploadedFileMultiBlock] WARNING: published repo=%s commit=%s but failed to clear pending fs_object owner for %s: %v", repoID, newCommitID, fileFSID, ownerErr)
	}
	finalizeSeafHTTPPublishedBlockReferences(fsHelper, h.db, orgID, repoID, newCommitID, fileFSID, "commitUploadedFileMultiBlock", blockIDs, stagedBlockIDs)

	log.Printf("[commitUploadedFileMultiBlock] Created commit %s with root %s", newCommitID, newRootFSID)
	return newCommitID, actualFilename, storageDeltaBytes, storageDeltaFiles, nil
}

// commitUploadedFile updates the filesystem metadata after a file upload.
// When replace is false and a file with the same name exists, it auto-renames to "name (1).ext".
// Returns the commit ID, the actual filename used (may differ if auto-renamed),
// and the storage delta from the winning publish attempt.
func (h *SeafHTTPHandler) commitUploadedFile(ctx context.Context, orgID, repoID, userID, parentDir, filename, fileID string, content []byte, fileSize int64, replace bool) (string, string, int64, int64, error) {
	startedAt := time.Now()
	attemptsUsed := 0
	result := "error"
	defer func() {
		metrics.UploadFinalizeAttempts.WithLabelValues("seafhttp_single", result).Observe(float64(attemptsUsed))
		metrics.UploadFinalizeDuration.WithLabelValues("seafhttp_single", result).Observe(time.Since(startedAt).Seconds())
	}()

	var lastConflict error
	for attempt := 1; attempt <= uploadMetadataRetryAttempts; attempt++ {
		if err := checkSeafHTTPUploadFinalizeContext(ctx, repoID, "metadata finalize retry loop"); err != nil {
			return "", "", 0, 0, err
		}
		attemptsUsed = attempt
		commitID, actualFilename, storageDeltaBytes, storageDeltaFiles, err := h.commitUploadedFileOnce(ctx, orgID, repoID, userID, parentDir, filename, fileID, content, fileSize, replace)
		if err == nil {
			result = "success"
			return commitID, actualFilename, storageDeltaBytes, storageDeltaFiles, nil
		}
		if !errors.Is(err, v2.ErrLibraryHeadConflict) {
			if errors.Is(err, errStorageQuotaExceeded) {
				result = "quota_exceeded"
			}
			return "", "", 0, 0, err
		}
		lastConflict = err
		metrics.UploadFinalizeHeadConflictsTotal.WithLabelValues("seafhttp_single").Inc()
		if attempt == uploadMetadataRetryAttempts {
			break
		}
		sleepFor := v2.RetryBackoff(attempt)
		log.Printf("[commitUploadedFile] Retrying metadata publish for repo=%s after head conflict (%d/%d), sleeping %s", repoID, attempt, uploadMetadataRetryAttempts, sleepFor)
		time.Sleep(sleepFor)
	}

	log.Printf("[commitUploadedFile] Exhausted metadata retries for repo=%s: %v", repoID, lastConflict)
	result = "retry_exhausted"
	metrics.UploadFinalizeRetryExhaustedTotal.WithLabelValues("seafhttp_single").Inc()
	return "", "", 0, 0, fmt.Errorf("%w: failed to finalize upload metadata after %d attempts", v2.ErrLibraryHeadConflict, uploadMetadataRetryAttempts)
}

func (h *SeafHTTPHandler) commitUploadedFileOnce(ctx context.Context, orgID, repoID, userID, parentDir, filename, fileID string, content []byte, fileSize int64, replace bool) (string, string, int64, int64, error) {
	if err := checkSeafHTTPUploadFinalizeContext(ctx, repoID, "metadata finalize attempt"); err != nil {
		return "", "", 0, 0, err
	}
	// Single consistent snapshot: head_commit_id + root_fs_id read together so
	// the quota delta, tree traversal, and CAS compare all use the same HEAD.
	fsHelper := v2.NewFSHelper(h.db)
	snapshot, err := fsHelper.GetLibraryHeadSnapshot(repoID)
	if err != nil {
		return "", "", 0, 0, fmt.Errorf("failed to get library head: %w", err)
	}

	storageDeltaBytes, storageDeltaFiles, err := h.uploadStorageDeltaForRoot(repoID, snapshot.RootFSID, parentDir, filename, fileSize, replace)
	if err != nil {
		return "", "", 0, 0, fmt.Errorf("failed to compute storage delta: %w", err)
	}
	if storageDeltaBytes > 0 {
		if checker := getAPIQuotaChecker(); checker != nil {
			if st, _ := checker.CheckStorageQuota(orgID, userID, storageDeltaBytes); !st.Allowed {
				return "", "", 0, 0, errStorageQuotaExceeded
			}
		}
	}

	log.Printf("[commitUploadedFile] headCommit=%s, rootFSID=%s, parentDir=%s, filename=%s",
		snapshot.HeadCommitID, snapshot.RootFSID, parentDir, filename)

	blockID := fileID
	fsContent := map[string]interface{}{
		"version":   1,
		"type":      1,
		"block_ids": []string{blockID},
		"size":      fileSize,
	}
	fsContentJSON, err := json.Marshal(fsContent)
	if err != nil {
		return "", "", 0, 0, fmt.Errorf("failed to marshal fs content: %w", err)
	}
	fsHash := sha1.Sum(fsContentJSON)
	fileFSID := hex.EncodeToString(fsHash[:])

	log.Printf("[commitUploadedFile] File fs_id computed: %s (from JSON: %s)", fileFSID, string(fsContentJSON))

	newRootFSID, actualFilename, err := h.addFileToDirectory(repoID, snapshot.RootFSID, parentDir, filename, fileFSID, fileSize, userID, replace)
	if err != nil {
		return "", "", 0, 0, fmt.Errorf("failed to add file to directory: %w", err)
	}

	var fullPath string
	if parentDir == "/" {
		fullPath = "/" + actualFilename
	} else {
		fullPath = parentDir + "/" + actualFilename
	}

	description := fmt.Sprintf("Added or modified \"%s\".\n", actualFilename)
	commitData := fmt.Sprintf("%s:%s:%s:%d", repoID, newRootFSID, description, time.Now().UnixNano())
	commitHash := sha1.Sum([]byte(commitData))
	newCommitID := hex.EncodeToString(commitHash[:])

	stagedBlockIDs, err := stageSeafHTTPPublishAttemptReferences(fsHelper, h.db, orgID, repoID, newCommitID, []string{blockID})
	if err != nil {
		return "", "", 0, 0, fmt.Errorf("failed to stage publish-attempt block references: %w", err)
	}
	if err := checkSeafHTTPUploadFinalizeContext(ctx, repoID, "persist pending publish state"); err != nil {
		cleanupErr := db.RemovePublishAttemptReferences(h.db, orgID, newCommitID, stagedBlockIDs)
		if cleanupErr != nil {
			return "", "", 0, 0, errors.Join(err, cleanupErr)
		}
		return "", "", 0, 0, err
	}
	if err := queuePublishedFSObjectBlockReferenceRepairFn(h.db, orgID, repoID, newCommitID, fileFSID, stagedBlockIDs); err != nil {
		cleanupErr := cleanupSeafHTTPFailedPublishAttempt(h.db, orgID, repoID, newCommitID, fileFSID, stagedBlockIDs)
		return "", "", 0, 0, errors.Join(
			fmt.Errorf("failed to queue durable publish repair for commit %s: %w", newCommitID, err),
			cleanupErr,
		)
	}
	createdAt := time.Now().UTC()
	if err := h.createPendingSeafHTTPFileFSObject(orgID, repoID, newCommitID, fileFSID, actualFilename, fullPath, fileSize, createdAt, []string{blockID}, stagedBlockIDs); err != nil {
		return "", "", 0, 0, err
	}
	if err := checkSeafHTTPUploadFinalizeContext(ctx, repoID, "commit creation"); err != nil {
		cleanupErr := cleanupSeafHTTPFailedPublishAttempt(h.db, orgID, repoID, newCommitID, fileFSID, stagedBlockIDs)
		if cleanupErr != nil {
			return "", "", 0, 0, errors.Join(err, cleanupErr)
		}
		return "", "", 0, 0, err
	}
	log.Printf("[commitUploadedFile] Created file fs_object: %s at %s", fileFSID, fullPath)

	err = h.db.Session().Query(`
		INSERT INTO commits (library_id, commit_id, parent_id, root_fs_id, creator_id, description, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, repoID, newCommitID, snapshot.HeadCommitID, newRootFSID, userID, description, time.Now()).Exec()
	if err != nil {
		_ = cleanupSeafHTTPFailedPublishAttempt(h.db, orgID, repoID, newCommitID, fileFSID, stagedBlockIDs)
		return "", "", 0, 0, fmt.Errorf("failed to create commit: %w", err)
	}
	if err := checkSeafHTTPUploadFinalizeContext(ctx, repoID, "library head publish"); err != nil {
		cleanupErr := cleanupSeafHTTPFailedPublishAttempt(h.db, orgID, repoID, newCommitID, fileFSID, stagedBlockIDs)
		if cleanupErr != nil {
			return "", "", 0, 0, errors.Join(err, cleanupErr)
		}
		return "", "", 0, 0, err
	}

	if err := fsHelper.UpdateLibraryHeadFromSnapshot(snapshot, repoID, newCommitID, snapshot.HeadCommitID); err != nil {
		if errors.Is(err, v2.ErrLibraryHeadConflict) {
			if cleanupErr := cleanupSeafHTTPFailedPublishAttempt(h.db, orgID, repoID, newCommitID, fileFSID, stagedBlockIDs); cleanupErr != nil {
				return "", "", 0, 0, fmt.Errorf("failed to clean up conflict publish attempt %s: %w", newCommitID, cleanupErr)
			}
		}
		return "", "", 0, 0, fmt.Errorf("failed to update library head: %w", err)
	}
	if ownerErr := clearSeafHTTPPendingFSObjectOwnerFn(h.db, repoID, fileFSID, newCommitID, createdAt); ownerErr != nil {
		log.Printf("[commitUploadedFile] WARNING: published repo=%s commit=%s but failed to clear pending fs_object owner for %s: %v", repoID, newCommitID, fileFSID, ownerErr)
	}
	finalizeSeafHTTPPublishedBlockReferences(fsHelper, h.db, orgID, repoID, newCommitID, fileFSID, "commitUploadedFile", []string{blockID}, stagedBlockIDs)

	log.Printf("[commitUploadedFile] Created commit %s with root %s", newCommitID, newRootFSID)
	return newCommitID, actualFilename, storageDeltaBytes, storageDeltaFiles, nil
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
	downloadTrafficStatus := traffic.QuotaStatus{Allowed: true}
	if checker := traffic.GetChecker(); checker != nil {
		downloadTrafficStatus, _ = traffic.CheckTrafficQuotaWithChecker(checker, token.OrgID, token.UserID, "download", 0)
		if !downloadTrafficStatus.Allowed {
			c.JSON(http.StatusForbidden, traffic.TrafficQuotaExceededResponse(downloadTrafficStatus, "traffic quota exceeded", true))
			return
		} else {
			if warning, ok := traffic.TrafficQuotaWarningHeader(downloadTrafficStatus); ok {
				c.Header("X-Quota-Warning", warning)
			}
		}
	}

	// Try to stream file from block storage (content-addressed)
	// This is the normal flow for SesameFS files
	if h.db != nil && h.storageManager != nil {
		log.Printf("[HandleDownload] Attempting block-based streaming download")
		err := h.streamFileFromBlocks(c, token, filename, downloadTrafficStatus.PeriodStartedAt)
		if err == nil {
			return
		}
		log.Printf("[HandleDownload] Block-based streaming FAILED: %v", err)
		// If block-based retrieval fails, fall back to direct S3 path-based retrieval
	} else {
		log.Printf("[HandleDownload] Block storage not available (db=%v, storageManager=%v)", h.db != nil, h.storageManager != nil)
	}

	// Fallback: Stream directly from the resolved object store.
	objectStore, _, err := h.resolveLibraryObjectStore(httputil.GetRoutingHostname(c, h.configuredServerURL()), token.OrgID, token.RepoID)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "storage not available"})
		return
	}

	storageKey := fmt.Sprintf("%s/%s%s", token.OrgID, token.RepoID, token.Path)

	reader, err := objectStore.Get(c.Request.Context(), storageKey)
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
			traffic.RecordCheckedTransfer(rec, downloadTrafficStatus, token.OrgID, token.UserID, tt, sent)
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
func (h *SeafHTTPHandler) lookupFileBlocks(hostname string, token *AccessToken) (blockIDs []string, fileSize int64, fileKey []byte, fileIV []byte, blockStore *storage.BlockStore, err error) {
	// Check encryption
	var encrypted bool
	err = h.db.Session().Query(`
		SELECT encrypted FROM libraries WHERE org_id = ? AND library_id = ?
	`, token.OrgID, token.RepoID).Scan(&encrypted)
	if err != nil {
		return nil, 0, nil, nil, nil, fmt.Errorf("failed to check library encryption: %w", err)
	}

	if encrypted {
		fileKey, fileIV = v2.GetDecryptSessions().GetFileKeyAndIV(token.UserID, token.RepoID)
		if fileKey == nil {
			return nil, 0, nil, nil, nil, fmt.Errorf("library is encrypted but not unlocked")
		}
	}

	// Get head commit → root FS
	var headCommit string
	err = h.db.Session().Query(`
		SELECT head_commit_id FROM libraries WHERE org_id = ? AND library_id = ?
	`, token.OrgID, token.RepoID).Scan(&headCommit)
	if err != nil {
		return nil, 0, nil, nil, nil, fmt.Errorf("library not found: %w", err)
	}

	var rootFSID string
	err = h.db.Session().Query(`
		SELECT root_fs_id FROM commits WHERE library_id = ? AND commit_id = ?
	`, token.RepoID, headCommit).Scan(&rootFSID)
	if err != nil {
		return nil, 0, nil, nil, nil, fmt.Errorf("commit not found: %w", err)
	}

	// Navigate directory tree to the target file
	filePath := token.Path
	if !strings.HasPrefix(filePath, "/") {
		filePath = "/" + filePath
	}
	pathParts := strings.Split(strings.Trim(filePath, "/"), "/")
	if len(pathParts) == 0 || (len(pathParts) == 1 && pathParts[0] == "") {
		return nil, 0, nil, nil, nil, fmt.Errorf("invalid file path")
	}

	currentFSID := rootFSID
	for i := 0; i < len(pathParts)-1; i++ {
		nextFSID, err := h.findEntryInDir(token.RepoID, currentFSID, pathParts[i])
		if err != nil {
			return nil, 0, nil, nil, nil, fmt.Errorf("directory not found: %s: %w", pathParts[i], err)
		}
		currentFSID = nextFSID
	}

	targetName := pathParts[len(pathParts)-1]
	fileFSID, err := h.findEntryInDir(token.RepoID, currentFSID, targetName)
	if err != nil {
		return nil, 0, nil, nil, nil, fmt.Errorf("file not found: %s: %w", targetName, err)
	}

	// Get block IDs and file size from fs_object
	err = h.db.Session().Query(`
		SELECT block_ids, size_bytes FROM fs_objects WHERE library_id = ? AND fs_id = ?
	`, token.RepoID, fileFSID).Scan(&blockIDs, &fileSize)
	if err != nil {
		return nil, 0, nil, nil, nil, fmt.Errorf("file metadata not found: %w", err)
	}

	blockStore, _, err = h.resolveLibraryBlockStore(hostname, token.OrgID, token.RepoID)
	if err != nil {
		return nil, 0, nil, nil, nil, fmt.Errorf("block store not available: %w", err)
	}

	return blockIDs, fileSize, fileKey, fileIV, blockStore, nil
}

// streamFileFromBlocks streams a file's blocks directly to the HTTP response.
// Uses prefetching (overlap S3 fetch with HTTP write) and 4MB io.CopyBuffer
// for maximum throughput. Only O(2 × block_size) RAM.
func (h *SeafHTTPHandler) streamFileFromBlocks(c *gin.Context, token *AccessToken, filename string, periodStartedAt time.Time) error {
	blockIDs, fileSize, fileKey, fileIV, blockStore, err := h.lookupFileBlocks(httputil.GetRoutingHostname(c, h.configuredServerURL()), token)
	if err != nil {
		return err
	}

	downloadTrafficStatus := traffic.QuotaStatus{Allowed: true, PeriodStartedAt: periodStartedAt}
	if checker := traffic.GetChecker(); checker != nil {
		downloadTrafficStatus, _ = traffic.CheckTrafficQuotaWithChecker(checker, token.OrgID, token.UserID, "download", fileSize)
		if !downloadTrafficStatus.Allowed {
			c.JSON(http.StatusForbidden, traffic.TrafficQuotaExceededResponse(downloadTrafficStatus, "traffic quota exceeded", true))
			return nil
		}
		if warning, ok := traffic.TrafficQuotaWarningHeader(downloadTrafficStatus); ok {
			c.Header("X-Quota-Warning", warning)
		}
	}

	log.Printf("[streamFileFromBlocks] Streaming %d blocks, size=%d, encrypted=%v", len(blockIDs), fileSize, fileKey != nil)

	// Batch resolve all block IDs upfront (avoids per-block Cassandra queries).
	// Strict: a stale SHA-1 sent to SHA-256 storage would truncate the stream
	// mid-download, so we resolve BEFORE committing any headers and fail clean.
	resolvedIDs, err := streaming.BatchResolveBlockIDs(h.db, token.OrgID, blockIDs)
	if err != nil {
		log.Printf("[streamFileFromBlocks] block ID resolution failed for org=%s: %v", token.OrgID, err)
		return fmt.Errorf("resolve block IDs: %w", err)
	}

	// Set headers before streaming — Content-Length lets clients show progress
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Header("Content-Type", "application/octet-stream")
	if fileSize > 0 {
		// fs_objects.size_bytes is always the plaintext byte count — even for
		// encrypted libraries — so the emitted stream length equals fileSize
		// after decryption. Exposing this header lets clients show progress.
		c.Header("Content-Length", strconv.FormatInt(fileSize, 10))
	}
	c.Status(http.StatusOK)

	// Stream with prefetching pipeline
	streaming.StreamBlocks(c, c.Request.Context(), blockStore, resolvedIDs, fileKey, fileIV, "streamFileFromBlocks")

	log.Printf("[streamFileFromBlocks] Streaming complete: %d blocks", len(blockIDs))

	// Record traffic — fire-and-forget, never blocks the response.
	if rec := traffic.Get(); rec != nil {
		tt := traffic.WebDownload
		if token.Source == "link" {
			tt = traffic.LinkDownload
		}
		traffic.RecordCheckedTransfer(rec, downloadTrafficStatus, token.OrgID, token.UserID, tt, fileSize)
	}

	return nil
}

// getFileFromBlocks retrieves a file by loading all blocks into memory.
// DEPRECATED: Use streamFileFromBlocks for downloads. This is kept only for
// upload metadata (commitUploadedFile) where the full content is already in memory.
func (h *SeafHTTPHandler) getFileFromBlocks(c *gin.Context, token *AccessToken) ([]byte, error) {
	blockIDs, _, fileKey, fileIV, blockStore, err := h.lookupFileBlocks(httputil.GetRoutingHostname(c, h.configuredServerURL()), token)
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
			blockData, err = crypto.DecryptLibraryBlock(blockData, fileKey, fileIV)
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
	zipTrafficStatus := traffic.QuotaStatus{Allowed: true}
	if checker := traffic.GetChecker(); checker != nil {
		zipTrafficStatus, _ = traffic.CheckTrafficQuotaWithChecker(checker, token.OrgID, token.UserID, "download", 0)
		if !zipTrafficStatus.Allowed {
			c.JSON(http.StatusForbidden, traffic.TrafficQuotaExceededResponse(zipTrafficStatus, "traffic quota exceeded", true))
			return
		} else {
			if warning, ok := traffic.TrafficQuotaWarningHeader(zipTrafficStatus); ok {
				c.Header("X-Quota-Warning", warning)
			}
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
	var fileIV []byte
	h.db.Session().Query(`
		SELECT encrypted FROM libraries WHERE org_id = ? AND library_id = ?
	`, token.OrgID, token.RepoID).Scan(&encrypted)
	if encrypted {
		fileKey, fileIV = v2.GetDecryptSessions().GetFileKeyAndIV(token.UserID, token.RepoID)
		if fileKey == nil {
			c.JSON(http.StatusForbidden, gin.H{"error": "library is encrypted but not unlocked"})
			return
		}
	}

	hostname := httputil.GetRoutingHostname(c, h.configuredServerURL())
	blockStore, _, err := h.resolveLibraryBlockStore(hostname, token.OrgID, token.RepoID)
	if err != nil {
		log.Printf("[HandleZipDownload] Failed to resolve block store: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to prepare zip download"})
		return
	}

	preflightBudget := h.newZipTraversalBudget()
	preparedFiles, err := h.prepareZipDirectory(token.RepoID, token.OrgID, targetFSID, "", 0, preflightBudget)
	if err != nil {
		if isZipLimitError(err) {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": err.Error()})
			return
		}
		log.Printf("[HandleZipDownload] Failed to validate ZIP directory: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to prepare zip download"})
		return
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

	if err := h.addDirToZip(c.Request.Context(), zipWriter, blockStore, preparedFiles, fileKey, fileIV); err != nil {
		log.Printf("[HandleZipDownload] ZIP stream aborted: %v", err)
		return
	}

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
			traffic.RecordCheckedTransfer(rec, zipTrafficStatus, token.OrgID, token.UserID, tt, sent)
		}
	}
}

func (h *SeafHTTPHandler) prepareZipDirectory(repoID, orgID, dirFSID, prefix string, depth int, budget *zipTraversalBudget) ([]zipPreparedFile, error) {
	if budget != nil {
		if err := budget.noteDirectory(depth); err != nil {
			return nil, err
		}
	}

	var dirEntriesJSON string
	err := h.db.Session().Query(`
		SELECT dir_entries FROM fs_objects WHERE library_id = ? AND fs_id = ?
	`, repoID, dirFSID).Scan(&dirEntriesJSON)
	if err != nil || dirEntriesJSON == "" || dirEntriesJSON == "[]" {
		return nil, err
	}

	var entries []map[string]interface{}
	if err := json.Unmarshal([]byte(dirEntriesJSON), &entries); err != nil {
		return nil, fmt.Errorf("parse dir entries: %w", err)
	}

	prepared := make([]zipPreparedFile, 0, len(entries))
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
		if mode == 16384 || mode&0170000 == 040000 {
			childFiles, err := h.prepareZipDirectory(repoID, orgID, id, entryPath, depth+1, budget)
			if err != nil {
				return nil, err
			}
			prepared = append(prepared, childFiles...)
			continue
		}

		var blockIDs []string
		var fileSize int64
		err := h.db.Session().Query(`
			SELECT block_ids, size_bytes FROM fs_objects WHERE library_id = ? AND fs_id = ?
		`, repoID, id).Scan(&blockIDs, &fileSize)
		if err != nil {
			return nil, fmt.Errorf("load blocks for %s: %w", entryPath, err)
		}
		if budget != nil {
			if err := budget.noteFile(fileSize); err != nil {
				return nil, err
			}
		}

		resolvedIDs, err := zipBatchResolveBlockIDsFn(h.db, orgID, blockIDs)
		if err != nil {
			return nil, fmt.Errorf("resolve block IDs for %s: %w", entryPath, err)
		}

		prepared = append(prepared, zipPreparedFile{
			path:        entryPath,
			blockIDs:    append([]string(nil), blockIDs...),
			resolvedIDs: append([]string(nil), resolvedIDs...),
			sizeBytes:   fileSize,
		})
	}

	return prepared, nil
}

func (h *SeafHTTPHandler) addDirToZip(ctx context.Context, zw *zip.Writer, blockStore *storage.BlockStore, files []zipPreparedFile, fileKey []byte, fileIV []byte) error {
	for _, file := range files {
		if err := h.addFileToZip(ctx, zw, blockStore, file, fileKey, fileIV); err != nil {
			return err
		}
	}
	return nil
}

// addFileToZip streams a preflighted file's blocks into a ZIP archive entry.
// Uses zip.Store (no compression) for maximum throughput — the data is already
// compressed by S3/MinIO or is binary data where deflate adds CPU cost for minimal gain.
// For encrypted files, one block at a time is loaded, decrypted, and written.
func (h *SeafHTTPHandler) addFileToZip(ctx context.Context, zw *zip.Writer, blockStore *storage.BlockStore, file zipPreparedFile, fileKey []byte, fileIV []byte) error {
	header := &zip.FileHeader{
		Name:   file.path,
		Method: zip.Store,
	}
	if file.sizeBytes > 0 {
		header.UncompressedSize64 = uint64(file.sizeBytes)
	}
	w, err := zw.CreateHeader(header)
	if err != nil {
		return fmt.Errorf("create zip entry %s: %w", file.path, err)
	}

	buf := streaming.GetCopyBuf()
	defer streaming.PutCopyBuf(buf)

	for i, blockID := range file.blockIDs {
		internalID := file.resolvedIDs[i]
		_ = blockID

		if fileKey != nil {
			blockData, err := blockStore.GetBlock(ctx, internalID)
			if err != nil {
				return fmt.Errorf("get block %s for %s: %w", file.blockIDs[i], file.path, err)
			}
			decrypted, err := crypto.DecryptLibraryBlock(blockData, fileKey, fileIV)
			if err != nil {
				return fmt.Errorf("decrypt block for %s: %w", file.path, err)
			}
			if _, err := w.Write(decrypted); err != nil {
				return fmt.Errorf("write decrypted block for %s: %w", file.path, err)
			}
			continue
		}

		reader, err := blockStore.GetBlockReader(ctx, internalID)
		if err != nil {
			return fmt.Errorf("get block reader %s for %s: %w", file.blockIDs[i], file.path, err)
		}
		_, err = io.CopyBuffer(w, reader, buf)
		reader.Close()
		if err != nil {
			return fmt.Errorf("stream block %s for %s: %w", file.blockIDs[i], file.path, err)
		}
	}

	return nil
}
