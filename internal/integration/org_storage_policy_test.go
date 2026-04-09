//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

func restoreOrgStoragePolicyOnCleanup(t *testing.T, orgID string, original map[string]interface{}) {
	t.Helper()
	t.Cleanup(func() {
		policy, ok := original["storage_policy"].(map[string]interface{})
		if !ok || policy == nil {
			policy = map[string]interface{}{"data_residency": "flexible"}
		}
		updateAdminOrganization(t, orgID, map[string]interface{}{"storage_policy": policy})
	})
}

func setOrgStoragePolicyForTest(t *testing.T, orgID string, dataResidency, defaultRegion string) {
	t.Helper()
	original := getAdminOrganizationInfo(t, orgID)
	restoreOrgStoragePolicyOnCleanup(t, orgID, original)

	body := map[string]interface{}{
		"storage_policy": map[string]interface{}{
			"data_residency": dataResidency,
		},
	}
	if defaultRegion != "" {
		body["storage_policy"].(map[string]interface{})["default_region"] = defaultRegion
	}
	updateAdminOrganization(t, orgID, body)
}

func expectRepoStorage(t *testing.T, c *testClient, repoID, expectedStorageID string) {
	t.Helper()
	resp := c.Get(t, fmt.Sprintf("/api2/repos/%s/", repoID))
	expectStatus(t, resp, http.StatusOK)
	payload := responseJSON(t, resp)
	if got, _ := payload["storage_id"].(string); got != expectedStorageID {
		t.Fatalf("repo %s storage_id = %q, want %q", repoID, got, expectedStorageID)
	}
}

func createUserRepoWithHost(t *testing.T, name, host string) string {
	t.Helper()
	resp := userClient.PostJSONWithHost(t, "/api2/repos/", map[string]string{"name": name}, host)
	expectStatus(t, resp, http.StatusOK)
	payload := responseJSON(t, resp)
	repoID, _ := payload["repo_id"].(string)
	if repoID == "" {
		t.Fatalf("user create response missing repo_id: %v", payload)
	}
	t.Cleanup(func() {
		cleanup := userClient.Delete(t, fmt.Sprintf("/api/v2.1/repos/%s/", repoID))
		if cleanup.StatusCode == http.StatusOK || cleanup.StatusCode == http.StatusNotFound {
			cleanup.Body.Close()
			return
		}
		body := responseBody(t, cleanup)
		t.Errorf("cleanup delete user repo %s failed: status=%d body=%s", repoID, cleanup.StatusCode, body)
	})
	return repoID
}

func createGroupOwnedRepoWithHost(t *testing.T, groupID, name, host string) string {
	t.Helper()
	resp := adminClient.PostJSONWithHost(t, fmt.Sprintf("/api/v2.1/groups/%s/group-owned-libraries/", groupID), map[string]string{"name": name}, host)
	expectStatus(t, resp, http.StatusOK)
	payload := responseJSON(t, resp)
	repoID, _ := payload["repo_id"].(string)
	if repoID == "" {
		t.Fatalf("group-owned create response missing repo_id: %v", payload)
	}
	t.Cleanup(func() {
		cleanup := adminClient.Delete(t, fmt.Sprintf("/api/v2.1/groups/%s/group-owned-libraries/%s/", groupID, repoID))
		if cleanup.StatusCode == http.StatusOK || cleanup.StatusCode == http.StatusNotFound {
			cleanup.Body.Close()
			return
		}
		body := responseBody(t, cleanup)
		t.Errorf("cleanup delete group-owned repo %s failed: status=%d body=%s", repoID, cleanup.StatusCode, body)
	})
	return repoID
}

func createOrgAdminGroupOwnedRepoWithHost(t *testing.T, groupID, name, host string) string {
	t.Helper()
	resp := adminClient.PostJSONWithHost(t, fmt.Sprintf("/api/v2.1/org/%s/admin/groups/%s/group-owned-libraries/", defaultOrgID, groupID), map[string]string{"repo_name": name}, host)
	expectStatus(t, resp, http.StatusOK)
	payload := responseJSON(t, resp)
	repoID, _ := payload["repo_id"].(string)
	if repoID == "" {
		t.Fatalf("org-admin group-owned create response missing repo_id: %v", payload)
	}
	t.Cleanup(func() {
		cleanup := adminClient.Delete(t, fmt.Sprintf("/api/v2.1/org/%s/admin/groups/%s/group-owned-libraries/%s/", defaultOrgID, groupID, repoID))
		if cleanup.StatusCode == http.StatusOK || cleanup.StatusCode == http.StatusNotFound {
			cleanup.Body.Close()
			return
		}
		body := responseBody(t, cleanup)
		t.Errorf("cleanup delete org-admin group-owned repo %s failed: status=%d body=%s", repoID, cleanup.StatusCode, body)
	})
	return repoID
}

func createSuperadminRepoWithHost(t *testing.T, ownerEmail, name, host string) string {
	t.Helper()
	resp := superadminClient.PostJSONWithHost(t, "/api/v2.1/admin/libraries/", map[string]string{
		"name":  name,
		"owner": ownerEmail,
	}, host)
	expectStatus(t, resp, http.StatusOK)
	payload := responseJSON(t, resp)
	repoID, _ := payload["id"].(string)
	if repoID == "" {
		repoID, _ = payload["repo_id"].(string)
	}
	if repoID == "" {
		t.Fatalf("superadmin create response missing repo id: %v", payload)
	}
	t.Cleanup(func() {
		cleanup := superadminClient.Delete(t, fmt.Sprintf("/api/v2.1/admin/libraries/%s/", repoID))
		if cleanup.StatusCode == http.StatusOK || cleanup.StatusCode == http.StatusNotFound {
			cleanup.Body.Close()
			return
		}
		body := responseBody(t, cleanup)
		t.Errorf("cleanup delete superadmin repo %s failed: status=%d body=%s", repoID, cleanup.StatusCode, body)
	})
	return repoID
}

func TestOrgStoragePolicyStrictAcrossCreateFlows(t *testing.T) {
	setOrgStoragePolicyForTest(t, defaultOrgID, "strict", "usa")
	host := "eu.sesamefs.local"
	expectedStorageID := "hot-s3-usa"

	t.Run("user personal library", func(t *testing.T) {
		repoID := createUserRepoWithHost(t, fmt.Sprintf("inttest-org-policy-user-strict-%d", time.Now().UnixNano()), host)
		expectRepoStorage(t, userClient, repoID, expectedStorageID)
	})

	t.Run("group owned library", func(t *testing.T) {
		groupID := createGroupForRegressionTest(t, adminClient, fmt.Sprintf("inttest-org-policy-group-strict-%d", time.Now().UnixNano()))
		repoID := createGroupOwnedRepoWithHost(t, groupID, fmt.Sprintf("inttest-org-policy-group-lib-strict-%d", time.Now().UnixNano()), host)
		expectRepoStorage(t, adminClient, repoID, expectedStorageID)
	})

	t.Run("org admin group owned library", func(t *testing.T) {
		groupID := createGroupForRegressionTest(t, adminClient, fmt.Sprintf("inttest-org-policy-org-admin-strict-%d", time.Now().UnixNano()))
		repoID := createOrgAdminGroupOwnedRepoWithHost(t, groupID, fmt.Sprintf("inttest-org-policy-org-admin-lib-strict-%d", time.Now().UnixNano()), host)
		expectRepoStorage(t, adminClient, repoID, expectedStorageID)
	})

	t.Run("superadmin create for user", func(t *testing.T) {
		repoID := createSuperadminRepoWithHost(t, defaultUserEmail, fmt.Sprintf("inttest-org-policy-superadmin-strict-%d", time.Now().UnixNano()), host)
		expectRepoStorage(t, userClient, repoID, expectedStorageID)
	})
}

func TestOrgStoragePolicyFlexibleAcrossCreateFlows(t *testing.T) {
	setOrgStoragePolicyForTest(t, defaultOrgID, "flexible", "usa")
	host := "eu.sesamefs.local"
	expectedStorageID := "hot-s3-eu"

	t.Run("user personal library", func(t *testing.T) {
		repoID := createUserRepoWithHost(t, fmt.Sprintf("inttest-org-policy-user-flex-%d", time.Now().UnixNano()), host)
		expectRepoStorage(t, userClient, repoID, expectedStorageID)
	})

	t.Run("group owned library", func(t *testing.T) {
		groupID := createGroupForRegressionTest(t, adminClient, fmt.Sprintf("inttest-org-policy-group-flex-%d", time.Now().UnixNano()))
		repoID := createGroupOwnedRepoWithHost(t, groupID, fmt.Sprintf("inttest-org-policy-group-lib-flex-%d", time.Now().UnixNano()), host)
		expectRepoStorage(t, adminClient, repoID, expectedStorageID)
	})

	t.Run("org admin group owned library", func(t *testing.T) {
		groupID := createGroupForRegressionTest(t, adminClient, fmt.Sprintf("inttest-org-policy-org-admin-flex-%d", time.Now().UnixNano()))
		repoID := createOrgAdminGroupOwnedRepoWithHost(t, groupID, fmt.Sprintf("inttest-org-policy-org-admin-lib-flex-%d", time.Now().UnixNano()), host)
		expectRepoStorage(t, adminClient, repoID, expectedStorageID)
	})

	t.Run("superadmin create for user", func(t *testing.T) {
		repoID := createSuperadminRepoWithHost(t, defaultUserEmail, fmt.Sprintf("inttest-org-policy-superadmin-flex-%d", time.Now().UnixNano()), host)
		expectRepoStorage(t, userClient, repoID, expectedStorageID)
	})
}
