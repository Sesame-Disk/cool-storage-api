//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestLibraryProjectionRegression_CreateRenameDeleteRestore(t *testing.T) {
	name := fmt.Sprintf("inttest-lib-projection-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)

	row := requireAdminLibraryByQuery(t, superadminClient, repoID)
	if got, _ := row["name"].(string); got != name {
		t.Fatalf("admin projection name = %q, want %q", got, name)
	}

	renamed := name + "-renamed"
	resp := adminClient.PostJSON(t, fmt.Sprintf("/api2/repos/%s/?op=rename", repoID), map[string]string{"repo_name": renamed})
	expectStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	waitForIntegrationCondition(t, "renamed library to appear in admin projection", func() bool {
		row, ok := adminLibraryByQuery(t, superadminClient, repoID)
		if !ok {
			return false
		}
		got, _ := row["name"].(string)
		return got == renamed
	})

	deleteResp := adminClient.Delete(t, fmt.Sprintf("/api/v2.1/repos/%s/", repoID))
	expectStatus(t, deleteResp, http.StatusOK)
	deleteResp.Body.Close()

	waitForIntegrationCondition(t, "deleted library to leave admin active projection", func() bool {
		_, ok := adminLibraryByQuery(t, superadminClient, repoID)
		return !ok
	})
	waitForIntegrationCondition(t, "deleted library to appear in admin trash projection", func() bool {
		return adminTrashContainsRepo(t, superadminClient, repoID, defaultAdminEmail)
	})

	restoreResp := adminClient.Do(t, http.MethodPut, fmt.Sprintf("/api/v2.1/repos/deleted/%s/", repoID), nil)
	expectStatus(t, restoreResp, http.StatusOK)
	restoreResp.Body.Close()
	t.Cleanup(func() {
		resp := adminClient.Delete(t, fmt.Sprintf("/api/v2.1/repos/%s/", repoID))
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound {
			resp.Body.Close()
			return
		}
		body := responseBody(t, resp)
		t.Errorf("cleanup delete library %s failed: status=%d body=%s", repoID, resp.StatusCode, body)
	})

	waitForIntegrationCondition(t, "restored library to reappear in admin projection", func() bool {
		row, ok := adminLibraryByQuery(t, superadminClient, repoID)
		if !ok {
			return false
		}
		got, _ := row["name"].(string)
		return got == renamed
	})
	waitForIntegrationCondition(t, "restored library to leave admin trash projection", func() bool {
		return !adminTrashContainsRepo(t, superadminClient, repoID, defaultAdminEmail)
	})
}

func TestLibraryProjectionRegression_FileCreateUpdatesAdminStats(t *testing.T) {
	name := fmt.Sprintf("inttest-lib-stats-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)

	createResp := adminClient.PostForm(t, fmt.Sprintf("/api2/repos/%s/file/?p=/projection-stats.txt&operation=create", repoID), url.Values{})
	if createResp.StatusCode != http.StatusCreated && createResp.StatusCode != http.StatusOK {
		body := responseBody(t, createResp)
		t.Fatalf("expected 200 or 201 for file create, got %d body=%s", createResp.StatusCode, body)
	}
	createResp.Body.Close()

	waitForIntegrationCondition(t, "file count to reach admin projection after file create", func() bool {
		row, ok := adminLibraryByQuery(t, superadminClient, repoID)
		if !ok {
			return false
		}
		fileCount, ok := row["file_count"].(float64)
		return ok && fileCount >= 1
	})
}

func TestAdminCreateLibraryProjectionVisibleImmediately(t *testing.T) {
	name := fmt.Sprintf("inttest-admin-lib-projection-%d", time.Now().UnixNano())
	createResp := superadminClient.PostJSON(t, "/api/v2.1/admin/libraries/", map[string]string{
		"name":  name,
		"owner": defaultUserEmail,
	})
	expectStatus(t, createResp, http.StatusOK)
	result := responseJSON(t, createResp)

	repoID, _ := result["id"].(string)
	if repoID == "" {
		t.Fatalf("admin create library response missing id: %v", result)
	}
	t.Cleanup(func() {
		resp := superadminClient.Delete(t, fmt.Sprintf("/api/v2.1/admin/libraries/%s/", repoID))
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound {
			resp.Body.Close()
			return
		}
		body := responseBody(t, resp)
		t.Errorf("cleanup delete admin library %s failed: status=%d body=%s", repoID, resp.StatusCode, body)
	})

	row := requireAdminLibraryByQuery(t, superadminClient, repoID)
	if got, _ := row["name"].(string); got != name {
		t.Fatalf("admin-created library name = %q, want %q", got, name)
	}
	if got, _ := row["owner_email"].(string); got != defaultUserEmail {
		t.Fatalf("admin-created library owner_email = %q, want %q", got, defaultUserEmail)
	}
}

func TestGroupOwnedLibraryProjectionVisibleImmediately(t *testing.T) {
	groupName := fmt.Sprintf("inttest-group-owned-%d", time.Now().UnixNano())
	groupID := createGroupForRegressionTest(t, adminClient, groupName)
	repoName := fmt.Sprintf("inttest-group-owned-lib-%d", time.Now().UnixNano())

	createResp := adminClient.PostJSON(t, fmt.Sprintf("/api/v2.1/groups/%s/group-owned-libraries/", groupID), map[string]string{"name": repoName})
	expectStatus(t, createResp, http.StatusOK)
	result := responseJSON(t, createResp)

	repoID, _ := result["repo_id"].(string)
	if repoID == "" {
		t.Fatalf("group-owned library response missing repo_id: %v", result)
	}
	t.Cleanup(func() {
		resp := adminClient.Delete(t, fmt.Sprintf("/api/v2.1/repos/%s/", repoID))
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound {
			resp.Body.Close()
			return
		}
		body := responseBody(t, resp)
		t.Errorf("cleanup delete group-owned library %s failed: status=%d body=%s", repoID, resp.StatusCode, body)
	})

	row := requireAdminLibraryByQuery(t, superadminClient, repoID)
	if got, _ := row["name"].(string); got != repoName {
		t.Fatalf("group-owned library name = %q, want %q", got, repoName)
	}
	assertGroupSharePresent(t, repoID, groupID, true)
}

func waitForIntegrationCondition(t *testing.T, description string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(pollTimeout)
	for {
		if check() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", description)
		}
		time.Sleep(pollInterval)
	}
}

func requireAdminLibraryByQuery(t *testing.T, c *testClient, query string) map[string]interface{} {
	t.Helper()
	row, ok := adminLibraryByQuery(t, c, query)
	if !ok {
		t.Fatalf("admin library projection missing for query %q", query)
	}
	return row
}

func adminLibraryByQuery(t *testing.T, c *testClient, query string) (map[string]interface{}, bool) {
	t.Helper()
	resp := c.Get(t, "/api/v2.1/admin/search-libraries/?name_or_id="+url.QueryEscape(query)+"&page=1&per_page=100")
	expectStatus(t, resp, http.StatusOK)
	payload := responseJSON(t, resp)
	entries, ok := payload["repo_list"].([]interface{})
	if !ok {
		return nil, false
	}
	for _, entry := range entries {
		row, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		if id, _ := row["id"].(string); id == query {
			return row, true
		}
		if repoID, _ := row["repo_id"].(string); repoID == query {
			return row, true
		}
		if name, _ := row["name"].(string); name == query {
			return row, true
		}
		if repoName, _ := row["repo_name"].(string); repoName == query {
			return row, true
		}
	}
	return nil, false
}

func adminTrashContainsRepo(t *testing.T, c *testClient, repoID, ownerEmail string) bool {
	t.Helper()
	path := "/api/v2.1/admin/trash-libraries/?page=1&per_page=100"
	if ownerEmail != "" {
		path += "&owner=" + url.QueryEscape(ownerEmail)
	}
	resp := c.Get(t, path)
	expectStatus(t, resp, http.StatusOK)
	payload := responseJSON(t, resp)
	entries, ok := payload["repos"].([]interface{})
	if !ok {
		return false
	}
	return containsEntry(entries, "id", repoID)
}
