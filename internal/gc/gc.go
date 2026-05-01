package gc

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
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
	blocksDeleted   atomic.Int64
	lastWorkerRun   atomic.Value // time.Time
	lastScanRun     atomic.Value // time.Time; legacy alias for last scan attempt
	lastScanAttempt atomic.Value // time.Time
	lastScanSuccess atomic.Value // time.Time
	lastScanError   atomic.Value // string
}

func (s *Stats) IncrBlocksDeleted()           { s.blocksDeleted.Add(1) }
func (s *Stats) BlocksDeleted() int64         { return s.blocksDeleted.Load() }
func (s *Stats) SetLastWorkerRun(t time.Time) { s.lastWorkerRun.Store(t) }
func (s *Stats) SetLastScanRun(t time.Time)   { s.SetLastScanAttempt(t) }
func (s *Stats) SetLastScanAttempt(t time.Time) {
	s.lastScanAttempt.Store(t)
	s.lastScanRun.Store(t)
}
func (s *Stats) SetLastScanSuccess(t time.Time) { s.lastScanSuccess.Store(t) }
func (s *Stats) SetLastScanError(v string)      { s.lastScanError.Store(v) }

func (s *Stats) LastWorkerRun() time.Time {
	v := s.lastWorkerRun.Load()
	if v == nil {
		return time.Time{}
	}
	return v.(time.Time)
}

func (s *Stats) LastScanRun() time.Time {
	return s.LastScanAttempt()
}

func (s *Stats) LastScanAttempt() time.Time {
	v := s.lastScanAttempt.Load()
	if v != nil {
		return v.(time.Time)
	}
	v = s.lastScanRun.Load()
	if v == nil {
		return time.Time{}
	}
	return v.(time.Time)
}

func (s *Stats) LastScanSuccess() time.Time {
	v := s.lastScanSuccess.Load()
	if v == nil {
		return time.Time{}
	}
	return v.(time.Time)
}

func (s *Stats) LastScanError() string {
	v := s.lastScanError.Load()
	if v == nil {
		return ""
	}
	return v.(string)
}

// GCStatus is the JSON response for the admin status endpoint.
type GCStatus struct {
	Enabled          bool   `json:"enabled"`
	DryRun           bool   `json:"dry_run"`
	LastWorkerRun    string `json:"last_worker_run"`
	LastScanRun      string `json:"last_scan_run"`
	LastScanAttempt  string `json:"last_scan_attempt"`
	LastScanSuccess  string `json:"last_scan_success"`
	LastScanError    string `json:"last_scan_error"`
	LastReconcileRun string `json:"last_reconcile_run"`
	QueueSize        int    `json:"queue_size"`
	FailedItemsTotal int    `json:"failed_items_total"`
	DirtyOrgsTotal   int    `json:"dirty_orgs_total"`
	// SnapshotAgeSeconds reports how long ago the queue/failed snapshots were
	// last reconciled. -1 means no reconciliation has run yet (e.g. cold deploy).
	SnapshotAgeSeconds int64 `json:"snapshot_age_seconds"`
	BlocksDeletedTotal int64 `json:"blocks_deleted_total"`
	GracePeriodSeconds int64 `json:"grace_period_seconds"`
}

const (
	gcStatKeyTotalQueue          = "total_queue_depth"
	gcStatKeyTotalFailed         = "total_failed_items"
	gcStatKeyTotalDirtyOrgs      = "dirty_orgs_total"
	gcStatKeyLastReconcile       = "last_reconcile_run"
	gcStatKeyLastWorkerRun       = "last_worker_run"
	gcStatKeyLastScanRun         = "last_scan_run"
	gcStatKeyLastScanAttempt     = "last_scan_attempt"
	gcStatKeyLastScanSuccess     = "last_scan_success"
	gcStatKeyLastScanError       = "last_scan_error"
	gcStatKeyBlocksDeletedTotal  = "blocks_deleted_total"
	gcDefaultFailedItemsPageSize = 100
	gcDefaultFailedOrgPageSize   = 20
	gcAutoRetryFailedOrgLimit    = 20
	gcAutoRetryFailedItemLimit   = 100
	gcSnapshotDriftCheckEvery    = 10
	gcActiveOrgRecoveryEvery     = 10
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

	// dlqOpsMu serializes admin DLQ mutations (requeue/delete) so the
	// non-atomic SELECT+INSERT+DELETE in RequeueFailedItem cannot duplicate
	// queue rows under concurrent admin requests on the same leader.
	dlqOpsMu sync.Mutex

	// reconcilePasses counts serialized reconcile runs so we can do occasional
	// full drift checks without paying that cost on every pass.
	reconcilePasses uint64

	// consecutiveErrors is only accessed from the workerLoop goroutine
	// (via runWorkerOnce). If this changes, protect with s.mu.
	consecutiveErrors int
	workerPasses      uint64

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

	lastWorker := s.loadStatTime(gcStatKeyLastWorkerRun)
	if local := s.stats.LastWorkerRun(); lastWorker.IsZero() || (!local.IsZero() && local.After(lastWorker)) {
		lastWorker = local
	}
	lastScanAttempt := s.loadStatTime(gcStatKeyLastScanAttempt)
	if local := s.stats.LastScanAttempt(); lastScanAttempt.IsZero() || (!local.IsZero() && local.After(lastScanAttempt)) {
		lastScanAttempt = local
	}
	lastScanSuccess := s.loadStatTime(gcStatKeyLastScanSuccess)
	if local := s.stats.LastScanSuccess(); lastScanSuccess.IsZero() || (!local.IsZero() && local.After(lastScanSuccess)) {
		lastScanSuccess = local
	}
	lastScanError := s.loadStatString(gcStatKeyLastScanError)
	if lastScanError == "" {
		lastScanError = s.stats.LastScanError()
	}
	lastReconcile := s.loadStatTime(gcStatKeyLastReconcile)
	dirtyOrgs := s.loadStatInt(gcStatKeyTotalDirtyOrgs)
	blocksDeletedTotal := int64(s.loadStatInt(gcStatKeyBlocksDeletedTotal))
	if local := s.stats.BlocksDeleted(); local > blocksDeletedTotal {
		blocksDeletedTotal = local
	}
	// -1 means "no reconciliation has run yet" — distinct from "0 seconds old".
	// Dashboards/alerts can branch on this sentinel instead of reading a falsely
	// fresh age right after deploy.
	snapshotAgeSeconds := int64(-1)
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
		LastScanRun:        formatTime(lastScanAttempt),
		LastScanAttempt:    formatTime(lastScanAttempt),
		LastScanSuccess:    formatTime(lastScanSuccess),
		LastScanError:      lastScanError,
		LastReconcileRun:   formatTime(lastReconcile),
		QueueSize:          queueSize,
		FailedItemsTotal:   failedItemsTotal,
		DirtyOrgsTotal:     dirtyOrgs,
		SnapshotAgeSeconds: snapshotAgeSeconds,
		BlocksDeletedTotal: blocksDeletedTotal,
		GracePeriodSeconds: int64(s.config.GracePeriod.Seconds()),
	}
}

func (s *Service) loadStatString(key string) string {
	val, err := s.store.LoadGCStats(key)
	if err != nil {
		return ""
	}
	return val
}

// RefreshFailedItemSnapshot repairs stale failed-item counters derived from
// gc_org_stats/gc_stats. This is needed because gc_failed_items rows expire via
// TTL and those expirations do not mark orgs dirty on their own.
func (s *Service) RefreshFailedItemSnapshot() {
	s.reconcileMu.Lock()
	defer s.reconcileMu.Unlock()

	if err := s.refreshFailedItemSnapshotLocked(); err != nil {
		log.Printf("[GC] Failed to refresh failed-item snapshot: %v", err)
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
	exists, err := s.store.PendingItemExists(orgID, uuid.Nil, candidateAt, ItemBlock, blockID)
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
	// Run the worker immediately on startup so persisted queue work resumes
	// without waiting for the first ticker interval after restart.
	s.runWorkerOnce(ctx)

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
	if queueBefore > 0 && s.workerPasses%gcActiveOrgRecoveryEvery == 0 {
		s.recoverMissingActiveQueueOrgs()
	}
	s.workerPasses++

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
	s.persistStats()

	s.reconcileDirtyQueueStats(s.reconcileLimit())

	queueAfter := s.loadStatInt(gcStatKeyTotalQueue)
	metrics.GCQueueSize.Set(float64(queueAfter))
	metrics.GCQueueGrowthRate.Set(float64(queueAfter - queueBefore))

	if n > 0 {
		log.Printf("[GC Worker] Processed %d items", n)
	}
}

func (s *Service) recoverMissingActiveQueueOrgs() {
	snapshotOrgs, err := s.store.ListOrgsWithQueuedSnapshots(0)
	if err != nil {
		log.Printf("[GC] Failed to list queued snapshots for active-set recovery: %v", err)
		return
	}
	if len(snapshotOrgs) == 0 {
		return
	}

	activeOrgs, err := s.store.ListOrgsWithQueuedItems()
	if err != nil {
		log.Printf("[GC] Failed to list active queued orgs for recovery: %v", err)
		return
	}
	activeSet := make(map[uuid.UUID]struct{}, len(activeOrgs))
	for _, orgID := range activeOrgs {
		activeSet[orgID] = struct{}{}
	}

	now := time.Now().UTC()
	recovered := 0
	for _, orgID := range snapshotOrgs {
		if _, ok := activeSet[orgID]; ok {
			continue
		}
		if err := s.store.MarkOrgActive(orgID, now); err != nil {
			log.Printf("[GC] Failed to re-mark org %s active from queued snapshot: %v", orgID, err)
			continue
		}
		if err := s.store.MarkOrgDirty(orgID, now); err != nil {
			log.Printf("[GC] Failed to mark org %s dirty during active-set recovery: %v", orgID, err)
		}
		recovered++
	}
	if recovered > 0 {
		log.Printf("[GC] Recovered %d queued org(s) into active set from gc_org_stats", recovered)
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
	err := s.scanner.ScanOnce(ctx)
	retried := s.retryAutoRecoverableFailedItems()
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		log.Printf("[GC Scanner] Error: %v", err)
		s.stats.SetLastScanError(err.Error())
	} else if err == nil {
		s.stats.SetLastScanError("")
	}
	s.persistStats()
	if retried > 0 {
		log.Printf("[GC Scanner] Auto-requeued %d recoverable failed items", retried)
		s.TriggerWorker()
	}
	s.reconcileDirtyQueueStats(s.reconcileLimit())
	metrics.GCScannerDuration.Observe(time.Since(start).Seconds())
	metrics.GCLastScannerRun.Set(float64(time.Now().Unix()))
}

func (s *Service) retryAutoRecoverableFailedItems() int {
	orgs, err := s.store.ListOrgsWithFailedItems(gcAutoRetryFailedOrgLimit)
	if err != nil {
		log.Printf("[GC Scanner] Failed to list orgs with failed items for auto-retry: %v", err)
		return 0
	}

	retried := 0
	s.dlqOpsMu.Lock()
	defer s.dlqOpsMu.Unlock()

	for _, org := range orgs {
		if retried >= gcAutoRetryFailedItemLimit {
			break
		}

		items, err := s.store.ListFailedItems(org.OrgID, s.FailedItemsPageSize())
		if err != nil {
			log.Printf("[GC Scanner] Failed to list failed items for org %s: %v", org.OrgID, err)
			continue
		}

		for _, item := range items {
			if retried >= gcAutoRetryFailedItemLimit {
				break
			}

			eligible, err := s.isAutoRecoverableFailedItem(item)
			if err != nil {
				log.Printf("[GC Scanner] Failed to classify DLQ item %s/%s: %v", item.OrgID, item.ItemID, err)
				continue
			}
			if !eligible {
				continue
			}

			if err := s.store.RequeueFailedItem(item.OrgID, item.FailedAt, item.ItemType, item.ItemID, time.Now().UTC()); err != nil {
				log.Printf("[GC Scanner] Failed to auto-requeue DLQ item %s/%s: %v", item.OrgID, item.ItemID, err)
				continue
			}
			retried++
		}
	}

	return retried
}

func (s *Service) isAutoRecoverableFailedItem(item GCFailedItemInfo) (bool, error) {
	if item.ResolvedState != "" && item.ResolvedState != "open" {
		return false, nil
	}
	if !item.RequiresLibraryDeletedCheck {
		return false, nil
	}
	if item.ItemType != ItemCommit && item.ItemType != ItemFSObject {
		return false, nil
	}
	if item.LibraryID == uuid.Nil {
		return false, nil
	}
	if item.FailureCode != GCFailureCodeLibraryHardDeleteInProgress {
		return false, nil
	}

	exists, err := s.store.LibraryExists(item.LibraryID)
	if err != nil {
		return false, fmt.Errorf("confirm library existence for DLQ item %s/%s: %w", item.LibraryID, item.ItemID, err)
	}
	return !exists, nil
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
			oldestQueuedAt := prevStats.OldestQueuedAt
			if queueDepth == 0 {
				oldestQueuedAt = nil
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

	if err := s.refreshFailedItemSnapshotLocked(); err != nil {
		log.Printf("[GC] Failed to repair stale failed-item snapshot: %v", err)
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

func (s *Service) refreshFailedItemSnapshotLocked() error {
	snapshotOrgs, err := s.store.ListOrgsWithFailedItems(0)
	if err != nil {
		return err
	}

	if len(snapshotOrgs) == 0 {
		if s.loadStatInt(gcStatKeyTotalFailed) != 0 {
			s.saveStatInt(gcStatKeyTotalFailed, 0)
			metrics.GCFailedItemsTotal.Set(0)
		}
		return nil
	}

	repaired := 0
	now := time.Now().UTC()
	for _, org := range snapshotOrgs {
		failedDepth, err := s.store.RecountOrgFailedDepth(org.OrgID)
		if err != nil {
			log.Printf("[GC] Failed to recount stale failed depth for %s: %v", org.OrgID, err)
			continue
		}
		if failedDepth == org.FailedItemsTotal {
			continue
		}

		stats, err := s.store.GetOrgQueueStats(org.OrgID)
		if err != nil {
			log.Printf("[GC] Failed to load stale failed stats for %s: %v", org.OrgID, err)
			continue
		}
		stats.FailedDepth = failedDepth
		stats.UpdatedAt = now
		if err := s.store.SaveOrgQueueStats(stats); err != nil {
			log.Printf("[GC] Failed to save repaired failed stats for %s: %v", org.OrgID, err)
			continue
		}
		repaired++
	}

	if repaired == 0 {
		return nil
	}

	_, summedFailed, err := s.store.SumOrgQueueStats()
	if err != nil {
		return err
	}
	s.saveStatInt(gcStatKeyTotalFailed, summedFailed)
	metrics.GCFailedItemsTotal.Set(float64(summedFailed))
	log.Printf("[GC] Repaired stale failed-item snapshot for %d org(s); total_failed=%d", repaired, summedFailed)
	return nil
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

func (s *Service) ListFailedItemOrgs(limit int) ([]GCFailedItemOrgInfo, error) {
	if limit <= 0 {
		limit = gcDefaultFailedOrgPageSize
	}
	orgIDs, err := s.store.ListOrganizations()
	if err != nil {
		return nil, err
	}

	orgs := make([]GCFailedItemOrgInfo, 0)
	for _, orgID := range orgIDs {
		failedDepth, err := s.store.RecountOrgFailedDepth(orgID)
		if err != nil {
			return nil, err
		}
		if failedDepth <= 0 {
			continue
		}

		latestItems, err := s.store.ListFailedItems(orgID, 1)
		if err != nil {
			return nil, err
		}
		if len(latestItems) == 0 {
			continue
		}

		orgName, err := s.store.GetOrgName(orgID)
		if err != nil {
			orgName = ""
		}

		orgs = append(orgs, GCFailedItemOrgInfo{
			OrgID:            orgID,
			OrgName:          orgName,
			FailedItemsTotal: failedDepth,
			UpdatedAt:        latestItems[0].FailedAt,
		})
	}

	sort.Slice(orgs, func(i, j int) bool {
		if orgs[i].FailedItemsTotal != orgs[j].FailedItemsTotal {
			return orgs[i].FailedItemsTotal > orgs[j].FailedItemsTotal
		}
		if !orgs[i].UpdatedAt.Equal(orgs[j].UpdatedAt) {
			return orgs[i].UpdatedAt.After(orgs[j].UpdatedAt)
		}
		return orgs[i].OrgID.String() < orgs[j].OrgID.String()
	})
	if len(orgs) > limit {
		orgs = orgs[:limit]
	}
	return orgs, nil
}

func (s *Service) DeleteFailedItem(orgID uuid.UUID, failedAt time.Time, itemType ItemType, itemID string) error {
	if !s.hasLeadership(context.Background(), "admin_failed_item_delete") {
		return ErrNotLeader
	}
	s.dlqOpsMu.Lock()
	defer s.dlqOpsMu.Unlock()
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
	// Serialize DLQ admin mutations: RequeueFailedItem performs a non-atomic
	// SELECT+INSERT+DELETE in the store. Without this lock, two concurrent
	// requeues of the same failed item would both succeed at SELECT, both
	// INSERT into gc_queue (with different queued_at), and the second DELETE
	// would no-op — leaving a duplicated queue row that the worker would
	// process twice.
	s.dlqOpsMu.Lock()
	defer s.dlqOpsMu.Unlock()
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
		s.store.SaveGCStats(gcStatKeyLastWorkerRun, lastWorker.Format(time.RFC3339))
	}
	if lastScan := s.stats.LastScanRun(); !lastScan.IsZero() {
		s.store.SaveGCStats(gcStatKeyLastScanRun, lastScan.Format(time.RFC3339))
	}
	if lastScanAttempt := s.stats.LastScanAttempt(); !lastScanAttempt.IsZero() {
		s.store.SaveGCStats(gcStatKeyLastScanAttempt, lastScanAttempt.Format(time.RFC3339))
	}
	if lastScanSuccess := s.stats.LastScanSuccess(); !lastScanSuccess.IsZero() {
		s.store.SaveGCStats(gcStatKeyLastScanSuccess, lastScanSuccess.Format(time.RFC3339))
	}
	s.store.SaveGCStats(gcStatKeyLastScanError, s.stats.LastScanError())
	s.store.SaveGCStats(gcStatKeyBlocksDeletedTotal, fmt.Sprintf("%d", s.stats.BlocksDeleted()))
}

// restoreStats loads persisted GC stats from the database on startup.
func (s *Service) restoreStats() {
	if val, err := s.store.LoadGCStats(gcStatKeyLastWorkerRun); err == nil && val != "" {
		if t, err := time.Parse(time.RFC3339, val); err == nil {
			s.stats.SetLastWorkerRun(t)
		}
	}
	if val, err := s.store.LoadGCStats(gcStatKeyLastScanAttempt); err == nil && val != "" {
		if t, err := time.Parse(time.RFC3339, val); err == nil {
			s.stats.SetLastScanAttempt(t)
		}
	} else if val, err := s.store.LoadGCStats(gcStatKeyLastScanRun); err == nil && val != "" {
		if t, err := time.Parse(time.RFC3339, val); err == nil {
			s.stats.SetLastScanAttempt(t)
		}
	}
	if val, err := s.store.LoadGCStats(gcStatKeyLastScanSuccess); err == nil && val != "" {
		if t, err := time.Parse(time.RFC3339, val); err == nil {
			s.stats.SetLastScanSuccess(t)
		}
	}
	if val, err := s.store.LoadGCStats(gcStatKeyLastScanError); err == nil && val != "" {
		s.stats.SetLastScanError(val)
	}
	if val, err := s.store.LoadGCStats(gcStatKeyBlocksDeletedTotal); err == nil && val != "" {
		if n, err := strconv.ParseInt(val, 10, 64); err == nil {
			s.stats.blocksDeleted.Store(n)
		}
	}
}
