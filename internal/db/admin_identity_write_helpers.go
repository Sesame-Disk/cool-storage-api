package db

import (
	"fmt"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

const adminIdentityStatusActive = "active"

type AdminOrganizationWriteSpec struct {
	OrgID                  string
	Name                   string
	Status                 string
	Settings               map[string]string
	StorageQuota           int64
	StorageUsed            int64
	ChunkingPolynomial     int64
	StorageConfig          map[string]string
	CreatedAt              time.Time
	Plan                   string
	QuotaPolicy            string
	BillingCycle           string
	TrafficQuota           int64
	TrafficUploadQuota     int64
	TrafficDownloadQuota   int64
	MaxUsers               int
	CurrentPeriodStartedAt time.Time
	CurrentPeriodEndsAt    time.Time
	DeletedAt              *time.Time
}

type AdminUserWriteSpec struct {
	OrgID                string
	UserID               string
	Email                string
	Name                 string
	Role                 string
	Status               string
	QuotaBytes           int64
	UsedBytes            int64
	CreatedAt            time.Time
	DeletedAt            *time.Time
	TrafficUploadQuota   int64
	TrafficDownloadQuota int64
	LastLoginAt          *time.Time
	OIDCIssuer           string
	OIDCSub              string
}

type AdminUserUpdateSpec struct {
	Name                 string
	Role                 string
	Status               string
	DeletedAt            *time.Time
	QuotaBytes           int64
	TrafficUploadQuota   int64
	TrafficDownloadQuota int64
	AttachOIDCIssuer     string
	AttachOIDCSub        string
	AttachEmail          string
}

type AdminOrganizationLifecycleUpdate struct {
	Status              string
	DeletedAt           *time.Time
	DeletedMarkerName   string
	DeletedMarkerAt     *time.Time
	DeleteDeletedMarker bool
}

type adminCanonicalUserState struct {
	Name                 string
	Status               string
	QuotaBytes           int64
	TrafficUploadQuota   int64
	TrafficDownloadQuota int64
	DeletedAt            *time.Time
}

func BuildAdminOrganizationProjectionRowForNewUser(session *gocql.Session, orgID, email, name, role string) (AdminOrganizationProjectionRow, error) {
	row, err := ReadAdminOrganizationProjectionRow(session, orgID)
	if err != nil {
		return AdminOrganizationProjectionRow{}, err
	}
	row.UsersCount++
	if row.OwnerEmail == "" || role == "owner" {
		row.OwnerEmail = email
		row.OwnerName = name
	}
	return row, nil
}

func BuildAdminOrganizationProjectionRowForUpdatedUser(session *gocql.Session, orgID, updatedUserID, updatedName, updatedRole string) (AdminOrganizationProjectionRow, error) {
	row := AdminOrganizationProjectionRow{OrgID: orgID}
	var deletedAt *time.Time
	if err := session.Query(`
		SELECT name, status, plan, storage_quota, deleted_at, created_at
		FROM organizations WHERE org_id = ?
	`, orgID).Scan(&row.Name, &row.Status, &row.Plan, &row.StorageQuota, &deletedAt, &row.CreatedAt); err != nil {
		return AdminOrganizationProjectionRow{}, err
	}
	row.Status = normalizeAdminProjectionStatus(row.Status)
	row.DeletedAt = deletedAt

	iter := session.Query(`
		SELECT user_id, email, name, role FROM users WHERE org_id = ?
	`, orgID).Iter()

	var userID, email, name, role string
	var firstEmail, firstName string
	firstRemaining := true
	for iter.Scan(&userID, &email, &name, &role) {
		if userID == updatedUserID {
			name = updatedName
			role = updatedRole
		}
		row.UsersCount++
		if firstRemaining {
			firstEmail, firstName = email, name
			firstRemaining = false
		}
		if row.OwnerEmail == "" && (role == "superadmin" || role == "owner" || role == "admin") {
			row.OwnerEmail = email
			row.OwnerName = name
		}
	}
	if err := iter.Close(); err != nil {
		return AdminOrganizationProjectionRow{}, err
	}
	if row.OwnerEmail == "" {
		row.OwnerEmail = firstEmail
		row.OwnerName = firstName
	}
	return row, nil
}

func CreateOrganizationWithUsersAndReadModels(session *gocql.Session, org AdminOrganizationWriteSpec, users []AdminUserWriteSpec) error {
	status := normalizeAdminProjectionStatus(org.Status)
	batch := session.Batch(gocql.LoggedBatch)
	batch.Query(`
		INSERT INTO organizations (
			org_id, name, status, settings, storage_quota, storage_used,
			chunking_polynomial, storage_config, created_at,
			plan, quota_policy, billing_cycle,
			traffic_quota, traffic_upload_quota, traffic_download_quota, max_users,
			current_period_started_at, current_period_ends_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		org.OrgID, org.Name, status, org.Settings,
		org.StorageQuota, org.StorageUsed, org.ChunkingPolynomial, org.StorageConfig, org.CreatedAt,
		org.Plan, org.QuotaPolicy, org.BillingCycle,
		org.TrafficQuota, org.TrafficUploadQuota, org.TrafficDownloadQuota, org.MaxUsers,
		org.CurrentPeriodStartedAt, org.CurrentPeriodEndsAt,
	)

	for _, user := range users {
		addCreateAdminUserQueries(batch, user)
		AddUpsertAdminUserReadModelQuery(batch, adminUserProjectionRowFromWriteSpec(user))
	}

	AddUpsertAdminOrganizationReadModelQuery(batch, adminOrganizationProjectionRowFromWriteSpec(org, users))
	return batch.Exec()
}

func CreateUserWithLookupsAndReadModels(session *gocql.Session, user AdminUserWriteSpec) error {
	orgProjectionRow, err := BuildAdminOrganizationProjectionRowForNewUser(session, user.OrgID, user.Email, user.Name, user.Role)
	if err != nil {
		return fmt.Errorf("build admin org projection for new user %s in org %s: %w", user.UserID, user.OrgID, err)
	}

	batch := session.Batch(gocql.LoggedBatch)
	addCreateAdminUserQueries(batch, user)
	AddUpsertAdminUserReadModelQuery(batch, adminUserProjectionRowFromWriteSpec(user))
	AddUpsertAdminOrganizationReadModelQuery(batch, orgProjectionRow)
	if err := batch.Exec(); err != nil {
		return fmt.Errorf("exec create user batch for user %s in org %s: %w", user.UserID, user.OrgID, err)
	}
	return nil
}

func UpdateUserAndAdminReadModels(session *gocql.Session, orgID, userID string, next AdminUserUpdateSpec) error {
	previousUserState, previousUserStateErr := ReadAdminUserProjectionState(session, userID)
	if previousUserStateErr != nil && previousUserStateErr != gocql.ErrNotFound {
		return previousUserStateErr
	}

	userProjectionRow, err := ReadAdminUserProjectionRow(session, orgID, userID)
	if err != nil {
		return err
	}
	userProjectionRow.Name = next.Name
	userProjectionRow.Role = next.Role
	userProjectionRow.Status = normalizeAdminProjectionStatus(next.Status)
	userProjectionRow.QuotaBytes = next.QuotaBytes

	orgProjectionRow, err := BuildAdminOrganizationProjectionRowForUpdatedUser(session, orgID, userID, next.Name, next.Role)
	if err != nil {
		return err
	}

	batch := session.Batch(gocql.LoggedBatch)
	if next.AttachOIDCSub != "" {
		batch.Query(`
			UPDATE users
			SET name = ?, role = ?, status = ?, deleted_at = ?, quota_bytes = ?, traffic_upload_quota = ?, traffic_download_quota = ?, oidc_sub = ?
			WHERE org_id = ? AND user_id = ?
		`, next.Name, next.Role, userProjectionRow.Status, next.DeletedAt, next.QuotaBytes, next.TrafficUploadQuota, next.TrafficDownloadQuota, next.AttachOIDCSub, orgID, userID)
		batch.Query(`
			INSERT INTO users_by_oidc (oidc_issuer, oidc_sub, user_id, org_id)
			VALUES (?, ?, ?, ?)
		`, next.AttachOIDCIssuer, next.AttachOIDCSub, userID, orgID)
		if next.AttachEmail != "" {
			batch.Query(`
				INSERT INTO users_by_email (email, user_id, org_id)
				VALUES (?, ?, ?)
			`, next.AttachEmail, userID, orgID)
		}
	} else {
		batch.Query(`
			UPDATE users
			SET name = ?, role = ?, status = ?, deleted_at = ?, quota_bytes = ?, traffic_upload_quota = ?, traffic_download_quota = ?
			WHERE org_id = ? AND user_id = ?
		`, next.Name, next.Role, userProjectionRow.Status, next.DeletedAt, next.QuotaBytes, next.TrafficUploadQuota, next.TrafficDownloadQuota, orgID, userID)
	}
	if previousUserStateErr == nil && (previousUserState.Status != userProjectionRow.Status || previousUserState.OrgID != userProjectionRow.OrgID || !previousUserState.CreatedAt.Equal(userProjectionRow.CreatedAt)) {
		AddDeleteAdminUserStatusProjectionEntryQuery(batch, previousUserState)
	}
	AddUpsertAdminUserReadModelQuery(batch, userProjectionRow)
	AddUpsertAdminOrganizationReadModelQuery(batch, orgProjectionRow)
	return batch.Exec()
}

func UpdateUserRoleAndAdminReadModels(session *gocql.Session, orgID, userID, role string) error {
	current, err := readAdminCanonicalUserState(session, orgID, userID)
	if err != nil {
		return err
	}
	return UpdateUserAndAdminReadModels(session, orgID, userID, AdminUserUpdateSpec{
		Name:                 current.Name,
		Role:                 role,
		Status:               current.Status,
		DeletedAt:            current.DeletedAt,
		QuotaBytes:           current.QuotaBytes,
		TrafficUploadQuota:   current.TrafficUploadQuota,
		TrafficDownloadQuota: current.TrafficDownloadQuota,
	})
}

func UpdateUserRoleAttachOIDCIdentityAndAdminReadModels(session *gocql.Session, orgID, userID, email, issuer, oidcSub, role string) error {
	current, err := readAdminCanonicalUserState(session, orgID, userID)
	if err != nil {
		return err
	}
	return UpdateUserAndAdminReadModels(session, orgID, userID, AdminUserUpdateSpec{
		Name:                 current.Name,
		Role:                 role,
		Status:               current.Status,
		DeletedAt:            current.DeletedAt,
		QuotaBytes:           current.QuotaBytes,
		TrafficUploadQuota:   current.TrafficUploadQuota,
		TrafficDownloadQuota: current.TrafficDownloadQuota,
		AttachOIDCIssuer:     issuer,
		AttachOIDCSub:        oidcSub,
		AttachEmail:          email,
	})
}

func UpdateOrganizationLifecycleAndReadModel(session *gocql.Session, orgID string, next AdminOrganizationLifecycleUpdate) error {
	previousOrgState, previousOrgStateErr := ReadAdminOrganizationProjectionState(session, orgID)
	if previousOrgStateErr != nil && previousOrgStateErr != gocql.ErrNotFound {
		return previousOrgStateErr
	}

	orgProjectionRow, err := ReadAdminOrganizationProjectionRow(session, orgID)
	if err != nil {
		return err
	}
	orgProjectionRow.Status = normalizeAdminProjectionStatus(next.Status)
	orgProjectionRow.DeletedAt = next.DeletedAt

	batch := session.Batch(gocql.LoggedBatch)
	batch.Query(`
		UPDATE organizations SET status = ?, deleted_at = ? WHERE org_id = ?
	`, orgProjectionRow.Status, next.DeletedAt, orgID)
	if next.DeleteDeletedMarker {
		batch.Query(`DELETE FROM deleted_organizations WHERE org_id = ?`, orgID)
	}
	if next.DeletedMarkerAt != nil {
		batch.Query(`
			INSERT INTO deleted_organizations (org_id, name, deleted_at) VALUES (?, ?, ?)
		`, orgID, next.DeletedMarkerName, *next.DeletedMarkerAt)
	}
	if previousOrgStateErr == nil && (previousOrgState.Status != orgProjectionRow.Status || !previousOrgState.CreatedAt.Equal(orgProjectionRow.CreatedAt)) {
		AddDeleteAdminOrganizationStatusProjectionEntryQuery(batch, previousOrgState)
	}
	AddUpsertAdminOrganizationReadModelQuery(batch, orgProjectionRow)
	return batch.Exec()
}

func adminOrganizationProjectionRowFromWriteSpec(org AdminOrganizationWriteSpec, users []AdminUserWriteSpec) AdminOrganizationProjectionRow {
	row := AdminOrganizationProjectionRow{
		OrgID:        org.OrgID,
		Name:         org.Name,
		Status:       normalizeAdminProjectionStatus(org.Status),
		Plan:         org.Plan,
		StorageQuota: org.StorageQuota,
		DeletedAt:    org.DeletedAt,
		UsersCount:   len(users),
		CreatedAt:    org.CreatedAt,
	}
	for _, user := range users {
		if user.Role == "superadmin" || user.Role == "owner" || user.Role == "admin" {
			row.OwnerEmail = user.Email
			row.OwnerName = user.Name
			return row
		}
	}
	if len(users) > 0 {
		row.OwnerEmail = users[0].Email
		row.OwnerName = users[0].Name
	}
	return row
}

func adminUserProjectionRowFromWriteSpec(user AdminUserWriteSpec) AdminUserProjectionRow {
	return AdminUserProjectionRow{
		OrgID:       user.OrgID,
		UserID:      user.UserID,
		Email:       user.Email,
		Name:        user.Name,
		Role:        user.Role,
		Status:      normalizeAdminProjectionStatus(user.Status),
		QuotaBytes:  user.QuotaBytes,
		QuotaUsage:  user.UsedBytes,
		LastLoginAt: user.LastLoginAt,
		CreatedAt:   user.CreatedAt,
	}
}

func addCreateAdminUserQueries(batch *gocql.Batch, user AdminUserWriteSpec) {
	status := normalizeAdminProjectionStatus(user.Status)
	if user.OIDCSub != "" {
		batch.Query(`
			INSERT INTO users (org_id, user_id, email, name, role, status, quota_bytes, used_bytes, created_at, oidc_sub)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, user.OrgID, user.UserID, user.Email, user.Name, user.Role, status, user.QuotaBytes, user.UsedBytes, user.CreatedAt, user.OIDCSub)
	} else {
		batch.Query(`
			INSERT INTO users (org_id, user_id, email, name, role, status, quota_bytes, used_bytes, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, user.OrgID, user.UserID, user.Email, user.Name, user.Role, status, user.QuotaBytes, user.UsedBytes, user.CreatedAt)
	}
	if user.Email != "" {
		batch.Query(`
			INSERT INTO users_by_email (email, user_id, org_id)
			VALUES (?, ?, ?)
		`, user.Email, user.UserID, user.OrgID)
	}
	if user.OIDCIssuer != "" && user.OIDCSub != "" {
		batch.Query(`
			INSERT INTO users_by_oidc (oidc_issuer, oidc_sub, user_id, org_id)
			VALUES (?, ?, ?, ?)
		`, user.OIDCIssuer, user.OIDCSub, user.UserID, user.OrgID)
	}
}

func readAdminCanonicalUserState(session *gocql.Session, orgID, userID string) (adminCanonicalUserState, error) {
	state := adminCanonicalUserState{}
	err := session.Query(`
		SELECT name, status, quota_bytes, traffic_upload_quota, traffic_download_quota, deleted_at
		FROM users WHERE org_id = ? AND user_id = ?
	`, orgID, userID).Scan(&state.Name, &state.Status, &state.QuotaBytes, &state.TrafficUploadQuota, &state.TrafficDownloadQuota, &state.DeletedAt)
	if err != nil {
		return adminCanonicalUserState{}, err
	}
	state.Status = normalizeAdminProjectionStatus(state.Status)
	return state, nil
}
