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

	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
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

func TestZipDownloadFailsBeforeHeadersWhenLegacyMappingIsMissing(t *testing.T) {
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
	var seafileBlockIDs []string
	if err := session.Query(`SELECT block_ids, seafile_block_ids_sha1 FROM fs_objects WHERE library_id = ? AND fs_id = ?`, repoID, fileFSID).Scan(&blockIDs, &seafileBlockIDs); err != nil {
		t.Fatalf("failed to read block ids for %s: %v", fileFSID, err)
	}
	if len(blockIDs) == 0 {
		t.Fatalf("expected block ids for uploaded file %q", fileName)
	}
	if len(seafileBlockIDs) == 0 {
		t.Fatalf("expected seafile block ids for uploaded file %q", fileName)
	}

	// Post-flip canonical rows keep ZIP downloads off the SHA-1 mapping path.
	// Force a legacy SHA-1-only row so the preflight still exercises the strict
	// "fail before headers when resolution is broken" contract.
	if err := session.Query(`
		UPDATE fs_objects SET block_ids = ?, seafile_block_ids_sha1 = ? WHERE library_id = ? AND fs_id = ?
	`, seafileBlockIDs, []string{}, repoID, fileFSID).Exec(); err != nil {
		t.Fatalf("failed to force legacy block id layout for %s: %v", fileFSID, err)
	}

	brokenMapping := seafileBlockIDs[0]
	if err := session.Query(`DELETE FROM block_id_mappings WHERE org_id = ? AND representation_id = ? AND external_id = ?`, orgID, dbpkg.PlainBlockRepresentationID, brokenMapping).Exec(); err != nil {
		t.Fatalf("failed to delete block mapping %s: %v", brokenMapping, err)
	}

	// Undo BOTH corruptions before the library is torn down. This fixture is why the full
	// integration suite used to strand an eternal block (ISSUE-GC-TEST-RESIDUE-01 / 1G):
	// `block_references` is keyed by the canonical SHA-256, but the legacy layout forced
	// above leaves fs_objects.block_ids holding SHA-1s, and the mapping that would resolve
	// SHA-1 → SHA-256 is deleted right here. So the library cascade read SHA-1s, released
	// refs that do not exist, and left the real `fs:` ref in place — pinning the block and
	// its S3 object with the library gone from both indexes, which no GC phase can ever
	// rediscover (the F1/P7 shape).
	//
	// Registered AFTER createTestLibrary's cleanup so LIFO runs it first: the cascade then
	// sees canonical SHA-256 block_ids and releases the ref through the real GC path.
	// IF EXISTS keeps the restore from resurrecting rows GC already reclaimed.
	t.Cleanup(func() {
		if err := session.Query(`
			UPDATE fs_objects SET block_ids = ?, seafile_block_ids_sha1 = ? WHERE library_id = ? AND fs_id = ? IF EXISTS
		`, blockIDs, seafileBlockIDs, repoID, fileFSID).Exec(); err != nil {
			t.Errorf("restore canonical block ids for %s/%s before teardown: %v", repoID, fileFSID, err)
		}
		if err := session.Query(`
			INSERT INTO block_id_mappings (org_id, representation_id, external_id, internal_id, created_at) VALUES (?, ?, ?, ?, ?)
		`, orgID, dbpkg.PlainBlockRepresentationID, brokenMapping, blockIDs[0], time.Now().UTC()).Exec(); err != nil {
			t.Errorf("restore block mapping %s before teardown: %v", brokenMapping, err)
		}
	})

	zipTaskResp := adminClient.PostJSON(t, fmt.Sprintf("/api/v2.1/repos/%s/zip-task/?p=/", repoID), map[string]string{})
	expectStatus(t, zipTaskResp, http.StatusOK)
	zipTaskPayload := responseJSON(t, zipTaskResp)
	zipToken, _ := zipTaskPayload["zip_token"].(string)
	if zipToken == "" {
		t.Fatalf("expected zip_token in zip task response: %v", zipTaskPayload)
	}

	zipResp := adminClient.GetWithHost(t, fmt.Sprintf("/seafhttp/zip/%s", zipToken), requestHost)
	expectStatus(t, zipResp, http.StatusServiceUnavailable)
	if got := zipResp.Header.Get("Retry-After"); got == "" {
		zipResp.Body.Close()
		t.Fatal("expected Retry-After on retryable zip preparation failure")
	}
	if got := zipResp.Header.Get("Content-Type"); strings.Contains(got, "application/zip") {
		zipResp.Body.Close()
		t.Fatalf("expected JSON error response before zip headers, got Content-Type %q", got)
	}
	zipBody := responseBody(t, zipResp)
	if !strings.Contains(zipBody, "temporarily unavailable") {
		t.Fatalf("zip error body = %q, want retryable failure", zipBody)
	}
	if strings.Contains(zipBody, brokenMapping) {
		t.Fatalf("zip error body leaked internal block mapping details: %q", zipBody)
	}
	zipResp.Body.Close()
}
