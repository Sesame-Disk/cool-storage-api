package streaming

import (
	"context"
	"fmt"
	"io"
	"log"
	"sort"

	"github.com/Sesame-Disk/sesamefs/internal/crypto"
	"github.com/Sesame-Disk/sesamefs/internal/db"
)

// BlockReadSeeker implements io.ReadSeeker over block storage.
// It loads only the current block into memory, enabling http.ServeContent
// to handle Range requests (HTTP 206) without buffering the entire file.
// Memory usage: O(1 block) per concurrent reader (~16MB typical).
type BlockReadSeeker struct {
	ctx        context.Context
	blockStore BlockReader
	blockIDs   []string // resolved block IDs (SHA-256)
	blockSizes []int64  // size of each block
	offsets    []int64  // cumulative byte offset where each block starts
	totalSize  int64    // total file size

	fileKey []byte // encryption key (nil = not encrypted)

	pos int64 // current read position in the virtual file

	// Block cache: holds at most one block in memory
	cachedIdx  int    // index of cached block (-1 = none)
	cachedData []byte // decrypted/raw block data
}

// NewBlockReadSeeker creates a ReadSeeker that reads from block storage on demand.
// blockSizes must correspond 1:1 with blockIDs.
func NewBlockReadSeeker(
	ctx context.Context,
	blockStore BlockReader,
	blockIDs []string,
	blockSizes []int64,
	totalSize int64,
	fileKey []byte,
) *BlockReadSeeker {
	// Build cumulative offset table
	offsets := make([]int64, len(blockSizes))
	var cum int64
	for i, sz := range blockSizes {
		offsets[i] = cum
		cum += sz
	}

	return &BlockReadSeeker{
		ctx:        ctx,
		blockStore: blockStore,
		blockIDs:   blockIDs,
		blockSizes: blockSizes,
		offsets:    offsets,
		totalSize:  totalSize,
		fileKey:    fileKey,
		cachedIdx:  -1,
	}
}

// Read implements io.Reader. Fetches blocks on demand, one at a time.
func (r *BlockReadSeeker) Read(p []byte) (int, error) {
	if r.pos >= r.totalSize {
		return 0, io.EOF
	}

	totalRead := 0
	for totalRead < len(p) && r.pos < r.totalSize {
		// Find which block contains the current position
		blockIdx := r.findBlock(r.pos)
		if blockIdx < 0 || blockIdx >= len(r.blockIDs) {
			return totalRead, io.EOF
		}

		// Load block if not cached
		if err := r.ensureBlock(blockIdx); err != nil {
			return totalRead, err
		}

		// Calculate offset within the block
		offsetInBlock := r.pos - r.offsets[blockIdx]
		available := int64(len(r.cachedData)) - offsetInBlock
		if available <= 0 {
			// Safety net: advance to next block to prevent infinite loop.
			if blockIdx+1 < len(r.offsets) {
				r.pos = r.offsets[blockIdx+1]
			} else {
				r.pos = r.totalSize
			}
			continue
		}

		// Copy as much as we can from this block
		toCopy := int64(len(p) - totalRead)
		if toCopy > available {
			toCopy = available
		}

		copy(p[totalRead:], r.cachedData[offsetInBlock:offsetInBlock+toCopy])
		totalRead += int(toCopy)
		r.pos += toCopy
	}

	if totalRead == 0 && r.pos >= r.totalSize {
		return 0, io.EOF
	}
	return totalRead, nil
}

// Seek implements io.Seeker. Pure math, no I/O.
func (r *BlockReadSeeker) Seek(offset int64, whence int) (int64, error) {
	var newPos int64
	switch whence {
	case io.SeekStart:
		newPos = offset
	case io.SeekCurrent:
		newPos = r.pos + offset
	case io.SeekEnd:
		newPos = r.totalSize + offset
	default:
		return 0, fmt.Errorf("invalid whence: %d", whence)
	}

	if newPos < 0 {
		return 0, fmt.Errorf("negative position: %d", newPos)
	}

	r.pos = newPos
	return newPos, nil
}

// findBlock returns the index of the block containing the given byte offset.
// Uses binary search on the cumulative offsets.
func (r *BlockReadSeeker) findBlock(pos int64) int {
	// Binary search: find the last block whose start offset <= pos
	idx := sort.Search(len(r.offsets), func(i int) bool {
		return r.offsets[i] > pos
	}) - 1

	if idx < 0 {
		return 0
	}
	return idx
}

// ensureBlock loads a block into cache if not already cached.
func (r *BlockReadSeeker) ensureBlock(idx int) error {
	if r.cachedIdx == idx && r.cachedData != nil {
		return nil
	}

	// Release previous block
	r.cachedData = nil
	r.cachedIdx = -1

	blockID := r.blockIDs[idx]

	if r.fileKey != nil {
		// Encrypted: must load entire block to decrypt
		data, err := r.blockStore.GetBlock(r.ctx, blockID)
		if err != nil {
			return fmt.Errorf("failed to get block %d (%s): %w", idx, blockID[:16], err)
		}
		decrypted, err := crypto.DecryptBlock(data, r.fileKey)
		if err != nil {
			return fmt.Errorf("failed to decrypt block %d: %w", idx, err)
		}
		r.cachedData = decrypted
	} else {
		// Unencrypted: read full block
		reader, err := r.blockStore.GetBlockReader(r.ctx, blockID)
		if err != nil {
			return fmt.Errorf("failed to get block reader %d (%s): %w", idx, blockID[:16], err)
		}
		defer reader.Close()

		data, err := io.ReadAll(reader)
		if err != nil {
			return fmt.Errorf("failed to read block %d: %w", idx, err)
		}
		r.cachedData = data
	}

	r.cachedIdx = idx

	// If the actual data size differs from the recorded block size (e.g. encrypted
	// blocks store the ciphertext size but cachedData is the smaller plaintext),
	// fix blockSizes and recompute offsets so that Seek and findBlock stay correct.
	actualSize := int64(len(r.cachedData))
	if r.blockSizes[idx] != actualSize {
		r.blockSizes[idx] = actualSize
		var cum int64
		for i, sz := range r.blockSizes {
			r.offsets[i] = cum
			cum += sz
		}
		r.totalSize = cum
	}

	return nil
}

// QueryBlockSizes fetches block sizes for a list of resolved block IDs.
// Uses Cassandra first (fast, single batch query), then falls back to S3 HEAD
// for any blocks missing from the DB (legacy uploads that didn't populate the blocks table).
func QueryBlockSizes(ctx context.Context, database *db.DB, orgID string, blockStore BlockReader, blockIDs []string) ([]int64, error) {
	sizes := make([]int64, len(blockIDs))

	if len(blockIDs) == 0 {
		return sizes, nil
	}

	// Step 1: batch query from Cassandra (fast, 1 round trip per 100 blocks)
	var missing []int // indices of blocks not found in DB
	const batchSize = 100
	for start := 0; start < len(blockIDs); start += batchSize {
		end := start + batchSize
		if end > len(blockIDs) {
			end = len(blockIDs)
		}

		batchIDs := blockIDs[start:end]
		iter := database.Session().Query(`
			SELECT block_id, size_bytes FROM blocks WHERE org_id = ? AND block_id IN ?
		`, orgID, batchIDs).Iter()

		sizeMap := make(map[string]int64, len(batchIDs))
		var blockID string
		var sizeBytes int
		for iter.Scan(&blockID, &sizeBytes) {
			sizeMap[blockID] = int64(sizeBytes)
		}
		if err := iter.Close(); err != nil {
			log.Printf("[QueryBlockSizes] WARNING: DB query failed, falling back to S3: %v", err)
			// Mark all in this batch as missing
			for i := start; i < end; i++ {
				missing = append(missing, i)
			}
			continue
		}

		for i := start; i < end; i++ {
			if sz, ok := sizeMap[blockIDs[i]]; ok && sz > 0 {
				sizes[i] = sz
			} else {
				missing = append(missing, i)
			}
		}
	}

	if len(missing) == 0 {
		return sizes, nil
	}

	// Step 2: fallback to S3 HEAD for missing blocks (parallel)
	log.Printf("[QueryBlockSizes] %d/%d blocks missing from DB, falling back to S3 HEAD", len(missing), len(blockIDs))

	type result struct {
		idx  int
		size int64
		err  error
	}

	ch := make(chan result, len(missing))
	const maxConcurrency = 20
	sem := make(chan struct{}, maxConcurrency)

	for _, idx := range missing {
		sem <- struct{}{}
		go func(i int, blockID string) {
			defer func() { <-sem }()
			size, err := blockStore.GetBlockSize(ctx, blockID)
			ch <- result{idx: i, size: size, err: err}
		}(idx, blockIDs[idx])
	}

	for range missing {
		r := <-ch
		if r.err != nil {
			return nil, fmt.Errorf("failed to get size for block %d via S3: %w", r.idx, r.err)
		}
		sizes[r.idx] = r.size
	}

	return sizes, nil
}
