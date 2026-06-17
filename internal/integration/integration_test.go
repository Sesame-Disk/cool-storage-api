//go:build integration

package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
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

// liveRepoIDs tracks repos created during the current test run that must not
// be deleted by the stale-library cleanup path.
var liveRepoIDs sync.Map

var ephemeralLibraryPrefixes = []string{
	"inttest-",
	"smoke-",
}

var ephemeralLibraryExactNames = []string{
	"sesamefs-public-smoke",
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

	cleanupIntegrationEphemeralLibraries("before")
	afterCleanupDone := false
	defer func() {
		if !afterCleanupDone {
			cleanupIntegrationEphemeralLibraries("after")
		}
	}()

	code := m.Run()
	afterCleanupDone = true
	cleanupIntegrationEphemeralLibraries("after")
	os.Exit(code)
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

	const maxAttempts = 5
	for attempt := 0; attempt < maxAttempts; attempt++ {
		repoID, limitReached := tryCreateLibrary(t, c, name, body)
		if repoID != "" {
			liveRepoIDs.Store(repoID, struct{}{})
			if cleanup {
				t.Cleanup(func() {
					liveRepoIDs.Delete(repoID)
					resp := c.Delete(t, fmt.Sprintf("/api/v2.1/repos/%s/", repoID))
					if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound {
						resp.Body.Close()
						return
					}
					body := responseBody(t, resp)
					t.Errorf("cleanup delete library %s failed: status=%d body=%s", repoID, resp.StatusCode, body)
				})
			} else {
				// Remove from live set when test ends so stale-cleanup can find it
				// if it was never deleted by the test itself.
				t.Cleanup(func() { liveRepoIDs.Delete(repoID) })
			}
			return repoID
		}

		if !limitReached {
			break
		}

		deleted := cleanupTestLibrariesAcrossKnownUsers(t)
		if deleted > 0 {
			t.Logf("cleaned up %d stale test libraries after hitting the library limit while creating %q", deleted, name)
		} else if attempt == maxAttempts-1 {
			t.Fatalf("failed to create library %q: library limit reached and no stale test libraries were available for cleanup", name)
		} else {
			t.Logf("library limit reached while creating %q but no stale test libraries were available; waiting for active-library projections to converge before retrying", name)
		}

		// Library creation enforcement reads the projection-backed active-library
		// count. After a previous test cleanup, that projection can lag briefly
		// behind the delete that already made the library disappear from the owned
		// repo list, so an immediate retry can still see the stale count. Give the
		// shared integration environment a short convergence window before retrying.
		time.Sleep(time.Duration(attempt+1) * 250 * time.Millisecond)
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

func cleanupIntegrationEphemeralLibraries(phase string) {
	clients := []*testClient{adminClient, userClient, superadminClient}
	seen := map[string]struct{}{}
	total := 0
	for _, client := range clients {
		if client == nil {
			continue
		}
		if _, ok := seen[client.token]; ok {
			continue
		}
		seen[client.token] = struct{}{}
		total += cleanupOwnedEphemeralLibrariesWithoutTesting(client)
	}
	if total > 0 {
		fmt.Printf("cleaned up %d stale integration test libraries %s test run\n", total, phase)
	}
}

func cleanupOwnedEphemeralLibrariesWithoutTesting(c *testClient) int {
	req, err := http.NewRequest(http.MethodGet, c.baseURL+"/api/v2.1/repos/?type=mine", nil)
	if err != nil {
		fmt.Printf("skipping stale library cleanup for %s: create list request: %v\n", c.token, err)
		return 0
	}
	req.Header.Set("Authorization", "Token "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		fmt.Printf("skipping stale library cleanup for %s: list request failed: %v\n", c.token, err)
		return 0
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Printf("skipping stale library cleanup for %s: list status=%d\n", c.token, resp.StatusCode)
		return 0
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		fmt.Printf("skipping stale library cleanup for %s: decode list response: %v\n", c.token, err)
		return 0
	}

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
		if _, live := liveRepoIDs.Load(repoID); live {
			continue
		}

		if deleteEphemeralLibrary(c, repoID, repoName) {
			deleted++
		}
	}

	return deleted
}

func deleteEphemeralLibrary(c *testClient, repoID, repoName string) bool {
	req, err := http.NewRequest(http.MethodDelete, c.baseURL+"/api/v2.1/repos/"+repoID+"/", nil)
	if err != nil {
		fmt.Printf("failed to create delete request for stale test library %q (%s): %v\n", repoName, repoID, err)
		return false
	}
	req.Header.Set("Authorization", "Token "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		fmt.Printf("failed to delete stale test library %q (%s): %v\n", repoName, repoID, err)
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound {
		return true
	}
	fmt.Printf("failed to delete stale test library %q (%s): status=%d\n", repoName, repoID, resp.StatusCode)
	return false
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
		if _, live := liveRepoIDs.Load(repoID); live {
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
	for _, exactName := range ephemeralLibraryExactNames {
		if name == exactName {
			return true
		}
	}
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
