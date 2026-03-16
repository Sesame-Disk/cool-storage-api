package streaming

import (
	"context"
	"io"
	"log"
	"sync"

	"github.com/Sesame-Disk/sesamefs/internal/crypto"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/gin-gonic/gin"
)

// BlockReader is the interface for reading blocks from storage.
// Satisfied by *storage.BlockStore.
type BlockReader interface {
	GetBlock(ctx context.Context, hash string) ([]byte, error)
	GetBlockReader(ctx context.Context, hash string) (io.ReadCloser, error)
}

// copyBufPool provides reusable 4MB buffers for io.CopyBuffer to avoid
// the default 32KB buffer and reduce syscall overhead by ~128x.
var copyBufPool = sync.Pool{
	New: func() interface{} {
		buf := make([]byte, 4*1024*1024) // 4 MB
		return &buf
	},
}

// GetCopyBuf retrieves a 4MB buffer from the pool for high-throughput streaming.
func GetCopyBuf() []byte {
	return *(copyBufPool.Get().(*[]byte))
}

// PutCopyBuf returns a buffer to the pool.
func PutCopyBuf(buf []byte) {
	copyBufPool.Put(&buf)
}

// BatchResolveBlockIDs resolves all SHA-1 block IDs (40 chars) to SHA-256 in batch.
// IDs that are already SHA-256 (64 chars) are returned as-is.
// Uses Cassandra IN queries in batches of 100 for efficiency.
func BatchResolveBlockIDs(database *db.DB, orgID string, blockIDs []string) []string {
	resolved := make([]string, len(blockIDs))
	copy(resolved, blockIDs)

	// Collect indices that need resolution (SHA-1 = 40 chars)
	var toResolve []int
	for i, bid := range blockIDs {
		if len(bid) == 40 {
			toResolve = append(toResolve, i)
		}
	}
	if len(toResolve) == 0 {
		return resolved
	}

	// Batch resolve using IN clause (up to 100 at a time)
	const batchSize = 100
	for start := 0; start < len(toResolve); start += batchSize {
		end := start + batchSize
		if end > len(toResolve) {
			end = len(toResolve)
		}
		batch := toResolve[start:end]

		externalIDs := make([]string, len(batch))
		for j, idx := range batch {
			externalIDs[j] = blockIDs[idx]
		}

		iter := database.Session().Query(`
			SELECT external_id, internal_id FROM block_id_mappings
			WHERE org_id = ? AND external_id IN ?
		`, orgID, externalIDs).Iter()

		var extID, intID string
		mapping := make(map[string]string, len(batch))
		for iter.Scan(&extID, &intID) {
			mapping[extID] = intID
		}
		if err := iter.Close(); err != nil {
			log.Printf("[BatchResolveBlockIDs] WARNING: query error org=%s blocks=%d: %v", orgID, len(batch), err)
		}

		var unresolved int
		for _, idx := range batch {
			if mapped, ok := mapping[blockIDs[idx]]; ok && mapped != "" {
				resolved[idx] = mapped
			} else {
				unresolved++
			}
		}
		if unresolved > 0 {
			log.Printf("[BatchResolveBlockIDs] WARNING: %d/%d blocks UNRESOLVED for org=%s (first unresolved: %s)",
				unresolved, len(batch), orgID, externalIDs[0])
		}
	}

	return resolved
}

// PrefetchResult holds the result of a prefetched block.
type PrefetchResult struct {
	Reader io.ReadCloser
	Data   []byte // only for encrypted blocks
	Err    error
}

// PrefetchBlock starts fetching a block in a goroutine and returns a channel with the result.
func PrefetchBlock(ctx context.Context, blockStore BlockReader, blockID string, fileKey []byte) chan PrefetchResult {
	ch := make(chan PrefetchResult, 1)
	go func() {
		if fileKey != nil {
			blockData, err := blockStore.GetBlock(ctx, blockID)
			if err != nil {
				ch <- PrefetchResult{Err: err}
				return
			}
			decrypted, err := crypto.DecryptBlock(blockData, fileKey)
			ch <- PrefetchResult{Data: decrypted, Err: err}
		} else {
			reader, err := blockStore.GetBlockReader(ctx, blockID)
			ch <- PrefetchResult{Reader: reader, Err: err}
		}
	}()
	return ch
}

// StreamBlocks streams resolved blocks to an HTTP response with prefetching.
// Uses prefetch (overlap S3 fetch with HTTP write) and 4MB io.CopyBuffer
// for maximum throughput. Only O(2 x block_size) RAM.
func StreamBlocks(c *gin.Context, ctx context.Context, blockStore BlockReader, resolvedIDs []string, fileKey []byte, logPrefix string) {
	if len(resolvedIDs) == 0 {
		return
	}

	buf := GetCopyBuf()
	defer PutCopyBuf(buf)

	// Start prefetching block 0
	nextResult := PrefetchBlock(ctx, blockStore, resolvedIDs[0], fileKey)

	for i := range resolvedIDs {
		// Wait for the prefetched block
		result := <-nextResult

		// Start prefetching the NEXT block immediately
		if i+1 < len(resolvedIDs) {
			nextResult = PrefetchBlock(ctx, blockStore, resolvedIDs[i+1], fileKey)
		}

		if result.Err != nil {
			log.Printf("[%s] Failed to get block %d/%d: %v", logPrefix, i, len(resolvedIDs), result.Err)
			return // headers already sent, can't return error to client
		}

		if fileKey != nil {
			// Encrypted: write decrypted data
			if _, err := c.Writer.Write(result.Data); err != nil {
				log.Printf("[%s] Write error: %v", logPrefix, err)
				return
			}
		} else {
			// Unencrypted: stream with 4MB buffer
			_, err := io.CopyBuffer(c.Writer, result.Reader, buf)
			result.Reader.Close()
			if err != nil {
				log.Printf("[%s] Stream copy error: %v", logPrefix, err)
				return
			}
		}

		// Flush every 4 blocks instead of every block to reduce overhead
		if (i+1)%4 == 0 || i == len(resolvedIDs)-1 {
			c.Writer.Flush()
		}
	}
}
