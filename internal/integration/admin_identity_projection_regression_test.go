//go:build integration

package integration

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"
)

type adminOrganizationProjectionState struct {
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

type adminUserProjectionState struct {
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

type adminUserUpdateSnapshot struct {
	Name                 string
	Role                 string
	Status               string
	QuotaTotal           int64
	TrafficUploadQuota   int64
	TrafficDownloadQuota int64
}

func TestAdminIdentityProjectionRegression_CreateAndUpdateOrganization(t *testing.T) {
	name := fmt.Sprintf("inttest-admin-org-projection-%d", time.Now().UnixNano())
	ownerEmail := fmt.Sprintf("inttest-admin-owner-%d@sesamefs.local", time.Now().UnixNano())
	orgID := createAdminIdentityTestOrganization(t, name, ownerEmail)

	waitForIntegrationCondition(t, "created organization to appear in admin org projection", func() bool {
		row, ok := adminOrganizationProjectionByID(t, orgID)
		return ok && row.Name == name && row.OwnerEmail == ownerEmail && row.UsersCount == 1 && row.Status == "active"
	})
	waitForIntegrationCondition(t, "created organization to appear in admin org list", func() bool {
		return adminOrganizationPresentInList(t, orgID, name, ownerEmail)
	})

	updatedName := name + "-renamed"
	updatedQuota := int64(1099511627776 + 4096)
	updateResp := superadminClient.PutJSON(t, "/api/v2.1/admin/organizations/"+orgID+"/", map[string]interface{}{
		"name":          updatedName,
		"storage_quota": updatedQuota,
	})
	expectStatus(t, updateResp, http.StatusOK)
	updateResp.Body.Close()

	waitForIntegrationCondition(t, "updated organization to stay aligned with admin org projection", func() bool {
		row, ok := adminOrganizationProjectionByID(t, orgID)
		return ok && row.Name == updatedName && row.StorageQuota == updatedQuota && row.OwnerEmail == ownerEmail && row.UsersCount == 1
	})
	waitForIntegrationCondition(t, "updated organization to stay visible in admin org list", func() bool {
		return adminOrganizationPresentInList(t, orgID, updatedName, ownerEmail)
	})
}

func TestAdminIdentityProjectionRegression_AdminAddOrgUser(t *testing.T) {
	orgName := fmt.Sprintf("inttest-admin-org-user-%d", time.Now().UnixNano())
	ownerEmail := fmt.Sprintf("inttest-admin-org-user-owner-%d@sesamefs.local", time.Now().UnixNano())
	orgID := createAdminIdentityTestOrganization(t, orgName, ownerEmail)
	userEmail := fmt.Sprintf("inttest-admin-org-user-member-%d@sesamefs.local", time.Now().UnixNano())
	userName := "projection-member"

	createResp := superadminClient.PostJSON(t, "/api/v2.1/admin/organizations/"+orgID+"/users/", map[string]string{
		"email": userEmail,
		"name":  userName,
	})
	expectStatus(t, createResp, http.StatusCreated)
	createResp.Body.Close()

	waitForIntegrationCondition(t, "new admin-added user to appear in user projection", func() bool {
		row, ok := adminUserProjectionByEmail(t, userEmail)
		return ok && row.OrgID == orgID && row.Name == userName && row.Role == "user" && row.Status == "active"
	})
	waitForIntegrationCondition(t, "new admin-added user to increment org projection user count", func() bool {
		row, ok := adminOrganizationProjectionByID(t, orgID)
		return ok && row.UsersCount == 2 && row.OwnerEmail == ownerEmail
	})
	waitForIntegrationCondition(t, "new admin-added user to appear in admin user search", func() bool {
		return adminUserPresentInSearch(t, userEmail, "user")
	})
}

func TestAdminIdentityProjectionRegression_TransferOrgOwnership(t *testing.T) {
	orgName := fmt.Sprintf("inttest-admin-org-transfer-%d", time.Now().UnixNano())
	originalOwnerEmail := fmt.Sprintf("inttest-admin-org-transfer-owner-%d@sesamefs.local", time.Now().UnixNano())
	orgID := createAdminIdentityTestOrganization(t, orgName, originalOwnerEmail)
	newOwnerEmail := fmt.Sprintf("inttest-admin-org-transfer-admin-%d@sesamefs.local", time.Now().UnixNano())

	createResp := superadminClient.PostJSON(t, "/api/v2.1/admin/organizations/"+orgID+"/users/", map[string]string{
		"email": newOwnerEmail,
		"name":  "projection-admin",
	})
	expectStatus(t, createResp, http.StatusCreated)
	createResp.Body.Close()

	promoteResp := superadminClient.PutJSON(t, "/api/v2.1/admin/users/"+url.PathEscape(newOwnerEmail)+"/", map[string]string{
		"role": "admin",
	})
	expectStatus(t, promoteResp, http.StatusOK)
	promoteResp.Body.Close()
	waitForIntegrationCondition(t, "future owner to become admin before transfer", func() bool {
		row, ok := adminUserProjectionByEmail(t, newOwnerEmail)
		return ok && row.Role == "admin"
	})

	transferResp := superadminClient.PutJSON(t, "/api/v2.1/org/"+orgID+"/admin/transfer-ownership/", map[string]string{
		"new_owner": newOwnerEmail,
	})
	expectStatus(t, transferResp, http.StatusOK)
	transferResp.Body.Close()

	waitForIntegrationCondition(t, "ownership transfer to update admin identity projections", func() bool {
		orgRow, ok := adminOrganizationProjectionByID(t, orgID)
		if !ok || orgRow.OwnerEmail != newOwnerEmail {
			return false
		}
		oldOwnerRow, ok := adminUserProjectionByEmail(t, originalOwnerEmail)
		if !ok || oldOwnerRow.Role != "admin" {
			return false
		}
		newOwnerRow, ok := adminUserProjectionByEmail(t, newOwnerEmail)
		return ok && newOwnerRow.Role == "owner"
	})
	waitForIntegrationCondition(t, "ownership transfer to update admin org list owner", func() bool {
		return adminOrganizationPresentInList(t, orgID, orgName, newOwnerEmail)
	})
}

func TestAdminIdentityProjectionRegression_HardDeleteUser(t *testing.T) {
	orgName := fmt.Sprintf("inttest-admin-hard-delete-user-org-%d", time.Now().UnixNano())
	ownerEmail := fmt.Sprintf("inttest-admin-hard-delete-user-owner-%d@sesamefs.local", time.Now().UnixNano())
	orgID := createAdminIdentityTestOrganization(t, orgName, ownerEmail)
	userEmail := fmt.Sprintf("inttest-admin-hard-delete-user-member-%d@sesamefs.local", time.Now().UnixNano())
	userName := "hard-delete-member"

	createResp := superadminClient.PostJSON(t, "/api/v2.1/admin/organizations/"+orgID+"/users/", map[string]string{
		"email": userEmail,
		"name":  userName,
	})
	expectStatus(t, createResp, http.StatusCreated)
	createResp.Body.Close()

	waitForIntegrationCondition(t, "hard-delete user setup to appear in projections", func() bool {
		row, ok := adminUserProjectionByEmail(t, userEmail)
		if !ok || row.Status != "active" {
			return false
		}
		orgRow, ok := adminOrganizationProjectionByID(t, orgID)
		return ok && orgRow.UsersCount == 2 && orgRow.OwnerEmail == ownerEmail
	})

	userID, found := lookupUserIDByEmail(t, userEmail)
	if !found {
		t.Fatalf("expected user_id for %s", userEmail)
	}

	deleteResp := superadminClient.Delete(t, "/api/v2.1/admin/organizations/"+orgID+"/users/"+url.PathEscape(userEmail)+"/")
	expectStatus(t, deleteResp, http.StatusOK)
	deleteResp.Body.Close()

	waitForIntegrationCondition(t, "soft-deleted user to remain in canonical deleted state before GC hard delete", func() bool {
		return canonicalUserStatusIs(t, orgID, userID, "deleted")
	})

	store := gcpkg.NewCassandraStore(shareProjectionDBForTest(t))
	if err := store.HardDeleteUser(mustParseUUID(t, orgID), mustParseUUID(t, userID), userEmail); err != nil {
		t.Fatalf("hard delete user failed: %v", err)
	}

	waitForIntegrationCondition(t, "hard-deleted user to disappear from canonical rows and admin projections", func() bool {
		if canonicalUserExists(t, orgID, userID, userEmail) {
			return false
		}
		if _, ok := adminUserProjectionByID(t, orgID, userID); ok {
			return false
		}
		orgRow, ok := adminOrganizationProjectionByID(t, orgID)
		if !ok || orgRow.UsersCount != 1 || orgRow.OwnerEmail != ownerEmail {
			return false
		}
		return true
	})

	waitForIntegrationCondition(t, "hard-deleted user to disappear from admin user search", func() bool {
		return !adminUserPresentInSearch(t, userEmail, "user")
	})
}

func TestAdminIdentityProjectionRegression_GCDeletedPlatformUser(t *testing.T) {
	userEmail := fmt.Sprintf("inttest-platform-deleted-user-%d@sesamefs.local", time.Now().UnixNano())
	userName := "platform-gc-user"

	createResp := superadminClient.PostJSON(t, "/api/v2.1/admin/users/", map[string]string{
		"email": userEmail,
		"name":  userName,
	})
	expectStatus(t, createResp, http.StatusCreated)
	createResp.Body.Close()

	userID, found := lookupUserIDByEmail(t, userEmail)
	if !found {
		t.Fatalf("expected user_id for %s", userEmail)
	}

	t.Cleanup(func() {
		if !canonicalUserExists(t, platformOrgID, userID, userEmail) {
			return
		}
		store := gcpkg.NewCassandraStore(shareProjectionDBForTest(t))
		if err := store.HardDeleteUser(mustParseUUID(t, platformOrgID), mustParseUUID(t, userID), userEmail); err != nil {
			t.Fatalf("cleanup hard delete platform user %s failed: %v", userEmail, err)
		}
	})

	deleteResp := superadminClient.Delete(t, "/api/v2.1/admin/users/"+url.PathEscape(userEmail)+"/")
	expectStatus(t, deleteResp, http.StatusOK)
	deleteResp.Body.Close()

	waitForIntegrationCondition(t, "soft-deleted platform user to appear in deleted admin user list before GC hard delete", func() bool {
		return adminUserPresentInStatusList(t, "deleted", userEmail)
	})

	waitForIntegrationCondition(t, "soft-deleted platform user to remain in canonical deleted state before GC hard delete", func() bool {
		return canonicalUserStatusIs(t, platformOrgID, userID, "deleted")
	})

	store := gcpkg.NewCassandraStore(shareProjectionDBForTest(t))
	deletedUsers, err := store.ListDeletedUsersExpired(0)
	if err != nil {
		t.Fatalf("list deleted users expired failed: %v", err)
	}

	foundDeletedUser := false
	for _, item := range deletedUsers {
		if item.OrgID == mustParseUUID(t, platformOrgID) && item.UserID == mustParseUUID(t, userID) && item.Email == userEmail {
			foundDeletedUser = true
			break
		}
	}
	if !foundDeletedUser {
		t.Fatalf("expected deleted platform user %s (%s) to be returned by ListDeletedUsersExpired", userEmail, userID)
	}

	if err := store.HardDeleteUser(mustParseUUID(t, platformOrgID), mustParseUUID(t, userID), userEmail); err != nil {
		t.Fatalf("hard delete platform user failed: %v", err)
	}

	waitForIntegrationCondition(t, "hard-deleted platform user to disappear from canonical and deleted admin user list", func() bool {
		if canonicalUserExists(t, platformOrgID, userID, userEmail) {
			return false
		}
		if _, ok := adminUserProjectionByID(t, platformOrgID, userID); ok {
			return false
		}
		return !adminUserPresentInStatusList(t, "deleted", userEmail)
	})
}

func TestAdminIdentityProjectionRegression_HardDeleteOrganization(t *testing.T) {
	orgName := fmt.Sprintf("inttest-admin-hard-delete-org-%d", time.Now().UnixNano())
	ownerEmail := fmt.Sprintf("inttest-admin-hard-delete-org-owner-%d@sesamefs.local", time.Now().UnixNano())
	orgID := createAdminIdentityTestOrganization(t, orgName, ownerEmail)
	ownerUserID, found := lookupUserIDByEmail(t, ownerEmail)
	if !found {
		t.Fatalf("expected owner user_id for %s", ownerEmail)
	}

	t.Cleanup(func() {
		store := gcpkg.NewCassandraStore(shareProjectionDBForTest(t))
		if canonicalUserExists(t, orgID, ownerUserID, ownerEmail) {
			if err := store.HardDeleteUser(mustParseUUID(t, orgID), mustParseUUID(t, ownerUserID), ownerEmail); err != nil {
				t.Fatalf("cleanup hard delete owner user %s failed: %v", ownerEmail, err)
			}
		}
		if canonicalOrganizationExists(t, orgID) {
			if err := store.HardDeleteOrg(mustParseUUID(t, orgID)); err != nil {
				t.Fatalf("cleanup hard delete organization %s failed: %v", orgID, err)
			}
		}
	})

	deleteResp := superadminClient.Delete(t, "/api/v2.1/admin/organizations/"+orgID+"/")
	expectStatus(t, deleteResp, http.StatusOK)
	deleteResp.Body.Close()

	waitForIntegrationCondition(t, "soft-deleted organization to leave the active admin org list and appear in deleted status list", func() bool {
		if adminOrganizationPresentInStatusList(t, "active", orgID, orgName, ownerEmail) {
			return false
		}
		return adminOrganizationPresentInStatusList(t, "deleted", orgID, orgName, ownerEmail)
	})

	waitForIntegrationCondition(t, "soft-deleted organization to keep its deleted marker before GC hard delete", func() bool {
		return deletedOrganizationMarkerExists(t, orgID)
	})

	store := gcpkg.NewCassandraStore(shareProjectionDBForTest(t))
	queue := gcpkg.NewQueue(store)
	queuedAt := deletedOrganizationMarkerDeletedAt(t, orgID)
	orgUUID := mustParseUUID(t, orgID)
	if err := store.EnqueueItem(orgUUID, queuedAt, gcpkg.ItemOrgCascade, orgID, uuid.Nil, "", 0); err != nil {
		t.Fatalf("enqueue org cascade failed: %v", err)
	}
	worker := gcpkg.NewWorker(store, nil, queue, 100, 0, false, &gcpkg.Stats{})
	processed, err := worker.ProcessOnce(context.Background())
	if err != nil {
		t.Fatalf("process org cascade failed: %v", err)
	}
	if processed == 0 {
		t.Fatalf("expected org cascade worker to process at least one item")
	}

	waitForIntegrationCondition(t, "hard-deleted organization to disappear from canonical and admin projections", func() bool {
		if canonicalOrganizationExists(t, orgID) {
			return false
		}
		if deletedOrganizationMarkerExists(t, orgID) {
			return false
		}
		if _, ok := adminOrganizationProjectionByID(t, orgID); ok {
			return false
		}
		if canonicalUserExists(t, orgID, ownerUserID, ownerEmail) {
			return false
		}
		return !adminOrganizationPresentInStatusList(t, "deleted", orgID, orgName, ownerEmail) && !adminUserPresentInStatusList(t, "active", ownerEmail)
	})
}

func TestAdminIdentityProjectionRegression_RestoreOrganization(t *testing.T) {
	orgName := fmt.Sprintf("inttest-admin-restore-org-%d", time.Now().UnixNano())
	ownerEmail := fmt.Sprintf("inttest-admin-restore-org-owner-%d@sesamefs.local", time.Now().UnixNano())
	orgID := createAdminIdentityTestOrganization(t, orgName, ownerEmail)

	deleteResp := superadminClient.Delete(t, "/api/v2.1/admin/organizations/"+orgID+"/")
	expectStatus(t, deleteResp, http.StatusOK)
	deleteResp.Body.Close()

	waitForIntegrationCondition(t, "soft-deleted organization to reach deleted canonical/admin state before restore", func() bool {
		row, ok := adminOrganizationProjectionByID(t, orgID)
		return ok && row.Status == "deleted" && row.DeletedAt != nil && canonicalOrganizationStatusIs(t, orgID, "deleted") && deletedOrganizationMarkerExists(t, orgID)
	})

	restoreResp := superadminClient.Do(t, http.MethodPost, "/api/v2.1/admin/organizations/"+orgID+"/restore/", nil)
	expectStatus(t, restoreResp, http.StatusOK)
	restoreResp.Body.Close()

	waitForIntegrationCondition(t, "restored organization to return to active canonical/admin state", func() bool {
		row, ok := adminOrganizationProjectionByID(t, orgID)
		return ok && row.Status == "active" && row.DeletedAt == nil && canonicalOrganizationStatusIs(t, orgID, "active") && !deletedOrganizationMarkerExists(t, orgID) && adminOrganizationPresentInStatusList(t, "active", orgID, orgName, ownerEmail)
	})
}

func TestAdminIdentityProjectionRegression_RestoreOrganizationRepairsPartialActivation(t *testing.T) {
	orgName := fmt.Sprintf("inttest-admin-restore-repair-org-%d", time.Now().UnixNano())
	ownerEmail := fmt.Sprintf("inttest-admin-restore-repair-owner-%d@sesamefs.local", time.Now().UnixNano())
	orgID := createAdminIdentityTestOrganization(t, orgName, ownerEmail)

	deleteResp := superadminClient.Delete(t, "/api/v2.1/admin/organizations/"+orgID+"/")
	expectStatus(t, deleteResp, http.StatusOK)
	deleteResp.Body.Close()

	waitForIntegrationCondition(t, "soft-deleted organization to reach deleted canonical/admin state before repair test", func() bool {
		row, ok := adminOrganizationProjectionByID(t, orgID)
		return ok && row.Status == "deleted" && row.DeletedAt != nil && canonicalOrganizationStatusIs(t, orgID, "deleted") && deletedOrganizationMarkerExists(t, orgID)
	})

	session := shareProjectionDBForTest(t).Session()
	if err := session.Query(`UPDATE organizations SET status = ?, deleted_at = ? WHERE org_id = ?`, "active", nil, orgID).Exec(); err != nil {
		t.Fatalf("set org %s canonical state to active failed: %v", orgID, err)
	}

	restoreResp := superadminClient.Do(t, http.MethodPost, "/api/v2.1/admin/organizations/"+orgID+"/restore/", nil)
	expectStatus(t, restoreResp, http.StatusOK)
	restoreResp.Body.Close()

	waitForIntegrationCondition(t, "restore retry to repair partial activation state", func() bool {
		row, ok := adminOrganizationProjectionByID(t, orgID)
		return ok && row.Status == "active" && row.DeletedAt == nil && canonicalOrganizationStatusIs(t, orgID, "active") && !deletedOrganizationMarkerExists(t, orgID) && adminOrganizationPresentInStatusList(t, "active", orgID, orgName, ownerEmail) && !adminOrganizationPresentInStatusList(t, "deleted", orgID, orgName, ownerEmail)
	})
}

func TestAdminIdentityProjectionRegression_RestoreRepairReenablesLifecycleDisabledLinks(t *testing.T) {
	orgName := fmt.Sprintf("inttest-admin-restore-link-repair-org-%d", time.Now().UnixNano())
	ownerEmail := fmt.Sprintf("inttest-admin-restore-link-repair-owner-%d@sesamefs.local", time.Now().UnixNano())
	orgID := createAdminIdentityTestOrganization(t, orgName, ownerEmail)

	deleteResp := superadminClient.Delete(t, "/api/v2.1/admin/organizations/"+orgID+"/")
	expectStatus(t, deleteResp, http.StatusOK)
	deleteResp.Body.Close()

	waitForIntegrationCondition(t, "soft-deleted organization to reach deleted canonical/admin state before inactive-link repair test", func() bool {
		row, ok := adminOrganizationProjectionByID(t, orgID)
		return ok && row.Status == "deleted" && row.DeletedAt != nil && canonicalOrganizationStatusIs(t, orgID, "deleted") && deletedOrganizationMarkerExists(t, orgID)
	})

	session := shareProjectionDBForTest(t).Session()
	createdAt := time.Now().UTC().Truncate(time.Millisecond)
	linkType := "share"
	token := fmt.Sprintf("inttest-inactive-link-%d", time.Now().UnixNano())
	libraryID := uuid.New().String()
	createdBy := uuid.New().String()
	batch := session.Batch(gocql.LoggedBatch)
	batch.Query(`
		INSERT INTO share_links (
			link_token, link_type, org_id, library_id, file_path, created_by,
			permission, active, view_count, download_count, upload_count,
			max_downloads, single_use, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, token, linkType, orgID, libraryID, "/", createdBy, "preview_download", false, 0, 0, 0, 0, false, createdAt)
	batch.Query(`
		INSERT INTO share_links_by_creator (
			org_id, created_by, created_at, link_token, link_type, library_id,
			file_path, permission, single_use, active, view_count,
			download_count, upload_count, max_downloads, has_password
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, orgID, createdBy, createdAt, token, linkType, libraryID, "/", "preview_download", false, false, 0, 0, 0, 0, false)
	dbpkg.AddUpsertAdminLinkReadModelQuery(batch, token, linkType, orgID, libraryID, "/", createdBy, "preview_download", "Inactive Link Repo", "Inactive Link Repo", ownerEmail, ownerEmail, nil, false, false, 0, 0, 0, createdAt)
	if err := batch.Exec(); err != nil {
		t.Fatalf("seed inactive share link rows: %v", err)
	}
	t.Cleanup(func() {
		bucketDay := dbpkg.AdminLinkBucketDay(createdAt)
		_ = session.Query(`DELETE FROM admin_links_by_created WHERE link_type = ? AND bucket_day = ? AND created_at = ? AND org_id = ? AND link_token = ?`, linkType, bucketDay, createdAt, orgID, token).Exec()
		_ = session.Query(`DELETE FROM admin_links_by_org_created WHERE org_id = ? AND link_type = ? AND bucket_day = ? AND created_at = ? AND link_token = ?`, orgID, linkType, bucketDay, createdAt, token).Exec()
		_ = session.Query(`DELETE FROM admin_link_buckets_by_org WHERE org_id = ? AND link_type = ? AND bucket_day = ?`, orgID, linkType, bucketDay).Exec()
		_ = session.Query(`DELETE FROM share_links WHERE link_token = ?`, token).Exec()
		_ = session.Query(`DELETE FROM share_links_by_creator WHERE org_id = ? AND created_by = ? AND created_at = ? AND link_token = ?`, orgID, createdBy, createdAt, token).Exec()
	})

	if err := session.Query(`UPDATE organizations SET status = ?, deleted_at = ? WHERE org_id = ?`, "active", nil, orgID).Exec(); err != nil {
		t.Fatalf("set org %s canonical state to active failed: %v", orgID, err)
	}

	restoreResp := superadminClient.Do(t, http.MethodPost, "/api/v2.1/admin/organizations/"+orgID+"/restore/", nil)
	expectStatus(t, restoreResp, http.StatusOK)
	restoreResp.Body.Close()

	waitForIntegrationCondition(t, "restore retry to repair partial activation state with link reactivation", func() bool {
		row, ok := adminOrganizationProjectionByID(t, orgID)
		return ok && row.Status == "active" && row.DeletedAt == nil && canonicalOrganizationStatusIs(t, orgID, "active") && !deletedOrganizationMarkerExists(t, orgID) && adminOrgLinkActive(t, orgID, linkType, token, createdAt) && canonicalShareLinkActive(t, token) && creatorShareLinkActive(t, orgID, createdBy, token, createdAt)
	})
}

func TestAdminIdentityProjectionRegression_RestoreOrganizationRejectedWhilePurging(t *testing.T) {
	orgName := fmt.Sprintf("inttest-admin-restore-purging-org-%d", time.Now().UnixNano())
	ownerEmail := fmt.Sprintf("inttest-admin-restore-purging-owner-%d@sesamefs.local", time.Now().UnixNano())
	orgID := createAdminIdentityTestOrganization(t, orgName, ownerEmail)

	deleteResp := superadminClient.Delete(t, "/api/v2.1/admin/organizations/"+orgID+"/")
	expectStatus(t, deleteResp, http.StatusOK)
	deleteResp.Body.Close()

	waitForIntegrationCondition(t, "soft-deleted organization to reach deleted canonical state before purge guard test", func() bool {
		return canonicalOrganizationStatusIs(t, orgID, "deleted") && deletedOrganizationMarkerExists(t, orgID)
	})

	session := shareProjectionDBForTest(t).Session()
	if err := session.Query(`UPDATE organizations SET status = ? WHERE org_id = ?`, "purging", orgID).Exec(); err != nil {
		t.Fatalf("set org %s to purging failed: %v", orgID, err)
	}
	t.Cleanup(func() {
		_ = session.Query(`UPDATE organizations SET status = ? WHERE org_id = ?`, "deleted", orgID).Exec()
	})

	restoreResp := superadminClient.Do(t, http.MethodPost, "/api/v2.1/admin/organizations/"+orgID+"/restore/", nil)
	expectStatus(t, restoreResp, http.StatusBadRequest)
	body := responseJSON(t, restoreResp)
	if body["error"] != "organization is pending permanent deletion" {
		t.Fatalf("restore purging org error = %v, want %q", body["error"], "organization is pending permanent deletion")
	}
	if !canonicalOrganizationStatusIs(t, orgID, "purging") {
		t.Fatalf("expected canonical organization %s to remain purging after rejected restore", orgID)
	}
	if !deletedOrganizationMarkerExists(t, orgID) {
		t.Fatalf("expected deleted_organizations marker to remain for purging org %s", orgID)
	}
}

func TestAdminIdentityProjectionRegression_ReactivateOrganization(t *testing.T) {
	orgName := fmt.Sprintf("inttest-admin-reactivate-org-%d", time.Now().UnixNano())
	ownerEmail := fmt.Sprintf("inttest-admin-reactivate-org-owner-%d@sesamefs.local", time.Now().UnixNano())
	orgID := createAdminIdentityTestOrganization(t, orgName, ownerEmail)

	deactivateResp := superadminClient.Do(t, http.MethodPost, "/api/v2.1/admin/organizations/"+orgID+"/deactivate/", nil)
	expectStatus(t, deactivateResp, http.StatusOK)
	deactivateResp.Body.Close()

	waitForIntegrationCondition(t, "deactivated organization to reach deactivated canonical/admin state", func() bool {
		row, ok := adminOrganizationProjectionByID(t, orgID)
		return ok && row.Status == "deactivated" && row.DeletedAt == nil && canonicalOrganizationStatusIs(t, orgID, "deactivated")
	})

	reactivateResp := superadminClient.Do(t, http.MethodPost, "/api/v2.1/admin/organizations/"+orgID+"/reactivate/", nil)
	expectStatus(t, reactivateResp, http.StatusOK)
	reactivateResp.Body.Close()

	waitForIntegrationCondition(t, "reactivated organization to return to active canonical/admin state", func() bool {
		row, ok := adminOrganizationProjectionByID(t, orgID)
		return ok && row.Status == "active" && row.DeletedAt == nil && canonicalOrganizationStatusIs(t, orgID, "active") && adminOrganizationPresentInStatusList(t, "active", orgID, orgName, ownerEmail)
	})
}

func TestAdminIdentityProjectionRegression_ReactivateOrganizationRepairsPartialActivation(t *testing.T) {
	orgName := fmt.Sprintf("inttest-admin-reactivate-repair-org-%d", time.Now().UnixNano())
	ownerEmail := fmt.Sprintf("inttest-admin-reactivate-repair-owner-%d@sesamefs.local", time.Now().UnixNano())
	orgID := createAdminIdentityTestOrganization(t, orgName, ownerEmail)

	deactivateResp := superadminClient.Do(t, http.MethodPost, "/api/v2.1/admin/organizations/"+orgID+"/deactivate/", nil)
	expectStatus(t, deactivateResp, http.StatusOK)
	deactivateResp.Body.Close()

	waitForIntegrationCondition(t, "deactivated organization to reach deactivated canonical/admin state before repair test", func() bool {
		row, ok := adminOrganizationProjectionByID(t, orgID)
		return ok && row.Status == "deactivated" && row.DeletedAt == nil && canonicalOrganizationStatusIs(t, orgID, "deactivated")
	})

	session := shareProjectionDBForTest(t).Session()
	if err := session.Query(`UPDATE organizations SET status = ?, deleted_at = ? WHERE org_id = ?`, "active", nil, orgID).Exec(); err != nil {
		t.Fatalf("set org %s canonical state to active failed: %v", orgID, err)
	}

	reactivateResp := superadminClient.Do(t, http.MethodPost, "/api/v2.1/admin/organizations/"+orgID+"/reactivate/", nil)
	expectStatus(t, reactivateResp, http.StatusOK)
	reactivateResp.Body.Close()

	waitForIntegrationCondition(t, "reactivate retry to repair partial activation state", func() bool {
		row, ok := adminOrganizationProjectionByID(t, orgID)
		return ok && row.Status == "active" && row.DeletedAt == nil && canonicalOrganizationStatusIs(t, orgID, "active") && adminOrganizationPresentInStatusList(t, "active", orgID, orgName, ownerEmail) && !adminOrganizationPresentInStatusList(t, "deactivated", orgID, orgName, ownerEmail)
	})
}

func TestAdminIdentityProjectionRegression_UpdateUserByEmailBatch(t *testing.T) {
	orgName := fmt.Sprintf("inttest-admin-update-email-org-%d", time.Now().UnixNano())
	ownerEmail := fmt.Sprintf("inttest-admin-update-email-owner-%d@sesamefs.local", time.Now().UnixNano())
	orgID := createAdminIdentityTestOrganization(t, orgName, ownerEmail)
	setAdminIdentityTestOrgTrafficQuotas(t, orgID)
	userEmail := fmt.Sprintf("inttest-admin-update-email-user-%d@sesamefs.local", time.Now().UnixNano())

	createResp := superadminClient.PostJSON(t, "/api/v2.1/admin/organizations/"+orgID+"/users/", map[string]string{
		"email": userEmail,
		"name":  "update-email-before",
	})
	expectStatus(t, createResp, http.StatusCreated)
	createResp.Body.Close()

	updatedName := "update-email-after"
	updatedQuota := int64(456789)
	updatedUpload := int64(1200)
	updatedDownload := int64(3400)
	updateResp := superadminClient.PutJSON(t, "/api/v2.1/admin/users/"+url.PathEscape(userEmail)+"/", map[string]interface{}{
		"name":                   updatedName,
		"quota_total":            updatedQuota,
		"traffic_upload_quota":   updatedUpload,
		"traffic_download_quota": updatedDownload,
		"is_staff":               true,
		"is_active":              false,
	})
	expectStatus(t, updateResp, http.StatusOK)
	updateResp.Body.Close()

	waitForIntegrationCondition(t, "email-based admin update to align admin user projection immediately", func() bool {
		row, ok := adminUserProjectionByEmail(t, userEmail)
		return ok && row.OrgID == orgID && row.Name == updatedName && row.Role == "admin" && row.Status == "deactivated" && row.QuotaBytes == updatedQuota
	})
	waitForIntegrationCondition(t, "email-based admin update to remain visible in admin search with final role", func() bool {
		return adminUserPresentInSearch(t, userEmail, "admin")
	})
}

func TestAdminIdentityProjectionRegression_AdminUpdateOrgUserBatch(t *testing.T) {
	orgName := fmt.Sprintf("inttest-admin-update-org-user-%d", time.Now().UnixNano())
	ownerEmail := fmt.Sprintf("inttest-admin-update-org-owner-%d@sesamefs.local", time.Now().UnixNano())
	orgID := createAdminIdentityTestOrganization(t, orgName, ownerEmail)
	setAdminIdentityTestOrgTrafficQuotas(t, orgID)
	userEmail := fmt.Sprintf("inttest-admin-update-org-user-%d@sesamefs.local", time.Now().UnixNano())

	createResp := superadminClient.PostJSON(t, "/api/v2.1/admin/organizations/"+orgID+"/users/", map[string]string{
		"email": userEmail,
		"name":  "org-update-before",
	})
	expectStatus(t, createResp, http.StatusCreated)
	createResp.Body.Close()

	updatedName := "org-update-after"
	updatedQuota := int64(654321)
	updatedUpload := int64(2100)
	updatedDownload := int64(4300)
	updateResp := superadminClient.PutJSON(t, "/api/v2.1/admin/organizations/"+orgID+"/users/"+url.PathEscape(userEmail)+"/", map[string]interface{}{
		"name":                   updatedName,
		"role":                   "readonly",
		"quota_total":            updatedQuota,
		"traffic_upload_quota":   updatedUpload,
		"traffic_download_quota": updatedDownload,
		"active":                 false,
	})
	expectStatus(t, updateResp, http.StatusOK)
	updateResp.Body.Close()

	waitForIntegrationCondition(t, "org-scoped admin update to align admin user projection immediately", func() bool {
		row, ok := adminUserProjectionByEmail(t, userEmail)
		return ok && row.OrgID == orgID && row.Name == updatedName && row.Role == "readonly" && row.Status == "deactivated" && row.QuotaBytes == updatedQuota
	})
	waitForIntegrationCondition(t, "org-scoped admin update to remain visible in admin search with final role", func() bool {
		return adminUserPresentInSearch(t, userEmail, "readonly")
	})
}

func TestAdminIdentityProjectionRegression_OrgAdminUpdateUserBatchBlockedByAccountsAuthority(t *testing.T) {
	updateResp := adminClient.PutJSON(t, "/api/v2.1/org/"+defaultOrgID+"/admin/users/"+url.PathEscape(defaultUserEmail)+"/", map[string]interface{}{
		"name":                   fmt.Sprintf("default-user-update-%d", time.Now().UnixNano()),
		"quota_total":            int64(987654),
		"traffic_upload_quota":   int64(3100),
		"traffic_download_quota": int64(4100),
		"is_staff":               true,
		"is_active":              false,
	})
	expectStatus(t, updateResp, http.StatusForbidden)
	body := responseJSON(t, updateResp)
	if body["managed_by"] != "accounts" {
		t.Fatalf("managed_by = %v, want accounts", body["managed_by"])
	}
}

func createAdminIdentityTestOrganization(t *testing.T, name, ownerEmail string) string {
	t.Helper()

	resp := superadminClient.PostJSON(t, "/api/v2.1/admin/organizations/", map[string]interface{}{
		"name":          name,
		"owner_email":   ownerEmail,
		"storage_quota": int64(1099511627776),
	})
	expectStatus(t, resp, http.StatusCreated)
	result := responseJSON(t, resp)

	orgID, ok := result["org_id"].(string)
	if !ok || orgID == "" {
		t.Fatalf("expected org_id in create organization response, got %v", result)
	}

	updateResp := superadminClient.PutJSON(t, "/api/v2.1/admin/organizations/"+orgID+"/", map[string]interface{}{
		"max_users": 8,
	})
	expectStatus(t, updateResp, http.StatusOK)
	updateResp.Body.Close()

	t.Cleanup(func() {
		deleteScopedTestOrganization(t, orgID)
	})

	return orgID
}

func setAdminIdentityTestOrgTrafficQuotas(t *testing.T, orgID string) {
	t.Helper()
	updateResp := superadminClient.PutJSON(t, "/api/v2.1/admin/organizations/"+orgID+"/", map[string]interface{}{
		"traffic_quota":          testTrafficQuota,
		"traffic_upload_quota":   testTrafficUpload,
		"traffic_download_quota": testTrafficDownload,
	})
	expectStatus(t, updateResp, http.StatusOK)
	updateResp.Body.Close()
}

func adminOrganizationProjectionByID(t *testing.T, orgID string) (adminOrganizationProjectionState, bool) {
	t.Helper()

	session := shareProjectionDBForTest(t).Session()
	var createdAt time.Time
	if err := session.Query(`
		SELECT created_at FROM organization_admin_projection_state WHERE org_id = ?
	`, orgID).Scan(&createdAt); err != nil {
		if err == gocql.ErrNotFound {
			return adminOrganizationProjectionState{}, false
		}
		t.Fatalf("read org projection state for %s failed: %v", orgID, err)
	}

	bucketDay := createdAt.UTC().Format("2006-01-02")
	row := adminOrganizationProjectionState{OrgID: orgID, CreatedAt: createdAt}
	var deletedAt *time.Time
	if err := session.Query(`
		SELECT name, owner_email, owner_name, status, plan, storage_quota, deleted_at, users_count
		FROM organizations_admin_by_created
		WHERE bucket_day = ? AND created_at = ? AND org_id = ?
	`, bucketDay, createdAt, orgID).Scan(
		&row.Name,
		&row.OwnerEmail,
		&row.OwnerName,
		&row.Status,
		&row.Plan,
		&row.StorageQuota,
		&deletedAt,
		&row.UsersCount,
	); err != nil {
		if err == gocql.ErrNotFound {
			return adminOrganizationProjectionState{}, false
		}
		t.Fatalf("read org projection row for %s failed: %v", orgID, err)
	}
	row.DeletedAt = deletedAt
	return row, true
}

func adminUserProjectionByEmail(t *testing.T, email string) (adminUserProjectionState, bool) {
	t.Helper()

	session := shareProjectionDBForTest(t).Session()
	var userID, orgID string
	if err := session.Query(`SELECT user_id, org_id FROM users_by_email WHERE email = ?`, email).Scan(&userID, &orgID); err != nil {
		if err == gocql.ErrNotFound {
			return adminUserProjectionState{}, false
		}
		t.Fatalf("lookup user by email %s failed: %v", email, err)
	}
	return adminUserProjectionByID(t, orgID, userID)
}

func adminUserProjectionByID(t *testing.T, orgID, userID string) (adminUserProjectionState, bool) {
	t.Helper()

	session := shareProjectionDBForTest(t).Session()
	var createdAt time.Time
	if err := session.Query(`
		SELECT created_at FROM user_admin_projection_state WHERE user_id = ?
	`, userID).Scan(&createdAt); err != nil {
		if err == gocql.ErrNotFound {
			return adminUserProjectionState{}, false
		}
		t.Fatalf("read user projection state for %s/%s failed: %v", orgID, userID, err)
	}

	bucketDay := createdAt.UTC().Format("2006-01-02")
	row := adminUserProjectionState{OrgID: orgID, UserID: userID, CreatedAt: createdAt}
	var lastLoginAt *time.Time
	if err := session.Query(`
		SELECT email, name, role, status, quota_bytes, quota_usage, last_login_at
		FROM users_admin_global_by_created
		WHERE bucket_day = ? AND created_at = ? AND org_id = ? AND user_id = ?
	`, bucketDay, createdAt, orgID, userID).Scan(
		&row.Email,
		&row.Name,
		&row.Role,
		&row.Status,
		&row.QuotaBytes,
		&row.QuotaUsage,
		&lastLoginAt,
	); err != nil {
		if err == gocql.ErrNotFound {
			return adminUserProjectionState{}, false
		}
		t.Fatalf("read user projection row for %s/%s failed: %v", orgID, userID, err)
	}
	row.LastLoginAt = lastLoginAt
	return row, true
}

func adminOrganizationPresentInList(t *testing.T, orgID, name, ownerEmail string) bool {
	t.Helper()
	return adminOrganizationPresentInStatusList(t, "active", orgID, name, ownerEmail)
}

func adminOrganizationPresentInStatusList(t *testing.T, status, orgID, name, ownerEmail string) bool {
	t.Helper()

	resp := superadminClient.Get(t, "/api/v2.1/admin/organizations/?status="+url.QueryEscape(status))
	expectStatus(t, resp, http.StatusOK)
	payload := responseJSON(t, resp)
	entries, ok := payload["organizations"].([]interface{})
	if !ok {
		return false
	}
	for _, entry := range entries {
		row, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		if rowOrgID, _ := row["org_id"].(string); rowOrgID != orgID {
			continue
		}
		rowName, _ := row["org_name"].(string)
		rowOwnerEmail, _ := row["owner_email"].(string)
		return rowName == name && rowOwnerEmail == ownerEmail
	}
	return false
}

func lookupUserIDByEmail(t *testing.T, email string) (string, bool) {
	t.Helper()

	session := shareProjectionDBForTest(t).Session()
	var userID string
	if err := session.Query(`SELECT user_id FROM users_by_email WHERE email = ?`, email).Scan(&userID); err != nil {
		if err == gocql.ErrNotFound {
			return "", false
		}
		t.Fatalf("lookup user_id by email %s failed: %v", email, err)
	}
	return userID, true
}

func canonicalUserExists(t *testing.T, orgID, userID, email string) bool {
	t.Helper()

	session := shareProjectionDBForTest(t).Session()
	var storedEmail string
	if err := session.Query(`
		SELECT email FROM users WHERE org_id = ? AND user_id = ?
	`, orgID, userID).Scan(&storedEmail); err == nil {
		return true
	} else if err != gocql.ErrNotFound {
		t.Fatalf("read canonical user %s/%s failed: %v", orgID, userID, err)
	}
	if email == "" {
		return false
	}
	var lookedUpUserID string
	if err := session.Query(`SELECT user_id FROM users_by_email WHERE email = ?`, email).Scan(&lookedUpUserID); err == nil {
		return true
	} else if err != gocql.ErrNotFound {
		t.Fatalf("read users_by_email %s failed: %v", email, err)
	}
	return false
}

func canonicalUserStatusIs(t *testing.T, orgID, userID, expectedStatus string) bool {
	t.Helper()

	session := shareProjectionDBForTest(t).Session()
	var status string
	if err := session.Query(`SELECT status FROM users WHERE org_id = ? AND user_id = ?`, orgID, userID).Scan(&status); err != nil {
		if err == gocql.ErrNotFound {
			return false
		}
		t.Fatalf("read canonical user status %s/%s failed: %v", orgID, userID, err)
	}
	return status == expectedStatus
}

func canonicalOrganizationExists(t *testing.T, orgID string) bool {
	t.Helper()

	session := shareProjectionDBForTest(t).Session()
	var name string
	if err := session.Query(`SELECT name FROM organizations WHERE org_id = ?`, orgID).Scan(&name); err == nil {
		return true
	} else if err != gocql.ErrNotFound {
		t.Fatalf("read canonical organization %s failed: %v", orgID, err)
	}
	return false
}

func canonicalOrganizationStatusIs(t *testing.T, orgID, expectedStatus string) bool {
	t.Helper()

	session := shareProjectionDBForTest(t).Session()
	var status string
	if err := session.Query(`SELECT status FROM organizations WHERE org_id = ?`, orgID).Scan(&status); err != nil {
		if err == gocql.ErrNotFound {
			return false
		}
		t.Fatalf("read canonical organization status %s failed: %v", orgID, err)
	}
	return status == expectedStatus
}

func deletedOrganizationMarkerExists(t *testing.T, orgID string) bool {
	t.Helper()

	session := shareProjectionDBForTest(t).Session()
	var name string
	if err := session.Query(`SELECT name FROM deleted_organizations WHERE org_id = ?`, orgID).Scan(&name); err == nil {
		return true
	} else if err != gocql.ErrNotFound {
		t.Fatalf("read deleted_organizations %s failed: %v", orgID, err)
	}
	return false
}

func adminOrgLinkActive(t *testing.T, orgID, linkType, token string, createdAt time.Time) bool {
	t.Helper()

	session := shareProjectionDBForTest(t).Session()
	var active bool
	if err := session.Query(`
		SELECT active FROM admin_links_by_org_created
		WHERE org_id = ? AND link_type = ? AND bucket_day = ? AND created_at = ? AND link_token = ?
	`, orgID, linkType, dbpkg.AdminLinkBucketDay(createdAt), createdAt, token).Scan(&active); err != nil {
		t.Fatalf("read admin org link active flag for %s failed: %v", token, err)
	}
	return active
}

func canonicalShareLinkActive(t *testing.T, token string) bool {
	t.Helper()

	session := shareProjectionDBForTest(t).Session()
	var active bool
	if err := session.Query(`SELECT active FROM share_links WHERE link_token = ?`, token).Scan(&active); err != nil {
		t.Fatalf("read canonical share link active flag for %s failed: %v", token, err)
	}
	return active
}

func creatorShareLinkActive(t *testing.T, orgID, createdBy, token string, createdAt time.Time) bool {
	t.Helper()

	session := shareProjectionDBForTest(t).Session()
	var active bool
	if err := session.Query(`
		SELECT active FROM share_links_by_creator
		WHERE org_id = ? AND created_by = ? AND created_at = ? AND link_token = ?
	`, orgID, createdBy, createdAt, token).Scan(&active); err != nil {
		t.Fatalf("read creator share link active flag for %s failed: %v", token, err)
	}
	return active
}

func deletedOrganizationMarkerDeletedAt(t *testing.T, orgID string) time.Time {
	t.Helper()

	session := shareProjectionDBForTest(t).Session()
	var deletedAt time.Time
	if err := session.Query(`SELECT deleted_at FROM deleted_organizations WHERE org_id = ?`, orgID).Scan(&deletedAt); err != nil {
		t.Fatalf("read deleted_organizations deleted_at %s failed: %v", orgID, err)
	}
	return deletedAt
}

func snapshotAdminUserForUpdate(t *testing.T, email string) adminUserUpdateSnapshot {
	t.Helper()
	payload := getAdminUserByEmail(t, email)
	return adminUserUpdateSnapshot{
		Name:                 stringValue(payload, "name"),
		Role:                 stringValue(payload, "role"),
		Status:               stringValue(payload, "status"),
		QuotaTotal:           jsonInt64(payload, "quota_total"),
		TrafficUploadQuota:   jsonInt64(payload, "traffic_upload_quota"),
		TrafficDownloadQuota: jsonInt64(payload, "traffic_download_quota"),
	}
}

func restoreAdminUserAfterUpdate(t *testing.T, email string, snapshot adminUserUpdateSnapshot) {
	t.Helper()
	resp := superadminClient.PutJSON(t, "/api/v2.1/admin/users/"+url.PathEscape(email)+"/", map[string]interface{}{
		"name":                   snapshot.Name,
		"role":                   snapshot.Role,
		"quota_total":            snapshot.QuotaTotal,
		"traffic_upload_quota":   snapshot.TrafficUploadQuota,
		"traffic_download_quota": snapshot.TrafficDownloadQuota,
		"is_active":              snapshot.Status == "active",
	})
	expectStatus(t, resp, http.StatusOK)
	resp.Body.Close()
}

func stringValue(payload map[string]interface{}, key string) string {
	if raw, ok := payload[key]; ok {
		if value, ok := raw.(string); ok {
			return value
		}
	}
	return ""
}

func mustParseUUID(t *testing.T, value string) uuid.UUID {
	t.Helper()

	parsed, err := uuid.Parse(value)
	if err != nil {
		t.Fatalf("parse UUID %q failed: %v", value, err)
	}
	return parsed
}

func adminUserPresentInSearch(t *testing.T, email, role string) bool {
	t.Helper()

	resp := superadminClient.Get(t, "/api/v2.1/admin/search-user/?query="+url.QueryEscape(email)+"&page=1&per_page=100")
	expectStatus(t, resp, http.StatusOK)
	payload := responseJSON(t, resp)
	entries, ok := payload["user_list"].([]interface{})
	if !ok {
		return false
	}
	for _, entry := range entries {
		row, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		rowEmail, _ := row["email"].(string)
		rowRole, _ := row["role"].(string)
		if rowEmail == email && rowRole == role {
			return true
		}
	}
	return false
}

func legacySearchUserPresent(t *testing.T, client *testClient, query, email string) bool {
	t.Helper()

	resp := client.Get(t, "/api2/search-user/?q="+url.QueryEscape(query))
	expectStatus(t, resp, http.StatusOK)
	payload := responseJSON(t, resp)
	entries, ok := payload["users"].([]interface{})
	if !ok {
		return false
	}
	for _, entry := range entries {
		row, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		rowEmail, _ := row["email"].(string)
		if rowEmail == email {
			return true
		}
	}
	return false
}

func TestSuperadminSearchUsesAdminEndpointAcrossOrgs(t *testing.T) {
	if legacySearchUserPresent(t, superadminClient, defaultUserEmail, defaultUserEmail) {
		t.Fatalf("superadmin legacy search unexpectedly returned %q across orgs", defaultUserEmail)
	}
	if !adminUserPresentInSearch(t, defaultUserEmail, "user") {
		t.Fatalf("superadmin admin search did not return %q across orgs", defaultUserEmail)
	}
}

func adminUserPresentInStatusList(t *testing.T, status, email string) bool {
	t.Helper()

	resp := superadminClient.Get(t, "/api/v2.1/admin/users/?status="+url.QueryEscape(status)+"&page=1&per_page=100")
	expectStatus(t, resp, http.StatusOK)
	payload := responseJSON(t, resp)
	entries, ok := payload["data"].([]interface{})
	if !ok {
		return false
	}
	for _, entry := range entries {
		row, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		rowEmail, _ := row["email"].(string)
		if rowEmail == email {
			return true
		}
	}
	return false
}
