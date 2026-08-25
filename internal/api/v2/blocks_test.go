package v2

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/Sesame-Disk/sesamefs/internal/streaming"
	"github.com/Sesame-Disk/sesamefs/internal/traffic"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func init() {
	gin.SetMode(gin.TestMode)
}

type checkBlocksTestCanonicalReader struct {
	exists map[string]bool
	err    error
}

func stubSuccessfulLibraryStorageClassLookup(t *testing.T, storageClass string) {
	t.Helper()
	original := lookupLibraryStorageClassContextFn
	lookupLibraryStorageClassContextFn = func(context.Context, *db.DB, string, string) (string, error) {
		return storageClass, nil
	}
	t.Cleanup(func() { lookupLibraryStorageClassContextFn = original })
}

func (r *checkBlocksTestCanonicalReader) CheckBlocksExist(context.Context, []string, int) (map[string]bool, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.exists, nil
}

func (*checkBlocksTestCanonicalReader) GetBlock(context.Context, string) ([]byte, error) {
	return nil, errors.New("not implemented")
}

func (*checkBlocksTestCanonicalReader) GetBlockReader(context.Context, string) (io.ReadCloser, error) {
	return nil, errors.New("not implemented")
}

func (*checkBlocksTestCanonicalReader) GetBlockSize(context.Context, string) (int64, error) {
	return 0, errors.New("not implemented")
}

func TestCheckBlocks_InvalidJSON(t *testing.T) {
	r := gin.New()
	r.Use(gin.Recovery())

	h := &BlockHandler{storageManager: nil, config: nil}
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

	h := &BlockHandler{storageManager: nil, config: nil}
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

	h := &BlockHandler{storageManager: nil, config: nil}
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

	h := &BlockHandler{storageManager: nil, config: nil}
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

// The no-session /blocks/check oracle was removed with the no-session upload path
// it was paired with. A session-less check is now rejected with 400 before it can
// touch any store, so this replaces the old "nil block store -> 503" test whose
// scenario (a session-less request reaching the store) no longer exists.
func TestCheckBlocks_NoSessionIsRejected(t *testing.T) {
	hash := strings.Repeat("a", 64)
	r := gin.New()
	r.Use(gin.Recovery())

	h := &BlockHandler{storageManager: nil, config: nil}
	r.POST("/api/v2/blocks/check", h.CheckBlocks)

	body := CheckBlocksRequest{Hashes: []string{hash}}
	jsonBody, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", "/api/v2/blocks/check", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if resp["code"] != blockUploadSessionRequiredCode {
		t.Fatalf("code = %v, want %q", resp["code"], blockUploadSessionRequiredCode)
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

	var probeCalls atomic.Int32
	checkBlocksProbeReuseFn = func(database *db.DB, orgID, hash string) (db.BlockReuseProbe, error) {
		probeCalls.Add(1)
		if hash == "reusable" {
			return db.BlockReuseProbe{Decision: db.BlockReuseReusable}, nil
		}
		if hash == "stub" {
			return db.BlockReuseProbe{Decision: db.BlockReuseRepairableStub}, nil
		}
		return db.BlockReuseProbe{Decision: db.BlockReuseNeedsPut}, nil
	}

	reusableByHash, reusableHashes, err := checkBlocksReusableCandidatesParallel(
		context.Background(),
		nil,
		"org",
		[]string{"new", "stub", "reusable"},
		2,
	)
	if err != nil {
		t.Fatalf("checkBlocksReusableCandidatesParallel returned error: %v", err)
	}
	if probeCalls.Load() != 3 {
		t.Fatalf("probeCalls = %d, want 3 (probe all hashes in metadata plane first)", probeCalls.Load())
	}
	if reusableByHash["new"] {
		t.Fatal("non-reusable block reported reusable")
	}
	if reusableByHash["stub"] {
		t.Fatal("repairable stub reported reusable")
	}
	if !reusableByHash["reusable"] {
		t.Fatal("reusable block not reported reusable")
	}
	if len(reusableHashes) != 1 || reusableHashes[0] != "reusable" {
		t.Fatalf("reusableHashes = %v, want [reusable]", reusableHashes)
	}
}

func TestCheckBlocksReadyParallel_UsesCanonicalExistenceBeforeOwnership(t *testing.T) {
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
		t.Fatal("block present in the canonical store and owned by the session should be ready")
	}
}

func TestCheckBlocksForSession_UsesCanonicalStoreBeforeOwnership(t *testing.T) {
	stubSuccessfulLibraryStorageClassLookup(t, "")
	origProbe := checkBlocksProbeReuseFn
	origClassify := checkBlocksClassifyOwnershipFn
	origNewReader := checkBlocksNewCanonicalReaderFn
	t.Cleanup(func() {
		checkBlocksProbeReuseFn = origProbe
		checkBlocksClassifyOwnershipFn = origClassify
		checkBlocksNewCanonicalReaderFn = origNewReader
	})
	checkBlocksProbeReuseFn = func(*db.DB, string, string) (db.BlockReuseProbe, error) {
		return db.BlockReuseProbe{Decision: db.BlockReuseReusable, StorageClass: "canonical"}, nil
	}

	const orgID = "3fa85f64-5717-4562-b3fc-2c963f66afa6"
	existingHash := strings.Repeat("a", 64)
	missingHash := strings.Repeat("b", 64)
	checkBlocksNewCanonicalReaderFn = func(_ context.Context, _ *db.DB, _ *storage.Manager, gotOrgID string, hashes []string, fallback *storage.BlockStore, fallbackClass string) (streaming.CanonicalBlockReader, error) {
		if gotOrgID != orgID || fallback == nil || fallbackClass != "legacy" {
			return nil, fmt.Errorf("unexpected canonical routing: org=%q fallback=%p class=%q", gotOrgID, fallback, fallbackClass)
		}
		if len(hashes) != 2 || !slices.Contains(hashes, existingHash) || !slices.Contains(hashes, missingHash) {
			return nil, fmt.Errorf("canonical hashes = %v, want both reusable hashes", hashes)
		}
		return &checkBlocksTestCanonicalReader{exists: map[string]bool{existingHash: true, missingHash: false}}, nil
	}

	var ownershipCalls atomic.Int32
	checkBlocksClassifyOwnershipFn = func(_ *db.DB, gotOrgID, referrer, blockID string) (bool, error) {
		ownershipCalls.Add(1)
		if gotOrgID != orgID || referrer != db.BlockReferrerForUpload("sess-1") || blockID != existingHash {
			return false, fmt.Errorf("unexpected ownership classification: org=%q referrer=%q block=%q", gotOrgID, referrer, blockID)
		}
		return true, nil
	}
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	s3Store, err := storage.NewS3Store(context.Background(), storage.S3Config{
		Endpoint: server.URL, Bucket: "test-bucket", Region: "us-east-1",
		AccessKeyID: "test", SecretAccessKey: "test", UsePathStyle: true,
	})
	if err != nil {
		t.Fatalf("NewS3Store() error = %v", err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v2/blocks/check", nil)
	c.Set("org_id", orgID)
	h := &BlockHandler{storage: s3Store, db: &db.DB{}}
	response, err := h.checkBlocksForSession(c, db.BlockUploadSession{
		SessionID: "sess-1",
		OrgID:     orgID,
		RepoID:    "repo-1",
	}, []string{existingHash, missingHash, existingHash})
	if err != nil {
		t.Fatalf("checkBlocksForSession() error = %v", err)
	}
	if ownershipCalls.Load() != 1 {
		t.Fatalf("ownership calls = %d, want 1 for the physically present canonical candidate", ownershipCalls.Load())
	}
	if got, want := fmt.Sprint(response.Existing), fmt.Sprint([]string{existingHash, existingHash}); got != want {
		t.Fatalf("existing = %v, want %v", response.Existing, []string{existingHash, existingHash})
	}
	if got, want := fmt.Sprint(response.Missing), fmt.Sprint([]string{missingHash}); got != want {
		t.Fatalf("missing = %v, want %v", response.Missing, []string{missingHash})
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

	h := &BlockHandler{storageManager: nil, config: nil}
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
	h := &BlockHandler{storageManager: manager, config: nil}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v2/blocks/"+strings.Repeat("a", 64), nil)
	c.Request.Host = "files.example.com"
	c.Set("org_id", "00000000-0000-0000-0000-000000000001")

	blockStore, storageClass := h.getBlockStore(c)
	if blockStore != nil {
		t.Fatal("expected nil blockStore when storage manager cannot resolve a healthy backend")
	}
	if storageClass != "" {
		t.Fatalf("storageClass = %q, want empty on resolution error", storageClass)
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

// A session-less upload is rejected with 400 block_upload_session_required before
// the body is read, so it can never leave an unreferenced S3 object behind (F8).
// The per-session content-length and size guards below it run only for a valid
// session and are exercised end to end in internal/integration/web_block_upload_test.go.
func TestUploadBlock_NoSessionIsRejected(t *testing.T) {
	r := gin.New()
	r.Use(gin.Recovery())

	h := &BlockHandler{storageManager: nil, config: nil}
	r.POST("/api/v2/blocks/upload", h.UploadBlock)

	req, _ := http.NewRequest("POST", "/api/v2/blocks/upload", bytes.NewReader([]byte("some block bytes")))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if resp["code"] != blockUploadSessionRequiredCode {
		t.Fatalf("code = %v, want %q", resp["code"], blockUploadSessionRequiredCode)
	}
}

func TestUploadBlock_PlacementLookupErrorSkipsStorage(t *testing.T) {
	oldGetSession := getBlockUploadSessionFn
	oldLookup := lookupLibraryStorageClassContextFn
	oldProbe := probeUploadedBlockReuseFn
	t.Cleanup(func() {
		getBlockUploadSessionFn = oldGetSession
		lookupLibraryStorageClassContextFn = oldLookup
		probeUploadedBlockReuseFn = oldProbe
	})

	const (
		orgID     = "3fa85f64-5717-4562-b3fc-2c963f66afa6"
		userID    = "4fa85f64-5717-4562-b3fc-2c963f66afa6"
		sessionID = "sess-placement-error"
	)
	getBlockUploadSessionFn = func(*db.DB, string) (db.BlockUploadSession, bool, error) {
		return db.BlockUploadSession{SessionID: sessionID, OrgID: orgID, UserID: userID, RepoID: "repo-1", BlockSizeBytes: 1024}, true, nil
	}
	lookupLibraryStorageClassContextFn = func(context.Context, *db.DB, string, string) (string, error) {
		return "", errors.New("cassandra unavailable")
	}
	probeCalls := 0
	probeUploadedBlockReuseFn = func(*db.DB, string, string) (db.BlockReuseProbe, error) {
		probeCalls++
		return db.BlockReuseProbe{}, nil
	}

	manager := storage.NewManager()
	manager.SetDefaultClass("hot-s3-default")
	manager.RegisterBackend("hot-s3-default", &storage.S3Store{}, "")
	h := &BlockHandler{
		db:             &db.DB{},
		storageManager: manager,
		config:         &config.Config{WebUploads: config.WebUploadsConfig{EnableWebBlockUpload: true}},
	}
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("org_id", orgID)
		c.Set("user_id", userID)
		c.Next()
	})
	r.POST("/api/v2/blocks/upload", h.UploadBlock)
	req := httptest.NewRequest(http.MethodPost, "/api/v2/blocks/upload", bytes.NewBufferString("hello"))
	req.ContentLength = 5
	req.Header.Set("X-Block-Upload-Session", sessionID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusServiceUnavailable, w.Body.String())
	}
	if probeCalls != 0 {
		t.Fatalf("storage probe calls = %d, want 0", probeCalls)
	}
}

func TestCheckBlocks_PlacementLookupErrorReturnsServiceUnavailable(t *testing.T) {
	oldGetSession := getBlockUploadSessionFn
	oldLookup := lookupLibraryStorageClassContextFn
	oldProbe := checkBlocksProbeReuseFn
	oldReader := checkBlocksNewCanonicalReaderFn
	t.Cleanup(func() {
		getBlockUploadSessionFn = oldGetSession
		lookupLibraryStorageClassContextFn = oldLookup
		checkBlocksProbeReuseFn = oldProbe
		checkBlocksNewCanonicalReaderFn = oldReader
	})

	const (
		orgID     = "3fa85f64-5717-4562-b3fc-2c963f66afa6"
		userID    = "4fa85f64-5717-4562-b3fc-2c963f66afa6"
		sessionID = "sess-check-placement-error"
	)
	hash := strings.Repeat("a", 64)
	getBlockUploadSessionFn = func(*db.DB, string) (db.BlockUploadSession, bool, error) {
		return db.BlockUploadSession{SessionID: sessionID, OrgID: orgID, UserID: userID, RepoID: "repo-1"}, true, nil
	}
	checkBlocksProbeReuseFn = func(*db.DB, string, string) (db.BlockReuseProbe, error) {
		return db.BlockReuseProbe{Decision: db.BlockReuseReusable}, nil
	}
	manager := storage.NewManager()
	manager.SetDefaultClass("hot-s3-default")
	h := &BlockHandler{
		db:             &db.DB{},
		storageManager: manager,
		config:         &config.Config{WebUploads: config.WebUploadsConfig{EnableWebBlockUpload: true}},
	}

	for _, tt := range []struct {
		name string
		err  error
	}{
		{name: "transport error", err: errors.New("cassandra unavailable")},
		{name: "wrapped not found", err: fmt.Errorf("lookup: %w", gocql.ErrNotFound)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var lookupCalls atomic.Int32
			lookupLibraryStorageClassContextFn = func(context.Context, *db.DB, string, string) (string, error) {
				lookupCalls.Add(1)
				return "", tt.err
			}
			var readerCalls atomic.Int32
			checkBlocksNewCanonicalReaderFn = func(context.Context, *db.DB, *storage.Manager, string, []string, *storage.BlockStore, string) (streaming.CanonicalBlockReader, error) {
				readerCalls.Add(1)
				return nil, errors.New("canonical reader must not be reached")
			}

			r := gin.New()
			r.Use(func(c *gin.Context) {
				c.Set("org_id", orgID)
				c.Set("user_id", userID)
				c.Next()
			})
			r.POST("/api/v2/blocks/check", h.CheckBlocks)
			body, err := json.Marshal(CheckBlocksRequest{Hashes: []string{hash}})
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/api/v2/blocks/check", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Block-Upload-Session", sessionID)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusServiceUnavailable, w.Body.String())
			}
			if lookupCalls.Load() != 1 {
				t.Fatalf("placement lookup calls = %d, want 1", lookupCalls.Load())
			}
			if readerCalls.Load() != 0 {
				t.Fatalf("canonical reader calls = %d, want 0", readerCalls.Load())
			}
		})
	}
}

func TestUploadBlockSessionRetriesWithSingleShotAccounting(t *testing.T) {
	stubSuccessfulLibraryStorageClassLookup(t, "")
	fastBlockMaterializationRetries(t)
	oldGetSession := getBlockUploadSessionFn
	oldCount := countSessionStagedBlocksFn
	oldReserve := reserveSessionStagedBlockFn
	oldProbe := probeUploadedBlockReuseFn
	oldPut := repairCanonicalBlockDirectFn
	oldExists := reusableCanonicalObjectExistsFn
	oldRegister := registerUploadedBlockTargetForMaterializationFn
	oldMapping := writeVerifiedWebBlockMappingFn
	oldTraffic := recordBlockUploadTrafficFn
	t.Cleanup(func() {
		getBlockUploadSessionFn = oldGetSession
		countSessionStagedBlocksFn = oldCount
		reserveSessionStagedBlockFn = oldReserve
		probeUploadedBlockReuseFn = oldProbe
		repairCanonicalBlockDirectFn = oldPut
		reusableCanonicalObjectExistsFn = oldExists
		registerUploadedBlockTargetForMaterializationFn = oldRegister
		writeVerifiedWebBlockMappingFn = oldMapping
		recordBlockUploadTrafficFn = oldTraffic
	})

	const (
		orgID     = "3fa85f64-5717-4562-b3fc-2c963f66afa6"
		userID    = "4fa85f64-5717-4562-b3fc-2c963f66afa6"
		sessionID = "sess-1"
	)
	getBlockUploadSessionFn = func(*db.DB, string) (db.BlockUploadSession, bool, error) {
		return db.BlockUploadSession{
			SessionID: sessionID, OrgID: orgID, UserID: userID, RepoID: "repo-1",
			BlockSizeBytes: 1024, StagedBucketCount: 1, StagedBucketCap: 10,
		}, true, nil
	}
	countCalls := 0
	countSessionStagedBlocksFn = func(*db.DB, string, int, int) (int, error) {
		countCalls++
		return 0, nil
	}
	reserveCalls := 0
	reserveSessionStagedBlockFn = func(*db.DB, string, int, string, int64) error {
		reserveCalls++
		return nil
	}
	probeCalls := 0
	probeUploadedBlockReuseFn = func(_ *db.DB, orgID, blockID string) (db.BlockReuseProbe, error) {
		probeCalls++
		if probeCalls < 3 {
			return db.BlockReuseProbe{Decision: db.BlockReuseNeedsPut}, nil
		}
		return db.BlockReuseProbe{
			Decision:     db.BlockReuseReusable,
			StorageClass: "legacy",
			StorageKey:   fmt.Sprintf("blocks/%s/%s/%s/%s", orgID, blockID[:2], blockID[2:4], blockID),
		}, nil
	}
	putCalls := 0
	repairCanonicalBlockDirectFn = func(_ context.Context, _ *storage.BlockStore, key string, _ []byte) (string, error) {
		putCalls++
		return key, nil
	}
	existsCalls := 0
	reusableCanonicalObjectExistsFn = func(context.Context, *storage.BlockStore, string) (bool, error) {
		existsCalls++
		return true, nil
	}
	registerCalls := 0
	registerUploadedBlockTargetForMaterializationFn = func(context.Context, *db.DB, string, string, string, string, int, BlockMaterializationTarget, string) error {
		registerCalls++
		if registerCalls == 1 {
			return ErrBlockDeleteInProgress
		}
		return nil
	}
	writeVerifiedWebBlockMappingFn = func(*db.DB, string, string, string, string) error { return nil }
	trafficCalls := 0
	recordBlockUploadTrafficFn = func(traffic.TrafficPeriodRecorder, traffic.QuotaStatus, string, string, string, int64) {
		trafficCalls++
	}
	beforeMetric := testutil.ToFloat64(metrics.BlockUploadStagedBlocksTotal.WithLabelValues("true"))

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("org_id", orgID)
		c.Set("user_id", userID)
		c.Next()
	})
	h := &BlockHandler{
		storage: &storage.S3Store{}, db: &db.DB{},
		config: &config.Config{WebUploads: config.WebUploadsConfig{EnableWebBlockUpload: true}},
	}
	r.POST("/api/v2/blocks/upload", h.UploadBlock)
	req := httptest.NewRequest(http.MethodPost, "/api/v2/blocks/upload", bytes.NewBufferString("hello"))
	req.ContentLength = 5
	req.Header.Set("X-Block-Upload-Session", sessionID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", w.Code, w.Body.String())
	}
	if probeCalls != 3 || putCalls != 2 || existsCalls != 1 || registerCalls != 2 {
		t.Fatalf("probe/put/confirm/register = %d/%d/%d/%d, want 3/2/1/2", probeCalls, putCalls, existsCalls, registerCalls)
	}
	if countCalls != 1 || reserveCalls != 1 {
		t.Fatalf("staging count/reserve = %d/%d, want 1/1", countCalls, reserveCalls)
	}
	if trafficCalls != 1 {
		t.Fatalf("traffic calls = %d, want 1", trafficCalls)
	}
	if got := testutil.ToFloat64(metrics.BlockUploadStagedBlocksTotal.WithLabelValues("true")); got != beforeMetric+1 {
		t.Fatalf("staged metric = %v, want %v", got, beforeMetric+1)
	}
}

func TestUploadBlockSessionExhaustedFenceReturnsRetryable409(t *testing.T) {
	stubSuccessfulLibraryStorageClassLookup(t, "")
	fastBlockMaterializationRetries(t)
	oldGetSession := getBlockUploadSessionFn
	oldProbe := probeUploadedBlockReuseFn
	t.Cleanup(func() {
		getBlockUploadSessionFn = oldGetSession
		probeUploadedBlockReuseFn = oldProbe
	})

	const (
		orgID     = "3fa85f64-5717-4562-b3fc-2c963f66afa6"
		userID    = "4fa85f64-5717-4562-b3fc-2c963f66afa6"
		sessionID = "sess-fenced"
	)
	getBlockUploadSessionFn = func(*db.DB, string) (db.BlockUploadSession, bool, error) {
		return db.BlockUploadSession{SessionID: sessionID, OrgID: orgID, UserID: userID, RepoID: "repo-1", BlockSizeBytes: 1024}, true, nil
	}
	probeCalls := 0
	probeUploadedBlockReuseFn = func(*db.DB, string, string) (db.BlockReuseProbe, error) {
		probeCalls++
		return db.BlockReuseProbe{Decision: db.BlockReuseBlockedByGC}, nil
	}

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("org_id", orgID)
		c.Set("user_id", userID)
		c.Next()
	})
	h := &BlockHandler{
		storage: &storage.S3Store{}, db: &db.DB{},
		config: &config.Config{WebUploads: config.WebUploadsConfig{EnableWebBlockUpload: true}},
	}
	r.POST("/api/v2/blocks/upload", h.UploadBlock)
	req := httptest.NewRequest(http.MethodPost, "/api/v2/blocks/upload", bytes.NewBufferString("hello"))
	req.ContentLength = 5
	req.Header.Set("X-Block-Upload-Session", sessionID)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusConflict || w.Header().Get("Retry-After") != "1" {
		t.Fatalf("status/Retry-After = %d/%q, want 409/1, body=%s", w.Code, w.Header().Get("Retry-After"), w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"code":"block_delete_in_progress"`) {
		t.Fatalf("body = %s, want block_delete_in_progress", w.Body.String())
	}
	if probeCalls != RetryAttempts() {
		t.Fatalf("probeCalls = %d, want retry budget %d", probeCalls, RetryAttempts())
	}
}

func TestRespondBlockMaterializeError(t *testing.T) {
	newCtx := func() (*gin.Context, *httptest.ResponseRecorder) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		return c, w
	}

	t.Run("nil error writes no response and does not stop the handler", func(t *testing.T) {
		c, w := newCtx()
		if respondBlockMaterializeError(c, nil) {
			t.Fatal("respondBlockMaterializeError(nil) = true, want false")
		}
		if w.Code != http.StatusOK { // untouched recorder default
			t.Fatalf("status = %d, want %d (no response written)", w.Code, http.StatusOK)
		}
	})

	t.Run("exhausted GC fence is a retryable coded 409", func(t *testing.T) {
		c, w := newCtx()
		fenceErr := fmt.Errorf("%w: block fenced during web-session materialize", ErrBlockDeleteInProgress)
		if !respondBlockMaterializeError(c, fenceErr) {
			t.Fatal("respondBlockMaterializeError(fence) = false, want true (response written, caller returns)")
		}
		if w.Code != http.StatusConflict {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusConflict)
		}
		if got := w.Header().Get("Retry-After"); got != "1" {
			t.Fatalf("Retry-After = %q, want 1", got)
		}
		if !strings.Contains(w.Body.String(), `"code":"block_delete_in_progress"`) {
			t.Fatalf("409 body = %s, want block_delete_in_progress", w.Body.String())
		}
	})

	t.Run("verified mapping conflict is a permanent 409", func(t *testing.T) {
		c, w := newCtx()
		if !respondBlockMaterializeError(c, fmt.Errorf("remap: %w", db.ErrBlockIDMappingConflict)) {
			t.Fatal("respondBlockMaterializeError(conflict) = false, want true")
		}
		if w.Code != http.StatusConflict {
			t.Fatalf("status = %d, want %d", w.Code, http.StatusConflict)
		}
		if !strings.Contains(w.Body.String(), "block id mapping conflict") {
			t.Fatalf("409 body = %s, want block id mapping conflict", w.Body.String())
		}
		if got := w.Header().Get("Retry-After"); got != "" {
			t.Fatalf("Retry-After = %q, want no retry hint for permanent conflict", got)
		}
	})
}

// TestUploadBlockMaterializePropagatesFence keeps the seam honest at the
// materializeUploadedBlock boundary: it must propagate ErrBlockDeleteInProgress
// unchanged (never swallow it or reshape it into a mapping conflict) so the handler
// mapping above sees the real signal.
func TestUploadBlockMaterializePropagatesFence(t *testing.T) {
	old := registerUploadedBlockTargetForMaterializationFn
	t.Cleanup(func() { registerUploadedBlockTargetForMaterializationFn = old })
	registerUploadedBlockTargetForMaterializationFn = func(context.Context, *db.DB, string, string, string, string, int, BlockMaterializationTarget, string) error {
		return fmt.Errorf("%w: block fenced during web-session materialize", ErrBlockDeleteInProgress)
	}

	h := &BlockHandler{}
	session := db.BlockUploadSession{SessionID: "sess-1", OrgID: "org-1", RepoID: "repo-1"}
	err := h.materializeUploadedBlock(context.Background(), session, strings.Repeat("a", 64), strings.Repeat("b", 40), 5, BlockMaterializationTarget{StorageClass: "hot", StorageKey: "blocks/org/a"})
	if !errors.Is(err, ErrBlockDeleteInProgress) {
		t.Fatalf("materializeUploadedBlock err = %v, want ErrBlockDeleteInProgress propagated", err)
	}
	if errors.Is(err, db.ErrBlockIDMappingConflict) {
		t.Fatal("fence error must not masquerade as a mapping conflict")
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

// TestBlockBodyLimit covers that a session upload is bounded to the CAS block
// size (not chunking.absolute_max), so the per-user concurrency cap is a
// meaningful RAM bound (cap × block_size). There is no no-session branch: that
// path is rejected in UploadBlock before the body is read (F8).
func TestBlockBodyLimit(t *testing.T) {
	const blockSizeMB = 8
	cfg := &config.Config{}
	cfg.WebUploads.WebBlockUploadBlockSizeMB = blockSizeMB
	cfg.Chunking.Adaptive.AbsoluteMax = 256 * 1024 * 1024
	h := &BlockHandler{config: cfg}

	if got := h.blockBodyLimit(db.BlockUploadSession{}); got != int64(blockSizeMB)*1024*1024 {
		t.Fatalf("body limit = %d, want %d (the configured CAS block size, never absolute_max)", got, int64(blockSizeMB)*1024*1024)
	}

	session := db.BlockUploadSession{BlockSizeBytes: 4 * 1024 * 1024}
	if got := h.blockBodyLimit(session); got != session.BlockSizeBytes {
		t.Fatalf("body limit = %d, want persisted session block size %d", got, session.BlockSizeBytes)
	}
}

// TestUploadBlockPerUserConcurrencyCap covers the handler wiring: a session
// upload is admitted until the user's cap is full, then rejected with 429 +
// Retry-After.
func TestUploadBlockPerUserConcurrencyCap(t *testing.T) {
	newCtx := func() (*gin.Context, *httptest.ResponseRecorder) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/api/v2/blocks/upload", nil)
		c.Set("org_id", "org1")
		c.Set("user_id", "userA")
		return c, w
	}

	h := &BlockHandler{uploadLimiter: newBlockUploadConcurrencyLimiter(1)}

	c1, _ := newCtx()
	release, ok := h.tryAdmitSessionUpload(c1)
	if !ok {
		t.Fatal("first session upload should be admitted")
	}

	c2, w2 := newCtx()
	_, ok = h.tryAdmitSessionUpload(c2)
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
	if _, ok := h.tryAdmitSessionUpload(c3); !ok {
		t.Fatal("upload after release should be admitted")
	}
}
