package db

import (
	"sort"
	"strings"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

type AdminOrgLinkIndexRow struct {
	LinkType  string
	Token     string
	CreatedBy string
	CreatedAt time.Time
	Active    bool
}

func AdminLinkBucketDay(createdAt time.Time) string {
	return createdAt.UTC().Format("2006-01-02")
}

func IsAdminLinkType(linkType string) bool {
	return linkType == "share" || linkType == "upload"
}

func AdminLinkObjName(filePath, repoName string) string {
	if filePath == "/" {
		return repoName
	}
	trimmed := strings.TrimSuffix(filePath, "/")
	if idx := strings.LastIndex(trimmed, "/"); idx >= 0 {
		return trimmed[idx+1:]
	}
	return filePath
}

func ResolveAdminLinkDisplayFields(session *gocql.Session, orgID, libraryID, filePath, createdBy string) (string, string, string, string) {
	var repoName string
	_ = session.Query(`SELECT name FROM libraries WHERE org_id = ? AND library_id = ?`, orgID, libraryID).Scan(&repoName)
	if repoName == "" {
		repoName = "Unknown Library"
	}

	var creatorEmail, creatorName string
	_ = session.Query(`SELECT email, name FROM users WHERE org_id = ? AND user_id = ?`, orgID, createdBy).Scan(&creatorEmail, &creatorName)
	if creatorEmail == "" {
		creatorEmail = createdBy
	}
	if creatorName == "" {
		creatorName = creatorEmail
	}

	return repoName, AdminLinkObjName(filePath, repoName), creatorEmail, creatorName
}

func AdminOrgLinkCountDelta(delta int) int {
	return delta
}

func AdjustAdminOrgLinkCount(session *gocql.Session, orgID, linkType string, delta int) error {
	if !IsAdminLinkType(linkType) || delta == 0 {
		return nil
	}
	return session.Query(`
		UPDATE admin_link_counts_by_org
		SET link_count = link_count + ?
		WHERE org_id = ? AND link_type = ?
	`, int64(delta), orgID, linkType).Exec()
}

func InvalidateAdminOrgLinkCount(session *gocql.Session, orgID, linkType string) error {
	if !IsAdminLinkType(linkType) {
		return nil
	}
	return session.Query(`DELETE FROM admin_link_counts_by_org WHERE org_id = ? AND link_type = ?`, orgID, linkType).Exec()
}

func BestEffortAdjustAdminOrgLinkCount(session *gocql.Session, orgID, linkType string, delta int) {
	if err := AdjustAdminOrgLinkCount(session, orgID, linkType, delta); err != nil {
		_ = InvalidateAdminOrgLinkCount(session, orgID, linkType)
	}
}

func AddUpsertAdminLinkReadModelQuery(
	batch *gocql.Batch,
	token, linkType, orgID, libraryID, filePath, createdBy, permission string,
	repoName, objName, creatorEmail, creatorName string,
	expiresAt *time.Time,
	hasPassword, active bool,
	viewCount, uploadCount int,
	ttlSeconds int,
	createdAt time.Time,
) {
	if !IsAdminLinkType(linkType) {
		return
	}

	bucketDay := AdminLinkBucketDay(createdAt)
	batch.Query(`INSERT INTO admin_link_buckets (link_type, bucket_day) VALUES (?, ?)`, linkType, bucketDay)
	batch.Query(`INSERT INTO admin_link_buckets_by_org (org_id, link_type, bucket_day) VALUES (?, ?, ?)`, orgID, linkType, bucketDay)

	if ttlSeconds > 0 {
		batch.Query(`
			INSERT INTO admin_links_by_created (
				link_type, bucket_day, created_at, org_id, link_token,
				library_id, repo_name, file_path, obj_name, created_by,
				creator_email, creator_name, permission, expires_at,
				has_password, active, view_count, upload_count
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) USING TTL ?
		`, linkType, bucketDay, createdAt, orgID, token,
			libraryID, repoName, filePath, objName, createdBy,
			creatorEmail, creatorName, permission, expiresAt,
			hasPassword, active, viewCount, uploadCount, ttlSeconds)
		batch.Query(`
			INSERT INTO admin_links_by_org_created (
				org_id, link_type, bucket_day, created_at, link_token,
				library_id, repo_name, file_path, obj_name, created_by,
				creator_email, creator_name, permission, expires_at,
				has_password, active, view_count, upload_count
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) USING TTL ?
		`, orgID, linkType, bucketDay, createdAt, token,
			libraryID, repoName, filePath, objName, createdBy,
			creatorEmail, creatorName, permission, expiresAt,
			hasPassword, active, viewCount, uploadCount, ttlSeconds)
		return
	}

	batch.Query(`
		INSERT INTO admin_links_by_created (
			link_type, bucket_day, created_at, org_id, link_token,
			library_id, repo_name, file_path, obj_name, created_by,
			creator_email, creator_name, permission, expires_at,
			has_password, active, view_count, upload_count
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, linkType, bucketDay, createdAt, orgID, token,
		libraryID, repoName, filePath, objName, createdBy,
		creatorEmail, creatorName, permission, expiresAt,
		hasPassword, active, viewCount, uploadCount)
	batch.Query(`
		INSERT INTO admin_links_by_org_created (
			org_id, link_type, bucket_day, created_at, link_token,
			library_id, repo_name, file_path, obj_name, created_by,
			creator_email, creator_name, permission, expires_at,
			has_password, active, view_count, upload_count
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, orgID, linkType, bucketDay, createdAt, token,
		libraryID, repoName, filePath, objName, createdBy,
		creatorEmail, creatorName, permission, expiresAt,
		hasPassword, active, viewCount, uploadCount)
}

func AddDeleteAdminLinkReadModelQuery(batch *gocql.Batch, linkType string, createdAt time.Time, orgID, token string) {
	if !IsAdminLinkType(linkType) {
		return
	}
	bucketDay := AdminLinkBucketDay(createdAt)

	batch.Query(`
		DELETE FROM admin_links_by_created
		WHERE link_type = ? AND bucket_day = ? AND created_at = ? AND org_id = ? AND link_token = ?
	`, linkType, bucketDay, createdAt, orgID, token)
	batch.Query(`
		DELETE FROM admin_links_by_org_created
		WHERE org_id = ? AND link_type = ? AND bucket_day = ? AND created_at = ? AND link_token = ?
	`, orgID, linkType, bucketDay, createdAt, token)
}

func AddUpdateAdminLinkActiveQuery(batch *gocql.Batch, linkType string, createdAt time.Time, orgID, token string, active bool) {
	if !IsAdminLinkType(linkType) {
		return
	}
	bucketDay := AdminLinkBucketDay(createdAt)

	batch.Query(`
		UPDATE admin_links_by_created
		SET active = ?
		WHERE link_type = ? AND bucket_day = ? AND created_at = ? AND org_id = ? AND link_token = ?
	`, active, linkType, bucketDay, createdAt, orgID, token)
	batch.Query(`
		UPDATE admin_links_by_org_created
		SET active = ?
		WHERE org_id = ? AND link_type = ? AND bucket_day = ? AND created_at = ? AND link_token = ?
	`, active, orgID, linkType, bucketDay, createdAt, token)
}

func AddUpdateAdminLinkCountersQuery(batch *gocql.Batch, linkType string, createdAt time.Time, orgID, token string, viewCount, uploadCount int) {
	if !IsAdminLinkType(linkType) {
		return
	}
	bucketDay := AdminLinkBucketDay(createdAt)

	batch.Query(`
		UPDATE admin_links_by_created
		SET view_count = ?, upload_count = ?
		WHERE link_type = ? AND bucket_day = ? AND created_at = ? AND org_id = ? AND link_token = ?
	`, viewCount, uploadCount, linkType, bucketDay, createdAt, orgID, token)
	batch.Query(`
		UPDATE admin_links_by_org_created
		SET view_count = ?, upload_count = ?
		WHERE org_id = ? AND link_type = ? AND bucket_day = ? AND created_at = ? AND link_token = ?
	`, viewCount, uploadCount, orgID, linkType, bucketDay, createdAt, token)
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
	repoName, objName, creatorEmail, creatorName := ResolveAdminLinkDisplayFields(session, orgID, libraryID, filePath, createdBy)

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
		repoName,
		objName,
		creatorEmail,
		creatorName,
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

func listAdminLinkBucketsByOrg(session *gocql.Session, orgID, linkType string) ([]string, error) {
	iter := session.Query(`SELECT bucket_day FROM admin_link_buckets_by_org WHERE org_id = ? AND link_type = ?`, orgID, linkType).Iter()
	var buckets []string
	var bucketDay string
	for iter.Scan(&bucketDay) {
		buckets = append(buckets, bucketDay)
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return buckets, nil
}

func ListAdminOrgLinkIndexRows(session *gocql.Session, orgID string) ([]AdminOrgLinkIndexRow, error) {
	var rows []AdminOrgLinkIndexRow
	for _, linkType := range []string{"share", "upload"} {
		buckets, err := listAdminLinkBucketsByOrg(session, orgID, linkType)
		if err != nil {
			return nil, err
		}
		for _, bucketDay := range buckets {
			iter := session.Query(`
				SELECT created_at, link_token, created_by, active
				FROM admin_links_by_org_created
				WHERE org_id = ? AND link_type = ? AND bucket_day = ?
			`, orgID, linkType, bucketDay).Iter()
			var row AdminOrgLinkIndexRow
			for iter.Scan(&row.CreatedAt, &row.Token, &row.CreatedBy, &row.Active) {
				row.LinkType = linkType
				rows = append(rows, row)
				row = AdminOrgLinkIndexRow{}
			}
			if err := iter.Close(); err != nil {
				return nil, err
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			if rows[i].LinkType == rows[j].LinkType {
				return rows[i].Token < rows[j].Token
			}
			return rows[i].LinkType < rows[j].LinkType
		}
		return rows[i].CreatedAt.After(rows[j].CreatedAt)
	})
	return rows, nil
}

func recountAdminOrgLinks(session *gocql.Session, orgID, linkType string) (int, error) {
	buckets, err := listAdminLinkBucketsByOrg(session, orgID, linkType)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, bucketDay := range buckets {
		iter := session.Query(`
			SELECT link_token FROM admin_links_by_org_created
			WHERE org_id = ? AND link_type = ? AND bucket_day = ?
		`, orgID, linkType, bucketDay).Iter()
		var token string
		for iter.Scan(&token) {
			count++
		}
		if err := iter.Close(); err != nil {
			return 0, err
		}
	}
	return count, nil
}

func CountAdminOrgLinks(session *gocql.Session, orgID, linkType string) (int, error) {
	if !IsAdminLinkType(linkType) {
		return 0, nil
	}

	var count int64
	err := session.Query(`SELECT link_count FROM admin_link_counts_by_org WHERE org_id = ? AND link_type = ?`, orgID, linkType).Scan(&count)
	if err == nil {
		if count >= 0 {
			return int(count), nil
		}
		_ = InvalidateAdminOrgLinkCount(session, orgID, linkType)
	}
	if err != nil && err != gocql.ErrNotFound {
		return 0, err
	}

	recount, recountErr := recountAdminOrgLinks(session, orgID, linkType)
	if recountErr != nil {
		return 0, recountErr
	}
	if recount > 0 {
		BestEffortAdjustAdminOrgLinkCount(session, orgID, linkType, recount)
	}
	return recount, nil
}
