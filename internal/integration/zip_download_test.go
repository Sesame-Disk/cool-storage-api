//go:build integration

package integration

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestRegionPinnedZipDownload(t *testing.T) {
	const requestHost = "eu.sesamefs.local"
	name := fmt.Sprintf("inttest-zip-region-%d", time.Now().UnixNano())
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

	fileName := "zip-region-test.txt"
	fileContent := "zip download region verification\n"

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

	zipTaskResp := adminClient.PostJSON(t, fmt.Sprintf("/api/v2.1/repos/%s/zip-task/?p=/", repoID), map[string]string{})
	expectStatus(t, zipTaskResp, http.StatusOK)
	zipTaskPayload := responseJSON(t, zipTaskResp)
	zipToken, _ := zipTaskPayload["zip_token"].(string)
	if zipToken == "" {
		t.Fatalf("expected zip_token in zip task response: %v", zipTaskPayload)
	}

	zipResp := adminClient.GetWithHost(t, fmt.Sprintf("/seafhttp/zip/%s", zipToken), requestHost)
	expectStatus(t, zipResp, http.StatusOK)
	zipBody, err := io.ReadAll(zipResp.Body)
	zipResp.Body.Close()
	if err != nil {
		t.Fatalf("reading zip response failed: %v", err)
	}

	reader, err := zip.NewReader(bytes.NewReader(zipBody), int64(len(zipBody)))
	if err != nil {
		t.Fatalf("opening zip response failed: %v", err)
	}

	if len(reader.File) != 1 {
		t.Fatalf("zip entry count = %d, want 1", len(reader.File))
	}
	if reader.File[0].Name != fileName {
		t.Fatalf("zip entry name = %q, want %q", reader.File[0].Name, fileName)
	}

	rc, err := reader.File[0].Open()
	if err != nil {
		t.Fatalf("opening zipped file failed: %v", err)
	}
	content, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		t.Fatalf("reading zipped file failed: %v", err)
	}
	if string(content) != fileContent {
		t.Fatalf("zipped file content = %q, want %q", string(content), fileContent)
	}
}

func TestZipDownloadFailsBeforeHeadersWhenMappingIsMissing(t *testing.T) {
	requireCassandra(t)

	const requestHost = "eu.sesamefs.local"
	name := fmt.Sprintf("inttest-zip-mapping-%d", time.Now().UnixNano())
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

	fileName := "zip-missing-mapping.txt"
	fileContent := fmt.Sprintf("zip missing mapping %d\n", time.Now().UnixNano())

	resp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/upload-link/?p=/", repoID))
	expectStatus(t, resp, http.StatusOK)
	uploadURL := strings.Trim(responseBody(t, resp), "\" \n\r")
	uploadFileThroughLink(t, adminClient, uploadURL, fileName, "/", fileContent)

	orgID := resolveOrgID(t, repoID)
	session := shareProjectionDBForTest(t).Session()

	var headCommit string
	if err := session.Query(`SELECT head_commit_id FROM libraries WHERE org_id = ? AND library_id = ?`, orgID, repoID).Scan(&headCommit); err != nil {
		t.Fatalf("failed to read head commit for %s: %v", repoID, err)
	}

	var rootFSID string
	if err := session.Query(`SELECT root_fs_id FROM commits WHERE library_id = ? AND commit_id = ?`, repoID, headCommit).Scan(&rootFSID); err != nil {
		t.Fatalf("failed to read root fs for %s: %v", repoID, err)
	}

	var dirEntriesJSON string
	if err := session.Query(`SELECT dir_entries FROM fs_objects WHERE library_id = ? AND fs_id = ?`, repoID, rootFSID).Scan(&dirEntriesJSON); err != nil {
		t.Fatalf("failed to read root dir entries for %s: %v", repoID, err)
	}

	var dirEntries []map[string]interface{}
	if err := json.Unmarshal([]byte(dirEntriesJSON), &dirEntries); err != nil {
		t.Fatalf("failed to decode root dir entries: %v", err)
	}

	var fileFSID string
	for _, entry := range dirEntries {
		name, _ := entry["name"].(string)
		if name != fileName {
			continue
		}
		fileFSID, _ = entry["id"].(string)
		break
	}
	if fileFSID == "" {
		t.Fatalf("uploaded file %q not found in root dir entries: %s", fileName, dirEntriesJSON)
	}

	var blockIDs []string
	if err := session.Query(`SELECT block_ids FROM fs_objects WHERE library_id = ? AND fs_id = ?`, repoID, fileFSID).Scan(&blockIDs); err != nil {
		t.Fatalf("failed to read block ids for %s: %v", fileFSID, err)
	}
	if len(blockIDs) == 0 {
		t.Fatalf("expected block ids for uploaded file %q", fileName)
	}

	brokenMapping := blockIDs[0]
	if err := session.Query(`DELETE FROM block_id_mappings WHERE org_id = ? AND external_id = ?`, orgID, brokenMapping).Exec(); err != nil {
		t.Fatalf("failed to delete block mapping %s: %v", brokenMapping, err)
	}

	zipTaskResp := adminClient.PostJSON(t, fmt.Sprintf("/api/v2.1/repos/%s/zip-task/?p=/", repoID), map[string]string{})
	expectStatus(t, zipTaskResp, http.StatusOK)
	zipTaskPayload := responseJSON(t, zipTaskResp)
	zipToken, _ := zipTaskPayload["zip_token"].(string)
	if zipToken == "" {
		t.Fatalf("expected zip_token in zip task response: %v", zipTaskPayload)
	}

	zipResp := adminClient.GetWithHost(t, fmt.Sprintf("/seafhttp/zip/%s", zipToken), requestHost)
	expectStatus(t, zipResp, http.StatusInternalServerError)
	if got := zipResp.Header.Get("Content-Type"); strings.Contains(got, "application/zip") {
		zipResp.Body.Close()
		t.Fatalf("expected JSON error response before zip headers, got Content-Type %q", got)
	}
	zipBody := responseBody(t, zipResp)
	if !strings.Contains(zipBody, "failed to prepare zip download") {
		t.Fatalf("zip error body = %q, want prepare failure", zipBody)
	}
	if strings.Contains(zipBody, brokenMapping) {
		t.Fatalf("zip error body leaked internal block mapping details: %q", zipBody)
	}
	zipResp.Body.Close()
}
