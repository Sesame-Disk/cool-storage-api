package v2

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/traffic"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

// errFileTagNotFound is returned when a file tag lookup fails (SELECT miss).
// Callers should map this to HTTP 404 rather than 500.
var errFileTagNotFound = errors.New("file tag not found")

var errOrganizationPurging = errors.New("organization is pending permanent deletion")

// User/Org status constants — lifecycle state independent of role.
const (
	StatusActive      = "active"
	StatusDeactivated = "deactivated"
	StatusDeleted     = "deleted"
	statusPurging     = "purging"
)

// IsUserUsable returns true if the user status allows normal access.
func IsUserUsable(status string) bool {
	return status == "" || status == StatusActive
}

// IsOrgUsable returns true if the org status allows normal access.
func IsOrgUsable(status string) bool {
	return status == "" || status == StatusActive
}

func formatOptionalTimestamp(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	return ts.UTC().Format(time.RFC3339)
}

func createUserWithEmailLookup(db interface{ Session() *gocql.Session }, orgID, userID, email, name, role string, quotaBytes, usedBytes int64, createdAt time.Time) error {
	return dbpkg.CreateUserWithLookupsAndReadModels(db.Session(), dbpkg.AdminUserWriteSpec{
		OrgID:      orgID,
		UserID:     userID,
		Email:      email,
		Name:       name,
		Role:       role,
		Status:     StatusActive,
		QuotaBytes: quotaBytes,
		UsedBytes:  usedBytes,
		CreatedAt:  createdAt,
	})
}

type batchedUserUpdate struct {
	Name                 string
	Role                 string
	Status               string
	DeletedAt            *time.Time
	QuotaBytes           int64
	TrafficUploadQuota   int64
	TrafficDownloadQuota int64
}

func updateUserAndAdminReadModels(
	db interface{ Session() *gocql.Session },
	orgID, userID string,
	next batchedUserUpdate,
) error {
	return dbpkg.UpdateUserAndAdminReadModels(db.Session(), orgID, userID, dbpkg.AdminUserUpdateSpec{
		Name:                 next.Name,
		Role:                 next.Role,
		Status:               next.Status,
		DeletedAt:            next.DeletedAt,
		QuotaBytes:           next.QuotaBytes,
		TrafficUploadQuota:   next.TrafficUploadQuota,
		TrafficDownloadQuota: next.TrafficDownloadQuota,
	})
}

type batchedOrganizationLifecycleUpdate struct {
	Status              string
	DeletedAt           *time.Time
	DeletedMarkerName   string
	DeletedMarkerAt     *time.Time
	DeleteDeletedMarker bool
}

func updateOrganizationLifecycleAndReadModel(
	db interface{ Session() *gocql.Session },
	orgID string,
	next batchedOrganizationLifecycleUpdate,
) error {
	return dbpkg.UpdateOrganizationLifecycleAndReadModel(db.Session(), orgID, dbpkg.AdminOrganizationLifecycleUpdate{
		Status:              next.Status,
		DeletedAt:           next.DeletedAt,
		DeletedMarkerName:   next.DeletedMarkerName,
		DeletedMarkerAt:     next.DeletedMarkerAt,
		DeleteDeletedMarker: next.DeleteDeletedMarker,
	})
}

func addAdminLibraryReadModelRefreshQueries(batch *gocql.Batch, row dbpkg.AdminLibraryProjectionRow, previous *dbpkg.AdminLibraryProjectionRow) {
	dbpkg.AddRefreshAdminLibraryReadModelQueries(batch, row, previous)
}

func buildNewLibraryProjectionRow(session *gocql.Session, orgID, libraryID, ownerID, name string, encrypted bool, storageClass string, sizeBytes, fileCount int64, createdAt, updatedAt time.Time) dbpkg.AdminLibraryProjectionRow {
	ownerEmail, ownerName := dbpkg.ResolveAdminLibraryOwnerFields(session, orgID, ownerID)
	return dbpkg.AdminLibraryProjectionRow{
		OrgID:        orgID,
		LibraryID:    libraryID,
		OwnerID:      ownerID,
		OwnerEmail:   ownerEmail,
		OwnerName:    ownerName,
		Name:         name,
		Encrypted:    encrypted,
		StorageClass: storageClass,
		SizeBytes:    sizeBytes,
		FileCount:    fileCount,
		CreatedAt:    createdAt,
		UpdatedAt:    updatedAt,
	}
}

// addNewLibraryProjectionQueries enqueues the admin read-model projection writes
// (libraries_by_org_updated / libraries_by_owner / global) for a freshly created
// library into batch and returns the exact projection row so rollback can tear
// down the same keys without re-reading Cassandra.
func addNewLibraryProjectionQueries(session *gocql.Session, batch *gocql.Batch, orgID, libraryID, ownerID, name string, encrypted bool, storageClass string, sizeBytes, fileCount int64, createdAt, updatedAt time.Time) dbpkg.AdminLibraryProjectionRow {
	projectionRow := buildNewLibraryProjectionRow(session, orgID, libraryID, ownerID, name, encrypted, storageClass, sizeBytes, fileCount, createdAt, updatedAt)
	addAdminLibraryReadModelRefreshQueries(batch, projectionRow, nil)
	return projectionRow
}

func buildShareReadModelRow(
	db interface{ Session() *gocql.Session },
	libraryID, shareID, sharedBy, sharedTo, sharedToType, permission string,
	createdAt time.Time,
	expiresAt *time.Time,
) (dbpkg.ShareReadModelRow, error) {
	row := dbpkg.ShareReadModelRow{
		LibraryID:    libraryID,
		ShareID:      shareID,
		SharedBy:     sharedBy,
		SharedTo:     sharedTo,
		SharedToType: sharedToType,
		Permission:   permission,
		CreatedAt:    createdAt,
		ExpiresAt:    expiresAt,
	}
	if err := db.Session().Query(`
		SELECT org_id, name, encrypted FROM libraries_by_id WHERE library_id = ?
	`, libraryID).Scan(&row.OrgID, &row.RepoName, &row.Encrypted); err != nil {
		return dbpkg.ShareReadModelRow{}, err
	}
	_ = db.Session().Query(`
		SELECT size_bytes FROM libraries WHERE org_id = ? AND library_id = ?
	`, row.OrgID, libraryID).Scan(&row.SizeBytes)
	row.SharedByEmail, row.SharedByName = dbpkg.ResolveAdminLibraryOwnerFields(db.Session(), row.OrgID, sharedBy)
	return row, nil
}

func buildAdminGroupProjectionRow(
	db interface{ Session() *gocql.Session },
	orgID, groupID, name, creatorID, parentGroupID string,
	isDepartment bool,
	createdAt time.Time,
) dbpkg.AdminGroupProjectionRow {
	ownerEmail, ownerName := dbpkg.ResolveAdminGroupOwnerFields(db.Session(), orgID, creatorID)
	return dbpkg.AdminGroupProjectionRow{
		OrgID:         orgID,
		GroupID:       groupID,
		Name:          name,
		CreatorID:     creatorID,
		OwnerEmail:    ownerEmail,
		OwnerName:     ownerName,
		ParentGroupID: parentGroupID,
		IsDepartment:  isDepartment,
		CreatedAt:     createdAt,
	}
}

func addAdminGroupReadModelUpsertQuery(batch *gocql.Batch, row dbpkg.AdminGroupProjectionRow) {
	dbpkg.AddUpsertAdminGroupReadModelQuery(batch, row)
}

func updateLibraryOwner(db interface{ Session() *gocql.Session }, orgID, libraryID, newOwnerID string, updatedAt time.Time) error {
	previousRow, err := dbpkg.ReadAdminLibraryProjectionRow(db.Session(), orgID, libraryID)
	if err != nil {
		return err
	}
	nextRow := previousRow
	nextRow.OwnerID = newOwnerID
	nextRow.OwnerEmail, nextRow.OwnerName = dbpkg.ResolveAdminLibraryOwnerFields(db.Session(), orgID, newOwnerID)
	nextRow.UpdatedAt = updatedAt

	batch := db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		UPDATE libraries SET owner_id = ?, updated_at = ?
		WHERE org_id = ? AND library_id = ?
	`, newOwnerID, updatedAt, orgID, libraryID)
	batch.Query(`
		UPDATE libraries_by_id SET owner_id = ?
		WHERE library_id = ?
	`, newOwnerID, libraryID)
	addAdminLibraryReadModelRefreshQueries(batch, nextRow, &previousRow)
	return batch.Exec()
}

func createRepoAPIToken(db interface{ Session() *gocql.Session }, repoID, appName, apiToken, permission, generatedBy string, createdAt time.Time) error {
	batch := db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		INSERT INTO repo_api_tokens (repo_id, app_name, api_token, permission, generated_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, repoID, appName, apiToken, permission, generatedBy, createdAt)
	batch.Query(`
		INSERT INTO repo_api_tokens_by_token (api_token, repo_id, app_name, permission, generated_by)
		VALUES (?, ?, ?, ?, ?)
	`, apiToken, repoID, appName, permission, generatedBy)
	return batch.Exec()
}

func updateRepoAPITokenPermission(db interface{ Session() *gocql.Session }, repoID, appName, apiToken, permission string) error {
	batch := db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		UPDATE repo_api_tokens SET permission = ? WHERE repo_id = ? AND app_name = ?
	`, permission, repoID, appName)
	batch.Query(`
		UPDATE repo_api_tokens_by_token SET permission = ? WHERE api_token = ?
	`, permission, apiToken)
	return batch.Exec()
}

func deleteRepoAPIToken(db interface{ Session() *gocql.Session }, repoID, appName, apiToken string) error {
	batch := db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		DELETE FROM repo_api_tokens WHERE repo_id = ? AND app_name = ?
	`, repoID, appName)
	if apiToken != "" {
		batch.Query(`
			DELETE FROM repo_api_tokens_by_token WHERE api_token = ?
		`, apiToken)
	}
	return batch.Exec()
}

func createLibraryShare(db interface{ Session() *gocql.Session }, libraryID, shareID, sharedBy, sharedTo, sharedToType, permission string, createdAt time.Time, expiresAt *time.Time) error {
	row, err := buildShareReadModelRow(db, libraryID, shareID, sharedBy, sharedTo, sharedToType, permission, createdAt, expiresAt)
	if err != nil {
		return err
	}

	batch := db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		INSERT INTO shares (
			library_id, share_id, org_id, shared_by, shared_by_email, shared_by_name,
			shared_to, shared_to_type, repo_name, encrypted, size_bytes,
			permission, created_at, expires_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, libraryID, shareID, row.OrgID, sharedBy, row.SharedByEmail, row.SharedByName,
		sharedTo, sharedToType, row.RepoName, row.Encrypted, row.SizeBytes,
		permission, createdAt, expiresAt)
	if expiresAt != nil {
		dbpkg.AddUpsertShareExpiryQuery(batch, row.OrgID, libraryID, shareID, sharedTo, sharedToType, sharedBy, createdAt, *expiresAt)
	}
	dbpkg.AddUpsertShareReadModelQuery(batch, row)
	return batch.Exec()
}

func updateLibrarySharePermission(db interface{ Session() *gocql.Session }, libraryID, shareID, permission string) error {
	row, err := dbpkg.ReadShareReadModelRow(db.Session(), libraryID, shareID)
	if err != nil {
		return err
	}
	row.Permission = permission

	batch := db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		UPDATE shares SET permission = ? WHERE library_id = ? AND share_id = ?
	`, permission, libraryID, shareID)
	dbpkg.AddUpsertShareReadModelQuery(batch, row)
	return batch.Exec()
}

func deleteLibraryShare(db interface{ Session() *gocql.Session }, libraryID, shareID string) error {
	row, err := dbpkg.ReadShareReadModelRow(db.Session(), libraryID, shareID)
	if err != nil {
		return err
	}
	batch := db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		DELETE FROM shares WHERE library_id = ? AND share_id = ?
	`, libraryID, shareID)
	if row.ExpiresAt != nil && !row.ExpiresAt.IsZero() {
		dbpkg.AddDeleteShareExpiryQuery(batch, shareID, *row.ExpiresAt, row.OrgID, libraryID)
	}
	dbpkg.AddDeleteShareReadModelQuery(batch, row)
	return batch.Exec()
}

func syncAdminGroupReadModel(db interface{ Session() *gocql.Session }, orgID, groupID string) error {
	return dbpkg.SyncAdminGroupReadModel(db.Session(), orgID, groupID)
}

func readAdminGroupReadModelRow(db interface{ Session() *gocql.Session }, orgID, groupID string) (dbpkg.AdminGroupProjectionRow, error) {
	return dbpkg.ReadAdminGroupProjectionRow(db.Session(), orgID, groupID)
}

func deleteAdminGroupReadModel(db interface{ Session() *gocql.Session }, row dbpkg.AdminGroupProjectionRow) error {
	return dbpkg.DeleteAdminGroupReadModel(db.Session(), row)
}

func renameGroup(db interface{ Session() *gocql.Session }, orgID, groupID, newName string, updatedAt time.Time) error {
	groupRow, err := readAdminGroupReadModelRow(db, orgID, groupID)
	if err != nil {
		return err
	}

	iter := db.Session().Query(`
		SELECT user_id FROM group_members WHERE group_id = ?
	`, groupID).Iter()

	var memberID string
	var memberIDs []string
	for iter.Scan(&memberID) {
		memberIDs = append(memberIDs, memberID)
	}
	if err := iter.Close(); err != nil {
		return err
	}

	batch := db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		UPDATE groups SET name = ?, updated_at = ? WHERE org_id = ? AND group_id = ?
	`, newName, updatedAt, orgID, groupID)
	batch.Query(`
		UPDATE groups_by_id SET name = ? WHERE group_id = ?
	`, newName, groupID)
	for _, userID := range memberIDs {
		batch.Query(`
			UPDATE groups_by_member SET group_name = ? WHERE org_id = ? AND user_id = ? AND group_id = ?
		`, newName, orgID, userID, groupID)
	}

	groupRow.Name = newName
	addAdminGroupReadModelUpsertQuery(batch, groupRow)
	if err := batch.Exec(); err != nil {
		return fmt.Errorf("rename group: %w", err)
	}
	return nil
}

// SessionInvalidator can revoke all sessions for a given user.
// Implemented by auth.SessionManager; used by admin handlers to kill sessions
// when a user is deactivated or deleted.
type SessionInvalidator interface {
	InvalidateOrgSessions(orgID string) error
	InvalidateUserSessions(orgID, userID string) error
	InvalidateAPIKeySessions(apiKeyHash string) error
}

// APIKeyInvalidator can revoke all API keys for a given user.
type APIKeyInvalidator interface {
	InvalidateUserAPIKeys(orgID, userID gocql.UUID) error
}

func invalidateUserAPIKeys(aki APIKeyInvalidator, orgID, userID string) {
	if aki == nil {
		return
	}
	orgUUID, err := gocql.ParseUUID(orgID)
	if err != nil {
		log.Printf("[write_helpers] skip api key invalidation: invalid org_id %q: %v", orgID, err)
		return
	}
	userUUID, err := gocql.ParseUUID(userID)
	if err != nil {
		log.Printf("[write_helpers] skip api key invalidation: invalid user_id %q: %v", userID, err)
		return
	}
	aki.InvalidateUserAPIKeys(orgUUID, userUUID) //nolint:errcheck
}

func invalidateUserCredentials(si SessionInvalidator, aki APIKeyInvalidator, orgID, userID string) {
	if si != nil {
		if err := si.InvalidateUserSessions(orgID, userID); err != nil {
			log.Printf("[write_helpers] failed to invalidate sessions for user %s in org %s: %v", userID, orgID, err)
		}
	}
	invalidateUserAPIKeys(aki, orgID, userID)
}

// orgRoleRanks maps role names to their hierarchy rank for demotion detection.
var orgRoleRanks = map[string]int{
	"superadmin": 5,
	"owner":      4,
	"admin":      3,
	"user":       2,
	"readonly":   1,
	"guest":      0,
}

// isRoleDemotion returns true if newRole is strictly lower than oldRole in the hierarchy.
func isRoleDemotion(oldRole, newRole string) bool {
	return orgRoleRanks[newRole] < orgRoleRanks[oldRole]
}

// invalidateSessionsOnDemotion kills all user sessions when their role is lowered.
// This prevents stale sessions from retaining elevated privileges after a demotion.
func invalidateSessionsOnDemotion(si SessionInvalidator, orgID, userID, oldRole, newRole string) {
	if si == nil || !isRoleDemotion(oldRole, newRole) {
		return
	}
	go func() {
		if err := si.InvalidateUserSessions(orgID, userID); err != nil {
			log.Printf("[write_helpers] failed to invalidate sessions on demotion for user %s (org %s, %s→%s): %v",
				userID, orgID, oldRole, newRole, err)
		}
	}()
}

func runUserStatusSideEffects(
	db interface{ Session() *gocql.Session },
	si SessionInvalidator,
	aki APIKeyInvalidator,
	orgID, userID, oldStatus, newStatus string,
) {
	if normalizeUserStatus(oldStatus) == normalizeUserStatus(newStatus) {
		return
	}
	switch normalizeUserStatus(newStatus) {
	case StatusActive:
		go setUserShareLinksActive(db, orgID, userID, true)
	case StatusDeactivated, StatusDeleted:
		go func() {
			invalidateUserCredentials(si, aki, orgID, userID)
			setUserShareLinksActive(db, orgID, userID, false)
		}()
	}
}

// activateUser marks a user as active, clears deleted_at, and in a background
// goroutine re-enables their share links. Works for both reactivation
// (deactivated → active) and restore (deleted → active).
func activateUser(db interface{ Session() *gocql.Session }, orgID, userID string) error {
	var lockedUserID string
	if err := db.Session().Query(`
		SELECT user_id FROM gc_user_hard_delete_locks WHERE user_id = ?
	`, userID).Scan(&lockedUserID); err == nil {
		return fmt.Errorf("user is pending permanent deletion")
	} else if !errors.Is(err, gocql.ErrNotFound) {
		return err
	}

	var name, role, status string
	var quotaBytes, trafficUploadQuota, trafficDownloadQuota int64
	var deletedAt *time.Time
	if err := db.Session().Query(`
		SELECT name, role, status, quota_bytes, traffic_upload_quota, traffic_download_quota, deleted_at
		FROM users WHERE org_id = ? AND user_id = ?
	`, orgID, userID).Scan(&name, &role, &status, &quotaBytes, &trafficUploadQuota, &trafficDownloadQuota, &deletedAt); err != nil {
		return err
	}
	if err := updateUserAndAdminReadModels(db, orgID, userID, batchedUserUpdate{
		Name:                 name,
		Role:                 role,
		Status:               StatusActive,
		DeletedAt:            nil,
		QuotaBytes:           quotaBytes,
		TrafficUploadQuota:   trafficUploadQuota,
		TrafficDownloadQuota: trafficDownloadQuota,
	}); err != nil {
		return err
	}
	go setUserShareLinksActive(db, orgID, userID, true)
	return nil
}

// softDeleteUser marks a user as deleted and, in a background goroutine,
// kills all their active sessions and disables their share links.
func softDeleteUser(db interface{ Session() *gocql.Session }, si SessionInvalidator, aki APIKeyInvalidator, orgID, userID string, deletedAt time.Time) error {
	var name, role, status string
	var quotaBytes, trafficUploadQuota, trafficDownloadQuota int64
	var currentDeletedAt *time.Time
	if err := db.Session().Query(`
		SELECT name, role, status, quota_bytes, traffic_upload_quota, traffic_download_quota, deleted_at
		FROM users WHERE org_id = ? AND user_id = ?
	`, orgID, userID).Scan(&name, &role, &status, &quotaBytes, &trafficUploadQuota, &trafficDownloadQuota, &currentDeletedAt); err != nil {
		return err
	}
	if err := updateUserAndAdminReadModels(db, orgID, userID, batchedUserUpdate{
		Name:                 name,
		Role:                 role,
		Status:               StatusDeleted,
		DeletedAt:            &deletedAt,
		QuotaBytes:           quotaBytes,
		TrafficUploadQuota:   trafficUploadQuota,
		TrafficDownloadQuota: trafficDownloadQuota,
	}); err != nil {
		return err
	}
	go func() {
		invalidateUserCredentials(si, aki, orgID, userID)
		setUserShareLinksActive(db, orgID, userID, false)
	}()
	return nil
}

// deactivateOrg marks an org as deactivated and, in a background goroutine,
// kills all sessions for every user in the org and disables all org share links.
func deactivateOrg(db interface{ Session() *gocql.Session }, si SessionInvalidator, aki APIKeyInvalidator, orgID string) error {
	if err := updateOrganizationLifecycleAndReadModel(db, orgID, batchedOrganizationLifecycleUpdate{
		Status:              StatusDeactivated,
		DeletedAt:           nil,
		DeleteDeletedMarker: true,
	}); err != nil {
		return err
	}
	go func() {
		invalidateOrgSessions(db, si, aki, orgID)
		setOrgShareLinksActive(db, orgID, false)
	}()
	return nil
}

// activateOrg marks an org as active, clears deleted_at, and repairs lifecycle
// read models. Already-active repair is accepted only when stale lifecycle
// metadata proves a previous activation completed the CAS but not phase two.
func activateOrg(db interface{ Session() *gocql.Session }, orgID, expectedStatus, repairFromStatus string) error {
	session := db.Session()
	reenableShareLinks := false
	var deletedMarkerAt *time.Time
	transitionToActive := func(expectedStatus string) (bool, string, error) {
		result := map[string]interface{}{}
		applied, err := session.Query(`
			UPDATE organizations SET status = ?, deleted_at = ? WHERE org_id = ?
			IF status = ?
		`, StatusActive, nil, orgID, expectedStatus).MapScanCAS(result)
		if err != nil {
			return false, "", err
		}
		currentStatus, _ := result["status"].(string)
		return applied, currentStatus, nil
	}

	if expectedStatus == "" {
		expectedStatus = StatusDeleted
	}
	if repairFromStatus == "" {
		repairFromStatus = expectedStatus
	}
	if repairFromStatus == StatusDeleted {
		markerAt, err := readDeletedOrganizationMarkerAt(session, orgID)
		if err != nil {
			return err
		}
		deletedMarkerAt = markerAt
	}
	applied, currentStatus, err := false, StatusActive, error(nil)
	if expectedStatus != StatusActive {
		applied, currentStatus, err = transitionToActive(expectedStatus)
	} else {
		if !hasInterruptedActivationRepairState(session, orgID, repairFromStatus, deletedMarkerAt) {
			return fmt.Errorf("organization lifecycle changed while reactivating")
		}
		applied, currentStatus, err = transitionToActive(StatusActive)
	}
	if err != nil {
		return err
	}
	if !applied {
		switch currentStatus {
		case StatusActive:
			if !hasInterruptedActivationRepairState(session, orgID, repairFromStatus, deletedMarkerAt) {
				return fmt.Errorf("organization lifecycle changed while reactivating")
			}
		case statusPurging:
			return errOrganizationPurging
		default:
			return fmt.Errorf("organization lifecycle changed while reactivating")
		}
	}
	reenableShareLinks = repairFromStatus == StatusDeleted || repairFromStatus == StatusDeactivated

	if err := repairActiveOrganizationReadModels(session, orgID, deletedMarkerAt); err != nil {
		return err
	}
	if reenableShareLinks {
		go setOrgShareLinksActive(db, orgID, true)
	}
	return nil
}

func readDeletedOrganizationMarkerAt(session *gocql.Session, orgID string) (*time.Time, error) {
	var deletedAt time.Time
	if err := session.Query(`SELECT deleted_at FROM deleted_organizations WHERE org_id = ?`, orgID).Scan(&deletedAt); err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &deletedAt, nil
}

func hasInterruptedActivationRepairState(session *gocql.Session, orgID, repairFromStatus string, deletedMarkerAt *time.Time) bool {
	if repairFromStatus == StatusDeleted && deletedMarkerAt != nil {
		return true
	}
	state, err := dbpkg.ReadAdminOrganizationProjectionState(session, orgID)
	return err == nil && state.Status == repairFromStatus
}

func repairActiveOrganizationReadModels(session *gocql.Session, orgID string, deletedMarkerAt *time.Time) error {
	if deletedMarkerAt != nil {
		applied, err := session.Query(`
			DELETE FROM deleted_organizations WHERE org_id = ? IF deleted_at = ?
		`, orgID, *deletedMarkerAt).MapScanCAS(map[string]interface{}{})
		if err != nil {
			return err
		}
		if !applied {
			return fmt.Errorf("organization lifecycle changed while reactivating")
		}
	}

	previousOrgState, previousOrgStateErr := dbpkg.ReadAdminOrganizationProjectionState(session, orgID)
	if previousOrgStateErr != nil && previousOrgStateErr != gocql.ErrNotFound {
		return previousOrgStateErr
	}
	orgProjectionRow, err := dbpkg.ReadAdminOrganizationProjectionRow(session, orgID)
	if err != nil {
		return err
	}
	if orgProjectionRow.Status != StatusActive {
		return fmt.Errorf("organization lifecycle changed while reactivating")
	}
	orgProjectionRow.DeletedAt = nil

	batch := session.Batch(gocql.LoggedBatch)
	if previousOrgStateErr == nil && (previousOrgState.Status != orgProjectionRow.Status || !previousOrgState.CreatedAt.Equal(orgProjectionRow.CreatedAt)) {
		dbpkg.AddDeleteAdminOrganizationStatusProjectionEntryQuery(batch, previousOrgState)
	}
	dbpkg.AddUpsertAdminOrganizationReadModelQuery(batch, orgProjectionRow)
	return batch.Exec()
}

// softDeleteOrg marks an org as deleted and, in a background goroutine,
// kills all sessions for every user in the org and disables all org share links.
func softDeleteOrg(db interface{ Session() *gocql.Session }, si SessionInvalidator, aki APIKeyInvalidator, orgID string, deletedAt time.Time) error {
	orgProjectionRow, err := dbpkg.ReadAdminOrganizationProjectionRow(db.Session(), orgID)
	if err != nil {
		return err
	}
	if err := updateOrganizationLifecycleAndReadModel(db, orgID, batchedOrganizationLifecycleUpdate{
		Status:            StatusDeleted,
		DeletedAt:         &deletedAt,
		DeletedMarkerName: orgProjectionRow.Name,
		DeletedMarkerAt:   &deletedAt,
	}); err != nil {
		return err
	}
	go func() {
		invalidateOrgSessions(db, si, aki, orgID)
		setOrgShareLinksActive(db, orgID, false)
	}()
	return nil
}

// invalidateOrgSessions invalidates sessions for every user in an org.
// Used when an org is deactivated or soft-deleted.
func invalidateOrgSessions(dbSess interface{ Session() *gocql.Session }, si SessionInvalidator, aki APIKeyInvalidator, orgID string) {
	if si == nil && aki == nil {
		return
	}
	if si != nil {
		if err := si.InvalidateOrgSessions(orgID); err != nil {
			log.Printf("[write_helpers] failed to invalidate org sessions for org %q: %v", orgID, err)
		}
	}
	if aki == nil {
		return
	}

	iter := dbSess.Session().Query(`SELECT user_id FROM users WHERE org_id = ?`, orgID).Iter()
	var uid string
	for iter.Scan(&uid) {
		invalidateUserAPIKeys(aki, orgID, uid)
	}
	if err := iter.Close(); err != nil {
		log.Printf("[write_helpers] invalidate org sessions: iter close failed for org %q: %v", orgID, err)
	}
}

// setUserShareLinksActive toggles the `active` flag on all share links created by a user.
// Updates the primary row, share_links_by_creator and the admin read models.
// Used when a user is deactivated/deleted (active=false) or reactivated (active=true).
func setUserShareLinksActive(db interface{ Session() *gocql.Session }, orgID, userID string, active bool) {
	type link struct {
		linkType  string
		token     string
		createdAt time.Time
		active    bool
	}
	iter := db.Session().Query(
		`SELECT link_type, link_token, created_at, active FROM share_links_by_creator WHERE org_id = ? AND created_by = ?`,
		orgID, userID,
	).Iter()

	var links []link
	var l link
	for iter.Scan(&l.linkType, &l.token, &l.createdAt, &l.active) {
		links = append(links, l)
	}
	if err := iter.Close(); err != nil {
		log.Printf("[setUserShareLinksActive] iter close: %v", err)
	}

	for i := 0; i < len(links); i += 25 {
		end := i + 25
		if end > len(links) {
			end = len(links)
		}
		batch := db.Session().Batch(gocql.UnloggedBatch)
		changed := false
		for _, lk := range links[i:end] {
			if lk.active == active {
				continue
			}
			changed = true
			batch.Query(`UPDATE share_links SET active = ? WHERE link_token = ?`, active, lk.token)
			batch.Query(`UPDATE share_links_by_creator SET active = ? WHERE org_id = ? AND created_by = ? AND created_at = ? AND link_token = ?`,
				active, orgID, userID, lk.createdAt, lk.token)
			dbpkg.AddUpdateAdminLinkActiveQuery(batch, lk.linkType, lk.createdAt, orgID, lk.token, active)
		}
		if !changed {
			continue
		}
		if err := batch.Exec(); err != nil {
			log.Printf("[setUserShareLinksActive] batch exec: %v", err)
		}
	}
}

// setOrgShareLinksActive toggles the `active` flag on all share links in an org.
// Uses the org admin read model to enumerate links, then updates canonical rows.
// Used when an org is deactivated/deleted (active=false) or reactivated (active=true).
func setOrgShareLinksActive(db interface{ Session() *gocql.Session }, orgID string, active bool) {
	links, err := dbpkg.ListAdminOrgLinkIndexRows(db.Session(), orgID)
	if err != nil {
		log.Printf("[setOrgShareLinksActive] list links: %v", err)
		return
	}

	for i := 0; i < len(links); i += 25 {
		end := i + 25
		if end > len(links) {
			end = len(links)
		}
		batch := db.Session().Batch(gocql.UnloggedBatch)
		changed := false
		for _, lk := range links[i:end] {
			if lk.Active == active {
				continue
			}
			changed = true
			batch.Query(`UPDATE share_links SET active = ? WHERE link_token = ?`, active, lk.Token)
			batch.Query(`UPDATE share_links_by_creator SET active = ? WHERE org_id = ? AND created_by = ? AND created_at = ? AND link_token = ?`,
				active, orgID, lk.CreatedBy, lk.CreatedAt, lk.Token)
			dbpkg.AddUpdateAdminLinkActiveQuery(batch, lk.LinkType, lk.CreatedAt, orgID, lk.Token, active)
		}
		if !changed {
			continue
		}
		if err := batch.Exec(); err != nil {
			log.Printf("[setOrgShareLinksActive] batch exec: %v", err)
		}
	}
}

// deleteConsumedShareLink removes a single-use share link from all 3 tables after it is consumed.
// Called fire-and-forget after a successful download/upload on a single-use link.
// Keeps the DB clean — consumed single-use links are permanently gone.
func deleteConsumedShareLink(db interface{ Session() *gocql.Session }, token, orgID, libraryID, createdBy string, createdAt time.Time) {
	var linkType string
	var expiresAt *time.Time
	if err := db.Session().Query(`SELECT link_type, expires_at FROM share_links WHERE link_token = ?`, token).Scan(&linkType, &expiresAt); err != nil {
		log.Printf("[deleteConsumedShareLink] lookup failed for token %s: %v", token, err)
		return
	}

	batch := db.Session().Batch(gocql.UnloggedBatch) // deletes are idempotent
	batch.Query(`DELETE FROM share_links WHERE link_token = ?`, token)
	batch.Query(`DELETE FROM share_links_by_creator WHERE org_id = ? AND created_by = ? AND created_at = ? AND link_token = ?`,
		orgID, createdBy, createdAt, token)
	batch.Query(`DELETE FROM share_links_by_library WHERE org_id = ? AND library_id = ? AND link_token = ?`,
		orgID, libraryID, token)
	if expiresAt != nil && !expiresAt.IsZero() {
		dbpkg.AddDeleteShareLinkExpiryQuery(batch, token, *expiresAt)
	}
	dbpkg.AddDeleteAdminLinkReadModelQuery(batch, linkType, createdAt, orgID, token)
	if err := batch.Exec(); err != nil {
		log.Printf("[deleteConsumedShareLink] failed for token %s: %v", token, err)
		return
	}
	dbpkg.BestEffortAdjustAdminOrgLinkCount(db.Session(), orgID, linkType, dbpkg.AdminOrgLinkCountDelta(-1))
}

// incrementShareLinkCounterDualWrite increments a link counter on primary + lookup tables.
// counter must be one of: view_count, download_count, upload_count.
func incrementShareLinkCounterDualWrite(db interface{ Session() *gocql.Session }, token, counter string, touchedAt time.Time) error {
	if counter != "view_count" && counter != "download_count" && counter != "upload_count" {
		return fmt.Errorf("invalid counter: %s", counter)
	}

	var orgID, createdBy, linkType string
	var createdAt time.Time
	var viewCountPtr, downloadCountPtr, uploadCountPtr *int
	if err := db.Session().Query(`
		SELECT org_id, created_by, created_at, link_type, view_count, download_count, upload_count
		FROM share_links WHERE link_token = ?
	`, token).Scan(&orgID, &createdBy, &createdAt, &linkType, &viewCountPtr, &downloadCountPtr, &uploadCountPtr); err != nil {
		return err
	}

	viewCount := 0
	if viewCountPtr != nil {
		viewCount = *viewCountPtr
	}
	downloadCount := 0
	if downloadCountPtr != nil {
		downloadCount = *downloadCountPtr
	}
	uploadCount := 0
	if uploadCountPtr != nil {
		uploadCount = *uploadCountPtr
	}

	switch counter {
	case "view_count":
		viewCount++
	case "download_count":
		downloadCount++
	case "upload_count":
		uploadCount++
	}

	batch := db.Session().Batch(gocql.UnloggedBatch)
	batch.Query(`
		UPDATE share_links
		SET view_count = ?, download_count = ?, upload_count = ?, last_accessed_at = ?
		WHERE link_token = ?
	`, viewCount, downloadCount, uploadCount, touchedAt, token)
	batch.Query(`
		UPDATE share_links_by_creator
		SET view_count = ?, download_count = ?, upload_count = ?, last_accessed_at = ?
		WHERE org_id = ? AND created_by = ? AND created_at = ? AND link_token = ?
	`, viewCount, downloadCount, uploadCount, touchedAt, orgID, createdBy, createdAt, token)

	dbpkg.AddUpdateAdminLinkCountersQuery(batch, linkType, createdAt, orgID, token, viewCount, uploadCount)

	return batch.Exec()
}

func rollbackNewLibrary(db interface{ Session() *gocql.Session }, projectionRow dbpkg.AdminLibraryProjectionRow) error {
	batch := db.Session().Batch(gocql.LoggedBatch)
	dbpkg.AddDeleteLibraryPolicyQuery(batch, dbpkg.GCLibraryPolicyVersionTTL, projectionRow.OrgID, projectionRow.LibraryID)
	dbpkg.AddDeleteLibraryPolicyQuery(batch, dbpkg.GCLibraryPolicyAutoDelete, projectionRow.OrgID, projectionRow.LibraryID)
	// Tear down the same projection keys written during creation. Using the
	// original row avoids a fresh Cassandra read during rollback, so a transient
	// lookup failure cannot leave phantom admin/global projections behind while
	// still deleting the canonical library row.
	dbpkg.AddDeleteAdminLibraryReadModelQuery(batch, projectionRow)
	batch.Query(`
		DELETE FROM libraries WHERE org_id = ? AND library_id = ?
	`, projectionRow.OrgID, projectionRow.LibraryID)
	batch.Query(`
		DELETE FROM libraries_by_id WHERE library_id = ?
	`, projectionRow.LibraryID)
	batch.Query(`
		DELETE FROM fs_objects WHERE library_id = ?
	`, projectionRow.LibraryID)
	batch.Query(`
		DELETE FROM commits WHERE library_id = ?
	`, projectionRow.LibraryID)
	return batch.Exec()
}

func syncAdminLibraryReadModel(db interface{ Session() *gocql.Session }, orgID, libraryID string) error {
	return dbpkg.SyncAdminLibraryReadModel(db.Session(), orgID, libraryID)
}

func addDeleteAdminLibraryReadModelQueries(db interface{ Session() *gocql.Session }, batch *gocql.Batch, orgID, libraryID string) error {
	return dbpkg.AddDeleteAdminLibraryReadModelQueries(db.Session(), batch, orgID, libraryID)
}

// ── Library soft-delete / restore with storage accounting ─────────────────────

// softDeleteLibrary marks a library as deleted, persists a GC marker, and
// adjusts storage counters.
// This is the canonical soft-delete path — all callers (user, admin, GC) must
// route through equivalent logic to keep storage accounting consistent.
//
// The lib-scope counter is left intact so restoreDeletedLibrary can reverse the
// operation. Permanent delete (or GC cascade) cleans up the lib-scope row via
// traffic.DeleteLibraryStorageCounter.
func softDeleteLibrary(db interface{ Session() *gocql.Session }, orgID, ownerID, deletedBy, libraryID string) error {
	now := time.Now().UTC()
	previousRow, err := dbpkg.ReadAdminLibraryProjectionRow(db.Session(), orgID, libraryID)
	if err != nil {
		return fmt.Errorf("read library projection row: %w", err)
	}
	nextRow := previousRow
	nextRow.UpdatedAt = now
	nextRow.DeletedAt = &now
	batch := db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		UPDATE libraries SET deleted_at = ?, deleted_by = ?, updated_at = ?
		WHERE org_id = ? AND library_id = ?`,
		now, deletedBy, now, orgID, libraryID,
	)
	batch.Query(`
		INSERT INTO deleted_libraries (library_id, org_id, deleted_at, storage_class)
		VALUES (?, ?, ?, ?)`,
		libraryID, orgID, now, previousRow.StorageClass,
	)
	traffic.AddAggregateStorageReconciliationQueries(batch, orgID, ownerID, now)
	addAdminLibraryReadModelRefreshQueries(batch, nextRow, &previousRow)
	if err := batch.Exec(); err != nil {
		return fmt.Errorf("soft-delete library: %w", err)
	}

	// Read the live lib-scope counter and subtract from aggregate scopes.
	traffic.AdjustAggregateStorageCounters(db, orgID, ownerID, libraryID, false)
	return nil
}

// restoreDeletedLibrary clears deleted_at, removes the GC marker, and re-adds
// the library's storage to aggregate counters. Mirror image of softDeleteLibrary.
func restoreDeletedLibrary(db interface{ Session() *gocql.Session }, orgID, ownerID, libraryID string) error {
	var lockedLibraryID string
	if err := db.Session().Query(`
		SELECT library_id FROM gc_library_hard_delete_locks WHERE library_id = ?
	`, libraryID).Scan(&lockedLibraryID); err == nil {
		return fmt.Errorf("library is pending permanent deletion")
	} else if !errors.Is(err, gocql.ErrNotFound) {
		return err
	}

	now := time.Now().UTC()
	previousRow, err := dbpkg.ReadAdminLibraryProjectionRow(db.Session(), orgID, libraryID)
	if err != nil {
		return fmt.Errorf("read library projection row: %w", err)
	}
	nextRow := previousRow
	nextRow.UpdatedAt = now
	nextRow.DeletedAt = nil
	batch := db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		UPDATE libraries SET updated_at = ?
		WHERE org_id = ? AND library_id = ?`,
		now, orgID, libraryID,
	)
	batch.Query(`
		DELETE deleted_at, deleted_by FROM libraries
		WHERE org_id = ? AND library_id = ?`,
		orgID, libraryID,
	)
	batch.Query(`DELETE FROM deleted_libraries WHERE library_id = ?`, libraryID)
	traffic.AddAggregateStorageReconciliationQueries(batch, orgID, ownerID, now)
	addAdminLibraryReadModelRefreshQueries(batch, nextRow, &previousRow)
	if err := batch.Exec(); err != nil {
		return fmt.Errorf("restore library: %w", err)
	}

	// Re-add the library's storage to aggregates.
	traffic.AdjustAggregateStorageCounters(db, orgID, ownerID, libraryID, true)
	return nil
}

// orgQuotas holds an organization's quota limits.
type orgQuotas struct {
	StorageQuota         int64
	TrafficQuota         int64 // combined upload+download
	TrafficUploadQuota   int64
	TrafficDownloadQuota int64
}

var readOrgQuotasFn = readOrgQuotas

type quotaValidationError struct {
	StatusCode int
	Message    string
}

func (e *quotaValidationError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// readOrgQuotas fetches the quota limits for an organization.
// Returns the quotas and an error if the DB read failed (as opposed to
// "org has no limits").  Callers that guard writes MUST check the error
// and reject the request instead of silently allowing unlimited quotas.
func readOrgQuotas(db interface{ Session() *gocql.Session }, orgID string) (orgQuotas, error) {
	var q orgQuotas
	err := db.Session().Query(
		`SELECT storage_quota, traffic_quota, traffic_upload_quota, traffic_download_quota FROM organizations WHERE org_id = ?`,
		orgID,
	).Scan(&q.StorageQuota, &q.TrafficQuota, &q.TrafficUploadQuota, &q.TrafficDownloadQuota)
	return q, err
}

func readAndValidateUserQuotaLimits(db interface{ Session() *gocql.Session }, orgID string, storageQuota, uploadQuota, downloadQuota int64) (orgQuotas, *quotaValidationError) {
	oq, err := readOrgQuotasFn(db, orgID)
	if err != nil {
		return orgQuotas{}, &quotaValidationError{
			StatusCode: http.StatusInternalServerError,
			Message:    "failed to read organization quotas",
		}
	}
	if msg := validateUserQuotaAgainstOrg(storageQuota, oq.StorageQuota, "storage quota"); msg != "" {
		return oq, &quotaValidationError{
			StatusCode: http.StatusBadRequest,
			Message:    msg,
		}
	}
	if msg := validateUserTrafficQuotasAgainstOrg(uploadQuota, downloadQuota, oq); msg != "" {
		return oq, &quotaValidationError{
			StatusCode: http.StatusBadRequest,
			Message:    msg,
		}
	}
	return oq, nil
}

// validateUserQuotaAgainstOrg checks that the given user quota does not exceed
// the corresponding org quota. orgLimit <= 0 means unlimited (no cap).
// Returns an error message or "" if valid.
func validateUserQuotaAgainstOrg(userValue, orgLimit int64, field string) string {
	if orgLimit <= 0 {
		return "" // org has no limit
	}
	if userValue <= 0 {
		return "" // user value is default/inherit/unlimited
	}
	if userValue > orgLimit {
		return fmt.Sprintf("%s (%d) exceeds organization limit (%d)", field, userValue, orgLimit)
	}
	return ""
}

// validateUserTrafficQuotasAgainstOrg validates that user traffic quotas don't
// exceed the org's per-direction limits, AND that their sum doesn't exceed the
// org's combined traffic limit (if set).
func validateUserTrafficQuotasAgainstOrg(uploadQuota, downloadQuota int64, oq orgQuotas) string {
	// Per-direction checks.
	if msg := validateUserQuotaAgainstOrg(uploadQuota, oq.TrafficUploadQuota, "upload quota"); msg != "" {
		return msg
	}
	if msg := validateUserQuotaAgainstOrg(downloadQuota, oq.TrafficDownloadQuota, "download quota"); msg != "" {
		return msg
	}
	// Combined check: each individual value must fit, AND their sum must fit.
	if oq.TrafficQuota > 0 {
		if uploadQuota > 0 && uploadQuota > oq.TrafficQuota {
			return fmt.Sprintf("upload quota (%d) exceeds organization combined traffic limit (%d)", uploadQuota, oq.TrafficQuota)
		}
		if downloadQuota > 0 && downloadQuota > oq.TrafficQuota {
			return fmt.Sprintf("download quota (%d) exceeds organization combined traffic limit (%d)", downloadQuota, oq.TrafficQuota)
		}
		// Sum check: if both are set, their sum must not exceed the combined limit.
		if uploadQuota > 0 && downloadQuota > 0 && uploadQuota+downloadQuota > oq.TrafficQuota {
			return fmt.Sprintf("upload + download quota sum (%d) exceeds organization combined traffic limit (%d)",
				uploadQuota+downloadQuota, oq.TrafficQuota)
		}
	}
	return ""
}

// createCustomSharePermission handles the dual-writes for creating a new custom share permission.
func createCustomSharePermission(sess *gocql.Session, permID, userID, name, description, permissionJSON string, createdAt time.Time) error {
	batch := sess.Batch(gocql.LoggedBatch)
	batch.Query(`
		INSERT INTO custom_share_permissions (permission_id, creator_id, name, description, permission_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, permID, userID, name, description, permissionJSON, createdAt)
	batch.Query(`
		INSERT INTO custom_share_permissions_by_user (creator_id, permission_id, name, description, permission_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, userID, permID, name, description, permissionJSON, createdAt)
	return batch.Exec()
}

// updateCustomSharePermission handles the dual-writes for updating a custom share permission.
func updateCustomSharePermission(sess *gocql.Session, permID, userID, name, description, permissionJSON string) error {
	batch := sess.Batch(gocql.LoggedBatch)
	batch.Query(`
		UPDATE custom_share_permissions SET name = ?, description = ?, permission_json = ? WHERE permission_id = ?
	`, name, description, permissionJSON, permID)
	batch.Query(`
		UPDATE custom_share_permissions_by_user SET name = ?, description = ?, permission_json = ? WHERE creator_id = ? AND permission_id = ?
	`, name, description, permissionJSON, userID, permID)
	return batch.Exec()
}

// deleteCustomSharePermission handles the dual-writes for deleting a custom share permission.
func deleteCustomSharePermission(sess *gocql.Session, permID, userID string) error {
	batch := sess.Batch(gocql.LoggedBatch)
	batch.Query(`DELETE FROM custom_share_permissions WHERE permission_id = ?`, permID)
	batch.Query(`DELETE FROM custom_share_permissions_by_user WHERE creator_id = ? AND permission_id = ?`, userID, permID)
	return batch.Exec()
}

// createRepoTag creates a new repository tag in the database and updates the counter.
func createRepoTag(sess *gocql.Session, repoID gocql.UUID, name, color string, createdAt time.Time) (int, error) {
	var tagID int = 1
	err := sess.Query(`
		SELECT next_tag_id FROM repo_tag_counters WHERE repo_id = ?
	`, repoID).Scan(&tagID)

	if err != nil {
		tagID = 1
		err = sess.Query(`
			INSERT INTO repo_tag_counters (repo_id, next_tag_id) VALUES (?, ?)
		`, repoID, 2).Exec()
		if err != nil {
			return 0, fmt.Errorf("failed to initialize tag counter: %w", err)
		}
	} else {
		err = sess.Query(`
			UPDATE repo_tag_counters SET next_tag_id = ? WHERE repo_id = ?
		`, tagID+1, repoID).Exec()
		if err != nil {
			return 0, fmt.Errorf("failed to update tag counter: %w", err)
		}
	}

	err = sess.Query(`
		INSERT INTO repo_tags (repo_id, tag_id, name, color, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, repoID, tagID, name, color, createdAt).Exec()
	if err != nil {
		return 0, fmt.Errorf("failed to create tag: %w", err)
	}

	return tagID, nil
}

// updateRepoTag updates the name and color of an existing repository tag.
func updateRepoTag(sess *gocql.Session, repoID gocql.UUID, tagID int, name, color string) error {
	return sess.Query(`
		UPDATE repo_tags SET name = ?, color = ? WHERE repo_id = ? AND tag_id = ?
	`, name, color, repoID, tagID).Exec()
}

// deleteRepoTag deletes a repository tag and all its file_tags associations, cleaning counters.
func deleteRepoTag(sess *gocql.Session, repoID gocql.UUID, tagID int) error {
	iter := sess.Query(`
		SELECT file_path, file_tag_id FROM file_tags WHERE repo_id = ? AND tag_id = ? ALLOW FILTERING
	`, repoID, tagID).Iter()

	var filePath string
	var fileTagID int
	batch := sess.Batch(gocql.LoggedBatch)

	batch.Query(`
		DELETE FROM repo_tags WHERE repo_id = ? AND tag_id = ?
	`, repoID, tagID)

	for iter.Scan(&filePath, &fileTagID) {
		batch.Query(`
			DELETE FROM file_tags WHERE repo_id = ? AND file_path = ? AND tag_id = ?
		`, repoID, filePath, tagID)
		batch.Query(`
			DELETE FROM file_tags_by_id WHERE repo_id = ? AND file_tag_id = ?
		`, repoID, fileTagID)
	}
	iter.Close()

	if err := batch.Exec(); err != nil {
		return fmt.Errorf("failed to delete tag: %w", err)
	}

	if err := sess.Query(`
		DELETE FROM repo_tag_file_counts WHERE repo_id = ? AND tag_id = ?
	`, repoID, tagID).Exec(); err != nil {
		log.Printf("[DeleteRepoTag] failed to clean repo_tag_file_counts for repo %s tag %d: %v", repoID.String(), tagID, err)
	}

	return nil
}

// addFileTag adds a file tag mapping and increments the tag's file usage counter.
func addFileTag(sess *gocql.Session, repoID gocql.UUID, filePath string, repoTagID, fileTagID int, createdAt time.Time) error {
	batch := sess.Batch(gocql.LoggedBatch)
	batch.Query(`
		INSERT INTO file_tags (repo_id, file_path, tag_id, file_tag_id, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, repoID, filePath, repoTagID, fileTagID, createdAt)
	batch.Query(`
		INSERT INTO file_tags_by_id (repo_id, file_tag_id, file_path, tag_id, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, repoID, fileTagID, filePath, repoTagID, createdAt)

	if err := batch.Exec(); err != nil {
		return fmt.Errorf("failed to add file tag: %w", err)
	}

	if err := sess.Query(`
		UPDATE repo_tag_file_counts SET file_count = file_count + 1
		WHERE repo_id = ? AND tag_id = ?
	`, repoID, repoTagID).Exec(); err != nil {
		log.Printf("[AddFileTag] failed to increment repo_tag_file_counts for repo %s tag %d: %v", repoID.String(), repoTagID, err)
	}
	return nil
}

// removeFileTag removes a file tag mapping and decrements the tag's file usage counter.
func removeFileTag(sess *gocql.Session, repoID gocql.UUID, fileTagID int) error {
	var filePath string
	var tagID int
	if err := sess.Query(`
		SELECT file_path, tag_id FROM file_tags_by_id WHERE repo_id = ? AND file_tag_id = ?
	`, repoID, fileTagID).Scan(&filePath, &tagID); err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return errFileTagNotFound
		}
		return fmt.Errorf("failed to lookup file tag: %w", err)
	}

	batch := sess.Batch(gocql.LoggedBatch)
	batch.Query(`
		DELETE FROM file_tags WHERE repo_id = ? AND file_path = ? AND tag_id = ?
	`, repoID, filePath, tagID)
	batch.Query(`
		DELETE FROM file_tags_by_id WHERE repo_id = ? AND file_tag_id = ?
	`, repoID, fileTagID)

	if err := batch.Exec(); err != nil {
		return fmt.Errorf("failed to remove file tag: %w", err)
	}

	if err := sess.Query(`
		UPDATE repo_tag_file_counts SET file_count = file_count - 1
		WHERE repo_id = ? AND tag_id = ?
	`, repoID, tagID).Exec(); err != nil {
		log.Printf("[RemoveFileTag] failed to decrement repo_tag_file_counts for repo %s tag %d: %v", repoID.String(), tagID, err)
	}
	return nil
}

// starFile adds a file to the user's starred files list. The canonical row
// (partitioned by user_id) and the starred_files_by_repo projection (partitioned
// by repo_id, used by GC library cleanup) are written in one batch so the
// reverse lookup stays consistent without a secondary index.
func starFile(sess *gocql.Session, userID, repoID, path string, starredAt time.Time) error {
	batch := sess.Batch(gocql.LoggedBatch)
	batch.Query(`
		INSERT INTO starred_files (user_id, repo_id, path, starred_at)
		VALUES (?, ?, ?, ?)
	`, userID, repoID, path, starredAt)
	batch.Query(`
		INSERT INTO starred_files_by_repo (repo_id, user_id, path, starred_at)
		VALUES (?, ?, ?, ?)
	`, repoID, userID, path, starredAt)
	return batch.Exec()
}

// unstarFile removes a file from the user's starred files list, tearing down the
// canonical row and the starred_files_by_repo projection together.
func unstarFile(sess *gocql.Session, userID, repoID, path string) error {
	batch := sess.Batch(gocql.LoggedBatch)
	batch.Query(`
		DELETE FROM starred_files WHERE user_id = ? AND repo_id = ? AND path = ?
	`, userID, repoID, path)
	batch.Query(`
		DELETE FROM starred_files_by_repo WHERE repo_id = ? AND user_id = ? AND path = ?
	`, repoID, userID, path)
	return batch.Exec()
}
