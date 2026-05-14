//go:build integration

package integration

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

const (
	defaultOrgID           = "00000000-0000-0000-0000-000000000001"
	defaultAdminEmail      = "admin@sesamefs.local"
	defaultUserEmail       = "user@sesamefs.local"
	pollInterval           = 200 * time.Millisecond
	pollTimeout            = 5 * time.Second
	testStorageQuota       = int64(333000000000)
	testTrafficQuota       = int64(100000000)
	testTrafficUpload      = int64(70000000)
	testTrafficDownload    = int64(80000000)
	invalidTrafficUpload   = int64(60000000)
	invalidTrafficDownload = int64(50000000)
)

func TestQuotaFieldsAppearInUserResponses(t *testing.T) {
	original := getAdminOrganizationInfo(t, defaultOrgID)
	restoreOrgQuotasOnCleanup(t, original)

	updateAdminOrganizationQuotas(t, defaultOrgID, map[string]interface{}{
		"storage_quota":          testStorageQuota,
		"traffic_quota":          testTrafficQuota,
		"traffic_upload_quota":   testTrafficUpload,
		"traffic_download_quota": testTrafficDownload,
	})

	waitForCondition(t, "org quotas visible in admin user response", func() bool {
		payload := getAdminUserByEmail(t, defaultAdminEmail)
		return jsonInt64(payload, "org_storage_quota") == testStorageQuota &&
			jsonInt64(payload, "org_traffic_quota") == testTrafficQuota &&
			jsonInt64(payload, "org_traffic_upload_quota") == testTrafficUpload &&
			jsonInt64(payload, "org_traffic_download_quota") == testTrafficDownload
	})

	t.Run("sysadmin user response exposes org quota fields", func(t *testing.T) {
		payload := getAdminUserByEmail(t, defaultAdminEmail)
		assertQuotaFields(t, payload)
	})

	t.Run("org admin user response exposes org quota fields", func(t *testing.T) {
		payload := getOrgAdminUserByEmail(t, defaultAdminEmail)
		assertQuotaFields(t, payload)
	})
}

func TestInvalidUserQuotaUpdatesAreRejected(t *testing.T) {
	original := getAdminOrganizationInfo(t, defaultOrgID)
	restoreOrgQuotasOnCleanup(t, original)

	updateAdminOrganizationQuotas(t, defaultOrgID, map[string]interface{}{
		"storage_quota":          testStorageQuota,
		"traffic_quota":          testTrafficQuota,
		"traffic_upload_quota":   0,
		"traffic_download_quota": 0,
	})

	body := map[string]interface{}{
		"traffic_upload_quota":   invalidTrafficUpload,
		"traffic_download_quota": invalidTrafficDownload,
	}

	t.Run("admin users email update rejects combined overflow", func(t *testing.T) {
		resp := superadminClient.PutJSON(t, "/api/v2.1/admin/users/"+url.PathEscape(defaultUserEmail)+"/", body)
		expectStatus(t, resp, http.StatusBadRequest)
		body := responseJSON(t, resp)
		if body["error"] != "upload + download quota sum (110000000) exceeds organization combined traffic limit (100000000)" {
			t.Fatalf("unexpected error: %v", body["error"])
		}
	})

	t.Run("platform admin org user update rejects combined overflow", func(t *testing.T) {
		resp := superadminClient.PutJSON(t, "/api/v2.1/admin/organizations/"+defaultOrgID+"/users/"+url.PathEscape(defaultUserEmail)+"/", body)
		expectStatus(t, resp, http.StatusBadRequest)
		body := responseJSON(t, resp)
		if body["error"] != "upload + download quota sum (110000000) exceeds organization combined traffic limit (100000000)" {
			t.Fatalf("unexpected error: %v", body["error"])
		}
	})

	t.Run("org admin user update rejects combined overflow", func(t *testing.T) {
		resp := adminClient.PutJSON(t, "/api/v2.1/org/"+defaultOrgID+"/admin/users/"+url.PathEscape(defaultUserEmail)+"/", body)
		expectStatus(t, resp, http.StatusForbidden)
		body := responseJSON(t, resp)
		if body["error"] != "organization membership and user lifecycle are managed by Accounts; org-admin user writes are disabled" {
			t.Fatalf("unexpected error: %v", body["error"])
		}
	})
}

func TestPerUserStorageQuotaBlocksUploadBeforeOrgQuota(t *testing.T) {
	originalOrg := getAdminOrganizationInfo(t, defaultOrgID)
	originalUser := getAdminUserByEmail(t, defaultUserEmail)

	t.Cleanup(func() {
		// Restore the user first while the org cap is still high; older seeded
		// dev data may have a per-user quota above the original org cap.
		restoreResp := superadminClient.PutJSON(t, "/api/v2.1/admin/users/"+url.PathEscape(defaultUserEmail)+"/", map[string]interface{}{
			"quota_total": jsonInt64(originalUser, "quota_total"),
		})
		if restoreResp.StatusCode != http.StatusOK {
			t.Logf("restore default user quota returned status %d body=%s", restoreResp.StatusCode, responseBody(t, restoreResp))
		} else {
			restoreResp.Body.Close()
		}
		restoreOrgBody := map[string]interface{}{
			"storage_quota":          jsonInt64(originalOrg, "storage_quota"),
			"traffic_quota":          jsonInt64(originalOrg, "traffic_quota"),
			"traffic_upload_quota":   jsonInt64(originalOrg, "traffic_upload_quota"),
			"traffic_download_quota": jsonInt64(originalOrg, "traffic_download_quota"),
		}
		if quotaPolicy := jsonString(originalOrg, "quota_policy"); quotaPolicy != "" {
			restoreOrgBody["quota_policy"] = quotaPolicy
		}
		updateAdminOrganizationQuotas(t, defaultOrgID, restoreOrgBody)
	})

	updateAdminOrganizationQuotas(t, defaultOrgID, map[string]interface{}{
		"storage_quota": int64(1 << 50),
		"quota_policy":  "hard",
	})

	updateResp := superadminClient.PutJSON(t, "/api/v2.1/admin/users/"+url.PathEscape(defaultUserEmail)+"/", map[string]interface{}{
		"quota_total": int64(1),
	})
	expectStatus(t, updateResp, http.StatusOK)
	updateResp.Body.Close()

	name := fmt.Sprintf("inttest-user-storage-quota-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, userClient, name)

	resp := userClient.Get(t, fmt.Sprintf("/api2/repos/%s/upload-link/?p=/", repoID))
	expectStatus(t, resp, http.StatusOK)
	uploadURL := strings.Trim(responseBody(t, resp), "\" \n\r")

	status, body := uploadFileThroughLinkStatus(t, userClient, uploadURL, "blocked-by-user-quota.txt", "/", "this upload should exceed the one byte per-user storage cap")
	if status != http.StatusForbidden {
		t.Fatalf("upload status = %d, want %d; body=%s", status, http.StatusForbidden, body)
	}
	if !strings.Contains(body, "storage quota exceeded") {
		t.Fatalf("upload body = %q, want storage quota exceeded", body)
	}
}

func TestDeduplicatedBlockUploadSkipsStorageQuota(t *testing.T) {
	originalOrg := getAdminOrganizationInfo(t, defaultOrgID)
	originalUser := getAdminUserByEmail(t, defaultUserEmail)

	t.Cleanup(func() {
		restoreResp := superadminClient.PutJSON(t, "/api/v2.1/admin/users/"+url.PathEscape(defaultUserEmail)+"/", map[string]interface{}{
			"quota_total": jsonInt64(originalUser, "quota_total"),
		})
		if restoreResp.StatusCode != http.StatusOK {
			t.Logf("restore default user quota returned status %d body=%s", restoreResp.StatusCode, responseBody(t, restoreResp))
		} else {
			restoreResp.Body.Close()
		}
		restoreOrgBody := map[string]interface{}{
			"storage_quota":          jsonInt64(originalOrg, "storage_quota"),
			"traffic_quota":          jsonInt64(originalOrg, "traffic_quota"),
			"traffic_upload_quota":   jsonInt64(originalOrg, "traffic_upload_quota"),
			"traffic_download_quota": jsonInt64(originalOrg, "traffic_download_quota"),
		}
		if quotaPolicy := jsonString(originalOrg, "quota_policy"); quotaPolicy != "" {
			restoreOrgBody["quota_policy"] = quotaPolicy
		}
		updateAdminOrganizationQuotas(t, defaultOrgID, restoreOrgBody)
	})

	blockContent := []byte(fmt.Sprintf("deduplicated block uploads should not consume additional storage quota %d", time.Now().UnixNano()))

	updateAdminOrganizationQuotas(t, defaultOrgID, map[string]interface{}{
		"storage_quota": int64(1 << 50),
		"quota_policy":  "hard",
	})

	updateResp := superadminClient.PutJSON(t, "/api/v2.1/admin/users/"+url.PathEscape(defaultUserEmail)+"/", map[string]interface{}{
		"quota_total": int64(len(blockContent) + 16),
	})
	expectStatus(t, updateResp, http.StatusOK)
	updateResp.Body.Close()

	firstStatus, firstBody := uploadRawBlockStatus(t, userClient, blockContent)
	if firstStatus != http.StatusCreated {
		t.Fatalf("first block upload status = %d, want %d; body=%s", firstStatus, http.StatusCreated, firstBody)
	}
	if got, ok := firstBody["new"].(bool); !ok || !got {
		t.Fatalf("first block upload new = %v, want true; body=%v", firstBody["new"], firstBody)
	}

	updateResp = superadminClient.PutJSON(t, "/api/v2.1/admin/users/"+url.PathEscape(defaultUserEmail)+"/", map[string]interface{}{
		"quota_total": int64(1),
	})
	expectStatus(t, updateResp, http.StatusOK)
	updateResp.Body.Close()

	secondStatus, secondBody := uploadRawBlockStatus(t, userClient, blockContent)
	if secondStatus != http.StatusOK {
		t.Fatalf("deduplicated block upload status = %d, want %d; body=%v", secondStatus, http.StatusOK, secondBody)
	}
	if got, ok := secondBody["new"].(bool); !ok || got {
		t.Fatalf("deduplicated block upload new = %v, want false; body=%v", secondBody["new"], secondBody)
	}
	if got, ok := secondBody["size"].(float64); !ok || int64(got) != int64(len(blockContent)) {
		t.Fatalf("deduplicated block upload size = %v, want %d; body=%v", secondBody["size"], len(blockContent), secondBody)
	}
	if _, hasError := secondBody["error"]; hasError {
		t.Fatalf("deduplicated block upload returned error payload: %v", secondBody)
	}
}

func TestDeduplicatedSyncBlockUploadSkipsStorageQuota(t *testing.T) {
	originalOrg := getAdminOrganizationInfo(t, defaultOrgID)
	originalUser := getAdminUserByEmail(t, defaultUserEmail)

	t.Cleanup(func() {
		restoreResp := superadminClient.PutJSON(t, "/api/v2.1/admin/users/"+url.PathEscape(defaultUserEmail)+"/", map[string]interface{}{
			"quota_total": jsonInt64(originalUser, "quota_total"),
		})
		if restoreResp.StatusCode != http.StatusOK {
			t.Logf("restore default user quota returned status %d body=%s", restoreResp.StatusCode, responseBody(t, restoreResp))
		} else {
			restoreResp.Body.Close()
		}
		restoreOrgBody := map[string]interface{}{
			"storage_quota":          jsonInt64(originalOrg, "storage_quota"),
			"traffic_quota":          jsonInt64(originalOrg, "traffic_quota"),
			"traffic_upload_quota":   jsonInt64(originalOrg, "traffic_upload_quota"),
			"traffic_download_quota": jsonInt64(originalOrg, "traffic_download_quota"),
		}
		if quotaPolicy := jsonString(originalOrg, "quota_policy"); quotaPolicy != "" {
			restoreOrgBody["quota_policy"] = quotaPolicy
		}
		updateAdminOrganizationQuotas(t, defaultOrgID, restoreOrgBody)
	})

	blockContent := []byte(fmt.Sprintf("deduplicated sync block uploads should not consume additional storage quota %d", time.Now().UnixNano()))

	updateAdminOrganizationQuotas(t, defaultOrgID, map[string]interface{}{
		"storage_quota": int64(1 << 50),
		"quota_policy":  "hard",
	})

	updateResp := superadminClient.PutJSON(t, "/api/v2.1/admin/users/"+url.PathEscape(defaultUserEmail)+"/", map[string]interface{}{
		"quota_total": int64(len(blockContent) + 16),
	})
	expectStatus(t, updateResp, http.StatusOK)
	updateResp.Body.Close()

	repoID := createTestLibrary(t, userClient, fmt.Sprintf("inttest-sync-block-quota-%d", time.Now().UnixNano()))

	firstStatus, firstBody := uploadSyncBlockStatus(t, userClient, repoID, blockContent)
	if firstStatus != http.StatusOK {
		t.Fatalf("first sync block upload status = %d, want %d; body=%s", firstStatus, http.StatusOK, firstBody)
	}

	updateResp = superadminClient.PutJSON(t, "/api/v2.1/admin/users/"+url.PathEscape(defaultUserEmail)+"/", map[string]interface{}{
		"quota_total": int64(1),
	})
	expectStatus(t, updateResp, http.StatusOK)
	updateResp.Body.Close()

	secondStatus, secondBody := uploadSyncBlockStatus(t, userClient, repoID, blockContent)
	if secondStatus != http.StatusOK {
		t.Fatalf("deduplicated sync block upload status = %d, want %d; body=%s", secondStatus, http.StatusOK, secondBody)
	}
	if strings.Contains(secondBody, "storage quota exceeded") {
		t.Fatalf("deduplicated sync block upload returned storage quota error: %s", secondBody)
	}
}

func getAdminOrganizationInfo(t *testing.T, orgID string) map[string]interface{} {
	t.Helper()
	resp := superadminClient.Get(t, "/api/v2.1/admin/organizations/"+orgID+"/")
	expectStatus(t, resp, http.StatusOK)
	return responseJSON(t, resp)
}

func updateAdminOrganizationQuotas(t *testing.T, orgID string, body map[string]interface{}) {
	t.Helper()
	resp := superadminClient.PutJSON(t, "/api/v2.1/admin/organizations/"+orgID+"/", body)
	expectStatus(t, resp, http.StatusOK)
	resp.Body.Close()
}

func restoreOrgQuotasOnCleanup(t *testing.T, original map[string]interface{}) {
	t.Helper()
	t.Cleanup(func() {
		updateAdminOrganizationQuotas(t, defaultOrgID, map[string]interface{}{
			"storage_quota":          jsonInt64(original, "storage_quota"),
			"traffic_quota":          jsonInt64(original, "traffic_quota"),
			"traffic_upload_quota":   jsonInt64(original, "traffic_upload_quota"),
			"traffic_download_quota": jsonInt64(original, "traffic_download_quota"),
		})
	})
}

func getAdminUserByEmail(t *testing.T, email string) map[string]interface{} {
	t.Helper()
	resp := superadminClient.Get(t, "/api/v2.1/admin/users/"+url.PathEscape(email)+"/")
	expectStatus(t, resp, http.StatusOK)
	return responseJSON(t, resp)
}

func getOrgAdminUserByEmail(t *testing.T, email string) map[string]interface{} {
	t.Helper()
	resp := adminClient.Get(t, "/api/v2.1/org/"+defaultOrgID+"/admin/users/"+url.PathEscape(email)+"/")
	expectStatus(t, resp, http.StatusOK)
	return responseJSON(t, resp)
}

func waitForCondition(t *testing.T, description string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(pollTimeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(pollInterval)
	}
	t.Fatalf("timed out waiting for %s", description)
}

func jsonInt64(payload map[string]interface{}, key string) int64 {
	if raw, ok := payload[key]; ok {
		if value, ok := raw.(float64); ok {
			return int64(value)
		}
	}
	return 0
}

func uploadFileThroughLinkStatus(t *testing.T, c *testClient, uploadURL, fileName, parentDir, content string) (int, string) {
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

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading upload response failed: %v", err)
	}
	return resp.StatusCode, string(body)
}

func uploadRawBlockStatus(t *testing.T, c *testClient, content []byte) (int, map[string]interface{}) {
	t.Helper()

	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/api/v2/blocks/upload", bytes.NewReader(content))
	if err != nil {
		t.Fatalf("creating block upload request failed: %v", err)
	}
	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.ContentLength = int64(len(content))

	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatalf("block upload request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading block upload response failed: %v", err)
	}

	payload := map[string]interface{}{}
	if len(bytes.TrimSpace(body)) > 0 {
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("decoding block upload response failed: %v body=%s", err, string(body))
		}
	}

	return resp.StatusCode, payload
}

func uploadSyncBlockStatus(t *testing.T, c *testClient, repoID string, content []byte) (int, string) {
	t.Helper()

	hash := sha256.Sum256(content)
	blockID := hex.EncodeToString(hash[:])
	req, err := http.NewRequest(
		http.MethodPut,
		fmt.Sprintf("%s/seafhttp/repo/%s/block/%s?hash_type=sha256", c.baseURL, repoID, blockID),
		bytes.NewReader(content),
	)
	if err != nil {
		t.Fatalf("creating sync block upload request failed: %v", err)
	}
	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.ContentLength = int64(len(content))

	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatalf("sync block upload request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading sync block upload response failed: %v", err)
	}

	return resp.StatusCode, string(body)
}

func assertQuotaFields(t *testing.T, payload map[string]interface{}) {
	t.Helper()
	if got := jsonInt64(payload, "org_storage_quota"); got != testStorageQuota {
		t.Fatalf("org_storage_quota = %d, want %d", got, testStorageQuota)
	}
	if got := jsonInt64(payload, "org_traffic_quota"); got != testTrafficQuota {
		t.Fatalf("org_traffic_quota = %d, want %d", got, testTrafficQuota)
	}
	if got := jsonInt64(payload, "org_traffic_upload_quota"); got != testTrafficUpload {
		t.Fatalf("org_traffic_upload_quota = %d, want %d", got, testTrafficUpload)
	}
	if got := jsonInt64(payload, "org_traffic_download_quota"); got != testTrafficDownload {
		t.Fatalf("org_traffic_download_quota = %d, want %d", got, testTrafficDownload)
	}
}
