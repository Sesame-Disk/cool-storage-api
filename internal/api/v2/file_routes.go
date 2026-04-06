package v2

import (
	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/middleware"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/gin-gonic/gin"
)

// RegisterFileRoutes registers file routes.

func RegisterFileRoutes(rg *gin.RouterGroup, database *db.DB, cfg *config.Config, s3Store *storage.S3Store, blockStore *storage.BlockStore, storageManager *storage.Manager, tokenCreator TokenCreator, serverURL string) {
	h := newRouteFileHandler(database, cfg, s3Store, blockStore, storageManager, tokenCreator, serverURL)

	registerRepoFileRoutes(rg.Group("/repos/:repo_id"), h)
	registerFileRevisionRoutes(rg.Group("/repo"), h)
}

func newRouteFileHandler(database *db.DB, cfg *config.Config, s3Store *storage.S3Store, blockStore *storage.BlockStore, storageManager *storage.Manager, tokenCreator TokenCreator, serverURL string) *FileHandler {
	permMiddleware := middleware.NewPermissionMiddleware(database)
	h := NewFileHandler(database, cfg, s3Store, blockStore, storageManager, tokenCreator, serverURL, permMiddleware)
	h.SetGCEnqueuer(getBlockEnqueuer())
	return h
}

func registerRepoFileRoutes(repos *gin.RouterGroup, h *FileHandler) {
	registerGetWithSlashVariants(repos, "/dir", h.ListDirectory)
	registerPostWithSlashVariants(repos, "/dir", h.DirectoryOperation)
	registerDeleteWithSlashVariants(repos, "/dir", h.DeleteDirectory)

	registerGetWithSlashVariants(repos, "/file", h.GetFileInfo)
	registerGetWithSlashVariants(repos, "/file/detail", h.GetFileDetail)
	registerPostWithSlashVariants(repos, "/file", h.FileOperation)
	registerDeleteWithSlashVariants(repos, "/file", h.DeleteFile)
	repos.POST("/file/move", h.MoveFile)
	repos.POST("/file/copy", h.CopyFile)

	registerGetWithSlashVariants(repos, "/file/download-link", h.GetDownloadLink)
	registerGetWithSlashVariants(repos, "/upload-link", h.GetUploadLink)
	registerGetWithSlashVariants(repos, "/update-link", h.GetUploadLink)

	registerPostWithSlashVariants(repos, "/upload", h.UploadFile)
	registerGetWithSlashVariants(repos, "/download-info", h.GetDownloadInfo)
	registerGetWithSlashVariants(repos, "/file-uploaded-bytes", h.GetFileUploadedBytes)
}

func registerFileRevisionRoutes(repo *gin.RouterGroup, h *FileHandler) {
	registerGetWithSlashVariants(repo, "/file_revisions/:repo_id", h.GetFileRevisions)
}
