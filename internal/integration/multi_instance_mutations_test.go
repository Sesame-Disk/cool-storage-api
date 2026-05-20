//go:build integration

package integration

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func multiInstanceRequireAdminClients(t *testing.T, minCount int) []*testClient {
	t.Helper()

	clients := []*testClient{adminClient}
	for _, envKey := range []string{"SESAMEFS_URL_2", "SESAMEFS_URL_3"} {
		baseURL := strings.TrimSpace(os.Getenv(envKey))
		if baseURL == "" {
			continue
		}
		if err := verifyIntegrationAuth(baseURL, "dev-token-admin"); err != nil {
			t.Fatalf("%s auth probe failed: %v", envKey, err)
		}
		clients = append(clients, newTestClient(baseURL, "dev-token-admin"))
	}
	if len(clients) < minCount {
		t.Skipf("multi-instance suite requires at least %d reachable SesameFS nodes, got %d", minCount, len(clients))
	}
	return clients
}

func multiInstanceRequireUserClients(t *testing.T, minCount int) []*testClient {
	t.Helper()

	clients := []*testClient{userClient}
	for _, envKey := range []string{"SESAMEFS_URL_2", "SESAMEFS_URL_3"} {
		baseURL := strings.TrimSpace(os.Getenv(envKey))
		if baseURL == "" {
			continue
		}
		if err := verifyIntegrationAuth(baseURL, "dev-token-user"); err != nil {
			t.Fatalf("%s auth probe failed: %v", envKey, err)
		}
		clients = append(clients, newTestClient(baseURL, "dev-token-user"))
	}
	if len(clients) < minCount {
		t.Skipf("multi-instance suite requires at least %d reachable SesameFS nodes, got %d", minCount, len(clients))
	}
	return clients
}

func multiInstanceRunConcurrentMutations(t *testing.T, clients []*testClient, names []string, mutate func(*testClient, string, int) concurrentMutationResult) []concurrentMutationResult {
	t.Helper()

	start := make(chan struct{})
	results := make(chan concurrentMutationResult, len(names))
	var wg sync.WaitGroup
	for idx, name := range names {
		idx := idx
		name := name
		client := clients[idx%len(clients)]
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results <- mutate(client, name, idx)
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

func multiInstanceDeleteStatus(c *testClient, path string) concurrentMutationResult {
	req, err := http.NewRequest(http.MethodDelete, c.baseURL+path, nil)
	if err != nil {
		return concurrentMutationResult{status: -1, body: err.Error()}
	}
	req.Header.Set("Authorization", "Token "+c.token)
	return doStatus(c, req)
}

func multiInstanceDeleteJSONStatus(c *testClient, path string, body interface{}) concurrentMutationResult {
	data, err := json.Marshal(body)
	if err != nil {
		return concurrentMutationResult{status: -1, body: err.Error()}
	}
	req, err := http.NewRequest(http.MethodDelete, c.baseURL+path, bytes.NewBuffer(data))
	if err != nil {
		return concurrentMutationResult{status: -1, body: err.Error()}
	}
	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Content-Type", "application/json")
	return doStatus(c, req)
}

func multiInstanceExpectStatus(t *testing.T, resp *http.Response, allowed ...int) {
	t.Helper()
	for _, status := range allowed {
		if resp.StatusCode == status {
			return
		}
	}
	body := responseBody(t, resp)
	t.Fatalf("unexpected status %d, allowed=%v; body=%s", resp.StatusCode, allowed, body)
}

func multiInstanceFileNames(prefix string, count int) []string {
	names := retryNames(prefix, count)
	for idx := range names {
		names[idx] += ".txt"
	}
	return names
}

func multiInstanceUpdateBranchStatus(c *testClient, repoID, head string) concurrentMutationResult {
	req, err := http.NewRequest(http.MethodPost, c.baseURL+fmt.Sprintf("/seafhttp/repo/%s/update-branch?head=%s", repoID, url.QueryEscape(head)), nil)
	if err != nil {
		return concurrentMutationResult{status: -1, body: err.Error()}
	}
	req.Header.Set("Authorization", "Token "+c.token)
	return doStatus(c, req)
}

func multiInstancePutCommitHeadStatus(c *testClient, repoID, head string) concurrentMutationResult {
	req, err := http.NewRequest(http.MethodPut, c.baseURL+fmt.Sprintf("/seafhttp/repo/%s/commit/HEAD?head=%s", repoID, url.QueryEscape(head)), nil)
	if err != nil {
		return concurrentMutationResult{status: -1, body: err.Error()}
	}
	req.Header.Set("Authorization", "Token "+c.token)
	return doStatus(c, req)
}

func multiInstanceUploadLinks(t *testing.T, clients []*testClient, repoID, parentDir string) []string {
	t.Helper()

	links := make([]string, len(clients))
	for idx, client := range clients {
		links[idx] = rewriteUploadURLHost(getUploadLink(t, client, repoID, parentDir), client.baseURL)
	}
	return links
}

func TestMultiInstanceCreateFilePreservesAllEntries(t *testing.T) {
	clients := multiInstanceRequireAdminClients(t, 3)
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-multi-create-file-%d", time.Now().UnixNano()))
	fileNames := multiInstanceFileNames("multi-file", 9)

	results := multiInstanceRunConcurrentMutations(t, clients, fileNames, func(c *testClient, name string, _ int) concurrentMutationResult {
		result := postFormStatus(c, fmt.Sprintf("/api2/repos/%s/file/?p=/%s&operation=create", repoID, url.QueryEscape(name)), url.Values{})
		result.name = name
		return result
	})
	expectConcurrentStatuses(t, results, http.StatusCreated, http.StatusOK)
	expectEntriesPresent(t, repoID, "/", fileNames)
}

func TestMultiInstanceUpdateBranchConvergesWhenParentPromotionWinsDuringRetry(t *testing.T) {
	clients := multiInstanceRequireAdminClients(t, 2)
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-multi-update-branch-%d", time.Now().UnixNano()))
	session := shareProjectionDBForTest(t).Session()

	initial := readLibrarySyncHeadState(t, session, repoID)
	intermediateHead := fmt.Sprintf("%040x", time.Now().UnixNano())
	finalHead := fmt.Sprintf("%040x", time.Now().UnixNano()+1)
	insertSyntheticCommitForTest(t, session, repoID, intermediateHead, initial.HeadCommitID, initial.RootFSID, "integration multi-instance update-branch intermediate")
	insertSyntheticCommitForTest(t, session, repoID, finalHead, intermediateHead, initial.RootFSID, "integration multi-instance update-branch final")
	t.Cleanup(func() {
		for _, commitID := range []string{intermediateHead, finalHead} {
			if err := session.Query(`DELETE FROM commits WHERE library_id = ? AND commit_id = ?`, repoID, commitID).Exec(); err != nil {
				t.Errorf("cleanup synthetic commit %s failed: %v", commitID, err)
			}
		}
	})

	results := make(chan concurrentMutationResult, 2)
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		result := multiInstanceUpdateBranchStatus(clients[1], repoID, finalHead)
		result.name = "promote-final"
		results <- result
	}()

	time.Sleep(10 * time.Millisecond)

	wg.Add(1)
	go func() {
		defer wg.Done()
		result := multiInstanceUpdateBranchStatus(clients[0], repoID, intermediateHead)
		result.name = "promote-intermediate"
		results <- result
	}()

	wg.Wait()
	close(results)

	collected := make([]concurrentMutationResult, 0, 2)
	for result := range results {
		collected = append(collected, result)
	}
	expectConcurrentStatuses(t, collected, http.StatusOK)

	waitForIntegrationCondition(t, "multi-instance update-branch to converge to the final chained head", func() bool {
		current := readLibrarySyncHeadState(t, session, repoID)
		return current.HeadCommitID == finalHead &&
			current.LookupHeadCommitID == finalHead &&
			current.ProjectionUpdatedAt.Equal(current.UpdatedAt)
	})
}

func TestMultiInstancePutCommitHeadConvergesWhenParentPromotionWinsDuringRetry(t *testing.T) {
	clients := multiInstanceRequireAdminClients(t, 2)
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-multi-put-head-%d", time.Now().UnixNano()))
	session := shareProjectionDBForTest(t).Session()

	initial := readLibrarySyncHeadState(t, session, repoID)
	intermediateHead := fmt.Sprintf("%040x", time.Now().UnixNano())
	finalHead := fmt.Sprintf("%040x", time.Now().UnixNano()+1)
	insertSyntheticCommitForTest(t, session, repoID, intermediateHead, initial.HeadCommitID, initial.RootFSID, "integration multi-instance put head intermediate")
	insertSyntheticCommitForTest(t, session, repoID, finalHead, intermediateHead, initial.RootFSID, "integration multi-instance put head final")
	t.Cleanup(func() {
		for _, commitID := range []string{intermediateHead, finalHead} {
			if err := session.Query(`DELETE FROM commits WHERE library_id = ? AND commit_id = ?`, repoID, commitID).Exec(); err != nil {
				t.Errorf("cleanup synthetic commit %s failed: %v", commitID, err)
			}
		}
	})

	results := make(chan concurrentMutationResult, 2)
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		result := multiInstancePutCommitHeadStatus(clients[1], repoID, finalHead)
		result.name = "promote-final"
		results <- result
	}()

	time.Sleep(10 * time.Millisecond)

	wg.Add(1)
	go func() {
		defer wg.Done()
		result := multiInstancePutCommitHeadStatus(clients[0], repoID, intermediateHead)
		result.name = "promote-intermediate"
		results <- result
	}()

	wg.Wait()
	close(results)

	collected := make([]concurrentMutationResult, 0, 2)
	for result := range results {
		collected = append(collected, result)
	}
	expectConcurrentStatuses(t, collected, http.StatusOK)

	waitForIntegrationCondition(t, "multi-instance put commit HEAD to converge to the final chained head", func() bool {
		current := readLibrarySyncHeadState(t, session, repoID)
		return current.HeadCommitID == finalHead &&
			current.LookupHeadCommitID == finalHead &&
			current.ProjectionUpdatedAt.Equal(current.UpdatedAt)
	})
}

func TestMultiInstanceSeafHTTPUploadWhileRenamingNoLostFiles(t *testing.T) {
	clients := multiInstanceRequireAdminClients(t, 3)
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-multi-upload-rename-%d", time.Now().UnixNano()))
	uploadURLs := multiInstanceUploadLinks(t, clients, repoID, "/")
	uploadFileThroughLink(t, adminClient, uploadURLs[0], "anchor.txt", "/", "anchor content\n")

	uploadNames := multiInstanceFileNames("multi-upload-rename", 9)
	start := make(chan struct{})
	results := make(chan concurrentMutationResult, len(uploadNames)+1)
	var wg sync.WaitGroup

	for idx, name := range uploadNames {
		idx := idx
		name := name
		client := clients[idx%len(clients)]
		uploadURL := uploadURLs[idx%len(uploadURLs)]
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result := uploadViaLinkConcurrent(client, uploadURL, name, "/", fmt.Sprintf("multi upload %s at %d\n", name, time.Now().UnixNano()))
			result.name = name
			results <- result
		}()
	}

	renameClient := clients[len(clients)-1]
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		result := postJSONStatus(renameClient, fmt.Sprintf("/api2/repos/%s/file/?p=/anchor.txt&operation=rename", repoID), map[string]string{
			"newname": "anchor-renamed.txt",
		})
		result.name = "rename-anchor"
		results <- result
	}()

	close(start)
	wg.Wait()
	close(results)

	uploadResults := make([]concurrentMutationResult, 0, len(uploadNames))
	for result := range results {
		if result.name == "rename-anchor" {
			if result.err != nil {
				t.Fatalf("rename failed: %v", result.err)
			}
			if result.status != http.StatusOK {
				t.Fatalf("rename status = %d body=%s", result.status, result.body)
			}
			continue
		}
		uploadResults = append(uploadResults, result)
	}

	expectConcurrentStatuses(t, uploadResults, http.StatusOK, http.StatusCreated)
	expectEntriesPresent(t, repoID, "/", uploadNames)
	expectEntriesPresent(t, repoID, "/", []string{"anchor-renamed.txt"})
	expectEntriesAbsent(t, repoID, "/", []string{"anchor.txt"})
}

func TestMultiInstanceV2UploadWhileDeletingNoLostFiles(t *testing.T) {
	clients := multiInstanceRequireAdminClients(t, 3)
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-multi-v2-upload-delete-%d", time.Now().UnixNano()))
	uploadURL := rewriteUploadURLHost(getUploadLink(t, adminClient, repoID, "/"), adminClient.baseURL)

	deleteNames := []string{"multi-delete-a.txt", "multi-delete-b.txt", "multi-delete-c.txt"}
	for _, name := range deleteNames {
		uploadFileThroughLink(t, adminClient, uploadURL, name, "/", fmt.Sprintf("delete me: %s\n", name))
	}

	uploadNames := multiInstanceFileNames("multi-v2-upload", 9)
	start := make(chan struct{})
	results := make(chan concurrentMutationResult, len(uploadNames)+1)
	var wg sync.WaitGroup

	for idx, name := range uploadNames {
		idx := idx
		name := name
		client := clients[idx%len(clients)]
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result := uploadViaV2DirectConcurrent(client, repoID, name, "/", fmt.Sprintf("multi v2 upload %s at %d\n", name, time.Now().UnixNano()))
			result.name = name
			results <- result
		}()
	}

	deleteClient := clients[len(clients)-1]
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		result := multiInstanceDeleteJSONStatus(deleteClient, "/api/v2.1/repos/batch-delete-item/", map[string]interface{}{
			"repo_id":    repoID,
			"parent_dir": "/",
			"dirents":    deleteNames,
		})
		result.name = "batch-delete"
		results <- result
	}()

	close(start)
	wg.Wait()
	close(results)

	uploadResults := make([]concurrentMutationResult, 0, len(uploadNames))
	for result := range results {
		if result.name == "batch-delete" {
			if result.err != nil {
				t.Fatalf("batch delete failed: %v", result.err)
			}
			if result.status != http.StatusOK {
				t.Fatalf("batch delete status = %d body=%s", result.status, result.body)
			}
			continue
		}
		uploadResults = append(uploadResults, result)
	}

	expectConcurrentStatuses(t, uploadResults, http.StatusOK, http.StatusCreated)
	expectEntriesPresent(t, repoID, "/", uploadNames)
	expectEntriesAbsent(t, repoID, "/", deleteNames)
}

func TestMultiInstanceSeafHTTPUploadWhileMovingNoLostFiles(t *testing.T) {
	clients := multiInstanceRequireAdminClients(t, 3)
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-multi-upload-move-%d", time.Now().UnixNano()))
	for _, dirPath := range []string{"/src", "/dst"} {
		resp := adminClient.PostJSON(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=%s", repoID, dirPath), map[string]string{})
		expectStatus(t, resp, http.StatusCreated)
		resp.Body.Close()
	}
	uploadURLs := multiInstanceUploadLinks(t, clients, repoID, "/")
	uploadFileThroughLink(t, adminClient, uploadURLs[0], "anchor-move.txt", "/src", "anchor move content\n")

	uploadNames := multiInstanceFileNames("multi-upload-move", 9)
	start := make(chan struct{})
	results := make(chan concurrentMutationResult, len(uploadNames)+1)
	var wg sync.WaitGroup

	for idx, name := range uploadNames {
		idx := idx
		name := name
		client := clients[idx%len(clients)]
		uploadURL := uploadURLs[idx%len(uploadURLs)]
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result := uploadViaLinkConcurrent(client, uploadURL, name, "/", fmt.Sprintf("multi upload move %s at %d\n", name, time.Now().UnixNano()))
			result.name = name
			results <- result
		}()
	}

	moveClient := clients[len(clients)-1]
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		result := postJSONStatus(moveClient, "/api/v2.1/repos/sync-batch-move-item/", map[string]interface{}{
			"src_repo_id":    repoID,
			"src_parent_dir": "/src",
			"dst_repo_id":    repoID,
			"dst_parent_dir": "/dst",
			"src_dirents":    []string{"anchor-move.txt"},
		})
		result.name = "move-anchor"
		results <- result
	}()

	close(start)
	wg.Wait()
	close(results)

	uploadResults := make([]concurrentMutationResult, 0, len(uploadNames))
	for result := range results {
		if result.name == "move-anchor" {
			if result.err != nil {
				t.Fatalf("move failed: %v", result.err)
			}
			if result.status != http.StatusOK {
				t.Fatalf("move status = %d body=%s", result.status, result.body)
			}
			continue
		}
		uploadResults = append(uploadResults, result)
	}

	expectConcurrentStatuses(t, uploadResults, http.StatusOK, http.StatusCreated)
	expectEntriesPresent(t, repoID, "/", uploadNames)
	expectEntriesPresent(t, repoID, "/dst", []string{"anchor-move.txt"})
	expectEntriesAbsent(t, repoID, "/src", []string{"anchor-move.txt"})
}

func TestMultiInstanceCreateDirectoryPreservesAllEntries(t *testing.T) {
	clients := multiInstanceRequireAdminClients(t, 3)
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-multi-create-dir-%d", time.Now().UnixNano()))
	dirNames := retryNames("multi-dir", 9)

	results := multiInstanceRunConcurrentMutations(t, clients, dirNames, func(c *testClient, name string, _ int) concurrentMutationResult {
		result := postJSONStatus(c, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/%s", repoID, url.QueryEscape(name)), map[string]string{})
		result.name = name
		return result
	})
	expectConcurrentStatuses(t, results, http.StatusCreated)
	expectEntriesPresent(t, repoID, "/", dirNames)
}

func TestMultiInstanceRenameFilePreservesAllEntries(t *testing.T) {
	clients := multiInstanceRequireAdminClients(t, 3)
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-multi-rename-file-%d", time.Now().UnixNano()))
	oldNames := multiInstanceFileNames("rename-file", 9)
	newNames := make([]string, 0, len(oldNames))
	for _, oldName := range oldNames {
		newNames = append(newNames, "renamed-"+oldName)
		resp := adminClient.PostForm(t, fmt.Sprintf("/api2/repos/%s/file/?p=/%s&operation=create", repoID, url.QueryEscape(oldName)), url.Values{})
		multiInstanceExpectStatus(t, resp, http.StatusCreated, http.StatusOK)
		resp.Body.Close()
	}

	results := multiInstanceRunConcurrentMutations(t, clients, oldNames, func(c *testClient, name string, idx int) concurrentMutationResult {
		result := postJSONStatus(c, fmt.Sprintf("/api2/repos/%s/file/?p=/%s&operation=rename", repoID, url.QueryEscape(name)), map[string]string{
			"newname": newNames[idx],
		})
		result.name = name
		return result
	})
	expectConcurrentStatuses(t, results, http.StatusOK)
	expectEntriesAbsent(t, repoID, "/", oldNames)
	expectEntriesPresent(t, repoID, "/", newNames)
}

func TestMultiInstanceRenameDirectoryPreservesNestedChildren(t *testing.T) {
	clients := multiInstanceRequireAdminClients(t, 3)
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-multi-rename-dir-%d", time.Now().UnixNano()))
	dirNames := retryNames("rename-dir", 6)
	renamed := make([]string, 0, len(dirNames))
	for _, name := range dirNames {
		resp := adminClient.PostJSON(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/%s", repoID, url.QueryEscape(name)), map[string]string{})
		expectStatus(t, resp, http.StatusCreated)
		resp.Body.Close()
		childResp := adminClient.PostForm(t, fmt.Sprintf("/api2/repos/%s/file/?p=/%s/child.txt&operation=create", repoID, url.QueryEscape(name)), url.Values{})
		multiInstanceExpectStatus(t, childResp, http.StatusCreated, http.StatusOK)
		childResp.Body.Close()
		renamed = append(renamed, "renamed-"+name)
	}

	results := multiInstanceRunConcurrentMutations(t, clients, dirNames, func(c *testClient, name string, idx int) concurrentMutationResult {
		result := postJSONStatus(c, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/%s&operation=rename", repoID, url.QueryEscape(name)), map[string]string{
			"newname": renamed[idx],
		})
		result.name = name
		return result
	})
	expectConcurrentStatuses(t, results, http.StatusOK)
	expectEntriesAbsent(t, repoID, "/", dirNames)
	expectEntriesPresent(t, repoID, "/", renamed)
	for _, name := range renamed {
		resp := adminClient.Get(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/%s", repoID, url.QueryEscape(name)))
		expectStatus(t, resp, http.StatusOK)
		result := responseJSON(t, resp)
		entries, _ := result["dirent_list"].([]interface{})
		if !containsEntry(entries, "name", "child.txt") {
			t.Fatalf("child.txt missing under renamed directory %q", name)
		}
	}
}

func TestMultiInstanceDeleteFileRemovesAllEntries(t *testing.T) {
	clients := multiInstanceRequireAdminClients(t, 3)
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-multi-delete-file-%d", time.Now().UnixNano()))
	fileNames := multiInstanceFileNames("delete-file", 9)
	for _, name := range fileNames {
		resp := adminClient.PostForm(t, fmt.Sprintf("/api2/repos/%s/file/?p=/%s&operation=create", repoID, url.QueryEscape(name)), url.Values{})
		multiInstanceExpectStatus(t, resp, http.StatusCreated, http.StatusOK)
		resp.Body.Close()
	}

	results := multiInstanceRunConcurrentMutations(t, clients, fileNames, func(c *testClient, name string, _ int) concurrentMutationResult {
		result := multiInstanceDeleteStatus(c, fmt.Sprintf("/api/v2.1/repos/%s/file/?p=/%s", repoID, url.QueryEscape(name)))
		result.name = name
		return result
	})
	expectConcurrentStatuses(t, results, http.StatusOK)
	expectEntriesAbsent(t, repoID, "/", fileNames)
}

func TestMultiInstanceDeleteDirectoryRemovesAllEntries(t *testing.T) {
	clients := multiInstanceRequireAdminClients(t, 3)
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-multi-delete-dir-%d", time.Now().UnixNano()))
	dirNames := retryNames("delete-dir", 6)
	for _, name := range dirNames {
		resp := adminClient.PostJSON(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/%s", repoID, url.QueryEscape(name)), map[string]string{})
		expectStatus(t, resp, http.StatusCreated)
		resp.Body.Close()
	}

	results := multiInstanceRunConcurrentMutations(t, clients, dirNames, func(c *testClient, name string, _ int) concurrentMutationResult {
		result := multiInstanceDeleteStatus(c, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/%s", repoID, url.QueryEscape(name)))
		result.name = name
		return result
	})
	expectConcurrentStatuses(t, results, http.StatusOK)
	expectEntriesAbsent(t, repoID, "/", dirNames)
}

func TestMultiInstanceBatchDeleteFilesRemovesAllEntries(t *testing.T) {
	clients := multiInstanceRequireAdminClients(t, 3)
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-multi-batch-delete-%d", time.Now().UnixNano()))
	fileNames := multiInstanceFileNames("batch-delete", 6)
	for _, name := range fileNames {
		resp := adminClient.PostForm(t, fmt.Sprintf("/api2/repos/%s/file/?p=/%s&operation=create", repoID, url.QueryEscape(name)), url.Values{})
		multiInstanceExpectStatus(t, resp, http.StatusCreated, http.StatusOK)
		resp.Body.Close()
	}

	results := multiInstanceRunConcurrentMutations(t, clients, fileNames, func(c *testClient, name string, _ int) concurrentMutationResult {
		result := multiInstanceDeleteJSONStatus(c, "/api/v2.1/repos/batch-delete-item/", map[string]interface{}{
			"repo_id":    repoID,
			"parent_dir": "/",
			"dirents":    []string{name},
		})
		result.name = name
		return result
	})
	expectConcurrentStatuses(t, results, http.StatusOK)
	expectEntriesAbsent(t, repoID, "/", fileNames)
}

func TestMultiInstanceSameRepoMovePreservesAllEntries(t *testing.T) {
	clients := multiInstanceRequireAdminClients(t, 3)
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-multi-move-%d", time.Now().UnixNano()))
	for _, dirPath := range []string{"/src", "/dst"} {
		resp := adminClient.PostJSON(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=%s", repoID, dirPath), map[string]string{})
		expectStatus(t, resp, http.StatusCreated)
		resp.Body.Close()
	}
	fileNames := multiInstanceFileNames("move-file", 9)
	for _, name := range fileNames {
		resp := adminClient.PostForm(t, fmt.Sprintf("/api2/repos/%s/file/?p=/src/%s&operation=create", repoID, url.QueryEscape(name)), url.Values{})
		multiInstanceExpectStatus(t, resp, http.StatusCreated, http.StatusOK)
		resp.Body.Close()
	}

	results := multiInstanceRunConcurrentMutations(t, clients, fileNames, func(c *testClient, name string, _ int) concurrentMutationResult {
		result := postJSONStatus(c, "/api/v2.1/repos/sync-batch-move-item/", map[string]interface{}{
			"src_repo_id":    repoID,
			"src_parent_dir": "/src",
			"dst_repo_id":    repoID,
			"dst_parent_dir": "/dst",
			"src_dirents":    []string{name},
		})
		result.name = name
		return result
	})
	expectConcurrentStatuses(t, results, http.StatusOK)
	expectEntriesAbsent(t, repoID, "/src", fileNames)
	expectEntriesPresent(t, repoID, "/dst", fileNames)
}

func TestMultiInstanceSameRepoCopyReplaceCleansUpReplacedBlocks(t *testing.T) {
	clients := multiInstanceRequireAdminClients(t, 3)
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-multi-copy-replace-%d", time.Now().UnixNano()))
	for _, dirPath := range []string{"/src", "/dst"} {
		resp := adminClient.PostJSON(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=%s", repoID, dirPath), map[string]string{})
		expectStatus(t, resp, http.StatusCreated)
		resp.Body.Close()
	}

	uploadURL := getUploadURL(t, adminClient, repoID)
	fileNames := multiInstanceFileNames("copy-replace", 4)
	orgID := resolveOrgID(t, repoID)
	sourceBlockIDs := make(map[string]string, len(fileNames))
	replacedBlockIDs := make(map[string]string, len(fileNames))
	for _, name := range fileNames {
		sourceContent := fmt.Sprintf("multi-instance-copy-source-%s-%d\n", name, time.Now().UnixNano())
		replacedContent := fmt.Sprintf("multi-instance-copy-dst-%s-%d\n", name, time.Now().UnixNano())
		status, body := uploadFileThroughLinkStatus(t, adminClient, uploadURL, name, "/src", sourceContent)
		if status != http.StatusOK {
			t.Fatalf("seed upload for %s source returned status=%d body=%s", name, status, body)
		}
		status, body = uploadFileThroughLinkStatus(t, adminClient, uploadURL, name, "/dst", replacedContent)
		if status != http.StatusOK {
			t.Fatalf("seed upload for %s destination returned status=%d body=%s", name, status, body)
		}
		sourceHash := sha256.Sum256([]byte(sourceContent))
		replacedHash := sha256.Sum256([]byte(replacedContent))
		sourceBlockIDs[name] = hex.EncodeToString(sourceHash[:])
		replacedBlockIDs[name] = hex.EncodeToString(replacedHash[:])
	}

	results := multiInstanceRunConcurrentMutations(t, clients, fileNames, func(c *testClient, name string, _ int) concurrentMutationResult {
		result := postJSONStatus(c, "/api/v2.1/repos/"+repoID+"/file/copy/", map[string]interface{}{
			"src_path":        "/src/" + name,
			"dst_dir":         "/dst",
			"conflict_policy": "replace",
		})
		result.name = name
		return result
	})
	expectConcurrentStatuses(t, results, http.StatusOK)

	if !pollUntil(t, 15*time.Second, 250*time.Millisecond, func() bool {
		for _, name := range fileNames {
			if readBlockRefCount(t, orgID, sourceBlockIDs[name]) != 2 {
				return false
			}
			if readBlockRefCount(t, orgID, replacedBlockIDs[name]) > 0 {
				return false
			}
		}
		return true
	}) {
		for _, name := range fileNames {
			t.Logf("copy replace cleanup state for %s: source=%d replaced=%d", name, readBlockRefCount(t, orgID, sourceBlockIDs[name]), readBlockRefCount(t, orgID, replacedBlockIDs[name]))
		}
		t.Fatal("same-repo copy replace did not converge to expected ref_count state")
	}

	expectEntriesPresent(t, repoID, "/dst", fileNames)
}

func TestMultiInstanceRevertFilePreservesAllEntries(t *testing.T) {
	clients := multiInstanceRequireAdminClients(t, 3)
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-multi-revert-file-%d", time.Now().UnixNano()))
	fileNames := multiInstanceFileNames("revert-file", 6)
	for _, name := range fileNames {
		resp := adminClient.PostForm(t, fmt.Sprintf("/api2/repos/%s/file/?p=/%s&operation=create", repoID, url.QueryEscape(name)), url.Values{})
		multiInstanceExpectStatus(t, resp, http.StatusCreated, http.StatusOK)
		resp.Body.Close()
	}
	restoreCommit := getRepoHeadCommit(t, adminClient, repoID)
	if restoreCommit == "" {
		t.Fatal("could not resolve revert-file restore commit")
	}
	deleteResp := adminClient.DeleteJSON(t, "/api/v2.1/repos/batch-delete-item/", map[string]interface{}{
		"repo_id":    repoID,
		"parent_dir": "/",
		"dirents":    fileNames,
	})
	expectStatus(t, deleteResp, http.StatusOK)
	deleteResp.Body.Close()
	expectEntriesAbsent(t, repoID, "/", fileNames)

	results := multiInstanceRunConcurrentMutations(t, clients, fileNames, func(c *testClient, name string, _ int) concurrentMutationResult {
		result := postFormStatus(c, fmt.Sprintf("/api/v2.1/repos/%s/file/?operation=revert&p=/%s", repoID, url.QueryEscape(name)), url.Values{
			"commit_id":       {restoreCommit},
			"conflict_policy": {"replace"},
		})
		result.name = name
		return result
	})
	expectConcurrentStatuses(t, results, http.StatusOK)
	expectEntriesPresent(t, repoID, "/", fileNames)
}

func TestMultiInstanceRevertDirectoryPreservesAllEntries(t *testing.T) {
	clients := multiInstanceRequireAdminClients(t, 3)
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-multi-revert-dir-%d", time.Now().UnixNano()))
	dirNames := retryNames("revert-dir", 6)
	for _, name := range dirNames {
		resp := adminClient.PostJSON(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/%s", repoID, url.QueryEscape(name)), map[string]string{})
		expectStatus(t, resp, http.StatusCreated)
		resp.Body.Close()
	}
	restoreCommit := getRepoHeadCommit(t, adminClient, repoID)
	if restoreCommit == "" {
		t.Fatal("could not resolve revert-dir restore commit")
	}
	deleteResp := adminClient.DeleteJSON(t, "/api/v2.1/repos/batch-delete-item/", map[string]interface{}{
		"repo_id":    repoID,
		"parent_dir": "/",
		"dirents":    dirNames,
	})
	expectStatus(t, deleteResp, http.StatusOK)
	deleteResp.Body.Close()
	expectEntriesAbsent(t, repoID, "/", dirNames)

	results := multiInstanceRunConcurrentMutations(t, clients, dirNames, func(c *testClient, name string, _ int) concurrentMutationResult {
		result := postFormStatus(c, fmt.Sprintf("/api/v2.1/repos/%s/dir/?operation=revert&p=/%s", repoID, url.QueryEscape(name)), url.Values{
			"commit_id":       {restoreCommit},
			"conflict_policy": {"replace"},
		})
		result.name = name
		return result
	})
	expectConcurrentStatuses(t, results, http.StatusOK)
	expectEntriesPresent(t, repoID, "/", dirNames)
}

func TestMultiInstanceRestoreTrashFilesPreservesAllEntries(t *testing.T) {
	clients := multiInstanceRequireAdminClients(t, 3)
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-multi-restore-trash-%d", time.Now().UnixNano()))
	fileNames := multiInstanceFileNames("restore-trash", 6)
	trashCommitIDs := make(map[string]string, len(fileNames))
	trashPaths := make(map[string]string, len(fileNames))
	for _, name := range fileNames {
		resp := adminClient.PostForm(t, fmt.Sprintf("/api2/repos/%s/file/?p=/%s&operation=create", repoID, url.QueryEscape(name)), url.Values{})
		multiInstanceExpectStatus(t, resp, http.StatusCreated, http.StatusOK)
		resp.Body.Close()
	}
	deleteResp := adminClient.DeleteJSON(t, "/api/v2.1/repos/batch-delete-item/", map[string]interface{}{
		"repo_id":    repoID,
		"parent_dir": "/",
		"dirents":    fileNames,
	})
	expectStatus(t, deleteResp, http.StatusOK)
	deleteResp.Body.Close()
	for _, name := range fileNames {
		commitID, parentDir := findTrashItemFor(t, adminClient, repoID, name)
		if commitID == "" {
			t.Fatalf("trash listing did not expose %q", name)
		}
		trashCommitIDs[name] = commitID
		trashPaths[name] = parentDir + name
	}

	results := multiInstanceRunConcurrentMutations(t, clients, fileNames, func(c *testClient, name string, _ int) concurrentMutationResult {
		result := postJSONStatus(c, "/api/v2.1/repos/"+repoID+"/file/restore/", map[string]interface{}{
			"commit_id": trashCommitIDs[name],
			"p":         trashPaths[name],
		})
		result.name = name
		return result
	})
	expectConcurrentStatuses(t, results, http.StatusOK)
	expectEntriesPresent(t, repoID, "/", fileNames)
}
