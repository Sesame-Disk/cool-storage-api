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
	expectedPlatformAfterReconcileByShard := expectedPlatformStorageSnapshotsByShardForTest(t, database)

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
	assertPlatformStorageShardsForTest(t, session, expectedPlatformAfterReconcileByShard)
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

func TestLibraryProjectionRegression_ReconcilePendingStorageCountersUsesCanonicalLibraryStats(t *testing.T) {
	name := fmt.Sprintf("inttest-lib-storage-canonical-%d", time.Now().UnixNano())
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

	uploadResp := adminClient.Get(t, fmt.Sprintf("/api2/repos/%s/upload-link/?p=/", repoID))
	expectStatus(t, uploadResp, http.StatusOK)
	uploadURL := strings.Trim(responseBody(t, uploadResp), "\" \n\r")
	uploadFileThroughLink(t, adminClient, uploadURL, "canonical.txt", "/", "canonical-reconciliation-content\n")

	var libBeforeDrift traffic.StorageSnapshot
	waitForIntegrationCondition(t, "library upload to update storage counters for canonical reconciliation", func() bool {
		libBeforeDrift = traffic.ReadStorageSnapshot(database, libScope)
		return libBeforeDrift.BytesUsed > 0 && libBeforeDrift.FileCount > 0
	})

	expected := expectedAggregateStorageSnapshotsForScopes(t, database, platformScope, orgScope, userScope)
	expectedPlatformByShard := expectedPlatformStorageSnapshotsByShardForTest(t, database)

	const driftBytes int64 = 999
	const driftFiles int64 = 7
	addStorageCounterDriftForTest(t, session, libScope, driftBytes, driftFiles)
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

	if got := traffic.ReadStorageSnapshot(database, platformScope); got != expected[platformScope] {
		t.Fatalf("platform snapshot after canonical reconciliation = %+v, want %+v", got, expected[platformScope])
	}
	assertPlatformStorageShardsForTest(t, session, expectedPlatformByShard)
	if got := traffic.ReadStorageSnapshot(database, orgScope); got != expected[orgScope] {
		t.Fatalf("org snapshot after canonical reconciliation = %+v, want %+v", got, expected[orgScope])
	}
	if got := traffic.ReadStorageSnapshot(database, userScope); got != expected[userScope] {
		t.Fatalf("user snapshot after canonical reconciliation = %+v, want %+v", got, expected[userScope])
	}
	if got := traffic.ReadStorageSnapshot(database, libScope); got != addStorageSnapshots(libBeforeDrift, traffic.StorageSnapshot{BytesUsed: driftBytes, FileCount: driftFiles}) {
		t.Fatalf("library snapshot after aggregate reconciliation = %+v, want %+v", got, addStorageSnapshots(libBeforeDrift, traffic.StorageSnapshot{BytesUsed: driftBytes, FileCount: driftFiles}))
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

func TestSyncHeadConflictSameTreeReturnsOKWithoutHeadRollback(t *testing.T) {
	testSyncHeadConflictSameTreeReturnsOKWithoutHeadRollback(t, http.MethodPut, "/seafhttp/repo/%s/commit/HEAD?head=%s")
}

func TestUpdateBranchConflictSameTreeReturnsOKWithoutHeadRollback(t *testing.T) {
	testSyncHeadConflictSameTreeReturnsOKWithoutHeadRollback(t, http.MethodPost, "/seafhttp/repo/%s/update-branch?head=%s")
}

func testSyncHeadConflictSameTreeReturnsOKWithoutHeadRollback(t *testing.T, method, routeFormat string) {
	name := fmt.Sprintf("inttest-sync-same-tree-conflict-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)
	session := shareProjectionDBForTest(t).Session()

	initial := readLibrarySyncHeadState(t, session, repoID)
	acceptedHead := fmt.Sprintf("%040x", time.Now().UnixNano())
	staleHead := fmt.Sprintf("%040x", time.Now().UnixNano()+1)
	insertSyntheticCommitForTest(t, session, repoID, acceptedHead, initial.HeadCommitID, initial.RootFSID, "integration accepted same-tree head")
	insertSyntheticCommitForTest(t, session, repoID, staleHead, initial.HeadCommitID, initial.RootFSID, "integration stale same-tree head")
	t.Cleanup(func() {
		for _, commitID := range []string{acceptedHead, staleHead} {
			if err := session.Query(`DELETE FROM commits WHERE library_id = ? AND commit_id = ?`, repoID, commitID).Exec(); err != nil {
				t.Errorf("cleanup synthetic commit %s failed: %v", commitID, err)
			}
		}
	})

	resp := adminClient.Do(t, method, fmt.Sprintf(routeFormat, repoID, url.QueryEscape(acceptedHead)), nil)
	if resp.StatusCode != http.StatusOK {
		body := responseBody(t, resp)
		t.Fatalf("initial publish failed: status=%d body=%s", resp.StatusCode, body)
	}
	resp.Body.Close()

	var advanced librarySyncHeadState
	waitForIntegrationCondition(t, "accepted same-tree head to become authoritative", func() bool {
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

	conflictResp := adminClient.Do(t, method, fmt.Sprintf(routeFormat, repoID, url.QueryEscape(staleHead)), nil)
	if conflictResp.StatusCode != http.StatusOK {
		body := responseBody(t, conflictResp)
		t.Fatalf("same-tree stale publish returned status=%d body=%s", conflictResp.StatusCode, body)
	}
	conflictResp.Body.Close()

	waitForIntegrationCondition(t, "same-tree stale publish to leave accepted head authoritative", func() bool {
		current := readLibrarySyncHeadState(t, session, repoID)
		return current.HeadCommitID == acceptedHead &&
			current.LookupHeadCommitID == acceptedHead &&
			current.UpdatedAt.Equal(advanced.UpdatedAt) &&
			current.ProjectionUpdatedAt.Equal(advanced.ProjectionUpdatedAt)
	})
}

func TestSyncHeadConflictUnmergeableReturnsRetryable503(t *testing.T) {
	testSyncHeadConflictUnmergeableReturnsRetryable503(t, http.MethodPut, "/seafhttp/repo/%s/commit/HEAD?head=%s")
}

func TestUpdateBranchConflictUnmergeableReturnsRetryable503(t *testing.T) {
	testSyncHeadConflictUnmergeableReturnsRetryable503(t, http.MethodPost, "/seafhttp/repo/%s/update-branch?head=%s")
}

func TestSyncHeadRejectsNonEmptyParentWhenCurrentHeadMissing(t *testing.T) {
	testSyncHeadRejectsNonEmptyParentWhenCurrentHeadMissing(t, http.MethodPut, "/seafhttp/repo/%s/commit/HEAD?head=%s")
}

func TestUpdateBranchRejectsNonEmptyParentWhenCurrentHeadMissing(t *testing.T) {
	testSyncHeadRejectsNonEmptyParentWhenCurrentHeadMissing(t, http.MethodPost, "/seafhttp/repo/%s/update-branch?head=%s")
}

func testSyncHeadRejectsNonEmptyParentWhenCurrentHeadMissing(t *testing.T, method, routeFormat string) {
	name := fmt.Sprintf("inttest-sync-empty-head-guard-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)
	session := shareProjectionDBForTest(t).Session()

	initial := readLibrarySyncHeadState(t, session, repoID)
	targetHead := fmt.Sprintf("%040x", time.Now().UnixNano())
	insertSyntheticCommitForTest(t, session, repoID, targetHead, initial.HeadCommitID, initial.RootFSID, "integration publish with non-empty parent against missing current head")
	t.Cleanup(func() {
		if err := session.Query(`DELETE FROM commits WHERE library_id = ? AND commit_id = ?`, repoID, targetHead).Exec(); err != nil {
			t.Errorf("cleanup synthetic commit %s failed: %v", targetHead, err)
		}
	})
	t.Cleanup(func() {
		restoreTime := time.Now().UTC()
		if err := session.Query(`
			UPDATE libraries SET head_commit_id = ?, updated_at = ? WHERE org_id = ? AND library_id = ?
		`, initial.HeadCommitID, restoreTime, defaultOrgID, repoID).Exec(); err != nil {
			t.Errorf("restore canonical head for %s failed: %v", repoID, err)
		}
		if err := session.Query(`
			UPDATE libraries_by_id SET head_commit_id = ? WHERE library_id = ?
		`, initial.HeadCommitID, repoID).Exec(); err != nil {
			t.Errorf("restore lookup head for %s failed: %v", repoID, err)
		}
	})

	brokenTime := time.Now().UTC()
	if err := session.Query(`
		UPDATE libraries SET head_commit_id = ?, updated_at = ? WHERE org_id = ? AND library_id = ?
	`, "", brokenTime, defaultOrgID, repoID).Exec(); err != nil {
		t.Fatalf("failed to blank canonical head for %s: %v", repoID, err)
	}
	if err := session.Query(`
		UPDATE libraries_by_id SET head_commit_id = ? WHERE library_id = ?
	`, "", repoID).Exec(); err != nil {
		t.Fatalf("failed to blank lookup head for %s: %v", repoID, err)
	}

	resp := adminClient.Do(t, method, fmt.Sprintf(routeFormat, repoID, url.QueryEscape(targetHead)), nil)
	body := responseBody(t, resp)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("publish against missing current head returned status=%d body=%s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After header = %q, want %q", got, "1")
	}
	if !strings.Contains(body, "sync head publish conflicted; retry") {
		t.Fatalf("publish against missing current head body = %q, want retry hint", body)
	}

	var canonicalHead string
	if err := session.Query(`
		SELECT head_commit_id FROM libraries WHERE org_id = ? AND library_id = ?
	`, defaultOrgID, repoID).Scan(&canonicalHead); err != nil {
		t.Fatalf("failed to read canonical head after empty-head guard test: %v", err)
	}
	if canonicalHead != "" {
		t.Fatalf("canonical head advanced to %q, want empty head to remain unchanged", canonicalHead)
	}
}

func testSyncHeadConflictUnmergeableReturnsRetryable503(t *testing.T, method, routeFormat string) {
	name := fmt.Sprintf("inttest-sync-conflict-503-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)
	session := shareProjectionDBForTest(t).Session()

	initial := readLibrarySyncHeadState(t, session, repoID)
	seed := time.Now().UnixNano()
	currentFileFSID := fmt.Sprintf("%040x", seed+1)
	targetFileFSID := fmt.Sprintf("%040x", seed+2)
	currentRootFSID := fmt.Sprintf("%040x", seed+3)
	targetRootFSID := fmt.Sprintf("%040x", seed+4)
	currentHead := fmt.Sprintf("%040x", seed+5)
	staleHead := fmt.Sprintf("%040x", seed+6)

	insertSyntheticFileObjectForTest(t, session, repoID, currentFileFSID, 61)
	insertSyntheticFileObjectForTest(t, session, repoID, targetFileFSID, 62)
	insertSyntheticDirObjectForTest(t, session, repoID, currentRootFSID, []syntheticDirEntry{{
		ID:   currentFileFSID,
		Name: "collision.txt",
		Mode: 33188,
		Size: 61,
	}})
	insertSyntheticDirObjectForTest(t, session, repoID, targetRootFSID, []syntheticDirEntry{{
		ID:   targetFileFSID,
		Name: "collision.txt",
		Mode: 33188,
		Size: 62,
	}})
	insertSyntheticCommitForTest(t, session, repoID, currentHead, initial.HeadCommitID, currentRootFSID, "integration current unmergeable head")
	insertSyntheticCommitForTest(t, session, repoID, staleHead, initial.HeadCommitID, targetRootFSID, "integration stale unmergeable head")
	t.Cleanup(func() {
		for _, commitID := range []string{currentHead, staleHead} {
			if err := session.Query(`DELETE FROM commits WHERE library_id = ? AND commit_id = ?`, repoID, commitID).Exec(); err != nil {
				t.Errorf("cleanup synthetic commit %s failed: %v", commitID, err)
			}
		}
		for _, fsID := range []string{currentRootFSID, targetRootFSID, currentFileFSID, targetFileFSID} {
			if err := session.Query(`DELETE FROM fs_objects WHERE library_id = ? AND fs_id = ?`, repoID, fsID).Exec(); err != nil {
				t.Errorf("cleanup synthetic fs_object %s failed: %v", fsID, err)
			}
		}
	})

	acceptedResp := adminClient.Do(t, method, fmt.Sprintf(routeFormat, repoID, url.QueryEscape(currentHead)), nil)
	if acceptedResp.StatusCode != http.StatusOK {
		body := responseBody(t, acceptedResp)
		t.Fatalf("accepted publish returned status=%d body=%s", acceptedResp.StatusCode, body)
	}
	acceptedResp.Body.Close()

	var advanced librarySyncHeadState
	waitForIntegrationCondition(t, "accepted unmergeable head to become authoritative", func() bool {
		current := readLibrarySyncHeadState(t, session, repoID)
		if current.HeadCommitID != currentHead || current.LookupHeadCommitID != currentHead {
			return false
		}
		if !current.ProjectionUpdatedAt.Equal(current.UpdatedAt) {
			return false
		}
		advanced = current
		return true
	})

	conflictResp := adminClient.Do(t, method, fmt.Sprintf(routeFormat, repoID, url.QueryEscape(staleHead)), nil)
	body := responseBody(t, conflictResp)
	if conflictResp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("unmergeable stale publish returned status=%d body=%s", conflictResp.StatusCode, body)
	}
	if got := conflictResp.Header.Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After header = %q, want %q", got, "1")
	}
	if !strings.Contains(body, "sync head publish conflicted; retry") {
		t.Fatalf("unmergeable stale publish body = %q, want retry hint", body)
	}

	waitForIntegrationCondition(t, "unmergeable stale publish to leave accepted head authoritative", func() bool {
		current := readLibrarySyncHeadState(t, session, repoID)
		return current.HeadCommitID == currentHead &&
			current.LookupHeadCommitID == currentHead &&
			current.UpdatedAt.Equal(advanced.UpdatedAt) &&
			current.ProjectionUpdatedAt.Equal(advanced.ProjectionUpdatedAt)
	})
}

func TestSyncHeadConflictAutoMergesNonOverlappingEntries(t *testing.T) {
	testSyncHeadConflictAutoMergeNonOverlappingEntries(t, http.MethodPut, "/seafhttp/repo/%s/commit/HEAD?head=%s")
}

func TestUpdateBranchConflictAutoMergesNonOverlappingEntries(t *testing.T) {
	testSyncHeadConflictAutoMergeNonOverlappingEntries(t, http.MethodPost, "/seafhttp/repo/%s/update-branch?head=%s")
}

func testSyncHeadConflictAutoMergeNonOverlappingEntries(t *testing.T, method, routeFormat string) {
	name := fmt.Sprintf("inttest-sync-auto-merge-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)
	session := shareProjectionDBForTest(t).Session()
	t.Cleanup(func() {
		resp := adminClient.Delete(t, fmt.Sprintf("/api/v2.1/repos/%s/", repoID))
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound {
			resp.Body.Close()
			return
		}
		body := responseBody(t, resp)
		t.Errorf("cleanup delete library %s failed: status=%d body=%s", repoID, resp.StatusCode, body)
	})

	initial := readLibrarySyncHeadState(t, session, repoID)
	seed := time.Now().UnixNano()
	currentFileFSID := fmt.Sprintf("%040x", seed+1)
	targetFileFSID := fmt.Sprintf("%040x", seed+2)
	currentRootFSID := fmt.Sprintf("%040x", seed+3)
	targetRootFSID := fmt.Sprintf("%040x", seed+4)
	currentHead := fmt.Sprintf("%040x", seed+5)
	staleHead := fmt.Sprintf("%040x", seed+6)

	insertSyntheticFileObjectForTest(t, session, repoID, currentFileFSID, 61)
	insertSyntheticFileObjectForTest(t, session, repoID, targetFileFSID, 60)
	insertSyntheticDirObjectForTest(t, session, repoID, currentRootFSID, []syntheticDirEntry{{
		ID:   currentFileFSID,
		Name: "client-1.txt",
		Mode: 33188,
		Size: 61,
	}})
	insertSyntheticDirObjectForTest(t, session, repoID, targetRootFSID, []syntheticDirEntry{{
		ID:   targetFileFSID,
		Name: "client-2.txt",
		Mode: 33188,
		Size: 60,
	}})
	insertSyntheticCommitForTest(t, session, repoID, currentHead, initial.HeadCommitID, currentRootFSID, "integration current sync head")
	insertSyntheticCommitForTest(t, session, repoID, staleHead, initial.HeadCommitID, targetRootFSID, "integration stale sync head")
	t.Cleanup(func() {
		for _, commitID := range []string{currentHead, staleHead} {
			if err := session.Query(`DELETE FROM commits WHERE library_id = ? AND commit_id = ?`, repoID, commitID).Exec(); err != nil {
				t.Errorf("cleanup synthetic commit %s failed: %v", commitID, err)
			}
		}
		for _, fsID := range []string{currentRootFSID, targetRootFSID, currentFileFSID, targetFileFSID} {
			if err := session.Query(`DELETE FROM fs_objects WHERE library_id = ? AND fs_id = ?`, repoID, fsID).Exec(); err != nil {
				t.Errorf("cleanup synthetic fs_object %s failed: %v", fsID, err)
			}
		}
	})

	acceptedResp := adminClient.Do(t, method, fmt.Sprintf(routeFormat, repoID, url.QueryEscape(currentHead)), nil)
	if acceptedResp.StatusCode != http.StatusOK {
		body := responseBody(t, acceptedResp)
		t.Fatalf("accepted publish returned status=%d body=%s", acceptedResp.StatusCode, body)
	}
	acceptedResp.Body.Close()

	waitForIntegrationCondition(t, "current sync head to become authoritative before auto-merge", func() bool {
		current := readLibrarySyncHeadState(t, session, repoID)
		return current.HeadCommitID == currentHead && current.LookupHeadCommitID == currentHead
	})

	mergeResp := adminClient.Do(t, method, fmt.Sprintf(routeFormat, repoID, url.QueryEscape(staleHead)), nil)
	if mergeResp.StatusCode != http.StatusOK {
		body := responseBody(t, mergeResp)
		t.Fatalf("stale publish returned status=%d body=%s", mergeResp.StatusCode, body)
	}
	mergeResp.Body.Close()

	waitForIntegrationCondition(t, "non-overlapping stale sync publish to auto-merge into a new HEAD", func() bool {
		current := readLibrarySyncHeadState(t, session, repoID)
		if current.HeadCommitID == currentHead || current.HeadCommitID == staleHead {
			return false
		}
		if current.LookupHeadCommitID != current.HeadCommitID {
			return false
		}
		if !current.ProjectionUpdatedAt.Equal(current.UpdatedAt) {
			return false
		}
		if current.FileCount < 2 || current.SizeBytes < 121 {
			return false
		}

		var entriesJSON string
		if err := session.Query(`
			SELECT dir_entries FROM fs_objects WHERE library_id = ? AND fs_id = ?
		`, repoID, current.RootFSID).Scan(&entriesJSON); err != nil {
			return false
		}
		var entries []syntheticDirEntry
		if err := json.Unmarshal([]byte(entriesJSON), &entries); err != nil {
			return false
		}

		haveClient1 := false
		haveClient2 := false
		for _, entry := range entries {
			switch entry.Name {
			case "client-1.txt":
				haveClient1 = true
			case "client-2.txt":
				haveClient2 = true
			}
		}
		return haveClient1 && haveClient2
	})
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

func TestSyncHeadSameHeadRepairsMissingOwnerProjection(t *testing.T) {
	testSyncHeadSameHeadRepairsMissingOwnerProjection(t, http.MethodPut, "/seafhttp/repo/%s/commit/HEAD?head=%s")
}

func TestUpdateBranchSameHeadRepairsMissingOwnerProjection(t *testing.T) {
	testSyncHeadSameHeadRepairsMissingOwnerProjection(t, http.MethodPost, "/seafhttp/repo/%s/update-branch?head=%s")
}

func testSyncHeadSameHeadRepairsMissingOwnerProjection(t *testing.T, method, routeFormat string) {
	name := fmt.Sprintf("inttest-sync-idempotent-owner-repair-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)
	session := shareProjectionDBForTest(t).Session()

	var ownerID string
	if err := session.Query(`SELECT owner_id FROM libraries WHERE org_id = ? AND library_id = ?`, defaultOrgID, repoID).Scan(&ownerID); err != nil {
		t.Fatalf("failed to read owner_id for repo %s: %v", repoID, err)
	}

	initial := readLibrarySyncHeadState(t, session, repoID)
	nextHead := fmt.Sprintf("%040x", time.Now().UnixNano())
	insertSyntheticCommitForTest(t, session, repoID, nextHead, initial.HeadCommitID, initial.RootFSID, "integration idempotent owner-projection repair")
	t.Cleanup(func() {
		if err := session.Query(`DELETE FROM commits WHERE library_id = ? AND commit_id = ?`, repoID, nextHead).Exec(); err != nil {
			t.Errorf("cleanup synthetic commit %s failed: %v", nextHead, err)
		}
	})

	resp := adminClient.Do(t, method, fmt.Sprintf(routeFormat, repoID, url.QueryEscape(nextHead)), nil)
	if resp.StatusCode != http.StatusOK {
		body := responseBody(t, resp)
		t.Fatalf("initial same-head repair setup failed: status=%d body=%s", resp.StatusCode, body)
	}
	resp.Body.Close()

	var advanced librarySyncHeadState
	waitForIntegrationCondition(t, "accepted head for owner-projection repair test to become authoritative", func() bool {
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

	if err := session.Query(`DELETE FROM libraries_by_owner WHERE org_id = ? AND owner_id = ? AND library_id = ?`, defaultOrgID, ownerID, repoID).Exec(); err != nil {
		t.Fatalf("failed to remove owner projection row for repo %s: %v", repoID, err)
	}
	if _, ok := adminLibraryProjectionRowForTest(t, session, defaultOrgID, repoID); !ok {
		t.Fatalf("org projection row unexpectedly missing for repo %s after deleting owner projection", repoID)
	}

	idempotentResp := adminClient.Do(t, method, fmt.Sprintf(routeFormat, repoID, url.QueryEscape(nextHead)), nil)
	if idempotentResp.StatusCode != http.StatusOK {
		body := responseBody(t, idempotentResp)
		t.Fatalf("idempotent retry after owner projection loss returned status=%d body=%s", idempotentResp.StatusCode, body)
	}
	idempotentResp.Body.Close()

	waitForIntegrationCondition(t, "idempotent same-head retry to rebuild missing owner projection", func() bool {
		current := readLibrarySyncHeadState(t, session, repoID)
		if current.HeadCommitID != nextHead || current.LookupHeadCommitID != nextHead {
			return false
		}
		ownerRow, ok := adminOwnerLibraryProjectionRowForTest(t, session, defaultOrgID, ownerID, repoID)
		if !ok {
			return false
		}
		return ownerRow.UpdatedAt.Equal(advanced.ProjectionUpdatedAt) && ownerRow.SizeBytes == advanced.ProjectionSizeBytes && ownerRow.FileCount == advanced.ProjectionFileCount
	})

	stabilized := readLibrarySyncHeadState(t, session, repoID)
	if !stabilized.UpdatedAt.Equal(advanced.UpdatedAt) {
		t.Fatalf("canonical updated_at changed after owner projection repair: got %s want %s", stabilized.UpdatedAt.Format(time.RFC3339Nano), advanced.UpdatedAt.Format(time.RFC3339Nano))
	}
	if !stabilized.ProjectionUpdatedAt.Equal(advanced.ProjectionUpdatedAt) {
		t.Fatalf("org projection updated_at changed after owner projection repair: got %s want %s", stabilized.ProjectionUpdatedAt.Format(time.RFC3339Nano), advanced.ProjectionUpdatedAt.Format(time.RFC3339Nano))
	}
	ownerRow, ok := adminOwnerLibraryProjectionRowForTest(t, session, defaultOrgID, ownerID, repoID)
	if !ok {
		t.Fatalf("owner projection row was not recreated for repo %s", repoID)
	}
	if !ownerRow.UpdatedAt.Equal(advanced.ProjectionUpdatedAt) || ownerRow.SizeBytes != advanced.ProjectionSizeBytes || ownerRow.FileCount != advanced.ProjectionFileCount {
		t.Fatalf("owner projection after repair = {updated_at=%s size=%d files=%d}, want {%s %d %d}", ownerRow.UpdatedAt.Format(time.RFC3339Nano), ownerRow.SizeBytes, ownerRow.FileCount, advanced.ProjectionUpdatedAt.Format(time.RFC3339Nano), advanced.ProjectionSizeBytes, advanced.ProjectionFileCount)
	}
}

func TestSyncHeadSameHeadRepairsMissingOrgProjection(t *testing.T) {
	testSyncHeadSameHeadRepairsMissingOrgProjection(t, http.MethodPut, "/seafhttp/repo/%s/commit/HEAD?head=%s")
}

func TestUpdateBranchSameHeadRepairsMissingOrgProjection(t *testing.T) {
	testSyncHeadSameHeadRepairsMissingOrgProjection(t, http.MethodPost, "/seafhttp/repo/%s/update-branch?head=%s")
}

// testSyncHeadSameHeadRepairsMissingOrgProjection is the org-projection mirror
// of testSyncHeadSameHeadRepairsMissingOwnerProjection. It validates that the
// idempotent fast-path's canary catches a missing libraries_by_org_updated row
// and reruns the full repair to recreate it from the canonical state. The owner
// and global projection rows must remain present so the test isolates the
// org-projection surface and proves repair targets it specifically.
func testSyncHeadSameHeadRepairsMissingOrgProjection(t *testing.T, method, routeFormat string) {
	name := fmt.Sprintf("inttest-sync-idempotent-org-repair-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)
	session := shareProjectionDBForTest(t).Session()

	var ownerID string
	if err := session.Query(`SELECT owner_id FROM libraries WHERE org_id = ? AND library_id = ?`, defaultOrgID, repoID).Scan(&ownerID); err != nil {
		t.Fatalf("failed to read owner_id for repo %s: %v", repoID, err)
	}

	initial := readLibrarySyncHeadState(t, session, repoID)
	nextHead := fmt.Sprintf("%040x", time.Now().UnixNano())
	insertSyntheticCommitForTest(t, session, repoID, nextHead, initial.HeadCommitID, initial.RootFSID, "integration idempotent org-projection repair")
	t.Cleanup(func() {
		if err := session.Query(`DELETE FROM commits WHERE library_id = ? AND commit_id = ?`, repoID, nextHead).Exec(); err != nil {
			t.Errorf("cleanup synthetic commit %s failed: %v", nextHead, err)
		}
	})

	resp := adminClient.Do(t, method, fmt.Sprintf(routeFormat, repoID, url.QueryEscape(nextHead)), nil)
	if resp.StatusCode != http.StatusOK {
		body := responseBody(t, resp)
		t.Fatalf("initial same-head repair setup failed: status=%d body=%s", resp.StatusCode, body)
	}
	resp.Body.Close()

	var advanced librarySyncHeadState
	waitForIntegrationCondition(t, "accepted head for org-projection repair test to become authoritative", func() bool {
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

	if err := session.Query(`DELETE FROM libraries_by_org_updated WHERE org_id = ? AND library_id = ?`, defaultOrgID, repoID).Exec(); err != nil {
		t.Fatalf("failed to remove org projection row for repo %s: %v", repoID, err)
	}
	if _, ok := adminOwnerLibraryProjectionRowForTest(t, session, defaultOrgID, ownerID, repoID); !ok {
		t.Fatalf("owner projection row unexpectedly missing for repo %s after deleting org projection", repoID)
	}

	idempotentResp := adminClient.Do(t, method, fmt.Sprintf(routeFormat, repoID, url.QueryEscape(nextHead)), nil)
	if idempotentResp.StatusCode != http.StatusOK {
		body := responseBody(t, idempotentResp)
		t.Fatalf("idempotent retry after org projection loss returned status=%d body=%s", idempotentResp.StatusCode, body)
	}
	idempotentResp.Body.Close()

	waitForIntegrationCondition(t, "idempotent same-head retry to rebuild missing org projection", func() bool {
		current := readLibrarySyncHeadState(t, session, repoID)
		if current.HeadCommitID != nextHead || current.LookupHeadCommitID != nextHead {
			return false
		}
		orgRow, ok := adminLibraryProjectionRowForTest(t, session, defaultOrgID, repoID)
		if !ok {
			return false
		}
		return orgRow.UpdatedAt.Equal(advanced.ProjectionUpdatedAt) && orgRow.SizeBytes == advanced.ProjectionSizeBytes && orgRow.FileCount == advanced.ProjectionFileCount
	})

	stabilized := readLibrarySyncHeadState(t, session, repoID)
	if !stabilized.UpdatedAt.Equal(advanced.UpdatedAt) {
		t.Fatalf("canonical updated_at changed after org projection repair: got %s want %s", stabilized.UpdatedAt.Format(time.RFC3339Nano), advanced.UpdatedAt.Format(time.RFC3339Nano))
	}
	orgRow, ok := adminLibraryProjectionRowForTest(t, session, defaultOrgID, repoID)
	if !ok {
		t.Fatalf("org projection row was not recreated for repo %s", repoID)
	}
	if !orgRow.UpdatedAt.Equal(advanced.ProjectionUpdatedAt) || orgRow.SizeBytes != advanced.ProjectionSizeBytes || orgRow.FileCount != advanced.ProjectionFileCount {
		t.Fatalf("org projection after repair = {updated_at=%s size=%d files=%d}, want {%s %d %d}", orgRow.UpdatedAt.Format(time.RFC3339Nano), orgRow.SizeBytes, orgRow.FileCount, advanced.ProjectionUpdatedAt.Format(time.RFC3339Nano), advanced.ProjectionSizeBytes, advanced.ProjectionFileCount)
	}
}

func TestSyncHeadSameHeadRepairsMissingGlobalProjection(t *testing.T) {
	testSyncHeadSameHeadRepairsMissingGlobalProjection(t, http.MethodPut, "/seafhttp/repo/%s/commit/HEAD?head=%s")
}

func TestUpdateBranchSameHeadRepairsMissingGlobalProjection(t *testing.T) {
	testSyncHeadSameHeadRepairsMissingGlobalProjection(t, http.MethodPost, "/seafhttp/repo/%s/update-branch?head=%s")
}

// testSyncHeadSameHeadRepairsMissingGlobalProjection is the global-projection
// mirror of the owner and org variants. The canary read of
// libraries_admin_global_by_updated is bucketed by created_at day, so the
// fixture sets up the deletion against every active bucket day to avoid
// depending on the test clock. The owner and org projections remain present
// to isolate the global-projection surface.
func testSyncHeadSameHeadRepairsMissingGlobalProjection(t *testing.T, method, routeFormat string) {
	name := fmt.Sprintf("inttest-sync-idempotent-global-repair-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)
	session := shareProjectionDBForTest(t).Session()

	var ownerID string
	if err := session.Query(`SELECT owner_id FROM libraries WHERE org_id = ? AND library_id = ?`, defaultOrgID, repoID).Scan(&ownerID); err != nil {
		t.Fatalf("failed to read owner_id for repo %s: %v", repoID, err)
	}

	initial := readLibrarySyncHeadState(t, session, repoID)
	nextHead := fmt.Sprintf("%040x", time.Now().UnixNano())
	insertSyntheticCommitForTest(t, session, repoID, nextHead, initial.HeadCommitID, initial.RootFSID, "integration idempotent global-projection repair")
	t.Cleanup(func() {
		if err := session.Query(`DELETE FROM commits WHERE library_id = ? AND commit_id = ?`, repoID, nextHead).Exec(); err != nil {
			t.Errorf("cleanup synthetic commit %s failed: %v", nextHead, err)
		}
	})

	resp := adminClient.Do(t, method, fmt.Sprintf(routeFormat, repoID, url.QueryEscape(nextHead)), nil)
	if resp.StatusCode != http.StatusOK {
		body := responseBody(t, resp)
		t.Fatalf("initial same-head repair setup failed: status=%d body=%s", resp.StatusCode, body)
	}
	resp.Body.Close()

	var advanced librarySyncHeadState
	waitForIntegrationCondition(t, "accepted head for global-projection repair test to become authoritative", func() bool {
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

	// Delete the global projection row from every active bucket day; the
	// canary reads via the canonical created_at-derived bucket, but the
	// fixture stays robust against clock skew during test setup.
	buckets, err := dbpkg.ListAdminLibraryBucketDays(session)
	if err != nil {
		t.Fatalf("failed to list admin library bucket days for repo %s: %v", repoID, err)
	}
	for _, bucketDay := range buckets {
		if err := session.Query(`DELETE FROM libraries_admin_global_by_updated WHERE bucket_day = ? AND org_id = ? AND library_id = ?`, bucketDay, defaultOrgID, repoID).Exec(); err != nil {
			t.Fatalf("failed to remove global projection row for repo %s bucket %s: %v", repoID, bucketDay, err)
		}
	}

	if _, ok := adminOwnerLibraryProjectionRowForTest(t, session, defaultOrgID, ownerID, repoID); !ok {
		t.Fatalf("owner projection row unexpectedly missing for repo %s after deleting global projection", repoID)
	}
	if _, ok := adminLibraryProjectionRowForTest(t, session, defaultOrgID, repoID); !ok {
		t.Fatalf("org projection row unexpectedly missing for repo %s after deleting global projection", repoID)
	}

	idempotentResp := adminClient.Do(t, method, fmt.Sprintf(routeFormat, repoID, url.QueryEscape(nextHead)), nil)
	if idempotentResp.StatusCode != http.StatusOK {
		body := responseBody(t, idempotentResp)
		t.Fatalf("idempotent retry after global projection loss returned status=%d body=%s", idempotentResp.StatusCode, body)
	}
	idempotentResp.Body.Close()

	waitForIntegrationCondition(t, "idempotent same-head retry to rebuild missing global projection", func() bool {
		globalRow, ok := globalAdminLibraryProjectionRowForTest(t, session, defaultOrgID, repoID)
		if !ok {
			return false
		}
		return globalRow.UpdatedAt.Equal(advanced.ProjectionUpdatedAt) && globalRow.SizeBytes == advanced.ProjectionSizeBytes && globalRow.FileCount == advanced.ProjectionFileCount
	})

	stabilized := readLibrarySyncHeadState(t, session, repoID)
	if !stabilized.UpdatedAt.Equal(advanced.UpdatedAt) {
		t.Fatalf("canonical updated_at changed after global projection repair: got %s want %s", stabilized.UpdatedAt.Format(time.RFC3339Nano), advanced.UpdatedAt.Format(time.RFC3339Nano))
	}
}

func TestSyncHeadSameHeadRepairsDriftedProjectionStats(t *testing.T) {
	testSyncHeadSameHeadRepairsDriftedProjectionStats(t, http.MethodPut, "/seafhttp/repo/%s/commit/HEAD?head=%s")
}

func TestUpdateBranchSameHeadRepairsDriftedProjectionStats(t *testing.T) {
	testSyncHeadSameHeadRepairsDriftedProjectionStats(t, http.MethodPost, "/seafhttp/repo/%s/update-branch?head=%s")
}

// testSyncHeadSameHeadRepairsDriftedProjectionStats exercises the case where
// every projection row is PRESENT but disagrees with canonical state on one
// of the CAS-controlled fields (size_bytes/file_count/updated_at). The canary
// still has to flag drift and the repair must overwrite the drifted values
// with the canonical truth. This is the post-CAS counter-delta-OK + projection
// drift case that motivated extending the canary beyond just head_commit_id.
func testSyncHeadSameHeadRepairsDriftedProjectionStats(t *testing.T, method, routeFormat string) {
	name := fmt.Sprintf("inttest-sync-idempotent-stats-drift-%d", time.Now().UnixNano())
	repoID := createTestLibrary(t, adminClient, name)
	session := shareProjectionDBForTest(t).Session()

	var ownerID string
	if err := session.Query(`SELECT owner_id FROM libraries WHERE org_id = ? AND library_id = ?`, defaultOrgID, repoID).Scan(&ownerID); err != nil {
		t.Fatalf("failed to read owner_id for repo %s: %v", repoID, err)
	}

	initial := readLibrarySyncHeadState(t, session, repoID)
	nextHead := fmt.Sprintf("%040x", time.Now().UnixNano())
	insertSyntheticCommitForTest(t, session, repoID, nextHead, initial.HeadCommitID, initial.RootFSID, "integration idempotent drifted-stats repair")
	t.Cleanup(func() {
		if err := session.Query(`DELETE FROM commits WHERE library_id = ? AND commit_id = ?`, repoID, nextHead).Exec(); err != nil {
			t.Errorf("cleanup synthetic commit %s failed: %v", nextHead, err)
		}
	})

	resp := adminClient.Do(t, method, fmt.Sprintf(routeFormat, repoID, url.QueryEscape(nextHead)), nil)
	if resp.StatusCode != http.StatusOK {
		body := responseBody(t, resp)
		t.Fatalf("initial same-head repair setup failed: status=%d body=%s", resp.StatusCode, body)
	}
	resp.Body.Close()

	var advanced librarySyncHeadState
	waitForIntegrationCondition(t, "accepted head for stats-drift repair test to become authoritative", func() bool {
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

	const driftBytes int64 = 7777777
	const driftFiles int64 = 99
	if err := session.Query(`UPDATE libraries_by_org_updated SET size_bytes = ?, file_count = ? WHERE org_id = ? AND library_id = ?`, driftBytes, driftFiles, defaultOrgID, repoID).Exec(); err != nil {
		t.Fatalf("failed to drift org projection size/file count for repo %s: %v", repoID, err)
	}
	if err := session.Query(`UPDATE libraries_by_owner SET size_bytes = ?, file_count = ? WHERE org_id = ? AND owner_id = ? AND library_id = ?`, driftBytes, driftFiles, defaultOrgID, ownerID, repoID).Exec(); err != nil {
		t.Fatalf("failed to drift owner projection size/file count for repo %s: %v", repoID, err)
	}
	buckets, err := dbpkg.ListAdminLibraryBucketDays(session)
	if err != nil {
		t.Fatalf("failed to list admin library bucket days for repo %s: %v", repoID, err)
	}
	for _, bucketDay := range buckets {
		if err := session.Query(`UPDATE libraries_admin_global_by_updated SET size_bytes = ?, file_count = ? WHERE bucket_day = ? AND org_id = ? AND library_id = ?`, driftBytes, driftFiles, bucketDay, defaultOrgID, repoID).Exec(); err != nil {
			t.Fatalf("failed to drift global projection size/file count for repo %s bucket %s: %v", repoID, bucketDay, err)
		}
	}

	idempotentResp := adminClient.Do(t, method, fmt.Sprintf(routeFormat, repoID, url.QueryEscape(nextHead)), nil)
	if idempotentResp.StatusCode != http.StatusOK {
		body := responseBody(t, idempotentResp)
		t.Fatalf("idempotent retry against drifted stats returned status=%d body=%s", idempotentResp.StatusCode, body)
	}
	idempotentResp.Body.Close()

	waitForIntegrationCondition(t, "idempotent same-head retry to overwrite drifted projection stats with canonical", func() bool {
		orgRow, ok := adminLibraryProjectionRowForTest(t, session, defaultOrgID, repoID)
		if !ok {
			return false
		}
		if orgRow.SizeBytes != advanced.ProjectionSizeBytes || orgRow.FileCount != advanced.ProjectionFileCount {
			return false
		}
		ownerRow, ok := adminOwnerLibraryProjectionRowForTest(t, session, defaultOrgID, ownerID, repoID)
		if !ok {
			return false
		}
		if ownerRow.SizeBytes != advanced.ProjectionSizeBytes || ownerRow.FileCount != advanced.ProjectionFileCount {
			return false
		}
		globalRow, ok := globalAdminLibraryProjectionRowForTest(t, session, defaultOrgID, repoID)
		if !ok {
			return false
		}
		return globalRow.SizeBytes == advanced.ProjectionSizeBytes && globalRow.FileCount == advanced.ProjectionFileCount
	})

	stabilized := readLibrarySyncHeadState(t, session, repoID)
	if !stabilized.UpdatedAt.Equal(advanced.UpdatedAt) {
		t.Fatalf("canonical updated_at changed after drifted-stats repair: got %s want %s", stabilized.UpdatedAt.Format(time.RFC3339Nano), advanced.UpdatedAt.Format(time.RFC3339Nano))
	}
	if stabilized.SizeBytes != advanced.SizeBytes || stabilized.FileCount != advanced.FileCount {
		t.Fatalf("canonical size/file_count changed after drifted-stats repair: got {size=%d files=%d} want {size=%d files=%d}", stabilized.SizeBytes, stabilized.FileCount, advanced.SizeBytes, advanced.FileCount)
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
		// Delete synthetic fs_objects: heavy root + 256 child dirs + 256*256 file rows + empty root.
		fsIDs := make([]string, 0, 258+256*256)
		fsIDs = append(fsIDs, heavyRootFSID, emptyRootFSID)
		for i := 0; i < 256; i++ {
			fsIDs = append(fsIDs, fmt.Sprintf("%s-dir-%03d", heavyRootFSID, i))
			for j := 0; j < 256; j++ {
				fsIDs = append(fsIDs, fmt.Sprintf("%s-file-%03d-%03d", heavyRootFSID, i, j))
			}
		}
		deleteSyntheticFSObjectsForTest(t, session, repoID, fsIDs)
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
			for fileIndex := 0; fileIndex < filesPerChild; fileIndex++ {
				fsIDs = append(fsIDs, fmt.Sprintf("%s-file-%03d-%03d", nextRootFSID, dirIndex, fileIndex))
			}
		}
		deleteSyntheticFSObjectsForTest(t, session, repoID, fsIDs)
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
		SELECT org_id, owner_id, size_bytes, file_count, deleted_at FROM libraries
	`).Iter()

	var orgID, ownerID string
	var sizeBytes, fileCount int64
	var deletedAt *time.Time
	for iter.Scan(&orgID, &ownerID, &sizeBytes, &fileCount, &deletedAt) {
		if deletedAt != nil && !deletedAt.IsZero() {
			continue
		}

		libSnapshot := traffic.StorageSnapshot{BytesUsed: sizeBytes, FileCount: fileCount}
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

func expectedPlatformStorageSnapshotsByShardForTest(t *testing.T, database *dbpkg.DB) map[int]traffic.StorageSnapshot {
	t.Helper()

	expected := make(map[int]traffic.StorageSnapshot, traffic.CounterShardCount)
	iter := database.Session().Query(`
		SELECT org_id, size_bytes, file_count, deleted_at FROM libraries
	`).Iter()

	var orgID string
	var sizeBytes, fileCount int64
	var deletedAt *time.Time
	for iter.Scan(&orgID, &sizeBytes, &fileCount, &deletedAt) {
		if deletedAt != nil && !deletedAt.IsZero() {
			continue
		}
		if sizeBytes == 0 && fileCount == 0 {
			continue
		}
		shard := traffic.CounterShard(orgID)
		snap := expected[shard]
		snap.BytesUsed += sizeBytes
		snap.FileCount += fileCount
		expected[shard] = snap
	}
	if err := iter.Close(); err != nil {
		t.Fatalf("failed to scan libraries for expected sharded platform snapshots: %v", err)
	}

	return expected
}

func readStorageSnapshotByShardForTest(t *testing.T, session interface {
	Query(stmt string, values ...interface{}) *gocql.Query
}, scope string, shard int) traffic.StorageSnapshot {
	t.Helper()

	var bytesUsed, fileCount int64
	if err := session.Query(`
		SELECT bytes_used, file_count FROM storage_counters
		WHERE scope = ? AND shard = ? AND day = ?
	`, scope, shard, storageCounterTotalDayForTest).Scan(&bytesUsed, &fileCount); err != nil {
		if err == gocql.ErrNotFound {
			return traffic.StorageSnapshot{}
		}
		t.Fatalf("failed to read storage snapshot for scope %s shard %d: %v", scope, shard, err)
	}

	return traffic.StorageSnapshot{
		BytesUsed: max(bytesUsed, 0),
		FileCount: max(fileCount, 0),
	}
}

func assertPlatformStorageShardsForTest(t *testing.T, session interface {
	Query(stmt string, values ...interface{}) *gocql.Query
}, expected map[int]traffic.StorageSnapshot) {
	t.Helper()

	traffic.ForEachCounterShard(func(shard int) {
		if got := readStorageSnapshotByShardForTest(t, session, traffic.PlatformStorageScope(), shard); got != expected[shard] {
			t.Fatalf("platform shard %d snapshot = %+v, want %+v", shard, got, expected[shard])
		}
	})
}

func addStorageCounterDriftForTest(t *testing.T, session interface {
	Query(stmt string, values ...interface{}) *gocql.Query
}, scope string, deltaBytes, deltaFiles int64) {
	t.Helper()
	today := time.Now().UTC().Truncate(24 * time.Hour)
	shard := 0
	for _, day := range []time.Time{storageCounterTotalDayForTest, today} {
		if err := session.Query(`
			UPDATE storage_counters SET bytes_used = bytes_used + ?, file_count = file_count + ?
			WHERE scope = ? AND shard = ? AND day = ?
		`, deltaBytes, deltaFiles, scope, shard, day).Exec(); err != nil {
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

func adminOwnerLibraryProjectionRowForTest(t *testing.T, session *gocql.Session, orgID, ownerID, repoID string) (dbpkg.AdminLibraryProjectionRow, bool) {
	t.Helper()
	rows, err := dbpkg.ListAdminOwnerLibraryRows(session, orgID, ownerID)
	if err != nil {
		t.Fatalf("failed to list owner admin library projection rows for org %s owner %s: %v", orgID, ownerID, err)
	}
	for _, row := range rows {
		if row.LibraryID == repoID {
			return row, true
		}
	}
	return dbpkg.AdminLibraryProjectionRow{}, false
}

func globalAdminLibraryProjectionRowForTest(t *testing.T, session *gocql.Session, orgID, repoID string) (dbpkg.AdminLibraryProjectionRow, bool) {
	t.Helper()
	rows, err := dbpkg.ListAdminGlobalLibraryRows(session)
	if err != nil {
		t.Fatalf("failed to list global admin library projection rows: %v", err)
	}
	for _, row := range rows {
		if row.OrgID == orgID && row.LibraryID == repoID {
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

func insertSyntheticDirectoryTreeForTest(t *testing.T, session *gocql.Session, repoID, rootFSID string, childDirCount, filesPerChild int, fileSize int64) {
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
		childFileFSIDs := make([]string, 0, filesPerChild)
		for fileIndex := 0; fileIndex < filesPerChild; fileIndex++ {
			fileFSID := fmt.Sprintf("%s-file-%03d-%03d", rootFSID, dirIndex, fileIndex)
			childFileFSIDs = append(childFileFSIDs, fileFSID)
			childEntries = append(childEntries, syntheticDirEntry{
				ID:   fileFSID,
				Name: fmt.Sprintf("file-%03d.dat", fileIndex),
				Mode: 33188,
				Size: fileSize,
			})
		}
		insertSyntheticFileObjectsForTest(t, session, repoID, childFileFSIDs, fileSize)
		insertSyntheticDirObjectForTest(t, session, repoID, childFSID, childEntries)
	}

	insertSyntheticDirObjectForTest(t, session, repoID, rootFSID, rootEntries)
}

const syntheticFSObjectBatchSize = 128

func insertSyntheticFileObjectsForTest(t *testing.T, session *gocql.Session, repoID string, fsIDs []string, size int64) {
	t.Helper()
	if len(fsIDs) == 0 {
		return
	}

	for start := 0; start < len(fsIDs); start += syntheticFSObjectBatchSize {
		end := start + syntheticFSObjectBatchSize
		if end > len(fsIDs) {
			end = len(fsIDs)
		}

		mtime := time.Now().Unix()
		batch := session.Batch(gocql.UnloggedBatch)
		for _, fsID := range fsIDs[start:end] {
			batch.Query(`
				INSERT INTO fs_objects (library_id, fs_id, obj_type, obj_name, full_path, size_bytes, mtime, block_ids)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			`, repoID, fsID, "file", fsID, "/", size, mtime, []string{})
		}
		if err := session.ExecuteBatch(batch); err != nil {
			t.Fatalf("failed to batch insert synthetic file fs_objects for %s: %v", repoID, err)
		}
	}
}

func deleteSyntheticFSObjectsForTest(t *testing.T, session *gocql.Session, repoID string, fsIDs []string) {
	t.Helper()
	if len(fsIDs) == 0 {
		return
	}

	for start := 0; start < len(fsIDs); start += syntheticFSObjectBatchSize {
		end := start + syntheticFSObjectBatchSize
		if end > len(fsIDs) {
			end = len(fsIDs)
		}

		batch := session.Batch(gocql.UnloggedBatch)
		for _, fsID := range fsIDs[start:end] {
			batch.Query(`DELETE FROM fs_objects WHERE library_id = ? AND fs_id = ?`, repoID, fsID)
		}
		if err := session.ExecuteBatch(batch); err != nil {
			t.Errorf("cleanup synthetic fs_objects batch for %s failed: %v", repoID, err)
			return
		}
	}
}

func insertSyntheticDirObjectForTest(t *testing.T, session *gocql.Session, repoID, fsID string, entries []syntheticDirEntry) {
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

func insertSyntheticFileObjectForTest(t *testing.T, session *gocql.Session, repoID, fsID string, size int64) {
	t.Helper()
	if err := session.Query(`
		INSERT INTO fs_objects (library_id, fs_id, obj_type, obj_name, full_path, size_bytes, mtime, block_ids)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, repoID, fsID, "file", fsID, "/", size, time.Now().Unix(), []string{}).Exec(); err != nil {
		t.Fatalf("failed to insert synthetic file object %s for %s: %v", fsID, repoID, err)
	}
}
