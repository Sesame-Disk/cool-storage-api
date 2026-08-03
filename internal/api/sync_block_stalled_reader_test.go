package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/downloadadmission"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	"github.com/Sesame-Disk/sesamefs/internal/storage"
	"github.com/Sesame-Disk/sesamefs/internal/streaming"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// syncStalledCanonicalReader answers GetBlockSize normally and then blocks in
// GetBlockReader until its context is cancelled — a first storage read that
// never returns. It holds the return until released, so the test can observe the
// window where the operation has been cancelled but is still running.
type syncStalledCanonicalReader struct {
	size                 int64
	cancellationObserved chan struct{}
	allowReturn          chan struct{}
}

func (r *syncStalledCanonicalReader) GetBlock(context.Context, string) ([]byte, error) {
	return nil, context.Canceled
}

func (r *syncStalledCanonicalReader) GetBlockReader(ctx context.Context, _ string) (io.ReadCloser, error) {
	<-ctx.Done()
	close(r.cancellationObserved)
	<-r.allowReturn
	return nil, ctx.Err()
}

func (r *syncStalledCanonicalReader) GetBlockSize(context.Context, string) (int64, error) {
	return r.size, nil
}

func (r *syncStalledCanonicalReader) CheckBlocksExist(context.Context, []string, int) (map[string]bool, error) {
	return map[string]bool{}, nil
}

// syncFailingReaderOpen answers GetBlockSize and then fails the reader open
// immediately, which happens after StartStreaming has already armed the idle
// interval.
type syncFailingReaderOpen struct {
	openErr error
}

func (r *syncFailingReaderOpen) GetBlock(context.Context, string) ([]byte, error) {
	return nil, r.openErr
}

func (r *syncFailingReaderOpen) GetBlockReader(context.Context, string) (io.ReadCloser, error) {
	return nil, r.openErr
}

func (r *syncFailingReaderOpen) GetBlockSize(context.Context, string) (int64, error) {
	return 16, nil
}

func (r *syncFailingReaderOpen) CheckBlocksExist(context.Context, []string, int) (map[string]bool, error) {
	return map[string]bool{}, nil
}

// TestSyncBlockDownloadAdmissionFastOpenFailureKeepsErrorStatus guards the cost
// of opening the interval early. Arming a timer must not commit headers, so a
// reader open that fails quickly can still answer with a real status code
// instead of a truncated 200 — which is exactly what forcing WriteHeaderNow at
// the phase change would have cost.
func TestSyncBlockDownloadAdmissionFastOpenFailureKeepsErrorStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, tt := range []struct {
		name       string
		openErr    error
		wantStatus int
	}{
		{"not found stays 404", streaming.ErrCanonicalBlockMetadataNotFound, http.StatusNotFound},
		{"storage failure stays 500", errors.New("s3 unavailable"), http.StatusInternalServerError},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := syncBlockAdmissionConfig()
			cfg.IdleWriteTimeout = time.Minute
			h, _ := newSyncBlockAdmissionHandler(t, cfg)

			oldReader := syncNewCanonicalBlockReaderFn
			oldTouch := syncTouchBlockLastAccessFn
			t.Cleanup(func() {
				syncNewCanonicalBlockReaderFn = oldReader
				syncTouchBlockLastAccessFn = oldTouch
			})
			syncTouchBlockLastAccessFn = func(context.Context, *db.DB, string, string, time.Time) {}
			syncNewCanonicalBlockReaderFn = func(context.Context, *db.DB, *storage.Manager, string, []string, *storage.BlockStore, string) (streaming.CanonicalBlockReader, error) {
				return &syncFailingReaderOpen{openErr: tt.openErr}, nil
			}

			r := setupSyncTestRouter()
			r.GET("/seafhttp/repo/:repo_id/block/:block_id", func(gc *gin.Context) {
				gc.Writer = &syncBlockDeadlineResponseWriter{ResponseWriter: gc.Writer}
				h.GetBlock(gc)
			})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/seafhttp/repo/repo/block/"+strings.Repeat("8", 64), nil))

			if w.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; arming the idle interval must not commit headers", w.Code, tt.wantStatus)
			}
			if got := testutil.ToFloat64(metrics.DownloadAdmissionActiveCurrent); got != 0 {
				t.Fatalf("active admissions = %v, want 0", got)
			}
		})
	}
}

// TestSyncBlockDownloadAdmissionIdleTimeoutBoundsStalledReaderOpen covers the
// phase D previously left unbounded. After StartStreaming the preparation
// deadline is retired, and before D5 opened the idle interval early nothing else
// applied: a GetBlockReader that never returned held its admission slot until
// the client disconnected or the storage SDK timed out.
//
// It also pins the release ordering. The timeout cancels the work and claims the
// cause, but the lease stays occupied while the cancelled reader is still
// running — freeing capacity whose work is still alive would let the coordinator
// admit past its real ceiling.
func TestSyncBlockDownloadAdmissionIdleTimeoutBoundsStalledReaderOpen(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := syncBlockAdmissionConfig()
	cfg.PreparationDeadline = time.Minute
	cfg.IdleWriteTimeout = 80 * time.Millisecond
	h, _ := newSyncBlockAdmissionHandler(t, cfg)

	blockID := strings.Repeat("7", 64)
	reader := &syncStalledCanonicalReader{
		size:                 32,
		cancellationObserved: make(chan struct{}),
		allowReturn:          make(chan struct{}),
	}

	oldReader := syncNewCanonicalBlockReaderFn
	oldTouch := syncTouchBlockLastAccessFn
	t.Cleanup(func() {
		syncNewCanonicalBlockReaderFn = oldReader
		syncTouchBlockLastAccessFn = oldTouch
	})
	syncTouchBlockLastAccessFn = func(context.Context, *db.DB, string, string, time.Time) {}
	syncNewCanonicalBlockReaderFn = func(context.Context, *db.DB, *storage.Manager, string, []string, *storage.BlockStore, string) (streaming.CanonicalBlockReader, error) {
		return reader, nil
	}

	beforeIdle := testutil.ToFloat64(metrics.DownloadAdmissionReleasedTotal.WithLabelValues(string(downloadadmission.ReleaseIdleWriteTimeout)))
	beforeCompleted := testutil.ToFloat64(metrics.DownloadAdmissionReleasedTotal.WithLabelValues(string(downloadadmission.ReleaseCompleted)))
	beforeExpired := testutil.ToFloat64(metrics.DownloadAdmissionDeadlineExpiredTotal.WithLabelValues(string(downloadadmission.DeadlineIdleWrite)))

	r := setupSyncTestRouter()
	r.GET("/seafhttp/repo/:repo_id/block/:block_id", func(gc *gin.Context) {
		gc.Writer = &syncBlockDeadlineResponseWriter{ResponseWriter: gc.Writer}
		h.GetBlock(gc)
	})

	w := httptest.NewRecorder()
	handlerReturned := make(chan struct{})
	go func() {
		defer close(handlerReturned)
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/seafhttp/repo/repo/block/"+blockID, nil))
	}()

	select {
	case <-reader.cancellationObserved:
	case <-time.After(5 * time.Second):
		t.Fatal("the stalled reader open was never cancelled; nothing bounds the span between preparation and the first write")
	}

	// The work is cancelled but has not unwound. Capacity must still be charged.
	if got := testutil.ToFloat64(metrics.DownloadAdmissionActiveCurrent); got != 1 {
		t.Fatalf("active admissions while the cancelled reader is still running = %v, want 1; the lease must not be freed from the timeout callback", got)
	}
	if got := testutil.ToFloat64(metrics.DownloadAdmissionReleasedTotal.WithLabelValues(string(downloadadmission.ReleaseIdleWriteTimeout))); got != beforeIdle {
		t.Fatalf("idle_write_timeout releases = %v, want unchanged %v until the producer finishes", got, beforeIdle)
	}

	close(reader.allowReturn)
	select {
	case <-handlerReturned:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not return after the cancelled reader unwound")
	}

	if got := testutil.ToFloat64(metrics.DownloadAdmissionActiveCurrent); got != 0 {
		t.Fatalf("active admissions after unwind = %v, want 0", got)
	}
	if got := testutil.ToFloat64(metrics.DownloadAdmissionReleasedTotal.WithLabelValues(string(downloadadmission.ReleaseIdleWriteTimeout))); got != beforeIdle+1 {
		t.Fatalf("idle_write_timeout releases = %v, want exactly one more than %v", got, beforeIdle)
	}
	if got := testutil.ToFloat64(metrics.DownloadAdmissionReleasedTotal.WithLabelValues(string(downloadadmission.ReleaseCompleted))); got != beforeCompleted {
		t.Fatalf("completed releases = %v, want unchanged %v; a cancelled reader open is not a completed transfer", got, beforeCompleted)
	}
	if got := testutil.ToFloat64(metrics.DownloadAdmissionDeadlineExpiredTotal.WithLabelValues(string(downloadadmission.DeadlineIdleWrite))); got != beforeExpired+1 {
		t.Fatalf("idle_write deadline expiries = %v, want exactly one more than %v", got, beforeExpired)
	}

	// The failed writer rejects the producer's own error response, so without a
	// pre-header failure path Gin commits its default 200 with an empty body —
	// which seaf-cli cannot tell apart from a legitimately empty block that
	// downloaded fine. A timeout has to stay retryable.
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; a timed-out block must not look like an empty success", w.Code)
	}
	if got := w.Header().Get("Retry-After"); got != "2" {
		t.Fatalf("Retry-After = %q, want 2", got)
	}
	if w.Body.Len() != 0 {
		t.Fatalf("body = %q, want empty; nothing may be appended once the writer failed", w.Body.String())
	}
}
