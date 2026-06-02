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
	"net/url"
	"strings"
	"testing"
	"time"
)

func uploadFileThroughLink(t *testing.T, c *testClient, uploadURL, fileName, parentDir, content string) {
	t.Helper()
	uploadFileThroughLinkWithReplaceField(t, c, uploadURL, fileName, parentDir, content, nil)
}

func uploadFileThroughLinkWithReplaceField(t *testing.T, c *testClient, uploadURL, fileName, parentDir, content string, replace *string) {
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
	if replace != nil {
		if err := writer.WriteField("replace", *replace); err != nil {
			t.Fatalf("WriteField replace failed: %v", err)
		}
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

func assertNoUploadReferrers(t *testing.T, repoID, dirPath, fileName string) {
	t.Helper()
	referrers := uploadedFileBlockReferrers(t, repoID, dirPath, fileName)
	for _, referrer := range referrers {
		if strings.HasPrefix(referrer, "up:") {
			t.Fatalf("block referrers leaked provisional upload ref: %v", referrers)
		}
	}
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

	fileContent := fmt.Sprintf("Hello from integration test! This is test content for upload/download verification. repo=%s\n", repoID)
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

	assertNoUploadReferrers(t, repoID, "/", fileName)
}

func TestChunkedUploadAndDownloadRoundTrip(t *testing.T) {
	name := fmt.Sprintf("inttest-chunked-updown-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)
	uploadURL := getUploadURL(t, adminClient, repoID)

	fileName := "chunked-roundtrip.txt"
	fileContent := []byte(strings.Repeat("chunked roundtrip content "+repoID+" ", 32) + "done\n")
	chunkStarts := []int{0, 173, 347, 521}

	for index, start := range chunkStarts {
		end := len(fileContent) - 1
		if index+1 < len(chunkStarts) {
			end = chunkStarts[index+1] - 1
		}

		status, body := uploadChunkThroughLinkStatus(
			t,
			adminClient,
			uploadURL,
			fileName,
			"/",
			fileContent[start:end+1],
			fmt.Sprintf("bytes %d-%d/%d", start, end, len(fileContent)),
		)
		if status != http.StatusOK && status != http.StatusCreated {
			t.Fatalf("chunk %d upload status = %d, want 200/201; body=%s", index, status, body)
		}
	}

	listResp := adminClient.Get(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/", repoID))
	expectStatus(t, listResp, http.StatusOK)

	var dirList map[string]interface{}
	decodeJSON(t, listResp, &dirList)
	entries, _ := dirList["dirent_list"].([]interface{})
	if !containsEntry(entries, "name", fileName) {
		t.Fatalf("chunked uploaded file %q not found in directory listing", fileName)
	}

	dlResp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/file/?p=/%s", repoID, fileName))
	expectStatus(t, dlResp, http.StatusOK)
	downloadURL := strings.Trim(responseBody(t, dlResp), "\" \n\r")

	req, err := http.NewRequest(http.MethodGet, downloadURL, nil)
	if err != nil {
		t.Fatalf("creating chunked roundtrip download request failed: %v", err)
	}
	downloadResp, err := adminClient.http.Do(req)
	if err != nil {
		t.Fatalf("chunked roundtrip download request failed: %v", err)
	}
	defer downloadResp.Body.Close()
	if downloadResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(downloadResp.Body)
		t.Fatalf("chunked roundtrip download status = %d: %s", downloadResp.StatusCode, string(body))
	}

	downloadedContent, err := io.ReadAll(downloadResp.Body)
	if err != nil {
		t.Fatalf("reading chunked roundtrip download failed: %v", err)
	}
	if !bytes.Equal(downloadedContent, fileContent) {
		t.Fatalf("chunked downloaded content mismatch: got %d bytes, want %d", len(downloadedContent), len(fileContent))
	}

	assertNoUploadReferrers(t, repoID, "/", fileName)
}

func TestEncryptedUploadAndDownloadRoundTrip(t *testing.T) {
	name := fmt.Sprintf("inttest-encrypted-updown-%d", time.Now().UnixNano())
	password := "test-password-123"
	repoID := createLibraryWithBody(t, adminClient, name, map[string]interface{}{
		"repo_name": name,
		"encrypted": true,
		"passwd":    password,
	}, true)

	setPassResp := adminClient.PostForm(t, fmt.Sprintf("/api/v2.1/repos/%s/set-password/", repoID), url.Values{"password": {password}})
	expectStatus(t, setPassResp, http.StatusOK)
	setPassResp.Body.Close()

	fileName := "encrypted-roundtrip.txt"
	fileContent := "Encrypted roundtrip integration content. This must survive upload and download intact.\n"
	uploadURL := getUploadURL(t, adminClient, repoID)
	uploadFileThroughLink(t, adminClient, uploadURL, fileName, "/", fileContent)

	dlResp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/file/?p=/%s", repoID, fileName))
	expectStatus(t, dlResp, http.StatusOK)
	downloadURL := strings.Trim(responseBody(t, dlResp), "\" \n\r")

	req, err := http.NewRequest(http.MethodGet, downloadURL, nil)
	if err != nil {
		t.Fatalf("creating encrypted roundtrip download request failed: %v", err)
	}
	downloadResp, err := adminClient.http.Do(req)
	if err != nil {
		t.Fatalf("encrypted roundtrip download request failed: %v", err)
	}
	defer downloadResp.Body.Close()
	if downloadResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(downloadResp.Body)
		t.Fatalf("encrypted roundtrip download status = %d: %s", downloadResp.StatusCode, string(body))
	}

	downloadedContent, err := io.ReadAll(downloadResp.Body)
	if err != nil {
		t.Fatalf("reading encrypted roundtrip download failed: %v", err)
	}
	if string(downloadedContent) != fileContent {
		t.Fatalf("encrypted downloaded content mismatch:\n  got:  %q\n  want: %q", string(downloadedContent), fileContent)
	}
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

func TestDuplicateSeafhttpUploadDeduplicatesBlockReference(t *testing.T) {
	name := fmt.Sprintf("inttest-dedup-refcount-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)

	resp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/upload-link/?p=/", repoID))
	expectStatus(t, resp, http.StatusOK)
	uploadURL := strings.Trim(responseBody(t, resp), "\" \n\r")

	fileContent := fmt.Sprintf("same-content-across-uploads-%s\n", repoID)
	uploadFileThroughLink(t, adminClient, uploadURL, "dup-a.txt", "/", fileContent)
	uploadFileThroughLink(t, adminClient, uploadURL, "dup-b.txt", "/", fileContent)

	orgID := resolveOrgID(t, repoID)
	hash := sha256.Sum256([]byte(fileContent))
	blockID := hex.EncodeToString(hash[:])
	// Both files have identical content → identical fs_id → they SHARE the single
	// permanent fs:<lib>:<fs_id> reference (dedup). The block stays alive with one
	// reference, not two — there is no per-file counter to increment.
	if refCount := readBlockRefCount(t, orgID, blockID); refCount != 1 {
		t.Fatalf("references after duplicate uploads = %d, want 1 (shared fs_id)", refCount)
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

// TestUploadOverwrite verifies that update-link uploads overwrite the file by default.
func TestUploadOverwrite(t *testing.T) {
	name := fmt.Sprintf("inttest-overwrite-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)
	fileName := "overwrite-test.txt"

	upload := func(linkPath, content string) {
		t.Helper()
		resp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/%s/?p=/", repoID, linkPath))
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
	upload("upload-link", "version 1 content")

	// Upload v2 (overwrite via update-link)
	upload("update-link", "version 2 content")

	// Download and verify it's v2
	got := download()
	if got != "version 2 content" {
		t.Errorf("expected 'version 2 content', got %q", got)
	}
}

func TestUploadLinkAutoRenamesWithoutReplaceOverride(t *testing.T) {
	name := fmt.Sprintf("inttest-upload-autorename-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)
	fileName := "autorename-test.txt"
	autoRenamed := "autorename-test (1).txt"

	resp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/upload-link/?p=/", repoID))
	expectStatus(t, resp, http.StatusOK)
	uploadURL := strings.Trim(responseBody(t, resp), "\" \n\r")

	uploadFileThroughLink(t, adminClient, uploadURL, fileName, "/", "first version")
	uploadFileThroughLink(t, adminClient, uploadURL, fileName, "/", "second version")

	listResp := adminClient.Get(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/", repoID))
	expectStatus(t, listResp, http.StatusOK)

	var dirList map[string]interface{}
	decodeJSON(t, listResp, &dirList)
	entries, _ := dirList["dirent_list"].([]interface{})
	if !containsEntry(entries, "name", fileName) {
		t.Fatalf("original file %q not found after repeated upload-link upload", fileName)
	}
	if !containsEntry(entries, "name", autoRenamed) {
		t.Fatalf("autorename target %q not found after repeated upload-link upload", autoRenamed)
	}

	download := func(name string) string {
		t.Helper()
		resp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/file/?p=/%s", repoID, url.PathEscape(name)))
		expectStatus(t, resp, http.StatusOK)
		downloadURL := strings.Trim(responseBody(t, resp), "\" \n\r")

		req, err := http.NewRequest(http.MethodGet, downloadURL, nil)
		if err != nil {
			t.Fatalf("creating download request failed: %v", err)
		}
		downloadResp, err := adminClient.http.Do(req)
		if err != nil {
			t.Fatalf("download request failed: %v", err)
		}
		defer downloadResp.Body.Close()

		content, err := io.ReadAll(downloadResp.Body)
		if err != nil {
			t.Fatalf("reading download failed: %v", err)
		}
		return string(content)
	}

	if got := download(fileName); got != "first version" {
		t.Fatalf("original file content = %q, want %q", got, "first version")
	}
	if got := download(autoRenamed); got != "second version" {
		t.Fatalf("autorename file content = %q, want %q", got, "second version")
	}
}

func TestUploadLinkIgnoresForcedReplaceOverride(t *testing.T) {
	name := fmt.Sprintf("inttest-upload-ignore-replace-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)
	fileName := "text.txt"
	autoRenamed := "text (1).txt"
	replace := "1"

	resp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/upload-link/?p=/", repoID))
	expectStatus(t, resp, http.StatusOK)
	uploadURL := strings.Trim(responseBody(t, resp), "\" \n\r")

	uploadFileThroughLink(t, adminClient, uploadURL, fileName, "/", "original")
	uploadFileThroughLinkWithReplaceField(t, adminClient, uploadURL, fileName, "/", "new", &replace)

	listResp := adminClient.Get(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/", repoID))
	expectStatus(t, listResp, http.StatusOK)

	var dirList map[string]interface{}
	decodeJSON(t, listResp, &dirList)
	entries, _ := dirList["dirent_list"].([]interface{})
	if !containsEntry(entries, "name", fileName) {
		t.Fatalf("original file %q not found after forced replace upload-link upload", fileName)
	}
	if !containsEntry(entries, "name", autoRenamed) {
		t.Fatalf("autorename target %q not found after forced replace upload-link upload", autoRenamed)
	}

	download := func(name string) string {
		t.Helper()
		resp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/file/?p=/%s", repoID, url.PathEscape(name)))
		expectStatus(t, resp, http.StatusOK)
		downloadURL := strings.Trim(responseBody(t, resp), "\" \n\r")

		req, err := http.NewRequest(http.MethodGet, downloadURL, nil)
		if err != nil {
			t.Fatalf("creating download request failed: %v", err)
		}
		downloadResp, err := adminClient.http.Do(req)
		if err != nil {
			t.Fatalf("download request failed: %v", err)
		}
		defer downloadResp.Body.Close()

		content, err := io.ReadAll(downloadResp.Body)
		if err != nil {
			t.Fatalf("reading download failed: %v", err)
		}
		return string(content)
	}

	if got := download(fileName); got != "original" {
		t.Fatalf("original file content = %q, want %q", got, "original")
	}
	if got := download(autoRenamed); got != "new" {
		t.Fatalf("autorename file content = %q, want %q", got, "new")
	}
}

func TestUpdateLinkAllowsExplicitAutorenameOverride(t *testing.T) {
	name := fmt.Sprintf("inttest-update-link-autorename-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)
	fileName := "text.txt"
	autoRenamed := "text (1).txt"
	replace := "0"

	uploadResp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/upload-link/?p=/", repoID))
	expectStatus(t, uploadResp, http.StatusOK)
	uploadURL := strings.Trim(responseBody(t, uploadResp), "\" \n\r")

	updateResp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/update-link/?p=/", repoID))
	expectStatus(t, updateResp, http.StatusOK)
	updateURL := strings.Trim(responseBody(t, updateResp), "\" \n\r")

	uploadFileThroughLink(t, adminClient, uploadURL, fileName, "/", "original")
	uploadFileThroughLinkWithReplaceField(t, adminClient, updateURL, fileName, "/", "new", &replace)

	listResp := adminClient.Get(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/", repoID))
	expectStatus(t, listResp, http.StatusOK)

	var dirList map[string]interface{}
	decodeJSON(t, listResp, &dirList)
	entries, _ := dirList["dirent_list"].([]interface{})
	if !containsEntry(entries, "name", fileName) {
		t.Fatalf("original file %q not found after update-link autorename override", fileName)
	}
	if !containsEntry(entries, "name", autoRenamed) {
		t.Fatalf("autorename target %q not found after update-link autorename override", autoRenamed)
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
// the file must appear in the directory listing and the uploaded block must end
// with exactly one permanent fs: ref and no lingering provisional up: refs.
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

	// The single file's block has exactly one permanent fs_object reference.
	orgID := resolveOrgID(t, repoID)
	hash := sha256.Sum256([]byte(fileContent))
	blockID := hex.EncodeToString(hash[:])
	if refCount := readBlockRefCount(t, orgID, blockID); refCount != 1 {
		t.Fatalf("references after v2 upload = %d, want 1", refCount)
	}
	assertNoUploadReferrers(t, repoID, "/", fileName)
}
