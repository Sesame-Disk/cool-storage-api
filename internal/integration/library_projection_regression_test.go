//go:build integration

package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gcpkg "github.com/Sesame-Disk/sesamefs/internal/gc"
	"github.com/Sesame-Disk/sesamefs/internal/traffic"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"
)

func TestLibraryProjectionRegression_CreateRenameDeleteRestore(t *testing.T) {
	name := fmt.Sprintf("inttest-lib-projection-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)

	row := requireAdminLibraryByQuery(t, superadminClient, repoID)
	if got, _ := row["name"].(string); got != name {
		t.Fatalf("admin projection name = %q, want %q", got, name)
	}

	renamed := name + "-renamed"
	resp := adminClient.PostJSON(t, fmt.Sprintf("/api2/repos/%s/?op=rename", repoID), map[string]string{"repo_name": renamed})
	expectStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	waitForIntegrationCondition(t, "renamed library to appear in admin projection", func() bool {
		row, ok := adminLibraryByQuery(t, superadminClient, repoID)
		if !ok {
			return false
		}
		got, _ := row["name"].(string)
		return got == renamed
	})

	deleteResp := adminClient.Delete(t, fmt.Sprintf("/api/v2.1/repos/%s/", repoID))
	expectStatus(t, deleteResp, http.StatusOK)
	deleteResp.Body.Close()

	waitForIntegrationCondition(t, "deleted library to leave admin active projection", func() bool {
		_, ok := adminLibraryByQuery(t, superadminClient, repoID)
		return !ok
	})
	waitForIntegrationCondition(t, "deleted library to appear in admin trash projection", func() bool {
		return adminTrashContainsRepo(t, superadminClient, repoID, defaultAdminEmail)
	})

	restoreResp := adminClient.Do(t, http.MethodPut, fmt.Sprintf("/api/v2.1/repos/deleted/%s/", repoID), nil)
	expectStatus(t, restoreResp, http.StatusOK)
	restoreResp.Body.Close()
	t.Cleanup(func() {
		resp := adminClient.Delete(t, fmt.Sprintf("/api/v2.1/repos/%s/", repoID))
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound {
			resp.Body.Close()
			return
		}
		body := responseBody(t, resp)
		t.Errorf("cleanup delete library %s failed: status=%d body=%s", repoID, resp.StatusCode, body)
	})

	waitForIntegrationCondition(t, "restored library to reappear in admin projection", func() bool {
		row, ok := adminLibraryByQuery(t, superadminClient, repoID)
		if !ok {
			return false
		}
		got, _ := row["name"].(string)
		return got == renamed
	})
	waitForIntegrationCondition(t, "restored library to leave admin trash projection", func() bool {
		return !adminTrashContainsRepo(t, superadminClient, repoID, defaultAdminEmail)
	})
}

func TestLibraryProjectionRegression_FileCreateUpdatesAdminStats(t *testing.T) {
	name := fmt.Sprintf("inttest-lib-stats-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)

	createResp := adminClient.PostForm(t, fmt.Sprintf("/api2/repos/%s/file/?p=/projection-stats.txt&operation=create", repoID), url.Values{})
	if createResp.StatusCode != http.StatusCreated && createResp.StatusCode != http.StatusOK {
		body := responseBody(t, createResp)
		t.Fatalf("expected 200 or 201 for file create, got %d body=%s", createResp.StatusCode, body)
	}
	createResp.Body.Close()

	waitForIntegrationCondition(t, "file count to reach admin projection after file create", func() bool {
		row, ok := adminLibraryByQuery(t, superadminClient, repoID)
		if !ok {
			return false
		}
		fileCount, ok := row["file_count"].(float64)
		return ok && fileCount >= 1
	})
}

func TestLibraryProjectionRegression_ReconcilePendingStorageCountersAfterSoftDelete(t *testing.T) {
	name := fmt.Sprintf("inttest-lib-storage-reconcile-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)
	database := shareProjectionDBForTest(t)
	session := database.Session()
	store := gcpkg.NewCassandraStore(database)

	var ownerID string
	if err := session.Query(`SELECT owner_id FROM libraries WHERE org_id = ? AND library_id = ?`, defaultOrgID, repoID).Scan(&ownerID); err != nil {
		t.Fatalf("failed to read owner_id for repo %s: %v", repoID, err)
	}

	platformScope := traffic.PlatformStorageScope()
	orgScope := traffic.OrganizationStorageScope(defaultOrgID)
	userScope := traffic.UserStorageScope(defaultOrgID, ownerID)
	libScope := traffic.LibraryStorageScope(defaultOrgID, repoID)

	baselinePlatform := traffic.ReadStorageSnapshot(database, platformScope)
	baselineOrg := traffic.ReadStorageSnapshot(database, orgScope)
	baselineUser := traffic.ReadStorageSnapshot(database, userScope)

	uploadResp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/upload-link/?p=/", repoID))
	expectStatus(t, uploadResp, http.StatusOK)
	uploadURL := strings.Trim(responseBody(t, uploadResp), "\" \n\r")
	uploadFileThroughLink(t, adminClient, uploadURL, "reconcile.txt", "/", "strong-reconciliation-content\n")

	var uploadedLibSnapshot traffic.StorageSnapshot
	waitForIntegrationCondition(t, "library upload to update storage counters", func() bool {
		uploadedLibSnapshot = traffic.ReadStorageSnapshot(database, libScope)
		if uploadedLibSnapshot.BytesUsed <= 0 || uploadedLibSnapshot.FileCount <= 0 {
			return false
		}
		platformNow := traffic.ReadStorageSnapshot(database, platformScope)
		orgNow := traffic.ReadStorageSnapshot(database, orgScope)
		userNow := traffic.ReadStorageSnapshot(database, userScope)
		return platformNow.BytesUsed >= baselinePlatform.BytesUsed+uploadedLibSnapshot.BytesUsed &&
			orgNow.BytesUsed >= baselineOrg.BytesUsed+uploadedLibSnapshot.BytesUsed &&
			userNow.BytesUsed >= baselineUser.BytesUsed+uploadedLibSnapshot.BytesUsed
	})

	deleteResp := adminClient.Delete(t, fmt.Sprintf("/api/v2.1/repos/%s/", repoID))
	expectStatus(t, deleteResp, http.StatusOK)
	deleteResp.Body.Close()

	waitForIntegrationCondition(t, "soft-deleted library to leave aggregate storage counters at baseline", func() bool {
		if !deletedLibraryMarkerExistsForTest(t, store, repoID) {
			return false
		}
		platformNow := traffic.ReadStorageSnapshot(database, platformScope)
		orgNow := traffic.ReadStorageSnapshot(database, orgScope)
		userNow := traffic.ReadStorageSnapshot(database, userScope)
		libNow := traffic.ReadStorageSnapshot(database, libScope)
		return platformNow == baselinePlatform && orgNow == baselineOrg && userNow == baselineUser && libNow == uploadedLibSnapshot
	})
	expectedAfterReconcile := expectedAggregateStorageSnapshotsForScopes(t, database, platformScope, orgScope, userScope)

	const driftBytes int64 = 777
	const driftFiles int64 = 3
	for _, scope := range []string{platformScope, orgScope, userScope} {
		addStorageCounterDriftForTest(t, session, scope, driftBytes, driftFiles)
	}
	insertStorageReconciliationRequestForTest(t, session, platformScope, "", "")
	insertStorageReconciliationRequestForTest(t, session, orgScope, defaultOrgID, "")
	insertStorageReconciliationRequestForTest(t, session, userScope, defaultOrgID, ownerID)

	reconciled, err := store.ReconcilePendingStorageCounters()
	if err != nil {
		t.Fatalf("ReconcilePendingStorageCounters failed: %v", err)
	}
	if reconciled < 3 {
		t.Fatalf("reconciled scopes = %d, want at least 3 target scopes", reconciled)
	}

	if got := traffic.ReadStorageSnapshot(database, platformScope); got != expectedAfterReconcile[platformScope] {
		t.Fatalf("platform snapshot after reconciliation = %+v, want %+v", got, expectedAfterReconcile[platformScope])
	}
	if got := traffic.ReadStorageSnapshot(database, orgScope); got != expectedAfterReconcile[orgScope] {
		t.Fatalf("org snapshot after reconciliation = %+v, want %+v", got, expectedAfterReconcile[orgScope])
	}
	if got := traffic.ReadStorageSnapshot(database, userScope); got != expectedAfterReconcile[userScope] {
		t.Fatalf("user snapshot after reconciliation = %+v, want %+v", got, expectedAfterReconcile[userScope])
	}
	if storageReconciliationRequestExistsForTest(t, session, platformScope) ||
		storageReconciliationRequestExistsForTest(t, session, orgScope) ||
		storageReconciliationRequestExistsForTest(t, session, userScope) {
		t.Fatal("expected reconciliation requests to be cleared after reconciliation")
	}
}

func TestLibraryProjectionRegression_GCSoftDeleteUsesCanonicalReadModelHelper(t *testing.T) {
	name := fmt.Sprintf("inttest-gc-soft-delete-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)
	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)

	var ownerID string
	if err := database.Session().Query(`SELECT owner_id FROM libraries WHERE org_id = ? AND library_id = ?`, defaultOrgID, repoID).Scan(&ownerID); err != nil {
		t.Fatalf("failed to read owner_id for repo %s: %v", repoID, err)
	}
	ownerUUID, err := uuid.Parse(ownerID)
	if err != nil {
		t.Fatalf("failed to parse owner UUID %q: %v", ownerID, err)
	}
	repoUUID, err := uuid.Parse(repoID)
	if err != nil {
		t.Fatalf("failed to parse repo UUID %q: %v", repoID, err)
	}
	orgUUID, err := uuid.Parse(defaultOrgID)
	if err != nil {
		t.Fatalf("failed to parse org UUID %q: %v", defaultOrgID, err)
	}

	if err := store.SoftDeleteLibrary(orgUUID, repoUUID, ownerUUID); err != nil {
		t.Fatalf("GC SoftDeleteLibrary failed: %v", err)
	}

	waitForIntegrationCondition(t, "GC soft-delete to mark canonical admin projection deleted", func() bool {
		row, ok := adminLibraryProjectionRowForTest(t, database.Session(), defaultOrgID, repoID)
		return ok && row.DeletedAt != nil && !row.DeletedAt.IsZero()
	})
	waitForIntegrationCondition(t, "GC soft-delete to populate canonical trash projection", func() bool {
		_, ok := deletedAdminLibraryProjectionRowForTest(t, database.Session(), defaultOrgID, repoID)
		return ok
	})
	if !deletedLibraryMarkerExistsForTest(t, store, repoID) {
		t.Fatalf("expected deleted_libraries marker for repo %s after GC soft-delete", repoID)
	}
	restoreResp := adminClient.Do(t, http.MethodPut, fmt.Sprintf("/api/v2.1/repos/deleted/%s/", repoID), nil)
	expectStatus(t, restoreResp, http.StatusOK)
	restoreResp.Body.Close()
}

func TestLibraryProjectionRegression_GCSoftDeleteFallsBackToBaseLibraryRowWhenProjectionMissing(t *testing.T) {
	name := fmt.Sprintf("inttest-gc-soft-delete-fallback-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)
	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	session := database.Session()

	var ownerID, storageClass string
	if err := session.Query(`SELECT owner_id, storage_class FROM libraries WHERE org_id = ? AND library_id = ?`, defaultOrgID, repoID).Scan(&ownerID, &storageClass); err != nil {
		t.Fatalf("failed to read base library row for repo %s: %v", repoID, err)
	}
	removeAdminLibraryProjectionRowsForSoftDeleteFallbackTest(t, session, defaultOrgID, repoID, ownerID)

	repoUUID, err := uuid.Parse(repoID)
	if err != nil {
		t.Fatalf("failed to parse repo UUID %q: %v", repoID, err)
	}
	orgUUID, err := uuid.Parse(defaultOrgID)
	if err != nil {
		t.Fatalf("failed to parse org UUID %q: %v", defaultOrgID, err)
	}
	ownerUUID, err := uuid.Parse(ownerID)
	if err != nil {
		t.Fatalf("failed to parse owner UUID %q: %v", ownerID, err)
	}

	if err := store.SoftDeleteLibrary(orgUUID, repoUUID, ownerUUID); err != nil {
		t.Fatalf("GC SoftDeleteLibrary fallback failed: %v", err)
	}

	waitForIntegrationCondition(t, "GC soft-delete fallback to write deleted_libraries marker", func() bool {
		return deletedLibraryMarkerExistsForTest(t, store, repoID)
	})
	waitForIntegrationCondition(t, "GC soft-delete fallback to enqueue user reconciliation", func() bool {
		return storageReconciliationRequestExistsForTest(t, session, traffic.UserStorageScope(defaultOrgID, ownerID))
	})

	var gotStorageClass string
	if err := session.Query(`SELECT storage_class FROM deleted_libraries WHERE library_id = ?`, repoID).Scan(&gotStorageClass); err != nil {
		t.Fatalf("failed to read deleted_libraries row for repo %s: %v", repoID, err)
	}
	if gotStorageClass != storageClass {
		t.Fatalf("deleted_libraries storage_class = %q, want %q", gotStorageClass, storageClass)
	}

	restoreResp := adminClient.Do(t, http.MethodPut, fmt.Sprintf("/api/v2.1/repos/deleted/%s/", repoID), nil)
	expectStatus(t, restoreResp, http.StatusOK)
	restoreResp.Body.Close()
}

func TestLibraryProjectionRegression_GCHardDeleteCleansCanonicalTrashProjectionWithoutBaseRow(t *testing.T) {
	name := fmt.Sprintf("inttest-gc-hard-delete-%d", time.Now().UnixNano())
	repoID := createDisposableTestLibrary(t, adminClient, name)
	database := shareProjectionDBForTest(t)
	store := gcpkg.NewCassandraStore(database)
	session := database.Session()

	deleteResp := adminClient.Delete(t, fmt.Sprintf("/api/v2.1/repos/%s/", repoID))
	expectStatus(t, deleteResp, http.StatusOK)
	deleteResp.Body.Close()
	waitForIntegrationCondition(t, "deleted library to appear in admin trash projection", func() bool {
		return adminTrashContainsRepo(t, superadminClient, repoID, defaultAdminEmail)
	})

	removeLibraryBaseRowsForFallbackTest(t, session, repoID)

	repoUUID, err := uuid.Parse(repoID)
	if err != nil {
		t.Fatalf("failed to parse repo UUID %q: %v", repoID, err)
	}
	orgUUID, err := uuid.Parse(defaultOrgID)
	if err != nil {
		t.Fatalf("failed to parse org UUID %q: %v", defaultOrgID, err)
	}

	if err := store.HardDeleteLibrary(orgUUID, repoUUID); err != nil {
		t.Fatalf("GC HardDeleteLibrary failed: %v", err)
	}
	waitForIntegrationCondition(t, "GC hard-delete to clear canonical trash projection fallback", func() bool {
		_, ok := deletedAdminLibraryProjectionRowForTest(t, database.Session(), defaultOrgID, repoID)
		return !ok
	})
	waitForIntegrationCondition(t, "GC hard-delete to clear canonical admin projection fallback", func() bool {
		_, ok := adminLibraryProjectionRowForTest(t, database.Session(), defaultOrgID, repoID)
		return !ok
	})
}

func TestAdminCleanTrashLibraries_PrunesStaleProjectionRows(t *testing.T) {
	name := fmt.Sprintf("inttest-admin-trash-stale-%d", time.Now().UnixNano())
	repoID := createDisposableTestLibrary(t, adminClient, name)
	database := shareProjectionDBForTest(t)
	session := database.Session()

	deleteResp := adminClient.Delete(t, fmt.Sprintf("/api/v2.1/repos/%s/", repoID))
	expectStatus(t, deleteResp, http.StatusOK)
	deleteResp.Body.Close()

	waitForIntegrationCondition(t, "stale trash fixture to appear in admin trash projection", func() bool {
		return adminTrashContainsRepo(t, superadminClient, repoID, defaultAdminEmail)
	})

	removeLibraryBaseRowsForFallbackTest(t, session, repoID)

	cleanResp := superadminClient.Do(t, http.MethodDelete, "/api/v2.1/admin/trash-libraries/", nil)
	expectStatus(t, cleanResp, http.StatusOK)
	body := responseJSON(t, cleanResp)
	cleaned, _ := body["cleaned"].(float64)
	if cleaned < 1 {
		t.Fatalf("admin clean trash cleaned=%v, want at least 1 stale projection pruned", body["cleaned"])
	}

	waitForIntegrationCondition(t, "stale trash projection to be removed by admin clean", func() bool {
		_, ok := deletedAdminLibraryProjectionRowForTest(t, session, defaultOrgID, repoID)
		return !ok
	})

	resp := superadminClient.Get(t, "/api/v2.1/admin/trash-libraries/?page=1&per_page=100")
	expectStatus(t, resp, http.StatusOK)
	payload := responseJSON(t, resp)
	entries, ok := payload["repos"].([]interface{})
	if !ok {
		t.Fatalf("admin trash payload missing repos: %v", payload)
	}
	if containsEntry(entries, "id", repoID) {
		t.Fatalf("stale trash repo %s still visible after admin clean", repoID)
	}
}

func TestSyncHeadUpdateKeepsLookupAndAdminProjectionAligned(t *testing.T) {
	name := fmt.Sprintf("inttest-sync-head-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)
	session := shareProjectionDBForTest(t).Session()

	initial := readLibrarySyncHeadState(t, session, repoID)

	nextHead := fmt.Sprintf("%040x", time.Now().UnixNano())
	insertSyntheticCommitForTest(t, session, repoID, nextHead, initial.HeadCommitID, initial.RootFSID, "integration head advance")
	t.Cleanup(func() {
		if err := session.Query(`DELETE FROM commits WHERE library_id = ? AND commit_id = ?`, repoID, nextHead).Exec(); err != nil {
			t.Errorf("cleanup synthetic commit %s failed: %v", nextHead, err)
		}
	})

	resp := adminClient.Do(t, http.MethodPut, fmt.Sprintf("/seafhttp/repo/%s/commit/HEAD?head=%s", repoID, url.QueryEscape(nextHead)), nil)
	if resp.StatusCode != http.StatusOK {
		body := responseBody(t, resp)
		t.Fatalf("sync HEAD update failed: status=%d body=%s", resp.StatusCode, body)
	}
	resp.Body.Close()

	waitForIntegrationCondition(t, "sync HEAD dual-write to align canonical, lookup, and admin projection rows", func() bool {
		current := readLibrarySyncHeadState(t, session, repoID)
		return current.HeadCommitID == nextHead &&
			current.LookupHeadCommitID == nextHead &&
			current.UpdatedAt.After(initial.UpdatedAt) &&
			current.ProjectionUpdatedAt.Equal(current.UpdatedAt)
	})
}

func TestSyncHeadConflictReturnsOKWithoutRollback(t *testing.T) {
	name := fmt.Sprintf("inttest-sync-conflict-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)
	session := shareProjectionDBForTest(t).Session()

	initial := readLibrarySyncHeadState(t, session, repoID)
	acceptedHead := fmt.Sprintf("%040x", time.Now().UnixNano())
	staleHead := fmt.Sprintf("%040x", time.Now().UnixNano()+1)
	insertSyntheticCommitForTest(t, session, repoID, acceptedHead, initial.HeadCommitID, initial.RootFSID, "integration accepted head")
	insertSyntheticCommitForTest(t, session, repoID, staleHead, initial.HeadCommitID, initial.RootFSID, "integration stale head")
	t.Cleanup(func() {
		for _, commitID := range []string{acceptedHead, staleHead} {
			if err := session.Query(`DELETE FROM commits WHERE library_id = ? AND commit_id = ?`, repoID, commitID).Exec(); err != nil {
				t.Errorf("cleanup synthetic commit %s failed: %v", commitID, err)
			}
		}
	})

	resp := adminClient.Do(t, http.MethodPut, fmt.Sprintf("/seafhttp/repo/%s/commit/HEAD?head=%s", repoID, url.QueryEscape(acceptedHead)), nil)
	if resp.StatusCode != http.StatusOK {
		body := responseBody(t, resp)
		t.Fatalf("initial sync HEAD update failed: status=%d body=%s", resp.StatusCode, body)
	}
	resp.Body.Close()

	var advanced librarySyncHeadState
	waitForIntegrationCondition(t, "accepted sync head to become authoritative", func() bool {
		current := readLibrarySyncHeadState(t, session, repoID)
		if current.HeadCommitID != acceptedHead || current.LookupHeadCommitID != acceptedHead {
			return false
		}
		if !current.UpdatedAt.After(initial.UpdatedAt) || !current.ProjectionUpdatedAt.Equal(current.UpdatedAt) {
			return false
		}
		advanced = current
		return true
	})

	conflictResp := adminClient.Do(t, http.MethodPut, fmt.Sprintf("/seafhttp/repo/%s/commit/HEAD?head=%s", repoID, url.QueryEscape(staleHead)), nil)
	if conflictResp.StatusCode != http.StatusOK {
		body := responseBody(t, conflictResp)
		t.Fatalf("conflicting sync HEAD update returned status=%d body=%s", conflictResp.StatusCode, body)
	}
	conflictResp.Body.Close()

	stabilized := readLibrarySyncHeadState(t, session, repoID)
	if stabilized.HeadCommitID != acceptedHead {
		t.Fatalf("canonical head rolled back to %q, want %q", stabilized.HeadCommitID, acceptedHead)
	}
	if stabilized.LookupHeadCommitID != acceptedHead {
		t.Fatalf("lookup head rolled back to %q, want %q", stabilized.LookupHeadCommitID, acceptedHead)
	}
	if !stabilized.UpdatedAt.Equal(advanced.UpdatedAt) {
		t.Fatalf("canonical updated_at changed on stale conflict: got %s want %s", stabilized.UpdatedAt.Format(time.RFC3339Nano), advanced.UpdatedAt.Format(time.RFC3339Nano))
	}
	if !stabilized.ProjectionUpdatedAt.Equal(advanced.ProjectionUpdatedAt) {
		t.Fatalf("projection updated_at changed on stale conflict: got %s want %s", stabilized.ProjectionUpdatedAt.Format(time.RFC3339Nano), advanced.ProjectionUpdatedAt.Format(time.RFC3339Nano))
	}
}

func TestUpdateBranchConflictReturnsOKWithoutRollback(t *testing.T) {
	name := fmt.Sprintf("inttest-update-branch-conflict-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)
	session := shareProjectionDBForTest(t).Session()

	initial := readLibrarySyncHeadState(t, session, repoID)
	acceptedHead := fmt.Sprintf("%040x", time.Now().UnixNano())
	staleHead := fmt.Sprintf("%040x", time.Now().UnixNano()+1)
	insertSyntheticCommitForTest(t, session, repoID, acceptedHead, initial.HeadCommitID, initial.RootFSID, "integration accepted update-branch head")
	insertSyntheticCommitForTest(t, session, repoID, staleHead, initial.HeadCommitID, initial.RootFSID, "integration stale update-branch head")
	t.Cleanup(func() {
		for _, commitID := range []string{acceptedHead, staleHead} {
			if err := session.Query(`DELETE FROM commits WHERE library_id = ? AND commit_id = ?`, repoID, commitID).Exec(); err != nil {
				t.Errorf("cleanup synthetic commit %s failed: %v", commitID, err)
			}
		}
	})

	resp := adminClient.Do(t, http.MethodPost, fmt.Sprintf("/seafhttp/repo/%s/update-branch?head=%s", repoID, url.QueryEscape(acceptedHead)), nil)
	if resp.StatusCode != http.StatusOK {
		body := responseBody(t, resp)
		t.Fatalf("initial update-branch failed: status=%d body=%s", resp.StatusCode, body)
	}
	resp.Body.Close()

	var advanced librarySyncHeadState
	waitForIntegrationCondition(t, "accepted update-branch head to become authoritative", func() bool {
		current := readLibrarySyncHeadState(t, session, repoID)
		if current.HeadCommitID != acceptedHead || current.LookupHeadCommitID != acceptedHead {
			return false
		}
		if !current.UpdatedAt.After(initial.UpdatedAt) || !current.ProjectionUpdatedAt.Equal(current.UpdatedAt) {
			return false
		}
		advanced = current
		return true
	})

	conflictResp := adminClient.Do(t, http.MethodPost, fmt.Sprintf("/seafhttp/repo/%s/update-branch?head=%s", repoID, url.QueryEscape(staleHead)), nil)
	if conflictResp.StatusCode != http.StatusOK {
		body := responseBody(t, conflictResp)
		t.Fatalf("conflicting update-branch returned status=%d body=%s", conflictResp.StatusCode, body)
	}
	conflictResp.Body.Close()

	stabilized := readLibrarySyncHeadState(t, session, repoID)
	if stabilized.HeadCommitID != acceptedHead {
		t.Fatalf("canonical head rolled back to %q, want %q", stabilized.HeadCommitID, acceptedHead)
	}
	if stabilized.LookupHeadCommitID != acceptedHead {
		t.Fatalf("lookup head rolled back to %q, want %q", stabilized.LookupHeadCommitID, acceptedHead)
	}
	if !stabilized.UpdatedAt.Equal(advanced.UpdatedAt) {
		t.Fatalf("canonical updated_at changed on stale conflict: got %s want %s", stabilized.UpdatedAt.Format(time.RFC3339Nano), advanced.UpdatedAt.Format(time.RFC3339Nano))
	}
	if !stabilized.ProjectionUpdatedAt.Equal(advanced.ProjectionUpdatedAt) {
		t.Fatalf("projection updated_at changed on stale conflict: got %s want %s", stabilized.ProjectionUpdatedAt.Format(time.RFC3339Nano), advanced.ProjectionUpdatedAt.Format(time.RFC3339Nano))
	}
}

func TestUpdateBranchSameHeadReturnsOKWithoutProjectionChange(t *testing.T) {
	name := fmt.Sprintf("inttest-update-branch-idempotent-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)
	session := shareProjectionDBForTest(t).Session()

	initial := readLibrarySyncHeadState(t, session, repoID)
	nextHead := fmt.Sprintf("%040x", time.Now().UnixNano())
	insertSyntheticCommitForTest(t, session, repoID, nextHead, initial.HeadCommitID, initial.RootFSID, "integration idempotent update-branch head")
	t.Cleanup(func() {
		if err := session.Query(`DELETE FROM commits WHERE library_id = ? AND commit_id = ?`, repoID, nextHead).Exec(); err != nil {
			t.Errorf("cleanup synthetic commit %s failed: %v", nextHead, err)
		}
	})

	resp := adminClient.Do(t, http.MethodPost, fmt.Sprintf("/seafhttp/repo/%s/update-branch?head=%s", repoID, url.QueryEscape(nextHead)), nil)
	if resp.StatusCode != http.StatusOK {
		body := responseBody(t, resp)
		t.Fatalf("initial update-branch failed: status=%d body=%s", resp.StatusCode, body)
	}
	resp.Body.Close()

	var advanced librarySyncHeadState
	waitForIntegrationCondition(t, "accepted update-branch idempotent head to become authoritative", func() bool {
		current := readLibrarySyncHeadState(t, session, repoID)
		if current.HeadCommitID != nextHead || current.LookupHeadCommitID != nextHead {
			return false
		}
		if !current.UpdatedAt.After(initial.UpdatedAt) || !current.ProjectionUpdatedAt.Equal(current.UpdatedAt) {
			return false
		}
		advanced = current
		return true
	})

	idempotentResp := adminClient.Do(t, http.MethodPost, fmt.Sprintf("/seafhttp/repo/%s/update-branch?head=%s", repoID, url.QueryEscape(nextHead)), nil)
	if idempotentResp.StatusCode != http.StatusOK {
		body := responseBody(t, idempotentResp)
		t.Fatalf("idempotent update-branch returned status=%d body=%s", idempotentResp.StatusCode, body)
	}
	idempotentResp.Body.Close()

	stabilized := readLibrarySyncHeadState(t, session, repoID)
	if stabilized.HeadCommitID != nextHead {
		t.Fatalf("canonical head changed to %q, want %q", stabilized.HeadCommitID, nextHead)
	}
	if stabilized.LookupHeadCommitID != nextHead {
		t.Fatalf("lookup head changed to %q, want %q", stabilized.LookupHeadCommitID, nextHead)
	}
	if !stabilized.UpdatedAt.Equal(advanced.UpdatedAt) {
		t.Fatalf("canonical updated_at changed on idempotent success: got %s want %s", stabilized.UpdatedAt.Format(time.RFC3339Nano), advanced.UpdatedAt.Format(time.RFC3339Nano))
	}
	if !stabilized.ProjectionUpdatedAt.Equal(advanced.ProjectionUpdatedAt) {
		t.Fatalf("projection updated_at changed on idempotent success: got %s want %s", stabilized.ProjectionUpdatedAt.Format(time.RFC3339Nano), advanced.ProjectionUpdatedAt.Format(time.RFC3339Nano))
	}
}

func TestSyncHeadStaleAsyncStatsDoNotOverwriteCurrentHead(t *testing.T) {
	name := fmt.Sprintf("inttest-sync-stats-race-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)
	session := shareProjectionDBForTest(t).Session()

	initial := readLibrarySyncHeadState(t, session, repoID)
	heavyHead := fmt.Sprintf("%040x", time.Now().UnixNano())
	finalHead := fmt.Sprintf("%040x", time.Now().UnixNano()+1)
	heavyRootFSID := fmt.Sprintf("heavy-root-%d", time.Now().UnixNano())
	emptyRootFSID := fmt.Sprintf("empty-root-%d", time.Now().UnixNano()+1)

	insertSyntheticDirectoryTreeForTest(t, session, repoID, heavyRootFSID, 256, 256, 4096)
	insertSyntheticDirObjectForTest(t, session, repoID, emptyRootFSID, nil)
	insertSyntheticCommitForTest(t, session, repoID, heavyHead, initial.HeadCommitID, heavyRootFSID, "integration heavy head")
	insertSyntheticCommitForTest(t, session, repoID, finalHead, heavyHead, emptyRootFSID, "integration final empty head")
	t.Cleanup(func() {
		// Delete synthetic commits inserted directly into Cassandra.
		for _, commitID := range []string{heavyHead, finalHead} {
			if err := session.Query(`DELETE FROM commits WHERE library_id = ? AND commit_id = ?`, repoID, commitID).Exec(); err != nil {
				t.Errorf("cleanup synthetic commit %s failed: %v", commitID, err)
			}
		}
		// Delete synthetic fs_objects: heavy root + 256 child dirs + empty root.
		fsIDs := make([]string, 0, 258)
		fsIDs = append(fsIDs, heavyRootFSID, emptyRootFSID)
		for i := 0; i < 256; i++ {
			fsIDs = append(fsIDs, fmt.Sprintf("%s-dir-%03d", heavyRootFSID, i))
		}
		for _, fsID := range fsIDs {
			if err := session.Query(`DELETE FROM fs_objects WHERE library_id = ? AND fs_id = ?`, repoID, fsID).Exec(); err != nil {
				t.Errorf("cleanup synthetic fs_object %s failed: %v", fsID, err)
			}
		}
	})

	resp := adminClient.Do(t, http.MethodPut, fmt.Sprintf("/seafhttp/repo/%s/commit/HEAD?head=%s", repoID, url.QueryEscape(heavyHead)), nil)
	if resp.StatusCode != http.StatusOK {
		body := responseBody(t, resp)
		t.Fatalf("heavy sync HEAD update failed: status=%d body=%s", resp.StatusCode, body)
	}
	resp.Body.Close()

	resp = adminClient.Do(t, http.MethodPut, fmt.Sprintf("/seafhttp/repo/%s/commit/HEAD?head=%s", repoID, url.QueryEscape(finalHead)), nil)
	if resp.StatusCode != http.StatusOK {
		body := responseBody(t, resp)
		t.Fatalf("final sync HEAD update failed: status=%d body=%s", resp.StatusCode, body)
	}
	resp.Body.Close()

	waitForIntegrationCondition(t, "final sync head stats to reflect newest empty tree", func() bool {
		current := readLibrarySyncHeadState(t, session, repoID)
		return current.HeadCommitID == finalHead &&
			current.LookupHeadCommitID == finalHead &&
			current.SizeBytes == 0 &&
			current.FileCount == 0 &&
			current.ProjectionSizeBytes == 0 &&
			current.ProjectionFileCount == 0
	})

	stabilityDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(stabilityDeadline) {
		current := readLibrarySyncHeadState(t, session, repoID)
		if current.HeadCommitID != finalHead {
			t.Fatalf("canonical head changed during stat race stabilization: got %q want %q", current.HeadCommitID, finalHead)
		}
		if current.LookupHeadCommitID != finalHead {
			t.Fatalf("lookup head changed during stat race stabilization: got %q want %q", current.LookupHeadCommitID, finalHead)
		}
		if current.SizeBytes != 0 || current.FileCount != 0 {
			t.Fatalf("canonical stats were overwritten by stale recompute: size=%d files=%d", current.SizeBytes, current.FileCount)
		}
		if current.ProjectionSizeBytes != 0 || current.ProjectionFileCount != 0 {
			t.Fatalf("projection stats were overwritten by stale recompute: size=%d files=%d", current.ProjectionSizeBytes, current.ProjectionFileCount)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestSyncHeadUpdateAppliesStorageCountersBeforeReturning(t *testing.T) {
	name := fmt.Sprintf("inttest-sync-storage-counters-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)
	database := shareProjectionDBForTest(t)
	session := database.Session()

	var ownerID string
	if err := session.Query(`SELECT owner_id FROM libraries WHERE org_id = ? AND library_id = ?`, defaultOrgID, repoID).Scan(&ownerID); err != nil {
		t.Fatalf("failed to read owner_id for repo %s: %v", repoID, err)
	}

	platformScope := traffic.PlatformStorageScope()
	orgScope := traffic.OrganizationStorageScope(defaultOrgID)
	userScope := traffic.UserStorageScope(defaultOrgID, ownerID)
	libScope := traffic.LibraryStorageScope(defaultOrgID, repoID)

	baselinePlatform := traffic.ReadStorageSnapshot(database, platformScope)
	baselineOrg := traffic.ReadStorageSnapshot(database, orgScope)
	baselineUser := traffic.ReadStorageSnapshot(database, userScope)
	baselineLib := traffic.ReadStorageSnapshot(database, libScope)

	initial := readLibrarySyncHeadState(t, session, repoID)
	nextHead := fmt.Sprintf("%040x", time.Now().UnixNano())
	nextRootFSID := fmt.Sprintf("sync-storage-root-%d", time.Now().UnixNano())

	const childDirCount = 3
	const filesPerChild = 2
	const fileSize = int64(1234)
	expectedDelta := traffic.StorageSnapshot{
		BytesUsed: childDirCount * filesPerChild * fileSize,
		FileCount: childDirCount * filesPerChild,
	}

	insertSyntheticDirectoryTreeForTest(t, session, repoID, nextRootFSID, childDirCount, filesPerChild, fileSize)
	insertSyntheticCommitForTest(t, session, repoID, nextHead, initial.HeadCommitID, nextRootFSID, "integration sync storage counters")
	t.Cleanup(func() {
		if err := session.Query(`DELETE FROM commits WHERE library_id = ? AND commit_id = ?`, repoID, nextHead).Exec(); err != nil {
			t.Errorf("cleanup synthetic commit %s failed: %v", nextHead, err)
		}
		fsIDs := []string{nextRootFSID}
		for dirIndex := 0; dirIndex < childDirCount; dirIndex++ {
			fsIDs = append(fsIDs, fmt.Sprintf("%s-dir-%03d", nextRootFSID, dirIndex))
		}
		for _, fsID := range fsIDs {
			if err := session.Query(`DELETE FROM fs_objects WHERE library_id = ? AND fs_id = ?`, repoID, fsID).Exec(); err != nil {
				t.Errorf("cleanup synthetic fs_object %s failed: %v", fsID, err)
			}
		}
	})

	resp := adminClient.Do(t, http.MethodPut, fmt.Sprintf("/seafhttp/repo/%s/commit/HEAD?head=%s", repoID, url.QueryEscape(nextHead)), nil)
	if resp.StatusCode != http.StatusOK {
		body := responseBody(t, resp)
		t.Fatalf("sync HEAD update failed: status=%d body=%s", resp.StatusCode, body)
	}
	resp.Body.Close()

	current := readLibrarySyncHeadState(t, session, repoID)
	if current.HeadCommitID != nextHead || current.LookupHeadCommitID != nextHead {
		t.Fatalf("sync HEAD not fully visible after success response: canonical=%q lookup=%q want=%q", current.HeadCommitID, current.LookupHeadCommitID, nextHead)
	}
	if current.SizeBytes != expectedDelta.BytesUsed || current.FileCount != expectedDelta.FileCount {
		t.Fatalf("sync HEAD stats after success response = {%d %d}, want {%d %d}", current.SizeBytes, current.FileCount, expectedDelta.BytesUsed, expectedDelta.FileCount)
	}
	if current.ProjectionSizeBytes != expectedDelta.BytesUsed || current.ProjectionFileCount != expectedDelta.FileCount {
		t.Fatalf("sync HEAD projection stats after success response = {%d %d}, want {%d %d}", current.ProjectionSizeBytes, current.ProjectionFileCount, expectedDelta.BytesUsed, expectedDelta.FileCount)
	}

	if got := traffic.ReadStorageSnapshot(database, libScope); got != addStorageSnapshots(baselineLib, expectedDelta) {
		t.Fatalf("library storage snapshot after sync HEAD = %+v, want %+v", got, addStorageSnapshots(baselineLib, expectedDelta))
	}
	if got := traffic.ReadStorageSnapshot(database, platformScope); got != addStorageSnapshots(baselinePlatform, expectedDelta) {
		t.Fatalf("platform storage snapshot after sync HEAD = %+v, want %+v", got, addStorageSnapshots(baselinePlatform, expectedDelta))
	}
	if got := traffic.ReadStorageSnapshot(database, orgScope); got != addStorageSnapshots(baselineOrg, expectedDelta) {
		t.Fatalf("org storage snapshot after sync HEAD = %+v, want %+v", got, addStorageSnapshots(baselineOrg, expectedDelta))
	}
	if got := traffic.ReadStorageSnapshot(database, userScope); got != addStorageSnapshots(baselineUser, expectedDelta) {
		t.Fatalf("user storage snapshot after sync HEAD = %+v, want %+v", got, addStorageSnapshots(baselineUser, expectedDelta))
	}
}

func TestAdminCreateLibraryProjectionVisibleImmediately(t *testing.T) {
	name := fmt.Sprintf("inttest-admin-lib-projection-%d", time.Now().UnixNano())
	createResp := superadminClient.PostJSON(t, "/api/v2.1/admin/libraries/", map[string]string{
		"name":  name,
		"owner": defaultUserEmail,
	})
	expectStatus(t, createResp, http.StatusOK)
	result := responseJSON(t, createResp)

	repoID, _ := result["id"].(string)
	if repoID == "" {
		t.Fatalf("admin create library response missing id: %v", result)
	}
	t.Cleanup(func() {
		resp := superadminClient.Delete(t, fmt.Sprintf("/api/v2.1/admin/libraries/%s/", repoID))
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound {
			resp.Body.Close()
			return
		}
		body := responseBody(t, resp)
		t.Errorf("cleanup delete admin library %s failed: status=%d body=%s", repoID, resp.StatusCode, body)
	})

	row := requireAdminLibraryByQuery(t, superadminClient, repoID)
	if got, _ := row["name"].(string); got != name {
		t.Fatalf("admin-created library name = %q, want %q", got, name)
	}
	if got, _ := row["owner_email"].(string); got != defaultUserEmail {
		t.Fatalf("admin-created library owner_email = %q, want %q", got, defaultUserEmail)
	}
}

func TestGroupOwnedLibraryProjectionVisibleImmediately(t *testing.T) {
	groupName := fmt.Sprintf("inttest-group-owned-%d", time.Now().UnixNano())
	groupID := createGroupForRegressionTest(t, adminClient, groupName)
	repoName := fmt.Sprintf("inttest-group-owned-lib-%d", time.Now().UnixNano())

	createResp := adminClient.PostJSON(t, fmt.Sprintf("/api/v2.1/groups/%s/group-owned-libraries/", groupID), map[string]string{"name": repoName})
	expectStatus(t, createResp, http.StatusOK)
	result := responseJSON(t, createResp)

	repoID, _ := result["repo_id"].(string)
	if repoID == "" {
		t.Fatalf("group-owned library response missing repo_id: %v", result)
	}
	t.Cleanup(func() {
		resp := adminClient.Delete(t, fmt.Sprintf("/api/v2.1/repos/%s/", repoID))
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound {
			resp.Body.Close()
			return
		}
		body := responseBody(t, resp)
		t.Errorf("cleanup delete group-owned library %s failed: status=%d body=%s", repoID, resp.StatusCode, body)
	})

	row := requireAdminLibraryByQuery(t, superadminClient, repoID)
	if got, _ := row["name"].(string); got != repoName {
		t.Fatalf("group-owned library name = %q, want %q", got, repoName)
	}
	assertGroupSharePresent(t, repoID, groupID, true)
}

func waitForIntegrationCondition(t *testing.T, description string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(pollTimeout)
	for {
		if check() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", description)
		}
		time.Sleep(pollInterval)
	}
}

func requireAdminLibraryByQuery(t *testing.T, c *testClient, query string) map[string]interface{} {
	t.Helper()
	row, ok := adminLibraryByQuery(t, c, query)
	if !ok {
		t.Fatalf("admin library projection missing for query %q", query)
	}
	return row
}

func adminLibraryByQuery(t *testing.T, c *testClient, query string) (map[string]interface{}, bool) {
	t.Helper()
	resp := c.Get(t, "/api/v2.1/admin/search-libraries/?name_or_id="+url.QueryEscape(query)+"&page=1&per_page=100")
	expectStatus(t, resp, http.StatusOK)
	payload := responseJSON(t, resp)
	entries, ok := payload["repo_list"].([]interface{})
	if !ok {
		return nil, false
	}
	for _, entry := range entries {
		row, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		if id, _ := row["id"].(string); id == query {
			return row, true
		}
		if repoID, _ := row["repo_id"].(string); repoID == query {
			return row, true
		}
		if name, _ := row["name"].(string); name == query {
			return row, true
		}
		if repoName, _ := row["repo_name"].(string); repoName == query {
			return row, true
		}
	}
	return nil, false
}

func adminTrashContainsRepo(t *testing.T, c *testClient, repoID, ownerEmail string) bool {
	t.Helper()
	path := "/api/v2.1/admin/trash-libraries/?page=1&per_page=100"
	if ownerEmail != "" {
		path += "&owner=" + url.QueryEscape(ownerEmail)
	}
	resp := c.Get(t, path)
	expectStatus(t, resp, http.StatusOK)
	payload := responseJSON(t, resp)
	entries, ok := payload["repos"].([]interface{})
	if !ok {
		return false
	}
	return containsEntry(entries, "id", repoID)
}

type librarySyncHeadState struct {
	HeadCommitID        string
	LookupHeadCommitID  string
	RootFSID            string
	UpdatedAt           time.Time
	SizeBytes           int64
	FileCount           int64
	ProjectionUpdatedAt time.Time
	ProjectionSizeBytes int64
	ProjectionFileCount int64
}

func readLibrarySyncHeadState(t *testing.T, session interface {
	Query(stmt string, values ...interface{}) *gocql.Query
}, repoID string) librarySyncHeadState {
	t.Helper()

	var state librarySyncHeadState
	if err := session.Query(`
		SELECT head_commit_id, updated_at, size_bytes, file_count FROM libraries WHERE org_id = ? AND library_id = ?
	`, defaultOrgID, repoID).Scan(&state.HeadCommitID, &state.UpdatedAt, &state.SizeBytes, &state.FileCount); err != nil {
		t.Fatalf("failed to read canonical sync head state for %s: %v", repoID, err)
	}
	if err := session.Query(`
		SELECT head_commit_id FROM libraries_by_id WHERE library_id = ?
	`, repoID).Scan(&state.LookupHeadCommitID); err != nil {
		t.Fatalf("failed to read lookup sync head state for %s: %v", repoID, err)
	}
	if err := session.Query(`
		SELECT updated_at, size_bytes, file_count FROM libraries_by_org_updated WHERE org_id = ? AND library_id = ?
	`, defaultOrgID, repoID).Scan(&state.ProjectionUpdatedAt, &state.ProjectionSizeBytes, &state.ProjectionFileCount); err != nil {
		t.Fatalf("failed to read projection sync head state for %s: %v", repoID, err)
	}
	if err := session.Query(`
		SELECT root_fs_id FROM commits WHERE library_id = ? AND commit_id = ?
	`, repoID, state.HeadCommitID).Scan(&state.RootFSID); err != nil {
		t.Fatalf("failed to read root fs id for %s/%s: %v", repoID, state.HeadCommitID, err)
	}

	return state
}

func addStorageSnapshots(left, right traffic.StorageSnapshot) traffic.StorageSnapshot {
	return traffic.StorageSnapshot{
		BytesUsed: left.BytesUsed + right.BytesUsed,
		FileCount: left.FileCount + right.FileCount,
	}
}

func insertSyntheticCommitForTest(t *testing.T, session interface {
	Query(stmt string, values ...interface{}) *gocql.Query
}, repoID, commitID, parentID, rootFSID, description string) {
	t.Helper()
	if err := session.Query(`
		INSERT INTO commits (library_id, commit_id, parent_id, root_fs_id, creator_id, description, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, repoID, commitID, parentID, rootFSID, defaultOrgID, description, time.Now().UTC()).Exec(); err != nil {
		t.Fatalf("failed to insert synthetic commit %s for %s: %v", commitID, repoID, err)
	}
}

var storageCounterTotalDayForTest = time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)

func expectedAggregateStorageSnapshotsForScopes(t *testing.T, database *dbpkg.DB, scopes ...string) map[string]traffic.StorageSnapshot {
	t.Helper()

	expected := make(map[string]traffic.StorageSnapshot, len(scopes))
	requested := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		requested[scope] = struct{}{}
	}

	iter := database.Session().Query(`
		SELECT org_id, library_id, owner_id, deleted_at FROM libraries
	`).Iter()

	var orgID, libraryID, ownerID string
	var deletedAt *time.Time
	for iter.Scan(&orgID, &libraryID, &ownerID, &deletedAt) {
		if deletedAt != nil && !deletedAt.IsZero() {
			continue
		}

		libSnapshot := traffic.ReadStorageSnapshot(database, traffic.LibraryStorageScope(orgID, libraryID))
		if libSnapshot.BytesUsed == 0 && libSnapshot.FileCount == 0 {
			continue
		}

		if _, ok := requested[traffic.PlatformStorageScope()]; ok {
			snap := expected[traffic.PlatformStorageScope()]
			snap.BytesUsed += libSnapshot.BytesUsed
			snap.FileCount += libSnapshot.FileCount
			expected[traffic.PlatformStorageScope()] = snap
		}

		orgScope := traffic.OrganizationStorageScope(orgID)
		if _, ok := requested[orgScope]; ok {
			snap := expected[orgScope]
			snap.BytesUsed += libSnapshot.BytesUsed
			snap.FileCount += libSnapshot.FileCount
			expected[orgScope] = snap
		}

		userScope := traffic.UserStorageScope(orgID, ownerID)
		if _, ok := requested[userScope]; ok {
			snap := expected[userScope]
			snap.BytesUsed += libSnapshot.BytesUsed
			snap.FileCount += libSnapshot.FileCount
			expected[userScope] = snap
		}
	}
	if err := iter.Close(); err != nil {
		t.Fatalf("failed to scan libraries for expected storage reconciliation snapshots: %v", err)
	}

	return expected
}

func addStorageCounterDriftForTest(t *testing.T, session interface {
	Query(stmt string, values ...interface{}) *gocql.Query
}, scope string, deltaBytes, deltaFiles int64) {
	t.Helper()
	today := time.Now().UTC().Truncate(24 * time.Hour)
	for _, day := range []time.Time{storageCounterTotalDayForTest, today} {
		if err := session.Query(`
			UPDATE storage_counters SET bytes_used = bytes_used + ?, file_count = file_count + ?
			WHERE scope = ? AND day = ?
		`, deltaBytes, deltaFiles, scope, day).Exec(); err != nil {
			t.Fatalf("failed to add storage drift for scope %s on %s: %v", scope, day.Format(time.RFC3339), err)
		}
	}
}

func insertStorageReconciliationRequestForTest(t *testing.T, session interface {
	Query(stmt string, values ...interface{}) *gocql.Query
}, scope, orgID, ownerID string) {
	t.Helper()
	orgUUID := gocql.UUID{}
	ownerUUID := gocql.UUID{}
	if orgID != "" {
		parsed, err := gocql.ParseUUID(orgID)
		if err != nil {
			t.Fatalf("failed to parse org UUID %q: %v", orgID, err)
		}
		orgUUID = parsed
	}
	if ownerID != "" {
		parsed, err := gocql.ParseUUID(ownerID)
		if err != nil {
			t.Fatalf("failed to parse owner UUID %q: %v", ownerID, err)
		}
		ownerUUID = parsed
	}
	if err := session.Query(`
		INSERT INTO gc_storage_counter_reconciliation (scope, org_id, owner_id, requested_at)
		VALUES (?, ?, ?, ?)
	`, scope, orgUUID, ownerUUID, time.Now().UTC()).Exec(); err != nil {
		t.Fatalf("failed to insert storage reconciliation request for %s: %v", scope, err)
	}
}

func storageReconciliationRequestExistsForTest(t *testing.T, session interface {
	Query(stmt string, values ...interface{}) *gocql.Query
}, scope string) bool {
	t.Helper()
	var existingScope string
	return session.Query(`SELECT scope FROM gc_storage_counter_reconciliation WHERE scope = ?`, scope).Scan(&existingScope) == nil
}

func deletedLibraryMarkerExistsForTest(t *testing.T, store interface {
	GetLibraryDeletedAt(libraryID uuid.UUID) (*time.Time, error)
}, repoID string) bool {
	t.Helper()
	repoUUID, err := uuid.Parse(repoID)
	if err != nil {
		t.Fatalf("failed to parse repo UUID %q: %v", repoID, err)
	}
	deletedAt, err := store.GetLibraryDeletedAt(repoUUID)
	if err != nil {
		t.Fatalf("failed to read deleted_libraries marker for repo %s: %v", repoID, err)
	}
	return deletedAt != nil
}

func adminLibraryProjectionRowForTest(t *testing.T, session *gocql.Session, orgID, repoID string) (dbpkg.AdminLibraryProjectionRow, bool) {
	t.Helper()
	rows, err := dbpkg.ListAdminOrgLibraryRows(session, orgID)
	if err != nil {
		t.Fatalf("failed to list admin library projection rows for org %s: %v", orgID, err)
	}
	for _, row := range rows {
		if row.LibraryID == repoID {
			return row, true
		}
	}
	return dbpkg.AdminLibraryProjectionRow{}, false
}

func deletedAdminLibraryProjectionRowForTest(t *testing.T, session *gocql.Session, orgID, repoID string) (dbpkg.AdminDeletedLibraryProjectionRow, bool) {
	t.Helper()
	rows, err := dbpkg.ListDeletedAdminLibraryRowsByOrg(session, orgID)
	if err != nil {
		t.Fatalf("failed to list deleted admin library projection rows for org %s: %v", orgID, err)
	}
	for _, row := range rows {
		if row.LibraryID == repoID {
			return row, true
		}
	}
	return dbpkg.AdminDeletedLibraryProjectionRow{}, false
}

func removeAdminLibraryProjectionRowsForSoftDeleteFallbackTest(t *testing.T, session *gocql.Session, orgID, repoID, ownerID string) {
	t.Helper()
	if err := session.Query(`DELETE FROM libraries_by_org_updated WHERE org_id = ? AND library_id = ?`, orgID, repoID).Exec(); err != nil {
		t.Fatalf("failed to remove org projection row for repo %s: %v", repoID, err)
	}
	if err := session.Query(`DELETE FROM libraries_by_owner WHERE org_id = ? AND owner_id = ? AND library_id = ?`, orgID, ownerID, repoID).Exec(); err != nil {
		t.Fatalf("failed to remove owner projection row for repo %s: %v", repoID, err)
	}
	buckets, err := dbpkg.ListAdminLibraryBucketDays(session)
	if err != nil {
		t.Fatalf("failed to list admin library bucket days for repo %s: %v", repoID, err)
	}
	for _, bucketDay := range buckets {
		if err := session.Query(`DELETE FROM libraries_admin_global_by_updated WHERE bucket_day = ? AND org_id = ? AND library_id = ?`, bucketDay, orgID, repoID).Exec(); err != nil {
			t.Fatalf("failed to remove global projection row for repo %s bucket %s: %v", repoID, bucketDay, err)
		}
	}
}

// removeLibraryBaseRowsForFallbackTest deliberately corrupts the base library
// rows to verify GC hard-delete fallback cleanup when only the denormalized
// trash projection remains. There is no canonical API for constructing this
// broken state, so the fixture is intentionally low-level.
func removeLibraryBaseRowsForFallbackTest(t *testing.T, session *gocql.Session, repoID string) {
	t.Helper()
	if err := session.Query(`DELETE FROM libraries WHERE org_id = ? AND library_id = ?`, defaultOrgID, repoID).Exec(); err != nil {
		t.Fatalf("failed to remove base library row for repo %s: %v", repoID, err)
	}
	if err := session.Query(`DELETE FROM libraries_by_id WHERE library_id = ?`, repoID).Exec(); err != nil {
		t.Fatalf("failed to remove library lookup row for repo %s: %v", repoID, err)
	}
	if err := session.Query(`DELETE FROM deleted_libraries WHERE library_id = ?`, repoID).Exec(); err != nil {
		t.Fatalf("failed to remove deleted_libraries marker for repo %s: %v", repoID, err)
	}
}

type syntheticDirEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Mode int    `json:"mode"`
	Size int64  `json:"size,omitempty"`
}

func insertSyntheticDirectoryTreeForTest(t *testing.T, session interface {
	Query(stmt string, values ...interface{}) *gocql.Query
}, repoID, rootFSID string, childDirCount, filesPerChild int, fileSize int64) {
	t.Helper()

	rootEntries := make([]syntheticDirEntry, 0, childDirCount)
	for dirIndex := 0; dirIndex < childDirCount; dirIndex++ {
		childFSID := fmt.Sprintf("%s-dir-%03d", rootFSID, dirIndex)
		rootEntries = append(rootEntries, syntheticDirEntry{
			ID:   childFSID,
			Name: fmt.Sprintf("dir-%03d", dirIndex),
			Mode: 16384,
		})

		childEntries := make([]syntheticDirEntry, 0, filesPerChild)
		for fileIndex := 0; fileIndex < filesPerChild; fileIndex++ {
			childEntries = append(childEntries, syntheticDirEntry{
				ID:   fmt.Sprintf("%s-file-%03d-%03d", rootFSID, dirIndex, fileIndex),
				Name: fmt.Sprintf("file-%03d.dat", fileIndex),
				Mode: 33188,
				Size: fileSize,
			})
		}
		insertSyntheticDirObjectForTest(t, session, repoID, childFSID, childEntries)
	}

	insertSyntheticDirObjectForTest(t, session, repoID, rootFSID, rootEntries)
}

func insertSyntheticDirObjectForTest(t *testing.T, session interface {
	Query(stmt string, values ...interface{}) *gocql.Query
}, repoID, fsID string, entries []syntheticDirEntry) {
	t.Helper()

	entriesJSON, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("failed to marshal synthetic fs object %s: %v", fsID, err)
	}
	if err := session.Query(`
		INSERT INTO fs_objects (library_id, fs_id, obj_type, obj_name, full_path, dir_entries, size_bytes, mtime)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, repoID, fsID, "dir", fsID, "/", string(entriesJSON), int64(0), time.Now().Unix()).Exec(); err != nil {
		t.Fatalf("failed to insert synthetic fs object %s for %s: %v", fsID, repoID, err)
	}
}
