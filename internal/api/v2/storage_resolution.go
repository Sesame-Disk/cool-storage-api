package v2

import (
	"context"
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

var lookupLibraryStorageClassContextFn = lookupLibraryStorageClassContext

func lookupLibraryStorageClass(database *db.DB, orgID, repoID string) (string, error) {
	return lookupLibraryStorageClassContextFn(context.Background(), database, orgID, repoID)
}

func lookupLibraryStorageClassContext(ctx context.Context, database *db.DB, orgID, repoID string) (string, error) {
	if database == nil || orgID == "" || repoID == "" {
		return "", nil
	}
	if database.Session() == nil {
		return "", fmt.Errorf("lookup library storage class: database session unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	var storageClass string
	if err := database.Session().Query(`
		SELECT storage_class FROM libraries WHERE org_id = ? AND library_id = ?
	`, orgID, repoID).WithContext(ctx).Scan(&storageClass); err != nil {
		return "", fmt.Errorf("lookup library storage class: %w", err)
	}

	return storageClass, nil
}

func resolvePreferredLibraryStorageClassForRequest(c *gin.Context, cfg *config.Config, storageManager *storage.Manager, libraryClass, defaultClass string) (string, error) {
	if storageManager != nil {
		hostname := routingHostname(c, cfg)
		return storageManager.ResolveStorageClass(hostname, libraryClass, "hot")
	}

	if libraryClass != "" {
		return libraryClass, nil
	}

	return defaultClass, nil
}

func resolveLibraryBlockStoreForRequest(c *gin.Context, database *db.DB, cfg *config.Config, storageManager *storage.Manager, s3Store *storage.S3Store, orgID, repoID string) (*storage.BlockStore, string, error) {
	return resolveLibraryBlockStoreForRequestContext(context.Background(), c, database, cfg, storageManager, s3Store, orgID, repoID)
}

func resolveLibraryBlockStoreForRequestContext(ctx context.Context, c *gin.Context, database *db.DB, cfg *config.Config, storageManager *storage.Manager, s3Store *storage.S3Store, orgID, repoID string) (*storage.BlockStore, string, error) {
	libraryClass, err := lookupLibraryStorageClassContextFn(ctx, database, orgID, repoID)
	if err != nil {
		return nil, "", err
	}
	defaultClass := ""
	if cfg != nil {
		defaultClass = cfg.Storage.DefaultClass
	}

	preferredClass, err := resolvePreferredLibraryStorageClassForRequest(c, cfg, storageManager, libraryClass, defaultClass)
	if err != nil {
		return nil, libraryClass, err
	}
	if storageManager != nil {
		return storageManager.GetHealthyBlockStoreForOrg(orgID, preferredClass)
	}
	if s3Store != nil {
		bs, err := storage.NewOrgBlockStore(s3Store, "blocks/", orgID)
		if err != nil {
			return nil, preferredClass, err
		}
		return bs, preferredClass, nil
	}

	return nil, preferredClass, fmt.Errorf("block storage not available")
}
