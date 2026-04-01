//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestCreateAndListLibraries(t *testing.T) {
	name := fmt.Sprintf("inttest-lib-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)

	t.Run("library appears in list", func(t *testing.T) {
		resp := adminClient.Get(t, "/api/v2.1/repos/?type=mine")
		expectStatus(t, resp, http.StatusOK)

		var result map[string]interface{}
		decodeJSON(t, resp, &result)

		repos, ok := result["repos"].([]interface{})
		if !ok {
			t.Fatal("expected repos array in response")
		}

		if !containsEntry(repos, "repo_id", repoID) {
			t.Errorf("created library %s not found in list", repoID)
		}
	})

	t.Run("get library by ID", func(t *testing.T) {
		resp := adminClient.Get(t, fmt.Sprintf("/api/v2.1/repos/%s/", repoID))
		expectStatus(t, resp, http.StatusOK)

		result := responseJSON(t, resp)
		if result["repo_id"] != repoID {
			t.Errorf("expected repo_id %s, got %v", repoID, result["repo_id"])
		}
	})
}

func TestRenameLibrary(t *testing.T) {
	name := fmt.Sprintf("inttest-rename-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)

	newName := name + "-renamed"
	body := map[string]string{"repo_name": newName}
	// Rename is only available on /api2/ routes, not /api/v2.1/
	resp := adminClient.PostJSON(t, fmt.Sprintf("/api2/repos/%s/?op=rename", repoID), body)
	expectStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// Verify name changed
	getResp := adminClient.Get(t, fmt.Sprintf("/api/v2.1/repos/%s/", repoID))
	expectStatus(t, getResp, http.StatusOK)

	result := responseJSON(t, getResp)
	if result["repo_name"] != newName {
		t.Errorf("expected repo_name %q, got %v", newName, result["repo_name"])
	}
}

func TestDeleteLibrary(t *testing.T) {
	name := fmt.Sprintf("inttest-delete-%d", time.Now().UnixNano())
	repoID := createDisposableTestLibrary(t, adminClient, name)

	// Delete it
	delResp := adminClient.Delete(t, fmt.Sprintf("/api/v2.1/repos/%s/", repoID))
	expectStatus(t, delResp, http.StatusOK)
	delResp.Body.Close()

	// Verify 404 on get
	getResp := adminClient.Get(t, fmt.Sprintf("/api/v2.1/repos/%s/", repoID))
	if getResp.StatusCode != http.StatusNotFound && getResp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 404 or 403 after delete, got %d", getResp.StatusCode)
	}
	getResp.Body.Close()
}

func TestLibraryPermissions(t *testing.T) {
	timestamp := time.Now().UnixNano()

	t.Run("readonly cannot create library", func(t *testing.T) {
		body := map[string]string{"repo_name": fmt.Sprintf("inttest-readonly-%d", timestamp)}
		resp := readonlyClient.PostJSON(t, "/api/v2.1/repos/", body)
		expectStatus(t, resp, http.StatusForbidden)
		resp.Body.Close()
	})

	t.Run("guest cannot create library", func(t *testing.T) {
		body := map[string]string{"repo_name": fmt.Sprintf("inttest-guest-%d", timestamp)}
		resp := guestClient.PostJSON(t, "/api/v2.1/repos/", body)
		expectStatus(t, resp, http.StatusForbidden)
		resp.Body.Close()
	})

	t.Run("user can create own library", func(t *testing.T) {
		name := fmt.Sprintf("inttest-userperm-%d", timestamp)
		repoID := createTestLibrary(t, userClient, name)
		if repoID == "" {
			t.Fatal("user should be able to create a library")
		}
	})
}

func TestEncryptedLibrary(t *testing.T) {
	name := fmt.Sprintf("inttest-encrypted-%d", time.Now().UnixNano())

	// Create encrypted library — "encrypted": true is required alongside "passwd"
	body := map[string]interface{}{
		"repo_name": name,
		"encrypted": true,
		"passwd":    "test-password-123",
	}
	repoID := createLibraryWithBody(t, adminClient, name, body, false)

	t.Cleanup(func() {
		resp := adminClient.Delete(t, fmt.Sprintf("/api/v2.1/repos/%s/", repoID))
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound {
			resp.Body.Close()
			return
		}
		body := responseBody(t, resp)
		t.Errorf("cleanup delete library %s failed: status=%d body=%s", repoID, resp.StatusCode, body)
	})

	// Verify encrypted flag
	getResp := adminClient.Get(t, fmt.Sprintf("/api/v2.1/repos/%s/", repoID))
	expectStatus(t, getResp, http.StatusOK)

	result := responseJSON(t, getResp)
	encrypted := result["encrypted"]
	if encrypted == nil || encrypted == false {
		t.Errorf("expected encrypted to be true, got %v", encrypted)
	}

	// Unlock library (set password / decrypt session)
	setPassValues := url.Values{"password": {"test-password-123"}}
	setPassResp := adminClient.PostForm(t, fmt.Sprintf("/api/v2.1/repos/%s/set-password/", repoID), setPassValues)
	if setPassResp.StatusCode != http.StatusOK {
		t.Errorf("set-password expected 200, got %d", setPassResp.StatusCode)
	}
	setPassResp.Body.Close()
}
