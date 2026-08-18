package v2

import (
	"net/http"
	"net/http/httptest"
	"testing"

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
