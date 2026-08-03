package httputil

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

type idleWriteTestWriter struct {
	header       http.Header
	body         bytes.Buffer
	deadlines    []time.Time
	flushes      int
	writeErr     error
	flushErr     error
	shortWrite   bool
	status       int
	written      bool
	closed       chan bool
	deadlineHook func(time.Time)
}

func newIdleWriteTestWriter() *idleWriteTestWriter {
	return &idleWriteTestWriter{header: make(http.Header), closed: make(chan bool)}
}

func (w *idleWriteTestWriter) Header() http.Header { return w.header }

func (w *idleWriteTestWriter) Write(p []byte) (int, error) {
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	if w.shortWrite && len(p) > 0 {
		n := len(p) - 1
		_, _ = w.body.Write(p[:n])
		w.written = true
		return n, nil
	}
	w.written = true
	return w.body.Write(p)
}

func (w *idleWriteTestWriter) WriteHeader(status int) {
	w.status = status
	w.written = true
}

func (w *idleWriteTestWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, errors.New("hijack unsupported in test writer")
}

func (w *idleWriteTestWriter) Flush() {
	w.flushes++
	w.written = true
}

func (w *idleWriteTestWriter) FlushError() error {
	if w.flushErr != nil {
		return w.flushErr
	}
	w.Flush()
	return nil
}

func (w *idleWriteTestWriter) CloseNotify() <-chan bool { return w.closed }

func (w *idleWriteTestWriter) Status() int { return w.status }

func (w *idleWriteTestWriter) Size() int { return w.body.Len() }

func (w *idleWriteTestWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func (w *idleWriteTestWriter) Written() bool { return w.written }

func (w *idleWriteTestWriter) WriteHeaderNow() { w.WriteHeader(http.StatusOK) }

func (w *idleWriteTestWriter) Pusher() http.Pusher { return nil }

func (w *idleWriteTestWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadlines = append(w.deadlines, deadline)
	if w.deadlineHook != nil {
		w.deadlineHook(deadline)
	}
	return nil
}

func idleWriteOptions(timeout time.Duration) IdleWriteOptions {
	return IdleWriteOptions{Timeout: timeout, Cancel: func() {}}
}

func (w *IdleWriteWriter) testGeneration() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.timerGeneration
}

func (w *IdleWriteWriter) testTimerRunning() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.timer != nil
}

func (w *IdleWriteWriter) testProgressDeadline() time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.progressDeadline
}

func TestIdleWriteWriterTracksProgressAndClearsDeadline(t *testing.T) {
	underlying := newIdleWriteTestWriter()
	writer, err := NewIdleWriteWriter(underlying, idleWriteOptions(100*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := writer.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.WriteString(" world"); err != nil {
		t.Fatal(err)
	}
	if err := writer.FlushError(); err != nil {
		t.Fatal(err)
	}
	if err := writer.Finish(); err != nil {
		t.Fatal(err)
	}

	if got := underlying.body.String(); got != "hello world" {
		t.Fatalf("body = %q, want %q", got, "hello world")
	}
	if underlying.flushes != 1 {
		t.Fatalf("flushes = %d, want 1", underlying.flushes)
	}
	if len(underlying.deadlines) < 8 {
		t.Fatalf("deadline calls = %d, want probe, write and flush deadlines", len(underlying.deadlines))
	}
	if _, ok := any(writer).(io.ReaderFrom); ok {
		t.Fatal("IdleWriteWriter must not expose io.ReaderFrom")
	}

	time.Sleep(150 * time.Millisecond)
	if err := writer.Err(); err != nil {
		t.Fatalf("writer error after Finish = %v", err)
	}
	if got := underlying.deadlines[len(underlying.deadlines)-1]; !got.IsZero() {
		t.Fatalf("final deadline = %v, want cleared", got)
	}
}

func TestIdleWriteWriterCancelsAfterIdlePeriod(t *testing.T) {
	underlying := newIdleWriteTestWriter()
	cancelled := make(chan struct{})
	timedOut := make(chan struct{})
	var timeoutCalls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	writer, err := NewIdleWriteWriter(underlying, IdleWriteOptions{
		Timeout: 30 * time.Millisecond,
		Cancel: func() {
			cancel()
			select {
			case <-cancelled:
			default:
				close(cancelled)
			}
		},
		OnTimeout: func() {
			if timeoutCalls.Add(1) == 1 {
				close(timedOut)
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("progress")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-timedOut:
	case <-time.After(time.Second):
		t.Fatal("idle timeout callback did not run")
	}
	if ctx.Err() == nil {
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
			t.Fatal("idle timeout did not cancel the request context")
		}
	}
	if !errors.Is(writer.Err(), ErrIdleWriteTimeout) {
		t.Fatalf("writer error = %v, want idle timeout", writer.Err())
	}
	if timeoutCalls.Load() != 1 {
		t.Fatalf("timeout callback calls = %d, want 1", timeoutCalls.Load())
	}
	_ = writer.Finish()
}

func TestIdleWriteWriterReportsWriteError(t *testing.T) {
	underlying := newIdleWriteTestWriter()
	writeErr := errors.New("write failed")
	underlying.writeErr = writeErr
	errorSeen := make(chan error, 1)
	writer, err := NewIdleWriteWriter(underlying, IdleWriteOptions{
		Timeout:      time.Second,
		Cancel:       func() {},
		OnWriteError: func(err error) { errorSeen <- err },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("failure")); !errors.Is(err, writeErr) {
		t.Fatalf("Write error = %v, want %v", err, writeErr)
	}
	select {
	case got := <-errorSeen:
		if !errors.Is(got, writeErr) {
			t.Fatalf("callback error = %v, want %v", got, writeErr)
		}
	case <-time.After(time.Second):
		t.Fatal("write error callback did not run")
	}
	_ = writer.Finish()
}

type writerToSource struct{}

func (writerToSource) Read([]byte) (int, error) { return 0, io.EOF }

func (writerToSource) WriteTo(dst io.Writer) (int64, error) {
	n, err := dst.Write([]byte("writer-to"))
	return int64(n), err
}

func TestIdleWriteWriterDoesNotBypassWriteWithWriterTo(t *testing.T) {
	underlying := newIdleWriteTestWriter()
	writer, err := NewIdleWriteWriter(underlying, idleWriteOptions(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(writer, writerToSource{}); err != nil {
		t.Fatal(err)
	}
	if got := underlying.body.String(); got != "writer-to" {
		t.Fatalf("body = %q, want writer-to", got)
	}
	_ = writer.Finish()
}

func TestIdleWriteWriterRejectsUnreachableResponseWriter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	_, err := NewIdleWriteWriter(c.Writer, idleWriteOptions(time.Second))
	if !errors.Is(err, ErrIdleWriteWriterUnreachable) {
		t.Fatalf("NewIdleWriteWriter error = %v, want unreachable writer", err)
	}
}

func TestIdleWriteWriterRequiresCancellation(t *testing.T) {
	_, err := NewIdleWriteWriter(newIdleWriteTestWriter(), IdleWriteOptions{Timeout: time.Second})
	if !errors.Is(err, ErrIdleWriteCancelRequired) {
		t.Fatalf("NewIdleWriteWriter error = %v, want cancel-required error", err)
	}
}

func TestIdleWriteWriterRejectsShortWrites(t *testing.T) {
	for name, write := range map[string]func(*IdleWriteWriter) (int, error){
		"bytes":  func(w *IdleWriteWriter) (int, error) { return w.Write([]byte("short")) },
		"string": func(w *IdleWriteWriter) (int, error) { return w.WriteString("short") },
	} {
		t.Run(name, func(t *testing.T) {
			underlying := newIdleWriteTestWriter()
			underlying.shortWrite = true
			errorSeen := make(chan error, 1)
			writer, err := NewIdleWriteWriter(underlying, IdleWriteOptions{
				Timeout:      time.Second,
				Cancel:       func() {},
				OnWriteError: func(err error) { errorSeen <- err },
			})
			if err != nil {
				t.Fatal(err)
			}
			if n, err := write(writer); n != 4 || !errors.Is(err, io.ErrShortWrite) {
				t.Fatalf("Write = (%d, %v), want (4, io.ErrShortWrite)", n, err)
			}
			select {
			case err := <-errorSeen:
				if !errors.Is(err, io.ErrShortWrite) {
					t.Fatalf("write callback error = %v, want io.ErrShortWrite", err)
				}
			case <-time.After(time.Second):
				t.Fatal("short write callback did not run")
			}
			if !errors.Is(writer.Err(), io.ErrShortWrite) {
				t.Fatalf("writer error = %v, want io.ErrShortWrite", writer.Err())
			}
		})
	}
}

func TestIdleWriteWriterTreatsEmptyWriteAsProgress(t *testing.T) {
	writer, err := NewIdleWriteWriter(newIdleWriteTestWriter(), idleWriteOptions(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n, err := writer.Write(nil); n != 0 || err != nil {
		t.Fatalf("empty Write = (%d, %v), want (0, nil)", n, err)
	}
	writer.mu.Lock()
	timerStarted := writer.timer != nil
	writer.mu.Unlock()
	if !timerStarted {
		t.Fatal("empty successful write did not start the idle-progress timer")
	}
	if err := writer.Finish(); err != nil {
		t.Fatal(err)
	}
}

func TestIdleWriteWriterIgnoresStaleTimerCallbacks(t *testing.T) {
	timedOut := 0
	writer, err := NewIdleWriteWriter(newIdleWriteTestWriter(), IdleWriteOptions{
		Timeout: time.Hour,
		Cancel:  func() {},
		OnTimeout: func() {
			timedOut++
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("first")); err != nil {
		t.Fatal(err)
	}
	firstGeneration := writer.testGeneration()
	if _, err := writer.Write([]byte("second")); err != nil {
		t.Fatal(err)
	}
	secondGeneration := writer.testGeneration()
	if secondGeneration == firstGeneration {
		t.Fatal("successful progress did not advance timer generation")
	}

	writer.expire(firstGeneration)
	if err := writer.Err(); err != nil {
		t.Fatalf("stale timer callback failed healthy writer: %v", err)
	}
	if timedOut != 0 {
		t.Fatalf("stale timer callback ran timeout callback %d time(s)", timedOut)
	}

	writer.expire(secondGeneration)
	if !errors.Is(writer.Err(), ErrIdleWriteTimeout) {
		t.Fatalf("current timer callback error = %v, want idle timeout", writer.Err())
	}
	if timedOut != 1 {
		t.Fatalf("current timer callback ran %d time(s), want 1", timedOut)
	}
}

func TestIdleWriteWriterFinishInvalidatesTimerCallbacks(t *testing.T) {
	timedOut := 0
	writer, err := NewIdleWriteWriter(newIdleWriteTestWriter(), IdleWriteOptions{
		Timeout: time.Hour,
		Cancel:  func() {},
		OnTimeout: func() {
			timedOut++
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("complete")); err != nil {
		t.Fatal(err)
	}
	generation := writer.testGeneration()
	if err := writer.Finish(); err != nil {
		t.Fatalf("Finish = %v, want nil", err)
	}
	writer.expire(generation)
	if err := writer.Err(); err != nil {
		t.Fatalf("late timer callback changed completed writer error to %v", err)
	}
	if timedOut != 0 {
		t.Fatalf("late timer callback ran timeout callback %d time(s)", timedOut)
	}
}

func TestIdleWriteWriterFinishClearsDeadlineWithoutHoldingLifecycleLock(t *testing.T) {
	underlying := newIdleWriteTestWriter()
	var writer *IdleWriteWriter
	underlying.deadlineHook = func(deadline time.Time) {
		if deadline.IsZero() && writer != nil {
			_ = writer.Err()
		}
	}
	var err error
	writer, err = NewIdleWriteWriter(underlying, idleWriteOptions(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- writer.Finish() }()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("Finish = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Finish held the lifecycle lock while clearing the deadline")
	}
}

type deadlineGinWriter struct {
	gin.ResponseWriter
	deadlines []time.Time
}

func (w *deadlineGinWriter) SetWriteDeadline(deadline time.Time) error {
	w.deadlines = append(w.deadlines, deadline)
	return nil
}

func TestIdleWriteWriterWriteHeaderDoesNotStartTimeoutBeforeGinCommits(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	underlying := &deadlineGinWriter{ResponseWriter: c.Writer}
	cancelled := make(chan struct{})
	writer, err := NewIdleWriteWriter(underlying, IdleWriteOptions{
		Timeout: 30 * time.Millisecond,
		Cancel:  func() { close(cancelled) },
	})
	if err != nil {
		t.Fatal(err)
	}
	writer.WriteHeader(http.StatusOK)
	if c.Writer.Written() {
		t.Fatal("Gin committed headers after deferred WriteHeader")
	}
	if writer.testTimerRunning() {
		t.Fatal("deferred Gin WriteHeader started an idle-progress timer")
	}
	if deadline := writer.testProgressDeadline(); !deadline.IsZero() {
		t.Fatalf("progress deadline = %v, want zero before output", deadline)
	}
	if got := underlying.deadlines[len(underlying.deadlines)-1]; !got.IsZero() {
		t.Fatalf("final deadline = %v, want cleared", got)
	}
	select {
	case <-cancelled:
		t.Fatal("deferred Gin WriteHeader cancelled before output")
	case <-time.After(90 * time.Millisecond):
	}
	if err := writer.Finish(); err != nil {
		t.Fatal(err)
	}
}

func TestIdleWriteWriterWriteHeaderStartsTimeoutAfterImmediateCommit(t *testing.T) {
	underlying := newIdleWriteTestWriter()
	writer, err := NewIdleWriteWriter(underlying, idleWriteOptions(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	writer.WriteHeader(http.StatusCreated)
	if !underlying.Written() {
		t.Fatal("immediate WriteHeader did not commit output")
	}
	if !writer.testTimerRunning() {
		t.Fatal("immediate WriteHeader did not start the idle-progress timer")
	}
	if deadline := writer.testProgressDeadline(); deadline.IsZero() {
		t.Fatal("immediate WriteHeader did not set a progress deadline")
	}
	if err := writer.Finish(); err != nil {
		t.Fatal(err)
	}
}

func TestIdleWriteWriterFlushPreservesDeferredGinStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	underlying := &deadlineGinWriter{ResponseWriter: c.Writer}
	writer, err := NewIdleWriteWriter(underlying, idleWriteOptions(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	writer.WriteHeader(http.StatusPartialContent)
	if c.Writer.Written() {
		t.Fatal("Gin committed headers before Flush")
	}
	if err := writer.FlushError(); err != nil {
		t.Fatal(err)
	}
	if !c.Writer.Written() {
		t.Fatal("FlushError did not commit Gin headers")
	}
	if recorder.Code != http.StatusPartialContent {
		t.Fatalf("wire status = %d, want %d", recorder.Code, http.StatusPartialContent)
	}
	if err := writer.Finish(); err != nil {
		t.Fatal(err)
	}
}

func TestIdleWriteWriterUsesLastProgressDeadlineForNextWrite(t *testing.T) {
	underlying := newIdleWriteTestWriter()
	writer, err := NewIdleWriteWriter(underlying, idleWriteOptions(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("first")); err != nil {
		t.Fatal(err)
	}
	lastProgressDeadline := writer.testProgressDeadline()
	if lastProgressDeadline.IsZero() {
		t.Fatal("first write did not set a progress deadline")
	}
	deadlineCalls := len(underlying.deadlines)
	if _, err := writer.Write([]byte("second")); err != nil {
		t.Fatal(err)
	}
	if got := underlying.deadlines[deadlineCalls]; !got.Equal(lastProgressDeadline) {
		t.Fatalf("next write deadline = %v, want last progress deadline %v", got, lastProgressDeadline)
	}
	if err := writer.Finish(); err != nil {
		t.Fatal(err)
	}
}

func TestIdleWriteWriterWriteHeaderNowAfterOutputDoesNotExtendDeadline(t *testing.T) {
	underlying := newIdleWriteTestWriter()
	writer, err := NewIdleWriteWriter(underlying, idleWriteOptions(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("body")); err != nil {
		t.Fatal(err)
	}
	progressDeadline := writer.testProgressDeadline()
	deadlineCalls := len(underlying.deadlines)
	writer.WriteHeaderNow()
	if got := writer.testProgressDeadline(); !got.Equal(progressDeadline) {
		t.Fatalf("progress deadline = %v, want unchanged %v", got, progressDeadline)
	}
	if got := len(underlying.deadlines); got != deadlineCalls {
		t.Fatalf("deadline calls = %d, want %d after no-op WriteHeaderNow", got, deadlineCalls)
	}
	if err := writer.Finish(); err != nil {
		t.Fatal(err)
	}
}

func TestIdleWriteWriterRejectsWriteAfterProgressDeadline(t *testing.T) {
	underlying := newIdleWriteTestWriter()
	cancelled := make(chan struct{})
	writer, err := NewIdleWriteWriter(underlying, IdleWriteOptions{
		Timeout: time.Second,
		Cancel:  func() { close(cancelled) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("first")); err != nil {
		t.Fatal(err)
	}
	writer.mu.Lock()
	writer.timer.Stop()
	writer.timerGeneration++
	writer.progressDeadline = time.Now().Add(-time.Millisecond)
	writer.mu.Unlock()
	if n, err := writer.Write([]byte("late")); n != 0 || !errors.Is(err, ErrIdleWriteTimeout) {
		t.Fatalf("late Write = (%d, %v), want (0, idle timeout)", n, err)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("expired progress deadline did not cancel the request")
	}
	if err := writer.Finish(); !errors.Is(err, ErrIdleWriteTimeout) {
		t.Fatalf("Finish = %v, want idle timeout", err)
	}
}

func TestIdleWriteWriterReachesRealTCPConnection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	timedOut := make(chan struct{})
	router.GET("/", func(c *gin.Context) {
		_, cancel := context.WithCancel(c.Request.Context())
		defer cancel()
		writer, err := NewIdleWriteWriter(c.Writer, IdleWriteOptions{
			Timeout: 100 * time.Millisecond,
			Cancel:  cancel,
			OnTimeout: func() {
				select {
				case <-timedOut:
				default:
					close(timedOut)
				}
			},
		})
		if err != nil {
			c.String(http.StatusInternalServerError, "%v", err)
			return
		}
		c.Writer = writer
		defer writer.Finish()
		chunk := bytes.Repeat([]byte("x"), 64*1024)
		for i := 0; i < 1024; i++ {
			if _, err := c.Writer.Write(chunk); err != nil {
				return
			}
		}
	})

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	conn, err := net.Dial("tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := fmt.Fprintf(conn, "GET / HTTP/1.1\r\nHost: idle-write-test\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	select {
	case <-timedOut:
	case <-time.After(2 * time.Second):
		t.Fatal("slow TCP client did not trigger idle write timeout")
	}
}
