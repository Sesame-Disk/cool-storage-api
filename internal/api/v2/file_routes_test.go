package v2

import (
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
)

func TestNewRouteFileHandlerUsesStorageRoutingDependencies(t *testing.T) {
	s3Store := &storage.S3Store{}
	storageManager := storage.NewManager()

	h := newRouteFileHandler(nil, &config.Config{}, s3Store, storageManager, nil, "https://files.example.com")

	if h == nil {
		t.Fatal("newRouteFileHandler returned nil")
	}
	if h.storage != s3Store {
		t.Fatal("newRouteFileHandler did not preserve raw S3 fallback")
	}
	if h.storageManager != storageManager {
		t.Fatal("newRouteFileHandler did not preserve storageManager")
	}
	if h.permMiddleware == nil {
		t.Fatal("newRouteFileHandler did not initialize permission middleware")
	}
	if h.gcEnqueuer != getBlockEnqueuer() {
		t.Fatal("newRouteFileHandler did not attach GC enqueuer")
	}
	if h.serverURL != "https://files.example.com" {
		t.Fatalf("serverURL = %q, want %q", h.serverURL, "https://files.example.com")
	}
}
