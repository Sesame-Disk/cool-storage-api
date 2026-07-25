//go:build integration

package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestShareLinkBootstrapPasswordGateOnBothEndpoints is the behavioural proof for
// ISSUE-SHARELINK-PASSWORD-BYPASS-01, driven through the REAL public endpoints
// against a real cluster.
//
// Why it exists on top of the unit tests: those substitute the block read and
// call the shared emitter directly, and the AST check is syntactic — it walks the
// package's call graph but never executes a handler, so it cannot see a gate that
// is present in source and ineffective at runtime. The bypass was an anonymous
// HTTP request returning a 200 whose body carried the file, so the only assertion
// that truly closes it is an anonymous HTTP request whose body does not.
//
// It asserts on the BODY, never on the status: the vulnerable response was a
// perfectly ordinary 200. A status-only test passes against the bug.
func TestShareLinkBootstrapPasswordGateOnBothEndpoints(t *testing.T) {
	const (
		password = "correct horse battery staple"
		secret   = "SECRET-MARKDOWN-BODY-DO-NOT-LEAK"
	)

	name := fmt.Sprintf("inttest-sharelink-pwgate-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)

	uploadURL := getUploadLink(t, adminClient, repoID, "/")
	uploadFileThroughLink(t, adminClient, uploadURL, "notes.md", "/", secret+"\n")

	// Endpoint 1 takes a file link; endpoint 2 only exists under a directory link
	// resolved through ?p=. Both are password-protected with the same password.
	fileToken := createPasswordShareLinkForTest(t, adminClient, repoID, "/notes.md", password)
	dirToken := createPasswordShareLinkForTest(t, adminClient, repoID, "/", password)

	surfaces := []struct {
		name string
		get  func(t *testing.T, jar http.CookieJar) (int, string)
	}{
		{
			name: "GET /share-links/:token/bootstrap",
			get: func(t *testing.T, jar http.CookieJar) (int, string) {
				return getAnonymousBootstrap(t, jar,
					adminClient.baseURL+"/api/v2.1/share-links/"+fileToken+"/bootstrap/")
			},
		},
		{
			name: "GET /share-links/:token/files/bootstrap",
			get: func(t *testing.T, jar http.CookieJar) (int, string) {
				return getAnonymousBootstrap(t, jar, fmt.Sprintf(
					"%s/api/v2.1/share-links/%s/files/bootstrap/?p=%s",
					adminClient.baseURL, dirToken, url.QueryEscape("/notes.md")))
			},
		},
	}

	for _, s := range surfaces {
		t.Run(s.name, func(t *testing.T) {
			// 1. Anonymous, no password cookie. This is the exploit.
			status, body := s.get(t, nil)
			if status != http.StatusOK {
				t.Fatalf("status = %d, want 200 (the password page is a 200 with no content): %s", status, body)
			}
			assertBootstrapWithholdsProtectedPayload(t, body, secret)

			// 2. Same surface after the real password exchange. Proves the gate
			//    withholds rather than breaks the feature — a gate that always
			//    denied would pass step 1 and is the obvious wrong fix.
			token := fileToken
			if strings.Contains(s.name, "files/bootstrap") {
				token = dirToken
			}
			jar := verifyShareLinkPasswordForTest(t, token, password)

			status, body = s.get(t, jar)
			if status != http.StatusOK {
				t.Fatalf("verified status = %d, want 200: %s", status, body)
			}
			if !strings.Contains(body, secret) {
				t.Fatalf("verified bootstrap did not carry the file content; the gate withholds too much: %s", body)
			}
		})
	}
}

// assertBootstrapWithholdsProtectedPayload checks the two things the bypass
// leaked, plus the flag that must survive so the SPA still prompts.
func assertBootstrapWithholdsProtectedPayload(t *testing.T, body, secret string) {
	t.Helper()

	if strings.Contains(body, secret) {
		t.Fatalf("anonymous bootstrap leaked the protected file content: %s", body)
	}

	var decoded struct {
		PageOptions struct {
			FileContent      string          `json:"fileContent"`
			NeedPassword     bool            `json:"needPassword"`
			NoPassword       bool            `json:"noPassword"`
			OnlyOfficeConfig json.RawMessage `json:"onlyOfficeConfig"`
		} `json:"page_options"`
	}
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decode bootstrap body: %v (body=%s)", err, body)
	}

	if decoded.PageOptions.FileContent != "" {
		t.Fatalf("fileContent is non-empty for an unverified password link: %q", decoded.PageOptions.FileContent)
	}
	if len(decoded.PageOptions.OnlyOfficeConfig) != 0 {
		t.Fatalf("onlyOfficeConfig present for an unverified password link: %s", decoded.PageOptions.OnlyOfficeConfig)
	}
	if !decoded.PageOptions.NeedPassword {
		t.Fatal("needPassword must stay true, or the SPA stops showing the password dialog")
	}
	if decoded.PageOptions.NoPassword {
		t.Fatal("noPassword must be false while the password is unverified")
	}
}

// verifyShareLinkPasswordForTest performs the real password exchange and returns
// a jar holding the cookie the server set, exactly as the browser does before
// SharedLinkPasswordDialog reloads the page.
func verifyShareLinkPasswordForTest(t *testing.T, token, password string) http.CookieJar {
	t.Helper()

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("new cookie jar: %v", err)
	}
	client := &http.Client{Jar: jar, Timeout: 30 * time.Second}

	payload, marshalErr := json.Marshal(map[string]string{"password": password})
	if marshalErr != nil {
		t.Fatalf("marshal password payload: %v", marshalErr)
	}
	req, err := http.NewRequest(http.MethodPost,
		adminClient.baseURL+"/api/v2.1/public-links/"+token+"/check-password", strings.NewReader(string(payload)))
	if err != nil {
		t.Fatalf("build check-password request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("check-password request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("check-password status = %d, want 200: %s", resp.StatusCode, responseBody(t, resp))
	}
	if len(resp.Cookies()) == 0 {
		t.Fatal("check-password set no cookie; the verified case below would be meaningless")
	}
	return jar
}

// getAnonymousBootstrap issues the request with NO auth header. The bypass is an
// anonymous-caller problem, so borrowing adminClient's credentials would prove
// nothing.
func getAnonymousBootstrap(t *testing.T, jar http.CookieJar, requestURL string) (int, string) {
	t.Helper()

	client := &http.Client{Timeout: 30 * time.Second}
	if jar != nil {
		client.Jar = jar
	}
	req, err := http.NewRequest(http.MethodGet, requestURL, nil)
	if err != nil {
		t.Fatalf("build bootstrap request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("bootstrap request failed: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode, responseBody(t, resp)
}

func createPasswordShareLinkForTest(t *testing.T, client *testClient, repoID, path, password string) string {
	t.Helper()

	resp := client.PostJSON(t, "/api/v2.1/share-links/", map[string]interface{}{
		"repo_id":     repoID,
		"path":        path,
		"password":    password,
		"permissions": "preview_download",
	})
	if resp.StatusCode == http.StatusForbidden {
		payload := responseJSON(t, resp)
		if payload["error"] == "Share link limit reached" {
			deleteFirstOrgShareLinkForTest(t, client)
			resp = client.PostJSON(t, "/api/v2.1/share-links/", map[string]interface{}{
				"repo_id":     repoID,
				"path":        path,
				"password":    password,
				"permissions": "preview_download",
			})
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
		t.Errorf("cleanup delete share link %s failed: status=%d body=%s", token, resp.StatusCode, responseBody(t, resp))
	})
	return token
}
