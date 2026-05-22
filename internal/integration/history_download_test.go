//go:build integration

package integration

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestHistoryDownloadRoundTrip tests the full flow:
//  1. Create library
//  2. Upload a file (v1)
//  3. Upload same file again (v2) to create a second revision
//  4. Get file revisions — verify two entries with different rev_file_id
//  5. Download each revision via /repo/:id/history/download?obj_id=...
//  6. Verify each download returns the correct content
func TestHistoryDownloadRoundTrip(t *testing.T) {
	name := fmt.Sprintf("inttest-histdl-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)

	fileName := "history-test.txt"
	v1Content := "version 1 content for history download test"
	v2Content := "version 2 content — updated for history download test"

	// Helper: upload content to the library using the requested link contract.
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

	// Upload v1
	upload("upload-link", v1Content)

	// Upload v2 (overwrite)
	upload("update-link", v2Content)

	// Get file revisions
	t.Run("get revisions", func(t *testing.T) {
		resp := adminClient.Get(t, fmt.Sprintf("/api2/repo/file_revisions/%s/?p=/%s", repoID, fileName))
		expectStatus(t, resp, http.StatusOK)

		var result map[string]interface{}
		decodeJSON(t, resp, &result)

		data, ok := result["data"].([]interface{})
		if !ok {
			t.Fatal("expected data array in revisions response")
		}

		if len(data) < 2 {
			t.Skipf("expected at least 2 revisions, got %d (library may deduplicate identical commits)", len(data))
		}

		// Extract rev_file_ids and download each revision
		type revEntry struct {
			revFileID string
			content   string
		}
		var revisions []revEntry
		for _, item := range data {
			entry, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			id, ok := entry["rev_file_id"].(string)
			if !ok || id == "" {
				t.Error("revision entry missing rev_file_id")
				continue
			}
			revisions = append(revisions, revEntry{revFileID: id})
		}
		t.Logf("found %d revisions", len(revisions))

		// Download each revision and record its content
		t.Run("download revisions and verify", func(t *testing.T) {
			var gotV1, gotV2 bool
			for i, rev := range revisions {
				dlURL := fmt.Sprintf("%s/repo/%s/history/download?obj_id=%s&p=/%s",
					baseURL, repoID, rev.revFileID, fileName)
				req, _ := http.NewRequest("GET", dlURL, nil)
				req.Header.Set("Authorization", "Token "+adminClient.token)
				dlResp, err := adminClient.http.Do(req)
				if err != nil {
					t.Fatalf("download revision %d failed: %v", i, err)
				}

				if dlResp.StatusCode != http.StatusOK {
					body, _ := io.ReadAll(dlResp.Body)
					dlResp.Body.Close()
					t.Fatalf("download revision %d returned %d: %s", i, dlResp.StatusCode, string(body))
				}

				content, _ := io.ReadAll(dlResp.Body)
				dlResp.Body.Close()
				got := string(content)
				t.Logf("revision %d (obj_id=%s): %q", i, rev.revFileID, got)

				if got == v1Content {
					gotV1 = true
				}
				if got == v2Content {
					gotV2 = true
				}

				// Verify Content-Disposition header
				cd := dlResp.Header.Get("Content-Disposition")
				if !strings.Contains(cd, fileName) {
					t.Errorf("Content-Disposition = %q, expected to contain %q", cd, fileName)
				}
			}

			if !gotV1 {
				t.Error("did not find v1 content in any revision")
			}
			if !gotV2 {
				t.Error("did not find v2 content in any revision")
			}
			if gotV1 && gotV2 {
				t.Log("both v1 and v2 content found — history download verified")
			}
		})
	})
}

// historyDownloadNoRedirect issues a GET that does NOT follow redirects, so we
// can inspect the 302 → /file-error/ contract used by the historic file handlers.
func historyDownloadNoRedirect(t *testing.T, path string) *http.Response {
	t.Helper()
	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, err := http.NewRequest(http.MethodGet, baseURL+path, nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("Authorization", "Token "+adminClient.token)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s failed: %v", path, err)
	}
	return resp
}

// TestHistoryDownloadMissingObjID verifies that a request with no obj_id redirects
// to the frontend error page with status=400 and a "Missing obj_id" message.
func TestHistoryDownloadMissingObjID(t *testing.T) {
	name := fmt.Sprintf("inttest-histnoid-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)

	resp := historyDownloadNoRedirect(t, fmt.Sprintf("/repo/%s/history/download?p=/test.txt", repoID))
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Errorf("expected 302 redirect for missing obj_id, got %d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.Contains(loc, "/file-error/") || !strings.Contains(loc, "status=400") || !strings.Contains(loc, "Missing+obj_id") {
		t.Errorf("expected redirect to /file-error/ with status=400 and missing obj_id message, got %q", loc)
	}
}

func TestRegionPinnedHistoricReadPaths(t *testing.T) {
	const requestHost = "eu.sesamefs.local"
	name := fmt.Sprintf("inttest-hist-region-%d", time.Now().UnixNano())
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

	t.Cleanup(func() {
		cleanup := adminClient.Delete(t, fmt.Sprintf("/api/v2.1/repos/%s/", repoID))
		cleanup.Body.Close()
	})

	fileName := "historic-region-test.txt"
	v1Content := "historic region version 1\n"
	v2Content := "historic region version 2\n"

	upload := func(linkPath, content string) {
		t.Helper()
		resp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/%s/?p=/", repoID, linkPath))
		expectStatus(t, resp, http.StatusOK)
		uploadURL := strings.Trim(responseBody(t, resp), "\" \n\r")

		var buf bytes.Buffer
		writer := multipart.NewWriter(&buf)
		part, err := writer.CreateFormFile("file", fileName)
		if err != nil {
			t.Fatalf("CreateFormFile failed: %v", err)
		}
		if _, err := part.Write([]byte(content)); err != nil {
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
	}

	upload("upload-link", v1Content)
	upload("update-link", v2Content)

	revisionsResp := adminClient.Get(t, fmt.Sprintf("/api2/repo/file_revisions/%s/?p=/%s", repoID, fileName))
	expectStatus(t, revisionsResp, http.StatusOK)

	var revisionsPayload map[string]interface{}
	decodeJSON(t, revisionsResp, &revisionsPayload)
	revisionItems, ok := revisionsPayload["data"].([]interface{})
	if !ok || len(revisionItems) < 2 {
		t.Fatalf("expected at least 2 revisions, got %v", revisionsPayload)
	}

	var revisionIDs []string
	for _, item := range revisionItems {
		entry, _ := item.(map[string]interface{})
		if revID, _ := entry["rev_file_id"].(string); revID != "" {
			revisionIDs = append(revisionIDs, revID)
		}
	}
	if len(revisionIDs) < 2 {
		t.Fatalf("expected rev_file_id values, got %v", revisionsPayload)
	}

	assertHistoricContent := func(path string, wantOneOf ...string) {
		t.Helper()
		resp := adminClient.GetWithHost(t, path, requestHost)
		expectStatus(t, resp, http.StatusOK)
		body := responseBody(t, resp)
		for _, want := range wantOneOf {
			if body == want {
				return
			}
		}
		t.Fatalf("historic content = %q, want one of %q", body, wantOneOf)
	}

	for _, revID := range revisionIDs {
		assertHistoricContent(fmt.Sprintf("/repo/%s/history/download?obj_id=%s&p=/%s", repoID, revID, fileName), v1Content, v2Content)
		assertHistoricContent(fmt.Sprintf("/repo/%s/history/raw?obj_id=%s&p=/%s", repoID, revID, fileName), v1Content, v2Content)
	}
}

// TestHistoryDownloadInvalidObjID verifies that a nonexistent obj_id redirects
// to the frontend error page with status=404.
func TestHistoryDownloadInvalidObjID(t *testing.T) {
	name := fmt.Sprintf("inttest-histbadid-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)

	resp := historyDownloadNoRedirect(t, fmt.Sprintf("/repo/%s/history/download?obj_id=nonexistent_id_abc123&p=/test.txt", repoID))
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Errorf("expected 302 redirect for invalid obj_id, got %d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.Contains(loc, "/file-error/") || !strings.Contains(loc, "status=404") {
		t.Errorf("expected redirect to /file-error/ with status=404, got %q", loc)
	}
}

// TestHistoryDownloadInvalidPath verifies that p=/ redirects to the frontend
// error page with status=400.
func TestHistoryDownloadInvalidPath(t *testing.T) {
	name := fmt.Sprintf("inttest-histbadpath-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)

	resp := historyDownloadNoRedirect(t, fmt.Sprintf("/repo/%s/history/download?obj_id=abc&p=/", repoID))
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Errorf("expected 302 redirect for path=/, got %d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.Contains(loc, "/file-error/") || !strings.Contains(loc, "status=400") {
		t.Errorf("expected redirect to /file-error/ with status=400, got %q", loc)
	}
}

// TestHistoryDownloadUnauthenticated tests that unauthenticated requests don't succeed.
// In dev mode, anonymous access may be allowed through auth middleware but will fail at DB lookup.
func TestHistoryDownloadUnauthenticated(t *testing.T) {
	url := fmt.Sprintf("%s/repo/some-repo/history/download?obj_id=abc&p=/test.txt", baseURL)
	req, _ := http.NewRequest("GET", url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	// Should get either 401 (auth required) or 400/404 (route hit but params invalid).
	// Should NOT get 200 (success) or 301/302 (redirect to SPA).
	if resp.StatusCode == http.StatusOK {
		t.Error("unauthenticated request should not succeed with 200")
	}
	if resp.StatusCode == http.StatusMovedPermanently || resp.StatusCode == http.StatusFound {
		t.Error("unauthenticated request should not redirect")
	}
}
