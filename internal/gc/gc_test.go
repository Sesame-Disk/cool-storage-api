package gc

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/google/uuid"
)

type fakeLeaderLease struct {
	allowed  bool
	err      error
	delay    time.Duration
	calls    int32
	released atomic.Bool
	leader   atomic.Bool
	acquired chan struct{}
	once     sync.Once
}

func (f *fakeLeaderLease) TryAcquireOrRenew(ctx context.Context) (bool, error) {
	atomic.AddInt32(&f.calls, 1)
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if f.delay > 0 {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(f.delay):
		}
	}
	f.leader.Store(f.allowed)
	if f.allowed && f.acquired != nil {
		f.once.Do(func() {
			close(f.acquired)
		})
	}
	return f.allowed, f.err
}

func (f *fakeLeaderLease) Release(ctx context.Context) {
	f.leader.Store(false)
	f.released.Store(true)
}

func (f *fakeLeaderLease) IsLeader() bool {
	// If TryAcquireOrRenew has never been called the leader atomic is still
	// at its zero value (false). Fall back to the static field so tests that
	// call runWorkerOnce / runScannerOnce directly — without going through
	// Start() — don't need to prime the fake first.
	if atomic.LoadInt32(&f.calls) == 0 {
		return f.allowed
	}
	return f.leader.Load()
}

func TestStats_BlocksDeleted(t *testing.T) {
	s := &Stats{}

	if got := s.BlocksDeleted(); got != 0 {
		t.Errorf("initial BlocksDeleted() = %d, want 0", got)
	}

	s.IncrBlocksDeleted()
	s.IncrBlocksDeleted()
	s.IncrBlocksDeleted()

	if got := s.BlocksDeleted(); got != 3 {
		t.Errorf("BlocksDeleted() = %d, want 3", got)
	}
}

func TestStats_LastWorkerRun(t *testing.T) {
	s := &Stats{}

	if got := s.LastWorkerRun(); !got.IsZero() {
		t.Errorf("initial LastWorkerRun() = %v, want zero time", got)
	}

	now := time.Now()
	s.SetLastWorkerRun(now)

	if got := s.LastWorkerRun(); !got.Equal(now) {
		t.Errorf("LastWorkerRun() = %v, want %v", got, now)
	}
}

func TestStats_LastScanRun(t *testing.T) {
	s := &Stats{}

	if got := s.LastScanRun(); !got.IsZero() {
		t.Errorf("initial LastScanRun() = %v, want zero time", got)
	}

	now := time.Now()
	s.SetLastScanRun(now)

	if got := s.LastScanRun(); !got.Equal(now) {
		t.Errorf("LastScanRun() = %v, want %v", got, now)
	}
}

func TestStats_Concurrent(t *testing.T) {
	s := &Stats{}
	done := make(chan struct{})

	// Concurrent increments
	for i := 0; i < 100; i++ {
		go func() {
			s.IncrBlocksDeleted()
			done <- struct{}{}
		}()
	}

	for i := 0; i < 100; i++ {
		<-done
	}

	if got := s.BlocksDeleted(); got != 100 {
		t.Errorf("BlocksDeleted() = %d after 100 concurrent increments, want 100", got)
	}
}

func TestGCStatus_Formatting(t *testing.T) {
	status := GCStatus{
		Enabled:            true,
		DryRun:             false,
		LastWorkerRun:      "never",
		LastScanRun:        "never",
		QueueSize:          0,
		BlocksDeletedTotal: 0,
	}

	if !status.Enabled {
		t.Error("expected Enabled=true")
	}
	if status.DryRun {
		t.Error("expected DryRun=false")
	}
	if status.LastWorkerRun != "never" {
		t.Errorf("LastWorkerRun = %q, want %q", status.LastWorkerRun, "never")
	}
}

func TestGCStatus_JSONAlwaysIncludesLastScanError(t *testing.T) {
	raw, err := json.Marshal(GCStatus{})
	if err != nil {
		t.Fatalf("Marshal(GCStatus{}) failed: %v", err)
	}
	if !strings.Contains(string(raw), `"last_scan_error":""`) {
		t.Fatalf("GCStatus JSON = %s, want last_scan_error to be present even when empty", raw)
	}
}

func TestNewService_WithMockStore(t *testing.T) {
	cfg := config.GCConfig{
		Enabled:        true,
		WorkerInterval: 30 * time.Second,
		ScanInterval:   24 * time.Hour,
		BatchSize:      100,
		GracePeriod:    1 * time.Hour,
		DryRun:         false,
	}

	store := NewMockStore()
	svc := NewService(store, nil, cfg, nil)

	if svc == nil {
		t.Fatal("NewService returned nil")
	}
	if svc.queue == nil {
		t.Error("queue should be initialized")
	}
	if svc.worker == nil {
		t.Error("worker should be initialized")
	}
	if svc.scanner == nil {
		t.Error("scanner should be initialized")
	}
	if svc.stats == nil {
		t.Error("stats should be initialized")
	}
}

func TestNewService_ConfigPropagation(t *testing.T) {
	cfg := config.GCConfig{
		Enabled:        true,
		WorkerInterval: 45 * time.Second,
		ScanInterval:   12 * time.Hour,
		BatchSize:      50,
		GracePeriod:    2 * time.Hour,
		DryRun:         true,
	}

	store := NewMockStore()
	svc := NewService(store, nil, cfg, nil)

	if svc.config.BatchSize != 50 {
		t.Errorf("config.BatchSize = %d, want 50", svc.config.BatchSize)
	}
	if svc.config.GracePeriod != 2*time.Hour {
		t.Errorf("config.GracePeriod = %v, want 2h", svc.config.GracePeriod)
	}
	if svc.config.DryRun != true {
		t.Error("config.DryRun should be true")
	}
	if svc.worker.dryRun != true {
		t.Error("worker.dryRun should propagate from config")
	}
}

func TestService_SetDryRun(t *testing.T) {
	cfg := config.GCConfig{
		Enabled: true,
		DryRun:  false,
	}

	store := NewMockStore()
	svc := NewService(store, nil, cfg, nil)

	if svc.config.DryRun {
		t.Error("initial DryRun should be false")
	}

	svc.SetDryRun(true)

	if !svc.config.DryRun {
		t.Error("config.DryRun should be true after SetDryRun(true)")
	}
	if !svc.worker.dryRun {
		t.Error("worker.dryRun should be true after SetDryRun(true)")
	}

	svc.SetDryRun(false)

	if svc.config.DryRun {
		t.Error("config.DryRun should be false after SetDryRun(false)")
	}
}

func TestService_SetDryRun_Concurrent(t *testing.T) {
	cfg := config.GCConfig{
		Enabled: true,
		DryRun:  false,
	}

	store := NewMockStore()
	svc := NewService(store, nil, cfg, nil)

	// Concurrent SetDryRun calls should not race
	done := make(chan struct{})
	for i := 0; i < 50; i++ {
		go func(val bool) {
			svc.SetDryRun(val)
			done <- struct{}{}
		}(i%2 == 0)
	}

	for i := 0; i < 50; i++ {
		<-done
	}

	// Just verify no panic/race occurred; final value is non-deterministic
	_ = svc.config.DryRun
}

func TestService_StartStop(t *testing.T) {
	cfg := config.GCConfig{
		Enabled:        true,
		WorkerInterval: 100 * time.Millisecond,
		ScanInterval:   100 * time.Millisecond,
		BatchSize:      10,
		GracePeriod:    1 * time.Hour,
		DryRun:         true,
	}

	store := NewMockStore()
	svc := NewService(store, nil, cfg, nil)

	// Start the service
	svc.Start()
	if !svc.started {
		t.Error("service should be started")
	}

	// Double-start should be a no-op
	svc.Start()
	if !svc.started {
		t.Error("service should still be started after double-start")
	}

	// Allow worker/scanner to run at least once
	time.Sleep(250 * time.Millisecond)

	// Stop the service
	svc.Stop()
	if svc.started {
		t.Error("service should not be started after Stop")
	}

	// Double-stop should be safe
	svc.Stop()
}

func TestService_RunWorkerOnce_SkipsWithoutLeadership(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	worker := NewWorker(store, nil, q, 100, 0, false, stats)
	lease := &fakeLeaderLease{allowed: false}

	orgID := uuid.New()
	store.AddBlock(orgID, "lease-block", "hot", 0)
	store.EnqueueItem(orgID, time.Now().Add(-2*time.Hour), ItemBlock, "lease-block", uuid.Nil, "hot", 0)

	svc := &Service{
		store:   store,
		config:  config.GCConfig{Enabled: true},
		queue:   q,
		worker:  worker,
		scanner: NewScanner(store, q, stats, config.GCConfig{}),
		stats:   stats,
		lease:   lease,
	}

	svc.runWorkerOnce(context.Background())

	if store.GetBlock(orgID, "lease-block") == nil {
		t.Fatal("worker should not process blocks without leadership")
	}
	if stats.BlocksDeleted() != 0 {
		t.Fatalf("expected 0 deleted blocks without leadership, got %d", stats.BlocksDeleted())
	}
}

func TestService_RunWorkerOnce_ProcessesWithLeadership(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	worker := NewWorker(store, nil, q, 100, 0, false, stats)
	lease := &fakeLeaderLease{allowed: true}

	orgID := uuid.New()
	store.AddBlock(orgID, "lease-block", "hot", 0)
	store.EnqueueItem(orgID, time.Now().Add(-2*time.Hour), ItemBlock, "lease-block", uuid.Nil, "hot", 0)

	svc := &Service{
		store:   store,
		config:  config.GCConfig{Enabled: true},
		queue:   q,
		worker:  worker,
		scanner: NewScanner(store, q, stats, config.GCConfig{}),
		stats:   stats,
		lease:   lease,
	}

	svc.runWorkerOnce(context.Background())

	if stats.BlocksDeleted() != 1 {
		t.Fatalf("expected 1 deleted block with leadership, got %d", stats.BlocksDeleted())
	}
	if store.GetBlock(orgID, "lease-block") != nil {
		t.Fatal("worker should process blocks when leadership is held")
	}
}

func TestService_RunScannerOnce_RecordsScanErrorWithLeadership(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	lease := &fakeLeaderLease{allowed: true}

	orgID := uuid.New()
	store.AddOrganization(orgID)
	store.AddShareLink("token-expired", orgID, time.Now().Add(-24*time.Hour))
	store.deleteExpiredShareLinkErr = errors.New("delete failed")

	svc := &Service{
		store:   store,
		config:  config.GCConfig{Enabled: true},
		queue:   q,
		worker:  NewWorker(store, nil, q, 100, 0, false, stats),
		scanner: NewScanner(store, q, stats, config.GCConfig{}),
		stats:   stats,
		lease:   lease,
	}

	svc.runScannerOnce(context.Background())

	if got := svc.stats.LastScanError(); got == "" {
		t.Fatal("expected scanner error to be recorded")
	}
	if status := svc.Status(); status.LastScanError == "" {
		t.Fatal("expected status to expose last scanner error")
	}
	if svc.stats.LastScanRun().IsZero() {
		t.Fatal("expected scan run timestamp to be recorded")
	}
}

func TestService_RunScannerOnce_SkipsAutoRetryWithoutLeadership(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	q := NewQueue(store)
	lease := &fakeLeaderLease{allowed: false}
	orgID := uuid.New()
	libID := uuid.New()
	identityAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)
	failedAt := time.Now().UTC().Truncate(time.Millisecond)
	itemID := "heavy-root-child"

	store.failedItems[orgID] = []GCFailedItemInfo{{
		OrgID:                       orgID,
		FailedAt:                    failedAt,
		QueuedAt:                    identityAt,
		IdentityAt:                  identityAt,
		RequiresLibraryDeletedCheck: true,
		ItemType:                    ItemFSObject,
		ItemID:                      itemID,
		LibraryID:                   libID,
		LastError:                   "library " + libID.String() + " hard delete already in progress for child " + itemID,
		FailureCode:                 GCFailureCodeLibraryHardDeleteInProgress,
		ResolvedState:               "open",
	}}

	svc := &Service{
		store:   store,
		config:  config.GCConfig{Enabled: true},
		queue:   q,
		worker:  NewWorker(store, nil, q, 100, 0, false, stats),
		scanner: NewScanner(store, q, stats, config.GCConfig{}),
		stats:   stats,
		lease:   lease,
	}

	svc.runScannerOnce(context.Background())

	if got := len(store.FailedItems(orgID)); got != 1 {
		t.Fatalf("expected failed item to remain in DLQ without leadership, got %d items", got)
	}
	if got := len(store.QueueItems(orgID)); got != 0 {
		t.Fatalf("expected no queued items without leadership, got %d", got)
	}
	if !svc.stats.LastScanRun().IsZero() {
		t.Fatal("scanner should not record a scan run without leadership")
	}
}

func TestService_RunScannerOnce_ClearsLastScanErrorOnSuccess(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	stats.SetLastScanError("previous failure")
	q := NewQueue(store)
	lease := &fakeLeaderLease{allowed: true}

	svc := &Service{
		store:   store,
		config:  config.GCConfig{Enabled: true},
		queue:   q,
		worker:  NewWorker(store, nil, q, 100, 0, false, stats),
		scanner: NewScanner(store, q, stats, config.GCConfig{}),
		stats:   stats,
		lease:   lease,
	}

	svc.runScannerOnce(context.Background())

	if got := svc.stats.LastScanError(); got != "" {
		t.Fatalf("LastScanError = %q, want empty", got)
	}
	if status := svc.Status(); status.LastScanError != "" {
		t.Fatalf("Status().LastScanError = %q, want empty", status.LastScanError)
	}
}

func TestService_DeleteFailedItem_RequiresLeadership(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	failedAt := time.Now().UTC()
	store.failedItems[orgID] = []GCFailedItemInfo{{
		OrgID:      orgID,
		FailedAt:   failedAt,
		QueuedAt:   failedAt.Add(-time.Minute),
		ItemType:   ItemBlock,
		ItemID:     "blocked",
		LibraryID:  uuid.Nil,
		RetryCount: 5,
	}}

	svc := &Service{
		store:  store,
		config: config.GCConfig{Enabled: true},
		lease:  &fakeLeaderLease{allowed: false},
	}

	err := svc.DeleteFailedItem(orgID, failedAt, ItemBlock, "blocked")
	if !errors.Is(err, ErrNotLeader) {
		t.Fatalf("DeleteFailedItem error = %v, want ErrNotLeader", err)
	}
	if got := len(store.FailedItems(orgID)); got != 1 {
		t.Fatalf("expected failed item to remain untouched on follower, got %d items", got)
	}
}

func TestService_Status_UsesDirtySnapshot(t *testing.T) {
	store := NewMockStore()
	store.gcStats[gcStatKeyTotalQueue] = "11"
	store.gcStats[gcStatKeyTotalFailed] = "4"
	store.gcStats[gcStatKeyTotalDirtyOrgs] = "7"
	store.gcStats[gcStatKeyLastReconcile] = time.Now().UTC().Format(time.RFC3339)

	store.dirtyQueueOrgs[uuid.New()] = time.Now().UTC()
	store.dirtyQueueOrgs[uuid.New()] = time.Now().UTC()

	svc := &Service{store: store, config: config.GCConfig{Enabled: true}, stats: &Stats{}}
	status := svc.Status()

	if status.QueueSize != 11 {
		t.Fatalf("status.QueueSize = %d, want 11", status.QueueSize)
	}
	if status.FailedItemsTotal != 4 {
		t.Fatalf("status.FailedItemsTotal = %d, want 4", status.FailedItemsTotal)
	}
	if status.DirtyOrgsTotal != 7 {
		t.Fatalf("status.DirtyOrgsTotal = %d, want 7", status.DirtyOrgsTotal)
	}
}

func TestService_ReconcileDirtyQueueStats_SerializesRuns(t *testing.T) {
	store := NewMockStore()
	orgA := uuid.New()
	orgB := uuid.New()
	store.dirtyQueueOrgs[orgA] = time.Now().Add(-time.Minute)
	store.dirtyQueueOrgs[orgB] = time.Now().Add(-time.Minute)
	store.activeQueueOrgs[orgA] = time.Now().Add(-time.Minute)
	store.activeQueueOrgs[orgB] = time.Now().Add(-time.Minute)

	var inFlight atomic.Int32
	var maxInFlight atomic.Int32
	release := make(chan struct{})
	store.recountQueueDepthHook = func(orgID uuid.UUID, depth int) {
		current := inFlight.Add(1)
		for {
			max := maxInFlight.Load()
			if current <= max || maxInFlight.CompareAndSwap(max, current) {
				break
			}
		}
		<-release
		inFlight.Add(-1)
	}

	svc := &Service{store: store, config: config.GCConfig{Enabled: true}}

	done := make(chan struct{}, 2)
	go func() {
		svc.reconcileDirtyQueueStats(1)
		done <- struct{}{}
	}()
	go func() {
		svc.reconcileDirtyQueueStats(1)
		done <- struct{}{}
	}()

	for i := 0; i < 2; i++ {
		release <- struct{}{}
	}
	for i := 0; i < 2; i++ {
		<-done
	}

	if got := maxInFlight.Load(); got != 1 {
		t.Fatalf("expected serialized reconcile passes, max concurrent in-flight recounts = %d", got)
	}
}

// TestService_Status_SnapshotAgeSentinelWhenNeverReconciled verifies the
// reporter distinguishes "no reconcile yet" (-1) from "reconciled 0s ago" (0).
// Without the sentinel, dashboards would show fresh data on a cold deploy.
func TestService_Status_SnapshotAgeSentinelWhenNeverReconciled(t *testing.T) {
	store := NewMockStore()
	// Mimic the migration's empty-string seed for last_reconcile_run.
	store.gcStats[gcStatKeyLastReconcile] = ""

	svc := &Service{store: store, config: config.GCConfig{Enabled: true}, stats: &Stats{}}
	status := svc.Status()

	if status.SnapshotAgeSeconds != -1 {
		t.Fatalf("status.SnapshotAgeSeconds = %d, want -1 (no reconcile yet)", status.SnapshotAgeSeconds)
	}
	if status.LastReconcileRun != "never" {
		t.Fatalf("status.LastReconcileRun = %q, want %q", status.LastReconcileRun, "never")
	}
}

// TestService_Status_SnapshotAgeReflectsRecentReconcile verifies the snapshot
// age is positive (or zero) once a reconcile has run.
func TestService_Status_SnapshotAgeReflectsRecentReconcile(t *testing.T) {
	store := NewMockStore()
	store.gcStats[gcStatKeyLastReconcile] = time.Now().Add(-30 * time.Second).UTC().Format(time.RFC3339)

	svc := &Service{store: store, config: config.GCConfig{Enabled: true}, stats: &Stats{}}
	status := svc.Status()

	if status.SnapshotAgeSeconds < 0 {
		t.Fatalf("status.SnapshotAgeSeconds = %d, want >= 0 after a real reconcile", status.SnapshotAgeSeconds)
	}
	if status.SnapshotAgeSeconds > 60 {
		t.Fatalf("status.SnapshotAgeSeconds = %d, want roughly ~30", status.SnapshotAgeSeconds)
	}
}

func TestService_ListFailedItemOrgs_SortsAndLimits(t *testing.T) {
	store := NewMockStore()
	orgA := uuid.New()
	orgB := uuid.New()
	orgC := uuid.New()
	store.AddOrganizationWithName(orgA, "alpha")
	store.AddOrganizationWithName(orgB, "beta")
	store.AddOrganizationWithName(orgC, "gamma")
	older := time.Now().UTC().Add(-2 * time.Hour)
	newer := time.Now().UTC().Add(-1 * time.Hour)
	if err := store.SaveOrgQueueStats(GCOrgStats{OrgID: orgA, FailedDepth: 2, UpdatedAt: older}); err != nil {
		t.Fatalf("SaveOrgQueueStats(orgA): %v", err)
	}
	if err := store.SaveOrgQueueStats(GCOrgStats{OrgID: orgB, FailedDepth: 5, UpdatedAt: newer}); err != nil {
		t.Fatalf("SaveOrgQueueStats(orgB): %v", err)
	}
	if err := store.SaveOrgQueueStats(GCOrgStats{OrgID: orgC, FailedDepth: 0, UpdatedAt: newer}); err != nil {
		t.Fatalf("SaveOrgQueueStats(orgC): %v", err)
	}
	store.failedItems[orgA] = []GCFailedItemInfo{
		{OrgID: orgA, FailedAt: older.Add(1 * time.Minute), ItemType: ItemBlock, ItemID: "alpha-1"},
		{OrgID: orgA, FailedAt: older.Add(2 * time.Minute), ItemType: ItemBlock, ItemID: "alpha-2"},
	}
	store.failedItems[orgB] = []GCFailedItemInfo{
		{OrgID: orgB, FailedAt: newer.Add(1 * time.Minute), ItemType: ItemBlock, ItemID: "beta-1"},
		{OrgID: orgB, FailedAt: newer.Add(2 * time.Minute), ItemType: ItemBlock, ItemID: "beta-2"},
		{OrgID: orgB, FailedAt: newer.Add(3 * time.Minute), ItemType: ItemBlock, ItemID: "beta-3"},
		{OrgID: orgB, FailedAt: newer.Add(4 * time.Minute), ItemType: ItemBlock, ItemID: "beta-4"},
		{OrgID: orgB, FailedAt: newer.Add(5 * time.Minute), ItemType: ItemBlock, ItemID: "beta-5"},
	}
	svc := NewService(store, nil, config.GCConfig{Enabled: true}, nil)

	orgs, err := svc.ListFailedItemOrgs(1)
	if err != nil {
		t.Fatalf("ListFailedItemOrgs: %v", err)
	}
	if len(orgs) != 1 {
		t.Fatalf("len(orgs) = %d, want 1", len(orgs))
	}
	if orgs[0].OrgID != orgB {
		t.Fatalf("orgs[0].OrgID = %s, want %s", orgs[0].OrgID, orgB)
	}
	if orgs[0].OrgName != "beta" {
		t.Fatalf("orgs[0].OrgName = %q, want beta", orgs[0].OrgName)
	}
	if orgs[0].FailedItemsTotal != 5 {
		t.Fatalf("orgs[0].FailedItemsTotal = %d, want 5", orgs[0].FailedItemsTotal)
	}

	orgs, err = svc.ListFailedItemOrgs(10)
	if err != nil {
		t.Fatalf("ListFailedItemOrgs(all): %v", err)
	}
	if len(orgs) != 2 {
		t.Fatalf("len(orgs) = %d, want 2", len(orgs))
	}
	if orgs[1].OrgID != orgA {
		t.Fatalf("orgs[1].OrgID = %s, want %s", orgs[1].OrgID, orgA)
	}
}

func TestService_ListFailedItemOrgs_FiltersStaleSnapshotAndIncludesDirtyActualFailures(t *testing.T) {
	store := NewMockStore()
	staleOrg := uuid.New()
	realOrg := uuid.New()
	dirtyOrg := uuid.New()
	store.AddOrganizationWithName(staleOrg, "stale")
	store.AddOrganizationWithName(realOrg, "real")
	store.AddOrganizationWithName(dirtyOrg, "dirty")
	now := time.Now().UTC().Truncate(time.Second)

	if err := store.SaveOrgQueueStats(GCOrgStats{OrgID: staleOrg, FailedDepth: 1, UpdatedAt: now.Add(-3 * time.Hour)}); err != nil {
		t.Fatalf("SaveOrgQueueStats(staleOrg): %v", err)
	}
	if err := store.SaveOrgQueueStats(GCOrgStats{OrgID: realOrg, FailedDepth: 1, UpdatedAt: now.Add(-2 * time.Hour)}); err != nil {
		t.Fatalf("SaveOrgQueueStats(realOrg): %v", err)
	}
	store.failedItems[realOrg] = []GCFailedItemInfo{{
		OrgID:    realOrg,
		FailedAt: now.Add(-10 * time.Minute),
		ItemType: ItemBlock,
		ItemID:   "real-item",
	}}

	if err := store.MarkOrgDirty(dirtyOrg, now); err != nil {
		t.Fatalf("MarkOrgDirty(dirtyOrg): %v", err)
	}
	store.failedItems[dirtyOrg] = []GCFailedItemInfo{{
		OrgID:    dirtyOrg,
		FailedAt: now.Add(-5 * time.Minute),
		ItemType: ItemBlock,
		ItemID:   "dirty-item",
	}}

	svc := NewService(store, nil, config.GCConfig{Enabled: true}, nil)

	orgs, err := svc.ListFailedItemOrgs(10)
	if err != nil {
		t.Fatalf("ListFailedItemOrgs: %v", err)
	}
	if len(orgs) != 2 {
		t.Fatalf("len(orgs) = %d, want 2", len(orgs))
	}
	for _, org := range orgs {
		if org.OrgID == staleOrg {
			t.Fatalf("stale org %s should have been filtered out", staleOrg)
		}
	}
	foundDirty := false
	for _, org := range orgs {
		if org.OrgID == dirtyOrg {
			foundDirty = true
			if org.FailedItemsTotal != 1 {
				t.Fatalf("dirty org FailedItemsTotal = %d, want 1", org.FailedItemsTotal)
			}
		}
	}
	if !foundDirty {
		t.Fatalf("dirty org %s was not included", dirtyOrg)
	}
}

// TestService_DLQOps_SerializeUnderConcurrency exercises the dlqOpsMu guard
// directly. The hook is wired to the store-level DLQ mutations so the test
// observes overlap of the actual non-atomic SELECT+INSERT+DELETE in
// RequeueFailedItem, not the downstream reconcile (which is already
// serialized by reconcileMu and would mask the absence of dlqOpsMu).
//
// Without dlqOpsMu, two concurrent admin requests would interleave inside
// the store and the hook would record max in-flight > 1.
func TestService_DLQOps_SerializeUnderConcurrency(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	failedAt := time.Now().UTC().Truncate(time.Millisecond)

	store.failedItems[orgID] = []GCFailedItemInfo{
		{OrgID: orgID, FailedAt: failedAt, QueuedAt: failedAt.Add(-time.Minute), ItemType: ItemBlock, ItemID: "race-item-a", RetryCount: 5},
		{OrgID: orgID, FailedAt: failedAt.Add(time.Second), QueuedAt: failedAt, ItemType: ItemBlock, ItemID: "race-item-b", RetryCount: 5},
	}

	svc := &Service{
		store:  store,
		config: config.GCConfig{Enabled: true},
		lease:  &fakeLeaderLease{allowed: true},
	}

	var inFlight atomic.Int32
	var maxInFlight atomic.Int32
	var sawRequeue, sawDelete atomic.Bool
	// Hook fires at the top of the mock's DLQ store ops, BEFORE the
	// internal mock mutex is taken. If dlqOpsMu were missing, both
	// goroutines would enter here simultaneously and inFlight would
	// reach 2 during the brief sleep.
	store.dlqOpHook = func(_ uuid.UUID, op string) {
		switch op {
		case "requeue":
			sawRequeue.Store(true)
		case "delete":
			sawDelete.Store(true)
		}
		current := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			max := maxInFlight.Load()
			if current <= max || maxInFlight.CompareAndSwap(max, current) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	done := make(chan struct{}, 2)
	go func() {
		_ = svc.RequeueFailedItem(orgID, failedAt, ItemBlock, "race-item-a")
		done <- struct{}{}
	}()
	go func() {
		_ = svc.DeleteFailedItem(orgID, failedAt.Add(time.Second), ItemBlock, "race-item-b")
		done <- struct{}{}
	}()

	for i := 0; i < 2; i++ {
		<-done
	}

	// Sanity: both ops actually hit the store. If a code change ever
	// short-circuited one of them before the store call, the hook
	// signal-only result would be a false-negative on serialization.
	if !sawRequeue.Load() || !sawDelete.Load() {
		t.Fatalf("expected both DLQ ops to reach the store hook; saw requeue=%v delete=%v", sawRequeue.Load(), sawDelete.Load())
	}
	if got := maxInFlight.Load(); got > 1 {
		t.Fatalf("expected DLQ admin ops to be serialized by dlqOpsMu, max concurrent store ops = %d", got)
	}
}

func TestService_RequeueFailedCascade_PreservesIdentityAt(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	deletedAt := time.Now().Add(-3 * time.Hour).UTC().Truncate(time.Millisecond)
	failedAt := time.Now().UTC().Truncate(time.Millisecond)
	itemID := uuid.New().String()

	store.failedItems[orgID] = []GCFailedItemInfo{{
		OrgID:        orgID,
		FailedAt:     failedAt,
		QueuedAt:     failedAt.Add(-time.Minute),
		IdentityAt:   deletedAt,
		ItemType:     ItemLibraryCascade,
		ItemID:       itemID,
		LibraryID:    uuid.Nil,
		StorageClass: "hot",
		RetryCount:   5,
	}}

	svc := &Service{
		store:  store,
		config: config.GCConfig{Enabled: true},
		lease:  &fakeLeaderLease{allowed: true},
	}

	if err := svc.RequeueFailedItem(orgID, failedAt, ItemLibraryCascade, itemID); err != nil {
		t.Fatalf("RequeueFailedItem failed: %v", err)
	}

	items := store.QueueItems(orgID)
	if len(items) != 1 {
		t.Fatalf("expected 1 queue item after requeue, got %d", len(items))
	}
	if items[0].QueuedAt.Equal(deletedAt) {
		t.Fatalf("requeued cascade item kept deleted_at as queued_at; want a fresh queue position")
	}
	if !items[0].IdentityAt.Equal(deletedAt) {
		t.Fatalf("requeued cascade item IdentityAt = %v, want %v", items[0].IdentityAt, deletedAt)
	}
}

func TestService_RetryAutoRecoverableFailedItems_RequeuesMissingLibraryChildren(t *testing.T) {
	store := NewMockStore()
	orgID := uuid.New()
	libID := uuid.New()
	identityAt := time.Now().Add(-2 * time.Hour).UTC().Truncate(time.Millisecond)
	failedAt := time.Now().UTC().Truncate(time.Millisecond)
	itemID := "heavy-root-child"

	store.failedItems[orgID] = []GCFailedItemInfo{{
		OrgID:                       orgID,
		FailedAt:                    failedAt,
		QueuedAt:                    identityAt,
		IdentityAt:                  identityAt,
		RequiresLibraryDeletedCheck: true,
		ItemType:                    ItemFSObject,
		ItemID:                      itemID,
		LibraryID:                   libID,
		StorageClass:                "hot",
		RetryCount:                  5,
		LastError:                   "library " + libID.String() + " hard delete already in progress for child " + itemID,
		FailureCode:                 GCFailureCodeLibraryHardDeleteInProgress,
		ResolvedState:               "open",
	}}

	svc := &Service{
		store:  store,
		config: config.GCConfig{Enabled: true},
		lease:  &fakeLeaderLease{allowed: true},
	}

	if retried := svc.retryAutoRecoverableFailedItems(); retried != 1 {
		t.Fatalf("retryAutoRecoverableFailedItems retried %d items, want 1", retried)
	}

	failedItems, err := store.ListFailedItems(orgID, 10)
	if err != nil {
		t.Fatalf("ListFailedItems after auto-requeue: %v", err)
	}
	if len(failedItems) != 0 {
		t.Fatalf("expected failed item to be removed from DLQ, got %d entries", len(failedItems))
	}

	items := store.QueueItems(orgID)
	if len(items) != 1 {
		t.Fatalf("expected 1 queue item after auto-requeue, got %d", len(items))
	}
	if items[0].ItemType != ItemFSObject || items[0].ItemID != itemID {
		t.Fatalf("unexpected requeued item: type=%s id=%s", items[0].ItemType, items[0].ItemID)
	}
	if items[0].LibraryID != libID {
		t.Fatalf("requeued item library_id = %s, want %s", items[0].LibraryID, libID)
	}
	if !items[0].IdentityAt.Equal(identityAt) {
		t.Fatalf("requeued item IdentityAt = %v, want %v", items[0].IdentityAt, identityAt)
	}
	if !items[0].RequiresLibraryDeletedCheck {
		t.Fatal("requeued item lost RequiresLibraryDeletedCheck flag")
	}
}

func TestService_ReconcileDirtyQueueStats_CorrectsSnapshotDrift(t *testing.T) {
	store := NewMockStore()
	orgA := uuid.New()
	orgB := uuid.New()
	store.orgQueueStats[orgA] = GCOrgStats{OrgID: orgA, QueueDepth: 3, FailedDepth: 1}
	store.orgQueueStats[orgB] = GCOrgStats{OrgID: orgB, QueueDepth: 5, FailedDepth: 2}
	now := time.Now().UTC()
	store.failedItems[orgA] = []GCFailedItemInfo{{OrgID: orgA, FailedAt: now.Add(-3 * time.Minute), ItemType: ItemBlock, ItemID: "orgA-failed"}}
	store.failedItems[orgB] = []GCFailedItemInfo{
		{OrgID: orgB, FailedAt: now.Add(-2 * time.Minute), ItemType: ItemBlock, ItemID: "orgB-failed-1"},
		{OrgID: orgB, FailedAt: now.Add(-1 * time.Minute), ItemType: ItemBlock, ItemID: "orgB-failed-2"},
	}
	store.gcStats[gcStatKeyTotalQueue] = "1"
	store.gcStats[gcStatKeyTotalFailed] = "0"
	store.gcStats[gcStatKeyTotalDirtyOrgs] = "9"

	svc := &Service{store: store, config: config.GCConfig{Enabled: true}, reconcilePasses: gcSnapshotDriftCheckEvery - 1}
	svc.reconcileDirtyQueueStats(0)

	queueSnapshot, _ := store.LoadGCStats(gcStatKeyTotalQueue)
	failedSnapshot, _ := store.LoadGCStats(gcStatKeyTotalFailed)
	dirtySnapshot, _ := store.LoadGCStats(gcStatKeyTotalDirtyOrgs)
	if queueSnapshot != "8" {
		t.Fatalf("queue snapshot = %q, want 8", queueSnapshot)
	}
	if failedSnapshot != "3" {
		t.Fatalf("failed snapshot = %q, want 3", failedSnapshot)
	}
	if dirtySnapshot != "0" {
		t.Fatalf("dirty snapshot = %q, want 0", dirtySnapshot)
	}
}

func TestService_RefreshFailedItemSnapshot_ClearsExpiredFailedCounts(t *testing.T) {
	store := NewMockStore()
	staleOrgA := uuid.New()
	staleOrgB := uuid.New()
	store.AddOrganizationWithName(staleOrgA, "stale-a")
	store.AddOrganizationWithName(staleOrgB, "stale-b")
	store.orgQueueStats[staleOrgA] = GCOrgStats{OrgID: staleOrgA, FailedDepth: 1, UpdatedAt: time.Now().UTC().Add(-2 * time.Hour)}
	store.orgQueueStats[staleOrgB] = GCOrgStats{OrgID: staleOrgB, FailedDepth: 2, UpdatedAt: time.Now().UTC().Add(-90 * time.Minute)}
	store.gcStats[gcStatKeyTotalFailed] = "3"

	svc := NewService(store, nil, config.GCConfig{Enabled: true}, nil)
	svc.RefreshFailedItemSnapshot()

	status := svc.Status()
	if status.FailedItemsTotal != 0 {
		t.Fatalf("status.FailedItemsTotal = %d, want 0", status.FailedItemsTotal)
	}
	if got := store.orgQueueStats[staleOrgA].FailedDepth; got != 0 {
		t.Fatalf("staleOrgA failed depth = %d, want 0", got)
	}
	if got := store.orgQueueStats[staleOrgB].FailedDepth; got != 0 {
		t.Fatalf("staleOrgB failed depth = %d, want 0", got)
	}
	orgs, err := svc.ListFailedItemOrgs(10)
	if err != nil {
		t.Fatalf("ListFailedItemOrgs: %v", err)
	}
	if len(orgs) != 0 {
		t.Fatalf("len(orgs) = %d, want 0", len(orgs))
	}
}

func TestMockStore_ListDistinctArtifactLibraries_UsesLiveAndDeletedLibraries(t *testing.T) {
	store := NewMockStore()
	liveLib := uuid.New()
	deletedLib := uuid.New()
	orgID := uuid.New()

	store.libraries[liveLib] = &mockLibrary{OrgID: orgID, LibraryID: liveLib}
	store.deletedLibraries[deletedLib] = &mockDeletedLibrary{OrgID: orgID, LibraryID: deletedLib, DeletedAt: time.Now().UTC()}

	commitLibs, err := store.ListDistinctCommitLibraries()
	if err != nil {
		t.Fatalf("ListDistinctCommitLibraries: %v", err)
	}
	if len(commitLibs) != 2 {
		t.Fatalf("len(commitLibs) = %d, want 2", len(commitLibs))
	}

	fsLibs, err := store.ListDistinctFSObjectLibraries()
	if err != nil {
		t.Fatalf("ListDistinctFSObjectLibraries: %v", err)
	}
	if len(fsLibs) != 2 {
		t.Fatalf("len(fsLibs) = %d, want 2", len(fsLibs))
	}
}

func TestService_Start_AcquiresLeaseBeforeImmediateScannerRun(t *testing.T) {
	store := NewMockStore()
	stats := &Stats{}
	lease := &fakeLeaderLease{
		allowed:  true,
		delay:    75 * time.Millisecond,
		acquired: make(chan struct{}),
	}

	svc := &Service{
		store:          store,
		config:         config.GCConfig{Enabled: true, WorkerInterval: time.Minute, ScanInterval: time.Minute, BatchSize: 10, DryRun: true},
		queue:          NewQueue(store),
		worker:         NewWorker(store, nil, NewQueue(store), 10, 0, true, stats),
		scanner:        NewScanner(store, NewQueue(store), stats, config.GCConfig{}),
		stats:          stats,
		lease:          lease,
		triggerWorker:  make(chan struct{}, 1),
		triggerScanner: make(chan struct{}, 1),
	}

	start := time.Now()
	svc.Start()
	defer svc.Stop()

	if atomic.LoadInt32(&lease.calls) == 0 {
		t.Fatal("Start should acquire the lease before returning")
	}
	if time.Since(start) < lease.delay {
		t.Fatal("Start should wait for the initial lease acquisition")
	}

	select {
	case <-lease.acquired:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("expected lease acquisition signal")
	}

	deadline := time.Now().Add(250 * time.Millisecond)
	for time.Now().Before(deadline) {
		if !svc.stats.LastScanRun().IsZero() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected immediate startup scan after acquiring lease")
}

func TestService_StartStop_Concurrent(t *testing.T) {
	cfg := config.GCConfig{
		Enabled:        true,
		WorkerInterval: 50 * time.Millisecond,
		ScanInterval:   50 * time.Millisecond,
		BatchSize:      10,
		GracePeriod:    1 * time.Hour,
		DryRun:         true,
	}

	store := NewMockStore()
	svc := NewService(store, nil, cfg, nil)

	// Concurrent Start/Stop/SetDryRun should not race
	done := make(chan struct{})
	for i := 0; i < 20; i++ {
		go func(idx int) {
			switch idx % 3 {
			case 0:
				svc.Start()
			case 1:
				svc.Stop()
			case 2:
				svc.SetDryRun(idx%2 == 0)
			}
			done <- struct{}{}
		}(i)
	}

	for i := 0; i < 20; i++ {
		<-done
	}

	// Ensure clean stop
	svc.Stop()
}

func TestService_StatusAfterActivity(t *testing.T) {
	cfg := config.GCConfig{
		Enabled:        true,
		WorkerInterval: 50 * time.Millisecond,
		ScanInterval:   50 * time.Millisecond,
		BatchSize:      10,
		GracePeriod:    1 * time.Hour,
		DryRun:         true,
	}

	store := NewMockStore()
	svc := NewService(store, nil, cfg, nil)

	svc.Start()
	time.Sleep(150 * time.Millisecond)

	status := svc.Status()
	if !status.Enabled {
		t.Error("status.Enabled should be true")
	}
	if !status.DryRun {
		t.Error("status.DryRun should be true")
	}
	if status.LastWorkerRun == "never" {
		t.Error("LastWorkerRun should not be 'never' after running")
	}

	svc.Stop()
}

func TestService_ManualTrigger(t *testing.T) {
	cfg := config.GCConfig{
		Enabled:        true,
		WorkerInterval: 10 * time.Minute, // Long interval so only manual trigger fires
		ScanInterval:   10 * time.Minute,
		BatchSize:      10,
		GracePeriod:    1 * time.Hour,
		DryRun:         true,
	}

	store := NewMockStore()
	svc := NewService(store, nil, cfg, nil)
	svc.Start()

	// Trigger worker manually
	svc.TriggerWorker()
	time.Sleep(100 * time.Millisecond)

	status := svc.Status()
	if status.LastWorkerRun == "never" {
		t.Error("LastWorkerRun should be set after manual trigger")
	}

	svc.Stop()
}

func TestService_Start_RunsWorkerImmediately(t *testing.T) {
	cfg := config.GCConfig{
		Enabled:        true,
		WorkerInterval: 10 * time.Minute,
		ScanInterval:   10 * time.Minute,
		BatchSize:      10,
		GracePeriod:    1 * time.Hour,
		DryRun:         true,
	}

	store := NewMockStore()
	svc := NewService(store, nil, cfg, nil)
	svc.Start()
	defer svc.Stop()

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if !svc.stats.LastWorkerRun().IsZero() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatal("expected worker to run immediately on Start without waiting for the first ticker")
}

func TestService_DisabledDoesNotStart(t *testing.T) {
	cfg := config.GCConfig{
		Enabled: false,
	}

	store := NewMockStore()
	svc := NewService(store, nil, cfg, nil)
	svc.Start()

	if svc.started {
		t.Error("service should not start when disabled")
	}

	// Stop should be safe on a non-started service
	svc.Stop()
}

func TestService_TriggerChannels(t *testing.T) {
	cfg := config.GCConfig{
		Enabled: true,
	}

	store := NewMockStore()
	svc := NewService(store, nil, cfg, nil)

	// Triggers should not block even when service is not running
	svc.TriggerWorker()
	svc.TriggerScanner()

	// Double-trigger should not block (buffered channel size 1)
	svc.TriggerWorker()
	svc.TriggerWorker()
	svc.TriggerScanner()
	svc.TriggerScanner()
}

func TestService_StatusWithMockStore(t *testing.T) {
	cfg := config.GCConfig{
		Enabled: false,
		DryRun:  true,
	}

	store := NewMockStore()
	svc := NewService(store, nil, cfg, nil)
	status := svc.Status()

	if status.Enabled {
		t.Error("status.Enabled should be false")
	}
	if !status.DryRun {
		t.Error("status.DryRun should be true")
	}
	if status.LastWorkerRun != "never" {
		t.Errorf("LastWorkerRun = %q, want 'never'", status.LastWorkerRun)
	}
	if status.LastScanRun != "never" {
		t.Errorf("LastScanRun = %q, want 'never'", status.LastScanRun)
	}
	if status.LastScanError != "" {
		t.Errorf("LastScanError = %q, want empty", status.LastScanError)
	}
	if status.QueueSize != 0 {
		t.Errorf("QueueSize = %d, want 0", status.QueueSize)
	}
}

func TestService_Queue(t *testing.T) {
	cfg := config.GCConfig{}
	store := NewMockStore()
	svc := NewService(store, nil, cfg, nil)

	if svc.Queue() == nil {
		t.Error("Queue() should not return nil")
	}

	if svc.Queue() != svc.queue {
		t.Error("Queue() should return the internal queue")
	}
}

func TestService_PersistAndRestoreStats(t *testing.T) {
	store := NewMockStore()
	cfg := config.GCConfig{Enabled: true}

	// Create service and set some stats
	svc := NewService(store, nil, cfg, nil)
	workerTime := time.Date(2026, 3, 15, 10, 30, 0, 0, time.UTC)
	scanTime := time.Date(2026, 3, 15, 11, 0, 0, 0, time.UTC)
	svc.stats.SetLastWorkerRun(workerTime)
	svc.stats.SetLastScanRun(scanTime)
	svc.stats.SetLastScanError("scanner failed")
	svc.stats.blocksDeleted.Store(42)

	// Persist
	svc.persistStats()

	// Create a new service and restore — should recover the stats
	svc2 := NewService(store, nil, cfg, nil)
	svc2.restoreStats()

	if got := svc2.stats.LastWorkerRun(); !got.Equal(workerTime) {
		t.Errorf("restored LastWorkerRun = %v, want %v", got, workerTime)
	}
	if got := svc2.stats.LastScanRun(); !got.Equal(scanTime) {
		t.Errorf("restored LastScanRun = %v, want %v", got, scanTime)
	}
	if got := svc2.stats.LastScanError(); got != "scanner failed" {
		t.Errorf("restored LastScanError = %q, want %q", got, "scanner failed")
	}
	if got := svc2.stats.BlocksDeleted(); got != 42 {
		t.Errorf("restored BlocksDeleted = %d, want 42", got)
	}
}

func TestService_PersistStats_ClearsPersistedLastScanError(t *testing.T) {
	store := NewMockStore()
	cfg := config.GCConfig{Enabled: true}

	svc := NewService(store, nil, cfg, nil)
	svc.stats.SetLastScanError("scanner failed")
	svc.persistStats()

	if got, err := store.LoadGCStats("last_scan_error"); err != nil || got != "scanner failed" {
		t.Fatalf("persisted last_scan_error = (%q, %v), want (%q, nil)", got, err, "scanner failed")
	}

	svc.stats.SetLastScanError("")
	svc.persistStats()

	got, err := store.LoadGCStats("last_scan_error")
	if err != nil {
		t.Fatalf("LoadGCStats(last_scan_error) failed: %v", err)
	}
	if got != "" {
		t.Fatalf("persisted last_scan_error = %q, want empty string", got)
	}

	svc2 := NewService(store, nil, cfg, nil)
	svc2.restoreStats()
	if got := svc2.stats.LastScanError(); got != "" {
		t.Fatalf("restored LastScanError = %q, want empty", got)
	}
}

func TestService_RestoreStats_Empty(t *testing.T) {
	store := NewMockStore()
	cfg := config.GCConfig{Enabled: true}

	svc := NewService(store, nil, cfg, nil)
	svc.restoreStats()

	// Should be zero values when nothing was persisted
	if got := svc.stats.LastWorkerRun(); !got.IsZero() {
		t.Errorf("expected zero LastWorkerRun, got %v", got)
	}
	if got := svc.stats.LastScanRun(); !got.IsZero() {
		t.Errorf("expected zero LastScanRun, got %v", got)
	}
	if got := svc.stats.BlocksDeleted(); got != 0 {
		t.Errorf("expected 0 BlocksDeleted, got %d", got)
	}
}

func TestService_PersistStats_SkipsZeroTimes(t *testing.T) {
	store := NewMockStore()
	cfg := config.GCConfig{Enabled: true}

	svc := NewService(store, nil, cfg, nil)
	// Only set blocksDeleted, leave times at zero
	svc.stats.blocksDeleted.Store(10)
	svc.persistStats()

	// Verify only blocks_deleted_total was saved, not the zero times
	if val, _ := store.LoadGCStats("last_worker_run"); val != "" {
		t.Errorf("should not persist zero last_worker_run, got %q", val)
	}
	if val, _ := store.LoadGCStats("last_scan_run"); val != "" {
		t.Errorf("should not persist zero last_scan_run, got %q", val)
	}
	if val, _ := store.LoadGCStats("blocks_deleted_total"); val != "10" {
		t.Errorf("blocks_deleted_total = %q, want %q", val, "10")
	}
}

func TestService_Status_UsesPersistedSharedStats(t *testing.T) {
	store := NewMockStore()
	cfg := config.GCConfig{Enabled: true, GracePeriod: time.Minute}
	workerTime := time.Date(2026, 4, 30, 22, 10, 0, 0, time.UTC)
	scanAttempt := time.Date(2026, 4, 30, 22, 11, 0, 0, time.UTC)
	scanSuccess := time.Date(2026, 4, 30, 22, 11, 30, 0, time.UTC)

	if err := store.SaveGCStats(gcStatKeyLastWorkerRun, workerTime.Format(time.RFC3339)); err != nil {
		t.Fatalf("SaveGCStats(last_worker_run): %v", err)
	}
	if err := store.SaveGCStats(gcStatKeyLastScanAttempt, scanAttempt.Format(time.RFC3339)); err != nil {
		t.Fatalf("SaveGCStats(last_scan_attempt): %v", err)
	}
	if err := store.SaveGCStats(gcStatKeyLastScanSuccess, scanSuccess.Format(time.RFC3339)); err != nil {
		t.Fatalf("SaveGCStats(last_scan_success): %v", err)
	}
	if err := store.SaveGCStats(gcStatKeyLastScanError, "leader scan failed"); err != nil {
		t.Fatalf("SaveGCStats(last_scan_error): %v", err)
	}
	if err := store.SaveGCStats(gcStatKeyBlocksDeletedTotal, "42"); err != nil {
		t.Fatalf("SaveGCStats(blocks_deleted_total): %v", err)
	}

	svc := NewService(store, nil, cfg, nil)
	status := svc.Status()

	if status.LastWorkerRun != workerTime.Format(time.RFC3339) {
		t.Fatalf("Status().LastWorkerRun = %q, want %q", status.LastWorkerRun, workerTime.Format(time.RFC3339))
	}
	if status.LastScanAttempt != scanAttempt.Format(time.RFC3339) {
		t.Fatalf("Status().LastScanAttempt = %q, want %q", status.LastScanAttempt, scanAttempt.Format(time.RFC3339))
	}
	if status.LastScanSuccess != scanSuccess.Format(time.RFC3339) {
		t.Fatalf("Status().LastScanSuccess = %q, want %q", status.LastScanSuccess, scanSuccess.Format(time.RFC3339))
	}
	if status.LastScanError != "leader scan failed" {
		t.Fatalf("Status().LastScanError = %q, want %q", status.LastScanError, "leader scan failed")
	}
	if status.BlocksDeletedTotal != 42 {
		t.Fatalf("Status().BlocksDeletedTotal = %d, want 42", status.BlocksDeletedTotal)
	}
}
