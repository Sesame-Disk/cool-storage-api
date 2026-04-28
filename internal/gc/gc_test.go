package gc

import (
	"context"
	"errors"
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

func TestService_ReconcileDirtyQueueStats_CorrectsSnapshotDrift(t *testing.T) {
	store := NewMockStore()
	orgA := uuid.New()
	orgB := uuid.New()
	store.orgQueueStats[orgA] = GCOrgStats{OrgID: orgA, QueueDepth: 3, FailedDepth: 1}
	store.orgQueueStats[orgB] = GCOrgStats{OrgID: orgB, QueueDepth: 5, FailedDepth: 2}
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
	if got := svc2.stats.BlocksDeleted(); got != 42 {
		t.Errorf("restored BlocksDeleted = %d, want 42", got)
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
