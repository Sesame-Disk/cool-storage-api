package streaming

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http/httptest"
	"runtime"
	"runtime/debug"
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/crypto"
	"github.com/gin-gonic/gin"
)

// This benchmark answers, with a number instead of a guess, the question every
// download-admission cap divides into:
//
//	max_active_per_node = (memory budget for downloads) / (per-admission cost)
//
// Subcontract B measured the buffered PUT body the same way. The download side
// has two very different costs and one cap covering both, so both are measured:
//
//   - plaintext streaming holds a pooled 4 MiB copy buffer plus the object-store
//     readers for the current and prefetched block. It does not scale with file
//     size, so a 2 GiB file costs the same as a 20 MiB one.
//   - encrypted streaming does not stream at all inside the prefetch:
//     PrefetchBlock calls GetBlock and DecryptLibraryBlock, so an admitted
//     transfer holds the *decrypted* current block and the *decrypted* next
//     block at once, plus the encrypted source of the one being decrypted. That
//     scales with block size and is the figure that sizes the node cap.
//
// The peak is mid-stream, not at the end, so a reader pauses at a chosen block
// and the heap is sampled there rather than after the response completes.
//
// Run:
//
//	go test -run '^$' -bench BenchmarkDownloadStreamMemory -benchmem ./internal/streaming/

const downloadBenchBlockSize = 8 << 20 // the chunker's upper block size

// pausingBlockReader serves synthetic blocks and blocks the stream at pauseAt so
// the caller can sample the heap while an admitted transfer is at its peak.
type pausingBlockReader struct {
	plain     []byte
	encrypted []byte
	pauseAt   int
	served    int
	reached   chan struct{}
	release   chan struct{}
}

func (r *pausingBlockReader) maybePause() {
	r.served++
	if r.served == r.pauseAt {
		close(r.reached)
		<-r.release
	}
}

func (r *pausingBlockReader) GetBlock(context.Context, string) ([]byte, error) {
	r.maybePause()
	// The real store returns a fresh buffer per call; reusing one would hide the
	// per-admission retention this benchmark exists to measure.
	out := make([]byte, len(r.encrypted))
	copy(out, r.encrypted)
	return out, nil
}

func (r *pausingBlockReader) GetBlockReader(context.Context, string) (io.ReadCloser, error) {
	r.maybePause()
	return io.NopCloser(bytes.NewReader(r.plain)), nil
}

func (r *pausingBlockReader) GetBlockSize(context.Context, string) (int64, error) {
	return int64(len(r.plain)), nil
}

func BenchmarkDownloadStreamMemory(b *testing.B) {
	for _, tc := range []struct {
		name      string
		encrypted bool
	}{
		{"plaintext", false},
		{"encrypted", true},
	} {
		b.Run(tc.name, func(b *testing.B) {
			benchmarkDownloadStream(b, tc.encrypted)
		})
	}
}

func benchmarkDownloadStream(b *testing.B, encrypted bool) {
	gin.SetMode(gin.TestMode)

	plain := bytes.Repeat([]byte{0xA5}, downloadBenchBlockSize)
	var fileKey, fileIV, ciphertext []byte
	if encrypted {
		fileKey = bytes.Repeat([]byte{0x11}, crypto.FileKeySize)
		fileIV = bytes.Repeat([]byte{0x22}, crypto.IVSize)
		var err error
		ciphertext, err = crypto.EncryptBlockSeafile(plain, fileKey, fileIV)
		if err != nil {
			b.Fatalf("encrypt fixture block: %v", err)
		}
	}

	retained := measureStreamPeak(b, plain, ciphertext, fileKey, fileIV)

	b.ReportAllocs()
	b.SetBytes(int64(downloadBenchBlockSize) * 4)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		runDownloadStream(b, plain, ciphertext, fileKey, fileIV, 0, nil, nil)
	}
	b.StopTimer()

	b.ReportMetric(float64(retained), "peak-B/admission")
	b.ReportMetric(float64(retained)/float64(downloadBenchBlockSize), "peak/block")
}

// measureStreamPeak samples the heap while one transfer is parked mid-stream,
// with the collector off so nothing is reclaimed under the sample.
func measureStreamPeak(b *testing.B, plain, ciphertext, fileKey, fileIV []byte) uint64 {
	b.Helper()

	reached := make(chan struct{})
	release := make(chan struct{})

	prevGC := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(prevGC)
	runtime.GC()

	var before, during runtime.MemStats
	runtime.ReadMemStats(&before)

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Pause on the third served block: by then the pipeline is in steady
		// state, holding the block being written and the one prefetched ahead.
		runDownloadStream(b, plain, ciphertext, fileKey, fileIV, 3, reached, release)
	}()

	<-reached
	runtime.ReadMemStats(&during)
	close(release)
	<-done

	if during.HeapAlloc < before.HeapAlloc {
		return 0
	}
	return during.HeapAlloc - before.HeapAlloc
}

func runDownloadStream(b *testing.B, plain, ciphertext, fileKey, fileIV []byte, pauseAt int, reached, release chan struct{}) {
	b.Helper()

	if reached == nil {
		reached = make(chan struct{})
	}
	if release == nil {
		release = make(chan struct{})
		close(release)
	}
	reader := &pausingBlockReader{
		plain:     plain,
		encrypted: ciphertext,
		pauseAt:   pauseAt,
		reached:   reached,
		release:   release,
	}

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/download", nil)
	// Discard the body: this measures what the server holds, not what a client
	// buffer would add on top.
	c.Writer = &discardResponseWriter{ResponseWriter: c.Writer}

	ids := make([]string, 4)
	for i := range ids {
		ids[i] = fmt.Sprintf("%064x", i)
	}
	if err := StreamBlocks(c, context.Background(), reader, ids, fileKey, fileIV, "download-memory-bench"); err != nil {
		b.Fatalf("StreamBlocks: %v", err)
	}
}

// discardResponseWriter keeps the recorder from accumulating the whole response,
// which would measure the test harness rather than the server.
type discardResponseWriter struct {
	gin.ResponseWriter
	written int
}

func (w *discardResponseWriter) Write(p []byte) (int, error) {
	w.written += len(p)
	return len(p), nil
}

func (w *discardResponseWriter) WriteString(s string) (int, error) {
	w.written += len(s)
	return len(s), nil
}

func (w *discardResponseWriter) Size() int { return w.written }

func (w *discardResponseWriter) Written() bool { return w.written > 0 }
