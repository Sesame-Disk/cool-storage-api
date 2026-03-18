//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestLeaveShareRepo_RemovesPersonalShare(t *testing.T) {
	probe := userClient.Get(t, "/api2/beshared-repos/")
	if probe.StatusCode == http.StatusNotFound {
		probe.Body.Close()
		t.Skip("/api2/beshared-repos/ endpoint not available in this integration environment")
	}
	probe.Body.Close()

	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-leave-share-%d", time.Now().UnixNano()))

	shareResp := adminClient.PutForm(t, fmt.Sprintf("/api2/repos/%s/dir/shared_items/?p=/", repoID), url.Values{
		"share_type": {"user"},
		"username":   {"user@sesamefs.local"},
		"permission": {"r"},
	})
	if shareResp.StatusCode != http.StatusOK {
		body := responseBody(t, shareResp)
		t.Fatalf("create share status=%d body=%s", shareResp.StatusCode, body)
	}
	shareResp.Body.Close()

	assertBeSharedContainsRepo(t, userClient, repoID, true)

	leaveResp := userClient.Delete(t, fmt.Sprintf("/api2/beshared-repos/%s/?share_type=personal&from=admin@sesamefs.local", repoID))
	if leaveResp.StatusCode != http.StatusOK {
		body := responseBody(t, leaveResp)
		t.Fatalf("leave share status=%d body=%s", leaveResp.StatusCode, body)
	}
	leaveResp.Body.Close()

	assertBeSharedContainsRepo(t, userClient, repoID, false)
}

func assertBeSharedContainsRepo(t *testing.T, client *testClient, repoID string, expected bool) {
	t.Helper()

	resp := client.Get(t, "/api2/beshared-repos/")
	if resp.StatusCode != http.StatusOK {
		body := responseBody(t, resp)
		t.Fatalf("list beshared status=%d body=%s", resp.StatusCode, body)
	}

	var repos []map[string]interface{}
	decodeJSON(t, resp, &repos)

	found := false
	for _, repo := range repos {
		if id, ok := repo["repo_id"].(string); ok && id == repoID {
			found = true
			break
		}
	}

	if found != expected {
		t.Fatalf("expected repo %s presence=%t in beshared repos, got %t", repoID, expected, found)
	}
}
