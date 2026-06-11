//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	v2pkg "github.com/Sesame-Disk/sesamefs/internal/api/v2"
	"github.com/Sesame-Disk/sesamefs/internal/middleware"
)

// These tests fence the org-scoped library reads that were moved off the
// tombstone-heavy canonical `libraries` org-partition scan onto the
// libraries_by_owner / libraries_by_org_updated projections (audit item H):
//   - enforcement.CountActiveLibraries        -> libraries_by_org_updated
//   - PermissionMiddleware.GetUserLibraries   -> libraries_by_owner (owned discovery)
//   - LibraryHandler.ownerHasActiveLibraryNamed (duplicate-name guard) -> libraries_by_owner
//   - OrgAdminHandler.GetOrgUserOwnedRepos     -> libraries_by_owner
//
// The reads are only correct if the projections are complete across every
// creation path — including the group-owned-library path, which historically
// skipped the admin projections. These tests assert that a group-created library
// shows up in the projection-backed reads.

func TestLibrariesOrgReaders_ProjectionBackedReadsSeeRegularAndGroupLibraries(t *testing.T) {
	database := shareProjectionDBForTest(t)
	perm := middleware.NewPermissionMiddleware(database)

	adminUserID, ok := lookupUserIDByEmail(t, defaultAdminEmail)
	if !ok {
		t.Fatalf("expected user_id for %s", defaultAdminEmail)
	}

	baseline, err := v2pkg.CountActiveLibraries(database, defaultOrgID)
	if err != nil {
		t.Fatalf("baseline CountActiveLibraries failed: %v", err)
	}

	regularRepoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-orgreaders-regular-%d", time.Now().UnixNano()))
	groupRepoID := createGroupOwnedLibraryForOrgReadersTest(t, fmt.Sprintf("inttest-orgreaders-group-%d", time.Now().UnixNano()))

	// Both libraries must be discoverable as owned via libraries_by_owner.
	waitForIntegrationCondition(t, "GetUserLibraries sees regular + group-owned libraries via projection", func() bool {
		return userOwnsLibraryForTest(t, perm, defaultOrgID, adminUserID, regularRepoID) &&
			userOwnsLibraryForTest(t, perm, defaultOrgID, adminUserID, groupRepoID)
	})

	// And both must be counted as active via libraries_by_org_updated.
	waitForIntegrationCondition(t, "CountActiveLibraries reflects the two new libraries", func() bool {
		count, err := v2pkg.CountActiveLibraries(database, defaultOrgID)
		if err != nil {
			t.Fatalf("CountActiveLibraries failed: %v", err)
		}
		return count == baseline+2
	})
}

func TestLibrariesOrgReaders_SoftDeleteRemovedFromActiveCount(t *testing.T) {
	database := shareProjectionDBForTest(t)

	baseline, err := v2pkg.CountActiveLibraries(database, defaultOrgID)
	if err != nil {
		t.Fatalf("baseline CountActiveLibraries failed: %v", err)
	}

	repoID := createDisposableTestLibrary(t, adminClient, fmt.Sprintf("inttest-orgreaders-softdel-%d", time.Now().UnixNano()))

	waitForIntegrationCondition(t, "CountActiveLibraries includes the new library", func() bool {
		count, err := v2pkg.CountActiveLibraries(database, defaultOrgID)
		if err != nil {
			t.Fatalf("CountActiveLibraries failed: %v", err)
		}
		return count == baseline+1
	})

	resp := adminClient.Delete(t, fmt.Sprintf("/api/v2.1/repos/%s/", repoID))
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("soft-delete library %s failed: status=%d body=%s", repoID, resp.StatusCode, responseBody(t, resp))
	}
	resp.Body.Close()

	waitForIntegrationCondition(t, "soft-deleted library drops out of CountActiveLibraries", func() bool {
		count, err := v2pkg.CountActiveLibraries(database, defaultOrgID)
		if err != nil {
			t.Fatalf("CountActiveLibraries failed: %v", err)
		}
		return count == baseline
	})
}

func TestLibrariesOrgReaders_DuplicateNameRejectedViaProjection(t *testing.T) {
	name := fmt.Sprintf("inttest-orgreaders-dup-%d", time.Now().UnixNano())
	createTestLibrary(t, adminClient, name)

	// ownerHasActiveLibraryNamed reads libraries_by_owner; the first create wrote
	// the projection in the same batch, so the duplicate must be rejected.
	waitForIntegrationCondition(t, "duplicate library name rejected with 409 via projection", func() bool {
		resp := adminClient.PostJSON(t, "/api/v2.1/repos/", map[string]string{"repo_name": name})
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusConflict {
			return true
		}
		// A library created by the duplicate attempt must be cleaned up so retries
		// (and the suite) stay deterministic.
		if resp.StatusCode == http.StatusOK {
			if repoID := repoIDFromCreateResponse(t, resp); repoID != "" {
				adminClient.Delete(t, fmt.Sprintf("/api/v2.1/repos/%s/", repoID)).Body.Close()
			}
		}
		return false
	})
}

func TestLibrariesOrgReaders_OrgAdminOwnedReposIncludesLibrary(t *testing.T) {
	repoID := createTestLibrary(t, adminClient, fmt.Sprintf("inttest-orgreaders-orgadmin-%d", time.Now().UnixNano()))

	waitForIntegrationCondition(t, "org-admin owned-repos lists the library via libraries_by_owner", func() bool {
		resp := superadminClient.Get(t, fmt.Sprintf("/api/v2.1/org/%s/admin/users/%s/repos", defaultOrgID, defaultAdminEmail))
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return false
		}
		return responseListContainsRepoID(t, resp, repoID)
	})
}

// --- helpers ---

func createGroupOwnedLibraryForOrgReadersTest(t *testing.T, repoName string) string {
	t.Helper()

	groupName := fmt.Sprintf("inttest-orgreaders-grp-%d", time.Now().UnixNano())
	groupID := createGroupForRegressionTest(t, adminClient, groupName)

	resp := adminClient.PostJSON(t, fmt.Sprintf("/api/v2.1/groups/%s/group-owned-libraries/", groupID), map[string]string{"name": repoName})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create group-owned library %q failed: status=%d body=%s", repoName, resp.StatusCode, responseBody(t, resp))
	}
	repoID, _ := responseJSON(t, resp)["repo_id"].(string)
	if repoID == "" {
		t.Fatalf("group-owned library response missing repo_id")
	}
	t.Cleanup(func() {
		r := adminClient.Delete(t, fmt.Sprintf("/api/v2.1/repos/%s/", repoID))
		r.Body.Close()
	})
	return repoID
}

func userOwnsLibraryForTest(t *testing.T, perm *middleware.PermissionMiddleware, orgID, userID, repoID string) bool {
	t.Helper()

	libs, err := perm.GetUserLibraries(orgID, userID)
	if err != nil {
		t.Fatalf("GetUserLibraries failed: %v", err)
	}
	for _, lib := range libs {
		if lib.LibraryID.String() == repoID && lib.Permission == middleware.PermissionOwner {
			return true
		}
	}
	return false
}

func repoIDFromCreateResponse(t *testing.T, resp *http.Response) string {
	t.Helper()

	body := responseJSON(t, resp)
	if repoID, ok := body["repo_id"].(string); ok {
		return repoID
	}
	if repoID, ok := body["id"].(string); ok {
		return repoID
	}
	return ""
}

func responseListContainsRepoID(t *testing.T, resp *http.Response, repoID string) bool {
	t.Helper()

	list, _ := responseJSON(t, resp)["repo_list"].([]interface{})
	for _, item := range list {
		obj, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		if id, _ := obj["repo_id"].(string); id == repoID {
			return true
		}
	}
	return false
}
