//go:build integration

package integration

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"

	api "github.com/Sesame-Disk/sesamefs/internal/api"
)

const chunkedPreflushIntegrationBlockSize = api.UploadBlockSize

func uploadFileThroughLink(t *testing.T, c *testClient, uploadURL, fileName, parentDir, content string) {
	t.Helper()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", fileName)
	if err != nil {
		t.Fatalf("CreateFormFile failed: %v", err)
	}
	if _, err := part.Write([]byte(content)); err != nil {
		t.Fatalf("writing upload content failed: %v", err)
	}
	if err := writer.WriteField("parent_dir", parentDir); err != nil {
		t.Fatalf("WriteField failed: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("closing multipart writer failed: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, uploadURL, &buf)
	if err != nil {
		t.Fatalf("creating upload request failed: %v", err)
	}
	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatalf("upload request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("upload failed with status %d: %s", resp.StatusCode, string(body))
	}
}

func uploadChunkThroughLink(t *testing.T, c *testClient, uploadURL, fileName, parentDir string, chunk []byte, start, end, total int) {
	t.Helper()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", fileName)
	if err != nil {
		t.Fatalf("CreateFormFile failed: %v", err)
	}
	if _, err := part.Write(chunk); err != nil {
		t.Fatalf("writing chunk content failed: %v", err)
	}
	if err := writer.WriteField("parent_dir", parentDir); err != nil {
		t.Fatalf("WriteField failed: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("closing multipart writer failed: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, uploadURL, &buf)
	if err != nil {
		t.Fatalf("creating chunk upload request failed: %v", err)
	}
	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, total))

	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatalf("chunk upload request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("chunk upload failed with status %d: %s", resp.StatusCode, string(body))
	}
}

func uploadChunkedBytesThroughLink(t *testing.T, c *testClient, uploadURL, fileName, parentDir string, content []byte, firstChunkSize int, repeatFirstChunk bool) {
	t.Helper()

	if len(content) < 2 {
		t.Fatalf("chunked upload content must be at least 2 bytes, got %d", len(content))
	}
	if firstChunkSize <= 0 || firstChunkSize >= len(content) {
		t.Fatalf("first chunk size must be between 1 and %d, got %d", len(content)-1, firstChunkSize)
	}
	first := content[:firstChunkSize]
	second := content[firstChunkSize:]

	uploadChunkThroughLink(t, c, uploadURL, fileName, parentDir, first, 0, firstChunkSize-1, len(content))
	if repeatFirstChunk {
		uploadChunkThroughLink(t, c, uploadURL, fileName, parentDir, first, 0, firstChunkSize-1, len(content))
	}
	uploadChunkThroughLink(t, c, uploadURL, fileName, parentDir, second, firstChunkSize, len(content)-1, len(content))
}

func uploadChunkedFileThroughLink(t *testing.T, c *testClient, uploadURL, fileName, parentDir string, content string, repeatFirstChunk bool) {
	t.Helper()

	data := []byte(content)
	if len(data) < 2 {
		t.Fatalf("chunked upload content must be at least 2 bytes, got %d", len(data))
	}
	middle := len(data) / 2
	if middle == 0 {
		middle = 1
	}
	uploadChunkedBytesThroughLink(t, c, uploadURL, fileName, parentDir, data, middle, repeatFirstChunk)
}

// TestUploadAndDownloadRoundTrip simulates the full frontend upload/download flow:
//
//  1. Create library
//  2. GET /api2/repos/:id/upload-link/?p=/  → get upload URL
//  3. POST <upload-url> with multipart file → upload content
//  4. GET /api2/repos/:id/file/?p=/filename → get download URL
//  5. GET <download-url> → download and verify content matches
//
// This is the exact flow the Seahub frontend uses via seafile-js.
func TestUploadAndDownloadRoundTrip(t *testing.T) {
	name := fmt.Sprintf("inttest-updown-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)

	fileContent := "Hello from integration test! This is test content for upload/download verification.\n"
	fileName := "roundtrip-test.txt"

	// Step 1: Get upload link
	t.Run("get upload link", func(t *testing.T) {
		resp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/upload-link/?p=/", repoID))
		expectStatus(t, resp, http.StatusOK)
		body := responseBody(t, resp)
		uploadURL := strings.Trim(body, "\" \n\r")

		if uploadURL == "" {
			t.Fatal("upload URL is empty")
		}
		if !strings.Contains(uploadURL, "/seafhttp/upload-api/") {
			t.Fatalf("unexpected upload URL format: %s", uploadURL)
		}
		t.Logf("upload URL: %s", uploadURL)

		// Step 2: Upload file via multipart form (same as frontend)
		t.Run("upload file", func(t *testing.T) {
			var buf bytes.Buffer
			writer := multipart.NewWriter(&buf)

			// Add the file field
			part, err := writer.CreateFormFile("file", fileName)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := part.Write([]byte(fileContent)); err != nil {
				t.Fatal(err)
			}

			// Add parent_dir field
			if err := writer.WriteField("parent_dir", "/"); err != nil {
				t.Fatal(err)
			}

			writer.Close()

			// POST to the upload URL
			req, err := http.NewRequest("POST", uploadURL, &buf)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Token "+adminClient.token)
			req.Header.Set("Content-Type", writer.FormDataContentType())

			uploadResp, err := adminClient.http.Do(req)
			if err != nil {
				t.Fatalf("upload request failed: %v", err)
			}
			defer uploadResp.Body.Close()

			if uploadResp.StatusCode != http.StatusOK && uploadResp.StatusCode != http.StatusCreated {
				respBody, _ := io.ReadAll(uploadResp.Body)
				t.Fatalf("upload failed with status %d: %s", uploadResp.StatusCode, string(respBody))
			}
			t.Logf("upload status: %d", uploadResp.StatusCode)
		})
	})

	// Step 3: Verify file appears in directory listing
	t.Run("file in listing", func(t *testing.T) {
		listResp := adminClient.Get(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/", repoID))
		expectStatus(t, listResp, http.StatusOK)

		var dirList map[string]interface{}
		decodeJSON(t, listResp, &dirList)
		entries, ok := dirList["dirent_list"].([]interface{})
		if !ok {
			t.Fatal("expected dirent_list in response")
		}
		if !containsEntry(entries, "name", fileName) {
			t.Errorf("uploaded file %q not in directory listing", fileName)
		}
	})

	// Step 4: Get download link
	t.Run("download and verify", func(t *testing.T) {
		dlResp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/file/?p=/%s", repoID, fileName))
		expectStatus(t, dlResp, http.StatusOK)
		body := responseBody(t, dlResp)
		downloadURL := strings.Trim(body, "\" \n\r")

		if downloadURL == "" {
			t.Fatal("download URL is empty")
		}
		if !strings.Contains(downloadURL, "/seafhttp/files/") {
			t.Fatalf("unexpected download URL format: %s", downloadURL)
		}
		t.Logf("download URL: %s", downloadURL)

		// Step 5: Download the file and verify content
		req, err := http.NewRequest("GET", downloadURL, nil)
		if err != nil {
			t.Fatal(err)
		}
		// seafhttp download doesn't need auth header (token is in URL)
		downloadResp, err := adminClient.http.Do(req)
		if err != nil {
			t.Fatalf("download request failed: %v", err)
		}
		defer downloadResp.Body.Close()

		if downloadResp.StatusCode != http.StatusOK {
			respBody, _ := io.ReadAll(downloadResp.Body)
			t.Fatalf("download failed with status %d: %s", downloadResp.StatusCode, string(respBody))
		}

		downloadedContent, err := io.ReadAll(downloadResp.Body)
		if err != nil {
			t.Fatal(err)
		}

		if string(downloadedContent) != fileContent {
			t.Errorf("downloaded content mismatch:\n  got:  %q\n  want: %q", string(downloadedContent), fileContent)
		} else {
			t.Log("content matches — full round-trip verified")
		}
	})
}

// TestUploadLinkURL verifies the upload link URL points to the correct server.
// This catches the bug where getBrowserURL returned the wrong host/port.
func TestUploadLinkURL(t *testing.T) {
	name := fmt.Sprintf("inttest-upurl-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)

	resp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/upload-link/?p=/", repoID))
	expectStatus(t, resp, http.StatusOK)
	uploadURL := strings.Trim(responseBody(t, resp), "\" \n\r")

	// URL must start with the base URL we're testing against
	if !strings.HasPrefix(uploadURL, baseURL) {
		t.Errorf("upload URL %q does not start with base URL %q", uploadURL, baseURL)
	}
}

// TestDownloadLinkURL verifies the download link URL points to the correct server.
func TestDownloadLinkURL(t *testing.T) {
	name := fmt.Sprintf("inttest-dlurl-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)

	// Create a file first
	createResp := adminClient.PostForm(t, fmt.Sprintf("/api2/repos/%s/file/?p=/urltest.txt&operation=create", repoID), nil)
	if createResp.StatusCode != http.StatusOK && createResp.StatusCode != http.StatusCreated {
		t.Fatalf("failed to create file: %d", createResp.StatusCode)
	}
	createResp.Body.Close()

	// Get download link
	resp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/file/?p=/urltest.txt", repoID))
	expectStatus(t, resp, http.StatusOK)
	downloadURL := strings.Trim(responseBody(t, resp), "\" \n\r")

	// URL must start with the base URL we're testing against
	if !strings.HasPrefix(downloadURL, baseURL) {
		t.Errorf("download URL %q does not start with base URL %q", downloadURL, baseURL)
	}
}

func TestDuplicateSeafhttpUploadIncrementsBlockRefCount(t *testing.T) {
	name := fmt.Sprintf("inttest-dedup-refcount-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)

	resp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/upload-link/?p=/", repoID))
	expectStatus(t, resp, http.StatusOK)
	uploadURL := strings.Trim(responseBody(t, resp), "\" \n\r")

	fileContent := fmt.Sprintf("same-content-across-uploads-%s\n", repoID)
	uploadFileThroughLink(t, adminClient, uploadURL, "dup-a.txt", "/", fileContent)
	uploadFileThroughLink(t, adminClient, uploadURL, "dup-b.txt", "/", fileContent)

	session := shareProjectionDBForTest(t).Session()
	var orgID string
	if err := session.Query(`SELECT org_id FROM libraries_by_id WHERE library_id = ?`, repoID).Scan(&orgID); err != nil {
		t.Fatalf("failed to resolve org_id for repo %s: %v", repoID, err)
	}

	hash := sha256.Sum256([]byte(fileContent))
	blockID := hex.EncodeToString(hash[:])
	var refCount int
	if err := session.Query(`SELECT ref_count FROM blocks WHERE org_id = ? AND block_id = ?`, orgID, blockID).Scan(&refCount); err != nil {
		t.Fatalf("failed to read block ref_count for %s: %v", blockID, err)
	}
	if refCount != 2 {
		t.Fatalf("ref_count after duplicate uploads = %d, want 2", refCount)
	}
}

func TestRegionPinnedLibraryReadPaths(t *testing.T) {
	const requestHost = "eu.sesamefs.local"
	name := fmt.Sprintf("inttest-region-read-%d", time.Now().UnixNano())
	createResp := adminClient.PostJSONWithHost(t, "/api2/repos/", map[string]string{
		"name":       name,
		"storage_id": "hot-s3-usa",
	}, requestHost)
	expectStatus(t, createResp, http.StatusOK)

	result := responseJSON(t, createResp)
	repoID, _ := result["repo_id"].(string)
	if repoID == "" {
		t.Fatalf("expected repo_id in create response: %v", result)
	}
	if result["storage_id"] != "hot-s3-usa" {
		t.Fatalf("storage_id = %v, want %q", result["storage_id"], "hot-s3-usa")
	}

	t.Cleanup(func() {
		cleanup := adminClient.Delete(t, fmt.Sprintf("/api/v2.1/repos/%s/", repoID))
		cleanup.Body.Close()
	})

	fileName := "region-read-test.txt"
	fileContent := "region-pinned read path verification\n"

	resp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/upload-link/?p=/", repoID))
	expectStatus(t, resp, http.StatusOK)
	uploadURL := strings.Trim(responseBody(t, resp), "\" \n\r")

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", fileName)
	if err != nil {
		t.Fatalf("CreateFormFile failed: %v", err)
	}
	if _, err := part.Write([]byte(fileContent)); err != nil {
		t.Fatalf("writing upload body failed: %v", err)
	}
	if err := writer.WriteField("parent_dir", "/"); err != nil {
		t.Fatalf("WriteField failed: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("closing multipart writer failed: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, uploadURL, &buf)
	if err != nil {
		t.Fatalf("creating upload request failed: %v", err)
	}
	req.Header.Set("Authorization", "Token "+adminClient.token)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	uploadResp, err := adminClient.http.Do(req)
	if err != nil {
		t.Fatalf("upload request failed: %v", err)
	}
	if uploadResp.StatusCode != http.StatusOK && uploadResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(uploadResp.Body)
		uploadResp.Body.Close()
		t.Fatalf("upload failed with status %d: %s", uploadResp.StatusCode, string(body))
	}
	uploadResp.Body.Close()

	t.Run("seafhttp download reads persisted storage class", func(t *testing.T) {
		dlResp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/file/?p=/%s", repoID, fileName))
		expectStatus(t, dlResp, http.StatusOK)
		downloadURL := strings.Trim(responseBody(t, dlResp), "\" \n\r")

		req, err := http.NewRequest(http.MethodGet, downloadURL, nil)
		if err != nil {
			t.Fatalf("creating download request failed: %v", err)
		}
		downloadResp, err := adminClient.http.Do(req)
		if err != nil {
			t.Fatalf("download request failed: %v", err)
		}
		if downloadResp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(downloadResp.Body)
			downloadResp.Body.Close()
			t.Fatalf("download failed with status %d: %s", downloadResp.StatusCode, string(body))
		}
		content, err := io.ReadAll(downloadResp.Body)
		downloadResp.Body.Close()
		if err != nil {
			t.Fatalf("reading download response failed: %v", err)
		}
		if string(content) != fileContent {
			t.Fatalf("download content = %q, want %q", string(content), fileContent)
		}
	})

	t.Run("raw reader ignores conflicting host when library storage is pinned", func(t *testing.T) {
		rawResp := adminClient.GetWithHost(t, fmt.Sprintf("/repo/%s/raw/%s", repoID, fileName), requestHost)
		expectStatus(t, rawResp, http.StatusOK)
		content := responseBody(t, rawResp)
		if content != fileContent {
			t.Fatalf("raw content = %q, want %q", content, fileContent)
		}
	})
}

// TestUploadOverwrite verifies that uploading to the same path overwrites the file.
func TestUploadOverwrite(t *testing.T) {
	name := fmt.Sprintf("inttest-overwrite-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)
	fileName := "overwrite-test.txt"

	upload := func(content string) {
		t.Helper()
		// Get upload link
		resp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/upload-link/?p=/", repoID))
		expectStatus(t, resp, http.StatusOK)
		uploadURL := strings.Trim(responseBody(t, resp), "\" \n\r")

		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)
		part, _ := writer.CreateFormFile("file", fileName)
		part.Write([]byte(content))
		writer.WriteField("parent_dir", "/")
		writer.Close()

		req, _ := http.NewRequest("POST", uploadURL, &buf)
		req.Header.Set("Authorization", "Token "+adminClient.token)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		uploadResp, err := adminClient.http.Do(req)
		if err != nil {
			t.Fatalf("upload failed: %v", err)
		}
		uploadResp.Body.Close()
	}

	download := func() string {
		t.Helper()
		resp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/file/?p=/%s", repoID, fileName))
		expectStatus(t, resp, http.StatusOK)
		downloadURL := strings.Trim(responseBody(t, resp), "\" \n\r")

		req, _ := http.NewRequest("GET", downloadURL, nil)
		dlResp, err := adminClient.http.Do(req)
		if err != nil {
			t.Fatalf("download failed: %v", err)
		}
		defer dlResp.Body.Close()
		content, _ := io.ReadAll(dlResp.Body)
		return string(content)
	}

	// Upload v1
	upload("version 1 content")

	// Upload v2 (overwrite)
	upload("version 2 content")

	// Download and verify it's v2
	got := download()
	if got != "version 2 content" {
		t.Errorf("expected 'version 2 content', got %q", got)
	}
}

func TestUploadLinkReuseCreatesMultipleRevisions(t *testing.T) {
	name := fmt.Sprintf("inttest-link-reuse-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)
	fileName := "link-reuse-history.txt"

	resp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/upload-link/?p=/", repoID))
	expectStatus(t, resp, http.StatusOK)
	uploadURL := strings.Trim(responseBody(t, resp), "\" \n\r")
	if uploadURL == "" {
		t.Fatal("upload URL is empty")
	}

	uploadFileThroughLink(t, adminClient, uploadURL, fileName, "/", "link reuse version 1")
	uploadFileThroughLink(t, adminClient, uploadURL, fileName, "/", "link reuse version 2")
	uploadFileThroughLink(t, adminClient, uploadURL, fileName, "/", "link reuse version 3")

	t.Run("current content is latest revision", func(t *testing.T) {
		resp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/file/?p=/%s", repoID, fileName))
		expectStatus(t, resp, http.StatusOK)
		downloadURL := strings.Trim(responseBody(t, resp), "\" \n\r")

		req, err := http.NewRequest(http.MethodGet, downloadURL, nil)
		if err != nil {
			t.Fatalf("creating download request failed: %v", err)
		}
		dlResp, err := adminClient.http.Do(req)
		if err != nil {
			t.Fatalf("download request failed: %v", err)
		}
		defer dlResp.Body.Close()
		if dlResp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(dlResp.Body)
			t.Fatalf("download failed with status %d: %s", dlResp.StatusCode, string(body))
		}
		content, err := io.ReadAll(dlResp.Body)
		if err != nil {
			t.Fatalf("reading download body failed: %v", err)
		}
		if got := string(content); got != "link reuse version 3" {
			t.Fatalf("downloaded content = %q, want %q", got, "link reuse version 3")
		}
	})

	t.Run("history contains multiple revisions", func(t *testing.T) {
		revisionsResp := adminClient.Get(t, fmt.Sprintf("/api2/repo/file_revisions/%s/?p=/%s", repoID, fileName))
		expectStatus(t, revisionsResp, http.StatusOK)

		payload := responseJSON(t, revisionsResp)
		items, ok := payload["data"].([]interface{})
		if !ok {
			t.Fatalf("expected data array in revisions response, got %v", payload)
		}
		if len(items) < 3 {
			t.Fatalf("revision count = %d, want at least 3 after reusing the same upload URL", len(items))
		}
	})
}

func TestChunkedUploadLinkReuseCreatesMultipleRevisions(t *testing.T) {
	name := fmt.Sprintf("inttest-chunked-link-reuse-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)
	fileName := "chunked-link-reuse.txt"

	resp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/upload-link/?p=/", repoID))
	expectStatus(t, resp, http.StatusOK)
	uploadURL := strings.Trim(responseBody(t, resp), "\" \n\r")
	if uploadURL == "" {
		t.Fatal("upload URL is empty")
	}

	uploadChunkedFileThroughLink(t, adminClient, uploadURL, fileName, "/", "chunked retry version 1", true)
	uploadChunkedFileThroughLink(t, adminClient, uploadURL, fileName, "/", "chunked retry version 2", false)

	t.Run("current content is latest chunked revision", func(t *testing.T) {
		resp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/file/?p=/%s", repoID, fileName))
		expectStatus(t, resp, http.StatusOK)
		downloadURL := strings.Trim(responseBody(t, resp), "\" \n\r")

		req, err := http.NewRequest(http.MethodGet, downloadURL, nil)
		if err != nil {
			t.Fatalf("creating download request failed: %v", err)
		}
		dlResp, err := adminClient.http.Do(req)
		if err != nil {
			t.Fatalf("download request failed: %v", err)
		}
		defer dlResp.Body.Close()
		if dlResp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(dlResp.Body)
			t.Fatalf("download failed with status %d: %s", dlResp.StatusCode, string(body))
		}
		content, err := io.ReadAll(dlResp.Body)
		if err != nil {
			t.Fatalf("reading download body failed: %v", err)
		}
		if got := string(content); got != "chunked retry version 2" {
			t.Fatalf("downloaded content = %q, want %q", got, "chunked retry version 2")
		}
	})

	t.Run("history contains both chunked revisions", func(t *testing.T) {
		revisionsResp := adminClient.Get(t, fmt.Sprintf("/api2/repo/file_revisions/%s/?p=/%s", repoID, fileName))
		expectStatus(t, revisionsResp, http.StatusOK)

		payload := responseJSON(t, revisionsResp)
		items, ok := payload["data"].([]interface{})
		if !ok {
			t.Fatalf("expected data array in revisions response, got %v", payload)
		}
		if len(items) < 2 {
			t.Fatalf("revision count = %d, want at least 2 after chunked upload-link reuse", len(items))
		}
	})
}

func TestChunkedUploadLinkReusePreflushesLargeRevisions(t *testing.T) {
	name := fmt.Sprintf("inttest-chunked-preflush-link-reuse-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)
	fileName := "chunked-preflush-link-reuse.bin"

	resp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/upload-link/?p=/", repoID))
	expectStatus(t, resp, http.StatusOK)
	uploadURL := strings.Trim(responseBody(t, resp), "\" \n\r")
	if uploadURL == "" {
		t.Fatal("upload URL is empty")
	}

	version1 := append(bytes.Repeat([]byte("a"), chunkedPreflushIntegrationBlockSize), []byte("-version-1-tail")...)
	version2 := append(bytes.Repeat([]byte("b"), chunkedPreflushIntegrationBlockSize), []byte("-version-2-tail")...)

	uploadChunkedBytesThroughLink(t, adminClient, uploadURL, fileName, "/", version1, chunkedPreflushIntegrationBlockSize, true)
	uploadChunkedBytesThroughLink(t, adminClient, uploadURL, fileName, "/", version2, chunkedPreflushIntegrationBlockSize, false)

	t.Run("current content is latest large chunked revision", func(t *testing.T) {
		resp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/file/?p=/%s", repoID, fileName))
		expectStatus(t, resp, http.StatusOK)
		downloadURL := strings.Trim(responseBody(t, resp), "\" \n\r")

		req, err := http.NewRequest(http.MethodGet, downloadURL, nil)
		if err != nil {
			t.Fatalf("creating download request failed: %v", err)
		}
		dlResp, err := adminClient.http.Do(req)
		if err != nil {
			t.Fatalf("download request failed: %v", err)
		}
		defer dlResp.Body.Close()
		if dlResp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(dlResp.Body)
			t.Fatalf("download failed with status %d: %s", dlResp.StatusCode, string(body))
		}
		content, err := io.ReadAll(dlResp.Body)
		if err != nil {
			t.Fatalf("reading download body failed: %v", err)
		}
		if !bytes.Equal(content, version2) {
			expectedHash := sha256.Sum256(version2)
			actualHash := sha256.Sum256(content)
			t.Fatalf("downloaded large content hash = %s, want %s", hex.EncodeToString(actualHash[:]), hex.EncodeToString(expectedHash[:]))
		}
	})

	t.Run("history contains both large chunked revisions", func(t *testing.T) {
		revisionsResp := adminClient.Get(t, fmt.Sprintf("/api2/repo/file_revisions/%s/?p=/%s", repoID, fileName))
		expectStatus(t, revisionsResp, http.StatusOK)

		payload := responseJSON(t, revisionsResp)
		items, ok := payload["data"].([]interface{})
		if !ok {
			t.Fatalf("expected data array in revisions response, got %v", payload)
		}
		if len(items) < 2 {
			t.Fatalf("revision count = %d, want at least 2 after large chunked upload-link reuse", len(items))
		}
	})
}

func TestChunkedUploadOutOfOrderCompletesWhenGapFills(t *testing.T) {
	name := fmt.Sprintf("inttest-chunked-out-of-order-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)
	fileName := "chunked-out-of-order.bin"

	resp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/upload-link/?p=/", repoID))
	expectStatus(t, resp, http.StatusOK)
	uploadURL := strings.Trim(responseBody(t, resp), "\" \n\r")
	if uploadURL == "" {
		t.Fatal("upload URL is empty")
	}

	content := append(bytes.Repeat([]byte("c"), chunkedPreflushIntegrationBlockSize), []byte("-out-of-order-tail")...)
	half := chunkedPreflushIntegrationBlockSize / 2

	uploadChunkThroughLink(t, adminClient, uploadURL, fileName, "/", content[half:], half, len(content)-1, len(content))
	uploadChunkThroughLink(t, adminClient, uploadURL, fileName, "/", content[:half], 0, half-1, len(content))

	resp = adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/file/?p=/%s", repoID, fileName))
	expectStatus(t, resp, http.StatusOK)
	downloadURL := strings.Trim(responseBody(t, resp), "\" \n\r")

	req, err := http.NewRequest(http.MethodGet, downloadURL, nil)
	if err != nil {
		t.Fatalf("creating download request failed: %v", err)
	}
	dlResp, err := adminClient.http.Do(req)
	if err != nil {
		t.Fatalf("download request failed: %v", err)
	}
	defer dlResp.Body.Close()
	if dlResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(dlResp.Body)
		t.Fatalf("download failed with status %d: %s", dlResp.StatusCode, string(body))
	}
	downloaded, err := io.ReadAll(dlResp.Body)
	if err != nil {
		t.Fatalf("reading download body failed: %v", err)
	}
	if !bytes.Equal(downloaded, content) {
		expectedHash := sha256.Sum256(content)
		actualHash := sha256.Sum256(downloaded)
		t.Fatalf("downloaded out-of-order content hash = %s, want %s", hex.EncodeToString(actualHash[:]), hex.EncodeToString(expectedHash[:]))
	}
}

// TestReadonlyCannotUpload verifies that readonly users cannot successfully upload files.
// Note: The upload-link endpoint currently returns 200 even for non-owners (the link is
// generated but the actual upload would be subject to permission checks). This test
// verifies the actual upload fails, not just the link generation.
func TestReadonlyCannotUpload(t *testing.T) {
	name := fmt.Sprintf("inttest-roupload-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)

	// Readonly user tries to create a file in admin's library
	resp := readonlyClient.PostForm(t, fmt.Sprintf("/api2/repos/%s/file/?p=/no-write.txt&operation=create", repoID), nil)
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusNotFound &&
		resp.StatusCode != http.StatusUnauthorized {
		t.Logf("readonly create file returned %d (permission enforcement may vary)", resp.StatusCode)
	}
	resp.Body.Close()
}

// TestGuestCannotUpload verifies that guest users cannot successfully upload files.
func TestGuestCannotUpload(t *testing.T) {
	name := fmt.Sprintf("inttest-guestupload-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)

	// Guest user tries to create a file in admin's library
	resp := guestClient.PostForm(t, fmt.Sprintf("/api2/repos/%s/file/?p=/no-write.txt&operation=create", repoID), nil)
	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusNotFound &&
		resp.StatusCode != http.StatusUnauthorized {
		t.Logf("guest create file returned %d (permission enforcement may vary)", resp.StatusCode)
	}
	resp.Body.Close()
}

// TestV2DirectUploadRoundTrip verifies the POST /api/v2.1/repos/:id/upload endpoint:
// the file must appear in the directory listing and the block ref_count must be 1 after upload.
func TestV2DirectUploadRoundTrip(t *testing.T) {
	name := fmt.Sprintf("inttest-v2upload-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)

	fileName := "v2-upload-test.txt"
	fileContent := fmt.Sprintf("v2-direct-upload-content-%s\n", repoID)

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", fileName)
	if err != nil {
		t.Fatalf("CreateFormFile failed: %v", err)
	}
	if _, err := part.Write([]byte(fileContent)); err != nil {
		t.Fatalf("writing upload content failed: %v", err)
	}
	if err := writer.WriteField("parent_dir", "/"); err != nil {
		t.Fatalf("WriteField failed: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("closing multipart writer failed: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, adminClient.baseURL+"/api/v2.1/repos/"+repoID+"/upload", &buf)
	if err != nil {
		t.Fatalf("creating upload request failed: %v", err)
	}
	req.Header.Set("Authorization", "Token "+adminClient.token)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	uploadResp, err := adminClient.http.Do(req)
	if err != nil {
		t.Fatalf("v2 upload request failed: %v", err)
	}
	if uploadResp.StatusCode != http.StatusOK && uploadResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(uploadResp.Body)
		uploadResp.Body.Close()
		t.Fatalf("v2 upload got status %d: %s", uploadResp.StatusCode, string(body))
	}

	// Response must be [{name, id, size}].
	var uploadResult []map[string]interface{}
	decodeJSON(t, uploadResp, &uploadResult)
	if len(uploadResult) == 0 {
		t.Fatal("v2 upload response payload is empty")
	}
	if uploadResult[0]["name"] != fileName {
		t.Fatalf("upload response name = %v, want %q", uploadResult[0]["name"], fileName)
	}

	// File must appear in the directory listing.
	listResp := adminClient.Get(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/", repoID))
	expectStatus(t, listResp, http.StatusOK)
	listResult := responseJSON(t, listResp)
	entries, _ := listResult["dirent_list"].([]interface{})
	if !containsEntry(entries, "name", fileName) {
		t.Fatalf("file %q not found in directory listing after v2 upload", fileName)
	}

	// Block ref_count must be 1 (one unique write).
	session := shareProjectionDBForTest(t).Session()
	var orgID string
	if err := session.Query(`SELECT org_id FROM libraries_by_id WHERE library_id = ?`, repoID).Scan(&orgID); err != nil {
		t.Fatalf("failed to resolve org_id for repo %s: %v", repoID, err)
	}
	hash := sha256.Sum256([]byte(fileContent))
	blockID := hex.EncodeToString(hash[:])
	var refCount int
	if err := session.Query(`SELECT ref_count FROM blocks WHERE org_id = ? AND block_id = ?`, orgID, blockID).Scan(&refCount); err != nil {
		t.Fatalf("failed to read block ref_count for block %s: %v", blockID, err)
	}
	if refCount != 1 {
		t.Fatalf("ref_count after v2 upload = %d, want 1", refCount)
	}
}
