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
		GCErrorsTotal,
		GCItemsSkippedTotal,
		GCLastWorkerRun,
		GCLastScannerRun,
		GCScannerLastPhaseRun,
		GCWorkerDuration,
		GCScannerDuration,
		ActiveSessions,
		GCWorkerConsecutiveErrors,
		GCQueueGrowthRate,
		GCWorkerLastSuccessTimestamp,
		GCAuditEventsTotal,
		ChunkUploadTempOrphansCleaned,
	)
}
