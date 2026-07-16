package gc

import (
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/storage"
)

func TestStorageManagerAdapterRequiresValidOrgID(t *testing.T) {
	manager := storage.NewManager()
	manager.RegisterBackend("hot", &storage.S3Store{}, "")
	adapter := NewStorageManagerAdapter(manager)

	for _, orgID := range []string{"", "not-a-uuid"} {
		if _, err := adapter.GetBlockStoreForOrg(orgID, "hot"); err == nil {
			t.Fatalf("GetBlockStoreForOrg(%q) error = nil", orgID)
		}
	}
}

func TestStorageManagerAdapterScopesPlatformAndTenantKeys(t *testing.T) {
	manager := storage.NewManager()
	manager.RegisterBackend("hot", &storage.S3Store{}, "")
	adapter := NewStorageManagerAdapter(manager)

	const (
		platformOrg = "00000000-0000-0000-0000-000000000000"
		tenantOrg   = "3fa85f64-5717-4562-b3fc-2c963f66afa6"
		blockID     = "abcd1234"
	)
	platformDeleter, err := adapter.GetBlockStoreForOrg(platformOrg, "hot")
	if err != nil {
		t.Fatalf("platform org: %v", err)
	}
	tenantDeleter, err := adapter.GetBlockStoreForOrg(tenantOrg, "hot")
	if err != nil {
		t.Fatalf("tenant org: %v", err)
	}
	platformStore := platformDeleter.(*storage.BlockStore)
	tenantStore := tenantDeleter.(*storage.BlockStore)
	if platformStore.StorageKeyForHash(blockID) == tenantStore.StorageKeyForHash(blockID) {
		t.Fatal("platform and tenant orgs resolved the same physical key")
	}
}

func TestStorageManagerAdapterUsesExactClassWithoutHealthFailover(t *testing.T) {
	manager := storage.NewManager()
	manager.RegisterBackend("primary", &storage.S3Store{}, "missing-fallback")
	manager.UpdateHealth("primary", storage.HealthUnhealthy, nil)
	adapter := NewStorageManagerAdapter(manager)

	if _, err := adapter.GetBlockStoreForOrg("3fa85f64-5717-4562-b3fc-2c963f66afa6", "primary"); err != nil {
		t.Fatalf("exact primary class should resolve despite unhealthy status: %v", err)
	}
}
