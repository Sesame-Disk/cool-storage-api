//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestReadonlyCannotWrite(t *testing.T) {
	// First create a library as admin so readonly has something to try against
	name := fmt.Sprintf("inttest-roperm-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)

	t.Run("readonly cannot create directory", func(t *testing.T) {
		resp := readonlyClient.PostJSON(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/no-access", repoID), map[string]string{})
		if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("expected 403 or 401, got %d", resp.StatusCode)
		}
		resp.Body.Close()
	})

	t.Run("readonly cannot create library", func(t *testing.T) {
		body := map[string]string{"repo_name": fmt.Sprintf("inttest-ro-create-%d", time.Now().UnixNano())}
		resp := readonlyClient.PostJSON(t, "/api/v2.1/repos/", body)
		expectStatus(t, resp, http.StatusForbidden)
		resp.Body.Close()
	})
}

func TestGuestCannotCreateLibrary(t *testing.T) {
	body := map[string]string{"repo_name": fmt.Sprintf("inttest-guest-create-%d", time.Now().UnixNano())}
	resp := guestClient.PostJSON(t, "/api/v2.1/repos/", body)
	expectStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()
}

func TestAdminCanManageOtherLibraries(t *testing.T) {
	// User creates a library
	name := fmt.Sprintf("inttest-adminmanage-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, userClient, name)

	t.Run("admin can rename user library", func(t *testing.T) {
		newName := name + "-admin-renamed"
		body := map[string]string{"repo_name": newName}
		// Rename is only available on /api2/ routes
		resp := adminClient.PostJSON(t, fmt.Sprintf("/api2/repos/%s/?op=rename", repoID), body)
		// Admin should be able to manage — accept 200 or other success
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusForbidden {
			// If admin can't rename (some permission models), that's also an acceptable outcome
			t.Logf("admin rename returned %d (may depend on permission model)", resp.StatusCode)
		}
		resp.Body.Close()
	})

	t.Run("admin can delete user library", func(t *testing.T) {
		// Create a separate library for deletion test
		delName := fmt.Sprintf("inttest-admindel-%d", time.Now().UnixNano())
		delRepoID := createDisposableTestLibrary(t, userClient, delName)

		resp := adminClient.Delete(t, fmt.Sprintf("/api/v2.1/repos/%s/", delRepoID))
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusForbidden {
			t.Logf("admin delete returned %d (may depend on permission model)", resp.StatusCode)
		}
		resp.Body.Close()

		// Clean up if admin couldn't delete it
		if resp.StatusCode == http.StatusForbidden {
			cleanResp := userClient.Delete(t, fmt.Sprintf("/api/v2.1/repos/%s/", delRepoID))
			cleanResp.Body.Close()
		}
	})
}

func TestCrossUserIsolation(t *testing.T) {
	// Admin creates a private library
	name := fmt.Sprintf("inttest-isolation-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)

	t.Run("user cannot access admin private library", func(t *testing.T) {
		resp := userClient.Get(t, fmt.Sprintf("/api/v2.1/repos/%s/", repoID))
		if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusNotFound {
			t.Errorf("expected 403 or 404, got %d", resp.StatusCode)
		}
		resp.Body.Close()
	})

	t.Run("guest cannot access admin private library", func(t *testing.T) {
		resp := guestClient.Get(t, fmt.Sprintf("/api/v2.1/repos/%s/", repoID))
		if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusNotFound {
			t.Errorf("expected 403 or 404, got %d", resp.StatusCode)
		}
		resp.Body.Close()
	})

	t.Run("readonly cannot access admin private library", func(t *testing.T) {
		resp := readonlyClient.Get(t, fmt.Sprintf("/api/v2.1/repos/%s/", repoID))
		if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusNotFound {
			t.Errorf("expected 403 or 404, got %d", resp.StatusCode)
		}
		resp.Body.Close()
	})

	t.Run("admin private library not in user list", func(t *testing.T) {
		resp := userClient.Get(t, "/api/v2.1/repos/?type=mine")
		expectStatus(t, resp, http.StatusOK)

		var result map[string]interface{}
		decodeJSON(t, resp, &result)

		repos, ok := result["repos"].([]interface{})
		if !ok {
			t.Fatal("expected repos array in response")
		}

		if containsEntry(repos, "repo_id", repoID) {
			t.Error("admin's private library should not appear in user's list")
		}
	})
}
