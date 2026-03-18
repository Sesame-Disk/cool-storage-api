package v2

import (
	"fmt"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

func createUserWithEmailLookup(db interface{ Session() *gocql.Session }, orgID, userID, email, name, role string, quotaBytes, usedBytes int64, createdAt time.Time) error {
	batch := db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		INSERT INTO users (org_id, user_id, email, name, role, quota_bytes, used_bytes, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, orgID, userID, email, name, role, quotaBytes, usedBytes, createdAt)
	batch.Query(`
		INSERT INTO users_by_email (email, user_id, org_id)
		VALUES (?, ?, ?)
	`, email, userID, orgID)
	return batch.Exec()
}

func updateLibraryOwner(db interface{ Session() *gocql.Session }, orgID, libraryID, newOwnerID string, updatedAt time.Time) error {
	batch := db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		UPDATE libraries SET owner_id = ?, updated_at = ?
		WHERE org_id = ? AND library_id = ?
	`, newOwnerID, updatedAt, orgID, libraryID)
	batch.Query(`
		UPDATE libraries_by_id SET owner_id = ?
		WHERE library_id = ?
	`, newOwnerID, libraryID)
	return batch.Exec()
}

func createRepoAPIToken(db interface{ Session() *gocql.Session }, repoID, appName, apiToken, permission, generatedBy string, createdAt time.Time) error {
	batch := db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		INSERT INTO repo_api_tokens (repo_id, app_name, api_token, permission, generated_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, repoID, appName, apiToken, permission, generatedBy, createdAt)
	batch.Query(`
		INSERT INTO repo_api_tokens_by_token (api_token, repo_id, app_name, permission, generated_by)
		VALUES (?, ?, ?, ?, ?)
	`, apiToken, repoID, appName, permission, generatedBy)
	return batch.Exec()
}

func updateRepoAPITokenPermission(db interface{ Session() *gocql.Session }, repoID, appName, apiToken, permission string) error {
	batch := db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		UPDATE repo_api_tokens SET permission = ? WHERE repo_id = ? AND app_name = ?
	`, permission, repoID, appName)
	batch.Query(`
		UPDATE repo_api_tokens_by_token SET permission = ? WHERE api_token = ?
	`, permission, apiToken)
	return batch.Exec()
}

func deleteRepoAPIToken(db interface{ Session() *gocql.Session }, repoID, appName, apiToken string) error {
	batch := db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		DELETE FROM repo_api_tokens WHERE repo_id = ? AND app_name = ?
	`, repoID, appName)
	if apiToken != "" {
		batch.Query(`
			DELETE FROM repo_api_tokens_by_token WHERE api_token = ?
		`, apiToken)
	}
	return batch.Exec()
}

func createLibraryShare(db interface{ Session() *gocql.Session }, libraryID, shareID, sharedBy, sharedTo, sharedToType, permission string, createdAt time.Time, expiresAt *time.Time) error {
	batch := db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		INSERT INTO shares (
			library_id, share_id, shared_by, shared_to, shared_to_type,
			permission, created_at, expires_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, libraryID, shareID, sharedBy, sharedTo, sharedToType, permission, createdAt, expiresAt)
	batch.Query(`
		INSERT INTO shares_by_user (shared_to, library_id, shared_to_type, permission, shared_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, sharedTo, libraryID, sharedToType, permission, sharedBy, createdAt)
	return batch.Exec()
}

func updateLibrarySharePermission(db interface{ Session() *gocql.Session }, libraryID, shareID, sharedTo, permission string) error {
	batch := db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		UPDATE shares SET permission = ? WHERE library_id = ? AND share_id = ?
	`, permission, libraryID, shareID)
	batch.Query(`
		UPDATE shares_by_user SET permission = ? WHERE shared_to = ? AND library_id = ?
	`, permission, sharedTo, libraryID)
	return batch.Exec()
}

func deleteLibraryShare(db interface{ Session() *gocql.Session }, libraryID, shareID, sharedTo string) error {
	batch := db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		DELETE FROM shares WHERE library_id = ? AND share_id = ?
	`, libraryID, shareID)
	batch.Query(`
		DELETE FROM shares_by_user WHERE shared_to = ? AND library_id = ?
	`, sharedTo, libraryID)
	return batch.Exec()
}

func renameGroup(db interface{ Session() *gocql.Session }, orgID, groupID, newName string, updatedAt time.Time) error {
	batch := db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		UPDATE groups SET name = ?, updated_at = ? WHERE org_id = ? AND group_id = ?
	`, newName, updatedAt, orgID, groupID)
	batch.Query(`
		UPDATE groups_by_id SET name = ? WHERE group_id = ?
	`, newName, groupID)
	if err := batch.Exec(); err != nil {
		return err
	}

	iter := db.Session().Query(`
		SELECT user_id FROM group_members WHERE group_id = ?
	`, groupID).Iter()

	var memberID string
	var memberIDs []string
	for iter.Scan(&memberID) {
		memberIDs = append(memberIDs, memberID)
	}
	if err := iter.Close(); err != nil {
		return err
	}

	var failedCount int
	for i := 0; i < len(memberIDs); i += 50 {
		end := i + 50
		if end > len(memberIDs) {
			end = len(memberIDs)
		}
		memberBatch := db.Session().Batch(gocql.UnloggedBatch)
		batchSize := end - i
		for _, userID := range memberIDs[i:end] {
			memberBatch.Query(`
				UPDATE groups_by_member SET group_name = ? WHERE org_id = ? AND user_id = ? AND group_id = ?
			`, newName, orgID, userID, groupID)
		}
		if err := memberBatch.Exec(); err != nil {
			failedCount += batchSize
		}
	}

	if failedCount > 0 {
		return fmt.Errorf("group renamed but failed to update %d member index rows", failedCount)
	}

	return nil
}

func softDeleteUser(db interface{ Session() *gocql.Session }, orgID, userID string, deletedAt time.Time) error {
	return db.Session().Query(`
		UPDATE users SET role = ?, deleted_at = ? WHERE org_id = ? AND user_id = ?
	`, "deleted", deletedAt, orgID, userID).Exec()
}

func restoreDeletedUser(db interface{ Session() *gocql.Session }, orgID, userID string) error {
	return db.Session().Query(`
		UPDATE users SET role = ?, deleted_at = ? WHERE org_id = ? AND user_id = ?
	`, "user", nil, orgID, userID).Exec()
}

func rollbackNewLibrary(db interface{ Session() *gocql.Session }, orgID, libraryID string) error {
	batch := db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		DELETE FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, libraryID)
	batch.Query(`
		DELETE FROM libraries_by_id WHERE library_id = ?
	`, libraryID)
	batch.Query(`
		DELETE FROM fs_objects WHERE library_id = ?
	`, libraryID)
	batch.Query(`
		DELETE FROM commits WHERE library_id = ?
	`, libraryID)
	return batch.Exec()
}
