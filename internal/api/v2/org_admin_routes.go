package v2

import (
	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	"github.com/gin-gonic/gin"
)

// RegisterOrgAdminRoutes registers org-admin API routes under /api/v2.1/org.
//
// Route groups:
//   - /org/admin/...          — endpoints that use the JWT org (no :org_id in URL)
//   - /org/:org_id/admin/...  — endpoints that require an explicit org_id parameter
//
// All endpoints require the user to be an org admin of the specified org or a superadmin.
func RegisterOrgAdminRoutes(rg *gin.RouterGroup, database *db.DB, cfg *config.Config, perm *middleware.PermissionMiddleware, sessions SessionInvalidator) {
	h := NewOrgAdminHandler(database, cfg, perm, sessions)
	orgBase := rg.Group("/org")
	orgBase.Use(perm.RequireAdminOrAbove())

	registerOrgAdminSharedRoutes(orgBase.Group("/admin"), h)
	registerOrgAdminScopedRoutes(orgBase.Group("/:org_id/admin"), h)
}

func registerOrgAdminSharedRoutes(noIDGroup *gin.RouterGroup, h *OrgAdminHandler) {
	registerGetWithSlashVariants(noIDGroup, "/info", h.GetOrgInfo)
	registerPutWithSlashVariants(noIDGroup, "/info", h.UpdateOrgInfo)
	registerGetWithSlashVariants(noIDGroup, "/links", h.ListOrgLinks)
	registerDeleteWithSlashVariants(noIDGroup, "/links/:token", h.DeleteOrgLink)
	registerGetWithSlashVariants(noIDGroup, "/upload-links", h.ListOrgUploadLinks)
	registerDeleteWithSlashVariants(noIDGroup, "/upload-links/:token", h.DeleteOrgUploadLink)
	registerGetWithSlashVariants(noIDGroup, "/logs/file-access", h.ListOrgFileAccessLogs)
	registerGetWithSlashVariants(noIDGroup, "/logs/file-update", h.ListOrgFileUpdateLogs)
	registerGetWithSlashVariants(noIDGroup, "/logs/repo-permission", h.ListOrgRepoPermLogs)
}

func registerOrgAdminScopedRoutes(idGroup *gin.RouterGroup, h *OrgAdminHandler) {
	registerGetWithSlashVariants(idGroup, "/links", h.ListOrgLinks)
	registerDeleteWithSlashVariants(idGroup, "/links/:token", h.DeleteOrgLink)
	registerGetWithSlashVariants(idGroup, "/upload-links", h.ListOrgUploadLinks)
	registerDeleteWithSlashVariants(idGroup, "/upload-links/:token", h.DeleteOrgUploadLink)
	registerOrgAdminUserRoutes(idGroup, h)
	registerPutWithSlashVariants(idGroup, "/transfer-ownership", h.TransferOrgOwnership)
	registerOrgAdminGroupRoutes(idGroup, h)
	registerOrgAdminRepoRoutes(idGroup, h)
	registerOrgAdminDepartmentRoutes(idGroup, h)
	registerOrgAdminStatisticsRoutes(idGroup, h)
	registerOrgAdminDeviceRoutes(idGroup, h)
	registerOrgAdminSettingsRoutes(idGroup, h)
}

func registerOrgAdminUserRoutes(idGroup *gin.RouterGroup, h *OrgAdminHandler) {
	registerGetWithSlashVariants(idGroup, "/users", h.ListOrgUsers)
	registerPostWithSlashVariants(idGroup, "/users", h.AddOrgUser)
	registerGetWithSlashVariants(idGroup, "/users/:email", h.GetOrgUser)
	registerPutWithSlashVariants(idGroup, "/users/:email", h.UpdateOrgUser)
	registerDeleteWithSlashVariants(idGroup, "/users/:email", h.DeleteOrgUser)
	registerPutWithSlashVariants(idGroup, "/users/:email/restore", h.RestoreOrgUser)
	registerPutWithSlashVariants(idGroup, "/users/:email/set-password", h.ResetOrgUserPassword)
	registerGetWithSlashVariants(idGroup, "/users/:email/repos", h.GetOrgUserOwnedRepos)
	registerGetWithSlashVariants(idGroup, "/users/:email/beshared-repos", h.GetOrgUserBesharedRepos)
	registerGetWithSlashVariants(idGroup, "/search-user", h.SearchOrgUser)
	registerPostWithSlashVariants(idGroup, "/import-users", h.ImportOrgUsers)
	registerPostWithSlashVariants(idGroup, "/invite-users", h.InviteOrgUsers)
}

func registerOrgAdminGroupRoutes(idGroup *gin.RouterGroup, h *OrgAdminHandler) {
	registerGetWithSlashVariants(idGroup, "/groups", h.ListOrgGroups)
	registerGetWithSlashVariants(idGroup, "/groups/:gid", h.GetOrgGroup)
	registerPutWithSlashVariants(idGroup, "/groups/:gid", h.UpdateOrgGroup)
	registerPutWithSlashVariants(idGroup, "/groups/:gid/transfer", h.TransferOrgGroup)
	registerDeleteWithSlashVariants(idGroup, "/groups/:gid", h.DeleteOrgGroup)
	registerGetWithSlashVariants(idGroup, "/groups/:gid/members", h.ListOrgGroupMembers)
	registerPostWithSlashVariants(idGroup, "/groups/:gid/members", h.AddOrgGroupMember)
	registerDeleteWithSlashVariants(idGroup, "/groups/:gid/members/:email", h.DeleteOrgGroupMember)
	registerPutWithSlashVariants(idGroup, "/groups/:gid/members/:email", h.UpdateOrgGroupMember)
	registerGetWithSlashVariants(idGroup, "/groups/:gid/libraries", h.ListOrgGroupLibraries)
	registerPostWithSlashVariants(idGroup, "/groups/:gid/group-owned-libraries", h.AddOrgGroupOwnedLibrary)
	registerDeleteWithSlashVariants(idGroup, "/groups/:gid/group-owned-libraries/:rid", h.DeleteOrgGroupOwnedLibrary)
	registerGetWithSlashVariants(idGroup, "/search-group", h.SearchOrgGroup)
}

func registerOrgAdminRepoRoutes(idGroup *gin.RouterGroup, h *OrgAdminHandler) {
	registerGetWithSlashVariants(idGroup, "/repos", h.ListOrgRepos)
	registerDeleteWithSlashVariants(idGroup, "/repos/:rid", h.DeleteOrgRepo)
	registerPutWithSlashVariants(idGroup, "/repos/:rid", h.TransferOrgRepo)
	registerGetWithSlashVariants(idGroup, "/repos/:rid/dirents", h.ListOrgRepoDirents)
	registerGetWithSlashVariants(idGroup, "/trash-libraries", h.ListOrgTrashLibraries)
	registerDeleteWithSlashVariants(idGroup, "/trash-libraries", h.CleanOrgTrashLibraries)
	registerDeleteWithSlashVariants(idGroup, "/trash-libraries/:rid", h.DeleteOrgTrashLibrary)
	registerPutWithSlashVariants(idGroup, "/trash-libraries/:rid", h.RestoreOrgTrashLibrary)
}

func registerOrgAdminDepartmentRoutes(idGroup *gin.RouterGroup, h *OrgAdminHandler) {
	registerGetWithSlashVariants(idGroup, "/departments", h.ListOrgDepartments)
	registerGetWithSlashVariants(idGroup, "/address-book/groups", h.ListOrgAddressBookGroups)
	registerPostWithSlashVariants(idGroup, "/address-book/groups", h.AddOrgAddressBookGroup)
	registerGetWithSlashVariants(idGroup, "/address-book/groups/:gid", h.GetOrgAddressBookGroup)
	registerPutWithSlashVariants(idGroup, "/address-book/groups/:gid", h.UpdateOrgAddressBookGroup)
	registerDeleteWithSlashVariants(idGroup, "/address-book/groups/:gid", h.DeleteOrgAddressBookGroup)
}

func registerOrgAdminStatisticsRoutes(idGroup *gin.RouterGroup, h *OrgAdminHandler) {
	registerGetWithSlashVariants(idGroup, "/statistics/file-operations", h.OrgStatisticFiles)
	registerGetWithSlashVariants(idGroup, "/statistics/total-storage", h.OrgStatisticStorage)
	registerGetWithSlashVariants(idGroup, "/statistics/active-users", h.OrgStatisticActiveUsers)
	registerGetWithSlashVariants(idGroup, "/statistics/system-traffic", h.OrgStatisticTraffic)
	registerGetWithSlashVariants(idGroup, "/statistics/user-traffic", h.OrgStatisticUserTraffic)
}

func registerOrgAdminDeviceRoutes(idGroup *gin.RouterGroup, h *OrgAdminHandler) {
	registerGetWithSlashVariants(idGroup, "/devices", h.ListOrgDevices)
	registerDeleteWithSlashVariants(idGroup, "/devices", h.UnlinkOrgDevice)
	registerGetWithSlashVariants(idGroup, "/devices-errors", h.ListOrgDeviceErrors)
	registerDeleteWithSlashVariants(idGroup, "/devices-errors", h.ClearOrgDeviceErrors)
}

func registerOrgAdminSettingsRoutes(idGroup *gin.RouterGroup, h *OrgAdminHandler) {
	registerGetWithSlashVariants(idGroup, "/web-settings", h.GetOrgWebSettings)
	registerPutWithSlashVariants(idGroup, "/web-settings", h.SetOrgWebSettings)
	registerPostWithSlashVariants(idGroup, "/logo", h.UpdateOrgLogo)
	registerGetWithSlashVariants(idGroup, "/saml-config", h.GetOrgSAMLConfig)
	registerPutWithSlashVariants(idGroup, "/saml-config", h.UpdateOrgSAMLConfig)
	registerPutWithSlashVariants(idGroup, "/verify-domain", h.VerifyOrgDomain)
}
