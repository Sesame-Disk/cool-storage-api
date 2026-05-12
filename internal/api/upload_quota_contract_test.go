package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/Sesame-Disk/sesamefs/internal/traffic"
	"github.com/gin-gonic/gin"
)

type fakeAPIQuotaChecker struct {
	storageStatus traffic.QuotaStatus
	trafficStatus traffic.QuotaStatus
	storageBytes  []int64
	trafficBytes  []int64
}

func (f *fakeAPIQuotaChecker) CheckStorageQuota(orgID string, additionalBytes int64) (traffic.QuotaStatus, error) {
	f.storageBytes = append(f.storageBytes, additionalBytes)
	return f.storageStatus, nil
}

func (f *fakeAPIQuotaChecker) CheckTrafficQuota(orgID, userID, direction string, additionalBytes int64) (traffic.QuotaStatus, error) {
	f.trafficBytes = append(f.trafficBytes, additionalBytes)
	return f.trafficStatus, nil
}

func (f *fakeAPIQuotaChecker) CheckMaxUsers(orgID string) (traffic.QuotaStatus, error) {
	return traffic.QuotaStatus{Allowed: true}, nil
}

func setAPIQuotaChecker(t *testing.T, checker traffic.QuotaChecker) {
	t.Helper()
	oldChecker := traffic.GetChecker()
	traffic.SetChecker(checker)
	t.Cleanup(func() {
		traffic.SetChecker(oldChecker)
	})
}

func newMultipartUploadRequest(t *testing.T, path string) *http.Request {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "test.txt")
	if err != nil {
		t.Fatalf("CreateFormFile() error = %v", err)
	}
	if _, err := part.Write([]byte("hello")); err != nil {
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
	tokenStore.CreateUploadToken("org1", "repo1", "/", "user1")
	handler := NewSeafHTTPHandler(nil, storage.NewManager(), nil, tokenStore, nil, nil)

	r := gin.New()
	r.POST("/seafhttp/upload-api/:token", handler.HandleUpload)

	req := newMultipartUploadRequest(t, "/seafhttp/upload-api/mock-upload-token")
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
	tokenStore.CreateUploadToken("org1", "repo1", "/", "user1")
	handler := NewSeafHTTPHandler(nil, storage.NewManager(), nil, tokenStore, nil, nil)

	r := gin.New()
	r.POST("/seafhttp/upload-api/:token", handler.HandleUpload)

	req := newMultipartUploadRequest(t, "/seafhttp/upload-api/mock-upload-token")
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
		trafficStatus: traffic.QuotaStatus{Allowed: true},
	}
	setAPIQuotaChecker(t, checker)

	tokenStore := NewMockTokenStore()
	tokenStore.CreateUploadToken("org1", "repo1", "/", "user1")
	handler := NewSeafHTTPHandler(nil, storage.NewManager(), nil, tokenStore, nil, nil)

	r := gin.New()
	r.POST("/seafhttp/upload-api/:token", handler.HandleUpload)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "test.txt")
	if err != nil {
		t.Fatalf("CreateFormFile() error = %v", err)
	}
	if _, err := part.Write([]byte("hel")); err != nil {
		t.Fatalf("part.Write() error = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close() error = %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/seafhttp/upload-api/mock-upload-token", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Content-Range", "bytes 0-2/5")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if len(checker.storageBytes) != 1 || checker.storageBytes[0] != 5 {
		t.Fatalf("storage precheck bytes = %v, want [5]", checker.storageBytes)
	}
	if len(checker.trafficBytes) != 1 || checker.trafficBytes[0] != 5 {
		t.Fatalf("traffic precheck bytes = %v, want [5]", checker.trafficBytes)
	}
	chunkManager.CleanupUpload("mock-upload-token", "test.txt")
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
