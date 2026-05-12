package v2

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestLibraryHeadProjectionRepairWorkerRunsImmediatelyAndPeriodically(t *testing.T) {
	callCh := make(chan struct{}, 4)
	var calls int32
	worker := newLibraryHeadProjectionRepairWorker(20*time.Millisecond, func() {
		atomic.AddInt32(&calls, 1)
		select {
		case callCh <- struct{}{}:
		default:
		}
	})
	defer worker.Stop()

	select {
	case <-callCh:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected worker to run immediately on start")
	}

	select {
	case <-callCh:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected worker to run again after the configured interval")
	}

	if got := atomic.LoadInt32(&calls); got < 2 {
		t.Fatalf("worker calls = %d, want at least 2", got)
	}
}

func TestLibraryHeadProjectionRepairWorkerStopHaltsFutureTicks(t *testing.T) {
	var calls int32
	worker := newLibraryHeadProjectionRepairWorker(20*time.Millisecond, func() {
		atomic.AddInt32(&calls, 1)
	})

	time.Sleep(40 * time.Millisecond)
	worker.Stop()
	afterStop := atomic.LoadInt32(&calls)
	time.Sleep(80 * time.Millisecond)
	if got := atomic.LoadInt32(&calls); got != afterStop {
		t.Fatalf("worker calls after stop = %d, want %d", got, afterStop)
	}
}
