package storage

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/Sesame-Disk/sesamefs/internal/chunker"
)

// BlockStore provides content-addressable block storage
// Blocks are stored by their SHA256 hash, enabling deduplication
type BlockStore struct {
	s3     *S3Store
	prefix string // Prefix for block keys in S3 (e.g., "blocks/")
}

// NewBlockStore creates a new block store backed by S3
func NewBlockStore(s3Store *S3Store, prefix string) *BlockStore {
	if prefix == "" {
		prefix = "blocks/"
	}
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return &BlockStore{
		s3:     s3Store,
		prefix: prefix,
	}
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

// GetBlock retrieves a block by its hash
func (bs *BlockStore) GetBlock(ctx context.Context, hash string) ([]byte, error) {
	key := bs.hashToKey(hash)

	reader, err := bs.s3.Get(ctx, key)
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
	key := bs.hashToKey(hash)
	return bs.s3.Get(ctx, key)
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
			sem <- struct{}{} // Acquire
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

// DeleteBlock removes a block from storage
// Note: Should only be called after verifying no references exist
func (bs *BlockStore) DeleteBlock(ctx context.Context, hash string) error {
	key := bs.hashToKey(hash)
	return bs.s3.Delete(ctx, key)
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

// hashToKey converts a block hash to an S3 key
// Uses a two-level directory structure for better S3 performance
// e.g., "blocks/ab/cd/abcdef123456..."
func (bs *BlockStore) hashToKey(hash string) string {
	if len(hash) < 4 {
		return bs.prefix + hash
	}
	// Two-level sharding: first 2 chars, next 2 chars
	return fmt.Sprintf("%s%s/%s/%s", bs.prefix, hash[:2], hash[2:4], hash)
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
	TotalBlocks     int64 `json:"total_blocks"`
	TotalSize       int64 `json:"total_size"`
	UniqueBlocks    int64 `json:"unique_blocks"`
	DeduplicatedPct float64 `json:"deduplicated_pct"`
}
