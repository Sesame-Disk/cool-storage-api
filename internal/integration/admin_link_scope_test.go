//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestAdminAndOrgLinkScopes(t *testing.T) {
	unique := fmt.Sprintf("inttest-link-scope-%d", time.Now().UnixNano())
	defaultRepoID := createTestLibrary(t, adminClient, unique+"-default")
	platformRepoID := createTestLibrary(t, superadminClient, unique+"-platform")

	createShareLinkForTest(t, adminClient, defaultRepoID, "/")
	createShareLinkForTest(t, superadminClient, platformRepoID, "/")
	createUploadLinkForTest(t, adminClient, defaultRepoID, "/")
	createUploadLinkForTest(t, superadminClient, platformRepoID, "/")

	t.Run("sysadmin share links are global across orgs", func(t *testing.T) {
		payload := getJSONMap(t, superadminClient.Get(t, "/api/v2.1/admin/share-links/?search="+unique))
		assertLinkCountAtLeast(t, payload, "share_link_list", 2)
	})

	t.Run("sysadmin upload links are global across orgs", func(t *testing.T) {
		payload := getJSONMap(t, superadminClient.Get(t, "/api/v2.1/admin/upload-links/?search="+unique))
		assertLinkCountAtLeast(t, payload, "upload_link_list", 2)
	})

	t.Run("org admin share links stay scoped to caller org", func(t *testing.T) {
		payload := getJSONMap(t, adminClient.Get(t, "/api/v2.1/org/admin/links/?search="+unique))
		assertLinkCountExactly(t, payload, "link_list", 1)
	})

	t.Run("superadmin org share links stay scoped to caller org", func(t *testing.T) {
		payload := getJSONMap(t, superadminClient.Get(t, "/api/v2.1/org/admin/links/?search="+unique))
		assertLinkCountExactly(t, payload, "link_list", 1)
	})

	t.Run("org admin upload links stay scoped to caller org", func(t *testing.T) {
		payload := getJSONMap(t, adminClient.Get(t, "/api/v2.1/org/admin/upload-links/?search="+unique))
		assertLinkCountExactly(t, payload, "upload_link_list", 1)
	})

	t.Run("superadmin org upload links stay scoped to caller org", func(t *testing.T) {
		payload := getJSONMap(t, superadminClient.Get(t, "/api/v2.1/org/admin/upload-links/?search="+unique))
		assertLinkCountExactly(t, payload, "upload_link_list", 1)
	})
}

func createShareLinkForTest(t *testing.T, client *testClient, repoID, path string) string {
	t.Helper()
	resp := postShareLinkForTest(t, client, repoID, path)
	if resp.StatusCode == http.StatusForbidden {
		payload := responseJSON(t, resp)
		if payload["error"] == "Share link limit reached" {
			deleteFirstOrgShareLinkForTest(t, client)
			resp = postShareLinkForTest(t, client, repoID, path)
		}
	}
	expectStatus(t, resp, http.StatusOK)
	payload := responseJSON(t, resp)
	token, _ := payload["token"].(string)
	if token == "" {
		t.Fatalf("expected share link token, got %v", payload)
	}
	t.Cleanup(func() {
		resp := client.Delete(t, "/api/v2.1/org/admin/links/"+token+"/")
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound {
			resp.Body.Close()
			return
		}
		body := responseBody(t, resp)
		t.Errorf("cleanup delete share link %s failed: status=%d body=%s", token, resp.StatusCode, body)
	})
	return token
}

func postShareLinkForTest(t *testing.T, client *testClient, repoID, path string) *http.Response {
	t.Helper()
	return client.PostJSON(t, "/api/v2.1/share-links/", map[string]interface{}{
		"repo_id":     repoID,
		"path":        path,
		"permissions": "preview_download",
	})
}

func createUploadLinkForTest(t *testing.T, client *testClient, repoID, path string) string {
	t.Helper()
	resp := postUploadLinkForTest(t, client, repoID, path)
	if resp.StatusCode == http.StatusForbidden {
		payload := responseJSON(t, resp)
		if payload["error"] == "Upload link limit reached" {
			deleteFirstOrgUploadLinkForTest(t, client)
			resp = postUploadLinkForTest(t, client, repoID, path)
		}
	}
	expectStatus(t, resp, http.StatusOK)
	payload := responseJSON(t, resp)
	token, _ := payload["token"].(string)
	if token == "" {
		t.Fatalf("expected upload link token, got %v", payload)
	}
	t.Cleanup(func() {
		resp := client.Delete(t, "/api/v2.1/org/admin/upload-links/"+token+"/")
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound {
			resp.Body.Close()
			return
		}
		body := responseBody(t, resp)
		t.Errorf("cleanup delete upload link %s failed: status=%d body=%s", token, resp.StatusCode, body)
	})
	return token
}

func postUploadLinkForTest(t *testing.T, client *testClient, repoID, path string) *http.Response {
	t.Helper()
	return client.PostJSON(t, "/api/v2.1/upload-links/", map[string]interface{}{
		"repo_id": repoID,
		"path":    path,
	})
}

func deleteFirstOrgUploadLinkForTest(t *testing.T, client *testClient) {
	t.Helper()
	resp := client.Get(t, "/api/v2.1/org/admin/upload-links/?page=1&per_page=1")
	expectStatus(t, resp, http.StatusOK)
	payload := responseJSON(t, resp)
	links, _ := payload["upload_link_list"].([]interface{})
	if len(links) == 0 {
		t.Fatalf("expected at least one org upload link to clear capacity, got %v", payload)
	}
	first, _ := links[0].(map[string]interface{})
	token, _ := first["token"].(string)
	if token == "" {
		t.Fatalf("expected upload link token in response, got %v", first)
	}

	deleteResp := client.Delete(t, "/api/v2.1/org/admin/upload-links/"+token+"/")
	expectStatus(t, deleteResp, http.StatusOK)
	deleteResp.Body.Close()
}

func deleteFirstOrgShareLinkForTest(t *testing.T, client *testClient) {
	t.Helper()
	resp := client.Get(t, "/api/v2.1/org/admin/links/?page=1&per_page=1")
	expectStatus(t, resp, http.StatusOK)
	payload := responseJSON(t, resp)
	links, _ := payload["link_list"].([]interface{})
	if len(links) == 0 {
		t.Fatalf("expected at least one org share link to clear capacity, got %v", payload)
	}
	first, _ := links[0].(map[string]interface{})
	token, _ := first["token"].(string)
	if token == "" {
		t.Fatalf("expected share link token in response, got %v", first)
	}

	deleteResp := client.Delete(t, "/api/v2.1/org/admin/links/"+token+"/")
	expectStatus(t, deleteResp, http.StatusOK)
	deleteResp.Body.Close()
}

func getJSONMap(t *testing.T, resp *http.Response) map[string]interface{} {
	t.Helper()
	expectStatus(t, resp, http.StatusOK)
	return responseJSON(t, resp)
}

func assertLinkCountAtLeast(t *testing.T, payload map[string]interface{}, key string, expectedMin int) {
	t.Helper()
	entries, ok := payload[key].([]interface{})
	if !ok {
		t.Fatalf("expected %s array in response, got %v", key, payload)
	}
	if len(entries) < expectedMin {
		t.Fatalf("expected at least %d entries in %s, got %d (%v)", expectedMin, key, len(entries), payload)
	}
}

func assertLinkCountExactly(t *testing.T, payload map[string]interface{}, key string, expected int) {
	t.Helper()
	entries, ok := payload[key].([]interface{})
	if !ok {
		t.Fatalf("expected %s array in response, got %v", key, payload)
	}
	if len(entries) != expected {
		t.Fatalf("expected %d entries in %s, got %d (%v)", expected, key, len(entries), payload)
	}
}
