package httputil

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/downloadadmission"
	"github.com/gin-gonic/gin"
)

var (
	ErrDownloadAdmissionContextRequired     = errors.New("gin context is required for download admission")
	ErrDownloadAdmissionCoordinatorRequired = errors.New("download admission coordinator is required")
	ErrDownloadAdmissionTimeoutRequired     = errors.New("download admission preparation and idle-write timeouts must be positive")
	ErrDownloadAdmissionReleased            = errors.New("download admission has already been released")
	ErrDownloadAdmissionStreaming           = errors.New("download admission streaming transition is already in progress")
)

// DownloadAdmission holds one admitted request through its preparation and
// response-streaming phases. Callers should defer FinishHandler immediately
// after a successful acquisition, then call StartStreaming before any output.
type DownloadAdmission struct {
	ginContext *gin.Context
	enabled    bool
	parent     context.Context
	lease      *downloadadmission.Lease
	idleWrite  time.Duration

	mu              sync.Mutex
	terminal        bool
	released        bool
	terminalCause   downloadadmission.ReleaseCause
	preparing       bool
	startingStream  bool
	preparation     context.Context
	prepareCancel   context.CancelFunc
	streaming       context.Context
	streamCancel    context.CancelFunc
	writer          *IdleWriteWriter
	originalWriter  gin.ResponseWriter
	stopParent      func() bool
	stopPreparation func() bool
}

type releaseState struct {
	stopParent      func() bool
	stopPreparation func() bool
	prepareCancel   context.CancelFunc
	streamCancel    context.CancelFunc
	lease           *downloadadmission.Lease
}

// AcquireDownloadAdmission acquires request before protected preparation work
// begins. An admission refusal is returned as a non-empty RejectReason; setup
// failures are returned as errors. With disabled configuration it is a
// transparent no-op and does not acquire a lease or replace the Gin writer.
func AcquireDownloadAdmission(c *gin.Context, coordinator *downloadadmission.Coordinator, cfg config.DownloadAdmissionConfig, request downloadadmission.AdmissionRequest) (*DownloadAdmission, downloadadmission.RejectReason, error) {
	if c == nil || c.Request == nil {
		return nil, "", ErrDownloadAdmissionContextRequired
	}

	parent := c.Request.Context()
	lifecycle := &DownloadAdmission{
		ginContext:    c,
		enabled:       cfg.Enabled,
		parent:        parent,
		idleWrite:     cfg.IdleWriteTimeout,
		preparation:   parent,
		prepareCancel: func() {},
	}
	if !cfg.Enabled {
		return lifecycle, "", nil
	}
	if coordinator == nil {
		return nil, "", ErrDownloadAdmissionCoordinatorRequired
	}
	if cfg.PreparationDeadline <= 0 || cfg.IdleWriteTimeout <= 0 {
		return nil, "", ErrDownloadAdmissionTimeoutRequired
	}

	lease, reason := coordinator.Acquire(parent, request)
	if lease == nil {
		return nil, reason, nil
	}

	preparation, cancel := context.WithTimeout(parent, cfg.PreparationDeadline)
	lifecycle.lease = lease
	lifecycle.preparing = true
	lifecycle.preparation = preparation
	lifecycle.prepareCancel = cancel
	stopParent := context.AfterFunc(parent, func() {
		lifecycle.Fail(downloadadmission.ReleaseClientDisconnect)
	})
	lifecycle.installParentStop(stopParent)
	stopPreparation := context.AfterFunc(preparation, lifecycle.preparationDone)
	lifecycle.installPreparationStop(stopPreparation)
	c.Request = c.Request.WithContext(preparation)
	return lifecycle, "", nil
}

// PreparationContext is the post-acquisition context that bounds metadata and
// reader setup. It is cancelled when streaming starts, or when the lease ends.
func (l *DownloadAdmission) PreparationContext() context.Context {
	if l == nil || l.preparation == nil {
		return context.Background()
	}
	return l.preparation
}

// StartStreaming replaces the preparation context with a cancelable context
// derived from the original request and installs an IdleWriteWriter before any
// response output. The returned context deliberately has no preparation
// deadline, so a legitimate long response is governed only by write progress.
func (l *DownloadAdmission) StartStreaming() (context.Context, error) {
	if l == nil {
		return nil, ErrDownloadAdmissionContextRequired
	}
	if !l.enabled {
		return l.parent, nil
	}

	l.mu.Lock()
	if l.terminal {
		l.mu.Unlock()
		return nil, ErrDownloadAdmissionReleased
	}
	if l.streaming != nil {
		streaming := l.streaming
		l.mu.Unlock()
		return streaming, nil
	}
	if l.startingStream {
		l.mu.Unlock()
		return nil, ErrDownloadAdmissionStreaming
	}
	if err := l.parent.Err(); err != nil {
		state, failed := l.claimFailureLocked(downloadadmission.ReleaseClientDisconnect)
		l.mu.Unlock()
		if failed {
			l.finishFailure(state)
		}
		return nil, err
	}
	if err := l.preparation.Err(); err != nil {
		cause := downloadadmission.ReleaseStorageError
		phase := downloadadmission.DeadlinePhase("")
		if errors.Is(err, context.DeadlineExceeded) {
			cause = downloadadmission.ReleasePreparationTimeout
			phase = downloadadmission.DeadlinePreparation
		}
		state, failed := l.claimFailureLocked(cause)
		l.mu.Unlock()
		if failed {
			if phase != "" {
				state.lease.RecordDeadlineExpired(phase)
			}
			l.finishFailure(state)
		}
		return nil, err
	}
	l.startingStream = true
	l.mu.Unlock()

	streaming, cancel := context.WithCancel(l.parent)
	writer, err := NewIdleWriteWriter(l.ginContext.Writer, IdleWriteOptions{
		Timeout: l.idleWrite,
		Cancel:  cancel,
		OnTimeout: func() {
			l.lease.RecordDeadlineExpired(downloadadmission.DeadlineIdleWrite)
			l.Fail(downloadadmission.ReleaseIdleWriteTimeout)
		},
		OnWriteError: func(error) {
			l.FailStreamError(downloadadmission.ReleaseResponseError)
		},
	})
	if err != nil {
		cancel()
		l.mu.Lock()
		l.startingStream = false
		l.mu.Unlock()
		l.lease.RecordWriterUnreachable()
		l.Fail(downloadadmission.ReleaseResponseError)
		return nil, err
	}

	l.mu.Lock()
	l.startingStream = false
	if l.terminal {
		l.mu.Unlock()
		_ = writer.Finish()
		cancel()
		return nil, ErrDownloadAdmissionReleased
	}
	if err := l.parent.Err(); err != nil {
		state, failed := l.claimFailureLocked(downloadadmission.ReleaseClientDisconnect)
		l.mu.Unlock()
		_ = writer.Finish()
		cancel()
		if failed {
			l.finishFailure(state)
		}
		return nil, err
	}
	if err := l.preparation.Err(); err != nil {
		cause := downloadadmission.ReleaseStorageError
		phase := downloadadmission.DeadlinePhase("")
		if errors.Is(err, context.DeadlineExceeded) {
			cause = downloadadmission.ReleasePreparationTimeout
			phase = downloadadmission.DeadlinePreparation
		}
		state, failed := l.claimFailureLocked(cause)
		l.mu.Unlock()
		_ = writer.Finish()
		cancel()
		if failed {
			if phase != "" {
				state.lease.RecordDeadlineExpired(phase)
			}
			l.finishFailure(state)
		}
		return nil, err
	}
	l.streaming = streaming
	l.streamCancel = cancel
	l.writer = writer
	l.originalWriter = l.ginContext.Writer
	l.preparing = false
	stopPreparation := l.stopPreparation
	l.stopPreparation = nil
	prepareCancel := l.prepareCancel
	l.ginContext.Request = l.ginContext.Request.WithContext(streaming)
	l.ginContext.Writer = writer
	l.mu.Unlock()

	if stopPreparation != nil {
		stopPreparation()
	}
	prepareCancel()
	return streaming, nil
}

// Finish stops the idle-write writer, clears its connection deadline, restores
// the original Gin writer, and releases the lease. If a writer callback already
// claimed the lifecycle, its earlier cause remains authoritative.
func (l *DownloadAdmission) Finish(cause downloadadmission.ReleaseCause) error {
	if l == nil || !l.enabled {
		return nil
	}
	l.mu.Lock()
	writer := l.writer
	l.mu.Unlock()

	var err error
	if writer != nil {
		err = writer.Finish()
		l.mu.Lock()
		l.ginContext.Writer = l.originalWriter
		l.mu.Unlock()
	}
	l.finishCause(cause)
	l.releaseLease()
	return err
}

// FinishHandler is intended for defer immediately after successful admission.
// It preserves a handler panic while ensuring the lease is attributed to panic
// rather than the normal completed path.
func (l *DownloadAdmission) FinishHandler() {
	if recovered := recover(); recovered != nil {
		_ = l.Finish(downloadadmission.ReleasePanic)
		panic(recovered)
	}
	_ = l.Finish(downloadadmission.ReleaseCompleted)
}

// Fail claims the first terminal cause and cancels lifecycle contexts. It does
// not release the admission lease; the producer's deferred Finish performs the
// physical release after response cleanup such as zip.Writer.Close returns.
func (l *DownloadAdmission) Fail(cause downloadadmission.ReleaseCause) {
	if l == nil || !l.enabled {
		return
	}

	l.failCause(cause, false)
}

// FailStreamError classifies a terminal stream error as a client disconnect
// when the request context is already canceled. This check is linearized with
// the first-cause claim so a stream worker cannot turn an observed disconnect
// into storage_error while the parent cancellation callback is still queued.
func (l *DownloadAdmission) FailStreamError(cause downloadadmission.ReleaseCause) {
	if l == nil || !l.enabled {
		return
	}
	l.failCause(cause, true)
}

func (l *DownloadAdmission) failCause(cause downloadadmission.ReleaseCause, parentCancellationWins bool) {
	l.mu.Lock()
	if parentCancellationWins && l.parent.Err() != nil {
		cause = downloadadmission.ReleaseClientDisconnect
	}
	state, failed := l.claimFailureLocked(cause)
	l.mu.Unlock()
	if !failed {
		return
	}
	l.finishFailure(state)
}

func (l *DownloadAdmission) finishCause(cause downloadadmission.ReleaseCause) {
	l.mu.Lock()
	if cause == downloadadmission.ReleaseCompleted {
		if l.parent.Err() != nil {
			cause = downloadadmission.ReleaseClientDisconnect
		} else if l.preparing && errors.Is(l.preparation.Err(), context.DeadlineExceeded) {
			cause = downloadadmission.ReleasePreparationTimeout
		}
	}
	state, failed := l.claimFailureLocked(cause)
	l.mu.Unlock()
	if failed {
		if cause == downloadadmission.ReleasePreparationTimeout {
			state.lease.RecordDeadlineExpired(downloadadmission.DeadlinePreparation)
		}
		l.finishFailure(state)
	}
}

// ReleasePreparationError attributes a preparation failure to the request's
// cancellation or preparation deadline when either one caused the error. It
// claims the lease under the same mutex as the deadline callback, so the
// callback and a handler observing context.DeadlineExceeded cannot race to
// produce different causes.
func (l *DownloadAdmission) ReleasePreparationError(err error) {
	if l == nil || !l.enabled {
		return
	}

	l.mu.Lock()
	if l.terminal || l.released || !l.preparing {
		l.mu.Unlock()
		return
	}
	cause := downloadadmission.ReleaseStorageError
	phase := downloadadmission.DeadlinePhase("")
	if l.parent.Err() != nil {
		cause = downloadadmission.ReleaseClientDisconnect
	} else if preparationDeadlineExpired(l.preparation, err) {
		cause = downloadadmission.ReleasePreparationTimeout
		phase = downloadadmission.DeadlinePreparation
	}
	state, failed := l.claimFailureLocked(cause)
	l.mu.Unlock()
	if failed {
		if phase != "" {
			state.lease.RecordDeadlineExpired(phase)
		}
		l.finishFailure(state)
	}
}

func preparationDeadlineExpired(preparation context.Context, err error) bool {
	if preparation == nil {
		return false
	}
	if errors.Is(preparation.Err(), context.DeadlineExceeded) {
		return true
	}
	deadline, hasDeadline := preparation.Deadline()
	return hasDeadline && !time.Now().Before(deadline) && errors.Is(err, context.DeadlineExceeded)
}

// RenderDownloadAdmissionRefusal sends the shared retryable refusal response.
// It must be called before a handler writes its own response.
func RenderDownloadAdmissionRefusal(c *gin.Context, coordinator *downloadadmission.Coordinator) {
	if c == nil || c.Writer.Written() {
		return
	}
	c.Header("Retry-After", strconv.Itoa(coordinator.RetryAfterSeconds()))
	c.AbortWithStatus(http.StatusServiceUnavailable)
}

func (l *DownloadAdmission) preparationDone() {
	l.ReleasePreparationError(l.PreparationContext().Err())
}

func (l *DownloadAdmission) claimFailureLocked(cause downloadadmission.ReleaseCause) (releaseState, bool) {
	if l.terminal || l.released {
		return releaseState{}, false
	}
	l.terminal = true
	l.terminalCause = cause
	l.preparing = false
	state := releaseState{
		stopParent:      l.stopParent,
		stopPreparation: l.stopPreparation,
		prepareCancel:   l.prepareCancel,
		streamCancel:    l.streamCancel,
		lease:           l.lease,
	}
	l.stopParent = nil
	l.stopPreparation = nil
	return state, true
}

func (l *DownloadAdmission) finishFailure(state releaseState) {
	if state.stopParent != nil {
		state.stopParent()
	}
	if state.stopPreparation != nil {
		state.stopPreparation()
	}
	if state.prepareCancel != nil {
		state.prepareCancel()
	}
	if state.streamCancel != nil {
		state.streamCancel()
	}
}

func (l *DownloadAdmission) releaseLease() {
	l.mu.Lock()
	if l.released {
		l.mu.Unlock()
		return
	}
	l.released = true
	if !l.terminal {
		l.terminal = true
		l.terminalCause = downloadadmission.ReleaseCompleted
	}
	lease := l.lease
	cause := l.terminalCause
	l.mu.Unlock()
	if lease != nil {
		lease.Release(cause)
	}
}

func (l *DownloadAdmission) installParentStop(stop func() bool) {
	l.mu.Lock()
	if !l.terminal {
		l.stopParent = stop
		l.mu.Unlock()
		return
	}
	l.mu.Unlock()
	stop()
}

func (l *DownloadAdmission) installPreparationStop(stop func() bool) {
	l.mu.Lock()
	if !l.terminal {
		l.stopPreparation = stop
		l.mu.Unlock()
		return
	}
	l.mu.Unlock()
	stop()
}
