package metrics

import "github.com/prometheus/client_golang/prometheus"

var (
	// HTTPRequestsTotal counts total HTTP requests by method, path pattern, and status.
	HTTPRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests.",
		},
		[]string{"method", "path", "status"},
	)

	// HTTPRequestDuration records HTTP request latency in seconds.
	HTTPRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request latency in seconds.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "path"},
	)

	// StorageOperationsTotal counts storage backend operations.
	StorageOperationsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "storage_operations_total",
			Help: "Total number of storage operations.",
		},
		[]string{"operation", "backend", "status"},
	)

	// GCQueueSize tracks the current GC queue depth.
	GCQueueSize = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "gc_queue_size",
			Help: "Current number of items in the GC queue.",
		},
	)

	// GCItemsProcessedTotal counts items successfully processed by the GC worker.
	GCItemsProcessedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gc_items_processed_total",
			Help: "Total number of items processed by garbage collection.",
		},
		[]string{"type"},
	)

	// GCItemsEnqueuedTotal counts items enqueued by each scanner phase.
	GCItemsEnqueuedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gc_items_enqueued_total",
			Help: "Total number of items enqueued by GC scanner phases.",
		},
		[]string{"phase"},
	)

	// GCScannerActionsTotal counts direct cleanup/recovery actions performed by scanner phases.
	GCScannerActionsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gc_scanner_actions_total",
			Help: "Total number of direct actions performed by GC scanner phases.",
		},
		[]string{"phase", "action"},
	)

	// GCErrorsTotal counts item processing failures in the GC worker.
	GCErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gc_errors_total",
			Help: "Total number of GC item processing errors.",
		},
		[]string{"type"},
	)

	// GCItemsSkippedTotal counts items skipped because they were re-referenced during grace period.
	GCItemsSkippedTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "gc_items_skipped_total",
			Help: "Total number of GC items skipped (re-referenced during grace period).",
		},
	)

	// GCZeroRefEnqueueFailuresTotal counts decrement-to-zero events whose
	// canonical candidate registration or follow-up queue insert failed. The blocks
	// schema refactor removed the per-org partition scan backfill, so a
	// non-zero value here is the only signal that a block hit ref_count=0
	// without being enqueued. Alert on sustained increase.
	GCZeroRefEnqueueFailuresTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gc_zero_ref_enqueue_failures_total",
			Help: "Number of zero-refcount blocks that failed to reach pending GC state. Sustained values indicate lost-to-GC blocks.",
		},
		[]string{"stage"},
	)

	// GCBlockCandidateDiscoveryDegradedTotal counts events where the canonical
	// gc_block_candidates row was usable but ensuring or repairing the
	// gc_block_candidates_by_day discovery row failed. These events do not imply
	// lost GC work; they indicate degraded scanner backfill/recovery safety.
	GCBlockCandidateDiscoveryDegradedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gc_block_candidate_discovery_degraded_total",
			Help: "Number of block candidate events where the canonical row succeeded but the discovery projection could not be ensured.",
		},
		[]string{"source"},
	)

	// GCLastWorkerRun records the Unix timestamp of the last worker pass.
	GCLastWorkerRun = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "gc_last_worker_run_timestamp_seconds",
			Help: "Unix timestamp of the last GC worker run.",
		},
	)

	// GCLastScannerRun records the Unix timestamp of the last scanner pass.
	GCLastScannerRun = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "gc_last_scanner_run_timestamp_seconds",
			Help: "Unix timestamp of the last GC scanner run.",
		},
	)

	// GCScannerLastPhaseRun records the Unix timestamp of the last run of each scanner phase.
	GCScannerLastPhaseRun = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "gc_scanner_last_phase_run_timestamp_seconds",
			Help: "Unix timestamp of the last run of each GC scanner phase.",
		},
		[]string{"phase"},
	)

	// GCWorkerDuration observes the duration of each GC worker pass.
	GCWorkerDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "gc_worker_duration_seconds",
			Help:    "Duration of GC worker passes in seconds.",
			Buckets: prometheus.DefBuckets,
		},
	)

	// GCScannerDuration observes the duration of each GC scanner pass.
	GCScannerDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "gc_scanner_duration_seconds",
			Help:    "Duration of GC scanner passes in seconds.",
			Buckets: prometheus.DefBuckets,
		},
	)

	// ActiveSessions tracks the number of active user sessions.
	ActiveSessions = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "active_sessions_total",
			Help: "Current number of active user sessions.",
		},
	)

	// GCWorkerConsecutiveErrors tracks consecutive worker errors for alerting.
	// Reset to 0 on successful pass, incremented on failure.
	GCWorkerConsecutiveErrors = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "gc_worker_consecutive_errors",
			Help: "Number of consecutive GC worker errors (reset on success). Alert if > 5.",
		},
	)

	// GCQueueGrowthRate tracks how fast the queue is growing (items enqueued minus processed per interval).
	GCQueueGrowthRate = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "gc_queue_growth_rate",
			Help: "Net queue growth per worker interval (positive = growing, negative = draining).",
		},
	)

	// GCFailedItemsTotal tracks how many items are currently in the GC DLQ.
	GCFailedItemsTotal = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "gc_failed_items_total",
			Help: "Current number of items in the GC dead-letter queue.",
		},
	)

	// GCDirtyOrgsTotal tracks how many orgs still need queue snapshot reconciliation.
	GCDirtyOrgsTotal = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "gc_dirty_orgs_total",
			Help: "Current number of orgs marked dirty for GC queue snapshot reconciliation.",
		},
	)

	// GCSnapshotAgeSeconds tracks how stale the GC queue snapshot is.
	GCSnapshotAgeSeconds = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "gc_snapshot_age_seconds",
			Help: "Age in seconds of the latest reconciled GC queue snapshot.",
		},
	)

	// GCReconcileDuration observes the duration of queue snapshot reconciliation passes.
	GCReconcileDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "gc_reconcile_duration_seconds",
			Help:    "Duration of GC queue snapshot reconciliation passes in seconds.",
			Buckets: prometheus.DefBuckets,
		},
	)

	// GCSnapshotDriftCorrectedTotal counts how many times the reconciler had to
	// overwrite global queue snapshots because they diverged from summed org stats.
	GCSnapshotDriftCorrectedTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "gc_snapshot_drift_corrected_total",
			Help: "Total number of GC snapshot drift corrections applied from summed org stats.",
		},
	)

	// GCActiveOrgRecoveryTriggersTotal counts how often the worker attempts to
	// rebuild gc_active_orgs from gc_org_stats. Labelled by trigger source.
	GCActiveOrgRecoveryTriggersTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gc_active_org_recovery_triggers_total",
			Help: "Total number of GC active-org recovery attempts triggered by the worker.",
		},
		[]string{"reason"},
	)

	// GCActiveOrgRecoveriesTotal counts how many org rows were actually restored
	// into gc_active_orgs from queued snapshots. Labelled by trigger source.
	GCActiveOrgRecoveriesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gc_active_org_recoveries_total",
			Help: "Total number of GC active-org rows restored from queue snapshots.",
		},
		[]string{"reason"},
	)

	// GCWorkerLastSuccessTimestamp records when the worker last successfully processed items.
	GCWorkerLastSuccessTimestamp = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "gc_worker_last_success_timestamp_seconds",
			Help: "Unix timestamp of last successful GC worker pass with items processed. Alert if stale > 1h.",
		},
	)

	// GCAuditEventsTotal counts audit log entries written.
	GCAuditEventsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gc_audit_events_total",
			Help: "Total number of audit log entries written by GC system.",
		},
		[]string{"action"},
	)

	// ChunkUploadTempOrphansCleaned counts stale chunked-upload temp files
	// reaped by the ChunkManager janitor goroutine.
	ChunkUploadTempOrphansCleaned = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "chunk_upload_temp_orphans_cleaned_total",
			Help: "Total number of abandoned chunked-upload temp files cleaned by the janitor.",
		},
		[]string{"source"}, // "tracker" (in-memory map) or "disk" (tempDir sweep)
	)

	// ChunkUploadFinalizationAttemptsTotal counts how often a chunked upload
	// tracker reaches the finalization gate, is still incomplete, or finds
	// another goroutine already finalizing it.
	ChunkUploadFinalizationAttemptsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "chunk_upload_finalization_attempts_total",
			Help: "Total number of chunked upload finalization gate outcomes, split by started, not_complete, and already_finalizing.",
		},
		[]string{"result"}, // "started", "not_complete", or "already_finalizing"
	)

	// UploadFinalizeHeadConflictsTotal counts metadata publish conflicts that
	// force an upload finalize retry.
	UploadFinalizeHeadConflictsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "upload_finalize_head_conflicts_total",
			Help: "Total number of upload finalize head conflicts by surface.",
		},
		[]string{"surface"},
	)

	// UploadFinalizeRetryExhaustedTotal counts finalize operations that hit the
	// retry budget before publishing metadata.
	UploadFinalizeRetryExhaustedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "upload_finalize_retry_exhausted_total",
			Help: "Total number of upload finalize operations that exhausted the retry budget.",
		},
		[]string{"surface"},
	)

	// UploadFinalizeAttempts observes how many publish attempts were needed for
	// each upload finalize call.
	UploadFinalizeAttempts = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "upload_finalize_attempts",
			Help:    "Number of publish attempts required by each upload finalize call.",
			Buckets: []float64{1, 2, 3, 5, 8, 13, 21},
		},
		[]string{"surface", "result"},
	)

	// UploadFinalizeDuration observes metadata finalize latency, including any
	// retries and backoff. Buckets extend past prometheus.DefBuckets so that
	// retry_exhausted finalize calls (up to 20 attempts with exponential
	// backoff and jitter) still fall into bounded buckets instead of +Inf.
	UploadFinalizeDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "upload_finalize_duration_seconds",
			Help:    "Duration of upload metadata finalize calls, including retries.",
			Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 15, 30, 60},
		},
		[]string{"surface", "result"},
	)
)

// Register registers all custom metrics with the default Prometheus registry.
func Register() {
	prometheus.MustRegister(
		HTTPRequestsTotal,
		HTTPRequestDuration,
		StorageOperationsTotal,
		GCQueueSize,
		GCItemsProcessedTotal,
		GCItemsEnqueuedTotal,
		GCScannerActionsTotal,
		GCErrorsTotal,
		GCItemsSkippedTotal,
		GCZeroRefEnqueueFailuresTotal,
		GCBlockCandidateDiscoveryDegradedTotal,
		GCLastWorkerRun,
		GCLastScannerRun,
		GCScannerLastPhaseRun,
		GCWorkerDuration,
		GCScannerDuration,
		ActiveSessions,
		GCWorkerConsecutiveErrors,
		GCQueueGrowthRate,
		GCFailedItemsTotal,
		GCDirtyOrgsTotal,
		GCSnapshotAgeSeconds,
		GCReconcileDuration,
		GCSnapshotDriftCorrectedTotal,
		GCActiveOrgRecoveryTriggersTotal,
		GCActiveOrgRecoveriesTotal,
		GCWorkerLastSuccessTimestamp,
		GCAuditEventsTotal,
		ChunkUploadTempOrphansCleaned,
		ChunkUploadFinalizationAttemptsTotal,
		UploadFinalizeHeadConflictsTotal,
		UploadFinalizeRetryExhaustedTotal,
		UploadFinalizeAttempts,
		UploadFinalizeDuration,
	)
}
