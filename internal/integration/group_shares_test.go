//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestGroupShareDeletedWithGroupCleanup(t *testing.T) {
	groupName := fmt.Sprintf("inttest-group-%d", time.Now().UnixNano())
	createGroupResp := adminClient.PostJSON(t, "/api/v2.1/groups/", map[string]string{"name": groupName})
	expectStatus(t, createGroupResp, http.StatusCreated)

	group := responseJSON(t, createGroupResp)
	groupID, ok := group["id"].(string)
	if !ok || groupID == "" {
		t.Fatalf("failed to get group id from response: %v", group)
	}

	groupDeleted := false
	t.Cleanup(func() {
		if groupDeleted {
			return
		}
		resp := adminClient.Delete(t, fmt.Sprintf("/api/v2.1/groups/%s/", groupID))
		resp.Body.Close()
	})

	addMemberResp := adminClient.PostForm(t, fmt.Sprintf("/api/v2.1/groups/%s/members/", groupID), url.Values{
		"email": {"user@sesamefs.local"},
	})
	expectStatus(t, addMemberResp, http.StatusOK)
	addMemberResp.Body.Close()

	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-group-share-%d", time.Now().UnixNano()))

	shareResp := adminClient.PutForm(t, fmt.Sprintf("/api2/repos/%s/dir/shared_items/?p=/", repoID), url.Values{
		"share_type": {"group"},
		"group_id":   {groupID},
		"permission": {"r"},
	})
	expectStatus(t, shareResp, http.StatusOK)
	shareResp.Body.Close()

	assertGroupSharePresent(t, repoID, groupID, true)

	deleteGroupResp := adminClient.Delete(t, fmt.Sprintf("/api/v2.1/groups/%s/", groupID))
	expectStatus(t, deleteGroupResp, http.StatusOK)
	deleteGroupResp.Body.Close()
	groupDeleted = true

	deadline := time.Now().Add(5 * time.Second)
	for {
		if !groupSharePresent(t, repoID, groupID) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("group share for repo %s and group %s still present after group deletion", repoID, groupID)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func assertGroupSharePresent(t *testing.T, repoID, groupID string, expected bool) {
	t.Helper()
	if groupSharePresent(t, repoID, groupID) != expected {
		t.Fatalf("expected group share presence=%t for repo %s and group %s", expected, repoID, groupID)
	}
}

func groupSharePresent(t *testing.T, repoID, groupID string) bool {
	t.Helper()
	resp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/dir/shared_items/?p=/&share_type=group", repoID))
	expectStatus(t, resp, http.StatusOK)

	var shares []map[string]interface{}
	decodeJSON(t, resp, &shares)
	for _, share := range shares {
		groupInfo, ok := share["group_info"].(map[string]interface{})
		if !ok {
			continue
		}
		if id, ok := groupInfo["id"].(string); ok && id == groupID {
			return true
		}
	}
	return false
}
