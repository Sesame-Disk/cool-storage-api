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

func TestIdleWriteWriterTracksProgressAndClearsDeadline(t *testing.T) {
	underlying := newIdleWriteTestWriter()
	writer, err := NewIdleWriteWriter(underlying, IdleWriteOptions{Timeout: 100 * time.Millisecond})
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
	if !errors.Is(writer.Err(), ErrIdleWriteTimeout) {
		t.Fatalf("writer error = %v, want idle timeout", writer.Err())
	}
	if ctx.Err() == nil {
		t.Fatal("idle timeout did not cancel the request context")
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
	writer, err := NewIdleWriteWriter(underlying, IdleWriteOptions{Timeout: time.Second})
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
	_, err := NewIdleWriteWriter(c.Writer, IdleWriteOptions{Timeout: time.Second})
	if !errors.Is(err, ErrIdleWriteWriterUnreachable) {
		t.Fatalf("NewIdleWriteWriter error = %v, want unreachable writer", err)
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
