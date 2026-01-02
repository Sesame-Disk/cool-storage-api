package storage

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"sync"
)

// SpillBuffer is a hybrid memory/disk buffer that starts in memory
// and spills to a temporary file when the threshold is exceeded.
// This is similar to MySQL's tmp_table_size behavior.
//
// Usage:
//
//	buf := NewSpillBuffer(16*1024*1024) // 16 MB threshold
//	defer buf.Close()                    // Cleanup temp file if created
//
//	io.Copy(buf, someReader)             // Write data
//	reader := buf.Reader()               // Get reader for the data
//	size := buf.Size()                   // Get total size written
type SpillBuffer struct {
	threshold int64  // Size threshold before spilling to disk
	tempDir   string // Directory for temp files

	mu       sync.Mutex
	memory   *bytes.Buffer // In-memory buffer (used when small)
	file     *os.File      // Temp file (used when large)
	size     int64         // Total bytes written
	spilled  bool          // Whether we've spilled to disk
	closed   bool          // Whether Close() has been called
	filePath string        // Path to temp file (for cleanup)
}

// SpillBufferConfig holds configuration for SpillBuffer
type SpillBufferConfig struct {
	MemoryThreshold int64  // Bytes threshold before spilling to disk (default: 16 MB)
	TempDir         string // Directory for temp files (default: os.TempDir())
	TempPrefix      string // Prefix for temp files (default: "sesamefs-")
}

// DefaultSpillBufferConfig returns sensible defaults
func DefaultSpillBufferConfig() SpillBufferConfig {
	return SpillBufferConfig{
		MemoryThreshold: 16 * 1024 * 1024, // 16 MB
		TempDir:         "",               // Use os.TempDir()
		TempPrefix:      "sesamefs-",
	}
}

// NewSpillBuffer creates a new SpillBuffer with the given threshold.
// Data smaller than threshold stays in memory; larger data spills to disk.
func NewSpillBuffer(threshold int64) *SpillBuffer {
	if threshold <= 0 {
		threshold = 16 * 1024 * 1024 // 16 MB default
	}
	return &SpillBuffer{
		threshold: threshold,
		tempDir:   os.TempDir(),
		memory:    &bytes.Buffer{},
	}
}

// NewSpillBufferWithConfig creates a SpillBuffer with custom configuration
func NewSpillBufferWithConfig(cfg SpillBufferConfig) *SpillBuffer {
	if cfg.MemoryThreshold <= 0 {
		cfg.MemoryThreshold = 16 * 1024 * 1024
	}
	tempDir := cfg.TempDir
	if tempDir == "" {
		tempDir = os.TempDir()
	}
	return &SpillBuffer{
		threshold: cfg.MemoryThreshold,
		tempDir:   tempDir,
		memory:    &bytes.Buffer{},
	}
}

// Write implements io.Writer
func (b *SpillBuffer) Write(p []byte) (n int, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return 0, fmt.Errorf("buffer is closed")
	}

	// If already spilled, write directly to file
	if b.spilled {
		n, err = b.file.Write(p)
		b.size += int64(n)
		return n, err
	}

	// Check if this write would exceed threshold
	newSize := b.size + int64(len(p))
	if newSize > b.threshold {
		// Spill to disk
		if err := b.spillToDisk(); err != nil {
			return 0, fmt.Errorf("failed to spill to disk: %w", err)
		}
		n, err = b.file.Write(p)
		b.size += int64(n)
		return n, err
	}

	// Still under threshold, write to memory
	n, err = b.memory.Write(p)
	b.size += int64(n)
	return n, err
}

// spillToDisk creates a temp file and copies memory contents to it
func (b *SpillBuffer) spillToDisk() error {
	if b.spilled {
		return nil
	}

	// Create temp file
	file, err := os.CreateTemp(b.tempDir, "sesamefs-upload-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}

	b.filePath = file.Name()

	// Copy existing memory contents to file
	if b.memory.Len() > 0 {
		if _, err := file.Write(b.memory.Bytes()); err != nil {
			file.Close()
			os.Remove(file.Name())
			return fmt.Errorf("failed to write memory to file: %w", err)
		}
	}

	// Switch to file mode
	b.file = file
	b.spilled = true
	b.memory = nil // Release memory

	return nil
}

// Reader returns an io.Reader for the buffered data.
// For memory buffers, this is a bytes.Reader.
// For file buffers, this seeks to the beginning and returns the file.
func (b *SpillBuffer) Reader() (io.Reader, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return nil, fmt.Errorf("buffer is closed")
	}

	if b.spilled {
		// Seek to beginning of file
		if _, err := b.file.Seek(0, io.SeekStart); err != nil {
			return nil, fmt.Errorf("failed to seek: %w", err)
		}
		return b.file, nil
	}

	// Return a reader over the memory buffer
	return bytes.NewReader(b.memory.Bytes()), nil
}

// ReadSeeker returns an io.ReadSeeker for the buffered data.
// Useful for S3 uploads that need seeking.
func (b *SpillBuffer) ReadSeeker() (io.ReadSeeker, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return nil, fmt.Errorf("buffer is closed")
	}

	if b.spilled {
		if _, err := b.file.Seek(0, io.SeekStart); err != nil {
			return nil, fmt.Errorf("failed to seek: %w", err)
		}
		return b.file, nil
	}

	return bytes.NewReader(b.memory.Bytes()), nil
}

// Size returns the total number of bytes written
func (b *SpillBuffer) Size() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.size
}

// InMemory returns true if the data is still in memory (not spilled to disk)
func (b *SpillBuffer) InMemory() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return !b.spilled
}

// Bytes returns the buffered data as a byte slice.
// For memory buffers, this is efficient (no copy).
// For file buffers, this reads the entire file into memory (use with caution).
func (b *SpillBuffer) Bytes() ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return nil, fmt.Errorf("buffer is closed")
	}

	if !b.spilled {
		return b.memory.Bytes(), nil
	}

	// Read entire file into memory (expensive for large files)
	if _, err := b.file.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("failed to seek: %w", err)
	}
	return io.ReadAll(b.file)
}

// Close releases resources and deletes any temp file
func (b *SpillBuffer) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return nil
	}

	b.closed = true

	if b.file != nil {
		b.file.Close()
		if b.filePath != "" {
			os.Remove(b.filePath)
		}
	}

	b.memory = nil
	return nil
}

// Reset clears the buffer for reuse
func (b *SpillBuffer) Reset() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Clean up any temp file
	if b.file != nil {
		b.file.Close()
		if b.filePath != "" {
			os.Remove(b.filePath)
		}
		b.file = nil
		b.filePath = ""
	}

	b.spilled = false
	b.closed = false
	b.size = 0
	b.memory = &bytes.Buffer{}

	return nil
}

// String returns a description of the buffer state (for debugging)
func (b *SpillBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.spilled {
		return fmt.Sprintf("SpillBuffer{size=%d, spilled=true, file=%s}", b.size, b.filePath)
	}
	return fmt.Sprintf("SpillBuffer{size=%d, spilled=false, memory=true}", b.size)
}
