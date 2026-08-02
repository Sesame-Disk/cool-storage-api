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
		waitForMetric(t, func() float64 { return releaseCount(downloadadmission.ReleaseClientDisconnect) }, beforeRelease+1)
		if got := deadlineCount(downloadadmission.DeadlinePreparation); got != beforeDeadline {
			t.Fatalf("request cancellation changed preparation deadline metric from %v to %v", beforeDeadline, got)
		}
	})
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
	waitForMetric(t, func() float64 { return releaseCount(downloadadmission.ReleaseResponseError) }, beforeRelease+1)
	if err := lifecycle.Finish(downloadadmission.ReleaseCompleted); !errors.Is(err, writeErr) {
		t.Fatalf("Finish = %v, want response write error", err)
	}
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
	waitForMetric(t, func() float64 { return deadlineCount(downloadadmission.DeadlineIdleWrite) }, beforeDeadline+1)
	waitForMetric(t, func() float64 { return releaseCount(downloadadmission.ReleaseIdleWriteTimeout) }, beforeRelease+1)
	if err := lifecycle.Finish(downloadadmission.ReleaseCompleted); !errors.Is(err, ErrIdleWriteTimeout) {
		t.Fatalf("Finish = %v, want idle write timeout", err)
	}
}

func TestDownloadAdmissionReleaseUsesFirstCauseExactlyOnce(t *testing.T) {
	cfg := admissionLifecycleConfig()
	coordinator := newAdmissionLifecycleCoordinator(t, cfg)
	beforeStorage := releaseCount(downloadadmission.ReleaseStorageError)
	beforeCompleted := releaseCount(downloadadmission.ReleaseCompleted)
	c, _ := newAdmissionLifecycleContext(context.Background(), newIdleWriteTestWriter())
	lifecycle, reason, err := AcquireDownloadAdmission(c, coordinator, cfg, admissionLifecycleRequest(t, "idempotent"))
	if err != nil || reason != "" {
		t.Fatalf("AcquireDownloadAdmission = (%q, %v)", reason, err)
	}

	lifecycle.Release(downloadadmission.ReleaseStorageError)
	lifecycle.Release(downloadadmission.ReleaseCompleted)
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

func TestDownloadAdmissionConcurrentReleaseIsIdempotent(t *testing.T) {
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
			lifecycle.Release(cause)
		}()
	}
	wg.Wait()
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
