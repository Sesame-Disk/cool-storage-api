//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
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

func webCreateBlockSession(t *testing.T, c *testClient, repoID, parentDir string, size int64) string {
	t.Helper()
	resp := c.PostJSON(t, fmt.Sprintf("/api/v2/repos/%s/block-upload-session/", repoID), map[string]interface{}{
		"parent_dir": parentDir,
		"size":       size,
	})
	expectStatus(t, resp, http.StatusOK)
	var out map[string]interface{}
	decodeJSON(t, resp, &out)
	sid, _ := out["session_id"].(string)
	if sid == "" {
		t.Fatalf("empty session_id in response: %v", out)
	}
	t.Cleanup(func() {
		cleanupBlockUploadSessionForTest(t, sid)
	})
	return sid
}

// cleanupUploadedBlockArtifactsForTest tears down everything a real web block upload
// materializes for ONE block, plus any extra referrers the fixture injected by hand.
//
// cleanupBlockUploadSessionForTest only releases the session's own `up:` ref, staged
// rows and caps; the block itself survives it. What survives a session-only teardown —
// and what this helper removes — is: the `blocks` row, the forward SHA-1→SHA-256
// `block_id_mappings` row, the S3 object, and the provisional expiry projection
// (`gc_provisional_block_refs` + its by-day discovery row) that the upload registered.
// Left in place, that set is a block with a permanent-looking reference and a physical
// object that no GC phase will ever reclaim — the "eternal residue" F1 chased.
//
// Every delete is keyed by the fixture's EXACT ids (org/repo/block/referrer). Never
// broaden this to a range or full-partition delete: the keyspace is shared with every
// other integration test (invariants #5/#6).
//
// See ISSUE-GC-TEST-RESIDUE-01 (branch 1A) and docs/GC-DELETE-CLEANUP-INVESTIGATION.md F1.
func cleanupUploadedBlockArtifactsForTest(t *testing.T, orgID, repoID, blockID, externalSHA1 string, referrers ...string) {
	t.Helper()

	// Built here rather than inside t.Cleanup so an unreachable MinIO degrades to a
	// logged skip of the S3 step only. newVerificationBlockStore would t.Skipf, which
	// would silently turn this test into a no-op whenever the object store is not
	// reachable from the test process.
	blockStore := blockStoreForCleanupOrNil(t, orgID)

	t.Cleanup(func() {
		database := shareProjectionDBForTest(t)

		for _, referrer := range referrers {
			if err := database.Session().Query(
				`DELETE FROM block_references WHERE org_id = ? AND block_id = ? AND referrer = ?`,
				orgID, blockID, referrer).Exec(); err != nil {
				t.Errorf("cleanup block reference %s/%s/%s: %v", orgID, blockID, referrer, err)
			}
			// The upload's expiry projection is NOT removed by deleting the ref row:
			// gc_provisional_block_refs and its by-day discovery row are written
			// separately by the upload path. Use the production helper so this cannot
			// drift from the write side. Harmless for referrers that never had one.
			if err := database.DeleteProvisionalBlockReferenceExpiry(orgID, blockID, referrer, time.Time{}); err != nil {
				t.Errorf("cleanup provisional expiry %s/%s/%s: %v", orgID, blockID, referrer, err)
			}
		}

		if err := database.Session().Query(
			`DELETE FROM block_id_mappings WHERE org_id = ? AND representation_id = ? AND external_id = ?`,
			orgID, dbpkg.PlainBlockRepresentationID, externalSHA1).Exec(); err != nil {
			t.Errorf("cleanup block mapping %s/%s: %v", orgID, externalSHA1, err)
		}

		if err := database.Session().Query(
			`DELETE FROM blocks WHERE org_id = ? AND block_id = ?`, orgID, blockID).Exec(); err != nil {
			t.Errorf("cleanup blocks row %s/%s: %v", orgID, blockID, err)
		}

		if blockStore != nil {
			if err := blockStore.DeleteBlock(context.Background(), blockID); err != nil {
				t.Errorf("cleanup S3 object for block %s: %v", blockID, err)
			}
		}

		// Assert the teardown actually landed: a surviving referrer here is exactly the
		// F1 leak this branch exists to close, and a silent cleanup failure would let it
		// regress unnoticed.
		for _, referrer := range referrers {
			var found string
			err := database.Session().Query(
				`SELECT referrer FROM block_references WHERE org_id = ? AND block_id = ? AND referrer = ?`,
				orgID, blockID, referrer).Scan(&found)
			if err == nil {
				t.Errorf("block reference %s/%s/%s survived teardown; it pins the block and its S3 object forever (ISSUE-GC-TEST-RESIDUE-01)", orgID, blockID, referrer)
			} else if !errors.Is(err, gocql.ErrNotFound) {
				t.Errorf("verify block reference %s/%s/%s removed: %v", orgID, blockID, referrer, err)
			}
		}
	})
}

// blockStoreForCleanupOrNil returns a BlockStore for teardown, or nil (with a log) when
// the object store is unreachable. Unlike newVerificationBlockStore it never skips: S3
// reachability is incidental to callers that only need to delete their own object, and
// skipping would cost real coverage.
func blockStoreForCleanupOrNil(t *testing.T, orgID string) *storage.BlockStore {
	t.Helper()
	ctx := context.Background()
	s3Store, err := storage.NewS3Store(ctx, storage.S3Config{
		Endpoint:        envOrDefault("S3_ENDPOINT", "http://minio:9000"),
		Bucket:          envOrDefault("S3_BUCKET", "sesamefs-blocks"),
		Region:          envOrDefault("S3_REGION", "us-east-1"),
		AccessKeyID:     envOrDefault("S3_ACCESS_KEY_ID", "minioadmin"),
		SecretAccessKey: envOrDefault("S3_SECRET_ACCESS_KEY", "minioadmin"),
		UsePathStyle:    true,
	})
	if err != nil {
		t.Logf("cleanup: S3 store unavailable (%v); block objects will be left behind", err)
		return nil
	}
	if err := s3Store.HeadBucket(ctx); err != nil {
		t.Logf("cleanup: S3 bucket unreachable (%v); block objects will be left behind", err)
		return nil
	}
	blockStore, err := storage.NewOrgBlockStore(s3Store, "blocks/", orgID)
	if err != nil {
		t.Errorf("cleanup: invalid org id %q: %v", orgID, err)
		return nil
	}
	return blockStore
}

// releaseStagedBlockForTest finishes tearing down one block staged by an upload session
// whose `up:` reference the caller has just deleted.
//
// Deleting that reference with raw CQL removes the block's last referrer without going
// through the production release path, so nothing calls EnsureBlockGCCandidate: the block
// becomes zero-ref but UNDISCOVERABLE. scanOrphanedBlocks only walks candidates that
// already exist, so the only thing that eventually rescues it is Phase 0 firing on the
// provisional expiry projection — two days later (P4 is the same shape for `pub:`). Until
// then every suite run leaves its uploaded blocks and S3 objects lying around, which is
// precisely the drift the delete audit had to untangle.
//
// So: drop the expiry projection the upload registered, and if the block has no referrers
// left, remove the block row, its forward mapping and its S3 object. A block that still
// has a referrer (a committed `fs:`) belongs to a library — leave it alone and let that
// library's cascade reclaim it through the real GC path.
//
// See ISSUE-GC-TEST-RESIDUE-01 (branch 1B).
func releaseStagedBlockForTest(t *testing.T, database *dbpkg.DB, orgID, blockID, referrer string, blockStore *storage.BlockStore) {
	t.Helper()

	if err := database.DeleteProvisionalBlockReferenceExpiry(orgID, blockID, referrer, time.Time{}); err != nil {
		t.Errorf("cleanup staged block %s/%s: delete provisional expiry: %v", orgID, blockID, err)
		return
	}

	var survivingReferrer string
	err := database.Session().Query(
		`SELECT referrer FROM block_references WHERE org_id = ? AND block_id = ? LIMIT 1`,
		orgID, blockID).Scan(&survivingReferrer)
	if err == nil {
		return // still referenced (e.g. committed fs:) — its library's cascade owns it
	}
	if !errors.Is(err, gocql.ErrNotFound) {
		t.Errorf("cleanup staged block %s/%s: check remaining referrers: %v", orgID, blockID, err)
		return
	}

	// Zero-ref: read sha1 before deleting the row — it is the mapping's external id.
	//
	// A missing row means STOP, not "delete the object anyway". The `blocks` row is the
	// fixture's evidence that it materialized this org-scoped object, and it carries the
	// storage_class that says which bucket the object lives in. Without it we would be
	// deleting a hash we cannot prove this fixture created, from a bucket we are guessing.
	var externalSHA1 string
	switch err := database.Session().Query(
		`SELECT sha1 FROM blocks WHERE org_id = ? AND block_id = ?`, orgID, blockID).Scan(&externalSHA1); {
	case errors.Is(err, gocql.ErrNotFound):
		return // already reclaimed by GC, or never materialized here
	case err != nil:
		t.Errorf("cleanup staged block %s/%s: read sha1: %v", orgID, blockID, err)
		return
	}

	if externalSHA1 != "" {
		if err := database.Session().Query(
			`DELETE FROM block_id_mappings WHERE org_id = ? AND representation_id = ? AND external_id = ?`,
			orgID, dbpkg.PlainBlockRepresentationID, externalSHA1).Exec(); err != nil {
			t.Errorf("cleanup staged block %s/%s: delete mapping %s: %v", orgID, blockID, externalSHA1, err)
		}
	}
	if err := database.Session().Query(
		`DELETE FROM blocks WHERE org_id = ? AND block_id = ?`, orgID, blockID).Exec(); err != nil {
		t.Errorf("cleanup staged block %s/%s: delete blocks row: %v", orgID, blockID, err)
	}
	if blockStore != nil {
		if err := blockStore.DeleteBlock(context.Background(), blockID); err != nil {
			t.Errorf("cleanup staged block %s/%s: delete S3 object: %v", orgID, blockID, err)
		}
	}
}

func cleanupBlockUploadSessionForTest(t *testing.T, sessionID string) {
	t.Helper()

	database := shareProjectionDBForTest(t)
	session, ok, err := database.GetBlockUploadSession(sessionID)
	if err != nil {
		t.Errorf("cleanup block upload session %s: read session: %v", sessionID, err)
		return
	}
	if !ok {
		return
	}
	blockStore := blockStoreForCleanupOrNil(t, session.OrgID)

	if err := database.CleanupCommittedBlockUploadSessionCaps(session); err != nil {
		t.Errorf("cleanup block upload session %s: release slot: %v", sessionID, err)
	}

	referrer := dbpkg.BlockReferrerForUpload(sessionID)
	for bucket := 0; bucket < dbpkg.BlockUploadStagedBlockBuckets; bucket++ {
		iter := database.Session().Query(`
			SELECT block_id FROM block_upload_session_staged_blocks
			WHERE session_id = ? AND bucket = ?
		`, sessionID, bucket).Iter()
		var blockID string
		var blockIDs []string
		for iter.Scan(&blockID) {
			blockIDs = append(blockIDs, blockID)
		}
		if err := iter.Close(); err != nil {
			t.Errorf("cleanup block upload session %s: list staged blocks for bucket %d: %v", sessionID, bucket, err)
			continue
		}
		for _, blockID := range blockIDs {
			if err := database.Session().Query(`
				DELETE FROM block_references WHERE org_id = ? AND block_id = ? AND referrer = ?
			`, session.OrgID, blockID, referrer).Exec(); err != nil {
				t.Errorf("cleanup block upload session %s: delete provisional ref for block %s: %v", sessionID, blockID, err)
			}
			releaseStagedBlockForTest(t, database, session.OrgID, blockID, referrer, blockStore)
		}
		if err := database.Session().Query(`
			DELETE FROM block_upload_session_staged_blocks WHERE session_id = ? AND bucket = ?
		`, sessionID, bucket).Exec(); err != nil {
			t.Errorf("cleanup block upload session %s: delete staged bucket %d: %v", sessionID, bucket, err)
		}
	}

	if err := database.Session().Query(`
		DELETE FROM block_upload_sessions WHERE session_id = ?
	`, sessionID).Exec(); err != nil {
		t.Errorf("cleanup block upload session %s: delete session row: %v", sessionID, err)
	}
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
//
// Teardown is registered here rather than left to callers because this path writes an
// object with NO `blocks` row and no reference: it is a pure S3 orphan by construction.
// Nothing reclaims it — GC discovers blocks through candidates, and its S3-orphan recovery
// only replays its own in-flight deletes from `gc_s3_orphans`. So an untorn-down legacy
// upload lives in the bucket forever (ISSUE-GC-TEST-RESIDUE-01 / branch 1B).
//
// The delete is reported, not swallowed: S3 DELETE is idempotent, so callers that assert a
// rejection (e.g. quota 403) and never created the object still succeed here. That means a
// real error is a real teardown failure — silently dropping it would leave the exact eternal
// S3 object this teardown exists to remove, with the test still green.
func webUploadBlockLegacy(t *testing.T, c *testClient, orgID string, data []byte) *http.Response {
	t.Helper()

	if blockStore := blockStoreForCleanupOrNil(t, orgID); blockStore != nil {
		hash := sha256hex(data)
		t.Cleanup(func() {
			if err := blockStore.DeleteBlock(context.Background(), hash); err != nil {
				t.Errorf("cleanup legacy S3 block %s: %v", hash, err)
			}
		})
	}
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
	resp := webCheckBlocksResponse(t, c, session, hashes)
	defer resp.Body.Close()
	expectStatus(t, resp, http.StatusOK)
	var out struct {
		Existing []string `json:"existing"`
		Missing  []string `json:"missing"`
	}
	decodeJSON(t, resp, &out)
	return out.Existing, out.Missing
}

func webCheckBlocksResponse(t *testing.T, c *testClient, session string, hashes []string) *http.Response {
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
	return resp
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

func webFileFSID(t *testing.T, externalBlockIDs []string, size int) string {
	t.Helper()
	encoded, err := json.Marshal(map[string]interface{}{
		"version":   1,
		"type":      1,
		"block_ids": externalBlockIDs,
		"size":      size,
	})
	if err != nil {
		t.Fatalf("marshal file fs object: %v", err)
	}
	return sha1hex(encoded)
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
	session := webCreateBlockSession(t, c, repoID, parentDir, int64(totalSize(blocks)))
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

func TestWebBlockUploadDeduplicatesAcrossLibrariesInSameOrg(t *testing.T) {
	content := []byte(fmt.Sprintf("org-scoped intra-org dedup %d", time.Now().UnixNano()))
	blockID := sha256hex(content)
	externalSHA1 := sha1hex(content)
	fileFSID := webFileFSID(t, []string{externalSHA1}, len(content))
	repoA := createDisposableTestLibrary(t, adminClient, fmt.Sprintf("inttest-org-dedup-a-%d", time.Now().UnixNano()))
	repoB := createDisposableTestLibrary(t, adminClient, fmt.Sprintf("inttest-org-dedup-b-%d", time.Now().UnixNano()))
	blockStore := blockStoreForCleanupOrNil(t, defaultOrgID)
	if blockStore == nil {
		t.Skip("MinIO unavailable for physical org-scoped key verification")
	}
	t.Cleanup(func() {
		session := shareProjectionDBForTest(t).Session()
		removeTestLibraryFully(t, adminClient, session, defaultOrgID, repoA)
		removeTestLibraryFully(t, adminClient, session, defaultOrgID, repoB)
	})
	cleanupUploadedBlockArtifactsForTest(t, defaultOrgID, repoA, blockID, externalSHA1,
		dbpkg.BlockReferrerForFSObject(repoA, fileFSID),
		dbpkg.BlockReferrerForFSObject(repoB, fileFSID))

	for _, upload := range []struct {
		repoID   string
		filename string
	}{
		{repoID: repoA, filename: "same-a.bin"},
		{repoID: repoB, filename: "same-b.bin"},
	} {
		resp := uploadFileViaBlocksFlow(t, adminClient, upload.repoID, "/", upload.filename, [][]byte{content}, false)
		expectStatus(t, resp, http.StatusOK)
		resp.Body.Close()
		if got := downloadRepoFile(t, adminClient, upload.repoID, "/"+upload.filename); !bytes.Equal(got, content) {
			t.Fatalf("download %s differed byte-for-byte: got %q want %q", upload.filename, got, content)
		}
	}

	if exists, err := blockStore.BlockExists(context.Background(), blockID); err != nil {
		t.Fatalf("check same-org physical block: %v", err)
	} else if !exists {
		t.Fatalf("same-org deduplicated block %s does not exist at %s", blockID, blockStore.StorageKeyForHash(blockID))
	}
}

func TestWebBlockUploadIdenticalBytesUseDistinctOrgKeys(t *testing.T) {
	content := []byte(fmt.Sprintf("org-scoped cross-org isolation %d", time.Now().UnixNano()))
	blockID := sha256hex(content)
	externalSHA1 := sha1hex(content)
	fileFSID := webFileFSID(t, []string{externalSHA1}, len(content))
	defaultRepo := createDisposableTestLibrary(t, adminClient, fmt.Sprintf("inttest-org-isolation-default-%d", time.Now().UnixNano()))
	platformRepo := createDisposableTestLibrary(t, superadminClient, fmt.Sprintf("inttest-org-isolation-platform-%d", time.Now().UnixNano()))
	defaultStore := blockStoreForCleanupOrNil(t, defaultOrgID)
	platformStore := blockStoreForCleanupOrNil(t, platformOrgID)
	if defaultStore == nil || platformStore == nil {
		t.Skip("MinIO unavailable for physical org-scoped key verification")
	}
	t.Cleanup(func() {
		session := shareProjectionDBForTest(t).Session()
		removeTestLibraryFully(t, adminClient, session, defaultOrgID, defaultRepo)
		removeTestLibraryFully(t, superadminClient, session, platformOrgID, platformRepo)
	})
	cleanupUploadedBlockArtifactsForTest(t, defaultOrgID, defaultRepo, blockID, externalSHA1,
		dbpkg.BlockReferrerForFSObject(defaultRepo, fileFSID))
	cleanupUploadedBlockArtifactsForTest(t, platformOrgID, platformRepo, blockID, externalSHA1,
		dbpkg.BlockReferrerForFSObject(platformRepo, fileFSID))

	for _, upload := range []struct {
		client   *testClient
		repoID   string
		filename string
	}{
		{client: adminClient, repoID: defaultRepo, filename: "default.bin"},
		{client: superadminClient, repoID: platformRepo, filename: "platform.bin"},
	} {
		resp := uploadFileViaBlocksFlow(t, upload.client, upload.repoID, "/", upload.filename, [][]byte{content}, false)
		expectStatus(t, resp, http.StatusOK)
		resp.Body.Close()
		if got := downloadRepoFile(t, upload.client, upload.repoID, "/"+upload.filename); !bytes.Equal(got, content) {
			t.Fatalf("download %s differed byte-for-byte: got %q want %q", upload.filename, got, content)
		}
	}

	defaultKey := defaultStore.StorageKeyForHash(blockID)
	platformKey := platformStore.StorageKeyForHash(blockID)
	if defaultKey == platformKey {
		t.Fatalf("identical bytes in distinct orgs resolved to the same key %q", defaultKey)
	}
	for orgID, blockStore := range map[string]*storage.BlockStore{
		defaultOrgID:  defaultStore,
		platformOrgID: platformStore,
	} {
		if exists, err := blockStore.BlockExists(context.Background(), blockID); err != nil {
			t.Fatalf("check physical block for org %s: %v", orgID, err)
		} else if !exists {
			t.Fatalf("physical block for org %s does not exist at %s", orgID, blockStore.StorageKeyForHash(blockID))
		}
	}
}

// TestWebBlockUploadCommittedSessionIsTerminal guards that a committed session can
// no longer be used for /blocks/check or /blocks/upload (item 1): otherwise a client
// could commit once, recover its per-user session slot, and keep staging blocks under
// the committed session for the whole 48h TTL — defeating
// max_uncommitted_block_sessions_per_user and leaking never-committable provisional
// refs.
func TestWebBlockUploadCommittedSessionIsTerminal(t *testing.T) {
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-wbu-terminal-%d", time.Now().UnixNano()))
	content := []byte("committed-session terminal " + fmt.Sprint(time.Now().UnixNano()))
	blocks := [][]byte{content}

	session := webCreateBlockSession(t, adminClient, repoID, "/", int64(totalSize(blocks)))
	upResp := webUploadBlock(t, adminClient, session, content)
	if upResp.StatusCode != http.StatusOK && upResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(upResp.Body)
		upResp.Body.Close()
		t.Fatalf("upload status %d, want 200/201; body=%s", upResp.StatusCode, body)
	}
	upResp.Body.Close()

	commitResp := webCommit(t, adminClient, repoID, map[string]interface{}{
		"session":    session,
		"parent_dir": "/",
		"filename":   "terminal.txt",
		"replace":    false,
		"size":       totalSize(blocks),
		"blocks":     blocksManifest(blocks),
	})
	expectStatus(t, commitResp, http.StatusOK)
	commitResp.Body.Close()

	// A further upload under the committed session must be rejected (409), not accepted.
	again := webUploadBlock(t, adminClient, session, []byte("extra staged block "+fmt.Sprint(time.Now().UnixNano())))
	expectStatus(t, again, http.StatusConflict)
	again.Body.Close()

	// /blocks/check under the committed session is likewise terminal.
	checkResp := webCheckBlocksResponse(t, adminClient, session, []string{sha256hex(content)})
	expectStatus(t, checkResp, http.StatusConflict)
	checkResp.Body.Close()
}

// ── tests ────────────────────────────────────────────────────────────────────

func TestWebBlockUploadSessionRequiresDeclaredSizeWhenCapEnabled(t *testing.T) {
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-wbu-size-required-%d", time.Now().UnixNano()))
	resp := adminClient.PostJSON(t, fmt.Sprintf("/api/v2/repos/%s/block-upload-session/", repoID), map[string]interface{}{
		"parent_dir": "/",
	})
	expectStatus(t, resp, http.StatusBadRequest)
	var out map[string]interface{}
	decodeJSON(t, resp, &out)
	if out["error"] != "size is required" {
		t.Fatalf("error = %v, want size is required", out["error"])
	}
}

func TestWebBlockUploadSessionRejectsExplicitZeroSize(t *testing.T) {
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-wbu-zero-size-%d", time.Now().UnixNano()))
	resp := adminClient.PostJSON(t, fmt.Sprintf("/api/v2/repos/%s/block-upload-session/", repoID), map[string]interface{}{
		"parent_dir": "/",
		"size":       0,
	})
	expectStatus(t, resp, http.StatusBadRequest)
	var out map[string]interface{}
	decodeJSON(t, resp, &out)
	if out["error"] != "invalid size (the block upload flow does not support empty files)" {
		t.Fatalf("error = %v, want invalid empty-file size rejection", out["error"])
	}
}

func TestWebBlockUploadCommitRejectsSubdeclaredSessionSize(t *testing.T) {
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-wbu-underdeclared-%d", time.Now().UnixNano()))
	content := []byte("underdeclared session size " + fmt.Sprint(time.Now().UnixNano()))
	session := webCreateBlockSession(t, adminClient, repoID, "/", int64(len(content)-1))

	resp := webUploadBlock(t, adminClient, session, content)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("upload status %d: %s", resp.StatusCode, body)
	}
	resp.Body.Close()

	commit := webCommit(t, adminClient, repoID, map[string]interface{}{
		"session": session, "parent_dir": "/", "filename": "underdeclared.txt",
		"replace": false, "size": len(content),
		"blocks": []map[string]interface{}{{"sha256": sha256hex(content), "size": len(content)}},
	})
	expectStatus(t, commit, http.StatusConflict)
	var out map[string]interface{}
	decodeJSON(t, commit, &out)
	if out["error"] != "manifest size does not match the size declared at session creation" {
		t.Fatalf("error = %v, want manifest size mismatch", out["error"])
	}
}

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
	session := webCreateBlockSession(t, adminClient, repoID, "/", int64(len(content)))
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
		if err := session.Query(`SELECT internal_id FROM block_id_mappings WHERE org_id = ? AND representation_id = ? AND external_id = ?`, orgID, dbpkg.PlainBlockRepresentationID, ext).Scan(&mappedInternal); err != nil {
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
	content := []byte("server-derived sha1 " + fmt.Sprint(time.Now().UnixNano()))
	session := webCreateBlockSession(t, adminClient, repoID, "/", int64(len(content)))
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
	content := []byte("forward-mapping block " + fmt.Sprint(time.Now().UnixNano()))
	session := webCreateBlockSession(t, adminClient, repoID, "/", int64(len(content)))

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
	content := []byte("replay ignores sha1 " + fmt.Sprint(time.Now().UnixNano()))
	session := webCreateBlockSession(t, adminClient, repoID, "/", int64(len(content)))

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
		content := []byte("never uploaded " + fmt.Sprint(time.Now().UnixNano()))
		session := webCreateBlockSession(t, adminClient, repoID, "/", int64(len(content)))
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
		legacy := webUploadBlockLegacy(t, adminClient, defaultOrgID, content)
		if legacy.StatusCode != http.StatusOK && legacy.StatusCode != http.StatusCreated {
			body, _ := io.ReadAll(legacy.Body)
			legacy.Body.Close()
			t.Fatalf("legacy block upload status %d: %s", legacy.StatusCode, body)
		}
		legacy.Body.Close()

		session := webCreateBlockSession(t, adminClient, repoID, "/", int64(len(content)))
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
	content := []byte("repair missing block sha1 " + fmt.Sprint(time.Now().UnixNano()))
	sessionID := webCreateBlockSession(t, adminClient, repoID, "/", int64(len(content)))
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
	// Put the sha1 back before teardown reads it. On the happy path the re-upload below
	// repairs it (that is what this test asserts), but if the test fails earlier the row
	// stays blank — and blocks.sha1 is the only way to name this block's forward
	// block_id_mappings row, since the reverse index was dropped in migration 006. The
	// session teardown would then skip the mapping while still deleting the block and its
	// S3 object, stranding a mapping that points at nothing. Registered AFTER the session
	// cleanup so LIFO runs this first. IF EXISTS keeps it from resurrecting a blocks row
	// that GC already reclaimed (a bare UPDATE is an upsert in Cassandra).
	t.Cleanup(func() {
		if err := dbSession.Query(
			`UPDATE blocks SET sha1 = ? WHERE org_id = ? AND block_id = ? IF EXISTS`,
			sha1ID, orgID, sha256ID).Exec(); err != nil {
			t.Errorf("restore block sha1 for %s/%s before teardown: %v", orgID, sha256ID, err)
		}
	})

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
	content := []byte("size sum mismatch")
	session := webCreateBlockSession(t, adminClient, repoID, "/", int64(len(content)))

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
	const blockSize = 8 * 1024 * 1024
	raw := []byte("conflicting size block " + fmt.Sprint(time.Now().UnixNano()))
	session := webCreateBlockSession(t, adminClient, repoID, "/", int64(blockSize+4))
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
	content := []byte("ten bytes!") // 10 bytes
	session := webCreateBlockSession(t, adminClient, repoID, "/", 20)
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
	content := []byte("gc fenced block " + fmt.Sprint(time.Now().UnixNano()))
	session := webCreateBlockSession(t, adminClient, repoID, "/", int64(len(content)))
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
	session := webCreateBlockSession(t, adminClient, repoID, "/", int64(len(content)))
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
	session := webCreateBlockSession(t, adminClient, repoID, "/", int64(len(content)))
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
	sessionA := webCreateBlockSession(t, adminClient, repoID, "/", int64(len(content)))
	uploadReferrer := dbpkg.BlockReferrerForUpload(sessionA)
	resp := webUploadBlock(t, adminClient, sessionA, content)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload status %d", resp.StatusCode)
	}
	resp.Body.Close()

	// The fake ref is inserted with NO TTL, unlike a production pub: ref (35d). Capture
	// the referrer so teardown can delete this exact row: left behind, it is a permanent
	// reference that pins the block + mapping + S3 object forever and shows up in every
	// later audit as unexplained residue (ISSUE-GC-TEST-RESIDUE-01 / F1 / branch 1A).
	pubReferrer := fmt.Sprintf("pub:foreign-%d", time.Now().UnixNano())
	cleanupUploadedBlockArtifactsForTest(t, orgID, repoID, hash, hash1, pubReferrer, uploadReferrer)

	// Rewrite liveness so the ONLY ref is a foreign pub: attempt (no fs:, no up:B).
	dbSession := shareProjectionDBForTest(t).Session()
	if err := dbSession.Query(`DELETE FROM block_references WHERE org_id = ? AND block_id = ? AND referrer = ?`,
		orgID, hash, uploadReferrer).Exec(); err != nil {
		t.Fatalf("remove up ref: %v", err)
	}
	if err := dbSession.Query(`INSERT INTO block_references (org_id, block_id, referrer, library_id, created_at) VALUES (?, ?, ?, ?, ?)`,
		orgID, hash, pubReferrer, repoID, time.Now()).Exec(); err != nil {
		t.Fatalf("add pub ref: %v", err)
	}

	// Commit under a fresh session B without uploading → must refuse the block.
	sessionB := webCreateBlockSession(t, adminClient, repoID, "/", int64(len(content)))
	commit := webCommit(t, adminClient, repoID, map[string]interface{}{
		"session": sessionB, "parent_dir": "/", "filename": "pubonly.txt",
		"replace": false, "size": len(content),
		"blocks": []map[string]interface{}{{"sha1": hash1, "sha256": hash, "size": len(content)}},
	})
	expectStatus(t, commit, http.StatusConflict)
	commit.Body.Close()
	// Sessions A and B need no teardown here: webCreateBlockSession already registers
	// cleanupBlockUploadSessionForTest for every session it hands out.
}

func TestWebBlockUploadSessionRoutesRejectRevokedSharedRepoPermission(t *testing.T) {
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-wbu-revoke-%d", time.Now().UnixNano()))

	shareResp := adminClient.PutJSON(t, fmt.Sprintf("/api2/repos/%s/dir/shared_items/?p=/", repoID), map[string]interface{}{
		"share_type": "user",
		"username":   []string{defaultUserEmail},
		"permission": "rw",
	})
	expectStatus(t, shareResp, http.StatusOK)
	shareResp.Body.Close()
	t.Cleanup(func() {
		cleanup := adminClient.Delete(t,
			fmt.Sprintf("/api2/repos/%s/dir/shared_items/?p=/&share_type=user&username=%s", repoID, url.QueryEscape(defaultUserEmail)),
		)
		defer cleanup.Body.Close()
		if cleanup.StatusCode != http.StatusOK && cleanup.StatusCode != http.StatusNotFound {
			body := responseBody(t, cleanup)
			t.Errorf("cleanup delete share status=%d body=%s", cleanup.StatusCode, body)
			return
		}
	})

	waitForIntegrationCondition(t, "shared repo becomes writable for user before session mint", func() bool {
		resp := userClient.Get(t, fmt.Sprintf("/api/v2.1/repos/%s/", repoID))
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	})

	sessionID := webCreateBlockSession(t, userClient, repoID, "/", 1)

	deleteResp := adminClient.Delete(t,
		fmt.Sprintf("/api2/repos/%s/dir/shared_items/?p=/&share_type=user&username=%s", repoID, url.QueryEscape(defaultUserEmail)),
	)
	expectStatus(t, deleteResp, http.StatusOK)
	deleteResp.Body.Close()

	waitForIntegrationCondition(t, "shared repo permission revocation reaches user-facing reads", func() bool {
		resp := userClient.Get(t, fmt.Sprintf("/api/v2.1/repos/%s/", repoID))
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound
	})

	// This covers the first post-revocation use of the session routes, before any
	// successful route call could warm the 2-minute per-session permission cache.
	checkResp := webCheckBlocksResponse(t, userClient, sessionID, []string{strings.Repeat("a", 64)})
	defer checkResp.Body.Close()
	if checkResp.StatusCode != http.StatusForbidden {
		body := responseBody(t, checkResp)
		t.Fatalf("check after permission revocation status=%d, want 403; body=%s", checkResp.StatusCode, body)
	}
	var checkBody map[string]interface{}
	decodeJSON(t, checkResp, &checkBody)
	if checkBody["error"] != "upload is no longer allowed for this session" {
		t.Fatalf("check after permission revocation error=%v, want upload is no longer allowed for this session", checkBody["error"])
	}

	uploadResp := webUploadBlock(t, userClient, sessionID, []byte("revoked session upload "+fmt.Sprint(time.Now().UnixNano())))
	defer uploadResp.Body.Close()
	if uploadResp.StatusCode != http.StatusForbidden {
		body := responseBody(t, uploadResp)
		t.Fatalf("upload after permission revocation status=%d, want 403; body=%s", uploadResp.StatusCode, body)
	}
	var uploadBody map[string]interface{}
	decodeJSON(t, uploadResp, &uploadBody)
	if uploadBody["error"] != "upload is no longer allowed for this session" {
		t.Fatalf("upload after permission revocation error=%v, want upload is no longer allowed for this session", uploadBody["error"])
	}
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
	legacy := webUploadBlockLegacy(t, userClient, defaultOrgID, legacyContent)
	if legacy.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(legacy.Body)
		legacy.Body.Close()
		t.Fatalf("legacy block upload status = %d, want 403 under 1-byte quota; body=%s", legacy.StatusCode, body)
	}
	legacy.Body.Close()

	// Session staging of fresh content must succeed despite the same 1-byte quota.
	sessionContent := []byte("session block under tiny quota " + fmt.Sprint(time.Now().UnixNano()))
	session := webCreateBlockSession(t, userClient, repoID, "/", int64(len(sessionContent)))
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
