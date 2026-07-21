package api

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	v2 "github.com/Sesame-Disk/sesamefs/internal/api/v2"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/gin-gonic/gin"
)

func TestSeafHTTPHandleUploadMappingFailureReturns500(t *testing.T) {
	oldPutDirect := putUploadedBlockAutoDirectForUploadFn
	oldProbe := probeUploadedBlockReuseForUploadFn
	oldRegister := registerUploadedBlockAndMappingForUploadFn
	oldQuota := checkUploadStorageQuotaForCurrentHeadFn
	oldEncrypted := lookupLibraryEncryptedForUploadFn
	t.Cleanup(func() {
		putUploadedBlockAutoDirectForUploadFn = oldPutDirect
		probeUploadedBlockReuseForUploadFn = oldProbe
		registerUploadedBlockAndMappingForUploadFn = oldRegister
		checkUploadStorageQuotaForCurrentHeadFn = oldQuota
		lookupLibraryEncryptedForUploadFn = oldEncrypted
	})

	probeUploadedBlockReuseForUploadFn = func(*db.DB, string, string) (db.BlockReuseProbe, error) {
		return db.BlockReuseProbe{Decision: db.BlockReuseNeedsPut}, nil
	}
	putUploadedBlockAutoDirectForUploadFn = func(ctx context.Context, blockStore *storage.BlockStore, hash string, data []byte) (string, error) {
		return hash, nil
	}
	checkUploadStorageQuotaForCurrentHeadFn = func(h *SeafHTTPHandler, orgID, repoID, userID, parentDir, filename string, fileSize int64, replace bool) (int64, int64, error) {
		return fileSize, 1, nil
	}
	lookupLibraryEncryptedForUploadFn = func(h *SeafHTTPHandler, orgID, repoID string) (bool, error) {
		return false, nil
	}
	registerUploadedBlockAndMappingForUploadFn = func(database *db.DB, orgID, repoID, internalBlockID, operationID string, sizeBytes int, storageClass, storageKey, externalBlockID string) error {
		return fmt.Errorf("mapping failed: %w", v2.ErrBlockMappingWriteFailed)
	}

	tokenStore := NewMockTokenStore()
	if _, err := tokenStore.CreateUploadToken("00000000-0000-0000-0000-000000000001", "repo1", "/", "user1"); err != nil {
		t.Fatalf("CreateUploadToken() error = %v", err)
	}

	handler := NewSeafHTTPHandler(&storage.S3Store{}, nil, nil, tokenStore, nil, nil)
	r := gin.New()
	r.POST("/seafhttp/upload-api/:token", handler.HandleUpload)

	req := newMultipartUploadRequest(t, "/seafhttp/upload-api/mock-upload-token", "test.txt", []byte("hello"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	if got := decodeJSONObject(t, w.Body)["error"]; got != "failed to create block mapping" {
		t.Fatalf("error = %v, want failed to create block mapping", got)
	}
}

func TestSeafHTTPHandleUploadFailsClosedWhenEncryptionStatusLookupFails(t *testing.T) {
	oldQuota := checkUploadStorageQuotaForCurrentHeadFn
	oldEncrypted := lookupLibraryEncryptedForUploadFn
	t.Cleanup(func() {
		checkUploadStorageQuotaForCurrentHeadFn = oldQuota
		lookupLibraryEncryptedForUploadFn = oldEncrypted
	})

	checkUploadStorageQuotaForCurrentHeadFn = func(h *SeafHTTPHandler, orgID, repoID, userID, parentDir, filename string, fileSize int64, replace bool) (int64, int64, error) {
		return fileSize, 1, nil
	}
	lookupLibraryEncryptedForUploadFn = func(h *SeafHTTPHandler, orgID, repoID string) (bool, error) {
		return false, fmt.Errorf("lookup failed")
	}

	tokenStore := NewMockTokenStore()
	if _, err := tokenStore.CreateUploadToken("org1", "repo1", "/", "user1"); err != nil {
		t.Fatalf("CreateUploadToken() error = %v", err)
	}

	handler := NewSeafHTTPHandler(&storage.S3Store{}, nil, nil, tokenStore, nil, nil)
	r := gin.New()
	r.POST("/seafhttp/upload-api/:token", handler.HandleUpload)

	req := newMultipartUploadRequest(t, "/seafhttp/upload-api/mock-upload-token", "test.txt", []byte("hello"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	if got := decodeJSONObject(t, w.Body)["error"]; got != "failed to check encryption status" {
		t.Fatalf("error = %v, want failed to check encryption status", got)
	}
}

func TestSyncPutBlockMappingFailureReturns500(t *testing.T) {
	oldExists := syncBlockExistsFn
	oldPut := syncPutBlockDataFn
	oldPutDirect := syncPutBlockAutoDirectFn
	oldProbe := syncProbeUploadedBlockReuseFn
	oldRegister := registerUploadedBlockAndMappingForSyncFn
	oldLookupClass := lookupLibraryStorageClassForSyncFn
	t.Cleanup(func() {
		syncBlockExistsFn = oldExists
		syncPutBlockDataFn = oldPut
		syncPutBlockAutoDirectFn = oldPutDirect
		syncProbeUploadedBlockReuseFn = oldProbe
		registerUploadedBlockAndMappingForSyncFn = oldRegister
		lookupLibraryStorageClassForSyncFn = oldLookupClass
	})

	syncBlockExistsFn = func(ctx context.Context, blockStore *storage.BlockStore, hash string) (bool, error) {
		return false, nil
	}
	syncPutBlockDataFn = func(ctx context.Context, blockStore *storage.BlockStore, block *storage.BlockData) (string, error) {
		return block.Hash, nil
	}
	syncProbeUploadedBlockReuseFn = func(*db.DB, string, string) (db.BlockReuseProbe, error) {
		return db.BlockReuseProbe{Decision: db.BlockReuseNeedsPut}, nil
	}
	syncPutBlockAutoDirectFn = func(ctx context.Context, blockStore *storage.BlockStore, hash string, data []byte) (string, error) {
		return hash, nil
	}
	registerUploadedBlockAndMappingForSyncFn = func(database *db.DB, orgID, repoID, internalBlockID, operationID string, sizeBytes int, storageClass, storageKey, externalBlockID string) error {
		return fmt.Errorf("mapping failed: %w", v2.ErrBlockMappingWriteFailed)
	}
	lookupLibraryStorageClassForSyncFn = func(h *SyncHandler, orgID, repoID string) string {
		return ""
	}

	r := setupSyncTestRouter()
	handler := &SyncHandler{storage: &storage.S3Store{}, db: &db.DB{}}
	r.PUT("/seafhttp/repo/:repo_id/block/:block_id", handler.PutBlock)

	req := httptest.NewRequest(http.MethodPut, "/seafhttp/repo/repo-1/block/0123456789012345678901234567890123456789", bytes.NewBufferString("hello"))
	req.ContentLength = int64(len("hello"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	if got := decodeJSONObject(t, w.Body)["error"]; got != "failed to create block mapping" {
		t.Fatalf("error = %v, want failed to create block mapping", got)
	}
}

func TestSyncPutBlockNeedsPutSkipsLegacyExistsAndUsesDirectPut(t *testing.T) {
	oldExists := syncBlockExistsFn
	oldPut := syncPutBlockDataFn
	oldPutDirect := syncPutBlockAutoDirectFn
	oldProbe := syncProbeUploadedBlockReuseFn
	oldRegister := registerUploadedBlockAndMappingForSyncFn
	oldLookupClass := lookupLibraryStorageClassForSyncFn
	t.Cleanup(func() {
		syncBlockExistsFn = oldExists
		syncPutBlockDataFn = oldPut
		syncPutBlockAutoDirectFn = oldPutDirect
		syncProbeUploadedBlockReuseFn = oldProbe
		registerUploadedBlockAndMappingForSyncFn = oldRegister
		lookupLibraryStorageClassForSyncFn = oldLookupClass
	})

	syncBlockExistsFn = func(ctx context.Context, blockStore *storage.BlockStore, hash string) (bool, error) {
		t.Fatal("legacy BlockExists path should not run when Cassandra marks the block needs-put")
		return false, nil
	}
	syncPutBlockDataFn = func(ctx context.Context, blockStore *storage.BlockStore, block *storage.BlockData) (string, error) {
		t.Fatal("legacy PutBlockData path should not run when Cassandra marks the block needs-put")
		return "", nil
	}
	directPutCalls := 0
	syncPutBlockAutoDirectFn = func(ctx context.Context, blockStore *storage.BlockStore, hash string, data []byte) (string, error) {
		directPutCalls++
		return hash, nil
	}
	syncProbeUploadedBlockReuseFn = func(database *db.DB, orgID, blockID string) (db.BlockReuseProbe, error) {
		return db.BlockReuseProbe{Decision: db.BlockReuseNeedsPut, StorageClass: "hot"}, nil
	}
	registerCalls := 0
	registerUploadedBlockAndMappingForSyncFn = func(database *db.DB, orgID, repoID, internalBlockID, operationID string, sizeBytes int, storageClass, storageKey, externalBlockID string) error {
		registerCalls++
		return nil
	}
	lookupLibraryStorageClassForSyncFn = func(h *SyncHandler, orgID, repoID string) string {
		return ""
	}

	r := setupSyncTestRouter()
	handler := &SyncHandler{storage: &storage.S3Store{}, db: &db.DB{}}
	r.PUT("/seafhttp/repo/:repo_id/block/:block_id", handler.PutBlock)

	req := httptest.NewRequest(http.MethodPut, "/seafhttp/repo/repo-1/block/0123456789012345678901234567890123456789", bytes.NewBufferString("hello"))
	req.ContentLength = int64(len("hello"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if directPutCalls != 1 {
		t.Fatalf("directPutCalls = %d, want 1", directPutCalls)
	}
	if registerCalls != 1 {
		t.Fatalf("registerCalls = %d, want 1", registerCalls)
	}
}

func TestSeafHTTPHandleUploadFailsClosedWithoutS3BlockBackend(t *testing.T) {
	oldRegister := registerUploadedBlockAndMappingForUploadFn
	oldQuota := checkUploadStorageQuotaForCurrentHeadFn
	oldEncrypted := lookupLibraryEncryptedForUploadFn
	t.Cleanup(func() {
		registerUploadedBlockAndMappingForUploadFn = oldRegister
		checkUploadStorageQuotaForCurrentHeadFn = oldQuota
		lookupLibraryEncryptedForUploadFn = oldEncrypted
	})

	checkUploadStorageQuotaForCurrentHeadFn = func(h *SeafHTTPHandler, orgID, repoID, userID, parentDir, filename string, fileSize int64, replace bool) (int64, int64, error) {
		return fileSize, 1, nil
	}
	lookupLibraryEncryptedForUploadFn = func(h *SeafHTTPHandler, orgID, repoID string) (bool, error) {
		return false, nil
	}
	registerCalled := false
	registerUploadedBlockAndMappingForUploadFn = func(database *db.DB, orgID, repoID, internalBlockID, operationID string, sizeBytes int, storageClass, storageKey, externalBlockID string) error {
		registerCalled = true
		return nil
	}

	manager := storage.NewManager()
	manager.RegisterBackend("hot-fallback", &mockObjectStore{}, "")
	manager.SetDefaultClass("hot-fallback")

	tokenStore := NewMockTokenStore()
	if _, err := tokenStore.CreateUploadToken("00000000-0000-0000-0000-000000000001", "repo1", "/", "user1"); err != nil {
		t.Fatalf("CreateUploadToken() error = %v", err)
	}

	handler := NewSeafHTTPHandler(nil, manager, nil, tokenStore, nil, nil)
	r := gin.New()
	r.POST("/seafhttp/upload-api/:token", handler.HandleUpload)

	req := newMultipartUploadRequest(t, "/seafhttp/upload-api/mock-upload-token", "test.txt", []byte("hello"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if registerCalled {
		t.Fatal("block metadata must not be registered when no org-scoped S3 block backend is available")
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
	if got := decodeJSONObject(t, w.Body)["error"]; got != "block storage not available" {
		t.Fatalf("error = %v, want block storage not available", got)
	}
}
