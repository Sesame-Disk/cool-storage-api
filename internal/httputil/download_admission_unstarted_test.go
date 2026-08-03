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
