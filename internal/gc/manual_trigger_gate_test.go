package gc

import (
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
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
