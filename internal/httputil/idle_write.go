package httputil

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

var (
	ErrIdleWriteTimeout           = errors.New("idle write timeout")
	ErrIdleWriteWriterUnreachable = errors.New("idle write writer cannot reach connection")
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

	mu       sync.Mutex
	timer    *time.Timer
	finished bool
	failed   bool
	err      error
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
	if err != nil {
		w.fail(err)
		return n, err
	}
	if n > 0 {
		if err := w.progress(); err != nil {
			return n, err
		}
	}
	return n, nil
}

func (w *IdleWriteWriter) WriteString(s string) (int, error) {
	if err := w.beforeWrite(); err != nil {
		return 0, err
	}
	n, err := w.ResponseWriter.WriteString(s)
	if err != nil {
		w.fail(err)
		return n, err
	}
	if n > 0 {
		if err := w.progress(); err != nil {
			return n, err
		}
	}
	return n, nil
}

func (w *IdleWriteWriter) WriteHeaderNow() {
	if err := w.beforeWrite(); err != nil {
		return
	}
	w.ResponseWriter.WriteHeaderNow()
	_ = w.progress()
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
	if w.timer != nil {
		w.timer.Stop()
	}
	err := w.err
	w.mu.Unlock()

	clearErr := w.SetWriteDeadline(time.Time{})
	if clearErr != nil && err == nil {
		err = clearErr
		w.mu.Lock()
		if w.err == nil {
			w.err = clearErr
		}
		w.mu.Unlock()
		if w.onError != nil {
			w.onError(clearErr)
		}
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
	w.mu.Unlock()

	if err := w.SetWriteDeadline(time.Now().Add(w.timeout)); err != nil {
		w.fail(err)
		return err
	}
	return nil
}

func (w *IdleWriteWriter) progress() error {
	if err := w.SetWriteDeadline(time.Time{}); err != nil {
		w.fail(err)
		return err
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.finished || w.failed {
		if w.err != nil {
			return w.err
		}
		return ErrIdleWriteTimeout
	}
	if w.timer == nil {
		w.timer = time.AfterFunc(w.timeout, w.expire)
	} else {
		w.timer.Reset(w.timeout)
	}
	return nil
}

func (w *IdleWriteWriter) expire() {
	w.fail(ErrIdleWriteTimeout)
}

func (w *IdleWriteWriter) fail(err error) {
	if err == nil {
		return
	}

	w.mu.Lock()
	if w.err == nil {
		w.err = err
	}
	if w.failed || w.finished {
		w.mu.Unlock()
		return
	}
	w.failed = true
	if w.timer != nil {
		w.timer.Stop()
	}
	w.mu.Unlock()

	if w.cancel != nil {
		w.cancel()
	}
	if isTimeoutError(err) {
		if w.onTimeout != nil {
			w.onTimeout()
		}
	} else if w.onError != nil {
		w.onError(err)
	}
}

func isTimeoutError(err error) bool {
	if errors.Is(err, ErrIdleWriteTimeout) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}
