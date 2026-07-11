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
	gcMinSnapshotRecalcInterval  = time.Minute
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

// SetOnlyOfficeReconciler wires the OnlyOffice pending-blocks reconciler into
// the scanner. Called from the API layer after the OnlyOffice handler exists.
func (s *Service) SetOnlyOfficeReconciler(r OnlyOfficeReconciler) {
	if s == nil || s.scanner == nil {
		return
	}
	s.scanner.SetOnlyOfficeReconciler(r)
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

// RefreshFailedItemSnapshot forces an exact refresh of failed-item-related
// queue snapshots before admin/status views read them. This is intentionally
// heavier than GetOrgQueueStats and should stay off the mutation hot path.
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
	candidateAt, candidateErr := s.store.EnsureBlockGCCandidate(orgID, blockID, storageClass, time.Now())
	if candidateErr != nil && candidateAt.IsZero() {
		return candidateErr
	}
	exists, err := s.store.PendingItemExists(orgID, uuid.Nil, candidateAt, ItemBlock, blockID)
	if err != nil {
		return errors.Join(candidateErr, err)
	}
	if exists {
		return nil
	}
	if candidateErr != nil {
		metrics.GCBlockCandidateDiscoveryDegradedTotal.WithLabelValues("service").Inc()
		log.Printf("[GC] WARNING: block candidate discovery degraded for org=%s block=%s: %v", orgID, blockID, candidateErr)
	}
	if err := s.store.EnqueueItem(orgID, candidateAt, ItemBlock, blockID, libraryID, storageClass, 0); err != nil {
		return errors.Join(candidateErr, err)
	}
	return nil
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
	// Stamp the block representation at enqueue time (the library is still live
	// here) so fs_object GC never has to re-resolve it from a possibly-deleted
	// library row later.
	blockRepresentationID, err := resolveRequiredLibraryBlockRepresentation(s.store, orgID, libraryID, "", "commit enqueue")
	if err != nil {
		return err
	}
	items := make([]QueueItem, 0, len(commitIDs))
	for _, id := range commitIDs {
		items = append(items, QueueItem{
			OrgID:                 orgID,
			QueuedAt:              now,
			ItemType:              ItemCommit,
			ItemID:                id,
			LibraryID:             libraryID,
			BlockRepresentationID: blockRepresentationID,
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
	if reason, ok := s.shouldRecoverMissingActiveQueueOrgs(queueBefore); ok {
		s.recoverMissingActiveQueueOrgs(reason, s.queueSnapshotRecalcMinInterval())
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

	s.reconcileDirtyQueueStats(s.reconcileLimit(), s.queueSnapshotRecalcMinInterval())

	queueAfter := s.loadStatInt(gcStatKeyTotalQueue)
	metrics.GCQueueSize.Set(float64(queueAfter))
	metrics.GCQueueGrowthRate.Set(float64(queueAfter - queueBefore))

	if n > 0 {
		log.Printf("[GC Worker] Processed %d items", n)
	}
}

func (s *Service) shouldRecoverMissingActiveQueueOrgs(queueBefore int) (string, bool) {
	dirtyHint := false
	if queueBefore <= 0 {
		dirtyOrgs, err := s.store.ListDirtyOrgs(1)
		if err != nil {
			log.Printf("[GC] Failed to list dirty orgs for recovery trigger: %v", err)
			return "", false
		}
		if len(dirtyOrgs) == 0 {
			return "", false
		}
		dirtyHint = true
	}

	activeOrgs, err := s.store.ListOrgsWithQueuedItems()
	if err != nil {
		log.Printf("[GC] Failed to list active queued orgs for recovery trigger: %v", err)
		return "", false
	}
	if len(activeOrgs) == 0 {
		reason := "no_active_orgs"
		if dirtyHint {
			reason = "dirty_orgs"
		}
		metrics.GCActiveOrgRecoveryTriggersTotal.WithLabelValues(reason).Inc()
		return reason, true
	}
	if s.workerPasses%gcActiveOrgRecoveryEvery == 0 {
		metrics.GCActiveOrgRecoveryTriggersTotal.WithLabelValues("periodic").Inc()
		return "periodic", true
	}
	return "", false
}

func (s *Service) recoverMissingActiveQueueOrgs(reason string, staleSnapshotAfter time.Duration) {
	snapshotOrgs, err := s.store.ListOrgsWithQueuedSnapshots(0)
	if err != nil {
		log.Printf("[GC] Failed to list queued snapshots for active-set recovery: %v", err)
		return
	}
	dirtyOrgs, err := s.store.ListDirtyOrgs(0)
	if err != nil {
		log.Printf("[GC] Failed to list dirty orgs for active-set recovery: %v", err)
		return
	}
	if len(snapshotOrgs) == 0 && len(dirtyOrgs) == 0 {
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
	candidateByOrg := make(map[uuid.UUID]gcSnapshotCandidate, len(snapshotOrgs)+len(dirtyOrgs))
	for _, orgID := range snapshotOrgs {
		candidateByOrg[orgID] = gcSnapshotCandidate{OrgID: orgID}
	}
	for _, dirtyOrg := range dirtyOrgs {
		candidate := candidateByOrg[dirtyOrg.OrgID]
		candidate.OrgID = dirtyOrg.OrgID
		candidate.DirtyBefore = dirtyOrg.MarkedAt
		candidate.HasDirtyMarker = true
		candidateByOrg[dirtyOrg.OrgID] = candidate
	}

	now := time.Now().UTC()
	recovered := 0
	staleSnapshotsQueuedForRefresh := 0
	for orgID, candidate := range candidateByOrg {
		if _, ok := activeSet[orgID]; ok {
			continue
		}
		oldestQueuedAt, oldestErr := s.store.GetOldestQueuedAt(orgID)
		if oldestErr != nil {
			log.Printf("[GC] Failed to probe queued rows for active-set recovery org %s: %v", orgID, oldestErr)
			continue
		}
		if oldestQueuedAt == nil {
			if candidate.HasDirtyMarker {
				continue
			}
			prevStats, err := s.store.GetOrgQueueStats(orgID)
			if err != nil {
				log.Printf("[GC] Failed to load org queue stats during stale snapshot recovery for org %s: %v", orgID, err)
				continue
			}
			if prevStats.QueueDepth <= 0 {
				continue
			}
			lastExactRefresh := prevStats.RecalculatedAt
			if lastExactRefresh.IsZero() {
				lastExactRefresh = prevStats.UpdatedAt
			}
			if !lastExactRefresh.IsZero() && staleSnapshotAfter > 0 && time.Since(lastExactRefresh) < staleSnapshotAfter {
				continue
			}
			if err := s.store.MarkOrgDirty(orgID, now); err != nil {
				log.Printf("[GC] Failed to mark org %s dirty during stale snapshot recovery: %v", orgID, err)
				continue
			}
			staleSnapshotsQueuedForRefresh++
			continue
		}
		if err := s.store.MarkOrgActive(orgID, now); err != nil {
			log.Printf("[GC] Failed to re-mark org %s active during recovery: %v", orgID, err)
			continue
		}
		if err := s.store.MarkOrgDirty(orgID, now); err != nil {
			log.Printf("[GC] Failed to mark org %s dirty during active-set recovery: %v", orgID, err)
		}
		recovered++
	}
	if recovered > 0 {
		metrics.GCActiveOrgRecoveriesTotal.WithLabelValues(reason).Add(float64(recovered))
		log.Printf("[GC] Recovered %d queued org(s) into active set from GC snapshots/dirty markers (reason=%s)", recovered, reason)
	}
	if staleSnapshotsQueuedForRefresh > 0 {
		log.Printf("[GC] Queued %d stale queued snapshot(s) for exact refresh after active-set recovery found no live gc_queue rows", staleSnapshotsQueuedForRefresh)
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
	s.reconcileDirtyQueueStats(s.reconcileLimit(), s.queueSnapshotRecalcMinInterval())
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
	guardMode := effectiveLibraryGuardMode(item.LibraryGuardMode, item.RequiresLibraryDeletedCheck)
	if guardMode == LibraryGuardNone {
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

	var exists bool
	var err error
	switch guardMode {
	case LibraryGuardCanonicalMustBeAbsent:
		exists, err = s.store.CanonicalLibraryExists(item.OrgID, item.LibraryID)
	case LibraryGuardDeletedAtIdentity:
		exists, err = s.store.LibraryExists(item.LibraryID)
	default:
		// Unknown guard mode (e.g. a row written by a newer binary): do not guess a
		// revalidation path. Leave it in the DLQ for operator inspection rather than
		// auto-recovering it against the wrong existence check.
		return false, nil
	}
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

type gcSnapshotCandidate struct {
	OrgID          uuid.UUID
	DirtyBefore    time.Time
	HasDirtyMarker bool
	ForceRefresh   bool
}

func (s *Service) queueSnapshotRecalcMinInterval() time.Duration {
	interval := 2 * s.config.WorkerInterval
	if interval < gcMinSnapshotRecalcInterval {
		return gcMinSnapshotRecalcInterval
	}
	return interval
}

func (s *Service) reconcileSnapshotCandidatesLocked(candidates []gcSnapshotCandidate, minInterval time.Duration) {
	start := time.Now()
	defer func() {
		metrics.GCReconcileDuration.Observe(time.Since(start).Seconds())
	}()

	totalQueue := s.loadStatInt(gcStatKeyTotalQueue)
	totalFailed := s.loadStatInt(gcStatKeyTotalFailed)

	// deferred counts candidates that still need an exact refresh but did not
	// get one this pass (throttled, or errored). While it is > 0 the snapshots
	// are knowingly stale, so we must not advance last_reconcile_run /
	// snapshot_age_seconds and claim the snapshot is fresh.
	deferred := 0

	for _, candidate := range candidates {
		prevStats, err := s.store.GetOrgQueueStats(candidate.OrgID)
		if err != nil {
			log.Printf("[GC] Failed to load org queue stats for %s: %v", candidate.OrgID, err)
			deferred++
			continue
		}
		if candidate.HasDirtyMarker && !candidate.ForceRefresh && !prevStats.RecalculatedAt.IsZero() && !prevStats.RecalculatedAt.Before(candidate.DirtyBefore) {
			if err := s.store.ClearDirtyOrg(candidate.OrgID, candidate.DirtyBefore); err != nil {
				log.Printf("[GC] Failed to clear already-refreshed dirty org %s: %v", candidate.OrgID, err)
			}
			continue
		}
		if !candidate.ForceRefresh && minInterval > 0 && !prevStats.RecalculatedAt.IsZero() && time.Since(prevStats.RecalculatedAt) < minInterval {
			deferred++
			continue
		}

		nextStats, err := s.store.RecalculateOrgQueueStats(candidate.OrgID)
		if err != nil {
			log.Printf("[GC] Failed to recalculate org queue stats for %s: %v", candidate.OrgID, err)
			deferred++
			continue
		}

		totalQueue += nextStats.QueueDepth - prevStats.QueueDepth
		if totalQueue < 0 {
			totalQueue = 0
		}
		totalFailed += nextStats.FailedDepth - prevStats.FailedDepth
		if totalFailed < 0 {
			totalFailed = 0
		}

		if nextStats.QueueDepth > 0 {
			if err := s.store.MarkOrgActive(candidate.OrgID, nextStats.RecalculatedAt); err != nil {
				log.Printf("[GC] Failed to preserve active org %s after snapshot refresh: %v", candidate.OrgID, err)
			}
		}
		if candidate.HasDirtyMarker {
			if err := s.store.ClearDirtyOrg(candidate.OrgID, candidate.DirtyBefore); err != nil {
				log.Printf("[GC] Failed to clear dirty org %s: %v", candidate.OrgID, err)
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

	remainingDirtyCount := 0
	if remainingDirty, err := s.store.ListDirtyOrgs(0); err == nil {
		remainingDirtyCount = len(remainingDirty)
	} else {
		log.Printf("[GC] Failed to list dirty orgs after snapshot refresh: %v", err)
	}
	s.saveStatInt(gcStatKeyTotalQueue, totalQueue)
	s.saveStatInt(gcStatKeyTotalFailed, totalFailed)
	s.saveStatInt(gcStatKeyTotalDirtyOrgs, remainingDirtyCount)
	metrics.GCQueueSize.Set(float64(totalQueue))
	metrics.GCFailedItemsTotal.Set(float64(totalFailed))
	metrics.GCDirtyOrgsTotal.Set(float64(remainingDirtyCount))

	// last_reconcile_run / snapshot_age_seconds must mean "the snapshots were
	// confirmed current", not "a pass ran". Only advance them when no candidate
	// was left needing a refresh; otherwise report the real age so dashboards do
	// not see a falsely fresh snapshot during a throttle/deferral window.
	if deferred == 0 {
		lastRun := time.Now().UTC()
		if err := s.store.SaveGCStats(gcStatKeyLastReconcile, lastRun.Format(time.RFC3339)); err != nil {
			log.Printf("[GC] Failed to persist last reconcile run: %v", err)
		}
		metrics.GCSnapshotAgeSeconds.Set(0)
	} else if prev := s.loadStatTime(gcStatKeyLastReconcile); !prev.IsZero() {
		age := time.Since(prev).Seconds()
		if age < 0 {
			age = 0
		}
		metrics.GCSnapshotAgeSeconds.Set(age)
	}
}

func (s *Service) reconcileDirtyQueueStats(limit int, minInterval time.Duration) {
	s.reconcileMu.Lock()
	defer s.reconcileMu.Unlock()

	dirtyOrgs, err := s.store.ListDirtyOrgs(limit)
	if err != nil {
		log.Printf("[GC] Failed to list dirty orgs: %v", err)
		return
	}
	candidates := make([]gcSnapshotCandidate, 0, len(dirtyOrgs))
	for _, dirtyOrg := range dirtyOrgs {
		candidates = append(candidates, gcSnapshotCandidate{
			OrgID:          dirtyOrg.OrgID,
			DirtyBefore:    dirtyOrg.MarkedAt,
			HasDirtyMarker: true,
		})
	}
	s.reconcileSnapshotCandidatesLocked(candidates, minInterval)
}

func (s *Service) refreshFailedItemSnapshotLocked() error {
	snapshotOrgs, err := s.store.ListOrgsWithFailedItems(0)
	if err != nil {
		return err
	}

	dirtyOrgs, err := s.store.ListDirtyOrgs(0)
	if err != nil {
		return err
	}

	candidateByOrg := make(map[uuid.UUID]gcSnapshotCandidate, len(snapshotOrgs)+len(dirtyOrgs))
	for _, org := range snapshotOrgs {
		candidateByOrg[org.OrgID] = gcSnapshotCandidate{OrgID: org.OrgID}
	}
	for _, dirtyOrg := range dirtyOrgs {
		candidateByOrg[dirtyOrg.OrgID] = gcSnapshotCandidate{
			OrgID:          dirtyOrg.OrgID,
			DirtyBefore:    dirtyOrg.MarkedAt,
			HasDirtyMarker: true,
		}
	}
	if len(candidateByOrg) == 0 {
		s.reconcileSnapshotCandidatesLocked(nil, 0)
		return nil
	}

	candidates := make([]gcSnapshotCandidate, 0, len(candidateByOrg))
	for _, candidate := range candidateByOrg {
		candidates = append(candidates, candidate)
	}
	s.reconcileSnapshotCandidatesLocked(candidates, 0)
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
	return s.store.ListOrgsWithFailedItems(limit)
}

func (s *Service) refreshOrgQueueStatsNow(orgID uuid.UUID) error {
	s.reconcileMu.Lock()
	defer s.reconcileMu.Unlock()

	candidate := gcSnapshotCandidate{OrgID: orgID, ForceRefresh: true}
	dirtyOrgs, err := s.store.ListDirtyOrgs(0)
	if err != nil {
		return err
	}
	for _, dirtyOrg := range dirtyOrgs {
		if dirtyOrg.OrgID != orgID {
			continue
		}
		candidate.DirtyBefore = dirtyOrg.MarkedAt
		candidate.HasDirtyMarker = true
		break
	}
	s.reconcileSnapshotCandidatesLocked([]gcSnapshotCandidate{candidate}, 0)
	return nil
}

func (s *Service) DeleteFailedItem(orgID uuid.UUID, failedAt time.Time, itemType ItemType, itemID string) error {
	if !s.tryClaimLeadershipForAdmin(context.Background(), "admin_failed_item_delete") {
		return ErrNotLeader
	}
	s.dlqOpsMu.Lock()
	defer s.dlqOpsMu.Unlock()
	err := s.store.DeleteFailedItem(orgID, failedAt, itemType, itemID)
	if err != nil {
		return err
	}
	if err := s.refreshOrgQueueStatsNow(orgID); err != nil {
		log.Printf("[GC] WARNING: failed to refresh GC snapshot after admin delete for org %s: %v", orgID, err)
	}
	return nil
}

func (s *Service) RequeueFailedItem(orgID uuid.UUID, failedAt time.Time, itemType ItemType, itemID string) error {
	if !s.tryClaimLeadershipForAdmin(context.Background(), "admin_failed_item_requeue") {
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
	if err := s.refreshOrgQueueStatsNow(orgID); err != nil {
		log.Printf("[GC] WARNING: failed to refresh GC snapshot after admin requeue for org %s: %v", orgID, err)
	}
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

// tryClaimLeadershipForAdmin is hasLeadership plus a synchronous recovery
// path for human-triggered admin operations (Requeue/Delete from the GC
// admin panel). It tries, in order:
//
//  1. The cached IsLeader() check — fast no-op if we already lead.
//  2. A normal TryAcquireOrRenew — succeeds if the lease row was deleted by
//     TTL expiry between the renewal loop's last tick and this call.
//  3. A stale-lease takeover via TryTakeoverIfStale — succeeds if a previous
//     leader crashed without Release() and its heartbeat is older than
//     2 × renewalInterval. This is what unsticks single-instance dev after
//     `kill -9` / OOM where the row's TTL is still ticking but no live
//     process is renewing it.
//
// Only used in the admin DLQ path, never in the worker hot loop.
func (s *Service) tryClaimLeadershipForAdmin(ctx context.Context, component string) bool {
	if s.hasLeadership(ctx, component) {
		return true
	}
	if _, err := s.lease.TryAcquireOrRenew(ctx); err != nil {
		log.Printf("[GC] Admin-triggered lease claim failed for %s: %v", component, err)
	}
	if s.lease.IsLeader() {
		return true
	}
	// Stale-lease window: 2 × renewal interval. Two missed heartbeats means
	// the previous leader is dead — a healthy leader would have refreshed
	// twice by now. Smaller windows risk stealing from a momentarily-slow
	// leader; larger windows make admin recovery feel as slow as TTL.
	staleness := 2 * s.leaseRenewalInterval()
	if _, err := s.lease.TryTakeoverIfStale(ctx, staleness); err != nil {
		log.Printf("[GC] Admin-triggered stale-lease takeover failed for %s: %v", component, err)
		return false
	}
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
