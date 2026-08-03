package httputil

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/downloadadmission"
)

// TestDownloadAdmissionFinishAnswers503WhenNothingWasWritten covers the state a
// failed writer creates: it rejects every later write, including the producer's
// own pre-header error, and Gin would then commit its default 200 with an empty
// body. On block GET that is indistinguishable from an empty block that
// transferred successfully, so a retryable timeout would read as valid data.
func TestDownloadAdmissionFinishAnswers503WhenNothingWasWritten(t *testing.T) {
	cfg := admissionLifecycleConfig()
	cfg.IdleWriteTimeout = 25 * time.Millisecond
	cfg.RetryAfter = 3 * time.Second
	coordinator := newAdmissionLifecycleCoordinator(t, cfg)

	base := newIdleWriteTestWriter()
	c, _ := newAdmissionLifecycleContext(context.Background(), base)
	lifecycle, reason, err := AcquireDownloadAdmission(c, coordinator, cfg, admissionLifecycleRequest(t, "unstarted"))
	if err != nil || reason != "" {
		t.Fatalf("AcquireDownloadAdmission = (%q, %v)", reason, err)
	}
	streaming, err := lifecycle.StartStreaming()
	if err != nil {
		t.Fatal(err)
	}

	// Everything a producer stages for the file before its first storage read.
	// D4 does exactly this ahead of the block-0 prefetch.
	base.Header().Set("Content-Length", "734003200")
	base.Header().Set("Content-Range", "bytes 0-1023/734003200")
	base.Header().Set("Content-Disposition", `attachment; filename="large.bin"`)
	base.Header().Set("Content-Type", "application/octet-stream")
	base.Header().Set("Content-Encoding", "gzip")
	base.Header().Set("Accept-Ranges", "bytes")
	base.Header().Set("ETag", `"file-version"`)
	base.Header().Set("Last-Modified", "Mon, 03 Aug 2026 00:00:00 GMT")
	base.Header().Set("Expires", "Tue, 04 Aug 2026 00:00:00 GMT")
	base.Header().Set("Cache-Control", "public, max-age=3600")
	// Not a representation header: this must survive.
	base.Header().Set("X-Quota-Warning", "soft")

	// No output at all: this is the stalled first storage read.
	select {
	case <-streaming.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("idle interval did not cancel without output")
	}

	// The producer tries its own error response and is refused, exactly as a real
	// handler's c.JSON would be.
	if _, err := c.Writer.Write([]byte(`{"error":"failed"}`)); err == nil {
		t.Fatal("failed writer accepted a late error body")
	}

	_ = lifecycle.Finish(downloadadmission.ReleaseCompleted)

	if base.status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", base.status)
	}
	if got := base.Header().Get("Retry-After"); got != "3" {
		t.Fatalf("Retry-After = %q, want 3", got)
	}
	if base.body.Len() != 0 {
		t.Fatalf("body = %q, want empty", base.body.String())
	}

	// Swapping only the status would emit a 503 that still promises the whole
	// file. net/http closes the connection when a declared length never arrives,
	// so the client reads an unexpected EOF instead of the Retry-After contract
	// this response exists to deliver.
	if got := base.Header().Get("Content-Length"); got != "0" {
		t.Fatalf("Content-Length = %q, want 0; the error must not inherit the file's length", got)
	}
	for _, name := range []string{
		"Content-Range",
		"Content-Disposition",
		"Content-Type",
		"Content-Encoding",
		"Accept-Ranges",
		"ETag",
		"Last-Modified",
		"Expires",
	} {
		if got := base.Header().Get(name); got != "" {
			t.Fatalf("%s = %q on the error response; it describes a file that was never sent", name, got)
		}
	}
	// The file's caching policy must not be inherited, or the failure could be
	// stored and replayed as the resource.
	if got := base.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	// Headers that are not representation metadata belong to the stack, not the
	// entity, and a blanket reset would silently drop them.
	if got := base.Header().Get("X-Quota-Warning"); got != "soft" {
		t.Fatalf("X-Quota-Warning = %q, want it preserved", got)
	}
}

// TestDownloadAdmissionFinishLeavesCommittedResponseAlone is the other half: a
// failure after output has begun must stop the stream, never append a second
// status to a response the client is already reading.
func TestDownloadAdmissionFinishLeavesCommittedResponseAlone(t *testing.T) {
	cfg := admissionLifecycleConfig()
	cfg.IdleWriteTimeout = 25 * time.Millisecond
	coordinator := newAdmissionLifecycleCoordinator(t, cfg)

	base := newIdleWriteTestWriter()
	c, _ := newAdmissionLifecycleContext(context.Background(), base)
	lifecycle, reason, err := AcquireDownloadAdmission(c, coordinator, cfg, admissionLifecycleRequest(t, "committed"))
	if err != nil || reason != "" {
		t.Fatalf("AcquireDownloadAdmission = (%q, %v)", reason, err)
	}
	streaming, err := lifecycle.StartStreaming()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Writer.Write([]byte("partial")); err != nil {
		t.Fatal(err)
	}

	select {
	case <-streaming.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("idle write timeout did not cancel streaming")
	}
	_ = lifecycle.Finish(downloadadmission.ReleaseCompleted)

	if base.status == http.StatusServiceUnavailable {
		t.Fatal("rewrote a committed response as 503")
	}
	if got := base.body.String(); got != "partial" {
		t.Fatalf("body = %q, want the delivered bytes untouched", got)
	}
}

// panicOnCommitWriter defers its header like Gin and then panics when the status
// is finally committed.
type panicOnCommitWriter struct {
	*idleWriteTestWriter
}

func (w *panicOnCommitWriter) WriteHeader(status int) {
	w.status = status
}

func (w *panicOnCommitWriter) WriteHeaderNow() {
	panic("response writer exploded while committing the status")
}

// TestDownloadAdmissionFinishReleasesLeaseWhenCommitPanics keeps the "the lease
// is always released" property whole. Emitting the failure response is response
// cleanup that runs before the release, so a plain statement afterwards would be
// skipped by a panic and strand the slot for the life of the process — the same
// reason the ZIP producer registers its release before zipWriter.Close.
func TestDownloadAdmissionFinishReleasesLeaseWhenCommitPanics(t *testing.T) {
	cfg := admissionLifecycleConfig()
	cfg.IdleWriteTimeout = 25 * time.Millisecond
	coordinator := newAdmissionLifecycleCoordinator(t, cfg)

	base := &panicOnCommitWriter{idleWriteTestWriter: newIdleWriteTestWriter()}
	c, _ := newAdmissionLifecycleContext(context.Background(), base)
	lifecycle, reason, err := AcquireDownloadAdmission(c, coordinator, cfg, admissionLifecycleRequest(t, "commit-panic"))
	if err != nil || reason != "" {
		t.Fatalf("AcquireDownloadAdmission = (%q, %v)", reason, err)
	}
	streaming, err := lifecycle.StartStreaming()
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-streaming.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("idle interval did not cancel without output")
	}

	beforeIdle := releaseCount(downloadadmission.ReleaseIdleWriteTimeout)

	func() {
		defer func() {
			if recover() == nil {
				t.Error("expected the writer panic to propagate")
			}
		}()
		_ = lifecycle.Finish(downloadadmission.ReleaseCompleted)
	}()

	waitForMetric(t, func() float64 { return releaseCount(downloadadmission.ReleaseIdleWriteTimeout) }, beforeIdle+1)

	// The slot must be usable again even though committing the error blew up.
	next, reason := coordinator.Acquire(context.Background(), admissionLifecycleRequest(t, "after-panic"))
	if next == nil {
		t.Fatalf("acquire after a panicking commit = %q; the slot was stranded", reason)
	}
	next.Release(downloadadmission.ReleaseCompleted)
}

// TestDownloadAdmissionCompletedLosesToAFailedWriter pins the classification
// race. The writer commits to failed under its own mutex and only afterwards
// calls back to claim the cause here, so a handler finishing inside that window
// would otherwise record a killed transfer as `completed`.
func TestDownloadAdmissionCompletedLosesToAFailedWriter(t *testing.T) {
	cfg := admissionLifecycleConfig()
	coordinator := newAdmissionLifecycleCoordinator(t, cfg)

	c, _ := newAdmissionLifecycleContext(context.Background(), newIdleWriteTestWriter())
	lifecycle, reason, err := AcquireDownloadAdmission(c, coordinator, cfg, admissionLifecycleRequest(t, "race"))
	if err != nil || reason != "" {
		t.Fatalf("AcquireDownloadAdmission = (%q, %v)", reason, err)
	}
	if _, err := lifecycle.StartStreaming(); err != nil {
		t.Fatal(err)
	}

	beforeCompleted := releaseCount(downloadadmission.ReleaseCompleted)
	beforeIdle := releaseCount(downloadadmission.ReleaseIdleWriteTimeout)

	// Reproduce the window exactly: expire() commits the writer to failed under
	// the writer's own mutex, releases it, and only then calls back to claim the
	// cause. Dropping the returned callbacks leaves the lifecycle in the state it
	// occupies during that handoff, without depending on timing.
	lifecycle.mu.Lock()
	writer := lifecycle.writer
	lifecycle.mu.Unlock()
	writer.mu.Lock()
	_, _, _ = writer.failLocked(ErrIdleWriteTimeout)
	writer.mu.Unlock()

	_ = lifecycle.Finish(downloadadmission.ReleaseCompleted)

	if got := releaseCount(downloadadmission.ReleaseCompleted); got != beforeCompleted {
		t.Fatalf("completed releases = %v, want unchanged %v; a killed transfer is not completed", got, beforeCompleted)
	}
	if got := releaseCount(downloadadmission.ReleaseIdleWriteTimeout); got != beforeIdle+1 {
		t.Fatalf("idle_write_timeout releases = %v, want %v", got, beforeIdle+1)
	}
}
