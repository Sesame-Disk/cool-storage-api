package v2

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubDeletedLibraryRoleResolver struct {
	role middleware.OrganizationRole
	err  error
	seen [][2]string
}

func (s *stubDeletedLibraryRoleResolver) GetUserOrgRole(orgID, userID string) (middleware.OrganizationRole, error) {
	s.seen = append(s.seen, [2]string{orgID, userID})
	if s.err != nil {
		return middleware.RoleGuest, s.err
	}
	return s.role, nil
}

func TestResolveDeletedLibraryCallerRole(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("uses current org role resolver", func(t *testing.T) {
		resolver := &stubDeletedLibraryRoleResolver{role: middleware.RoleAdmin}
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Set("org_id", "org-1")
		c.Set("user_id", "user-1")

		role, err := resolveDeletedLibraryCallerRole(c, resolver)
		require.NoError(t, err)
		assert.Equal(t, middleware.RoleAdmin, role)
		assert.Equal(t, [][2]string{{"org-1", "user-1"}}, resolver.seen)
	})

	t.Run("fails closed on missing identity", func(t *testing.T) {
		resolver := &stubDeletedLibraryRoleResolver{role: middleware.RoleSuperAdmin}
		c, _ := gin.CreateTestContext(httptest.NewRecorder())

		role, err := resolveDeletedLibraryCallerRole(c, resolver)
		require.Error(t, err)
		assert.Equal(t, middleware.RoleGuest, role)
		assert.Empty(t, resolver.seen)
	})

	t.Run("propagates resolver errors", func(t *testing.T) {
		resolver := &stubDeletedLibraryRoleResolver{err: errors.New("boom")}
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Set("org_id", middleware.PlatformOrgID)
		c.Set("user_id", "user-1")

		role, err := resolveDeletedLibraryCallerRole(c, resolver)
		require.Error(t, err)
		assert.Equal(t, middleware.RoleGuest, role)
	})
}

func TestPlatformSuperAdminHelperForDeletedLibraries(t *testing.T) {
	assert.True(t, middleware.IsPlatformSuperAdmin(middleware.PlatformOrgID, middleware.RoleSuperAdmin))
	assert.False(t, middleware.IsPlatformSuperAdmin("org-1", middleware.RoleSuperAdmin))
	assert.False(t, middleware.IsPlatformSuperAdmin(middleware.PlatformOrgID, middleware.RoleAdmin))
}
