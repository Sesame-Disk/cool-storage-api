//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestShareLinkBootstrapFailsClosedOnTheRealEndpoint drives the REAL public
// endpoint end to end. Unit coverage stubs the block read and calls the shared
// emitter directly, so it cannot prove that GetShareLinkBootstrap actually
// routes its failures through that emitter — an endpoint that went back to
// `c.JSON(status, gin.H{"error": err.Error()})` would pass every unit test while
// leaking internals to anonymous visitors.
//
// The failure is forced by pointing the file's fs_object at a block that has no
// canonical metadata, which is deterministic and needs no fault injection: the
// inline text read fails, and the bootstrap must answer a retryable 503 with a
// generic body rather than a 200 carrying an empty document.
func TestShareLinkBootstrapFailsClosedOnTheRealEndpoint(t *testing.T) {
	name := fmt.Sprintf("inttest-sharelink-bootstrap-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)
	database := shareProjectionDBForTest(t)

	uploadURL := getUploadLink(t, adminClient, repoID, "/")
	uploadFileThroughLink(t, adminClient, uploadURL, "notes.md", "/", "# heading\n\nbody text\n")

	token := createShareLinkForTest(t, adminClient, repoID, "/notes.md")

	// Sanity: the healthy bootstrap answers 200 and inlines the text. Without
	// this the corrupted assertion below could pass for the wrong reason.
	if status, body := getShareBootstrap(t, token); status != http.StatusOK {
		t.Fatalf("healthy bootstrap status = %d, want 200: %s", status, body)
	}

	// Point the file at a block id that exists nowhere, so the inline text read
	// fails inside readFileContentAsText.
	rootFSID := downloadTestRootFSID(t, database, repoID)
	fileFSID := downloadTestDirEntryID(t, database, repoID, rootFSID, "notes.md")
	const missingBlock = "5f3a9b2c8d1e4f6a7b9c0d2e3f4a5b6c7d8e9f0a1b2c3d4e5f6a7b8c9d0e1f2a"
	if err := database.Session().Query(`
		UPDATE fs_objects SET block_ids = ? WHERE library_id = ? AND fs_id = ?
	`, []string{missingBlock}, repoID, fileFSID).Exec(); err != nil {
		t.Fatalf("point file at a missing block: %v", err)
	}

	status, body := getShareBootstrap(t, token)

	if status == http.StatusOK {
		t.Fatalf("a failed inline read answered 200; the document would render as empty: %s", body)
	}
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 for a transient inline-read failure: %s", status, body)
	}
	lowered := strings.ToLower(body)
	for _, leaked := range []string{missingBlock, "block", "bucket", "cassandra", "gocql", "s3"} {
		if strings.Contains(lowered, strings.ToLower(leaked)) {
			t.Fatalf("public bootstrap body leaked %q: %s", leaked, body)
		}
	}
}

func getShareBootstrap(t *testing.T, token string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, adminClient.baseURL+"/api/v2.1/share-links/"+token+"/bootstrap/", nil)
	if err != nil {
		t.Fatalf("build bootstrap request: %v", err)
	}
	resp, err := adminClient.http.Do(req)
	if err != nil {
		t.Fatalf("bootstrap request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusServiceUnavailable && resp.Header.Get("Retry-After") == "" {
		t.Fatal("a retryable bootstrap failure must carry Retry-After")
	}
	return resp.StatusCode, responseBody(t, resp)
}
