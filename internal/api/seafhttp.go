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
	"github.com/Sesame-Disk/sesamefs/internal/downloadadmission"
	"github.com/Sesame-Disk/sesamefs/internal/httputil"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/Sesame-Disk/sesamefs/internal/streaming"
	"github.com/Sesame-Disk/sesamefs/internal/traffic"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"
)

var checkSeafHTTPFileLockedByOther = func(h *SeafHTTPHandler, repoID, filePath, userID string) (bool, string, error) {
	if h == nil || h.db == nil {
		return false, "", nil
	}
	normalizedPath := path.Clean("/" + strings.TrimPrefix(filePath, "/"))
	return db.FileLockedByOther(h.db.Session(), repoID, normalizedPath, userID)
}

// TokenType represents the type of access token
type TokenType string

const (
	TokenTypeUpload       TokenType = "upload"
	TokenTypeDownload     TokenType = "download"
	TokenTypeOneTimeLogin TokenType = "onetime_login"
	// TokenTypeSync is the repository sync credential the desktop client gets
	// from download-info. It is a distinct type rather than a download token of
	// a particular shape so that a download bearer cannot authenticate the sync
	// surface by construction — see ISSUE-SYNC-LINK-TOKEN-AUTH-01, where a
	// public share-link download token did exactly that. GetToken compares the
	// stored type exactly, so the separation is enforced at the store.
	TokenTypeSync TokenType = "sync"
)

// AccessToken represents a temporary access token for file operations
type AccessToken struct {
	Token   string
	Type    TokenType
	OrgID   string
	RepoID  string
	Path    string // File path for downloads, parent dir for uploads
	Replace bool   // Default overwrite behavior for upload tokens
	UserID  string
	// Source is "" for a regular user token and "link" for a share/upload link.
	// Those are the only two values ever written. Do not add a third without
	// checking isRepositorySyncToken: sync authentication allowlists Source ==
	// "" exactly, so a new value is refused there until it is admitted on
	// purpose. (An earlier version of this comment listed "web" as an
	// equivalent regular-user value; nothing has ever minted it.)
	Source    string
	SourceID  string // Stable non-secret identity for the originating public link
	AuthToken string // User's auth token (for one-time login tokens)
	ExpiresAt time.Time
	CreatedAt time.Time
}

// TokenStore is the interface for token operations (can be in-memory or Cassandra-backed)
type TokenStore interface {
	CreateUploadToken(orgID, repoID, path, userID string) (string, error)
	CreateUpdateToken(orgID, repoID, path, userID string) (string, error)
	CreateDownloadToken(orgID, repoID, path, userID string) (string, error)
	CreateSyncToken(orgID, repoID, userID string) (string, error)
	CreateLinkUploadToken(orgID, repoID, path, userID, sourceID string) (string, error)
	CreateLinkDownloadToken(orgID, repoID, path, userID, sourceID string) (string, error)
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

// CreateToken creates a new access token.
//
// It refuses TokenTypeSync. The generic constructor takes a path, and a sync
// credential's root path is meant to be a property of its constructor rather
// than a value a caller supplies — see CreateSyncToken. Without this guard
// "CreateSyncToken is the only way to mint one" would be a convention rather
// than a fact, and the whole point of the separate type is that it cannot be
// produced by accident.
func (tm *TokenManager) CreateToken(tokenType TokenType, orgID, repoID, path, userID, source string, ttl time.Duration) (*AccessToken, error) {
	if tokenType == TokenTypeSync {
		return nil, errors.New("sync tokens must be created through CreateSyncToken")
	}
	return tm.createToken(tokenType, orgID, repoID, path, userID, source, "", false, ttl)
}

func (tm *TokenManager) createToken(tokenType TokenType, orgID, repoID, path, userID, source, sourceID string, replace bool, ttl time.Duration) (*AccessToken, error) {
	// Canonicalise source before it is stored and before the link check reads it.
	// See the matching comment in db.TokenStore.createToken: a non-canonical
	// value would be a link token to the one EqualFold reader and a regular web
	// token to the nine exact-comparison readers, skipping the source-ID
	// requirement, the blank-source download rejections and the upload-link
	// limiters.
	source = strings.ToLower(strings.TrimSpace(source))
	if source == "link" {
		sourceID = strings.TrimSpace(sourceID)
		if sourceID == "" {
			return nil, errors.New("source ID is required for link tokens")
		}
	}

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
		SourceID:  sourceID,
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
	token, err := tm.createToken(TokenTypeUpload, orgID, repoID, path, userID, "", "", false, tm.tokenTTL)
	if err != nil {
		return "", err
	}
	return token.Token, nil
}

// CreateUpdateToken creates an upload token that overwrites the target path by default.
func (tm *TokenManager) CreateUpdateToken(orgID, repoID, path, userID string) (string, error) {
	token, err := tm.createToken(TokenTypeUpload, orgID, repoID, path, userID, "", "", true, tm.tokenTTL)
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

// CreateSyncToken creates the repository sync credential.
//
// It takes no path: a sync token is always scoped to the repository root, and
// leaving the caller unable to pass anything else is the point. The previous
// design minted these through CreateDownloadToken with a literal "/" argument,
// which meant a file-scoped download token and a sync credential differed only
// by the value one caller happened to pass.
func (tm *TokenManager) CreateSyncToken(orgID, repoID, userID string) (string, error) {
	token, err := tm.createToken(TokenTypeSync, orgID, repoID, "/", userID, "", "", false, tm.tokenTTL)
	if err != nil {
		return "", err
	}
	return token.Token, nil
}

// CreateLinkUploadToken creates an upload token tagged as a share/upload link.
func (tm *TokenManager) CreateLinkUploadToken(orgID, repoID, path, userID, sourceID string) (string, error) {
	if strings.TrimSpace(sourceID) == "" {
		return "", errors.New("source ID is required for link upload tokens")
	}
	token, err := tm.createToken(TokenTypeUpload, orgID, repoID, path, userID, "link", sourceID, false, tm.tokenTTL)
	if err != nil {
		return "", err
	}
	return token.Token, nil
}

// CreateLinkDownloadToken creates a download token tagged as a share link.
func (tm *TokenManager) CreateLinkDownloadToken(orgID, repoID, path, userID, sourceID string) (string, error) {
	if strings.TrimSpace(sourceID) == "" {
		return "", errors.New("source ID is required for link download tokens")
	}
	token, err := tm.createToken(TokenTypeDownload, orgID, repoID, path, userID, "link", sourceID, false, tm.tokenTTL)
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
	Identifier  string
	Filename    string
	ParentDir   string
	TotalSize   int64
	TrackerKey  string
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

	// Finalization fan-in: the single request that wins ClaimFinalization runs
	// the actual finalize and publishes its outcome here; any other request that
	// arrives once all bytes are present (e.g. a resumable retry of the final
	// chunk after the original finalize response was lost) waits on finalizeDone
	// and returns the same result instead of a bare {"success":true} ack the
	// client cannot turn into a dirent.
	finalizeDone   chan struct{}
	finalizeResult *finalizeOutcome
}

// finalizeOutcome is the shared result of a chunked-upload finalization, read by
// waiters once finalizeDone is closed. Exactly one of (fileID/...) or err is set.
type finalizeOutcome struct {
	fileID         string
	actualFilename string
	totalSize      int64
	err            error
}

type finalizeClaim int

const (
	// finalizeClaimIncomplete: not all bytes are present yet — return an ack.
	finalizeClaimIncomplete finalizeClaim = iota
	// finalizeClaimWinner: this request must run finalization and publish it.
	finalizeClaimWinner
	// finalizeClaimWaiter: another request is finalizing — wait for its result.
	finalizeClaimWaiter
)

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
	// chunkFinalizeOutcomeTTL bounds how long a finalized upload's result is kept
	// after its tracker is removed, so a late final-chunk retry can still read the
	// real file id. Long enough to cover client retry/backoff on huge uploads.
	// Safe to keep generous because the cache key includes the per-upload resumable
	// identifier, so it can only ever match a retry of that same upload.
	chunkFinalizeOutcomeTTL = 15 * time.Minute
	// chunkFinalizeOutcomeLimit is a hard safety fuse for the residual finalize-
	// outcome cache. 64k entries still covers dozens of finalized uploads/sec
	// across the 15-minute TTL window, while preventing sustained upload traffic
	// from growing the cache without bound between janitor sweeps.
	chunkFinalizeOutcomeLimit = 64 * 1024
)

// cachedFinalizeOutcome retains a finalized upload's result after its tracker is
// cleaned up, so a final-chunk retry that arrives in the residual window (winner
// already published and cleaned up the tracker) still receives the real file id
// instead of falling back to a "could not be confirmed" client error.
type cachedFinalizeOutcome struct {
	fileID         string
	actualFilename string
	totalSize      int64
	expiresAt      time.Time
}

// ChunkManager manages chunked uploads
type ChunkManager struct {
	uploads map[string]*ChunkUpload
	// outcomes caches successful finalize results by upload key for a short TTL
	// after the tracker is removed. Guarded by mu alongside uploads.
	outcomes map[string]cachedFinalizeOutcome
	mu       sync.RWMutex
	tempDir  string

	// Janitor/cache config — overridable in tests.
	janitorInterval time.Duration
	trackerTTL      time.Duration
	diskTTL         time.Duration
	outcomeTTL      time.Duration
	outcomeLimit    int
	now             func() time.Time

	janitorOnce sync.Once
	stopCh      chan struct{}
}

// NewChunkManager creates a new chunk manager and starts its janitor goroutine.
func NewChunkManager() *ChunkManager {
	cm := &ChunkManager{
		uploads:         make(map[string]*ChunkUpload),
		outcomes:        make(map[string]cachedFinalizeOutcome),
		tempDir:         os.TempDir(),
		janitorInterval: chunkJanitorInterval,
		trackerTTL:      chunkTrackerTTL,
		diskTTL:         chunkDiskTTL,
		outcomeTTL:      chunkFinalizeOutcomeTTL,
		outcomeLimit:    chunkFinalizeOutcomeLimit,
		now:             time.Now,
		stopCh:          make(chan struct{}),
	}
	cm.StartJanitor()
	return cm
}

// chunkFinalizeOutcomeKey identifies a specific upload for the finalize-outcome
// cache. It is keyed by the resumable upload identifier (unique per file per
// upload session — it encodes the relative path, so two folder files sharing a
// basename never collide, and a brand-new upload of the same name/path always
// gets a fresh identifier). parentDir + filename are folded in for defense.
// Callers must only use the cache when identifier is non-empty; an empty
// identifier cannot distinguish a retry from a different upload, so it must NOT
// be cached or served (it would risk returning a stale id for new content).
func chunkFinalizeOutcomeKey(token, identifier, parentDir, filename string) string {
	return token + "\x00" + identifier + "\x00" + parentDir + "\x00" + filename
}

// chunkUploadTrackerKey identifies the active in-memory/temp-file tracker for a
// chunked upload. Prefer the per-upload resumable identifier when present; it
// uniquely distinguishes retries of the same upload from a brand-new upload of
// the same path. When no identifier is available, fall back to parentDir +
// filename + totalSize so folder uploads with repeated basenames still isolate
// their temp files and progress trackers.
func chunkUploadTrackerKey(token, identifier, parentDir, filename string, totalSize int64) string {
	if identifier != "" {
		return token + "\x00" + identifier + "\x00" + parentDir + "\x00" + filename + "\x00" + strconv.FormatInt(totalSize, 10)
	}
	return token + "\x00" + parentDir + "\x00" + filename + "\x00" + strconv.FormatInt(totalSize, 10)
}

// chunkUploadTempName length budget: the sha1 hash already makes the name
// unique, so the human-readable filename hint is capped to keep the whole temp
// filename well under the filesystem's 255-byte per-component limit even for
// very long upload names.
const chunkUploadTempNameHintMax = 40

func chunkUploadTempPath(tempDir, trackerKey, filename string) string {
	hash := sha1.Sum([]byte(trackerKey))
	// sanitizeFilename yields pure ASCII, so a byte slice is rune-safe.
	hint := sanitizeFilename(filename)
	if len(hint) > chunkUploadTempNameHintMax {
		hint = hint[:chunkUploadTempNameHintMax]
	}
	return filepath.Join(
		tempDir,
		fmt.Sprintf("sesamefs_upload_%s_%s", hex.EncodeToString(hash[:]), hint),
	)
}

// CacheFinalizeOutcome records a successful finalize result so a late final-chunk
// retry (after CleanupUpload removed the tracker) can still be answered with the
// real file id. No-op when identifier is empty. totalSize is matched on lookup
// as a cheap extra guard.
func (cm *ChunkManager) CacheFinalizeOutcome(token, identifier, parentDir, filename, fileID, actualFilename string, totalSize int64) {
	if identifier == "" {
		return
	}
	key := chunkFinalizeOutcomeKey(token, identifier, parentDir, filename)
	now := cm.now()
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.outcomes[key] = cachedFinalizeOutcome{
		fileID:         fileID,
		actualFilename: actualFilename,
		totalSize:      totalSize,
		expiresAt:      now.Add(cm.outcomeTTL),
	}
	cm.pruneFinalizeOutcomesLocked(now)
}

// LookupFinalizeOutcome returns a cached finalize result if present, unexpired,
// and matching the requested totalSize. Always misses for an empty identifier so
// a distinct upload can never read another upload's id.
func (cm *ChunkManager) LookupFinalizeOutcome(token, identifier, parentDir, filename string, totalSize int64) (cachedFinalizeOutcome, bool) {
	if identifier == "" {
		return cachedFinalizeOutcome{}, false
	}
	key := chunkFinalizeOutcomeKey(token, identifier, parentDir, filename)
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	cached, ok := cm.outcomes[key]
	if !ok || cached.totalSize != totalSize || !cm.now().Before(cached.expiresAt) {
		return cachedFinalizeOutcome{}, false
	}
	return cached, true
}

// StartJanitor launches the background sweeper exactly once per manager.
func (cm *ChunkManager) StartJanitor() {
	cm.janitorOnce.Do(func() {
		go cm.janitorLoop()
	})
}

func (cm *ChunkManager) pruneFinalizeOutcomesLocked(now time.Time) {
	for key, cached := range cm.outcomes {
		if !now.Before(cached.expiresAt) {
			delete(cm.outcomes, key)
			metrics.ChunkUploadFinalizeOutcomeCacheEvictionsTotal.WithLabelValues("expired").Inc()
		}
	}

	if cm.outcomeLimit > 0 && len(cm.outcomes) > cm.outcomeLimit {
		type evictionCandidate struct {
			key       string
			expiresAt time.Time
		}

		candidates := make([]evictionCandidate, 0, len(cm.outcomes))
		for key, cached := range cm.outcomes {
			candidates = append(candidates, evictionCandidate{
				key:       key,
				expiresAt: cached.expiresAt,
			})
		}
		sort.Slice(candidates, func(i, j int) bool {
			if candidates[i].expiresAt.Equal(candidates[j].expiresAt) {
				return candidates[i].key < candidates[j].key
			}
			return candidates[i].expiresAt.Before(candidates[j].expiresAt)
		})

		overflow := len(cm.outcomes) - cm.outcomeLimit
		for i := 0; i < overflow; i++ {
			delete(cm.outcomes, candidates[i].key)
			metrics.ChunkUploadFinalizeOutcomeCacheEvictionsTotal.WithLabelValues("capacity").Inc()
		}
	}

	metrics.ChunkUploadFinalizeOutcomeCacheEntries.Set(float64(len(cm.outcomes)))
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

var (
	errChunkedUploadTooLarge             = errors.New("chunked upload exceeds configured max upload size")
	errChunkedUploadStagingLimitExceeded = errors.New("chunked upload staging capacity exceeded")
	errChunkedUploadInvalidTotalSize     = errors.New("chunked upload requires a positive total size")
	errInvalidChunkRange                 = errors.New("invalid chunk range")
)

// GetOrCreateUploadByIdentity gets or creates a chunk upload tracker for the
// specific upload identity carried on this request. Argument order matches
// GetUploadByIdentity / chunkUploadTrackerKey (parentDir before filename).
func (cm *ChunkManager) GetOrCreateUploadByIdentity(token, identifier, parentDir, filename string, totalSize int64) (*ChunkUpload, error) {
	return cm.GetOrCreateUploadByIdentityWithLimits(token, identifier, parentDir, filename, totalSize, 0, 0)
}

func (cm *ChunkManager) reservedStagingBytesLocked() int64 {
	var total int64
	for _, upload := range cm.uploads {
		if upload == nil || upload.TotalSize <= 0 {
			continue
		}
		total += upload.TotalSize
	}
	return total
}

func (cm *ChunkManager) validateUploadByKeyLocked(key string, totalSize, maxUploadBytes, maxStagingBytes int64) error {
	// Defense in depth: a non-positive declared total disables every size-derived
	// guard below (and validateChunkRange's upper bound downstream). The public HTTP
	// path already rejects it earlier, but guard here too so any direct caller in the
	// package cannot bypass the limits with total <= 0.
	if totalSize <= 0 {
		return fmt.Errorf("%w: declared size %d bytes", errChunkedUploadInvalidTotalSize, totalSize)
	}
	if _, exists := cm.uploads[key]; exists {
		return nil
	}
	if maxUploadBytes > 0 && totalSize > maxUploadBytes {
		return fmt.Errorf("%w: declared size %d bytes exceeds limit %d bytes", errChunkedUploadTooLarge, totalSize, maxUploadBytes)
	}
	if maxStagingBytes > 0 {
		reserved := cm.reservedStagingBytesLocked()
		if reserved > maxStagingBytes-totalSize {
			return fmt.Errorf("%w: reserved=%d requested=%d limit=%d", errChunkedUploadStagingLimitExceeded, reserved, totalSize, maxStagingBytes)
		}
	}
	return nil
}

func (cm *ChunkManager) ValidateUploadByIdentityWithLimits(token, identifier, parentDir, filename string, totalSize, maxUploadBytes, maxStagingBytes int64) error {
	key := chunkUploadTrackerKey(token, identifier, parentDir, filename, totalSize)
	// Read-only admission pre-check on the per-chunk hot path: validateUploadByKeyLocked
	// only reads cm.uploads and immutable TotalSize fields, so a shared lock is enough
	// and avoids serializing concurrent chunk requests. Tracker creation re-validates
	// under the exclusive lock, so this is purely a fail-fast.
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.validateUploadByKeyLocked(key, totalSize, maxUploadBytes, maxStagingBytes)
}

// GetOrCreateUploadByIdentityWithLimits gets or creates a chunk upload tracker
// while enforcing server-side hard limits for chunked uploads.
func (cm *ChunkManager) GetOrCreateUploadByIdentityWithLimits(token, identifier, parentDir, filename string, totalSize, maxUploadBytes, maxStagingBytes int64) (*ChunkUpload, error) {
	key := chunkUploadTrackerKey(token, identifier, parentDir, filename, totalSize)
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if err := cm.validateUploadByKeyLocked(key, totalSize, maxUploadBytes, maxStagingBytes); err != nil {
		return nil, err
	}
	if upload, exists := cm.uploads[key]; exists {
		return upload, nil
	}

	// Create temp file
	tempPath := chunkUploadTempPath(cm.tempDir, key, filename)
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
		Identifier:  identifier,
		Filename:    filename,
		ParentDir:   parentDir,
		TotalSize:   totalSize,
		TrackerKey:  key,
		OperationID: newSeafHTTPUploadOperationID(token),
		TempFile:    tempFile,
		TempPath:    tempPath,
		ReceivedEnd: -1,
		updatedAt:   cm.now(),
	}
	cm.uploads[key] = upload
	log.Printf("[ChunkManager] Created upload tracker: op=%s file=%s dir=%s totalSize=%d", upload.OperationID, filename, parentDir, totalSize)
	return upload, nil
}

// GetOrCreateUpload is the legacy helper used by tests and callers that do not
// carry a resumable identifier.
func (cm *ChunkManager) GetOrCreateUpload(token, filename, parentDir string, totalSize int64) (*ChunkUpload, error) {
	return cm.GetOrCreateUploadByIdentity(token, "", parentDir, filename, totalSize)
}

func (cm *ChunkManager) GetOrCreateUploadWithLimits(token, filename, parentDir string, totalSize, maxUploadBytes, maxStagingBytes int64) (*ChunkUpload, error) {
	return cm.GetOrCreateUploadByIdentityWithLimits(token, "", parentDir, filename, totalSize, maxUploadBytes, maxStagingBytes)
}

func (cm *ChunkManager) GetUploadByIdentity(token, identifier, parentDir, filename string, totalSize int64) *ChunkUpload {
	key := chunkUploadTrackerKey(token, identifier, parentDir, filename, totalSize)
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.uploads[key]
}

func (cm *ChunkManager) GetUpload(token, filename string) *ChunkUpload {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	for _, upload := range cm.uploads {
		if upload.Token == token && upload.Filename == filename {
			return upload
		}
	}
	return nil
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
	// Sweep cached finalize outcomes in the same pass so both expiration and the
	// hard-cap safety fuse stay enforced even when writes go quiet.
	cm.pruneFinalizeOutcomesLocked(now)
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

// ClaimFinalization decides this request's role in finalizing the upload.
// Exactly one concurrent caller becomes the winner and must run finalization
// then publish the outcome via PublishFinalize{Success,Failure}. Callers that
// arrive once all bytes are present but finalization is already underway become
// waiters and must block on the returned channel, then read FinalizeOutcome.
// Callers whose bytes are not all present yet are incomplete.
func (cu *ChunkUpload) ClaimFinalization() (finalizeClaim, <-chan struct{}) {
	cu.mu.Lock()
	defer cu.mu.Unlock()
	if cu.Finalizing || cu.finalizeDone != nil {
		metrics.ChunkUploadFinalizationAttemptsTotal.WithLabelValues("already_finalizing").Inc()
		return finalizeClaimWaiter, cu.finalizeDone
	}
	if !cu.isCompleteLocked() {
		metrics.ChunkUploadFinalizationAttemptsTotal.WithLabelValues("not_complete").Inc()
		return finalizeClaimIncomplete, nil
	}
	cu.Finalizing = true
	cu.finalizationStarted = true
	cu.finalizeDone = make(chan struct{})
	metrics.ChunkUploadFinalizationAttemptsTotal.WithLabelValues("started").Inc()
	return finalizeClaimWinner, cu.finalizeDone
}

// TryStartFinalization reports whether this caller won the right to finalize.
// Retained for callers/tests that only need winner detection.
func (cu *ChunkUpload) TryStartFinalization() bool {
	claim, _ := cu.ClaimFinalization()
	return claim == finalizeClaimWinner
}

func (cu *ChunkUpload) publishFinalizeOutcomeLocked(outcome *finalizeOutcome) {
	if cu.finalizeDone == nil {
		// Ownership was reset/dropped; there is no current waiter set to notify.
		return
	}
	select {
	case <-cu.finalizeDone:
		// Already published once — do not overwrite or double-close.
		return
	default:
	}
	cu.finalizeResult = outcome
	close(cu.finalizeDone)
	cu.updatedAt = time.Now()
}

// PublishFinalizeSuccess records the finalize result and wakes any waiters.
func (cu *ChunkUpload) PublishFinalizeSuccess(fileID, actualFilename string, totalSize int64) {
	cu.mu.Lock()
	defer cu.mu.Unlock()
	cu.publishFinalizeOutcomeLocked(&finalizeOutcome{fileID: fileID, actualFilename: actualFilename, totalSize: totalSize})
}

// PublishFinalizeFailure records a finalize failure and wakes any waiters so
// they surface a retryable error instead of a false success.
func (cu *ChunkUpload) PublishFinalizeFailure(err error) {
	cu.mu.Lock()
	defer cu.mu.Unlock()
	cu.publishFinalizeOutcomeLocked(&finalizeOutcome{err: err})
}

// FinalizeOutcome returns the published finalize result, if any.
func (cu *ChunkUpload) FinalizeOutcome() (*finalizeOutcome, bool) {
	cu.mu.Lock()
	defer cu.mu.Unlock()
	return cu.finalizeResult, cu.finalizeResult != nil
}

func (cu *ChunkUpload) ResetFinalization() {
	cu.mu.Lock()
	defer cu.mu.Unlock()
	cu.Finalizing = false
	// Drop the done channel so the next claimant becomes a fresh winner. Any
	// waiters already holding the previous (closed) channel keep their captured
	// reference; finalizeResult is left intact for them to read.
	cu.finalizeDone = nil
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
		return fmt.Errorf("%w: start=%d end=%d", errInvalidChunkRange, start, end)
	}
	if totalSize > 0 && end >= totalSize {
		return fmt.Errorf("%w: end=%d total=%d", errInvalidChunkRange, end, totalSize)
	}
	expected := end - start + 1
	if written >= 0 && written != expected {
		return fmt.Errorf("%w: chunk size mismatch range=%d written=%d", errInvalidChunkRange, expected, written)
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

// DebugCompletenessSnapshot returns the tracker's completeness state for
// diagnostics: whether it is complete, the first missing byte range (gap), the
// number of merged ranges, and the total received bytes. Used to pinpoint why a
// chunked upload never finalizes despite the final-offset chunk having arrived.
func (cu *ChunkUpload) DebugCompletenessSnapshot() (complete bool, firstGapStart, firstGapEnd int64, rangeCount int, receivedBytes int64) {
	cu.mu.Lock()
	defer cu.mu.Unlock()
	complete = cu.isCompleteLocked()
	rangeCount = len(cu.Ranges)
	firstGapStart, firstGapEnd = -1, -1
	for _, r := range cu.Ranges {
		receivedBytes += r.End - r.Start + 1
	}
	if complete {
		return complete, firstGapStart, firstGapEnd, rangeCount, receivedBytes
	}
	// Ranges are kept sorted+merged by markRangeReceivedLocked, so the first gap
	// is either before the first range, between two ranges, or after the last.
	cursor := int64(0)
	for _, r := range cu.Ranges {
		if r.Start > cursor {
			return complete, cursor, r.Start - 1, rangeCount, receivedBytes
		}
		if r.End+1 > cursor {
			cursor = r.End + 1
		}
	}
	if cursor < cu.TotalSize {
		firstGapStart, firstGapEnd = cursor, cu.TotalSize-1
	}
	return complete, firstGapStart, firstGapEnd, rangeCount, receivedBytes
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

func (cm *ChunkManager) cleanupUploadByKey(key string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if upload, exists := cm.uploads[key]; exists {
		upload.Cleanup()
		delete(cm.uploads, key)
		log.Printf("[ChunkManager] Cleaned up upload: op=%s file=%s", upload.OperationID, upload.Filename)
	}
}

func (cm *ChunkManager) CleanupUploadByIdentity(token, identifier, parentDir, filename string, totalSize int64) {
	cm.cleanupUploadByKey(chunkUploadTrackerKey(token, identifier, parentDir, filename, totalSize))
}

func (cm *ChunkManager) CleanupTrackedUpload(upload *ChunkUpload) {
	if upload == nil {
		return
	}
	if upload.TrackerKey != "" {
		cm.cleanupUploadByKey(upload.TrackerKey)
		return
	}
	cm.CleanupUpload(upload.Token, upload.Filename)
}

// CleanupUpload removes tracked uploads matching the legacy token/filename
// lookup. Production chunked uploads should prefer CleanupTrackedUpload or
// CleanupUploadByIdentity so same-basename uploads in different paths stay
// isolated.
func (cm *ChunkManager) CleanupUpload(token, filename string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	for key, upload := range cm.uploads {
		if upload.Token != token || upload.Filename != filename {
			continue
		}
		upload.Cleanup()
		delete(cm.uploads, key)
		log.Printf("[ChunkManager] Cleaned up upload: op=%s file=%s", upload.OperationID, upload.Filename)
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
	storage                *storage.S3Store
	storageManager         *storage.Manager
	db                     *db.DB
	tokenStore             TokenStore
	config                 *config.Config
	permMiddleware         *middleware.PermissionMiddleware
	downloadAdmission      *downloadadmission.Coordinator
	blockRepresentationIDs *syncBlockRepresentationIDCache
	zipMaxEntries          int
	zipMaxDepth            int
	zipMaxBytes            int64

	// uploadLinkWriteLimits bounds anonymous writes arriving through a public
	// upload link. Nil when disabled by configuration.
	uploadLinkWriteLimits *uploadLinkWriteLimits
	// uploadLinkInflight bounds active anonymous link writes per stable source
	// and across this process. It is intentionally not cluster-global.
	uploadLinkInflight *uploadLinkInflightLimiter
}

type uploadLinkInflightLimiter struct {
	mu           sync.Mutex
	maxPerSource int
	maxPerNode   int
	inflight     int
	perSource    map[string]int
	logSample    rate.Sometimes
}

func newUploadLinkInflightLimiter(cfg *config.Config) *uploadLinkInflightLimiter {
	if cfg == nil || (cfg.SeafHTTP.UploadLinkMaxInflightPerSource <= 0 && cfg.SeafHTTP.UploadLinkMaxInflightPerNode <= 0) {
		return nil
	}
	return &uploadLinkInflightLimiter{
		maxPerSource: cfg.SeafHTTP.UploadLinkMaxInflightPerSource,
		maxPerNode:   cfg.SeafHTTP.UploadLinkMaxInflightPerNode,
		perSource:    make(map[string]int),
		logSample:    rate.Sometimes{Interval: time.Minute},
	}
}

// tryAcquire atomically checks both bounds and never queues callers. The
// returned closure owns exactly one admission and is safe to call repeatedly.
func (l *uploadLinkInflightLimiter) tryAcquire(sourceID string) (func(), string) {
	if l == nil {
		return func() {}, ""
	}
	l.mu.Lock()
	if l.maxPerSource > 0 && l.perSource[sourceID] >= l.maxPerSource {
		l.mu.Unlock()
		return nil, "source"
	}
	if l.maxPerNode > 0 && l.inflight >= l.maxPerNode {
		l.mu.Unlock()
		return nil, "node"
	}
	l.inflight++
	l.perSource[sourceID]++
	sourceInflight := l.perSource[sourceID]
	metrics.UploadLinkInflightCurrent.Inc()
	metrics.UploadLinkSourceInflightOccupancy.Observe(float64(sourceInflight))
	l.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			l.inflight--
			metrics.UploadLinkInflightCurrent.Dec()
			if l.perSource[sourceID] == 1 {
				delete(l.perSource, sourceID)
			} else {
				l.perSource[sourceID]--
			}
			l.mu.Unlock()
		})
	}, ""
}

func (l *uploadLinkInflightLimiter) reject(c *gin.Context, reason string) {
	const retryAfter = 1
	metrics.UploadLinkInflightRejectedTotal.WithLabelValues(reason).Inc()
	l.logSample.Do(func() {
		log.Printf("[HandleUpload] rejecting anonymous upload-link write at process-local in-flight cap (reason=%s); sampled, see upload_link_inflight_rejected_total", reason)
	})
	c.Header("Retry-After", strconv.Itoa(retryAfter))
	c.JSON(http.StatusTooManyRequests, gin.H{
		"error":       "too many uploads in progress, please try again shortly",
		"retry_after": retryAfter,
	})
}

func (h *SeafHTTPHandler) acquireUploadLinkInflight(c *gin.Context, token *AccessToken) (func(), bool) {
	if h == nil || h.uploadLinkInflight == nil || token == nil || token.Source != "link" {
		return func() {}, true
	}
	release, reason := h.uploadLinkInflight.tryAcquire(token.SourceID)
	if release == nil {
		h.uploadLinkInflight.reject(c, reason)
		return nil, false
	}
	return release, true
}

// uploadLinkWriteLimits holds the two buckets that bound anonymous upload-link
// writes, plus the sampler that keeps the rejection path from becoming its own
// amplifier.
type uploadLinkWriteLimits struct {
	// perClient is keyed on (client IP, stable link source). Keying on IP alone would
	// make one uploader behind a NAT throttle everyone else behind it who is
	// using a different link.
	perClient *middleware.RateLimiter
	// perSource is keyed on the stable link source across every IP, which is the only
	// bucket that can see a leaked upload URL being hit from many addresses at
	// once. Nil when that bound is disabled separately.
	perSource *middleware.RateLimiter
	// logSample keeps at most one throttle log per interval. A rejected request
	// is cheap by design, and an attacker who keeps sending after the bucket
	// empties would otherwise turn the defence into a log-volume amplifier: the
	// counter still moves per rejection, the log line does not.
	logSample rate.Sometimes
}

// allowUploadLinkWrite decides whether an upload may proceed, and is deliberately
// keyed on the token's origin rather than on the route.
//
// /seafhttp/upload-api/:token serves BOTH the anonymous public-upload-link flow
// and authenticated web uploads; only the former is the unbounded anonymous
// surface (subcontract A). A limiter installed as route middleware would have
// throttled the authenticated path too, which is traffic this bound has no
// business touching. AccessToken.Source is what separates them: "link" is a
// share/upload link, "" is a regular user.
//
// It admits the request whenever the limiter is disabled or the token is not a
// link token, so the only requests it can ever refuse are anonymous link writes.
func (h *SeafHTTPHandler) allowUploadLinkWrite(c *gin.Context, token *AccessToken) bool {
	if h == nil || h.uploadLinkWriteLimits == nil || token == nil || token.Source != "link" {
		return true
	}
	limits := h.uploadLinkWriteLimits

	sourceID := token.SourceID
	clientIP := c.ClientIP()
	now := time.Now()

	sourceAdmission := limits.perSource.TryReserve(sourceID, now)
	if sourceAdmission == nil {
		limits.reject(c, "source", clientIP, token.RepoID)
		return false
	}
	clientAdmission := limits.perClient.TryReserve(clientIP+"|"+sourceID, now)
	if clientAdmission == nil {
		// Use the original captured now: cancelling at a later time can fail to
		// restore the token after the reservation's refill interval has elapsed.
		sourceAdmission.CancelAt(now)
		limits.reject(c, "client", clientIP, token.RepoID)
		return false
	}

	return true
}

// reject answers a throttled write. 429 is right for this surface specifically
// because it is browser traffic: the sync protocol is elsewhere, and its client
// does not treat 429 as retryable.
func (l *uploadLinkWriteLimits) reject(c *gin.Context, reason, clientIP, repoID string) {
	limiter := l.perClient
	if reason == "source" {
		limiter = l.perSource
	}
	retryAfter := limiter.RetryAfterSeconds()

	metrics.UploadLinkWriteThrottledTotal.WithLabelValues(reason).Inc()
	l.logSample.Do(func() {
		log.Printf("[HandleUpload] throttling anonymous upload-link writes (reason=%s, most recent from %s, repo %s); sampled, see upload_link_write_throttled_total",
			reason, clientIP, repoID)
	})

	c.Header("Retry-After", strconv.Itoa(retryAfter))
	c.JSON(http.StatusTooManyRequests, gin.H{
		"error":       "too many upload requests, please try again shortly",
		"retry_after": retryAfter,
	})
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

type zipTrafficAccounting struct {
	context     *gin.Context
	quotaStatus traffic.QuotaStatus
	orgID       string
	userID      string
	trafficType string
	bytesBefore int64
	active      bool
}

var seafHTTPNewCanonicalBlockReaderFn = streaming.NewCanonicalBlockReader
var seafHTTPBatchResolveBlockIDsFn = streaming.BatchResolveBlockIDsContext
var seafHTTPLookupFileBlocksFn = func(ctx context.Context, h *SeafHTTPHandler, hostname string, token *AccessToken) ([]string, int64, []byte, []byte, *storage.BlockStore, string, error) {
	return h.lookupFileBlocksContext(ctx, hostname, token)
}
var seafHTTPResolveBlockRepresentationIDFn = func(ctx context.Context, h *SeafHTTPHandler, orgID, repoID string) (string, error) {
	return db.ResolveBlockRepresentationIDContext(ctx, h.db.Session(), orgID, repoID)
}

// seafHTTPLookupLibraryEncryptedFn is the shared encrypted-library probe for
// file and ZIP download. Ignoring its error previously made ZIP treat a
// Cassandra failure as "not encrypted" and stream ciphertext as a 200.
var seafHTTPLookupLibraryEncryptedFn = func(ctx context.Context, h *SeafHTTPHandler, orgID, repoID string) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var encrypted bool
	err := h.db.Session().Query(`
		SELECT encrypted FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).WithContext(ctx).Scan(&encrypted)
	return encrypted, err
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
		storage:                s3Store,
		storageManager:         storageManager,
		db:                     database,
		tokenStore:             tokenStore,
		config:                 cfg,
		permMiddleware:         permMiddleware,
		blockRepresentationIDs: newSyncBlockRepresentationIDCache(),
		zipMaxEntries:          defaultZipMaxEntries,
		zipMaxDepth:            defaultZipMaxDepth,
		zipMaxBytes:            defaultZipMaxBytes,
		uploadLinkWriteLimits:  newUploadLinkWriteLimits(cfg),
		uploadLinkInflight:     newUploadLinkInflightLimiter(cfg),
	}
}

// SetDownloadAdmissionCoordinator makes the process-wide coordinator available
// for the D4 download producers owned by this handler.
func (h *SeafHTTPHandler) SetDownloadAdmissionCoordinator(coordinator *downloadadmission.Coordinator) {
	h.downloadAdmission = coordinator
}

// newUploadLinkWriteLimits builds the enabled anonymous upload-link buckets.
// Either bound can be disabled independently; the container is nil only when
// both are off. RateLimiter.Allow and Stop are nil-safe for the missing member.
func newUploadLinkWriteLimits(cfg *config.Config) *uploadLinkWriteLimits {
	if cfg == nil {
		return nil
	}
	limits := &uploadLinkWriteLimits{
		perClient: newPerMinuteRateLimiter(cfg.SeafHTTP.UploadLinkWritesPerMinute, cfg.SeafHTTP.UploadLinkWriteBurst),
		perSource: newPerMinuteRateLimiter(cfg.SeafHTTP.UploadLinkSourceWritesPerMinute, cfg.SeafHTTP.UploadLinkSourceWriteBurst),
	}
	if limits.perClient == nil && limits.perSource == nil {
		return nil
	}
	limits.logSample.Interval = time.Minute
	return limits
}

// newPerMinuteRateLimiter converts a per-minute rate into a token bucket, or nil
// when the rate disables it. Validate() has already rejected a zero burst paired
// with a live rate; the guard here is for handlers built in tests from a config
// that never went through it.
func newPerMinuteRateLimiter(perMinute, burst int) *middleware.RateLimiter {
	if perMinute <= 0 || burst <= 0 {
		return nil
	}
	return middleware.NewRateLimiter(rate.Every(time.Minute/time.Duration(perMinute)), burst)
}

// Close releases the handler's background workers. The limiters each run a
// cleanup goroutine, so a handler that outlives its server — or one built per
// test — must be able to stop them.
func (h *SeafHTTPHandler) Close() {
	if h == nil || h.uploadLinkWriteLimits == nil {
		return
	}
	h.uploadLinkWriteLimits.perClient.Stop()
	h.uploadLinkWriteLimits.perSource.Stop()
}

func (h *SeafHTTPHandler) resolveBlockRepresentationID(orgID, repoID string) (string, error) {
	if h == nil || h.db == nil {
		return db.PlainBlockRepresentationID, nil
	}
	if h.blockRepresentationIDs != nil {
		if representationID, ok := h.blockRepresentationIDs.get(orgID, repoID); ok {
			return representationID, nil
		}
	}
	representationID, err := db.ResolveBlockRepresentationID(h.db.Session(), orgID, repoID)
	if err != nil {
		return "", err
	}
	if h.blockRepresentationIDs != nil {
		h.blockRepresentationIDs.put(orgID, repoID, representationID)
	}
	return representationID, nil
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

const bytesPerMiB = 1024 * 1024

func configuredChunkedMaxUploadBytes(cfg *config.Config) int64 {
	if cfg == nil || cfg.Server.MaxUploadMB <= 0 {
		return 0
	}
	return cfg.Server.MaxUploadMB * bytesPerMiB
}

func configuredChunkedStagingMaxBytes(cfg *config.Config) int64 {
	if cfg == nil || cfg.SeafHTTP.ChunkedStagingMaxBytes <= 0 {
		return 0
	}
	return cfg.SeafHTTP.ChunkedStagingMaxBytes
}

func writeChunkedUploadInitializationError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, errChunkedUploadTooLarge):
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "chunked upload exceeds configured max upload size"})
	case errors.Is(err, errChunkedUploadStagingLimitExceeded):
		c.JSON(http.StatusInsufficientStorage, gin.H{"error": "chunked upload staging capacity exceeded on this node"})
	case errors.Is(err, errChunkedUploadInvalidTotalSize):
		c.JSON(http.StatusBadRequest, gin.H{"error": "chunked upload requires a positive total size in Content-Range"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to initialize upload"})
	}
}

func writeInvalidChunkRangeError(c *gin.Context, err error) {
	c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
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

func (h *SeafHTTPHandler) lookupLibraryStorageClass(orgID, repoID string) (string, error) {
	return h.lookupLibraryStorageClassContext(context.Background(), orgID, repoID)
}

func (h *SeafHTTPHandler) lookupLibraryStorageClassContext(ctx context.Context, orgID, repoID string) (string, error) {
	if h == nil || h.db == nil || orgID == "" || repoID == "" {
		return "", nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	var storageClass string
	if err := h.db.Session().Query(`
		SELECT storage_class FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).WithContext(ctx).Scan(&storageClass); err != nil {
		return "", err
	}

	return storageClass, nil
}

var lookupLibraryStorageClassForSeafHTTPFn = func(ctx context.Context, h *SeafHTTPHandler, orgID, repoID string) (string, error) {
	return h.lookupLibraryStorageClassContext(ctx, orgID, repoID)
}

// resolveLibraryBlockStore returns the library-preferred store for new
// materialization and fallback plumbing. Canonical reads and repairs resolve the
// persisted block storage_class instead of this mutable library preference.
func (h *SeafHTTPHandler) resolveLibraryBlockStore(hostname, orgID, repoID string) (*storage.BlockStore, string, error) {
	return h.resolveLibraryBlockStoreContext(context.Background(), hostname, orgID, repoID)
}

func (h *SeafHTTPHandler) resolveLibraryBlockStoreContext(ctx context.Context, hostname, orgID, repoID string) (*storage.BlockStore, string, error) {
	libraryClass, err := lookupLibraryStorageClassForSeafHTTPFn(ctx, h, orgID, repoID)
	if err != nil {
		return nil, "", fmt.Errorf("lookup library storage class: %w", err)
	}
	if h.storageManager != nil {
		preferredClass, err := h.storageManager.ResolveStorageClass(hostname, libraryClass, "hot")
		if err != nil {
			return nil, libraryClass, err
		}
		return h.storageManager.GetHealthyBlockStoreForOrg(orgID, preferredClass)
	}
	// Fallback: org-scoped store from the raw S3 store; never the org-less singleton.
	if h.storage != nil {
		bs, err := storage.NewOrgBlockStore(h.storage, "blocks/", orgID)
		if err != nil {
			return nil, libraryClass, err
		}
		return bs, libraryClass, nil
	}

	return nil, libraryClass, fmt.Errorf("block storage not available")
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
var putUploadedBlockAutoDirectForUploadFn = func(ctx context.Context, blockStore *storage.BlockStore, storageKey string, data []byte) (string, error) {
	return blockStore.PutObjectAutoDirect(ctx, storageKey, data)
}
var probeUploadedBlockReuseForUploadFn = v2.ProbeUploadedBlockReuse
var prepareUploadedBlockProbeForUploadFn = v2.PrepareUploadedBlockProbe
var ensureReusableBlockPresentForUploadFn = v2.EnsureReusableBlockPresent
var resolveNeedsPutBlockStoreForUploadFn = v2.ResolveNeedsPutBlockStore
var registerUploadedBlockAndMappingForUploadFn = v2.RegisterUploadedBlockAndMapping
var resolveSeafHTTPStoredBlockIDsFn = func(fsHelper *v2.FSHelper, orgID, repoID string, blockIDs []string) ([]string, error) {
	return fsHelper.ResolveStoredBlockIDs(orgID, repoID, blockIDs)
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
	if token.Source == "link" && strings.TrimSpace(token.SourceID) == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid upload token"})
		return
	}

	// Throttle anonymous link writes here — after the token resolves, because
	// Source is what the decision keys on, and before the permission lookup, the
	// body read and any storage work. It does NOT save the token lookup above:
	// that read has already happened by the time we can tell a link token from a
	// regular one. It can only refuse Source=="link" tokens; authenticated web
	// uploads on this same route are unaffected.
	if !h.allowUploadLinkWrite(c, token) {
		return
	}
	releaseInflight, admitted := h.acquireUploadLinkInflight(c, token)
	if !admitted {
		return
	}
	defer releaseInflight()

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
	if isChunked {
		if total <= 0 {
			log.Printf("[HandleUpload] Rejecting chunked upload with non-positive declared total=%d", total)
			c.JSON(http.StatusBadRequest, gin.H{"error": "chunked upload requires a positive total size in Content-Range"})
			return
		}
		if err := validateChunkRange(start, end, total, -1); err != nil {
			log.Printf("[HandleUpload] Rejecting chunked upload with invalid range start=%d end=%d total=%d: %v", start, end, total, err)
			writeInvalidChunkRangeError(c, err)
			return
		}
	} else if contentRange != "" {
		// A present-but-unparseable Content-Range must fail closed rather than
		// silently fall through to the single-shot path: the client clearly meant
		// a chunked transfer, so honoring it as a whole-file upload would bypass
		// the chunked range/size handling entirely.
		log.Printf("[HandleUpload] Rejecting malformed Content-Range header: %q", contentRange)
		c.JSON(http.StatusBadRequest, gin.H{"error": "malformed Content-Range header"})
		return
	}

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
	// resumable.js sends a stable per-file-per-session identifier on every chunk.
	// It uniquely distinguishes this upload (it encodes the relative path and a
	// session timestamp), so it is the only safe key for the finalize-outcome
	// cache: a retry carries the same identifier, a different upload never does.
	uploadIdentifier := c.PostForm("resumableIdentifier")

	filename := header.Filename

	// Handle relative_path for folder uploads (e.g., "my-folder/subfolder/file.txt")
	if relativePath != "" {
		if strings.HasSuffix(relativePath, "/") {
			dirName := strings.TrimSuffix(relativePath, "/")
			dirBaseName := path.Base(dirName)

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
			parentDir = path.Join(parentDir, dirName)
		} else {
			relDir := path.Dir(relativePath)
			if relDir != "." && relDir != "" {
				parentDir = path.Join(parentDir, relDir)
			}
			filename = path.Base(relativePath)
		}
	}

	if !strings.HasPrefix(parentDir, "/") {
		parentDir = "/" + parentDir
	}
	parentDir = path.Clean(parentDir)

	log.Printf("[HandleUpload] relativePath=%s, parentDir=%s, filename=%s", relativePath, parentDir, filename)

	filePath := path.Join(parentDir, filename)
	storageKey := fmt.Sprintf("%s/%s%s", token.OrgID, token.RepoID, filePath)

	// LOCK ENFORCEMENT: overwriting a file locked by another user is forbidden.
	// Only the overwrite path can clobber the locked file; an autorename upload
	// creates a new name and leaves the locked file untouched.
	if replaceFile {
		blocked, ownerID, err := checkSeafHTTPFileLockedByOther(h, token.RepoID, filePath, token.UserID)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "failed to verify file lock"})
			return
		}
		if blocked {
			log.Printf("[HandleUpload] Rejecting overwrite of file %q locked by %s (uploader %s)", filePath, ownerID, token.UserID)
			c.JSON(http.StatusForbidden, gin.H{"error": "file is locked by another user", "lock_owner": ownerID})
			return
		}
	}

	log.Printf("[HandleUpload] Token=%s, File=%s, ContentRange=%s, isChunked=%v",
		tokenStr, filename, contentRange, isChunked)

	if isChunked {
		// total <= 0 and invalid chunk ranges were already rejected right after
		// parseContentRange, before any tracker or temp file could be created.
		chunkedMaxUploadBytes := configuredChunkedMaxUploadBytes(h.config)
		chunkedStagingMaxBytes := configuredChunkedStagingMaxBytes(h.config)

		// A finalize result cached for this upload means it already completed —
		// this is a late retry of whichever chunk carried the finalize (the
		// winner published and the tracker was already cleaned up). Answer with
		// the real file id so the client builds the dirent instead of failing
		// with "could not be confirmed".
		//
		// We must NOT gate this on end == total-1: with simultaneous_uploads > 1
		// chunks arrive out of order, so the chunk that completes contiguity (and
		// runs finalization) is arbitrary — frequently NOT the final-offset chunk.
		// Gating on the final offset re-broke exactly this case. The cache key is
		// the per-upload resumable identifier, so any matching request is by
		// definition a retry of this already-finalized upload and safe to answer.
		if cached, ok := chunkManager.LookupFinalizeOutcome(tokenStr, uploadIdentifier, parentDir, filename, total); ok {
			log.Printf("[HandleUpload] Returning cached finalize result for late retry: %s", filename)
			if retJSON {
				c.JSON(http.StatusOK, []gin.H{{"name": cached.actualFilename, "id": cached.fileID, "size": strconv.FormatInt(cached.totalSize, 10)}})
			} else {
				c.String(http.StatusOK, cached.fileID)
			}
			return
		}
		if err := chunkManager.ValidateUploadByIdentityWithLimits(tokenStr, uploadIdentifier, parentDir, filename, total, chunkedMaxUploadBytes, chunkedStagingMaxBytes); err != nil {
			log.Printf("[HandleUpload] Chunked upload rejected before quota precheck: %v", err)
			writeChunkedUploadInitializationError(c, err)
			return
		}

		existingUpload := chunkManager.GetUploadByIdentity(tokenStr, uploadIdentifier, parentDir, filename, total)
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
		upload, err := chunkManager.GetOrCreateUploadByIdentityWithLimits(
			tokenStr,
			uploadIdentifier,
			parentDir,
			filename,
			total,
			chunkedMaxUploadBytes,
			chunkedStagingMaxBytes,
		)
		if err != nil {
			log.Printf("[HandleUpload] Failed to create upload tracker: %v", err)
			writeChunkedUploadInitializationError(c, err)
			return
		}
		upload.MarkQuotaPrecheck(parentDir, total, replaceFile)

		// Stream chunk directly to temp file at the correct offset
		if err := upload.WriteChunkFromReader(file, start, end); err != nil {
			log.Printf("[HandleUpload] Failed to write chunk: %v", err)
			if errors.Is(err, errInvalidChunkRange) {
				writeInvalidChunkRangeError(c, err)
			} else {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to write chunk"})
			}
			return
		}

		writeFinalizeSuccess := func(id, name string, size int64) {
			if retJSON {
				c.JSON(http.StatusOK, []gin.H{{"name": name, "id": id, "size": strconv.FormatInt(size, 10)}})
			} else {
				c.String(http.StatusOK, id)
			}
		}

		claim, finalizeDone := upload.ClaimFinalization()
		if claim == finalizeClaimIncomplete {
			// Out-of-order chunks routinely arrive while the upload is still
			// incomplete — normal under concurrency, and it must stay a 200 ack so
			// the client keeps sending the remaining chunks. The healthy case has
			// its gap near the END (a middle/late chunk still in flight). But a gap
			// that starts at byte 0 — the prefix is still missing even though the
			// final byte has arrived — is the suspicious shape we saw when chunks
			// were split across trackers after a token change. It can still be
			// transient under reordering, so keep the log explicitly diagnostic-only.
			if end >= total-1 {
				_, gapStart, gapEnd, ranges, received := upload.DebugCompletenessSnapshot()
				if gapStart == 0 {
					log.Printf("[HandleUpload] FINAL_CHUNK_BUT_INCOMPLETE (prefix missing; possible tracker split or earlier chunks still in flight) op=%s file=%q first_gap=%d-%d ranges=%d received=%d/%d identifier=%q",
						upload.OperationID, filename, gapStart, gapEnd, ranges, received, total, uploadIdentifier)
				}
			}
			log.Printf("[HandleUpload] Chunk received, waiting for more: %d/%d", end+1, total)
			c.JSON(http.StatusOK, gin.H{"success": true})
			return
		}

		if claim == finalizeClaimWaiter {
			// Another in-flight request owns finalization for this exact upload —
			// typically a resumable retry of the final chunk after the original
			// finalize response was lost on the wire. Block on the shared result
			// and return it, so the client gets real file metadata instead of a
			// bare ack it cannot turn into a dirent (which silently dropped big
			// files from the listing even though the bytes were committed).
			log.Printf("[HandleUpload] Finalization already in progress for %s; waiting for shared result", filename)
			select {
			case <-finalizeDone:
			case <-c.Request.Context().Done():
				return
			}
			outcome, ok := upload.FinalizeOutcome()
			if !ok || outcome == nil {
				c.JSON(http.StatusConflict, gin.H{"error": "library was modified concurrently; retry the upload"})
				return
			}
			if outcome.err != nil {
				writeSeafHTTPUploadError(c, outcome.err, "failed to finalize upload")
				return
			}
			writeFinalizeSuccess(outcome.fileID, outcome.actualFilename, outcome.totalSize)
			return
		}

		// claim == finalizeClaimWinner — this request owns finalization.
		log.Printf("[HandleUpload] All chunks received, finalizing upload (streaming): file=%s identifier=%q", filename, uploadIdentifier)
		if uploadIdentifier == "" {
			// Without an identifier the finalize-outcome cache is disabled, so a
			// final-chunk retry that lands after cleanup cannot be answered and the
			// client will report "could not be confirmed". Surface it loudly.
			log.Printf("[HandleUpload] WARNING: chunked finalize has no resumableIdentifier; late-retry outcome cache is DISABLED for file=%s", filename)
		}
		upload.Touch()
		// Safety net: never leave waiters blocked. If finalization panics before
		// publishing, release them with a retryable error.
		finalizePublished := false
		defer func() {
			if !finalizePublished {
				upload.PublishFinalizeFailure(fmt.Errorf("upload finalization aborted"))
			}
		}()
		fileID, actualFilename, storageDeltaBytes, storageDeltaFiles, err := h.finalizeUploadStreaming(c, token, upload, parentDir, filename, storageKey, total, replaceFile)
		if err != nil {
			upload.PublishFinalizeFailure(err)
			finalizePublished = true
			h.handleChunkedFinalizeError(upload, err)
			log.Printf("[HandleUpload] Finalization failed: %v", err)
			writeSeafHTTPUploadError(c, err, "failed to finalize upload")
			return
		}
		// Publish the result before cleanup so any waiter holding this tracker
		// wakes with the real file id even after the tracker leaves the map.
		upload.PublishFinalizeSuccess(fileID, actualFilename, total)
		finalizePublished = true
		// Cache the outcome before removing the tracker so a final-chunk retry
		// arriving in the residual window (after cleanup) still gets the file id.
		chunkManager.CacheFinalizeOutcome(tokenStr, uploadIdentifier, parentDir, filename, fileID, actualFilename, total)
		chunkManager.CleanupTrackedUpload(upload)

		log.Printf("[HandleUpload] Upload complete: file=%s, size=%d, id=%s, cached=%t", actualFilename, total, fileID[:16], uploadIdentifier != "")

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

		writeFinalizeSuccess(fileID, actualFilename, total)
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
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check encryption status"})
		return
	}

	if encrypted {
		fileKey, fileIV := v2.GetDecryptSessions().GetFileKeyAndIV(token.UserID, token.RepoID)
		if fileKey == nil {
			writeLibraryEncryptedNotUnlocked(c)
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
	ctx := c.Request.Context()
	blockStore, actualStorageClass, err := h.resolveLibraryBlockStoreContext(ctx, httputil.GetRoutingHostname(c, h.configuredServerURL()), token.OrgID, token.RepoID)
	if err != nil {
		log.Printf("[HandleUpload] Failed to get org-scoped block store: %v", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "block storage not available"})
		return
	}
	materializedStorageClass := actualStorageClass
	materializedStorageKey := ""
	storeUploadedBlock := func() error {
		materializedStorageClass = actualStorageClass
		materializedStorageKey = ""
		probe, probeErr := probeUploadedBlockReuseForUploadFn(h.db, token.OrgID, sha256ID)
		if probeErr != nil {
			return fmt.Errorf("probe block reuse for %s: %w", sha256ID, probeErr)
		}
		probe, probeErr = prepareUploadedBlockProbeForUploadFn(h.db, token.OrgID, sha256ID, probe)
		if probeErr != nil {
			return probeErr
		}
		switch probe.Decision {
		case db.BlockReuseReusable:
			materializedStorageClass = probe.StorageClass
			var ensureErr error
			materializedStorageKey, ensureErr = ensureReusableBlockPresentForUploadFn(ctx, sha256ID, probe, storedContent, h.storageManager, blockStore, actualStorageClass, token.OrgID)
			if ensureErr != nil {
				return ensureErr
			}
			log.Printf("[HandleUpload] Reused canonical block %s (SHA-256: %s) after physical verification", fileID[:16], sha256ID[:16])
			return nil
		case db.BlockReuseNeedsPut:
			putStore, resolvedClass, resolvedKey, resolveErr := resolveNeedsPutBlockStoreForUploadFn(h.storageManager, blockStore, actualStorageClass, probe, token.OrgID, sha256ID)
			if resolveErr != nil {
				return resolveErr
			}
			materializedStorageClass = resolvedClass
			var putErr error
			materializedStorageKey, putErr = putUploadedBlockAutoDirectForUploadFn(ctx, putStore, resolvedKey, storedContent)
			if putErr == nil {
				log.Printf("[HandleUpload] Stored block %s (SHA-256: %s) via direct PUT", fileID[:16], sha256ID[:16])
			}
			return putErr
		case db.BlockReuseBlockedByGC:
			return v2.ErrBlockDeleteInProgress
		}
		return fmt.Errorf("unexpected block reuse decision %d for %s", probe.Decision, sha256ID)
	}

	// Register block metadata + a provisional reference (kept alive by TTL until
	// the fs_object commit creates the permanent reference), then write the
	// external SHA-1 mapping only after the block is durable in Cassandra.
	if err := retrySeafHTTPBlockMaterializationContext(c.Request.Context(), "HandleUpload", sha256ID, func() error {
		if putErr := storeUploadedBlock(); putErr != nil {
			return fmt.Errorf("failed to store block: %w", putErr)
		}
		return nil
	}, func() error {
		return registerUploadedBlockAndMappingForUploadFn(h.db, token.OrgID, token.RepoID, sha256ID, uploadOperationID, len(storedContent), materializedStorageClass, materializedStorageKey, fileID)
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
		log.Printf("[HandleUpload] Failed to update filesystem: %v", err)
		writeSeafHTTPUploadError(c, err, "file stored but metadata update failed")
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

var zipBatchResolveBlockIDsFn = streaming.BatchResolveBlockIDsContext

var cleanupChunkUploadFn = func(upload *ChunkUpload) {
	chunkManager.CleanupTrackedUpload(upload)
}

func (h *SeafHTTPHandler) handleChunkedFinalizeError(upload *ChunkUpload, err error) {
	// HeadConflict and quota_exceeded are both unrecoverable on the same
	// tracker: the client cannot finalize this session again (head moved past the
	// retry budget, or quota check will keep rejecting). Drop the tracker, but
	// leave every provisional up: reference to Cassandra TTL and Phase 0. A
	// block-mutation outcome that stayed unknown must also stop same-tracker
	// retries.
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
		cleanupChunkUploadFn(upload)
		return
	}
	upload.ResetFinalization()
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
	case errors.Is(err, v2.ErrLibraryEncryptedNotUnlocked):
		writeLibraryEncryptedNotUnlocked(c)
	case errors.Is(err, errStorageQuotaExceeded):
		c.JSON(http.StatusForbidden, gin.H{"error": "storage quota exceeded"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": genericMsg})
	}
}

// writeLibraryEncryptedNotUnlocked emits the app-wide 403 "needs decrypt" contract
// (matching v2 requireDecryptSession) so the frontend re-opens the repo password
// dialog instead of collapsing the response to a generic error. The resumable
// (streaming) upload only encrypts at finalize, so a decrypt session that expired
// or was never established surfaces here rather than on the upload-link GET.
func writeLibraryEncryptedNotUnlocked(c *gin.Context) {
	c.JSON(http.StatusForbidden, gin.H{
		"error":            "Library is encrypted",
		"error_msg":        "This library is encrypted. Please provide the password to unlock it.",
		"lib_need_decrypt": true,
	})
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

// retrySeafHTTPBlockMaterializationContext runs the bounded
// store -> materialize -> confirm cycle. A nil ctx makes the backoff wait
// uncancellable and routes it through seafHTTPBlockMaterializationSleepFn; every
// production caller passes a real request context, so only tests rely on that.
func retrySeafHTTPBlockMaterializationContext(ctx context.Context, label, blockID string, store func() error, materialize func() error, resolveFence func() (bool, error)) error {
	attempts := v2.RetryAttempts()
	if attempts < 1 {
		attempts = 1
	}

	// retryBlocked records the retry under a reason derived from the failing PHASE
	// (never the sentinel), so a materialize-phase write failure is never
	// attributed to the probe read path (finding F14). The reason labels match the
	// generic v2 wrapper's so all upload surfaces expose one label vocabulary.
	retryBlocked := func(attempt int, phaseReason string, retryErr error) error {
		reason := phaseReason
		if errors.Is(retryErr, v2.ErrBlockDeleteInProgress) {
			reason = "gc_fence"
		}
		metrics.BlockUploadMaterializationRetriesTotal.WithLabelValues(label, reason).Inc()
		if reason == "gc_fence" && resolveFence != nil {
			resolved, resolveErr := resolveFence()
			if resolveErr != nil {
				log.Printf("[%s] failed to inspect S3 orphan fence for block %s: %v", label, blockID, resolveErr)
			} else if resolved {
				return nil
			}
		}
		sleepFor := seafHTTPBlockMaterializationRetryBackoffFn(attempt)
		log.Printf("[%s] block %s materialization retry reason=%s (%d/%d) after %s", label, blockID, reason, attempt, attempts, sleepFor)
		if sleepFor > 0 {
			if ctx == nil {
				seafHTTPBlockMaterializationSleepFn(sleepFor)
			} else {
				timer := time.NewTimer(sleepFor)
				defer timer.Stop()
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-timer.C:
				}
			}
		}
		return nil
	}

	for attempt := 1; attempt <= attempts; attempt++ {
		if err := store(); err != nil {
			if !v2.IsRetryableBlockMaterializationError(err) || attempt == attempts {
				return err
			}
			if abortErr := retryBlocked(attempt, "probe", err); abortErr != nil {
				return abortErr
			}
			continue
		}
		if err := materialize(); err != nil {
			if !v2.IsRetryableBlockMaterializationError(err) || attempt == attempts {
				return err
			}
			if abortErr := retryBlocked(attempt, "materialization", err); abortErr != nil {
				return abortErr
			}
			continue
		}
		// Confirm physical presence after the provisional reference is durable.
		// This repairs a fast GC cycle whose fence cleared before materialization.
		if err := store(); err != nil {
			if !v2.IsRetryableBlockMaterializationError(err) || attempt == attempts {
				return err
			}
			if abortErr := retryBlocked(attempt, "probe", err); abortErr != nil {
				return abortErr
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
		return "", "", 0, 0, fmt.Errorf("failed to check encryption status: %w", err)
	}
	var fileKey, fileIV []byte
	if encrypted {
		fileKey, fileIV = v2.GetDecryptSessions().GetFileKeyAndIV(token.UserID, token.RepoID)
		if fileKey == nil {
			return "", "", 0, 0, v2.ErrLibraryEncryptedNotUnlocked
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
				materializedStorageClass := actualStorageClass
				materializedStorageKey := ""
				return retrySeafHTTPBlockMaterializationContext(egCtx, "finalizeUploadStreaming", sha256ID, func() error {
					materializedStorageClass = actualStorageClass
					materializedStorageKey = ""
					probe, probeErr := probeUploadedBlockReuseForUploadFn(h.db, token.OrgID, sha256ID)
					if probeErr != nil {
						return fmt.Errorf("probe block reuse for %s: %w", sha256ID, probeErr)
					}
					probe, probeErr = prepareUploadedBlockProbeForUploadFn(h.db, token.OrgID, sha256ID, probe)
					if probeErr != nil {
						return probeErr
					}
					switch probe.Decision {
					case db.BlockReuseReusable:
						materializedStorageClass = probe.StorageClass
						var ensureErr error
						materializedStorageKey, ensureErr = ensureReusableBlockPresentForUploadFn(egCtx, sha256ID, probe, storedBlock, h.storageManager, blockStore, actualStorageClass, token.OrgID)
						return ensureErr
					case db.BlockReuseNeedsPut:
						putStore, resolvedClass, resolvedKey, resolveErr := resolveNeedsPutBlockStoreForUploadFn(h.storageManager, blockStore, actualStorageClass, probe, token.OrgID, sha256ID)
						if resolveErr != nil {
							return resolveErr
						}
						materializedStorageClass = resolvedClass
						var putErr error
						materializedStorageKey, putErr = putUploadedBlockAutoDirectForUploadFn(egCtx, putStore, resolvedKey, storedBlock)
						if putErr != nil {
							return fmt.Errorf("failed to store block: %w", putErr)
						}
						return nil
					case db.BlockReuseBlockedByGC:
						return v2.ErrBlockDeleteInProgress
					}
					return fmt.Errorf("unexpected block reuse decision %d for %s", probe.Decision, sha256ID)
				}, func() error {
					// Cassandra materialization: serialized process-wide after the
					// S3 PUT so provisional refs + metadata/mapping writes do not
					// stampede Cassandra.
					releaseMetadataPermit, permitErr := acquireFinalizeUploadBlockMetadataPermit(egCtx)
					if permitErr != nil {
						return permitErr
					}
					defer releaseMetadataPermit()
					return registerUploadedBlockAndMappingForUploadFn(h.db, token.OrgID, token.RepoID, sha256ID, uploadOperationID, len(storedBlock), materializedStorageClass, materializedStorageKey, blockSHA1IDLocal)
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
	resolved, err := resolveSeafHTTPStoredBlockIDsFn(fsHelper, orgID, repoID, externalBlockIDs)
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
	// Canonical layout: block_ids holds the internal SHA-256 ids, seafile_block_ids_sha1
	// the external SHA-1 ids the desktop client needs (and which derive fs_id). Resolve
	// the POSITIONAL SHA-1 list (externalBlockIDs) to SHA-256 — not stagedBlockIDs, which
	// is deduped for references and would drop repeated blocks / reorder the file. The
	// SHA-1->SHA-256 mappings were written from real bytes during block upload.
	representationID, err := db.ResolveBlockRepresentationID(h.db.Session(), orgID, repoID)
	if err != nil {
		cleanupErr := cleanupSeafHTTPFailedPublishAttempt(h.db, orgID, repoID, attemptID, fsID, stagedBlockIDs)
		if cleanupErr != nil {
			return errors.Join(fmt.Errorf("failed to resolve block representation for fs_object: %w", err), cleanupErr)
		}
		return fmt.Errorf("failed to resolve block representation for fs_object: %w", err)
	}
	internalBlockIDs, err := streaming.BatchResolveBlockIDs(h.db, orgID, representationID, externalBlockIDs)
	if err != nil {
		cleanupErr := cleanupSeafHTTPFailedPublishAttempt(h.db, orgID, repoID, attemptID, fsID, stagedBlockIDs)
		if cleanupErr != nil {
			return errors.Join(fmt.Errorf("failed to resolve block ids for fs_object: %w", err), cleanupErr)
		}
		return fmt.Errorf("failed to resolve block ids for fs_object: %w", err)
	}
	if len(internalBlockIDs) != len(externalBlockIDs) {
		err := fmt.Errorf("resolved block id list length mismatch: internal=%d external=%d", len(internalBlockIDs), len(externalBlockIDs))
		cleanupErr := cleanupSeafHTTPFailedPublishAttempt(h.db, orgID, repoID, attemptID, fsID, stagedBlockIDs)
		if cleanupErr != nil {
			return errors.Join(err, cleanupErr)
		}
		return err
	}
	for i := range internalBlockIDs {
		if !isHexN(internalBlockIDs[i], 64) {
			err := fmt.Errorf("resolved internal block id %d is not SHA-256", i)
			cleanupErr := cleanupSeafHTTPFailedPublishAttempt(h.db, orgID, repoID, attemptID, fsID, stagedBlockIDs)
			if cleanupErr != nil {
				return errors.Join(err, cleanupErr)
			}
			return err
		}
		if !isHexN(externalBlockIDs[i], 40) {
			err := fmt.Errorf("external block id %d is not SHA-1", i)
			cleanupErr := cleanupSeafHTTPFailedPublishAttempt(h.db, orgID, repoID, attemptID, fsID, stagedBlockIDs)
			if cleanupErr != nil {
				return errors.Join(err, cleanupErr)
			}
			return err
		}
	}
	if err := h.db.Session().Query(`
		INSERT INTO fs_objects (library_id, fs_id, obj_type, obj_name, full_path, size_bytes, mtime, block_ids, seafile_block_ids_sha1)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, repoID, fsID, "file", filename, fullPath, fileSize, createdAt.Unix(), internalBlockIDs, externalBlockIDs).Exec(); err != nil {
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
// downloadPermissionChecker is the slice of the permission middleware the
// download gate needs. It exists so the gate's real decision logic can be driven
// against a revoked permission in a test: asserting only that the handlers call
// a hook, or that the source mentions the right method names, would still pass
// for an implementation that ignores what those methods return.
type downloadPermissionChecker interface {
	HasLibraryAccess(orgID, userID, repoID string, required middleware.LibraryPermission) (bool, error)
	RequirePermFlagForRepo(c *gin.Context, repoID string, flag string) bool
}

// authorizeSeafHTTPDownload is the ONE download gate, shared by the single-file
// and ZIP endpoints. They previously carried separate copies and drifted: ZIP
// checked read access but not the granular "download" flag, so after an admin
// revoked download while leaving read intact, /seafhttp/files/:token answered
// 403 while /seafhttp/zip/:token still handed over the whole directory with the
// same still-valid token (they live up to an hour). One gate, one contract.
//
// It writes the response and returns false when access is denied.
func authorizeSeafHTTPDownload(h *SeafHTTPHandler, c *gin.Context, token *AccessToken) bool {
	if h.permMiddleware == nil {
		return true
	}
	return authorizeDownloadWithChecker(h.permMiddleware, c, token)
}

func authorizeDownloadWithChecker(perms downloadPermissionChecker, c *gin.Context, token *AccessToken) bool {
	hasRead, err := perms.HasLibraryAccess(token.OrgID, token.UserID, token.RepoID, middleware.PermissionR)
	if err != nil {
		log.Printf("[seafhttp] Failed to check permissions repo=%s: %v", token.RepoID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check permissions"})
		return false
	}
	if !hasRead {
		c.JSON(http.StatusForbidden, gin.H{"error": "you do not have read access to this library"})
		return false
	}

	c.Set("org_id", token.OrgID)
	c.Set("user_id", token.UserID)
	if !perms.RequirePermFlagForRepo(c, token.RepoID, "download") {
		c.JSON(http.StatusForbidden, gin.H{"error": "download is not allowed by your permission"})
		return false
	}
	return true
}

var seafHTTPAuthorizeDownloadFn = authorizeSeafHTTPDownload

var recordSeafHTTPDownloadTrafficFn = func(status traffic.QuotaStatus, orgID, userID, trafficType string, bytes int64) {
	if rec := traffic.Get(); rec != nil {
		traffic.RecordCheckedTransfer(rec, status, orgID, userID, trafficType, bytes)
	}
}

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
	if token.Source == "link" && strings.TrimSpace(token.SourceID) == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid download token"})
		return
	}

	log.Printf("[HandleDownload] Token valid: OrgID=%s, RepoID=%s, Path=%s", token.OrgID, token.RepoID, token.Path)

	if !seafHTTPAuthorizeDownloadFn(h, c, token) {
		return
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
		}
	}

	// Block storage is the ONLY download path. The legacy <org>/<repo><path>
	// object fallback was removed with PR-6: it fired on ANY streaming failure,
	// including a Cassandra timeout, so on a library since written through blocks
	// it could answer 200 with an older version of the same path, and answered 404
	// when the object was simply absent. Production starts from empty buckets, so
	// no path-based object exists to read.
	if h.db == nil || h.storageManager == nil {
		log.Printf("[HandleDownload] Block storage not available (db=%v, storageManager=%v)", h.db != nil, h.storageManager != nil)
		respondSeafHTTPDownloadError(c, token.RepoID, token.Path, fmt.Errorf("storage not available"))
		return
	}

	lifecycle, ok := h.acquireDownloadAdmission(c, token, downloadadmission.ProfileFile)
	if !ok {
		return
	}
	defer lifecycle.FinishHandler()

	if err := h.streamFileFromBlocks(c, token, filename, downloadTrafficStatus.PeriodStartedAt, lifecycle); err != nil {
		// StartStreaming and StreamBlocks classify their own terminal failures;
		// preparation errors are classified here with the request context state.
		if !errors.Is(err, v2.ErrLibraryEncryptedNotUnlocked) {
			lifecycle.ReleasePreparationError(err)
		}
		respondSeafHTTPDownloadError(c, token.RepoID, token.Path, err)
		return
	}
}

// respondSeafHTTPDownloadError applies the fail-closed download contract. Metadata
// reads use LOCAL_QUORUM, so even a valid directory listing without an entry may be
// an older cross-DC snapshot. No local absence observation is strong enough to tell
// a sync client that the file was deleted; all metadata/read failures are retryable
// 503s unless they have a distinct non-retryable application contract.
//
// It writes nothing once the response has been committed, because streaming may
// have already sent headers and part of the body.
func respondSeafHTTPDownloadError(c *gin.Context, repoID, path string, err error) {
	if c.Writer.Written() {
		log.Printf("[HandleDownload] streaming aborted after headers repo=%s path=%s: %v", repoID, path, err)
		return
	}
	if errors.Is(err, v2.ErrLibraryEncryptedNotUnlocked) {
		// Not transient: retrying without a decrypt session can never succeed.
		// Emit the app-wide 403 { lib_need_decrypt: true } the frontend keys off.
		writeLibraryEncryptedNotUnlocked(c)
		return
	}
	log.Printf("[HandleDownload] download unavailable repo=%s path=%s: %v", repoID, path, err)
	c.Header("Retry-After", "1")
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": "file is temporarily unavailable; retry"})
}

// acquireDownloadAdmission starts the shared D4 lifecycle after the endpoint's
// authentication, quota, and storage-availability gates have passed.
func (h *SeafHTTPHandler) acquireDownloadAdmission(c *gin.Context, token *AccessToken, profile downloadadmission.Profile) (*httputil.DownloadAdmission, bool) {
	cfg := config.DownloadAdmissionConfig{}
	if h != nil && h.config != nil {
		cfg = h.config.DownloadAdmission
	}

	request := downloadadmission.AdmissionRequest{}
	if cfg.Enabled {
		var err error
		if token.Source == "link" {
			request, err = downloadadmission.NewPublicLinkRequest(profile, token.SourceID, c.ClientIP())
		} else {
			request, err = downloadadmission.NewAuthenticatedRequest(profile, token.OrgID, token.UserID)
		}
		if err != nil {
			respondSeafHTTPDownloadError(c, token.RepoID, token.Path, fmt.Errorf("build download admission request: %w", err))
			return nil, false
		}
	}

	lifecycle, reason, err := httputil.AcquireDownloadAdmission(c, h.downloadAdmission, cfg, request)
	if err != nil {
		respondSeafHTTPDownloadError(c, token.RepoID, token.Path, fmt.Errorf("acquire download admission: %w", err))
		return nil, false
	}
	if reason != "" {
		httputil.RenderDownloadAdmissionRefusal(c, h.downloadAdmission)
		return nil, false
	}
	return lifecycle, true
}

func releaseSeafHTTPDownloadStreamFailure(lifecycle *httputil.DownloadAdmission, err error) {
	if lifecycle == nil {
		return
	}
	lifecycle.FailStreamError(seafHTTPDownloadStreamCause(err))
}

func seafHTTPDownloadStreamCause(err error) downloadadmission.ReleaseCause {
	if errors.Is(err, streaming.ErrStreamResponse) {
		return downloadadmission.ReleaseResponseError
	}
	return downloadadmission.ReleaseStorageError
}

// resolveBlockID translates a client-facing block id to the internal SHA-256
// storage id inside the target library's representation namespace. Only hex
// SHA-1 or SHA-256 ids are accepted here; GC keeps its own lenient resolver.
func (h *SeafHTTPHandler) resolveBlockID(orgID, repoID, blockID string) (string, error) {
	classifiedID, err := classifyClientReadableBlockID(blockID)
	if err != nil {
		return "", err
	}
	if !classifiedID.isLegacySHA1 {
		return classifiedID.normalized, nil
	}
	representationID, err := h.resolveBlockRepresentationID(orgID, repoID)
	if err != nil {
		return "", err
	}
	mappedID, ok, err := h.db.GetBlockIDMapping(orgID, representationID, classifiedID.normalized)
	if err != nil {
		return "", err
	}
	if !ok || strings.TrimSpace(mappedID) == "" {
		return "", fmt.Errorf("block mapping not found for %s", blockID)
	}
	return normalizeResolvedInternalBlockID(mappedID)
}

// lookupFileBlocks resolves a token's path to its block IDs, file size, encryption key, and block store.
// This is the common metadata lookup used by both download and streaming paths.
func (h *SeafHTTPHandler) lookupFileBlocks(hostname string, token *AccessToken) (blockIDs []string, fileSize int64, fileKey []byte, fileIV []byte, blockStore *storage.BlockStore, storageClass string, err error) {
	return h.lookupFileBlocksContext(context.Background(), hostname, token)
}

func (h *SeafHTTPHandler) lookupFileBlocksContext(ctx context.Context, hostname string, token *AccessToken) (blockIDs []string, fileSize int64, fileKey []byte, fileIV []byte, blockStore *storage.BlockStore, storageClass string, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	// Check encryption. A read failure must NOT fall through as "not encrypted".
	encrypted, err := seafHTTPLookupLibraryEncryptedFn(ctx, h, token.OrgID, token.RepoID)
	if err != nil {
		return nil, 0, nil, nil, nil, "", fmt.Errorf("failed to check library encryption: %w", err)
	}

	if encrypted {
		fileKey, fileIV = v2.GetDecryptSessions().GetFileKeyAndIVContext(ctx, token.UserID, token.RepoID)
		if fileKey == nil {
			// Must stay a distinct sentinel: this is not a transient failure, and
			// answering 503 would make the client retry forever instead of
			// prompting for the password.
			return nil, 0, nil, nil, nil, "", v2.ErrLibraryEncryptedNotUnlocked
		}
	}

	// Get head commit → root FS
	var headCommit string
	err = h.db.Session().Query(`
		SELECT head_commit_id FROM libraries WHERE org_id = ? AND library_id = ?
	`, token.OrgID, token.RepoID).WithContext(ctx).Scan(&headCommit)
	if err != nil {
		return nil, 0, nil, nil, nil, "", fmt.Errorf("library not found: %w", err)
	}

	var rootFSID string
	err = h.db.Session().Query(`
		SELECT root_fs_id FROM commits WHERE library_id = ? AND commit_id = ?
	`, token.RepoID, headCommit).WithContext(ctx).Scan(&rootFSID)
	if err != nil {
		return nil, 0, nil, nil, nil, "", fmt.Errorf("commit not found: %w", err)
	}

	// Navigate directory tree to the target file
	filePath := token.Path
	if !strings.HasPrefix(filePath, "/") {
		filePath = "/" + filePath
	}
	pathParts := strings.Split(strings.Trim(filePath, "/"), "/")
	if len(pathParts) == 0 || (len(pathParts) == 1 && pathParts[0] == "") {
		return nil, 0, nil, nil, nil, "", fmt.Errorf("invalid file path")
	}

	currentFSID := rootFSID
	for i := 0; i < len(pathParts)-1; i++ {
		entry, err := h.findValidatedEntryInDirContext(ctx, token.RepoID, currentFSID, pathParts[i])
		if err != nil {
			return nil, 0, nil, nil, nil, "", fmt.Errorf("directory not found: %s: %w", pathParts[i], err)
		}
		if !entry.isDir() {
			return nil, 0, nil, nil, nil, "", fmt.Errorf("path component %s is not a directory", pathParts[i])
		}
		currentFSID = entry.id
	}

	targetName := pathParts[len(pathParts)-1]
	targetEntry, err := h.findValidatedEntryInDirContext(ctx, token.RepoID, currentFSID, targetName)
	if err != nil {
		return nil, 0, nil, nil, nil, "", fmt.Errorf("file not found: %s: %w", targetName, err)
	}
	if targetEntry.isDir() {
		return nil, 0, nil, nil, nil, "", fmt.Errorf("target %s is not a regular file", targetName)
	}
	fileFSID := targetEntry.id

	// Get block IDs and file size from fs_object. The dirent mode and target row
	// must agree; otherwise corrupt metadata could turn a directory into an empty
	// file response.
	var objType string
	err = h.db.Session().Query(`
		SELECT obj_type, block_ids, size_bytes FROM fs_objects WHERE library_id = ? AND fs_id = ?
	`, token.RepoID, fileFSID).WithContext(ctx).Scan(&objType, &blockIDs, &fileSize)
	if err != nil {
		return nil, 0, nil, nil, nil, "", fmt.Errorf("file metadata not found: %w", err)
	}
	if objType != "file" {
		return nil, 0, nil, nil, nil, "", fmt.Errorf("file metadata has unexpected object type %q", objType)
	}

	blockStore, storageClass, err = h.resolveLibraryBlockStoreContext(ctx, hostname, token.OrgID, token.RepoID)
	if err != nil {
		return nil, 0, nil, nil, nil, "", fmt.Errorf("block store not available: %w", err)
	}

	return blockIDs, fileSize, fileKey, fileIV, blockStore, storageClass, nil
}

// streamFileFromBlocks streams a file's blocks directly to the HTTP response.
// Uses prefetching (overlap S3 fetch with HTTP write) and 4MB io.CopyBuffer
// for maximum throughput. Only O(2 × block_size) RAM.
func (h *SeafHTTPHandler) streamFileFromBlocks(c *gin.Context, token *AccessToken, filename string, periodStartedAt time.Time, lifecycle *httputil.DownloadAdmission) error {
	preparationCtx := c.Request.Context()
	blockIDs, fileSize, fileKey, fileIV, blockStore, storageClass, err := seafHTTPLookupFileBlocksFn(preparationCtx, h, httputil.GetRoutingHostname(c, h.configuredServerURL()), token)
	if err != nil {
		return err
	}

	downloadTrafficStatus := traffic.QuotaStatus{Allowed: true, PeriodStartedAt: periodStartedAt}
	quotaWarning := ""
	if checker := traffic.GetChecker(); checker != nil {
		downloadTrafficStatus, _ = checker.CheckTrafficQuotaContext(preparationCtx, token.OrgID, token.UserID, "download", fileSize)
		if !downloadTrafficStatus.Allowed {
			c.JSON(http.StatusForbidden, traffic.TrafficQuotaExceededResponse(downloadTrafficStatus, "traffic quota exceeded", true))
			return nil
		}
		if warning, ok := traffic.TrafficQuotaWarningHeader(downloadTrafficStatus); ok {
			quotaWarning = warning
		}
	}

	log.Printf("[streamFileFromBlocks] Streaming %d blocks, size=%d, encrypted=%v", len(blockIDs), fileSize, fileKey != nil)
	representationID, err := seafHTTPResolveBlockRepresentationIDFn(preparationCtx, h, token.OrgID, token.RepoID)
	if err != nil {
		return fmt.Errorf("resolve block representation: %w", err)
	}

	// Batch resolve all block IDs upfront (avoids per-block Cassandra queries).
	// Strict: a stale SHA-1 sent to SHA-256 storage would truncate the stream
	// mid-download, so we resolve BEFORE committing any headers and fail clean.
	resolvedIDs, err := seafHTTPBatchResolveBlockIDsFn(preparationCtx, h.db, token.OrgID, representationID, blockIDs)
	if err != nil {
		log.Printf("[streamFileFromBlocks] block ID resolution failed for org=%s: %v", token.OrgID, err)
		return fmt.Errorf("resolve block IDs: %w", err)
	}
	canonicalReader, err := seafHTTPNewCanonicalBlockReaderFn(preparationCtx, h.db, h.storageManager, token.OrgID, resolvedIDs, blockStore, storageClass)
	if err != nil {
		return fmt.Errorf("resolve canonical block locations: %w", err)
	}

	streamCtx := c.Request.Context()
	if lifecycle != nil {
		streamCtx, err = lifecycle.StartStreaming()
		if err != nil {
			return fmt.Errorf("start download stream: %w", err)
		}
	}

	// Set headers before streaming — Content-Length lets clients show progress
	if quotaWarning != "" {
		c.Header("X-Quota-Warning", quotaWarning)
	}
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Header("Content-Type", "application/octet-stream")
	if fileSize > 0 {
		// fs_objects.size_bytes is always the plaintext byte count — even for
		// encrypted libraries — so the emitted stream length equals fileSize
		// after decryption. Exposing this header lets clients show progress.
		c.Header("Content-Length", strconv.FormatInt(fileSize, 10))
	}
	c.Status(http.StatusOK)

	// Stream with prefetching pipeline. Count bytes even when the stream fails
	// after a partial response has already reached the client.
	bytesBefore := int64(c.Writer.Size())
	streamErr := streaming.StreamBlocks(c, streamCtx, canonicalReader, resolvedIDs, fileKey, fileIV, "streamFileFromBlocks")
	bytesWritten := httputil.ResponseBytesSince(c.Writer, bytesBefore)
	if bytesWritten > 0 {
		tt := traffic.WebDownload
		if token.Source == "link" {
			tt = traffic.LinkDownload
		}
		recordSeafHTTPDownloadTrafficFn(downloadTrafficStatus, token.OrgID, token.UserID, tt, bytesWritten)
	}
	if streamErr != nil {
		releaseSeafHTTPDownloadStreamFailure(lifecycle, streamErr)
		return streamErr
	}

	log.Printf("[streamFileFromBlocks] Streaming complete: %d blocks", len(blockIDs))

	return nil
}

// getFileFromBlocks retrieves a file by loading all blocks into memory.
// Deprecated and intentionally unused: download producers must use
// streamFileFromBlocks so D admission and bounded streaming cannot be bypassed.
func (h *SeafHTTPHandler) getFileFromBlocks(c *gin.Context, token *AccessToken) ([]byte, error) {
	blockIDs, _, fileKey, fileIV, blockStore, storageClass, err := seafHTTPLookupFileBlocksFn(c.Request.Context(), h, httputil.GetRoutingHostname(c, h.configuredServerURL()), token)
	if err != nil {
		return nil, err
	}

	ctx := c.Request.Context()
	representationID, err := seafHTTPResolveBlockRepresentationIDFn(ctx, h, token.OrgID, token.RepoID)
	if err != nil {
		return nil, fmt.Errorf("resolve block representation: %w", err)
	}
	resolvedIDs, err := seafHTTPBatchResolveBlockIDsFn(ctx, h.db, token.OrgID, representationID, blockIDs)
	if err != nil {
		return nil, fmt.Errorf("resolve block IDs: %w", err)
	}
	canonicalReader, err := seafHTTPNewCanonicalBlockReaderFn(ctx, h.db, h.storageManager, token.OrgID, resolvedIDs, blockStore, storageClass)
	if err != nil {
		return nil, fmt.Errorf("resolve canonical block locations: %w", err)
	}

	var content bytes.Buffer
	for i, blockID := range blockIDs {
		blockData, err := canonicalReader.GetBlock(ctx, resolvedIDs[i])
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

// errDirEntryAbsent distinguishes a validated listing without the requested name
// from malformed metadata. It is still NOT proof of global absence: LOCAL_QUORUM
// may return an older cross-DC snapshot, so the HTTP classifier maps it to 503.
var errDirEntryAbsent = errors.New("directory entry absent")

// validatedDirEntry is one directory entry that passed full validation.
type validatedDirEntry struct {
	name string
	id   string
	mode int
}

func (e validatedDirEntry) isDir() bool {
	return e.mode&0170000 == 040000
}

func validateDirEntryName(name string) error {
	if name == "" {
		return fmt.Errorf("directory entry has no usable name")
	}
	if name == "." || name == ".." {
		return fmt.Errorf("directory entry name %q is not a safe path component", name)
	}
	if strings.ContainsAny(name, `/\\`) || strings.IndexByte(name, 0) >= 0 {
		return fmt.Errorf("directory entry name %q is not a safe path component", name)
	}
	return nil
}

// parseValidatedDirEntry validates one entry, rejecting anything that could
// resolve to the wrong FS object. encoding/json silently keeps the LAST value
// for a repeated key, so {"id":"A","id":"B"} would resolve to B and
// {"name":"a","name":"b"} would hide "a" entirely; both are caught here by
// walking the object's tokens instead of unmarshalling into a map.
func parseValidatedDirEntry(raw json.RawMessage) (validatedDirEntry, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil {
		return validatedDirEntry{}, fmt.Errorf("unreadable directory entry: %w", err)
	}
	if delim, ok := token.(json.Delim); !ok || delim != '{' {
		return validatedDirEntry{}, fmt.Errorf("directory entry is not a JSON object")
	}

	seen := make(map[string]struct{}, 4)
	var entry validatedDirEntry
	var nameFound, idFound, modeFound bool
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return validatedDirEntry{}, fmt.Errorf("unreadable directory entry key: %w", err)
		}
		key, ok := keyToken.(string)
		if !ok {
			return validatedDirEntry{}, fmt.Errorf("directory entry key is not a string")
		}
		if _, duplicate := seen[key]; duplicate {
			return validatedDirEntry{}, fmt.Errorf("directory entry repeats key %q", key)
		}
		seen[key] = struct{}{}

		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return validatedDirEntry{}, fmt.Errorf("unreadable value for directory entry key %q: %w", key, err)
		}
		switch key {
		case "name":
			if err := json.Unmarshal(value, &entry.name); err != nil {
				return validatedDirEntry{}, fmt.Errorf("directory entry name is not a string: %w", err)
			}
			nameFound = true
		case "id":
			if err := json.Unmarshal(value, &entry.id); err != nil {
				return validatedDirEntry{}, fmt.Errorf("directory entry id is not a string: %w", err)
			}
			idFound = true
		case "mode":
			modeDecoder := json.NewDecoder(bytes.NewReader(value))
			modeDecoder.UseNumber()
			var modeValue interface{}
			if err := modeDecoder.Decode(&modeValue); err != nil {
				return validatedDirEntry{}, fmt.Errorf("directory entry mode is not an integer: %w", err)
			}
			modeNumber, ok := modeValue.(json.Number)
			if !ok {
				return validatedDirEntry{}, fmt.Errorf("directory entry mode is not a number")
			}
			mode, err := modeNumber.Int64()
			if err != nil {
				return validatedDirEntry{}, fmt.Errorf("directory entry mode is not an integer: %w", err)
			}
			if mode < 0 || mode > 0177777 {
				return validatedDirEntry{}, fmt.Errorf("directory entry mode %d is out of range", mode)
			}
			modeType := mode & 0170000
			if modeType != 0100000 && modeType != 040000 {
				return validatedDirEntry{}, fmt.Errorf("directory entry mode %d is neither a regular file nor a directory", mode)
			}
			entry.mode = int(mode)
			modeFound = true
		}
	}

	if !nameFound {
		return validatedDirEntry{}, fmt.Errorf("directory entry has no name")
	}
	if err := validateDirEntryName(entry.name); err != nil {
		return validatedDirEntry{}, err
	}
	if !idFound {
		return validatedDirEntry{}, fmt.Errorf("directory entry %q has no id", entry.name)
	}
	if len(entry.id) != 40 || !isHexString([]byte(entry.id)) {
		return validatedDirEntry{}, fmt.Errorf("directory entry %q has a non-40-hex id", entry.name)
	}
	if !modeFound {
		return validatedDirEntry{}, fmt.Errorf("directory entry %q has no mode", entry.name)
	}
	return entry, nil
}

// parseValidatedDirEntries validates an ENTIRE dir_entries payload, or fails.
//
// It is all-or-nothing on purpose. An earlier revision returned a valid match
// even when a sibling was malformed, so that one bad dirent could not make a
// healthy file unreadable. That exception was wrong: a corrupt entry may carry
// the very name being resolved (or hide it behind a repeated "name" key), in
// which case the listing is ambiguous about the requested path and returning
// the other copy serves the wrong FS object. A corrupt dirent is an anomaly that
// should be investigated, and 503 is retryable and destroys nothing, so every
// consumer now fails closed on the whole listing.
func parseValidatedDirEntries(rawEntries string) ([]validatedDirEntry, error) {
	trimmed := strings.TrimSpace(rawEntries)
	if trimmed == "" || trimmed == "null" {
		return nil, fmt.Errorf("directory listing is blank")
	}

	var raws []json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &raws); err != nil {
		return nil, fmt.Errorf("malformed directory listing: %w", err)
	}

	entries := make([]validatedDirEntry, 0, len(raws))
	seenNames := make(map[string]struct{}, len(raws))
	for i, raw := range raws {
		entry, err := parseValidatedDirEntry(raw)
		if err != nil {
			return nil, fmt.Errorf("directory entry %d: %w", i, err)
		}
		if _, duplicate := seenNames[entry.name]; duplicate {
			return nil, fmt.Errorf("directory lists %q more than once", entry.name)
		}
		seenNames[entry.name] = struct{}{}
		entries = append(entries, entry)
	}
	return entries, nil
}

// findValidatedDirEntry resolves entryName against a raw dir_entries payload.
// The internal absence sentinel is produced ONLY when the whole listing validated
// and no entry matched. This is the single parse-and-match path: the production
// lookup and the corrupt-listing test matrix both go through it, so a change to
// the matching rule cannot keep the tests green while breaking the real caller.
func findValidatedDirEntry(rawEntries, entryName string) (validatedDirEntry, error) {
	entries, err := parseValidatedDirEntries(rawEntries)
	if err != nil {
		return validatedDirEntry{}, err
	}
	for _, entry := range entries {
		if entry.name == entryName {
			return entry, nil
		}
	}
	return validatedDirEntry{}, fmt.Errorf("%w: %s", errDirEntryAbsent, entryName)
}

// findValidatedEntryInDir resolves entryName inside a directory FS object while
// preserving its validated mode for callers that must enforce file/directory type.
//
// A read failure is NOT absence: a bare gocql.ErrNotFound on the directory row
// means the dirent that named it points at a row that is missing — premature GC,
// a partial write, cross-DC lag — which is dangling metadata, not proof the path
// is gone. A validated listing miss returns errDirEntryAbsent, which remains a
// retryable 503 at the HTTP boundary.
func (h *SeafHTTPHandler) findValidatedEntryInDir(repoID, dirFSID, entryName string) (validatedDirEntry, error) {
	return h.findValidatedEntryInDirContext(context.Background(), repoID, dirFSID, entryName)
}

func (h *SeafHTTPHandler) findValidatedEntryInDirContext(ctx context.Context, repoID, dirFSID, entryName string) (validatedDirEntry, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var objType, dirEntries string
	if err := h.db.Session().Query(`
		SELECT obj_type, dir_entries FROM fs_objects
		WHERE library_id = ? AND fs_id = ?
	`, repoID, dirFSID).WithContext(ctx).Scan(&objType, &dirEntries); err != nil {
		return validatedDirEntry{}, fmt.Errorf("failed to read directory %s: %w", dirFSID, err)
	}
	if objType != "dir" {
		return validatedDirEntry{}, fmt.Errorf("directory %s has unexpected object type %q", dirFSID, objType)
	}

	entry, err := findValidatedDirEntry(dirEntries, entryName)
	if err != nil {
		if errors.Is(err, errDirEntryAbsent) {
			return validatedDirEntry{}, err
		}
		return validatedDirEntry{}, fmt.Errorf("directory %s: %w", dirFSID, err)
	}
	return entry, nil
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
	if token.Source == "link" && strings.TrimSpace(token.SourceID) == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid download token"})
		return
	}

	if !seafHTTPAuthorizeDownloadFn(h, c, token) {
		return
	}

	if h.db == nil || h.storageManager == nil {
		respondSeafHTTPDownloadError(c, token.RepoID, token.Path, fmt.Errorf("storage not available"))
		return
	}

	// Quota pre-check — reject early if traffic quota is already exhausted.
	zipTrafficStatus := traffic.QuotaStatus{Allowed: true}
	zipQuotaWarning := ""
	if checker := traffic.GetChecker(); checker != nil {
		zipTrafficStatus, _ = traffic.CheckTrafficQuotaWithChecker(checker, token.OrgID, token.UserID, "download", 0)
		if !zipTrafficStatus.Allowed {
			c.JSON(http.StatusForbidden, traffic.TrafficQuotaExceededResponse(zipTrafficStatus, "traffic quota exceeded", true))
			return
		}
		if warning, ok := traffic.TrafficQuotaWarningHeader(zipTrafficStatus); ok {
			zipQuotaWarning = warning
		}
	}

	lifecycle, ok := h.acquireDownloadAdmission(c, token, downloadadmission.ProfileZIP)
	if !ok {
		return
	}
	var zipWriter *zip.Writer
	zipCause := downloadadmission.ReleaseCompleted
	zipAccounting := &zipTrafficAccounting{
		context:     c,
		quotaStatus: zipTrafficStatus,
		orgID:       token.OrgID,
		userID:      token.UserID,
		trafficType: traffic.WebDownload,
	}
	if token.Source == "link" {
		zipAccounting.trafficType = traffic.LinkDownload
	}
	defer finishSeafHTTPZipDownload(lifecycle, &zipWriter, &zipCause, zipAccounting)
	preparationCtx := lifecycle.PreparationContext()

	// Encryption must be checked before any metadata walk or stream. Ignoring a
	// Cassandra error here previously left encrypted=false and produced a 200 zip
	// of ciphertext bytes treated as plaintext.
	encrypted, err := seafHTTPLookupLibraryEncryptedFn(preparationCtx, h, token.OrgID, token.RepoID)
	if err != nil {
		lifecycle.ReleasePreparationError(err)
		respondSeafHTTPDownloadError(c, token.RepoID, token.Path, fmt.Errorf("failed to check library encryption: %w", err))
		return
	}
	var fileKey []byte
	var fileIV []byte
	if encrypted {
		fileKey, fileIV = v2.GetDecryptSessions().GetFileKeyAndIVContext(preparationCtx, token.UserID, token.RepoID)
		if fileKey == nil {
			respondSeafHTTPDownloadError(c, token.RepoID, token.Path, v2.ErrLibraryEncryptedNotUnlocked)
			return
		}
	}

	// Get the library's root FS. Cross-DC lag can make a fresh download token
	// observe a missing library/commit row; that is retryable 503, not 500.
	var headCommit string
	err = h.db.Session().Query(`
		SELECT head_commit_id FROM libraries
		WHERE org_id = ? AND library_id = ?
	`, token.OrgID, token.RepoID).WithContext(preparationCtx).Scan(&headCommit)
	if err != nil {
		lifecycle.ReleasePreparationError(err)
		respondSeafHTTPDownloadError(c, token.RepoID, token.Path, fmt.Errorf("library not found: %w", err))
		return
	}

	var rootFSID string
	err = h.db.Session().Query(`
		SELECT root_fs_id FROM commits
		WHERE library_id = ? AND commit_id = ?
	`, token.RepoID, headCommit).WithContext(preparationCtx).Scan(&rootFSID)
	if err != nil {
		lifecycle.ReleasePreparationError(err)
		respondSeafHTTPDownloadError(c, token.RepoID, token.Path, fmt.Errorf("commit not found: %w", err))
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
		`, token.OrgID, token.RepoID).WithContext(preparationCtx).Scan(&libraryName)
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
			entry, err := h.findValidatedEntryInDirContext(preparationCtx, token.RepoID, currentFSID, part)
			if err != nil {
				// Same contract as HandleDownload: only a validated listing that
				// does not name the entry distinguishes local absence; HTTP still
				// maps it to 503 because the snapshot may be stale across DCs.
				lifecycle.ReleasePreparationError(err)
				respondSeafHTTPDownloadError(c, token.RepoID, part, err)
				return
			}
			if !entry.isDir() {
				lifecycle.ReleasePreparationError(fmt.Errorf("path component %s is not a directory", part))
				respondSeafHTTPDownloadError(c, token.RepoID, part, fmt.Errorf("path component %s is not a directory", part))
				return
			}
			currentFSID = entry.id
		}
		targetFSID = currentFSID
	}

	// Fallback if dirName is still empty
	if dirName == "" {
		dirName = "download"
	}

	hostname := httputil.GetRoutingHostname(c, h.configuredServerURL())
	blockStore, storageClass, err := h.resolveLibraryBlockStoreContext(preparationCtx, hostname, token.OrgID, token.RepoID)
	if err != nil {
		log.Printf("[HandleZipDownload] Failed to resolve block store: %v", err)
		lifecycle.ReleasePreparationError(err)
		respondSeafHTTPDownloadError(c, token.RepoID, token.Path, fmt.Errorf("block store not available: %w", err))
		return
	}

	preflightBudget := h.newZipTraversalBudget()
	preparedFiles, err := h.prepareZipDirectoryContext(preparationCtx, token.RepoID, token.OrgID, targetFSID, "", 0, preflightBudget)
	if err != nil {
		if isZipLimitError(err) {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": err.Error()})
			return
		}
		log.Printf("[HandleZipDownload] Failed to validate ZIP directory: %v", err)
		lifecycle.ReleasePreparationError(err)
		respondSeafHTTPDownloadError(c, token.RepoID, token.Path, err)
		return
	}
	allResolvedIDs := make([]string, 0)
	for _, file := range preparedFiles {
		allResolvedIDs = append(allResolvedIDs, file.resolvedIDs...)
	}
	canonicalReader, err := seafHTTPNewCanonicalBlockReaderFn(preparationCtx, h.db, h.storageManager, token.OrgID, allResolvedIDs, blockStore, storageClass)
	if err != nil {
		log.Printf("[HandleZipDownload] Failed to resolve canonical block locations: %v", err)
		lifecycle.ReleasePreparationError(err)
		respondSeafHTTPDownloadError(c, token.RepoID, token.Path, fmt.Errorf("resolve canonical block locations: %w", err))
		return
	}

	streamCtx, err := lifecycle.StartStreaming()
	if err != nil {
		respondSeafHTTPDownloadError(c, token.RepoID, token.Path, fmt.Errorf("start zip stream: %w", err))
		return
	}

	// Stream ZIP to response
	zipFilename := dirName + ".zip"
	if zipQuotaWarning != "" {
		c.Header("X-Quota-Warning", zipQuotaWarning)
	}
	c.Header("Content-Type", "application/zip")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, zipFilename))
	c.Status(http.StatusOK)

	zipWriter = zip.NewWriter(c.Writer)
	zipAccounting.bytesBefore = int64(c.Writer.Size())
	zipAccounting.active = true

	if err := h.addDirToZip(streamCtx, zipWriter, canonicalReader, preparedFiles, fileKey, fileIV); err != nil {
		zipCause = seafHTTPDownloadStreamCause(err)
		lifecycle.FailStreamError(zipCause)
		log.Printf("[HandleZipDownload] ZIP stream aborted: %v", err)
		return
	}

}

func (h *SeafHTTPHandler) prepareZipDirectory(repoID, orgID, dirFSID, prefix string, depth int, budget *zipTraversalBudget) ([]zipPreparedFile, error) {
	return h.prepareZipDirectoryContext(context.Background(), repoID, orgID, dirFSID, prefix, depth, budget)
}

func (h *SeafHTTPHandler) prepareZipDirectoryContext(ctx context.Context, repoID, orgID, dirFSID, prefix string, depth int, budget *zipTraversalBudget) ([]zipPreparedFile, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if budget != nil {
		if err := budget.noteDirectory(depth); err != nil {
			return nil, err
		}
	}

	var objType, dirEntriesJSON string
	if err := h.db.Session().Query(`
		SELECT obj_type, dir_entries FROM fs_objects WHERE library_id = ? AND fs_id = ?
	`, repoID, dirFSID).WithContext(ctx).Scan(&objType, &dirEntriesJSON); err != nil {
		return nil, fmt.Errorf("failed to read directory %s: %w", dirFSID, err)
	}
	if objType != "dir" {
		return nil, fmt.Errorf("directory %s has unexpected object type %q", dirFSID, objType)
	}
	if strings.TrimSpace(dirEntriesJSON) == "[]" {
		return nil, nil
	}

	// The zip walk validates exactly like the download path. It used to parse
	// into a map — keeping the last value of a repeated key — and silently skip
	// entries without a name or id, so a corrupt listing could produce a 200 zip
	// with wrong or missing content instead of an error.
	entries, err := parseValidatedDirEntries(dirEntriesJSON)
	if err != nil {
		return nil, fmt.Errorf("directory %s: %w", dirFSID, err)
	}

	prepared := make([]zipPreparedFile, 0, len(entries))
	for _, entry := range entries {
		name, id := entry.name, entry.id

		entryPath := name
		if prefix != "" {
			entryPath = prefix + "/" + name
		}

		if entry.isDir() {
			childFiles, err := h.prepareZipDirectoryContext(ctx, repoID, orgID, id, entryPath, depth+1, budget)
			if err != nil {
				return nil, err
			}
			prepared = append(prepared, childFiles...)
			continue
		}

		var fileObjType string
		var blockIDs []string
		var fileSize int64
		err := h.db.Session().Query(`
			SELECT obj_type, block_ids, size_bytes FROM fs_objects WHERE library_id = ? AND fs_id = ?
		`, repoID, id).WithContext(ctx).Scan(&fileObjType, &blockIDs, &fileSize)
		if err != nil {
			return nil, fmt.Errorf("load blocks for %s: %w", entryPath, err)
		}
		if fileObjType != "file" {
			return nil, fmt.Errorf("file %s has unexpected object type %q", entryPath, fileObjType)
		}
		if budget != nil {
			if err := budget.noteFile(fileSize); err != nil {
				return nil, err
			}
		}

		representationID, err := db.ResolveBlockRepresentationIDContext(ctx, h.db.Session(), orgID, repoID)
		if err != nil {
			return nil, fmt.Errorf("resolve block representation for %s: %w", entryPath, err)
		}
		resolvedIDs, err := zipBatchResolveBlockIDsFn(ctx, h.db, orgID, representationID, blockIDs)
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

// finishSeafHTTPZipDownload closes the archive while its admission lease is
// still active. It preserves a handler panic while also releasing the lease if
// Close itself panics.
func finishSeafHTTPZipDownload(lifecycle *httputil.DownloadAdmission, zipWriter **zip.Writer, cause *downloadadmission.ReleaseCause, accounting *zipTrafficAccounting) {
	originalPanic := recover()
	finalCause := downloadadmission.ReleaseCompleted
	if cause != nil {
		finalCause = *cause
	}
	if originalPanic != nil {
		finalCause = downloadadmission.ReleasePanic
	}
	claimSeafHTTPZipCause(lifecycle, finalCause)

	var closeErr error
	var closePanic any
	if zipWriter != nil && *zipWriter != nil {
		func() {
			defer func() { closePanic = recover() }()
			closeErr = (*zipWriter).Close()
		}()
	}
	if closeErr != nil {
		if finalCause == downloadadmission.ReleaseCompleted {
			finalCause = downloadadmission.ReleaseResponseError
		}
		log.Printf("[HandleZipDownload] ZIP close failed: %v", closeErr)
	}
	if closePanic != nil {
		finalCause = downloadadmission.ReleasePanic
		log.Printf("[HandleZipDownload] ZIP close panicked: %v", closePanic)
	}
	claimSeafHTTPZipCause(lifecycle, finalCause)
	var accountingPanic any
	if accounting != nil && accounting.active {
		bytesWritten := httputil.ResponseBytesSince(accounting.context.Writer, accounting.bytesBefore)
		if bytesWritten > 0 {
			func() {
				defer func() { accountingPanic = recover() }()
				recordSeafHTTPDownloadTrafficFn(
					accounting.quotaStatus,
					accounting.orgID,
					accounting.userID,
					accounting.trafficType,
					bytesWritten,
				)
			}()
		}
	}
	if accountingPanic != nil && finalCause == downloadadmission.ReleaseCompleted {
		finalCause = downloadadmission.ReleasePanic
		claimSeafHTTPZipCause(lifecycle, finalCause)
	}
	_ = lifecycle.Finish(finalCause)
	if originalPanic != nil {
		panic(originalPanic)
	}
	if closePanic != nil {
		panic(closePanic)
	}
	if accountingPanic != nil {
		panic(accountingPanic)
	}
}

func claimSeafHTTPZipCause(lifecycle *httputil.DownloadAdmission, cause downloadadmission.ReleaseCause) {
	if cause == downloadadmission.ReleaseCompleted {
		return
	}
	if cause == downloadadmission.ReleasePanic {
		lifecycle.Fail(cause)
		return
	}
	lifecycle.FailStreamError(cause)
}

func (h *SeafHTTPHandler) addDirToZip(ctx context.Context, zw *zip.Writer, blockStore streaming.BlockReader, files []zipPreparedFile, fileKey []byte, fileIV []byte) error {
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
func (h *SeafHTTPHandler) addFileToZip(ctx context.Context, zw *zip.Writer, blockStore streaming.BlockReader, file zipPreparedFile, fileKey []byte, fileIV []byte) error {
	header := &zip.FileHeader{
		Name:   file.path,
		Method: zip.Store,
	}
	if file.sizeBytes > 0 {
		header.UncompressedSize64 = uint64(file.sizeBytes)
	}
	w, err := zw.CreateHeader(header)
	if err != nil {
		return fmt.Errorf("%w: create zip entry %s: %w", streaming.ErrStreamResponse, file.path, err)
	}

	buf := streaming.GetCopyBuf()
	defer streaming.PutCopyBuf(buf)

	for i, blockID := range file.blockIDs {
		internalID := file.resolvedIDs[i]
		_ = blockID

		if fileKey != nil {
			blockData, err := blockStore.GetBlock(ctx, internalID)
			if err != nil {
				return fmt.Errorf("%w: get block %s for %s: %w", streaming.ErrStreamStorage, file.blockIDs[i], file.path, err)
			}
			decrypted, err := crypto.DecryptLibraryBlock(blockData, fileKey, fileIV)
			if err != nil {
				return fmt.Errorf("%w: decrypt block for %s: %w", streaming.ErrStreamStorage, file.path, err)
			}
			if _, err := w.Write(decrypted); err != nil {
				return fmt.Errorf("%w: write decrypted block for %s: %w", streaming.ErrStreamResponse, file.path, err)
			}
			continue
		}

		reader, err := blockStore.GetBlockReader(ctx, internalID)
		if err != nil {
			return fmt.Errorf("%w: get block reader %s for %s: %w", streaming.ErrStreamStorage, file.blockIDs[i], file.path, err)
		}
		writer := &zipResponseWriteTracker{Writer: w}
		_, err = io.CopyBuffer(writer, reader, buf)
		closeErr := reader.Close()
		if err != nil {
			if writer.err != nil {
				return fmt.Errorf("%w: stream block %s for %s: %w", streaming.ErrStreamResponse, file.blockIDs[i], file.path, err)
			}
			return fmt.Errorf("%w: stream block %s for %s: %w", streaming.ErrStreamStorage, file.blockIDs[i], file.path, err)
		}
		if closeErr != nil {
			return fmt.Errorf("%w: close block reader %s for %s: %w", streaming.ErrStreamStorage, file.blockIDs[i], file.path, closeErr)
		}
	}

	return nil
}

type zipResponseWriteTracker struct {
	io.Writer
	err error
}

func (w *zipResponseWriteTracker) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	if err != nil {
		w.err = err
	}
	return n, err
}
