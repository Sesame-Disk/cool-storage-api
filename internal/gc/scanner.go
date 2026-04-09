package gc

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/config"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	"github.com/google/uuid"
)

func scanTaskID(kind string, orgID uuid.UUID, itemID string, markerTime time.Time) uuid.UUID {
	return uuid.NewMD5(uuid.NameSpaceOID, []byte(fmt.Sprintf("%s:%s:%s:%d", kind, orgID, itemID, markerTime.UTC().UnixNano())))
}

// Scanner periodically finds orphaned items that were missed by inline enqueue
// and adds them to the gc_queue for processing.
type Scanner struct {
	store  GCStore
	queue  *Queue
	stats  *Stats
	config config.GCConfig
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

// ScanOnce performs a full scan of all phases.
func (s *Scanner) ScanOnce(ctx context.Context) error {
	start := time.Now()
	log.Println("[GC Scanner] Starting safety scan...")

	enqueued := 0

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
		{"expired_deleted_libraries", s.scanExpiredDeletedLibraries},
		{"expired_deleted_orgs", s.scanExpiredDeletedOrgs},
	}

	for _, phase := range phases {
		select {
		case <-ctx.Done():
			log.Printf("[GC Scanner] Scan interrupted after %d items in %v", enqueued, time.Since(start))
			return ctx.Err()
		default:
		}

		n, err := phase.fn(ctx)
		if err != nil {
			log.Printf("[GC Scanner] Error in phase %s: %v", phase.name, err)
		}
		enqueued += n
	}

	elapsed := time.Since(start)
	log.Printf("[GC Scanner] Safety scan complete: enqueued %d items in %v", enqueued, elapsed)
	s.stats.SetLastScanRun(time.Now())
	return nil
}

// scanOrphanedBlocks re-enqueues zero-ref block candidates that should still be in GC.
func (s *Scanner) scanOrphanedBlocks(ctx context.Context) (int, error) {
	log.Println("[GC Scanner] Phase 1: Scanning for orphaned blocks...")

	orgs, err := s.store.ListBlockGCCandidateOrgs()
	if err != nil {
		return 0, err
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

		var batch []QueueItem
		for _, candidate := range candidates {
			exists, err := s.store.QueueItemExists(orgID, candidate.CandidateAt, ItemBlock, candidate.BlockID)
			if err != nil {
				log.Printf("[GC Scanner] Phase 1: failed to inspect queue for block %s in org %s: %v", candidate.BlockID, orgID, err)
				continue
			}
			if exists {
				continue
			}
			batch = append(batch, QueueItem{
				OrgID:        orgID,
				QueuedAt:     candidate.CandidateAt,
				ItemType:     ItemBlock,
				ItemID:       candidate.BlockID,
				LibraryID:    uuid.Nil,
				StorageClass: candidate.StorageClass,
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
	links, err := s.store.ListShareLinks()
	if err != nil {
		return 0, err
	}

	enqueued := 0
	var batch []QueueItem
	batchTime := time.Now()
	for _, link := range links {
		if !link.ExpiresAt.IsZero() && link.ExpiresAt.Before(now) {
			batch = append(batch, QueueItem{
				OrgID:    link.OrgID,
				QueuedAt: batchTime,
				ItemType: ItemShareLink,
				ItemID:   link.ShareToken,
			})
		}
	}
	if len(batch) > 0 {
		if err := s.queue.EnqueueBatch(batch); err != nil {
			log.Printf("[GC Scanner] Phase 2: failed to batch enqueue expired links: %v", err)
		} else {
			enqueued = len(batch)
		}
	}

	log.Printf("[GC Scanner] Phase 2 complete: enqueued %d expired share links", enqueued)
	metrics.GCItemsEnqueuedTotal.WithLabelValues("expired_links").Add(float64(enqueued))
	metrics.GCScannerLastPhaseRun.WithLabelValues("expired_links").SetToCurrentTime()
	return enqueued, nil
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

	shares, err := s.store.ListExpiredShares()
	if err != nil {
		return 0, err
	}

	enqueued := 0
	for _, share := range shares {
		select {
		case <-ctx.Done():
			return enqueued, ctx.Err()
		default:
		}

		// Delete directly — shares are small metadata, no need for queue
		if err := s.store.DeleteShare(share.LibraryID, share.ShareID); err == nil {
			enqueued++
		}
	}

	log.Printf("[GC Scanner] Phase 7 complete: cleaned %d expired shares", enqueued)
	metrics.GCItemsEnqueuedTotal.WithLabelValues("expired_shares").Add(float64(enqueued))
	metrics.GCScannerLastPhaseRun.WithLabelValues("expired_shares").SetToCurrentTime()
	return enqueued, nil
}

// scanExpiredRestoreJobs finds completed/expired Glacier restore jobs.
func (s *Scanner) scanExpiredRestoreJobs(ctx context.Context) (int, error) {
	log.Println("[GC Scanner] Phase 8: Scanning for expired restore jobs...")

	jobs, err := s.store.ListExpiredRestoreJobs()
	if err != nil {
		return 0, err
	}

	enqueued := 0
	for _, job := range jobs {
		select {
		case <-ctx.Done():
			return enqueued, ctx.Err()
		default:
		}

		// Delete directly — restore jobs are small metadata
		if err := s.store.DeleteRestoreJob(job.OrgID, job.LibraryID, job.JobID); err == nil {
			enqueued++
		}
	}

	log.Printf("[GC Scanner] Phase 8 complete: cleaned %d expired restore jobs", enqueued)
	metrics.GCItemsEnqueuedTotal.WithLabelValues("expired_restore_jobs").Add(float64(enqueued))
	metrics.GCScannerLastPhaseRun.WithLabelValues("expired_restore_jobs").SetToCurrentTime()
	return enqueued, nil
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
			if err := s.store.DeleteShare(gs.LibraryID, gs.ShareID); err == nil {
				cleaned++
			}
		}
	}

	log.Printf("[GC Scanner] Phase 9 complete: cleaned %d orphaned group shares", cleaned)
	metrics.GCItemsEnqueuedTotal.WithLabelValues("orphaned_group_shares").Add(float64(cleaned))
	metrics.GCScannerLastPhaseRun.WithLabelValues("orphaned_group_shares").SetToCurrentTime()
	return cleaned, nil
}

// scanExpiredDeletedUsers finds soft-deleted users whose grace period has expired
// and enqueues them for cascade deletion.
func (s *Scanner) scanExpiredDeletedUsers(ctx context.Context) (int, error) {
	log.Println("[GC Scanner] Phase 10: Scanning for expired deleted users...")

	users, err := s.store.ListDeletedUsersExpired(s.config.UserGraceDays)
	if err != nil {
		return 0, err
	}

	enqueued := 0
	var batch []QueueItem
	for _, u := range users {
		taskID := scanTaskID("user_cascade", u.OrgID, u.UserID.String(), u.DeletedAt)
		applied, err := s.store.MarkItemProcessed(taskID)
		if err != nil {
			log.Printf("[GC Scanner] Phase 10: failed to dedupe expired deleted user %s: %v", u.UserID, err)
			continue
		}
		if !applied {
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
		} else {
			enqueued = len(batch)
		}
	}

	log.Printf("[GC Scanner] Phase 10 complete: enqueued %d expired deleted users", enqueued)
	metrics.GCItemsEnqueuedTotal.WithLabelValues("expired_deleted_users").Add(float64(enqueued))
	metrics.GCScannerLastPhaseRun.WithLabelValues("expired_deleted_users").SetToCurrentTime()
	return enqueued, nil
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
		taskID := scanTaskID("library_cascade", lib.OrgID, lib.LibraryID.String(), lib.DeletedAt)
		applied, err := s.store.MarkItemProcessed(taskID)
		if err != nil {
			log.Printf("[GC Scanner] Phase 11: failed to dedupe expired deleted library %s: %v", lib.LibraryID, err)
			continue
		}
		if !applied {
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
		taskID := scanTaskID("org_cascade", org.OrgID, org.OrgID.String(), org.DeletedAt)
		applied, err := s.store.MarkItemProcessed(taskID)
		if err != nil {
			log.Printf("[GC Scanner] Phase 12: failed to dedupe expired deleted org %s: %v", org.OrgID, err)
			continue
		}
		if !applied {
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
