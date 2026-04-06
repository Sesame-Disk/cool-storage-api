//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// testClient wraps an HTTP client with a base URL and auth token.
type testClient struct {
	baseURL string
	token   string
	http    *http.Client
}

func newTestClient(baseURL, token string) *testClient {
	return &testClient{
		baseURL: baseURL,
		token:   token,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// Do sends a request with the given method, path, and optional body.
// The caller is responsible for closing the response body.
func (c *testClient) Do(t *testing.T, method, path string, body *bytes.Buffer) *http.Response {
	t.Helper()

	urlStr := c.baseURL + path
	var req *http.Request
	var err error

	if body != nil {
		req, err = http.NewRequest(method, urlStr, body)
	} else {
		req, err = http.NewRequest(method, urlStr, nil)
	}
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	req.Header.Set("Authorization", "Token "+c.token)

	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatalf("%s %s failed: %v", method, path, err)
	}
	return resp
}

// Get sends a GET request and returns the response.
func (c *testClient) Get(t *testing.T, path string) *http.Response {
	t.Helper()
	return c.Do(t, http.MethodGet, path, nil)
}

func (c *testClient) GetWithHost(t *testing.T, path string, host string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("Authorization", "Token "+c.token)
	if host != "" {
		req.Host = host
	}

	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatalf("GET %s failed: %v", path, err)
	}
	return resp
}

// PostJSON sends a POST request with a JSON body.
func (c *testClient) PostJSON(t *testing.T, path string, body interface{}) *http.Response {
	t.Helper()

	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("failed to marshal JSON body: %v", err)
	}

	buf := bytes.NewBuffer(data)
	req, err := http.NewRequest(http.MethodPost, c.baseURL+path, buf)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatalf("POST %s failed: %v", path, err)
	}
	return resp
}

func (c *testClient) PostJSONWithHost(t *testing.T, path string, body interface{}, host string) *http.Response {
	t.Helper()

	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("failed to marshal JSON body: %v", err)
	}

	buf := bytes.NewBuffer(data)
	req, err := http.NewRequest(http.MethodPost, c.baseURL+path, buf)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Content-Type", "application/json")
	if host != "" {
		req.Host = host
	}

	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatalf("POST %s failed: %v", path, err)
	}
	return resp
}

// PostForm sends a POST request with form-encoded body.
func (c *testClient) PostForm(t *testing.T, path string, values url.Values) *http.Response {
	t.Helper()

	buf := bytes.NewBufferString(values.Encode())
	req, err := http.NewRequest(http.MethodPost, c.baseURL+path, buf)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatalf("POST %s failed: %v", path, err)
	}
	return resp
}

// PutJSON sends a PUT request with a JSON body.
func (c *testClient) PutJSON(t *testing.T, path string, body interface{}) *http.Response {
	t.Helper()

	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("failed to marshal JSON body: %v", err)
	}

	buf := bytes.NewBuffer(data)
	req, err := http.NewRequest(http.MethodPut, c.baseURL+path, buf)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatalf("PUT %s failed: %v", path, err)
	}
	return resp
}

// PutForm sends a PUT request with form-encoded body.
func (c *testClient) PutForm(t *testing.T, path string, values url.Values) *http.Response {
	t.Helper()

	buf := bytes.NewBufferString(values.Encode())
	req, err := http.NewRequest(http.MethodPut, c.baseURL+path, buf)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatalf("PUT %s failed: %v", path, err)
	}
	return resp
}

// Delete sends a DELETE request.
func (c *testClient) Delete(t *testing.T, path string) *http.Response {
	t.Helper()
	return c.Do(t, http.MethodDelete, path, nil)
}

// responseBody reads and returns the response body as a string, closing the body.
func responseBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}
	return buf.String()
}

// responseJSON decodes the response body into a map and closes the body.
func responseJSON(t *testing.T, resp *http.Response) map[string]interface{} {
	t.Helper()
	var result map[string]interface{}
	decodeJSON(t, resp, &result)
	return result
}

// containsEntry checks if a JSON array response contains an entry with
// the given key matching the expected value.
func containsEntry(entries []interface{}, key string, expected string) bool {
	for _, entry := range entries {
		m, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		if v, ok := m[key]; ok {
			if str, ok := v.(string); ok && strings.EqualFold(str, expected) {
				return true
			}
		}
	}
	return false
}
