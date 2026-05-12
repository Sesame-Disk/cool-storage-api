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
	"path"
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
	gocql "github.com/apache/cassandra-gocql-driver/v2"
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
	AttemptID   string
	TotalSize   int64
	CreatedAt   time.Time
	TempFile    *os.File
	TempPath    string
	ReceivedEnd int64 // Highest byte received; informational only.
	Ranges      []byteRange
	Finalizing  bool

	finalizationStarted    bool
	accountedBlockPosition map[int]string
	preflushedBlocks       map[int]uploadBlockPromotion
	preflushInFlight       map[int]chan struct{}
	nextPreflushBlockIndex int
	updatedAt              time.Time
	mu                     sync.Mutex
}

type byteRange struct {
	Start int64
	End   int64
}

// Chunked upload janitor tunables. The in-memory tracker TTL must be longer
// than any realistic pause between chunks. The disk TTL is the safety net for
// temp files orphaned by a process restart (map is gone, file remains).
const (
	chunkJanitorInterval = 10 * time.Minute
	chunkTrackerTTL      = 1 * time.Hour
	chunkDiskTTL         = 2 * time.Hour
	uploadRetryWindow    = 5 * time.Minute
)

var errChunkUploadTotalSizeMismatch = errors.New("chunked upload total size mismatch")

// ChunkManager manages chunked uploads
type ChunkManager struct {
	uploads map[string]*ChunkUpload // keyed by "token:filename"
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

func newUploadAttemptID() string {
	return strings.ReplaceAll(uuid.NewString(), "-", "")
}

// GetOrCreateUpload gets or creates a chunk upload tracker.
// The boolean reports whether a new tracker was created.
func (cm *ChunkManager) GetOrCreateUpload(token, filename, parentDir string, totalSize int64) (*ChunkUpload, bool, error) {
	key := token + ":" + filename
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if upload, exists := cm.uploads[key]; exists {
		if totalSize > 0 && upload.TotalSize > 0 && upload.TotalSize != totalSize {
			return nil, false, fmt.Errorf("%w: existing=%d requested=%d", errChunkUploadTotalSizeMismatch, upload.TotalSize, totalSize)
		}
		return upload, false, nil
	}

	// Create temp file
	tempPath := filepath.Join(cm.tempDir, fmt.Sprintf("sesamefs_upload_%s_%s", token, sanitizeFilename(filename)))
	tempFile, err := os.OpenFile(tempPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, false, fmt.Errorf("failed to create temp file: %w", err)
	}

	// Pre-allocate the file to total size (for seeking)
	if totalSize > 0 {
		if err := tempFile.Truncate(totalSize); err != nil {
			tempFile.Close()
			os.Remove(tempPath)
			return nil, false, fmt.Errorf("failed to pre-allocate temp file: %w", err)
		}
	}

	upload := &ChunkUpload{
		Token:       token,
		Filename:    filename,
		ParentDir:   parentDir,
		AttemptID:   newUploadAttemptID(),
		TotalSize:   totalSize,
		CreatedAt:   cm.now(),
		TempFile:    tempFile,
		TempPath:    tempPath,
		ReceivedEnd: -1,
		updatedAt:   cm.now(),
	}
	cm.uploads[key] = upload
	log.Printf("[ChunkManager] Created upload tracker: %s, totalSize=%d", key, totalSize)
	return upload, true, nil
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
	if cu.Finalizing || !cu.isCompleteLocked() {
		return false
	}
	cu.Finalizing = true
	cu.finalizationStarted = true
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

// NextFlushableContiguousBlock atomically reserves the next contiguous block
// available for preflush. On success the cursor is advanced before returning,
// so concurrent callers never receive the same index. The reservation is not
// rolled back on failure: a failed preflush simply leaves that block to be
// re-read and uploaded by the finalize path.
func (cu *ChunkUpload) NextFlushableContiguousBlock(blockSize int64) (int, int64, int64, bool) {
	cu.mu.Lock()
	defer cu.mu.Unlock()
	if blockSize <= 0 || cu.TotalSize <= blockSize {
		return 0, 0, 0, false
	}
	start := int64(cu.nextPreflushBlockIndex) * blockSize
	if start+blockSize > cu.TotalSize {
		return 0, 0, 0, false
	}
	end := start + blockSize - 1
	if !cu.hasRangeLocked(start, end) {
		return 0, 0, 0, false
	}
	reserved := cu.nextPreflushBlockIndex
	if cu.preflushInFlight == nil {
		cu.preflushInFlight = make(map[int]chan struct{})
	}
	cu.preflushInFlight[reserved] = make(chan struct{})
	cu.nextPreflushBlockIndex++
	return reserved, start, end, true
}

func (cu *ChunkUpload) finishPreflush(index int) {
	cu.mu.Lock()
	defer cu.mu.Unlock()
	if cu.preflushInFlight == nil {
		return
	}
	if done, ok := cu.preflushInFlight[index]; ok {
		delete(cu.preflushInFlight, index)
		close(done)
	}
	cu.updatedAt = time.Now()
}

// waitForPreflush blocks until any in-flight preflush for the given block
// index has finished (or returns immediately if none is running). Indices are
// monotonically assigned by NextFlushableContiguousBlock, so a closed channel
// is terminal — no re-check loop is needed.
func (cu *ChunkUpload) waitForPreflush(index int) {
	cu.mu.Lock()
	done, ok := cu.preflushInFlight[index]
	cu.mu.Unlock()
	if !ok {
		return
	}
	<-done
}

func (cu *ChunkUpload) ReadRange(start, end int64) ([]byte, error) {
	cu.mu.Lock()
	defer cu.mu.Unlock()
	if start < 0 || end < start {
		return nil, fmt.Errorf("invalid range: start=%d end=%d", start, end)
	}
	if _, err := cu.TempFile.Seek(start, io.SeekStart); err != nil {
		return nil, err
	}
	buf := make([]byte, end-start+1)
	if _, err := io.ReadFull(cu.TempFile, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// RecordPreflushedBlock stores the metadata for a block that was successfully
// preflushed. The preflush cursor is advanced by NextFlushableContiguousBlock
// when the block was reserved, so this method only records the result.
func (cu *ChunkUpload) RecordPreflushedBlock(index int, block uploadBlockPromotion) {
	cu.mu.Lock()
	defer cu.mu.Unlock()
	if cu.preflushedBlocks == nil {
		cu.preflushedBlocks = make(map[int]uploadBlockPromotion)
	}
	cu.preflushedBlocks[index] = block
	cu.updatedAt = time.Now()
}

func (cu *ChunkUpload) PreflushedBlock(index int) (uploadBlockPromotion, bool) {
	cu.mu.Lock()
	defer cu.mu.Unlock()
	if cu.preflushedBlocks == nil {
		return uploadBlockPromotion{}, false
	}
	block, ok := cu.preflushedBlocks[index]
	return block, ok
}

func (cu *ChunkUpload) ResolvePreflushedBlock(index int, blockSHA256, blockSHA1 string) (uploadBlockPromotion, bool, error) {
	cu.waitForPreflush(index)
	preflushedBlock, preflushed := cu.PreflushedBlock(index)
	alreadyAccounted, accountErr := cu.BlockAlreadyAccounted(index, blockSHA256)
	if accountErr != nil {
		return uploadBlockPromotion{}, false, fmt.Errorf("preflushed block %d mismatch: %w", index, accountErr)
	}
	if alreadyAccounted {
		if !preflushed {
			return uploadBlockPromotion{}, false, fmt.Errorf("block %d was marked preflushed but metadata is missing", index)
		}
		if preflushedBlock.BlockSHA1 != "" && preflushedBlock.BlockSHA1 != blockSHA1 {
			return uploadBlockPromotion{}, false, fmt.Errorf("preflushed block %d SHA-1 mismatch", index)
		}
	}
	return preflushedBlock, alreadyAccounted, nil
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

func normalizeUploadFilename(filename string) string {
	normalized := strings.ReplaceAll(strings.TrimSpace(filename), "\\", "/")
	base := path.Base(normalized)
	if base == "." || base == "/" || base == ".." {
		return ""
	}
	return base
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
	storage              *storage.S3Store
	storageManager       *storage.Manager
	db                   *db.DB
	tokenStore           TokenStore
	config               *config.Config
	permMiddleware       *middleware.PermissionMiddleware
	uploadStaging        UploadStagingStore
	uploadOrphanRecorder uploadOrphanRecorderFunc
	uploadOrphanCleaner  uploadOrphanCleanerFunc
	blockPromoter        uploadBlockPromoterFunc
	blockRollback        uploadBlockRollbackFunc
	commitPublisher      uploadCommitPublisherFunc
	uploadBlockResolver  uploadBlockStoreResolverFunc
	uploadBlockWriter    uploadBlockWriterFunc
	uploadBlockDeleter   uploadBlockDeleteFunc
	uploadBlockMetadata  uploadBlockMetadataLoaderFunc
	cleanupPendingSweep  uploadCleanupSweepFunc
	chunkedFinalize      chunkedFinalizeFunc
	zipMaxEntries        int
	zipMaxDepth          int
	zipMaxBytes          int64
	cleanupSweepActive   sync.Map
}

type finalizeUploadResult struct {
	FileID         string
	ActualFilename string
	CommitID       string
	Recovered      bool
}

type preparedUploadCommit struct {
	CommitID       string
	ActualFilename string
	ExpectedHead   string
}

type uploadBlockPromotion struct {
	BlockIndex   int
	BlockSHA1    string
	BlockSHA256  string
	SizeBytes    int
	StorageClass string
	StorageKey   string
	Source       UploadBlockSource
	CreatedAt    time.Time
	UploadedAt   *time.Time
}

type chunkedFinalizeFunc func(c *gin.Context, token *AccessToken, upload *ChunkUpload, uploadID, parentDir, filename, storageKey string, totalSize int64, replace bool) (finalizeUploadResult, error)

type uploadOrphanRecorderFunc func(orgID, blockID, storageClass, errMsg string) error
type uploadOrphanCleanerFunc func(orgID, blockID string) error
type uploadBlockPromoterFunc func(orgID, operationKey, blockID string, sizeBytes int, storageClass, storageKey string) error
type uploadBlockRollbackFunc func(orgID, operationKey string, blockIDs []string) []string
type uploadCommitPublisherFunc func(orgID, repoID, commitID, expectedHead string) error
type uploadBlockStoreResolverFunc func(c *gin.Context, token *AccessToken) (*storage.BlockStore, string, error)
type uploadBlockWriterFunc func(ctx context.Context, blockStore *storage.BlockStore, hash string, data []byte) (string, error)
type uploadBlockDeleteFunc func(ctx context.Context, blockStore *storage.BlockStore, hash string) error
type uploadBlockMetadata struct {
	Exists       bool
	RefCount     int
	StorageClass string
	StorageKey   string
}
type uploadBlockMetadataLoaderFunc func(orgID, blockID string) (uploadBlockMetadata, error)
type uploadCleanupSweepFunc func(orgID string, limit int)

const (
	defaultZipMaxEntries     = 100000
	defaultZipMaxDepth       = 64
	defaultZipMaxBytes       = 10 * 1024 * 1024 * 1024 // 10 GiB of uncompressed file content
	uploadRecoveryBatchLimit = 8
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
	handler := &SeafHTTPHandler{
		storage:        s3Store,
		storageManager: storageManager,
		db:             database,
		tokenStore:     tokenStore,
		config:         cfg,
		permMiddleware: permMiddleware,
		uploadStaging:  NewCassandraUploadStagingStore(database),
		zipMaxEntries:  defaultZipMaxEntries,
		zipMaxDepth:    defaultZipMaxDepth,
		zipMaxBytes:    defaultZipMaxBytes,
	}
	handler.chunkedFinalize = handler.finalizeUploadStreaming
	handler.uploadOrphanRecorder = handler.recordUploadS3OrphanRow
	handler.uploadOrphanCleaner = handler.deleteUploadS3OrphanRow
	handler.uploadBlockResolver = func(c *gin.Context, token *AccessToken) (*storage.BlockStore, string, error) {
		return handler.resolveLibraryBlockStore(httputil.GetRoutingHostname(c, handler.configuredServerURL()), token.OrgID, token.RepoID)
	}
	handler.uploadBlockWriter = func(ctx context.Context, blockStore *storage.BlockStore, hash string, data []byte) (string, error) {
		if blockStore == nil {
			return "", fmt.Errorf("block store is not available")
		}
		return blockStore.PutBlockAuto(ctx, hash, data)
	}
	handler.uploadBlockDeleter = func(ctx context.Context, blockStore *storage.BlockStore, hash string) error {
		if blockStore == nil {
			return fmt.Errorf("block store is not available")
		}
		return blockStore.DeleteBlock(ctx, hash)
	}
	handler.uploadBlockMetadata = func(orgID, blockID string) (uploadBlockMetadata, error) {
		metadata, err := v2.NewFSHelper(handler.db).GetBlockMetadata(orgID, blockID)
		if err != nil {
			return uploadBlockMetadata{}, err
		}
		return uploadBlockMetadata{
			Exists:       metadata.Exists,
			RefCount:     metadata.RefCount,
			StorageClass: metadata.StorageClass,
			StorageKey:   metadata.StorageKey,
		}, nil
	}
	handler.blockPromoter = func(orgID, operationKey, blockID string, sizeBytes int, storageClass, storageKey string) error {
		return v2.NewFSHelper(handler.db).IncrementOrCreateBlockOnce(orgID, operationKey, blockID, sizeBytes, storageClass, storageKey)
	}
	handler.blockRollback = func(orgID, operationKey string, blockIDs []string) []string {
		return v2.NewFSHelper(handler.db).DecrementBlockRefCountsOnce(orgID, operationKey, blockIDs)
	}
	handler.commitPublisher = func(orgID, repoID, commitID, expectedHead string) error {
		return v2.NewFSHelper(handler.db).UpdateLibraryHeadWithExpected(orgID, repoID, commitID, expectedHead)
	}
	handler.cleanupPendingSweep = handler.scheduleCleanupPendingSweep
	return handler
}

func (h *SeafHTTPHandler) SetUploadStagingStore(store UploadStagingStore) {
	h.uploadStaging = store
}

func (h *SeafHTTPHandler) SetUploadOrphanRecorder(recorder uploadOrphanRecorderFunc) {
	h.uploadOrphanRecorder = recorder
}

func (h *SeafHTTPHandler) clearUploadS3Orphan(orgID, blockID string) {
	if h == nil || h.uploadOrphanCleaner == nil || strings.TrimSpace(blockID) == "" {
		return
	}
	if err := h.uploadOrphanCleaner(orgID, blockID); err != nil {
		log.Printf("[HandleUpload] WARNING: Failed to clear pending S3 orphan org=%s block=%s: %v", orgID, shortLogID(blockID), err)
	}
}

func shortLogID(id string) string {
	if len(id) <= 16 {
		return id
	}
	return id[:16]
}

func (h *SeafHTTPHandler) persistUploadBlockState(record UploadSessionBlockRecord, stateLabel string) {
	if h.uploadStaging == nil {
		return
	}
	if err := h.uploadStaging.UpsertBlock(record); err != nil {
		log.Printf("[finalizeUploadStreaming] WARNING: Failed to persist staging block state=%s upload=%s index=%d sha256=%s: %v", stateLabel, shortLogID(record.UploadID), record.BlockIndex, shortLogID(record.BlockSHA256), err)
	}
}

func (h *SeafHTTPHandler) persistUploadS3Orphan(orgID, blockID, storageClass, errMsg string) {
	if h == nil || h.uploadOrphanRecorder == nil || strings.TrimSpace(blockID) == "" {
		return
	}
	if err := h.uploadOrphanRecorder(orgID, blockID, storageClass, errMsg); err != nil {
		log.Printf("[HandleUpload] WARNING: Failed to record pending S3 orphan org=%s block=%s: %v", orgID, shortLogID(blockID), err)
	}
}

func (h *SeafHTTPHandler) recordUploadS3OrphanRow(orgID, blockID, storageClass, errMsg string) error {
	if h == nil || h.db == nil {
		return nil
	}
	parsedOrgID, err := uuid.Parse(strings.TrimSpace(orgID))
	if err != nil {
		return fmt.Errorf("parse org id for S3 orphan: %w", err)
	}
	if strings.TrimSpace(storageClass) == "" {
		return fmt.Errorf("record S3 orphan: storage class is required")
	}
	now := time.Now().UTC()
	initialRetryCount := 0
	if errMsg != "" {
		initialRetryCount = 1
	}
	applied, err := h.db.Session().Query(`
		INSERT INTO gc_s3_orphans (org_id, block_id, storage_class, first_seen_at, last_attempt_at, retry_count, last_error)
		VALUES (?, ?, ?, ?, ?, ?, ?) IF NOT EXISTS
	`, parsedOrgID, blockID, storageClass, now, now, initialRetryCount, errMsg).ScanCAS(nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		return fmt.Errorf("record S3 orphan: %w", err)
	}
	if applied || errMsg == "" {
		return nil
	}
	var prev int
	err = h.db.Session().Query(`
		SELECT retry_count FROM gc_s3_orphans WHERE org_id = ? AND block_id = ?
	`, parsedOrgID, blockID).Scan(&prev)
	if err != nil && !errors.Is(err, gocql.ErrNotFound) {
		return fmt.Errorf("read prior S3 orphan retry count: %w", err)
	}
	return h.db.Session().Query(`
		UPDATE gc_s3_orphans
		SET last_attempt_at = ?, retry_count = ?, last_error = ?
		WHERE org_id = ? AND block_id = ?
	`, now, prev+1, errMsg, parsedOrgID, blockID).Exec()
}

func (h *SeafHTTPHandler) deleteUploadS3OrphanRow(orgID, blockID string) error {
	if h == nil || h.db == nil || strings.TrimSpace(blockID) == "" {
		return nil
	}
	parsedOrgID, err := uuid.Parse(strings.TrimSpace(orgID))
	if err != nil {
		return fmt.Errorf("parse org id for S3 orphan delete: %w", err)
	}
	return h.db.Session().Query(`
		DELETE FROM gc_s3_orphans WHERE org_id = ? AND block_id = ?
	`, parsedOrgID, blockID).Exec()
}

func (h *SeafHTTPHandler) rollbackPromotedUploadBlocks(orgID, uploadID, commitID string, blockIDs []string) {
	if h == nil || h.blockRollback == nil || len(blockIDs) == 0 {
		return
	}
	operationKey := fmt.Sprintf("upload-rollback:%s:%s", uploadID, commitID)
	zeroRefBlocks := h.blockRollback(orgID, operationKey, blockIDs)
	if len(zeroRefBlocks) > 0 {
		log.Printf("[publishPreparedUpload] Rolled back %d promoted blocks to zero refs for upload=%s commit=%s", len(zeroRefBlocks), shortLogID(uploadID), shortLogID(commitID))
	}
}

func (h *SeafHTTPHandler) releaseUploadPromotionClaims(orgID, uploadID string, blocks []uploadBlockPromotion) {
	if h == nil || h.uploadStaging == nil || len(blocks) == 0 {
		return
	}
	for _, block := range blocks {
		if err := h.uploadStaging.DeleteBlockPromotion(orgID, uploadID, block.BlockIndex); err != nil {
			log.Printf("[publishPreparedUpload] WARNING: Failed to release promotion claim upload=%s index=%d sha256=%s: %v", shortLogID(uploadID), block.BlockIndex, shortLogID(block.BlockSHA256), err)
		}
	}
}

func (h *SeafHTTPHandler) persistPromotedUploadBlock(uploadID, orgID string, block uploadBlockPromotion, promotedAt time.Time) {
	if h.uploadStaging == nil {
		return
	}
	promotedAtUTC := promotedAt.UTC()
	h.persistUploadBlockState(UploadSessionBlockRecord{
		UploadID:     uploadID,
		BlockIndex:   block.BlockIndex,
		OrgID:        orgID,
		BlockSHA1:    block.BlockSHA1,
		BlockSHA256:  block.BlockSHA256,
		SizeBytes:    block.SizeBytes,
		StorageClass: block.StorageClass,
		StorageKey:   block.StorageKey,
		Source:       block.Source,
		State:        UploadBlockStatePromoted,
		CreatedAt:    block.CreatedAt,
		UpdatedAt:    promotedAtUTC,
		UploadedAt:   block.UploadedAt,
		PromotedAt:   &promotedAtUTC,
	}, string(UploadBlockStatePromoted))
}

func (h *SeafHTTPHandler) closeRecoveredUploadSession(session *UploadSessionRecord) error {
	if h == nil || h.uploadStaging == nil || session == nil {
		return nil
	}
	now := time.Now().UTC()
	return h.uploadStaging.UpsertSession(UploadSessionRecord{
		UploadID:       session.UploadID,
		OrgID:          session.OrgID,
		RepoID:         session.RepoID,
		UserID:         session.UserID,
		TokenID:        session.TokenID,
		ParentDir:      session.ParentDir,
		Filename:       session.Filename,
		ActualFilename: session.ActualFilename,
		CommitID:       session.CommitID,
		TotalSize:      session.TotalSize,
		State:          UploadSessionStateClosed,
		CreatedAt:      session.CreatedAt,
		UpdatedAt:      now,
		ExpiresAt:      now.Add(uploadRetryWindow),
	})
}

func (h *SeafHTTPHandler) loadCommitParentID(repoID, commitID string) string {
	if h == nil || h.db == nil || strings.TrimSpace(repoID) == "" || strings.TrimSpace(commitID) == "" {
		return ""
	}
	var parentID string
	if err := h.db.Session().Query(`
		SELECT parent_id FROM commits WHERE library_id = ? AND commit_id = ?
	`, repoID, commitID).Scan(&parentID); err != nil {
		log.Printf("[HandleUpload] WARNING: Failed to read parent commit for upload commit=%s repo=%s: %v", shortLogID(commitID), repoID, err)
		return ""
	}
	return strings.TrimSpace(parentID)
}

func shouldPreserveUploadSession(existing *UploadSessionRecord) bool {
	if existing == nil {
		return false
	}
	switch existing.State {
	case UploadSessionStateClosed, UploadSessionStatePromoting:
		return strings.TrimSpace(existing.CommitID) != ""
	case UploadSessionStateCleanupPending:
		return true
	default:
		return false
	}
}

func canPurgeStalePromotions(state UploadSessionState) bool {
	return state == UploadSessionStateClosed || state == UploadSessionStateCleanupPending
}

func uploadBlockNeedsCleanup(record UploadSessionBlockRecord) bool {
	switch record.State {
	case UploadBlockStateRegistered, UploadBlockStatePreflushed, UploadBlockStateUploaded, UploadBlockStateCleanupPending:
		return true
	default:
		return false
	}
}

func uploadSessionHoldsStagedBlock(state UploadSessionState) bool {
	switch state {
	case UploadSessionStateReceiving, UploadSessionStatePromoting:
		return true
	default:
		return false
	}
}

func (h *SeafHTTPHandler) resolveUploadCleanupBlockStore(storageClass string) (*storage.BlockStore, error) {
	if h == nil {
		return nil, fmt.Errorf("upload handler is required")
	}
	if h.storageManager != nil {
		if strings.TrimSpace(storageClass) != "" {
			blockStore, err := h.storageManager.GetBlockStore(storageClass)
			if err == nil {
				return blockStore, nil
			}
			return nil, fmt.Errorf("resolve cleanup block store %q: %w", storageClass, err)
		}
		blockStore, _, err := h.storageManager.GetHealthyBlockStore("")
		if err == nil {
			return blockStore, nil
		}
	}
	if h.storage != nil && strings.TrimSpace(storageClass) == "" {
		return storage.NewBlockStore(h.storage, "blocks/"), nil
	}
	return nil, fmt.Errorf("block store is not available")
}

func (h *SeafHTTPHandler) scheduleCleanupPendingSweep(orgID string, limit int) {
	if h == nil || strings.TrimSpace(orgID) == "" {
		return
	}
	if _, loaded := h.cleanupSweepActive.LoadOrStore(orgID, struct{}{}); loaded {
		return
	}
	go func() {
		defer h.cleanupSweepActive.Delete(orgID)
		h.cleanupPendingUploadsForOrg(orgID, limit)
	}()
}

func (h *SeafHTTPHandler) materializeUploadBlock(ctx context.Context, blockStore *storage.BlockStore, orgID, blockSHA256 string, storedBlock []byte, preferredStorageClass string) (UploadBlockSource, string, string, *time.Time, error) {
	if h == nil {
		return UploadBlockSourceNewObject, "", "", nil, fmt.Errorf("upload handler is required")
	}
	storageClass := preferredStorageClass
	storageKey := blockSHA256
	if h.uploadBlockMetadata != nil {
		metadata, err := h.uploadBlockMetadata(orgID, blockSHA256)
		if err != nil {
			return UploadBlockSourceNewObject, "", "", nil, fmt.Errorf("probe existing block metadata: %w", err)
		}
		if metadata.Exists && metadata.RefCount > 0 {
			if strings.TrimSpace(metadata.StorageClass) != "" {
				storageClass = metadata.StorageClass
			}
			if strings.TrimSpace(metadata.StorageKey) != "" {
				storageKey = metadata.StorageKey
			}
			return UploadBlockSourceExistingLive, storageClass, storageKey, nil, nil
		}
	}
	if _, err := h.uploadBlockWriter(ctx, blockStore, blockSHA256, storedBlock); err != nil {
		return UploadBlockSourceNewObject, "", "", nil, err
	}
	uploadedAt := time.Now().UTC()
	return UploadBlockSourceNewObject, storageClass, storageKey, &uploadedAt, nil
}

func (h *SeafHTTPHandler) uploadBlockHasOtherActiveStagedOwners(orgID, uploadID, blockSHA256 string) (bool, error) {
	if h == nil || h.uploadStaging == nil {
		return false, nil
	}
	refs, err := h.uploadStaging.ListBlocksBySHA256(orgID, blockSHA256)
	if err != nil {
		return false, err
	}
	for _, ref := range refs {
		if ref.UploadID == uploadID {
			continue
		}
		session, err := h.uploadStaging.GetSession(orgID, ref.UploadID)
		if err != nil {
			return false, err
		}
		if session != nil && uploadSessionHoldsStagedBlock(session.State) {
			return true, nil
		}
	}
	return false, nil
}

func (h *SeafHTTPHandler) markUploadBlockCleaned(record UploadSessionBlockRecord, cleanedAt time.Time) {
	if h.uploadStaging == nil {
		return
	}
	cleanedAtUTC := cleanedAt.UTC()
	h.persistUploadBlockState(UploadSessionBlockRecord{
		UploadID:     record.UploadID,
		BlockIndex:   record.BlockIndex,
		OrgID:        record.OrgID,
		BlockSHA1:    record.BlockSHA1,
		BlockSHA256:  record.BlockSHA256,
		SizeBytes:    record.SizeBytes,
		StorageClass: record.StorageClass,
		StorageKey:   record.StorageKey,
		Source:       record.Source,
		State:        UploadBlockStateCleaned,
		CreatedAt:    record.CreatedAt,
		UpdatedAt:    cleanedAtUTC,
		UploadedAt:   record.UploadedAt,
		PromotedAt:   record.PromotedAt,
	}, string(UploadBlockStateCleaned))
}

func (h *SeafHTTPHandler) markUploadBlockCleanupPending(record UploadSessionBlockRecord, cleanupAt time.Time) {
	if h.uploadStaging == nil {
		return
	}
	cleanupAtUTC := cleanupAt.UTC()
	h.persistUploadBlockState(UploadSessionBlockRecord{
		UploadID:     record.UploadID,
		BlockIndex:   record.BlockIndex,
		OrgID:        record.OrgID,
		BlockSHA1:    record.BlockSHA1,
		BlockSHA256:  record.BlockSHA256,
		SizeBytes:    record.SizeBytes,
		StorageClass: record.StorageClass,
		StorageKey:   record.StorageKey,
		Source:       record.Source,
		State:        UploadBlockStateCleanupPending,
		CreatedAt:    record.CreatedAt,
		UpdatedAt:    cleanupAtUTC,
		UploadedAt:   record.UploadedAt,
		PromotedAt:   record.PromotedAt,
	}, string(UploadBlockStateCleanupPending))
}

func (h *SeafHTTPHandler) cleanupPendingUploadSession(session *UploadSessionRecord) (bool, error) {
	if h == nil || h.uploadStaging == nil || session == nil {
		return false, nil
	}
	current, err := h.uploadStaging.GetSession(session.OrgID, session.UploadID)
	if err != nil {
		return false, fmt.Errorf("reload cleanup_pending upload session: %w", err)
	}
	if current == nil || current.State != UploadSessionStateCleanupPending {
		return false, nil
	}
	promotions, err := h.uploadStaging.ListBlockPromotions(current.OrgID, current.UploadID)
	if err != nil {
		return false, fmt.Errorf("list cleanup_pending upload promotions: %w", err)
	}
	appliedPromotionBlockIDs := make(map[int]string, len(promotions))
	for _, promotion := range promotions {
		if promotion.AppliedAt != nil {
			appliedPromotionBlockIDs[promotion.BlockIndex] = promotion.BlockSHA256
		}
	}
	blocks, err := h.uploadStaging.ListBlocks(current.UploadID)
	if err != nil {
		return false, fmt.Errorf("list cleanup_pending upload blocks: %w", err)
	}
	if len(appliedPromotionBlockIDs) > 0 && h.blockRollback == nil {
		return false, fmt.Errorf("cleanup_pending upload has applied promotions but block rollback is not configured")
	}
	if len(appliedPromotionBlockIDs) > 0 {
		appliedBlockIDs := make([]string, 0, len(appliedPromotionBlockIDs))
		for _, blockID := range appliedPromotionBlockIDs {
			appliedBlockIDs = append(appliedBlockIDs, blockID)
		}
		h.rollbackPromotedUploadBlocks(current.OrgID, current.UploadID, current.CommitID, appliedBlockIDs)
	}
	for _, block := range blocks {
		if !uploadBlockNeedsCleanup(block) {
			continue
		}
		cleanupAt := time.Now().UTC()
		h.markUploadBlockCleanupPending(block, cleanupAt)
		if _, promoted := appliedPromotionBlockIDs[block.BlockIndex]; !promoted && block.Source == UploadBlockSourceNewObject && block.UploadedAt != nil {
			if h.uploadBlockMetadata != nil {
				metadata, err := h.uploadBlockMetadata(current.OrgID, block.BlockSHA256)
				if err != nil {
					return false, fmt.Errorf("probe cleanup block metadata for upload %s block %d: %w", shortLogID(current.UploadID), block.BlockIndex, err)
				}
				if metadata.Exists && metadata.RefCount > 0 {
					h.markUploadBlockCleaned(block, time.Now().UTC())
					continue
				}
			}
			heldByOtherUpload, err := h.uploadBlockHasOtherActiveStagedOwners(current.OrgID, current.UploadID, block.BlockSHA256)
			if err != nil {
				return false, fmt.Errorf("inspect staged upload owners for upload %s block %d: %w", shortLogID(current.UploadID), block.BlockIndex, err)
			}
			if heldByOtherUpload {
				h.markUploadBlockCleaned(block, time.Now().UTC())
				continue
			}
			blockStore, err := h.resolveUploadCleanupBlockStore(block.StorageClass)
			if err != nil {
				return false, fmt.Errorf("resolve cleanup block store for upload %s block %d: %w", shortLogID(current.UploadID), block.BlockIndex, err)
			}
			if err := h.uploadBlockDeleter(context.Background(), blockStore, block.BlockSHA256); err != nil {
				return false, fmt.Errorf("delete staged block for upload %s block %d: %w", shortLogID(current.UploadID), block.BlockIndex, err)
			}
		}
		h.markUploadBlockCleaned(block, time.Now().UTC())
	}
	if err := h.uploadStaging.DeleteAllBlockPromotions(current.OrgID, current.UploadID); err != nil {
		return false, fmt.Errorf("delete cleanup_pending upload promotions: %w", err)
	}
	now := time.Now().UTC()
	if err := h.uploadStaging.UpsertSession(UploadSessionRecord{
		UploadID:       current.UploadID,
		OrgID:          current.OrgID,
		RepoID:         current.RepoID,
		UserID:         current.UserID,
		TokenID:        current.TokenID,
		ParentDir:      current.ParentDir,
		Filename:       current.Filename,
		ActualFilename: current.ActualFilename,
		CommitID:       current.CommitID,
		TotalSize:      current.TotalSize,
		State:          UploadSessionStateAborted,
		CreatedAt:      current.CreatedAt,
		UpdatedAt:      now,
		ExpiresAt:      now.Add(chunkDiskTTL),
		LastError:      current.LastError,
	}); err != nil {
		return false, fmt.Errorf("mark cleanup_pending upload aborted: %w", err)
	}
	return true, nil
}

func (h *SeafHTTPHandler) cleanupPendingUploadsForOrg(orgID string, limit int) {
	if h == nil || h.uploadStaging == nil || strings.TrimSpace(orgID) == "" {
		return
	}
	sessions, err := h.uploadStaging.ListSessionsByState(orgID, UploadSessionStateCleanupPending, limit)
	if err != nil {
		log.Printf("[HandleUpload] WARNING: Failed to list cleanup_pending uploads for cleanup org=%s: %v", orgID, err)
		return
	}
	seen := make(map[string]struct{}, len(sessions))
	for _, session := range sessions {
		if _, ok := seen[session.UploadID]; ok {
			continue
		}
		seen[session.UploadID] = struct{}{}
		cleaned, err := h.cleanupPendingUploadSession(&session)
		if err != nil {
			log.Printf("[HandleUpload] WARNING: Failed to clean cleanup_pending upload org=%s upload=%s: %v", orgID, shortLogID(session.UploadID), err)
			continue
		}
		if cleaned {
			log.Printf("[HandleUpload] Cleaned upload-owned staged blocks for cleanup_pending upload org=%s upload=%s", orgID, shortLogID(session.UploadID))
		}
	}
}

// purgeStalePromotions drops promotion claims for an upload. Called lazily
// from recoverPreparedUploadIfPossible when a closed session's claims belong
// to an older revision and would otherwise block the new revision's
// TryStartBlockPromotion with a marker mismatch. Errors are logged only —
// the caller proceeds with the normal flow regardless, since a residual claim
// only causes the next attempt to repeat the same recovery branch.
//
// We deliberately do NOT call this on every successful close. Promotion rows
// are the only signal that lets recoverPreparedUploadIfPossible recognise a
// closed session as "already succeeded" and short-circuit duplicate revisions
// when the client retries after a successful response was lost in transit.
// They live until a fresh upload with mismatching content/commit invalidates
// them, at which point this helper purges them on the spot.
func (h *SeafHTTPHandler) purgeStalePromotions(orgID, uploadID string) {
	if h == nil || h.uploadStaging == nil {
		return
	}
	if err := h.uploadStaging.DeleteAllBlockPromotions(orgID, uploadID); err != nil {
		log.Printf("[HandleUpload] WARNING: Failed to purge stale promotion claims org=%s upload=%s: %v", orgID, shortLogID(uploadID), err)
	}
}

func (h *SeafHTTPHandler) markUploadSessionCleanupPending(session *UploadSessionRecord, errMsg string) error {
	if h == nil || h.uploadStaging == nil || session == nil {
		return nil
	}
	now := time.Now().UTC()
	return h.uploadStaging.UpsertSession(UploadSessionRecord{
		UploadID:       session.UploadID,
		OrgID:          session.OrgID,
		RepoID:         session.RepoID,
		UserID:         session.UserID,
		TokenID:        session.TokenID,
		ParentDir:      session.ParentDir,
		Filename:       session.Filename,
		ActualFilename: session.ActualFilename,
		CommitID:       session.CommitID,
		TotalSize:      session.TotalSize,
		State:          UploadSessionStateCleanupPending,
		CreatedAt:      session.CreatedAt,
		UpdatedAt:      now,
		ExpiresAt:      now.Add(chunkDiskTTL),
		LastError:      errMsg,
	})
}

func (h *SeafHTTPHandler) recoverPromotingUploadSession(session *UploadSessionRecord) error {
	if h == nil || h.uploadStaging == nil || session == nil {
		return nil
	}
	current, err := h.uploadStaging.GetSession(session.OrgID, session.UploadID)
	if err != nil {
		return fmt.Errorf("reload upload session: %w", err)
	}
	if current == nil || current.State != UploadSessionStatePromoting || strings.TrimSpace(current.CommitID) == "" {
		return nil
	}
	promotions, err := h.uploadStaging.ListBlockPromotions(current.OrgID, current.UploadID)
	if err != nil {
		return fmt.Errorf("list upload block promotions: %w", err)
	}
	if len(promotions) == 0 {
		return nil
	}
	now := time.Now().UTC()
	for _, promotion := range promotions {
		if promotion.CommitID != current.CommitID {
			return nil
		}
		if promotion.AppliedAt == nil {
			if !current.ExpiresAt.IsZero() && now.After(current.ExpiresAt) {
				return h.markUploadSessionCleanupPending(current, fmt.Sprintf("upload promotion marker still pending for block %d after expiry", promotion.BlockIndex))
			}
			return nil
		}
	}
	if h.commitPublisher == nil {
		return fmt.Errorf("commit publisher is not configured")
	}
	expectedHead := h.loadCommitParentID(current.RepoID, current.CommitID)
	if err := h.commitPublisher(current.OrgID, current.RepoID, current.CommitID, expectedHead); err != nil {
		if v2.IsLibraryHeadPublished(err) || v2.IsLibraryHeadAlreadyPublished(err, current.CommitID) {
			log.Printf("[HandleUpload] WARNING: Recovered upload org=%s upload=%s published canonical head but secondary projections need repair: %v", current.OrgID, shortLogID(current.UploadID), err)
		} else if v2.IsLibraryHeadConflict(err) {
			return h.markUploadSessionCleanupPending(current, fmt.Sprintf("head conflict while recovering promoting upload: %v", err))
		} else {
			return fmt.Errorf("publish recovered upload commit: %w", err)
		}
	}
	return h.closeRecoveredUploadSession(current)
}

func (h *SeafHTTPHandler) recoverPromotingUploadsForOrg(orgID string, limit int) {
	if h == nil || h.uploadStaging == nil || strings.TrimSpace(orgID) == "" {
		return
	}
	sessions, err := h.uploadStaging.ListSessionsByState(orgID, UploadSessionStatePromoting, limit)
	if err != nil {
		log.Printf("[HandleUpload] WARNING: Failed to list promoting uploads for recovery org=%s: %v", orgID, err)
		return
	}
	seen := make(map[string]struct{}, len(sessions))
	for _, session := range sessions {
		if _, ok := seen[session.UploadID]; ok {
			continue
		}
		seen[session.UploadID] = struct{}{}
		if err := h.recoverPromotingUploadSession(&session); err != nil {
			log.Printf("[HandleUpload] WARNING: Failed to recover promoting upload org=%s upload=%s: %v", orgID, shortLogID(session.UploadID), err)
		}
	}
}

func (h *SeafHTTPHandler) recoverPreparedUploadIfPossible(orgID, repoID, uploadID string, blocks []uploadBlockPromotion) (preparedUploadCommit, bool, error) {
	if h == nil || h.uploadStaging == nil || len(blocks) == 0 {
		return preparedUploadCommit{}, false, nil
	}
	session, err := h.uploadStaging.GetSession(orgID, uploadID)
	if err != nil {
		return preparedUploadCommit{}, false, fmt.Errorf("read upload session: %w", err)
	}
	if session == nil {
		return preparedUploadCommit{}, false, nil
	}
	if strings.TrimSpace(session.CommitID) == "" {
		if canPurgeStalePromotions(session.State) {
			promotions, err := h.uploadStaging.ListBlockPromotions(orgID, uploadID)
			if err != nil {
				return preparedUploadCommit{}, false, fmt.Errorf("list upload block promotions: %w", err)
			}
			if len(promotions) > 0 {
				h.purgeStalePromotions(orgID, uploadID)
			}
		}
		return preparedUploadCommit{}, false, nil
	}
	if session.State != UploadSessionStatePromoting && session.State != UploadSessionStateClosed && session.State != UploadSessionStateCleanupPending {
		return preparedUploadCommit{}, false, nil
	}
	promotions, err := h.uploadStaging.ListBlockPromotions(orgID, uploadID)
	if err != nil {
		return preparedUploadCommit{}, false, fmt.Errorf("list upload block promotions: %w", err)
	}
	// Mismatched cardinality between live promotions and the new upload's
	// blocks indicates a different revision (different chunking or content).
	// Drop stale claims if the session already terminated so the new upload
	// can register fresh markers.
	if len(promotions) != len(blocks) {
		if canPurgeStalePromotions(session.State) {
			h.purgeStalePromotions(orgID, uploadID)
		}
		return preparedUploadCommit{}, false, nil
	}
	actualFilename := session.ActualFilename
	if strings.TrimSpace(actualFilename) == "" {
		actualFilename = session.Filename
	}
	prepared := preparedUploadCommit{CommitID: session.CommitID, ActualFilename: actualFilename, ExpectedHead: h.loadCommitParentID(repoID, session.CommitID)}
	promotionsByIndex := make(map[int]UploadBlockPromotionRecord, len(promotions))
	for _, promotion := range promotions {
		promotionsByIndex[promotion.BlockIndex] = promotion
	}
	if session.State == UploadSessionStateClosed && !session.ExpiresAt.IsZero() && time.Now().UTC().After(session.ExpiresAt) {
		h.purgeStalePromotions(orgID, uploadID)
		return preparedUploadCommit{}, false, nil
	}
	hasPendingPromotion := false
	for _, block := range blocks {
		promotion, ok := promotionsByIndex[block.BlockIndex]
		if !ok {
			if canPurgeStalePromotions(session.State) {
				h.purgeStalePromotions(orgID, uploadID)
			}
			return preparedUploadCommit{}, false, nil
		}
		if promotion.CommitID != session.CommitID || promotion.BlockSHA256 != block.BlockSHA256 {
			if canPurgeStalePromotions(session.State) {
				h.purgeStalePromotions(orgID, uploadID)
			}
			return preparedUploadCommit{}, false, nil
		}
		if promotion.AppliedAt == nil {
			hasPendingPromotion = true
		}
	}
	if hasPendingPromotion {
		if session.State == UploadSessionStateClosed {
			return preparedUploadCommit{}, false, fmt.Errorf("closed upload session has pending promotion markers")
		}
		if session.State == UploadSessionStateCleanupPending {
			return preparedUploadCommit{}, false, nil
		}
		return prepared, false, nil
	}
	if session.State == UploadSessionStatePromoting {
		if h.commitPublisher == nil {
			return preparedUploadCommit{}, false, fmt.Errorf("commit publisher is not configured")
		}
		if err := h.commitPublisher(orgID, repoID, session.CommitID, prepared.ExpectedHead); err != nil {
			if v2.IsLibraryHeadPublished(err) || v2.IsLibraryHeadAlreadyPublished(err, session.CommitID) {
				log.Printf("[HandleUpload] WARNING: Recovered upload org=%s upload=%s published canonical head but secondary projections need repair: %v", orgID, shortLogID(uploadID), err)
			} else if v2.IsLibraryHeadConflict(err) {
				if markErr := h.markUploadSessionCleanupPending(session, fmt.Sprintf("head conflict while recovering upload commit: %v", err)); markErr != nil {
					return preparedUploadCommit{}, false, fmt.Errorf("mark conflicting recovered upload cleanup_pending: %w", markErr)
				}
				return preparedUploadCommit{}, false, nil
			} else {
				return preparedUploadCommit{}, false, fmt.Errorf("publish recovered upload commit: %w", err)
			}
		}
		if err := h.closeRecoveredUploadSession(session); err != nil {
			return preparedUploadCommit{}, false, fmt.Errorf("close recovered upload session: %w", err)
		}
	}
	if session.State == UploadSessionStateCleanupPending {
		return preparedUploadCommit{}, false, nil
	}
	return prepared, true, nil
}

func (h *SeafHTTPHandler) publishPreparedUpload(orgID, repoID, uploadID string, prepared preparedUploadCommit, blocks []uploadBlockPromotion) error {
	if h == nil {
		return fmt.Errorf("upload handler is required")
	}
	if prepared.CommitID == "" {
		return fmt.Errorf("prepared commit id is required")
	}
	if h.blockPromoter == nil {
		return fmt.Errorf("block promoter is not configured")
	}
	if h.commitPublisher == nil {
		return fmt.Errorf("commit publisher is not configured")
	}

	newlyPromotedBlockIDs := make([]string, 0, len(blocks))
	newlyClaimedBlocks := make([]uploadBlockPromotion, 0, len(blocks))
	for _, block := range blocks {
		claimInserted := false
		claimTime := time.Now().UTC()
		if h.uploadStaging != nil {
			attempt, err := h.uploadStaging.TryStartBlockPromotion(UploadBlockPromotionRecord{
				OrgID:       orgID,
				UploadID:    uploadID,
				BlockIndex:  block.BlockIndex,
				BlockSHA256: block.BlockSHA256,
				CommitID:    prepared.CommitID,
				ClaimedAt:   claimTime,
			})
			if err != nil {
				h.rollbackPromotedUploadBlocks(orgID, uploadID, prepared.CommitID, newlyPromotedBlockIDs)
				h.releaseUploadPromotionClaims(orgID, uploadID, newlyClaimedBlocks)
				return fmt.Errorf("failed to claim block promotion for block %d: %w", block.BlockIndex, err)
			}
			if !attempt.Inserted {
				if attempt.BlockSHA256 != block.BlockSHA256 || attempt.CommitID != prepared.CommitID {
					h.rollbackPromotedUploadBlocks(orgID, uploadID, prepared.CommitID, newlyPromotedBlockIDs)
					h.releaseUploadPromotionClaims(orgID, uploadID, newlyClaimedBlocks)
					return fmt.Errorf("block %d promotion marker mismatch", block.BlockIndex)
				}
				if attempt.AppliedAt != nil {
					h.persistPromotedUploadBlock(uploadID, orgID, block, *attempt.AppliedAt)
					continue
				}
				if attempt.ClaimedAt != nil {
					claimTime = attempt.ClaimedAt.UTC()
				}
			}
			claimInserted = true
		}

		promotedAt, livePromoted, err := h.applyUploadBlockPromotion(orgID, uploadID, prepared.CommitID, claimTime, block)
		if err != nil {
			if claimInserted {
				h.releaseUploadPromotionClaims(orgID, uploadID, []uploadBlockPromotion{block})
			}
			if livePromoted {
				currentPromotedBlockIDs := append(append([]string(nil), newlyPromotedBlockIDs...), block.BlockSHA256)
				h.rollbackPromotedUploadBlocks(orgID, uploadID, prepared.CommitID, currentPromotedBlockIDs)
			} else {
				h.rollbackPromotedUploadBlocks(orgID, uploadID, prepared.CommitID, newlyPromotedBlockIDs)
			}
			h.releaseUploadPromotionClaims(orgID, uploadID, newlyClaimedBlocks)
			return fmt.Errorf("failed to write block metadata for block %d: %w", block.BlockIndex, err)
		}

		if h.uploadStaging != nil {
			newlyClaimedBlocks = append(newlyClaimedBlocks, block)
		}
		newlyPromotedBlockIDs = append(newlyPromotedBlockIDs, block.BlockSHA256)
		h.persistPromotedUploadBlock(uploadID, orgID, block, promotedAt)
	}

	if err := h.commitPublisher(orgID, repoID, prepared.CommitID, prepared.ExpectedHead); err != nil {
		if v2.IsLibraryHeadPublished(err) || v2.IsLibraryHeadAlreadyPublished(err, prepared.CommitID) {
			log.Printf("[HandleUpload] WARNING: Upload org=%s upload=%s published canonical head but secondary projections need repair: %v", orgID, shortLogID(uploadID), err)
			return nil
		}
		h.rollbackPromotedUploadBlocks(orgID, uploadID, prepared.CommitID, newlyPromotedBlockIDs)
		h.releaseUploadPromotionClaims(orgID, uploadID, newlyClaimedBlocks)
		return fmt.Errorf("failed to publish upload commit: %w", err)
	}

	return nil
}

func uploadPromotionOperationKey(orgID, uploadID, commitID string, blockIndex int, claimedAt time.Time) string {
	return fmt.Sprintf("upload-promote:%s:%s:%s:%d:%d", orgID, uploadID, commitID, blockIndex, claimedAt.UTC().UnixNano())
}

func (h *SeafHTTPHandler) applyUploadBlockPromotion(orgID, uploadID, commitID string, claimedAt time.Time, block uploadBlockPromotion) (time.Time, bool, error) {
	if h == nil {
		return time.Time{}, false, fmt.Errorf("upload handler is required")
	}
	if h.blockPromoter == nil {
		return time.Time{}, false, fmt.Errorf("block promoter is not configured")
	}
	if claimedAt.IsZero() {
		claimedAt = time.Now().UTC()
	}
	operationKey := uploadPromotionOperationKey(orgID, uploadID, commitID, block.BlockIndex, claimedAt)
	if err := h.blockPromoter(orgID, operationKey, block.BlockSHA256, block.SizeBytes, block.StorageClass, block.StorageKey); err != nil {
		return time.Time{}, false, err
	}
	promotedAt := time.Now().UTC()
	if h.uploadStaging != nil {
		if err := h.uploadStaging.MarkBlockPromotionApplied(orgID, uploadID, block.BlockIndex, promotedAt); err != nil {
			h.clearUploadS3Orphan(orgID, block.BlockSHA256)
			return promotedAt, true, err
		}
	}
	h.clearUploadS3Orphan(orgID, block.BlockSHA256)
	return promotedAt, true, nil
}

func (h *SeafHTTPHandler) preflushChunkUploadBlocks(c *gin.Context, token *AccessToken, upload *ChunkUpload, uploadID string) {
	if h == nil || upload == nil || token == nil || upload.TotalSize <= uploadBlockSize || h.uploadBlockResolver == nil || h.uploadBlockWriter == nil {
		return
	}

	var encrypted bool
	var fileKey, fileIV []byte
	if h.db != nil {
		if err := h.db.Session().Query(`
			SELECT encrypted FROM libraries WHERE org_id = ? AND library_id = ?
		`, token.OrgID, token.RepoID).Scan(&encrypted); err != nil {
			// Don't risk uploading plaintext for an encrypted library — bail out
			// and let finalize handle the blocks once we can read the flag.
			log.Printf("[HandleUpload] WARNING: Skipping preflush; failed to read library encryption flag org=%s repo=%s: %v", token.OrgID, token.RepoID, err)
			return
		}
	}
	if encrypted {
		fileKey, fileIV = v2.GetDecryptSessions().GetFileKeyAndIV(token.UserID, token.RepoID)
		if fileKey == nil {
			return
		}
	}

	blockStore, actualStorageClass, err := h.uploadBlockResolver(c, token)
	if err != nil {
		log.Printf("[HandleUpload] WARNING: Failed to resolve block store for chunk preflush: %v", err)
		return
	}

	for {
		blockIndex, start, end, ok := upload.NextFlushableContiguousBlock(uploadBlockSize)
		if !ok {
			return
		}

		err := func() error {
			defer upload.finishPreflush(blockIndex)

			plaintextBlock, err := upload.ReadRange(start, end)
			if err != nil {
				return fmt.Errorf("read contiguous chunk block %d for preflush: %w", blockIndex, err)
			}

			blockSHA1Hash := sha1.Sum(plaintextBlock)
			blockSHA1ID := hex.EncodeToString(blockSHA1Hash[:])
			storedBlock := plaintextBlock
			if fileKey != nil {
				encryptedBlock, encErr := crypto.EncryptBlockSeafile(plaintextBlock, fileKey, fileIV)
				if encErr != nil {
					return fmt.Errorf("encrypt preflush block %d: %w", blockIndex, encErr)
				}
				storedBlock = encryptedBlock
			}

			sha256Hash := sha256.Sum256(storedBlock)
			sha256ID := hex.EncodeToString(sha256Hash[:])
			promotion := uploadBlockPromotion{
				BlockIndex:   blockIndex,
				BlockSHA1:    blockSHA1ID,
				BlockSHA256:  sha256ID,
				SizeBytes:    len(storedBlock),
				StorageClass: actualStorageClass,
				StorageKey:   sha256ID,
				Source:       UploadBlockSourceNewObject,
				CreatedAt:    upload.CreatedAt.UTC(),
			}

			if err := upload.AccountBlockOnce(blockIndex, sha256ID, func() error {
				source, blockStorageClass, blockStorageKey, uploadedAt, err := h.materializeUploadBlock(c.Request.Context(), blockStore, token.OrgID, sha256ID, storedBlock, actualStorageClass)
				if err != nil {
					return err
				}
				promotion.Source = source
				promotion.StorageClass = blockStorageClass
				promotion.StorageKey = blockStorageKey
				promotion.UploadedAt = uploadedAt
				registeredAt := time.Now().UTC()
				if h.uploadStaging != nil {
					h.persistUploadBlockState(UploadSessionBlockRecord{
						UploadID:     uploadID,
						BlockIndex:   blockIndex,
						OrgID:        token.OrgID,
						BlockSHA1:    blockSHA1ID,
						BlockSHA256:  sha256ID,
						SizeBytes:    len(storedBlock),
						StorageClass: blockStorageClass,
						StorageKey:   blockStorageKey,
						Source:       source,
						State:        UploadBlockStateRegistered,
						CreatedAt:    upload.CreatedAt.UTC(),
						UpdatedAt:    registeredAt,
					}, string(UploadBlockStateRegistered))
				}
				if h.uploadStaging != nil && uploadedAt != nil {
					h.persistUploadBlockState(UploadSessionBlockRecord{
						UploadID:     uploadID,
						BlockIndex:   blockIndex,
						OrgID:        token.OrgID,
						BlockSHA1:    blockSHA1ID,
						BlockSHA256:  sha256ID,
						SizeBytes:    len(storedBlock),
						StorageClass: blockStorageClass,
						StorageKey:   blockStorageKey,
						Source:       source,
						State:        UploadBlockStatePreflushed,
						CreatedAt:    upload.CreatedAt.UTC(),
						UpdatedAt:    uploadedAt.UTC(),
						UploadedAt:   uploadedAt,
					}, string(UploadBlockStatePreflushed))
				}
				return nil
			}); err != nil {
				return err
			}

			upload.RecordPreflushedBlock(blockIndex, promotion)
			return nil
		}()
		if err != nil {
			log.Printf("[HandleUpload] WARNING: Failed to preflush chunk block %d: %v", blockIndex, err)
			return
		}
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

// UploadBlockSize exposes the live upload/promotion block size for integration tests.
const UploadBlockSize = uploadBlockSize

// finalizeUploadConcurrency caps the number of S3 PUTs running in parallel
// during finalization of a chunked upload. The reader is sequential (one block
// at a time from the temp file); only the per-block work (encrypt + S3 PUT +
// Cassandra writes) runs concurrently. 8 keeps memory bounded
// (≤ 8 × uploadBlockSize ≈ 64 MB extra) while cutting wall-clock by ~6–8× on
// typical S3 latency.
const finalizeUploadConcurrency = 8

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

	// Best-effort recovery for older promoting uploads in the same org. This is
	// conservative: it only republishes HEAD when every block promotion marker is
	// already applied, and otherwise leaves active sessions alone.
	if h.cleanupPendingSweep != nil {
		h.cleanupPendingSweep(token.OrgID, uploadRecoveryBatchLimit)
	}
	h.recoverPromotingUploadsForOrg(token.OrgID, uploadRecoveryBatchLimit)

	contentRange := c.GetHeader("Content-Range")
	start, end, total, isChunked := parseContentRange(contentRange)

	// Quota pre-check — evaluated before reading the body so we can fail fast.
	// For chunked uploads use the declared total from Content-Range; per-request
	// multipart size underestimates the eventual upload and allows quota bypass.
	uploadTrafficStatus := traffic.QuotaStatus{Allowed: true}
	if checker := traffic.GetChecker(); checker != nil {
		quotaBytes := c.Request.ContentLength
		if isChunked && total > 0 {
			quotaBytes = total
		}
		precheck, _ := traffic.CheckUploadQuotaWithChecker(checker, token.OrgID, token.UserID, quotaBytes)
		if !precheck.StorageStatus.Allowed {
			c.JSON(http.StatusForbidden, traffic.StorageQuotaExceededResponse(precheck.StorageStatus, "storage quota exceeded"))
			return
		}
		uploadTrafficStatus = precheck.TrafficStatus
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
	tokenParentDir := NormalizeUploadParentDir(token.Path)
	parentDir := c.DefaultPostForm("parent_dir", tokenParentDir)
	relativePath := c.PostForm("relative_path")
	replaceStr := c.DefaultPostForm("replace", "1")
	replaceFile := replaceStr != "0"
	retJSON := c.Query("ret-json") == "1" || c.PostForm("ret-json") == "1"

	filename := normalizeUploadFilename(header.Filename)
	if filename == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid filename"})
		return
	}

	// Handle relative_path for folder uploads (e.g., "my-folder/subfolder/file.txt")
	if relativePath != "" {
		relativePath = strings.ReplaceAll(relativePath, "\\", "/")
		if strings.HasSuffix(relativePath, "/") {
			dirName := strings.TrimSuffix(relativePath, "/")
			dirBaseName := path.Base(dirName)
			parentDir = path.Join(parentDir, dirName)
			parentDir = NormalizeUploadParentDir(parentDir)
			if !UploadParentDirWithinScope(tokenParentDir, parentDir) {
				c.JSON(http.StatusForbidden, gin.H{"error": "upload path escapes token scope"})
				return
			}

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
		} else {
			relDir := path.Dir(relativePath)
			if relDir != "." && relDir != "" {
				parentDir = path.Join(parentDir, relDir)
			}
			filename = normalizeUploadFilename(relativePath)
			if filename == "" {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid filename"})
				return
			}
		}
	}

	parentDir = NormalizeUploadParentDir(parentDir)
	if !UploadParentDirWithinScope(tokenParentDir, parentDir) {
		c.JSON(http.StatusForbidden, gin.H{"error": "upload path escapes token scope"})
		return
	}

	log.Printf("[HandleUpload] relativePath=%s, parentDir=%s, filename=%s", relativePath, parentDir, filename)

	filePath := path.Join(parentDir, filename)
	storageKey := fmt.Sprintf("%s/%s%s", token.OrgID, token.RepoID, filePath)

	log.Printf("[HandleUpload] Token=%s, File=%s, ContentRange=%s, isChunked=%v",
		tokenStr, filename, contentRange, isChunked)
	var existingUploadSession *UploadSessionRecord

	if isChunked {
		// Chunked upload: stream chunk data directly to temp file (no io.ReadAll)
		upload, created, err := chunkManager.GetOrCreateUpload(tokenStr, filename, parentDir, total)
		if err != nil {
			if errors.Is(err, errChunkUploadTotalSizeMismatch) {
				c.JSON(http.StatusBadRequest, gin.H{"error": "chunked upload total size mismatch"})
				return
			}
			log.Printf("[HandleUpload] Failed to create upload tracker: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to initialize upload"})
			return
		}
		uploadID := BuildUploadSessionID(token.OrgID, token.RepoID, token.UserID, tokenStr, upload.AttemptID, parentDir, filename)
		if h.uploadStaging != nil {
			existingUploadSession, err = h.uploadStaging.GetSession(token.OrgID, uploadID)
			if err != nil {
				log.Printf("[HandleUpload] WARNING: Failed to read existing upload session before overwrite: %v", err)
				existingUploadSession = nil
			}
		}
		if created && h.uploadStaging != nil && !shouldPreserveUploadSession(existingUploadSession) {
			now := time.Now().UTC()
			if err := h.uploadStaging.UpsertSession(UploadSessionRecord{
				UploadID:  uploadID,
				OrgID:     token.OrgID,
				RepoID:    token.RepoID,
				UserID:    token.UserID,
				TokenID:   tokenStr,
				ParentDir: parentDir,
				Filename:  filename,
				TotalSize: total,
				State:     UploadSessionStateReceiving,
				CreatedAt: upload.CreatedAt.UTC(),
				UpdatedAt: now,
				ExpiresAt: now.Add(chunkDiskTTL),
			}); err != nil {
				log.Printf("[HandleUpload] Failed to persist upload session: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to persist upload session"})
				return
			}
		}

		// Stream chunk directly to temp file at the correct offset
		if err := upload.WriteChunkFromReader(file, start, end); err != nil {
			log.Printf("[HandleUpload] Failed to write chunk: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to write chunk"})
			return
		}
		h.preflushChunkUploadBlocks(c, token, upload, uploadID)

		if !upload.TryStartFinalization() {
			log.Printf("[HandleUpload] Chunk received, waiting for more: %d/%d", end+1, total)
			c.JSON(http.StatusOK, gin.H{"success": true})
			return
		}

		// All chunks received — finalize by streaming from temp file
		log.Printf("[HandleUpload] All chunks received, finalizing upload (streaming)")
		upload.Touch()
		if h.uploadStaging != nil && !shouldPreserveUploadSession(existingUploadSession) {
			now := time.Now().UTC()
			if err := h.uploadStaging.UpsertSession(UploadSessionRecord{
				UploadID:  uploadID,
				OrgID:     token.OrgID,
				RepoID:    token.RepoID,
				UserID:    token.UserID,
				TokenID:   tokenStr,
				ParentDir: parentDir,
				Filename:  filename,
				TotalSize: total,
				State:     UploadSessionStatePromoting,
				CreatedAt: upload.CreatedAt.UTC(),
				UpdatedAt: now,
				ExpiresAt: now.Add(chunkDiskTTL),
			}); err != nil {
				log.Printf("[HandleUpload] Failed to persist upload state: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to persist upload state"})
				return
			}
		}
		result, err := h.chunkedFinalize(c, token, upload, uploadID, parentDir, filename, storageKey, total, replaceFile)
		if err != nil {
			upload.ResetFinalization()
			if h.uploadStaging != nil {
				now := time.Now().UTC()
				_ = h.uploadStaging.UpsertSession(UploadSessionRecord{
					UploadID:  uploadID,
					OrgID:     token.OrgID,
					RepoID:    token.RepoID,
					UserID:    token.UserID,
					TokenID:   tokenStr,
					ParentDir: parentDir,
					Filename:  filename,
					TotalSize: total,
					State:     UploadSessionStateCleanupPending,
					CreatedAt: upload.CreatedAt.UTC(),
					UpdatedAt: now,
					ExpiresAt: now.Add(chunkDiskTTL),
					LastError: err.Error(),
				})
			}
			log.Printf("[HandleUpload] Finalization failed: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to finalize upload"})
			return
		}
		if h.uploadStaging != nil {
			now := time.Now().UTC()
			_ = h.uploadStaging.UpsertSession(UploadSessionRecord{
				UploadID:       uploadID,
				OrgID:          token.OrgID,
				RepoID:         token.RepoID,
				UserID:         token.UserID,
				TokenID:        tokenStr,
				ParentDir:      parentDir,
				Filename:       filename,
				ActualFilename: result.ActualFilename,
				CommitID:       result.CommitID,
				TotalSize:      total,
				State:          UploadSessionStateClosed,
				CreatedAt:      upload.CreatedAt.UTC(),
				UpdatedAt:      now,
				ExpiresAt:      now.Add(uploadRetryWindow),
			})
		}
		chunkManager.CleanupUpload(tokenStr, filename)

		log.Printf("[HandleUpload] Upload complete: file=%s, size=%d, id=%s", result.ActualFilename, total, shortLogID(result.FileID))

		// Record traffic and storage only for fresh uploads, not for publish recovery.
		if !result.Recovered {
			if rec := traffic.Get(); rec != nil {
				tt := traffic.WebUpload
				if token.Source == "link" {
					tt = traffic.LinkUpload
				}
				traffic.RecordCheckedTransfer(rec, uploadTrafficStatus, token.OrgID, token.UserID, tt, total)
			}
			if h.db != nil {
				traffic.IncrementStorageCounters(h.db, token.OrgID, token.UserID, token.RepoID, total, 1)
			}
		}

		if retJSON {
			c.JSON(http.StatusOK, []gin.H{{"name": result.ActualFilename, "id": result.FileID, "size": strconv.FormatInt(total, 10)}})
		} else {
			c.String(http.StatusOK, result.FileID)
		}
		return
	}

	// Single-shot upload: for small files, use the simple path.
	// For large files (> uploadBlockSize), save to temp file first then stream.
	uploadID := BuildUploadSessionID(token.OrgID, token.RepoID, token.UserID, tokenStr, newUploadAttemptID(), parentDir, filename)
	if h.uploadStaging != nil {
		existingUploadSession, err = h.uploadStaging.GetSession(token.OrgID, uploadID)
		if err != nil {
			log.Printf("[HandleUpload] WARNING: Failed to read existing upload session before overwrite: %v", err)
			existingUploadSession = nil
		}
	}
	chunkData, err := io.ReadAll(file)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to read file"})
		return
	}
	finalSize := int64(len(chunkData))
	singleShotCreatedAt := time.Now().UTC()
	markSingleShotCleanup := func(cause error) {
		if h.uploadStaging == nil || cause == nil {
			return
		}
		now := time.Now().UTC()
		_ = h.uploadStaging.UpsertSession(UploadSessionRecord{
			UploadID:  uploadID,
			OrgID:     token.OrgID,
			RepoID:    token.RepoID,
			UserID:    token.UserID,
			TokenID:   tokenStr,
			ParentDir: parentDir,
			Filename:  filename,
			TotalSize: finalSize,
			State:     UploadSessionStateCleanupPending,
			CreatedAt: singleShotCreatedAt,
			UpdatedAt: now,
			ExpiresAt: now.Add(chunkDiskTTL),
			LastError: cause.Error(),
		})
	}
	if h.uploadStaging != nil {
		if !shouldPreserveUploadSession(existingUploadSession) {
			if err := h.uploadStaging.UpsertSession(UploadSessionRecord{
				UploadID:  uploadID,
				OrgID:     token.OrgID,
				RepoID:    token.RepoID,
				UserID:    token.UserID,
				TokenID:   tokenStr,
				ParentDir: parentDir,
				Filename:  filename,
				TotalSize: finalSize,
				State:     UploadSessionStateReceiving,
				CreatedAt: singleShotCreatedAt,
				UpdatedAt: singleShotCreatedAt,
				ExpiresAt: singleShotCreatedAt.Add(chunkDiskTTL),
			}); err != nil {
				log.Printf("[HandleUpload] Failed to persist upload session: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to persist upload session"})
				return
			}
		}
	}

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
			markSingleShotCleanup(fmt.Errorf("library is encrypted and not unlocked"))
			c.JSON(http.StatusForbidden, gin.H{"error": "library is encrypted and not unlocked"})
			return
		}
		encryptedContent, err := crypto.EncryptBlockSeafile(chunkData, fileKey, fileIV)
		if err != nil {
			markSingleShotCleanup(err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to encrypt content"})
			return
		}
		storedContent = encryptedContent
	}

	sha256Hash := sha256.Sum256(storedContent)
	sha256ID := hex.EncodeToString(sha256Hash[:])
	storedInBlockStore := false
	blockSource := UploadBlockSourceNewObject
	blockStorageClass := ""
	blockStorageKey := sha256ID
	var singleShotUploadedAt *time.Time
	if h.uploadStaging != nil {
		registeredAt := time.Now().UTC()
		h.persistUploadBlockState(UploadSessionBlockRecord{
			UploadID:     uploadID,
			BlockIndex:   0,
			OrgID:        token.OrgID,
			BlockSHA1:    fileID,
			BlockSHA256:  sha256ID,
			SizeBytes:    len(storedContent),
			StorageClass: "",
			StorageKey:   sha256ID,
			Source:       UploadBlockSourceNewObject,
			State:        UploadBlockStateRegistered,
			CreatedAt:    singleShotCreatedAt,
			UpdatedAt:    registeredAt,
		}, string(UploadBlockStateRegistered))
	}

	// Store using PutAuto (automatically uses multipart for large files)
	ctx := context.Background()
	blockStore, actualStorageClass, err := h.resolveLibraryBlockStore(httputil.GetRoutingHostname(c, h.configuredServerURL()), token.OrgID, token.RepoID)
	if err != nil {
		log.Printf("[HandleUpload] Failed to get block store: %v, falling back to S3", err)
		objectStore, _, resolveErr := h.resolveLibraryObjectStore(httputil.GetRoutingHostname(c, h.configuredServerURL()), token.OrgID, token.RepoID)
		if resolveErr != nil {
			markSingleShotCleanup(resolveErr)
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "block storage not available"})
			return
		}
		_, err = objectStore.Put(c.Request.Context(), storageKey, newBytesReader(storedContent), int64(len(storedContent)))
		if err != nil {
			markSingleShotCleanup(err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to upload file"})
			return
		}
	} else {
		blockSource, blockStorageClass, blockStorageKey, singleShotUploadedAt, err = h.materializeUploadBlock(ctx, blockStore, token.OrgID, sha256ID, storedContent, actualStorageClass)
		if err != nil {
			markSingleShotCleanup(err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to store block"})
			return
		}
		storedInBlockStore = singleShotUploadedAt != nil
		if storedInBlockStore {
			log.Printf("[HandleUpload] Stored block %s (SHA-256: %s)", fileID[:16], sha256ID[:16])
		}
	}
	if h.uploadStaging != nil && singleShotUploadedAt != nil {
		h.persistUploadBlockState(UploadSessionBlockRecord{
			UploadID:     uploadID,
			BlockIndex:   0,
			OrgID:        token.OrgID,
			BlockSHA1:    fileID,
			BlockSHA256:  sha256ID,
			SizeBytes:    len(storedContent),
			StorageClass: blockStorageClass,
			StorageKey:   blockStorageKey,
			Source:       blockSource,
			State:        UploadBlockStateUploaded,
			CreatedAt:    singleShotCreatedAt,
			UpdatedAt:    singleShotUploadedAt.UTC(),
			UploadedAt:   singleShotUploadedAt,
		}, string(UploadBlockStateUploaded))
	}

	// Create SHA-1 → SHA-256 mapping (dual-write: forward + reverse lookup)
	if err := h.db.Session().Query(`
		INSERT INTO block_id_mappings (org_id, external_id, internal_id) VALUES (?, ?, ?)
	`, token.OrgID, fileID, sha256ID).Exec(); err != nil {
		log.Printf("[HandleUpload] CRITICAL: Failed to write block_id_mapping org=%s ext=%s int=%s: %v", token.OrgID, fileID[:16], sha256ID[:16], err)
		if storedInBlockStore {
			h.persistUploadS3Orphan(token.OrgID, sha256ID, blockStorageClass, err.Error())
		}
		markSingleShotCleanup(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create block mapping"})
		return
	}
	if err := h.db.Session().Query(`
		INSERT INTO block_id_mappings_by_internal (org_id, internal_id, external_id, created_at) VALUES (?, ?, ?, toTimestamp(now()))
	`, token.OrgID, sha256ID, fileID).Exec(); err != nil {
		log.Printf("[HandleUpload] WARNING: Failed to write reverse block_id_mapping org=%s int=%s ext=%s: %v", token.OrgID, sha256ID[:16], fileID[:16], err)
	}
	preparedCommit, recovered, err := h.recoverPreparedUploadIfPossible(token.OrgID, token.RepoID, uploadID, []uploadBlockPromotion{{
		BlockIndex:  0,
		BlockSHA1:   fileID,
		BlockSHA256: sha256ID,
	}})
	if err != nil {
		log.Printf("[HandleUpload] Failed to recover prepared upload: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to recover upload state"})
		return
	} else if recovered {
		actualFilename := preparedCommit.ActualFilename
		log.Printf("[HandleUpload] Recovered prepared upload: file=%s, commit=%s, id=%s", actualFilename, preparedCommit.CommitID, shortLogID(fileID))
		if retJSON {
			c.JSON(http.StatusOK, []gin.H{{"name": actualFilename, "id": fileID, "size": strconv.FormatInt(finalSize, 10)}})
		} else {
			c.String(http.StatusOK, fileID)
		}
		return
	}

	// Prepare filesystem metadata without publishing the new head yet.
	commitID := preparedCommit.CommitID
	actualFilename := preparedCommit.ActualFilename
	expectedHead := preparedCommit.ExpectedHead
	if strings.TrimSpace(commitID) == "" {
		commitID, actualFilename, expectedHead, err = h.prepareUploadedFileCommit(token.OrgID, token.RepoID, token.UserID, parentDir, filename, fileID, chunkData, finalSize, replaceFile)
		if err != nil {
			log.Printf("[HandleUpload] Failed to update filesystem: %v", err)
			markSingleShotCleanup(err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "file stored but metadata update failed"})
			return
		}
	}
	if h.uploadStaging != nil {
		now := time.Now().UTC()
		if err := h.uploadStaging.UpsertSession(UploadSessionRecord{
			UploadID:       uploadID,
			OrgID:          token.OrgID,
			RepoID:         token.RepoID,
			UserID:         token.UserID,
			TokenID:        tokenStr,
			ParentDir:      parentDir,
			Filename:       filename,
			ActualFilename: actualFilename,
			CommitID:       commitID,
			TotalSize:      finalSize,
			State:          UploadSessionStatePromoting,
			CreatedAt:      singleShotCreatedAt,
			UpdatedAt:      now,
			ExpiresAt:      now.Add(chunkDiskTTL),
		}); err != nil {
			log.Printf("[HandleUpload] Failed to persist prepared upload state: %v", err)
			markSingleShotCleanup(err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to persist upload state"})
			return
		}
	}
	if err := h.publishPreparedUpload(token.OrgID, token.RepoID, uploadID, preparedUploadCommit{
		CommitID:       commitID,
		ActualFilename: actualFilename,
		ExpectedHead:   expectedHead,
	}, []uploadBlockPromotion{{
		BlockIndex:   0,
		BlockSHA1:    fileID,
		BlockSHA256:  sha256ID,
		SizeBytes:    len(storedContent),
		StorageClass: blockStorageClass,
		StorageKey:   blockStorageKey,
		Source:       blockSource,
		CreatedAt:    singleShotCreatedAt,
		UploadedAt:   singleShotUploadedAt,
	}}); err != nil {
		log.Printf("[HandleUpload] Failed to publish filesystem update: %v", err)
		markSingleShotCleanup(err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "file stored but metadata update failed"})
		return
	}
	log.Printf("[HandleUpload] Filesystem updated, commit=%s", commitID)
	if h.uploadStaging != nil {
		now := time.Now().UTC()
		_ = h.uploadStaging.UpsertSession(UploadSessionRecord{
			UploadID:       uploadID,
			OrgID:          token.OrgID,
			RepoID:         token.RepoID,
			UserID:         token.UserID,
			TokenID:        tokenStr,
			ParentDir:      parentDir,
			Filename:       filename,
			ActualFilename: actualFilename,
			CommitID:       commitID,
			TotalSize:      finalSize,
			State:          UploadSessionStateClosed,
			CreatedAt:      singleShotCreatedAt,
			UpdatedAt:      now,
			ExpiresAt:      now.Add(uploadRetryWindow),
		})
	}

	log.Printf("[HandleUpload] Upload complete: file=%s, size=%d, id=%s", actualFilename, finalSize, shortLogID(fileID))

	// Record traffic and storage — fire-and-forget, never blocks the response.
	if rec := traffic.Get(); rec != nil {
		tt := traffic.WebUpload
		if token.Source == "link" {
			tt = traffic.LinkUpload
		}
		traffic.RecordCheckedTransfer(rec, uploadTrafficStatus, token.OrgID, token.UserID, tt, finalSize)
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
func (h *SeafHTTPHandler) finalizeUploadStreaming(c *gin.Context, token *AccessToken, upload *ChunkUpload, uploadID, parentDir, filename, storageKey string, totalSize int64, replace bool) (finalizeUploadResult, error) {
	ctx := context.Background()

	// Get the temp file reader
	reader, err := upload.GetReader()
	if err != nil {
		return finalizeUploadResult{}, fmt.Errorf("failed to get upload reader: %w", err)
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
			return finalizeUploadResult{}, fmt.Errorf("library is encrypted but not unlocked")
		}
	}

	blockStore, actualStorageClass, err := h.resolveLibraryBlockStore(httputil.GetRoutingHostname(c, h.configuredServerURL()), token.OrgID, token.RepoID)
	if err != nil {
		return finalizeUploadResult{}, fmt.Errorf("block store not available: %w", err)
	}

	// Stream the temp file sequentially (one block at a time) but submit per-block
	// work (encrypt + S3 PUT + Cassandra writes) to a bounded worker pool. The reader
	// stays single-threaded so we don't need to seek; what we parallelise is the
	// network/IO-bound part that dominates wall-clock time.
	sha1Hasher := sha1.New()

	eg, egCtx := errgroup.WithContext(ctx)
	sem := make(chan struct{}, finalizeUploadConcurrency)

	var blockSHA1IDs []string // SHA-1 block IDs for fs_object (Seafile compat) — populated in order
	var pendingPromotions []uploadBlockPromotion
	var pendingPromotionsMu sync.Mutex

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
				return finalizeUploadResult{}, fmt.Errorf("read error: %w", readErr)
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
			blockStorageClass := actualStorageClass
			if fileKey != nil {
				enc, encErr := crypto.EncryptBlockSeafile(blockDataLocal, fileKey, fileIV)
				if encErr != nil {
					return fmt.Errorf("failed to encrypt block: %w", encErr)
				}
				storedBlock = enc
			}

			sha256Hash := sha256.Sum256(storedBlock)
			sha256ID := hex.EncodeToString(sha256Hash[:])
			var uploadedAt *time.Time
			blockSource := UploadBlockSourceNewObject
			blockStorageKey := sha256ID
			preflushedBlock, alreadyAccounted, resolveErr := upload.ResolvePreflushedBlock(blockIndexLocal, sha256ID, blockSHA1IDLocal)
			if resolveErr != nil {
				return resolveErr
			}
			if alreadyAccounted {
				uploadedAt = preflushedBlock.UploadedAt
				blockSource = preflushedBlock.Source
				if preflushedBlock.StorageClass != "" {
					blockStorageClass = preflushedBlock.StorageClass
				}
				if preflushedBlock.StorageKey != "" {
					blockStorageKey = preflushedBlock.StorageKey
				}
			} else if h.uploadStaging != nil {
				registeredAt := time.Now().UTC()
				h.persistUploadBlockState(UploadSessionBlockRecord{
					UploadID:     uploadID,
					BlockIndex:   blockIndexLocal,
					OrgID:        token.OrgID,
					BlockSHA1:    blockSHA1IDLocal,
					BlockSHA256:  sha256ID,
					SizeBytes:    len(storedBlock),
					StorageClass: blockStorageClass,
					StorageKey:   blockStorageKey,
					Source:       blockSource,
					State:        UploadBlockStateRegistered,
					CreatedAt:    upload.CreatedAt.UTC(),
					UpdatedAt:    registeredAt,
				}, string(UploadBlockStateRegistered))
			}

			if !alreadyAccounted {
				materializedSource, materializedClass, materializedKey, materializedAt, materializeErr := h.materializeUploadBlock(egCtx, blockStore, token.OrgID, sha256ID, storedBlock, blockStorageClass)
				if materializeErr != nil {
					return fmt.Errorf("failed to store block: %w", materializeErr)
				}
				blockSource = materializedSource
				blockStorageClass = materializedClass
				blockStorageKey = materializedKey
				uploadedAt = materializedAt
			}
			if h.uploadStaging != nil && !alreadyAccounted && uploadedAt != nil {
				h.persistUploadBlockState(UploadSessionBlockRecord{
					UploadID:     uploadID,
					BlockIndex:   blockIndexLocal,
					OrgID:        token.OrgID,
					BlockSHA1:    blockSHA1IDLocal,
					BlockSHA256:  sha256ID,
					SizeBytes:    len(storedBlock),
					StorageClass: blockStorageClass,
					StorageKey:   blockStorageKey,
					Source:       blockSource,
					State:        UploadBlockStateUploaded,
					CreatedAt:    upload.CreatedAt.UTC(),
					UpdatedAt:    uploadedAt.UTC(),
					UploadedAt:   uploadedAt,
				}, string(UploadBlockStateUploaded))
			}

			// Forward mapping is on the critical read path — its failure aborts.
			if mapErr := h.db.Session().Query(`
				INSERT INTO block_id_mappings (org_id, external_id, internal_id) VALUES (?, ?, ?)
			`, token.OrgID, blockSHA1IDLocal, sha256ID).Exec(); mapErr != nil {
				log.Printf("[finalizeUploadStreaming] CRITICAL: Failed to write block_id_mapping org=%s ext=%s int=%s: %v", token.OrgID, blockSHA1IDLocal[:16], sha256ID[:16], mapErr)
				if uploadedAt != nil && blockSource == UploadBlockSourceNewObject {
					h.persistUploadS3Orphan(token.OrgID, sha256ID, blockStorageClass, mapErr.Error())
				}
				return fmt.Errorf("failed to create block mapping: %w", mapErr)
			}

			// Reverse mapping and block metadata are best-effort (matches the
			// previous serial behaviour: log on failure, don't abort).
			if revErr := h.db.Session().Query(`
				INSERT INTO block_id_mappings_by_internal (org_id, internal_id, external_id, created_at) VALUES (?, ?, ?, toTimestamp(now()))
			`, token.OrgID, sha256ID, blockSHA1IDLocal).Exec(); revErr != nil {
				log.Printf("[finalizeUploadStreaming] WARNING: Failed to write reverse block_id_mapping org=%s int=%s ext=%s: %v", token.OrgID, sha256ID[:16], blockSHA1IDLocal[:16], revErr)
			}

			pendingPromotion := uploadBlockPromotion{
				BlockIndex:   blockIndexLocal,
				BlockSHA1:    blockSHA1IDLocal,
				BlockSHA256:  sha256ID,
				SizeBytes:    len(storedBlock),
				StorageClass: blockStorageClass,
				StorageKey:   blockStorageKey,
				Source:       blockSource,
				CreatedAt:    upload.CreatedAt.UTC(),
				UploadedAt:   uploadedAt,
			}
			pendingPromotionsMu.Lock()
			pendingPromotions = append(pendingPromotions, pendingPromotion)
			pendingPromotionsMu.Unlock()

			upload.Touch()
			return nil
		})

		if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
			break
		}
	}

	if err := eg.Wait(); err != nil {
		return finalizeUploadResult{}, err
	}
	pendingPromotionsMu.Lock()
	sort.Slice(pendingPromotions, func(i, j int) bool {
		return pendingPromotions[i].BlockIndex < pendingPromotions[j].BlockIndex
	})
	pendingPromotionsMu.Unlock()

	// File ID = SHA-1 of the complete plaintext
	fileID := hex.EncodeToString(sha1Hasher.Sum(nil))

	log.Printf("[finalizeUploadStreaming] Stored %d blocks for file %s (size=%d, parallelism=%d)", len(blockSHA1IDs), fileID[:16], totalSize, finalizeUploadConcurrency)
	preparedCommit, recovered, err := h.recoverPreparedUploadIfPossible(token.OrgID, token.RepoID, uploadID, pendingPromotions)
	if err != nil {
		return finalizeUploadResult{}, fmt.Errorf("failed to recover prepared upload: %w", err)
	} else if recovered {
		log.Printf("[finalizeUploadStreaming] Recovered prepared upload commit=%s for file %s", preparedCommit.CommitID, shortLogID(fileID))
		return finalizeUploadResult{FileID: fileID, ActualFilename: preparedCommit.ActualFilename, CommitID: preparedCommit.CommitID, Recovered: true}, nil
	}

	// Prepare filesystem metadata with multiple block IDs without publishing head yet.
	commitID := preparedCommit.CommitID
	actualFilename := preparedCommit.ActualFilename
	expectedHead := preparedCommit.ExpectedHead
	if strings.TrimSpace(commitID) == "" {
		commitID, actualFilename, expectedHead, err = h.prepareUploadedFileCommitMultiBlock(token.OrgID, token.RepoID, token.UserID, parentDir, filename, fileID, blockSHA1IDs, totalSize, replace)
		if err != nil {
			return finalizeUploadResult{}, fmt.Errorf("failed to update filesystem metadata: %w", err)
		}
	}
	if h.uploadStaging != nil {
		now := time.Now().UTC()
		if err := h.uploadStaging.UpsertSession(UploadSessionRecord{
			UploadID:       uploadID,
			OrgID:          token.OrgID,
			RepoID:         token.RepoID,
			UserID:         token.UserID,
			TokenID:        token.Token,
			ParentDir:      parentDir,
			Filename:       filename,
			ActualFilename: actualFilename,
			CommitID:       commitID,
			TotalSize:      totalSize,
			State:          UploadSessionStatePromoting,
			CreatedAt:      upload.CreatedAt.UTC(),
			UpdatedAt:      now,
			ExpiresAt:      now.Add(chunkDiskTTL),
		}); err != nil {
			return finalizeUploadResult{}, fmt.Errorf("failed to persist prepared upload state: %w", err)
		}
	}
	if err := h.publishPreparedUpload(token.OrgID, token.RepoID, uploadID, preparedUploadCommit{
		CommitID:       commitID,
		ActualFilename: actualFilename,
		ExpectedHead:   expectedHead,
	}, pendingPromotions); err != nil {
		return finalizeUploadResult{}, fmt.Errorf("failed to publish filesystem metadata: %w", err)
	}
	log.Printf("[finalizeUploadStreaming] Filesystem updated, commit=%s", commitID)

	return finalizeUploadResult{FileID: fileID, ActualFilename: actualFilename, CommitID: commitID}, nil
}

// prepareUploadedFileCommitMultiBlock writes fs_objects and a new commit for a
// multi-block upload but leaves HEAD publication to the caller.
func (h *SeafHTTPHandler) prepareUploadedFileCommitMultiBlock(orgID, repoID, userID, parentDir, filename, fileID string, blockIDs []string, fileSize int64, replace bool) (string, string, string, error) {
	var headCommitID string
	err := h.db.Session().Query(`
		SELECT head_commit_id FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(&headCommitID)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to get head commit: %w", err)
	}

	var rootFSID string
	err = h.db.Session().Query(`
		SELECT root_fs_id FROM commits WHERE library_id = ? AND commit_id = ?
	`, repoID, headCommitID).Scan(&rootFSID)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to get root fs_id: %w", err)
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
		return "", "", "", fmt.Errorf("failed to marshal fs content: %w", err)
	}
	fsHash := sha1.Sum(fsContentJSON)
	fileFSID := hex.EncodeToString(fsHash[:])

	// Add file to directory (may auto-rename if replace=false and file exists)
	newRootFSID, actualFilename, err := h.addFileToDirectory(repoID, rootFSID, parentDir, filename, fileFSID, fileSize, userID, replace)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to add file to directory: %w", err)
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
		return "", "", "", fmt.Errorf("failed to create file fs_object: %w", err)
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
		return "", "", "", fmt.Errorf("failed to create commit: %w", err)
	}

	log.Printf("[prepareUploadedFileCommitMultiBlock] Prepared commit %s with root %s", newCommitID, newRootFSID)
	return newCommitID, actualFilename, headCommitID, nil
}

// prepareUploadedFileCommit writes fs_objects and a new commit for an upload but
// leaves HEAD publication to the caller.
func (h *SeafHTTPHandler) prepareUploadedFileCommit(orgID, repoID, userID, parentDir, filename, fileID string, content []byte, fileSize int64, replace bool) (string, string, string, error) {
	// Get current head commit
	var headCommitID string
	err := h.db.Session().Query(`
		SELECT head_commit_id FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(&headCommitID)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to get head commit: %w", err)
	}

	// Get root fs_id from head commit
	var rootFSID string
	err = h.db.Session().Query(`
		SELECT root_fs_id FROM commits WHERE library_id = ? AND commit_id = ?
	`, repoID, headCommitID).Scan(&rootFSID)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to get root fs_id: %w", err)
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
		return "", "", "", fmt.Errorf("failed to marshal fs content: %w", err)
	}
	fsHash := sha1.Sum(fsContentJSON)
	fileFSID := hex.EncodeToString(fsHash[:])

	log.Printf("[commitUploadedFile] File fs_id computed: %s (from JSON: %s)", fileFSID, string(fsContentJSON))

	// Navigate to parent directory and add file (may auto-rename if replace=false)
	newRootFSID, actualFilename, err := h.addFileToDirectory(repoID, rootFSID, parentDir, filename, fileFSID, fileSize, userID, replace)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to add file to directory: %w", err)
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
		return "", "", "", fmt.Errorf("failed to create file fs_object: %w", err)
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
		return "", "", "", fmt.Errorf("failed to create commit: %w", err)
	}

	log.Printf("[prepareUploadedFileCommit] Prepared commit %s with root %s", newCommitID, newRootFSID)
	return newCommitID, actualFilename, headCommitID, nil
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
func (h *SeafHTTPHandler) lookupFileBlocks(hostname string, token *AccessToken) (blockIDs []string, fileSize int64, fileKey []byte, blockStore *storage.BlockStore, err error) {
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

	blockStore, _, err = h.resolveLibraryBlockStore(hostname, token.OrgID, token.RepoID)
	if err != nil {
		return nil, 0, nil, nil, fmt.Errorf("block store not available: %w", err)
	}

	return blockIDs, fileSize, fileKey, blockStore, nil
}

// streamFileFromBlocks streams a file's blocks directly to the HTTP response.
// Uses prefetching (overlap S3 fetch with HTTP write) and 4MB io.CopyBuffer
// for maximum throughput. Only O(2 × block_size) RAM.
func (h *SeafHTTPHandler) streamFileFromBlocks(c *gin.Context, token *AccessToken, filename string, periodStartedAt time.Time) error {
	blockIDs, fileSize, fileKey, blockStore, err := h.lookupFileBlocks(httputil.GetRoutingHostname(c, h.configuredServerURL()), token)
	if err != nil {
		return err
	}

	log.Printf("[streamFileFromBlocks] Streaming %d blocks, size=%d, encrypted=%v", len(blockIDs), fileSize, fileKey != nil)

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
		rec.RecordWithPeriod(token.OrgID, token.UserID, tt, fileSize, periodStartedAt)
	}

	return nil
}

// getFileFromBlocks retrieves a file by loading all blocks into memory.
// DEPRECATED: Use streamFileFromBlocks for downloads. This is kept only for
// upload metadata (commitUploadedFile) where the full content is already in memory.
func (h *SeafHTTPHandler) getFileFromBlocks(c *gin.Context, token *AccessToken) ([]byte, error) {
	blockIDs, _, fileKey, blockStore, err := h.lookupFileBlocks(httputil.GetRoutingHostname(c, h.configuredServerURL()), token)
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

	preflightBudget := h.newZipTraversalBudget()
	if err := h.validateZipDirectory(token.RepoID, targetFSID, 0, preflightBudget); err != nil {
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

	// Recursively add directory contents to the ZIP
	runtimeBudget := h.newZipTraversalBudget()
	if err := h.addDirToZip(c.Request.Context(), httputil.GetRoutingHostname(c, h.configuredServerURL()), zipWriter, token.RepoID, token.OrgID, targetFSID, "", fileKey, 0, runtimeBudget); err != nil {
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

func (h *SeafHTTPHandler) validateZipDirectory(repoID, dirFSID string, depth int, budget *zipTraversalBudget) error {
	if budget == nil {
		return nil
	}
	if err := budget.noteDirectory(depth); err != nil {
		return err
	}

	var dirEntriesJSON string
	err := h.db.Session().Query(`
		SELECT dir_entries FROM fs_objects WHERE library_id = ? AND fs_id = ?
	`, repoID, dirFSID).Scan(&dirEntriesJSON)
	if err != nil || dirEntriesJSON == "" || dirEntriesJSON == "[]" {
		return err
	}

	var entries []map[string]interface{}
	if err := json.Unmarshal([]byte(dirEntriesJSON), &entries); err != nil {
		return fmt.Errorf("parse dir entries: %w", err)
	}

	for _, entry := range entries {
		id, _ := entry["id"].(string)
		if id == "" {
			continue
		}
		modeFloat, _ := entry["mode"].(float64)
		mode := int(modeFloat)
		if mode == 16384 || mode&0170000 == 040000 {
			if err := h.validateZipDirectory(repoID, id, depth+1, budget); err != nil {
				return err
			}
			continue
		}

		var fileSize int64
		if err := h.db.Session().Query(`
			SELECT size_bytes FROM fs_objects WHERE library_id = ? AND fs_id = ?
		`, repoID, id).Scan(&fileSize); err != nil {
			return fmt.Errorf("load zip file metadata: %w", err)
		}
		if err := budget.noteFile(fileSize); err != nil {
			return err
		}
	}

	return nil
}

// addDirToZip recursively adds directory contents to a ZIP archive
func (h *SeafHTTPHandler) addDirToZip(ctx context.Context, hostname string, zw *zip.Writer, repoID, orgID, dirFSID, prefix string, fileKey []byte, depth int, budget *zipTraversalBudget) error {
	if budget != nil {
		if err := budget.noteDirectory(depth); err != nil {
			return err
		}
	}

	var dirEntriesJSON string
	err := h.db.Session().Query(`
		SELECT dir_entries FROM fs_objects WHERE library_id = ? AND fs_id = ?
	`, repoID, dirFSID).Scan(&dirEntriesJSON)
	if err != nil || dirEntriesJSON == "" || dirEntriesJSON == "[]" {
		return err
	}

	var entries []map[string]interface{}
	if err := json.Unmarshal([]byte(dirEntriesJSON), &entries); err != nil {
		return fmt.Errorf("parse dir entries: %w", err)
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
			if err := h.addDirToZip(ctx, hostname, zw, repoID, orgID, id, entryPath, fileKey, depth+1, budget); err != nil {
				return err
			}
		} else { // File
			if err := h.addFileToZip(ctx, hostname, zw, repoID, orgID, id, entryPath, fileKey, budget); err != nil {
				return err
			}
		}
	}

	return nil
}

// addFileToZip streams a file's blocks into a ZIP archive entry.
// Uses zip.Store (no compression) for maximum throughput — the data is already
// compressed by S3/MinIO or is binary data where deflate adds CPU cost for minimal gain.
// For encrypted files, one block at a time is loaded, decrypted, and written.
func (h *SeafHTTPHandler) addFileToZip(ctx context.Context, hostname string, zw *zip.Writer, repoID, orgID, fileFSID, zipPath string, fileKey []byte, budget *zipTraversalBudget) error {
	var blockIDs []string
	var fileSize int64
	err := h.db.Session().Query(`
		SELECT block_ids, size_bytes FROM fs_objects WHERE library_id = ? AND fs_id = ?
	`, repoID, fileFSID).Scan(&blockIDs, &fileSize)
	if err != nil {
		return fmt.Errorf("load blocks for %s: %w", zipPath, err)
	}
	if budget != nil {
		if err := budget.noteFile(fileSize); err != nil {
			return err
		}
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
		return fmt.Errorf("create zip entry %s: %w", zipPath, err)
	}

	blockStore, _, err := h.resolveLibraryBlockStore(hostname, orgID, repoID)
	if err != nil {
		return fmt.Errorf("block store not available: %w", err)
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
				return fmt.Errorf("get block %s for %s: %w", blockIDs[i], zipPath, err)
			}
			decrypted, err := crypto.DecryptBlock(blockData, fileKey)
			if err != nil {
				return fmt.Errorf("decrypt block for %s: %w", zipPath, err)
			}
			if _, err := w.Write(decrypted); err != nil {
				return fmt.Errorf("write decrypted block for %s: %w", zipPath, err)
			}
		} else {
			// Unencrypted: stream directly from S3 → ZIP writer with 4MB buffer
			reader, err := blockStore.GetBlockReader(ctx, internalID)
			if err != nil {
				return fmt.Errorf("get block reader %s for %s: %w", blockIDs[i], zipPath, err)
			}
			_, err = io.CopyBuffer(w, reader, buf)
			reader.Close()
			if err != nil {
				return fmt.Errorf("stream block %s for %s: %w", blockIDs[i], zipPath, err)
			}
		}
	}

	return nil
}
