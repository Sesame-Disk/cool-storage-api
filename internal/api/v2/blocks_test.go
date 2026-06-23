package v2

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func TestCheckBlocks_InvalidJSON(t *testing.T) {
	r := gin.New()
	r.Use(gin.Recovery())

	h := &BlockHandler{blockStore: nil, storageManager: nil, config: nil}
	r.POST("/api/v2/blocks/check", h.CheckBlocks)

	req, _ := http.NewRequest("POST", "/api/v2/blocks/check", bytes.NewBufferString("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestCheckBlocks_EmptyHashes(t *testing.T) {
	r := gin.New()
	r.Use(gin.Recovery())

	h := &BlockHandler{blockStore: nil, storageManager: nil, config: nil}
	r.POST("/api/v2/blocks/check", h.CheckBlocks)

	body := CheckBlocksRequest{Hashes: []string{}}
	jsonBody, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", "/api/v2/blocks/check", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"] != "hashes array is required" {
		t.Errorf("error = %v, want 'hashes array is required'", resp["error"])
	}
}

func TestCheckBlocks_TooManyHashes(t *testing.T) {
	r := gin.New()
	r.Use(gin.Recovery())

	h := &BlockHandler{blockStore: nil, storageManager: nil, config: nil}
	r.POST("/api/v2/blocks/check", h.CheckBlocks)

	// Create 10001 hashes
	hashes := make([]string, 10001)
	for i := range hashes {
		hashes[i] = strings.Repeat("a", 64)
	}
	body := CheckBlocksRequest{Hashes: hashes}
	jsonBody, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", "/api/v2/blocks/check", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["error"] != "too many hashes, maximum is 10000" {
		t.Errorf("error = %v, want 'too many hashes, maximum is 10000'", resp["error"])
	}
}

func TestCheckBlocks_NilBlockStore(t *testing.T) {
	r := gin.New()
	r.Use(gin.Recovery())

	h := &BlockHandler{blockStore: nil, storageManager: nil, config: nil}
	r.POST("/api/v2/blocks/check", h.CheckBlocks)

	body := CheckBlocksRequest{Hashes: []string{strings.Repeat("a", 64)}}
	jsonBody, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", "/api/v2/blocks/check", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestDownloadBlock_InvalidHash(t *testing.T) {
	r := gin.New()

	h := &BlockHandler{blockStore: nil, storageManager: nil, config: nil}
	r.GET("/api/v2/blocks/:hash", h.DownloadBlock)

	tests := []struct {
		name       string
		hash       string
		wantStatus int
	}{
		{"too short", "abc123", http.StatusBadRequest},
		{"too long", strings.Repeat("a", 65), http.StatusBadRequest},
		{"exactly 63", strings.Repeat("a", 63), http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", "/api/v2/blocks/"+tt.hash, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

func TestDownloadBlock_NilBlockStore(t *testing.T) {
	r := gin.New()

	h := &BlockHandler{blockStore: nil, storageManager: nil, config: nil}
	r.GET("/api/v2/blocks/:hash", h.DownloadBlock)

	validHash := strings.Repeat("a", 64)
	req, _ := http.NewRequest("GET", "/api/v2/blocks/"+validHash, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestBlockExists_InvalidHash(t *testing.T) {
	r := gin.New()

	h := &BlockHandler{blockStore: nil, storageManager: nil, config: nil}
	r.HEAD("/api/v2/blocks/:hash", h.BlockExists)

	req, _ := http.NewRequest("HEAD", "/api/v2/blocks/short", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestBlockExists_NilBlockStore(t *testing.T) {
	r := gin.New()

	h := &BlockHandler{blockStore: nil, storageManager: nil, config: nil}
	r.HEAD("/api/v2/blocks/:hash", h.BlockExists)

	validHash := strings.Repeat("a", 64)
	req, _ := http.NewRequest("HEAD", "/api/v2/blocks/"+validHash, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", w.Code, http.StatusServiceUnavailable)
	}
}

func TestGetBlockStoreDoesNotFallBackToLegacyWhenStorageManagerFails(t *testing.T) {
	manager := storage.NewManager()
	manager.SetDefaultClass("hot-s3-eu")
	h := &BlockHandler{blockStore: &storage.BlockStore{}, storageManager: manager, config: nil}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v2/blocks/"+strings.Repeat("a", 64), nil)
	c.Request.Host = "files.example.com"

	blockStore, storageClass := h.getBlockStore(c)
	if blockStore != nil {
		t.Fatal("expected nil blockStore when storage manager cannot resolve a healthy backend")
	}
	if storageClass != "hot-s3-eu" {
		t.Fatalf("storageClass = %q, want %q", storageClass, "hot-s3-eu")
	}
}

func TestGetBlockStoreUsesForwardedHostForRegionRouting(t *testing.T) {
	manager := storage.NewManager()
	manager.SetDefaultClass("hot-s3-usa")
	manager.SetEndpointRegions(map[string]string{
		"eu.files.example.com": "eu",
	})
	manager.SetRegionClasses(map[string]storage.RegionClassConfig{
		"usa": {Hot: "hot-s3-usa"},
		"eu":  {Hot: "hot-s3-eu"},
	})
	manager.RegisterBackend("hot-s3-usa", &storage.S3Store{}, "")
	manager.RegisterBackend("hot-s3-eu", &storage.S3Store{}, "")
	h := &BlockHandler{storageManager: manager, config: nil}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v2/blocks/"+strings.Repeat("a", 64), nil)
	c.Request.Host = "sesamefs:8080"
	c.Request.Header.Set("X-Forwarded-Host", "eu.files.example.com")

	blockStore, storageClass := h.getBlockStore(c)
	if blockStore == nil {
		t.Fatal("expected blockStore, got nil")
	}
	if storageClass != "hot-s3-eu" {
		t.Fatalf("storageClass = %q, want %q", storageClass, "hot-s3-eu")
	}
}

func TestUploadBlock_NoContentLength(t *testing.T) {
	r := gin.New()
	r.Use(gin.Recovery())

	h := &BlockHandler{blockStore: nil, storageManager: nil, config: nil}
	r.POST("/api/v2/blocks/upload", h.UploadBlock)

	req, _ := http.NewRequest("POST", "/api/v2/blocks/upload", nil)
	req.ContentLength = 0
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestCheckBlocksRequest_JSONBinding(t *testing.T) {
	r := gin.New()
	r.POST("/test", func(c *gin.Context) {
		var req CheckBlocksRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"count": len(req.Hashes)})
	})

	tests := []struct {
		name       string
		body       string
		wantStatus int
	}{
		{
			name:       "valid hashes",
			body:       `{"hashes": ["abc123", "def456"]}`,
			wantStatus: http.StatusOK,
		},
		{
			name:       "missing hashes field",
			body:       `{}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "invalid json",
			body:       `{bad`,
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("POST", "/test", bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

func TestCheckBlocksResponse_JSONFormat(t *testing.T) {
	resp := CheckBlocksResponse{
		Existing: []string{"hash1", "hash2"},
		Missing:  []string{"hash3"},
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal CheckBlocksResponse: %v", err)
	}

	var decoded map[string]interface{}
	json.Unmarshal(data, &decoded)

	existing := decoded["existing"].([]interface{})
	if len(existing) != 2 {
		t.Errorf("existing count = %d, want 2", len(existing))
	}

	missing := decoded["missing"].([]interface{})
	if len(missing) != 1 {
		t.Errorf("missing count = %d, want 1", len(missing))
	}
}

func TestUploadBlockResponse_JSONFormat(t *testing.T) {
	resp := UploadBlockResponse{
		Hash: "abc123",
		Size: 1024,
		New:  true,
	}

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("failed to marshal UploadBlockResponse: %v", err)
	}

	var decoded map[string]interface{}
	json.Unmarshal(data, &decoded)

	if decoded["hash"] != "abc123" {
		t.Errorf("hash = %v, want abc123", decoded["hash"])
	}
	if decoded["size"] != float64(1024) {
		t.Errorf("size = %v, want 1024", decoded["size"])
	}
	if decoded["new"] != true {
		t.Errorf("new = %v, want true", decoded["new"])
	}
}

func TestRegisterBlockRoutes(t *testing.T) {
	r := gin.New()
	rg := r.Group("/api/v2")
	RegisterBlockRoutes(rg, nil, nil, nil, nil)

	routes := []struct {
		method string
		path   string
	}{
		{"POST", "/api/v2/blocks/check"},
		{"POST", "/api/v2/blocks/upload"},
		{"GET", "/api/v2/blocks/" + strings.Repeat("a", 64)},
		{"HEAD", "/api/v2/blocks/" + strings.Repeat("a", 64)},
	}

	for _, rt := range routes {
		req, _ := http.NewRequest(rt.method, rt.path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code == http.StatusNotFound {
			t.Errorf("route %s %s not registered", rt.method, rt.path)
		}
	}
}
