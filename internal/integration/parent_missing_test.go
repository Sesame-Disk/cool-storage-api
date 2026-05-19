//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func expectErrorResponseContains(t *testing.T, resp *http.Response, expectedStatus int, wantSubstrings ...string) string {
	t.Helper()

	if resp.StatusCode != expectedStatus {
		body := responseBody(t, resp)
		t.Fatalf("status = %d, want %d; body=%s", resp.StatusCode, expectedStatus, body)
	}

	payload := responseJSON(t, resp)
	errorMsg, _ := payload["error"].(string)
	if errorMsg == "" {
		t.Fatalf("expected error payload, got %#v", payload)
	}

	for _, want := range wantSubstrings {
		if !strings.Contains(errorMsg, want) {
			t.Fatalf("error = %q, want substring %q", errorMsg, want)
		}
	}

	return errorMsg
}

func TestCreateDirectoryMissingNestedParentReturnsNotFound(t *testing.T) {
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-parent-missing-create-%d", time.Now().UnixNano()))
	missingRoot := fmt.Sprintf("missing-create-%d", time.Now().UnixNano())
	missingPath := "/" + missingRoot + "/child/newdir"

	resp := adminClient.PostJSON(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=%s", repoID, url.QueryEscape(missingPath)), map[string]string{})
	expectErrorResponseContains(t, resp, http.StatusNotFound, "parent directory not found", "directory not found", missingRoot)
}

func TestBatchDeleteItemsMissingNestedParentReturnsNotFound(t *testing.T) {
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-parent-missing-batchdel-%d", time.Now().UnixNano()))
	missingRoot := fmt.Sprintf("missing-delete-%d", time.Now().UnixNano())
	missingParent := "/" + missingRoot + "/child"

	resp := adminClient.DeleteJSON(t, "/api/v2.1/repos/batch-delete-item/", map[string]interface{}{
		"repo_id":    repoID,
		"parent_dir": missingParent,
		"dirents":    []string{"ghost.txt"},
	})
	expectErrorResponseContains(t, resp, http.StatusNotFound, "parent directory not found", "directory not found", missingRoot)
}

func TestRevertFileMissingNestedParentReturnsNotFound(t *testing.T) {
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-parent-missing-revertfile-%d", time.Now().UnixNano()))
	rootDir := fmt.Sprintf("missing-revert-file-%d", time.Now().UnixNano())
	nestedDir := "/" + rootDir + "/inner"
	filePath := nestedDir + "/file.txt"

	resp := adminClient.PostJSON(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=%s", repoID, url.QueryEscape("/"+rootDir)), map[string]string{})
	expectStatus(t, resp, http.StatusCreated)
	resp.Body.Close()

	resp = adminClient.PostJSON(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=%s", repoID, url.QueryEscape(nestedDir)), map[string]string{})
	expectStatus(t, resp, http.StatusCreated)
	resp.Body.Close()

	resp = adminClient.PostForm(t, fmt.Sprintf("/api2/repos/%s/file/?operation=create&p=%s", repoID, url.QueryEscape(filePath)), url.Values{})
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body := responseBody(t, resp)
		t.Fatalf("create file status = %d, want 200 or 201; body=%s", resp.StatusCode, body)
	}
	resp.Body.Close()

	restoreCommit := getRepoHeadCommit(t, adminClient, repoID)
	if restoreCommit == "" {
		t.Fatal("could not resolve restore commit")
	}

	resp = adminClient.DeleteJSON(t, "/api/v2.1/repos/batch-delete-item/", map[string]interface{}{
		"repo_id":    repoID,
		"parent_dir": "/",
		"dirents":    []string{rootDir},
	})
	expectStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	expectEntriesAbsent(t, repoID, "/", []string{rootDir})

	resp = adminClient.PostForm(t, fmt.Sprintf("/api/v2.1/repos/%s/file/?operation=revert&p=%s", repoID, url.QueryEscape(filePath)), url.Values{
		"commit_id": {restoreCommit},
	})
	expectErrorResponseContains(t, resp, http.StatusNotFound, "parent directory not found", "directory not found", rootDir)
}

func TestRevertDirectoryMissingNestedParentReturnsNotFound(t *testing.T) {
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-parent-missing-revertdir-%d", time.Now().UnixNano()))
	rootDir := fmt.Sprintf("missing-revert-dir-%d", time.Now().UnixNano())
	middleDir := "/" + rootDir + "/middle"
	nestedDir := middleDir + "/inner"

	resp := adminClient.PostJSON(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=%s", repoID, url.QueryEscape("/"+rootDir)), map[string]string{})
	expectStatus(t, resp, http.StatusCreated)
	resp.Body.Close()

	resp = adminClient.PostJSON(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=%s", repoID, url.QueryEscape(middleDir)), map[string]string{})
	expectStatus(t, resp, http.StatusCreated)
	resp.Body.Close()

	resp = adminClient.PostJSON(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=%s", repoID, url.QueryEscape(nestedDir)), map[string]string{})
	expectStatus(t, resp, http.StatusCreated)
	resp.Body.Close()

	restoreCommit := getRepoHeadCommit(t, adminClient, repoID)
	if restoreCommit == "" {
		t.Fatal("could not resolve restore commit")
	}

	resp = adminClient.DeleteJSON(t, "/api/v2.1/repos/batch-delete-item/", map[string]interface{}{
		"repo_id":    repoID,
		"parent_dir": "/",
		"dirents":    []string{rootDir},
	})
	expectStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	expectEntriesAbsent(t, repoID, "/", []string{rootDir})

	resp = adminClient.PostForm(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?operation=revert&p=%s", repoID, url.QueryEscape(nestedDir)), url.Values{
		"commit_id": {restoreCommit},
	})
	expectErrorResponseContains(t, resp, http.StatusNotFound, "parent directory not found", "directory not found", rootDir)
}

func TestRevertFileInNestedParentKeepsAncestorTreeReachable(t *testing.T) {
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-revert-nested-file-%d", time.Now().UnixNano()))
	rootDir := fmt.Sprintf("nested-file-root-%d", time.Now().UnixNano())
	nestedDir := "/" + rootDir + "/inner"
	fileName := "restore-me.txt"
	filePath := nestedDir + "/" + fileName

	for _, dirPath := range []string{"/" + rootDir, nestedDir} {
		resp := adminClient.PostJSON(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=%s", repoID, url.QueryEscape(dirPath)), map[string]string{})
		expectStatus(t, resp, http.StatusCreated)
		resp.Body.Close()
	}

	resp := adminClient.PostForm(t, fmt.Sprintf("/api2/repos/%s/file/?operation=create&p=%s", repoID, url.QueryEscape(filePath)), url.Values{})
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body := responseBody(t, resp)
		t.Fatalf("create nested file status = %d, want 200 or 201; body=%s", resp.StatusCode, body)
	}
	resp.Body.Close()

	restoreCommit := getRepoHeadCommit(t, adminClient, repoID)
	if restoreCommit == "" {
		t.Fatal("could not resolve restore commit")
	}

	resp = adminClient.DeleteJSON(t, "/api/v2.1/repos/batch-delete-item/", map[string]interface{}{
		"repo_id":    repoID,
		"parent_dir": nestedDir,
		"dirents":    []string{fileName},
	})
	expectStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	expectEntriesAbsent(t, repoID, nestedDir, []string{fileName})

	resp = adminClient.PostForm(t, fmt.Sprintf("/api/v2.1/repos/%s/file/?operation=revert&p=%s", repoID, url.QueryEscape(filePath)), url.Values{
		"commit_id":       {restoreCommit},
		"conflict_policy": {"replace"},
	})
	expectStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	expectEntriesPresent(t, repoID, "/", []string{rootDir})
	expectEntriesPresent(t, repoID, "/"+rootDir, []string{"inner"})
	expectEntriesPresent(t, repoID, nestedDir, []string{fileName})
}

func TestRevertDirectoryInNestedParentKeepsAncestorTreeReachable(t *testing.T) {
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-revert-nested-dir-%d", time.Now().UnixNano()))
	rootDir := fmt.Sprintf("nested-dir-root-%d", time.Now().UnixNano())
	middleDir := "/" + rootDir + "/middle"
	nestedName := "restore-dir"
	nestedDir := middleDir + "/" + nestedName

	for _, dirPath := range []string{"/" + rootDir, middleDir, nestedDir} {
		resp := adminClient.PostJSON(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=%s", repoID, url.QueryEscape(dirPath)), map[string]string{})
		expectStatus(t, resp, http.StatusCreated)
		resp.Body.Close()
	}

	restoreCommit := getRepoHeadCommit(t, adminClient, repoID)
	if restoreCommit == "" {
		t.Fatal("could not resolve restore commit")
	}

	resp := adminClient.DeleteJSON(t, "/api/v2.1/repos/batch-delete-item/", map[string]interface{}{
		"repo_id":    repoID,
		"parent_dir": middleDir,
		"dirents":    []string{nestedName},
	})
	expectStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	expectEntriesAbsent(t, repoID, middleDir, []string{nestedName})

	resp = adminClient.PostForm(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?operation=revert&p=%s", repoID, url.QueryEscape(nestedDir)), url.Values{
		"commit_id":       {restoreCommit},
		"conflict_policy": {"replace"},
	})
	expectStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	expectEntriesPresent(t, repoID, "/", []string{rootDir})
	expectEntriesPresent(t, repoID, "/"+rootDir, []string{"middle"})
	expectEntriesPresent(t, repoID, middleDir, []string{nestedName})
}
