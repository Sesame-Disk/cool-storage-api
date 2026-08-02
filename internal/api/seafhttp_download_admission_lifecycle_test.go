package api

import (
	"archive/zip"
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/downloadadmission"
	"github.com/Sesame-Disk/sesamefs/internal/httputil"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type zipCloseGateResponseWriter struct {
	gin.ResponseWriter
	started chan struct{}
	allow   chan struct{}
	once    sync.Once
}

func (w *zipCloseGateResponseWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.started) })
	<-w.allow
	return len(p), nil
}

func (*zipCloseGateResponseWriter) SetWriteDeadline(time.Time) error { return nil }

func zipAdmissionConfig() config.DownloadAdmissionConfig {
	return config.DownloadAdmissionConfig{
		Enabled:                true,
		MaxActivePerNode:       1,
		MaxActivePerAuthUser:   1,
		MaxActivePerLinkSource: 1,
		MaxActivePerClientLink: 1,
		MaxActiveZIP:           1,
		MaxActiveFile:          1,
		PreparationDeadline:    time.Minute,
		IdleWriteTimeout:       time.Minute,
		RetryAfter:             time.Second,
	}
}

func newZipAdmissionLifecycle(t *testing.T) (*httputil.DownloadAdmission, *downloadadmission.Coordinator) {
	lifecycle, coordinator, _ := newZipAdmissionLifecycleWithWriter(t, nil)
	return lifecycle, coordinator
}

func newZipAdmissionLifecycleWithWriter(t *testing.T, writer gin.ResponseWriter) (*httputil.DownloadAdmission, *downloadadmission.Coordinator, *gin.Context) {
	t.Helper()
	cfg := zipAdmissionConfig()
	coordinator, err := downloadadmission.New(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	if writer != nil {
		c.Writer = writer
	}
	c.Request = httptest.NewRequest(http.MethodGet, "/seafhttp/zip/token", nil)
	request, err := downloadadmission.NewAuthenticatedRequest(downloadadmission.ProfileZIP, "org-1", "zip-user")
	if err != nil {
		t.Fatal(err)
	}
	lifecycle, reason, err := httputil.AcquireDownloadAdmission(c, coordinator, cfg, request)
	if err != nil || reason != "" || lifecycle == nil {
		t.Fatalf("AcquireDownloadAdmission = (%v, %q, %v)", lifecycle, reason, err)
	}
	return lifecycle, coordinator, c
}

func TestFinishSeafHTTPZipDownloadHoldsLeaseThroughClose(t *testing.T) {
	baseRecorder := httptest.NewRecorder()
	baseContext, _ := gin.CreateTestContext(baseRecorder)
	gated := &zipCloseGateResponseWriter{
		ResponseWriter: baseContext.Writer,
		started:        make(chan struct{}),
		allow:          make(chan struct{}),
	}
	lifecycle, coordinator, c := newZipAdmissionLifecycleWithWriter(t, gated)
	if _, err := lifecycle.StartStreaming(); err != nil {
		t.Fatalf("StartStreaming = %v", err)
	}
	zipWriter := zip.NewWriter(c.Writer)
	cause := downloadadmission.ReleaseStorageError
	done := make(chan struct{})
	go func() {
		finishSeafHTTPZipDownload(lifecycle, &zipWriter, &cause)
		close(done)
	}()

	select {
	case <-gated.started:
	case <-time.After(time.Second):
		t.Fatal("ZIP close did not reach the response writer")
	}
	request, err := downloadadmission.NewAuthenticatedRequest(downloadadmission.ProfileFile, "org-1", "other-user")
	if err != nil {
		t.Fatal(err)
	}
	blocked, reason := coordinator.Acquire(context.Background(), request)
	if blocked != nil || reason != downloadadmission.RejectNodeFull {
		t.Fatalf("acquire during ZIP close = (%v, %q), want node-full refusal", blocked, reason)
	}

	close(gated.allow)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ZIP lifecycle did not finish after Close returned")
	}

	available, reason := coordinator.Acquire(context.Background(), request)
	if available == nil || reason != "" {
		t.Fatalf("acquire after ZIP close = (%v, %q), want admission", available, reason)
	}
	available.Release(downloadadmission.ReleaseCompleted)
}

func TestFinishSeafHTTPZipDownloadPreservesBodyPanicCause(t *testing.T) {
	lifecycle, _ := newZipAdmissionLifecycle(t)
	before := testutil.ToFloat64(metrics.DownloadAdmissionReleasedTotal.WithLabelValues(string(downloadadmission.ReleasePanic)))
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		var zipWriter *zip.Writer
		cause := downloadadmission.ReleaseStorageError
		defer finishSeafHTTPZipDownload(lifecycle, &zipWriter, &cause)
		panic("ZIP body panic")
	}()
	if recovered != "ZIP body panic" {
		t.Fatalf("recovered panic = %v, want ZIP body panic", recovered)
	}
	if got := testutil.ToFloat64(metrics.DownloadAdmissionReleasedTotal.WithLabelValues(string(downloadadmission.ReleasePanic))); got != before+1 {
		t.Fatalf("panic release count = %v, want %v", got, before+1)
	}
}

func TestFinishSeafHTTPZipDownloadPreservesClosePanicCause(t *testing.T) {
	lifecycle, _ := newZipAdmissionLifecycle(t)
	before := testutil.ToFloat64(metrics.DownloadAdmissionReleasedTotal.WithLabelValues(string(downloadadmission.ReleasePanic)))
	cause := downloadadmission.ReleaseCompleted

	var recovered any
	func() {
		defer func() { recovered = recover() }()
		// A normal writer cannot panic, so use a second writer whose Write method
		// panics during the central-directory write.
		panicWriter := &zipClosePanicWriter{}
		panicZip := zip.NewWriter(panicWriter)
		finishSeafHTTPZipDownload(lifecycle, &panicZip, &cause)
	}()
	if recovered != "ZIP close panic" {
		t.Fatalf("recovered panic = %v, want ZIP close panic", recovered)
	}
	if got := testutil.ToFloat64(metrics.DownloadAdmissionReleasedTotal.WithLabelValues(string(downloadadmission.ReleasePanic))); got != before+1 {
		t.Fatalf("close panic release count = %v, want %v", got, before+1)
	}
}

type zipClosePanicWriter struct{}

func (*zipClosePanicWriter) Write([]byte) (int, error) {
	panic("ZIP close panic")
}
