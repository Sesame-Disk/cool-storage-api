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

	// LibraryDeleteRepresentationResolutionFailures counts library delete
	// operations that could not resolve a canonical block representation before
	// writing the GC marker. A non-zero rate signals a delete path or migration
	// that would leave a library un-purgeable; labelled by operation only (no
	// org/library id — those stay in the structured log).
	LibraryDeleteRepresentationResolutionFailures = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gc_library_delete_representation_resolution_failures_total",
			Help: "Library delete operations where block representation resolution failed, by operation.",
		},
		[]string{"operation"},
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

	// ChunkUploadFinalizeOutcomeCacheEntries reports the current number of
	// residual finalized-upload outcomes kept for late final-chunk retries.
	ChunkUploadFinalizeOutcomeCacheEntries = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "chunk_upload_finalize_outcome_cache_entries",
			Help: "Current number of cached finalized chunk-upload outcomes retained for late retries.",
		},
	)

	// ChunkUploadFinalizeOutcomeCacheEvictionsTotal counts removals from the
	// residual finalize-outcome cache, whether by TTL expiry or hard-cap pressure.
	ChunkUploadFinalizeOutcomeCacheEvictionsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "chunk_upload_finalize_outcome_cache_evictions_total",
			Help: "Total number of cached finalized chunk-upload outcomes evicted from the residual retry cache.",
		},
		[]string{"reason"}, // "expired" or "capacity"
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

	// BlockUploadVerifyDuration observes how long the web block-upload commit's
	// per-block verification pass (verifyManifestBlocks: liveness + physical
	// presence + ownership + size, per distinct block, bounded concurrency)
	// took, labeled by whether every block turned out ready or some needed
	// re-upload. This — plus BlockUploadSession* and BlockUploadStagedBlocksTotal
	// below — closes the observability gap noted in docs/WEB-BLOCK-UPLOAD.md
	// finding 8: finalizeStoredUploadMetadata already had metrics, but the
	// verification pass, the session claim/wait path, and staging did not.
	BlockUploadVerifyDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "block_upload_verify_duration_seconds",
			Help:    "Duration of the web block-upload commit's per-block verification pass.",
			Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 15, 30, 60, 120},
		},
		[]string{"result"}, // "ready" or "needs_upload"
	)

	// BlockUploadVerifyBlocksTotal counts distinct blocks classified during web
	// block-upload commit verification, by outcome.
	BlockUploadVerifyBlocksTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "block_upload_verify_blocks_total",
			Help: "Total number of blocks classified during web block-upload commit verification, by outcome.",
		},
		[]string{"status"}, // "ready", "needs_upload", or "size_mismatch"
	)

	// BlockUploadVerifyErrorsTotal counts infrastructure failures during the web
	// block-upload commit's verification pass, before a logical verification
	// result could be emitted.
	BlockUploadVerifyErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "block_upload_verify_errors_total",
			Help: "Total number of infrastructure errors during web block-upload commit verification.",
		},
		[]string{"stage"}, // "presence" or "classify"
	)

	// BlockUploadSessionClaimTotal counts commit-claim outcomes for concurrent
	// file-from-blocks requests on the same session (R7's LWT claim).
	BlockUploadSessionClaimTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "block_upload_session_claim_total",
			Help: "Total number of web block-upload session commit claim attempts, by outcome.",
		},
		// "won": this request finalized the file.
		// "lost_result_ready": lost the claim, but the winner's result was
		//   already available (no wait, or the wait completed before timing out).
		// "lost_timeout": lost the claim and the ~10s wait for the winner's
		//   result expired; the client must retry.
		// "lost_conflict": lost the claim to a commit for a DIFFERENT manifest
		//   on the same session (permanent, not retryable as-is).
		[]string{"result"},
	)

	// BlockUploadSessionWaitDuration observes how long a losing/retried commit
	// waited for a concurrent winner's result (bounded to ~10s).
	BlockUploadSessionWaitDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "block_upload_session_wait_duration_seconds",
			Help:    "Duration a web block-upload commit waited for a concurrent winner's result.",
			Buckets: []float64{0.1, 0.25, 0.5, 1, 2, 4, 6, 8, 10, 15},
		},
	)

	// BlockUploadStagedBlocksTotal counts blocks materialized (staged: metadata
	// + provisional reference) under a web block-upload session at
	// /blocks/upload, by whether the underlying S3 PUT was a new write or a
	// dedup no-op (R9 — a session governs a block either way).
	BlockUploadStagedBlocksTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "block_upload_staged_blocks_total",
			Help: "Total number of blocks materialized under a web block-upload session.",
		},
		[]string{"new"}, // "true" or "false"
	)

	// BlockUploadConcurrencyRejectionsTotal counts session-mode /blocks/upload
	// requests rejected with 429 because the caller already had the configured
	// maximum number of concurrent block uploads in flight
	// (web_uploads.max_concurrent_block_uploads_per_user). A non-zero rate means
	// the per-user in-flight cap is biting; use it to tune the limit before/after
	// flag-on. See docs/WEB-BLOCK-UPLOAD.md item 18.
	BlockUploadConcurrencyRejectionsTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "block_upload_concurrency_rejections_total",
			Help: "Total number of session-mode /blocks/upload requests rejected by the per-user concurrency cap.",
		},
	)

	// BlockUploadSessionAdmissionRejectionsTotal counts web block-upload requests
	// rejected by the staging caps (docs/WEB-BLOCK-UPLOAD.md item 1), by reason:
	//   "max_sessions"  — CreateBlockUploadSession over the per-user concurrent
	//                     uncommitted-session cap (all LWT slots claimed).
	//   "staged_blocks" — the per-session staging ceiling was hit, on EITHER path:
	//                     CreateBlockUploadSession rejecting a declared size over the
	//                     ceiling (413, fail-fast), OR /blocks/upload rejecting a new
	//                     block over a session's per-bucket staged-block cap (429).
	// A non-zero rate means a cap is biting; use it to tune the limits before/after
	// flag-on.
	BlockUploadSessionAdmissionRejectionsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "block_upload_session_admission_rejections_total",
			Help: "Total number of web block-upload requests rejected by the staging caps, by reason.",
		},
		[]string{"reason"},
	)

	// BlockUploadMaterializationRetriesTotal counts bounded store->materialize
	// retries taken by the upload funnels, by surface and reason. The reason is
	// derived from the retry PHASE (which callback failed), never the sentinel:
	//   "gc_fence"       — the block is fenced by a GC delete claim, in either phase.
	//   "probe"          — a retryable failure in the STORE phase (probe/HEAD/repair/PUT).
	//                      The shipped store callbacks return only a fence (counted as
	//                      gc_fence) or non-retryable raw errors, so in practice this
	//                      label is reached only if a store callback opts into the
	//                      transient sentinel; it is NOT a claim that every store error
	//                      is retried.
	//   "materialization"— a retryable failure in the metadata materialize phase
	//                      (RegisterUploadedBlock / mapping write).
	// The invariant that matters for F14: a materialize-phase failure is ALWAYS
	// "materialization", never "probe" — a metadata write is never counted as a probe.
	BlockUploadMaterializationRetriesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "block_upload_materialization_retries_total",
			Help: "Total bounded block upload store->materialize retries by surface and reason.",
		},
		[]string{"surface", "reason"},
	)

	// SyncPutBlockBodyBytes measures request bodies on the desktop-sync block
	// route that passed the size gate. The cap on that route was originally
	// derived from the web uploader's chunk ceiling rather than from observed
	// traffic, which is how it ended up 16x oversized; this histogram is what
	// makes the next adjustment evidence-based instead of another inherited
	// constant.
	//
	// It is observed right after the read, before the block id is checked and
	// before anything is stored, so it describes *accepted request bodies*, not
	// successful uploads and not necessarily well-behaved clients. Correlate it
	// with successful block validation before moving the cap.
	//
	// Buckets run 64 KiB to 64 MiB so both the expected ~8 MiB block and anything
	// approaching the validation ceiling are visible.
	SyncPutBlockBodyBytes = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "sync_put_block_body_bytes",
			Help:    "Observed PUT /seafhttp/repo/:repo_id/block/:block_id body size in bytes.",
			Buckets: prometheus.ExponentialBuckets(65536, 2, 11),
		},
	)

	// SyncPutBlockRejectedTotal counts blocks refused before or during the read.
	//
	// A non-zero "too_large" reports over-cap *attempts* and is the first place
	// the failure mode of lowering a cap would show up. It does not by itself
	// prove legitimate traffic was rejected: an authenticated client sending
	// deliberately oversized bodies moves the same counter. Treat it as a
	// prompt to investigate, not as evidence the cap is wrong.
	SyncPutBlockRejectedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "sync_put_block_rejected_total",
			Help: "Total PUT block requests rejected, by reason.",
		},
		[]string{"reason"},
	)

	// UploadLinkWriteThrottledTotal counts anonymous upload-link writes refused by
	// the rate limiters. The reason distinguishes which bucket fired:
	//   "client" — the (IP, token) bucket: one uploader going too fast
	//   "token"  — the per-token bucket: one link being hit from many addresses
	// Those call for opposite responses, so they must not be summed. The token
	// itself is never a label — it is a bearer credential and would be unbounded
	// cardinality besides.
	UploadLinkWriteThrottledTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "upload_link_write_throttled_total",
			Help: "Total anonymous upload-link write requests throttled, by which bucket refused them.",
		},
		[]string{"reason"},
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
		LibraryDeleteRepresentationResolutionFailures,
		ChunkUploadTempOrphansCleaned,
		ChunkUploadFinalizationAttemptsTotal,
		ChunkUploadFinalizeOutcomeCacheEntries,
		ChunkUploadFinalizeOutcomeCacheEvictionsTotal,
		UploadFinalizeHeadConflictsTotal,
		UploadFinalizeRetryExhaustedTotal,
		UploadFinalizeAttempts,
		UploadFinalizeDuration,
		BlockUploadVerifyDuration,
		BlockUploadVerifyBlocksTotal,
		BlockUploadVerifyErrorsTotal,
		BlockUploadSessionClaimTotal,
		BlockUploadSessionWaitDuration,
		BlockUploadStagedBlocksTotal,
		BlockUploadConcurrencyRejectionsTotal,
		BlockUploadSessionAdmissionRejectionsTotal,
		BlockUploadMaterializationRetriesTotal,
		SyncPutBlockBodyBytes,
		SyncPutBlockRejectedTotal,
		UploadLinkWriteThrottledTotal,
	)
}
