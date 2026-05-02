package v2

import (
	"fmt"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/httputil"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/gin-gonic/gin"
)

func configuredServerURL(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	return cfg.Server.URL
}

func routingHostname(c *gin.Context, cfg *config.Config) string {
	return httputil.GetRoutingHostname(c, configuredServerURL(cfg))
}

func effectiveHostname(c *gin.Context, cfg *config.Config) string {
	return httputil.GetEffectiveHostname(c, configuredServerURL(cfg))
}

func relayPortFromRequest(c *gin.Context, cfg *config.Config) string {
	return httputil.GetRelayPortFromRequest(c, configuredServerURL(cfg))
}

func lookupLibraryStorageClass(database *db.DB, orgID, repoID string) string {
	if database == nil || orgID == "" || repoID == "" {
		return ""
	}

	var storageClass string
	if err := database.Session().Query(`
		SELECT storage_class FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).Scan(&storageClass); err != nil {
		return ""
	}

	return storageClass
}

func resolvePreferredLibraryStorageClassForRequest(c *gin.Context, cfg *config.Config, storageManager *storage.Manager, libraryClass, defaultClass string) string {
	if storageManager != nil {
		hostname := routingHostname(c, cfg)
		return storageManager.ResolveStorageClass(hostname, libraryClass, "hot")
	}

	if libraryClass != "" {
		return libraryClass
	}

	return defaultClass
}

func resolveLibraryBlockStoreForRequest(c *gin.Context, database *db.DB, cfg *config.Config, storageManager *storage.Manager, s3Store *storage.S3Store, orgID, repoID string) (*storage.BlockStore, string, error) {
	libraryClass := lookupLibraryStorageClass(database, orgID, repoID)
	defaultClass := ""
	if cfg != nil {
		defaultClass = cfg.Storage.DefaultClass
	}

	preferredClass := resolvePreferredLibraryStorageClassForRequest(c, cfg, storageManager, libraryClass, defaultClass)
	if storageManager != nil {
		return storageManager.GetHealthyBlockStore(preferredClass)
	}
	if s3Store != nil {
		return storage.NewBlockStore(s3Store, "blocks/"), preferredClass, nil
	}

	return nil, preferredClass, fmt.Errorf("block storage not available")
}
