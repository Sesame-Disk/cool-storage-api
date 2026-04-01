package db

import (
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

type ShareReadModelRow struct {
	OrgID         string
	LibraryID     string
	ShareID       string
	SharedBy      string
	SharedByEmail string
	SharedByName  string
	SharedTo      string
	SharedToType  string
	Permission    string
	CreatedAt     time.Time
	ExpiresAt     *time.Time
	RepoName      string
	Encrypted     bool
	SizeBytes     int64
}

func ReadShareReadModelRow(session *gocql.Session, libraryID, shareID string) (ShareReadModelRow, error) {
	row := ShareReadModelRow{LibraryID: libraryID, ShareID: shareID}
	var expiresAt *time.Time
	err := session.Query(`
		SELECT shared_by, shared_to, shared_to_type, permission, created_at, expires_at
		FROM shares WHERE library_id = ? AND share_id = ?
	`, libraryID, shareID).Scan(&row.SharedBy, &row.SharedTo, &row.SharedToType, &row.Permission, &row.CreatedAt, &expiresAt)
	if err != nil {
		return ShareReadModelRow{}, err
	}
	row.ExpiresAt = expiresAt
	if err := session.Query(`
		SELECT org_id, name, encrypted FROM libraries_by_id WHERE library_id = ?
	`, libraryID).Scan(&row.OrgID, &row.RepoName, &row.Encrypted); err != nil {
		return ShareReadModelRow{}, err
	}
	_ = session.Query(`SELECT size_bytes FROM libraries WHERE org_id = ? AND library_id = ?`, row.OrgID, libraryID).Scan(&row.SizeBytes)
	row.SharedByEmail, row.SharedByName = ResolveAdminLibraryOwnerFields(session, row.OrgID, row.SharedBy)
	return row, nil
}

func AddUpsertShareReadModelQuery(batch *gocql.Batch, row ShareReadModelRow) {
	if row.SharedToType == "group" {
		batch.Query(`
			INSERT INTO shares_by_group (
				org_id, group_id, created_at, library_id, share_id, permission,
				shared_by, shared_by_email, shared_by_name, repo_name, encrypted, size_bytes
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, row.OrgID, row.SharedTo, row.CreatedAt, row.LibraryID, row.ShareID, row.Permission,
			row.SharedBy, row.SharedByEmail, row.SharedByName, row.RepoName, row.Encrypted, row.SizeBytes)
	}
	if row.SharedToType == "user" {
		batch.Query(`
			INSERT INTO shares_by_user_org (
				org_id, user_id, created_at, library_id, share_id, permission,
				shared_by, shared_by_email, shared_by_name, repo_name, encrypted, size_bytes
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, row.OrgID, row.SharedTo, row.CreatedAt, row.LibraryID, row.ShareID, row.Permission,
			row.SharedBy, row.SharedByEmail, row.SharedByName, row.RepoName, row.Encrypted, row.SizeBytes)
	}
	batch.Query(`
		INSERT INTO shares_by_creator (
			org_id, shared_by, created_at, library_id, share_id, shared_to, shared_to_type, permission, expires_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, row.OrgID, row.SharedBy, row.CreatedAt, row.LibraryID, row.ShareID, row.SharedTo, row.SharedToType, row.Permission, row.ExpiresAt)
	batch.Query(`
		INSERT INTO shares_by_recipient (
			org_id, shared_to_type, shared_to, created_at, library_id, share_id, shared_by, permission, expires_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, row.OrgID, row.SharedToType, row.SharedTo, row.CreatedAt, row.LibraryID, row.ShareID, row.SharedBy, row.Permission, row.ExpiresAt)
}

func AddDeleteShareReadModelQuery(batch *gocql.Batch, row ShareReadModelRow) {
	if row.SharedToType == "group" {
		batch.Query(`
			DELETE FROM shares_by_group
			WHERE org_id = ? AND group_id = ? AND created_at = ? AND library_id = ? AND share_id = ?
		`, row.OrgID, row.SharedTo, row.CreatedAt, row.LibraryID, row.ShareID)
	}
	if row.SharedToType == "user" {
		batch.Query(`
			DELETE FROM shares_by_user_org
			WHERE org_id = ? AND user_id = ? AND created_at = ? AND library_id = ? AND share_id = ?
		`, row.OrgID, row.SharedTo, row.CreatedAt, row.LibraryID, row.ShareID)
	}
	batch.Query(`
		DELETE FROM shares_by_creator
		WHERE org_id = ? AND shared_by = ? AND created_at = ? AND library_id = ? AND share_id = ?
	`, row.OrgID, row.SharedBy, row.CreatedAt, row.LibraryID, row.ShareID)
	batch.Query(`
		DELETE FROM shares_by_recipient
		WHERE org_id = ? AND shared_to_type = ? AND shared_to = ? AND created_at = ? AND library_id = ? AND share_id = ?
	`, row.OrgID, row.SharedToType, row.SharedTo, row.CreatedAt, row.LibraryID, row.ShareID)
}

func SyncShareReadModel(session *gocql.Session, libraryID, shareID string) error {
	row, err := ReadShareReadModelRow(session, libraryID, shareID)
	if err != nil {
		return err
	}
	batch := session.Batch(gocql.LoggedBatch)
	AddUpsertShareReadModelQuery(batch, row)
	return batch.Exec()
}