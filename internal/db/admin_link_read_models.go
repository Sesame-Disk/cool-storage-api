package db

import (
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

func AdminLinkBucketDay(createdAt time.Time) string {
	return createdAt.UTC().Format("2006-01-02")
}

func IsAdminLinkType(linkType string) bool {
	return linkType == "share" || linkType == "upload"
}

func AddUpsertAdminLinkReadModelQuery(
	batch *gocql.Batch,
	token, linkType, orgID, libraryID, filePath, createdBy, permission string,
	expiresAt *time.Time,
	hasPassword, active bool,
	viewCount, uploadCount int,
	ttlSeconds int,
	createdAt time.Time,
) {
	if !IsAdminLinkType(linkType) {
		return
	}

	if ttlSeconds > 0 {
		batch.Query(`
			INSERT INTO admin_links_by_created (
				link_type, bucket_day, created_at, org_id, link_token,
				library_id, file_path, created_by, permission, expires_at,
				has_password, active, view_count, upload_count
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) USING TTL ?
		`, linkType, AdminLinkBucketDay(createdAt), createdAt, orgID, token,
			libraryID, filePath, createdBy, permission, expiresAt,
			hasPassword, active, viewCount, uploadCount, ttlSeconds)
		return
	}

	batch.Query(`
		INSERT INTO admin_links_by_created (
			link_type, bucket_day, created_at, org_id, link_token,
			library_id, file_path, created_by, permission, expires_at,
			has_password, active, view_count, upload_count
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, linkType, AdminLinkBucketDay(createdAt), createdAt, orgID, token,
		libraryID, filePath, createdBy, permission, expiresAt,
		hasPassword, active, viewCount, uploadCount)
}

func AddDeleteAdminLinkReadModelQuery(batch *gocql.Batch, linkType string, createdAt time.Time, orgID, token string) {
	if !IsAdminLinkType(linkType) {
		return
	}

	batch.Query(`
		DELETE FROM admin_links_by_created
		WHERE link_type = ? AND bucket_day = ? AND created_at = ? AND org_id = ? AND link_token = ?
	`, linkType, AdminLinkBucketDay(createdAt), createdAt, orgID, token)
}

func AddUpdateAdminLinkActiveQuery(batch *gocql.Batch, linkType string, createdAt time.Time, orgID, token string, active bool) {
	if !IsAdminLinkType(linkType) {
		return
	}

	batch.Query(`
		UPDATE admin_links_by_created
		SET active = ?
		WHERE link_type = ? AND bucket_day = ? AND created_at = ? AND org_id = ? AND link_token = ?
	`, active, linkType, AdminLinkBucketDay(createdAt), createdAt, orgID, token)
}

func AddUpdateAdminLinkCountersQuery(batch *gocql.Batch, linkType string, createdAt time.Time, orgID, token string, viewCount, uploadCount int) {
	if !IsAdminLinkType(linkType) {
		return
	}

	batch.Query(`
		UPDATE admin_links_by_created
		SET view_count = ?, upload_count = ?
		WHERE link_type = ? AND bucket_day = ? AND created_at = ? AND org_id = ? AND link_token = ?
	`, viewCount, uploadCount, linkType, AdminLinkBucketDay(createdAt), createdAt, orgID, token)
}

func SyncAdminLinkReadModel(session *gocql.Session, token string) error {
	var linkType, orgID, libraryID, filePath, createdBy, permission, passwordHash string
	var expiresAt *time.Time
	var active bool
	var viewCount, uploadCount int
	var createdAt time.Time

	if err := session.Query(`
		SELECT link_type, org_id, library_id, file_path, created_by, permission,
		       password_hash, expires_at, active, view_count, upload_count, created_at
		FROM share_links
		WHERE link_token = ?
	`, token).Scan(
		&linkType,
		&orgID,
		&libraryID,
		&filePath,
		&createdBy,
		&permission,
		&passwordHash,
		&expiresAt,
		&active,
		&viewCount,
		&uploadCount,
		&createdAt,
	); err != nil {
		return err
	}

	if !IsAdminLinkType(linkType) {
		return nil
	}

	batch := session.Batch(gocql.UnloggedBatch)
	ttlSeconds := 0
	if expiresAt != nil {
		ttlSeconds = int(time.Until(*expiresAt).Seconds())
		if ttlSeconds < 1 {
			ttlSeconds = 1
		}
	}
	AddUpsertAdminLinkReadModelQuery(
		batch,
		token,
		linkType,
		orgID,
		libraryID,
		filePath,
		createdBy,
		permission,
		expiresAt,
		passwordHash != "",
		active,
		viewCount,
		uploadCount,
		ttlSeconds,
		createdAt,
	)
	return batch.Exec()
}
