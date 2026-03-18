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
	t.Helper()

	body := map[string]string{"repo_name": name}
	resp := c.PostJSON(t, "/api/v2.1/repos/", body)
	expectStatus(t, resp, http.StatusOK)

	var result map[string]interface{}
	decodeJSON(t, resp, &result)

	repoID, ok := result["repo_id"].(string)
	if !ok || repoID == "" {
		t.Fatalf("failed to get repo_id from create library response: %v", result)
	}

	t.Cleanup(func() {
		delResp := c.Delete(t, fmt.Sprintf("/api/v2.1/repos/%s/", repoID))
		delResp.Body.Close()
	})

	return repoID
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
