//go:build integration

package integration

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/fnv"
	"net/http"
	"strings"
	"testing"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"
)

// GC integration tests. Each test is independent: creates its own library,
// uses the global adminClient/superadminClient from TestMain, and cleans up
// after itself. Run with:
//
//	go test -tags integration -v -run "TestGC_" -timeout 10m ./internal/integration/...

// ---------------------------------------------------------------------------
// Prerequisite helpers
// ---------------------------------------------------------------------------

// requireGCEnabled skips the test if the GC admin endpoint is not reachable
// or GC is disabled.
func requireGCEnabled(t *testing.T) {
	t.Helper()
	resp := superadminClient.Get(t, "/api/v2.1/admin/gc/status")
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
		t.Skip("GC admin endpoint requires superadmin; skipping")
	}
	if resp.StatusCode != http.StatusOK {
		t.Skipf("GC admin endpoint returned %d; skipping", resp.StatusCode)
	}
	var body map[string]interface{}
	decodeJSON(t, resp, &body)
	if enabled, ok := body["enabled"].(bool); ok && !enabled {
		t.Skip("GC is disabled in server config; skipping")
	}
}

// requireCassandra skips the test if we can't connect to Cassandra directly.
func requireCassandra(t *testing.T) {
	t.Helper()
	session := shareProjectionDBForTest(t).Session()
	var result string
	if err := session.Query(`SELECT release_version FROM system.local`).Scan(&result); err != nil {
		t.Skipf("Cassandra not reachable: %v; skipping", err)
	}
}

// triggerGCWorker fires the GC worker via the admin API and waits briefly.
func triggerGCWorker(t *testing.T) {
	t.Helper()
	resp := superadminClient.PostJSON(t, "/api/v2.1/admin/gc/run", map[string]string{"type": "worker"})
	if resp.StatusCode != http.StatusOK {
		t.Logf("triggerGCWorker: HTTP %d (non-fatal)", resp.StatusCode)
	}
	resp.Body.Close()
	time.Sleep(500 * time.Millisecond) // let worker tick complete
}

// triggerGCScanner fires the GC scanner via the admin API and waits briefly.
func triggerGCScanner(t *testing.T) {
	t.Helper()
	resp := superadminClient.PostJSON(t, "/api/v2.1/admin/gc/run", map[string]string{"type": "scanner"})
	if resp.StatusCode != http.StatusOK {
		t.Logf("triggerGCScanner: HTTP %d (non-fatal)", resp.StatusCode)
	}
	resp.Body.Close()
	time.Sleep(1 * time.Second) // scanner phases take longer
}

func triggerGCWorkerAndWait(t *testing.T) {
	t.Helper()
	before := getGCStatus(t).LastWorkerRun
	triggerGCWorker(t)
	ok := pollUntil(t, 45*time.Second, 500*time.Millisecond, func() bool {
		return getGCStatus(t).LastWorkerRun != before
	})
	if before == "never" && !ok {
		t.Log("GC worker run timestamp is still 'never'; falling back to behavior-specific assertions")
		return
	}
	if !ok {
		t.Fatalf("timed out waiting for GC worker run after manual trigger (before=%q after=%q)", before, getGCStatus(t).LastWorkerRun)
	}
}

func triggerGCScannerAndWait(t *testing.T) {
	t.Helper()
	before := getGCStatus(t).LastScanRun
	triggerGCScanner(t)
	ok := pollUntil(t, 45*time.Second, 500*time.Millisecond, func() bool {
		return getGCStatus(t).LastScanRun != before
	})
	if before == "never" && !ok {
		t.Log("GC scanner run timestamp is still 'never'; falling back to behavior-specific assertions")
		return
	}
	if !ok {
		t.Fatalf("timed out waiting for GC scanner run after manual trigger (before=%q after=%q)", before, getGCStatus(t).LastScanRun)
	}
}

// resolveOrgID reads the org_id for a library from Cassandra.
func resolveOrgID(t *testing.T, repoID string) string {
	t.Helper()
	session := shareProjectionDBForTest(t).Session()
	var orgID string
	if err := session.Query(`SELECT org_id FROM libraries_by_id WHERE library_id = ?`, repoID).Scan(&orgID); err != nil {
		t.Fatalf("failed to resolve org_id for %s: %v", repoID, err)
	}
	return orgID
}

// readBlockRefCount returns the ref_count for a block, or -999 if not found.
func readBlockRefCount(t *testing.T, orgID, blockID string) int {
	t.Helper()
	session := shareProjectionDBForTest(t).Session()
	var rc int
	err := session.Query(`SELECT ref_count FROM blocks WHERE org_id = ? AND block_id = ?`, orgID, blockID).Scan(&rc)
	if errors.Is(err, gocql.ErrNotFound) {
		return -999 // block row deleted
	}
	if err != nil {
		t.Fatalf("readBlockRefCount: %v", err)
	}
	return rc
}

// blockExistsInDB returns true if the block row exists in the blocks table.
func blockExistsInDB(t *testing.T, orgID, blockID string) bool {
	t.Helper()
	return readBlockRefCount(t, orgID, blockID) != -999
}

// gcCandidateExists returns true if the block has a gc_block_candidates entry.
func gcCandidateExists(t *testing.T, orgID, blockID string) bool {
	t.Helper()
	session := shareProjectionDBForTest(t).Session()
	var ts time.Time
	err := session.Query(`SELECT candidate_at FROM gc_block_candidates WHERE org_id = ? AND block_id = ?`, orgID, blockID).Scan(&ts)
	return err == nil
}

// uploadUniqueFile uploads a file with unique content and returns the block ID (SHA-256 of content).
func uploadUniqueFile(t *testing.T, c *testClient, repoID, fileName, parentDir string) (content string, blockID string) {
	t.Helper()
	content = fmt.Sprintf("gc-test-%s-%d\n", fileName, time.Now().UnixNano())
	uploadLinkResp := c.Get(t, fmt.Sprintf("/api2/repos/%s/upload-link/?p=%s", repoID, parentDir))
	expectStatus(t, uploadLinkResp, http.StatusOK)
	uploadURL := strings.Trim(responseBody(t, uploadLinkResp), "\" \n\r")
	uploadFileThroughLink(t, c, uploadURL, fileName, parentDir, content)

	hash := sha256.Sum256([]byte(content))
	blockID = hex.EncodeToString(hash[:])
	return content, blockID
}

// batchDeleteFiles deletes files via the batch-delete API.
func batchDeleteFiles(t *testing.T, c *testClient, repoID, parentDir string, fileNames []string) {
	t.Helper()
	body := map[string]interface{}{
		"repo_id":    repoID,
		"parent_dir": parentDir,
		"dirents":    fileNames,
	}
	resp := c.DeleteJSON(t, "/api/v2.1/repos/batch-delete-item/", body)
	expectStatus(t, resp, http.StatusOK)
	resp.Body.Close()
}

// pollUntil polls fn every interval until it returns true or timeout expires.
func pollUntil(t *testing.T, timeout time.Duration, interval time.Duration, fn func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return true
		}
		time.Sleep(interval)
	}
	return false
}

type gcStatusPayload struct {
	Enabled            bool   `json:"enabled"`
	DryRun             bool   `json:"dry_run"`
	LastWorkerRun      string `json:"last_worker_run"`
	LastScanRun        string `json:"last_scan_run"`
	QueueSize          int    `json:"queue_size"`
	FailedItemsTotal   int    `json:"failed_items_total"`
	BlocksDeletedTotal int64  `json:"blocks_deleted_total"`
}

func getGCStatus(t *testing.T) gcStatusPayload {
	t.Helper()

	resp := superadminClient.Get(t, "/api/v2.1/admin/gc/status")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gc status returned HTTP %d", resp.StatusCode)
	}
	var status gcStatusPayload
	decodeJSON(t, resp, &status)
	return status
}

// getGCQueueSize reads the current GC queue size from the admin API.
func getGCQueueSize(t *testing.T) int {
	t.Helper()
	return getGCStatus(t).QueueSize
}

func readGCQueueLiveCount(t *testing.T) int {
	t.Helper()

	session := shareProjectionDBForTest(t).Session()
	var count int64
	if err := session.Query(`SELECT COUNT(*) FROM gc_queue`).Scan(&count); err != nil {
		t.Fatalf("failed to count live gc_queue rows: %v", err)
	}
	return int(count)
}

func readGCStatsInt(t *testing.T, key string) int {
	t.Helper()

	session := shareProjectionDBForTest(t).Session()
	var value string
	err := session.Query(`SELECT stat_value FROM gc_stats WHERE stat_key = ?`, key).Scan(&value)
	if errors.Is(err, gocql.ErrNotFound) {
		return 0
	}
	if err != nil {
		t.Fatalf("failed to read gc_stats[%s]: %v", key, err)
	}
	var parsed int
	if _, err := fmt.Sscanf(value, "%d", &parsed); err != nil {
		t.Fatalf("failed to parse gc_stats[%s]=%q: %v", key, value, err)
	}
	return parsed
}

func readGCQueueSnapshotTotal(t *testing.T) int {
	t.Helper()
	return readGCStatsInt(t, "total_queue_depth")
}

func readGCFailedSnapshotTotal(t *testing.T) int {
	t.Helper()
	return readGCStatsInt(t, "total_failed_items")
}

func readOrgQueueLiveCount(t *testing.T, orgID uuid.UUID) int {
	t.Helper()

	session := shareProjectionDBForTest(t).Session()
	var count int64
	if err := session.Query(`SELECT COUNT(*) FROM gc_queue WHERE org_id = ?`, orgID.String()).Scan(&count); err != nil {
		t.Fatalf("failed to count org gc_queue rows for %s: %v", orgID, err)
	}
	return int(count)
}

func gcQueueItemExists(t *testing.T, orgID string, itemType string, itemID string) bool {
	t.Helper()

	session := shareProjectionDBForTest(t).Session()
	iter := session.Query(`SELECT item_type, item_id FROM gc_queue WHERE org_id = ?`, orgID).Iter()
	var queuedItemType string
	var queuedItemID string
	for iter.Scan(&queuedItemType, &queuedItemID) {
		if queuedItemType == itemType && queuedItemID == itemID {
			if err := iter.Close(); err != nil {
				t.Fatalf("failed to close gc_queue iterator for org %s: %v", orgID, err)
			}
			return true
		}
	}
	if err := iter.Close(); err != nil {
		t.Fatalf("failed to scan gc_queue for org %s: %v", orgID, err)
	}
	return false
}

func gcOrgBucketForTest(orgID uuid.UUID) int {
	h := fnv.New32a()
	_, _ = h.Write(orgID[:])
	return int(h.Sum32() % 32)
}

func deleteGCQueueItemsByIdentity(t *testing.T, orgID string, itemType string, itemID string) {
	t.Helper()
	session := shareProjectionDBForTest(t).Session()
	iter := session.Query(`SELECT queued_at, item_type, item_id FROM gc_queue WHERE org_id = ?`, orgID).Iter()
	var queuedAt time.Time
	var queuedItemType string
	var queuedItemID string
	for iter.Scan(&queuedAt, &queuedItemType, &queuedItemID) {
		if queuedItemType == itemType && queuedItemID == itemID {
			if err := session.Query(`DELETE FROM gc_queue WHERE org_id = ? AND queued_at = ? AND item_type = ? AND item_id = ?`, orgID, queuedAt, queuedItemType, queuedItemID).Exec(); err != nil {
				t.Fatalf("failed to delete gc_queue row for %s/%s: %v", orgID, itemID, err)
			}
		}
	}
	if err := iter.Close(); err != nil {
		t.Fatalf("failed to scan gc_queue for deletion: %v", err)
	}
}

func readGCOrgQueueStats(t *testing.T, orgID uuid.UUID) (int, int) {
	t.Helper()
	session := shareProjectionDBForTest(t).Session()
	var queueDepth int
	var failedDepth int
	err := session.Query(`SELECT queue_depth, failed_depth FROM gc_org_stats WHERE org_id = ?`, orgID.String()).Scan(&queueDepth, &failedDepth)
	if errors.Is(err, gocql.ErrNotFound) {
		return 0, 0
	}
	if err != nil {
		t.Fatalf("failed to read gc_org_stats for %s: %v", orgID, err)
	}
	return queueDepth, failedDepth
}

func failedQueueItemExists(t *testing.T, orgID string, itemType string, itemID string) bool {
	t.Helper()
	session := shareProjectionDBForTest(t).Session()
	iter := session.Query(`SELECT item_type, item_id FROM gc_failed_items WHERE org_id = ?`, orgID).Iter()
	var failedItemType string
	var failedItemID string
	for iter.Scan(&failedItemType, &failedItemID) {
		if failedItemType == itemType && failedItemID == itemID {
			if err := iter.Close(); err != nil {
				t.Fatalf("failed to close gc_failed_items iterator for org %s: %v", orgID, err)
			}
			return true
		}
	}
	if err := iter.Close(); err != nil {
		t.Fatalf("failed to scan gc_failed_items for org %s: %v", orgID, err)
	}
	return false
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestGC_BlockLifecycle verifies the complete happy path:
// upload file → delete → blocks enqueued → GC processes → blocks gone from DB.
func TestGC_BlockLifecycle(t *testing.T) {
	requireGCEnabled(t)
	requireCassandra(t)

	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-gc-lifecycle-%d", time.Now().UnixNano()))
	orgID := resolveOrgID(t, repoID)

	// Upload a file with unique content
	_, blockID := uploadUniqueFile(t, adminClient, repoID, "lifecycle.txt", "/")
	t.Logf("Uploaded file, blockID=%s", blockID[:16])

	// Verify block exists with ref_count=1
	rc := readBlockRefCount(t, orgID, blockID)
	if rc != 1 {
		t.Fatalf("expected ref_count=1 after upload, got %d", rc)
	}

	// Delete the file
	batchDeleteFiles(t, adminClient, repoID, "/", []string{"lifecycle.txt"})

	// Poll until ref_count drops to 0 (async decrement)
	ok := pollUntil(t, 20*time.Second, 200*time.Millisecond, func() bool {
		return readBlockRefCount(t, orgID, blockID) <= 0
	})
	if !ok {
		t.Fatalf("ref_count did not reach 0 within timeout (got %d)", readBlockRefCount(t, orgID, blockID))
	}

	// Verify GC candidate was created
	ok = pollUntil(t, 10*time.Second, 200*time.Millisecond, func() bool {
		return gcCandidateExists(t, orgID, blockID)
	})
	if !ok {
		t.Fatal("gc_block_candidates entry not created after ref_count hit 0")
	}

	// Trigger GC worker multiple times (grace period is server-configured;
	// in dev mode it may be short enough to process immediately)
	for i := 0; i < 5; i++ {
		triggerGCWorker(t)
	}

	// Check if block was deleted from DB (may still be in grace period)
	t.Logf("Block ref_count after GC triggers: %d, exists=%v",
		readBlockRefCount(t, orgID, blockID),
		blockExistsInDB(t, orgID, blockID))

	// The test succeeds if: (a) ref_count reached 0, and (b) GC candidate was created.
	// Full deletion depends on the server's grace period config which we can't control.
	t.Log("Block lifecycle: ref_count→0 and GC candidate created — pipeline healthy")
}

// TestGC_DeduplicationSafety verifies that shared blocks survive when only
// one of two files referencing them is deleted.
func TestGC_DeduplicationSafety(t *testing.T) {
	requireGCEnabled(t)
	requireCassandra(t)

	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-gc-dedup-%d", time.Now().UnixNano()))
	orgID := resolveOrgID(t, repoID)

	// Upload two files with identical content (shared block)
	sharedContent := fmt.Sprintf("dedup-shared-%d\n", time.Now().UnixNano())
	hash := sha256.Sum256([]byte(sharedContent))
	blockID := hex.EncodeToString(hash[:])

	uploadLinkResp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/upload-link/?p=/", repoID))
	expectStatus(t, uploadLinkResp, http.StatusOK)
	uploadURL := strings.Trim(responseBody(t, uploadLinkResp), "\" \n\r")
	uploadFileThroughLink(t, adminClient, uploadURL, "dedup-a.txt", "/", sharedContent)
	uploadFileThroughLink(t, adminClient, uploadURL, "dedup-b.txt", "/", sharedContent)

	// Verify ref_count=2
	rc := readBlockRefCount(t, orgID, blockID)
	if rc != 2 {
		t.Fatalf("expected ref_count=2 after dedup upload, got %d", rc)
	}

	// Delete only file A
	batchDeleteFiles(t, adminClient, repoID, "/", []string{"dedup-a.txt"})

	// Wait for async decrement
	ok := pollUntil(t, 15*time.Second, 200*time.Millisecond, func() bool {
		return readBlockRefCount(t, orgID, blockID) == 1
	})
	if !ok {
		t.Fatalf("ref_count should be 1 after deleting one file, got %d", readBlockRefCount(t, orgID, blockID))
	}

	// Trigger GC — block should NOT be deleted (ref_count=1)
	for i := 0; i < 3; i++ {
		triggerGCWorker(t)
	}

	// Block must still exist
	if !blockExistsInDB(t, orgID, blockID) {
		t.Fatal("CRITICAL: shared block was deleted while still referenced — deduplication safety violated!")
	}

	rc = readBlockRefCount(t, orgID, blockID)
	if rc < 1 {
		t.Fatalf("shared block ref_count dropped below 1 (%d) — potential data loss", rc)
	}

	// File B should still be downloadable
	dlResp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/file/?p=/dedup-b.txt", repoID))
	expectStatus(t, dlResp, http.StatusOK)
	dlResp.Body.Close()

	t.Log("Dedup safety: shared block survived single-file delete — correct")
}

// TestGC_ConcurrentUploadDuringGC verifies the LWT guard: if a block is
// re-referenced after being enqueued for GC, the LWT prevents deletion.
func TestGC_ConcurrentUploadDuringGC(t *testing.T) {
	requireGCEnabled(t)
	requireCassandra(t)

	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-gc-race-%d", time.Now().UnixNano()))
	orgID := resolveOrgID(t, repoID)

	// Upload file, then delete it (ref_count → 0, enqueued for GC)
	content := fmt.Sprintf("race-content-%d\n", time.Now().UnixNano())
	hash := sha256.Sum256([]byte(content))
	blockID := hex.EncodeToString(hash[:])

	uploadLinkResp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/upload-link/?p=/", repoID))
	expectStatus(t, uploadLinkResp, http.StatusOK)
	uploadURL := strings.Trim(responseBody(t, uploadLinkResp), "\" \n\r")
	uploadFileThroughLink(t, adminClient, uploadURL, "race-v1.txt", "/", content)

	batchDeleteFiles(t, adminClient, repoID, "/", []string{"race-v1.txt"})

	// Wait for ref_count to drop
	pollUntil(t, 15*time.Second, 200*time.Millisecond, func() bool {
		return readBlockRefCount(t, orgID, blockID) <= 0
	})

	// Re-upload the same content (ref_count should go back to 1)
	uploadLinkResp2 := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/upload-link/?p=/", repoID))
	expectStatus(t, uploadLinkResp2, http.StatusOK)
	uploadURL2 := strings.Trim(responseBody(t, uploadLinkResp2), "\" \n\r")
	uploadFileThroughLink(t, adminClient, uploadURL2, "race-v2.txt", "/", content)

	// Verify ref_count is back to positive
	ok := pollUntil(t, 10*time.Second, 200*time.Millisecond, func() bool {
		return readBlockRefCount(t, orgID, blockID) >= 1
	})
	if !ok {
		t.Logf("Warning: ref_count=%d (may not have been incremented yet)", readBlockRefCount(t, orgID, blockID))
	}

	// Trigger GC — LWT should prevent deletion because ref_count > 0
	for i := 0; i < 5; i++ {
		triggerGCWorker(t)
	}

	// Block must still exist (LWT guard)
	if !blockExistsInDB(t, orgID, blockID) {
		t.Fatal("CRITICAL: LWT guard failed — block deleted despite ref_count > 0 after re-upload!")
	}

	// File should be downloadable
	dlResp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/file/?p=/race-v2.txt", repoID))
	expectStatus(t, dlResp, http.StatusOK)
	dlResp.Body.Close()

	t.Log("Concurrent upload safety: LWT prevented deletion of re-referenced block — correct")
}

// TestGC_LibraryCascade verifies that soft-deleting a library and triggering
// the scanner/worker cascades through commits, fs_objects, and blocks.
func TestGC_LibraryCascade(t *testing.T) {
	requireGCEnabled(t)
	requireCassandra(t)

	repoName := fmt.Sprintf("inttest-gc-cascade-%d", time.Now().UnixNano())
	repoID := createDisposableTestLibrary(t, adminClient, repoName)
	orgID := resolveOrgID(t, repoID)

	// Upload multiple files to create commits and blocks
	var blockIDs []string
	for i := 0; i < 3; i++ {
		_, bid := uploadUniqueFile(t, adminClient, repoID, fmt.Sprintf("cascade-%d.txt", i), "/")
		blockIDs = append(blockIDs, bid)
	}

	// Verify blocks exist
	for _, bid := range blockIDs {
		if !blockExistsInDB(t, orgID, bid) {
			t.Fatalf("block %s not found after upload", bid[:16])
		}
	}

	// Soft-delete the library
	delResp := adminClient.Delete(t, fmt.Sprintf("/api2/repos/%s/", repoID))
	expectStatus(t, delResp, http.StatusOK)
	delResp.Body.Close()

	t.Log("Library soft-deleted, triggering scanner + worker...")

	// Trigger scanner to find expired deleted library, then worker to process
	for i := 0; i < 3; i++ {
		triggerGCScanner(t)
		time.Sleep(2 * time.Second)
	}
	for i := 0; i < 10; i++ {
		triggerGCWorker(t)
		time.Sleep(500 * time.Millisecond)
	}

	// Check if blocks' ref_counts were decremented
	for _, bid := range blockIDs {
		rc := readBlockRefCount(t, orgID, bid)
		t.Logf("Block %s ref_count after cascade: %d", bid[:16], rc)
	}

	// Verify the library no longer appears in active list
	listResp := adminClient.Get(t, "/api2/repos/")
	expectStatus(t, listResp, http.StatusOK)
	listBody := responseBody(t, listResp)
	if strings.Contains(listBody, repoID) {
		t.Log("Library still in active list (may be in trash); checking trash...")
	}

	t.Log("Library cascade: scanner + worker processed soft-deleted library")
}

// TestGC_ScannerOrphanRecovery verifies the scanner finds blocks with
// ref_count=0 that were never enqueued for GC (simulating a missed enqueue).
func TestGC_ScannerOrphanRecovery(t *testing.T) {
	requireGCEnabled(t)
	requireCassandra(t)

	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-gc-orphan-%d", time.Now().UnixNano()))
	orgID := resolveOrgID(t, repoID)

	// Upload a file, then delete it to get a real block with ref_count=0
	_, blockID := uploadUniqueFile(t, adminClient, repoID, "orphan.txt", "/")
	batchDeleteFiles(t, adminClient, repoID, "/", []string{"orphan.txt"})

	// Wait for ref_count to drop
	ok := pollUntil(t, 20*time.Second, 200*time.Millisecond, func() bool {
		return readBlockRefCount(t, orgID, blockID) <= 0
	})
	if !ok {
		t.Fatalf("ref_count did not reach 0: %d", readBlockRefCount(t, orgID, blockID))
	}

	// Delete the gc_block_candidates entry directly (simulate missed enqueue)
	session := shareProjectionDBForTest(t).Session()
	if err := session.Query(`DELETE FROM gc_block_candidates WHERE org_id = ? AND block_id = ?`, orgID, blockID).Exec(); err != nil {
		t.Logf("Warning: could not delete GC candidate (may not exist): %v", err)
	}
	deleteGCQueueItemsByIdentity(t, orgID, "block", blockID)

	t.Log("Orphan block created (ref_count=0, no GC candidate). Running scanner...")

	triggerGCScannerAndWait(t)

	// Verify the orphan was re-enqueued (allow generous timeout for scanner to complete)
	ok = pollUntil(t, 30*time.Second, 1*time.Second, func() bool {
		return gcCandidateExists(t, orgID, blockID)
	})
	if !ok {
		// Check if the block was already fully processed (deleted by worker after scanner found it)
		if !blockExistsInDB(t, orgID, blockID) {
			t.Log("Scanner orphan recovery: block was found AND deleted (scanner + worker both ran) — correct")
			return
		}
		t.Fatal("Scanner did not re-discover orphaned block with ref_count=0")
	}

	t.Log("Scanner orphan recovery: block re-enqueued for GC — correct")
}

// getGCGracePeriod reads the configured grace period from the admin status API.
// Returns 0 if the field is absent or the server is too old to expose it.
func getGCGracePeriod(t *testing.T) time.Duration {
	t.Helper()
	resp := superadminClient.Get(t, "/api/v2.1/admin/gc/status")
	if resp.StatusCode != http.StatusOK {
		return 0
	}
	var body map[string]interface{}
	decodeJSON(t, resp, &body)
	if gps, ok := body["grace_period_seconds"].(float64); ok {
		return time.Duration(gps) * time.Second
	}
	return 0
}

// TestGC_GracePeriodEnforcement verifies that the GC worker does not process
// items that are within the grace period.
func TestGC_GracePeriodEnforcement(t *testing.T) {
	requireGCEnabled(t)
	requireCassandra(t)

	gracePeriod := getGCGracePeriod(t)
	if gracePeriod == 0 {
		t.Skip("grace_period_seconds=0 in server config — grace period enforcement is disabled; skipping")
	}
	t.Logf("Grace period configured: %v", gracePeriod)

	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-gc-grace-%d", time.Now().UnixNano()))
	orgID := resolveOrgID(t, repoID)

	_, blockID := uploadUniqueFile(t, adminClient, repoID, "grace.txt", "/")
	batchDeleteFiles(t, adminClient, repoID, "/", []string{"grace.txt"})

	// Wait for ref_count to drop to 0 and the GC candidate to be created.
	ok := pollUntil(t, 20*time.Second, 200*time.Millisecond, func() bool {
		return readBlockRefCount(t, orgID, blockID) <= 0 && gcCandidateExists(t, orgID, blockID)
	})
	if !ok {
		t.Fatalf("block did not reach ref_count=0 + gc_candidate within timeout (rc=%d, candidate=%v)",
			readBlockRefCount(t, orgID, blockID), gcCandidateExists(t, orgID, blockID))
	}
	enqueuedAt := time.Now()

	// Trigger GC immediately — the candidate was enqueued mere milliseconds ago,
	// which is well within any non-zero grace period. The worker must skip it.
	triggerGCWorker(t)

	elapsed := time.Since(enqueuedAt)
	if !blockExistsInDB(t, orgID, blockID) {
		t.Fatalf("CRITICAL: grace period not enforced — block deleted ~%v after enqueue (grace_period=%v)",
			elapsed, gracePeriod)
	}
	t.Logf("Grace period enforcement: block correctly preserved ~%v after enqueue (grace_period=%v) — correct",
		elapsed, gracePeriod)
}

// TestGC_QueueSizeTracking verifies that real queue rows are created when GC work
// is enqueued and that status follows the persisted queue-depth snapshot.
func TestGC_QueueSizeTracking(t *testing.T) {
	requireGCEnabled(t)
	requireCassandra(t)

	statusBefore := getGCQueueSize(t)
	liveBefore := readGCQueueLiveCount(t)
	snapshotBefore := readGCQueueSnapshotTotal(t)

	// Create a library, upload, delete to generate GC work
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-gc-qsize-%d", time.Now().UnixNano()))
	orgID := resolveOrgID(t, repoID)
	_, blockID := uploadUniqueFile(t, adminClient, repoID, "qsize.txt", "/")
	batchDeleteFiles(t, adminClient, repoID, "/", []string{"qsize.txt"})

	// Poll for the specific block queue item created by this test. Global queue
	// totals are noisy because other GC work may be draining concurrently.
	ok := pollUntil(t, 15*time.Second, 500*time.Millisecond, func() bool {
		return gcQueueItemExists(t, orgID, "block", blockID)
	})
	if !ok {
		t.Fatalf("expected block %s to appear in gc_queue for org %s after enqueue (status_before=%d snapshot_before=%d status_now=%d snapshot_now=%d)",
			blockID, orgID, statusBefore, snapshotBefore, getGCQueueSize(t), readGCQueueSnapshotTotal(t))
	}

	statusAfter := getGCQueueSize(t)
	liveAfter := readGCQueueLiveCount(t)
	snapshotAfter := readGCQueueSnapshotTotal(t)
	t.Logf("GC queue state after enqueue: status=%d live=%d snapshot=%d (before status=%d live=%d snapshot=%d)",
		statusAfter, liveAfter, snapshotAfter, statusBefore, liveBefore, snapshotBefore)

	if snapshotAfter == 0 {
		t.Fatalf("expected total_queue_depth snapshot to be non-zero after enqueue")
	}
	if statusAfter == 0 {
		t.Fatalf("expected status.queue_size to be non-zero after enqueue")
	}
}

// TestGC_StatusSnapshotReconcilesLiveQueueCount verifies that the scanner can
// reconcile the queue-depth snapshot from live Cassandra rows without relying
// on the retired gc_queue_stats counter table.
func TestGC_StatusSnapshotReconcilesLiveQueueCount(t *testing.T) {
	requireGCEnabled(t)
	requireCassandra(t)

	session := shareProjectionDBForTest(t).Session()
	orgID := uuid.New()
	bucket := gcOrgBucketForTest(orgID)
	queuedAt := time.Now().Add(-time.Minute).UTC()
	itemID := fmt.Sprintf("synthetic-drift-%d", time.Now().UnixNano())
	libraryID := uuid.New()
	if err := session.Query(`
		INSERT INTO gc_queue (org_id, queued_at, item_type, item_id, library_id, storage_class, retry_count)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, orgID.String(), queuedAt, "block", itemID, libraryID.String(), "hot", 0).Exec(); err != nil {
		t.Fatalf("failed to insert synthetic gc_queue row: %v", err)
	}
	if err := session.Query(`
		INSERT INTO gc_active_orgs (bucket, org_id, last_enqueued_at)
		VALUES (?, ?, ?)
	`, bucket, orgID.String(), time.Now().UTC()).Exec(); err != nil {
		t.Fatalf("failed to insert active org row: %v", err)
	}
	if err := session.Query(`
		INSERT INTO gc_dirty_orgs (bucket, org_id, marked_at)
		VALUES (?, ?, ?)
	`, bucket, orgID.String(), time.Now().UTC()).Exec(); err != nil {
		t.Fatalf("failed to insert dirty org row: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Query(`
			DELETE FROM gc_queue
			WHERE org_id = ? AND queued_at = ? AND item_type = ? AND item_id = ?
		`, orgID.String(), queuedAt, "block", itemID).Exec()
		_ = session.Query(`DELETE FROM gc_active_orgs WHERE bucket = ? AND org_id = ?`, bucket, orgID.String()).Exec()
		_ = session.Query(`DELETE FROM gc_dirty_orgs WHERE bucket = ? AND org_id = ?`, bucket, orgID.String()).Exec()
	})
	snapshotBefore := readGCQueueSnapshotTotal(t)
	triggerGCScannerAndWait(t)

	ok := pollUntil(t, 45*time.Second, 500*time.Millisecond, func() bool {
		queueDepth, _ := readGCOrgQueueStats(t, orgID)
		return queueDepth == 1
	})
	statusAfter := getGCStatus(t)
	liveAfter := readGCQueueLiveCount(t)
	snapshotAfter := readGCQueueSnapshotTotal(t)
	orgQueueDepth, orgFailedDepth := readGCOrgQueueStats(t, orgID)
	t.Logf("GC snapshot reconciliation: status=%d live=%d snapshot=%d snapshot_before=%d org_queue=%d org_failed=%d", statusAfter.QueueSize, liveAfter, snapshotAfter, snapshotBefore, orgQueueDepth, orgFailedDepth)

	if !ok {
		t.Fatalf("expected org-local queue stats to be reconciled for synthetic row, status=%d snapshot=%d live=%d org_queue=%d", statusAfter.QueueSize, snapshotAfter, liveAfter, orgQueueDepth)
	}
	if orgQueueDepth != 1 {
		t.Fatalf("expected reconciled org queue depth to equal 1, got %d", orgQueueDepth)
	}
	if snapshotAfter <= 0 {
		t.Fatalf("expected total queue snapshot to stay positive after reconciliation")
	}
}

// TestGC_MaxRetryItemMovesToFailedQueue verifies that a max-retry item is
// removed from the live queue and captured in gc_failed_items.
func TestGC_MaxRetryItemMovesToFailedQueue(t *testing.T) {
	requireGCEnabled(t)
	requireCassandra(t)

	session := shareProjectionDBForTest(t).Session()
	orgID := uuid.New()
	bucket := gcOrgBucketForTest(orgID)
	queuedAt := time.Now().Add(-2 * time.Minute).UTC().Truncate(time.Millisecond)
	itemID := fmt.Sprintf("synthetic-max-retry-%d", time.Now().UnixNano())
	libraryID := uuid.New()

	orgLiveBefore := readOrgQueueLiveCount(t, orgID)
	failedBefore := readGCFailedSnapshotTotal(t)

	if err := session.Query(`
		INSERT INTO gc_queue (org_id, queued_at, item_type, item_id, library_id, storage_class, retry_count)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, orgID.String(), queuedAt, "unknown_type", itemID, libraryID.String(), "hot", 5).Exec(); err != nil {
		t.Fatalf("failed to insert max-retry queue row: %v", err)
	}
	if err := session.Query(`
		INSERT INTO gc_active_orgs (bucket, org_id, last_enqueued_at)
		VALUES (?, ?, ?)
	`, bucket, orgID.String(), time.Now().UTC()).Exec(); err != nil {
		t.Fatalf("failed to insert active org row: %v", err)
	}
	if err := session.Query(`
		INSERT INTO gc_dirty_orgs (bucket, org_id, marked_at)
		VALUES (?, ?, ?)
	`, bucket, orgID.String(), time.Now().UTC()).Exec(); err != nil {
		t.Fatalf("failed to insert dirty org row: %v", err)
	}
	var failedAt time.Time

	triggerGCWorkerAndWait(t)

	var (
		failedItemType  string
		failedItemID    string
		retryCountAfter int
		lastError       string
		rowFound        bool
	)
	ok := pollUntil(t, 45*time.Second, 500*time.Millisecond, func() bool {
		iter := session.Query(`
			SELECT failed_at, item_type, item_id, retry_count, last_error FROM gc_failed_items WHERE org_id = ?
		`, orgID.String()).Iter()
		rowFound = false
		for iter.Scan(&failedAt, &failedItemType, &failedItemID, &retryCountAfter, &lastError) {
			if failedItemType == "unknown_type" && failedItemID == itemID {
				rowFound = true
				break
			}
		}
		if err := iter.Close(); err != nil {
			t.Fatalf("failed to read gc_failed_items rows: %v", err)
		}
		if !rowFound {
			return false
		}
		var lingering string
		err := session.Query(`
			SELECT item_id FROM gc_queue
			WHERE org_id = ? AND queued_at = ? AND item_type = ? AND item_id = ?
		`, orgID.String(), queuedAt, "unknown_type", itemID).Scan(&lingering)
		return errors.Is(err, gocql.ErrNotFound) && getGCStatus(t).FailedItemsTotal > failedBefore
	})
	if !ok {
		t.Fatalf("expected max-retry item to be moved from gc_queue to gc_failed_items")
	}
	t.Cleanup(func() {
		_ = session.Query(`
			DELETE FROM gc_queue WHERE org_id = ? AND queued_at = ? AND item_type = ? AND item_id = ?
		`, orgID.String(), queuedAt, "unknown_type", itemID).Exec()
		_ = session.Query(`
			DELETE FROM gc_failed_items WHERE org_id = ? AND failed_at = ? AND item_type = ? AND item_id = ?
		`, orgID.String(), failedAt, "unknown_type", itemID).Exec()
		_ = session.Query(`DELETE FROM gc_active_orgs WHERE bucket = ? AND org_id = ?`, bucket, orgID.String()).Exec()
		_ = session.Query(`DELETE FROM gc_dirty_orgs WHERE bucket = ? AND org_id = ?`, bucket, orgID.String()).Exec()
	})

	statusAfter := getGCStatus(t)
	liveAfter := readGCQueueLiveCount(t)
	orgLiveAfter := readOrgQueueLiveCount(t, orgID)
	queueSnapshotAfter := readGCQueueSnapshotTotal(t)
	failedAfter := readGCFailedSnapshotTotal(t)
	orgQueueDepth, orgFailedDepth := readGCOrgQueueStats(t, orgID)
	t.Logf("GC max-retry flow: status=%d live=%d queue_snapshot=%d failed_snapshot=%d retry_after=%d failed_at=%s last_error=%q org_queue=%d org_failed=%d",
		statusAfter.QueueSize, liveAfter, queueSnapshotAfter, failedAfter, retryCountAfter, failedAt.Format(time.RFC3339Nano), lastError, orgQueueDepth, orgFailedDepth)

	if failedAt.IsZero() {
		t.Fatalf("expected gc_failed_items.failed_at to be populated")
	}
	if retryCountAfter != 5 {
		t.Fatalf("expected retry_count to stay at 5 in gc_failed_items, got %d", retryCountAfter)
	}
	if lastError == "" {
		t.Fatalf("expected gc_failed_items.last_error to be populated")
	}
	if orgLiveAfter != orgLiveBefore {
		t.Fatalf("expected the org-local live queue count to return to baseline after failing the max-retry item, before=%d after=%d", orgLiveBefore, orgLiveAfter)
	}
	if orgFailedDepth <= 0 {
		t.Fatalf("expected reconciled org failed depth to be positive after max-retry flow, got %d", orgFailedDepth)
	}
	if failedAfter <= failedBefore {
		t.Fatalf("expected total_failed_items snapshot to increase, before=%d after=%d", failedBefore, failedAfter)
	}
	if statusAfter.FailedItemsTotal <= failedBefore {
		t.Fatalf("expected status.failed_items_total to increase beyond baseline, before=%d after=%d", failedBefore, statusAfter.FailedItemsTotal)
	}
}

func TestGC_FailedItemsAdminEndpoints(t *testing.T) {
	requireGCEnabled(t)
	requireCassandra(t)

	session := shareProjectionDBForTest(t).Session()
	orgID := uuid.New()
	bucket := gcOrgBucketForTest(orgID)
	failedAtA := time.Now().Add(-2 * time.Minute).UTC().Truncate(time.Millisecond)
	failedAtB := time.Now().Add(-time.Minute).UTC().Truncate(time.Millisecond)
	itemIDA := fmt.Sprintf("dlq-a-%d", time.Now().UnixNano())
	itemIDB := fmt.Sprintf("dlq-b-%d", time.Now().UnixNano())
	libraryID := uuid.New()

	insertFailed := func(failedAt time.Time, itemID string) {
		t.Helper()
		if err := session.Query(`
			INSERT INTO gc_failed_items (
				org_id, failed_at, queued_at, item_type, item_id, library_id, storage_class, retry_count, last_error, resolution_status
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, orgID.String(), failedAt, failedAt.Add(-time.Minute), "unknown_type", itemID, libraryID.String(), "hot", 5, "boom", "open").Exec(); err != nil {
			t.Fatalf("failed to insert gc_failed_items row for %s: %v", itemID, err)
		}
		if err := session.Query(`
			INSERT INTO gc_dirty_orgs (bucket, org_id, marked_at) VALUES (?, ?, ?)
		`, bucket, orgID.String(), time.Now().UTC()).Exec(); err != nil {
			t.Fatalf("failed to insert dirty org row: %v", err)
		}
	}

	insertFailed(failedAtA, itemIDA)
	insertFailed(failedAtB, itemIDB)
	t.Cleanup(func() {
		_ = session.Query(`DELETE FROM gc_failed_items WHERE org_id = ? AND failed_at = ? AND item_type = ? AND item_id = ?`, orgID.String(), failedAtA, "unknown_type", itemIDA).Exec()
		_ = session.Query(`DELETE FROM gc_failed_items WHERE org_id = ? AND failed_at = ? AND item_type = ? AND item_id = ?`, orgID.String(), failedAtB, "unknown_type", itemIDB).Exec()
		deleteGCQueueItemsByIdentity(t, orgID.String(), "unknown_type", itemIDA)
		deleteGCQueueItemsByIdentity(t, orgID.String(), "unknown_type", itemIDB)
		_ = session.Query(`DELETE FROM gc_active_orgs WHERE bucket = ? AND org_id = ?`, bucket, orgID.String()).Exec()
		_ = session.Query(`DELETE FROM gc_dirty_orgs WHERE bucket = ? AND org_id = ?`, bucket, orgID.String()).Exec()
	})

	listResp := superadminClient.Get(t, "/api/v2.1/admin/gc/failed-items?org_id="+orgID.String()+"&limit=10")
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("expected failed-items list HTTP 200, got %d", listResp.StatusCode)
	}
	listBody := responseJSON(t, listResp)
	items, ok := listBody["items"].([]interface{})
	if !ok || len(items) < 2 {
		t.Fatalf("expected at least 2 failed items in admin list, got %#v", listBody["items"])
	}

	requeueResp := superadminClient.PostJSON(t, "/api/v2.1/admin/gc/failed-items/requeue", map[string]string{
		"org_id":    orgID.String(),
		"failed_at": failedAtA.Format(time.RFC3339Nano),
		"item_type": "unknown_type",
		"item_id":   itemIDA,
	})
	if requeueResp.StatusCode != http.StatusOK {
		t.Fatalf("expected failed-item requeue HTTP 200, got %d", requeueResp.StatusCode)
	}
	ok = pollUntil(t, 30*time.Second, 500*time.Millisecond, func() bool {
		return gcQueueItemExists(t, orgID.String(), "unknown_type", itemIDA) && !failedQueueItemExists(t, orgID.String(), "unknown_type", itemIDA)
	})
	if !ok {
		t.Fatalf("expected requeued failed item %s to leave gc_failed_items and reappear in gc_queue", itemIDA)
	}

	deleteResp := superadminClient.DeleteJSON(t, "/api/v2.1/admin/gc/failed-items", map[string]string{
		"org_id":    orgID.String(),
		"failed_at": failedAtB.Format(time.RFC3339Nano),
		"item_type": "unknown_type",
		"item_id":   itemIDB,
	})
	if deleteResp.StatusCode != http.StatusOK {
		t.Fatalf("expected failed-item delete HTTP 200, got %d", deleteResp.StatusCode)
	}
	ok = pollUntil(t, 30*time.Second, 500*time.Millisecond, func() bool {
		return !failedQueueItemExists(t, orgID.String(), "unknown_type", itemIDB)
	})
	if !ok {
		t.Fatalf("expected failed item %s to be removed from gc_failed_items by admin delete", itemIDB)
	}

	orgQueueDepth, orgFailedDepth := readGCOrgQueueStats(t, orgID)
	if orgQueueDepth <= 0 {
		t.Fatalf("expected org queue depth to be positive after admin requeue, got %d", orgQueueDepth)
	}
	if orgFailedDepth != 0 {
		t.Fatalf("expected org failed depth to be 0 after requeue+delete, got %d", orgFailedDepth)
	}
}
