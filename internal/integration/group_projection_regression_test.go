//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestCreateGroupProjectionSyncAllowsRootGroups(t *testing.T) {
	groupName := fmt.Sprintf("inttest-group-projection-%d", time.Now().UnixNano())
	groupID := createGroupForRegressionTest(t, adminClient, groupName)

	resp := adminClient.Get(t, fmt.Sprintf("/api/v2.1/groups/%s/", groupID))
	expectStatus(t, resp, http.StatusOK)
	group := responseJSON(t, resp)

	if got, _ := group["name"].(string); got != groupName {
		t.Fatalf("group name = %q, want %q", got, groupName)
	}
	if got, _ := group["id"].(string); got != groupID {
		t.Fatalf("group id = %q, want %q", got, groupID)
	}
	if got, ok := group["member_count"].(float64); !ok || got < 1 {
		t.Fatalf("member_count = %#v, want at least 1", group["member_count"])
	}

	shareableResp := adminClient.Get(t, "/api/v2.1/shareable-groups/")
	expectStatus(t, shareableResp, http.StatusOK)
	var shareableGroups []map[string]interface{}
	decodeJSON(t, shareableResp, &shareableGroups)
	if !shareableGroupPresent(shareableGroups, groupID, groupName) {
		t.Fatalf("new group %s was not returned by shareable-groups", groupID)
	}
}

func TestAdminCreateGroupProjectionSyncAllowsRootGroups(t *testing.T) {
	groupName := fmt.Sprintf("inttest-admin-group-projection-%d", time.Now().UnixNano())
	createResp := superadminClient.PostJSON(t, "/api/v2.1/admin/groups/", map[string]string{"group_name": groupName})
	expectStatus(t, createResp, http.StatusCreated)
	group := responseJSON(t, createResp)

	groupID, ok := group["id"].(string)
	if !ok || groupID == "" {
		t.Fatalf("failed to get admin-created group id from response: %v", group)
	}

	t.Cleanup(func() {
		resp := superadminClient.Delete(t, fmt.Sprintf("/api/v2.1/admin/groups/%s/", groupID))
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound {
			resp.Body.Close()
			return
		}
		body := responseBody(t, resp)
		t.Errorf("cleanup delete admin group %s failed: status=%d body=%s", groupID, resp.StatusCode, body)
	})

	if got, _ := group["name"].(string); got != groupName {
		t.Fatalf("admin create group name = %q, want %q", got, groupName)
	}

	listResp := superadminClient.Get(t, "/api/v2.1/admin/groups/?page=1&per_page=100")
	expectStatus(t, listResp, http.StatusOK)
	listBody := responseJSON(t, listResp)
	groups, ok := listBody["groups"].([]interface{})
	if !ok {
		t.Fatalf("admin group list missing groups array: %v", listBody)
	}
	if !containsEntry(groups, "id", groupID) {
		t.Fatalf("admin-created group %s was not returned by admin groups list", groupID)
	}
}

func createGroupForRegressionTest(t *testing.T, c *testClient, groupName string) string {
	t.Helper()

	createResp := c.PostJSON(t, "/api/v2.1/groups/", map[string]string{"name": groupName})
	if createResp.StatusCode != http.StatusCreated {
		body := responseJSON(t, createResp)
		if upgradeRequired, _ := body["upgrade_required"].(bool); upgradeRequired {
			t.Skip("group creation is disabled by the current org plan in this integration environment")
		}
		t.Fatalf("create group %q failed: status=%d body=%v", groupName, createResp.StatusCode, body)
	}

	group := responseJSON(t, createResp)
	groupID, ok := group["id"].(string)
	if !ok || groupID == "" {
		t.Fatalf("failed to get group id from create response: %v", group)
	}

	t.Cleanup(func() {
		resp := c.Delete(t, fmt.Sprintf("/api/v2.1/groups/%s/", groupID))
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound {
			resp.Body.Close()
			return
		}
		body := responseBody(t, resp)
		t.Errorf("cleanup delete group %s failed: status=%d body=%s", groupID, resp.StatusCode, body)
	})

	return groupID
}

func shareableGroupPresent(groups []map[string]interface{}, groupID, groupName string) bool {
	for _, group := range groups {
		id, _ := group["id"].(string)
		name, _ := group["name"].(string)
		if id == groupID && name == groupName {
			return true
		}
	}
	return false
}
