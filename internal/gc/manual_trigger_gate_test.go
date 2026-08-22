package gc

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/google/uuid"
)

// The GC kill switch is the barrier the whole X1 work programme runs behind, and
// in production only one datacenter runs GC at all — every other node serves the
// superadmin trigger endpoint with GC_ENABLED=false. These tests pin the guard
// down at the Service boundary so the switch does not silently come to depend on
// "a disabled service happens to have no consumer goroutine" again.

func TestService_ManualTriggersRefusedWhenDisabled(t *testing.T) {
	svc := NewService(NewMockStore(), nil, config.GCConfig{Enabled: false, BatchSize: 10}, nil)

	// Start() on a disabled service returns before launching the loops. The service
	// object still exists and the admin route still reaches it, which is the whole
	// point of the guard.
	svc.Start()
	defer svc.Stop()

	if svc.AcceptsManualTriggers() {
		t.Fatal("AcceptsManualTriggers() = true for a disabled service, want false")
	}
	if svc.TriggerWorker() {
		t.Error("TriggerWorker() = true for a disabled service, want false")
	}
	if svc.TriggerScanner() {
		t.Error("TriggerScanner() = true for a disabled service, want false")
	}

	// Nothing may be parked in the buffered channels: a queued token would fire an
	// unrequested run the moment GC was later enabled.
	select {
	case <-svc.triggerWorker:
		t.Error("a worker trigger was queued while GC was disabled")
	default:
	}
	select {
	case <-svc.triggerScanner:
		t.Error("a scanner trigger was queued while GC was disabled")
	default:
	}
}

func TestService_ManualTriggersRefusedWhenNotStarted(t *testing.T) {
	// Enabled but never started: still no consumer, so still no honest "started".
	svc := NewService(NewMockStore(), nil, config.GCConfig{Enabled: true, BatchSize: 10}, nil)

	if svc.AcceptsManualTriggers() {
		t.Fatal("AcceptsManualTriggers() = true before Start(), want false")
	}
	if svc.TriggerWorker() {
		t.Error("TriggerWorker() = true before Start(), want false")
	}
	if svc.TriggerScanner() {
		t.Error("TriggerScanner() = true before Start(), want false")
	}
	if !errors.Is(svc.ManualTriggerError(), ErrGCNotRunning) {
		t.Fatalf("ManualTriggerError() = %v, want ErrGCNotRunning", svc.ManualTriggerError())
	}
}

func TestService_ManualTriggersAcceptedWhenEnabledAndStarted(t *testing.T) {
	cfg := config.GCConfig{
		Enabled:        true,
		WorkerInterval: 10 * time.Minute,
		ScanInterval:   10 * time.Minute,
		BatchSize:      10,
		GracePeriod:    time.Hour,
		DryRun:         true,
	}
	svc := NewService(NewMockStore(), nil, cfg, nil)
	svc.Start()
	defer svc.Stop()

	if !svc.AcceptsManualTriggers() {
		t.Fatal("AcceptsManualTriggers() = false for an enabled, started service, want true")
	}
	if !svc.TriggerWorker() {
		t.Error("TriggerWorker() = false for an enabled, started service, want true")
	}
	if !svc.TriggerScanner() {
		t.Error("TriggerScanner() = false for an enabled, started service, want true")
	}
}

func TestService_ManualTriggersRefusedAfterStop(t *testing.T) {
	cfg := config.GCConfig{
		Enabled:        true,
		WorkerInterval: 10 * time.Minute,
		ScanInterval:   10 * time.Minute,
		BatchSize:      10,
		GracePeriod:    time.Hour,
		DryRun:         true,
	}
	svc := NewService(NewMockStore(), nil, cfg, nil)
	svc.Start()
	svc.Stop()

	if svc.AcceptsManualTriggers() {
		t.Fatal("AcceptsManualTriggers() = true after Stop(), want false")
	}
	if svc.TriggerWorker() {
		t.Error("TriggerWorker() = true after Stop(), want false")
	}
	if !errors.Is(svc.ManualTriggerError(), ErrGCNotRunning) {
		t.Fatalf("ManualTriggerError() = %v, want ErrGCNotRunning", svc.ManualTriggerError())
	}
}

func TestService_ManualTriggersRefusedWithoutLeadership(t *testing.T) {
	cfg := config.GCConfig{
		Enabled:        true,
		WorkerInterval: 10 * time.Minute,
		ScanInterval:   10 * time.Minute,
		BatchSize:      10,
		GracePeriod:    time.Hour,
		DryRun:         true,
	}
	svc := NewService(NewMockStore(), nil, cfg, nil)
	svc.lease = &fakeLeaderLease{allowed: false}
	svc.Start()
	defer svc.Stop()

	if !errors.Is(svc.ManualTriggerError(), ErrNotLeader) {
		t.Fatalf("ManualTriggerError() = %v, want ErrNotLeader", svc.ManualTriggerError())
	}
	if svc.AcceptsManualTriggers() {
		t.Fatal("AcceptsManualTriggers() = true on a follower, want false")
	}
	if svc.TriggerWorker() || svc.TriggerScanner() {
		t.Fatal("follower accepted a manual trigger")
	}
}

func TestService_RejectedDryRunOverrideIsNotCommitted(t *testing.T) {
	cfg := config.GCConfig{
		Enabled:        true,
		WorkerInterval: 10 * time.Minute,
		ScanInterval:   10 * time.Minute,
		BatchSize:      10,
		GracePeriod:    time.Hour,
		DryRun:         true,
	}
	svc := NewService(NewMockStore(), nil, cfg, nil)
	svc.lease = &fakeLeaderLease{allowed: false}
	svc.Start()
	defer svc.Stop()

	dryRun := false
	if svc.TriggerWorkerWithDryRun(&dryRun) {
		t.Fatal("follower accepted a trigger with a dry-run override")
	}
	if !svc.dryRun.Load() || !svc.worker.dryRun.Load() {
		t.Fatal("rejected trigger committed its dry-run override")
	}
}

func TestService_NilServiceRefusesManualTriggers(t *testing.T) {
	var svc *Service
	if svc.AcceptsManualTriggers() {
		t.Fatal("AcceptsManualTriggers() = true on a nil service, want false")
	}
}

func TestService_StopDoesNotDeadlockAgainstTrigger(t *testing.T) {
	// Regression: AcceptsManualTriggers once took s.mu, and Stop() holds s.mu across
	// s.wg.Wait(). The scanner goroutine calls TriggerWorker() from inside
	// runScannerOnce after auto-requeuing recoverable failed items, so shutdown
	// could hang forever: Stop waits for the scanner, the scanner waits for Stop's
	// lock. Reproduced here directly at the boundary — a goroutine counted in s.wg
	// that triggers while Stop is draining.
	cfg := config.GCConfig{
		Enabled:        true,
		WorkerInterval: 10 * time.Minute,
		ScanInterval:   10 * time.Minute,
		BatchSize:      10,
		GracePeriod:    time.Hour,
		DryRun:         true,
	}
	svc := NewService(NewMockStore(), nil, cfg, nil)
	svc.Start()

	// Stand in for the scanner goroutine: counted in s.wg, and held until Stop()
	// has closed acceptingWork. This makes the trigger land while Stop() holds
	// s.mu inside wg.Wait(), without relying on scheduler timing.
	svc.wg.Add(1)
	trigger := make(chan struct{})
	go func() {
		defer svc.wg.Done()
		<-trigger
		svc.TriggerWorker()
		svc.TriggerScanner()
	}()

	done := make(chan struct{})
	go func() {
		svc.Stop()
		close(done)
	}()

	deadline := time.Now().Add(time.Second)
	for svc.acceptingWork.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if svc.acceptingWork.Load() {
		close(trigger)
		t.Fatal("Stop() did not close trigger admission")
	}
	close(trigger)

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("Stop() deadlocked against a concurrent trigger; shutdown would hang Server.Shutdown()")
	}
}

func TestService_StartDrainsStaleTriggerTokens(t *testing.T) {
	// A trigger that passed the acceptingWork check just before Stop() cleared it
	// can still land in the buffered channel. Start() drains, so restarting never
	// fires a run nobody asked for.
	cfg := config.GCConfig{
		Enabled:        true,
		WorkerInterval: 10 * time.Minute,
		ScanInterval:   10 * time.Minute,
		BatchSize:      10,
		GracePeriod:    time.Hour,
		DryRun:         true,
	}
	svc := NewService(NewMockStore(), nil, cfg, nil)
	svc.Start()
	svc.Stop()

	// Simulate the token that lost the race with Stop().
	svc.triggerWorker <- struct{}{}
	svc.triggerScanner <- struct{}{}

	svc.Start()
	defer svc.Stop()

	select {
	case <-svc.triggerWorker:
		t.Error("Start() left a stale worker trigger queued; it would fire an unrequested run")
	default:
	}
	select {
	case <-svc.triggerScanner:
		t.Error("Start() left a stale scanner trigger queued")
	default:
	}
}

func TestService_TriggerBlockedDuringStopCannotLeakIntoRestart(t *testing.T) {
	cfg := config.GCConfig{
		Enabled:        true,
		WorkerInterval: 10 * time.Minute,
		ScanInterval:   10 * time.Minute,
		BatchSize:      10,
		GracePeriod:    time.Hour,
		DryRun:         true,
	}
	svc := NewService(NewMockStore(), nil, cfg, nil)
	svc.Start()

	// Hold the trigger admission lock so a concurrent trigger is paused between
	// the caller and the lifecycle transition. Stop must clear acceptingWork and
	// complete its drain before the trigger can proceed; otherwise that token can
	// land after the next Start() drain and fire an unrequested run.
	svc.triggerMu.Lock()
	triggerDone := make(chan bool, 1)
	go func() {
		triggerDone <- svc.TriggerWorker()
	}()

	stopDone := make(chan struct{})
	go func() {
		svc.Stop()
		close(stopDone)
	}()

	deadline := time.Now().Add(time.Second)
	for svc.acceptingWork.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if svc.acceptingWork.Load() {
		svc.triggerMu.Unlock()
		t.Fatal("Stop() did not close trigger admission")
	}
	svc.triggerMu.Unlock()

	if accepted := <-triggerDone; accepted {
		t.Error("trigger accepted after Stop() closed trigger admission")
	}
	select {
	case <-stopDone:
	case <-time.After(time.Second):
		t.Fatal("Stop() did not complete after trigger admission was released")
	}

	svc.Start()
	defer svc.Stop()
	select {
	case <-svc.triggerWorker:
		t.Fatal("a trigger racing Stop()/Start() leaked into the restarted service")
	default:
	}
}

func TestService_DLQMutationsRefusedWhenDisabled(t *testing.T) {
	// Same kill switch, sibling admin surface. These call
	// tryClaimLeadershipForAdmin, which CLAIMS the lease — so an operator on a
	// disabled replica could take GC leadership away from the datacenter that
	// actually runs it.
	svc := NewService(NewMockStore(), nil, config.GCConfig{Enabled: false, BatchSize: 10}, nil)
	svc.Start()
	defer svc.Stop()

	orgID := uuid.New()
	failedAt := time.Now().UTC()

	if err := svc.DeleteFailedItem(orgID, failedAt, ItemBlock, "block-1"); !errors.Is(err, ErrGCDisabled) {
		t.Errorf("DeleteFailedItem error = %v, want ErrGCDisabled", err)
	}
	if err := svc.RequeueFailedItem(orgID, failedAt, ItemBlock, "block-1"); !errors.Is(err, ErrGCDisabled) {
		t.Errorf("RequeueFailedItem error = %v, want ErrGCDisabled", err)
	}
}

func TestService_DLQMutationsRefusedBeforeStart(t *testing.T) {
	// An enabled but not-started service is not a useful admin target. Refusing
	// here prevents a node that has not entered its lifecycle from claiming the
	// lease through a synchronous DLQ mutation.
	svc := NewService(NewMockStore(), nil, config.GCConfig{Enabled: true, BatchSize: 10}, nil)

	if svc.AcceptsManualTriggers() {
		t.Fatal("precondition: triggers must be refused before Start()")
	}

	orgID := uuid.New()
	failedAt := time.Now().UTC()

	if err := svc.DeleteFailedItem(orgID, failedAt, ItemBlock, "block-1"); !errors.Is(err, ErrGCNotRunning) {
		t.Errorf("DeleteFailedItem error = %v, want ErrGCNotRunning", err)
	}
	if err := svc.RequeueFailedItem(orgID, failedAt, ItemBlock, "block-1"); !errors.Is(err, ErrGCNotRunning) {
		t.Errorf("RequeueFailedItem error = %v, want ErrGCNotRunning", err)
	}
}

func TestService_DLQMutationsRefusedAfterStopWithoutReclaimingLease(t *testing.T) {
	cfg := config.GCConfig{
		Enabled:        true,
		WorkerInterval: 10 * time.Minute,
		ScanInterval:   10 * time.Minute,
		BatchSize:      10,
		GracePeriod:    time.Hour,
		DryRun:         true,
	}
	svc := NewService(NewMockStore(), nil, cfg, nil)
	lease := &fakeLeaderLease{allowed: true}
	svc.lease = lease
	svc.Start()
	svc.Stop()

	callsAfterStop := atomic.LoadInt32(&lease.calls)
	orgID := uuid.New()
	failedAt := time.Now().UTC()
	for _, op := range []struct {
		name string
		fn   func() error
	}{
		{name: "delete", fn: func() error {
			return svc.DeleteFailedItem(orgID, failedAt, ItemBlock, "block-1")
		}},
		{name: "requeue", fn: func() error {
			return svc.RequeueFailedItem(orgID, failedAt, ItemBlock, "block-1")
		}},
	} {
		t.Run(op.name, func(t *testing.T) {
			if err := op.fn(); !errors.Is(err, ErrGCNotRunning) {
				t.Fatalf("error = %v, want ErrGCNotRunning", err)
			}
			if got := atomic.LoadInt32(&lease.calls); got != callsAfterStop {
				t.Fatalf("lease calls after stopped DLQ %s = %d, want unchanged at %d", op.name, got, callsAfterStop)
			}
		})
	}
}

func TestService_StopWaitsForInFlightDLQClaimBeforeReleasingLease(t *testing.T) {
	cfg := config.GCConfig{
		Enabled:        true,
		WorkerInterval: 10 * time.Minute,
		ScanInterval:   10 * time.Minute,
		BatchSize:      10,
		GracePeriod:    time.Hour,
		DryRun:         true,
	}
	svc := NewService(NewMockStore(), nil, cfg, nil)
	lease := &fakeLeaderLease{allowed: true}
	svc.lease = lease
	svc.Start()

	baselineCalls := atomic.LoadInt32(&lease.calls)
	lease.leader.Store(false)
	lease.delay = 100 * time.Millisecond
	dlqDone := make(chan struct{})
	go func() {
		_ = svc.RequeueFailedItem(uuid.New(), time.Now().UTC(), ItemBlock, "block-1")
		close(dlqDone)
	}()

	deadline := time.Now().Add(time.Second)
	for atomic.LoadInt32(&lease.calls) == baselineCalls && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if atomic.LoadInt32(&lease.calls) == baselineCalls {
		t.Fatal("DLQ operation did not reach lease claim")
	}

	stopDone := make(chan struct{})
	go func() {
		svc.Stop()
		close(stopDone)
	}()

	select {
	case <-dlqDone:
	case <-time.After(time.Second):
		t.Fatal("in-flight DLQ operation did not complete")
	}
	select {
	case <-stopDone:
	case <-time.After(time.Second):
		t.Fatal("Stop() did not complete after the in-flight DLQ operation")
	}
	if lease.leader.Load() {
		t.Fatal("stopped service retained or reacquired GC leadership")
	}
}
