package v2

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
)

// ISSUE-LIBRARY-MUTATION-NO-PERMISSION-CHECK-01 regression suite.
//
// UpdateLibrary, LibraryOperation(op=rename) and ChangeStorageClass shipped
// behind authMiddleware alone: they read org_id and repo_id, never user_id, and
// never consulted the caller's library permission. Any authenticated member of an
// organization could rename any library in it, rewrite its description, shorten
// its version_ttl_days retention, or move its storage-class preference.
//
// The routes are registered five times (with and without a trailing slash) under
// two prefixes, so these tests drive the HANDLERS — the gate has to live in the
// handler for every registration to inherit it.

const (
	authTestOrgID  = "00000000-0000-0000-0000-000000000001"
	authTestUserID = "00000000-0000-0000-0000-000000000002"
	authTestRepoID = "11111111-1111-1111-1111-111111111111"
)

// withLiveLibraryStateStub makes the liveness fence pass so the permission gate is
// the next thing the handler reaches. The stubbed owner is deliberately NOT the
// caller: nothing about authority may be inferred from this row.
//
// The permission seam itself is stubbed with withLibraryPermissionStub, shared
// with library_not_found_test.go.
func withLiveLibraryStateStub(t *testing.T, fn func()) {
	withLibraryOwnerStateStub(t, "someone-else", fn)
}

func withLibraryOwnerStateStub(t *testing.T, ownerID string, fn func()) {
	t.Helper()
	original := readLiveLibraryStateFn
	readLiveLibraryStateFn = func(_ *gocql.Session, _, _ string) (dbpkg.LibraryState, error) {
		return dbpkg.LibraryState{OwnerID: ownerID, HeadCommitID: "head"}, nil
	}
	defer func() { readLiveLibraryStateFn = original }()
	fn()
}

func withOrgRoleStub(t *testing.T, role middleware.OrganizationRole, stubErr error, fn func()) {
	t.Helper()
	original := getUserOrgRoleFn
	getUserOrgRoleFn = func(_ *middleware.PermissionMiddleware, _, _ string) (middleware.OrganizationRole, error) {
		return role, stubErr
	}
	defer func() { getUserOrgRoleFn = original }()
	fn()
}

// libraryMutation describes one guarded handler so every case runs against all three.
type libraryMutation struct {
	name   string
	method string
	route  string
	target string
	body   string
	invoke func(*LibraryHandler, *gin.Context)
}

func guardedLibraryMutations() []libraryMutation {
	return []libraryMutation{
		{
			name:   "UpdateLibrary",
			method: "PUT",
			route:  "/repos/:repo_id",
			target: "/repos/" + authTestRepoID,
			body:   `{"name":"Attacker Renamed","version_ttl_days":7}`,
			invoke: func(h *LibraryHandler, c *gin.Context) { h.UpdateLibrary(c) },
		},
		{
			name:   "RenameLibrary",
			method: "POST",
			route:  "/repos/:repo_id",
			target: "/repos/" + authTestRepoID + "?op=rename",
			body:   `{"repo_name":"Attacker Renamed"}`,
			// Driven through LibraryOperation, the way the route actually dispatches.
			invoke: func(h *LibraryHandler, c *gin.Context) { h.LibraryOperation(c) },
		},
		{
			name:   "ChangeStorageClass",
			method: "POST",
			route:  "/repos/:repo_id/storage-class",
			target: "/repos/" + authTestRepoID + "/storage-class",
			body:   `{"storage_class":"standard"}`,
			invoke: func(h *LibraryHandler, c *gin.Context) { h.ChangeStorageClass(c) },
		},
	}
}

// runMutation drives one handler with the given context setup and returns the recorder.
func runMutation(t *testing.T, m libraryMutation, setup func(*gin.Context)) *httptest.ResponseRecorder {
	t.Helper()
	r := gin.New()
	handler := newLibraryHandlerForLiveFenceTests()
	r.Handle(m.method, m.route, func(c *gin.Context) {
		c.Set("org_id", authTestOrgID)
		if setup != nil {
			setup(c)
		}
		m.invoke(handler, c)
	})

	req := httptest.NewRequest(m.method, m.target, strings.NewReader(m.body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestLibraryMutations_RejectNonOwnerWithWriteAccess(t *testing.T) {
	// Shares are content authority only. Even a share labelled "admin" cannot
	// substitute for canonical ownership or an organization administrator role.
	for _, m := range guardedLibraryMutations() {
		t.Run(m.name, func(t *testing.T) {
			withLiveLibraryStateStub(t, func() {
				withOrgRoleStub(t, middleware.RoleUser, nil, func() {
					w := runMutation(t, m, func(c *gin.Context) { c.Set("user_id", authTestUserID) })
					if w.Code != http.StatusForbidden {
						t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
					}
					assertJSONError(t, w.Body, "you do not have permission to modify this library")
				})
			})
		})
	}
}

func TestLibraryMutations_AllowOwnerAndOrgAdmin(t *testing.T) {
	tests := []struct {
		name    string
		ownerID string
		role    middleware.OrganizationRole
		scope   string
	}{
		{name: "canonical owner session", ownerID: authTestUserID, role: middleware.RoleUser},
		{name: "canonical owner read-write key", ownerID: authTestUserID, role: middleware.RoleUser, scope: "read-write"},
		{name: "org admin session", ownerID: "someone-else", role: middleware.RoleAdmin},
		{name: "org admin admin key", ownerID: "someone-else", role: middleware.RoleAdmin, scope: "admin"},
	}
	for _, tc := range tests {
		for _, m := range guardedLibraryMutations() {
			t.Run(tc.name+"/"+m.name, func(t *testing.T) {
				withOrgRoleStub(t, tc.role, nil, func() {
					h := newLibraryHandlerForLiveFenceTests()
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Set("org_id", authTestOrgID)
					c.Set("user_id", authTestUserID)
					if tc.scope != "" {
						c.Set("api_key_scope", tc.scope)
					}
					c.Params = gin.Params{{Key: "repo_id", Value: authTestRepoID}}

					if !h.requireLibraryConfigAuthority(c, m.name, authTestOrgID, authTestRepoID, tc.ownerID) {
						t.Fatal("requireLibraryConfigAuthority = false, want true")
					}
				})
			})
		}
	}
}

func TestLibraryMutations_RejectMissingUserID(t *testing.T) {
	// authMiddleware always sets user_id; if a future route ever reaches these
	// handlers without one, fail closed rather than resolve permissions for "".
	for _, m := range guardedLibraryMutations() {
		t.Run(m.name, func(t *testing.T) {
			withLiveLibraryStateStub(t, func() {
				withOrgRoleStub(t, middleware.RoleAdmin, nil, func() {
					w := runMutation(t, m, nil)
					if w.Code != http.StatusForbidden {
						t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
					}
				})
			})
		})
	}
}

func TestLibraryMutations_RejectRepoAPIToken(t *testing.T) {
	// A repo API token is a content credential scoped to one library and carries at
	// most "rw". Library configuration is outside its scope.
	for _, m := range guardedLibraryMutations() {
		t.Run(m.name, func(t *testing.T) {
			withLiveLibraryStateStub(t, func() {
				withOrgRoleStub(t, middleware.RoleAdmin, nil, func() {
					w := runMutation(t, m, func(c *gin.Context) {
						c.Set("user_id", authTestUserID)
						c.Set("repo_api_token", true)
						c.Set("repo_api_token_repo_id", authTestRepoID)
						c.Set("repo_api_token_permission", "rw")
					})
					if w.Code != http.StatusForbidden {
						t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
					}
				})
			})
		})
	}
}

func TestLibraryMutations_PermissionLookupErrorFailsClosed(t *testing.T) {
	for _, m := range guardedLibraryMutations() {
		t.Run(m.name, func(t *testing.T) {
			withLiveLibraryStateStub(t, func() {
				withOrgRoleStub(t, middleware.RoleGuest, errors.New("cassandra unavailable"), func() {
					w := runMutation(t, m, func(c *gin.Context) {
						c.Set("user_id", authTestUserID)
					})
					if w.Code != http.StatusInternalServerError {
						t.Fatalf("status = %d, want %d", w.Code, http.StatusInternalServerError)
					}
					assertJSONError(t, w.Body, "failed to check permissions")
				})
			})
		})
	}
}

func TestLibraryMutations_OwnerAPIKeyScope(t *testing.T) {
	for _, scope := range []string{"read", "", "bogus"} {
		for _, m := range guardedLibraryMutations() {
			t.Run(m.name+"/"+scope, func(t *testing.T) {
				withLibraryOwnerStateStub(t, authTestUserID, func() {
					w := runMutation(t, m, func(c *gin.Context) {
						c.Set("user_id", authTestUserID)
						c.Set("api_key_scope", scope)
					})
					if w.Code != http.StatusForbidden {
						t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
					}
					assertJSONError(t, w.Body, "you do not have permission to modify this library")
				})
			})
		}
	}
}

func TestLibraryMutations_OrgAdminRequiresAdminAPIKeyScope(t *testing.T) {
	for _, m := range guardedLibraryMutations() {
		t.Run(m.name, func(t *testing.T) {
			withLiveLibraryStateStub(t, func() {
				withOrgRoleStub(t, middleware.RoleAdmin, nil, func() {
					w := runMutation(t, m, func(c *gin.Context) {
						c.Set("user_id", authTestUserID)
						c.Set("api_key_scope", "read-write")
					})
					if w.Code != http.StatusForbidden {
						t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
					}
				})
			})
		})
	}
}

func TestLibraryMutations_AdminAPIKeyScopeDoesNotSubstituteForPermission(t *testing.T) {
	// The scope ceiling is a ceiling, not a grant: an admin-scoped key belonging to
	// a regular user with only content access is still refused.
	for _, m := range guardedLibraryMutations() {
		t.Run(m.name, func(t *testing.T) {
			withLiveLibraryStateStub(t, func() {
				withOrgRoleStub(t, middleware.RoleUser, nil, func() {
					w := runMutation(t, m, func(c *gin.Context) {
						c.Set("user_id", authTestUserID)
						c.Set("api_key_scope", "admin")
					})
					if w.Code != http.StatusForbidden {
						t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
					}
				})
			})
		})
	}
}

func TestDeleteLibrary_CredentialScopeMatrix(t *testing.T) {
	tests := []struct {
		name       string
		scope      string
		repoToken  bool
		ownerID    string
		wantStatus int
		wantDelete bool
	}{
		{name: "owner session", ownerID: authTestUserID, wantStatus: http.StatusOK, wantDelete: true},
		{name: "owner read-write key", scope: "read-write", ownerID: authTestUserID, wantStatus: http.StatusOK, wantDelete: true},
		{name: "owner admin key", scope: "admin", ownerID: authTestUserID, wantStatus: http.StatusOK, wantDelete: true},
		{name: "owner read key", scope: "read", ownerID: authTestUserID, wantStatus: http.StatusForbidden},
		{name: "owner repo token", repoToken: true, ownerID: authTestUserID, wantStatus: http.StatusForbidden},
		{name: "non-owner admin key", scope: "admin", ownerID: "someone-else", wantStatus: http.StatusForbidden},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			withLibraryOwnerStateStub(t, tc.ownerID, func() {
				original := softDeleteLibraryFn
				deleted := false
				softDeleteLibraryFn = func(_ interface{ Session() *gocql.Session }, _, _, _, _ string) error {
					deleted = true
					return nil
				}
				defer func() { softDeleteLibraryFn = original }()

				r := gin.New()
				h := newLibraryHandlerForLiveFenceTests()
				r.DELETE("/repos/:repo_id", func(c *gin.Context) {
					c.Set("org_id", authTestOrgID)
					c.Set("user_id", authTestUserID)
					if tc.scope != "" {
						c.Set("api_key_scope", tc.scope)
					}
					if tc.repoToken {
						c.Set("repo_api_token", true)
					}
					h.DeleteLibrary(c)
				})

				w := httptest.NewRecorder()
				r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/repos/"+authTestRepoID, nil))
				if w.Code != tc.wantStatus {
					t.Fatalf("status = %d, want %d", w.Code, tc.wantStatus)
				}
				if deleted != tc.wantDelete {
					t.Fatalf("soft delete called = %v, want %v", deleted, tc.wantDelete)
				}
			})
		})
	}
}
