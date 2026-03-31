//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

const platformOrgID = "00000000-0000-0000-0000-000000000000"

func TestOrgAdminScopedIsolation(t *testing.T) {
	tenantOrgID := createScopedTestOrganization(t, "inttest-org-scope")

	t.Run("org admin can access own org scoped endpoints", func(t *testing.T) {
		assertScopedStatus(t, adminClient, http.MethodGet, fmt.Sprintf("/api/v2.1/org/%s/admin/users/", defaultOrgID), http.StatusOK)
		assertScopedStatus(t, adminClient, http.MethodGet, fmt.Sprintf("/api/v2.1/org/%s/admin/repos/", defaultOrgID), http.StatusOK)
		assertScopedStatus(t, adminClient, http.MethodGet, fmt.Sprintf("/api/v2.1/org/%s/admin/devices-errors/", defaultOrgID), http.StatusOK)
	})

	t.Run("org admin is forbidden from tenant and platform orgs", func(t *testing.T) {
		assertScopedStatus(t, adminClient, http.MethodGet, fmt.Sprintf("/api/v2.1/org/%s/admin/users/", tenantOrgID), http.StatusForbidden)
		assertScopedStatus(t, adminClient, http.MethodGet, fmt.Sprintf("/api/v2.1/org/%s/admin/repos/", tenantOrgID), http.StatusForbidden)
		assertScopedStatus(t, adminClient, http.MethodGet, fmt.Sprintf("/api/v2.1/org/%s/admin/devices-errors/", tenantOrgID), http.StatusForbidden)

		assertScopedStatus(t, adminClient, http.MethodGet, fmt.Sprintf("/api/v2.1/org/%s/admin/users/", platformOrgID), http.StatusForbidden)
		assertScopedStatus(t, adminClient, http.MethodGet, fmt.Sprintf("/api/v2.1/org/%s/admin/repos/", platformOrgID), http.StatusForbidden)
		assertScopedStatus(t, adminClient, http.MethodGet, fmt.Sprintf("/api/v2.1/org/%s/admin/devices-errors/", platformOrgID), http.StatusForbidden)
	})

	t.Run("org admin delete endpoints still reject cross org before resource lookup", func(t *testing.T) {
		missingRepoID := "00000000-0000-0000-0000-000000000099"
		assertScopedStatus(t, adminClient, http.MethodDelete, fmt.Sprintf("/api/v2.1/org/%s/admin/repos/%s/", tenantOrgID, missingRepoID), http.StatusForbidden)
	})

	t.Run("superadmin can access any org scope", func(t *testing.T) {
		for _, orgID := range []string{defaultOrgID, tenantOrgID, platformOrgID} {
			assertScopedStatus(t, superadminClient, http.MethodGet, fmt.Sprintf("/api/v2.1/org/%s/admin/users/", orgID), http.StatusOK)
			assertScopedStatus(t, superadminClient, http.MethodGet, fmt.Sprintf("/api/v2.1/org/%s/admin/repos/", orgID), http.StatusOK)
			assertScopedStatus(t, superadminClient, http.MethodGet, fmt.Sprintf("/api/v2.1/org/%s/admin/devices-errors/", orgID), http.StatusOK)
		}
	})

	t.Run("superadmin delete checks continue past access gate", func(t *testing.T) {
		missingRepoID := "00000000-0000-0000-0000-000000000099"
		assertScopedStatus(t, superadminClient, http.MethodDelete, fmt.Sprintf("/api/v2.1/org/%s/admin/repos/%s/", tenantOrgID, missingRepoID), http.StatusNotFound)
	})
}

func createScopedTestOrganization(t *testing.T, prefix string) string {
	t.Helper()

	body := map[string]interface{}{
		"name":          fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano()),
		"storage_quota": int64(1099511627776),
	}

	resp := superadminClient.PostJSON(t, "/api/v2.1/admin/organizations/", body)
	expectStatus(t, resp, http.StatusCreated)
	result := responseJSON(t, resp)

	orgID, ok := result["org_id"].(string)
	if !ok || orgID == "" {
		t.Fatalf("expected org_id in create organization response, got %v", result)
	}

	t.Cleanup(func() {
		deleteScopedTestOrganization(t, orgID)
	})

	return orgID
}

func deleteScopedTestOrganization(t *testing.T, orgID string) {
	t.Helper()

	resp := superadminClient.Delete(t, fmt.Sprintf("/api/v2.1/admin/organizations/%s/", orgID))
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return
	}
	body := responseBody(t, resp)
	t.Errorf("cleanup delete org %s failed: status=%d body=%s", orgID, resp.StatusCode, body)
}

func assertScopedStatus(t *testing.T, client *testClient, method, path string, expected int) {
	t.Helper()

	resp := client.Do(t, method, path, nil)
	expectStatus(t, resp, expected)
	resp.Body.Close()
}
