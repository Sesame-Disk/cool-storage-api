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
	t.Helper()
	original := readLiveLibraryStateFn
	readLiveLibraryStateFn = func(_ *gocql.Session, _, _ string) (dbpkg.LibraryState, error) {
		return dbpkg.LibraryState{OwnerID: "someone-else", HeadCommitID: "head"}, nil
	}
	defer func() { readLiveLibraryStateFn = original }()
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
	// "rw" is the interesting case: it is real write access to CONTENT, and it must
	// still not carry authority over library configuration.
	for _, perm := range []middleware.LibraryPermission{
		middleware.PermissionRW,
		middleware.PermissionR,
		middleware.PermissionCloudEdit,
		middleware.PermissionPreview,
		middleware.PermissionNone,
		// PermissionAdmin is a distinct constant that GetLibraryPermission never
		// returns today (org admins resolve to PermissionOwner). Pinned anyway so a
		// future change that starts returning it cannot quietly widen the gate.
		middleware.PermissionAdmin,
	} {
		for _, m := range guardedLibraryMutations() {
			t.Run(m.name+"/"+string(perm), func(t *testing.T) {
				withLiveLibraryStateStub(t, func() {
					withLibraryPermissionStub(t, perm, nil, func() {
						w := runMutation(t, m, func(c *gin.Context) {
							c.Set("user_id", authTestUserID)
						})
						if w.Code != http.StatusForbidden {
							t.Fatalf("status = %d, want %d", w.Code, http.StatusForbidden)
						}
						assertJSONError(t, w.Body, "you do not have permission to modify this library")
					})
				})
			})
		}
	}
}

func TestLibraryMutations_AllowOwnerAndOrgAdmin(t *testing.T) {
	// GetLibraryPermission collapses library owner and org admin/superadmin onto
	// PermissionOwner, so one allow case covers both callers who legitimately
	// administer a library. Asserted at the gate rather than through the handler:
	// past the gate the handlers write to Cassandra, which these unit tests do not
	// have.
	for _, m := range guardedLibraryMutations() {
		t.Run(m.name, func(t *testing.T) {
			withLibraryPermissionStub(t, middleware.PermissionOwner, nil, func() {
				h := newLibraryHandlerForLiveFenceTests()
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Set("org_id", authTestOrgID)
				c.Set("user_id", authTestUserID)
				c.Params = gin.Params{{Key: "repo_id", Value: authTestRepoID}}

				if !h.requireLibraryConfigAuthority(c, m.name, authTestOrgID, authTestRepoID) {
					t.Fatal("requireLibraryConfigAuthority = false for PermissionOwner, want true")
				}
			})
		})
	}
}

func TestLibraryMutations_RejectMissingUserID(t *testing.T) {
	// authMiddleware always sets user_id; if a future route ever reaches these
	// handlers without one, fail closed rather than resolve permissions for "".
	for _, m := range guardedLibraryMutations() {
		t.Run(m.name, func(t *testing.T) {
			withLiveLibraryStateStub(t, func() {
				// Owner permission on purpose: the empty user_id must be refused
				// before the lookup can be asked anything.
				withLibraryPermissionStub(t, middleware.PermissionOwner, nil, func() {
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
				withLibraryPermissionStub(t, middleware.PermissionOwner, nil, func() {
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
				withLibraryPermissionStub(t, middleware.PermissionNone, errors.New("cassandra unavailable"), func() {
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

func TestLibraryMutations_RejectNonAdminAPIKeyScope(t *testing.T) {
	// An API key carries a scope ceiling independent of the user's own authority.
	// GetLibraryPermission reads the role and owner column from Cassandra and knows
	// nothing about the credential, so a read/read-write key minted by the library
	// OWNER still resolves to PermissionOwner — the stub below reproduces exactly
	// that. Only the admin scope may reach library configuration.
	for _, scope := range []string{"read", "read-write", "", "bogus"} {
		for _, m := range guardedLibraryMutations() {
			t.Run(m.name+"/"+scope, func(t *testing.T) {
				withLiveLibraryStateStub(t, func() {
					withLibraryPermissionStub(t, middleware.PermissionOwner, nil, func() {
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
			})
		}
	}
}

func TestLibraryMutations_AllowAdminAPIKeyScope(t *testing.T) {
	// The admin scope clears the ceiling; the permission lookup still decides.
	for _, m := range guardedLibraryMutations() {
		t.Run(m.name, func(t *testing.T) {
			withLibraryPermissionStub(t, middleware.PermissionOwner, nil, func() {
				h := newLibraryHandlerForLiveFenceTests()
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Set("org_id", authTestOrgID)
				c.Set("user_id", authTestUserID)
				c.Set("api_key_scope", "admin")
				c.Params = gin.Params{{Key: "repo_id", Value: authTestRepoID}}

				if !h.requireLibraryConfigAuthority(c, m.name, authTestOrgID, authTestRepoID) {
					t.Fatal("requireLibraryConfigAuthority = false for admin-scoped key with PermissionOwner, want true")
				}
			})
		})
	}
}

func TestLibraryMutations_AdminAPIKeyScopeDoesNotSubstituteForPermission(t *testing.T) {
	// The scope ceiling is a ceiling, not a grant: an admin-scoped key belonging to
	// a user with only rw on the library is still refused.
	for _, m := range guardedLibraryMutations() {
		t.Run(m.name, func(t *testing.T) {
			withLiveLibraryStateStub(t, func() {
				withLibraryPermissionStub(t, middleware.PermissionRW, nil, func() {
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
