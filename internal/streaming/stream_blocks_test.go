package streaming

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func init() { gin.SetMode(gin.TestMode) }

// trackingReader is an io.ReadCloser that counts how many times it was opened
// (handed out by the stub) and closed, so a test can assert the two balance —
// every reader StreamBlocks opens is closed exactly once, including one prefetched
// a block ahead but never streamed (F11).
type trackingReader struct {
	io.Reader
	opens  atomic.Int32
	closes atomic.Int32
}

func (r *trackingReader) Close() error {
	r.closes.Add(1)
	return nil
}

func newTrackingReader(data string) *trackingReader {
	return &trackingReader{Reader: bytes.NewReader([]byte(data))}
}

// balanced reports whether the reader was closed exactly as many times as it was
// opened — no leak and no double-close. A reader that was never opened (0 == 0) is
// balanced: with a cancelable prefetch, an abandoned block may legitimately never be
// opened, and that is not a leak.
func (r *trackingReader) balanced() bool {
	return r.opens.Load() == r.closes.Load()
}

// stubBlockReader serves a per-block reader/data/error from maps and records the
// readers it handed out so a test can assert each one was closed. readerFuncs lets
// a test install ctx-aware behavior for a specific block (e.g. block until the fetch
// context is canceled), which the static maps cannot express.
type stubBlockReader struct {
	mu             sync.Mutex
	readers        map[string]*trackingReader
	readerErr      map[string]error
	readerFuncs    map[string]func(context.Context) (io.ReadCloser, error)
	data           map[string][]byte
	getReaderCalls atomic.Int32
}

func (s *stubBlockReader) GetBlock(_ context.Context, hash string) ([]byte, error) {
	if d, ok := s.data[hash]; ok {
		return d, nil
	}
	return nil, errors.New("no data for " + hash)
}

func (s *stubBlockReader) GetBlockReader(ctx context.Context, hash string) (io.ReadCloser, error) {
	s.getReaderCalls.Add(1)
	s.mu.Lock()
	fn := s.readerFuncs[hash]
	s.mu.Unlock()
	if fn != nil {
		return fn(ctx)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.readerErr[hash]; err != nil {
		return nil, err
	}
	if r, ok := s.readers[hash]; ok {
		r.opens.Add(1)
		return r, nil
	}
	return nil, errors.New("no reader for " + hash)
}

func (s *stubBlockReader) GetBlockSize(context.Context, string) (int64, error) {
	return 0, errors.New("not implemented")
}

// gatedPanicWriter panics on its first Write, but only after `gate` is closed, so a
// test can guarantee the next block was already prefetched and open before the copy
// path panics — proving the prefetched reader is drained and closed during a panic.
type gatedPanicWriter struct {
	header http.Header
	gate   <-chan struct{}
}

func (w *gatedPanicWriter) Header() http.Header {
	if w.header == nil {
		w.header = http.Header{}
	}
	return w.header
}
func (w *gatedPanicWriter) WriteHeader(int) {}
func (w *gatedPanicWriter) Write([]byte) (int, error) {
	<-w.gate
	panic("client gone: write panicked after prefetch")
}
func (w *gatedPanicWriter) Flush() {}

// gatedFailingWriter fails its first Write, but only after `gate` is closed, so a
// test can guarantee the next block was already prefetched (and is sitting open in
// the buffer) before the stream is abandoned.
type gatedFailingWriter struct {
	header http.Header
	gate   <-chan struct{}
}

func (w *gatedFailingWriter) Header() http.Header {
	if w.header == nil {
		w.header = http.Header{}
	}
	return w.header
}
func (w *gatedFailingWriter) WriteHeader(int) {}
func (w *gatedFailingWriter) Write([]byte) (int, error) {
	<-w.gate
	return 0, errors.New("client gone: write failed after prefetch")
}
func (w *gatedFailingWriter) Flush() {}

// runStreamBlocksWithin runs fn (a StreamBlocks call, optionally wrapping its own
// recover) in a goroutine and fails fast if it does not return within 5s. The gated
// writers block their Write on a channel the next block's prefetch is expected to
// open; a regression that stops that prefetch would otherwise hang the test to the
// global go test timeout instead of failing here.
func runStreamBlocksWithin(t *testing.T, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("StreamBlocks did not return within 5s (a gated write or drain likely stalled)")
	}
}

// TestStreamBlocks_ClosesPrefetchedReaderOnWriteError is the F11 regression: when
// a write fails while streaming block i, the block i+1 already prefetched a block
// ahead must not be dropped with its S3 reader still open.
func TestStreamBlocks_ClosesPrefetchedReaderOnWriteError(t *testing.T) {
	b0 := newTrackingReader("block-zero-bytes")
	b1 := newTrackingReader("block-one-bytes")
	b1Opened := make(chan struct{})
	stub := &stubBlockReader{
		readers: map[string]*trackingReader{"b0": b0},
		readerFuncs: map[string]func(context.Context) (io.ReadCloser, error){
			"b1": func(context.Context) (io.ReadCloser, error) {
				b1.opens.Add(1)
				close(b1Opened)
				return b1, nil
			},
		},
	}

	// Block 0's write fails only after block 1 has been prefetched, so the
	// abandonment deterministically leaves block 1's reader open in the buffer —
	// exactly the F11 leak the drain must reclaim.
	c, _ := gin.CreateTestContext(&gatedFailingWriter{gate: b1Opened})
	runStreamBlocksWithin(t, func() {
		StreamBlocks(c, context.Background(), stub, []string{"b0", "b1"}, nil, nil, "test")
	})

	if b0.opens.Load() != 1 || b0.closes.Load() != 1 {
		t.Errorf("streamed reader: open=%d close=%d, want 1/1", b0.opens.Load(), b0.closes.Load())
	}
	if b1.opens.Load() != 1 || b1.closes.Load() != 1 {
		t.Errorf("prefetched-but-abandoned reader: open=%d close=%d, want 1/1 (F11 leak)", b1.opens.Load(), b1.closes.Load())
	}
}

// TestStreamBlocks_ClosesReadersEvenIfWritePanics proves the "closed on every path"
// clause: if the copy path panics rather than erroring, both the block being
// streamed and the block prefetched one ahead are still closed exactly once. This
// fails against an inline-close implementation, where a panic skips the current
// reader's close.
func TestStreamBlocks_ClosesReadersEvenIfWritePanics(t *testing.T) {
	b0 := newTrackingReader("block-zero-bytes")
	b1 := newTrackingReader("block-one-bytes")
	b1Opened := make(chan struct{})
	stub := &stubBlockReader{
		readers: map[string]*trackingReader{"b0": b0},
		readerFuncs: map[string]func(context.Context) (io.ReadCloser, error){
			"b1": func(context.Context) (io.ReadCloser, error) {
				b1.opens.Add(1)
				close(b1Opened)
				return b1, nil
			},
		},
	}

	// Panic only after block 1 is prefetched and open, so the test proves the
	// prefetched reader is drained and closed during a panic — not only the current.
	c, _ := gin.CreateTestContext(&gatedPanicWriter{gate: b1Opened})
	runStreamBlocksWithin(t, func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("expected StreamBlocks to propagate the writer panic")
			}
		}()
		StreamBlocks(c, context.Background(), stub, []string{"b0", "b1"}, nil, nil, "test")
	})

	if b0.opens.Load() != 1 || b0.closes.Load() != 1 {
		t.Errorf("streamed reader on panic: open=%d close=%d, want 1/1", b0.opens.Load(), b0.closes.Load())
	}
	if b1.opens.Load() != 1 || b1.closes.Load() != 1 {
		t.Errorf("prefetched reader on panic: open=%d close=%d, want 1/1", b1.opens.Load(), b1.closes.Load())
	}
}

// TestStreamBlocks_BlockFetchErrorDoesNotPrefetchOrLeakNext covers a current-block
// fetch error: the stream ends there, so the next block must never be prefetched (no
// wasted S3 open, nothing to cancel or drain) and nothing is leaked.
func TestStreamBlocks_BlockFetchErrorDoesNotPrefetchOrLeakNext(t *testing.T) {
	b1 := newTrackingReader("block-one-bytes")
	stub := &stubBlockReader{
		readers:   map[string]*trackingReader{"b1": b1},
		readerErr: map[string]error{"b0": errors.New("s3 get failed")},
	}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	StreamBlocks(c, context.Background(), stub, []string{"b0", "b1"}, nil, nil, "test")

	if b1.opens.Load() != 0 {
		t.Errorf("next block prefetched %d times after a current-block error, want 0", b1.opens.Load())
	}
	if !b1.balanced() {
		t.Errorf("next block reader leaked: open=%d close=%d", b1.opens.Load(), b1.closes.Load())
	}
}

// TestStreamBlocks_DoesNotBlockOnAbandonedInFlightPrefetch proves the cleanup
// cancels a prefetch still in flight BEFORE draining it. Block 1's fetch blocks
// until its context is canceled (modeling a slow/hung S3 GetObject); block 0's write
// then fails, abandoning the stream. StreamBlocks must return promptly — the drain
// must not wait out the fetch — and the canceled fetch must leave no open reader.
// With an uncancelable drain this hangs, since the test's context is never canceled.
func TestStreamBlocks_DoesNotBlockOnAbandonedInFlightPrefetch(t *testing.T) {
	b0 := newTrackingReader("block-zero-bytes")
	b1Started := make(chan struct{})
	var b1Canceled atomic.Bool
	stub := &stubBlockReader{
		readers: map[string]*trackingReader{"b0": b0},
		readerFuncs: map[string]func(context.Context) (io.ReadCloser, error){
			"b1": func(ctx context.Context) (io.ReadCloser, error) {
				close(b1Started) // block 1's fetch is now genuinely in flight
				<-ctx.Done()     // hang until StreamBlocks cancels the prefetch
				b1Canceled.Store(true)
				return nil, ctx.Err()
			},
		},
	}

	// Block 0's write fails only once block 1's fetch has entered GetBlockReader, so
	// the abandonment deterministically happens with a fetch genuinely in flight —
	// otherwise a fast cancel could skip opening block 1 and the test would be racy.
	c, _ := gin.CreateTestContext(&gatedFailingWriter{gate: b1Started})

	done := make(chan struct{})
	go func() {
		StreamBlocks(c, context.Background(), stub, []string{"b0", "b1"}, nil, nil, "test")
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("StreamBlocks hung draining a prefetch it never canceled")
	}

	if !b1Canceled.Load() {
		t.Error("block 1 prefetch was not canceled on abandonment")
	}
	if got := b0.closes.Load(); got != 1 {
		t.Errorf("streamed reader closed %d times, want 1", got)
	}
}

// TestStreamBlocks_HappyPathClosesEveryReaderOnce proves the fix does not
// double-close or hang on the normal path: every reader is closed exactly once,
// the body is the concatenation of all blocks, and the call returns.
func TestStreamBlocks_HappyPathClosesEveryReaderOnce(t *testing.T) {
	readers := map[string]*trackingReader{
		"b0": newTrackingReader("AAAA"),
		"b1": newTrackingReader("BBBB"),
		"b2": newTrackingReader("CCCC"),
	}
	stub := &stubBlockReader{readers: readers}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	done := make(chan struct{})
	go func() {
		StreamBlocks(c, context.Background(), stub, []string{"b0", "b1", "b2"}, nil, nil, "test")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("StreamBlocks did not return (deferred drain hung on an already-consumed channel)")
	}

	for id, r := range readers {
		if got := r.closes.Load(); got != 1 {
			t.Errorf("block %s reader closed %d times, want exactly 1", id, got)
		}
		if !r.balanced() {
			t.Errorf("block %s opens!=closes: open=%d close=%d", id, r.opens.Load(), r.closes.Load())
		}
	}
	if body := rec.Body.String(); body != "AAAABBBBCCCC" {
		t.Errorf("streamed body = %q, want %q", body, "AAAABBBBCCCC")
	}
}

// TestPrefetchBlock_SkipsFetchWhenContextAlreadyCanceled verifies the
// context-aware delivery: an already-abandoned stream must not initiate a new S3
// fetch, and the delivered result carries the context error.
func TestPrefetchBlock_SkipsFetchWhenContextAlreadyCanceled(t *testing.T) {
	stub := &stubBlockReader{readers: map[string]*trackingReader{"b0": newTrackingReader("x")}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	res := <-PrefetchBlock(ctx, stub, "b0", nil, nil)
	if res.Err == nil {
		t.Fatal("PrefetchBlock delivered nil error for a canceled context")
	}
	if res.Reader != nil {
		t.Error("PrefetchBlock returned an open reader for a canceled context")
	}
	if n := stub.getReaderCalls.Load(); n != 0 {
		t.Errorf("GetBlockReader called %d times on a canceled context, want 0", n)
	}
}
