//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

type concurrentMutationResult struct {
	name   string
	status int
	body   string
	err    error
	took   time.Duration
}

func runConcurrentMutations(t *testing.T, names []string, mutate func(name string) concurrentMutationResult) []concurrentMutationResult {
	t.Helper()

	start := make(chan struct{})
	results := make(chan concurrentMutationResult, len(names))
	var wg sync.WaitGroup

	for _, name := range names {
		name := name
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- mutate(name)
		}()
	}

	close(start)
	wg.Wait()
	close(results)

	out := make([]concurrentMutationResult, 0, len(names))
	for result := range results {
		out = append(out, result)
	}
	return out
}

func expectConcurrentStatuses(t *testing.T, results []concurrentMutationResult, allowed ...int) {
	t.Helper()
	allowedSet := make(map[int]struct{}, len(allowed))
	for _, status := range allowed {
		allowedSet[status] = struct{}{}
	}
	for _, result := range results {
		if result.err != nil {
			t.Fatalf("%s failed: %v", result.name, result.err)
		}
		if _, ok := allowedSet[result.status]; !ok {
			t.Fatalf("%s status = %d, want one of %v; body=%s", result.name, result.status, allowed, result.body)
		}
	}
}

func postJSONStatus(c *testClient, path string, body interface{}) concurrentMutationResult {
	data, err := json.Marshal(body)
	if err != nil {
		return concurrentMutationResult{err: err}
	}
	req, err := http.NewRequest(http.MethodPost, c.baseURL+path, bytes.NewBuffer(data))
	if err != nil {
		return concurrentMutationResult{err: err}
	}
	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Content-Type", "application/json")
	return doStatus(c, req)
}

func postFormStatus(c *testClient, path string, values url.Values) concurrentMutationResult {
	req, err := http.NewRequest(http.MethodPost, c.baseURL+path, strings.NewReader(values.Encode()))
	if err != nil {
		return concurrentMutationResult{err: err}
	}
	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return doStatus(c, req)
}

func doStatus(c *testClient, req *http.Request) concurrentMutationResult {
	started := time.Now()
	resp, err := c.http.Do(req)
	if err != nil {
		return concurrentMutationResult{err: err, took: time.Since(started)}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return concurrentMutationResult{status: resp.StatusCode, err: err, took: time.Since(started)}
	}
	return concurrentMutationResult{status: resp.StatusCode, body: string(body), took: time.Since(started)}
}

func listDirEntries(t *testing.T, repoID, dirPath string) []interface{} {
	t.Helper()
	resp := adminClient.Get(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=%s", repoID, url.QueryEscape(dirPath)))
	expectStatus(t, resp, http.StatusOK)
	payload := responseJSON(t, resp)
	entries, _ := payload["dirent_list"].([]interface{})
	return entries
}

func expectEntriesPresent(t *testing.T, repoID, dirPath string, names []string) {
	t.Helper()
	entries := listDirEntries(t, repoID, dirPath)
	for _, name := range names {
		if !containsEntry(entries, "name", name) {
			t.Fatalf("%q missing from %s after concurrent mutation", name, dirPath)
		}
	}
}

func expectEntriesAbsent(t *testing.T, repoID, dirPath string, names []string) {
	t.Helper()
	entries := listDirEntries(t, repoID, dirPath)
	for _, name := range names {
		if containsEntry(entries, "name", name) {
			t.Fatalf("%q still present in %s after concurrent mutation", name, dirPath)
		}
	}
}

func retryNames(prefix string, count int) []string {
	names := make([]string, count)
	for i := 0; i < count; i++ {
		names[i] = fmt.Sprintf("%s-%02d", prefix, i)
	}
	return names
}

func TestConcurrentCreateDirectoryRetriesPreserveAllSiblings(t *testing.T) {
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-head-retry-mkdir-%d", time.Now().UnixNano()))
	names := retryNames("dir", 4)

	results := runConcurrentMutations(t, names, func(name string) concurrentMutationResult {
		result := postJSONStatus(adminClient, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/%s", repoID, url.QueryEscape(name)), map[string]string{})
		result.name = name
		return result
	})
	expectConcurrentStatuses(t, results, http.StatusCreated)
	expectEntriesPresent(t, repoID, "/", names)
}

func TestConcurrentCreateFileRetriesPreserveAllSiblings(t *testing.T) {
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-head-retry-createfile-%d", time.Now().UnixNano()))
	baseNames := retryNames("file", 4)
	fileNames := make([]string, len(baseNames))
	for i, name := range baseNames {
		fileNames[i] = name + ".txt"
	}

	results := runConcurrentMutations(t, fileNames, func(name string) concurrentMutationResult {
		result := postFormStatus(adminClient, fmt.Sprintf("/api2/repos/%s/file/?operation=create&p=/%s", repoID, url.QueryEscape(name)), url.Values{})
		result.name = name
		return result
	})
	expectConcurrentStatuses(t, results, http.StatusCreated, http.StatusOK)
	expectEntriesPresent(t, repoID, "/", fileNames)
}

func TestConcurrentCopyFileAutorenameRetriesPreserveAllCopies(t *testing.T) {
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-head-retry-copy-%d", time.Now().UnixNano()))

	const sourceName = "copy-source.txt"
	uploadLinkResp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/upload-link/?p=/", repoID))
	expectStatus(t, uploadLinkResp, http.StatusOK)
	uploadURL := strings.Trim(responseBody(t, uploadLinkResp), "\" \n\r")
	uploadFileThroughLink(t, adminClient, uploadURL, sourceName, "/", fmt.Sprintf("copy-source-%d\n", time.Now().UnixNano()))

	names := retryNames("copy", 4)
	results := runConcurrentMutations(t, names, func(name string) concurrentMutationResult {
		result := postJSONStatus(adminClient, "/api/v2.1/repos/"+repoID+"/file/copy/", map[string]interface{}{
			"src_path":        "/" + sourceName,
			"dst_dir":         "/",
			"conflict_policy": "autorename",
		})
		result.name = name
		return result
	})
	expectConcurrentStatuses(t, results, http.StatusOK)

	entries := listDirEntries(t, repoID, "/")
	copyCount := 0
	for _, entry := range entries {
		m, _ := entry.(map[string]interface{})
		name, _ := m["name"].(string)
		if name == sourceName || strings.HasPrefix(name, "copy-source (") {
			copyCount++
		}
	}
	want := len(names) + 1
	if copyCount != want {
		t.Fatalf("copy-source entries = %d, want %d after concurrent autorename copies", copyCount, want)
	}
}

func TestConcurrentBatchDeleteRetriesRemoveAllDirents(t *testing.T) {
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-head-retry-batchdel-%d", time.Now().UnixNano()))
	names := retryNames("delete-me", 4)

	uploadLinkResp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/upload-link/?p=/", repoID))
	expectStatus(t, uploadLinkResp, http.StatusOK)
	uploadURL := strings.Trim(responseBody(t, uploadLinkResp), "\" \n\r")
	for _, name := range names {
		uploadFileThroughLink(t, adminClient, uploadURL, name+".txt", "/", fmt.Sprintf("%s-%d\n", name, time.Now().UnixNano()))
	}

	fileNames := make([]string, len(names))
	for i, name := range names {
		fileNames[i] = name + ".txt"
	}
	results := runConcurrentMutations(t, fileNames, func(name string) concurrentMutationResult {
		status, body, err := deleteJSONWithoutFatal(adminClient, "/api/v2.1/repos/batch-delete-item/", map[string]interface{}{
			"repo_id":    repoID,
			"parent_dir": "/",
			"dirents":    []string{name},
		})
		return concurrentMutationResult{name: name, status: status, body: body, err: err}
	})
	expectConcurrentStatuses(t, results, http.StatusOK)
	expectEntriesAbsent(t, repoID, "/", fileNames)
}

func TestConcurrentRevertFileRetriesPreserveAllRestoredFiles(t *testing.T) {
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-head-retry-revertfile-%d", time.Now().UnixNano()))
	fileNames := make([]string, 4)
	for i, name := range retryNames("restore-file", len(fileNames)) {
		fileNames[i] = name + ".txt"
		resp := adminClient.PostForm(t, fmt.Sprintf("/api2/repos/%s/file/?operation=create&p=/%s", repoID, url.QueryEscape(fileNames[i])), url.Values{})
		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
			t.Fatalf("create %s status = %d; body=%s", fileNames[i], resp.StatusCode, responseBody(t, resp))
		}
		resp.Body.Close()
	}
	restoreCommit := getRepoHeadCommit(t, adminClient, repoID)
	if restoreCommit == "" {
		t.Fatal("could not resolve restore commit")
	}

	deleteResp := adminClient.DeleteJSON(t, "/api/v2.1/repos/batch-delete-item/", map[string]interface{}{
		"repo_id":    repoID,
		"parent_dir": "/",
		"dirents":    fileNames,
	})
	expectStatus(t, deleteResp, http.StatusOK)
	deleteResp.Body.Close()
	expectEntriesAbsent(t, repoID, "/", fileNames)

	results := runConcurrentMutations(t, fileNames, func(name string) concurrentMutationResult {
		result := postFormStatus(adminClient, fmt.Sprintf("/api/v2.1/repos/%s/file/?operation=revert&p=/%s", repoID, url.QueryEscape(name)), url.Values{
			"commit_id":       {restoreCommit},
			"conflict_policy": {"replace"},
		})
		result.name = name
		return result
	})
	expectConcurrentStatuses(t, results, http.StatusOK)
	expectEntriesPresent(t, repoID, "/", fileNames)
}

func TestConcurrentRevertDirectoryRetriesPreserveAllRestoredDirs(t *testing.T) {
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-head-retry-revertdir-%d", time.Now().UnixNano()))
	dirNames := retryNames("restore-dir", 4)
	for _, name := range dirNames {
		resp := adminClient.PostJSON(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/%s", repoID, url.QueryEscape(name)), map[string]string{})
		expectStatus(t, resp, http.StatusCreated)
		resp.Body.Close()
	}
	restoreCommit := getRepoHeadCommit(t, adminClient, repoID)
	if restoreCommit == "" {
		t.Fatal("could not resolve restore commit")
	}

	deleteResp := adminClient.DeleteJSON(t, "/api/v2.1/repos/batch-delete-item/", map[string]interface{}{
		"repo_id":    repoID,
		"parent_dir": "/",
		"dirents":    dirNames,
	})
	expectStatus(t, deleteResp, http.StatusOK)
	deleteResp.Body.Close()
	expectEntriesAbsent(t, repoID, "/", dirNames)

	results := runConcurrentMutations(t, dirNames, func(name string) concurrentMutationResult {
		result := postFormStatus(adminClient, fmt.Sprintf("/api/v2.1/repos/%s/dir/?operation=revert&p=/%s", repoID, url.QueryEscape(name)), url.Values{
			"commit_id":       {restoreCommit},
			"conflict_policy": {"replace"},
		})
		result.name = name
		return result
	})
	expectConcurrentStatuses(t, results, http.StatusOK)
	expectEntriesPresent(t, repoID, "/", dirNames)
}
