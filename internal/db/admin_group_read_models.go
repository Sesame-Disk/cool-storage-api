package db

import (
	"sort"
	"strings"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

type AdminGroupProjectionRow struct {
	OrgID         string
	GroupID       string
	Name          string
	CreatorID     string
	OwnerEmail    string
	OwnerName     string
	ParentGroupID string
	IsDepartment  bool
	CreatedAt     time.Time
}

func optionalGroupParentIDString(parentGroupID gocql.UUID) string {
	if parentGroupID == (gocql.UUID{}) {
		return ""
	}
	return parentGroupID.String()
}

func AdminGroupBucketDay(createdAt time.Time) string {
	return createdAt.UTC().Format("2006-01-02")
}

func ResolveAdminGroupOwnerFields(session *gocql.Session, orgID, creatorID string) (string, string) {
	var ownerEmail, ownerName string
	_ = session.Query(`SELECT email, name FROM users WHERE org_id = ? AND user_id = ?`, orgID, creatorID).Scan(&ownerEmail, &ownerName)
	if ownerEmail == "" {
		ownerEmail = creatorID
	}
	if ownerName == "" {
		ownerName = strings.Split(ownerEmail, "@")[0]
	}
	return ownerEmail, ownerName
}

func ReadAdminGroupProjectionRow(session *gocql.Session, orgID, groupID string) (AdminGroupProjectionRow, error) {
	row := AdminGroupProjectionRow{OrgID: orgID, GroupID: groupID}
	var parentGroupID gocql.UUID
	err := session.Query(`
		SELECT name, creator_id, parent_group_id, is_department, created_at
		FROM groups WHERE org_id = ? AND group_id = ?
	`, orgID, groupID).Scan(&row.Name, &row.CreatorID, &parentGroupID, &row.IsDepartment, &row.CreatedAt)
	if err != nil {
		return AdminGroupProjectionRow{}, err
	}
	row.ParentGroupID = optionalGroupParentIDString(parentGroupID)
	row.OwnerEmail, row.OwnerName = ResolveAdminGroupOwnerFields(session, orgID, row.CreatorID)
	return row, nil
}

func AddUpsertAdminGroupReadModelQuery(batch *gocql.Batch, row AdminGroupProjectionRow) {
	bucketDay := AdminGroupBucketDay(row.CreatedAt)
	var parentGroupID interface{}
	if row.ParentGroupID != "" {
		parentGroupID = row.ParentGroupID
	}
	batch.Query(`INSERT INTO group_admin_global_buckets (bucket_day) VALUES (?)`, bucketDay)
	batch.Query(`
		INSERT INTO groups_admin_global_by_created (
			bucket_day, created_at, org_id, group_id, name, creator_id, owner_email,
			owner_name, parent_group_id, is_department
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, bucketDay, row.CreatedAt, row.OrgID, row.GroupID, row.Name, row.CreatorID, row.OwnerEmail,
		row.OwnerName, parentGroupID, row.IsDepartment)
}

func AddDeleteAdminGroupReadModelQuery(batch *gocql.Batch, row AdminGroupProjectionRow) {
	bucketDay := AdminGroupBucketDay(row.CreatedAt)
	batch.Query(`
		DELETE FROM groups_admin_global_by_created
		WHERE bucket_day = ? AND created_at = ? AND org_id = ? AND group_id = ?
	`, bucketDay, row.CreatedAt, row.OrgID, row.GroupID)
}

func SyncAdminGroupReadModel(session *gocql.Session, orgID, groupID string) error {
	row, err := ReadAdminGroupProjectionRow(session, orgID, groupID)
	if err != nil {
		return err
	}
	batch := session.Batch(gocql.LoggedBatch)
	AddUpsertAdminGroupReadModelQuery(batch, row)
	return batch.Exec()
}

func DeleteAdminGroupReadModel(session *gocql.Session, row AdminGroupProjectionRow) error {
	batch := session.Batch(gocql.LoggedBatch)
	AddDeleteAdminGroupReadModelQuery(batch, row)
	return batch.Exec()
}

func ListAdminGroupBucketDays(session *gocql.Session) ([]string, error) {
	iter := session.Query(`SELECT bucket_day FROM group_admin_global_buckets`).Iter()
	var buckets []string
	var bucketDay string
	for iter.Scan(&bucketDay) {
		buckets = append(buckets, bucketDay)
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	sort.Slice(buckets, func(i, j int) bool {
		return buckets[i] > buckets[j]
	})
	return buckets, nil
}

func ListAdminGlobalGroupRows(session *gocql.Session) ([]AdminGroupProjectionRow, error) {
	buckets, err := ListAdminGroupBucketDays(session)
	if err != nil {
		return nil, err
	}

	var rows []AdminGroupProjectionRow
	for _, bucketDay := range buckets {
		iter := session.Query(`
			SELECT created_at, org_id, group_id, name, creator_id, owner_email,
			       owner_name, parent_group_id, is_department
			FROM groups_admin_global_by_created
			WHERE bucket_day = ?
		`, bucketDay).Iter()

		var row AdminGroupProjectionRow
		var parentGroupID gocql.UUID
		for iter.Scan(&row.CreatedAt, &row.OrgID, &row.GroupID, &row.Name, &row.CreatorID, &row.OwnerEmail,
			&row.OwnerName, &parentGroupID, &row.IsDepartment) {
			row.ParentGroupID = optionalGroupParentIDString(parentGroupID)
			rows = append(rows, row)
			row = AdminGroupProjectionRow{}
			parentGroupID = gocql.UUID{}
		}
		if err := iter.Close(); err != nil {
			return nil, err
		}
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			if rows[i].OrgID == rows[j].OrgID {
				return rows[i].GroupID < rows[j].GroupID
			}
			return rows[i].OrgID < rows[j].OrgID
		}
		return rows[i].CreatedAt.After(rows[j].CreatedAt)
	})
	return rows, nil
}
