package middleware

// IsPlatformSuperAdmin returns true only for users that both belong to the
// platform organization and hold the superadmin role.
func IsPlatformSuperAdmin(orgID string, role OrganizationRole) bool {
	return orgID == PlatformOrgID && role == RoleSuperAdmin
}
