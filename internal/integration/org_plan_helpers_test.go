//go:build integration

package integration

import "testing"

func ensureDefaultOrgSupportsGroups(t *testing.T) {
	t.Helper()

	original := getAdminOrganizationInfo(t, defaultOrgID)
	t.Cleanup(func() {
		updateAdminOrganization(t, defaultOrgID, map[string]interface{}{
			"plan":         jsonString(original, "plan"),
			"quota_policy": jsonString(original, "quota_policy"),
		})
	})

	if jsonString(original, "quota_policy") == "soft" {
		return
	}

	updateAdminOrganization(t, defaultOrgID, map[string]interface{}{
		"plan":         "pro",
		"quota_policy": "soft",
	})
}

func updateAdminOrganization(t *testing.T, orgID string, body map[string]interface{}) {
	t.Helper()
	resp := superadminClient.PutJSON(t, "/api/v2.1/admin/organizations/"+orgID+"/", body)
	expectStatus(t, resp, 200)
	resp.Body.Close()
}

func jsonString(payload map[string]interface{}, key string) string {
	if raw, ok := payload[key]; ok {
		if value, ok := raw.(string); ok {
			return value
		}
	}
	return ""
}
