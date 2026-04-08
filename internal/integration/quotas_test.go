//go:build integration

package integration

import (
	"net/http"
	"net/url"
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
