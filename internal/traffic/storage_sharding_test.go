package traffic

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

type nilDBSession struct{}

func (nilDBSession) Session() *gocql.Session { return nil }

func TestCounterShardDeterministicAndInRange(t *testing.T) {
	const orgID = "00000000-0000-0000-0000-000000000123"

	first := CounterShard(orgID)
	second := CounterShard(orgID)
	if first != second {
		t.Fatalf("CounterShard(%q) changed across calls: %d vs %d", orgID, first, second)
	}
	if first < 0 || first >= CounterShardCount {
		t.Fatalf("CounterShard(%q) = %d, want 0 <= shard < %d", orgID, first, CounterShardCount)
	}

	seen := map[int]struct{}{}
	for i := 0; i < 256; i++ {
		seen[CounterShard(fmt.Sprintf("00000000-0000-0000-0000-%012d", i))] = struct{}{}
	}
	if len(seen) < 8 {
		t.Fatalf("CounterShard used only %d shard(s) across sample org IDs, want at least 8", len(seen))
	}
}

func TestCounterShardCanonicalizesUUIDRepresentations(t *testing.T) {
	const canonical = "00000000-0000-0000-0000-00000000abcd"

	parsed, err := gocql.ParseUUID(canonical)
	if err != nil {
		t.Fatalf("ParseUUID(%q) failed: %v", canonical, err)
	}

	want := CounterShardUUID(parsed)
	variants := []string{
		canonical,
		strings.ToUpper(canonical),
		"  " + canonical + "  ",
	}
	for _, variant := range variants {
		if got := CounterShard(variant); got != want {
			t.Fatalf("CounterShard(%q) = %d, want canonical shard %d", variant, got, want)
		}
	}
}

func TestStorageMutationRoutesOnlyPlatformUsesHashedShard(t *testing.T) {
	orgID := "00000000-0000-0000-0000-000000000555"
	userID := "00000000-0000-0000-0000-000000000556"
	libraryID := "00000000-0000-0000-0000-000000000557"

	routes := storageMutationRoutes(orgID, userID, libraryID)
	if len(routes) != 4 {
		t.Fatalf("storageMutationRoutes returned %d routes, want 4", len(routes))
	}

	want := map[string]int{
		PlatformStorageScope():                CounterShard(orgID),
		OrganizationStorageScope(orgID):       counterShardZero,
		UserStorageScope(orgID, userID):       counterShardZero,
		LibraryStorageScope(orgID, libraryID): counterShardZero,
	}
	for _, route := range routes {
		if got, ok := want[route.scope]; !ok {
			t.Fatalf("unexpected scope route: %+v", route)
		} else if route.shard != got {
			t.Fatalf("scope %s routed to shard %d, want %d", route.scope, route.shard, got)
		}
	}
}

func TestForEachStorageReadShardPlatformFansOutButTenantScopesStayLocal(t *testing.T) {
	var platformShards []int
	forEachStorageReadShard(PlatformStorageScope(), func(shard int) {
		platformShards = append(platformShards, shard)
	})
	if len(platformShards) != CounterShardCount {
		t.Fatalf("platform read fan-out visited %d shards, want %d", len(platformShards), CounterShardCount)
	}
	for shard := 0; shard < CounterShardCount; shard++ {
		found := false
		for _, got := range platformShards {
			if got == shard {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("platform read fan-out missing shard %d in %v", shard, platformShards)
		}
	}

	var orgShards []int
	forEachStorageReadShard(OrganizationStorageScope("00000000-0000-0000-0000-000000000999"), func(shard int) {
		orgShards = append(orgShards, shard)
	})
	if len(orgShards) != 1 || orgShards[0] != counterShardZero {
		t.Fatalf("org scope read fan-out = %v, want [0]", orgShards)
	}
}

func TestReconcileStorageScopeReturnsReadError(t *testing.T) {
	prev := readStorageSnapshotAtShardErrFn
	t.Cleanup(func() { readStorageSnapshotAtShardErrFn = prev })

	readStorageSnapshotAtShardErrFn = func(db DBSession, scope string, shard int, day time.Time) (StorageSnapshot, error) {
		return StorageSnapshot{}, errors.New("read failed")
	}

	err := ReconcileStorageScope(nilDBSession{}, OrganizationStorageScope("00000000-0000-0000-0000-000000000123"), StorageSnapshot{})
	if err == nil || !strings.Contains(err.Error(), "read failed") {
		t.Fatalf("ReconcileStorageScope error = %v, want wrapped read failure", err)
	}
}

func TestReconcileStorageScopeShardedReturnsReadError(t *testing.T) {
	prev := readStorageSnapshotAtShardErrFn
	t.Cleanup(func() { readStorageSnapshotAtShardErrFn = prev })

	readStorageSnapshotAtShardErrFn = func(db DBSession, scope string, shard int, day time.Time) (StorageSnapshot, error) {
		if shard == 7 {
			return StorageSnapshot{}, errors.New("shard read failed")
		}
		return StorageSnapshot{}, nil
	}

	err := ReconcileStorageScopeSharded(nilDBSession{}, PlatformStorageScope(), map[int]StorageSnapshot{})
	if err == nil || !strings.Contains(err.Error(), "shard read failed") {
		t.Fatalf("ReconcileStorageScopeSharded error = %v, want wrapped shard read failure", err)
	}
}

func TestReconcileStorageScopeShardedDoesNotWriteBeforeReadPassSucceeds(t *testing.T) {
	prevRead := readStorageSnapshotAtShardErrFn
	prevWrite := storageUpdateErrFn
	t.Cleanup(func() {
		readStorageSnapshotAtShardErrFn = prevRead
		storageUpdateErrFn = prevWrite
	})

	readStorageSnapshotAtShardErrFn = func(db DBSession, scope string, shard int, day time.Time) (StorageSnapshot, error) {
		if shard == 7 {
			return StorageSnapshot{}, errors.New("shard read failed")
		}
		return StorageSnapshot{}, nil
	}

	writeCalls := 0
	storageUpdateErrFn = func(session *gocql.Session, scope string, shard int, day time.Time, deltaBytes, deltaFiles int64) error {
		writeCalls++
		return nil
	}

	err := ReconcileStorageScopeSharded(nilDBSession{}, PlatformStorageScope(), map[int]StorageSnapshot{
		0: {BytesUsed: 123, FileCount: 1},
		9: {BytesUsed: 456, FileCount: 2},
	})
	if err == nil || !strings.Contains(err.Error(), "shard read failed") {
		t.Fatalf("ReconcileStorageScopeSharded error = %v, want wrapped shard read failure", err)
	}
	if writeCalls != 0 {
		t.Fatalf("ReconcileStorageScopeSharded performed %d write(s) before read pass completed, want 0", writeCalls)
	}
}
