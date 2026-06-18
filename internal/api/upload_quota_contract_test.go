package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/Sesame-Disk/sesamefs/internal/traffic"
	"github.com/gin-gonic/gin"
)

type fakeAPIQuotaChecker struct {
	storageStatus traffic.QuotaStatus
	trafficStatus traffic.QuotaStatus
	storageBytes  []int64
	trafficBytes  []int64
	checkStorage  func(orgID, userID string, additionalBytes int64) (traffic.QuotaStatus, error)
	checkTraffic  func(orgID, userID, direction string, additionalBytes int64) (traffic.QuotaStatus, error)
}

func (f *fakeAPIQuotaChecker) CheckStorageQuota(orgID, userID string, additionalBytes int64) (traffic.QuotaStatus, error) {
	f.storageBytes = append(f.storageBytes, additionalBytes)
	if f.checkStorage != nil {
		return f.checkStorage(orgID, userID, additionalBytes)
	}
	return f.storageStatus, nil
}

func (f *fakeAPIQuotaChecker) CheckTrafficQuota(orgID, userID, direction string, additionalBytes int64) (traffic.QuotaStatus, error) {
	f.trafficBytes = append(f.trafficBytes, additionalBytes)
	if f.checkTraffic != nil {
		return f.checkTraffic(orgID, userID, direction, additionalBytes)
	}
	return f.trafficStatus, nil
}

func setAPIQuotaChecker(t *testing.T, checker apiQuotaChecker) {
	t.Helper()

	oldChecker := getAPIQuotaChecker
	getAPIQuotaChecker = func() apiQuotaChecker {
		return checker
	}
	t.Cleanup(func() {
		getAPIQuotaChecker = oldChecker
	})
}

func newMultipartUploadRequest(t *testing.T, path, filename string, content []byte) *http.Request {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("CreateFormFile() error = %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("part.Write() error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, path, &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

func decodeJSONObject(t *testing.T, body *bytes.Buffer) map[string]interface{} {
	t.Helper()

	var payload map[string]interface{}
	if err := json.Unmarshal(body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v, body=%q", err, body.String())
	}
	return payload
}

func TestHandleUploadQuotaContract_NonChunkedTrafficExceeded(t *testing.T) {
	setAPIQuotaChecker(t, &fakeAPIQuotaChecker{
		storageStatus: traffic.QuotaStatus{Allowed: true},
		trafficStatus: traffic.QuotaStatus{Allowed: false, Reason: "traffic-upload"},
	})

	tokenStore := NewMockTokenStore()
	if _, err := tokenStore.CreateUploadToken("org1", "repo1", "/", "user1"); err != nil {
		t.Fatalf("CreateUploadToken() error = %v", err)
	}
	handler := NewSeafHTTPHandler(nil, storage.NewManager(), nil, tokenStore, nil, nil)

	r := gin.New()
	r.POST("/seafhttp/upload-api/:token", handler.HandleUpload)

	req := newMultipartUploadRequest(t, "/seafhttp/upload-api/mock-upload-token", "test.txt", []byte("hello"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
	payload := decodeJSONObject(t, w.Body)
	if payload["error"] != "traffic quota exceeded" {
		t.Fatalf("error = %v, want traffic quota exceeded", payload["error"])
	}
	if payload["reason"] != "traffic-upload" {
		t.Fatalf("reason = %v, want traffic-upload", payload["reason"])
	}
}

func TestHandleUploadQuotaContract_ChunkedTrafficExceeded(t *testing.T) {
	setAPIQuotaChecker(t, &fakeAPIQuotaChecker{
		storageStatus: traffic.QuotaStatus{Allowed: true},
		trafficStatus: traffic.QuotaStatus{Allowed: false, Reason: "traffic-upload"},
	})

	tokenStore := NewMockTokenStore()
	if _, err := tokenStore.CreateUploadToken("org1", "repo1", "/", "user1"); err != nil {
		t.Fatalf("CreateUploadToken() error = %v", err)
	}
	handler := NewSeafHTTPHandler(nil, storage.NewManager(), nil, tokenStore, nil, nil)

	r := gin.New()
	r.POST("/seafhttp/upload-api/:token", handler.HandleUpload)

	req := newMultipartUploadRequest(t, "/seafhttp/upload-api/mock-upload-token", "test.txt", []byte("hello"))
	req.Header.Set("Content-Range", "bytes 0-4/5")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
	payload := decodeJSONObject(t, w.Body)
	if payload["error"] != "traffic quota exceeded" {
		t.Fatalf("error = %v, want traffic quota exceeded", payload["error"])
	}
	if payload["reason"] != "traffic-upload" {
		t.Fatalf("reason = %v, want traffic-upload", payload["reason"])
	}
}

func TestHandleUploadQuotaContract_ChunkedPrecheckUsesDeclaredTotal(t *testing.T) {
	checker := &fakeAPIQuotaChecker{
		storageStatus: traffic.QuotaStatus{Allowed: true},
		checkTraffic: func(orgID, userID, direction string, additionalBytes int64) (traffic.QuotaStatus, error) {
			if additionalBytes == 5 {
				return traffic.QuotaStatus{Allowed: false, Reason: "traffic-upload"}, nil
			}
			return traffic.QuotaStatus{Allowed: true}, nil
		},
	}
	setAPIQuotaChecker(t, checker)

	tokenStore := NewMockTokenStore()
	if _, err := tokenStore.CreateUploadToken("org1", "repo1", "/", "user1"); err != nil {
		t.Fatalf("CreateUploadToken() error = %v", err)
	}
	handler := NewSeafHTTPHandler(nil, storage.NewManager(), nil, tokenStore, nil, nil)

	r := gin.New()
	r.POST("/seafhttp/upload-api/:token", handler.HandleUpload)

	req := newMultipartUploadRequest(t, "/seafhttp/upload-api/mock-upload-token", "test.txt", []byte("hel"))
	req.Header.Set("Content-Range", "bytes 0-2/5")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
	payload := decodeJSONObject(t, w.Body)
	if payload["error"] != "traffic quota exceeded" {
		t.Fatalf("error = %v, want traffic quota exceeded", payload["error"])
	}
	if payload["reason"] != "traffic-upload" {
		t.Fatalf("reason = %v, want traffic-upload", payload["reason"])
	}
	if len(checker.trafficBytes) != 1 || checker.trafficBytes[0] != 5 {
		t.Fatalf("traffic precheck bytes = %v, want [5]", checker.trafficBytes)
	}
}

func TestHandleUploadChunkedInvalidContentRangeRejectedWith400(t *testing.T) {
	tests := []struct {
		name          string
		contentRange  string
		wantError     string
		wantTempFiles int
	}{
		{
			name:          "non-positive total zero",
			contentRange:  "bytes 0-7/0",
			wantError:     "chunked upload requires a positive total size in Content-Range",
			wantTempFiles: 0,
		},
		{
			name:          "non-positive total negative",
			contentRange:  "bytes 0-7/-1",
			wantError:     "chunked upload requires a positive total size in Content-Range",
			wantTempFiles: 0,
		},
		{
			name:          "end before start",
			contentRange:  "bytes 8-7/100",
			wantError:     "invalid chunk range: start=8 end=7",
			wantTempFiles: 0,
		},
		{
			name:          "end reaches declared total",
			contentRange:  "bytes 0-100/100",
			wantError:     "invalid chunk range: end=100 total=100",
			wantTempFiles: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setAPIQuotaChecker(t, &fakeAPIQuotaChecker{
				storageStatus: traffic.QuotaStatus{Allowed: true},
				trafficStatus: traffic.QuotaStatus{Allowed: true},
			})

			oldChunkManager := chunkManager
			testChunkManager, tempDir := newTestChunkManager(t)
			chunkManager = testChunkManager
			t.Cleanup(func() {
				chunkManager = oldChunkManager
			})

			tokenStore := NewMockTokenStore()
			if _, err := tokenStore.CreateUploadToken("org1", "repo1", "/", "user1"); err != nil {
				t.Fatalf("CreateUploadToken() error = %v", err)
			}
			handler := NewSeafHTTPHandler(nil, storage.NewManager(), nil, tokenStore, config.DefaultConfig(), nil)

			r := gin.New()
			r.POST("/seafhttp/upload-api/:token", handler.HandleUpload)

			req := newMultipartUploadRequest(t, "/seafhttp/upload-api/mock-upload-token", "test.txt", []byte("abc"))
			req.Header.Set("Content-Range", tc.contentRange)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
			}
			payload := decodeJSONObject(t, w.Body)
			if payload["error"] != tc.wantError {
				t.Fatalf("error = %v, want %s", payload["error"], tc.wantError)
			}
			testChunkManager.mu.RLock()
			trackerCount := len(testChunkManager.uploads)
			testChunkManager.mu.RUnlock()
			if trackerCount != 0 {
				t.Fatalf("tracker count = %d, want 0", trackerCount)
			}
			entries, err := os.ReadDir(tempDir)
			if err != nil {
				t.Fatalf("ReadDir() error = %v", err)
			}
			if len(entries) != tc.wantTempFiles {
				t.Fatalf("temp file count = %d, want %d", len(entries), tc.wantTempFiles)
			}
		})
	}
}

func TestHandleUploadChunkedMaxUploadMBRejectedWith413(t *testing.T) {
	setAPIQuotaChecker(t, &fakeAPIQuotaChecker{
		storageStatus: traffic.QuotaStatus{Allowed: true},
		trafficStatus: traffic.QuotaStatus{Allowed: true},
	})
	oldQuota := checkUploadStorageQuotaForCurrentHeadFn
	checkUploadStorageQuotaForCurrentHeadFn = func(h *SeafHTTPHandler, orgID, repoID, userID, parentDir, filename string, fileSize int64, replace bool) (int64, int64, error) {
		return fileSize, 1, nil
	}
	t.Cleanup(func() {
		checkUploadStorageQuotaForCurrentHeadFn = oldQuota
	})

	tokenStore := NewMockTokenStore()
	if _, err := tokenStore.CreateUploadToken("org1", "repo1", "/", "user1"); err != nil {
		t.Fatalf("CreateUploadToken() error = %v", err)
	}
	cfg := config.DefaultConfig()
	cfg.Server.MaxUploadMB = 1
	handler := NewSeafHTTPHandler(nil, storage.NewManager(), nil, tokenStore, cfg, nil)

	r := gin.New()
	r.POST("/seafhttp/upload-api/:token", handler.HandleUpload)

	req := newMultipartUploadRequest(t, "/seafhttp/upload-api/mock-upload-token", "test.txt", []byte("abc"))
	req.Header.Set("Content-Range", "bytes 0-2/1048577")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusRequestEntityTooLarge)
	}
	payload := decodeJSONObject(t, w.Body)
	if payload["error"] != "chunked upload exceeds configured max upload size" {
		t.Fatalf("error = %v, want chunked upload exceeds configured max upload size", payload["error"])
	}
}

func TestHandleUploadChunkedStagingBudgetRejectedWith507(t *testing.T) {
	setAPIQuotaChecker(t, &fakeAPIQuotaChecker{
		storageStatus: traffic.QuotaStatus{Allowed: true},
		trafficStatus: traffic.QuotaStatus{Allowed: true},
	})
	oldQuota := checkUploadStorageQuotaForCurrentHeadFn
	checkUploadStorageQuotaForCurrentHeadFn = func(h *SeafHTTPHandler, orgID, repoID, userID, parentDir, filename string, fileSize int64, replace bool) (int64, int64, error) {
		return fileSize, 1, nil
	}
	t.Cleanup(func() {
		checkUploadStorageQuotaForCurrentHeadFn = oldQuota
	})

	oldChunkManager := chunkManager
	testChunkManager, _ := newTestChunkManager(t)
	chunkManager = testChunkManager
	t.Cleanup(func() {
		chunkManager = oldChunkManager
	})

	seedUpload, err := chunkManager.GetOrCreateUploadByIdentityWithLimits("seed-token", "seed-ident", "/", "existing.bin", 8, 0, 10)
	if err != nil {
		t.Fatalf("seed upload error = %v", err)
	}
	t.Cleanup(func() {
		chunkManager.CleanupTrackedUpload(seedUpload)
	})

	tokenStore := NewMockTokenStore()
	if _, err := tokenStore.CreateUploadToken("org1", "repo1", "/", "user1"); err != nil {
		t.Fatalf("CreateUploadToken() error = %v", err)
	}
	cfg := config.DefaultConfig()
	cfg.SeafHTTP.ChunkedStagingMaxBytes = 10
	handler := NewSeafHTTPHandler(nil, storage.NewManager(), nil, tokenStore, cfg, nil)

	r := gin.New()
	r.POST("/seafhttp/upload-api/:token", handler.HandleUpload)

	req := newMultipartUploadRequest(t, "/seafhttp/upload-api/mock-upload-token", "test.txt", []byte("abc"))
	req.Header.Set("Content-Range", "bytes 0-2/3")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInsufficientStorage {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInsufficientStorage)
	}
	payload := decodeJSONObject(t, w.Body)
	if payload["error"] != "chunked upload staging capacity exceeded on this node" {
		t.Fatalf("error = %v, want chunked upload staging capacity exceeded on this node", payload["error"])
	}
}

func TestPutBlockQuotaContract_TrafficExceeded(t *testing.T) {
	setAPIQuotaChecker(t, &fakeAPIQuotaChecker{
		storageStatus: traffic.QuotaStatus{Allowed: true},
		trafficStatus: traffic.QuotaStatus{Allowed: false, Reason: "traffic-upload"},
	})

	r := setupSyncTestRouter()
	handler := &SyncHandler{}
	r.PUT("/seafhttp/repo/:repo_id/block/:block_id", handler.PutBlock)

	req := httptest.NewRequest(http.MethodPut, "/seafhttp/repo/repo-1/block/block-1", bytes.NewBufferString("hello"))
	req.ContentLength = int64(len("hello"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
	payload := decodeJSONObject(t, w.Body)
	if payload["error"] != "upload traffic quota exceeded" {
		t.Fatalf("error = %v, want upload traffic quota exceeded", payload["error"])
	}
	if _, exists := payload["reason"]; exists {
		t.Fatalf("did not expect reason field, got %v", payload["reason"])
	}
}

func TestPutBlockWithoutQuotaCheckerDoesNotPanic(t *testing.T) {
	setAPIQuotaChecker(t, nil)

	r := setupSyncTestRouter()
	handler := &SyncHandler{}
	r.PUT("/seafhttp/repo/:repo_id/block/:block_id", handler.PutBlock)

	req := httptest.NewRequest(http.MethodPut, "/seafhttp/repo/repo-1/block/block-1", bytes.NewBufferString("hello"))
	req.ContentLength = int64(len("hello"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
	payload := decodeJSONObject(t, w.Body)
	if payload["error"] != "block storage not available" {
		t.Fatalf("error = %v, want block storage not available", payload["error"])
	}
}
