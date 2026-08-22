package gc

import (
	"errors"
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

	// Stand in for the scanner goroutine: counted in s.wg, and it triggers partway
	// through its pass. The sleep is what makes the reproduction deterministic —
	// the trigger has to land WHILE Stop() holds s.mu inside wg.Wait(), not before
	// Stop has taken the lock. Without it the goroutine finishes first and the
	// mutex version passes, proving nothing.
	svc.wg.Add(1)
	go func() {
		defer svc.wg.Done()
		time.Sleep(250 * time.Millisecond)
		svc.TriggerWorker()
		svc.TriggerScanner()
	}()

	done := make(chan struct{})
	go func() {
		svc.Stop()
		close(done)
	}()

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

func TestService_DLQMutationsDoNotRequireStarted(t *testing.T) {
	// The DLQ gate is deliberately WEAKER than the trigger gate. A trigger needs a
	// consumer loop to be honest; a DLQ mutation does its store work inline and
	// needs none, so requiring `started` here would refuse a legitimate requeue on
	// a GC node that has simply not booted its loops yet.
	//
	// This is pinned because collapsing the two predicates into one broke five
	// pre-existing DLQ tests — the mistake is easy to repeat when someone reads
	// "one kill switch" as "one predicate".
	svc := NewService(NewMockStore(), nil, config.GCConfig{Enabled: true, BatchSize: 10}, nil)

	if svc.AcceptsManualTriggers() {
		t.Fatal("precondition: triggers must be refused before Start()")
	}

	orgID := uuid.New()
	failedAt := time.Now().UTC()

	if err := svc.DeleteFailedItem(orgID, failedAt, ItemBlock, "block-1"); errors.Is(err, ErrGCDisabled) {
		t.Error("DeleteFailedItem refused as ErrGCDisabled on an enabled but unstarted service")
	}
	if err := svc.RequeueFailedItem(orgID, failedAt, ItemBlock, "block-1"); errors.Is(err, ErrGCDisabled) {
		t.Error("RequeueFailedItem refused as ErrGCDisabled on an enabled but unstarted service")
	}
}
