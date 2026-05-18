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
// race. Before the LibraryWriteCoordinator fix, concurrent finalizations would
// each read the same stale HEAD, build commits from it, and then unconditionally
// overwrite HEAD — the last writer "won" and all other commits were orphaned. This
// test fires N simultaneous uploads to the same library and then verifies every
// file appears in the directory listing.
func TestConcurrentUploadsNoLostCommits(t *testing.T) {
	const concurrency = 8

	libName := fmt.Sprintf("inttest-concurrent-upload-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, libName)

	uploadURL := getUploadURL(t, adminClient, repoID)

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
			err := uploadFileToURL(adminClient, uploadURL, filename, "/", content)
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

// TestSequentialUploadThroughput verifies that the coordinator does not add
// significant overhead to sequential uploads. We measure the per-upload latency
// and fail if the mean is implausible for a local stack (> 5 s per file).
func TestSequentialUploadThroughput(t *testing.T) {
	const count = 5
	const maxMeanLatency = 5 * time.Second

	libName := fmt.Sprintf("inttest-throughput-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, libName)
	uploadURL := getUploadURL(t, adminClient, repoID)

	var total time.Duration
	for i := 0; i < count; i++ {
		filename := fmt.Sprintf("seq-%02d.txt", i)
		content := fmt.Sprintf("sequential upload %d\n", i)
		start := time.Now()
		if err := uploadFileToURL(adminClient, uploadURL, filename, "/", content); err != nil {
			t.Fatalf("upload %d failed: %v", i, err)
		}
		elapsed := time.Since(start)
		t.Logf("upload %d: %v", i, elapsed)
		total += elapsed
	}

	mean := total / count
	t.Logf("mean upload latency: %v (limit: %v)", mean, maxMeanLatency)
	if mean > maxMeanLatency {
		t.Errorf("mean upload latency %v exceeds limit %v — coordinator may be holding the lock too long", mean, maxMeanLatency)
	}
}

// getUploadURL fetches the seafhttp upload URL for the given library.
func getUploadURL(t *testing.T, c *testClient, repoID string) string {
	t.Helper()
	resp := c.Get(t, fmt.Sprintf("/api2/repos/%s/upload-link/?p=/", repoID))
	expectStatus(t, resp, http.StatusOK)
	return strings.Trim(responseBody(t, resp), "\" \n\r")
}

// uploadFileToURL posts a multipart file to a seafhttp upload URL.
func uploadFileToURL(c *testClient, uploadURL, filename, parentDir, content string) error {
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
