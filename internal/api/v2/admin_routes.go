package v2

import (
	"github.com/Sesame-Disk/sesamefs/internal/apikeys"
	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	"github.com/gin-gonic/gin"
)

// RegisterAdminRoutes registers platform-admin API routes under the given router group.
// All /admin endpoints are reserved for platform superadmins.
func RegisterAdminRoutes(rg *gin.RouterGroup, database *db.DB, cfg *config.Config, perm *middleware.PermissionMiddleware, tokenCreator TokenCreator, sessions SessionInvalidator, apiKeys APIKeyInvalidator, serverURL string) {
	h := NewAdminHandler(database, cfg, perm, tokenCreator, sessions, apiKeys, serverURL)
	admin := rg.Group("/admin", perm.RequireSuperAdmin(), apikeys.RequireScope(apikeys.ScopeAdmin))
	superadminOnly := admin.Group("", perm.RequireSuperAdmin())

	registerAdminOrganizationRoutes(admin, superadminOnly, h)
	registerAdminUserRoutes(admin, h)
	registerAdminGroupRoutes(admin, h)
	registerAdminLibraryRoutes(admin, h)
	registerAdminSystemRoutes(admin, h)
	registerAdminLogRoutes(admin, h)
	registerAdminLinkRoutes(admin, h)
	registerAdminInstitutionRoutes(admin, h)
	registerAdminDepartmentRoutes(admin, h)
}

func registerAdminOrganizationRoutes(admin, superadminOnly *gin.RouterGroup, h *AdminHandler) {
	registerGetWithSlashVariants(admin, "/organizations", h.ListOrganizations)
	registerGetWithSlashVariants(admin, "/organizations/:org_id", h.GetOrganization)

	registerPostWithSlashVariants(superadminOnly, "/organizations", h.CreateOrganization)
	registerPutWithSlashVariants(superadminOnly, "/organizations/:org_id", h.UpdateOrganization)
	registerDeleteWithSlashVariants(superadminOnly, "/organizations/:org_id", h.SoftDeleteOrganization)
	registerPostWithSlashVariants(superadminOnly, "/organizations/:org_id/delete", h.SoftDeleteOrganization)
	registerPostWithSlashVariants(superadminOnly, "/organizations/:org_id/deactivate", h.DeactivateOrganization)
	registerPostWithSlashVariants(superadminOnly, "/organizations/:org_id/reactivate", h.ReactivateOrganization)
	registerPostWithSlashVariants(superadminOnly, "/organizations/:org_id/restore", h.RestoreOrganization)

	registerGetWithSlashVariants(admin, "/organizations/:org_id/users", h.ListOrgUsers)
	registerPostWithSlashVariants(admin, "/organizations/:org_id/users", h.AdminAddOrgUser)
	registerPutWithSlashVariants(admin, "/organizations/:org_id/users/:email", h.AdminUpdateOrgUser)
	registerDeleteWithSlashVariants(admin, "/organizations/:org_id/users/:email", h.AdminDeleteOrgUser)
	registerGetWithSlashVariants(admin, "/organizations/:org_id/groups", h.AdminListOrgGroups)
	registerGetWithSlashVariants(admin, "/search-organization", h.AdminSearchOrganizations)
}

func registerAdminUserRoutes(admin *gin.RouterGroup, h *AdminHandler) {
	admin.Any("/users", h.adminUsersHandler)
	admin.Any("/users/*path", h.adminUsersHandler)
	registerGetWithSlashVariants(admin, "/search-user", h.SearchUsers)
	registerGetWithSlashVariants(admin, "/admins", h.ListAdminUsers)
	registerPostWithSlashVariants(admin, "/admins", h.BatchAddAdmins)
}

func registerAdminGroupRoutes(admin *gin.RouterGroup, h *AdminHandler) {
	registerGetWithSlashVariants(admin, "/groups", h.ListAllGroups)
	registerPostWithSlashVariants(admin, "/groups", h.AdminCreateGroup)
	registerDeleteWithSlashVariants(admin, "/groups/:group_id", h.AdminDeleteGroup)
	registerPutWithSlashVariants(admin, "/groups/:group_id", h.AdminTransferGroup)
	registerGetWithSlashVariants(admin, "/groups/:group_id/members", h.AdminListGroupMembers)
	registerPostWithSlashVariants(admin, "/groups/:group_id/members", h.AdminAddGroupMember)
	registerDeleteWithSlashVariants(admin, "/groups/:group_id/members/:email", h.AdminRemoveGroupMember)
	registerPutWithSlashVariants(admin, "/groups/:group_id/members/:email", h.AdminUpdateGroupMemberRole)
	registerGetWithSlashVariants(admin, "/groups/:group_id/libraries", h.AdminListGroupLibraries)
	registerPostWithSlashVariants(admin, "/groups/:group_id/group-owned-libraries", h.AdminAddGroupOwnedLibrary)
	registerDeleteWithSlashVariants(admin, "/groups/:group_id/group-owned-libraries/:library_id", h.AdminDeleteGroupOwnedLibrary)
	registerGetWithSlashVariants(admin, "/search-group", h.SearchGroups)
}

func registerAdminLibraryRoutes(admin *gin.RouterGroup, h *AdminHandler) {
	registerGetWithSlashVariants(admin, "/libraries", h.AdminListAllLibraries)
	registerPostWithSlashVariants(admin, "/libraries", h.AdminCreateLibrary)
	registerGetWithSlashVariants(admin, "/libraries/:library_id", h.AdminGetLibrary)
	registerDeleteWithSlashVariants(admin, "/libraries/:library_id", h.AdminDeleteLibrary)
	registerPutWithSlashVariants(admin, "/libraries/:library_id/transfer", h.AdminTransferLibrary)
	registerGetWithSlashVariants(admin, "/libraries/:library_id/dirents", h.AdminListDirents)
	registerGetWithSlashVariants(admin, "/libraries/:library_id/download-link", h.AdminGetDownloadLink)
	registerGetWithSlashVariants(admin, "/libraries/:library_id/history-setting", h.AdminGetHistorySetting)
	registerPutWithSlashVariants(admin, "/libraries/:library_id/history-setting", h.AdminUpdateHistorySetting)
	registerGetWithSlashVariants(admin, "/libraries/:library_id/shared-items", h.AdminListSharedItems)
	registerGetWithSlashVariants(admin, "/search-libraries", h.AdminSearchLibraries)
	registerGetWithSlashVariants(admin, "/trash-libraries", h.AdminListTrashLibraries)
	registerDeleteWithSlashVariants(admin, "/trash-libraries", h.AdminCleanTrashLibraries)
}

func registerAdminSystemRoutes(admin *gin.RouterGroup, h *AdminHandler) {
	registerGetWithSlashVariants(admin, "/sysinfo", h.AdminGetSysInfo)
	registerPostWithSlashVariants(admin, "/license", h.AdminUploadLicense)
	registerGetWithSlashVariants(admin, "/statistics/file-operations", h.AdminStatisticFiles)
	registerGetWithSlashVariants(admin, "/statistics/total-storage", h.AdminStatisticStorage)
	registerGetWithSlashVariants(admin, "/statistics/active-users", h.AdminStatisticActiveUsers)
	registerGetWithSlashVariants(admin, "/statistics/system-traffic", h.AdminStatisticTraffic)
	registerGetWithSlashVariants(admin, "/statistics/org-traffic", h.AdminListOrgTraffic)
	registerGetWithSlashVariants(admin, "/statistics/user-traffic", h.AdminListUserTraffic)
	registerGetWithSlashVariants(admin, "/devices", h.AdminListDevices)
	registerGetWithSlashVariants(admin, "/device-errors", h.AdminListDeviceErrors)
	registerDeleteWithSlashVariants(admin, "/device-errors", h.AdminClearDeviceErrors)
	registerGetWithSlashVariants(admin, "/web-settings", h.AdminGetWebSettings)
	registerPutWithSlashVariants(admin, "/web-settings", h.AdminSetWebSettings)
	registerPostWithSlashVariants(admin, "/logo", h.AdminUpdateLogo)
	registerPostWithSlashVariants(admin, "/favicon", h.AdminUpdateFavicon)
	registerPostWithSlashVariants(admin, "/login-background-image", h.AdminUpdateLoginBG)
}

func registerAdminLogRoutes(admin *gin.RouterGroup, h *AdminHandler) {
	registerGetWithSlashVariants(admin, "/logs/login-logs", h.AdminListLoginLogs)
	registerGetWithSlashVariants(admin, "/logs/file-access-logs", h.AdminListFileAccessLogs)
	registerGetWithSlashVariants(admin, "/logs/file-update-logs", h.AdminListFileUpdateLogs)
	registerGetWithSlashVariants(admin, "/logs/share-permission-logs", h.AdminListSharePermissionLogs)
	registerGetWithSlashVariants(admin, "/admin-logs", h.AdminListAdminLogs)
	registerGetWithSlashVariants(admin, "/admin-login-logs", h.AdminListAdminLoginLogs)
}

func registerAdminLinkRoutes(admin *gin.RouterGroup, h *AdminHandler) {
	registerGetWithSlashVariants(admin, "/share-links", h.AdminListShareLinks)
	registerDeleteWithSlashVariants(admin, "/share-links/:token", h.AdminDeleteShareLink)
	registerPutWithSlashVariants(admin, "/share-links/:token/active", h.AdminSetShareLinkActive)
	registerGetWithSlashVariants(admin, "/upload-links", h.AdminListUploadLinks)
	registerDeleteWithSlashVariants(admin, "/upload-links/:token", h.AdminDeleteUploadLink)
	registerPutWithSlashVariants(admin, "/upload-links/:token/active", h.AdminSetUploadLinkActive)
	registerGetWithSlashVariants(admin, "/sys-notifications", h.AdminListSysNotifications)
	registerPostWithSlashVariants(admin, "/sys-notifications", h.AdminAddSysNotification)
	registerPutWithSlashVariants(admin, "/sys-notifications/:id", h.AdminUpdateSysNotification)
	registerDeleteWithSlashVariants(admin, "/sys-notifications/:id", h.AdminDeleteSysNotification)
	registerGetWithSlashVariants(admin, "/invitations", h.AdminListInvitations)
	registerDeleteWithSlashVariants(admin, "/invitations/:token", h.AdminDeleteInvitation)
}

func registerAdminInstitutionRoutes(admin *gin.RouterGroup, h *AdminHandler) {
	registerGetWithSlashVariants(admin, "/institutions", h.AdminListInstitutions)
	registerPostWithSlashVariants(admin, "/institutions", h.AdminAddInstitution)
	registerGetWithSlashVariants(admin, "/institutions/:id", h.AdminGetInstitution)
	registerPutWithSlashVariants(admin, "/institutions/:id", h.AdminUpdateInstitution)
	registerDeleteWithSlashVariants(admin, "/institutions/:id", h.AdminDeleteInstitution)
}

func registerAdminDepartmentRoutes(admin *gin.RouterGroup, h *AdminHandler) {
	registerGetWithSlashVariants(admin, "/organizations/:org_id/departments", h.AdminListOrgDepartments)
}
