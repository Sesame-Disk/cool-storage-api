package v2

import (
	"sync"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
)

const (
	defaultLibraryHeadProjectionRepairWorkerInterval    = 30 * time.Second
	defaultLibraryHeadProjectionRepairWorkerOrgLimit    = 16
	defaultLibraryHeadProjectionRepairWorkerPerOrgLimit = 8
)

type LibraryHeadProjectionRepairWorker struct {
	stopOnce sync.Once
	stopCh   chan struct{}
	doneCh   chan struct{}
	interval time.Duration
	process  func()
}

func NewLibraryHeadProjectionRepairWorker(database *db.DB, interval time.Duration) *LibraryHeadProjectionRepairWorker {
	if database == nil {
		return nil
	}
	helper := NewFSHelper(database)
	return newLibraryHeadProjectionRepairWorker(interval, func() {
		helper.ProcessPendingLibraryHeadProjectionRepairs(
			defaultLibraryHeadProjectionRepairWorkerOrgLimit,
			defaultLibraryHeadProjectionRepairWorkerPerOrgLimit,
		)
	})
}

func newLibraryHeadProjectionRepairWorker(interval time.Duration, process func()) *LibraryHeadProjectionRepairWorker {
	if interval <= 0 {
		interval = defaultLibraryHeadProjectionRepairWorkerInterval
	}
	worker := &LibraryHeadProjectionRepairWorker{
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
		interval: interval,
		process:  process,
	}
	go worker.loop()
	return worker
}

func (w *LibraryHeadProjectionRepairWorker) loop() {
	defer close(w.doneCh)
	for {
		if w.process != nil {
			w.process()
		}
		timer := time.NewTimer(w.interval)
		select {
		case <-w.stopCh:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case <-timer.C:
		}
	}
}

func (w *LibraryHeadProjectionRepairWorker) Stop() {
	if w == nil {
		return
	}
	w.stopOnce.Do(func() {
		close(w.stopCh)
		<-w.doneCh
	})
}
