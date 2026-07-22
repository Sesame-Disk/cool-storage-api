package gc

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"
)

type gcFailureCoder interface {
	error
	FailureCode() string
}

type libraryHardDeleteInProgressError struct {
	LibraryID uuid.UUID
	ItemID    string
}

type hardDeleteInProgressError struct {
	Kind   string
	Target string
	ItemID string
}

func (e libraryHardDeleteInProgressError) Error() string {
	return fmt.Sprintf("library %s hard delete already in progress for child %s", e.LibraryID, e.ItemID)
}

func (e libraryHardDeleteInProgressError) FailureCode() string {
	return GCFailureCodeLibraryHardDeleteInProgress
}

func (e hardDeleteInProgressError) Error() string {
	return fmt.Sprintf("%s %s hard delete already in progress for item %s", e.Kind, e.Target, e.ItemID)
}

func (e hardDeleteInProgressError) FailureCode() string {
	return GCFailureCodeLibraryHardDeleteInProgress
}

func failureCodeForError(err error) string {
	var coded gcFailureCoder
	if errors.As(err, &coded) {
		return coded.FailureCode()
	}
	return GCFailureCodeNone
}

func isHardDeleteInProgressError(err error) bool {
	return failureCodeForError(err) == GCFailureCodeLibraryHardDeleteInProgress
}

func isBlockNotFound(err error) bool {
	return errors.Is(err, gocql.ErrNotFound)
}

// s3DeleteRetryDelays is the backoff schedule used when S3 DeleteBlock fails.
// Total in-worker wait budget: 100 + 500 + 2000 = 2.6s across 3 retries.
// Exposed as a var so tests can shorten it.
var s3DeleteRetryDelays = []time.Duration{
	100 * time.Millisecond,
	500 * time.Millisecond,
	2 * time.Second,
}

var hardDeleteLockHeartbeatInterval = 30 * time.Minute
var hardDeleteLockStaleAfter = 3 * hardDeleteLockHeartbeatInterval
var fsObjectReferenceFenceInterval = 5 * time.Minute

type hardDeleteLease struct {
	stopCh  chan struct{}
	release func() error

	mu         sync.Mutex
	err        error
	closeOnce  sync.Once
	closedChan chan struct{}
}

func newHardDeleteLease(ctx context.Context, kind, target string, renew func() (bool, error), release func() error) *hardDeleteLease {
	lease := &hardDeleteLease{
		stopCh:     make(chan struct{}),
		release:    release,
		closedChan: make(chan struct{}),
	}
	go func() {
		defer close(lease.closedChan)
		ticker := time.NewTicker(hardDeleteLockHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-lease.stopCh:
				return
			case <-ticker.C:
				applied, err := renew()
				if err != nil {
					lease.setErr(fmt.Errorf("renew %s hard-delete lock for %s: %w", kind, target, err))
					return
				}
				if !applied {
					lease.setErr(fmt.Errorf("%s hard-delete lock for %s lost during cascade", kind, target))
					return
				}
			}
		}
	}()
	return lease
}

func (l *hardDeleteLease) setErr(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.err == nil {
		l.err = err
	}
}

func (l *hardDeleteLease) Check() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.err
}

func (l *hardDeleteLease) Close() {
	l.closeOnce.Do(func() {
		close(l.stopCh)
		<-l.closedChan
		if err := l.release(); err != nil {
			l.setErr(err)
		}
	})
}

func (w *Worker) newTimedFence(fence func() error, interval time.Duration) func() error {
	lastFenceAt := time.Time{}
	return func() error {
		now := w.clock().UTC()
		if interval > 0 && !lastFenceAt.IsZero() && !now.Before(lastFenceAt) && now.Sub(lastFenceAt) < interval {
			return nil
		}
		if err := fence(); err != nil {
			return err
		}
		lastFenceAt = now
		return nil
	}
}

// Worker drains the gc_queue and deletes items from S3 and the database.
type Worker struct {
	store       GCStore
	storage     StorageProvider
	queue       *Queue
	batchSize   int
	gracePeriod time.Duration
	dryRun      bool
	stats       *Stats
	clock       func() time.Time
}

// NewWorker creates a new GC worker.
func NewWorker(store GCStore, storage StorageProvider, queue *Queue, batchSize int, gracePeriod time.Duration, dryRun bool, stats *Stats) *Worker {
	return &Worker{
		store:       store,
		storage:     storage,
		queue:       queue,
		batchSize:   batchSize,
		gracePeriod: gracePeriod,
		dryRun:      dryRun,
		stats:       stats,
		clock:       time.Now,
	}
}

// ProcessOnce runs a single pass of the worker: find orgs with queued items,
// dequeue a batch for each, and process them.
func (w *Worker) ProcessOnce(ctx context.Context) (int, error) {
	orgs, err := w.queue.ListOrgsWithQueuedItems()
	if err != nil {
		return 0, fmt.Errorf("failed to list orgs: %w", err)
	}

	totalProcessed := 0
	for _, orgID := range orgs {
		select {
		case <-ctx.Done():
			return totalProcessed, ctx.Err()
		default:
		}

		n, err := w.processOrg(ctx, orgID)
		if err != nil {
			log.Printf("[GC Worker] Error processing org %s: %v", orgID, err)
			continue
		}
		totalProcessed += n
	}

	return totalProcessed, nil
}

// ProcessOrgOnce processes a single org's queued items in one pass. It is the
// scoped counterpart to ProcessOnce (which fans out across every active org) so
// callers — notably integration tests that enqueue work under a synthetic org —
// can drive GC for exactly that org without dequeuing unrelated orgs' items. A
// worker wired with a nil or partial storage provider must never touch another
// org's real blocks (it would route their S3 deletes down the slow recovery
// path), and this is the entry point that guarantees that scoping.
func (w *Worker) ProcessOrgOnce(ctx context.Context, orgID uuid.UUID) (int, error) {
	return w.processOrg(ctx, orgID)
}

// cleanupBlockMapping removes the single forward block_id_mappings row (external
// SHA-1 -> internal SHA-256) for a block being GC'd. externalSHA1 is the block's
// blocks.sha1, captured from GetBlockInfo BEFORE the canonical row is deleted.
//
// The reverse index (block_id_mappings_by_internal) was dropped in migration 006,
// so there is no alias enumeration: blocks.sha1 is the authoritative, single-
// valued external id (block encryption is deterministic — AES-CBC with a derived
// fixed IV — so SHA-256 -> SHA-1 is 1:1). The delete is a single-partition write
// by (org_id, external_id): no ALLOW FILTERING, no clustering/tombstone scan.
func (w *Worker) cleanupBlockMapping(orgID uuid.UUID, internalBlockID, representationID, externalSHA1 string) error {
	externalSHA1 = strings.TrimSpace(externalSHA1)
	if externalSHA1 == "" {
		// No server-derived SHA-1 to resolve the forward row. Without the reverse
		// index we cannot locate it; a leftover forward row is a harmless dangling
		// pointer (a desktop bare-SHA-1 block GET 404s; it self-heals if the
		// identical block is re-uploaded). Record it so any such leak is observable.
		metrics.GCAuditEventsTotal.WithLabelValues("gc_block_mapping_sha1_missing").Inc()
		return nil
	}
	representationID = strings.TrimSpace(representationID)
	if representationID == "" {
		metrics.GCAuditEventsTotal.WithLabelValues("gc_block_mapping_representation_missing").Inc()
		return nil
	}
	if err := w.store.DeleteBlockMappingExact(orgID, representationID, externalSHA1); err != nil {
		return fmt.Errorf("failed to delete forward block mapping %s for %s: %w", externalSHA1, internalBlockID, err)
	}
	return nil
}

func (w *Worker) processOrg(ctx context.Context, orgID uuid.UUID) (int, error) {
	activeBefore := w.clock()
	items, err := w.queue.DequeueBatch(orgID, w.batchSize, w.gracePeriod)
	if err != nil {
		return 0, err
	}

	processed := 0
	for _, item := range items {
		select {
		case <-ctx.Done():
			return processed, ctx.Err()
		default:
		}

		if err := w.processItem(ctx, item); err != nil {
			log.Printf("[GC Worker] Failed to process item %s/%s (type=%s): %v",
				item.OrgID, item.ItemID, item.ItemType, err)
			metrics.GCErrorsTotal.WithLabelValues(string(item.ItemType)).Inc()

			if isHardDeleteInProgressError(err) {
				if postponeErr := w.postponeItem(item); postponeErr != nil {
					log.Printf("[GC Worker] Failed to postpone lock-contended item %s/%s without retry increment: %v",
						item.OrgID, item.ItemID, postponeErr)
				}
				continue
			}

			// Requeue transient failures, but move retry-capped items into the DLQ
			// so they stop polluting the live queue forever. If the requeue itself
			// reports an error, do NOT blindly escalate to the DLQ: RequeueItem is
			// a LoggedBatch (DELETE old + INSERT new) and Cassandra timeout /
			// unavailable responses are ambiguous — the batch may have applied
			// even though the client saw an error. Re-checking the original row
			// tells us which side of the ambiguity we landed on:
			//
			//   - row gone  → batch applied, new requeued row is live; do nothing.
			//   - row still → batch did not apply; safe to escalate to the DLQ.
			//   - check err → unknown; leave the item in place rather than risk
			//                 a duplicated processing path. Next tick will retry.
			if item.RetryCount < 5 {
				if incErr := w.queue.IncrementRetry(item); incErr != nil {
					log.Printf("[GC Worker] Failed to requeue item %s/%s after error %v: %v",
						item.OrgID, item.ItemID, err, incErr)
					stillExists, checkErr := w.store.QueueItemExists(item.OrgID, item.QueuedAt, item.ItemType, item.ItemID)
					if checkErr != nil {
						log.Printf("[GC Worker] Cannot verify requeue state for %s/%s (%v); leaving item untouched to avoid double-processing",
							item.OrgID, item.ItemID, checkErr)
						continue
					}
					if !stillExists {
						log.Printf("[GC Worker] IncrementRetry returned %v but old row is already gone for %s/%s; treating as successful requeue",
							incErr, item.OrgID, item.ItemID)
						continue
					}
					escalation := fmt.Sprintf("requeue failed (%v) after processing error: %v", incErr, err)
					if failErr := w.store.FailItem(item, w.clock(), escalation, failureCodeForError(err)); failErr != nil {
						log.Printf("[GC Worker] Failed to escalate item %s/%s to DLQ after requeue failure: %v", item.OrgID, item.ItemID, failErr)
					}
				}
			} else {
				if failErr := w.store.FailItem(item, w.clock(), err.Error(), failureCodeForError(err)); failErr != nil {
					log.Printf("[GC Worker] Failed to move retry-capped item %s/%s to DLQ: %v", item.OrgID, item.ItemID, failErr)
				}
			}
			continue
		}

		if w.dryRun {
			continue
		}

		// Remove from queue
		if err := w.queue.Complete(item.OrgID, item.QueuedAt, item.ItemType, item.ItemID); err != nil {
			log.Printf("[GC Worker] Failed to complete item %s/%s: %v",
				item.OrgID, item.ItemID, err)
		}

		metrics.GCItemsProcessedTotal.WithLabelValues(string(item.ItemType)).Inc()
		processed++
	}

	if len(items) < w.batchSize {
		if len(items) > 0 {
			stats, statsErr := w.store.GetOrgQueueStats(orgID)
			if statsErr != nil {
				log.Printf("[GC Worker] Failed to read queue snapshot for org %s: %v", orgID, statsErr)
			} else if stats.QueueDepth > 0 {
				return processed, nil
			}
		}

		oldestQueuedAt, oldestErr := w.store.GetOldestQueuedAt(orgID)
		if oldestErr != nil {
			log.Printf("[GC Worker] Failed to inspect remaining queue state for org %s: %v", orgID, oldestErr)
			return processed, nil
		}
		if oldestQueuedAt == nil {
			if activeErr := w.store.RemoveOrgFromActiveSet(orgID, activeBefore); activeErr != nil {
				log.Printf("[GC Worker] Failed to remove org %s from active set: %v", orgID, activeErr)
			}
		}
	}

	return processed, nil
}

func (w *Worker) postponeItem(item QueueItem) error {
	return w.store.RequeueItem(
		item.OrgID,
		item.QueuedAt,
		w.clock(),
		item.ItemType,
		item.ItemID,
		item.LibraryID,
		item.BlockRepresentationID,
		item.StorageClass,
		item.RetryCount,
		effectiveIdentityAt(item.QueuedAt, item.IdentityAt),
		item.RequiresLibraryDeletedCheck,
		item.LibraryGuardMode,
	)
}

func (w *Worker) processItem(ctx context.Context, item QueueItem) error {
	switch item.ItemType {
	case ItemBlock:
		return w.processBlock(ctx, item)
	case ItemCommit:
		return w.processCommit(item)
	case ItemFSObject:
		return w.processFSObject(ctx, item)
	case ItemShareLink:
		return w.processShareLink(ctx, item)
	case ItemShare:
		return w.processShare(ctx, item)
	case ItemRestoreJob:
		return w.processRestoreJob(ctx, item)
	case ItemUserCascade:
		return w.processUserCascade(ctx, item)
	case ItemLibraryCascade:
		return w.processLibraryCascade(ctx, item)
	case ItemOrgCascade:
		return w.processOrgCascade(ctx, item)
	default:
		return fmt.Errorf("unknown item type: %s", item.ItemType)
	}
}

func (w *Worker) processBlock(ctx context.Context, item QueueItem) error {
	if w.dryRun {
		log.Printf("[GC Worker] DRY RUN: Would conditionally delete block %s from DB and S3", item.ItemID)
		return nil
	}

	candidateAt := effectiveIdentityAt(item.QueuedAt, item.IdentityAt)
	claimID := blockDeleteClaimID(candidateAt)

	// Pre-check: a block is alive iff it still has reference rows. This single-
	// partition point read replaces the old per-org full scan of live fs_objects.
	hasRefs, err := w.store.BlockHasReferences(item.OrgID, item.ItemID)
	if err != nil {
		return fmt.Errorf("failed to check block references for %s: %w", item.ItemID, err)
	}
	if hasRefs {
		log.Printf("[GC Worker] Block %s still referenced, skipping deletion", item.ItemID)
		if err := w.store.DeleteBlockGCCandidate(item.OrgID, item.ItemID, candidateAt); err != nil {
			return fmt.Errorf("failed to clear stale block GC candidate: %w", err)
		}
		metrics.GCItemsSkippedTotal.Inc()
		return nil
	}

	exists, err := w.store.BlockExists(item.OrgID, item.ItemID)
	if err != nil {
		return fmt.Errorf("failed to check canonical block row for %s: %w", item.ItemID, err)
	}
	if !exists {
		// Canonical blocks row is already gone, so blocks.sha1 is unavailable; the
		// forward mapping (if any) can no longer be resolved without the dropped
		// reverse index. cleanupBlockMapping records this as observable.
		if err := w.cleanupBlockMapping(item.OrgID, item.ItemID, "", ""); err != nil {
			return err
		}
		if err := w.store.DeleteBlockGCCandidate(item.OrgID, item.ItemID, candidateAt); err != nil {
			return fmt.Errorf("failed to clear stale block GC candidate: %w", err)
		}
		log.Printf("[GC Worker] Block %s missing canonical row, skipping deletion", item.ItemID)
		metrics.GCItemsSkippedTotal.Inc()
		return nil
	}

	// 1. Claim the block (gc_state='deleting') via LWT. claimID is stable for one
	// logical candidate so retries of the same item remain the owner, but a
	// different attempt cannot release or finalize another attempt's claim.
	applied, err := w.store.ClaimBlockDelete(item.OrgID, item.ItemID, claimID)
	if err != nil {
		return fmt.Errorf("failed to claim block record for deletion: %w", err)
	}
	if !applied {
		// Row missing or could not be claimed; nothing to delete from S3.
		if err := w.store.DeleteBlockGCCandidate(item.OrgID, item.ItemID, candidateAt); err != nil {
			return fmt.Errorf("failed to clear stale block GC candidate: %w", err)
		}
		log.Printf("[GC Worker] Block %s claim not applied (row gone), skipping S3 deletion", item.ItemID)
		metrics.GCItemsSkippedTotal.Inc()
		return nil
	}

	// 2. Claim-then-verify: re-check references AFTER claiming. If a concurrent
	// upload registered a reference, abandon the claim so the block stays alive.
	hasRefs, err = w.store.BlockHasReferences(item.OrgID, item.ItemID)
	if err != nil {
		return fmt.Errorf("failed to re-check block references for %s: %w", item.ItemID, err)
	}
	if hasRefs {
		blockInfo, infoErr := w.store.GetBlockInfo(item.OrgID, item.ItemID)
		if infoErr != nil {
			return fmt.Errorf("failed to load re-referenced block info for %s: %w", item.ItemID, infoErr)
		}
		if blockInfo.CreatedAt == nil {
			if strings.TrimSpace(blockInfo.StorageClass) != "" {
				return fmt.Errorf("stub block %s has storage class without creation timestamp", item.ItemID)
			}
			// The owned claim fences writers. Remove any partially backfilled mapping
			// before deleting the only row that carries its identity; retries are
			// idempotent if the subsequent conditional stub delete fails.
			if err := w.cleanupBlockMapping(item.OrgID, item.ItemID, blockInfo.RepresentationID, blockInfo.Sha1); err != nil {
				return err
			}
			deleted, deleteErr := w.store.DeleteClaimedBlockStub(item.OrgID, item.ItemID, claimID)
			if deleteErr != nil {
				return fmt.Errorf("failed to delete re-referenced claimed stub %s: %w", item.ItemID, deleteErr)
			}
			if !deleted {
				return fmt.Errorf("claimed stub %s changed before conditional delete", item.ItemID)
			}
			if err := w.store.DeleteBlockGCCandidate(item.OrgID, item.ItemID, candidateAt); err != nil {
				return fmt.Errorf("failed to clear block GC candidate after re-referenced stub cleanup: %w", err)
			}
			log.Printf("[GC Worker] Block %s re-referenced after a stub claim; removed the owned stub", item.ItemID)
			metrics.GCItemsSkippedTotal.Inc()
			return nil
		}
		if relErr := w.store.ReleaseBlockClaim(item.OrgID, item.ItemID, claimID); relErr != nil {
			return fmt.Errorf("failed to release claim on re-referenced block %s: %w", item.ItemID, relErr)
		}
		if err := w.store.DeleteBlockGCCandidate(item.OrgID, item.ItemID, candidateAt); err != nil {
			return fmt.Errorf("failed to clear block GC candidate after re-reference: %w", err)
		}
		log.Printf("[GC Worker] Block %s re-referenced after claim, skipping deletion", item.ItemID)
		metrics.GCItemsSkippedTotal.Inc()
		return nil
	}

	// 3. Persist the S3-pending record BEFORE removing the DB row. This closes the
	// crash window where the process dies after deleting the canonical row but
	// before recording recovery metadata for the later S3 delete.
	blockInfo, err := w.store.GetBlockInfo(item.OrgID, item.ItemID)
	if err != nil {
		return fmt.Errorf("failed to load canonical block info for %s: %w", item.ItemID, err)
	}
	storageClass := strings.TrimSpace(blockInfo.StorageClass)
	if storageClass == "" {
		if blockInfo.CreatedAt == nil {
			if err := w.cleanupBlockMapping(item.OrgID, item.ItemID, blockInfo.RepresentationID, blockInfo.Sha1); err != nil {
				return err
			}
			deleted, deleteErr := w.store.DeleteClaimedBlockStub(item.OrgID, item.ItemID, claimID)
			if deleteErr != nil {
				return fmt.Errorf("failed to remove stub block row for %s: %w", item.ItemID, deleteErr)
			}
			if !deleted {
				return fmt.Errorf("claimed stub %s changed before conditional delete", item.ItemID)
			}
			// Stub row carries no canonical metadata; blockInfo.Sha1 is captured
			// before deletion and is normally empty, but may have been backfilled by
			// an interrupted materialization attempt.
			if err := w.store.DeleteBlockGCCandidate(item.OrgID, item.ItemID, candidateAt); err != nil {
				return fmt.Errorf("failed to clear block GC candidate after stub cleanup: %w", err)
			}
			log.Printf("[GC Worker] Block %s missing canonical metadata after claim; removed stub row and skipped deletion", item.ItemID)
			metrics.GCItemsSkippedTotal.Inc()
			return nil
		}
		if relErr := w.store.ReleaseBlockClaim(item.OrgID, item.ItemID, claimID); relErr != nil {
			return fmt.Errorf("failed to release claim on malformed block %s: %w", item.ItemID, relErr)
		}
		return fmt.Errorf("block %s has empty canonical storage class", item.ItemID)
	}
	if item.StorageClass != "" && item.StorageClass != storageClass {
		log.Printf("[GC Worker] WARNING: block %s queued with storage_class=%s but canonical storage_class=%s; using canonical value", item.ItemID, item.StorageClass, storageClass)
	}
	orphanFirstSeenAt, err := w.store.StartBlockDeleteOrphan(item.OrgID, item.ItemID, storageClass, blockInfo.RepresentationID, blockInfo.Sha1, w.clock().UTC())
	if err != nil {
		return fmt.Errorf("failed to record pending S3 delete for block %s: %w", item.ItemID, err)
	}

	// 4. Now remove the claimed DB row. If this fails, the row stays claimed and
	// the queue item will retry; the pending S3 row already preserves recovery state.
	if err := w.store.FinalizeBlockDelete(item.OrgID, item.ItemID, claimID); err != nil {
		return fmt.Errorf("failed to finalize claimed block delete for %s: %w", item.ItemID, err)
	}

	// With no storage provider (degenerate/no-storage-manager config) there is no S3
	// step and RecoverS3Orphans is a no-op, so the recovery row has nothing left to
	// drive: clear it after mapping cleanup instead of leaving it to TTL. With
	// storage, the row is only cleared once the S3 delete has succeeded (or it stays
	// for RecoverS3Orphans to retry).
	clearRecoveryRow := w.storage == nil
	if w.storage != nil {
		blockStore, err := w.storage.GetBlockStoreForOrg(item.OrgID.String(), storageClass)
		if err != nil {
			return fmt.Errorf("failed to get block store for org %s class %s: %w", item.OrgID, storageClass, err)
		}
		if delErr := w.deleteS3WithRetry(ctx, blockStore, item.ItemID); delErr != nil {
			log.Printf("[GC Worker] WARNING: Failed to delete block %s from S3 after DB deletion: %v (recording for scanner recovery)", item.ItemID, delErr)
			if recErr := w.store.UpdateS3OrphanAttempt(item.OrgID, item.ItemID, delErr.Error(), w.clock()); recErr != nil {
				log.Printf("[GC Worker] ERROR: Failed to update S3 orphan %s: %v", item.ItemID, recErr)
				metrics.GCErrorsTotal.WithLabelValues("s3_orphan_record").Inc()
			}
			metrics.GCAuditEventsTotal.WithLabelValues("gc_block_s3_orphaned").Inc()
			// Do NOT return error — the block is recorded for recovery.
			// Continue to post-delete cleanup so the queue item completes.
		} else if err := w.store.MarkS3OrphanMappingCleanupPending(item.OrgID, item.ItemID, blockInfo.RepresentationID, blockInfo.Sha1, w.clock()); err != nil {
			log.Printf("[GC Worker] WARNING: S3 delete for block %s succeeded but failed to advance recovery row: %v", item.ItemID, err)
			clearRecoveryRow = true
		} else {
			clearRecoveryRow = true
		}
	}

	// 5. Clean up the forward mapping after the canonical row is gone, using the
	// blocks.sha1 captured in blockInfo BEFORE FinalizeBlockDelete. The recovery
	// row now persists this SHA-1 and remains live until this cleanup finishes, so
	// restart recovery can resume safely after either the DB delete or the S3 step.
	if err := w.cleanupBlockMapping(item.OrgID, item.ItemID, blockInfo.RepresentationID, blockInfo.Sha1); err != nil {
		return err
	}
	if clearRecoveryRow {
		if err := w.store.DeleteS3Orphan(item.OrgID, item.ItemID, orphanFirstSeenAt); err != nil {
			log.Printf("[GC Worker] WARNING: block %s mapping cleanup succeeded but failed to clear recovery row: %v", item.ItemID, err)
		}
	}

	if err := w.store.DeleteBlockGCCandidate(item.OrgID, item.ItemID, candidateAt); err != nil {
		return fmt.Errorf("failed to clear block GC candidate: %w", err)
	}

	w.stats.IncrBlocksDeleted()
	metrics.GCAuditEventsTotal.WithLabelValues("gc_block_deleted").Inc()
	log.Printf("[GC Worker] Deleted block %s", item.ItemID)
	return nil
}

func blockDeleteClaimID(candidateAt time.Time) string {
	return candidateAt.UTC().Format(time.RFC3339Nano)
}

// deleteS3WithRetry attempts to delete a block from S3 with exponential backoff.
// It is cancellable via the context. Returns nil on success; the last error
// otherwise. Retries are NOT applied to context cancellation.
func (w *Worker) deleteS3WithRetry(ctx context.Context, blockStore BlockStoreDeleter, blockID string) error {
	var lastErr error
	attempts := len(s3DeleteRetryDelays) + 1 // 1 initial try + N retries
	for i := 0; i < attempts; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := blockStore.DeleteBlock(ctx, blockID); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if i >= len(s3DeleteRetryDelays) {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s3DeleteRetryDelays[i]):
		}
	}
	return lastErr
}

// RecoverS3Orphans retries S3 deletes for orphan rows in gc_s3_orphans.
// Called by the scanner; exposed on the worker because it needs access to
// w.storage. Returns the number of orphans successfully recovered.
//
// Walks the gc_s3_orphans_by_day discovery projection from a persisted UTC-day
// cursor up to today. On cold start (no cursor) it scans the full 90-day TTL
// horizon so old orphan rows cannot get stranded forever. `perBucketLimit`
// caps the rows pulled per (day, bucket) so a single misbehaving bucket cannot
// starve the worker.
func (w *Worker) RecoverS3Orphans(ctx context.Context, perBucketLimit int) (int, error) {
	if w.storage == nil {
		return 0, nil
	}
	if w.dryRun {
		log.Println("[GC Worker] DRY RUN: skipping S3 orphan recovery")
		return 0, nil
	}
	if perBucketLimit <= 0 {
		perBucketLimit = 100
	}

	cutoffDay := db.GCProjectionUTCDate(w.clock())
	startDay, err := w.loadS3OrphansStartDay(cutoffDay)
	if err != nil {
		return 0, err
	}
	if startDay.After(cutoffDay) {
		return 0, nil
	}

	recovered := 0
	var phaseErr error
	for day := startDay; !day.After(cutoffDay); day = day.AddDate(0, 0, 1) {
		for bucket := 0; bucket < db.GCDiscoveryBucketCount; bucket++ {
			select {
			case <-ctx.Done():
				return recovered, ctx.Err()
			default:
			}

			orphans, err := w.store.ListS3OrphansByDay(day, bucket, perBucketLimit+1)
			if err != nil {
				log.Printf("[GC Worker] S3 orphan recovery: list failed for day=%s bucket=%d: %v", db.GCProjectionDateString(day), bucket, err)
				if phaseErr == nil {
					phaseErr = fmt.Errorf("list S3 orphan recovery partition day=%s bucket=%d: %w", db.GCProjectionDateString(day), bucket, err)
				}
				continue
			}
			if len(orphans) > perBucketLimit {
				log.Printf("[GC Worker] S3 orphan recovery: partition day=%s bucket=%d hit limit=%d; deferring cursor advance", db.GCProjectionDateString(day), bucket, perBucketLimit)
				if phaseErr == nil {
					phaseErr = fmt.Errorf("S3 orphan recovery partition day=%s bucket=%d incomplete after reaching limit=%d", db.GCProjectionDateString(day), bucket, perBucketLimit)
				}
				orphans = orphans[:perBucketLimit]
			}
			for _, orph := range orphans {
				select {
				case <-ctx.Done():
					return recovered, ctx.Err()
				default:
				}
				if strings.TrimSpace(orph.RecoveryPhase) == S3OrphanPhasePendingMappingCleanup {
					// Guard against block resurrection: if the same block_id was
					// re-uploaded after its delete (deterministic content -> same
					// block_id + same SHA-1), the live block now OWNS the forward
					// mapping. Deleting it here would strand the resurrected block (a
					// desktop bare-SHA-1 GET would 404, with no self-heal). Discard the
					// stale recovery row instead of cleaning the mapping.
					if exists, err := w.store.BlockExists(orph.OrgID, orph.BlockID); err != nil {
						log.Printf("[GC Worker] S3 orphan recovery: block existence lookup failed for org=%s block=%s: %v", orph.OrgID, orph.BlockID, err)
						if phaseErr == nil {
							phaseErr = fmt.Errorf("check block existence for mapping-cleanup orphan org=%s block=%s: %w", orph.OrgID, orph.BlockID, err)
						}
						continue
					} else if exists {
						if err := w.store.DeleteS3Orphan(orph.OrgID, orph.BlockID, orph.FirstSeenAt); err != nil {
							log.Printf("[GC Worker] S3 orphan recovery: failed to discard stale mapping-cleanup row for resurrected block %s: %v", orph.BlockID, err)
							if phaseErr == nil {
								phaseErr = fmt.Errorf("discard stale mapping-cleanup orphan for resurrected block %s: %w", orph.BlockID, err)
							}
							continue
						}
						metrics.GCAuditEventsTotal.WithLabelValues("gc_s3_orphan_resurrected_discarded").Inc()
						log.Printf("[GC Worker] S3 orphan recovery: block %s resurrected; discarded stale mapping-cleanup row, kept its live forward mapping", orph.BlockID)
						continue
					}
					if err := w.cleanupBlockMapping(orph.OrgID, orph.BlockID, orph.RepresentationID, orph.ExternalSHA1); err != nil {
						log.Printf("[GC Worker] S3 orphan recovery: forward mapping cleanup failed for org=%s block=%s: %v", orph.OrgID, orph.BlockID, err)
						if phaseErr == nil {
							phaseErr = fmt.Errorf("cleanup forward mapping for recovered block org=%s block=%s: %w", orph.OrgID, orph.BlockID, err)
						}
						continue
					}
					if err := w.store.DeleteS3Orphan(orph.OrgID, orph.BlockID, orph.FirstSeenAt); err != nil {
						log.Printf("[GC Worker] S3 orphan recovery: failed to clear mapping-cleanup row %s: %v", orph.BlockID, err)
						if phaseErr == nil {
							phaseErr = fmt.Errorf("clear mapping-cleanup orphan row for block %s: %w", orph.BlockID, err)
						}
						continue
					}
					recovered++
					metrics.GCAuditEventsTotal.WithLabelValues("gc_s3_orphan_recovered").Inc()
					log.Printf("[GC Worker] Recovered mapping cleanup for block %s (org=%s, retries=%d)", orph.BlockID, orph.OrgID, orph.RetryCount)
					continue
				}
				if exists, err := w.store.BlockExists(orph.OrgID, orph.BlockID); err != nil {
					log.Printf("[GC Worker] S3 orphan recovery: block existence lookup failed for org=%s block=%s: %v", orph.OrgID, orph.BlockID, err)
					if phaseErr == nil {
						phaseErr = fmt.Errorf("check block existence for S3 orphan org=%s block=%s: %w", orph.OrgID, orph.BlockID, err)
					}
					continue
				} else if exists {
					// The canonical block row still exists (likely claimed but not yet finalized).
					// Skip recovery for now; a later worker retry or startup scan will finish it.
					if phaseErr == nil {
						phaseErr = fmt.Errorf("S3 orphan recovery deferred for org=%s block=%s because canonical block row still exists", orph.OrgID, orph.BlockID)
					}
					continue
				}

				storageClass := strings.TrimSpace(orph.StorageClass)
				if storageClass == "" {
					if phaseErr == nil {
						phaseErr = fmt.Errorf("S3 orphan recovery row has empty storage class for org=%s block=%s", orph.OrgID, orph.BlockID)
					}
					continue
				}
				blockStore, err := w.storage.GetBlockStoreForOrg(orph.OrgID.String(), storageClass)
				if err != nil {
					log.Printf("[GC Worker] S3 orphan recovery: get block store for org=%s class=%s failed: %v", orph.OrgID, storageClass, err)
					if phaseErr == nil {
						phaseErr = fmt.Errorf("get block store for S3 orphan org=%s class=%s: %w", orph.OrgID, storageClass, err)
					}
					continue
				}
				if err := blockStore.DeleteBlock(ctx, orph.BlockID); err != nil {
					if updErr := w.store.UpdateS3OrphanAttempt(orph.OrgID, orph.BlockID, err.Error(), w.clock()); updErr != nil {
						log.Printf("[GC Worker] S3 orphan recovery: update attempt for %s failed: %v", orph.BlockID, updErr)
						if phaseErr == nil {
							phaseErr = fmt.Errorf("update S3 orphan attempt for block %s: %w", orph.BlockID, updErr)
						}
					}
					if phaseErr == nil {
						phaseErr = fmt.Errorf("delete S3 orphan block %s from backing store: %w", orph.BlockID, err)
					}
					continue
				}
				if err := w.store.MarkS3OrphanMappingCleanupPending(orph.OrgID, orph.BlockID, orph.RepresentationID, orph.ExternalSHA1, w.clock()); err != nil {
					log.Printf("[GC Worker] S3 orphan recovery: failed to advance %s to mapping cleanup: %v", orph.BlockID, err)
					if phaseErr == nil {
						phaseErr = fmt.Errorf("advance recovered block %s to mapping cleanup: %w", orph.BlockID, err)
					}
					continue
				}
				if err := w.cleanupBlockMapping(orph.OrgID, orph.BlockID, orph.RepresentationID, orph.ExternalSHA1); err != nil {
					log.Printf("[GC Worker] S3 orphan recovery: forward mapping cleanup failed after S3 delete for org=%s block=%s: %v", orph.OrgID, orph.BlockID, err)
					if phaseErr == nil {
						phaseErr = fmt.Errorf("cleanup forward mapping after S3 recovery for block %s: %w", orph.BlockID, err)
					}
					continue
				}
				if err := w.store.DeleteS3Orphan(orph.OrgID, orph.BlockID, orph.FirstSeenAt); err != nil {
					log.Printf("[GC Worker] S3 orphan recovery: failed to clear orphan row %s: %v", orph.BlockID, err)
					if phaseErr == nil {
						phaseErr = fmt.Errorf("clear S3 orphan row for block %s: %w", orph.BlockID, err)
					}
					continue
				}
				recovered++
				metrics.GCAuditEventsTotal.WithLabelValues("gc_s3_orphan_recovered").Inc()
				log.Printf("[GC Worker] Recovered S3 orphan %s (org=%s, retries=%d)", orph.BlockID, orph.OrgID, orph.RetryCount)
			}
		}
	}

	if phaseErr == nil {
		newCursor := cutoffDay.AddDate(0, 0, -1)
		if !newCursor.Before(startDay) {
			if err := w.store.SaveGCStats(gcS3OrphansCursorKey, db.GCProjectionDateString(newCursor)); err != nil {
				phaseErr = fmt.Errorf("persist S3 orphan recovery cursor: %w", err)
			}
		}
	}

	return recovered, phaseErr
}

func (w *Worker) loadS3OrphansStartDay(cutoffDay time.Time) (time.Time, error) {
	value, err := w.store.LoadGCStats(gcS3OrphansCursorKey)
	return s3OrphansRecoveryStartDayFromCursor(value, err, cutoffDay)
}

func s3OrphansRecoveryStartDayFromCursor(value string, loadErr error, cutoffDay time.Time) (time.Time, error) {
	if loadErr != nil {
		if errors.Is(loadErr, gocql.ErrNotFound) {
			return s3OrphansRecoveryScanStartDay(time.Time{}, cutoffDay), nil
		}
		return time.Time{}, loadErr
	}
	lastDay, err := db.ParseGCProjectionDate(value)
	if err != nil {
		return time.Time{}, err
	}
	return s3OrphansRecoveryScanStartDay(lastDay, cutoffDay), nil
}

func s3OrphansRecoveryScanStartDay(lastProcessedDay, cutoffDay time.Time) time.Time {
	if lastProcessedDay.IsZero() {
		return cutoffDay.AddDate(0, 0, -gcS3OrphanInitialScanLookbackDays)
	}
	return lastProcessedDay.AddDate(0, 0, -gcScanOverlapDays)
}

// gcS3OrphanInitialScanLookbackDays bounds the cold-start recovery sweep when
// no cursor exists yet. Match the gc_s3_orphans / gc_s3_orphans_by_day TTL so
// the first pass can still see every live orphan row.
const gcS3OrphanInitialScanLookbackDays = 90

func (w *Worker) processCommit(item QueueItem) error {
	// Get the commit to find its root_fs_id for cascading deletion
	commit, err := w.store.GetCommit(item.LibraryID, item.ItemID)
	if err != nil {
		// Commit may already be deleted
		log.Printf("[GC Worker] Commit %s not found (may already be deleted)", item.ItemID)
		return nil
	}

	if w.dryRun {
		log.Printf("[GC Worker] DRY RUN: Would delete commit %s from library %s", item.ItemID, item.LibraryID)
		return nil
	}

	identityAt := effectiveIdentityAt(item.QueuedAt, item.IdentityAt)
	releaseGuard, fenceGuard, stale, err := w.acquireLibraryDeleteGuard(item)
	if err != nil {
		return err
	}
	if stale {
		return nil
	}
	defer releaseGuard()

	// Enqueue the root fs_object for cascading deletion (fs_object → blocks).
	// Use parent's QueuedAt so cascade children skip the grace period.
	// CRITICAL: if enqueue fails, we must NOT delete the commit — otherwise
	// the root fs_object becomes an orphan with no GC entry. The next scanner
	// sweep will re-discover and re-enqueue this commit.
	if commit.RootFSID != "" {
		exists, err := w.store.PendingItemExists(item.OrgID, item.LibraryID, time.Time{}, ItemFSObject, commit.RootFSID)
		if err != nil {
			return fmt.Errorf("failed to inspect root fs_object %s for commit %s: %w", commit.RootFSID, item.ItemID, err)
		}
		if !exists {
			child := QueueItem{
				OrgID:                       item.OrgID,
				QueuedAt:                    item.QueuedAt,
				IdentityAt:                  identityAt,
				RequiresLibraryDeletedCheck: item.RequiresLibraryDeletedCheck,
				LibraryGuardMode:            item.LibraryGuardMode,
				ItemType:                    ItemFSObject,
				ItemID:                      commit.RootFSID,
				LibraryID:                   item.LibraryID,
				BlockRepresentationID:       item.BlockRepresentationID,
			}
			if err := w.queue.EnqueueBatch([]QueueItem{child}); err != nil {
				return fmt.Errorf("failed to enqueue root fs_object %s for commit %s: %w", commit.RootFSID, item.ItemID, err)
			}
		}
	}

	// Fence immediately before the destructive delete: re-confirm we still own the
	// library hard-delete lock so a lease lost to expiry/restore cannot let us drop a
	// live library's commit. Fail closed (item stays queued and re-validates on retry).
	if err := fenceGuard(); err != nil {
		return err
	}
	if err := w.store.DeleteCommit(item.LibraryID, item.ItemID); err != nil {
		return fmt.Errorf("failed to delete commit: %w", err)
	}

	log.Printf("[GC Worker] Deleted commit %s", item.ItemID)
	return nil
}

func (w *Worker) processFSObject(ctx context.Context, item QueueItem) error {
	// Get the fs_object to find its block_ids
	fsObj, err := w.store.GetFSObject(item.LibraryID, item.ItemID)
	if err != nil {
		// Already deleted
		log.Printf("[GC Worker] FS object %s not found (may already be deleted)", item.ItemID)
		return nil
	}

	if w.dryRun {
		log.Printf("[GC Worker] DRY RUN: Would delete fs_object %s from library %s", item.ItemID, item.LibraryID)
		return nil
	}

	identityAt := effectiveIdentityAt(item.QueuedAt, item.IdentityAt)
	releaseGuard, fenceGuard, stale, err := w.acquireLibraryDeleteGuard(item)
	if err != nil {
		return err
	}
	if stale {
		return nil
	}
	defer releaseGuard()

	// If it's a directory, enqueue child fs_objects for recursive deletion.
	// Use parent's QueuedAt so cascade children skip the grace period.
	if len(fsObj.DirEntries) > 0 {
		var batch []QueueItem
		for _, childID := range fsObj.DirEntries {
			exists, err := w.store.PendingItemExists(item.OrgID, item.LibraryID, time.Time{}, ItemFSObject, childID)
			if err != nil {
				return fmt.Errorf("failed to inspect child fs_object %s for parent %s: %w", childID, item.ItemID, err)
			}
			if exists {
				continue
			}
			batch = append(batch, QueueItem{
				OrgID:                       item.OrgID,
				QueuedAt:                    item.QueuedAt,
				IdentityAt:                  identityAt,
				RequiresLibraryDeletedCheck: item.RequiresLibraryDeletedCheck,
				LibraryGuardMode:            item.LibraryGuardMode,
				ItemType:                    ItemFSObject,
				ItemID:                      childID,
				LibraryID:                   item.LibraryID,
				BlockRepresentationID:       item.BlockRepresentationID,
				StorageClass:                "",
				RetryCount:                  0,
			})
		}
		if err := w.queue.EnqueueBatch(batch); err != nil {
			log.Printf("[GC Worker] Failed to batch enqueue children for %s: %v", item.ItemID, err)
			return err
		}
	}

	// If it's a file with blocks, remove its permanent block references. Any block
	// left with no references becomes a GC candidate. Re-fence periodically during
	// long loops so a suspended worker cannot keep mutating with a stale lease.
	if len(fsObj.BlockIDs) > 0 {
		referenceFence := w.newTimedFence(fenceGuard, fsObjectReferenceFenceInterval)
		zeroRefBlocks, err := w.removeFSObjectBlockReferences(item.OrgID, item.LibraryID, item.BlockRepresentationID, item.ItemID, fsObj.BlockIDs, referenceFence)
		if err != nil {
			return err
		}
		storageClass, _ := w.store.GetLibraryStorageClass(item.OrgID, item.LibraryID)
		if len(zeroRefBlocks) > 0 {
			if err := w.enqueueZeroRefBlocks(item.OrgID, item.LibraryID, zeroRefBlocks, storageClass); err != nil {
				return fmt.Errorf("failed to enqueue zero-ref blocks for fs_object %s: %w", item.ItemID, err)
			}
		}
	}

	// Delete the fs_object. Fence immediately before this destructive delete so a lease
	// lost to expiry/restore cannot let us drop a node from a live/restored library
	// (the directory branch above releases no references, so this is its only fence).
	if err := fenceGuard(); err != nil {
		return err
	}
	if err := w.store.DeleteFSObject(item.LibraryID, item.ItemID); err != nil {
		return fmt.Errorf("failed to delete fs_object: %w", err)
	}

	log.Printf("[GC Worker] Deleted fs_object %s", item.ItemID)
	return nil
}

func (w *Worker) processShareLink(ctx context.Context, item QueueItem) error {
	if w.dryRun {
		log.Printf("[GC Worker] DRY RUN: Would delete share link %s", item.ItemID)
		return nil
	}

	if err := w.store.DeleteShareLink(item.ItemID, item.OrgID, item.LibraryID); err != nil {
		return fmt.Errorf("failed to delete share link: %w", err)
	}

	log.Printf("[GC Worker] Deleted share link %s", item.ItemID)
	return nil
}

func (w *Worker) processShare(ctx context.Context, item QueueItem) error {
	if w.dryRun {
		log.Printf("[GC Worker] DRY RUN: Would delete share %s", item.ItemID)
		return nil
	}

	shareID, err := uuid.Parse(item.ItemID)
	if err != nil {
		return fmt.Errorf("invalid share ID: %w", err)
	}

	if err := w.store.DeleteShare(item.LibraryID, shareID); err != nil {
		return fmt.Errorf("failed to delete share: %w", err)
	}

	log.Printf("[GC Worker] Deleted share %s", item.ItemID)
	return nil
}

func (w *Worker) processRestoreJob(ctx context.Context, item QueueItem) error {
	if w.dryRun {
		log.Printf("[GC Worker] DRY RUN: Would delete restore job %s", item.ItemID)
		return nil
	}

	jobID, err := uuid.Parse(item.ItemID)
	if err != nil {
		return fmt.Errorf("invalid restore job ID: %w", err)
	}

	if err := w.store.DeleteRestoreJob(item.OrgID, item.LibraryID, jobID); err != nil {
		return fmt.Errorf("failed to delete restore job: %w", err)
	}

	log.Printf("[GC Worker] Deleted restore job %s", item.ItemID)
	return nil
}

// processUserCascade performs the full cascade deletion of a soft-deleted user:
// 1. Soft-delete all owned libraries (move to trash)
// 2. Remove from all groups
// 3. Clean up shares received by and created by this user
// 4. Delete starred files and monitored repos
// 5. Hard-delete user record + email lookup
// 6. Audit log
func (w *Worker) processUserCascade(ctx context.Context, item QueueItem) error {
	if w.dryRun {
		log.Printf("[GC Worker] DRY RUN: Would cascade-delete user %s in org %s", item.ItemID, item.OrgID)
		return nil
	}

	userID, err := uuid.Parse(item.ItemID)
	if err != nil {
		return fmt.Errorf("invalid user ID: %w", err)
	}

	deletedAt, err := w.store.GetUserDeletedAt(item.OrgID, userID)
	if err != nil {
		return fmt.Errorf("failed to read deleted user marker for %s: %w", item.ItemID, err)
	}
	identityAt := effectiveIdentityAt(item.QueuedAt, item.IdentityAt)
	if deletedAt == nil || !deletedAt.Equal(identityAt) {
		log.Printf("[GC Worker] Skipping stale user cascade for %s (current deleted_at=%v identity_at=%v queued_at=%v)", item.ItemID, deletedAt, identityAt, item.QueuedAt)
		return nil
	}

	// Acquire a short-lived lock so a concurrent activateUser (restore) cannot
	// race between the stale-check above and the final HardDeleteUser write.
	leaseToken := uuid.New()
	acquired, err := w.store.AcquireUserHardDeleteLock(userID, leaseToken)
	if err != nil {
		return fmt.Errorf("failed to acquire user hard-delete lock for %s: %w", item.ItemID, err)
	}
	if !acquired {
		return hardDeleteInProgressError{Kind: "user", Target: userID.String(), ItemID: item.ItemID}
	}
	lease := newHardDeleteLease(ctx, "user", userID.String(), func() (bool, error) {
		return w.store.RenewUserHardDeleteLock(userID, leaseToken)
	}, func() error {
		return w.store.ReleaseUserHardDeleteLock(userID, leaseToken)
	})
	defer lease.Close()

	// Secondary stale-check after the lock: if the user was restored in the
	// window between the first stale-check and the lock acquisition, skip.
	deletedAt2, err := w.store.GetUserDeletedAt(item.OrgID, userID)
	if err != nil {
		return fmt.Errorf("failed to re-read deleted user marker for %s: %w", item.ItemID, err)
	}
	if deletedAt2 == nil || !deletedAt2.Equal(identityAt) {
		log.Printf("[GC Worker] Skipping user cascade for %s after lock: restored between checks (deleted_at=%v identity_at=%v)", item.ItemID, deletedAt2, identityAt)
		return nil
	}
	if err := lease.Check(); err != nil {
		return err
	}

	// Get user email before deletion (needed for users_by_email cleanup)
	email, err := w.store.GetUserEmail(item.OrgID, userID)
	if err != nil {
		return fmt.Errorf("failed to read user email for %s: %w", item.ItemID, err)
	}

	libCount, err := w.softDeleteUserLibraries(item.OrgID, userID)
	if err != nil {
		return fmt.Errorf("failed to soft-delete libraries owned by user %s: %w", item.ItemID, err)
	}
	if err := lease.Check(); err != nil {
		return err
	}

	groupCount, shareCount, err := w.cleanupUserArtifacts(item.OrgID, userID)
	if err != nil {
		return fmt.Errorf("failed to clean up artifacts for user %s: %w", item.ItemID, err)
	}
	if err := lease.Check(); err != nil {
		return err
	}

	// 5. Hard-delete user record + email lookup
	if err := w.store.HardDeleteUser(item.OrgID, userID, email); err != nil {
		return fmt.Errorf("failed to hard-delete user %s: %w", item.ItemID, err)
	}

	// 6. Audit log
	w.store.WriteAuditLog(AuditLogEntry{
		OrgID:      item.OrgID,
		Action:     "gc_user_cascade_deleted",
		TargetType: "user",
		TargetID:   item.ItemID,
		ActorID:    "gc_worker",
		Details:    fmt.Sprintf("email=%s libraries=%d groups=%d shares=%d", email, libCount, groupCount, shareCount),
		Timestamp:  time.Now(),
	})

	log.Printf("[GC Worker] Cascade-deleted user %s (%s): %d libraries, %d groups, %d shares",
		item.ItemID, email, libCount, groupCount, shareCount)
	return nil
}

func (w *Worker) softDeleteUserLibraries(orgID, userID uuid.UUID) (int, error) {
	libIDs, err := w.store.ListLibrariesByOwner(orgID, userID)
	if err != nil {
		return 0, fmt.Errorf("list owned libraries: %w", err)
	}

	var cleanupErr error
	for _, libID := range libIDs {
		if err := w.store.SoftDeleteLibrary(orgID, libID, userID); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("soft-delete library %s: %w", libID, err))
		}
	}
	return len(libIDs), cleanupErr
}

func (w *Worker) cleanupUserArtifacts(orgID, userID uuid.UUID) (int, int, error) {
	var cleanupErr error

	groupIDs, err := w.store.ListGroupMembershipsByUser(orgID, userID)
	if err != nil {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("list group memberships: %w", err))
	} else {
		for _, groupID := range groupIDs {
			if err := w.store.DeleteGroupMember(groupID, userID); err != nil {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("delete group member %s/%s: %w", groupID, userID, err))
			}
			if err := w.store.DeleteGroupByMember(orgID, userID, groupID); err != nil {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("delete group by member %s/%s/%s: %w", orgID, userID, groupID, err))
			}
		}
	}

	shareCount, err := w.deleteUserShares(orgID, userID)
	if err != nil {
		cleanupErr = errors.Join(cleanupErr, err)
	}
	if err := w.store.DeleteStarredFilesByUser(userID); err != nil {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("delete starred files: %w", err))
	}
	if err := w.store.DeleteMonitoredReposByUser(userID); err != nil {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("delete monitored repos: %w", err))
	}
	if err := w.store.DeleteAPIKeysByUser(orgID, userID); err != nil {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("delete api keys: %w", err))
	}

	return len(groupIDs), shareCount, cleanupErr
}

func (w *Worker) deleteUserShares(orgID, userID uuid.UUID) (int, error) {
	shareRefs := make(map[string]ShareInfo)
	var cleanupErr error

	receivedShares, err := w.store.ListSharesByUser(orgID, userID)
	if err != nil {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("list received shares: %w", err))
	} else {
		for _, share := range receivedShares {
			key := share.LibraryID.String() + ":" + share.ShareID.String()
			shareRefs[key] = ShareInfo{LibraryID: share.LibraryID, ShareID: share.ShareID, SharedTo: share.SharedTo}
		}
	}

	createdShares, err := w.store.ListSharesCreatedByUser(orgID, userID)
	if err != nil {
		cleanupErr = errors.Join(cleanupErr, fmt.Errorf("list created shares: %w", err))
	} else {
		for _, share := range createdShares {
			key := share.LibraryID.String() + ":" + share.ShareID.String()
			shareRefs[key] = ShareInfo{LibraryID: share.LibraryID, ShareID: share.ShareID}
		}
	}

	for _, share := range shareRefs {
		if err := w.store.DeleteShare(share.LibraryID, share.ShareID); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("delete share %s/%s: %w", share.LibraryID, share.ShareID, err))
		}
	}

	return len(shareRefs), cleanupErr
}

// processLibraryCascade performs the full cascade deletion of a soft-deleted library:
// 1. Enqueue all commits, fs_objects, and clean up artifacts
// 2. Hard-delete the library record
// 3. Audit log
func (w *Worker) processLibraryCascade(ctx context.Context, item QueueItem) error {
	if w.dryRun {
		log.Printf("[GC Worker] DRY RUN: Would cascade-delete library %s in org %s", item.ItemID, item.OrgID)
		return nil
	}

	libraryID, err := uuid.Parse(item.ItemID)
	if err != nil {
		return fmt.Errorf("invalid library ID: %w", err)
	}

	deletedAt, err := w.store.GetLibraryDeletedAt(libraryID)
	if err != nil {
		return fmt.Errorf("failed to read deleted library marker for %s: %w", item.ItemID, err)
	}
	identityAt := effectiveIdentityAt(item.QueuedAt, item.IdentityAt)
	if deletedAt == nil || !deletedAt.Equal(identityAt) {
		if deletedAt == nil {
			// The delete marker is gone: either the library was restored (canonical
			// row present again) or a prior cascade pass already hard-deleted it. In
			// the latter case a crash between HardDeleteLibrary and
			// DeleteLibraryStorageCounter may have left the per-library storage
			// counter behind — reclaim it, but only after confirming the canonical
			// row is absent so we never disturb a restored library's live counter.
			if err := w.reclaimHardDeletedLibraryStorageCounter(item.OrgID, libraryID); err != nil {
				return err
			}
		}
		log.Printf("[GC Worker] Skipping stale library cascade for %s (current deleted_at=%v identity_at=%v queued_at=%v)", item.ItemID, deletedAt, identityAt, item.QueuedAt)
		return nil
	}

	lease, fenceLibrary, err := w.acquireLibraryCascadeLease(ctx, libraryID, item.ItemID)
	if err != nil {
		return err
	}
	defer lease.Close()

	// Second stale-check after acquiring the lock.
	deletedAt2, err := w.store.GetLibraryDeletedAt(libraryID)
	if err != nil {
		return fmt.Errorf("failed to re-read deleted library marker for %s: %w", item.ItemID, err)
	}
	if deletedAt2 == nil || !deletedAt2.Equal(identityAt) {
		log.Printf("[GC Worker] Skipping stale library cascade for %s after lock (current deleted_at=%v identity_at=%v queued_at=%v)", item.ItemID, deletedAt2, identityAt, item.QueuedAt)
		return nil
	}
	if err := lease.Check(); err != nil {
		return err
	}

	if err := w.cascadeDeleteLibrary(item.OrgID, libraryID, item.BlockRepresentationID, item.StorageClass, identityAt, fenceLibrary); err != nil {
		return err
	}
	return lease.Check()
}

// reclaimHardDeletedLibraryStorageCounter idempotently deletes the per-library
// storage counter of a library whose canonical row is already gone. It exists to
// recover from a crash between HardDeleteLibrary and DeleteLibraryStorageCounter
// (see cascadeDeleteLibrary): the hard delete succeeded but the counter cleanup
// did not, leaving an orphaned counter that would otherwise inflate future
// reconciliation. It refuses to act while the canonical row still exists, so a
// restored library keeps its live counter. Fails closed on read errors.
func (w *Worker) reclaimHardDeletedLibraryStorageCounter(orgID, libraryID uuid.UUID) error {
	exists, err := w.store.CanonicalLibraryExists(orgID, libraryID)
	if err != nil {
		return fmt.Errorf("failed to confirm canonical library absence before storage-counter reclaim for %s: %w", libraryID, err)
	}
	if exists {
		return nil
	}
	if err := w.store.DeleteLibraryStorageCounter(orgID, libraryID); err != nil {
		return fmt.Errorf("failed to reclaim storage counter for hard-deleted library %s: %w", libraryID, err)
	}
	return nil
}

func (w *Worker) acquireLibraryCascadeLease(ctx context.Context, libraryID uuid.UUID, itemID string) (*hardDeleteLease, func() error, error) {
	leaseToken := uuid.New()
	acquired, err := w.store.AcquireLibraryHardDeleteLock(libraryID, leaseToken)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to acquire library hard-delete lock for %s: %w", itemID, err)
	}
	if !acquired {
		return nil, nil, hardDeleteInProgressError{Kind: "library", Target: libraryID.String(), ItemID: itemID}
	}
	lease := newHardDeleteLease(ctx, "library", libraryID.String(), func() (bool, error) {
		return w.store.RenewLibraryHardDeleteLock(libraryID, leaseToken)
	}, func() error {
		return w.store.ReleaseLibraryHardDeleteLock(libraryID, leaseToken)
	})
	fence := func() error {
		owned, err := w.store.RenewLibraryHardDeleteLock(libraryID, leaseToken)
		if err != nil {
			return fmt.Errorf("failed to fence library cascade for %s: %w", libraryID, err)
		}
		if !owned {
			return fmt.Errorf("lost library hard-delete lock for %s", libraryID)
		}
		return nil
	}
	return lease, fence, nil
}

func (w *Worker) cascadeDeleteLibrary(orgID, libraryID uuid.UUID, blockRepresentationID, storageClass string, libraryDeletedAt time.Time, fenceLibrary func() error) error {
	if storageClass == "" {
		storageClass, _ = w.store.GetLibraryStorageClass(orgID, libraryID)
	}

	if err := w.enqueueLibraryContentsAt(orgID, libraryID, blockRepresentationID, storageClass, libraryDeletedAt, LibraryGuardDeletedAtIdentity); err != nil {
		return fmt.Errorf("failed to enqueue library contents: %w", err)
	}

	// Fence before the point of no return, then hard-delete the canonical row +
	// marker FIRST. Ordering matters: once the canonical `libraries` row is gone,
	// restoreDeletedLibrary can no longer resurrect the library, so a concurrent
	// restore cannot observe a deleted storage counter and reactivate an
	// under-counted library. Deleting the counter before the hard delete (the old
	// order) left exactly that window. See DEBT-GC-COUNTER-ORDERING history.
	if err := fenceLibrary(); err != nil {
		return err
	}
	if err := w.store.HardDeleteLibrary(orgID, libraryID); err != nil {
		return fmt.Errorf("failed to hard-delete library %s: %w", libraryID, err)
	}

	// The library is now definitively gone — record the audit here (not after the
	// counter cleanup) so the event is captured even if the reclamation below has to
	// be retried on a later pass.
	w.store.WriteAuditLog(AuditLogEntry{
		OrgID:      orgID,
		Action:     "gc_library_cascade_deleted",
		TargetType: "library",
		TargetID:   libraryID.String(),
		ActorID:    "gc_worker",
		Details:    fmt.Sprintf("storage_class=%s", storageClass),
		Timestamp:  time.Now(),
	})
	log.Printf("[GC Worker] Cascade-deleted library %s (storage_class=%s)", libraryID, storageClass)

	// Reclaim the per-library storage counter after the library is gone. No fence
	// here: the canonical row no longer exists, so losing the lease past this point
	// cannot corrupt a live/restored library. A failure still returns an error so the
	// item is retried; the retry lands on the canonical-absent reclamation path in
	// processLibraryCascade, which cleans the orphaned counter idempotently.
	if err := w.store.DeleteLibraryStorageCounter(orgID, libraryID); err != nil {
		return fmt.Errorf("failed to delete library storage counter for %s: %w", libraryID, err)
	}

	return nil
}

// processOrgCascade performs the full cascade deletion of a soft-deleted organization:
// 1. Cascade-delete all libraries synchronously
// 2. Clean up all users (shares, starred, monitored, hard-delete)
// 3. Delete all groups (members, by_member, by_id, group record)
// 4. Hard-delete org record
// 5. Audit log
func (w *Worker) processOrgCascade(ctx context.Context, item QueueItem) error {
	if w.dryRun {
		log.Printf("[GC Worker] DRY RUN: Would cascade-delete org %s", item.ItemID)
		return nil
	}

	orgID := item.OrgID
	deletedAt, err := w.store.GetOrgDeletedAt(orgID)
	if err != nil {
		return fmt.Errorf("failed to read deleted org marker for %s: %w", item.ItemID, err)
	}
	identityAt := effectiveIdentityAt(item.QueuedAt, item.IdentityAt)
	if deletedAt == nil || !deletedAt.Equal(identityAt) {
		log.Printf("[GC Worker] Skipping stale org cascade for %s (current deleted_at=%v identity_at=%v queued_at=%v)", item.ItemID, deletedAt, identityAt, item.QueuedAt)
		return nil
	}

	leaseToken := uuid.New()
	acquired, err := w.store.AcquireOrgHardDeleteLock(orgID, leaseToken)
	if err != nil {
		return fmt.Errorf("failed to acquire org hard-delete lock for %s: %w", item.ItemID, err)
	}
	if !acquired {
		return hardDeleteInProgressError{Kind: "org", Target: orgID.String(), ItemID: item.ItemID}
	}
	lease := newHardDeleteLease(ctx, "org", orgID.String(), func() (bool, error) {
		return w.store.RenewOrgHardDeleteLock(orgID, leaseToken)
	}, func() error {
		return w.store.ReleaseOrgHardDeleteLock(orgID, leaseToken)
	})
	defer lease.Close()

	// Secondary stale-check after the lock: if the org was restored in the
	// window between the first stale-check and the lock acquisition, skip.
	deletedAt2, err := w.store.GetOrgDeletedAt(orgID)
	if err != nil {
		return fmt.Errorf("failed to re-read deleted org marker for %s: %w", item.ItemID, err)
	}
	if deletedAt2 == nil || !deletedAt2.Equal(identityAt) {
		log.Printf("[GC Worker] Skipping org cascade for %s after lock: restored between checks (deleted_at=%v identity_at=%v)", item.ItemID, deletedAt2, identityAt)
		return nil
	}
	if err := lease.Check(); err != nil {
		return err
	}

	purging, err := w.store.BeginOrgPurge(orgID, identityAt)
	if err != nil {
		return fmt.Errorf("failed to transition org %s into purge state: %w", item.ItemID, err)
	}
	if !purging {
		log.Printf("[GC Worker] Skipping org cascade for %s after purge transition race (identity_at=%v)", item.ItemID, identityAt)
		return nil
	}

	orgName, err := w.store.GetOrgName(orgID)
	if err != nil {
		return fmt.Errorf("failed to read org name for %s: %w", item.ItemID, err)
	}

	libs, err := w.store.ListLibrariesForOrg(orgID)
	if err != nil {
		return fmt.Errorf("failed to list libraries for org %s: %w", item.ItemID, err)
	}
	for _, lib := range libs {
		if err := lease.Check(); err != nil {
			return err
		}
		libraryLease, fenceLibrary, err := w.acquireLibraryCascadeLease(ctx, lib.LibraryID, item.ItemID)
		if err != nil {
			return err
		}
		funcErr := func() error {
			defer libraryLease.Close()
			if err := lease.Check(); err != nil {
				return err
			}
			deletedLibraryAt, err := w.store.GetLibraryDeletedAt(lib.LibraryID)
			if err != nil {
				return fmt.Errorf("failed to read deleted library marker for %s during org cascade: %w", lib.LibraryID, err)
			}
			if deletedLibraryAt == nil {
				exists, err := w.store.CanonicalLibraryExists(orgID, lib.LibraryID)
				if err != nil {
					return fmt.Errorf("failed to read canonical library row for %s during org cascade: %w", lib.LibraryID, err)
				}
				if !exists {
					return nil
				}
				if err := w.store.SoftDeleteLibrary(orgID, lib.LibraryID, uuid.Nil); err != nil {
					return fmt.Errorf("failed to soft-delete library %s during org cascade: %w", lib.LibraryID, err)
				}
				deletedLibraryAt, err = w.store.GetLibraryDeletedAt(lib.LibraryID)
				if err != nil {
					return fmt.Errorf("failed to read deleted library marker for %s during org cascade: %w", lib.LibraryID, err)
				}
				if deletedLibraryAt == nil {
					return fmt.Errorf("missing deleted library marker for %s during org cascade", lib.LibraryID)
				}
			}
			blockRepresentationID, err := resolveRequiredLibraryBlockRepresentation(w.store, orgID, lib.LibraryID, "", "org cascade")
			if err != nil {
				return err
			}
			if err := libraryLease.Check(); err != nil {
				return err
			}
			if err := w.cascadeDeleteLibrary(orgID, lib.LibraryID, blockRepresentationID, lib.StorageClass, *deletedLibraryAt, fenceLibrary); err != nil {
				return fmt.Errorf("failed to cascade-delete library %s during org delete: %w", lib.LibraryID, err)
			}
			return libraryLease.Check()
		}()
		if funcErr != nil {
			return funcErr
		}
		if err := lease.Check(); err != nil {
			return err
		}
	}

	users, err := w.store.ListUsersByOrg(orgID)
	if err != nil {
		return fmt.Errorf("failed to list users for org %s: %w", item.ItemID, err)
	}
	for _, u := range users {
		if err := lease.Check(); err != nil {
			return err
		}
		if _, _, err := w.cleanupUserArtifacts(orgID, u.UserID); err != nil {
			return fmt.Errorf("failed to clean up user %s during org cascade: %w", u.UserID, err)
		}
		if err := w.store.HardDeleteUser(orgID, u.UserID, u.Email); err != nil {
			return fmt.Errorf("failed to hard-delete user %s during org cascade: %w", u.UserID, err)
		}
		if err := lease.Check(); err != nil {
			return err
		}
	}

	groupIDs, err := w.store.ListGroupsByOrg(orgID)
	if err != nil {
		return fmt.Errorf("failed to list groups for org %s: %w", item.ItemID, err)
	}
	for _, gid := range groupIDs {
		if err := lease.Check(); err != nil {
			return err
		}
		if err := w.store.DeleteGroupFull(orgID, gid); err != nil {
			return fmt.Errorf("failed to delete group %s during org cascade: %w", gid, err)
		}
		if err := lease.Check(); err != nil {
			return err
		}
	}
	if err := lease.Check(); err != nil {
		return err
	}

	if err := w.store.HardDeleteOrgLocked(orgID); err != nil {
		return fmt.Errorf("failed to hard-delete org %s: %w", item.ItemID, err)
	}

	w.store.WriteAuditLog(AuditLogEntry{
		OrgID:      orgID,
		Action:     "gc_org_cascade_deleted",
		TargetType: "organization",
		TargetID:   item.ItemID,
		ActorID:    "gc_worker",
		Details:    fmt.Sprintf("name=%s libraries=%d users=%d groups=%d", orgName, len(libs), len(users), len(groupIDs)),
		Timestamp:  time.Now(),
	})

	log.Printf("[GC Worker] Cascade-deleted org %s (%s): %d libraries, %d users, %d groups",
		item.ItemID, orgName, len(libs), len(users), len(groupIDs))
	return nil
}

func fsObjectBlockDecrementTaskID(libraryID uuid.UUID, fsID string, identityAt time.Time, blockIndex int, blockID string) uuid.UUID {
	taskIDStr := fmt.Sprintf("fs_object_block_decrement:%s:%s:%d:%d:%s", libraryID, fsID, identityAt.UnixNano(), blockIndex, blockID)
	return uuid.NewMD5(uuid.NameSpaceOID, []byte(taskIDStr))
}

// removeFSObjectBlockReferences deletes the permanent reference rows held by an
// fs_object (one "fs:<library>:<fs_id>" referrer per block) and returns the blocks
// that are now unreferenced, so the caller can enqueue them for GC. Block IDs are
// resolved to internal SHA-256 IDs first. Idempotent: deleting a missing reference
// is a no-op, so a retried fs_object GC pass is safe (no double-decrement risk —
// the whole class of decrement idempotency bugs disappears with the counter).
func (w *Worker) removeFSObjectBlockReferences(orgID, libraryID uuid.UUID, blockRepresentationID, fsID string, blockIDs []string, beforeMutation func() error) ([]string, error) {
	resolvedBlockIDs, err := w.store.ResolveBlockIDs(orgID, libraryID, blockRepresentationID, blockIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve block IDs for fs_object %s/%s: %w", libraryID, fsID, err)
	}

	referrer := db.BlockReferrerForFSObject(libraryID.String(), fsID)
	seen := make(map[string]struct{}, len(resolvedBlockIDs))
	var zeroRef []string
	for _, blockID := range resolvedBlockIDs {
		if _, dup := seen[blockID]; dup {
			continue
		}
		seen[blockID] = struct{}{}

		if err := beforeMutation(); err != nil {
			return nil, fmt.Errorf("failed to fence block reference cleanup for fs_object %s/%s block %s: %w", libraryID, fsID, blockID, err)
		}
		if err := w.store.RemoveBlockReference(orgID, blockID, referrer); err != nil {
			return nil, fmt.Errorf("failed to remove block reference for fs_object %s/%s block %s: %w", libraryID, fsID, blockID, err)
		}
		hasRefs, err := w.store.BlockHasReferences(orgID, blockID)
		if err != nil {
			return nil, fmt.Errorf("failed to check references for fs_object %s/%s block %s: %w", libraryID, fsID, blockID, err)
		}
		if !hasRefs {
			zeroRef = append(zeroRef, blockID)
		}
	}
	return zeroRef, nil
}

func (w *Worker) enqueueZeroRefBlocks(orgID, libraryID uuid.UUID, blockIDs []string, storageClass string) error {
	var blockBatch []QueueItem
	var candidateProjectionErr error
	for _, blockID := range blockIDs {
		candidateAt, candidateErr := w.store.EnsureBlockGCCandidate(orgID, blockID, storageClass, time.Now())
		if candidateErr != nil && candidateAt.IsZero() {
			return candidateErr
		}
		exists, err := w.store.PendingItemExists(orgID, uuid.Nil, candidateAt, ItemBlock, blockID)
		if err != nil {
			return errors.Join(candidateErr, candidateProjectionErr, err)
		}
		if exists {
			continue
		}
		if candidateErr != nil {
			metrics.GCBlockCandidateDiscoveryDegradedTotal.WithLabelValues("worker").Inc()
			log.Printf("[GC Worker] WARNING: block candidate discovery degraded for org=%s block=%s: %v", orgID, blockID, candidateErr)
			candidateProjectionErr = errors.Join(candidateProjectionErr, fmt.Errorf("ensure block GC candidate projection for block %s: %w", blockID, candidateErr))
		}
		blockBatch = append(blockBatch, QueueItem{
			OrgID:    orgID,
			QueuedAt: candidateAt,
			ItemType: ItemBlock,
			ItemID:   blockID,
			// Blocks are content-addressed and library-independent: processBlock only
			// uses OrgID+ItemID. Enqueue every block under uuid.Nil, matching the
			// uuid.Nil dedup check above and the scanner's orphan-block path. A single
			// producer is self-consistent even with a real libraryID (CompleteItem
			// re-reads it from the same queue row), but gc_pending_items is
			// library-scoped in its key while gc_queue is not: if a second producer
			// (the scanner, or another library sharing this block) enqueues the same
			// block/candidate under a different libraryID, the single gc_queue row keeps
			// only the last writer's library_id column while BOTH producers' pending
			// rows survive — and CompleteItem then deletes only the one matching the
			// surviving queue row, orphaning the other forever. Keying every producer
			// under uuid.Nil collapses them to one pending row. The store-level pending
			// helpers coerce ItemBlock to uuid.Nil as the backstop.
			// See ISSUE-GC-PENDING-ITEM-BLOCK-LIBRARY-SCOPE-01.
			LibraryID:    uuid.Nil,
			StorageClass: storageClass,
			RetryCount:   0,
		})
	}
	if len(blockBatch) == 0 {
		return nil
	}
	if err := w.queue.EnqueueBatch(blockBatch); err != nil {
		return errors.Join(candidateProjectionErr, err)
	}
	return nil
}

// EnqueueLibraryContents enqueues all contents of a deleted library for GC.
// Only enqueues commits and fs_objects — blocks are handled in cascade
// when fs_objects are processed (via decrementFSObjectBlocks).
func (w *Worker) EnqueueLibraryContents(orgID, libraryID uuid.UUID, storageClass string) error {
	return w.enqueueLibraryContentsAt(orgID, libraryID, "", storageClass, w.clock(), LibraryGuardNone)
}

func (w *Worker) enqueueLibraryContentsAt(orgID, libraryID uuid.UUID, blockRepresentationID, storageClass string, identityAt time.Time, libraryGuardMode LibraryGuardMode) error {
	if identityAt.IsZero() {
		identityAt = w.clock()
	}
	resolvedBlockRepresentationID, err := resolveRequiredLibraryBlockRepresentation(w.store, orgID, libraryID, blockRepresentationID, "library contents enqueue")
	if err != nil {
		return err
	}
	blockRepresentationID = resolvedBlockRepresentationID
	requiresLibraryDeletedCheck := libraryGuardMode != LibraryGuardNone

	// Enqueue all commits for this library (batched)
	commits, err := w.store.ListCommitsForLibrary(libraryID)
	if err != nil {
		return fmt.Errorf("failed to list commits for library %s: %w", libraryID, err)
	}
	if len(commits) > 0 {
		batch := make([]QueueItem, 0, len(commits))
		for _, c := range commits {
			exists, err := w.store.PendingItemExists(orgID, libraryID, identityAt, ItemCommit, c.CommitID)
			if err != nil {
				return fmt.Errorf("failed to check commit queue state for library %s: %w", libraryID, err)
			}
			if exists {
				continue
			}
			batch = append(batch, QueueItem{
				OrgID: orgID, QueuedAt: identityAt, IdentityAt: identityAt, RequiresLibraryDeletedCheck: requiresLibraryDeletedCheck, LibraryGuardMode: libraryGuardMode, ItemType: ItemCommit,
				ItemID: c.CommitID, LibraryID: libraryID, BlockRepresentationID: blockRepresentationID,
			})
		}
		if len(batch) > 0 {
			if err := w.queue.EnqueueBatch(batch); err != nil {
				return fmt.Errorf("failed to batch enqueue commits for library %s: %w", libraryID, err)
			}
		}
	}

	// Enqueue all fs_objects (batched; blocks will cascade via processFSObject)
	fsObjects, err := w.store.ListFSObjectsForLibrary(libraryID)
	if err != nil {
		return fmt.Errorf("failed to list fs_objects for library %s: %w", libraryID, err)
	}
	if len(fsObjects) > 0 {
		batch := make([]QueueItem, 0, len(fsObjects))
		for _, obj := range fsObjects {
			exists, err := w.store.PendingItemExists(orgID, libraryID, identityAt, ItemFSObject, obj.FSID)
			if err != nil {
				return fmt.Errorf("failed to check fs_object queue state for library %s: %w", libraryID, err)
			}
			if exists {
				continue
			}
			batch = append(batch, QueueItem{
				OrgID: orgID, QueuedAt: identityAt, IdentityAt: identityAt, RequiresLibraryDeletedCheck: requiresLibraryDeletedCheck, LibraryGuardMode: libraryGuardMode, ItemType: ItemFSObject,
				ItemID: obj.FSID, LibraryID: libraryID, BlockRepresentationID: blockRepresentationID,
			})
		}
		if len(batch) > 0 {
			if err := w.queue.EnqueueBatch(batch); err != nil {
				return fmt.Errorf("failed to batch enqueue fs_objects for library %s: %w", libraryID, err)
			}
		}
	}

	// Clean up library-specific artifacts that don't cascade through fs_objects
	shareCount, linkCount, err := w.enqueueLibraryArtifacts(orgID, libraryID)
	if err != nil {
		return err
	}

	log.Printf("[GC Worker] Enqueued library %s contents for deletion (%d commits, %d fs_objects, %d shares, %d share links)", libraryID, len(commits), len(fsObjects), shareCount, linkCount)
	return nil
}

func (w *Worker) acquireLibraryDeleteGuard(item QueueItem) (func(), func() error, bool, error) {
	guardMode := effectiveLibraryGuardMode(item.LibraryGuardMode, item.RequiresLibraryDeletedCheck)
	if guardMode == LibraryGuardNone {
		return func() {}, func() error { return nil }, false, nil
	}
	identityAt := effectiveIdentityAt(item.QueuedAt, item.IdentityAt)
	if guardMode == LibraryGuardDeletedAtIdentity {
		deletedAt, err := w.store.GetLibraryDeletedAt(item.LibraryID)
		if err != nil {
			return nil, nil, false, fmt.Errorf("failed to read deleted library marker for %s/%s: %w", item.LibraryID, item.ItemID, err)
		}
		if deletedAt == nil {
			exists, err := w.store.CanonicalLibraryExists(item.OrgID, item.LibraryID)
			if err != nil {
				return nil, nil, false, fmt.Errorf("failed to confirm canonical library absence for completed cascade %s/%s: %w", item.LibraryID, item.ItemID, err)
			}
			if exists {
				log.Printf("[GC Worker] Skipping stale guarded item %s/%s: delete marker is gone but canonical library exists", item.LibraryID, item.ItemID)
				return func() {}, func() error { return nil }, true, nil
			}
			// The parent cascade already hard-deleted both the canonical row and its
			// marker. Its children must keep draining; the row can no longer be
			// restored through the guarded restore path.
			return func() {}, func() error { return nil }, false, nil
		}
		if !deletedAt.Equal(identityAt) {
			log.Printf("[GC Worker] Skipping stale guarded item %s/%s (current deleted_at=%v identity_at=%v)", item.LibraryID, item.ItemID, deletedAt, identityAt)
			return func() {}, func() error { return nil }, true, nil
		}
	} else if guardMode != LibraryGuardCanonicalMustBeAbsent {
		return nil, nil, false, fmt.Errorf("unknown library guard mode %q for %s/%s", guardMode, item.LibraryID, item.ItemID)
	}

	leaseToken := uuid.New()
	acquired, err := w.store.AcquireLibraryHardDeleteLock(item.LibraryID, leaseToken)
	if err != nil {
		return nil, nil, false, fmt.Errorf("failed to acquire library hard-delete lock for child %s/%s: %w", item.LibraryID, item.ItemID, err)
	}
	if !acquired {
		return nil, nil, false, libraryHardDeleteInProgressError{LibraryID: item.LibraryID, ItemID: item.ItemID}
	}

	release := func() {
		_ = w.store.ReleaseLibraryHardDeleteLock(item.LibraryID, leaseToken)
	}

	if guardMode == LibraryGuardCanonicalMustBeAbsent {
		exists, err := w.store.CanonicalLibraryExists(item.OrgID, item.LibraryID)
		if err != nil {
			release()
			return nil, nil, false, fmt.Errorf("failed to confirm canonical library absence for %s/%s: %w", item.LibraryID, item.ItemID, err)
		}
		if exists {
			release()
			log.Printf("[GC Worker] Skipping orphan item %s/%s: canonical library exists", item.LibraryID, item.ItemID)
			return func() {}, func() error { return nil }, true, nil
		}
	} else {
		deletedAt, err := w.store.GetLibraryDeletedAt(item.LibraryID)
		if err != nil {
			release()
			return nil, nil, false, fmt.Errorf("failed to re-read deleted library marker for child %s/%s: %w", item.LibraryID, item.ItemID, err)
		}
		if deletedAt == nil || !deletedAt.Equal(identityAt) {
			release()
			log.Printf("[GC Worker] Skipping stale guarded item %s/%s after lock (current deleted_at=%v identity_at=%v)", item.LibraryID, item.ItemID, deletedAt, identityAt)
			return func() {}, func() error { return nil }, true, nil
		}
		// The delete marker still matches, but a matching marker does NOT prove the parent
		// cascade finished HardDeleteLibrary. HardDeleteLibrary removes the canonical
		// `libraries` row and the marker together; while the canonical row still exists the
		// library is soft-deleted and RESTORABLE. If a child reaches here with the canonical
		// row present, the parent crashed after enqueuing children but before the canonical
		// delete (and this worker stole its stale lease). Purging content now would let a
		// later restore revive a partially-purged library. Require the canonical row to be
		// gone; otherwise postpone (no retry burn, no DLQ) until the cascade is re-driven and
		// completes the hard delete. In the normal flow children run only after
		// HardDeleteLibrary, so the marker is already gone and this branch is not reached.
		exists, err := w.store.CanonicalLibraryExists(item.OrgID, item.LibraryID)
		if err != nil {
			release()
			return nil, nil, false, fmt.Errorf("failed to confirm canonical library absence for guarded child %s/%s: %w", item.LibraryID, item.ItemID, err)
		}
		if exists {
			release()
			log.Printf("[GC Worker] Postponing guarded item %s/%s: canonical library still present (cascade not yet hard-deleted)", item.LibraryID, item.ItemID)
			return nil, nil, false, libraryHardDeleteInProgressError{LibraryID: item.LibraryID, ItemID: item.ItemID}
		}
	}

	fence := func() error {
		owned, err := w.store.RenewLibraryHardDeleteLock(item.LibraryID, leaseToken)
		if err != nil {
			return fmt.Errorf("failed to fence library delete for %s/%s: %w", item.LibraryID, item.ItemID, err)
		}
		if !owned {
			return fmt.Errorf("lost library delete lock for %s/%s", item.LibraryID, item.ItemID)
		}
		return nil
	}
	return release, fence, false, nil
}

// enqueueLibraryArtifacts cleans up ALL auxiliary data tied to a deleted library:
// share links, shares, tags, tag counters, api tokens, locked files,
// starred files, monitored repos, and restore jobs.
func (w *Worker) enqueueLibraryArtifacts(orgID, libraryID uuid.UUID) (int, int, error) {
	var cleanupErr error
	joinErr := func(label string, err error) {
		if err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("%s: %w", label, err))
		}
	}

	// Delete share links via the by_library index (efficient)
	tokens, err := w.store.DeleteShareLinksByLibrary(orgID, libraryID)
	joinErr("delete share links", err)
	if err == nil && len(tokens) > 0 {
		log.Printf("[GC Worker] Cleaned up %d share links for deleted library %s", len(tokens), libraryID)
	}

	// Delete shares (user-to-user and group shares)
	shares, err := w.store.ListSharesByLibrary(libraryID)
	joinErr("list shares", err)
	if err == nil {
		for _, share := range shares {
			joinErr("delete share", w.store.DeleteShare(libraryID, share.ShareID))
		}
		if len(shares) > 0 {
			log.Printf("[GC Worker] Cleaned up %d shares for deleted library %s", len(shares), libraryID)
		}
	}

	// Delete repo tags and file tags
	joinErr("cleanup tags", w.cleanupLibraryTags(libraryID))

	// Delete tag counter tables (repo_tag_counters, file_tag_counters, repo_tag_file_counts)
	joinErr("delete repo tag counters", w.store.DeleteRepoTagCounters(libraryID))
	joinErr("delete file tag counters", w.store.DeleteFileTagCounters(libraryID))
	joinErr("delete repo tag file counts", w.store.DeleteRepoTagFileCounts(libraryID))

	// Delete API tokens
	tokens2, err := w.store.ListRepoAPITokensByLibrary(libraryID)
	joinErr("list repo api tokens", err)
	if err == nil {
		for _, t := range tokens2 {
			joinErr("delete repo api token", w.store.DeleteRepoAPIToken(libraryID, t.AppName))
			joinErr("delete repo api token by token", w.store.DeleteRepoAPITokenByToken(t.APIToken))
		}
	}

	// Delete locked files
	joinErr("delete locked files", w.store.DeleteLockedFilesByLibrary(libraryID))

	// Delete starred files referencing this library
	joinErr("delete starred files", w.store.DeleteStarredFilesByLibrary(libraryID))

	// Delete monitored repos referencing this library
	joinErr("delete monitored repos", w.store.DeleteMonitoredReposByLibrary(libraryID))

	// Delete restore jobs for this library
	joinErr("delete restore jobs", w.store.DeleteRestoreJobsByLibrary(orgID, libraryID))

	if cleanupErr != nil {
		return len(shares), len(tokens), fmt.Errorf("failed to clean auxiliary artifacts for library %s: %w", libraryID, cleanupErr)
	}

	// Audit log
	w.store.WriteAuditLog(AuditLogEntry{
		OrgID:      orgID,
		Action:     "gc_library_artifacts_cleaned",
		TargetType: "library",
		TargetID:   libraryID.String(),
		ActorID:    "gc_worker",
		Details:    fmt.Sprintf("shares=%d links=%d", len(shares), len(tokens)),
		Timestamp:  time.Now(),
	})

	return len(shares), len(tokens), nil
}

func (w *Worker) cleanupLibraryTags(libraryID uuid.UUID) error {
	var cleanupErr error
	joinErr := func(label string, err error) {
		if err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("%s: %w", label, err))
		}
	}

	// Delete file tags first (they reference repo tags)
	fileTags, err := w.store.ListFileTagsByLibrary(libraryID)
	if err != nil {
		return err
	}
	for _, ft := range fileTags {
		joinErr("delete file tag", w.store.DeleteFileTag(libraryID, ft.FilePath, ft.TagID))
		joinErr("delete file tag by id", w.store.DeleteFileTagByID(libraryID, ft.FileTagID))
	}

	// Delete repo tag definitions
	tagIDs, err := w.store.ListRepoTagsByLibrary(libraryID)
	if err != nil {
		return err
	}
	for _, tagIDStr := range tagIDs {
		var tagID int
		if _, err := fmt.Sscanf(tagIDStr, "%d", &tagID); err == nil {
			joinErr("delete repo tag", w.store.DeleteRepoTag(libraryID, tagID))
		}
	}

	return cleanupErr
}
