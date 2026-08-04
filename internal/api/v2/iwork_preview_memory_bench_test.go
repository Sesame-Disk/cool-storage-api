package v2

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"runtime"
	"runtime/debug"
	"testing"

	"github.com/Sesame-Disk/sesamefs/internal/crypto"
)

// The D0 contract requires D6 to measure the iWork preview peak rather than
// derive it from fileSize, because that branch is the worst memory profile in
// subcontract D: it is not a stream. ServeRawFile copies every block of the
// source into one bytes.Buffer — reading each block fully into memory first when
// the library is encrypted, so decryption can run — and only then parses the
// assembled document as a ZIP.
//
// The source size is gated by FileView.MaxIWorkSourceBytes (32 MiB in the
// shipped configurations), because the general 1 GiB preview limit is not an
// in-memory budget for this branch:
//
//	iWork node memory budget ≈ max_active_raw × measured_peak_iwork_request
//
// This measures that peak for both library kinds. It covers the terms the
// contract names — the source buffer's capacity rather than its length, the
// current encrypted and decrypted block, the extracted preview and the ZIP
// parsing overhead — by driving the same buffering loop and the same extractor
// the handler uses. It deliberately does not include the HTTP layer, which does
// not scale with file size.
//
// Run:
//
//	go test -run '^$' -bench BenchmarkIWorkPreviewMemory -benchmem ./internal/api/v2/

const iworkBenchBlockSize = 8 << 20 // the system block size

func BenchmarkIWorkPreviewMemory(b *testing.B) {
	for _, sourceMiB := range []int{16, 32, 64, 256} {
		for _, encrypted := range []bool{false, true} {
			name := fmt.Sprintf("source=%dMiB/plaintext", sourceMiB)
			if encrypted {
				name = fmt.Sprintf("source=%dMiB/encrypted", sourceMiB)
			}
			b.Run(name, func(b *testing.B) {
				benchmarkIWorkPreview(b, sourceMiB<<20, encrypted)
			})
		}
	}
}

func benchmarkIWorkPreview(b *testing.B, sourceBytes int, encrypted bool) {
	document := buildIWorkFixture(b, sourceBytes)

	peak, previewLen := measureIWorkPeak(b, document, encrypted)

	b.ReportAllocs()
	b.SetBytes(int64(len(document)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := assembleAndExtractIWork(document, encrypted); err != nil {
			b.Fatalf("iwork preview: %v", err)
		}
	}
	b.StopTimer()

	b.ReportMetric(float64(peak), "peak-B/admission")
	b.ReportMetric(float64(peak)/float64(len(document)), "peak/source")
	b.ReportMetric(float64(previewLen), "preview-B")
}

// measureIWorkPeak samples the heap with the collector off, so nothing is
// reclaimed mid-assembly and the figure is everything one admitted request
// touches at once.
func measureIWorkPeak(b *testing.B, document []byte, encrypted bool) (peak uint64, previewLen int) {
	b.Helper()

	prevGC := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(prevGC)
	runtime.GC()

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)

	preview, err := assembleAndExtractIWork(document, encrypted)
	if err != nil {
		b.Fatalf("iwork preview: %v", err)
	}

	runtime.ReadMemStats(&after)
	runtime.KeepAlive(preview)

	if after.HeapAlloc < before.HeapAlloc {
		return 0, len(preview)
	}
	return after.HeapAlloc - before.HeapAlloc, len(preview)
}

// assembleAndExtractIWork mirrors the ServeRawFile preview=1 branch: buffer every
// block into one bytes.Buffer, decrypting per block when the library is
// encrypted, then parse the assembled document.
func assembleAndExtractIWork(document []byte, encrypted bool) ([]byte, error) {
	var fileKey, fileIV []byte
	if encrypted {
		fileKey = bytes.Repeat([]byte{0x11}, crypto.FileKeySize)
		fileIV = bytes.Repeat([]byte{0x22}, crypto.IVSize)
	}

	var content bytes.Buffer
	for offset := 0; offset < len(document); offset += iworkBenchBlockSize {
		end := offset + iworkBenchBlockSize
		if end > len(document) {
			end = len(document)
		}
		plain := document[offset:end]

		if encrypted {
			ciphertext, err := crypto.EncryptBlockSeafile(plain, fileKey, fileIV)
			if err != nil {
				return nil, err
			}
			// The handler reads the whole encrypted block, then decrypts it, so
			// both live at once before the result is appended.
			decrypted, err := crypto.DecryptLibraryBlock(ciphertext, fileKey, fileIV)
			if err != nil {
				return nil, err
			}
			content.Write(decrypted)
			continue
		}
		content.Write(plain)
	}

	return extractIWorkPreviewPDFContext(context.Background(), content.Bytes(), 50<<20)
}

// buildIWorkFixture produces a document shaped like a real iWork file: a ZIP
// whose bulk is incompressible payload, carrying a small QuickLook preview.
func buildIWorkFixture(b *testing.B, size int) []byte {
	b.Helper()

	var out bytes.Buffer
	writer := zip.NewWriter(&out)

	preview, err := writer.Create("QuickLook/Preview.pdf")
	if err != nil {
		b.Fatal(err)
	}
	if _, err := preview.Write(bytes.Repeat([]byte("%PDF-1.4 preview "), 4096)); err != nil {
		b.Fatal(err)
	}

	// Stored rather than deflated, and random rather than repeated, so the
	// fixture's on-disk size tracks the requested source size instead of
	// collapsing to nothing.
	body, err := writer.CreateHeader(&zip.FileHeader{Name: "Index/Document.iwa", Method: zip.Store})
	if err != nil {
		b.Fatal(err)
	}
	payload := make([]byte, 1<<20)
	if _, err := rand.Read(payload); err != nil {
		b.Fatal(err)
	}
	for written := 0; written < size; written += len(payload) {
		if _, err := body.Write(payload); err != nil {
			b.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		b.Fatal(err)
	}
	return out.Bytes()
}
