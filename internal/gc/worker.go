package gc

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	"github.com/google/uuid"
)

// Worker drains the gc_queue and deletes items from S3 and the database.
type Worker struct {
	store       GCStore
	storage     StorageProvider
	queue       *Queue
	batchSize   int
	gracePeriod time.Duration
	dryRun      bool
	stats       *Stats
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

			// Increment retry count; if too many retries, let TTL clean it up
			if item.RetryCount < 5 {
				w.queue.IncrementRetry(item.OrgID, item.QueuedAt, item.ItemType, item.ItemID, item.RetryCount)
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

	return processed, nil
}

func (w *Worker) processItem(ctx context.Context, item QueueItem) error {
	switch item.ItemType {
	case ItemBlock:
		return w.processBlock(ctx, item)
	case ItemCommit:
		return w.processCommit(ctx, item)
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
	default:
		return fmt.Errorf("unknown item type: %s", item.ItemType)
	}
}

func (w *Worker) processBlock(ctx context.Context, item QueueItem) error {
	if w.dryRun {
		log.Printf("[GC Worker] DRY RUN: Would conditionally delete block %s from DB and S3", item.ItemID)
		return nil
	}

	// 1. Delete block record from DB using LWT (IF ref_count <= 0)
	applied, err := w.store.DeleteBlock(item.OrgID, item.ItemID)
	if err != nil {
		return fmt.Errorf("failed to execute LWT delete for block record: %w", err)
	}

	// 2. If it didn't apply, it means ref_count > 0 or it was already deleted.
	// We skip deleting from S3 to avoid data loss.
	if !applied {
		log.Printf("[GC Worker] Block %s LWT delete not applied (ref_count > 0 or already deleted), skipping S3 deletion", item.ItemID)
		metrics.GCItemsSkippedTotal.Inc()
		return nil
	}

	// 3. Since DB delete succeeded (ref_count was 0), now safely delete from S3
	storageClass := item.StorageClass
	if storageClass == "" {
		storageClass = "hot"
	}

	if w.storage != nil {
		blockStore, err := w.storage.GetBlockStore(storageClass)
		if err != nil {
			return fmt.Errorf("failed to get block store for class %s: %w", storageClass, err)
		}
		if err := blockStore.DeleteBlock(ctx, item.ItemID); err != nil {
			// S3 deletion failed, but DB record is gone.
			// This leaves an orphan in S3, which is safer than deleting live data.
			log.Printf("[GC Worker] WARNING: Failed to delete block %s from S3 after DB deletion: %v", item.ItemID, err)
			return fmt.Errorf("failed to delete block from S3: %w", err)
		}
	}

	// 4. Clean up related mappings
	mappings, err := w.store.ListBlockMappingsByInternalID(item.OrgID, item.ItemID)
	if err == nil {
		for _, mapping := range mappings {
			w.store.DeleteBlockMapping(item.OrgID, mapping.ExternalID)
		}
	}

	w.stats.IncrBlocksDeleted()
	metrics.GCAuditEventsTotal.WithLabelValues("gc_block_deleted").Inc()
	log.Printf("[GC Worker] Deleted block %s", item.ItemID)
	return nil
}

func (w *Worker) processCommit(ctx context.Context, item QueueItem) error {
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

	// Enqueue the root fs_object for cascading deletion (fs_object → blocks)
	if commit.RootFSID != "" {
		w.queue.Enqueue(item.OrgID, ItemFSObject, commit.RootFSID, item.LibraryID, "")
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

	// If it's a directory, enqueue child fs_objects for recursive deletion
	if len(fsObj.DirEntries) > 0 {
		var batch []QueueItem
		now := time.Now()
		for _, childID := range fsObj.DirEntries {
			batch = append(batch, QueueItem{
				OrgID:        item.OrgID,
				QueuedAt:     now,
				ItemType:     ItemFSObject,
				ItemID:       childID,
				LibraryID:    item.LibraryID,
				StorageClass: "",
				RetryCount:   0,
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
		// based on the fs_object and the specific queue item timestamp to ensure 
		// retries of this exact queue item don't double-decrement.
		taskIDStr := fmt.Sprintf("%s-%d", item.ItemID, item.QueuedAt.UnixNano())
		taskID := uuid.NewMD5(uuid.NameSpaceOID, []byte(taskIDStr))

		applied, err := w.store.MarkItemProcessed(taskID)
		if err != nil {
			return fmt.Errorf("failed to check idempotency for fs_object %s: %w", item.ItemID, err)
		}

		if applied {
			// First time processing this exact task, safe to decrement
			zeroRefBlocks := w.decrementAndFindZeroRef(item.OrgID, fsObj.BlockIDs)
			storageClass, _ := w.store.GetLibraryStorageClass(item.OrgID, item.LibraryID)

			var blockBatch []QueueItem
			now := time.Now()
			for _, blockID := range zeroRefBlocks {
				blockBatch = append(blockBatch, QueueItem{
					OrgID:        item.OrgID,
					QueuedAt:     now,
					ItemType:     ItemBlock,
					ItemID:       blockID,
					LibraryID:    item.LibraryID,
					StorageClass: storageClass,
					RetryCount:   0,
				})
			}
			if len(blockBatch) > 0 {
				w.queue.EnqueueBatch(blockBatch)
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
// 3. Clean up shares received by this user
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

	// Get user email before deletion (needed for users_by_email cleanup)
	email, err := w.store.GetUserEmail(item.OrgID, userID)
	if err != nil {
		log.Printf("[GC Worker] User %s email lookup failed (may already be deleted): %v", item.ItemID, err)
		return nil
	}

	// 1. Soft-delete all owned libraries → they go to library trash
	libIDs, err := w.store.ListLibrariesByOwner(item.OrgID, userID)
	if err != nil {
		log.Printf("[GC Worker] Failed to list libraries for user %s: %v", item.ItemID, err)
	} else {
		for _, libID := range libIDs {
			if err := w.store.SoftDeleteLibrary(item.OrgID, libID, userID); err != nil {
				log.Printf("[GC Worker] Failed to soft-delete library %s for user %s: %v", libID, item.ItemID, err)
			}
		}
	}

	// 2. Remove from all groups (both tables)
	groupIDs, err := w.store.ListGroupMembershipsByUser(item.OrgID, userID)
	if err != nil {
		log.Printf("[GC Worker] Failed to list groups for user %s: %v", item.ItemID, err)
	} else {
		for _, groupID := range groupIDs {
			w.store.DeleteGroupMember(groupID, userID)
			w.store.DeleteGroupByMember(item.OrgID, userID, groupID)
		}
	}

	// 3. Clean up shares received by this user
	shares, err := w.store.ListSharesByUser(userID)
	if err != nil {
		log.Printf("[GC Worker] Failed to list shares for user %s: %v", item.ItemID, err)
	} else {
		for _, share := range shares {
			w.store.DeleteShareByUser(share.SharedTo, share.LibraryID)
		}
	}

	// 4. Delete starred files and monitored repos
	w.store.DeleteStarredFilesByUser(userID)
	w.store.DeleteMonitoredReposByUser(userID)

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
		Details:    fmt.Sprintf("email=%s libraries=%d groups=%d shares=%d", email, len(libIDs), len(groupIDs), len(shares)),
		Timestamp:  time.Now(),
	})

	log.Printf("[GC Worker] Cascade-deleted user %s (%s): %d libraries, %d groups, %d shares",
		item.ItemID, email, len(libIDs), len(groupIDs), len(shares))
	return nil
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

	storageClass := item.StorageClass
	if storageClass == "" {
		storageClass, _ = w.store.GetLibraryStorageClass(item.OrgID, libraryID)
	}

	// 1. Enqueue all library contents for deletion (commits, fs_objects, artifacts)
	if err := w.EnqueueLibraryContents(item.OrgID, libraryID, storageClass); err != nil {
		return fmt.Errorf("failed to enqueue library contents: %w", err)
	}

	// 2. Hard-delete the library record itself
	if err := w.store.HardDeleteLibrary(item.OrgID, libraryID); err != nil {
		return fmt.Errorf("failed to hard-delete library %s: %w", item.ItemID, err)
	}

	// 3. Audit log
	w.store.WriteAuditLog(AuditLogEntry{
		OrgID:      item.OrgID,
		Action:     "gc_library_cascade_deleted",
		TargetType: "library",
		TargetID:   item.ItemID,
		ActorID:    "gc_worker",
		Details:    fmt.Sprintf("storage_class=%s", storageClass),
		Timestamp:  time.Now(),
	})

	log.Printf("[GC Worker] Cascade-deleted library %s (storage_class=%s)", item.ItemID, storageClass)
	return nil
}

// decrementAndFindZeroRef decrements ref_count for blocks and returns those that hit 0.
func (w *Worker) decrementAndFindZeroRef(orgID uuid.UUID, blockIDs []string) []string {
	var zeroRef []string
	for _, blockID := range blockIDs {
		if err := w.store.DecrementBlockRefCount(orgID, blockID); err != nil {
			continue
		}

		refCount, err := w.store.GetBlockRefCount(orgID, blockID)
		if err != nil {
			continue
		}

		if refCount <= 0 {
			zeroRef = append(zeroRef, blockID)
		}
	}
	return zeroRef
}

// EnqueueLibraryContents enqueues all contents of a deleted library for GC.
// Only enqueues commits and fs_objects — blocks are handled in cascade
// when fs_objects are processed (via decrementAndFindZeroRef).
func (w *Worker) EnqueueLibraryContents(orgID, libraryID uuid.UUID, storageClass string) error {
	now := time.Now()

	// Enqueue all commits for this library (batched)
	commits, err := w.store.ListCommitsForLibrary(libraryID)
	if err != nil {
		return fmt.Errorf("failed to list commits for library %s: %w", libraryID, err)
	}
	if len(commits) > 0 {
		batch := make([]QueueItem, 0, len(commits))
		for _, c := range commits {
			batch = append(batch, QueueItem{
				OrgID: orgID, QueuedAt: now, ItemType: ItemCommit,
				ItemID: c.CommitID, LibraryID: libraryID,
			})
		}
		if err := w.queue.EnqueueBatch(batch); err != nil {
			return fmt.Errorf("failed to batch enqueue commits for library %s: %w", libraryID, err)
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
			batch = append(batch, QueueItem{
				OrgID: orgID, QueuedAt: now, ItemType: ItemFSObject,
				ItemID: obj.FSID, LibraryID: libraryID,
			})
		}
		if err := w.queue.EnqueueBatch(batch); err != nil {
			return fmt.Errorf("failed to batch enqueue fs_objects for library %s: %w", libraryID, err)
		}
	}

	// Clean up library-specific artifacts that don't cascade through fs_objects
	w.enqueueLibraryArtifacts(orgID, libraryID)

	log.Printf("[GC Worker] Enqueued library %s contents for deletion (%d commits, %d fs_objects)", libraryID, len(commits), len(fsObjects))
	return nil
}

// enqueueLibraryArtifacts cleans up ALL auxiliary data tied to a deleted library:
// share links, shares, tags, tag counters, api tokens, locked files,
// starred files, monitored repos, and restore jobs.
func (w *Worker) enqueueLibraryArtifacts(orgID, libraryID uuid.UUID) {
	// Delete share links via the by_library index (efficient)
	tokens, err := w.store.DeleteShareLinksByLibrary(orgID, libraryID)
	if err == nil && len(tokens) > 0 {
		log.Printf("[GC Worker] Cleaned up %d share links for deleted library %s", len(tokens), libraryID)
	}

	// Delete shares (user-to-user and group shares)
	shares, err := w.store.ListSharesByLibrary(libraryID)
	if err == nil {
		for _, share := range shares {
			w.store.DeleteShare(libraryID, share.ShareID)
			w.store.DeleteShareByUser(share.SharedTo, libraryID)
		}
		if len(shares) > 0 {
			log.Printf("[GC Worker] Cleaned up %d shares for deleted library %s", len(shares), libraryID)
		}
	}

	// Delete repo tags and file tags
	if err := w.cleanupLibraryTags(libraryID); err != nil {
		log.Printf("[GC Worker] Error cleaning tags for library %s: %v", libraryID, err)
	}

	// Delete tag counter tables (repo_tag_counters, file_tag_counters, repo_tag_file_counts)
	w.store.DeleteRepoTagCounters(libraryID)
	w.store.DeleteFileTagCounters(libraryID)
	w.store.DeleteRepoTagFileCounts(libraryID)

	// Delete API tokens
	tokens2, err := w.store.ListRepoAPITokensByLibrary(libraryID)
	if err == nil {
		for _, t := range tokens2 {
			w.store.DeleteRepoAPIToken(libraryID, t.AppName)
			w.store.DeleteRepoAPITokenByToken(t.APIToken)
		}
	}

	// Delete locked files
	w.store.DeleteLockedFilesByLibrary(libraryID)

	// Delete starred files referencing this library
	if err := w.store.DeleteStarredFilesByLibrary(libraryID); err != nil {
		log.Printf("[GC Worker] Error cleaning starred files for library %s: %v", libraryID, err)
	}

	// Delete monitored repos referencing this library
	if err := w.store.DeleteMonitoredReposByLibrary(libraryID); err != nil {
		log.Printf("[GC Worker] Error cleaning monitored repos for library %s: %v", libraryID, err)
	}

	// Delete restore jobs for this library
	if err := w.store.DeleteRestoreJobsByLibrary(orgID, libraryID); err != nil {
		log.Printf("[GC Worker] Error cleaning restore jobs for library %s: %v", libraryID, err)
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
}

func (w *Worker) cleanupLibraryTags(libraryID uuid.UUID) error {
	// Delete file tags first (they reference repo tags)
	fileTags, err := w.store.ListFileTagsByLibrary(libraryID)
	if err != nil {
		return err
	}
	for _, ft := range fileTags {
		w.store.DeleteFileTag(libraryID, ft.FilePath, ft.TagID)
		w.store.DeleteFileTagByID(libraryID, ft.FileTagID)
	}

	// Delete repo tag definitions
	tagIDs, err := w.store.ListRepoTagsByLibrary(libraryID)
	if err != nil {
		return err
	}
	for _, tagIDStr := range tagIDs {
		var tagID int
		if _, err := fmt.Sscanf(tagIDStr, "%d", &tagID); err == nil {
			w.store.DeleteRepoTag(libraryID, tagID)
		}
	}

	return nil
}
