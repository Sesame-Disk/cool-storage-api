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

// TestSameLibraryCopyKeepsBlockAliveViaSharedReference verifies that copying a
// file within the same library does NOT create a new block reference: the copy
// shares the source's content-addressed fs_id, so the same permanent
// fs:<library>:<fs_id> reference covers every copy. The block stays alive (>=1
// reference) the whole time, which is what matters for the row-per-reference
// model — there is no per-copy counter to inflate.
func TestSameLibraryCopyKeepsBlockAliveViaSharedReference(t *testing.T) {
	name := fmt.Sprintf("inttest-copy-refcount-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)

	fileName := "copy-refcount.txt"
	fileContent := fmt.Sprintf("same-library-copy-refcount-%d\n", time.Now().UnixNano())

	uploadLinkResp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/upload-link/?p=/", repoID))
	expectStatus(t, uploadLinkResp, http.StatusOK)
	uploadURL := strings.Trim(responseBody(t, uploadLinkResp), "\" \n\r")
	uploadFileThroughLink(t, adminClient, uploadURL, fileName, "/", fileContent)

	orgID := resolveOrgID(t, repoID)
	hash := sha256.Sum256([]byte(fileContent))
	blockID := hex.EncodeToString(hash[:])
	if refCount := readBlockRefCount(t, orgID, blockID); refCount != 1 {
		t.Fatalf("references after seed upload = %d, want 1", refCount)
	}

	copyResp := adminClient.PostJSON(t, "/api/v2.1/repos/"+repoID+"/file/copy/", map[string]interface{}{
		"src_path":        "/" + fileName,
		"dst_dir":         "/",
		"conflict_policy": "autorename",
	})
	expectStatus(t, copyResp, http.StatusOK)
	copyResp.Body.Close()
	// Same content → same fs_id → the existing fs_object reference is shared, not
	// duplicated. The block stays alive with its single permanent reference.
	if refCount := readBlockRefCount(t, orgID, blockID); refCount != 1 {
		t.Fatalf("references after single same-library copy = %d, want 1 (shared fs_id)", refCount)
	}

	batchCopyResp := adminClient.PostJSON(t, "/api/v2.1/repos/"+repoID+"/file/copy/", map[string]interface{}{
		"src_dir":         "/",
		"filename":        []string{fileName},
		"dst_dir":         "/",
		"conflict_policy": "autorename",
	})
	expectStatus(t, batchCopyResp, http.StatusOK)
	batchCopyResp.Body.Close()
	if refCount := readBlockRefCount(t, orgID, blockID); refCount != 1 {
		t.Fatalf("references after batch same-library copy = %d, want 1 (shared fs_id)", refCount)
	}
}

func assertSameLibraryCopyReplaceKeepsReplacedBlockUntilGC(t *testing.T, useBatch bool) {
	t.Helper()

	name := fmt.Sprintf("inttest-copy-replace-refcount-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)

	for _, dirPath := range []string{"/src", "/dst"} {
		resp := adminClient.PostJSON(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=%s", repoID, dirPath), map[string]string{})
		expectStatus(t, resp, http.StatusCreated)
		resp.Body.Close()
	}

	fileName := "replace-me.txt"
	sourceContent := fmt.Sprintf("same-library-copy-replace-source-%d\n", time.Now().UnixNano())
	replacedContent := fmt.Sprintf("same-library-copy-replace-dest-%d\n", time.Now().UnixNano())

	uploadLinkResp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/upload-link/?p=/", repoID))
	expectStatus(t, uploadLinkResp, http.StatusOK)
	uploadURL := strings.Trim(responseBody(t, uploadLinkResp), "\" \n\r")
	uploadFileThroughLink(t, adminClient, uploadURL, fileName, "/src", sourceContent)
	uploadFileThroughLink(t, adminClient, uploadURL, fileName, "/dst", replacedContent)

	orgID := resolveOrgID(t, repoID)
	sourceHash := sha256.Sum256([]byte(sourceContent))
	sourceBlockID := hex.EncodeToString(sourceHash[:])
	replacedHash := sha256.Sum256([]byte(replacedContent))
	replacedBlockID := hex.EncodeToString(replacedHash[:])

	if refCount := readBlockRefCount(t, orgID, sourceBlockID); refCount != 1 {
		t.Fatalf("source references after seed upload = %d, want 1", refCount)
	}
	if refCount := readBlockRefCount(t, orgID, replacedBlockID); refCount != 1 {
		t.Fatalf("replaced references after seed upload = %d, want 1", refCount)
	}

	var copyResp *http.Response
	if useBatch {
		copyResp = adminClient.PostJSON(t, "/api/v2.1/repos/"+repoID+"/file/copy/", map[string]interface{}{
			"src_dir":         "/src",
			"filename":        []string{fileName},
			"dst_dir":         "/dst",
			"conflict_policy": "replace",
		})
	} else {
		copyResp = adminClient.PostJSON(t, "/api/v2.1/repos/"+repoID+"/file/copy/", map[string]interface{}{
			"src_path":        "/src/" + fileName,
			"dst_dir":         "/dst",
			"conflict_policy": "replace",
		})
	}
	expectStatus(t, copyResp, http.StatusOK)
	copyResp.Body.Close()

	// Same content → shared fs_id, so the source block keeps its single reference.
	if refCount := readBlockRefCount(t, orgID, sourceBlockID); refCount != 1 {
		t.Fatalf("source references after copy replace = %d, want 1 (shared fs_id)", refCount)
	}

	// The replaced file's old fs_object stays in fs_objects (reachable from older
	// commits) until GC sweeps it, so its block is NOT decremented inline by the
	// copy-replace — storage is reclaimed later by GC, not synchronously.
	if refCount := readBlockRefCount(t, orgID, replacedBlockID); refCount != 1 {
		t.Fatalf("replaced references after copy replace = %d, want 1 (survives until GC)", refCount)
	}

	dstResp := adminClient.Get(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/dst", repoID))
	expectStatus(t, dstResp, http.StatusOK)
	var dstList map[string]interface{}
	decodeJSON(t, dstResp, &dstList)
	entries, _ := dstList["dirent_list"].([]interface{})
	if !containsEntry(entries, "name", fileName) {
		t.Fatalf("copied file %q not found in /dst after replace", fileName)
	}
	if dstResp.Body != nil {
		dstResp.Body.Close()
	}
}

func TestSameLibraryCopyReplaceKeepsReplacedBlockUntilGC(t *testing.T) {
	assertSameLibraryCopyReplaceKeepsReplacedBlockUntilGC(t, false)
}

func TestSameLibraryBatchCopyReplaceKeepsReplacedBlockUntilGC(t *testing.T) {
	assertSameLibraryCopyReplaceKeepsReplacedBlockUntilGC(t, true)
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

	// The block now has 2 permanent fs_object references: one in srcRepo (original
	// upload) and one in dstRepo — the cross-library copy created a NEW fs_object
	// there (unlike a same-library copy, which shares the fs_id).
	orgID := resolveOrgID(t, srcRepoID)
	hash := sha256.Sum256([]byte(fileContent))
	blockID := hex.EncodeToString(hash[:])
	if refCount := readBlockRefCount(t, orgID, blockID); refCount != 2 {
		t.Fatalf("references after cross-library batch copy = %d, want 2", refCount)
	}
}

// TestCrossLibraryBatchCopy_DeleteSourceBlockSurvives verifies that after a
// cross-library batch copy, deleting the source file decrements ref_count to 1
// (not 0), so the block remains accessible in the destination library and will
// NOT be garbage-collected.
func TestCrossLibraryBatchCopy_DeleteSourceBlockSurvives(t *testing.T) {
	srcName := fmt.Sprintf("inttest-copydel-src-%d", time.Now().UnixNano())
	dstName := fmt.Sprintf("inttest-copydel-dst-%d", time.Now().UnixNano())
	srcRepoID := createTestLibrary(t, adminClient, srcName)
	dstRepoID := createTestLibrary(t, adminClient, dstName)

	fileName := "survive-gc-test.txt"
	fileContent := fmt.Sprintf("survive-gc-content-%d\n", time.Now().UnixNano())

	// 1. Upload file to source library.
	uploadLinkResp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/upload-link/?p=/", srcRepoID))
	expectStatus(t, uploadLinkResp, http.StatusOK)
	uploadURL := strings.Trim(responseBody(t, uploadLinkResp), "\" \n\r")
	uploadFileThroughLink(t, adminClient, uploadURL, fileName, "/", fileContent)

	// 2. Cross-library batch copy: src → dst.
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

	// Wait for copy task to complete.
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

	// 3. Verify the block now has 2 permanent references (srcRepo + dstRepo).
	orgID := resolveOrgID(t, srcRepoID)
	hash := sha256.Sum256([]byte(fileContent))
	blockID := hex.EncodeToString(hash[:])
	if refCount := readBlockRefCount(t, orgID, blockID); refCount != 2 {
		t.Fatalf("references after copy = %d, want 2", refCount)
	}

	// 4. Delete the source file. This creates a new commit without it, but the
	// source's old fs_object stays in fs_objects until GC sweeps it — deletes no
	// longer decrement inline — so the block stays alive. What matters is that the
	// dst copy remains readable; storage for the source reference is reclaimed by GC.
	delResp := adminClient.Delete(t, fmt.Sprintf("/api2/repos/%s/file/?p=/%s", srcRepoID, fileName))
	expectStatus(t, delResp, http.StatusOK)
	delResp.Body.Close()

	if refCount := readBlockRefCount(t, orgID, blockID); refCount < 1 {
		t.Fatalf("references after deleting source file = %d, want >= 1 (block must survive for dst)", refCount)
	}

	// 6. Verify file is still accessible in destination library.
	listResp := adminClient.Get(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/", dstRepoID))
	expectStatus(t, listResp, http.StatusOK)
	listResult := responseJSON(t, listResp)
	dstEntries, _ := listResult["dirent_list"].([]interface{})
	if !containsEntry(dstEntries, "name", fileName) {
		t.Fatalf("file %q not found in dstRepo after source deletion", fileName)
	}

	// 7. Verify the file is downloadable from the destination.
	dlResp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/file/?p=/%s", dstRepoID, fileName))
	if dlResp.StatusCode != http.StatusOK {
		t.Fatalf("download link for dst file returned %d, want 200", dlResp.StatusCode)
	}
	dlResp.Body.Close()
}

// TestBatchDeleteItems_BlockSurvivesUntilGC verifies that deleting via the batch
// operation does NOT decrement block references inline: the deleted file's
// fs_object stays in fs_objects (reachable from older commits) until GC sweeps it,
// so the block keeps its reference. Storage is reclaimed by GC, not synchronously.
func TestBatchDeleteItems_BlockSurvivesUntilGC(t *testing.T) {
	name := fmt.Sprintf("inttest-batchdel-gc-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)

	fileName := "batch-del-gc-test.txt"
	fileContent := fmt.Sprintf("batch-del-gc-content-%d\n", time.Now().UnixNano())

	// 1. Upload file
	uploadLinkResp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/upload-link/?p=/", repoID))
	expectStatus(t, uploadLinkResp, http.StatusOK)
	uploadURL := strings.Trim(responseBody(t, uploadLinkResp), "\" \n\r")
	uploadFileThroughLink(t, adminClient, uploadURL, fileName, "/", fileContent)

	orgID := resolveOrgID(t, repoID)
	hash := sha256.Sum256([]byte(fileContent))
	blockID := hex.EncodeToString(hash[:])

	if refCount := readBlockRefCount(t, orgID, blockID); refCount != 1 {
		t.Fatalf("references before batch delete = %d, want 1", refCount)
	}

	// 2. Batch Delete the file
	delBody := map[string]interface{}{
		"repo_id":    repoID,
		"parent_dir": "/",
		"dirents":    []string{fileName},
	}
	delResp := adminClient.DeleteJSON(t, "/api/v2.1/repos/batch-delete-item/", delBody)
	expectStatus(t, delResp, http.StatusOK)
	delResp.Body.Close()

	// 3. The deleted file's fs_object persists until GC, so the block keeps its
	// reference — no inline decrement.
	if refCount := readBlockRefCount(t, orgID, blockID); refCount != 1 {
		t.Fatalf("references after batch delete = %d, want 1 (fs_object survives until GC)", refCount)
	}
}
