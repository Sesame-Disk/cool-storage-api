package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/downloadadmission"
	"github.com/Sesame-Disk/sesamefs/internal/httputil"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	"github.com/Sesame-Disk/sesamefs/internal/streaming"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// stalledPrefetchReader blocks the very first block read until its context is
// cancelled, then holds the return so the test can inspect the window where the
// prefetch is cancelled but still running.
type stalledPrefetchReader struct {
	startOnce            sync.Once
	started              chan struct{}
	cancellationObserved chan struct{}
	allowReturn          chan struct{}
}

// stall is shared by both entry points because StreamBlocks may prefetch through
// either one depending on whether the library is encrypted.
func (r *stalledPrefetchReader) stall(ctx context.Context) error {
	r.startOnce.Do(func() { close(r.started) })
	<-ctx.Done()
	close(r.cancellationObserved)
	<-r.allowReturn
	return ctx.Err()
}

func (r *stalledPrefetchReader) GetBlock(ctx context.Context, _ string) ([]byte, error) {
	return nil, r.stall(ctx)
}

func (r *stalledPrefetchReader) GetBlockReader(ctx context.Context, _ string) (io.ReadCloser, error) {
	return nil, r.stall(ctx)
}

func (r *stalledPrefetchReader) GetBlockSize(context.Context, string) (int64, error) {
	return 0, nil
}

func stalledPrefetchAdmissionConfig() config.DownloadAdmissionConfig {
	cfg := zipAdmissionConfig()
	cfg.IdleWriteTimeout = 80 * time.Millisecond
	return cfg
}

type stalledPrefetchWriter struct {
	gin.ResponseWriter
}

func (w *stalledPrefetchWriter) SetWriteDeadline(time.Time) error { return nil }

// TestStreamBlocksStalledFirstPrefetchIsBoundedByIdleInterval is the D4 half of
// the pre-first-write gap. streamFileFromBlocks calls StartStreaming, sets
// headers, calls c.Status(200) and only then enters StreamBlocks, which starts
// prefetching block 0 before emitting any byte. Until the idle interval opened
// at the phase change, that first storage read ran with no deadline of its own.
//
// The deferred c.Status(200) is part of the regression rather than incidental
// setup: it is the call that used to clear the interval a moment after it began,
// because Gin only records the status and leaves the writer uncommitted.
func TestStreamBlocksStalledFirstPrefetchIsBoundedByIdleInterval(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := stalledPrefetchAdmissionConfig()
	coordinator, err := downloadadmission.New(&cfg)
	if err != nil {
		t.Fatal(err)
	}

	baseRecorder := httptest.NewRecorder()
	baseContext, _ := gin.CreateTestContext(baseRecorder)
	c := baseContext
	c.Writer = &stalledPrefetchWriter{ResponseWriter: baseContext.Writer}
	c.Request = httptest.NewRequest(http.MethodGet, "/seafhttp/files/token/name.bin", nil)

	request, err := downloadadmission.NewAuthenticatedRequest(downloadadmission.ProfileFile, "org-1", "file-user")
	if err != nil {
		t.Fatal(err)
	}
	lifecycle, reason, err := httputil.AcquireDownloadAdmission(c, coordinator, cfg, request)
	if err != nil || reason != "" || lifecycle == nil {
		t.Fatalf("AcquireDownloadAdmission = (%v, %q, %v)", lifecycle, reason, err)
	}

	streamCtx, err := lifecycle.StartStreaming()
	if err != nil {
		t.Fatalf("StartStreaming = %v", err)
	}

	// Exactly what streamFileFromBlocks does before entering StreamBlocks: the
	// file's representation headers, including a Content-Length for the whole
	// file, are staged before the first storage read can fail.
	c.Header("Content-Disposition", `attachment; filename="large.bin"`)
	c.Header("Content-Type", "application/octet-stream")
	c.Header("Content-Length", "734003200")
	c.Status(http.StatusOK)

	reader := &stalledPrefetchReader{
		started:              make(chan struct{}),
		cancellationObserved: make(chan struct{}),
		allowReturn:          make(chan struct{}),
	}

	beforeIdle := testutil.ToFloat64(metrics.DownloadAdmissionReleasedTotal.WithLabelValues(string(downloadadmission.ReleaseIdleWriteTimeout)))
	beforeCompleted := testutil.ToFloat64(metrics.DownloadAdmissionReleasedTotal.WithLabelValues(string(downloadadmission.ReleaseCompleted)))

	streamReturned := make(chan error, 1)
	go func() {
		streamReturned <- streaming.StreamBlocks(c, streamCtx, reader, []string{"block-0"}, nil, nil, "stalled-prefetch-test")
	}()

	// Prove the prefetch actually started before asserting its cancellation.
	select {
	case <-reader.started:
	case <-time.After(5 * time.Second):
		t.Fatal("StreamBlocks never reached the first prefetch")
	}
	select {
	case <-reader.cancellationObserved:
	case <-time.After(5 * time.Second):
		t.Fatal("the stalled first prefetch was never cancelled; c.Status(200) or the missing interval left it unbounded")
	}

	if got := c.Writer.Size(); got > 0 {
		t.Fatalf("wrote %d bytes before the prefetch blocked; this case must exercise the pre-first-write window", got)
	}
	// Cancelled but still running: the capacity is still in use.
	if got := testutil.ToFloat64(metrics.DownloadAdmissionActiveCurrent); got != 1 {
		t.Fatalf("active admissions while the cancelled prefetch is still running = %v, want 1", got)
	}
	if got := testutil.ToFloat64(metrics.DownloadAdmissionReleasedTotal.WithLabelValues(string(downloadadmission.ReleaseIdleWriteTimeout))); got != beforeIdle {
		t.Fatalf("idle_write_timeout releases = %v, want unchanged %v until the producer finishes", got, beforeIdle)
	}

	close(reader.allowReturn)
	select {
	case streamErr := <-streamReturned:
		if streamErr == nil {
			t.Fatal("StreamBlocks returned nil after its first prefetch was cancelled")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("StreamBlocks did not return after the cancelled prefetch unwound")
	}

	// The producer's deferred cleanup is what physically frees the slot.
	if err := lifecycle.Finish(downloadadmission.ReleaseCompleted); err != nil && err != httputil.ErrIdleWriteTimeout {
		t.Fatalf("finish: %v", err)
	}

	if got := testutil.ToFloat64(metrics.DownloadAdmissionActiveCurrent); got != 0 {
		t.Fatalf("active admissions after unwind = %v, want 0", got)
	}
	if got := testutil.ToFloat64(metrics.DownloadAdmissionReleasedTotal.WithLabelValues(string(downloadadmission.ReleaseIdleWriteTimeout))); got != beforeIdle+1 {
		t.Fatalf("idle_write_timeout releases = %v, want exactly one more than %v", got, beforeIdle)
	}
	// Finish was asked for `completed`, but the timeout claimed first and wins.
	if got := testutil.ToFloat64(metrics.DownloadAdmissionReleasedTotal.WithLabelValues(string(downloadadmission.ReleaseCompleted))); got != beforeCompleted {
		t.Fatalf("completed releases = %v, want unchanged %v; the first cause must win", got, beforeCompleted)
	}

	// D4 records the 200 before entering StreamBlocks, so without a pre-header
	// failure path a timed-out file download would reach the client as a
	// successful empty file rather than a retryable error.
	if baseRecorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; a timed-out download must not look like an empty success", baseRecorder.Code)
	}
	if got := baseRecorder.Header().Get("Retry-After"); got == "" {
		t.Fatal("timed-out download has no Retry-After")
	}
	if baseRecorder.Body.Len() != 0 {
		t.Fatalf("body = %q, want empty", baseRecorder.Body.String())
	}
	// The 503 must not keep promising the file. A declared length that never
	// arrives makes net/http drop the connection, so the client would read an
	// unexpected EOF rather than the retry contract.
	if got := baseRecorder.Header().Get("Content-Length"); got != "0" {
		t.Fatalf("Content-Length = %q, want 0; the 503 inherited the file's length", got)
	}
	if got := baseRecorder.Header().Get("Content-Disposition"); got != "" {
		t.Fatalf("Content-Disposition = %q; a browser would save the error as the file", got)
	}
}
