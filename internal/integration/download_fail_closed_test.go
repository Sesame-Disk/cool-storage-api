//go:build integration

package integration

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
)

// downloadTokenURL mints a real seafhttp download URL for a path.
func downloadTokenURL(t *testing.T, repoID, path string) string {
	t.Helper()
	resp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/file/?p=%s", repoID, path))
	expectStatus(t, resp, http.StatusOK)
	url := strings.Trim(responseBody(t, resp), "\" \n\r")
	if !strings.Contains(url, "/seafhttp/files/") {
		t.Fatalf("unexpected download URL format: %s", url)
	}
	return url
}

// getDownload drives the production seafhttp download endpoint.
func getDownload(t *testing.T, url string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("build download request: %v", err)
	}
	resp, err := adminClient.http.Do(req)
	if err != nil {
		t.Fatalf("download request failed: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read download body: %v", err)
	}
	return resp.StatusCode, string(body)
}

func downloadTestOrgID(t *testing.T, database *dbpkg.DB, repoID string) string {
	t.Helper()
	var orgID string
	if err := database.Session().Query(`SELECT org_id FROM libraries_by_id WHERE library_id = ?`, repoID).Scan(&orgID); err != nil {
		t.Fatalf("resolve library org: %v", err)
	}
	return orgID
}

// downloadTestRootFSID re-reads the CURRENT root listing. Every mutation below
// publishes a new commit, so this must not be cached across subtests.
func downloadTestRootFSID(t *testing.T, database *dbpkg.DB, repoID string) string {
	t.Helper()
	var headCommit string
	if err := database.Session().Query(`
		SELECT head_commit_id FROM libraries WHERE org_id = ? AND library_id = ?
	`, downloadTestOrgID(t, database, repoID), repoID).Scan(&headCommit); err != nil {
		t.Fatalf("read head commit: %v", err)
	}
	var rootFSID string
	if err := database.Session().Query(`
		SELECT root_fs_id FROM commits WHERE library_id = ? AND commit_id = ?
	`, repoID, headCommit).Scan(&rootFSID); err != nil {
		t.Fatalf("read root fs id: %v", err)
	}
	return rootFSID
}

func downloadTestDirEntryID(t *testing.T, database *dbpkg.DB, repoID, dirFSID, entryName string) string {
	t.Helper()
	var rawEntries string
	if err := database.Session().Query(`
		SELECT dir_entries FROM fs_objects WHERE library_id = ? AND fs_id = ?
	`, repoID, dirFSID).Scan(&rawEntries); err != nil {
		t.Fatalf("read directory %s: %v", dirFSID, err)
	}
	var entries []map[string]interface{}
	if err := json.Unmarshal([]byte(rawEntries), &entries); err != nil {
		t.Fatalf("parse directory %s: %v", dirFSID, err)
	}
	for _, entry := range entries {
		if name, _ := entry["name"].(string); name == entryName {
			id, _ := entry["id"].(string)
			if id == "" {
				t.Fatalf("entry %q in %s has no id", entryName, dirFSID)
			}
			return id
		}
	}
	t.Fatalf("entry %q not found in directory %s", entryName, dirFSID)
	return ""
}

// TestDownloadFailClosedContract drives the four end-to-end cases of the PR-6
// fail-closed contract against real Cassandra through the production endpoint.
//
// A 404 tells a sync client the file is gone and that it may drop its local copy.
// LOCAL_QUORUM cannot prove global absence because a valid listing may be an older
// cross-DC snapshot, so absence, dangling metadata and corruption all answer 503.
func TestDownloadFailClosedContract(t *testing.T) {
	name := fmt.Sprintf("inttest-download-failclosed-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)
	database := shareProjectionDBForTest(t)

	const presentContent = "download fail-closed contract payload"
	uploadURL := getUploadLink(t, adminClient, repoID, "/")
	uploadFileThroughLink(t, adminClient, uploadURL, "present.txt", "/", presentContent)
	uploadFileThroughLink(t, adminClient, uploadURL, "victim.txt", "/", "victim payload")
	uploadFileThroughLink(t, adminClient, uploadURL, "dangling.txt", "/", "dangling payload")
	uploadFileThroughLink(t, adminClient, uploadURL, "corrupt.txt", "/", "corrupt payload")
	var corruptFileFSID string

	t.Run("present file downloads", func(t *testing.T) {
		status, body := getDownload(t, downloadTokenURL(t, repoID, "/present.txt"))
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", status, body)
		}
		if body != presentContent {
			t.Fatalf("body = %q, want %q", body, presentContent)
		}
	})

	t.Run("deleted file remains retryable because local absence is not global proof", func(t *testing.T) {
		url := downloadTokenURL(t, repoID, "/victim.txt")

		resp := adminClient.Delete(t, fmt.Sprintf("/api2/repos/%s/file/?p=/victim.txt", repoID))
		resp.Body.Close()

		status, body := getDownload(t, url)
		if status != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503 because LOCAL_QUORUM absence may be stale (body=%s)", status, body)
		}
	})

	t.Run("dangling fs_object is 503, never 404", func(t *testing.T) {
		url := downloadTokenURL(t, repoID, "/dangling.txt")

		// The root listing still names dangling.txt, but the fs_object it points
		// at is gone — premature GC, a partial write or cross-DC lag. That is
		// unproven absence: the file may well still be there.
		rootFSID := downloadTestRootFSID(t, database, repoID)
		targetFSID := downloadTestDirEntryID(t, database, repoID, rootFSID, "dangling.txt")
		if err := database.Session().Query(`
			DELETE FROM fs_objects WHERE library_id = ? AND fs_id = ?
		`, repoID, targetFSID).Exec(); err != nil {
			t.Fatalf("delete referenced fs_object: %v", err)
		}

		status, body := getDownload(t, url)
		if status == http.StatusNotFound {
			t.Fatalf("dangling metadata answered 404; a sync client would delete a file that may still exist (body=%s)", body)
		}
		if status != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503 (body=%s)", status, body)
		}
	})

	t.Run("corrupt listing is 503, never 404", func(t *testing.T) {
		url := downloadTokenURL(t, repoID, "/corrupt.txt")

		// Structurally valid JSON but semantically corrupt: the entry the path
		// names carries a non-40-hex id, so the listing cannot be trusted to
		// prove anything — least of all absence.
		rootFSID := downloadTestRootFSID(t, database, repoID)
		corruptFileFSID = downloadTestDirEntryID(t, database, repoID, rootFSID, "corrupt.txt")
		if err := database.Session().Query(`
			UPDATE fs_objects SET dir_entries = ? WHERE library_id = ? AND fs_id = ?
		`, `[{"name":"corrupt.txt","id":"nothex"}]`, repoID, rootFSID).Exec(); err != nil {
			t.Fatalf("corrupt root listing: %v", err)
		}

		status, body := getDownload(t, url)
		if status == http.StatusNotFound {
			t.Fatalf("corrupt listing answered 404 (body=%s)", body)
		}
		if status != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503 (body=%s)", status, body)
		}
	})

	for _, tc := range []struct {
		name       string
		dirEntries string
	}{
		{
			name: "zip rejects traversal names with retryable 503",
			dirEntries: fmt.Sprintf(`[{"name":"../escape.txt","id":"%s","mode":33188}]`,
				corruptFileFSID),
		},
		{
			name: "zip rejects fractional mode with retryable 503",
			dirEntries: fmt.Sprintf(`[{"name":"corrupt.txt","id":"%s","mode":16384.5}]`,
				corruptFileFSID),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rootFSID := downloadTestRootFSID(t, database, repoID)
			if err := database.Session().Query(`
				UPDATE fs_objects SET dir_entries = ? WHERE library_id = ? AND fs_id = ?
			`, tc.dirEntries, repoID, rootFSID).Exec(); err != nil {
				t.Fatalf("corrupt root listing for zip: %v", err)
			}

			zipTaskResp := adminClient.PostJSON(t, fmt.Sprintf("/api/v2.1/repos/%s/zip-task/?p=/", repoID), map[string]string{})
			expectStatus(t, zipTaskResp, http.StatusOK)
			zipToken, _ := responseJSON(t, zipTaskResp)["zip_token"].(string)
			if zipToken == "" {
				t.Fatal("zip task returned no token")
			}

			zipResp := adminClient.Get(t, fmt.Sprintf("/seafhttp/zip/%s", zipToken))
			defer zipResp.Body.Close()
			if zipResp.StatusCode != http.StatusServiceUnavailable {
				t.Fatalf("zip status = %d, want 503 (body=%s)", zipResp.StatusCode, responseBody(t, zipResp))
			}
			if got := zipResp.Header.Get("Retry-After"); got == "" {
				t.Fatal("zip 503 must carry Retry-After")
			}
		})
	}
}
