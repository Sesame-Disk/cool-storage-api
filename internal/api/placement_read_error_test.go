package api

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/gin-gonic/gin"
)

func TestSyncPutBlockPlacementLookupFailureSkipsStorage(t *testing.T) {
	oldLookupClass := lookupLibraryStorageClassForSyncFn
	oldProbe := syncProbeUploadedBlockReuseFn
	oldPutDirect := syncPutBlockAutoDirectFn
	t.Cleanup(func() {
		lookupLibraryStorageClassForSyncFn = oldLookupClass
		syncProbeUploadedBlockReuseFn = oldProbe
		syncPutBlockAutoDirectFn = oldPutDirect
	})

	lookupLibraryStorageClassForSyncFn = func(*SyncHandler, string, string) (string, error) {
		return "", errors.New("placement lookup failed")
	}
	probeCalls := 0
	syncProbeUploadedBlockReuseFn = func(*db.DB, string, string) (db.BlockReuseProbe, error) {
		probeCalls++
		return db.BlockReuseProbe{Decision: db.BlockReuseNeedsPut}, nil
	}
	putCalls := 0
	syncPutBlockAutoDirectFn = func(context.Context, *storage.BlockStore, string, []byte) (string, error) {
		putCalls++
		return "", nil
	}

	r := setupSyncTestRouter()
	handler := &SyncHandler{storage: &storage.S3Store{}, db: &db.DB{}}
	r.PUT("/seafhttp/repo/:repo_id/block/:block_id", handler.PutBlock)
	req := httptest.NewRequest(http.MethodPut, "/seafhttp/repo/repo-1/block/0123456789012345678901234567890123456789", bytes.NewBufferString("hello"))
	req.ContentLength = int64(len("hello"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
	if probeCalls != 0 || putCalls != 0 {
		t.Fatalf("probe/put calls = %d/%d, want 0/0", probeCalls, putCalls)
	}
}

func TestSeafHTTPHandleUploadPlacementLookupFailureSkipsStorage(t *testing.T) {
	oldLookupClass := lookupLibraryStorageClassForSeafHTTPFn
	oldQuota := checkUploadStorageQuotaForCurrentHeadFn
	oldEncrypted := lookupLibraryEncryptedForUploadFn
	oldProbe := probeUploadedBlockReuseForUploadFn
	oldPutDirect := putUploadedBlockAutoDirectForUploadFn
	t.Cleanup(func() {
		lookupLibraryStorageClassForSeafHTTPFn = oldLookupClass
		checkUploadStorageQuotaForCurrentHeadFn = oldQuota
		lookupLibraryEncryptedForUploadFn = oldEncrypted
		probeUploadedBlockReuseForUploadFn = oldProbe
		putUploadedBlockAutoDirectForUploadFn = oldPutDirect
	})

	lookupLibraryStorageClassForSeafHTTPFn = func(context.Context, *SeafHTTPHandler, string, string) (string, error) {
		return "", errors.New("placement lookup failed")
	}
	checkUploadStorageQuotaForCurrentHeadFn = func(*SeafHTTPHandler, string, string, string, string, string, int64, bool) (int64, int64, error) {
		return 5, 1, nil
	}
	lookupLibraryEncryptedForUploadFn = func(*SeafHTTPHandler, string, string) (bool, error) {
		return false, nil
	}
	probeCalls := 0
	probeUploadedBlockReuseForUploadFn = func(*db.DB, string, string) (db.BlockReuseProbe, error) {
		probeCalls++
		return db.BlockReuseProbe{Decision: db.BlockReuseNeedsPut}, nil
	}
	putCalls := 0
	putUploadedBlockAutoDirectForUploadFn = func(context.Context, *storage.BlockStore, string, []byte) (string, error) {
		putCalls++
		return "", nil
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

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
	if probeCalls != 0 || putCalls != 0 {
		t.Fatalf("probe/put calls = %d/%d, want 0/0", probeCalls, putCalls)
	}
}

func TestPlacementResolversPreserveExplicitCanonicalClass(t *testing.T) {
	const orgID = "00000000-0000-0000-0000-000000000001"
	oldSyncLookup := lookupLibraryStorageClassForSyncFn
	oldSeafHTTPLookup := lookupLibraryStorageClassForSeafHTTPFn
	t.Cleanup(func() {
		lookupLibraryStorageClassForSyncFn = oldSyncLookup
		lookupLibraryStorageClassForSeafHTTPFn = oldSeafHTTPLookup
	})

	manager := storage.NewManager()
	manager.SetDefaultClass("hot-default")
	manager.RegisterBackend("hot-default", &storage.S3Store{}, "")
	manager.RegisterBackend("cold-canonical", &storage.S3Store{}, "")
	lookupLibraryStorageClassForSyncFn = func(*SyncHandler, string, string) (string, error) {
		return "cold-canonical", nil
	}
	lookupLibraryStorageClassForSeafHTTPFn = func(context.Context, *SeafHTTPHandler, string, string) (string, error) {
		return "cold-canonical", nil
	}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	if _, class, err := (&SyncHandler{storageManager: manager}).resolvePreferredBlockStore(c, orgID, "repo1"); err != nil || class != "cold-canonical" {
		t.Fatalf("Sync resolve class/error = %q/%v, want cold-canonical/nil", class, err)
	}
	if _, class, err := (&SeafHTTPHandler{storageManager: manager}).resolveLibraryBlockStore("", orgID, "repo1"); err != nil || class != "cold-canonical" {
		t.Fatalf("SeafHTTP resolve class/error = %q/%v, want cold-canonical/nil", class, err)
	}
}
