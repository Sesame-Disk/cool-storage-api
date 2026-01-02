package chunker

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"
)

// =============================================================================
// Adaptive Chunker Tests
// =============================================================================

func TestDefaultAdaptiveConfig(t *testing.T) {
	cfg := DefaultAdaptiveConfig()

	if cfg.AbsoluteMin != 2*1024*1024 {
		t.Errorf("AbsoluteMin = %d, want 2 MB", cfg.AbsoluteMin)
	}
	if cfg.AbsoluteMax != 256*1024*1024 {
		t.Errorf("AbsoluteMax = %d, want 256 MB", cfg.AbsoluteMax)
	}
	if cfg.InitialSize != 16*1024*1024 {
		t.Errorf("InitialSize = %d, want 16 MB", cfg.InitialSize)
	}
	if cfg.TargetSeconds != 8.0 {
		t.Errorf("TargetSeconds = %f, want 8.0", cfg.TargetSeconds)
	}
	if cfg.ProbeSize != 1*1024*1024 {
		t.Errorf("ProbeSize = %d, want 1 MB", cfg.ProbeSize)
	}
}

func TestNewAdaptiveChunker(t *testing.T) {
	cfg := DefaultAdaptiveConfig()
	ac := NewAdaptiveChunker(cfg)

	if ac.GetChunkSize() != cfg.InitialSize {
		t.Errorf("initial chunk size = %d, want %d", ac.GetChunkSize(), cfg.InitialSize)
	}
}

func TestAdaptiveChunkerSetSpeed(t *testing.T) {
	tests := []struct {
		name          string
		bytesPerSec   float64
		targetSeconds float64
		expectedSize  int64
	}{
		{
			name:          "slow connection (500 Kbps)",
			bytesPerSec:   62500, // 500 Kbps = 62.5 KB/s
			targetSeconds: 8.0,
			expectedSize:  2 * 1024 * 1024, // Clamped to min (2 MB)
		},
		{
			name:          "home connection (10 Mbps)",
			bytesPerSec:   1.25 * 1024 * 1024, // 10 Mbps = 1.25 MB/s
			targetSeconds: 8.0,
			expectedSize:  10 * 1024 * 1024, // 10 MB
		},
		{
			name:          "office connection (100 Mbps)",
			bytesPerSec:   12.5 * 1024 * 1024, // 100 Mbps = 12.5 MB/s
			targetSeconds: 8.0,
			expectedSize:  100 * 1024 * 1024, // 100 MB
		},
		{
			name:          "datacenter connection (1 Gbps)",
			bytesPerSec:   125 * 1024 * 1024, // 1 Gbps = 125 MB/s
			targetSeconds: 8.0,
			expectedSize:  256 * 1024 * 1024, // Clamped to max (256 MB)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultAdaptiveConfig()
			cfg.TargetSeconds = tt.targetSeconds
			ac := NewAdaptiveChunker(cfg)

			ac.SetSpeed(tt.bytesPerSec)
			got := ac.GetChunkSize()

			// Allow 10% tolerance for rounding
			tolerance := tt.expectedSize / 10
			if got < tt.expectedSize-tolerance || got > tt.expectedSize+tolerance {
				t.Errorf("chunk size = %d, want ~%d", got, tt.expectedSize)
			}
		})
	}
}

func TestAdaptiveChunkerGetChunkSizes(t *testing.T) {
	cfg := DefaultAdaptiveConfig()
	ac := NewAdaptiveChunker(cfg)

	// Set speed for 40 MB chunks
	ac.SetSpeed(5 * 1024 * 1024) // 5 MB/s → 40 MB chunks

	min, avg, max := ac.GetChunkSizes()

	// avg should be 40 MB
	if avg != 40*1024*1024 {
		t.Errorf("avg = %d, want 40 MB", avg)
	}

	// min should be avg/4 = 10 MB
	if min != 10*1024*1024 {
		t.Errorf("min = %d, want 10 MB", min)
	}

	// max should be avg*4 = 160 MB (but clamped to 256 MB)
	if max != 160*1024*1024 {
		t.Errorf("max = %d, want 160 MB", max)
	}
}

func TestAdaptiveChunkerMinBounds(t *testing.T) {
	cfg := DefaultAdaptiveConfig()
	ac := NewAdaptiveChunker(cfg)

	// Set very slow speed
	ac.SetSpeed(1000) // 1 KB/s

	min, avg, max := ac.GetChunkSizes()

	// avg should be clamped to AbsoluteMin
	if avg < cfg.AbsoluteMin {
		t.Errorf("avg = %d, should be at least %d", avg, cfg.AbsoluteMin)
	}

	// min should be at least 64 bytes (FastCDC requirement)
	if min < 64 {
		t.Errorf("min = %d, should be at least 64", min)
	}

	// max should not exceed AbsoluteMax
	if max > cfg.AbsoluteMax {
		t.Errorf("max = %d, should not exceed %d", max, cfg.AbsoluteMax)
	}
}

func TestAdaptiveChunkerAdjustOnTimeout(t *testing.T) {
	cfg := DefaultAdaptiveConfig()
	cfg.InitialSize = 100 * 1024 * 1024 // 100 MB
	ac := NewAdaptiveChunker(cfg)

	// Reduce by half
	newSize := ac.AdjustOnTimeout(0.5)
	if newSize != 50*1024*1024 {
		t.Errorf("after timeout, size = %d, want 50 MB", newSize)
	}

	// Reduce again
	newSize = ac.AdjustOnTimeout(0.5)
	if newSize != 25*1024*1024 {
		t.Errorf("after second timeout, size = %d, want 25 MB", newSize)
	}

	// Keep reducing until min
	for i := 0; i < 10; i++ {
		ac.AdjustOnTimeout(0.5)
	}
	if ac.GetChunkSize() < cfg.AbsoluteMin {
		t.Errorf("size = %d, should not go below %d", ac.GetChunkSize(), cfg.AbsoluteMin)
	}
}

func TestAdaptiveChunkerAdjustOnSuccess(t *testing.T) {
	cfg := DefaultAdaptiveConfig()
	cfg.InitialSize = 16 * 1024 * 1024 // 16 MB
	ac := NewAdaptiveChunker(cfg)

	// Fast upload (2 seconds instead of 8)
	newSize := ac.AdjustOnSuccess(2*time.Second, 1.25)
	if newSize != 20*1024*1024 {
		t.Errorf("after fast upload, size = %d, want 20 MB", newSize)
	}

	// Slow upload (10 seconds) - should not change
	prevSize := ac.GetChunkSize()
	ac.AdjustOnSuccess(10*time.Second, 1.25)
	if ac.GetChunkSize() != prevSize {
		t.Errorf("after slow upload, size changed from %d to %d", prevSize, ac.GetChunkSize())
	}
}

func TestAdaptiveChunkerNewFastCDCFromSpeed(t *testing.T) {
	cfg := DefaultAdaptiveConfig()
	ac := NewAdaptiveChunker(cfg)

	// Set speed for 32 MB chunks
	ac.SetSpeed(4 * 1024 * 1024) // 4 MB/s → 32 MB chunks

	cdc := ac.NewFastCDCFromSpeed()
	if cdc == nil {
		t.Fatal("NewFastCDCFromSpeed returned nil")
	}

	// Verify the FastCDC has correct parameters
	if cdc.avgSize != 32*1024*1024 {
		t.Errorf("FastCDC avgSize = %d, want 32 MB", cdc.avgSize)
	}
}

// =============================================================================
// Speed Probe Tests
// =============================================================================

func TestNewSpeedProbe(t *testing.T) {
	cfg := DefaultAdaptiveConfig()
	probe := NewSpeedProbe(cfg)

	if probe.IsProbed() {
		t.Error("new probe should not be marked as probed")
	}
	if probe.GetSpeed() != 0 {
		t.Errorf("new probe speed = %f, want 0", probe.GetSpeed())
	}
}

// mockWriter is a writer that simulates network latency
type mockWriter struct {
	written int64
	delay   time.Duration
}

func (w *mockWriter) Write(p []byte) (int, error) {
	if w.delay > 0 {
		time.Sleep(w.delay)
	}
	w.written += int64(len(p))
	return len(p), nil
}

func TestSpeedProbe(t *testing.T) {
	cfg := DefaultAdaptiveConfig()
	cfg.ProbeSize = 1024 // Small probe for fast test
	probe := NewSpeedProbe(cfg)

	// Fast writer (simulates fast connection)
	writer := &mockWriter{delay: 10 * time.Millisecond}

	result, err := probe.Probe(context.Background(), writer)
	if err != nil {
		t.Fatalf("Probe failed: %v", err)
	}

	if result.BytesSent != cfg.ProbeSize {
		t.Errorf("BytesSent = %d, want %d", result.BytesSent, cfg.ProbeSize)
	}
	if result.BytesPerSecond <= 0 {
		t.Error("BytesPerSecond should be positive")
	}
	if result.Duration <= 0 {
		t.Error("Duration should be positive")
	}

	// Check that probe state was updated
	if !probe.IsProbed() {
		t.Error("probe should be marked as probed")
	}
	if probe.GetSpeed() != result.BytesPerSecond {
		t.Errorf("GetSpeed() = %f, want %f", probe.GetSpeed(), result.BytesPerSecond)
	}
}

func TestSpeedProbeTimeout(t *testing.T) {
	cfg := DefaultAdaptiveConfig()
	cfg.ProbeSize = 1024
	cfg.ProbeTimeout = 50 * time.Millisecond
	probe := NewSpeedProbe(cfg)

	// Slow writer that will timeout
	writer := &mockWriter{delay: 200 * time.Millisecond}

	_, err := probe.Probe(context.Background(), writer)
	if err == nil {
		t.Error("expected timeout error")
	}
}

// =============================================================================
// Speed Category Tests
// =============================================================================

func TestSpeedCategory(t *testing.T) {
	tests := []struct {
		bytesPerSec float64
		contains    string
	}{
		{50000, "slow"},             // 400 Kbps
		{500000, "mobile"},          // 4 Mbps
		{3000000, "home"},           // 24 Mbps
		{10000000, "office"},        // 80 Mbps
		{50000000, "fast"},          // 400 Mbps
		{200000000, "datacenter"},   // 1.6 Gbps
	}

	for _, tt := range tests {
		cat := SpeedCategory(tt.bytesPerSec)
		if !bytes.Contains([]byte(cat), []byte(tt.contains)) {
			t.Errorf("SpeedCategory(%f) = %q, want to contain %q", tt.bytesPerSec, cat, tt.contains)
		}
	}
}

func TestRecommendedChunkSize(t *testing.T) {
	tests := []struct {
		bytesPerSec   float64
		targetSeconds float64
		expected      int64
	}{
		{1000, 8.0, 2 * 1024 * 1024},            // Very slow → min (2 MB)
		{5 * 1024 * 1024, 8.0, 40 * 1024 * 1024}, // 5 MB/s → 40 MB
		{500 * 1024 * 1024, 8.0, 256 * 1024 * 1024}, // Very fast → max (256 MB)
	}

	for _, tt := range tests {
		got := RecommendedChunkSize(tt.bytesPerSec, tt.targetSeconds)
		if got != tt.expected {
			t.Errorf("RecommendedChunkSize(%f, %f) = %d, want %d",
				tt.bytesPerSec, tt.targetSeconds, got, tt.expected)
		}
	}
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestAdaptiveChunkerWithFastCDC(t *testing.T) {
	cfg := DefaultAdaptiveConfig()
	cfg.AbsoluteMin = 64    // Small for testing
	cfg.AbsoluteMax = 10240 // Small for testing
	cfg.InitialSize = 256
	cfg.TargetSeconds = 1.0
	ac := NewAdaptiveChunker(cfg)

	// Set speed for 512 byte chunks
	ac.SetSpeed(512)

	cdc := ac.NewFastCDCFromSpeed()

	// Create test data
	data := make([]byte, 2048)
	for i := range data {
		data[i] = byte(i % 256)
	}

	// Chunk the data
	blocks := cdc.ChunkAll(data)

	if len(blocks) == 0 {
		t.Error("expected at least one block")
	}

	// Verify total size
	var totalSize int64
	for _, b := range blocks {
		totalSize += b.Size
	}
	if totalSize != int64(len(data)) {
		t.Errorf("total size = %d, want %d", totalSize, len(data))
	}
}

// discardWriter is a writer that discards all data
type discardWriter struct{}

func (w discardWriter) Write(p []byte) (int, error) {
	return len(p), nil
}

func TestSpeedProbeWithDiscard(t *testing.T) {
	cfg := DefaultAdaptiveConfig()
	cfg.ProbeSize = 1024
	probe := NewSpeedProbe(cfg)

	result, err := probe.Probe(context.Background(), io.Discard)
	if err != nil {
		t.Fatalf("Probe failed: %v", err)
	}

	// With discard writer, should be very fast
	if result.BytesPerSecond < 1000000 {
		t.Errorf("BytesPerSecond = %f, expected very fast with discard", result.BytesPerSecond)
	}
}
