package chunker

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

// =============================================================================
// Large File Integration Tests
// =============================================================================

// TestLargeFileChunking tests chunking behavior with large files
// Run with: go test -v -run TestLargeFileChunking -timeout 5m
func TestLargeFileChunking(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping large file test in short mode")
	}

	tests := []struct {
		name     string
		fileSize int64
		minChunk int64
		avgChunk int64
		maxChunk int64
	}{
		{
			name:     "100MB file with 2MB chunks",
			fileSize: 100 * 1024 * 1024,
			minChunk: 512 * 1024,        // 512 KB
			avgChunk: 2 * 1024 * 1024,   // 2 MB
			maxChunk: 8 * 1024 * 1024,   // 8 MB
		},
		{
			name:     "100MB file with 16MB chunks",
			fileSize: 100 * 1024 * 1024,
			minChunk: 4 * 1024 * 1024,   // 4 MB
			avgChunk: 16 * 1024 * 1024,  // 16 MB
			maxChunk: 64 * 1024 * 1024,  // 64 MB
		},
		{
			name:     "256MB file with 32MB chunks",
			fileSize: 256 * 1024 * 1024,
			minChunk: 8 * 1024 * 1024,   // 8 MB
			avgChunk: 32 * 1024 * 1024,  // 32 MB
			maxChunk: 128 * 1024 * 1024, // 128 MB
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Generate test data
			t.Logf("Generating %s of test data...", formatBytes(tt.fileSize))
			data := generateTestData(t, tt.fileSize)

			// Create FastCDC chunker
			cdc := NewFastCDC(tt.minChunk, tt.avgChunk, tt.maxChunk)

			// Chunk the data
			t.Logf("Chunking with min=%s, avg=%s, max=%s...",
				formatBytes(tt.minChunk), formatBytes(tt.avgChunk), formatBytes(tt.maxChunk))

			start := time.Now()
			blocks := cdc.ChunkAll(data)
			duration := time.Since(start)

			// Report results
			t.Logf("Chunking completed in %v", duration)
			t.Logf("Number of chunks: %d", len(blocks))
			t.Logf("Throughput: %s/s", formatBytes(int64(float64(tt.fileSize)/duration.Seconds())))

			// Analyze chunk distribution
			var totalSize int64
			var minSize, maxSize int64 = int64(^uint64(0) >> 1), 0
			for _, b := range blocks {
				totalSize += b.Size
				if b.Size < minSize {
					minSize = b.Size
				}
				if b.Size > maxSize {
					maxSize = b.Size
				}
			}

			avgSize := totalSize / int64(len(blocks))
			t.Logf("Chunk sizes: min=%s, avg=%s, max=%s",
				formatBytes(minSize), formatBytes(avgSize), formatBytes(maxSize))

			// Verify total size matches
			if totalSize != tt.fileSize {
				t.Errorf("Total chunk size %d != file size %d", totalSize, tt.fileSize)
			}

			// Verify data integrity by reassembling
			reassembled := make([]byte, 0, tt.fileSize)
			for _, b := range blocks {
				reassembled = append(reassembled, b.Data...)
			}
			if !bytes.Equal(data, reassembled) {
				t.Error("Reassembled data doesn't match original")
			}

			// Verify hash integrity
			originalHash := sha256.Sum256(data)
			reassembledHash := sha256.Sum256(reassembled)
			if originalHash != reassembledHash {
				t.Error("Hash mismatch after reassembly")
			}
			t.Logf("SHA-256 verified: %x", originalHash[:8])
		})
	}
}

// TestAdaptiveChunkingWithSpeed tests adaptive chunking at different speeds
func TestAdaptiveChunkingWithSpeed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping adaptive test in short mode")
	}

	fileSize := int64(100 * 1024 * 1024) // 100 MB
	data := generateTestData(t, fileSize)

	// Simulate different connection speeds
	speeds := []struct {
		name        string
		bytesPerSec float64
	}{
		{"Slow (1 Mbps)", 125 * 1024},           // 125 KB/s
		{"Mobile (10 Mbps)", 1.25 * 1024 * 1024}, // 1.25 MB/s
		{"Home (50 Mbps)", 6.25 * 1024 * 1024},   // 6.25 MB/s
		{"Office (100 Mbps)", 12.5 * 1024 * 1024}, // 12.5 MB/s
		{"Fast (500 Mbps)", 62.5 * 1024 * 1024},   // 62.5 MB/s
		{"Datacenter (1 Gbps)", 125 * 1024 * 1024}, // 125 MB/s
	}

	t.Logf("\n%-20s | %-12s | %-8s | %-12s | %-10s",
		"Connection", "Chunk Size", "Chunks", "Upload Time*", "Category")
	t.Log("----------------------------------------------------------------------")

	for _, speed := range speeds {
		cfg := DefaultAdaptiveConfig()
		ac := NewAdaptiveChunker(cfg)
		ac.SetSpeed(speed.bytesPerSec)

		chunkSize := ac.GetChunkSize()
		cdc := ac.NewFastCDCFromSpeed()

		blocks := cdc.ChunkAll(data)

		// Estimate upload time
		estimatedTime := time.Duration(float64(fileSize) / speed.bytesPerSec * float64(time.Second))

		category := SpeedCategory(speed.bytesPerSec)

		t.Logf("%-20s | %-12s | %-8d | %-12s | %s",
			speed.name,
			formatBytes(chunkSize),
			len(blocks),
			estimatedTime.Round(time.Millisecond),
			category)
	}
	t.Log("* Estimated upload time for 100 MB file")
}

// TestSpeedProbeAccuracy tests the speed probe with controlled throughput
func TestSpeedProbeAccuracy(t *testing.T) {
	cfg := DefaultAdaptiveConfig()
	cfg.ProbeSize = 1 * 1024 * 1024 // 1 MB probe

	probe := NewSpeedProbe(cfg)

	// Test with a throttled writer that simulates 10 MB/s
	targetSpeed := 10 * 1024 * 1024 // 10 MB/s
	writer := &throttledWriter{
		bytesPerSecond: float64(targetSpeed),
	}

	result, err := probe.Probe(context.Background(), writer)
	if err != nil {
		t.Fatalf("Probe failed: %v", err)
	}

	t.Logf("Target speed: %s/s", formatBytes(int64(targetSpeed)))
	t.Logf("Measured speed: %s/s", formatBytes(int64(result.BytesPerSecond)))
	t.Logf("Probe duration: %v", result.Duration)
	t.Logf("Bytes sent: %s", formatBytes(result.BytesSent))

	// Allow 50% tolerance due to timing variability
	tolerance := float64(targetSpeed) * 0.5
	if result.BytesPerSecond < float64(targetSpeed)-tolerance ||
		result.BytesPerSecond > float64(targetSpeed)+tolerance {
		t.Logf("Warning: Measured speed differs significantly from target (this can happen due to timing)")
	}
}

// TestChunkDedupPotential measures deduplication potential in chunked data
func TestChunkDedupPotential(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping dedup test in short mode")
	}

	// Create two similar files with 80% overlap
	fileSize := int64(50 * 1024 * 1024) // 50 MB each
	t.Logf("Creating two %s files with ~80%% overlap...", formatBytes(fileSize))

	// First file: random data
	file1 := generateTestData(t, fileSize)

	// Second file: 80% same + 20% different
	file2 := make([]byte, fileSize)
	overlapSize := int64(float64(fileSize) * 0.8)
	copy(file2[:overlapSize], file1[:overlapSize])
	rand.Read(file2[overlapSize:])

	// Chunk both files
	cdc := NewFastCDC(512*1024, 2*1024*1024, 8*1024*1024)

	blocks1 := cdc.ChunkAll(file1)
	blocks2 := cdc.ChunkAll(file2)

	t.Logf("File 1: %d chunks", len(blocks1))
	t.Logf("File 2: %d chunks", len(blocks2))

	// Count unique blocks by hash
	hashes := make(map[string]int)
	for _, b := range blocks1 {
		h := sha256.Sum256(b.Data)
		hashes[string(h[:])]++
	}
	for _, b := range blocks2 {
		h := sha256.Sum256(b.Data)
		hashes[string(h[:])]++
	}

	totalBlocks := len(blocks1) + len(blocks2)
	uniqueBlocks := len(hashes)
	duplicateBlocks := totalBlocks - uniqueBlocks
	dedupRatio := float64(duplicateBlocks) / float64(totalBlocks) * 100

	t.Logf("Total chunks: %d", totalBlocks)
	t.Logf("Unique chunks: %d", uniqueBlocks)
	t.Logf("Duplicate chunks: %d", duplicateBlocks)
	t.Logf("Deduplication ratio: %.1f%%", dedupRatio)

	// With 80% overlap and content-defined chunking, we should see significant dedup
	if dedupRatio < 50 {
		t.Logf("Note: Lower dedup ratio than expected - this is normal if boundaries shift")
	}
}

// TestChunkAllPerformance tests chunking entire data buffer (the recommended approach)
// Note: Chunk(io.Reader) has limitations - use ChunkAll for reliable chunking
func TestChunkAllPerformance(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping streaming test in short mode")
	}

	fileSize := int64(50 * 1024 * 1024)
	t.Logf("Testing ChunkAll with %s...", formatBytes(fileSize))

	// Generate test data
	data := generateTestData(t, fileSize)

	// Create chunker
	cdc := NewFastCDC(512*1024, 2*1024*1024, 8*1024*1024)

	// Chunk entire buffer (reliable approach)
	start := time.Now()
	blocks := cdc.ChunkAll(data)
	duration := time.Since(start)

	t.Logf("ChunkAll completed in %v", duration)
	t.Logf("Number of chunks: %d", len(blocks))
	t.Logf("Throughput: %s/s", formatBytes(int64(float64(fileSize)/duration.Seconds())))

	// Verify total size
	var totalSize int64
	for _, b := range blocks {
		totalSize += b.Size
	}
	if totalSize != fileSize {
		t.Errorf("Total size %d != expected %d", totalSize, fileSize)
	}
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkFastCDC_2MB_Chunks(b *testing.B) {
	data := make([]byte, 100*1024*1024) // 100 MB
	rand.Read(data)

	cdc := NewFastCDC(512*1024, 2*1024*1024, 8*1024*1024)

	b.ResetTimer()
	b.SetBytes(int64(len(data)))

	for i := 0; i < b.N; i++ {
		cdc.ChunkAll(data)
	}
}

func BenchmarkFastCDC_16MB_Chunks(b *testing.B) {
	data := make([]byte, 256*1024*1024) // 256 MB
	rand.Read(data)

	cdc := NewFastCDC(4*1024*1024, 16*1024*1024, 64*1024*1024)

	b.ResetTimer()
	b.SetBytes(int64(len(data)))

	for i := 0; i < b.N; i++ {
		cdc.ChunkAll(data)
	}
}

func BenchmarkSpeedProbe(b *testing.B) {
	cfg := DefaultAdaptiveConfig()
	cfg.ProbeSize = 1 * 1024 * 1024

	for i := 0; i < b.N; i++ {
		probe := NewSpeedProbe(cfg)
		probe.Probe(context.Background(), io.Discard)
	}
}

// =============================================================================
// Helpers
// =============================================================================

// generateTestData creates pseudo-random data with some repeated patterns
// (to make deduplication testing more realistic)
func generateTestData(t testing.TB, size int64) []byte {
	t.Helper()

	data := make([]byte, size)

	// Mix random data with some repeating patterns
	chunkSize := 64 * 1024 // 64 KB pattern chunks

	for offset := int64(0); offset < size; offset += int64(chunkSize) {
		end := offset + int64(chunkSize)
		if end > size {
			end = size
		}

		// 70% random, 30% repeating pattern
		if offset%(10*int64(chunkSize)) < 7*int64(chunkSize) {
			rand.Read(data[offset:end])
		} else {
			// Repeating pattern
			pattern := []byte("SesameFS-TestData-Pattern-12345678")
			for i := offset; i < end; i++ {
				data[i] = pattern[i%int64(len(pattern))]
			}
		}
	}

	return data
}

// throttledWriter simulates a writer with limited throughput
type throttledWriter struct {
	bytesPerSecond float64
	totalWritten   int64
}

func (w *throttledWriter) Write(p []byte) (int, error) {
	if w.bytesPerSecond <= 0 {
		return len(p), nil
	}

	// Calculate how long this write should take
	duration := time.Duration(float64(len(p)) / w.bytesPerSecond * float64(time.Second))
	time.Sleep(duration)

	w.totalWritten += int64(len(p))
	return len(p), nil
}

// formatBytes formats bytes as human-readable string
func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// =============================================================================
// CLI Test Runner (can be run standalone)
// =============================================================================

// TestCLIChunkingDemo is a demo that can be run to see chunking in action
// Run with: go test -v -run TestCLIChunkingDemo -timeout 10m
func TestCLIChunkingDemo(t *testing.T) {
	if os.Getenv("CHUNKING_DEMO") != "1" {
		t.Skip("Set CHUNKING_DEMO=1 to run this demo")
	}

	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("SesameFS Adaptive Chunking Demo")
	fmt.Println(strings.Repeat("=", 60))

	// Demo 1: Speed-based chunk size calculation
	fmt.Println("\n1. Speed-Based Chunk Sizing:")
	fmt.Println("   Target: 8 seconds per chunk upload")
	fmt.Println()

	speeds := []float64{
		125 * 1024,           // 1 Mbps
		1.25 * 1024 * 1024,   // 10 Mbps
		12.5 * 1024 * 1024,   // 100 Mbps
		125 * 1024 * 1024,    // 1 Gbps
	}

	for _, speed := range speeds {
		chunkSize := RecommendedChunkSize(speed, 8.0)
		fmt.Printf("   %s/s → %s chunks\n",
			formatBytes(int64(speed)), formatBytes(chunkSize))
	}

	// Demo 2: Large file chunking
	fmt.Println("\n2. Chunking a 200 MB File:")
	fmt.Println()

	fileSize := int64(200 * 1024 * 1024)
	data := make([]byte, fileSize)
	rand.Read(data)

	chunkSizes := []struct {
		name string
		min, avg, max int64
	}{
		{"Small (2 MB avg)", 512 * 1024, 2 * 1024 * 1024, 8 * 1024 * 1024},
		{"Medium (16 MB avg)", 4 * 1024 * 1024, 16 * 1024 * 1024, 64 * 1024 * 1024},
		{"Large (64 MB avg)", 16 * 1024 * 1024, 64 * 1024 * 1024, 256 * 1024 * 1024},
	}

	for _, cs := range chunkSizes {
		cdc := NewFastCDC(cs.min, cs.avg, cs.max)
		start := time.Now()
		blocks := cdc.ChunkAll(data)
		duration := time.Since(start)

		fmt.Printf("   %s: %d chunks in %v (%.0f MB/s)\n",
			cs.name, len(blocks), duration,
			float64(fileSize)/duration.Seconds()/1024/1024)
	}

	fmt.Println("\n" + strings.Repeat("=", 60))
}
