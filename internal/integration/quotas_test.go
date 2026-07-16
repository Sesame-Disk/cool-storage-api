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

	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
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
	originalUser := getAdminUserByEmail(t, defaultUserEmail)
	restoreDefaultOrgAndUserQuotasOnCleanup(t, original, originalUser)

	updateAdminOrganizationQuotas(t, defaultOrgID, map[string]interface{}{
		"storage_quota":          testStorageQuota,
		"traffic_quota":          testTrafficQuota,
		"traffic_upload_quota":   0,
		"traffic_download_quota": 0,
	})
	setDefaultUserQuota(t, testStorageQuota)

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
	restoreDefaultOrgAndUserQuotasOnCleanup(t, originalOrg, originalUser)

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
	restoreDefaultOrgAndUserQuotasOnCleanup(t, originalOrg, originalUser)

	blockContent := []byte(fmt.Sprintf("deduplicated block uploads should not consume additional storage quota %d", time.Now().UnixNano()))

	updateAdminOrganizationQuotas(t, defaultOrgID, map[string]interface{}{
		"storage_quota": int64(1 << 50),
		"quota_policy":  "hard",
	})

	baselineUsage := jsonInt64(getAdminUserByEmail(t, defaultUserEmail), "quota_usage")
	updateResp := superadminClient.PutJSON(t, "/api/v2.1/admin/users/"+url.PathEscape(defaultUserEmail)+"/", map[string]interface{}{
		// These integration tests share the default user, so quota_usage may
		// already be non-zero from earlier tests even when cleanup is working.
		// Budget relative to the live baseline instead of assuming an empty user.
		"quota_total": baselineUsage + int64(len(blockContent)) + 16,
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
	restoreDefaultOrgAndUserQuotasOnCleanup(t, originalOrg, originalUser)

	blockContent := []byte(fmt.Sprintf("deduplicated sync block uploads should not consume additional storage quota %d", time.Now().UnixNano()))

	updateAdminOrganizationQuotas(t, defaultOrgID, map[string]interface{}{
		"storage_quota": int64(1 << 50),
		"quota_policy":  "hard",
	})

	baselineUsage := jsonInt64(getAdminUserByEmail(t, defaultUserEmail), "quota_usage")
	updateResp := superadminClient.PutJSON(t, "/api/v2.1/admin/users/"+url.PathEscape(defaultUserEmail)+"/", map[string]interface{}{
		// These integration tests share the default user, so quota_usage may
		// already be non-zero from earlier tests even when cleanup is working.
		// Budget relative to the live baseline instead of assuming an empty user.
		"quota_total": baselineUsage + int64(len(blockContent)) + 16,
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

func TestChunkedWebUploadChecksTotalStorageQuota(t *testing.T) {
	originalOrg := getAdminOrganizationInfo(t, defaultOrgID)
	originalUser := getAdminUserByEmail(t, defaultUserEmail)
	restoreDefaultOrgAndUserQuotasOnCleanup(t, originalOrg, originalUser)

	updateAdminOrganizationQuotas(t, defaultOrgID, map[string]interface{}{
		"storage_quota": int64(1 << 50),
		"quota_policy":  "hard",
	})

	currentUsage := jsonInt64(getAdminUserByEmail(t, defaultUserEmail), "quota_usage")
	setDefaultUserQuota(t, currentUsage+10)

	repoID := createTestLibrary(t, userClient, fmt.Sprintf("inttest-chunked-quota-%d", time.Now().UnixNano()))
	uploadURL := getUploadURL(t, userClient, repoID)

	status, body := uploadChunkThroughLinkStatus(t, userClient, uploadURL, "chunked-quota.txt", "/", []byte("0123456789"), "bytes 0-9/30")
	if status != http.StatusForbidden {
		t.Fatalf("chunked upload status = %d, want %d; body=%s", status, http.StatusForbidden, body)
	}
	if !strings.Contains(body, "storage quota exceeded") {
		t.Fatalf("chunked upload body = %q, want storage quota exceeded", body)
	}
}

func TestWebUploadReplaceUsesStorageDelta(t *testing.T) {
	originalOrg := getAdminOrganizationInfo(t, defaultOrgID)
	originalUser := getAdminUserByEmail(t, defaultUserEmail)
	restoreDefaultOrgAndUserQuotasOnCleanup(t, originalOrg, originalUser)

	updateAdminOrganizationQuotas(t, defaultOrgID, map[string]interface{}{
		"storage_quota": int64(1 << 50),
		"quota_policy":  "hard",
	})

	baselineUsage := jsonInt64(getAdminUserByEmail(t, defaultUserEmail), "quota_usage")
	setDefaultUserQuota(t, baselineUsage+1000)

	repoID := createTestLibrary(t, userClient, fmt.Sprintf("inttest-replace-delta-%d", time.Now().UnixNano()))
	uploadURL := getUploadURL(t, userClient, repoID)
	updateURL := getUpdateURL(t, userClient, repoID)
	fileName := "replace-delta.txt"

	status, body := uploadFileThroughLinkStatus(t, userClient, uploadURL, fileName, "/", strings.Repeat("a", 100))
	if status != http.StatusOK {
		t.Fatalf("initial upload status = %d, want %d; body=%s", status, http.StatusOK, body)
	}

	afterInitialUsage := waitForUserQuotaUsage(t, baselineUsage+100)
	setDefaultUserQuota(t, afterInitialUsage)

	status, body = uploadFileThroughLinkStatus(t, userClient, updateURL, fileName, "/", strings.Repeat("b", 100))
	if status != http.StatusOK {
		t.Fatalf("same-size replace status = %d, want %d; body=%s", status, http.StatusOK, body)
	}
	afterSameSizeReplace := waitForUserQuotaUsage(t, afterInitialUsage)
	if afterSameSizeReplace != afterInitialUsage {
		t.Fatalf("same-size replace usage = %d, want %d", afterSameSizeReplace, afterInitialUsage)
	}

	setDefaultUserQuota(t, afterInitialUsage+20)
	status, body = uploadFileThroughLinkStatus(t, userClient, updateURL, fileName, "/", strings.Repeat("c", 120))
	if status != http.StatusOK {
		t.Fatalf("larger replace status = %d, want %d; body=%s", status, http.StatusOK, body)
	}
	waitForUserQuotaUsage(t, afterInitialUsage+20)
}

// Multi-node uploads share live storage counters but do not reserve quota
// atomically before publish. The supported guarantee today is eventual hard
// enforcement once the successful writes have published their storage deltas.
func TestMultiInstancePerUserStorageQuotaBlocksSubsequentUploadAfterConcurrentBurst(t *testing.T) {
	originalOrg := getAdminOrganizationInfo(t, defaultOrgID)
	originalUser := getAdminUserByEmail(t, defaultUserEmail)
	restoreDefaultOrgAndUserQuotasOnCleanup(t, originalOrg, originalUser)

	clients := multiInstanceRequireUserClients(t, 2)

	updateAdminOrganizationQuotas(t, defaultOrgID, map[string]interface{}{
		"storage_quota": int64(1 << 50),
		"quota_policy":  "hard",
	})

	baselineUsage := jsonInt64(getAdminUserByEmail(t, defaultUserEmail), "quota_usage")
	setDefaultUserQuota(t, baselineUsage+150)

	repoID := createTestLibrary(t, userClient, fmt.Sprintf("inttest-multi-instance-user-quota-%d", time.Now().UnixNano()))
	uploadURLs := multiInstanceUploadLinks(t, clients, repoID, "/")
	names := multiInstanceFileNames("quota-race", len(clients))
	const contentSize = 100

	results := multiInstanceRunConcurrentMutations(t, clients, names, func(client *testClient, name string, idx int) concurrentMutationResult {
		content := fmt.Sprintf("%02d%s", idx, strings.Repeat(string(rune('a'+idx)), contentSize-2))
		return uploadViaLinkConcurrent(client, uploadURLs[idx], name, "/", content)
	})

	successCount := 0
	for _, result := range results {
		if result.err != nil {
			t.Fatalf("%s failed: %v", result.name, result.err)
		}
		switch result.status {
		case http.StatusOK, http.StatusCreated:
			successCount++
		case http.StatusForbidden:
			if !strings.Contains(result.body, "storage quota exceeded") {
				t.Fatalf("%s forbidden body = %q, want storage quota exceeded", result.name, result.body)
			}
		default:
			t.Fatalf("%s status = %d, want success or 403; body=%s", result.name, result.status, result.body)
		}
	}

	if successCount == 0 {
		t.Fatalf("concurrent multi-node uploads all failed unexpectedly; results=%+v", results)
	}

	expectedUsage := baselineUsage + int64(successCount)*contentSize
	finalUsage := waitForUserQuotaUsage(t, expectedUsage)
	if finalUsage != expectedUsage {
		t.Fatalf("final quota_usage = %d, want %d", finalUsage, expectedUsage)
	}

	followUpUploadURL := getUploadURL(t, clients[len(clients)-1], repoID)
	status, body := uploadFileThroughLinkStatus(t, clients[len(clients)-1], followUpUploadURL, "quota-after-burst.txt", "/", strings.Repeat("z", contentSize))
	if status != http.StatusForbidden {
		t.Fatalf("follow-up upload status = %d, want %d after quota_usage converged to %d; body=%s; results=%+v", status, http.StatusForbidden, finalUsage, body, results)
	}
	if !strings.Contains(body, "storage quota exceeded") {
		t.Fatalf("follow-up upload body = %q, want storage quota exceeded", body)
	}
}

func TestV2DirectUploadEnforcesPerUserStorageQuota(t *testing.T) {
	originalOrg := getAdminOrganizationInfo(t, defaultOrgID)
	originalUser := getAdminUserByEmail(t, defaultUserEmail)
	restoreDefaultOrgAndUserQuotasOnCleanup(t, originalOrg, originalUser)

	updateAdminOrganizationQuotas(t, defaultOrgID, map[string]interface{}{
		"storage_quota": int64(1 << 50),
		"quota_policy":  "hard",
	})
	setDefaultUserQuota(t, int64(1))

	repoID := createTestLibrary(t, userClient, fmt.Sprintf("inttest-v2-upload-user-storage-%d", time.Now().UnixNano()))

	status, body := uploadV2DirectFileStatus(t, userClient, repoID, "blocked-v2.txt", "/", "this v2 direct upload should exceed the one byte per-user storage cap", false)
	if status != http.StatusForbidden {
		t.Fatalf("v2 direct upload status = %d, want %d; body=%s", status, http.StatusForbidden, body)
	}
	if !strings.Contains(body, "storage quota exceeded") {
		t.Fatalf("v2 direct upload body = %q, want storage quota exceeded", body)
	}
}

func TestV2DirectUploadTrafficQuotaExceeded(t *testing.T) {
	originalOrg := getAdminOrganizationInfo(t, defaultOrgID)
	restoreOrgQuotasOnCleanup(t, originalOrg)

	updateAdminOrganizationQuotas(t, defaultOrgID, map[string]interface{}{
		"storage_quota":          int64(1 << 50),
		"traffic_quota":          int64(1 << 50),
		"traffic_upload_quota":   int64(1),
		"traffic_download_quota": int64(1 << 50),
		"quota_policy":           "hard",
	})

	repoID := createTestLibrary(t, userClient, fmt.Sprintf("inttest-v2-upload-traffic-%d", time.Now().UnixNano()))

	status, body := uploadV2DirectFileStatus(t, userClient, repoID, "traffic-v2.txt", "/", "hello", false)
	if status != http.StatusForbidden {
		t.Fatalf("v2 direct upload traffic status = %d, want %d; body=%s", status, http.StatusForbidden, body)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("failed to decode v2 direct upload traffic response: %v body=%q", err, body)
	}
	if payload["error"] != "traffic quota exceeded" {
		t.Fatalf("v2 direct upload error = %v, want traffic quota exceeded", payload["error"])
	}
	if payload["reason"] != "traffic-upload" {
		t.Fatalf("v2 direct upload reason = %v, want traffic-upload", payload["reason"])
	}
}

func TestV2DirectUploadReplaceUsesStorageDelta(t *testing.T) {
	originalOrg := getAdminOrganizationInfo(t, defaultOrgID)
	originalUser := getAdminUserByEmail(t, defaultUserEmail)
	restoreDefaultOrgAndUserQuotasOnCleanup(t, originalOrg, originalUser)

	updateAdminOrganizationQuotas(t, defaultOrgID, map[string]interface{}{
		"storage_quota": int64(1 << 50),
		"quota_policy":  "hard",
	})

	baselineUsage := jsonInt64(getAdminUserByEmail(t, defaultUserEmail), "quota_usage")
	setDefaultUserQuota(t, baselineUsage+1000)

	repoID := createTestLibrary(t, userClient, fmt.Sprintf("inttest-v2-replace-delta-%d", time.Now().UnixNano()))
	fileName := "replace-v2-delta.txt"

	status, body := uploadV2DirectFileStatus(t, userClient, repoID, fileName, "/", strings.Repeat("a", 100), false)
	if status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("initial v2 direct upload status = %d, want 200/201; body=%s", status, body)
	}

	afterInitialUsage := waitForUserQuotaUsage(t, baselineUsage+100)
	setDefaultUserQuota(t, afterInitialUsage)

	status, body = uploadV2DirectFileStatus(t, userClient, repoID, fileName, "/", strings.Repeat("b", 100), true)
	if status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("same-size v2 direct replace status = %d, want 200/201; body=%s", status, body)
	}
	afterSameSizeReplace := waitForUserQuotaUsage(t, afterInitialUsage)
	if afterSameSizeReplace != afterInitialUsage {
		t.Fatalf("same-size v2 direct replace usage = %d, want %d", afterSameSizeReplace, afterInitialUsage)
	}

	setDefaultUserQuota(t, afterInitialUsage+20)
	status, body = uploadV2DirectFileStatus(t, userClient, repoID, fileName, "/", strings.Repeat("c", 120), true)
	if status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("larger v2 direct replace status = %d, want 200/201; body=%s", status, body)
	}
	waitForUserQuotaUsage(t, afterInitialUsage+20)
}

func TestV2BlockUploadTrafficQuotaExceeded(t *testing.T) {
	originalOrg := getAdminOrganizationInfo(t, defaultOrgID)
	restoreOrgQuotasOnCleanup(t, originalOrg)

	updateAdminOrganizationQuotas(t, defaultOrgID, map[string]interface{}{
		"storage_quota":          int64(1 << 50),
		"traffic_quota":          int64(1 << 50),
		"traffic_upload_quota":   int64(1),
		"traffic_download_quota": int64(1 << 50),
		"quota_policy":           "hard",
	})

	status, payload := uploadRawBlockStatus(t, userClient, []byte("hello"))
	if status != http.StatusForbidden {
		t.Fatalf("v2 block upload traffic status = %d, want %d; body=%v", status, http.StatusForbidden, payload)
	}
	if payload["error"] != "traffic quota exceeded" {
		t.Fatalf("v2 block upload error = %v, want traffic quota exceeded", payload["error"])
	}
	if payload["reason"] != "traffic-upload" {
		t.Fatalf("v2 block upload reason = %v, want traffic-upload", payload["reason"])
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

func restoreDefaultOrgAndUserQuotasOnCleanup(t *testing.T, originalOrg, originalUser map[string]interface{}) {
	t.Helper()
	t.Cleanup(func() {
		maxInt64 := func(a, b int64) int64 {
			if b > a {
				return b
			}
			return a
		}

		originalUserQuota := jsonInt64(originalUser, "quota_total")
		originalOrgStorageQuota := jsonInt64(originalOrg, "storage_quota")
		restoreStorageQuota := originalOrgStorageQuota
		if originalUserQuota > restoreStorageQuota {
			restoreStorageQuota = originalUserQuota
		}

		originalUserUploadQuota := jsonInt64(originalUser, "traffic_upload_quota")
		originalUserDownloadQuota := jsonInt64(originalUser, "traffic_download_quota")
		restoreTrafficQuota := jsonInt64(originalOrg, "traffic_quota")
		if restoreTrafficQuota > 0 {
			restoreTrafficQuota = maxInt64(restoreTrafficQuota, originalUserUploadQuota)
			restoreTrafficQuota = maxInt64(restoreTrafficQuota, originalUserDownloadQuota)
			if originalUserUploadQuota > 0 && originalUserDownloadQuota > 0 {
				restoreTrafficQuota = maxInt64(restoreTrafficQuota, originalUserUploadQuota+originalUserDownloadQuota)
			}
		}
		restoreTrafficUploadQuota := jsonInt64(originalOrg, "traffic_upload_quota")
		if restoreTrafficUploadQuota > 0 {
			restoreTrafficUploadQuota = maxInt64(restoreTrafficUploadQuota, originalUserUploadQuota)
		}
		restoreTrafficDownloadQuota := jsonInt64(originalOrg, "traffic_download_quota")
		if restoreTrafficDownloadQuota > 0 {
			restoreTrafficDownloadQuota = maxInt64(restoreTrafficDownloadQuota, originalUserDownloadQuota)
		}

		// These tests mutate the shared default org/user quota and must run
		// serially for defaultOrgID; do not add t.Parallel() around them.
		tempOrgBody := map[string]interface{}{
			"storage_quota":          restoreStorageQuota,
			"traffic_quota":          restoreTrafficQuota,
			"traffic_upload_quota":   restoreTrafficUploadQuota,
			"traffic_download_quota": restoreTrafficDownloadQuota,
		}
		if quotaPolicy := jsonString(originalOrg, "quota_policy"); quotaPolicy != "" {
			tempOrgBody["quota_policy"] = quotaPolicy
		}
		updateAdminOrganizationQuotas(t, defaultOrgID, tempOrgBody)

		restoreResp := superadminClient.PutJSON(t, "/api/v2.1/admin/users/"+url.PathEscape(defaultUserEmail)+"/", map[string]interface{}{
			"quota_total":            originalUserQuota,
			"traffic_upload_quota":   originalUserUploadQuota,
			"traffic_download_quota": originalUserDownloadQuota,
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
}

func setDefaultUserQuota(t *testing.T, quota int64) {
	t.Helper()
	resp := superadminClient.PutJSON(t, "/api/v2.1/admin/users/"+url.PathEscape(defaultUserEmail)+"/", map[string]interface{}{
		"quota_total": quota,
	})
	expectStatus(t, resp, http.StatusOK)
	resp.Body.Close()
}

func waitForUserQuotaUsage(t *testing.T, want int64) int64 {
	t.Helper()
	var got int64
	deadline := time.Now().Add(10 * time.Second)
	for {
		got = jsonInt64(getAdminUserByEmail(t, defaultUserEmail), "quota_usage")
		if got == want {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for default user quota_usage=%d (last=%d)", want, got)
		}
		time.Sleep(pollInterval)
	}
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

func uploadV2DirectFileStatus(t *testing.T, c *testClient, repoID, fileName, parentDir, content string, replace bool) (int, string) {
	t.Helper()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", fileName)
	if err != nil {
		t.Fatalf("CreateFormFile failed: %v", err)
	}
	if _, err := part.Write([]byte(content)); err != nil {
		t.Fatalf("writing v2 direct upload content failed: %v", err)
	}
	if err := writer.WriteField("parent_dir", parentDir); err != nil {
		t.Fatalf("WriteField failed: %v", err)
	}
	if replace {
		if err := writer.WriteField("replace", "1"); err != nil {
			t.Fatalf("WriteField replace failed: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("closing multipart writer failed: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/api/v2.1/repos/"+repoID+"/upload", &buf)
	if err != nil {
		t.Fatalf("creating v2 direct upload request failed: %v", err)
	}
	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatalf("v2 direct upload request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading v2 direct upload response failed: %v", err)
	}
	return resp.StatusCode, string(body)
}

func getUploadURL(t *testing.T, c *testClient, repoID string) string {
	t.Helper()
	resp := c.Get(t, fmt.Sprintf("/api2/repos/%s/upload-link/?p=/", repoID))
	expectStatus(t, resp, http.StatusOK)
	return strings.Trim(responseBody(t, resp), "\" \n\r")
}

func getUpdateURL(t *testing.T, c *testClient, repoID string) string {
	t.Helper()
	resp := c.Get(t, fmt.Sprintf("/api2/repos/%s/update-link/?p=/", repoID))
	expectStatus(t, resp, http.StatusOK)
	return strings.Trim(responseBody(t, resp), "\" \n\r")
}

func uploadChunkThroughLinkStatus(t *testing.T, c *testClient, uploadURL, fileName, parentDir string, content []byte, contentRange string) (int, string) {
	t.Helper()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	part, err := writer.CreateFormFile("file", fileName)
	if err != nil {
		t.Fatalf("CreateFormFile failed: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("writing chunk content failed: %v", err)
	}
	if err := writer.WriteField("parent_dir", parentDir); err != nil {
		t.Fatalf("WriteField failed: %v", err)
	}
	// Mirror resumable.js: send a stable per-upload identifier so the server can
	// distinguish a retry of this upload from a different one. Derived from
	// name+dir only (not the URL) so retries via the ret-json and raw URLs share
	// the same identity.
	if err := writer.WriteField("resumableIdentifier", fileName+"|"+parentDir); err != nil {
		t.Fatalf("WriteField resumableIdentifier failed: %v", err)
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
	req.Header.Set("Content-Range", contentRange)

	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatalf("chunk upload request failed: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading chunk upload response failed: %v", err)
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

// releaseSyncStagedBlockForTest tears down one block staged by the sync/seafhttp upload
// path: it drops the `up:sync:` pin the handler wrote, then hands the block to the shared
// releaseStagedBlockForTest, which clears the expiry projection and — only if no referrer
// survives — the blocks row, forward mapping and S3 object. A block that ends up committed
// keeps an `fs:` ref, so it is left to its library's cascade.
func releaseSyncStagedBlockForTest(t *testing.T, repoID, blockID string) {
	t.Helper()

	// Built outside t.Cleanup so an unreachable object store degrades to a logged skip.
	blockStore := blockStoreForCleanupOrNil(t)
	orgID := resolveOrgID(t, repoID)
	// Mirrors internal/api.syncBlockUploadOperationID, which is unexported.
	referrer := dbpkg.BlockReferrerForUpload("sync:" + repoID + ":" + blockID)

	t.Cleanup(func() {
		database := shareProjectionDBForTest(t)
		if err := database.Session().Query(
			`DELETE FROM block_references WHERE org_id = ? AND block_id = ? AND referrer = ?`,
			orgID, blockID, referrer).Exec(); err != nil {
			t.Errorf("cleanup sync block reference %s/%s: %v", orgID, blockID, err)
			return
		}
		releaseStagedBlockForTest(t, database, orgID, blockID, referrer, blockStore)
	})
}

// uploadSyncBlockStatus PUTs a block through the desktop/sync seafhttp path and returns
// the status. The block is staged, never committed: the handler pins it with a
// deterministic `up:sync:<repo>:<block>` reference plus a provisional expiry projection,
// which production only releases when the sync head publish promotes or drops it.
//
// Teardown is registered here because no caller commits. Left alone, the pin survives the
// run and only Phase 0 clears it when the projection expires — two days later — so every
// suite run parks a block and its S3 object in the cluster until then
// (ISSUE-GC-TEST-RESIDUE-01 / branch 1G, same shape 1B fixed on the web upload path).
// Best-effort: callers asserting a quota rejection never staged anything.
func uploadSyncBlockStatus(t *testing.T, c *testClient, repoID string, content []byte) (int, string) {
	t.Helper()

	hash := sha256.Sum256(content)
	blockID := hex.EncodeToString(hash[:])
	releaseSyncStagedBlockForTest(t, repoID, blockID)
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

// TestCopyFileEnforcesPerUserStorageQuota verifies that CopyFile blocks copies
// that would exceed the per-user storage quota.
//
// Regression for ISSUE-QUOTA-COVERAGE-01: CopyFile previously bypassed
// CheckStorageQuota entirely and never adjusted storage_counters, so users could
// duplicate files beyond their cap.
func TestCopyFileEnforcesPerUserStorageQuota(t *testing.T) {
	originalOrg := getAdminOrganizationInfo(t, defaultOrgID)
	originalUser := getAdminUserByEmail(t, defaultUserEmail)
	restoreDefaultOrgAndUserQuotasOnCleanup(t, originalOrg, originalUser)

	updateAdminOrganizationQuotas(t, defaultOrgID, map[string]interface{}{
		"storage_quota": int64(1 << 50),
		"quota_policy":  "hard",
	})

	repoID := createTestLibrary(t, userClient, fmt.Sprintf("inttest-copy-quota-%d", time.Now().UnixNano()))

	const seedContent = "this is a 50-byte payload for the copy quota test ok"
	seedSize := int64(len(seedContent))

	baselineUsage := jsonInt64(getAdminUserByEmail(t, defaultUserEmail), "quota_usage")
	setDefaultUserQuota(t, baselineUsage+seedSize+100)

	uploadURL := getUploadURL(t, userClient, repoID)
	status, body := uploadFileThroughLinkStatus(t, userClient, uploadURL, "seed.txt", "/", seedContent)
	if status != http.StatusOK {
		t.Fatalf("seed upload status = %d, want %d; body=%s", status, http.StatusOK, body)
	}
	afterUpload := waitForUserQuotaUsage(t, baselineUsage+seedSize)

	// Leave half a seed-size of headroom — not enough to absorb a full copy.
	setDefaultUserQuota(t, afterUpload+seedSize/2)

	copyResp := userClient.PostJSON(t, "/api/v2.1/repos/"+repoID+"/file/copy/", map[string]interface{}{
		"src_path":        "/seed.txt",
		"dst_dir":         "/",
		"conflict_policy": "autorename",
	})
	defer copyResp.Body.Close()
	copyBody := responseBody(t, copyResp)
	if copyResp.StatusCode != http.StatusForbidden {
		t.Fatalf("copy status = %d, want %d; body=%s", copyResp.StatusCode, http.StatusForbidden, copyBody)
	}
	if !strings.Contains(copyBody, "storage quota exceeded") {
		t.Fatalf("copy body = %q, want storage quota exceeded", copyBody)
	}

	// Counter must not have advanced.
	if got := jsonInt64(getAdminUserByEmail(t, defaultUserEmail), "quota_usage"); got != afterUpload {
		t.Fatalf("quota_usage after blocked copy = %d, want %d", got, afterUpload)
	}

	// Restore enough headroom and verify the copy succeeds and the counter
	// reflects the new tree size.
	setDefaultUserQuota(t, afterUpload+seedSize+100)
	copyResp2 := userClient.PostJSON(t, "/api/v2.1/repos/"+repoID+"/file/copy/", map[string]interface{}{
		"src_path":        "/seed.txt",
		"dst_dir":         "/",
		"conflict_policy": "autorename",
	})
	defer copyResp2.Body.Close()
	if copyResp2.StatusCode != http.StatusOK {
		t.Fatalf("retry copy status = %d, want %d; body=%s", copyResp2.StatusCode, http.StatusOK, responseBody(t, copyResp2))
	}
	waitForUserQuotaUsage(t, afterUpload+seedSize)
}

// TestRestoreTrashItemEnforcesPerUserStorageQuota verifies that restoring a
// deleted file from trash pre-checks the per-user storage quota.
//
// Regression for ISSUE-QUOTA-COVERAGE-01.
func TestRestoreTrashItemEnforcesPerUserStorageQuota(t *testing.T) {
	originalOrg := getAdminOrganizationInfo(t, defaultOrgID)
	originalUser := getAdminUserByEmail(t, defaultUserEmail)
	restoreDefaultOrgAndUserQuotasOnCleanup(t, originalOrg, originalUser)

	updateAdminOrganizationQuotas(t, defaultOrgID, map[string]interface{}{
		"storage_quota": int64(1 << 50),
		"quota_policy":  "hard",
	})

	repoID := createTestLibrary(t, userClient, fmt.Sprintf("inttest-restore-quota-%d", time.Now().UnixNano()))

	const seedContent = "deleted file body that will be restored and tested"
	seedSize := int64(len(seedContent))

	baselineUsage := jsonInt64(getAdminUserByEmail(t, defaultUserEmail), "quota_usage")
	setDefaultUserQuota(t, baselineUsage+seedSize+100)

	uploadURL := getUploadURL(t, userClient, repoID)
	status, body := uploadFileThroughLinkStatus(t, userClient, uploadURL, "del.txt", "/", seedContent)
	if status != http.StatusOK {
		t.Fatalf("seed upload status = %d, want %d; body=%s", status, http.StatusOK, body)
	}
	waitForUserQuotaUsage(t, baselineUsage+seedSize)

	batchDeleteFiles(t, userClient, repoID, "/", []string{"del.txt"})
	afterDelete := waitForUserQuotaUsage(t, baselineUsage)

	commitID, parentDir := findTrashItemFor(t, userClient, repoID, "del.txt")
	if commitID == "" {
		t.Fatalf("trash listing did not surface the deleted file")
	}

	// Headroom too small for the seed file to come back.
	setDefaultUserQuota(t, afterDelete+seedSize/2)

	restoreResp := userClient.PostJSON(t, "/api/v2.1/repos/"+repoID+"/file/restore/", map[string]interface{}{
		"commit_id": commitID,
		"p":         parentDir + "del.txt",
	})
	defer restoreResp.Body.Close()
	restoreBody := responseBody(t, restoreResp)
	if restoreResp.StatusCode != http.StatusForbidden {
		t.Fatalf("restore status = %d, want %d; body=%s", restoreResp.StatusCode, http.StatusForbidden, restoreBody)
	}
	if !strings.Contains(restoreBody, "storage quota exceeded") {
		t.Fatalf("restore body = %q, want storage quota exceeded", restoreBody)
	}
	var restorePayload map[string]interface{}
	if err := json.Unmarshal([]byte(restoreBody), &restorePayload); err != nil {
		t.Fatalf("restore body is not valid JSON: %v; body=%s", err, restoreBody)
	}
	if got, _ := restorePayload["error_msg"].(string); got != "storage quota exceeded" {
		t.Fatalf("restore error_msg = %v, want %q; payload=%v", restorePayload["error_msg"], "storage quota exceeded", restorePayload)
	}
	if _, ok := restorePayload["error"]; ok {
		t.Fatalf("restore payload unexpectedly exposed error key: %v", restorePayload)
	}

	if got := jsonInt64(getAdminUserByEmail(t, defaultUserEmail), "quota_usage"); got != afterDelete {
		t.Fatalf("quota_usage after blocked restore = %d, want %d", got, afterDelete)
	}

	// Give it enough room and verify it succeeds.
	setDefaultUserQuota(t, afterDelete+seedSize+100)
	restoreResp2 := userClient.PostJSON(t, "/api/v2.1/repos/"+repoID+"/file/restore/", map[string]interface{}{
		"commit_id": commitID,
		"p":         parentDir + "del.txt",
	})
	defer restoreResp2.Body.Close()
	if restoreResp2.StatusCode != http.StatusOK {
		t.Fatalf("retry restore status = %d, want %d; body=%s", restoreResp2.StatusCode, http.StatusOK, responseBody(t, restoreResp2))
	}
	waitForUserQuotaUsage(t, afterDelete+seedSize)
}

// TestRevertFileEnforcesPerUserStorageQuota verifies that reverting a file to
// an older version pre-checks the per-user storage quota for the byte delta.
//
// Regression for ISSUE-QUOTA-COVERAGE-01.
func TestRevertFileEnforcesPerUserStorageQuota(t *testing.T) {
	originalOrg := getAdminOrganizationInfo(t, defaultOrgID)
	originalUser := getAdminUserByEmail(t, defaultUserEmail)
	restoreDefaultOrgAndUserQuotasOnCleanup(t, originalOrg, originalUser)

	updateAdminOrganizationQuotas(t, defaultOrgID, map[string]interface{}{
		"storage_quota": int64(1 << 50),
		"quota_policy":  "hard",
	})

	repoID := createTestLibrary(t, userClient, fmt.Sprintf("inttest-revert-quota-%d", time.Now().UnixNano()))

	largeBody := strings.Repeat("L", 200)
	smallBody := strings.Repeat("s", 30)

	baselineUsage := jsonInt64(getAdminUserByEmail(t, defaultUserEmail), "quota_usage")
	setDefaultUserQuota(t, baselineUsage+int64(len(largeBody))+100)

	uploadURL := getUploadURL(t, userClient, repoID)
	updateURL := getUpdateURL(t, userClient, repoID)

	// Upload large version, capture HEAD = C1 (contains 200-byte rev.txt).
	status, body := uploadFileThroughLinkStatus(t, userClient, uploadURL, "rev.txt", "/", largeBody)
	if status != http.StatusOK {
		t.Fatalf("upload large status = %d; body=%s", status, body)
	}
	afterLarge := waitForUserQuotaUsage(t, baselineUsage+int64(len(largeBody)))

	c1 := getRepoHeadCommit(t, userClient, repoID)
	if c1 == "" {
		t.Fatalf("could not resolve C1 head commit")
	}

	// Replace with small version. HEAD advances to C2, counter shrinks.
	status, body = uploadFileThroughLinkStatus(t, userClient, updateURL, "rev.txt", "/", smallBody)
	if status != http.StatusOK {
		t.Fatalf("upload small status = %d; body=%s", status, body)
	}
	afterSmall := waitForUserQuotaUsage(t, afterLarge-int64(len(largeBody))+int64(len(smallBody)))

	// Headroom too small for the (large - small) delta the revert would bring back.
	delta := int64(len(largeBody) - len(smallBody))
	setDefaultUserQuota(t, afterSmall+delta-10)

	revertResp := userClient.PostForm(t, "/api/v2.1/repos/"+repoID+"/file/?p=/rev.txt&operation=revert", url.Values{
		"commit_id":       {c1},
		"conflict_policy": {"replace"},
	})
	defer revertResp.Body.Close()
	revertBody := responseBody(t, revertResp)
	if revertResp.StatusCode != http.StatusForbidden {
		t.Fatalf("revert status = %d, want %d; body=%s", revertResp.StatusCode, http.StatusForbidden, revertBody)
	}
	if !strings.Contains(revertBody, "storage quota exceeded") {
		t.Fatalf("revert body = %q, want storage quota exceeded", revertBody)
	}

	if got := jsonInt64(getAdminUserByEmail(t, defaultUserEmail), "quota_usage"); got != afterSmall {
		t.Fatalf("quota_usage after blocked revert = %d, want %d", got, afterSmall)
	}

	// Give enough headroom and verify the revert applies the byte delta.
	setDefaultUserQuota(t, afterSmall+delta+100)
	revertResp2 := userClient.PostForm(t, "/api/v2.1/repos/"+repoID+"/file/?p=/rev.txt&operation=revert", url.Values{
		"commit_id":       {c1},
		"conflict_policy": {"replace"},
	})
	defer revertResp2.Body.Close()
	if revertResp2.StatusCode != http.StatusOK {
		t.Fatalf("retry revert status = %d, want %d; body=%s", revertResp2.StatusCode, http.StatusOK, responseBody(t, revertResp2))
	}
	waitForUserQuotaUsage(t, afterSmall+delta)
}

func TestAsyncBatchMoveDoesNotRequireExtraQuotaForNetZeroMove(t *testing.T) {
	originalOrg := getAdminOrganizationInfo(t, defaultOrgID)
	originalUser := getAdminUserByEmail(t, defaultUserEmail)
	restoreDefaultOrgAndUserQuotasOnCleanup(t, originalOrg, originalUser)

	updateAdminOrganizationQuotas(t, defaultOrgID, map[string]interface{}{
		"storage_quota": int64(1 << 50),
		"quota_policy":  "hard",
	})

	srcRepoID := createTestLibrary(t, userClient, fmt.Sprintf("inttest-async-move-src-%d", time.Now().UnixNano()))
	dstRepoID := createTestLibrary(t, userClient, fmt.Sprintf("inttest-async-move-dst-%d", time.Now().UnixNano()))

	const seedContent = "cross repo move should be net zero for quota enforcement"
	seedSize := int64(len(seedContent))
	baselineUsage := jsonInt64(getAdminUserByEmail(t, defaultUserEmail), "quota_usage")
	setDefaultUserQuota(t, baselineUsage+seedSize+100)

	uploadURL := getUploadURL(t, userClient, srcRepoID)
	status, body := uploadFileThroughLinkStatus(t, userClient, uploadURL, "move-me.txt", "/", seedContent)
	if status != http.StatusOK {
		t.Fatalf("seed upload status = %d, want %d; body=%s", status, http.StatusOK, body)
	}
	afterUpload := waitForUserQuotaUsage(t, baselineUsage+seedSize)

	setDefaultUserQuota(t, afterUpload)

	taskID := startAsyncBatchMoveTask(t, userClient, srcRepoID, dstRepoID, "/", "/", []string{"move-me.txt"})
	progress := waitForCopyMoveTaskCompletion(t, userClient, taskID)
	if failed := jsonInt64(progress, "failed"); failed != 0 {
		t.Fatalf("async batch move reported failed=%d progress=%v", failed, progress)
	}

	if got := jsonInt64(getAdminUserByEmail(t, defaultUserEmail), "quota_usage"); got != afterUpload {
		t.Fatalf("quota_usage after net-zero move = %d, want %d", got, afterUpload)
	}

	assertRepoHasEntry(t, userClient, dstRepoID, "move-me.txt")
	assertRepoMissingEntry(t, userClient, srcRepoID, "move-me.txt")
}

func startAsyncBatchMoveTask(t *testing.T, c *testClient, srcRepoID, dstRepoID, srcParentDir, dstParentDir string, srcDirents []string) string {
	t.Helper()
	resp := c.PostJSON(t, "/api/v2.1/repos/async-batch-move-item/", map[string]interface{}{
		"src_repo_id":    srcRepoID,
		"dst_repo_id":    dstRepoID,
		"src_parent_dir": srcParentDir,
		"dst_parent_dir": dstParentDir,
		"src_dirents":    srcDirents,
	})
	expectStatus(t, resp, http.StatusOK)
	payload := responseJSON(t, resp)
	taskID, _ := payload["task_id"].(string)
	if taskID == "" {
		t.Fatalf("async batch move response missing task_id: %v", payload)
	}
	return taskID
}

func waitForCopyMoveTaskCompletion(t *testing.T, c *testClient, taskID string) map[string]interface{} {
	t.Helper()
	var payload map[string]interface{}
	waitForCondition(t, fmt.Sprintf("copy/move task %s completion", taskID), func() bool {
		resp := c.Get(t, "/api/v2.1/query-copy-move-progress/?task_id="+url.QueryEscape(taskID))
		expectStatus(t, resp, http.StatusOK)
		payload = responseJSON(t, resp)
		if done, _ := payload["done"].(bool); done {
			return true
		}
		failed := jsonInt64(payload, "failed")
		successful := jsonInt64(payload, "successful")
		total := jsonInt64(payload, "total")
		return failed > 0 && successful == 0 && total > 0
	})
	return payload
}

func assertRepoHasEntry(t *testing.T, c *testClient, repoID, name string) {
	t.Helper()
	resp := c.Get(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/", repoID))
	expectStatus(t, resp, http.StatusOK)
	payload := responseJSON(t, resp)
	entries, _ := payload["dirent_list"].([]interface{})
	for _, raw := range entries {
		entry, _ := raw.(map[string]interface{})
		if got, _ := entry["name"].(string); got == name {
			return
		}
	}
	t.Fatalf("repo %s missing entry %q; payload=%v", repoID, name, payload)
}

func assertRepoMissingEntry(t *testing.T, c *testClient, repoID, name string) {
	t.Helper()
	resp := c.Get(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/", repoID))
	expectStatus(t, resp, http.StatusOK)
	payload := responseJSON(t, resp)
	entries, _ := payload["dirent_list"].([]interface{})
	for _, raw := range entries {
		entry, _ := raw.(map[string]interface{})
		if got, _ := entry["name"].(string); got == name {
			t.Fatalf("repo %s still contains entry %q; payload=%v", repoID, name, payload)
		}
	}
}

func findTrashItemFor(t *testing.T, c *testClient, repoID, fileName string) (string, string) {
	t.Helper()
	resp := c.Get(t, "/api/v2.1/repos/"+repoID+"/trash/?parent_dir=/")
	expectStatus(t, resp, http.StatusOK)
	body := responseBody(t, resp)

	var payload struct {
		Data []struct {
			ObjName   string `json:"obj_name"`
			ParentDir string `json:"parent_dir"`
			CommitID  string `json:"commit_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		// Some variants return the array at the top level.
		var direct []struct {
			ObjName   string `json:"obj_name"`
			ParentDir string `json:"parent_dir"`
			CommitID  string `json:"commit_id"`
		}
		if err2 := json.Unmarshal([]byte(body), &direct); err2 != nil {
			t.Fatalf("could not parse trash listing %q: %v / %v", body, err, err2)
		}
		for _, it := range direct {
			if it.ObjName == fileName {
				return it.CommitID, it.ParentDir
			}
		}
		return "", ""
	}
	for _, it := range payload.Data {
		if it.ObjName == fileName {
			return it.CommitID, it.ParentDir
		}
	}
	return "", ""
}

func getRepoHeadCommit(t *testing.T, c *testClient, repoID string) string {
	t.Helper()
	resp := c.Get(t, "/api2/repos/"+repoID+"/")
	expectStatus(t, resp, http.StatusOK)
	body := responseBody(t, resp)
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("could not parse repo info %q: %v", body, err)
	}
	if v, ok := payload["head_commit_id"].(string); ok && v != "" {
		return v
	}
	return ""
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
