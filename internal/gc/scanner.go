package gc

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	"github.com/google/uuid"
)

// OrphanRecoverer retries S3 deletes for blocks whose DB rows are gone but
// whose S3 objects linger. Implemented by *Worker.
type OrphanRecoverer interface {
	RecoverS3Orphans(ctx context.Context, perOrgLimit int) (int, error)
}

// Scanner periodically finds orphaned items that were missed by inline enqueue
// and adds them to the gc_queue for processing.
type Scanner struct {
	store           GCStore
	queue           *Queue
	stats           *Stats
	config          config.GCConfig
	orphanRecoverer OrphanRecoverer
}

// NewScanner creates a new safety scanner.
func NewScanner(store GCStore, queue *Queue, stats *Stats, cfg config.GCConfig) *Scanner {
	return &Scanner{
		store:  store,
		queue:  queue,
		stats:  stats,
		config: cfg,
	}
}

// SetOrphanRecoverer wires the S3 orphan recovery dependency. Optional; if
// unset, the s3_orphan_recovery phase is a no-op (useful for mock-only tests).
func (s *Scanner) SetOrphanRecoverer(r OrphanRecoverer) {
	s.orphanRecoverer = r
}

func (s *Scanner) saveCursor(key string, day time.Time) error {
	return s.store.SaveGCStats(key, db.GCProjectionDateString(day))
}

func expiredShareLinksCursorDay(now time.Time) time.Time {
	return db.GCProjectionUTCDate(now)
}

func expiredSharesCursorDay(now time.Time) time.Time {
	return db.GCProjectionUTCDate(now)
}

func deletedUsersCursorDay(now time.Time, graceDays int) time.Time {
	return db.GCProjectionUTCDate(now.AddDate(0, 0, -graceDays))
}

func recordScannerAction(phase, action string, count int) {
	metrics.GCScannerActionsTotal.WithLabelValues(phase, action).Add(float64(count))
}

func isScannerInterruptError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// ScanOnce performs a full scan of all phases.
func (s *Scanner) ScanOnce(ctx context.Context) error {
	start := time.Now()
	log.Println("[GC Scanner] Starting safety scan...")

	enqueued := 0
	var scanErr error

	phases := []struct {
		name string
		fn   func(context.Context) (int, error)
	}{
		{"orphaned_blocks", s.scanOrphanedBlocks},
		{"expired_links", s.scanExpiredShareLinks},
		{"orphaned_commits", s.scanOrphanedCommits},
		{"orphaned_fs_objects", s.scanOrphanedFSObjects},
		{"expired_versions", s.scanExpiredVersions},
		{"auto_delete", s.scanAutoDeleteExpiredObjects},
		{"expired_shares", s.scanExpiredShares},
		{"expired_restore_jobs", s.scanExpiredRestoreJobs},
		{"orphaned_group_shares", s.scanOrphanedGroupShares},
		{"expired_deleted_users", s.scanExpiredDeletedUsers},
		{"storage_counter_reconciliation", s.scanPendingStorageCounterReconciliation},
		{"expired_deleted_libraries", s.scanExpiredDeletedLibraries},
		{"expired_deleted_orgs", s.scanExpiredDeletedOrgs},
		{"s3_orphan_recovery", s.scanS3OrphanRecovery},
	}

	for _, phase := range phases {
		select {
		case <-ctx.Done():
			log.Printf("[GC Scanner] Scan interrupted after %d items in %v", enqueued, time.Since(start))
			if scanErr != nil {
				return scanErr
			}
			return ctx.Err()
		default:
		}

		n, err := phase.fn(ctx)
		if err != nil {
			if isScannerInterruptError(err) {
				if scanErr != nil {
					return scanErr
				}
				return err
			}
			log.Printf("[GC Scanner] Error in phase %s: %v", phase.name, err)
			recordScannerAction(phase.name, "phase_error", 1)
			scanErr = errors.Join(scanErr, fmt.Errorf("%s: %w", phase.name, err))
		}
		enqueued += n
	}

	elapsed := time.Since(start)
	log.Printf("[GC Scanner] Safety scan complete: enqueued %d items in %v", enqueued, elapsed)
	s.stats.SetLastScanRun(time.Now())
	return scanErr
}

func (s *Scanner) scanPendingStorageCounterReconciliation(ctx context.Context) (int, error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}

	log.Println("[GC Scanner] Phase 11: Reconciling pending storage counters...")
	reconciled, err := s.store.ReconcilePendingStorageCounters()
	if err != nil {
		return reconciled, err
	}
	log.Printf("[GC Scanner] Phase 11 complete: reconciled %d storage counter scopes", reconciled)
	metrics.GCScannerLastPhaseRun.WithLabelValues("storage_counter_reconciliation").SetToCurrentTime()
	return 0, nil
}

// scanOrphanedBlocks re-enqueues zero-ref block candidates that should still be in GC.
func (s *Scanner) scanOrphanedBlocks(ctx context.Context) (int, error) {
	log.Println("[GC Scanner] Phase 1: Scanning for orphaned blocks...")

	candidateOrgs, err := s.store.ListBlockGCCandidateOrgs()
	if err != nil {
		return 0, err
	}
	allOrgs, err := s.store.ListOrganizations()
	if err != nil {
		return 0, err
	}

	orgSeen := make(map[uuid.UUID]struct{}, len(candidateOrgs)+len(allOrgs))
	orgs := make([]uuid.UUID, 0, len(candidateOrgs)+len(allOrgs))
	for _, orgID := range candidateOrgs {
		if _, ok := orgSeen[orgID]; ok {
			continue
		}
		orgSeen[orgID] = struct{}{}
		orgs = append(orgs, orgID)
	}
	for _, orgID := range allOrgs {
		if _, ok := orgSeen[orgID]; ok {
			continue
		}
		orgSeen[orgID] = struct{}{}
		orgs = append(orgs, orgID)
	}

	enqueued := 0
	for _, orgID := range orgs {
		select {
		case <-ctx.Done():
			return enqueued, ctx.Err()
		default:
		}

		candidates, err := s.store.ListBlockGCCandidates(orgID)
		if err != nil {
			continue
		}
		blocks, err := s.store.ListBlocksForOrg(orgID)
		if err != nil {
			continue
		}

		var batch []QueueItem
		queuedBlocks := make(map[string]struct{}, len(candidates))
		for _, candidate := range candidates {
			exists, err := s.store.QueueItemExists(orgID, candidate.CandidateAt, ItemBlock, candidate.BlockID)
			if err != nil {
				log.Printf("[GC Scanner] Phase 1: failed to inspect queue for block %s in org %s: %v", candidate.BlockID, orgID, err)
				continue
			}
			if exists {
				queuedBlocks[candidate.BlockID] = struct{}{}
				continue
			}
			queuedBlocks[candidate.BlockID] = struct{}{}
			batch = append(batch, QueueItem{
				OrgID:        orgID,
				QueuedAt:     candidate.CandidateAt,
				ItemType:     ItemBlock,
				ItemID:       candidate.BlockID,
				LibraryID:    uuid.Nil,
				StorageClass: candidate.StorageClass,
			})
		}
		for _, block := range blocks {
			if block.RefCount > 0 {
				continue
			}
			if _, ok := queuedBlocks[block.BlockID]; ok {
				continue
			}
			candidateAt, err := s.store.EnsureBlockGCCandidate(orgID, block.BlockID, block.StorageClass, time.Now())
			if err != nil {
				log.Printf("[GC Scanner] Phase 1: failed to backfill GC candidate for block %s in org %s: %v", block.BlockID, orgID, err)
				continue
			}
			exists, err := s.store.QueueItemExists(orgID, candidateAt, ItemBlock, block.BlockID)
			if err != nil {
				log.Printf("[GC Scanner] Phase 1: failed to inspect queue for reconciled block %s in org %s: %v", block.BlockID, orgID, err)
				continue
			}
			if exists {
				queuedBlocks[block.BlockID] = struct{}{}
				continue
			}
			queuedBlocks[block.BlockID] = struct{}{}
			batch = append(batch, QueueItem{
				OrgID:        orgID,
				QueuedAt:     candidateAt,
				ItemType:     ItemBlock,
				ItemID:       block.BlockID,
				LibraryID:    uuid.Nil,
				StorageClass: block.StorageClass,
			})
		}
		if len(batch) > 0 {
			if err := s.queue.EnqueueBatch(batch); err != nil {
				log.Printf("[GC Scanner] Phase 1: failed to batch enqueue blocks for org %s: %v", orgID, err)
			} else {
				enqueued += len(batch)
			}
		}
	}

	log.Printf("[GC Scanner] Phase 1 complete: enqueued %d orphaned blocks", enqueued)
	metrics.GCItemsEnqueuedTotal.WithLabelValues("orphaned_blocks").Add(float64(enqueued))
	metrics.GCScannerLastPhaseRun.WithLabelValues("orphaned_blocks").SetToCurrentTime()
	return enqueued, nil
}

// scanExpiredShareLinks finds share links past their expiration date.
func (s *Scanner) scanExpiredShareLinks(ctx context.Context) (int, error) {
	log.Println("[GC Scanner] Phase 2: Scanning for expired share links...")

	now := time.Now()
	links, err := s.store.ListExpiredShareLinks()
	if err != nil {
		return 0, err
	}

	cleaned := 0
	failed := 0
	var phaseErr error
	for _, link := range links {
		select {
		case <-ctx.Done():
			return cleaned, ctx.Err()
		default:
		}

		if err := s.store.DeleteExpiredShareLink(link); err != nil {
			log.Printf("[GC Scanner] Phase 2: failed to delete expired share link %s: %v", link.ShareToken, err)
			failed++
			if phaseErr == nil {
				phaseErr = err
			}
			continue
		}
		cleaned++
	}

	if phaseErr == nil {
		if err := s.saveCursor(gcExpiredShareLinksCursorKey, expiredShareLinksCursorDay(now)); err != nil {
			return cleaned, err
		}
	}

	log.Printf("[GC Scanner] Phase 2 complete: cleaned %d expired share links", cleaned)
	recordScannerAction("expired_links", "cleaned", cleaned)
	recordScannerAction("expired_links", "failed", failed)
	metrics.GCScannerLastPhaseRun.WithLabelValues("expired_links").SetToCurrentTime()
	return cleaned, phaseErr
}

// scanOrphanedCommits finds commits whose library no longer exists.
func (s *Scanner) scanOrphanedCommits(ctx context.Context) (int, error) {
	log.Println("[GC Scanner] Phase 3: Scanning for orphaned commits...")

	libraryIDs, err := s.store.ListDistinctCommitLibraries()
	if err != nil {
		return 0, err
	}

	enqueued := 0
	for _, libID := range libraryIDs {
		select {
		case <-ctx.Done():
			return enqueued, ctx.Err()
		default:
		}

		exists, err := s.store.LibraryExists(libID)
		if err != nil || exists {
			continue
		}

		// Library doesn't exist - try to find the org
		orgID, err := s.store.FindOrgForLibrary(libID)
		if err != nil || orgID == uuid.Nil {
			// Can't determine org — scan all orgs to find matching commits
			// This handles the case where library_by_id record was also deleted
			log.Printf("[GC Scanner] Phase 3: Library %s deleted, org lookup failed, skipping", libID)
			continue
		}

		commitIDs, err := s.store.ListCommitIDsForLibrary(libID)
		if err != nil {
			continue
		}
		if len(commitIDs) > 0 {
			now := time.Now()
			batch := make([]QueueItem, 0, len(commitIDs))
			for _, commitID := range commitIDs {
				batch = append(batch, QueueItem{
					OrgID:     orgID,
					QueuedAt:  now,
					ItemType:  ItemCommit,
					ItemID:    commitID,
					LibraryID: libID,
				})
			}
			if err := s.queue.EnqueueBatch(batch); err != nil {
				log.Printf("[GC Scanner] Phase 3: failed to batch enqueue commits for library %s: %v", libID, err)
			} else {
				enqueued += len(batch)
			}
		}
	}

	log.Printf("[GC Scanner] Phase 3 complete: enqueued %d orphaned commits", enqueued)
	metrics.GCItemsEnqueuedTotal.WithLabelValues("orphaned_commits").Add(float64(enqueued))
	metrics.GCScannerLastPhaseRun.WithLabelValues("orphaned_commits").SetToCurrentTime()
	return enqueued, nil
}

// scanOrphanedFSObjects finds fs_objects whose library no longer exists.
func (s *Scanner) scanOrphanedFSObjects(ctx context.Context) (int, error) {
	log.Println("[GC Scanner] Phase 4: Scanning for orphaned fs_objects...")

	libraryIDs, err := s.store.ListDistinctFSObjectLibraries()
	if err != nil {
		return 0, err
	}

	enqueued := 0
	for _, libID := range libraryIDs {
		select {
		case <-ctx.Done():
			return enqueued, ctx.Err()
		default:
		}

		exists, err := s.store.LibraryExists(libID)
		if err != nil || exists {
			continue
		}

		orgID, err := s.store.FindOrgForLibrary(libID)
		if err != nil || orgID == uuid.Nil {
			log.Printf("[GC Scanner] Phase 4: Library %s deleted, org lookup failed, skipping", libID)
			continue
		}

		fsIDs, err := s.store.ListFSObjectIDsForLibrary(libID)
		if err != nil {
			continue
		}
		if len(fsIDs) > 0 {
			now := time.Now()
			batch := make([]QueueItem, 0, len(fsIDs))
			for _, fsID := range fsIDs {
				batch = append(batch, QueueItem{
					OrgID:     orgID,
					QueuedAt:  now,
					ItemType:  ItemFSObject,
					ItemID:    fsID,
					LibraryID: libID,
				})
			}
			if err := s.queue.EnqueueBatch(batch); err != nil {
				log.Printf("[GC Scanner] Phase 4: failed to batch enqueue fs_objects for library %s: %v", libID, err)
			} else {
				enqueued += len(batch)
			}
		}
	}

	log.Printf("[GC Scanner] Phase 4 complete: enqueued %d orphaned fs_objects", enqueued)
	metrics.GCItemsEnqueuedTotal.WithLabelValues("orphaned_fs_objects").Add(float64(enqueued))
	metrics.GCScannerLastPhaseRun.WithLabelValues("orphaned_fs_objects").SetToCurrentTime()
	return enqueued, nil
}

// scanExpiredVersions finds commits older than the library's version_ttl_days
// that are NOT in the HEAD commit chain, and enqueues them for deletion.
func (s *Scanner) scanExpiredVersions(ctx context.Context) (int, error) {
	log.Println("[GC Scanner] Phase 5: Scanning for expired versions...")

	libs, err := s.store.ListLibrariesWithVersionTTL()
	if err != nil {
		return 0, err
	}

	enqueued := 0
	for _, lib := range libs {
		select {
		case <-ctx.Done():
			return enqueued, ctx.Err()
		default:
		}

		commits, err := s.store.ListCommitsWithTimestamps(lib.LibraryID)
		if err != nil {
			log.Printf("[GC Scanner] Phase 5: failed to list commits for library %s: %v", lib.LibraryID, err)
			continue
		}

		// Build a lookup map for walking the parent chain
		commitMap := make(map[string]CommitWithTimestamp, len(commits))
		for _, c := range commits {
			commitMap[c.CommitID] = c
		}

		// Walk HEAD chain to build the keep set
		keepSet := make(map[string]bool)
		current := lib.HeadCommitID
		for current != "" {
			if keepSet[current] {
				break // cycle protection
			}
			keepSet[current] = true
			if c, ok := commitMap[current]; ok {
				current = c.ParentID
			} else {
				break
			}
		}

		// Find expired commits not in keep set
		cutoff := time.Now().AddDate(0, 0, -lib.VersionTTLDays)
		now := time.Now()
		var batch []QueueItem
		for _, c := range commits {
			if keepSet[c.CommitID] {
				continue
			}
			if c.CreatedAt.Before(cutoff) {
				batch = append(batch, QueueItem{
					OrgID:     lib.OrgID,
					QueuedAt:  now,
					ItemType:  ItemCommit,
					ItemID:    c.CommitID,
					LibraryID: lib.LibraryID,
				})
			}
		}
		if len(batch) > 0 {
			if err := s.queue.EnqueueBatch(batch); err != nil {
				log.Printf("[GC Scanner] Phase 5: failed to batch enqueue expired commits for library %s: %v", lib.LibraryID, err)
			} else {
				enqueued += len(batch)
			}
		}
	}

	log.Printf("[GC Scanner] Phase 5 complete: enqueued %d expired version commits", enqueued)
	metrics.GCItemsEnqueuedTotal.WithLabelValues("expired_versions").Add(float64(enqueued))
	metrics.GCScannerLastPhaseRun.WithLabelValues("expired_versions").SetToCurrentTime()
	return enqueued, nil
}

// scanAutoDeleteExpiredObjects finds fs_objects that are not referenced by the
// HEAD commit tree or any recent commit tree (within auto_delete_days), and
// enqueues them for deletion.
func (s *Scanner) scanAutoDeleteExpiredObjects(ctx context.Context) (int, error) {
	log.Println("[GC Scanner] Phase 6: Scanning for auto-delete expired fs_objects...")

	libs, err := s.store.ListLibrariesWithAutoDelete()
	if err != nil {
		return 0, err
	}

	enqueued := 0
	for _, lib := range libs {
		select {
		case <-ctx.Done():
			return enqueued, ctx.Err()
		default:
		}

		commits, err := s.store.ListCommitsWithTimestamps(lib.LibraryID)
		if err != nil {
			log.Printf("[GC Scanner] Phase 6: failed to list commits for library %s: %v", lib.LibraryID, err)
			continue
		}

		// Build a lookup map for walking the parent chain
		commitMap := make(map[string]CommitWithTimestamp, len(commits))
		for _, c := range commits {
			commitMap[c.CommitID] = c
		}

		// Walk HEAD chain to build keepCommits
		keepCommits := make(map[string]bool)
		current := lib.HeadCommitID
		for current != "" {
			if keepCommits[current] {
				break // cycle protection
			}
			keepCommits[current] = true
			if c, ok := commitMap[current]; ok {
				current = c.ParentID
			} else {
				break
			}
		}

		// Add commits within auto_delete_days window to keepCommits
		cutoff := time.Now().AddDate(0, 0, -lib.AutoDeleteDays)
		for _, c := range commits {
			if !c.CreatedAt.Before(cutoff) {
				keepCommits[c.CommitID] = true
			}
		}

		// Walk filesystem trees of all keepCommits to build keepFSSet (iterative)
		keepFSSet := make(map[string]bool)
		for commitID := range keepCommits {
			if c, ok := commitMap[commitID]; ok && c.RootFSID != "" {
				s.walkFSTree(lib.LibraryID, c.RootFSID, keepFSSet)
			}
		}

		// List all fs_object IDs for this library and enqueue orphans
		allFSIDs, err := s.store.ListFSObjectIDsForLibrary(lib.LibraryID)
		if err != nil {
			log.Printf("[GC Scanner] Phase 6: failed to list fs_objects for library %s: %v", lib.LibraryID, err)
			continue
		}

		now := time.Now()
		var batch []QueueItem
		for _, fsID := range allFSIDs {
			if !keepFSSet[fsID] {
				batch = append(batch, QueueItem{
					OrgID:     lib.OrgID,
					QueuedAt:  now,
					ItemType:  ItemFSObject,
					ItemID:    fsID,
					LibraryID: lib.LibraryID,
				})
			}
		}
		if len(batch) > 0 {
			if err := s.queue.EnqueueBatch(batch); err != nil {
				log.Printf("[GC Scanner] Phase 6: failed to batch enqueue fs_objects for library %s: %v", lib.LibraryID, err)
			} else {
				enqueued += len(batch)
			}
		}
	}

	log.Printf("[GC Scanner] Phase 6 complete: enqueued %d auto-delete expired fs_objects", enqueued)
	metrics.GCItemsEnqueuedTotal.WithLabelValues("auto_delete").Add(float64(enqueued))
	metrics.GCScannerLastPhaseRun.WithLabelValues("auto_delete").SetToCurrentTime()
	return enqueued, nil
}

// scanExpiredShares finds user-to-user library shares past their expiration date.
func (s *Scanner) scanExpiredShares(ctx context.Context) (int, error) {
	log.Println("[GC Scanner] Phase 7: Scanning for expired user-to-user shares...")

	now := time.Now()
	shares, err := s.store.ListExpiredShares()
	if err != nil {
		return 0, err
	}

	enqueued := 0
	failed := 0
	var phaseErr error
	for _, share := range shares {
		select {
		case <-ctx.Done():
			return enqueued, ctx.Err()
		default:
		}

		// Delete directly — shares are small metadata, no need for queue
		if err := s.store.DeleteExpiredShare(share); err != nil {
			log.Printf("[GC Scanner] Phase 7: failed to delete expired share %s for library %s: %v", share.ShareID, share.LibraryID, err)
			failed++
			if phaseErr == nil {
				phaseErr = err
			}
			continue
		}
		enqueued++
	}

	if phaseErr == nil {
		if err := s.saveCursor(gcExpiredSharesCursorKey, expiredSharesCursorDay(now)); err != nil {
			return enqueued, err
		}
	}

	log.Printf("[GC Scanner] Phase 7 complete: cleaned %d expired shares", enqueued)
	recordScannerAction("expired_shares", "cleaned", enqueued)
	recordScannerAction("expired_shares", "failed", failed)
	metrics.GCScannerLastPhaseRun.WithLabelValues("expired_shares").SetToCurrentTime()
	return enqueued, phaseErr
}

// scanExpiredRestoreJobs finds completed/expired Glacier restore jobs.
func (s *Scanner) scanExpiredRestoreJobs(ctx context.Context) (int, error) {
	log.Println("[GC Scanner] Phase 8: Scanning for expired restore jobs...")

	jobs, err := s.store.ListExpiredRestoreJobs()
	if err != nil {
		return 0, err
	}

	enqueued := 0
	failed := 0
	var phaseErr error
	for _, job := range jobs {
		select {
		case <-ctx.Done():
			return enqueued, ctx.Err()
		default:
		}

		// Delete directly — restore jobs are small metadata
		if err := s.store.DeleteRestoreJob(job.OrgID, job.LibraryID, job.JobID); err != nil {
			log.Printf("[GC Scanner] Phase 8: failed to delete restore job %s for library %s: %v", job.JobID, job.LibraryID, err)
			failed++
			if phaseErr == nil {
				phaseErr = err
			}
			continue
		}
		enqueued++
	}

	log.Printf("[GC Scanner] Phase 8 complete: cleaned %d expired restore jobs", enqueued)
	recordScannerAction("expired_restore_jobs", "cleaned", enqueued)
	recordScannerAction("expired_restore_jobs", "failed", failed)
	metrics.GCScannerLastPhaseRun.WithLabelValues("expired_restore_jobs").SetToCurrentTime()
	return enqueued, phaseErr
}

// scanOrphanedGroupShares finds shares where shared_to is a group that no longer exists.
func (s *Scanner) scanOrphanedGroupShares(ctx context.Context) (int, error) {
	log.Println("[GC Scanner] Phase 9: Scanning for orphaned group shares...")

	groupShares, err := s.store.ListAllGroupShares()
	if err != nil {
		return 0, err
	}

	// Cache group existence checks to avoid repeated lookups
	groupExistsCache := make(map[uuid.UUID]bool)

	cleaned := 0
	failed := 0
	var phaseErr error
	for _, gs := range groupShares {
		select {
		case <-ctx.Done():
			return cleaned, ctx.Err()
		default:
		}

		// Check if group still exists (with cache)
		exists, cached := groupExistsCache[gs.SharedTo]
		if !cached {
			// We need the org_id to check group existence.
			// Try to find it via the library's org.
			orgID, err := s.store.FindOrgForLibrary(gs.LibraryID)
			if err != nil || orgID == uuid.Nil {
				continue
			}
			exists, _ = s.store.GroupExists(orgID, gs.SharedTo)
			groupExistsCache[gs.SharedTo] = exists
		}

		if !exists {
			// Group deleted — clean up the orphaned share
			if err := s.store.DeleteShare(gs.LibraryID, gs.ShareID); err != nil {
				log.Printf("[GC Scanner] Phase 9: failed to delete orphaned group share %s for library %s: %v", gs.ShareID, gs.LibraryID, err)
				failed++
				if phaseErr == nil {
					phaseErr = err
				}
				continue
			}
			cleaned++
		}
	}

	log.Printf("[GC Scanner] Phase 9 complete: cleaned %d orphaned group shares", cleaned)
	recordScannerAction("orphaned_group_shares", "cleaned", cleaned)
	recordScannerAction("orphaned_group_shares", "failed", failed)
	metrics.GCScannerLastPhaseRun.WithLabelValues("orphaned_group_shares").SetToCurrentTime()
	return cleaned, phaseErr
}

// scanExpiredDeletedUsers finds soft-deleted users whose grace period has expired
// and enqueues them for cascade deletion.
func (s *Scanner) scanExpiredDeletedUsers(ctx context.Context) (int, error) {
	log.Println("[GC Scanner] Phase 10: Scanning for expired deleted users...")

	now := time.Now()
	users, err := s.store.ListDeletedUsersExpired(s.config.UserGraceDays)
	if err != nil {
		return 0, err
	}

	enqueued := 0
	var batch []QueueItem
	var phaseErr error
	for _, u := range users {
		exists, err := s.store.QueueItemExists(u.OrgID, u.DeletedAt, ItemUserCascade, u.UserID.String())
		if err != nil {
			log.Printf("[GC Scanner] Phase 10: failed to dedupe expired deleted user %s: %v", u.UserID, err)
			if phaseErr == nil {
				phaseErr = err
			}
			continue
		}
		if exists {
			continue
		}
		batch = append(batch, QueueItem{
			OrgID:    u.OrgID,
			QueuedAt: u.DeletedAt,
			ItemType: ItemUserCascade,
			ItemID:   u.UserID.String(),
		})
	}
	if len(batch) > 0 {
		if err := s.queue.EnqueueBatch(batch); err != nil {
			log.Printf("[GC Scanner] Phase 10: failed to enqueue expired deleted users: %v", err)
			if phaseErr == nil {
				phaseErr = err
			}
		} else {
			enqueued = len(batch)
		}
	}

	if phaseErr == nil {
		if err := s.saveCursor(gcDeletedUsersCursorKey, deletedUsersCursorDay(now, s.config.UserGraceDays)); err != nil {
			return enqueued, err
		}
	}

	log.Printf("[GC Scanner] Phase 10 complete: enqueued %d expired deleted users", enqueued)
	metrics.GCItemsEnqueuedTotal.WithLabelValues("expired_deleted_users").Add(float64(enqueued))
	metrics.GCScannerLastPhaseRun.WithLabelValues("expired_deleted_users").SetToCurrentTime()
	return enqueued, phaseErr
}

// scanExpiredDeletedLibraries finds soft-deleted libraries whose trash retention
// period has expired and enqueues them for cascade deletion (commits, fs_objects,
// blocks, and all library artifacts).
func (s *Scanner) scanExpiredDeletedLibraries(ctx context.Context) (int, error) {
	log.Println("[GC Scanner] Phase 11: Scanning for expired deleted libraries...")

	libs, err := s.store.ListExpiredDeletedLibraries(s.config.TrashRetentionDays)
	if err != nil {
		return 0, err
	}

	enqueued := 0
	var batch []QueueItem
	for _, lib := range libs {
		exists, err := s.store.QueueItemExists(lib.OrgID, lib.DeletedAt, ItemLibraryCascade, lib.LibraryID.String())
		if err != nil {
			log.Printf("[GC Scanner] Phase 11: failed to dedupe expired deleted library %s: %v", lib.LibraryID, err)
			continue
		}
		if exists {
			continue
		}
		batch = append(batch, QueueItem{
			OrgID:        lib.OrgID,
			QueuedAt:     lib.DeletedAt,
			ItemType:     ItemLibraryCascade,
			ItemID:       lib.LibraryID.String(),
			StorageClass: lib.StorageClass,
		})
	}
	if len(batch) > 0 {
		if err := s.queue.EnqueueBatch(batch); err != nil {
			log.Printf("[GC Scanner] Phase 11: failed to enqueue expired deleted libraries: %v", err)
		} else {
			enqueued = len(batch)
		}
	}

	log.Printf("[GC Scanner] Phase 11 complete: enqueued %d expired deleted libraries", enqueued)
	metrics.GCItemsEnqueuedTotal.WithLabelValues("expired_deleted_libraries").Add(float64(enqueued))
	metrics.GCScannerLastPhaseRun.WithLabelValues("expired_deleted_libraries").SetToCurrentTime()
	return enqueued, nil
}

// scanExpiredDeletedOrgs finds soft-deleted organizations whose grace period
// has expired and enqueues them for cascade deletion (users, libraries, groups).
func (s *Scanner) scanExpiredDeletedOrgs(ctx context.Context) (int, error) {
	log.Println("[GC Scanner] Phase 12: Scanning for expired deleted organizations...")

	orgs, err := s.store.ListExpiredDeletedOrgs(s.config.OrgGraceDays)
	if err != nil {
		return 0, err
	}

	enqueued := 0
	var batch []QueueItem
	for _, org := range orgs {
		exists, err := s.store.QueueItemExists(org.OrgID, org.DeletedAt, ItemOrgCascade, org.OrgID.String())
		if err != nil {
			log.Printf("[GC Scanner] Phase 12: failed to dedupe expired deleted org %s: %v", org.OrgID, err)
			continue
		}
		if exists {
			continue
		}
		batch = append(batch, QueueItem{
			OrgID:    org.OrgID,
			QueuedAt: org.DeletedAt,
			ItemType: ItemOrgCascade,
			ItemID:   org.OrgID.String(),
		})
	}
	if len(batch) > 0 {
		if err := s.queue.EnqueueBatch(batch); err != nil {
			log.Printf("[GC Scanner] Phase 12: failed to enqueue expired deleted orgs: %v", err)
		} else {
			enqueued = len(batch)
		}
	}

	log.Printf("[GC Scanner] Phase 12 complete: enqueued %d expired deleted organizations", enqueued)
	metrics.GCItemsEnqueuedTotal.WithLabelValues("expired_deleted_orgs").Add(float64(enqueued))
	metrics.GCScannerLastPhaseRun.WithLabelValues("expired_deleted_orgs").SetToCurrentTime()
	return enqueued, nil
}

// scanS3OrphanRecovery retries S3 deletes for blocks whose DB rows were
// removed successfully but whose S3 objects lingered because DeleteBlock
// failed after the LWT step (see docs/GC-SERVICE-ANALYSIS.md).
func (s *Scanner) scanS3OrphanRecovery(ctx context.Context) (int, error) {
	log.Println("[GC Scanner] Phase 13: Recovering S3 orphans...")
	if s.orphanRecoverer == nil {
		return 0, nil
	}
	recovered, err := s.orphanRecoverer.RecoverS3Orphans(ctx, 500)
	if err != nil {
		log.Printf("[GC Scanner] Phase 13: recovery error: %v", err)
	}
	log.Printf("[GC Scanner] Phase 13 complete: recovered %d S3 orphans", recovered)
	recordScannerAction("s3_orphan_recovery", "recovered", recovered)
	metrics.GCScannerLastPhaseRun.WithLabelValues("s3_orphan_recovery").SetToCurrentTime()
	return recovered, err
}

// walkFSTree iteratively walks a filesystem tree starting from fsID,
// adding all visited fs_ids to the visited set. Uses an explicit stack
// instead of recursion to avoid stack overflow on deep directory trees.
func (s *Scanner) walkFSTree(libraryID uuid.UUID, fsID string, visited map[string]bool) {
	if fsID == "" || visited[fsID] {
		return
	}

	stack := []string{fsID}
	for len(stack) > 0 {
		// Pop
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		if current == "" || visited[current] {
			continue
		}
		visited[current] = true

		obj, err := s.store.GetFSObject(libraryID, current)
		if err != nil {
			continue
		}

		// Push children
		for _, childID := range obj.DirEntries {
			if !visited[childID] {
				stack = append(stack, childID)
			}
		}
	}
}
