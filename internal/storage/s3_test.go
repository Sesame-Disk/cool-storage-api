package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
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
	if MultipartUploadConcurrency != 4 {
		t.Errorf("MultipartUploadConcurrency = %d, want 4", MultipartUploadConcurrency)
	}
	if MaxMultipartParts != 10000 {
		t.Errorf("MaxMultipartParts = %d, want 10000", MaxMultipartParts)
	}
}

type fakeMultipartUploadClient struct {
	mu              sync.Mutex
	createCalls     int
	completeCalls   int
	abortCalls      int
	maxConcurrent   int
	inFlight        int
	failPartNumber  int32
	completedParts  []types.CompletedPart
	uploadedPartIDs []int32
	startedCh       chan int32
	releaseCh       chan struct{}
}

func (f *fakeMultipartUploadClient) CreateMultipartUpload(_ context.Context, _ *s3.CreateMultipartUploadInput, _ ...func(*s3.Options)) (*s3.CreateMultipartUploadOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.createCalls++
	return &s3.CreateMultipartUploadOutput{UploadId: aws.String("upload-1")}, nil
}

func (f *fakeMultipartUploadClient) UploadPart(ctx context.Context, input *s3.UploadPartInput, _ ...func(*s3.Options)) (*s3.UploadPartOutput, error) {
	partNumber := aws.ToInt32(input.PartNumber)
	if _, err := io.ReadAll(input.Body); err != nil {
		return nil, err
	}

	f.mu.Lock()
	f.inFlight++
	if f.inFlight > f.maxConcurrent {
		f.maxConcurrent = f.inFlight
	}
	f.uploadedPartIDs = append(f.uploadedPartIDs, partNumber)
	f.mu.Unlock()

	defer func() {
		f.mu.Lock()
		f.inFlight--
		f.mu.Unlock()
	}()

	if f.startedCh != nil {
		f.startedCh <- partNumber
	}
	if f.releaseCh != nil {
		select {
		case <-f.releaseCh:
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
			return nil, fmt.Errorf("timed out waiting to release part %d", partNumber)
		}
	}
	if f.failPartNumber != 0 && partNumber == f.failPartNumber {
		return nil, fmt.Errorf("boom on part %d", partNumber)
	}

	return &s3.UploadPartOutput{
		ETag: aws.String(fmt.Sprintf("etag-%d", partNumber)),
	}, nil
}

func (f *fakeMultipartUploadClient) CompleteMultipartUpload(_ context.Context, input *s3.CompleteMultipartUploadInput, _ ...func(*s3.Options)) (*s3.CompleteMultipartUploadOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.completeCalls++
	if input.MultipartUpload != nil {
		f.completedParts = append([]types.CompletedPart(nil), input.MultipartUpload.Parts...)
	}
	return &s3.CompleteMultipartUploadOutput{}, nil
}

func (f *fakeMultipartUploadClient) AbortMultipartUpload(_ context.Context, _ *s3.AbortMultipartUploadInput, _ ...func(*s3.Options)) (*s3.AbortMultipartUploadOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.abortCalls++
	return &s3.AbortMultipartUploadOutput{}, nil
}

func TestPutLargeUploadsPartsConcurrently(t *testing.T) {
	store := &S3Store{bucket: "test-bucket"}
	client := &fakeMultipartUploadClient{
		startedCh: make(chan int32, 4),
		releaseCh: make(chan struct{}),
	}
	size := int64(MultipartPartSize + 1)
	reader := bytes.NewReader(make([]byte, int(size)))

	type result struct {
		key string
		err error
	}
	resultCh := make(chan result, 1)
	go func() {
		key, err := store.putLargeWithClient(context.Background(), client, "block-123", reader, size)
		resultCh <- result{key: key, err: err}
	}()

	for i := 0; i < 2; i++ {
		select {
		case <-client.startedCh:
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for multipart upload part %d to start", i+1)
		}
	}
	close(client.releaseCh)

	res := <-resultCh
	key, err := res.key, res.err
	if err != nil {
		t.Fatalf("putLargeWithClient() error = %v", err)
	}
	if key != "block-123" {
		t.Fatalf("key = %q, want %q", key, "block-123")
	}
	if client.createCalls != 1 {
		t.Fatalf("CreateMultipartUpload calls = %d, want 1", client.createCalls)
	}
	if client.completeCalls != 1 {
		t.Fatalf("CompleteMultipartUpload calls = %d, want 1", client.completeCalls)
	}
	if client.abortCalls != 0 {
		t.Fatalf("AbortMultipartUpload calls = %d, want 0", client.abortCalls)
	}
	if client.maxConcurrent < 2 {
		t.Fatalf("max concurrent part uploads = %d, want at least 2", client.maxConcurrent)
	}
	if len(client.completedParts) != 2 {
		t.Fatalf("completed parts = %d, want 2", len(client.completedParts))
	}
	for i, part := range client.completedParts {
		wantPartNumber := int32(i + 1)
		if aws.ToInt32(part.PartNumber) != wantPartNumber {
			t.Fatalf("completed part[%d] number = %d, want %d", i, aws.ToInt32(part.PartNumber), wantPartNumber)
		}
		wantETag := fmt.Sprintf("etag-%d", wantPartNumber)
		if aws.ToString(part.ETag) != wantETag {
			t.Fatalf("completed part[%d] etag = %q, want %q", i, aws.ToString(part.ETag), wantETag)
		}
	}
}

func TestPutLargeAbortsMultipartUploadOnPartFailure(t *testing.T) {
	store := &S3Store{bucket: "test-bucket"}
	client := &fakeMultipartUploadClient{failPartNumber: 2}
	size := int64(MultipartPartSize + 1)
	reader := bytes.NewReader(make([]byte, int(size)))

	_, err := store.putLargeWithClient(context.Background(), client, "block-err", reader, size)
	if err == nil {
		t.Fatal("putLargeWithClient() error = nil, want failure")
	}
	if !strings.Contains(err.Error(), "failed to upload part 2") {
		t.Fatalf("error = %v, want upload part 2 failure", err)
	}
	if client.createCalls != 1 {
		t.Fatalf("CreateMultipartUpload calls = %d, want 1", client.createCalls)
	}
	if client.completeCalls != 0 {
		t.Fatalf("CompleteMultipartUpload calls = %d, want 0", client.completeCalls)
	}
	if client.abortCalls != 1 {
		t.Fatalf("AbortMultipartUpload calls = %d, want 1", client.abortCalls)
	}
}

func TestPutLargeAbortsWhenSourceShorterThanSize(t *testing.T) {
	store := &S3Store{bucket: "test-bucket"}
	client := &fakeMultipartUploadClient{}
	// Declare two parts' worth of size but only feed one part of data.
	size := int64(MultipartPartSize + 100)
	reader := bytes.NewReader(make([]byte, MultipartPartSize))

	_, err := store.putLargeWithClient(context.Background(), client, "block-short", reader, size)
	if err == nil {
		t.Fatal("putLargeWithClient() error = nil, want failure for short source")
	}
	if !strings.Contains(err.Error(), "empty part") {
		t.Fatalf("error = %v, want empty part failure", err)
	}
	if client.completeCalls != 0 {
		t.Fatalf("CompleteMultipartUpload calls = %d, want 0", client.completeCalls)
	}
	if client.abortCalls != 1 {
		t.Fatalf("AbortMultipartUpload calls = %d, want 1", client.abortCalls)
	}
}

func TestPutLargeRejectsNonPositiveSize(t *testing.T) {
	store := &S3Store{bucket: "test-bucket"}
	client := &fakeMultipartUploadClient{}

	_, err := store.putLargeWithClient(context.Background(), client, "block-zero", bytes.NewReader(nil), 0)
	if err == nil {
		t.Fatal("putLargeWithClient() error = nil, want failure for size 0")
	}
	if !strings.Contains(err.Error(), "positive size") {
		t.Fatalf("error = %v, want positive size failure", err)
	}
	if client.createCalls != 0 {
		t.Fatalf("CreateMultipartUpload calls = %d, want 0", client.createCalls)
	}
	if client.completeCalls != 0 {
		t.Fatalf("CompleteMultipartUpload calls = %d, want 0", client.completeCalls)
	}
	if client.abortCalls != 0 {
		t.Fatalf("AbortMultipartUpload calls = %d, want 0", client.abortCalls)
	}
}

// fatalOnReadReader fails the test if it is ever read; used to prove the part-count
// guard short-circuits before consuming the (enormous) source stream.
type fatalOnReadReader struct{ t *testing.T }

func (r fatalOnReadReader) Read(_ []byte) (int, error) {
	r.t.Fatal("source must not be read when part count exceeds the S3 limit")
	return 0, io.EOF
}

func TestPutLargeRejectsTooManyParts(t *testing.T) {
	store := &S3Store{bucket: "test-bucket"}
	client := &fakeMultipartUploadClient{}
	// One part beyond the S3 limit, so the guard must reject before any read.
	size := int64(MaxMultipartParts+1) * int64(MultipartPartSize)

	_, err := store.putLargeWithClient(context.Background(), client, "block-huge", fatalOnReadReader{t: t}, size)
	if err == nil {
		t.Fatal("putLargeWithClient() error = nil, want failure for too many parts")
	}
	if !strings.Contains(err.Error(), "exceeding the S3 limit") {
		t.Fatalf("error = %v, want S3 part-limit failure", err)
	}
	if client.createCalls != 0 {
		t.Fatalf("CreateMultipartUpload calls = %d, want 0", client.createCalls)
	}
	if client.completeCalls != 0 {
		t.Fatalf("CompleteMultipartUpload calls = %d, want 0", client.completeCalls)
	}
	if client.abortCalls != 0 {
		t.Fatalf("AbortMultipartUpload calls = %d, want 0", client.abortCalls)
	}
}

func TestPutLargeAbortsOnContextCancellation(t *testing.T) {
	store := &S3Store{bucket: "test-bucket"}
	client := &fakeMultipartUploadClient{
		startedCh: make(chan int32, 4),
		releaseCh: make(chan struct{}), // never closed: parts block until ctx is cancelled
	}
	size := int64(MultipartPartSize + 1)
	reader := bytes.NewReader(make([]byte, int(size)))

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := store.putLargeWithClient(ctx, client, "block-cancel", reader, size)
		errCh <- err
	}()

	// Wait until at least one part is in flight, then cancel mid-upload.
	select {
	case <-client.startedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a part upload to start")
	}
	cancel()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("putLargeWithClient() error = nil, want cancellation failure")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for putLargeWithClient to return after cancel")
	}

	if client.completeCalls != 0 {
		t.Fatalf("CompleteMultipartUpload calls = %d, want 0", client.completeCalls)
	}
	if client.abortCalls != 1 {
		t.Fatalf("AbortMultipartUpload calls = %d, want 1 (abort must run on a fresh context)", client.abortCalls)
	}
}

func TestValidateMultipartUploadSize(t *testing.T) {
	tests := []struct {
		name      string
		size      int64
		wantParts int
		wantErr   string
	}{
		{name: "one byte", size: 1, wantParts: 1},
		{name: "exactly one part", size: MultipartPartSize, wantParts: 1},
		{name: "round up", size: MultipartPartSize + 1, wantParts: 2},
		{name: "max valid parts", size: int64(MaxMultipartParts) * int64(MultipartPartSize), wantParts: MaxMultipartParts},
		{name: "zero", size: 0, wantErr: "positive size"},
		{name: "negative", size: -1, wantErr: "positive size"},
		{name: "too many parts", size: int64(MaxMultipartParts)*int64(MultipartPartSize) + 1, wantErr: "exceeding the S3 limit"},
		{name: "near int64 max does not overflow", size: math.MaxInt64, wantErr: "exceeding the S3 limit"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validateMultipartUploadSize(tt.size)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("validateMultipartUploadSize(%d) error = nil, want %q", tt.size, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("validateMultipartUploadSize(%d) error = %v, want substring %q", tt.size, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("validateMultipartUploadSize(%d) error = %v", tt.size, err)
			}
			if got != tt.wantParts {
				t.Fatalf("validateMultipartUploadSize(%d) = %d, want %d", tt.size, got, tt.wantParts)
			}
		})
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
		Endpoint:             "http://localhost:9000",
		Bucket:               "test-bucket",
		Region:               "us-east-1",
		AccessKeyID:          "minioadmin",
		SecretAccessKey:      "minioadmin",
		Prefix:               "test/",
		AccessType:           AccessImmediate,
		UsePathStyle:         true,
		ServerSideEncryption: "AES256",
		SSEKMSKeyID:          "",
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
	if cfg.ServerSideEncryption != "AES256" {
		t.Errorf("ServerSideEncryption = %s, want AES256", cfg.ServerSideEncryption)
	}
}

func TestNormalizeServerSideEncryption(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    types.ServerSideEncryption
		wantErr bool
	}{
		{name: "empty", input: "", want: ""},
		{name: "aes256", input: "AES256", want: types.ServerSideEncryptionAes256},
		{name: "kms", input: "aws:kms", want: types.ServerSideEncryptionAwsKms},
		{name: "invalid", input: "kms", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeServerSideEncryption(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("normalizeServerSideEncryption(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("normalizeServerSideEncryption(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestApplySSEToInputs(t *testing.T) {
	store := &S3Store{
		serverSideEncryption: types.ServerSideEncryptionAwsKms,
		sseKMSKeyID:          "arn:aws:kms:us-east-1:123456789012:key/test",
	}

	putInput := &s3.PutObjectInput{}
	store.applySSEToPutObjectInput(putInput)
	if putInput.ServerSideEncryption != types.ServerSideEncryptionAwsKms {
		t.Fatalf("PutObjectInput.ServerSideEncryption = %q, want %q", putInput.ServerSideEncryption, types.ServerSideEncryptionAwsKms)
	}
	if putInput.SSEKMSKeyId == nil || *putInput.SSEKMSKeyId != store.sseKMSKeyID {
		t.Fatalf("PutObjectInput.SSEKMSKeyId = %v, want %q", putInput.SSEKMSKeyId, store.sseKMSKeyID)
	}

	createInput := &s3.CreateMultipartUploadInput{}
	store.applySSEToCreateMultipartUploadInput(createInput)
	if createInput.ServerSideEncryption != types.ServerSideEncryptionAwsKms {
		t.Fatalf("CreateMultipartUploadInput.ServerSideEncryption = %q, want %q", createInput.ServerSideEncryption, types.ServerSideEncryptionAwsKms)
	}
	if createInput.SSEKMSKeyId == nil || *createInput.SSEKMSKeyId != store.sseKMSKeyID {
		t.Fatalf("CreateMultipartUploadInput.SSEKMSKeyId = %v, want %q", createInput.SSEKMSKeyId, store.sseKMSKeyID)
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
