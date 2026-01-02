package chunker

import (
	"context"
	"io"
	"sync"
	"time"
)

// AdaptiveConfig holds configuration for adaptive chunk sizing
type AdaptiveConfig struct {
	// Chunk size bounds
	AbsoluteMin int64 // Minimum chunk size (default: 2 MB)
	AbsoluteMax int64 // Maximum chunk size (default: 256 MB)
	InitialSize int64 // Starting chunk size (default: 16 MB)

	// Target upload time per chunk
	TargetSeconds float64 // Target ~8 seconds per chunk upload

	// Speed probe settings
	ProbeSize    int64         // Size of speed probe (default: 1 MB)
	ProbeTimeout time.Duration // Timeout for probe (default: 30s)
}

// DefaultAdaptiveConfig returns sensible defaults
func DefaultAdaptiveConfig() AdaptiveConfig {
	return AdaptiveConfig{
		AbsoluteMin:   2 * 1024 * 1024,   // 2 MB
		AbsoluteMax:   256 * 1024 * 1024, // 256 MB
		InitialSize:   16 * 1024 * 1024,  // 16 MB
		TargetSeconds: 8.0,               // 8 seconds per chunk
		ProbeSize:     1 * 1024 * 1024,   // 1 MB probe
		ProbeTimeout:  30 * time.Second,
	}
}

// SpeedProbe measures connection speed by timing a data transfer
type SpeedProbe struct {
	cfg      AdaptiveConfig
	mu       sync.RWMutex
	speed    float64 // bytes per second
	probed   bool
	probedAt time.Time
}

// NewSpeedProbe creates a new speed probe
func NewSpeedProbe(cfg AdaptiveConfig) *SpeedProbe {
	return &SpeedProbe{cfg: cfg}
}

// ProbeResult holds the result of a speed probe
type ProbeResult struct {
	BytesPerSecond float64
	Duration       time.Duration
	BytesSent      int64
}

// Probe measures upload speed using a writer
// The writer should be connected to the actual upload destination
func (p *SpeedProbe) Probe(ctx context.Context, w io.Writer) (*ProbeResult, error) {
	// Create probe data (random-ish bytes that compress poorly)
	probeData := make([]byte, p.cfg.ProbeSize)
	for i := range probeData {
		probeData[i] = byte((i * 7) % 256)
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(ctx, p.cfg.ProbeTimeout)
	defer cancel()

	// Time the write
	start := time.Now()

	// Write in a goroutine so we can respect context cancellation
	done := make(chan error, 1)
	var written int64
	go func() {
		n, err := w.Write(probeData)
		written = int64(n)
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			return nil, err
		}
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	duration := time.Since(start)
	bytesPerSecond := float64(written) / duration.Seconds()

	// Store the result
	p.mu.Lock()
	p.speed = bytesPerSecond
	p.probed = true
	p.probedAt = time.Now()
	p.mu.Unlock()

	return &ProbeResult{
		BytesPerSecond: bytesPerSecond,
		Duration:       duration,
		BytesSent:      written,
	}, nil
}

// GetSpeed returns the last measured speed (0 if not probed)
func (p *SpeedProbe) GetSpeed() float64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.speed
}

// IsProbed returns whether a probe has been done
func (p *SpeedProbe) IsProbed() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.probed
}

// AdaptiveChunker adjusts chunk sizes based on connection speed
type AdaptiveChunker struct {
	cfg       AdaptiveConfig
	mu        sync.RWMutex
	chunkSize int64   // Current chunk size
	speed     float64 // Current measured speed (bytes/sec)
}

// NewAdaptiveChunker creates a new adaptive chunker
func NewAdaptiveChunker(cfg AdaptiveConfig) *AdaptiveChunker {
	return &AdaptiveChunker{
		cfg:       cfg,
		chunkSize: cfg.InitialSize,
	}
}

// SetSpeed updates the measured speed and adjusts chunk size
func (c *AdaptiveChunker) SetSpeed(bytesPerSecond float64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.speed = bytesPerSecond

	// Calculate optimal chunk size for target seconds
	optimalSize := int64(bytesPerSecond * c.cfg.TargetSeconds)

	// Clamp to bounds
	if optimalSize < c.cfg.AbsoluteMin {
		optimalSize = c.cfg.AbsoluteMin
	}
	if optimalSize > c.cfg.AbsoluteMax {
		optimalSize = c.cfg.AbsoluteMax
	}

	c.chunkSize = optimalSize
}

// GetChunkSize returns the current optimal chunk size
func (c *AdaptiveChunker) GetChunkSize() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.chunkSize
}

// GetChunkSizes returns min, avg, max for FastCDC based on current speed
// FastCDC works best with min = avg/4 and max = avg*4
func (c *AdaptiveChunker) GetChunkSizes() (min, avg, max int64) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	avg = c.chunkSize
	min = avg / 4
	max = avg * 4

	// Ensure min is at least 64 bytes (FastCDC requirement)
	if min < 64 {
		min = 64
	}

	// Ensure max doesn't exceed absolute max
	if max > c.cfg.AbsoluteMax {
		max = c.cfg.AbsoluteMax
	}

	return min, avg, max
}

// NewFastCDCFromSpeed creates a FastCDC chunker with adaptive sizes
func (c *AdaptiveChunker) NewFastCDCFromSpeed() *FastCDC {
	min, avg, max := c.GetChunkSizes()
	return NewFastCDC(min, avg, max)
}

// AdjustOnTimeout reduces chunk size when an upload times out
// Returns the new chunk size
func (c *AdaptiveChunker) AdjustOnTimeout(factor float64) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()

	if factor <= 0 || factor >= 1 {
		factor = 0.5 // Default: reduce by half
	}

	newSize := int64(float64(c.chunkSize) * factor)
	if newSize < c.cfg.AbsoluteMin {
		newSize = c.cfg.AbsoluteMin
	}
	c.chunkSize = newSize

	return newSize
}

// AdjustOnSuccess increases chunk size when upload succeeds faster than target
// Returns the new chunk size
func (c *AdaptiveChunker) AdjustOnSuccess(actualDuration time.Duration, factor float64) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Only increase if upload was significantly faster than target
	targetDuration := time.Duration(c.cfg.TargetSeconds * float64(time.Second))
	if actualDuration >= targetDuration {
		return c.chunkSize // No change if at or above target
	}

	if factor <= 1 {
		factor = 1.25 // Default: increase by 25%
	}

	newSize := int64(float64(c.chunkSize) * factor)
	if newSize > c.cfg.AbsoluteMax {
		newSize = c.cfg.AbsoluteMax
	}
	c.chunkSize = newSize

	return newSize
}

// SpeedCategory returns a human-readable category for the connection speed
func SpeedCategory(bytesPerSecond float64) string {
	mbps := bytesPerSecond * 8 / 1_000_000 // Convert to Mbps

	switch {
	case mbps < 1:
		return "slow (< 1 Mbps)"
	case mbps < 10:
		return "mobile (1-10 Mbps)"
	case mbps < 50:
		return "home (10-50 Mbps)"
	case mbps < 200:
		return "office (50-200 Mbps)"
	case mbps < 1000:
		return "fast (200 Mbps - 1 Gbps)"
	default:
		return "datacenter (> 1 Gbps)"
	}
}

// RecommendedChunkSize returns the recommended chunk size for a given speed
func RecommendedChunkSize(bytesPerSecond float64, targetSeconds float64) int64 {
	if targetSeconds <= 0 {
		targetSeconds = 8.0
	}

	size := int64(bytesPerSecond * targetSeconds)

	// Clamp to reasonable bounds
	const minSize = 2 * 1024 * 1024   // 2 MB
	const maxSize = 256 * 1024 * 1024 // 256 MB

	if size < minSize {
		return minSize
	}
	if size > maxSize {
		return maxSize
	}

	return size
}
