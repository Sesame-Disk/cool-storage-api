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

var (
	// ErrStreamStorage wraps block prefetch, fetch, decrypt, and reader failures.
	ErrStreamStorage = errors.New("stream storage failure")
	// ErrStreamResponse wraps response write and flush failures.
	ErrStreamResponse = errors.New("stream response failure")
)

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
	return BatchResolveBlockIDsContext(context.Background(), database, orgID, representationID, blockIDs)
}

// BatchResolveBlockIDsContext is BatchResolveBlockIDs bound to ctx, including
// every Cassandra mapping read. It stops dispatching work once the request is
// cancelled and waits for already-issued context-aware reads to return.
func BatchResolveBlockIDsContext(ctx context.Context, database *db.DB, orgID, representationID string, blockIDs []string) ([]string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := db.ValidateBlockRepresentationID(representationID); err != nil {
		return nil, err
	}
	return resolveBlockIDsContext(ctx, orgID, blockIDs, mappingResolveConcurrency, func(idx int) (string, error) {
		internalID, found, err := database.GetBlockIDMappingContext(ctx, orgID, representationID, blockIDs[idx])
		if err != nil {
			return "", err
		}
		if !found {
			return "", gocql.ErrNotFound
		}
		return internalID, nil
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
	return resolveBlockIDsContext(context.Background(), orgID, blockIDs, maxConcurrency, lookup)
}

func resolveBlockIDsContext(ctx context.Context, orgID string, blockIDs []string, maxConcurrency int, lookup func(idx int) (string, error)) ([]string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
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

	launched := 0
dispatch:
	for _, idx := range toResolve {
		if err := ctx.Err(); err != nil {
			break
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			break dispatch
		}
		// Both cases can be ready at once. Re-check after acquiring the
		// semaphore so a cancellation that raced with the select cannot
		// dispatch another lookup.
		if err := ctx.Err(); err != nil {
			<-sem
			break
		}
		go func(idx int) {
			defer func() { <-sem }()
			if err := ctx.Err(); err != nil {
				results <- lookupResult{idx: idx, err: err}
				return
			}
			internalID, err := lookup(idx)
			results <- lookupResult{idx: idx, internalID: internalID, err: err}
		}(idx)
		launched++
	}

	var resolveErr error
	queryFailures := 0
	missingMappings := 0
	for range launched {
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
	if err := ctx.Err(); err != nil {
		return nil, err
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
// for maximum throughput. Only O(2 x block_size) RAM. It returns errors wrapped
// with ErrStreamStorage or ErrStreamResponse; callers that cannot act after the
// response is committed may ignore the result.
func StreamBlocks(c *gin.Context, ctx context.Context, blockStore BlockReader, resolvedIDs []string, fileKey []byte, fileIV []byte, logPrefix string) error {
	if len(resolvedIDs) == 0 {
		return nil
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

		if err := streamOneBlock(c, buf, result, fileKey, logPrefix, i, len(resolvedIDs)); err != nil {
			return err
		}
	}

	return nil
}

// streamOneBlock writes one prefetched block to the response and ALWAYS closes
// that block's reader — including if the write or copy panics — so the reader this
// call owns is closed exactly once on every path. Its errors are categorized for
// callers even though response headers may already be committed. The block
// prefetched one ahead is owned and closed by StreamBlocks' own defer, not here, so
// the two never target the same reader.
func streamOneBlock(c *gin.Context, buf []byte, result PrefetchResult, fileKey []byte, logPrefix string, i, total int) error {
	if result.Reader != nil {
		defer result.Reader.Close()
	}

	if result.Err != nil {
		log.Printf("[%s] Failed to get block %d/%d: %v", logPrefix, i, total, result.Err)
		return fmt.Errorf("%w: get block %d/%d: %w", ErrStreamStorage, i, total, result.Err)
	}

	if fileKey != nil {
		// Encrypted: write the already-decrypted bytes (there is no reader to own).
		if _, err := writeResponse(c.Writer, result.Data); err != nil {
			log.Printf("[%s] Write error: %v", logPrefix, err)
			return fmt.Errorf("%w: write block %d/%d: %w", ErrStreamResponse, i, total, err)
		}
	} else {
		// Unencrypted: stream with the 4MB buffer.
		writer := &responseWriteTracker{Writer: c.Writer}
		if _, err := io.CopyBuffer(writer, result.Reader, buf); err != nil {
			if writer.err != nil {
				log.Printf("[%s] Write error: %v", logPrefix, err)
				return fmt.Errorf("%w: write block %d/%d: %w", ErrStreamResponse, i, total, err)
			}
			log.Printf("[%s] Block read error: %v", logPrefix, err)
			return fmt.Errorf("%w: read block %d/%d: %w", ErrStreamStorage, i, total, err)
		}
	}

	// Flush every 4 blocks instead of every block to reduce overhead.
	if (i+1)%4 == 0 || i == total-1 {
		if writer, ok := c.Writer.(interface{ FlushError() error }); ok {
			if err := writer.FlushError(); err != nil {
				log.Printf("[%s] Flush error: %v", logPrefix, err)
				return fmt.Errorf("%w: flush response: %w", ErrStreamResponse, err)
			}
		} else {
			c.Writer.Flush()
		}
	}
	return nil
}

// responseWriteTracker identifies errors returned by the response writer when
// io.CopyBuffer otherwise cannot distinguish them from source-reader errors.
type responseWriteTracker struct {
	io.Writer
	err error
}

func (w *responseWriteTracker) Write(p []byte) (int, error) {
	n, err := writeResponse(w.Writer, p)
	if err != nil {
		w.err = err
	}
	return n, err
}

func writeResponse(w io.Writer, p []byte) (int, error) {
	n, err := w.Write(p)
	if err == nil && n != len(p) {
		err = io.ErrShortWrite
	}
	return n, err
}
