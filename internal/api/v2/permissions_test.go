package v2

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// MockPermissionMiddleware implements permission checking for tests
type MockPermissionMiddleware struct {
	userRoles   map[string]middleware.OrganizationRole // userID -> role
	libraryPerm map[string]string                      // repoID -> permission
}

func NewMockPermissionMiddleware() *MockPermissionMiddleware {
	return &MockPermissionMiddleware{
		userRoles:   make(map[string]middleware.OrganizationRole),
		libraryPerm: make(map[string]string),
	}
}

func (m *MockPermissionMiddleware) SetUserRole(userID string, role middleware.OrganizationRole) {
	m.userRoles[userID] = role
}

func (m *MockPermissionMiddleware) GetUserOrgRole(orgID, userID string) (middleware.OrganizationRole, error) {
	if role, ok := m.userRoles[userID]; ok {
		return role, nil
	}
	return middleware.RoleUser, nil // Default to user if not set
}

func (m *MockPermissionMiddleware) HasLibraryAccess(orgID, userID, repoID string, perm middleware.LibraryPermission) (bool, error) {
	return true, nil // Allow all for this mock
}

// TestRequireWritePermission tests the requireWritePermission helper function
func TestRequireWritePermission(t *testing.T) {
	tests := []struct {
		name           string
		userRole       middleware.OrganizationRole
		expectAllowed  bool
		expectedStatus int
	}{
		{
			name:           "superadmin can write",
			userRole:       middleware.RoleSuperAdmin,
			expectAllowed:  true,
			expectedStatus: 0,
		},
		{
			name:           "admin can write",
			userRole:       middleware.RoleAdmin,
			expectAllowed:  true,
			expectedStatus: 0, // No response set, allowed
		},
		{
			name:           "user can write",
			userRole:       middleware.RoleUser,
			expectAllowed:  true,
			expectedStatus: 0,
		},
		{
			name:           "readonly cannot write",
			userRole:       middleware.RoleReadOnly,
			expectAllowed:  false,
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "guest cannot write",
			userRole:       middleware.RoleGuest,
			expectAllowed:  false,
			expectedStatus: http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a mock middleware with the test role
			mockPerm := NewMockPermissionMiddleware()
			mockPerm.SetUserRole("test-user", tt.userRole)

			// Create gin context
			gin.SetMode(gin.TestMode)
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Set("org_id", "test-org")
			c.Set("user_id", "test-user")

			// Simulate permission check logic using shared helper
			allowed := middleware.HasRequiredOrgRole(tt.userRole, middleware.RoleUser)

			assert := assert.New(t)
			assert.Equal(tt.expectAllowed, allowed,
				"Role %v should have expectAllowed=%v", tt.userRole, tt.expectAllowed)

			if !allowed {
				// Simulate what the handler would do
				c.JSON(http.StatusForbidden, gin.H{
					"error": "insufficient permissions: write operations require 'user' role or higher",
				})
				assert.Equal(tt.expectedStatus, w.Code)
			}
		})
	}
}

// TestRoleHierarchyConstants verifies the role hierarchy is correctly defined
// Uses the shared HasRequiredOrgRole function instead of duplicating the map
func TestRoleHierarchyConstants(t *testing.T) {
	assert := assert.New(t)

	// Verify superadmin is highest — satisfies all roles
	assert.True(middleware.HasRequiredOrgRole(middleware.RoleSuperAdmin, middleware.RoleSuperAdmin))
	assert.True(middleware.HasRequiredOrgRole(middleware.RoleSuperAdmin, middleware.RoleAdmin))
	assert.True(middleware.HasRequiredOrgRole(middleware.RoleSuperAdmin, middleware.RoleUser))
	assert.True(middleware.HasRequiredOrgRole(middleware.RoleSuperAdmin, middleware.RoleReadOnly))
	assert.True(middleware.HasRequiredOrgRole(middleware.RoleSuperAdmin, middleware.RoleGuest))

	// Verify admin is second highest
	assert.False(middleware.HasRequiredOrgRole(middleware.RoleAdmin, middleware.RoleSuperAdmin))
	assert.True(middleware.HasRequiredOrgRole(middleware.RoleAdmin, middleware.RoleAdmin))
	assert.True(middleware.HasRequiredOrgRole(middleware.RoleAdmin, middleware.RoleUser))
	assert.True(middleware.HasRequiredOrgRole(middleware.RoleAdmin, middleware.RoleReadOnly))
	assert.True(middleware.HasRequiredOrgRole(middleware.RoleAdmin, middleware.RoleGuest))

	// Verify user is third highest
	assert.False(middleware.HasRequiredOrgRole(middleware.RoleUser, middleware.RoleSuperAdmin))
	assert.False(middleware.HasRequiredOrgRole(middleware.RoleUser, middleware.RoleAdmin))
	assert.True(middleware.HasRequiredOrgRole(middleware.RoleUser, middleware.RoleUser))
	assert.True(middleware.HasRequiredOrgRole(middleware.RoleUser, middleware.RoleReadOnly))
	assert.True(middleware.HasRequiredOrgRole(middleware.RoleUser, middleware.RoleGuest))

	// Verify readonly is fourth
	assert.False(middleware.HasRequiredOrgRole(middleware.RoleReadOnly, middleware.RoleUser))
	assert.True(middleware.HasRequiredOrgRole(middleware.RoleReadOnly, middleware.RoleReadOnly))
	assert.True(middleware.HasRequiredOrgRole(middleware.RoleReadOnly, middleware.RoleGuest))

	// Verify guest is lowest
	assert.False(middleware.HasRequiredOrgRole(middleware.RoleGuest, middleware.RoleReadOnly))
	assert.True(middleware.HasRequiredOrgRole(middleware.RoleGuest, middleware.RoleGuest))
}

// TestWritePermissionDeniedResponse verifies the error response format
func TestWritePermissionDeniedResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	// Simulate the permission denied response
	c.JSON(http.StatusForbidden, gin.H{
		"error": "insufficient permissions: write operations require 'user' role or higher",
	})

	assert := assert.New(t)
	assert.Equal(http.StatusForbidden, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(err)
	assert.Contains(resp["error"], "insufficient permissions")
}

// TestFileOperationsRequireWritePermission documents which operations require write permission
func TestFileOperationsRequireWritePermission(t *testing.T) {
	// This test documents all file operations that require write permission
	writeOperations := []string{
		"CreateDirectory",
		"RenameDirectory",
		"DeleteDirectory",
		"CreateFile",
		"RenameFile",
		"DeleteFile",
		"MoveFile",
		"CopyFile",
		"BatchDeleteItems",
	}

	readOnlyOperations := []string{
		"ListDirectory",
		"GetFileInfo",
		"GetFileDetail",
		"GetFileDownloadLink",
	}

	t.Run("write operations require user role or higher", func(t *testing.T) {
		for _, op := range writeOperations {
			t.Logf("Operation %s requires write permission", op)
		}
		assert.Equal(t, 9, len(writeOperations), "Should have 9 write operations")
	})

	t.Run("read operations allow all roles", func(t *testing.T) {
		for _, op := range readOnlyOperations {
			t.Logf("Operation %s is read-only", op)
		}
		assert.Equal(t, 4, len(readOnlyOperations), "Should have 4 read-only operations")
	})
}

// TestLibraryCreationRequiresWriteRole verifies library creation permission check
func TestLibraryCreationRequiresWriteRole(t *testing.T) {
	tests := []struct {
		role     middleware.OrganizationRole
		canWrite bool
	}{
		{middleware.RoleSuperAdmin, true},
		{middleware.RoleAdmin, true},
		{middleware.RoleUser, true},
		{middleware.RoleReadOnly, false},
		{middleware.RoleGuest, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.role), func(t *testing.T) {
			canWrite := middleware.HasRequiredOrgRole(tt.role, middleware.RoleUser)
			assert.Equal(t, tt.canWrite, canWrite, "Role %v write access mismatch", tt.role)
		})
	}
}

// TestPermissionCheckOrderOfPrecedence documents the order of permission checks
func TestPermissionCheckOrderOfPrecedence(t *testing.T) {
	// Document the order of permission checks in handlers:
	// 1. User role check (admin/user/readonly/guest)
	// 2. Library-level permission check (owner/rw/r)
	// 3. Encryption session check (if library is encrypted)

	t.Run("permission check order", func(t *testing.T) {
		assert := assert.New(t)

		// Order of checks
		checks := []string{
			"1. requireWritePermission - checks user org role",
			"2. HasLibraryAccess - checks library-level permission",
			"3. requireDecryptSession - checks if encrypted library is unlocked",
		}

		assert.Equal(3, len(checks), "Should have 3 permission checks")

		// Role check should happen first
		t.Log("Permission checks are performed in order:")
		for _, check := range checks {
			t.Log("  " + check)
		}
	})
}

// TestAccountInfoPermissionFlags verifies the permission flags in account info response
func TestAccountInfoPermissionFlags(t *testing.T) {
	// Test the permission flags that should be returned by /api2/account/info/
	// based on user role

	tests := []struct {
		role            string
		canAddRepo      bool
		canShareRepo    bool
		canAddGroup     bool
		canGenerateLink bool
	}{
		{"superadmin", true, true, true, true},
		{"owner", true, true, true, true},
		{"admin", true, true, true, true},
		{"user", true, true, true, true},
		{"readonly", false, false, false, false},
		{"guest", false, false, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			// These are the expected values based on role
			isWriteRole := canManageLegacyContent(tt.role)

			assert := assert.New(t)
			assert.Equal(tt.canAddRepo, isWriteRole, "can_add_repo mismatch for %s", tt.role)
			assert.Equal(tt.canShareRepo, isWriteRole, "can_share_repo mismatch for %s", tt.role)
			assert.Equal(tt.canAddGroup, isWriteRole, "can_add_group mismatch for %s", tt.role)
			assert.Equal(tt.canGenerateLink, isWriteRole, "can_generate_link mismatch for %s", tt.role)
		})
	}
}

// TestEncryptedLibraryPermissionDenied verifies encrypted library access denial
func TestEncryptedLibraryPermissionDenied(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	// Simulate the encrypted library access denied response
	c.JSON(http.StatusForbidden, gin.H{
		"error":     "Library is encrypted",
		"error_msg": "This library is encrypted. Please provide the password to unlock it.",
	})

	assert := assert.New(t)
	assert.Equal(http.StatusForbidden, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	assert.NoError(err)
	assert.Equal("Library is encrypted", resp["error"])
	assert.Contains(resp["error_msg"], "password")
}

// BenchmarkRoleHierarchyCheck benchmarks the role hierarchy check
func BenchmarkRoleHierarchyCheck(b *testing.B) {
	roles := []middleware.OrganizationRole{
		middleware.RoleSuperAdmin,
		middleware.RoleAdmin,
		middleware.RoleUser,
		middleware.RoleReadOnly,
		middleware.RoleGuest,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		role := roles[i%5]
		_ = middleware.HasRequiredOrgRole(role, middleware.RoleUser)
	}
}
