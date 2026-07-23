//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"net/url"
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
	assertShareBootstrapBodyIsPublic(t, body, missingBlock)
}

// TestShareLinkFileBootstrapFailsClosedOnTheRealEndpoint covers the SECOND public
// bootstrap endpoint. Both endpoints delegate to emitShareFileBootstrap today, but
// nothing pinned that for this one: the sibling test above drives only
// /bootstrap/, and the AST test only proves no OTHER method calls
// respondShareBootstrapError. So a GetShareLinkFileBootstrap that went back to
//
//	bootstrap, status, err := h.buildShareFileBootstrapResponse(c, sl)
//	if err != nil { c.JSON(status, gin.H{"error": err.Error()}) ; return }
//
// would keep every existing test green while leaking internal block ids, bucket
// names and Cassandra/S3 detail to anonymous visitors. This is the behavioural
// regression that closes that hole.
//
// It differs from the sibling in the shape of the link, not just the URL: this
// one is a DIRECTORY share link resolved down to a file through ?p=, which is
// the only way to reach this handler.
func TestShareLinkFileBootstrapFailsClosedOnTheRealEndpoint(t *testing.T) {
	name := fmt.Sprintf("inttest-sharelink-file-bootstrap-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)
	database := shareProjectionDBForTest(t)

	uploadURL := getUploadLink(t, adminClient, repoID, "/")
	uploadFileThroughLink(t, adminClient, uploadURL, "notes.md", "/", "# heading\n\nbody text\n")

	// A directory share link: this endpoint exists to resolve ?p= underneath one.
	token := createShareLinkForTest(t, adminClient, repoID, "/")

	// Sanity first, so the assertion below cannot pass because the link, the
	// path resolution or the endpoint were broken to begin with.
	if status, body := getShareFileBootstrap(t, token, "/notes.md"); status != http.StatusOK {
		t.Fatalf("healthy file bootstrap status = %d, want 200: %s", status, body)
	}

	rootFSID := downloadTestRootFSID(t, database, repoID)
	fileFSID := downloadTestDirEntryID(t, database, repoID, rootFSID, "notes.md")
	const missingBlock = "9c8b7a6d5e4f3a2b1c0d9e8f7a6b5c4d3e2f1a0b9c8d7e6f5a4b3c2d1e0f9a8b"
	if err := database.Session().Query(`
		UPDATE fs_objects SET block_ids = ? WHERE library_id = ? AND fs_id = ?
	`, []string{missingBlock}, repoID, fileFSID).Exec(); err != nil {
		t.Fatalf("point file at a missing block: %v", err)
	}

	status, body := getShareFileBootstrap(t, token, "/notes.md")

	if status == http.StatusOK {
		t.Fatalf("a failed inline read answered 200; the document would render as empty: %s", body)
	}
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 for a transient inline-read failure: %s", status, body)
	}
	assertShareBootstrapBodyIsPublic(t, body, missingBlock)
}

// assertShareBootstrapBodyIsPublic rejects any internal detail the read path's
// wrapped errors carry. Both bootstrap endpoints are anonymous surfaces.
func assertShareBootstrapBodyIsPublic(t *testing.T, body, secretBlockID string) {
	t.Helper()
	lowered := strings.ToLower(body)
	for _, leaked := range []string{secretBlockID, "block", "bucket", "cassandra", "gocql", "s3"} {
		if strings.Contains(lowered, strings.ToLower(leaked)) {
			t.Fatalf("public bootstrap body leaked %q: %s", leaked, body)
		}
	}
}

func getShareBootstrap(t *testing.T, token string) (int, string) {
	t.Helper()
	return getShareBootstrapURL(t, adminClient.baseURL+"/api/v2.1/share-links/"+token+"/bootstrap/")
}

// getShareFileBootstrap drives GET /api/v2.1/share-links/:token/files/bootstrap/?p=…,
// the endpoint a directory share link uses to open one file inside it.
func getShareFileBootstrap(t *testing.T, token, subPath string) (int, string) {
	t.Helper()
	return getShareBootstrapURL(t, fmt.Sprintf("%s/api/v2.1/share-links/%s/files/bootstrap/?p=%s",
		adminClient.baseURL, token, url.QueryEscape(subPath)))
}

func getShareBootstrapURL(t *testing.T, requestURL string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, requestURL, nil)
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
