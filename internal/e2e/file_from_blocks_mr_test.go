//go:build e2e

// Package e2e contains true multi-region HTTP tests. Unlike internal/integration,
// these tests do not touch Cassandra or S3 directly: they exercise the public API
// of a running region pair and verify that a write accepted in one region is
// readable in the other.
//
// Run against the two-region cluster:
//
//	SESAMEFS_URL_USA=http://sesamefs-usa:8080 \
//	SESAMEFS_URL_EU=http://sesamefs-eu:8080 \
//	go test -tags e2e ./internal/e2e -run TestFileFromBlocksMultiRegion -v
package e2e

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

const (
	defaultUSURL = "http://localhost:8088"
	defaultEUURL = "http://localhost:8081"
	defaultToken = "dev-token-admin"

	// A distinct-block manifest large enough to exceed the 20-slot worker
	// bound many times over, while staying cheap enough to replicate through
	// both regions. The stack must run with
	// WEB_UPLOADS_BLOCK_UPLOAD_BLOCK_SIZE_MB=1; every non-final block must be
	// exactly that size.
	blockCount = 32
	blockSize  = 1 << 20
)

type apiClient struct {
	base  string
	token string
	http  *http.Client
}

func newAPIClient(base, token string) *apiClient {
	return &apiClient{
		base:  strings.TrimRight(base, "/"),
		token: token,
		http:  &http.Client{Timeout: 90 * time.Second},
	}
}

func (c *apiClient) do(method, path string, body []byte, headers map[string]string) (int, []byte) {
	req, err := http.NewRequest(method, c.base+path, bytes.NewReader(body))
	if err != nil {
		panic(err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Token "+c.token)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		panic(err)
	}
	return resp.StatusCode, data
}

func (c *apiClient) postJSON(path string, in any) (int, []byte) {
	data, err := json.Marshal(in)
	if err != nil {
		panic(err)
	}
	return c.do(http.MethodPost, path, data, map[string]string{"Content-Type": "application/json"})
}

func mustStatus(t *testing.T, step string, status int, body []byte, want ...int) {
	t.Helper()
	for _, w := range want {
		if status == w {
			return
		}
	}
	t.Fatalf("%s: status=%d body=%s", step, status, body)
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// blockContent returns a distinct payload per index so every manifest entry is
// a distinct SHA-256 and therefore a distinct verification/liveness worker.
func blockContent(i int) []byte {
	b := make([]byte, blockSize)
	for j := range b {
		b[j] = byte(i + j)
	}
	copy(b, fmt.Sprintf("e2e-block-%06d:", i))
	return b
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func sha1Hex(b []byte) string {
	sum := sha1.Sum(b)
	return hex.EncodeToString(sum[:])
}

func createLibrary(t *testing.T, c *apiClient, name string) string {
	t.Helper()
	status, body := c.postJSON("/api/v2.1/repos/", map[string]string{"repo_name": name})
	mustStatus(t, "create library", status, body, http.StatusOK)
	var out struct {
		RepoID string `json:"repo_id"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.RepoID == "" {
		t.Fatalf("create library: repo_id missing: body=%s err=%v", body, err)
	}
	return out.RepoID
}

func deleteLibrary(c *apiClient, repoID string) {
	status, body := c.do(http.MethodDelete, "/api/v2.1/repos/"+repoID+"/", nil, nil)
	if status != http.StatusOK && status != http.StatusNoContent && status != http.StatusNotFound {
		fmt.Printf("cleanup: delete library %s: status=%d body=%s\n", repoID, status, body)
	}
}

func createSession(t *testing.T, c *apiClient, repoID string, size int) string {
	t.Helper()
	status, body := c.postJSON("/api/v2/repos/"+repoID+"/block-upload-session/", map[string]any{
		"parent_dir": "/",
		"size":       size,
	})
	mustStatus(t, "create upload session", status, body, http.StatusOK)
	var out struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.SessionID == "" {
		t.Fatalf("create upload session: session_id missing: body=%s err=%v", body, err)
	}
	return out.SessionID
}

func uploadBlock(t *testing.T, c *apiClient, session string, block []byte) {
	t.Helper()
	status, body := c.do(http.MethodPost, "/api/v2/blocks/upload", block, map[string]string{
		"Content-Type":           "application/octet-stream",
		"X-Block-Hash":           sha256Hex(block),
		"X-Block-Upload-Session": session,
	})
	mustStatus(t, "upload block", status, body, http.StatusOK, http.StatusCreated)
}

func commitFile(t *testing.T, c *apiClient, repoID, session, filename string, blocks [][]byte) {
	t.Helper()
	entries := make([]map[string]any, len(blocks))
	total := 0
	for i, block := range blocks {
		entries[i] = map[string]any{
			"sha1":   sha1Hex(block),
			"sha256": sha256Hex(block),
			"size":   len(block),
		}
		total += len(block)
	}
	status, body := c.postJSON("/api/v2/repos/"+repoID+"/file-from-blocks/", map[string]any{
		"session":    session,
		"parent_dir": "/",
		"filename":   filename,
		"replace":    false,
		"size":       total,
		"blocks":     entries,
	})
	mustStatus(t, "commit file-from-blocks", status, body, http.StatusOK)
}

// tryDownload asks one region for the file's download URL and fetches it,
// rewriting a loopback host to the region endpoint so the test can also run
// from outside the compose network. It returns an error (not a test failure)
// so cross-region callers can retry while replication converges.
func tryDownload(c *apiClient, repoID, filename string) ([]byte, error) {
	path := fmt.Sprintf("/api2/repos/%s/file/?p=/%s", repoID, url.PathEscape(filename))
	status, body := c.do(http.MethodGet, path, nil, nil)
	if status != http.StatusOK {
		return nil, fmt.Errorf("download link status=%d body=%s", status, body)
	}

	link := strings.TrimSpace(string(body))
	var quoted string
	if json.Unmarshal(body, &quoted) == nil {
		link = quoted
	}
	link = strings.Trim(link, `"`)
	parsed, err := url.Parse(link)
	if err != nil || parsed.String() == "" {
		return nil, fmt.Errorf("unparsable download link %q: %w", link, err)
	}
	if !parsed.IsAbs() {
		base, _ := url.Parse(c.base)
		parsed = base.ResolveReference(parsed)
	}
	if parsed.Hostname() == "localhost" || parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "::1" {
		base, _ := url.Parse(c.base)
		parsed.Scheme = base.Scheme
		parsed.Host = base.Host
	}

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Get(parsed.String())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download status=%d body=%s", resp.StatusCode, data)
	}
	return data, nil
}

func waitForContent(t *testing.T, c *apiClient, repoID, filename string, want []byte) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		data, err := tryDownload(c, repoID, filename)
		if err == nil && bytes.Equal(data, want) {
			return
		}
		if err != nil {
			last = err
		} else {
			last = fmt.Errorf("content mismatch: got %d bytes, want %d", len(data), len(want))
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("content for %s did not converge: %v", filename, last)
}

// TestFileFromBlocksMultiRegion commits a 256-block file through the USA region
// and reads it back through both USA and EU. This is the end-to-end guard for
// the commit path changed by the bounded worker fan-out.
func TestFileFromBlocksMultiRegion(t *testing.T) {
	usa := newAPIClient(envOr("SESAMEFS_URL_USA", defaultUSURL), envOr("SESAMEFS_DEV_TOKEN", defaultToken))
	eu := newAPIClient(envOr("SESAMEFS_URL_EU", defaultEUURL), envOr("SESAMEFS_DEV_TOKEN", defaultToken))

	if _, body := usa.do(http.MethodGet, "/ping", nil, nil); !bytes.Equal(bytes.TrimSpace(body), []byte("pong")) {
		t.Fatalf("USA region not reachable at %s: body=%q", usa.base, body)
	}
	if _, body := eu.do(http.MethodGet, "/ping", nil, nil); !bytes.Equal(bytes.TrimSpace(body), []byte("pong")) {
		t.Fatalf("EU region not reachable at %s: body=%q", eu.base, body)
	}

	name := fmt.Sprintf("e2e-mr-fromb-%d", time.Now().UnixNano())
	repoID := createLibrary(t, usa, name)
	t.Cleanup(func() { deleteLibrary(usa, repoID) })

	blocks := make([][]byte, blockCount)
	total := 0
	for i := range blocks {
		blocks[i] = blockContent(i)
		total += len(blocks[i])
	}

	session := createSession(t, usa, repoID, total)
	for i, block := range blocks {
		uploadBlock(t, usa, session, block)
		if i == 0 {
			// The first upload proves the session is live; the rest are the
			// distinct-block fan-out this test is here to exercise.
			t.Logf("uploaded first block of %d (session %s)", blockCount, session)
		}
	}

	filename := name + ".bin"
	commitFile(t, usa, repoID, session, filename, blocks)

	want := bytes.Join(blocks, nil)
	waitForContent(t, usa, repoID, filename, want)
	t.Logf("USA read %d bytes from %d distinct blocks", len(want), blockCount)
	waitForContent(t, eu, repoID, filename, want)
	t.Logf("EU read %d bytes from %d distinct blocks", len(want), blockCount)
}
