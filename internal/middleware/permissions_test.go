package middleware

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	dbpkg "github.com/Sesame-Disk/sesamefs/internal/db"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type scopeOnlyContext struct {
	values map[any]any
}

func (c scopeOnlyContext) Get(key any) (any, bool) {
	v, ok := c.values[key]
	return v, ok
}

func (c scopeOnlyContext) GetString(key any) string {
	v, ok := c.values[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

func init() {
	gin.SetMode(gin.TestMode)
}

// setupTestDB returns nil; these tests don't need a DB for hierarchy logic.
func setupTestDB(t *testing.T) *PermissionMiddleware {
	return NewPermissionMiddleware(nil)
}

// --- RequireAuth middleware tests ---

func TestRequireAuth_RejectsEmptyContext(t *testing.T) {
	pm := NewPermissionMiddleware(nil)
	mw := pm.RequireAuth()

	r := gin.New()
	r.Use(mw)
	r.GET("/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
	var body map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["error"] != "authentication required" {
		t.Errorf("unexpected error: %v", body["error"])
	}
}

func TestAPIKeyScopeCeiling(t *testing.T) {
	ctx := scopeOnlyContext{values: map[any]any{"api_key_scope": "read"}}
	if !apiKeyScopeAllowsLibraryPermission(ctx, PermissionR) {
		t.Fatal("read scope should allow read permission")
	}
	if apiKeyScopeAllowsLibraryPermission(ctx, PermissionRW) {
		t.Fatal("read scope should not allow rw permission")
	}

	ctx = scopeOnlyContext{values: map[any]any{"api_key_scope": "read-write"}}
	if !apiKeyScopeAllowsLibraryPermission(ctx, PermissionRW) {
		t.Fatal("read-write scope should allow rw permission")
	}
	if apiKeyScopeAllowsLibraryPermission(ctx, PermissionAdmin) {
		t.Fatal("read-write scope should not allow admin permission")
	}
}

func TestRequireAuth_RejectsMissingOrgID(t *testing.T) {
	pm := NewPermissionMiddleware(nil)
	mw := pm.RequireAuth()

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("user_id", "user-1")
		// org_id intentionally missing
		c.Next()
	})
	r.Use(mw)
	r.GET("/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestRequireAuth_RejectsMissingUserID(t *testing.T) {
	pm := NewPermissionMiddleware(nil)
	mw := pm.RequireAuth()

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("org_id", "org-1")
		// user_id intentionally missing
		c.Next()
	})
	r.Use(mw)
	r.GET("/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestRequireAuth_AllowsAuthenticatedUser(t *testing.T) {
	pm := NewPermissionMiddleware(nil)
	mw := pm.RequireAuth()

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("user_id", "user-1")
		c.Set("org_id", "org-1")
		c.Next()
	})
	r.Use(mw)
	r.GET("/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

// --- RequireSuperAdmin middleware tests ---

func TestRequireSuperAdmin_RejectsNonPlatformOrg(t *testing.T) {
	pm := NewPermissionMiddleware(nil)
	mw := pm.RequireSuperAdmin()

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("user_id", "user-1")
		c.Set("org_id", "00000000-0000-0000-0000-000000000001") // non-platform
		c.Next()
	})
	r.Use(mw)
	r.GET("/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

func TestRequireSuperAdmin_RejectsEmptyAuth(t *testing.T) {
	pm := NewPermissionMiddleware(nil)
	mw := pm.RequireSuperAdmin()

	r := gin.New()
	r.Use(mw)
	r.GET("/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestIsPlatformSuperAdmin(t *testing.T) {
	if !IsPlatformSuperAdmin(PlatformOrgID, RoleSuperAdmin) {
		t.Fatal("expected platform superadmin to be recognized")
	}
	if IsPlatformSuperAdmin("00000000-0000-0000-0000-000000000001", RoleSuperAdmin) {
		t.Fatal("tenant superadmin role should not be treated as platform superadmin")
	}
	if IsPlatformSuperAdmin(PlatformOrgID, RoleAdmin) {
		t.Fatal("platform admin role should not be treated as platform superadmin")
	}
}

// --- RequireOrgRole middleware tests ---

func TestRequireOrgRole_RejectsEmptyAuth(t *testing.T) {
	pm := NewPermissionMiddleware(nil)
	mw := pm.RequireOrgRole(RoleUser)

	r := gin.New()
	r.Use(mw)
	r.GET("/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestRequireOrgRole_RejectsMissingUserID(t *testing.T) {
	pm := NewPermissionMiddleware(nil)
	mw := pm.RequireOrgRole(RoleAdmin)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("org_id", "org-1")
		c.Next()
	})
	r.Use(mw)
	r.GET("/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

// --- Permission hierarchy tests (existing, but calling real methods) ---

func TestHasLibraryAccess_Owner(t *testing.T) {
	pm := setupTestDB(t)

	if !pm.hasRequiredLibraryPermission(PermissionOwner, PermissionRW) {
		t.Error("Owner should have RW permission")
	}
	if !pm.hasRequiredLibraryPermission(PermissionOwner, PermissionR) {
		t.Error("Owner should have R permission")
	}
	if !pm.hasRequiredLibraryPermission(PermissionRW, PermissionR) {
		t.Error("RW permission should satisfy R requirement")
	}
	if pm.hasRequiredLibraryPermission(PermissionR, PermissionRW) {
		t.Error("R permission should not satisfy RW requirement")
	}
	if pm.hasRequiredLibraryPermission(PermissionNone, PermissionR) {
		t.Error("None permission should not satisfy R requirement")
	}
}

func TestGetLibraryPermissionWithFlags_DeletedLibraryReturnsNone(t *testing.T) {
	original := readLiveLibraryStateFn
	readLiveLibraryStateFn = func(_ *gocql.Session, _, _ string) (dbpkg.LibraryState, error) {
		return dbpkg.LibraryState{}, dbpkg.ErrLibraryDeleted
	}
	defer func() {
		readLiveLibraryStateFn = original
	}()

	pm := NewPermissionMiddleware(&dbpkg.DB{})
	perm, flags, err := pm.GetLibraryPermissionWithFlags("org-1", "user-1", "repo-1")
	if err != nil {
		t.Fatalf("GetLibraryPermissionWithFlags() error = %v", err)
	}
	if perm != PermissionNone {
		t.Fatalf("permission = %q, want %q", perm, PermissionNone)
	}
	if flags == nil {
		t.Fatal("flags should not be nil")
	}
	if flags.Upload || flags.Download || flags.Create || flags.Modify || flags.Copy || flags.Delete || flags.Preview || flags.DownloadExternalLink {
		t.Fatal("deleted libraries should not expose any permission flags")
	}
}

func TestHasRequiredOrgRole(t *testing.T) {
	pm := setupTestDB(t)

	if !pm.hasRequiredOrgRole(RoleSuperAdmin, RoleAdmin) {
		t.Error("Superadmin should have admin privileges")
	}
	if !pm.hasRequiredOrgRole(RoleSuperAdmin, RoleUser) {
		t.Error("Superadmin should have user privileges")
	}
	if !pm.hasRequiredOrgRole(RoleAdmin, RoleUser) {
		t.Error("Admin should have user privileges")
	}
	if pm.hasRequiredOrgRole(RoleAdmin, RoleSuperAdmin) {
		t.Error("Admin should not have superadmin privileges")
	}
	if !pm.hasRequiredOrgRole(RoleUser, RoleReadOnly) {
		t.Error("User should have readonly privileges")
	}
	if !pm.hasRequiredOrgRole(RoleReadOnly, RoleGuest) {
		t.Error("Readonly should have guest privileges")
	}
	if pm.hasRequiredOrgRole(RoleGuest, RoleUser) {
		t.Error("Guest should not have user privileges")
	}
	if pm.hasRequiredOrgRole(RoleReadOnly, RoleUser) {
		t.Error("Readonly should not have user privileges")
	}
}

func TestLibraryPermissionHierarchy(t *testing.T) {
	tests := []struct {
		name           string
		userPermission LibraryPermission
		requiredPerm   LibraryPermission
		expectedResult bool
	}{
		{"owner satisfies rw", PermissionOwner, PermissionRW, true},
		{"owner satisfies r", PermissionOwner, PermissionR, true},
		{"rw satisfies r", PermissionRW, PermissionR, true},
		{"rw satisfies rw", PermissionRW, PermissionRW, true},
		{"r does not satisfy rw", PermissionR, PermissionRW, false},
		{"r satisfies r", PermissionR, PermissionR, true},
		{"none does not satisfy r", PermissionNone, PermissionR, false},
		{"none does not satisfy rw", PermissionNone, PermissionRW, false},
		{"none does not satisfy owner", PermissionNone, PermissionOwner, false},
	}

	pm := setupTestDB(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := pm.hasRequiredLibraryPermission(tt.userPermission, tt.requiredPerm)
			if result != tt.expectedResult {
				t.Errorf("hasRequiredLibraryPermission(%s, %s) = %v, want %v",
					tt.userPermission, tt.requiredPerm, result, tt.expectedResult)
			}
		})
	}
}

func TestOrgRoleHierarchy(t *testing.T) {
	tests := []struct {
		name           string
		userRole       OrganizationRole
		requiredRole   OrganizationRole
		expectedResult bool
	}{
		{"superadmin satisfies superadmin", RoleSuperAdmin, RoleSuperAdmin, true},
		{"superadmin satisfies owner", RoleSuperAdmin, RoleOwner, true},
		{"superadmin satisfies admin", RoleSuperAdmin, RoleAdmin, true},
		{"superadmin satisfies user", RoleSuperAdmin, RoleUser, true},
		{"superadmin satisfies readonly", RoleSuperAdmin, RoleReadOnly, true},
		{"superadmin satisfies guest", RoleSuperAdmin, RoleGuest, true},
		{"owner satisfies owner", RoleOwner, RoleOwner, true},
		{"owner satisfies admin", RoleOwner, RoleAdmin, true},
		{"owner satisfies user", RoleOwner, RoleUser, true},
		{"owner satisfies readonly", RoleOwner, RoleReadOnly, true},
		{"owner satisfies guest", RoleOwner, RoleGuest, true},
		{"owner does not satisfy superadmin", RoleOwner, RoleSuperAdmin, false},
		{"admin does not satisfy superadmin", RoleAdmin, RoleSuperAdmin, false},
		{"admin does not satisfy owner", RoleAdmin, RoleOwner, false},
		{"admin satisfies admin", RoleAdmin, RoleAdmin, true},
		{"admin satisfies user", RoleAdmin, RoleUser, true},
		{"admin satisfies readonly", RoleAdmin, RoleReadOnly, true},
		{"admin satisfies guest", RoleAdmin, RoleGuest, true},
		{"user satisfies user", RoleUser, RoleUser, true},
		{"user satisfies readonly", RoleUser, RoleReadOnly, true},
		{"user satisfies guest", RoleUser, RoleGuest, true},
		{"user does not satisfy admin", RoleUser, RoleAdmin, false},
		{"user does not satisfy owner", RoleUser, RoleOwner, false},
		{"user does not satisfy superadmin", RoleUser, RoleSuperAdmin, false},
		{"readonly satisfies readonly", RoleReadOnly, RoleReadOnly, true},
		{"readonly satisfies guest", RoleReadOnly, RoleGuest, true},
		{"readonly does not satisfy user", RoleReadOnly, RoleUser, false},
		{"guest satisfies guest", RoleGuest, RoleGuest, true},
		{"guest does not satisfy readonly", RoleGuest, RoleReadOnly, false},
		{"guest does not satisfy user", RoleGuest, RoleUser, false},
		{"guest does not satisfy admin", RoleGuest, RoleAdmin, false},
		{"guest does not satisfy owner", RoleGuest, RoleOwner, false},
		{"guest does not satisfy superadmin", RoleGuest, RoleSuperAdmin, false},
	}

	pm := setupTestDB(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := pm.hasRequiredOrgRole(tt.userRole, tt.requiredRole)
			if result != tt.expectedResult {
				t.Errorf("hasRequiredOrgRole(%s, %s) = %v, want %v",
					tt.userRole, tt.requiredRole, result, tt.expectedResult)
			}
		})
	}
}

func TestLibraryWithPermissionStruct(t *testing.T) {
	libID := uuid.New()
	lwp := LibraryWithPermission{
		LibraryID:  libID,
		Permission: PermissionRW,
	}

	if lwp.LibraryID != libID {
		t.Error("LibraryID not set correctly")
	}
	if lwp.Permission != PermissionRW {
		t.Error("Permission not set correctly")
	}
}

func TestMergeLibraryPermissionPrefersHigherAccess(t *testing.T) {
	pm := setupTestDB(t)
	librariesMap := make(map[uuid.UUID]LibraryPermission)
	libraryID := uuid.New()

	mergeLibraryPermission(librariesMap, libraryID, PermissionR, pm)
	mergeLibraryPermission(librariesMap, libraryID, PermissionRW, pm)

	if got := librariesMap[libraryID]; got != PermissionRW {
		t.Fatalf("merged permission = %s, want %s", got, PermissionRW)
	}
}

func TestMergeLibraryPermissionKeepsExistingHigherAccess(t *testing.T) {
	pm := setupTestDB(t)
	librariesMap := make(map[uuid.UUID]LibraryPermission)
	libraryID := uuid.New()

	mergeLibraryPermission(librariesMap, libraryID, PermissionOwner, pm)
	mergeLibraryPermission(librariesMap, libraryID, PermissionR, pm)

	if got := librariesMap[libraryID]; got != PermissionOwner {
		t.Fatalf("merged permission = %s, want %s", got, PermissionOwner)
	}
}

func TestPlatformOrgID(t *testing.T) {
	expected := "00000000-0000-0000-0000-000000000000"
	if PlatformOrgID != expected {
		t.Errorf("PlatformOrgID = %q, want %q", PlatformOrgID, expected)
	}
	defaultOrgID := "00000000-0000-0000-0000-000000000001"
	if PlatformOrgID == defaultOrgID {
		t.Error("PlatformOrgID must be different from default org ID")
	}
}

// TestHasRequiredOrgRolePublic tests the exported HasRequiredOrgRole function
func TestHasRequiredOrgRolePublic(t *testing.T) {
	tests := []struct {
		name         string
		userRole     OrganizationRole
		requiredRole OrganizationRole
		expected     bool
	}{
		{"superadmin satisfies superadmin", RoleSuperAdmin, RoleSuperAdmin, true},
		{"superadmin satisfies owner", RoleSuperAdmin, RoleOwner, true},
		{"superadmin satisfies admin", RoleSuperAdmin, RoleAdmin, true},
		{"superadmin satisfies user", RoleSuperAdmin, RoleUser, true},
		{"superadmin satisfies readonly", RoleSuperAdmin, RoleReadOnly, true},
		{"superadmin satisfies guest", RoleSuperAdmin, RoleGuest, true},
		{"owner satisfies owner", RoleOwner, RoleOwner, true},
		{"owner satisfies admin", RoleOwner, RoleAdmin, true},
		{"owner satisfies user", RoleOwner, RoleUser, true},
		{"owner does not satisfy superadmin", RoleOwner, RoleSuperAdmin, false},
		{"admin does not satisfy superadmin", RoleAdmin, RoleSuperAdmin, false},
		{"admin does not satisfy owner", RoleAdmin, RoleOwner, false},
		{"admin satisfies admin", RoleAdmin, RoleAdmin, true},
		{"admin satisfies user", RoleAdmin, RoleUser, true},
		{"user satisfies user", RoleUser, RoleUser, true},
		{"user does not satisfy admin", RoleUser, RoleAdmin, false},
		{"user does not satisfy owner", RoleUser, RoleOwner, false},
		{"readonly does not satisfy user", RoleReadOnly, RoleUser, false},
		{"readonly satisfies readonly", RoleReadOnly, RoleReadOnly, true},
		{"guest satisfies guest", RoleGuest, RoleGuest, true},
		{"guest does not satisfy readonly", RoleGuest, RoleReadOnly, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := HasRequiredOrgRole(tt.userRole, tt.requiredRole)
			if result != tt.expected {
				t.Errorf("HasRequiredOrgRole(%s, %s) = %v, want %v",
					tt.userRole, tt.requiredRole, result, tt.expected)
			}
		})
	}
}

func TestSuperAdminConstant(t *testing.T) {
	if RoleSuperAdmin != "superadmin" {
		t.Errorf("RoleSuperAdmin = %q, want %q", RoleSuperAdmin, "superadmin")
	}
}

// --- isNotFound tests ---

func TestIsNotFound(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, false},
		{"not found error", fmt.Errorf("not found"), true},
		{"wrapped not found", fmt.Errorf("gocql: not found"), true},
		{"unrelated error", fmt.Errorf("connection refused"), false},
		{"empty error", fmt.Errorf(""), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isNotFound(tt.err)
			if result != tt.expected {
				t.Errorf("isNotFound(%v) = %v, want %v", tt.err, result, tt.expected)
			}
		})
	}
}

// --- Group role hierarchy tests ---

func TestGroupRoleHierarchy(t *testing.T) {
	tests := []struct {
		name     string
		userRole GroupRole
		required GroupRole
		expected bool
	}{
		{"owner satisfies owner", GroupRoleOwner, GroupRoleOwner, true},
		{"owner satisfies admin", GroupRoleOwner, GroupRoleAdmin, true},
		{"owner satisfies member", GroupRoleOwner, GroupRoleMember, true},
		{"admin satisfies admin", GroupRoleAdmin, GroupRoleAdmin, true},
		{"admin satisfies member", GroupRoleAdmin, GroupRoleMember, true},
		{"admin does not satisfy owner", GroupRoleAdmin, GroupRoleOwner, false},
		{"member satisfies member", GroupRoleMember, GroupRoleMember, true},
		{"member does not satisfy admin", GroupRoleMember, GroupRoleAdmin, false},
		{"member does not satisfy owner", GroupRoleMember, GroupRoleOwner, false},
	}

	pm := setupTestDB(t)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := pm.hasRequiredGroupRole(tt.userRole, tt.required)
			if result != tt.expected {
				t.Errorf("hasRequiredGroupRole(%s, %s) = %v, want %v",
					tt.userRole, tt.required, result, tt.expected)
			}
		})
	}
}

// --- Group role constants test ---

func TestGroupRoleConstants(t *testing.T) {
	if GroupRoleOwner != "owner" {
		t.Errorf("GroupRoleOwner = %q, want %q", GroupRoleOwner, "owner")
	}
	if GroupRoleAdmin != "admin" {
		t.Errorf("GroupRoleAdmin = %q, want %q", GroupRoleAdmin, "admin")
	}
	if GroupRoleMember != "member" {
		t.Errorf("GroupRoleMember = %q, want %q", GroupRoleMember, "member")
	}
}

// --- Org role constants test ---

func TestOrgRoleConstants(t *testing.T) {
	if RoleAdmin != "admin" {
		t.Errorf("RoleAdmin = %q, want %q", RoleAdmin, "admin")
	}
	if RoleOwner != "owner" {
		t.Errorf("RoleOwner = %q, want %q", RoleOwner, "owner")
	}
	if RoleUser != "user" {
		t.Errorf("RoleUser = %q, want %q", RoleUser, "user")
	}
	if RoleReadOnly != "readonly" {
		t.Errorf("RoleReadOnly = %q, want %q", RoleReadOnly, "readonly")
	}
	if RoleGuest != "guest" {
		t.Errorf("RoleGuest = %q, want %q", RoleGuest, "guest")
	}
}

// TestIsOrgStaff tests the exported IsOrgStaff helper
func TestIsOrgStaff(t *testing.T) {
	tests := []struct {
		role     string
		expected bool
	}{
		{"superadmin", true},
		{"owner", true},
		{"admin", true},
		{"user", false},
		{"readonly", false},
		{"guest", false},
		{"", false},
		{"unknown", false},
	}

	for _, tt := range tests {
		t.Run("role="+tt.role, func(t *testing.T) {
			result := IsOrgStaff(tt.role)
			if result != tt.expected {
				t.Errorf("IsOrgStaff(%q) = %v, want %v", tt.role, result, tt.expected)
			}
		})
	}
}

// --- RequireLibraryPermission tests with repo API token ---

func TestRequireLibraryPermission_MissingRepoID(t *testing.T) {
	pm := NewPermissionMiddleware(nil)
	mw := pm.RequireLibraryPermission("repo_id", PermissionR)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("user_id", "user-1")
		c.Set("org_id", "org-1")
		c.Next()
	})
	r.Use(mw)
	r.GET("/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestRequireLibraryPermission_RepoApiToken_Matching(t *testing.T) {
	original := readLiveLibraryStateFn
	readLiveLibraryStateFn = func(_ *gocql.Session, _, _ string) (dbpkg.LibraryState, error) {
		return dbpkg.LibraryState{OrgID: "org-1", LibraryID: "test-repo"}, nil
	}
	defer func() {
		readLiveLibraryStateFn = original
	}()

	pm := NewPermissionMiddleware(&dbpkg.DB{})
	mw := pm.RequireLibraryPermission("repo_id", PermissionR)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("user_id", "user-1")
		c.Set("org_id", "org-1")
		c.Set("repo_api_token", true)
		c.Set("repo_api_token_repo_id", "test-repo")
		c.Set("repo_api_token_permission", "rw")
		c.Next()
	})
	r.Use(mw)
	r.GET("/lib/:repo_id/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/lib/test-repo/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d, body: %s", w.Code, w.Body.String())
	}
}

func TestRequireLibraryPermission_RepoApiToken_WrongRepo(t *testing.T) {
	original := readLiveLibraryStateFn
	readLiveLibraryStateFn = func(_ *gocql.Session, _, _ string) (dbpkg.LibraryState, error) {
		return dbpkg.LibraryState{OrgID: "org-1", LibraryID: "test-repo"}, nil
	}
	defer func() {
		readLiveLibraryStateFn = original
	}()

	pm := NewPermissionMiddleware(&dbpkg.DB{})
	mw := pm.RequireLibraryPermission("repo_id", PermissionR)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("user_id", "user-1")
		c.Set("org_id", "org-1")
		c.Set("repo_api_token", true)
		c.Set("repo_api_token_repo_id", "other-repo")
		c.Set("repo_api_token_permission", "rw")
		c.Next()
	})
	r.Use(mw)
	r.GET("/lib/:repo_id/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/lib/test-repo/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

func TestRequireLibraryPermission_RepoApiToken_InsufficientPerm(t *testing.T) {
	original := readLiveLibraryStateFn
	readLiveLibraryStateFn = func(_ *gocql.Session, _, _ string) (dbpkg.LibraryState, error) {
		return dbpkg.LibraryState{OrgID: "org-1", LibraryID: "test-repo"}, nil
	}
	defer func() {
		readLiveLibraryStateFn = original
	}()

	pm := NewPermissionMiddleware(&dbpkg.DB{})
	mw := pm.RequireLibraryPermission("repo_id", PermissionRW)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("user_id", "user-1")
		c.Set("org_id", "org-1")
		c.Set("repo_api_token", true)
		c.Set("repo_api_token_repo_id", "test-repo")
		c.Set("repo_api_token_permission", "r") // only read, requesting rw
		c.Next()
	})
	r.Use(mw)
	r.GET("/lib/:repo_id/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/lib/test-repo/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

// --- RequireLibraryOwner tests ---

func TestRequireLibraryOwner_MissingRepoID(t *testing.T) {
	pm := NewPermissionMiddleware(nil)
	mw := pm.RequireLibraryOwner("repo_id")

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("user_id", "user-1")
		c.Set("org_id", "org-1")
		c.Next()
	})
	r.Use(mw)
	r.GET("/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// --- RequireGroupRole tests ---

func TestRequireGroupRole_MissingGroupID(t *testing.T) {
	pm := NewPermissionMiddleware(nil)
	mw := pm.RequireGroupRole("group_id", GroupRoleMember)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("user_id", "user-1")
		c.Next()
	})
	r.Use(mw)
	r.GET("/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// --- HasLibraryAccessCtx tests (repo API token path) ---

func TestHasLibraryAccessCtx_RepoApiToken_Matching(t *testing.T) {
	original := readLiveLibraryStateFn
	readLiveLibraryStateFn = func(_ *gocql.Session, _, _ string) (dbpkg.LibraryState, error) {
		return dbpkg.LibraryState{OrgID: "org-1", LibraryID: "repo-1"}, nil
	}
	defer func() {
		readLiveLibraryStateFn = original
	}()

	pm := NewPermissionMiddleware(&dbpkg.DB{})

	r := gin.New()
	var hasAccess bool
	r.GET("/test", func(c *gin.Context) {
		c.Set("repo_api_token", true)
		c.Set("repo_api_token_repo_id", "repo-1")
		c.Set("repo_api_token_permission", "rw")

		result, err := pm.HasLibraryAccessCtx(c, "org-1", "user-1", "repo-1", PermissionR)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		hasAccess = result
		c.Status(200)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if !hasAccess {
		t.Error("expected access to be granted for matching repo token")
	}
}

func TestHasLibraryAccessCtx_RepoApiToken_WrongRepo(t *testing.T) {
	original := readLiveLibraryStateFn
	readLiveLibraryStateFn = func(_ *gocql.Session, _, _ string) (dbpkg.LibraryState, error) {
		return dbpkg.LibraryState{OrgID: "org-1", LibraryID: "repo-2"}, nil
	}
	defer func() {
		readLiveLibraryStateFn = original
	}()

	pm := NewPermissionMiddleware(&dbpkg.DB{})

	r := gin.New()
	var hasAccess bool
	r.GET("/test", func(c *gin.Context) {
		c.Set("repo_api_token", true)
		c.Set("repo_api_token_repo_id", "repo-1")
		c.Set("repo_api_token_permission", "rw")

		result, err := pm.HasLibraryAccessCtx(c, "org-1", "user-1", "repo-2", PermissionR)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		hasAccess = result
		c.Status(200)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if hasAccess {
		t.Error("expected access to be denied for wrong repo")
	}
}

func TestHasLibraryAccessCtx_RepoApiToken_DeletedLibraryDenied(t *testing.T) {
	original := readLiveLibraryStateFn
	readLiveLibraryStateFn = func(_ *gocql.Session, _, _ string) (dbpkg.LibraryState, error) {
		return dbpkg.LibraryState{}, dbpkg.ErrLibraryDeleted
	}
	defer func() {
		readLiveLibraryStateFn = original
	}()

	pm := NewPermissionMiddleware(&dbpkg.DB{})

	r := gin.New()
	var hasAccess bool
	r.GET("/test", func(c *gin.Context) {
		c.Set("repo_api_token", true)
		c.Set("repo_api_token_repo_id", "repo-1")
		c.Set("repo_api_token_permission", "rw")

		result, err := pm.HasLibraryAccessCtx(c, "org-1", "user-1", "repo-1", PermissionRW)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		hasAccess = result
		c.Status(200)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if hasAccess {
		t.Error("expected deleted library repo token access to be denied")
	}
}

// --- RequireAdminOrAbove tests ---

func TestRequireAdminOrAbove_RejectsEmptyAuth(t *testing.T) {
	pm := NewPermissionMiddleware(nil)
	mw := pm.RequireAdminOrAbove()

	r := gin.New()
	r.Use(mw)
	r.GET("/test", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

// --- Unknown role tests ---

func TestUnknownOrgRole_TreatedAsLowest(t *testing.T) {
	pm := setupTestDB(t)
	// Unknown role gets 0 in the hierarchy (same as guest)
	if !pm.hasRequiredOrgRole(OrganizationRole("unknown"), RoleGuest) {
		t.Error("unknown role should be treated as lowest level (same as guest)")
	}
	if pm.hasRequiredOrgRole(OrganizationRole("unknown"), RoleUser) {
		t.Error("unknown role should not satisfy user requirement")
	}
}

func TestUnknownLibraryPermission_DeniesAccess(t *testing.T) {
	pm := setupTestDB(t)
	if pm.hasRequiredLibraryPermission(LibraryPermission("unknown"), PermissionR) {
		t.Error("unknown permission should not satisfy any requirement")
	}
}

func TestUnknownGroupRole_DeniesAccess(t *testing.T) {
	pm := setupTestDB(t)
	if pm.hasRequiredGroupRole(GroupRole("unknown"), GroupRoleMember) {
		t.Error("unknown group role should not satisfy any requirement")
	}
}

// ==================== Custom Permission Flags Tests ====================

func TestFlagsForPermission_Owner(t *testing.T) {
	flags := FlagsForPermission(PermissionOwner)
	for _, flag := range []string{"upload", "download", "create", "modify", "copy", "delete", "preview", "download_external_link"} {
		if !flags.HasFlag(flag) {
			t.Errorf("owner should have %s flag enabled", flag)
		}
	}
}

func TestFlagsForPermission_Admin(t *testing.T) {
	flags := FlagsForPermission(PermissionAdmin)
	for _, flag := range []string{"upload", "download", "create", "modify", "copy", "delete", "preview", "download_external_link"} {
		if !flags.HasFlag(flag) {
			t.Errorf("admin should have %s flag enabled", flag)
		}
	}
}

func TestFlagsForPermission_RW(t *testing.T) {
	flags := FlagsForPermission(PermissionRW)
	for _, flag := range []string{"upload", "download", "create", "modify", "copy", "delete", "preview", "download_external_link"} {
		if !flags.HasFlag(flag) {
			t.Errorf("rw should have %s flag enabled", flag)
		}
	}
}

func TestFlagsForPermission_CloudEdit(t *testing.T) {
	flags := FlagsForPermission(PermissionCloudEdit)

	enabled := []string{"upload", "create", "modify", "delete", "preview"}
	disabled := []string{"download", "copy", "download_external_link"}

	for _, flag := range enabled {
		if !flags.HasFlag(flag) {
			t.Errorf("cloud-edit should have %s flag enabled", flag)
		}
	}
	for _, flag := range disabled {
		if flags.HasFlag(flag) {
			t.Errorf("cloud-edit should NOT have %s flag enabled", flag)
		}
	}
}

func TestFlagsForPermission_R(t *testing.T) {
	flags := FlagsForPermission(PermissionR)

	enabled := []string{"download", "preview", "copy", "download_external_link"}
	disabled := []string{"upload", "create", "modify", "delete"}

	for _, flag := range enabled {
		if !flags.HasFlag(flag) {
			t.Errorf("r should have %s flag enabled", flag)
		}
	}
	for _, flag := range disabled {
		if flags.HasFlag(flag) {
			t.Errorf("r should NOT have %s flag enabled", flag)
		}
	}
}

func TestFlagsForPermission_Preview(t *testing.T) {
	flags := FlagsForPermission(PermissionPreview)

	if !flags.HasFlag("preview") {
		t.Error("preview should have preview flag enabled")
	}
	disabled := []string{"upload", "download", "create", "modify", "copy", "delete", "download_external_link"}
	for _, flag := range disabled {
		if flags.HasFlag(flag) {
			t.Errorf("preview should NOT have %s flag enabled", flag)
		}
	}
}

func TestFlagsForPermission_None(t *testing.T) {
	flags := FlagsForPermission(PermissionNone)
	allFlags := []string{"upload", "download", "create", "modify", "copy", "delete", "preview", "download_external_link"}
	for _, flag := range allFlags {
		if flags.HasFlag(flag) {
			t.Errorf("none should NOT have %s flag enabled", flag)
		}
	}
}

// --- HasFlag edge cases ---

func TestHasFlag_NilFlags_ReturnsTrue(t *testing.T) {
	var flags *PermissionFlags
	// nil flags = no restrictions (backward compat with standard permissions)
	if !flags.HasFlag("upload") {
		t.Error("nil flags should return true (no restrictions)")
	}
}

func TestHasFlag_UnknownFlag_ReturnsTrue(t *testing.T) {
	flags := &PermissionFlags{}
	// Unknown flags default to true (safe for forward compat)
	if !flags.HasFlag("some_future_flag") {
		t.Error("unknown flag should return true by default")
	}
}

func TestHasFlag_AllFlagNames(t *testing.T) {
	flags := &PermissionFlags{
		Upload: true, Download: false, Create: true, Modify: false,
		Copy: true, Delete: false, Preview: true, DownloadExternalLink: false,
	}

	tests := []struct {
		flag     string
		expected bool
	}{
		{"upload", true},
		{"download", false},
		{"create", true},
		{"modify", false},
		{"copy", true},
		{"delete", false},
		{"preview", true},
		{"download_external_link", false},
	}

	for _, tt := range tests {
		if flags.HasFlag(tt.flag) != tt.expected {
			t.Errorf("HasFlag(%q) = %v, want %v", tt.flag, !tt.expected, tt.expected)
		}
	}
}

// --- mergeFlags tests ---

func TestMergeFlags_Union(t *testing.T) {
	a := &PermissionFlags{Upload: true, Download: false, Create: false}
	b := &PermissionFlags{Upload: false, Download: true, Create: false}
	a.mergeFlags(b)

	if !a.Upload {
		t.Error("merge should preserve Upload=true from a")
	}
	if !a.Download {
		t.Error("merge should add Download=true from b")
	}
	if a.Create {
		t.Error("merge should leave Create=false when both false")
	}
}

func TestMergeFlags_NilOther(t *testing.T) {
	a := &PermissionFlags{Upload: true}
	a.mergeFlags(nil)
	if !a.Upload {
		t.Error("merge with nil should not change existing flags")
	}
}

func TestMergeFlags_AllFlags(t *testing.T) {
	a := &PermissionFlags{}
	b := allPermFlags()
	a.mergeFlags(b)

	allFlagNames := []string{"upload", "download", "create", "modify", "copy", "delete", "preview", "download_external_link"}
	for _, flag := range allFlagNames {
		if !a.HasFlag(flag) {
			t.Errorf("after merge with allPermFlags, %s should be true", flag)
		}
	}
}

// --- resolveCustomPermWithFlags tests ---

func TestResolveCustomPermWithFlags_MappingUploadToRW(t *testing.T) {
	// We can't test DB-dependent code without a DB, but we can test the mapping logic
	// by testing FlagsForPermission and the coarse mapping logic indirectly.
	// The mapping in resolveCustomPermWithFlags is:
	//   upload/modify/delete → RW
	//   download/copy → R
	//   preview → Preview
	//   else → None

	// Test via FlagsForPermission that the flag sets are consistent
	rwFlags := FlagsForPermission(PermissionRW)
	if !rwFlags.Upload || !rwFlags.Modify || !rwFlags.Delete {
		t.Error("RW should have upload, modify, and delete")
	}

	rFlags := FlagsForPermission(PermissionR)
	if !rFlags.Download || !rFlags.Copy {
		t.Error("R should have download and copy")
	}
	if rFlags.Upload || rFlags.Modify || rFlags.Delete {
		t.Error("R should NOT have upload, modify, or delete")
	}
}

// --- Library permission hierarchy with new levels ---

func TestLibraryPermissionHierarchy_NewLevels(t *testing.T) {
	pm := setupTestDB(t)

	tests := []struct {
		name     string
		user     LibraryPermission
		required LibraryPermission
		expected bool
	}{
		// Admin is same level as RW
		{"admin satisfies rw", PermissionAdmin, PermissionRW, true},
		{"admin satisfies r", PermissionAdmin, PermissionR, true},
		{"admin does not satisfy owner", PermissionAdmin, PermissionOwner, false},
		{"rw satisfies admin", PermissionRW, PermissionAdmin, true},

		// Cloud-edit is same level as RW
		{"cloud-edit satisfies r", PermissionCloudEdit, PermissionR, true},
		{"cloud-edit satisfies rw", PermissionCloudEdit, PermissionRW, true},
		{"cloud-edit does not satisfy owner", PermissionCloudEdit, PermissionOwner, false},

		// Preview is same level as R
		{"preview satisfies r", PermissionPreview, PermissionR, true},
		{"preview does not satisfy rw", PermissionPreview, PermissionRW, false},
		{"r satisfies preview", PermissionR, PermissionPreview, true},

		// Owner satisfies everything
		{"owner satisfies admin", PermissionOwner, PermissionAdmin, true},
		{"owner satisfies cloud-edit", PermissionOwner, PermissionCloudEdit, true},
		{"owner satisfies preview", PermissionOwner, PermissionPreview, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := pm.hasRequiredLibraryPermission(tt.user, tt.required)
			if result != tt.expected {
				t.Errorf("hasRequiredLibraryPermission(%s, %s) = %v, want %v",
					tt.user, tt.required, result, tt.expected)
			}
		})
	}
}

// --- RequirePermFlag with gin context caching ---

func TestRequirePermFlag_CachesInContext(t *testing.T) {
	// Pre-set cached flags in context, verify they're used
	r := gin.New()
	pm := NewPermissionMiddleware(nil) // nil DB — should use cache

	var result1, result2 bool
	r.GET("/test", func(c *gin.Context) {
		// Pre-populate cache
		flags := &PermissionFlags{Upload: true, Download: false, Delete: true}
		c.Set("_perm_flags", flags)

		result1 = pm.RequirePermFlag(c, "upload")
		result2 = pm.RequirePermFlag(c, "download")
		c.Status(200)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if !result1 {
		t.Error("cached upload flag should be true")
	}
	if result2 {
		t.Error("cached download flag should be false")
	}
}

func TestRequirePermFlagForRepo_CachesInContext(t *testing.T) {
	r := gin.New()
	pm := NewPermissionMiddleware(nil)

	var result bool
	r.GET("/test", func(c *gin.Context) {
		flags := &PermissionFlags{Copy: false, Preview: true}
		c.Set("_perm_flags", flags)

		result = pm.RequirePermFlagForRepo(c, "some-repo-id", "copy")
		c.Status(200)
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/test", nil)
	r.ServeHTTP(w, req)

	if result {
		t.Error("cached copy flag should be false")
	}
}

// Note: RequirePermFlag without cache requires a live DB connection.
// When no cache exists and DB is nil, GetLibraryPermissionWithFlags panics
// on db.Session(). This is acceptable because in production the DB is always
// available, and the cache is populated on first access per request.
// A full integration test with a real DB would cover this path.

// --- allPermFlags test ---

func TestAllPermFlags(t *testing.T) {
	flags := allPermFlags()
	allFlagNames := []string{"upload", "download", "create", "modify", "copy", "delete", "preview", "download_external_link"}
	for _, flag := range allFlagNames {
		if !flags.HasFlag(flag) {
			t.Errorf("allPermFlags should have %s enabled", flag)
		}
	}
}

// --- PermissionFlags JSON serialization ---

func TestPermissionFlags_JSONRoundTrip(t *testing.T) {
	original := &PermissionFlags{
		Upload: true, Download: false, Create: true, Modify: false,
		Copy: true, Delete: false, Preview: true, DownloadExternalLink: false,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var decoded PermissionFlags
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if decoded.Upload != original.Upload || decoded.Download != original.Download ||
		decoded.Create != original.Create || decoded.Modify != original.Modify ||
		decoded.Copy != original.Copy || decoded.Delete != original.Delete ||
		decoded.Preview != original.Preview || decoded.DownloadExternalLink != original.DownloadExternalLink {
		t.Error("JSON round-trip produced different flags")
	}
}

func TestPermissionFlags_JSONFieldNames(t *testing.T) {
	flags := &PermissionFlags{Upload: true, DownloadExternalLink: true}
	data, _ := json.Marshal(flags)
	str := string(data)

	// Verify JSON field names match what the DB stores
	expectedFields := []string{`"upload"`, `"download"`, `"create"`, `"modify"`, `"copy"`, `"delete"`, `"preview"`, `"download_external_link"`}
	for _, field := range expectedFields {
		if !contains(str, field) {
			t.Errorf("JSON should contain field %s, got: %s", field, str)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
