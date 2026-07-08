//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	streamingpkg "github.com/Sesame-Disk/sesamefs/internal/streaming"
)

// TestServeHistoricFileRawSkipsRepresentationLookupOnAllSHA256Path pins the
// fast-path guard added to ServeHistoricFileRaw (mirroring ServeRawFile and
// DownloadHistoricFile): representation resolution
// (db.ResolveBlockRepresentationID, which reads the LIVE libraries row via
// ReadLiveLibraryState and errors with ErrLibraryDeleted once deleted_at is
// set) must only run when the block list actually contains a legacy SHA-1.
//
// Soft-deleting the library (deleted_at set, row otherwise intact) creates the
// exact divergence needed to observe this from the outside:
//   - the handler's own encrypted-flag lookup a few lines above does a plain
//     `SELECT encrypted FROM libraries ...` that does not look at deleted_at,
//     so it still succeeds;
//   - but ResolveBlockRepresentationID's ReadLiveLibraryState explicitly fails
//     closed on a non-nil deleted_at.
//
// So: an all-SHA-256 revision must still serve 200 (proving the lookup was
// skipped), while the same revision forced into a legacy SHA-1 block list must
// fail (proving the lookup runs, and its error propagates) instead of being
// silently ignored.
func TestServeHistoricFileRawSkipsRepresentationLookupOnAllSHA256Path(t *testing.T) {
	requireCassandra(t)
	database := shareProjectionDBForTest(t)
	session := database.Session()

	name := fmt.Sprintf("inttest-histraw-fastpath-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)
	orgID := resolveOrgID(t, repoID)

	fileName := "fastpath.txt"
	content := fmt.Sprintf("history raw fast-path content %d\n", time.Now().UnixNano())
	uploadURL := getUploadURL(t, adminClient, repoID)
	uploadFileThroughLink(t, adminClient, uploadURL, fileName, "/", content)

	// Resolve the uploaded revision's obj_id (fs_id) via the revisions API,
	// same contract TestHistoryDownloadRoundTrip uses.
	revResp := adminClient.Get(t, fmt.Sprintf("/api2/repo/file_revisions/%s/?p=/%s", repoID, fileName))
	expectStatus(t, revResp, http.StatusOK)
	var revBody map[string]interface{}
	decodeJSON(t, revResp, &revBody)
	data, _ := revBody["data"].([]interface{})
	if len(data) == 0 {
		t.Fatalf("expected at least 1 revision, got 0: %v", revBody)
	}
	entry, _ := data[0].(map[string]interface{})
	objID, _ := entry["rev_file_id"].(string)
	if objID == "" {
		t.Fatalf("revision entry missing rev_file_id: %v", entry)
	}

	// Sanity check: the freshly uploaded revision is genuinely all-SHA-256
	// (block_ids already resolved), not the legacy layout.
	var blockIDs, seafileBlockIDs []string
	if err := session.Query(`SELECT block_ids, seafile_block_ids_sha1 FROM fs_objects WHERE library_id = ? AND fs_id = ?`, repoID, objID).Scan(&blockIDs, &seafileBlockIDs); err != nil {
		t.Fatalf("failed to read block ids for %s: %v", objID, err)
	}
	if len(blockIDs) == 0 {
		t.Fatalf("expected block ids for uploaded revision %q", objID)
	}
	if len(seafileBlockIDs) == 0 {
		t.Fatalf("expected seafile_block_ids_sha1 for uploaded revision %q (needed to force the legacy layout below)", objID)
	}

	// A second, synthetic fs_objects row using the SAME real block content but a
	// legacy SHA-1 block list. This is inserted directly (never uploaded through
	// the app), so it never appears in any commit/dir_entries tree the app's own
	// async publish/reconciliation pipeline scans — unlike the real uploaded
	// revision, which that pipeline can and does rewrite back to canonical
	// SHA-256 moments after a direct Cassandra edit, making in-place corruption
	// of the real row an unreliable way to pin this behavior. The SHA-1 ->
	// SHA-256 mapping itself is legitimate (the app wrote it during the real
	// upload above), so only the representation LOOKUP is exercised here, not
	// block-id resolution correctness.
	legacyObjID := fmt.Sprintf("legacy-fastpath-%d", time.Now().UnixNano())
	if err := session.Query(`
		INSERT INTO fs_objects (library_id, fs_id, obj_type, obj_name, full_path, size_bytes, mtime, block_ids, seafile_block_ids_sha1)
		VALUES (?, ?, 'file', ?, ?, ?, ?, ?, ?)
	`, repoID, legacyObjID, fileName, "/"+fileName, int64(len(content)), time.Now().Unix(), seafileBlockIDs, []string{}).Exec(); err != nil {
		t.Fatalf("failed to insert synthetic legacy fs_object: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Query(`DELETE FROM fs_objects WHERE library_id = ? AND fs_id = ?`, repoID, legacyObjID).Exec()
	})
	if !streamingpkg.ContainsLegacySHA1(seafileBlockIDs) {
		t.Fatalf("seafileBlockIDs %v not recognized as legacy SHA-1; test fixture is broken", seafileBlockIDs)
	}

	// Soft-delete the library: row stays, deleted_at gets set. Restored in
	// cleanup before the normal repo-delete cleanup runs.
	if err := session.Query(`UPDATE libraries SET deleted_at = ? WHERE org_id = ? AND library_id = ?`, time.Now(), orgID, repoID).Exec(); err != nil {
		t.Fatalf("failed to soft-delete library %s: %v", repoID, err)
	}
	t.Cleanup(func() {
		_ = session.Query(`UPDATE libraries SET deleted_at = null WHERE org_id = ? AND library_id = ?`, orgID, repoID).Exec()
		cleanup := adminClient.Delete(t, fmt.Sprintf("/api/v2.1/repos/%s/", repoID))
		cleanup.Body.Close()
	})

	rawURL := func(obj string) string {
		return fmt.Sprintf("%s/repo/%s/history/raw?obj_id=%s&p=/%s", baseURL, repoID, obj, fileName)
	}
	doRaw := func(obj string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest("GET", rawURL(obj), nil)
		req.Header.Set("Authorization", "Token "+adminClient.token)
		resp, err := adminClient.http.Do(req)
		if err != nil {
			t.Fatalf("GET history/raw failed: %v", err)
		}
		return resp
	}

	// 1. All-SHA-256 revision: representation lookup must be skipped, so the
	// soft-delete's ErrLibraryDeleted never surfaces and the request succeeds.
	okResp := doRaw(objID)
	gotBody := responseBody(t, okResp)
	if okResp.StatusCode != http.StatusOK {
		t.Fatalf("all-SHA-256 history/raw on soft-deleted library: status = %d, want 200 (body: %s)", okResp.StatusCode, gotBody)
	}
	if gotBody != content {
		t.Fatalf("all-SHA-256 history/raw content = %q, want %q", gotBody, content)
	}

	// 2. The synthetic legacy-SHA-1 revision: representation lookup must run,
	// hit ErrLibraryDeleted, and fail the request instead of silently resolving
	// against a deleted library.
	failResp := doRaw(legacyObjID)
	failBody := responseBody(t, failResp)
	if failResp.StatusCode == http.StatusOK {
		t.Fatalf("legacy-SHA-1 history/raw on soft-deleted library unexpectedly returned 200 (body: %s); representation lookup should have failed closed", failBody)
	}
	if !strings.Contains(failBody, "failed to read file") {
		t.Fatalf("legacy-SHA-1 history/raw error body = %q, want it to mention the representation-lookup failure", failBody)
	}
}
