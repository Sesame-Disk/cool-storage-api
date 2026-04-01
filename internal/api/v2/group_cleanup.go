package v2

import (
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

// bulkMemberInsert holds the data needed to insert one group member.
type bulkMemberInsert struct {
	UserID string
	Role   string
}

// bulkUpsertGroupMembers inserts multiple members into group_members + groups_by_member
// using UnloggedBatch in chunks of 25 members (50 statements per batch).
func bulkUpsertGroupMembers(db interface{ Session() *gocql.Session }, orgID, groupID, groupName string, members []bulkMemberInsert, addedAt time.Time) error {
	const chunkSize = 25 // 25 members × 2 statements = 50 statements per batch
	for i := 0; i < len(members); i += chunkSize {
		end := i + chunkSize
		if end > len(members) {
			end = len(members)
		}
		batch := db.Session().Batch(gocql.UnloggedBatch)
		for _, m := range members[i:end] {
			batch.Query(`INSERT INTO group_members (group_id, user_id, role, added_at) VALUES (?, ?, ?, ?)`,
				groupID, m.UserID, m.Role, addedAt)
			batch.Query(`INSERT INTO groups_by_member (org_id, user_id, group_id, group_name, role, added_at) VALUES (?, ?, ?, ?, ?, ?)`,
				orgID, m.UserID, groupID, groupName, m.Role, addedAt)
		}
		if err := batch.Exec(); err != nil {
			return err
		}
	}
	return nil
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

		var shareID, sharedTo string
		for shareIter.Scan(&shareID, &sharedTo) {
			if sharedTo != groupIDStr {
				continue
			}
			if err := deleteLibraryShare(database, libID, shareID, groupIDStr); err != nil {
				_ = shareIter.Close()
				return err
			}
		}
		if err := shareIter.Close(); err != nil {
			return err
		}
	}

	if len(libraryIDs) > 0 {
		log.Printf("[Group cleanup] Cleaned up %d group shares for deleted group %s", len(libraryIDs), groupID)
	}
	return nil
}
