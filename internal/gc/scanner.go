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
	gocql "github.com/apache/cassandra-gocql-driver/v2"
	"github.com/google/uuid"
)

// OrphanRecoverer retries S3 deletes for blocks whose DB rows are gone but
// whose S3 objects linger. Implemented by *Worker.
type OrphanRecoverer interface {
	RecoverS3Orphans(ctx context.Context, perBucketLimit int) (int, error)
}

// OnlyOfficeReconciler reconciles stale onlyoffice_pending_blocks rows for an
// org: it either drops rows whose publish commit is reachable from the
// current library head or rolls back materialized blocks for rows that were
// never published. Wired in from the API layer because the implementation
// lives there alongside the OnlyOffice handler.
type OnlyOfficeReconciler interface {
	ReconcileOnlyOfficePendingBlocks(orgID uuid.UUID) error
}

// Scanner periodically finds orphaned items that were missed by inline enqueue
// and adds them to the gc_queue for processing.
type Scanner struct {
	store                GCStore
	queue                *Queue
	stats                *Stats
	config               config.GCConfig
	orphanRecoverer      OrphanRecoverer
	onlyOfficeReconciler OnlyOfficeReconciler
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

// ScanExpiredProvisionalBlockRefsOnce runs only the provisional-reference
// recovery phase. Integration tests use this narrow entry point so they can
// exercise the production Cassandra store without advancing unrelated scanner
// cursors or mutating unrelated GC state.
func (s *Scanner) ScanExpiredProvisionalBlockRefsOnce(ctx context.Context) (int, error) {
	return s.scanExpiredProvisionalBlockRefs(ctx)
}

// ScanExpiredDeletedLibrariesOnce runs only the deleted-library expiry phase.
// Integration tests use this narrow entry point to avoid mutating unrelated
// scanner cursors/global state via a full ScanOnce pass.
func (s *Scanner) ScanExpiredDeletedLibrariesOnce(ctx context.Context) (int, error) {
	return s.scanExpiredDeletedLibraries(ctx)
}

// SetOrphanRecoverer wires the S3 orphan recovery dependency. Optional; if
// unset, the s3_orphan_recovery phase is a no-op (useful for mock-only tests).
func (s *Scanner) SetOrphanRecoverer(r OrphanRecoverer) {
	s.orphanRecoverer = r
}

// SetOnlyOfficeReconciler wires the OnlyOffice pending-blocks reconciler.
// Optional; if unset, the onlyoffice_pending_blocks phase is a no-op (the
// inline reconcile in saveEditedDocument is still the primary cleanup
// trigger).
func (s *Scanner) SetOnlyOfficeReconciler(r OnlyOfficeReconciler) {
	s.onlyOfficeReconciler = r
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

func (s *Scanner) pendingWorkExists(orgID, libraryID uuid.UUID, itemType ItemType, itemID string) (bool, error) {
	return s.store.PendingItemExists(orgID, libraryID, time.Time{}, itemType, itemID)
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
		{"expired_provisional_block_refs", s.scanExpiredProvisionalBlockRefs},
		{"orphaned_blocks", s.scanOrphanedBlocks},
		{"expired_links", s.scanExpiredShareLinks},
		{"orphaned_commits", s.scanOrphanedCommits},
		{"orphaned_fs_objects", s.scanOrphanedFSObjects},
		{"expired_versions", s.scanExpiredVersions},
		{"auto_delete", s.scanAutoDeleteExpiredObjects},
		{"expired_shares", s.scanExpiredShares},
		{"expired_restore_jobs", s.scanExpiredRestoreJobs},
		{"orphaned_group_shares", s.scanOrphanedGroupShares},
		{"expired_failed_items", s.scanExpiredFailedItems},
		{"expired_deleted_users", s.scanExpiredDeletedUsers},
		{"storage_counter_reconciliation", s.scanPendingStorageCounterReconciliation},
		{"expired_deleted_libraries", s.scanExpiredDeletedLibraries},
		{"expired_deleted_orgs", s.scanExpiredDeletedOrgs},
		{"onlyoffice_pending_blocks", s.scanOnlyOfficePendingBlocks},
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
	completedAt := time.Now()
	s.stats.SetLastScanAttempt(completedAt)
	if scanErr == nil {
		s.stats.SetLastScanSuccess(completedAt)
	}
	return scanErr
}

func (s *Scanner) scanPendingStorageCounterReconciliation(ctx context.Context) (int, error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}

	log.Println("[GC Scanner] Phase 12: Reconciling pending storage counters...")
	reconciled, err := s.store.ReconcilePendingStorageCounters()
	if err != nil {
		return reconciled, err
	}
	log.Printf("[GC Scanner] Phase 12 complete: reconciled %d storage counter scopes", reconciled)
	metrics.GCScannerLastPhaseRun.WithLabelValues("storage_counter_reconciliation").SetToCurrentTime()
	return 0, nil
}

// promoteBlockIfUnreferenced runs the zero-reference transition for a block whose
// provisional pin is provably gone. Both Phase 0 paths that reach that conclusion
// use it, so neither can quietly skip the promotion — the transition has exactly one
// discovery window and nothing re-opens it once the tracking rows are swept.
func (s *Scanner) promoteBlockIfUnreferenced(orgID uuid.UUID, blockID, storageClass string, now time.Time) error {
	hasRefs, err := s.store.BlockHasReferences(orgID, blockID)
	if err != nil {
		return fmt.Errorf("check refs after provisional expiry org=%s block=%s: %w", orgID, blockID, err)
	}
	if hasRefs {
		return nil
	}
	if _, err := s.store.EnsureBlockGCCandidateExact(orgID, blockID, storageClass, now); err != nil {
		if errors.Is(err, ErrBlockCandidateTargetUnavailable) {
			// Nothing reclaimable: the block has no canonical row, or none with a usable
			// locator. Reporting that as a phase error would keep the expiry projection
			// row alive and re-fail on it every Phase 0 cycle, forever, over a block that
			// has nothing to collect.
			metrics.GCBlockCandidateDiscoveryDegradedTotal.WithLabelValues("scanner").Inc()
			log.Printf("[GC Scanner] Phase 0: block %s in org=%s has nothing reclaimable (%v); resolving the expired provisional ref without a candidate", blockID, orgID, err)
			return nil
		}
		return fmt.Errorf("promote expired provisional ref org=%s block=%s into gc candidate: %w", orgID, blockID, err)
	}
	return nil
}

// scanExpiredProvisionalBlockRefs processes abandoned provisional upload refs by
// specific referrer and promotes any block their expiry left at zero references
// into gc_block_candidates. Canonical trackers retire only through their TTL;
// this phase never mutates them and sweeps only resolved discovery projections.
//
// It does NOT delete the reference row. That row carries a Cassandra TTL derived
// from the very deadline being tracked, so it expires on its own; the phase waits
// for that and acts only on what it observes afterwards. Deleting it here was F9:
// an upload can renew a reference between this scan's read and its delete, and
// removing a renewed reference unpins a live upload — after which the block goes
// to zero references and GC is free to delete data the uploader is still writing.
// Records whose reference has not yet gone are deferred to a later cycle, and the
// day cursor is held back so a deferred record stays discoverable.
func (s *Scanner) scanExpiredProvisionalBlockRefs(ctx context.Context) (int, error) {
	log.Println("[GC Scanner] Phase 0: Scanning for expired provisional upload refs...")

	now := time.Now().UTC()
	cutoffDay := db.GCProjectionUTCDate(now)
	startDay, err := s.loadProvisionalBlockRefsStartDay(cutoffDay)
	if err != nil {
		return 0, err
	}
	if startDay.After(cutoffDay) {
		log.Printf("[GC Scanner] Phase 0 complete: cursor ahead of cutoff, nothing to scan")
		metrics.GCScannerLastPhaseRun.WithLabelValues("expired_provisional_block_refs").SetToCurrentTime()
		return 0, nil
	}

	cleaned := 0
	// deferred counts records this pass deliberately left in place because their
	// specific provisional reference is still live; oldestDeferredDay keeps the
	// cursor from stepping past the day that holds one, which would make it
	// undiscoverable.
	deferred := 0
	var oldestDeferredDay time.Time
	var phaseErr error
	for day := startDay; !day.After(cutoffDay); day = day.AddDate(0, 0, 1) {
		for bucket := 0; bucket < db.GCDiscoveryBucketCount; bucket++ {
			select {
			case <-ctx.Done():
				return cleaned, ctx.Err()
			default:
			}

			expiries, err := s.store.ListProvisionalBlockRefExpiriesByDay(day, bucket)
			if err != nil {
				log.Printf("[GC Scanner] Phase 0: failed to list provisional expiries for day=%s bucket=%d: %v", db.GCProjectionDateString(day), bucket, err)
				if phaseErr == nil {
					phaseErr = fmt.Errorf("list provisional expiries for day=%s bucket=%d: %w", db.GCProjectionDateString(day), bucket, err)
				}
				continue
			}

			for _, expiry := range expiries {
				if expiry.ExpiresAt.After(now) {
					continue
				}

				canonical, found, err := s.store.GetProvisionalBlockRefExpiry(expiry.OrgID, expiry.BlockID, expiry.Referrer)
				if err != nil {
					log.Printf("[GC Scanner] Phase 0: failed to load canonical provisional expiry org=%s block=%s referrer=%s: %v", expiry.OrgID, expiry.BlockID, expiry.Referrer, err)
					if phaseErr == nil {
						phaseErr = fmt.Errorf("load canonical provisional expiry org=%s block=%s referrer=%s: %w", expiry.OrgID, expiry.BlockID, expiry.Referrer, err)
					}
					continue
				}

				// Check the exact provisional reference before classifying either a
				// canonical-present or canonical-missing projection. This must also run
				// for stale projections left by renewal: a logged batch is atomic but
				// not isolated, and sweeping a projection while its live reference is
				// visible can erase the upload's only durable retry anchor.
				refPresent, err := s.store.BlockReferenceExists(expiry.OrgID, expiry.BlockID, expiry.Referrer)
				if err != nil {
					log.Printf("[GC Scanner] Phase 0: failed to check provisional ref presence org=%s block=%s referrer=%s: %v", expiry.OrgID, expiry.BlockID, expiry.Referrer, err)
					if phaseErr == nil {
						phaseErr = fmt.Errorf("check provisional ref presence org=%s block=%s referrer=%s: %w", expiry.OrgID, expiry.BlockID, expiry.Referrer, err)
					}
					continue
				}
				if refPresent {
					deferred++
					if oldestDeferredDay.IsZero() || day.Before(oldestDeferredDay) {
						oldestDeferredDay = day
					}
					continue
				}

				storageClass := expiry.StorageClass
				if found && canonical.StorageClass != "" {
					storageClass = canonical.StorageClass
				}

				// The specific provisional pin is gone, so resolve whole-block
				// liveness through the shared helper. If no references remain, the GC
				// candidate is durably persisted before the projection is removed.
				// Any read/write failure leaves the projection and cursor untouched for
				// a later scan.
				if err := s.promoteBlockIfUnreferenced(expiry.OrgID, expiry.BlockID, storageClass, now); err != nil {
					log.Printf("[GC Scanner] Phase 0: failed to resolve expired provisional ref org=%s block=%s referrer=%s: %v", expiry.OrgID, expiry.BlockID, expiry.Referrer, err)
					if phaseErr == nil {
						phaseErr = err
					}
					continue
				}

				// Delete only the processed discovery row. The canonical tracker is
				// never mutated by Phase 0; it retires exclusively through Cassandra
				// TTL, avoiding tracker LWT/CAS races with renewal.
				if err := s.store.DeleteProvisionalBlockRefExpiryProjection(expiry.OrgID, expiry.BlockID, expiry.Referrer, expiry.ExpiresAt); err != nil {
					log.Printf("[GC Scanner] Phase 0: failed to delete resolved provisional expiry projection org=%s block=%s referrer=%s: %v", expiry.OrgID, expiry.BlockID, expiry.Referrer, err)
					if phaseErr == nil {
						phaseErr = fmt.Errorf("delete resolved provisional expiry projection org=%s block=%s referrer=%s: %w", expiry.OrgID, expiry.BlockID, expiry.Referrer, err)
					}
					continue
				}
				cleaned++
			}
		}
	}

	if phaseErr == nil {
		newCursor := cutoffDay.AddDate(0, 0, -1)
		// Never advance past a day still holding a deferred record. The cursor is
		// what bounds future scans, so moving beyond an unprocessed day would drop
		// that record out of every later pass — a leak with no discovery path left.
		if !oldestDeferredDay.IsZero() && oldestDeferredDay.Before(newCursor) {
			newCursor = oldestDeferredDay
		}
		if !newCursor.Before(startDay) {
			if err := s.store.SaveGCStats(gcProvisionalBlockRefsCursorKey, db.GCProjectionDateString(newCursor)); err != nil {
				log.Printf("[GC Scanner] Phase 0: failed to persist provisional block ref cursor: %v", err)
				phaseErr = fmt.Errorf("persist provisional block ref cursor: %w", err)
			}
		}
	}

	log.Printf("[GC Scanner] Phase 0 complete: cleaned %d provisional upload refs, deferred %d tracked", cleaned, deferred)
	recordScannerAction("expired_provisional_block_refs", "cleaned", cleaned)
	// Deferrals are expected during the bounded interval in which the canonical
	// tracker deliberately outlives the reference. A count that stays high beyond
	// that margin means cleanup is not converging and the day cursor remains pinned.
	recordScannerAction("expired_provisional_block_refs", "deferred", deferred)
	metrics.GCScannerLastPhaseRun.WithLabelValues("expired_provisional_block_refs").SetToCurrentTime()
	return cleaned, phaseErr
}

// scanOrphanedBlocks re-enqueues zero-ref block candidates that should still
// be in GC. Walks the `gc_block_candidates_by_day` discovery projection from a
// persisted day cursor up to today, across all discovery buckets, instead of
// scanning the canonical `gc_block_candidates` partition by org. The
// per-org partition scan disappeared with the schema move to per-block
// partitioning; blocks now enter GC either from explicit zero-ref enqueue paths
// or from the provisional-upload expiry scan above.
func (s *Scanner) scanOrphanedBlocks(ctx context.Context) (int, error) {
	log.Println("[GC Scanner] Phase 1: Scanning for orphaned blocks...")

	cutoffDay := db.GCProjectionUTCDate(time.Now())
	startDay, err := s.loadBlockCandidatesStartDay(cutoffDay)
	if err != nil {
		return 0, err
	}
	if startDay.After(cutoffDay) {
		log.Printf("[GC Scanner] Phase 1 complete: cursor ahead of cutoff, nothing to scan")
		metrics.GCScannerLastPhaseRun.WithLabelValues("orphaned_blocks").SetToCurrentTime()
		return 0, nil
	}

	enqueued := 0
	var phaseErr error
	for day := startDay; !day.After(cutoffDay); day = day.AddDate(0, 0, 1) {
		for bucket := 0; bucket < db.GCDiscoveryBucketCount; bucket++ {
			select {
			case <-ctx.Done():
				return enqueued, ctx.Err()
			default:
			}

			candidates, err := s.store.ListBlockGCCandidatesByDay(day, bucket)
			if err != nil {
				log.Printf("[GC Scanner] Phase 1: failed to list candidates for day=%s bucket=%d: %v", db.GCProjectionDateString(day), bucket, err)
				if phaseErr == nil {
					phaseErr = fmt.Errorf("list block candidates for day=%s bucket=%d: %w", db.GCProjectionDateString(day), bucket, err)
				}
				continue
			}

			var batch []QueueItem
			for _, candidate := range candidates {
				exists, err := s.store.PendingItemExists(candidate.OrgID, uuid.Nil, candidate.CandidateAt, ItemBlock, candidate.BlockID, candidate.Identity())
				if err != nil {
					log.Printf("[GC Scanner] Phase 1: failed to inspect queue for block %s in org %s: %v", candidate.BlockID, candidate.OrgID, err)
					if phaseErr == nil {
						phaseErr = fmt.Errorf("inspect queue for block %s in org %s: %w", candidate.BlockID, candidate.OrgID, err)
					}
					continue
				}
				if exists {
					continue
				}
				batch = append(batch, QueueItem{
					OrgID:                    candidate.OrgID,
					QueuedAt:                 candidate.CandidateAt,
					IdentityAt:               candidate.CandidateAt,
					ItemType:                 ItemBlock,
					ItemID:                   candidate.BlockID,
					LibraryID:                uuid.Nil,
					StorageClass:             candidate.StorageClass(),
					BlockGCCandidateIdentity: candidate.Identity(),
				})
			}
			if len(batch) > 0 {
				if err := s.queue.EnqueueBatch(batch); err != nil {
					log.Printf("[GC Scanner] Phase 1: failed to batch enqueue blocks for day=%s bucket=%d: %v", db.GCProjectionDateString(day), bucket, err)
					if phaseErr == nil {
						phaseErr = fmt.Errorf("enqueue blocks for day=%s bucket=%d: %w", db.GCProjectionDateString(day), bucket, err)
					}
				} else {
					enqueued += len(batch)
				}
			}
		}
	}

	if phaseErr == nil {
		// Advance cursor to today-1 so a same-day late-arriving candidate still
		// gets picked up on the next pass (gcScanOverlapDays-style overlap).
		newCursor := cutoffDay.AddDate(0, 0, -1)
		if !newCursor.Before(startDay) {
			if err := s.store.SaveGCStats(gcBlockCandidatesCursorKey, db.GCProjectionDateString(newCursor)); err != nil {
				log.Printf("[GC Scanner] Phase 1: failed to persist block candidates cursor: %v", err)
				phaseErr = fmt.Errorf("persist block candidates cursor: %w", err)
			}
		}
	}

	log.Printf("[GC Scanner] Phase 1 complete: enqueued %d orphaned blocks", enqueued)
	metrics.GCItemsEnqueuedTotal.WithLabelValues("orphaned_blocks").Add(float64(enqueued))
	metrics.GCScannerLastPhaseRun.WithLabelValues("orphaned_blocks").SetToCurrentTime()
	return enqueued, phaseErr
}

// loadBlockCandidatesStartDay reads the persisted cursor day and returns the
// next day to start scanning from. On cold start (no cursor) it falls back to
// `cutoffDay - gcInitialScanLookbackDays` so we still catch recently created
// candidates after a fresh deploy.
func (s *Scanner) loadBlockCandidatesStartDay(cutoffDay time.Time) (time.Time, error) {
	value, err := s.store.LoadGCStats(gcBlockCandidatesCursorKey)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return cutoffDay.AddDate(0, 0, -gcInitialScanLookbackDays), nil
		}
		return time.Time{}, err
	}
	lastDay, err := db.ParseGCProjectionDate(value)
	if err != nil {
		return time.Time{}, err
	}
	return lastDay.AddDate(0, 0, -gcScanOverlapDays), nil
}

func (s *Scanner) loadProvisionalBlockRefsStartDay(cutoffDay time.Time) (time.Time, error) {
	value, err := s.store.LoadGCStats(gcProvisionalBlockRefsCursorKey)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return cutoffDay.AddDate(0, 0, -gcInitialScanLookbackDays), nil
		}
		return time.Time{}, err
	}
	lastDay, err := db.ParseGCProjectionDate(value)
	if err != nil {
		return time.Time{}, err
	}
	return lastDay.AddDate(0, 0, -gcScanOverlapDays), nil
}

func (s *Scanner) loadFailedItemsExpiryStartDay(cutoffDay time.Time) (time.Time, error) {
	value, err := s.store.LoadGCStats(gcFailedItemsExpiryCursorKey)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return cutoffDay.AddDate(0, 0, -gcFailedItemExpiryInitialLookbackDays), nil
		}
		return time.Time{}, err
	}
	lastDay, err := db.ParseGCProjectionDate(value)
	if err != nil {
		return time.Time{}, err
	}
	return lastDay.AddDate(0, 0, -gcScanOverlapDays), nil
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
	var phaseErr error
	for _, libID := range libraryIDs {
		select {
		case <-ctx.Done():
			return enqueued, ctx.Err()
		default:
		}

		// Fail closed: a LibraryExists error must NOT be treated as "library gone"
		// (that would enqueue a live library's commits for deletion). Skip and surface.
		exists, err := s.store.LibraryExists(libID)
		if err != nil {
			log.Printf("[GC Scanner] Phase 3: library existence check failed for %s; skipping: %v", libID, err)
			if phaseErr == nil {
				phaseErr = err
			}
			continue
		}
		if exists {
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
		blockRepresentationID, repErr := resolveRequiredLibraryBlockRepresentation(s.store, orgID, libID, "", "orphaned commit scan")
		if repErr != nil {
			log.Printf("[GC Scanner] Phase 3: skipping library %s: %v", libID, repErr)
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
				exists, err := s.pendingWorkExists(orgID, libID, ItemCommit, commitID)
				if err != nil {
					log.Printf("[GC Scanner] Phase 3: failed to inspect pending commit %s for library %s: %v", commitID, libID, err)
					continue
				}
				if exists {
					continue
				}
				batch = append(batch, QueueItem{
					OrgID:      orgID,
					QueuedAt:   now,
					IdentityAt: now,
					// Re-validate against the canonical libraries table at execution time
					// (P6b): the library was absent at scan time, but the worker must
					// re-confirm it is still gone before deleting its content, in case it
					// was restored/recreated or the projection drifted between scan and run.
					RequiresLibraryDeletedCheck: true,
					LibraryGuardMode:            LibraryGuardCanonicalMustBeAbsent,
					ItemType:                    ItemCommit,
					ItemID:                      commitID,
					LibraryID:                   libID,
					BlockRepresentationID:       blockRepresentationID,
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
	return enqueued, phaseErr
}

// scanOrphanedFSObjects finds fs_objects whose library no longer exists.
func (s *Scanner) scanOrphanedFSObjects(ctx context.Context) (int, error) {
	log.Println("[GC Scanner] Phase 4: Scanning for orphaned fs_objects...")

	libraryIDs, err := s.store.ListDistinctFSObjectLibraries()
	if err != nil {
		return 0, err
	}

	enqueued := 0
	var phaseErr error
	for _, libID := range libraryIDs {
		select {
		case <-ctx.Done():
			return enqueued, ctx.Err()
		default:
		}

		// Fail closed: a LibraryExists error must NOT be treated as "library gone"
		// (that would enqueue a live library's fs_objects for deletion). Skip and surface.
		exists, err := s.store.LibraryExists(libID)
		if err != nil {
			log.Printf("[GC Scanner] Phase 4: library existence check failed for %s; skipping: %v", libID, err)
			if phaseErr == nil {
				phaseErr = err
			}
			continue
		}
		if exists {
			continue
		}

		orgID, err := s.store.FindOrgForLibrary(libID)
		if err != nil || orgID == uuid.Nil {
			log.Printf("[GC Scanner] Phase 4: Library %s deleted, org lookup failed, skipping", libID)
			continue
		}
		blockRepresentationID, repErr := resolveRequiredLibraryBlockRepresentation(s.store, orgID, libID, "", "orphaned fs_object scan")
		if repErr != nil {
			log.Printf("[GC Scanner] Phase 4: skipping library %s: %v", libID, repErr)
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
				exists, err := s.pendingWorkExists(orgID, libID, ItemFSObject, fsID)
				if err != nil {
					log.Printf("[GC Scanner] Phase 4: failed to inspect pending fs_object %s for library %s: %v", fsID, libID, err)
					continue
				}
				if exists {
					continue
				}
				batch = append(batch, QueueItem{
					OrgID:      orgID,
					QueuedAt:   now,
					IdentityAt: now,
					// Re-validate against the canonical libraries table at execution time (P6b).
					RequiresLibraryDeletedCheck: true,
					LibraryGuardMode:            LibraryGuardCanonicalMustBeAbsent,
					ItemType:                    ItemFSObject,
					ItemID:                      fsID,
					LibraryID:                   libID,
					BlockRepresentationID:       blockRepresentationID,
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
	return enqueued, phaseErr
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

		if lib.RepresentationInvalid {
			countLibraryRepresentationDrift(lib.BlockRepresentationID)
			log.Printf("[GC Scanner] Phase 5: skipping library %s: stored block_representation_id %q is invalid for the library identity/encryption state", lib.LibraryID, lib.BlockRepresentationID)
			continue
		}
		if err := validateQueueItemBlockRepresentation(QueueItem{
			ItemType:              ItemCommit,
			ItemID:                lib.HeadCommitID,
			LibraryID:             lib.LibraryID,
			BlockRepresentationID: lib.BlockRepresentationID,
		}); err != nil {
			countLibraryRepresentationDrift(lib.BlockRepresentationID)
			log.Printf("[GC Scanner] Phase 5: skipping library %s: %v", lib.LibraryID, err)
			continue
		}
		if lib.RepresentationDefaulted {
			countLibraryRepresentationDefaulted()
			log.Printf("[GC Scanner] Phase 5: library %s has no stored block_representation_id; processing under derived default %q (drift)", lib.LibraryID, lib.BlockRepresentationID)
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
				exists, err := s.pendingWorkExists(lib.OrgID, lib.LibraryID, ItemCommit, c.CommitID)
				if err != nil {
					log.Printf("[GC Scanner] Phase 5: failed to inspect pending commit %s for library %s: %v", c.CommitID, lib.LibraryID, err)
					continue
				}
				if exists {
					continue
				}
				batch = append(batch, QueueItem{
					OrgID:                 lib.OrgID,
					QueuedAt:              now,
					IdentityAt:            now,
					ItemType:              ItemCommit,
					ItemID:                c.CommitID,
					LibraryID:             lib.LibraryID,
					BlockRepresentationID: lib.BlockRepresentationID,
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

		if lib.RepresentationInvalid {
			countLibraryRepresentationDrift(lib.BlockRepresentationID)
			log.Printf("[GC Scanner] Phase 6: skipping library %s: stored block_representation_id %q is invalid for the library identity/encryption state", lib.LibraryID, lib.BlockRepresentationID)
			continue
		}
		if err := validateQueueItemBlockRepresentation(QueueItem{
			ItemType:              ItemFSObject,
			ItemID:                lib.HeadCommitID,
			LibraryID:             lib.LibraryID,
			BlockRepresentationID: lib.BlockRepresentationID,
		}); err != nil {
			countLibraryRepresentationDrift(lib.BlockRepresentationID)
			log.Printf("[GC Scanner] Phase 6: skipping library %s: %v", lib.LibraryID, err)
			continue
		}
		if lib.RepresentationDefaulted {
			countLibraryRepresentationDefaulted()
			log.Printf("[GC Scanner] Phase 6: library %s has no stored block_representation_id; processing under derived default %q (drift)", lib.LibraryID, lib.BlockRepresentationID)
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
		keepSetIncomplete := false
		for commitID := range keepCommits {
			if c, ok := commitMap[commitID]; ok && c.RootFSID != "" {
				if err := s.walkFSTree(lib.LibraryID, c.RootFSID, keepFSSet); err != nil {
					log.Printf("[GC Scanner] Phase 6: skipping auto-delete for library %s after keep-tree read failure at commit %s root %s: %v", lib.LibraryID, commitID, c.RootFSID, err)
					keepSetIncomplete = true
					break
				}
			}
		}
		if keepSetIncomplete {
			continue
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
				exists, err := s.pendingWorkExists(lib.OrgID, lib.LibraryID, ItemFSObject, fsID)
				if err != nil {
					log.Printf("[GC Scanner] Phase 6: failed to inspect pending fs_object %s for library %s: %v", fsID, lib.LibraryID, err)
					continue
				}
				if exists {
					continue
				}
				batch = append(batch, QueueItem{
					OrgID:                 lib.OrgID,
					QueuedAt:              now,
					IdentityAt:            now,
					ItemType:              ItemFSObject,
					ItemID:                fsID,
					LibraryID:             lib.LibraryID,
					BlockRepresentationID: lib.BlockRepresentationID,
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

	// A single-entry "last partition" cache opportunistically reuses the GroupExists
	// result (or error) for consecutive rows of the same (org_id, group_id) partition,
	// which is how a shares_by_group scan normally returns them. Memory stays O(1) and
	// correctness does not depend on scan ordering: if a partition reappears later,
	// Phase 9 simply performs another lookup — never a wrong answer.
	type groupExistenceKey struct {
		orgID   uuid.UUID
		groupID uuid.UUID
	}
	var (
		lastKey    groupExistenceKey
		lastExists bool
		lastErr    error
		haveLast   bool
	)

	cleaned := 0
	failed := 0
	var phaseErr error
	scanErr := s.store.ScanAllGroupShares(ctx, func(gs GroupShareInfo) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Resolve the group's org. Prefer the org_id carried by the group-share
		// projection row; only
		// fall back to the library→org projection when it is absent, and surface that
		// fallback failure instead of silently skipping a share we could not classify.
		orgID := gs.OrgID
		if orgID == uuid.Nil {
			resolvedOrgID, err := s.store.FindOrgForLibrary(gs.LibraryID)
			if err != nil || resolvedOrgID == uuid.Nil {
				log.Printf("[GC Scanner] Phase 9: cannot resolve org for group share %s (library %s); skipping: %v", gs.ShareID, gs.LibraryID, err)
				failed++
				if phaseErr == nil {
					if err != nil {
						phaseErr = err
					} else {
						phaseErr = fmt.Errorf("cannot resolve org for library %s", gs.LibraryID)
					}
				}
				return nil
			}
			orgID = resolvedOrgID
		}

		cacheKey := groupExistenceKey{orgID: orgID, groupID: gs.SharedTo}
		if !haveLast || cacheKey != lastKey {
			// Look up (and cache) the group's existence for this partition. Cache the
			// error too, so a transient GroupExists failure is not re-issued and
			// re-logged once per share in a large partition.
			lastExists, lastErr = s.store.GroupExists(orgID, gs.SharedTo)
			lastKey = cacheKey
			haveLast = true
			if lastErr != nil {
				log.Printf("[GC Scanner] Phase 9: group existence check failed for group %s (library %s); skipping this partition's shares: %v", gs.SharedTo, gs.LibraryID, lastErr)
				if phaseErr == nil {
					phaseErr = lastErr
				}
			}
		}
		if lastErr != nil {
			// Fail closed: a GroupExists error must NOT be read as "group gone",
			// which would delete a valid share. Count each deferred share; the next
			// scanner cycle retries the partition.
			failed++
			return nil
		}

		if !lastExists {
			// Group deleted — clean up the orphaned share
			if err := s.store.DeleteShare(gs.LibraryID, gs.ShareID); err != nil {
				log.Printf("[GC Scanner] Phase 9: failed to delete orphaned group share %s for library %s: %v", gs.ShareID, gs.LibraryID, err)
				failed++
				if phaseErr == nil {
					phaseErr = err
				}
				return nil
			}
			cleaned++
		}
		return nil
	})
	if scanErr != nil {
		phaseErr = errors.Join(phaseErr, scanErr)
	}

	log.Printf("[GC Scanner] Phase 9 complete: cleaned %d orphaned group shares", cleaned)
	recordScannerAction("orphaned_group_shares", "cleaned", cleaned)
	recordScannerAction("orphaned_group_shares", "failed", failed)
	metrics.GCScannerLastPhaseRun.WithLabelValues("orphaned_group_shares").SetToCurrentTime()
	return cleaned, phaseErr
}

// scanExpiredFailedItems expires DLQ rows through the store rather than relying
// on Cassandra TTL. This keeps failed-depth counters and pending markers in sync
// with the primary gc_failed_items row.
func (s *Scanner) scanExpiredFailedItems(ctx context.Context) (int, error) {
	log.Println("[GC Scanner] Phase 10: Expiring GC failed items...")

	now := time.Now().UTC()
	cutoffDay := db.GCProjectionUTCDate(now)
	startDay, err := s.loadFailedItemsExpiryStartDay(cutoffDay)
	if err != nil {
		return 0, err
	}
	if startDay.After(cutoffDay) {
		log.Printf("[GC Scanner] Phase 10 complete: cursor ahead of cutoff, nothing to expire")
		metrics.GCScannerLastPhaseRun.WithLabelValues("expired_failed_items").SetToCurrentTime()
		return 0, nil
	}

	expired := 0
	projections := 0
	failed := 0
	var phaseErr error
	for day := startDay; !day.After(cutoffDay); day = day.AddDate(0, 0, 1) {
		for bucket := 0; bucket < db.GCDiscoveryBucketCount; bucket++ {
			select {
			case <-ctx.Done():
				return expired, ctx.Err()
			default:
			}

			expiries, err := s.store.ListFailedItemExpiriesByDay(day, bucket)
			if err != nil {
				log.Printf("[GC Scanner] Phase 10: failed to list failed-item expiries for day=%s bucket=%d: %v", db.GCProjectionDateString(day), bucket, err)
				failed++
				if phaseErr == nil {
					phaseErr = fmt.Errorf("list failed-item expiries for day=%s bucket=%d: %w", db.GCProjectionDateString(day), bucket, err)
				}
				continue
			}

			for _, expiry := range expiries {
				if expiry.ExpiresAt.After(now) {
					continue
				}
				deleted, err := s.store.DeleteExpiredFailedItem(expiry, now)
				if err != nil {
					log.Printf("[GC Scanner] Phase 10: failed to expire failed item org=%s item_type=%s item_id=%s failed_at=%s: %v",
						expiry.OrgID, expiry.ItemType, expiry.ItemID, expiry.FailedAt.Format(time.RFC3339Nano), err)
					failed++
					if phaseErr == nil {
						phaseErr = fmt.Errorf("expire failed item org=%s item=%s: %w", expiry.OrgID, expiry.ItemID, err)
					}
					continue
				}
				projections++
				if deleted {
					expired++
				}
			}
		}
	}

	if phaseErr == nil {
		newCursor := cutoffDay.AddDate(0, 0, -1)
		if !newCursor.Before(startDay) {
			if err := s.store.SaveGCStats(gcFailedItemsExpiryCursorKey, db.GCProjectionDateString(newCursor)); err != nil {
				log.Printf("[GC Scanner] Phase 10: failed to persist failed-item expiry cursor: %v", err)
				phaseErr = fmt.Errorf("persist failed-item expiry cursor: %w", err)
			}
		}
	}

	log.Printf("[GC Scanner] Phase 10 complete: expired %d failed items, touched %d projections", expired, projections)
	recordScannerAction("expired_failed_items", "expired", expired)
	recordScannerAction("expired_failed_items", "failed", failed)
	metrics.GCScannerLastPhaseRun.WithLabelValues("expired_failed_items").SetToCurrentTime()
	return expired, phaseErr
}

// scanExpiredDeletedUsers finds soft-deleted users whose grace period has expired
// and enqueues them for cascade deletion.
func (s *Scanner) scanExpiredDeletedUsers(ctx context.Context) (int, error) {
	log.Println("[GC Scanner] Phase 11: Scanning for expired deleted users...")

	now := time.Now()
	users, err := s.store.ListDeletedUsersExpired(s.config.UserGraceDays)
	if err != nil {
		return 0, err
	}

	enqueued := 0
	var batch []QueueItem
	var phaseErr error
	for _, u := range users {
		exists, err := s.store.PendingItemExists(u.OrgID, uuid.Nil, u.DeletedAt, ItemUserCascade, u.UserID.String())
		if err != nil {
			log.Printf("[GC Scanner] Phase 11: failed to dedupe expired deleted user %s: %v", u.UserID, err)
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
			log.Printf("[GC Scanner] Phase 11: failed to enqueue expired deleted users: %v", err)
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

	log.Printf("[GC Scanner] Phase 11 complete: enqueued %d expired deleted users", enqueued)
	metrics.GCItemsEnqueuedTotal.WithLabelValues("expired_deleted_users").Add(float64(enqueued))
	metrics.GCScannerLastPhaseRun.WithLabelValues("expired_deleted_users").SetToCurrentTime()
	return enqueued, phaseErr
}

// scanExpiredDeletedLibraries finds soft-deleted libraries whose trash retention
// period has expired and enqueues them for cascade deletion (commits, fs_objects,
// blocks, and all library artifacts).
func (s *Scanner) scanExpiredDeletedLibraries(ctx context.Context) (int, error) {
	log.Println("[GC Scanner] Phase 13: Scanning for expired deleted libraries...")

	libs, err := s.store.ListExpiredDeletedLibraries(s.config.TrashRetentionDays)
	if err != nil {
		return 0, err
	}

	enqueued := 0
	var batch []QueueItem
	for _, lib := range libs {
		exists, err := s.store.PendingItemExists(lib.OrgID, uuid.Nil, lib.DeletedAt, ItemLibraryCascade, lib.LibraryID.String())
		if err != nil {
			log.Printf("[GC Scanner] Phase 13: failed to dedupe expired deleted library %s: %v", lib.LibraryID, err)
			continue
		}
		if exists {
			continue
		}
		// Resolve (do not just validate) the representation: most production
		// soft-delete/permanent-delete paths write the deleted_libraries marker
		// without copying block_representation_id, so lib.BlockRepresentationID is
		// usually empty here. The library row still exists at soft-delete time and
		// carries the authoritative value, so recover it from there and stamp it on
		// the cascade — mirroring phases 3/4 — instead of skipping the library and
		// stranding it in trash forever. Only a genuinely unresolvable library
		// (row gone AND marker empty) is skipped and reported as drift.
		blockRepresentationID, repErr := resolveRequiredLibraryBlockRepresentation(s.store, lib.OrgID, lib.LibraryID, lib.BlockRepresentationID, "expired deleted library scan")
		if repErr != nil {
			log.Printf("[GC Scanner] Phase 13: skipping deleted library %s: %v", lib.LibraryID, repErr)
			continue
		}
		if lib.BlockRepresentationID == "" {
			// The deleted_libraries marker was written without a representation and
			// we recovered it from the surviving library row; surface that as drift
			// so the unstamped write path stays visible.
			countLibraryRepresentationDefaulted()
			log.Printf("[GC Scanner] Phase 13: deleted library %s had no stamped block_representation_id; recovered %q from library row (drift)", lib.LibraryID, blockRepresentationID)
		}
		batch = append(batch, QueueItem{
			OrgID:                 lib.OrgID,
			QueuedAt:              lib.DeletedAt,
			ItemType:              ItemLibraryCascade,
			ItemID:                lib.LibraryID.String(),
			BlockRepresentationID: blockRepresentationID,
			StorageClass:          lib.StorageClass,
		})
	}
	if len(batch) > 0 {
		if err := s.queue.EnqueueBatch(batch); err != nil {
			log.Printf("[GC Scanner] Phase 13: failed to enqueue expired deleted libraries: %v", err)
		} else {
			enqueued = len(batch)
		}
	}

	log.Printf("[GC Scanner] Phase 13 complete: enqueued %d expired deleted libraries", enqueued)
	metrics.GCItemsEnqueuedTotal.WithLabelValues("expired_deleted_libraries").Add(float64(enqueued))
	metrics.GCScannerLastPhaseRun.WithLabelValues("expired_deleted_libraries").SetToCurrentTime()
	return enqueued, nil
}

// scanExpiredDeletedOrgs finds soft-deleted organizations whose grace period
// has expired and enqueues them for cascade deletion (users, libraries, groups).
func (s *Scanner) scanExpiredDeletedOrgs(ctx context.Context) (int, error) {
	log.Println("[GC Scanner] Phase 14: Scanning for expired deleted organizations...")

	orgs, err := s.store.ListExpiredDeletedOrgs(s.config.OrgGraceDays)
	if err != nil {
		return 0, err
	}

	enqueued := 0
	var batch []QueueItem
	for _, org := range orgs {
		exists, err := s.store.PendingItemExists(org.OrgID, uuid.Nil, org.DeletedAt, ItemOrgCascade, org.OrgID.String())
		if err != nil {
			log.Printf("[GC Scanner] Phase 14: failed to dedupe expired deleted org %s: %v", org.OrgID, err)
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
			log.Printf("[GC Scanner] Phase 14: failed to enqueue expired deleted orgs: %v", err)
		} else {
			enqueued = len(batch)
		}
	}

	log.Printf("[GC Scanner] Phase 14 complete: enqueued %d expired deleted organizations", enqueued)
	metrics.GCItemsEnqueuedTotal.WithLabelValues("expired_deleted_orgs").Add(float64(enqueued))
	metrics.GCScannerLastPhaseRun.WithLabelValues("expired_deleted_orgs").SetToCurrentTime()
	return enqueued, nil
}

// scanOnlyOfficePendingBlocks reconciles stale onlyoffice_pending_blocks
// rows org-by-org. The inline trigger in saveEditedDocument only fires when
// someone saves a new revision in that library, so without this scanner
// phase a library that loses OnlyOffice traffic could retain pending rows
// (and their materialized blocks) indefinitely. Reconciliation itself is
// idempotent — DecrementBlockRefCountsOnce protects against double-decrement
// across the inline and scanner trigger paths.
func (s *Scanner) scanOnlyOfficePendingBlocks(ctx context.Context) (int, error) {
	log.Println("[GC Scanner] Phase 15: Reconciling OnlyOffice pending blocks...")
	if s.onlyOfficeReconciler == nil {
		return 0, nil
	}

	orgs, err := s.store.ListOrganizations()
	if err != nil {
		return 0, fmt.Errorf("list organizations: %w", err)
	}

	reconciled := 0
	var firstErr error
	for _, orgID := range orgs {
		select {
		case <-ctx.Done():
			return reconciled, ctx.Err()
		default:
		}

		if err := s.onlyOfficeReconciler.ReconcileOnlyOfficePendingBlocks(orgID); err != nil {
			log.Printf("[GC Scanner] Phase 15: reconcile org %s: %v", orgID, err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		reconciled++
	}

	log.Printf("[GC Scanner] Phase 15 complete: reconciled %d organizations", reconciled)
	recordScannerAction("onlyoffice_pending_blocks", "reconciled", reconciled)
	metrics.GCScannerLastPhaseRun.WithLabelValues("onlyoffice_pending_blocks").SetToCurrentTime()
	return reconciled, firstErr
}

// scanS3OrphanRecovery retries S3 deletes for blocks whose DB rows were
// removed successfully but whose S3 objects lingered because DeleteBlock
// failed after the LWT step (see docs/GC-SERVICE-ANALYSIS.md).
func (s *Scanner) scanS3OrphanRecovery(ctx context.Context) (int, error) {
	log.Println("[GC Scanner] Phase 16: Recovering S3 orphans...")
	if s.orphanRecoverer == nil {
		return 0, nil
	}
	recovered, err := s.orphanRecoverer.RecoverS3Orphans(ctx, 500)
	if err != nil {
		log.Printf("[GC Scanner] Phase 16: recovery error: %v", err)
	}
	log.Printf("[GC Scanner] Phase 16 complete: recovered %d S3 orphans", recovered)
	recordScannerAction("s3_orphan_recovery", "recovered", recovered)
	metrics.GCScannerLastPhaseRun.WithLabelValues("s3_orphan_recovery").SetToCurrentTime()
	return recovered, err
}

// walkFSTree iteratively walks a filesystem tree starting from fsID,
// adding all visited fs_ids to the visited set. Uses an explicit stack
// instead of recursion to avoid stack overflow on deep directory trees.
func (s *Scanner) walkFSTree(libraryID uuid.UUID, fsID string, visited map[string]bool) error {
	if fsID == "" || visited[fsID] {
		return nil
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
			return fmt.Errorf("load fs_object %s for library %s: %w", current, libraryID, err)
		}

		// Push children
		for _, childID := range obj.DirEntries {
			if !visited[childID] {
				stack = append(stack, childID)
			}
		}
	}

	return nil
}
