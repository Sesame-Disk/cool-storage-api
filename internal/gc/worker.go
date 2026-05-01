package gc

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/metrics"
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

func (e libraryHardDeleteInProgressError) Error() string {
	return fmt.Sprintf("library %s hard delete already in progress for child %s", e.LibraryID, e.ItemID)
}

func (e libraryHardDeleteInProgressError) FailureCode() string {
	return GCFailureCodeLibraryHardDeleteInProgress
}

func failureCodeForError(err error) string {
	var coded gcFailureCoder
	if errors.As(err, &coded) {
		return coded.FailureCode()
	}
	return GCFailureCodeNone
}

// s3DeleteRetryDelays is the backoff schedule used when S3 DeleteBlock fails.
// Total in-worker wait budget: 100 + 500 + 2000 = 2.6s across 3 retries.
// Exposed as a var so tests can shorten it.
var s3DeleteRetryDelays = []time.Duration{
	100 * time.Millisecond,
	500 * time.Millisecond,
	2 * time.Second,
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

		// Remove from queue
		if err := w.queue.Complete(item.OrgID, item.QueuedAt, item.ItemType, item.ItemID); err != nil {
			log.Printf("[GC Worker] Failed to complete item %s/%s: %v",
				item.OrgID, item.ItemID, err)
		}

		metrics.GCItemsProcessedTotal.WithLabelValues(string(item.ItemType)).Inc()
		processed++
	}

	if len(items) < w.batchSize {
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

func (w *Worker) processItem(ctx context.Context, item QueueItem) error {
	switch item.ItemType {
	case ItemBlock:
		return w.processBlock(ctx, item)
	case ItemCommit:
		return w.processCommit(item)
	case ItemFSObject:
		return w.processFSObject(ctx, item)
	case ItemBlockMapping:
		return w.processBlockMapping(ctx, item)
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

	// 1. Claim the block row using LWT (IF ref_count <= 0). We defer the
	// physical DELETE until after we've persisted S3-recovery state.
	applied, err := w.store.ClaimBlockDelete(item.OrgID, item.ItemID)
	if err != nil {
		return fmt.Errorf("failed to claim block record for deletion: %w", err)
	}

	// 2. If it didn't apply, it means ref_count > 0 or it was already deleted.
	// We skip deleting from S3 to avoid data loss.
	if !applied {
		if err := w.store.DeleteBlockGCCandidate(item.OrgID, item.ItemID); err != nil {
			return fmt.Errorf("failed to clear stale block GC candidate: %w", err)
		}
		log.Printf("[GC Worker] Block %s LWT delete not applied (ref_count > 0 or already deleted), skipping S3 deletion", item.ItemID)
		metrics.GCItemsSkippedTotal.Inc()
		return nil
	}

	// 3. Persist the S3-pending record BEFORE removing the DB row. This closes the
	// crash window where the process dies after deleting the canonical row but
	// before recording recovery metadata for the later S3 delete.
	storageClass := item.StorageClass
	if storageClass == "" {
		storageClass = "hot"
	}
	if err := w.store.RecordS3Orphan(item.OrgID, item.ItemID, storageClass, "", w.clock()); err != nil {
		return fmt.Errorf("failed to record pending S3 delete for block %s: %w", item.ItemID, err)
	}

	// 4. Now remove the claimed DB row. If this fails, the row stays at -999 and
	// the queue item will retry; the pending S3 row already preserves recovery state.
	if err := w.store.FinalizeBlockDelete(item.OrgID, item.ItemID); err != nil {
		return fmt.Errorf("failed to finalize claimed block delete for %s: %w", item.ItemID, err)
	}

	if w.storage != nil {
		blockStore, err := w.storage.GetBlockStore(storageClass)
		if err != nil {
			return fmt.Errorf("failed to get block store for class %s: %w", storageClass, err)
		}
		if delErr := w.deleteS3WithRetry(ctx, blockStore, item.ItemID); delErr != nil {
			log.Printf("[GC Worker] WARNING: Failed to delete block %s from S3 after DB deletion: %v (recording for scanner recovery)", item.ItemID, delErr)
			if recErr := w.store.UpdateS3OrphanAttempt(item.OrgID, item.ItemID, delErr.Error(), w.clock()); recErr != nil {
				log.Printf("[GC Worker] ERROR: Failed to update S3 orphan %s: %v", item.ItemID, recErr)
				metrics.GCErrorsTotal.WithLabelValues("s3_orphan_record").Inc()
			}
			metrics.GCAuditEventsTotal.WithLabelValues("gc_block_s3_orphaned").Inc()
			// Do NOT return error — the block is recorded for recovery.
			// Continue to mapping/candidate cleanup so the queue item completes.
		} else if err := w.store.DeleteS3Orphan(item.OrgID, item.ItemID); err != nil {
			log.Printf("[GC Worker] WARNING: S3 delete for block %s succeeded but failed to clear recovery row: %v", item.ItemID, err)
		}
	}

	// 5. Clean up related mappings
	mappings, err := w.store.ListBlockMappingsByInternalID(item.OrgID, item.ItemID)
	if err == nil {
		for _, mapping := range mappings {
			w.store.DeleteBlockMapping(item.OrgID, mapping.ExternalID)
		}
	}

	if err := w.store.DeleteBlockGCCandidate(item.OrgID, item.ItemID); err != nil {
		return fmt.Errorf("failed to clear block GC candidate: %w", err)
	}

	w.stats.IncrBlocksDeleted()
	metrics.GCAuditEventsTotal.WithLabelValues("gc_block_deleted").Inc()
	log.Printf("[GC Worker] Deleted block %s", item.ItemID)
	return nil
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
func (w *Worker) RecoverS3Orphans(ctx context.Context, perOrgLimit int) (int, error) {
	if w.storage == nil {
		return 0, nil
	}
	if w.dryRun {
		log.Println("[GC Worker] DRY RUN: skipping S3 orphan recovery")
		return 0, nil
	}
	orgs, err := w.store.ListS3OrphanOrgs()
	if err != nil {
		return 0, fmt.Errorf("failed to list S3 orphan orgs: %w", err)
	}
	recovered := 0
	for _, orgID := range orgs {
		select {
		case <-ctx.Done():
			return recovered, ctx.Err()
		default:
		}
		orphans, err := w.store.ListS3Orphans(orgID, perOrgLimit)
		if err != nil {
			log.Printf("[GC Worker] Failed to list S3 orphans for org %s: %v", orgID, err)
			continue
		}
		for _, orph := range orphans {
			select {
			case <-ctx.Done():
				return recovered, ctx.Err()
			default:
			}
			if _, err := w.store.GetBlockRefCount(orph.OrgID, orph.BlockID); err == nil {
				// The canonical block row still exists (likely claimed but not yet finalized).
				// Skip recovery for now; a later worker retry or startup scan will finish it.
				continue
			}

			storageClass := orph.StorageClass
			if storageClass == "" {
				storageClass = "hot"
			}
			blockStore, err := w.storage.GetBlockStore(storageClass)
			if err != nil {
				log.Printf("[GC Worker] S3 orphan recovery: get block store for class %s failed: %v", storageClass, err)
				continue
			}
			if err := blockStore.DeleteBlock(ctx, orph.BlockID); err != nil {
				if updErr := w.store.UpdateS3OrphanAttempt(orph.OrgID, orph.BlockID, err.Error(), w.clock()); updErr != nil {
					log.Printf("[GC Worker] S3 orphan recovery: update attempt for %s failed: %v", orph.BlockID, updErr)
				}
				continue
			}
			if err := w.store.DeleteS3Orphan(orph.OrgID, orph.BlockID); err != nil {
				log.Printf("[GC Worker] S3 orphan recovery: failed to clear orphan row %s: %v", orph.BlockID, err)
				continue
			}
			recovered++
			metrics.GCAuditEventsTotal.WithLabelValues("gc_s3_orphan_recovered").Inc()
			log.Printf("[GC Worker] Recovered S3 orphan %s (org=%s, retries=%d)", orph.BlockID, orph.OrgID, orph.RetryCount)
		}
	}
	return recovered, nil
}

func (w *Worker) processCommit(item QueueItem) error {
	identityAt := effectiveIdentityAt(item.QueuedAt, item.IdentityAt)
	releaseGuard, stale, err := w.acquireLibraryDeleteGuard(item)
	if err != nil {
		return err
	}
	if stale {
		return nil
	}
	defer releaseGuard()

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
				ItemType:                    ItemFSObject,
				ItemID:                      commit.RootFSID,
				LibraryID:                   item.LibraryID,
			}
			if err := w.queue.EnqueueBatch([]QueueItem{child}); err != nil {
				return fmt.Errorf("failed to enqueue root fs_object %s for commit %s: %w", commit.RootFSID, item.ItemID, err)
			}
		}
	}

	if err := w.store.DeleteCommit(item.LibraryID, item.ItemID); err != nil {
		return fmt.Errorf("failed to delete commit: %w", err)
	}

	log.Printf("[GC Worker] Deleted commit %s", item.ItemID)
	return nil
}

func (w *Worker) processFSObject(ctx context.Context, item QueueItem) error {
	identityAt := effectiveIdentityAt(item.QueuedAt, item.IdentityAt)
	releaseGuard, stale, err := w.acquireLibraryDeleteGuard(item)
	if err != nil {
		return err
	}
	if stale {
		return nil
	}
	defer releaseGuard()

	// Get the fs_object to find its block_ids
	fsObj, err := w.store.GetFSObject(item.LibraryID, item.ItemID)
	if err != nil {
		// Already deleted
		log.Printf("[GC Worker] FS object %s not found (may already be deleted)", item.ItemID)
		return nil
	}

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
				ItemType:                    ItemFSObject,
				ItemID:                      childID,
				LibraryID:                   item.LibraryID,
				StorageClass:                "",
				RetryCount:                  0,
			})
		}
		if err := w.queue.EnqueueBatch(batch); err != nil {
			log.Printf("[GC Worker] Failed to batch enqueue children for %s: %v", item.ItemID, err)
			return err
		}
	}

	// If it's a file with blocks, decrement ref counts
	if len(fsObj.BlockIDs) > 0 {
		// Create a deterministic task ID for this specific decrement operation
		// based on the fs_object and its stable semantic identity so retries or
		// duplicate queue rows for the same item do not double-decrement blocks.
		taskIDStr := fmt.Sprintf("%s-%s-%d", item.LibraryID, item.ItemID, identityAt.UnixNano())
		taskID := uuid.NewMD5(uuid.NameSpaceOID, []byte(taskIDStr))

		applied, err := w.store.MarkItemProcessed(taskID)
		if err != nil {
			return fmt.Errorf("failed to check idempotency for fs_object %s: %w", item.ItemID, err)
		}

		if applied {
			// First time processing this exact task, safe to decrement
			zeroRefBlocks := w.decrementAndFindZeroRef(item.OrgID, fsObj.BlockIDs)
			storageClass, _ := w.store.GetLibraryStorageClass(item.OrgID, item.LibraryID)
			if len(zeroRefBlocks) > 0 {
				if err := w.enqueueZeroRefBlocks(item.OrgID, item.LibraryID, zeroRefBlocks, storageClass); err != nil {
					return fmt.Errorf("failed to enqueue zero-ref blocks for fs_object %s: %w", item.ItemID, err)
				}
			}
		} else {
			log.Printf("[GC Worker] Skipping decrement for %s (already processed task %s)", item.ItemID, taskID)
		}
	}

	if w.dryRun {
		log.Printf("[GC Worker] DRY RUN: Would delete fs_object %s from library %s", item.ItemID, item.LibraryID)
		return nil
	}

	// Delete the fs_object
	if err := w.store.DeleteFSObject(item.LibraryID, item.ItemID); err != nil {
		return fmt.Errorf("failed to delete fs_object: %w", err)
	}

	log.Printf("[GC Worker] Deleted fs_object %s", item.ItemID)
	return nil
}

func (w *Worker) processBlockMapping(ctx context.Context, item QueueItem) error {
	if w.dryRun {
		log.Printf("[GC Worker] DRY RUN: Would delete block mapping %s", item.ItemID)
		return nil
	}

	if err := w.store.DeleteBlockMapping(item.OrgID, item.ItemID); err != nil {
		return fmt.Errorf("failed to delete block mapping: %w", err)
	}

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
	acquired, err := w.store.AcquireUserHardDeleteLock(userID)
	if err != nil {
		return fmt.Errorf("failed to acquire user hard-delete lock for %s: %w", item.ItemID, err)
	}
	if !acquired {
		return fmt.Errorf("user %s hard delete already in progress", item.ItemID)
	}
	defer w.store.ReleaseUserHardDeleteLock(userID)

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

	// Get user email before deletion (needed for users_by_email cleanup)
	email, err := w.store.GetUserEmail(item.OrgID, userID)
	if err != nil {
		return fmt.Errorf("failed to read user email for %s: %w", item.ItemID, err)
	}

	libCount, err := w.softDeleteUserLibraries(item.OrgID, userID)
	if err != nil {
		return fmt.Errorf("failed to soft-delete libraries owned by user %s: %w", item.ItemID, err)
	}

	groupCount, shareCount, err := w.cleanupUserArtifacts(item.OrgID, userID)
	if err != nil {
		return fmt.Errorf("failed to clean up artifacts for user %s: %w", item.ItemID, err)
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
		log.Printf("[GC Worker] Skipping stale library cascade for %s (current deleted_at=%v identity_at=%v queued_at=%v)", item.ItemID, deletedAt, identityAt, item.QueuedAt)
		return nil
	}

	acquired, err := w.store.AcquireLibraryHardDeleteLock(libraryID)
	if err != nil {
		return fmt.Errorf("failed to acquire library hard-delete lock for %s: %w", item.ItemID, err)
	}
	if !acquired {
		return fmt.Errorf("library %s hard delete already in progress", item.ItemID)
	}
	defer w.store.ReleaseLibraryHardDeleteLock(libraryID) //nolint:errcheck

	// Second stale-check after acquiring the lock.
	deletedAt2, err := w.store.GetLibraryDeletedAt(libraryID)
	if err != nil {
		return fmt.Errorf("failed to re-read deleted library marker for %s: %w", item.ItemID, err)
	}
	if deletedAt2 == nil || !deletedAt2.Equal(identityAt) {
		log.Printf("[GC Worker] Skipping stale library cascade for %s after lock (current deleted_at=%v identity_at=%v queued_at=%v)", item.ItemID, deletedAt2, identityAt, item.QueuedAt)
		return nil
	}

	return w.cascadeDeleteLibrary(item.OrgID, libraryID, item.StorageClass, identityAt)
}

func (w *Worker) cascadeDeleteLibrary(orgID, libraryID uuid.UUID, storageClass string, libraryDeletedAt time.Time) error {
	if storageClass == "" {
		storageClass, _ = w.store.GetLibraryStorageClass(orgID, libraryID)
	}

	if err := w.enqueueLibraryContentsAt(orgID, libraryID, storageClass, libraryDeletedAt, true); err != nil {
		return fmt.Errorf("failed to enqueue library contents: %w", err)
	}

	if err := w.store.DeleteLibraryStorageCounter(orgID, libraryID); err != nil {
		return fmt.Errorf("failed to delete library storage counter for %s: %w", libraryID, err)
	}

	if err := w.store.HardDeleteLibrary(orgID, libraryID); err != nil {
		return fmt.Errorf("failed to hard-delete library %s: %w", libraryID, err)
	}

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

	acquired, err := w.store.AcquireOrgHardDeleteLock(orgID)
	if err != nil {
		return fmt.Errorf("failed to acquire org hard-delete lock for %s: %w", item.ItemID, err)
	}
	if !acquired {
		return fmt.Errorf("org %s hard delete already in progress", item.ItemID)
	}
	defer w.store.ReleaseOrgHardDeleteLock(orgID)

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
		libraryDeletedAt := lib.DeletedAt
		if lib.DeletedAt.IsZero() {
			if err := w.store.SoftDeleteLibrary(orgID, lib.LibraryID, uuid.Nil); err != nil {
				return fmt.Errorf("failed to soft-delete library %s during org cascade: %w", lib.LibraryID, err)
			}
			deletedLibraryAt, err := w.store.GetLibraryDeletedAt(lib.LibraryID)
			if err != nil {
				return fmt.Errorf("failed to read deleted library marker for %s during org cascade: %w", lib.LibraryID, err)
			}
			if deletedLibraryAt == nil {
				return fmt.Errorf("missing deleted library marker for %s during org cascade", lib.LibraryID)
			}
			libraryDeletedAt = *deletedLibraryAt
		}
		if err := w.cascadeDeleteLibrary(orgID, lib.LibraryID, lib.StorageClass, libraryDeletedAt); err != nil {
			return fmt.Errorf("failed to cascade-delete library %s during org delete: %w", lib.LibraryID, err)
		}
	}

	users, err := w.store.ListUsersByOrg(orgID)
	if err != nil {
		return fmt.Errorf("failed to list users for org %s: %w", item.ItemID, err)
	}
	for _, u := range users {
		if _, _, err := w.cleanupUserArtifacts(orgID, u.UserID); err != nil {
			return fmt.Errorf("failed to clean up user %s during org cascade: %w", u.UserID, err)
		}
		if err := w.store.HardDeleteUser(orgID, u.UserID, u.Email); err != nil {
			return fmt.Errorf("failed to hard-delete user %s during org cascade: %w", u.UserID, err)
		}
	}

	groupIDs, err := w.store.ListGroupsByOrg(orgID)
	if err != nil {
		return fmt.Errorf("failed to list groups for org %s: %w", item.ItemID, err)
	}
	for _, gid := range groupIDs {
		if err := w.store.DeleteGroupFull(orgID, gid); err != nil {
			return fmt.Errorf("failed to delete group %s during org cascade: %w", gid, err)
		}
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

// decrementAndFindZeroRef decrements ref_count for blocks and returns those that hit 0.
func (w *Worker) decrementAndFindZeroRef(orgID uuid.UUID, blockIDs []string) []string {
	resolvedBlockIDs, err := w.store.ResolveBlockIDs(orgID, blockIDs)
	if err != nil {
		log.Printf("[GC Worker] Failed to resolve block IDs for org %s: %v", orgID, err)
		return nil
	}

	var zeroRef []string
	for _, blockID := range resolvedBlockIDs {
		hitZero, err := w.store.DecrementBlockRefCount(orgID, blockID)
		if err != nil {
			continue
		}
		if hitZero {
			zeroRef = append(zeroRef, blockID)
		}
	}
	return zeroRef
}

func (w *Worker) enqueueZeroRefBlocks(orgID, libraryID uuid.UUID, blockIDs []string, storageClass string) error {
	var blockBatch []QueueItem
	for _, blockID := range blockIDs {
		candidateAt, err := w.store.EnsureBlockGCCandidate(orgID, blockID, storageClass, time.Now())
		if err != nil {
			return err
		}
		exists, err := w.store.PendingItemExists(orgID, uuid.Nil, candidateAt, ItemBlock, blockID)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		blockBatch = append(blockBatch, QueueItem{
			OrgID:        orgID,
			QueuedAt:     candidateAt,
			ItemType:     ItemBlock,
			ItemID:       blockID,
			LibraryID:    libraryID,
			StorageClass: storageClass,
			RetryCount:   0,
		})
	}
	if len(blockBatch) == 0 {
		return nil
	}
	return w.queue.EnqueueBatch(blockBatch)
}

// EnqueueLibraryContents enqueues all contents of a deleted library for GC.
// Only enqueues commits and fs_objects — blocks are handled in cascade
// when fs_objects are processed (via decrementAndFindZeroRef).
func (w *Worker) EnqueueLibraryContents(orgID, libraryID uuid.UUID, storageClass string) error {
	return w.enqueueLibraryContentsAt(orgID, libraryID, storageClass, w.clock(), false)
}

func (w *Worker) enqueueLibraryContentsAt(orgID, libraryID uuid.UUID, storageClass string, identityAt time.Time, requiresLibraryDeletedCheck bool) error {
	if identityAt.IsZero() {
		identityAt = w.clock()
	}

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
				OrgID: orgID, QueuedAt: identityAt, IdentityAt: identityAt, RequiresLibraryDeletedCheck: requiresLibraryDeletedCheck, ItemType: ItemCommit,
				ItemID: c.CommitID, LibraryID: libraryID,
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
				OrgID: orgID, QueuedAt: identityAt, IdentityAt: identityAt, RequiresLibraryDeletedCheck: requiresLibraryDeletedCheck, ItemType: ItemFSObject,
				ItemID: obj.FSID, LibraryID: libraryID,
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

func (w *Worker) acquireLibraryDeleteGuard(item QueueItem) (func(), bool, error) {
	if !item.RequiresLibraryDeletedCheck {
		return func() {}, false, nil
	}
	identityAt := effectiveIdentityAt(item.QueuedAt, item.IdentityAt)
	libraryMissing := false
	isStale := func(deletedAt *time.Time) (bool, error) {
		if deletedAt == nil {
			exists, err := w.store.LibraryExists(item.LibraryID)
			if err != nil {
				return false, fmt.Errorf("failed to confirm library existence for child %s/%s: %w", item.LibraryID, item.ItemID, err)
			}
			libraryMissing = !exists
			return exists, nil
		}
		libraryMissing = false
		return !deletedAt.Equal(identityAt), nil
	}

	deletedAt, err := w.store.GetLibraryDeletedAt(item.LibraryID)
	if err != nil {
		return nil, false, fmt.Errorf("failed to read deleted library marker for %s/%s: %w", item.LibraryID, item.ItemID, err)
	}
	stale, err := isStale(deletedAt)
	if err != nil {
		return nil, false, err
	}
	if stale {
		log.Printf("[GC Worker] Skipping stale guarded item %s/%s (current deleted_at=%v identity_at=%v)", item.LibraryID, item.ItemID, deletedAt, identityAt)
		return func() {}, true, nil
	}
	if libraryMissing {
		// The library has already been hard-deleted, so any remaining child items
		// should continue draining even if a short-lived lock row lingers until TTL.
		return func() {}, false, nil
	}

	acquired, err := w.store.AcquireLibraryHardDeleteLock(item.LibraryID)
	if err != nil {
		return nil, false, fmt.Errorf("failed to acquire library hard-delete lock for child %s/%s: %w", item.LibraryID, item.ItemID, err)
	}
	if !acquired {
		return nil, false, libraryHardDeleteInProgressError{LibraryID: item.LibraryID, ItemID: item.ItemID}
	}

	release := func() {
		_ = w.store.ReleaseLibraryHardDeleteLock(item.LibraryID)
	}

	deletedAt2, err := w.store.GetLibraryDeletedAt(item.LibraryID)
	if err != nil {
		release()
		return nil, false, fmt.Errorf("failed to re-read deleted library marker for child %s/%s: %w", item.LibraryID, item.ItemID, err)
	}
	stale, err = isStale(deletedAt2)
	if err != nil {
		release()
		return nil, false, err
	}
	if stale {
		release()
		log.Printf("[GC Worker] Skipping stale guarded item %s/%s after lock (current deleted_at=%v identity_at=%v)", item.LibraryID, item.ItemID, deletedAt2, identityAt)
		return func() {}, true, nil
	}

	return release, false, nil
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
