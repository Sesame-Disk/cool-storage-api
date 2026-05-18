//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"
)

// TestSameRepoMoveIsAtomic exercises processSameRepoMove via sync-batch-move-item.
// Before the single-commit atomic move, the implementation ran two separate commits
// (destination add then source removal), leaving a transient state where the file
// appeared in both directories. After the change, both operations happen in one
// commit, so the source must be empty the moment the destination contains the file.
func TestSameRepoMoveIsAtomic(t *testing.T) {
	name := fmt.Sprintf("inttest-same-repo-move-atomic-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)

	// Create destination directory.
	resp := adminClient.PostJSON(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/dst", repoID), map[string]string{})
	expectStatus(t, resp, http.StatusCreated)
	resp.Body.Close()

	// Create source file at root.
	resp = adminClient.PostForm(t, fmt.Sprintf("/api2/repos/%s/file/?p=/moveme.txt&operation=create", repoID), url.Values{})
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("failed to create source file: %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Move it to /dst.
	moveBody := map[string]interface{}{
		"src_repo_id":    repoID,
		"src_parent_dir": "/",
		"dst_repo_id":    repoID,
		"dst_parent_dir": "/dst",
		"src_dirents":    []string{"moveme.txt"},
	}
	moveResp := adminClient.PostJSON(t, "/api/v2.1/repos/sync-batch-move-item/", moveBody)
	expectStatus(t, moveResp, http.StatusOK)
	moveResp.Body.Close()

	// Root must no longer contain moveme.txt.
	rootResp := adminClient.Get(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/", repoID))
	expectStatus(t, rootResp, http.StatusOK)
	var rootList map[string]interface{}
	decodeJSON(t, rootResp, &rootList)
	rootEntries, _ := rootList["dirent_list"].([]interface{})
	if containsEntry(rootEntries, "name", "moveme.txt") {
		t.Error("file still present at source after same-repo move (atomicity violated)")
	}

	// /dst must contain moveme.txt.
	dstResp := adminClient.Get(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/dst", repoID))
	expectStatus(t, dstResp, http.StatusOK)
	var dstList map[string]interface{}
	decodeJSON(t, dstResp, &dstList)
	dstEntries, _ := dstList["dirent_list"].([]interface{})
	if !containsEntry(dstEntries, "name", "moveme.txt") {
		t.Error("moved file not found at destination")
	}
}

// TestSameRepoMoveNestedDirectory exercises updateDirectoryAtPathFromRoot's tree
// rebuild recursion: the source is a directory containing nested children, and
// the destination is a sibling directory. The rebuild must preserve every
// descendant under the new path.
func TestSameRepoMoveNestedDirectory(t *testing.T) {
	name := fmt.Sprintf("inttest-same-repo-move-nested-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)

	// Build /parent/child/ tree at root, plus a file under child.
	for _, p := range []string{"/parent", "/parent/child", "/target"} {
		resp := adminClient.PostJSON(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=%s", repoID, p), map[string]string{})
		expectStatus(t, resp, http.StatusCreated)
		resp.Body.Close()
	}
	resp := adminClient.PostForm(t, fmt.Sprintf("/api2/repos/%s/file/?p=/parent/child/leaf.txt&operation=create", repoID), url.Values{})
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("failed to create nested leaf file: %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Move /parent -> /target/parent.
	moveBody := map[string]interface{}{
		"src_repo_id":    repoID,
		"src_parent_dir": "/",
		"dst_repo_id":    repoID,
		"dst_parent_dir": "/target",
		"src_dirents":    []string{"parent"},
	}
	moveResp := adminClient.PostJSON(t, "/api/v2.1/repos/sync-batch-move-item/", moveBody)
	expectStatus(t, moveResp, http.StatusOK)
	moveResp.Body.Close()

	// Root must no longer contain "parent".
	rootResp := adminClient.Get(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/", repoID))
	expectStatus(t, rootResp, http.StatusOK)
	var rootList map[string]interface{}
	decodeJSON(t, rootResp, &rootList)
	rootEntries, _ := rootList["dirent_list"].([]interface{})
	if containsEntry(rootEntries, "name", "parent") {
		t.Error("source directory still present at root after move")
	}

	// /target must contain "parent" now.
	targetResp := adminClient.Get(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/target", repoID))
	expectStatus(t, targetResp, http.StatusOK)
	var targetList map[string]interface{}
	decodeJSON(t, targetResp, &targetList)
	targetEntries, _ := targetList["dirent_list"].([]interface{})
	if !containsEntry(targetEntries, "name", "parent") {
		t.Error("moved directory not found at /target")
	}

	// Descendants must still be reachable under the new path: /target/parent/child/leaf.txt.
	leafDirResp := adminClient.Get(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/target/parent/child", repoID))
	expectStatus(t, leafDirResp, http.StatusOK)
	var leafDirList map[string]interface{}
	decodeJSON(t, leafDirResp, &leafDirList)
	leafDirEntries, _ := leafDirList["dirent_list"].([]interface{})
	if !containsEntry(leafDirEntries, "name", "leaf.txt") {
		t.Error("nested leaf.txt not reachable under /target/parent/child after move")
	}
}

// TestSameRepoMoveDirectoryIntoItselfRejected exercises the isPathWithin guard
// inside processSameRepoMove. Without it, moving a directory under itself would
// create an fs_object cycle and corrupt the repo metadata.
func TestSameRepoMoveDirectoryIntoItselfRejected(t *testing.T) {
	name := fmt.Sprintf("inttest-same-repo-move-cycle-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)

	// Create /loop directory.
	resp := adminClient.PostJSON(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/loop", repoID), map[string]string{})
	expectStatus(t, resp, http.StatusCreated)
	resp.Body.Close()

	// Attempt to move /loop -> /loop (i.e. into itself).
	moveBody := map[string]interface{}{
		"src_repo_id":    repoID,
		"src_parent_dir": "/",
		"dst_repo_id":    repoID,
		"dst_parent_dir": "/loop",
		"src_dirents":    []string{"loop"},
	}
	moveResp := adminClient.PostJSON(t, "/api/v2.1/repos/sync-batch-move-item/", moveBody)
	if moveResp.StatusCode == http.StatusOK {
		moveResp.Body.Close()
		t.Fatal("expected move-into-itself to fail, got 200 OK")
	}
	moveResp.Body.Close()

	// /loop must still exist intact at root.
	rootResp := adminClient.Get(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/", repoID))
	expectStatus(t, rootResp, http.StatusOK)
	var rootList map[string]interface{}
	decodeJSON(t, rootResp, &rootList)
	rootEntries, _ := rootList["dirent_list"].([]interface{})
	if !containsEntry(rootEntries, "name", "loop") {
		t.Error("/loop disappeared after rejected move-into-itself")
	}

	// /loop should still be readable and empty (no cycle introduced).
	loopResp := adminClient.Get(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/loop", repoID))
	expectStatus(t, loopResp, http.StatusOK)
	loopResp.Body.Close()
}

// TestSameRepoMoveDirectoryIntoDescendantRejected covers the deeper variant of
// the cycle case: moving /a into /a/sub. The guard must catch this too, not
// just the same-path case.
func TestSameRepoMoveDirectoryIntoDescendantRejected(t *testing.T) {
	name := fmt.Sprintf("inttest-same-repo-move-descendant-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)

	for _, p := range []string{"/outer", "/outer/inner"} {
		resp := adminClient.PostJSON(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=%s", repoID, p), map[string]string{})
		expectStatus(t, resp, http.StatusCreated)
		resp.Body.Close()
	}

	moveBody := map[string]interface{}{
		"src_repo_id":    repoID,
		"src_parent_dir": "/",
		"dst_repo_id":    repoID,
		"dst_parent_dir": "/outer/inner",
		"src_dirents":    []string{"outer"},
	}
	moveResp := adminClient.PostJSON(t, "/api/v2.1/repos/sync-batch-move-item/", moveBody)
	if moveResp.StatusCode == http.StatusOK {
		moveResp.Body.Close()
		t.Fatal("expected move-into-descendant to fail, got 200 OK")
	}
	moveResp.Body.Close()

	// /outer must still exist at root with /outer/inner reachable.
	innerResp := adminClient.Get(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/outer/inner", repoID))
	expectStatus(t, innerResp, http.StatusOK)
	innerResp.Body.Close()
}

// TestSameRepoMoveReplacePolicy verifies that when an item with the same name
// exists at the destination and the request opts into "replace", the source is
// removed, the destination ends up with only one entry, and the replaced item
// is gone. This exercises the replacedEntry branch inside processSameRepoMove.
func TestSameRepoMoveReplacePolicy(t *testing.T) {
	name := fmt.Sprintf("inttest-same-repo-move-replace-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)

	resp := adminClient.PostJSON(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/dst", repoID), map[string]string{})
	expectStatus(t, resp, http.StatusCreated)
	resp.Body.Close()

	// Create same-named file at both source and destination.
	resp = adminClient.PostForm(t, fmt.Sprintf("/api2/repos/%s/file/?p=/dup.txt&operation=create", repoID), url.Values{})
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("failed to create source file: %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp = adminClient.PostForm(t, fmt.Sprintf("/api2/repos/%s/file/?p=/dst/dup.txt&operation=create", repoID), url.Values{})
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("failed to create destination file: %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Move with replace.
	moveBody := map[string]interface{}{
		"src_repo_id":     repoID,
		"src_parent_dir":  "/",
		"dst_repo_id":     repoID,
		"dst_parent_dir":  "/dst",
		"src_dirents":     []string{"dup.txt"},
		"conflict_policy": "replace",
	}
	moveResp := adminClient.PostJSON(t, "/api/v2.1/repos/sync-batch-move-item/", moveBody)
	expectStatus(t, moveResp, http.StatusOK)
	moveResp.Body.Close()

	// Root must no longer have dup.txt.
	rootResp := adminClient.Get(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/", repoID))
	expectStatus(t, rootResp, http.StatusOK)
	var rootList map[string]interface{}
	decodeJSON(t, rootResp, &rootList)
	rootEntries, _ := rootList["dirent_list"].([]interface{})
	if containsEntry(rootEntries, "name", "dup.txt") {
		t.Error("source dup.txt still present at root after replace-move")
	}

	// /dst must have exactly one dup.txt.
	dstResp := adminClient.Get(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/dst", repoID))
	expectStatus(t, dstResp, http.StatusOK)
	var dstList map[string]interface{}
	decodeJSON(t, dstResp, &dstList)
	dstEntries, _ := dstList["dirent_list"].([]interface{})
	dupCount := 0
	for _, entry := range dstEntries {
		m, _ := entry.(map[string]interface{})
		if m == nil {
			continue
		}
		if n, _ := m["name"].(string); n == "dup.txt" {
			dupCount++
		}
	}
	if dupCount != 1 {
		t.Errorf("expected exactly one dup.txt at /dst after replace, got %d", dupCount)
	}
}
