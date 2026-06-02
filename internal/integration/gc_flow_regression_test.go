//go:build integration

package integration

import (
	"bytes"
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

// TestBatchDeleteItems_ZeroRefBlockRegistersForGC covers the current delete contract:
// batch delete advances the library HEAD, but old commits retain the deleted
// file's fs_object until version GC sweeps it later. The block therefore keeps
// its existing permanent fs:<library>:<fs_id> reference and must NOT become a GC
// candidate inline.
func TestBatchDeleteItems_ZeroRefBlockRegistersForGC(t *testing.T) {
	name := fmt.Sprintf("inttest-gc-candidate-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)

	fileName := "gc-candidate.txt"
	fileContent := fmt.Sprintf("gc-candidate-content-%d\n", time.Now().UnixNano())

	uploadLinkResp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/upload-link/?p=/", repoID))
	expectStatus(t, uploadLinkResp, http.StatusOK)
	uploadURL := strings.Trim(responseBody(t, uploadLinkResp), "\" \n\r")
	uploadFileThroughLink(t, adminClient, uploadURL, fileName, "/", fileContent)

	orgID := resolveOrgID(t, repoID)

	hash := sha256.Sum256([]byte(fileContent))
	blockID := hex.EncodeToString(hash[:])
	if refCount := readBlockRefCount(t, orgID, blockID); refCount != 1 {
		t.Fatalf("permanent refs after upload = %d, want 1", refCount)
	}

	delBody := map[string]interface{}{
		"repo_id":    repoID,
		"parent_dir": "/",
		"dirents":    []string{fileName},
	}
	delResp := adminClient.DeleteJSON(t, "/api/v2.1/repos/batch-delete-item/", delBody)
	expectStatus(t, delResp, http.StatusOK)
	delResp.Body.Close()

	if !pollUntil(t, 5*time.Second, 200*time.Millisecond, func() bool {
		return readBlockRefCount(t, orgID, blockID) == 1 && !gcCandidateExists(t, orgID, blockID)
	}) {
		t.Fatalf("batch delete unexpectedly changed block liveness state: refs=%d candidate=%v", readBlockRefCount(t, orgID, blockID), gcCandidateExists(t, orgID, blockID))
	}
}

// TestBatchDeleteDirectory_AllNestedBlocksReachZeroRef covers the retained-version
// behavior for directory deletes: removing a directory from HEAD does not inline
// drop descendant block refs while older commits still retain those fs_objects.
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

	orgID := resolveOrgID(t, repoID)

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

	if !pollUntil(t, 5*time.Second, 200*time.Millisecond, func() bool {
		return readBlockRefCount(t, orgID, blockA) == 1 &&
			readBlockRefCount(t, orgID, blockB) == 1 &&
			!gcCandidateExists(t, orgID, blockA) &&
			!gcCandidateExists(t, orgID, blockB)
	}) {
		t.Fatalf("directory delete unexpectedly changed retained block state: blockA refs=%d candidate=%v blockB refs=%d candidate=%v",
			readBlockRefCount(t, orgID, blockA), gcCandidateExists(t, orgID, blockA),
			readBlockRefCount(t, orgID, blockB), gcCandidateExists(t, orgID, blockB))
	}
}

// TestBatchDeleteItems_DeduplicatedFilesDecrementSharedBlockTwice covers the current
// same-library dedup model: identical content resolves to one shared fs_id, so the
// block has a single permanent reference before and after the delete until version
// GC reclaims the old commit's fs_object.
func TestBatchDeleteItems_DeduplicatedFilesDecrementSharedBlockTwice(t *testing.T) {
	name := fmt.Sprintf("inttest-gc-dedup-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)

	uploadLinkResp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/upload-link/?p=/", repoID))
	expectStatus(t, uploadLinkResp, http.StatusOK)
	uploadURL := strings.Trim(responseBody(t, uploadLinkResp), "\" \n\r")

	shared := fmt.Sprintf("shared-bytes-%d\n", time.Now().UnixNano())
	uploadFileThroughLink(t, adminClient, uploadURL, "dedup-a.txt", "/", shared)
	uploadFileThroughLink(t, adminClient, uploadURL, "dedup-b.txt", "/", shared)

	orgID := resolveOrgID(t, repoID)
	sumShared := sha256.Sum256([]byte(shared))
	blockID := hex.EncodeToString(sumShared[:])

	if refCount := readBlockRefCount(t, orgID, blockID); refCount != 1 {
		t.Fatalf("permanent refs before delete = %d, want 1 (shared fs_id)", refCount)
	}

	delBody := map[string]interface{}{
		"repo_id":    repoID,
		"parent_dir": "/",
		"dirents":    []string{"dedup-a.txt", "dedup-b.txt"},
	}
	delResp := adminClient.DeleteJSON(t, "/api/v2.1/repos/batch-delete-item/", delBody)
	expectStatus(t, delResp, http.StatusOK)
	delResp.Body.Close()

	if !pollUntil(t, 5*time.Second, 200*time.Millisecond, func() bool {
		return readBlockRefCount(t, orgID, blockID) == 1 && !gcCandidateExists(t, orgID, blockID)
	}) {
		t.Fatalf("deduplicated delete unexpectedly changed retained block state: refs=%d candidate=%v", readBlockRefCount(t, orgID, blockID), gcCandidateExists(t, orgID, blockID))
	}
}

// TestBatchDeleteItems_ConcurrentDeletesPreserveLastSharedReference covers the
// cross-library retained-version behavior: deleting from repoA and repoB updates
// each library HEAD, but their old commits still hold their block refs, so the
// shared block remains at three permanent refs until version GC reclaims those
// histories. The surviving repoC copy must remain readable and the block must not
// be queued for GC prematurely.
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

	if !pollUntil(t, 5*time.Second, 200*time.Millisecond, func() bool {
		return readBlockRefCount(t, orgA, blockID) == 3
	}) {
		t.Fatalf("refs after concurrent deletes = %d, want exactly 3 while old commits are retained", readBlockRefCount(t, orgA, blockID))
	}
	if !blockExistsInDB(t, orgA, blockID) {
		t.Fatalf("shared block %s was deleted despite one surviving reference", blockID)
	}

	if gcCandidateExists(t, orgA, blockID) {
		t.Fatalf("block %s was registered in gc_block_candidates despite retained cross-library refs", blockID)
	}

	dlResp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/file/?p=/race-c.txt", repoC))
	expectStatus(t, dlResp, http.StatusOK)
	dlResp.Body.Close()
}
