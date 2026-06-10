//go:build integration

package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"
)

// These tests fence the file_tags_by_tag reverse-lookup projection (audit item B)
// against regressions: every file_tags mutation must keep the projection in sync,
// the tag_id read paths must use it (no ALLOW FILTERING), and library/tag teardown
// must leave no orphan projection rows.

func TestFileTagsProjectionRegression_AddAndRemoveDualWrite(t *testing.T) {
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-filetags-dualwrite-%d", time.Now().UnixNano()))
	session := shareProjectionDBForTest(t).Session()

	tagID := createRepoTagForProjectionTest(t, adminClient, repoID, "projection-dualwrite")
	filePath := "/projection-dualwrite.txt"

	fileTagID := addFileTagForProjectionTest(t, adminClient, repoID, filePath, tagID)
	waitForIntegrationCondition(t, "file tag canonical/projection dual-write", func() bool {
		return fileTagCanonicalExistsForTest(t, session, repoID, filePath, tagID) &&
			fileTagProjectionExistsForTest(t, session, repoID, tagID, filePath)
	})

	removeFileTagForProjectionTest(t, adminClient, repoID, fileTagID)
	waitForIntegrationCondition(t, "file tag canonical/projection dual-delete", func() bool {
		return !fileTagCanonicalExistsForTest(t, session, repoID, filePath, tagID) &&
			!fileTagProjectionExistsForTest(t, session, repoID, tagID, filePath)
	})

	t.Cleanup(func() { deleteRepoTagForProjectionTest(t, adminClient, repoID, tagID) })
}

func TestFileTagsProjectionRegression_DeleteRepoTagDropsProjection(t *testing.T) {
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-filetags-deltag-%d", time.Now().UnixNano()))
	session := shareProjectionDBForTest(t).Session()

	tagA := createRepoTagForProjectionTest(t, adminClient, repoID, "projection-tag-a")
	tagB := createRepoTagForProjectionTest(t, adminClient, repoID, "projection-tag-b")
	pathA1 := "/del-tag-a-1.txt"
	pathA2 := "/del-tag-a-2.txt"
	pathB := "/del-tag-b.txt"

	addFileTagForProjectionTest(t, adminClient, repoID, pathA1, tagA)
	addFileTagForProjectionTest(t, adminClient, repoID, pathA2, tagA)
	addFileTagForProjectionTest(t, adminClient, repoID, pathB, tagB)
	t.Cleanup(func() { deleteRepoTagForProjectionTest(t, adminClient, repoID, tagB) })

	waitForIntegrationCondition(t, "file tag projection seeded for both tags", func() bool {
		return fileTagProjectionExistsForTest(t, session, repoID, tagA, pathA1) &&
			fileTagProjectionExistsForTest(t, session, repoID, tagA, pathA2) &&
			fileTagProjectionExistsForTest(t, session, repoID, tagB, pathB)
	})

	// DeleteRepoTag reads the projection to drive canonical deletion and then
	// drops the whole projection partition for the tag.
	deleteRepoTagForProjectionTest(t, adminClient, repoID, tagA)

	waitForIntegrationCondition(t, "delete repo tag clears canonical + projection for that tag only", func() bool {
		return !fileTagCanonicalExistsForTest(t, session, repoID, pathA1, tagA) &&
			!fileTagProjectionExistsForTest(t, session, repoID, tagA, pathA1) &&
			!fileTagCanonicalExistsForTest(t, session, repoID, pathA2, tagA) &&
			!fileTagProjectionExistsForTest(t, session, repoID, tagA, pathA2) &&
			fileTagCanonicalExistsForTest(t, session, repoID, pathB, tagB) &&
			fileTagProjectionExistsForTest(t, session, repoID, tagB, pathB)
	})
}

func TestFileTagsProjectionRegression_GCDeleteFileTag(t *testing.T) {
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-filetags-gc-%d", time.Now().UnixNano()))
	database := shareProjectionDBForTest(t)
	session := database.Session()
	store := gcpkg.NewCassandraStore(database)

	tagID := 4242
	keptTagID := 4243
	targetPath := "/gc-target.txt"
	keptPath := "/gc-kept.txt"
	now := time.Now().UTC()

	insertFileTagRowForProjectionTest(t, session, repoID, targetPath, tagID, 9001, now)
	insertFileTagRowForProjectionTest(t, session, repoID, keptPath, keptTagID, 9002, now)
	t.Cleanup(func() {
		deleteFileTagRowForProjectionTest(t, session, repoID, targetPath, tagID)
		deleteFileTagRowForProjectionTest(t, session, repoID, keptPath, keptTagID)
	})

	repoUUID, err := uuid.Parse(repoID)
	if err != nil {
		t.Fatalf("failed to parse repo uuid %q: %v", repoID, err)
	}
	if err := store.DeleteFileTag(repoUUID, targetPath, tagID); err != nil {
		t.Fatalf("DeleteFileTag failed: %v", err)
	}

	waitForIntegrationCondition(t, "GC file-tag cleanup removes canonical and projection rows", func() bool {
		return !fileTagCanonicalExistsForTest(t, session, repoID, targetPath, tagID) &&
			!fileTagProjectionExistsForTest(t, session, repoID, tagID, targetPath) &&
			fileTagCanonicalExistsForTest(t, session, repoID, keptPath, keptTagID) &&
			fileTagProjectionExistsForTest(t, session, repoID, keptTagID, keptPath)
	})
}

// --- API helpers ---

func createRepoTagForProjectionTest(t *testing.T, client *testClient, repoID, name string) int {
	t.Helper()

	resp := client.PostJSON(t, fmt.Sprintf("/api/v2.1/repos/%s/repo-tags/", repoID), map[string]string{
		"name":  name,
		"color": "#FF8000",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create repo tag %q failed: status=%d body=%s", name, resp.StatusCode, responseBody(t, resp))
	}

	var parsed struct {
		RepoTag struct {
			ID int `json:"repo_tag_id"`
		} `json:"repo_tag"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		t.Fatalf("decode repo tag response: %v", err)
	}
	if parsed.RepoTag.ID <= 0 {
		t.Fatalf("create repo tag %q returned invalid tag id %d", name, parsed.RepoTag.ID)
	}
	return parsed.RepoTag.ID
}

func addFileTagForProjectionTest(t *testing.T, client *testClient, repoID, filePath string, repoTagID int) int {
	t.Helper()

	resp := client.PostJSON(t, fmt.Sprintf("/api/v2.1/repos/%s/file-tags/", repoID), map[string]interface{}{
		"file_path":   filePath,
		"repo_tag_id": repoTagID,
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("add file tag %s/%d failed: status=%d body=%s", filePath, repoTagID, resp.StatusCode, responseBody(t, resp))
	}

	var parsed struct {
		FileTag struct {
			ID int `json:"file_tag_id"`
		} `json:"file_tag"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		t.Fatalf("decode file tag response: %v", err)
	}
	if parsed.FileTag.ID <= 0 {
		t.Fatalf("add file tag %s returned invalid file_tag_id %d", filePath, parsed.FileTag.ID)
	}
	return parsed.FileTag.ID
}

func removeFileTagForProjectionTest(t *testing.T, client *testClient, repoID string, fileTagID int) {
	t.Helper()

	resp := client.Delete(t, fmt.Sprintf("/api/v2.1/repos/%s/file-tags/%d/", repoID, fileTagID))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("remove file tag %d failed: status=%d body=%s", fileTagID, resp.StatusCode, responseBody(t, resp))
	}
}

func deleteRepoTagForProjectionTest(t *testing.T, client *testClient, repoID string, tagID int) {
	t.Helper()

	resp := client.Delete(t, fmt.Sprintf("/api/v2.1/repos/%s/repo-tags/%d/", repoID, tagID))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("delete repo tag %d failed: status=%d body=%s", tagID, resp.StatusCode, responseBody(t, resp))
	}
}

// --- DB helpers ---

func insertFileTagRowForProjectionTest(t *testing.T, session *gocql.Session, repoID, filePath string, tagID, fileTagID int, createdAt time.Time) {
	t.Helper()

	batch := session.Batch(gocql.LoggedBatch)
	batch.Query(`
		INSERT INTO file_tags (repo_id, file_path, tag_id, file_tag_id, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, repoID, filePath, tagID, fileTagID, createdAt)
	batch.Query(`
		INSERT INTO file_tags_by_tag (repo_id, tag_id, file_path, file_tag_id, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, repoID, tagID, filePath, fileTagID, createdAt)
	if err := session.ExecuteBatch(batch); err != nil {
		t.Fatalf("failed to seed file tag rows for repo %s path %s tag %d: %v", repoID, filePath, tagID, err)
	}
}

func deleteFileTagRowForProjectionTest(t *testing.T, session *gocql.Session, repoID, filePath string, tagID int) {
	t.Helper()

	batch := session.Batch(gocql.UnloggedBatch)
	batch.Query(`DELETE FROM file_tags WHERE repo_id = ? AND file_path = ? AND tag_id = ?`, repoID, filePath, tagID)
	batch.Query(`DELETE FROM file_tags_by_tag WHERE repo_id = ? AND tag_id = ? AND file_path = ?`, repoID, tagID, filePath)
	if err := session.ExecuteBatch(batch); err != nil {
		t.Errorf("cleanup file tag rows for repo %s path %s tag %d failed: %v", repoID, filePath, tagID, err)
	}
}

func fileTagCanonicalExistsForTest(t *testing.T, session *gocql.Session, repoID, filePath string, tagID int) bool {
	t.Helper()

	var fileTagID int
	err := session.Query(`
		SELECT file_tag_id FROM file_tags WHERE repo_id = ? AND file_path = ? AND tag_id = ?
	`, repoID, filePath, tagID).Scan(&fileTagID)
	if err == nil {
		return true
	}
	if err == gocql.ErrNotFound {
		return false
	}
	t.Fatalf("query file_tags for repo %s path %s tag %d failed: %v", repoID, filePath, tagID, err)
	return false
}

func fileTagProjectionExistsForTest(t *testing.T, session *gocql.Session, repoID string, tagID int, filePath string) bool {
	t.Helper()

	var fileTagID int
	err := session.Query(`
		SELECT file_tag_id FROM file_tags_by_tag WHERE repo_id = ? AND tag_id = ? AND file_path = ?
	`, repoID, tagID, filePath).Scan(&fileTagID)
	if err == nil {
		return true
	}
	if err == gocql.ErrNotFound {
		return false
	}
	t.Fatalf("query file_tags_by_tag for repo %s tag %d path %s failed: %v", repoID, tagID, filePath, err)
	return false
}
