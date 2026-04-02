package gc

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	gocql "github.com/apache/cassandra-gocql-driver/v2"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	"github.com/Sesame-Disk/sesamefs/internal/traffic"
	"github.com/google/uuid"
)

// Stats tracks GC runtime statistics (thread-safe).
type Stats struct {
	blocksDeleted atomic.Int64
	lastWorkerRun atomic.Value // time.Time
	lastScanRun   atomic.Value // time.Time
}

func (s *Stats) IncrBlocksDeleted()           { s.blocksDeleted.Add(1) }
func (s *Stats) BlocksDeleted() int64         { return s.blocksDeleted.Load() }
func (s *Stats) SetLastWorkerRun(t time.Time) { s.lastWorkerRun.Store(t) }
func (s *Stats) SetLastScanRun(t time.Time)   { s.lastScanRun.Store(t) }

func (s *Stats) LastWorkerRun() time.Time {
	v := s.lastWorkerRun.Load()
	if v == nil {
		return time.Time{}
	}
	return v.(time.Time)
}

func (s *Stats) LastScanRun() time.Time {
	v := s.lastScanRun.Load()
	if v == nil {
		return time.Time{}
	}
	return v.(time.Time)
}

// GCStatus is the JSON response for the admin status endpoint.
type GCStatus struct {
	Enabled            bool   `json:"enabled"`
	DryRun             bool   `json:"dry_run"`
	LastWorkerRun      string `json:"last_worker_run"`
	LastScanRun        string `json:"last_scan_run"`
	QueueSize          int    `json:"queue_size"`
	BlocksDeletedTotal int64  `json:"blocks_deleted_total"`
}

// Service is the top-level GC orchestrator.
// It starts and manages the worker and scanner goroutines.
type Service struct {
	store   GCStore
	storage StorageProvider
	config  config.GCConfig
	queue   *Queue
	worker  *Worker
	scanner *Scanner
	stats   *Stats

	// dbSession is the raw gocql session used for quota-period rollover.
	// Kept separate from store because rollover operates on organizations,
	// not on the GC queue.
	dbSession *gocql.Session

	cancel  context.CancelFunc
	wg      sync.WaitGroup
	started bool
	mu      sync.Mutex

	// consecutiveErrors is only accessed from the workerLoop goroutine
	// (via runWorkerOnce). If this changes, protect with s.mu.
	consecutiveErrors int

	// Channels for manual triggers
	triggerWorker  chan struct{}
	triggerScanner chan struct{}
}

// NewService creates a new GC service using the provided store and storage provider.
// dbSession is used for quota-period rollover; pass nil to disable rollover.
func NewService(store GCStore, storage StorageProvider, cfg config.GCConfig, dbSession *gocql.Session) *Service {
	queue := NewQueue(store)
	stats := &Stats{}

	return &Service{
		store:          store,
		storage:        storage,
		config:         cfg,
		queue:          queue,
		worker:         NewWorker(store, storage, queue, cfg.BatchSize, cfg.GracePeriod, cfg.DryRun, stats),
		scanner:        NewScanner(store, queue, stats, cfg),
		stats:          stats,
		dbSession:      dbSession,
		triggerWorker:  make(chan struct{}, 1),
		triggerScanner: make(chan struct{}, 1),
	}
}

// Start begins the worker and scanner goroutines.
func (s *Service) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.started {
		return
	}

	if !s.config.Enabled {
		log.Println("[GC] Garbage collection is disabled")
		return
	}

	// Restore persisted stats from database
	s.restoreStats()

	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.started = true

	// Start worker goroutine
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.runWorkerLoop(ctx)
	}()

	// Start scanner goroutine — runs immediately on startup then on interval
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.runScannerLoop(ctx)
	}()

	// Start rollover goroutine — advances expired quota billing periods
	if s.dbSession != nil {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.runRolloverLoop(ctx)
		}()
	}

	log.Printf("[GC] Started (worker every %v, scanner every %v, grace %v, batch %d, dry_run=%v)",
		s.config.WorkerInterval, s.config.ScanInterval, s.config.GracePeriod,
		s.config.BatchSize, s.config.DryRun)
}

// Stop gracefully stops the GC service.
func (s *Service) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.started {
		return
	}

	log.Println("[GC] Stopping...")
	s.cancel()
	s.wg.Wait()

	// Persist stats to database before shutdown
	s.persistStats()

	s.started = false
	log.Println("[GC] Stopped")
}

// TriggerWorker triggers an immediate worker run.
func (s *Service) TriggerWorker() {
	select {
	case s.triggerWorker <- struct{}{}:
	default:
		// Already triggered
	}
}

// TriggerScanner triggers an immediate scanner run.
func (s *Service) TriggerScanner() {
	select {
	case s.triggerScanner <- struct{}{}:
	default:
	}
}

// Status returns the current GC status for the admin API.
func (s *Service) Status() GCStatus {
	queueSize, _ := s.queue.GetTotalQueueSize()

	lastWorker := s.stats.LastWorkerRun()
	lastScan := s.stats.LastScanRun()

	formatTime := func(t time.Time) string {
		if t.IsZero() {
			return "never"
		}
		return t.Format(time.RFC3339)
	}

	return GCStatus{
		Enabled:            s.config.Enabled,
		DryRun:             s.config.DryRun,
		LastWorkerRun:      formatTime(lastWorker),
		LastScanRun:        formatTime(lastScan),
		QueueSize:          queueSize,
		BlocksDeletedTotal: s.stats.BlocksDeleted(),
	}
}

// Queue returns the underlying queue for inline enqueue operations.
func (s *Service) Queue() *Queue {
	return s.queue
}

// EnqueueBlock is a convenience method for enqueuing a block from application code.
func (s *Service) EnqueueBlock(orgID uuid.UUID, blockID string, libraryID uuid.UUID, storageClass string) error {
	return s.queue.Enqueue(orgID, ItemBlock, blockID, libraryID, storageClass)
}

// EnqueueLibraryDeletion enqueues all contents of a library for GC.
func (s *Service) EnqueueLibraryDeletion(orgID, libraryID uuid.UUID, storageClass string) error {
	return s.worker.EnqueueLibraryContents(orgID, libraryID, storageClass)
}

// EnqueueCommits enqueues specific commits for GC deletion (used by CleanRepoTrash).
func (s *Service) EnqueueCommits(orgID, libraryID uuid.UUID, commitIDs []string) error {
	if len(commitIDs) == 0 {
		return nil
	}
	now := time.Now()
	items := make([]QueueItem, 0, len(commitIDs))
	for _, id := range commitIDs {
		items = append(items, QueueItem{
			OrgID:     orgID,
			QueuedAt:  now,
			ItemType:  ItemCommit,
			ItemID:    id,
			LibraryID: libraryID,
		})
	}
	return s.queue.EnqueueBatch(items)
}

func (s *Service) runWorkerLoop(ctx context.Context) {
	ticker := time.NewTicker(s.config.WorkerInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runWorkerOnce(ctx)
		case <-s.triggerWorker:
			s.runWorkerOnce(ctx)
		}
	}
}

func (s *Service) runWorkerOnce(ctx context.Context) {
	queueBefore, _ := s.queue.GetTotalQueueSize()

	start := time.Now()
	n, err := s.worker.ProcessOnce(ctx)
	metrics.GCWorkerDuration.Observe(time.Since(start).Seconds())

	now := time.Now()
	s.stats.SetLastWorkerRun(now)
	metrics.GCLastWorkerRun.Set(float64(now.Unix()))

	if err != nil {
		log.Printf("[GC Worker] Error: %v", err)
		s.consecutiveErrors++
		metrics.GCWorkerConsecutiveErrors.Set(float64(s.consecutiveErrors))
	} else {
		s.consecutiveErrors = 0
		metrics.GCWorkerConsecutiveErrors.Set(0)
		if n > 0 {
			metrics.GCWorkerLastSuccessTimestamp.Set(float64(now.Unix()))
		}
	}

	if queueAfter, qErr := s.queue.GetTotalQueueSize(); qErr == nil {
		metrics.GCQueueSize.Set(float64(queueAfter))
		metrics.GCQueueGrowthRate.Set(float64(queueAfter - queueBefore))
	}

	if n > 0 {
		log.Printf("[GC Worker] Processed %d items", n)
	}
}

func (s *Service) runScannerLoop(ctx context.Context) {
	// Run scanner immediately on startup to catch anything missed during downtime
	s.runScannerOnce(ctx)

	ticker := time.NewTicker(s.config.ScanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runScannerOnce(ctx)
		case <-s.triggerScanner:
			s.runScannerOnce(ctx)
		}
	}
}

func (s *Service) runScannerOnce(ctx context.Context) {
	start := time.Now()
	s.scanner.ScanOnce(ctx)
	metrics.GCScannerDuration.Observe(time.Since(start).Seconds())
	metrics.GCLastScannerRun.Set(float64(time.Now().Unix()))
}

// SetDryRun changes the dry run mode at runtime (for admin API).
func (s *Service) SetDryRun(dryRun bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config.DryRun = dryRun
	s.worker.dryRun = dryRun
}

// rolloverInterval is how often the rollover loop checks for expired billing
// periods. 1 hour strikes a balance between prompt rollover and low overhead
// (full-table scan of organizations).
const rolloverInterval = 1 * time.Hour

func (s *Service) runRolloverLoop(ctx context.Context) {
	// Run once on startup to catch periods that expired during downtime.
	s.runRolloverOnce()

	ticker := time.NewTicker(rolloverInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runRolloverOnce()
		}
	}
}

func (s *Service) runRolloverOnce() {
	n, err := traffic.RolloverExpiredPeriods(s.dbSession, time.Now())
	if err != nil {
		log.Printf("[GC] Rollover error: %v", err)
	}
	if n > 0 {
		log.Printf("[GC] Rollover: advanced billing period for %d org(s)", n)
	}
}

// persistStats saves GC stats to the database for recovery after restart.
func (s *Service) persistStats() {
	if lastWorker := s.stats.LastWorkerRun(); !lastWorker.IsZero() {
		s.store.SaveGCStats("last_worker_run", lastWorker.Format(time.RFC3339))
	}
	if lastScan := s.stats.LastScanRun(); !lastScan.IsZero() {
		s.store.SaveGCStats("last_scan_run", lastScan.Format(time.RFC3339))
	}
	s.store.SaveGCStats("blocks_deleted_total", fmt.Sprintf("%d", s.stats.BlocksDeleted()))
}

// restoreStats loads persisted GC stats from the database on startup.
func (s *Service) restoreStats() {
	if val, err := s.store.LoadGCStats("last_worker_run"); err == nil && val != "" {
		if t, err := time.Parse(time.RFC3339, val); err == nil {
			s.stats.SetLastWorkerRun(t)
		}
	}
	if val, err := s.store.LoadGCStats("last_scan_run"); err == nil && val != "" {
		if t, err := time.Parse(time.RFC3339, val); err == nil {
			s.stats.SetLastScanRun(t)
		}
	}
	if val, err := s.store.LoadGCStats("blocks_deleted_total"); err == nil && val != "" {
		if n, err := strconv.ParseInt(val, 10, 64); err == nil {
			s.stats.blocksDeleted.Store(n)
		}
	}
}
