//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
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

func TestSyncHeadUpdateKeepsLookupAndAdminProjectionAligned(t *testing.T) {
	name := fmt.Sprintf("inttest-sync-head-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)
	session := shareProjectionDBForTest(t).Session()

	initial := readLibrarySyncHeadState(t, session, repoID)

	nextHead := fmt.Sprintf("%040x", time.Now().UnixNano())
	insertSyntheticCommitForTest(t, session, repoID, nextHead, initial.HeadCommitID, initial.RootFSID, "integration head advance")

	resp := adminClient.Do(t, http.MethodPut, fmt.Sprintf("/seafhttp/repo/%s/commit/HEAD?head=%s", repoID, url.QueryEscape(nextHead)), nil)
	if resp.StatusCode != http.StatusOK {
		body := responseBody(t, resp)
		t.Fatalf("sync HEAD update failed: status=%d body=%s", resp.StatusCode, body)
	}
	resp.Body.Close()

	waitForIntegrationCondition(t, "sync HEAD dual-write to align canonical, lookup, and admin projection rows", func() bool {
		current := readLibrarySyncHeadState(t, session, repoID)
		return current.HeadCommitID == nextHead &&
			current.LookupHeadCommitID == nextHead &&
			current.UpdatedAt.After(initial.UpdatedAt) &&
			current.ProjectionUpdatedAt.Equal(current.UpdatedAt)
	})
}

func TestSyncHeadConflictReturnsOKWithoutRollback(t *testing.T) {
	name := fmt.Sprintf("inttest-sync-conflict-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)
	session := shareProjectionDBForTest(t).Session()

	initial := readLibrarySyncHeadState(t, session, repoID)
	acceptedHead := fmt.Sprintf("%040x", time.Now().UnixNano())
	staleHead := fmt.Sprintf("%040x", time.Now().UnixNano()+1)
	insertSyntheticCommitForTest(t, session, repoID, acceptedHead, initial.HeadCommitID, initial.RootFSID, "integration accepted head")
	insertSyntheticCommitForTest(t, session, repoID, staleHead, initial.HeadCommitID, initial.RootFSID, "integration stale head")

	resp := adminClient.Do(t, http.MethodPut, fmt.Sprintf("/seafhttp/repo/%s/commit/HEAD?head=%s", repoID, url.QueryEscape(acceptedHead)), nil)
	if resp.StatusCode != http.StatusOK {
		body := responseBody(t, resp)
		t.Fatalf("initial sync HEAD update failed: status=%d body=%s", resp.StatusCode, body)
	}
	resp.Body.Close()

	var advanced librarySyncHeadState
	waitForIntegrationCondition(t, "accepted sync head to become authoritative", func() bool {
		current := readLibrarySyncHeadState(t, session, repoID)
		if current.HeadCommitID != acceptedHead || current.LookupHeadCommitID != acceptedHead {
			return false
		}
		if !current.UpdatedAt.After(initial.UpdatedAt) || !current.ProjectionUpdatedAt.Equal(current.UpdatedAt) {
			return false
		}
		advanced = current
		return true
	})

	conflictResp := adminClient.Do(t, http.MethodPut, fmt.Sprintf("/seafhttp/repo/%s/commit/HEAD?head=%s", repoID, url.QueryEscape(staleHead)), nil)
	if conflictResp.StatusCode != http.StatusOK {
		body := responseBody(t, conflictResp)
		t.Fatalf("conflicting sync HEAD update returned status=%d body=%s", conflictResp.StatusCode, body)
	}
	conflictResp.Body.Close()

	stabilized := readLibrarySyncHeadState(t, session, repoID)
	if stabilized.HeadCommitID != acceptedHead {
		t.Fatalf("canonical head rolled back to %q, want %q", stabilized.HeadCommitID, acceptedHead)
	}
	if stabilized.LookupHeadCommitID != acceptedHead {
		t.Fatalf("lookup head rolled back to %q, want %q", stabilized.LookupHeadCommitID, acceptedHead)
	}
	if !stabilized.UpdatedAt.Equal(advanced.UpdatedAt) {
		t.Fatalf("canonical updated_at changed on stale conflict: got %s want %s", stabilized.UpdatedAt.Format(time.RFC3339Nano), advanced.UpdatedAt.Format(time.RFC3339Nano))
	}
	if !stabilized.ProjectionUpdatedAt.Equal(advanced.ProjectionUpdatedAt) {
		t.Fatalf("projection updated_at changed on stale conflict: got %s want %s", stabilized.ProjectionUpdatedAt.Format(time.RFC3339Nano), advanced.ProjectionUpdatedAt.Format(time.RFC3339Nano))
	}
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

type librarySyncHeadState struct {
	HeadCommitID       string
	LookupHeadCommitID string
	RootFSID           string
	UpdatedAt          time.Time
	ProjectionUpdatedAt time.Time
}

func readLibrarySyncHeadState(t *testing.T, session interface {
	Query(stmt string, values ...interface{}) *gocql.Query
}, repoID string) librarySyncHeadState {
	t.Helper()

	var state librarySyncHeadState
	if err := session.Query(`
		SELECT head_commit_id, updated_at FROM libraries WHERE org_id = ? AND library_id = ?
	`, defaultOrgID, repoID).Scan(&state.HeadCommitID, &state.UpdatedAt); err != nil {
		t.Fatalf("failed to read canonical sync head state for %s: %v", repoID, err)
	}
	if err := session.Query(`
		SELECT head_commit_id FROM libraries_by_id WHERE library_id = ?
	`, repoID).Scan(&state.LookupHeadCommitID); err != nil {
		t.Fatalf("failed to read lookup sync head state for %s: %v", repoID, err)
	}
	if err := session.Query(`
		SELECT updated_at FROM libraries_by_org_updated WHERE org_id = ? AND library_id = ?
	`, defaultOrgID, repoID).Scan(&state.ProjectionUpdatedAt); err != nil {
		t.Fatalf("failed to read projection sync head state for %s: %v", repoID, err)
	}
	if err := session.Query(`
		SELECT root_fs_id FROM commits WHERE library_id = ? AND commit_id = ?
	`, repoID, state.HeadCommitID).Scan(&state.RootFSID); err != nil {
		t.Fatalf("failed to read root fs id for %s/%s: %v", repoID, state.HeadCommitID, err)
	}

	return state
}

func insertSyntheticCommitForTest(t *testing.T, session interface {
	Query(stmt string, values ...interface{}) *gocql.Query
}, repoID, commitID, parentID, rootFSID, description string) {
	t.Helper()
	if err := session.Query(`
		INSERT INTO commits (library_id, commit_id, parent_id, root_fs_id, creator_id, description, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, repoID, commitID, parentID, rootFSID, defaultOrgID, description, time.Now().UTC()).Exec(); err != nil {
		t.Fatalf("failed to insert synthetic commit %s for %s: %v", commitID, repoID, err)
	}
}
