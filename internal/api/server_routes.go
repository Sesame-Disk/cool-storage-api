package api

import (
	"net/http"
	"os"
	"strings"

	v2 "github.com/Sesame-Disk/sesamefs/internal/api/v2"
	"github.com/Sesame-Disk/sesamefs/internal/health"
	"github.com/Sesame-Disk/sesamefs/internal/traffic"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func (s *Server) setupRuntimeHooks() {
	// Set up GC hooks so that v2 handlers can enqueue blocks/libraries for garbage collection.
	if s.gcService != nil {
		v2.SetGCHooks(
			&gcBlockEnqueuer{service: s.gcService},
			&gcLibraryEnqueuer{service: s.gcService},
			&gcCommitEnqueuer{service: s.gcService},
		)
	}

	// Initialise the global traffic recorder and quota checker.
	traffic.SetRecorder(traffic.NewRecorder(s.db.Session()))
	traffic.SetChecker(traffic.NewChecker(s.db.Session()))
}

func (s *Server) registerCoreRoutes() {
	// Health check endpoints.
	s.router.GET("/ping", s.handlePing)

	var storageChecker health.StorageChecker
	if s.storage != nil {
		storageChecker = s.storage
	}
	healthChecker := health.NewChecker(s.db, storageChecker, s.config.Monitoring.HealthTimeout, s.version)
	s.router.GET("/health", healthChecker.HandleLiveness)
	s.router.GET("/ready", healthChecker.HandleReadiness)

	if s.config.Monitoring.MetricsEnabled {
		s.router.GET(s.config.Monitoring.MetricsPath, gin.WrapH(promhttp.Handler()))
	}

	// Logout endpoint - clears token and redirects to home.
	s.router.GET("/accounts/logout", s.handleLogout)
	s.router.GET("/accounts/logout/", s.handleLogout)

	// Auto-login endpoint for desktop client "View on Cloud" feature.
	s.router.GET("/client-login", s.handleAutoLogin)
	s.router.GET("/client-login/", s.handleAutoLogin)

	// OAuth/OIDC server-side endpoints for the Seafile desktop client SSO flow.
	s.router.GET("/oauth/login", s.handleOAuthLogin)
	s.router.GET("/oauth/login/", s.handleOAuthLogin)
	s.router.GET("/client-sso", s.handleOAuthLogin)
	s.router.GET("/client-sso/", s.handleOAuthLogin)
	s.router.GET("/client-sso/:token", s.handleOAuthLogin)
	s.router.GET("/client-sso/:token/", s.handleOAuthLogin)

	oauthRL := s.authRateLimiter.Limit()
	s.router.GET("/oauth/callback", oauthRL, s.handleOAuthCallback)
	s.router.GET("/oauth/callback/", oauthRL, s.handleOAuthCallback)
}

func (s *Server) resolveServerURL() string {
	// FILE_SERVER_ROOT takes highest priority (like Seahub's FILE_SERVER_ROOT setting),
	// SERVER_URL is second priority, and request-host auto-detection is the fallback.
	serverURL := os.Getenv("FILE_SERVER_ROOT")
	if serverURL != "" {
		serverURL = strings.TrimSuffix(serverURL, "/seafhttp")
		return strings.TrimSuffix(serverURL, "/")
	}

	if serverURL = os.Getenv("SERVER_URL"); serverURL != "" {
		return strings.TrimSuffix(serverURL, "/")
	}

	return ""
}

func (s *Server) registerAPIV2Routes(serverURL string) {
	apiV2 := s.router.Group("/api/v2")
	apiV2.GET("/ping", s.handlePing)
	v2.RegisterAuthRoutes(apiV2, s.db, s.config, s.authRateLimiter.Limit())

	protected := apiV2.Group("")
	protected.Use(s.authMiddleware())

	v2.RegisterLibraryRoutesWithToken(protected, s.db, s.config, s.tokenStore)
	v2.RegisterFileRoutes(protected, s.db, s.config, s.storage, s.tokenStore, serverURL)
	if s.blockStore != nil || s.storageManager != nil {
		v2.RegisterBlockRoutes(protected, s.blockStore, s.storageManager, s.config)
	}
	v2.RegisterShareRoutes(protected, s.db, s.config)
	v2.RegisterRestoreRoutes(protected, s.db, s.config)
}

func (s *Server) registerLegacyAPIV2Routes(serverURL string) {
	api2 := s.router.Group("/api2")
	authRL := s.authRateLimiter.Limit()

	api2.POST("/auth-token", authRL, s.handleAuthToken)
	api2.POST("/client-sso-link", authRL, s.handleClientSSOLink)
	api2.POST("/client-sso-link/", authRL, s.handleClientSSOLink)
	api2.GET("/client-sso-link", s.handleGetClientSSOLink)
	api2.GET("/client-sso-link/", s.handleGetClientSSOLink)
	api2.GET("/client-sso-link/:token", s.handleGetClientSSOLink)
	api2.GET("/client-sso-link/:token/", s.handleGetClientSSOLink)
	api2.POST("/client-login", s.handleClientLogin)
	api2.GET("/ping", s.handlePing)
	api2.GET("/server-info", s.handleServerInfo)
	api2.GET("/server-info/", s.handleServerInfo)
	api2.GET("/auth/ping", s.authMiddleware(), s.handlePing)
	api2.GET("/auth/ping/", s.authMiddleware(), s.handlePing)
	api2.GET("/account/info", s.authMiddleware(), s.handleAccountInfo)
	v2.RegisterStarredRoutes(api2.Group("", s.authMiddleware()), s.db)
	api2.GET("/avatars/user/:email/resized/:size", s.handleUserAvatar)
	api2.GET("/avatars/user/:email/resized/:size/", s.handleUserAvatar)

	protected := api2.Group("")
	protected.Use(s.authMiddleware())

	v2.RegisterLibraryRoutesWithToken(protected, s.db, s.config, s.tokenStore)
	v2.RegisterFileRoutes(protected, s.db, s.config, s.storage, s.tokenStore, serverURL)
	v2.RegisterFileShareRoutes(protected, s.db, s.permMiddleware)
	protected.GET("/search-user", s.handleSearchUser)
	protected.GET("/search-user/", s.handleSearchUser)
	v2.RegisterSearchRoutes(protected, s.db)
	v2.RegisterTrashRoutes(protected, s.db)
	v2.RegisterDeletedLibraryRoutes(protected, s.db, nil)
	protected.GET("/repo-tokens", s.handleRepoTokens)
	protected.GET("/default-repo", s.handleDefaultRepo)
	protected.GET("/default-repo/", s.handleDefaultRepo)
	protected.POST("/default-repo", s.handleDefaultRepo)
	protected.POST("/default-repo/", s.handleDefaultRepo)
	v2.RegisterHistoryLimitRoutes(protected, s.db, s.config)
	v2.RegisterLibraryTransferRoutes(protected, s.db, s.config)
	protected.GET("/devices", s.handleEmptyDevices)
	protected.GET("/devices/", s.handleEmptyDevices)
}

func (s *Server) registerAPIV21Routes(serverURL string) {
	apiV21 := s.router.Group("/api/v2.1")
	v2.RegisterAuthRoutes(apiV21, s.db, s.config, s.authRateLimiter.Limit())
	apiV21.GET("/bootstrap/", s.handleBootstrap)
	apiV21.GET("/bootstrap", s.handleBootstrap)

	protected := apiV21.Group("")
	protected.Use(s.authMiddleware())

	var sessionInvalidator v2.SessionInvalidator
	if s.authHandler != nil {
		sessionInvalidator = s.authHandler.GetSessionManager()
	}

	v2.RegisterAdminRoutes(protected, s.db, s.config, s.permMiddleware, s.tokenStore, sessionInvalidator, serverURL)
	v2.RegisterOrgAdminRoutes(protected, s.db, s.config, s.permMiddleware, sessionInvalidator)
	if s.gcService != nil {
		gcAdmin := protected.Group("/admin", s.permMiddleware.RequireSuperAdmin())
		gcAdmin.GET("/gc/status", s.handleGCStatus)
		gcAdmin.GET("/gc/status/", s.handleGCStatus)
		gcAdmin.POST("/gc/run", s.handleGCRun)
		gcAdmin.POST("/gc/run/", s.handleGCRun)
	}

	v2.RegisterV21LibraryRoutes(protected, s.db, s.config, s.tokenStore, s.storage, s.blockStore, serverURL)

	fileHandler := v2.NewFileHandler(s.db, s.config, s.storage, s.tokenStore, serverURL, s.permMiddleware)
	fileHandler.SetGCEnqueuer(v2.GetBlockEnqueuerFunc())
	protected.DELETE("/repos/batch-delete-item/", fileHandler.BatchDeleteItems)
	protected.DELETE("/repos/batch-delete-item", fileHandler.BatchDeleteItems)
	protected.GET("/repos/:repo_id/file/new_history/", fileHandler.GetFileHistoryV21)
	protected.GET("/repos/:repo_id/file/new_history", fileHandler.GetFileHistoryV21)
	protected.GET("/repos/:repo_id/file/history/", fileHandler.GetFileHistoryV21)
	protected.GET("/repos/:repo_id/file/history", fileHandler.GetFileHistoryV21)
	protected.GET("/repos/:repo_id/history/", fileHandler.GetRepoHistory)
	protected.GET("/repos/:repo_id/history", fileHandler.GetRepoHistory)
	v2.RegisterBatchOperationRoutes(protected, s.db, s.config)
	v2.RegisterOnlyOfficeRoutes(protected, s.db, s.config, s.storage, s.tokenStore, serverURL)
	v2.RegisterV21StarredRoutes(protected, s.db)
	v2.RegisterShareLinkRoutes(protected, s.db, serverURL, s.permMiddleware, s.config)
	v2.RegisterUploadLinkRoutes(protected, s.db, serverURL, s.permMiddleware, s.config)
	v2.RegisterGroupRoutes(protected, s.db, s.config)
	v2.RegisterShareableGroupRoutes(protected, s.db, s.config)
	v2.RegisterMonitoredRepoRoutes(protected, s.db)
	v2.RegisterSearchRoutes(protected, s.db)

	protected.GET("/activities", s.handleEmptyActivities)
	protected.GET("/activities/", s.handleEmptyActivities)
	protected.GET("/notifications", s.handleEmptyNotifications)
	protected.GET("/notifications/", s.handleEmptyNotifications)
	v21FileShareHandler := v2.NewFileShareHandler(s.db, s.permMiddleware)
	protected.GET("/shared-repos", v21FileShareHandler.ListSharedRepos)
	protected.GET("/shared-repos/", v21FileShareHandler.ListSharedRepos)
	protected.GET("/shared-folders", s.handleEmptySharedFolders)
	protected.GET("/shared-folders/", s.handleEmptySharedFolders)
	protected.GET("/wikis", s.handleEmptyWikis)
	protected.GET("/wikis/", s.handleEmptyWikis)
	protected.GET("/repo-folder-share-info", s.handleEmptyFolderShareInfo)
	protected.GET("/repo-folder-share-info/", s.handleEmptyFolderShareInfo)
	protected.GET("/search-user", s.handleSearchUser)
	protected.GET("/search-user/", s.handleSearchUser)
	v2.RegisterDepartmentRoutes(protected, s.db, s.permMiddleware)
	v2.RegisterV21LibrarySettingsRoutes(protected, s.db, s.config)
	protected.GET("/smart-link", fileHandler.GetSmartLink)
	protected.GET("/smart-link/", fileHandler.GetSmartLink)
	protected.GET("/smart-link/:token", fileHandler.ResolveSmartLink)
	protected.GET("/smart-link/:token/", fileHandler.ResolveSmartLink)
	v2.RegisterTagRoutes(protected, s.db)
	protected.GET("/subscription/", s.handleGetSubscription)
	protected.GET("/subscription", s.handleGetSubscription)
}

func (s *Server) registerPublicRoutes(serverURL string) {
	onlyoffice := s.router.Group("/onlyoffice")
	v2.RegisterOnlyOfficeCallbackRoutes(onlyoffice, s.db, s.config, s.storage, serverURL)

	exportHandler := v2.NewShareLinkHandler(s.db, serverURL, s.permMiddleware, s.config)
	s.router.GET("/share/link/export-excel/", s.authMiddleware(), exportHandler.ExportShareLinksExcel)
	s.router.GET("/share/link/export-excel", s.authMiddleware(), exportHandler.ExportShareLinksExcel)

	smartLinkHandler := v2.NewFileHandler(s.db, s.config, s.storage, s.tokenStore, serverURL, s.permMiddleware)
	s.router.GET("/smart-link/:token", s.smartLinkAuthMiddleware(), smartLinkHandler.ResolveSmartLink)
	s.router.GET("/smart-link/:token/", s.smartLinkAuthMiddleware(), smartLinkHandler.ResolveSmartLink)

	s.router.GET("/billing", s.handleBillingRedirect)
	s.router.GET("/billing/", s.handleBillingRedirect)

	slv := v2.NewShareLinkViewHandler(s.db, s.config, s.storage, s.storageManager, s.tokenStore, serverURL)
	s.router.GET("/d/:token", slv.ServeShareLinkPage)
	s.router.GET("/d/:token/files/", slv.ServeShareLinkFilePage)
	s.router.GET("/d/:token/files", slv.ServeShareLinkFilePage)
	s.router.POST("/api/v2.1/public-links/:token/check-password/", slv.CheckPublicLinkPassword)
	s.router.POST("/api/v2.1/public-links/:token/check-password", slv.CheckPublicLinkPassword)
	s.router.GET("/api/v2.1/share-links/:token/bootstrap/", slv.GetShareLinkBootstrap)
	s.router.GET("/api/v2.1/share-links/:token/bootstrap", slv.GetShareLinkBootstrap)
	s.router.GET("/api/v2.1/share-links/:token/files/bootstrap/", slv.GetShareLinkFileBootstrap)
	s.router.GET("/api/v2.1/share-links/:token/files/bootstrap", slv.GetShareLinkFileBootstrap)
	s.router.GET("/api/v2.1/share-links/:token/dirents/", slv.ListShareLinkDirents)
	s.router.GET("/api/v2.1/share-links/:token/dirents", slv.ListShareLinkDirents)
	s.router.GET("/api/v2.1/share-link-zip-task/", slv.GetShareLinkZipTask)
	s.router.GET("/api/v2.1/share-link-zip-task", slv.GetShareLinkZipTask)

	zipProgressStub := func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"zipped": 1,
			"total":  1,
			"failed": 0,
		})
	}
	s.router.GET("/api/v2.1/query-zip-progress/", zipProgressStub)
	s.router.GET("/api/v2.1/query-zip-progress", zipProgressStub)

	cancelZipStub := func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"success": true})
	}
	s.router.DELETE("/api/v2.1/cancel-zip-task/", cancelZipStub)
	s.router.DELETE("/api/v2.1/cancel-zip-task", cancelZipStub)

	s.router.GET("/api/v2.1/upload-links/:token/bootstrap/", slv.GetUploadLinkBootstrap)
	s.router.GET("/api/v2.1/upload-links/:token/bootstrap", slv.GetUploadLinkBootstrap)
	s.router.GET("/api/v2.1/upload-links/:token/upload/", slv.GetUploadLinkUploadURL)
	s.router.GET("/api/v2.1/upload-links/:token/upload", slv.GetUploadLinkUploadURL)
	s.router.POST("/api/v2.1/upload-links/:token/upload-done/", slv.PostUploadLinkDone)
	s.router.POST("/api/v2.1/upload-links/:token/upload-done", slv.PostUploadLinkDone)
	s.router.GET("/api/v2.1/share-links/:token/upload/", slv.GetShareLinkUploadURL)
	s.router.GET("/api/v2.1/share-links/:token/upload", slv.GetShareLinkUploadURL)
	s.router.POST("/api/v2.1/share-links/:token/upload-done/", slv.PostShareLinkUploadDone)
	s.router.POST("/api/v2.1/share-links/:token/upload-done", slv.PostShareLinkUploadDone)
	s.router.GET("/api/v2.1/share-links/:token/repo-tags/", slv.GetShareLinkRepoTags)
	s.router.GET("/api/v2.1/share-links/:token/repo-tags", slv.GetShareLinkRepoTags)

	officeConvertStub := func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ERROR"})
	}
	s.router.GET("/office-convert/status/", officeConvertStub)
	s.router.GET("/office-convert/status", officeConvertStub)
}

func (s *Server) registerCompatibilityRoutes(serverURL string) {
	v2.RegisterFileViewRoutes(s.router, s.db, s.config, s.storage, s.storageManager, s.tokenStore, serverURL, s.authMiddleware(), s.permMiddleware)

	seafHTTPHandler := NewSeafHTTPHandler(s.storage, s.storageManager, s.db, s.tokenStore, s.permMiddleware)
	seafHTTPHandler.RegisterSeafHTTPRoutes(s.router)

	syncHandler := NewSyncHandler(s.db, s.storage, s.blockStore, s.storageManager, s.permMiddleware)
	syncHandler.SetTokenCreator(s.tokenStore)
	syncHandler.RegisterSyncRoutes(s.router, s.syncAuthMiddleware())
}
