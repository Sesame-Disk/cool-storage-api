package v2

import (
	"errors"
	"fmt"
	"hash/fnv"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	gocql "github.com/apache/cassandra-gocql-driver/v2"
)

const (
	publishedBlockReferenceRepairBuckets       = 32
	publishedBlockReferenceRepairSweepInterval = time.Minute
	publishedBlockReferenceRepairStaleAfter    = 30 * time.Second
)

type publishedBlockReferenceRepair struct {
	Bucket    int
	OrgID     string
	RepoID    string
	CommitID  string
	FSID      string
	StagedBlockIDs []string
	CreatedAt time.Time
}

var scheduledPublishedBlockReferenceRepairs sync.Map

var startPublishedBlockReferenceRepairWorkerOnce sync.Once

var schedulePublishedBlockReferenceRepairSleepFn = time.Sleep

var schedulePublishedBlockReferenceRepairRunFn = func(repair func()) {
	go repair()
}

var publishedBlockReferenceRepairNowFn = time.Now

var publishedBlockReferenceRepairTickerFn = func(interval time.Duration) *time.Ticker {
	return time.NewTicker(interval)
}

var insertPublishedBlockReferenceRepairFn = func(database *db.DB, repair publishedBlockReferenceRepair) error {
	if database == nil {
		return fmt.Errorf("database not available")
	}
	return database.Session().Query(`
		INSERT INTO published_block_reference_repairs (bucket, org_id, repo_id, commit_id, fs_id, staged_block_ids, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, repair.Bucket, repair.OrgID, repair.RepoID, repair.CommitID, repair.FSID, repair.StagedBlockIDs, repair.CreatedAt).Exec()
}

var deletePublishedBlockReferenceRepairFn = func(database *db.DB, repair publishedBlockReferenceRepair) error {
	if database == nil {
		return fmt.Errorf("database not available")
	}
	return database.Session().Query(`
		DELETE FROM published_block_reference_repairs
		WHERE bucket = ? AND org_id = ? AND repo_id = ? AND commit_id = ? AND fs_id = ?
	`, repair.Bucket, repair.OrgID, repair.RepoID, repair.CommitID, repair.FSID).Exec()
}

var listPublishedBlockReferenceRepairsForBucketFn = func(database *db.DB, bucket int) ([]publishedBlockReferenceRepair, error) {
	if database == nil {
		return nil, fmt.Errorf("database not available")
	}
	iter := database.Session().Query(`
		SELECT org_id, repo_id, commit_id, fs_id, staged_block_ids, created_at
		FROM published_block_reference_repairs WHERE bucket = ?
	`, bucket).Iter()

	var repairs []publishedBlockReferenceRepair
	var repair publishedBlockReferenceRepair
	for iter.Scan(&repair.OrgID, &repair.RepoID, &repair.CommitID, &repair.FSID, &repair.StagedBlockIDs, &repair.CreatedAt) {
		repair.Bucket = bucket
		repairs = append(repairs, repair)
		repair = publishedBlockReferenceRepair{}
	}
	if err := iter.Close(); err != nil {
		return nil, err
	}
	return repairs, nil
}

var publishedBlockReferenceRepairCommitReachableFn = func(database *db.DB, repoID, commitID string) (bool, error) {
	if database == nil {
		return false, fmt.Errorf("database not available")
	}
	fsHelper := NewFSHelper(database)
	headCommitID, err := fsHelper.GetHeadCommitID(repoID)
	if err != nil {
		if errors.Is(err, gocql.ErrNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("lookup current head for repo %s: %w", repoID, err)
	}
	return onlyOfficeCommitReachable(commitID, headCommitID, func(currentCommitID string) (string, error) {
		var parentCommitID string
		err := database.Session().Query(`
			SELECT parent_id FROM commits WHERE library_id = ? AND commit_id = ?
		`, repoID, currentCommitID).Scan(&parentCommitID)
		if err != nil {
			if errors.Is(err, gocql.ErrNotFound) {
				return "", nil
			}
			return "", fmt.Errorf("lookup parent for commit %s: %w", currentCommitID, err)
		}
		return parentCommitID, nil
	})
}

var loadPublishedBlockReferenceRepairPendingFileFn = func(database *db.DB, repoID, fsID string) (*pendingPublishedFile, error) {
	if database == nil {
		return nil, fmt.Errorf("database not available")
	}
	var blockIDs []string
	err := database.Session().Query(`
		SELECT block_ids FROM fs_objects WHERE library_id = ? AND fs_id = ?
	`, repoID, fsID).Scan(&blockIDs)
	if err != nil {
		return nil, fmt.Errorf("lookup repair fs_object %s/%s: %w", repoID, fsID, err)
	}
	return &pendingPublishedFile{
		fsID:             fsID,
		externalBlockIDs: append([]string(nil), blockIDs...),
	}, nil
}

var publishedBlockReferenceRepairPromoteFn = func(helper *FSHelper, orgID, repoID, commitID string, pending *pendingPublishedFile) error {
	return helper.promotePendingPublishedFiles(orgID, repoID, commitID, []*pendingPublishedFile{pending})
}

var publishedBlockReferenceRepairCleanupFn = func(database *db.DB, orgID, repoID, commitID string, blockIDs []string) error {
	return db.CleanupFailedPublishAttempt(database, orgID, repoID, commitID, commitID, blockIDs)
}

func publishedBlockReferenceRepairBucket(orgID, repoID, commitID, fsID string) int {
	if publishedBlockReferenceRepairBuckets <= 1 {
		return 0
	}
	hasher := fnv.New32a()
	for _, part := range []string{orgID, repoID, commitID, fsID} {
		_, _ = hasher.Write([]byte(part))
		_, _ = hasher.Write([]byte{0})
	}
	return int(hasher.Sum32() % uint32(publishedBlockReferenceRepairBuckets))
}

func newPublishedBlockReferenceRepair(orgID, repoID, commitID, fsID string, stagedBlockIDs []string) publishedBlockReferenceRepair {
	orgID = strings.TrimSpace(orgID)
	repoID = strings.TrimSpace(repoID)
	commitID = strings.TrimSpace(commitID)
	fsID = strings.TrimSpace(fsID)
	return publishedBlockReferenceRepair{
		Bucket:    publishedBlockReferenceRepairBucket(orgID, repoID, commitID, fsID),
		OrgID:     orgID,
		RepoID:    repoID,
		CommitID:  commitID,
		FSID:      fsID,
		StagedBlockIDs: append([]string(nil), db.NormalizeBlockIDs(stagedBlockIDs)...),
		CreatedAt: publishedBlockReferenceRepairNowFn().UTC(),
	}
}

func shouldQueuePublishedBlockReferenceRepair(fsID string, blockIDs []string) bool {
	return strings.TrimSpace(fsID) != "" && len(blockIDs) > 0
}

func queuePublishedBlockReferenceRepair(database *db.DB, repair publishedBlockReferenceRepair) error {
	if strings.TrimSpace(repair.CommitID) == "" || strings.TrimSpace(repair.FSID) == "" {
		return nil
	}
	return insertPublishedBlockReferenceRepairFn(database, repair)
}

func QueuePublishedFSObjectBlockReferenceRepair(database *db.DB, orgID, repoID, commitID, fsID string, stagedBlockIDs []string) error {
	return queuePublishedBlockReferenceRepair(database, newPublishedBlockReferenceRepair(orgID, repoID, commitID, fsID, stagedBlockIDs))
}

func ClearPublishedFSObjectBlockReferenceRepair(database *db.DB, orgID, repoID, commitID, fsID string) error {
	repair := newPublishedBlockReferenceRepair(orgID, repoID, commitID, fsID, nil)
	if strings.TrimSpace(repair.CommitID) == "" || strings.TrimSpace(repair.FSID) == "" {
		return nil
	}
	return deletePublishedBlockReferenceRepairFn(database, repair)
}

func publishedBlockReferenceRepairKey(repoID, commitID, fsID string) string {
	return strings.TrimSpace(repoID) + ":" + strings.TrimSpace(commitID) + ":" + strings.TrimSpace(fsID)
}

func rollbackQueuedPublishedBlockReferenceRepairs(database *db.DB, inserted []publishedBlockReferenceRepair, stageErr error) error {
	if len(inserted) == 0 {
		return stageErr
	}
	var cleanupErr error
	for _, repair := range inserted {
		if err := deletePublishedBlockReferenceRepairFn(database, repair); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("delete queued publish repair %s/%s/%s: %w", repair.RepoID, repair.CommitID, repair.FSID, err))
		}
	}
	if cleanupErr != nil {
		return errors.Join(stageErr, cleanupErr)
	}
	return stageErr
}

func queuePendingPublishedFileRepairs(database *db.DB, orgID, repoID, commitID string, pendingFiles []*pendingPublishedFile) error {
	queued := make([]publishedBlockReferenceRepair, 0, len(pendingFiles))
	for _, pending := range pendingFiles {
		if pending == nil || !shouldQueuePublishedBlockReferenceRepair(pending.fsID, pending.internalBlockIDs) {
			continue
		}
		repair := newPublishedBlockReferenceRepair(orgID, repoID, commitID, pending.fsID, pending.internalBlockIDs)
		if err := queuePublishedBlockReferenceRepair(database, repair); err != nil {
			return rollbackQueuedPublishedBlockReferenceRepairs(database, append(append([]publishedBlockReferenceRepair(nil), queued...), repair), fmt.Errorf("queue publish repair for fs_object %s: %w", pending.fsID, err))
		}
		queued = append(queued, repair)
	}
	return nil
}

func clearPendingPublishedFileRepairs(database *db.DB, orgID, repoID, commitID string, pendingFiles []*pendingPublishedFile) error {
	var clearErr error
	for _, pending := range pendingFiles {
		if pending == nil || !shouldQueuePublishedBlockReferenceRepair(pending.fsID, pending.internalBlockIDs) {
			continue
		}
		if err := ClearPublishedFSObjectBlockReferenceRepair(database, orgID, repoID, commitID, pending.fsID); err != nil {
			clearErr = errors.Join(clearErr, fmt.Errorf("clear queued publish repair for fs_object %s: %w", pending.fsID, err))
		}
	}
	return clearErr
}

func SchedulePublishedFSObjectBlockReferenceRepair(database *db.DB, orgID, repoID, commitID, fsID, label string, stagedBlockIDs []string) {
	if !shouldQueuePublishedBlockReferenceRepair(fsID, stagedBlockIDs) || strings.TrimSpace(commitID) == "" {
		return
	}
	SchedulePublishedBlockReferenceRepair(publishedBlockReferenceRepairKey(repoID, commitID, fsID), label, func() error {
		return RepairPublishedFSObjectBlockReferenceRepair(database, orgID, repoID, commitID, fsID, stagedBlockIDs)
	})
}

func schedulePendingPublishedFileRepairs(database *db.DB, orgID, repoID, commitID string, pendingFiles []*pendingPublishedFile, label string) {
	keyParts := []string{strings.TrimSpace(repoID), strings.TrimSpace(commitID)}
	for _, pending := range pendingFiles {
		if pending == nil || !shouldQueuePublishedBlockReferenceRepair(pending.fsID, pending.internalBlockIDs) {
			continue
		}
		keyParts = append(keyParts, strings.TrimSpace(pending.fsID))
	}
	SchedulePublishedBlockReferenceRepair(strings.Join(keyParts, ":"), label, func() error {
		var repairErr error
		for _, pending := range pendingFiles {
			if pending == nil || !shouldQueuePublishedBlockReferenceRepair(pending.fsID, pending.internalBlockIDs) {
				continue
			}
			if err := RepairPublishedFSObjectBlockReferenceRepair(database, orgID, repoID, commitID, pending.fsID, pending.internalBlockIDs); err != nil {
				repairErr = errors.Join(repairErr, fmt.Errorf("repair queued publish refs for fs_object %s: %w", pending.fsID, err))
			}
		}
		return repairErr
	})
}

func RepairPublishedFSObjectBlockReferenceRepair(database *db.DB, orgID, repoID, commitID, fsID string, stagedBlockIDs []string) error {
	repair := newPublishedBlockReferenceRepair(orgID, repoID, commitID, fsID, stagedBlockIDs)
	if !shouldQueuePublishedBlockReferenceRepair(repair.FSID, repair.StagedBlockIDs) || strings.TrimSpace(repair.CommitID) == "" {
		return nil
	}
	if len(repair.StagedBlockIDs) == 0 {
		return fmt.Errorf("queued publish repair for fs_object %s has no staged block IDs", repair.FSID)
	}
	commitReachable, err := publishedBlockReferenceRepairCommitReachableFn(database, repair.RepoID, repair.CommitID)
	if err != nil {
		return err
	}
	if commitReachable {
		pending, err := loadPublishedBlockReferenceRepairPendingFileFn(database, repair.RepoID, repair.FSID)
		if err != nil {
			return err
		}
		pending.internalBlockIDs = append([]string(nil), repair.StagedBlockIDs...)
		helper := NewFSHelper(database)
		if err := publishedBlockReferenceRepairPromoteFn(helper, repair.OrgID, repair.RepoID, repair.CommitID, pending); err != nil {
			return fmt.Errorf("promote published fs_object %s for commit %s: %w", repair.FSID, repair.CommitID, err)
		}
	} else {
		if err := publishedBlockReferenceRepairCleanupFn(database, repair.OrgID, repair.RepoID, repair.CommitID, repair.StagedBlockIDs); err != nil {
			return fmt.Errorf("cleanup unreachable published fs_object %s for commit %s: %w", repair.FSID, repair.CommitID, err)
		}
	}
	if err := deletePublishedBlockReferenceRepairFn(database, repair); err != nil {
		return fmt.Errorf("delete queued publish repair for fs_object %s: %w", repair.FSID, err)
	}
	return nil
}

func runPublishedBlockReferenceRepairSweep(database *db.DB) error {
	if database == nil {
		return nil
	}
	cutoff := publishedBlockReferenceRepairNowFn().Add(-publishedBlockReferenceRepairStaleAfter)
	var firstErr error
	for bucket := 0; bucket < publishedBlockReferenceRepairBuckets; bucket++ {
		repairs, err := listPublishedBlockReferenceRepairsForBucketFn(database, bucket)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("list queued publish repairs for bucket %d: %w", bucket, err)
			}
			continue
		}
		for _, repair := range repairs {
			if !repair.CreatedAt.IsZero() && repair.CreatedAt.After(cutoff) {
				continue
			}
			if err := RepairPublishedFSObjectBlockReferenceRepair(database, repair.OrgID, repair.RepoID, repair.CommitID, repair.FSID, repair.StagedBlockIDs); err != nil {
				log.Printf("[publish_repair] queued repair failed for repo=%s commit=%s fs_object=%s: %v", repair.RepoID, repair.CommitID, repair.FSID, err)
				if firstErr == nil {
					firstErr = err
				}
			}
		}
	}
	return firstErr
}

func StartPublishedBlockReferenceRepairer(database *db.DB) {
	if database == nil {
		return
	}
	startPublishedBlockReferenceRepairWorkerOnce.Do(func() {
		schedulePublishedBlockReferenceRepairRunFn(func() {
			if err := runPublishedBlockReferenceRepairSweep(database); err != nil {
				log.Printf("[publish_repair] initial sweep failed: %v", err)
			}
			ticker := publishedBlockReferenceRepairTickerFn(publishedBlockReferenceRepairSweepInterval)
			defer ticker.Stop()
			for range ticker.C {
				if err := runPublishedBlockReferenceRepairSweep(database); err != nil {
					log.Printf("[publish_repair] periodic sweep failed: %v", err)
				}
			}
		})
	})
}

// SchedulePublishedBlockReferenceRepair runs one additional repair pass for a
// published fs_object's block references outside the request path. repairKey
// deduplicates concurrent schedulers for the same publish attempt.
func SchedulePublishedBlockReferenceRepair(repairKey, label string, repair func() error) {
	repairKey = strings.TrimSpace(repairKey)
	if repairKey == "" || repair == nil {
		return
	}
	if _, loaded := scheduledPublishedBlockReferenceRepairs.LoadOrStore(repairKey, struct{}{}); loaded {
		return
	}
	schedulePublishedBlockReferenceRepairRunFn(func() {
		defer scheduledPublishedBlockReferenceRepairs.Delete(repairKey)
		sleepFor := RetryBackoff(1)
		if sleepFor > 0 {
			schedulePublishedBlockReferenceRepairSleepFn(sleepFor)
		}
		if err := repair(); err != nil {
			log.Printf("[%s] WARNING: background block-reference repair failed for %s: %v", label, repairKey, err)
			return
		}
		log.Printf("[%s] INFO: background block-reference repair completed for %s", label, repairKey)
	})
}
