package v2

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
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

func TestCheckBlocks_InvalidSHA256(t *testing.T) {
	r := gin.New()
	r.Use(gin.Recovery())

	h := &BlockHandler{blockStore: nil, storageManager: nil, config: nil}
	r.POST("/api/v2/blocks/check", h.CheckBlocks)

	body := CheckBlocksRequest{Hashes: []string{strings.Repeat("g", 64)}}
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
	if resp["error"] != "hashes[0]: invalid sha256" {
		t.Errorf("error = %v, want 'hashes[0]: invalid sha256'", resp["error"])
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

// Direct block reads by bare hash are deliberately NOT part of the API: even
// with org-scoped physical keys, a bare-hash read cannot be authorized against
// a library permission, so the endpoint is an intra-org content/existence oracle. Web
// downloads go through file paths; desktop sync uses the repo-scoped,
// permission-checked seafhttp block route. This test locks the removal so the
// routes cannot quietly come back without a repo-scoped design.
func TestDirectBlockReadRoutesAreNotRegistered(t *testing.T) {
	r := gin.New()
	rg := r.Group("/api/v2")
	RegisterBlockRoutes(rg, nil, nil, nil, nil, nil, nil)

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

// finding 9: the X-Block-Upload-Session header is the only supported transport
// for a block-upload session id. The legacy ?session= query parameter is
// rejected explicitly so an outdated caller cannot silently fall back to the
// legacy no-session path.
func TestSessionIDFromRequest(t *testing.T) {
	tests := []struct {
		name   string
		header string
		query  string
		want   string
		legacy bool
	}{
		{"header only", "sess-header", "", "sess-header", false},
		{"query only rejected", "", "sess-query", "", true},
		{"header wins over query", "sess-header", "sess-query", "sess-header", false},
		{"neither present", "", "", "", false},
		{"whitespace-only header still rejects query transport", "   ", "sess-query", "", true},
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

			got, legacy := sessionIDFromRequest(c)
			if got != tt.want {
				t.Errorf("sessionIDFromRequest() id = %q, want %q", got, tt.want)
			}
			if legacy != tt.legacy {
				t.Errorf("sessionIDFromRequest() legacy = %v, want %v", legacy, tt.legacy)
			}
		})
	}
}

func TestCheckBlocks_SessionValidationUnavailable(t *testing.T) {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(func(c *gin.Context) {
		c.Set("org_id", "org-1")
		c.Set("user_id", "user-1")
		c.Next()
	})

	h := &BlockHandler{
		db:     nil,
		config: &config.Config{WebUploads: config.WebUploadsConfig{EnableWebBlockUpload: true}},
	}
	r.POST("/api/v2/blocks/check", h.CheckBlocks)

	body := CheckBlocksRequest{Hashes: []string{strings.Repeat("a", 64)}}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v2/blocks/check", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Block-Upload-Session", "sess-unavailable")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusServiceUnavailable, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v; body=%s", err, w.Body.String())
	}
	if resp["error"] != "upload session unavailable" {
		t.Errorf("error = %v, want 'upload session unavailable'", resp["error"])
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
			c.checked[fmt.Sprintf("stale-%d", i)] = stale
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

	t.Run("evicts oldest fresh entries to keep a hard cap during bursts", func(t *testing.T) {
		c := &sessionPermissionCache{checked: make(map[string]time.Time, sessionPermissionCacheSweepSize)}
		base := time.Now()
		for i := 0; i < sessionPermissionCacheSweepSize; i++ {
			c.checked[hexSessionID(i)] = base.Add(time.Duration(i) * time.Millisecond)
		}
		verifyCalls := 0
		verify := func() bool { verifyCalls++; return true }

		if !c.allow("sess-new", verify) {
			t.Fatal("expected allow")
		}
		if verifyCalls != 1 {
			t.Fatalf("verify called %d times, want 1", verifyCalls)
		}
		if len(c.checked) > sessionPermissionCacheSweepSize {
			t.Fatalf("cache size = %d, want <= %d hard cap", len(c.checked), sessionPermissionCacheSweepSize)
		}
		if len(c.checked) != sessionPermissionCacheTrimTo {
			t.Fatalf("cache size = %d, want trim target %d after burst eviction", len(c.checked), sessionPermissionCacheTrimTo)
		}
		if _, ok := c.checked["sess-new"]; !ok {
			t.Fatal("newly verified session should remain cached")
		}
		if _, ok := c.checked[hexSessionID(0)]; ok {
			t.Fatal("oldest fresh entry should have been evicted under burst pressure")
		}
	})
}

func hexSessionID(i int) string {
	return fmt.Sprintf("sess-%05d", i)
}

func TestDedupePreserveOrder(t *testing.T) {
	input := []string{"a", "b", "a", "c", "b", "d"}
	got := dedupePreserveOrder(input)
	want := []string{"a", "b", "c", "d"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestCheckBlocksReusableCandidatesParallel_ProbesMetadataFirst(t *testing.T) {
	origProbe := checkBlocksProbeReuseFn
	defer func() {
		checkBlocksProbeReuseFn = origProbe
	}()

	var probeCalls int
	checkBlocksProbeReuseFn = func(database *db.DB, orgID, hash string) (db.BlockReuseProbe, error) {
		probeCalls++
		if hash == "reusable" {
			return db.BlockReuseProbe{Decision: db.BlockReuseReusable}, nil
		}
		return db.BlockReuseProbe{Decision: db.BlockReuseNeedsPut}, nil
	}

	reusableByHash, reusableHashes, err := checkBlocksReusableCandidatesParallel(
		context.Background(),
		nil,
		"org",
		[]string{"new", "reusable"},
		2,
	)
	if err != nil {
		t.Fatalf("checkBlocksReusableCandidatesParallel returned error: %v", err)
	}
	if probeCalls != 2 {
		t.Fatalf("probeCalls = %d, want 2 (probe all hashes in metadata plane first)", probeCalls)
	}
	if reusableByHash["new"] {
		t.Fatal("non-reusable block reported reusable")
	}
	if !reusableByHash["reusable"] {
		t.Fatal("reusable block not reported reusable")
	}
	if len(reusableHashes) != 1 || reusableHashes[0] != "reusable" {
		t.Fatalf("reusableHashes = %v, want [reusable]", reusableHashes)
	}
}

func TestCheckBlocksReadyParallel_OnlyChecksOwnershipForPhysicallyPresentCandidates(t *testing.T) {
	origClassify := checkBlocksClassifyOwnershipFn
	defer func() {
		checkBlocksClassifyOwnershipFn = origClassify
	}()

	var classifyCalls int
	checkBlocksClassifyOwnershipFn = func(database *db.DB, orgID, referrer, blockID string) (bool, error) {
		classifyCalls++
		return blockID == "present", nil
	}

	ready, err := checkBlocksReadyParallel(
		context.Background(),
		nil,
		"org",
		"up:sess",
		[]string{"missing", "present"},
		map[string]bool{"missing": false, "present": true},
		2,
	)
	if err != nil {
		t.Fatalf("checkBlocksReadyParallel returned error: %v", err)
	}
	if classifyCalls != 1 {
		t.Fatalf("classifyCalls = %d, want 1 (missing physical object should skip ownership read)", classifyCalls)
	}
	if ready["missing"] {
		t.Fatal("missing block reported ready")
	}
	if !ready["present"] {
		t.Fatal("present reusable block should be ready")
	}
}

func TestCheckBlocksForSession_AllMetadataMissingSkipsBlockStore(t *testing.T) {
	origProbe := checkBlocksProbeReuseFn
	defer func() {
		checkBlocksProbeReuseFn = origProbe
	}()

	checkBlocksProbeReuseFn = func(database *db.DB, orgID, hash string) (db.BlockReuseProbe, error) {
		return db.BlockReuseProbe{Decision: db.BlockReuseNeedsPut}, nil
	}

	h := &BlockHandler{blockStore: nil, storageManager: nil, config: nil}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v2/blocks/check", nil)
	c.Set("org_id", "org-1")

	resp, err := h.checkBlocksForSession(c, db.BlockUploadSession{SessionID: "sess-1", OrgID: "org-1", RepoID: "repo-1"}, []string{"new-a", "new-b", "new-a"})
	if err != nil {
		t.Fatalf("checkBlocksForSession returned error: %v", err)
	}
	if len(resp.Existing) != 0 {
		t.Fatalf("existing = %v, want empty", resp.Existing)
	}
	wantMissing := []string{"new-a", "new-b", "new-a"}
	if len(resp.Missing) != len(wantMissing) {
		t.Fatalf("missing len = %d, want %d (%v)", len(resp.Missing), len(wantMissing), resp.Missing)
	}
	for i := range wantMissing {
		if resp.Missing[i] != wantMissing[i] {
			t.Fatalf("missing[%d] = %q, want %q (order and duplicates should be preserved)", i, resp.Missing[i], wantMissing[i])
		}
	}
}

func TestBlockUploadSessionQueryTransportIsRejected(t *testing.T) {
	t.Run("check blocks rejects legacy session query transport", func(t *testing.T) {
		r := gin.New()
		h := &BlockHandler{}
		r.POST("/api/v2/blocks/check", h.CheckBlocks)

		body := CheckBlocksRequest{Hashes: []string{strings.Repeat("a", 64)}}
		jsonBody, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", "/api/v2/blocks/check?session=legacy", bytes.NewBuffer(jsonBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
		var resp map[string]interface{}
		json.Unmarshal(w.Body.Bytes(), &resp)
		if resp["error"] != "block upload session must be sent via X-Block-Upload-Session header" {
			t.Fatalf("error = %v", resp["error"])
		}
	})

	t.Run("upload block rejects legacy session query transport", func(t *testing.T) {
		r := gin.New()
		h := &BlockHandler{}
		r.POST("/api/v2/blocks/upload", h.UploadBlock)

		req, _ := http.NewRequest("POST", "/api/v2/blocks/upload?session=legacy", bytes.NewBufferString("abc"))
		req.Header.Set("Content-Type", "application/octet-stream")
		req.ContentLength = 3
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusBadRequest)
		}
		var resp map[string]interface{}
		json.Unmarshal(w.Body.Bytes(), &resp)
		if resp["error"] != "block upload session must be sent via X-Block-Upload-Session header" {
			t.Fatalf("error = %v", resp["error"])
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
	c.Set("org_id", "00000000-0000-0000-0000-000000000001")

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
	c.Set("org_id", "00000000-0000-0000-0000-000000000001")

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
	RegisterBlockRoutes(rg, nil, nil, nil, nil, nil, nil)

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

// TestBlockUploadConcurrencyLimiter covers the pure per-user in-flight cap logic
// (item 18): acquire up to max, reject at max, release frees a slot, the key is
// deleted at zero (map self-cleans), and max <= 0 disables the cap.
func TestBlockUploadConcurrencyLimiter(t *testing.T) {
	t.Run("caps per user and frees on release", func(t *testing.T) {
		l := newBlockUploadConcurrencyLimiter(2)
		const user = "org1:userA"

		if !l.tryAcquire(user) {
			t.Fatal("first acquire should succeed")
		}
		if !l.tryAcquire(user) {
			t.Fatal("second acquire should succeed (at cap)")
		}
		if l.tryAcquire(user) {
			t.Fatal("third acquire should be rejected (over cap)")
		}

		// A different user has an independent budget.
		if !l.tryAcquire("org1:userB") {
			t.Fatal("a different user should not be affected by userA's cap")
		}

		// Releasing one slot lets the next acquire through.
		l.release(user)
		if !l.tryAcquire(user) {
			t.Fatal("acquire after release should succeed")
		}
	})

	t.Run("key is deleted when the count returns to zero", func(t *testing.T) {
		l := newBlockUploadConcurrencyLimiter(3)
		const user = "org1:userA"
		l.tryAcquire(user)
		l.tryAcquire(user)
		l.release(user)
		l.release(user)

		l.mu.Lock()
		_, present := l.inflight[user]
		l.mu.Unlock()
		if present {
			t.Fatal("key should be deleted from the map once its count is zero (self-cleaning)")
		}
	})

	t.Run("max <= 0 disables the cap", func(t *testing.T) {
		for _, max := range []int{0, -1} {
			l := newBlockUploadConcurrencyLimiter(max)
			for i := 0; i < 100; i++ {
				if !l.tryAcquire("org1:userA") {
					t.Fatalf("max=%d should disable the cap; acquire %d was rejected", max, i)
				}
			}
			l.mu.Lock()
			n := len(l.inflight)
			l.mu.Unlock()
			if n != 0 {
				t.Fatalf("disabled limiter should not record in-flight entries, got %d", n)
			}
		}
	})

	t.Run("nil limiter is a no-op that always admits", func(t *testing.T) {
		var l *blockUploadConcurrencyLimiter
		if !l.tryAcquire("org1:userA") {
			t.Fatal("nil limiter should always admit")
		}
		l.release("org1:userA") // must not panic
	})
}

// TestStagedBlockBucket verifies the ledger bucket is deterministic and in range
// (so the reserve is idempotent by (session, bucket, block_id)).
func TestStagedBlockBucket(t *testing.T) {
	for _, bucketCount := range []int{1, 8, 64} {
		for _, id := range []string{strings.Repeat("a", 64), strings.Repeat("b", 64), "deadbeef"} {
			b1 := db.StagedBlockBucket(id, bucketCount)
			b2 := db.StagedBlockBucket(id, bucketCount)
			if b1 != b2 {
				t.Fatalf("bucket for %q (n=%d) not deterministic: %d vs %d", id, bucketCount, b1, b2)
			}
			if b1 < 0 || b1 >= bucketCount {
				t.Fatalf("bucket %d out of range [0,%d)", b1, bucketCount)
			}
		}
	}
}

// TestStagedBlockBucketCap covers the per-bucket cap math, the dynamic bucket
// count for small ceilings, and the enabled flag.
func TestStagedBlockBucketCap(t *testing.T) {
	newHandler := func(stagedMB int64) *BlockHandler {
		cfg := &config.Config{}
		cfg.WebUploads.WebBlockUploadBlockSizeMB = 8
		cfg.WebUploads.MaxStagedBytesPerSessionMB = stagedMB
		return &BlockHandler{config: cfg, db: &db.DB{}}
	}

	t.Run("large ceiling fans out to the max bucket count", func(t *testing.T) {
		h := newHandler(12 * 1024) // 12 GiB / 8 MiB = 1536 blocks
		buckets, cap, enabled := h.stagedBlockBucketCap(db.BlockUploadSession{})
		if !enabled {
			t.Fatal("expected enabled")
		}
		if buckets != db.BlockUploadStagedBlockBuckets {
			t.Fatalf("bucketCount = %d, want %d", buckets, db.BlockUploadStagedBlockBuckets)
		}
		perBucket := 1536 / db.BlockUploadStagedBlockBuckets // 24
		want := perBucket*stagedBlockBucketCapFactor + stagedBlockBucketSlack
		if cap != want {
			t.Fatalf("bucket cap = %d, want %d", cap, want)
		}
	})

	t.Run("tiny ceiling uses few buckets and a bounded total (no explosion)", func(t *testing.T) {
		h := newHandler(8) // 8 MiB / 8 MiB = 1 block
		buckets, cap, enabled := h.stagedBlockBucketCap(db.BlockUploadSession{})
		if !enabled {
			t.Fatal("expected enabled")
		}
		if buckets != 1 {
			t.Fatalf("bucketCount = %d, want 1 for a single-block ceiling", buckets)
		}
		// Total bound = buckets × cap must stay small (was ~192 blocks with the old
		// fixed-64-buckets bug); here it is 1 × (1×2+3) = 5.
		if total := buckets * cap; total > 8 {
			t.Fatalf("total staged-block bound = %d, want a small bounded number (<=8)", total)
		}
	})

	t.Run("disabled when the ceiling is disabled", func(t *testing.T) {
		if _, _, enabled := newHandler(-1).stagedBlockBucketCap(db.BlockUploadSession{}); enabled {
			t.Fatal("expected disabled when the per-session ceiling is disabled")
		}
	})

	t.Run("disabled when db is not wired", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.WebUploads.WebBlockUploadBlockSizeMB = 8
		cfg.WebUploads.MaxStagedBytesPerSessionMB = 100
		h := &BlockHandler{config: cfg}
		if _, _, enabled := h.stagedBlockBucketCap(db.BlockUploadSession{}); enabled {
			t.Fatal("expected disabled when db is nil (no ledger to check)")
		}
	})

	t.Run("persisted session params override live config", func(t *testing.T) {
		h := newHandler(12 * 1024)
		session := db.BlockUploadSession{StagedBucketCount: 3, StagedBucketCap: 11}
		buckets, cap, enabled := h.stagedBlockBucketCap(session)
		if !enabled {
			t.Fatal("expected enabled")
		}
		if buckets != 3 || cap != 11 {
			t.Fatalf("got buckets=%d cap=%d, want persisted 3/11", buckets, cap)
		}
	})
}

// TestBlockBodyLimit covers that a session-mode upload is bounded to the CAS
// block size (not chunking.absolute_max), so the per-user concurrency cap is a
// meaningful RAM bound (cap × block_size), while the legacy no-session path keeps
// the larger absolute_max bound (variable FastCDC sync blocks).
func TestBlockBodyLimit(t *testing.T) {
	const blockSizeMB = 8
	const absoluteMax = 256 * 1024 * 1024
	cfg := &config.Config{}
	cfg.WebUploads.WebBlockUploadBlockSizeMB = blockSizeMB
	cfg.Chunking.Adaptive.AbsoluteMax = absoluteMax
	h := &BlockHandler{config: cfg}

	if got := h.blockBodyLimit(uploadSessionValid, db.BlockUploadSession{}); got != int64(blockSizeMB)*1024*1024 {
		t.Fatalf("session-mode body limit = %d, want %d (the CAS block size, not absolute_max)", got, int64(blockSizeMB)*1024*1024)
	}
	if got := h.blockBodyLimit(uploadSessionAbsent, db.BlockUploadSession{}); got != absoluteMax {
		t.Fatalf("legacy body limit = %d, want %d (chunking.absolute_max)", got, absoluteMax)
	}

	session := db.BlockUploadSession{BlockSizeBytes: 4 * 1024 * 1024}
	if got := h.blockBodyLimit(uploadSessionValid, session); got != session.BlockSizeBytes {
		t.Fatalf("session-mode body limit = %d, want persisted session block size %d", got, session.BlockSizeBytes)
	}
}

// TestUploadBlockPerUserConcurrencyCap covers the handler wiring: a session-mode
// upload is admitted until the user's cap is full, then rejected with 429 +
// Retry-After, while a non-session (legacy) request is never capped.
func TestUploadBlockPerUserConcurrencyCap(t *testing.T) {
	newCtx := func() (*gin.Context, *httptest.ResponseRecorder) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/v2/blocks/upload", nil)
		c.Set("org_id", "org1")
		c.Set("user_id", "userA")
		return c, w
	}

	t.Run("session mode rejects over the cap with 429 + Retry-After", func(t *testing.T) {
		h := &BlockHandler{uploadLimiter: newBlockUploadConcurrencyLimiter(1)}

		c1, _ := newCtx()
		release, ok := h.tryAdmitSessionUpload(c1, uploadSessionValid)
		if !ok {
			t.Fatal("first session upload should be admitted")
		}

		c2, w2 := newCtx()
		_, ok = h.tryAdmitSessionUpload(c2, uploadSessionValid)
		if ok {
			t.Fatal("second concurrent session upload should be rejected at the cap")
		}
		if w2.Code != http.StatusTooManyRequests {
			t.Fatalf("status = %d, want %d", w2.Code, http.StatusTooManyRequests)
		}
		if got := w2.Header().Get("Retry-After"); got == "" {
			t.Fatal("expected a Retry-After header on the 429")
		}

		// Releasing the first slot lets a later upload through again.
		release()
		c3, _ := newCtx()
		if _, ok := h.tryAdmitSessionUpload(c3, uploadSessionValid); !ok {
			t.Fatal("upload after release should be admitted")
		}
	})

	t.Run("non-session (legacy) uploads are never capped", func(t *testing.T) {
		h := &BlockHandler{uploadLimiter: newBlockUploadConcurrencyLimiter(1)}
		for i := 0; i < 5; i++ {
			c, w := newCtx()
			_, ok := h.tryAdmitSessionUpload(c, uploadSessionAbsent)
			if !ok {
				t.Fatalf("legacy no-session upload %d should never be capped", i)
			}
			if w.Code != http.StatusOK {
				t.Fatalf("legacy path should not write an error status, got %d", w.Code)
			}
		}
	})
}
