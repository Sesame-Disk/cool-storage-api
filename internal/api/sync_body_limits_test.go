package api

import (
	"net/http"
	"net/http/httptest"
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
