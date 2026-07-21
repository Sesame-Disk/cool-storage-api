package streaming

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
)

const canonicalReaderTestOrg = "3fa85f64-5717-4562-b3fc-2c963f66afa6"

func canonicalReaderTestStore(t *testing.T) *storage.BlockStore {
	t.Helper()
	store, err := storage.NewOrgBlockStore(nil, "blocks/", canonicalReaderTestOrg)
	if err != nil {
		t.Fatalf("NewOrgBlockStore() error = %v", err)
	}
	return store
}

func resetCanonicalReaderHooks(t *testing.T) {
	t.Helper()
	oldLocationLookup := canonicalBlockLocationLookup
	oldStoreLookup := canonicalBlockStoreLookup
	oldGet := canonicalBlockGet
	oldGetReader := canonicalBlockGetReader
	oldGetSize := canonicalBlockGetSize
	t.Cleanup(func() {
		canonicalBlockLocationLookup = oldLocationLookup
		canonicalBlockStoreLookup = oldStoreLookup
		canonicalBlockGet = oldGet
		canonicalBlockGetReader = oldGetReader
		canonicalBlockGetSize = oldGetSize
	})
}

func TestCanonicalBlockReaderRoutesCanonicalLocationsAndDeduplicates(t *testing.T) {
	resetCanonicalReaderHooks(t)
	ctx := context.Background()
	idA := sha256Hex(101)
	idB := sha256Hex(202)
	storeA := canonicalReaderTestStore(t)
	storeB := canonicalReaderTestStore(t)
	keyA := storeA.StorageKeyForHash(idA)
	keyB := storeB.StorageKeyForHash(idB)
	metadata := map[string]db.BlockStorageLocation{
		idA: {SizeBytes: 11, StorageClass: "class-a", StorageKey: keyA},
		idB: {SizeBytes: 22, StorageClass: "class-b", StorageKey: keyB},
	}
	lookupCalls := map[string]int{}
	var lookupMu sync.Mutex
	canonicalBlockLocationLookup = func(_ context.Context, _ *db.DB, orgID, blockID string) (db.BlockStorageLocation, bool, error) {
		if orgID != canonicalReaderTestOrg {
			t.Fatalf("orgID = %q, want %q", orgID, canonicalReaderTestOrg)
		}
		lookupMu.Lock()
		lookupCalls[blockID]++
		lookupMu.Unlock()
		location, found := metadata[blockID]
		return location, found, nil
	}
	canonicalBlockStoreLookup = func(_ *storage.Manager, orgID, class string) (*storage.BlockStore, error) {
		if orgID != canonicalReaderTestOrg {
			t.Fatalf("orgID = %q, want %q", orgID, canonicalReaderTestOrg)
		}
		switch class {
		case "class-a":
			return storeA, nil
		case "class-b":
			return storeB, nil
		default:
			return nil, fmt.Errorf("unexpected class %q", class)
		}
	}
	canonicalBlockGet = func(_ context.Context, store *storage.BlockStore, key string) ([]byte, error) {
		if store != storeA || key != keyA {
			t.Fatalf("GetBlock routed to (%p, %q), want storeA %q", store, key, keyA)
		}
		return []byte("a"), nil
	}
	canonicalBlockGetReader = func(_ context.Context, store *storage.BlockStore, key string) (io.ReadCloser, error) {
		if store != storeB || key != keyB {
			t.Fatalf("GetBlockReader routed to (%p, %q), want storeB %q", store, key, keyB)
		}
		return io.NopCloser(strings.NewReader("b")), nil
	}
	canonicalBlockGetSize = func(context.Context, *storage.BlockStore, string) (int64, error) {
		t.Fatal("metadata sizes must avoid HEAD")
		return 0, nil
	}

	reader, err := NewCanonicalBlockReader(ctx, nil, storage.NewManager(), canonicalReaderTestOrg, []string{idA, idB, idA}, canonicalReaderTestStore(t), "fallback")
	if err != nil {
		t.Fatalf("NewCanonicalBlockReader() error = %v", err)
	}
	lookupMu.Lock()
	if lookupCalls[idA] != 1 || lookupCalls[idB] != 1 {
		t.Fatalf("location lookup calls = %v, want one per unique block", lookupCalls)
	}
	lookupMu.Unlock()
	if data, err := reader.GetBlock(ctx, idA); err != nil || string(data) != "a" {
		t.Fatalf("GetBlock() = %q, %v, want a, nil", data, err)
	}
	blockReader, err := reader.GetBlockReader(ctx, idB)
	if err != nil {
		t.Fatalf("GetBlockReader() error = %v", err)
	}
	data, readErr := io.ReadAll(blockReader)
	blockReader.Close()
	if readErr != nil || string(data) != "b" {
		t.Fatalf("reader data = %q, %v, want b, nil", data, readErr)
	}
	if size, err := reader.GetBlockSize(ctx, idA); err != nil || size != 11 {
		t.Fatalf("GetBlockSize(idA) = %d, %v, want 11, nil", size, err)
	}
	if size, err := reader.GetBlockSize(ctx, idB); err != nil || size != 22 {
		t.Fatalf("GetBlockSize(idB) = %d, %v, want 22, nil", size, err)
	}
}

func TestCanonicalBlockReaderDerivesEmptyKeyAndHeadsSelectedStore(t *testing.T) {
	resetCanonicalReaderHooks(t)
	ctx := context.Background()
	blockID := sha256Hex(303)
	selected := canonicalReaderTestStore(t)
	canonicalBlockLocationLookup = func(context.Context, *db.DB, string, string) (db.BlockStorageLocation, bool, error) {
		return db.BlockStorageLocation{StorageClass: "canonical"}, true, nil
	}
	canonicalBlockStoreLookup = func(*storage.Manager, string, string) (*storage.BlockStore, error) {
		return selected, nil
	}
	wantKey := selected.StorageKeyForHash(blockID)
	canonicalBlockGetSize = func(_ context.Context, store *storage.BlockStore, key string) (int64, error) {
		if store != selected || key != wantKey {
			t.Fatalf("HEAD routed to (%p, %q), want selected store and %q", store, key, wantKey)
		}
		return 303, nil
	}

	reader, err := NewCanonicalBlockReader(ctx, nil, storage.NewManager(), canonicalReaderTestOrg, []string{blockID}, canonicalReaderTestStore(t), "fallback")
	if err != nil {
		t.Fatalf("NewCanonicalBlockReader() error = %v", err)
	}
	if size, err := reader.GetBlockSize(ctx, blockID); err != nil || size != 303 {
		t.Fatalf("GetBlockSize() = %d, %v, want 303, nil", size, err)
	}
}

func TestCanonicalBlockReaderExactClassFailureDoesNotFallback(t *testing.T) {
	resetCanonicalReaderHooks(t)
	blockID := sha256Hex(404)
	infrastructureErr := errors.New("canonical backend unavailable")
	canonicalBlockLocationLookup = func(context.Context, *db.DB, string, string) (db.BlockStorageLocation, bool, error) {
		return db.BlockStorageLocation{StorageClass: "canonical", StorageKey: "canonical/key"}, true, nil
	}
	canonicalBlockStoreLookup = func(*storage.Manager, string, string) (*storage.BlockStore, error) {
		return nil, infrastructureErr
	}

	reader, err := NewCanonicalBlockReader(context.Background(), nil, storage.NewManager(), canonicalReaderTestOrg, []string{blockID}, canonicalReaderTestStore(t), "fallback")
	if reader != nil || !errors.Is(err, infrastructureErr) {
		t.Fatalf("NewCanonicalBlockReader() = (%v, %v), want nil and exact-class error", reader, err)
	}
}

func TestCanonicalBlockReaderAbsentMetadataUsesFallback(t *testing.T) {
	resetCanonicalReaderHooks(t)
	ctx := context.Background()
	blockID := sha256Hex(505)
	fallback := canonicalReaderTestStore(t)
	canonicalBlockLocationLookup = func(context.Context, *db.DB, string, string) (db.BlockStorageLocation, bool, error) {
		return db.BlockStorageLocation{}, false, nil
	}
	canonicalBlockStoreLookup = func(*storage.Manager, string, string) (*storage.BlockStore, error) {
		t.Fatal("absent metadata must not resolve a manager class")
		return nil, nil
	}
	wantKey := fallback.StorageKeyForHash(blockID)
	canonicalBlockGet = func(_ context.Context, store *storage.BlockStore, key string) ([]byte, error) {
		if store != fallback || key != wantKey {
			t.Fatalf("fallback read routed to (%p, %q), want fallback and %q", store, key, wantKey)
		}
		return []byte("legacy"), nil
	}

	reader, err := NewCanonicalBlockReader(ctx, nil, storage.NewManager(), canonicalReaderTestOrg, []string{blockID}, fallback, "fallback")
	if err != nil {
		t.Fatalf("NewCanonicalBlockReader() error = %v", err)
	}
	if data, err := reader.GetBlock(ctx, blockID); err != nil || string(data) != "legacy" {
		t.Fatalf("GetBlock() = %q, %v, want legacy, nil", data, err)
	}
}

func TestCanonicalBlockReaderEmptyMetadataClassUsesFallbackDerivedKey(t *testing.T) {
	resetCanonicalReaderHooks(t)
	ctx := context.Background()
	blockID := sha256Hex(555)
	fallback := canonicalReaderTestStore(t)
	wantKey := fallback.StorageKeyForHash(blockID)
	canonicalBlockLocationLookup = func(context.Context, *db.DB, string, string) (db.BlockStorageLocation, bool, error) {
		return db.BlockStorageLocation{StorageClass: "", StorageKey: wantKey}, true, nil
	}
	canonicalBlockStoreLookup = func(*storage.Manager, string, string) (*storage.BlockStore, error) {
		t.Fatal("an empty metadata class must not resolve a manager class")
		return nil, nil
	}
	canonicalBlockGet = func(_ context.Context, store *storage.BlockStore, key string) ([]byte, error) {
		if store != fallback || key != wantKey {
			t.Fatalf("fallback read routed to (%p, %q), want fallback and %q", store, key, wantKey)
		}
		return []byte("legacy"), nil
	}

	reader, err := NewCanonicalBlockReader(ctx, nil, storage.NewManager(), canonicalReaderTestOrg, []string{blockID}, fallback, "fallback")
	if err != nil {
		t.Fatalf("NewCanonicalBlockReader() error = %v", err)
	}
	if _, err := reader.GetBlock(ctx, blockID); err != nil {
		t.Fatalf("GetBlock() error = %v", err)
	}
}

func TestCanonicalBlockReaderRejectsNonOrgScopedStorageKey(t *testing.T) {
	resetCanonicalReaderHooks(t)
	blockID := sha256Hex(556)
	selected := canonicalReaderTestStore(t)
	canonicalBlockLocationLookup = func(context.Context, *db.DB, string, string) (db.BlockStorageLocation, bool, error) {
		return db.BlockStorageLocation{StorageClass: "canonical", StorageKey: "blocks/other-org/00/00/" + blockID}, true, nil
	}
	canonicalBlockStoreLookup = func(*storage.Manager, string, string) (*storage.BlockStore, error) {
		return selected, nil
	}

	reader, err := NewCanonicalBlockReader(context.Background(), nil, storage.NewManager(), canonicalReaderTestOrg, []string{blockID}, canonicalReaderTestStore(t), "fallback")
	if reader != nil || err == nil || !strings.Contains(err.Error(), "does not match derived org-scoped key") {
		t.Fatalf("NewCanonicalBlockReader() = (%v, %v), want nil and key mismatch", reader, err)
	}
}

func TestCanonicalBlockReaderCanceledBeforeResolutionReturnsError(t *testing.T) {
	resetCanonicalReaderHooks(t)
	canonicalBlockLocationLookup = func(context.Context, *db.DB, string, string) (db.BlockStorageLocation, bool, error) {
		t.Fatal("canceled construction must not start a location lookup")
		return db.BlockStorageLocation{}, false, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	reader, err := NewCanonicalBlockReader(ctx, nil, storage.NewManager(), canonicalReaderTestOrg, []string{sha256Hex(557)}, canonicalReaderTestStore(t), "fallback")
	if reader != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("NewCanonicalBlockReader() = (%v, %v), want nil, context.Canceled", reader, err)
	}
}

func TestCanonicalBlockReaderDoesNotFallbackOnMetadataError(t *testing.T) {
	resetCanonicalReaderHooks(t)
	infrastructureErr := errors.New("cassandra timeout")
	canonicalBlockLocationLookup = func(context.Context, *db.DB, string, string) (db.BlockStorageLocation, bool, error) {
		return db.BlockStorageLocation{}, false, infrastructureErr
	}

	reader, err := NewCanonicalBlockReader(context.Background(), nil, storage.NewManager(), canonicalReaderTestOrg, []string{sha256Hex(606)}, canonicalReaderTestStore(t), "fallback")
	if reader != nil || !errors.Is(err, infrastructureErr) {
		t.Fatalf("NewCanonicalBlockReader() = (%v, %v), want nil and Cassandra error", reader, err)
	}
}

func TestCanonicalBlockReaderNilManagerRequiresMatchingFallbackClass(t *testing.T) {
	resetCanonicalReaderHooks(t)
	ctx := context.Background()
	blockID := sha256Hex(707)
	fallback := canonicalReaderTestStore(t)
	canonicalBlockLocationLookup = func(context.Context, *db.DB, string, string) (db.BlockStorageLocation, bool, error) {
		return db.BlockStorageLocation{StorageClass: "canonical", StorageKey: fallback.StorageKeyForHash(blockID)}, true, nil
	}

	if _, err := NewCanonicalBlockReader(ctx, nil, nil, canonicalReaderTestOrg, []string{blockID}, fallback, "canonical"); err != nil {
		t.Fatalf("matching fallback class error = %v", err)
	}
	if reader, err := NewCanonicalBlockReader(ctx, nil, nil, canonicalReaderTestOrg, []string{blockID}, fallback, "other"); reader != nil || err == nil {
		t.Fatalf("mismatched fallback class = (%v, %v), want nil, error", reader, err)
	}
}
