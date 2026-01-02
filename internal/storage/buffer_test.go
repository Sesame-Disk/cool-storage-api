package storage

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

// =============================================================================
// SpillBuffer Tests (pure Go, no external dependencies)
// =============================================================================

// TestNewSpillBuffer tests SpillBuffer creation with various thresholds
func TestNewSpillBuffer(t *testing.T) {
	tests := []struct {
		name              string
		threshold         int64
		expectedThreshold int64
	}{
		{
			name:              "positive threshold",
			threshold:         1024,
			expectedThreshold: 1024,
		},
		{
			name:              "zero threshold uses default",
			threshold:         0,
			expectedThreshold: 16 * 1024 * 1024,
		},
		{
			name:              "negative threshold uses default",
			threshold:         -100,
			expectedThreshold: 16 * 1024 * 1024,
		},
		{
			name:              "large threshold",
			threshold:         256 * 1024 * 1024,
			expectedThreshold: 256 * 1024 * 1024,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := NewSpillBuffer(tt.threshold)
			defer buf.Close()

			if buf == nil {
				t.Fatal("NewSpillBuffer returned nil")
			}
			if buf.threshold != tt.expectedThreshold {
				t.Errorf("threshold = %d, want %d", buf.threshold, tt.expectedThreshold)
			}
			if buf.memory == nil {
				t.Error("memory buffer should be initialized")
			}
			if buf.spilled {
				t.Error("should not be spilled initially")
			}
		})
	}
}

// TestNewSpillBufferWithConfig tests SpillBuffer creation with config
func TestNewSpillBufferWithConfig(t *testing.T) {
	cfg := SpillBufferConfig{
		MemoryThreshold: 8 * 1024 * 1024,
		TempDir:         os.TempDir(),
		TempPrefix:      "test-",
	}

	buf := NewSpillBufferWithConfig(cfg)
	defer buf.Close()

	if buf.threshold != cfg.MemoryThreshold {
		t.Errorf("threshold = %d, want %d", buf.threshold, cfg.MemoryThreshold)
	}
	if buf.tempDir != cfg.TempDir {
		t.Errorf("tempDir = %s, want %s", buf.tempDir, cfg.TempDir)
	}
}

// TestDefaultSpillBufferConfig tests default config values
func TestDefaultSpillBufferConfig(t *testing.T) {
	cfg := DefaultSpillBufferConfig()

	if cfg.MemoryThreshold != 16*1024*1024 {
		t.Errorf("MemoryThreshold = %d, want 16 MB", cfg.MemoryThreshold)
	}
	if cfg.TempDir != "" {
		t.Errorf("TempDir = %s, want empty (use os.TempDir)", cfg.TempDir)
	}
	if cfg.TempPrefix != "sesamefs-" {
		t.Errorf("TempPrefix = %s, want sesamefs-", cfg.TempPrefix)
	}
}

// TestSpillBufferWriteSmall tests writing data that stays in memory
func TestSpillBufferWriteSmall(t *testing.T) {
	buf := NewSpillBuffer(1024) // 1 KB threshold
	defer buf.Close()

	data := []byte("hello world")
	n, err := buf.Write(data)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if n != len(data) {
		t.Errorf("n = %d, want %d", n, len(data))
	}
	if buf.Size() != int64(len(data)) {
		t.Errorf("Size() = %d, want %d", buf.Size(), len(data))
	}
	if !buf.InMemory() {
		t.Error("should be in memory")
	}
}

// TestSpillBufferWriteExceedsThreshold tests spilling to disk
func TestSpillBufferWriteExceedsThreshold(t *testing.T) {
	buf := NewSpillBuffer(100) // 100 byte threshold
	defer buf.Close()

	// Write data that exceeds threshold
	data := make([]byte, 150)
	for i := range data {
		data[i] = byte(i % 256)
	}

	n, err := buf.Write(data)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if n != len(data) {
		t.Errorf("n = %d, want %d", n, len(data))
	}
	if buf.InMemory() {
		t.Error("should have spilled to disk")
	}
	if buf.Size() != int64(len(data)) {
		t.Errorf("Size() = %d, want %d", buf.Size(), len(data))
	}
}

// TestSpillBufferMultipleWrites tests multiple writes with spill
func TestSpillBufferMultipleWrites(t *testing.T) {
	buf := NewSpillBuffer(100) // 100 byte threshold
	defer buf.Close()

	// First write: 50 bytes (stays in memory)
	data1 := make([]byte, 50)
	for i := range data1 {
		data1[i] = 'a'
	}
	n, err := buf.Write(data1)
	if err != nil {
		t.Fatalf("Write 1 failed: %v", err)
	}
	if n != 50 {
		t.Errorf("n = %d, want 50", n)
	}
	if !buf.InMemory() {
		t.Error("should still be in memory after first write")
	}

	// Second write: 60 bytes (causes spill)
	data2 := make([]byte, 60)
	for i := range data2 {
		data2[i] = 'b'
	}
	n, err = buf.Write(data2)
	if err != nil {
		t.Fatalf("Write 2 failed: %v", err)
	}
	if n != 60 {
		t.Errorf("n = %d, want 60", n)
	}
	if buf.InMemory() {
		t.Error("should have spilled to disk after second write")
	}

	// Third write: after spill, goes directly to file
	data3 := make([]byte, 30)
	for i := range data3 {
		data3[i] = 'c'
	}
	n, err = buf.Write(data3)
	if err != nil {
		t.Fatalf("Write 3 failed: %v", err)
	}
	if n != 30 {
		t.Errorf("n = %d, want 30", n)
	}

	// Verify total size
	expectedSize := int64(50 + 60 + 30)
	if buf.Size() != expectedSize {
		t.Errorf("Size() = %d, want %d", buf.Size(), expectedSize)
	}
}

// TestSpillBufferReader tests getting a reader for memory buffer
func TestSpillBufferReaderMemory(t *testing.T) {
	buf := NewSpillBuffer(1024)
	defer buf.Close()

	data := []byte("hello world")
	buf.Write(data)

	reader, err := buf.Reader()
	if err != nil {
		t.Fatalf("Reader failed: %v", err)
	}

	result, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	if string(result) != string(data) {
		t.Errorf("result = %q, want %q", result, data)
	}
}

// TestSpillBufferReaderDisk tests getting a reader for disk buffer
func TestSpillBufferReaderDisk(t *testing.T) {
	buf := NewSpillBuffer(50)
	defer buf.Close()

	data := []byte("this is a longer string that will spill to disk for testing purposes")
	buf.Write(data)

	if buf.InMemory() {
		t.Error("should have spilled to disk")
	}

	reader, err := buf.Reader()
	if err != nil {
		t.Fatalf("Reader failed: %v", err)
	}

	result, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	if string(result) != string(data) {
		t.Errorf("result = %q, want %q", result, data)
	}
}

// TestSpillBufferReadSeeker tests the ReadSeeker interface
func TestSpillBufferReadSeeker(t *testing.T) {
	buf := NewSpillBuffer(1024)
	defer buf.Close()

	data := []byte("hello world")
	buf.Write(data)

	seeker, err := buf.ReadSeeker()
	if err != nil {
		t.Fatalf("ReadSeeker failed: %v", err)
	}

	// Read first 5 bytes
	first := make([]byte, 5)
	n, err := seeker.Read(first)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if n != 5 || string(first) != "hello" {
		t.Errorf("first = %q, want hello", first)
	}

	// Seek back to start
	pos, err := seeker.Seek(0, io.SeekStart)
	if err != nil {
		t.Fatalf("Seek failed: %v", err)
	}
	if pos != 0 {
		t.Errorf("pos = %d, want 0", pos)
	}

	// Read again
	again := make([]byte, 5)
	n, err = seeker.Read(again)
	if err != nil {
		t.Fatalf("Read again failed: %v", err)
	}
	if n != 5 || string(again) != "hello" {
		t.Errorf("again = %q, want hello", again)
	}
}

// TestSpillBufferBytes tests the Bytes method
func TestSpillBufferBytesMemory(t *testing.T) {
	buf := NewSpillBuffer(1024)
	defer buf.Close()

	data := []byte("hello world")
	buf.Write(data)

	result, err := buf.Bytes()
	if err != nil {
		t.Fatalf("Bytes failed: %v", err)
	}
	if string(result) != string(data) {
		t.Errorf("result = %q, want %q", result, data)
	}
}

// TestSpillBufferBytesDisk tests Bytes when spilled to disk
func TestSpillBufferBytesDisk(t *testing.T) {
	buf := NewSpillBuffer(50)
	defer buf.Close()

	data := []byte("this is a longer string that will spill to disk")
	buf.Write(data)

	result, err := buf.Bytes()
	if err != nil {
		t.Fatalf("Bytes failed: %v", err)
	}
	if string(result) != string(data) {
		t.Errorf("result = %q, want %q", result, data)
	}
}

// TestSpillBufferClose tests closing and cleanup
func TestSpillBufferClose(t *testing.T) {
	buf := NewSpillBuffer(50)

	// Write enough to spill
	data := make([]byte, 100)
	buf.Write(data)

	if buf.InMemory() {
		t.Error("should have spilled to disk")
	}

	// Get the temp file path before closing
	buf.mu.Lock()
	filePath := buf.filePath
	buf.mu.Unlock()

	if filePath == "" {
		t.Fatal("filePath should not be empty after spill")
	}

	// Verify file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Fatal("temp file should exist")
	}

	// Close the buffer
	err := buf.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// Verify file is deleted
	if _, err := os.Stat(filePath); !os.IsNotExist(err) {
		t.Error("temp file should be deleted after Close")
	}

	// Second close should be safe
	err = buf.Close()
	if err != nil {
		t.Errorf("Second Close failed: %v", err)
	}
}

// TestSpillBufferWriteAfterClose tests writing to closed buffer
func TestSpillBufferWriteAfterClose(t *testing.T) {
	buf := NewSpillBuffer(1024)
	buf.Close()

	_, err := buf.Write([]byte("test"))
	if err == nil {
		t.Error("Write after Close should fail")
	}
}

// TestSpillBufferReaderAfterClose tests getting reader from closed buffer
func TestSpillBufferReaderAfterClose(t *testing.T) {
	buf := NewSpillBuffer(1024)
	buf.Close()

	_, err := buf.Reader()
	if err == nil {
		t.Error("Reader after Close should fail")
	}
}

// TestSpillBufferReset tests resetting the buffer
func TestSpillBufferReset(t *testing.T) {
	buf := NewSpillBuffer(50)
	defer buf.Close()

	// Write and spill
	data := make([]byte, 100)
	buf.Write(data)
	if buf.InMemory() {
		t.Error("should have spilled")
	}

	// Get temp file path
	buf.mu.Lock()
	filePath := buf.filePath
	buf.mu.Unlock()

	// Reset
	err := buf.Reset()
	if err != nil {
		t.Fatalf("Reset failed: %v", err)
	}

	// Verify state is reset
	if buf.Size() != 0 {
		t.Errorf("Size after reset = %d, want 0", buf.Size())
	}
	if !buf.InMemory() {
		t.Error("should be in memory after reset")
	}

	// Verify temp file is deleted
	if filePath != "" {
		if _, err := os.Stat(filePath); !os.IsNotExist(err) {
			t.Error("temp file should be deleted after Reset")
		}
	}

	// Write again
	buf.Write([]byte("new data"))
	if buf.Size() != 8 {
		t.Errorf("Size after new write = %d, want 8", buf.Size())
	}
}

// TestSpillBufferString tests the String method for debugging
func TestSpillBufferString(t *testing.T) {
	buf := NewSpillBuffer(100)
	defer buf.Close()

	// In memory
	buf.Write([]byte("hello"))
	str := buf.String()
	if !strings.Contains(str, "size=5") {
		t.Errorf("String should contain size=5, got %s", str)
	}
	if !strings.Contains(str, "spilled=false") {
		t.Errorf("String should contain spilled=false, got %s", str)
	}

	// After spill
	buf.Write(make([]byte, 200))
	str = buf.String()
	if !strings.Contains(str, "spilled=true") {
		t.Errorf("String should contain spilled=true, got %s", str)
	}
}

// TestSpillBufferConcurrentWrites tests concurrent write safety
func TestSpillBufferConcurrentWrites(t *testing.T) {
	buf := NewSpillBuffer(1000)
	defer buf.Close()

	done := make(chan bool)
	numGoroutines := 10
	writesPerGoroutine := 100

	for i := 0; i < numGoroutines; i++ {
		go func() {
			for j := 0; j < writesPerGoroutine; j++ {
				buf.Write([]byte("x"))
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < numGoroutines; i++ {
		<-done
	}

	expectedSize := int64(numGoroutines * writesPerGoroutine)
	if buf.Size() != expectedSize {
		t.Errorf("Size = %d, want %d", buf.Size(), expectedSize)
	}
}

// TestSpillBufferLargeData tests with larger data to verify disk behavior
func TestSpillBufferLargeData(t *testing.T) {
	buf := NewSpillBuffer(1024) // 1 KB threshold
	defer buf.Close()

	// Write 10 KB of data
	data := make([]byte, 10*1024)
	for i := range data {
		data[i] = byte(i % 256)
	}

	n, err := buf.Write(data)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	if n != len(data) {
		t.Errorf("n = %d, want %d", n, len(data))
	}

	// Read back and verify
	result, err := buf.Bytes()
	if err != nil {
		t.Fatalf("Bytes failed: %v", err)
	}
	if !bytes.Equal(result, data) {
		t.Error("data mismatch after read")
	}
}

// TestSpillBufferMultipleReads tests reading multiple times
func TestSpillBufferMultipleReads(t *testing.T) {
	buf := NewSpillBuffer(50)
	defer buf.Close()

	data := []byte("test data that will spill to disk for multiple reads")
	buf.Write(data)

	// Read multiple times
	for i := 0; i < 3; i++ {
		reader, err := buf.Reader()
		if err != nil {
			t.Fatalf("Reader %d failed: %v", i, err)
		}
		result, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("ReadAll %d failed: %v", i, err)
		}
		if string(result) != string(data) {
			t.Errorf("Read %d: result = %q, want %q", i, result, data)
		}
	}
}

// TestSpillBufferEmptyBuffer tests operations on empty buffer
func TestSpillBufferEmptyBuffer(t *testing.T) {
	buf := NewSpillBuffer(1024)
	defer buf.Close()

	if buf.Size() != 0 {
		t.Errorf("Size = %d, want 0", buf.Size())
	}
	if !buf.InMemory() {
		t.Error("empty buffer should be in memory")
	}

	reader, err := buf.Reader()
	if err != nil {
		t.Fatalf("Reader failed: %v", err)
	}
	result, _ := io.ReadAll(reader)
	if len(result) != 0 {
		t.Errorf("empty buffer should return empty reader, got %d bytes", len(result))
	}
}
