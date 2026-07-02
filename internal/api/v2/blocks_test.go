package v2

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

// Direct block reads by bare hash are deliberately NOT part of the API: S3
// block keys are global content-addressed objects with no org scoping, and a
// bare-hash read cannot be authorized against a library permission, so the
// endpoint was a cross-tenant (and intra-org) content/existence oracle. Web
// downloads go through file paths; desktop sync uses the repo-scoped,
// permission-checked seafhttp block route. This test locks the removal so the
// routes cannot quietly come back without a repo-scoped design.
func TestDirectBlockReadRoutesAreNotRegistered(t *testing.T) {
	r := gin.New()
	rg := r.Group("/api/v2")
	RegisterBlockRoutes(rg, nil, nil, nil, nil, nil)

	validHash := strings.Repeat("a", 64)
	for _, method := range []string{"GET", "HEAD"} {
		req, _ := http.NewRequest(method, "/api/v2/blocks/"+validHash, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s /blocks/:hash status = %d, want %d (route must not exist)", method, w.Code, http.StatusNotFound)
		}
	}
}

// finding 9: the X-Block-Upload-Session header must win over the legacy
// ?session= query parameter, and the query parameter must still work as a
// fallback for any caller not yet updated.
func TestSessionIDFromRequest(t *testing.T) {
	tests := []struct {
		name   string
		header string
		query  string
		want   string
	}{
		{"header only", "sess-header", "", "sess-header"},
		{"query only", "", "sess-query", "sess-query"},
		{"header wins over query", "sess-header", "sess-query", "sess-header"},
		{"neither present", "", "", ""},
		{"whitespace-only header falls back to query", "   ", "sess-query", "sess-query"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := "/api/v2/blocks/check"
			if tt.query != "" {
				url += "?session=" + tt.query
			}
			req := httptest.NewRequest(http.MethodPost, url, nil)
			if tt.header != "" {
				req.Header.Set("X-Block-Upload-Session", tt.header)
			}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = req

			if got := sessionIDFromRequest(c); got != tt.want {
				t.Errorf("sessionIDFromRequest() = %q, want %q", got, tt.want)
			}
		})
	}
}

// finding 12: sessionPermissionCache must skip re-verification within the
// interval, always re-verify once it elapses, and never cache a denial (so a
// permission fix takes effect on the very next request instead of waiting out
// the interval).
func TestSessionPermissionCache_Allow(t *testing.T) {
	t.Run("caches a successful check for the interval", func(t *testing.T) {
		c := &sessionPermissionCache{}
		calls := 0
		verify := func() bool { calls++; return true }

		if !c.allow("sess-1", verify) {
			t.Fatal("first call should be allowed")
		}
		if !c.allow("sess-1", verify) {
			t.Fatal("second call within the interval should be allowed")
		}
		if calls != 1 {
			t.Errorf("verify called %d times, want 1 (second call should hit the cache)", calls)
		}
	})

	t.Run("never caches a denial", func(t *testing.T) {
		c := &sessionPermissionCache{}
		calls := 0
		verify := func() bool { calls++; return false }

		if c.allow("sess-2", verify) {
			t.Fatal("denial must not be allowed")
		}
		if c.allow("sess-2", verify) {
			t.Fatal("denial must not be allowed")
		}
		if calls != 2 {
			t.Errorf("verify called %d times, want 2 (a denial must never be cached)", calls)
		}
	})

	t.Run("re-verifies once the interval elapses", func(t *testing.T) {
		c := &sessionPermissionCache{checked: map[string]time.Time{
			"sess-3": time.Now().Add(-sessionPermissionRecheckInterval - time.Second),
		}}
		calls := 0
		verify := func() bool { calls++; return true }

		if !c.allow("sess-3", verify) {
			t.Fatal("expected allow")
		}
		if calls != 1 {
			t.Errorf("verify called %d times, want 1 (stale entry must trigger re-verification)", calls)
		}
	})

	t.Run("different sessions are independent", func(t *testing.T) {
		c := &sessionPermissionCache{}
		allowVerify := func() bool { return true }
		denyVerify := func() bool { return false }

		if !c.allow("sess-allowed", allowVerify) {
			t.Fatal("sess-allowed should be allowed")
		}
		if c.allow("sess-denied", denyVerify) {
			t.Fatal("sess-denied should be denied")
		}
		if !c.allow("sess-allowed", allowVerify) {
			t.Fatal("sess-allowed should still be cached as allowed")
		}
	})

	t.Run("sweeps stale entries once the cache is full", func(t *testing.T) {
		c := &sessionPermissionCache{checked: make(map[string]time.Time, sessionPermissionCacheSweepSize+1)}
		stale := time.Now().Add(-sessionPermissionRecheckInterval - time.Second)
		for i := 0; i < sessionPermissionCacheSweepSize; i++ {
			c.checked[strings.Repeat("x", 1)+string(rune(i))] = stale
		}
		verify := func() bool { return true }

		if !c.allow("sess-fresh", verify) {
			t.Fatal("expected allow")
		}
		if len(c.checked) >= sessionPermissionCacheSweepSize {
			t.Errorf("cache size = %d, want the stale entries swept below the sweep threshold", len(c.checked))
		}
		if _, ok := c.checked["sess-fresh"]; !ok {
			t.Error("the fresh entry that triggered the sweep must survive it")
		}
	})
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
	RegisterBlockRoutes(rg, nil, nil, nil, nil, nil)

	routes := []struct {
		method string
		path   string
	}{
		{"POST", "/api/v2/blocks/check"},
		{"POST", "/api/v2/blocks/upload"},
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
