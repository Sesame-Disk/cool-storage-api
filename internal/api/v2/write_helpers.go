package v2

import (
	"fmt"
	"log"
	"net/http"
	"time"

	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/traffic"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

// User/Org status constants — lifecycle state independent of role.
const (
	StatusActive      = "active"
	StatusDeactivated = "deactivated"
	StatusDeleted     = "deleted"
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
	batch := db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		INSERT INTO users (org_id, user_id, email, name, role, status, quota_bytes, used_bytes, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, orgID, userID, email, name, role, StatusActive, quotaBytes, usedBytes, createdAt)
	batch.Query(`
		INSERT INTO users_by_email (email, user_id, org_id)
		VALUES (?, ?, ?)
	`, email, userID, orgID)
	return batch.Exec()
}

func updateLibraryOwner(db interface{ Session() *gocql.Session }, orgID, libraryID, newOwnerID string, updatedAt time.Time) error {
	batch := db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		UPDATE libraries SET owner_id = ?, updated_at = ?
		WHERE org_id = ? AND library_id = ?
	`, newOwnerID, updatedAt, orgID, libraryID)
	batch.Query(`
		UPDATE libraries_by_id SET owner_id = ?
		WHERE library_id = ?
	`, newOwnerID, libraryID)
	if err := batch.Exec(); err != nil {
		return err
	}
	return dbpkg.SyncAdminLibraryReadModel(db.Session(), orgID, libraryID)
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
	batch := db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		INSERT INTO shares (
			library_id, share_id, shared_by, shared_to, shared_to_type,
			permission, created_at, expires_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, libraryID, shareID, sharedBy, sharedTo, sharedToType, permission, createdAt, expiresAt)
	batch.Query(`
		INSERT INTO shares_by_user (shared_to, library_id, shared_to_type, permission, shared_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, sharedTo, libraryID, sharedToType, permission, sharedBy, createdAt)
	if err := batch.Exec(); err != nil {
		return err
	}
	return dbpkg.SyncShareReadModel(db.Session(), libraryID, shareID)
}

func updateLibrarySharePermission(db interface{ Session() *gocql.Session }, libraryID, shareID, sharedTo, permission string) error {
	batch := db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		UPDATE shares SET permission = ? WHERE library_id = ? AND share_id = ?
	`, permission, libraryID, shareID)
	batch.Query(`
		UPDATE shares_by_user SET permission = ? WHERE shared_to = ? AND library_id = ?
	`, permission, sharedTo, libraryID)
	if err := batch.Exec(); err != nil {
		return err
	}
	return dbpkg.SyncShareReadModel(db.Session(), libraryID, shareID)
}

func deleteLibraryShare(db interface{ Session() *gocql.Session }, libraryID, shareID, sharedTo string) error {
	row, err := dbpkg.ReadShareReadModelRow(db.Session(), libraryID, shareID)
	if err != nil {
		return err
	}
	batch := db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		DELETE FROM shares WHERE library_id = ? AND share_id = ?
	`, libraryID, shareID)
	batch.Query(`
		DELETE FROM shares_by_user WHERE shared_to = ? AND library_id = ?
	`, sharedTo, libraryID)
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
	batch := db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		UPDATE groups SET name = ?, updated_at = ? WHERE org_id = ? AND group_id = ?
	`, newName, updatedAt, orgID, groupID)
	batch.Query(`
		UPDATE groups_by_id SET name = ? WHERE group_id = ?
	`, newName, groupID)
	if err := batch.Exec(); err != nil {
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

	var failedCount int
	for i := 0; i < len(memberIDs); i += 50 {
		end := i + 50
		if end > len(memberIDs) {
			end = len(memberIDs)
		}
		memberBatch := db.Session().Batch(gocql.UnloggedBatch)
		batchSize := end - i
		for _, userID := range memberIDs[i:end] {
			memberBatch.Query(`
				UPDATE groups_by_member SET group_name = ? WHERE org_id = ? AND user_id = ? AND group_id = ?
			`, newName, orgID, userID, groupID)
		}
		if err := memberBatch.Exec(); err != nil {
			failedCount += batchSize
		}
	}

	if failedCount > 0 {
		return fmt.Errorf("group renamed but failed to update %d member index rows", failedCount)
	}

	return syncAdminGroupReadModel(db, orgID, groupID)
}

// SessionInvalidator can revoke all sessions for a given user.
// Implemented by auth.SessionManager; used by admin handlers to kill sessions
// when a user is deactivated or deleted.
type SessionInvalidator interface {
	InvalidateUserSessions(orgID, userID string) error
}

// deactivateUser marks a user as deactivated and, in a background goroutine,
// kills all their active sessions and disables their share links.
func deactivateUser(db interface{ Session() *gocql.Session }, si SessionInvalidator, orgID, userID string) error {
	if err := db.Session().Query(`
		UPDATE users SET status = ? WHERE org_id = ? AND user_id = ?
	`, StatusDeactivated, orgID, userID).Exec(); err != nil {
		return err
	}
	go func() {
		if si != nil {
			si.InvalidateUserSessions(orgID, userID) //nolint:errcheck
		}
		setUserShareLinksActive(db, orgID, userID, false)
	}()
	return nil
}

// activateUser marks a user as active, clears deleted_at, and in a background
// goroutine re-enables their share links. Works for both reactivation
// (deactivated → active) and restore (deleted → active).
func activateUser(db interface{ Session() *gocql.Session }, orgID, userID string) error {
	if err := db.Session().Query(`
		UPDATE users SET status = ?, deleted_at = ? WHERE org_id = ? AND user_id = ?
	`, StatusActive, nil, orgID, userID).Exec(); err != nil {
		return err
	}
	go setUserShareLinksActive(db, orgID, userID, true)
	return nil
}

// softDeleteUser marks a user as deleted and, in a background goroutine,
// kills all their active sessions and disables their share links.
func softDeleteUser(db interface{ Session() *gocql.Session }, si SessionInvalidator, orgID, userID string, deletedAt time.Time) error {
	if err := db.Session().Query(`
		UPDATE users SET status = ?, deleted_at = ? WHERE org_id = ? AND user_id = ?
	`, StatusDeleted, deletedAt, orgID, userID).Exec(); err != nil {
		return err
	}
	go func() {
		if si != nil {
			si.InvalidateUserSessions(orgID, userID) //nolint:errcheck
		}
		setUserShareLinksActive(db, orgID, userID, false)
	}()
	return nil
}

// deactivateOrg marks an org as deactivated and, in a background goroutine,
// kills all sessions for every user in the org and disables all org share links.
func deactivateOrg(db interface{ Session() *gocql.Session }, si SessionInvalidator, orgID string) error {
	if err := db.Session().Query(`
		UPDATE organizations SET status = ? WHERE org_id = ?
	`, StatusDeactivated, orgID).Exec(); err != nil {
		return err
	}
	go func() {
		invalidateOrgSessions(db, si, orgID)
		setOrgShareLinksActive(db, orgID, false)
	}()
	return nil
}

// activateOrg marks an org as active, clears deleted_at, and in a background
// goroutine re-enables all org share links. Works for both reactivation
// (deactivated → active) and restore (deleted → active).
func activateOrg(db interface{ Session() *gocql.Session }, orgID string) error {
	if err := db.Session().Query(`
		UPDATE organizations SET status = ?, deleted_at = ? WHERE org_id = ?
	`, StatusActive, nil, orgID).Exec(); err != nil {
		return err
	}
	go setOrgShareLinksActive(db, orgID, true)
	return nil
}

// softDeleteOrg marks an org as deleted and, in a background goroutine,
// kills all sessions for every user in the org and disables all org share links.
func softDeleteOrg(db interface{ Session() *gocql.Session }, si SessionInvalidator, orgID string, deletedAt time.Time) error {
	if err := db.Session().Query(`
		UPDATE organizations SET status = ?, deleted_at = ? WHERE org_id = ?
	`, StatusDeleted, deletedAt, orgID).Exec(); err != nil {
		return err
	}
	go func() {
		invalidateOrgSessions(db, si, orgID)
		setOrgShareLinksActive(db, orgID, false)
	}()
	return nil
}

// invalidateOrgSessions invalidates sessions for every user in an org.
// Used when an org is deactivated or soft-deleted.
func invalidateOrgSessions(dbSess interface{ Session() *gocql.Session }, si SessionInvalidator, orgID string) {
	if si == nil {
		return
	}
	iter := dbSess.Session().Query(`SELECT user_id FROM users WHERE org_id = ?`, orgID).Iter()
	var uid string
	for iter.Scan(&uid) {
		si.InvalidateUserSessions(orgID, uid) //nolint:errcheck
	}
	iter.Close() //nolint:errcheck
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
	if err := db.Session().Query(`SELECT link_type FROM share_links WHERE link_token = ?`, token).Scan(&linkType); err != nil {
		log.Printf("[deleteConsumedShareLink] lookup failed for token %s: %v", token, err)
		return
	}

	batch := db.Session().Batch(gocql.UnloggedBatch) // deletes are idempotent
	batch.Query(`DELETE FROM share_links WHERE link_token = ?`, token)
	batch.Query(`DELETE FROM share_links_by_creator WHERE org_id = ? AND created_by = ? AND created_at = ? AND link_token = ?`,
		orgID, createdBy, createdAt, token)
	batch.Query(`DELETE FROM share_links_by_library WHERE org_id = ? AND library_id = ? AND link_token = ?`,
		orgID, libraryID, token)
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

func rollbackNewLibrary(db interface{ Session() *gocql.Session }, orgID, libraryID string) error {
	batch := db.Session().Batch(gocql.LoggedBatch)
	batch.Query(`
		DELETE FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, libraryID)
	batch.Query(`
		DELETE FROM libraries_by_id WHERE library_id = ?
	`, libraryID)
	batch.Query(`
		DELETE FROM fs_objects WHERE library_id = ?
	`, libraryID)
	batch.Query(`
		DELETE FROM commits WHERE library_id = ?
	`, libraryID)
	return batch.Exec()
}

func syncAdminLibraryReadModel(db interface{ Session() *gocql.Session }, orgID, libraryID string) error {
	return dbpkg.SyncAdminLibraryReadModel(db.Session(), orgID, libraryID)
}

func addDeleteAdminLibraryReadModelQueries(db interface{ Session() *gocql.Session }, batch *gocql.Batch, libraryID string) error {
	state, err := dbpkg.ReadAdminLibraryProjectionState(db.Session(), libraryID)
	if err == gocql.ErrNotFound {
		return nil
	}
	if err != nil {
		return err
	}

	dbpkg.AddDeleteAdminLibraryReadModelQuery(batch, state)
	batch.Query(`DELETE FROM library_admin_projection_state WHERE library_id = ?`, libraryID)
	return nil
}

// ── Library soft-delete / restore with storage accounting ─────────────────────

// softDeleteLibrary marks a library as deleted and adjusts storage counters.
// This is the canonical soft-delete path — all callers (user, admin, GC) must
// route through equivalent logic to keep storage accounting consistent.
//
// The lib-scope counter is left intact so restoreDeletedLibrary can reverse the
// operation. Permanent delete (or GC cascade) cleans up the lib-scope row via
// traffic.DeleteLibraryStorageCounter.
func softDeleteLibrary(db interface{ Session() *gocql.Session }, orgID, ownerID, deletedBy, libraryID string) error {
	now := time.Now()
	if err := db.Session().Query(`
		UPDATE libraries SET deleted_at = ?, deleted_by = ?
		WHERE org_id = ? AND library_id = ?`,
		now, deletedBy, orgID, libraryID,
	).Exec(); err != nil {
		return fmt.Errorf("soft-delete library: %w", err)
	}

	// Read the live lib-scope counter and subtract from aggregate scopes.
	traffic.AdjustAggregateStorageCounters(db, orgID, ownerID, libraryID, false)
	return syncAdminLibraryReadModel(db, orgID, libraryID)
}

// restoreDeletedLibrary clears deleted_at and re-adds the library's storage to
// aggregate counters. Mirror image of softDeleteLibrary.
func restoreDeletedLibrary(db interface{ Session() *gocql.Session }, orgID, ownerID, libraryID string) error {
	if err := db.Session().Query(`
		DELETE deleted_at, deleted_by FROM libraries
		WHERE org_id = ? AND library_id = ?`,
		orgID, libraryID,
	).Exec(); err != nil {
		return fmt.Errorf("restore library: %w", err)
	}

	// Re-add the library's storage to aggregates.
	traffic.AdjustAggregateStorageCounters(db, orgID, ownerID, libraryID, true)
	return syncAdminLibraryReadModel(db, orgID, libraryID)
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
