//go:build integration

package integration

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestConcurrentUploadsNoLostCommits is the regression test for the HEAD-overwrite
// race. Before the canonical CAS + retry fix, concurrent finalizations could
// each read the same stale HEAD, build commits from it, and then lose updates
// when publishing metadata. This test fires N simultaneous uploads to the same
// library and then verifies every file appears in the directory listing.
func TestConcurrentUploadsNoLostCommits(t *testing.T) {
	const concurrency = 8

	libName := fmt.Sprintf("inttest-concurrent-upload-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, libName)

	uploadURL := getConcurrentUploadURL(t, adminClient, repoID)

	type result struct {
		filename string
		err      error
	}
	results := make(chan result, concurrency)

	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			filename := fmt.Sprintf("concurrent-%02d.txt", i)
			content := fmt.Sprintf("concurrent upload %d at %d\n", i, time.Now().UnixNano())
			err := uploadFileToConcurrentURL(adminClient, uploadURL, filename, "/", content)
			results <- result{filename: filename, err: err}
		}()
	}

	wg.Wait()
	close(results)

	var failures []string
	var uploaded []string
	for r := range results {
		if r.err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", r.filename, r.err))
		} else {
			uploaded = append(uploaded, r.filename)
		}
	}
	if len(failures) > 0 {
		t.Fatalf("upload(s) failed: %v", failures)
	}

	// Verify every uploaded file appears in the directory listing.
	// A missing file means its commit was overwritten (the bug we're fixing).
	listResp := adminClient.Get(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/", repoID))
	expectStatus(t, listResp, http.StatusOK)
	var dirList map[string]interface{}
	decodeJSON(t, listResp, &dirList)
	entries, _ := dirList["dirent_list"].([]interface{})

	var missing []string
	for _, filename := range uploaded {
		if !containsEntry(entries, "name", filename) {
			missing = append(missing, filename)
		}
	}
	if len(missing) > 0 {
		t.Errorf("%d/%d commits were lost (HEAD overwrite bug): %v",
			len(missing), concurrency, missing)
	} else {
		t.Logf("all %d concurrent uploads survived — no commits lost", concurrency)
	}
}

// getConcurrentUploadURL fetches the seafhttp upload URL for the given library.
func getConcurrentUploadURL(t *testing.T, c *testClient, repoID string) string {
	t.Helper()
	resp := c.Get(t, fmt.Sprintf("/api2/repos/%s/upload-link/?p=/", repoID))
	expectStatus(t, resp, http.StatusOK)
	return strings.Trim(responseBody(t, resp), "\" \n\r")
}

// uploadFileToConcurrentURL posts a multipart file to a seafhttp upload URL.
func uploadFileToConcurrentURL(c *testClient, uploadURL, filename, parentDir, content string) error {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		return err
	}
	if _, err := part.Write([]byte(content)); err != nil {
		return err
	}
	if err := w.WriteField("parent_dir", parentDir); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, uploadURL, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("status %d: %s", resp.StatusCode, body)
	}
	return nil
}
