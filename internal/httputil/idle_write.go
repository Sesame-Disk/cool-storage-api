package httputil

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

var (
	ErrIdleWriteTimeout           = errors.New("idle write timeout")
	ErrIdleWriteWriterUnreachable = errors.New("idle write writer cannot reach connection")
	ErrIdleWriteCancelRequired    = errors.New("idle write cancel function is required")
)

// IdleWriteOptions controls the lifetime callbacks owned by a response writer.
// Admission leases are deliberately not referenced here; D4 supplies the
// callbacks when it connects producers to the coordinator.
type IdleWriteOptions struct {
	Timeout      time.Duration
	Cancel       context.CancelFunc
	OnTimeout    func()
	OnWriteError func(error)
}

// IdleWriteWriter refreshes a connection write deadline around response progress
// and cancels the request when no progress is made for Timeout. It intentionally
// does not implement io.ReaderFrom, so a copy cannot bypass Write's deadline hook.
type IdleWriteWriter struct {
	gin.ResponseWriter
	controller *http.ResponseController
	timeout    time.Duration
	cancel     context.CancelFunc
	onTimeout  func()
	onError    func(error)

	mu               sync.Mutex
	timer            *time.Timer
	timerGeneration  uint64
	progressDeadline time.Time
	finished         bool
	failed           bool
	err              error
}

var _ gin.ResponseWriter = (*IdleWriteWriter)(nil)

// NewIdleWriteWriter verifies that the underlying writer can reach the network
// connection before the response headers are committed.
func NewIdleWriteWriter(w gin.ResponseWriter, opts IdleWriteOptions) (*IdleWriteWriter, error) {
	if w == nil {
		return nil, fmt.Errorf("%w: response writer is nil", ErrIdleWriteWriterUnreachable)
	}
	if opts.Timeout <= 0 {
		return nil, fmt.Errorf("idle write timeout must be positive")
	}
	if opts.Cancel == nil {
		return nil, ErrIdleWriteCancelRequired
	}

	writer := &IdleWriteWriter{
		ResponseWriter: w,
		controller:     http.NewResponseController(w),
		timeout:        opts.Timeout,
		cancel:         opts.Cancel,
		onTimeout:      opts.OnTimeout,
		onError:        opts.OnWriteError,
	}
	if err := writer.SetWriteDeadline(time.Now().Add(opts.Timeout)); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrIdleWriteWriterUnreachable, err)
	}
	if err := writer.SetWriteDeadline(time.Time{}); err != nil {
		return nil, fmt.Errorf("%w: failed to clear probe deadline: %v", ErrIdleWriteWriterUnreachable, err)
	}
	return writer, nil
}

// StartIdleInterval opens the idle interval before any output exists, so the
// span between admission's phase change and the first byte is bounded too.
// Without it the timer is armed only by real progress, leaving a stalled first
// storage read holding its slot with no deadline at all: the preparation
// deadline is already over and no write has happened yet.
//
// It is deliberately separate from NewIdleWriteWriter, which owns only the
// reachability probe. DownloadAdmission.StartStreaming is the single caller;
// arming twice is a no-op rather than a second timer.
func (w *IdleWriteWriter) StartIdleInterval() error {
	w.mu.Lock()
	if w.finished || w.failed {
		err := w.err
		if err == nil {
			err = ErrIdleWriteTimeout
		}
		w.mu.Unlock()
		return err
	}
	if !w.progressDeadline.IsZero() {
		w.mu.Unlock()
		return nil
	}
	w.timerGeneration++
	generation := w.timerGeneration
	if w.timer != nil {
		w.timer.Stop()
	}
	// No socket deadline here: nothing is being written yet. beforeWrite installs
	// one from this absolute deadline when a write actually begins, so the first
	// write inherits the remaining interval instead of a fresh full one.
	w.progressDeadline = time.Now().Add(w.timeout)
	w.timer = time.AfterFunc(time.Until(w.progressDeadline), func() { w.expire(generation) })
	w.mu.Unlock()
	return nil
}

// SetWriteDeadline exposes the connection capability to ResponseController and
// delegates through the original writer chain.
func (w *IdleWriteWriter) SetWriteDeadline(deadline time.Time) error {
	return w.controller.SetWriteDeadline(deadline)
}

// Unwrap lets other HTTP helpers inspect the writer chain. The direct deadline
// method above is still required because ResponseController stops at wrappers
// that do not expose the capability themselves.
func (w *IdleWriteWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *IdleWriteWriter) Write(p []byte) (int, error) {
	if err := w.beforeWrite(); err != nil {
		return 0, err
	}
	n, err := w.ResponseWriter.Write(p)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	if err != nil {
		w.fail(err)
		return n, err
	}
	if err := w.progress(); err != nil {
		return n, err
	}
	return n, nil
}

func (w *IdleWriteWriter) WriteString(s string) (int, error) {
	if err := w.beforeWrite(); err != nil {
		return 0, err
	}
	n, err := w.ResponseWriter.WriteString(s)
	if err == nil && n != len(s) {
		err = io.ErrShortWrite
	}
	if err != nil {
		w.fail(err)
		return n, err
	}
	if err := w.progress(); err != nil {
		return n, err
	}
	return n, nil
}

// WriteHeader preserves Gin's deferred-header behavior. A custom writer may
// commit headers here, while Gin merely records the status until output begins.
func (w *IdleWriteWriter) WriteHeader(status int) {
	if w.ResponseWriter.Written() {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	if err := w.beforeWrite(); err != nil {
		return
	}
	w.ResponseWriter.WriteHeader(status)
	if w.ResponseWriter.Written() {
		_ = w.progress()
		return
	}
	_ = w.restoreIdleIntervalWithoutProgress()
}

func (w *IdleWriteWriter) WriteHeaderNow() {
	if w.ResponseWriter.Written() {
		w.ResponseWriter.WriteHeaderNow()
		return
	}
	if err := w.beforeWrite(); err != nil {
		return
	}
	w.ResponseWriter.WriteHeaderNow()
	if w.ResponseWriter.Written() {
		_ = w.progress()
		return
	}
	_ = w.restoreIdleIntervalWithoutProgress()
}

// FlushError is the error-returning form used by D4. Flush remains available
// through gin.ResponseWriter and intentionally cannot return an error.
func (w *IdleWriteWriter) FlushError() error {
	if err := w.beforeWrite(); err != nil {
		return err
	}
	if err := w.controller.Flush(); err != nil {
		w.fail(err)
		return err
	}
	return w.progress()
}

func (w *IdleWriteWriter) Flush() {
	_ = w.FlushError()
}

// Finish stops the progress timer and clears the connection deadline so a
// keep-alive connection does not inherit this response's idle timeout.
func (w *IdleWriteWriter) Finish() error {
	w.mu.Lock()
	if w.finished {
		err := w.err
		w.mu.Unlock()
		return err
	}
	w.finished = true
	w.timerGeneration++
	w.progressDeadline = time.Time{}
	if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}
	err := w.err
	onError := w.onError
	w.mu.Unlock()

	clearErr := w.SetWriteDeadline(time.Time{})
	notifyError := false
	if clearErr != nil {
		w.mu.Lock()
		if w.err == nil {
			w.err = clearErr
			err = clearErr
			notifyError = true
		}
		w.mu.Unlock()
	}
	if notifyError && onError != nil {
		onError(clearErr)
	}
	return err
}

// Err returns the first terminal writer error, if any.
func (w *IdleWriteWriter) Err() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

func (w *IdleWriteWriter) beforeWrite() error {
	w.mu.Lock()
	if w.finished || w.failed {
		err := w.err
		if err == nil {
			err = ErrIdleWriteTimeout
		}
		w.mu.Unlock()
		return err
	}
	// A socket deadline, rather than the progress timer, bounds the write that
	// is about to begin. After progress, keep the original absolute deadline so
	// beginning a write cannot grant another full idle timeout.
	w.timerGeneration++
	if w.timer != nil {
		w.timer.Stop()
	}
	now := time.Now()
	deadline := w.progressDeadline
	if deadline.IsZero() {
		deadline = now.Add(w.timeout)
	} else if !deadline.After(now) {
		cancel, onTimeout, onError := w.failLocked(ErrIdleWriteTimeout)
		w.mu.Unlock()
		w.notifyFailure(ErrIdleWriteTimeout, cancel, onTimeout, onError)
		return ErrIdleWriteTimeout
	}
	if err := w.SetWriteDeadline(deadline); err != nil {
		cancel, onTimeout, onError := w.failLocked(err)
		w.mu.Unlock()
		w.notifyFailure(err, cancel, onTimeout, onError)
		return err
	}
	w.mu.Unlock()
	return nil
}

// restoreIdleIntervalWithoutProgress undoes the socket deadline beforeWrite
// installed for a write that turned out not to commit any output, such as Gin
// recording a deferred status.
//
// A deferred header is not progress, so it must not extend the interval — but it
// must not erase it either. Clearing it outright is what would reopen the gap
// StartIdleInterval exists to close: `c.Status(200)` right after the phase change
// would cancel the only deadline covering the first storage read. The original
// absolute deadline is therefore re-armed unchanged, and the pre-arming case
// still ends with no timer at all.
func (w *IdleWriteWriter) restoreIdleIntervalWithoutProgress() error {
	w.mu.Lock()
	if w.finished || w.failed {
		err := w.err
		if err == nil {
			err = ErrIdleWriteTimeout
		}
		w.mu.Unlock()
		return err
	}
	w.timerGeneration++
	generation := w.timerGeneration
	if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}
	if err := w.SetWriteDeadline(time.Time{}); err != nil {
		cancel, onTimeout, onError := w.failLocked(err)
		w.mu.Unlock()
		w.notifyFailure(err, cancel, onTimeout, onError)
		return err
	}
	if !w.progressDeadline.IsZero() {
		w.timer = time.AfterFunc(time.Until(w.progressDeadline), func() { w.expire(generation) })
	}
	w.mu.Unlock()
	return nil
}

func (w *IdleWriteWriter) progress() error {
	w.mu.Lock()
	if w.finished || w.failed {
		if w.err != nil {
			w.mu.Unlock()
			return w.err
		}
		w.mu.Unlock()
		return ErrIdleWriteTimeout
	}
	w.timerGeneration++
	generation := w.timerGeneration
	w.progressDeadline = time.Now().Add(w.timeout)
	if w.timer != nil {
		w.timer.Stop()
	}
	if err := w.SetWriteDeadline(time.Time{}); err != nil {
		cancel, onTimeout, onError := w.failLocked(err)
		w.mu.Unlock()
		w.notifyFailure(err, cancel, onTimeout, onError)
		return err
	}
	w.timer = time.AfterFunc(time.Until(w.progressDeadline), func() { w.expire(generation) })
	w.mu.Unlock()
	return nil
}

func (w *IdleWriteWriter) expire(generation uint64) {
	w.mu.Lock()
	if w.finished || w.failed || generation != w.timerGeneration {
		w.mu.Unlock()
		return
	}
	cancel, onTimeout, onError := w.failLocked(ErrIdleWriteTimeout)
	w.mu.Unlock()
	w.notifyFailure(ErrIdleWriteTimeout, cancel, onTimeout, onError)
}

func (w *IdleWriteWriter) fail(err error) {
	if err == nil {
		return
	}
	w.mu.Lock()
	cancel, onTimeout, onError := w.failLocked(err)
	w.mu.Unlock()
	w.notifyFailure(err, cancel, onTimeout, onError)
}

func (w *IdleWriteWriter) failLocked(err error) (context.CancelFunc, func(), func(error)) {
	if w.finished || w.failed {
		return nil, nil, nil
	}
	w.failed = true
	w.err = err
	w.timerGeneration++
	w.progressDeadline = time.Time{}
	if w.timer == nil {
		return w.cancel, w.onTimeout, w.onError
	}
	w.timer.Stop()
	return w.cancel, w.onTimeout, w.onError
}

func (w *IdleWriteWriter) notifyFailure(err error, cancel context.CancelFunc, onTimeout func(), onError func(error)) {
	if cancel == nil {
		return
	}
	// Notify the owner before exposing cancellation to request workers. D4 uses
	// these callbacks to claim the terminal admission cause; cancelling first
	// lets a worker observe context.Canceled and win the lease as storage_error.
	defer cancel()
	if isTimeoutError(err) {
		if onTimeout != nil {
			onTimeout()
		}
	} else if onError != nil {
		onError(err)
	}
}

func isTimeoutError(err error) bool {
	if errors.Is(err, ErrIdleWriteTimeout) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}
