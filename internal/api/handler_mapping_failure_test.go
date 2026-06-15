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
	oldPutAuto := putUploadedBlockAutoFn
	oldRegister := registerUploadedBlockAndMappingForUploadFn
	oldQuota := checkUploadStorageQuotaForCurrentHeadFn
	oldEncrypted := lookupLibraryEncryptedForUploadFn
	t.Cleanup(func() {
		putUploadedBlockAutoFn = oldPutAuto
		registerUploadedBlockAndMappingForUploadFn = oldRegister
		checkUploadStorageQuotaForCurrentHeadFn = oldQuota
		lookupLibraryEncryptedForUploadFn = oldEncrypted
	})

	putUploadedBlockAutoFn = func(ctx context.Context, blockStore *storage.BlockStore, hash string, data []byte) (string, error) {
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
	oldRegister := registerUploadedBlockAndMappingForSyncFn
	oldLookupClass := lookupLibraryStorageClassForSyncFn
	t.Cleanup(func() {
		syncBlockExistsFn = oldExists
		syncPutBlockDataFn = oldPut
		registerUploadedBlockAndMappingForSyncFn = oldRegister
		lookupLibraryStorageClassForSyncFn = oldLookupClass
	})

	syncBlockExistsFn = func(ctx context.Context, blockStore *storage.BlockStore, hash string) (bool, error) {
		return false, nil
	}
	syncPutBlockDataFn = func(ctx context.Context, blockStore *storage.BlockStore, block *storage.BlockData) (string, error) {
		return block.Hash, nil
	}
	registerUploadedBlockAndMappingForSyncFn = func(database *db.DB, orgID, repoID, internalBlockID, operationID string, sizeBytes int, storageClass, storageKey, externalBlockID string) error {
		return fmt.Errorf("mapping failed: %w", v2.ErrBlockMappingWriteFailed)
	}
	lookupLibraryStorageClassForSyncFn = func(h *SyncHandler, orgID, repoID string) string {
		return ""
	}

	r := setupSyncTestRouter()
	handler := &SyncHandler{blockStore: &storage.BlockStore{}, db: &db.DB{}}
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

func TestSeafHTTPHandleUploadFallbackPreservesObjectStoreStorageClass(t *testing.T) {
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
	var gotStorageClass string
	registerUploadedBlockAndMappingForUploadFn = func(database *db.DB, orgID, repoID, internalBlockID, operationID string, sizeBytes int, storageClass, storageKey, externalBlockID string) error {
		gotStorageClass = storageClass
		return fmt.Errorf("stop after register")
	}

	manager := storage.NewManager()
	manager.RegisterBackend("hot-fallback", &mockObjectStore{}, "")
	manager.SetDefaultClass("hot-fallback")

	tokenStore := NewMockTokenStore()
	if _, err := tokenStore.CreateUploadToken("org1", "repo1", "/", "user1"); err != nil {
		t.Fatalf("CreateUploadToken() error = %v", err)
	}

	handler := NewSeafHTTPHandler(nil, manager, nil, tokenStore, nil, nil)
	r := gin.New()
	r.POST("/seafhttp/upload-api/:token", handler.HandleUpload)

	req := newMultipartUploadRequest(t, "/seafhttp/upload-api/mock-upload-token", "test.txt", []byte("hello"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if gotStorageClass != "hot-fallback" {
		t.Fatalf("register storage_class = %q, want %q", gotStorageClass, "hot-fallback")
	}
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	if got := decodeJSONObject(t, w.Body)["error"]; got != "failed to store block metadata" {
		t.Fatalf("error = %v, want failed to store block metadata", got)
	}
}
