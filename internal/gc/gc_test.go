package gc

import (
	"context"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/google/uuid"
)

type fakeLeaderLease struct {
	allowed bool
	err     error
	calls   int
}

func (f *fakeLeaderLease) TryAcquireOrRenew(ctx context.Context) (bool, error) {
	f.calls++
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return f.allowed, f.err
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

	if lease.calls != 1 {
		t.Fatalf("lease should be consulted once, got %d calls", lease.calls)
	}
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

	if lease.calls != 1 {
		t.Fatalf("lease should be consulted once, got %d calls", lease.calls)
	}
	if stats.BlocksDeleted() != 1 {
		t.Fatalf("expected 1 deleted block with leadership, got %d", stats.BlocksDeleted())
	}
	if store.GetBlock(orgID, "lease-block") != nil {
		t.Fatal("worker should process blocks when leadership is held")
	}
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
