package v2

import (
	"sync"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
)

const (
	defaultLibraryHeadProjectionRepairWorkerInterval    = time.Second
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
	if w.process != nil {
		w.process()
	}
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-w.stopCh:
			return
		case <-ticker.C:
			if w.process != nil {
				w.process()
			}
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
