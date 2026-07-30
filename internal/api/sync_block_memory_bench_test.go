package api

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"runtime/debug"
	"testing"

	"github.com/gin-gonic/gin"
)

// This benchmark exists to answer one question with a number instead of a guess:
// how much heap does a single admitted PutBlock hold, and what does it touch at
// its peak?
//
// Those figures are the divisor for the node cap:
//
//	sync_block_max_inflight_per_node = (memory budget for block PUT) / (per-request cost)
//
// It deliberately measures the *buffered-body* segment — MaxBytesReader +
// io.ReadAll + the SHA-256 pass — because that is the segment the in-flight gate
// covers and the only one that scales with the configured body cap. Storage and
// Cassandra costs sit downstream of the same admission and are bounded by their
// own client-side concurrency; they are not what a body cap multiplies.
//
// Two numbers are reported, and they answer different questions:
//
//   - retained-B/op is cap(data): the buffer one admitted request holds for its
//     whole lifetime, through hashing and the storage write. N concurrent
//     admissions cost N x this. It is the number the node cap divides into.
//   - alloc-peak-B/op is measured with the GC off, so nothing is reclaimed
//     mid-read: it is the total a single read touches, which upper-bounds the
//     transient spike when io.ReadAll grows its buffer and briefly holds the old
//     and new arrays at once.
//
// Neither is "16 MiB". io.ReadAll grows by append from a 512-byte start, so the
// retained capacity overshoots the body and the growth path allocates several
// times the final size. Sizing a cap off the wire size understates it.
//
// Run:
//
//	go test -run '^$' -bench BenchmarkPutBlockBodyMemory -benchmem ./internal/api/
func BenchmarkPutBlockBodyMemory(b *testing.B) {
	for _, size := range []int{1 << 20, 4 << 20, 8 << 20, 16 << 20} {
		b.Run(fmt.Sprintf("body=%dMiB", size>>20), func(b *testing.B) {
			benchmarkBufferedBlockBody(b, size)
		})
	}
}

func benchmarkBufferedBlockBody(b *testing.B, size int) {
	gin.SetMode(gin.TestMode)
	payload := bytes.Repeat([]byte{0xA5}, size)
	maxBytes := int64(16 << 20)

	retained, allocPeak := measureOneBlockRead(b, payload, maxBytes)

	b.ReportAllocs()
	b.SetBytes(int64(size))
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		data := readBlockBodyOnce(b, payload, maxBytes)
		sum := sha256.Sum256(data)
		if sum == ([32]byte{}) {
			b.Fatal("unexpected zero digest")
		}
	}

	b.StopTimer()
	b.ReportMetric(float64(retained), "retained-B/op")
	b.ReportMetric(float64(retained)/float64(size), "retained/body")
	b.ReportMetric(float64(allocPeak), "alloc-peak-B/op")
	b.ReportMetric(float64(allocPeak)/float64(size), "peak/body")
}

// measureOneBlockRead runs the read exactly once with the collector disabled, so
// the HeapAlloc delta is a clean "everything this read touched" figure rather
// than a racy sample that can underflow when a GC cycle lands between reads.
func measureOneBlockRead(b *testing.B, payload []byte, maxBytes int64) (retained, allocPeak uint64) {
	b.Helper()

	prevGC := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(prevGC)

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)

	data := readBlockBodyOnce(b, payload, maxBytes)

	runtime.ReadMemStats(&after)
	if after.HeapAlloc > before.HeapAlloc {
		allocPeak = after.HeapAlloc - before.HeapAlloc
	}
	retained = uint64(cap(data))

	// Keep the buffer reachable until after both readings, exactly as PutBlock
	// keeps it reachable while handing the bytes to storage.
	runtime.KeepAlive(data)
	return retained, allocPeak
}

func readBlockBodyOnce(b *testing.B, payload []byte, maxBytes int64) []byte {
	b.Helper()

	req := httptest.NewRequest(http.MethodPut, "/seafhttp/repo/r/block/b", bytes.NewReader(payload))
	req.ContentLength = int64(len(payload))
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req

	data, ok := readLimitedRequestBody(c, maxBytes)
	if !ok {
		b.Fatalf("body read rejected at %d bytes under a %d cap", len(payload), maxBytes)
	}
	if len(data) != len(payload) {
		b.Fatalf("short read: got %d bytes, want %d", len(data), len(payload))
	}
	return data
}
