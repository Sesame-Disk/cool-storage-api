package db

import (
	"sort"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

const defaultAdminProjectionStatus = "active"

type AdminOrganizationProjectionRow struct {
	OrgID        string
	Name         string
	OwnerEmail   string
	OwnerName    string
	Status       string
	Plan         string
	StorageQuota int64
	DeletedAt    *time.Time
	UsersCount   int
	CreatedAt    time.Time
}

type AdminOrganizationProjectionState struct {
	OrgID     string
	Status    string
	CreatedAt time.Time
}

type AdminUserProjectionRow struct {
	OrgID       string
	UserID      string
	Email       string
	Name        string
	Role        string
	Status      string
	QuotaBytes  int64
	QuotaUsage  int64
	LastLoginAt *time.Time
	CreatedAt   time.Time
}

type AdminUserProjectionState struct {
	UserID    string
	OrgID     string
	Status    string
	CreatedAt time.Time
}

func normalizeAdminProjectionStatus(status string) string {
	if status == "" {
		return defaultAdminProjectionStatus
	}
	return status
}

func AdminOrganizationBucketDay(createdAt time.Time) string {
	return createdAt.UTC().Format("2006-01-02")
}

func AdminUserBucketDay(createdAt time.Time) string {
	return createdAt.UTC().Format("2006-01-02")
}

func ResolveAdminOrganizationOwnerFields(session *gocql.Session, orgID string) (string, string) {
	var email, name, role string
	var firstEmail, firstName string
	first := true
	iter := session.Query(`
		SELECT email, name, role FROM users WHERE org_id = ?
	`, orgID).Iter()
	for iter.Scan(&email, &name, &role) {
		if first {
			firstEmail, firstName = email, name
			first = false
		}
		if role == "superadmin" || role == "owner" || role == "admin" {
			_ = iter.Close()
			return email, name
		}
	}
	_ = iter.Close()
	return firstEmail, firstName
}

func CountAdminOrganizationUsers(session *gocql.Session, orgID string) (int, error) {
	count := 0
	iter := session.Query(`SELECT user_id FROM users WHERE org_id = ?`, orgID).Iter()
	var userID string
	for iter.Scan(&userID) {
		count++
	}
	if err := iter.Close(); err != nil {
		return 0, err
	}
	return count, nil
}

func ReadAdminOrganizationProjectionRow(session *gocql.Session, orgID string) (AdminOrganizationProjectionRow, error) {
	row := AdminOrganizationProjectionRow{OrgID: orgID}
	var deletedAt *time.Time
	err := session.Query(`
		SELECT name, status, plan, storage_quota, deleted_at, created_at
		FROM organizations WHERE org_id = ?
	`, orgID).Scan(&row.Name, &row.Status, &row.Plan, &row.StorageQuota, &deletedAt, &row.CreatedAt)
	if err != nil {
		return AdminOrganizationProjectionRow{}, err
	}
	row.Status = normalizeAdminProjectionStatus(row.Status)
	row.DeletedAt = deletedAt
	row.OwnerEmail, row.OwnerName = ResolveAdminOrganizationOwnerFields(session, orgID)
	row.UsersCount, err = CountAdminOrganizationUsers(session, orgID)
	if err != nil {
		return AdminOrganizationProjectionRow{}, err
	}
	return row, nil
}

func ReadAdminOrganizationProjectionState(session *gocql.Session, orgID string) (AdminOrganizationProjectionState, error) {
	state := AdminOrganizationProjectionState{OrgID: orgID}
	err := session.Query(`
		SELECT status, created_at FROM organization_admin_projection_state WHERE org_id = ?
	`, orgID).Scan(&state.Status, &state.CreatedAt)
	if err != nil {
		return AdminOrganizationProjectionState{}, err
	}
	state.Status = normalizeAdminProjectionStatus(state.Status)
	return state, nil
}

func addDeleteAdminOrganizationProjectionEntryQuery(batch *gocql.Batch, orgID, status string, createdAt time.Time) {
	status = normalizeAdminProjectionStatus(status)
	bucketDay := AdminOrganizationBucketDay(createdAt)
	batch.Query(`
		DELETE FROM organizations_admin_by_created
		WHERE bucket_day = ? AND created_at = ? AND org_id = ?
	`, bucketDay, createdAt, orgID)
	batch.Query(`
		DELETE FROM organizations_admin_by_status_created
		WHERE status = ? AND bucket_day = ? AND created_at = ? AND org_id = ?
	`, status, bucketDay, createdAt, orgID)
}

func AddUpsertAdminOrganizationReadModelQuery(batch *gocql.Batch, row AdminOrganizationProjectionRow) {
	row.Status = normalizeAdminProjectionStatus(row.Status)
	bucketDay := AdminOrganizationBucketDay(row.CreatedAt)
	batch.Query(`INSERT INTO organization_admin_buckets (bucket_day) VALUES (?)`, bucketDay)
	batch.Query(`INSERT INTO organization_admin_buckets_by_status (status, bucket_day) VALUES (?, ?)`, row.Status, bucketDay)
	batch.Query(`
		INSERT INTO organizations_admin_by_created (
			bucket_day, created_at, org_id, name, owner_email, owner_name,
			status, plan, storage_quota, deleted_at, users_count
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, bucketDay, row.CreatedAt, row.OrgID, row.Name, row.OwnerEmail, row.OwnerName,
		row.Status, row.Plan, row.StorageQuota, row.DeletedAt, row.UsersCount)
	batch.Query(`
		INSERT INTO organizations_admin_by_status_created (
			status, bucket_day, created_at, org_id, name, owner_email, owner_name,
			plan, storage_quota, deleted_at, users_count
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, row.Status, bucketDay, row.CreatedAt, row.OrgID, row.Name, row.OwnerEmail, row.OwnerName,
		row.Plan, row.StorageQuota, row.DeletedAt, row.UsersCount)
	batch.Query(`
		INSERT INTO organization_admin_projection_state (org_id, status, created_at)
		VALUES (?, ?, ?)
	`, row.OrgID, row.Status, row.CreatedAt)
	if row.DeletedAt == nil {
		batch.Query(`
			DELETE deleted_at FROM organizations_admin_by_created
			WHERE bucket_day = ? AND created_at = ? AND org_id = ?
		`, bucketDay, row.CreatedAt, row.OrgID)
		batch.Query(`
			DELETE deleted_at FROM organizations_admin_by_status_created
			WHERE status = ? AND bucket_day = ? AND created_at = ? AND org_id = ?
		`, row.Status, bucketDay, row.CreatedAt, row.OrgID)
	}
}

func AddDeleteAdminOrganizationReadModelQuery(batch *gocql.Batch, state AdminOrganizationProjectionState) {
	addDeleteAdminOrganizationProjectionEntryQuery(batch, state.OrgID, state.Status, state.CreatedAt)
	batch.Query(`DELETE FROM organization_admin_projection_state WHERE org_id = ?`, state.OrgID)
}

func SyncAdminOrganizationReadModel(session *gocql.Session, orgID string) error {
	row, err := ReadAdminOrganizationProjectionRow(session, orgID)
	if err != nil {
		return err
	}
	batch := session.Batch(gocql.LoggedBatch)
	if previousState, stateErr := ReadAdminOrganizationProjectionState(session, orgID); stateErr == nil {
		if previousState.Status != row.Status || !previousState.CreatedAt.Equal(row.CreatedAt) {
			addDeleteAdminOrganizationProjectionEntryQuery(batch, previousState.OrgID, previousState.Status, previousState.CreatedAt)
		}
	}
	AddUpsertAdminOrganizationReadModelQuery(batch, row)
	return batch.Exec()
}

func DeleteAdminOrganizationReadModel(session *gocql.Session, orgID string) error {
	state, err := ReadAdminOrganizationProjectionState(session, orgID)
	if err != nil {
		if err == gocql.ErrNotFound {
			return nil
		}
		return err
	}
	batch := session.Batch(gocql.LoggedBatch)
	AddDeleteAdminOrganizationReadModelQuery(batch, state)
	return batch.Exec()
}

func listAdminOrganizationBucketDays(session *gocql.Session) ([]string, error) {
	iter := session.Query(`SELECT bucket_day FROM organization_admin_buckets`).Iter()
	var buckets []string
	var bucketDay string
	for iter.Scan(&bucketDay) {
		buckets = append(buckets, bucketDay)
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	sort.Slice(buckets, func(i, j int) bool { return buckets[i] > buckets[j] })
	return buckets, nil
}

func listAdminOrganizationStatusBucketDays(session *gocql.Session, status string) ([]string, error) {
	status = normalizeAdminProjectionStatus(status)
	iter := session.Query(`SELECT bucket_day FROM organization_admin_buckets_by_status WHERE status = ?`, status).Iter()
	var buckets []string
	var bucketDay string
	for iter.Scan(&bucketDay) {
		buckets = append(buckets, bucketDay)
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	sort.Slice(buckets, func(i, j int) bool { return buckets[i] > buckets[j] })
	return buckets, nil
}

func ListAdminOrganizationRows(session *gocql.Session, statusFilter string) ([]AdminOrganizationProjectionRow, error) {
	var buckets []string
	var err error
	filtered := statusFilter != ""
	if filtered {
		statusFilter = normalizeAdminProjectionStatus(statusFilter)
		buckets, err = listAdminOrganizationStatusBucketDays(session, statusFilter)
	} else {
		buckets, err = listAdminOrganizationBucketDays(session)
	}
	if err != nil {
		return nil, err
	}

	var rows []AdminOrganizationProjectionRow
	for _, bucketDay := range buckets {
		var iter *gocql.Iter
		if filtered {
			iter = session.Query(`
				SELECT created_at, org_id, name, owner_email, owner_name, plan, storage_quota, deleted_at, users_count
				FROM organizations_admin_by_status_created
				WHERE status = ? AND bucket_day = ?
			`, statusFilter, bucketDay).Iter()
			var row AdminOrganizationProjectionRow
			var deletedAt *time.Time
			for iter.Scan(&row.CreatedAt, &row.OrgID, &row.Name, &row.OwnerEmail, &row.OwnerName, &row.Plan, &row.StorageQuota, &deletedAt, &row.UsersCount) {
				row.Status = statusFilter
				row.DeletedAt = deletedAt
				rows = append(rows, row)
				row = AdminOrganizationProjectionRow{}
			}
		} else {
			iter = session.Query(`
				SELECT created_at, org_id, name, owner_email, owner_name, status, plan, storage_quota, deleted_at, users_count
				FROM organizations_admin_by_created
				WHERE bucket_day = ?
			`, bucketDay).Iter()
			var row AdminOrganizationProjectionRow
			var deletedAt *time.Time
			for iter.Scan(&row.CreatedAt, &row.OrgID, &row.Name, &row.OwnerEmail, &row.OwnerName, &row.Status, &row.Plan, &row.StorageQuota, &deletedAt, &row.UsersCount) {
				row.Status = normalizeAdminProjectionStatus(row.Status)
				row.DeletedAt = deletedAt
				rows = append(rows, row)
				row = AdminOrganizationProjectionRow{}
			}
		}
		if err := iter.Close(); err != nil {
			return nil, err
		}
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			return rows[i].OrgID < rows[j].OrgID
		}
		return rows[i].CreatedAt.After(rows[j].CreatedAt)
	})
	return rows, nil
}

func ReadAdminUserProjectionRow(session *gocql.Session, orgID, userID string) (AdminUserProjectionRow, error) {
	row := AdminUserProjectionRow{OrgID: orgID, UserID: userID}
	var lastLoginAt *time.Time
	err := session.Query(`
		SELECT email, name, role, status, quota_bytes, used_bytes, last_login_at, created_at
		FROM users WHERE org_id = ? AND user_id = ?
	`, orgID, userID).Scan(&row.Email, &row.Name, &row.Role, &row.Status, &row.QuotaBytes, &row.QuotaUsage, &lastLoginAt, &row.CreatedAt)
	if err != nil {
		return AdminUserProjectionRow{}, err
	}
	row.Status = normalizeAdminProjectionStatus(row.Status)
	row.LastLoginAt = lastLoginAt
	return row, nil
}

func ReadAdminUserProjectionState(session *gocql.Session, userID string) (AdminUserProjectionState, error) {
	state := AdminUserProjectionState{UserID: userID}
	err := session.Query(`
		SELECT org_id, status, created_at FROM user_admin_projection_state WHERE user_id = ?
	`, userID).Scan(&state.OrgID, &state.Status, &state.CreatedAt)
	if err != nil {
		return AdminUserProjectionState{}, err
	}
	state.Status = normalizeAdminProjectionStatus(state.Status)
	return state, nil
}

func addDeleteAdminUserProjectionEntryQuery(batch *gocql.Batch, orgID, userID, status string, createdAt time.Time) {
	status = normalizeAdminProjectionStatus(status)
	bucketDay := AdminUserBucketDay(createdAt)
	batch.Query(`
		DELETE FROM users_admin_global_by_created
		WHERE bucket_day = ? AND created_at = ? AND org_id = ? AND user_id = ?
	`, bucketDay, createdAt, orgID, userID)
	batch.Query(`
		DELETE FROM users_admin_global_by_status_created
		WHERE status = ? AND bucket_day = ? AND created_at = ? AND org_id = ? AND user_id = ?
	`, status, bucketDay, createdAt, orgID, userID)
}

func AddUpsertAdminUserReadModelQuery(batch *gocql.Batch, row AdminUserProjectionRow) {
	row.Status = normalizeAdminProjectionStatus(row.Status)
	bucketDay := AdminUserBucketDay(row.CreatedAt)
	batch.Query(`INSERT INTO user_admin_global_buckets (bucket_day) VALUES (?)`, bucketDay)
	batch.Query(`INSERT INTO user_admin_buckets_by_status (status, bucket_day) VALUES (?, ?)`, row.Status, bucketDay)
	batch.Query(`
		INSERT INTO users_admin_global_by_created (
			bucket_day, created_at, org_id, user_id, email, name, role, status,
			quota_bytes, quota_usage, last_login_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, bucketDay, row.CreatedAt, row.OrgID, row.UserID, row.Email, row.Name, row.Role, row.Status,
		row.QuotaBytes, row.QuotaUsage, row.LastLoginAt)
	batch.Query(`
		INSERT INTO users_admin_global_by_status_created (
			status, bucket_day, created_at, org_id, user_id, email, name, role,
			quota_bytes, quota_usage, last_login_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, row.Status, bucketDay, row.CreatedAt, row.OrgID, row.UserID, row.Email, row.Name, row.Role,
		row.QuotaBytes, row.QuotaUsage, row.LastLoginAt)
	batch.Query(`
		INSERT INTO user_admin_projection_state (user_id, org_id, status, created_at)
		VALUES (?, ?, ?, ?)
	`, row.UserID, row.OrgID, row.Status, row.CreatedAt)
	if row.LastLoginAt == nil {
		batch.Query(`
			DELETE last_login_at FROM users_admin_global_by_created
			WHERE bucket_day = ? AND created_at = ? AND org_id = ? AND user_id = ?
		`, bucketDay, row.CreatedAt, row.OrgID, row.UserID)
		batch.Query(`
			DELETE last_login_at FROM users_admin_global_by_status_created
			WHERE status = ? AND bucket_day = ? AND created_at = ? AND org_id = ? AND user_id = ?
		`, row.Status, bucketDay, row.CreatedAt, row.OrgID, row.UserID)
	}
}

func AddDeleteAdminUserReadModelQuery(batch *gocql.Batch, state AdminUserProjectionState) {
	addDeleteAdminUserProjectionEntryQuery(batch, state.OrgID, state.UserID, state.Status, state.CreatedAt)
	batch.Query(`DELETE FROM user_admin_projection_state WHERE user_id = ?`, state.UserID)
}

func SyncAdminUserReadModel(session *gocql.Session, orgID, userID string) error {
	row, err := ReadAdminUserProjectionRow(session, orgID, userID)
	if err != nil {
		return err
	}
	batch := session.Batch(gocql.LoggedBatch)
	if previousState, stateErr := ReadAdminUserProjectionState(session, userID); stateErr == nil {
		if previousState.Status != row.Status || !previousState.CreatedAt.Equal(row.CreatedAt) || previousState.OrgID != row.OrgID {
			addDeleteAdminUserProjectionEntryQuery(batch, previousState.OrgID, previousState.UserID, previousState.Status, previousState.CreatedAt)
		}
	}
	AddUpsertAdminUserReadModelQuery(batch, row)
	return batch.Exec()
}

func DeleteAdminUserReadModel(session *gocql.Session, userID string) error {
	state, err := ReadAdminUserProjectionState(session, userID)
	if err != nil {
		if err == gocql.ErrNotFound {
			return nil
		}
		return err
	}
	batch := session.Batch(gocql.LoggedBatch)
	AddDeleteAdminUserReadModelQuery(batch, state)
	return batch.Exec()
}

func listAdminUserBucketDays(session *gocql.Session) ([]string, error) {
	iter := session.Query(`SELECT bucket_day FROM user_admin_global_buckets`).Iter()
	var buckets []string
	var bucketDay string
	for iter.Scan(&bucketDay) {
		buckets = append(buckets, bucketDay)
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	sort.Slice(buckets, func(i, j int) bool { return buckets[i] > buckets[j] })
	return buckets, nil
}

func listAdminUserStatusBucketDays(session *gocql.Session, status string) ([]string, error) {
	status = normalizeAdminProjectionStatus(status)
	iter := session.Query(`SELECT bucket_day FROM user_admin_buckets_by_status WHERE status = ?`, status).Iter()
	var buckets []string
	var bucketDay string
	for iter.Scan(&bucketDay) {
		buckets = append(buckets, bucketDay)
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	sort.Slice(buckets, func(i, j int) bool { return buckets[i] > buckets[j] })
	return buckets, nil
}

func ListAdminUserRows(session *gocql.Session, statusFilter string) ([]AdminUserProjectionRow, error) {
	var buckets []string
	var err error
	filtered := statusFilter != ""
	if filtered {
		buckets, err = listAdminUserStatusBucketDays(session, statusFilter)
	} else {
		buckets, err = listAdminUserBucketDays(session)
	}
	if err != nil {
		return nil, err
	}

	var rows []AdminUserProjectionRow
	for _, bucketDay := range buckets {
		var iter *gocql.Iter
		if filtered {
			iter = session.Query(`
				SELECT created_at, org_id, user_id, email, name, role, quota_bytes, quota_usage, last_login_at
				FROM users_admin_global_by_status_created
				WHERE status = ? AND bucket_day = ?
			`, normalizeAdminProjectionStatus(statusFilter), bucketDay).Iter()
			var row AdminUserProjectionRow
			var lastLoginAt *time.Time
			for iter.Scan(&row.CreatedAt, &row.OrgID, &row.UserID, &row.Email, &row.Name, &row.Role, &row.QuotaBytes, &row.QuotaUsage, &lastLoginAt) {
				row.Status = normalizeAdminProjectionStatus(statusFilter)
				row.LastLoginAt = lastLoginAt
				rows = append(rows, row)
				row = AdminUserProjectionRow{}
			}
		} else {
			iter = session.Query(`
				SELECT created_at, org_id, user_id, email, name, role, status, quota_bytes, quota_usage, last_login_at
				FROM users_admin_global_by_created
				WHERE bucket_day = ?
			`, bucketDay).Iter()
			var row AdminUserProjectionRow
			var lastLoginAt *time.Time
			for iter.Scan(&row.CreatedAt, &row.OrgID, &row.UserID, &row.Email, &row.Name, &row.Role, &row.Status, &row.QuotaBytes, &row.QuotaUsage, &lastLoginAt) {
				row.Status = normalizeAdminProjectionStatus(row.Status)
				row.LastLoginAt = lastLoginAt
				rows = append(rows, row)
				row = AdminUserProjectionRow{}
			}
		}
		if err := iter.Close(); err != nil {
			return nil, err
		}
	}

	sort.Slice(rows, func(i, j int) bool {
		if rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			if rows[i].OrgID == rows[j].OrgID {
				return rows[i].UserID < rows[j].UserID
			}
			return rows[i].OrgID < rows[j].OrgID
		}
		return rows[i].CreatedAt.After(rows[j].CreatedAt)
	})
	return rows, nil
}
