package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// S3Store implements the Store interface for S3-compatible storage
type S3Store struct {
	client       *s3.Client
	presignClient *s3.PresignClient
	bucket       string
	prefix       string // Optional prefix for all keys (e.g., org ID)
	accessType   AccessType
}

// S3Config holds configuration for S3 storage
type S3Config struct {
	Endpoint        string // Custom endpoint for S3-compatible storage (e.g., MinIO)
	Bucket          string
	Region          string
	AccessKeyID     string
	SecretAccessKey string
	Prefix          string     // Optional key prefix
	AccessType      AccessType // hot or cold
	UsePathStyle    bool       // Use path-style addressing (required for MinIO)
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

	return &S3Store{
		client:       client,
		presignClient: presignClient,
		bucket:       cfg.Bucket,
		prefix:       cfg.Prefix,
		accessType:   accessType,
	}, nil
}

// key builds the full S3 key from a block ID
func (s *S3Store) key(blockID string) string {
	if s.prefix != "" {
		return s.prefix + "/" + blockID
	}
	return blockID
}

// Put stores a block in S3
func (s *S3Store) Put(ctx context.Context, blockID string, data io.Reader, size int64) (string, error) {
	key := s.key(blockID)

	// Read all data into memory for PutObject
	// TODO: Use multipart upload for large files
	buf, err := io.ReadAll(data)
	if err != nil {
		return "", fmt.Errorf("failed to read data: %w", err)
	}

	input := &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		Body:          bytes.NewReader(buf),
		ContentLength: aws.Int64(int64(len(buf))),
	}

	_, err = s.client.PutObject(ctx, input)
	if err != nil {
		return "", fmt.Errorf("failed to upload to S3: %w", err)
	}

	return key, nil
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

// Exists checks if a block exists in S3
func (s *S3Store) Exists(ctx context.Context, storageKey string) (bool, error) {
	input := &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(storageKey),
	}

	_, err := s.client.HeadObject(ctx, input)
	if err != nil {
		// Check if it's a "not found" error
		var notFound *types.NotFound
		if ok := isNotFoundError(err); ok {
			return false, nil
		}
		// Also check using the types package
		if notFound != nil {
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
