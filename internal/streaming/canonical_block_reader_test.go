package streaming

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
)

const canonicalReaderTestOrg = "3fa85f64-5717-4562-b3fc-2c963f66afa6"

func canonicalReaderTestID(n int) string {
	return fmt.Sprintf("%064x", n)
}

func canonicalReaderTestCreatedAt() *time.Time {
	createdAt := time.Unix(1, 0).UTC()
	return &createdAt
}

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
	oldExists := canonicalBlockExists
	oldRetryDelay := canonicalBlockLocationRetryDelay
	canonicalBlockLocationRetryDelay = 0
	t.Cleanup(func() {
		canonicalBlockLocationLookup = oldLocationLookup
		canonicalBlockStoreLookup = oldStoreLookup
		canonicalBlockGet = oldGet
		canonicalBlockGetReader = oldGetReader
		canonicalBlockGetSize = oldGetSize
		canonicalBlockExists = oldExists
		canonicalBlockLocationRetryDelay = oldRetryDelay
	})
}

func TestCanonicalBlockReaderRoutesCanonicalLocationsAndDeduplicates(t *testing.T) {
	resetCanonicalReaderHooks(t)
	idA := canonicalReaderTestID(101)
	idB := canonicalReaderTestID(202)
	storeA := canonicalReaderTestStore(t)
	storeB := canonicalReaderTestStore(t)
	keyA := storeA.StorageKeyForHash(idA)
	keyB := storeB.StorageKeyForHash(idB)
	metadata := map[string]db.BlockStorageLocation{
		idA: {SizeBytes: 11, StorageClass: "class-a", StorageKey: keyA, CreatedAt: canonicalReaderTestCreatedAt()},
		idB: {SizeBytes: 22, StorageClass: "class-b", StorageKey: keyB, CreatedAt: canonicalReaderTestCreatedAt()},
	}
	lookupCalls := map[string]int{}
	var lookupMu sync.Mutex
	canonicalBlockLocationLookup = func(_ context.Context, _ *db.DB, orgID, blockID string) (db.BlockStorageLocation, bool, error) {
		if orgID != canonicalReaderTestOrg {
			return db.BlockStorageLocation{}, false, fmt.Errorf("orgID = %q", orgID)
		}
		lookupMu.Lock()
		lookupCalls[blockID]++
		lookupMu.Unlock()
		location, found := metadata[blockID]
		return location, found, nil
	}
	canonicalBlockStoreLookup = func(_ *storage.Manager, orgID, class string) (*storage.BlockStore, error) {
		if orgID != canonicalReaderTestOrg {
			return nil, fmt.Errorf("orgID = %q", orgID)
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
			return nil, fmt.Errorf("wrong GetBlock route")
		}
		return []byte("a"), nil
	}
	canonicalBlockGetReader = func(_ context.Context, store *storage.BlockStore, key string) (io.ReadCloser, error) {
		if store != storeB || key != keyB {
			return nil, fmt.Errorf("wrong GetBlockReader route")
		}
		return io.NopCloser(strings.NewReader("b")), nil
	}
	canonicalBlockGetSize = func(context.Context, *storage.BlockStore, string) (int64, error) {
		return 0, errors.New("metadata size should avoid HEAD")
	}

	reader, err := NewCanonicalBlockReader(context.Background(), nil, storage.NewManager(), canonicalReaderTestOrg, []string{idA, idB, idA}, nil, "")
	if err != nil {
		t.Fatalf("NewCanonicalBlockReader() error = %v", err)
	}
	if lookupCalls[idA] != 1 || lookupCalls[idB] != 1 {
		t.Fatalf("lookup calls = %v, want one per unique ID", lookupCalls)
	}
	if data, err := reader.GetBlock(context.Background(), idA); err != nil || string(data) != "a" {
		t.Fatalf("GetBlock() = %q, %v", data, err)
	}
	blockReader, err := reader.GetBlockReader(context.Background(), idB)
	if err != nil {
		t.Fatalf("GetBlockReader() error = %v", err)
	}
	data, err := io.ReadAll(blockReader)
	_ = blockReader.Close()
	if err != nil || string(data) != "b" {
		t.Fatalf("reader = %q, %v", data, err)
	}
	if size, err := reader.GetBlockSize(context.Background(), idA); err != nil || size != 11 {
		t.Fatalf("GetBlockSize() = %d, %v, want 11, nil", size, err)
	}
}

// A persistently missing row is looked up 3 times in total (1 attempt + 2
// retries), not retried 3 times.
func TestCanonicalBlockReaderStrictMissingUsesThreeTotalAttempts(t *testing.T) {
	resetCanonicalReaderHooks(t)
	blockID := canonicalReaderTestID(303)
	var calls atomic.Int32
	canonicalBlockLocationLookup = func(context.Context, *db.DB, string, string) (db.BlockStorageLocation, bool, error) {
		calls.Add(1)
		return db.BlockStorageLocation{}, false, nil
	}

	reader, err := NewCanonicalBlockReader(context.Background(), nil, nil, canonicalReaderTestOrg, []string{blockID}, canonicalReaderTestStore(t), "fallback")
	if reader != nil || !errors.Is(err, ErrCanonicalBlockMetadataNotFound) {
		t.Fatalf("NewCanonicalBlockReader() = (%v, %v), want metadata-not-found", reader, err)
	}
	if calls.Load() != canonicalBlockLocationLookupAttempts {
		t.Fatalf("lookup calls = %d, want %d", calls.Load(), canonicalBlockLocationLookupAttempts)
	}
}

func TestCanonicalBlockReaderStrictRetryCanObserveMetadata(t *testing.T) {
	resetCanonicalReaderHooks(t)
	blockID := canonicalReaderTestID(304)
	store := canonicalReaderTestStore(t)
	var calls atomic.Int32
	canonicalBlockLocationLookup = func(context.Context, *db.DB, string, string) (db.BlockStorageLocation, bool, error) {
		// Fail every attempt but the last one the budget allows.
		if calls.Add(1) < canonicalBlockLocationLookupAttempts {
			return db.BlockStorageLocation{}, false, nil
		}
		return db.BlockStorageLocation{StorageClass: "fallback", StorageKey: store.StorageKeyForHash(blockID), CreatedAt: canonicalReaderTestCreatedAt()}, true, nil
	}

	reader, err := NewCanonicalBlockReader(context.Background(), nil, nil, canonicalReaderTestOrg, []string{blockID}, store, "fallback")
	if err != nil || reader == nil {
		t.Fatalf("NewCanonicalBlockReader() = (%v, %v), want reader", reader, err)
	}
	if calls.Load() != canonicalBlockLocationLookupAttempts {
		t.Fatalf("lookup calls = %d, want %d", calls.Load(), canonicalBlockLocationLookupAttempts)
	}
}

func TestCanonicalBlockReaderDatabaseErrorFailsWithoutRetry(t *testing.T) {
	resetCanonicalReaderHooks(t)
	databaseErr := errors.New("cassandra timeout")
	var calls atomic.Int32
	canonicalBlockLocationLookup = func(context.Context, *db.DB, string, string) (db.BlockStorageLocation, bool, error) {
		calls.Add(1)
		return db.BlockStorageLocation{}, false, databaseErr
	}

	reader, err := NewCanonicalBlockReader(context.Background(), nil, nil, canonicalReaderTestOrg, []string{canonicalReaderTestID(305)}, canonicalReaderTestStore(t), "fallback")
	if reader != nil || !errors.Is(err, databaseErr) {
		t.Fatalf("NewCanonicalBlockReader() = (%v, %v), want database error", reader, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("lookup calls = %d, want 1", calls.Load())
	}
}

func TestCanonicalBlockCheckReaderDoesNotRetryMissingOrDeleting(t *testing.T) {
	for _, tc := range []struct {
		name     string
		location db.BlockStorageLocation
		found    bool
	}{
		{name: "missing"},
		{name: "deleting", location: db.BlockStorageLocation{GCState: db.BlockGCStateDeleting}, found: true},
		{name: "released stub", location: db.BlockStorageLocation{}, found: true},
		{name: "repairing stub", location: db.BlockStorageLocation{GCState: db.BlockGCStateRepairingStub}, found: true},
		{name: "repairing stub with storage metadata", location: db.BlockStorageLocation{GCState: db.BlockGCStateRepairingStub, StorageClass: "fallback", CreatedAt: canonicalReaderTestCreatedAt()}, found: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetCanonicalReaderHooks(t)
			blockID := canonicalReaderTestID(400)
			var calls atomic.Int32
			canonicalBlockLocationLookup = func(context.Context, *db.DB, string, string) (db.BlockStorageLocation, bool, error) {
				calls.Add(1)
				return tc.location, tc.found, nil
			}
			canonicalBlockExists = func(context.Context, *storage.BlockStore, string) (bool, error) {
				return false, errors.New("missing location must not probe storage")
			}

			reader, err := NewCanonicalBlockCheckReader(context.Background(), nil, storage.NewManager(), canonicalReaderTestOrg, []string{blockID}, nil, "")
			if err != nil {
				t.Fatalf("NewCanonicalBlockCheckReader() error = %v", err)
			}
			exists, err := reader.CheckBlocksExist(context.Background(), []string{blockID}, 1)
			if err != nil || exists[blockID] {
				t.Fatalf("CheckBlocksExist() = %v, %v, want absent", exists, err)
			}
			if calls.Load() != 1 {
				t.Fatalf("lookup calls = %d, want 1", calls.Load())
			}
		})
	}
}

func TestCanonicalBlockReaderStrictRejectsDeleting(t *testing.T) {
	resetCanonicalReaderHooks(t)
	blockID := canonicalReaderTestID(401)
	store := canonicalReaderTestStore(t)
	canonicalBlockLocationLookup = func(context.Context, *db.DB, string, string) (db.BlockStorageLocation, bool, error) {
		return db.BlockStorageLocation{
			StorageClass: "fallback",
			StorageKey:   store.StorageKeyForHash(blockID),
			GCState:      db.BlockGCStateDeleting,
			CreatedAt:    canonicalReaderTestCreatedAt(),
		}, true, nil
	}
	canonicalBlockGet = func(context.Context, *storage.BlockStore, string) ([]byte, error) {
		t.Fatal("deleting block must not reach storage")
		return nil, nil
	}

	reader, err := NewCanonicalBlockReader(context.Background(), nil, nil, canonicalReaderTestOrg, []string{blockID}, store, "fallback")
	if reader != nil || err == nil {
		t.Fatalf("NewCanonicalBlockReader() = (%v, %v), want deleting error", reader, err)
	}
}

func TestCanonicalBlockReaderRejectsStorageMetadataWithoutCreationTimestamp(t *testing.T) {
	resetCanonicalReaderHooks(t)
	blockID := canonicalReaderTestID(402)
	store := canonicalReaderTestStore(t)
	canonicalBlockLocationLookup = func(context.Context, *db.DB, string, string) (db.BlockStorageLocation, bool, error) {
		return db.BlockStorageLocation{StorageClass: "fallback", StorageKey: store.StorageKeyForHash(blockID)}, true, nil
	}
	if reader, err := NewCanonicalBlockCheckReader(context.Background(), nil, nil, canonicalReaderTestOrg, []string{blockID}, store, "fallback"); reader != nil || err == nil {
		t.Fatalf("NewCanonicalBlockCheckReader() = (%v, %v), want malformed metadata error", reader, err)
	}
}

func TestCanonicalBlockReaderRejectsUnknownGCState(t *testing.T) {
	resetCanonicalReaderHooks(t)
	blockID := canonicalReaderTestID(403)
	store := canonicalReaderTestStore(t)
	for _, gcState := range []string{"unknown", " deleting "} {
		t.Run(gcState, func(t *testing.T) {
			canonicalBlockLocationLookup = func(context.Context, *db.DB, string, string) (db.BlockStorageLocation, bool, error) {
				return db.BlockStorageLocation{StorageClass: "fallback", StorageKey: store.StorageKeyForHash(blockID), GCState: gcState, CreatedAt: canonicalReaderTestCreatedAt()}, true, nil
			}
			if reader, err := NewCanonicalBlockCheckReader(context.Background(), nil, nil, canonicalReaderTestOrg, []string{blockID}, store, "fallback"); reader != nil || err == nil {
				t.Fatalf("NewCanonicalBlockCheckReader() = (%v, %v), want unknown-state error", reader, err)
			}
		})
	}
}

func TestCanonicalBlockReaderCancellationInterruptsRetryWait(t *testing.T) {
	resetCanonicalReaderHooks(t)
	canonicalBlockLocationRetryDelay = time.Hour
	started := make(chan struct{})
	var calls atomic.Int32
	canonicalBlockLocationLookup = func(context.Context, *db.DB, string, string) (db.BlockStorageLocation, bool, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		return db.BlockStorageLocation{}, false, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	store := canonicalReaderTestStore(t)
	go func() {
		_, err := NewCanonicalBlockReader(ctx, nil, nil, canonicalReaderTestOrg, []string{canonicalReaderTestID(500)}, store, "fallback")
		result <- err
	}()
	<-started
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not interrupt retry wait")
	}
	if calls.Load() != 1 {
		t.Fatalf("lookup calls = %d, want 1", calls.Load())
	}
}

func TestCanonicalBlockReaderBoundsUniqueLocationLookups(t *testing.T) {
	resetCanonicalReaderHooks(t)
	const uniqueCount = 64
	blockIDs := make([]string, 0, uniqueCount+1)
	for i := 1; i <= uniqueCount; i++ {
		blockIDs = append(blockIDs, canonicalReaderTestID(i))
	}
	blockIDs = append(blockIDs, blockIDs[0])
	release := make(chan struct{})
	reachedLimit := make(chan struct{})
	var active atomic.Int32
	var maximum atomic.Int32
	var calls atomic.Int32
	canonicalBlockLocationLookup = func(_ context.Context, _ *db.DB, _ string, _ string) (db.BlockStorageLocation, bool, error) {
		calls.Add(1)
		current := active.Add(1)
		for {
			old := maximum.Load()
			if current <= old || maximum.CompareAndSwap(old, current) {
				break
			}
		}
		if current == canonicalBlockLocationConcurrency {
			select {
			case <-reachedLimit:
			default:
				close(reachedLimit)
			}
		}
		<-release
		active.Add(-1)
		return db.BlockStorageLocation{StorageClass: "fallback", CreatedAt: canonicalReaderTestCreatedAt()}, true, nil
	}
	result := make(chan error, 1)
	store := canonicalReaderTestStore(t)
	go func() {
		_, err := NewCanonicalBlockReader(context.Background(), nil, nil, canonicalReaderTestOrg, blockIDs, store, "fallback")
		result <- err
	}()
	select {
	case <-reachedLimit:
	case <-time.After(time.Second):
		t.Fatal("lookups did not reach configured concurrency")
	}
	close(release)
	if err := <-result; err != nil {
		t.Fatalf("NewCanonicalBlockReader() error = %v", err)
	}
	if maximum.Load() != canonicalBlockLocationConcurrency {
		t.Fatalf("maximum concurrency = %d, want %d", maximum.Load(), canonicalBlockLocationConcurrency)
	}
	if calls.Load() != uniqueCount {
		t.Fatalf("lookup calls = %d, want %d unique lookups", calls.Load(), uniqueCount)
	}
}

func TestCanonicalBlockReaderRejectsInvalidIDAndMismatchedKey(t *testing.T) {
	resetCanonicalReaderHooks(t)
	if reader, err := NewCanonicalBlockReader(context.Background(), nil, nil, canonicalReaderTestOrg, []string{strings.Repeat("z", 64)}, canonicalReaderTestStore(t), "fallback"); reader != nil || err == nil {
		t.Fatalf("invalid SHA-256 = (%v, %v), want rejection", reader, err)
	}

	blockID := canonicalReaderTestID(600)
	store := canonicalReaderTestStore(t)
	canonicalBlockLocationLookup = func(context.Context, *db.DB, string, string) (db.BlockStorageLocation, bool, error) {
		return db.BlockStorageLocation{
			StorageClass: "fallback",
			StorageKey:   "blocks/6b2f9c1e-7a3d-4c5b-8e1f-0a9b8c7d6e5f/00/00/" + blockID,
			CreatedAt:    canonicalReaderTestCreatedAt(),
		}, true, nil
	}
	reader, err := NewCanonicalBlockReader(context.Background(), nil, nil, canonicalReaderTestOrg, []string{blockID}, store, "fallback")
	if reader != nil || err == nil || !strings.Contains(err.Error(), "does not match derived org-scoped key") {
		t.Fatalf("mismatched key = (%v, %v), want rejection", reader, err)
	}
}

func TestCanonicalBlockReaderExactClassAndBackendErrorsFailClosed(t *testing.T) {
	resetCanonicalReaderHooks(t)
	blockID := canonicalReaderTestID(700)
	backendErr := errors.New("canonical backend unavailable")
	canonicalBlockLocationLookup = func(context.Context, *db.DB, string, string) (db.BlockStorageLocation, bool, error) {
		return db.BlockStorageLocation{StorageClass: "canonical", CreatedAt: canonicalReaderTestCreatedAt()}, true, nil
	}
	canonicalBlockStoreLookup = func(*storage.Manager, string, string) (*storage.BlockStore, error) {
		return nil, backendErr
	}
	reader, err := NewCanonicalBlockReader(context.Background(), nil, storage.NewManager(), canonicalReaderTestOrg, []string{blockID}, canonicalReaderTestStore(t), "canonical")
	if reader != nil || !errors.Is(err, backendErr) {
		t.Fatalf("manager lookup = (%v, %v), want backend error", reader, err)
	}

	fallback := canonicalReaderTestStore(t)
	reader, err = NewCanonicalBlockReader(context.Background(), nil, nil, canonicalReaderTestOrg, []string{blockID}, fallback, "CANONICAL")
	if reader != nil || err == nil {
		t.Fatalf("case-mismatched fallback class = (%v, %v), want exact-class error", reader, err)
	}

	reader, err = NewCanonicalBlockReader(context.Background(), nil, nil, canonicalReaderTestOrg, []string{blockID}, fallback, "")
	if reader != nil || err == nil {
		t.Fatalf("empty fallback class = (%v, %v), want exact-class error", reader, err)
	}
}

func TestCanonicalBlockReaderExistenceBackendErrorFailsClosed(t *testing.T) {
	resetCanonicalReaderHooks(t)
	blockID := canonicalReaderTestID(800)
	store := canonicalReaderTestStore(t)
	canonicalBlockLocationLookup = func(context.Context, *db.DB, string, string) (db.BlockStorageLocation, bool, error) {
		return db.BlockStorageLocation{StorageClass: "fallback", CreatedAt: canonicalReaderTestCreatedAt()}, true, nil
	}
	backendErr := errors.New("HEAD failed")
	var calls atomic.Int32
	canonicalBlockExists = func(context.Context, *storage.BlockStore, string) (bool, error) {
		calls.Add(1)
		return false, backendErr
	}
	reader, err := NewCanonicalBlockCheckReader(context.Background(), nil, nil, canonicalReaderTestOrg, []string{blockID, blockID}, store, "fallback")
	if err != nil {
		t.Fatalf("NewCanonicalBlockCheckReader() error = %v", err)
	}
	exists, err := reader.CheckBlocksExist(context.Background(), []string{blockID, blockID}, 100)
	if exists != nil || !errors.Is(err, backendErr) {
		t.Fatalf("CheckBlocksExist() = (%v, %v), want nil and backend error", exists, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("existence calls = %d, want one deduplicated call", calls.Load())
	}
}

func TestCanonicalBlockReaderDerivesEmptyKeyAndUsesCachedSizes(t *testing.T) {
	resetCanonicalReaderHooks(t)
	blockID := canonicalReaderTestID(900)
	store := canonicalReaderTestStore(t)
	canonicalBlockLocationLookup = func(context.Context, *db.DB, string, string) (db.BlockStorageLocation, bool, error) {
		return db.BlockStorageLocation{SizeBytes: 99, StorageClass: "fallback", CreatedAt: canonicalReaderTestCreatedAt()}, true, nil
	}
	canonicalBlockGetSize = func(context.Context, *storage.BlockStore, string) (int64, error) {
		return 0, errors.New("cached size should avoid HEAD")
	}
	reader, err := NewCanonicalBlockReader(context.Background(), nil, nil, canonicalReaderTestOrg, []string{blockID}, store, "fallback")
	if err != nil {
		t.Fatalf("NewCanonicalBlockReader() error = %v", err)
	}
	sizes, err := QueryBlockSizes(context.Background(), nil, canonicalReaderTestOrg, reader, []string{blockID, blockID})
	if err != nil || len(sizes) != 2 || sizes[0] != 99 || sizes[1] != 99 {
		t.Fatalf("QueryBlockSizes() = %v, %v, want [99 99], nil", sizes, err)
	}
}
