//go:build integration

package integration

import (
	"archive/zip"
	"bytes"
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
