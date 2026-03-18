package v2

import (
	"fmt"
	"log"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"
)

func upsertGroupMember(db interface{ Session() *gocql.Session }, orgID, groupID, userID, groupName, role string, addedAt time.Time) error {
	batch := db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		INSERT INTO group_members (group_id, user_id, role, added_at)
		VALUES (?, ?, ?, ?)
	`, groupID, userID, role, addedAt)
	batch.Query(`
		INSERT INTO groups_by_member (org_id, user_id, group_id, group_name, role, added_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, orgID, userID, groupID, groupName, role, addedAt)
	return batch.Exec()
}

func deleteGroupMember(db interface{ Session() *gocql.Session }, orgID, groupID, userID string) error {
	batch := db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		DELETE FROM group_members WHERE group_id = ? AND user_id = ?
	`, groupID, userID)
	batch.Query(`
		DELETE FROM groups_by_member WHERE org_id = ? AND user_id = ? AND group_id = ?
	`, orgID, userID, groupID)
	return batch.Exec()
}

func cleanupGroupsByMember(db interface{ Session() *gocql.Session }, orgID, groupID string, memberIDs []string) error {
	for i := 0; i < len(memberIDs); i += 50 {
		end := i + 50
		if end > len(memberIDs) {
			end = len(memberIDs)
		}
		memberBatch := db.Session().Batch(gocql.UnloggedBatch)
		for _, userID := range memberIDs[i:end] {
			memberBatch.Query(`DELETE FROM groups_by_member WHERE org_id = ? AND user_id = ? AND group_id = ?`,
				orgID, userID, groupID)
		}
		if err := memberBatch.Exec(); err != nil {
			return err
		}
	}
	return nil
}

func cleanupGroupShares(database *db.DB, groupID uuid.UUID) error {
	iter := database.Session().Query(`
		SELECT library_id FROM shares_by_user WHERE shared_to = ?
	`, groupID.String()).Iter()

	var libraryID string
	var libraryIDs []string
	for iter.Scan(&libraryID) {
		libraryIDs = append(libraryIDs, libraryID)
	}
	if err := iter.Close(); err != nil {
		return err
	}

	groupIDStr := groupID.String()
	for _, libID := range libraryIDs {
		shareIter := database.Session().Query(`
			SELECT share_id, shared_to FROM shares WHERE library_id = ?
		`, libID).Iter()

		batch := database.Session().Batch(gocql.LoggedBatch)
		var shareID, sharedTo string
		for shareIter.Scan(&shareID, &sharedTo) {
			if sharedTo != groupIDStr {
				continue
			}
			batch.Query(`DELETE FROM shares WHERE library_id = ? AND share_id = ?`,
				libID, shareID)
		}
		if err := shareIter.Close(); err != nil {
			return err
		}

		batch.Query(`DELETE FROM shares_by_user WHERE shared_to = ? AND library_id = ?`,
			groupIDStr, libID)
		if err := batch.Exec(); err != nil {
			return fmt.Errorf("failed to delete shares for library %s and group %s: %w", libID, groupID, err)
		}
	}

	if len(libraryIDs) > 0 {
		log.Printf("[Group cleanup] Cleaned up %d group shares for deleted group %s", len(libraryIDs), groupID)
	}
	return nil
}
