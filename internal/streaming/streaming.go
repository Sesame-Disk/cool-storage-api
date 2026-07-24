package streaming

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"sync"

	"github.com/Sesame-Disk/sesamefs/internal/crypto"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/gin-gonic/gin"
)

// BlockReader is the interface for reading blocks from storage.
// Satisfied by *storage.BlockStore.
type BlockReader interface {
	GetBlock(ctx context.Context, hash string) ([]byte, error)
	// GetBlockReader must abort promptly when ctx is canceled. StreamBlocks cancels a
	// prefetch's context on abandonment and then waits for that fetch to return before
	// closing its reader, so an implementation that ignores ctx would hold the handler
	// until the underlying call completes on its own (defeating the fast cleanup).
	GetBlockReader(ctx context.Context, hash string) (io.ReadCloser, error)
	GetBlockSize(ctx context.Context, hash string) (int64, error)
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

// mappingResolveConcurrency bounds the number of in-flight single-row lookups
// against block_id_mappings so a large file's block list cannot flood the driver.
const mappingResolveConcurrency = 32

// ContainsLegacySHA1 reports whether any entry canonicalizes to a 40-char SHA-1
// that BatchResolveBlockIDs would have to resolve through block_id_mappings.
// Callers use it to skip the per-request representation_id lookup on the common
// all-SHA-256 path, where BatchResolveBlockIDs is a no-op passthrough and the
// representation is never consulted.
func ContainsLegacySHA1(blockIDs []string) bool {
	for _, id := range blockIDs {
		if db.IsSHA1BlockID(db.NormalizeBlockID(id)) {
			return true
		}
	}
	return false
}

// BatchResolveBlockIDs resolves all SHA-1 block IDs (40 chars) to their internal
// SHA-256 content address inside one block-representation domain. IDs that are
// already SHA-256 (64 chars) pass through untouched.
//
// Resolution is STRICT: if any 40-char ID cannot be resolved — whether the lookup
// errored (e.g. Cassandra timeout) or no mapping row exists — the call returns a
// nil slice and a non-nil error. Callers MUST treat this as fatal and abort BEFORE
// writing any response headers/body. Streaming a partially-resolved list would
// send a stale SHA-1 to SHA-256 storage, truncating the download mid-stream after
// the headers are already committed (see StreamBlocks: "headers already sent").
func BatchResolveBlockIDs(database *db.DB, orgID, representationID string, blockIDs []string) ([]string, error) {
	if err := db.ValidateBlockRepresentationID(representationID); err != nil {
		return nil, err
	}
	return resolveBlockIDs(orgID, blockIDs, mappingResolveConcurrency, func(idx int) (string, error) {
		var internalID string
		err := database.Session().Query(`
			SELECT internal_id FROM block_id_mappings
			WHERE org_id = ? AND representation_id = ? AND external_id = ?
		`, orgID, representationID, db.NormalizeBlockID(blockIDs[idx])).Scan(&internalID)
		return internalID, err
	})
}

// resolveBlockIDs maps every 40-char SHA-1 entry of blockIDs to its internal
// SHA-256 by calling lookup with bounded concurrency, preserving slice order.
// Every ID is canonicalized with db.NormalizeBlockID (trim + lowercase) BEFORE
// it is classified by hex content (db.IsSHA1BlockID / db.IsSHA256BlockID), so a
// padded or uppercase SHA-1 is still recognized and a 64-char SHA-256 passes
// through canonicalized instead of raw. lookup must return gocql.ErrNotFound when
// no mapping row exists.
//
// Resolution is strict: an ID that is neither a hex 40-char SHA-1 nor a hex
// 64-char SHA-256, a lookup error, a missing mapping row, or an internal_id that
// is not a hex 64-char SHA-256 all mark the block as unresolved. If any
// block is unresolved the function returns (nil, err) with every cause joined, so
// callers never act on a partially-resolved slice. orgID is used only for
// error/log context. The DB-backed lookup is injected so the
// concurrency/ordering/error semantics stay unit-testable without a live
// Cassandra (block_id_mappings has no in-process fake).
func resolveBlockIDs(orgID string, blockIDs []string, maxConcurrency int, lookup func(idx int) (string, error)) ([]string, error) {
	resolved := make([]string, len(blockIDs))

	// Canonicalize first, THEN classify. 64-char SHA-256 IDs land canonicalized
	// and lookup is never called for them; 40-char SHA-1 IDs are queued.
	var toResolve []int
	var invalidErr error
	for i, bid := range blockIDs {
		normalized := db.NormalizeBlockID(bid)
		switch {
		case db.IsSHA1BlockID(normalized):
			resolved[i] = normalized
			toResolve = append(toResolve, i)
		case db.IsSHA256BlockID(normalized):
			resolved[i] = normalized
		default:
			invalidErr = errors.Join(invalidErr, fmt.Errorf("block %q is not a valid hex SHA-1 or SHA-256 block id", bid))
		}
	}
	if invalidErr != nil {
		return nil, invalidErr
	}
	if len(toResolve) == 0 {
		return resolved, nil
	}

	type lookupResult struct {
		idx        int
		internalID string
		err        error
	}

	concurrency := maxConcurrency
	if concurrency > len(toResolve) {
		concurrency = len(toResolve)
	}
	if concurrency < 1 {
		concurrency = 1
	}
	sem := make(chan struct{}, concurrency)
	results := make(chan lookupResult, len(toResolve))

	for _, idx := range toResolve {
		sem <- struct{}{}
		go func(idx int) {
			defer func() { <-sem }()
			internalID, err := lookup(idx)
			results <- lookupResult{idx: idx, internalID: internalID, err: err}
		}(idx)
	}

	var resolveErr error
	queryFailures := 0
	missingMappings := 0
	for range toResolve {
		result := <-results
		if result.err != nil {
			if errors.Is(result.err, gocql.ErrNotFound) {
				missingMappings++
				resolveErr = errors.Join(resolveErr, fmt.Errorf("block %s has no SHA-1→SHA-256 mapping row: %w", blockIDs[result.idx], result.err))
			} else {
				queryFailures++
				resolveErr = errors.Join(resolveErr, fmt.Errorf("resolve block mapping org=%s block=%s: %w", orgID, blockIDs[result.idx], result.err))
			}
			continue
		}
		internalID := db.NormalizeBlockID(result.internalID)
		if !db.IsSHA256BlockID(internalID) {
			missingMappings++
			if internalID == "" {
				resolveErr = errors.Join(resolveErr, fmt.Errorf("block %s mapping row has empty internal_id", blockIDs[result.idx]))
			} else {
				resolveErr = errors.Join(resolveErr, fmt.Errorf("block %s mapping resolved to non-hex/non-SHA-256 internal id %q", blockIDs[result.idx], result.internalID))
			}
			continue
		}
		resolved[result.idx] = internalID
	}
	if resolveErr != nil {
		log.Printf("[BatchResolveBlockIDs] ERROR: aborting resolution for org=%s: %d/%d blocks unresolved (query_failures=%d, missing_mappings=%d)",
			orgID, queryFailures+missingMappings, len(toResolve), queryFailures, missingMappings)
		return nil, resolveErr
	}

	return resolved, nil
}

// PrefetchResult holds the result of a prefetched block.
type PrefetchResult struct {
	Reader io.ReadCloser
	Data   []byte // only for encrypted blocks
	Err    error
}

// PrefetchBlock starts fetching a block in a goroutine and returns a channel with the result.
//
// It is context-aware: if ctx is already canceled when the goroutine runs — the
// stream was abandoned before this block's turn — it delivers ctx.Err() instead
// of opening a new S3 request that would only have to be closed again. Whenever it
// does open a reader, ownership passes to the receiver (StreamBlocks), which closes
// it on both the streamed and the abandoned-prefetch paths (F11).
func PrefetchBlock(ctx context.Context, blockStore BlockReader, blockID string, fileKey []byte, fileIV []byte) chan PrefetchResult {
	ch := make(chan PrefetchResult, 1)
	go func() {
		if err := ctx.Err(); err != nil {
			ch <- PrefetchResult{Err: err}
			return
		}
		if fileKey != nil {
			blockData, err := blockStore.GetBlock(ctx, blockID)
			if err != nil {
				ch <- PrefetchResult{Err: err}
				return
			}
			decrypted, err := crypto.DecryptLibraryBlock(blockData, fileKey, fileIV)
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
func StreamBlocks(c *gin.Context, ctx context.Context, blockStore BlockReader, resolvedIDs []string, fileKey []byte, fileIV []byte, logPrefix string) {
	if len(resolvedIDs) == 0 {
		return
	}

	buf := GetCopyBuf()
	defer PutCopyBuf(buf)

	// Prefetches run under a child context so an early exit can cancel a fetch still
	// in flight BEFORE the cleanup below waits on it. Without that, draining a
	// prefetch whose GetBlockReader is mid-S3-request blocks StreamBlocks for a whole
	// fetch/timeout whenever the stream is abandoned for a reason that does not cancel
	// ctx (a write error, or a current-block error) — turning a would-be reader leak
	// into a handler held open. cancelPrefetch also releases the context on the
	// normal-completion path.
	prefetchCtx, cancelPrefetch := context.WithCancel(ctx)

	// pending holds the channel for the block prefetched one ahead but not yet
	// consumed. StreamBlocks always runs a block ahead, so on ANY early exit (block
	// error, write error, copy error, panic) that prefetched block would otherwise be
	// dropped with its S3 reader still open — the F11 leak. The defer cancels the
	// in-flight fetch, then drains the channel and closes any reader, on every exit
	// path; pending is nil whenever nothing is in flight.
	var pending chan PrefetchResult
	defer func() {
		cancelPrefetch()
		if pending == nil {
			return
		}
		if res := <-pending; res.Reader != nil {
			res.Reader.Close()
		}
	}()

	// Start prefetching block 0.
	pending = PrefetchBlock(prefetchCtx, blockStore, resolvedIDs[0], fileKey, fileIV)

	for i := range resolvedIDs {
		// Consume the block whose fetch we started last iteration, then mark nothing
		// pending until the next prefetch begins.
		result := <-pending
		pending = nil

		// Only prefetch the next block once the current one is known good: a failed
		// current block ends the stream, so opening its successor is wasted S3 work
		// and one more reader to cancel and drain.
		if result.Err == nil && i+1 < len(resolvedIDs) {
			pending = PrefetchBlock(prefetchCtx, blockStore, resolvedIDs[i+1], fileKey, fileIV)
		}

		if !streamOneBlock(c, buf, result, fileKey, logPrefix, i, len(resolvedIDs)) {
			return
		}
	}
}

// streamOneBlock writes one prefetched block to the response and ALWAYS closes
// that block's reader — including if the write or copy panics — so the reader this
// call owns is closed exactly once on every path. It returns false when streaming
// must stop (a fetch error, a write error, or a copy error); the response headers
// are already committed by then, so the failure can only be logged. The block
// prefetched one ahead is owned and closed by StreamBlocks' own defer, not here, so
// the two never target the same reader.
func streamOneBlock(c *gin.Context, buf []byte, result PrefetchResult, fileKey []byte, logPrefix string, i, total int) bool {
	if result.Reader != nil {
		defer result.Reader.Close()
	}

	if result.Err != nil {
		log.Printf("[%s] Failed to get block %d/%d: %v", logPrefix, i, total, result.Err)
		return false // headers already sent, can't return error to client
	}

	if fileKey != nil {
		// Encrypted: write the already-decrypted bytes (there is no reader to own).
		if _, err := c.Writer.Write(result.Data); err != nil {
			log.Printf("[%s] Write error: %v", logPrefix, err)
			return false
		}
	} else {
		// Unencrypted: stream with the 4MB buffer.
		if _, err := io.CopyBuffer(c.Writer, result.Reader, buf); err != nil {
			log.Printf("[%s] Stream copy error: %v", logPrefix, err)
			return false
		}
	}

	// Flush every 4 blocks instead of every block to reduce overhead.
	if (i+1)%4 == 0 || i == total-1 {
		c.Writer.Flush()
	}
	return true
}
