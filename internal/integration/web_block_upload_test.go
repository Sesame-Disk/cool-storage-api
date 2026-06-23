//go:build integration

package integration

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func webCreateBlockSession(t *testing.T, c *testClient, repoID, parentDir string) string {
	t.Helper()
	resp := c.PostJSON(t, fmt.Sprintf("/api/v2/repos/%s/block-upload-session/", repoID), map[string]string{"parent_dir": parentDir})
	expectStatus(t, resp, http.StatusOK)
	var out map[string]interface{}
	decodeJSON(t, resp, &out)
	sid, _ := out["session_id"].(string)
	if sid == "" {
		t.Fatalf("empty session_id in response: %v", out)
	}
	return sid
}

// webUploadBlock POSTs raw block bytes under a session. Returns the response.
func webUploadBlock(t *testing.T, c *testClient, session string, data []byte) *http.Response {
	t.Helper()
	url := c.baseURL + "/api/v2/blocks/upload?session=" + session
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		t.Fatalf("new block upload request: %v", err)
	}
	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Block-Hash", sha256hex(data))
	req.ContentLength = int64(len(data))
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatalf("block upload request failed: %v", err)
	}
	return resp
}

// webUploadBlockLegacy POSTs raw block bytes WITHOUT a session (S3 only, no
// materialization) — mirrors the desktop/mobile oracle path.
func webUploadBlockLegacy(t *testing.T, c *testClient, data []byte) *http.Response {
	t.Helper()
	url := c.baseURL + "/api/v2/blocks/upload"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		t.Fatalf("new legacy block upload request: %v", err)
	}
	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Block-Hash", sha256hex(data))
	req.ContentLength = int64(len(data))
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatalf("legacy block upload request failed: %v", err)
	}
	return resp
}

func webCheckBlocks(t *testing.T, c *testClient, session string, hashes []string) (existing, missing []string) {
	t.Helper()
	resp := c.PostJSON(t, "/api/v2/blocks/check?session="+session, map[string]interface{}{"hashes": hashes})
	expectStatus(t, resp, http.StatusOK)
	var out struct {
		Existing []string `json:"existing"`
		Missing  []string `json:"missing"`
	}
	decodeJSON(t, resp, &out)
	return out.Existing, out.Missing
}

func webCommit(t *testing.T, c *testClient, repoID string, manifest map[string]interface{}) *http.Response {
	t.Helper()
	return c.PostJSON(t, fmt.Sprintf("/api/v2/repos/%s/file-from-blocks/", repoID), manifest)
}

// blocksManifest builds the ordered manifest entries from raw block byte slices.
func blocksManifest(blocks [][]byte) []map[string]interface{} {
	out := make([]map[string]interface{}, len(blocks))
	for i, b := range blocks {
		out[i] = map[string]interface{}{"sha256": sha256hex(b), "size": len(b)}
	}
	return out
}

func totalSize(blocks [][]byte) int {
	n := 0
	for _, b := range blocks {
		n += len(b)
	}
	return n
}

// downloadRepoFile fetches a file's content through the standard two-step flow.
func downloadRepoFile(t *testing.T, c *testClient, repoID, path string) []byte {
	t.Helper()
	dlResp := c.Get(t, fmt.Sprintf("/api2/repos/%s/file/?p=%s", repoID, path))
	expectStatus(t, dlResp, http.StatusOK)
	downloadURL := strings.Trim(responseBody(t, dlResp), "\" \n\r")
	req, _ := http.NewRequest(http.MethodGet, downloadURL, nil)
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("download status %d: %s", resp.StatusCode, body)
	}
	data, _ := io.ReadAll(resp.Body)
	return data
}

// uploadFileViaBlocksFlow runs the full client flow (session→check→upload→commit)
// and returns the actual filename from the commit response.
func uploadFileViaBlocksFlow(t *testing.T, c *testClient, repoID, parentDir, filename string, blocks [][]byte, replace bool) *http.Response {
	t.Helper()
	session := webCreateBlockSession(t, c, repoID, parentDir)
	manifest := blocksManifest(blocks)
	hashes := make([]string, len(manifest))
	for i, m := range manifest {
		hashes[i] = m["sha256"].(string)
	}
	_, missing := webCheckBlocks(t, c, session, hashes)
	missingSet := map[string]bool{}
	for _, h := range missing {
		missingSet[h] = true
	}
	for _, b := range blocks {
		if missingSet[sha256hex(b)] {
			resp := webUploadBlock(t, c, session, b)
			if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
				body, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				t.Fatalf("block upload status %d: %s", resp.StatusCode, body)
			}
			resp.Body.Close()
		}
	}
	return webCommit(t, c, repoID, map[string]interface{}{
		"session":    session,
		"parent_dir": parentDir,
		"filename":   filename,
		"replace":    replace,
		"size":       totalSize(blocks),
		"blocks":     manifest,
	})
}

// ── tests ────────────────────────────────────────────────────────────────────

func TestWebBlockUploadRoundTripAndDedup(t *testing.T) {
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-wbu-rt-%d", time.Now().UnixNano()))
	content := []byte("web block upload round-trip content " + fmt.Sprint(time.Now().UnixNano()))
	blocks := [][]byte{content}

	resp := uploadFileViaBlocksFlow(t, adminClient, repoID, "/", "wbu.txt", blocks, false)
	expectStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	got := downloadRepoFile(t, adminClient, repoID, "/wbu.txt")
	if !bytes.Equal(got, content) {
		t.Fatalf("downloaded content mismatch: got %q want %q", got, content)
	}

	// R2: no provisional upload referrers leak after commit.
	assertNoUploadReferrers(t, repoID, "/", "wbu.txt")

	// Dedup/resume: a session over the same hash reports it as already existing.
	session := webCreateBlockSession(t, adminClient, repoID, "/")
	existing, missing := webCheckBlocks(t, adminClient, session, []string{sha256hex(content)})
	if len(missing) != 0 || len(existing) != 1 {
		t.Fatalf("expected block to be existing (dedup), got existing=%v missing=%v", existing, missing)
	}
}

func TestWebBlockUploadMultiBlockOrdering(t *testing.T) {
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-wbu-multi-%d", time.Now().UnixNano()))
	first := bytes.Repeat([]byte("A"), 8*1024*1024) // exactly one 8 MB block
	last := []byte("TAIL-" + fmt.Sprint(time.Now().UnixNano()))
	blocks := [][]byte{first, last}

	resp := uploadFileViaBlocksFlow(t, adminClient, repoID, "/", "multi.bin", blocks, false)
	expectStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	got := downloadRepoFile(t, adminClient, repoID, "/multi.bin")
	want := append(append([]byte{}, first...), last...)
	if !bytes.Equal(got, want) {
		t.Fatalf("multiblock download mismatch: got %d bytes want %d bytes", len(got), len(want))
	}
}

func TestWebBlockUploadRejectsUncommittableBlocks(t *testing.T) {
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-wbu-r1-%d", time.Now().UnixNano()))

	t.Run("manifest block never uploaded -> needs_upload", func(t *testing.T) {
		session := webCreateBlockSession(t, adminClient, repoID, "/")
		content := []byte("never uploaded " + fmt.Sprint(time.Now().UnixNano()))
		resp := webCommit(t, adminClient, repoID, map[string]interface{}{
			"session": session, "parent_dir": "/", "filename": "ghost.txt",
			"replace": false, "size": len(content),
			"blocks": []map[string]interface{}{{"sha256": sha256hex(content), "size": len(content)}},
		})
		expectStatus(t, resp, http.StatusConflict)
		var out map[string]interface{}
		decodeJSON(t, resp, &out)
		if _, ok := out["needs_upload"]; !ok {
			t.Fatalf("expected needs_upload, got %v", out)
		}
	})

	t.Run("S3-only block (legacy upload, no metadata) -> needs_upload", func(t *testing.T) {
		content := []byte("s3 only no metadata " + fmt.Sprint(time.Now().UnixNano()))
		// Store physically in S3 but DO NOT materialize (no session).
		legacy := webUploadBlockLegacy(t, adminClient, content)
		if legacy.StatusCode != http.StatusOK && legacy.StatusCode != http.StatusCreated {
			body, _ := io.ReadAll(legacy.Body)
			legacy.Body.Close()
			t.Fatalf("legacy block upload status %d: %s", legacy.StatusCode, body)
		}
		legacy.Body.Close()

		session := webCreateBlockSession(t, adminClient, repoID, "/")
		// Session-aware check must report it MISSING despite S3 presence (R3).
		_, missing := webCheckBlocks(t, adminClient, session, []string{sha256hex(content)})
		if len(missing) != 1 {
			t.Fatalf("expected S3-only block reported missing, got missing=%v", missing)
		}
		// Commit without uploading under the session must refuse it (R1).
		resp := webCommit(t, adminClient, repoID, map[string]interface{}{
			"session": session, "parent_dir": "/", "filename": "s3only.txt",
			"replace": false, "size": len(content),
			"blocks": []map[string]interface{}{{"sha256": sha256hex(content), "size": len(content)}},
		})
		expectStatus(t, resp, http.StatusConflict)
		resp.Body.Close()
	})
}

func TestWebBlockUploadManifestValidation(t *testing.T) {
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-wbu-r6-%d", time.Now().UnixNano()))
	session := webCreateBlockSession(t, adminClient, repoID, "/")
	content := []byte("size sum mismatch")

	// sum(block sizes) != declared size → 400.
	resp := webCommit(t, adminClient, repoID, map[string]interface{}{
		"session": session, "parent_dir": "/", "filename": "bad.txt",
		"replace": false, "size": len(content) + 100,
		"blocks": []map[string]interface{}{{"sha256": sha256hex(content), "size": len(content)}},
	})
	expectStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()
}

func TestWebBlockUploadSizeMismatch(t *testing.T) {
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-wbu-r11-%d", time.Now().UnixNano()))
	session := webCreateBlockSession(t, adminClient, repoID, "/")
	content := []byte("ten bytes!") // 10 bytes
	resp := webUploadBlock(t, adminClient, session, content)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload status %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Manifest lies about the block size (20) while the stored block is 10.
	commit := webCommit(t, adminClient, repoID, map[string]interface{}{
		"session": session, "parent_dir": "/", "filename": "lie.txt",
		"replace": false, "size": 20,
		"blocks": []map[string]interface{}{{"sha256": sha256hex(content), "size": 20}},
	})
	expectStatus(t, commit, http.StatusUnprocessableEntity)
	commit.Body.Close()
}

func TestWebBlockUploadGCFenceRejected(t *testing.T) {
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-wbu-gc-%d", time.Now().UnixNano()))
	orgID := resolveOrgID(t, repoID)
	session := webCreateBlockSession(t, adminClient, repoID, "/")
	content := []byte("gc fenced block " + fmt.Sprint(time.Now().UnixNano()))
	hash := sha256hex(content)

	resp := webUploadBlock(t, adminClient, session, content)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload status %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Simulate the GC worker claiming the block for deletion.
	db := shareProjectionDBForTest(t).Session()
	if err := db.Query(`UPDATE blocks SET gc_state = 'deleting' WHERE org_id = ? AND block_id = ?`, orgID, hash).Exec(); err != nil {
		t.Fatalf("set gc_state: %v", err)
	}
	defer db.Query(`UPDATE blocks SET gc_state = '' WHERE org_id = ? AND block_id = ?`, orgID, hash).Exec()

	commit := webCommit(t, adminClient, repoID, map[string]interface{}{
		"session": session, "parent_dir": "/", "filename": "fenced.txt",
		"replace": false, "size": len(content),
		"blocks": []map[string]interface{}{{"sha256": hash, "size": len(content)}},
	})
	expectStatus(t, commit, http.StatusConflict)
	commit.Body.Close()
}

func TestWebBlockUploadIdempotentCommit(t *testing.T) {
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-wbu-idem-%d", time.Now().UnixNano()))
	content := []byte("idempotent commit content " + fmt.Sprint(time.Now().UnixNano()))
	session := webCreateBlockSession(t, adminClient, repoID, "/")
	resp := webUploadBlock(t, adminClient, session, content)
	resp.Body.Close()

	manifest := map[string]interface{}{
		"session": session, "parent_dir": "/", "filename": "idem.txt",
		"replace": false, "size": len(content),
		"blocks": []map[string]interface{}{{"sha256": sha256hex(content), "size": len(content)}},
	}

	first := webCommit(t, adminClient, repoID, manifest)
	expectStatus(t, first, http.StatusOK)
	first.Body.Close()

	// Replaying the same commit must return success WITHOUT creating "idem (1).txt".
	second := webCommit(t, adminClient, repoID, manifest)
	expectStatus(t, second, http.StatusOK)
	second.Body.Close()

	listResp := adminClient.Get(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/", repoID))
	expectStatus(t, listResp, http.StatusOK)
	var dirList map[string]interface{}
	decodeJSON(t, listResp, &dirList)
	entries, _ := dirList["dirent_list"].([]interface{})
	count := 0
	for _, e := range entries {
		entry, _ := e.(map[string]interface{})
		name, _ := entry["name"].(string)
		if strings.HasPrefix(name, "idem") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("idempotent replay created duplicates: %d files match 'idem'", count)
	}
}

func TestWebBlockUploadConcurrentDoubleCommit(t *testing.T) {
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-wbu-conc-%d", time.Now().UnixNano()))
	content := []byte("concurrent commit content " + fmt.Sprint(time.Now().UnixNano()))
	session := webCreateBlockSession(t, adminClient, repoID, "/")
	resp := webUploadBlock(t, adminClient, session, content)
	resp.Body.Close()

	manifest := map[string]interface{}{
		"session": session, "parent_dir": "/", "filename": "conc.txt",
		"replace": false, "size": len(content),
		"blocks": []map[string]interface{}{{"sha256": sha256hex(content), "size": len(content)}},
	}

	var wg sync.WaitGroup
	statuses := make([]int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			r := webCommit(t, adminClient, repoID, manifest)
			statuses[idx] = r.StatusCode
			r.Body.Close()
		}(i)
	}
	wg.Wait()

	// Both should succeed (idempotent) — neither a 500 nor a duplicate file.
	for i, s := range statuses {
		if s != http.StatusOK {
			t.Fatalf("commit %d returned %d, want 200", i, s)
		}
	}
	listResp := adminClient.Get(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/", repoID))
	var dirList map[string]interface{}
	decodeJSON(t, listResp, &dirList)
	entries, _ := dirList["dirent_list"].([]interface{})
	count := 0
	for _, e := range entries {
		entry, _ := e.(map[string]interface{})
		if name, _ := entry["name"].(string); strings.HasPrefix(name, "conc") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("concurrent double commit created %d files matching 'conc', want 1", count)
	}
}

func TestWebBlockUploadCompatibilityOps(t *testing.T) {
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-wbu-compat-%d", time.Now().UnixNano()))
	content := []byte("compat ops content " + fmt.Sprint(time.Now().UnixNano()))

	resp := uploadFileViaBlocksFlow(t, adminClient, repoID, "/", "orig.txt", [][]byte{content}, false)
	expectStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// R4: a file whose fs_object.block_ids are SHA-256 (web flow) must behave like
	// any other file. Rename rewrites the fs metadata; the renamed file must still
	// resolve its blocks on download.
	renameResp := adminClient.PostJSON(t,
		fmt.Sprintf("/api2/repos/%s/file/?operation=rename&p=/orig.txt", repoID),
		map[string]string{"newname": "renamed.txt"})
	if renameResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(renameResp.Body)
		renameResp.Body.Close()
		t.Fatalf("rename status %d: %s", renameResp.StatusCode, body)
	}
	renameResp.Body.Close()

	got := downloadRepoFile(t, adminClient, repoID, "/renamed.txt")
	if !bytes.Equal(got, content) {
		t.Fatalf("content after rename mismatch: got %q want %q", got, content)
	}

	// History/versioning over the web file must be listable.
	histResp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/file/revisions/?p=/renamed.txt", repoID))
	histResp.Body.Close()
	if histResp.StatusCode >= 500 {
		t.Fatalf("file revisions returned server error %d", histResp.StatusCode)
	}
}
