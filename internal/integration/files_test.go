//go:build integration

package integration

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestCreateDirectory(t *testing.T) {
	name := fmt.Sprintf("inttest-dir-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)

	// Create directory
	resp := adminClient.PostJSON(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/test-dir", repoID), map[string]string{})
	expectStatus(t, resp, http.StatusCreated)
	resp.Body.Close()

	// List root and verify directory exists
	listResp := adminClient.Get(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/", repoID))
	expectStatus(t, listResp, http.StatusOK)

	var dirList map[string]interface{}
	decodeJSON(t, listResp, &dirList)

	entries, ok := dirList["dirent_list"].([]interface{})
	if !ok {
		t.Fatal("expected dirent_list array in response")
	}

	if !containsEntry(entries, "name", "test-dir") {
		t.Error("created directory 'test-dir' not found in listing")
	}
}

func TestFileUpload(t *testing.T) {
	name := fmt.Sprintf("inttest-upload-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)

	// Create a test file using the api2 create endpoint
	values := url.Values{}
	resp := adminClient.PostForm(t, fmt.Sprintf("/api2/repos/%s/file/?p=/upload-test.txt&operation=create", repoID), values)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 or 201 for file create, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Verify file appears in directory listing
	listResp := adminClient.Get(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/", repoID))
	expectStatus(t, listResp, http.StatusOK)

	var dirList map[string]interface{}
	decodeJSON(t, listResp, &dirList)

	entries, ok := dirList["dirent_list"].([]interface{})
	if !ok {
		t.Fatal("expected dirent_list array in response")
	}

	if !containsEntry(entries, "name", "upload-test.txt") {
		t.Error("created file 'upload-test.txt' not found in listing")
	}
}

func TestFileDownload(t *testing.T) {
	name := fmt.Sprintf("inttest-download-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)

	// Create a test file
	values := url.Values{}
	createResp := adminClient.PostForm(t, fmt.Sprintf("/api2/repos/%s/file/?p=/download-test.txt&operation=create", repoID), values)
	if createResp.StatusCode != http.StatusCreated && createResp.StatusCode != http.StatusOK {
		t.Fatalf("failed to create test file, got %d", createResp.StatusCode)
	}
	createResp.Body.Close()

	// Get download link
	dlResp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/file/?p=/download-test.txt", repoID))
	if dlResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for download link, got %d", dlResp.StatusCode)
	}
	dlResp.Body.Close()
}

func TestFileMoveAndCopy(t *testing.T) {
	name := fmt.Sprintf("inttest-movecopy-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)

	// Create source and target dirs
	resp := adminClient.PostJSON(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/src-dir", repoID), map[string]string{})
	expectStatus(t, resp, http.StatusCreated)
	resp.Body.Close()

	resp = adminClient.PostJSON(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/dst-dir", repoID), map[string]string{})
	expectStatus(t, resp, http.StatusCreated)
	resp.Body.Close()

	// Create a test item in source dir
	resp = adminClient.PostJSON(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/src-dir/item-to-move", repoID), map[string]string{})
	expectStatus(t, resp, http.StatusCreated)
	resp.Body.Close()

	resp = adminClient.PostJSON(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/src-dir/item-to-copy", repoID), map[string]string{})
	expectStatus(t, resp, http.StatusCreated)
	resp.Body.Close()

	t.Run("move item", func(t *testing.T) {
		moveBody := map[string]interface{}{
			"src_repo_id":    repoID,
			"src_parent_dir": "/src-dir",
			"dst_repo_id":    repoID,
			"dst_parent_dir": "/dst-dir",
			"src_dirents":    []string{"item-to-move"},
		}
		moveResp := adminClient.PostJSON(t, "/api/v2.1/repos/sync-batch-move-item/", moveBody)
		expectStatus(t, moveResp, http.StatusOK)
		moveResp.Body.Close()

		// Verify item is in dst
		listResp := adminClient.Get(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/dst-dir", repoID))
		expectStatus(t, listResp, http.StatusOK)

		var dirList map[string]interface{}
		decodeJSON(t, listResp, &dirList)
		entries, _ := dirList["dirent_list"].([]interface{})
		if !containsEntry(entries, "name", "item-to-move") {
			t.Error("moved item not found in dst-dir")
		}
	})

	t.Run("copy item", func(t *testing.T) {
		copyBody := map[string]interface{}{
			"src_repo_id":    repoID,
			"src_parent_dir": "/src-dir",
			"dst_repo_id":    repoID,
			"dst_parent_dir": "/dst-dir",
			"src_dirents":    []string{"item-to-copy"},
		}
		copyResp := adminClient.PostJSON(t, "/api/v2.1/repos/sync-batch-copy-item/", copyBody)
		expectStatus(t, copyResp, http.StatusOK)
		copyResp.Body.Close()

		// Verify original still exists
		srcResp := adminClient.Get(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/src-dir", repoID))
		expectStatus(t, srcResp, http.StatusOK)

		var srcList map[string]interface{}
		decodeJSON(t, srcResp, &srcList)
		srcEntries, _ := srcList["dirent_list"].([]interface{})
		if !containsEntry(srcEntries, "name", "item-to-copy") {
			t.Error("original item missing from src-dir after copy")
		}

		// Verify copy exists in dst
		dstResp := adminClient.Get(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/dst-dir", repoID))
		expectStatus(t, dstResp, http.StatusOK)

		var dstList map[string]interface{}
		decodeJSON(t, dstResp, &dstList)
		dstEntries, _ := dstList["dirent_list"].([]interface{})
		if !containsEntry(dstEntries, "name", "item-to-copy") {
			t.Error("copied item not found in dst-dir")
		}
	})
}

func TestFileDelete(t *testing.T) {
	name := fmt.Sprintf("inttest-filedelete-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)

	// Create a file
	values := url.Values{}
	createResp := adminClient.PostForm(t, fmt.Sprintf("/api2/repos/%s/file/?p=/delete-me.txt&operation=create", repoID), values)
	if createResp.StatusCode != http.StatusCreated && createResp.StatusCode != http.StatusOK {
		t.Fatalf("failed to create test file, got %d", createResp.StatusCode)
	}
	createResp.Body.Close()

	// Delete the file
	delResp := adminClient.Delete(t, fmt.Sprintf("/api2/repos/%s/file/?p=/delete-me.txt", repoID))
	expectStatus(t, delResp, http.StatusOK)
	delResp.Body.Close()

	// Verify file is gone from listing
	listResp := adminClient.Get(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/", repoID))
	expectStatus(t, listResp, http.StatusOK)

	var dirList map[string]interface{}
	decodeJSON(t, listResp, &dirList)
	entries, _ := dirList["dirent_list"].([]interface{})
	if containsEntry(entries, "name", "delete-me.txt") {
		t.Error("deleted file still found in listing")
	}
}

// TestCrossLibraryBatchCopyIncrementsBlockRefCount verifies that copying a file
// across libraries via the async-batch-copy-item endpoint correctly increments the
// shared block's ref_count to 2 (once for the original upload, once for the copy).
func TestCrossLibraryBatchCopyIncrementsBlockRefCount(t *testing.T) {
	srcName := fmt.Sprintf("inttest-batchcopy-src-%d", time.Now().UnixNano())
	dstName := fmt.Sprintf("inttest-batchcopy-dst-%d", time.Now().UnixNano())
	srcRepoID := createTestLibrary(t, adminClient, srcName)
	dstRepoID := createTestLibrary(t, adminClient, dstName)

	fileName := "batch-copy-ref-test.txt"
	fileContent := fmt.Sprintf("cross-library-batch-copy-content-%d\n", time.Now().UnixNano())

	// Upload the source file via the seafhttp upload link.
	uploadLinkResp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/upload-link/?p=/", srcRepoID))
	expectStatus(t, uploadLinkResp, http.StatusOK)
	uploadURL := strings.Trim(responseBody(t, uploadLinkResp), "\" \n\r")
	uploadFileThroughLink(t, adminClient, uploadURL, fileName, "/", fileContent)

	// Trigger async cross-library batch copy: srcRepo → dstRepo.
	copyBody := map[string]interface{}{
		"src_repo_id":     srcRepoID,
		"dst_repo_id":     dstRepoID,
		"src_parent_dir":  "/",
		"dst_parent_dir":  "/",
		"src_dirents":     []string{fileName},
		"conflict_policy": "autorename",
	}
	taskResp := adminClient.PostJSON(t, "/api/v2.1/repos/async-batch-copy-item/", copyBody)
	expectStatus(t, taskResp, http.StatusOK)
	taskResult := responseJSON(t, taskResp)
	taskID, _ := taskResult["task_id"].(string)
	if taskID == "" {
		t.Fatal("async-batch-copy-item response missing task_id")
	}

	// Poll until the task completes or 10 s timeout.
	deadline := time.Now().Add(10 * time.Second)
	done := false
	for time.Now().Before(deadline) {
		progress := responseJSON(t, adminClient.Get(t, "/api/v2.1/query-copy-move-progress/?task_id="+taskID))
		if d, _ := progress["done"].(bool); d {
			done = true
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !done {
		t.Fatal("async batch copy task did not complete within 10 s")
	}

	// File must be accessible in dstRepo.
	listResp := adminClient.Get(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/", dstRepoID))
	expectStatus(t, listResp, http.StatusOK)
	listResult := responseJSON(t, listResp)
	dstEntries, _ := listResult["dirent_list"].([]interface{})
	if !containsEntry(dstEntries, "name", fileName) {
		t.Fatalf("file %q not found in dstRepo after cross-library batch copy", fileName)
	}

	// Block ref_count must be 2: once for the original upload, once for the cross-library copy.
	session := shareProjectionDBForTest(t).Session()
	var orgID string
	if err := session.Query(`SELECT org_id FROM libraries_by_id WHERE library_id = ?`, srcRepoID).Scan(&orgID); err != nil {
		t.Fatalf("failed to resolve org_id: %v", err)
	}
	hash := sha256.Sum256([]byte(fileContent))
	blockID := hex.EncodeToString(hash[:])
	var refCount int
	if err := session.Query(`SELECT ref_count FROM blocks WHERE org_id = ? AND block_id = ?`, orgID, blockID).Scan(&refCount); err != nil {
		t.Fatalf("failed to read block ref_count for block %s: %v", blockID, err)
	}
	if refCount != 2 {
		t.Fatalf("ref_count after cross-library batch copy = %d, want 2", refCount)
	}
}
