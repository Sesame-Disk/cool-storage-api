package httputil

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

// The idle interval used to begin only at the first successful output. That left
// the span between admission's phase change and the first byte unbounded: the
// preparation deadline is already over and no write has happened yet, so a
// stalled first storage read held its slot until the client disconnected or the
// storage SDK gave up. These regressions cover the interval opening earlier and
// surviving the one event that used to erase it.

// deferredHeaderTestWriter mirrors Gin's responseWriter: WriteHeader records the
// status without committing, so Written() stays false. That is the exact shape
// that let c.Status(200) clear the interval a moment after it was armed.
type deferredHeaderTestWriter struct {
	*idleWriteTestWriter
}

func (w *deferredHeaderTestWriter) WriteHeader(status int) {
	w.status = status
}

func (w *deferredHeaderTestWriter) WriteHeaderNow() {
	w.idleWriteTestWriter.WriteHeader(w.status)
}

func newDeferredHeaderTestWriter() *deferredHeaderTestWriter {
	return &deferredHeaderTestWriter{idleWriteTestWriter: newIdleWriteTestWriter()}
}

func TestIdleWriteWriterStartIdleIntervalArmsBeforeAnyOutput(t *testing.T) {
	base := newIdleWriteTestWriter()
	writer, err := NewIdleWriteWriter(base, idleWriteOptions(time.Minute))
	if err != nil {
		t.Fatalf("new idle write writer: %v", err)
	}
	t.Cleanup(func() { _ = writer.Finish() })

	if writer.testTimerRunning() {
		t.Fatal("timer armed by the constructor; the reachability probe must not start the interval")
	}
	// Ignore the constructor's reachability probe, which deliberately sets and
	// then clears a deadline.
	afterProbe := len(base.deadlines)
	if err := writer.StartIdleInterval(); err != nil {
		t.Fatalf("start idle interval: %v", err)
	}
	if !writer.testTimerRunning() {
		t.Fatal("StartIdleInterval did not arm the timer")
	}
	if writer.testProgressDeadline().IsZero() {
		t.Fatal("StartIdleInterval did not set an absolute deadline")
	}
	// The interval must cost nothing on the wire: no output, and no socket
	// deadline until a write actually begins.
	if base.body.Len() != 0 || base.Written() {
		t.Fatal("StartIdleInterval committed output")
	}
	for _, deadline := range base.deadlines[afterProbe:] {
		if !deadline.IsZero() {
			t.Fatalf("StartIdleInterval installed socket deadline %v; beforeWrite owns that", deadline)
		}
	}
}

func TestIdleWriteWriterStartIdleIntervalIsIdempotent(t *testing.T) {
	writer, err := NewIdleWriteWriter(newIdleWriteTestWriter(), idleWriteOptions(time.Minute))
	if err != nil {
		t.Fatalf("new idle write writer: %v", err)
	}
	t.Cleanup(func() { _ = writer.Finish() })

	if err := writer.StartIdleInterval(); err != nil {
		t.Fatalf("first arm: %v", err)
	}
	first := writer.testProgressDeadline()
	generation := writer.testGeneration()

	if err := writer.StartIdleInterval(); err != nil {
		t.Fatalf("second arm: %v", err)
	}
	if got := writer.testProgressDeadline(); !got.Equal(first) {
		t.Fatalf("deadline moved from %v to %v; a second arm must not extend the interval", first, got)
	}
	if got := writer.testGeneration(); got != generation {
		t.Fatalf("generation moved from %d to %d; a second arm must not leave two timers", generation, got)
	}
}

func TestIdleWriteWriterDeferredHeaderPreservesIdleInterval(t *testing.T) {
	base := newDeferredHeaderTestWriter()
	writer, err := NewIdleWriteWriter(base, idleWriteOptions(time.Minute))
	if err != nil {
		t.Fatalf("new idle write writer: %v", err)
	}
	t.Cleanup(func() { _ = writer.Finish() })

	if err := writer.StartIdleInterval(); err != nil {
		t.Fatalf("start idle interval: %v", err)
	}
	armed := writer.testProgressDeadline()

	// This is c.Status(200): recorded, not committed.
	writer.WriteHeader(http.StatusOK)

	if base.Written() {
		t.Fatal("test writer committed the header; this case must exercise the deferred path")
	}
	if !writer.testTimerRunning() {
		t.Fatal("a deferred header cleared the idle interval; the pre-first-write gap is reopened")
	}
	if got := writer.testProgressDeadline(); !got.Equal(armed) {
		t.Fatalf("deadline moved from %v to %v; a deferred header is not progress and must not extend it", armed, got)
	}
	// The socket deadline beforeWrite installed must still be undone: no write is
	// in flight, and a keep-alive connection would otherwise inherit it.
	if last := base.deadlines[len(base.deadlines)-1]; !last.IsZero() {
		t.Fatalf("socket deadline left at %v after a non-committing header", last)
	}
}

func TestIdleWriteWriterDeferredHeaderBeforeArmingLeavesNoTimer(t *testing.T) {
	base := newDeferredHeaderTestWriter()
	writer, err := NewIdleWriteWriter(base, idleWriteOptions(time.Minute))
	if err != nil {
		t.Fatalf("new idle write writer: %v", err)
	}
	t.Cleanup(func() { _ = writer.Finish() })

	writer.WriteHeader(http.StatusOK)

	if writer.testTimerRunning() {
		t.Fatal("a deferred header armed a timer without an interval having been started")
	}
	if !writer.testProgressDeadline().IsZero() {
		t.Fatal("a deferred header set a deadline without an interval having been started")
	}
}

func TestIdleWriteWriterFirstProgressReplacesInitialInterval(t *testing.T) {
	writer, err := NewIdleWriteWriter(newIdleWriteTestWriter(), idleWriteOptions(time.Minute))
	if err != nil {
		t.Fatalf("new idle write writer: %v", err)
	}
	t.Cleanup(func() { _ = writer.Finish() })

	if err := writer.StartIdleInterval(); err != nil {
		t.Fatalf("start idle interval: %v", err)
	}
	armed := writer.testProgressDeadline()
	armedGeneration := writer.testGeneration()

	time.Sleep(5 * time.Millisecond)
	if _, err := writer.Write([]byte("first")); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Progress restarts the interval from the progress instant, so the total span
	// since StartIdleInterval may approach 2T. That is correct — there was real
	// progress. What must not survive is the original callback.
	if got := writer.testProgressDeadline(); !got.After(armed) {
		t.Fatalf("deadline %v did not move past the armed %v after real progress", got, armed)
	}
	if got := writer.testGeneration(); got == armedGeneration {
		t.Fatal("generation unchanged after progress; the initial timer callback is still live")
	}
}

func TestIdleWriteWriterFinishBeforeFirstByteClearsInterval(t *testing.T) {
	base := newIdleWriteTestWriter()
	writer, err := NewIdleWriteWriter(base, idleWriteOptions(time.Minute))
	if err != nil {
		t.Fatalf("new idle write writer: %v", err)
	}
	if err := writer.StartIdleInterval(); err != nil {
		t.Fatalf("start idle interval: %v", err)
	}
	if err := writer.Finish(); err != nil {
		t.Fatalf("finish: %v", err)
	}

	if writer.testTimerRunning() {
		t.Fatal("Finish left the initial timer running")
	}
	if !writer.testProgressDeadline().IsZero() {
		t.Fatal("Finish left an absolute deadline behind")
	}
	if last := base.deadlines[len(base.deadlines)-1]; !last.IsZero() {
		t.Fatalf("Finish left socket deadline %v; a keep-alive connection would inherit it", last)
	}
	// Arming after Finish must fail rather than resurrect the interval.
	if err := writer.StartIdleInterval(); err == nil {
		t.Fatal("StartIdleInterval succeeded after Finish")
	}
}

func TestIdleWriteWriterStartIdleIntervalCancelsWithoutAnyOutput(t *testing.T) {
	cancelled := make(chan struct{})
	timedOut := make(chan struct{})
	opts := IdleWriteOptions{
		Timeout:   30 * time.Millisecond,
		Cancel:    func() { close(cancelled) },
		OnTimeout: func() { close(timedOut) },
	}
	writer, err := NewIdleWriteWriter(newIdleWriteTestWriter(), opts)
	if err != nil {
		t.Fatalf("new idle write writer: %v", err)
	}
	t.Cleanup(func() { _ = writer.Finish() })

	if err := writer.StartIdleInterval(); err != nil {
		t.Fatalf("start idle interval: %v", err)
	}

	// No write ever happens: this is the stalled first storage read.
	select {
	case <-timedOut:
	case <-time.After(2 * time.Second):
		t.Fatal("idle interval never expired without output; a stalled first read would hold its slot indefinitely")
	}
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("idle timeout did not cancel the request context")
	}
	if err := writer.Err(); !errors.Is(err, ErrIdleWriteTimeout) {
		t.Fatalf("writer error = %v, want ErrIdleWriteTimeout", err)
	}
}
