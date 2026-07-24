package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestReadLimitedRequestBody exercises the shared bounded-read helper directly, so
// the byte boundary is covered without allocating a real 257 MiB body: a body up to
// and including the cap is returned intact with no response written, and one byte
// over the cap is rejected 413 by the MaxBytesReader before it is fully buffered.
func TestReadLimitedRequestBody(t *testing.T) {
	const max = 16
	cases := []struct {
		name     string
		body     string
		wantOK   bool
		wantCode int
	}{
		{"under limit", "hello", true, 0},
		{"at limit", strings.Repeat("x", max), true, 0},
		{"over limit", strings.Repeat("x", max+1), false, http.StatusRequestEntityTooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))

			data, ok := readLimitedRequestBody(c, max)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (code=%d body=%q)", ok, tc.wantOK, w.Code, w.Body.String())
			}
			if ok {
				if string(data) != tc.body {
					t.Fatalf("data = %q, want %q", data, tc.body)
				}
				if w.Body.Len() != 0 {
					t.Fatalf("success path wrote a response body: %q", w.Body.String())
				}
				return
			}
			if w.Code != tc.wantCode {
				t.Fatalf("code = %d, want %d; body=%s", w.Code, tc.wantCode, w.Body.String())
			}
		})
	}
}

// TestPutBlockBoundsBodySize verifies PutBlock rejects an oversized block before
// reading it and lets a block declared exactly at the cap through the size gate.
func TestPutBlockBoundsBodySize(t *testing.T) {
	putBlock := func(t *testing.T, declaredLen int64) int {
		t.Helper()
		r := setupSyncTestRouter()
		h := &SyncHandler{}
		r.POST("/seafhttp/repo/:repo_id/block/:block_id", h.PutBlock)

		blockID := strings.Repeat("a", 64) // valid 64-hex SHA-256 id
		req := httptest.NewRequest(http.MethodPost, "/seafhttp/repo/repo/block/"+blockID, strings.NewReader("small"))
		req.ContentLength = declaredLen
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}

	if code := putBlock(t, maxSyncBlockBytes+1); code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized block = %d, want 413", code)
	}
	// Exactly at the cap must pass the size gate (it fails later — 503 with no
	// storage configured — but never 413).
	if code := putBlock(t, maxSyncBlockBytes); code == http.StatusRequestEntityTooLarge {
		t.Errorf("block at the size cap was rejected 413, want it through the size gate")
	}
}

// TestCheckBlocksBoundsBlockIDCount verifies the id-count cap: a list one over the
// cap is rejected 413 before any per-id work, while a list exactly at the cap passes
// the size gate (failing later on invalid ids, not with 413).
func TestCheckBlocksBoundsBlockIDCount(t *testing.T) {
	newlineBody := func(n int) string {
		ids := make([]string, n)
		for i := range ids {
			ids[i] = "a"
		}
		return strings.Join(ids, "\n")
	}
	postCheckBlocks := func(t *testing.T, body string) int {
		t.Helper()
		r := setupSyncTestRouter()
		h := &SyncHandler{}
		r.POST("/seafhttp/repo/:repo_id/check-blocks", h.CheckBlocks)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/seafhttp/repo/repo/check-blocks", strings.NewReader(body)))
		return w.Code
	}

	if code := postCheckBlocks(t, newlineBody(maxCheckBlockIDs+1)); code != http.StatusRequestEntityTooLarge {
		t.Errorf("check-blocks over id cap = %d, want 413", code)
	}
	if code := postCheckBlocks(t, newlineBody(maxCheckBlockIDs)); code == http.StatusRequestEntityTooLarge {
		t.Errorf("check-blocks at id cap was rejected 413, want it through the size gate")
	}
}

// parseCheckBlockIDsForTest drives the parser directly and reports the ids, the
// ok flag and the status code written on rejection.
func parseCheckBlockIDsForTest(body string) ([]string, bool, int) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	ids, ok := parseCheckBlockIDs(c, []byte(body))
	return ids, ok, w.Code
}

// TestParseCheckBlockIDsJSONBoundary pins the id cap on the JSON path, which the
// newline-only coverage left untested: exactly the cap is accepted, one more is
// rejected 413.
func TestParseCheckBlockIDsJSONBoundary(t *testing.T) {
	jsonBody := func(n int) string {
		return "[" + strings.TrimSuffix(strings.Repeat(`"a",`, n), ",") + "]"
	}

	ids, ok, code := parseCheckBlockIDsForTest(jsonBody(maxCheckBlockIDs))
	if !ok {
		t.Fatalf("JSON at id cap rejected with %d, want accepted", code)
	}
	if len(ids) != maxCheckBlockIDs {
		t.Fatalf("JSON at id cap parsed %d ids, want %d", len(ids), maxCheckBlockIDs)
	}
	if _, ok, code := parseCheckBlockIDsForTest(jsonBody(maxCheckBlockIDs + 1)); ok || code != http.StatusRequestEntityTooLarge {
		t.Fatalf("JSON over id cap: ok=%v code=%d, want rejected 413", ok, code)
	}
}

// TestParseCheckBlockIDsJSONStrictness keeps the decoder as strict as the
// json.Unmarshal it replaced: a truncated array, trailing content after the
// close, and a non-string element must all stay 400 rather than parse partially.
func TestParseCheckBlockIDsJSONStrictness(t *testing.T) {
	for _, body := range []string{
		`["a"`,        // never closed
		`["a"] junk`,  // trailing garbage
		`["a"] ["b"]`, // a second array
		`["a", 1]`,    // non-string element
		`[`,           // bare open
	} {
		if _, ok, code := parseCheckBlockIDsForTest(body); ok || code != http.StatusBadRequest {
			t.Errorf("body %q: ok=%v code=%d, want rejected 400", body, ok, code)
		}
	}
	// An empty array stays valid and yields no ids.
	if ids, ok, code := parseCheckBlockIDsForTest(`[]`); !ok || len(ids) != 0 {
		t.Errorf("empty array: ids=%v ok=%v code=%d, want accepted with 0 ids", ids, ok, code)
	}
}

// TestParseCheckBlockIDsRejectsBeforeMaterializing is the regression for the
// cardinality gap: both formats let a body *under* the 16 MiB byte cap explode
// into millions of ids, and the cap used to be checked only after the whole list
// had been built. TotalAlloc is cumulative and unaffected by GC, so this asserts
// deterministically that the pathological bodies are cut during the parse.
//
// This is the allocation *canary*, not the cardinality contract — that contract is
// TestCheckBlocksBoundsBlockIDCount and TestParseCheckBlockIDsJSONBoundary, which
// pin the boundary functionally and hold regardless of allocator behavior. Keep it
// that way: TotalAlloc is process-global, so this test must never be made parallel,
// and a failure here under a new Go version or an instrumented runner should be
// read as "re-measure the headroom" before "the cap regressed". Measured cost is
// 16 MB / 34 MB against a 96 MB threshold; a materializing parse cost 272 MB/198 MB.
func TestParseCheckBlockIDsRejectsBeforeMaterializing(t *testing.T) {
	// Bodies at the byte cap that maximize id count. The newline case cannot be
	// collapsed by TrimSpace because both ends are non-space, so it reaches ~1
	// byte per id — the worst case at ~272 MB if fully materialized.
	cases := []struct {
		name string
		body string
	}{
		{"newline one byte per id", "a" + strings.Repeat("\n", maxCheckBlocksBodyBytes-2) + "a"},
		{"json empty strings", "[" + strings.TrimSuffix(strings.Repeat(`"",`, (maxCheckBlocksBodyBytes-2)/3), ",") + "]"},
	}
	// Generous headroom over the unavoidable cost (the body plus its string
	// conversion, ~16 MiB each) while still far below the hundreds of MB a
	// materializing parse would cost.
	const maxAllocBytes = 96 * 1024 * 1024

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var m1, m2 runtime.MemStats
			runtime.ReadMemStats(&m1)
			_, ok, code := parseCheckBlockIDsForTest(tc.body)
			runtime.ReadMemStats(&m2)

			if ok || code != http.StatusRequestEntityTooLarge {
				t.Fatalf("ok=%v code=%d, want rejected 413", ok, code)
			}
			allocated := m2.TotalAlloc - m1.TotalAlloc
			t.Logf("body=%d bytes, allocated %.1f MB", len(tc.body), float64(allocated)/(1024*1024))
			if allocated > maxAllocBytes {
				t.Fatalf("allocated %.1f MB parsing a rejected body, want < %d MB: the id cap is being applied after the list is materialized",
					float64(allocated)/(1024*1024), maxAllocBytes/(1024*1024))
			}
		})
	}
}

// errReader fails with something other than a size overflow, so the helper's
// 400-vs-413 split can be pinned.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

// TestReadLimitedRequestBodyReadErrorIs400 distinguishes a plain read failure
// from an overflow: only the latter is 413.
func TestReadLimitedRequestBodyReadErrorIs400(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", errReader{})

	if _, ok := readLimitedRequestBody(c, 1024); ok {
		t.Fatal("read error returned ok=true")
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400 (413 is reserved for overflow)", w.Code)
	}
}

// TestCheckBlocksBoundsChunkedBody covers the case a declared ContentLength
// cannot: a chunked body with no length that exceeds the byte cap must still be
// cut by the MaxBytesReader at the endpoint, not merely by the length fast-path.
// PutBlock shares the same helper; check-blocks is exercised because its 16 MiB
// cap makes the test cheap.
func TestCheckBlocksBoundsChunkedBody(t *testing.T) {
	r := setupSyncTestRouter()
	h := &SyncHandler{}
	r.POST("/seafhttp/repo/:repo_id/check-blocks", h.CheckBlocks)

	body := strings.NewReader(strings.Repeat("a", maxCheckBlocksBodyBytes+1))
	req := httptest.NewRequest(http.MethodPost, "/seafhttp/repo/repo/check-blocks", body)
	req.ContentLength = -1 // chunked: no declared length to fast-reject on
	req.TransferEncoding = []string{"chunked"}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("chunked oversized body = %d, want 413; body=%s", w.Code, w.Body.String())
	}
}
