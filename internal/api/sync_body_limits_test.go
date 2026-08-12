package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Sesame-Disk/sesamefs/internal/config"
)

// TestReadLimitedRequestBody exercises the shared bounded-read helper directly, so
// the byte boundary is covered without allocating a body anywhere near the real
// cap: a body up to and including the cap is returned intact with no response
// written, and one byte over the cap is rejected 413 by the MaxBytesReader before
// it is fully buffered.
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

// putBlockRequest drives PutBlock with an explicit body and an explicit declared
// length, because the two are different enforcement paths and the test must be
// able to disagree with itself about them: ContentLength is the attacker-
// controlled fast path, the body is what MaxBytesReader actually measures.
// declaredLen of -1 leaves the length unknown (chunked), which is precisely the
// case the fast path cannot cover.
func putBlockRequest(t *testing.T, h *SyncHandler, body io.Reader, declaredLen int64) int {
	t.Helper()
	r := setupSyncTestRouter()
	r.POST("/seafhttp/repo/:repo_id/block/:block_id", h.PutBlock)

	blockID := strings.Repeat("a", 64) // valid 64-hex SHA-256 id
	req := httptest.NewRequest(http.MethodPost, "/seafhttp/repo/repo/block/"+blockID, body)
	req.ContentLength = declaredLen
	if declaredLen < 0 {
		req.TransferEncoding = []string{"chunked"}
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

// TestPutBlockBoundsBodySize verifies PutBlock rejects an oversized block before
// reading it and lets a block declared exactly at the cap through the size gate.
//
// These subtests spoof ContentLength over a tiny body on purpose: they pin the
// cheap pre-read rejection, which is the only thing a declared length can prove.
// That a real body is measured — and that a client which declares nothing is
// still bounded — is TestPutBlockBoundsRealBody below; neither test substitutes
// for the other.
func TestPutBlockBoundsBodySize(t *testing.T) {
	putBlockDeclaring := func(t *testing.T, h *SyncHandler, declaredLen int64) int {
		t.Helper()
		return putBlockRequest(t, h, strings.NewReader("small"), declaredLen)
	}

	// A handler with no config falls back to the package default rather than to
	// something permissive: failing open here would restore the unbounded read.
	t.Run("nil config uses the default cap", func(t *testing.T) {
		h := &SyncHandler{}
		if got := h.syncBlockMaxBytes(); got != config.DefaultSyncBlockMaxBytes {
			t.Fatalf("cap = %d, want the %d default", got, config.DefaultSyncBlockMaxBytes)
		}
		if code := putBlockDeclaring(t, h, config.DefaultSyncBlockMaxBytes+1); code != http.StatusRequestEntityTooLarge {
			t.Errorf("oversized block = %d, want 413", code)
		}
		// Exactly at the cap must pass the size gate (it fails later — 503 with no
		// storage configured — but never 413).
		if code := putBlockDeclaring(t, h, config.DefaultSyncBlockMaxBytes); code == http.StatusRequestEntityTooLarge {
			t.Errorf("block at the size cap was rejected 413, want it through the size gate")
		}
	})

	// The cap is configuration, not a constant. Without this, the default could be
	// changed and the operator lever silently stop being wired.
	t.Run("configured cap overrides the default", func(t *testing.T) {
		const configured = 4 * 1024 * 1024
		h := &SyncHandler{config: &config.Config{}}
		h.config.SeafHTTP.SyncBlockMaxBytes = configured

		if got := h.syncBlockMaxBytes(); got != configured {
			t.Fatalf("cap = %d, want the configured %d", got, configured)
		}
		if code := putBlockDeclaring(t, h, configured+1); code != http.StatusRequestEntityTooLarge {
			t.Errorf("block over the configured cap = %d, want 413", code)
		}
		// Sized between the configured cap and the default: proves the rejection
		// follows configuration rather than the default constant.
		if code := putBlockDeclaring(t, h, config.DefaultSyncBlockMaxBytes); code != http.StatusRequestEntityTooLarge {
			t.Errorf("block above the configured cap but under the default = %d, want 413", code)
		}
	})
}

// TestPutBlockBoundsRealBody sends bytes rather than a declared length, which is
// what the declared-length subtests above cannot do.
//
// Right-sizing the cap from 257 MiB to 16 MiB has exactly one failure mode:
// cutting traffic this route legitimately carries. So the 8 MiB block — the size
// SesameFS splits at — is uploaded for real, through MaxBytesReader and the full
// buffering read, not merely declared in a header. The mirror case sends more
// than the cap with the length withheld, the shape a lying or chunked client
// uses to walk past the fast path.
func TestPutBlockBoundsRealBody(t *testing.T) {
	t.Run("a real 8 MiB body passes under the default cap", func(t *testing.T) {
		h := &SyncHandler{}
		body := bytes.Repeat([]byte("a"), 8*1024*1024)

		code := putBlockRequest(t, h, bytes.NewReader(body), int64(len(body)))
		if code == http.StatusRequestEntityTooLarge {
			t.Fatalf("an 8 MiB block — the size this route splits at — was rejected 413 by the %d-byte default cap",
				config.DefaultSyncBlockMaxBytes)
		}
	})

	// A small configured cap keeps this cheap while exercising the same code: the
	// point is the unknown length, not the number of bytes.
	t.Run("an oversized body with no declared length is still cut", func(t *testing.T) {
		const configured = 64 * 1024
		h := &SyncHandler{config: &config.Config{}}
		h.config.SeafHTTP.SyncBlockMaxBytes = configured

		body := bytes.Repeat([]byte("a"), configured+1)
		if code := putBlockRequest(t, h, bytes.NewReader(body), -1); code != http.StatusRequestEntityTooLarge {
			t.Fatalf("chunked body over the configured cap = %d, want 413; a client that declares no length must still be bounded", code)
		}
	})

	// The same client shape, this time under the cap: proves the case above is the
	// cap firing and not chunked requests being rejected wholesale.
	t.Run("a body under the cap with no declared length is accepted", func(t *testing.T) {
		const configured = 64 * 1024
		h := &SyncHandler{config: &config.Config{}}
		h.config.SeafHTTP.SyncBlockMaxBytes = configured

		body := bytes.Repeat([]byte("a"), configured)
		if code := putBlockRequest(t, h, bytes.NewReader(body), -1); code == http.StatusRequestEntityTooLarge {
			t.Fatal("chunked body exactly at the configured cap was rejected 413")
		}
	})
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

	if code := postCheckBlocks(t, newlineBody(config.DefaultCheckBlocksMaxIDs+1)); code != http.StatusRequestEntityTooLarge {
		t.Errorf("check-blocks over id cap = %d, want 413", code)
	}
	if code := postCheckBlocks(t, newlineBody(config.DefaultCheckBlocksMaxIDs)); code == http.StatusRequestEntityTooLarge {
		t.Errorf("check-blocks at id cap was rejected 413, want it through the size gate")
	}
}

// parseCheckBlockIDsForTest drives the parser directly and reports the ids, the
// ok flag and the status code written on rejection.
func parseCheckBlockIDsForTest(body string) ([]string, bool, int) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	ids, ok := parseBoundedIDList(c, []byte(body), checkBlocksIDListSpec(config.DefaultCheckBlocksMaxIDs))
	return ids, ok, w.Code
}

// TestParseCheckBlockIDsJSONBoundary pins the id cap on the JSON path, which the
// newline-only coverage left untested: exactly the cap is accepted, one more is
// rejected 413.
func TestParseCheckBlockIDsJSONBoundary(t *testing.T) {
	jsonBody := func(n int) string {
		return "[" + strings.TrimSuffix(strings.Repeat(`"a",`, n), ",") + "]"
	}

	ids, ok, code := parseCheckBlockIDsForTest(jsonBody(config.DefaultCheckBlocksMaxIDs))
	if !ok {
		t.Fatalf("JSON at id cap rejected with %d, want accepted", code)
	}
	if len(ids) != config.DefaultCheckBlocksMaxIDs {
		t.Fatalf("JSON at id cap parsed %d ids, want %d", len(ids), config.DefaultCheckBlocksMaxIDs)
	}
	if _, ok, code := parseCheckBlockIDsForTest(jsonBody(config.DefaultCheckBlocksMaxIDs + 1)); ok || code != http.StatusRequestEntityTooLarge {
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

// TestIDListSpec413BodiesAreStable pins the client-visible 413 *schema* across the
// generalization of parseCheckBlockIDs into parseBoundedIDList — the error text, the
// cap field's name, and the cap's value. Not byte equality: key order in a gin.H is
// not a contract anyone should depend on, and pinning it would fail on an unrelated
// field being added rather than on the shape actually changing.
//
// check-blocks' schema predates the refactor and must survive it. The two fs routes
// name fs ids because their 413 is new; calling them "block ids" would be a lie a
// future reader would have to debug.
func TestIDListSpec413BodiesAreStable(t *testing.T) {
	for _, tc := range []struct {
		name      string
		spec      idListSpec
		wantError string
		wantField string
	}{
		{"check-blocks", checkBlocksIDListSpec(7), "too many block ids", "max_block_ids"},
		{"pack-fs", packFSIDListSpec(), "too many fs ids", "max_fs_ids"},
		{"check-fs", checkFSIDListSpec(), "too many fs ids", "max_fs_ids"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/", nil)

			// One id past the cap on the cheap newline path.
			body := strings.Repeat("a\n", tc.spec.maxIDs) + "a"
			if _, ok := parseBoundedIDList(c, []byte(body), tc.spec); ok {
				t.Fatal("oversized list accepted")
			}
			if w.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("code = %d, want 413", w.Code)
			}
			var got map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatalf("413 body %q is not JSON: %v", w.Body.String(), err)
			}
			if got["error"] != tc.wantError {
				t.Errorf("error = %v, want %q", got["error"], tc.wantError)
			}
			// The field must carry the cap that actually fired, not merely exist:
			// a 413 that reports the wrong number is worse than one that reports
			// none, since an operator would tune against it.
			if got[tc.wantField] != float64(tc.spec.maxIDs) {
				t.Errorf("413 body[%q] = %v, want the cap %d that fired (body=%v)", tc.wantField, got[tc.wantField], tc.spec.maxIDs, got)
			}
		})
	}
}

// TestFSIDCapsCannotCutWellFormedBodies is the non-regression contract for the
// pack-fs/check-fs id caps: they must be unreachable for any body the byte cap
// already admits, so they can only ever fire on degenerate input.
//
// The densest well-formed body is the newline format — N ids of 40 hex chars with
// N-1 separators, so 41N-1 bytes. If that N ever exceeds the id cap, the cap has
// started cutting legitimate traffic (a very large library's fs-id list) rather
// than only amplification, which is the one failure mode this defense must not
// have. Asserted arithmetically rather than by sending 16 MiB.
func TestFSIDCapsCannotCutWellFormedBodies(t *testing.T) {
	for _, tc := range []struct {
		name     string
		byteCap  int
		idCap    int
		jsonCost int
	}{
		{"pack-fs", maxPackFSBodyBytes, maxPackFSIDs, 43},
		{"check-fs", maxCheckFSBodyBytes, maxCheckFSIDs, 43},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// 41N-1 <= byteCap  =>  N <= (byteCap+1)/41
			densestNewline := (tc.byteCap + 1) / minFSIDWireBytes
			if densestNewline > tc.idCap {
				t.Fatalf("a well-formed newline body within the %d-byte cap carries up to %d ids, above the %d id cap: the cap would reject real traffic, not just amplification",
					tc.byteCap, densestNewline, tc.idCap)
			}
			// JSON is strictly less dense (2 + 42N + (N-1) bytes), so it cannot
			// bind either; asserted so a future format change is caught here.
			densestJSON := (tc.byteCap - 1) / tc.jsonCost
			if densestJSON > tc.idCap {
				t.Fatalf("a well-formed JSON body within the %d-byte cap carries up to %d ids, above the %d id cap", tc.byteCap, densestJSON, tc.idCap)
			}
		})
	}
}

// TestFSIDCountCapsCutAmplification is the regression for the gap the byte caps
// alone left open on pack-fs and check-fs: a body *under* the byte cap that
// explodes into ~17x its size in string headers (16 MiB of bare newlines ->
// ~16.7M ids, ~268 MB). Both routes now share check-blocks' bounded parser, so
// the list is refused during the parse instead of after it is materialized.
//
// Same allocation-canary caveats as TestParseCheckBlockIDsRejectsBeforeMaterializing:
// TotalAlloc is process-global, so this must never be made parallel, and a failure
// under a new Go version should be read as "re-measure the headroom" first.
func TestFSIDCountCapsCutAmplification(t *testing.T) {
	// A body at the byte cap made of bare newlines: ~1 byte per id, the worst
	// case, and one TrimSpace cannot collapse because both ends are non-space.
	degenerate := func(n int) string { return "a" + strings.Repeat("\n", n-2) + "a" }

	for _, tc := range []struct {
		name    string
		route   string
		handler func(*SyncHandler) gin.HandlerFunc
		byteCap int
	}{
		{"pack-fs", "/seafhttp/repo/:repo_id/pack-fs", func(h *SyncHandler) gin.HandlerFunc { return h.PackFS }, maxPackFSBodyBytes},
		{"check-fs", "/seafhttp/repo/:repo_id/check-fs", func(h *SyncHandler) gin.HandlerFunc { return h.CheckFS }, maxCheckFSBodyBytes},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := setupSyncTestRouter()
			h := &SyncHandler{}
			r.POST(tc.route, tc.handler(h))

			body := degenerate(tc.byteCap)
			req := httptest.NewRequest(http.MethodPost, strings.Replace(tc.route, ":repo_id", "repo", 1), strings.NewReader(body))
			req.ContentLength = int64(len(body))
			w := httptest.NewRecorder()

			var m1, m2 runtime.MemStats
			runtime.ReadMemStats(&m1)
			r.ServeHTTP(w, req)
			runtime.ReadMemStats(&m2)

			if w.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("degenerate body under the byte cap = %d, want 413; body=%s", w.Code, w.Body.String())
			}
			// Same 96 MB threshold the check-blocks canary uses: comfortably above
			// the unavoidable cost (the body plus its string conversion, ~16 MiB
			// each) and far below the ~268 MB a materializing parse would spend.
			const maxAllocBytes = 96 * 1024 * 1024
			allocated := m2.TotalAlloc - m1.TotalAlloc
			t.Logf("body=%d bytes, allocated %.1f MB", len(body), float64(allocated)/(1024*1024))
			if allocated > maxAllocBytes {
				t.Fatalf("allocated %.1f MB on a rejected body, want < %d MB: the id cap is being applied after the list is materialized",
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

// TestPutCommitBoundsBodySize covers ISSUE-SYNC-UNBOUNDED-BODIES-01 / X9 for
// PutCommit, the first of the four sibling handlers PR-10 left on unbounded
// io.ReadAll. Only the rejection boundary is pinned (cap+1 -> 413); the positive
// case below asserts the size gate is passed (not 413) using an invalid body
// that fails cleanly at JSON parsing (400) rather than reaching h.db, which is
// nil in this handler and would panic on the unconditional DB write PutCommit
// performs after a valid commit — that write is out of scope for this cap.
func TestPutCommitBoundsBodySize(t *testing.T) {
	postPutCommit := func(t *testing.T, body string, declaredLen int64) int {
		t.Helper()
		r := setupSyncTestRouter()
		h := &SyncHandler{}
		r.PUT("/seafhttp/repo/:repo_id/commit/:commit_id", h.PutCommit)
		req := httptest.NewRequest(http.MethodPut, "/seafhttp/repo/repo/commit/somecommitid", strings.NewReader(body))
		req.ContentLength = declaredLen
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}

	if code := postPutCommit(t, strings.Repeat("a", maxPutCommitBodyBytes+1), int64(maxPutCommitBodyBytes+1)); code != http.StatusRequestEntityTooLarge {
		t.Errorf("PutCommit body over cap = %d, want 413", code)
	}
	// Not valid JSON, so it fails at "invalid commit format" (400) rather than
	// reaching the database — this proves the size gate let it through, not a
	// parser accident, since a malformed body is a stable, intentional 400.
	invalidJSON := strings.Repeat("a", maxPutCommitBodyBytes)
	if code := postPutCommit(t, invalidJSON, int64(maxPutCommitBodyBytes)); code == http.StatusRequestEntityTooLarge {
		t.Errorf("PutCommit body at cap was rejected 413, want it through the size gate")
	} else if code != http.StatusBadRequest {
		t.Errorf("PutCommit body at cap (invalid JSON) = %d, want 400", code)
	}
}

// TestPackFSBoundsBodySize covers ISSUE-SYNC-UNBOUNDED-BODIES-01 / X9 for
// PackFS. Only the rejection boundary is pinned: what PackFS does with a body
// under the cap depends on parser/DB behavior that is not this cap's contract.
func TestPackFSBoundsBodySize(t *testing.T) {
	r := setupSyncTestRouter()
	h := &SyncHandler{}
	r.POST("/seafhttp/repo/:repo_id/pack-fs", h.PackFS)

	body := strings.Repeat("a", maxPackFSBodyBytes+1)
	req := httptest.NewRequest(http.MethodPost, "/seafhttp/repo/repo/pack-fs", strings.NewReader(body))
	req.ContentLength = int64(len(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("PackFS body over cap = %d, want 413", w.Code)
	}
}

// TestPackFSBoundsChunkedBody mirrors TestCheckBlocksBoundsChunkedBody for
// PackFS: a client that declares no Content-Length must still be cut by
// MaxBytesReader, not merely by the declared-length fast path — precisely the
// shape io.ReadAll without a limit could not bound before this fix.
func TestPackFSBoundsChunkedBody(t *testing.T) {
	r := setupSyncTestRouter()
	h := &SyncHandler{}
	r.POST("/seafhttp/repo/:repo_id/pack-fs", h.PackFS)

	body := strings.NewReader(strings.Repeat("a", maxPackFSBodyBytes+1))
	req := httptest.NewRequest(http.MethodPost, "/seafhttp/repo/repo/pack-fs", body)
	req.ContentLength = -1
	req.TransferEncoding = []string{"chunked"}

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("chunked oversized PackFS body = %d, want 413; body=%s", w.Code, w.Body.String())
	}
}

// TestCheckFSBoundsBodySize covers ISSUE-SYNC-UNBOUNDED-BODIES-01 / X9 for
// CheckFS. Only the rejection boundary is pinned: unlike PackFS/RecvFS, CheckFS
// touches h.db unconditionally right after parsing (even for an empty id list),
// so there is no body shape that reaches a positive result without a live DB.
func TestCheckFSBoundsBodySize(t *testing.T) {
	r := setupSyncTestRouter()
	h := &SyncHandler{}
	r.POST("/seafhttp/repo/:repo_id/check-fs", h.CheckFS)

	body := strings.Repeat("a", maxCheckFSBodyBytes+1)
	req := httptest.NewRequest(http.MethodPost, "/seafhttp/repo/repo/check-fs", strings.NewReader(body))
	req.ContentLength = int64(len(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("CheckFS body over cap = %d, want 413", w.Code)
	}
}

// TestRecvFSBoundsBodySize covers ISSUE-SYNC-UNBOUNDED-BODIES-01 / X9 for
// RecvFS, the one handler of the four where the cap is configuration
// (config.SeafHTTP.RecvFSMaxBytes) rather than a const, because unlike the
// other three it carries a real batch payload with no measured client size to
// anchor a fixed number on.
//
// The two properties are pinned separately and deliberately: the resolver returns
// the default under a nil config, and the handler enforces whatever the resolver
// returns. Driving a body over the 128 MiB *default* through HTTP would prove
// nothing the small configured cap does not already prove, while costing ~404 MB
// of allocation (~128 MiB for the body plus ~269 MB of io.ReadAll buffer growth) —
// four times the 96 MB ceiling this same file treats as a failure condition in
// TestParseCheckBlockIDsRejectsBeforeMaterializing.
func TestRecvFSBoundsBodySize(t *testing.T) {
	t.Run("nil config resolves the default cap", func(t *testing.T) {
		h := &SyncHandler{}
		if got := h.syncRecvFSMaxBytes(); got != config.DefaultRecvFSMaxBytes {
			t.Fatalf("cap = %d, want the %d default", got, config.DefaultRecvFSMaxBytes)
		}
	})

	// A small configured cap keeps this cheap while exercising the same code: the
	// point is that RecvFS reads through syncRecvFSMaxBytes(), not the byte count.
	t.Run("the handler enforces the resolved cap", func(t *testing.T) {
		const configured = 64 * 1024
		h := &SyncHandler{config: &config.Config{}}
		h.config.SeafHTTP.RecvFSMaxBytes = configured

		if got := h.syncRecvFSMaxBytes(); got != configured {
			t.Fatalf("cap = %d, want the configured %d", got, configured)
		}

		postRecvFS := func(bodyLen int, declaredLen int64) int {
			r := setupSyncTestRouter()
			r.POST("/seafhttp/repo/:repo_id/recv-fs", h.RecvFS)
			req := httptest.NewRequest(http.MethodPost, "/seafhttp/repo/repo/recv-fs", strings.NewReader(strings.Repeat("a", bodyLen)))
			req.ContentLength = declaredLen
			if declaredLen < 0 {
				req.TransferEncoding = []string{"chunked"}
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			return w.Code
		}

		if code := postRecvFS(configured+1, int64(configured+1)); code != http.StatusRequestEntityTooLarge {
			t.Errorf("RecvFS body over the configured cap = %d, want 413", code)
		}
		// A client that declares no length must still be cut — the shape an
		// unbounded io.ReadAll could not bound, and the one a declared-length
		// check alone would miss.
		if code := postRecvFS(configured+1, -1); code != http.StatusRequestEntityTooLarge {
			t.Errorf("chunked RecvFS body over the configured cap = %d, want 413", code)
		}
		// The same shape under the cap: proves the two cases above are the cap
		// firing, not RecvFS rejecting these requests for some other reason.
		if code := postRecvFS(configured, -1); code == http.StatusRequestEntityTooLarge {
			t.Error("chunked RecvFS body exactly at the configured cap was rejected 413")
		}
	})
}
