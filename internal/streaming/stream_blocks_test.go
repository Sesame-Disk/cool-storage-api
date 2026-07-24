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

// trackingReader is an io.ReadCloser that records how many times Close was
// called, so a test can prove StreamBlocks closes every reader it opens —
// including one prefetched a block ahead but never streamed (F11).
type trackingReader struct {
	io.Reader
	closes atomic.Int32
}

func (r *trackingReader) Close() error {
	r.closes.Add(1)
	return nil
}

func newTrackingReader(data string) *trackingReader {
	return &trackingReader{Reader: bytes.NewReader([]byte(data))}
}

// stubBlockReader serves a per-block reader/data/error from maps and records the
// readers it handed out so a test can assert each one was closed.
type stubBlockReader struct {
	mu             sync.Mutex
	readers        map[string]*trackingReader
	readerErr      map[string]error
	data           map[string][]byte
	getReaderCalls atomic.Int32
}

func (s *stubBlockReader) GetBlock(_ context.Context, hash string) ([]byte, error) {
	if d, ok := s.data[hash]; ok {
		return d, nil
	}
	return nil, errors.New("no data for " + hash)
}

func (s *stubBlockReader) GetBlockReader(_ context.Context, hash string) (io.ReadCloser, error) {
	s.getReaderCalls.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.readerErr[hash]; err != nil {
		return nil, err
	}
	if r, ok := s.readers[hash]; ok {
		return r, nil
	}
	return nil, errors.New("no reader for " + hash)
}

func (s *stubBlockReader) GetBlockSize(context.Context, string) (int64, error) {
	return 0, errors.New("not implemented")
}

// failingWriter is an http.ResponseWriter whose Write always errors, used to
// drive StreamBlocks' mid-stream copy-failure exit path.
type failingWriter struct{ header http.Header }

func (w *failingWriter) Header() http.Header {
	if w.header == nil {
		w.header = http.Header{}
	}
	return w.header
}
func (w *failingWriter) WriteHeader(int)          {}
func (w *failingWriter) Write([]byte) (int, error) { return 0, errors.New("client gone: write failed") }
func (w *failingWriter) Flush()                   {}

// TestStreamBlocks_ClosesPrefetchedReaderOnWriteError is the F11 regression: when
// a write fails while streaming block i, the block i+1 already prefetched a block
// ahead must not be dropped with its S3 reader still open.
func TestStreamBlocks_ClosesPrefetchedReaderOnWriteError(t *testing.T) {
	b0 := newTrackingReader("block-zero-bytes")
	b1 := newTrackingReader("block-one-bytes")
	stub := &stubBlockReader{readers: map[string]*trackingReader{
		"b0": b0,
		"b1": b1,
	}}

	c, _ := gin.CreateTestContext(&failingWriter{})
	StreamBlocks(c, context.Background(), stub, []string{"b0", "b1"}, nil, nil, "test")

	if got := b0.closes.Load(); got != 1 {
		t.Errorf("streamed block reader closed %d times, want 1", got)
	}
	if got := b1.closes.Load(); got != 1 {
		t.Errorf("prefetched-but-abandoned block reader closed %d times, want 1 (F11 leak)", got)
	}
}

// TestStreamBlocks_ClosesPrefetchedReaderOnBlockError covers the other early exit:
// block i fails to fetch, so its result carries an error, but block i+1 was
// already prefetched and its reader must still be closed.
func TestStreamBlocks_ClosesPrefetchedReaderOnBlockError(t *testing.T) {
	b1 := newTrackingReader("block-one-bytes")
	stub := &stubBlockReader{
		readers:   map[string]*trackingReader{"b1": b1},
		readerErr: map[string]error{"b0": errors.New("s3 get failed")},
	}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	StreamBlocks(c, context.Background(), stub, []string{"b0", "b1"}, nil, nil, "test")

	if got := b1.closes.Load(); got != 1 {
		t.Errorf("prefetched reader after a block error closed %d times, want 1 (F11 leak)", got)
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
