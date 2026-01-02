package storage

import (
	"errors"
	"io"
	"testing"
	"time"
)

var testTime = time.Date(2025, 12, 30, 10, 0, 0, 0, time.UTC)

// =============================================================================
// S3 Helper Function Tests (pure Go, no external dependencies)
// =============================================================================

// TestMultipartConstants tests the multipart upload constants
func TestMultipartConstants(t *testing.T) {
	if MultipartThreshold != 100*1024*1024 {
		t.Errorf("MultipartThreshold = %d, want 100 MB", MultipartThreshold)
	}
	if MultipartPartSize != 16*1024*1024 {
		t.Errorf("MultipartPartSize = %d, want 16 MB", MultipartPartSize)
	}
}

// TestBytesReadSeeker tests the bytesReadSeeker implementation
func TestBytesReadSeeker(t *testing.T) {
	data := []byte("hello world")
	r := &bytesReadSeeker{data: data}

	// Test Read
	buf := make([]byte, 5)
	n, err := r.Read(buf)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if n != 5 || string(buf) != "hello" {
		t.Errorf("Read = %q, want hello", buf)
	}

	// Test Seek to start
	pos, err := r.Seek(0, io.SeekStart)
	if err != nil {
		t.Fatalf("Seek failed: %v", err)
	}
	if pos != 0 {
		t.Errorf("Seek pos = %d, want 0", pos)
	}

	// Read again
	n, err = r.Read(buf)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if string(buf) != "hello" {
		t.Errorf("Read after seek = %q, want hello", buf)
	}

	// Test Seek from current
	pos, err = r.Seek(1, io.SeekCurrent)
	if err != nil {
		t.Fatalf("Seek current failed: %v", err)
	}
	if pos != 6 {
		t.Errorf("Seek current pos = %d, want 6", pos)
	}

	// Test Seek from end
	pos, err = r.Seek(-5, io.SeekEnd)
	if err != nil {
		t.Fatalf("Seek end failed: %v", err)
	}
	if pos != 6 { // len("hello world") - 5 = 6
		t.Errorf("Seek end pos = %d, want 6", pos)
	}

	// Read remaining
	buf = make([]byte, 10)
	n, err = r.Read(buf)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if n != 5 || string(buf[:n]) != "world" {
		t.Errorf("Read from 6 = %q, want world", buf[:n])
	}

	// Read at EOF
	n, err = r.Read(buf)
	if err != io.EOF {
		t.Errorf("Read at EOF err = %v, want EOF", err)
	}
}

// TestBytesReadSeekerErrors tests error cases
func TestBytesReadSeekerErrors(t *testing.T) {
	data := []byte("test")
	r := &bytesReadSeeker{data: data}

	// Invalid whence
	_, err := r.Seek(0, 999)
	if err == nil {
		t.Error("expected error for invalid whence")
	}

	// Negative offset
	_, err = r.Seek(-100, io.SeekStart)
	if err == nil {
		t.Error("expected error for negative offset")
	}
}

// TestIsNotFoundError tests the isNotFoundError helper function
func TestIsNotFoundError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "NotFound in message",
			err:      errors.New("S3Error: NotFound"),
			expected: true,
		},
		{
			name:     "404 in message",
			err:      errors.New("HTTP 404: Not Found"),
			expected: true,
		},
		{
			name:     "NoSuchKey in message",
			err:      errors.New("NoSuchKey: The specified key does not exist"),
			expected: true,
		},
		{
			name:     "generic error",
			err:      errors.New("connection refused"),
			expected: false,
		},
		{
			name:     "empty error",
			err:      errors.New(""),
			expected: false,
		},
		{
			name:     "case sensitive NotFound",
			err:      errors.New("notfound"),
			expected: false, // Case sensitive
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isNotFoundError(tt.err)
			if result != tt.expected {
				t.Errorf("isNotFoundError(%v) = %v, want %v", tt.err, result, tt.expected)
			}
		})
	}
}

// TestContains tests the contains helper function
func TestContains(t *testing.T) {
	tests := []struct {
		s        string
		substr   string
		expected bool
	}{
		{"hello world", "world", true},
		{"hello world", "hello", true},
		{"hello world", "xyz", false},
		{"", "", true},
		{"abc", "", true},
		{"", "abc", false},
		{"abc", "abc", true},
		{"abcdef", "cde", true},
		{"ABC", "abc", false}, // Case sensitive
	}

	for _, tt := range tests {
		t.Run(tt.s+"_"+tt.substr, func(t *testing.T) {
			result := contains(tt.s, tt.substr)
			if result != tt.expected {
				t.Errorf("contains(%q, %q) = %v, want %v", tt.s, tt.substr, result, tt.expected)
			}
		})
	}
}

// TestContainsHelper tests the containsHelper function
func TestContainsHelper(t *testing.T) {
	tests := []struct {
		s        string
		substr   string
		expected bool
	}{
		{"hello world", "world", true},
		{"hello world", "hello", true},
		{"hello world", "xyz", false},
		{"abcdef", "cde", true},
		{"abcdef", "def", true},
		{"abcdef", "abc", true},
		{"a", "a", true},
		{"ab", "b", true},
	}

	for _, tt := range tests {
		t.Run(tt.s+"_"+tt.substr, func(t *testing.T) {
			result := containsHelper(tt.s, tt.substr)
			if result != tt.expected {
				t.Errorf("containsHelper(%q, %q) = %v, want %v", tt.s, tt.substr, result, tt.expected)
			}
		})
	}
}

// TestS3StoreKey tests the key building function
func TestS3StoreKey(t *testing.T) {
	tests := []struct {
		name     string
		prefix   string
		blockID  string
		expected string
	}{
		{
			name:     "no prefix",
			prefix:   "",
			blockID:  "abc123",
			expected: "abc123",
		},
		{
			name:     "with prefix",
			prefix:   "org-1",
			blockID:  "abc123",
			expected: "org-1/abc123",
		},
		{
			name:     "sha256 block ID",
			prefix:   "blocks",
			blockID:  "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			expected: "blocks/e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &S3Store{prefix: tt.prefix}
			result := store.key(tt.blockID)
			if result != tt.expected {
				t.Errorf("key(%q) = %q, want %q", tt.blockID, result, tt.expected)
			}
		})
	}
}

// TestS3Config tests the S3Config struct
func TestS3Config(t *testing.T) {
	cfg := S3Config{
		Endpoint:        "http://localhost:9000",
		Bucket:          "test-bucket",
		Region:          "us-east-1",
		AccessKeyID:     "minioadmin",
		SecretAccessKey: "minioadmin",
		Prefix:          "test/",
		AccessType:      AccessImmediate,
		UsePathStyle:    true,
	}

	if cfg.Endpoint != "http://localhost:9000" {
		t.Errorf("Endpoint = %s, want http://localhost:9000", cfg.Endpoint)
	}
	if cfg.Bucket != "test-bucket" {
		t.Errorf("Bucket = %s, want test-bucket", cfg.Bucket)
	}
	if cfg.AccessType != AccessImmediate {
		t.Errorf("AccessType = %s, want %s", cfg.AccessType, AccessImmediate)
	}
	if !cfg.UsePathStyle {
		t.Error("UsePathStyle should be true")
	}
}

// TestAccessTypeConstants tests the AccessType constants
func TestAccessTypeConstants(t *testing.T) {
	if AccessImmediate != "hot" {
		t.Errorf("AccessImmediate = %s, want hot", AccessImmediate)
	}
	if AccessDelayed != "cold" {
		t.Errorf("AccessDelayed = %s, want cold", AccessDelayed)
	}
}

// TestS3StoreGetAccessType tests the GetAccessType method
func TestS3StoreGetAccessType(t *testing.T) {
	tests := []struct {
		name       string
		accessType AccessType
	}{
		{"hot storage", AccessImmediate},
		{"cold storage", AccessDelayed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &S3Store{accessType: tt.accessType}
			if store.GetAccessType() != tt.accessType {
				t.Errorf("GetAccessType() = %s, want %s", store.GetAccessType(), tt.accessType)
			}
		})
	}
}

// TestS3StoreBucket tests the Bucket method
func TestS3StoreBucket(t *testing.T) {
	store := &S3Store{bucket: "my-bucket"}
	if store.Bucket() != "my-bucket" {
		t.Errorf("Bucket() = %s, want my-bucket", store.Bucket())
	}
}

// TestPresignedURLStruct tests the PresignedURL struct
func TestPresignedURLStruct(t *testing.T) {
	url := PresignedURL{
		URL:       "https://s3.example.com/bucket/key?signature=abc",
		ExpiresAt: testTime,
	}

	if url.URL != "https://s3.example.com/bucket/key?signature=abc" {
		t.Errorf("URL mismatch")
	}
}

// TestObjectInfoStruct tests the ObjectInfo struct
func TestObjectInfoStruct(t *testing.T) {
	info := ObjectInfo{
		Key:          "path/to/file.txt",
		Size:         1024,
		LastModified: testTime,
		IsDirectory:  false,
	}

	if info.Key != "path/to/file.txt" {
		t.Errorf("Key = %s, want path/to/file.txt", info.Key)
	}
	if info.Size != 1024 {
		t.Errorf("Size = %d, want 1024", info.Size)
	}
	if info.IsDirectory {
		t.Error("IsDirectory should be false")
	}

	// Directory info
	dirInfo := ObjectInfo{
		Key:         "path/to/dir/",
		IsDirectory: true,
	}
	if !dirInfo.IsDirectory {
		t.Error("IsDirectory should be true for directory")
	}
}
