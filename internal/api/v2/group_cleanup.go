package v2

import (
	"log"
	"time"

	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"
)

type groupDeleteState struct {
	projectionRow dbpkg.AdminGroupProjectionRow
	memberIDs     []string
	shareRows     []dbpkg.ShareReadModelRow
}

func listGroupMemberIDs(db interface{ Session() *gocql.Session }, groupID string) ([]string, error) {
	iter := db.Session().Query(`SELECT user_id FROM group_members WHERE group_id = ?`, groupID).Iter()
	var memberID string
	var memberIDs []string
	for iter.Scan(&memberID) {
		memberIDs = append(memberIDs, memberID)
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	if memberIDs == nil {
		memberIDs = []string{}
	}
	return memberIDs, nil
}

func addCleanupGroupsByMemberQueries(batch *gocql.Batch, orgID, groupID string, memberIDs []string) {
	for _, userID := range memberIDs {
		batch.Query(`DELETE FROM groups_by_member WHERE org_id = ? AND user_id = ? AND group_id = ?`, orgID, userID, groupID)
	}
}

func collectGroupShareReadModelRows(database *dbpkg.DB, groupID string) ([]dbpkg.ShareReadModelRow, error) {
	iter := database.Session().Query(`
		SELECT library_id FROM shares_by_user WHERE shared_to = ?
	`, groupID).Iter()

	var libraryID string
	var libraryIDs []string
	for iter.Scan(&libraryID) {
		libraryIDs = append(libraryIDs, libraryID)
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}

	var shareRows []dbpkg.ShareReadModelRow
	for _, libID := range libraryIDs {
		shareIter := database.Session().Query(`
			SELECT share_id, shared_to FROM shares WHERE library_id = ?
		`, libID).Iter()

		var shareID, sharedTo string
		for shareIter.Scan(&shareID, &sharedTo) {
			if sharedTo != groupID {
				continue
			}
			row, err := dbpkg.ReadShareReadModelRow(database.Session(), libID, shareID)
			if err != nil {
				_ = shareIter.Close()
				return nil, err
			}
			if row.SharedToType != "group" || row.SharedTo != groupID {
				continue
			}
			shareRows = append(shareRows, row)
		}
		if err := shareIter.Close(); err != nil {
			return nil, err
		}
	}

	if shareRows == nil {
		shareRows = []dbpkg.ShareReadModelRow{}
	}
	return shareRows, nil
}

func addDeleteGroupShareQueries(batch *gocql.Batch, shareRows []dbpkg.ShareReadModelRow) {
	for _, row := range shareRows {
		batch.Query(`DELETE FROM shares WHERE library_id = ? AND share_id = ?`, row.LibraryID, row.ShareID)
		batch.Query(`DELETE FROM shares_by_user WHERE shared_to = ? AND library_id = ?`, row.SharedTo, row.LibraryID)
		dbpkg.AddDeleteShareReadModelQuery(batch, row)
	}
}

func loadGroupDeleteState(database *dbpkg.DB, orgID, groupID string) (groupDeleteState, error) {
	projectionRow, err := readAdminGroupReadModelRow(database, orgID, groupID)
	if err != nil {
		return groupDeleteState{}, err
	}

	memberIDs, err := listGroupMemberIDs(database, groupID)
	if err != nil {
		return groupDeleteState{}, err
	}

	shareRows, err := collectGroupShareReadModelRows(database, groupID)
	if err != nil {
		return groupDeleteState{}, err
	}

	return groupDeleteState{
		projectionRow: projectionRow,
		memberIDs:     memberIDs,
		shareRows:     shareRows,
	}, nil
}

func addDeleteGroupMutationQueries(batch *gocql.Batch, orgID, groupID string, state groupDeleteState) {
	batch.Query(`DELETE FROM groups WHERE org_id = ? AND group_id = ?`, orgID, groupID)
	batch.Query(`DELETE FROM groups_by_id WHERE group_id = ?`, groupID)
	batch.Query(`DELETE FROM group_members WHERE group_id = ?`, groupID)
	addCleanupGroupsByMemberQueries(batch, orgID, groupID, state.memberIDs)
	dbpkg.AddDeleteAdminGroupReadModelQuery(batch, state.projectionRow)
	addDeleteGroupShareQueries(batch, state.shareRows)
}

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
	batch := db.Session().Batch(gocql.LoggedBatch)
	addCleanupGroupsByMemberQueries(batch, orgID, groupID, memberIDs)
	if len(batch.Entries) == 0 {
		return nil
	}
	return batch.Exec()
}

func cleanupGroupShares(database *dbpkg.DB, groupID uuid.UUID) error {
	shareRows, err := collectGroupShareReadModelRows(database, groupID.String())
	if err != nil {
		return err
	}

	batch := database.Session().Batch(gocql.LoggedBatch)
	addDeleteGroupShareQueries(batch, shareRows)
	if len(batch.Entries) == 0 {
		return nil
	}
	if err := batch.Exec(); err != nil {
		return err
	}

	if len(shareRows) > 0 {
		log.Printf("[Group cleanup] Cleaned up %d group shares for deleted group %s", len(shareRows), groupID)
	}
	return nil
}
