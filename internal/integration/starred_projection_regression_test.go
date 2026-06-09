//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"
)

func TestStarredFilesProjectionRegression_StarAndUnstarDualWrite(t *testing.T) {
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-starred-dualwrite-%d", time.Now().UnixNano()))
	session := shareProjectionDBForTest(t).Session()

	userID, ok := lookupUserIDByEmail(t, defaultAdminEmail)
	if !ok {
		t.Fatalf("expected user_id for %s", defaultAdminEmail)
	}

	filePath := "/projection-dualwrite.txt"
	createEmptyFileForStarredProjectionTest(t, adminClient, repoID, filePath)

	starFileForProjectionTest(t, adminClient, repoID, filePath)
	waitForIntegrationCondition(t, "starred canonical/projection dual-write", func() bool {
		return starredCanonicalExistsForTest(t, session, userID, repoID, filePath) &&
			starredProjectionExistsForTest(t, session, userID, repoID, filePath)
	})

	unstarFileForProjectionTest(t, adminClient, repoID, filePath)
	waitForIntegrationCondition(t, "starred canonical/projection dual-delete", func() bool {
		return !starredCanonicalExistsForTest(t, session, userID, repoID, filePath) &&
			!starredProjectionExistsForTest(t, session, userID, repoID, filePath)
	})
}

func TestStarredFilesProjectionRegression_DeleteStarredFilesByLibrary(t *testing.T) {
	repoA := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-starred-gc-lib-a-%d", time.Now().UnixNano()))
	repoB := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-starred-gc-lib-b-%d", time.Now().UnixNano()))
	database := shareProjectionDBForTest(t)
	session := database.Session()
	store := gcpkg.NewCassandraStore(database)

	userA := uuid.New().String()
	userB := uuid.New().String()
	pathA1 := "/repo-a-user-a.txt"
	pathA2 := "/repo-a-user-b.txt"
	pathB := "/repo-b-user-a.txt"
	now := time.Now().UTC()

	insertStarredRowForProjectionTest(t, session, userA, repoA, pathA1, now)
	insertStarredRowForProjectionTest(t, session, userB, repoA, pathA2, now.Add(time.Second))
	insertStarredRowForProjectionTest(t, session, userA, repoB, pathB, now.Add(2*time.Second))
	t.Cleanup(func() {
		deleteStarredRowForProjectionTest(t, session, userA, repoA, pathA1)
		deleteStarredRowForProjectionTest(t, session, userB, repoA, pathA2)
		deleteStarredRowForProjectionTest(t, session, userA, repoB, pathB)
	})

	repoUUID, err := uuid.Parse(repoA)
	if err != nil {
		t.Fatalf("failed to parse repo uuid %q: %v", repoA, err)
	}
	if err := store.DeleteStarredFilesByLibrary(repoUUID); err != nil {
		t.Fatalf("DeleteStarredFilesByLibrary failed: %v", err)
	}

	waitForIntegrationCondition(t, "library starred cleanup to remove canonical and projection rows", func() bool {
		return !starredCanonicalExistsForTest(t, session, userA, repoA, pathA1) &&
			!starredProjectionExistsForTest(t, session, userA, repoA, pathA1) &&
			!starredCanonicalExistsForTest(t, session, userB, repoA, pathA2) &&
			!starredProjectionExistsForTest(t, session, userB, repoA, pathA2) &&
			starredCanonicalExistsForTest(t, session, userA, repoB, pathB) &&
			starredProjectionExistsForTest(t, session, userA, repoB, pathB)
	})
}

func TestStarredFilesProjectionRegression_DeleteStarredFilesByUser(t *testing.T) {
	repoA := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-starred-gc-user-a-%d", time.Now().UnixNano()))
	repoB := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-starred-gc-user-b-%d", time.Now().UnixNano()))
	database := shareProjectionDBForTest(t)
	session := database.Session()
	store := gcpkg.NewCassandraStore(database)

	targetUser := uuid.New().String()
	otherUser := uuid.New().String()
	pathA := "/user-target-a.txt"
	pathB := "/user-target-b.txt"
	otherPath := "/user-other-a.txt"
	now := time.Now().UTC()

	insertStarredRowForProjectionTest(t, session, targetUser, repoA, pathA, now)
	insertStarredRowForProjectionTest(t, session, targetUser, repoB, pathB, now.Add(time.Second))
	insertStarredRowForProjectionTest(t, session, otherUser, repoA, otherPath, now.Add(2*time.Second))
	t.Cleanup(func() {
		deleteStarredRowForProjectionTest(t, session, targetUser, repoA, pathA)
		deleteStarredRowForProjectionTest(t, session, targetUser, repoB, pathB)
		deleteStarredRowForProjectionTest(t, session, otherUser, repoA, otherPath)
	})

	targetUUID, err := uuid.Parse(targetUser)
	if err != nil {
		t.Fatalf("failed to parse user uuid %q: %v", targetUser, err)
	}
	if err := store.DeleteStarredFilesByUser(targetUUID); err != nil {
		t.Fatalf("DeleteStarredFilesByUser failed: %v", err)
	}

	waitForIntegrationCondition(t, "user starred cleanup to remove canonical and projection rows", func() bool {
		return !starredCanonicalExistsForTest(t, session, targetUser, repoA, pathA) &&
			!starredProjectionExistsForTest(t, session, targetUser, repoA, pathA) &&
			!starredCanonicalExistsForTest(t, session, targetUser, repoB, pathB) &&
			!starredProjectionExistsForTest(t, session, targetUser, repoB, pathB) &&
			starredCanonicalExistsForTest(t, session, otherUser, repoA, otherPath) &&
			starredProjectionExistsForTest(t, session, otherUser, repoA, otherPath)
	})
}

func createEmptyFileForStarredProjectionTest(t *testing.T, client *testClient, repoID, filePath string) {
	t.Helper()

	resp := client.PostForm(t, fmt.Sprintf("/api2/repos/%s/file/?p=%s&operation=create", repoID, url.QueryEscape(filePath)), url.Values{})
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body := responseBody(t, resp)
		t.Fatalf("create file for starred projection test failed: status=%d body=%s", resp.StatusCode, body)
	}
	resp.Body.Close()
}

func starFileForProjectionTest(t *testing.T, client *testClient, repoID, filePath string) {
	t.Helper()

	resp := client.PostJSON(t, "/api/v2.1/starred-items/", map[string]string{
		"repo_id": repoID,
		"path":    filePath,
	})
	if resp.StatusCode != http.StatusOK {
		body := responseBody(t, resp)
		t.Fatalf("star file %s in repo %s failed: status=%d body=%s", filePath, repoID, resp.StatusCode, body)
	}
	resp.Body.Close()
}

func unstarFileForProjectionTest(t *testing.T, client *testClient, repoID, filePath string) {
	t.Helper()

	resp := client.Delete(t, fmt.Sprintf("/api/v2.1/starred-items/?repo_id=%s&path=%s", repoID, url.QueryEscape(filePath)))
	if resp.StatusCode != http.StatusOK {
		body := responseBody(t, resp)
		t.Fatalf("unstar file %s in repo %s failed: status=%d body=%s", filePath, repoID, resp.StatusCode, body)
	}
	resp.Body.Close()
}

func insertStarredRowForProjectionTest(t *testing.T, session *gocql.Session, userID, repoID, filePath string, starredAt time.Time) {
	t.Helper()

	batch := session.Batch(gocql.LoggedBatch)
	batch.Query(`
		INSERT INTO starred_files (user_id, repo_id, path, starred_at)
		VALUES (?, ?, ?, ?)
	`, userID, repoID, filePath, starredAt)
	batch.Query(`
		INSERT INTO starred_files_by_repo (repo_id, user_id, path, starred_at)
		VALUES (?, ?, ?, ?)
	`, repoID, userID, filePath, starredAt)
	if err := session.ExecuteBatch(batch); err != nil {
		t.Fatalf("failed to seed starred rows for user %s repo %s path %s: %v", userID, repoID, filePath, err)
	}
}

func deleteStarredRowForProjectionTest(t *testing.T, session *gocql.Session, userID, repoID, filePath string) {
	t.Helper()

	batch := session.Batch(gocql.UnloggedBatch)
	batch.Query(`DELETE FROM starred_files WHERE user_id = ? AND repo_id = ? AND path = ?`, userID, repoID, filePath)
	batch.Query(`DELETE FROM starred_files_by_repo WHERE repo_id = ? AND user_id = ? AND path = ?`, repoID, userID, filePath)
	if err := session.ExecuteBatch(batch); err != nil {
		t.Errorf("cleanup starred rows for user %s repo %s path %s failed: %v", userID, repoID, filePath, err)
	}
}

func starredCanonicalExistsForTest(t *testing.T, session *gocql.Session, userID, repoID, filePath string) bool {
	t.Helper()

	var starredAt time.Time
	err := session.Query(`
		SELECT starred_at FROM starred_files WHERE user_id = ? AND repo_id = ? AND path = ?
	`, userID, repoID, filePath).Scan(&starredAt)
	if err == nil {
		return true
	}
	if err == gocql.ErrNotFound {
		return false
	}
	t.Fatalf("query starred_files for user %s repo %s path %s failed: %v", userID, repoID, filePath, err)
	return false
}

func starredProjectionExistsForTest(t *testing.T, session *gocql.Session, userID, repoID, filePath string) bool {
	t.Helper()

	var starredAt time.Time
	err := session.Query(`
		SELECT starred_at FROM starred_files_by_repo WHERE repo_id = ? AND user_id = ? AND path = ?
	`, repoID, userID, filePath).Scan(&starredAt)
	if err == nil {
		return true
	}
	if err == gocql.ErrNotFound {
		return false
	}
	t.Fatalf("query starred_files_by_repo for repo %s user %s path %s failed: %v", repoID, userID, filePath, err)
	return false
}
