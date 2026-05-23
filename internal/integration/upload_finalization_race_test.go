//go:build integration

package integration

// Tests in this file target the two upload-finalization paths that are known to
// have a HEAD-snapshot inconsistency:
//
//  1. seafhttp HandleUpload → commitUploadedFile / commitUploadedFileOnce
//  2. seafhttp finalizeUploadStreaming → commitUploadedFileMultiBlock / commitUploadedFileMultiBlockOnce
//  3. v2 files.go → finalizeStoredUploadMetadata / finalizeStoredUploadMetadataOnce
//
// The common bug: within a single commit attempt, head_commit_id and the root
// tree are read in separate Cassandra queries instead of from one consistent
// snapshot. If another upload commits between those reads the wrong HEAD gets
// used for the CAS compare, so either the CAS spuriously fails (retries more
// than necessary) or — in the worst case — it compares against a HEAD that does
// not match the tree it just built and silently discards the intermediate commit.
//
// A correct fix snapshot-gates all reads (head_commit_id + root_fs_id) in a
// single GetLibraryHeadSnapshot call before any tree traversal begins.
//
// Each test is designed to be a reliable regression test that FAILS with the
// current code and PASSES after the fix is applied.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func getUploadLink(t *testing.T, c *testClient, repoID, parentDir string) string {
	t.Helper()
	resp := c.Get(t, fmt.Sprintf("/api2/repos/%s/upload-link/?p=%s", repoID, url.QueryEscape(parentDir)))
	expectStatus(t, resp, http.StatusOK)
	return strings.Trim(responseBody(t, resp), "\" \n\r")
}

// uploadViaLinkConcurrent posts a multipart file to a seafhttp upload URL,
// returning a concurrentMutationResult so it composes with expectConcurrentStatuses.
func uploadViaLinkConcurrent(c *testClient, uploadURL, filename, parentDir, content string) concurrentMutationResult {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if part, err := w.CreateFormFile("file", filename); err != nil {
		return concurrentMutationResult{err: err}
	} else if _, err := part.Write([]byte(content)); err != nil {
		return concurrentMutationResult{err: err}
	}
	if err := w.WriteField("parent_dir", parentDir); err != nil {
		return concurrentMutationResult{err: err}
	}
	if err := w.Close(); err != nil {
		return concurrentMutationResult{err: err}
	}

	req, err := http.NewRequest(http.MethodPost, uploadURL, &buf)
	if err != nil {
		return concurrentMutationResult{err: err}
	}
	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := c.http.Do(req)
	if err != nil {
		return concurrentMutationResult{err: err}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return concurrentMutationResult{status: resp.StatusCode, body: string(body)}
}

// uploadViaV2DirectConcurrent posts a multipart file to the direct v2 upload
// endpoint so the request exercises files.go UploadFile and
// finalizeStoredUploadMetadataOnce instead of seafhttp.
func uploadViaV2DirectConcurrent(c *testClient, repoID, filename, parentDir, content string) concurrentMutationResult {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if part, err := w.CreateFormFile("file", filename); err != nil {
		return concurrentMutationResult{err: err}
	} else if _, err := part.Write([]byte(content)); err != nil {
		return concurrentMutationResult{err: err}
	}
	if err := w.WriteField("parent_dir", parentDir); err != nil {
		return concurrentMutationResult{err: err}
	}
	if err := w.Close(); err != nil {
		return concurrentMutationResult{err: err}
	}

	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/api/v2.1/repos/"+repoID+"/upload", &buf)
	if err != nil {
		return concurrentMutationResult{err: err}
	}
	req.Header.Set("Authorization", "Token "+c.token)
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := c.http.Do(req)
	if err != nil {
		return concurrentMutationResult{err: err}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return concurrentMutationResult{status: resp.StatusCode, body: string(body)}
}

func uploadTokenFromURL(uploadURL string) string {
	idx := strings.LastIndex(uploadURL, "/")
	if idx == -1 || idx+1 >= len(uploadURL) {
		return ""
	}
	return uploadURL[idx+1:]
}

// ── 1. High-concurrency seafhttp upload (commitUploadedFile path) ─────────────
//
// 16 goroutines race to finalise their upload metadata against the same library
// HEAD. With the bug every goroutine reads the same stale HEAD, builds an
// independent tree, then competes in the CAS. At most one wins per round; the
// others get ErrLibraryHeadConflict and retry. The retry re-reads HEAD from
// scratch, so a correct implementation converges. A broken implementation that
// does NOT re-read HEAD on retry would publish its stale tree and lose the
// winning commits of concurrent uploads.

func TestConcurrentSeafhttpUploadsHighConcurrencyNoLostCommits(t *testing.T) {
	const concurrency = 16

	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-seafhttp-race-%d", time.Now().UnixNano()))
	uploadURL := getUploadLink(t, adminClient, repoID, "/")

	names := make([]string, concurrency)
	for i := range names {
		names[i] = fmt.Sprintf("race-%02d.txt", i)
	}

	results := runConcurrentMutations(t, names, func(name string) concurrentMutationResult {
		content := fmt.Sprintf("upload %s at %d\n", name, time.Now().UnixNano())
		r := uploadViaLinkConcurrent(adminClient, uploadURL, name, "/", content)
		r.name = name
		return r
	})
	expectConcurrentStatuses(t, results, http.StatusOK, http.StatusCreated)
	expectEntriesPresent(t, repoID, "/", names)
}

// ── 2. Upload vs concurrent rename — upload must survive the rename ───────────
//
// While 6 uploads to the root are in flight a rename on an existing file also
// runs. The rename advances HEAD; the uploads must retry against the new HEAD
// and still land. A broken Once() that uses the pre-rename snapshot on retry
// would lose data.

func TestConcurrentSeafhttpUploadWhileRenamingNoLostFiles(t *testing.T) {
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-upload-rename-race-%d", time.Now().UnixNano()))
	uploadURL := getUploadLink(t, adminClient, repoID, "/")

	// Seed an anchor file that we will rename during the concurrent uploads.
	uploadFileThroughLink(t, adminClient, uploadURL, "anchor.txt", "/", "anchor content\n")

	const uploadCount = 6
	uploadNames := make([]string, uploadCount)
	for i := range uploadNames {
		uploadNames[i] = fmt.Sprintf("upload-%02d.txt", i)
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	uploadErrs := make(chan error, uploadCount)
	var renameStatus int
	var renameBody string

	// Start uploads
	for _, name := range uploadNames {
		name := name
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			r := uploadViaLinkConcurrent(adminClient, uploadURL, name, "/", fmt.Sprintf("content of %s\n", name))
			if r.err != nil {
				uploadErrs <- r.err
			} else if r.status != http.StatusOK && r.status != http.StatusCreated {
				uploadErrs <- fmt.Errorf("upload %s: status %d body %s", name, r.status, r.body)
			}
		}()
	}

	// Start rename concurrently — FileOperation reads `operation` from form body
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		resp := adminClient.PostJSON(t,
			fmt.Sprintf("/api2/repos/%s/file/?p=/anchor.txt&operation=rename", repoID),
			map[string]string{"newname": "anchor-renamed.txt"},
		)
		renameStatus = resp.StatusCode
		renameBody = responseBody(t, resp)
	}()

	close(start)
	wg.Wait()
	close(uploadErrs)

	for err := range uploadErrs {
		t.Errorf("upload error: %v", err)
	}
	if renameStatus != http.StatusOK {
		t.Errorf("rename status = %d body=%s", renameStatus, renameBody)
	}

	expectEntriesPresent(t, repoID, "/", uploadNames)
	expectEntriesPresent(t, repoID, "/", []string{"anchor-renamed.txt"})
	expectEntriesAbsent(t, repoID, "/", []string{"anchor.txt"})
}

// ── 3. Upload vs concurrent delete — upload must land; deleted file must go ───
//
// Deleting a file commits a new HEAD. Uploads in flight at the same time must
// retry against that new HEAD. Both outcomes must be correct: new files present,
// deleted file absent.

func TestConcurrentSeafhttpUploadWhileDeletingNoLostFiles(t *testing.T) {
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-upload-delete-race-%d", time.Now().UnixNano()))
	uploadURL := getUploadLink(t, adminClient, repoID, "/")

	// Seed files to delete
	deleteNames := []string{"del-a.txt", "del-b.txt", "del-c.txt"}
	for _, n := range deleteNames {
		uploadFileThroughLink(t, adminClient, uploadURL, n, "/", fmt.Sprintf("delete me: %s\n", n))
	}

	const uploadCount = 6
	uploadNames := make([]string, uploadCount)
	for i := range uploadNames {
		uploadNames[i] = fmt.Sprintf("new-%02d.txt", i)
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	uploadErrs := make(chan error, uploadCount)

	for _, name := range uploadNames {
		name := name
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			r := uploadViaLinkConcurrent(adminClient, uploadURL, name, "/", fmt.Sprintf("new file %s\n", name))
			if r.err != nil {
				uploadErrs <- r.err
			} else if r.status != http.StatusOK && r.status != http.StatusCreated {
				uploadErrs <- fmt.Errorf("upload %s: status %d body %s", name, r.status, r.body)
			}
		}()
	}

	// Batch-delete the seed files concurrently
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		resp := adminClient.DeleteJSON(t, "/api/v2.1/repos/batch-delete-item/", map[string]interface{}{
			"repo_id":    repoID,
			"parent_dir": "/",
			"dirents":    deleteNames,
		})
		resp.Body.Close()
	}()

	close(start)
	wg.Wait()
	close(uploadErrs)

	for err := range uploadErrs {
		t.Errorf("upload error: %v", err)
	}

	expectEntriesPresent(t, repoID, "/", uploadNames)
	expectEntriesAbsent(t, repoID, "/", deleteNames)
}

// ── 4. Concurrent v2 uploads (finalizeStoredUploadMetadataOnce) ──────────────
//
// The direct v2 API upload path (POST /api/v2.1/repos/:id/upload) calls
// finalizeStoredUploadMetadata, which had the same snapshot inconsistency as
// the seafhttp paths. This test fires 10 concurrent direct uploads to confirm
// the retry logic converges with no lost commits.

func TestConcurrentV2UploadsNoLostCommits(t *testing.T) {
	const concurrency = 10

	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-v2upload-race-%d", time.Now().UnixNano()))

	names := make([]string, concurrency)
	for i := range names {
		names[i] = fmt.Sprintf("v2-%02d.txt", i)
	}

	results := runConcurrentMutations(t, names, func(name string) concurrentMutationResult {
		content := fmt.Sprintf("v2 upload %s at %d\n", name, time.Now().UnixNano())
		r := uploadViaV2DirectConcurrent(adminClient, repoID, name, "/", content)
		r.name = name
		return r
	})
	expectConcurrentStatuses(t, results, http.StatusOK, http.StatusCreated)
	expectEntriesPresent(t, repoID, "/", names)
}

// ── 5. Subdirectory upload concurrency ────────────────────────────────────────
//
// Uploads to a subdirectory require path rebuilding from leaf to root.
// The snapshot inconsistency is worse here: TraverseToPath reads HEAD to find
// the subdirectory's FS ID, then UpdateLibraryHead compares against a possibly
// different headCommitID captured before the traversal. Test this path
// explicitly with many concurrent uploads to /subdir/.

func TestConcurrentSeafhttpUploadsToSubdirNoLostCommits(t *testing.T) {
	const concurrency = 10
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-subdir-race-%d", time.Now().UnixNano()))

	// Create the subdirectory first
	resp := adminClient.PostJSON(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/subdir", repoID), map[string]string{})
	expectStatus(t, resp, http.StatusCreated)
	resp.Body.Close()

	uploadURL := getUploadLink(t, adminClient, repoID, "/subdir")

	names := make([]string, concurrency)
	for i := range names {
		names[i] = fmt.Sprintf("sub-%02d.txt", i)
	}

	results := runConcurrentMutations(t, names, func(name string) concurrentMutationResult {
		content := fmt.Sprintf("subdir upload %s at %d\n", name, time.Now().UnixNano())
		r := uploadViaLinkConcurrent(adminClient, uploadURL, name, "/subdir", content)
		r.name = name
		return r
	})
	expectConcurrentStatuses(t, results, http.StatusOK, http.StatusCreated)
	expectEntriesPresent(t, repoID, "/subdir", names)
}

// When a resumable upload starts first but another writer creates the original
// target name before finalize, the chunked upload should autorename and return
// the winning name back to the client in the final ret-json response.
func TestChunkedUploadRaceReturnsAutorenameInResponse(t *testing.T) {
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-chunked-autorename-race-%d", time.Now().UnixNano()))
	uploadURL := getUploadLink(t, adminClient, repoID, "/")
	retJSONUploadURL := uploadURL + "?ret-json=1"

	fileName := "chunked-race.txt"
	autoRenamed := "chunked-race (1).txt"
	fileContent := []byte("abcdefghij")

	status, body := uploadChunkThroughLinkStatus(t, adminClient, retJSONUploadURL, fileName, "/", fileContent[:5], "bytes 0-4/10")
	if status != http.StatusOK {
		t.Fatalf("first chunk status = %d, want %d; body=%s", status, http.StatusOK, body)
	}

	uploadFileThroughLink(t, adminClient, uploadURL, fileName, "/", "competing writer\n")

	status, body = uploadChunkThroughLinkStatus(t, adminClient, retJSONUploadURL, fileName, "/", fileContent[5:], "bytes 5-9/10")
	if status != http.StatusOK {
		t.Fatalf("final chunk status = %d, want %d; body=%s", status, http.StatusOK, body)
	}

	var payload []map[string]interface{}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("failed to decode finalize payload: %v body=%s", err, body)
	}
	if len(payload) != 1 {
		t.Fatalf("finalize payload length = %d, want 1; body=%s", len(payload), body)
	}
	if got, _ := payload[0]["name"].(string); got != autoRenamed {
		t.Fatalf("finalize payload name = %q, want %q; body=%s", got, autoRenamed, body)
	}

	listResp := adminClient.Get(t, fmt.Sprintf("/api/v2.1/repos/%s/dir/?p=/", repoID))
	expectStatus(t, listResp, http.StatusOK)
	var dirList map[string]interface{}
	decodeJSON(t, listResp, &dirList)
	entries, _ := dirList["dirent_list"].([]interface{})
	if !containsEntry(entries, "name", fileName) {
		t.Fatalf("original file %q not found after chunked autorename race", fileName)
	}
	if !containsEntry(entries, "name", autoRenamed) {
		t.Fatalf("autorename target %q not found after chunked autorename race", autoRenamed)
	}
}

func TestChunkedUploadConflictRollbackCleansStateBeforeFreshReupload(t *testing.T) {
	requireGCEnabled(t)
	requireCassandra(t)

	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-chunked-conflict-reupload-%d", time.Now().UnixNano()))
	orgID := resolveOrgID(t, repoID)
	session := shareProjectionDBForTest(t).Session()
	setHeadCommit := func(head string) error {
		if err := session.Query(`UPDATE libraries SET head_commit_id = ? WHERE org_id = ? AND library_id = ?`, head, orgID, repoID).Exec(); err != nil {
			return err
		}
		return session.Query(`UPDATE libraries_by_id SET head_commit_id = ? WHERE library_id = ?`, head, repoID).Exec()
	}
	readHeadCommit := func() string {
		var head string
		if err := session.Query(`SELECT head_commit_id FROM libraries WHERE org_id = ? AND library_id = ?`, orgID, repoID).Scan(&head); err != nil {
			t.Fatalf("failed to read canonical head for %s: %v", repoID, err)
		}
		return head
	}
	currentHead := readHeadCommit()
	var rootFSID string
	if err := session.Query(`SELECT root_fs_id FROM commits WHERE library_id = ? AND commit_id = ?`, repoID, currentHead).Scan(&rootFSID); err != nil {
		t.Fatalf("failed to resolve root_fs_id for repo %s head %s: %v", repoID, currentHead, err)
	}

	// Track every churn commit so we can wipe them out at the end of the
	// test. Cassandra volumes outlive the docker-compose test runner, so
	// leaving thousands of rows behind would inflate counter-sensitive tests
	// like the GC queue snapshot reconciliation suite on subsequent runs.
	var churnTrackerMu sync.Mutex
	churnedCommits := make([]string, 0, 4096)
	t.Cleanup(func() {
		churnTrackerMu.Lock()
		commitsToDelete := append([]string(nil), churnedCommits...)
		churnTrackerMu.Unlock()
		for _, head := range commitsToDelete {
			if err := session.Query(`DELETE FROM commits WHERE library_id = ? AND commit_id = ?`, repoID, head).Exec(); err != nil {
				t.Logf("cleanup: delete churn commit %s failed: %v", head, err)
			}
		}
	})
	uploadURL := getUploadLink(t, adminClient, repoID, "/")
	uploadToken := uploadTokenFromURL(uploadURL)
	if uploadToken == "" {
		t.Fatalf("could not extract upload token from %q", uploadURL)
	}

	fileName := "chunked-conflict-reupload.txt"
	uniqueRunMarker := fmt.Sprintf("run-%d", time.Now().UnixNano())
	fileContent := []byte(strings.Repeat("chunked-conflict-reupload-", 8) + uniqueRunMarker + "-done")
	hash := sha256.Sum256(fileContent)
	blockID := hex.EncodeToString(hash[:])
	orgUUID, err := uuid.Parse(orgID)
	if err != nil {
		t.Fatalf("failed to parse org ID %q: %v", orgID, err)
	}
	cleanupGCBlockFixturesForTest(t, orgUUID, blockID)
	t.Cleanup(func() {
		cleanupGCBlockFixturesForTest(t, orgUUID, blockID)
	})
	tempPath := filepath.Join(os.TempDir(), fmt.Sprintf("sesamefs_upload_%s_%s", uploadToken, fileName))

	status, body := uploadChunkThroughLinkStatus(t, adminClient, uploadURL, fileName, "/", fileContent[:len(fileContent)/2], fmt.Sprintf("bytes %d-%d/%d", 0, len(fileContent)/2-1, len(fileContent)))
	if status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("first chunk status = %d, want 200/201; body=%s", status, body)
	}

	// Churn the canonical HEAD continuously by generating a fresh commit on
	// every iteration. Each value is unique, so the server's snapshot is
	// guaranteed stale before it reaches CAS, and the churn keeps running
	// until we stop it — covering the full ~9s upload-finalize retry budget
	// without any chance of accidental alignment with the snapshot value.
	seedBase := time.Now().UnixNano()
	stop := make(chan struct{})
	errCh := make(chan error, 1)
	churnStarted := make(chan struct{}, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		var churnIndex int64
		for {
			select {
			case <-stop:
				return
			default:
			}
			churnIndex++
			head := fmt.Sprintf("%040x", seedBase+churnIndex)
			// creator_id is a UUID column; the existing
			// insertSyntheticCommitForTest helper uses defaultOrgID for the
			// same reason. Any valid UUID works — the value is only inspected
			// when resolving the original commit author, which churn rows
			// never participate in.
			if err := session.Query(`
				INSERT INTO commits (library_id, commit_id, parent_id, root_fs_id, creator_id, description, created_at)
				VALUES (?, ?, ?, ?, ?, ?, ?)
			`, repoID, head, currentHead, rootFSID, defaultOrgID, fmt.Sprintf("synthetic churn %d", churnIndex), time.Now()).Exec(); err == nil {
				churnTrackerMu.Lock()
				churnedCommits = append(churnedCommits, head)
				churnTrackerMu.Unlock()
			} else {
				select {
				case errCh <- fmt.Errorf("insert churn commit %s: %w", head, err):
				default:
				}
				return
			}
			if err := setHeadCommit(head); err != nil {
				select {
				case errCh <- fmt.Errorf("advance head %s: %w", head, err):
				default:
				}
				return
			}
			if churnIndex == 32 {
				select {
				case churnStarted <- struct{}{}:
				default:
				}
			}
			time.Sleep(1 * time.Millisecond)
		}
	}()
	select {
	case <-churnStarted:
	case err := <-errCh:
		close(stop)
		wg.Wait()
		t.Fatalf("head churn failed before finalize: %v", err)
	case <-time.After(10 * time.Second):
		close(stop)
		wg.Wait()
		t.Fatalf("head churn did not start within 10s; current head=%s", readHeadCommit())
	}
	time.Sleep(100 * time.Millisecond)

	status, body = uploadChunkThroughLinkStatus(t, adminClient, uploadURL, fileName, "/", fileContent[len(fileContent)/2:], fmt.Sprintf("bytes %d-%d/%d", len(fileContent)/2, len(fileContent)-1, len(fileContent)))
	close(stop)
	wg.Wait()
	if err := setHeadCommit(currentHead); err != nil {
		t.Fatalf("failed to restore stable head %s after churn: %v", currentHead, err)
	}
	select {
	case err := <-errCh:
		t.Fatalf("head churn failed: %v", err)
	default:
	}

	if status != http.StatusConflict {
		t.Fatalf("final chunk status = %d, want %d; body=%s", status, http.StatusConflict, body)
	}
	if !strings.Contains(body, "retry the upload") {
		t.Fatalf("final chunk body = %q, want retryable upload conflict", body)
	}
	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Fatalf("expected old chunk temp file %s to be removed after conflict cleanup, stat err=%v", tempPath, err)
	}

	// After rollback, ref_count must be <= 0. Three states satisfy this and are
	// all valid depending on GC scanner timing:
	//   - 0:    rollback decremented; GC has not yet touched the row.
	//   - -999: GC's ClaimBlockDelete won the LWT and stamped the sentinel, or
	//           the row has already been physically deleted (readBlockRefCount
	//           returns -999 in both cases).
	// A ref_count == 1 here is the bug this test exists to catch (rollback
	// failed to decrement). Checking gc_queue presence would race against the
	// scanner consuming the entry, so we rely solely on ref_count.
	if !pollUntil(t, 10*time.Second, 200*time.Millisecond, func() bool {
		return readBlockRefCount(t, orgID, blockID) <= 0
	}) {
		t.Fatalf("rollback did not decrement block refs after conflict: ref_count=%d", readBlockRefCount(t, orgID, blockID))
	}

	freshUploadURL := getUploadLink(t, adminClient, repoID, "/")
	if freshUploadURL == uploadURL {
		t.Fatalf("expected a fresh upload link after conflict, got same URL %q", freshUploadURL)
	}

	status, body = uploadChunkThroughLinkStatus(t, adminClient, freshUploadURL, fileName, "/", fileContent[:len(fileContent)/2], fmt.Sprintf("bytes %d-%d/%d", 0, len(fileContent)/2-1, len(fileContent)))
	if status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("fresh first chunk status = %d, want 200/201; body=%s", status, body)
	}
	status, body = uploadChunkThroughLinkStatus(t, adminClient, freshUploadURL, fileName, "/", fileContent[len(fileContent)/2:], fmt.Sprintf("bytes %d-%d/%d", len(fileContent)/2, len(fileContent)-1, len(fileContent)))
	if status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("fresh final chunk status = %d, want 200/201; body=%s", status, body)
	}

	expectEntriesPresent(t, repoID, "/", []string{fileName})
	if !pollUntil(t, 10*time.Second, 200*time.Millisecond, func() bool {
		return readBlockRefCount(t, orgID, blockID) == 1
	}) {
		t.Fatalf("ref_count after fresh reupload = %d, want 1", readBlockRefCount(t, orgID, blockID))
	}
}

// ── 6. Two-region concurrent uploads ─────────────────────────────────────────
//
// This test requires a second sesamefs instance (different storage region)
// reachable at SESAMEFS_URL_2. Both instances share Cassandra, so they race
// against the same library HEAD in Cassandra. This is the sharpest test of the
// CAS correctness: two independent Go processes both trying to advance HEAD of
// the same library at the same time, with no in-process coordination.
//
// Bring up both regions with:
//   docker compose -f docker-compose-multiregion.yaml up -d
//
// Then run this test with (3000 = USA, 4001 = EU; see compose port mappings):
//   SESAMEFS_URL=http://localhost:3000 SESAMEFS_URL_2=http://localhost:4001 \
//     go test -tags integration ./internal/integration/ -run TestConcurrentTwoRegionUploadsNoLostCommits -v

func requireSecondRegionUploadRaceClient(t *testing.T) (*testClient, string) {
	t.Helper()

	url2 := os.Getenv("SESAMEFS_URL_2")
	if url2 == "" {
		t.Skip("SESAMEFS_URL_2 not set — start the EU region with docker-compose-multiregion.yaml and set the env var")
	}

	region2Client := newTestClient(url2, "dev-token-admin")
	if err := verifyIntegrationAuth(url2, "dev-token-admin"); err != nil {
		t.Skipf("second region %s not reachable or not authenticated: %v", url2, err)
	}

	return region2Client, url2
}

func TestConcurrentTwoRegionUploadsNoLostCommits(t *testing.T) {
	region2Client, url2 := requireSecondRegionUploadRaceClient(t)

	const perRegion = 8
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-tworegion-%d", time.Now().UnixNano()))

	uploadURL1 := getUploadLink(t, adminClient, repoID, "/")
	uploadURL2 := getUploadLink(t, region2Client, repoID, "/")

	// Replace the host of URL2 so it routes through the region-2 API
	// (upload-link URLs are absolute; the region2Client base URL has the right host)
	uploadURL2 = rewriteUploadURLHost(uploadURL2, url2)

	allNames := make([]string, 0, perRegion*2)
	region1Names := make([]string, perRegion)
	region2Names := make([]string, perRegion)
	for i := 0; i < perRegion; i++ {
		region1Names[i] = fmt.Sprintf("usa-%02d.txt", i)
		region2Names[i] = fmt.Sprintf("eu-%02d.txt", i)
		allNames = append(allNames, region1Names[i], region2Names[i])
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, perRegion*2)

	for _, name := range region1Names {
		name := name
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			r := uploadViaLinkConcurrent(adminClient, uploadURL1, name, "/", fmt.Sprintf("usa %s at %d\n", name, time.Now().UnixNano()))
			if r.err != nil {
				errs <- fmt.Errorf("region1 %s: %w", name, r.err)
			} else if r.status != http.StatusOK && r.status != http.StatusCreated {
				errs <- fmt.Errorf("region1 %s: status %d body %s", name, r.status, r.body)
			}
		}()
	}

	for _, name := range region2Names {
		name := name
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			r := uploadViaLinkConcurrent(region2Client, uploadURL2, name, "/", fmt.Sprintf("eu %s at %d\n", name, time.Now().UnixNano()))
			if r.err != nil {
				errs <- fmt.Errorf("region2 %s: %w", name, r.err)
			} else if r.status != http.StatusOK && r.status != http.StatusCreated {
				errs <- fmt.Errorf("region2 %s: status %d body %s", name, r.status, r.body)
			}
		}()
	}

	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("upload error: %v", err)
	}

	// Use region1 client for listing (both regions share Cassandra)
	expectEntriesPresent(t, repoID, "/", allNames)
}

// The direct v2 upload endpoint has its own metadata-finalization path in
// files.go. This test makes both regions race through that path against the
// same library HEAD so the multi-process proof covers both upload seams.
func TestConcurrentTwoRegionV2UploadsNoLostCommits(t *testing.T) {
	region2Client, _ := requireSecondRegionUploadRaceClient(t)

	const perRegion = 6
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-tworegion-v2-%d", time.Now().UnixNano()))

	allNames := make([]string, 0, perRegion*2)
	region1Names := make([]string, perRegion)
	region2Names := make([]string, perRegion)
	for i := 0; i < perRegion; i++ {
		region1Names[i] = fmt.Sprintf("v2-usa-%02d.txt", i)
		region2Names[i] = fmt.Sprintf("v2-eu-%02d.txt", i)
		allNames = append(allNames, region1Names[i], region2Names[i])
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, perRegion*2)

	for _, name := range region1Names {
		name := name
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			r := uploadViaV2DirectConcurrent(adminClient, repoID, name, "/", fmt.Sprintf("region1 %s at %d\n", name, time.Now().UnixNano()))
			if r.err != nil {
				errs <- fmt.Errorf("region1 %s: %w", name, r.err)
			} else if r.status != http.StatusOK && r.status != http.StatusCreated {
				errs <- fmt.Errorf("region1 %s: status %d body %s", name, r.status, r.body)
			}
		}()
	}

	for _, name := range region2Names {
		name := name
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			r := uploadViaV2DirectConcurrent(region2Client, repoID, name, "/", fmt.Sprintf("region2 %s at %d\n", name, time.Now().UnixNano()))
			if r.err != nil {
				errs <- fmt.Errorf("region2 %s: %w", name, r.err)
			} else if r.status != http.StatusOK && r.status != http.StatusCreated {
				errs <- fmt.Errorf("region2 %s: status %d body %s", name, r.status, r.body)
			}
		}()
	}

	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("upload error: %v", err)
	}

	expectEntriesPresent(t, repoID, "/", allNames)
}

// rewriteUploadURLHost takes the upload URL returned by the API and rewrites
// its scheme+host to route through the given base URL instead. The seafhttp
// upload token is in the path, so the path stays unchanged.
func rewriteUploadURLHost(uploadURL, baseURL string) string {
	u1, err1 := url.Parse(uploadURL)
	u2, err2 := url.Parse(baseURL)
	if err1 != nil || err2 != nil {
		return uploadURL
	}
	u1.Scheme = u2.Scheme
	u1.Host = u2.Host
	return u1.String()
}
