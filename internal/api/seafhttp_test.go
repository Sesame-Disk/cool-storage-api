package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

type fakeUploadStagingStore struct {
	mu                 sync.Mutex
	sessions           []UploadSessionRecord
	blocks             []UploadSessionBlockRecord
	promotions         map[string]UploadBlockPromotionRecord
	err                error
	sessionErr         error
	blockErr           error
	promotionErr       error
	promotionApplyErr  error
	promotionDeleteErr error
}

func (f *fakeUploadStagingStore) UpsertSession(record UploadSessionRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sessionErr != nil {
		return f.sessionErr
	}
	if f.err != nil {
		return f.err
	}
	f.sessions = append(f.sessions, record)
	return nil
}

func (f *fakeUploadStagingStore) UpsertBlock(record UploadSessionBlockRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.blockErr != nil {
		return f.blockErr
	}
	if f.err != nil {
		return f.err
	}
	f.blocks = append(f.blocks, record)
	return nil
}

func (f *fakeUploadStagingStore) GetSession(orgID, uploadID string) (*UploadSessionRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	for idx := len(f.sessions) - 1; idx >= 0; idx-- {
		record := f.sessions[idx]
		if record.OrgID == orgID && record.UploadID == uploadID {
			copyRecord := record
			return &copyRecord, nil
		}
	}
	return nil, nil
}

func (f *fakeUploadStagingStore) ListSessionsByState(orgID string, state UploadSessionState, limit int) ([]UploadSessionRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	if limit <= 0 {
		limit = len(f.sessions)
	}
	seen := make(map[string]struct{})
	var records []UploadSessionRecord
	for idx := len(f.sessions) - 1; idx >= 0 && len(records) < limit; idx-- {
		record := f.sessions[idx]
		if record.OrgID != orgID || record.State != state {
			continue
		}
		if _, ok := seen[record.UploadID]; ok {
			continue
		}
		seen[record.UploadID] = struct{}{}
		records = append(records, record)
	}
	return records, nil
}

func (f *fakeUploadStagingStore) ListBlockPromotions(orgID, uploadID string) ([]UploadBlockPromotionRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	var records []UploadBlockPromotionRecord
	for _, record := range f.promotions {
		if record.OrgID == orgID && record.UploadID == uploadID {
			records = append(records, record)
		}
	}
	return records, nil
}

func (f *fakeUploadStagingStore) TryStartBlockPromotion(record UploadBlockPromotionRecord) (UploadBlockPromotionAttempt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.promotionErr != nil {
		return UploadBlockPromotionAttempt{}, f.promotionErr
	}
	if f.err != nil {
		return UploadBlockPromotionAttempt{}, f.err
	}
	if f.promotions == nil {
		f.promotions = make(map[string]UploadBlockPromotionRecord)
	}
	key := fakeUploadPromotionKey(record.OrgID, record.UploadID, record.BlockIndex)
	existing, ok := f.promotions[key]
	if ok {
		return UploadBlockPromotionAttempt{
			Inserted:    false,
			BlockSHA256: existing.BlockSHA256,
			CommitID:    existing.CommitID,
			ClaimedAt:   &existing.ClaimedAt,
			AppliedAt:   existing.AppliedAt,
		}, nil
	}
	f.promotions[key] = record
	return UploadBlockPromotionAttempt{
		Inserted:    true,
		BlockSHA256: record.BlockSHA256,
		CommitID:    record.CommitID,
		ClaimedAt:   &record.ClaimedAt,
	}, nil
}

func (f *fakeUploadStagingStore) MarkBlockPromotionApplied(orgID, uploadID string, blockIndex int, appliedAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.promotionApplyErr != nil {
		return f.promotionApplyErr
	}
	if f.err != nil {
		return f.err
	}
	if f.promotions == nil {
		return nil
	}
	key := fakeUploadPromotionKey(orgID, uploadID, blockIndex)
	record, ok := f.promotions[key]
	if !ok {
		return nil
	}
	appliedAtCopy := appliedAt.UTC()
	record.AppliedAt = &appliedAtCopy
	f.promotions[key] = record
	return nil
}

func (f *fakeUploadStagingStore) DeleteBlockPromotion(orgID, uploadID string, blockIndex int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.promotionDeleteErr != nil {
		return f.promotionDeleteErr
	}
	if f.err != nil {
		return f.err
	}
	if f.promotions == nil {
		return nil
	}
	delete(f.promotions, fakeUploadPromotionKey(orgID, uploadID, blockIndex))
	return nil
}

func (f *fakeUploadStagingStore) DeleteAllBlockPromotions(orgID, uploadID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.promotionDeleteErr != nil {
		return f.promotionDeleteErr
	}
	if f.err != nil {
		return f.err
	}
	if f.promotions == nil {
		return nil
	}
	for key, record := range f.promotions {
		if record.OrgID == orgID && record.UploadID == uploadID {
			delete(f.promotions, key)
		}
	}
	return nil
}

func fakeUploadPromotionKey(orgID, uploadID string, blockIndex int) string {
	return orgID + "|" + uploadID + "|" + strconv.Itoa(blockIndex)
}

type orphanRecorderCall struct {
	orgID        string
	blockID      string
	storageClass string
	errMsg       string
}

func init() {
	gin.SetMode(gin.TestMode)
}

type mockObjectStore struct {
	data []byte
}

func (m *mockObjectStore) Put(ctx context.Context, blockID string, data io.Reader, size int64) (string, error) {
	_, _ = io.Copy(io.Discard, data)
	return blockID, nil
}

func (m *mockObjectStore) Get(ctx context.Context, storageKey string) (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(m.data)), nil
}

func (m *mockObjectStore) Delete(ctx context.Context, storageKey string) error {
	return nil
}

func (m *mockObjectStore) Exists(ctx context.Context, storageKey string) (bool, error) {
	return true, nil
}

func (m *mockObjectStore) GetAccessType() storage.AccessType {
	return storage.AccessImmediate
}

func (m *mockObjectStore) InitiateRestore(ctx context.Context, storageKey string) (string, error) {
	return "", nil
}

func (m *mockObjectStore) CheckRestoreStatus(ctx context.Context, storageKey string) (bool, error) {
	return true, nil
}

func (m *mockObjectStore) GetRestoreExpiry(ctx context.Context, storageKey string) (*time.Time, error) {
	return nil, nil
}

// ============================================================================
// ChunkManager janitor Tests
// ============================================================================

// newTestChunkManager creates a ChunkManager with a controllable clock and
// isolated tempDir. It does NOT start the janitor goroutine — tests call
// sweepOnce() directly.
func newTestChunkManager(t *testing.T) (*ChunkManager, string) {
	t.Helper()
	dir := t.TempDir()
	cm := &ChunkManager{
		uploads:         make(map[string]*ChunkUpload),
		tempDir:         dir,
		janitorInterval: time.Hour, // irrelevant — goroutine not started
		trackerTTL:      1 * time.Hour,
		diskTTL:         2 * time.Hour,
		now:             time.Now,
		stopCh:          make(chan struct{}),
	}
	return cm, dir
}

// TestChunkJanitor_SweepStaleTracker verifies that a tracker with no recent
// activity gets reaped and its temp file deleted.
func TestChunkJanitor_SweepStaleTracker(t *testing.T) {
	cm, dir := newTestChunkManager(t)

	// Create a real temp file in the isolated dir.
	f, err := os.CreateTemp(dir, "sesamefs_upload_tok1_file.txt")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	f.Close()

	past := time.Now().Add(-90 * time.Minute)
	upload := &ChunkUpload{
		Token:     "tok1",
		Filename:  "file.txt",
		TempFile:  nil, // already closed above; Cleanup will fail on Close but Remove still runs
		TempPath:  f.Name(),
		updatedAt: past,
	}
	upload.TempFile, _ = os.OpenFile(f.Name(), os.O_RDWR, 0600)
	cm.uploads["tok1:file.txt"] = upload

	// Advance clock past trackerTTL.
	cm.now = func() time.Time { return time.Now().Add(70 * time.Minute) }
	cm.sweepOnce()

	if _, exists := cm.uploads["tok1:file.txt"]; exists {
		t.Error("stale tracker should have been removed from map")
	}
	if _, err := os.Stat(f.Name()); !os.IsNotExist(err) {
		t.Errorf("temp file should be deleted, stat err=%v", err)
	}
}

// TestChunkJanitor_ActiveTrackerNotSwept verifies that a tracker with recent
// activity is NOT swept.
func TestChunkJanitor_ActiveTrackerNotSwept(t *testing.T) {
	cm, dir := newTestChunkManager(t)

	f, err := os.CreateTemp(dir, "sesamefs_upload_tok2_file.txt")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	f.Close()

	upload := &ChunkUpload{
		Token:     "tok2",
		Filename:  "file.txt",
		TempFile:  nil,
		TempPath:  f.Name(),
		updatedAt: time.Now(), // just now
	}
	upload.TempFile, _ = os.OpenFile(f.Name(), os.O_RDWR, 0600)
	cm.uploads["tok2:file.txt"] = upload

	// Clock advances only 5 minutes — well below the 1-hour tracker TTL.
	cm.now = func() time.Time { return time.Now().Add(5 * time.Minute) }
	cm.sweepOnce()

	if _, exists := cm.uploads["tok2:file.txt"]; !exists {
		t.Error("active tracker should NOT be removed from map")
	}
	if _, err := os.Stat(f.Name()); err != nil {
		t.Errorf("temp file should still exist, stat err=%v", err)
	}
	upload.TempFile.Close()
}

// TestChunkJanitor_DiskOrphanCleaned verifies that a prefixed temp file on
// disk whose tracker is gone and whose mtime is past diskTTL gets deleted.
func TestChunkJanitor_DiskOrphanCleaned(t *testing.T) {
	cm, dir := newTestChunkManager(t)

	// Create an "orphan" file (no in-memory tracker).
	f, err := os.CreateTemp(dir, "sesamefs_upload_stale_orphan")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	f.Close()

	// Back-date the file's mtime to 3 hours ago.
	staleTime := time.Now().Add(-3 * time.Hour)
	os.Chtimes(f.Name(), staleTime, staleTime)

	cm.sweepOnce()

	if _, err := os.Stat(f.Name()); !os.IsNotExist(err) {
		t.Errorf("orphaned disk file should be removed, stat err=%v", err)
	}
}

// TestChunkJanitor_DiskFileNotYetStale verifies that a newer orphan file is
// left alone (still within diskTTL).
func TestChunkJanitor_DiskFileNotYetStale(t *testing.T) {
	cm, dir := newTestChunkManager(t)

	f, err := os.CreateTemp(dir, "sesamefs_upload_fresh_orphan")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	f.Close()
	// mtime is "now" by default — well within diskTTL.

	cm.sweepOnce()

	if _, err := os.Stat(f.Name()); err != nil {
		t.Errorf("fresh file should survive sweep, stat err=%v", err)
	}
}

// TestChunkJanitor_NonPrefixedFileIgnored verifies that files without the
// sesamefs_upload_ prefix are never touched.
func TestChunkJanitor_NonPrefixedFileIgnored(t *testing.T) {
	cm, dir := newTestChunkManager(t)

	f, err := os.CreateTemp(dir, "unrelated_file")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	f.Close()
	// Back-date it so it would be cleaned if it had the right prefix.
	old := time.Now().Add(-24 * time.Hour)
	os.Chtimes(f.Name(), old, old)

	cm.sweepOnce()

	if _, err := os.Stat(f.Name()); err != nil {
		t.Errorf("non-prefixed file should be ignored, stat err=%v", err)
	}
}

func TestChunkUpload_TouchRefreshesActivity(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "sesamefs_upload_touch")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	defer f.Close()

	past := time.Now().Add(-2 * time.Hour)
	upload := &ChunkUpload{TempFile: f, TempPath: f.Name(), updatedAt: past}
	upload.Touch()

	if !upload.updatedAt.After(past) {
		t.Fatal("Touch should refresh updatedAt")
	}
}

func TestNewTokenManager(t *testing.T) {
	tests := []struct {
		name        string
		ttl         time.Duration
		expectedTTL time.Duration
	}{
		{
			name:        "custom TTL",
			ttl:         30 * time.Minute,
			expectedTTL: 30 * time.Minute,
		},
		{
			name:        "zero TTL uses default",
			ttl:         0,
			expectedTTL: DefaultTokenTTL,
		},
		{
			name:        "negative TTL uses default",
			ttl:         -1 * time.Hour,
			expectedTTL: DefaultTokenTTL,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tm := NewTokenManager(tt.ttl)
			if tm == nil {
				t.Fatal("NewTokenManager returned nil")
			}
			if tm.tokenTTL != tt.expectedTTL {
				t.Errorf("tokenTTL = %v, want %v", tm.tokenTTL, tt.expectedTTL)
			}
			if tm.tokens == nil {
				t.Error("tokens map should be initialized")
			}
		})
	}
}

func TestTokenManagerCreateToken(t *testing.T) {
	tm := NewTokenManager(1 * time.Hour)

	token, err := tm.CreateToken(TokenTypeUpload, "org1", "repo1", "/path", "user1", "", 1*time.Hour)
	if err != nil {
		t.Fatalf("CreateToken failed: %v", err)
	}

	if token == nil {
		t.Fatal("token should not be nil")
	}
	if token.Token == "" {
		t.Error("token string should not be empty")
	}
	if len(token.Token) != 32 { // 16 bytes = 32 hex chars
		t.Errorf("token length = %d, want 32", len(token.Token))
	}
	if token.Type != TokenTypeUpload {
		t.Errorf("Type = %s, want %s", token.Type, TokenTypeUpload)
	}
	if token.OrgID != "org1" {
		t.Errorf("OrgID = %s, want org1", token.OrgID)
	}
	if token.RepoID != "repo1" {
		t.Errorf("RepoID = %s, want repo1", token.RepoID)
	}
	if token.Path != "/path" {
		t.Errorf("Path = %s, want /path", token.Path)
	}
	if token.UserID != "user1" {
		t.Errorf("UserID = %s, want user1", token.UserID)
	}
	if token.ExpiresAt.Before(time.Now()) {
		t.Error("ExpiresAt should be in the future")
	}
}

func TestTokenManagerCreateUploadToken(t *testing.T) {
	tm := NewTokenManager(1 * time.Hour)

	tokenStr, err := tm.CreateUploadToken("org1", "repo1", "/upload/path", "user1")
	if err != nil {
		t.Fatalf("CreateUploadToken failed: %v", err)
	}

	if tokenStr == "" {
		t.Error("token string should not be empty")
	}

	// Verify we can retrieve it
	token, ok := tm.GetToken(tokenStr, TokenTypeUpload)
	if !ok {
		t.Error("token should be retrievable")
	}
	if token.Type != TokenTypeUpload {
		t.Errorf("Type = %s, want %s", token.Type, TokenTypeUpload)
	}
}

func TestTokenManagerCreateDownloadToken(t *testing.T) {
	tm := NewTokenManager(1 * time.Hour)

	tokenStr, err := tm.CreateDownloadToken("org1", "repo1", "/file.txt", "user1")
	if err != nil {
		t.Fatalf("CreateDownloadToken failed: %v", err)
	}

	if tokenStr == "" {
		t.Error("token string should not be empty")
	}

	// Verify we can retrieve it
	token, ok := tm.GetToken(tokenStr, TokenTypeDownload)
	if !ok {
		t.Error("token should be retrievable")
	}
	if token.Type != TokenTypeDownload {
		t.Errorf("Type = %s, want %s", token.Type, TokenTypeDownload)
	}
}

func TestTokenManagerGetToken(t *testing.T) {
	tm := NewTokenManager(1 * time.Hour)

	// Create tokens
	uploadToken, _ := tm.CreateUploadToken("org1", "repo1", "/", "user1")
	downloadToken, _ := tm.CreateDownloadToken("org1", "repo1", "/file.txt", "user1")

	tests := []struct {
		name         string
		tokenStr     string
		expectedType TokenType
		wantOK       bool
	}{
		{
			name:         "valid upload token",
			tokenStr:     uploadToken,
			expectedType: TokenTypeUpload,
			wantOK:       true,
		},
		{
			name:         "valid download token",
			tokenStr:     downloadToken,
			expectedType: TokenTypeDownload,
			wantOK:       true,
		},
		{
			name:         "upload token with wrong type",
			tokenStr:     uploadToken,
			expectedType: TokenTypeDownload,
			wantOK:       false,
		},
		{
			name:         "download token with wrong type",
			tokenStr:     downloadToken,
			expectedType: TokenTypeUpload,
			wantOK:       false,
		},
		{
			name:         "non-existent token",
			tokenStr:     "nonexistent",
			expectedType: TokenTypeUpload,
			wantOK:       false,
		},
		{
			name:         "empty token",
			tokenStr:     "",
			expectedType: TokenTypeUpload,
			wantOK:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token, ok := tm.GetToken(tt.tokenStr, tt.expectedType)
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
			if tt.wantOK && token == nil {
				t.Error("token should not be nil when ok is true")
			}
		})
	}
}

func TestResolveLibraryBlockStoreUsesDefaultStorageClass(t *testing.T) {
	manager := storage.NewManager()
	manager.SetDefaultClass("hot-minio-local")
	manager.RegisterBackend("hot-minio-local", &storage.S3Store{}, "")

	h := &SeafHTTPHandler{storageManager: manager}

	blockStore, storageClass, err := h.resolveLibraryBlockStore("", "org-id", "repo-id")
	if err != nil {
		t.Fatalf("resolveLibraryBlockStore returned error: %v", err)
	}
	if blockStore == nil {
		t.Fatal("resolveLibraryBlockStore returned nil block store")
	}
	if storageClass != "hot-minio-local" {
		t.Fatalf("resolveLibraryBlockStore storage class = %q, want %q", storageClass, "hot-minio-local")
	}
}

func TestZipTraversalBudgetRejectsDepthLimit(t *testing.T) {
	budget := &zipTraversalBudget{maxEntries: 10, maxDepth: 1, maxBytes: 1024}
	err := budget.noteDirectory(2)
	if err == nil {
		t.Fatal("expected depth limit error")
	}
	if !isZipLimitError(err) {
		t.Fatalf("expected zip limit error, got %v", err)
	}
}

func TestZipTraversalBudgetRejectsFileCountLimit(t *testing.T) {
	budget := &zipTraversalBudget{maxEntries: 1, maxDepth: 10, maxBytes: 1024}
	if err := budget.noteFile(100); err != nil {
		t.Fatalf("first noteFile() error = %v", err)
	}
	err := budget.noteFile(100)
	if err == nil {
		t.Fatal("expected file count limit error")
	}
	if !isZipLimitError(err) {
		t.Fatalf("expected zip limit error, got %v", err)
	}
}

func TestZipTraversalBudgetRejectsByteLimit(t *testing.T) {
	budget := &zipTraversalBudget{maxEntries: 10, maxDepth: 10, maxBytes: 100}
	if err := budget.noteFile(60); err != nil {
		t.Fatalf("first noteFile() error = %v", err)
	}
	err := budget.noteFile(50)
	if err == nil {
		t.Fatal("expected byte limit error")
	}
	if !isZipLimitError(err) {
		t.Fatalf("expected zip limit error, got %v", err)
	}
}

func TestResolveLibraryBlockStoreUsesHostnameFallback(t *testing.T) {
	manager := storage.NewManager()
	manager.SetDefaultClass("hot-minio-local")
	manager.SetEndpointRegions(map[string]string{"eu.example.com": "eu"})
	manager.SetRegionClasses(map[string]storage.RegionClassConfig{
		"eu": {Hot: "hot-minio-eu"},
	})
	manager.RegisterBackend("hot-minio-local", &storage.S3Store{}, "")
	manager.RegisterBackend("hot-minio-eu", &storage.S3Store{}, "")

	h := &SeafHTTPHandler{
		storageManager: manager,
	}

	blockStore, storageClass, err := h.resolveLibraryBlockStore("eu.example.com", "org-id", "repo-id")
	if err != nil {
		t.Fatalf("resolveLibraryBlockStore returned error: %v", err)
	}
	if blockStore == nil {
		t.Fatal("resolveLibraryBlockStore returned nil block store")
	}
	if storageClass != "hot-minio-eu" {
		t.Fatalf("resolveLibraryBlockStore storage class = %q, want %q", storageClass, "hot-minio-eu")
	}
}

func TestTokenManagerGetTokenExpired(t *testing.T) {
	tm := NewTokenManager(1 * time.Hour)

	// Create a token with very short TTL
	token, _ := tm.CreateToken(TokenTypeUpload, "org1", "repo1", "/", "user1", "", 1*time.Millisecond)

	// Wait for it to expire
	time.Sleep(10 * time.Millisecond)

	// Should not be retrievable
	_, ok := tm.GetToken(token.Token, TokenTypeUpload)
	if ok {
		t.Error("expired token should not be retrievable")
	}
}

func TestTokenManagerDeleteToken(t *testing.T) {
	tm := NewTokenManager(1 * time.Hour)

	tokenStr, _ := tm.CreateUploadToken("org1", "repo1", "/", "user1")

	// Verify token exists
	_, ok := tm.GetToken(tokenStr, TokenTypeUpload)
	if !ok {
		t.Fatal("token should exist before deletion")
	}

	// Delete token
	err := tm.DeleteToken(tokenStr)
	if err != nil {
		t.Fatalf("DeleteToken failed: %v", err)
	}

	// Verify token is gone
	_, ok = tm.GetToken(tokenStr, TokenTypeUpload)
	if ok {
		t.Error("token should not exist after deletion")
	}
}

func TestTokenManagerDeleteNonExistent(t *testing.T) {
	tm := NewTokenManager(1 * time.Hour)

	// Deleting non-existent token should not error
	err := tm.DeleteToken("nonexistent")
	if err != nil {
		t.Errorf("DeleteToken should not error for non-existent token: %v", err)
	}
}

func TestTokenManagerTokenUniqueness(t *testing.T) {
	tm := NewTokenManager(1 * time.Hour)

	tokens := make(map[string]bool)
	for i := 0; i < 100; i++ {
		tokenStr, err := tm.CreateUploadToken("org", "repo", "/", "user")
		if err != nil {
			t.Fatalf("CreateUploadToken failed: %v", err)
		}
		if tokens[tokenStr] {
			t.Errorf("duplicate token generated: %s", tokenStr)
		}
		tokens[tokenStr] = true
	}
}

func TestTokenManagerImplementsInterface(t *testing.T) {
	// Compile-time check that TokenManager implements TokenStore
	var _ TokenStore = (*TokenManager)(nil)
}

func TestTokenTypeConstants(t *testing.T) {
	if TokenTypeUpload != "upload" {
		t.Errorf("TokenTypeUpload = %s, want upload", TokenTypeUpload)
	}
	if TokenTypeDownload != "download" {
		t.Errorf("TokenTypeDownload = %s, want download", TokenTypeDownload)
	}
}

func TestDefaultTokenTTL(t *testing.T) {
	if DefaultTokenTTL != 1*time.Hour {
		t.Errorf("DefaultTokenTTL = %v, want 1h", DefaultTokenTTL)
	}
}

// ============================================================================
// AccessToken struct tests
// ============================================================================

func TestAccessTokenFields(t *testing.T) {
	now := time.Now()
	token := AccessToken{
		Token:     "abc123",
		Type:      TokenTypeUpload,
		OrgID:     "org-1",
		RepoID:    "repo-1",
		Path:      "/documents/file.txt",
		UserID:    "user-1",
		ExpiresAt: now.Add(1 * time.Hour),
		CreatedAt: now,
	}

	if token.Token != "abc123" {
		t.Errorf("Token = %s, want abc123", token.Token)
	}
	if token.Type != TokenTypeUpload {
		t.Errorf("Type = %s, want upload", token.Type)
	}
	if token.Path != "/documents/file.txt" {
		t.Errorf("Path = %s, want /documents/file.txt", token.Path)
	}
}

// ============================================================================
// SeafHTTPHandler tests
// ============================================================================

// MockTokenStore implements TokenStore for testing
type MockTokenStore struct {
	tokens map[string]*AccessToken
}

func NewMockTokenStore() *MockTokenStore {
	return &MockTokenStore{
		tokens: make(map[string]*AccessToken),
	}
}

func (m *MockTokenStore) CreateUploadToken(orgID, repoID, path, userID string) (string, error) {
	token := &AccessToken{
		Token:     "mock-upload-token",
		Type:      TokenTypeUpload,
		OrgID:     orgID,
		RepoID:    repoID,
		Path:      path,
		UserID:    userID,
		ExpiresAt: time.Now().Add(1 * time.Hour),
		CreatedAt: time.Now(),
	}
	m.tokens[token.Token] = token
	return token.Token, nil
}

func (m *MockTokenStore) CreateDownloadToken(orgID, repoID, path, userID string) (string, error) {
	token := &AccessToken{
		Token:     "mock-download-token",
		Type:      TokenTypeDownload,
		OrgID:     orgID,
		RepoID:    repoID,
		Path:      path,
		UserID:    userID,
		ExpiresAt: time.Now().Add(1 * time.Hour),
		CreatedAt: time.Now(),
	}
	m.tokens[token.Token] = token
	return token.Token, nil
}

func (m *MockTokenStore) GetToken(tokenStr string, expectedType TokenType) (*AccessToken, bool) {
	token, ok := m.tokens[tokenStr]
	if !ok || token.Type != expectedType {
		return nil, false
	}
	return token, true
}

func (m *MockTokenStore) DeleteToken(tokenStr string) error {
	delete(m.tokens, tokenStr)
	return nil
}

func (m *MockTokenStore) CreateOneTimeLoginToken(userID, orgID, authToken string) (string, error) {
	return "mock-one-time-token", nil
}

func (m *MockTokenStore) ConsumeOneTimeLoginToken(oneTimeToken string) (string, error) {
	return "mock-auth-token", nil
}

func (m *MockTokenStore) CreateLinkUploadToken(orgID, repoID, path, userID string) (string, error) {
	token := &AccessToken{
		Token:     "mock-link-upload-token",
		Type:      TokenTypeUpload,
		Source:    "link",
		OrgID:     orgID,
		RepoID:    repoID,
		Path:      path,
		UserID:    userID,
		ExpiresAt: time.Now().Add(1 * time.Hour),
		CreatedAt: time.Now(),
	}
	m.tokens[token.Token] = token
	return token.Token, nil
}

func (m *MockTokenStore) CreateLinkDownloadToken(orgID, repoID, path, userID string) (string, error) {
	token := &AccessToken{
		Token:     "mock-link-download-token",
		Type:      TokenTypeDownload,
		Source:    "link",
		OrgID:     orgID,
		RepoID:    repoID,
		Path:      path,
		UserID:    userID,
		ExpiresAt: time.Now().Add(1 * time.Hour),
		CreatedAt: time.Now(),
	}
	m.tokens[token.Token] = token
	return token.Token, nil
}

func TestNewSeafHTTPHandler(t *testing.T) {
	tokenStore := NewMockTokenStore()
	handler := NewSeafHTTPHandler(nil, nil, nil, tokenStore, nil, nil)

	if handler == nil {
		t.Fatal("NewSeafHTTPHandler returned nil")
	}
	if handler.tokenStore == nil {
		t.Error("tokenStore should be set")
	}
}

func TestSeafHTTPHandlerUploadNoStorage(t *testing.T) {
	tokenStore := NewMockTokenStore()
	// Add a valid upload token
	tokenStore.CreateUploadToken("org1", "repo1", "/", "user1")

	handler := NewSeafHTTPHandler(nil, nil, nil, tokenStore, nil, nil) // nil storage

	r := gin.New()
	r.POST("/seafhttp/upload-api/:token", handler.HandleUpload)

	// Create multipart form
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, _ := writer.CreateFormFile("file", "test.txt")
	part.Write([]byte("test content"))
	writer.Close()

	req, _ := http.NewRequest("POST", "/seafhttp/upload-api/mock-upload-token", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Should fail because storage is nil
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestSeafHTTPHandlerUploadInvalidToken(t *testing.T) {
	tokenStore := NewMockTokenStore()
	handler := NewSeafHTTPHandler(nil, nil, nil, tokenStore, nil, nil)

	r := gin.New()
	r.POST("/seafhttp/upload-api/:token", handler.HandleUpload)

	// Create multipart form
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, _ := writer.CreateFormFile("file", "test.txt")
	part.Write([]byte("test content"))
	writer.Close()

	req, _ := http.NewRequest("POST", "/seafhttp/upload-api/invalid-token", &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestSeafHTTPHandlerUploadNoFile(t *testing.T) {
	tokenStore := NewMockTokenStore()
	tokenStore.CreateUploadToken("org1", "repo1", "/", "user1")
	handler := NewSeafHTTPHandler(nil, nil, nil, tokenStore, nil, nil)

	r := gin.New()
	r.POST("/seafhttp/upload-api/:token", handler.HandleUpload)

	// Request without file - but storage is nil, so returns 503 first
	req, _ := http.NewRequest("POST", "/seafhttp/upload-api/mock-upload-token", nil)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Storage check happens before file check, so we get 503
	// Testing "no file" scenario requires integration testing with real storage
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestSeafHTTPHandlerUploadNoFileWithStorageManager(t *testing.T) {
	tokenStore := NewMockTokenStore()
	tokenStore.CreateUploadToken("org1", "repo1", "/", "user1")
	handler := NewSeafHTTPHandler(nil, storage.NewManager(), nil, tokenStore, nil, nil)

	r := gin.New()
	r.POST("/seafhttp/upload-api/:token", handler.HandleUpload)

	req, _ := http.NewRequest("POST", "/seafhttp/upload-api/mock-upload-token", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleUploadChunked_PartialChunkPersistsReceivingSession(t *testing.T) {
	tm := NewTokenManager(time.Hour)
	tokenStr, err := tm.CreateUploadToken("org-1", "repo-1", "/", "user-1")
	if err != nil {
		t.Fatalf("CreateUploadToken failed: %v", err)
	}
	staging := &fakeUploadStagingStore{}
	handler := NewSeafHTTPHandler(nil, storage.NewManager(), nil, tm, nil, nil)
	handler.SetUploadStagingStore(staging)

	r := gin.New()
	r.POST("/seafhttp/upload-api/:token", handler.HandleUpload)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "partial.txt")
	_, _ = part.Write([]byte("hel"))
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/seafhttp/upload-api/"+tokenStr, &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Content-Range", "bytes 0-2/5")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if len(staging.sessions) == 0 {
		t.Fatal("expected staging session to be persisted")
	}
	got := staging.sessions[len(staging.sessions)-1]
	if got.State != UploadSessionStateReceiving {
		t.Fatalf("session state = %q, want %q", got.State, UploadSessionStateReceiving)
	}
	if got.Filename != "partial.txt" {
		t.Fatalf("filename = %q, want partial.txt", got.Filename)
	}
	chunkManager.CleanupUpload(tokenStr, "partial.txt")
}

func TestHandleUploadChunked_FinalizeSuccessClosesSession(t *testing.T) {
	tm := NewTokenManager(time.Hour)
	tokenStr, err := tm.CreateUploadToken("org-1", "repo-1", "/", "user-1")
	if err != nil {
		t.Fatalf("CreateUploadToken failed: %v", err)
	}
	staging := &fakeUploadStagingStore{}
	handler := NewSeafHTTPHandler(nil, storage.NewManager(), nil, tm, nil, nil)
	handler.SetUploadStagingStore(staging)
	handler.chunkedFinalize = func(c *gin.Context, token *AccessToken, upload *ChunkUpload, uploadID, parentDir, filename, storageKey string, totalSize int64, replace bool) (finalizeUploadResult, error) {
		return finalizeUploadResult{FileID: "file-id", ActualFilename: filename, CommitID: "commit-id"}, nil
	}

	r := gin.New()
	r.POST("/seafhttp/upload-api/:token", handler.HandleUpload)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "done.txt")
	_, _ = part.Write([]byte("hello"))
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/seafhttp/upload-api/"+tokenStr+"?ret-json=1", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Content-Range", "bytes 0-4/5")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if len(staging.sessions) < 3 {
		t.Fatalf("expected receiving/promoting/closed session writes, got %d", len(staging.sessions))
	}
	if staging.sessions[len(staging.sessions)-2].State != UploadSessionStatePromoting {
		t.Fatalf("penultimate state = %q, want %q", staging.sessions[len(staging.sessions)-2].State, UploadSessionStatePromoting)
	}
	if staging.sessions[len(staging.sessions)-1].State != UploadSessionStateClosed {
		t.Fatalf("final state = %q, want %q", staging.sessions[len(staging.sessions)-1].State, UploadSessionStateClosed)
	}
	if staging.sessions[len(staging.sessions)-1].CommitID != "commit-id" {
		t.Fatalf("commit id = %q, want commit-id", staging.sessions[len(staging.sessions)-1].CommitID)
	}
	if body := w.Body.String(); body == "" {
		t.Fatal("expected JSON response body")
	}
}

func TestHandleUploadChunked_ReceivingSessionWrittenOnceAcrossChunks(t *testing.T) {
	tm := NewTokenManager(time.Hour)
	tokenStr, err := tm.CreateUploadToken("org-1", "repo-1", "/", "user-1")
	if err != nil {
		t.Fatalf("CreateUploadToken failed: %v", err)
	}
	staging := &fakeUploadStagingStore{}
	handler := NewSeafHTTPHandler(nil, storage.NewManager(), nil, tm, nil, nil)
	handler.SetUploadStagingStore(staging)
	handler.chunkedFinalize = func(c *gin.Context, token *AccessToken, upload *ChunkUpload, uploadID, parentDir, filename, storageKey string, totalSize int64, replace bool) (finalizeUploadResult, error) {
		return finalizeUploadResult{FileID: "short-id", ActualFilename: filename, CommitID: "commit-id"}, nil
	}

	r := gin.New()
	r.POST("/seafhttp/upload-api/:token", handler.HandleUpload)

	makeRequest := func(contentRange string, payload string) *httptest.ResponseRecorder {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		part, _ := writer.CreateFormFile("file", "twostep.txt")
		_, _ = part.Write([]byte(payload))
		writer.Close()

		req := httptest.NewRequest(http.MethodPost, "/seafhttp/upload-api/"+tokenStr, &body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		req.Header.Set("Content-Range", contentRange)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	first := makeRequest("bytes 0-1/5", "he")
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d, want %d", first.Code, http.StatusOK)
	}
	second := makeRequest("bytes 2-4/5", "llo")
	if second.Code != http.StatusOK {
		t.Fatalf("second status = %d, want %d", second.Code, http.StatusOK)
	}

	receivingCount := 0
	for _, session := range staging.sessions {
		if session.State == UploadSessionStateReceiving {
			receivingCount++
		}
	}
	if receivingCount != 1 {
		t.Fatalf("receiving session writes = %d, want 1", receivingCount)
	}
	if staging.sessions[len(staging.sessions)-1].State != UploadSessionStateClosed {
		t.Fatalf("final state = %q, want %q", staging.sessions[len(staging.sessions)-1].State, UploadSessionStateClosed)
	}
}

func TestHandleUploadChunked_PreservesClosedSessionUntilFinalize(t *testing.T) {
	tm := NewTokenManager(time.Hour)
	tokenStr, err := tm.CreateUploadToken("org-1", "repo-1", "/", "user-1")
	if err != nil {
		t.Fatalf("CreateUploadToken failed: %v", err)
	}
	staging := &fakeUploadStagingStore{}
	uploadID := BuildUploadSessionID("org-1", "repo-1", "user-1", tokenStr, "/", "retry.txt")
	now := time.Now().UTC()
	staging.sessions = append(staging.sessions, UploadSessionRecord{
		UploadID:       uploadID,
		OrgID:          "org-1",
		RepoID:         "repo-1",
		UserID:         "user-1",
		TokenID:        tokenStr,
		ParentDir:      "/",
		Filename:       "retry.txt",
		ActualFilename: "retry.txt",
		CommitID:       "commit-prev",
		TotalSize:      5,
		State:          UploadSessionStateClosed,
		CreatedAt:      now,
		UpdatedAt:      now,
		ExpiresAt:      now.Add(time.Hour),
	})

	handler := NewSeafHTTPHandler(nil, storage.NewManager(), nil, tm, nil, nil)
	handler.SetUploadStagingStore(staging)
	handler.chunkedFinalize = func(c *gin.Context, token *AccessToken, upload *ChunkUpload, uploadID, parentDir, filename, storageKey string, totalSize int64, replace bool) (finalizeUploadResult, error) {
		session, err := staging.GetSession("org-1", uploadID)
		if err != nil {
			t.Fatalf("GetSession failed: %v", err)
		}
		if session == nil {
			t.Fatal("expected preserved closed session before finalize")
		}
		if session.State != UploadSessionStateClosed {
			t.Fatalf("session state before finalize = %q, want %q", session.State, UploadSessionStateClosed)
		}
		if session.CommitID != "commit-prev" {
			t.Fatalf("commit id before finalize = %q, want commit-prev", session.CommitID)
		}
		return finalizeUploadResult{FileID: "file-id", ActualFilename: filename, CommitID: "commit-next"}, nil
	}

	r := gin.New()
	r.POST("/seafhttp/upload-api/:token", handler.HandleUpload)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "retry.txt")
	_, _ = part.Write([]byte("hello"))
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/seafhttp/upload-api/"+tokenStr+"?ret-json=1", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Content-Range", "bytes 0-4/5")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if len(staging.sessions) != 2 {
		t.Fatalf("session writes = %d, want 2 (original closed + final closed)", len(staging.sessions))
	}
	if staging.sessions[len(staging.sessions)-1].State != UploadSessionStateClosed {
		t.Fatalf("final state = %q, want %q", staging.sessions[len(staging.sessions)-1].State, UploadSessionStateClosed)
	}
	if staging.sessions[len(staging.sessions)-1].CommitID != "commit-next" {
		t.Fatalf("final commit id = %q, want commit-next", staging.sessions[len(staging.sessions)-1].CommitID)
	}
	chunkManager.CleanupUpload(tokenStr, "retry.txt")
}

func TestPersistUploadBlockStateSwallowsErrors(t *testing.T) {
	handler := NewSeafHTTPHandler(nil, storage.NewManager(), nil, NewMockTokenStore(), nil, nil)
	staging := &fakeUploadStagingStore{blockErr: errors.New("staging down")}
	handler.SetUploadStagingStore(staging)

	handler.persistUploadBlockState(UploadSessionBlockRecord{
		UploadID:    "upload-1",
		BlockIndex:  0,
		OrgID:       "org-1",
		BlockSHA256: "abc123",
		State:       UploadBlockStateRegistered,
		CreatedAt:   time.Now().UTC(),
		UpdatedAt:   time.Now().UTC(),
	}, string(UploadBlockStateRegistered))

	if len(staging.blocks) != 0 {
		t.Fatalf("expected failed block staging writes to be swallowed, got %d recorded blocks", len(staging.blocks))
	}
}

func TestPersistUploadS3OrphanUsesInjectedRecorder(t *testing.T) {
	handler := NewSeafHTTPHandler(nil, storage.NewManager(), nil, NewMockTokenStore(), nil, nil)
	var calls []orphanRecorderCall
	handler.SetUploadOrphanRecorder(func(orgID, blockID, storageClass, errMsg string) error {
		calls = append(calls, orphanRecorderCall{
			orgID:        orgID,
			blockID:      blockID,
			storageClass: storageClass,
			errMsg:       errMsg,
		})
		return nil
	})

	handler.persistUploadS3Orphan("org-1", "block-1", "hot", "mapping failed")

	if len(calls) != 1 {
		t.Fatalf("orphan recorder calls = %d, want 1", len(calls))
	}
	if calls[0].blockID != "block-1" {
		t.Fatalf("block id = %q, want block-1", calls[0].blockID)
	}
	if calls[0].storageClass != "hot" {
		t.Fatalf("storage class = %q, want hot", calls[0].storageClass)
	}
	if calls[0].errMsg != "mapping failed" {
		t.Fatalf("error msg = %q, want mapping failed", calls[0].errMsg)
	}
}

func TestPersistUploadS3OrphanSwallowsErrors(t *testing.T) {
	handler := NewSeafHTTPHandler(nil, storage.NewManager(), nil, NewMockTokenStore(), nil, nil)
	handler.SetUploadOrphanRecorder(func(orgID, blockID, storageClass, errMsg string) error {
		return errors.New("gc_s3_orphans down")
	})

	handler.persistUploadS3Orphan("org-1", "block-1", "hot", "mapping failed")
}

func TestPublishPreparedUploadPromotesBeforePublishingHead(t *testing.T) {
	handler := NewSeafHTTPHandler(nil, storage.NewManager(), nil, NewMockTokenStore(), nil, nil)
	staging := &fakeUploadStagingStore{}
	handler.SetUploadStagingStore(staging)

	var calls []string
	handler.blockPromoter = func(orgID, operationKey, blockID string, sizeBytes int, storageClass, storageKey string) error {
		calls = append(calls, "promote:"+blockID)
		return nil
	}
	handler.blockRollback = func(orgID, operationKey string, blockIDs []string) []string {
		calls = append(calls, "rollback")
		return nil
	}
	handler.commitPublisher = func(orgID, repoID, commitID string) error {
		calls = append(calls, "publish:"+commitID)
		return nil
	}

	uploadedAt := time.Now().UTC()
	err := handler.publishPreparedUpload("org-1", "repo-1", "upload-1", preparedUploadCommit{CommitID: "commit-1"}, []uploadBlockPromotion{
		{
			BlockIndex:   0,
			BlockSHA1:    "sha1-1",
			BlockSHA256:  "sha256-1",
			SizeBytes:    10,
			StorageClass: "hot",
			StorageKey:   "sha256-1",
			Source:       UploadBlockSourceNewObject,
			CreatedAt:    uploadedAt,
			UploadedAt:   &uploadedAt,
		},
		{
			BlockIndex:   1,
			BlockSHA1:    "sha1-2",
			BlockSHA256:  "sha256-2",
			SizeBytes:    20,
			StorageClass: "hot",
			StorageKey:   "sha256-2",
			Source:       UploadBlockSourceNewObject,
			CreatedAt:    uploadedAt,
			UploadedAt:   &uploadedAt,
		},
	})
	if err != nil {
		t.Fatalf("publishPreparedUpload failed: %v", err)
	}

	if len(calls) != 3 {
		t.Fatalf("call count = %d, want 3", len(calls))
	}
	if calls[0] != "promote:sha256-1" || calls[1] != "promote:sha256-2" || calls[2] != "publish:commit-1" {
		t.Fatalf("call order = %v, want promote/promote/publish", calls)
	}
	if len(staging.blocks) != 2 {
		t.Fatalf("promoted staging writes = %d, want 2", len(staging.blocks))
	}
	if staging.blocks[0].State != UploadBlockStatePromoted || staging.blocks[1].State != UploadBlockStatePromoted {
		t.Fatalf("staging states = %q/%q, want promoted/promoted", staging.blocks[0].State, staging.blocks[1].State)
	}
}

func TestPublishPreparedUploadRollsBackWhenPublishFails(t *testing.T) {
	handler := NewSeafHTTPHandler(nil, storage.NewManager(), nil, NewMockTokenStore(), nil, nil)
	staging := &fakeUploadStagingStore{}
	handler.SetUploadStagingStore(staging)

	var rollbackOpKey string
	var rollbackBlockIDs []string
	handler.blockPromoter = func(orgID, operationKey, blockID string, sizeBytes int, storageClass, storageKey string) error {
		return nil
	}
	handler.blockRollback = func(orgID, operationKey string, blockIDs []string) []string {
		rollbackOpKey = operationKey
		rollbackBlockIDs = append([]string(nil), blockIDs...)
		return nil
	}
	handler.commitPublisher = func(orgID, repoID, commitID string) error {
		return errors.New("publish failed")
	}

	err := handler.publishPreparedUpload("org-1", "repo-1", "upload-1", preparedUploadCommit{CommitID: "commit-1"}, []uploadBlockPromotion{
		{BlockIndex: 0, BlockSHA256: "sha256-1", SizeBytes: 10, StorageClass: "hot", StorageKey: "sha256-1"},
		{BlockIndex: 1, BlockSHA256: "sha256-2", SizeBytes: 20, StorageClass: "hot", StorageKey: "sha256-2"},
	})
	if err == nil {
		t.Fatal("expected publishPreparedUpload to fail")
	}
	if !strings.Contains(err.Error(), "failed to publish upload commit") {
		t.Fatalf("error = %v, want publish failure", err)
	}
	if rollbackOpKey != "upload-rollback:upload-1:commit-1" {
		t.Fatalf("rollback op key = %q, want upload-rollback:upload-1:commit-1", rollbackOpKey)
	}
	if len(rollbackBlockIDs) != 2 || rollbackBlockIDs[0] != "sha256-1" || rollbackBlockIDs[1] != "sha256-2" {
		t.Fatalf("rollback block ids = %v, want [sha256-1 sha256-2]", rollbackBlockIDs)
	}
}

func TestPublishPreparedUploadRollsBackPartialPromotionFailures(t *testing.T) {
	handler := NewSeafHTTPHandler(nil, storage.NewManager(), nil, NewMockTokenStore(), nil, nil)
	staging := &fakeUploadStagingStore{}
	handler.SetUploadStagingStore(staging)

	var publishCalled bool
	var rollbackBlockIDs []string
	handler.blockPromoter = func(orgID, operationKey, blockID string, sizeBytes int, storageClass, storageKey string) error {
		if blockID == "sha256-2" {
			return errors.New("promote failed")
		}
		return nil
	}
	handler.blockRollback = func(orgID, operationKey string, blockIDs []string) []string {
		rollbackBlockIDs = append([]string(nil), blockIDs...)
		return nil
	}
	handler.commitPublisher = func(orgID, repoID, commitID string) error {
		publishCalled = true
		return nil
	}

	err := handler.publishPreparedUpload("org-1", "repo-1", "upload-1", preparedUploadCommit{CommitID: "commit-1"}, []uploadBlockPromotion{
		{BlockIndex: 0, BlockSHA256: "sha256-1", SizeBytes: 10, StorageClass: "hot", StorageKey: "sha256-1"},
		{BlockIndex: 1, BlockSHA256: "sha256-2", SizeBytes: 20, StorageClass: "hot", StorageKey: "sha256-2"},
	})
	if err == nil {
		t.Fatal("expected publishPreparedUpload to fail")
	}
	if !strings.Contains(err.Error(), "failed to write block metadata") {
		t.Fatalf("error = %v, want promotion failure", err)
	}
	if publishCalled {
		t.Fatal("publish should not be called after a promotion failure")
	}
	if len(rollbackBlockIDs) != 1 || rollbackBlockIDs[0] != "sha256-1" {
		t.Fatalf("rollback block ids = %v, want [sha256-1]", rollbackBlockIDs)
	}
}

func TestPublishPreparedUploadRetrySkipsAlreadyAppliedPromotion(t *testing.T) {
	handler := NewSeafHTTPHandler(nil, storage.NewManager(), nil, NewMockTokenStore(), nil, nil)
	staging := &fakeUploadStagingStore{}
	handler.SetUploadStagingStore(staging)

	var promoteCalls int
	var publishCalls int
	handler.blockPromoter = func(orgID, operationKey, blockID string, sizeBytes int, storageClass, storageKey string) error {
		promoteCalls++
		return nil
	}
	handler.commitPublisher = func(orgID, repoID, commitID string) error {
		publishCalls++
		return nil
	}

	block := uploadBlockPromotion{BlockIndex: 0, BlockSHA256: "sha256-1", SizeBytes: 10, StorageClass: "hot", StorageKey: "sha256-1"}
	if err := handler.publishPreparedUpload("org-1", "repo-1", "upload-1", preparedUploadCommit{CommitID: "commit-1"}, []uploadBlockPromotion{block}); err != nil {
		t.Fatalf("first publishPreparedUpload failed: %v", err)
	}
	if err := handler.publishPreparedUpload("org-1", "repo-1", "upload-1", preparedUploadCommit{CommitID: "commit-1"}, []uploadBlockPromotion{block}); err != nil {
		t.Fatalf("second publishPreparedUpload failed: %v", err)
	}

	if promoteCalls != 1 {
		t.Fatalf("promote calls = %d, want 1", promoteCalls)
	}
	if publishCalls != 2 {
		t.Fatalf("publish calls = %d, want 2", publishCalls)
	}
}

func TestPublishPreparedUploadReleasesClaimAfterPromotionFailure(t *testing.T) {
	handler := NewSeafHTTPHandler(nil, storage.NewManager(), nil, NewMockTokenStore(), nil, nil)
	staging := &fakeUploadStagingStore{}
	handler.SetUploadStagingStore(staging)

	var promoteCalls int
	handler.blockPromoter = func(orgID, operationKey, blockID string, sizeBytes int, storageClass, storageKey string) error {
		promoteCalls++
		if promoteCalls == 1 {
			return errors.New("promote failed")
		}
		return nil
	}
	handler.commitPublisher = func(orgID, repoID, commitID string) error {
		return nil
	}

	block := uploadBlockPromotion{BlockIndex: 0, BlockSHA256: "sha256-1", SizeBytes: 10, StorageClass: "hot", StorageKey: "sha256-1"}
	err := handler.publishPreparedUpload("org-1", "repo-1", "upload-1", preparedUploadCommit{CommitID: "commit-1"}, []uploadBlockPromotion{block})
	if err == nil {
		t.Fatal("expected first publishPreparedUpload to fail")
	}
	if _, exists := staging.promotions[fakeUploadPromotionKey("org-1", "upload-1", 0)]; exists {
		t.Fatal("expected failed promotion claim to be released")
	}
	if err := handler.publishPreparedUpload("org-1", "repo-1", "upload-1", preparedUploadCommit{CommitID: "commit-1"}, []uploadBlockPromotion{block}); err != nil {
		t.Fatalf("second publishPreparedUpload failed: %v", err)
	}
	if promoteCalls != 2 {
		t.Fatalf("promote calls = %d, want 2", promoteCalls)
	}
}

func TestPublishPreparedUploadPublishFailureDoesNotRollbackAlreadyAppliedBlocks(t *testing.T) {
	handler := NewSeafHTTPHandler(nil, storage.NewManager(), nil, NewMockTokenStore(), nil, nil)
	staging := &fakeUploadStagingStore{}
	handler.SetUploadStagingStore(staging)

	handler.blockPromoter = func(orgID, operationKey, blockID string, sizeBytes int, storageClass, storageKey string) error {
		return nil
	}
	handler.commitPublisher = func(orgID, repoID, commitID string) error {
		return nil
	}

	block := uploadBlockPromotion{BlockIndex: 0, BlockSHA256: "sha256-1", SizeBytes: 10, StorageClass: "hot", StorageKey: "sha256-1"}
	if err := handler.publishPreparedUpload("org-1", "repo-1", "upload-1", preparedUploadCommit{CommitID: "commit-1"}, []uploadBlockPromotion{block}); err != nil {
		t.Fatalf("initial publishPreparedUpload failed: %v", err)
	}

	var rollbackCalls int
	handler.blockRollback = func(orgID, operationKey string, blockIDs []string) []string {
		rollbackCalls++
		return nil
	}
	handler.commitPublisher = func(orgID, repoID, commitID string) error {
		return errors.New("publish failed")
	}

	err := handler.publishPreparedUpload("org-1", "repo-1", "upload-1", preparedUploadCommit{CommitID: "commit-1"}, []uploadBlockPromotion{block})
	if err == nil {
		t.Fatal("expected retry publishPreparedUpload to fail")
	}
	if rollbackCalls != 0 {
		t.Fatalf("rollback calls = %d, want 0", rollbackCalls)
	}
}

func TestRecoverPreparedUploadIfPossiblePublishesPromotingSession(t *testing.T) {
	handler := NewSeafHTTPHandler(nil, storage.NewManager(), nil, NewMockTokenStore(), nil, nil)
	staging := &fakeUploadStagingStore{}
	handler.SetUploadStagingStore(staging)
	now := time.Now().UTC()
	staging.sessions = append(staging.sessions, UploadSessionRecord{
		UploadID:       "upload-1",
		OrgID:          "org-1",
		RepoID:         "repo-1",
		UserID:         "user-1",
		TokenID:        "token-1",
		ParentDir:      "/",
		Filename:       "file.txt",
		ActualFilename: "file.txt",
		CommitID:       "commit-1",
		TotalSize:      10,
		State:          UploadSessionStatePromoting,
		CreatedAt:      now,
		UpdatedAt:      now,
		ExpiresAt:      now.Add(time.Hour),
	})
	appliedAt := now.Add(time.Minute)
	staging.promotions = map[string]UploadBlockPromotionRecord{
		fakeUploadPromotionKey("org-1", "upload-1", 0): {
			OrgID:       "org-1",
			UploadID:    "upload-1",
			BlockIndex:  0,
			BlockSHA256: "sha256-1",
			CommitID:    "commit-1",
			ClaimedAt:   now,
			AppliedAt:   &appliedAt,
		},
	}

	var publishCalls int
	handler.commitPublisher = func(orgID, repoID, commitID string) error {
		publishCalls++
		return nil
	}

	recovered, ok, err := handler.recoverPreparedUploadIfPossible("org-1", "repo-1", "upload-1", []uploadBlockPromotion{{BlockIndex: 0, BlockSHA256: "sha256-1"}})
	if err != nil {
		t.Fatalf("recoverPreparedUploadIfPossible failed: %v", err)
	}
	if !ok {
		t.Fatal("expected recovery to succeed")
	}
	if recovered.CommitID != "commit-1" || recovered.ActualFilename != "file.txt" {
		t.Fatalf("recovered result = %+v, want commit-1/file.txt", recovered)
	}
	if publishCalls != 1 {
		t.Fatalf("publish calls = %d, want 1", publishCalls)
	}
	latest := staging.sessions[len(staging.sessions)-1]
	if latest.State != UploadSessionStateClosed {
		t.Fatalf("latest session state = %q, want %q", latest.State, UploadSessionStateClosed)
	}
}

func TestRecoverPreparedUploadIfPossibleReturnsClosedSessionWithoutRepublish(t *testing.T) {
	handler := NewSeafHTTPHandler(nil, storage.NewManager(), nil, NewMockTokenStore(), nil, nil)
	staging := &fakeUploadStagingStore{}
	handler.SetUploadStagingStore(staging)
	now := time.Now().UTC()
	staging.sessions = append(staging.sessions, UploadSessionRecord{
		UploadID:       "upload-1",
		OrgID:          "org-1",
		RepoID:         "repo-1",
		Filename:       "file.txt",
		ActualFilename: "renamed.txt",
		CommitID:       "commit-1",
		State:          UploadSessionStateClosed,
		CreatedAt:      now,
		UpdatedAt:      now,
		ExpiresAt:      now.Add(time.Hour),
	})
	appliedAt := now.Add(time.Minute)
	staging.promotions = map[string]UploadBlockPromotionRecord{
		fakeUploadPromotionKey("org-1", "upload-1", 0): {
			OrgID:       "org-1",
			UploadID:    "upload-1",
			BlockIndex:  0,
			BlockSHA256: "sha256-1",
			CommitID:    "commit-1",
			ClaimedAt:   now,
			AppliedAt:   &appliedAt,
		},
	}

	var publishCalls int
	handler.commitPublisher = func(orgID, repoID, commitID string) error {
		publishCalls++
		return nil
	}

	recovered, ok, err := handler.recoverPreparedUploadIfPossible("org-1", "repo-1", "upload-1", []uploadBlockPromotion{{BlockIndex: 0, BlockSHA256: "sha256-1"}})
	if err != nil {
		t.Fatalf("recoverPreparedUploadIfPossible failed: %v", err)
	}
	if !ok {
		t.Fatal("expected recovery to return closed upload")
	}
	if recovered.ActualFilename != "renamed.txt" {
		t.Fatalf("actual filename = %q, want renamed.txt", recovered.ActualFilename)
	}
	if publishCalls != 0 {
		t.Fatalf("publish calls = %d, want 0", publishCalls)
	}
}

func TestRecoverPreparedUploadIfPossibleReturnsPreparedCommitOnPendingPromotionMarker(t *testing.T) {
	handler := NewSeafHTTPHandler(nil, storage.NewManager(), nil, NewMockTokenStore(), nil, nil)
	staging := &fakeUploadStagingStore{}
	handler.SetUploadStagingStore(staging)
	now := time.Now().UTC()
	staging.sessions = append(staging.sessions, UploadSessionRecord{
		UploadID:       "upload-1",
		OrgID:          "org-1",
		RepoID:         "repo-1",
		Filename:       "file.txt",
		ActualFilename: "file.txt",
		CommitID:       "commit-1",
		State:          UploadSessionStatePromoting,
		CreatedAt:      now,
		UpdatedAt:      now,
		ExpiresAt:      now.Add(time.Hour),
	})
	staging.promotions = map[string]UploadBlockPromotionRecord{
		fakeUploadPromotionKey("org-1", "upload-1", 0): {
			OrgID:       "org-1",
			UploadID:    "upload-1",
			BlockIndex:  0,
			BlockSHA256: "sha256-1",
			CommitID:    "commit-1",
			ClaimedAt:   now,
		},
	}

	prepared, ok, err := handler.recoverPreparedUploadIfPossible("org-1", "repo-1", "upload-1", []uploadBlockPromotion{{BlockIndex: 0, BlockSHA256: "sha256-1"}})
	if err != nil {
		t.Fatalf("recoverPreparedUploadIfPossible failed: %v", err)
	}
	if ok {
		t.Fatal("expected pending marker to return a resumable prepared commit, not a recovered upload")
	}
	if prepared.CommitID != "commit-1" || prepared.ActualFilename != "file.txt" {
		t.Fatalf("prepared commit = %+v, want commit-1/file.txt", prepared)
	}
}

// TestRecoverPreparedUploadIfPossiblePurgesStalePromotionsOnSHAMismatch
// guards the E2E bug where a new revision of the same path/filename (same
// uploadID, different content) hit a closed session's promotion claims with
// the old BlockSHA256 and TryStartBlockPromotion later rejected with
// "promotion marker mismatch". The purge must happen lazily on detection so
// the new upload can register fresh markers, while the matching-content
// idempotent retry path still finds the live claims.
func TestRecoverPreparedUploadIfPossiblePurgesStalePromotionsOnSHAMismatch(t *testing.T) {
	handler := NewSeafHTTPHandler(nil, storage.NewManager(), nil, NewMockTokenStore(), nil, nil)
	staging := &fakeUploadStagingStore{}
	handler.SetUploadStagingStore(staging)
	now := time.Now().UTC()
	staging.sessions = append(staging.sessions, UploadSessionRecord{
		UploadID: "upload-1", OrgID: "org-1", RepoID: "repo-1",
		Filename: "file.txt", ActualFilename: "file.txt",
		CommitID: "commit-rev1", State: UploadSessionStateClosed,
		CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour),
	})
	staging.promotions = map[string]UploadBlockPromotionRecord{
		fakeUploadPromotionKey("org-1", "upload-1", 0): {
			OrgID: "org-1", UploadID: "upload-1", BlockIndex: 0,
			BlockSHA256: "sha256-rev1", CommitID: "commit-rev1",
			ClaimedAt: now, AppliedAt: &now,
		},
	}

	// Rev 2 with different content — same BlockIndex but different SHA.
	prepared, ok, err := handler.recoverPreparedUploadIfPossible("org-1", "repo-1", "upload-1",
		[]uploadBlockPromotion{{BlockIndex: 0, BlockSHA256: "sha256-rev2"}})
	if err != nil {
		t.Fatalf("recoverPreparedUploadIfPossible: %v", err)
	}
	if ok {
		t.Fatal("expected no recovery on SHA mismatch")
	}
	if prepared.CommitID != "" {
		t.Fatalf("expected empty prepared on SHA mismatch, got %+v", prepared)
	}
	if _, exists := staging.promotions[fakeUploadPromotionKey("org-1", "upload-1", 0)]; exists {
		t.Fatal("expected stale rev-1 promotion to be purged on mismatch with closed session")
	}
}

// TestRecoverPreparedUploadIfPossiblePurgesStalePromotionsOnLengthMismatch
// covers the cardinality variant: a different chunking on the new upload
// (e.g. file size changed dramatically) leaves the stale claims unusable.
func TestRecoverPreparedUploadIfPossiblePurgesStalePromotionsOnLengthMismatch(t *testing.T) {
	handler := NewSeafHTTPHandler(nil, storage.NewManager(), nil, NewMockTokenStore(), nil, nil)
	staging := &fakeUploadStagingStore{}
	handler.SetUploadStagingStore(staging)
	now := time.Now().UTC()
	staging.sessions = append(staging.sessions, UploadSessionRecord{
		UploadID: "upload-1", OrgID: "org-1", RepoID: "repo-1",
		Filename: "file.txt", CommitID: "commit-rev1",
		State:     UploadSessionStateClosed,
		CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour),
	})
	staging.promotions = map[string]UploadBlockPromotionRecord{
		fakeUploadPromotionKey("org-1", "upload-1", 0): {
			OrgID: "org-1", UploadID: "upload-1", BlockIndex: 0,
			BlockSHA256: "sha256-rev1", CommitID: "commit-rev1",
			ClaimedAt: now, AppliedAt: &now,
		},
	}

	// Rev 2 splits into 3 blocks instead of 1.
	_, _, err := handler.recoverPreparedUploadIfPossible("org-1", "repo-1", "upload-1",
		[]uploadBlockPromotion{
			{BlockIndex: 0, BlockSHA256: "a"},
			{BlockIndex: 1, BlockSHA256: "b"},
			{BlockIndex: 2, BlockSHA256: "c"},
		})
	if err != nil {
		t.Fatalf("recoverPreparedUploadIfPossible: %v", err)
	}
	if _, exists := staging.promotions[fakeUploadPromotionKey("org-1", "upload-1", 0)]; exists {
		t.Fatal("expected stale rev-1 promotion to be purged on cardinality mismatch")
	}
}

func TestRecoverPreparedUploadIfPossiblePurgesStalePromotionsForCleanupPendingWithoutCommit(t *testing.T) {
	handler := NewSeafHTTPHandler(nil, storage.NewManager(), nil, NewMockTokenStore(), nil, nil)
	staging := &fakeUploadStagingStore{}
	handler.SetUploadStagingStore(staging)
	now := time.Now().UTC()
	staging.sessions = append(staging.sessions, UploadSessionRecord{
		UploadID: "upload-1", OrgID: "org-1", RepoID: "repo-1",
		Filename: "file.txt", State: UploadSessionStateCleanupPending,
		CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour),
		LastError: "block 0 promotion marker mismatch",
	})
	staging.promotions = map[string]UploadBlockPromotionRecord{
		fakeUploadPromotionKey("org-1", "upload-1", 0): {
			OrgID: "org-1", UploadID: "upload-1", BlockIndex: 0,
			BlockSHA256: "sha256-rev1", CommitID: "commit-rev1",
			ClaimedAt: now, AppliedAt: &now,
		},
	}

	prepared, ok, err := handler.recoverPreparedUploadIfPossible("org-1", "repo-1", "upload-1",
		[]uploadBlockPromotion{{BlockIndex: 0, BlockSHA256: "sha256-rev2"}})
	if err != nil {
		t.Fatalf("recoverPreparedUploadIfPossible: %v", err)
	}
	if ok {
		t.Fatal("expected cleanup_pending session with blank commit to return no recovery")
	}
	if prepared.CommitID != "" {
		t.Fatalf("expected empty prepared result, got %+v", prepared)
	}
	if _, exists := staging.promotions[fakeUploadPromotionKey("org-1", "upload-1", 0)]; exists {
		t.Fatal("expected stale promotion to be purged for cleanup_pending session without commit")
	}
}

// TestRecoverPreparedUploadIfPossiblePreservesPromotionsForIdempotentRetry
// verifies that the lazy-purge does NOT trigger when the new upload matches
// the closed session's content — that's a retry of an upload whose response
// was lost in transit, and the claims are the only signal we have to return
// success without recreating storage and accounting work.
func TestRecoverPreparedUploadIfPossiblePreservesPromotionsForIdempotentRetry(t *testing.T) {
	handler := NewSeafHTTPHandler(nil, storage.NewManager(), nil, NewMockTokenStore(), nil, nil)
	staging := &fakeUploadStagingStore{}
	handler.SetUploadStagingStore(staging)
	now := time.Now().UTC()
	staging.sessions = append(staging.sessions, UploadSessionRecord{
		UploadID: "upload-1", OrgID: "org-1", RepoID: "repo-1",
		Filename: "file.txt", ActualFilename: "file.txt",
		CommitID: "commit-1", State: UploadSessionStateClosed,
		CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour),
	})
	applied := now.Add(time.Minute)
	staging.promotions = map[string]UploadBlockPromotionRecord{
		fakeUploadPromotionKey("org-1", "upload-1", 0): {
			OrgID: "org-1", UploadID: "upload-1", BlockIndex: 0,
			BlockSHA256: "sha256-1", CommitID: "commit-1",
			ClaimedAt: now, AppliedAt: &applied,
		},
	}

	prepared, ok, err := handler.recoverPreparedUploadIfPossible("org-1", "repo-1", "upload-1",
		[]uploadBlockPromotion{{BlockIndex: 0, BlockSHA256: "sha256-1"}})
	if err != nil {
		t.Fatalf("recoverPreparedUploadIfPossible: %v", err)
	}
	if !ok {
		t.Fatal("expected idempotent recovery to return ok=true")
	}
	if prepared.CommitID != "commit-1" {
		t.Fatalf("expected recovered commit-1, got %q", prepared.CommitID)
	}
	if _, exists := staging.promotions[fakeUploadPromotionKey("org-1", "upload-1", 0)]; !exists {
		t.Fatal("expected matching promotion to be preserved for idempotent retry")
	}
}

// TestCloseRecoveredUploadSessionDoesNotTouchPromotions documents that the
// recovery close intentionally leaves promotion rows in place so a follow-up
// retry can recognise the upload as already done. They are purged lazily on
// the next conflicting upload.
func TestCloseRecoveredUploadSessionDoesNotTouchPromotions(t *testing.T) {
	handler := NewSeafHTTPHandler(nil, storage.NewManager(), nil, NewMockTokenStore(), nil, nil)
	staging := &fakeUploadStagingStore{}
	handler.SetUploadStagingStore(staging)
	now := time.Now().UTC()
	staging.promotions = map[string]UploadBlockPromotionRecord{
		fakeUploadPromotionKey("org-1", "upload-1", 0): {
			OrgID: "org-1", UploadID: "upload-1", BlockIndex: 0,
			BlockSHA256: "sha256-1", CommitID: "commit-1",
			ClaimedAt: now, AppliedAt: &now,
		},
	}

	if err := handler.closeRecoveredUploadSession(&UploadSessionRecord{
		OrgID: "org-1", UploadID: "upload-1", RepoID: "repo-1",
		Filename: "file.txt", CommitID: "commit-1",
	}); err != nil {
		t.Fatalf("closeRecoveredUploadSession: %v", err)
	}
	if _, exists := staging.promotions[fakeUploadPromotionKey("org-1", "upload-1", 0)]; !exists {
		t.Fatal("expected promotion claim to remain for idempotent-retry detection")
	}
}

func TestShouldPreserveUploadSession(t *testing.T) {
	tests := []struct {
		name     string
		session  *UploadSessionRecord
		wantKeep bool
	}{
		{
			name:     "closed commit preserved",
			session:  &UploadSessionRecord{State: UploadSessionStateClosed, CommitID: "commit-1"},
			wantKeep: true,
		},
		{
			name:     "promoting commit preserved",
			session:  &UploadSessionRecord{State: UploadSessionStatePromoting, CommitID: "commit-1"},
			wantKeep: true,
		},
		{
			name:     "cleanup pending with commit preserved",
			session:  &UploadSessionRecord{State: UploadSessionStateCleanupPending, CommitID: "commit-1"},
			wantKeep: true,
		},
		{
			name:     "cleanup pending without commit not preserved",
			session:  &UploadSessionRecord{State: UploadSessionStateCleanupPending},
			wantKeep: true,
		},
		{
			name:     "receiving not preserved",
			session:  &UploadSessionRecord{State: UploadSessionStateReceiving, CommitID: "commit-1"},
			wantKeep: false,
		},
		{
			name:     "nil session not preserved",
			session:  nil,
			wantKeep: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldPreserveUploadSession(tt.session); got != tt.wantKeep {
				t.Fatalf("shouldPreserveUploadSession() = %v, want %v", got, tt.wantKeep)
			}
		})
	}
}

// TestCloseRecoveredUploadSessionDoesNotPropagateRecoveryFlowsErrors
// verifies that a recovery flow does not return 500 to the user solely
// because purging promotions is logged-only — the session was upserted, the
// HEAD published, and no further write should be able to fail the request.
// This is enforced by closeRecoveredUploadSession not calling purge at all.
func TestCloseRecoveredUploadSessionDoesNotPropagateRecoveryFlowsErrors(t *testing.T) {
	handler := NewSeafHTTPHandler(nil, storage.NewManager(), nil, NewMockTokenStore(), nil, nil)
	// Use a staging store whose Delete* would fail. closeRecoveredUploadSession
	// must not invoke it — so the fake's promotionDeleteErr should never surface.
	staging := &fakeUploadStagingStore{promotionDeleteErr: errors.New("delete down")}
	handler.SetUploadStagingStore(staging)

	if err := handler.closeRecoveredUploadSession(&UploadSessionRecord{
		OrgID: "org-1", UploadID: "upload-1", RepoID: "repo-1",
		Filename: "file.txt", CommitID: "commit-1",
	}); err != nil {
		t.Fatalf("closeRecoveredUploadSession unexpectedly returned error: %v", err)
	}
}

func TestPublishPreparedUploadResumesPendingPromotionMarker(t *testing.T) {
	handler := NewSeafHTTPHandler(nil, storage.NewManager(), nil, NewMockTokenStore(), nil, nil)
	staging := &fakeUploadStagingStore{}
	handler.SetUploadStagingStore(staging)
	now := time.Now().UTC()
	staging.promotions = map[string]UploadBlockPromotionRecord{
		fakeUploadPromotionKey("org-1", "upload-1", 0): {
			OrgID:       "org-1",
			UploadID:    "upload-1",
			BlockIndex:  0,
			BlockSHA256: "sha256-1",
			CommitID:    "commit-1",
			ClaimedAt:   now,
		},
	}

	var promoted []string
	handler.blockPromoter = func(orgID, operationKey, blockID string, sizeBytes int, storageClass, storageKey string) error {
		promoted = append(promoted, blockID)
		return nil
	}
	var publishCalls int
	handler.commitPublisher = func(orgID, repoID, commitID string) error {
		publishCalls++
		return nil
	}

	err := handler.publishPreparedUpload("org-1", "repo-1", "upload-1", preparedUploadCommit{CommitID: "commit-1", ActualFilename: "file.txt"}, []uploadBlockPromotion{{
		BlockIndex:  0,
		BlockSHA256: "sha256-1",
	}})
	if err != nil {
		t.Fatalf("publishPreparedUpload failed: %v", err)
	}
	if len(promoted) != 1 || promoted[0] != "sha256-1" {
		t.Fatalf("promoted blocks = %v, want [sha256-1]", promoted)
	}
	if publishCalls != 1 {
		t.Fatalf("publish calls = %d, want 1", publishCalls)
	}
	promotion := staging.promotions[fakeUploadPromotionKey("org-1", "upload-1", 0)]
	if promotion.AppliedAt == nil {
		t.Fatal("expected pending promotion marker to be marked applied")
	}
}

func TestPublishPreparedUploadRollsBackResumedPendingPromotionOnPublishFailure(t *testing.T) {
	handler := NewSeafHTTPHandler(nil, storage.NewManager(), nil, NewMockTokenStore(), nil, nil)
	staging := &fakeUploadStagingStore{}
	handler.SetUploadStagingStore(staging)
	now := time.Now().UTC()
	staging.promotions = map[string]UploadBlockPromotionRecord{
		fakeUploadPromotionKey("org-1", "upload-1", 0): {
			OrgID:       "org-1",
			UploadID:    "upload-1",
			BlockIndex:  0,
			BlockSHA256: "sha256-1",
			CommitID:    "commit-1",
			ClaimedAt:   now,
		},
	}

	handler.blockPromoter = func(orgID, operationKey, blockID string, sizeBytes int, storageClass, storageKey string) error {
		return nil
	}
	var rolledBackOperationKey string
	var rolledBackBlockIDs []string
	handler.blockRollback = func(orgID, operationKey string, blockIDs []string) []string {
		rolledBackOperationKey = operationKey
		rolledBackBlockIDs = append([]string(nil), blockIDs...)
		return nil
	}
	handler.commitPublisher = func(orgID, repoID, commitID string) error {
		return errors.New("publish failed")
	}

	err := handler.publishPreparedUpload("org-1", "repo-1", "upload-1", preparedUploadCommit{CommitID: "commit-1", ActualFilename: "file.txt"}, []uploadBlockPromotion{{
		BlockIndex:  0,
		BlockSHA256: "sha256-1",
	}})
	if err == nil {
		t.Fatal("expected publishPreparedUpload to fail")
	}
	if rolledBackOperationKey == "" {
		t.Fatal("expected rollback operation key to be recorded")
	}
	if len(rolledBackBlockIDs) != 1 || rolledBackBlockIDs[0] != "sha256-1" {
		t.Fatalf("rolled back block IDs = %v, want [sha256-1]", rolledBackBlockIDs)
	}
	if _, ok := staging.promotions[fakeUploadPromotionKey("org-1", "upload-1", 0)]; ok {
		t.Fatal("expected resumed pending promotion claim to be released after publish failure")
	}
}

func TestRecoverPromotingUploadSessionPublishesWhenAllPromotionsApplied(t *testing.T) {
	handler := NewSeafHTTPHandler(nil, storage.NewManager(), nil, NewMockTokenStore(), nil, nil)
	staging := &fakeUploadStagingStore{}
	handler.SetUploadStagingStore(staging)
	now := time.Now().UTC()
	staging.sessions = append(staging.sessions, UploadSessionRecord{
		UploadID:       "upload-1",
		OrgID:          "org-1",
		RepoID:         "repo-1",
		UserID:         "user-1",
		Filename:       "file.txt",
		ActualFilename: "file.txt",
		CommitID:       "commit-1",
		State:          UploadSessionStatePromoting,
		CreatedAt:      now,
		UpdatedAt:      now,
		ExpiresAt:      now.Add(time.Hour),
	})
	appliedAt := now.Add(time.Minute)
	staging.promotions = map[string]UploadBlockPromotionRecord{
		fakeUploadPromotionKey("org-1", "upload-1", 0): {
			OrgID:       "org-1",
			UploadID:    "upload-1",
			BlockIndex:  0,
			BlockSHA256: "sha256-1",
			CommitID:    "commit-1",
			ClaimedAt:   now,
			AppliedAt:   &appliedAt,
		},
	}

	var publishCalls int
	handler.commitPublisher = func(orgID, repoID, commitID string) error {
		publishCalls++
		return nil
	}

	if err := handler.recoverPromotingUploadSession(&UploadSessionRecord{OrgID: "org-1", UploadID: "upload-1"}); err != nil {
		t.Fatalf("recoverPromotingUploadSession failed: %v", err)
	}
	if publishCalls != 1 {
		t.Fatalf("publish calls = %d, want 1", publishCalls)
	}
	latest := staging.sessions[len(staging.sessions)-1]
	if latest.State != UploadSessionStateClosed {
		t.Fatalf("latest session state = %q, want %q", latest.State, UploadSessionStateClosed)
	}
}

func TestRecoverPromotingUploadSessionMarksExpiredPendingPromotionCleanup(t *testing.T) {
	handler := NewSeafHTTPHandler(nil, storage.NewManager(), nil, NewMockTokenStore(), nil, nil)
	staging := &fakeUploadStagingStore{}
	handler.SetUploadStagingStore(staging)
	now := time.Now().UTC()
	staging.sessions = append(staging.sessions, UploadSessionRecord{
		UploadID:       "upload-1",
		OrgID:          "org-1",
		RepoID:         "repo-1",
		Filename:       "file.txt",
		ActualFilename: "file.txt",
		CommitID:       "commit-1",
		State:          UploadSessionStatePromoting,
		CreatedAt:      now.Add(-3 * time.Hour),
		UpdatedAt:      now.Add(-3 * time.Hour),
		ExpiresAt:      now.Add(-time.Minute),
	})
	staging.promotions = map[string]UploadBlockPromotionRecord{
		fakeUploadPromotionKey("org-1", "upload-1", 0): {
			OrgID:       "org-1",
			UploadID:    "upload-1",
			BlockIndex:  0,
			BlockSHA256: "sha256-1",
			CommitID:    "commit-1",
			ClaimedAt:   now.Add(-2 * time.Hour),
		},
	}

	if err := handler.recoverPromotingUploadSession(&UploadSessionRecord{OrgID: "org-1", UploadID: "upload-1"}); err != nil {
		t.Fatalf("recoverPromotingUploadSession failed: %v", err)
	}
	latest := staging.sessions[len(staging.sessions)-1]
	if latest.State != UploadSessionStateCleanupPending {
		t.Fatalf("latest session state = %q, want %q", latest.State, UploadSessionStateCleanupPending)
	}
	if !strings.Contains(latest.LastError, "still pending") {
		t.Fatalf("last error = %q, want pending marker message", latest.LastError)
	}
}

func TestRecoverPromotingUploadsForOrgRecoversLatestUniqueSessions(t *testing.T) {
	handler := NewSeafHTTPHandler(nil, storage.NewManager(), nil, NewMockTokenStore(), nil, nil)
	staging := &fakeUploadStagingStore{}
	handler.SetUploadStagingStore(staging)
	now := time.Now().UTC()
	appliedAt := now.Add(time.Minute)
	staging.sessions = append(staging.sessions,
		UploadSessionRecord{UploadID: "upload-1", OrgID: "org-1", RepoID: "repo-1", CommitID: "commit-1", State: UploadSessionStatePromoting, UpdatedAt: now.Add(-2 * time.Minute), ExpiresAt: now.Add(time.Hour)},
		UploadSessionRecord{UploadID: "upload-1", OrgID: "org-1", RepoID: "repo-1", CommitID: "commit-1", State: UploadSessionStatePromoting, UpdatedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour)},
		UploadSessionRecord{UploadID: "upload-2", OrgID: "org-1", RepoID: "repo-1", CommitID: "commit-2", State: UploadSessionStatePromoting, UpdatedAt: now, ExpiresAt: now.Add(time.Hour)},
	)
	staging.promotions = map[string]UploadBlockPromotionRecord{
		fakeUploadPromotionKey("org-1", "upload-1", 0): {OrgID: "org-1", UploadID: "upload-1", BlockIndex: 0, BlockSHA256: "sha256-1", CommitID: "commit-1", ClaimedAt: now, AppliedAt: &appliedAt},
		fakeUploadPromotionKey("org-1", "upload-2", 0): {OrgID: "org-1", UploadID: "upload-2", BlockIndex: 0, BlockSHA256: "sha256-2", CommitID: "commit-2", ClaimedAt: now, AppliedAt: &appliedAt},
	}

	var published []string
	handler.commitPublisher = func(orgID, repoID, commitID string) error {
		published = append(published, commitID)
		return nil
	}

	handler.recoverPromotingUploadsForOrg("org-1", 10)
	if len(published) != 2 {
		t.Fatalf("published commits = %v, want 2 commits", published)
	}
}

func TestHandleUploadChunked_FinalizeFailureMarksCleanupPending(t *testing.T) {
	tm := NewTokenManager(time.Hour)
	tokenStr, err := tm.CreateUploadToken("org-1", "repo-1", "/", "user-1")
	if err != nil {
		t.Fatalf("CreateUploadToken failed: %v", err)
	}
	staging := &fakeUploadStagingStore{}
	handler := NewSeafHTTPHandler(nil, storage.NewManager(), nil, tm, nil, nil)
	handler.SetUploadStagingStore(staging)
	handler.chunkedFinalize = func(c *gin.Context, token *AccessToken, upload *ChunkUpload, uploadID, parentDir, filename, storageKey string, totalSize int64, replace bool) (finalizeUploadResult, error) {
		return finalizeUploadResult{}, io.ErrUnexpectedEOF
	}

	r := gin.New()
	r.POST("/seafhttp/upload-api/:token", handler.HandleUpload)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", "broken.txt")
	_, _ = part.Write([]byte("hello"))
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/seafhttp/upload-api/"+tokenStr, &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Content-Range", "bytes 0-4/5")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	if len(staging.sessions) < 3 {
		t.Fatalf("expected receiving/promoting/cleanup_pending session writes, got %d", len(staging.sessions))
	}
	if staging.sessions[len(staging.sessions)-1].State != UploadSessionStateCleanupPending {
		t.Fatalf("final state = %q, want %q", staging.sessions[len(staging.sessions)-1].State, UploadSessionStateCleanupPending)
	}
	if staging.sessions[len(staging.sessions)-1].LastError == "" {
		t.Fatal("expected cleanup_pending state to capture last error")
	}
	chunkManager.CleanupUpload(tokenStr, "broken.txt")
}

func TestSeafHTTPHandlerDownloadInvalidToken(t *testing.T) {
	tokenStore := NewMockTokenStore()
	handler := NewSeafHTTPHandler(nil, nil, nil, tokenStore, nil, nil)

	r := gin.New()
	r.GET("/seafhttp/files/:token/*filepath", handler.HandleDownload)

	req, _ := http.NewRequest("GET", "/seafhttp/files/invalid-token/file.txt", nil)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestSeafHTTPHandlerDownloadNoStorage(t *testing.T) {
	tokenStore := NewMockTokenStore()
	tokenStore.CreateDownloadToken("org1", "repo1", "/file.txt", "user1")
	handler := NewSeafHTTPHandler(nil, nil, nil, tokenStore, nil, nil) // nil storage

	r := gin.New()
	r.GET("/seafhttp/files/:token/*filepath", handler.HandleDownload)

	req, _ := http.NewRequest("GET", "/seafhttp/files/mock-download-token/file.txt", nil)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestSeafHTTPHandlerDownloadWithStorageManagerObjectFallback(t *testing.T) {
	tokenStore := NewMockTokenStore()
	tokenStore.CreateDownloadToken("org1", "repo1", "/file.txt", "user1")
	manager := storage.NewManager()
	manager.SetDefaultClass("hot-s3-eu")
	manager.RegisterBackend("hot-s3-eu", &mockObjectStore{data: []byte("hello")}, "")
	handler := NewSeafHTTPHandler(nil, manager, nil, tokenStore, nil, nil)

	r := gin.New()
	r.GET("/seafhttp/files/:token/*filepath", handler.HandleDownload)

	req, _ := http.NewRequest("GET", "/seafhttp/files/mock-download-token/file.txt", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if w.Body.String() != "hello" {
		t.Fatalf("body = %q, want %q", w.Body.String(), "hello")
	}
}

func TestGenerateFileID(t *testing.T) {
	id1 := generateFileID("key1")
	id2 := generateFileID("key2")

	// Should be 40 hex chars (20 bytes)
	if len(id1) != 40 {
		t.Errorf("id length = %d, want 40", len(id1))
	}

	// Should be unique (random)
	if id1 == id2 {
		t.Error("generateFileID should produce unique IDs")
	}
}

func TestBytesReader(t *testing.T) {
	data := []byte("hello world")
	reader := newBytesReader(data)

	// Read in parts
	buf := make([]byte, 5)
	n, err := reader.Read(buf)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if n != 5 {
		t.Errorf("n = %d, want 5", n)
	}
	if string(buf) != "hello" {
		t.Errorf("buf = %q, want hello", buf)
	}

	// Read rest
	buf = make([]byte, 10)
	n, err = reader.Read(buf)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if n != 6 {
		t.Errorf("n = %d, want 6", n)
	}
	if string(buf[:n]) != " world" {
		t.Errorf("buf = %q, want ' world'", buf[:n])
	}

	// Read at EOF
	n, err = reader.Read(buf)
	if err != io.EOF {
		t.Errorf("err = %v, want EOF", err)
	}
	if n != 0 {
		t.Errorf("n = %d, want 0", n)
	}
}

func TestBytesReaderEmpty(t *testing.T) {
	reader := newBytesReader([]byte{})
	buf := make([]byte, 10)

	n, err := reader.Read(buf)
	if err != io.EOF {
		t.Errorf("err = %v, want EOF", err)
	}
	if n != 0 {
		t.Errorf("n = %d, want 0", n)
	}
}

// ============================================================================
// sanitizeFilename Tests
// ============================================================================

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"normal filename", "test.txt", "test.txt"},
		{"filename with spaces", "my file.txt", "my_file.txt"},
		{"filename with slashes", "path/to/file.txt", "path_to_file.txt"},
		{"filename with special chars", "file@#$%.txt", "file____.txt"},
		{"filename with dots and hyphens", "my-file.v2.tar.gz", "my-file.v2.tar.gz"},
		{"empty string", "", ""},
		{"filename with unicode", "文件.txt", "__.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeFilename(tt.input)
			if result != tt.expected {
				t.Errorf("sanitizeFilename(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// ============================================================================
// parseContentRange Tests
// ============================================================================

func TestParseContentRange(t *testing.T) {
	tests := []struct {
		name      string
		header    string
		wantStart int64
		wantEnd   int64
		wantTotal int64
		wantOK    bool
	}{
		{"valid range", "bytes 0-1023/5000", 0, 1023, 5000, true},
		{"middle chunk", "bytes 1024-2047/5000", 1024, 2047, 5000, true},
		{"last chunk", "bytes 4096-4999/5000", 4096, 4999, 5000, true},
		{"empty header", "", 0, 0, 0, false},
		{"invalid format", "invalid", 0, 0, 0, false},
		{"missing bytes prefix", "0-100/200", 0, 0, 0, false},
		{"single byte", "bytes 0-0/1", 0, 0, 1, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, end, total, ok := parseContentRange(tt.header)
			if ok != tt.wantOK {
				t.Errorf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok {
				if start != tt.wantStart {
					t.Errorf("start = %d, want %d", start, tt.wantStart)
				}
				if end != tt.wantEnd {
					t.Errorf("end = %d, want %d", end, tt.wantEnd)
				}
				if total != tt.wantTotal {
					t.Errorf("total = %d, want %d", total, tt.wantTotal)
				}
			}
		})
	}
}

// ============================================================================
// OneTimeLoginToken Tests
// ============================================================================

func TestTokenManagerOneTimeLoginToken(t *testing.T) {
	tm := NewTokenManager(1 * time.Hour)

	tokenStr, err := tm.CreateOneTimeLoginToken("user-1", "org-1", "auth-token-xyz")
	if err != nil {
		t.Fatalf("CreateOneTimeLoginToken failed: %v", err)
	}
	if tokenStr == "" {
		t.Error("token string should not be empty")
	}

	// Consume the token
	authToken, err := tm.ConsumeOneTimeLoginToken(tokenStr)
	if err != nil {
		t.Fatalf("ConsumeOneTimeLoginToken failed: %v", err)
	}
	if authToken != "auth-token-xyz" {
		t.Errorf("authToken = %q, want %q", authToken, "auth-token-xyz")
	}

	// Token should be consumed (single-use)
	_, err = tm.ConsumeOneTimeLoginToken(tokenStr)
	if err == nil {
		t.Error("consumed token should return error on second use")
	}
}

func TestTokenManagerOneTimeLoginToken_NonExistent(t *testing.T) {
	tm := NewTokenManager(1 * time.Hour)

	_, err := tm.ConsumeOneTimeLoginToken("nonexistent")
	if err == nil {
		t.Error("expected error for non-existent token")
	}
}

func TestTokenManagerOneTimeLoginToken_Expired(t *testing.T) {
	tm := NewTokenManager(1 * time.Hour)

	tokenStr, _ := tm.CreateOneTimeLoginToken("user-1", "org-1", "auth-token")

	// Manually expire the token
	tm.mu.Lock()
	tm.tokens[tokenStr].ExpiresAt = time.Now().Add(-1 * time.Second)
	tm.mu.Unlock()

	_, err := tm.ConsumeOneTimeLoginToken(tokenStr)
	if err == nil {
		t.Error("expected error for expired token")
	}
}

func TestTokenManagerOneTimeLoginToken_WrongType(t *testing.T) {
	tm := NewTokenManager(1 * time.Hour)

	// Create a regular upload token
	uploadToken, _ := tm.CreateUploadToken("org-1", "repo-1", "/", "user-1")

	// Try to consume as one-time login token
	_, err := tm.ConsumeOneTimeLoginToken(uploadToken)
	if err == nil {
		t.Error("expected error when consuming non-login token")
	}
}

// ============================================================================
// ChunkManager Tests
// ============================================================================

func TestNewChunkManager(t *testing.T) {
	cm := NewChunkManager()
	if cm == nil {
		t.Fatal("NewChunkManager returned nil")
	}
	if cm.uploads == nil {
		t.Error("uploads map should be initialized")
	}
	if cm.tempDir == "" {
		t.Error("tempDir should not be empty")
	}
}

func TestChunkManagerGetOrCreateUpload(t *testing.T) {
	cm := NewChunkManager()

	upload, created, err := cm.GetOrCreateUpload("token1", "file.txt", "/", 1024)
	if err != nil {
		t.Fatalf("GetOrCreateUpload failed: %v", err)
	}
	if !created {
		t.Fatal("expected first call to create upload tracker")
	}
	if upload == nil {
		t.Fatal("upload should not be nil")
	}
	if upload.Token != "token1" {
		t.Errorf("Token = %q, want %q", upload.Token, "token1")
	}
	if upload.Filename != "file.txt" {
		t.Errorf("Filename = %q, want %q", upload.Filename, "file.txt")
	}
	if upload.TotalSize != 1024 {
		t.Errorf("TotalSize = %d, want %d", upload.TotalSize, 1024)
	}

	// Getting the same upload should return the existing one
	upload2, created, err := cm.GetOrCreateUpload("token1", "file.txt", "/", 1024)
	if err != nil {
		t.Fatalf("GetOrCreateUpload (2nd call) failed: %v", err)
	}
	if created {
		t.Fatal("expected second call to reuse existing upload tracker")
	}
	if upload2 != upload {
		t.Error("expected same upload instance for same key")
	}

	// Different key should create a new upload
	upload3, created, err := cm.GetOrCreateUpload("token2", "file.txt", "/", 2048)
	if err != nil {
		t.Fatalf("GetOrCreateUpload (different key) failed: %v", err)
	}
	if !created {
		t.Fatal("expected different key to create new upload tracker")
	}
	if upload3 == upload {
		t.Error("expected different upload instance for different key")
	}

	// Cleanup
	upload.Cleanup()
	upload3.Cleanup()
	cm.CleanupUpload("token1", "file.txt")
	cm.CleanupUpload("token2", "file.txt")
}

func TestChunkUploadWriteAndRead(t *testing.T) {
	cm := NewChunkManager()

	upload, _, err := cm.GetOrCreateUpload("token1", "test.bin", "/", 10)
	if err != nil {
		t.Fatalf("GetOrCreateUpload failed: %v", err)
	}
	defer func() {
		upload.Cleanup()
		cm.CleanupUpload("token1", "test.bin")
	}()

	// Write chunk
	err = upload.WriteChunk([]byte("hello"), 0, 4)
	if err != nil {
		t.Fatalf("WriteChunk failed: %v", err)
	}

	// Write second chunk
	err = upload.WriteChunk([]byte("world"), 5, 9)
	if err != nil {
		t.Fatalf("WriteChunk (2nd) failed: %v", err)
	}

	// Check completeness
	if !upload.IsComplete() {
		t.Error("upload should be complete after writing all bytes")
	}

	// Read content
	content, err := upload.GetContent()
	if err != nil {
		t.Fatalf("GetContent failed: %v", err)
	}
	if string(content) != "helloworld" {
		t.Errorf("content = %q, want %q", string(content), "helloworld")
	}
}

func TestChunkUploadIsComplete_Incomplete(t *testing.T) {
	cm := NewChunkManager()

	upload, _, err := cm.GetOrCreateUpload("token1", "test.bin", "/", 100)
	if err != nil {
		t.Fatalf("GetOrCreateUpload failed: %v", err)
	}
	defer func() {
		upload.Cleanup()
		cm.CleanupUpload("token1", "test.bin")
	}()

	if upload.IsComplete() {
		t.Error("empty upload should not be complete")
	}

	upload.WriteChunk([]byte("partial"), 0, 6)
	if upload.IsComplete() {
		t.Error("partially written upload should not be complete")
	}
}

func TestChunkUploadIsComplete_OutOfOrderLastChunk(t *testing.T) {
	cm := NewChunkManager()

	upload, _, err := cm.GetOrCreateUpload("token1", "test.bin", "/", 12)
	if err != nil {
		t.Fatalf("GetOrCreateUpload failed: %v", err)
	}
	defer func() {
		upload.Cleanup()
		cm.CleanupUpload("token1", "test.bin")
	}()

	if err := upload.WriteChunk([]byte("world!"), 6, 11); err != nil {
		t.Fatalf("WriteChunk last chunk failed: %v", err)
	}
	if upload.IsComplete() {
		t.Fatal("upload should not be complete when only the final range was received")
	}
	if upload.TryStartFinalization() {
		t.Fatal("upload should not start finalization with missing leading range")
	}

	if err := upload.WriteChunk([]byte("hello "), 0, 5); err != nil {
		t.Fatalf("WriteChunk first chunk failed: %v", err)
	}
	if !upload.IsComplete() {
		t.Fatal("upload should be complete after all ranges are received")
	}
	if !upload.TryStartFinalization() {
		t.Fatal("first finalization attempt should win")
	}
	if upload.TryStartFinalization() {
		t.Fatal("second finalization attempt should be rejected")
	}
	upload.ResetFinalization()
	if !upload.TryStartFinalization() {
		t.Fatal("retry should be able to restart finalization after a transient failure")
	}

	content, err := upload.GetContent()
	if err != nil {
		t.Fatalf("GetContent failed: %v", err)
	}
	if string(content) != "hello world!" {
		t.Errorf("content = %q, want %q", string(content), "hello world!")
	}
}

func TestChunkUploadWriteDuringFinalizationIsIdempotentOnly(t *testing.T) {
	cm := NewChunkManager()

	upload, _, err := cm.GetOrCreateUpload("token1", "test.bin", "/", 10)
	if err != nil {
		t.Fatalf("GetOrCreateUpload failed: %v", err)
	}
	defer func() {
		upload.Cleanup()
		cm.CleanupUpload("token1", "test.bin")
	}()

	if err := upload.WriteChunk([]byte("hello"), 0, 4); err != nil {
		t.Fatalf("WriteChunk first chunk failed: %v", err)
	}
	if err := upload.WriteChunk([]byte("world"), 5, 9); err != nil {
		t.Fatalf("WriteChunk second chunk failed: %v", err)
	}
	if !upload.TryStartFinalization() {
		t.Fatal("upload should enter finalization")
	}

	if err := upload.WriteChunk([]byte("XXXXX"), 5, 9); err != nil {
		t.Fatalf("duplicate range during finalization should be idempotent: %v", err)
	}
	content, err := upload.GetContent()
	if err != nil {
		t.Fatalf("GetContent failed: %v", err)
	}
	if string(content) != "helloworld" {
		t.Fatalf("duplicate finalizing write mutated temp file: got %q", string(content))
	}

	if err := upload.WriteChunk([]byte("!"), 10, 10); err == nil {
		t.Fatal("new range during finalization should be rejected")
	}

	upload.ResetFinalization()
	if err := upload.WriteChunk([]byte("XXXXX"), 5, 9); err != nil {
		t.Fatalf("duplicate range after failed finalization should stay idempotent: %v", err)
	}
	content, err = upload.GetContent()
	if err != nil {
		t.Fatalf("GetContent after reset failed: %v", err)
	}
	if string(content) != "helloworld" {
		t.Fatalf("post-reset duplicate write mutated temp file: got %q", string(content))
	}
}

func TestChunkUploadAccountBlockOnceSurvivesFinalizeRetry(t *testing.T) {
	cm := NewChunkManager()

	upload, _, err := cm.GetOrCreateUpload("token1", "test.bin", "/", 10)
	if err != nil {
		t.Fatalf("GetOrCreateUpload failed: %v", err)
	}
	defer func() {
		upload.Cleanup()
		cm.CleanupUpload("token1", "test.bin")
	}()

	calls := 0
	account := func() error {
		calls++
		return nil
	}
	if err := upload.AccountBlockOnce(0, "block-a", account); err != nil {
		t.Fatalf("first account failed: %v", err)
	}
	if err := upload.AccountBlockOnce(0, "block-a", account); err != nil {
		t.Fatalf("retry account failed: %v", err)
	}
	if calls != 1 {
		t.Fatalf("account called %d times, want 1", calls)
	}
	if err := upload.AccountBlockOnce(0, "block-b", account); err == nil {
		t.Fatal("same block position changing identity should be rejected")
	}
	accounted, err := upload.BlockAlreadyAccounted(0, "block-a")
	if err != nil {
		t.Fatalf("BlockAlreadyAccounted failed: %v", err)
	}
	if !accounted {
		t.Fatal("block position should be marked accounted")
	}
	accounted, err = upload.BlockAlreadyAccounted(1, "block-a")
	if err != nil {
		t.Fatalf("BlockAlreadyAccounted for missing position failed: %v", err)
	}
	if accounted {
		t.Fatal("missing block position should not be marked accounted")
	}
}

// ============================================================================
// TokenManager Concurrent Access Tests
// ============================================================================

func TestTokenManagerConcurrentAccess(t *testing.T) {
	tm := NewTokenManager(1 * time.Hour)
	done := make(chan struct{})

	// Concurrent token creation and deletion
	for i := 0; i < 50; i++ {
		go func() {
			tokenStr, _ := tm.CreateUploadToken("org", "repo", "/", "user")
			tm.GetToken(tokenStr, TokenTypeUpload)
			tm.DeleteToken(tokenStr)
			done <- struct{}{}
		}()
	}

	for i := 0; i < 50; i++ {
		<-done
	}
}

func TestRegisterSeafHTTPRoutes(t *testing.T) {
	tokenStore := NewMockTokenStore()
	handler := NewSeafHTTPHandler(nil, nil, nil, tokenStore, nil, nil)

	r := gin.New()
	handler.RegisterSeafHTTPRoutes(r)

	// Test that routes are registered by checking they don't 404
	tests := []struct {
		method string
		path   string
	}{
		{"POST", "/seafhttp/upload-api/test-token"},
		{"GET", "/seafhttp/files/test-token/file.txt"},
		{"GET", "/seafhttp/zip/test-token"},
	}

	for _, tt := range tests {
		req, _ := http.NewRequest(tt.method, tt.path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		// Should not be 404 (route exists, just may fail auth)
		if w.Code == http.StatusNotFound {
			t.Errorf("%s %s returned 404, route not registered", tt.method, tt.path)
		}
	}
}

func TestRegisterSeafHTTPRoutesZipRateLimit(t *testing.T) {
	tokenStore := NewMockTokenStore()
	handler := NewSeafHTTPHandler(nil, nil, nil, tokenStore, nil, nil)

	r := gin.New()
	rl := middleware.NewRateLimiter(rate.Every(time.Hour), 1)
	defer rl.Stop()
	handler.RegisterSeafHTTPRoutes(r, rl.Limit())

	req1 := httptest.NewRequest(http.MethodGet, "/seafhttp/zip/test-token", nil)
	req1.RemoteAddr = "198.51.100.10:12345"
	w1 := httptest.NewRecorder()
	r.ServeHTTP(w1, req1)
	if w1.Code != http.StatusUnauthorized {
		t.Fatalf("first status = %d, want %d", w1.Code, http.StatusUnauthorized)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/seafhttp/zip/test-token", nil)
	req2.RemoteAddr = "198.51.100.10:12345"
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)
	if w2.Code != http.StatusTooManyRequests {
		t.Fatalf("second status = %d, want %d", w2.Code, http.StatusTooManyRequests)
	}
}
