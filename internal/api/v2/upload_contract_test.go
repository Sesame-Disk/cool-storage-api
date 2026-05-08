package v2

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/traffic"
	"github.com/gin-gonic/gin"
)

type observedQuotaChecker struct {
	storageStatus traffic.QuotaStatus
	trafficStatus traffic.QuotaStatus
	storageCalled bool
	trafficCalled bool
}

func (f *observedQuotaChecker) CheckStorageQuota(orgID string, additionalBytes int64) (traffic.QuotaStatus, error) {
	f.storageCalled = true
	return f.storageStatus, nil
}

func (f *observedQuotaChecker) CheckTrafficQuota(orgID, userID, direction string, additionalBytes int64) (traffic.QuotaStatus, error) {
	f.trafficCalled = true
	return f.trafficStatus, nil
}

func (f *observedQuotaChecker) CheckMaxUsers(orgID string) (traffic.QuotaStatus, error) {
	return traffic.QuotaStatus{Allowed: true}, nil
}

func setV2QuotaChecker(t *testing.T, checker traffic.QuotaChecker) {
	t.Helper()
	oldChecker := traffic.GetChecker()
	traffic.SetChecker(checker)
	t.Cleanup(func() {
		traffic.SetChecker(oldChecker)
	})
}

func newV2MultipartUploadRequest(t *testing.T, path string) *http.Request {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("parent_dir", "/"); err != nil {
		t.Fatalf("WriteField() error = %v", err)
	}
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

func decodeV2JSONObject(t *testing.T, body *bytes.Buffer) map[string]interface{} {
	t.Helper()

	var payload map[string]interface{}
	if err := json.Unmarshal(body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal() error = %v, body=%q", err, body.String())
	}
	return payload
}

func TestUploadFileContract_NilDBGuardWinsBeforeQuota(t *testing.T) {
	checker := &observedQuotaChecker{
		storageStatus: traffic.QuotaStatus{Allowed: false, Reason: "storage"},
		trafficStatus: traffic.QuotaStatus{Allowed: false, Reason: "traffic-upload"},
	}
	setV2QuotaChecker(t, checker)

	r := setupTestRouter()
	handler := &FileHandler{}
	r.POST("/api/v2.1/repos/:repo_id/upload", handler.UploadFile)

	req := newV2MultipartUploadRequest(t, "/api/v2.1/repos/repo-1/upload")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
	}
	payload := decodeV2JSONObject(t, w.Body)
	if payload["error"] != "database not available" {
		t.Fatalf("error = %v, want database not available", payload["error"])
	}
	if checker.storageCalled || checker.trafficCalled {
		t.Fatalf("quota checker should not run before DB guard, got storage=%v traffic=%v", checker.storageCalled, checker.trafficCalled)
	}
}

func TestUploadBlockContract_TrafficExceededIncludesReason(t *testing.T) {
	setV2QuotaChecker(t, &observedQuotaChecker{
		storageStatus: traffic.QuotaStatus{Allowed: true},
		trafficStatus: traffic.QuotaStatus{Allowed: false, Reason: "traffic-upload"},
	})

	r := gin.New()
	r.Use(gin.Recovery())
	handler := &BlockHandler{
		config: &config.Config{
			Chunking: config.ChunkingConfig{
				Adaptive: config.AdaptiveConfig{AbsoluteMax: 1024},
			},
		},
	}
	r.POST("/api/v2/blocks/upload", func(c *gin.Context) {
		c.Set("org_id", "org-1")
		c.Set("user_id", "user-1")
		handler.UploadBlock(c)
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v2/blocks/upload", bytes.NewBufferString("hello"))
	req.ContentLength = int64(len("hello"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
	}
	payload := decodeV2JSONObject(t, w.Body)
	if payload["error"] != "traffic quota exceeded" {
		t.Fatalf("error = %v, want traffic quota exceeded", payload["error"])
	}
	if payload["reason"] != "traffic-upload" {
		t.Fatalf("reason = %v, want traffic-upload", payload["reason"])
	}
}
