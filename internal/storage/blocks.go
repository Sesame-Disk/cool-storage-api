package storage

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/Sesame-Disk/sesamefs/internal/chunker"
	"github.com/google/uuid"
)

// BlockStore provides content-addressable block storage
// Blocks are stored by their SHA256 hash, enabling deduplication
type BlockStore struct {
	s3     *S3Store
	prefix string // Prefix for block keys in S3 (e.g., "blocks/")
	// orgID org-scopes every physical S3 key. It is always a canonical UUID
	// string validated by NewOrgBlockStore, so it cannot contain a path separator.
	orgID string
}

// normalizeOrgID trims and validates an org id, returning its canonical UUID
// string form for deterministic cache keys and S3 paths.
func normalizeOrgID(orgID string) (string, error) {
	trimmed := strings.TrimSpace(orgID)
	if trimmed == "" {
		return "", fmt.Errorf("org-scoped block store requires a non-empty org id")
	}
	parsed, err := uuid.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("org-scoped block store org id %q is not a valid UUID: %w", orgID, err)
	}
	return parsed.String(), nil
}

// NewOrgBlockStore creates a block store whose physical S3 keys are org-scoped:
// blocks/<org_id>/<h0:2>/<h2:4>/<hash>. This aligns physical ownership with the
// per-org blocks/block_references tables and GC claims, so one org's delete can
// never remove another org's content.
//
// It fails closed: an empty or invalid org id is rejected rather than silently
// falling back to a global key. The org id is normalized to its canonical UUID
// form so the derived key is deterministic regardless of input casing/format
// and can never contain a path separator.
func NewOrgBlockStore(s3Store *S3Store, prefix, orgID string) (*BlockStore, error) {
	normalizedOrgID, err := normalizeOrgID(orgID)
	if err != nil {
		return nil, err
	}
	if prefix == "" {
		prefix = "blocks/"
	}
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return &BlockStore{s3: s3Store, prefix: prefix, orgID: normalizedOrgID}, nil
}

// BlockInfo contains metadata about a stored block
type BlockInfo struct {
	Hash         string `json:"hash"`
	Size         int64  `json:"size"`
	StorageClass string `json:"storage_class"`
	Exists       bool   `json:"exists"`
}

// BlockData represents a block with its data (used by API layer)
type BlockData struct {
	Hash string
	Data []byte
	Size int64
}

// PutBlockData stores a block from raw data and returns its storage key
// If the block already exists (same hash), it's a no-op (deduplication)
func (bs *BlockStore) PutBlockData(ctx context.Context, block *BlockData) (string, error) {
	key := bs.hashToKey(block.Hash)

	// Check if block already exists (deduplication)
	exists, err := bs.s3.Exists(ctx, key)
	if err != nil {
		return "", fmt.Errorf("failed to check block existence: %w", err)
	}
	if exists {
		// Block already exists, no need to upload
		return key, nil
	}

	// Upload the block
	reader := &bytesReader{data: block.Data}
	_, err = bs.s3.Put(ctx, key, reader, block.Size)
	if err != nil {
		return "", fmt.Errorf("failed to store block: %w", err)
	}

	return key, nil
}

// PutBlock stores a block and returns its storage key
// If the block already exists (same hash), it's a no-op (deduplication)
func (bs *BlockStore) PutBlock(ctx context.Context, block *chunker.Block) (string, error) {
	key := bs.hashToKey(block.Hash)

	// Check if block already exists (deduplication)
	exists, err := bs.s3.Exists(ctx, key)
	if err != nil {
		return "", fmt.Errorf("failed to check block existence: %w", err)
	}
	if exists {
		// Block already exists, no need to upload
		return key, nil
	}

	// Upload the block
	reader := &bytesReader{data: block.Data}
	_, err = bs.s3.Put(ctx, key, reader, block.Size)
	if err != nil {
		return "", fmt.Errorf("failed to store block: %w", err)
	}

	return key, nil
}

// PutBlockAuto stores a block using PutAuto which automatically chooses
// between regular and multipart upload based on size. Includes deduplication.
func (bs *BlockStore) PutBlockAuto(ctx context.Context, hash string, data []byte) (string, error) {
	key := bs.hashToKey(hash)

	// Deduplication check
	exists, err := bs.s3.Exists(ctx, key)
	if err != nil {
		return "", fmt.Errorf("failed to check block existence: %w", err)
	}
	if exists {
		return key, nil
	}

	reader := &bytesReader{data: data}
	_, err = bs.s3.PutAuto(ctx, key, reader, int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("failed to store block: %w", err)
	}

	return key, nil
}

// PutObjectAutoDirect stores raw bytes at an explicit storage key without a prior
// Exists/HEAD. Callers must only use this when another source of truth has already
// decided the block is not safely reusable as-is, and must supply the canonical
// locator instead of letting the store derive one. There is deliberately no
// hash-derived PUT variant left: minting a key here would let a writer store bytes
// at a locator that never reaches `blocks.storage_key`.
func (bs *BlockStore) PutObjectAutoDirect(ctx context.Context, storageKey string, data []byte) (string, error) {
	if strings.TrimSpace(storageKey) == "" {
		return "", fmt.Errorf("block storage key is empty")
	}
	reader := &bytesReader{data: data}
	_, err := bs.s3.PutAuto(ctx, storageKey, reader, int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("failed to store block: %w", err)
	}

	return storageKey, nil
}

// ObjectExists checks whether an explicit storage key exists.
func (bs *BlockStore) ObjectExists(ctx context.Context, storageKey string) (bool, error) {
	return bs.s3.Exists(ctx, storageKey)
}

// StorageKeyForHash exposes the deterministic storage key for a content hash.
func (bs *BlockStore) StorageKeyForHash(hash string) string {
	return bs.hashToKey(hash)
}

// GetBlock retrieves a block by its hash
func (bs *BlockStore) GetBlock(ctx context.Context, hash string) ([]byte, error) {
	return bs.GetBlockByStorageKey(ctx, bs.hashToKey(hash))
}

// GetBlockByStorageKey retrieves a block from an explicit storage key.
func (bs *BlockStore) GetBlockByStorageKey(ctx context.Context, storageKey string) ([]byte, error) {
	reader, err := bs.GetBlockReaderByStorageKey(ctx, storageKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get block: %w", err)
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read block data: %w", err)
	}

	return data, nil
}

// GetBlockReader returns a reader for a block (for streaming large blocks)
func (bs *BlockStore) GetBlockReader(ctx context.Context, hash string) (io.ReadCloser, error) {
	return bs.GetBlockReaderByStorageKey(ctx, bs.hashToKey(hash))
}

// GetBlockReaderByStorageKey returns a reader for an explicit storage key.
func (bs *BlockStore) GetBlockReaderByStorageKey(ctx context.Context, storageKey string) (io.ReadCloser, error) {
	return bs.s3.Get(ctx, storageKey)
}

// GetBlockSize returns the size in bytes of a block using S3 HEAD (no data transfer).
func (bs *BlockStore) GetBlockSize(ctx context.Context, hash string) (int64, error) {
	return bs.GetBlockSizeByStorageKey(ctx, bs.hashToKey(hash))
}

// GetBlockSizeByStorageKey returns the size of an explicit storage key using S3 HEAD.
func (bs *BlockStore) GetBlockSizeByStorageKey(ctx context.Context, storageKey string) (int64, error) {
	return bs.s3.GetObjectSize(ctx, storageKey)
}

// BlockExists checks if a block exists
func (bs *BlockStore) BlockExists(ctx context.Context, hash string) (bool, error) {
	key := bs.hashToKey(hash)
	return bs.s3.Exists(ctx, key)
}

// CheckBlocks checks which blocks from a list already exist
// Returns a map of hash -> exists
func (bs *BlockStore) CheckBlocks(ctx context.Context, hashes []string) (map[string]bool, error) {
	result := make(map[string]bool, len(hashes))

	// Check each block (could be parallelized for performance)
	for _, hash := range hashes {
		exists, err := bs.BlockExists(ctx, hash)
		if err != nil {
			// Log error but continue checking others
			result[hash] = false
			continue
		}
		result[hash] = exists
	}

	return result, nil
}

// CheckBlocksParallel checks blocks in parallel for better performance
func (bs *BlockStore) CheckBlocksParallel(ctx context.Context, hashes []string, concurrency int) (map[string]bool, error) {
	if concurrency <= 0 {
		concurrency = 10
	}

	result := make(map[string]bool, len(hashes))
	resultChan := make(chan struct {
		hash   string
		exists bool
	}, len(hashes))

	// Semaphore for concurrency control
	sem := make(chan struct{}, concurrency)

	for _, hash := range hashes {
		go func(h string) {
			sem <- struct{}{}        // Acquire
			defer func() { <-sem }() // Release

			exists, _ := bs.BlockExists(ctx, h)
			resultChan <- struct {
				hash   string
				exists bool
			}{h, exists}
		}(hash)
	}

	// Collect results
	for range hashes {
		r := <-resultChan
		result[r.hash] = r.exists
	}

	return result, nil
}

// DeleteBlockByStorageKey removes an object from its explicit canonical key.
// This is the ONLY delete entry point on purpose: with no hash-derived variant, a
// destructive caller cannot skip loading the persisted locator, and the compiler
// enforces that rather than a review convention.
// Note: Should only be called after verifying no references exist.
func (bs *BlockStore) DeleteBlockByStorageKey(ctx context.Context, storageKey string) error {
	if strings.TrimSpace(storageKey) == "" {
		return fmt.Errorf("block storage key is empty")
	}
	return bs.s3.Delete(ctx, storageKey)
}

// PutBlocks stores multiple blocks and returns the hashes of successfully stored blocks
func (bs *BlockStore) PutBlocks(ctx context.Context, blocks []chunker.Block) ([]string, error) {
	var stored []string

	for _, block := range blocks {
		_, err := bs.PutBlock(ctx, &block)
		if err != nil {
			return stored, fmt.Errorf("failed to store block %s: %w", block.Hash, err)
		}
		stored = append(stored, block.Hash)
	}

	return stored, nil
}

// hashToKey converts a block hash to an S3 key.
// Uses a two-level directory structure for better S3 performance:
//
//	"blocks/<org_id>/ab/cd/abcdef123456..."
func (bs *BlockStore) hashToKey(hash string) string {
	prefix := bs.prefix + bs.orgID + "/"
	if len(hash) < 4 {
		return prefix + hash
	}
	// Two-level sharding: first 2 chars, next 2 chars
	return fmt.Sprintf("%s%s/%s/%s", prefix, hash[:2], hash[2:4], hash)
}

// bytesReader wraps []byte to implement io.Reader
type bytesReader struct {
	data []byte
	pos  int
}

func (r *bytesReader) Read(p []byte) (n int, err error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n = copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

// BlockStats contains statistics about block storage
type BlockStats struct {
	TotalBlocks     int64   `json:"total_blocks"`
	TotalSize       int64   `json:"total_size"`
	UniqueBlocks    int64   `json:"unique_blocks"`
	DeduplicatedPct float64 `json:"deduplicated_pct"`
}
