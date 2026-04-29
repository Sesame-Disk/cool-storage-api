package v2

import (
	"errors"
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

func collectGroupShareReadModelRows(database *dbpkg.DB, orgID, groupID string) ([]dbpkg.ShareReadModelRow, error) {
	iter := database.Session().Query(`
		SELECT created_at, library_id, share_id, permission,
		       shared_by, shared_by_email, shared_by_name, repo_name, encrypted, size_bytes
		FROM shares_by_group WHERE org_id = ? AND group_id = ?
	`, orgID, groupID).Iter()

	var shareRows []dbpkg.ShareReadModelRow
	var row dbpkg.ShareReadModelRow
	for iter.Scan(
		&row.CreatedAt,
		&row.LibraryID,
		&row.ShareID,
		&row.Permission,
		&row.SharedBy,
		&row.SharedByEmail,
		&row.SharedByName,
		&row.RepoName,
		&row.Encrypted,
		&row.SizeBytes,
	) {
		row.OrgID = orgID
		row.SharedTo = groupID
		row.SharedToType = "group"
		shareRows = append(shareRows, row)
		row = dbpkg.ShareReadModelRow{}
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}

	shareRows, err := dbpkg.HydrateShareReadModelRows(database.Session(), shareRows)
	if err != nil {
		return nil, err
	}

	if shareRows == nil {
		shareRows = []dbpkg.ShareReadModelRow{}
	}
	return shareRows, nil
}

func addDeleteGroupShareQueries(batch *gocql.Batch, shareRows []dbpkg.ShareReadModelRow) {
	for _, row := range shareRows {
		batch.Query(`DELETE FROM shares WHERE library_id = ? AND share_id = ?`, row.LibraryID, row.ShareID)
		if row.ExpiresAt != nil {
			dbpkg.AddDeleteShareExpiryQuery(batch, row.ShareID, *row.ExpiresAt, row.OrgID, row.LibraryID)
		}
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

	shareRows, err := collectGroupShareReadModelRows(database, orgID, groupID)
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
	var orgID string
	if err := database.Session().Query(`SELECT org_id FROM groups_by_id WHERE group_id = ?`, groupID.String()).Scan(&orgID); err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return nil
		}
		return err
	}
	shareRows, err := collectGroupShareReadModelRows(database, orgID, groupID.String())
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
