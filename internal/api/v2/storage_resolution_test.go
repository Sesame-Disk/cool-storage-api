package v2

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/gin-gonic/gin"
)

func TestResolvePreferredLibraryStorageClassForRequestUsesLibraryOverride(t *testing.T) {
	manager := storage.NewManager()
	manager.SetDefaultClass("hot-minio-local")
	manager.SetEndpointRegions(map[string]string{"eu.sesamefs.local": "eu"})
	manager.SetRegionClasses(map[string]storage.RegionClassConfig{
		"eu": {Hot: "hot-s3-eu"},
	})
	manager.RegisterBackend("hot-s3-eu", &storage.S3Store{}, "")
	manager.RegisterBackend("hot-s3-usa", &storage.S3Store{}, "")

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/repo/repo-id/raw/file.txt", nil)
	c.Request.Host = "eu.sesamefs.local"

	got, err := resolvePreferredLibraryStorageClassForRequest(c, nil, manager, "hot-s3-usa", "")
	if err != nil {
		t.Fatalf("resolvePreferredLibraryStorageClassForRequest returned error: %v", err)
	}
	if got != "hot-s3-usa" {
		t.Fatalf("resolvePreferredLibraryStorageClassForRequest = %q, want %q", got, "hot-s3-usa")
	}
}

func TestResolvePreferredLibraryStorageClassForRequestUsesEndpointRoutingFallback(t *testing.T) {
	manager := storage.NewManager()
	manager.SetDefaultClass("hot-minio-local")
	manager.SetEndpointRegions(map[string]string{"eu.sesamefs.local": "eu"})
	manager.SetRegionClasses(map[string]storage.RegionClassConfig{
		"eu": {Hot: "hot-s3-eu"},
	})
	manager.RegisterBackend("hot-s3-eu", &storage.S3Store{}, "")

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/repo/repo-id/raw/file.txt", nil)
	c.Request.Host = "eu.sesamefs.local"

	got, err := resolvePreferredLibraryStorageClassForRequest(c, nil, manager, "", "")
	if err != nil {
		t.Fatalf("resolvePreferredLibraryStorageClassForRequest returned error: %v", err)
	}
	if got != "hot-s3-eu" {
		t.Fatalf("resolvePreferredLibraryStorageClassForRequest = %q, want %q", got, "hot-s3-eu")
	}
}

func TestResolvePreferredLibraryStorageClassForRequestIgnoresServerURLForRouting(t *testing.T) {
	manager := storage.NewManager()
	manager.SetDefaultClass("hot-minio-local")
	manager.SetEndpointRegions(map[string]string{"eu.sesamefs.local": "eu"})
	manager.SetRegionClasses(map[string]storage.RegionClassConfig{
		"eu": {Hot: "hot-s3-eu"},
	})
	manager.RegisterBackend("hot-s3-eu", &storage.S3Store{}, "")

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/repo/repo-id/raw/file.txt", nil)
	c.Request.Host = "eu.sesamefs.local"

	got, err := resolvePreferredLibraryStorageClassForRequest(c, nil, manager, "", "")
	if err != nil {
		t.Fatalf("resolvePreferredLibraryStorageClassForRequest returned error: %v", err)
	}
	if got != "hot-s3-eu" {
		t.Fatalf("resolvePreferredLibraryStorageClassForRequest = %q, want %q", got, "hot-s3-eu")
	}
}

func TestResolvePreferredLibraryStorageClassForRequestUsesDefaultWithoutManager(t *testing.T) {
	got, err := resolvePreferredLibraryStorageClassForRequest(nil, nil, nil, "", "hot-minio-local")
	if err != nil {
		t.Fatalf("resolvePreferredLibraryStorageClassForRequest returned error: %v", err)
	}
	if got != "hot-minio-local" {
		t.Fatalf("resolvePreferredLibraryStorageClassForRequest = %q, want %q", got, "hot-minio-local")
	}
}

func TestLibraryPlacementLookupErrorFailsClosedAcrossResolvers(t *testing.T) {
	original := lookupLibraryStorageClassContextFn
	wantErr := errors.New("cassandra unavailable")
	lookupLibraryStorageClassContextFn = func(context.Context, *db.DB, string, string) (string, error) {
		return "", wantErr
	}
	t.Cleanup(func() { lookupLibraryStorageClassContextFn = original })

	manager := storage.NewManager()
	manager.SetDefaultClass("hot-s3-default")
	manager.RegisterBackend("hot-s3-default", &storage.S3Store{}, "")
	database := &db.DB{}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	tests := []struct {
		name    string
		resolve func() (*storage.BlockStore, string, error)
	}{
		{
			name: "shared reader",
			resolve: func() (*storage.BlockStore, string, error) {
				return resolveLibraryBlockStoreForRequestContext(context.Background(), c, database, nil, manager, nil, "org-1", "repo-1")
			},
		},
		{
			name: "file handler",
			resolve: func() (*storage.BlockStore, string, error) {
				return (&FileHandler{db: database, storageManager: manager}).resolveLibraryBlockStore(c, "org-1", "repo-1")
			},
		},
		{
			name: "block handler",
			resolve: func() (*storage.BlockStore, string, error) {
				return (&BlockHandler{db: database, storageManager: manager}).getBlockStoreForRepo(c, "org-1", "repo-1")
			},
		},
		{
			name: "onlyoffice handler",
			resolve: func() (*storage.BlockStore, string, error) {
				return (&OnlyOfficeHandler{db: database, storageManager: manager}).resolveLibraryBlockStore("org-1", "repo-1")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blockStore, storageClass, err := tt.resolve()
			if !errors.Is(err, wantErr) {
				t.Fatalf("error = %v, want %v", err, wantErr)
			}
			if blockStore != nil || storageClass != "" {
				t.Fatalf("store/class = %p/%q, want nil/empty (no default fallback)", blockStore, storageClass)
			}
		})
	}
}

func TestSuccessfulEmptyLibraryPlacementStillUsesDefaultRouting(t *testing.T) {
	original := lookupLibraryStorageClassContextFn
	lookupLibraryStorageClassContextFn = func(context.Context, *db.DB, string, string) (string, error) {
		return "", nil
	}
	t.Cleanup(func() { lookupLibraryStorageClassContextFn = original })

	manager := storage.NewManager()
	manager.SetDefaultClass("hot-s3-default")
	manager.RegisterBackend("hot-s3-default", &storage.S3Store{}, "")
	store, class, err := resolveLibraryBlockStoreForRequestContext(context.Background(), nil, &db.DB{}, &config.Config{}, manager, nil, "3fa85f64-5717-4562-b3fc-2c963f66afa6", "repo-1")
	if err != nil {
		t.Fatalf("resolveLibraryBlockStoreForRequestContext returned error: %v", err)
	}
	if store == nil || class != "hot-s3-default" {
		t.Fatalf("store/class = %p/%q, want non-nil/%q", store, class, "hot-s3-default")
	}
}
