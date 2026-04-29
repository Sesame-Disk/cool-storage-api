//go:build integration

package integration

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

// Regression suite for block refcount → zero-ref → GC enqueue pipeline (batch delete,
// directory aggregation, deduplicated uploads). Targets regressions from ref-count LWT
// changes, async batch-delete goroutine, and API commit_id contract.

func deleteJSONWithoutFatal(c *testClient, path string, body interface{}) (int, string, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return 0, "", err
	}

	req, err := http.NewRequest(http.MethodDelete, c.baseURL+path, bytes.NewBuffer(data))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, "", err
	}
	return resp.StatusCode, string(bodyBytes), nil
}

// TestBatchDeleteItems_CommitIDMatchesLibraryHead verifies the batch-delete response
// returns the new library head (not the pre-delete head). A wrong commit_id breaks
// clients and desyncs expectations from DecrementBlockRefCountsOnce's operation key.
func TestBatchDeleteItems_CommitIDMatchesLibraryHead(t *testing.T) {
	name := fmt.Sprintf("inttest-gc-commitid-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)

	fileName := "commit-id-check.txt"
	fileContent := fmt.Sprintf("commit-id-content-%d\n", time.Now().UnixNano())

	uploadLinkResp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/upload-link/?p=/", repoID))
	expectStatus(t, uploadLinkResp, http.StatusOK)
	uploadURL := strings.Trim(responseBody(t, uploadLinkResp), "\" \n\r")
	uploadFileThroughLink(t, adminClient, uploadURL, fileName, "/", fileContent)

	delBody := map[string]interface{}{
		"repo_id":    repoID,
		"parent_dir": "/",
		"dirents":    []string{fileName},
	}
	delResp := adminClient.DeleteJSON(t, "/api/v2.1/repos/batch-delete-item/", delBody)
	expectStatus(t, delResp, http.StatusOK)

	var body map[string]interface{}
	decodeJSON(t, delResp, &body)
	respCommit, _ := body["commit_id"].(string)
	if respCommit == "" {
		t.Fatalf("batch-delete response missing commit_id: %v", body)
	}

	session := shareProjectionDBForTest(t).Session()
	var dbHead string
	if err := session.Query(`SELECT head_commit_id FROM libraries_by_id WHERE library_id = ?`, repoID).Scan(&dbHead); err != nil {
		t.Fatalf("failed to read head_commit_id: %v", err)
	}
	if dbHead != respCommit {
		t.Fatalf("commit_id mismatch: API=%q libraries_by_id=%q (expected API to match new library head)", respCommit, dbHead)
	}
}

// TestBatchDeleteItems_ZeroRefBlockRegistersForGC asserts that after refcount hits zero,
// the block appears in gc_block_candidates (written before gc_queue enqueue). This catches
// regressions where DecrementBlockRefCountsOnce runs but EnqueueBlocks / adapter is broken,
// or where ref never reaches zero.
func TestBatchDeleteItems_ZeroRefBlockRegistersForGC(t *testing.T) {
	name := fmt.Sprintf("inttest-gc-candidate-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)

	fileName := "gc-candidate.txt"
	fileContent := fmt.Sprintf("gc-candidate-content-%d\n", time.Now().UnixNano())

	uploadLinkResp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/upload-link/?p=/", repoID))
	expectStatus(t, uploadLinkResp, http.StatusOK)
	uploadURL := strings.Trim(responseBody(t, uploadLinkResp), "\" \n\r")
	uploadFileThroughLink(t, adminClient, uploadURL, fileName, "/", fileContent)

	session := shareProjectionDBForTest(t).Session()
	var orgID string
	if err := session.Query(`SELECT org_id FROM libraries_by_id WHERE library_id = ?`, repoID).Scan(&orgID); err != nil {
		t.Fatalf("failed to resolve org_id: %v", err)
	}

	hash := sha256.Sum256([]byte(fileContent))
	blockID := hex.EncodeToString(hash[:])

	delBody := map[string]interface{}{
		"repo_id":    repoID,
		"parent_dir": "/",
		"dirents":    []string{fileName},
	}
	delResp := adminClient.DeleteJSON(t, "/api/v2.1/repos/batch-delete-item/", delBody)
	expectStatus(t, delResp, http.StatusOK)
	delResp.Body.Close()

	deadline := time.Now().Add(20 * time.Second)
	var refCount int
	var candidateAt time.Time
	for time.Now().Before(deadline) {
		if err := session.Query(`SELECT ref_count FROM blocks WHERE org_id = ? AND block_id = ?`, orgID, blockID).Scan(&refCount); err != nil {
			t.Fatalf("failed to read ref_count: %v", err)
		}
		if refCount <= 0 {
			err := session.Query(`
				SELECT candidate_at FROM gc_block_candidates WHERE org_id = ? AND block_id = ?
			`, orgID, blockID).Scan(&candidateAt)
			if err == nil {
				return
			}
			if !errors.Is(err, gocql.ErrNotFound) {
				t.Fatalf("unexpected gc_block_candidates query error: %v", err)
			}
		}
		time.Sleep(150 * time.Millisecond)
	}

	if refCount > 0 {
		t.Fatalf("ref_count still %d after batch delete — refcount / async decrement regression", refCount)
	}
	t.Fatalf("ref_count reached 0 but block %s never appeared in gc_block_candidates — GC enqueue regression", blockID)
}

// TestBatchDeleteDirectory_AllNestedBlocksReachZeroRef covers directory deletion:
// collectDirStats must aggregate all descendant blocks; missing directory handling
// would leave refs > 0 and leak storage.
func TestBatchDeleteDirectory_AllNestedBlocksReachZeroRef(t *testing.T) {
	name := fmt.Sprintf("inttest-gc-dir-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)

	dirPath := "/gc-nested-dir"
	dirResp := adminClient.PostJSON(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=%s", repoID, dirPath), map[string]string{})
	expectStatus(t, dirResp, http.StatusCreated)
	dirResp.Body.Close()

	contentA := fmt.Sprintf("nested-a-%d\n", time.Now().UnixNano())
	contentB := fmt.Sprintf("nested-b-%d\n", time.Now().UnixNano())
	uploadLinkResp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/upload-link/?p=%s", repoID, dirPath))
	expectStatus(t, uploadLinkResp, http.StatusOK)
	uploadURL := strings.Trim(responseBody(t, uploadLinkResp), "\" \n\r")
	uploadFileThroughLink(t, adminClient, uploadURL, "a.txt", dirPath, contentA)
	uploadFileThroughLink(t, adminClient, uploadURL, "b.txt", dirPath, contentB)

	session := shareProjectionDBForTest(t).Session()
	var orgID string
	if err := session.Query(`SELECT org_id FROM libraries_by_id WHERE library_id = ?`, repoID).Scan(&orgID); err != nil {
		t.Fatalf("failed to resolve org_id: %v", err)
	}

	sumA := sha256.Sum256([]byte(contentA))
	sumB := sha256.Sum256([]byte(contentB))
	blockA := hex.EncodeToString(sumA[:])
	blockB := hex.EncodeToString(sumB[:])

	delBody := map[string]interface{}{
		"repo_id":    repoID,
		"parent_dir": "/",
		"dirents":    []string{"gc-nested-dir"},
	}
	delResp := adminClient.DeleteJSON(t, "/api/v2.1/repos/batch-delete-item/", delBody)
	expectStatus(t, delResp, http.StatusOK)
	delResp.Body.Close()

	deadline := time.Now().Add(25 * time.Second)
	var refA, refB int
	for time.Now().Before(deadline) {
		_ = session.Query(`SELECT ref_count FROM blocks WHERE org_id = ? AND block_id = ?`, orgID, blockA).Scan(&refA)
		_ = session.Query(`SELECT ref_count FROM blocks WHERE org_id = ? AND block_id = ?`, orgID, blockB).Scan(&refB)
		if refA <= 0 && refB <= 0 {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("directory batch delete refcount regression: blockA=%d blockB=%d (want both <= 0)", refA, refB)
}

// TestBatchDeleteItems_DeduplicatedFilesDecrementSharedBlockTwice ensures a single
// batch-delete removes two dirents that share one block (identical content); ref_count
// must go from 2 to 0 in one operation. Catches failures to pass duplicate block IDs
// into DecrementBlockRefCountsOnce or broken LWT decrement loops.
func TestBatchDeleteItems_DeduplicatedFilesDecrementSharedBlockTwice(t *testing.T) {
	name := fmt.Sprintf("inttest-gc-dedup-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)

	uploadLinkResp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/upload-link/?p=/", repoID))
	expectStatus(t, uploadLinkResp, http.StatusOK)
	uploadURL := strings.Trim(responseBody(t, uploadLinkResp), "\" \n\r")

	shared := fmt.Sprintf("shared-bytes-%d\n", time.Now().UnixNano())
	uploadFileThroughLink(t, adminClient, uploadURL, "dedup-a.txt", "/", shared)
	uploadFileThroughLink(t, adminClient, uploadURL, "dedup-b.txt", "/", shared)

	session := shareProjectionDBForTest(t).Session()
	var orgID string
	if err := session.Query(`SELECT org_id FROM libraries_by_id WHERE library_id = ?`, repoID).Scan(&orgID); err != nil {
		t.Fatalf("failed to resolve org_id: %v", err)
	}
	sumShared := sha256.Sum256([]byte(shared))
	blockID := hex.EncodeToString(sumShared[:])

	var refCount int
	if err := session.Query(`SELECT ref_count FROM blocks WHERE org_id = ? AND block_id = ?`, orgID, blockID).Scan(&refCount); err != nil {
		t.Fatalf("failed to read ref_count: %v", err)
	}
	if refCount != 2 {
		t.Fatalf("ref_count before delete = %d, want 2 (deduplicated block)", refCount)
	}

	delBody := map[string]interface{}{
		"repo_id":    repoID,
		"parent_dir": "/",
		"dirents":    []string{"dedup-a.txt", "dedup-b.txt"},
	}
	delResp := adminClient.DeleteJSON(t, "/api/v2.1/repos/batch-delete-item/", delBody)
	expectStatus(t, delResp, http.StatusOK)
	delResp.Body.Close()

	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		if err := session.Query(`SELECT ref_count FROM blocks WHERE org_id = ? AND block_id = ?`, orgID, blockID).Scan(&refCount); err != nil {
			t.Fatalf("failed to read ref_count after delete: %v", err)
		}
		if refCount <= 0 {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("ref_count after deleting both deduplicated files = %d, want <= 0", refCount)
}

// TestBatchDeleteItems_ConcurrentDeletesPreserveLastSharedReference forces two
// batch-delete requests to remove the same shared block concurrently from separate
// libraries in the same org. The exact post-condition must be ref_count == 1, the
// final surviving reference must stay readable, and GC must not register the block
// as a delete candidate.
func TestBatchDeleteItems_ConcurrentDeletesPreserveLastSharedReference(t *testing.T) {
	repoA := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-gc-race-a-%d", time.Now().UnixNano()))
	repoB := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-gc-race-b-%d", time.Now().UnixNano()))
	repoC := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-gc-race-c-%d", time.Now().UnixNano()))

	orgA := resolveOrgID(t, repoA)
	orgB := resolveOrgID(t, repoB)
	orgC := resolveOrgID(t, repoC)
	if orgA != orgB || orgA != orgC {
		t.Fatalf("expected all test libraries in same org, got orgA=%q orgB=%q orgC=%q", orgA, orgB, orgC)
	}

	shared := fmt.Sprintf("shared-race-bytes-%d\n", time.Now().UnixNano())
	sumShared := sha256.Sum256([]byte(shared))
	blockID := hex.EncodeToString(sumShared[:])

	for _, tc := range []struct {
		repoID   string
		fileName string
	}{
		{repoID: repoA, fileName: "race-a.txt"},
		{repoID: repoB, fileName: "race-b.txt"},
		{repoID: repoC, fileName: "race-c.txt"},
	} {
		uploadLinkResp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/upload-link/?p=/", tc.repoID))
		expectStatus(t, uploadLinkResp, http.StatusOK)
		uploadURL := strings.Trim(responseBody(t, uploadLinkResp), "\" \n\r")
		uploadFileThroughLink(t, adminClient, uploadURL, tc.fileName, "/", shared)
	}

	if !pollUntil(t, 15*time.Second, 200*time.Millisecond, func() bool {
		return readBlockRefCount(t, orgA, blockID) == 3
	}) {
		t.Fatalf("ref_count before concurrent delete = %d, want 3", readBlockRefCount(t, orgA, blockID))
	}

	type deleteResult struct {
		name   string
		status int
		body   string
		err    error
	}

	start := make(chan struct{})
	results := make(chan deleteResult, 2)
	path := "/api/v2.1/repos/batch-delete-item/"
	deleteBodyFor := func(repoID, fileName string) map[string]interface{} {
		return map[string]interface{}{
			"repo_id":    repoID,
			"parent_dir": "/",
			"dirents":    []string{fileName},
		}
	}

	clients := []*testClient{
		newTestClient(adminClient.baseURL, adminClient.token),
		newTestClient(adminClient.baseURL, adminClient.token),
	}
	requests := []struct {
		client   *testClient
		name     string
		repoID   string
		fileName string
	}{
		{client: clients[0], name: "delete-race-a", repoID: repoA, fileName: "race-a.txt"},
		{client: clients[1], name: "delete-race-b", repoID: repoB, fileName: "race-b.txt"},
	}

	var wg sync.WaitGroup
	for _, req := range requests {
		wg.Add(1)
		go func(req struct {
			client   *testClient
			name     string
			repoID   string
			fileName string
		}) {
			defer wg.Done()
			<-start
			status, body, err := deleteJSONWithoutFatal(req.client, path, deleteBodyFor(req.repoID, req.fileName))
			results <- deleteResult{name: req.name, status: status, body: body, err: err}
		}(req)
	}
	close(start)
	wg.Wait()
	close(results)

	for res := range results {
		if res.err != nil {
			t.Fatalf("%s failed: %v", res.name, res.err)
		}
		if res.status != http.StatusOK {
			t.Fatalf("%s returned %d: %s", res.name, res.status, res.body)
		}
	}

	if !pollUntil(t, 20*time.Second, 200*time.Millisecond, func() bool {
		return readBlockRefCount(t, orgA, blockID) == 1
	}) {
		t.Fatalf("ref_count after concurrent deletes = %d, want exactly 1", readBlockRefCount(t, orgA, blockID))
	}
	if !blockExistsInDB(t, orgA, blockID) {
		t.Fatalf("shared block %s was deleted despite one surviving reference", blockID)
	}

	time.Sleep(1 * time.Second)
	if gcCandidateExists(t, orgA, blockID) {
		t.Fatalf("block %s was registered in gc_block_candidates despite surviving ref_count=1", blockID)
	}

	dlResp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/file/?p=/race-c.txt", repoC))
	expectStatus(t, dlResp, http.StatusOK)
	dlResp.Body.Close()
}
