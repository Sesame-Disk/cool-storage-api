package middleware

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// isNotFound checks if a Cassandra error indicates no rows were found.
func isNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "not found")
}

// PermissionMiddleware handles permission checking
type PermissionMiddleware struct {
	db *db.DB
}

// NewPermissionMiddleware creates a new permission middleware
func NewPermissionMiddleware(database *db.DB) *PermissionMiddleware {
	return &PermissionMiddleware{db: database}
}

// OrganizationRole represents user roles at org level
type OrganizationRole string

const (
	RoleSuperAdmin OrganizationRole = "superadmin"
	RoleOwner      OrganizationRole = "owner"
	RoleAdmin      OrganizationRole = "admin"
	RoleUser       OrganizationRole = "user"
	RoleReadOnly   OrganizationRole = "readonly"
	RoleGuest      OrganizationRole = "guest"
)

// PlatformOrgID is the well-known UUID for the platform-level organization.
// Superadmin users must belong to this org.
const PlatformOrgID = "00000000-0000-0000-0000-000000000000"

// LibraryPermission represents library access permissions
type LibraryPermission string

const (
	PermissionOwner     LibraryPermission = "owner"
	PermissionAdmin     LibraryPermission = "admin"
	PermissionRW        LibraryPermission = "rw"
	PermissionCloudEdit LibraryPermission = "cloud-edit"
	PermissionR         LibraryPermission = "r"
	PermissionPreview   LibraryPermission = "preview"
	PermissionNone      LibraryPermission = ""
)

// GroupRole represents roles within a group
type GroupRole string

const (
	GroupRoleOwner  GroupRole = "owner"
	GroupRoleAdmin  GroupRole = "admin"
	GroupRoleMember GroupRole = "member"
)

// RequireAuth ensures user is authenticated
// This should be applied to all protected routes
func (m *PermissionMiddleware) RequireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("user_id")
		orgID := c.GetString("org_id")

		if userID == "" || orgID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			c.Abort()
			return
		}

		c.Next()
	}
}

// RequireOrgRole checks if user has required organization role
// Usage: RequireOrgRole(RoleAdmin) - requires admin role
func (m *PermissionMiddleware) RequireOrgRole(requiredRole OrganizationRole) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("user_id")
		orgID := c.GetString("org_id")

		if userID == "" || orgID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			c.Abort()
			return
		}

		// Get user's role in the organization
		role, err := m.GetUserOrgRole(orgID, userID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check permissions"})
			c.Abort()
			return
		}

		// Check if user has required role
		if !m.hasRequiredOrgRole(role, requiredRole) {
			c.JSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
			c.Abort()
			return
		}

		// Store role in context for later use
		c.Set("user_org_role", role)
		c.Next()
	}
}

// RequireLibraryPermission checks if user has required permission for a library
// Usage: RequireLibraryPermission("repo_id", PermissionRW)
func (m *PermissionMiddleware) RequireLibraryPermission(paramName string, requiredPerm LibraryPermission) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("user_id")
		orgID := c.GetString("org_id")
		repoID := c.Param(paramName)

		if repoID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "library_id required"})
			c.Abort()
			return
		}

		// Check if this is a repo API token (scoped to a specific library)
		if isRepoToken, _ := c.Get("repo_api_token"); isRepoToken == true {
			tokenRepoID := c.GetString("repo_api_token_repo_id")
			tokenPerm := c.GetString("repo_api_token_permission")

			// Token must be for this specific library
			if tokenRepoID != repoID {
				c.JSON(http.StatusForbidden, gin.H{"error": "API token does not have access to this library"})
				c.Abort()
				return
			}

			// Map token permission to library permission
			var libPerm LibraryPermission
			switch tokenPerm {
			case "rw":
				libPerm = PermissionRW
			default:
				libPerm = PermissionR
			}

			if !m.hasRequiredLibraryPermission(libPerm, requiredPerm) {
				c.JSON(http.StatusForbidden, gin.H{"error": "API token has insufficient permissions"})
				c.Abort()
				return
			}

			c.Set("library_permission", libPerm)
			c.Next()
			return
		}

		// Get user's permission for this library
		permission, err := m.GetLibraryPermission(orgID, userID, repoID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check library permissions"})
			c.Abort()
			return
		}

		// Check if user has required permission
		if !m.hasRequiredLibraryPermission(permission, requiredPerm) {
			c.JSON(http.StatusForbidden, gin.H{"error": "insufficient library permissions"})
			c.Abort()
			return
		}

		// Store permission in context
		c.Set("library_permission", permission)
		c.Next()
	}
}

// RequireLibraryOwner checks if user is the owner of a library
func (m *PermissionMiddleware) RequireLibraryOwner(paramName string) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("user_id")
		orgID := c.GetString("org_id")
		repoID := c.Param(paramName)

		if repoID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "library_id required"})
			c.Abort()
			return
		}

		isOwner, err := m.IsLibraryOwner(orgID, userID, repoID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check ownership"})
			c.Abort()
			return
		}

		if !isOwner {
			c.JSON(http.StatusForbidden, gin.H{"error": "only library owner can perform this action"})
			c.Abort()
			return
		}

		c.Next()
	}
}

// RequireGroupRole checks if user has required role in a group
func (m *PermissionMiddleware) RequireGroupRole(paramName string, requiredRole GroupRole) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("user_id")
		groupID := c.Param(paramName)

		if groupID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "group_id required"})
			c.Abort()
			return
		}

		role, err := m.GetGroupRole(groupID, userID)
		if err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": "not a group member"})
			c.Abort()
			return
		}

		if !m.hasRequiredGroupRole(role, requiredRole) {
			c.JSON(http.StatusForbidden, gin.H{"error": "insufficient group permissions"})
			c.Abort()
			return
		}

		c.Set("group_role", role)
		c.Next()
	}
}

// GetUserOrgRole retrieves user's role in an organization
func (m *PermissionMiddleware) GetUserOrgRole(orgID, userID string) (OrganizationRole, error) {
	if m == nil || m.db == nil {
		return RoleGuest, fmt.Errorf("database not available")
	}

	var role string
	err := m.db.Session().Query(`
		SELECT role FROM users WHERE org_id = ? AND user_id = ?
	`, orgID, userID).Scan(&role)

	if err != nil {
		return RoleGuest, err
	}

	return OrganizationRole(role), nil
}

// PermissionFlags represents granular permission flags for custom permissions.
// For standard permissions (rw, r, etc.), default flags are derived automatically.
type PermissionFlags struct {
	Upload               bool `json:"upload"`
	Download             bool `json:"download"`
	Create               bool `json:"create"`
	Modify               bool `json:"modify"`
	Copy                 bool `json:"copy"`
	Delete               bool `json:"delete"`
	Preview              bool `json:"preview"`
	DownloadExternalLink bool `json:"download_external_link"`
}

// allPermFlags returns flags with everything enabled.
func allPermFlags() *PermissionFlags {
	return &PermissionFlags{true, true, true, true, true, true, true, true}
}

// FlagsForPermission returns the default flags for a standard permission level.
func FlagsForPermission(perm LibraryPermission) *PermissionFlags {
	switch perm {
	case PermissionOwner, PermissionAdmin, PermissionRW:
		return allPermFlags()
	case PermissionCloudEdit:
		return &PermissionFlags{Upload: true, Create: true, Modify: true, Delete: true, Preview: true}
	case PermissionR:
		return &PermissionFlags{Download: true, Preview: true, Copy: true, DownloadExternalLink: true}
	case PermissionPreview:
		return &PermissionFlags{Preview: true}
	default:
		return &PermissionFlags{}
	}
}

// HasFlag checks if a specific flag is enabled by name.
func (f *PermissionFlags) HasFlag(flag string) bool {
	if f == nil {
		return true // nil flags = no restrictions
	}
	switch flag {
	case "upload":
		return f.Upload
	case "download":
		return f.Download
	case "create":
		return f.Create
	case "modify":
		return f.Modify
	case "copy":
		return f.Copy
	case "delete":
		return f.Delete
	case "preview":
		return f.Preview
	case "download_external_link":
		return f.DownloadExternalLink
	default:
		return true
	}
}

// mergeFlags combines two PermissionFlags using OR (union of capabilities).
func (f *PermissionFlags) mergeFlags(other *PermissionFlags) {
	if other == nil {
		return
	}
	f.Upload = f.Upload || other.Upload
	f.Download = f.Download || other.Download
	f.Create = f.Create || other.Create
	f.Modify = f.Modify || other.Modify
	f.Copy = f.Copy || other.Copy
	f.Delete = f.Delete || other.Delete
	f.Preview = f.Preview || other.Preview
	f.DownloadExternalLink = f.DownloadExternalLink || other.DownloadExternalLink
}

// GetLibraryPermission retrieves user's permission for a library
func (m *PermissionMiddleware) GetLibraryPermission(orgID, userID, repoID string) (LibraryPermission, error) {
	perm, _, err := m.GetLibraryPermissionWithFlags(orgID, userID, repoID)
	return perm, err
}

// GetLibraryPermissionWithFlags retrieves both the coarse permission level
// and the granular PermissionFlags for the user's access to a library.
// When the user has multiple shares (direct + group), flags are merged (OR).
func (m *PermissionMiddleware) GetLibraryPermissionWithFlags(orgID, userID, repoID string) (LibraryPermission, *PermissionFlags, error) {
	// Check if user is admin/superadmin - they have full access to all libraries
	role, err := m.GetUserOrgRole(orgID, userID)
	if err == nil && (role == RoleSuperAdmin || role == RoleOwner || role == RoleAdmin) {
		return PermissionOwner, allPermFlags(), nil
	}

	// Check if user is the owner
	var ownerIDStr string
	err = m.db.Session().Query(`
		SELECT owner_id FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(&ownerIDStr)

	if err != nil {
		if isNotFound(err) {
			return PermissionNone, nil, nil
		}
		return PermissionNone, nil, err
	}

	if ownerIDStr == userID {
		return PermissionOwner, allPermFlags(), nil
	}

	// Check if library is shared with user or user's groups
	shareIter := m.db.Session().Query(`
		SELECT shared_to, shared_to_type, permission FROM shares
		WHERE library_id = ?
	`, repoID).Iter()

	var sharedTo, sharedToType, sharePerm string
	var highestPermission LibraryPermission = PermissionNone
	mergedFlags := &PermissionFlags{}

	// Build set of user's group IDs for quick lookup (lazy-loaded)
	var userGroupIDs map[string]bool

	for shareIter.Scan(&sharedTo, &sharedToType, &sharePerm) {
		var perm LibraryPermission
		var flags *PermissionFlags

		// Resolve custom permissions (UUID) vs standard permissions
		if _, parseErr := uuid.Parse(sharePerm); parseErr == nil {
			perm, flags = m.resolveCustomPermWithFlags(sharePerm)
		} else {
			perm = LibraryPermission(sharePerm)
			flags = FlagsForPermission(perm)
		}

		matched := false
		if sharedToType == "user" && sharedTo == userID {
			matched = true
		} else if sharedToType == "group" {
			// Lazy-load user's groups on first group share encountered
			if userGroupIDs == nil {
				userGroupIDs = make(map[string]bool)
				groupIter := m.db.Session().Query(`
					SELECT group_id FROM groups_by_member
					WHERE org_id = ? AND user_id = ?
				`, orgID, userID).Iter()
				var gid string
				for groupIter.Scan(&gid) {
					userGroupIDs[gid] = true
				}
				groupIter.Close()
			}
			matched = userGroupIDs[sharedTo]
		}

		if matched {
			// For standard rw/admin (all flags), we can exit early
			if (perm == PermissionRW || perm == PermissionAdmin) && flags.Upload && flags.Download {
				shareIter.Close()
				return perm, allPermFlags(), nil
			}
			if m.hasRequiredLibraryPermission(perm, highestPermission) {
				highestPermission = perm
			}
			mergedFlags.mergeFlags(flags)
		}
	}

	if err := shareIter.Close(); err != nil {
		// Log error but continue - return what we found so far
	}

	if highestPermission != PermissionNone {
		return highestPermission, mergedFlags, nil
	}

	return PermissionNone, &PermissionFlags{}, nil
}

// GetLibraryPermissionRaw returns the raw permission string for a user's access
// to a library. For standard permissions it returns "rw", "r", etc.
// For custom permissions it returns "custom-{uuid}" which the frontend uses
// to fetch the full permission object via getCustomPermission().
func (m *PermissionMiddleware) GetLibraryPermissionRaw(orgID, userID, repoID string) (string, error) {
	// Check if user is admin/superadmin - they have full access
	role, err := m.GetUserOrgRole(orgID, userID)
	if err == nil && (role == RoleSuperAdmin || role == RoleOwner || role == RoleAdmin) {
		return "rw", nil
	}

	// Check if user is the owner
	var ownerIDStr string
	err = m.db.Session().Query(`
		SELECT owner_id FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(&ownerIDStr)
	if err != nil {
		if isNotFound(err) {
			return "", nil
		}
		return "", err
	}
	if ownerIDStr == userID {
		return "rw", nil
	}

	// Find the highest-priority share permission (raw string)
	shareIter := m.db.Session().Query(`
		SELECT shared_to, shared_to_type, permission FROM shares
		WHERE library_id = ?
	`, repoID).Iter()

	var sharedTo, sharedToType, sharePerm string
	var bestRaw string
	var bestLevel int

	var userGroupIDs map[string]bool

	for shareIter.Scan(&sharedTo, &sharedToType, &sharePerm) {
		matched := false
		if sharedToType == "user" && sharedTo == userID {
			matched = true
		} else if sharedToType == "group" {
			if userGroupIDs == nil {
				userGroupIDs = make(map[string]bool)
				groupIter := m.db.Session().Query(`
					SELECT group_id FROM groups_by_member
					WHERE org_id = ? AND user_id = ?
				`, orgID, userID).Iter()
				var gid string
				for groupIter.Scan(&gid) {
					userGroupIDs[gid] = true
				}
				groupIter.Close()
			}
			matched = userGroupIDs[sharedTo]
		}

		if matched {
			// Determine the coarse level for ranking
			var level int
			if _, parseErr := uuid.Parse(sharePerm); parseErr == nil {
				// Custom permission — resolve coarse level
				perm, _ := m.resolveCustomPermWithFlags(sharePerm)
				level = permLevel(perm)
				// Format as "custom-{uuid}" for the frontend
				if level > bestLevel {
					bestLevel = level
					bestRaw = "custom-" + sharePerm
				}
			} else {
				perm := LibraryPermission(sharePerm)
				level = permLevel(perm)
				if level > bestLevel {
					bestLevel = level
					bestRaw = sharePerm
				}
			}
		}
	}
	shareIter.Close()

	if bestRaw == "" {
		return "", nil
	}

	// Map owner/admin to rw for non-custom permissions
	if bestRaw == "owner" {
		return "rw", nil
	}

	return bestRaw, nil
}

// permLevel returns a numeric level for permission ranking.
func permLevel(perm LibraryPermission) int {
	switch perm {
	case PermissionOwner:
		return 4
	case PermissionAdmin, PermissionRW:
		return 3
	case PermissionCloudEdit:
		return 2
	case PermissionR, PermissionPreview:
		return 1
	default:
		return 0
	}
}

// IsLibraryOwner checks if user is the owner of a library
func (m *PermissionMiddleware) IsLibraryOwner(orgID, userID, repoID string) (bool, error) {
	if m == nil || m.db == nil {
		return false, fmt.Errorf("database not available")
	}

	var ownerIDStr string
	err := m.db.Session().Query(`
		SELECT owner_id FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(&ownerIDStr)

	if err != nil {
		if isNotFound(err) {
			return false, nil // Library doesn't exist
		}
		return false, err
	}

	return ownerIDStr == userID, nil
}

// GetGroupRole retrieves user's role in a group
func (m *PermissionMiddleware) GetGroupRole(groupID, userID string) (GroupRole, error) {
	var role string
	err := m.db.Session().Query(`
		SELECT role FROM group_members WHERE group_id = ? AND user_id = ?
	`, groupID, userID).Scan(&role)

	if err != nil {
		return "", err
	}

	return GroupRole(role), nil
}

// RequireSuperAdmin checks that the user has the superadmin role AND belongs to
// the platform org. This prevents privilege escalation by setting the role string
// on a regular org user.
func (m *PermissionMiddleware) RequireSuperAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetString("user_id")
		orgID := c.GetString("org_id")

		if userID == "" || orgID == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			c.Abort()
			return
		}

		// Must belong to platform org
		if orgID != PlatformOrgID {
			c.JSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
			c.Abort()
			return
		}

		// Get user's role
		role, err := m.GetUserOrgRole(orgID, userID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check permissions"})
			c.Abort()
			return
		}

		if !IsPlatformSuperAdmin(orgID, role) {
			c.JSON(http.StatusForbidden, gin.H{"error": "insufficient permissions"})
			c.Abort()
			return
		}

		c.Set("user_org_role", role)
		c.Next()
	}
}

// RequireAdminOrAbove checks that the user has at least admin role (admin or superadmin).
func (m *PermissionMiddleware) RequireAdminOrAbove() gin.HandlerFunc {
	return m.RequireOrgRole(RoleAdmin)
}

// orgRoleHierarchy defines the canonical role hierarchy.
// superadmin(5) > owner(4) > admin(3) > user(2) > readonly(1) > guest(0)
var orgRoleHierarchy = map[OrganizationRole]int{
	RoleSuperAdmin: 5,
	RoleOwner:      4,
	RoleAdmin:      3,
	RoleUser:       2,
	RoleReadOnly:   1,
	RoleGuest:      0,
}

// HasRequiredOrgRole checks if userRole meets or exceeds requiredRole in the hierarchy.
// Hierarchy: superadmin(5) > owner(4) > admin(3) > user(2) > readonly(1) > guest(0)
func HasRequiredOrgRole(userRole, requiredRole OrganizationRole) bool {
	return orgRoleHierarchy[userRole] >= orgRoleHierarchy[requiredRole]
}

// IsOrgStaff returns true if the role string represents an admin-level or higher role
// (admin, owner, superadmin). Use this instead of inline comparisons.
func IsOrgStaff(role string) bool {
	return HasRequiredOrgRole(OrganizationRole(role), RoleAdmin)
}

// hasRequiredOrgRole checks if user's role meets requirement
func (m *PermissionMiddleware) hasRequiredOrgRole(userRole, requiredRole OrganizationRole) bool {
	return HasRequiredOrgRole(userRole, requiredRole)
}

// hasRequiredLibraryPermission checks if user's permission meets requirement
func (m *PermissionMiddleware) hasRequiredLibraryPermission(userPerm, requiredPerm LibraryPermission) bool {
	// Permission hierarchy: owner > rw/cloud-edit > r/preview > none
	// cloud-edit: user can view and edit online (no download/sync) → write-level
	// preview:    user can only view online (no download/sync)    → read-level
	permHierarchy := map[LibraryPermission]int{
		PermissionOwner:     3,
		PermissionAdmin:     2,
		PermissionRW:        2,
		PermissionCloudEdit: 2,
		PermissionR:         1,
		PermissionPreview:   1,
		PermissionNone:      0,
	}

	return permHierarchy[userPerm] >= permHierarchy[requiredPerm]
}

// hasRequiredGroupRole checks if user's group role meets requirement
func (m *PermissionMiddleware) hasRequiredGroupRole(userRole, requiredRole GroupRole) bool {
	// Role hierarchy: owner > admin > member
	roleHierarchy := map[GroupRole]int{
		GroupRoleOwner:  3,
		GroupRoleAdmin:  2,
		GroupRoleMember: 1,
	}

	return roleHierarchy[userRole] >= roleHierarchy[requiredRole]
}

// resolveCustomPermWithFlags resolves a custom permission UUID to both a coarse
// LibraryPermission level and granular PermissionFlags.
func (m *PermissionMiddleware) resolveCustomPermWithFlags(permID string) (LibraryPermission, *PermissionFlags) {
	var permJSON string
	if err := m.db.Session().Query(`
		SELECT permission_json FROM custom_share_permissions WHERE permission_id = ?
	`, permID).Scan(&permJSON); err != nil {
		return PermissionNone, &PermissionFlags{}
	}

	var flags PermissionFlags
	if err := json.Unmarshal([]byte(permJSON), &flags); err != nil {
		return PermissionNone, &PermissionFlags{}
	}

	// Map flags to coarse permission level
	var perm LibraryPermission
	switch {
	case flags.Upload || flags.Modify || flags.Delete:
		perm = PermissionRW
	case flags.Download || flags.Copy:
		perm = PermissionR
	case flags.Preview:
		perm = PermissionPreview
	default:
		perm = PermissionNone
	}

	return perm, &flags
}

// RequirePermFlag checks if the user has a specific granular permission flag
// for the library in the current request. Results are cached in gin context
// to avoid repeated DB queries within the same request.
func (m *PermissionMiddleware) RequirePermFlag(c *gin.Context, flag string) bool {
	// Try cached flags first
	if cached, exists := c.Get("_perm_flags"); exists {
		return cached.(*PermissionFlags).HasFlag(flag)
	}

	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")
	repoID := c.Param("repo_id")

	_, flags, err := m.GetLibraryPermissionWithFlags(orgID, userID, repoID)
	if err != nil || flags == nil {
		return false // fail-closed: deny if resolution fails
	}

	c.Set("_perm_flags", flags)
	return flags.HasFlag(flag)
}

// RequirePermFlagForRepo is like RequirePermFlag but accepts an explicit repoID
// (for handlers where repo_id comes from request body instead of URL params)
func (m *PermissionMiddleware) RequirePermFlagForRepo(c *gin.Context, repoID string, flag string) bool {
	if cached, exists := c.Get("_perm_flags"); exists {
		return cached.(*PermissionFlags).HasFlag(flag)
	}

	orgID := c.GetString("org_id")
	userID := c.GetString("user_id")

	_, flags, err := m.GetLibraryPermissionWithFlags(orgID, userID, repoID)
	if err != nil || flags == nil {
		return false // fail-closed: deny if resolution fails
	}

	c.Set("_perm_flags", flags)
	return flags.HasFlag(flag)
}

// CanModifyLibrary checks if user can modify a library (owner, rw, or cloud-edit permission)
func (m *PermissionMiddleware) CanModifyLibrary(orgID, userID, repoID string) (bool, error) {
	permission, err := m.GetLibraryPermission(orgID, userID, repoID)
	if err != nil {
		return false, err
	}

	return permission == PermissionOwner || permission == PermissionAdmin || permission == PermissionRW || permission == PermissionCloudEdit, nil
}

// CanReadLibrary checks if user can read a library
func (m *PermissionMiddleware) CanReadLibrary(orgID, userID, repoID string) (bool, error) {
	permission, err := m.GetLibraryPermission(orgID, userID, repoID)
	if err != nil {
		return false, err
	}

	return permission != PermissionNone, nil
}

// HasLibraryAccess checks if user has at least the specified permission level for a library
// This is the main method used for permission checks throughout the API
func (m *PermissionMiddleware) HasLibraryAccess(orgID, userID, repoID string, requiredPermission LibraryPermission) (bool, error) {
	// Get user's permission level for this library
	permission, err := m.GetLibraryPermission(orgID, userID, repoID)
	if err != nil {
		return false, err
	}

	// Check if user has at least the required permission level
	return m.hasRequiredLibraryPermission(permission, requiredPermission), nil
}

// HasLibraryAccessCtx checks library access, with repo API token support.
// If the request was authenticated via a repo API token, it checks the token's
// scoped repo_id and permission instead of querying ownership/shares.
func (m *PermissionMiddleware) HasLibraryAccessCtx(c interface {
	Get(any) (any, bool)
	GetString(any) string
}, orgID, userID, repoID string, requiredPermission LibraryPermission) (bool, error) {
	if isRepoToken, _ := c.Get("repo_api_token"); isRepoToken == true {
		tokenRepoID := c.GetString("repo_api_token_repo_id")
		tokenPerm := c.GetString("repo_api_token_permission")

		// Token must be for this specific library
		if tokenRepoID != repoID {
			return false, nil
		}

		var libPerm LibraryPermission
		switch tokenPerm {
		case "rw":
			libPerm = PermissionRW
		default:
			libPerm = PermissionR
		}

		return m.hasRequiredLibraryPermission(libPerm, requiredPermission), nil
	}

	return m.HasLibraryAccess(orgID, userID, repoID, requiredPermission)
}

// LibraryWithPermission represents a library along with the user's permission level
type LibraryWithPermission struct {
	LibraryID  uuid.UUID
	Permission LibraryPermission
}

// GetUserLibraries returns all libraries the user has access to (owned + shared)
// This is used for filtering library lists to show only accessible libraries
func (m *PermissionMiddleware) GetUserLibraries(orgID, userID string) ([]LibraryWithPermission, error) {
	librariesMap := make(map[uuid.UUID]LibraryPermission)

	// 1. Get all libraries for the org and filter by owner in application code
	// Note: We query all libraries for the org (indexed by org_id), then filter in Go
	// This avoids ALLOW FILTERING and is acceptable since we're already querying by partition key
	iter := m.db.Session().Query(`
		SELECT library_id, owner_id FROM libraries WHERE org_id = ?
	`, orgID).Iter()

	var libIDStr, ownerIDStr string
	for iter.Scan(&libIDStr, &ownerIDStr) {
		// Filter by owner_id in application code
		if ownerIDStr == userID {
			libID, _ := uuid.Parse(libIDStr)
			librariesMap[libID] = PermissionOwner
		}
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}

	// 2. Get all libraries directly shared with user
	// Note: ALLOW FILTERING used here - acceptable for shares table as it's typically small
	// TODO: Create shares_by_recipient table for better performance at scale
	iter = m.db.Session().Query(`
		SELECT library_id, permission FROM shares
		WHERE shared_to = ? AND shared_to_type = 'user'
		ALLOW FILTERING
	`, userID).Iter()

	var permission string
	for iter.Scan(&libIDStr, &permission) {
		libID, _ := uuid.Parse(libIDStr)
		// If user is owner, keep owner permission (highest)
		if existingPerm, exists := librariesMap[libID]; exists {
			if m.hasRequiredLibraryPermission(LibraryPermission(permission), existingPerm) {
				librariesMap[libID] = LibraryPermission(permission)
			}
		} else {
			librariesMap[libID] = LibraryPermission(permission)
		}
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}

	// 3. Get all groups user is a member of
	// Use groups_by_member table which is indexed by (org_id, user_id)
	groupIter := m.db.Session().Query(`
		SELECT group_id FROM groups_by_member WHERE org_id = ? AND user_id = ?
	`, orgID, userID).Iter()

	var groupIDStr string
	var groupIDs []string

	for groupIter.Scan(&groupIDStr) {
		groupIDs = append(groupIDs, groupIDStr)
	}
	if err := groupIter.Close(); err != nil {
		return nil, err
	}

	// 4. For each group, get libraries shared with that group
	for _, groupID := range groupIDs {
		// Note: ALLOW FILTERING used here - acceptable for shares table as it's typically small
		iter := m.db.Session().Query(`
			SELECT library_id, permission FROM shares
			WHERE shared_to = ? AND shared_to_type = 'group'
			ALLOW FILTERING
		`, groupID).Iter()

		for iter.Scan(&libIDStr, &permission) {
			libID, _ := uuid.Parse(libIDStr)
			// Keep highest permission level
			if existingPerm, exists := librariesMap[libID]; exists {
				if m.hasRequiredLibraryPermission(LibraryPermission(permission), existingPerm) {
					librariesMap[libID] = LibraryPermission(permission)
				}
			} else {
				librariesMap[libID] = LibraryPermission(permission)
			}
		}
		if err := iter.Close(); err != nil {
			return nil, err
		}
	}

	// Convert map to slice
	result := make([]LibraryWithPermission, 0, len(librariesMap))
	for libID, perm := range librariesMap {
		result = append(result, LibraryWithPermission{
			LibraryID:  libID,
			Permission: perm,
		})
	}

	return result, nil
}
