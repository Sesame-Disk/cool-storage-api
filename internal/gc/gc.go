package gc

import (
	"context"
	"errors"
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
	LastReconcileRun   string `json:"last_reconcile_run"`
	QueueSize          int    `json:"queue_size"`
	FailedItemsTotal   int    `json:"failed_items_total"`
	DirtyOrgsTotal     int    `json:"dirty_orgs_total"`
	SnapshotAgeSeconds int64  `json:"snapshot_age_seconds"`
	BlocksDeletedTotal int64  `json:"blocks_deleted_total"`
	GracePeriodSeconds int64  `json:"grace_period_seconds"`
}

const (
	gcStatKeyTotalQueue          = "total_queue_depth"
	gcStatKeyTotalFailed         = "total_failed_items"
	gcStatKeyTotalDirtyOrgs      = "dirty_orgs_total"
	gcStatKeyLastReconcile       = "last_reconcile_run"
	gcDefaultFailedItemsPageSize = 100
	gcSnapshotDriftCheckEvery    = 10
)

var ErrNotLeader = errors.New("gc leadership required")

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
	lease   leaderLease

	// dbSession is the raw gocql session used for quota-period rollover.
	// Kept separate from store because rollover operates on organizations,
	// not on the GC queue.
	dbSession *gocql.Session

	cancel  context.CancelFunc
	wg      sync.WaitGroup
	started bool
	mu      sync.Mutex

	// reconcileMu serializes snapshot reconciliation so global gc_stats totals
	// are not corrupted by concurrent read-modify-write cycles.
	reconcileMu sync.Mutex

	// reconcilePasses counts serialized reconcile runs so we can do occasional
	// full drift checks without paying that cost on every pass.
	reconcilePasses uint64

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

	worker := NewWorker(store, storage, queue, cfg.BatchSize, cfg.GracePeriod, cfg.DryRun, stats)
	scanner := NewScanner(store, queue, stats, cfg)
	scanner.SetOrphanRecoverer(worker)

	return &Service{
		store:          store,
		storage:        storage,
		config:         cfg,
		queue:          queue,
		worker:         worker,
		scanner:        scanner,
		stats:          stats,
		lease:          newCassandraLeaderLease(dbSession, gcLeaderRole, cfg.WorkerInterval),
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

	// Acquire leadership once before starting worker/scanner so the immediate
	// startup scan cannot race ahead of the first lease heartbeat.
	if s.lease != nil {
		if ok, err := s.lease.TryAcquireOrRenew(ctx); err != nil {
			log.Printf("[GC] Initial lease acquisition failed: %v", err)
		} else if ok {
			log.Println("[GC] Acquired leader lease")
		}
	}

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

	// Start lease renewal goroutine — keeps the lease alive independently of
	// worker/scanner duration so long-running ProcessOnce or ScanOnce calls
	// (which can exceed the TTL) don't cause the lease to lapse.
	if s.lease != nil {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.runLeaseRenewalLoop(ctx)
		}()
	}

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

	// Release leadership so another replica can take over without waiting
	// for TTL expiry. Best-effort — if it fails, TTL handles it.
	if s.lease != nil {
		s.lease.Release(context.Background())
	}

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
	queueSize := s.loadStatInt(gcStatKeyTotalQueue)
	failedItemsTotal := s.loadStatInt(gcStatKeyTotalFailed)

	lastWorker := s.stats.LastWorkerRun()
	lastScan := s.stats.LastScanRun()
	lastReconcile := s.loadStatTime(gcStatKeyLastReconcile)
	dirtyOrgs := s.loadStatInt(gcStatKeyTotalDirtyOrgs)
	snapshotAgeSeconds := int64(0)
	if !lastReconcile.IsZero() {
		snapshotAgeSeconds = int64(time.Since(lastReconcile).Seconds())
		if snapshotAgeSeconds < 0 {
			snapshotAgeSeconds = 0
		}
	}

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
		LastReconcileRun:   formatTime(lastReconcile),
		QueueSize:          queueSize,
		FailedItemsTotal:   failedItemsTotal,
		DirtyOrgsTotal:     dirtyOrgs,
		SnapshotAgeSeconds: snapshotAgeSeconds,
		BlocksDeletedTotal: s.stats.BlocksDeleted(),
		GracePeriodSeconds: int64(s.config.GracePeriod.Seconds()),
	}
}

// Queue returns the underlying queue for inline enqueue operations.
func (s *Service) Queue() *Queue {
	return s.queue
}

// EnqueueBlock is a convenience method for enqueuing a block from application code.
func (s *Service) EnqueueBlock(orgID uuid.UUID, blockID string, libraryID uuid.UUID, storageClass string) error {
	candidateAt, err := s.store.EnsureBlockGCCandidate(orgID, blockID, storageClass, time.Now())
	if err != nil {
		return err
	}
	exists, err := s.store.QueueItemExists(orgID, candidateAt, ItemBlock, blockID)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	return s.store.EnqueueItem(orgID, candidateAt, ItemBlock, blockID, libraryID, storageClass, 0)
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
	if !s.hasLeadership(ctx, "worker") {
		return
	}

	queueBefore := s.loadStatInt(gcStatKeyTotalQueue)

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

	s.reconcileDirtyQueueStats(s.reconcileLimit())

	queueAfter := s.loadStatInt(gcStatKeyTotalQueue)
	metrics.GCQueueSize.Set(float64(queueAfter))
	metrics.GCQueueGrowthRate.Set(float64(queueAfter - queueBefore))

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
	if !s.hasLeadership(ctx, "scanner") {
		return
	}

	start := time.Now()
	s.scanner.ScanOnce(ctx)
	s.reconcileDirtyQueueStats(s.reconcileLimit())
	metrics.GCScannerDuration.Observe(time.Since(start).Seconds())
	metrics.GCLastScannerRun.Set(float64(time.Now().Unix()))
}

func (s *Service) loadStatTime(key string) time.Time {
	val, err := s.store.LoadGCStats(key)
	if err != nil || val == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, val)
	if err != nil {
		return time.Time{}
	}
	return t
}

func (s *Service) loadStatInt(key string) int {
	val, err := s.store.LoadGCStats(key)
	if err != nil || val == "" {
		return 0
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		return 0
	}
	if n < 0 {
		return 0
	}
	return n
}

func (s *Service) saveStatInt(key string, value int) {
	if value < 0 {
		value = 0
	}
	if err := s.store.SaveGCStats(key, strconv.Itoa(value)); err != nil {
		log.Printf("[GC] Failed to persist %s=%d: %v", key, value, err)
	}
}

func (s *Service) reconcileDirtyQueueStats(limit int) {
	s.reconcileMu.Lock()
	defer s.reconcileMu.Unlock()

	start := time.Now()
	defer func() {
		metrics.GCReconcileDuration.Observe(time.Since(start).Seconds())
	}()

	dirtyOrgs, err := s.store.ListDirtyOrgs(limit)
	if err != nil {
		log.Printf("[GC] Failed to list dirty orgs: %v", err)
		return
	}
	metrics.GCDirtyOrgsTotal.Set(float64(len(dirtyOrgs)))

	totalQueue := s.loadStatInt(gcStatKeyTotalQueue)
	totalFailed := s.loadStatInt(gcStatKeyTotalFailed)
	remainingDirtyCount := len(dirtyOrgs)

	if len(dirtyOrgs) > 0 {
		for _, dirtyOrg := range dirtyOrgs {
			recountedAt := time.Now().UTC()
			prevStats, err := s.store.GetOrgQueueStats(dirtyOrg.OrgID)
			if err != nil {
				log.Printf("[GC] Failed to load org queue stats for %s: %v", dirtyOrg.OrgID, err)
				continue
			}
			queueDepth, err := s.store.RecountOrgQueueDepth(dirtyOrg.OrgID)
			if err != nil {
				log.Printf("[GC] Failed to recount queue depth for %s: %v", dirtyOrg.OrgID, err)
				continue
			}
			failedDepth, err := s.store.RecountOrgFailedDepth(dirtyOrg.OrgID)
			if err != nil {
				log.Printf("[GC] Failed to recount failed depth for %s: %v", dirtyOrg.OrgID, err)
				continue
			}
			oldestQueuedAt, err := s.store.GetOldestQueuedAt(dirtyOrg.OrgID)
			if err != nil {
				log.Printf("[GC] Failed to read oldest queue item for %s: %v", dirtyOrg.OrgID, err)
				continue
			}

			nextStats := GCOrgStats{
				OrgID:          dirtyOrg.OrgID,
				QueueDepth:     queueDepth,
				FailedDepth:    failedDepth,
				OldestQueuedAt: oldestQueuedAt,
				UpdatedAt:      recountedAt,
			}
			if err := s.store.SaveOrgQueueStats(nextStats); err != nil {
				log.Printf("[GC] Failed to save org queue stats for %s: %v", dirtyOrg.OrgID, err)
				continue
			}

			totalQueue += queueDepth - prevStats.QueueDepth
			if totalQueue < 0 {
				totalQueue = 0
			}
			totalFailed += failedDepth - prevStats.FailedDepth
			if totalFailed < 0 {
				totalFailed = 0
			}

			if err := s.store.ClearDirtyOrg(dirtyOrg.OrgID, dirtyOrg.MarkedAt); err != nil {
				log.Printf("[GC] Failed to clear dirty org %s: %v", dirtyOrg.OrgID, err)
			}
			if queueDepth == 0 {
				if err := s.store.RemoveOrgFromActiveSet(dirtyOrg.OrgID, recountedAt); err != nil {
					log.Printf("[GC] Failed to remove drained org %s from active set: %v", dirtyOrg.OrgID, err)
				}
			}
		}
	}

	s.reconcilePasses++
	if s.reconcilePasses%gcSnapshotDriftCheckEvery == 0 {
		summedQueue, summedFailed, err := s.store.SumOrgQueueStats()
		if err != nil {
			log.Printf("[GC] Failed to sum org queue stats for drift check: %v", err)
		} else if summedQueue != totalQueue || summedFailed != totalFailed {
			log.Printf("[GC] Snapshot drift detected; correcting totals queue=%d->%d failed=%d->%d", totalQueue, summedQueue, totalFailed, summedFailed)
			totalQueue = summedQueue
			totalFailed = summedFailed
			metrics.GCSnapshotDriftCorrectedTotal.Inc()
		}
	}

	lastRun := time.Now().UTC()
	if remainingDirty, err := s.store.ListDirtyOrgs(0); err == nil {
		remainingDirtyCount = len(remainingDirty)
	}
	s.saveStatInt(gcStatKeyTotalQueue, totalQueue)
	s.saveStatInt(gcStatKeyTotalFailed, totalFailed)
	s.saveStatInt(gcStatKeyTotalDirtyOrgs, remainingDirtyCount)
	if err := s.store.SaveGCStats(gcStatKeyLastReconcile, lastRun.Format(time.RFC3339)); err != nil {
		log.Printf("[GC] Failed to persist last reconcile run: %v", err)
	}
	metrics.GCQueueSize.Set(float64(totalQueue))
	metrics.GCFailedItemsTotal.Set(float64(totalFailed))
	metrics.GCDirtyOrgsTotal.Set(float64(remainingDirtyCount))
	metrics.GCSnapshotAgeSeconds.Set(0)
}

func (s *Service) reconcileLimit() int {
	if s.config.ReconcileBatchSize > 0 {
		return s.config.ReconcileBatchSize
	}
	return 0
}

func (s *Service) FailedItemsPageSize() int {
	if s.config.FailedItemsPageSize > 0 {
		return s.config.FailedItemsPageSize
	}
	return gcDefaultFailedItemsPageSize
}

func (s *Service) ListFailedItems(orgID uuid.UUID, limit int) ([]GCFailedItemInfo, error) {
	if limit <= 0 {
		limit = s.FailedItemsPageSize()
	}
	return s.store.ListFailedItems(orgID, limit)
}

func (s *Service) DeleteFailedItem(orgID uuid.UUID, failedAt time.Time, itemType ItemType, itemID string) error {
	if !s.hasLeadership(context.Background(), "admin_failed_item_delete") {
		return ErrNotLeader
	}
	err := s.store.DeleteFailedItem(orgID, failedAt, itemType, itemID)
	if err != nil {
		return err
	}
	s.reconcileDirtyQueueStats(s.reconcileLimit())
	return nil
}

func (s *Service) RequeueFailedItem(orgID uuid.UUID, failedAt time.Time, itemType ItemType, itemID string) error {
	if !s.hasLeadership(context.Background(), "admin_failed_item_requeue") {
		return ErrNotLeader
	}
	err := s.store.RequeueFailedItem(orgID, failedAt, itemType, itemID, time.Now().UTC())
	if err != nil {
		return err
	}
	s.reconcileDirtyQueueStats(s.reconcileLimit())
	return nil
}

func (s *Service) hasLeadership(ctx context.Context, component string) bool {
	if s.lease == nil {
		return true // no lease configured — single-instance mode
	}
	// Fast check against the cached atomic — the background renewal goroutine
	// keeps this up to date every TTL/3 seconds.
	return s.lease.IsLeader()
}

// leaseRenewalInterval returns TTL/3 — frequent enough that a single missed
// renewal doesn't lose the lease (which lives for the full TTL).
func (s *Service) leaseRenewalInterval() time.Duration {
	if s.lease == nil {
		return time.Minute
	}
	cl, ok := s.lease.(*cassandraLeaderLease)
	if !ok {
		return 10 * time.Second
	}
	interval := time.Duration(cl.ttlSeconds) * time.Second / 3
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}
	return interval
}

func (s *Service) runLeaseRenewalLoop(ctx context.Context) {
	ticker := time.NewTicker(s.leaseRenewalInterval())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := s.lease.TryAcquireOrRenew(ctx); err != nil {
				log.Printf("[GC] Lease renewal failed: %v", err)
			}
		}
	}
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
	if !s.hasLeadership(context.Background(), "rollover") {
		return
	}

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
