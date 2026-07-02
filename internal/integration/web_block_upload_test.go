//go:build integration

package integration

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

func sha1hex(b []byte) string {
	sum := sha1.Sum(b)
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
	url := c.baseURL + "/api/v2/blocks/upload"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		t.Fatalf("new block upload request: %v", err)
	}
	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Block-Hash", sha256hex(data))
	req.Header.Set("X-Block-Upload-Session", session)
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
	data, err := json.Marshal(map[string]interface{}{"hashes": hashes})
	if err != nil {
		t.Fatalf("marshal block check request: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/api/v2/blocks/check", bytes.NewBuffer(data))
	if err != nil {
		t.Fatalf("new block check request: %v", err)
	}
	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Block-Upload-Session", session)
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatalf("block check request failed: %v", err)
	}
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

// blocksManifest builds the ordered dual-hash manifest entries from raw block
// byte slices (sha256 = storage identity, sha1 = external Seafile block ID).
func blocksManifest(blocks [][]byte) []map[string]interface{} {
	out := make([]map[string]interface{}, len(blocks))
	for i, b := range blocks {
		out[i] = map[string]interface{}{"sha1": sha1hex(b), "sha256": sha256hex(b), "size": len(b)}
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

// TestWebBlockUploadFSObjectUsesSHA1ForDesktopCompat is the regression guard for
// the post-flip canonical layout: a file committed via the web block flow must
// store SHA-256 block IDs in fs_objects.block_ids for fast internal reads, while
// the Seafile-compatible SHA-1 list lives in fs_objects.seafile_block_ids_sha1 so
// sync endpoints can still serialize the 40-hex IDs the desktop client expects.
func TestWebBlockUploadFSObjectUsesSHA1ForDesktopCompat(t *testing.T) {
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-wbu-sha1-%d", time.Now().UnixNano()))
	first := bytes.Repeat([]byte("Z"), 8*1024*1024) // one full 8 MB block
	last := []byte("desktop-compat-tail-" + fmt.Sprint(time.Now().UnixNano()))
	blocks := [][]byte{first, last}

	resp := uploadFileViaBlocksFlow(t, adminClient, repoID, "/", "compat.bin", blocks, false)
	expectStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	orgID := resolveOrgID(t, repoID)
	session := shareProjectionDBForTest(t).Session()

	var headCommit string
	if err := session.Query(`SELECT head_commit_id FROM libraries WHERE org_id = ? AND library_id = ?`, orgID, repoID).Scan(&headCommit); err != nil {
		t.Fatalf("read head commit: %v", err)
	}
	var rootFSID string
	if err := session.Query(`SELECT root_fs_id FROM commits WHERE library_id = ? AND commit_id = ?`, repoID, headCommit).Scan(&rootFSID); err != nil {
		t.Fatalf("read root fs: %v", err)
	}
	var dirEntriesJSON string
	if err := session.Query(`SELECT dir_entries FROM fs_objects WHERE library_id = ? AND fs_id = ?`, repoID, rootFSID).Scan(&dirEntriesJSON); err != nil {
		t.Fatalf("read root dir entries: %v", err)
	}
	var dirEntries []map[string]interface{}
	if err := json.Unmarshal([]byte(dirEntriesJSON), &dirEntries); err != nil {
		t.Fatalf("decode dir entries: %v", err)
	}
	var fileFSID string
	for _, e := range dirEntries {
		if name, _ := e["name"].(string); name == "compat.bin" {
			fileFSID, _ = e["id"].(string)
			break
		}
	}
	if fileFSID == "" {
		t.Fatalf("committed file not found in root dir entries: %s", dirEntriesJSON)
	}

	var blockIDs []string
	var seafileBlockIDs []string
	if err := session.Query(`SELECT block_ids, seafile_block_ids_sha1 FROM fs_objects WHERE library_id = ? AND fs_id = ?`, repoID, fileFSID).Scan(&blockIDs, &seafileBlockIDs); err != nil {
		t.Fatalf("read block ids: %v", err)
	}
	if len(blockIDs) != len(blocks) {
		t.Fatalf("expected %d block ids, got %d", len(blocks), len(blockIDs))
	}
	if len(seafileBlockIDs) != len(blocks) {
		t.Fatalf("expected %d seafile block ids, got %d", len(blocks), len(seafileBlockIDs))
	}

	// Canonical/internal block_ids must be SHA-256, while the Seafile boundary
	// column stays SHA-1 and resolves to the same storage identity.
	for i, internal := range blockIDs {
		if len(internal) != 64 {
			t.Fatalf("block %d internal id %q is not a 64-hex SHA-256", i, internal)
		}
		if want := sha256hex(blocks[i]); internal != want {
			t.Fatalf("block %d sha256 = %s, want %s", i, internal, want)
		}

		ext := seafileBlockIDs[i]
		if len(ext) != 40 {
			t.Fatalf("block %d seafile id %q is not a 40-hex SHA-1 (desktop sync would reject it)", i, ext)
		}
		if want := sha1hex(blocks[i]); ext != want {
			t.Fatalf("block %d seafile sha1 = %s, want %s", i, ext, want)
		}
		var mappedInternal string
		if err := session.Query(`SELECT internal_id FROM block_id_mappings WHERE org_id = ? AND external_id = ?`, orgID, ext).Scan(&mappedInternal); err != nil {
			t.Fatalf("block %d: no SHA-1→SHA-256 mapping for %s: %v", i, ext, err)
		}
		if mappedInternal != internal {
			t.Fatalf("block %d mapping internal_id = %s, want canonical sha256 %s", i, mappedInternal, internal)
		}
	}

	// Download must still reassemble correctly from the canonical SHA-256 list.
	got := downloadRepoFile(t, adminClient, repoID, "/compat.bin")
	want := append(append([]byte{}, first...), last...)
	if !bytes.Equal(got, want) {
		t.Fatalf("download mismatch: got %d bytes want %d bytes", len(got), len(want))
	}
}

// webReadCommittedFileBlockIDs returns the block_ids (SHA-256) and
// seafile_block_ids_sha1 (SHA-1) columns of a committed file, located by walking
// the library HEAD's root dir entries for the given filename.
func webReadCommittedFileBlockIDs(t *testing.T, repoID, filename string) (blockIDs, seafileBlockIDs []string) {
	t.Helper()
	session := shareProjectionDBForTest(t).Session()
	orgID := resolveOrgID(t, repoID)
	var headCommit string
	if err := session.Query(`SELECT head_commit_id FROM libraries WHERE org_id = ? AND library_id = ?`, orgID, repoID).Scan(&headCommit); err != nil {
		t.Fatalf("read head commit: %v", err)
	}
	var rootFSID string
	if err := session.Query(`SELECT root_fs_id FROM commits WHERE library_id = ? AND commit_id = ?`, repoID, headCommit).Scan(&rootFSID); err != nil {
		t.Fatalf("read root fs: %v", err)
	}
	var dirEntriesJSON string
	if err := session.Query(`SELECT dir_entries FROM fs_objects WHERE library_id = ? AND fs_id = ?`, repoID, rootFSID).Scan(&dirEntriesJSON); err != nil {
		t.Fatalf("read root dir entries: %v", err)
	}
	var dirEntries []map[string]interface{}
	if err := json.Unmarshal([]byte(dirEntriesJSON), &dirEntries); err != nil {
		t.Fatalf("decode dir entries: %v", err)
	}
	var fileFSID string
	for _, e := range dirEntries {
		if name, _ := e["name"].(string); name == filename {
			fileFSID, _ = e["id"].(string)
			break
		}
	}
	if fileFSID == "" {
		t.Fatalf("committed file %q not found in root dir entries: %s", filename, dirEntriesJSON)
	}
	if err := session.Query(`SELECT block_ids, seafile_block_ids_sha1 FROM fs_objects WHERE library_id = ? AND fs_id = ?`, repoID, fileFSID).Scan(&blockIDs, &seafileBlockIDs); err != nil {
		t.Fatalf("read block ids: %v", err)
	}
	return blockIDs, seafileBlockIDs
}

// TestWebBlockUploadIgnoresForgedClientSHA1 is the PR5 source-of-truth guard: the
// client no longer asserts a SHA-1 — the server derives it from blocks.sha1 (the
// value it computed from the real bytes at upload). A manifest carrying a forged
// SHA-1 must therefore commit fine, and the fs_object must store the server-derived
// REAL SHA-1, never the forged one.
func TestWebBlockUploadIgnoresForgedClientSHA1(t *testing.T) {
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-wbu-srvsha1-%d", time.Now().UnixNano()))
	session := webCreateBlockSession(t, adminClient, repoID, "/")
	content := []byte("server-derived sha1 " + fmt.Sprint(time.Now().UnixNano()))
	resp := webUploadBlock(t, adminClient, session, content)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload status %d", resp.StatusCode)
	}
	resp.Body.Close()

	forgedSHA1 := strings.Repeat("a", 40) // valid 40-hex, wrong content
	if forgedSHA1 == sha1hex(content) {
		t.Skip("astronomical: forged sha1 equals real sha1")
	}
	commit := webCommit(t, adminClient, repoID, map[string]interface{}{
		"session": session, "parent_dir": "/", "filename": "forged.bin",
		"replace": false, "size": len(content),
		// A legacy/forged sha1 in the manifest is simply ignored by the server.
		"blocks": []map[string]interface{}{{"sha1": forgedSHA1, "sha256": sha256hex(content), "size": len(content)}},
	})
	expectStatus(t, commit, http.StatusOK)
	commit.Body.Close()

	_, seafileBlockIDs := webReadCommittedFileBlockIDs(t, repoID, "forged.bin")
	if len(seafileBlockIDs) != 1 || seafileBlockIDs[0] != sha1hex(content) {
		t.Fatalf("seafile_block_ids_sha1 = %v, want server-derived [%s] (forged client sha1 must be ignored)", seafileBlockIDs, sha1hex(content))
	}

	got := downloadRepoFile(t, adminClient, repoID, "/forged.bin")
	if !bytes.Equal(got, content) {
		t.Fatal("download mismatch")
	}
}

// TestWebBlockUploadCommitIndependentOfReverseMapping proves the commit relies on
// the FORWARD mapping only: deleting the reverse projection row (which is
// best-effort and may lag) must NOT block a commit whose forward row is intact.
// TestWebBlockUploadCommitForwardMappingOnly confirms a web block upload commits
// and downloads using only the forward block_id_mappings row. The reverse index
// (block_id_mappings_by_internal) was dropped in migration 006; commit and the
// desktop bare-SHA-1 block download both resolve through the forward table alone.
func TestWebBlockUploadCommitForwardMappingOnly(t *testing.T) {
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-wbu-fwd-%d", time.Now().UnixNano()))
	session := webCreateBlockSession(t, adminClient, repoID, "/")
	content := []byte("forward-mapping block " + fmt.Sprint(time.Now().UnixNano()))

	resp := webUploadBlock(t, adminClient, session, content)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload status %d", resp.StatusCode)
	}
	resp.Body.Close()

	commit := webCommit(t, adminClient, repoID, map[string]interface{}{
		"session": session, "parent_dir": "/", "filename": "revindep.bin",
		"replace": false, "size": len(content),
		"blocks": []map[string]interface{}{{"sha256": sha256hex(content), "size": len(content)}},
	})
	expectStatus(t, commit, http.StatusOK)
	commit.Body.Close()

	got := downloadRepoFile(t, adminClient, repoID, "/revindep.bin")
	if !bytes.Equal(got, content) {
		t.Fatalf("download mismatch with forward mapping only")
	}
}

// TestWebBlockUploadReplayIgnoresClientSHA1 covers the PR5 idempotency model: the
// manifest digest is over SHA-256 + size only (the SHA-1 is server-derived, not
// part of the logical identity). Replaying a committed session with the same
// SHA-256 but a different client-sent SHA-1 is therefore the SAME file — an
// idempotent 200 replay, never a spurious 409.
func TestWebBlockUploadReplayIgnoresClientSHA1(t *testing.T) {
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-wbu-replay-%d", time.Now().UnixNano()))
	session := webCreateBlockSession(t, adminClient, repoID, "/")
	content := []byte("replay ignores sha1 " + fmt.Sprint(time.Now().UnixNano()))

	resp := webUploadBlock(t, adminClient, session, content)
	resp.Body.Close()

	first := webCommit(t, adminClient, repoID, map[string]interface{}{
		"session": session, "parent_dir": "/", "filename": "replay.bin",
		"replace": false, "size": len(content),
		"blocks": []map[string]interface{}{{"sha256": sha256hex(content), "size": len(content)}},
	})
	expectStatus(t, first, http.StatusOK)
	first.Body.Close()

	// Same session + same sha256 but a stray different client sha1 → same digest →
	// idempotent replay (200), not a different file.
	second := webCommit(t, adminClient, repoID, map[string]interface{}{
		"session": session, "parent_dir": "/", "filename": "replay.bin",
		"replace": false, "size": len(content),
		"blocks": []map[string]interface{}{{"sha1": strings.Repeat("b", 40), "sha256": sha256hex(content), "size": len(content)}},
	})
	expectStatus(t, second, http.StatusOK)
	second.Body.Close()
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
			"blocks": []map[string]interface{}{{"sha1": sha1hex(content), "sha256": sha256hex(content), "size": len(content)}},
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
			"blocks": []map[string]interface{}{{"sha1": sha1hex(content), "sha256": sha256hex(content), "size": len(content)}},
		})
		expectStatus(t, resp, http.StatusConflict)
		resp.Body.Close()
	})
}

func TestWebBlockUploadReuploadRepairsMissingBlockSHA1(t *testing.T) {
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-wbu-repair-%d", time.Now().UnixNano()))
	orgID := resolveOrgID(t, repoID)
	sessionID := webCreateBlockSession(t, adminClient, repoID, "/")
	content := []byte("repair missing block sha1 " + fmt.Sprint(time.Now().UnixNano()))
	sha256ID := sha256hex(content)
	sha1ID := sha1hex(content)

	upload := webUploadBlock(t, adminClient, sessionID, content)
	if upload.StatusCode != http.StatusOK && upload.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(upload.Body)
		upload.Body.Close()
		t.Fatalf("initial upload status %d: %s", upload.StatusCode, body)
	}
	upload.Body.Close()

	dbSession := shareProjectionDBForTest(t).Session()
	if err := dbSession.Query(`UPDATE blocks SET sha1 = ? WHERE org_id = ? AND block_id = ?`, "", orgID, sha256ID).Exec(); err != nil {
		t.Fatalf("blank block sha1: %v", err)
	}

	manifest := map[string]interface{}{
		"session": sessionID, "parent_dir": "/", "filename": "repair.bin",
		"replace": false, "size": len(content),
		"blocks": []map[string]interface{}{{"sha256": sha256ID, "size": len(content)}},
	}

	firstCommit := webCommit(t, adminClient, repoID, manifest)
	expectStatus(t, firstCommit, http.StatusConflict)
	var out map[string]interface{}
	decodeJSON(t, firstCommit, &out)
	needsUpload, _ := out["needs_upload"].([]interface{})
	if len(needsUpload) != 1 || needsUpload[0] != sha256ID {
		t.Fatalf("needs_upload = %#v, want [%s]", out["needs_upload"], sha256ID)
	}

	reupload := webUploadBlock(t, adminClient, sessionID, content)
	if reupload.StatusCode != http.StatusOK && reupload.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(reupload.Body)
		reupload.Body.Close()
		t.Fatalf("repair upload status %d: %s", reupload.StatusCode, body)
	}
	reupload.Body.Close()

	var repairedSHA1 string
	if err := dbSession.Query(`SELECT sha1 FROM blocks WHERE org_id = ? AND block_id = ?`, orgID, sha256ID).Scan(&repairedSHA1); err != nil {
		t.Fatalf("read repaired block sha1: %v", err)
	}
	if repairedSHA1 != sha1ID {
		t.Fatalf("repaired block sha1 = %q, want %q", repairedSHA1, sha1ID)
	}

	secondCommit := webCommit(t, adminClient, repoID, manifest)
	expectStatus(t, secondCommit, http.StatusOK)
	secondCommit.Body.Close()

	blockIDs, seafileBlockIDs := webReadCommittedFileBlockIDs(t, repoID, "repair.bin")
	if len(blockIDs) != 1 || blockIDs[0] != sha256ID {
		t.Fatalf("block_ids = %#v, want [%s]", blockIDs, sha256ID)
	}
	if len(seafileBlockIDs) != 1 || seafileBlockIDs[0] != sha1ID {
		t.Fatalf("seafile_block_ids_sha1 = %#v, want [%s]", seafileBlockIDs, sha1ID)
	}
}

func TestWebBlockUploadManifestValidation(t *testing.T) {
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-wbu-r6-%d", time.Now().UnixNano()))
	session := webCreateBlockSession(t, adminClient, repoID, "/")
	content := []byte("size sum mismatch")

	// sum(block sizes) != declared size → 400.
	resp := webCommit(t, adminClient, repoID, map[string]interface{}{
		"session": session, "parent_dir": "/", "filename": "bad.txt",
		"replace": false, "size": len(content) + 100,
		"blocks": []map[string]interface{}{{"sha1": sha1hex(content), "sha256": sha256hex(content), "size": len(content)}},
	})
	expectStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()
}

// TestWebBlockUploadManifestRejectsConflictingBlockSizes covers R6: the same
// SHA-256 may repeat in a manifest, but every occurrence must declare the same
// size. A manifest that declares one hash with two different sizes is rejected so
// the last-wins size dedup cannot mask a lie and corrupt the file's size/offsets.
func TestWebBlockUploadManifestRejectsConflictingBlockSizes(t *testing.T) {
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-wbu-dupsize-%d", time.Now().UnixNano()))
	session := webCreateBlockSession(t, adminClient, repoID, "/")
	const blockSize = 8 * 1024 * 1024
	raw := []byte("conflicting size block " + fmt.Sprint(time.Now().UnixNano()))
	hash := sha256hex(raw)
	hash1 := sha1hex(raw)

	// Same hash declared as an 8 MB non-final block AND a 4-byte final block. The
	// SHA-1 is identical (same content) so the rejection is for the conflicting
	// size, not a conflicting hash pairing.
	resp := webCommit(t, adminClient, repoID, map[string]interface{}{
		"session": session, "parent_dir": "/", "filename": "dup.bin",
		"replace": false, "size": blockSize + 4,
		"blocks": []map[string]interface{}{
			{"sha1": hash1, "sha256": hash, "size": blockSize},
			{"sha1": hash1, "sha256": hash, "size": 4},
		},
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
		"blocks": []map[string]interface{}{{"sha1": sha1hex(content), "sha256": sha256hex(content), "size": 20}},
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
	hash1 := sha1hex(content)

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
		"blocks": []map[string]interface{}{{"sha1": hash1, "sha256": hash, "size": len(content)}},
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
		"blocks": []map[string]interface{}{{"sha1": sha1hex(content), "sha256": sha256hex(content), "size": len(content)}},
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
		"blocks": []map[string]interface{}{{"sha1": sha1hex(content), "sha256": sha256hex(content), "size": len(content)}},
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

// TestWebBlockUploadForeignPubRefNotPermanent verifies a block kept alive only
// by a FOREIGN publish-attempt ref ("pub:") — not a committed file ("fs:") and
// not this session — is treated as needs_upload, never published. A pub: ref is
// transient (it vanishes if the foreign attempt loses its CAS), so trusting it
// could leave the new file pointing at a GC-able block.
func TestWebBlockUploadForeignPubRefNotPermanent(t *testing.T) {
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-wbu-pub-%d", time.Now().UnixNano()))
	orgID := resolveOrgID(t, repoID)
	content := []byte("foreign pub ref block " + fmt.Sprint(time.Now().UnixNano()))
	hash := sha256hex(content)
	hash1 := sha1hex(content)

	// Upload under session A → materializes metadata + S3 object + up:<A> ref.
	sessionA := webCreateBlockSession(t, adminClient, repoID, "/")
	resp := webUploadBlock(t, adminClient, sessionA, content)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload status %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Rewrite liveness so the ONLY ref is a foreign pub: attempt (no fs:, no up:B).
	dbSession := shareProjectionDBForTest(t).Session()
	if err := dbSession.Query(`DELETE FROM block_references WHERE org_id = ? AND block_id = ? AND referrer = ?`,
		orgID, hash, "up:"+sessionA).Exec(); err != nil {
		t.Fatalf("remove up ref: %v", err)
	}
	if err := dbSession.Query(`INSERT INTO block_references (org_id, block_id, referrer, library_id, created_at) VALUES (?, ?, ?, ?, ?)`,
		orgID, hash, fmt.Sprintf("pub:foreign-%d", time.Now().UnixNano()), repoID, time.Now()).Exec(); err != nil {
		t.Fatalf("add pub ref: %v", err)
	}

	// Commit under a fresh session B without uploading → must refuse the block.
	sessionB := webCreateBlockSession(t, adminClient, repoID, "/")
	commit := webCommit(t, adminClient, repoID, map[string]interface{}{
		"session": sessionB, "parent_dir": "/", "filename": "pubonly.txt",
		"replace": false, "size": len(content),
		"blocks": []map[string]interface{}{{"sha1": hash1, "sha256": hash, "size": len(content)}},
	})
	expectStatus(t, commit, http.StatusConflict)
	commit.Body.Close()
}

// TestWebBlockUploadSessionStagingSkipsLogicalStorageQuota verifies R5: a block
// staged under a session does NOT pay the user's logical storage quota per block.
// The logical quota is a property of the FINAL file delta, decided once at
// file-from-blocks. Charging it during staging would wrongly reject valid cases
// like a same-size overwrite (delta ≈ 0) at the first new block. We pin the user
// quota to 1 byte and upload a genuinely NEW block (so it hits the store path,
// not the dedup path): under a session it must succeed; the legacy no-session
// path on the same content is still rejected by the per-block admission check.
func TestWebBlockUploadSessionStagingSkipsLogicalStorageQuota(t *testing.T) {
	originalOrg := getAdminOrganizationInfo(t, defaultOrgID)
	originalUser := getAdminUserByEmail(t, defaultUserEmail)
	restoreDefaultOrgAndUserQuotasOnCleanup(t, originalOrg, originalUser)

	updateAdminOrganizationQuotas(t, defaultOrgID, map[string]interface{}{
		"storage_quota": int64(1 << 50),
		"quota_policy":  "hard",
	})
	setDefaultUserQuota(t, int64(1))

	repoID := createTestLibrary(t, userClient, fmt.Sprintf("inttest-wbu-quota-%d", time.Now().UnixNano()))

	// Legacy (no-session) upload of fresh content is still gated by the per-block
	// physical admission check → 403 with the user pinned to 1 byte.
	legacyContent := []byte("legacy block under tiny quota " + fmt.Sprint(time.Now().UnixNano()))
	legacy := webUploadBlockLegacy(t, userClient, legacyContent)
	if legacy.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(legacy.Body)
		legacy.Body.Close()
		t.Fatalf("legacy block upload status = %d, want 403 under 1-byte quota; body=%s", legacy.StatusCode, body)
	}
	legacy.Body.Close()

	// Session staging of fresh content must succeed despite the same 1-byte quota.
	sessionContent := []byte("session block under tiny quota " + fmt.Sprint(time.Now().UnixNano()))
	session := webCreateBlockSession(t, userClient, repoID, "/")
	resp := webUploadBlock(t, userClient, session, sessionContent)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("session block upload status = %d, want 200/201 (staging skips logical quota); body=%s", resp.StatusCode, body)
	}
	resp.Body.Close()
}

// TestWebBlockUploadCommitEnforcesLogicalDelta is the other half of the staging
// invariant: staging skips the logical storage quota, so file-from-blocks MUST be
// the place that enforces it by the FINAL file delta (R5, files.go). It proves
// both directions with the user pinned at exactly their limit (zero headroom):
//   - a same-size overwrite (delta ≈ 0) commits successfully end-to-end, and
//   - a brand-new file (positive delta) is rejected at commit with 403,
//     even though its block staged fine.
//
// If the commit-side check ever regresses, the staging-skip would become a real
// quota bypass — this test is the guard.
func TestWebBlockUploadCommitEnforcesLogicalDelta(t *testing.T) {
	originalOrg := getAdminOrganizationInfo(t, defaultOrgID)
	originalUser := getAdminUserByEmail(t, defaultUserEmail)
	restoreDefaultOrgAndUserQuotasOnCleanup(t, originalOrg, originalUser)

	updateAdminOrganizationQuotas(t, defaultOrgID, map[string]interface{}{
		"storage_quota": int64(1 << 50),
		"quota_policy":  "hard",
	})

	repoID := createTestLibrary(t, userClient, fmt.Sprintf("inttest-wbu-commitquota-%d", time.Now().UnixNano()))

	const fileSize = 200
	initial := []byte(strings.Repeat("a", fileSize))

	baseline := jsonInt64(getAdminUserByEmail(t, defaultUserEmail), "quota_usage")
	setDefaultUserQuota(t, baseline+int64(fileSize)+50)

	resp := uploadFileViaBlocksFlow(t, userClient, repoID, "/", "delta.bin", [][]byte{initial}, false)
	expectStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	afterInitial := waitForUserQuotaUsage(t, baseline+int64(fileSize))

	// Pin quota with NO headroom: used == limit.
	setDefaultUserQuota(t, afterInitial)

	// Same-size overwrite (logical delta ≈ 0) must succeed: the new block stages
	// at-limit (staging skips logical quota) and the commit delta is 0.
	overwrite := []byte(strings.Repeat("b", fileSize))
	ow := uploadFileViaBlocksFlow(t, userClient, repoID, "/", "delta.bin", [][]byte{overwrite}, true)
	if ow.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(ow.Body)
		ow.Body.Close()
		t.Fatalf("same-size overwrite commit status = %d, want 200; body=%s", ow.StatusCode, body)
	}
	ow.Body.Close()

	// A NEW file is a positive delta with no headroom → commit MUST be rejected by
	// file-from-blocks even though its block staged fine.
	newFile := []byte("new file positive delta " + fmt.Sprint(time.Now().UnixNano()))
	nf := uploadFileViaBlocksFlow(t, userClient, repoID, "/", "newdelta.bin", [][]byte{newFile}, false)
	body, _ := io.ReadAll(nf.Body)
	nf.Body.Close()
	if nf.StatusCode != http.StatusForbidden {
		t.Fatalf("new-file commit status = %d, want 403 (logical delta enforced); body=%s", nf.StatusCode, body)
	}
	if !strings.Contains(string(body), "storage quota exceeded") {
		t.Fatalf("new-file commit body = %q, want storage quota exceeded", body)
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
