package storage

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"golang.org/x/sync/errgroup"
)

// S3Store implements the Store interface for S3-compatible storage
type S3Store struct {
	client               *s3.Client
	presignClient        *s3.PresignClient
	bucket               string
	prefix               string // Optional prefix for all keys (e.g., org ID)
	accessType           AccessType
	serverSideEncryption types.ServerSideEncryption
	sseKMSKeyID          string
}

// S3Config holds configuration for S3 storage
type S3Config struct {
	Endpoint             string // Custom endpoint for S3-compatible storage (e.g., MinIO)
	Bucket               string
	Region               string
	AccessKeyID          string
	SecretAccessKey      string
	Prefix               string     // Optional key prefix
	AccessType           AccessType // hot or cold
	UsePathStyle         bool       // Use path-style addressing (required for MinIO)
	ServerSideEncryption string
	SSEKMSKeyID          string
}

func normalizeServerSideEncryption(value string) (types.ServerSideEncryption, error) {
	switch strings.TrimSpace(value) {
	case "":
		return "", nil
	case string(types.ServerSideEncryptionAes256):
		return types.ServerSideEncryptionAes256, nil
	case string(types.ServerSideEncryptionAwsKms):
		return types.ServerSideEncryptionAwsKms, nil
	default:
		return "", fmt.Errorf("unsupported server-side encryption mode %q", value)
	}
}

// NewS3Store creates a new S3 storage backend
func NewS3Store(ctx context.Context, cfg S3Config) (*S3Store, error) {
	// Build AWS config options
	var opts []func(*config.LoadOptions) error

	opts = append(opts, config.WithRegion(cfg.Region))

	// Use static credentials if provided
	if cfg.AccessKeyID != "" && cfg.SecretAccessKey != "" {
		opts = append(opts, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		))
	}

	// Custom HTTP transport with connection pool tuned for S3 resilience.
	// Key settings prevent stale/zombie connections from blocking all traffic:
	// - TLSHandshakeTimeout: prevents hung TLS negotiations
	// - IdleConnTimeout 30s: detects stale connections faster (was 120s)
	// - MaxConnsPerHost 0: no cap, so zombie connections can't block new ones
	// - ForceAttemptHTTP2: enables multiplexing over a single connection
	// - ExpectContinueTimeout: validates S3 accepts PUT/POST before sending body
	httpClient := &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:          200,
			MaxIdleConnsPerHost:   64,
			MaxConnsPerHost:       0, // unlimited — prevents zombie connections from blocking all traffic
			IdleConnTimeout:       30 * time.Second,
			TLSHandshakeTimeout:   5 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			ForceAttemptHTTP2:     true,
			ReadBufferSize:        128 * 1024, // 128KB read buffer
			WriteBufferSize:       64 * 1024,  // 64KB write buffer
			DialContext: (&net.Dialer{
				Timeout:   10 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ResponseHeaderTimeout: 30 * time.Second,
		},
	}

	opts = append(opts, config.WithHTTPClient(httpClient))

	// Load AWS config
	awsCfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	// Build S3 client options
	var s3Opts []func(*s3.Options)

	// Custom endpoint (for MinIO, LocalStack, etc.)
	if cfg.Endpoint != "" {
		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
			o.UsePathStyle = cfg.UsePathStyle
		})
	}

	client := s3.NewFromConfig(awsCfg, s3Opts...)
	presignClient := s3.NewPresignClient(client)

	accessType := cfg.AccessType
	if accessType == "" {
		accessType = AccessImmediate
	}

	serverSideEncryption, err := normalizeServerSideEncryption(cfg.ServerSideEncryption)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.SSEKMSKeyID) != "" && serverSideEncryption != types.ServerSideEncryptionAwsKms {
		return nil, fmt.Errorf("sse_kms_key_id requires server-side encryption mode aws:kms")
	}

	return &S3Store{
		client:               client,
		presignClient:        presignClient,
		bucket:               cfg.Bucket,
		prefix:               cfg.Prefix,
		accessType:           accessType,
		serverSideEncryption: serverSideEncryption,
		sseKMSKeyID:          strings.TrimSpace(cfg.SSEKMSKeyID),
	}, nil
}

// key builds the full S3 key from a block ID
func (s *S3Store) key(blockID string) string {
	if s.prefix != "" {
		return s.prefix + "/" + blockID
	}
	return blockID
}

func (s *S3Store) applySSEToPutObjectInput(input *s3.PutObjectInput) {
	if s.serverSideEncryption == "" {
		return
	}
	input.ServerSideEncryption = s.serverSideEncryption
	if s.serverSideEncryption == types.ServerSideEncryptionAwsKms && s.sseKMSKeyID != "" {
		input.SSEKMSKeyId = aws.String(s.sseKMSKeyID)
	}
}

func (s *S3Store) applySSEToCreateMultipartUploadInput(input *s3.CreateMultipartUploadInput) {
	if s.serverSideEncryption == "" {
		return
	}
	input.ServerSideEncryption = s.serverSideEncryption
	if s.serverSideEncryption == types.ServerSideEncryptionAwsKms && s.sseKMSKeyID != "" {
		input.SSEKMSKeyId = aws.String(s.sseKMSKeyID)
	}
}

// Put stores a block in S3
// Optimized to avoid double-buffering when possible:
// 1. If data is already an io.ReadSeeker and size is known, use directly
// 2. For unknown size, use SpillBuffer (memory for small, disk for large)
func (s *S3Store) Put(ctx context.Context, blockID string, data io.Reader, size int64) (string, error) {
	key := s.key(blockID)

	var body io.ReadSeeker
	var contentLength int64

	// Check if data is already seekable (e.g., bytes.Reader, os.File)
	if rs, ok := data.(io.ReadSeeker); ok && size > 0 {
		// Data is already seekable and size is known - use directly (no copy!)
		body = rs
		contentLength = size
	} else {
		// Need to buffer the data to get size and make it seekable
		// Use SpillBuffer: memory for small data, disk for large data
		spill := NewSpillBuffer(16 * 1024 * 1024) // 16 MB threshold
		defer spill.Close()

		if _, err := io.Copy(spill, data); err != nil {
			return "", fmt.Errorf("failed to buffer data: %w", err)
		}

		contentLength = spill.Size()

		// Get a reader for the buffered data
		reader, err := spill.ReadSeeker()
		if err != nil {
			return "", fmt.Errorf("failed to get reader: %w", err)
		}
		body = reader
	}

	input := &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		Body:          body,
		ContentLength: aws.Int64(contentLength),
	}
	s.applySSEToPutObjectInput(input)

	_, err := s.client.PutObject(ctx, input)
	if err != nil {
		return "", fmt.Errorf("failed to upload to S3: %w", err)
	}

	return key, nil
}

// MultipartThreshold is the size above which multipart upload is used
const MultipartThreshold = 100 * 1024 * 1024 // 100 MB

// MultipartPartSize is the size of each part in multipart upload
const MultipartPartSize = 16 * 1024 * 1024 // 16 MB per part

// MultipartUploadConcurrency bounds concurrent UploadPart requests for large objects.
// Reads stay sequential; only the network-bound part uploads run in parallel.
const MultipartUploadConcurrency = 4

// MaxMultipartParts is the hard limit S3 places on the number of parts in a
// single multipart upload (valid part numbers are 1..10000). With a fixed
// MultipartPartSize this caps the largest object we can upload via this path.
const MaxMultipartParts = 10000

type multipartUploadClient interface {
	CreateMultipartUpload(context.Context, *s3.CreateMultipartUploadInput, ...func(*s3.Options)) (*s3.CreateMultipartUploadOutput, error)
	UploadPart(context.Context, *s3.UploadPartInput, ...func(*s3.Options)) (*s3.UploadPartOutput, error)
	CompleteMultipartUpload(context.Context, *s3.CompleteMultipartUploadInput, ...func(*s3.Options)) (*s3.CompleteMultipartUploadOutput, error)
	AbortMultipartUpload(context.Context, *s3.AbortMultipartUploadInput, ...func(*s3.Options)) (*s3.AbortMultipartUploadOutput, error)
}

type multipartUploadPart struct {
	partNumber int32
	data       []byte
}

// PutAuto automatically chooses between regular and multipart upload based on size
func (s *S3Store) PutAuto(ctx context.Context, blockID string, data io.Reader, size int64) (string, error) {
	if size > MultipartThreshold {
		return s.PutLarge(ctx, blockID, data, size)
	}
	return s.Put(ctx, blockID, data, size)
}

// PutLarge stores a large block using multipart upload
// This is more reliable for large files and supports parallel uploads
func (s *S3Store) PutLarge(ctx context.Context, blockID string, data io.Reader, size int64) (string, error) {
	return s.putLargeWithClient(ctx, s.client, blockID, data, size)
}

func (s *S3Store) putLargeWithClient(ctx context.Context, client multipartUploadClient, blockID string, data io.Reader, size int64) (string, error) {
	partCount, err := validateMultipartUploadSize(size)
	if err != nil {
		return "", err
	}

	key := s.key(blockID)

	// Initiate multipart upload
	createInput := &s3.CreateMultipartUploadInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}
	s.applySSEToCreateMultipartUploadInput(createInput)
	createResp, err := client.CreateMultipartUpload(ctx, createInput)
	if err != nil {
		return "", fmt.Errorf("failed to initiate multipart upload: %w", err)
	}

	uploadID := createResp.UploadId
	abortInput := &s3.AbortMultipartUploadInput{
		Bucket:   aws.String(s.bucket),
		Key:      aws.String(key),
		UploadId: uploadID,
	}
	abortMultipartUpload := func() {
		// Use a fresh, bounded context: the original ctx may already be cancelled
		// or timed out (often the very reason we're aborting). S3 retains the
		// already-uploaded parts until the upload is completed or aborted, so a
		// best-effort abort still needs a live context.
		abortCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := client.AbortMultipartUpload(abortCtx, abortInput); err != nil {
			log.Printf("Warning: failed to abort multipart upload for key %s: %v", key, err)
		}
	}

	completedParts, err := uploadMultipartParts(ctx, client, s.bucket, key, uploadID, data, size, partCount)
	if err != nil {
		abortMultipartUpload()
		return "", err
	}

	// Complete multipart upload
	_, err = client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:   aws.String(s.bucket),
		Key:      aws.String(key),
		UploadId: uploadID,
		MultipartUpload: &types.CompletedMultipartUpload{
			Parts: completedParts,
		},
	})
	if err != nil {
		// Abort the multipart upload on error
		abortMultipartUpload()
		return "", fmt.Errorf("failed to complete multipart upload: %w", err)
	}

	return key, nil
}

func validateMultipartUploadSize(size int64) (int, error) {
	if size <= 0 {
		return 0, fmt.Errorf("multipart upload requires positive size")
	}

	partSize := int64(MultipartPartSize)
	partCount64 := (size-1)/partSize + 1
	if partCount64 > MaxMultipartParts {
		return 0, fmt.Errorf("object of %d bytes needs %d parts of %d bytes, exceeding the S3 limit of %d parts", size, partCount64, MultipartPartSize, MaxMultipartParts)
	}

	return int(partCount64), nil
}

func uploadMultipartParts(ctx context.Context, client multipartUploadClient, bucket, key string, uploadID *string, data io.Reader, size int64, partCount int) ([]types.CompletedPart, error) {
	// Defense in depth: the sole caller validates size via validateMultipartUploadSize,
	// but never trust an unvalidated part count here.
	if partCount <= 0 {
		return nil, fmt.Errorf("invalid multipart part count %d", partCount)
	}

	workerCount := MultipartUploadConcurrency
	if partCount < workerCount {
		workerCount = partCount
	}
	if workerCount <= 0 {
		workerCount = 1
	}

	completedParts := make([]types.CompletedPart, partCount)
	jobs := make(chan multipartUploadPart, workerCount)
	g, gctx := errgroup.WithContext(ctx)

	for i := 0; i < workerCount; i++ {
		g.Go(func() error {
			for part := range jobs {
				partResp, err := client.UploadPart(gctx, &s3.UploadPartInput{
					Bucket:        aws.String(bucket),
					Key:           aws.String(key),
					UploadId:      uploadID,
					PartNumber:    aws.Int32(part.partNumber),
					Body:          &bytesReadSeeker{data: part.data},
					ContentLength: aws.Int64(int64(len(part.data))),
				})
				if err != nil {
					return fmt.Errorf("failed to upload part %d: %w", part.partNumber, err)
				}

				completedParts[part.partNumber-1] = types.CompletedPart{
					ETag:       partResp.ETag,
					PartNumber: aws.Int32(part.partNumber),
				}
			}
			return nil
		})
	}

	g.Go(func() error {
		defer close(jobs)

		partNumber := int32(1)
		var uploaded int64

		for uploaded < size {
			remaining := size - uploaded
			partSize := int64(MultipartPartSize)
			if remaining < partSize {
				partSize = remaining
			}

			partBuf := make([]byte, partSize)
			n, err := io.ReadFull(data, partBuf)
			if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
				return fmt.Errorf("failed to read part %d: %w", partNumber, err)
			}
			if n <= 0 {
				return fmt.Errorf("failed to read part %d: empty part", partNumber)
			}
			if n < len(partBuf) {
				partBuf = partBuf[:n]
			}

			select {
			case jobs <- multipartUploadPart{partNumber: partNumber, data: partBuf}:
			case <-gctx.Done():
				return gctx.Err()
			}

			uploaded += int64(len(partBuf))
			partNumber++
		}

		return nil
	})

	if err := g.Wait(); err != nil {
		return nil, err
	}

	return completedParts, nil
}

// bytesReadSeeker wraps a byte slice as io.ReadSeeker
type bytesReadSeeker struct {
	data   []byte
	offset int
}

func (r *bytesReadSeeker) Read(p []byte) (int, error) {
	if r.offset >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.offset:])
	r.offset += n
	return n, nil
}

func (r *bytesReadSeeker) Seek(offset int64, whence int) (int64, error) {
	var newOffset int64
	switch whence {
	case io.SeekStart:
		newOffset = offset
	case io.SeekCurrent:
		newOffset = int64(r.offset) + offset
	case io.SeekEnd:
		newOffset = int64(len(r.data)) + offset
	default:
		return 0, fmt.Errorf("invalid whence: %d", whence)
	}
	if newOffset < 0 {
		return 0, fmt.Errorf("negative offset")
	}
	r.offset = int(newOffset)
	return newOffset, nil
}

// Get retrieves a block from S3
func (s *S3Store) Get(ctx context.Context, storageKey string) (io.ReadCloser, error) {
	input := &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(storageKey),
	}

	result, err := s.client.GetObject(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to get object from S3: %w", err)
	}

	return result.Body, nil
}

// Delete removes a block from S3
func (s *S3Store) Delete(ctx context.Context, storageKey string) error {
	input := &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(storageKey),
	}

	_, err := s.client.DeleteObject(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to delete object from S3: %w", err)
	}

	return nil
}

// GetObjectSize returns the size of an object using HEAD (no data transfer).
func (s *S3Store) GetObjectSize(ctx context.Context, storageKey string) (int64, error) {
	input := &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(storageKey),
	}

	result, err := s.client.HeadObject(ctx, input)
	if err != nil {
		return 0, fmt.Errorf("failed to get object size: %w", err)
	}

	if result.ContentLength != nil {
		return *result.ContentLength, nil
	}
	return 0, nil
}

// Exists checks if a block exists in S3
func (s *S3Store) Exists(ctx context.Context, storageKey string) (bool, error) {
	input := &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(storageKey),
	}

	_, err := s.client.HeadObject(ctx, input)
	if err != nil {
		// Check if it's a "not found" error
		if ok := isNotFoundError(err); ok {
			return false, nil
		}
		return false, fmt.Errorf("failed to check object existence: %w", err)
	}

	return true, nil
}

// isNotFoundError checks if an error is a "not found" error
func isNotFoundError(err error) bool {
	// AWS SDK v2 returns different error types for "not found"
	// This is a simple string check
	if err == nil {
		return false
	}
	errStr := err.Error()
	return contains(errStr, "NotFound") || contains(errStr, "404") || contains(errStr, "NoSuchKey")
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// GetAccessType returns whether this is hot or cold storage
func (s *S3Store) GetAccessType() AccessType {
	return s.accessType
}

// InitiateRestore starts a restore operation (only for Glacier/cold storage)
func (s *S3Store) InitiateRestore(ctx context.Context, storageKey string) (string, error) {
	if s.accessType != AccessDelayed {
		return "", fmt.Errorf("restore not needed for hot storage")
	}

	input := &s3.RestoreObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(storageKey),
		RestoreRequest: &types.RestoreRequest{
			Days: aws.Int32(7), // Keep restored copy for 7 days
			GlacierJobParameters: &types.GlacierJobParameters{
				Tier: types.TierStandard, // Standard retrieval (3-5 hours)
			},
		},
	}

	_, err := s.client.RestoreObject(ctx, input)
	if err != nil {
		return "", fmt.Errorf("failed to initiate restore: %w", err)
	}

	// S3 doesn't return a job ID, use the storage key as identifier
	return storageKey, nil
}

// CheckRestoreStatus checks if a restore operation is complete
func (s *S3Store) CheckRestoreStatus(ctx context.Context, storageKey string) (bool, error) {
	input := &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(storageKey),
	}

	result, err := s.client.HeadObject(ctx, input)
	if err != nil {
		return false, fmt.Errorf("failed to check restore status: %w", err)
	}

	// If Restore is nil or empty, object is not in Glacier or already restored
	if result.Restore == nil || *result.Restore == "" {
		return true, nil
	}

	// Check if restore is complete
	// Format: ongoing-request="false", expiry-date="..."
	restore := *result.Restore
	return contains(restore, `ongoing-request="false"`), nil
}

// GetRestoreExpiry returns when a restored object will expire
func (s *S3Store) GetRestoreExpiry(ctx context.Context, storageKey string) (*time.Time, error) {
	input := &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(storageKey),
	}

	result, err := s.client.HeadObject(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("failed to get restore expiry: %w", err)
	}

	// Parse expiry from Restore header
	// This is a simplified implementation - production code should parse the date properly
	if result.Restore == nil {
		return nil, nil
	}

	// For now, return nil - would need to parse the expiry-date from the Restore header
	return nil, nil
}

// PresignedURL generates a presigned URL for direct access
type PresignedURL struct {
	URL       string
	ExpiresAt time.Time
}

// GetPresignedDownloadURL generates a presigned URL for downloading
func (s *S3Store) GetPresignedDownloadURL(ctx context.Context, storageKey string, expiration time.Duration) (*PresignedURL, error) {
	input := &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(storageKey),
	}

	presignResult, err := s.presignClient.PresignGetObject(ctx, input, func(opts *s3.PresignOptions) {
		opts.Expires = expiration
	})
	if err != nil {
		return nil, fmt.Errorf("failed to generate presigned download URL: %w", err)
	}

	return &PresignedURL{
		URL:       presignResult.URL,
		ExpiresAt: time.Now().Add(expiration),
	}, nil
}

// GetPresignedUploadURL generates a presigned URL for uploading
func (s *S3Store) GetPresignedUploadURL(ctx context.Context, storageKey string, expiration time.Duration) (*PresignedURL, error) {
	input := &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(storageKey),
	}

	presignResult, err := s.presignClient.PresignPutObject(ctx, input, func(opts *s3.PresignOptions) {
		opts.Expires = expiration
	})
	if err != nil {
		return nil, fmt.Errorf("failed to generate presigned upload URL: %w", err)
	}

	return &PresignedURL{
		URL:       presignResult.URL,
		ExpiresAt: time.Now().Add(expiration),
	}, nil
}

// HeadBucket verifies connectivity to the S3 bucket.
func (s *S3Store) HeadBucket(ctx context.Context) error {
	_, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(s.bucket),
	})
	return err
}

// Bucket returns the bucket name
func (s *S3Store) Bucket() string {
	return s.bucket
}

// Client returns the underlying S3 client (for advanced operations)
func (s *S3Store) Client() *s3.Client {
	return s.client
}

// ObjectInfo represents information about an S3 object
type ObjectInfo struct {
	Key          string
	Size         int64
	LastModified time.Time
	IsDirectory  bool // True if this is a "directory" (common prefix)
}

// List lists objects with the given prefix
// If delimiter is non-empty, it groups objects by that delimiter (for directory listing)
func (s *S3Store) List(ctx context.Context, prefix string, delimiter string) ([]ObjectInfo, error) {
	input := &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
		Prefix: aws.String(prefix),
	}

	if delimiter != "" {
		input.Delimiter = aws.String(delimiter)
	}

	var objects []ObjectInfo

	paginator := s3.NewListObjectsV2Paginator(s.client, input)
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to list objects: %w", err)
		}

		// Add common prefixes (directories)
		for _, cp := range page.CommonPrefixes {
			if cp.Prefix != nil {
				objects = append(objects, ObjectInfo{
					Key:         *cp.Prefix,
					IsDirectory: true,
				})
			}
		}

		// Add objects (files)
		for _, obj := range page.Contents {
			if obj.Key != nil {
				info := ObjectInfo{
					Key:  *obj.Key,
					Size: aws.ToInt64(obj.Size),
				}
				if obj.LastModified != nil {
					info.LastModified = *obj.LastModified
				}
				objects = append(objects, info)
			}
		}
	}

	return objects, nil
}
