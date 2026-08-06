package httputil

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/downloadadmission"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func admissionLifecycleConfig() config.DownloadAdmissionConfig {
	return config.DownloadAdmissionConfig{
		Enabled:                true,
		MaxActivePerNode:       1,
		MaxActivePerAuthUser:   1,
		MaxActivePerLinkSource: 1,
		MaxActivePerClientLink: 1,
		PreparationDeadline:    time.Second,
		IdleWriteTimeout:       time.Second,
		RetryAfter:             1500 * time.Millisecond,
	}
}

func newAdmissionLifecycleCoordinator(t *testing.T, cfg config.DownloadAdmissionConfig) *downloadadmission.Coordinator {
	t.Helper()
	coordinator, err := downloadadmission.New(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	return coordinator
}

func admissionLifecycleRequest(t *testing.T, user string) downloadadmission.AdmissionRequest {
	t.Helper()
	request, err := downloadadmission.NewAuthenticatedRequest(downloadadmission.ProfileFile, "org-1", user)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func newAdmissionLifecycleContext(parent context.Context, writer gin.ResponseWriter) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/download", nil).WithContext(parent)
	if writer != nil {
		c.Writer = writer
	}
	return c, recorder
}

func releaseCount(cause downloadadmission.ReleaseCause) float64 {
	return testutil.ToFloat64(metrics.DownloadAdmissionReleasedTotal.WithLabelValues(string(cause)))
}

func deadlineCount(phase downloadadmission.DeadlinePhase) float64 {
	return testutil.ToFloat64(metrics.DownloadAdmissionDeadlineExpiredTotal.WithLabelValues(string(phase)))
}

func waitForMetric(t *testing.T, metric func() float64, want float64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if metric() == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("metric = %v, want %v", metric(), want)
}

type recoveryDeadlineResponseWriter struct {
	gin.ResponseWriter
}

func (w *recoveryDeadlineResponseWriter) SetWriteDeadline(time.Time) error { return nil }

func TestDownloadAdmissionDisabledIsTransparent(t *testing.T) {
	parent := context.Background()
	writer := newIdleWriteTestWriter()
	c, _ := newAdmissionLifecycleContext(parent, writer)
	requestBefore := c.Request
	writerBefore := c.Writer

	lifecycle, reason, err := AcquireDownloadAdmission(c, nil, config.DownloadAdmissionConfig{}, downloadadmission.AdmissionRequest{})
	if err != nil || reason != "" || lifecycle == nil {
		t.Fatalf("AcquireDownloadAdmission = (%v, %q, %v), want lifecycle", lifecycle, reason, err)
	}
	if c.Request != requestBefore || c.Writer != writerBefore {
		t.Fatal("disabled admission changed the request or response writer")
	}
	if lifecycle.PreparationContext() != parent {
		t.Fatal("disabled preparation context is not the original request context")
	}
	streaming, err := lifecycle.StartStreaming()
	if err != nil || streaming != parent {
		t.Fatalf("StartStreaming = (%v, %v), want original context", streaming, err)
	}
	if c.Request != requestBefore || c.Writer != writerBefore {
		t.Fatal("disabled streaming transition changed the request or response writer")
	}
	if err := lifecycle.Finish(downloadadmission.ReleaseCompleted); err != nil {
		t.Fatalf("Finish = %v", err)
	}
}

func TestDownloadAdmissionRefusalRendersRetryAfter(t *testing.T) {
	cfg := admissionLifecycleConfig()
	coordinator := newAdmissionLifecycleCoordinator(t, cfg)
	holder, reason := coordinator.Acquire(context.Background(), admissionLifecycleRequest(t, "holder"))
	if holder == nil || reason != "" {
		t.Fatalf("holder acquire = (%v, %q)", holder, reason)
	}
	defer holder.Release(downloadadmission.ReleaseCompleted)

	c, recorder := newAdmissionLifecycleContext(context.Background(), nil)
	lifecycle, reason, err := AcquireDownloadAdmission(c, coordinator, cfg, admissionLifecycleRequest(t, "waiter"))
	if err != nil || lifecycle != nil {
		t.Fatalf("AcquireDownloadAdmission = (%v, %q, %v), want refusal", lifecycle, reason, err)
	}
	if reason != downloadadmission.RejectNodeFull {
		t.Fatalf("refusal reason = %q, want %q", reason, downloadadmission.RejectNodeFull)
	}

	RenderDownloadAdmissionRefusal(c, coordinator)
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
	if got := recorder.Header().Get("Retry-After"); got != "2" {
		t.Fatalf("Retry-After = %q, want 2", got)
	}
}

func TestDownloadAdmissionPreparationTimeoutAndCancellation(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		cfg := admissionLifecycleConfig()
		cfg.PreparationDeadline = 25 * time.Millisecond
		coordinator := newAdmissionLifecycleCoordinator(t, cfg)
		beforeDeadline := deadlineCount(downloadadmission.DeadlinePreparation)
		beforeRelease := releaseCount(downloadadmission.ReleasePreparationTimeout)
		c, _ := newAdmissionLifecycleContext(context.Background(), newIdleWriteTestWriter())
		lifecycle, reason, err := AcquireDownloadAdmission(c, coordinator, cfg, admissionLifecycleRequest(t, "timeout"))
		if err != nil || reason != "" {
			t.Fatalf("AcquireDownloadAdmission = (%q, %v)", reason, err)
		}

		select {
		case <-lifecycle.PreparationContext().Done():
		case <-time.After(time.Second):
			t.Fatal("preparation context did not expire")
		}
		if !errors.Is(lifecycle.PreparationContext().Err(), context.DeadlineExceeded) {
			t.Fatalf("preparation context error = %v, want deadline exceeded", lifecycle.PreparationContext().Err())
		}
		if err := lifecycle.Finish(downloadadmission.ReleaseCompleted); err != nil {
			t.Fatalf("Finish = %v", err)
		}
		waitForMetric(t, func() float64 { return deadlineCount(downloadadmission.DeadlinePreparation) }, beforeDeadline+1)
		waitForMetric(t, func() float64 { return releaseCount(downloadadmission.ReleasePreparationTimeout) }, beforeRelease+1)
	})

	t.Run("request cancellation", func(t *testing.T) {
		cfg := admissionLifecycleConfig()
		coordinator := newAdmissionLifecycleCoordinator(t, cfg)
		beforeDeadline := deadlineCount(downloadadmission.DeadlinePreparation)
		beforeRelease := releaseCount(downloadadmission.ReleaseClientDisconnect)
		parent, cancel := context.WithCancel(context.Background())
		defer cancel()
		c, _ := newAdmissionLifecycleContext(parent, newIdleWriteTestWriter())
		lifecycle, reason, err := AcquireDownloadAdmission(c, coordinator, cfg, admissionLifecycleRequest(t, "cancel"))
		if err != nil || reason != "" {
			t.Fatalf("AcquireDownloadAdmission = (%q, %v)", reason, err)
		}

		cancel()
		select {
		case <-lifecycle.PreparationContext().Done():
		case <-time.After(time.Second):
			t.Fatal("preparation context did not inherit request cancellation")
		}
		if err := lifecycle.Finish(downloadadmission.ReleaseCompleted); err != nil {
			t.Fatalf("Finish = %v", err)
		}
		waitForMetric(t, func() float64 { return releaseCount(downloadadmission.ReleaseClientDisconnect) }, beforeRelease+1)
		if got := deadlineCount(downloadadmission.DeadlinePreparation); got != beforeDeadline {
			t.Fatalf("request cancellation changed preparation deadline metric from %v to %v", beforeDeadline, got)
		}
	})
}

func TestDownloadAdmissionStartStreamingRejectsExpiredPreparation(t *testing.T) {
	cfg := admissionLifecycleConfig()
	coordinator := newAdmissionLifecycleCoordinator(t, cfg)
	c, _ := newAdmissionLifecycleContext(context.Background(), newIdleWriteTestWriter())
	lifecycle, reason, err := AcquireDownloadAdmission(c, coordinator, cfg, admissionLifecycleRequest(t, "expired-before-stream"))
	if err != nil || reason != "" {
		t.Fatalf("AcquireDownloadAdmission = (%q, %v)", reason, err)
	}

	// Stop the real callback and substitute an already-expired preparation context
	// so StartStreaming races neither the timer nor a goroutine scheduler.
	lifecycle.mu.Lock()
	stopPreparation := lifecycle.stopPreparation
	lifecycle.stopPreparation = nil
	lifecycle.mu.Unlock()
	if stopPreparation == nil || !stopPreparation() {
		t.Fatal("failed to stop preparation callback")
	}
	lifecycle.mu.Lock()
	preparation, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Millisecond))
	lifecycle.preparation = preparation
	lifecycle.prepareCancel = cancel
	lifecycle.mu.Unlock()
	defer cancel()

	<-preparation.Done()
	writerBefore := c.Writer
	beforeDeadline := deadlineCount(downloadadmission.DeadlinePreparation)
	beforeRelease := releaseCount(downloadadmission.ReleasePreparationTimeout)
	if _, err := lifecycle.StartStreaming(); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("StartStreaming error = %v, want context deadline exceeded", err)
	}
	if c.Writer != writerBefore {
		t.Fatal("expired preparation installed a streaming writer")
	}
	if err := lifecycle.Finish(downloadadmission.ReleaseCompleted); err != nil {
		t.Fatalf("Finish = %v", err)
	}
	waitForMetric(t, func() float64 { return deadlineCount(downloadadmission.DeadlinePreparation) }, beforeDeadline+1)
	waitForMetric(t, func() float64 { return releaseCount(downloadadmission.ReleasePreparationTimeout) }, beforeRelease+1)
}

func TestDownloadAdmissionStartStreamingRejectsCancellationDuringWriterSetup(t *testing.T) {
	cfg := admissionLifecycleConfig()
	coordinator := newAdmissionLifecycleCoordinator(t, cfg)
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	writer := newIdleWriteTestWriter()
	setupStarted := make(chan struct{})
	unblockSetup := make(chan struct{})
	writer.deadlineHook = func(deadline time.Time) {
		if deadline.IsZero() {
			return
		}
		close(setupStarted)
		<-unblockSetup
	}
	c, _ := newAdmissionLifecycleContext(parent, writer)
	lifecycle, reason, err := AcquireDownloadAdmission(c, coordinator, cfg, admissionLifecycleRequest(t, "setup-cancel"))
	if err != nil || reason != "" {
		t.Fatalf("AcquireDownloadAdmission = (%q, %v)", reason, err)
	}

	lifecycle.mu.Lock()
	stopParent := lifecycle.stopParent
	lifecycle.stopParent = nil
	lifecycle.mu.Unlock()
	if stopParent == nil || !stopParent() {
		t.Fatal("failed to stop parent cancellation callback")
	}
	writerBefore := c.Writer
	result := make(chan error, 1)
	go func() {
		_, err := lifecycle.StartStreaming()
		result <- err
	}()
	select {
	case <-setupStarted:
	case <-time.After(time.Second):
		t.Fatal("writer setup did not begin")
	}
	cancel()
	close(unblockSetup)
	if err := <-result; err == nil || (!errors.Is(err, context.Canceled) && !errors.Is(err, ErrDownloadAdmissionReleased)) {
		t.Fatalf("StartStreaming error = %v, want cancellation or released admission", err)
	}
	if c.Writer != writerBefore {
		t.Fatal("cancelled writer setup installed a streaming writer")
	}
	if err := lifecycle.Finish(downloadadmission.ReleaseCompleted); err != nil {
		t.Fatalf("Finish = %v", err)
	}
}

func TestDownloadAdmissionFailStreamErrorClassifiesDisconnect(t *testing.T) {
	cfg := admissionLifecycleConfig()
	coordinator := newAdmissionLifecycleCoordinator(t, cfg)
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, _ := newAdmissionLifecycleContext(parent, newIdleWriteTestWriter())
	lifecycle, reason, err := AcquireDownloadAdmission(c, coordinator, cfg, admissionLifecycleRequest(t, "stream-disconnect"))
	if err != nil || reason != "" {
		t.Fatalf("AcquireDownloadAdmission = (%q, %v)", reason, err)
	}

	lifecycle.mu.Lock()
	stopParent := lifecycle.stopParent
	lifecycle.stopParent = nil
	lifecycle.mu.Unlock()
	if stopParent == nil || !stopParent() {
		t.Fatal("failed to stop parent cancellation callback")
	}
	beforeClient := releaseCount(downloadadmission.ReleaseClientDisconnect)
	beforeStorage := releaseCount(downloadadmission.ReleaseStorageError)
	cancel()
	lifecycle.FailStreamError(downloadadmission.ReleaseStorageError)
	if err := lifecycle.Finish(downloadadmission.ReleaseCompleted); err != nil {
		t.Fatalf("Finish = %v", err)
	}
	waitForMetric(t, func() float64 { return releaseCount(downloadadmission.ReleaseClientDisconnect) }, beforeClient+1)
	if got := releaseCount(downloadadmission.ReleaseStorageError); got != beforeStorage {
		t.Fatalf("storage-error release count = %v, want %v", got, beforeStorage)
	}
}

func TestDownloadAdmissionRestoresWriterForGinRecovery(t *testing.T) {
	cfg := admissionLifecycleConfig()
	coordinator := newAdmissionLifecycleCoordinator(t, cfg)
	router := gin.New()
	router.Use(gin.Recovery())
	router.GET("/panic", func(c *gin.Context) {
		c.Writer = &recoveryDeadlineResponseWriter{ResponseWriter: c.Writer}
		lifecycle, reason, err := AcquireDownloadAdmission(c, coordinator, cfg, admissionLifecycleRequest(t, "recovery"))
		if err != nil || reason != "" {
			t.Fatalf("AcquireDownloadAdmission = (%q, %v)", reason, err)
		}
		defer lifecycle.FinishHandler()
		if _, err := lifecycle.StartStreaming(); err != nil {
			t.Fatalf("StartStreaming = %v", err)
		}
		panic("recovery panic")
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/panic", nil))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("recovered status = %d, want %d; body=%q", recorder.Code, http.StatusInternalServerError, recorder.Body.String())
	}
}

func TestDownloadAdmissionWriterUnreachableFailsClosed(t *testing.T) {
	cfg := admissionLifecycleConfig()
	coordinator := newAdmissionLifecycleCoordinator(t, cfg)
	beforeWriter := testutil.ToFloat64(metrics.DownloadAdmissionWriterUnreachableTotal)
	beforeRelease := releaseCount(downloadadmission.ReleaseResponseError)
	c, _ := newAdmissionLifecycleContext(context.Background(), nil)
	writerBefore := c.Writer
	lifecycle, reason, err := AcquireDownloadAdmission(c, coordinator, cfg, admissionLifecycleRequest(t, "unreachable"))
	if err != nil || reason != "" {
		t.Fatalf("AcquireDownloadAdmission = (%q, %v)", reason, err)
	}

	_, err = lifecycle.StartStreaming()
	if !errors.Is(err, ErrIdleWriteWriterUnreachable) {
		t.Fatalf("StartStreaming error = %v, want unreachable writer", err)
	}
	if c.Writer != writerBefore {
		t.Fatal("unreachable writer was installed")
	}
	if err := lifecycle.Finish(downloadadmission.ReleaseCompleted); err != nil {
		t.Fatalf("Finish = %v", err)
	}
	select {
	case <-lifecycle.PreparationContext().Done():
	case <-time.After(time.Second):
		t.Fatal("writer failure did not cancel preparation")
	}
	waitForMetric(t, func() float64 { return testutil.ToFloat64(metrics.DownloadAdmissionWriterUnreachableTotal) }, beforeWriter+1)
	waitForMetric(t, func() float64 { return releaseCount(downloadadmission.ReleaseResponseError) }, beforeRelease+1)
}

func TestDownloadAdmissionWriteErrorReleasesLease(t *testing.T) {
	cfg := admissionLifecycleConfig()
	coordinator := newAdmissionLifecycleCoordinator(t, cfg)
	beforeRelease := releaseCount(downloadadmission.ReleaseResponseError)
	writeErr := errors.New("response write failed")
	underlying := newIdleWriteTestWriter()
	underlying.writeErr = writeErr
	c, _ := newAdmissionLifecycleContext(context.Background(), underlying)
	lifecycle, reason, err := AcquireDownloadAdmission(c, coordinator, cfg, admissionLifecycleRequest(t, "write-error"))
	if err != nil || reason != "" {
		t.Fatalf("AcquireDownloadAdmission = (%q, %v)", reason, err)
	}
	streaming, err := lifecycle.StartStreaming()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Writer.Write([]byte("response")); !errors.Is(err, writeErr) {
		t.Fatalf("Write = %v, want response write error", err)
	}
	select {
	case <-streaming.Done():
	case <-time.After(time.Second):
		t.Fatal("write error did not cancel streaming context")
	}
	finishErr := lifecycle.Finish(downloadadmission.ReleaseCompleted)
	if !errors.Is(finishErr, writeErr) {
		t.Fatalf("Finish = %v, want response write error", finishErr)
	}
	waitForMetric(t, func() float64 { return releaseCount(downloadadmission.ReleaseResponseError) }, beforeRelease+1)
}

func TestDownloadAdmissionIdleWriteTimeoutReleasesLease(t *testing.T) {
	cfg := admissionLifecycleConfig()
	cfg.IdleWriteTimeout = 25 * time.Millisecond
	coordinator := newAdmissionLifecycleCoordinator(t, cfg)
	beforeDeadline := deadlineCount(downloadadmission.DeadlineIdleWrite)
	beforeRelease := releaseCount(downloadadmission.ReleaseIdleWriteTimeout)
	c, _ := newAdmissionLifecycleContext(context.Background(), newIdleWriteTestWriter())
	lifecycle, reason, err := AcquireDownloadAdmission(c, coordinator, cfg, admissionLifecycleRequest(t, "idle"))
	if err != nil || reason != "" {
		t.Fatalf("AcquireDownloadAdmission = (%q, %v)", reason, err)
	}
	streaming, err := lifecycle.StartStreaming()
	if err != nil {
		t.Fatal(err)
	}
	if _, hasDeadline := streaming.Deadline(); hasDeadline {
		t.Fatal("streaming context retained the preparation deadline")
	}
	if _, ok := c.Writer.(*IdleWriteWriter); !ok {
		t.Fatal("streaming did not install IdleWriteWriter")
	}
	if _, err := c.Writer.Write([]byte("progress")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-streaming.Done():
	case <-time.After(time.Second):
		t.Fatal("idle write timeout did not cancel streaming context")
	}
	finishErr := lifecycle.Finish(downloadadmission.ReleaseCompleted)
	if !errors.Is(finishErr, ErrIdleWriteTimeout) {
		t.Fatalf("Finish = %v, want idle write timeout", finishErr)
	}
	waitForMetric(t, func() float64 { return deadlineCount(downloadadmission.DeadlineIdleWrite) }, beforeDeadline+1)
	waitForMetric(t, func() float64 { return releaseCount(downloadadmission.ReleaseIdleWriteTimeout) }, beforeRelease+1)
}

func TestDownloadAdmissionIdleTimeoutClaimsCauseBeforeCancellation(t *testing.T) {
	cfg := admissionLifecycleConfig()
	cfg.IdleWriteTimeout = 20 * time.Millisecond
	coordinator := newAdmissionLifecycleCoordinator(t, cfg)
	c, _ := newAdmissionLifecycleContext(context.Background(), newIdleWriteTestWriter())
	lifecycle, reason, err := AcquireDownloadAdmission(c, coordinator, cfg, admissionLifecycleRequest(t, "idle-cause"))
	if err != nil || reason != "" {
		t.Fatalf("AcquireDownloadAdmission = (%q, %v)", reason, err)
	}
	streaming, err := lifecycle.StartStreaming()
	if err != nil {
		t.Fatal(err)
	}
	beforeIdle := releaseCount(downloadadmission.ReleaseIdleWriteTimeout)
	beforeDeadline := deadlineCount(downloadadmission.DeadlineIdleWrite)
	beforeStorage := releaseCount(downloadadmission.ReleaseStorageError)
	workerDone := make(chan struct{})
	go func() {
		<-streaming.Done()
		lifecycle.Fail(downloadadmission.ReleaseStorageError)
		close(workerDone)
	}()
	if _, err := c.Writer.Write([]byte("progress")); err != nil {
		t.Fatal(err)
	}
	waitForMetric(t, func() float64 { return deadlineCount(downloadadmission.DeadlineIdleWrite) }, beforeDeadline+1)
	select {
	case <-workerDone:
	case <-time.After(time.Second):
		t.Fatal("worker did not observe idle-timeout cancellation")
	}
	if got := releaseCount(downloadadmission.ReleaseStorageError); got != beforeStorage {
		t.Fatalf("storage-error release count = %v, want %v; cancellation worker won the race", got, beforeStorage)
	}
	if got := releaseCount(downloadadmission.ReleaseIdleWriteTimeout); got != beforeIdle {
		t.Fatalf("idle-timeout release count = %v before Finish, want %v", got, beforeIdle)
	}
	if err := lifecycle.Finish(downloadadmission.ReleaseCompleted); !errors.Is(err, ErrIdleWriteTimeout) {
		t.Fatalf("Finish = %v, want idle write timeout", err)
	}
	waitForMetric(t, func() float64 { return releaseCount(downloadadmission.ReleaseIdleWriteTimeout) }, beforeIdle+1)
}

func TestDownloadAdmissionRequestCancellationPreservesIdleWriteTimeout(t *testing.T) {
	cfg := admissionLifecycleConfig()
	cfg.IdleWriteTimeout = time.Minute
	coordinator := newAdmissionLifecycleCoordinator(t, cfg)
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, _ := newAdmissionLifecycleContext(parent, newIdleWriteTestWriter())
	lifecycle, reason, err := AcquireDownloadAdmission(c, coordinator, cfg, admissionLifecycleRequest(t, "cancel-after-timeout"))
	if err != nil || reason != "" {
		t.Fatalf("AcquireDownloadAdmission = (%q, %v)", reason, err)
	}
	if _, err := lifecycle.StartStreaming(); err != nil {
		t.Fatalf("StartStreaming = %v", err)
	}

	// Reproduce the ordering at the net/http boundary deterministically: the
	// writer has already recorded its server-owned timeout, but the request
	// cancellation callback has not claimed the lifecycle yet.
	lifecycle.mu.Lock()
	writer := lifecycle.writer
	lifecycle.mu.Unlock()
	writer.mu.Lock()
	_, _, _ = writer.failLocked(ErrIdleWriteTimeout)
	writer.mu.Unlock()

	beforeIdle := deadlineCount(downloadadmission.DeadlineIdleWrite)
	beforeTimeout := releaseCount(downloadadmission.ReleaseIdleWriteTimeout)
	beforeClient := releaseCount(downloadadmission.ReleaseClientDisconnect)
	cancel()
	lifecycle.failRequestCancellation()

	if err := lifecycle.Finish(downloadadmission.ReleaseCompleted); !errors.Is(err, ErrIdleWriteTimeout) {
		t.Fatalf("Finish = %v, want idle write timeout", err)
	}
	waitForMetric(t, func() float64 { return deadlineCount(downloadadmission.DeadlineIdleWrite) }, beforeIdle+1)
	waitForMetric(t, func() float64 { return releaseCount(downloadadmission.ReleaseIdleWriteTimeout) }, beforeTimeout+1)
	if got := releaseCount(downloadadmission.ReleaseClientDisconnect); got != beforeClient {
		t.Fatalf("client-disconnect releases = %v, want unchanged %v", got, beforeClient)
	}
}

func TestDownloadAdmissionLateIdleTimeoutCallbackStillRecordsDeadline(t *testing.T) {
	cfg := admissionLifecycleConfig()
	coordinator := newAdmissionLifecycleCoordinator(t, cfg)
	c, _ := newAdmissionLifecycleContext(context.Background(), newIdleWriteTestWriter())
	lifecycle, reason, err := AcquireDownloadAdmission(c, coordinator, cfg, admissionLifecycleRequest(t, "late-idle-callback"))
	if err != nil || reason != "" {
		t.Fatalf("AcquireDownloadAdmission = (%q, %v)", reason, err)
	}
	if _, err := lifecycle.StartStreaming(); err != nil {
		t.Fatalf("StartStreaming = %v", err)
	}

	lifecycle.mu.Lock()
	writer := lifecycle.writer
	lifecycle.mu.Unlock()
	writer.mu.Lock()
	_, _, _ = writer.failLocked(ErrIdleWriteTimeout)
	writer.mu.Unlock()

	beforeDeadline := deadlineCount(downloadadmission.DeadlineIdleWrite)
	beforeIdle := releaseCount(downloadadmission.ReleaseIdleWriteTimeout)
	beforeClient := releaseCount(downloadadmission.ReleaseClientDisconnect)
	if err := lifecycle.Finish(downloadadmission.ReleaseCompleted); !errors.Is(err, ErrIdleWriteTimeout) {
		t.Fatalf("Finish = %v, want idle write timeout", err)
	}

	// The writer timeout callback can arrive after Finish released the lease.
	lifecycle.failIdleWriteTimeout()
	waitForMetric(t, func() float64 { return deadlineCount(downloadadmission.DeadlineIdleWrite) }, beforeDeadline+1)
	if got := releaseCount(downloadadmission.ReleaseIdleWriteTimeout); got != beforeIdle+1 {
		t.Fatalf("idle-timeout releases = %v, want %v", got, beforeIdle+1)
	}
	if got := releaseCount(downloadadmission.ReleaseClientDisconnect); got != beforeClient {
		t.Fatalf("client-disconnect releases = %v, want unchanged %v", got, beforeClient)
	}
}

func TestDownloadAdmissionFailUsesFirstCauseExactlyOnce(t *testing.T) {
	cfg := admissionLifecycleConfig()
	coordinator := newAdmissionLifecycleCoordinator(t, cfg)
	beforeStorage := releaseCount(downloadadmission.ReleaseStorageError)
	beforeCompleted := releaseCount(downloadadmission.ReleaseCompleted)
	c, _ := newAdmissionLifecycleContext(context.Background(), newIdleWriteTestWriter())
	lifecycle, reason, err := AcquireDownloadAdmission(c, coordinator, cfg, admissionLifecycleRequest(t, "idempotent"))
	if err != nil || reason != "" {
		t.Fatalf("AcquireDownloadAdmission = (%q, %v)", reason, err)
	}

	lifecycle.Fail(downloadadmission.ReleaseStorageError)
	lifecycle.Fail(downloadadmission.ReleaseCompleted)
	if err := lifecycle.Finish(downloadadmission.ReleaseCompleted); err != nil {
		t.Fatalf("Finish = %v", err)
	}
	if got := releaseCount(downloadadmission.ReleaseStorageError); got != beforeStorage+1 {
		t.Fatalf("storage release count = %v, want %v", got, beforeStorage+1)
	}
	if got := releaseCount(downloadadmission.ReleaseCompleted); got != beforeCompleted {
		t.Fatalf("completed release count = %v, want %v", got, beforeCompleted)
	}
}

func TestDownloadAdmissionConcurrentFailIsIdempotent(t *testing.T) {
	cfg := admissionLifecycleConfig()
	coordinator := newAdmissionLifecycleCoordinator(t, cfg)
	causes := []downloadadmission.ReleaseCause{
		downloadadmission.ReleaseCompleted,
		downloadadmission.ReleaseStorageError,
		downloadadmission.ReleaseResponseError,
		downloadadmission.ReleaseClientDisconnect,
	}
	before := 0.0
	for _, cause := range causes {
		before += releaseCount(cause)
	}
	c, _ := newAdmissionLifecycleContext(context.Background(), newIdleWriteTestWriter())
	lifecycle, reason, err := AcquireDownloadAdmission(c, coordinator, cfg, admissionLifecycleRequest(t, "concurrent"))
	if err != nil || reason != "" {
		t.Fatalf("AcquireDownloadAdmission = (%q, %v)", reason, err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		cause := causes[i%len(causes)]
		wg.Add(1)
		go func() {
			defer wg.Done()
			lifecycle.Fail(cause)
		}()
	}
	wg.Wait()
	if err := lifecycle.Finish(downloadadmission.ReleaseCompleted); err != nil {
		t.Fatalf("Finish = %v", err)
	}
	after := 0.0
	for _, cause := range causes {
		after += releaseCount(cause)
	}
	if after != before+1 {
		t.Fatalf("release total = %v, want %v", after, before+1)
	}
}

func TestDownloadAdmissionFinishHandlerPreservesPanic(t *testing.T) {
	cfg := admissionLifecycleConfig()
	coordinator := newAdmissionLifecycleCoordinator(t, cfg)
	before := releaseCount(downloadadmission.ReleasePanic)
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		c, _ := newAdmissionLifecycleContext(context.Background(), newIdleWriteTestWriter())
		lifecycle, reason, err := AcquireDownloadAdmission(c, coordinator, cfg, admissionLifecycleRequest(t, "panic"))
		if err != nil || reason != "" {
			t.Fatalf("AcquireDownloadAdmission = (%q, %v)", reason, err)
		}
		defer lifecycle.FinishHandler()
		panic("handler panic")
	}()
	if recovered != "handler panic" {
		t.Fatalf("recovered panic = %v, want handler panic", recovered)
	}
	if got := releaseCount(downloadadmission.ReleasePanic); got != before+1 {
		t.Fatalf("panic release count = %v, want %v", got, before+1)
	}
}

func TestDownloadAdmissionFinishHandlerClaimsPanicBeforeWriterCleanup(t *testing.T) {
	cfg := admissionLifecycleConfig()
	coordinator := newAdmissionLifecycleCoordinator(t, cfg)
	underlying := newIdleWriteTestWriter()
	c, _ := newAdmissionLifecycleContext(context.Background(), underlying)
	lifecycle, reason, err := AcquireDownloadAdmission(c, coordinator, cfg, admissionLifecycleRequest(t, "panic-stream"))
	if err != nil || reason != "" {
		t.Fatalf("AcquireDownloadAdmission = (%q, %v)", reason, err)
	}
	if _, err := lifecycle.StartStreaming(); err != nil {
		t.Fatalf("StartStreaming = %v", err)
	}
	underlying.deadlineHook = func(deadline time.Time) {
		if deadline.IsZero() {
			lifecycle.Fail(downloadadmission.ReleaseStorageError)
		}
	}

	beforePanic := releaseCount(downloadadmission.ReleasePanic)
	beforeStorage := releaseCount(downloadadmission.ReleaseStorageError)
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		defer lifecycle.FinishHandler()
		panic("streaming handler panic")
	}()
	if recovered != "streaming handler panic" {
		t.Fatalf("recovered panic = %v, want streaming handler panic", recovered)
	}
	waitForMetric(t, func() float64 { return releaseCount(downloadadmission.ReleasePanic) }, beforePanic+1)
	if got := releaseCount(downloadadmission.ReleaseStorageError); got != beforeStorage {
		t.Fatalf("storage-error release count = %v, want %v", got, beforeStorage)
	}
}
