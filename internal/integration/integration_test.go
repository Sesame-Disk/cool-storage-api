//go:build integration

package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

var (
	baseURL          string
	adminClient      *testClient
	userClient       *testClient
	readonlyClient   *testClient
	guestClient      *testClient
	superadminClient *testClient
)

// ephemeralLibraryPrefixes identifies libraries that the integration suite is
// allowed to delete when the per-org library limit is hit. The list MUST track
// every prefix used by ad-hoc bash scripts under /scripts that create libraries
// against the same dev environment (test-file-history.sh, test-file-ops.sh,
// etc.). Keep this explicit: broad prefixes like "test-" are too aggressive
// for shared dev environments and can delete manually created libraries.
var ephemeralLibraryPrefixes = []string{
	"inttest-",
	"smoke-",
	"sesamefs-public-smoke",
	// Bash test scripts under /scripts. Keep in sync with cleanup-test-repos.sh.
	"batch-ops-test-",
	"encrypted-test-",
	"HistoryTest-",
	"HistoryRetest-",
	"HistoryStateCheck-",
	"FileOpsTest-",
	"history-test-library-",
	"nested-move-copy-test",
	"cross-lib-src-",
	"cross-lib-dst-",
	"search-test-",
	"with-parents-test",
	"api-token-test-",
	"tag-test-library-",
	"sa-test-lib-",
	"test-encrypted",
	"test-libsettings-",
	"test-write-ops",
	"failover-test-",
	"multiregion-test-",
	"sync-test-encrypted-",
	"sync-test-unencrypted-",
	"usa-routing-test-",
	"eu-routing-test-",
}

func TestMain(m *testing.M) {
	baseURL = os.Getenv("SESAMEFS_URL")
	if baseURL == "" {
		baseURL = "http://localhost:3000"
	}

	resolvedBaseURL, err := resolveIntegrationBaseURL(baseURL)
	if err != nil {
		fmt.Printf("Backend not available at %s: %v\n", baseURL, err)
		fmt.Println("")
		fmt.Println("Start the backend with:")
		fmt.Println("  docker compose up -d")
		os.Exit(0)
	}
	baseURL = resolvedBaseURL

	if err := verifyIntegrationAuth(baseURL, "dev-token-admin"); err != nil {
		fmt.Printf("Integration auth not available at %s: %v\n", baseURL, err)
		fmt.Println("")
		fmt.Println("The running backend is reachable, but the standard dev tokens are not enabled.")
		fmt.Println("Set SESAMEFS_URL to an environment with dev tokens, or run the backend with AUTH_DEV_MODE and seeded dev tokens.")
		os.Exit(0)
	}

	// Set up clients for each role
	superadminClient = newTestClient(baseURL, "dev-token-superadmin")
	adminClient = newTestClient(baseURL, "dev-token-admin")
	userClient = newTestClient(baseURL, "dev-token-user")
	readonlyClient = newTestClient(baseURL, "dev-token-readonly")
	guestClient = newTestClient(baseURL, "dev-token-guest")

	os.Exit(m.Run())
}

func resolveIntegrationBaseURL(baseURL string) (string, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	candidates := []string{baseURL}

	if strings.Contains(baseURL, "localhost") {
		candidates = append(candidates, strings.Replace(baseURL, "localhost", "127.0.0.1", 1))
	}

	if os.Getenv("SESAMEFS_URL") == "" {
		candidates = append(candidates,
			"http://127.0.0.1:3000",
			"http://localhost:3000",
			"http://127.0.0.1:8082",
			"http://localhost:8082",
		)
	}

	seen := make(map[string]struct{}, len(candidates))
	filtered := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if _, exists := seen[candidate]; exists {
			continue
		}
		seen[candidate] = struct{}{}
		filtered = append(filtered, candidate)
	}

	var lastErr error
	for _, candidate := range filtered {
		resp, err := client.Get(candidate + "/health")
		if err != nil {
			lastErr = err
			continue
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return candidate, nil
		}
		lastErr = fmt.Errorf("health check returned %d", resp.StatusCode)
	}

	return "", lastErr
}

func verifyIntegrationAuth(baseURL, token string) error {
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(http.MethodGet, baseURL+"/api2/account/info/", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Token "+token)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("account info returned %d", resp.StatusCode)
	}

	return nil
}

// createTestLibrary creates a library and registers cleanup via t.Cleanup.
// Returns the repo_id.
func createTestLibrary(t *testing.T, c *testClient, name string) string {
	return createLibraryForTest(t, c, name, map[string]string{"repo_name": name}, true)
}

func createDisposableTestLibrary(t *testing.T, c *testClient, name string) string {
	return createLibraryForTest(t, c, name, map[string]string{"repo_name": name}, false)
}

func createLibraryWithBody(t *testing.T, c *testClient, name string, body interface{}, cleanup bool) string {
	return createLibraryForTest(t, c, name, body, cleanup)
}

func createLibraryForTest(t *testing.T, c *testClient, name string, body interface{}, cleanup bool) string {
	t.Helper()

	for attempt := 0; attempt < 2; attempt++ {
		repoID, limitReached := tryCreateLibrary(t, c, name, body)
		if repoID != "" {
			if cleanup {
				t.Cleanup(func() {
					resp := c.Delete(t, fmt.Sprintf("/api/v2.1/repos/%s/", repoID))
					if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound {
						resp.Body.Close()
						return
					}
					body := responseBody(t, resp)
					t.Errorf("cleanup delete library %s failed: status=%d body=%s", repoID, resp.StatusCode, body)
				})
			}
			return repoID
		}

		if !limitReached {
			break
		}

		deleted := cleanupTestLibrariesAcrossKnownUsers(t)
		if deleted == 0 {
			t.Fatalf("failed to create library %q: library limit reached and no stale test libraries were available for cleanup", name)
		}
		t.Logf("cleaned up %d stale test libraries after hitting the library limit while creating %q", deleted, name)
	}

	t.Fatalf("failed to create library %q after retrying", name)
	return ""
}

func tryCreateLibrary(t *testing.T, c *testClient, name string, body interface{}) (string, bool) {
	t.Helper()

	resp := c.PostJSON(t, "/api/v2.1/repos/", body)
	var result map[string]interface{}
	decodeJSON(t, resp, &result)

	if resp.StatusCode != http.StatusOK {
		if isLibraryLimitResponse(resp.StatusCode, result) {
			return "", true
		}
		t.Fatalf("create library %q failed: status=%d body=%v", name, resp.StatusCode, result)
	}

	repoID, ok := result["repo_id"].(string)
	if !ok || repoID == "" {
		t.Fatalf("failed to get repo_id from create library response for %q: %v", name, result)
	}

	return repoID, false
}

func isLibraryLimitResponse(statusCode int, body map[string]interface{}) bool {
	if statusCode != http.StatusForbidden {
		return false
	}
	errMsg, _ := body["error"].(string)
	return errMsg == "Library limit reached"
}

func cleanupTestLibrariesAcrossKnownUsers(t *testing.T) int {
	t.Helper()

	clients := []*testClient{adminClient, userClient, superadminClient}
	seen := map[*testClient]struct{}{}
	total := 0
	for _, client := range clients {
		if client == nil {
			continue
		}
		if _, ok := seen[client]; ok {
			continue
		}
		seen[client] = struct{}{}
		total += cleanupOwnedEphemeralLibraries(t, client)
	}
	return total
}

func cleanupOwnedEphemeralLibraries(t *testing.T, c *testClient) int {
	t.Helper()

	resp := c.Get(t, "/api/v2.1/repos/?type=mine")
	if resp.StatusCode != http.StatusOK {
		body := responseBody(t, resp)
		t.Logf("skipping stale library cleanup for %s: list status=%d body=%s", c.token, resp.StatusCode, body)
		return 0
	}

	var result map[string]interface{}
	decodeJSON(t, resp, &result)

	repos, ok := result["repos"].([]interface{})
	if !ok {
		return 0
	}

	deleted := 0
	for _, entry := range repos {
		repo, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}

		repoID, _ := repo["repo_id"].(string)
		if repoID == "" {
			repoID, _ = repo["id"].(string)
		}
		repoName, _ := repo["repo_name"].(string)
		if repoName == "" {
			repoName, _ = repo["name"].(string)
		}
		if repoID == "" || !isEphemeralLibraryName(repoName) {
			continue
		}

		delResp := c.Delete(t, fmt.Sprintf("/api/v2.1/repos/%s/", repoID))
		if delResp.StatusCode == http.StatusOK || delResp.StatusCode == http.StatusNotFound {
			deleted++
		} else {
			body := responseBody(t, delResp)
			t.Logf("failed to delete stale test library %q (%s): status=%d body=%s", repoName, repoID, delResp.StatusCode, body)
			continue
		}
		delResp.Body.Close()
	}

	return deleted
}

func isEphemeralLibraryName(name string) bool {
	for _, prefix := range ephemeralLibraryPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// decodeJSON decodes a response body into v and closes the body.
func decodeJSON(t *testing.T, resp *http.Response, v interface{}) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatalf("failed to decode JSON response: %v", err)
	}
}

// expectStatus asserts the response has the expected status code.
func expectStatus(t *testing.T, resp *http.Response, expected int) {
	t.Helper()
	if resp.StatusCode != expected {
		t.Errorf("expected status %d, got %d", expected, resp.StatusCode)
	}
}
