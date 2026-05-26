package streaming

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"sort"

	"github.com/Sesame-Disk/sesamefs/internal/crypto"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

// blockSizesConcurrency caps in-flight single-row Cassandra reads when fetching
// block sizes for a file. After the blocks schema move to per-block partitions,
// each lookup is independent and parallelizes cleanly. 32 keeps wall-clock low
// on large files without pressuring the driver pool.
const blockSizesConcurrency = 32

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
	fileIV  []byte // library IV for Seafile-compatible encrypted blocks

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
	fileIV []byte,
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
		fileIV:     fileIV,
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
		decrypted, err := crypto.DecryptLibraryBlock(data, r.fileKey, r.fileIV)
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
//
// The blocks table is partitioned by `((org_id, block_id))` so each lookup
// is a single-partition single-row read with no Paxos cost. We issue up to
// `blockSizesConcurrency` parallel reads against Cassandra and fall back to
// S3 HEAD only for blocks still missing (legacy uploads that didn't populate
// the blocks table). Prior to the per-block partition refactor this used a
// single `IN (?)` batch of 100, which became inefficient once each element
// of `IN` resolved to its own partition.
func QueryBlockSizes(ctx context.Context, database *db.DB, orgID string, blockStore BlockReader, blockIDs []string) ([]int64, error) {
	sizes := make([]int64, len(blockIDs))

	if len(blockIDs) == 0 {
		return sizes, nil
	}

	// Step 1: parallel single-row reads from Cassandra.
	type lookupResult struct {
		idx  int
		size int64
		ok   bool
	}

	concurrency := blockSizesConcurrency
	if concurrency > len(blockIDs) {
		concurrency = len(blockIDs)
	}
	sem := make(chan struct{}, concurrency)
	results := make(chan lookupResult, len(blockIDs))

	for i, blockID := range blockIDs {
		sem <- struct{}{}
		go func(idx int, bid string) {
			defer func() { <-sem }()
			var sizeBytes int
			err := database.Session().Query(`
				SELECT size_bytes FROM blocks WHERE org_id = ? AND block_id = ?
			`, orgID, bid).WithContext(ctx).Scan(&sizeBytes)
			if err != nil {
				if !errors.Is(err, gocql.ErrNotFound) {
					log.Printf("[QueryBlockSizes] WARNING: DB read failed for block %s: %v", bid, err)
				}
				results <- lookupResult{idx: idx, ok: false}
				return
			}
			results <- lookupResult{idx: idx, size: int64(sizeBytes), ok: sizeBytes > 0}
		}(i, blockID)
	}

	var missing []int
	for i := 0; i < len(blockIDs); i++ {
		r := <-results
		if r.ok {
			sizes[r.idx] = r.size
		} else {
			missing = append(missing, r.idx)
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
	s3Sem := make(chan struct{}, maxConcurrency)

	for _, idx := range missing {
		s3Sem <- struct{}{}
		go func(i int, blockID string) {
			defer func() { <-s3Sem }()
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
